package orchestrator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/runtimeevents"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

type failFirstEnqueueQueue struct {
	recordingQueue
	failures int
}

type acceptThenFailEnqueueQueue struct {
	recordingQueue
}

type scheduledAdmissionHookStore struct {
	taskstate.Store
	scheduled   taskstate.ScheduledRunStore
	beforeApply func(context.Context, taskstate.TaskScheduleRunAdmission) error
	afterApply  func(taskstate.TaskScheduleRunAdmissionResult, error) (taskstate.TaskScheduleRunAdmissionResult, error)
}

func (s *scheduledAdmissionHookStore) PreflightTaskScheduleRunAdmission(
	ctx context.Context,
	preflight taskstate.TaskScheduleRunPreflight,
) (taskstate.TaskScheduleRunPreflightResult, error) {
	return s.scheduled.PreflightTaskScheduleRunAdmission(ctx, preflight)
}

func (s *scheduledAdmissionHookStore) ApplyTaskScheduleRunAdmission(
	ctx context.Context,
	admission taskstate.TaskScheduleRunAdmission,
) (taskstate.TaskScheduleRunAdmissionResult, error) {
	if s.beforeApply != nil {
		if err := s.beforeApply(ctx, admission); err != nil {
			return taskstate.TaskScheduleRunAdmissionResult{}, err
		}
	}
	result, err := s.scheduled.ApplyTaskScheduleRunAdmission(ctx, admission)
	if s.afterApply != nil {
		return s.afterApply(result, err)
	}
	return result, err
}

func TestRunnerStartTaskReturnsNotFoundAfterTaskDeletionWins(t *testing.T) {
	store := taskstate.NewMemoryStore()
	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store,
		profiler.NewInMemoryTracer(nil),
		Config{DeferQueueStart: true},
	)
	task := types.Task{ID: "task-runner-delete-winner", Status: types.TaskStatusNotStarted}
	if _, err := store.CreateTask(t.Context(), task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := store.DeleteTask(t.Context(), task.ID); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	_, err := runner.StartTask(t.Context(), task, func(prefix string) string { return prefix + "_unused" })
	if !errors.Is(err, taskstate.ErrTaskNotFound) {
		t.Fatalf("StartTask() error = %v, want taskstate.ErrTaskNotFound", err)
	}
}

func (q *failFirstEnqueueQueue) Enqueue(ctx context.Context, job QueueJob) error {
	if q.failures > 0 {
		q.failures--
		return errors.New("scheduled enqueue unavailable")
	}
	return q.recordingQueue.Enqueue(ctx, job)
}

func (q *acceptThenFailEnqueueQueue) Enqueue(ctx context.Context, job QueueJob) error {
	if err := q.recordingQueue.Enqueue(ctx, job); err != nil {
		return err
	}
	return errors.New("ambiguous scheduled enqueue")
}

func TestRunnerScheduledStartAtomicallyLinksNewRun(t *testing.T) {
	store := taskstate.NewMemoryStore()
	queue := &recordingQueue{}
	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store,
		profiler.NewInMemoryTracer(nil),
		Config{DeferQueueStart: true},
	)
	attachTestQueueCoordinator(runner, queue)
	now := time.Now().UTC()
	task := types.Task{
		ID: "task-scheduled-new", Status: "failed",
		WorkspaceMode: "in_place", WorkingDirectory: t.TempDir(), CreatedAt: now, UpdatedAt: now,
	}
	if _, err := store.CreateTask(t.Context(), task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	occurrence := claimRunnerScheduleOccurrence(t, store, task.ID, "schedule-new", "occurrence-new", "owner-new", now)

	result, err := runner.StartScheduledTask(t.Context(), task, func(prefix string) string {
		return prefix + "_scheduled_new"
	}, ScheduledTaskStart{
		ScheduleID: occurrence.ScheduleID, ScheduleOccurrenceID: occurrence.ID,
		ScheduledFor: occurrence.ScheduledFor, ClaimOwner: occurrence.ClaimOwner,
	})
	if err != nil {
		t.Fatalf("StartScheduledTask: %v", err)
	}
	if result.Run.ScheduleID != occurrence.ScheduleID ||
		result.Run.ScheduleOccurrenceID != occurrence.ID ||
		!result.Run.ScheduledFor.Equal(occurrence.ScheduledFor) {
		t.Fatalf("scheduled run provenance = %+v, want occurrence %+v", result.Run, occurrence)
	}
	occurrences, err := store.ListTaskScheduleOccurrences(t.Context(), taskstate.TaskScheduleOccurrenceFilter{ScheduleID: occurrence.ScheduleID})
	if err != nil || len(occurrences) != 1 {
		t.Fatalf("occurrences = (%+v, %v)", occurrences, err)
	}
	if occurrences[0].Status != taskstate.TaskScheduleOccurrenceStarted || occurrences[0].RunID != result.Run.ID {
		t.Fatalf("linked occurrence = %+v, want started run %q", occurrences[0], result.Run.ID)
	}
	if len(queue.enqueued) != 1 || queue.enqueued[0].RunID != result.Run.ID {
		t.Fatalf("queued jobs = %+v, want new scheduled run", queue.enqueued)
	}
}

