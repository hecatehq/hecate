package api

import (
	"context"
	"sync"

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
	// turnSettled marks the External Agent handler's final publication after
	// session metadata and turn counters have finished their ordered writes.
	// Other terminal-looking snapshots can occur while a new or completing
	// turn is still active and must not close the stream. settledMessageID
	// binds that evidence to the exact terminal assistant row so a delayed
	// marker cannot settle a newer turn.
	turnSettled      bool
	settledMessageID string
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
	activeTurns map[string]*agentChatTurnControl
	mutating    map[string]struct{}
	lifecycles  map[string]*agentChatLifecycleState
	// turnAdmissionClosed is permanent for this live registry. Handler
	// shutdown closes admission and snapshots active turns under the same
	// mutex, so every registration either joins that snapshot or is rejected.
	turnAdmissionClosed bool
	snapshot            agentChatSnapshotConfig
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

type agentChatTurnRegistration uint8

const (
	agentChatTurnAccepted agentChatTurnRegistration = iota
	agentChatTurnBusy
	agentChatTurnAdmissionClosed
)

type agentChatTurnControl struct {
	cancel context.CancelFunc
	done   chan struct{}
	// reason records the trigger for a runtime-controlled cancel
	// (operator Stop/close/delete or handler shutdown). The handler reads
	// this when the turn terminates so the cancellation counter can label
	// the first cancellation authority.
	// Protected by agentChatLive.mu so selecting a turn and recording the
	// authority are one operation with respect to handler shutdown.
	reason string
}

// markCancelReasonLocked installs the first cancellation authority. The
// caller must hold agentChatLive.mu so selecting the turn and recording the
// reason cannot be interleaved with shutdown's turn snapshot.
func (c *agentChatTurnControl) markCancelReasonLocked(reason string) {
	if c.reason == "" {
		c.reason = reason
	}
}

// cancelReasonLocked reports the recorded reason or empty string. The caller
// must hold agentChatLive.mu.
func (c *agentChatTurnControl) cancelReasonLocked() string {
	return c.reason
}

func newAgentChatLive(snapshot agentChatSnapshotConfig) *agentChatLive {
	return &agentChatLive{
		subscribers: make(map[string]map[chan AgentChatLiveEvent]struct{}),
		activeTurns: make(map[string]*agentChatTurnControl),
		mutating:    make(map[string]struct{}),
		lifecycles:  make(map[string]*agentChatLifecycleState),
		snapshot:    snapshot,
	}
}

func (l *agentChatLive) subscribe(sessionID string) (<-chan AgentChatLiveEvent, func()) {
	ch, _, unsubscribe := l.subscribeWithTurnState(sessionID)
	return ch, unsubscribe
}

// subscribeWithTurnState atomically installs a subscriber and reports whether
// a turn was already admitted. A reconnect that lands during terminal
// settlement must remember that it observed live work even if clearTurn races
// the subsequent durable session read.
func (l *agentChatLive) subscribeWithTurnState(sessionID string) (<-chan AgentChatLiveEvent, bool, func()) {
	ch := make(chan AgentChatLiveEvent, 16)
	l.mu.Lock()
	if l.subscribers[sessionID] == nil {
		l.subscribers[sessionID] = make(map[chan AgentChatLiveEvent]struct{})
	}
	l.subscribers[sessionID][ch] = struct{}{}
	_, turnActive := l.activeTurns[sessionID]
	l.mu.Unlock()

	return ch, turnActive, func() {
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
	l.publishSessionWithSettlement(session, "")
}

func (l *agentChatLive) publishSettledSession(session chat.Session, messageID string) {
	l.publishSessionWithSettlement(session, messageID)
}

func (l *agentChatLive) publishSessionWithSettlement(session chat.Session, settledMessageID string) {
	l.publish(session.ID, AgentChatLiveEvent{
		Type: AgentChatLiveEventSessionUpdate,
		SessionUpdate: &ChatSessionResponse{
			Object: "chat_session",
			Data:   renderChatSession(session, l.snapshot),
		},
		turnSettled:      settledMessageID != "",
		settledMessageID: settledMessageID,
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
	if _, active := l.activeTurns[sessionID]; active {
		return
	}
	if _, mutating := l.mutating[sessionID]; mutating {
		return
	}
	if l.lifecycles[sessionID] == state {
		delete(l.lifecycles, sessionID)
	}
}

// registerTurn is the single admission point for every Chat execution path.
// Destructive session operations close admission before checking for a live
// turn, so registration either wins first and is cancelled and awaited, or is
// rejected before a message append, attachment claim, or provider dispatch.
func (l *agentChatLive) registerTurn(snapshot agentChatLifecycleSnapshot, cancel context.CancelFunc) agentChatTurnRegistration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.turnAdmissionClosed {
		return agentChatTurnAdmissionClosed
	}
	if _, current := l.snapshotCurrentLocked(snapshot); !current {
		return agentChatTurnAdmissionClosed
	}
	if _, exists := l.activeTurns[snapshot.sessionID]; exists {
		return agentChatTurnBusy
	}
	if _, exists := l.mutating[snapshot.sessionID]; exists {
		return agentChatTurnBusy
	}
	l.activeTurns[snapshot.sessionID] = &agentChatTurnControl{cancel: cancel, done: make(chan struct{})}
	return agentChatTurnAccepted
}

// beginExclusiveMutation admits a settings mutation that must exclude chat
// turns without masquerading as a cancellable turn. Releasing the mutation
// advances the lifecycle generation so any turn that read the session before
// the winning write cannot later register and execute with that stale
// snapshot. Count it as a lifecycle operation so delete/close waits for the
// durable settings write to settle.
func (l *agentChatLive) beginExclusiveMutation(snapshot agentChatLifecycleSnapshot) (func(), agentChatTurnRegistration) {
	l.mu.Lock()
	state, current := l.snapshotCurrentLocked(snapshot)
	if !current {
		l.mu.Unlock()
		return nil, agentChatTurnAdmissionClosed
	}
	if _, exists := l.activeTurns[snapshot.sessionID]; exists {
		l.mu.Unlock()
		return nil, agentChatTurnBusy
	}
	if _, exists := l.mutating[snapshot.sessionID]; exists {
		l.mu.Unlock()
		return nil, agentChatTurnBusy
	}
	if state.operations == 0 {
		state.operationsDrained = make(chan struct{})
	}
	state.operations++
	l.mutating[snapshot.sessionID] = struct{}{}
	l.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			delete(l.mutating, snapshot.sessionID)
			state := l.lifecycles[snapshot.sessionID]
			if state != nil {
				// Invalidate snapshots captured before or during the mutation,
				// regardless of whether the store write succeeded.
				state.epoch++
				if state.operations > 0 {
					state.operations--
					if state.operations == 0 {
						close(state.operationsDrained)
						state.operationsDrained = nil
					}
				}
				l.reapLifecycleLocked(snapshot.sessionID, state)
			}
			l.mu.Unlock()
		})
	}, agentChatTurnAccepted
}

