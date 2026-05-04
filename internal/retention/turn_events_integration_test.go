package retention

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/hecate/agent-runtime/internal/config"
	"github.com/hecate/agent-runtime/internal/profiler"
	"github.com/hecate/agent-runtime/internal/taskstate"
	"github.com/hecate/agent-runtime/pkg/types"
)

// TestManagerSweepsRealTurnEventsButSparesOtherTypes wires a real
// MemoryStore into the retention Manager (instead of the fake
// pruner used in retention_test.go) and verifies the end-to-end
// path: the configured TurnEvents policy reaches the store, only
// turn.completed rows are pruned, and the SubsystemResult
// returns the right deletion count.
//
// Without this test, a regression that — say — passed the wrong
// policy field to the pruner adapter, or accidentally widened the
// SQL filter to delete other event types, would only show up at
// runtime. The fakeTurnEventPruner in retention_test.go can't
// catch either class.
func TestManagerSweepsRealTurnEventsButSparesOtherTypes(t *testing.T) {
	t.Parallel()

	store := taskstate.NewMemoryStore()
	ctx := context.Background()

	// Seed: two stale turn events + one fresh turn event + one
	// stale run.finished event (must not be pruned by the sweep).
	stale := time.Now().UTC().Add(-10 * time.Hour)
	for i := 0; i < 2; i++ {
		_, err := store.AppendRunEvent(ctx, types.TaskRunEvent{
			TaskID:    "t",
			RunID:     "r",
			EventType: "turn.completed",
			CreatedAt: stale,
		})
		if err != nil {
			t.Fatalf("seed stale turn[%d]: %v", i, err)
		}
	}
	if _, err := store.AppendRunEvent(ctx, types.TaskRunEvent{
		TaskID:    "t",
		RunID:     "r",
		EventType: "turn.completed",
	}); err != nil {
		t.Fatalf("seed fresh turn: %v", err)
	}
	// Stale run.finished — same age as the stale turns. The sweep
	// must NOT delete it; only event_type='turn.completed'
	// is in scope.
	if _, err := store.AppendRunEvent(ctx, types.TaskRunEvent{
		TaskID:    "t",
		RunID:     "r",
		EventType: "run.finished",
		CreatedAt: stale,
	}); err != nil {
		t.Fatalf("seed stale run.finished: %v", err)
	}

	// Build the manager with TurnEvents enabled at MaxAge=1h. All
	// other subsystems are intentionally nil so they no-op.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tracer := profiler.NewInMemoryTracer(nil)
	manager := NewManager(
		logger,
		config.RetentionConfig{
			Enabled:    true,
			TurnEvents: config.RetentionPolicy{MaxAge: time.Hour},
		},
		tracer,
		nil, // traces
		nil, // budget events
		nil, // audit events
		nil, // provider history
		store,
		nil, // agent chat approvals
		nil, // history (in-memory not needed)
	)

	result := manager.Run(ctx, RunRequest{
		Trigger:    "test",
		Subsystems: []string{SubsystemTurnEvents},
	})

	// Locate the turn-events subsystem result.
	var sub *SubsystemResult
	for i := range result.Results {
		if result.Results[i].Name == SubsystemTurnEvents {
			sub = &result.Results[i]
			break
		}
	}
	if sub == nil {
		t.Fatalf("turn_events subsystem missing from result; got: %+v", result.Results)
	}
	if sub.Skipped {
		t.Fatalf("turn_events skipped; want it to run")
	}
	if sub.Error != "" {
		t.Fatalf("turn_events error: %s", sub.Error)
	}
	if sub.Deleted != 2 {
		t.Errorf("Deleted = %d, want 2 (the two stale turn events)", sub.Deleted)
	}

	// Confirm post-state: stale turn events gone, fresh turn event
	// kept, stale run.finished untouched.
	all, err := store.ListEvents(ctx, taskstate.EventFilter{Limit: 100})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	turnCount, runFinishedCount := 0, 0
	for _, e := range all {
		switch e.EventType {
		case "turn.completed":
			turnCount++
		case "run.finished":
			runFinishedCount++
		}
	}
	if turnCount != 1 {
		t.Errorf("surviving turn.completed = %d, want 1 (the fresh one)", turnCount)
	}
	if runFinishedCount != 1 {
		t.Errorf("surviving run.finished = %d, want 1 (sweep should not touch non-turn events)", runFinishedCount)
	}
}

// TestManagerCountCapDoesNotAffectNonTurnEvents pins down the
// count-cap branch's scope: even when MaxCount is exceeded by
// non-turn events present in the same store, only turn events
// should be pruned. The PruneTurnEvents implementations take the
// count over turn.completed rows specifically — but a
// regression that counted across all event types would silently
// delete operator-visible run.* events, which is the worst kind
// of forensic-data loss.
func TestManagerCountCapDoesNotAffectNonTurnEvents(t *testing.T) {
	t.Parallel()

	store := taskstate.NewMemoryStore()
	ctx := context.Background()

	// 4 turn events (we'll cap to 2) + 5 run.finished events. If
	// the cap mistakenly counted across types, it would either
	// (a) interpret "5 + 4 = 9 turn rows" and delete 7 (wrong), or
	// (b) include run.finished in the deletion candidate set. The
	// correct behavior is: keep the latest 2 turns, leave all 5
	// run.finished rows alone.
	for i := 0; i < 4; i++ {
		if _, err := store.AppendRunEvent(ctx, types.TaskRunEvent{
			TaskID: "t", RunID: "r", EventType: "turn.completed",
		}); err != nil {
			t.Fatalf("seed turn[%d]: %v", i, err)
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
			Enabled:    true,
			TurnEvents: config.RetentionPolicy{MaxCount: 2},
		},
		tracer,
		nil, nil, nil, nil, store, nil, nil,
	)
	result := manager.Run(ctx, RunRequest{
		Trigger:    "test",
		Subsystems: []string{SubsystemTurnEvents},
	})

	all, err := store.ListEvents(ctx, taskstate.EventFilter{Limit: 100})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	turnCount, runFinishedCount := 0, 0
	for _, e := range all {
		switch e.EventType {
		case "turn.completed":
			turnCount++
		case "run.finished":
			runFinishedCount++
		}
	}
	if turnCount != 2 {
		t.Errorf("surviving turn.completed = %d, want 2 (capped to MaxCount)", turnCount)
	}
	if runFinishedCount != 5 {
		t.Errorf("surviving run.finished = %d, want 5 (count cap must not touch non-turn events)", runFinishedCount)
	}

	// Result.Deleted should be 2 — only the two oldest turn rows.
	for _, sub := range result.Results {
		if sub.Name == SubsystemTurnEvents && sub.Deleted != 2 {
			t.Errorf("SubsystemResult.Deleted = %d, want 2 (4 turns - cap of 2)", sub.Deleted)
		}
	}
}
