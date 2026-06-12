package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/projects"
)

func newProjectsTestServer() http.Handler {
	handler := NewHandler(config.Config{}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	return NewServer(quietLogger(), handler)
}

func TestProjectsAPI_CRUD(t *testing.T) {
	t.Parallel()
	server := newProjectsTestServer()

	createBody := []byte(`{
		"name":"Hecate",
		"description":"main repo",
		"roots":[{"path":"/tmp/hecate","kind":"local","git_remote":"git@example.com:hecate/hecate.git","git_branch":"main"}],
		"context_sources":[{"path":"README.md","title":"README"}],
		"default_provider":"ollama",
		"default_model":"llama3.1:8b",
		"default_tools_enabled":true
	}`)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader(createBody)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Data.ID == "" || created.Data.Roots[0].ID == "" {
		t.Fatalf("created project missing generated ids: %+v", created.Data)
	}
	if created.Data.DefaultRootID != created.Data.Roots[0].ID {
		t.Fatalf("default_root_id = %q, want first root id %q", created.Data.DefaultRootID, created.Data.Roots[0].ID)
	}
	if len(created.Data.ContextSources) != 1 || created.Data.ContextSources[0].ID == "" {
		t.Fatalf("created project missing generated context source id: %+v", created.Data)
	}
	if created.Data.ContextSources[0].Kind != "doc" || !created.Data.ContextSources[0].Enabled {
		t.Fatalf("context source = %+v, want enabled doc source", created.Data.ContextSources[0])
	}
	if created.Data.LastOpenedAt != "" {
		t.Fatalf("last_opened_at = %q, want omitted until explicitly opened", created.Data.LastOpenedAt)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+created.Data.ID, bytes.NewReader([]byte(`{
		"name":"Renamed",
		"default_model":"ministral-3:latest",
		"context_sources":[{"id":"ctx_architecture","path":"docs/contributor/architecture.md","enabled":false}]
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var updated ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode patch response: %v", err)
	}
	if updated.Data.Name != "Renamed" || updated.Data.DefaultModel != "ministral-3:latest" {
		t.Fatalf("updated project = %+v, want patched name/model", updated.Data)
	}
	if len(updated.Data.ContextSources) != 1 || updated.Data.ContextSources[0].ID != "ctx_architecture" || updated.Data.ContextSources[0].Enabled {
		t.Fatalf("updated context sources = %+v, want disabled architecture source", updated.Data.ContextSources)
	}

	openedAt := time.Date(2026, 5, 20, 12, 30, 0, 0, time.UTC)
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+created.Data.ID, bytes.NewReader([]byte(`{"last_opened_at":"`+openedAt.Format(time.RFC3339Nano)+`"}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("last-opened patch status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode last-opened patch response: %v", err)
	}
	if updated.Data.LastOpenedAt != openedAt.Format(time.RFC3339Nano) {
		t.Fatalf("last_opened_at = %q, want %q", updated.Data.LastOpenedAt, openedAt.Format(time.RFC3339Nano))
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var listed ProjectsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listed.Data) != 1 || listed.Data[0].ID != created.Data.ID {
		t.Fatalf("listed projects = %+v, want created project", listed.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/projects/"+created.Data.ID, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d body=%s, want 204", rec.Code, rec.Body.String())
	}
}

func TestProjectsAPI_Validation(t *testing.T) {
	t.Parallel()
	server := newProjectsTestServer()

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{"name":" "}`))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("blank-name status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{"name":"Broken","roots":[{"id":"root_a","path":"/tmp/a"}],"default_root_id":"root_missing"}`))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid default root status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "root_missing") || !strings.Contains(rec.Body.String(), "root_a") {
		t.Fatalf("invalid default root body = %s, want invalid and available root ids", rec.Body.String())
	}
}

