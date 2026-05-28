package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/storage"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

type approvalLifecycleStoreCase struct {
	name  string
	store taskstate.Store
}

func approvalLifecycleStores(t *testing.T) []approvalLifecycleStoreCase {
	t.Helper()
	ctx := context.Background()
	client, err := storage.NewSQLiteClient(ctx, storage.SQLiteConfig{
		Path:        filepath.Join(t.TempDir(), "approval-lifecycle.db"),
		TablePrefix: "approval_lifecycle",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient() error = %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
	})
	sqliteStore, err := taskstate.NewSQLiteStore(ctx, client)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}

	return []approvalLifecycleStoreCase{
		{name: "memory", store: taskstate.NewMemoryStore()},
		{name: "sqlite", store: sqliteStore},
	}
}

func newApprovalLifecycleRunner(store taskstate.Store) (*Runner, *recordingQueue) {
	queue := &recordingQueue{}
	return &Runner{
		logger:   slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store:    store,
		tracer:   profiler.NewInMemoryTracer(nil),
		queue:    queue,
		policies: make(map[string]struct{}),
		jobs:     make(map[string]context.CancelFunc),
	}, queue
}

func seedAwaitingApprovalRun(t *testing.T, ctx context.Context, store taskstate.Store, suffix string) (types.Task, types.TaskRun, types.TaskStep, types.TaskApproval) {
	t.Helper()
	now := time.Now().UTC().Add(-time.Minute)
	task := types.Task{
		ID:          "task_" + suffix,
		Title:       "Approval lifecycle",
		Status:      "awaiting_approval",
		LatestRunID: "run_" + suffix,
		CreatedAt:   now,
		UpdatedAt:   now,
		StartedAt:   now,
	}
	run := types.TaskRun{
		ID:        "run_" + suffix,
		TaskID:    task.ID,
		Number:    1,
		Status:    "awaiting_approval",
		StartedAt: now,
		RequestID: "request_" + suffix,
		TraceID:   "trace_" + suffix,
	}
	step := types.TaskStep{
		ID:         "step_" + suffix,
		TaskID:     task.ID,
		RunID:      run.ID,
		Index:      1,
		Kind:       "approval",
		Title:      "Awaiting approval",
		Status:     "awaiting_approval",
		Phase:      "approval",
		ApprovalID: "approval_" + suffix,
		StartedAt:  now,
		RequestID:  run.RequestID,
		TraceID:    run.TraceID,
	}
	approval := types.TaskApproval{
		ID:          "approval_" + suffix,
		TaskID:      task.ID,
		RunID:       run.ID,
		StepID:      step.ID,
		Kind:        "shell_command",
		Status:      "pending",
		Reason:      "Shell execution requires approval.",
		RequestedBy: "operator",
		CreatedAt:   now,
		RequestID:   run.RequestID,
		TraceID:     run.TraceID,
	}

	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	if _, err := store.AppendStep(ctx, step); err != nil {
		t.Fatalf("AppendStep() error = %v", err)
	}
	if _, err := store.CreateApproval(ctx, approval); err != nil {
		t.Fatalf("CreateApproval() error = %v", err)
	}
	return task, run, step, approval
}

func deterministicApprovalID(prefix string) string {
	return prefix + "_approval_lifecycle"
}

