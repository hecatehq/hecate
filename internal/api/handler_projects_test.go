package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
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

func newProjectsCairnlineSidecarReadTestServer(t *testing.T, mode string) (*Handler, http.Handler) {
	t.Helper()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend:          "cairnline",
			CairnlineConnector:           "sidecar",
			CairnlineReadSource:          "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + mode},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })
	return handler, NewServer(quietLogger(), handler)
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

func newProjectsCairnlineMetadataDefaultsAuthorityTestServer(t *testing.T) (*Handler, http.Handler) {
	t.Helper()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend:     "cairnline",
			CairnlineWriteAuthority: projectCairnlineWriteAuthorityProjectMetadataDefaults,
		},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	return handler, NewServer(quietLogger(), handler)
}

func newProjectsCairnlineIdentityAuthorityTestServer(t *testing.T) (*Handler, http.Handler) {
	t.Helper()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend:     "cairnline",
			CairnlineWriteAuthority: projectCairnlineWriteAuthorityProjectIdentity,
		},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	return handler, NewServer(quietLogger(), handler)
}

func newProjectsCairnlineReplacementIdentityAuthorityTestServer(t *testing.T) (*Handler, http.Handler) {
	t.Helper()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend:      "cairnline",
			CairnlineConnector:       "embedded",
			CairnlineReadSource:      "embedded",
			CairnlineWriteAuthority:  "all-portable",
			CairnlineReplacementMode: "embedded",
		},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	return handler, NewServer(quietLogger(), handler)
}

func newProjectsCairnlineRootSourceAuthorityTestServer(t *testing.T) (*Handler, http.Handler) {
	t.Helper()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend:     "cairnline",
			CairnlineWriteAuthority: projectCairnlineWriteAuthorityProjectRoots + "," + projectCairnlineWriteAuthorityProjectContextSources,
		},
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

func TestProjectsAPI_ReadsUseCairnlineSidecarWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineSidecarReadTestServer(t, "full")
	if handler.projectReadRoutesUseCairnlineReadModel() {
		t.Fatal("sidecar project reads enabled embedded Cairnline read-model routes")
	}
	if !handler.projectCairnlineSidecarReadRoutesEnabled() {
		t.Fatal("sidecar project read predicate = false, want true")
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var listed ProjectsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listed.Data) != 1 {
		t.Fatalf("listed projects = %+v, want sidecar fixture project", listed.Data)
	}
	assertCairnlineSidecarProjectForTest(t, listed.Data[0], "proj_fixture")

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var fetched ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("decode detail response: %v", err)
	}
	assertCairnlineSidecarProjectForTest(t, fetched.Data, "proj_fixture")
}

