package orchestrator

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/hecatehq/hecate/internal/gitrunner"
	"github.com/hecatehq/hecate/internal/workspacefs"
	"github.com/hecatehq/hecate/pkg/types"
)

type WorkspaceManager struct {
	root string
}

func NewWorkspaceManager(root string) *WorkspaceManager {
	if strings.TrimSpace(root) == "" {
		root = filepath.Join(os.TempDir(), "hecate-workspaces")
	}
	root = filepath.Clean(root)
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	return &WorkspaceManager{root: root}
}

func (m *WorkspaceManager) Provision(ctx context.Context, task types.Task, run types.TaskRun) (string, error) {
	if m == nil {
		return "", fmt.Errorf("workspace manager is not configured")
	}
	source := workspaceSource(task)
	// "in_place" mode: skip the clone/copy and run directly in the
	// source directory. The sandbox AllowedRoot becomes the source
	// path, so writes from shell_exec/file/agent_loop tools land in
	// the operator's actual repo. Necessarily destructive — opt-in
	// only. We require an absolute, existing source directory; if
	// task.WorkingDirectory or task.Repo doesn't resolve to one, we
	// reject the run rather than silently fall back to an isolated
	// clone (which would be a surprising mode flip).
	if strings.TrimSpace(task.WorkspaceMode) == "in_place" {
		if source.path == "" {
			return "", fmt.Errorf("workspace_mode=in_place requires an absolute, existing working_directory or repo path")
		}
		return source.path, nil
	}
	workspacePath, err := m.workspacePath(task.ID, run.ID)
	if err != nil {
		return "", err
	}
	if err := provisionWorkspaceSource(ctx, workspacePath, source); err != nil {
		return "", err
	}
	return workspacePath, nil
}

func (m *WorkspaceManager) workspacePath(taskID, runID string) (string, error) {
	root, err := canonicalWorkspaceRoot(m.root)
	if err != nil {
		return "", err
	}
	taskSegment, err := workspacePathSegment("task id", taskID)
	if err != nil {
		return "", err
	}
	runSegment, err := workspacePathSegment("run id", runID)
	if err != nil {
		return "", err
	}
	return safeJoinWithinRoot(root, filepath.Join(taskSegment, runSegment))
}

type workspaceSourceSpec struct {
	path string
	kind string
}

func workspaceSource(task types.Task) workspaceSourceSpec {
	for _, candidate := range []string{task.WorkingDirectory, task.Repo} {
		candidate, ok := canonicalWorkspaceDir(candidate)
		if !ok {
			continue
		}
		if isGitRepository(candidate) {
			return workspaceSourceSpec{path: candidate, kind: "git"}
		}
		return workspaceSourceSpec{path: candidate, kind: "directory"}
	}
	return workspaceSourceSpec{}
}

func provisionWorkspaceSource(ctx context.Context, workspacePath string, source workspaceSourceSpec) error {
	switch source.kind {
	case "git":
		return provisionGitWorkspace(ctx, source.path, workspacePath)
	case "directory":
		return provisionDirectoryWorkspace(source.path, workspacePath)
	default:
		return ensureWorkspaceRoot(workspacePath)
	}
}

func provisionGitWorkspace(ctx context.Context, sourcePath, workspacePath string) error {
	if err := ensureWorkspaceParent(workspacePath); err != nil {
		return err
	}
	result, err := gitrunner.NewLocalRunner().Clone(ctx, sourcePath, workspacePath)
	if err != nil {
		output := strings.TrimSpace(result.Stdout)
		if stderr := strings.TrimSpace(result.Stderr); stderr != "" {
			if output != "" {
				output += "\n"
			}
			output += stderr
		}
		if output != "" {
			return fmt.Errorf("clone workspace: %w: %s", err, output)
		}
		return fmt.Errorf("clone workspace: %w", err)
	}
	return nil
}

func provisionDirectoryWorkspace(sourcePath, workspacePath string) error {
	if err := ensureWorkspaceRoot(workspacePath); err != nil {
		return err
	}
	return copyDirectory(sourcePath, workspacePath)
}

func ensureWorkspaceParent(workspacePath string) error {
	return os.MkdirAll(filepath.Dir(workspacePath), 0o755)
}

func ensureWorkspaceRoot(workspacePath string) error {
	return os.MkdirAll(workspacePath, 0o755)
}

func isGitRepository(path string) bool {
	gitDir, err := safeJoinWithinRoot(path, ".git")
	if err != nil {
		return false
	}
	info, err := os.Stat(gitDir)
	return err == nil && info.IsDir()
}

func copyDirectory(sourcePath, destinationPath string) error {
	return filepath.WalkDir(sourcePath, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == sourcePath {
			return nil
		}

		relativePath, err := filepath.Rel(sourcePath, path)
		if err != nil {
			return err
		}
		targetPath, err := safeJoinWithinRoot(destinationPath, relativePath)
		if err != nil {
			return err
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(targetPath, info.Mode())
		}
		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(linkTarget, targetPath)
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}
		return copyFile(path, targetPath, info.Mode())
	})
}

func canonicalWorkspaceDir(candidate string) (string, bool) {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" || !filepath.IsAbs(candidate) {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(candidate))
	if err != nil {
		return "", false
	}
	root, err := os.OpenRoot(resolved)
	if err != nil {
		return "", false
	}
	defer root.Close()
	info, err := root.Stat(".")
	if err != nil || !info.IsDir() {
		return "", false
	}
	return resolved, true
}

func canonicalWorkspaceRoot(root string) (string, error) {
	root = filepath.Clean(root)
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("workspace root %q resolves to a symlink", root)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace root %q is not a directory", root)
	}
	return resolved, nil
}

func workspacePathSegment(field, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("invalid workspace %s: empty path segment", field)
	}
	if value == "." || value == ".." || strings.ContainsAny(value, `/\`) || value != filepath.Base(value) || !filepath.IsLocal(value) {
		return "", fmt.Errorf("invalid workspace %s %q: must be a single local path segment", field, value)
	}
	return value, nil
}

func safeJoinWithinRoot(root, relativePath string) (string, error) {
	return workspacefs.SafeJoin(root, relativePath)
}

func rejectExistingSymlinkComponents(root, relativePath string) error {
	return workspacefs.RejectExistingSymlinkComponents(root, relativePath)
}

func copyFile(sourcePath, destinationPath string, mode fs.FileMode) error {
	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	targetFile, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer targetFile.Close()

	_, err = io.Copy(targetFile, sourceFile)
	return err
}