func TestRunnerScheduledStartReplaySkipsPostCreateEffects(t *testing.T) {
	store := taskstate.NewMemoryStore()
	queue := &recordingQueue{}
	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store,
		profiler.NewInMemoryTracer(nil),
		Config{DeferQueueStart: true, ApprovalPolicies: []string{"shell_exec"}},
	)
	attachTestQueueCoordinator(runner, queue)
	now := time.Now().UTC()
	task := types.Task{
		ID: "task-scheduled-replay", Status: "failed", ExecutionKind: "shell", ShellCommand: "true",
		WorkspaceMode: "in_place", WorkingDirectory: t.TempDir(), CreatedAt: now, UpdatedAt: now,
	}
	if _, err := store.CreateTask(t.Context(), task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	occurrence := claimRunnerScheduleOccurrence(t, store, task.ID, "schedule-replay", "occurrence-replay", "owner-replay", now)
	existing := types.TaskRun{
		ID: "run-scheduled-existing", TaskID: task.ID, Number: 1, Status: "completed",
		ScheduleID: occurrence.ScheduleID, ScheduleOccurrenceID: occurrence.ID,
		ScheduledFor: occurrence.ScheduledFor, StartedAt: now.Add(-time.Minute), FinishedAt: now,
		TraceID: "trace-existing", RootSpanID: "span-existing",
	}
	if _, err := store.CreateRun(t.Context(), existing); err != nil {
		t.Fatalf("CreateRun(existing): %v", err)
	}

	result, err := runner.StartScheduledTask(t.Context(), task, func(prefix string) string {
		return prefix + "_candidate"
	}, ScheduledTaskStart{
		ScheduleID: occurrence.ScheduleID, ScheduleOccurrenceID: occurrence.ID,
		ScheduledFor: occurrence.ScheduledFor, ClaimOwner: occurrence.ClaimOwner,
	})
	if err != nil {
		t.Fatalf("StartScheduledTask: %v", err)
	}
	if result.Run.ID != existing.ID || result.Run.Status != "completed" || result.TraceID != existing.TraceID {
		t.Fatalf("scheduled replay result = %+v, want authoritative existing run", result)
	}
	if len(queue.enqueued) != 0 {
		t.Fatalf("queued jobs = %+v, want none for existing run", queue.enqueued)
	}
	approvals, err := store.ListApprovals(t.Context(), task.ID)
	if err != nil || len(approvals) != 0 {
		t.Fatalf("approvals = (%+v, %v), want none for existing run", approvals, err)
	}
	events, err := store.ListRunEvents(t.Context(), task.ID, existing.ID, 0, 20)
	if err != nil {
		t.Fatalf("ListRunEvents: %v", err)
	}
	for _, event := range events {
		if event.EventType == runtimeevents.EventRunCreated.String() ||
			event.EventType == runtimeevents.EventRunQueued.String() ||
			event.EventType == runtimeevents.EventRunAwaitingApproval.String() {
			t.Fatalf("unexpected replay event = %+v", event)
		}
	}
	runs, err := store.ListRuns(t.Context(), task.ID)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = (%+v, %v), want one existing run", runs, err)
	}
}

