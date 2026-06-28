package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/projects"
)

func TestProjectRootsDiscovery_MergesGitWorktrees(t *testing.T) {
	t.Parallel()
	repo := initProjectRootDiscoveryRepo(t)
	worktree := filepath.Join(t.TempDir(), "feature-worktree")
	if err := exec.Command("git", "-C", repo, "worktree", "add", "-b", "feature/worktrees", worktree).Run(); err != nil {
		t.Fatalf("git worktree add: %v", err)
	}

	handler := NewHandler(config.Config{}, quietLogger(), nil, nil, nil, nil)
	projectStore := projects.NewMemoryStore()
	handler.SetProjectStore(projectStore)
	server := NewServer(quietLogger(), handler)

	if _, err := projectStore.Create(t.Context(), projects.Project{
		ID:            "proj_roots",
		Name:          "Roots",
		DefaultRootID: "root_main",
		Roots: []projects.Root{{
			ID:     "root_main",
			Path:   repo,
			Kind:   "git",
			Active: true,
		}},
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_roots/roots/discover", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("discover status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var resp ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode discovery response: %v", err)
	}

	roots := make(map[string]ProjectRootResponseItem)
	for _, root := range resp.Data.Roots {
		roots[canonicalProjectRootDiscoveryTestPath(t, root.Path)] = root
	}
	main := roots[canonicalProjectRootDiscoveryTestPath(t, repo)]
	if main.ID != "root_main" || !main.Active || main.Kind != "git" || main.GitBranch != "main" || main.GitRemote != "https://example.com/cynic.git" {
		t.Fatalf("main root = %+v, want preserved id/active and refreshed git metadata", main)
	}
	linked := roots[canonicalProjectRootDiscoveryTestPath(t, worktree)]
	if linked.ID == "" || linked.ID == "root_main" || linked.Active || linked.Kind != "git_worktree" || linked.GitBranch != "feature/worktrees" || linked.GitRemote != "https://example.com/cynic.git" {
		t.Fatalf("linked root = %+v, want inactive discovered git worktree", linked)
	}
	if resp.Data.DefaultRootID != "root_main" {
		t.Fatalf("default_root_id = %q, want root_main", resp.Data.DefaultRootID)
	}
}

func TestProjectRootsDiscovery_MirrorsRootsWithoutReplacingCairnlineState(t *testing.T) {
	t.Parallel()
	repo := initProjectRootDiscoveryRepo(t)
	worktree := filepath.Join(t.TempDir(), "feature-worktree")
	if err := exec.Command("git", "-C", repo, "worktree", "add", "-b", "feature/worktrees", worktree).Run(); err != nil {
		t.Fatalf("git worktree add: %v", err)
	}

	handler := NewHandler(config.Config{
		Server:   config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{CoordinationBackend: "cairnline"},
	}, quietLogger(), nil, nil, nil, nil)
	projectStore := projects.NewMemoryStore()
	handler.SetProjectStore(projectStore)
	server := NewServer(quietLogger(), handler)

	project := projects.Project{
		ID:            "proj_roots",
		Name:          "Roots",
		DefaultRootID: "root_main",
		Roots: []projects.Root{{
			ID:     "root_main",
			Path:   repo,
			Kind:   "git",
			Active: true,
		}},
	}
	if _, err := projectStore.Create(t.Context(), project); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	seedCairnlineProjectRootsForTest(t, handler, project.ID, repo)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_roots/roots/discover", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("discover status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}

	mirrored := getMirroredCairnlineProjectForTest(t, handler, project.ID)
	if findMirroredCairnlineRootForTest(mirrored.Roots, "root_cairnline_only") == nil {
		t.Fatalf("mirrored roots = %+v, want Cairnline-only root preserved", mirrored.Roots)
	}
	if findMirroredCairnlineSourceForTest(mirrored.ContextSources, "ctx_cairnline_only") == nil {
		t.Fatalf("mirrored context sources = %+v, want Cairnline-only source preserved", mirrored.ContextSources)
	}
	if mirrored.DefaultRootID != "root_main" {
		t.Fatalf("mirrored default_root_id = %q, want root_main", mirrored.DefaultRootID)
	}
	var discoveredRoot *cairnline.Root
	wantPath := canonicalProjectRootDiscoveryTestPath(t, worktree)
	for idx := range mirrored.Roots {
		if canonicalProjectRootDiscoveryTestPath(t, mirrored.Roots[idx].Path) == wantPath {
			discoveredRoot = &mirrored.Roots[idx]
			break
		}
	}
	if discoveredRoot == nil || discoveredRoot.Kind != "git_worktree" || discoveredRoot.GitBranch != "feature/worktrees" || discoveredRoot.Active {
		t.Fatalf("mirrored discovered root = %+v in %+v, want inactive feature worktree root", discoveredRoot, mirrored.Roots)
	}
}

