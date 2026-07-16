package orchestrator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/gitrunner"
	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/internal/workspacecoord"
	"github.com/hecatehq/hecate/pkg/types"
)

type workspaceCoordinatedExecutor struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int64
}

type workspaceBlockingRunStartStore struct {
	taskstate.Store
	started chan struct{}
	release chan struct{}
}

func (store *workspaceBlockingRunStartStore) ApplyRunStartTransition(ctx context.Context, transition taskstate.RunStartTransition) (taskstate.RunStartTransitionResult, error) {
	close(store.started)
	select {
	case <-store.release:
	case <-ctx.Done():
		return taskstate.RunStartTransitionResult{}, ctx.Err()
	}
	return store.Store.ApplyRunStartTransition(ctx, transition)
}

func (executor *workspaceCoordinatedExecutor) Execute(ctx context.Context, _ ExecutionSpec) (*ExecutionResult, error) {
	executor.calls.Add(1)
	close(executor.started)
	select {
	case <-executor.release:
		return &ExecutionResult{Status: "completed"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestRunnerExecuteRun_HoldsWorkspaceWriterLeaseThroughExecution(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	workspaceRoot := t.TempDir()
	workspacePath := filepath.Join(workspaceRoot, "child")
	if err := os.Mkdir(workspacePath, 0o755); err != nil {
		t.Fatalf("Mkdir(child) error = %v", err)
	}
	store := taskstate.NewMemoryStore()
	now := time.Now().UTC()
	task, err := store.CreateTask(ctx, types.Task{
		ID: "task-workspace-coordination", ExecutionKind: "stub", Status: "running",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	run, err := store.CreateRun(ctx, types.TaskRun{
		ID: "run-workspace-coordination", TaskID: task.ID, Number: 1, Status: "running",
		WorkspacePath: workspacePath, StartedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	executor := &workspaceCoordinatedExecutor{started: make(chan struct{}), release: make(chan struct{})}
	registry := workspacecoord.NewRegistry()
	runner := &Runner{store: store, exec: executor}
	runner.SetWorkspaceCoordinator(registry)
	initialClosure, err := registry.TryClose(ctx, workspaceRoot)
	if err != nil {
		t.Fatalf("TryClose() before execution error = %v", err)
	}
	defer initialClosure.Release()

	done := make(chan error, 1)
	go func() {
		_, executeErr := runner.executeRun(ctx, profiler.NewTrace("req-workspace-coordination", nil), task, run, "req-workspace-coordination", nil)
		done <- executeErr
	}()
	select {
	case <-executor.started:
		t.Fatal("executor dispatched before exclusive workspace closure released")
	case <-time.After(25 * time.Millisecond):
	}
	initialClosure.Release()
	select {
	case <-executor.started:
	case <-time.After(time.Second):
		t.Fatal("executor did not start after exclusive workspace closure released")
	}
	if _, err := registry.TryClose(ctx, workspaceRoot); !errors.Is(err, workspacecoord.ErrBusy) {
		t.Fatalf("TryClose() during execution error = %v, want ErrBusy", err)
	}

	close(executor.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("executeRun() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("executeRun() did not finish")
	}
	closure, err := registry.TryClose(ctx, workspaceRoot)
	if err != nil {
		t.Fatalf("TryClose() after execution error = %v", err)
	}
	closure.Release()
}

func TestRunnerExecuteRun_WaitsForExclusiveWorkspaceClosureAndHonorsCancellation(t *testing.T) {
	t.Parallel()

	workspacePath := t.TempDir()
	registry := workspacecoord.NewRegistry()
	closure, err := registry.TryClose(t.Context(), workspacePath)
	if err != nil {
		t.Fatalf("TryClose() error = %v", err)
	}
	defer closure.Release()
	executor := &workspaceCoordinatedExecutor{started: make(chan struct{}), release: make(chan struct{})}
	runner := &Runner{exec: executor}
	runner.SetWorkspaceCoordinator(registry)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, executeErr := runner.executeRun(
			ctx,
			profiler.NewTrace("req-closed-workspace", nil),
			types.Task{ID: "task-closed-workspace", ExecutionKind: "stub"},
			types.TaskRun{ID: "run-closed-workspace", TaskID: "task-closed-workspace", WorkspacePath: workspacePath},
			"req-closed-workspace",
			nil,
		)
		done <- executeErr
	}()
	select {
	case <-executor.started:
		t.Fatal("executor dispatched while workspace closure was held")
	case <-time.After(25 * time.Millisecond):
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("executeRun() error = %v, want context.Canceled", err)
	}
	if calls := executor.calls.Load(); calls != 0 {
		t.Fatalf("executor calls = %d, want 0", calls)
	}
}

func TestRunnerStartTask_HoldsWorkspaceAdmissionThroughDurableRunCreation(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	workspacePath := t.TempDir()
	base := taskstate.NewMemoryStore()
	now := time.Now().UTC()
	task, err := base.CreateTask(ctx, types.Task{
		ID: "task-workspace-start-admission", ExecutionKind: "stub", Status: "pending",
		WorkspaceMode: "in_place", WorkingDirectory: workspacePath,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	store := &workspaceBlockingRunStartStore{Store: base, started: make(chan struct{}), release: make(chan struct{})}
	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store,
		nil,
		Config{DeferQueueStart: true},
	)
	registry := workspacecoord.NewRegistry()
	runner.SetWorkspaceCoordinator(registry)

	done := make(chan error, 1)
	go func() {
		_, startErr := runner.StartTask(ctx, task, defaultResourceID)
		done <- startErr
	}()
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("run-start transition did not begin")
	}
	if _, err := registry.TryClose(ctx, workspacePath); !errors.Is(err, workspacecoord.ErrBusy) {
		t.Fatalf("TryClose() during run-start transition error = %v, want ErrBusy", err)
	}

	close(store.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("StartTask() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("StartTask() did not finish")
	}
	closure, err := registry.TryClose(ctx, workspacePath)
	if err != nil {
		t.Fatalf("TryClose() after run-start admission release error = %v", err)
	}
	defer closure.Release()
	runs, err := base.ListRuns(ctx, task.ID)
	if err != nil || len(runs) != 1 || types.IsTerminalTaskRunStatus(runs[0].Status) {
		t.Fatalf("durable runs after StartTask() = %+v, err=%v; want one non-terminal run", runs, err)
	}
}

func TestRunnerStartTask_CancelledWhileWorkspaceClosedCreatesNoRun(t *testing.T) {
	t.Parallel()

	workspacePath := t.TempDir()
	store := taskstate.NewMemoryStore()
	now := time.Now().UTC()
	task, err := store.CreateTask(t.Context(), types.Task{
		ID: "task-workspace-start-cancelled", ExecutionKind: "stub", Status: "pending",
		WorkspaceMode: "in_place", WorkingDirectory: workspacePath,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store,
		nil,
		Config{DeferQueueStart: true},
	)
	registry := workspacecoord.NewRegistry()
	runner.SetWorkspaceCoordinator(registry)
	closure, err := registry.TryClose(t.Context(), workspacePath)
	if err != nil {
		t.Fatalf("TryClose() error = %v", err)
	}
	defer closure.Release()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, startErr := runner.StartTask(ctx, task, defaultResourceID)
		done <- startErr
	}()
	select {
	case err := <-done:
		t.Fatalf("StartTask() returned before cancellation: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("StartTask() error = %v, want context.Canceled", err)
	}
	runs, err := store.ListRuns(t.Context(), task.ID)
	if err != nil || len(runs) != 0 {
		t.Fatalf("durable runs after cancelled admission = %+v, err=%v; want none", runs, err)
	}
}

func TestRunnerStartTask_RefusedAdmissionDoesNotPartiallyProvisionIsolatedWorkspace(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*testing.T, *types.Task)
	}{
		{
			name: "create",
		},
		{
			name: "copy",
			configure: func(t *testing.T, task *types.Task) {
				source := t.TempDir()
				if err := os.WriteFile(filepath.Join(source, "marker.txt"), []byte("copy source"), 0o644); err != nil {
					t.Fatalf("WriteFile(copy marker) error = %v", err)
				}
				task.WorkingDirectory = source
			},
		},
		{
			name: "clone",
			configure: func(t *testing.T, task *types.Task) {
				source := t.TempDir()
				result, err := gitrunner.NewLocalRunner().Run(t.Context(), source, "init", "--quiet")
				if err != nil {
					t.Fatalf("git init source error = %v: %s", err, result.Stderr)
				}
				task.WorkingDirectory = source
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			coordinationRoot := t.TempDir()
			workspaceRoot := filepath.Join(coordinationRoot, "generated-workspaces")
			store := taskstate.NewMemoryStore()
			now := time.Now().UTC()
			task := types.Task{
				ID: "task-isolated-provision-" + tc.name, ExecutionKind: "stub", Status: "pending",
				CreatedAt: now, UpdatedAt: now,
			}
			if tc.configure != nil {
				tc.configure(t, &task)
			}
			var err error
			task, err = store.CreateTask(t.Context(), task)
			if err != nil {
				t.Fatalf("CreateTask() error = %v", err)
			}
			runner := NewRunner(
				slog.New(slog.NewJSONHandler(io.Discard, nil)),
				store,
				nil,
				Config{DeferQueueStart: true},
			)
			runner.workspaces = NewWorkspaceManager(workspaceRoot)
			registry := workspacecoord.NewRegistry()
			runner.SetWorkspaceCoordinator(registry)
			closure, err := registry.TryClose(t.Context(), coordinationRoot)
			if err != nil {
				t.Fatalf("TryClose(coordination root) error = %v", err)
			}
			defer closure.Release()

			ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
			defer cancel()
			if _, err := runner.StartTask(ctx, task, defaultResourceID); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("StartTask() error = %v, want context deadline", err)
			}
			if _, err := os.Stat(workspaceRoot); !os.IsNotExist(err) {
				t.Fatalf("workspace root after refused admission exists or is unreadable: %v", err)
			}
			runs, err := store.ListRuns(t.Context(), task.ID)
			if err != nil || len(runs) != 0 {
				t.Fatalf("durable runs after refused admission = %+v, err=%v; want none", runs, err)
			}
			persisted, found, err := store.GetTask(t.Context(), task.ID)
			if err != nil || !found {
				t.Fatalf("GetTask() = %+v, %v, %v", persisted, found, err)
			}
			if persisted.Status != "pending" || persisted.LatestRunID != "" {
				t.Fatalf("task projection changed after refused admission: %+v", persisted)
			}
		})
	}
}
