package orchestrator

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/hecate/agent-runtime/internal/profiler"
	"github.com/hecate/agent-runtime/internal/taskstate"
	"github.com/hecate/agent-runtime/pkg/types"
)

func TestCancelRunWithMessageAlignsStartedTraceID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	tracer := profiler.NewInMemoryTracer(nil)
	runner := &Runner{
		logger:   slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store:    store,
		tracer:   tracer,
		policies: make(map[string]struct{}),
		jobs:     make(map[string]context.CancelFunc),
	}

	now := time.Now().UTC()
	task := types.Task{
		ID:        "task-trace",
		Status:    "running",
		CreatedAt: now,
		UpdatedAt: now,
	}
	run := types.TaskRun{
		ID:        "run-trace",
		TaskID:    task.ID,
		Status:    "running",
		StartedAt: now,
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	cancelled, err := runner.cancelRunWithMessage(ctx, task, run, "run cancelled", "request-trace", "incoming-trace-id")
	if err != nil {
		t.Fatalf("cancelRunWithMessage: %v", err)
	}
	trace, found := tracer.Get("request-trace")
	if !found {
		t.Fatalf("expected profiler trace for request")
	}
	if cancelled.TraceID != trace.TraceID {
		t.Fatalf("cancelled run trace id = %q, want started profiler trace id %q", cancelled.TraceID, trace.TraceID)
	}
	if cancelled.TraceID == "incoming-trace-id" {
		t.Fatalf("cancelled run kept stale incoming trace id")
	}

	updatedTask, found, err := store.GetTask(ctx, task.ID)
	if err != nil || !found {
		t.Fatalf("GetTask: found=%v err=%v", found, err)
	}
	if updatedTask.LatestTraceID != trace.TraceID {
		t.Fatalf("task latest trace id = %q, want %q", updatedTask.LatestTraceID, trace.TraceID)
	}

	events, err := store.ListRunEvents(ctx, task.ID, run.ID, 0, 10)
	if err != nil {
		t.Fatalf("ListRunEvents: %v", err)
	}
	if len(events) == 0 {
		t.Fatalf("expected run events")
	}
	if events[0].TraceID != trace.TraceID {
		t.Fatalf("first run event trace id = %q, want %q", events[0].TraceID, trace.TraceID)
	}
}
