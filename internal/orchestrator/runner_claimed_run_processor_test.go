package orchestrator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/taskruncoord"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

type failUpdateTaskStore struct {
	taskstate.Store
}

type blockingClaimedStartStore struct {
	taskstate.Store
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockingClaimedStartStore) ApplyRunStateTransition(ctx context.Context, transition taskstate.RunStateTransition) (taskstate.RunStateTransitionResult, error) {
	s.once.Do(func() { close(s.started) })
	select {
	case <-s.release:
	case <-ctx.Done():
		return taskstate.RunStateTransitionResult{}, ctx.Err()
	}
	return s.Store.ApplyRunStateTransition(ctx, transition)
}

func (s failUpdateTaskStore) UpdateTask(context.Context, types.Task) (types.Task, error) {
	return types.Task{}, errors.New("update task failed")
}

func (s failUpdateTaskStore) ApplyRunStateTransition(context.Context, taskstate.RunStateTransition) (taskstate.RunStateTransitionResult, error) {
	return taskstate.RunStateTransitionResult{}, errors.New("update task failed")
}

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

func TestClaimedRunProcessor_DoesNotAckWhenStartTransitionPersistFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseStore := taskstate.NewMemoryStore()
	queue := &recordingQueue{}
	runner := newClaimedRunProcessorTestRunner(failUpdateTaskStore{Store: baseStore}, queue)
	now := time.Now().UTC()
	task := types.Task{
		ID:            "task-start-fail",
		Title:         "Start fail",
		Prompt:        "complete",
		Status:        "queued",
		ExecutionKind: "stub",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	run := types.TaskRun{
		ID:        "run-start-fail",
		TaskID:    task.ID,
		Number:    1,
		Status:    "queued",
		StartedAt: now,
		RequestID: "request-start-fail",
	}
	if _, err := baseStore.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if _, err := baseStore.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}

	runner.queueCoordinator.processQueuedRun(QueueClaim{
		ClaimID: "claim-start-fail",
		Job:     QueueJob{TaskID: task.ID, RunID: run.ID},
	})

	if len(queue.acked) != 0 {
		t.Fatalf("acked claims = %+v, want none", queue.acked)
	}
	if got := runner.inFlightJobCount(); got != 0 {
		t.Fatalf("in-flight jobs = %d, want 0", got)
	}
	events, err := baseStore.ListRunEvents(ctx, task.ID, run.ID, 0, 20)
	if err != nil {
		t.Fatalf("ListRunEvents() error = %v", err)
	}
	for _, event := range events {
		if event.EventType == "run.started" {
			t.Fatalf("unexpected run.started event after failed start transition: %+v", event)
		}
	}
}