func TestProjectsAPI_CairnlineSidecarReadRequiresStructuredContent(t *testing.T) {
	t.Parallel()
	_, server := newProjectsCairnlineSidecarReadTestServer(t, "text-only")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("list status = %d body=%s, want 502", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "structuredContent") {
		t.Fatalf("error body = %s, want structuredContent diagnostic", rec.Body.String())
	}
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

func TestProjectsAPI_CairnlineConfiguredProjectDetailReadsEmbeddedMirror(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineMirrorTestServer(t)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{
		"name":"Embedded Mirror Read"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	if _, _, err := service.CreateRoot(t.Context(), created.Data.ID, cairnline.Root{
		ID:     "root_mirror_only",
		Path:   "/workspace/mirror-only",
		Kind:   "local",
		Active: true,
	}); err != nil {
		store.Close()
		t.Fatalf("CreateRoot(root_mirror_only): %v", err)
	}
	store.Close()

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+created.Data.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var detail ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail response: %v", err)
	}
	if detail.Data.ReadBackend != "cairnline" {
		t.Fatalf("read backend = %q, want cairnline", detail.Data.ReadBackend)
	}
	if len(detail.Data.Roots) != 1 || detail.Data.Roots[0].ID != "root_mirror_only" || detail.Data.Roots[0].Path != "/workspace/mirror-only" {
		t.Fatalf("detail roots = %+v, want mirror-only root from embedded Cairnline DB", detail.Data.Roots)
	}
}

func TestProjectsAPI_StrictEmbeddedReadModelReadsProjectsWithoutHecateStore(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend: "cairnline",
			CairnlineReadSource: "embedded",
		},
	}, quietLogger(), nil, nil, nil, nil)
	server := NewServer(quietLogger(), handler)
	client := newAPITestClient(t, server)
	const projectID = "proj_embedded_project"

	if err := handler.withCairnlineEmbeddedMirrorService(t.Context(), func(service *cairnline.Service) error {
		if _, err := service.CreateExecutionProfile(t.Context(), cairnline.ExecutionProfile{
			ID:             "exec_embedded",
			Name:           "Embedded runtime",
			ProviderHint:   "openai",
			ModelHint:      "gpt-5",
			ToolsPolicy:    "block",
			AdapterOptions: map[string]any{"workspace_mode": "worktree", "compact_tool_output": true},
		}); err != nil {
			return err
		}
		_, err := service.CreateProject(t.Context(), cairnline.Project{
			ID:                        projectID,
			Name:                      "Embedded Project",
			Description:               "Read directly from Cairnline.",
			DefaultRootID:             "root_main",
			DefaultProfileID:          "profile_architect",
			DefaultExecutionProfileID: "exec_embedded",
			Roots: []cairnline.Root{{
				ID:        "root_main",
				Path:      "/workspace/embedded",
				Kind:      "git",
				GitBranch: "main",
				Active:    true,
			}},
			ContextSources: []cairnline.Source{{
				ID:         "ctx_agents",
				Kind:       "workspace_instruction",
				Title:      "AGENTS.md",
				Locator:    "AGENTS.md",
				Enabled:    true,
				Format:     "agents_md",
				TrustLabel: "workspace_guidance",
				Metadata:   map[string]string{"root_id": "root_main"},
			}},
		})
		return err
	}); err != nil {
		t.Fatalf("seed embedded Cairnline project: %v", err)
	}

	listed := mustRequestJSON[ProjectsResponse](client, http.MethodGet, "/hecate/v1/projects", "")
	if listed.Object != "projects" || len(listed.Data) != 1 {
		t.Fatalf("projects response = %+v, want one embedded Cairnline project", listed)
	}
	assertStrictEmbeddedProjectProjectionForTest(t, listed.Data[0], projectID)

	detail := mustRequestJSON[ProjectResponse](client, http.MethodGet, "/hecate/v1/projects/"+projectID, "")
	assertStrictEmbeddedProjectProjectionForTest(t, detail.Data, projectID)

	client.mustRequestStatus(http.StatusNotFound, http.MethodGet, "/hecate/v1/projects/proj_missing", "")
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
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+created.Data.ID+"/roots/root_worktree", bytes.NewReader([]byte(`{
		"path":"/workspace/.worktrees/root-mirror-updated",
		"kind":"git_worktree",
		"git_branch":"feature/root-mirror-updated",
		"active":false
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("update root status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	mirrored = getMirroredCairnlineProjectForTest(t, handler, created.Data.ID)
	worktreeRoot := findMirroredCairnlineRootForTest(mirrored.Roots, "root_worktree")
	if worktreeRoot == nil || worktreeRoot.Path != "/workspace/.worktrees/root-mirror-updated" || worktreeRoot.GitBranch != "feature/root-mirror-updated" || worktreeRoot.Active {
		t.Fatalf("mirrored root_worktree after update = %+v in %+v, want inactive updated worktree root", worktreeRoot, mirrored.Roots)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/projects/"+created.Data.ID+"/roots/root_worktree", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete root status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	mirrored = getMirroredCairnlineProjectForTest(t, handler, created.Data.ID)
	if len(mirrored.Roots) != 1 || findMirroredCairnlineRootForTest(mirrored.Roots, "root_worktree") != nil || findMirroredCairnlineRootForTest(mirrored.Roots, "root_main") == nil {
		t.Fatalf("mirrored roots after delete = %+v, want only original root", mirrored.Roots)
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
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+created.Data.ID+"/context-sources/ctx_design", bytes.NewReader([]byte(`{
		"path":"docs/design/proposals/cairnline-portable-project-coordination.md",
		"kind":"doc",
		"title":"Cairnline proposal",
		"enabled":false,
		"trust_label":"workspace_guidance"
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("update source status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	mirrored = getMirroredCairnlineProjectForTest(t, handler, created.Data.ID)
	designSource := findMirroredCairnlineSourceForTest(mirrored.ContextSources, "ctx_design")
	if designSource == nil || designSource.Title != "Cairnline proposal" || designSource.Locator != "docs/design/proposals/cairnline-portable-project-coordination.md" || designSource.Enabled {
		t.Fatalf("mirrored ctx_design after update = %+v in %+v, want disabled Cairnline proposal source", designSource, mirrored.ContextSources)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/projects/"+created.Data.ID+"/context-sources/ctx_design", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete source status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	mirrored = getMirroredCairnlineProjectForTest(t, handler, created.Data.ID)
	if len(mirrored.ContextSources) != 1 || findMirroredCairnlineSourceForTest(mirrored.ContextSources, "ctx_design") != nil || findMirroredCairnlineSourceForTest(mirrored.ContextSources, "ctx_agents") == nil {
		t.Fatalf("mirrored context sources after delete = %+v, want only original AGENTS.md source", mirrored.ContextSources)
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

func TestProjectsAPI_CairnlineIdentityAuthorityCommitsCreateFirst(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineIdentityAuthorityTestServer(t)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{
		"name":"Identity Authority",
		"description":"created through Cairnline",
		"roots":[{"id":"root_main","path":"/workspace/identity","kind":"git","git_branch":"main"}],
		"context_sources":[{"id":"ctx_agents","path":"AGENTS.md","kind":"workspace_instruction","title":"AGENTS.md","format":"agents_md"}],
		"default_provider":"openai",
		"default_model":"gpt-5",
		"default_workspace_mode":"worktree"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Data.Name != "Identity Authority" || created.Data.DefaultRootID != "root_main" || created.Data.DefaultProvider != "openai" || created.Data.DefaultModel != "gpt-5" || len(created.Data.Roots) != 1 || len(created.Data.ContextSources) != 1 {
		t.Fatalf("created project = %+v, want Cairnline-authored project shadowed into Hecate", created.Data)
	}
	if created.Data.ReadBackend != "cairnline" {
		t.Fatalf("created read_backend = %q, want Cairnline-authoritative mutation response", created.Data.ReadBackend)
	}

	mirrored := getMirroredCairnlineProjectForTest(t, handler, created.Data.ID)
	if mirrored.Name != "Identity Authority" || mirrored.Description != "created through Cairnline" || mirrored.DefaultRootID != "root_main" || len(mirrored.Roots) != 1 || len(mirrored.ContextSources) != 1 {
		t.Fatalf("mirrored project = %+v, want identity create committed to Cairnline", mirrored)
	}
	assertMirroredExecutionProfileForTest(t, handler, mirrored.DefaultExecutionProfileID, "openai", "gpt-5")
}

func TestProjectsAPI_CairnlineReplacementModeCreatesCairnlineOnlyIdentity(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineReplacementIdentityAuthorityTestServer(t)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{
		"name":"Replacement Identity",
		"description":"created only in Cairnline",
		"roots":[{"id":"root_main","path":"/workspace/replacement","kind":"git","git_branch":"main"}],
		"default_provider":"openai",
		"default_model":"gpt-5"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Data.Name != "Replacement Identity" || created.Data.DefaultRootID != "root_main" || created.Data.DefaultProvider != "openai" || created.Data.DefaultModel != "gpt-5" {
		t.Fatalf("created project = %+v, want Cairnline-authored replacement identity", created.Data)
	}
	if created.Data.ReadBackend != "cairnline" {
		t.Fatalf("created read_backend = %q, want Cairnline-authoritative replacement response", created.Data.ReadBackend)
	}
	if _, ok, err := handler.projects.Get(t.Context(), created.Data.ID); err != nil || ok {
		t.Fatalf("Hecate project store ok=%v err=%v after replacement create, want no native identity row", ok, err)
	}
	mirrored := getMirroredCairnlineProjectForTest(t, handler, created.Data.ID)
	if mirrored.Name != "Replacement Identity" || mirrored.Description != "created only in Cairnline" || mirrored.DefaultRootID != "root_main" || len(mirrored.Roots) != 1 {
		t.Fatalf("mirrored project = %+v, want Cairnline-only replacement identity", mirrored)
	}
	assertMirroredExecutionProfileForTest(t, handler, mirrored.DefaultExecutionProfileID, "openai", "gpt-5")

	listed := listProjectsForTest(t, server)
	if len(listed) != 1 || listed[0].ID != created.Data.ID || listed[0].ReadBackend != "cairnline" {
		t.Fatalf("listed projects = %+v, want strict embedded Cairnline read of replacement identity", listed)
	}
	detail := getProjectForTest(t, server, created.Data.ID)
	if detail.ID != created.Data.ID || detail.ReadBackend != "cairnline" || detail.Name != "Replacement Identity" {
		t.Fatalf("project detail = %+v, want strict embedded Cairnline replacement identity", detail)
	}
}

func TestProjectsAPI_CairnlineReplacementModeRejectsDuplicateNameAndRootPath(t *testing.T) {
	t.Parallel()
	_, server := newProjectsCairnlineReplacementIdentityAuthorityTestServer(t)
	client := newAPITestClient(t, server)

	created := mustRequestJSONStatus[ProjectResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects", `{
		"name":"Replacement Duplicate",
		"roots":[{"id":"root_main","path":"/workspace/replacement-duplicate","kind":"git"}]
	}`)

	nameConflict := client.mustRequestStatus(http.StatusConflict, http.MethodPost, "/hecate/v1/projects", `{
		"name":"Replacement Duplicate",
		"roots":[{"id":"root_other","path":"/workspace/replacement-duplicate-other","kind":"git"}]
	}`)
	if !strings.Contains(nameConflict.Body.String(), "project name") {
		t.Fatalf("duplicate name response = %s, want project name conflict", nameConflict.Body.String())
	}

	rootConflict := client.mustRequestStatus(http.StatusConflict, http.MethodPost, "/hecate/v1/projects", fmt.Sprintf(`{
		"name":"Replacement Duplicate Root",
		"roots":[{"id":"root_conflict","path":%q,"kind":"git"}]
	}`, created.Data.Roots[0].Path))
	if !strings.Contains(rootConflict.Body.String(), "project root path") {
		t.Fatalf("duplicate root response = %s, want project root path conflict", rootConflict.Body.String())
	}

	other := mustRequestJSONStatus[ProjectResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects", `{
		"name":"Replacement Unique",
		"roots":[{"id":"root_unique","path":"/workspace/replacement-unique","kind":"git"}]
	}`)
	renameConflict := client.mustRequestStatus(http.StatusConflict, http.MethodPatch, "/hecate/v1/projects/"+other.Data.ID, `{
		"name":"Replacement Duplicate"
	}`)
	if !strings.Contains(renameConflict.Body.String(), "project name") {
		t.Fatalf("rename conflict response = %s, want project name conflict", renameConflict.Body.String())
	}
	updateRootConflict := client.mustRequestStatus(http.StatusConflict, http.MethodPatch, "/hecate/v1/projects/"+other.Data.ID, fmt.Sprintf(`{
		"roots":[{"id":"root_unique","path":%q,"kind":"git"}]
	}`, created.Data.Roots[0].Path))
	if !strings.Contains(updateRootConflict.Body.String(), "project root path") {
		t.Fatalf("update root conflict response = %s, want project root path conflict", updateRootConflict.Body.String())
	}
}

func TestProjectsAPI_CairnlineReplacementModeWithoutPortableAuthorityKeepsNativeIdentityShadow(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend:      "cairnline",
			CairnlineConnector:       "embedded",
			CairnlineReadSource:      "embedded",
			CairnlineWriteAuthority:  projectCairnlineWriteAuthorityProjectIdentity,
			CairnlineReplacementMode: "embedded",
		},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	server := NewServer(quietLogger(), handler)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{
		"name":"Replacement Identity With Shadow",
		"description":"replacement mode is armed, but portable write authority is incomplete",
		"default_provider":"openai",
		"default_model":"gpt-5"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if _, ok, err := handler.projects.Get(t.Context(), created.Data.ID); err != nil || !ok {
		t.Fatalf("Hecate project store ok=%v err=%v after replacement-mode create, want compatibility identity shadow while portable authority is incomplete", ok, err)
	}
	status := handler.projectCoordinationBackendStatusWithContext(t.Context())
	if status.ReplacementReady {
		t.Fatalf("replacement_ready = true, want false while only project identity authority is enabled")
	}
	if !containsString(status.PortableWriteGaps, "memory") || !containsString(status.PortableWriteGaps, "work-items") {
		t.Fatalf("portable write gaps = %+v, want remaining portable blockers", status.PortableWriteGaps)
	}
}

func TestProjectsAPI_CairnlineIdentityAuthorityCommitsDeleteFirst(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineIdentityAuthorityTestServer(t)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{
		"name":"Identity Delete Authority",
		"roots":[{"id":"root_main","path":"/workspace/delete-authority","kind":"git"}],
		"default_provider":"openai",
		"default_model":"gpt-5"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if mirrored := getMirroredCairnlineProjectForTest(t, handler, created.Data.ID); mirrored.ID != created.Data.ID {
		t.Fatalf("mirrored project = %+v, want created project before delete", mirrored)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/projects/"+created.Data.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if _, ok, err := handler.projects.Get(t.Context(), created.Data.ID); err != nil || ok {
		t.Fatalf("Hecate project after delete ok=%v err=%v, want missing", ok, err)
	}
	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror after delete: %v", err)
	}
	defer store.Close()
	if _, err := service.GetProject(t.Context(), created.Data.ID); !errors.Is(err, cairnline.ErrNotFound) {
		t.Fatalf("Cairnline project after delete error = %v, want ErrNotFound", err)
	}
}

func TestProjectsAPI_CairnlineIdentityAuthorityDeletesCairnlineOnlyProject(t *testing.T) {
	t.Parallel()
	const projectID = "proj_identity_delete_cairnline_only"
	handler, server := newProjectsCairnlineIdentityAuthorityTestServer(t)
	handler.SetProjectWorkStore(projectwork.NewMemoryStore())
	chatStore := chat.NewMemoryStore()
	handler.SetAgentChatStore(chatStore)
	runner := &fakeAgentChatRunner{}
	handler.SetAgentChatRunner(runner)
	if err := handler.withCairnlineEmbeddedMirrorService(t.Context(), func(service *cairnline.Service) error {
		_, err := service.CreateProject(t.Context(), cairnline.Project{
			ID:   projectID,
			Name: "Cairnline-only delete",
		})
		return err
	}); err != nil {
		t.Fatalf("seed Cairnline-only project: %v", err)
	}
	if _, err := handler.projectWork.CreateWorkItem(t.Context(), projectwork.WorkItem{
		ID:        "work_shadow",
		ProjectID: projectID,
		Title:     "Shadow cleanup",
	}); err != nil {
		t.Fatalf("seed Hecate shadow work item: %v", err)
	}
	if _, err := chatStore.Create(t.Context(), chat.Session{
		ID:        "chat_cairnline_only_project",
		ProjectID: projectID,
		AgentID:   chat.DefaultAgentID,
	}); err != nil {
		t.Fatalf("seed Hecate chat shadow: %v", err)
	}
	if _, err := chatStore.Create(t.Context(), chat.Session{
		ID:              "chat_cairnline_only_external",
		ProjectID:       projectID,
		AgentID:         "codex",
		DriverKind:      "acp",
		NativeSessionID: "native_cairnline_only_external",
	}); err != nil {
		t.Fatalf("seed external chat shadow: %v", err)
	}
	if _, err := chatStore.Create(t.Context(), chat.Session{
		ID:        "chat_other_project",
		ProjectID: "proj_other",
		AgentID:   chat.DefaultAgentID,
	}); err != nil {
		t.Fatalf("seed unrelated chat: %v", err)
	}
	if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("native project exists = %t err=%v, want missing before delete", ok, err)
	}

	deleted := mustRequestJSONStatus[ProjectDeleteResponse](newAPITestClient(t, server), http.StatusOK, http.MethodDelete, "/hecate/v1/projects/"+projectID, "")
	if deleted.Data.ProjectID != projectID || deleted.Data.ProjectName != "Cairnline-only delete" || deleted.Data.ProjectWorkRowsDeleted != 1 || deleted.Data.ChatSessionsDeleted != 2 {
		t.Fatalf("delete response = %+v, want Cairnline project identity plus shadow work/chat cleanup", deleted.Data)
	}
	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror after delete: %v", err)
	}
	defer store.Close()
	if _, err := service.GetProject(t.Context(), projectID); !errors.Is(err, cairnline.ErrNotFound) {
		t.Fatalf("Cairnline project after delete error = %v, want ErrNotFound", err)
	}
	workItems, err := handler.projectWork.ListWorkItems(t.Context(), projectID)
	if err != nil {
		t.Fatalf("ListWorkItems after delete: %v", err)
	}
	if len(workItems) != 0 {
		t.Fatalf("shadow work items after delete = %+v, want none", workItems)
	}
	sessions, err := chatStore.List(t.Context())
	if err != nil {
		t.Fatalf("List chat sessions after delete: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != "chat_other_project" {
		t.Fatalf("chat sessions after delete = %+v, want only unrelated chat", sessions)
	}
	if len(runner.deletedSessions) != 1 || runner.deletedSessions[0] != "chat_cairnline_only_external" {
		t.Fatalf("deleted native sessions = %#v, want external project chat deleted", runner.deletedSessions)
	}
}

func TestProjectsAPI_CairnlineIdentityAuthorityRollsBackCairnlineOnlyDeleteWhenShadowCleanupFails(t *testing.T) {
	t.Parallel()
	const projectID = "proj_identity_delete_cairnline_only_rollback"
	handler, server := newProjectsCairnlineIdentityAuthorityTestServer(t)
	handler.SetProjectSkillStore(failingProjectSkillDeleteStore{
		Store: projectskills.NewMemoryStore(),
		err:   errors.New("skill cleanup failed"),
	})
	if err := handler.withCairnlineEmbeddedMirrorService(t.Context(), func(service *cairnline.Service) error {
		_, err := service.CreateProject(t.Context(), cairnline.Project{
			ID:   projectID,
			Name: "Cairnline-only rollback",
		})
		return err
	}); err != nil {
		t.Fatalf("seed Cairnline-only project: %v", err)
	}
	if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("native project exists = %t err=%v, want missing before delete", ok, err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/projects/"+projectID, nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete status = %d body=%s, want 500", rec.Code, rec.Body.String())
	}
	mirrored := getMirroredCairnlineProjectForTest(t, handler, projectID)
	if mirrored.Name != "Cairnline-only rollback" {
		t.Fatalf("rolled-back Cairnline project = %+v, want restored project", mirrored)
	}
}

func TestProjectsAPI_CairnlineIdentityAuthorityRollsBackDeleteWhenCompatibilityCleanupFails(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineIdentityAuthorityTestServer(t)
	handler.SetProjectSkillStore(failingProjectSkillDeleteStore{
		Store: projectskills.NewMemoryStore(),
		err:   errors.New("skill cleanup failed"),
	})

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{
		"name":"Identity Delete Rollback",
		"roots":[{"id":"root_main","path":"/workspace/delete-rollback","kind":"git"}],
		"default_provider":"openai",
		"default_model":"gpt-5"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/projects/"+created.Data.ID, nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete status = %d body=%s, want 500", rec.Code, rec.Body.String())
	}
	if _, ok, err := handler.projects.Get(t.Context(), created.Data.ID); err != nil || !ok {
		t.Fatalf("Hecate project after failed delete ok=%v err=%v, want retained", ok, err)
	}
	mirrored := getMirroredCairnlineProjectForTest(t, handler, created.Data.ID)
	if mirrored.Name != "Identity Delete Rollback" || findMirroredCairnlineRootForTest(mirrored.Roots, "root_main") == nil {
		t.Fatalf("rolled-back Cairnline project = %+v, want restored project snapshot", mirrored)
	}
}

func TestProjectsAPI_CairnlineIdentityAuthorityRejectsDuplicateNameBeforeCommit(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineIdentityAuthorityTestServer(t)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{"name":"Existing"}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create existing status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{"name":" existing "}`))))
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate create status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "project name") {
		t.Fatalf("duplicate create body = %s, want project name conflict", rec.Body.String())
	}
	items := listMirroredCairnlineProjectsForTest(t, handler)
	if len(items) != 1 || items[0].Name != "Existing" {
		t.Fatalf("mirrored projects = %+v, want rejected duplicate create to leave only original Cairnline row", items)
	}
}

func TestProjectsAPI_CairnlineMetadataDefaultsAuthorityCommitsScopedUpdatesFirst(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineMetadataDefaultsAuthorityTestServer(t)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{
		"name":"Authority Project",
		"description":"initial",
		"roots":[{"id":"root_main","path":"/workspace/main","kind":"git","git_branch":"main"}],
		"default_provider":"openai",
		"default_model":"gpt-5"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+created.Data.ID, bytes.NewReader([]byte(`{
		"name":"Authority Rename",
		"description":"Cairnline owns scoped project settings.",
		"default_root_id":"root_main",
		"default_provider":"anthropic",
		"default_model":"claude-sonnet-4-5",
		"default_agent_profile":"architecture",
		"default_tools_enabled":false,
		"default_workspace_mode":"worktree",
		"default_system_prompt":"Use the portable project context.",
		"default_compact_tool_output":true
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("metadata/default patch status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var updated ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode metadata/default patch response: %v", err)
	}
	if updated.Data.Name != "Authority Rename" || updated.Data.DefaultProvider != "anthropic" || updated.Data.DefaultModel != "claude-sonnet-4-5" || updated.Data.DefaultAgentProfile != "architecture" || updated.Data.DefaultWorkspaceMode != "worktree" || updated.Data.DefaultSystemPrompt != "Use the portable project context." {
		t.Fatalf("updated project = %+v, want Cairnline-authority settings shadowed into Hecate", updated.Data)
	}
	if updated.Data.DefaultToolsEnabled == nil || *updated.Data.DefaultToolsEnabled {
		t.Fatalf("default_tools_enabled = %+v, want false", updated.Data.DefaultToolsEnabled)
	}
	if updated.Data.DefaultCompactToolOutput == nil || !*updated.Data.DefaultCompactToolOutput {
		t.Fatalf("default_compact_tool_output = %+v, want true", updated.Data.DefaultCompactToolOutput)
	}

	mirrored := getMirroredCairnlineProjectForTest(t, handler, created.Data.ID)
	if mirrored.Name != "Authority Rename" || mirrored.Description != "Cairnline owns scoped project settings." || mirrored.DefaultRootID != "root_main" || mirrored.DefaultProfileID != "architecture" {
		t.Fatalf("mirrored project = %+v, want scoped metadata/default authority", mirrored)
	}
	if len(mirrored.Roots) != 1 || mirrored.Roots[0].ID != "root_main" {
		t.Fatalf("mirrored roots = %+v, want existing root preserved", mirrored.Roots)
	}
	assertMirroredExecutionProfileForTest(t, handler, mirrored.DefaultExecutionProfileID, "anthropic", "claude-sonnet-4-5")

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+created.Data.ID, bytes.NewReader([]byte(`{
		"name":"Mixed Root Replacement",
		"roots":[{"id":"root_replaced","path":"/workspace/replaced","kind":"git","git_branch":"feature/root"}]
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("mixed root patch status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode mixed root patch response: %v", err)
	}
	if updated.Data.Name != "Mixed Root Replacement" || len(updated.Data.Roots) != 1 || updated.Data.Roots[0].ID != "root_replaced" {
		t.Fatalf("mixed root patch project = %+v, want Hecate-owned root replacement path", updated.Data)
	}
	mirrored = getMirroredCairnlineProjectForTest(t, handler, created.Data.ID)
	if mirrored.Name != "Mixed Root Replacement" || len(mirrored.Roots) != 1 || mirrored.Roots[0].ID != "root_replaced" {
		t.Fatalf("mirrored mixed root patch = %+v, want root replacement still mirrored through Hecate-owned path", mirrored)
	}
}

func TestProjectsAPI_CairnlineMetadataDefaultsAuthorityRejectsDuplicateNameBeforeCommit(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineMetadataDefaultsAuthorityTestServer(t)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{"name":"Existing"}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create existing status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{"name":"Target"}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create target status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var target ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &target); err != nil {
		t.Fatalf("decode target response: %v", err)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+target.Data.ID, bytes.NewReader([]byte(`{"name":" existing "}`))))
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate metadata authority rename status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "project name") {
		t.Fatalf("duplicate metadata authority rename body = %s, want project name conflict", rec.Body.String())
	}
	mirrored := getMirroredCairnlineProjectForTest(t, handler, target.Data.ID)
	if mirrored.Name != "Target" {
		t.Fatalf("mirrored target name = %q, want rejected duplicate rename to leave Cairnline row unchanged", mirrored.Name)
	}
}

func TestProjectsAPI_CairnlineRootAuthorityCommitsDirectMutationsFirst(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineRootSourceAuthorityTestServer(t)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{
		"name":"Root Authority",
		"roots":[{"id":"root_main","path":"/workspace/root-main","kind":"git","git_branch":"main"}]
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+created.Data.ID+"/roots", bytes.NewReader([]byte(`{
		"id":"root_feature",
		"path":"/workspace/root-feature",
		"kind":"git_worktree",
		"git_branch":"feature/cairnline-roots",
		"active":false
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create root status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var withRoot ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &withRoot); err != nil {
		t.Fatalf("decode root create response: %v", err)
	}
	if len(withRoot.Data.Roots) != 2 || withRoot.Data.DefaultRootID != "root_main" {
		t.Fatalf("root create response = %+v, want Hecate compatibility row shadowed with two roots and stable default", withRoot.Data)
	}
	mirrored := getMirroredCairnlineProjectForTest(t, handler, created.Data.ID)
	if feature := findMirroredCairnlineRootForTest(mirrored.Roots, "root_feature"); feature == nil || feature.Path != "/workspace/root-feature" || feature.Active {
		t.Fatalf("mirrored root_feature = %+v in %+v, want inactive created root", feature, mirrored.Roots)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+created.Data.ID+"/roots/root_feature", bytes.NewReader([]byte(`{
		"path":"/workspace/root-feature-updated",
		"kind":"git_worktree",
		"git_branch":"feature/cairnline-roots-updated",
		"active":true
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("update root status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &withRoot); err != nil {
		t.Fatalf("decode root update response: %v", err)
	}
	if feature := findProjectResponseRootForTest(withRoot.Data.Roots, "root_feature"); feature == nil || feature.Path != "/workspace/root-feature-updated" || !feature.Active {
		t.Fatalf("updated response root_feature = %+v in %+v, want active updated root", feature, withRoot.Data.Roots)
	}
	mirrored = getMirroredCairnlineProjectForTest(t, handler, created.Data.ID)
	if feature := findMirroredCairnlineRootForTest(mirrored.Roots, "root_feature"); feature == nil || feature.Path != "/workspace/root-feature-updated" || !feature.Active {
		t.Fatalf("updated mirrored root_feature = %+v in %+v, want active updated root", feature, mirrored.Roots)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/projects/"+created.Data.ID+"/roots/root_main", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete root status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &withRoot); err != nil {
		t.Fatalf("decode root delete response: %v", err)
	}
	if len(withRoot.Data.Roots) != 1 || withRoot.Data.Roots[0].ID != "root_feature" || withRoot.Data.DefaultRootID != "root_feature" {
		t.Fatalf("root delete response = %+v, want remaining root promoted to default", withRoot.Data)
	}
	mirrored = getMirroredCairnlineProjectForTest(t, handler, created.Data.ID)
	if len(mirrored.Roots) != 1 || mirrored.Roots[0].ID != "root_feature" || mirrored.DefaultRootID != "root_feature" {
		t.Fatalf("mirrored roots after delete = %+v default=%q, want remaining root promoted", mirrored.Roots, mirrored.DefaultRootID)
	}
}

func TestProjectsAPI_CairnlineRootAuthorityRejectsDuplicatePathBeforeCommit(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineRootSourceAuthorityTestServer(t)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{
		"name":"Existing Root Project",
		"roots":[{"id":"root_existing","path":"/workspace/shared-root","kind":"git"}]
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create existing status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{"name":"Root Target"}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create target status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var target ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &target); err != nil {
		t.Fatalf("decode target response: %v", err)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+target.Data.ID+"/roots", bytes.NewReader([]byte(`{
		"id":"root_conflict",
		"path":"/workspace/shared-root/",
		"kind":"git"
	}`))))
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate root path status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}
	mirrored := getMirroredCairnlineProjectForTest(t, handler, target.Data.ID)
	if findMirroredCairnlineRootForTest(mirrored.Roots, "root_conflict") != nil {
		t.Fatalf("mirrored roots = %+v, want rejected duplicate root path to leave Cairnline row unchanged", mirrored.Roots)
	}
}

func TestProjectsAPI_CairnlineRootAuthorityCommitsListReplacementFirst(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineRootSourceAuthorityTestServer(t)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{
		"name":"Root List Authority",
		"roots":[
			{"id":"root_main","path":"/workspace/root-list-main","kind":"git","git_branch":"main"},
			{"id":"root_old","path":"/workspace/root-list-old","kind":"git_worktree","git_branch":"old"}
		],
		"default_root_id":"root_old"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+created.Data.ID, bytes.NewReader([]byte(`{
		"roots":[
			{"id":"root_next","path":"/workspace/root-list-next","kind":"git_worktree","git_branch":"next"},
			{"id":"root_docs","path":"/workspace/root-list-docs","kind":"directory","active":false}
		]
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("replace roots status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var updated ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode root replacement response: %v", err)
	}
	if len(updated.Data.Roots) != 2 || updated.Data.Roots[0].ID != "root_next" || updated.Data.Roots[1].ID != "root_docs" || updated.Data.DefaultRootID != "root_next" {
		t.Fatalf("root replacement response = %+v, want replacement roots with first root promoted to default", updated.Data)
	}
	mirrored := getMirroredCairnlineProjectForTest(t, handler, created.Data.ID)
	if len(mirrored.Roots) != 2 || mirrored.Roots[0].ID != "root_next" || mirrored.Roots[1].ID != "root_docs" || mirrored.DefaultRootID != "root_next" {
		t.Fatalf("mirrored roots after list replacement = %+v default=%q, want replacement committed to Cairnline first", mirrored.Roots, mirrored.DefaultRootID)
	}
}

func TestProjectsAPI_CairnlineContextSourceAuthorityCommitsDirectMutationsFirst(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineRootSourceAuthorityTestServer(t)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{"name":"Source Authority"}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+created.Data.ID+"/context-sources", bytes.NewReader([]byte(`{
		"id":"ctx_agents",
		"path":"AGENTS.md",
		"kind":"workspace_instruction",
		"title":"AGENTS.md",
		"format":"agents_md",
		"scope":"workspace",
		"trust_label":"workspace_guidance"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create source status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var withSource ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &withSource); err != nil {
		t.Fatalf("decode source create response: %v", err)
	}
	if len(withSource.Data.ContextSources) != 1 || withSource.Data.ContextSources[0].ID != "ctx_agents" {
		t.Fatalf("source create response = %+v, want ctx_agents", withSource.Data.ContextSources)
	}
	mirrored := getMirroredCairnlineProjectForTest(t, handler, created.Data.ID)
	if source := findMirroredCairnlineSourceForTest(mirrored.ContextSources, "ctx_agents"); source == nil || source.Locator != "AGENTS.md" || source.Format != "agents_md" {
		t.Fatalf("mirrored ctx_agents = %+v in %+v, want created source", source, mirrored.ContextSources)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+created.Data.ID+"/context-sources/ctx_agents", bytes.NewReader([]byte(`{
		"path":"docs/AGENTS.md",
		"kind":"workspace_instruction",
		"title":"Project agents",
		"format":"agents_md",
		"scope":"project",
		"trust_label":"workspace_guidance",
		"enabled":false
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("update source status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &withSource); err != nil {
		t.Fatalf("decode source update response: %v", err)
	}
	if source := findProjectResponseContextSourceForTest(withSource.Data.ContextSources, "ctx_agents"); source == nil || source.Path != "docs/AGENTS.md" || source.Enabled {
		t.Fatalf("updated response ctx_agents = %+v in %+v, want disabled updated source", source, withSource.Data.ContextSources)
	}
	mirrored = getMirroredCairnlineProjectForTest(t, handler, created.Data.ID)
	if source := findMirroredCairnlineSourceForTest(mirrored.ContextSources, "ctx_agents"); source == nil || source.Locator != "docs/AGENTS.md" || source.Enabled {
		t.Fatalf("updated mirrored ctx_agents = %+v in %+v, want disabled updated source", source, mirrored.ContextSources)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+created.Data.ID+"/context-sources", bytes.NewReader([]byte(`{
		"id":"ctx_agents",
		"path":"duplicate.md"
	}`))))
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate source id status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/projects/"+created.Data.ID+"/context-sources/ctx_agents", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete source status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &withSource); err != nil {
		t.Fatalf("decode source delete response: %v", err)
	}
	if len(withSource.Data.ContextSources) != 0 {
		t.Fatalf("source delete response = %+v, want no sources", withSource.Data.ContextSources)
	}
	mirrored = getMirroredCairnlineProjectForTest(t, handler, created.Data.ID)
	if len(mirrored.ContextSources) != 0 {
		t.Fatalf("mirrored sources after delete = %+v, want none", mirrored.ContextSources)
	}
}

func TestProjectsAPI_CairnlineContextSourceAuthorityCommitsListReplacementFirst(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineRootSourceAuthorityTestServer(t)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{
		"name":"Source List Authority",
		"context_sources":[
			{"id":"ctx_old","path":"AGENTS.md","kind":"workspace_instruction","title":"Old agents","format":"agents_md","scope":"workspace","trust_label":"workspace_guidance"}
		]
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+created.Data.ID, bytes.NewReader([]byte(`{
		"context_sources":[
			{"id":"ctx_agents","path":"AGENTS.md","kind":"workspace_instruction","title":"AGENTS.md","format":"agents_md","scope":"workspace","trust_label":"workspace_guidance"},
			{"id":"ctx_claude","path":"CLAUDE.md","kind":"workspace_instruction","title":"CLAUDE.md","format":"claude_md","scope":"workspace","trust_label":"workspace_guidance","enabled":false}
		]
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("replace sources status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var updated ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode source replacement response: %v", err)
	}
	if len(updated.Data.ContextSources) != 2 || updated.Data.ContextSources[0].ID != "ctx_agents" || updated.Data.ContextSources[1].ID != "ctx_claude" || updated.Data.ContextSources[1].Enabled {
		t.Fatalf("source replacement response = %+v, want replacement sources with disabled ctx_claude", updated.Data.ContextSources)
	}
	mirrored := getMirroredCairnlineProjectForTest(t, handler, created.Data.ID)
	if len(mirrored.ContextSources) != 2 || mirrored.ContextSources[0].ID != "ctx_agents" || mirrored.ContextSources[1].ID != "ctx_claude" || mirrored.ContextSources[1].Enabled {
		t.Fatalf("mirrored sources after list replacement = %+v, want replacement committed to Cairnline first", mirrored.ContextSources)
	}
}

func TestProjectsAPI_DefaultOnlyPatchMirrorsDefaultsWithoutReplacingCairnlineState(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineMirrorTestServer(t)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{
		"name":"Defaults Mirror",
		"roots":[{"id":"root_main","path":"/workspace/main","kind":"git"}],
		"context_sources":[{"id":"ctx_agents","path":"AGENTS.md","kind":"workspace_instruction","title":"AGENTS.md"}],
		"default_provider":"openai",
		"default_model":"gpt-5"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	seedCairnlineOnlyProjectGraphForTest(t, handler, created.Data.ID)

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+created.Data.ID, bytes.NewReader([]byte(`{
		"default_provider":"anthropic",
		"default_model":"claude-sonnet-4-5",
		"default_agent_profile":"architecture",
		"default_tools_enabled":false,
		"default_workspace_mode":"worktree"
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("defaults update status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	mirrored := getMirroredCairnlineProjectForTest(t, handler, created.Data.ID)
	if mirrored.DefaultProfileID != "architecture" {
		t.Fatalf("mirrored default profile = %q, want architecture", mirrored.DefaultProfileID)
	}
	if findMirroredCairnlineRootForTest(mirrored.Roots, "root_cairnline_only") == nil {
		t.Fatalf("mirrored roots = %+v, want Cairnline-only root preserved", mirrored.Roots)
	}
	if findMirroredCairnlineSourceForTest(mirrored.ContextSources, "ctx_cairnline_only") == nil {
		t.Fatalf("mirrored context sources = %+v, want Cairnline-only source preserved", mirrored.ContextSources)
	}
	assertMirroredExecutionProfileForTest(t, handler, mirrored.DefaultExecutionProfileID, "anthropic", "claude-sonnet-4-5")
}

func TestProjectsAPI_DefaultRootPatchCairnlineAuthorityUsesCairnlineOnlyRoots(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend:     "cairnline",
			CairnlineReadSource:     "embedded",
			CairnlineWriteAuthority: projectCairnlineWriteAuthorityProjectMetadataDefaults,
		},
	}, quietLogger(), nil, nil, nil, nil)
	server := NewServer(quietLogger(), handler)
	const projectID = "proj_defaults_cairnline_only_root"
	if err := handler.withCairnlineEmbeddedMirrorService(t.Context(), func(service *cairnline.Service) error {
		_, err := service.CreateProject(t.Context(), cairnline.Project{
			ID:            projectID,
			Name:          "Defaults Cairnline Only Root",
			DefaultRootID: "root_main",
			Roots: []cairnline.Root{
				{ID: "root_main", Path: "/workspace/main", Kind: "git", Active: true},
				{ID: "root_next", Path: "/workspace/next", Kind: "git_worktree", Active: true},
			},
		})
		return err
	}); err != nil {
		t.Fatalf("seed Cairnline-only project: %v", err)
	}
	if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("Hecate project store ok=%v err=%v before patch, want no native project row", ok, err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+projectID, bytes.NewReader([]byte(`{
		"default_root_id":"root_next",
		"default_provider":"anthropic",
		"default_model":"claude-sonnet-4-5"
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("defaults update status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var updated ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if updated.Data.DefaultRootID != "root_next" || updated.Data.DefaultProvider != "anthropic" || updated.Data.DefaultModel != "claude-sonnet-4-5" {
		t.Fatalf("updated defaults = root %q provider/model %q/%q, want root_next anthropic/claude-sonnet-4-5", updated.Data.DefaultRootID, updated.Data.DefaultProvider, updated.Data.DefaultModel)
	}
	mirrored := getMirroredCairnlineProjectForTest(t, handler, projectID)
	if mirrored.DefaultRootID != "root_next" {
		t.Fatalf("mirrored default root = %q, want root_next", mirrored.DefaultRootID)
	}
	if findMirroredCairnlineRootForTest(mirrored.Roots, "root_next") == nil {
		t.Fatalf("mirrored roots = %+v, want root_next preserved", mirrored.Roots)
	}
	if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("Hecate project store ok=%v err=%v after patch, want no native project row", ok, err)
	}
}

func TestProjectsAPI_MetadataPatchMirrorsMetadataWithoutReplacingCairnlineState(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineMirrorTestServer(t)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{
		"name":"Metadata Mirror",
		"description":"Before",
		"roots":[{"id":"root_main","path":"/workspace/main","kind":"git"}],
		"context_sources":[{"id":"ctx_agents","path":"AGENTS.md","kind":"workspace_instruction","title":"AGENTS.md"}],
		"default_provider":"openai",
		"default_model":"gpt-5"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	seedCairnlineOnlyProjectGraphForTest(t, handler, created.Data.ID)

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+created.Data.ID, bytes.NewReader([]byte(`{
		"name":"Metadata Mirror Renamed",
		"description":"After",
		"default_provider":"anthropic",
		"default_model":"claude-sonnet-4-5",
		"default_agent_profile":"architecture"
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("metadata update status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	mirrored := getMirroredCairnlineProjectForTest(t, handler, created.Data.ID)
	if mirrored.Name != "Metadata Mirror Renamed" || mirrored.Description != "After" {
		t.Fatalf("mirrored metadata = %+v, want renamed project with updated description", mirrored)
	}
	if mirrored.DefaultProfileID != "architecture" {
		t.Fatalf("mirrored default profile = %q, want architecture", mirrored.DefaultProfileID)
	}
	if findMirroredCairnlineRootForTest(mirrored.Roots, "root_cairnline_only") == nil {
		t.Fatalf("mirrored roots = %+v, want Cairnline-only root preserved", mirrored.Roots)
	}
	if findMirroredCairnlineSourceForTest(mirrored.ContextSources, "ctx_cairnline_only") == nil {
		t.Fatalf("mirrored context sources = %+v, want Cairnline-only source preserved", mirrored.ContextSources)
	}
	assertMirroredExecutionProfileForTest(t, handler, mirrored.DefaultExecutionProfileID, "anthropic", "claude-sonnet-4-5")
}

func TestProjectsAPI_RootListPatchMirrorsRootReplacementWithoutReplacingSources(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineMirrorTestServer(t)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{
		"name":"Root Replace Mirror",
		"roots":[{"id":"root_main","path":"/workspace/main","kind":"git"}],
		"context_sources":[{"id":"ctx_agents","path":"AGENTS.md","kind":"workspace_instruction","title":"AGENTS.md"}]
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	seedCairnlineOnlyProjectGraphForTest(t, handler, created.Data.ID)

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+created.Data.ID, bytes.NewReader([]byte(`{
		"roots":[{"id":"root_replacement","path":"/workspace/replacement","kind":"git","active":true}]
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("roots update status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	mirrored := getMirroredCairnlineProjectForTest(t, handler, created.Data.ID)
	if len(mirrored.Roots) != 1 || mirrored.Roots[0].ID != "root_replacement" || mirrored.DefaultRootID != "root_replacement" {
		t.Fatalf("mirrored roots = %+v default=%q, want replacement root only", mirrored.Roots, mirrored.DefaultRootID)
	}
	if findMirroredCairnlineSourceForTest(mirrored.ContextSources, "ctx_cairnline_only") == nil {
		t.Fatalf("mirrored context sources = %+v, want Cairnline-only source preserved", mirrored.ContextSources)
	}
}

func TestProjectsAPI_ContextSourceListPatchMirrorsSourceReplacementWithoutReplacingRoots(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineMirrorTestServer(t)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{
		"name":"Source Replace Mirror",
		"roots":[{"id":"root_main","path":"/workspace/main","kind":"git"}],
		"context_sources":[{"id":"ctx_agents","path":"AGENTS.md","kind":"workspace_instruction","title":"AGENTS.md"}]
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	seedCairnlineOnlyProjectGraphForTest(t, handler, created.Data.ID)

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+created.Data.ID, bytes.NewReader([]byte(`{
		"context_sources":[{"id":"ctx_replacement","path":"docs/replacement.md","kind":"doc","title":"Replacement"}]
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("sources update status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	mirrored := getMirroredCairnlineProjectForTest(t, handler, created.Data.ID)
	if len(mirrored.ContextSources) != 1 || mirrored.ContextSources[0].ID != "ctx_replacement" {
		t.Fatalf("mirrored context sources = %+v, want replacement source only", mirrored.ContextSources)
	}
	if findMirroredCairnlineRootForTest(mirrored.Roots, "root_cairnline_only") == nil {
		t.Fatalf("mirrored roots = %+v, want Cairnline-only root preserved", mirrored.Roots)
	}
}

func TestProjectChildMirrorsPreserveCairnlineProjectGraph(t *testing.T) {
	t.Parallel()
	handler, _ := newProjectsCairnlineMirrorTestServer(t)
	now := time.Date(2026, 6, 28, 10, 30, 0, 0, time.UTC)
	project, err := handler.projects.Create(t.Context(), projects.Project{
		ID:   "proj_child_mirror",
		Name: "Child Mirror",
		Roots: []projects.Root{{
			ID:     "root_main",
			Path:   "/workspace/main",
			Kind:   "git",
			Active: true,
		}},
		ContextSources: []projects.ContextSource{{
			ID:      "ctx_agents",
			Kind:    "workspace_instruction",
			Title:   "AGENTS.md",
			Path:    "AGENTS.md",
			Enabled: true,
		}},
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("Create project: %v", err)
	}
	if err := handler.writeProjectIdentityToCairnline(t.Context(), project); err != nil {
		t.Fatalf("write initial Cairnline project: %v", err)
	}
	seedCairnlineOnlyProjectGraphForTest(t, handler, project.ID)

	role := projectwork.AgentRoleProfile{
		ID:                "architect",
		ProjectID:         project.ID,
		Name:              "Architect",
		DefaultDriverKind: projectwork.AssignmentDriverHecateTask,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := handler.writeProjectRoleToCairnline(t.Context(), project, role); err != nil {
		t.Fatalf("write role mirror: %v", err)
	}
	workItem := projectwork.WorkItem{
		ID:          "work_design",
		ProjectID:   project.ID,
		Title:       "Design child mirrors",
		Status:      projectwork.WorkItemStatusReady,
		Priority:    "normal",
		OwnerRoleID: role.ID,
		RootID:      "root_main",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := handler.writeProjectWorkItemToCairnline(t.Context(), project, workItem); err != nil {
		t.Fatalf("write work item mirror: %v", err)
	}
	if err := handler.writeProjectMemoryEntryToCairnline(t.Context(), memory.Entry{
		ID:         "mem_child",
		Scope:      memory.ScopeProject,
		ProjectID:  project.ID,
		Title:      "Child mirror note",
		Body:       "Preserve graph state when child rows change.",
		TrustLabel: memory.TrustLabelOperatorMemory,
		SourceKind: memory.SourceKindOperator,
		Enabled:    true,
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("write memory mirror: %v", err)
	}
	if err := handler.writeProjectMemoryCandidateToCairnline(t.Context(), memory.Candidate{
		ID:                  "cand_child",
		ProjectID:           project.ID,
		Title:               "Candidate child mirror note",
		Body:                "Reviewable child mirror candidate.",
		SuggestedKind:       "project_note",
		SuggestedTrustLabel: memory.TrustLabelGenerated,
		SuggestedSourceKind: memory.SourceKindGenerated,
		Status:              memory.CandidateStatusPending,
		CreatedAt:           now,
		UpdatedAt:           now,
	}); err != nil {
		t.Fatalf("write memory candidate mirror: %v", err)
	}
	if err := handler.writeProjectSkillsToCairnline(t.Context(), project, []projectskills.Skill{{
		ID:           "backend",
		ProjectID:    project.ID,
		Title:        "Backend",
		Path:         "docs-ai/skills/backend/SKILL.md",
		RootID:       "root_main",
		Format:       projectskills.FormatSkillMD,
		Enabled:      true,
		Status:       projectskills.StatusAvailable,
		TrustLabel:   projectskills.TrustWorkspaceSkill,
		DiscoveredAt: now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}}); err != nil {
		t.Fatalf("write skills mirror: %v", err)
	}

	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	defer store.Close()
	roles, err := service.ListRoles(t.Context(), project.ID)
	if err != nil {
		t.Fatalf("ListRoles: %v", err)
	}
	foundRole := false
	for _, mirroredRole := range roles {
		if mirroredRole.ID == role.ID {
			foundRole = true
			break
		}
	}
	if !foundRole {
		t.Fatalf("mirrored roles = %+v, want architect role preserved", roles)
	}
	if _, err := service.GetWorkItem(t.Context(), project.ID, workItem.ID); err != nil {
		t.Fatalf("GetWorkItem: %v", err)
	}
	if _, err := service.GetMemoryEntry(t.Context(), project.ID, "mem_child"); err != nil {
		t.Fatalf("GetMemoryEntry: %v", err)
	}
	if _, err := service.GetMemoryCandidate(t.Context(), project.ID, "cand_child"); err != nil {
		t.Fatalf("GetMemoryCandidate: %v", err)
	}
	if _, err := service.GetProjectSkill(t.Context(), project.ID, "backend"); err != nil {
		t.Fatalf("GetProjectSkill: %v", err)
	}
	mirrored := getMirroredCairnlineProjectForTest(t, handler, project.ID)
	if findMirroredCairnlineRootForTest(mirrored.Roots, "root_cairnline_only") == nil {
		t.Fatalf("mirrored roots = %+v, want Cairnline-only root preserved", mirrored.Roots)
	}
	if findMirroredCairnlineSourceForTest(mirrored.ContextSources, "ctx_cairnline_only") == nil {
		t.Fatalf("mirrored context sources = %+v, want Cairnline-only source preserved", mirrored.ContextSources)
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

func assertStrictEmbeddedProjectProjectionForTest(t *testing.T, project ProjectResponseItem, projectID string) {
	t.Helper()
	if project.ID != projectID || project.ReadBackend != "cairnline" || project.Name != "Embedded Project" || project.Description != "Read directly from Cairnline." {
		t.Fatalf("project = %+v, want direct embedded Cairnline project %s", project, projectID)
	}
	if project.DefaultRootID != "root_main" || project.DefaultAgentProfile != "profile_architect" || project.DefaultProvider != "openai" || project.DefaultModel != "gpt-5" || project.DefaultWorkspaceMode != "worktree" {
		t.Fatalf("project defaults = %+v, want Cairnline project/execution-profile defaults", project)
	}
	if project.DefaultToolsEnabled == nil || *project.DefaultToolsEnabled {
		t.Fatalf("default_tools_enabled = %v, want false from Cairnline execution profile", project.DefaultToolsEnabled)
	}
	if project.DefaultCompactToolOutput == nil || !*project.DefaultCompactToolOutput {
		t.Fatalf("default_compact_tool_output = %v, want true from Cairnline execution profile", project.DefaultCompactToolOutput)
	}
	if project.CreatedAt == "" || project.UpdatedAt == "" {
		t.Fatalf("project timestamps = created %q updated %q, want Cairnline timestamps", project.CreatedAt, project.UpdatedAt)
	}
	if len(project.Roots) != 1 || project.Roots[0].ID != "root_main" || project.Roots[0].Path != "/workspace/embedded" || project.Roots[0].Kind != "git" || project.Roots[0].GitBranch != "main" || !project.Roots[0].Active {
		t.Fatalf("project roots = %+v, want embedded Cairnline root metadata", project.Roots)
	}
	if len(project.ContextSources) != 1 || project.ContextSources[0].ID != "ctx_agents" || project.ContextSources[0].Path != "AGENTS.md" || project.ContextSources[0].TrustLabel != "workspace_guidance" || project.ContextSources[0].Metadata["root_id"] != "root_main" {
		t.Fatalf("project context sources = %+v, want embedded Cairnline source metadata", project.ContextSources)
	}
}

func assertCairnlineSidecarProjectForTest(t *testing.T, project ProjectResponseItem, projectID string) {
	t.Helper()
	if project.ID != projectID || project.ReadBackend != "cairnline" || project.Name != "Fixture Project" || project.Description != "Structured fixture project" {
		t.Fatalf("project = %+v, want Cairnline sidecar fixture project %s", project, projectID)
	}
	if len(project.Roots) != 1 || project.Roots[0].ID != "root_fixture" || project.Roots[0].Path != "/workspace/fixture" || project.Roots[0].Kind != "local" || !project.Roots[0].Active {
		t.Fatalf("roots = %+v, want fixture root metadata", project.Roots)
	}
	if len(project.ContextSources) != 1 || project.ContextSources[0].ID != "src_fixture" || project.ContextSources[0].Path != "AGENTS.md" || !project.ContextSources[0].Enabled {
		t.Fatalf("context sources = %+v, want fixture source metadata", project.ContextSources)
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

func listMirroredCairnlineProjectsForTest(t *testing.T, handler *Handler) []cairnline.Project {
	t.Helper()
	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	defer store.Close()
	items, err := service.ListProjects(t.Context())
	if err != nil {
		t.Fatalf("ListProjects(): %v", err)
	}
	return items
}

func findMirroredCairnlineRootForTest(roots []cairnline.Root, id string) *cairnline.Root {
	for idx := range roots {
		if roots[idx].ID == id {
			return &roots[idx]
		}
	}
	return nil
}

func findProjectResponseRootForTest(roots []ProjectRootResponseItem, id string) *ProjectRootResponseItem {
	for idx := range roots {
		if roots[idx].ID == id {
			return &roots[idx]
		}
	}
	return nil
}

func findProjectResponseContextSourceForTest(sources []ProjectContextSourceResponseItem, id string) *ProjectContextSourceResponseItem {
	for idx := range sources {
		if sources[idx].ID == id {
			return &sources[idx]
		}
	}
	return nil
}

func findMirroredCairnlineSourceForTest(sources []cairnline.Source, id string) *cairnline.Source {
	for idx := range sources {
		if sources[idx].ID == id {
			return &sources[idx]
		}
	}
	return nil
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

type failingProjectSkillDeleteStore struct {
	projectskills.Store
	err error
}

func (s failingProjectSkillDeleteStore) DeleteProject(context.Context, string) (int, error) {
	return 0, s.err
}

func seedCairnlineOnlyProjectGraphForTest(t *testing.T, handler *Handler, projectID string) {
	t.Helper()
	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	defer store.Close()
	if _, _, err := service.CreateRoot(t.Context(), projectID, cairnline.Root{
		ID:     "root_cairnline_only",
		Path:   "/workspace/cairnline-only",
		Kind:   "folder",
		Active: true,
	}); err != nil {
		t.Fatalf("CreateRoot(cairnline-only): %v", err)
	}
	if _, _, err := service.CreateContextSource(t.Context(), projectID, cairnline.Source{
		ID:      "ctx_cairnline_only",
		Kind:    "operator_note",
		Title:   "Cairnline-only source",
		Locator: "cairnline://source",
		Enabled: true,
	}); err != nil {
		t.Fatalf("CreateContextSource(cairnline-only): %v", err)
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
