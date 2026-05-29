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
// held and folded into the next flush (or into close's trailing
// flush).
//
// Correctness rests on the adapter contract that OnOutput/OnActivity
// fire only while RunRequest is executing, from the adapter's single
// reader goroutine, and that the handler calls close() after Run
// returns — i.e. once no further callbacks can arrive. Under that
// contract flushes never overlap, so there is deliberately no
// background timer goroutine that could race the post-run finalize;
// the trailing flush is close's job. The mutex still guards the
// pending state so the type stays correct if an adapter ever calls
// back from multiple goroutines.
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

	mu          sync.Mutex
	lastFlush   time.Time
	closed      bool
	haveContent bool
	content     string
	activities  []agentadapters.Activity
}

func newChatStreamCoalescer(interval time.Duration, flush func(content string, haveContent bool, activities []agentadapters.Activity)) *chatStreamCoalescer {
	return &chatStreamCoalescer{flush: flush, interval: interval, clock: time.Now}
}

// output records the latest full transcript snapshot and flushes when
// the coalesce window has elapsed since the last flush.
func (c *chatStreamCoalescer) output(display string) {
	c.mu.Lock()
	c.content = display
	c.haveContent = true
	c.maybeFlushLocked()
}

// activity buffers a streamed activity record and flushes when the
// coalesce window has elapsed since the last flush.
func (c *chatStreamCoalescer) activity(act agentadapters.Activity) {
	c.mu.Lock()
	c.activities = append(c.activities, act)
	c.maybeFlushLocked()
}

// maybeFlushLocked flushes the pending batch when the coalesce window
// has elapsed. It is called with c.mu held and always releases the
// lock before returning — the flush callback performs IO (SQLite
// write + SSE publish) and must not run under the lock.
func (c *chatStreamCoalescer) maybeFlushLocked() {
	if c.closed {
		c.mu.Unlock()
		return
	}
	now := c.clock()
	if !c.lastFlush.IsZero() && now.Sub(c.lastFlush) < c.interval {
		c.mu.Unlock()
		return
	}
	c.lastFlush = now
	content, haveContent, activities := c.takePendingLocked()
	c.mu.Unlock()
	c.flush(content, haveContent, activities)
}

// close flushes any remaining pending batch and blocks further
// flushes. The handler calls it after Run returns and before writing
// the terminal message state, so buffered activities are persisted
// ahead of the finalize that appends terminal rows.
func (c *chatStreamCoalescer) close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	content, haveContent, activities := c.takePendingLocked()
	c.mu.Unlock()
	if haveContent || len(activities) > 0 {
		c.flush(content, haveContent, activities)
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
