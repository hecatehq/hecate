package taskstate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/hecate/pkg/types"
)

// StoreFactory builds a fresh Store for one conformance subtest.
// Each subtest gets its own factory invocation so backends with
// per-instance state (sqlite file under t.TempDir, fresh memory
// map) start clean. The factory is t.Helper()-friendly and may use
// t.Cleanup for teardown.
type StoreFactory func(t *testing.T) Store

// RunConformanceTests exercises every Store-interface contract the
// memory + sqlite backends both implement against the backend the
// factory produces. New backends added later only need to supply a
// factory + one entry-point test, not duplicate every case body.
//
// Per-backend tests that exercise something the contract doesn't
// describe (sqlite constructor validation, sqlite Backend() name
// assertion) stay as standalone tests in their backend's _test.go.
func RunConformanceTests(t *testing.T, name string, factory StoreFactory) {
	t.Helper()
	t.Run(name+"/TaskRunStepRoundTrip", func(t *testing.T) {
		t.Parallel()
		runStoreTaskRunStepRoundTrip(t, factory(t))
	})
	t.Run(name+"/ListTasksFilterAndLimit", func(t *testing.T) {
		t.Parallel()
		runStoreListTasksFilterAndLimit(t, factory(t))
	})
	t.Run(name+"/ApprovalRoundTrip", func(t *testing.T) {
		t.Parallel()
		runStoreApprovalRoundTrip(t, factory(t))
	})
	t.Run(name+"/ArtifactRoundTrip", func(t *testing.T) {
		t.Parallel()
		runStoreArtifactRoundTrip(t, factory(t))
	})
	t.Run(name+"/RunEventsAppendAndList", func(t *testing.T) {
		t.Parallel()
		runStoreRunEventsAppendAndList(t, factory(t))
	})
	t.Run(name+"/ListEventsCrossRunFilters", func(t *testing.T) {
		t.Parallel()
		runStoreListEventsCrossRunFilters(t, factory(t))
	})
	t.Run(name+"/ApplyRunTerminalTransition", func(t *testing.T) {
		t.Parallel()
		runStoreApplyRunTerminalTransition(t, factory(t))
	})
	t.Run(name+"/ApplyRunTerminalTransitionPreservesChildrenWithoutCancelFlags", func(t *testing.T) {
		t.Parallel()
		runStoreApplyRunTerminalTransitionPreservesChildrenWithoutCancelFlags(t, factory(t))
	})
	t.Run(name+"/ApplyRunTerminalTransitionPreservesTaskProjection", func(t *testing.T) {
		t.Parallel()
		runStoreApplyRunTerminalTransitionPreservesTaskProjection(t, factory(t))
	})
	t.Run(name+"/ApplyRunTerminalTransitionSameStatusReplay", func(t *testing.T) {
		t.Parallel()
		runStoreApplyRunTerminalTransitionSameStatusReplay(t, factory(t))
	})
	t.Run(name+"/ApplyRunTerminalTransitionTrustedMetadataAfterDifferentStatusWinner", func(t *testing.T) {
		t.Parallel()
		runStoreApplyRunTerminalTransitionTrustedMetadataAfterDifferentStatusWinner(t, factory(t))
	})
	t.Run(name+"/ApplyRunTerminalTransitionConcurrentSameStatus", func(t *testing.T) {
		t.Parallel()
		runStoreApplyRunTerminalTransitionConcurrentSameStatus(t, factory(t))
	})
	t.Run(name+"/ApplyRunTerminalTransitionRequiresStoredRunTaskMatch", func(t *testing.T) {
		t.Parallel()
		runStoreApplyRunTerminalTransitionRequiresStoredRunTaskMatch(t, factory(t))
	})
	t.Run(name+"/ApplyRunStateTransitionCompareAndSwap", func(t *testing.T) {
		t.Parallel()
		runStoreApplyRunStateTransitionCompareAndSwap(t, factory(t))
	})
	t.Run(name+"/ApplyRunStateTransitionResolvesApprovalAtomically", func(t *testing.T) {
		t.Parallel()
		runStoreApplyRunStateTransitionResolvesApprovalAtomically(t, factory(t))
	})
	t.Run(name+"/RecordRichInputProviderAttempt", func(t *testing.T) {
		t.Parallel()
		runStoreRecordRichInputProviderAttempt(t, factory(t))
	})
	t.Run(name+"/RecordRichInputProviderAttemptConcurrent", func(t *testing.T) {
		t.Parallel()
		runStoreRecordRichInputProviderAttemptConcurrent(t, factory(t))
	})
	t.Run(name+"/StateTransitionPreservesRichInputAttempt", func(t *testing.T) {
		t.Parallel()
		runStoreStateTransitionPreservesRichInputAttempt(t, factory(t))
	})
	t.Run(name+"/TerminalTransitionPreservesRichInputAttempt", func(t *testing.T) {
		t.Parallel()
		runStoreTerminalTransitionPreservesRichInputAttempt(t, factory(t))
	})
	t.Run(name+"/ApplyRunTerminalTransitionRejectsApprovalAtomically", func(t *testing.T) {
		t.Parallel()
		runStoreApplyRunTerminalTransitionRejectsApprovalAtomically(t, factory(t))
	})
	t.Run(name+"/ApproveResolutionConcurrentWithCancellation", func(t *testing.T) {
		t.Parallel()
		runStoreApprovalResolutionConcurrentWithCancellation(t, factory(t), "approved")
	})
	t.Run(name+"/RejectResolutionConcurrentWithCancellation", func(t *testing.T) {
		t.Parallel()
		runStoreApprovalResolutionConcurrentWithCancellation(t, factory(t), "rejected")
	})
	t.Run(name+"/ApplyRunStartTransition", func(t *testing.T) {
		t.Parallel()
		runStoreApplyRunStartTransition(t, factory(t))
	})
	t.Run(name+"/ApplyRunStartTransitionConcurrent", func(t *testing.T) {
		t.Parallel()
		runStoreApplyRunStartTransitionConcurrent(t, factory(t))
	})
	t.Run(name+"/ApplyRunStartTransitionScheduledIdempotence", func(t *testing.T) {
		t.Parallel()
		runStoreApplyRunStartTransitionScheduledIdempotence(t, factory(t))
	})
	t.Run(name+"/ApplyRunStartTransitionScheduledConcurrent", func(t *testing.T) {
		t.Parallel()
		runStoreApplyRunStartTransitionScheduledConcurrent(t, factory(t))
	})
	t.Run(name+"/TerminalTransitionPreservesDifferentTerminalWinner", func(t *testing.T) {
		t.Parallel()
		runStoreTerminalTransitionPreservesDifferentTerminalWinner(t, factory(t))
	})
	t.Run(name+"/ListRunsByFilterStatusSet", func(t *testing.T) {
		t.Parallel()
		runStoreListRunsByFilterStatusSet(t, factory(t))
	})
	t.Run(name+"/DeleteTaskCascades", func(t *testing.T) {
		t.Parallel()
		runStoreDeleteTaskCascades(t, factory(t))
	})
	t.Run(name+"/DeleteTaskRejectsActiveRun", func(t *testing.T) {
		t.Parallel()
		runStoreDeleteTaskRejectsActiveRun(t, factory(t))
	})
	t.Run(name+"/DeleteTaskMissing", func(t *testing.T) {
		t.Parallel()
		runStoreDeleteTaskMissing(t, factory(t))
	})
	t.Run(name+"/DeleteTaskConcurrentWithRunStart", func(t *testing.T) {
		t.Parallel()
		runStoreDeleteTaskConcurrentWithRunStart(t, factory(t))
	})
	t.Run(name+"/TaskMCPServersRoundTrip", func(t *testing.T) {
		t.Parallel()
		runStoreTaskMCPServersRoundTrip(t, factory(t))
	})
	t.Run(name+"/WakesOnRunScopedMutations", func(t *testing.T) {
		t.Parallel()
		runStoreWakesOnRunScopedMutations(t, factory(t))
	})
}