func TestRunnerResolveTaskApproval_ApprovesAndRequeuesSameRun(t *testing.T) {
	for _, tc := range approvalLifecycleStores(t) {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			task, run, _, approval := seedAwaitingApprovalRun(t, ctx, tc.store, tc.name+"_approve")
			runner, queue := newApprovalLifecycleRunner(tc.store)

			result, err := runner.ResolveTaskApproval(ctx, ResolveApprovalRequest{
				Task:        task,
				ApprovalID:  approval.ID,
				Decision:    "approve",
				Note:        "ship it",
				ResolvedBy:  "operator",
				IDGenerator: deterministicApprovalID,
			})
			if err != nil {
				t.Fatalf("ResolveTaskApproval() error = %v", err)
			}

			if result.Run.ID != run.ID {
				t.Fatalf("resolved run id = %q, want same run %q", result.Run.ID, run.ID)
			}
			if result.Run.Status != "queued" {
				t.Fatalf("resolved run status = %q, want queued", result.Run.Status)
			}
			if result.Task.Status != "queued" {
				t.Fatalf("resolved task status = %q, want queued", result.Task.Status)
			}
			if result.Approval.Status != "approved" {
				t.Fatalf("approval status = %q, want approved", result.Approval.Status)
			}
			if result.Approval.ResolvedBy != "operator" || result.Approval.ResolutionNote != "ship it" || result.Approval.ResolvedAt.IsZero() {
				t.Fatalf("approval resolution fields = by %q note %q at %v", result.Approval.ResolvedBy, result.Approval.ResolutionNote, result.Approval.ResolvedAt)
			}
			if len(queue.enqueued) != 1 || queue.enqueued[0].TaskID != task.ID || queue.enqueued[0].RunID != run.ID {
				t.Fatalf("enqueued jobs = %+v, want same task/run requeued once", queue.enqueued)
			}

			storedApproval, found, err := tc.store.GetApproval(ctx, task.ID, approval.ID)
			if err != nil || !found {
				t.Fatalf("GetApproval() found=%v err=%v", found, err)
			}
			if storedApproval.Status != "approved" {
				t.Fatalf("stored approval status = %q, want approved", storedApproval.Status)
			}
			storedRun, found, err := tc.store.GetRun(ctx, task.ID, run.ID)
			if err != nil || !found {
				t.Fatalf("GetRun() found=%v err=%v", found, err)
			}
			if storedRun.Status != "queued" {
				t.Fatalf("stored run status = %q, want queued", storedRun.Status)
			}
			storedTask, found, err := tc.store.GetTask(ctx, task.ID)
			if err != nil || !found {
				t.Fatalf("GetTask() found=%v err=%v", found, err)
			}
			if storedTask.Status != "queued" || storedTask.LatestRunID != run.ID || storedTask.LastError != "" {
				t.Fatalf("stored task = %+v, want queued latest run with no error", storedTask)
			}

			events := listApprovalLifecycleEvents(t, ctx, tc.store, task.ID, run.ID)
			assertApprovalEvent(t, events, "approved")
			assertApprovalEventRunStatus(t, events, "approved", "queued")
			assertEventType(t, events, "run.queued")
		})
	}
}

func TestRunnerResolveTaskApproval_RejectCancelsRunTaskAndStep(t *testing.T) {
	for _, tc := range approvalLifecycleStores(t) {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			task, run, step, approval := seedAwaitingApprovalRun(t, ctx, tc.store, tc.name+"_reject")
			runner, queue := newApprovalLifecycleRunner(tc.store)

			result, err := runner.ResolveTaskApproval(ctx, ResolveApprovalRequest{
				Task:        task,
				ApprovalID:  approval.ID,
				Decision:    "reject",
				Note:        "not safe",
				ResolvedBy:  "operator",
				IDGenerator: deterministicApprovalID,
			})
			if err != nil {
				t.Fatalf("ResolveTaskApproval() error = %v", err)
			}

			if len(queue.enqueued) != 0 {
				t.Fatalf("enqueued jobs = %+v, want none after rejection", queue.enqueued)
			}
			if result.Approval.Status != "rejected" {
				t.Fatalf("approval status = %q, want rejected", result.Approval.Status)
			}
			if result.Run.Status != "cancelled" || result.Run.LastError != "approval rejected" {
				t.Fatalf("result run = %+v, want cancelled with approval rejected", result.Run)
			}
			if result.Task.Status != "cancelled" || result.Task.LastError != "approval rejected" {
				t.Fatalf("result task = %+v, want cancelled with approval rejected", result.Task)
			}

			storedStep, found, err := tc.store.GetStep(ctx, run.ID, step.ID)
			if err != nil || !found {
				t.Fatalf("GetStep() found=%v err=%v", found, err)
			}
			if storedStep.Status != "cancelled" || storedStep.Error != "approval rejected" || storedStep.ErrorKind != "run_cancelled" {
				t.Fatalf("stored step = %+v, want cancelled approval step", storedStep)
			}

			events := listApprovalLifecycleEvents(t, ctx, tc.store, task.ID, run.ID)
			assertApprovalEvent(t, events, "rejected")
			assertApprovalEventRunStatus(t, events, "rejected", "cancelled")
			assertApprovalEventStepStatus(t, events, "rejected", step.ID, "cancelled")
			assertEventType(t, events, "run.cancelled")
			assertEventType(t, events, "task.updated")
		})
	}
}