func TestRunnerScheduledStartRepairsFailedQueueEnqueueWithoutDuplicateEvents(t *testing.T) {
	store := taskstate.NewMemoryStore()
	queue := &failFirstEnqueueQueue{failures: 2}
	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store,
		profiler.NewInMemoryTracer(nil),
		Config{DeferQueueStart: true},
	)
	attachTestQueueCoordinator(runner, queue)
	now := time.Now().UTC()
	task := types.Task{
		ID: "task-scheduled-enqueue-repair", Status: types.TaskStatusNotStarted,
		WorkspaceMode: "in_place", WorkingDirectory: t.TempDir(), CreatedAt: now, UpdatedAt: now,
	}
	if _, err := store.CreateTask(t.Context(), task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	occurrence := claimRunnerScheduleOccurrence(t, store, task.ID, "schedule-enqueue-repair", "occurrence-enqueue-repair", "owner-enqueue-repair", now)
	start := ScheduledTaskStart{
		ScheduleID: occurrence.ScheduleID, ScheduleOccurrenceID: occurrence.ID,
		ScheduledFor: occurrence.ScheduledFor, ClaimOwner: occurrence.ClaimOwner,
	}

	_, err := runner.StartScheduledTask(t.Context(), task, func(prefix string) string {
		return prefix + "_enqueue_repair"
	}, start)
	if err == nil || err.Error() != "scheduled enqueue unavailable" {
		t.Fatalf("first StartScheduledTask error = %v, want enqueue failure", err)
	}
	runs, err := store.ListRuns(t.Context(), task.ID)
	if err != nil || len(runs) != 1 || runs[0].Status != "queued" {
		t.Fatalf("runs after enqueue failure = (%+v, %v), want one durable queued Run", runs, err)
	}
	if len(queue.enqueued) != 0 {
		t.Fatalf("queue after failed enqueue = %+v, want empty", queue.enqueued)
	}
	assertScheduledInitialEventCounts(t, store, task.ID, runs[0].ID, map[string]int{
		"run.created":          1,
		"run.queued":           1,
		"gap.run_disconnected": 0,
	})
	if len(runner.queueCoordinator.pendingReconciles) != 1 {
		t.Fatalf("pending reconciles = %+v, want failed scheduled enqueue registered", runner.queueCoordinator.pendingReconciles)
	}
	if err := runner.queueCoordinator.retryPendingReconciles(t.Context()); err == nil {
		t.Fatal("retryPendingReconciles error = nil, want second enqueue failure")
	}
	if len(runner.queueCoordinator.pendingReconciles) != 1 {
		t.Fatalf("pending reconciles after failed recovery = %+v, want retained", runner.queueCoordinator.pendingReconciles)
	}

	replayed, err := runner.StartScheduledTask(t.Context(), task, func(prefix string) string {
		return prefix + "_must_not_be_used"
	}, start)
	if err != nil {
		t.Fatalf("replayed StartScheduledTask: %v", err)
	}
	if replayed.Run.ID != runs[0].ID || len(queue.enqueued) != 1 || queue.enqueued[0].RunID != runs[0].ID {
		t.Fatalf("replayed Run/queue = (%+v, %+v), want original Run enqueued once", replayed.Run, queue.enqueued)
	}
	assertScheduledInitialEventCounts(t, store, task.ID, runs[0].ID, map[string]int{
		"run.created":          1,
		"run.queued":           1,
		"gap.run_disconnected": 0,
	})
	if len(runner.queueCoordinator.pendingReconciles) != 0 {
		t.Fatalf("pending reconciles after repair = %+v, want cleared", runner.queueCoordinator.pendingReconciles)
	}
}

func TestRunnerScheduledStartPersistsExactlyOnePendingApproval(t *testing.T) {
	store := taskstate.NewMemoryStore()
	queue := &recordingQueue{}
	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store,
		profiler.NewInMemoryTracer(nil),
		Config{DeferQueueStart: true, ApprovalPolicies: []string{"shell_exec"}},
	)
	attachTestQueueCoordinator(runner, queue)
	now := time.Now().UTC()
	task := types.Task{
		ID: "task-scheduled-approval", Status: types.TaskStatusNotStarted,
		ExecutionKind: "shell", ShellCommand: "true",
		WorkspaceMode: "in_place", WorkingDirectory: t.TempDir(), CreatedAt: now, UpdatedAt: now,
	}
	if _, err := store.CreateTask(t.Context(), task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	occurrence := claimRunnerScheduleOccurrence(t, store, task.ID, "schedule-approval", "occurrence-approval", "owner-approval", now)
	start := ScheduledTaskStart{
		ScheduleID: occurrence.ScheduleID, ScheduleOccurrenceID: occurrence.ID,
		ScheduledFor: occurrence.ScheduledFor, ClaimOwner: occurrence.ClaimOwner,
	}
	result, err := runner.StartScheduledTask(t.Context(), task, func(prefix string) string {
		return prefix + "_scheduled_approval"
	}, start)
	if err != nil {
		t.Fatalf("StartScheduledTask: %v", err)
	}
	if result.Run.Status != "awaiting_approval" || result.Run.ApprovalCount != 1 {
		t.Fatalf("scheduled approval Run = %+v, want awaiting with count 1", result.Run)
	}
	assertOneScheduledPendingApproval(t, store, task.ID, result.Run.ID)
	assertScheduledInitialEventCounts(t, store, task.ID, result.Run.ID, map[string]int{
		"run.created":           1,
		"approval.requested":    1,
		"run.awaiting_approval": 1,
	})
	if len(queue.enqueued) != 0 {
		t.Fatalf("queued jobs = %+v, awaiting approval must not enqueue", queue.enqueued)
	}

	replayed, err := runner.StartScheduledTask(t.Context(), task, func(prefix string) string {
		return prefix + "_must_not_be_used"
	}, start)
	if err != nil || replayed.Run.ID != result.Run.ID {
		t.Fatalf("replayed scheduled approval = (%+v, %v)", replayed, err)
	}
	assertOneScheduledPendingApproval(t, store, task.ID, result.Run.ID)
	assertScheduledInitialEventCounts(t, store, task.ID, result.Run.ID, map[string]int{
		"run.created":           1,
		"approval.requested":    1,
		"run.awaiting_approval": 1,
	})
}