func runStoreApplyRunStartTransition(t *testing.T, store Store) {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC()
	storedTask, err := store.CreateTask(ctx, types.Task{
		ID: "task-run-start", Title: "authoritative title", Status: "failed", BudgetMicrosUSD: 100,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.CreateRun(ctx, types.TaskRun{
		ID: "run-start-source", TaskID: storedTask.ID, Number: 4, Status: "failed", StartedAt: now, FinishedAt: now,
	}); err != nil {
		t.Fatalf("CreateRun(source): %v", err)
	}
	candidateTask := storedTask
	candidateTask.Title = "stale caller title"
	candidateTask.Status = "queued"
	candidateTask.LatestRunID = "run-start-new"
	candidateTask.FinishedAt = time.Time{}
	candidateTask.UpdatedAt = now.Add(time.Second)
	candidateTask.LatestRequestID = "request-run-start"
	result, err := store.ApplyRunStartTransition(ctx, RunStartTransition{
		Task: candidateTask,
		Run: types.TaskRun{
			ID: "run-start-new", TaskID: storedTask.ID, Status: "queued", StartedAt: now.Add(time.Second),
		},
		BudgetMicrosUSD: 250,
	})
	if err != nil {
		t.Fatalf("ApplyRunStartTransition: %v", err)
	}
	if result.Task.Title != "authoritative title" || result.Task.BudgetMicrosUSD != 250 || result.Task.LatestRunID != result.Run.ID {
		t.Fatalf("started task = %+v", result.Task)
	}
	if result.Run.Number != 5 || result.Run.Status != "queued" {
		t.Fatalf("started run = %+v, want number 5 queued", result.Run)
	}
	conflictTask := result.Task
	conflictTask.LatestRunID = "run-start-conflict"
	if _, err := store.ApplyRunStartTransition(ctx, RunStartTransition{
		Task: conflictTask,
		Run:  types.TaskRun{ID: "run-start-conflict", TaskID: storedTask.ID, Status: "queued"},
	}); !errors.Is(err, ErrActiveRun) {
		t.Fatalf("active run error = %v, want ErrActiveRun", err)
	}
	completed := result.Run
	completed.Status = "completed"
	completed.FinishedAt = now.Add(2 * time.Second)
	if _, err := store.UpdateRun(ctx, completed); err != nil {
		t.Fatalf("UpdateRun(completed): %v", err)
	}
	lowerTask := result.Task
	lowerTask.LatestRunID = "run-start-lower"
	if _, err := store.ApplyRunStartTransition(ctx, RunStartTransition{
		Task:            lowerTask,
		Run:             types.TaskRun{ID: "run-start-lower", TaskID: storedTask.ID, Status: "queued"},
		BudgetMicrosUSD: 200,
	}); !errors.Is(err, ErrBudgetLower) {
		t.Fatalf("lower budget error = %v, want ErrBudgetLower", err)
	}
}

func runStoreApplyRunStartTransitionConcurrent(t *testing.T, store Store) {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC()
	task, err := store.CreateTask(ctx, types.Task{
		ID: "task-run-start-concurrent", Status: "failed", BudgetMicrosUSD: 100, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.CreateRun(ctx, types.TaskRun{
		ID: "run-start-concurrent-source", TaskID: task.ID, Number: 1, Status: "failed", StartedAt: now, FinishedAt: now,
	}); err != nil {
		t.Fatalf("CreateRun(source): %v", err)
	}
	start := make(chan struct{})
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for index, budget := range []int64{200, 250} {
		index, budget := index, budget
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			candidate := task
			candidate.Status = "queued"
			candidate.LatestRunID = fmt.Sprintf("run-start-concurrent-%d", index)
			_, errs[index] = store.ApplyRunStartTransition(ctx, RunStartTransition{
				Task: candidate,
				Run: types.TaskRun{
					ID: candidate.LatestRunID, TaskID: task.ID, Status: "queued", StartedAt: now.Add(time.Second),
				},
				BudgetMicrosUSD: budget,
			})
		}()
	}
	close(start)
	wg.Wait()
	successes, conflicts := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrActiveRun):
			conflicts++
		default:
			t.Fatalf("concurrent start error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent start errors = %v, want one success and one active conflict", errs)
	}
	runs, err := store.ListRuns(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	active := 0
	for _, run := range runs {
		if !types.IsTerminalTaskRunStatus(run.Status) {
			active++
		}
	}
	if len(runs) != 2 || active != 1 {
		t.Fatalf("runs = %+v, want source plus exactly one active run", runs)
	}
}

func runStoreApplyRunStartTransitionScheduledIdempotence(t *testing.T, store Store) {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC()
	task, err := store.CreateTask(ctx, types.Task{
		ID: "task-run-start-scheduled", Status: "failed", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	scheduledFor := now.Add(time.Minute)
	firstTask := task
	firstTask.Status = "queued"
	firstTask.LatestRunID = "run-start-scheduled-first"
	first, err := store.ApplyRunStartTransition(ctx, RunStartTransition{
		Task: firstTask,
		Run: types.TaskRun{
			ID: firstTask.LatestRunID, TaskID: task.ID, Status: "queued", StartedAt: now,
			ScheduleID: "schedule-run-start", ScheduleOccurrenceID: "occurrence-run-start",
			ScheduledFor: scheduledFor,
		},
	})
	if err != nil {
		t.Fatalf("ApplyRunStartTransition(first): %v", err)
	}
	if first.ExistingRun {
		t.Fatal("first scheduled transition reported an existing run")
	}
	terminal := first.Run
	terminal.Status = "completed"
	terminal.FinishedAt = now.Add(time.Second)
	if _, err := store.UpdateRun(ctx, terminal); err != nil {
		t.Fatalf("UpdateRun(terminal): %v", err)
	}

	replayTask := first.Task
	replayTask.Status = "queued"
	replayTask.LatestRunID = "run-start-scheduled-replay"
	replay, err := store.ApplyRunStartTransition(ctx, RunStartTransition{
		Task: replayTask,
		Run: types.TaskRun{
			ID: replayTask.LatestRunID, TaskID: task.ID, Status: "queued", StartedAt: now.Add(2 * time.Second),
			ScheduleID: "schedule-run-start", ScheduleOccurrenceID: "occurrence-run-start",
			ScheduledFor: scheduledFor,
		},
	})
	if err != nil {
		t.Fatalf("ApplyRunStartTransition(replay): %v", err)
	}
	if !replay.ExistingRun || replay.Run.ID != terminal.ID || replay.Run.Status != "completed" {
		t.Fatalf("replay result = %+v, want authoritative terminal run %q", replay, terminal.ID)
	}
	runs, err := store.ListRuns(ctx, task.ID)
	if err != nil || len(runs) != 1 {
		t.Fatalf("ListRuns after replay = (%+v, %v), want one run", runs, err)
	}

	conflictingTask := replayTask
	conflictingTask.LatestRunID = "run-start-scheduled-conflict"
	if _, err := store.ApplyRunStartTransition(ctx, RunStartTransition{
		Task: conflictingTask,
		Run: types.TaskRun{
			ID: conflictingTask.LatestRunID, TaskID: task.ID, Status: "queued",
			ScheduleID: "different-schedule", ScheduleOccurrenceID: "occurrence-run-start",
			ScheduledFor: scheduledFor,
		},
	}); err == nil {
		t.Fatal("conflicting schedule provenance unexpectedly succeeded")
	}

	partialTask := replayTask
	partialTask.LatestRunID = "run-start-scheduled-partial"
	if _, err := store.ApplyRunStartTransition(ctx, RunStartTransition{
		Task: partialTask,
		Run: types.TaskRun{
			ID: partialTask.LatestRunID, TaskID: task.ID, Status: "queued",
			ScheduleOccurrenceID: "occurrence-partial",
		},
	}); err == nil {
		t.Fatal("partial schedule provenance unexpectedly succeeded")
	}
}

func runStoreApplyRunStartTransitionScheduledConcurrent(t *testing.T, store Store) {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC()
	task, err := store.CreateTask(ctx, types.Task{
		ID: "task-run-start-scheduled-concurrent", Status: "failed", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	start := make(chan struct{})
	results := make([]RunStartTransitionResult, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for index := range results {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			candidate := task
			candidate.Status = "queued"
			candidate.LatestRunID = fmt.Sprintf("run-start-scheduled-concurrent-%d", index)
			results[index], errs[index] = store.ApplyRunStartTransition(ctx, RunStartTransition{
				Task: candidate,
				Run: types.TaskRun{
					ID: candidate.LatestRunID, TaskID: task.ID, Status: "queued", StartedAt: now,
					ScheduleID: "schedule-concurrent", ScheduleOccurrenceID: "occurrence-concurrent",
					ScheduledFor: now.Add(time.Minute),
				},
			})
		}(index)
	}
	close(start)
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("concurrent scheduled start error = %v", err)
		}
	}
	if results[0].Run.ID != results[1].Run.ID {
		t.Fatalf("concurrent authoritative run IDs = %q/%q, want one run", results[0].Run.ID, results[1].Run.ID)
	}
	if results[0].ExistingRun == results[1].ExistingRun {
		t.Fatalf("concurrent existing flags = %v/%v, want one creator and one replay", results[0].ExistingRun, results[1].ExistingRun)
	}
	runs, err := store.ListRuns(ctx, task.ID)
	if err != nil || len(runs) != 1 {
		t.Fatalf("ListRuns after concurrent start = (%+v, %v), want one run", runs, err)
	}
}

func runStoreApplyRunStateTransitionCompareAndSwap(t *testing.T, store Store) {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC()
	task, err := store.CreateTask(ctx, types.Task{ID: "task-state-cas", Status: "queued", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	run, err := store.CreateRun(ctx, types.TaskRun{ID: "run-state-cas", TaskID: task.ID, Status: "queued", StartedAt: now})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	runningTask := task
	runningTask.Status = "running"
	runningRun := run
	runningRun.Status = "running"
	started, err := store.ApplyRunStateTransition(ctx, RunStateTransition{
		Task:                runningTask,
		Run:                 runningRun,
		ExpectedRunStatuses: []string{"queued"},
		Events:              []RunEventSpec{{EventType: "run.test_started", CreatedAt: now}},
	})
	if err != nil || !started.Applied {
		t.Fatalf("ApplyRunStateTransition(start) applied=%t err=%v", started.Applied, err)
	}

	cancelledTask := runningTask
	cancelledTask.Status = "cancelled"
	cancelledRun := runningRun
	cancelledRun.Status = "cancelled"
	cancelledRun.FinishedAt = now.Add(time.Second)
	cancelled, err := store.ApplyRunTerminalTransition(ctx, TerminalRunTransition{
		Task: cancelledTask, Run: cancelledRun, FinishedAt: cancelledRun.FinishedAt,
	})
	if err != nil || !cancelled.Applied {
		t.Fatalf("ApplyRunTerminalTransition(cancel) applied=%t err=%v", cancelled.Applied, err)
	}

	staleRun := runningRun
	staleRun.Status = "queued"
	staleTask := runningTask
	staleTask.Status = "queued"
	stale, err := store.ApplyRunStateTransition(ctx, RunStateTransition{
		Task:                staleTask,
		Run:                 staleRun,
		ExpectedRunStatuses: []string{"running"},
		Events:              []RunEventSpec{{EventType: "gap.should_not_exist", CreatedAt: now}},
	})
	if err != nil {
		t.Fatalf("ApplyRunStateTransition(stale): %v", err)
	}
	if stale.Applied || stale.Run.Status != "cancelled" || stale.Task.Status != "cancelled" {
		t.Fatalf("stale result = %+v, want unapplied cancelled winner", stale)
	}
	events, err := store.ListRunEvents(ctx, task.ID, run.ID, 0, 20)
	if err != nil {
		t.Fatalf("ListRunEvents: %v", err)
	}
	for _, event := range events {
		if event.EventType == "gap.should_not_exist" {
			t.Fatalf("stale transition appended event: %+v", event)
		}
	}
}

func runStoreRecordRichInputProviderAttempt(t *testing.T, store Store) {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC()
	task, err := store.CreateTask(ctx, types.Task{
		ID: "task-rich-input-attempt", Status: "running", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	run := types.TaskRun{
		ID: "run-rich-input-attempt", TaskID: task.ID, Status: "running", StartedAt: now, InputRef: "msg-rich-input",
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	routed := richInputProviderAttempt(task.ID, "run-rich-input-requested-route", "vision-a", "routed-model", "instance-routed")
	requested := types.TaskRun{
		ID:           "run-rich-input-requested-route",
		TaskID:       task.ID,
		Status:       "running",
		StartedAt:    now,
		InputRef:     "msg-rich-input-requested-route",
		Provider:     "requested-provider",
		ProviderKind: "requested-kind",
		Model:        "requested-model",
		// An explicit-provider attachment has an admission identity before
		// policy rewrites the requested model at final dispatch.
		InputProviderInstance: routed.ProviderInstance,
	}
	if _, err := store.CreateRun(ctx, requested); err != nil {
		t.Fatalf("CreateRun(requested route): %v", err)
	}
	if result, err := store.RecordRichInputProviderAttempt(ctx, routed); err != nil || !result.Applied || !result.Run.InputProviderDispatchRecorded || result.Run.Provider != routed.Provider || result.Run.ProviderKind != routed.ProviderKind || result.Run.Model != routed.Model {
		t.Fatalf("RecordRichInputProviderAttempt(first routed attempt) result=%+v err=%v, want policy-resolved route", result, err)
	}
	first := richInputProviderAttempt(task.ID, run.ID, "vision-a", "model-a", "instance-a")
	result, err := store.RecordRichInputProviderAttempt(ctx, first)
	if err != nil || !result.Applied {
		t.Fatalf("RecordRichInputProviderAttempt(first) applied=%t err=%v", result.Applied, err)
	}
	if !result.Run.InputProviderDispatchRecorded || result.Run.Provider != first.Provider || result.Run.ProviderKind != first.ProviderKind || result.Run.Model != first.Model || result.Run.InputProviderInstance != first.ProviderInstance {
		t.Fatalf("recorded rich-input route = %+v, want %+v", result.Run, first)
	}
	if result.Run.InputProviderDisclosedInstance.Valid() {
		t.Fatalf("recorded rich-input attempt disclosed=%+v, want empty before provider I/O", result.Run.InputProviderDisclosedInstance)
	}
	if replay, err := store.RecordRichInputProviderAttempt(ctx, first); err != nil || !replay.Applied || !replay.Run.InputProviderDispatchRecorded || replay.Run.InputProviderInstance != first.ProviderInstance {
		t.Fatalf("RecordRichInputProviderAttempt(replay) result=%+v err=%v", replay, err)
	}
	differentModel := first
	differentModel.Model = "model-b"
	if _, err := store.RecordRichInputProviderAttempt(ctx, differentModel); !errors.Is(err, ErrRichInputProviderRouteConflict) {
		t.Fatalf("RecordRichInputProviderAttempt(model conflict) error=%v, want ErrRichInputProviderRouteConflict", err)
	}
	differentKind := first
	differentKind.ProviderKind = "local"
	if _, err := store.RecordRichInputProviderAttempt(ctx, differentKind); !errors.Is(err, ErrRichInputProviderRouteConflict) {
		t.Fatalf("RecordRichInputProviderAttempt(kind conflict) error=%v, want ErrRichInputProviderRouteConflict", err)
	}
	conflicting := richInputProviderAttempt(task.ID, run.ID, "vision-b", "model-b", "instance-b")
	if _, err := store.RecordRichInputProviderAttempt(ctx, conflicting); !errors.Is(err, ErrRichInputProviderRouteConflict) {
		t.Fatalf("RecordRichInputProviderAttempt(conflict) error=%v, want ErrRichInputProviderRouteConflict", err)
	}
	stored, found, err := store.GetRun(ctx, task.ID, run.ID)
	if err != nil || !found {
		t.Fatalf("GetRun: found=%v err=%v", found, err)
	}
	if !stored.InputProviderDispatchRecorded || stored.Provider != first.Provider || stored.InputProviderInstance != first.ProviderInstance {
		t.Fatalf("conflicting attempt replaced stored route: %+v", stored)
	}

	stored.Status = "cancelled"
	stored.FinishedAt = now.Add(time.Second)
	if _, err := store.UpdateRun(ctx, stored); err != nil {
		t.Fatalf("UpdateRun(cancelled): %v", err)
	}
	if terminal, err := store.RecordRichInputProviderAttempt(ctx, first); err != nil || terminal.Applied || terminal.Run.Status != "cancelled" {
		t.Fatalf("RecordRichInputProviderAttempt(terminal) result=%+v err=%v, want unapplied cancelled run", terminal, err)
	}
	withoutRef := types.TaskRun{ID: "run-rich-input-missing-ref", TaskID: task.ID, Status: "running", StartedAt: now}
	if _, err := store.CreateRun(ctx, withoutRef); err != nil {
		t.Fatalf("CreateRun(missing ref): %v", err)
	}
	missingRef := richInputProviderAttempt(task.ID, withoutRef.ID, "vision-a", "model-a", "instance-a")
	if result, err := store.RecordRichInputProviderAttempt(ctx, missingRef); !errors.Is(err, ErrRichInputProviderRouteConflict) || result.Applied {
		t.Fatalf("RecordRichInputProviderAttempt(missing ref) result=%+v err=%v, want unapplied route conflict", result, err)
	}
}

func runStoreRecordRichInputProviderAttemptConcurrent(t *testing.T, store Store) {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC()
	task, err := store.CreateTask(ctx, types.Task{
		ID: "task-rich-input-race", Status: "running", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	run := types.TaskRun{ID: "run-rich-input-race", TaskID: task.ID, Status: "running", StartedAt: now, InputRef: "msg-rich-input"}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	attempts := []RichInputProviderAttempt{
		richInputProviderAttempt(task.ID, run.ID, "vision-a", "model-a", "instance-a"),
		richInputProviderAttempt(task.ID, run.ID, "vision-b", "model-b", "instance-b"),
	}
	start := make(chan struct{})
	results := make([]RichInputProviderAttemptResult, len(attempts))
	errs := make([]error, len(attempts))
	var wg sync.WaitGroup
	for index := range attempts {
		index := index
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[index], errs[index] = store.RecordRichInputProviderAttempt(ctx, attempts[index])
		}()
	}
	close(start)
	wg.Wait()

	applied, conflicts := 0, 0
	for index, err := range errs {
		switch {
		case err == nil && results[index].Applied:
			applied++
		case errors.Is(err, ErrRichInputProviderRouteConflict):
			conflicts++
		default:
			t.Fatalf("concurrent rich-input result[%d]=%+v err=%v", index, results[index], err)
		}
	}
	if applied != 1 || conflicts != 1 {
		t.Fatalf("concurrent rich-input results=%+v errors=%v, want one applied and one conflict", results, errs)
	}
	stored, found, err := store.GetRun(ctx, task.ID, run.ID)
	if err != nil || !found {
		t.Fatalf("GetRun: found=%v err=%v", found, err)
	}
	if stored.Provider != "vision-a" && stored.Provider != "vision-b" {
		t.Fatalf("stored provider=%q, want one contender", stored.Provider)
	}
	if !stored.InputProviderInstance.Valid() {
		t.Fatalf("stored rich-input instance=%+v, want one contender", stored.InputProviderInstance)
	}
}

func runStoreStateTransitionPreservesRichInputAttempt(t *testing.T, store Store) {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC()
	task, err := store.CreateTask(ctx, types.Task{
		ID: "task-rich-input-state", Status: "running", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	run := types.TaskRun{ID: "run-rich-input-state", TaskID: task.ID, Status: "running", StartedAt: now, InputRef: "msg-rich-input"}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	// Reconciliation can prepare a running->queued candidate from a snapshot
	// captured before final dispatch. The atomic transition must merge the
	// persisted fence instead of erasing it with that stale candidate.
	staleTask := task
	staleTask.Status = "queued"
	staleTask.UpdatedAt = now.Add(time.Second)
	staleRun := run
	staleRun.Status = "queued"
	staleRun.InputProviderDisclosedInstance = types.ProviderInstanceIdentity{ID: "instance-b", Kind: types.ProviderInstanceIdentityRuntime}
	if _, err := store.RecordRichInputProviderAttempt(ctx, richInputProviderAttempt(task.ID, run.ID, "vision-a", "model-a", "instance-a")); err != nil {
		t.Fatalf("RecordRichInputProviderAttempt: %v", err)
	}
	result, err := store.ApplyRunStateTransition(ctx, RunStateTransition{
		Task:                staleTask,
		Run:                 staleRun,
		ExpectedRunStatuses: []string{"running"},
	})
	if err != nil || !result.Applied {
		t.Fatalf("ApplyRunStateTransition(stale requeue) applied=%t err=%v", result.Applied, err)
	}
	if result.Run.Status != "queued" || !result.Run.InputProviderDispatchRecorded || result.Run.Provider != "vision-a" || result.Run.ProviderKind != "cloud" || result.Run.Model != "model-a" || result.Run.InputProviderInstance.ID != "instance-a" || result.Run.InputProviderDisclosedInstance.Valid() {
		t.Fatalf("state transition lost rich-input route: %+v", result.Run)
	}
}

func runStoreTerminalTransitionPreservesRichInputAttempt(t *testing.T, store Store) {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC()
	task, err := store.CreateTask(ctx, types.Task{
		ID: "task-rich-input-terminal", Status: "running", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	run := types.TaskRun{ID: "run-rich-input-terminal", TaskID: task.ID, Status: "running", StartedAt: now, InputRef: "msg-rich-input"}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	// Capture the cancellation candidate before the dispatch boundary updates
	// the stored route. A terminal write must retain that later fence.
	staleTask := task
	staleTask.Status = "cancelled"
	staleTask.FinishedAt = now.Add(time.Second)
	staleTask.UpdatedAt = staleTask.FinishedAt
	staleRun := run
	staleRun.Status = "cancelled"
	staleRun.FinishedAt = staleTask.FinishedAt
	if _, err := store.RecordRichInputProviderAttempt(ctx, richInputProviderAttempt(task.ID, run.ID, "vision-a", "model-a", "instance-a")); err != nil {
		t.Fatalf("RecordRichInputProviderAttempt: %v", err)
	}
	if _, err := store.ApplyRunTerminalTransition(ctx, TerminalRunTransition{
		Task:       staleTask,
		Run:        staleRun,
		FinishedAt: staleRun.FinishedAt,
	}); err != nil {
		t.Fatalf("ApplyRunTerminalTransition(stale cancellation): %v", err)
	}
	stored, found, err := store.GetRun(ctx, task.ID, run.ID)
	if err != nil || !found {
		t.Fatalf("GetRun: found=%v err=%v", found, err)
	}
	if stored.Status != "cancelled" || !stored.InputProviderDispatchRecorded || stored.InputProviderInstance.ID != "instance-a" || stored.Provider != "vision-a" || stored.Model != "model-a" {
		t.Fatalf("terminal run lost rich-input route: %+v", stored)
	}
}

func richInputProviderAttempt(taskID, runID, provider, model, instanceID string) RichInputProviderAttempt {
	return RichInputProviderAttempt{
		TaskID:       taskID,
		RunID:        runID,
		Provider:     provider,
		ProviderKind: "cloud",
		Model:        model,
		ProviderInstance: types.ProviderInstanceIdentity{
			ID:   instanceID,
			Kind: types.ProviderInstanceIdentityRuntime,
		},
	}
}

func runStoreApplyRunStateTransitionResolvesApprovalAtomically(t *testing.T, store Store) {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC().Add(-time.Minute)
	task := types.Task{
		ID: "task-approval-approve-atomic", Status: "awaiting_approval",
		CreatedAt: now, UpdatedAt: now, StartedAt: now,
	}
	run := types.TaskRun{
		ID: "run-approval-approve-atomic", TaskID: task.ID, Number: 1,
		Status: "awaiting_approval", StartedAt: now, RequestID: "request-approval-before",
		TraceID: "trace-approval-before",
	}
	approval := types.TaskApproval{
		ID: "approval-approve-atomic", TaskID: task.ID, RunID: run.ID, StepID: "step-approval-approve-atomic",
		Kind: "tool_call", Status: "pending", Reason: "immutable reason", RequestedBy: "agent", CreatedAt: now,
		RequestID: "request-approval-before", TraceID: "trace-approval-before", SpanID: "span-approval-before",
		ActionSummary: []string{"git branch -vv"},
	}
	step := types.TaskStep{
		ID: "step-approval-approve-atomic", TaskID: task.ID, RunID: run.ID,
		Index: 1, Status: "awaiting_approval", StartedAt: now,
	}
	artifact := types.TaskArtifact{
		ID: "artifact-approval-approve-atomic", TaskID: task.ID, RunID: run.ID,
		StepID: step.ID, Status: "ready", CreatedAt: now,
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := store.AppendStep(ctx, step); err != nil {
		t.Fatalf("AppendStep: %v", err)
	}
	if _, err := store.CreateArtifact(ctx, artifact); err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	if _, err := store.CreateApproval(ctx, approval); err != nil {
		t.Fatalf("CreateApproval: %v", err)
	}

	queuedTask := task
	queuedTask.Status = "queued"
	queuedTask.UpdatedAt = now.Add(time.Second)
	queuedTask.LatestRunID = run.ID
	queuedTask.LatestRequestID = "request-approval-approved"
	queuedTask.LatestTraceID = "trace-approval-approved"
	queuedRun := run
	queuedRun.Status = "queued"
	queuedRun.RequestID = queuedTask.LatestRequestID
	queuedRun.TraceID = queuedTask.LatestTraceID
	resolution := PendingApprovalResolution{
		ApprovalID: approval.ID, Status: "approved", ResolvedBy: "operator", ResolutionNote: "ship it",
		ResolvedAt: queuedTask.UpdatedAt, RequestID: queuedRun.RequestID, TraceID: queuedRun.TraceID,
	}
	transition := RunStateTransition{
		Task: queuedTask, Run: queuedRun, ExpectedRunStatuses: []string{"awaiting_approval"},
		ApprovalResolution: &resolution,
	}
	contradictory := transition
	contradictory.Events = []RunEventSpec{{
		EventType: "approval.resolved",
		Data: map[string]any{
			"approval_id": "wrong-approval", "decision": "rejected", "status": "rejected",
			"run": types.TaskRun{Status: "failed"}, "steps": []types.TaskStep{{Status: "failed"}},
			"artifacts": []types.TaskArtifact{{Status: "failed"}},
		},
		CreatedAt: now.Add(-time.Hour), IncludeRunSnapshot: true,
	}}
	if _, err := store.ApplyRunStateTransition(ctx, contradictory); err == nil {
		t.Fatal("ApplyRunStateTransition accepted caller-supplied approval audit payload")
	}
	invalidTime := transition
	invalidResolution := resolution
	invalidResolution.ResolvedAt = now.Add(-time.Nanosecond)
	invalidTime.ApprovalResolution = &invalidResolution
	if _, err := store.ApplyRunStateTransition(ctx, invalidTime); err == nil {
		t.Fatal("ApplyRunStateTransition accepted resolution before locked approval creation")
	}
	unchanged, found, err := store.GetApproval(ctx, task.ID, approval.ID)
	if err != nil || !found || unchanged.Status != "pending" {
		t.Fatalf("approval after invalid timestamp = %+v found=%t err=%v, want pending", unchanged, found, err)
	}
	result, err := store.ApplyRunStateTransition(ctx, transition)
	if err != nil || !result.Applied {
		t.Fatalf("ApplyRunStateTransition applied=%t err=%v", result.Applied, err)
	}
	if result.Task.Status != "queued" || result.Run.Status != "queued" || result.Approval.Status != "approved" {
		t.Fatalf("atomic approve result = task:%+v run:%+v approval:%+v", result.Task, result.Run, result.Approval)
	}
	if result.Approval.StepID != approval.StepID || result.Approval.Kind != approval.Kind || result.Approval.Reason != approval.Reason ||
		result.Approval.RequestedBy != approval.RequestedBy || !result.Approval.CreatedAt.Equal(approval.CreatedAt) ||
		result.Approval.RequestID != approval.RequestID || result.Approval.TraceID != approval.TraceID || result.Approval.SpanID != approval.SpanID ||
		!reflect.DeepEqual(result.Approval.ActionSummary, approval.ActionSummary) {
		t.Fatalf("atomic approve changed immutable approval provenance: got %+v want %+v", result.Approval, approval)
	}
	result.Approval.ActionSummary[0] = "mutated transition result"
	storedAfterTransition, found, err := store.GetApproval(ctx, task.ID, approval.ID)
	if err != nil || !found || storedAfterTransition.ActionSummary[0] != "git branch -vv" {
		t.Fatalf("approval after transition result mutation = %#v found=%t err=%v", storedAfterTransition.ActionSummary, found, err)
	}
	if len(result.Events) != 2 || result.Events[0].EventType != "approval.resolved" || result.Events[1].EventType != "run.queued" {
		t.Fatalf("atomic approve events = %+v, want approval.resolved then run.queued", result.Events)
	}
	assertRunEventSnapshotState(t, result.Events[0], "queued", "awaiting_approval", "ready")
	assertRunEventSnapshotState(t, result.Events[1], "queued", "awaiting_approval", "ready")
	if result.Events[0].Data["decision"] != "approved" || result.Events[1].Data["resume"] != true {
		t.Fatalf("atomic approve event data = resolved:%+v queued:%+v", result.Events[0].Data, result.Events[1].Data)
	}
	for _, event := range result.Events {
		if !event.CreatedAt.Equal(resolution.ResolvedAt) || event.RequestID != resolution.RequestID || event.TraceID != resolution.TraceID {
			t.Fatalf("derived approve event correlation/time = %+v, want command %+v", event, resolution)
		}
	}

	replay, err := store.ApplyRunStateTransition(ctx, transition)
	if err != nil {
		t.Fatalf("ApplyRunStateTransition replay: %v", err)
	}
	if replay.Applied || replay.Run.Status != "queued" || replay.Approval.Status != "approved" || len(replay.Events) != 0 {
		t.Fatalf("atomic approve replay = %+v, want authoritative unapplied result", replay)
	}
	events, err := store.ListRunEvents(ctx, task.ID, run.ID, 0, 20)
	if err != nil || len(events) != 2 {
		t.Fatalf("events after approve replay = %+v err=%v, want two", events, err)
	}

	loserTask := types.Task{
		ID: "task-approval-approve-loser", Status: "awaiting_approval",
		CreatedAt: now, UpdatedAt: now, StartedAt: now,
	}
	loserRun := types.TaskRun{
		ID: "run-approval-approve-loser", TaskID: loserTask.ID,
		Status: "awaiting_approval", StartedAt: now,
	}
	loserApproval := types.TaskApproval{
		ID: "approval-approve-loser", TaskID: loserTask.ID, RunID: loserRun.ID,
		Kind: "tool_call", Status: "pending", RequestedBy: "agent", CreatedAt: now,
	}
	if _, err := store.CreateTask(ctx, loserTask); err != nil {
		t.Fatalf("CreateTask(loser): %v", err)
	}
	if _, err := store.CreateRun(ctx, loserRun); err != nil {
		t.Fatalf("CreateRun(loser): %v", err)
	}
	if _, err := store.CreateApproval(ctx, loserApproval); err != nil {
		t.Fatalf("CreateApproval(loser): %v", err)
	}
	cancelledTask := loserTask
	cancelledTask.Status = "cancelled"
	cancelledTask.FinishedAt = now.Add(2 * time.Second)
	cancelledTask.UpdatedAt = cancelledTask.FinishedAt
	cancelledRun := loserRun
	cancelledRun.Status = "cancelled"
	cancelledRun.LastError = "run cancelled: winner"
	cancelledRun.FinishedAt = cancelledTask.FinishedAt
	if _, err := store.ApplyRunTerminalTransition(ctx, TerminalRunTransition{
		Task: cancelledTask, Run: cancelledRun, FinishedAt: cancelledRun.FinishedAt,
		CancelPendingApprovals: true, PendingApprovalStatus: "cancelled",
		PendingApprovalResolvedBy: "system", PendingApprovalResolutionNote: cancelledRun.LastError,
		ApprovalResolvedEventType: "approval.resolved",
	}); err != nil {
		t.Fatalf("ApplyRunTerminalTransition(loser winner): %v", err)
	}
	loserResolution := PendingApprovalResolution{
		ApprovalID: loserApproval.ID, Status: "approved", ResolvedBy: "operator",
		ResolvedAt: now.Add(3 * time.Second), RequestID: "request-approve-loser", TraceID: "trace-approve-loser",
	}
	loserQueuedTask := loserTask
	loserQueuedTask.Status = "queued"
	loserQueuedTask.LatestRequestID = loserResolution.RequestID
	loserQueuedTask.LatestTraceID = loserResolution.TraceID
	loserQueuedRun := loserRun
	loserQueuedRun.Status = "queued"
	loserQueuedRun.RequestID = loserResolution.RequestID
	loserQueuedRun.TraceID = loserResolution.TraceID
	loserResult, err := store.ApplyRunStateTransition(ctx, RunStateTransition{
		Task: loserQueuedTask, Run: loserQueuedRun, ExpectedRunStatuses: []string{"awaiting_approval"},
		ApprovalResolution: &loserResolution,
	})
	if err != nil {
		t.Fatalf("ApplyRunStateTransition(loser): %v", err)
	}
	if loserResult.Applied || loserResult.Run.Status != "cancelled" || loserResult.Approval.Status != "cancelled" {
		t.Fatalf("atomic approve loser = %+v, want cancelled winner", loserResult)
	}
	loserEvents, err := store.ListRunEvents(ctx, loserTask.ID, loserRun.ID, 0, 20)
	if err != nil {
		t.Fatalf("ListRunEvents(loser): %v", err)
	}
	for _, event := range loserEvents {
		if event.EventType == "run.queued" || (event.EventType == "approval.resolved" && event.Data["decision"] == "approved") {
			t.Fatalf("approve loser persisted event: %+v", event)
		}
	}
}

func runStoreApplyRunTerminalTransitionRejectsApprovalAtomically(t *testing.T, store Store) {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC().Add(-time.Minute)
	task := types.Task{
		ID: "task-approval-reject-atomic", Status: "awaiting_approval",
		CreatedAt: now, UpdatedAt: now, StartedAt: now,
	}
	run := types.TaskRun{
		ID: "run-approval-reject-atomic", TaskID: task.ID, Number: 1,
		Status: "awaiting_approval", StartedAt: now, RequestID: "request-reject-before", TraceID: "trace-reject-before",
	}
	step := types.TaskStep{
		ID: "step-approval-reject-atomic", TaskID: task.ID, RunID: run.ID,
		Index: 1, Status: "awaiting_approval", StartedAt: now,
	}
	artifact := types.TaskArtifact{
		ID: "artifact-approval-reject-atomic", TaskID: task.ID, RunID: run.ID,
		StepID: step.ID, Status: "streaming", CreatedAt: now,
	}
	target := types.TaskApproval{
		ID: "approval-reject-atomic", TaskID: task.ID, RunID: run.ID, StepID: step.ID,
		Kind: "tool_call", Status: "pending", RequestedBy: "agent", CreatedAt: now,
		ActionSummary: []string{"file_write write path=out.txt content_bytes=2"}, ActionSummaryIncomplete: true,
	}
	other := types.TaskApproval{
		ID: "approval-reject-other", TaskID: task.ID, RunID: run.ID,
		Kind: "tool_call", Status: "pending", RequestedBy: "agent", CreatedAt: now.Add(time.Millisecond),
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := store.AppendStep(ctx, step); err != nil {
		t.Fatalf("AppendStep: %v", err)
	}
	if _, err := store.CreateArtifact(ctx, artifact); err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	if _, err := store.CreateApproval(ctx, target); err != nil {
		t.Fatalf("CreateApproval(target): %v", err)
	}
	if _, err := store.CreateApproval(ctx, other); err != nil {
		t.Fatalf("CreateApproval(other): %v", err)
	}

	finishedAt := now.Add(time.Second)
	reason := "approval rejected"
	cancelledTask := task
	cancelledTask.Status = "cancelled"
	cancelledTask.LatestRunID = run.ID
	cancelledTask.LastError = reason
	cancelledTask.FinishedAt = finishedAt
	cancelledTask.UpdatedAt = finishedAt
	cancelledTask.LatestRequestID = "request-reject-winner"
	cancelledTask.LatestTraceID = "trace-reject-winner"
	cancelledRun := run
	cancelledRun.Status = "cancelled"
	cancelledRun.LastError = reason
	cancelledRun.FinishedAt = finishedAt
	cancelledRun.RequestID = cancelledTask.LatestRequestID
	cancelledRun.TraceID = cancelledTask.LatestTraceID
	resolution := PendingApprovalResolution{
		ApprovalID: target.ID, Status: "rejected", ResolvedBy: "operator", ResolutionNote: "not safe",
		ResolvedAt: finishedAt, RequestID: cancelledRun.RequestID, TraceID: cancelledRun.TraceID,
	}
	transition := TerminalRunTransition{
		Task: cancelledTask, Run: cancelledRun, FinishedAt: finishedAt,
		ApprovalResolution: &resolution,
	}
	contradictory := transition
	contradictory.TerminalEvent = &RunEventSpec{
		EventType: "approval.resolved",
		Data: map[string]any{
			"approval_id": "wrong-approval", "decision": "approved", "status": "approved",
			"run": types.TaskRun{Status: "completed"}, "steps": []types.TaskStep{{Status: "completed"}},
			"artifacts": []types.TaskArtifact{{Status: "ready"}},
		},
		CreatedAt: now.Add(-time.Hour), IncludeRunSnapshot: true,
	}
	if _, err := store.ApplyRunTerminalTransition(ctx, contradictory); err == nil {
		t.Fatal("ApplyRunTerminalTransition accepted caller-supplied approval audit payload")
	}
	invalidTime := transition
	invalidResolution := resolution
	invalidResolution.ResolvedAt = now.Add(-time.Nanosecond)
	invalidTime.ApprovalResolution = &invalidResolution
	if _, err := store.ApplyRunTerminalTransition(ctx, invalidTime); err == nil {
		t.Fatal("ApplyRunTerminalTransition accepted resolution before locked approval creation")
	}
	unchanged, found, err := store.GetApproval(ctx, task.ID, target.ID)
	if err != nil || !found || unchanged.Status != "pending" {
		t.Fatalf("target after invalid timestamp = %+v found=%t err=%v, want pending", unchanged, found, err)
	}
	result, err := store.ApplyRunTerminalTransition(ctx, transition)
	if err != nil || !result.Applied {
		t.Fatalf("ApplyRunTerminalTransition applied=%t err=%v", result.Applied, err)
	}
	if result.Task.Status != "cancelled" || result.Run.Status != "cancelled" || result.Approval.Status != "rejected" {
		t.Fatalf("atomic reject result = task:%+v run:%+v approval:%+v", result.Task, result.Run, result.Approval)
	}
	if result.Approval.StepID != target.StepID || result.Approval.Kind != target.Kind || result.Approval.RequestedBy != target.RequestedBy ||
		!result.Approval.CreatedAt.Equal(target.CreatedAt) || !reflect.DeepEqual(result.Approval.ActionSummary, target.ActionSummary) || !result.Approval.ActionSummaryIncomplete {
		t.Fatalf("atomic reject changed immutable approval provenance: got %+v want %+v", result.Approval, target)
	}
	result.Approval.ActionSummary[0] = "mutated terminal transition result"
	storedAfterTransition, found, err := store.GetApproval(ctx, task.ID, target.ID)
	if err != nil || !found || storedAfterTransition.ActionSummary[0] != target.ActionSummary[0] {
		t.Fatalf("approval after terminal transition result mutation = %#v found=%t err=%v", storedAfterTransition.ActionSummary, found, err)
	}
	otherStored, found, err := store.GetApproval(ctx, task.ID, other.ID)
	if err != nil || !found || otherStored.Status != "cancelled" {
		t.Fatalf("other approval = %+v found=%t err=%v, want cancelled", otherStored, found, err)
	}
	storedStep, found, err := store.GetStep(ctx, run.ID, step.ID)
	if err != nil || !found || storedStep.Status != "cancelled" {
		t.Fatalf("step = %+v found=%t err=%v, want cancelled", storedStep, found, err)
	}
	storedArtifact, found, err := store.GetArtifact(ctx, task.ID, artifact.ID)
	if err != nil || !found || storedArtifact.Status != "cancelled" {
		t.Fatalf("artifact = %+v found=%t err=%v, want cancelled", storedArtifact, found, err)
	}
	if len(result.Events) != 4 {
		t.Fatalf("atomic reject events = %+v, want four", result.Events)
	}
	if result.Events[0].EventType != "approval.resolved" || result.Events[0].Data["approval_id"] != other.ID ||
		result.Events[1].EventType != "run.cancelled" || result.Events[2].EventType != "task.updated" ||
		result.Events[3].EventType != "approval.resolved" || result.Events[3].Data["approval_id"] != target.ID {
		t.Fatalf("atomic reject event order/data = %+v", result.Events)
	}
	assertNoTerminalEventSnapshot(t, result.Events[1])
	assertNoTerminalEventSnapshot(t, result.Events[2])
	assertRunEventSnapshotState(t, result.Events[3], "cancelled", "cancelled", "cancelled")
	if result.Events[3].Data["decision"] != "rejected" {
		t.Fatalf("target approval event = %+v, want rejected", result.Events[3].Data)
	}
	if !result.Events[3].CreatedAt.Equal(resolution.ResolvedAt) || result.Events[3].RequestID != resolution.RequestID || result.Events[3].TraceID != resolution.TraceID {
		t.Fatalf("derived reject approval event correlation/time = %+v, want command %+v", result.Events[3], resolution)
	}

	replay, err := store.ApplyRunTerminalTransition(ctx, transition)
	if err != nil {
		t.Fatalf("ApplyRunTerminalTransition replay: %v", err)
	}
	if replay.Applied || replay.Run.Status != "cancelled" || replay.Approval.Status != "rejected" || len(replay.Events) != 0 {
		t.Fatalf("atomic reject replay = %+v, want authoritative unapplied result", replay)
	}
	events, err := store.ListRunEvents(ctx, task.ID, run.ID, 0, 20)
	if err != nil || len(events) != 4 {
		t.Fatalf("events after reject replay = %+v err=%v, want four", events, err)
	}

	loserTask := types.Task{
		ID: "task-approval-reject-loser", Status: "awaiting_approval",
		CreatedAt: now, UpdatedAt: now, StartedAt: now,
	}
	loserRun := types.TaskRun{
		ID: "run-approval-reject-loser", TaskID: loserTask.ID,
		Status: "awaiting_approval", StartedAt: now,
	}
	loserApproval := types.TaskApproval{
		ID: "approval-reject-loser", TaskID: loserTask.ID, RunID: loserRun.ID,
		Kind: "tool_call", Status: "pending", RequestedBy: "agent", CreatedAt: now,
	}
	if _, err := store.CreateTask(ctx, loserTask); err != nil {
		t.Fatalf("CreateTask(loser): %v", err)
	}
	if _, err := store.CreateRun(ctx, loserRun); err != nil {
		t.Fatalf("CreateRun(loser): %v", err)
	}
	if _, err := store.CreateApproval(ctx, loserApproval); err != nil {
		t.Fatalf("CreateApproval(loser): %v", err)
	}
	loserCancelledTask := loserTask
	loserCancelledTask.Status = "cancelled"
	loserCancelledTask.FinishedAt = now.Add(2 * time.Second)
	loserCancelledTask.UpdatedAt = loserCancelledTask.FinishedAt
	loserCancelledRun := loserRun
	loserCancelledRun.Status = "cancelled"
	loserCancelledRun.LastError = "run cancelled: winner"
	loserCancelledRun.FinishedAt = loserCancelledTask.FinishedAt
	if _, err := store.ApplyRunTerminalTransition(ctx, TerminalRunTransition{
		Task: loserCancelledTask, Run: loserCancelledRun, FinishedAt: loserCancelledRun.FinishedAt,
		CancelPendingApprovals: true, PendingApprovalStatus: "cancelled",
		PendingApprovalResolvedBy: "system", PendingApprovalResolutionNote: loserCancelledRun.LastError,
		ApprovalResolvedEventType: "approval.resolved",
	}); err != nil {
		t.Fatalf("ApplyRunTerminalTransition(loser winner): %v", err)
	}
	loserResolution := PendingApprovalResolution{
		ApprovalID: loserApproval.ID, Status: "rejected", ResolvedBy: "operator",
		ResolvedAt: now.Add(3 * time.Second), RequestID: "request-reject-loser", TraceID: "trace-reject-loser",
	}
	loserCancelledTask.LatestRequestID = loserResolution.RequestID
	loserCancelledTask.LatestTraceID = loserResolution.TraceID
	loserCancelledRun.RequestID = loserResolution.RequestID
	loserCancelledRun.TraceID = loserResolution.TraceID
	loserTransition := TerminalRunTransition{
		Task: loserCancelledTask, Run: loserCancelledRun, FinishedAt: loserCancelledRun.FinishedAt,
		ApprovalResolution: &loserResolution,
	}
	loserResult, err := store.ApplyRunTerminalTransition(ctx, loserTransition)
	if err != nil {
		t.Fatalf("ApplyRunTerminalTransition(loser): %v", err)
	}
	if loserResult.Applied || loserResult.Run.Status != "cancelled" || loserResult.Approval.Status != "cancelled" {
		t.Fatalf("atomic reject loser = %+v, want cancelled winner", loserResult)
	}
	loserEvents, err := store.ListRunEvents(ctx, loserTask.ID, loserRun.ID, 0, 20)
	if err != nil {
		t.Fatalf("ListRunEvents(loser): %v", err)
	}
	for _, event := range loserEvents {
		if event.EventType == "approval.resolved" && event.Data["decision"] == "rejected" {
			t.Fatalf("reject loser persisted event: %+v", event)
		}
	}
}

func runStoreApprovalResolutionConcurrentWithCancellation(t *testing.T, store Store, decision string) {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC().Add(-time.Minute)
	suffix := "approval-race-" + decision
	task := types.Task{
		ID: "task-" + suffix, Status: "awaiting_approval", CreatedAt: now, UpdatedAt: now, StartedAt: now,
	}
	run := types.TaskRun{
		ID: "run-" + suffix, TaskID: task.ID, Number: 1, Status: "awaiting_approval", StartedAt: now,
		RequestID: "request-before-" + decision, TraceID: "trace-before-" + decision,
	}
	step := types.TaskStep{
		ID: "step-" + suffix, TaskID: task.ID, RunID: run.ID, Index: 1,
		Status: "awaiting_approval", StartedAt: now,
	}
	artifact := types.TaskArtifact{
		ID: "artifact-" + suffix, TaskID: task.ID, RunID: run.ID, StepID: step.ID,
		Status: "streaming", CreatedAt: now,
	}
	approval := types.TaskApproval{
		ID: "approval-" + suffix, TaskID: task.ID, RunID: run.ID, StepID: step.ID,
		Kind: "tool_call", Status: "pending", Reason: "immutable reason", RequestedBy: "agent",
		CreatedAt: now, RequestID: run.RequestID, TraceID: run.TraceID, SpanID: "span-before-" + decision,
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := store.AppendStep(ctx, step); err != nil {
		t.Fatalf("AppendStep: %v", err)
	}
	if _, err := store.CreateArtifact(ctx, artifact); err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	if _, err := store.CreateApproval(ctx, approval); err != nil {
		t.Fatalf("CreateApproval: %v", err)
	}

	resolutionAt := now.Add(10 * time.Second)
	resolution := PendingApprovalResolution{
		ApprovalID: approval.ID, Status: decision, ResolvedBy: "operator", ResolutionNote: "operator decision",
		ResolvedAt: resolutionAt, RequestID: "request-resolve-" + decision, TraceID: "trace-resolve-" + decision,
	}
	resolutionTask := task
	resolutionTask.LatestRunID = run.ID
	resolutionTask.LatestRequestID = resolution.RequestID
	resolutionTask.LatestTraceID = resolution.TraceID
	resolutionTask.UpdatedAt = resolutionAt
	resolutionRun := run
	resolutionRun.RequestID = resolution.RequestID
	resolutionRun.TraceID = resolution.TraceID
	if decision == "approved" {
		resolutionTask.Status = "queued"
		resolutionRun.Status = "queued"
	} else {
		resolutionTask.Status = "cancelled"
		resolutionTask.LastError = "approval rejected"
		resolutionTask.FinishedAt = resolutionAt
		resolutionRun.Status = "cancelled"
		resolutionRun.LastError = resolutionTask.LastError
		resolutionRun.FinishedAt = resolutionAt
	}

	cancelledAt := now.Add(20 * time.Second)
	cancelReason := "run cancelled: concurrent operator"
	cancelTask := task
	cancelTask.Status = "cancelled"
	cancelTask.LatestRunID = run.ID
	cancelTask.LastError = cancelReason
	cancelTask.UpdatedAt = cancelledAt
	cancelTask.FinishedAt = cancelledAt
	cancelTask.LatestRequestID = "request-cancel-" + decision
	cancelTask.LatestTraceID = "trace-cancel-" + decision
	cancelRun := run
	cancelRun.Status = "cancelled"
	cancelRun.LastError = cancelReason
	cancelRun.FinishedAt = cancelledAt
	cancelRun.RequestID = cancelTask.LatestRequestID
	cancelRun.TraceID = cancelTask.LatestTraceID
	cancellation := TerminalRunTransition{
		Task: cancelTask, Run: cancelRun, FinishedAt: cancelledAt,
		CancelActiveSteps: true, ActiveStepResult: "error", ActiveStepError: cancelReason,
		ActiveStepErrorKind: "run_cancelled", CancelStreamingArtifacts: true,
		CancelPendingApprovals: true, PendingApprovalStatus: "cancelled",
		PendingApprovalResolvedBy: "system", PendingApprovalResolutionNote: cancelReason,
		ApprovalResolvedEventType: "approval.resolved",
		TerminalEvent: &RunEventSpec{
			EventType: "run.cancelled", Data: map[string]any{"reason": cancelReason},
			RequestID: cancelRun.RequestID, TraceID: cancelRun.TraceID, CreatedAt: cancelledAt,
		},
		TaskUpdatedEvent: &RunEventSpec{
			EventType: "task.updated", RequestID: cancelRun.RequestID,
			TraceID: cancelRun.TraceID, CreatedAt: cancelledAt,
		},
	}

	type outcome struct {
		name    string
		applied bool
		err     error
	}
	start := make(chan struct{})
	done := make(chan outcome, 2)
	go func() {
		<-start
		if decision == "approved" {
			result, err := store.ApplyRunStateTransition(ctx, RunStateTransition{
				Task: resolutionTask, Run: resolutionRun, ExpectedRunStatuses: []string{"awaiting_approval"},
				ApprovalResolution: &resolution,
			})
			done <- outcome{name: "resolution", applied: result.Applied, err: err}
			return
		}
		result, err := store.ApplyRunTerminalTransition(ctx, TerminalRunTransition{
			Task: resolutionTask, Run: resolutionRun, FinishedAt: resolutionAt,
			ApprovalResolution: &resolution,
		})
		done <- outcome{name: "resolution", applied: result.Applied, err: err}
	}()
	go func() {
		<-start
		result, err := store.ApplyRunTerminalTransition(ctx, cancellation)
		done <- outcome{name: "cancellation", applied: result.Applied, err: err}
	}()
	close(start)

	outcomes := make(map[string]outcome, 2)
	for range 2 {
		select {
		case result := <-done:
			if result.err != nil {
				t.Fatalf("%s contender: %v", result.name, result.err)
			}
			outcomes[result.name] = result
		case <-time.After(10 * time.Second):
			t.Fatal("approval resolution/cancellation contenders deadlocked")
		}
	}
	if !outcomes["cancellation"].applied {
		t.Fatalf("cancellation contender = %+v, want applied initial transition or same-status cleanup replay", outcomes["cancellation"])
	}

	storedApproval, found, err := store.GetApproval(ctx, task.ID, approval.ID)
	if err != nil || !found {
		t.Fatalf("GetApproval: found=%t err=%v", found, err)
	}
	if storedApproval.Status != decision && storedApproval.Status != "cancelled" {
		t.Fatalf("approval status = %q, want %q or cancelled", storedApproval.Status, decision)
	}
	if outcomes["resolution"].applied != (storedApproval.Status == decision) {
		t.Fatalf("resolution applied=%t approval=%q, want matching winner", outcomes["resolution"].applied, storedApproval.Status)
	}
	storedRun, found, err := store.GetRun(ctx, task.ID, run.ID)
	if err != nil || !found || storedRun.Status != "cancelled" {
		t.Fatalf("run = %+v found=%t err=%v, want cancelled", storedRun, found, err)
	}
	storedTask, found, err := store.GetTask(ctx, task.ID)
	if err != nil || !found || storedTask.Status != "cancelled" {
		t.Fatalf("task = %+v found=%t err=%v, want cancelled", storedTask, found, err)
	}
	storedStep, found, err := store.GetStep(ctx, run.ID, step.ID)
	if err != nil || !found || storedStep.Status != "cancelled" {
		t.Fatalf("step = %+v found=%t err=%v, want cancelled", storedStep, found, err)
	}
	storedArtifact, found, err := store.GetArtifact(ctx, task.ID, artifact.ID)
	if err != nil || !found || storedArtifact.Status != "cancelled" {
		t.Fatalf("artifact = %+v found=%t err=%v, want cancelled", storedArtifact, found, err)
	}

	events, err := store.ListRunEvents(ctx, task.ID, run.ID, 0, 20)
	if err != nil {
		t.Fatalf("ListRunEvents: %v", err)
	}
	counts := make(map[string]int)
	decisions := make(map[string]int)
	for _, event := range events {
		counts[event.EventType]++
		if event.EventType == "approval.resolved" && event.Data["approval_id"] == approval.ID {
			if value, ok := event.Data["decision"].(string); ok {
				decisions[value]++
			}
		}
	}
	if counts["run.cancelled"] != 1 || counts["task.updated"] != 1 || counts["approval.resolved"] != 1 {
		t.Fatalf("event counts = %+v events=%+v, want one approval/terminal/task audit", counts, events)
	}
	queuedWant := 0
	if storedApproval.Status == "approved" {
		queuedWant = 1
	}
	if counts["run.queued"] != queuedWant || decisions[storedApproval.Status] != 1 || len(decisions) != 1 {
		t.Fatalf("winner audit = counts:%+v decisions:%+v approval:%+v", counts, decisions, storedApproval)
	}
}

func assertRunEventSnapshotState(t *testing.T, event types.TaskRunEvent, runStatus, stepStatus, artifactStatus string) {
	t.Helper()
	decode := func(value any, target any) {
		t.Helper()
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal event snapshot: %v", err)
		}
		if err := json.Unmarshal(payload, target); err != nil {
			t.Fatalf("decode event snapshot: %v", err)
		}
	}
	var run types.TaskRun
	decode(event.Data["run"], &run)
	if run.Status != runStatus {
		t.Fatalf("event %q run status = %q, want %q", event.EventType, run.Status, runStatus)
	}
	var steps []types.TaskStep
	decode(event.Data["steps"], &steps)
	if len(steps) != 1 || steps[0].Status != stepStatus {
		t.Fatalf("event %q steps = %+v, want one %q", event.EventType, steps, stepStatus)
	}
	var artifacts []types.TaskArtifact
	decode(event.Data["artifacts"], &artifacts)
	if len(artifacts) != 1 || artifacts[0].Status != artifactStatus {
		t.Fatalf("event %q artifacts = %+v, want one %q", event.EventType, artifacts, artifactStatus)
	}
}

func runStoreTerminalTransitionPreservesDifferentTerminalWinner(t *testing.T, store Store) {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC()
	task, err := store.CreateTask(ctx, types.Task{ID: "task-terminal-cas", Status: "running", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	run, err := store.CreateRun(ctx, types.TaskRun{ID: "run-terminal-cas", TaskID: task.ID, Status: "running", StartedAt: now})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	cancelledTask := task
	cancelledTask.Status = "cancelled"
	cancelledRun := run
	cancelledRun.Status = "cancelled"
	cancelledRun.FinishedAt = now.Add(time.Second)
	winner, err := store.ApplyRunTerminalTransition(ctx, TerminalRunTransition{Task: cancelledTask, Run: cancelledRun, FinishedAt: cancelledRun.FinishedAt})
	if err != nil || !winner.Applied {
		t.Fatalf("winner transition applied=%t err=%v", winner.Applied, err)
	}
	completedTask := task
	completedTask.Status = "completed"
	completedRun := run
	completedRun.Status = "completed"
	completedRun.FinishedAt = now.Add(2 * time.Second)
	stale, err := store.ApplyRunTerminalTransition(ctx, TerminalRunTransition{Task: completedTask, Run: completedRun, FinishedAt: completedRun.FinishedAt})
	if err != nil {
		t.Fatalf("stale terminal transition: %v", err)
	}
	if stale.Applied || stale.Run.Status != "cancelled" || stale.Task.Status != "cancelled" {
		t.Fatalf("stale terminal result = %+v, want unapplied cancelled winner", stale)
	}
}

// runStoreWakesOnRunScopedMutations is the contract the task-run SSE
// stream relies on: every run-scoped write reaches a subscriber so the
// stream can wake and re-read instead of polling. Steps, artifacts, and
// run-status changes persist without emitting a run_event, so a
// wake-on-AppendRunEvent-only design would miss live updates — hence the
// store must signal on all of them. Backends that don't implement the
// optional SubscribeRun capability fall back to polling, so the case
// skips them rather than failing.
func runStoreWakesOnRunScopedMutations(t *testing.T, store Store) {
	t.Helper()
	sub, ok := store.(interface {
		SubscribeRun(string) (<-chan struct{}, func())
	})
	if !ok {
		t.Skip("store does not implement SubscribeRun; stream falls back to polling")
	}

	ctx := context.Background()
	const taskID, runID = "task-wake", "run-wake"

	// The run/approval round-trips create the parent task first, so the
	// sqlite backend's foreign keys are satisfied. Do it before
	// subscribing — CreateTask is not run-scoped and must not wake.
	if _, err := store.CreateTask(ctx, types.Task{ID: taskID, Status: "running"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	ch, unsubscribe := sub.SubscribeRun(runID)
	defer unsubscribe()

	now := time.Now().UTC()
	mutations := []struct {
		name string
		run  func() error
	}{
		{"CreateRun", func() error {
			_, err := store.CreateRun(ctx, types.TaskRun{ID: runID, TaskID: taskID, Number: 1, Status: "running", StartedAt: now})
			return err
		}},
		{"UpdateRun", func() error {
			_, err := store.UpdateRun(ctx, types.TaskRun{ID: runID, TaskID: taskID, Number: 1, Status: "succeeded", StartedAt: now})
			return err
		}},
		{"AppendStep", func() error {
			_, err := store.AppendStep(ctx, types.TaskStep{ID: "step-wake", TaskID: taskID, RunID: runID, Index: 0, Status: "running", StartedAt: now})
			return err
		}},
		{"UpdateStep", func() error {
			_, err := store.UpdateStep(ctx, types.TaskStep{ID: "step-wake", TaskID: taskID, RunID: runID, Index: 0, Status: "completed", StartedAt: now})
			return err
		}},
		{"CreateArtifact", func() error {
			_, err := store.CreateArtifact(ctx, types.TaskArtifact{ID: "art-wake", TaskID: taskID, RunID: runID, Kind: "git_summary", Name: "summary", MimeType: "text/plain", StorageKind: "inline", ContentText: "ok", SizeBytes: 2, Status: "ready", CreatedAt: now})
			return err
		}},
		{"CreateApproval", func() error {
			_, err := store.CreateApproval(ctx, types.TaskApproval{ID: "ap-wake", TaskID: taskID, RunID: runID, Kind: "shell", Status: "pending", RequestedBy: "agent", CreatedAt: now})
			return err
		}},
		{"AppendRunEvent", func() error {
			_, err := store.AppendRunEvent(ctx, types.TaskRunEvent{TaskID: taskID, RunID: runID, EventType: "model.call.completed", RequestID: "req-wake"})
			return err
		}},
		{"ApplyRunTerminalTransition", func() error {
			_, err := store.ApplyRunTerminalTransition(ctx, TerminalRunTransition{
				Task: types.Task{ID: taskID, Status: "succeeded"},
				Run:  types.TaskRun{ID: runID, TaskID: taskID, Status: "succeeded"},
			})
			return err
		}},
	}

	for _, m := range mutations {
		// Drain any buffered wake so each mutation is judged on its own
		// merits rather than on a signal left over from a prior step.
		select {
		case <-ch:
		default:
		}
		if err := m.run(); err != nil {
			t.Fatalf("%s: %v", m.name, err)
		}
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatalf("%s: expected a wake signal, got none", m.name)
		}
	}
}

func runStoreTaskRunStepRoundTrip(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	toolsEnabled := false
	browserAllowed := true

	task := types.Task{
		ID:                               "task-1",
		Title:                            "demo",
		ProjectID:                        "proj-1",
		WorkItemID:                       "work-1",
		AssignmentID:                     "asgn-1",
		AgentPresetID:                    "review_qa",
		AgentPresetToolsEnabled:          &toolsEnabled,
		AgentPresetBrowserAllowed:        &browserAllowed,
		AgentPresetBrowserAllowedOrigins: []string{"https://app.example.test"},
		WorkflowMode:                     types.WorkflowModeQA,
		WorkflowVersion:                  "v0",
		WorkspaceSystemPromptPolicy:      types.WorkspaceSystemPromptExclude,
		SandboxReadOnly:                  true,
		SandboxNetwork:                   false,
		Status:                           "queued",
	}
	saved, err := store.CreateTask(ctx, task)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if saved.CreatedAt.IsZero() {
		t.Fatal("CreateTask did not stamp CreatedAt")
	}
	toolsEnabled = true
	if saved.AgentPresetToolsEnabled == nil {
		t.Fatal("CreateTask response omitted tools snapshot")
	}
	*saved.AgentPresetToolsEnabled = true
	*saved.AgentPresetBrowserAllowed = false
	saved.AgentPresetBrowserAllowedOrigins[0] = "https://mutated.example.test"

	got, ok, err := store.GetTask(ctx, "task-1")
	if err != nil || !ok {
		t.Fatalf("GetTask: ok=%v err=%v", ok, err)
	}
	if got.Title != "demo" {
		t.Fatalf("GetTask round-trip mismatch: %+v", got)
	}
	if got.AgentPresetID != "review_qa" || got.AgentPresetToolsEnabled == nil || *got.AgentPresetToolsEnabled || got.AgentPresetBrowserAllowed == nil || !*got.AgentPresetBrowserAllowed || len(got.AgentPresetBrowserAllowedOrigins) != 1 || got.AgentPresetBrowserAllowedOrigins[0] != "https://app.example.test" || got.WorkflowMode != types.WorkflowModeQA || got.WorkflowVersion != "v0" || got.WorkspaceSystemPromptPolicy != types.WorkspaceSystemPromptExclude || !got.SandboxReadOnly || got.SandboxNetwork {
		t.Fatalf("GetTask runtime policy snapshot = %+v, want independent browser enabled/origin snapshot and review posture", got)
	}
	*got.AgentPresetToolsEnabled = true
	*got.AgentPresetBrowserAllowed = false
	got.AgentPresetBrowserAllowedOrigins[0] = "https://mutated.example.test"
	gotAgain, ok, err := store.GetTask(ctx, "task-1")
	if err != nil || !ok || gotAgain.AgentPresetToolsEnabled == nil || *gotAgain.AgentPresetToolsEnabled || gotAgain.AgentPresetBrowserAllowed == nil || !*gotAgain.AgentPresetBrowserAllowed || len(gotAgain.AgentPresetBrowserAllowedOrigins) != 1 || gotAgain.AgentPresetBrowserAllowedOrigins[0] != "https://app.example.test" {
		t.Fatalf("GetTask policy snapshot alias leaked into store: task=%+v ok=%v err=%v", gotAgain, ok, err)
	}

	run := types.TaskRun{
		ID:              "run-1",
		TaskID:          "task-1",
		ProjectID:       "proj-1",
		WorkItemID:      "work-1",
		AssignmentID:    "asgn-1",
		WorkflowMode:    types.WorkflowModeQA,
		WorkflowVersion: "v0",
		Number:          1,
		Status:          "running",
		StartedAt:       time.Now().UTC(),
		InputRef:        "msg-rich-input",
		InputProviderInstance: types.ProviderInstanceIdentity{
			ID:   "runtime-rich-input",
			Kind: types.ProviderInstanceIdentityRuntime,
		},
		InputProviderDispatchRecorded: true,
		InputProviderDisclosedInstance: types.ProviderInstanceIdentity{
			ID:   "runtime-rich-input-disclosed",
			Kind: types.ProviderInstanceIdentityRuntime,
		},
		ToolCallingVerification: types.ToolCallingVerificationFence{
			Provider: "fixture-cloud",
			Model:    "fixture-tools-unknown",
			ProviderInstance: types.ProviderInstanceIdentity{
				ID:   "runtime-tool-verification",
				Kind: types.ProviderInstanceIdentityRuntime,
			},
			ExpiresAt: time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC),
		},
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	gotRun, ok, err := store.GetRun(ctx, "task-1", "run-1")
	if err != nil || !ok {
		t.Fatalf("GetRun: ok=%v err=%v", ok, err)
	}
	if gotRun.Status != "running" || gotRun.Number != 1 {
		t.Fatalf("GetRun round-trip mismatch: %+v", gotRun)
	}
	if gotRun.ProjectID != "proj-1" || gotRun.WorkItemID != "work-1" || gotRun.AssignmentID != "asgn-1" {
		t.Fatalf("GetRun linkage = project %q work %q assignment %q, want proj-1/work-1/asgn-1", gotRun.ProjectID, gotRun.WorkItemID, gotRun.AssignmentID)
	}
	if gotRun.WorkflowMode != types.WorkflowModeQA || gotRun.WorkflowVersion != "v0" {
		t.Fatalf("GetRun workflow snapshot = %q/%q, want qa/v0", gotRun.WorkflowMode, gotRun.WorkflowVersion)
	}
	if gotRun.InputRef != run.InputRef || gotRun.InputProviderInstance != run.InputProviderInstance || gotRun.InputProviderDispatchRecorded != run.InputProviderDispatchRecorded || gotRun.InputProviderDisclosedInstance != run.InputProviderDisclosedInstance {
		t.Fatalf("GetRun rich-input fence = ref %q admitted %+v dispatched %t disclosed %+v, want %q/%+v/%t/%+v", gotRun.InputRef, gotRun.InputProviderInstance, gotRun.InputProviderDispatchRecorded, gotRun.InputProviderDisclosedInstance, run.InputRef, run.InputProviderInstance, run.InputProviderDispatchRecorded, run.InputProviderDisclosedInstance)
	}
	if gotRun.ToolCallingVerification != run.ToolCallingVerification {
		t.Fatalf("GetRun tool-calling verification fence = %+v, want %+v", gotRun.ToolCallingVerification, run.ToolCallingVerification)
	}

	for i, status := range []string{"running", "completed"} {
		step := types.TaskStep{
			ID:        "step-" + status,
			TaskID:    "task-1",
			RunID:     "run-1",
			Index:     i,
			Status:    status,
			StartedAt: time.Now().UTC(),
		}
		if _, err := store.AppendStep(ctx, step); err != nil {
			t.Fatalf("AppendStep(%s): %v", status, err)
		}
	}
	steps, err := store.ListSteps(ctx, "run-1")
	if err != nil {
		t.Fatalf("ListSteps: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("ListSteps len = %d, want 2", len(steps))
	}
	// step_index ASC ordering: index 0 first.
	if steps[0].Index != 0 || steps[1].Index != 1 {
		t.Fatalf("ListSteps ordering: %+v", steps)
	}
}

func runStoreListTasksFilterAndLimit(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()

	// Three tasks with staggered updated_at so ordering is
	// deterministic.
	now := time.Now().UTC()
	for i, spec := range []struct {
		id     string
		status string
		ts     time.Time
	}{
		{"t-a1", "queued", now.Add(-3 * time.Minute)},
		{"t-a2", "running", now.Add(-2 * time.Minute)},
		{"t-b1", "queued", now.Add(-1 * time.Minute)},
	} {
		_, err := store.CreateTask(ctx, types.Task{
			ID:        spec.id,
			Status:    spec.status,
			CreatedAt: spec.ts,
			UpdatedAt: spec.ts,
		})
		if err != nil {
			t.Fatalf("CreateTask[%d]: %v", i, err)
		}
	}

	all, err := store.ListTasks(ctx, TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks(all): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("ListTasks all len = %d, want 3", len(all))
	}
	// updated_at DESC: t-b1 first.
	if all[0].ID != "t-b1" {
		t.Fatalf("ListTasks ordering: got first %q, want t-b1", all[0].ID)
	}

	limited, err := store.ListTasks(ctx, TaskFilter{Limit: 2})
	if err != nil {
		t.Fatalf("ListTasks(limit): %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("ListTasks limit len = %d, want 2", len(limited))
	}

	statused, err := store.ListTasks(ctx, TaskFilter{Status: "queued"})
	if err != nil {
		t.Fatalf("ListTasks(status): %v", err)
	}
	if len(statused) != 2 {
		t.Fatalf("ListTasks status len = %d, want 2", len(statused))
	}

	projectID := "proj_a"
	projectScoped, err := store.ListTasks(ctx, TaskFilter{ProjectID: &projectID})
	if err != nil {
		t.Fatalf("ListTasks(project_id): %v", err)
	}
	if len(projectScoped) != 0 {
		t.Fatalf("ListTasks project_id len = %d, want 0 before project-linked fixtures", len(projectScoped))
	}

	for i, spec := range []struct {
		id        string
		projectID string
		ts        time.Time
	}{
		{"t-proj-1", "proj_a", now.Add(1 * time.Minute)},
		{"t-proj-2", "proj_b", now.Add(2 * time.Minute)},
		{"t-none", "", now.Add(3 * time.Minute)},
	} {
		_, err := store.CreateTask(ctx, types.Task{
			ID:        spec.id,
			Status:    "queued",
			ProjectID: spec.projectID,
			CreatedAt: spec.ts,
			UpdatedAt: spec.ts,
		})
		if err != nil {
			t.Fatalf("CreateTask(project[%d]): %v", i, err)
		}
	}

	projectScoped, err = store.ListTasks(ctx, TaskFilter{ProjectID: &projectID})
	if err != nil {
		t.Fatalf("ListTasks(project_id scoped): %v", err)
	}
	if len(projectScoped) != 1 || projectScoped[0].ID != "t-proj-1" {
		t.Fatalf("ListTasks project scope = %#v, want only t-proj-1", projectScoped)
	}

	noProjectID := ""
	unprojected, err := store.ListTasks(ctx, TaskFilter{ProjectID: &noProjectID})
	if err != nil {
		t.Fatalf("ListTasks(project_id empty): %v", err)
	}
	if len(unprojected) == 0 || unprojected[0].ID != "t-none" {
		t.Fatalf("ListTasks unprojected = %#v, want t-none first", unprojected)
	}
}

func runStoreApprovalRoundTrip(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()

	if _, err := store.CreateTask(ctx, types.Task{ID: "task-ap", Status: "running"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	approval := types.TaskApproval{
		ID:                      "ap-1",
		TaskID:                  "task-ap",
		RunID:                   "run-ap",
		Kind:                    "shell",
		Status:                  "pending",
		ActionSummary:           []string{"git branch -vv", "file_write path=notes.txt content_bytes=12"},
		ActionSummaryIncomplete: true,
		RequestedBy:             "agent",
	}
	created, err := store.CreateApproval(ctx, approval)
	if err != nil {
		t.Fatalf("CreateApproval: %v", err)
	}
	if created.CreatedAt.IsZero() {
		t.Fatal("CreateApproval CreatedAt is zero, want store timestamp")
	}
	approval.ActionSummary[0] = "mutated create input"
	created.ActionSummary[0] = "mutated create result"

	got, ok, err := store.GetApproval(ctx, "task-ap", "ap-1")
	if err != nil || !ok {
		t.Fatalf("GetApproval: ok=%v err=%v", ok, err)
	}
	if got.Status != "pending" || got.Kind != "shell" {
		t.Fatalf("GetApproval round-trip mismatch: %+v", got)
	}
	if !reflect.DeepEqual(got.ActionSummary, []string{"git branch -vv", "file_write path=notes.txt content_bytes=12"}) || !got.ActionSummaryIncomplete {
		t.Fatalf("GetApproval action summary = %#v incomplete=%v, want persisted immutable summary", got.ActionSummary, got.ActionSummaryIncomplete)
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("GetApproval CreatedAt is zero, want persisted store timestamp")
	}
	got.ActionSummary[0] = "mutated get result"
	got, ok, err = store.GetApproval(ctx, "task-ap", "ap-1")
	if err != nil || !ok || got.ActionSummary[0] != "git branch -vv" {
		t.Fatalf("GetApproval after result mutation = %#v ok=%v err=%v", got.ActionSummary, ok, err)
	}

	// Resolve.
	got.Status = "approved"
	got.ResolvedBy = "operator"
	got.ResolvedAt = time.Now().UTC()
	got.ResolutionNote = "looks fine"
	updated, err := store.UpdateApproval(ctx, got)
	if err != nil {
		t.Fatalf("UpdateApproval: %v", err)
	}
	got.ActionSummary[0] = "mutated update input"
	updated.ActionSummary[0] = "mutated update result"

	resolved, ok, err := store.GetApproval(ctx, "task-ap", "ap-1")
	if err != nil || !ok {
		t.Fatalf("GetApproval after resolve: ok=%v err=%v", ok, err)
	}
	if resolved.Status != "approved" || resolved.ResolvedBy != "operator" || resolved.ResolutionNote != "looks fine" {
		t.Fatalf("resolution not persisted: %+v", resolved)
	}
	if resolved.ActionSummary[0] != "git branch -vv" || !resolved.ActionSummaryIncomplete {
		t.Fatalf("resolved action summary = %#v incomplete=%v, want original", resolved.ActionSummary, resolved.ActionSummaryIncomplete)
	}

	approvals, err := store.ListApprovals(ctx, "task-ap")
	if err != nil {
		t.Fatalf("ListApprovals: %v", err)
	}
	if len(approvals) != 1 || approvals[0].Status != "approved" {
		t.Fatalf("ListApprovals: %+v", approvals)
	}
	approvals[0].ActionSummary[0] = "mutated list result"
	reread, ok, err := store.GetApproval(ctx, "task-ap", "ap-1")
	if err != nil || !ok || reread.ActionSummary[0] != "git branch -vv" {
		t.Fatalf("GetApproval after list mutation = %#v ok=%v err=%v", reread.ActionSummary, ok, err)
	}
}

func runStoreArtifactRoundTrip(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()

	artifact := types.TaskArtifact{
		ID:          "art-1",
		TaskID:      "task-art",
		RunID:       "run-art",
		StepID:      "step-art",
		Kind:        "log",
		Name:        "build.log",
		MimeType:    "text/plain",
		StorageKind: "inline",
		ContentText: "hello world",
		SizeBytes:   11,
		Status:      "ready",
		CreatedAt:   time.Now().UTC(),
	}
	if _, err := store.CreateArtifact(ctx, artifact); err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}

	got, ok, err := store.GetArtifact(ctx, "task-art", "art-1")
	if err != nil || !ok {
		t.Fatalf("GetArtifact: ok=%v err=%v", ok, err)
	}
	if got.ContentText != "hello world" || got.MimeType != "text/plain" {
		t.Fatalf("GetArtifact round-trip mismatch: %+v", got)
	}

	listed, err := store.ListArtifacts(ctx, ArtifactFilter{TaskID: "task-art"})
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	if len(listed) != 1 || listed[0].ContentText != "hello world" {
		t.Fatalf("ListArtifacts: %+v", listed)
	}

	// Filter by kind that doesn't match — should be empty.
	missing, err := store.ListArtifacts(ctx, ArtifactFilter{TaskID: "task-art", Kind: "trace"})
	if err != nil {
		t.Fatalf("ListArtifacts(kind=trace): %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("ListArtifacts(kind=trace) len = %d, want 0", len(missing))
	}
}

func runStoreRunEventsAppendAndList(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_, err := store.AppendRunEvent(ctx, types.TaskRunEvent{
			TaskID:    "task-evt",
			RunID:     "run-evt",
			EventType: "step.completed",
			Data:      map[string]any{"i": i},
			RequestID: "req-evt",
		})
		if err != nil {
			t.Fatalf("AppendRunEvent[%d]: %v", i, err)
		}
	}

	events, err := store.ListRunEvents(ctx, "task-evt", "run-evt", 0, 100)
	if err != nil {
		t.Fatalf("ListRunEvents: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("ListRunEvents len = %d, want 3", len(events))
	}
	// sequence ASC, so the first event has the smallest sequence.
	if events[0].Sequence >= events[2].Sequence {
		t.Fatalf("sequence ordering: %+v", events)
	}
	// Cursor: afterSequence skips earlier rows.
	tail, err := store.ListRunEvents(ctx, "task-evt", "run-evt", events[0].Sequence, 100)
	if err != nil {
		t.Fatalf("ListRunEvents(cursor): %v", err)
	}
	if len(tail) != 2 {
		t.Fatalf("ListRunEvents(cursor) len = %d, want 2", len(tail))
	}
}

func runStoreListEventsCrossRunFilters(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()

	// Three tasks/runs producing four events of varying types.
	// We want to confirm: (1) cross-run listing returns everything,
	// (2) event_type filter narrows correctly, (3) task_ids filter
	// narrows correctly, (4) afterSequence cursor works the same as
	// the per-run lister.
	mustAppend := func(taskID, runID, eventType string) types.TaskRunEvent {
		evt, err := store.AppendRunEvent(ctx, types.TaskRunEvent{
			TaskID: taskID, RunID: runID, EventType: eventType,
		})
		if err != nil {
			t.Fatalf("AppendRunEvent: %v", err)
		}
		return evt
	}
	e1 := mustAppend("t-A", "r-A", "model.call.completed")
	e2 := mustAppend("t-A", "r-A", "run.finished")
	e3 := mustAppend("t-B", "r-B", "model.call.completed")
	e4 := mustAppend("t-C", "r-C", "approval.requested")
	_ = e1
	_ = e2
	_ = e3
	_ = e4

	t.Run("no filter returns all events globally ordered", func(t *testing.T) {
		events, err := store.ListEvents(ctx, EventFilter{})
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		if len(events) != 4 {
			t.Fatalf("len = %d, want 4", len(events))
		}
		for i := 1; i < len(events); i++ {
			if events[i].Sequence <= events[i-1].Sequence {
				t.Errorf("not sequence-ascending at %d: %+v", i, events)
			}
		}
	})

	t.Run("event_type filter matches OR semantics", func(t *testing.T) {
		events, err := store.ListEvents(ctx, EventFilter{EventTypes: []string{"model.call.completed"}})
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		if len(events) != 2 {
			t.Fatalf("len = %d, want 2 (two model.call.completed)", len(events))
		}
		for _, e := range events {
			if e.EventType != "model.call.completed" {
				t.Errorf("unexpected type %q", e.EventType)
			}
		}
	})

	t.Run("task_ids filter restricts to listed tasks", func(t *testing.T) {
		events, err := store.ListEvents(ctx, EventFilter{TaskIDs: []string{"t-A", "t-C"}})
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		if len(events) != 3 {
			t.Fatalf("len = %d, want 3 (t-A: 2, t-C: 1)", len(events))
		}
		for _, e := range events {
			if e.TaskID != "t-A" && e.TaskID != "t-C" {
				t.Errorf("unexpected task %q in result", e.TaskID)
			}
		}
	})

	t.Run("after_sequence cursor skips older rows", func(t *testing.T) {
		events, err := store.ListEvents(ctx, EventFilter{AfterSequence: e2.Sequence})
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		if len(events) != 2 {
			t.Fatalf("len = %d, want 2 (e3, e4)", len(events))
		}
		if events[0].Sequence != e3.Sequence {
			t.Errorf("first sequence = %d, want %d", events[0].Sequence, e3.Sequence)
		}
	})

	t.Run("combined filters AND together", func(t *testing.T) {
		events, err := store.ListEvents(ctx, EventFilter{
			EventTypes: []string{"model.call.completed"},
			TaskIDs:    []string{"t-B"},
		})
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		if len(events) != 1 || events[0].TaskID != "t-B" {
			t.Errorf("expected one event from t-B, got %+v", events)
		}
	})

	t.Run("limit caps the response size", func(t *testing.T) {
		events, err := store.ListEvents(ctx, EventFilter{Limit: 2})
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		if len(events) != 2 {
			t.Fatalf("len = %d, want 2 (limit honored)", len(events))
		}
	})
}

func runStoreApplyRunTerminalTransition(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Minute)
	finishedAt := now.Add(time.Minute)
	task := types.Task{
		ID:        "task-terminal",
		Status:    "awaiting_approval",
		CreatedAt: now,
		UpdatedAt: now,
		StartedAt: now,
	}
	run := types.TaskRun{
		ID:        "run-terminal",
		TaskID:    task.ID,
		Number:    1,
		Status:    "awaiting_approval",
		StartedAt: now,
		RequestID: "req-terminal",
		TraceID:   "trace-terminal",
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	awaitingStep := types.TaskStep{
		ID:        "step-awaiting",
		TaskID:    task.ID,
		RunID:     run.ID,
		Index:     1,
		Status:    "awaiting_approval",
		StartedAt: now,
	}
	doneStep := types.TaskStep{
		ID:        "step-done",
		TaskID:    task.ID,
		RunID:     run.ID,
		Index:     2,
		Status:    "completed",
		StartedAt: now,
	}
	if _, err := store.AppendStep(ctx, awaitingStep); err != nil {
		t.Fatalf("AppendStep(awaiting): %v", err)
	}
	if _, err := store.AppendStep(ctx, doneStep); err != nil {
		t.Fatalf("AppendStep(done): %v", err)
	}
	approval := types.TaskApproval{
		ID:          "approval-terminal",
		TaskID:      task.ID,
		RunID:       run.ID,
		StepID:      awaitingStep.ID,
		Kind:        "tool_call",
		Status:      "pending",
		RequestedBy: "agent",
		CreatedAt:   now,
	}
	if _, err := store.CreateApproval(ctx, approval); err != nil {
		t.Fatalf("CreateApproval: %v", err)
	}
	streamingArtifact := types.TaskArtifact{
		ID:        "artifact-streaming",
		TaskID:    task.ID,
		RunID:     run.ID,
		Status:    "streaming",
		CreatedAt: now,
	}
	readyArtifact := types.TaskArtifact{
		ID:        "artifact-ready",
		TaskID:    task.ID,
		RunID:     run.ID,
		Status:    "ready",
		CreatedAt: now,
	}
	if _, err := store.CreateArtifact(ctx, streamingArtifact); err != nil {
		t.Fatalf("CreateArtifact(streaming): %v", err)
	}
	if _, err := store.CreateArtifact(ctx, readyArtifact); err != nil {
		t.Fatalf("CreateArtifact(ready): %v", err)
	}

	run.Status = "cancelled"
	run.LastError = "run cancelled: operator stop"
	run.FinishedAt = finishedAt
	run.OtelStatusCode = "error"
	run.OtelStatusMessage = run.LastError
	task.Status = "cancelled"
	task.LatestRunID = run.ID
	task.LastError = run.LastError
	task.FinishedAt = finishedAt
	task.UpdatedAt = finishedAt
	result, err := store.ApplyRunTerminalTransition(ctx, TerminalRunTransition{
		Task:                          task,
		Run:                           run,
		FinishedAt:                    finishedAt,
		CancelActiveSteps:             true,
		ActiveStepResult:              "error",
		ActiveStepError:               run.LastError,
		ActiveStepErrorKind:           "run_cancelled",
		CancelStreamingArtifacts:      true,
		CancelPendingApprovals:        true,
		PendingApprovalStatus:         "cancelled",
		PendingApprovalResolvedBy:     "system",
		PendingApprovalResolutionNote: run.LastError,
		ApprovalResolvedEventType:     "approval.resolved",
		TerminalEvent: &RunEventSpec{
			EventType: "run.cancelled",
			Data:      map[string]any{"reason": run.LastError},
			RequestID: run.RequestID,
			TraceID:   run.TraceID,
			CreatedAt: finishedAt,
		},
		TaskUpdatedEvent: &RunEventSpec{
			EventType: "task.updated",
			RequestID: run.RequestID,
			TraceID:   run.TraceID,
			CreatedAt: finishedAt,
		},
	})
	if err != nil {
		t.Fatalf("ApplyRunTerminalTransition: %v", err)
	}
	if result.Run.Status != "cancelled" || result.Task.Status != "cancelled" {
		t.Fatalf("result statuses task=%q run=%q, want cancelled/cancelled", result.Task.Status, result.Run.Status)
	}
	if len(result.CancelledApprovals) != 1 || result.CancelledApprovals[0].Status != "cancelled" {
		t.Fatalf("cancelled approvals = %+v, want one cancelled", result.CancelledApprovals)
	}
	if result.CancelledApprovals[0].ResolvedBy != "system" || result.CancelledApprovals[0].ResolutionNote != run.LastError {
		t.Fatalf("cancelled approval resolution = %+v", result.CancelledApprovals[0])
	}

	storedRun, found, err := store.GetRun(ctx, task.ID, run.ID)
	if err != nil || !found {
		t.Fatalf("GetRun: found=%v err=%v", found, err)
	}
	if storedRun.Status != "cancelled" || storedRun.LastError != run.LastError {
		t.Fatalf("stored run = %+v, want cancelled with last_error", storedRun)
	}
	storedTask, found, err := store.GetTask(ctx, task.ID)
	if err != nil || !found {
		t.Fatalf("GetTask: found=%v err=%v", found, err)
	}
	if storedTask.Status != "cancelled" || storedTask.LastError != run.LastError {
		t.Fatalf("stored task = %+v, want cancelled with last_error", storedTask)
	}
	storedAwaitingStep, found, err := store.GetStep(ctx, run.ID, awaitingStep.ID)
	if err != nil || !found {
		t.Fatalf("GetStep(awaiting): found=%v err=%v", found, err)
	}
	if storedAwaitingStep.Status != "cancelled" || storedAwaitingStep.ErrorKind != "run_cancelled" {
		t.Fatalf("awaiting step = %+v, want cancelled run_cancelled", storedAwaitingStep)
	}
	storedDoneStep, found, err := store.GetStep(ctx, run.ID, doneStep.ID)
	if err != nil || !found {
		t.Fatalf("GetStep(done): found=%v err=%v", found, err)
	}
	if storedDoneStep.Status != "completed" {
		t.Fatalf("done step status = %q, want completed", storedDoneStep.Status)
	}
	storedStreamingArtifact, found, err := store.GetArtifact(ctx, task.ID, streamingArtifact.ID)
	if err != nil || !found {
		t.Fatalf("GetArtifact(streaming): found=%v err=%v", found, err)
	}
	if storedStreamingArtifact.Status != "cancelled" {
		t.Fatalf("streaming artifact status = %q, want cancelled", storedStreamingArtifact.Status)
	}
	storedReadyArtifact, found, err := store.GetArtifact(ctx, task.ID, readyArtifact.ID)
	if err != nil || !found {
		t.Fatalf("GetArtifact(ready): found=%v err=%v", found, err)
	}
	if storedReadyArtifact.Status != "ready" {
		t.Fatalf("ready artifact status = %q, want ready", storedReadyArtifact.Status)
	}

	events, err := store.ListRunEvents(ctx, task.ID, run.ID, 0, 10)
	if err != nil {
		t.Fatalf("ListRunEvents: %v", err)
	}
	wantTypes := []string{"approval.resolved", "run.cancelled", "task.updated"}
	if len(events) != len(wantTypes) {
		t.Fatalf("events len = %d, want %d: %+v", len(events), len(wantTypes), events)
	}
	for i, want := range wantTypes {
		if events[i].EventType != want {
			t.Fatalf("event[%d] type = %q, want %q", i, events[i].EventType, want)
		}
		if events[i].Sequence <= 0 {
			t.Fatalf("event[%d] sequence = %d, want positive", i, events[i].Sequence)
		}
		assertNoTerminalEventSnapshot(t, events[i])
	}
	if got := events[0].Data; got["approval_id"] != approval.ID || got["decision"] != "cancelled" || got["status"] != "cancelled" || got["by"] != "system" {
		t.Fatalf("approval.resolved data = %+v, want compact cancelled approval data", got)
	}
	if got := events[1].Data; got["reason"] != run.LastError {
		t.Fatalf("run.cancelled data = %+v, want reason %q", got, run.LastError)
	}
	if events[2].Data != nil && len(events[2].Data) != 0 {
		t.Fatalf("task.updated data = %+v, want no extra keys", events[2].Data)
	}
}

func assertNoTerminalEventSnapshot(t *testing.T, event types.TaskRunEvent) {
	t.Helper()
	for _, key := range []string{"run", "steps", "artifacts", "snapshot"} {
		if _, ok := event.Data[key]; ok {
			t.Fatalf("%s event carried %q snapshot data: %+v", event.EventType, key, event.Data)
		}
	}
}

func runStoreApplyRunTerminalTransitionSameStatusReplay(t *testing.T, store Store) {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC().Add(-time.Minute)
	winnerFinishedAt := now.Add(30 * time.Second)
	task, err := store.CreateTask(ctx, types.Task{
		ID: "task-terminal-same-replay", Title: "authoritative title", Status: "running",
		CreatedAt: now, UpdatedAt: now, StartedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	run, err := store.CreateRun(ctx, types.TaskRun{
		ID: "run-terminal-same-replay", TaskID: task.ID, Number: 1, Status: "running",
		StartedAt: now, RequestID: "request-terminal-winner", TraceID: "trace-terminal-winner",
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	winnerReason := "run cancelled: first winner"
	winnerRun := run
	winnerRun.Status = "cancelled"
	winnerRun.LastError = winnerReason
	winnerRun.FinishedAt = winnerFinishedAt
	winnerRun.OtelStatusCode = "error"
	winnerRun.OtelStatusMessage = winnerReason
	winnerRun.StepCount = 2
	winnerRun.ModelCallCount = 1
	winnerRun.ArtifactCount = 5
	winnerRun.TotalCostMicrosUSD = 100
	winnerTask := task
	winnerTask.Status = "cancelled"
	winnerTask.LatestRunID = winnerRun.ID
	winnerTask.LastError = winnerReason
	winnerTask.FinishedAt = winnerFinishedAt
	winnerTask.UpdatedAt = winnerFinishedAt
	winnerTask.LatestRequestID = winnerRun.RequestID
	winnerTask.LatestTraceID = winnerRun.TraceID
	first, err := store.ApplyRunTerminalTransition(ctx, TerminalRunTransition{
		Task: winnerTask, Run: winnerRun, FinishedAt: winnerFinishedAt,
		TerminalEvent: &RunEventSpec{
			EventType: "run.cancelled", Data: map[string]any{"reason": winnerReason},
			RequestID: winnerRun.RequestID, TraceID: winnerRun.TraceID, CreatedAt: winnerFinishedAt,
		},
		TaskUpdatedEvent: &RunEventSpec{
			EventType: "task.updated", RequestID: winnerRun.RequestID,
			TraceID: winnerRun.TraceID, CreatedAt: winnerFinishedAt,
		},
	})
	if err != nil || !first.Applied {
		t.Fatalf("winner transition applied=%t err=%v", first.Applied, err)
	}

	replayFinishedAt := winnerFinishedAt.Add(time.Second)
	lateChildAt := replayFinishedAt.Add(time.Second)
	lateStep := types.TaskStep{
		ID: "step-terminal-same-replay", TaskID: task.ID, RunID: run.ID,
		Index: 1, Status: "running", StartedAt: lateChildAt,
	}
	if _, err := store.AppendStep(ctx, lateStep); err != nil {
		t.Fatalf("AppendStep(late): %v", err)
	}
	lateArtifact := types.TaskArtifact{
		ID: "artifact-terminal-same-replay", TaskID: task.ID, RunID: run.ID,
		Status: "streaming", CreatedAt: lateChildAt,
	}
	if _, err := store.CreateArtifact(ctx, lateArtifact); err != nil {
		t.Fatalf("CreateArtifact(late): %v", err)
	}
	lateApproval := types.TaskApproval{
		ID: "approval-terminal-same-replay", TaskID: task.ID, RunID: run.ID,
		StepID: lateStep.ID, Kind: "tool_call", Status: "pending", RequestedBy: "agent",
		CreatedAt: lateChildAt,
	}
	if _, err := store.CreateApproval(ctx, lateApproval); err != nil {
		t.Fatalf("CreateApproval(late): %v", err)
	}

	replayReason := "run cancelled: stale replay"
	replayRun := run
	replayRun.Status = "cancelled"
	replayRun.LastError = replayReason
	replayRun.FinishedAt = replayFinishedAt
	replayRun.RequestID = "request-terminal-replay"
	replayRun.TraceID = "trace-terminal-replay"
	replayRun.Provider = "provider-terminal-replay"
	replayRun.ProviderKind = "openai"
	replayRun.Model = "model-terminal-replay"
	replayRun.StepCount = 4
	replayRun.ModelCallCount = 3
	replayRun.ArtifactCount = 3
	replayRun.TotalCostMicrosUSD = 250
	replayTask := task
	replayTask.Title = "stale title"
	replayTask.Status = "cancelled"
	replayTask.LatestRunID = replayRun.ID
	replayTask.LastError = replayReason
	replayTask.FinishedAt = replayFinishedAt
	replayTask.UpdatedAt = replayFinishedAt
	replay, err := store.ApplyRunTerminalTransition(ctx, TerminalRunTransition{
		Task: replayTask, Run: replayRun, FinishedAt: replayFinishedAt,
		TrustedSupplementalRunMetadata: &TerminalRunSupplementalMetadata{
			Provider:           replayRun.Provider,
			ProviderKind:       replayRun.ProviderKind,
			Model:              replayRun.Model,
			StepCount:          replayRun.StepCount,
			ModelCallCount:     replayRun.ModelCallCount,
			ArtifactCount:      replayRun.ArtifactCount,
			TotalCostMicrosUSD: replayRun.TotalCostMicrosUSD,
		},
		CancelActiveSteps:             true,
		ActiveStepResult:              "error",
		ActiveStepError:               replayReason,
		ActiveStepErrorKind:           "run_cancelled",
		CancelStreamingArtifacts:      true,
		CancelPendingApprovals:        true,
		PendingApprovalStatus:         "cancelled",
		PendingApprovalResolvedBy:     "system",
		PendingApprovalResolutionNote: replayReason,
		ApprovalResolvedEventType:     "approval.resolved",
		TerminalEvent: &RunEventSpec{
			EventType: "run.cancelled", Data: map[string]any{"reason": replayReason},
			RequestID: replayRun.RequestID, TraceID: replayRun.TraceID, CreatedAt: replayFinishedAt,
		},
		TaskUpdatedEvent: &RunEventSpec{
			EventType: "task.updated", RequestID: replayRun.RequestID,
			TraceID: replayRun.TraceID, CreatedAt: replayFinishedAt,
		},
	})
	if err != nil || !replay.Applied {
		t.Fatalf("same-status replay applied=%t err=%v", replay.Applied, err)
	}
	if replay.Run.LastError != winnerReason || !replay.Run.FinishedAt.Equal(winnerFinishedAt) ||
		replay.Run.RequestID != winnerRun.RequestID || replay.Run.TraceID != winnerRun.TraceID {
		t.Fatalf("replay run = %+v, want first terminal winner", replay.Run)
	}
	if replay.Run.Provider != replayRun.Provider || replay.Run.ProviderKind != replayRun.ProviderKind ||
		replay.Run.Model != replayRun.Model || replay.Run.StepCount != 4 ||
		replay.Run.ModelCallCount != 3 || replay.Run.ArtifactCount != 5 || replay.Run.TotalCostMicrosUSD != 250 {
		t.Fatalf("replay run metadata = %+v, want safe supplemental fields with monotonic counts/cost", replay.Run)
	}
	if replay.Task.Title != winnerTask.Title || replay.Task.LastError != winnerReason ||
		replay.Task.LatestRequestID != winnerRun.RequestID || replay.Task.LatestTraceID != winnerRun.TraceID ||
		!replay.Task.FinishedAt.Equal(winnerFinishedAt) {
		t.Fatalf("replay task = %+v, want first terminal winner projection", replay.Task)
	}
	storedRun, found, err := store.GetRun(ctx, task.ID, run.ID)
	if err != nil || !found || storedRun.LastError != winnerReason ||
		storedRun.Provider != replayRun.Provider || storedRun.StepCount != 4 ||
		storedRun.ModelCallCount != 3 || storedRun.ArtifactCount != 5 || storedRun.TotalCostMicrosUSD != 250 {
		t.Fatalf("stored replay run = %+v found=%t err=%v, want winner plus supplemental metadata", storedRun, found, err)
	}
	if len(replay.CancelledApprovals) != 1 || replay.CancelledApprovals[0].ID != lateApproval.ID {
		t.Fatalf("replay cancelled approvals = %+v, want late approval", replay.CancelledApprovals)
	}

	storedStep, found, err := store.GetStep(ctx, run.ID, lateStep.ID)
	if err != nil || !found || storedStep.Status != "cancelled" || storedStep.Error != winnerReason ||
		storedStep.FinishedAt.Before(storedStep.StartedAt) {
		t.Fatalf("late step = %+v found=%t err=%v, want winner cancellation", storedStep, found, err)
	}
	storedArtifact, found, err := store.GetArtifact(ctx, task.ID, lateArtifact.ID)
	if err != nil || !found || storedArtifact.Status != "cancelled" {
		t.Fatalf("late artifact = %+v found=%t err=%v, want cancelled", storedArtifact, found, err)
	}
	storedApproval, found, err := store.GetApproval(ctx, task.ID, lateApproval.ID)
	if err != nil || !found || storedApproval.Status != "cancelled" ||
		storedApproval.ResolutionNote != winnerReason || storedApproval.ResolvedBy != "system" ||
		storedApproval.ResolvedAt.Before(storedApproval.CreatedAt) {
		t.Fatalf("late approval = %+v found=%t err=%v, want winner cancellation", storedApproval, found, err)
	}

	staleCancelRun := replayRun
	staleCancelRun.Provider = "stale-cancel-provider"
	staleCancelRun.ProviderKind = "stale-cancel-kind"
	staleCancelRun.Model = "stale-cancel-model"
	staleCancelRun.StepCount = 99
	staleCancelRun.ModelCallCount = 99
	staleCancelRun.ArtifactCount = 99
	staleCancelRun.TotalCostMicrosUSD = 999
	staleCancel, err := store.ApplyRunTerminalTransition(ctx, TerminalRunTransition{
		Task: replayTask, Run: staleCancelRun, FinishedAt: replayFinishedAt.Add(time.Second),
		TerminalEvent: &RunEventSpec{
			EventType: "run.cancelled", Data: map[string]any{"reason": replayReason},
		},
		TaskUpdatedEvent: &RunEventSpec{EventType: "task.updated"},
	})
	if err != nil || !staleCancel.Applied {
		t.Fatalf("stale cancellation replay applied=%t err=%v", staleCancel.Applied, err)
	}
	if staleCancel.Run.Provider != replayRun.Provider || staleCancel.Run.ProviderKind != replayRun.ProviderKind ||
		staleCancel.Run.Model != replayRun.Model || staleCancel.Run.StepCount != 4 ||
		staleCancel.Run.ModelCallCount != 3 || staleCancel.Run.ArtifactCount != 5 || staleCancel.Run.TotalCostMicrosUSD != 250 {
		t.Fatalf("stale cancellation replay replaced trusted execution metadata: %+v", staleCancel.Run)
	}

	events, err := store.ListRunEvents(ctx, task.ID, run.ID, 0, 20)
	if err != nil {
		t.Fatalf("ListRunEvents: %v", err)
	}
	counts := make(map[string]int)
	for _, event := range events {
		counts[event.EventType]++
	}
	if counts["run.cancelled"] != 1 || counts["task.updated"] != 1 || counts["approval.resolved"] != 1 || len(events) != 3 {
		t.Fatalf("events/counts = %+v/%v, want one terminal, task, and late approval event", events, counts)
	}
	if events[2].RequestID != winnerRun.RequestID || events[2].TraceID != winnerRun.TraceID {
		t.Fatalf("late approval event trace = request:%q trace:%q, want winner request/trace", events[2].RequestID, events[2].TraceID)
	}
	if events[2].CreatedAt.Before(lateApproval.CreatedAt) {
		t.Fatalf("late approval event predates approval: event=%s approval=%s", events[2].CreatedAt, lateApproval.CreatedAt)
	}
}

func runStoreApplyRunTerminalTransitionTrustedMetadataAfterDifferentStatusWinner(t *testing.T, store Store) {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC().Add(-time.Minute)
	winnerFinishedAt := now.Add(10 * time.Second)
	task, err := store.CreateTask(ctx, types.Task{
		ID: "task-terminal-different-replay", Title: "authoritative task", Status: "running",
		CreatedAt: now, UpdatedAt: now, StartedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	run, err := store.CreateRun(ctx, types.TaskRun{
		ID: "run-terminal-different-replay", TaskID: task.ID, Number: 1, Status: "running",
		StartedAt: now, RequestID: "request-terminal-different-winner", TraceID: "trace-terminal-different-winner",
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	step := types.TaskStep{
		ID: "step-terminal-different-replay", TaskID: task.ID, RunID: run.ID,
		Index: 1, Status: "running", StartedAt: now,
	}
	if _, err := store.AppendStep(ctx, step); err != nil {
		t.Fatalf("AppendStep: %v", err)
	}
	artifact := types.TaskArtifact{
		ID: "artifact-terminal-different-replay", TaskID: task.ID, RunID: run.ID,
		Status: "streaming", CreatedAt: now,
	}
	if _, err := store.CreateArtifact(ctx, artifact); err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	approval := types.TaskApproval{
		ID: "approval-terminal-different-replay", TaskID: task.ID, RunID: run.ID,
		StepID: step.ID, Kind: "tool_call", Status: "pending", RequestedBy: "agent", CreatedAt: now,
	}
	if _, err := store.CreateApproval(ctx, approval); err != nil {
		t.Fatalf("CreateApproval: %v", err)
	}

	winnerReason := "run cancelled: operator winner"
	winnerRun := run
	winnerRun.Status = "cancelled"
	winnerRun.LastError = winnerReason
	winnerRun.FinishedAt = winnerFinishedAt
	winnerRun.OtelStatusCode = "error"
	winnerRun.OtelStatusMessage = winnerReason
	winnerRun.StepCount = 1
	winnerRun.ModelCallCount = 1
	winnerRun.ArtifactCount = 4
	winnerRun.TotalCostMicrosUSD = 50
	winnerTask := task
	winnerTask.Status = "cancelled"
	winnerTask.LatestRunID = run.ID
	winnerTask.LastError = winnerReason
	winnerTask.FinishedAt = winnerFinishedAt
	winnerTask.UpdatedAt = winnerFinishedAt
	winnerTask.LatestRequestID = winnerRun.RequestID
	winnerTask.LatestTraceID = winnerRun.TraceID
	first, err := store.ApplyRunTerminalTransition(ctx, TerminalRunTransition{
		Task: winnerTask, Run: winnerRun, FinishedAt: winnerFinishedAt,
		TerminalEvent: &RunEventSpec{
			EventType: "run.cancelled", Data: map[string]any{"reason": winnerReason},
			RequestID: winnerRun.RequestID, TraceID: winnerRun.TraceID, CreatedAt: winnerFinishedAt,
		},
		TaskUpdatedEvent: &RunEventSpec{
			EventType: "task.updated", RequestID: winnerRun.RequestID,
			TraceID: winnerRun.TraceID, CreatedAt: winnerFinishedAt,
		},
	})
	if err != nil || !first.Applied {
		t.Fatalf("winner transition applied=%t err=%v", first.Applied, err)
	}

	loserFinishedAt := winnerFinishedAt.Add(20 * time.Second)
	loserRun := run
	loserRun.Status = "completed"
	loserRun.FinishedAt = loserFinishedAt
	loserRun.RequestID = "request-terminal-different-loser"
	loserRun.TraceID = "trace-terminal-different-loser"
	loserRun.Provider = "actual-provider"
	loserRun.ProviderKind = "openai"
	loserRun.Model = "actual-model"
	loserRun.StepCount = 3
	loserRun.ModelCallCount = 3
	loserRun.ArtifactCount = 2
	loserRun.TotalCostMicrosUSD = 250
	loserRun.OtelStatusCode = "ok"
	loserTask := task
	loserTask.Title = "stale loser task"
	loserTask.Status = "completed"
	loserTask.LatestRunID = run.ID
	loserTask.FinishedAt = loserFinishedAt
	loserTask.UpdatedAt = loserFinishedAt
	loser, err := store.ApplyRunTerminalTransition(ctx, TerminalRunTransition{
		Task: loserTask, Run: loserRun, FinishedAt: loserFinishedAt,
		TrustedSupplementalRunMetadata: &TerminalRunSupplementalMetadata{
			Provider:           loserRun.Provider,
			ProviderKind:       loserRun.ProviderKind,
			Model:              loserRun.Model,
			StepCount:          loserRun.StepCount,
			ModelCallCount:     loserRun.ModelCallCount,
			ArtifactCount:      loserRun.ArtifactCount,
			TotalCostMicrosUSD: loserRun.TotalCostMicrosUSD,
		},
		// Deliberately request every loser-side effect. A different-status
		// trusted replay may enrich accounting only; it cannot clean up the
		// winner's children or emit loser events.
		CancelActiveSteps:        true,
		CancelStreamingArtifacts: true,
		CancelPendingApprovals:   true,
		TerminalEvent: &RunEventSpec{
			EventType: "run.finished", RequestID: loserRun.RequestID,
			TraceID: loserRun.TraceID, CreatedAt: loserFinishedAt,
		},
		TaskUpdatedEvent: &RunEventSpec{
			EventType: "task.updated", RequestID: loserRun.RequestID,
			TraceID: loserRun.TraceID, CreatedAt: loserFinishedAt,
		},
	})
	if err != nil || !loser.Applied {
		t.Fatalf("trusted loser transition applied=%t err=%v", loser.Applied, err)
	}
	if loser.Run.Status != "cancelled" || loser.Run.LastError != winnerReason ||
		!loser.Run.FinishedAt.Equal(winnerFinishedAt) || loser.Run.RequestID != winnerRun.RequestID ||
		loser.Run.TraceID != winnerRun.TraceID || loser.Run.OtelStatusCode != "error" ||
		loser.Run.OtelStatusMessage != winnerReason {
		t.Fatalf("trusted loser run = %+v, want cancellation winner", loser.Run)
	}
	if loser.Run.Provider != loserRun.Provider || loser.Run.ProviderKind != loserRun.ProviderKind ||
		loser.Run.Model != loserRun.Model || loser.Run.StepCount != 3 ||
		loser.Run.ModelCallCount != 3 || loser.Run.ArtifactCount != 4 || loser.Run.TotalCostMicrosUSD != 250 {
		t.Fatalf("trusted loser metadata = %+v, want safe route and monotonic accounting", loser.Run)
	}
	if loser.Task.Title != winnerTask.Title || loser.Task.Status != "cancelled" ||
		loser.Task.LastError != winnerReason || !loser.Task.FinishedAt.Equal(winnerFinishedAt) {
		t.Fatalf("trusted loser task = %+v, want cancellation winner projection", loser.Task)
	}
	if len(loser.CancelledApprovals) != 0 || len(loser.Events) != 0 {
		t.Fatalf("trusted loser side effects = approvals:%+v events:%+v, want none", loser.CancelledApprovals, loser.Events)
	}

	storedStep, found, err := store.GetStep(ctx, run.ID, step.ID)
	if err != nil || !found || storedStep.Status != "running" || !storedStep.FinishedAt.IsZero() {
		t.Fatalf("winner step changed by trusted loser: step=%+v found=%t err=%v", storedStep, found, err)
	}
	storedArtifact, found, err := store.GetArtifact(ctx, task.ID, artifact.ID)
	if err != nil || !found || storedArtifact.Status != "streaming" {
		t.Fatalf("winner artifact changed by trusted loser: artifact=%+v found=%t err=%v", storedArtifact, found, err)
	}
	storedApproval, found, err := store.GetApproval(ctx, task.ID, approval.ID)
	if err != nil || !found || storedApproval.Status != "pending" || !storedApproval.ResolvedAt.IsZero() {
		t.Fatalf("winner approval changed by trusted loser: approval=%+v found=%t err=%v", storedApproval, found, err)
	}
	events, err := store.ListRunEvents(ctx, task.ID, run.ID, 0, 20)
	if err != nil {
		t.Fatalf("ListRunEvents: %v", err)
	}
	if len(events) != 2 || events[0].EventType != "run.cancelled" || events[1].EventType != "task.updated" {
		t.Fatalf("events = %+v, want only winner terminal/task events", events)
	}
}

func runStoreApplyRunTerminalTransitionConcurrentSameStatus(t *testing.T, store Store) {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC().Add(-time.Minute)
	task, err := store.CreateTask(ctx, types.Task{
		ID: "task-terminal-concurrent-same", Status: "running", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	run, err := store.CreateRun(ctx, types.TaskRun{
		ID: "run-terminal-concurrent-same", TaskID: task.ID, Status: "running", StartedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	start := make(chan struct{})
	errs := make([]error, 2)
	reasons := []string{"run cancelled: contender zero", "run cancelled: contender one"}
	finished := []time.Time{now.Add(10 * time.Second), now.Add(20 * time.Second)}
	var wg sync.WaitGroup
	for index := range reasons {
		index := index
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			candidateRun := run
			candidateRun.Status = "cancelled"
			candidateRun.LastError = reasons[index]
			candidateRun.FinishedAt = finished[index]
			candidateRun.RequestID = fmt.Sprintf("request-terminal-contender-%d", index)
			candidateRun.TraceID = fmt.Sprintf("trace-terminal-contender-%d", index)
			candidateTask := task
			candidateTask.Status = "cancelled"
			candidateTask.LatestRunID = candidateRun.ID
			candidateTask.LastError = candidateRun.LastError
			candidateTask.FinishedAt = candidateRun.FinishedAt
			candidateTask.UpdatedAt = candidateRun.FinishedAt
			_, errs[index] = store.ApplyRunTerminalTransition(ctx, TerminalRunTransition{
				Task: candidateTask, Run: candidateRun, FinishedAt: candidateRun.FinishedAt,
				TerminalEvent: &RunEventSpec{
					EventType: "run.cancelled", Data: map[string]any{"reason": candidateRun.LastError},
					RequestID: candidateRun.RequestID, TraceID: candidateRun.TraceID,
					CreatedAt: candidateRun.FinishedAt,
				},
				TaskUpdatedEvent: &RunEventSpec{
					EventType: "task.updated", RequestID: candidateRun.RequestID,
					TraceID: candidateRun.TraceID, CreatedAt: candidateRun.FinishedAt,
				},
			})
		}()
	}
	close(start)
	wg.Wait()
	for index, err := range errs {
		if err != nil {
			t.Fatalf("contender %d: %v", index, err)
		}
	}

	storedRun, found, err := store.GetRun(ctx, task.ID, run.ID)
	if err != nil || !found {
		t.Fatalf("GetRun: found=%t err=%v", found, err)
	}
	storedTask, found, err := store.GetTask(ctx, task.ID)
	if err != nil || !found {
		t.Fatalf("GetTask: found=%t err=%v", found, err)
	}
	if storedTask.LastError != storedRun.LastError || !storedTask.FinishedAt.Equal(storedRun.FinishedAt) {
		t.Fatalf("task/run winners diverged: task=%+v run=%+v", storedTask, storedRun)
	}

	events, err := store.ListRunEvents(ctx, task.ID, run.ID, 0, 10)
	if err != nil {
		t.Fatalf("ListRunEvents: %v", err)
	}
	counts := make(map[string]int)
	var terminalEvent types.TaskRunEvent
	for _, event := range events {
		counts[event.EventType]++
		if event.EventType == "run.cancelled" {
			terminalEvent = event
		}
	}
	if counts["run.cancelled"] != 1 || counts["task.updated"] != 1 || len(events) != 2 {
		t.Fatalf("events/counts = %+v/%v, want one terminal and task event", events, counts)
	}
	if terminalEvent.Data["reason"] != storedRun.LastError || terminalEvent.RequestID != storedRun.RequestID || terminalEvent.TraceID != storedRun.TraceID {
		t.Fatalf("terminal event = %+v, want stored winner %+v", terminalEvent, storedRun)
	}
}

func runStoreApplyRunTerminalTransitionPreservesTaskProjection(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Minute)
	authoritativeTask := types.Task{
		ID: "task-terminal-child-cleanup", Title: "authoritative title", Status: "queued",
		LatestRunID: "run-terminal-new", BudgetMicrosUSD: 300,
		CreatedAt: now, UpdatedAt: now.Add(30 * time.Second), StartedAt: now,
	}
	if _, err := store.CreateTask(ctx, authoritativeTask); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	oldRun := types.TaskRun{
		ID: "run-terminal-old", TaskID: authoritativeTask.ID, Number: 1, Status: "cancelled",
		StartedAt: now, FinishedAt: now.Add(10 * time.Second), LastError: "run cancelled",
	}
	if _, err := store.CreateRun(ctx, oldRun); err != nil {
		t.Fatalf("CreateRun(old): %v", err)
	}
	newRun := types.TaskRun{
		ID: authoritativeTask.LatestRunID, TaskID: authoritativeTask.ID, Number: 2,
		Status: "queued", StartedAt: now.Add(20 * time.Second),
	}
	if _, err := store.CreateRun(ctx, newRun); err != nil {
		t.Fatalf("CreateRun(new): %v", err)
	}
	approval := types.TaskApproval{
		ID: "approval-terminal-late", TaskID: authoritativeTask.ID, RunID: oldRun.ID,
		Kind: "tool_call", Status: "pending", RequestedBy: "agent", CreatedAt: now.Add(15 * time.Second),
	}
	if _, err := store.CreateApproval(ctx, approval); err != nil {
		t.Fatalf("CreateApproval: %v", err)
	}

	staleTask := authoritativeTask
	staleTask.Title = "stale title"
	staleTask.Status = "cancelled"
	staleTask.LatestRunID = oldRun.ID
	staleTask.BudgetMicrosUSD = 1
	staleTask.FinishedAt = oldRun.FinishedAt
	result, err := store.ApplyRunTerminalTransition(ctx, TerminalRunTransition{
		Task: staleTask, Run: oldRun, FinishedAt: oldRun.FinishedAt,
		PreserveTaskProjection:        true,
		CancelPendingApprovals:        true,
		PendingApprovalStatus:         "cancelled",
		PendingApprovalResolvedBy:     "system",
		PendingApprovalResolutionNote: oldRun.LastError,
	})
	if err != nil {
		t.Fatalf("ApplyRunTerminalTransition: %v", err)
	}
	if !result.Applied {
		t.Fatal("ApplyRunTerminalTransition did not apply child cleanup")
	}
	if result.Task.Title != authoritativeTask.Title || result.Task.Status != "queued" ||
		result.Task.LatestRunID != newRun.ID || result.Task.BudgetMicrosUSD != 300 ||
		!result.Task.FinishedAt.IsZero() {
		t.Fatalf("result task = %+v, want authoritative newer-run projection", result.Task)
	}
	storedTask, found, err := store.GetTask(ctx, authoritativeTask.ID)
	if err != nil || !found {
		t.Fatalf("GetTask: found=%t err=%v", found, err)
	}
	if storedTask.Title != authoritativeTask.Title || storedTask.Status != "queued" ||
		storedTask.LatestRunID != newRun.ID || storedTask.BudgetMicrosUSD != 300 ||
		!storedTask.FinishedAt.IsZero() {
		t.Fatalf("stored task = %+v, want authoritative newer-run projection", storedTask)
	}
	approvals, err := store.ListApprovals(ctx, authoritativeTask.ID)
	if err != nil || len(approvals) != 1 || approvals[0].Status != "cancelled" || approvals[0].ResolvedBy != "system" {
		t.Fatalf("late approval cleanup = %+v err=%v, want one system-cancelled approval", approvals, err)
	}
	if approvals[0].ResolvedAt.Before(approvals[0].CreatedAt) {
		t.Fatalf("late approval cleanup resolved before creation: %+v", approvals[0])
	}
	storedNewRun, found, err := store.GetRun(ctx, authoritativeTask.ID, newRun.ID)
	if err != nil || !found || storedNewRun.Status != "queued" {
		t.Fatalf("new run = %+v found=%t err=%v, want queued", storedNewRun, found, err)
	}
}

func runStoreApplyRunTerminalTransitionPreservesChildrenWithoutCancelFlags(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Minute)
	finishedAt := now.Add(time.Minute)
	task := types.Task{
		ID:        "task-terminal-preserve",
		Status:    "running",
		CreatedAt: now,
		UpdatedAt: now,
		StartedAt: now,
	}
	run := types.TaskRun{
		ID:        "run-terminal-preserve",
		TaskID:    task.ID,
		Number:    1,
		Status:    "running",
		StartedAt: now,
		RequestID: "req-terminal-preserve",
		TraceID:   "trace-terminal-preserve",
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	runningStep := types.TaskStep{
		ID:        "step-running",
		TaskID:    task.ID,
		RunID:     run.ID,
		Index:     1,
		Status:    "running",
		StartedAt: now,
	}
	awaitingStep := types.TaskStep{
		ID:        "step-awaiting",
		TaskID:    task.ID,
		RunID:     run.ID,
		Index:     2,
		Status:    "awaiting_approval",
		StartedAt: now,
	}
	if _, err := store.AppendStep(ctx, runningStep); err != nil {
		t.Fatalf("AppendStep(running): %v", err)
	}
	if _, err := store.AppendStep(ctx, awaitingStep); err != nil {
		t.Fatalf("AppendStep(awaiting): %v", err)
	}
	approval := types.TaskApproval{
		ID:          "approval-preserved",
		TaskID:      task.ID,
		RunID:       run.ID,
		StepID:      awaitingStep.ID,
		Kind:        "tool_call",
		Status:      "pending",
		RequestedBy: "agent",
		CreatedAt:   now,
	}
	if _, err := store.CreateApproval(ctx, approval); err != nil {
		t.Fatalf("CreateApproval: %v", err)
	}
	artifact := types.TaskArtifact{
		ID:        "artifact-preserved",
		TaskID:    task.ID,
		RunID:     run.ID,
		Status:    "streaming",
		CreatedAt: now,
	}
	if _, err := store.CreateArtifact(ctx, artifact); err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}

	run.Status = "completed"
	run.FinishedAt = finishedAt
	run.OtelStatusCode = "ok"
	task.Status = "completed"
	task.LatestRunID = run.ID
	task.FinishedAt = finishedAt
	task.UpdatedAt = finishedAt
	result, err := store.ApplyRunTerminalTransition(ctx, TerminalRunTransition{
		Task:       task,
		Run:        run,
		FinishedAt: finishedAt,
		TerminalEvent: &RunEventSpec{
			EventType: "run.finished",
			Data:      map[string]any{"status": "completed"},
			RequestID: run.RequestID,
			TraceID:   run.TraceID,
			CreatedAt: finishedAt,
		},
	})
	if err != nil {
		t.Fatalf("ApplyRunTerminalTransition: %v", err)
	}
	if result.Run.Status != "completed" || result.Task.Status != "completed" {
		t.Fatalf("result statuses task=%q run=%q, want completed/completed", result.Task.Status, result.Run.Status)
	}
	if len(result.CancelledApprovals) != 0 {
		t.Fatalf("cancelled approvals = %+v, want none without cancel flag", result.CancelledApprovals)
	}

	storedApproval, found, err := store.GetApproval(ctx, task.ID, approval.ID)
	if err != nil || !found {
		t.Fatalf("GetApproval: found=%v err=%v", found, err)
	}
	if storedApproval.Status != "pending" || !storedApproval.ResolvedAt.IsZero() {
		t.Fatalf("stored approval = %+v, want pending/unresolved", storedApproval)
	}
	storedRunningStep, found, err := store.GetStep(ctx, run.ID, runningStep.ID)
	if err != nil || !found {
		t.Fatalf("GetStep(running): found=%v err=%v", found, err)
	}
	if storedRunningStep.Status != "running" {
		t.Fatalf("running step status = %q, want running", storedRunningStep.Status)
	}
	storedAwaitingStep, found, err := store.GetStep(ctx, run.ID, awaitingStep.ID)
	if err != nil || !found {
		t.Fatalf("GetStep(awaiting): found=%v err=%v", found, err)
	}
	if storedAwaitingStep.Status != "awaiting_approval" {
		t.Fatalf("awaiting step status = %q, want awaiting_approval", storedAwaitingStep.Status)
	}
	storedArtifact, found, err := store.GetArtifact(ctx, task.ID, artifact.ID)
	if err != nil || !found {
		t.Fatalf("GetArtifact: found=%v err=%v", found, err)
	}
	if storedArtifact.Status != "streaming" {
		t.Fatalf("artifact status = %q, want streaming", storedArtifact.Status)
	}

	events, err := store.ListRunEvents(ctx, task.ID, run.ID, 0, 10)
	if err != nil {
		t.Fatalf("ListRunEvents: %v", err)
	}
	if len(events) != 1 || events[0].EventType != "run.finished" {
		t.Fatalf("events = %+v, want only run.finished", events)
	}
	assertNoTerminalEventSnapshot(t, events[0])
	if events[0].Data["status"] != "completed" {
		t.Fatalf("run.finished data = %+v, want compact status", events[0].Data)
	}
}

func runStoreApplyRunTerminalTransitionRequiresStoredRunTaskMatch(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	task := types.Task{
		ID:        "task-terminal-mismatch",
		Status:    "running",
		CreatedAt: now,
		UpdatedAt: now,
	}
	otherTask := types.Task{
		ID:        "task-terminal-owner",
		Status:    "running",
		CreatedAt: now,
		UpdatedAt: now,
	}
	run := types.TaskRun{
		ID:        "run-terminal-mismatch",
		TaskID:    otherTask.ID,
		Number:    1,
		Status:    "running",
		StartedAt: now,
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask(task): %v", err)
	}
	if _, err := store.CreateTask(ctx, otherTask); err != nil {
		t.Fatalf("CreateTask(other): %v", err)
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	mismatchedRun := run
	mismatchedRun.TaskID = task.ID
	mismatchedRun.Status = "cancelled"
	mismatchedRun.LastError = "wrong owner"
	_, err := store.ApplyRunTerminalTransition(ctx, TerminalRunTransition{
		Task: task,
		Run:  mismatchedRun,
		TerminalEvent: &RunEventSpec{
			EventType: "run.cancelled",
			Data:      map[string]any{"reason": mismatchedRun.LastError},
		},
	})
	if err == nil {
		t.Fatal("ApplyRunTerminalTransition succeeded for a run owned by another task")
	}

	storedTask, found, err := store.GetTask(ctx, task.ID)
	if err != nil || !found {
		t.Fatalf("GetTask(task): found=%v err=%v", found, err)
	}
	if storedTask.Status != "running" || storedTask.LastError != "" {
		t.Fatalf("task after failed transition = %+v, want unchanged running task", storedTask)
	}
	storedRun, found, err := store.GetRun(ctx, otherTask.ID, run.ID)
	if err != nil || !found {
		t.Fatalf("GetRun(owner): found=%v err=%v", found, err)
	}
	if storedRun.TaskID != otherTask.ID || storedRun.Status != "running" || storedRun.LastError != "" {
		t.Fatalf("run after failed transition = %+v, want unchanged owner/running run", storedRun)
	}
	events, err := store.ListRunEvents(ctx, task.ID, run.ID, 0, 10)
	if err != nil {
		t.Fatalf("ListRunEvents(wrong task): %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events for failed transition = %+v, want none", events)
	}
}

func runStoreListRunsByFilterStatusSet(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()

	now := time.Now().UTC()
	for i, status := range []string{"queued", "running", "completed", "failed"} {
		_, err := store.CreateRun(ctx, types.TaskRun{
			ID:        "run-" + status,
			TaskID:    "task-rfilter",
			Number:    i + 1,
			Status:    status,
			StartedAt: now.Add(time.Duration(i) * time.Second),
		})
		if err != nil {
			t.Fatalf("CreateRun(%s): %v", status, err)
		}
	}

	got, err := store.ListRunsByFilter(ctx, RunFilter{
		TaskID:   "task-rfilter",
		Statuses: []string{"running", "completed"},
	})
	if err != nil {
		t.Fatalf("ListRunsByFilter: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListRunsByFilter len = %d, want 2", len(got))
	}
	for _, run := range got {
		if run.Status != "running" && run.Status != "completed" {
			t.Fatalf("unexpected status in filtered set: %q", run.Status)
		}
	}

	// Limit clamps the result.
	limited, err := store.ListRunsByFilter(ctx, RunFilter{TaskID: "task-rfilter", Limit: 2})
	if err != nil {
		t.Fatalf("ListRunsByFilter(limit): %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("ListRunsByFilter(limit) len = %d, want 2", len(limited))
	}
	firstPage, err := store.ListRunsByFilter(ctx, RunFilter{TaskID: "task-rfilter", Limit: 2, OrderByID: true})
	if err != nil {
		t.Fatalf("ListRunsByFilter(cursor first page): %v", err)
	}
	if len(firstPage) != 2 || firstPage[0].ID != "run-completed" || firstPage[1].ID != "run-failed" {
		t.Fatalf("cursor first page = %+v", firstPage)
	}
	secondPage, err := store.ListRunsByFilter(ctx, RunFilter{
		TaskID: "task-rfilter", Limit: 2, OrderByID: true, AfterID: firstPage[len(firstPage)-1].ID,
	})
	if err != nil {
		t.Fatalf("ListRunsByFilter(cursor second page): %v", err)
	}
	if len(secondPage) != 2 || secondPage[0].ID != "run-queued" || secondPage[1].ID != "run-running" {
		t.Fatalf("cursor second page = %+v", secondPage)
	}
}

func runStoreDeleteTaskCascades(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()

	if _, err := store.CreateTask(ctx, types.Task{ID: "task-del", Status: "completed"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	finishedAt := time.Now().UTC()
	if _, err := store.CreateRun(ctx, types.TaskRun{
		ID: "run-del", TaskID: "task-del", Status: "completed",
		StartedAt: finishedAt.Add(-time.Minute), FinishedAt: finishedAt,
	}); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := store.AppendStep(ctx, types.TaskStep{ID: "step-del", TaskID: "task-del", RunID: "run-del", Status: "completed"}); err != nil {
		t.Fatalf("AppendStep: %v", err)
	}

	if err := store.DeleteTask(ctx, "task-del"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	if _, ok, _ := store.GetTask(ctx, "task-del"); ok {
		t.Fatal("task still present after delete")
	}
	if _, ok, _ := store.GetRun(ctx, "task-del", "run-del"); ok {
		t.Fatal("run still present after delete")
	}
	steps, err := store.ListSteps(ctx, "run-del")
	if err != nil {
		t.Fatalf("ListSteps: %v", err)
	}
	if len(steps) != 0 {
		t.Fatalf("steps still present after delete: %d", len(steps))
	}
}

func runStoreDeleteTaskRejectsActiveRun(t *testing.T, store Store) {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC()
	const taskID = "task-delete-active"
	const projectedRunID = "run-delete-projected-terminal"
	const activeRunID = "run-delete-active-unprojected"

	if _, err := store.CreateTask(ctx, types.Task{
		ID: taskID, Status: "completed", LatestRunID: projectedRunID, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.CreateRun(ctx, types.TaskRun{
		ID: projectedRunID, TaskID: taskID, Status: "completed",
		StartedAt: now.Add(-time.Minute), FinishedAt: now,
	}); err != nil {
		t.Fatalf("CreateRun(projected): %v", err)
	}
	if _, err := store.CreateRun(ctx, types.TaskRun{
		ID: activeRunID, TaskID: taskID, Status: "running", StartedAt: now,
	}); err != nil {
		t.Fatalf("CreateRun(active unprojected): %v", err)
	}

	if err := store.DeleteTask(ctx, taskID); !errors.Is(err, ErrActiveRun) {
		t.Fatalf("DeleteTask(active) error = %v, want ErrActiveRun", err)
	}
	if _, found, err := store.GetTask(ctx, taskID); err != nil || !found {
		t.Fatalf("GetTask after rejected delete = (found %v, error %v), want present", found, err)
	}
	if _, found, err := store.GetRun(ctx, taskID, activeRunID); err != nil || !found {
		t.Fatalf("GetRun(active) after rejected delete = (found %v, error %v), want present", found, err)
	}
}

func runStoreDeleteTaskMissing(t *testing.T, store Store) {
	t.Helper()
	if err := store.DeleteTask(t.Context(), "task-delete-missing"); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("DeleteTask(missing) error = %v, want ErrTaskNotFound", err)
	}
}

func runStoreDeleteTaskConcurrentWithRunStart(t *testing.T, store Store) {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC()
	const taskID = "task-delete-start-race"
	const runID = "run-delete-start-race"

	storedTask, err := store.CreateTask(ctx, types.Task{
		ID: taskID, Status: types.TaskStatusNotStarted, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	candidateTask := storedTask
	candidateTask.Status = "queued"
	candidateTask.LatestRunID = runID
	candidateTask.UpdatedAt = now.Add(time.Second)
	candidateRun := types.TaskRun{
		ID: runID, TaskID: taskID, Status: "queued", StartedAt: now.Add(time.Second),
	}

	start := make(chan struct{})
	var deleteErr, startErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		deleteErr = store.DeleteTask(ctx, taskID)
	}()
	go func() {
		defer wg.Done()
		<-start
		_, startErr = store.ApplyRunStartTransition(ctx, RunStartTransition{
			Task: candidateTask, Run: candidateRun,
		})
	}()
	close(start)
	wg.Wait()

	switch {
	case deleteErr == nil:
		if !errors.Is(startErr, ErrTaskNotFound) {
			t.Fatalf("delete won but Run start error = %v, want ErrTaskNotFound", startErr)
		}
		if _, found, err := store.GetTask(ctx, taskID); err != nil || found {
			t.Fatalf("Task after delete winner = (found %v, error %v), want absent", found, err)
		}
		if _, found, err := store.GetRun(ctx, taskID, runID); err != nil || found {
			t.Fatalf("Run after delete winner = (found %v, error %v), want absent", found, err)
		}
	case startErr == nil:
		if !errors.Is(deleteErr, ErrActiveRun) {
			t.Fatalf("Run start won but delete error = %v, want ErrActiveRun", deleteErr)
		}
		if _, found, err := store.GetTask(ctx, taskID); err != nil || !found {
			t.Fatalf("Task after Run-start winner = (found %v, error %v), want present", found, err)
		}
		if _, found, err := store.GetRun(ctx, taskID, runID); err != nil || !found {
			t.Fatalf("Run after Run-start winner = (found %v, error %v), want present", found, err)
		}
	default:
		t.Fatalf("delete/start race = (%v, %v), want exactly one winner", deleteErr, startErr)
	}
}

// runStoreTaskMCPServersRoundTrip pins that a Task with a
// fully-populated MCPServers slice survives a round-trip through
// the backend's storage path. The pkg/types/task JSON-round-trip
// test pins the marshaling contract on the type itself; this test
// pins the actual storage layer (write, read, compare).
//
// Catches: a regression where someone changes a Task field tag,
// adds an unmarshal hook that mishandles a default value, or
// makes a column type incompatible with the existing JSON shape
// would silently corrupt every persisted MCP config; this test
// fails first.
func runStoreTaskMCPServersRoundTrip(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()

	task := types.Task{
		ID:            "task-mcp",
		Title:         "MCP store round-trip",
		Status:        "queued",
		ExecutionKind: "agent_loop",
		MCPServers: []types.MCPServerConfig{
			{
				Name:    "fs",
				Command: "bunx",
				Args:    []string{"--bun", "@modelcontextprotocol/server-filesystem", "/workspace"},
				Env: map[string]string{
					"DEBUG_TOKEN": "$DEBUG_TOKEN",
					"AUTH":        "enc:abc123base64=",
					"NODE_ENV":    "production",
				},
				ApprovalPolicy: types.MCPApprovalAuto,
			},
			{
				Name: "github",
				URL:  "https://api.example.com/mcp",
				Headers: map[string]string{
					"Authorization": "Bearer $GITHUB_TOKEN",
					"X-Trace":       "on",
				},
				ApprovalPolicy: types.MCPApprovalRequireApproval,
			},
			{
				Name:           "blocked",
				Command:        "npx",
				Args:           []string{"@vendor/dangerous"},
				ApprovalPolicy: types.MCPApprovalBlock,
			},
		},
	}

	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	got, ok, err := store.GetTask(ctx, task.ID)
	if err != nil || !ok {
		t.Fatalf("GetTask: ok=%v err=%v", ok, err)
	}

	// Field-by-field check on the MCP slice rather than reflect.DeepEqual
	// on the whole Task: the store stamps CreatedAt / UpdatedAt /
	// other timestamps, so a whole-Task DeepEqual would fail on
	// fields the test doesn't care about.
	if len(got.MCPServers) != len(task.MCPServers) {
		t.Fatalf("MCPServers count: got %d, want %d", len(got.MCPServers), len(task.MCPServers))
	}
	for i, want := range task.MCPServers {
		gotEntry := got.MCPServers[i]
		if gotEntry.Name != want.Name {
			t.Errorf("[%d] Name = %q, want %q", i, gotEntry.Name, want.Name)
		}
		if gotEntry.Command != want.Command {
			t.Errorf("[%d] Command = %q, want %q", i, gotEntry.Command, want.Command)
		}
		if gotEntry.URL != want.URL {
			t.Errorf("[%d] URL = %q, want %q", i, gotEntry.URL, want.URL)
		}
		if gotEntry.ApprovalPolicy != want.ApprovalPolicy {
			t.Errorf("[%d] ApprovalPolicy = %q, want %q", i, gotEntry.ApprovalPolicy, want.ApprovalPolicy)
		}
		if !equalStringSlice(gotEntry.Args, want.Args) {
			t.Errorf("[%d] Args = %+v, want %+v", i, gotEntry.Args, want.Args)
		}
		if !equalStringMap(gotEntry.Env, want.Env) {
			t.Errorf("[%d] Env = %+v, want %+v", i, gotEntry.Env, want.Env)
		}
		if !equalStringMap(gotEntry.Headers, want.Headers) {
			t.Errorf("[%d] Headers = %+v, want %+v", i, gotEntry.Headers, want.Headers)
		}
	}
}
