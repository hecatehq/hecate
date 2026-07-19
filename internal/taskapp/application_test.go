package taskapp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

type recordingTaskApplicationRunner struct {
	startCalls int
	startTask  types.Task

	resumeCalls  int
	resumeTask   types.Task
	resumeRun    types.TaskRun
	resumeReason string
	resumeBudget int64

	continueCalls  int
	continuePrompt string

	retryFromModelCallCalls int
	retryFromModelCall      int

	cancelCalls  int
	cancelRunID  string
	cancelRunIDs []string
	cancelReason string
	cancelErr    error

	resolveCalls int
	resolveReq   orchestrator.ResolveApprovalRequest
}

func (r *recordingTaskApplicationRunner) StartTask(_ context.Context, task types.Task, _ func(string) string) (*orchestrator.StartTaskResult, error) {
	r.startCalls++
	r.startTask = task
	run := types.TaskRun{ID: "run_started", TaskID: task.ID, Status: "queued"}
	return &orchestrator.StartTaskResult{Task: task, Run: run}, nil
}

func (r *recordingTaskApplicationRunner) ResumeTaskWithBudget(_ context.Context, task types.Task, run types.TaskRun, reason string, budgetMicrosUSD int64, _ func(string) string) (*orchestrator.StartTaskResult, error) {
	r.resumeCalls++
	if budgetMicrosUSD > 0 {
		task.BudgetMicrosUSD = budgetMicrosUSD
	}
	r.resumeTask = task
	r.resumeRun = run
	r.resumeReason = reason
	r.resumeBudget = budgetMicrosUSD
	resumed := types.TaskRun{ID: "run_resumed", TaskID: task.ID, Status: "queued"}
	return &orchestrator.StartTaskResult{Task: task, Run: resumed}, nil
}

func (r *recordingTaskApplicationRunner) ContinueAgentTask(_ context.Context, task types.Task, run types.TaskRun, prompt string, _ func(string) string) (*orchestrator.StartTaskResult, error) {
	r.continueCalls++
	r.continuePrompt = prompt
	continued := types.TaskRun{ID: "run_continued", TaskID: task.ID, Status: "queued"}
	return &orchestrator.StartTaskResult{Task: task, Run: continued}, nil
}

func (r *recordingTaskApplicationRunner) RetryTaskFromModelCall(_ context.Context, task types.Task, run types.TaskRun, modelCall int, _ string, _ func(string) string) (*orchestrator.StartTaskResult, error) {
	r.retryFromModelCallCalls++
	r.retryFromModelCall = modelCall
	retried := types.TaskRun{ID: "run_model_call_retry", TaskID: task.ID, Status: "queued"}
	return &orchestrator.StartTaskResult{Task: task, Run: retried}, nil
}

func (r *recordingTaskApplicationRunner) CancelRun(_ context.Context, task types.Task, runID string, reason string) (types.TaskRun, error) {
	r.cancelCalls++
	r.cancelRunID = runID
	r.cancelRunIDs = append(r.cancelRunIDs, runID)
	r.cancelReason = reason
	if r.cancelErr != nil {
		return types.TaskRun{}, r.cancelErr
	}
	return types.TaskRun{ID: runID, TaskID: task.ID, Status: "cancelled"}, nil
}

func (r *recordingTaskApplicationRunner) ResolveTaskApproval(_ context.Context, req orchestrator.ResolveApprovalRequest) (*orchestrator.ResolveApprovalResult, error) {
	r.resolveCalls++
	r.resolveReq = req
	return &orchestrator.ResolveApprovalResult{
		Approval: types.TaskApproval{
			ID:     req.ApprovalID,
			TaskID: req.Task.ID,
			Status: "approved",
		},
	}, nil
}

func (r *recordingTaskApplicationRunner) totalCalls() int {
	return r.startCalls + r.resumeCalls + r.continueCalls + r.retryFromModelCallCalls + r.cancelCalls + r.resolveCalls
}

func newTestTaskApplication(store taskstate.Store, runner Runner) *Application {
	return newTestTaskApplicationWithProjects(store, runner, nil)
}

