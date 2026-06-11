package api

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

	continueCalls  int
	continuePrompt string

	retryFromTurnCalls int
	retryFromTurn      int

	cancelCalls  int
	cancelRunID  string
	cancelReason string

	resolveCalls int
	resolveReq   orchestrator.ResolveApprovalRequest
}

func (r *recordingTaskApplicationRunner) StartTask(_ context.Context, task types.Task, _ func(string) string) (*orchestrator.StartTaskResult, error) {
	r.startCalls++
	r.startTask = task
	run := types.TaskRun{ID: "run_started", TaskID: task.ID, Status: "queued"}
	return &orchestrator.StartTaskResult{Task: task, Run: run}, nil
}

func (r *recordingTaskApplicationRunner) ResumeTask(_ context.Context, task types.Task, run types.TaskRun, reason string, _ func(string) string) (*orchestrator.StartTaskResult, error) {
	r.resumeCalls++
	r.resumeTask = task
	r.resumeRun = run
	r.resumeReason = reason
	resumed := types.TaskRun{ID: "run_resumed", TaskID: task.ID, Status: "queued"}
	return &orchestrator.StartTaskResult{Task: task, Run: resumed}, nil
}

func (r *recordingTaskApplicationRunner) ContinueAgentTask(_ context.Context, task types.Task, run types.TaskRun, prompt string, _ func(string) string) (*orchestrator.StartTaskResult, error) {
	r.continueCalls++
	r.continuePrompt = prompt
	continued := types.TaskRun{ID: "run_continued", TaskID: task.ID, Status: "queued"}
	return &orchestrator.StartTaskResult{Task: task, Run: continued}, nil
}

func (r *recordingTaskApplicationRunner) RetryTaskFromTurn(_ context.Context, task types.Task, run types.TaskRun, turn int, _ string, _ func(string) string) (*orchestrator.StartTaskResult, error) {
	r.retryFromTurnCalls++
	r.retryFromTurn = turn
	retried := types.TaskRun{ID: "run_turn_retry", TaskID: task.ID, Status: "queued"}
	return &orchestrator.StartTaskResult{Task: task, Run: retried}, nil
}

func (r *recordingTaskApplicationRunner) CancelRun(_ context.Context, task types.Task, runID string, reason string) (types.TaskRun, error) {
	r.cancelCalls++
	r.cancelRunID = runID
	r.cancelReason = reason
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
	return r.startCalls + r.resumeCalls + r.continueCalls + r.retryFromTurnCalls + r.cancelCalls + r.resolveCalls
}

func newTestTaskApplication(store taskstate.Store, runner taskApplicationRunner) *taskApplication {
	return newTestTaskApplicationWithProjects(store, runner, nil)
}

