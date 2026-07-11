package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/controlplane"
	"github.com/hecatehq/hecate/internal/pluginregistry"
	"github.com/hecatehq/hecate/internal/projectruntime"
	"github.com/hecatehq/hecate/internal/storage"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestSystemResetDataMemoryBackendDeletesStateAndClosesAgentSessions(t *testing.T) {
	t.Parallel()
	logger := quietLogger()
	cpStore := controlplane.NewMemoryStore()
	runtime := &fakeProviderRuntime{store: cpStore}
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend:      "cairnline",
			CairnlineConnector:       "embedded",
			CairnlineReadSource:      "embedded",
			CairnlineReplacementMode: "embedded",
		},
	}, logger, nil, cpStore, taskstate.NewMemoryStore(), nil, runtime)
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
	if _, err := handler.projectRuntime.Upsert(ctx, projectruntime.AssignmentRuntime{
		ProjectID:    project.Data.ID,
		AssignmentID: "asgn_reset",
	}); err != nil {
		t.Fatalf("create project runtime: %v", err)
	}
	if _, err := handler.taskStore.CreateTask(ctx, types.Task{ID: "task_reset", Title: "Reset me", Status: "queued"}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := handler.agentProfiles.Create(ctx, agentprofiles.Profile{ID: "prof_reset", Name: "Reset profile"}); err != nil {
		t.Fatalf("create agent preset: %v", err)
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
	if reset.Data.ProjectsDeleted != 1 || reset.Data.ProjectRuntimeRowsDeleted != 1 || reset.Data.PluginsDeleted != 1 || reset.Data.AgentPresetsDeleted != 1 || reset.Data.ChatSessionsDeleted != 2 || reset.Data.TasksDeleted != 1 || reset.Data.ProvidersDeleted != 1 || reset.Data.PolicyRulesDeleted != 1 || reset.Data.CairnlineFilesDeleted == 0 {
		t.Fatalf("reset stats = %+v, want one Cairnline project, one runtime row, one plugin, one preset, one task, provider, rule and two chats", reset.Data)
	}
	if len(runner.deletedSessions) != 2 {
		t.Fatalf("deleted sessions = %#v, want two external chats deleted", runner.deletedSessions)
	}
	if len(runner.closedSessions) != 0 {
		t.Fatalf("closed sessions = %#v, want reset delete path not close", runner.closedSessions)
	}

	chats, err := chatStore.List(ctx)
	if err != nil {
		t.Fatalf("list chats: %v", err)
	}
	if len(chats) != 0 {
		t.Fatalf("chats after reset = %#v, want none", chats)
	}
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list projects after reset status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var projectsAfterReset ProjectsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &projectsAfterReset); err != nil {
		t.Fatalf("decode projects after reset: %v", err)
	}
	if len(projectsAfterReset.Data) != 0 {
		t.Fatalf("projects after reset = %#v, want none", projectsAfterReset.Data)
	}
	profiles, err := handler.agentProfiles.List(ctx)
	if err != nil {
		t.Fatalf("list agent presets: %v", err)
	}
	if !agentProfileListIsBuiltInOnly(profiles) {
		t.Fatalf("agent presets after reset = %#v, want only built-ins", profiles)
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

func TestSystemResetDataRemovesEmbeddedCairnlineDatabase(t *testing.T) {
	t.Parallel()
	logger := quietLogger()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend:      "cairnline",
			CairnlineConnector:       "embedded",
			CairnlineReadSource:      "embedded",
			CairnlineReplacementMode: "embedded",
		},
	}, logger, nil, controlplane.NewMemoryStore(), taskstate.NewMemoryStore(), nil)
	server := NewServer(logger, handler)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{"name":"Cairnline reset"}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create project status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(handler.cairnlineEmbeddedDatabasePath()); err != nil {
		t.Fatalf("stat Cairnline database before reset: %v", err)
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
	if reset.Data.CairnlineFilesDeleted == 0 {
		t.Fatalf("cairnline database files deleted = 0, want embedded files removed; stats=%+v", reset.Data)
	}
	if reset.Data.ProjectsDeleted != 1 {
		t.Fatalf("projects deleted = %d, want one authoritative Cairnline project", reset.Data.ProjectsDeleted)
	}
	for _, path := range []string{handler.cairnlineEmbeddedDatabasePath(), handler.cairnlineEmbeddedDatabasePath() + "-wal", handler.cairnlineEmbeddedDatabasePath() + "-shm"} {
		if _, err := os.Stat(path); err == nil || !os.IsNotExist(err) {
			t.Fatalf("stat %s after reset error = %v, want not exist", path, err)
		}
	}
}

