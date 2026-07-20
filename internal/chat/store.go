package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hecatehq/hecate/internal/agentcontrols"
	"github.com/hecatehq/hecate/pkg/types"
)

type Session struct {
	ID              string
	Title           string
	ProjectID       string
	AgentID         string
	DriverKind      string
	NativeSessionID string
	Workspace       string
	// WorkspaceMode records whether Hecate-owned task turns run in an
	// isolated copy of Workspace or directly in it. Legacy rows leave this
	// empty and retain the historical in-place behavior through
	// EffectiveWorkspaceMode.
	WorkspaceMode string
	// WorkspaceBranch is captured when the session is created so API
	// snapshots don't spawn git on every streamed update.
	WorkspaceBranch   string
	Status            string
	TaskID            string
	LatestRunID       string
	Provider          string
	Model             string
	Capabilities      types.ModelCapabilities
	ConfigOptions     []agentcontrols.ConfigOption
	AvailableCommands []agentcontrols.Command
	// AvailableCommandsAuthoritative records that an ACP peer has published a
	// live replacement catalog. It is deliberately internal state: API clients
	// receive the catalog itself, not the ordering fence that protects it from
	// an older prepare/run/config snapshot.
	AvailableCommandsAuthoritative bool
	AgentInfo                      *agentcontrols.ImplementationInfo
	MCPServers                     []types.MCPServerConfig
	RTKEnabled                     bool
	// TurnsUsed counts how many user→assistant round-trips have completed
	// (successfully or with failure) in this session. Used to enforce the
	// HECATE_CHAT_MAX_TURNS_PER_SESSION ceiling.
	TurnsUsed      int
	ContextSummary ContextSummary
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Messages       []Message
}

const (
	WorkspaceModePersistent = "persistent"
	WorkspaceModeEphemeral  = "ephemeral"
	WorkspaceModeInPlace    = "in_place"
)

// EffectiveWorkspaceMode preserves the historical Hecate Chat behavior for
// sessions created before workspace posture was persisted. New clients send
// an explicit value, with the operator UI defaulting to an isolated workspace.
func EffectiveWorkspaceMode(mode string) string {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return WorkspaceModeInPlace
	}
	return mode
}

type Message struct {
	ID            string
	ExecutionMode string
	// ToolsEnabled records whether the user submitted this turn with
	// tools on. Independent of ExecutionMode: a Hecate-task turn with
	// ToolsEnabled=false dispatches directly to the model without
	// creating an agent_loop task.
	ToolsEnabled bool
	SegmentID    string
	// TurnID identifies the Chat turn shared by its user and assistant
	// messages. RunID is separate and is populated only when that turn is
	// backed by a real Task Run.
	TurnID          string
	TaskID          string
	RunID           string
	RequestID       string
	TraceID         string
	SpanID          string
	Role            string
	Content         string
	Attachments     []MessageAttachment
	RawOutput       string
	AgentID         string
	AgentName       string
	DriverKind      string
	NativeSessionID string
	AgentInfo       *agentcontrols.ImplementationInfo
	Status          string
	ExitCode        int
	CostMode        string
	Provider        string
	// ProviderInstance is an internal, opaque fence for provider-bound binary
	// history. It is persisted but never rendered into chat API responses.
	ProviderInstance types.ProviderInstanceIdentity
	Model            string
	Capabilities     types.ModelCapabilities
	Workspace        string
	DiffStat         string
	Diff             string
	CreatedAt        time.Time
	StartedAt        time.Time
	CompletedAt      time.Time
	Error            string
	Activities       []Activity
	Usage            Usage
	Timing           Timing
	// Context is attached to assistant messages. It records metadata about the
	// context sources visible to the operator, without prompt bodies or file
	// contents.
	Context ContextPacket
}

// MessageAttachment is the immutable metadata snapshot stored with a chat
// message. Binary bodies live in chatattachments and are hydrated only for an
// outbound model/agent request or a Hecate-native content response; middleware
// applies the optional runtime-token or remote-runtime identity guard.
type MessageAttachment struct {
	ID        string    `json:"id"`
	Filename  string    `json:"filename"`
	MediaType string    `json:"media_type"`
	SizeBytes int64     `json:"size_bytes"`
	SHA256    string    `json:"sha256"`
	CreatedAt time.Time `json:"created_at"`
}

