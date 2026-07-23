package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/chatapp"
)

var errAgentChatSettlementClosed = errors.New("agent chat terminal settlement is closed")

// agentChatSettlementRegistry owns one serializer per external-agent chat
// session. A dispatcher is reused by later turns while a terminal from an
// earlier turn is still alive, so every read/modify/write of either originating
// assistant row is ordered with current stream and final writes.
//
// The zero value is ready for use. Keeping it embedded in Handler also lets
// tests that construct a Handler literal exercise the production boundary.
type agentChatSettlementRegistry struct {
	mu          sync.Mutex
	dispatchers map[string]*agentChatSettlementDispatcher
	closed      bool
}

type agentChatSettlementDispatcher struct {
	registry  *agentChatSettlementRegistry
	handler   *Handler
	sessionID string

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	mu        sync.Mutex
	cond      *sync.Cond
	queue     []agentChatSettlementJob
	turns     int
	terminals map[string]struct{}
	owner     *agentChatLifecycleClosure
	sealed    bool
}

type agentChatSettlementJob struct {
	ctx            context.Context
	detached       bool
	publish        bool
	terminalClosed string
	releaseOwner   bool
	run            func(context.Context) (chat.Session, error)
	result         chan agentChatSettlementResult
}

type agentChatSettlementResult struct {
	session chat.Session
	err     error
}

type agentChatSettlementTurn struct {
	dispatcher *agentChatSettlementDispatcher
	messageID  string
	once       sync.Once
}

type agentChatSettlementClaim struct {
	dispatcher *agentChatSettlementDispatcher
}

