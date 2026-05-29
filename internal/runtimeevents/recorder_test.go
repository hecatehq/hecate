package runtimeevents_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/runtimeevents"
	"github.com/hecatehq/hecate/internal/taskstate"
)

func TestRecorder_AppendAttachesSnapshotAndClonesData(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	now := time.Date(2026, 5, 29, 12, 30, 0, 0, time.FixedZone("offset", 3600))
	recorder := runtimeevents.NewRecorder(store,
		runtimeevents.WithClock(func() time.Time { return now }),
		runtimeevents.WithSnapshot(func(_ context.Context, taskID, runID string) (map[string]any, error) {
			if taskID != "task-1" || runID != "run-1" {
				t.Fatalf("snapshot ids = %q/%q, want task-1/run-1", taskID, runID)
			}
			return map[string]any{
				"snapshot": map[string]any{"run_id": runID},
			}, nil
		}),
	)
	data := map[string]any{"note": "original"}

	event, err := recorder.Append(ctx, runtimeevents.Event{
		TaskID:       "task-1",
		RunID:        "run-1",
		EventType:    "external.event",
		Data:         data,
		RequestID:    "req-1",
		TraceID:      "trace-1",
		SnapshotMode: runtimeevents.SnapshotRequired,
	})
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	data["note"] = "mutated"

	if event.CreatedAt.Location() != time.UTC || !event.CreatedAt.Equal(now) {
		t.Fatalf("CreatedAt = %v (%v), want UTC instant %v", event.CreatedAt, event.CreatedAt.Location(), now)
	}
	if event.RequestID != "req-1" || event.TraceID != "trace-1" {
		t.Fatalf("request/trace = %q/%q, want req-1/trace-1", event.RequestID, event.TraceID)
	}
	if event.Data["note"] != "original" {
		t.Fatalf("event note = %v, want original cloned value", event.Data["note"])
	}
	snapshot, ok := event.Data["snapshot"].(map[string]any)
	if !ok || snapshot["run_id"] != "run-1" {
		t.Fatalf("event snapshot = %#v, want run_id=run-1", event.Data["snapshot"])
	}
}

func TestRecorder_AppendSnapshotProvidesBaseLetsEventDataOverrideSnapshot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	recorder := runtimeevents.NewRecorder(store,
		runtimeevents.WithSnapshot(func(context.Context, string, string) (map[string]any, error) {
			return map[string]any{
				"run":    "snapshot-run",
				"reason": "snapshot-reason",
			}, nil
		}),
	)

	event, err := recorder.Append(ctx, runtimeevents.Event{
		TaskID:            "task-1",
		RunID:             "run-1",
		EventType:         "run.note",
		Data:              map[string]any{"reason": "caller-reason"},
		SnapshotMode:      runtimeevents.SnapshotRequired,
		SnapshotPlacement: runtimeevents.SnapshotProvidesBase,
	})
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if event.Data["run"] != "snapshot-run" {
		t.Fatalf("run = %v, want snapshot-run", event.Data["run"])
	}
	if event.Data["reason"] != "caller-reason" {
		t.Fatalf("reason = %v, want caller override", event.Data["reason"])
	}
}

func TestRecorder_AppendSnapshotOverridesDataLetsSnapshotOverrideEventData(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	recorder := runtimeevents.NewRecorder(store,
		runtimeevents.WithSnapshot(func(context.Context, string, string) (map[string]any, error) {
			return map[string]any{
				"snapshot": "snapshot-value",
				"reason":   "snapshot-reason",
			}, nil
		}),
	)

	event, err := recorder.Append(ctx, runtimeevents.Event{
		TaskID:            "task-1",
		RunID:             "run-1",
		EventType:         "external.event",
		Data:              map[string]any{"reason": "caller-reason"},
		SnapshotMode:      runtimeevents.SnapshotRequired,
		SnapshotPlacement: runtimeevents.SnapshotOverridesData,
	})
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if event.Data["snapshot"] != "snapshot-value" {
		t.Fatalf("snapshot = %v, want snapshot-value", event.Data["snapshot"])
	}
	if event.Data["reason"] != "snapshot-reason" {
		t.Fatalf("reason = %v, want snapshot override", event.Data["reason"])
	}
}

func TestRecorder_AppendOptionalSnapshotFailureStillWrites(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	recorder := runtimeevents.NewRecorder(store,
		runtimeevents.WithSnapshot(func(context.Context, string, string) (map[string]any, error) {
			return nil, errors.New("snapshot unavailable")
		}),
	)

	event, err := recorder.Append(ctx, runtimeevents.Event{
		TaskID:       "task-1",
		RunID:        "run-1",
		EventType:    "external.event",
		Data:         map[string]any{"note": "still write"},
		SnapshotMode: runtimeevents.SnapshotBestEffort,
	})
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if event.Data["note"] != "still write" {
		t.Fatalf("note = %v, want still write", event.Data["note"])
	}

	events, err := store.ListRunEvents(ctx, "task-1", "run-1", 0, 10)
	if err != nil {
		t.Fatalf("ListRunEvents() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
}

func TestRecorder_AppendRequiredSnapshotFailureReturnsError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	recorder := runtimeevents.NewRecorder(store,
		runtimeevents.WithSnapshot(func(context.Context, string, string) (map[string]any, error) {
			return nil, errors.New("snapshot unavailable")
		}),
	)

	if _, err := recorder.Append(ctx, runtimeevents.Event{
		TaskID:       "task-1",
		RunID:        "run-1",
		EventType:    "run.note",
		SnapshotMode: runtimeevents.SnapshotRequired,
	}); err == nil {
		t.Fatal("Append() error = nil, want snapshot error")
	}
	events, err := store.ListRunEvents(ctx, "task-1", "run-1", 0, 10)
	if err != nil {
		t.Fatalf("ListRunEvents() error = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %d, want no write after required snapshot failure", len(events))
	}
}

func TestRecorder_AppendRequiredSnapshotWithoutFunctionReturnsError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	recorder := runtimeevents.NewRecorder(store)

	if _, err := recorder.Append(ctx, runtimeevents.Event{
		TaskID:       "task-1",
		RunID:        "run-1",
		EventType:    "run.note",
		SnapshotMode: runtimeevents.SnapshotRequired,
	}); err == nil {
		t.Fatal("Append() error = nil, want missing snapshot function error")
	}
	events, err := store.ListRunEvents(ctx, "task-1", "run-1", 0, 10)
	if err != nil {
		t.Fatalf("ListRunEvents() error = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %d, want no write after missing snapshot function", len(events))
	}
}
