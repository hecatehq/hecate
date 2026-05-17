package agentadapters

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	acp "github.com/coder/acp-go-sdk"
)

// thoughtFallbackBlockID is the per-turn sentinel prefix used
// when ACP omits messageId on an `agent_thought_chunk`. Fallback
// activity rows come out as `thinking:__fallback-<n>` where <n>
// is a counter that increments once per boundary-triggered
// fallback episode in the turn. The leading double-underscore
// guarantees we never collide with a spec-conformant ACP
// messageId — ACP requires messageIds to be UUIDs (per the SDK
// comment on SessionUpdateAgentThoughtChunk.MessageId), and
// UUIDs are hex+dashes only, so an underscore-led prefix cannot
// appear in any spec-conformant id.
//
// The counter exists because mergeChatActivity dedupes by
// Activity.ID and *replaces* Detail wholesale on merge: if two
// distinct fallback episodes shared one id (e.g. an adapter
// mixing messageId-bearing and no-id chunks like
// fallback → real → empty), the second episode's Detail would
// overwrite the first's, silently losing the earlier reasoning.
// Counter-suffixed ids keep each episode on its own row.
//
// Within one fallback episode, all chunks reuse the same
// counter value and merge naturally (continuation case in
// appendAgentThoughtChunk).
//
// Boundary detection itself does NOT sniff this id: see
// `agentThoughtFallback` for the source of truth that a buggy
// adapter cannot impersonate.
const thoughtFallbackBlockID = "__fallback"

// thoughtMaxBytesPerBlock caps the per-block thought accumulator.
// Each chunk of an `agent_thought_chunk` stream re-emits the full
// accumulated `Detail` (mergeChatActivity replaces the row's
// Detail wholesale by ID), so an unbounded accumulator would
// inflate the persisted activities JSON and the websocket payload
// with every chunk. 32 KiB is comfortably above any practical
// thought size while keeping the worst-case row small. When the
// cap is hit, `Detail` is suffixed with a truncation marker so
// operators can see that they are looking at a partial reasoning
// block, not the whole thing.
const thoughtMaxBytesPerBlock = 32 * 1024

// thoughtTruncationSuffix is appended to a thought activity's
// Detail when its accumulator hits thoughtMaxBytesPerBlock.
const thoughtTruncationSuffix = "\n… (thought truncated)"

type acpTurn struct {
	output         limitedBuffer
	raw            limitedBuffer
	usage          Usage
	agentMessageID string
	// agentThoughtID is the ACP messageId carrying the current
	// `agent_thought_chunk` block. ACP emits thoughts as a chunk
	// stream, sharing a messageId across chunks of the same thought
	// and bumping it when a new thought block starts. We use it to
	// keep one merged "thinking" activity per block instead of one
	// per chunk; mergeChatActivity dedupes by Activity.ID
	// downstream.
	agentThoughtID string
	// agentThoughtFallback is the source of truth for whether the
	// active block was opened with a Hecate-minted fallback id.
	// Boundary detection used to sniff the id's prefix, which a
	// non-spec-conformant adapter could spoof by sending a real
	// messageId that happened to look like the fallback shape;
	// tracking the property explicitly removes that risk.
	agentThoughtFallback bool
	// agentThoughtFallbackCount counts boundary-triggered fallback
	// episodes within this turn so each gets a unique Activity.ID
	// (`__fallback-1`, `__fallback-2`, …). See thoughtFallbackBlockID
	// for why uniqueness is load-bearing on the merge path.
	agentThoughtFallbackCount int
	agentThoughtText          strings.Builder
	agentThoughtTruncated     bool
	// toolKindByCall caches the last-known ToolKind for each
	// ToolCallId in this turn. ACP `SessionToolCallUpdate.Kind` is
	// optional — adapters may emit a kind on the initial ToolCall
	// and omit it on the matching completion update. Without the
	// cache, emitFileChangeActivities would compute kind == "" on
	// the completion update and skip per-file emission for an edit
	// that genuinely happened.
	toolKindByCall map[string]acp.ToolKind
	toolSeen       bool
	postToolText   bool
	onOutput       func(string)
	onActivity     func(Activity)

	mu sync.Mutex
}

func newACPTurn(maxOutput int64, onOutput func(string)) *acpTurn {
	if maxOutput <= 0 {
		maxOutput = 1024 * 1024
	}
	turn := &acpTurn{onOutput: onOutput}
	turn.output.limit = maxOutput
	turn.raw.limit = maxOutput
	return turn
}