func TestRunnerScheduledApprovalRepairsFailedQueueEnqueue(t *testing.T) {
	store := taskstate.NewMemoryStore()
	queue := &failFirstEnqueueQueue{failures: 1}
	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store,
		profiler.NewInMemoryTracer(nil),
		Config{DeferQueueStart: true, ApprovalPolicies: []string{"shell_exec"}},
	)
	attachTestQueueCoordinator(runner, queue)
	now := time.Now().UTC()
	task := types.Task{
		ID: "task-scheduled-approval-enqueue-repair", Status: types.TaskStatusNotStarted,
		ExecutionKind: "shell", ShellCommand: "true",
		WorkspaceMode: "in_place", WorkingDirectory: t.TempDir(), CreatedAt: now, UpdatedAt: now,
	}
	if _, err := store.CreateTask(t.Context(), task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	occurrence := claimRunnerScheduleOccurrence(
		t, store, task.ID, "schedule-approval-enqueue-repair", "occurrence-approval-enqueue-repair", "owner-approval-enqueue-repair", now,
	)
	started, err := runner.StartScheduledTask(t.Context(), task, func(prefix string) string {
		return prefix + "_scheduled_approval_enqueue_repair"
	}, ScheduledTaskStart{
		ScheduleID: occurrence.ScheduleID, ScheduleOccurrenceID: occurrence.ID,
		ScheduledFor: occurrence.ScheduledFor, ClaimOwner: occurrence.ClaimOwner,
	})
	if err != nil {
		t.Fatalf("StartScheduledTask: %v", err)
	}
	approvals, err := store.ListApprovals(t.Context(), task.ID)
	if err != nil || len(approvals) != 1 {
		t.Fatalf("ListApprovals = (%+v, %v), want one", approvals, err)
	}
	_, err = runner.ResolveTaskApproval(t.Context(), ResolveApprovalRequest{
		Task: started.Task, ApprovalID: approvals[0].ID, Decision: "approve",
		ResolvedBy: "operator", IDGenerator: func(prefix string) string { return prefix + "_approval_enqueue_repair" },
	})
	if err == nil || err.Error() != "scheduled enqueue unavailable" {
		t.Fatalf("ResolveTaskApproval error = %v, want enqueue failure", err)
	}
	storedRun, found, err := store.GetRun(t.Context(), task.ID, started.Run.ID)
	if err != nil || !found || storedRun.Status != "queued" {
		t.Fatalf("Run after approval enqueue failure = (%+v, %v, %v), want durable queued", storedRun, found, err)
	}
	storedApproval, found, err := store.GetApproval(t.Context(), task.ID, approvals[0].ID)
	if err != nil || !found || storedApproval.Status != "approved" {
		t.Fatalf("Approval after enqueue failure = (%+v, %v, %v), want durable approved", storedApproval, found, err)
	}
	if len(runner.queueCoordinator.pendingReconciles) != 1 {
		t.Fatalf("pending reconciles = %+v, want approved Run registered", runner.queueCoordinator.pendingReconciles)
	}
	if err := runner.queueCoordinator.retryPendingReconciles(t.Context()); err != nil {
		t.Fatalf("retryPendingReconciles: %v", err)
	}
	if len(queue.enqueued) != 1 || queue.enqueued[0].RunID != started.Run.ID {
		t.Fatalf("queued jobs = %+v, want approved scheduled Run once", queue.enqueued)
	}
	if len(runner.queueCoordinator.pendingReconciles) != 0 {
		t.Fatalf("pending reconciles after repair = %+v, want cleared", runner.queueCoordinator.pendingReconciles)
	}
	runs, err := store.ListRuns(t.Context(), task.ID)
	if err != nil || len(runs) != 1 || runs[0].ID != started.Run.ID {
		t.Fatalf("Runs after repair = (%+v, %v), want one", runs, err)
	}
	assertScheduledInitialEventCounts(t, store, task.ID, started.Run.ID, map[string]int{
		"run.created":           1,
		"approval.requested":    1,
		"run.awaiting_approval": 1,
		"approval.resolved":     1,
		"run.queued":            1,
		"gap.run_disconnected":  0,
	})
}