func TestProjectsAPI_CreateWithWorkspacePathDefaultsRoot(t *testing.T) {
	t.Parallel()
	server := newProjectsTestServer()

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{
		"name":"Workspace project",
		"workspace_path":"/tmp/hecate-workspace",
		"workspace_kind":"git"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if len(created.Data.Roots) != 1 {
		t.Fatalf("roots = %+v, want one generated workspace root", created.Data.Roots)
	}
	root := created.Data.Roots[0]
	if root.ID == "" || root.Path != "/tmp/hecate-workspace" || root.Kind != "git" || !root.Active {
		t.Fatalf("root = %+v, want generated active git workspace root", root)
	}
	if created.Data.DefaultRootID != root.ID {
		t.Fatalf("default_root_id = %q, want generated root id %q", created.Data.DefaultRootID, root.ID)
	}
}

func TestProjectsAPI_RejectsDuplicateProjectIdentity(t *testing.T) {
	t.Parallel()
	server := newProjectsTestServer()

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{
		"name":"Hecate",
		"workspace_path":"/tmp/hecate",
		"workspace_kind":"git"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{
		"name":" hecate ",
		"workspace_path":"/tmp/other",
		"workspace_kind":"git"
	}`))))
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate name status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "project name") {
		t.Fatalf("duplicate name body = %s, want project name conflict", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{
		"name":"Runtime",
		"workspace_path":"/tmp/hecate/",
		"workspace_kind":"git"
	}`))))
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate root status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "project root path") {
		t.Fatalf("duplicate root body = %s, want root path conflict", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{"name":"Other"}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create other status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var other ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &other); err != nil {
		t.Fatalf("decode other response: %v", err)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+other.Data.ID, bytes.NewReader([]byte(`{"name":"HECATE"}`))))
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate rename status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+other.Data.ID, bytes.NewReader([]byte(`{"roots":[{"path":"/tmp/hecate/"}]}`))))
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate root patch status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}
}

func TestProjectsAPI_CreateWithoutWorkspace(t *testing.T) {
	t.Parallel()
	server := newProjectsTestServer()

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{"name":"No workspace"}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if len(created.Data.Roots) != 0 || created.Data.DefaultRootID != "" {
		t.Fatalf("project = %+v, want workspace-less project", created.Data)
	}
}

func TestProjectsAPI_CreateWorkspacePathRejectsConflictingRootFields(t *testing.T) {
	t.Parallel()
	server := newProjectsTestServer()

	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "explicit roots",
			body: `{"name":"Broken","workspace_path":"/tmp/hecate","roots":[{"path":"/tmp/other"}]}`,
			want: "workspace_path cannot be combined with roots",
		},
		{
			name: "workspace kind without path",
			body: `{"name":"Broken","workspace_kind":"git"}`,
			want: "workspace_kind requires workspace_path",
		},
		{
			name: "default root id",
			body: `{"name":"Broken","workspace_path":"/tmp/hecate","default_root_id":"root_a"}`,
			want: "default_root_id cannot be supplied with workspace_path",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(tc.body))))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("create status = %d body=%s, want 400", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("create body = %s, want %q", rec.Body.String(), tc.want)
			}
		})
	}
}

