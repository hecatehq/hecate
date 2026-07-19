package orchestrator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/internal/taskworkflow"
	"github.com/hecatehq/hecate/pkg/types"
)

type workflowPolicyCountingExecutor struct {
	calls int
}

func (e *workflowPolicyCountingExecutor) Execute(context.Context, ExecutionSpec) (*ExecutionResult, error) {
	e.calls++
	return &ExecutionResult{Status: "completed"}, nil
}

func TestRunnerExecuteRunFailsClosedForPersistedQAExecutorKinds(t *testing.T) {
	t.Parallel()

	for _, executionKind := range []string{"shell", "file", "git"} {
		executionKind := executionKind
		t.Run(executionKind, func(t *testing.T) {
			executor := &workflowPolicyCountingExecutor{}
			runner := &Runner{
				exec:  executor,
				shell: executor,
				file:  executor,
				git:   executor,
			}
			task := types.Task{
				ID:                          "task-persisted-qa-" + executionKind,
				ExecutionKind:               executionKind,
				WorkflowMode:                types.WorkflowModeQA,
				WorkflowVersion:             taskworkflow.QAVersion,
				WorkspaceMode:               "ephemeral",
				SandboxReadOnly:             true,
				WorkspaceSystemPromptPolicy: types.WorkspaceSystemPromptExclude,
			}
			run := types.TaskRun{
				ID:              "run-persisted-qa-" + executionKind,
				TaskID:          task.ID,
				WorkflowMode:    types.WorkflowModeQA,
				WorkflowVersion: taskworkflow.QAVersion,
			}
			trace := profiler.NewTrace("request-persisted-qa-"+executionKind, nil)
			defer trace.Finalize()

			_, err := runner.executeRun(context.Background(), trace, task, run, "request-persisted-qa", nil)
			if !errors.Is(err, taskworkflow.ErrQARequiresAgentLoop) {
				t.Fatalf("executeRun error = %v, want QA agent-loop policy rejection", err)
			}
			if executor.calls != 0 {
				t.Fatalf("%s executor calls = %d, want 0", executionKind, executor.calls)
			}
		})
	}
}

