package orchestrator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/taskruncoord"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestResumeTaskWithBudget_RejectedOriginLeavesTaskAndRunsUnchanged(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		gate func(*testing.T, taskruncoord.Origin) (*taskruncoord.Gate, func())
		want error
	}{
		{
			name: "confirmed missing owner",
			gate: func(_ *testing.T, _ taskruncoord.Origin) (*taskruncoord.Gate, func()) {
				gate := taskruncoord.NewOriginGate()
				gate.SetValidator("chat", func(context.Context, taskruncoord.Origin) error {
					return taskruncoord.ErrOriginNotFound
				})
				return gate, func() {}
			},
			want: taskruncoord.ErrOriginUnavailable,
		},
		{
			name: "owner deletion in progress",
			gate: func(t *testing.T, origin taskruncoord.Origin) (*taskruncoord.Gate, func()) {
				gate := taskruncoord.NewOriginGate()
				closure, err := gate.Close(t.Context(), origin)
				if err != nil {
					t.Fatalf("Close: %v", err)
				}
				return gate, closure.Release
			},
			want: taskruncoord.ErrOriginRunAdmissionClosed,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			store := taskstate.NewMemoryStore()
			now := time.Now().UTC()
			task, err := store.CreateTask(ctx, types.Task{
				ID: "task-resume-origin-" + tc.name, OriginKind: "chat", OriginID: "chat-resume-origin-" + tc.name,
				Status: "failed", LatestRunID: "run-resume-origin-" + tc.name, BudgetMicrosUSD: 100,
				CreatedAt: now, UpdatedAt: now,
			})
			if err != nil {
				t.Fatalf("CreateTask: %v", err)
			}
			run, err := store.CreateRun(ctx, types.TaskRun{
				ID: task.LatestRunID, TaskID: task.ID, Status: "failed", StartedAt: now, FinishedAt: now,
			})
			if err != nil {
				t.Fatalf("CreateRun: %v", err)
			}
			origin := taskruncoord.Origin{Kind: task.OriginKind, ID: task.OriginID}
			gate, cleanup := tc.gate(t, origin)
			defer cleanup()
			runner := &Runner{store: store}
			runner.SetOriginRunGate(gate)

			_, err = runner.ResumeTaskWithBudget(ctx, task, run, "retry", 250, defaultResourceID)
			if !errors.Is(err, tc.want) {
				t.Fatalf("ResumeTaskWithBudget error = %v, want %v", err, tc.want)
			}
			storedTask, found, getErr := store.GetTask(ctx, task.ID)
			if getErr != nil || !found || storedTask.BudgetMicrosUSD != 100 {
				t.Fatalf("task after rejection = %+v found=%t err=%v", storedTask, found, getErr)
			}
			runs, listErr := store.ListRuns(ctx, task.ID)
			if listErr != nil || len(runs) != 1 || runs[0].ID != run.ID {
				t.Fatalf("runs after rejection = %+v err=%v", runs, listErr)
			}
		})
	}
}

type blockingRunStartStore struct {
	taskstate.Store
	started chan struct{}
	release chan struct{}
}

func (s *blockingRunStartStore) ApplyRunStartTransition(ctx context.Context, transition taskstate.RunStartTransition) (taskstate.RunStartTransitionResult, error) {
	select {
	case <-s.started:
	default:
		close(s.started)
	}
	select {
	case <-s.release:
	case <-ctx.Done():
		return taskstate.RunStartTransitionResult{}, ctx.Err()
	}
	return s.Store.ApplyRunStartTransition(ctx, transition)
}

