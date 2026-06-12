package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

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
