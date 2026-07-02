package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/projects"
)

func TestProjectContextDiscovery_FindsWorkspaceGuidanceMetadata(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeDiscoveryFile(t, root, "AGENTS.md")
	writeDiscoveryFile(t, root, "internal/api/AGENTS.md")
	writeDiscoveryFile(t, root, "CLAUDE.md")
	writeDiscoveryFile(t, root, ".cursor/rules/backend.mdc")
	writeDiscoveryFile(t, root, ".github/copilot-instructions.md")
	writeDiscoveryFile(t, root, ".github/instructions/go.instructions.md")
	writeDiscoveryFile(t, root, "vendor/AGENTS.md")
	writeDiscoveryFile(t, root, "node_modules/pkg/AGENTS.md")
	writeDiscoveryFile(t, root, ".claude/worktrees/agent/AGENTS.md")
	writeDiscoveryFile(t, root, ".worktrees/agent/AGENTS.md")
	writeDiscoveryFile(t, root, "nested-checkout/AGENTS.md")
	writeDiscoveryFile(t, root, "nested-checkout/.git")

	handler := NewHandler(config.Config{}, quietLogger(), nil, nil, nil, nil)
	projectStore := projects.NewMemoryStore()
	handler.SetProjectStore(projectStore)
	server := NewServer(quietLogger(), handler)

	if _, err := projectStore.Create(t.Context(), projects.Project{
		ID:   "proj_guidance",
		Name: "Guidance",
		Roots: []projects.Root{{
			ID:     "root_main",
			Path:   root,
			Kind:   "local",
			Active: true,
		}},
		ContextSources: []projects.ContextSource{{
			ID:      "ctx_agents_existing",
			Kind:    "workspace_instruction",
			Path:    "AGENTS.md",
			Title:   "AGENTS.md",
			Enabled: false,
		}},
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_guidance/context-sources/discover", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("discover status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var resp ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode discovery response: %v", err)
	}

	sources := map[string]ProjectContextSourceResponseItem{}
	for _, source := range resp.Data.ContextSources {
		sources[source.Path] = source
	}
	if got := sources["AGENTS.md"]; got.ID != "ctx_agents_existing" || got.Enabled {
		t.Fatalf("root AGENTS source = %+v, want existing disabled source preserved", got)
	}
	if got := sources["internal/api/AGENTS.md"]; got.Kind != "workspace_instruction" || got.Format != "agents_md" || got.Scope != "path:internal/api" || got.TrustLabel != contextTrustWorkspaceGuidance {
		t.Fatalf("nested AGENTS source = %+v, want workspace guidance metadata", got)
	}
	if got := sources["CLAUDE.md"]; got.Kind != "host_instruction" || got.Metadata["host"] != "claude" {
		t.Fatalf("CLAUDE source = %+v, want labelled host instruction", got)
	}
	if got := sources[".cursor/rules/backend.mdc"]; got.Kind != "host_rule" || got.Metadata["host"] != "cursor" || got.Scope != "metadata_only" {
		t.Fatalf("Cursor source = %+v, want metadata-only host rule", got)
	}
	if got := sources[".github/copilot-instructions.md"]; got.Kind != "host_instruction" || got.Metadata["host"] != "github_copilot" || got.Scope != "metadata_only" {
		t.Fatalf("Copilot source = %+v, want metadata-only host instruction", got)
	}
	if _, ok := sources["vendor/AGENTS.md"]; ok {
		t.Fatalf("vendor AGENTS.md was discovered: %+v", sources["vendor/AGENTS.md"])
	}
	if _, ok := sources["node_modules/pkg/AGENTS.md"]; ok {
		t.Fatalf("node_modules AGENTS.md was discovered: %+v", sources["node_modules/pkg/AGENTS.md"])
	}
	if _, ok := sources[".claude/worktrees/agent/AGENTS.md"]; ok {
		t.Fatalf("nested Claude worktree AGENTS.md was discovered: %+v", sources[".claude/worktrees/agent/AGENTS.md"])
	}
	if _, ok := sources[".worktrees/agent/AGENTS.md"]; ok {
		t.Fatalf("nested worktree AGENTS.md was discovered: %+v", sources[".worktrees/agent/AGENTS.md"])
	}
	if _, ok := sources["nested-checkout/AGENTS.md"]; ok {
		t.Fatalf("nested git checkout AGENTS.md was discovered: %+v", sources["nested-checkout/AGENTS.md"])
	}
}

func TestProjectContextDiscovery_KeepsSamePathSourcesFromDifferentRoots(t *testing.T) {
	t.Parallel()
	rootA := t.TempDir()
	rootB := t.TempDir()
	writeDiscoveryFile(t, rootA, "AGENTS.md")
	writeDiscoveryFile(t, rootB, "AGENTS.md")

	handler := NewHandler(config.Config{}, quietLogger(), nil, nil, nil, nil)
	projectStore := projects.NewMemoryStore()
	handler.SetProjectStore(projectStore)
	server := NewServer(quietLogger(), handler)

	if _, err := projectStore.Create(t.Context(), projects.Project{
		ID:   "proj_multi_root",
		Name: "Multi-root",
		Roots: []projects.Root{
			{ID: "root_a", Path: rootA, Kind: "local", Active: true},
			{ID: "root_b", Path: rootB, Kind: "local", Active: true},
		},
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_multi_root/context-sources/discover", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("discover status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var resp ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode discovery response: %v", err)
	}

	roots := make(map[string]bool)
	for _, source := range resp.Data.ContextSources {
		if source.Path == "AGENTS.md" && source.Kind == "workspace_instruction" {
			roots[source.Metadata["root_id"]] = true
		}
	}
	if !roots["root_a"] || !roots["root_b"] || len(roots) != 2 {
		t.Fatalf("discovered AGENTS roots = %+v, want root_a and root_b", roots)
	}
}

func TestProjectContextDiscovery_MirrorsDiscoveredSourcesToCairnlineWhenConfigured(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeDiscoveryFile(t, root, "AGENTS.md")

	handler := NewHandler(config.Config{
		Server:   config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{CoordinationBackend: "cairnline"},
	}, quietLogger(), nil, nil, nil, nil)
	projectStore := projects.NewMemoryStore()
	handler.SetProjectStore(projectStore)
	server := NewServer(quietLogger(), handler)

	if _, err := projectStore.Create(t.Context(), projects.Project{
		ID:   "proj_guidance_mirror",
		Name: "Guidance mirror",
		Roots: []projects.Root{{
			ID:     "root_main",
			Path:   root,
			Kind:   "local",
			Active: true,
		}},
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	dbPath := handler.cairnlineEmbeddedDatabasePath()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("create Cairnline mirror directory: %v", err)
	}
	service, store, err := cairnline.NewSQLiteService(t.Context(), dbPath)
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	if _, err := service.CreateProject(t.Context(), cairnline.Project{
		ID:   "proj_guidance_mirror",
		Name: "Guidance mirror",
	}); err != nil {
		t.Fatalf("CreateProject(cairnline seed): %v", err)
	}
	if _, _, err := service.CreateContextSource(t.Context(), "proj_guidance_mirror", cairnline.Source{
		ID:      "ctx_cairnline_only",
		Kind:    "operator_note",
		Title:   "Cairnline-only source",
		Locator: "cairnline://source",
		Enabled: true,
	}); err != nil {
		t.Fatalf("CreateContextSource(cairnline-only): %v", err)
	}
	store.Close()

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_guidance_mirror/context-sources/discover", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("discover status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}

	service, store, err = cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	defer store.Close()
	project, err := service.GetProject(t.Context(), "proj_guidance_mirror")
	if err != nil {
		t.Fatalf("GetProject(proj_guidance_mirror): %v", err)
	}
	var foundDiscovered bool
	for _, source := range project.ContextSources {
		if source.Locator == "AGENTS.md" && source.Kind == "workspace_instruction" && source.Format == "agents_md" {
			foundDiscovered = true
		}
	}
	if !foundDiscovered {
		t.Fatalf("mirrored context sources = %+v, want discovered AGENTS.md source", project.ContextSources)
	}
	if findCairnlineSourceForAPITest(project.ContextSources, "ctx_cairnline_only") == nil {
		t.Fatalf("mirrored context sources = %+v, want Cairnline-only source preserved", project.ContextSources)
	}
}

func TestProjectContextDiscovery_CairnlineAuthorityCommitsDiscoveredSourcesFirst(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeDiscoveryFile(t, root, "AGENTS.md")
	writeDiscoveryFile(t, root, "CLAUDE.md")

	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend:     "cairnline",
			CairnlineWriteAuthority: projectCairnlineWriteAuthorityProjectContextSources,
		},
	}, quietLogger(), nil, nil, nil, nil)
	projectStore := projects.NewMemoryStore()
	handler.SetProjectStore(projectStore)
	server := NewServer(quietLogger(), handler)

	if _, err := projectStore.Create(t.Context(), projects.Project{
		ID:   "proj_guidance_authority",
		Name: "Guidance authority",
		Roots: []projects.Root{{
			ID:     "root_main",
			Path:   root,
			Kind:   "local",
			Active: true,
		}},
		ContextSources: []projects.ContextSource{{
			ID:      "ctx_existing_agents",
			Kind:    "workspace_instruction",
			Path:    "AGENTS.md",
			Title:   "AGENTS.md",
			Enabled: false,
			Metadata: map[string]string{
				"root_id": "root_main",
			},
		}},
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	dbPath := handler.cairnlineEmbeddedDatabasePath()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("create Cairnline mirror directory: %v", err)
	}
	service, store, err := cairnline.NewSQLiteService(t.Context(), dbPath)
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	if _, err := service.CreateProject(t.Context(), cairnline.Project{
		ID:   "proj_guidance_authority",
		Name: "Guidance authority",
		ContextSources: []cairnline.Source{{
			ID:      "ctx_cairnline_only",
			Kind:    "operator_note",
			Title:   "Cairnline-only source",
			Locator: "cairnline://source",
			Enabled: true,
		}},
	}); err != nil {
		t.Fatalf("CreateProject(cairnline seed): %v", err)
	}
	store.Close()

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_guidance_authority/context-sources/discover", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("discover status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var resp ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode discovery response: %v", err)
	}
	if len(resp.Data.ContextSources) != 2 {
		t.Fatalf("response sources = %+v, want discovered AGENTS.md and CLAUDE.md", resp.Data.ContextSources)
	}
	if got := projectContextSourceResponseByPath(resp.Data.ContextSources, "AGENTS.md"); got == nil || got.ID != "ctx_existing_agents" || got.Enabled {
		t.Fatalf("response AGENTS source = %+v, want existing disabled source preserved", got)
	}
	if got := projectContextSourceResponseByPath(resp.Data.ContextSources, "CLAUDE.md"); got == nil || got.Kind != "host_instruction" || got.Metadata["host"] != "claude" {
		t.Fatalf("response CLAUDE source = %+v, want discovered Claude source", got)
	}

	service, store, err = cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	defer store.Close()
	project, err := service.GetProject(t.Context(), "proj_guidance_authority")
	if err != nil {
		t.Fatalf("GetProject(proj_guidance_authority): %v", err)
	}
	if len(project.ContextSources) != 2 {
		t.Fatalf("Cairnline sources = %+v, want authoritative discovery replacement without Cairnline-only source", project.ContextSources)
	}
	if findCairnlineSourceForAPITest(project.ContextSources, "ctx_existing_agents") == nil {
		t.Fatalf("Cairnline sources = %+v, want existing AGENTS source id preserved", project.ContextSources)
	}
	if findCairnlineSourceForAPITest(project.ContextSources, "ctx_cairnline_only") != nil {
		t.Fatalf("Cairnline sources = %+v, want stale Cairnline-only source removed by authoritative discovery replacement", project.ContextSources)
	}
}

