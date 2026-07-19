package taskstate

import (
	"context"
	"testing"
	"time"

	"github.com/hecatehq/hecate/pkg/types"
)

// TestPrune_AgeAndCount exercises the retention sweep
// against every Store implementation that ships in this binary
// (memory + sqlite). Each backend should:
//
//   - delete `model.call.completed` rows older than maxAge
//   - leave other event types alone (run.finished, approval.*, etc.)
//   - when maxCount > 0, keep only the most-recent maxCount model-call rows
//     by sequence DESC, dropping older surviving model calls
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

			// Stale (10h ago) model call that the age sweep should drop.
			stale, err := store.AppendRunEvent(ctx, types.TaskRunEvent{
				TaskID:    "t-1",
				RunID:     "r-1",
				EventType: "model.call.completed",
				CreatedAt: time.Now().UTC().Add(-10 * time.Hour),
			})
			if err != nil {
				t.Fatalf("append stale: %v", err)
			}

			// Stale run.finished — must NOT be touched even though it's
			// older than the cutoff. The sweep only targets model-call rows.
			_, err = store.AppendRunEvent(ctx, types.TaskRunEvent{
				TaskID:    "t-1",
				RunID:     "r-1",
				EventType: "run.finished",
				CreatedAt: time.Now().UTC().Add(-10 * time.Hour),
			})
			if err != nil {
				t.Fatalf("append run.finished: %v", err)
			}

			// Fresh model call — must survive the age sweep.
			fresh, err := store.AppendRunEvent(ctx, types.TaskRunEvent{
				TaskID:    "t-1",
				RunID:     "r-1",
				EventType: "model.call.completed",
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
				t.Errorf("stale model call survived age sweep")
			}
			if !gotFresh {
				t.Errorf("fresh model call was deleted by age sweep")
			}
			if !gotRunFinished {
				t.Errorf("run.finished was deleted — sweep should only touch model.call.completed")
			}

			// --- count cap ---
			// Append four more fresh model-call events so we have five total
			// surviving. With maxCount=2, the three oldest survivors
			// should be dropped, leaving two most-recent.
			for i := 0; i < 4; i++ {
				_, err := store.AppendRunEvent(ctx, types.TaskRunEvent{
					TaskID:    "t-1",
					RunID:     "r-1",
					EventType: "model.call.completed",
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
				EventTypes: []string{"model.call.completed"},
				Limit:      100,
			})
			if err != nil {
				t.Fatalf("ListEvents(model call): %v", err)
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
					EventType: "model.call.completed",
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
