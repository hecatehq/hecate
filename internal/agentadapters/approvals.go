package agentadapters

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

// ApprovalMode controls what the recorder does with an incoming
// RequestPermission. The package default is ModeAuto, which preserves
// legacy behavior (auto-select the first allow option). Operator-facing
// runtime flips this default to ModePrompt — see internal/config.
//
// See docs/rfcs/external-adapter-approvals-v1.md.
type ApprovalMode string

const (
	// ModeAuto auto-resolves every approval with the first allow option.
	// Danger mode — preserved for batch / CI / local smoke and for
	// backward compat in tests that don't construct an approval store.
	ModeAuto ApprovalMode = "auto"

	// ModePrompt blocks waiting for an operator decision (or a matching
	// grant). The HTTP/SSE surface that operators interact with is
	// added in a follow-up slice; until that lands, prompt-mode
	// approvals time out at GATEWAY_AGENT_ADAPTER_APPROVAL_TIMEOUT and
	// resolve to a Cancelled outcome. Operators who need adapters to
	// keep working before the UI ships set GATEWAY_AGENT_ADAPTER_APPROVAL_MODE=auto.
	ModePrompt ApprovalMode = "prompt"

	// ModeDeny auto-rejects every approval. Audit / compliance
	// scenarios where adapter tool use must not proceed.
	ModeDeny ApprovalMode = "deny"
)

// ApprovalStatus is the lifecycle state of a single approval row.
type ApprovalStatus string

const (
	ApprovalStatusPending   ApprovalStatus = "pending"
	ApprovalStatusApproved  ApprovalStatus = "approved"
	ApprovalStatusDenied    ApprovalStatus = "denied"
	ApprovalStatusTimedOut  ApprovalStatus = "timed_out"
	ApprovalStatusCancelled ApprovalStatus = "cancelled"
)

// ApprovalScope is the breadth of an operator's "always" decision.
// `once` means no persistence; the others persist as a grant entry.
type ApprovalScope string

const (
	ApprovalScopeOnce          ApprovalScope = "once"
	ApprovalScopeSession       ApprovalScope = "session"
	ApprovalScopeWorkspaceTool ApprovalScope = "workspace_tool"
	ApprovalScopeAdapterTool   ApprovalScope = "adapter_tool"
)

// ApprovalDecision is the operator's high-level intent. Selected option
// is recorded separately so we don't lose adapter-named choices.
type ApprovalDecision string

const (
	ApprovalDecisionApprove ApprovalDecision = "approve"
	ApprovalDecisionDeny    ApprovalDecision = "deny"
)

// ApprovalResolutionPath labels how a pending approval got resolved.
// Telemetry uses this label to distinguish operator-driven decisions
// from grant-cache hits and from default-mode auto-resolutions.
type ApprovalResolutionPath string

const (
	PathOperator    ApprovalResolutionPath = "operator"
	PathGrant       ApprovalResolutionPath = "grant"
	PathDefaultMode ApprovalResolutionPath = "default_mode"
	PathTimeout     ApprovalResolutionPath = "timeout"
)