func newTestTaskApplicationWithProjects(store taskstate.Store, runner Runner, projectStore projects.Store) *Application {
	return New(Options{
		Store:       store,
		Runner:      runner,
		Projects:    projectStore,
		IDGenerator: func(prefix string) string { return prefix + "_fixed" },
		Now: func() time.Time {
			return time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
		},
	})
}

func createTaskForAppTest(t *testing.T, ctx context.Context, store taskstate.Store, task types.Task) types.Task {
	t.Helper()
	if task.ID == "" {
		task.ID = "task_test"
	}
	if task.Title == "" {
		task.Title = task.ID
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now().UTC()
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = task.CreatedAt
	}
	created, err := store.CreateTask(ctx, task)
	if err != nil {
		t.Fatalf("CreateTask(%s): %v", task.ID, err)
	}
	return created
}

func createRunForAppTest(t *testing.T, ctx context.Context, store taskstate.Store, run types.TaskRun) types.TaskRun {
	t.Helper()
	if run.ID == "" {
		run.ID = "run_test"
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now().UTC()
	}
	created, err := store.CreateRun(ctx, run)
	if err != nil {
		t.Fatalf("CreateRun(%s): %v", run.ID, err)
	}
	return created
}

func TestTaskApplication_CreateTaskAppliesDefaults(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	app := newTestTaskApplication(store, &recordingTaskApplicationRunner{})

	task, err := app.CreateTask(ctx, CreateCommand{
		Prompt:           "  Build the repo  ",
		ExecutionProfile: "repo_local",
		Repo:             "/tmp/hecate",
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	if task.ID != "task_fixed" {
		t.Fatalf("id = %q, want task_fixed", task.ID)
	}
	if task.Title != "Build the repo" || task.Prompt != "Build the repo" {
		t.Fatalf("title/prompt = %q/%q, want trimmed prompt title", task.Title, task.Prompt)
	}
	if task.ExecutionKind != "agent_loop" {
		t.Fatalf("execution_kind = %q, want agent_loop", task.ExecutionKind)
	}
	if task.WorkspaceMode != "persistent" || task.WorkingDirectory != "." {
		t.Fatalf("workspace_mode/working_directory = %q/%q, want persistent/.", task.WorkspaceMode, task.WorkingDirectory)
	}
	if task.SandboxAllowedRoot != "/tmp/hecate" {
		t.Fatalf("sandbox_allowed_root = %q, want /tmp/hecate", task.SandboxAllowedRoot)
	}
	if task.TimeoutMS != 120000 {
		t.Fatalf("timeout_ms = %d, want 120000", task.TimeoutMS)
	}
	if task.Priority != "normal" || task.Status != "queued" {
		t.Fatalf("priority/status = %q/%q, want normal/queued", task.Priority, task.Status)
	}
	if got := task.CreatedAt; !got.Equal(time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("created_at = %s, want fixed clock", got)
	}
}

func TestTaskApplication_CreateTaskValidatesProject(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	app := newTestTaskApplication(store, nil)

	_, err := app.CreateTask(ctx, CreateCommand{
		Prompt:    "Use project context",
		ProjectID: "proj_missing_store",
	})
	if !errors.Is(err, ErrProjectStoreNotConfigured) {
		t.Fatalf("CreateTask(project without store) error = %v, want ErrProjectStoreNotConfigured", err)
	}

	projectStore := projects.NewMemoryStore()
	app = newTestTaskApplicationWithProjects(store, nil, projectStore)
	_, err = app.CreateTask(ctx, CreateCommand{
		Prompt:    "Use project context",
		ProjectID: "proj_missing",
	})
	if !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("CreateTask(missing project) error = %v, want ErrProjectNotFound", err)
	}

	if _, err := projectStore.Create(ctx, projects.Project{ID: "proj_1", Name: "Project One"}); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	task, err := app.CreateTask(ctx, CreateCommand{
		Prompt:    "Use project context",
		ProjectID: " proj_1 ",
	})
	if err != nil {
		t.Fatalf("CreateTask(existing project) error = %v", err)
	}
	if task.ProjectID != "proj_1" {
		t.Fatalf("task project_id = %q, want proj_1", task.ProjectID)
	}
}

func TestTaskApplication_CreateTaskValidation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	app := newTestTaskApplication(taskstate.NewMemoryStore(), nil)

	if _, err := app.CreateTask(ctx, CreateCommand{}); !errors.Is(err, ErrPromptRequired) {
		t.Fatalf("CreateTask(agent_loop without prompt) error = %v, want ErrPromptRequired", err)
	}

	task, err := app.CreateTask(ctx, CreateCommand{
		ExecutionKind: "shell",
		ShellCommand:  "printf ok",
	})
	if err != nil {
		t.Fatalf("CreateTask(shell without prompt) error = %v", err)
	}
	if task.Prompt != "" || task.Title != "New task" {
		t.Fatalf("shell task prompt/title = %q/%q, want empty/New task", task.Prompt, task.Title)
	}
}

