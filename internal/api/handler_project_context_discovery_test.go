package api

import (
	"bytes"
	"encoding/json"
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

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_guidance_mirror/context-sources/discover", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("discover status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}

	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	defer store.Close()
	project, err := service.GetProject(t.Context(), "proj_guidance_mirror")
	if err != nil {
		t.Fatalf("GetProject(proj_guidance_mirror): %v", err)
	}
	var found bool
	for _, source := range project.ContextSources {
		if source.Locator == "AGENTS.md" && source.Kind == "workspace_instruction" && source.Format == "agents_md" {
			found = true
		}
	}
	if !found {
		t.Fatalf("mirrored context sources = %+v, want discovered AGENTS.md source", project.ContextSources)
	}
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
