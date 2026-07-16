package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/runtimeevents"
	"github.com/hecatehq/hecate/internal/storage"
	"github.com/hecatehq/hecate/internal/taskruncoord"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestApprovalWaitMilliseconds(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	tests := []struct {
		name       string
		createdAt  time.Time
		resolvedAt time.Time
		want       int64
	}{
		{name: "missing creation", resolvedAt: now, want: 0},
		{name: "missing resolution", createdAt: now, want: 0},
		{name: "resolution before creation", createdAt: now, resolvedAt: now.Add(-time.Second), want: 0},
		{name: "equal timestamps", createdAt: now, resolvedAt: now, want: 0},
		{name: "positive duration", createdAt: now, resolvedAt: now.Add(1500 * time.Millisecond), want: 1500},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := approvalWaitMilliseconds(tt.createdAt, tt.resolvedAt); got != tt.want {
				t.Fatalf("approvalWaitMilliseconds() = %d, want %d", got, tt.want)
			}
		})
	}
}

type approvalLifecycleStoreCase struct {
	name  string
	store taskstate.Store
}

type blockingApprovalResolutionStore struct {
	taskstate.Store
	decision string
	started  chan struct{}
	release  chan struct{}
	mu       sync.Mutex
	blocked  bool
}

func (s *blockingApprovalResolutionStore) ApplyRunStateTransition(ctx context.Context, transition taskstate.RunStateTransition) (taskstate.RunStateTransitionResult, error) {
	if transition.ApprovalResolution != nil && transition.ApprovalResolution.Status == s.decision {
		if err := s.block(ctx); err != nil {
			return taskstate.RunStateTransitionResult{}, err
		}
	}
	return s.Store.ApplyRunStateTransition(ctx, transition)
}

func (s *blockingApprovalResolutionStore) ApplyRunTerminalTransition(ctx context.Context, transition taskstate.TerminalRunTransition) (taskstate.TerminalRunTransitionResult, error) {
	if transition.ApprovalResolution != nil && transition.ApprovalResolution.Status == s.decision {
		if err := s.block(ctx); err != nil {
			return taskstate.TerminalRunTransitionResult{}, err
		}
	}
	return s.Store.ApplyRunTerminalTransition(ctx, transition)
}