func TestProjectContextDiscovery_CairnlineAuthorityAcceptsCairnlineOnlyProject(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeDiscoveryFile(t, root, "AGENTS.md")

	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend:     "cairnline",
			CairnlineWriteAuthority: projectCairnlineWriteAuthorityProjectContextSources,
		},
	}, quietLogger(), nil, nil, nil, nil)
	projectStore := projects.NewMemoryStore()
	handler.SetProjectStore(projectStore)
	server := NewServer(quietLogger(), handler)

	dbPath := handler.cairnlineEmbeddedDatabasePath()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("create Cairnline mirror directory: %v", err)
	}
	service, store, err := cairnline.NewSQLiteService(t.Context(), dbPath)
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	if _, err := service.CreateProject(t.Context(), cairnline.Project{
		ID:   "proj_guidance_cairnline_only",
		Name: "Guidance Cairnline only",
		Roots: []cairnline.Root{{
			ID:     "root_main",
			Path:   root,
			Kind:   "local",
			Active: true,
		}},
	}); err != nil {
		t.Fatalf("CreateProject(cairnline seed): %v", err)
	}
	store.Close()

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_guidance_cairnline_only/context-sources/discover", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("discover status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var resp ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode discovery response: %v", err)
	}
	if got := projectContextSourceResponseByPath(resp.Data.ContextSources, "AGENTS.md"); got == nil || got.Kind != "workspace_instruction" || got.Metadata["root_id"] != "root_main" {
		t.Fatalf("response AGENTS source = %+v, want discovered Cairnline-owned root guidance", got)
	}
	if resp.Data.ReadBackend != "cairnline" {
		t.Fatalf("discovery read_backend = %q, want cairnline for Cairnline-authoritative discovery response", resp.Data.ReadBackend)
	}
	if _, ok, err := projectStore.Get(t.Context(), "proj_guidance_cairnline_only"); err != nil || ok {
		t.Fatalf("native project lookup ok=%v err=%v, want no native project row", ok, err)
	}

	project := getMirroredCairnlineProjectForTest(t, handler, "proj_guidance_cairnline_only")
	if got := findCairnlineSourceByLocatorForAPITest(project.ContextSources, "AGENTS.md"); got == nil || got.Kind != "workspace_instruction" {
		t.Fatalf("Cairnline sources = %+v, want discovered AGENTS.md source", project.ContextSources)
	}
}

