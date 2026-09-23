package api

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/config"
)

func TestHandleCreateTaskFreezesStandaloneAgentPreset(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, nil, config.Config{}, nil)
	if _, err := apiHandler.agentProfiles.Create(t.Context(), agentprofiles.Profile{
		ID:                         "standalone_review",
		Name:                       "Standalone review",
		Instructions:               "Inspect before changing files.",
		Surface:                    agentprofiles.SurfaceHecateTask,
		ProviderHint:               "hint-provider",
		ModelHint:                  "hint-model",
		ExecutionProfile:           "repo_local",
		ToolsEnabled:               true,
		WritesAllowed:              false,
		NetworkAllowed:             false,
		BrowserAllowed:             true,
		BrowserInteractionsAllowed: true,
		BrowserAllowedOrigins:      []string{"https://app.example.test"},
		ApprovalPolicy:             agentprofiles.ApprovalRequire,
	}); err != nil {
		t.Fatalf("Create preset: %v", err)
	}

	tasks := newTaskTestClient(t, NewServer(logger, apiHandler))
	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/hecate/v1/tasks", `{
		"prompt":"Review the workspace",
		"execution_kind":"agent_loop",
		"execution_profile":"caller_override",
		"agent_preset_id":"standalone_review",
		"requested_provider":"explicit-provider",
		"requested_model":"explicit-model",
		"system_prompt":"Focus on regressions.",
		"sandbox_read_only":false,
		"sandbox_network":true,
		"agent_preset_tools_enabled":false,
		"agent_preset_approval_policy":"allow",
		"agent_preset_browser_allowed":false,
		"agent_preset_browser_interactions_allowed":false,
		"agent_preset_browser_allowed_origins":["https://forged.example.test"],
		"origin_kind":"chat"
	}`)
	if created.Data.AgentPresetID != "standalone_review" || created.Data.AgentPresetToolsEnabled == nil || !*created.Data.AgentPresetToolsEnabled {
		t.Fatalf("preset identity/tools snapshot = %q/%v", created.Data.AgentPresetID, created.Data.AgentPresetToolsEnabled)
	}
	if created.Data.AgentPresetApprovalPolicy != agentprofiles.ApprovalRequire || created.Data.AgentPresetBrowserAllowed == nil || !*created.Data.AgentPresetBrowserAllowed || created.Data.AgentPresetBrowserInteractionsAllowed == nil || !*created.Data.AgentPresetBrowserInteractionsAllowed {
		t.Fatalf("preset approval/browser snapshot = approval %q browser %v interactions %v", created.Data.AgentPresetApprovalPolicy, created.Data.AgentPresetBrowserAllowed, created.Data.AgentPresetBrowserInteractionsAllowed)
	}
	if created.Data.OriginKind != "" {
		t.Fatalf("origin_kind = %q, want output-only input ignored", created.Data.OriginKind)
	}
	if created.Data.ExecutionProfile != "repo_local" || !created.Data.SandboxReadOnly || created.Data.SandboxNetwork {
		t.Fatalf("execution posture = profile %q read_only:%t network:%t", created.Data.ExecutionProfile, created.Data.SandboxReadOnly, created.Data.SandboxNetwork)
	}
	if created.Data.RequestedProvider != "explicit-provider" || created.Data.RequestedModel != "explicit-model" {
		t.Fatalf("explicit route = %q/%q", created.Data.RequestedProvider, created.Data.RequestedModel)
	}
	if created.Data.SystemPrompt != "Work policy instructions:\nInspect before changing files.\n\nTask instructions:\nFocus on regressions." {
		t.Fatalf("system_prompt = %q", created.Data.SystemPrompt)
	}
	if len(created.Data.AgentPresetBrowserAllowedOrigins) != 1 || created.Data.AgentPresetBrowserAllowedOrigins[0] != "https://app.example.test" {
		t.Fatalf("browser origins = %v", created.Data.AgentPresetBrowserAllowedOrigins)
	}
}

func TestHandleCreateTaskRejectsUnavailableStandaloneAgentPreset(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, nil, config.Config{}, nil)
	if _, err := apiHandler.agentProfiles.Create(t.Context(), agentprofiles.Profile{
		ID:           "chat_only",
		Name:         "Chat only",
		Surface:      agentprofiles.SurfaceHecateChat,
		ToolsEnabled: true,
	}); err != nil {
		t.Fatalf("Create preset: %v", err)
	}
	tasks := newTaskTestClient(t, NewServer(logger, apiHandler))

	missing := tasks.mustRequestStatus(http.StatusNotFound, http.MethodPost, "/hecate/v1/tasks", `{"prompt":"work","execution_kind":"agent_loop","agent_preset_id":"missing"}`)
	if body := missing.Body.String(); !strings.Contains(body, "agent preset not found: missing") {
		t.Fatalf("missing preset response = %q", body)
	}
	incompatible := tasks.mustRequestStatus(http.StatusBadRequest, http.MethodPost, "/hecate/v1/tasks", `{"prompt":"work","execution_kind":"agent_loop","agent_preset_id":"chat_only"}`)
	if body := incompatible.Body.String(); !strings.Contains(body, "not available for Hecate Tasks") {
		t.Fatalf("incompatible preset response = %q", body)
	}
	qa := tasks.mustRequestStatus(http.StatusBadRequest, http.MethodPost, "/hecate/v1/tasks", `{"prompt":"inspect","execution_kind":"agent_loop","workflow_mode":"qa","agent_preset_id":"implementation"}`)
	if body := qa.Body.String(); !strings.Contains(body, "unavailable for workflow_mode=qa") {
		t.Fatalf("QA preset response = %q", body)
	}
}