func (t *acpTurn) setActivityCallback(onActivity func(Activity)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onActivity = onActivity
}

func (t *acpTurn) recordUpdate(params acp.SessionNotification) {
	raw, _ := json.Marshal(params)
	if len(raw) > 0 {
		t.appendRaw(append(raw, '\n'))
	}
	update := params.Update
	switch {
	case update.AgentMessageChunk != nil:
		t.appendAgentMessageChunk(update.AgentMessageChunk)
	case update.AgentThoughtChunk != nil:
		t.appendAgentThoughtChunk(update.AgentThoughtChunk)
	case update.ToolCall != nil:
		t.recordToolCall(update.ToolCall)
	case update.ToolCallUpdate != nil:
		t.recordToolCallUpdate(update.ToolCallUpdate)
	case update.Plan != nil:
		t.recordPlan(update.Plan)
	case update.UsageUpdate != nil:
		t.recordUsage(update.UsageUpdate)
	}
}

func (t *acpTurn) appendAgentMessageChunk(update *acp.SessionUpdateAgentMessageChunk) {
	if update == nil {
		return
	}
	text := contentBlockText(update.Content)
	if text == "" {
		return
	}

	var snapshot string
	t.mu.Lock()
	if t.toolSeen && !t.postToolText && isLikelyProgressNarration(t.output.String()) {
		t.output.Buffer.Reset()
		t.postToolText = true
	}
	if update.MessageId != nil {
		nextID := strings.TrimSpace(*update.MessageId)
		if nextID != "" {
			// ACP messageId is unstable but specifically exists to mark message
			// boundaries. Codex sends short progress narration as one assistant
			// message and the actual answer as another; when the id changes, the
			// transcript should follow the latest visible assistant message.
			if t.agentMessageID != "" && t.agentMessageID != nextID {
				t.output.Buffer.Reset()
			}
			t.agentMessageID = nextID
		}
	}
	_, _ = t.output.Write([]byte(text))
	snapshot = t.output.String()
	t.mu.Unlock()

	if t.onOutput != nil {
		t.onOutput(snapshot)
	}
}

// appendAgentThoughtChunk routes ACP `agent_thought_chunk` updates
// to the activity stream as `thinking` records. Thoughts are
// internal reasoning, not visible transcript text — `output` stays
// untouched. ACP streams a thought block as multiple chunks sharing
// a `messageId`; we accumulate chunks per messageId and emit one
// activity row per block, refreshing its `Detail` as new chunks
// arrive (mergeChatActivity dedupes by Activity.ID downstream).
// Block boundaries are detected by the four-case transition table
// inside the function (real → real, real → empty, empty → real,
// continuation); the goal is that Activity.ID stays stable for the
// lifetime of every emitted block.
func (t *acpTurn) appendAgentThoughtChunk(update *acp.SessionUpdateAgentThoughtChunk) {
	if update == nil {
		return
	}
	text := contentBlockText(update.Content)
	if text == "" {
		return
	}

	t.mu.Lock()
	nextID := ""
	if update.MessageId != nil {
		nextID = strings.TrimSpace(*update.MessageId)
	}
	// Resolve the active block id with explicit boundary detection.
	// Each transition is decided by what we know now vs. what was
	// active before — the goal is that Activity.ID is stable for the
	// lifetime of every emitted block (mergeChatActivity dedupes
	// by id downstream, so a mid-block id flip would split one
	// thought into two timeline rows or — worse — silently merge two
	// thoughts into one row).
	//
	// Cases:
	//   1. First chunk in the turn: adopt the real id when present;
	//      otherwise mint a counter-suffixed fallback id.
	//   2. Real id that differs from the active id: real-A → real-B
	//      is an explicit ACP-level block boundary.
	//   3. Empty id while a *real* id is active: real-A → ∅. Treat as
	//      a new block (defensive — adapters that consistently send
	//      messageIds shouldn't drop them mid-block, so an absence
	//      after a real id is more plausibly a new block than a
	//      continuation of the old one). Mint the next fallback
	//      counter so the new row never collides on Activity.ID
	//      with a prior fallback episode in the same turn —
	//      mergeChatActivity replaces Detail wholesale on
	//      collision, so reusing an id would silently lose the
	//      earlier episode's reasoning.
	//   4. Empty id while a *fallback* id is active, OR matching
	//      real id, OR matching fallback id: continuation; same row.
	blockChanged := false
	switch {
	case t.agentThoughtID == "":
		blockChanged = true
		if nextID != "" {
			t.agentThoughtID = nextID
			t.agentThoughtFallback = false
		} else {
			t.agentThoughtFallbackCount++
			t.agentThoughtID = fmt.Sprintf("%s-%d", thoughtFallbackBlockID, t.agentThoughtFallbackCount)
			t.agentThoughtFallback = true
		}
	case nextID != "" && nextID != t.agentThoughtID:
		blockChanged = true
		t.agentThoughtID = nextID
		t.agentThoughtFallback = false
	case nextID == "" && !t.agentThoughtFallback:
		blockChanged = true
		t.agentThoughtFallbackCount++
		t.agentThoughtID = fmt.Sprintf("%s-%d", thoughtFallbackBlockID, t.agentThoughtFallbackCount)
		t.agentThoughtFallback = true
	}
	if blockChanged {
		t.agentThoughtText.Reset()
		t.agentThoughtTruncated = false
	}
	t.appendBoundedThoughtText(text)
	id := t.agentThoughtID
	detail := t.agentThoughtText.String()
	if t.agentThoughtTruncated {
		detail += thoughtTruncationSuffix
	}
	t.mu.Unlock()

	t.emitActivity(Activity{
		ID:     "thinking:" + id,
		Type:   "thinking",
		Status: "completed",
		Title:  "Thinking",
		Detail: detail,
	})
}

