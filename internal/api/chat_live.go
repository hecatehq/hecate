package api

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/chat"
)

// AgentChatLiveEventType discriminates the payload union below. Stable
// strings — once exported on the SSE wire, they're part of the
// frontend contract.
type AgentChatLiveEventType string

const (
	AgentChatLiveEventSessionUpdate     AgentChatLiveEventType = "session_update"
	AgentChatLiveEventApprovalRequested AgentChatLiveEventType = "approval.requested"
	AgentChatLiveEventApprovalResolved  AgentChatLiveEventType = "approval.resolved"
)

// AgentChatLiveEvent is the typed envelope every per-session bus
// subscriber receives. The Type discriminator picks one of the
// payload pointers; exactly one is non-nil for any given event.
//
// Adding a new event type means adding a new const + a new pointer
// field; consumers either render it or ignore it (frontends switch
// on Type and tolerate unknown values).
type AgentChatLiveEvent struct {
	Type              AgentChatLiveEventType
	SessionUpdate     *ChatSessionResponse
	ApprovalRequested *ChatApprovalRequestedEvent
	ApprovalResolved  *ChatApprovalResolvedEvent
}

// ChatApprovalRequestedEvent is the SSE payload published when
// the coordinator records a new approval. Minimal by design — the
// full ACP options + scope_choices are reachable via
// GET /hecate/v1/chat/sessions/{id}/approvals/{id}.
type ChatApprovalRequestedEvent struct {
	ApprovalID   string                        `json:"approval_id"`
	SessionID    string                        `json:"session_id"`
	AdapterID    string                        `json:"adapter_id"`
	ToolKind     string                        `json:"tool_kind"`
	ToolName     string                        `json:"tool_name,omitempty"`
	ScopeChoices []agentadapters.ApprovalScope `json:"scope_choices,omitempty"`
	CreatedAt    string                        `json:"created_at"`
	ExpiresAt    string                        `json:"expires_at"`
}

// ChatApprovalResolvedEvent is the SSE payload published on every
// terminal transition: operator decision, timeout, ctx-cancel, grant
// short-circuit, default-mode auto-resolve. Frontends switch on Path
// to render the disposition correctly:
//   - "operator"           — explicit operator action
//   - "grant"              — pre-existing grant short-circuited the prompt
//   - "default_mode"       — auto/deny mode resolved without operator
//   - "timeout"            — prompt-mode timeout fired
//   - "request_cancelled"  — ctx died (session shutdown, adapter teardown, etc.)
type ChatApprovalResolvedEvent struct {
	ApprovalID     string `json:"approval_id"`
	SessionID      string `json:"session_id"`
	Status         string `json:"status"`
	Decision       string `json:"decision,omitempty"`
	Scope          string `json:"scope,omitempty"`
	Path           string `json:"path"`
	SelectedOption string `json:"selected_option,omitempty"`
	ResolvedAt     string `json:"resolved_at,omitempty"`
}

type agentChatLive struct {
	mu          sync.Mutex
	subscribers map[string]map[chan AgentChatLiveEvent]struct{}
	running     map[string]*agentChatRunControl
	snapshot    agentChatSnapshotConfig
}

type agentChatRunControl struct {
	cancel context.CancelFunc
	done   chan struct{}
	// reason records the trigger for an operator-driven cancel
	// (cancelRun / cancelRunAndWait). The handler reads this when
	// the run terminates so the cancellation counter can label
	// "operator" vs "request_cancelled" (parent ctx died first).
	// Stored as an atomic *string so the cancel and complete paths
	// can race without locking the live struct.
	reason atomic.Pointer[string]
}

// markCancelReason CAS-installs the cancellation reason so the
// handler can label the cancellation counter correctly. First write
// wins — once a reason is recorded, later cancel calls (e.g.
// cancelRunAndWait fired by Delete after Cancel) don't clobber it.
func (c *agentChatRunControl) markCancelReason(reason string) {
	c.reason.CompareAndSwap(nil, &reason)
}

// cancelReason reports the recorded reason or empty string.
func (c *agentChatRunControl) cancelReason() string {
	if r := c.reason.Load(); r != nil {
		return *r
	}
	return ""
}

func newAgentChatLive(snapshot agentChatSnapshotConfig) *agentChatLive {
	return &agentChatLive{
		subscribers: make(map[string]map[chan AgentChatLiveEvent]struct{}),
		running:     make(map[string]*agentChatRunControl),
		snapshot:    snapshot,
	}
}

func (l *agentChatLive) subscribe(sessionID string) (<-chan AgentChatLiveEvent, func()) {
	ch := make(chan AgentChatLiveEvent, 16)
	l.mu.Lock()
	if l.subscribers[sessionID] == nil {
		l.subscribers[sessionID] = make(map[chan AgentChatLiveEvent]struct{})
	}
	l.subscribers[sessionID][ch] = struct{}{}
	l.mu.Unlock()

	return ch, func() {
		l.mu.Lock()
		if subs := l.subscribers[sessionID]; subs != nil {
			delete(subs, ch)
			if len(subs) == 0 {
				delete(l.subscribers, sessionID)
			}
		}
		close(ch)
		l.mu.Unlock()
	}
}

