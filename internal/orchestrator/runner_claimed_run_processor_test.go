package orchestrator

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

func newClaimedRunProcessorTestRunner(store taskstate.Store, queue RunQueue) *Runner {
	runner := &Runner{
		logger:   slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store:    store,
		tracer:   profiler.NewInMemoryTracer(nil),
		exec:     NewStubExecutor(),
		policies: make(map[string]struct{}),
	}
	attachTestQueueCoordinator(runner, queue)
	return runner
}

func TestClaimedRunProcessor_StartsExecutesAndAcksQueuedRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	queue := &recordingQueue{}
	runner := newClaimedRunProcessorTestRunner(store, queue)
	now := time.Now().UTC().Add(-time.Second)
	task := types.Task{
		ID:            "task-claimed-run",
		Title:         "Claimed run",
		Prompt:        "complete",
		Status:        "queued",
		ExecutionKind: "stub",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	run := types.TaskRun{
		ID:        "run-claimed",
		TaskID:    task.ID,
		Number:    1,
		Status:    "queued",
		StartedAt: now,
		RequestID: "request-claimed",
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}

	runner.queueCoordinator.processQueuedRun(QueueClaim{
		ClaimID: "claim-claimed",
		Job:     QueueJob{TaskID: task.ID, RunID: run.ID},
	})

	if got, want := queue.acked, []string{"claim-claimed"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("acked claims = %+v, want %+v", got, want)
	}
	if got := runner.inFlightJobCount(); got != 0 {
		t.Fatalf("in-flight jobs = %d, want 0", got)
	}

	updatedRun, found, err := store.GetRun(ctx, task.ID, run.ID)
	if err != nil || !found {
		t.Fatalf("GetRun() found=%t error=%v", found, err)
	}
	if updatedRun.Status != "completed" {
		t.Fatalf("run status = %q, want completed", updatedRun.Status)
	}
	if updatedRun.TraceID == "" || updatedRun.RootSpanID == "" {
		t.Fatalf("run trace ids not populated: trace=%q span=%q", updatedRun.TraceID, updatedRun.RootSpanID)
	}

	events, err := store.ListRunEvents(ctx, task.ID, run.ID, 0, 20)
	if err != nil {
		t.Fatalf("ListRunEvents() error = %v", err)
	}
	assertRunEventSubsequence(t, events, []string{"run.started", "run.finished"})
}

func TestClaimedRunProcessor_AcksNonQueuedRunWithoutStarting(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	queue := &recordingQueue{}
	runner := newClaimedRunProcessorTestRunner(store, queue)
	now := time.Now().UTC()
	task := types.Task{
		ID:        "task-non-queued",
		Status:    "running",
		CreatedAt: now,
		UpdatedAt: now,
	}
	run := types.TaskRun{
		ID:     "run-non-queued",
		TaskID: task.ID,
		Number: 1,
		Status: "running",
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}

	runner.queueCoordinator.processQueuedRun(QueueClaim{
		ClaimID: "claim-non-queued",
		Job:     QueueJob{TaskID: task.ID, RunID: run.ID},
	})

	if got, want := queue.acked, []string{"claim-non-queued"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("acked claims = %+v, want %+v", got, want)
	}
	events, err := store.ListRunEvents(ctx, task.ID, run.ID, 0, 20)
	if err != nil {
		t.Fatalf("ListRunEvents() error = %v", err)
	}
	for _, event := range events {
		if event.EventType == "run.started" {
			t.Fatalf("unexpected run.started event for non-queued run: %+v", event)
		}
	}
}

func assertRunEventSubsequence(t *testing.T, events []types.TaskRunEvent, want []string) {
	t.Helper()
	cursor := 0
	for _, event := range events {
		if cursor >= len(want) {
			break
		}
		if event.EventType == want[cursor] {
			cursor++
		}
	}
	if cursor == len(want) {
		return
	}
	got := make([]string, 0, len(events))
	for _, event := range events {
		got = append(got, event.EventType)
	}
	t.Fatalf("event order missing %v; got %v", want[cursor:], got)
}
