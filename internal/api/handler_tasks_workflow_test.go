package api

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestHandleCreateTaskQAWorkflowReturnsEnforcedRuntimePosture(t *testing.T) {
	t.Parallel()

	handler := newTestHTTPHandlerForProviders(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		nil,
		config.Config{},
	)
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](
		tasks,
		http.MethodPost,
		"/hecate/v1/tasks",
		`{"title":"QA report","prompt":"Inspect the workspace for regressions.","workflow_mode":"qa","execution_kind":"agent_loop"}`,
	)
	if created.Data.WorkflowMode != "qa" || created.Data.WorkflowVersion != "v0" {
		t.Fatalf("workflow = %q/%q, want qa/v0", created.Data.WorkflowMode, created.Data.WorkflowVersion)
	}
	if created.Data.ExecutionKind != "agent_loop" || created.Data.WorkspaceMode != "ephemeral" {
		t.Fatalf("execution/workspace = %q/%q, want agent_loop/ephemeral", created.Data.ExecutionKind, created.Data.WorkspaceMode)
	}
	if !created.Data.SandboxReadOnly || created.Data.SandboxNetwork {
		t.Fatalf("QA sandbox posture = read_only:%t network:%t, want true/false", created.Data.SandboxReadOnly, created.Data.SandboxNetwork)
	}
	if created.Data.WorkspaceSystemPromptPolicy != types.WorkspaceSystemPromptExclude {
		t.Fatalf("QA workspace system prompt policy = %q, want %q", created.Data.WorkspaceSystemPromptPolicy, types.WorkspaceSystemPromptExclude)
	}

	invalid := tasks.mustRequestStatus(
		http.StatusBadRequest,
		http.MethodPost,
		"/hecate/v1/tasks",
		`{"prompt":"Inspect","workflow_mode":"qa","execution_kind":"agent_loop","mcp_servers":[{"name":"docs","command":"fake"}]}`,
	)
	if got := invalid.Body.String(); got == "" || !strings.Contains(got, "mcp_servers") {
		t.Fatalf("QA MCP rejection = %q, want actionable mcp_servers diagnostic", got)
	}
}

func TestHandleStartTaskRejectsInvalidPersistedQAWorkflowAsValidation(t *testing.T) {
	t.Parallel()

	store := taskstate.NewMemoryStore()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestAPIHandlerWithSettingsAndTaskStore(logger, nil, config.Config{}, nil, store)
	tasks := newTaskTestClient(t, NewServer(logger, handler))
	now := time.Now().UTC()
	if _, err := store.CreateTask(t.Context(), types.Task{
		ID:                          "task-invalid-persisted-qa",
		Title:                       "Invalid persisted QA",
		Prompt:                      "inspect",
		Status:                      "queued",
		ExecutionKind:               "shell",
		WorkflowMode:                types.WorkflowModeQA,
		WorkflowVersion:             "v0",
		WorkspaceMode:               "ephemeral",
		SandboxReadOnly:             true,
		WorkspaceSystemPromptPolicy: types.WorkspaceSystemPromptExclude,
		CreatedAt:                   now,
		UpdatedAt:                   now,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	response := tasks.mustRequestStatus(
		http.StatusUnprocessableEntity,
		http.MethodPost,
		"/hecate/v1/tasks/task-invalid-persisted-qa/start",
		"",
	)
	if body := response.Body.String(); !strings.Contains(body, "invalid_request") || !strings.Contains(body, "requires execution_kind=agent_loop") {
		t.Fatalf("persisted QA start response = %q, want validation policy diagnostic", body)
	}
}

func TestTaskRunLifecycleRejectsInvalidPersistedQAWorkflowAsValidation(t *testing.T) {
	t.Parallel()

	store := taskstate.NewMemoryStore()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestAPIHandlerWithSettingsAndTaskStore(logger, nil, config.Config{}, nil, store)
	tasks := newTaskTestClient(t, NewServer(logger, handler))
	now := time.Now().UTC()
	task := types.Task{
		ID:                          "task-invalid-persisted-qa-lifecycle",
		Title:                       "Invalid persisted QA lifecycle",
		Prompt:                      "inspect",
		Status:                      "failed",
		LatestRunID:                 "run-invalid-persisted-qa-lifecycle",
		ExecutionKind:               "agent_loop",
		WorkflowMode:                types.WorkflowModeQA,
		WorkflowVersion:             "v0",
		WorkspaceMode:               "ephemeral",
		SandboxReadOnly:             true,
		WorkspaceSystemPromptPolicy: types.WorkspaceSystemPromptExclude,
		SandboxNetwork:              true,
		RequestedModel:              "test-model",
		CreatedAt:                   now,
		UpdatedAt:                   now,
	}
	if _, err := store.CreateTask(t.Context(), task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	run := types.TaskRun{
		ID:              task.LatestRunID,
		TaskID:          task.ID,
		Number:          1,
		Status:          "failed",
		WorkflowMode:    types.WorkflowModeQA,
		WorkflowVersion: "v0",
		ModelCallCount:  1,
		StartedAt:       now.Add(-time.Minute),
		FinishedAt:      now,
	}
	if _, err := store.CreateRun(t.Context(), run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := store.CreateArtifact(t.Context(), types.TaskArtifact{
		ID:          "conversation-invalid-persisted-qa-lifecycle",
		TaskID:      task.ID,
		RunID:       run.ID,
		Kind:        "agent_conversation",
		StorageKind: "inline",
		ContentText: `[{"role":"user","content":"inspect"},{"role":"assistant","content":"finding"}]`,
		Status:      "ready",
		CreatedAt:   now,
	}); err != nil {
		t.Fatalf("CreateArtifact(agent conversation): %v", err)
	}

	for _, tc := range []struct {
		name string
		path string
		body string
	}{
		{name: "retry", path: "/retry", body: `{}`},
		{name: "resume", path: "/resume", body: `{}`},
		{name: "continue", path: "/continue", body: `{"prompt":"continue QA"}`},
		{name: "retry_from_model_call", path: "/retry-from-model-call", body: `{"model_call_index":1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := tasks.mustRequestStatus(
				http.StatusUnprocessableEntity,
				http.MethodPost,
				"/hecate/v1/tasks/"+task.ID+"/runs/"+run.ID+tc.path,
				tc.body,
			)
			if body := response.Body.String(); !strings.Contains(body, "invalid_request") || !strings.Contains(body, "does not allow native network access") {
				t.Fatalf("persisted QA %s response = %q, want validation policy diagnostic", tc.name, body)
			}
		})
	}
}