// publishSession notifies subscribers that the session row updated.
// The message replaces any pending session_update on a full buffer
// (latest-write-wins) — operators care about current state, not the
// chronology of every micro-update.
func (l *agentChatLive) publishSession(session chat.Session) {
	l.publish(session.ID, AgentChatLiveEvent{
		Type: AgentChatLiveEventSessionUpdate,
		SessionUpdate: &ChatSessionResponse{
			Object: "chat_session",
			Data:   renderChatSession(session, l.snapshot),
		},
	}, true)
}

// publishApprovalRequested notifies subscribers that a new approval
// is pending. Drop-on-full (no replacement) — approval events are
// each a discrete observation; clobbering one with another would
// silently lose the earlier event. Operators recover via the GET
// list endpoint on reconnect.
func (l *agentChatLive) publishApprovalRequested(payload ChatApprovalRequestedEvent) {
	l.publish(payload.SessionID, AgentChatLiveEvent{
		Type:              AgentChatLiveEventApprovalRequested,
		ApprovalRequested: &payload,
	}, false)
}

// publishApprovalResolved notifies subscribers that a pending approval
// reached a terminal state. Same drop-on-full rule as
// publishApprovalRequested.
func (l *agentChatLive) publishApprovalResolved(payload ChatApprovalResolvedEvent) {
	l.publish(payload.SessionID, AgentChatLiveEvent{
		Type:             AgentChatLiveEventApprovalResolved,
		ApprovalResolved: &payload,
	}, false)
}

// publish is the shared fan-out. The replacePolicy flag controls
// behavior on a full subscriber buffer:
//   - true  (session updates): replace an older pending session_update
//     and enqueue this one. Latest-write-wins, but only among session
//     rows; queued approval events are discrete observations and must
//     not be evicted by chat-state churn.
//   - false (approval events): drop this event. Each approval event
//     is a discrete observation; replacing one with another would
//     silently lose history. Operators recover via the GET list on
//     reconnect.
func (l *agentChatLive) publish(sessionID string, event AgentChatLiveEvent, replacePolicy bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for ch := range l.subscribers[sessionID] {
		select {
		case ch <- event:
		default:
			if !replacePolicy {
				continue
			}
			replaceBufferedSessionUpdate(ch, event)
		}
	}
}

func replaceBufferedSessionUpdate(ch chan AgentChatLiveEvent, event AgentChatLiveEvent) {
	buffered := len(ch)
	if buffered == 0 {
		return
	}
	preserved := make([]AgentChatLiveEvent, 0, buffered)
	replaced := false
	for i := 0; i < buffered; i++ {
		select {
		case pending := <-ch:
			if !replaced && pending.Type == AgentChatLiveEventSessionUpdate {
				replaced = true
				continue
			}
			preserved = append(preserved, pending)
		default:
			i = buffered
		}
	}
	for _, pending := range preserved {
		select {
		case ch <- pending:
		default:
			return
		}
	}
	if !replaced {
		return
	}
	select {
	case ch <- event:
	default:
	}
}

func (l *agentChatLive) registerRun(sessionID string, cancel context.CancelFunc) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, exists := l.running[sessionID]; exists {
		return false
	}
	l.running[sessionID] = &agentChatRunControl{cancel: cancel, done: make(chan struct{})}
	return true
}

func (l *agentChatLive) clearRun(sessionID string) {
	l.mu.Lock()
	run, ok := l.running[sessionID]
	if ok {
		delete(l.running, sessionID)
		close(run.done)
	}
	l.mu.Unlock()
}

// cancelReasonFor reports the cancellation reason recorded for the
// given session (operator-driven via cancelRun*) or empty if either
// no run is registered or no operator action was taken. The handler
// uses this on the run-completion path to distinguish operator
// cancels from a request_cancelled (parent ctx died first).
func (l *agentChatLive) cancelReasonFor(sessionID string) string {
	l.mu.Lock()
	run, ok := l.running[sessionID]
	l.mu.Unlock()
	if !ok || run == nil {
		return ""
	}
	return run.cancelReason()
}

func (l *agentChatLive) cancelRun(sessionID string) bool {
	l.mu.Lock()
	run, ok := l.running[sessionID]
	l.mu.Unlock()
	if !ok {
		return false
	}
	run.markCancelReason("operator")
	run.cancel()
	return true
}

func (l *agentChatLive) cancelRunAndWait(ctx context.Context, sessionID string) bool {
	l.mu.Lock()
	run, ok := l.running[sessionID]
	l.mu.Unlock()
	if !ok {
		return true
	}
	run.markCancelReason("operator")
	run.cancel()
	select {
	case <-run.done:
		return true
	case <-ctx.Done():
		return false
	}
}
