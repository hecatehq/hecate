package api

import (
	"context"
	"sync"

	"github.com/hecate/agent-runtime/internal/agentadapters"
	"github.com/hecate/agent-runtime/internal/agentchat"
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
	SessionUpdate     *AgentChatSessionResponse
	ApprovalRequested *AgentChatApprovalRequestedEvent
	ApprovalResolved  *AgentChatApprovalResolvedEvent
}

// AgentChatApprovalRequestedEvent is the SSE payload published when
// the coordinator records a new approval. Minimal by design — the
// full ACP options + scope_choices are reachable via
// GET /v1/agent-chat/sessions/{id}/approvals/{id}.
type AgentChatApprovalRequestedEvent struct {
	ApprovalID   string                        `json:"approval_id"`
	SessionID    string                        `json:"session_id"`
	AdapterID    string                        `json:"adapter_id"`
	ToolKind     string                        `json:"tool_kind"`
	ToolName     string                        `json:"tool_name,omitempty"`
	ScopeChoices []agentadapters.ApprovalScope `json:"scope_choices,omitempty"`
	CreatedAt    string                        `json:"created_at"`
	ExpiresAt    string                        `json:"expires_at"`
}

// AgentChatApprovalResolvedEvent is the SSE payload published on every
// terminal transition: operator decision, timeout, ctx-cancel, grant
// short-circuit, default-mode auto-resolve. Frontends switch on Path
// to render the disposition correctly:
//   - "operator"           — explicit operator action
//   - "grant"              — pre-existing grant short-circuited the prompt
//   - "default_mode"       — auto/deny mode resolved without operator
//   - "timeout"            — prompt-mode timeout fired
//   - "request_cancelled"  — ctx died (session shutdown, adapter teardown, etc.)
type AgentChatApprovalResolvedEvent struct {
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
	running     map[string]agentChatRunControl
}

type agentChatRunControl struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func newAgentChatLive() *agentChatLive {
	return &agentChatLive{
		subscribers: make(map[string]map[chan AgentChatLiveEvent]struct{}),
		running:     make(map[string]agentChatRunControl),
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
func (l *agentChatLive) publishSession(session agentchat.Session) {
	l.publish(session.ID, AgentChatLiveEvent{
		Type: AgentChatLiveEventSessionUpdate,
		SessionUpdate: &AgentChatSessionResponse{
			Object: "agent_chat_session",
			Data:   renderAgentChatSession(session),
		},
	}, true)
}

// publishApprovalRequested notifies subscribers that a new approval
// is pending. Drop-on-full (no replacement) — approval events are
// each a discrete observation; clobbering one with another would
// silently lose the earlier event. Operators recover via the GET
// list endpoint on reconnect.
func (l *agentChatLive) publishApprovalRequested(payload AgentChatApprovalRequestedEvent) {
	l.publish(payload.SessionID, AgentChatLiveEvent{
		Type:              AgentChatLiveEventApprovalRequested,
		ApprovalRequested: &payload,
	}, false)
}

// publishApprovalResolved notifies subscribers that a pending approval
// reached a terminal state. Same drop-on-full rule as
// publishApprovalRequested.
func (l *agentChatLive) publishApprovalResolved(payload AgentChatApprovalResolvedEvent) {
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
	l.running[sessionID] = agentChatRunControl{cancel: cancel, done: make(chan struct{})}
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

func (l *agentChatLive) cancelRun(sessionID string) bool {
	l.mu.Lock()
	run, ok := l.running[sessionID]
	l.mu.Unlock()
	if !ok {
		return false
	}
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
	run.cancel()
	select {
	case <-run.done:
		return true
	case <-ctx.Done():
		return false
	}
}
