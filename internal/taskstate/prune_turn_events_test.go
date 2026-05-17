package taskstate

import (
	"context"
	"testing"
	"time"

	"github.com/hecate/agent-runtime/pkg/types"
)

// TestPrune_AgeAndCount exercises the retention sweep
// against every Store implementation that ships in this binary
// (memory + sqlite). Each backend should:
//
//   - delete `turn.completed` rows older than maxAge
//   - leave other event types alone (run.finished, approval.*, etc.)
//   - when maxCount > 0, keep only the most-recent maxCount turn rows
//     by sequence DESC, dropping older surviving turns
//
// The age path is verified by injecting a stale CreatedAt directly;
// the count path uses real append order.
func TestPrune_AgeAndCount(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		open func(t *testing.T) Store
	}{
		{"memory", func(*testing.T) Store { return NewMemoryStore() }},
		{"sqlite", func(t *testing.T) Store { return newSQLiteTestStore(t) }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := tc.open(t)
			ctx := context.Background()

			// Stale (10h ago) turn that the age sweep should drop.
			stale, err := store.AppendRunEvent(ctx, types.TaskRunEvent{
				TaskID:    "t-1",
				RunID:     "r-1",
				EventType: "turn.completed",
				CreatedAt: time.Now().UTC().Add(-10 * time.Hour),
			})
			if err != nil {
				t.Fatalf("append stale: %v", err)
			}

			// Stale run.finished — must NOT be touched even though it's
			// older than the cutoff. The sweep only targets turn rows.
			_, err = store.AppendRunEvent(ctx, types.TaskRunEvent{
				TaskID:    "t-1",
				RunID:     "r-1",
				EventType: "run.finished",
				CreatedAt: time.Now().UTC().Add(-10 * time.Hour),
			})
			if err != nil {
				t.Fatalf("append run.finished: %v", err)
			}

			// Fresh turn — must survive the age sweep.
			fresh, err := store.AppendRunEvent(ctx, types.TaskRunEvent{
				TaskID:    "t-1",
				RunID:     "r-1",
				EventType: "turn.completed",
			})
			if err != nil {
				t.Fatalf("append fresh: %v", err)
			}

			// Sweep with 1h cutoff (no count cap).
			n, err := store.Prune(ctx, time.Hour, 0)
			if err != nil {
				t.Fatalf("Prune(age): %v", err)
			}
			if n != 1 {
				t.Fatalf("age sweep deleted = %d, want 1", n)
			}

			events, err := store.ListEvents(ctx, EventFilter{Limit: 100})
			if err != nil {
				t.Fatalf("ListEvents: %v", err)
			}
			gotStale := false
			gotRunFinished := false
			gotFresh := false
			for _, e := range events {
				if e.Sequence == stale.Sequence {
					gotStale = true
				}
				if e.Sequence == fresh.Sequence {
					gotFresh = true
				}
				if e.EventType == "run.finished" {
					gotRunFinished = true
				}
			}
			if gotStale {
				t.Errorf("stale turn survived age sweep")
			}
			if !gotFresh {
				t.Errorf("fresh turn was deleted by age sweep")
			}
			if !gotRunFinished {
				t.Errorf("run.finished was deleted — sweep should only touch turn.completed")
			}

			// --- count cap ---
			// Append four more fresh turn events so we have five total
			// surviving. With maxCount=2, the three oldest survivors
			// should be dropped, leaving two most-recent.
			for i := 0; i < 4; i++ {
				_, err := store.AppendRunEvent(ctx, types.TaskRunEvent{
					TaskID:    "t-1",
					RunID:     "r-1",
					EventType: "turn.completed",
				})
				if err != nil {
					t.Fatalf("append fresh[%d]: %v", i, err)
				}
			}

			n, err = store.Prune(ctx, 0, 2)
			if err != nil {
				t.Fatalf("Prune(count): %v", err)
			}
			if n != 3 {
				t.Fatalf("count sweep deleted = %d, want 3", n)
			}

			events, err = store.ListEvents(ctx, EventFilter{
				EventTypes: []string{"turn.completed"},
				Limit:      100,
			})
			if err != nil {
				t.Fatalf("ListEvents(turn): %v", err)
			}
			if len(events) != 2 {
				t.Fatalf("after count cap len = %d, want 2", len(events))
			}
		})
	}
}

// TestPrune_NoOpWithZeroBounds confirms the sweep is a
// genuine no-op when both maxAge and maxCount are zero — the worker
// uses zero to disable a particular bound.
func TestPrune_NoOpWithZeroBounds(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		open func(t *testing.T) Store
	}{
		{"memory", func(*testing.T) Store { return NewMemoryStore() }},
		{"sqlite", func(t *testing.T) Store { return newSQLiteTestStore(t) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := tc.open(t)
			ctx := context.Background()
			for i := 0; i < 3; i++ {
				_, err := store.AppendRunEvent(ctx, types.TaskRunEvent{
					TaskID:    "t-noop",
					RunID:     "r-noop",
					EventType: "turn.completed",
					CreatedAt: time.Now().UTC().Add(-100 * time.Hour),
				})
				if err != nil {
					t.Fatalf("append: %v", err)
				}
			}
			n, err := store.Prune(ctx, 0, 0)
			if err != nil {
				t.Fatalf("Prune(0, 0): %v", err)
			}
			if n != 0 {
				t.Fatalf("deleted = %d, want 0 (both bounds disabled)", n)
			}
		})
	}
}