func TestTaskApplication_NilStoreAndRunnerErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	app := New(Options{})
	if _, err := app.CreateTask(ctx, CreateCommand{Prompt: "x"}); !errors.Is(err, ErrStoreNotConfigured) {
		t.Fatalf("CreateTask(nil store) error = %v, want ErrStoreNotConfigured", err)
	}
	if _, err := app.ListTasks(ctx, taskstate.TaskFilter{}); !errors.Is(err, ErrStoreNotConfigured) {
		t.Fatalf("ListTasks(nil store) error = %v, want ErrStoreNotConfigured", err)
	}
	if _, err := app.LoadTask(ctx, "task"); !errors.Is(err, ErrStoreNotConfigured) {
		t.Fatalf("LoadTask(nil store) error = %v, want ErrStoreNotConfigured", err)
	}
	if _, err := app.LoadTaskRun(ctx, types.Task{ID: "task"}, "run"); !errors.Is(err, ErrStoreNotConfigured) {
		t.Fatalf("LoadTaskRun(nil store) error = %v, want ErrStoreNotConfigured", err)
	}
	if _, err := app.GetTaskApproval(ctx, types.Task{ID: "task"}, "approval"); !errors.Is(err, ErrStoreNotConfigured) {
		t.Fatalf("GetTaskApproval(nil store) error = %v, want ErrStoreNotConfigured", err)
	}
	if err := app.RequireRunner(); !errors.Is(err, ErrStoreNotConfigured) {
		t.Fatalf("RequireRunner(nil store) error = %v, want ErrStoreNotConfigured", err)
	}

	app = newTestTaskApplication(taskstate.NewMemoryStore(), nil)
	if err := app.RequireRunner(); !errors.Is(err, ErrRunnerNotConfigured) {
		t.Fatalf("RequireRunner(nil runner) error = %v, want ErrRunnerNotConfigured", err)
	}
	if _, err := app.StartTask(ctx, types.Task{ID: "task"}); !errors.Is(err, ErrRunnerNotConfigured) {
		t.Fatalf("StartTask(nil runner) error = %v, want ErrRunnerNotConfigured", err)
	}
	if _, err := app.ResolveTaskApproval(ctx, ResolveApprovalCommand{}); !errors.Is(err, ErrRunnerNotConfigured) {
		t.Fatalf("ResolveTaskApproval(nil runner) error = %v, want ErrRunnerNotConfigured", err)
	}
}

func TestTaskApplication_LoadNotFoundErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	app := newTestTaskApplication(store, nil)
	task := createTaskForAppTest(t, ctx, store, types.Task{ID: "task_found"})

	if _, err := app.LoadTask(ctx, "missing"); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("LoadTask(missing) error = %v, want ErrTaskNotFound", err)
	}
	if _, err := app.LoadTask(ctx, " "); !errors.Is(err, ErrTaskIDRequired) || !IsValidationError(err) {
		t.Fatalf("LoadTask(empty) error = %v, want task validation ErrTaskIDRequired", err)
	}
	if _, err := app.LoadTaskRun(ctx, task, "missing"); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("LoadTaskRun(missing) error = %v, want ErrRunNotFound", err)
	}
	if _, err := app.LoadTaskRun(ctx, task, " "); !errors.Is(err, ErrRunIDRequired) || !IsValidationError(err) {
		t.Fatalf("LoadTaskRun(empty) error = %v, want task validation ErrRunIDRequired", err)
	}
	if _, err := app.GetTaskApproval(ctx, task, "missing"); !errors.Is(err, ErrApprovalNotFound) {
		t.Fatalf("GetTaskApproval(missing) error = %v, want ErrApprovalNotFound", err)
	}
	if _, err := app.GetTaskApproval(ctx, task, " "); !errors.Is(err, ErrApprovalIDRequired) || !IsValidationError(err) {
		t.Fatalf("GetTaskApproval(empty) error = %v, want task validation ErrApprovalIDRequired", err)
	}
}

