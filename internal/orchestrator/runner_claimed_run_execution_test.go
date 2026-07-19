package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/internal/taskworkflow"
	"github.com/hecatehq/hecate/pkg/types"
)

type claimedRunFailingExecutor struct {
	err error
}

func (e claimedRunFailingExecutor) Execute(context.Context, ExecutionSpec) (*ExecutionResult, error) {
	return nil, e.err
}

type claimedRunCountingExecutor struct {
	calls int
}

func (e *claimedRunCountingExecutor) Execute(context.Context, ExecutionSpec) (*ExecutionResult, error) {
	e.calls++
	return &ExecutionResult{Status: "completed"}, nil
}

type claimedRunCancelExecutor struct {
	started chan struct{}
}

type claimedRunPersistenceOrderExecutor struct {
	store     taskstate.Store
	started   chan struct{}
	persisted chan bool
}

func (e claimedRunCancelExecutor) Execute(ctx context.Context, _ ExecutionSpec) (*ExecutionResult, error) {
	close(e.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func (e claimedRunPersistenceOrderExecutor) Execute(ctx context.Context, spec ExecutionSpec) (*ExecutionResult, error) {
	close(e.started)
	<-ctx.Done()
	run, found, err := e.store.GetRun(context.Background(), spec.Task.ID, spec.Run.ID)
	e.persisted <- err == nil && found && run.Status == "cancelled"
	return nil, ctx.Err()
}

type claimedRunDrainExecutor struct {
	started   chan struct{}
	cancelled chan struct{}
	release   chan struct{}
}

type claimedRunLateApprovalExecutor struct {
	started   chan struct{}
	cancelled chan struct{}
	release   chan struct{}
}

func (e claimedRunDrainExecutor) Execute(ctx context.Context, _ ExecutionSpec) (*ExecutionResult, error) {
	close(e.started)
	<-ctx.Done()
	close(e.cancelled)
	<-e.release
	return nil, ctx.Err()
}

func (e claimedRunLateApprovalExecutor) Execute(ctx context.Context, spec ExecutionSpec) (*ExecutionResult, error) {
	close(e.started)
	<-ctx.Done()
	close(e.cancelled)
	<-e.release
	createdAt := time.Now().UTC()
	stepID := "step-late-after-cancel"
	return &ExecutionResult{
		Status: "awaiting_approval",
		Steps: []types.TaskStep{{
			ID:        stepID,
			TaskID:    spec.Task.ID,
			RunID:     spec.Run.ID,
			Index:     1,
			Kind:      "approval",
			Status:    "awaiting_approval",
			StartedAt: createdAt,
		}},
		PendingApprovals: []types.TaskApproval{{
			ID:          "approval-late-after-cancel",
			TaskID:      spec.Task.ID,
			RunID:       spec.Run.ID,
			StepID:      stepID,
			Kind:        "agent_loop_tool_call",
			Status:      "pending",
			RequestedBy: "agent",
			CreatedAt:   createdAt,
		}},
	}, nil
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

func TestClaimedRunExecutionFailsClosedForInvalidPersistedQAWorkflow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	queue := &recordingQueue{}
	runner := newClaimedRunProcessorTestRunner(store, queue)
	executor := &claimedRunCountingExecutor{}
	runner.shell = executor
	now := time.Now().UTC().Add(-time.Second)
	task := types.Task{
		ID:                          "task-claimed-invalid-qa",
		Title:                       "Invalid persisted QA",
		Prompt:                      "inspect",
		Status:                      "queued",
		ExecutionKind:               "shell",
		WorkflowMode:                types.WorkflowModeQA,
		WorkflowVersion:             taskworkflow.QAVersion,
		WorkspaceMode:               "ephemeral",
		SandboxReadOnly:             true,
		WorkspaceSystemPromptPolicy: types.WorkspaceSystemPromptExclude,
		CreatedAt:                   now,
		UpdatedAt:                   now,
	}
	run := types.TaskRun{
		ID:              "run-claimed-invalid-qa",
		TaskID:          task.ID,
		Number:          1,
		Status:          "queued",
		WorkflowMode:    types.WorkflowModeQA,
		WorkflowVersion: taskworkflow.QAVersion,
		StartedAt:       now,
		RequestID:       "request-claimed-invalid-qa",
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	runner.queueCoordinator.processQueuedRun(QueueClaim{
		ClaimID: "claim-invalid-qa", Job: QueueJob{TaskID: task.ID, RunID: run.ID},
	})

	assertClaimedRunAcked(t, queue, "claim-invalid-qa")
	if executor.calls != 0 {
		t.Fatalf("shell executor calls = %d, want 0", executor.calls)
	}
	stored, found, err := store.GetRun(ctx, task.ID, run.ID)
	if err != nil || !found {
		t.Fatalf("GetRun found=%t err=%v", found, err)
	}
	if stored.Status != "failed" || !strings.Contains(stored.LastError, taskworkflow.ErrQARequiresAgentLoop.Error()) {
		t.Fatalf("stored run = %+v, want failed workflow-policy rejection", stored)
	}
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

func TestCancelRun_PersistsWinnerBeforeSignallingExecutor(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	store := taskstate.NewMemoryStore()
	queue := &recordingQueue{}
	runner := newClaimedRunProcessorTestRunner(store, queue)
	executor := claimedRunPersistenceOrderExecutor{
		store: store, started: make(chan struct{}), persisted: make(chan bool, 1),
	}
	runner.exec = executor
	task, run := createQueuedClaimedExecutionRun(t, ctx, store, "task-cancel-order", "run-cancel-order")

	processorDone := make(chan struct{})
	go func() {
		defer close(processorDone)
		runner.queueCoordinator.processQueuedRun(QueueClaim{
			ClaimID: "claim-cancel-order", Job: QueueJob{TaskID: task.ID, RunID: run.ID},
		})
	}()
	select {
	case <-executor.started:
	case <-time.After(time.Second):
		t.Fatal("executor did not start")
	}

	cancelled, err := runner.CancelRun(ctx, task, run.ID, "operator stop")
	if err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	if cancelled.Status != "cancelled" || cancelled.LastError != "run cancelled: operator stop" {
		t.Fatalf("cancelled run = %+v, want operator terminal winner", cancelled)
	}
	select {
	case persisted := <-executor.persisted:
		if !persisted {
			t.Fatal("executor observed cancellation before the terminal winner was durable")
		}
	case <-time.After(time.Second):
		t.Fatal("executor did not report cancellation ordering")
	}
	select {
	case <-processorDone:
	case <-time.After(time.Second):
		t.Fatal("claimed run processor did not drain")
	}

	events, err := store.ListRunEvents(ctx, task.ID, run.ID, 0, 20)
	if err != nil {
		t.Fatalf("ListRunEvents: %v", err)
	}
	cancelledEvents := 0
	for _, event := range events {
		if event.EventType == "run.cancelled" {
			cancelledEvents++
		}
	}
	if cancelledEvents != 1 {
		t.Fatalf("run.cancelled events = %d, want one", cancelledEvents)
	}
}

func TestCancelRun_WaitsForExecutorExitAndPreservesCancellationWinner(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	store := taskstate.NewMemoryStore()
	queue := &recordingQueue{}
	runner := newClaimedRunProcessorTestRunner(store, queue)
	executor := claimedRunDrainExecutor{
		started: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{}),
	}
	runner.exec = executor
	task, run := createQueuedClaimedExecutionRun(t, ctx, store, "task-exec-drain", "run-exec-drain")
	processorDone := make(chan struct{})
	go func() {
		defer close(processorDone)
		runner.queueCoordinator.processQueuedRun(QueueClaim{
			ClaimID: "claim-exec-drain", Job: QueueJob{TaskID: task.ID, RunID: run.ID},
		})
	}()
	select {
	case <-executor.started:
	case <-time.After(time.Second):
		t.Fatal("executor did not start")
	}

	cancelDone := make(chan error, 1)
	go func() {
		_, err := runner.CancelRun(ctx, task, run.ID, "owner deleted")
		cancelDone <- err
	}()
	select {
	case <-executor.cancelled:
	case <-time.After(time.Second):
		t.Fatal("executor did not observe cancellation")
	}
	select {
	case err := <-cancelDone:
		t.Fatalf("CancelRun returned before executor exit: %v", err)
	default:
	}
	var stored types.TaskRun
	var found bool
	var err error
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		stored, found, err = store.GetRun(ctx, task.ID, run.ID)
		if err == nil && found && stored.Status == "cancelled" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err != nil || !found || stored.Status != "cancelled" {
		t.Fatalf("run while draining = %+v found=%t err=%v, want cancelled", stored, found, err)
	}

	close(executor.release)
	select {
	case err := <-cancelDone:
		if err != nil {
			t.Fatalf("CancelRun: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("CancelRun did not return after executor exit")
	}
	select {
	case <-processorDone:
	case <-time.After(time.Second):
		t.Fatal("processor did not drain")
	}
	stored, found, err = store.GetRun(ctx, task.ID, run.ID)
	if err != nil || !found || stored.Status != "cancelled" || !strings.Contains(stored.LastError, "owner deleted") {
		t.Fatalf("final run = %+v found=%t err=%v, want operator cancellation winner", stored, found, err)
	}
}

func TestCancelRun_PostDrainCleanupCancelsApprovalPersistedAfterCancellation(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	store := taskstate.NewMemoryStore()
	queue := &recordingQueue{}
	runner := newClaimedRunProcessorTestRunner(store, queue)
	executor := claimedRunLateApprovalExecutor{
		started: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{}),
	}
	runner.exec = executor
	task, run := createQueuedClaimedExecutionRun(t, ctx, store, "task-late-approval", "run-late-approval")
	processorDone := make(chan struct{})
	go func() {
		defer close(processorDone)
		runner.queueCoordinator.processQueuedRun(QueueClaim{
			ClaimID: "claim-late-approval", Job: QueueJob{TaskID: task.ID, RunID: run.ID},
		})
	}()
	select {
	case <-executor.started:
	case <-time.After(time.Second):
		t.Fatal("executor did not start")
	}

	cancelDone := make(chan error, 1)
	go func() {
		_, err := runner.CancelRun(ctx, task, run.ID, "owner deleted")
		cancelDone <- err
	}()
	select {
	case <-executor.cancelled:
	case <-time.After(time.Second):
		t.Fatal("executor did not observe cancellation")
	}
	var winnerFinishedAt time.Time
	deadline := time.Now().Add(time.Second)
	for {
		stored, found, err := store.GetRun(ctx, task.ID, run.ID)
		if err == nil && found && stored.Status == "cancelled" {
			winnerFinishedAt = stored.FinishedAt
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cancellation was not durable before executor release: run=%+v found=%t err=%v", stored, found, err)
		}
		time.Sleep(time.Millisecond)
	}
	close(executor.release)
	select {
	case err := <-cancelDone:
		if err != nil {
			t.Fatalf("CancelRun: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("CancelRun did not return after late approval persistence")
	}
	select {
	case <-processorDone:
	case <-time.After(time.Second):
		t.Fatal("processor did not drain")
	}

	approvals, err := store.ListApprovals(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListApprovals: %v", err)
	}
	if len(approvals) != 1 || approvals[0].Status != "cancelled" || approvals[0].ResolvedBy != "system" {
		t.Fatalf("late approval = %+v, want one system-cancelled approval", approvals)
	}
	if approvals[0].ResolvedAt.Before(approvals[0].CreatedAt) {
		t.Fatalf("late approval resolved before creation: created=%s resolved=%s", approvals[0].CreatedAt, approvals[0].ResolvedAt)
	}
	step, found, err := store.GetStep(ctx, run.ID, "step-late-after-cancel")
	if err != nil || !found {
		t.Fatalf("GetStep(late): found=%t err=%v", found, err)
	}
	if step.Status != "cancelled" || step.FinishedAt.Before(step.StartedAt) {
		t.Fatalf("late step = %+v, want cancelled with nonnegative chronology", step)
	}
	storedRun, found, err := store.GetRun(ctx, task.ID, run.ID)
	if err != nil || !found {
		t.Fatalf("GetRun(final): found=%t err=%v", found, err)
	}
	if !storedRun.FinishedAt.Equal(winnerFinishedAt) {
		t.Fatalf("run FinishedAt changed during late-child cleanup: got=%s want=%s", storedRun.FinishedAt, winnerFinishedAt)
	}
	events, err := store.ListRunEvents(ctx, task.ID, run.ID, 0, 50)
	if err != nil {
		t.Fatalf("ListRunEvents: %v", err)
	}
	cancelledEvents := 0
	for _, event := range events {
		if event.EventType == "run.cancelled" {
			cancelledEvents++
		}
	}
	if cancelledEvents != 1 {
		t.Fatalf("run.cancelled events = %d, want one after cleanup replay", cancelledEvents)
	}
}

func TestCancelRun_PostDrainCleanupPreservesNewerRunTaskProjection(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	store := taskstate.NewMemoryStore()
	now := time.Now().UTC().Add(-time.Minute)
	task, err := store.CreateTask(ctx, types.Task{
		ID: "task-cleanup-newer-run", Title: "authoritative", Status: "queued",
		LatestRunID: "run-cleanup-newer", BudgetMicrosUSD: 300,
		CreatedAt: now, UpdatedAt: now.Add(30 * time.Second), StartedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	oldRun, err := store.CreateRun(ctx, types.TaskRun{
		ID: "run-cleanup-old", TaskID: task.ID, Number: 1, Status: "cancelled",
		StartedAt: now, FinishedAt: now.Add(10 * time.Second), LastError: "run cancelled: owner deleted",
	})
	if err != nil {
		t.Fatalf("CreateRun(old): %v", err)
	}
	if _, err := store.CreateRun(ctx, types.TaskRun{
		ID: task.LatestRunID, TaskID: task.ID, Number: 2, Status: "queued", StartedAt: now.Add(20 * time.Second),
	}); err != nil {
		t.Fatalf("CreateRun(new): %v", err)
	}
	if _, err := store.CreateApproval(ctx, types.TaskApproval{
		ID: "approval-cleanup-old", TaskID: task.ID, RunID: oldRun.ID,
		Kind: "agent_loop_tool_call", Status: "pending", RequestedBy: "agent", CreatedAt: now.Add(15 * time.Second),
	}); err != nil {
		t.Fatalf("CreateApproval: %v", err)
	}

	runner := &Runner{store: store}
	if _, err := runner.CancelRun(ctx, task, oldRun.ID, "owner deleted"); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	storedTask, found, err := store.GetTask(ctx, task.ID)
	if err != nil || !found {
		t.Fatalf("GetTask: found=%t err=%v", found, err)
	}
	if storedTask.Title != "authoritative" || storedTask.Status != "queued" ||
		storedTask.LatestRunID != "run-cleanup-newer" || storedTask.BudgetMicrosUSD != 300 {
		t.Fatalf("task after old-run cleanup = %+v, want newer run projection preserved", storedTask)
	}
	approvals, err := store.ListApprovals(ctx, task.ID)
	if err != nil || len(approvals) != 1 || approvals[0].Status != "cancelled" {
		t.Fatalf("old-run approvals = %+v err=%v, want cancelled", approvals, err)
	}
}

func TestCancelRun_TerminalRetryStillDrainsRegisteredExecutor(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	store := taskstate.NewMemoryStore()
	runner := newClaimedRunProcessorTestRunner(store, &recordingQueue{})
	now := time.Now().UTC()
	task, err := store.CreateTask(ctx, types.Task{ID: "task-terminal-drain", Status: "cancelled", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	run, err := store.CreateRun(ctx, types.TaskRun{
		ID: "run-terminal-drain", TaskID: task.ID, Status: "cancelled", StartedAt: now, FinishedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	jobCtx, jobCancel := context.WithCancel(context.Background())
	job := runner.registerJob(run.ID, jobCancel)
	defer func() {
		jobCancel()
		runner.unregisterJob(run.ID, job)
	}()

	firstCtx, cancelFirst := context.WithCancel(ctx)
	cancelFirst()
	if _, err := runner.CancelRun(firstCtx, task, run.ID, "retry deletion"); !errors.Is(err, context.Canceled) {
		t.Fatalf("first CancelRun error = %v, want context cancellation", err)
	}
	select {
	case <-jobCtx.Done():
	default:
		t.Fatal("terminal cancellation did not signal registered executor")
	}
	if _, err := store.CreateApproval(context.Background(), types.TaskApproval{
		ID: "approval-terminal-retry-late", TaskID: task.ID, RunID: run.ID,
		Kind: "agent_loop_tool_call", Status: "pending", RequestedBy: "agent", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateApproval(late): %v", err)
	}

	retryDone := make(chan error, 1)
	go func() {
		_, err := runner.CancelRun(ctx, task, run.ID, "retry deletion")
		retryDone <- err
	}()
	select {
	case err := <-retryDone:
		t.Fatalf("terminal retry returned before executor drain: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	runner.unregisterJob(run.ID, job)
	select {
	case err := <-retryDone:
		if err != nil {
			t.Fatalf("terminal retry CancelRun: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal retry did not return after drain")
	}
	approvals, err := store.ListApprovals(ctx, task.ID)
	if err != nil || len(approvals) != 1 || approvals[0].Status != "cancelled" {
		t.Fatalf("terminal retry late approvals = %+v err=%v, want cancelled", approvals, err)
	}
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
