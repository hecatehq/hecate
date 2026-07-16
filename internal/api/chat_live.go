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
	lifecycles  map[string]*agentChatLifecycleState
	snapshot    agentChatSnapshotConfig
}

// agentChatLifecycleSnapshot binds work admitted after a session read to the
// same lifecycle generation that was current before that read. Destructive
// mutations advance the generation on both entry and exit, so a request that
// was delayed either before or during a close cannot use a stale native handle
// after admission reopens.
type agentChatLifecycleSnapshot struct {
	sessionID string
	epoch     uint64
	lease     *agentChatLifecycleLease
}

type agentChatLifecycleState struct {
	epoch             uint64
	closures          int
	operations        int
	snapshots         int
	operationsDrained chan struct{}
	destructive       chan struct{}
}

type agentChatLifecycleLease struct {
	live      *agentChatLive
	sessionID string
	released  bool
}

type agentChatLifecycleClosure struct {
	live              *agentChatLive
	sessionID         string
	drained           <-chan struct{}
	destructive       chan struct{}
	destructiveMu     sync.Mutex
	destructiveWaited bool
	destructiveHeld   bool
	finished          bool
	once              sync.Once
}

type agentChatRunRegistration uint8

const (
	agentChatRunAccepted agentChatRunRegistration = iota
	agentChatRunBusy
	agentChatRunAdmissionClosed
)

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
		lifecycles:  make(map[string]*agentChatLifecycleState),
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

// snapshotLifecycle must run before loading a session whose execution handles
// will be used later. The returned snapshot is a lease and must be released by
// the caller. Leases keep a post-close generation alive only while a request
// that could have observed an older generation remains in flight; once every
// lease and operation drains, the per-session state can be reclaimed safely.
func (l *agentChatLive) snapshotLifecycle(sessionID string) agentChatLifecycleSnapshot {
	l.mu.Lock()
	defer l.mu.Unlock()
	state := l.lifecycles[sessionID]
	if state == nil {
		state = &agentChatLifecycleState{}
		l.lifecycles[sessionID] = state
	}
	state.snapshots++
	return agentChatLifecycleSnapshot{
		sessionID: sessionID,
		epoch:     state.epoch,
		lease:     &agentChatLifecycleLease{live: l, sessionID: sessionID},
	}
}

func (s agentChatLifecycleSnapshot) release() {
	if s.lease == nil || s.lease.live == nil {
		return
	}
	live := s.lease.live
	live.mu.Lock()
	defer live.mu.Unlock()
	if s.lease.released {
		return
	}
	s.lease.released = true
	state := live.lifecycles[s.lease.sessionID]
	if state != nil && state.snapshots > 0 {
		state.snapshots--
		live.reapLifecycleLocked(s.lease.sessionID, state)
	}
}

func (l *agentChatLive) snapshotCurrentLocked(snapshot agentChatLifecycleSnapshot) (*agentChatLifecycleState, bool) {
	if snapshot.lease == nil || snapshot.lease.live != l || snapshot.lease.sessionID != snapshot.sessionID || snapshot.lease.released {
		return nil, false
	}
	state := l.lifecycles[snapshot.sessionID]
	if state == nil || state.closures > 0 || state.epoch != snapshot.epoch {
		return nil, false
	}
	return state, true
}

func (l *agentChatLive) reapLifecycleLocked(sessionID string, state *agentChatLifecycleState) {
	if state == nil || state.closures > 0 || state.operations > 0 || state.snapshots > 0 {
		return
	}
	if _, running := l.running[sessionID]; running {
		return
	}
	if l.lifecycles[sessionID] == state {
		delete(l.lifecycles, sessionID)
	}
}

// registerRun is the single admission point for every chat execution path.
// Destructive session operations close admission before checking for a live
// run, so registration either wins first and is cancelled and awaited, or is
// rejected before a message append, attachment claim, or provider dispatch.
func (l *agentChatLive) registerRun(snapshot agentChatLifecycleSnapshot, cancel context.CancelFunc) agentChatRunRegistration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, current := l.snapshotCurrentLocked(snapshot); !current {
		return agentChatRunAdmissionClosed
	}
	if _, exists := l.running[snapshot.sessionID]; exists {
		return agentChatRunBusy
	}
	l.running[snapshot.sessionID] = &agentChatRunControl{cancel: cancel, done: make(chan struct{})}
	return agentChatRunAccepted
}

