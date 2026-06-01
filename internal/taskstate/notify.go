package taskstate

import "sync"

// runEventBus is a per-run wake fan-out shared by the store
// implementations. Writers call signalRun after committing a mutation;
// subscribers receive a coalesced wake on a buffered channel and
// re-read the run's state from the store.
//
// It carries no payload on purpose: the store stays the single source
// of truth, so a coalesced or duplicated wake is always safe (the
// reader just re-reads). That keeps the run stream event-driven
// without the busy-poll loop the SSE handler used to run, while every
// mutation method — steps, artifacts, run status, approvals, appended
// events — funnels through the same store and so reaches subscribers.
type runEventBus struct {
	mu   sync.Mutex
	subs map[string]map[chan struct{}]struct{}
}

// SubscribeRun registers interest in mutations for runID. The returned
// channel is buffered with depth 1 so a signal never blocks a writer
// and repeated signals between reads coalesce into a single wake. The
// cleanup func must be called (deferred) when the subscriber is done;
// it removes the channel under the lock so no later signal can reach
// it. The channel is intentionally not closed — the subscriber owns
// its lifetime and stops reading before unsubscribing, so closing
// would only add a send-on-closed hazard.
func (b *runEventBus) SubscribeRun(runID string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	b.mu.Lock()
	if b.subs == nil {
		b.subs = make(map[string]map[chan struct{}]struct{})
	}
	if b.subs[runID] == nil {
		b.subs[runID] = make(map[chan struct{}]struct{})
	}
	b.subs[runID][ch] = struct{}{}
	b.mu.Unlock()

	return ch, func() {
		b.mu.Lock()
		if set := b.subs[runID]; set != nil {
			delete(set, ch)
			if len(set) == 0 {
				delete(b.subs, runID)
			}
		}
		b.mu.Unlock()
	}
}

// signalRun wakes every subscriber for runID. The send is
// non-blocking: a full buffer already means an unconsumed wake is
// pending, and since the wake carries no data, dropping the duplicate
// loses nothing. b.mu is a leaf lock here — signalRun never calls back
// into the store — so holding a store lock across this call cannot
// deadlock.
func (b *runEventBus) signalRun(runID string) {
	if runID == "" {
		return
	}
	b.mu.Lock()
	for ch := range b.subs[runID] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	b.mu.Unlock()
}