func TestRunnerCancelRun_CancelsPendingApprovalAndAwaitingStep(t *testing.T) {
	for _, tc := range approvalLifecycleStores(t) {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			task, run, step, approval := seedAwaitingApprovalRun(t, ctx, tc.store, tc.name+"_cancel")
			runner, _ := newApprovalLifecycleRunner(tc.store)

			cancelled, err := runner.CancelRun(ctx, task, run.ID, "operator changed mind")
			if err != nil {
				t.Fatalf("CancelRun() error = %v", err)
			}
			if cancelled.Status != "cancelled" || cancelled.LastError != "run cancelled: operator changed mind" {
				t.Fatalf("cancelled run = %+v, want cancelled with operator reason", cancelled)
			}

			storedApproval, found, err := tc.store.GetApproval(ctx, task.ID, approval.ID)
			if err != nil || !found {
				t.Fatalf("GetApproval() found=%v err=%v", found, err)
			}
			if storedApproval.Status != "cancelled" || storedApproval.ResolvedBy != "system" || storedApproval.ResolutionNote != cancelled.LastError {
				t.Fatalf("stored approval = %+v, want system-cancelled approval", storedApproval)
			}
			storedStep, found, err := tc.store.GetStep(ctx, run.ID, step.ID)
			if err != nil || !found {
				t.Fatalf("GetStep() found=%v err=%v", found, err)
			}
			if storedStep.Status != "cancelled" || storedStep.Error != cancelled.LastError || storedStep.ErrorKind != "run_cancelled" {
				t.Fatalf("stored step = %+v, want cancelled awaiting approval step", storedStep)
			}
			storedTask, found, err := tc.store.GetTask(ctx, task.ID)
			if err != nil || !found {
				t.Fatalf("GetTask() found=%v err=%v", found, err)
			}
			if storedTask.Status != "cancelled" || storedTask.LastError != cancelled.LastError {
				t.Fatalf("stored task = %+v, want cancelled task with same reason", storedTask)
			}

			events := listApprovalLifecycleEvents(t, ctx, tc.store, task.ID, run.ID)
			assertEventType(t, events, "run.cancelled")
			assertApprovalEvent(t, events, "cancelled")
			assertApprovalEventRunStatus(t, events, "cancelled", "cancelled")
			assertApprovalEventStepStatus(t, events, "cancelled", step.ID, "cancelled")
			assertEventType(t, events, "task.updated")
		})
	}
}

func TestRunnerResolveTaskApproval_ConflictsWhenAlreadyResolved(t *testing.T) {
	for _, tc := range approvalLifecycleStores(t) {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			task, _, _, approval := seedAwaitingApprovalRun(t, ctx, tc.store, tc.name+"_already_resolved")
			runner, _ := newApprovalLifecycleRunner(tc.store)
			approval.Status = "approved"
			approval.ResolvedBy = "operator"
			approval.ResolvedAt = time.Now().UTC()
			if _, err := tc.store.UpdateApproval(ctx, approval); err != nil {
				t.Fatalf("UpdateApproval() error = %v", err)
			}

			_, err := runner.ResolveTaskApproval(ctx, ResolveApprovalRequest{
				Task:        task,
				ApprovalID:  approval.ID,
				Decision:    "approve",
				IDGenerator: deterministicApprovalID,
			})
			if !errors.Is(err, ErrApprovalConflict) {
				t.Fatalf("ResolveTaskApproval() error = %v, want ErrApprovalConflict", err)
			}
		})
	}
}

