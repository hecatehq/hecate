package api

import (
	"sync"
	"time"

	"github.com/hecatehq/hecate/internal/agentadapters"
)

// agentChatStreamCoalesceInterval bounds how often an in-flight
// external-agent turn persists + publishes its streamed updates. ACP
// adapters emit an agent_message_chunk (and agent_thought_chunk) per
// token; without coalescing, every chunk triggered a full-row
// chat.UpdateMessage write plus a full renderChatSession + SSE push —
// O(transcript) work at token frequency, the dominant source of chat
// UI lag on long turns. 50ms (~20 flushes/sec) stays below the
// threshold where streaming reads as choppy while cutting the
// persist/publish rate by an order of magnitude on fast streams.
const agentChatStreamCoalesceInterval = 50 * time.Millisecond

// chatStreamCoalescer batches an external-agent turn's streamed
// output + activity callbacks into at most one persist+publish per
// interval, on the leading edge: the first update after an idle gap
// flushes immediately (responsive), and bursts within the window are
// held and folded into the next flush — a later callback, the trailing
// timer, or close's final flush, whichever comes first.
//
// Correctness rests on the adapter contract that OnOutput/OnActivity fire only
// while RunRequest is executing, from the adapter's single reader goroutine,
// and that the handler calls close() after Run returns — i.e. once no further
// callbacks can arrive. Long-lived ACP terminal lifecycle events use the
// separate RunRequest.OnTerminalActivity sink, which is bound directly to the
// originating durable message and intentionally remains valid after Run. A
// trailing timer flushes a burst the window held when no later callback arrives to
// flush it, so a sparse stream can't hide an activity or partial output
// for the length of a tool/run pause. The flush runs under c.mu, so
// close() (which also takes c.mu) can neither overlap an in-flight
// trailing flush nor race a late timer fire: a timer that fires after
// close observes closed and is a no-op, leaving the post-run finalize
// the sole owner of terminal state. The mutex also keeps the type
// correct if an adapter ever calls back from multiple goroutines.
//
// Output is last-write-wins: each OnOutput carries the full
// accumulated transcript, so a dropped intermediate snapshot is
// harmless and the final value is always persisted (by the last
// flush, by close, or by the handler's post-run finalize). Activities
// are accumulate-not-drop: each OnActivity carries one record the
// handler merges by ID, so a dropped activity would be lost — the
// coalescer buffers them and the flush applies the whole batch in
// arrival order, preserving the per-call merge semantics.
type chatStreamCoalescer struct {
	flush    func(content string, haveContent bool, activities []agentadapters.Activity)
	interval time.Duration
	// clock is the time source for the coalesce window; production
	// uses time.Now. Tests override it to drive the window
	// deterministically without real sleeps.
	clock func() time.Time
	// afterFunc schedules the trailing flush; production wraps
	// time.AfterFunc. Tests override it to fire the timer on demand
	// instead of waiting real time. The returned func stops the timer
	// and reports whether it cancelled before firing.
	afterFunc func(d time.Duration, fn func()) func() bool

	mu          sync.Mutex
	lastFlush   time.Time
	closed      bool
	haveContent bool
	content     string
	activities  []agentadapters.Activity
	// stopTimer cancels the pending trailing flush; non-nil exactly
	// while a trailing timer is armed.
	stopTimer func() bool
}

func newChatStreamCoalescer(interval time.Duration, flush func(content string, haveContent bool, activities []agentadapters.Activity)) *chatStreamCoalescer {
	return &chatStreamCoalescer{
		flush:    flush,
		interval: interval,
		clock:    time.Now,
		afterFunc: func(d time.Duration, fn func()) func() bool {
			return time.AfterFunc(d, fn).Stop
		},
	}
}

// output records the latest full transcript snapshot and flushes when
// the coalesce window has elapsed since the last flush, else arms the
// trailing timer so the held snapshot flushes when the window closes.
func (c *chatStreamCoalescer) output(display string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.content = display
	c.haveContent = true
	c.maybeFlushLocked()
}