func TestClaimedRunProcessor_OriginAdmissionClassifiesClaims(t *testing.T) {
	t.Parallel()

	t.Run("confirmed missing owner is cancelled and acknowledged", func(t *testing.T) {
		ctx := t.Context()
		store := taskstate.NewMemoryStore()
		queue := &recordingQueue{}
		runner := newClaimedRunProcessorTestRunner(store, queue)
		gate := taskruncoord.NewOriginGate()
		gate.SetValidator("chat", func(context.Context, taskruncoord.Origin) error {
			return taskruncoord.ErrOriginNotFound
		})
		runner.SetOriginRunGate(gate)
		task, run := seedQueuedOriginClaim(t, ctx, store, "missing")

		runner.queueCoordinator.processQueuedRun(QueueClaim{ClaimID: "claim-missing", Job: QueueJob{TaskID: task.ID, RunID: run.ID}})

		if len(queue.acked) != 1 || queue.acked[0] != "claim-missing" {
			t.Fatalf("acked claims = %+v, want confirmed-missing claim", queue.acked)
		}
		stored, found, err := store.GetRun(ctx, task.ID, run.ID)
		if err != nil || !found || stored.Status != "cancelled" {
			t.Fatalf("stored run = %+v found=%t err=%v, want cancelled", stored, found, err)
		}
	})

	t.Run("transient validator failure remains retryable", func(t *testing.T) {
		ctx := t.Context()
		store := taskstate.NewMemoryStore()
		queue := &recordingQueue{}
		runner := newClaimedRunProcessorTestRunner(store, queue)
		gate := taskruncoord.NewOriginGate()
		gate.SetValidator("chat", func(context.Context, taskruncoord.Origin) error {
			return errors.New("owner store temporarily unavailable")
		})
		runner.SetOriginRunGate(gate)
		task, run := seedQueuedOriginClaim(t, ctx, store, "transient")

		runner.queueCoordinator.processQueuedRun(QueueClaim{ClaimID: "claim-transient", Job: QueueJob{TaskID: task.ID, RunID: run.ID}})

		if len(queue.acked) != 0 {
			t.Fatalf("acked claims = %+v, want retryable claim", queue.acked)
		}
		stored, found, err := store.GetRun(ctx, task.ID, run.ID)
		if err != nil || !found || stored.Status != "queued" {
			t.Fatalf("stored run = %+v found=%t err=%v, want queued", stored, found, err)
		}
	})

	t.Run("deletion fence rejects claim until reopened", func(t *testing.T) {
		ctx := t.Context()
		store := taskstate.NewMemoryStore()
		queue := &recordingQueue{}
		runner := newClaimedRunProcessorTestRunner(store, queue)
		gate := taskruncoord.NewOriginGate()
		runner.SetOriginRunGate(gate)
		task, run := seedQueuedOriginClaim(t, ctx, store, "closing")
		closure, err := gate.Close(ctx, taskruncoord.Origin{Kind: task.OriginKind, ID: task.OriginID})
		if err != nil {
			t.Fatalf("Close: %v", err)
		}

		runner.queueCoordinator.processQueuedRun(QueueClaim{ClaimID: "claim-closed", Job: QueueJob{TaskID: task.ID, RunID: run.ID}})
		if len(queue.acked) != 0 {
			t.Fatalf("acked closed claim = %+v, want retryable", queue.acked)
		}
		closure.Release()
		runner.queueCoordinator.processQueuedRun(QueueClaim{ClaimID: "claim-reopened", Job: QueueJob{TaskID: task.ID, RunID: run.ID}})
		if len(queue.acked) != 1 || queue.acked[0] != "claim-reopened" {
			t.Fatalf("acked reopened claims = %+v", queue.acked)
		}
	})
}

func TestClaimedRunProcessor_OriginClosureWaitsForClaimedStartCAS(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	base := taskstate.NewMemoryStore()
	store := &blockingClaimedStartStore{Store: base, started: make(chan struct{}), release: make(chan struct{})}
	queue := &recordingQueue{}
	runner := newClaimedRunProcessorTestRunner(store, queue)
	gate := taskruncoord.NewOriginGate()
	runner.SetOriginRunGate(gate)
	task, run := seedQueuedOriginClaim(t, ctx, base, "cas-fence")
	processorDone := make(chan struct{})
	go func() {
		defer close(processorDone)
		runner.queueCoordinator.processQueuedRun(QueueClaim{
			ClaimID: "claim-cas-fence", Job: QueueJob{TaskID: task.ID, RunID: run.ID},
		})
	}()
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("claimed start CAS did not begin")
	}
	closureDone := make(chan *taskruncoord.Closure, 1)
	go func() {
		closure, _ := gate.Close(ctx, taskruncoord.Origin{Kind: task.OriginKind, ID: task.OriginID})
		closureDone <- closure
	}()
	select {
	case <-closureDone:
		t.Fatal("origin closure returned while claimed start CAS was admitted")
	case <-time.After(25 * time.Millisecond):
	}
	close(store.release)
	closure := <-closureDone
	if closure == nil {
		t.Fatal("origin closure missing")
	}
	closure.Release()
	select {
	case <-processorDone:
	case <-time.After(time.Second):
		t.Fatal("claimed processor did not finish")
	}
}

func seedQueuedOriginClaim(t *testing.T, ctx context.Context, store taskstate.Store, suffix string) (types.Task, types.TaskRun) {
	t.Helper()
	now := time.Now().UTC()
	task, err := store.CreateTask(ctx, types.Task{
		ID: "task-origin-claim-" + suffix, OriginKind: "chat", OriginID: "chat-origin-claim-" + suffix,
		Status: "queued", ExecutionKind: "stub", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	run, err := store.CreateRun(ctx, types.TaskRun{
		ID: "run-origin-claim-" + suffix, TaskID: task.ID, Status: "queued", StartedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	return task, run
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
