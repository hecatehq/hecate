package api

import (
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/agentadapters"
)

type coalesceFlush struct {
	content     string
	haveContent bool
	activities  []agentadapters.Activity
}

// newTestCoalescer wires a coalescer to a fake clock the test drives by
// reassigning *now and a fake trailing timer the test fires via the
// returned func, plus a recorder capturing every flush. The coalescer
// arms at most one trailing timer at a time, so the fake holds a single
// pending callback; firing or cancelling it clears the slot.
func newTestCoalescer(interval time.Duration) (*chatStreamCoalescer, *[]coalesceFlush, *time.Time, func()) {
	var flushes []coalesceFlush
	now := time.Unix(0, 0)
	c := newChatStreamCoalescer(interval, func(content string, haveContent bool, activities []agentadapters.Activity) {
		flushes = append(flushes, coalesceFlush{content: content, haveContent: haveContent, activities: activities})
	})
	c.clock = func() time.Time { return now }

	var pending func()
	c.afterFunc = func(_ time.Duration, fn func()) func() bool {
		pending = fn
		return func() bool {
			if pending == nil {
				return false
			}
			pending = nil
			return true
		}
	}
	fireTimer := func() {
		fn := pending
		pending = nil
		if fn != nil {
			fn()
		}
	}
	return c, &flushes, &now, fireTimer
}

func TestChatStreamCoalescerLeadingEdgeFlushesFirstUpdate(t *testing.T) {
	c, flushes, _, _ := newTestCoalescer(50 * time.Millisecond)

	c.output("hello")

	if len(*flushes) != 1 {
		t.Fatalf("first output: got %d flushes, want 1 (leading edge)", len(*flushes))
	}
	if got := (*flushes)[0]; !got.haveContent || got.content != "hello" {
		t.Fatalf("first flush = %+v, want content %q with haveContent", got, "hello")
	}
}

func TestChatStreamCoalescerHoldsBurstAndKeepsLatestContent(t *testing.T) {
	c, flushes, now, _ := newTestCoalescer(50 * time.Millisecond)

	c.output("a") // leading-edge flush at t=0
	*now = now.Add(10 * time.Millisecond)
	c.output("ab") // within window: held
	*now = now.Add(10 * time.Millisecond)
	c.output("abc") // still within window (<50ms since flush): held

	if len(*flushes) != 1 {
		t.Fatalf("within-window outputs: got %d flushes, want 1", len(*flushes))
	}

	*now = now.Add(40 * time.Millisecond) // now 60ms since last flush
	c.output("abcd")

	if len(*flushes) != 2 {
		t.Fatalf("post-window output: got %d flushes, want 2", len(*flushes))
	}
	// Intermediate "ab"/"abc" snapshots are dropped (last-write-wins);
	// the second flush carries only the latest accumulated content.
	if got := (*flushes)[1].content; got != "abcd" {
		t.Fatalf("second flush content = %q, want %q", got, "abcd")
	}
}

func TestChatStreamCoalescerBuffersActivitiesInOrder(t *testing.T) {
	c, flushes, now, _ := newTestCoalescer(50 * time.Millisecond)

	c.output("a") // leading-edge flush; arms the window
	*now = now.Add(5 * time.Millisecond)
	c.activity(agentadapters.Activity{ID: "act-1"})
	*now = now.Add(5 * time.Millisecond)
	c.activity(agentadapters.Activity{ID: "act-2"})

	// Both activities arrived inside the window: buffered, not flushed.
	if len(*flushes) != 1 {
		t.Fatalf("buffered activities: got %d flushes, want 1", len(*flushes))
	}

	c.close() // trailing flush must carry the buffered batch

	if len(*flushes) != 2 {
		t.Fatalf("after close: got %d flushes, want 2", len(*flushes))
	}
	got := (*flushes)[1].activities
	if len(got) != 2 || got[0].ID != "act-1" || got[1].ID != "act-2" {
		t.Fatalf("close flush activities = %+v, want [act-1 act-2] in order", got)
	}
}

func TestChatStreamCoalescerTrailingTimerFlushesQuietBurst(t *testing.T) {
	c, flushes, now, fireTimer := newTestCoalescer(50 * time.Millisecond)

	c.output("a") // leading-edge flush at t=0; arms the window
	*now = now.Add(10 * time.Millisecond)
	c.activity(agentadapters.Activity{ID: "act-1"}) // held within window

	// No later callback arrives. Without a trailing timer this activity
	// would stay hidden until close(); the timer fires when the window
	// closes and flushes it.
	if len(*flushes) != 1 {
		t.Fatalf("within-window activity: got %d flushes, want 1", len(*flushes))
	}

	*now = now.Add(40 * time.Millisecond) // window closed: t=50
	fireTimer()

	if len(*flushes) != 2 {
		t.Fatalf("after trailing timer: got %d flushes, want 2", len(*flushes))
	}
	got := (*flushes)[1].activities
	if len(got) != 1 || got[0].ID != "act-1" {
		t.Fatalf("trailing flush activities = %+v, want [act-1]", got)
	}
}

func TestChatStreamCoalescerCloseFlushesPendingThenStops(t *testing.T) {
	c, flushes, now, _ := newTestCoalescer(50 * time.Millisecond)

	c.output("a") // leading-edge flush
	*now = now.Add(10 * time.Millisecond)
	c.output("ab") // held within window

	c.close()
	if len(*flushes) != 2 {
		t.Fatalf("close with pending: got %d flushes, want 2", len(*flushes))
	}
	if got := (*flushes)[1].content; got != "ab" {
		t.Fatalf("close flush content = %q, want %q", got, "ab")
	}

	// Late callbacks after close must not flush — the post-run finalize
	// owns terminal state and a stale re-publish would clobber it.
	*now = now.Add(time.Second)
	c.output("late")
	c.activity(agentadapters.Activity{ID: "late"})
	if len(*flushes) != 2 {
		t.Fatalf("post-close callbacks: got %d flushes, want 2 (no-op after close)", len(*flushes))
	}
}

func TestChatStreamCoalescerCloseCancelsPendingTrailingTimer(t *testing.T) {
	c, flushes, now, fireTimer := newTestCoalescer(50 * time.Millisecond)

	c.output("a") // leading-edge flush at t=0
	*now = now.Add(10 * time.Millisecond)
	c.output("ab") // held within window; arms the trailing timer

	c.close() // flushes the pending batch and cancels the timer
	if len(*flushes) != 2 {
		t.Fatalf("close with pending: got %d flushes, want 2", len(*flushes))
	}
	if got := (*flushes)[1].content; got != "ab" {
		t.Fatalf("close flush content = %q, want %q", got, "ab")
	}

	// The cancelled timer firing late must not flush — close() and the
	// finalize own terminal state.
	fireTimer()
	if len(*flushes) != 2 {
		t.Fatalf("fired cancelled timer: got %d flushes, want 2 (no-op)", len(*flushes))
	}
}

func TestChatStreamCoalescerCloseWithoutPendingIsNoOp(t *testing.T) {
	c, flushes, now, _ := newTestCoalescer(50 * time.Millisecond)

	c.output("a") // leading-edge flush consumes the pending content
	c.close()     // nothing pending -> no extra flush
	if len(*flushes) != 1 {
		t.Fatalf("close without pending: got %d flushes, want 1", len(*flushes))
	}

	*now = now.Add(time.Second)
	c.close() // idempotent
	if len(*flushes) != 1 {
		t.Fatalf("second close: got %d flushes, want 1 (idempotent)", len(*flushes))
	}
}