// activity buffers a streamed activity record and flushes when the
// coalesce window has elapsed since the last flush, else arms the
// trailing timer so the buffered batch flushes when the window closes.
func (c *chatStreamCoalescer) activity(act agentadapters.Activity) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.activities = append(c.activities, act)
	c.maybeFlushLocked()
}

// maybeFlushLocked flushes the pending batch when the coalesce window
// has elapsed, otherwise arms a trailing timer so a quiet burst still
// flushes when the window closes. Caller holds c.mu and has checked
// !closed.
func (c *chatStreamCoalescer) maybeFlushLocked() {
	now := c.clock()
	if !c.lastFlush.IsZero() && now.Sub(c.lastFlush) < c.interval {
		c.scheduleTrailingLocked(now)
		return
	}
	c.flushLocked(now)
}

// scheduleTrailingLocked arms a one-shot timer to flush the held batch
// when the current window closes, unless one is already armed. The
// window is anchored to lastFlush, so every callback held in the same
// window targets the same deadline and the first one's timer stands.
// Caller holds c.mu.
func (c *chatStreamCoalescer) scheduleTrailingLocked(now time.Time) {
	if c.stopTimer != nil {
		return
	}
	delay := c.interval - now.Sub(c.lastFlush)
	if delay < 0 {
		delay = 0
	}
	c.stopTimer = c.afterFunc(delay, c.trailingFlush)
}

// stopTimerLocked cancels a pending trailing timer if one is armed.
// Caller holds c.mu.
func (c *chatStreamCoalescer) stopTimerLocked() {
	if c.stopTimer != nil {
		c.stopTimer()
		c.stopTimer = nil
	}
}

// flushLocked sends the pending batch to the flush callback and records
// the flush time. The callback runs under c.mu by design: the adapter's
// single reader drives leading-edge flushes synchronously, and running
// the trailing-timer flush under the lock lets close() serialize with
// it — close takes c.mu, so it can't overlap an in-flight flush, and a
// timer that fires after close sees closed and bails. Caller holds c.mu
// and has checked !closed.
func (c *chatStreamCoalescer) flushLocked(now time.Time) {
	c.stopTimerLocked()
	c.lastFlush = now
	content, haveContent, activities := c.takePendingLocked()
	c.flush(content, haveContent, activities)
}

// trailingFlush is the timer callback. The timer that scheduled it has
// fired, so it clears stopTimer; if the coalescer closed or nothing is
// pending in the meantime it is a no-op, keeping close() and the
// post-run finalize the sole owners of terminal state.
func (c *chatStreamCoalescer) trailingFlush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopTimer = nil
	if c.closed {
		return
	}
	if !c.haveContent && len(c.activities) == 0 {
		return
	}
	c.flushLocked(c.clock())
}

// close cancels any pending trailing timer, flushes the remaining
// batch, and blocks further flushes. The handler calls it after Run
// returns and before writing the terminal message state, so buffered
// activities are persisted ahead of the finalize that appends terminal
// rows. Taking c.mu here is what serializes close with an in-flight or
// late trailing flush.
func (c *chatStreamCoalescer) close() {
	c.closeWithFlush(nil)
}

// closeWithFlush is close with an optional final-flush override. The external
// chat handler uses it after Run returns so the last durable activity batch
// shares the terminal persistence window; live flushes each use their own
// bounded persistence window.
func (c *chatStreamCoalescer) closeWithFlush(flush func(content string, haveContent bool, activities []agentadapters.Activity)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	c.stopTimerLocked()
	content, haveContent, activities := c.takePendingLocked()
	if haveContent || len(activities) > 0 {
		if flush == nil {
			flush = c.flush
		}
		flush(content, haveContent, activities)
	}
}

// takePendingLocked returns the pending batch and resets it. Caller
// holds c.mu.
func (c *chatStreamCoalescer) takePendingLocked() (string, bool, []agentadapters.Activity) {
	content := c.content
	haveContent := c.haveContent
	activities := c.activities
	c.content = ""
	c.haveContent = false
	c.activities = nil
	return content, haveContent, activities
}