func TestTaskApplication_StartTaskRejectsActiveRunBeforeRunner(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	runner := &recordingTaskApplicationRunner{}
	app := newTestTaskApplication(store, runner)
	task := types.Task{
		ID:          "task_active",
		Title:       "active",
		Status:      "completed",
		LatestRunID: "run_active",
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	createTaskForAppTest(t, ctx, store, task)
	createRunForAppTest(t, ctx, store, types.TaskRun{ID: task.LatestRunID, TaskID: task.ID, Status: "awaiting_approval"})

	_, err := app.StartTask(ctx, task)
	if !errors.Is(err, ErrActiveRun) {
		t.Fatalf("StartTask() error = %v, want ErrActiveRun", err)
	}
	if runner.startCalls != 0 {
		t.Fatalf("runner start calls = %d, want 0", runner.startCalls)
	}
}

func TestTaskApplication_CancelDispatchesRunIDAndReason(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	runner := &recordingTaskApplicationRunner{}
	app := newTestTaskApplication(store, runner)
	task := types.Task{ID: "task_cancel"}
	run := types.TaskRun{ID: "run_cancel", TaskID: task.ID, Status: "running"}

	cancelled, err := app.CancelTaskRun(ctx, task, run, "operator stop")
	if err != nil {
		t.Fatalf("CancelTaskRun() error = %v", err)
	}
	if cancelled.Status != "cancelled" {
		t.Fatalf("cancelled status = %q, want cancelled", cancelled.Status)
	}
	if runner.cancelCalls != 1 || runner.cancelRunID != run.ID || runner.cancelReason != "operator stop" {
		t.Fatalf("cancel dispatch calls/id/reason = %d/%q/%q, want 1/%q/operator stop", runner.cancelCalls, runner.cancelRunID, runner.cancelReason, run.ID)
	}
}

func TestTaskApplication_CancelNonTerminalRunsByOrigin(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	runner := &recordingTaskApplicationRunner{}
	app := newTestTaskApplication(store, runner)
	now := time.Now().UTC()
	ownedOne := createTaskForAppTest(t, ctx, store, types.Task{
		ID: "task_owned_one", OriginKind: "chat", OriginID: "chat_delete", Status: "running", CreatedAt: now, UpdatedAt: now,
	})
	ownedTwo := createTaskForAppTest(t, ctx, store, types.Task{
		ID: "task_owned_two", OriginKind: "chat", OriginID: "chat_delete", Status: "awaiting_approval", CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second),
	})
	otherOrigin := createTaskForAppTest(t, ctx, store, types.Task{
		ID: "task_other_origin", OriginKind: "chat", OriginID: "chat_other", Status: "running", CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second),
	})
	otherKind := createTaskForAppTest(t, ctx, store, types.Task{
		ID: "task_other_kind", OriginKind: "project_work_item", OriginID: "chat_delete", Status: "running", CreatedAt: now.Add(3 * time.Second), UpdatedAt: now.Add(3 * time.Second),
	})
	createRunForAppTest(t, ctx, store, types.TaskRun{ID: "run_owned_running", TaskID: ownedOne.ID, Status: "running"})
	createRunForAppTest(t, ctx, store, types.TaskRun{ID: "run_owned_terminal", TaskID: ownedOne.ID, Status: "completed"})
	createRunForAppTest(t, ctx, store, types.TaskRun{ID: "run_owned_approval", TaskID: ownedTwo.ID, Status: "awaiting_approval"})
	createRunForAppTest(t, ctx, store, types.TaskRun{ID: "run_other_origin", TaskID: otherOrigin.ID, Status: "running"})
	createRunForAppTest(t, ctx, store, types.TaskRun{ID: "run_other_kind", TaskID: otherKind.ID, Status: "running"})

	result, settlement, err := app.CancelNonTerminalRunsByOrigin(ctx, CancelRunsByOriginCommand{
		OriginKind: " chat ",
		OriginID:   " chat_delete ",
		Reason:     " source deleted ",
	})
	if err != nil {
		t.Fatalf("CancelNonTerminalRunsByOrigin() error = %v", err)
	}
	defer settlement.Release()
	if len(result.Runs) != 2 || runner.cancelCalls != 3 {
		t.Fatalf("cancelled result/cancel calls = %d/%d, want 2/3", len(result.Runs), runner.cancelCalls)
	}
	cancelledIDs := make(map[string]bool, len(runner.cancelRunIDs))
	for _, runID := range runner.cancelRunIDs {
		cancelledIDs[runID] = true
	}
	if !cancelledIDs["run_owned_running"] || !cancelledIDs["run_owned_approval"] {
		t.Fatalf("cancelled run ids = %v, want both owned nonterminal runs", runner.cancelRunIDs)
	}
	if !cancelledIDs["run_owned_terminal"] {
		t.Fatalf("cancelled run ids = %v, want terminal run drain/cleanup retry", runner.cancelRunIDs)
	}
	for _, unexpected := range []string{"run_other_origin", "run_other_kind"} {
		if cancelledIDs[unexpected] {
			t.Fatalf("cancelled run ids = %v, did not want %q", runner.cancelRunIDs, unexpected)
		}
	}
	if runner.cancelReason != "source deleted" {
		t.Fatalf("cancel reason = %q, want source deleted", runner.cancelReason)
	}
	if _, found, err := store.GetTask(ctx, ownedOne.ID); err != nil || !found {
		t.Fatalf("owned task history after cancellation: found=%t err=%v", found, err)
	}
	if _, found, err := store.GetRun(ctx, ownedOne.ID, "run_owned_terminal"); err != nil || !found {
		t.Fatalf("terminal run history after cancellation: found=%t err=%v", found, err)
	}
}