func TestProjectContextDiscovery_CairnlineReplacementModeSkipsMissingCompatibilityShadowWarning(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeDiscoveryFile(t, root, "AGENTS.md")

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend:      "cairnline",
			CairnlineConnector:       "embedded",
			CairnlineReadSource:      "embedded",
			CairnlineWriteAuthority:  "all-portable",
			CairnlineReplacementMode: "embedded",
		},
	}, logger, nil, nil, nil, nil)
	projectStore := projects.NewMemoryStore()
	handler.SetProjectStore(projectStore)
	server := NewServer(logger, handler)
	client := newAPITestClient(t, server)

	created := mustRequestJSONStatus[ProjectResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects", projectJourneyJSON(t, map[string]any{
		"name": "Replacement guidance discovery",
		"roots": []map[string]any{{
			"id":     "root_main",
			"path":   root,
			"kind":   "local",
			"active": true,
		}},
	}))
	if _, ok, err := projectStore.Get(t.Context(), created.Data.ID); err != nil || ok {
		t.Fatalf("native project lookup ok=%v err=%v, want no native project row", ok, err)
	}

	resp := mustRequestJSONStatus[ProjectResponse](client, http.StatusOK, http.MethodPost, "/hecate/v1/projects/"+created.Data.ID+"/context-sources/discover", `{}`)
	if resp.Data.ReadBackend != "cairnline" {
		t.Fatalf("discovery read_backend = %q, want cairnline for replacement-mode authoritative response", resp.Data.ReadBackend)
	}
	if got := projectContextSourceResponseByPath(resp.Data.ContextSources, "AGENTS.md"); got == nil || got.Kind != "workspace_instruction" {
		t.Fatalf("response AGENTS source = %+v, want discovered workspace guidance", got)
	}
	if bytes.Contains(logs.Bytes(), []byte("cairnline project mirror write failed")) {
		t.Fatalf("logs contain compatibility-shadow warning for intentional Cairnline-only project:\n%s", logs.String())
	}
}