func (s *blockingApprovalResolutionStore) block(ctx context.Context) error {
	s.mu.Lock()
	shouldBlock := !s.blocked
	if shouldBlock {
		s.blocked = true
		close(s.started)
	}
	s.mu.Unlock()
	if !shouldBlock {
		return nil
	}
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type blockingApprovalCancellationStore struct {
	taskstate.Store
	started chan struct{}
	release chan struct{}
	mu      sync.Mutex
	blocked bool
}

func (s *blockingApprovalCancellationStore) ApplyRunTerminalTransition(ctx context.Context, transition taskstate.TerminalRunTransition) (taskstate.TerminalRunTransitionResult, error) {
	shouldBlock := false
	if transition.ApprovalResolution == nil && transition.Run.Status == "cancelled" {
		s.mu.Lock()
		shouldBlock = !s.blocked
		if shouldBlock {
			s.blocked = true
			close(s.started)
		}
		s.mu.Unlock()
	}
	if shouldBlock {
		select {
		case <-s.release:
		case <-ctx.Done():
			return taskstate.TerminalRunTransitionResult{}, ctx.Err()
		}
	}
	return s.Store.ApplyRunTerminalTransition(ctx, transition)
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

func TestRunnerResolveTaskApproval_CancellationWinsAtomicRace(t *testing.T) {
	for _, decision := range []string{"approve", "reject"} {
		for _, tc := range approvalLifecycleStores(t) {
			t.Run(decision+"/"+tc.name, func(t *testing.T) {
				ctx := t.Context()
				suffix := "cancel_wins_" + decision + "_" + tc.name
				task, run, _, approval := seedAwaitingApprovalRun(t, ctx, tc.store, suffix)
				resolvedStatus := normalizedApprovalStatus(decision)
				blockingStore := &blockingApprovalResolutionStore{
					Store: tc.store, decision: resolvedStatus,
					started: make(chan struct{}), release: make(chan struct{}),
				}
				runner, queue := newApprovalLifecycleRunner(blockingStore)
				metrics, metricReader := newMetricsForTest(t)
				runner.SetMetrics(metrics)
				requestID := "request_resolve_loser_" + decision + "_" + tc.name
				resolveDone := make(chan error, 1)
				go func() {
					_, err := runner.ResolveTaskApproval(ctx, ResolveApprovalRequest{
						Task: task, ApprovalID: approval.ID, Decision: decision,
						Note: "operator decision", ResolvedBy: "operator",
						RequestID: requestID, IDGenerator: deterministicApprovalID,
					})
					resolveDone <- err
				}()
				waitForApprovalRaceGate(t, blockingStore.started, "approval resolution")

				cancelRequestID := "request_cancel_winner_" + decision + "_" + tc.name
				cancelCtx := telemetry.WithRequestID(ctx, cancelRequestID)
				if _, err := runner.CancelRun(cancelCtx, task, run.ID, "operator stop"); err != nil {
					t.Fatalf("CancelRun() error = %v", err)
				}
				close(blockingStore.release)
				if err := waitForApprovalRaceResult(t, resolveDone, "approval resolution"); !errors.Is(err, ErrApprovalConflict) {
					t.Fatalf("ResolveTaskApproval() error = %v, want ErrApprovalConflict", err)
				}

				storedApproval := requireApprovalLifecycleApproval(t, ctx, tc.store, task.ID, approval.ID)
				if storedApproval.Status != "cancelled" || storedApproval.ResolvedBy != "system" {
					t.Fatalf("stored approval = %+v, want cancellation winner", storedApproval)
				}
				storedRun := requireApprovalLifecycleRun(t, ctx, tc.store, task.ID, run.ID)
				storedTask := requireApprovalLifecycleTask(t, ctx, tc.store, task.ID)
				if storedRun.Status != "cancelled" || storedTask.Status != "cancelled" {
					t.Fatalf("stored task/run statuses = %q/%q, want cancelled/cancelled", storedTask.Status, storedRun.Status)
				}
				if len(queue.enqueued) != 0 {
					t.Fatalf("queued jobs = %+v, want none for losing resolution", queue.enqueued)
				}

				events := listApprovalLifecycleEvents(t, ctx, tc.store, task.ID, run.ID)
				assertEventCount(t, events, runtimeevents.EventRunCancelled.String(), 1)
				assertEventCount(t, events, runtimeevents.EventTaskUpdated.String(), 1)
				assertEventCount(t, events, runtimeevents.EventRunQueued.String(), 0)
				assertApprovalEventCount(t, events, approval.ID, "cancelled", 1)
				assertApprovalEventCount(t, events, approval.ID, resolvedStatus, 0)
				assertRequestEventCount(t, events, requestID, 0)
				assertEventOrder(t, events, []string{
					runtimeevents.EventApprovalResolved.String(),
					runtimeevents.EventRunCancelled.String(),
					runtimeevents.EventTaskUpdated.String(),
				})
				assertApprovalTelemetryCount(t, runner, requestID, 0)
				assertApprovalTelemetryCount(t, runner, cancelRequestID, 1)
				assertApprovalMetricDecisions(t, metricReader, map[string]int64{"cancelled": 1})
			})
		}
	}
}

func TestRunnerResolveTaskApproval_ResolutionCommitsBeforeCancellation(t *testing.T) {
	for _, decision := range []string{"approve", "reject"} {
		for _, tc := range approvalLifecycleStores(t) {
			t.Run(decision+"/"+tc.name, func(t *testing.T) {
				ctx := t.Context()
				suffix := "resolve_first_" + decision + "_" + tc.name
				task, run, _, approval := seedAwaitingApprovalRun(t, ctx, tc.store, suffix)
				blockingStore := &blockingApprovalCancellationStore{
					Store: tc.store, started: make(chan struct{}), release: make(chan struct{}),
				}
				runner, queue := newApprovalLifecycleRunner(blockingStore)
				metrics, metricReader := newMetricsForTest(t)
				runner.SetMetrics(metrics)
				cancelDone := make(chan error, 1)
				go func() {
					cancelCtx := telemetry.WithRequestID(ctx, "request_cancel_later_"+decision+"_"+tc.name)
					_, err := runner.CancelRun(cancelCtx, task, run.ID, "operator stop")
					cancelDone <- err
				}()
				waitForApprovalRaceGate(t, blockingStore.started, "run cancellation")

				requestID := "request_resolve_winner_" + decision + "_" + tc.name
				result, err := runner.ResolveTaskApproval(ctx, ResolveApprovalRequest{
					Task: task, ApprovalID: approval.ID, Decision: decision,
					Note: "operator decision", ResolvedBy: "operator",
					RequestID: requestID, IDGenerator: deterministicApprovalID,
				})
				if err != nil {
					close(blockingStore.release)
					_ = waitForApprovalRaceResult(t, cancelDone, "run cancellation")
					t.Fatalf("ResolveTaskApproval() error = %v", err)
				}
				resolvedStatus := normalizedApprovalStatus(decision)
				if result.Approval.Status != resolvedStatus {
					t.Fatalf("resolved approval status = %q, want %q", result.Approval.Status, resolvedStatus)
				}
				if decision == "approve" && (result.Run.Status != "queued" || result.Task.Status != "queued") {
					t.Fatalf("approve result task/run statuses = %q/%q, want queued/queued", result.Task.Status, result.Run.Status)
				}
				if decision == "reject" && (result.Run.Status != "cancelled" || result.Task.Status != "cancelled") {
					t.Fatalf("reject result task/run statuses = %q/%q, want cancelled/cancelled", result.Task.Status, result.Run.Status)
				}

				close(blockingStore.release)
				if err := waitForApprovalRaceResult(t, cancelDone, "run cancellation"); err != nil {
					t.Fatalf("CancelRun() error = %v", err)
				}

				storedApproval := requireApprovalLifecycleApproval(t, ctx, tc.store, task.ID, approval.ID)
				storedRun := requireApprovalLifecycleRun(t, ctx, tc.store, task.ID, run.ID)
				storedTask := requireApprovalLifecycleTask(t, ctx, tc.store, task.ID)
				if storedApproval.Status != resolvedStatus || storedApproval.ResolvedBy != "operator" || storedApproval.ResolutionNote != "operator decision" {
					t.Fatalf("stored approval = %+v, want durable %s operator decision", storedApproval, resolvedStatus)
				}
				if storedRun.Status != "cancelled" || storedTask.Status != "cancelled" {
					t.Fatalf("final task/run statuses = %q/%q, want later cancellation", storedTask.Status, storedRun.Status)
				}
				wantQueued := 0
				wantQueueJobs := 0
				if decision == "approve" {
					wantQueued = 1
					wantQueueJobs = 1
				}
				if len(queue.enqueued) != wantQueueJobs {
					t.Fatalf("queued jobs = %+v, want %d", queue.enqueued, wantQueueJobs)
				}

				events := listApprovalLifecycleEvents(t, ctx, tc.store, task.ID, run.ID)
				assertApprovalEventCount(t, events, approval.ID, resolvedStatus, 1)
				assertEventCount(t, events, runtimeevents.EventRunQueued.String(), wantQueued)
				assertEventCount(t, events, runtimeevents.EventRunCancelled.String(), 1)
				assertEventCount(t, events, runtimeevents.EventTaskUpdated.String(), 1)
				assertApprovalEventRunStatus(t, events, resolvedStatus, map[bool]string{true: "queued", false: "cancelled"}[decision == "approve"])
				wantEventOrder := []string{
					runtimeevents.EventApprovalResolved.String(),
					runtimeevents.EventRunQueued.String(),
					runtimeevents.EventRunCancelled.String(),
					runtimeevents.EventTaskUpdated.String(),
				}
				if decision == "reject" {
					wantEventOrder = []string{
						runtimeevents.EventRunCancelled.String(),
						runtimeevents.EventTaskUpdated.String(),
						runtimeevents.EventApprovalResolved.String(),
					}
				}
				assertEventOrder(t, events, wantEventOrder)
				assertApprovalTelemetryCount(t, runner, requestID, 1)
				assertApprovalMetricDecisions(t, metricReader, map[string]int64{resolvedStatus: 1})
			})
		}
	}
}

func newApprovalLifecycleRunner(store taskstate.Store) (*Runner, *recordingQueue) {
	queue := &recordingQueue{}
	runner := &Runner{
		logger:   slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store:    store,
		tracer:   profiler.NewInMemoryTracer(nil),
		policies: make(map[string]struct{}),
	}
	attachTestQueueCoordinator(runner, queue)
	return runner, queue
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

func normalizedApprovalStatus(decision string) string {
	if decision == "approve" {
		return "approved"
	}
	return "rejected"
}

func waitForApprovalRaceGate(t *testing.T, started <-chan struct{}, operation string) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not reach the atomic store transition", operation)
	}
}

func waitForApprovalRaceResult(t *testing.T, done <-chan error, operation string) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not return", operation)
		return nil
	}
}