func TestResumeTaskWithBudget_OriginClosureWaitsForBudgetAndRunMutation(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	base := taskstate.NewMemoryStore()
	now := time.Now().UTC()
	task, err := base.CreateTask(ctx, types.Task{
		ID: "task-resume-budget-fence", OriginKind: "chat", OriginID: "chat-resume-budget-fence",
		Status: "failed", LatestRunID: "run-resume-budget-source", BudgetMicrosUSD: 100,
		ExecutionKind: "stub", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	source, err := base.CreateRun(ctx, types.TaskRun{
		ID: task.LatestRunID, TaskID: task.ID, Status: "failed", StartedAt: now, FinishedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	store := &blockingRunStartStore{Store: base, started: make(chan struct{}), release: make(chan struct{})}
	gate := taskruncoord.NewOriginGate()
	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)), store, nil,
		Config{DeferQueueStart: true},
	)
	runner.SetOriginRunGate(gate)
	resumeDone := make(chan error, 1)
	go func() {
		_, err := runner.ResumeTaskWithBudget(ctx, task, source, "retry", 250, defaultResourceID)
		resumeDone <- err
	}()
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("budget update did not start")
	}
	closeDone := make(chan *taskruncoord.Closure, 1)
	go func() {
		closure, _ := gate.Close(ctx, taskruncoord.Origin{Kind: task.OriginKind, ID: task.OriginID})
		closeDone <- closure
	}()
	select {
	case <-closeDone:
		t.Fatal("origin closure returned while resumed mutation was admitted")
	case <-time.After(25 * time.Millisecond):
	}
	close(store.release)
	if err := <-resumeDone; err != nil {
		t.Fatalf("ResumeTaskWithBudget: %v", err)
	}
	closure := <-closeDone
	if closure == nil {
		t.Fatal("origin closure missing")
	}
	closure.Release()
	storedTask, found, err := base.GetTask(ctx, task.ID)
	if err != nil || !found || storedTask.BudgetMicrosUSD != 250 {
		t.Fatalf("stored task = %+v found=%t err=%v", storedTask, found, err)
	}
	runs, err := base.ListRuns(ctx, task.ID)
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs = %+v err=%v, want source plus resumed", runs, err)
	}
}

func TestResumeTaskWithBudget_ConcurrentStartsPreserveRaisedCeilingAndSingleActiveRun(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	base := taskstate.NewMemoryStore()
	now := time.Now().UTC()
	task, err := base.CreateTask(ctx, types.Task{
		ID: "task-resume-budget-race", Status: "failed", LatestRunID: "run-resume-budget-race-source",
		BudgetMicrosUSD: 100, ExecutionKind: "stub", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	source, err := base.CreateRun(ctx, types.TaskRun{
		ID: task.LatestRunID, TaskID: task.ID, Number: 1, Status: "failed", StartedAt: now, FinishedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	store := &blockingRunStartStore{Store: base, started: make(chan struct{}), release: make(chan struct{})}
	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)), store, nil,
		Config{DeferQueueStart: true},
	)
	highDone := make(chan error, 1)
	go func() {
		_, err := runner.ResumeTaskWithBudget(ctx, task, source, "raise", 250, defaultResourceID)
		highDone <- err
	}()
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("raised-budget transition did not start")
	}
	lowerDone := make(chan error, 1)
	go func() {
		_, err := runner.ResumeTaskWithBudget(ctx, task, source, "stale lower", 200, defaultResourceID)
		lowerDone <- err
	}()
	retryDone := make(chan error, 1)
	go func() {
		_, err := runner.StartTask(ctx, task, defaultResourceID)
		retryDone <- err
	}()
	select {
	case err := <-lowerDone:
		t.Fatalf("lower resume bypassed the per-task start gate: %v", err)
	case err := <-retryDone:
		t.Fatalf("zero-budget retry bypassed the per-task start gate: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(store.release)
	if err := <-highDone; err != nil {
		t.Fatalf("raised-budget resume: %v", err)
	}
	if err := <-lowerDone; !errors.Is(err, ErrBudgetLower) {
		t.Fatalf("stale lower resume error = %v, want ErrBudgetLower", err)
	}
	if err := <-retryDone; !errors.Is(err, ErrActiveRun) {
		t.Fatalf("zero-budget retry error = %v, want ErrActiveRun", err)
	}
	storedTask, found, err := base.GetTask(ctx, task.ID)
	if err != nil || !found || storedTask.BudgetMicrosUSD != 250 {
		t.Fatalf("stored task = %+v found=%t err=%v, want budget 250", storedTask, found, err)
	}
	runs, err := base.ListRuns(ctx, task.ID)
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
		t.Fatalf("runs = %+v, want source plus exactly one active resume", runs)
	}
}