func TestRunnerScheduledAmbiguousEnqueueDoesNotResetRunningRun(t *testing.T) {
	store := taskstate.NewMemoryStore()
	queue := &acceptThenFailEnqueueQueue{}
	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store,
		profiler.NewInMemoryTracer(nil),
		Config{DeferQueueStart: true},
	)
	attachTestQueueCoordinator(runner, queue)
	now := time.Now().UTC()
	task := types.Task{
		ID: "task-scheduled-ambiguous-enqueue", Status: types.TaskStatusNotStarted,
		WorkspaceMode: "in_place", WorkingDirectory: t.TempDir(), CreatedAt: now, UpdatedAt: now,
	}
	if _, err := store.CreateTask(t.Context(), task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	occurrence := claimRunnerScheduleOccurrence(
		t, store, task.ID, "schedule-ambiguous-enqueue", "occurrence-ambiguous-enqueue", "owner-ambiguous-enqueue", now,
	)
	_, err := runner.StartScheduledTask(t.Context(), task, func(prefix string) string {
		return prefix + "_scheduled_ambiguous_enqueue"
	}, ScheduledTaskStart{
		ScheduleID: occurrence.ScheduleID, ScheduleOccurrenceID: occurrence.ID,
		ScheduledFor: occurrence.ScheduledFor, ClaimOwner: occurrence.ClaimOwner,
	})
	if err == nil || err.Error() != "ambiguous scheduled enqueue" {
		t.Fatalf("StartScheduledTask error = %v, want ambiguous enqueue", err)
	}
	runs, err := store.ListRuns(t.Context(), task.ID)
	if err != nil || len(runs) != 1 || runs[0].Status != "queued" {
		t.Fatalf("Runs after ambiguous enqueue = (%+v, %v), want one queued", runs, err)
	}
	storedTask, found, err := store.GetTask(t.Context(), task.ID)
	if err != nil || !found {
		t.Fatalf("GetTask = (%+v, %v, %v)", storedTask, found, err)
	}
	running := runs[0]
	running.Status = "running"
	storedTask.Status = "running"
	transition, err := store.ApplyRunStateTransition(t.Context(), taskstate.RunStateTransition{
		Task: storedTask, Run: running, ExpectedRunStatuses: []string{"queued"},
	})
	if err != nil || !transition.Applied {
		t.Fatalf("ApplyRunStateTransition(running) = (%+v, %v)", transition, err)
	}
	if err := runner.queueCoordinator.retryPendingReconciles(t.Context()); err != nil {
		t.Fatalf("retryPendingReconciles: %v", err)
	}
	storedRun, found, err := store.GetRun(t.Context(), task.ID, running.ID)
	if err != nil || !found || storedRun.Status != "running" {
		t.Fatalf("Run after pending retry = (%+v, %v, %v), want running", storedRun, found, err)
	}
	if len(queue.enqueued) != 1 {
		t.Fatalf("queue attempts = %+v, want only ambiguous accepted job", queue.enqueued)
	}
	if len(runner.queueCoordinator.pendingReconciles) != 0 {
		t.Fatalf("pending reconciles after observing running = %+v, want deferred to stale recovery", runner.queueCoordinator.pendingReconciles)
	}
	assertScheduledInitialEventCounts(t, store, task.ID, running.ID, map[string]int{
		"gap.run_disconnected": 0,
	})
}

func assertOneScheduledPendingApproval(t *testing.T, store taskstate.Store, taskID, runID string) {
	t.Helper()
	approvals, err := store.ListApprovals(t.Context(), taskID)
	if err != nil || len(approvals) != 1 || approvals[0].RunID != runID || approvals[0].Status != "pending" {
		t.Fatalf("approvals = (%+v, %v), want exactly one pending for Run %q", approvals, err, runID)
	}
	if approvals[0].ID != scheduledApprovalID(runID) {
		t.Fatalf("approval id = %q, want stable %q", approvals[0].ID, scheduledApprovalID(runID))
	}
}

func assertScheduledInitialEventCounts(t *testing.T, store taskstate.Store, taskID, runID string, want map[string]int) {
	t.Helper()
	events, err := store.ListRunEvents(t.Context(), taskID, runID, 0, 50)
	if err != nil {
		t.Fatalf("ListRunEvents: %v", err)
	}
	counts := make(map[string]int)
	for _, event := range events {
		counts[event.EventType]++
	}
	for eventType, count := range want {
		if counts[eventType] != count {
			t.Fatalf("event counts = %+v, want %s=%d", counts, eventType, count)
		}
	}
}