func requireApprovalLifecycleApproval(t *testing.T, ctx context.Context, store taskstate.Store, taskID, approvalID string) types.TaskApproval {
	t.Helper()
	approval, found, err := store.GetApproval(ctx, taskID, approvalID)
	if err != nil || !found {
		t.Fatalf("GetApproval() found=%v err=%v", found, err)
	}
	return approval
}

func requireApprovalLifecycleRun(t *testing.T, ctx context.Context, store taskstate.Store, taskID, runID string) types.TaskRun {
	t.Helper()
	run, found, err := store.GetRun(ctx, taskID, runID)
	if err != nil || !found {
		t.Fatalf("GetRun() found=%v err=%v", found, err)
	}
	return run
}

func requireApprovalLifecycleTask(t *testing.T, ctx context.Context, store taskstate.Store, taskID string) types.Task {
	t.Helper()
	task, found, err := store.GetTask(ctx, taskID)
	if err != nil || !found {
		t.Fatalf("GetTask() found=%v err=%v", found, err)
	}
	return task
}

func TestRunnerResolveTaskApproval_RejectsClosedTaskOrigin(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	task, run, _, approval := seedAwaitingApprovalRun(t, ctx, store, "closed_origin")
	task.OriginKind = "chat"
	task.OriginID = "chat_deleted"
	if _, err := store.UpdateTask(ctx, task); err != nil {
		t.Fatalf("UpdateTask(origin) error = %v", err)
	}
	runner, _ := newApprovalLifecycleRunner(store)
	gate := taskruncoord.NewOriginGate()
	runner.SetOriginRunGate(gate)
	closure, err := gate.Close(ctx, taskruncoord.Origin{Kind: task.OriginKind, ID: task.OriginID})
	if err != nil {
		t.Fatalf("Close(origin) error = %v", err)
	}
	defer closure.Release()

	_, err = runner.ResolveTaskApproval(ctx, ResolveApprovalRequest{
		Task:        task,
		ApprovalID:  approval.ID,
		Decision:    "approve",
		IDGenerator: deterministicApprovalID,
	})
	if !errors.Is(err, taskruncoord.ErrOriginRunAdmissionClosed) {
		t.Fatalf("ResolveTaskApproval() error = %v, want origin admission closed", err)
	}
	storedRun, found, getErr := store.GetRun(ctx, task.ID, run.ID)
	if getErr != nil || !found || storedRun.Status != "awaiting_approval" {
		t.Fatalf("run after rejected approval = %+v found=%v err=%v", storedRun, found, getErr)
	}
	storedApproval, found, getErr := store.GetApproval(ctx, task.ID, approval.ID)
	if getErr != nil || !found || storedApproval.Status != "pending" {
		t.Fatalf("approval after rejected resolution = %+v found=%v err=%v", storedApproval, found, getErr)
	}
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
			assertApprovalEventNoSnapshot(t, events, "cancelled")
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

func TestRunnerApprovalHelpers_ConflictWhenRunNoLongerAwaiting(t *testing.T) {
	for _, tc := range approvalLifecycleStores(t) {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			task, run, _, approval := seedAwaitingApprovalRun(t, ctx, tc.store, tc.name+"_helper_conflict")
			runner, _ := newApprovalLifecycleRunner(tc.store)

			run.Status = "cancelled"
			run.LastError = "operator cancelled"
			if _, err := tc.store.UpdateRun(ctx, run); err != nil {
				t.Fatalf("UpdateRun() error = %v", err)
			}

			approval.Status = "approved"
			_, err := runner.ResumeTaskAfterApproval(ctx, task, approval, deterministicApprovalID)
			if !errors.Is(err, ErrApprovalConflict) {
				t.Fatalf("ResumeTaskAfterApproval() error = %v, want ErrApprovalConflict", err)
			}

			approval.Status = "rejected"
			_, err = runner.RejectTaskAfterApproval(ctx, task, approval, deterministicApprovalID)
			if !errors.Is(err, ErrApprovalConflict) {
				t.Fatalf("RejectTaskAfterApproval() error = %v, want ErrApprovalConflict", err)
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

func assertEventCount(t *testing.T, events []types.TaskRunEvent, eventType string, want int) {
	t.Helper()
	got := 0
	for _, event := range events {
		if event.EventType == eventType {
			got++
		}
	}
	if got != want {
		t.Fatalf("%s event count = %d, want %d; events = %+v", eventType, got, want, eventTypes(events))
	}
}

func assertEventOrder(t *testing.T, events []types.TaskRunEvent, want []string) {
	t.Helper()
	got := eventTypes(events)
	if len(got) != len(want) {
		t.Fatalf("event order = %+v, want %+v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("event order = %+v, want %+v", got, want)
		}
	}
}

func assertApprovalEventCount(t *testing.T, events []types.TaskRunEvent, approvalID, decision string, want int) {
	t.Helper()
	got := 0
	for _, event := range events {
		if event.EventType == runtimeevents.EventApprovalResolved.String() &&
			event.Data["approval_id"] == approvalID && event.Data["decision"] == decision {
			got++
		}
	}
	if got != want {
		t.Fatalf("approval.resolved %q decision %q count = %d, want %d; events = %+v", approvalID, decision, got, want, events)
	}
}

func assertRequestEventCount(t *testing.T, events []types.TaskRunEvent, requestID string, want int) {
	t.Helper()
	got := 0
	for _, event := range events {
		if event.RequestID == requestID {
			got++
		}
	}
	if got != want {
		t.Fatalf("request %q event count = %d, want %d; events = %+v", requestID, got, want, events)
	}
}

func assertApprovalTelemetryCount(t *testing.T, runner *Runner, requestID string, want int) {
	t.Helper()
	trace, found := runner.tracer.Get(requestID)
	if !found {
		t.Fatalf("trace %q not found", requestID)
	}
	got := 0
	for _, event := range trace.Events() {
		if event.Name == telemetry.EventOrchestratorApprovalResolved {
			got++
		}
	}
	if got != want {
		t.Fatalf("%s telemetry count = %d, want %d", telemetry.EventOrchestratorApprovalResolved, got, want)
	}
}

func assertApprovalMetricDecisions(t *testing.T, reader *sdkmetric.ManualReader, want map[string]int64) {
	t.Helper()
	metric := findMetricSum(t, reader, telemetry.MetricOrchestratorApprovalsTotal)
	got := make(map[string]int64, len(metric.DataPoints))
	for _, point := range metric.DataPoints {
		decision := metricAttribute(point.Attributes, telemetry.AttrHecateApprovalDecision)
		got[decision] += point.Value
	}
	if len(got) != len(want) {
		t.Fatalf("approval metric decisions = %+v, want %+v", got, want)
	}
	for decision, count := range want {
		if got[decision] != count {
			t.Fatalf("approval metric decision %q = %d, want %d; all=%+v", decision, got[decision], count, got)
		}
	}
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

func assertApprovalEventNoSnapshot(t *testing.T, events []types.TaskRunEvent, decision string) {
	t.Helper()
	event := approvalEvent(t, events, decision)
	if event == nil {
		t.Fatalf("missing approval.resolved decision %q in events %+v", decision, events)
	}
	for _, key := range []string{"run", "steps", "artifacts", "snapshot"} {
		if _, ok := event.Data[key]; ok {
			t.Fatalf("approval.resolved %q carried %q snapshot data: %+v", decision, key, event.Data)
		}
	}
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
