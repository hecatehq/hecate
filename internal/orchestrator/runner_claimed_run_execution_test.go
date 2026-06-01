package orchestrator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

type claimedRunFailingExecutor struct {
	err error
}

func (e claimedRunFailingExecutor) Execute(context.Context, ExecutionSpec) (*ExecutionResult, error) {
	return nil, e.err
}

type claimedRunCancelExecutor struct {
	started chan struct{}
}

func (e claimedRunCancelExecutor) Execute(ctx context.Context, _ ExecutionSpec) (*ExecutionResult, error) {
	close(e.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestClaimedRunExecution_FinalizesExecutorErrorAsFailed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	queue := &recordingQueue{}
	runner := newClaimedRunProcessorTestRunner(store, queue)
	runner.exec = claimedRunFailingExecutor{err: errors.New("executor boom")}
	task, run := createQueuedClaimedExecutionRun(t, ctx, store, "task-exec-failed", "run-exec-failed")

	runner.queueCoordinator.processQueuedRun(QueueClaim{
		ClaimID: "claim-exec-failed",
		Job:     QueueJob{TaskID: task.ID, RunID: run.ID},
	})

	assertClaimedRunAcked(t, queue, "claim-exec-failed")
	updatedRun, found, err := store.GetRun(ctx, task.ID, run.ID)
	if err != nil || !found {
		t.Fatalf("GetRun() found=%t error=%v", found, err)
	}
	if updatedRun.Status != "failed" || updatedRun.LastError != "executor boom" {
		t.Fatalf("run = %+v, want failed with executor boom", updatedRun)
	}
	events, err := store.ListRunEvents(ctx, task.ID, run.ID, 0, 20)
	if err != nil {
		t.Fatalf("ListRunEvents() error = %v", err)
	}
	assertRunEventSubsequence(t, events, []string{"run.started", "run.failed"})
}

func TestClaimedRunExecution_FinalizesCancelledJobAsCancelled(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	queue := &recordingQueue{}
	runner := newClaimedRunProcessorTestRunner(store, queue)
	started := make(chan struct{})
	runner.exec = claimedRunCancelExecutor{started: started}
	task, run := createQueuedClaimedExecutionRun(t, ctx, store, "task-exec-cancelled", "run-exec-cancelled")

	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.queueCoordinator.processQueuedRun(QueueClaim{
			ClaimID: "claim-exec-cancelled",
			Job:     QueueJob{TaskID: task.ID, RunID: run.ID},
		})
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("executor did not start")
	}
	runner.cancelInFlightJob(run.ID)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("claimed run processor did not finish after cancellation")
	}

	assertClaimedRunAcked(t, queue, "claim-exec-cancelled")
	updatedRun, found, err := store.GetRun(ctx, task.ID, run.ID)
	if err != nil || !found {
		t.Fatalf("GetRun() found=%t error=%v", found, err)
	}
	if updatedRun.Status != "cancelled" || updatedRun.LastError != "run cancelled" {
		t.Fatalf("run = %+v, want cancelled with run cancelled", updatedRun)
	}
	events, err := store.ListRunEvents(ctx, task.ID, run.ID, 0, 20)
	if err != nil {
		t.Fatalf("ListRunEvents() error = %v", err)
	}
	assertRunEventSubsequence(t, events, []string{"run.started", "run.cancelled"})
}

func createQueuedClaimedExecutionRun(t *testing.T, ctx context.Context, store taskstate.Store, taskID, runID string) (types.Task, types.TaskRun) {
	t.Helper()
	now := time.Now().UTC().Add(-time.Second)
	task := types.Task{
		ID:            taskID,
		Title:         "Claimed execution",
		Prompt:        "execute",
		Status:        "queued",
		ExecutionKind: "stub",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	run := types.TaskRun{
		ID:        runID,
		TaskID:    task.ID,
		Number:    1,
		Status:    "queued",
		StartedAt: now,
		RequestID: "request-" + runID,
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	return task, run
}

func assertClaimedRunAcked(t *testing.T, queue *recordingQueue, claimID string) {
	t.Helper()
	if got, want := queue.acked, []string{claimID}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("acked claims = %+v, want %+v", got, want)
	}
}