func TestTaskApplication_CancelNonTerminalRunsByOriginReportsEveryFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	cancelErr := errors.New("cancel persistence failed")
	runner := &recordingTaskApplicationRunner{cancelErr: cancelErr}
	app := newTestTaskApplication(store, runner)
	for index, taskID := range []string{"task_origin_a", "task_origin_b"} {
		task := createTaskForAppTest(t, ctx, store, types.Task{
			ID: taskID, OriginKind: "chat", OriginID: "chat_failure", Status: "running", UpdatedAt: time.Now().UTC().Add(time.Duration(index) * time.Second),
		})
		createRunForAppTest(t, ctx, store, types.TaskRun{ID: "run_" + taskID, TaskID: task.ID, Status: "running"})
	}

	result, settlement, err := app.CancelNonTerminalRunsByOrigin(ctx, CancelRunsByOriginCommand{OriginKind: "chat", OriginID: "chat_failure"})
	defer settlement.Release()
	if !errors.Is(err, cancelErr) {
		t.Fatalf("CancelNonTerminalRunsByOrigin() error = %v, want cancellation cause", err)
	}
	if len(result.Runs) != 0 || runner.cancelCalls != 2 {
		t.Fatalf("cancelled result/calls = %d/%d, want 0/2 after best-effort failures", len(result.Runs), runner.cancelCalls)
	}
}

func TestTaskApplication_CancelNonTerminalRunsByOriginFailsClosedWhenTerminalExecutorHasNotDrained(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	store := taskstate.NewMemoryStore()
	drainErr := errors.New("executor still draining")
	runner := &recordingTaskApplicationRunner{cancelErr: drainErr}
	app := newTestTaskApplication(store, runner)
	task := createTaskForAppTest(t, ctx, store, types.Task{
		ID: "task_terminal_drain", OriginKind: "chat", OriginID: "chat_terminal_drain", Status: "cancelled",
	})
	createRunForAppTest(t, ctx, store, types.TaskRun{ID: "run_terminal_drain", TaskID: task.ID, Status: "cancelled"})

	_, settlement, err := app.CancelNonTerminalRunsByOrigin(ctx, CancelRunsByOriginCommand{
		OriginKind: "chat", OriginID: "chat_terminal_drain",
	})
	if settlement == nil {
		t.Fatal("missing origin settlement")
	}
	defer settlement.Release()
	if !errors.Is(err, drainErr) {
		t.Fatalf("CancelNonTerminalRunsByOrigin error = %v, want drain failure", err)
	}
	if runner.cancelCalls != 1 {
		t.Fatalf("terminal cancellation calls = %d, want 1", runner.cancelCalls)
	}
}