// appendBoundedThoughtText writes text into the thought
// accumulator, stopping at thoughtMaxBytesPerBlock. The cut is
// rolled back to the nearest UTF-8 rune boundary so the
// JSON-serialized Activity.Detail stays valid (slicing mid-rune
// would emit a stray continuation byte). Once the block is
// truncated, further chunks for the same block are dropped on the
// floor — the suffix in Detail tells the operator the row is
// partial; appending more truncated bytes would not help.
//
// Caller MUST hold t.mu.
func (t *acpTurn) appendBoundedThoughtText(text string) {
	if t.agentThoughtTruncated {
		return
	}
	remaining := thoughtMaxBytesPerBlock - t.agentThoughtText.Len()
	if remaining <= 0 {
		t.agentThoughtTruncated = true
		return
	}
	if len(text) <= remaining {
		t.agentThoughtText.WriteString(text)
		return
	}
	cut := remaining
	for cut > 0 && (text[cut]&0xC0) == 0x80 {
		cut--
	}
	if cut > 0 {
		t.agentThoughtText.WriteString(text[:cut])
	}
	t.agentThoughtTruncated = true
}

func (t *acpTurn) recordToolCall(update *acp.SessionUpdateToolCall) {
	if update == nil {
		return
	}
	t.markToolSeen()
	status := acpToolStatus(string(update.Status))
	t.rememberToolKind(string(update.ToolCallId), update.Kind)
	t.emitActivity(Activity{
		ID:     "tool:" + string(update.ToolCallId),
		Type:   "tool_call",
		Status: status,
		Kind:   string(update.Kind),
		Title:  firstNonEmpty(update.Title, string(update.ToolCallId)),
		Detail: toolCallDetail(update.Kind, update.Locations, update.Content, update.RawInput),
	})
	t.emitFileChangeActivities(string(update.ToolCallId), update.Kind, status, update.Locations)
}

func (t *acpTurn) recordToolCallUpdate(update *acp.SessionToolCallUpdate) {
	if update == nil {
		return
	}
	t.markToolSeen()
	title := ""
	if update.Title != nil {
		title = *update.Title
	}
	// SessionToolCallUpdate.Title is optional. mergeChatActivity
	// drops an emission whose Title is empty when there is no prior
	// row with the same Activity.ID to merge into — that loses tool-call
	// state updates that arrive before (or instead of) a matching
	// SessionUpdateToolCall (e.g. an adapter that sends the start
	// event in a previous turn but a status update now). Default the
	// Title to the ToolCallId — the same fallback recordToolCall uses
	// at the start side — so the activity always carries something
	// renderable and never gets silently dropped on the merge path.
	if title == "" {
		title = string(update.ToolCallId)
	}
	status := ""
	if update.Status != nil {
		status = string(*update.Status)
	}
	// SessionToolCallUpdate.Kind is optional. Adapters routinely
	// emit kind on the initial ToolCall and drop it on the
	// completion update. Without the per-turn cache, a completed
	// edit whose update omits Kind would compute kind == "" and
	// skip emitFileChangeActivities — silently losing the per-file
	// rows for an edit that actually happened. Update the cache
	// when Kind is present so a later in_progress → completed
	// transition resolves correctly even if the adapter changes
	// its mind about the tool's category.
	var kind acp.ToolKind
	if update.Kind != nil {
		kind = *update.Kind
		t.rememberToolKind(string(update.ToolCallId), kind)
	} else {
		kind = t.lookupToolKind(string(update.ToolCallId))
	}
	normalizedStatus := acpToolStatus(status)
	t.emitActivity(Activity{
		ID:     "tool:" + string(update.ToolCallId),
		Type:   "tool_call",
		Status: normalizedStatus,
		Kind:   string(kind),
		Title:  title,
		Detail: toolCallDetail(kind, update.Locations, update.Content, update.RawInput),
	})
	t.emitFileChangeActivities(string(update.ToolCallId), kind, normalizedStatus, update.Locations)
}