func (r *agentChatSettlementRegistry) acquireTurn(h *Handler, sessionID, messageID string) (*agentChatSettlementTurn, error) {
	sessionID = strings.TrimSpace(sessionID)
	messageID = strings.TrimSpace(messageID)
	if h == nil || h.agentChat == nil || h.agentChatLive == nil || sessionID == "" || messageID == "" {
		return nil, fmt.Errorf("agent chat terminal settlement dependencies are required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, errAgentChatSettlementClosed
	}
	if r.dispatchers == nil {
		r.dispatchers = make(map[string]*agentChatSettlementDispatcher)
	}
	dispatcher := r.dispatchers[sessionID]
	if dispatcher != nil {
		dispatcher.mu.Lock()
		sealed := dispatcher.sealed
		if !sealed {
			dispatcher.turns++
		}
		dispatcher.mu.Unlock()
		if !sealed {
			return &agentChatSettlementTurn{dispatcher: dispatcher, messageID: messageID}, nil
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	dispatcher = &agentChatSettlementDispatcher{
		registry:  r,
		handler:   h,
		sessionID: sessionID,
		ctx:       ctx,
		cancel:    cancel,
		done:      make(chan struct{}),
		turns:     1,
		terminals: make(map[string]struct{}),
	}
	dispatcher.cond = sync.NewCond(&dispatcher.mu)
	r.dispatchers[sessionID] = dispatcher
	go dispatcher.run()
	return &agentChatSettlementTurn{dispatcher: dispatcher, messageID: messageID}, nil
}

func (r *agentChatSettlementRegistry) remove(dispatcher *agentChatSettlementDispatcher) {
	if dispatcher == nil {
		return
	}
	r.mu.Lock()
	if r.dispatchers[dispatcher.sessionID] == dispatcher {
		delete(r.dispatchers, dispatcher.sessionID)
	}
	r.mu.Unlock()
}

// claimSession attaches the closure that already owns the destructive token.
// Jobs that lost normal lifecycle admission after the epoch advanced can then
// settle under that owner without incrementing operations and deadlocking the
// caller that is waiting for them.
func (r *agentChatSettlementRegistry) claimSession(sessionID string, owner *agentChatLifecycleClosure) *agentChatSettlementClaim {
	sessionID = strings.TrimSpace(sessionID)
	if !owner.admitsSettlement(sessionID) {
		return &agentChatSettlementClaim{}
	}
	r.mu.Lock()
	dispatcher := r.dispatchers[sessionID]
	if dispatcher != nil {
		candidate := dispatcher
		candidate.mu.Lock()
		if candidate.sealed {
			dispatcher = nil
		} else {
			candidate.owner = owner
			candidate.cond.Broadcast()
		}
		candidate.mu.Unlock()
	}
	r.mu.Unlock()
	return &agentChatSettlementClaim{dispatcher: dispatcher}
}

// shutdown closes new turn admission after the runner has been asked to stop,
// then lets already-admitted HTTP handlers finish their durable final writes.
// Runner shutdown can return before those handlers unwind, so force-sealing a
// dispatcher here would strand its assistant row in running state. On deadline
// only, workers are sealed and cancelled; stores that honour context return
// promptly while a broken store remains fenced instead of accepting new work.
func (r *agentChatSettlementRegistry) shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	r.closed = true
	dispatchers := make([]*agentChatSettlementDispatcher, 0, len(r.dispatchers))
	for _, dispatcher := range r.dispatchers {
		dispatchers = append(dispatchers, dispatcher)
		dispatcher.mu.Lock()
		dispatcher.maybeSealIdleLocked()
		dispatcher.cond.Broadcast()
		dispatcher.mu.Unlock()
	}
	r.mu.Unlock()

	for _, dispatcher := range dispatchers {
		select {
		case <-dispatcher.done:
		case <-ctx.Done():
			for _, pending := range dispatchers {
				pending.mu.Lock()
				pending.sealed = true
				pending.cond.Broadcast()
				pending.mu.Unlock()
				pending.cancel()
			}
			return ctx.Err()
		}
	}
	return nil
}

func (t *agentChatSettlementTurn) finish() {
	if t == nil || t.dispatcher == nil {
		return
	}
	t.once.Do(func() {
		d := t.dispatcher
		d.mu.Lock()
		if d.turns > 0 {
			d.turns--
		}
		d.maybeSealIdleLocked()
		d.cond.Broadcast()
		d.mu.Unlock()
	})
}

func (t *agentChatSettlementTurn) updateMessage(ctx context.Context, publish bool, update func(*chat.Message)) (chat.Session, error) {
	if t == nil || t.dispatcher == nil || update == nil {
		return chat.Session{}, errAgentChatSettlementClosed
	}
	d := t.dispatcher
	messageID := t.messageID
	return d.submit(ctx, false, publish, func(writeCtx context.Context) (chat.Session, error) {
		return d.handler.agentChat.UpdateMessage(writeCtx, d.sessionID, messageID, update)
	})
}

func (t *agentChatSettlementTurn) updateSession(ctx context.Context, update func(*chat.Session)) (chat.Session, error) {
	if t == nil || t.dispatcher == nil || update == nil {
		return chat.Session{}, errAgentChatSettlementClosed
	}
	d := t.dispatcher
	return d.submit(ctx, false, false, func(writeCtx context.Context) (chat.Session, error) {
		return d.handler.agentChat.UpdateSession(writeCtx, d.sessionID, update)
	})
}

func (t *agentChatSettlementTurn) replaceNativeSession(ctx context.Context, cmd chatapp.ReplaceNativeSessionCommand) (chat.Session, error) {
	if t == nil || t.dispatcher == nil {
		return chat.Session{}, errAgentChatSettlementClosed
	}
	d := t.dispatcher
	return d.submit(ctx, false, false, func(writeCtx context.Context) (chat.Session, error) {
		result, err := d.handler.chatApplication().ReplaceNativeSession(writeCtx, cmd)
		if err != nil {
			return chat.Session{}, err
		}
		return result.Session, nil
	})
}

func (t *agentChatSettlementTurn) currentSession(ctx context.Context, publish bool) (chat.Session, error) {
	if t == nil || t.dispatcher == nil {
		return chat.Session{}, errAgentChatSettlementClosed
	}
	return t.dispatcher.submit(ctx, false, publish, func(readCtx context.Context) (chat.Session, error) {
		session, ok, err := t.dispatcher.handler.agentChat.Get(readCtx, t.dispatcher.sessionID)
		if err != nil {
			return chat.Session{}, err
		}
		if !ok {
			return chat.Session{}, fmt.Errorf("agent chat session %q not found", t.dispatcher.sessionID)
		}
		return session, nil
	})
}

// settledSession reads and publishes the final External Agent snapshot inside
// the settlement serializer. Keeping the publication in the submitted
// operation prevents a later queued metadata/activity write from overtaking
// the read before the snapshot is marked as fully settled.
func (t *agentChatSettlementTurn) settledSession(ctx context.Context) (chat.Session, error) {
	if t == nil || t.dispatcher == nil {
		return chat.Session{}, errAgentChatSettlementClosed
	}
	d := t.dispatcher
	return d.submit(ctx, false, false, func(readCtx context.Context) (chat.Session, error) {
		session, ok, err := d.handler.agentChat.Get(readCtx, d.sessionID)
		if err != nil {
			return chat.Session{}, err
		}
		if !ok {
			return chat.Session{}, fmt.Errorf("agent chat session %q not found", d.sessionID)
		}
		d.handler.agentChatLive.publishSettledSession(session, t.messageID)
		return session, nil
	})
}

// terminalActivity is intentionally enqueue-only. ACP calls it while holding
// its terminal callback-order mutex; storage latency must never delay terminal
// output drain or consume the adapter shutdown budget.
func (t *agentChatSettlementTurn) terminalActivity(activity agentadapters.Activity) {
	if t == nil || t.dispatcher == nil || strings.TrimSpace(activity.Type) != "terminal" {
		return
	}
	d := t.dispatcher
	terminalID := terminalIDFromActivity(activity.ID)
	d.mu.Lock()
	if d.sealed {
		d.mu.Unlock()
		return
	}
	if activity.Status == "running" && terminalID != "" {
		d.terminals[terminalID] = struct{}{}
	}
	messageID := t.messageID
	d.queue = append(d.queue, agentChatSettlementJob{
		detached: true,
		publish:  true,
		run: func(writeCtx context.Context) (chat.Session, error) {
			return d.handler.agentChat.UpdateMessage(writeCtx, d.sessionID, messageID, func(message *chat.Message) {
				message.Activities = mergeChatActivity(message.Activities, agentChatActivityFromAdapter(activity))
			})
		},
	})
	d.cond.Signal()
	d.mu.Unlock()
}

func (t *agentChatSettlementTurn) terminalClosed(terminalID string) {
	if t == nil || t.dispatcher == nil {
		return
	}
	terminalID = strings.TrimSpace(terminalID)
	if terminalID == "" {
		return
	}
	d := t.dispatcher
	d.mu.Lock()
	if !d.sealed {
		d.queue = append(d.queue, agentChatSettlementJob{terminalClosed: terminalID})
		d.cond.Signal()
	}
	d.mu.Unlock()
}

func terminalIDFromActivity(activityID string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(activityID), "terminal:"))
}