func findCairnlineSourceForAPITest(sources []cairnline.Source, id string) *cairnline.Source {
	for idx := range sources {
		if sources[idx].ID == id {
			return &sources[idx]
		}
	}
	return nil
}

func findCairnlineSourceByLocatorForAPITest(sources []cairnline.Source, locator string) *cairnline.Source {
	for idx := range sources {
		if sources[idx].Locator == locator {
			return &sources[idx]
		}
	}
	return nil
}

func projectContextSourceResponseByPath(sources []ProjectContextSourceResponseItem, path string) *ProjectContextSourceResponseItem {
	for idx := range sources {
		if sources[idx].Path == path {
			return &sources[idx]
		}
	}
	return nil
}

func TestProjectContextDiscovery_RejectsRelativeRoot(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{}, quietLogger(), nil, nil, nil, nil)
	projectStore := projects.NewMemoryStore()
	handler.SetProjectStore(projectStore)
	server := NewServer(quietLogger(), handler)
	if _, err := projectStore.Create(t.Context(), projects.Project{
		ID:   "proj_relative",
		Name: "Relative",
		Roots: []projects.Root{{
			ID:     "root_relative",
			Path:   "relative/path",
			Kind:   "local",
			Active: true,
		}},
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_relative/context-sources/discover", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("discover relative status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
}

func writeDiscoveryFile(t *testing.T, root, rel string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("# guidance\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}
