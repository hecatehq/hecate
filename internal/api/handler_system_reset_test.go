package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/controlplane"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/pluginregistry"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/storage"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestSystemResetDataMemoryBackendDeletesStateAndClosesAgentSessions(t *testing.T) {
	t.Parallel()
	logger := quietLogger()
	cpStore := controlplane.NewMemoryStore()
	runtime := &fakeProviderRuntime{store: cpStore}
	handler := NewHandler(config.Config{}, logger, nil, cpStore, taskstate.NewMemoryStore(), nil, runtime)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetPluginRegistryStore(pluginregistry.NewMemoryStore())
	handler.SetAgentProfileStore(agentprofiles.NewMemoryStore())
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
	if _, err := handler.projectWork.CreateWorkItem(ctx, projectwork.WorkItem{
		ID:        "work_reset",
		ProjectID: project.Data.ID,
		Title:     "Reset work",
	}); err != nil {
		t.Fatalf("create project work item: %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(ctx, projectwork.Assignment{
		ID:         "asgn_reset",
		ProjectID:  project.Data.ID,
		WorkItemID: "work_reset",
		RoleID:     "software_developer",
	}); err != nil {
		t.Fatalf("create project assignment: %v", err)
	}
	if _, err := handler.memory.Create(ctx, memory.Entry{
		ID:         "mem_reset",
		ProjectID:  project.Data.ID,
		Title:      "Reset memory",
		Body:       "Delete this with the project.",
		TrustLabel: memory.TrustLabelOperatorMemory,
		SourceKind: memory.SourceKindOperator,
		Enabled:    true,
	}); err != nil {
		t.Fatalf("create project memory: %v", err)
	}
	if _, err := handler.taskStore.CreateTask(ctx, types.Task{ID: "task_reset", Title: "Reset me", Status: "queued"}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := handler.agentProfiles.Create(ctx, agentprofiles.Profile{ID: "prof_reset", Name: "Reset profile"}); err != nil {
		t.Fatalf("create agent profile: %v", err)
	}
	if _, err := handler.pluginRegistry.Upsert(ctx, pluginregistry.Plugin{
		ID:                    "github",
		Name:                  "GitHub",
		Version:               "0.1.0",
		SourceKind:            pluginregistry.SourceLocalPath,
		ManifestSchemaVersion: pluginregistry.ManifestSchemaVersion,
		ManifestJSON:          []byte(`{"schema_version":"hecate.plugin.v0","id":"github","name":"GitHub","version":"0.1.0"}`),
	}); err != nil {
		t.Fatalf("create plugin record: %v", err)
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
	req := httptest.NewRequest(http.MethodPost, "/hecate/v1/system/reset-data", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var reset SystemResetDataResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &reset); err != nil {
		t.Fatalf("decode reset response: %v", err)
	}
	if reset.Data.ProjectsDeleted != 1 || reset.Data.ProjectWorkRowsDeleted != 2 || reset.Data.PluginsDeleted != 1 || reset.Data.AgentProfilesDeleted != 1 || reset.Data.ChatSessionsDeleted != 2 || reset.Data.TasksDeleted != 1 || reset.Data.ProvidersDeleted != 1 || reset.Data.PolicyRulesDeleted != 1 {
		t.Fatalf("reset stats = %+v, want one project, one plugin, one profile, two project-work rows, one task, provider, rule and two chats", reset.Data)
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
	profiles, err := handler.agentProfiles.List(ctx)
	if err != nil {
		t.Fatalf("list agent profiles: %v", err)
	}
	if len(profiles) != 0 {
		t.Fatalf("agent profiles after reset = %#v, want none", profiles)
	}
	workItems, err := handler.projectWork.ListWorkItems(ctx, project.Data.ID)
	if err != nil {
		t.Fatalf("list project work: %v", err)
	}
	if len(workItems) != 0 {
		t.Fatalf("project work after reset = %#v, want none", workItems)
	}
	memoryEntries, err := handler.memory.List(ctx, memory.Filter{ProjectID: project.Data.ID, IncludeDisabled: true})
	if err != nil {
		t.Fatalf("list project memory: %v", err)
	}
	if len(memoryEntries) != 0 {
		t.Fatalf("project memory after reset = %#v, want none", memoryEntries)
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
	if reset.Data.DatabaseRowsDeleted != 0 {
		t.Fatalf("database rows deleted = %d, want 0 for memory backend", reset.Data.DatabaseRowsDeleted)
	}
}

func TestSystemResetDataRejectsNonLoopbackClients(t *testing.T) {
	t.Parallel()

	logger := quietLogger()
	handler := NewHandler(config.Config{}, logger, nil, controlplane.NewMemoryStore(), taskstate.NewMemoryStore(), nil)
	server := NewServer(logger, handler)

	req := httptest.NewRequest(http.MethodPost, "/hecate/v1/system/reset-data", nil)
	req.RemoteAddr = "203.0.113.10:1234"
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body=%s", rec.Code, rec.Body.String())
	}
}

func TestSystemResetDataSQLiteBackendClearsRemainingRows(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	client, err := storage.NewSQLiteClient(ctx, storage.SQLiteConfig{
		Path:        filepath.Join(t.TempDir(), "hecate.db"),
		TablePrefix: "test",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	cpStore, err := controlplane.NewSQLiteStore(ctx, client, "")
	if err != nil {
		t.Fatalf("NewSQLiteStore(controlplane): %v", err)
	}
	chatStore, err := chat.NewSQLiteStore(ctx, client)
	if err != nil {
		t.Fatalf("NewSQLiteStore(chat): %v", err)
	}
	projectStore, err := projects.NewSQLiteStore(ctx, client)
	if err != nil {
		t.Fatalf("NewSQLiteStore(projects): %v", err)
	}
	memoryStore, err := memory.NewSQLiteStore(ctx, client)
	if err != nil {
		t.Fatalf("NewSQLiteStore(memory): %v", err)
	}
	projectWorkStore, err := projectwork.NewSQLiteStore(ctx, client)
	if err != nil {
		t.Fatalf("NewSQLiteStore(projectwork): %v", err)
	}
	agentProfileStore, err := agentprofiles.NewSQLiteStore(ctx, client)
	if err != nil {
		t.Fatalf("NewSQLiteStore(agentprofiles): %v", err)
	}
	pluginStore, err := pluginregistry.NewSQLiteStore(ctx, client)
	if err != nil {
		t.Fatalf("NewSQLiteStore(pluginregistry): %v", err)
	}
	taskStore, err := taskstate.NewSQLiteStore(ctx, client)
	if err != nil {
		t.Fatalf("NewSQLiteStore(taskstate): %v", err)
	}
	if _, err := client.DB().ExecContext(ctx, `CREATE TABLE `+client.QualifiedTable("reset_scratch")+` (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create scratch table: %v", err)
	}
	if _, err := client.DB().ExecContext(ctx, `INSERT INTO `+client.QualifiedTable("reset_scratch")+` (id) VALUES ('leftover')`); err != nil {
		t.Fatalf("insert scratch row: %v", err)
	}

	logger := quietLogger()
	runtime := &fakeProviderRuntime{store: cpStore}
	handler := NewHandler(config.Config{}, logger, nil, cpStore, taskStore, nil, runtime)
	handler.SetAgentChatStore(chatStore)
	handler.SetProjectStore(projectStore)
	handler.SetMemoryStore(memoryStore)
	handler.SetProjectWorkStore(projectWorkStore)
	handler.SetPluginRegistryStore(pluginStore)
	handler.SetAgentProfileStore(agentProfileStore)
	handler.SetStateCleaner(client)
	runner := &fakeAgentChatRunner{}
	handler.SetAgentChatRunner(runner)
	server := NewServer(logger, handler)

	project, err := projectStore.Create(ctx, projects.Project{ID: "proj_sqlite_reset", Name: "SQLite reset"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := chatStore.Create(ctx, chat.Session{
		ID:              "chat_sqlite_external",
		Title:           "SQLite external chat",
		ProjectID:       project.ID,
		AgentID:         "codex",
		DriverKind:      "acp",
		NativeSessionID: "native_sqlite_external",
	}); err != nil {
		t.Fatalf("create chat: %v", err)
	}
	if _, err := projectWorkStore.CreateWorkItem(ctx, projectwork.WorkItem{
		ID:        "work_sqlite_reset",
		ProjectID: project.ID,
		Title:     "SQLite work",
	}); err != nil {
		t.Fatalf("create project work: %v", err)
	}
	if _, err := memoryStore.Create(ctx, memory.Entry{
		ID:         "mem_sqlite_reset",
		ProjectID:  project.ID,
		Title:      "SQLite memory",
		Body:       "Delete this memory entry on reset.",
		TrustLabel: memory.TrustLabelOperatorMemory,
		SourceKind: memory.SourceKindOperator,
		Enabled:    true,
	}); err != nil {
		t.Fatalf("create project memory: %v", err)
	}
	if _, err := taskStore.CreateTask(ctx, types.Task{ID: "task_sqlite_reset", Title: "SQLite task", Status: "queued"}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := agentProfileStore.Create(ctx, agentprofiles.Profile{ID: "prof_sqlite_reset", Name: "SQLite profile"}); err != nil {
		t.Fatalf("create agent profile: %v", err)
	}
	if _, err := pluginStore.Upsert(ctx, pluginregistry.Plugin{
		ID:                    "linear",
		Name:                  "Linear",
		Version:               "0.1.0",
		SourceKind:            pluginregistry.SourceLocalPath,
		ManifestSchemaVersion: pluginregistry.ManifestSchemaVersion,
		ManifestJSON:          []byte(`{"schema_version":"hecate.plugin.v0","id":"linear","name":"Linear","version":"0.1.0"}`),
	}); err != nil {
		t.Fatalf("create plugin record: %v", err)
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

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/hecate/v1/system/reset-data", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var reset SystemResetDataResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &reset); err != nil {
		t.Fatalf("decode reset response: %v", err)
	}
	if reset.Data.DatabaseRowsDeleted == 0 {
		t.Fatalf("database rows deleted = 0, want remaining sqlite rows cleared; stats=%+v", reset.Data)
	}
	if reset.Data.ProjectWorkRowsDeleted != 1 {
		t.Fatalf("project work rows deleted = %d, want 1", reset.Data.ProjectWorkRowsDeleted)
	}
	if reset.Data.AgentProfilesDeleted != 1 {
		t.Fatalf("agent profiles deleted = %d, want 1", reset.Data.AgentProfilesDeleted)
	}
	if reset.Data.PluginsDeleted != 1 {
		t.Fatalf("plugins deleted = %d, want 1", reset.Data.PluginsDeleted)
	}
	if len(runner.closedSessions) != 1 || runner.closedSessions[0] != "chat_sqlite_external" {
		t.Fatalf("closed sessions = %#v, want sqlite external chat closed", runner.closedSessions)
	}
	assertSQLiteTableCount(t, client, client.QualifiedTable("memory_entries"), 0)
	assertSQLiteTableCount(t, client, client.QualifiedTable("agent_profiles"), 0)
	assertSQLiteTableCount(t, client, client.QualifiedTable("plugins"), 0)
	assertSQLiteTableCount(t, client, client.QualifiedTable("reset_scratch"), 0)
}

func assertSQLiteTableCount(t *testing.T, client *storage.SQLiteClient, table string, want int) {
	t.Helper()
	var got int
	if err := client.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM `+table).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("count %s = %d, want %d", table, got, want)
	}
}