// rememberToolKind caches the latest known kind for a tool call so
// a later update that omits SessionToolCallUpdate.Kind can still
// resolve the right category. Acquires t.mu internally — call
// sites MUST NOT hold t.mu (sync.Mutex is non-reentrant; reentry
// deadlocks). recordToolCall and recordToolCallUpdate match this
// pattern: they extract fields from the ACP update without holding
// t.mu and let each helper (markToolSeen, rememberToolKind,
// lookupToolKind, emitActivity) lock-and-release internally.
func (t *acpTurn) rememberToolKind(toolCallID string, kind acp.ToolKind) {
	if toolCallID == "" || kind == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.toolKindByCall == nil {
		t.toolKindByCall = make(map[string]acp.ToolKind)
	}
	t.toolKindByCall[toolCallID] = kind
}

// lookupToolKind returns the cached kind for a tool call, or the
// zero value if no kind was ever cached. Acquires t.mu internally;
// call sites MUST NOT hold t.mu (see rememberToolKind for the
// rationale).
func (t *acpTurn) lookupToolKind(toolCallID string) acp.ToolKind {
	if toolCallID == "" {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.toolKindByCall[toolCallID]
}

// emitFileChangeActivities surfaces per-file edits as their own
// activity records when a mutating tool call (kind = edit / delete /
// move) reaches the completed state. Today the UI synthesises a
// single end-of-turn `files_changed` summary from the captured Git
// diff stat (see handler_agent_chat.go); the activity-stream
// counterparts let operators see *which* files were touched as the
// agent works, not only after the turn settles. The diff-stat
// aggregate keeps its role — it covers the case where the adapter
// edits files outside an ACP-reported location.
//
// IDs are scoped per (tool_call, path) so duplicate updates from the
// same tool reach the same activity row in mergeChatActivity
// instead of stacking. Read / search / execute / fetch / think /
// other tool kinds are NOT promoted — they don't change files.
func (t *acpTurn) emitFileChangeActivities(toolCallID string, kind acp.ToolKind, status string, locations []acp.ToolCallLocation) {
	if status != "completed" {
		return
	}
	if !isFileMutatingToolKind(kind) {
		return
	}
	// Aggregate by path so that multiple ToolCallLocation entries for
	// the same file (e.g. several edited line ranges in one call)
	// collapse to a single activity row instead of colliding on a
	// shared Activity.ID — mergeChatActivity dedupes by ID
	// downstream, so two emissions with the same id would overwrite
	// each other's title and timestamp instead of stacking. We retain
	// insertion order: the first time we see a path defines its row's
	// position, and subsequent same-path entries fold their line
	// numbers into the existing accumulator.
	type pathAccum struct {
		path  string
		lines []int
	}
	seen := make(map[string]int, len(locations))
	accums := make([]pathAccum, 0, len(locations))
	for _, loc := range locations {
		path := strings.TrimSpace(loc.Path)
		if path == "" {
			continue
		}
		idx, ok := seen[path]
		if !ok {
			seen[path] = len(accums)
			idx = len(accums)
			accums = append(accums, pathAccum{path: path})
		}
		if loc.Line != nil && *loc.Line > 0 {
			accums[idx].lines = append(accums[idx].lines, *loc.Line)
		}
	}
	for _, acc := range accums {
		title := acc.path
		switch len(acc.lines) {
		case 0:
			// No line info — title is just the path.
		case 1:
			title = fmt.Sprintf("%s:%d", acc.path, acc.lines[0])
		default:
			title = fmt.Sprintf("%s (%s)", acc.path, summarizeFileChangeLines(acc.lines))
		}
		t.emitActivity(Activity{
			ID:     "file_change:" + toolCallID + ":" + acc.path,
			Type:   "file_change",
			Status: "completed",
			Kind:   string(kind),
			Title:  title,
			Detail: string(kind),
		})
	}
}

// summarizeFileChangeLines renders a comma-separated list of the
// first few line numbers, with a "+N more" tail when an edit touches
// many ranges in the same file. Mirrors the bounded summary style
// used by summarizeToolLocations so file_change titles read
// consistently with the underlying tool_call detail.
func summarizeFileChangeLines(lines []int) string {
	const maxShown = 3
	limit := len(lines)
	if limit > maxShown {
		limit = maxShown
	}
	parts := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		parts = append(parts, strconv.Itoa(lines[i]))
	}
	out := strings.Join(parts, ", ")
	if len(lines) > maxShown {
		out = fmt.Sprintf("%s, +%d more", out, len(lines)-maxShown)
	}
	return out
}

