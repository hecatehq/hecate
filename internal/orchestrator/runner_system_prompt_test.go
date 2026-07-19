package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/internal/taskworkflow"
	"github.com/hecatehq/hecate/pkg/types"
)

type capturingSystemPromptExecutor struct {
	spec ExecutionSpec
}

func (e *capturingSystemPromptExecutor) Execute(_ context.Context, spec ExecutionSpec) (*ExecutionResult, error) {
	e.spec = spec
	return &ExecutionResult{Status: "completed"}, nil
}

func TestRunnerExecuteRunHonorsWorkspaceSystemPromptPolicy(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		policy        string
		wantWorkspace bool
	}{
		{
			name:          "inherit includes workspace layer",
			wantWorkspace: true,
		},
		{
			name:   "exclude skips workspace layer",
			policy: types.WorkspaceSystemPromptExclude,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			store := taskstate.NewMemoryStore()
			exec := &capturingSystemPromptExecutor{}
			runner := &Runner{
				store: store,
				agent: exec,
			}
			runner.SetSystemPromptResolver(func(_ context.Context, _, perTaskPrompt, workspacePath string) string {
				if workspacePath != "" {
					return "workspace=" + workspacePath + "\n" + perTaskPrompt
				}
				return perTaskPrompt
			})
			task := types.Task{
				ID:                          "task_" + strings.ReplaceAll(tc.name, " ", "_"),
				ExecutionKind:               "agent_loop",
				SystemPrompt:                "Per-task prompt.",
				WorkspaceSystemPromptPolicy: tc.policy,
				Status:                      "running",
				CreatedAt:                   time.Now().UTC(),
				UpdatedAt:                   time.Now().UTC(),
			}
			if _, err := store.CreateTask(ctx, task); err != nil {
				t.Fatalf("CreateTask: %v", err)
			}
			run := types.TaskRun{
				ID:            "run_" + strings.ReplaceAll(tc.name, " ", "_"),
				TaskID:        task.ID,
				Number:        1,
				Status:        "running",
				WorkspacePath: "/workspace/project",
				StartedAt:     time.Now().UTC(),
			}
			if _, err := store.CreateRun(ctx, run); err != nil {
				t.Fatalf("CreateRun: %v", err)
			}
			if _, err := runner.executeRun(ctx, profiler.NewTrace("req_"+task.ID, nil), task, run, "req_"+task.ID, nil); err != nil {
				t.Fatalf("executeRun: %v", err)
			}
			hasWorkspace := strings.Contains(exec.spec.SystemPrompt, "workspace=/workspace/project")
			if hasWorkspace != tc.wantWorkspace {
				t.Fatalf("system prompt = %q, workspace layer present=%v want %v", exec.spec.SystemPrompt, hasWorkspace, tc.wantWorkspace)
			}
			if !strings.Contains(exec.spec.SystemPrompt, "Per-task prompt.") {
				t.Fatalf("system prompt = %q, want per-task prompt preserved", exec.spec.SystemPrompt)
			}
		})
	}
}

func TestRunnerExecuteRunQAExcludesWorkspacePromptFromSystemInstruction(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	store := taskstate.NewMemoryStore()
	exec := &capturingSystemPromptExecutor{}
	runner := &Runner{
		store:      store,
		agent:      exec,
		workspaces: NewWorkspaceManager(t.TempDir()),
	}
	runner.SetSystemPromptResolver(func(_ context.Context, _, perTaskPrompt, workspacePath string) string {
		if workspacePath != "" {
			return "REPOSITORY-INSTRUCTION\n" + perTaskPrompt
		}
		return perTaskPrompt
	})
	now := time.Now().UTC()
	task := types.Task{
		ID:                          "task-qa-system-prompt",
		ExecutionKind:               "agent_loop",
		WorkflowMode:                types.WorkflowModeQA,
		WorkflowVersion:             taskworkflow.QAVersion,
		WorkspaceMode:               "ephemeral",
		SandboxReadOnly:             true,
		WorkspaceSystemPromptPolicy: types.WorkspaceSystemPromptExclude,
		SystemPrompt:                "QA task instruction.",
		Status:                      "running",
		CreatedAt:                   now,
		UpdatedAt:                   now,
	}
	run := types.TaskRun{
		ID:              "run-qa-system-prompt",
		TaskID:          task.ID,
		Number:          1,
		Status:          "running",
		WorkflowMode:    types.WorkflowModeQA,
		WorkflowVersion: taskworkflow.QAVersion,
		StartedAt:       now,
	}
	_, workspacePath, _, err := runner.workspaces.plannedWorkspacePath(task.ID, run.ID)
	if err != nil {
		t.Fatalf("plannedWorkspacePath: %v", err)
	}
	if err := os.MkdirAll(filepath.Clean(workspacePath), 0o755); err != nil {
		t.Fatalf("create managed QA workspace: %v", err)
	}
	run.WorkspacePath = workspacePath
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	trace := profiler.NewTrace("req-qa-system-prompt", nil)
	defer trace.Finalize()
	if _, err := runner.executeRun(ctx, trace, task, run, "req-qa-system-prompt", nil); err != nil {
		t.Fatalf("executeRun: %v", err)
	}
	if strings.Contains(exec.spec.SystemPrompt, "REPOSITORY-INSTRUCTION") {
		t.Fatalf("QA system prompt inherited workspace instruction: %q", exec.spec.SystemPrompt)
	}
	if !strings.Contains(exec.spec.SystemPrompt, "QA task instruction.") {
		t.Fatalf("QA system prompt = %q, want task instruction", exec.spec.SystemPrompt)
	}
}