// ApprovalOption mirrors acp.PermissionOption in a stable wire shape we
// own. We keep the original adapter-supplied option_id verbatim so the
// resolution can route back to the exact ACP option the adapter expects.
type ApprovalOption struct {
	OptionID string `json:"option_id"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
}

// Approval is one recorded approval row. The shape mirrors the wire
// shape in the RFC; endpoints and persistence backends in the next
// slice serialize this directly.
type Approval struct {
	ID             string                 `json:"id"`
	SessionID      string                 `json:"session_id"`
	AdapterID      string                 `json:"adapter_id"`
	Workspace      string                 `json:"workspace,omitempty"`
	ToolKind       string                 `json:"tool_kind"`
	ToolName       string                 `json:"tool_name,omitempty"`
	Status         ApprovalStatus         `json:"status"`
	ACPPayload     json.RawMessage        `json:"acp_payload,omitempty"`
	ACPOptions     []ApprovalOption       `json:"acp_options"`
	ScopeChoices   []ApprovalScope        `json:"scope_choices,omitempty"`
	SelectedOption string                 `json:"selected_option,omitempty"`
	Scope          ApprovalScope          `json:"scope,omitempty"`
	Decision       ApprovalDecision       `json:"decision,omitempty"`
	Path           ApprovalResolutionPath `json:"path,omitempty"`
	DecisionNote   string                 `json:"decision_note,omitempty"`
	CreatedAt      time.Time              `json:"created_at"`
	ResolvedAt     *time.Time             `json:"resolved_at,omitempty"`
	ExpiresAt      time.Time              `json:"expires_at"`
}

// Grant is a persisted "always" decision. v1 stores grants only in
// memory; sqlite persistence + the GET/DELETE /v1/agent-chat/grants
// endpoints land in the next slice.
type Grant struct {
	ID        string           `json:"id"`
	Scope     ApprovalScope    `json:"scope"`
	AdapterID string           `json:"adapter_id"`
	ToolKind  string           `json:"tool_kind"`
	Workspace string           `json:"workspace,omitempty"`
	SessionID string           `json:"session_id,omitempty"`
	Decision  ApprovalDecision `json:"decision"`
	GrantedBy string           `json:"granted_by,omitempty"`
	GrantedAt time.Time        `json:"granted_at"`
	ExpiresAt *time.Time       `json:"expires_at,omitempty"`
}

// ApprovalStore is the persistence interface. Slice 1A ships an
// in-memory implementation; slice 1B adds sqlite.
type ApprovalStore interface {
	// CreateApproval persists a pending approval and returns the row
	// with its assigned ID + timestamps filled in.
	CreateApproval(ctx context.Context, a Approval) (Approval, error)

	// ResolveApproval transitions a pending approval to a terminal
	// status. Returns the updated row.
	ResolveApproval(ctx context.Context, id string, status ApprovalStatus, decision ApprovalDecision, selectedOption string, scope ApprovalScope, path ApprovalResolutionPath, note string, resolvedAt time.Time) (Approval, error)

	// GetApproval fetches an approval by id.
	GetApproval(ctx context.Context, id string) (Approval, error)

	// ListApprovals returns approvals for a session, oldest-first.
	// status="" returns all statuses; otherwise filters.
	ListApprovals(ctx context.Context, sessionID string, status ApprovalStatus) ([]Approval, error)

	// FindMatchingGrant returns the most-specific live grant for the
	// given (sessionID, workspace, adapterID, toolKind) tuple, or
	// (Grant{}, false). Lookup walks scopes session → workspace_tool →
	// adapter_tool, returning the first match. Expired grants are
	// ignored. The returned grant carries the operator's decision.
	FindMatchingGrant(ctx context.Context, sessionID, workspace, adapterID, toolKind string, now time.Time) (Grant, bool, error)
}

// ErrApprovalNotFound is returned by ApprovalStore.GetApproval and
// ResolveApproval when the id doesn't match a known row.
var ErrApprovalNotFound = errors.New("approval not found")

// ErrApprovalAlreadyResolved is returned by ResolveApproval when the
// row is already in a terminal state. Resolutions are append-only
// from a state-machine perspective even though they're a single row.
var ErrApprovalAlreadyResolved = errors.New("approval already resolved")

// ErrAmbiguousOption is returned by Recorder helpers when the operator's
// decision could match multiple ACP options and no explicit
// selected_option was provided. Surfaced as 409 by the HTTP handler in
// slice 1B.
var ErrAmbiguousOption = errors.New("ambiguous adapter option; selected_option required")

// MemoryApprovalStore is a goroutine-safe in-process ApprovalStore.
// All state lives in maps and is discarded on process exit. Suitable
// for tests, dev, and anyone running with
// GATEWAY_AGENT_CHAT_BACKEND=memory (the default).
type MemoryApprovalStore struct {
	mu        sync.Mutex
	approvals map[string]Approval
	grants    map[string]Grant
}

// NewMemoryApprovalStore returns an empty in-memory store.
func NewMemoryApprovalStore() *MemoryApprovalStore {
	return &MemoryApprovalStore{
		approvals: make(map[string]Approval),
		grants:    make(map[string]Grant),
	}
}

func (s *MemoryApprovalStore) CreateApproval(_ context.Context, a Approval) (Approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.ID == "" {
		a.ID = newApprovalID()
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	s.approvals[a.ID] = a
	return a, nil
}

func (s *MemoryApprovalStore) ResolveApproval(_ context.Context, id string, status ApprovalStatus, decision ApprovalDecision, selectedOption string, scope ApprovalScope, path ApprovalResolutionPath, note string, resolvedAt time.Time) (Approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.approvals[id]
	if !ok {
		return Approval{}, ErrApprovalNotFound
	}
	if row.Status != ApprovalStatusPending {
		return row, ErrApprovalAlreadyResolved
	}
	if resolvedAt.IsZero() {
		resolvedAt = time.Now().UTC()
	}
	row.Status = status
	row.Decision = decision
	row.SelectedOption = selectedOption
	row.Scope = scope
	row.Path = path
	row.DecisionNote = note
	row.ResolvedAt = &resolvedAt
	s.approvals[id] = row
	return row, nil
}

func (s *MemoryApprovalStore) GetApproval(_ context.Context, id string) (Approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.approvals[id]
	if !ok {
		return Approval{}, ErrApprovalNotFound
	}
	return row, nil
}

func (s *MemoryApprovalStore) ListApprovals(_ context.Context, sessionID string, status ApprovalStatus) ([]Approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Approval, 0)
	for _, row := range s.approvals {
		if row.SessionID != sessionID {
			continue
		}
		if status != "" && row.Status != status {
			continue
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// CreateGrant inserts a grant row. Used by tests + the resolve handler
// (slice 1B) when an operator picks a scope broader than `once`.
func (s *MemoryApprovalStore) CreateGrant(_ context.Context, g Grant) (Grant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g.ID == "" {
		g.ID = newGrantID()
	}
	if g.GrantedAt.IsZero() {
		g.GrantedAt = time.Now().UTC()
	}
	s.grants[g.ID] = g
	return g, nil
}

func (s *MemoryApprovalStore) FindMatchingGrant(_ context.Context, sessionID, workspace, adapterID, toolKind string, now time.Time) (Grant, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Lookup walks scopes from most-specific to broadest, matching the
	// operator's mental model. Within a scope, the most recently
	// granted entry wins on ties (later overrides earlier intent).
	type candidate struct {
		grant Grant
		rank  int // lower = more specific
	}
	scopeRank := map[ApprovalScope]int{
		ApprovalScopeSession:       0,
		ApprovalScopeWorkspaceTool: 1,
		ApprovalScopeAdapterTool:   2,
	}
	best := candidate{rank: 99}
	bestSet := false
	for _, g := range s.grants {
		if g.AdapterID != adapterID {
			continue
		}
		if g.ToolKind != toolKind {
			continue
		}
		if g.ExpiresAt != nil && !g.ExpiresAt.After(now) {
			continue
		}
		switch g.Scope {
		case ApprovalScopeSession:
			if g.SessionID != sessionID {
				continue
			}
		case ApprovalScopeWorkspaceTool:
			if g.Workspace != workspace {
				continue
			}
		case ApprovalScopeAdapterTool:
			// no extra constraint
		default:
			continue
		}
		rank, ok := scopeRank[g.Scope]
		if !ok {
			continue
		}
		if !bestSet || rank < best.rank || (rank == best.rank && g.GrantedAt.After(best.grant.GrantedAt)) {
			best = candidate{grant: g, rank: rank}
			bestSet = true
		}
	}
	if !bestSet {
		return Grant{}, false, nil
	}
	return best.grant, true, nil
}

// RecorderOptions configure an ApprovalRecorder.
type RecorderOptions struct {
	Mode    ApprovalMode
	Timeout time.Duration
	Store   ApprovalStore
	Logger  *slog.Logger
	// NowFunc is used by tests; defaults to time.Now.UTC.
	NowFunc func() time.Time
	// IDFunc generates approval ids; defaults to ULID-shaped hex.
	IDFunc func() string
	// Hooks are optional callbacks for telemetry. Zero values are no-ops.
	Hooks RecorderHooks
}

// RecorderHooks are optional callbacks invoked by the recorder at
// well-defined lifecycle points. The recorder is the single place that
// knows the (adapter, tool_kind, mode, path) tuple, so all telemetry
// instrumentation goes through here. Implementations live in the
// telemetry package; the recorder package keeps no metric dependencies
// of its own.
type RecorderHooks struct {
	OnRequested func(approval Approval)
	OnResolved  func(approval Approval, durationMS int64)
	OnTimedOut  func(approval Approval, durationMS int64)
}

// ApprovalRecorder applies the configured ApprovalMode to incoming
// ACP RequestPermission calls. It records every request, looks up
// matching grants, applies the mode default, and produces the ACP
// response that gets sent back to the adapter.
type ApprovalRecorder struct {
	opts RecorderOptions
}

// NewApprovalRecorder constructs a recorder with the given options.
// Sensible defaults are filled in for empty fields.
func NewApprovalRecorder(opts RecorderOptions) *ApprovalRecorder {
	if opts.Mode == "" {
		opts.Mode = ModeAuto
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Minute
	}
	if opts.Store == nil {
		opts.Store = NewMemoryApprovalStore()
	}
	if opts.NowFunc == nil {
		opts.NowFunc = func() time.Time { return time.Now().UTC() }
	}
	if opts.IDFunc == nil {
		opts.IDFunc = newApprovalID
	}
	return &ApprovalRecorder{opts: opts}
}

// Mode returns the configured mode (test introspection).
func (r *ApprovalRecorder) Mode() ApprovalMode { return r.opts.Mode }

// Timeout returns the configured timeout.
func (r *ApprovalRecorder) Timeout() time.Duration { return r.opts.Timeout }

// Store returns the configured store (test introspection).
func (r *ApprovalRecorder) Store() ApprovalStore { return r.opts.Store }

// recordingContext bundles the per-request context that the session
// manager carries about an in-flight ACP RequestPermission. Kept
// separate from RecorderOptions so the recorder is reusable across
// many sessions without rebuilding.
type recordingContext struct {
	SessionID string
	AdapterID string
	Workspace string
}

// Handle records the incoming RequestPermission, applies the configured
// mode, and returns the ACP response to send back to the adapter.
//
// In slice 1A:
//   - All modes record the approval row.
//   - ModeAuto resolves immediately by selecting the first allow option
//     (preserves legacy behavior).
//   - ModeDeny resolves immediately with the first reject option, or
//     Cancelled if none.
//   - ModePrompt has no UI to wait for yet, so it waits for the configured
//     timeout (or context cancellation) and then resolves to Cancelled with
//     status=timed_out, path=timeout. This is the intentional
//     "prompt-without-UI behaves as deny-via-timeout" case.
//     Slice 1B replaces this with real blocking on the operator decision.
func (r *ApprovalRecorder) Handle(ctx context.Context, recCtx recordingContext, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	now := r.opts.NowFunc()
	options := normalizeACPOptions(params.Options)
	toolName := extractToolName(params.ToolCall)
	toolKind := extractToolKind(params.ToolCall)

	rawPayload, _ := json.Marshal(params)

	row := Approval{
		ID:           r.opts.IDFunc(),
		SessionID:    recCtx.SessionID,
		AdapterID:    recCtx.AdapterID,
		Workspace:    recCtx.Workspace,
		ToolKind:     toolKind,
		ToolName:     toolName,
		Status:       ApprovalStatusPending,
		ACPPayload:   rawPayload,
		ACPOptions:   options,
		ScopeChoices: defaultScopeChoices(),
		CreatedAt:    now,
		ExpiresAt:    now.Add(r.opts.Timeout),
	}
	created, err := r.opts.Store.CreateApproval(ctx, row)
	if err != nil {
		// Storage failure shouldn't deadlock the adapter; degrade by
		// applying the configured mode without persistence and
		// surfacing the error in the log.
		r.logf("approval_store_create_failed", err, recCtx, toolKind)
		created = row
	}
	if r.opts.Hooks.OnRequested != nil {
		r.opts.Hooks.OnRequested(created)
	}

	// 1) Look for a live grant. A match short-circuits regardless of mode.
	if grant, ok, gerr := r.opts.Store.FindMatchingGrant(ctx, recCtx.SessionID, recCtx.Workspace, recCtx.AdapterID, toolKind, now); gerr == nil && ok {
		response, status, decision, selected := r.applyDecision(grant.Decision, options, false)
		resolved := r.resolve(ctx, created.ID, status, decision, selected, grant.Scope, PathGrant, "", now)
		return response, r.notifyResolved(resolved, now, nil)
	}

	// 2) Apply the configured mode default.
	switch r.opts.Mode {
	case ModeAuto:
		response, status, decision, selected := r.applyDecision(ApprovalDecisionApprove, options, true)
		resolved := r.resolve(ctx, created.ID, status, decision, selected, ApprovalScopeOnce, PathDefaultMode, "", now)
		return response, r.notifyResolved(resolved, now, nil)

	case ModeDeny:
		response, status, decision, selected := r.applyDecision(ApprovalDecisionDeny, options, false)
		resolved := r.resolve(ctx, created.ID, status, decision, selected, ApprovalScopeOnce, PathDefaultMode, "", now)
		return response, r.notifyResolved(resolved, now, nil)

	case ModePrompt:
		// Slice 1A: no UI yet. Slice 1B replaces this branch with a
		// real wait on operator decision. For now we wait until the
		// configured timeout and resolve to Cancelled with
		// status=timed_out. Operators who need adapters working before
		// the UI ships set GATEWAY_AGENT_ADAPTER_APPROVAL_MODE=auto.
		resolvedAt := r.waitForPromptTimeout(ctx, now)
		resolved := r.resolve(ctx, created.ID, ApprovalStatusTimedOut, "", "", ApprovalScopeOnce, PathTimeout, "no operator surface available yet (slice 1A)", resolvedAt)
		r.notifyTimedOut(resolved, now, resolvedAt)
		return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}}}, nil

	default:
		// Unknown mode: fail closed.
		resolved := r.resolve(ctx, created.ID, ApprovalStatusCancelled, "", "", ApprovalScopeOnce, PathDefaultMode, "unknown approval mode "+string(r.opts.Mode), now)
		_ = r.notifyResolved(resolved, now, nil)
		return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}}}, nil
	}
}

// applyDecision picks the appropriate ACP option for an approve/deny
// decision, returning the response, terminal status, and recorded
// selected_option id. allowCustomFallback is true only for explicit
// ModeAuto, where preserving the old "pick first option" behavior is
// intentional. Grants and deny paths stay strict: if Hecate cannot
// identify a normalized allow/reject option, it cancels instead of
// guessing what a custom adapter option means.
func (r *ApprovalRecorder) applyDecision(decision ApprovalDecision, options []ApprovalOption, allowCustomFallback bool) (acp.RequestPermissionResponse, ApprovalStatus, ApprovalDecision, string) {
	switch decision {
	case ApprovalDecisionApprove:
		if opt, ok := pickOption(options, "allow_once", "allow_always"); ok {
			return acp.RequestPermissionResponse{
				Outcome: acp.RequestPermissionOutcome{
					Selected: &acp.RequestPermissionOutcomeSelected{OptionId: acp.PermissionOptionId(opt.OptionID)},
				},
			}, ApprovalStatusApproved, ApprovalDecisionApprove, opt.OptionID
		}
		if allowCustomFallback && len(options) > 0 {
			opt := options[0]
			return acp.RequestPermissionResponse{
				Outcome: acp.RequestPermissionOutcome{
					Selected: &acp.RequestPermissionOutcomeSelected{OptionId: acp.PermissionOptionId(opt.OptionID)},
				},
			}, ApprovalStatusApproved, ApprovalDecisionApprove, opt.OptionID
		}
		return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}}}, ApprovalStatusCancelled, ApprovalDecisionApprove, ""

	case ApprovalDecisionDeny:
		if opt, ok := pickOption(options, "reject_once", "reject_always"); ok {
			return acp.RequestPermissionResponse{
				Outcome: acp.RequestPermissionOutcome{
					Selected: &acp.RequestPermissionOutcomeSelected{OptionId: acp.PermissionOptionId(opt.OptionID)},
				},
			}, ApprovalStatusDenied, ApprovalDecisionDeny, opt.OptionID
		}
		// No deny option — the only honest answer is Cancelled. The
		// adapter sees the same cancel outcome it would on timeout.
		return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}}}, ApprovalStatusCancelled, ApprovalDecisionDeny, ""

	default:
		return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}}}, ApprovalStatusCancelled, "", ""
	}
}

func (r *ApprovalRecorder) waitForPromptTimeout(ctx context.Context, createdAt time.Time) time.Time {
	timer := time.NewTimer(r.opts.Timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return r.opts.NowFunc()
	case <-timer.C:
		return createdAt.Add(r.opts.Timeout)
	}
}

func (r *ApprovalRecorder) resolve(ctx context.Context, id string, status ApprovalStatus, decision ApprovalDecision, selected string, scope ApprovalScope, path ApprovalResolutionPath, note string, now time.Time) Approval {
	row, err := r.opts.Store.ResolveApproval(ctx, id, status, decision, selected, scope, path, note, now)
	if err != nil {
		r.logf("approval_store_resolve_failed", err, recordingContext{SessionID: row.SessionID, AdapterID: row.AdapterID, Workspace: row.Workspace}, row.ToolKind)
	}
	return row
}

func (r *ApprovalRecorder) notifyResolved(row Approval, now time.Time, _ error) error {
	if r.opts.Hooks.OnResolved != nil {
		var dur int64
		if !row.CreatedAt.IsZero() {
			dur = now.Sub(row.CreatedAt).Milliseconds()
		}
		r.opts.Hooks.OnResolved(row, dur)
	}
	return nil
}

func (r *ApprovalRecorder) notifyTimedOut(row Approval, createdAt, resolvedAt time.Time) {
	if r.opts.Hooks.OnTimedOut == nil {
		return
	}
	var dur int64
	if !createdAt.IsZero() && !resolvedAt.IsZero() {
		dur = resolvedAt.Sub(createdAt).Milliseconds()
	}
	r.opts.Hooks.OnTimedOut(row, dur)
}

func (r *ApprovalRecorder) logf(event string, err error, recCtx recordingContext, toolKind string) {
	if r.opts.Logger == nil {
		return
	}
	r.opts.Logger.Warn(event,
		slog.String("error", err.Error()),
		slog.String("session_id", recCtx.SessionID),
		slog.String("adapter_id", recCtx.AdapterID),
		slog.String("tool_kind", toolKind),
	)
}

// pickOption searches options for the first entry whose Kind matches
// any of the given kinds (in priority order). Used to pick the "allow"
// or "reject" option from an ACP option list. Empty kinds list returns
// the first option.
func pickOption(options []ApprovalOption, kinds ...string) (ApprovalOption, bool) {
	for _, want := range kinds {
		for _, opt := range options {
			if strings.EqualFold(opt.Kind, want) {
				return opt, true
			}
		}
	}
	return ApprovalOption{}, false
}

// normalizeACPOptions converts the ACP SDK option list into our
// stable wire shape. The original option_id is preserved so the
// resolution can route back to the adapter's exact option.
func normalizeACPOptions(in []acp.PermissionOption) []ApprovalOption {
	out := make([]ApprovalOption, 0, len(in))
	for _, opt := range in {
		out = append(out, ApprovalOption{
			OptionID: string(opt.OptionId),
			Kind:     string(opt.Kind),
			Name:     opt.Name,
		})
	}
	return out
}

func defaultScopeChoices() []ApprovalScope {
	return []ApprovalScope{ApprovalScopeOnce, ApprovalScopeSession, ApprovalScopeWorkspaceTool, ApprovalScopeAdapterTool}
}

// newApprovalID returns a stable id for an approval row. Format:
// "appr_" + 24 hex chars (96 bits of randomness). Not a strict ULID
// for now — switching to ULID is a follow-up if/when we want lexical
// ordering at the storage layer.
func newApprovalID() string {
	return prefixedID("appr_")
}

func newGrantID() string {
	return prefixedID("grnt_")
}

func prefixedID(prefix string) string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read returning an error is exceptional in modern Go;
		// fall back to a time-based id so the system stays alive.
		return fmt.Sprintf("%s%x", prefix, time.Now().UnixNano())
	}
	return prefix + hex.EncodeToString(b[:])
}
