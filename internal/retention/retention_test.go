package retention

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/hecate/agent-runtime/internal/config"
	"github.com/hecate/agent-runtime/internal/profiler"
)

type fakeBudgetPruner struct{ deleted int }

func (f fakeBudgetPruner) PruneEvents(context.Context, time.Duration, int) (int, error) {
	return f.deleted, nil
}

type fakeAuditPruner struct{ deleted int }

func (f fakeAuditPruner) PruneAuditEvents(context.Context, time.Duration, int) (int, error) {
	return f.deleted, nil
}

type fakeTurnEventPruner struct{ deleted int }

func (f fakeTurnEventPruner) PruneTurnEvents(context.Context, time.Duration, int) (int, error) {
	return f.deleted, nil
}

func TestManagerRunFiltersSubsystems(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tracer := profiler.NewInMemoryTracer(nil)
	manager := NewManager(
		logger,
		config.RetentionConfig{
			Enabled:  true,
			Interval: time.Minute,
			TraceSnapshots: config.RetentionPolicy{
				MaxAge:   time.Hour,
				MaxCount: 10,
			},
			BudgetEvents: config.RetentionPolicy{
				MaxAge:   time.Hour,
				MaxCount: 5,
			},
			AuditEvents: config.RetentionPolicy{
				MaxAge:   time.Hour,
				MaxCount: 5,
			},
			TurnEvents: config.RetentionPolicy{
				MaxAge:   time.Hour,
				MaxCount: 100,
			},
		},
		tracer,
		tracer,
		fakeBudgetPruner{deleted: 2},
		fakeAuditPruner{deleted: 3},
		nil,
		fakeTurnEventPruner{deleted: 6},
		nil,
		NewMemoryHistoryStore(),
	)

	trace := tracer.Start("old-trace")
	trace.Finalize()

	result := manager.Run(context.Background(), RunRequest{
		Trigger:    "manual",
		Subsystems: []string{SubsystemBudgetEvents, SubsystemTurnEvents},
	})
	if result.Trigger != "manual" {
		t.Fatalf("trigger = %q, want manual", result.Trigger)
	}
	if len(result.Results) != 6 {
		t.Fatalf("results = %d, want 6", len(result.Results))
	}

	if result.Results[1].Name != SubsystemBudgetEvents || result.Results[1].Deleted != 2 {
		t.Fatalf("budget result = %#v, want budget deletion count 2", result.Results[1])
	}
	if result.Results[4].Name != SubsystemTurnEvents || result.Results[4].Deleted != 6 {
		t.Fatalf("turn events result = %#v, want turn-event deletion count 6", result.Results[4])
	}
	if !result.Results[0].Skipped || !result.Results[2].Skipped || !result.Results[3].Skipped {
		t.Fatalf("unexpected skip flags: %#v", result.Results)
	}
}

func TestManagerRunPersistsHistory(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tracer := profiler.NewInMemoryTracer(nil)
	history := NewMemoryHistoryStore()
	manager := NewManager(
		logger,
		config.RetentionConfig{
			Enabled:        true,
			Interval:       time.Minute,
			TraceSnapshots: config.RetentionPolicy{MaxAge: time.Hour, MaxCount: 10},
			BudgetEvents:   config.RetentionPolicy{MaxAge: time.Hour, MaxCount: 5},
			AuditEvents:    config.RetentionPolicy{MaxAge: time.Hour, MaxCount: 5},
			TurnEvents:     config.RetentionPolicy{MaxAge: time.Hour, MaxCount: 100},
		},
		tracer,
		tracer,
		fakeBudgetPruner{deleted: 2},
		fakeAuditPruner{deleted: 3},
		nil,
		fakeTurnEventPruner{deleted: 6},
		nil,
		history,
	)

	manager.Run(context.Background(), RunRequest{
		Trigger:   "manual",
		Actor:     "admin:req-1",
		RequestID: "req-1",
	})

	runs, err := manager.ListRuns(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	if runs[0].Actor != "admin:req-1" {
		t.Fatalf("actor = %q, want admin:req-1", runs[0].Actor)
	}
	if runs[0].RequestID != "req-1" {
		t.Fatalf("request_id = %q, want req-1", runs[0].RequestID)
	}
	if len(runs[0].Results) != 6 {
		t.Fatalf("results = %d, want 6", len(runs[0].Results))
	}
}
