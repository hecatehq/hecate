package orchestrator

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/hecate/agent-runtime/internal/taskstate"
	"github.com/hecate/agent-runtime/pkg/types"
)

// TestStartReconcileLoop_RequeuesStaleRunningRun verifies that the periodic
// reconcile loop re-enqueues a run that has been stuck in "running" state
// longer than the stale threshold.
func TestStartReconcileLoop_RequeuesStaleRunningRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	queue := &recordingQueue{}

	// Short interval and lease so the test completes quickly.
	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store,
		nil,
		Config{
			QueueWorkers:      0, // no actual workers — we're testing the loop only
			QueueLeaseSeconds: 1, // stale threshold = 3s
			ReconcileInterval: 20 * time.Millisecond,
		},
	)
	runner.SetQueue(queue)

	task := types.Task{
		ID:        "task-stale",
		Status:    "running",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	staleRun := types.TaskRun{
		ID:        "run-stale",
		TaskID:    task.ID,
		Number:    1,
		Status:    "running",
		StartedAt: time.Now().UTC().Add(-10 * time.Second), // well past 3s threshold
	}
	if _, err := store.CreateRun(ctx, staleRun); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	runner.StartReconcileLoop()

	// Wait up to 500ms for the loop to fire and requeue the run.
	deadline := time.Now().Add(500 * time.Millisecond)
	var requeued bool
	for time.Now().Before(deadline) {
		run, found, err := store.GetRun(ctx, task.ID, staleRun.ID)
		if err != nil || !found {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		if run.Status == "queued" {
			requeued = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = runner.Shutdown(shutdownCtx)

	if !requeued {
		t.Fatal("stale run was not requeued by reconcile loop")
	}
	if len(queue.enqueued) == 0 {
		t.Fatal("expected run to be enqueued; queue is empty")
	}

	events, err := store.ListRunEvents(ctx, task.ID, staleRun.ID, 0, 50)
	if err != nil {
		t.Fatalf("ListRunEvents: %v", err)
	}
	var found bool
	for _, e := range events {
		if e.EventType == "gap.run_disconnected" {
			if got := e.Data["reason"]; got != "worker_lease_expired" {
				t.Fatalf("reason = %v, want worker_lease_expired", got)
			}
			if got := e.Data["action"]; got != "requeued" {
				t.Fatalf("action = %v, want requeued", got)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatal("missing gap.run_disconnected event")
	}
}

// TestStartReconcileLoop_SkipsFreshRunningRun verifies that the loop does NOT
// re-enqueue a run that only recently entered "running" state — i.e. an
// active worker is still within its lease window.
func TestStartReconcileLoop_SkipsFreshRunningRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	queue := &recordingQueue{}

	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store,
		nil,
		Config{
			QueueWorkers:      0,
			QueueLeaseSeconds: 60, // stale threshold = 180s; run is fresh
			ReconcileInterval: 20 * time.Millisecond,
		},
	)
	runner.SetQueue(queue)

	task := types.Task{
		ID:        "task-fresh",
		Status:    "running",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	freshRun := types.TaskRun{
		ID:        "run-fresh",
		TaskID:    task.ID,
		Number:    1,
		Status:    "running",
		StartedAt: time.Now().UTC(), // just started
	}
	if _, err := store.CreateRun(ctx, freshRun); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	runner.StartReconcileLoop()

	// Give the loop multiple ticks to (incorrectly) fire.
	time.Sleep(100 * time.Millisecond)

	shutdownCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = runner.Shutdown(shutdownCtx)

	run, found, err := store.GetRun(ctx, task.ID, freshRun.ID)
	if err != nil || !found {
		t.Fatalf("GetRun: found=%t err=%v", found, err)
	}
	if run.Status != "running" {
		t.Fatalf("fresh run status = %q, want running (should not have been requeued)", run.Status)
	}
	if len(queue.enqueued) != 0 {
		t.Fatalf("fresh run was unexpectedly enqueued (%d jobs)", len(queue.enqueued))
	}
}

// TestStartReconcileLoop_StopsOnShutdown verifies that the reconcile goroutine
// joins the worker wait-group and exits when Shutdown is called. If it leaked,
// Shutdown would block until its context deadline.
func TestStartReconcileLoop_StopsOnShutdown(t *testing.T) {
	t.Parallel()

	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		taskstate.NewMemoryStore(),
		nil,
		Config{
			QueueWorkers:      0,
			ReconcileInterval: 10 * time.Millisecond,
		},
	)
	runner.StartReconcileLoop()

	// Shutdown must complete well within 1s; if the loop goroutine leaks
	// it would hold workerWg open until the context deadline.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runner.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown returned error (loop may have leaked): %v", err)
	}
}

// TestRunner_FileExecutor_FullLifecycle exercises the full
// start → queue → claim → execute → complete path for a file-write task
// and asserts that events arrive in the required order.
func TestRunner_FileExecutor_FullLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := taskstate.NewMemoryStore()

	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store,
		nil,
		Config{QueueWorkers: 1},
	)

	tempDir := t.TempDir()
	task := types.Task{
		ID:               "task-lifecycle-orch",
		Title:            "lifecycle",
		Prompt:           "write a file",
		ExecutionKind:    "file",
		FileOperation:    "write",
		FilePath:         "out.txt",
		FileContent:      "hello",
		WorkingDirectory: tempDir,
		Status:           "pending",
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	result, err := runner.StartTask(ctx, task, defaultResourceID)
	if err != nil {
		t.Fatalf("StartTask: %v", err)
	}

	// Poll until terminal or timeout.
	deadline := time.Now().Add(10 * time.Second)
	var finalRun types.TaskRun
	for time.Now().Before(deadline) {
		run, found, err := store.GetRun(ctx, task.ID, result.Run.ID)
		if err != nil || !found {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if types.IsTerminalTaskRunStatus(run.Status) {
			finalRun = run
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = runner.Shutdown(shutdownCtx)

	if finalRun.Status != "completed" {
		t.Fatalf("run status = %q, want completed", finalRun.Status)
	}

	events, err := store.ListRunEvents(ctx, task.ID, result.Run.ID, 0, 50)
	if err != nil {
		t.Fatalf("ListRunEvents: %v", err)
	}

	// Assert the required subsequence: created → queued → started → finished.
	wantOrder := []string{"run.created", "run.queued", "run.started", "run.finished"}
	cursor := 0
	for _, e := range events {
		if cursor >= len(wantOrder) {
			break
		}
		if e.EventType == wantOrder[cursor] {
			cursor++
		}
	}
	if cursor != len(wantOrder) {
		got := make([]string, 0, len(events))
		for _, e := range events {
			got = append(got, e.EventType)
		}
		t.Fatalf("event order missing %v; got %v", wantOrder[cursor:], got)
	}

	// Assert sequences strictly increase.
	var prev int64
	for _, e := range events {
		if e.Sequence <= prev {
			t.Fatalf("sequence %d after %d for %s; want strictly increasing", e.Sequence, prev, e.EventType)
		}
		prev = e.Sequence
	}
}