func TestRunnerScheduledStartAtomicallySkipsActiveOverlap(t *testing.T) {
	store := taskstate.NewMemoryStore()
	queue := &recordingQueue{}
	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store,
		profiler.NewInMemoryTracer(nil),
		Config{DeferQueueStart: true},
	)
	attachTestQueueCoordinator(runner, queue)
	provisionCalls := 0
	runner.workspaces = NewWorkspaceManager(t.TempDir())
	runner.workspaces.beforeProvisionRootOpen = func() { provisionCalls++ }
	now := time.Now().UTC()
	task := types.Task{
		ID: "task-scheduled-overlap", Status: "running", LatestRunID: "run-manual-active",
		CreatedAt: now, UpdatedAt: now,
	}
	if _, err := store.CreateTask(t.Context(), task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.CreateRun(t.Context(), types.TaskRun{
		ID: task.LatestRunID, TaskID: task.ID, Number: 1, Status: "running", StartedAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("CreateRun(active): %v", err)
	}
	occurrence := claimRunnerScheduleOccurrence(t, store, task.ID, "schedule-overlap", "occurrence-overlap", "owner-overlap", now)

	_, err := runner.StartScheduledTask(t.Context(), task, func(prefix string) string {
		return prefix + "_scheduled"
	}, ScheduledTaskStart{
		ScheduleID: occurrence.ScheduleID, ScheduleOccurrenceID: occurrence.ID,
		ScheduledFor: occurrence.ScheduledFor, ClaimOwner: occurrence.ClaimOwner,
	})
	if !errors.Is(err, ErrActiveRun) {
		t.Fatalf("StartScheduledTask error = %v, want ErrActiveRun", err)
	}
	occurrences, listErr := store.ListTaskScheduleOccurrences(t.Context(), taskstate.TaskScheduleOccurrenceFilter{ScheduleID: occurrence.ScheduleID})
	if listErr != nil || len(occurrences) != 1 || occurrences[0].Status != taskstate.TaskScheduleOccurrenceSkipped {
		t.Fatalf("occurrences after overlap = (%+v, %v), want durable skipped", occurrences, listErr)
	}
	if len(queue.enqueued) != 0 {
		t.Fatalf("queued jobs = %+v, want none for overlap", queue.enqueued)
	}
	if provisionCalls != 0 {
		t.Fatalf("workspace provisioning calls = %d, want none for overlap", provisionCalls)
	}
	runs, listErr := store.ListRuns(t.Context(), task.ID)
	if listErr != nil || len(runs) != 1 || runs[0].ID != task.LatestRunID {
		t.Fatalf("runs after overlap = (%+v, %v), want only active manual run", runs, listErr)
	}
}

func TestRunnerScheduledStartCleansManagedWorkspaceWhenManualRunWinsAfterPreflight(t *testing.T) {
	base := taskstate.NewMemoryStore()
	store := &scheduledAdmissionHookStore{Store: base, scheduled: base}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	workspaceRoot := t.TempDir()
	scheduledRunner := NewRunner(logger, store, profiler.NewInMemoryTracer(nil), Config{DeferQueueStart: true})
	scheduledRunner.workspaces = NewWorkspaceManager(workspaceRoot)
	manualRunner := NewRunner(logger, base, profiler.NewInMemoryTracer(nil), Config{DeferQueueStart: true})
	manualRunner.workspaces = NewWorkspaceManager(workspaceRoot)
	now := time.Now().UTC()
	task := types.Task{
		ID: "task-scheduled-cleanup-manual-winner", Status: types.TaskStatusNotStarted,
		CreatedAt: now, UpdatedAt: now,
	}
	if _, err := base.CreateTask(t.Context(), task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	occurrence := claimRunnerScheduleOccurrence(
		t, base, task.ID, "schedule-cleanup-manual-winner", "occurrence-cleanup-manual-winner", "owner-cleanup-manual-winner", now,
	)
	var manualResult *StartTaskResult
	store.beforeApply = func(ctx context.Context, _ taskstate.TaskScheduleRunAdmission) error {
		var err error
		manualResult, err = manualRunner.StartTask(ctx, task, func(prefix string) string {
			return prefix + "_manual_winner"
		})
		return err
	}
	_, candidateWorkspace, _, err := scheduledRunner.workspaces.plannedWorkspacePath(task.ID, "run_scheduled_candidate")
	if err != nil {
		t.Fatalf("plannedWorkspacePath(candidate): %v", err)
	}

	_, err = scheduledRunner.StartScheduledTask(t.Context(), task, func(prefix string) string {
		return prefix + "_scheduled_candidate"
	}, ScheduledTaskStart{
		ScheduleID: occurrence.ScheduleID, ScheduleOccurrenceID: occurrence.ID,
		ScheduledFor: occurrence.ScheduledFor, ClaimOwner: occurrence.ClaimOwner,
	})
	if !errors.Is(err, ErrActiveRun) {
		t.Fatalf("StartScheduledTask error = %v, want ErrActiveRun", err)
	}
	if _, err := os.Stat(candidateWorkspace); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("candidate workspace still exists or is unreadable after admission loss: %v", err)
	}
	if manualResult == nil {
		t.Fatal("manual winner result = nil")
	}
	if info, err := os.Stat(manualResult.Run.WorkspacePath); err != nil || !info.IsDir() {
		t.Fatalf("manual winner workspace = (%v, %v), want retained directory", info, err)
	}
	runs, err := base.ListRuns(t.Context(), task.ID)
	if err != nil || len(runs) != 1 || runs[0].ID != manualResult.Run.ID {
		t.Fatalf("durable runs = (%+v, %v), want only manual winner", runs, err)
	}
}

func TestRunnerScheduledStartCleansManagedWorkspaceWhenClaimIsLostAfterProvision(t *testing.T) {
	base := taskstate.NewMemoryStore()
	store := &scheduledAdmissionHookStore{Store: base, scheduled: base}
	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store,
		profiler.NewInMemoryTracer(nil),
		Config{DeferQueueStart: true},
	)
	runner.workspaces = NewWorkspaceManager(t.TempDir())
	now := time.Now().UTC()
	task := types.Task{
		ID: "task-scheduled-cleanup-claim-lost", Status: types.TaskStatusNotStarted,
		CreatedAt: now, UpdatedAt: now,
	}
	if _, err := base.CreateTask(t.Context(), task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	occurrence := claimRunnerScheduleOccurrence(
		t, base, task.ID, "schedule-cleanup-claim-lost", "occurrence-cleanup-claim-lost", "owner-cleanup-claim-lost", now,
	)
	store.beforeApply = func(ctx context.Context, admission taskstate.TaskScheduleRunAdmission) error {
		return base.DeleteTaskSchedule(ctx, admission.Run.ScheduleID)
	}
	_, candidateWorkspace, _, err := runner.workspaces.plannedWorkspacePath(task.ID, "run_claim_lost_candidate")
	if err != nil {
		t.Fatalf("plannedWorkspacePath(candidate): %v", err)
	}

	_, err = runner.StartScheduledTask(t.Context(), task, func(prefix string) string {
		return prefix + "_claim_lost_candidate"
	}, ScheduledTaskStart{
		ScheduleID: occurrence.ScheduleID, ScheduleOccurrenceID: occurrence.ID,
		ScheduledFor: occurrence.ScheduledFor, ClaimOwner: occurrence.ClaimOwner,
	})
	if !errors.Is(err, taskstate.ErrScheduleOccurrenceClaimLost) {
		t.Fatalf("StartScheduledTask error = %v, want ErrScheduleOccurrenceClaimLost", err)
	}
	if _, err := os.Stat(candidateWorkspace); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("candidate workspace still exists or is unreadable after claim loss: %v", err)
	}
	if runs, err := base.ListRuns(t.Context(), task.ID); err != nil || len(runs) != 0 {
		t.Fatalf("durable runs = (%+v, %v), want none", runs, err)
	}
}