func TestRunnerResolveTaskApproval_ConflictsAfterRunCancellation(t *testing.T) {
	for _, tc := range approvalLifecycleStores(t) {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			task, run, _, approval := seedAwaitingApprovalRun(t, ctx, tc.store, tc.name+"_cancel_then_resolve")
			runner, _ := newApprovalLifecycleRunner(tc.store)
			if _, err := runner.CancelRun(ctx, task, run.ID, "operator cancelled"); err != nil {
				t.Fatalf("CancelRun() error = %v", err)
			}

			_, err := runner.ResolveTaskApproval(ctx, ResolveApprovalRequest{
				Task:        task,
				ApprovalID:  approval.ID,
				Decision:    "approve",
				IDGenerator: deterministicApprovalID,
			})
			if !errors.Is(err, ErrApprovalConflict) {
				t.Fatalf("ResolveTaskApproval() error = %v, want ErrApprovalConflict", err)
			}
		})
	}
}

func listApprovalLifecycleEvents(t *testing.T, ctx context.Context, store taskstate.Store, taskID, runID string) []types.TaskRunEvent {
	t.Helper()
	events, err := store.ListRunEvents(ctx, taskID, runID, 0, 100)
	if err != nil {
		t.Fatalf("ListRunEvents() error = %v", err)
	}
	return events
}

func assertEventType(t *testing.T, events []types.TaskRunEvent, eventType string) {
	t.Helper()
	for _, event := range events {
		if event.EventType == eventType {
			return
		}
	}
	t.Fatalf("missing event %q in %+v", eventType, eventTypes(events))
}

func assertApprovalEvent(t *testing.T, events []types.TaskRunEvent, decision string) {
	t.Helper()
	if approvalEvent(t, events, decision) != nil {
		return
	}
	t.Fatalf("missing approval.resolved decision %q in events %+v", decision, events)
}

func assertApprovalEventRunStatus(t *testing.T, events []types.TaskRunEvent, decision, status string) {
	t.Helper()
	event := approvalEvent(t, events, decision)
	if event == nil {
		t.Fatalf("missing approval.resolved decision %q in events %+v", decision, events)
	}
	var run types.TaskRun
	decodeEventField(t, (*event).Data["run"], &run)
	if run.Status != status {
		t.Fatalf("approval.resolved %q run status = %q, want %q", decision, run.Status, status)
	}
}

func assertApprovalEventStepStatus(t *testing.T, events []types.TaskRunEvent, decision, stepID, status string) {
	t.Helper()
	event := approvalEvent(t, events, decision)
	if event == nil {
		t.Fatalf("missing approval.resolved decision %q in events %+v", decision, events)
	}
	var steps []types.TaskStep
	decodeEventField(t, (*event).Data["steps"], &steps)
	for _, step := range steps {
		if step.ID == stepID {
			if step.Status != status {
				t.Fatalf("approval.resolved %q step %q status = %q, want %q", decision, stepID, step.Status, status)
			}
			return
		}
	}
	t.Fatalf("approval.resolved %q missing step %q in %+v", decision, stepID, steps)
}

func approvalEvent(t *testing.T, events []types.TaskRunEvent, decision string) *types.TaskRunEvent {
	t.Helper()
	for _, event := range events {
		if event.EventType != "approval.resolved" {
			continue
		}
		if event.Data["decision"] == decision && event.Data["status"] == decision {
			return &event
		}
	}
	return nil
}

func decodeEventField(t *testing.T, value any, out any) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal event field: %v", err)
	}
	if err := json.Unmarshal(payload, out); err != nil {
		t.Fatalf("unmarshal event field: %v", err)
	}
}

func eventTypes(events []types.TaskRunEvent) []string {
	types := make([]string, 0, len(events))
	for _, event := range events {
		types = append(types, event.EventType)
	}
	return types
}