func TestProjectRoots_CreateWorktreeRoot(t *testing.T) {
	t.Parallel()
	repo := initProjectRootDiscoveryRepo(t)
	handler := NewHandler(config.Config{
		Server:   config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{CoordinationBackend: "cairnline"},
	}, quietLogger(), nil, nil, nil, nil)
	projectStore := projects.NewMemoryStore()
	handler.SetProjectStore(projectStore)
	server := NewServer(quietLogger(), handler)
	if _, err := projectStore.Create(t.Context(), projects.Project{
		ID:            "proj_roots",
		Name:          "Roots",
		DefaultRootID: "root_main",
		Roots: []projects.Root{{
			ID:     "root_main",
			Path:   repo,
			Kind:   "git",
			Active: true,
		}},
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	seedCairnlineProjectRootsForTest(t, handler, "proj_roots", repo)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_roots/roots/worktrees", bytes.NewReader([]byte(`{
		"base_root_id":"root_main",
		"branch":"feature/create-root",
		"active":true,
		"set_default":true
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create worktree status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var resp ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode create worktree response: %v", err)
	}
	if len(resp.Data.Roots) != 2 {
		t.Fatalf("roots = %+v, want main plus created worktree", resp.Data.Roots)
	}
	created := resp.Data.Roots[1]
	wantPath := filepath.Join(repo, ".worktrees", "feature-create-root")
	if created.ID == "" || created.Kind != "git_worktree" || created.GitBranch != "feature/create-root" || !created.Active || resp.Data.DefaultRootID != created.ID {
		t.Fatalf("created root = %+v default=%q, want active default git worktree", created, resp.Data.DefaultRootID)
	}
	if canonicalProjectRootDiscoveryTestPath(t, created.Path) != canonicalProjectRootDiscoveryTestPath(t, wantPath) {
		t.Fatalf("created path = %q, want %q", created.Path, wantPath)
	}
	branch, err := exec.Command("git", "-C", created.Path, "branch", "--show-current").Output()
	if err != nil {
		t.Fatalf("git branch in created worktree: %v", err)
	}
	if strings.TrimSpace(string(branch)) != "feature/create-root" {
		t.Fatalf("created worktree branch = %q, want feature/create-root", strings.TrimSpace(string(branch)))
	}

	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	defer store.Close()
	mirrored, err := service.GetProject(t.Context(), "proj_roots")
	if err != nil {
		t.Fatalf("GetProject(proj_roots): %v", err)
	}
	var mirroredRootFound bool
	for _, root := range mirrored.Roots {
		if root.ID == created.ID && root.Kind == "git_worktree" && root.GitBranch == "feature/create-root" && root.Active {
			mirroredRootFound = true
		}
	}
	if !mirroredRootFound || mirrored.DefaultRootID != created.ID {
		t.Fatalf("mirrored roots = %+v default=%q, want created active worktree root as default", mirrored.Roots, mirrored.DefaultRootID)
	}
	if findMirroredCairnlineRootForTest(mirrored.Roots, "root_cairnline_only") == nil {
		t.Fatalf("mirrored roots = %+v, want Cairnline-only root preserved", mirrored.Roots)
	}
	if findMirroredCairnlineSourceForTest(mirrored.ContextSources, "ctx_cairnline_only") == nil {
		t.Fatalf("mirrored context sources = %+v, want Cairnline-only source preserved", mirrored.ContextSources)
	}
}

func TestProjectRoots_CreateWorktreeRootRejectsOutsidePath(t *testing.T) {
	t.Parallel()
	repo := initProjectRootDiscoveryRepo(t)
	handler := NewHandler(config.Config{}, quietLogger(), nil, nil, nil, nil)
	projectStore := projects.NewMemoryStore()
	handler.SetProjectStore(projectStore)
	server := NewServer(quietLogger(), handler)
	if _, err := projectStore.Create(t.Context(), projects.Project{
		ID:            "proj_roots",
		Name:          "Roots",
		DefaultRootID: "root_main",
		Roots: []projects.Root{{
			ID:     "root_main",
			Path:   repo,
			Kind:   "git",
			Active: true,
		}},
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_roots/roots/worktrees", bytes.NewReader([]byte(`{
		"base_root_id":"root_main",
		"branch":"feature/outside",
		"path":"../outside"
	}`))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("outside worktree status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_roots/roots/worktrees", bytes.NewReader([]byte(`{
		"base_root_id":"root_main",
		"branch":"feature/nested",
		"path":".worktrees/nested/feature"
	}`))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("nested worktree status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
}

func initProjectRootDiscoveryRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	if err := exec.Command("git", "-C", dir, "init", "-b", "main").Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if err := exec.Command("git", "-C", dir, "remote", "add", "origin", "https://example.com/cynic.git").Run(); err != nil {
		t.Fatalf("git remote add: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := exec.Command("git", "-C", dir, "add", ".").Run(); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := exec.Command("git", "-C", dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "initial").Run(); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	return dir
}

func canonicalProjectRootDiscoveryTestPath(t *testing.T, path string) string {
	t.Helper()
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

func seedCairnlineProjectRootsForTest(t *testing.T, handler *Handler, projectID, repo string) {
	t.Helper()
	dbPath := handler.cairnlineEmbeddedDatabasePath()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("create Cairnline mirror parent: %v", err)
	}
	service, store, err := cairnline.NewSQLiteService(t.Context(), dbPath)
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	defer store.Close()
	if _, err := service.CreateProject(t.Context(), cairnline.Project{
		ID:            projectID,
		Name:          "Roots",
		DefaultRootID: "root_main",
		Roots: []cairnline.Root{{
			ID:     "root_main",
			Path:   repo,
			Kind:   "git",
			Active: true,
		}},
	}); err != nil {
		t.Fatalf("CreateProject(%s): %v", projectID, err)
	}
	if _, _, err := service.CreateRoot(t.Context(), projectID, cairnline.Root{
		ID:     "root_cairnline_only",
		Path:   filepath.Join(repo, ".worktrees", "cairnline-only"),
		Kind:   "git_worktree",
		Active: true,
	}); err != nil {
		t.Fatalf("CreateRoot(root_cairnline_only): %v", err)
	}
	if _, _, err := service.CreateContextSource(t.Context(), projectID, cairnline.Source{
		ID:      "ctx_cairnline_only",
		Kind:    "operator_note",
		Title:   "Cairnline-only source",
		Locator: "cairnline://source",
		Enabled: true,
	}); err != nil {
		t.Fatalf("CreateContextSource(ctx_cairnline_only): %v", err)
	}
}
