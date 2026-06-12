package api

import (
	"context"
	"errors"
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

type createProjectWorktreeRootRequest struct {
	BaseRootID string `json:"base_root_id,omitempty"`
	Branch     string `json:"branch"`
	Path       string `json:"path,omitempty"`
	StartPoint string `json:"start_point,omitempty"`
	Active     *bool  `json:"active,omitempty"`
	SetDefault bool   `json:"set_default,omitempty"`
}

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

func (h *Handler) HandleCreateProjectWorktreeRoot(w http.ResponseWriter, r *http.Request) {
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
	var req createProjectWorktreeRootRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	root, _, err := createProjectWorktreeRoot(r.Context(), project, req, gitrunner.NewLocalRunner())
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	updated, err := h.projects.Update(r.Context(), project.ID, func(item *projects.Project) {
		item.Roots = append(item.Roots, root)
		if req.SetDefault {
			item.DefaultRootID = root.ID
		}
	})
	if errors.Is(err, projects.ErrAlreadyExists) {
		WriteError(w, http.StatusConflict, errCodeConflict, err.Error())
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusCreated, ProjectResponse{Object: "project", Data: renderProject(updated)})
}

type projectGitRunner interface {
	IsWorkTree(context.Context, string) bool
	Worktrees(context.Context, string) ([]gitrunner.Worktree, error)
	Run(context.Context, string, ...string) (gitrunner.Result, error)
}

func createProjectWorktreeRoot(ctx context.Context, project projects.Project, req createProjectWorktreeRootRequest, runner projectGitRunner) (projects.Root, string, error) {
	if runner == nil {
		runner = gitrunner.NewLocalRunner()
	}
	branch := strings.TrimSpace(req.Branch)
	if branch == "" {
		return projects.Root{}, "", fmt.Errorf("branch is required")
	}
	if strings.HasPrefix(branch, "-") {
		return projects.Root{}, "", fmt.Errorf("branch cannot start with '-'")
	}
	baseRoot, ok := selectProjectWorktreeBaseRoot(project, req.BaseRootID)
	if !ok {
		return projects.Root{}, "", fmt.Errorf("base project root not found")
	}
	basePath := strings.TrimSpace(baseRoot.Path)
	if basePath == "" || !filepath.IsAbs(basePath) {
		return projects.Root{}, "", fmt.Errorf("base project root %q must have an absolute path", baseRoot.ID)
	}
	info, err := os.Stat(basePath)
	if err != nil {
		return projects.Root{}, "", fmt.Errorf("base project root %q is not accessible: %w", baseRoot.ID, err)
	}
	if !info.IsDir() {
		return projects.Root{}, "", fmt.Errorf("base project root %q is not a directory", baseRoot.ID)
	}
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	if !runner.IsWorkTree(checkCtx, basePath) {
		cancel()
		return projects.Root{}, "", fmt.Errorf("base project root %q is not a git worktree", baseRoot.ID)
	}
	cancel()

	worktreeParent := filepath.Join(basePath, ".worktrees")
	targetPath := strings.TrimSpace(req.Path)
	if targetPath == "" {
		targetPath = filepath.Join(worktreeParent, safeWorktreePathSegment(branch))
	} else if !filepath.IsAbs(targetPath) {
		targetPath = filepath.Join(basePath, targetPath)
	}
	targetPath = filepath.Clean(targetPath)
	if ok, err := pathWithin(worktreeParent, targetPath); err != nil {
		return projects.Root{}, "", err
	} else if !ok {
		return projects.Root{}, "", fmt.Errorf("worktree path must be under %s", worktreeParent)
	}
	if filepath.Dir(targetPath) != filepath.Clean(worktreeParent) {
		return projects.Root{}, "", fmt.Errorf("worktree path must be a direct child of %s", worktreeParent)
	}
	if _, err := os.Stat(targetPath); err == nil {
		return projects.Root{}, "", fmt.Errorf("worktree path already exists: %s", targetPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return projects.Root{}, "", fmt.Errorf("worktree path could not be inspected: %w", err)
	}
	// This is the only raw filesystem write here: gitrunner owns the actual
	// worktree creation after the base Git root and target boundary are validated.
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return projects.Root{}, "", fmt.Errorf("worktree parent could not be created: %w", err)
	}
	args := []string{"worktree", "add", "-b", branch, targetPath}
	if startPoint := strings.TrimSpace(req.StartPoint); startPoint != "" {
		args = append(args, startPoint)
	}
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := runner.Run(runCtx, basePath, args...); err != nil {
		return projects.Root{}, "", fmt.Errorf("git worktree add failed: %w", err)
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	return projects.Root{
		ID:        newOpaqueTaskResourceID("root"),
		Path:      targetPath,
		Kind:      projectRootKindGitWorktree,
		GitRemote: projectRootGitRemote(ctx, runner, targetPath),
		GitBranch: branch,
		Active:    active,
	}, targetPath, nil
}

func selectProjectWorktreeBaseRoot(project projects.Project, rootID string) (projects.Root, bool) {
	rootID = strings.TrimSpace(rootID)
	if rootID == "" {
		rootID = strings.TrimSpace(project.DefaultRootID)
	}
	if rootID != "" {
		for _, root := range project.Roots {
			if strings.TrimSpace(root.ID) == rootID {
				return root, true
			}
		}
		return projects.Root{}, false
	}
	for _, root := range project.Roots {
		if root.Active {
			return root, true
		}
	}
	if len(project.Roots) > 0 {
		return project.Roots[0], true
	}
	return projects.Root{}, false
}

func safeWorktreePathSegment(branch string) string {
	branch = strings.TrimSpace(branch)
	var b strings.Builder
	for _, r := range branch {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), ".-_/ ")
	if out == "" {
		return "worktree"
	}
	return out
}

func pathWithin(parent, child string) (bool, error) {
	parent, err := filepath.Abs(filepath.Clean(parent))
	if err != nil {
		return false, err
	}
	child, err = filepath.Abs(filepath.Clean(child))
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false, err
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))), nil
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