func TestRunnerScheduledStartPreservesManagedWorkspaceAfterAmbiguousAdmissionCommit(t *testing.T) {
	base := taskstate.NewMemoryStore()
	store := &scheduledAdmissionHookStore{Store: base, scheduled: base}
	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store,
		profiler.NewInMemoryTracer(nil),
		Config{DeferQueueStart: true},
	)
	runner.workspaces = NewWorkspaceManager(t.TempDir())
	now := time.Now().UTC()
	task := types.Task{
		ID: "task-scheduled-cleanup-ambiguous", Status: types.TaskStatusNotStarted,
		CreatedAt: now, UpdatedAt: now,
	}
	if _, err := base.CreateTask(t.Context(), task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	occurrence := claimRunnerScheduleOccurrence(
		t, base, task.ID, "schedule-cleanup-ambiguous", "occurrence-cleanup-ambiguous", "owner-cleanup-ambiguous", now,
	)
	ambiguousErr := errors.New("ambiguous scheduled admission")
	store.afterApply = func(result taskstate.TaskScheduleRunAdmissionResult, err error) (taskstate.TaskScheduleRunAdmissionResult, error) {
		if err == nil && result.Applied {
			return result, ambiguousErr
		}
		return result, err
	}
	_, candidateWorkspace, _, err := runner.workspaces.plannedWorkspacePath(task.ID, "run_ambiguous_candidate")
	if err != nil {
		t.Fatalf("plannedWorkspacePath(candidate): %v", err)
	}

	_, err = runner.StartScheduledTask(t.Context(), task, func(prefix string) string {
		return prefix + "_ambiguous_candidate"
	}, ScheduledTaskStart{
		ScheduleID: occurrence.ScheduleID, ScheduleOccurrenceID: occurrence.ID,
		ScheduledFor: occurrence.ScheduledFor, ClaimOwner: occurrence.ClaimOwner,
	})
	if !errors.Is(err, ambiguousErr) {
		t.Fatalf("StartScheduledTask error = %v, want ambiguous admission error", err)
	}
	if info, err := os.Stat(candidateWorkspace); err != nil || !info.IsDir() {
		t.Fatalf("admitted workspace = (%v, %v), want retained directory", info, err)
	}
	runs, err := base.ListRuns(t.Context(), task.ID)
	if err != nil || len(runs) != 1 || runs[0].WorkspacePath != candidateWorkspace {
		t.Fatalf("durable runs = (%+v, %v), want admitted candidate workspace", runs, err)
	}
}