// beginLifecycleOperation admits a non-run mutation that must finish before a
// destructive session operation can proceed. Attachment creation and native
// config writes use this path because neither owns a registered run, but both
// can otherwise commit through a stale session snapshot while delete/close is
// cleaning up that same owner.
func (l *agentChatLive) beginLifecycleOperation(snapshot agentChatLifecycleSnapshot) (func(), bool) {
	l.mu.Lock()
	state, current := l.snapshotCurrentLocked(snapshot)
	if !current {
		l.mu.Unlock()
		return nil, false
	}
	if state.operations == 0 {
		state.operationsDrained = make(chan struct{})
	}
	state.operations++
	l.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			state := l.lifecycles[snapshot.sessionID]
			if state != nil && state.operations > 0 {
				state.operations--
				if state.operations == 0 {
					close(state.operationsDrained)
					state.operationsDrained = nil
					l.reapLifecycleLocked(snapshot.sessionID, state)
				}
			}
			l.mu.Unlock()
		})
	}, true
}

// closeSessionLifecycle blocks new runs and counted operations, advances the
// lifecycle epoch, and exposes the pre-existing operation drain to the caller.
// The caller must wait for its serialized destructive turn and pre-existing
// operations before destructive work, then hold the closure through that work.
// release is idempotent and advances the epoch again so snapshots captured
// while the closure was active stay stale after reopen.
func (l *agentChatLive) closeSessionLifecycle(sessionID string) *agentChatLifecycleClosure {
	l.mu.Lock()
	state := l.lifecycles[sessionID]
	if state == nil {
		state = &agentChatLifecycleState{}
		l.lifecycles[sessionID] = state
	}
	if state.destructive == nil {
		state.destructive = make(chan struct{}, 1)
		state.destructive <- struct{}{}
	}
	state.epoch++
	state.closures++
	drained := closedAgentChatLifecycleDrain()
	if state.operations > 0 {
		drained = state.operationsDrained
	}
	l.mu.Unlock()
	return &agentChatLifecycleClosure{
		live:        l,
		sessionID:   sessionID,
		drained:     drained,
		destructive: state.destructive,
	}
}

func closedAgentChatLifecycleDrain() <-chan struct{} {
	drained := make(chan struct{})
	close(drained)
	return drained
}

func (c *agentChatLifecycleClosure) waitForOperations(ctx context.Context) bool {
	if c == nil || c.destructive == nil {
		return false
	}
	c.destructiveMu.Lock()
	if c.finished || c.destructiveWaited {
		c.destructiveMu.Unlock()
		return false
	}
	c.destructiveWaited = true
	c.destructiveMu.Unlock()

	select {
	case <-c.destructive:
		c.destructiveMu.Lock()
		if c.finished {
			c.destructiveMu.Unlock()
			c.destructive <- struct{}{}
			return false
		}
		c.destructiveHeld = true
		c.destructiveMu.Unlock()
	case <-ctx.Done():
		return false
	}
	select {
	case <-c.drained:
		return true
	case <-ctx.Done():
		return false
	}
}

// admitsSettlement reports whether this closure currently owns the serialized
// destructive turn for sessionID. Close-generated terminal callbacks cannot
// enter through beginLifecycleOperation after the epoch advances; the owner is
// therefore allowed to settle them directly, provided it drains the settlement
// dispatcher before mutating or deleting the transcript.
func (c *agentChatLifecycleClosure) admitsSettlement(sessionID string) bool {
	if c == nil || c.sessionID != sessionID {
		return false
	}
	c.destructiveMu.Lock()
	defer c.destructiveMu.Unlock()
	return c.destructiveWaited && c.destructiveHeld && !c.finished
}

func (c *agentChatLifecycleClosure) release() {
	if c == nil || c.live == nil {
		return
	}
	c.once.Do(func() {
		c.live.mu.Lock()
		state := c.live.lifecycles[c.sessionID]
		if state != nil {
			state.epoch++
			if state.closures > 0 {
				state.closures--
			}
			c.live.reapLifecycleLocked(c.sessionID, state)
		}
		c.live.mu.Unlock()

		c.destructiveMu.Lock()
		c.finished = true
		held := c.destructiveHeld
		c.destructiveHeld = false
		c.destructiveMu.Unlock()
		if held {
			c.destructive <- struct{}{}
		}
	})
}

func (l *agentChatLive) clearRun(sessionID string) {
	l.mu.Lock()
	run, ok := l.running[sessionID]
	if ok {
		delete(l.running, sessionID)
		close(run.done)
	}
	l.reapLifecycleLocked(sessionID, l.lifecycles[sessionID])
	l.mu.Unlock()
}

func (l *agentChatLive) hasRun(sessionID string) bool {
	l.mu.Lock()
	_, ok := l.running[sessionID]
	l.mu.Unlock()
	return ok
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
