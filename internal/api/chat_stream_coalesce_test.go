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

// newTestCoalescer wires a coalescer to a fake clock the test drives
// by reassigning *now, plus a recorder capturing every flush.
func newTestCoalescer(interval time.Duration) (*chatStreamCoalescer, *[]coalesceFlush, *time.Time) {
	var flushes []coalesceFlush
	now := time.Unix(0, 0)
	c := newChatStreamCoalescer(interval, func(content string, haveContent bool, activities []agentadapters.Activity) {
		flushes = append(flushes, coalesceFlush{content: content, haveContent: haveContent, activities: activities})
	})
	c.clock = func() time.Time { return now }
	return c, &flushes, &now
}

func TestChatStreamCoalescerLeadingEdgeFlushesFirstUpdate(t *testing.T) {
	c, flushes, _ := newTestCoalescer(50 * time.Millisecond)

	c.output("hello")

	if len(*flushes) != 1 {
		t.Fatalf("first output: got %d flushes, want 1 (leading edge)", len(*flushes))
	}
	if got := (*flushes)[0]; !got.haveContent || got.content != "hello" {
		t.Fatalf("first flush = %+v, want content %q with haveContent", got, "hello")
	}
}

func TestChatStreamCoalescerHoldsBurstAndKeepsLatestContent(t *testing.T) {
	c, flushes, now := newTestCoalescer(50 * time.Millisecond)

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
	c, flushes, now := newTestCoalescer(50 * time.Millisecond)

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

func TestChatStreamCoalescerCloseFlushesPendingThenStops(t *testing.T) {
	c, flushes, now := newTestCoalescer(50 * time.Millisecond)

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

func TestChatStreamCoalescerCloseWithoutPendingIsNoOp(t *testing.T) {
	c, flushes, now := newTestCoalescer(50 * time.Millisecond)

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