func TestProjectsAPI_InvalidDefaultRootPatchDoesNotMutate(t *testing.T) {
	t.Parallel()
	server := newProjectsTestServer()

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{
		"name":"Original",
		"roots":[{"id":"root_a","path":"/tmp/a"}],
		"default_root_id":"root_a"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+created.Data.ID, bytes.NewReader([]byte(`{"default_root_id":"root_missing"}`))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("patch status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+created.Data.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var got ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if got.Data.DefaultRootID != "root_a" {
		t.Fatalf("default_root_id mutated to %q, want root_a", got.Data.DefaultRootID)
	}
}

func TestProjectsAPI_ReplacingRootsDefaultsToFirstNewRoot(t *testing.T) {
	t.Parallel()
	server := newProjectsTestServer()

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{
		"name":"Original",
		"roots":[{"id":"root_a","path":"/tmp/a"}],
		"default_root_id":"root_a"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+created.Data.ID, bytes.NewReader([]byte(`{"roots":[{"id":"root_b","path":"/tmp/b"}]}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var updated ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode patch response: %v", err)
	}
	if updated.Data.DefaultRootID != "root_b" {
		t.Fatalf("default_root_id = %q, want root_b", updated.Data.DefaultRootID)
	}
}

func TestProjectsAPI_InvalidPatchDoesNotMutate(t *testing.T) {
	t.Parallel()
	server := newProjectsTestServer()

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{"name":"Original","description":"before"}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+created.Data.ID, bytes.NewReader([]byte(`{"description":"after","roots":[{"path":" "} ]}`))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("patch status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+created.Data.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var got ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if got.Data.Description != "before" || len(got.Data.Roots) != 0 {
		t.Fatalf("project mutated after invalid patch: %+v", got.Data)
	}
}

func TestProjectsAPI_BlankLastOpenedAtPatchDoesNotMutate(t *testing.T) {
	t.Parallel()
	server := newProjectsTestServer()

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{"name":"Original"}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	originalLastOpenedAt := created.Data.LastOpenedAt

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+created.Data.ID, bytes.NewReader([]byte(`{"last_opened_at":"   "}`))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("blank last_opened_at patch status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+created.Data.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var got ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if got.Data.LastOpenedAt != originalLastOpenedAt {
		t.Fatalf("last_opened_at mutated to %q, want %q", got.Data.LastOpenedAt, originalLastOpenedAt)
	}
}

func TestProjectsAPI_DeleteProjectDeletesProjectChats(t *testing.T) {
	t.Parallel()
	logger := quietLogger()
	handler := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	chatStore := chat.NewMemoryStore()
	handler.SetAgentChatStore(chatStore)
	runner := &fakeAgentChatRunner{}
	handler.SetAgentChatRunner(runner)
	server := NewServer(logger, handler)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{"name":"Hecate"}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	ctx := t.Context()
	if _, err := chatStore.Create(ctx, chat.Session{
		ID:        "chat_project",
		Title:     "Project chat",
		ProjectID: created.Data.ID,
		AgentID:   chat.DefaultAgentID,
	}); err != nil {
		t.Fatalf("create project chat: %v", err)
	}
	if _, err := chatStore.Create(ctx, chat.Session{
		ID:              "chat_project_external",
		Title:           "Project external chat",
		ProjectID:       created.Data.ID,
		AgentID:         "codex",
		DriverKind:      "acp",
		NativeSessionID: "native_project_external",
	}); err != nil {
		t.Fatalf("create project external chat: %v", err)
	}
	if _, err := chatStore.Create(ctx, chat.Session{
		ID:        "chat_other",
		Title:     "Other project chat",
		ProjectID: "proj_other",
		AgentID:   chat.DefaultAgentID,
	}); err != nil {
		t.Fatalf("create other project chat: %v", err)
	}
	if _, err := chatStore.Create(ctx, chat.Session{
		ID:      "chat_unprojected",
		Title:   "Unprojected chat",
		AgentID: chat.DefaultAgentID,
	}); err != nil {
		t.Fatalf("create unprojected chat: %v", err)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/projects/"+created.Data.ID, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d body=%s, want 204", rec.Code, rec.Body.String())
	}

	sessions, err := chatStore.List(ctx)
	if err != nil {
		t.Fatalf("list chats: %v", err)
	}
	ids := make(map[string]bool, len(sessions))
	for _, session := range sessions {
		ids[session.ID] = true
		if session.ProjectID == created.Data.ID {
			t.Fatalf("deleted project chat remains: %+v", session)
		}
	}
	if ids["chat_project"] {
		t.Fatalf("project chat was not deleted")
	}
	if ids["chat_project_external"] {
		t.Fatalf("project external chat was not deleted")
	}
	if !ids["chat_other"] || !ids["chat_unprojected"] {
		t.Fatalf("unrelated chats = %v, want other project and unprojected chats retained", ids)
	}
	if len(runner.closedSessions) != 1 || runner.closedSessions[0] != "chat_project_external" {
		t.Fatalf("closed sessions = %#v, want project external chat closed", runner.closedSessions)
	}
}
