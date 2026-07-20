package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/runtimeevents"
	"github.com/hecatehq/hecate/internal/taskruncoord"
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

type blockingReconcileStateStore struct {
	taskstate.Store
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockingReconcileStateStore) ApplyRunStateTransition(ctx context.Context, transition taskstate.RunStateTransition) (taskstate.RunStateTransitionResult, error) {
	s.once.Do(func() { close(s.started) })
	select {
	case <-s.release:
	case <-ctx.Done():
		return taskstate.RunStateTransitionResult{}, ctx.Err()
	}
	return s.Store.ApplyRunStateTransition(ctx, transition)
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

func TestRequeueDisconnectedRunPreservesRecordedAutoRichInputRoute(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	store := taskstate.NewMemoryStore()
	queue := &recordingQueue{}
	runner := &Runner{
		logger:   slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store:    store,
		policies: make(map[string]struct{}),
	}
	attachTestQueueCoordinator(runner, queue)
	now := time.Now().UTC()
	task := types.Task{ID: "task-auto-rich-recovery", Status: "running", CreatedAt: now, UpdatedAt: now}
	run := types.TaskRun{
		ID:        "run-auto-rich-recovery",
		TaskID:    task.ID,
		Status:    "running",
		Model:     "shared-vision",
		InputRef:  "msg-auto-rich-recovery",
		StartedAt: now,
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	instance := types.ProviderInstanceIdentity{ID: "runtime-vision-a", Kind: types.ProviderInstanceIdentityRuntime}
	if err := runner.recordAgentInputProviderAttempt(ctx, task, run, types.RouteDecision{
		Provider: "vision-a", ProviderKind: "cloud", ProviderInstance: instance, Model: "shared-vision",
	}); err != nil {
		t.Fatalf("recordAgentInputProviderAttempt: %v", err)
	}
	// This is the stale snapshot a boot reconciler could have loaded before
	// the first worker crossed the provider-dispatch boundary.
	if err := runner.requeueDisconnectedRun(ctx, task, run, disconnectedRunRequeueOptions{
		Reason: "boot_reconcile", RecoveryStrategy: "requeue",
	}); err != nil {
		t.Fatalf("requeueDisconnectedRun: %v", err)
	}
	stored, found, err := store.GetRun(ctx, task.ID, run.ID)
	if err != nil || !found {
		t.Fatalf("GetRun: found=%t err=%v", found, err)
	}
	if stored.Status != "queued" || stored.Provider != "vision-a" || stored.ProviderKind != "cloud" || stored.InputProviderInstance != instance {
		t.Fatalf("requeued rich-input run = %+v, want queued vision-a route", stored)
	}
	if len(queue.enqueued) != 1 {
		t.Fatalf("enqueued jobs = %+v, want one recovered run", queue.enqueued)
	}
	req := agentLoopChatRequest(ExecutionSpec{
		Run: stored,
		ChatRequirements: types.ChatRequestRequirements{
			ImageInput:         true,
			NoProviderFailover: true,
			ExactProvider:      true,
			ProviderInstance:   instance,
		},
	}, []types.Message{{Role: "user", Content: "inspect image"}}, nil)
	if req.Scope.ProviderHint != "vision-a" || req.Requirements.ProviderInstance != instance || !req.Requirements.ExactProvider {
		t.Fatalf("recovered request = hint %q requirements %+v, want the recorded Auto route", req.Scope.ProviderHint, req.Requirements)
	}
}

func TestReconcilePendingRunsPaginatesEveryQueuedRun(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	store := taskstate.NewMemoryStore()
	queue := &recordingQueue{}
	runner := &Runner{
		logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), store: store, policies: make(map[string]struct{}),
	}
	attachTestQueueCoordinator(runner, queue)
	want := pendingRunReconcilePageSize + 1
	for index := 0; index < want; index++ {
		taskID := fmt.Sprintf("task-reconcile-page-%04d", index)
		runID := fmt.Sprintf("run-reconcile-page-%04d", index)
		if _, err := store.CreateTask(ctx, types.Task{
			ID: taskID, Status: "queued", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("CreateTask(%d): %v", index, err)
		}
		if _, err := store.CreateRun(ctx, types.TaskRun{
			ID: runID, TaskID: taskID, Number: 1, Status: "queued", StartedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("CreateRun(%d): %v", index, err)
		}
	}

	if err := runner.ReconcilePendingRuns(ctx); err != nil {
		t.Fatalf("ReconcilePendingRuns: %v", err)
	}
	if len(queue.enqueued) != want {
		t.Fatalf("enqueued jobs = %d, want %d", len(queue.enqueued), want)
	}
	seen := make(map[QueueJob]struct{}, want)
	for _, job := range queue.enqueued {
		seen[job] = struct{}{}
	}
	if len(seen) != want {
		t.Fatalf("unique enqueued jobs = %d, want %d", len(seen), want)
	}
	last := QueueJob{TaskID: fmt.Sprintf("task-reconcile-page-%04d", want-1), RunID: fmt.Sprintf("run-reconcile-page-%04d", want-1)}
	if _, ok := seen[last]; !ok {
		t.Fatalf("last paginated queued run was not recovered: %+v", last)
	}
}

func TestReconcilePendingRuns_OriginValidationControlsRecovery(t *testing.T) {
	t.Parallel()

	t.Run("confirmed missing owner settles stale run", func(t *testing.T) {
		ctx := t.Context()
		store := taskstate.NewMemoryStore()
		queue := &recordingQueue{}
		runner := &Runner{logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), store: store, policies: make(map[string]struct{})}
		attachTestQueueCoordinator(runner, queue)
		gate := taskruncoord.NewOriginGate()
		gate.SetValidator("chat", func(context.Context, taskruncoord.Origin) error { return taskruncoord.ErrOriginNotFound })
		runner.SetOriginRunGate(gate)
		task, run := seedRecoverableOriginRun(t, ctx, store, "missing", "running")

		if err := runner.ReconcilePendingRuns(ctx); err != nil {
			t.Fatalf("ReconcilePendingRuns: %v", err)
		}
		stored, found, err := store.GetRun(ctx, task.ID, run.ID)
		if err != nil || !found || stored.Status != "cancelled" {
			t.Fatalf("run = %+v found=%t err=%v, want cancelled", stored, found, err)
		}
		if len(queue.enqueued) != 0 {
			t.Fatalf("enqueued jobs = %+v, want none", queue.enqueued)
		}
	})

	t.Run("transient validation retries after recovery", func(t *testing.T) {
		ctx := t.Context()
		store := taskstate.NewMemoryStore()
		queue := &recordingQueue{}
		runner := &Runner{logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), store: store, policies: make(map[string]struct{})}
		attachTestQueueCoordinator(runner, queue)
		gate := taskruncoord.NewOriginGate()
		available := false
		gate.SetValidator("chat", func(context.Context, taskruncoord.Origin) error {
			if !available {
				return errors.New("owner store temporarily unavailable")
			}
			return nil
		})
		runner.SetOriginRunGate(gate)
		task, run := seedRecoverableOriginRun(t, ctx, store, "transient", "running")

		if err := runner.ReconcilePendingRuns(ctx); !errors.Is(err, taskruncoord.ErrOriginValidationFailed) {
			t.Fatalf("first ReconcilePendingRuns error = %v, want validation failure", err)
		}
		stored, found, err := store.GetRun(ctx, task.ID, run.ID)
		if err != nil || !found || stored.Status != "running" {
			t.Fatalf("run after transient failure = %+v found=%t err=%v", stored, found, err)
		}
		available = true
		if err := runner.queueCoordinator.retryPendingReconciles(ctx); err != nil {
			t.Fatalf("retryPendingReconciles: %v", err)
		}
		stored, found, err = store.GetRun(ctx, task.ID, run.ID)
		if err != nil || !found || stored.Status != "queued" || len(queue.enqueued) != 1 {
			t.Fatalf("recovered run = %+v found=%t err=%v enqueued=%+v", stored, found, err, queue.enqueued)
		}
	})
}

func TestReconcileStaleRunRepairsFailedPostTransitionEnqueue(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	store := taskstate.NewMemoryStore()
	queue := &failFirstEnqueueQueue{failures: 1}
	runner := &Runner{
		logger:   slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store:    store,
		policies: make(map[string]struct{}),
	}
	attachTestQueueCoordinator(runner, queue)
	now := time.Now().UTC()
	task := types.Task{
		ID: "task-stale-enqueue-repair", Status: "running", LatestRunID: "run-stale-enqueue-repair",
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	run := types.TaskRun{
		ID: task.LatestRunID, TaskID: task.ID, Status: "running", StartedAt: now.Add(-time.Hour),
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	if err := runner.reconcileStaleRuns(ctx, time.Minute); err == nil {
		t.Fatal("reconcileStaleRuns error = nil, want enqueue failure")
	}
	storedRun, found, err := store.GetRun(ctx, task.ID, run.ID)
	if err != nil || !found || storedRun.Status != "queued" {
		t.Fatalf("Run after stale transition enqueue failure = (%+v, %v, %v), want queued", storedRun, found, err)
	}
	if len(runner.queueCoordinator.pendingReconciles) != 1 {
		t.Fatalf("pending reconciles = %+v, want failed post-transition enqueue registered", runner.queueCoordinator.pendingReconciles)
	}
	if err := runner.queueCoordinator.retryPendingReconciles(ctx); err != nil {
		t.Fatalf("retryPendingReconciles: %v", err)
	}
	if len(queue.enqueued) != 1 || queue.enqueued[0] != (QueueJob{TaskID: task.ID, RunID: run.ID}) {
		t.Fatalf("queued jobs = %+v, want recovered stale Run", queue.enqueued)
	}
	if len(runner.queueCoordinator.pendingReconciles) != 0 {
		t.Fatalf("pending reconciles after retry = %+v, want cleared", runner.queueCoordinator.pendingReconciles)
	}
	events, err := store.ListRunEvents(ctx, task.ID, run.ID, 0, 20)
	if err != nil {
		t.Fatalf("ListRunEvents: %v", err)
	}
	assertEventCount(t, events, runtimeevents.EventGapRunDisconnected.String(), 1)
}

func TestReconcilePendingRuns_CASPreservesConcurrentTerminalWinner(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	base := taskstate.NewMemoryStore()
	store := &blockingReconcileStateStore{Store: base, started: make(chan struct{}), release: make(chan struct{})}
	queue := &recordingQueue{}
	runner := &Runner{logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), store: store, policies: make(map[string]struct{})}
	attachTestQueueCoordinator(runner, queue)
	task, run := seedRecoverableOriginRun(t, ctx, base, "cas", "running")
	reconcileDone := make(chan error, 1)
	go func() { reconcileDone <- runner.ReconcilePendingRuns(ctx) }()
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("reconcile state transition did not begin")
	}
	cancelledTask := task
	cancelledTask.Status = "cancelled"
	cancelledRun := run
	cancelledRun.Status = "cancelled"
	cancelledRun.FinishedAt = time.Now().UTC()
	if result, err := base.ApplyRunTerminalTransition(ctx, taskstate.TerminalRunTransition{
		Task: cancelledTask, Run: cancelledRun, FinishedAt: cancelledRun.FinishedAt,
	}); err != nil || !result.Applied {
		t.Fatalf("concurrent terminal transition applied=%t err=%v", result.Applied, err)
	}
	close(store.release)
	if err := <-reconcileDone; err != nil {
		t.Fatalf("ReconcilePendingRuns: %v", err)
	}
	stored, found, err := base.GetRun(ctx, task.ID, run.ID)
	if err != nil || !found || stored.Status != "cancelled" {
		t.Fatalf("run after reconcile race = %+v found=%t err=%v", stored, found, err)
	}
	if len(queue.enqueued) != 0 {
		t.Fatalf("stale reconcile enqueued terminal run: %+v", queue.enqueued)
	}
}

func seedRecoverableOriginRun(t *testing.T, ctx context.Context, store taskstate.Store, suffix, status string) (types.Task, types.TaskRun) {
	t.Helper()
	now := time.Now().UTC().Add(-time.Minute)
	task, err := store.CreateTask(ctx, types.Task{
		ID: "task-reconcile-origin-" + suffix, OriginKind: "chat", OriginID: "chat-reconcile-origin-" + suffix,
		Status: status, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	run, err := store.CreateRun(ctx, types.TaskRun{ID: "run-reconcile-origin-" + suffix, TaskID: task.ID, Status: status, StartedAt: now})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	return task, run
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
