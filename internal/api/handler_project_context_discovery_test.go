package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

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
	writeDiscoveryFile(t, root, ".github/instructions/go.instructions.md")
	writeDiscoveryFile(t, root, "vendor/AGENTS.md")
	writeDiscoveryFile(t, root, "node_modules/pkg/AGENTS.md")

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
	if _, ok := sources["vendor/AGENTS.md"]; ok {
		t.Fatalf("vendor AGENTS.md was discovered: %+v", sources["vendor/AGENTS.md"])
	}
	if _, ok := sources["node_modules/pkg/AGENTS.md"]; ok {
		t.Fatalf("node_modules AGENTS.md was discovered: %+v", sources["node_modules/pkg/AGENTS.md"])
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
