package orchestrator

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

type recordingQueue struct {
	enqueued []QueueJob
	acked    []string
}

func (q *recordingQueue) Backend() string { return "recording" }
func (q *recordingQueue) Enqueue(_ context.Context, job QueueJob) error {
	q.enqueued = append(q.enqueued, job)
	return nil
}
func (q *recordingQueue) Claim(context.Context, string, time.Duration) (QueueClaim, bool, error) {
	return QueueClaim{}, false, nil
}
func (q *recordingQueue) Ack(_ context.Context, claimID string) error {
	q.acked = append(q.acked, claimID)
	return nil
}
func (q *recordingQueue) Nack(context.Context, string, string) error               { return nil }
func (q *recordingQueue) ExtendLease(context.Context, string, time.Duration) error { return nil }
func (q *recordingQueue) Depth(context.Context) (int, error)                       { return len(q.enqueued), nil }
func (q *recordingQueue) Capacity() int                                            { return 0 }

type eventBeforeEnqueueQueue struct {
	recordingQueue
	store     taskstate.Store
	wantEvent string
	missing   []QueueJob
}

func (q *eventBeforeEnqueueQueue) Enqueue(ctx context.Context, job QueueJob) error {
	events, err := q.store.ListRunEvents(ctx, job.TaskID, job.RunID, 0, 50)
	if err != nil {
		q.missing = append(q.missing, job)
		return q.recordingQueue.Enqueue(ctx, job)
	}
	for _, event := range events {
		if event.EventType == q.wantEvent {
			return q.recordingQueue.Enqueue(ctx, job)
		}
	}
	q.missing = append(q.missing, job)
	return q.recordingQueue.Enqueue(ctx, job)
}

func TestReconcilePendingRunsRequeuesRecoverableRuns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	queue := &eventBeforeEnqueueQueue{store: store, wantEvent: "gap.run_disconnected"}
	runner := &Runner{
		logger:   slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store:    store,
		policies: make(map[string]struct{}),
	}
	attachTestQueueCoordinator(runner, queue)

	task := types.Task{
		ID:        "task_1",
		Status:    "running",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	queuedRun := types.TaskRun{ID: "run_queued", TaskID: task.ID, Number: 1, Status: "queued"}
	runningRun := types.TaskRun{ID: "run_running", TaskID: task.ID, Number: 2, Status: "running"}
	if _, err := store.CreateRun(ctx, queuedRun); err != nil {
		t.Fatalf("CreateRun(queued) error = %v", err)
	}
	if _, err := store.CreateRun(ctx, runningRun); err != nil {
		t.Fatalf("CreateRun(running) error = %v", err)
	}

	if err := runner.ReconcilePendingRuns(ctx); err != nil {
		t.Fatalf("ReconcilePendingRuns() error = %v", err)
	}

	reconciledQueued, found, err := store.GetRun(ctx, task.ID, queuedRun.ID)
	if err != nil || !found {
		t.Fatalf("GetRun(queued) found=%t err=%v", found, err)
	}
	if reconciledQueued.Status != "queued" {
		t.Fatalf("queued run status = %q, want queued", reconciledQueued.Status)
	}

	reconciledRunning, found, err := store.GetRun(ctx, task.ID, runningRun.ID)
	if err != nil || !found {
		t.Fatalf("GetRun(running) found=%t err=%v", found, err)
	}
	if reconciledRunning.Status != "queued" {
		t.Fatalf("running run reconciled status = %q, want queued", reconciledRunning.Status)
	}

	if len(queue.enqueued) != 2 {
		t.Fatalf("enqueued jobs = %d, want 2", len(queue.enqueued))
	}
	if len(queue.missing) != 0 {
		t.Fatalf("reconcile enqueued before gap.run_disconnected for jobs %+v", queue.missing)
	}

	events, err := store.ListRunEvents(ctx, task.ID, runningRun.ID, 0, 50)
	if err != nil {
		t.Fatalf("ListRunEvents() error = %v", err)
	}
	foundEvent := false
	for _, event := range events {
		if event.EventType == "gap.run_disconnected" {
			if got := event.Data["reason"]; got != "boot_reconcile" {
				t.Fatalf("reason = %v, want boot_reconcile", got)
			}
			if got := event.Data["action"]; got != "requeued" {
				t.Fatalf("action = %v, want requeued", got)
			}
			foundEvent = true
			break
		}
	}
	if !foundEvent {
		t.Fatal("missing gap.run_disconnected event")
	}
}

func TestStartTaskEmitsRunQueuedBeforeEnqueue(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	queue := &eventBeforeEnqueueQueue{store: store, wantEvent: "run.queued"}
	runner := &Runner{
		logger:     slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store:      store,
		tracer:     profiler.NewInMemoryTracer(nil),
		exec:       NewStubExecutor(),
		workspaces: NewWorkspaceManager(t.TempDir()),
		policies:   make(map[string]struct{}),
	}
	attachTestQueueCoordinator(runner, queue)
	task := types.Task{
		ID:            "task-start-queue-order",
		Status:        "pending",
		ExecutionKind: "shell",
		ShellCommand:  "printf ok",
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	if _, err := runner.StartTask(ctx, task, deterministicRunLifecycleIDGenerator()); err != nil {
		t.Fatalf("StartTask() error = %v", err)
	}
	if len(queue.enqueued) != 1 {
		t.Fatalf("enqueued jobs = %d, want 1", len(queue.enqueued))
	}
	if len(queue.missing) != 0 {
		t.Fatalf("StartTask enqueued before run.queued for jobs %+v", queue.missing)
	}
}

func deterministicRunLifecycleIDGenerator() func(string) string {
	var next int
	return func(prefix string) string {
		next++
		return prefix + "_det_" + strconv.Itoa(next)
	}
}