func TestTaskApplication_CancelNonTerminalRunsByOriginValidatesOwnership(t *testing.T) {
	t.Parallel()

	app := newTestTaskApplication(taskstate.NewMemoryStore(), nil)
	if _, _, err := app.CancelNonTerminalRunsByOrigin(context.Background(), CancelRunsByOriginCommand{OriginID: "chat_1"}); !errors.Is(err, ErrOriginKindRequired) || !IsValidationError(err) {
		t.Fatalf("empty origin kind error = %v, want validation ErrOriginKindRequired", err)
	}
	if _, _, err := app.CancelNonTerminalRunsByOrigin(context.Background(), CancelRunsByOriginCommand{OriginKind: "chat"}); !errors.Is(err, ErrOriginIDRequired) || !IsValidationError(err) {
		t.Fatalf("empty origin id error = %v, want validation ErrOriginIDRequired", err)
	}
}

func TestTaskApplication_LifecycleRejectsOtherActiveRunBeforeRunner(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		call func(context.Context, *Application, types.Task, types.TaskRun) error
	}{
		{
			name: "retry",
			call: func(ctx context.Context, app *Application, task types.Task, run types.TaskRun) error {
				_, err := app.RetryTaskRun(ctx, task, run)
				return err
			},
		},
		{
			name: "resume",
			call: func(ctx context.Context, app *Application, task types.Task, run types.TaskRun) error {
				_, err := app.ResumeTaskRun(ctx, task, run, ResumeCommand{Reason: "try again"})
				return err
			},
		},
		{
			name: "continue",
			call: func(ctx context.Context, app *Application, task types.Task, run types.TaskRun) error {
				_, err := app.ContinueTaskRun(ctx, task, run, "continue")
				return err
			},
		},
		{
			name: "retry_from_model_call",
			call: func(ctx context.Context, app *Application, task types.Task, run types.TaskRun) error {
				_, err := app.RetryTaskRunFromModelCall(ctx, task, run, RetryFromModelCallCommand{ModelCallIndex: 1, Reason: "rewind"})
				return err
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			store := taskstate.NewMemoryStore()
			runner := &recordingTaskApplicationRunner{}
			app := newTestTaskApplication(store, runner)
			task := createTaskForAppTest(t, ctx, store, types.Task{
				ID:          "task_" + tc.name,
				Status:      "failed",
				LatestRunID: "run_active",
			})
			createRunForAppTest(t, ctx, store, types.TaskRun{ID: "run_active", TaskID: task.ID, Status: "running"})
			run := createRunForAppTest(t, ctx, store, types.TaskRun{ID: "run_source", TaskID: task.ID, Status: "failed", ModelCallCount: 1})

			err := tc.call(ctx, app, task, run)
			if !errors.Is(err, ErrOtherActiveRun) {
				t.Fatalf("%s error = %v, want ErrOtherActiveRun", tc.name, err)
			}
			if runner.totalCalls() != 0 {
				t.Fatalf("runner calls = %d, want 0", runner.totalCalls())
			}
		})
	}
}