// beginLifecycleOperation admits a non-turn mutation that must finish before a
// destructive session operation can proceed. Attachment creation and native
// config writes use this path because neither owns a registered turn, but both
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

// closeSessionLifecycle blocks new turns and counted operations, advances the
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

func (l *agentChatLive) clearTurn(sessionID string) {
	l.mu.Lock()
	turn, ok := l.activeTurns[sessionID]
	if ok {
		delete(l.activeTurns, sessionID)
		close(turn.done)
	}
	l.reapLifecycleLocked(sessionID, l.lifecycles[sessionID])
	l.mu.Unlock()
}

func (l *agentChatLive) hasTurn(sessionID string) bool {
	l.mu.Lock()
	_, ok := l.activeTurns[sessionID]
	l.mu.Unlock()
	return ok
}

// turnCancelReason reports the first cancellation reason recorded for the
// given session (operator-driven via cancelTurn* or handler shutdown) or empty
// if no turn is registered or no runtime authority cancelled it. The handler
// uses this on the turn-completion path for closed-set metric attribution.
func (l *agentChatLive) turnCancelReason(sessionID string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	turn, ok := l.activeTurns[sessionID]
	if !ok || turn == nil {
		return ""
	}
	return turn.cancelReasonLocked()
}

func (l *agentChatLive) cancelTurn(sessionID string) bool {
	l.mu.Lock()
	turn, ok := l.activeTurns[sessionID]
	if ok && turn != nil {
		turn.markCancelReasonLocked("operator")
	}
	l.mu.Unlock()
	if !ok || turn == nil {
		return false
	}
	turn.cancel()
	return true
}

func (l *agentChatLive) cancelTurnAndWait(ctx context.Context, sessionID string) bool {
	l.mu.Lock()
	turn, ok := l.activeTurns[sessionID]
	if ok && turn != nil {
		turn.markCancelReasonLocked("operator")
	}
	l.mu.Unlock()
	if !ok || turn == nil {
		return true
	}
	turn.cancel()
	select {
	case <-turn.done:
		return true
	case <-ctx.Done():
		return false
	}
}

// cancelAllTurns permanently closes turn admission, then marks and snapshots
// every registered turn without waiting for settlement. Admission closure and
// the snapshot share one critical section: a concurrent registration either
// wins first and is included here, or observes the closed gate. First reason
// wins, so an operator Stop that selected a turn before shutdown keeps its more
// specific attribution. Handler.Shutdown calls this before draining the
// adapter runtime so custom runners cannot leave detached turn contexts alive.
func (l *agentChatLive) cancelAllTurns(reason string) {
	l.mu.Lock()
	l.turnAdmissionClosed = true
	turns := make([]*agentChatTurnControl, 0, len(l.activeTurns))
	for _, turn := range l.activeTurns {
		if turn == nil {
			continue
		}
		turn.markCancelReasonLocked(reason)
		turns = append(turns, turn)
	}
	l.mu.Unlock()
	for _, turn := range turns {
		turn.cancel()
	}
}
