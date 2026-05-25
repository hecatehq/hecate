package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/controlplane"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestSystemResetDataDeletesStateAndClosesAgentSessions(t *testing.T) {
	t.Parallel()
	logger := quietLogger()
	cpStore := controlplane.NewMemoryStore()
	runtime := &fakeProviderRuntime{store: cpStore}
	handler := NewHandler(config.Config{}, logger, nil, cpStore, taskstate.NewMemoryStore(), nil, runtime)
	handler.SetProjectStore(projects.NewMemoryStore())
	chatStore := chat.NewMemoryStore()
	handler.SetAgentChatStore(chatStore)
	runner := &fakeAgentChatRunner{}
	handler.SetAgentChatRunner(runner)
	server := NewServer(logger, handler)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{"name":"Project reset"}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create project status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var project ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &project); err != nil {
		t.Fatalf("decode project response: %v", err)
	}

	ctx := t.Context()
	if _, err := chatStore.Create(ctx, chat.Session{
		ID:              "chat_project_external",
		Title:           "Project external chat",
		ProjectID:       project.Data.ID,
		AgentID:         "codex",
		DriverKind:      "acp",
		NativeSessionID: "native_project_external",
	}); err != nil {
		t.Fatalf("create project chat: %v", err)
	}
	if _, err := chatStore.Create(ctx, chat.Session{
		ID:              "chat_free_external",
		Title:           "Project-free external chat",
		AgentID:         "claude_code",
		DriverKind:      "acp",
		NativeSessionID: "native_free_external",
	}); err != nil {
		t.Fatalf("create project-free chat: %v", err)
	}
	if _, err := handler.taskStore.CreateTask(ctx, types.Task{ID: "task_reset", Title: "Reset me", Status: "queued"}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := cpStore.UpsertProvider(ctx, controlplane.Provider{
		ID:       "openai",
		Name:     "OpenAI",
		Kind:     "cloud",
		Protocol: "openai",
		BaseURL:  "https://api.openai.com/v1",
		Enabled:  true,
	}, nil); err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if _, err := cpStore.UpsertPolicyRule(ctx, config.PolicyRuleConfig{
		ID:     "block-expensive",
		Action: "deny",
		Reason: "test",
		Models: []string{"gpt-4.5"},
	}); err != nil {
		t.Fatalf("create policy rule: %v", err)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/system/reset-data", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("reset status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var reset SystemResetDataResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &reset); err != nil {
		t.Fatalf("decode reset response: %v", err)
	}
	if reset.Data.ProjectsDeleted != 1 || reset.Data.ChatSessionsDeleted != 2 || reset.Data.TasksDeleted != 1 || reset.Data.ProvidersDeleted != 1 || reset.Data.PolicyRulesDeleted != 1 {
		t.Fatalf("reset stats = %+v, want one project, task, provider, rule and two chats", reset.Data)
	}
	if len(runner.closedSessions) != 2 {
		t.Fatalf("closed sessions = %#v, want two external chats closed", runner.closedSessions)
	}

	chats, err := chatStore.List(ctx)
	if err != nil {
		t.Fatalf("list chats: %v", err)
	}
	if len(chats) != 0 {
		t.Fatalf("chats after reset = %#v, want none", chats)
	}
	projectList, err := handler.projects.List(ctx)
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	if len(projectList) != 0 {
		t.Fatalf("projects after reset = %#v, want none", projectList)
	}
	tasks, err := handler.taskStore.ListTasks(ctx, taskstate.TaskFilter{})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks after reset = %#v, want none", tasks)
	}
	settings, err := cpStore.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot settings: %v", err)
	}
	if len(settings.Providers) != 0 || len(settings.PolicyRules) != 0 {
		t.Fatalf("settings after reset = providers=%#v rules=%#v, want none", settings.Providers, settings.PolicyRules)
	}
}
