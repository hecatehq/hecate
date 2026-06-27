package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/projects"
)

func newProjectsTestServer() http.Handler {
	handler := NewHandler(config.Config{}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	return NewServer(quietLogger(), handler)
}

func newProjectsCairnlineReadTestServer() http.Handler {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{CoordinationBackend: "cairnline"},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	return NewServer(quietLogger(), handler)
}

func newProjectsCairnlineMirrorTestServer(t *testing.T) (*Handler, http.Handler) {
	t.Helper()
	handler := NewHandler(config.Config{
		Server:   config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{CoordinationBackend: "cairnline"},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	return handler, NewServer(quietLogger(), handler)
}

func newSeededProjectsTestServer(t *testing.T, cfg config.Config, project projects.Project) http.Handler {
	t.Helper()
	handler := NewHandler(cfg, quietLogger(), nil, nil, nil, nil)
	store := projects.NewMemoryStore()
	if _, err := store.Create(t.Context(), project); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	handler.SetProjectStore(store)
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
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var deleted ProjectDeleteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &deleted); err != nil {
		t.Fatalf("decode delete response: %v", err)
	}
	if deleted.Object != "project_delete" ||
		deleted.Data.ProjectID != created.Data.ID ||
		deleted.Data.ProjectName != "Renamed" {
		t.Fatalf("delete response = %+v, want project id/name", deleted)
	}
}

func TestProjectsAPI_ReadsUseCairnlineReadModelWhenConfigured(t *testing.T) {
	t.Parallel()
	server := newProjectsCairnlineReadTestServer()

	createBody := []byte(`{
		"name":"Cairnline Project",
		"description":"project identity read projection",
		"roots":[{"id":"root_main","path":"/tmp/cairnline-project","kind":"git","git_remote":"git@example.com:hecate/hecate.git","git_branch":"main"}],
		"context_sources":[{
			"id":"ctx_agents",
			"kind":"workspace_instruction",
			"title":"AGENTS.md",
			"path":"AGENTS.md",
			"enabled":true,
			"format":"agents_md",
			"scope":"workspace",
			"trust_label":"workspace_guidance",
			"source_category":"instructions",
			"metadata":{"root_id":"root_main"}
		}],
		"default_provider":"openai",
		"default_model":"gpt-5",
		"default_agent_profile":"architecture",
		"default_tools_enabled":false,
		"default_workspace_mode":"worktree",
		"default_system_prompt":"Stay crisp.",
		"default_compact_tool_output":true
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
	if created.Data.ReadBackend != "hecate" {
		t.Fatalf("created read_backend = %q, want hecate mutation response", created.Data.ReadBackend)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+created.Data.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var fetched ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	assertCairnlineProjectProjectionForTest(t, fetched.Data, created.Data.ID)

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var listed ProjectsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listed.Data) != 1 {
		t.Fatalf("listed projects = %+v, want one project", listed.Data)
	}
	assertCairnlineProjectProjectionForTest(t, listed.Data[0], created.Data.ID)
}

func TestProjectsAPI_CairnlineReadsMatchHecateProjectProjection(t *testing.T) {
	t.Parallel()

	project := projectReadParityFixture()
	hecateServer := newSeededProjectsTestServer(t, config.Config{}, project)
	cairnlineServer := newSeededProjectsTestServer(t, config.Config{
		Projects: config.ProjectsConfig{CoordinationBackend: "cairnline"},
	}, project)

	hecateDetail := getProjectForTest(t, hecateServer, project.ID)
	cairnlineDetail := getProjectForTest(t, cairnlineServer, project.ID)
	assertProjectProjectionParity(t, hecateDetail, cairnlineDetail, "detail")

	hecateList := listProjectsForTest(t, hecateServer)
	cairnlineList := listProjectsForTest(t, cairnlineServer)
	if len(hecateList) != 1 || len(cairnlineList) != 1 {
		t.Fatalf("project list counts = hecate:%d cairnline:%d, want one each", len(hecateList), len(cairnlineList))
	}
	assertProjectProjectionParity(t, hecateList[0], cairnlineList[0], "list")
}

func TestProjectsAPI_MirrorsIdentityMutationsToCairnlineWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineMirrorTestServer(t)

	createBody := []byte(`{
		"name":"Mirror Project",
		"description":"project identity mirror",
		"roots":[{"id":"root_main","path":"/workspace/main","kind":"git","git_branch":"main"}],
		"context_sources":[{"id":"ctx_agents","path":"AGENTS.md","kind":"workspace_instruction","title":"AGENTS.md"}],
		"default_provider":"openai",
		"default_model":"gpt-5"
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

	mirrored := getMirroredCairnlineProjectForTest(t, handler, created.Data.ID)
	if mirrored.ID != created.Data.ID || mirrored.Name != "Mirror Project" || mirrored.DefaultRootID != "root_main" {
		t.Fatalf("mirrored project = %+v, want created project identity", mirrored)
	}
	if len(mirrored.Roots) != 1 || mirrored.Roots[0].ID != "root_main" || mirrored.Roots[0].Path != "/workspace/main" {
		t.Fatalf("mirrored roots = %+v, want root_main", mirrored.Roots)
	}
	if len(mirrored.ContextSources) != 1 || mirrored.ContextSources[0].ID != "ctx_agents" || mirrored.ContextSources[0].Locator != "AGENTS.md" {
		t.Fatalf("mirrored context sources = %+v, want AGENTS.md source", mirrored.ContextSources)
	}
	assertMirroredExecutionProfileForTest(t, handler, mirrored.DefaultExecutionProfileID, "openai", "gpt-5")

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+created.Data.ID, bytes.NewReader([]byte(`{
		"name":"Mirrored Rename",
		"default_model":"gpt-5.1"
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	mirrored = getMirroredCairnlineProjectForTest(t, handler, created.Data.ID)
	if mirrored.Name != "Mirrored Rename" {
		t.Fatalf("mirrored name = %q, want patched name", mirrored.Name)
	}
	assertMirroredExecutionProfileForTest(t, handler, mirrored.DefaultExecutionProfileID, "openai", "gpt-5.1")

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+created.Data.ID+"/roots", bytes.NewReader([]byte(`{
		"id":"root_worktree",
		"path":"/workspace/.worktrees/feature",
		"kind":"git_worktree",
		"git_branch":"feature/mirror"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create root status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	mirrored = getMirroredCairnlineProjectForTest(t, handler, created.Data.ID)
	if len(mirrored.Roots) != 2 || mirrored.Roots[1].ID != "root_worktree" || mirrored.Roots[1].GitBranch != "feature/mirror" {
		t.Fatalf("mirrored roots after create = %+v, want worktree root", mirrored.Roots)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+created.Data.ID+"/context-sources", bytes.NewReader([]byte(`{
		"id":"ctx_design",
		"path":"docs/design/accepted/projects.md",
		"kind":"doc",
		"title":"Projects"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create source status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	mirrored = getMirroredCairnlineProjectForTest(t, handler, created.Data.ID)
	if len(mirrored.ContextSources) != 2 || mirrored.ContextSources[1].ID != "ctx_design" || mirrored.ContextSources[1].Locator != "docs/design/accepted/projects.md" {
		t.Fatalf("mirrored context sources after create = %+v, want design source", mirrored.ContextSources)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/projects/"+created.Data.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror after delete: %v", err)
	}
	defer store.Close()
	if _, err := service.GetProject(t.Context(), created.Data.ID); !errors.Is(err, cairnline.ErrNotFound) {
		t.Fatalf("mirrored deleted project error = %v, want ErrNotFound", err)
	}
}

func assertCairnlineProjectProjectionForTest(t *testing.T, project ProjectResponseItem, projectID string) {
	t.Helper()
	if project.ID != projectID || project.ReadBackend != "cairnline" {
		t.Fatalf("project = %+v, want Cairnline-backed project %s", project, projectID)
	}
	if project.DefaultProvider != "openai" || project.DefaultModel != "gpt-5" || project.DefaultAgentProfile != "architecture" || project.DefaultWorkspaceMode != "worktree" || project.DefaultSystemPrompt != "Stay crisp." {
		t.Fatalf("project defaults = %+v, want Cairnline-projected defaults", project)
	}
	if project.DefaultToolsEnabled == nil || *project.DefaultToolsEnabled {
		t.Fatalf("default_tools_enabled = %v, want false from Cairnline execution profile", project.DefaultToolsEnabled)
	}
	if project.DefaultCompactToolOutput == nil || !*project.DefaultCompactToolOutput {
		t.Fatalf("default_compact_tool_output = %v, want true from Cairnline execution profile", project.DefaultCompactToolOutput)
	}
	if len(project.Roots) != 1 || project.Roots[0].ID != "root_main" || project.Roots[0].Path != "/tmp/cairnline-project" || project.Roots[0].Kind != "git" || project.Roots[0].GitBranch != "main" {
		t.Fatalf("project roots = %+v, want Cairnline-projected root metadata", project.Roots)
	}
	if project.DefaultRootID != "root_main" {
		t.Fatalf("default_root_id = %q, want root_main", project.DefaultRootID)
	}
	if len(project.ContextSources) != 1 {
		t.Fatalf("context sources = %+v, want one source", project.ContextSources)
	}
	source := project.ContextSources[0]
	if source.ID != "ctx_agents" || source.Kind != "workspace_instruction" || source.Path != "AGENTS.md" || source.Format != "agents_md" || source.TrustLabel != "workspace_guidance" {
		t.Fatalf("context source = %+v, want Cairnline-projected source metadata", source)
	}
	if source.Metadata["root_id"] != "root_main" {
		t.Fatalf("context source metadata = %+v, want root_id", source.Metadata)
	}
}

func projectReadParityFixture() projects.Project {
	toolsEnabled := true
	compactToolOutput := true
	createdAt := time.Date(2026, 6, 10, 9, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 6, 11, 10, 30, 0, 0, time.UTC)
	lastOpenedAt := time.Date(2026, 6, 12, 15, 45, 0, 0, time.UTC)
	rootCreatedAt := time.Date(2026, 6, 10, 9, 5, 0, 0, time.UTC)
	rootUpdatedAt := time.Date(2026, 6, 10, 9, 10, 0, 0, time.UTC)
	sourceCreatedAt := time.Date(2026, 6, 10, 9, 15, 0, 0, time.UTC)
	sourceUpdatedAt := time.Date(2026, 6, 10, 9, 20, 0, 0, time.UTC)
	return projects.Project{
		ID:                       "proj_cairnline_parity",
		Name:                     "Cairnline parity",
		Description:              "portable project identity projection",
		DefaultRootID:            "root_main",
		DefaultProvider:          "openai",
		DefaultModel:             "gpt-5",
		DefaultAgentProfile:      "architecture",
		DefaultToolsEnabled:      &toolsEnabled,
		DefaultWorkspaceMode:     "worktree",
		DefaultSystemPrompt:      "Stay crisp.",
		DefaultCompactToolOutput: &compactToolOutput,
		CreatedAt:                createdAt,
		UpdatedAt:                updatedAt,
		LastOpenedAt:             lastOpenedAt,
		Roots: []projects.Root{
			{
				ID:        "root_main",
				Path:      "/workspace/hecate",
				Kind:      "git",
				GitRemote: "git@example.com:hecate/hecate.git",
				GitBranch: "main",
				Active:    true,
				CreatedAt: rootCreatedAt,
				UpdatedAt: rootUpdatedAt,
			},
			{
				ID:        "root_feature",
				Path:      "/workspace/hecate/.worktrees/feature",
				Kind:      "git_worktree",
				GitBranch: "feature/cairnline",
				Active:    false,
				CreatedAt: rootCreatedAt.Add(time.Minute),
				UpdatedAt: rootUpdatedAt.Add(time.Minute),
			},
		},
		ContextSources: []projects.ContextSource{
			{
				ID:             "ctx_agents",
				Kind:           "workspace_instruction",
				Title:          "AGENTS.md",
				Path:           "AGENTS.md",
				Enabled:        true,
				Format:         "agents_md",
				Scope:          "workspace",
				TrustLabel:     "workspace_guidance",
				SourceCategory: "instructions",
				Metadata: map[string]string{
					"root_id": "root_main",
					"scope":   "workspace",
				},
				CreatedAt: sourceCreatedAt,
				UpdatedAt: sourceUpdatedAt,
			},
			{
				ID:             "ctx_design",
				Kind:           "doc",
				Title:          "Projects design",
				Path:           "docs/design/accepted/projects.md",
				Enabled:        false,
				Format:         "markdown",
				Scope:          "project",
				TrustLabel:     "operator_guidance",
				SourceCategory: "design",
				Metadata: map[string]string{
					"root_id": "root_main",
				},
				CreatedAt: sourceCreatedAt.Add(time.Minute),
				UpdatedAt: sourceUpdatedAt.Add(time.Minute),
			},
		},
	}
}

func getProjectForTest(t *testing.T, server http.Handler, projectID string) ProjectResponseItem {
	t.Helper()
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+projectID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("get project status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode project response: %v", err)
	}
	return response.Data
}

func listProjectsForTest(t *testing.T, server http.Handler) []ProjectResponseItem {
	t.Helper()
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list projects status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode projects response: %v", err)
	}
	return response.Data
}

func assertProjectProjectionParity(t *testing.T, hecate, cairnline ProjectResponseItem, label string) {
	t.Helper()
	if hecate.ReadBackend != "hecate" {
		t.Fatalf("%s hecate read_backend = %q, want hecate", label, hecate.ReadBackend)
	}
	if cairnline.ReadBackend != "cairnline" {
		t.Fatalf("%s cairnline read_backend = %q, want cairnline", label, cairnline.ReadBackend)
	}
	hecate.ReadBackend = ""
	cairnline.ReadBackend = ""
	if !reflect.DeepEqual(hecate, cairnline) {
		t.Fatalf("%s project projection mismatch\nhecate:   %+v\ncairnline: %+v", label, hecate, cairnline)
	}
}

func getMirroredCairnlineProjectForTest(t *testing.T, handler *Handler, projectID string) cairnline.Project {
	t.Helper()
	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	defer store.Close()
	project, err := service.GetProject(t.Context(), projectID)
	if err != nil {
		t.Fatalf("GetProject(%q): %v", projectID, err)
	}
	return project
}

func assertMirroredExecutionProfileForTest(t *testing.T, handler *Handler, profileID, provider, model string) {
	t.Helper()
	if profileID == "" {
		t.Fatalf("mirrored project missing default execution profile id")
	}
	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	defer store.Close()
	profiles, err := service.ListExecutionProfiles(t.Context())
	if err != nil {
		t.Fatalf("ListExecutionProfiles(): %v", err)
	}
	for _, profile := range profiles {
		if profile.ID != profileID {
			continue
		}
		if profile.ProviderHint != provider || profile.ModelHint != model {
			t.Fatalf("execution profile = %+v, want provider/model %s/%s", profile, provider, model)
		}
		return
	}
	t.Fatalf("execution profile %q not found in %+v", profileID, profiles)
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

func TestProjectsAPI_ContextSourcesSupportRootlessOperatorSources(t *testing.T) {
	t.Parallel()
	server := newProjectsTestServer()

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{
		"name":"Research plan",
		"context_sources":[
			{
				"kind":"url",
				"title":"Design brief",
				"path":"https://example.invalid/design",
				"enabled":true,
				"format":"url",
				"trust_label":"operator_source",
				"source_category":"operator_source",
				"metadata":{"note":"Reviewed by the operator"}
			},
			{
				"kind":"note",
				"title":"Research goals",
				"path":"note:research-goals",
				"format":"text",
				"metadata":{"note":"Keep this source as provenance metadata."}
			},
			{
				"kind":"external_ref",
				"title":"Ticket",
				"path":"OPS-123",
				"enabled":false,
				"format":"reference"
			}
		]
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if len(created.Data.Roots) != 0 {
		t.Fatalf("roots = %+v, want rootless project", created.Data.Roots)
	}
	if len(created.Data.ContextSources) != 3 {
		t.Fatalf("context sources = %+v, want three operator sources", created.Data.ContextSources)
	}
	byTitle := make(map[string]ProjectContextSourceResponseItem)
	for _, source := range created.Data.ContextSources {
		if source.ID == "" {
			t.Fatalf("source missing generated id: %+v", source)
		}
		byTitle[source.Title] = source
	}
	if got := byTitle["Design brief"]; got.Kind != "url" || got.Path != "https://example.invalid/design" || got.Metadata["note"] != "Reviewed by the operator" {
		t.Fatalf("url source = %+v, want preserved url metadata", got)
	}
	if got := byTitle["Research goals"]; got.Kind != "note" || got.Format != "text" || got.Metadata["note"] == "" || !got.Enabled {
		t.Fatalf("note source = %+v, want enabled note metadata source", got)
	}
	if got := byTitle["Ticket"]; got.Kind != "external_ref" || got.Enabled {
		t.Fatalf("external ref source = %+v, want disabled external reference", got)
	}
}

func TestProjectsAPI_RootMutations(t *testing.T) {
	t.Parallel()
	server := newProjectsTestServer()

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{
		"name":"Workspace plan"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var project ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &project); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/roots", bytes.NewReader([]byte(`{
		"path":"/workspace/main",
		"kind":"git",
		"git_branch":"main",
		"active":true
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create root status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var withRoot ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &withRoot); err != nil {
		t.Fatalf("decode root create response: %v", err)
	}
	if len(withRoot.Data.Roots) != 1 || withRoot.Data.Roots[0].ID == "" || withRoot.Data.DefaultRootID != withRoot.Data.Roots[0].ID {
		t.Fatalf("project after root create = %+v, want generated default root", withRoot.Data)
	}
	root := withRoot.Data.Roots[0]
	if root.Kind != "git" || root.GitBranch != "main" || !root.Active {
		t.Fatalf("created root = %+v, want active git main root", root)
	}
	createdAt := root.CreatedAt

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/roots", bytes.NewReader([]byte(`{
		"id":"root_other",
		"path":"/workspace/other",
		"active":true
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create second root status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+project.Data.ID+"/roots/"+root.ID, bytes.NewReader([]byte(`{
		"path":"/workspace/main-renamed",
		"kind":"git_worktree",
		"git_branch":"feature/root",
		"active":false
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("update root status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var updated ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode root update response: %v", err)
	}
	if len(updated.Data.Roots) != 2 {
		t.Fatalf("roots after update = %+v, want two", updated.Data.Roots)
	}
	root = updated.Data.Roots[0]
	if root.ID != withRoot.Data.Roots[0].ID || root.Path != "/workspace/main-renamed" || root.Kind != "git_worktree" || root.Active {
		t.Fatalf("updated root = %+v, want same id inactive worktree root", root)
	}
	if root.CreatedAt != createdAt {
		t.Fatalf("updated root created_at = %q, want original %q", root.CreatedAt, createdAt)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/projects/"+project.Data.ID+"/roots/"+root.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete root status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var deleted ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &deleted); err != nil {
		t.Fatalf("decode root delete response: %v", err)
	}
	if len(deleted.Data.Roots) != 1 || deleted.Data.Roots[0].ID != "root_other" || deleted.Data.DefaultRootID != "root_other" {
		t.Fatalf("roots after delete = %+v default=%q, want remaining root as default", deleted.Data.Roots, deleted.Data.DefaultRootID)
	}
}

func TestProjectsAPI_RootMutationValidation(t *testing.T) {
	t.Parallel()
	server := newProjectsTestServer()

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{
		"name":"Workspace plan",
		"roots":[{"id":"root_existing","path":"/workspace/main"}]
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var project ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &project); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/roots", bytes.NewReader([]byte(`{"path":" "}`))))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "project root path is required") {
		t.Fatalf("blank root path response = %d %s, want 400 path required", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/roots", bytes.NewReader([]byte(`{
		"id":"root_existing",
		"path":"/workspace/other"
	}`))))
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate root status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+project.Data.ID+"/roots/root_missing", bytes.NewReader([]byte(`{
		"path":"/workspace/new"
	}`))))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing update root status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/projects/"+project.Data.ID+"/roots/root_missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing delete root status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_missing/roots", bytes.NewReader([]byte(`{
		"path":"/workspace/new"
	}`))))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing project root create status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
}

func TestProjectsAPI_ContextSourceMutations(t *testing.T) {
	t.Parallel()
	server := newProjectsTestServer()

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{
		"name":"Research plan"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var project ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &project); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/context-sources", bytes.NewReader([]byte(`{
		"kind":"url",
		"title":"Design brief",
		"path":"https://example.invalid/design",
		"enabled":true,
		"format":"url",
		"trust_label":"operator_source",
		"source_category":"operator_source",
		"metadata":{"note":"Reviewed by the operator"}
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create source status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var withSource ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &withSource); err != nil {
		t.Fatalf("decode source create response: %v", err)
	}
	if len(withSource.Data.ContextSources) != 1 {
		t.Fatalf("sources after create = %+v, want one", withSource.Data.ContextSources)
	}
	source := withSource.Data.ContextSources[0]
	if source.ID == "" || source.Kind != "url" || source.Metadata["note"] != "Reviewed by the operator" {
		t.Fatalf("created source = %+v, want generated url source", source)
	}
	createdAt := source.CreatedAt

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+project.Data.ID+"/context-sources/"+source.ID, bytes.NewReader([]byte(`{
		"kind":"note",
		"title":"Research goals",
		"path":"note:research-goals",
		"format":"text",
		"enabled":false,
		"metadata":{"note":"Keep as source metadata"}
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("update source status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var updated ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode source update response: %v", err)
	}
	if len(updated.Data.ContextSources) != 1 {
		t.Fatalf("sources after update = %+v, want one", updated.Data.ContextSources)
	}
	source = updated.Data.ContextSources[0]
	if source.ID == "" || source.ID != withSource.Data.ContextSources[0].ID || source.Kind != "note" || source.Enabled {
		t.Fatalf("updated source = %+v, want same id disabled note source", source)
	}
	if source.CreatedAt != createdAt {
		t.Fatalf("updated source created_at = %q, want original %q", source.CreatedAt, createdAt)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/projects/"+project.Data.ID+"/context-sources/"+source.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete source status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var deleted ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &deleted); err != nil {
		t.Fatalf("decode source delete response: %v", err)
	}
	if len(deleted.Data.ContextSources) != 0 {
		t.Fatalf("sources after delete = %+v, want none", deleted.Data.ContextSources)
	}
}

func TestProjectsAPI_ContextSourceMutationValidation(t *testing.T) {
	t.Parallel()
	server := newProjectsTestServer()

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{
		"name":"Research plan",
		"context_sources":[{"id":"ctx_existing","path":"README.md"}]
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var project ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &project); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/context-sources", bytes.NewReader([]byte(`{"title":"Broken","path":" "}`))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("blank source path status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/context-sources", bytes.NewReader([]byte(`{"id":"ctx_existing","path":"docs/README.md"}`))))
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate source status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+project.Data.ID+"/context-sources/ctx_missing", bytes.NewReader([]byte(`{"path":"README.md"}`))))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing source patch status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/projects/"+project.Data.ID+"/context-sources/ctx_missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing source delete status = %d body=%s, want 404", rec.Code, rec.Body.String())
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
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var deleted ProjectDeleteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &deleted); err != nil {
		t.Fatalf("decode delete response: %v", err)
	}
	if deleted.Object != "project_delete" ||
		deleted.Data.ProjectID != created.Data.ID ||
		deleted.Data.ProjectName != "Hecate" ||
		deleted.Data.ChatSessionsDeleted != 2 {
		t.Fatalf("delete response = %+v, want project id/name and 2 deleted chats", deleted)
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
	if len(runner.deletedSessions) != 1 || runner.deletedSessions[0] != "chat_project_external" {
		t.Fatalf("deleted sessions = %#v, want project external chat deleted", runner.deletedSessions)
	}
	if len(runner.closedSessions) != 0 {
		t.Fatalf("closed sessions = %#v, want project delete not close", runner.closedSessions)
	}
}