func newTestTaskApplicationWithProjects(store taskstate.Store, runner taskApplicationRunner, projectStore projects.Store) *taskApplication {
	return newTaskApplication(taskApplicationOptions{
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

	task, err := app.CreateTask(ctx, taskCreateCommand{
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

	_, err := app.CreateTask(ctx, taskCreateCommand{
		Prompt:    "Use project context",
		ProjectID: "proj_missing_store",
	})
	if !errors.Is(err, errTaskProjectStoreNotConfigured) {
		t.Fatalf("CreateTask(project without store) error = %v, want errTaskProjectStoreNotConfigured", err)
	}

	projectStore := projects.NewMemoryStore()
	app = newTestTaskApplicationWithProjects(store, nil, projectStore)
	_, err = app.CreateTask(ctx, taskCreateCommand{
		Prompt:    "Use project context",
		ProjectID: "proj_missing",
	})
	if !errors.Is(err, errTaskProjectNotFound) {
		t.Fatalf("CreateTask(missing project) error = %v, want errTaskProjectNotFound", err)
	}

	if _, err := projectStore.Create(ctx, projects.Project{ID: "proj_1", Name: "Project One"}); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	task, err := app.CreateTask(ctx, taskCreateCommand{
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

	if _, err := app.CreateTask(ctx, taskCreateCommand{}); !errors.Is(err, errTaskPromptRequired) {
		t.Fatalf("CreateTask(agent_loop without prompt) error = %v, want errTaskPromptRequired", err)
	}

	task, err := app.CreateTask(ctx, taskCreateCommand{
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
	app := newTaskApplication(taskApplicationOptions{})
	if _, err := app.CreateTask(ctx, taskCreateCommand{Prompt: "x"}); !errors.Is(err, errTaskStoreNotConfigured) {
		t.Fatalf("CreateTask(nil store) error = %v, want errTaskStoreNotConfigured", err)
	}
	if _, err := app.ListTasks(ctx, taskstate.TaskFilter{}); !errors.Is(err, errTaskStoreNotConfigured) {
		t.Fatalf("ListTasks(nil store) error = %v, want errTaskStoreNotConfigured", err)
	}
	if _, err := app.LoadTask(ctx, "task"); !errors.Is(err, errTaskStoreNotConfigured) {
		t.Fatalf("LoadTask(nil store) error = %v, want errTaskStoreNotConfigured", err)
	}
	if _, err := app.LoadTaskRun(ctx, types.Task{ID: "task"}, "run"); !errors.Is(err, errTaskStoreNotConfigured) {
		t.Fatalf("LoadTaskRun(nil store) error = %v, want errTaskStoreNotConfigured", err)
	}
	if _, err := app.GetTaskApproval(ctx, types.Task{ID: "task"}, "approval"); !errors.Is(err, errTaskStoreNotConfigured) {
		t.Fatalf("GetTaskApproval(nil store) error = %v, want errTaskStoreNotConfigured", err)
	}
	if err := app.RequireRunner(); !errors.Is(err, errTaskStoreNotConfigured) {
		t.Fatalf("RequireRunner(nil store) error = %v, want errTaskStoreNotConfigured", err)
	}

	app = newTestTaskApplication(taskstate.NewMemoryStore(), nil)
	if err := app.RequireRunner(); !errors.Is(err, errTaskRunnerNotConfigured) {
		t.Fatalf("RequireRunner(nil runner) error = %v, want errTaskRunnerNotConfigured", err)
	}
	if _, err := app.StartTask(ctx, types.Task{ID: "task"}); !errors.Is(err, errTaskRunnerNotConfigured) {
		t.Fatalf("StartTask(nil runner) error = %v, want errTaskRunnerNotConfigured", err)
	}
	if _, err := app.ResolveTaskApproval(ctx, taskResolveApprovalCommand{}); !errors.Is(err, errTaskRunnerNotConfigured) {
		t.Fatalf("ResolveTaskApproval(nil runner) error = %v, want errTaskRunnerNotConfigured", err)
	}
}

func TestTaskApplication_LoadNotFoundErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	app := newTestTaskApplication(store, nil)
	task := createTaskForAppTest(t, ctx, store, types.Task{ID: "task_found"})

	if _, err := app.LoadTask(ctx, "missing"); !errors.Is(err, errTaskNotFound) {
		t.Fatalf("LoadTask(missing) error = %v, want errTaskNotFound", err)
	}
	if _, err := app.LoadTask(ctx, " "); !errors.Is(err, errTaskIDRequired) || !isTaskValidationError(err) {
		t.Fatalf("LoadTask(empty) error = %v, want task validation errTaskIDRequired", err)
	}
	if _, err := app.LoadTaskRun(ctx, task, "missing"); !errors.Is(err, errTaskRunNotFound) {
		t.Fatalf("LoadTaskRun(missing) error = %v, want errTaskRunNotFound", err)
	}
	if _, err := app.LoadTaskRun(ctx, task, " "); !errors.Is(err, errTaskRunIDRequired) || !isTaskValidationError(err) {
		t.Fatalf("LoadTaskRun(empty) error = %v, want task validation errTaskRunIDRequired", err)
	}
	if _, err := app.GetTaskApproval(ctx, task, "missing"); !errors.Is(err, errTaskApprovalNotFound) {
		t.Fatalf("GetTaskApproval(missing) error = %v, want errTaskApprovalNotFound", err)
	}
	if _, err := app.GetTaskApproval(ctx, task, " "); !errors.Is(err, errTaskApprovalIDRequired) || !isTaskValidationError(err) {
		t.Fatalf("GetTaskApproval(empty) error = %v, want task validation errTaskApprovalIDRequired", err)
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
	if !errors.Is(err, errTaskHasActiveRun) {
		t.Fatalf("StartTask() error = %v, want errTaskHasActiveRun", err)
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

func TestTaskApplication_LifecycleRejectsOtherActiveRunBeforeRunner(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		call func(context.Context, *taskApplication, types.Task, types.TaskRun) error
	}{
		{
			name: "retry",
			call: func(ctx context.Context, app *taskApplication, task types.Task, run types.TaskRun) error {
				_, err := app.RetryTaskRun(ctx, task, run)
				return err
			},
		},
		{
			name: "resume",
			call: func(ctx context.Context, app *taskApplication, task types.Task, run types.TaskRun) error {
				_, err := app.ResumeTaskRun(ctx, task, run, taskResumeCommand{Reason: "try again"})
				return err
			},
		},
		{
			name: "continue",
			call: func(ctx context.Context, app *taskApplication, task types.Task, run types.TaskRun) error {
				_, err := app.ContinueTaskRun(ctx, task, run, "continue")
				return err
			},
		},
		{
			name: "retry_from_turn",
			call: func(ctx context.Context, app *taskApplication, task types.Task, run types.TaskRun) error {
				_, err := app.RetryTaskRunFromTurn(ctx, task, run, taskRetryFromTurnCommand{Turn: 1, Reason: "rewind"})
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
			run := createRunForAppTest(t, ctx, store, types.TaskRun{ID: "run_source", TaskID: task.ID, Status: "failed"})

			err := tc.call(ctx, app, task, run)
			if !errors.Is(err, errTaskHasOtherActiveRun) {
				t.Fatalf("%s error = %v, want errTaskHasOtherActiveRun", tc.name, err)
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
		call func(context.Context, *taskApplication, types.Task, types.TaskRun) error
		want error
	}{
		{
			name: "retry_nonterminal",
			call: func(ctx context.Context, app *taskApplication, task types.Task, run types.TaskRun) error {
				_, err := app.RetryTaskRun(ctx, task, run)
				return err
			},
			want: errTaskRunNotRetryable,
		},
		{
			name: "resume_nonterminal",
			call: func(ctx context.Context, app *taskApplication, task types.Task, run types.TaskRun) error {
				_, err := app.ResumeTaskRun(ctx, task, run, taskResumeCommand{Reason: "try again"})
				return err
			},
			want: errTaskRunNotResumable,
		},
		{
			name: "turn_retry_nonterminal",
			call: func(ctx context.Context, app *taskApplication, task types.Task, run types.TaskRun) error {
				_, err := app.RetryTaskRunFromTurn(ctx, task, run, taskRetryFromTurnCommand{Turn: 1})
				return err
			},
			want: errTaskRunNotTurnRetryable,
		},
		{
			name: "resume_other_active_before_lower_budget",
			call: func(ctx context.Context, app *taskApplication, task types.Task, run types.TaskRun) error {
				run.Status = "failed"
				task.BudgetMicrosUSD = 500
				_, err := app.ResumeTaskRun(ctx, task, run, taskResumeCommand{BudgetMicrosUSD: 100})
				return err
			},
			want: errTaskHasOtherActiveRun,
		},
		{
			name: "continue_other_active",
			call: func(ctx context.Context, app *taskApplication, task types.Task, run types.TaskRun) error {
				_, err := app.ContinueTaskRun(ctx, task, run, "continue")
				return err
			},
			want: errTaskHasOtherActiveRun,
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

	_, err := app.ResumeTaskRun(ctx, task, run, taskResumeCommand{BudgetMicrosUSD: 100})
	if !errors.Is(err, errTaskBudgetLower) {
		t.Fatalf("ResumeTaskRun(lower budget, nil runner) error = %v, want errTaskBudgetLower", err)
	}
}

func TestTaskApplication_RetryFromTurnValidatesTurnBeforeRunner(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	app := newTestTaskApplication(store, nil)
	task := createTaskForAppTest(t, ctx, store, types.Task{ID: "task_turn", Status: "failed"})
	run := createRunForAppTest(t, ctx, store, types.TaskRun{ID: "run_failed", TaskID: task.ID, Status: "failed"})

	_, err := app.RetryTaskRunFromTurn(ctx, task, run, taskRetryFromTurnCommand{Turn: 0})
	if !errors.Is(err, errTaskTurnRequired) || !isTaskValidationError(err) {
		t.Fatalf("RetryTaskRunFromTurn(turn 0) error = %v, want task validation errTaskTurnRequired", err)
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

	_, err := app.ResumeTaskRun(ctx, task, run, taskResumeCommand{
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
	persisted, found, err := store.GetTask(ctx, task.ID)
	if err != nil || !found {
		t.Fatalf("GetTask: found=%t err=%v", found, err)
	}
	if persisted.BudgetMicrosUSD != 250 {
		t.Fatalf("persisted budget = %d, want 250", persisted.BudgetMicrosUSD)
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

	_, err := app.ResumeTaskRun(ctx, task, run, taskResumeCommand{BudgetMicrosUSD: 100})
	if !errors.Is(err, errTaskBudgetLower) {
		t.Fatalf("ResumeTaskRun(lower budget) error = %v, want errTaskBudgetLower", err)
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
	if !errors.Is(err, errTaskDeleteActiveRun) {
		t.Fatalf("DeleteTask() error = %v, want errTaskDeleteActiveRun", err)
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

	_, err := app.ResolveTaskApproval(ctx, taskResolveApprovalCommand{
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