func TestSystemResetDataRemovesUnreadableEmbeddedCairnlineDatabase(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend:      "cairnline",
			CairnlineConnector:       "embedded",
			CairnlineReadSource:      "embedded",
			CairnlineReplacementMode: "embedded",
		},
	}, quietLogger(), nil, controlplane.NewMemoryStore(), taskstate.NewMemoryStore(), nil)
	databasePath := handler.cairnlineEmbeddedDatabasePath()
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o755); err != nil {
		t.Fatalf("create Cairnline database directory: %v", err)
	}
	if err := os.WriteFile(databasePath, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatalf("write unreadable Cairnline database: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/hecate/v1/system/reset-data", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	NewServer(quietLogger(), handler).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var reset SystemResetDataResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &reset); err != nil {
		t.Fatalf("decode reset response: %v", err)
	}
	if reset.Data.ProjectsDeleted != 0 || reset.Data.CairnlineFilesDeleted == 0 {
		t.Fatalf("reset stats = %+v, want unreadable Cairnline file removed without a project count", reset.Data)
	}
	if _, err := os.Stat(databasePath); err == nil || !os.IsNotExist(err) {
		t.Fatalf("stat unreadable Cairnline database after reset error = %v, want not exist", err)
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
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend:      "cairnline",
			CairnlineConnector:       "embedded",
			CairnlineReadSource:      "embedded",
			CairnlineReplacementMode: "embedded",
		},
	}, logger, nil, cpStore, taskStore, nil, runtime)
	handler.SetAgentChatStore(chatStore)
	handler.SetPluginRegistryStore(pluginStore)
	handler.SetAgentProfileStore(agentProfileStore)
	handler.SetStateCleaner(client)
	runner := &fakeAgentChatRunner{}
	handler.SetAgentChatRunner(runner)
	server := NewServer(logger, handler)

	createProject := httptest.NewRecorder()
	server.ServeHTTP(createProject, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{"name":"SQLite reset"}`))))
	if createProject.Code != http.StatusCreated {
		t.Fatalf("create project status = %d body=%s, want 201", createProject.Code, createProject.Body.String())
	}
	var project ProjectResponse
	if err := json.Unmarshal(createProject.Body.Bytes(), &project); err != nil {
		t.Fatalf("decode created project: %v", err)
	}
	if _, err := chatStore.Create(ctx, chat.Session{
		ID:              "chat_sqlite_external",
		Title:           "SQLite external chat",
		ProjectID:       project.Data.ID,
		AgentID:         "codex",
		DriverKind:      "acp",
		NativeSessionID: "native_sqlite_external",
	}); err != nil {
		t.Fatalf("create chat: %v", err)
	}
	if _, err := taskStore.CreateTask(ctx, types.Task{ID: "task_sqlite_reset", Title: "SQLite task", Status: "queued"}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := agentProfileStore.Create(ctx, agentprofiles.Profile{ID: "prof_sqlite_reset", Name: "SQLite profile"}); err != nil {
		t.Fatalf("create agent preset: %v", err)
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
	if reset.Data.CairnlineFilesDeleted == 0 {
		t.Fatalf("cairnline files deleted = 0, want authoritative Projects database removed; stats=%+v", reset.Data)
	}
	if reset.Data.ProjectsDeleted != 1 {
		t.Fatalf("projects deleted = %d, want one authoritative Cairnline project", reset.Data.ProjectsDeleted)
	}
	if reset.Data.AgentPresetsDeleted != 1 {
		t.Fatalf("agent presets deleted = %d, want 1", reset.Data.AgentPresetsDeleted)
	}
	if reset.Data.PluginsDeleted != 1 {
		t.Fatalf("plugins deleted = %d, want 1", reset.Data.PluginsDeleted)
	}
	if len(runner.deletedSessions) != 1 || runner.deletedSessions[0] != "chat_sqlite_external" {
		t.Fatalf("deleted sessions = %#v, want sqlite external chat deleted", runner.deletedSessions)
	}
	if len(runner.closedSessions) != 0 {
		t.Fatalf("closed sessions = %#v, want reset delete path not close", runner.closedSessions)
	}
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

func agentProfileListIsBuiltInOnly(items []agentprofiles.Profile) bool {
	if len(items) == 0 {
		return false
	}
	for _, item := range items {
		if !item.BuiltIn {
			return false
		}
	}
	return true
}