func TestTaskApplication_LifecycleValidationPrecedesRunnerConfiguration(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		call func(context.Context, *Application, types.Task, types.TaskRun) error
		want error
	}{
		{
			name: "retry_nonterminal",
			call: func(ctx context.Context, app *Application, task types.Task, run types.TaskRun) error {
				_, err := app.RetryTaskRun(ctx, task, run)
				return err
			},
			want: ErrRunNotRetryable,
		},
		{
			name: "resume_nonterminal",
			call: func(ctx context.Context, app *Application, task types.Task, run types.TaskRun) error {
				_, err := app.ResumeTaskRun(ctx, task, run, ResumeCommand{Reason: "try again"})
				return err
			},
			want: ErrRunNotResumable,
		},
		{
			name: "model_call_retry_nonterminal",
			call: func(ctx context.Context, app *Application, task types.Task, run types.TaskRun) error {
				_, err := app.RetryTaskRunFromModelCall(ctx, task, run, RetryFromModelCallCommand{ModelCallIndex: 1})
				return err
			},
			want: ErrRunNotModelCallRetryable,
		},
		{
			name: "resume_other_active_before_lower_budget",
			call: func(ctx context.Context, app *Application, task types.Task, run types.TaskRun) error {
				run.Status = "failed"
				task.BudgetMicrosUSD = 500
				_, err := app.ResumeTaskRun(ctx, task, run, ResumeCommand{BudgetMicrosUSD: 100})
				return err
			},
			want: ErrOtherActiveRun,
		},
		{
			name: "continue_other_active",
			call: func(ctx context.Context, app *Application, task types.Task, run types.TaskRun) error {
				_, err := app.ContinueTaskRun(ctx, task, run, "continue")
				return err
			},
			want: ErrOtherActiveRun,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			store := taskstate.NewMemoryStore()
			app := newTestTaskApplication(store, nil)
			task := createTaskForAppTest(t, ctx, store, types.Task{
				ID:          "task_" + tc.name,
				Status:      "failed",
				LatestRunID: "run_active",
			})
			createRunForAppTest(t, ctx, store, types.TaskRun{ID: "run_active", TaskID: task.ID, Status: "running"})
			run := createRunForAppTest(t, ctx, store, types.TaskRun{ID: "run_source", TaskID: task.ID, Status: "running"})

			err := tc.call(ctx, app, task, run)
			if !errors.Is(err, tc.want) {
				t.Fatalf("%s error = %v, want %v", tc.name, err, tc.want)
			}
		})
	}
}

func TestTaskApplication_ResumeLowerBudgetPrecedesRunnerConfiguration(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	app := newTestTaskApplication(store, nil)
	task := createTaskForAppTest(t, ctx, store, types.Task{
		ID:              "task_lower_budget_no_runner",
		Status:          "failed",
		BudgetMicrosUSD: 500,
		LatestRunID:     "run_failed",
	})
	run := createRunForAppTest(t, ctx, store, types.TaskRun{ID: task.LatestRunID, TaskID: task.ID, Status: "failed"})

	_, err := app.ResumeTaskRun(ctx, task, run, ResumeCommand{BudgetMicrosUSD: 100})
	if !errors.Is(err, ErrBudgetLower) {
		t.Fatalf("ResumeTaskRun(lower budget, nil runner) error = %v, want ErrBudgetLower", err)
	}
}

func TestTaskApplication_RetryFromModelCallValidatesModelCallBeforeRunner(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	app := newTestTaskApplication(store, nil)
	task := createTaskForAppTest(t, ctx, store, types.Task{ID: "task_model_call", Status: "failed"})
	run := createRunForAppTest(t, ctx, store, types.TaskRun{ID: "run_failed", TaskID: task.ID, Status: "failed", ModelCallCount: 1})

	_, err := app.RetryTaskRunFromModelCall(ctx, task, run, RetryFromModelCallCommand{ModelCallIndex: 0})
	if !errors.Is(err, ErrModelCallIndexRequired) || !IsValidationError(err) {
		t.Fatalf("RetryTaskRunFromModelCall(model call 0) error = %v, want task validation ErrModelCallIndexRequired", err)
	}
	_, err = app.RetryTaskRunFromModelCall(ctx, task, run, RetryFromModelCallCommand{ModelCallIndex: 2})
	if err == nil || !IsValidationError(err) {
		t.Fatalf("RetryTaskRunFromModelCall(model call 2) error = %v, want out-of-range validation", err)
	}
}

func TestTaskApplication_ResumeRaisesBudgetBeforeRunner(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	runner := &recordingTaskApplicationRunner{}
	app := newTestTaskApplication(store, runner)
	task := types.Task{
		ID:              "task_budget",
		Title:           "budget",
		Status:          "failed",
		BudgetMicrosUSD: 100,
		LatestRunID:     "run_failed",
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}
	run := types.TaskRun{ID: task.LatestRunID, TaskID: task.ID, Status: "failed"}
	createTaskForAppTest(t, ctx, store, task)
	createRunForAppTest(t, ctx, store, run)

	_, err := app.ResumeTaskRun(ctx, task, run, ResumeCommand{
		Reason:          "raise ceiling",
		BudgetMicrosUSD: 250,
	})
	if err != nil {
		t.Fatalf("ResumeTaskRun() error = %v", err)
	}
	if runner.resumeCalls != 1 {
		t.Fatalf("resume calls = %d, want 1", runner.resumeCalls)
	}
	if runner.resumeTask.BudgetMicrosUSD != 250 {
		t.Fatalf("runner task budget = %d, want 250", runner.resumeTask.BudgetMicrosUSD)
	}
	if runner.resumeReason != "raise ceiling" {
		t.Fatalf("resume reason = %q, want raise ceiling", runner.resumeReason)
	}
	if runner.resumeBudget != 250 {
		t.Fatalf("runner budget command = %d, want 250", runner.resumeBudget)
	}
}