type Activity struct {
	ID         string    `json:"id,omitempty"`
	Type       string    `json:"type"`
	Status     string    `json:"status,omitempty"`
	Kind       string    `json:"kind,omitempty"`
	Title      string    `json:"title"`
	Detail     string    `json:"detail,omitempty"`
	CreatedAt  time.Time `json:"created_at,omitempty"`
	ArtifactID string    `json:"artifact_id,omitempty"`
	// ArtifactSizeBytes is populated for task artifact activities.
	// It lets chat diagnostics hide empty stdout/stderr captures while
	// still linking useful non-empty run output.
	ArtifactSizeBytes int64 `json:"artifact_size_bytes,omitempty"`
	// ArtifactPreview carries a capped text preview for stdout/stderr-like
	// artifacts so chat diagnostics can explain a failed tool without forcing
	// the operator to leave the transcript.
	ArtifactPreview string `json:"artifact_preview,omitempty"`
	ApprovalID      string `json:"approval_id,omitempty"`
	// ActionSummary is the bounded, sanitized ordered bundle covered by an
	// approval decision. It is safe to persist with the compact Chat activity.
	ActionSummary           []string `json:"action_summary,omitempty"`
	ActionSummaryIncomplete bool     `json:"action_summary_incomplete,omitempty"`
	NeedsAction             bool     `json:"needs_action,omitempty"`
	MCPApp                  *MCPApp  `json:"mcp_app,omitempty"`
}

type MCPApp struct {
	ResourceURI   string          `json:"resource_uri,omitempty"`
	MIMEType      string          `json:"mime_type,omitempty"`
	HTML          string          `json:"html,omitempty"`
	HTMLTruncated bool            `json:"html_truncated,omitempty"`
	ToolName      string          `json:"tool_name,omitempty"`
	ToolInput     json.RawMessage `json:"tool_input,omitempty"`
	ToolResult    json.RawMessage `json:"tool_result,omitempty"`
	ResourceMeta  json.RawMessage `json:"resource_meta,omitempty"`
	ToolMeta      json.RawMessage `json:"tool_meta,omitempty"`
	Error         string          `json:"error,omitempty"`
}

type ContextPacket struct {
	ID                   string          `json:"id,omitempty"`
	Version              string          `json:"version,omitempty"`
	ExecutionMode        string          `json:"execution_mode,omitempty"`
	Provider             string          `json:"provider,omitempty"`
	Model                string          `json:"model,omitempty"`
	ExecutionProfile     string          `json:"execution_profile,omitempty"`
	Workspace            string          `json:"workspace,omitempty"`
	SystemPromptIncluded bool            `json:"system_prompt_included,omitempty"`
	MessageCount         int             `json:"message_count,omitempty"`
	Refs                 *ContextRefs    `json:"refs,omitempty"`
	Sources              []ContextSource `json:"sources,omitempty"`
	Items                []ContextItem   `json:"items,omitempty"`
}

type ContextRefs struct {
	SessionID    string `json:"session_id,omitempty"`
	TurnID       string `json:"turn_id,omitempty"`
	MessageID    string `json:"message_id,omitempty"`
	TaskID       string `json:"task_id,omitempty"`
	RunID        string `json:"run_id,omitempty"`
	ProjectID    string `json:"project_id,omitempty"`
	WorkItemID   string `json:"work_item_id,omitempty"`
	AssignmentID string `json:"assignment_id,omitempty"`
	RoleID       string `json:"role_id,omitempty"`
}

type ContextSource struct {
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
	Trust  string `json:"trust,omitempty"`
}