func TestRunnerScheduledStartAdmissionLossPreservesInPlaceWorkspace(t *testing.T) {
	base := taskstate.NewMemoryStore()
	store := &scheduledAdmissionHookStore{Store: base, scheduled: base}
	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store,
		profiler.NewInMemoryTracer(nil),
		Config{DeferQueueStart: true},
	)
	workspacePath := t.TempDir()
	markerPath := workspacePath + string(os.PathSeparator) + "keep.txt"
	if err := os.WriteFile(markerPath, []byte("keep"), 0o644); err != nil {
		t.Fatalf("WriteFile(marker): %v", err)
	}
	now := time.Now().UTC()
	task := types.Task{
		ID: "task-scheduled-cleanup-in-place", Status: types.TaskStatusNotStarted,
		WorkspaceMode: "in_place", WorkingDirectory: workspacePath,
		CreatedAt: now, UpdatedAt: now,
	}
	if _, err := base.CreateTask(t.Context(), task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	occurrence := claimRunnerScheduleOccurrence(
		t, base, task.ID, "schedule-cleanup-in-place", "occurrence-cleanup-in-place", "owner-cleanup-in-place", now,
	)
	store.beforeApply = func(ctx context.Context, admission taskstate.TaskScheduleRunAdmission) error {
		return base.DeleteTaskSchedule(ctx, admission.Run.ScheduleID)
	}

	_, err := runner.StartScheduledTask(t.Context(), task, func(prefix string) string {
		return prefix + "_in_place_candidate"
	}, ScheduledTaskStart{
		ScheduleID: occurrence.ScheduleID, ScheduleOccurrenceID: occurrence.ID,
		ScheduledFor: occurrence.ScheduledFor, ClaimOwner: occurrence.ClaimOwner,
	})
	if !errors.Is(err, taskstate.ErrScheduleOccurrenceClaimLost) {
		t.Fatalf("StartScheduledTask error = %v, want ErrScheduleOccurrenceClaimLost", err)
	}
	if content, err := os.ReadFile(markerPath); err != nil || string(content) != "keep" {
		t.Fatalf("in-place marker = (%q, %v), want preserved", content, err)
	}
}

func claimRunnerScheduleOccurrence(
	t *testing.T,
	store *taskstate.MemoryStore,
	taskID, scheduleID, occurrenceID, owner string,
	scheduledFor time.Time,
) taskstate.TaskScheduleOccurrence {
	t.Helper()
	stored, applied, err := store.CompareAndSwapTaskSchedule(t.Context(), taskstate.TaskScheduleCompareAndSwap{Schedule: taskstate.TaskSchedule{
		ID: scheduleID, TaskID: taskID, Kind: taskstate.TaskScheduleKindOnce,
		Timezone: "UTC", RunAt: scheduledFor, Enabled: true, NextRunAt: scheduledFor,
	}})
	if err != nil || !applied {
		t.Fatalf("CompareAndSwapTaskSchedule(create) = (%+v, %v, %v)", stored, applied, err)
	}
	occurrence, applied, err := store.ClaimTaskScheduleOccurrence(t.Context(), taskstate.TaskScheduleOccurrenceClaim{
		OccurrenceID: occurrenceID, ScheduleID: scheduleID, ScheduledFor: scheduledFor,
		ExpectedScheduleRevision: 1,
		ClaimOwner:               owner, ClaimedAt: scheduledFor,
	})
	if err != nil || !applied {
		t.Fatalf("ClaimTaskScheduleOccurrence = (%+v, %v, %v)", occurrence, applied, err)
	}
	return occurrence
}
