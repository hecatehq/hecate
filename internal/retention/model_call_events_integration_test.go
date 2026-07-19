package retention

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

// TestManagerSweepsRealModelCallEventsButSparesOtherTypes wires a real
// MemoryStore into the retention Manager (instead of the fake
// pruner used in retention_test.go) and verifies the end-to-end
// path: the configured ModelCallEvents policy reaches the store, only
// model.call.completed rows are pruned, and the SubsystemResult
// returns the right deletion count.
//
// Without this test, a regression that — say — passed the wrong
// policy field to the pruner adapter, or accidentally widened the
// SQL filter to delete other event types, would only show up at
// runtime. The fakeModelCallEventPruner in retention_test.go can't
// catch either class.
func TestManagerSweepsRealModelCallEventsButSparesOtherTypes(t *testing.T) {
	t.Parallel()

	store := taskstate.NewMemoryStore()
	ctx := context.Background()

	// Seed: two stale model-call events + one fresh model-call event + one
	// stale run.finished event (must not be pruned by the sweep).
	stale := time.Now().UTC().Add(-10 * time.Hour)
	for i := 0; i < 2; i++ {
		_, err := store.AppendRunEvent(ctx, types.TaskRunEvent{
			TaskID:    "t",
			RunID:     "r",
			EventType: "model.call.completed",
			CreatedAt: stale,
		})
		if err != nil {
			t.Fatalf("seed stale model call[%d]: %v", i, err)
		}
	}
	if _, err := store.AppendRunEvent(ctx, types.TaskRunEvent{
		TaskID:    "t",
		RunID:     "r",
		EventType: "model.call.completed",
	}); err != nil {
		t.Fatalf("seed fresh model call: %v", err)
	}
	// Stale run.finished — same age as the stale model calls. The sweep
	// must NOT delete it; only event_type='model.call.completed'
	// is in scope.
	if _, err := store.AppendRunEvent(ctx, types.TaskRunEvent{
		TaskID:    "t",
		RunID:     "r",
		EventType: "run.finished",
		CreatedAt: stale,
	}); err != nil {
		t.Fatalf("seed stale run.finished: %v", err)
	}

	// Build the manager with ModelCallEvents enabled at MaxAge=1h. All
	// other subsystems are intentionally nil so they no-op.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tracer := profiler.NewInMemoryTracer(nil)
	manager := NewManager(
		logger,
		config.RetentionConfig{
			Enabled:         true,
			ModelCallEvents: config.RetentionPolicy{MaxAge: time.Hour},
		},
		tracer,
		nil, // traces
		nil, // usage events
		nil, // audit events
		nil, // provider history
		store,
		nil, // agent chat approvals
		nil, // history (in-memory not needed)
	)

	result := manager.Run(ctx, RunRequest{
		Trigger:    "test",
		Subsystems: []string{SubsystemModelCallEvents},
	})

	// Locate the model-call-events subsystem result.
	var sub *SubsystemResult
	for i := range result.Results {
		if result.Results[i].Name == SubsystemModelCallEvents {
			sub = &result.Results[i]
			break
		}
	}
	if sub == nil {
		t.Fatalf("model_call_events subsystem missing from result; got: %+v", result.Results)
	}
	if sub.Skipped {
		t.Fatalf("model_call_events skipped; want it to run")
	}
	if sub.Error != "" {
		t.Fatalf("model_call_events error: %s", sub.Error)
	}
	if sub.Deleted != 2 {
		t.Errorf("Deleted = %d, want 2 (the two stale model-call events)", sub.Deleted)
	}

	// Confirm post-state: stale model-call events gone, fresh model-call event
	// kept, stale run.finished untouched.
	all, err := store.ListEvents(ctx, taskstate.EventFilter{Limit: 100})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	modelCallCount, runFinishedCount := 0, 0
	for _, e := range all {
		switch e.EventType {
		case "model.call.completed":
			modelCallCount++
		case "run.finished":
			runFinishedCount++
		}
	}
	if modelCallCount != 1 {
		t.Errorf("surviving model.call.completed = %d, want 1 (the fresh one)", modelCallCount)
	}
	if runFinishedCount != 1 {
		t.Errorf("surviving run.finished = %d, want 1 (sweep should not touch non-model-call events)", runFinishedCount)
	}
}

// TestManagerCountCapDoesNotAffectNonModelCallEvents pins down the
// count-cap branch's scope: even when MaxCount is exceeded by
// non-model-call events present in the same store, only model-call events
// should be pruned. The Prune implementations take the
// count over model.call.completed rows specifically — but a
// regression that counted across all event types would silently
// delete operator-visible run.* events, which is the worst kind
// of forensic-data loss.
func TestManagerCountCapDoesNotAffectNonModelCallEvents(t *testing.T) {
	t.Parallel()

	store := taskstate.NewMemoryStore()
	ctx := context.Background()

	// 4 model-call events (we'll cap to 2) + 5 run.finished events. If
	// the cap mistakenly counted across types, it would either
	// (a) interpret "5 + 4 = 9 model-call rows" and delete 7 (wrong), or
	// (b) include run.finished in the deletion candidate set. The
	// correct behavior is: keep the latest 2 model calls, leave all 5
	// run.finished rows alone.
	for i := 0; i < 4; i++ {
		if _, err := store.AppendRunEvent(ctx, types.TaskRunEvent{
			TaskID: "t", RunID: "r", EventType: "model.call.completed",
		}); err != nil {
			t.Fatalf("seed model call[%d]: %v", i, err)
		}
	}
	for i := 0; i < 5; i++ {
		if _, err := store.AppendRunEvent(ctx, types.TaskRunEvent{
			TaskID: "t", RunID: "r", EventType: "run.finished",
		}); err != nil {
			t.Fatalf("seed run.finished[%d]: %v", i, err)
		}
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tracer := profiler.NewInMemoryTracer(nil)
	manager := NewManager(
		logger,
		config.RetentionConfig{
			Enabled:         true,
			ModelCallEvents: config.RetentionPolicy{MaxCount: 2},
		},
		tracer,
		nil, nil, nil, nil, store, nil, nil,
	)
	result := manager.Run(ctx, RunRequest{
		Trigger:    "test",
		Subsystems: []string{SubsystemModelCallEvents},
	})

	all, err := store.ListEvents(ctx, taskstate.EventFilter{Limit: 100})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	modelCallCount, runFinishedCount := 0, 0
	for _, e := range all {
		switch e.EventType {
		case "model.call.completed":
			modelCallCount++
		case "run.finished":
			runFinishedCount++
		}
	}
	if modelCallCount != 2 {
		t.Errorf("surviving model.call.completed = %d, want 2 (capped to MaxCount)", modelCallCount)
	}
	if runFinishedCount != 5 {
		t.Errorf("surviving run.finished = %d, want 5 (count cap must not touch non-model-call events)", runFinishedCount)
	}

	// Result.Deleted should be 2 — only the two oldest model-call rows.
	for _, sub := range result.Results {
		if sub.Name == SubsystemModelCallEvents && sub.Deleted != 2 {
			t.Errorf("SubsystemResult.Deleted = %d, want 2 (4 model calls - cap of 2)", sub.Deleted)
		}
	}
}