type ContextItem struct {
	Section         string            `json:"section,omitempty"`
	Kind            string            `json:"kind"`
	TrustLevel      string            `json:"trust_level"`
	Origin          string            `json:"origin"`
	Title           string            `json:"title"`
	Body            string            `json:"body,omitempty"`
	BodyRef         string            `json:"body_ref,omitempty"`
	Included        bool              `json:"included"`
	InclusionReason string            `json:"inclusion_reason,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

func (packet ContextPacket) Empty() bool {
	return packet.Version == "" &&
		packet.ExecutionMode == "" &&
		packet.Provider == "" &&
		packet.Model == "" &&
		packet.Workspace == "" &&
		!packet.SystemPromptIncluded &&
		packet.MessageCount == 0 &&
		len(packet.Sources) == 0 &&
		len(packet.Items) == 0
}

type ContextSummary struct {
	Content          string    `json:"content,omitempty"`
	MessageCount     int       `json:"message_count,omitempty"`
	ThroughMessageID string    `json:"through_message_id,omitempty"`
	Strategy         string    `json:"strategy,omitempty"`
	CompactedAt      time.Time `json:"compacted_at,omitempty"`
}

func (summary ContextSummary) Empty() bool {
	return strings.TrimSpace(summary.Content) == "" &&
		summary.MessageCount == 0 &&
		strings.TrimSpace(summary.ThroughMessageID) == "" &&
		strings.TrimSpace(summary.Strategy) == "" &&
		summary.CompactedAt.IsZero()
}

type Usage struct {
	ContextSize          int    `json:"context_size,omitempty"`
	ContextUsed          int    `json:"context_used,omitempty"`
	ReportedCostAmount   string `json:"reported_cost_amount,omitempty"`
	ReportedCostCurrency string `json:"reported_cost_currency,omitempty"`
}

func (u Usage) Empty() bool {
	return u.ContextSize == 0 && u.ContextUsed == 0 && u.ReportedCostAmount == "" && u.ReportedCostCurrency == ""
}

type Timing struct {
	TotalMS        int64  `json:"total_ms,omitempty"`
	QueueMS        int64  `json:"queue_ms,omitempty"`
	ModelMS        int64  `json:"model_ms,omitempty"`
	ToolMS         int64  `json:"tool_ms,omitempty"`
	ApprovalWaitMS int64  `json:"approval_wait_ms,omitempty"`
	OverheadMS     int64  `json:"overhead_ms,omitempty"`
	ModelCallCount int    `json:"model_call_count,omitempty"`
	ToolCount      int    `json:"tool_count,omitempty"`
	Bottleneck     string `json:"bottleneck,omitempty"`
	BottleneckMS   int64  `json:"bottleneck_ms,omitempty"`
}

func (t Timing) Empty() bool {
	return t.TotalMS == 0 &&
		t.QueueMS == 0 &&
		t.ModelMS == 0 &&
		t.ToolMS == 0 &&
		t.ApprovalWaitMS == 0 &&
		t.OverheadMS == 0 &&
		t.ModelCallCount == 0 &&
		t.ToolCount == 0 &&
		t.Bottleneck == "" &&
		t.BottleneckMS == 0
}

type Store interface {
	Backend() string
	Create(ctx context.Context, session Session) (Session, error)
	Get(ctx context.Context, id string) (Session, bool, error)
	List(ctx context.Context) ([]Session, error)
	Delete(ctx context.Context, id string) error
	DeleteByProjectID(ctx context.Context, projectID string) error
	UpdateSession(ctx context.Context, id string, update func(*Session)) (Session, error)
	AppendMessage(ctx context.Context, sessionID string, message Message) (Session, error)
	MessageRequestLeaseTTL() time.Duration
	ClaimMessageRequest(ctx context.Context, sessionID, clientRequestID string, fingerprint MessageRequestFingerprint) (MessageRequestClaim, error)
	RenewMessageRequest(ctx context.Context, req RenewMessageRequestRequest) error
	CommitMessageRequest(ctx context.Context, lease MessageRequestLease, message Message) (Session, error)
	ReleaseMessageRequest(ctx context.Context, lease MessageRequestLease) error
	UpdateMessage(ctx context.Context, sessionID string, messageID string, update func(*Message)) (Session, error)
	// LinkTaskRun atomically binds a newly created Hecate task/run to its
	// session and the user/assistant message pair that launched it. Managed
	// workspace rebinding must never leave those three durable projections
	// disagreeing about which workspace Review should inspect.
	LinkTaskRun(ctx context.Context, sessionID, userMessageID, assistantMessageID string, update func(*Session, *Message, *Message)) (Session, error)
}

func ReconcileInterruptedTurns(ctx context.Context, store Store, now time.Time) (int, error) {
	if store == nil {
		return 0, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	sessions, err := store.List(ctx)
	if err != nil {
		return 0, err
	}
	reconciled := 0
	for _, summary := range sessions {
		session, ok, err := store.Get(ctx, summary.ID)
		if err != nil {
			return reconciled, err
		}
		if !ok {
			continue
		}
		sessionReconciled := false
		for _, message := range session.Messages {
			if message.Role != "assistant" || message.Status != "running" {
				continue
			}
			if _, err := store.UpdateMessage(ctx, session.ID, message.ID, func(item *Message) {
				item.Status = "cancelled"
				item.ExitCode = 1
				item.CompletedAt = now
				item.Error = "interrupted by Hecate restart"
				if item.Content == "" {
					item.Content = "Agent turn interrupted by Hecate restart."
				}
				if !activityTypeExists(item.Activities, "interrupted") {
					item.Activities = append(item.Activities, Activity{
						Type:      "interrupted",
						Status:    "cancelled",
						Title:     "Interrupted by restart",
						Detail:    "Hecate restarted before this agent turn finished.",
						CreatedAt: now,
					})
				}
			}); err != nil {
				return reconciled, err
			}
			reconciled++
			sessionReconciled = true
		}
		if !sessionReconciled && session.Status == "running" {
			if _, err := store.UpdateSession(ctx, session.ID, func(item *Session) {
				item.Status = "cancelled"
			}); err != nil {
				return reconciled, err
			}
			reconciled++
		}
	}
	return reconciled, nil
}

func activityTypeExists(items []Activity, typ string) bool {
	for _, item := range items {
		if item.Type == typ {
			return true
		}
	}
	return false
}

type MemoryStore struct {
	mu                sync.Mutex
	sessions          map[string]Session
	messageRequests   map[messageRequestKey]*memoryMessageRequest
	messageRequestNow func() time.Time
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		sessions:          make(map[string]Session),
		messageRequests:   make(map[messageRequestKey]*memoryMessageRequest),
		messageRequestNow: time.Now,
	}
}

func (s *MemoryStore) MessageRequestLeaseTTL() time.Duration {
	return MessageRequestLeaseStaleAfter
}

func (s *MemoryStore) messageRequestNowUTC() time.Time {
	if s.messageRequestNow == nil {
		return time.Now().UTC()
	}
	return s.messageRequestNow().UTC()
}

func (s *MemoryStore) setMessageRequestNow(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messageRequestNow = now
}

func (s *MemoryStore) Backend() string {
	return "memory"
}

func (s *MemoryStore) Create(_ context.Context, session Session) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session.ID == "" {
		return Session{}, fmt.Errorf("session id is required")
	}
	now := time.Now().UTC()
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	session.UpdatedAt = session.CreatedAt
	if session.Status == "" {
		session.Status = "idle"
	}
	session.Messages = append([]Message(nil), session.Messages...)
	if session.AgentID == "" {
		session.AgentID = DefaultAgentID
	}
	s.sessions[session.ID] = session
	return cloneSession(session), nil
}

func (s *MemoryStore) Get(_ context.Context, id string) (Session, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	if !ok {
		return Session{}, false, nil
	}
	return cloneSession(session), true, nil
}

func (s *MemoryStore) List(_ context.Context) ([]Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		items = append(items, cloneSession(session))
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	return items, nil
}

func (s *MemoryStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
	for key, record := range s.messageRequests {
		if key.SessionID != id {
			continue
		}
		if !record.committed {
			close(record.done)
		}
		delete(s.messageRequests, key)
	}
	return nil
}

func (s *MemoryStore) DeleteByProjectID(_ context.Context, projectID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, session := range s.sessions {
		if session.ProjectID == projectID {
			delete(s.sessions, id)
			for key, record := range s.messageRequests {
				if key.SessionID != id {
					continue
				}
				if !record.committed {
					close(record.done)
				}
				delete(s.messageRequests, key)
			}
		}
	}
	return nil
}

func (s *MemoryStore) UpdateSession(_ context.Context, id string, update func(*Session)) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	if !ok {
		return Session{}, fmt.Errorf("agent chat session %q not found", id)
	}
	update(&session)
	session.UpdatedAt = time.Now().UTC()
	s.sessions[id] = session
	return cloneSession(session), nil
}

func (s *MemoryStore) AppendMessage(_ context.Context, sessionID string, message Message) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[sessionID]
	if !ok {
		return Session{}, fmt.Errorf("agent chat session %q not found", sessionID)
	}
	if err := appendMemoryMessage(&session, message); err != nil {
		return Session{}, err
	}
	s.sessions[sessionID] = session
	return cloneSession(session), nil
}

func appendMemoryMessage(session *Session, message Message) error {
	if message.ID == "" {
		return fmt.Errorf("message id is required")
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = time.Now().UTC()
	}
	hydrateMessageRuntimeFromSession(&message, *session)
	session.Messages = append(session.Messages, message)
	session.UpdatedAt = message.CreatedAt
	if message.Status != "" && message.Role == "assistant" {
		session.Status = message.Status
	}
	return nil
}

func (s *MemoryStore) UpdateMessage(_ context.Context, sessionID string, messageID string, update func(*Message)) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[sessionID]
	if !ok {
		return Session{}, fmt.Errorf("agent chat session %q not found", sessionID)
	}
	for i := range session.Messages {
		if session.Messages[i].ID != messageID {
			continue
		}
		previousStatus := session.Messages[i].Status
		update(&session.Messages[i])
		session.UpdatedAt = time.Now().UTC()
		if session.Messages[i].Status != "" && session.Messages[i].Role == "assistant" && session.Messages[i].Status != previousStatus {
			session.Status = session.Messages[i].Status
		}
		s.sessions[sessionID] = session
		return cloneSession(session), nil
	}
	return Session{}, fmt.Errorf("agent chat message %q not found", messageID)
}

func (s *MemoryStore) LinkTaskRun(_ context.Context, sessionID, userMessageID, assistantMessageID string, update func(*Session, *Message, *Message)) (Session, error) {
	if strings.TrimSpace(userMessageID) == "" || strings.TrimSpace(assistantMessageID) == "" || userMessageID == assistantMessageID {
		return Session{}, fmt.Errorf("distinct task-run message ids are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[sessionID]
	if !ok {
		return Session{}, fmt.Errorf("agent chat session %q not found", sessionID)
	}
	userIndex, assistantIndex := -1, -1
	for i := range session.Messages {
		switch session.Messages[i].ID {
		case userMessageID:
			userIndex = i
		case assistantMessageID:
			assistantIndex = i
		}
	}
	if userIndex < 0 {
		return Session{}, fmt.Errorf("agent chat message %q not found", userMessageID)
	}
	if assistantIndex < 0 {
		return Session{}, fmt.Errorf("agent chat message %q not found", assistantMessageID)
	}
	update(&session, &session.Messages[userIndex], &session.Messages[assistantIndex])
	session.UpdatedAt = time.Now().UTC()
	s.sessions[sessionID] = session
	return cloneSession(session), nil
}

func cloneSession(session Session) Session {
	session.ConfigOptions = cloneConfigOptions(session.ConfigOptions)
	session.AvailableCommands = cloneCommands(session.AvailableCommands)
	session.AgentInfo = cloneImplementationInfo(session.AgentInfo)
	session.MCPServers = cloneMCPServerConfigs(session.MCPServers)
	session.ContextSummary = cloneContextSummary(session.ContextSummary)
	session.Messages = append([]Message(nil), session.Messages...)
	for i := range session.Messages {
		session.Messages[i].AgentInfo = cloneImplementationInfo(session.Messages[i].AgentInfo)
		session.Messages[i].Attachments = append([]MessageAttachment(nil), session.Messages[i].Attachments...)
		session.Messages[i].Activities = append([]Activity(nil), session.Messages[i].Activities...)
		for j := range session.Messages[i].Activities {
			session.Messages[i].Activities[j].ActionSummary = append([]string(nil), session.Messages[i].Activities[j].ActionSummary...)
			session.Messages[i].Activities[j].MCPApp = cloneMCPApp(session.Messages[i].Activities[j].MCPApp)
		}
		session.Messages[i].Context.Sources = append([]ContextSource(nil), session.Messages[i].Context.Sources...)
		session.Messages[i].Context.Items = append([]ContextItem(nil), session.Messages[i].Context.Items...)
		for j := range session.Messages[i].Context.Items {
			session.Messages[i].Context.Items[j].Metadata = cloneStringMap(session.Messages[i].Context.Items[j].Metadata)
		}
	}
	return session
}

func cloneImplementationInfo(info *agentcontrols.ImplementationInfo) *agentcontrols.ImplementationInfo {
	if info == nil {
		return nil
	}
	out := *info
	return &out
}

func cloneContextSummary(summary ContextSummary) ContextSummary {
	summary.Content = strings.TrimSpace(summary.Content)
	summary.ThroughMessageID = strings.TrimSpace(summary.ThroughMessageID)
	return summary
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneMCPServerConfigs(values []types.MCPServerConfig) []types.MCPServerConfig {
	if values == nil {
		return nil
	}
	out := make([]types.MCPServerConfig, len(values))
	for i, value := range values {
		out[i] = value
		out[i].Args = append([]string(nil), value.Args...)
		out[i].Env = cloneStringMap(value.Env)
		out[i].Headers = cloneStringMap(value.Headers)
	}
	return out
}

func cloneMCPApp(app *MCPApp) *MCPApp {
	if app == nil {
		return nil
	}
	clone := *app
	clone.ToolInput = append(json.RawMessage(nil), app.ToolInput...)
	clone.ToolResult = append(json.RawMessage(nil), app.ToolResult...)
	clone.ResourceMeta = append(json.RawMessage(nil), app.ResourceMeta...)
	clone.ToolMeta = append(json.RawMessage(nil), app.ToolMeta...)
	return &clone
}

func cloneConfigOptions(options []agentcontrols.ConfigOption) []agentcontrols.ConfigOption {
	if options == nil {
		return nil
	}
	out := make([]agentcontrols.ConfigOption, len(options))
	copy(out, options)
	for i := range out {
		if options[i].Options == nil {
			continue
		}
		out[i].Options = make([]agentcontrols.ConfigSelectOption, len(options[i].Options))
		copy(out[i].Options, options[i].Options)
	}
	return out
}

func cloneCommands(commands []agentcontrols.Command) []agentcontrols.Command {
	if commands == nil {
		return nil
	}
	out := make([]agentcontrols.Command, len(commands))
	copy(out, commands)
	return out
}

// ApplyAvailableCommandsBootstrap accepts a synchronous session snapshot only
// until an ACP peer has supplied an authoritative live replacement. This keeps
// an older prepare/run/config result from erasing a newer notification that
// arrived concurrently.
func ApplyAvailableCommandsBootstrap(session *Session, commands []agentcontrols.Command, known bool) {
	if session == nil || !known || session.AvailableCommandsAuthoritative {
		return
	}
	session.AvailableCommands = cloneCommands(commands)
}

// ApplyAvailableCommandsLive records the complete command snapshot last
// advertised by an ACP peer. An explicit empty list is authoritative too: it
// tells Hecate to clear commands the peer no longer offers.
func ApplyAvailableCommandsLive(session *Session, commands []agentcontrols.Command) {
	if session == nil {
		return
	}
	session.AvailableCommands = cloneCommands(commands)
	session.AvailableCommandsAuthoritative = true
}

// ResetAvailableCommandsAuthority discards a catalog bound to a prior native
// session. The next bootstrap snapshot or live peer notification may populate
// it for the replacement session.
func ResetAvailableCommandsAuthority(session *Session) {
	if session == nil {
		return
	}
	session.AvailableCommands = nil
	session.AvailableCommandsAuthoritative = false
}

const (
	DefaultAgentID             = "hecate"
	ExecutionModeHecateTask    = "hecate_task"
	ExecutionModeExternalAgent = "external_agent"
)

func hydrateMessageRuntimeFromSession(message *Message, session Session) {
	if message.ExecutionMode == "" {
		message.ExecutionMode = defaultMessageExecutionMode(session)
	}
	if message.TaskID == "" && message.ExecutionMode == ExecutionModeHecateTask && shouldHydrateMessageTaskID(message) {
		message.TaskID = session.TaskID
	}
	if message.AgentID == "" {
		message.AgentID = session.AgentID
	}
	if message.Provider == "" {
		message.Provider = session.Provider
	}
	if message.Model == "" {
		message.Model = session.Model
	}
	if message.Capabilities.ToolCalling == "" && message.Capabilities.ImageInput == "" && !message.Capabilities.Streaming && message.Capabilities.MaxContextTokens == 0 && message.Capabilities.Source == "" {
		message.Capabilities = session.Capabilities
	}
	if message.AgentInfo == nil {
		message.AgentInfo = cloneImplementationInfo(session.AgentInfo)
	}
	if message.SegmentID == "" {
		switch {
		case message.TaskID != "":
			message.SegmentID = "task:" + message.TaskID
		case session.NativeSessionID != "":
			message.SegmentID = "external:" + session.NativeSessionID
		default:
			message.SegmentID = "session:" + session.ID
		}
	}
}

func shouldHydrateMessageTaskID(message *Message) bool {
	if strings.TrimSpace(message.SegmentID) == "" {
		return true
	}
	return strings.HasPrefix(strings.TrimSpace(message.SegmentID), "task:")
}

func defaultMessageExecutionMode(session Session) string {
	if session.AgentID != "" && session.AgentID != DefaultAgentID {
		return ExecutionModeExternalAgent
	}
	return ExecutionModeHecateTask
}