func (d *agentChatSettlementDispatcher) submit(ctx context.Context, detached, publish bool, run func(context.Context) (chat.Session, error)) (chat.Session, error) {
	if d == nil || run == nil {
		return chat.Session{}, errAgentChatSettlementClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	result := make(chan agentChatSettlementResult, 1)
	d.mu.Lock()
	if d.sealed {
		d.mu.Unlock()
		return chat.Session{}, errAgentChatSettlementClosed
	}
	d.queue = append(d.queue, agentChatSettlementJob{ctx: ctx, detached: detached, publish: publish, run: run, result: result})
	d.cond.Signal()
	d.mu.Unlock()
	select {
	case settled := <-result:
		return settled.session, settled.err
	case <-ctx.Done():
		return chat.Session{}, ctx.Err()
	case <-d.ctx.Done():
		return chat.Session{}, errAgentChatSettlementClosed
	}
}

func (d *agentChatSettlementDispatcher) run() {
	defer close(d.done)
	defer d.cancel()
	defer d.registry.remove(d)
	for {
		d.mu.Lock()
		for len(d.queue) == 0 && !d.sealed {
			d.cond.Wait()
		}
		if len(d.queue) == 0 && d.sealed {
			d.mu.Unlock()
			return
		}
		job := d.queue[0]
		d.queue[0] = agentChatSettlementJob{}
		d.queue = d.queue[1:]
		d.mu.Unlock()

		result := d.execute(job)
		d.mu.Lock()
		if job.terminalClosed != "" {
			delete(d.terminals, job.terminalClosed)
		}
		if job.releaseOwner {
			d.owner = nil
		}
		d.maybeSealIdleLocked()
		d.mu.Unlock()
		if job.result != nil {
			job.result <- result
		}
	}
}

func (d *agentChatSettlementDispatcher) execute(job agentChatSettlementJob) agentChatSettlementResult {
	if job.run == nil {
		return agentChatSettlementResult{}
	}
	ctx, cleanup := d.jobContext(job)
	defer cleanup()

	var release func()
	if job.detached {
		var err error
		release, err = d.admitDetached(ctx)
		if err != nil {
			d.logTerminalSettlementFailure(ctx, err)
			return agentChatSettlementResult{err: err}
		}
		defer release()
	}
	session, err := job.run(ctx)
	if err != nil {
		if job.detached {
			d.logTerminalSettlementFailure(ctx, err)
		}
		return agentChatSettlementResult{err: err}
	}
	if job.publish {
		d.handler.agentChatLive.publishSession(session)
	}
	return agentChatSettlementResult{session: session}
}

func (d *agentChatSettlementDispatcher) jobContext(job agentChatSettlementJob) (context.Context, func()) {
	base := job.ctx
	baseCancel := func() {}
	if job.detached {
		base, baseCancel = context.WithTimeout(context.Background(), agentChatTerminalWriteTimeout)
	} else if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithCancel(base)
	stop := context.AfterFunc(d.ctx, cancel)
	return ctx, func() {
		stop()
		cancel()
		baseCancel()
	}
}

func (d *agentChatSettlementDispatcher) admitDetached(ctx context.Context) (func(), error) {
	for {
		d.mu.Lock()
		owner := d.owner
		d.mu.Unlock()
		if owner != nil && owner.admitsSettlement(d.sessionID) {
			return func() {}, nil
		}

		lifecycle := d.handler.agentChatLive.snapshotLifecycle(d.sessionID)
		release, accepted := d.handler.agentChatLive.beginLifecycleOperation(lifecycle)
		lifecycle.release()
		if accepted {
			return release, nil
		}

		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (d *agentChatSettlementDispatcher) maybeSealIdleLocked() {
	if d.owner == nil && d.turns == 0 && len(d.terminals) == 0 && len(d.queue) == 0 {
		d.sealed = true
		d.cond.Broadcast()
	}
}

func (d *agentChatSettlementDispatcher) logTerminalSettlementFailure(ctx context.Context, err error) {
	if d == nil || d.handler == nil || d.handler.logger == nil || err == nil {
		return
	}
	d.handler.logger.WarnContext(ctx, "chat.external_agent.terminal_activity_update_failed",
		"session_id", d.sessionID,
		"error", err,
	)
}

// sealAndDrain closes callback admission before waiting. runner CloseSession
// must have returned first, which guarantees its authoritative activity and
// OnTerminalClosed marker were already enqueued in that order.
func (c *agentChatSettlementClaim) sealAndDrain(ctx context.Context) bool {
	if c == nil || c.dispatcher == nil {
		return true
	}
	d := c.dispatcher
	d.mu.Lock()
	d.sealed = true
	d.cond.Broadcast()
	d.mu.Unlock()
	select {
	case <-d.done:
		return true
	case <-ctx.Done():
		d.cancel()
		return false
	}
}

// relinquish inserts an ordered owner-release barrier. Jobs already ahead of
// the barrier may use the destructive owner; callbacks arriving behind it must
// regain ordinary lifecycle admission after the caller releases the closure.
// This is used when a destructive request aborts before closing the runner.
func (c *agentChatSettlementClaim) relinquish(ctx context.Context) bool {
	if c == nil || c.dispatcher == nil {
		return true
	}
	if ctx == nil {
		ctx = context.Background()
	}
	d := c.dispatcher
	result := make(chan agentChatSettlementResult, 1)
	d.mu.Lock()
	if d.sealed {
		d.mu.Unlock()
		select {
		case <-d.done:
			return true
		case <-ctx.Done():
			return false
		}
	}
	d.queue = append(d.queue, agentChatSettlementJob{releaseOwner: true, result: result})
	d.cond.Signal()
	d.mu.Unlock()
	select {
	case <-result:
		return true
	case <-ctx.Done():
		return false
	}
}

func (c *agentChatSettlementClaim) releaseLifecycleAfterRelinquish(owner *agentChatLifecycleClosure) {
	if owner == nil {
		return
	}
	if c == nil || c.dispatcher == nil {
		owner.release()
		return
	}
	go func() {
		_ = c.relinquish(context.Background())
		owner.release()
	}()
}

// releaseLifecycleAfterDrain preserves the destructive fence when a broken or
// context-ignoring store outlives the caller's drain deadline. The handler can
// return "stopping" promptly, while a retry remains rejected until the worker
// actually exits and it is safe to reopen lifecycle admission.
func (c *agentChatSettlementClaim) releaseLifecycleAfterDrain(owner *agentChatLifecycleClosure) {
	if owner == nil {
		return
	}
	if c == nil || c.dispatcher == nil {
		owner.release()
		return
	}
	go func() {
		<-c.dispatcher.done
		owner.release()
	}()
}