func TestRunnerStartTaskRejectsInvalidPersistedQAWorkflowBeforeCreatingRun(t *testing.T) {
	t.Parallel()

	store := taskstate.NewMemoryStore()
	runner := NewRunner(slog.New(slog.NewJSONHandler(io.Discard, nil)), store, nil, Config{DeferQueueStart: true})
	task := types.Task{
		ID:                          "task-invalid-persisted-qa-start",
		Status:                      "queued",
		ExecutionKind:               "shell",
		WorkflowMode:                types.WorkflowModeQA,
		WorkflowVersion:             taskworkflow.QAVersion,
		WorkspaceMode:               "ephemeral",
		SandboxReadOnly:             true,
		WorkspaceSystemPromptPolicy: types.WorkspaceSystemPromptExclude,
	}
	if _, err := store.CreateTask(t.Context(), task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	_, err := runner.StartTask(t.Context(), task, func(prefix string) string { return prefix + "_invalid_qa" })
	if !errors.Is(err, taskworkflow.ErrQARequiresAgentLoop) {
		t.Fatalf("StartTask error = %v, want QA agent-loop policy rejection", err)
	}
	runs, err := store.ListRuns(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("runs = %+v, want none after rejected persisted QA task", runs)
	}
}

func TestRunnerStartTaskRejectsPersistedQAWorkspacePromptInheritance(t *testing.T) {
	t.Parallel()

	store := taskstate.NewMemoryStore()
	runner := NewRunner(slog.New(slog.NewJSONHandler(io.Discard, nil)), store, nil, Config{DeferQueueStart: true})
	task := types.Task{
		ID:              "task-invalid-persisted-qa-workspace-prompt",
		Status:          "queued",
		ExecutionKind:   "agent_loop",
		WorkflowMode:    types.WorkflowModeQA,
		WorkflowVersion: taskworkflow.QAVersion,
		WorkspaceMode:   "ephemeral",
		SandboxReadOnly: true,
		RequestedModel:  "test-model",
		// Empty inherits workspace CLAUDE.md/AGENTS.md and is intentionally
		// malformed for the QA contract.
	}
	if _, err := store.CreateTask(t.Context(), task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	_, err := runner.StartTask(t.Context(), task, func(prefix string) string { return prefix + "_invalid_qa_prompt" })
	if !errors.Is(err, taskworkflow.ErrQAWorkspaceSystemPrompt) {
		t.Fatalf("StartTask error = %v, want QA workspace-prompt policy rejection", err)
	}
	runs, err := store.ListRuns(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("runs = %+v, want none after rejected persisted QA workspace-prompt policy", runs)
	}
}

func TestRunnerQAWorkflowFollowupsReceiveFreshManagedWorkspace(t *testing.T) {
	for _, operation := range []string{"retry", "resume", "retry_from_model_call"} {
		operation := operation
		t.Run(operation, func(t *testing.T) {
			ctx := t.Context()
			store := taskstate.NewMemoryStore()
			runner := NewRunner(slog.New(slog.NewJSONHandler(io.Discard, nil)), store, nil, Config{DeferQueueStart: true})
			root := t.TempDir()
			runner.workspaces = NewWorkspaceManager(root)
			sourceWorkspace := t.TempDir()
			now := time.Now().UTC()
			task := types.Task{
				ID:                          "task-qa-followup-" + operation,
				Title:                       "QA follow-up",
				Prompt:                      "inspect",
				Status:                      "completed",
				ExecutionKind:               "agent_loop",
				WorkspaceMode:               "ephemeral",
				SandboxReadOnly:             true,
				WorkspaceSystemPromptPolicy: types.WorkspaceSystemPromptExclude,
				RequestedModel:              "test-model",
				WorkingDirectory:            sourceWorkspace,
				CreatedAt:                   now,
				UpdatedAt:                   now,
			}
			if operation != "retry" {
				task.WorkflowMode = types.WorkflowModeQA
				task.WorkflowVersion = taskworkflow.QAVersion
			}
			sourceRun := types.TaskRun{
				ID:              "run-qa-followup-source-" + operation,
				TaskID:          task.ID,
				Number:          1,
				Status:          "completed",
				WorkflowMode:    types.WorkflowModeQA,
				WorkflowVersion: taskworkflow.QAVersion,
				WorkspacePath:   sourceWorkspace,
				StartedAt:       now,
				FinishedAt:      now,
				ModelCallCount:  1,
			}
			if _, err := store.CreateTask(ctx, task); err != nil {
				t.Fatalf("CreateTask: %v", err)
			}
			if _, err := store.CreateRun(ctx, sourceRun); err != nil {
				t.Fatalf("CreateRun: %v", err)
			}
			if operation == "retry_from_model_call" {
				if _, err := store.CreateArtifact(ctx, types.TaskArtifact{
					ID:          "conversation-" + sourceRun.ID,
					TaskID:      task.ID,
					RunID:       sourceRun.ID,
					Kind:        "agent_conversation",
					StorageKind: "inline",
					ContentText: `[{"role":"user","content":"inspect"},{"role":"assistant","content":"finding"}]`,
					Status:      "ready",
					CreatedAt:   now,
				}); err != nil {
					t.Fatalf("CreateArtifact(agent conversation): %v", err)
				}
			}

			var result *StartTaskResult
			var err error
			switch operation {
			case "retry":
				result, err = runner.RetryTask(ctx, task, sourceRun, defaultResourceID)
			case "resume":
				result, err = runner.ResumeTask(ctx, task, sourceRun, "continue QA", defaultResourceID)
			case "retry_from_model_call":
				result, err = runner.RetryTaskFromModelCall(ctx, task, sourceRun, 1, "retry QA", defaultResourceID)
			}
			if err != nil {
				t.Fatalf("%s: %v", operation, err)
			}
			if result.Run.WorkspacePath == sourceWorkspace {
				t.Fatalf("%s reused source workspace %q, want fresh managed QA workspace", operation, sourceWorkspace)
			}
			if result.Run.WorkflowMode != types.WorkflowModeQA || result.Run.WorkflowVersion != taskworkflow.QAVersion {
				t.Fatalf("%s workflow snapshot = %q/%q, want qa/%s", operation, result.Run.WorkflowMode, result.Run.WorkflowVersion, taskworkflow.QAVersion)
			}
			managed, err := runner.workspaces.managedRunWorkspacePath(task, result.Run)
			if err != nil {
				t.Fatalf("managedRunWorkspacePath(%s): %v", operation, err)
			}
			if managed != result.Run.WorkspacePath {
				t.Fatalf("%s workspace = %q, want canonical managed %q", operation, result.Run.WorkspacePath, managed)
			}
		})
	}
}

func TestRunnerQAWorkflowFollowupsRejectNoncanonicalSourceVersion(t *testing.T) {
	t.Parallel()

	for _, source := range []struct {
		name    string
		mode    types.WorkflowMode
		version string
		want    error
	}{
		{name: "noncanonical_version", mode: types.WorkflowModeQA, version: " v0 ", want: taskworkflow.ErrQAWorkflowVersion},
		{name: "orphan_version", version: taskworkflow.QAVersion, want: taskworkflow.ErrInvalidWorkflowSnapshot},
	} {
		source := source
		for _, operation := range []string{"retry", "resume"} {
			operation := operation
			t.Run(source.name+"/"+operation, func(t *testing.T) {
				ctx := t.Context()
				store := taskstate.NewMemoryStore()
				runner := NewRunner(slog.New(slog.NewJSONHandler(io.Discard, nil)), store, nil, Config{DeferQueueStart: true})
				runner.workspaces = NewWorkspaceManager(t.TempDir())
				now := time.Now().UTC()
				task := types.Task{
					ID:                          "task-qa-noncanonical-followup-" + operation,
					Status:                      "completed",
					ExecutionKind:               "agent_loop",
					WorkflowMode:                types.WorkflowModeQA,
					WorkflowVersion:             taskworkflow.QAVersion,
					WorkspaceMode:               "ephemeral",
					SandboxReadOnly:             true,
					WorkspaceSystemPromptPolicy: types.WorkspaceSystemPromptExclude,
					RequestedModel:              "test-model",
					CreatedAt:                   now,
					UpdatedAt:                   now,
				}
				sourceRun := types.TaskRun{
					ID:              "run-qa-noncanonical-followup-" + operation,
					TaskID:          task.ID,
					Number:          1,
					Status:          "completed",
					WorkflowMode:    source.mode,
					WorkflowVersion: source.version,
					StartedAt:       now,
					FinishedAt:      now,
				}
				if _, err := store.CreateTask(ctx, task); err != nil {
					t.Fatalf("CreateTask: %v", err)
				}

				var err error
				switch operation {
				case "retry":
					_, err = runner.RetryTask(ctx, task, sourceRun, defaultResourceID)
				case "resume":
					_, err = runner.ResumeTask(ctx, task, sourceRun, "continue QA", defaultResourceID)
				}
				if !errors.Is(err, source.want) {
					t.Fatalf("%s error = %v, want source workflow policy rejection %v", operation, err, source.want)
				}
				runs, listErr := store.ListRuns(ctx, task.ID)
				if listErr != nil {
					t.Fatalf("ListRuns: %v", listErr)
				}
				if len(runs) != 0 {
					t.Fatalf("runs = %+v, want none after rejecting source version", runs)
				}
			})
		}
	}
}

func TestRunnerStartTaskRunInitializerCannotMutateQAExecutionContract(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	store := taskstate.NewMemoryStore()
	runner := NewRunner(slog.New(slog.NewJSONHandler(io.Discard, nil)), store, nil, Config{DeferQueueStart: true})
	runner.workspaces = NewWorkspaceManager(t.TempDir())
	task := types.Task{
		ID:                          "task-qa-initializer-contract",
		Title:                       "QA initializer contract",
		Prompt:                      "inspect",
		Status:                      "queued",
		ExecutionKind:               "agent_loop",
		WorkflowMode:                types.WorkflowModeQA,
		WorkflowVersion:             taskworkflow.QAVersion,
		WorkspaceMode:               "ephemeral",
		SandboxReadOnly:             true,
		WorkspaceSystemPromptPolicy: types.WorkspaceSystemPromptExclude,
		RequestedModel:              "test-model",
		WorkingDirectory:            t.TempDir(),
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	result, err := runner.StartTaskWithRunInitializer(ctx, task, defaultResourceID, func(run *types.TaskRun) {
		run.WorkflowMode = ""
		run.WorkflowVersion = "v1"
		run.WorkspacePath = t.TempDir()
		run.WorkspaceID = "unmanaged-workspace"
	})
	if err != nil {
		t.Fatalf("StartTaskWithRunInitializer: %v", err)
	}
	if result.Run.WorkflowMode != types.WorkflowModeQA || result.Run.WorkflowVersion != taskworkflow.QAVersion {
		t.Fatalf("run workflow = %q/%q, want qa/%s", result.Run.WorkflowMode, result.Run.WorkflowVersion, taskworkflow.QAVersion)
	}
	managed, err := runner.workspaces.managedRunWorkspacePath(task, result.Run)
	if err != nil {
		t.Fatalf("managedRunWorkspacePath: %v", err)
	}
	if result.Run.WorkspacePath != managed || result.Run.WorkspaceID != "workspace_"+task.ID {
		t.Fatalf("run workspace = %q/%q, want managed %q/workspace_%s", result.Run.WorkspacePath, result.Run.WorkspaceID, managed, task.ID)
	}
	stored, ok, err := store.GetRun(ctx, task.ID, result.Run.ID)
	if err != nil || !ok {
		t.Fatalf("GetRun: ok=%t err=%v", ok, err)
	}
	if stored.WorkflowMode != types.WorkflowModeQA || stored.WorkflowVersion != taskworkflow.QAVersion || stored.WorkspacePath != managed {
		t.Fatalf("stored QA run = %+v, want preserved workflow contract and managed workspace", stored)
	}
}

func TestRunnerExecuteRunRejectsUnknownPersistedWorkflowMode(t *testing.T) {
	t.Parallel()

	executor := &workflowPolicyCountingExecutor{}
	runner := &Runner{exec: executor, agent: executor}
	task := types.Task{ID: "task-unknown-workflow", ExecutionKind: "agent_loop"}
	run := types.TaskRun{ID: "run-unknown-workflow", TaskID: task.ID, WorkflowMode: "review"}
	trace := profiler.NewTrace("request-unknown-workflow", nil)
	defer trace.Finalize()

	_, err := runner.executeRun(context.Background(), trace, task, run, "request-unknown-workflow", nil)
	if !errors.Is(err, taskworkflow.ErrUnsupportedWorkflowMode) {
		t.Fatalf("executeRun error = %v, want unsupported workflow policy rejection", err)
	}
	if executor.calls != 0 {
		t.Fatalf("executor calls = %d, want 0", executor.calls)
	}
}

func TestRunnerExecuteRunRejectsArbitraryPersistedQAWorkspaceBeforeExecutor(t *testing.T) {
	t.Parallel()

	executor := &workflowPolicyCountingExecutor{}
	runner := &Runner{
		exec:       executor,
		agent:      executor,
		workspaces: NewWorkspaceManager(t.TempDir()),
	}
	task := types.Task{
		ID:                          "task-arbitrary-qa-workspace",
		ExecutionKind:               "agent_loop",
		WorkflowMode:                types.WorkflowModeQA,
		WorkflowVersion:             taskworkflow.QAVersion,
		WorkspaceMode:               "ephemeral",
		SandboxReadOnly:             true,
		WorkspaceSystemPromptPolicy: types.WorkspaceSystemPromptExclude,
	}
	run := types.TaskRun{
		ID:              "run-arbitrary-qa-workspace",
		TaskID:          task.ID,
		WorkflowMode:    types.WorkflowModeQA,
		WorkflowVersion: taskworkflow.QAVersion,
		WorkspacePath:   t.TempDir(),
	}
	trace := profiler.NewTrace("request-arbitrary-qa-workspace", nil)
	defer trace.Finalize()

	_, err := runner.executeRun(t.Context(), trace, task, run, "request-arbitrary-qa-workspace", nil)
	if !errors.Is(err, taskworkflow.ErrQAWorkspaceProvenance) {
		t.Fatalf("executeRun error = %v, want managed-workspace policy rejection", err)
	}
	if executor.calls != 0 {
		t.Fatalf("agent executor calls = %d, want 0", executor.calls)
	}
}
