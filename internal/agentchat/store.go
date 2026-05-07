package agentchat

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hecate/agent-runtime/pkg/types"
)

type Session struct {
	ID              string
	Title           string
	RuntimeKind     string
	AdapterID       string
	DriverKind      string
	NativeSessionID string
	Workspace       string
	// WorkspaceBranch is captured when the session is created so API
	// snapshots don't spawn git on every streamed update.
	WorkspaceBranch string
	Status          string
	TaskID          string
	LatestRunID     string
	Provider        string
	Model           string
	Capabilities    types.ModelCapabilities
	// TurnsUsed counts how many user→assistant round-trips have completed
	// (successfully or with failure) in this session. Used to enforce the
	// GATEWAY_AGENT_CHAT_MAX_TURNS_PER_SESSION ceiling.
	TurnsUsed int
	CreatedAt time.Time
	UpdatedAt time.Time
	Messages  []Message
}

type Message struct {
	ID              string
	RuntimeKind     string
	SegmentID       string
	TaskID          string
	RunID           string
	RequestID       string
	TraceID         string
	SpanID          string
	Role            string
	Content         string
	RawOutput       string
	AdapterID       string
	AdapterName     string
	DriverKind      string
	NativeSessionID string
	Status          string
	ExitCode        int
	CostMode        string
	Provider        string
	Model           string
	Capabilities    types.ModelCapabilities
	Workspace       string
	DiffStat        string
	Diff            string
	CreatedAt       time.Time
	StartedAt       time.Time
	CompletedAt     time.Time
	Error           string
	Activities      []Activity
	Usage           Usage
	Timing          Timing
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
	NeedsAction     bool   `json:"needs_action,omitempty"`
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
	TurnCount      int    `json:"turn_count,omitempty"`
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
		t.TurnCount == 0 &&
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
	UpdateSession(ctx context.Context, id string, update func(*Session)) (Session, error)
	AppendMessage(ctx context.Context, sessionID string, message Message) (Session, error)
	UpdateMessage(ctx context.Context, sessionID string, messageID string, update func(*Message)) (Session, error)
}

func ReconcileInterruptedRuns(ctx context.Context, store Store, now time.Time) (int, error) {
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
					item.Content = "Agent run interrupted by Hecate restart."
				}
				if !activityTypeExists(item.Activities, "interrupted") {
					item.Activities = append(item.Activities, Activity{
						Type:      "interrupted",
						Status:    "cancelled",
						Title:     "Interrupted by restart",
						Detail:    "Hecate restarted before this agent run finished.",
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
	mu       sync.Mutex
	sessions map[string]Session
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{sessions: make(map[string]Session)}
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
	if session.RuntimeKind == "" {
		session.RuntimeKind = defaultRuntimeKind(session)
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
	if message.ID == "" {
		return Session{}, fmt.Errorf("message id is required")
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = time.Now().UTC()
	}
	hydrateMessageRuntimeFromSession(&message, session)
	session.Messages = append(session.Messages, message)
	session.UpdatedAt = message.CreatedAt
	if message.Status != "" && message.Role == "assistant" {
		session.Status = message.Status
	}
	s.sessions[sessionID] = session
	return cloneSession(session), nil
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
		update(&session.Messages[i])
		session.UpdatedAt = time.Now().UTC()
		if session.Messages[i].Status != "" && session.Messages[i].Role == "assistant" {
			session.Status = session.Messages[i].Status
		}
		s.sessions[sessionID] = session
		return cloneSession(session), nil
	}
	return Session{}, fmt.Errorf("agent chat message %q not found", messageID)
}

func cloneSession(session Session) Session {
	session.Messages = append([]Message(nil), session.Messages...)
	for i := range session.Messages {
		session.Messages[i].Activities = append([]Activity(nil), session.Messages[i].Activities...)
	}
	return session
}

func hydrateMessageRuntimeFromSession(message *Message, session Session) {
	if message.RuntimeKind == "" {
		message.RuntimeKind = normalizeMessageRuntimeKind(session)
	}
	if message.TaskID == "" && message.RuntimeKind != "model" && shouldHydrateMessageTaskID(message) {
		message.TaskID = session.TaskID
	}
	if message.Provider == "" {
		message.Provider = session.Provider
	}
	if message.Model == "" {
		message.Model = session.Model
	}
	if message.Capabilities.ToolCalling == "" && !message.Capabilities.Streaming && message.Capabilities.MaxContextTokens == 0 && message.Capabilities.Source == "" {
		message.Capabilities = session.Capabilities
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

func normalizeMessageRuntimeKind(session Session) string {
	if session.RuntimeKind != "" {
		return session.RuntimeKind
	}
	return defaultRuntimeKind(session)
}

func defaultRuntimeKind(session Session) string {
	if session.AdapterID != "" {
		return "external_agent"
	}
	return "agent"
}