func isFileMutatingToolKind(kind acp.ToolKind) bool {
	switch kind {
	case acp.ToolKindEdit, acp.ToolKindDelete, acp.ToolKindMove:
		return true
	default:
		return false
	}
}

func (t *acpTurn) markToolSeen() {
	var snapshot *string
	t.mu.Lock()
	t.toolSeen = true
	if !t.postToolText && t.output.Len() > 0 && isLikelyProgressNarration(t.output.String()) {
		t.output.Buffer.Reset()
		empty := ""
		snapshot = &empty
	}
	onOutput := t.onOutput
	t.mu.Unlock()
	if snapshot != nil && onOutput != nil {
		onOutput(*snapshot)
	}
}

func isLikelyProgressNarration(text string) bool {
	text = strings.TrimSpace(strings.ToLower(text))
	if text == "" {
		return false
	}
	prefixes := []string{
		"i'll ",
		"i’ll ",
		"i will ",
		"i'm going to ",
		"i’m going to ",
		"i’m checking ",
		"i'm checking ",
		"i’ll check ",
		"i'll check ",
		"i’ll inspect ",
		"i'll inspect ",
		"let me ",
		"checking ",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func (t *acpTurn) recordPlan(update *acp.SessionUpdatePlan) {
	if update == nil {
		return
	}
	for index, entry := range update.Entries {
		t.emitActivity(Activity{
			ID:     fmt.Sprintf("plan:%d:%s", index, entry.Content),
			Type:   "plan",
			Status: string(entry.Status),
			Kind:   string(entry.Priority),
			Title:  entry.Content,
			Detail: string(entry.Priority),
		})
	}
}

func (t *acpTurn) emitActivity(activity Activity) {
	if strings.TrimSpace(activity.ID) == "" && strings.TrimSpace(activity.Title) == "" {
		return
	}
	t.mu.Lock()
	onActivity := t.onActivity
	t.mu.Unlock()
	if onActivity != nil {
		onActivity(activity)
	}
}

func acpToolStatus(status string) string {
	switch strings.TrimSpace(status) {
	case string(acp.ToolCallStatusPending):
		return "pending"
	case string(acp.ToolCallStatusInProgress):
		return "running"
	case string(acp.ToolCallStatusCompleted):
		return "completed"
	case string(acp.ToolCallStatusFailed):
		return "failed"
	default:
		return strings.TrimSpace(status)
	}
}

func toolCallDetail(kind acp.ToolKind, locations []acp.ToolCallLocation, content []acp.ToolCallContent, rawInput any) string {
	parts := make([]string, 0, 3)
	if kind != "" {
		parts = append(parts, string(kind))
	}
	if summary := summarizeToolRawInput(rawInput); summary != "" {
		parts = append(parts, summary)
	}
	if len(locations) > 0 {
		parts = append(parts, summarizeToolLocations(locations))
	}
	if len(content) > 0 {
		if summary := summarizeToolContent(content); summary != "" {
			parts = append(parts, summary)
		}
	}
	return strings.Join(parts, " · ")
}

func summarizeToolRawInput(rawInput any) string {
	if rawInput == nil {
		return ""
	}
	flattened := flattenRawInput(rawInput)
	for _, key := range []string{"command", "cmd", "shell_command", "script", "query", "path"} {
		if value := firstRawInputValue(flattened, key); value != "" {
			return trimToolSummary(value)
		}
	}
	if value := firstRawInputString(rawInput); value != "" {
		return trimToolSummary(value)
	}
	return ""
}

func flattenRawInput(value any) map[string]string {
	out := map[string]string{}
	var visit func(prefix string, current any)
	visit = func(prefix string, current any) {
		switch typed := current.(type) {
		case map[string]any:
			for key, child := range typed {
				next := key
				if prefix != "" {
					next = prefix + "." + key
				}
				visit(next, child)
			}
		case map[string]string:
			for key, child := range typed {
				next := key
				if prefix != "" {
					next = prefix + "." + key
				}
				out[strings.ToLower(next)] = child
			}
		case string:
			if prefix != "" {
				out[strings.ToLower(prefix)] = typed
			}
		case fmt.Stringer:
			if prefix != "" {
				out[strings.ToLower(prefix)] = typed.String()
			}
		}
	}
	visit("", value)
	return out
}

func firstRawInputValue(values map[string]string, suffix string) string {
	for key, value := range values {
		if key == suffix || strings.HasSuffix(key, "."+suffix) {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstRawInputString(value any) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}

func summarizeToolLocations(locations []acp.ToolCallLocation) string {
	const maxLocations = 3
	parts := make([]string, 0, min(len(locations), maxLocations))
	for i, location := range locations {
		if i >= maxLocations {
			break
		}
		if location.Line != nil && *location.Line > 0 {
			parts = append(parts, fmt.Sprintf("%s:%d", location.Path, *location.Line))
			continue
		}
		parts = append(parts, location.Path)
	}
	if len(locations) > maxLocations {
		parts = append(parts, fmt.Sprintf("+%d more", len(locations)-maxLocations))
	}
	return strings.Join(parts, ", ")
}

func summarizeToolContent(content []acp.ToolCallContent) string {
	var diffs, terminals int
	var textPreview string
	var textCount int
	for _, item := range content {
		switch {
		case item.Diff != nil:
			diffs++
		case item.Terminal != nil:
			terminals++
		case item.Content != nil:
			textCount++
			if textPreview == "" {
				textPreview = contentBlockText(item.Content.Content)
			}
		}
	}
	parts := make([]string, 0, 3)
	if diffs > 0 {
		parts = append(parts, pluralize(diffs, "diff"))
	}
	if terminals > 0 {
		parts = append(parts, pluralize(terminals, "terminal"))
	}
	if textPreview != "" {
		label := "output"
		if textCount > 1 {
			label = fmt.Sprintf("output 1/%d", textCount)
		}
		parts = append(parts, fmt.Sprintf("%s: %s", label, trimToolSummary(textPreview)))
	} else if textCount > 0 {
		parts = append(parts, pluralize(textCount, "output"))
	}
	return strings.Join(parts, ", ")
}

func trimToolSummary(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if utf8.RuneCountInString(value) <= 120 {
		return value
	}
	runes := []rune(value)
	return string(runes[:117]) + "..."
}

func pluralize(count int, singular string) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", singular)
	}
	return fmt.Sprintf("%d %ss", count, singular)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (t *acpTurn) recordUsage(update *acp.SessionUsageUpdate) {
	if update == nil {
		return
	}
	usage := Usage{
		ContextSize: update.Size,
		ContextUsed: update.Used,
	}
	if update.Cost != nil {
		usage.ReportedCostAmount = strconv.FormatFloat(update.Cost.Amount, 'f', -1, 64)
		usage.ReportedCostCurrency = strings.ToUpper(strings.TrimSpace(update.Cost.Currency))
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.usage = usage
}

func (t *acpTurn) appendRaw(data []byte) {
	if len(data) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	_, _ = t.raw.Write(data)
}

func (t *acpTurn) snapshot() (string, string, Usage) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.output.String(), t.raw.String(), t.usage
}

func (t *acpTurn) truncated() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.output.truncated || t.raw.truncated
}

func contentBlockText(block acp.ContentBlock) string {
	if block.Text != nil {
		return block.Text.Text
	}
	if block.ResourceLink != nil {
		return block.ResourceLink.Uri
	}
	return ""
}

func stderrSuffix(stderr string) string {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return ""
	}
	return ": " + stderr
}

func terminateProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if cmd.Cancel != nil {
		_ = cmd.Cancel()
	} else {
		_ = cmd.Process.Kill()
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}
