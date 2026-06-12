package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/gitrunner"
	"github.com/hecatehq/hecate/internal/projects"
)

const (
	projectRootKindGit         = "git"
	projectRootKindGitWorktree = "git_worktree"
)

func (h *Handler) HandleDiscoverProjectRoots(w http.ResponseWriter, r *http.Request) {
	if h.projects == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "project store is not configured")
		return
	}
	project, ok, err := h.projects.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
		return
	}
	discovered, err := discoverProjectGitWorktreeRoots(r.Context(), project, gitrunner.NewLocalRunner())
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	updated, err := h.projects.Update(r.Context(), project.ID, func(item *projects.Project) {
		item.Roots = mergeDiscoveredProjectRoots(item.Roots, discovered)
		if item.DefaultRootID == "" && len(item.Roots) > 0 {
			item.DefaultRootID = item.Roots[0].ID
		}
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, ProjectResponse{Object: "project", Data: renderProject(updated)})
}

type projectGitRunner interface {
	IsWorkTree(context.Context, string) bool
	Worktrees(context.Context, string) ([]gitrunner.Worktree, error)
	Run(context.Context, string, ...string) (gitrunner.Result, error)
}

func discoverProjectGitWorktreeRoots(ctx context.Context, project projects.Project, runner projectGitRunner) ([]projects.Root, error) {
	if runner == nil {
		runner = gitrunner.NewLocalRunner()
	}
	byPath := make(map[string]projects.Root)
	for _, root := range project.Roots {
		if !root.Active {
			continue
		}
		rootPath := strings.TrimSpace(root.Path)
		if rootPath == "" {
			continue
		}
		if !filepath.IsAbs(rootPath) {
			return nil, fmt.Errorf("project root %q path must be absolute", root.ID)
		}
		info, err := os.Stat(rootPath)
		if err != nil {
			return nil, fmt.Errorf("project root %q is not accessible: %w", root.ID, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("project root %q is not a directory", root.ID)
		}
		rootCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		if !runner.IsWorkTree(rootCtx, rootPath) {
			cancel()
			continue
		}
		worktrees, err := runner.Worktrees(rootCtx, rootPath)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("project root %q worktrees could not be discovered: %w", root.ID, err)
		}
		for _, worktree := range worktrees {
			path := strings.TrimSpace(worktree.Path)
			if path == "" || !filepath.IsAbs(path) || worktree.Bare {
				continue
			}
			path = filepath.Clean(path)
			branch := projectWorktreeBranchLabel(worktree)
			item := projects.Root{
				Path:      path,
				Kind:      projectRootKindGitWorktree,
				GitRemote: projectRootGitRemote(ctx, runner, path),
				GitBranch: branch,
				Active:    false,
			}
			if pathsEqual(path, rootPath) {
				item.Kind = projectRootKindGit
				item.Active = root.Active
			}
			byPath[cleanProjectRootPath(path)] = item
		}
	}
	out := make([]projects.Root, 0, len(byPath))
	for _, item := range byPath {
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Path < out[j].Path
	})
	return out, nil
}

func mergeDiscoveredProjectRoots(existing, discovered []projects.Root) []projects.Root {
	out := make([]projects.Root, 0, len(existing)+len(discovered))
	indexByPath := make(map[string]int, len(existing)+len(discovered))
	for _, root := range existing {
		path := cleanProjectRootPath(root.Path)
		out = append(out, root)
		if path != "" {
			indexByPath[path] = len(out) - 1
		}
	}
	for _, root := range discovered {
		path := cleanProjectRootPath(root.Path)
		if path == "" {
			continue
		}
		if idx, ok := indexByPath[path]; ok {
			existingRoot := out[idx]
			if strings.TrimSpace(existingRoot.Kind) == "" {
				existingRoot.Kind = root.Kind
			}
			if strings.TrimSpace(root.GitRemote) != "" {
				existingRoot.GitRemote = root.GitRemote
			}
			if strings.TrimSpace(root.GitBranch) != "" {
				existingRoot.GitBranch = root.GitBranch
			}
			out[idx] = existingRoot
			continue
		}
		root.ID = newOpaqueTaskResourceID("root")
		root.Active = false
		out = append(out, root)
		indexByPath[path] = len(out) - 1
	}
	return out
}

func projectWorktreeBranchLabel(worktree gitrunner.Worktree) string {
	if branch := strings.TrimSpace(worktree.Branch); branch != "" {
		return branch
	}
	head := strings.TrimSpace(worktree.Head)
	if head == "" {
		return ""
	}
	if len(head) > 12 {
		head = head[:12]
	}
	return "detached@" + head
}

func projectRootGitRemote(ctx context.Context, runner projectGitRunner, path string) string {
	runCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	result, err := runner.Run(runCtx, path, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(result.Stdout)
}

func cleanProjectRootPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

func pathsEqual(left, right string) bool {
	return cleanProjectRootPath(left) == cleanProjectRootPath(right)
}