func TestTaskApplication_ResumeRejectsLowerBudgetBeforeRunner(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	runner := &recordingTaskApplicationRunner{}
	app := newTestTaskApplication(store, runner)
	task := createTaskForAppTest(t, ctx, store, types.Task{
		ID:              "task_budget_lower",
		Status:          "failed",
		BudgetMicrosUSD: 250,
		LatestRunID:     "run_failed",
	})
	run := createRunForAppTest(t, ctx, store, types.TaskRun{ID: task.LatestRunID, TaskID: task.ID, Status: "failed"})

	_, err := app.ResumeTaskRun(ctx, task, run, ResumeCommand{BudgetMicrosUSD: 100})
	if !errors.Is(err, ErrBudgetLower) {
		t.Fatalf("ResumeTaskRun(lower budget) error = %v, want ErrBudgetLower", err)
	}
	if runner.resumeCalls != 0 {
		t.Fatalf("resume calls = %d, want 0", runner.resumeCalls)
	}
}

func TestTaskApplication_DeleteRejectsStaleSummaryWhenLatestRunActive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	app := newTestTaskApplication(store, nil)
	task := types.Task{
		ID:          "task_stale",
		Title:       "stale",
		Status:      "completed",
		LatestRunID: "run_active",
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	createTaskForAppTest(t, ctx, store, task)
	createRunForAppTest(t, ctx, store, types.TaskRun{ID: task.LatestRunID, TaskID: task.ID, Status: "running"})

	err := app.DeleteTask(ctx, task.ID)
	if !errors.Is(err, ErrDeleteActiveRun) {
		t.Fatalf("DeleteTask() error = %v, want ErrDeleteActiveRun", err)
	}
	if _, found, err := store.GetTask(ctx, task.ID); err != nil || !found {
		t.Fatalf("task after delete attempt: found=%t err=%v, want still present", found, err)
	}
}

func TestTaskApplication_ResolveApprovalEnrichesRequest(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	runner := &recordingTaskApplicationRunner{}
	app := newTestTaskApplication(taskstate.NewMemoryStore(), runner)
	task := types.Task{ID: "task_approval"}

	_, err := app.ResolveTaskApproval(ctx, ResolveApprovalCommand{
		Task:       task,
		ApprovalID: "approval_1",
		Decision:   "approve",
		Note:       "looks good",
		RequestID:  "req_1",
	})
	if err != nil {
		t.Fatalf("ResolveTaskApproval() error = %v", err)
	}
	if runner.resolveCalls != 1 {
		t.Fatalf("resolve calls = %d, want 1", runner.resolveCalls)
	}
	if runner.resolveReq.Task.ID != task.ID || runner.resolveReq.ApprovalID != "approval_1" {
		t.Fatalf("resolve task/approval = %q/%q, want task_approval/approval_1", runner.resolveReq.Task.ID, runner.resolveReq.ApprovalID)
	}
	if runner.resolveReq.ResolvedBy != "operator" {
		t.Fatalf("resolved_by = %q, want operator", runner.resolveReq.ResolvedBy)
	}
	if runner.resolveReq.RequestID != "req_1" {
		t.Fatalf("request_id = %q, want req_1", runner.resolveReq.RequestID)
	}
	if runner.resolveReq.IDGenerator == nil {
		t.Fatal("IDGenerator is nil, want app default")
	}
	if got := runner.resolveReq.IDGenerator("run"); got != "run_fixed" {
		t.Fatalf("IDGenerator(run) = %q, want run_fixed", got)
	}
}
