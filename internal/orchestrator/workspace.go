package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/hecatehq/hecate/internal/gitrunner"
	"github.com/hecatehq/hecate/internal/workspacecoord"
	"github.com/hecatehq/hecate/internal/workspacefs"
	"github.com/hecatehq/hecate/pkg/types"
)

type WorkspaceManager struct {
	root string

	// Test-only race seams. Production constructors leave these nil.
	beforeProvisionRootOpen func()
	afterProvisionRootOpen  func()
	beforeProvisionCommit   func()
	beforeProvisionReturn   func()
}

type workspaceProvisionPlan struct {
	workspacePath string
	root          string
	relativePath  string
	source        workspaceSourceSpec
	requiresWrite bool
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
	plan, err := m.planProvision(task, run)
	if err != nil {
		return "", err
	}
	return m.provisionPlanned(ctx, plan)
}

// planProvision resolves the exact workspace destination without creating any
// filesystem entries. Task start uses the planned path to acquire shared
// workspace admission before clone, copy, or mkdir can mutate the destination.
func (m *WorkspaceManager) planProvision(task types.Task, run types.TaskRun) (workspaceProvisionPlan, error) {
	if m == nil {
		return workspaceProvisionPlan{}, fmt.Errorf("workspace manager is not configured")
	}
	source := workspaceSource(task)
	// A new task segment may intentionally reuse a workspace that Hecate
	// already provisioned for this chat. Re-provisioning a Git source with
	// clone would discard unstaged and untracked work, so preserve the exact
	// runtime-owned root while keeping the task's operator posture persistent
	// or ephemeral rather than relabelling it as source-folder in_place.
	if task.WorkspaceReuse {
		if source.path == "" {
			return workspaceProvisionPlan{}, fmt.Errorf("workspace reuse requires an absolute, existing working_directory or repo path")
		}
		return workspaceProvisionPlan{workspacePath: source.path}, nil
	}
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
			return workspaceProvisionPlan{}, fmt.Errorf("workspace_mode=in_place requires an absolute, existing working_directory or repo path")
		}
		return workspaceProvisionPlan{workspacePath: source.path}, nil
	}
	root, workspacePath, relativePath, err := m.plannedWorkspacePath(task.ID, run.ID)
	if err != nil {
		return workspaceProvisionPlan{}, err
	}
	return workspaceProvisionPlan{
		workspacePath: workspacePath,
		root:          root,
		relativePath:  relativePath,
		source:        source,
		requiresWrite: true,
	}, nil
}

func (m *WorkspaceManager) provisionPlanned(ctx context.Context, plan workspaceProvisionPlan) (workspacePathResult string, returnErr error) {
	if !plan.requiresWrite {
		return plan.workspacePath, nil
	}
	if m != nil && m.beforeProvisionRootOpen != nil {
		m.beforeProvisionRootOpen()
	}
	root, err := openOrCreatePlannedRoot(plan.root)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if m != nil && m.afterProvisionRootOpen != nil {
		m.afterProvisionRootOpen()
	}

	segments, err := localPathSegments(plan.relativePath)
	if err != nil || len(segments) != 2 {
		return "", fmt.Errorf("invalid planned workspace destination %q", plan.relativePath)
	}
	workspacePath := filepath.Join(filepath.Clean(plan.root), plan.relativePath)
	if workspacePath != filepath.Clean(plan.workspacePath) {
		return "", fmt.Errorf("workspace destination changed after provisioning admission")
	}

	taskRoot, err := openOrCreateChildRoot(root, segments[0], 0o755)
	if err != nil {
		return "", fmt.Errorf("create generated task workspace root: %w", err)
	}
	defer taskRoot.Close()
	if destinationInfo, err := taskRoot.Lstat(segments[1]); err == nil {
		if plan.source.kind != "" {
			return "", fmt.Errorf("workspace destination %q already exists", workspacePath)
		}
		if err := contextError(ctx); err != nil {
			return "", err
		}
		existingRoot, err := openChildRootMatching(taskRoot, segments[1], destinationInfo)
		if err != nil {
			return "", fmt.Errorf("open existing generated workspace: %w", err)
		}
		openedInfo, err := existingRoot.Stat(".")
		closeErr := existingRoot.Close()
		if err := errors.Join(err, closeErr); err != nil {
			return "", fmt.Errorf("inspect existing generated workspace: %w", err)
		}
		if err := verifyStableRootPath(workspacePath, openedInfo); err != nil {
			return "", fmt.Errorf("workspace destination changed during provisioning: %w", err)
		}
		return workspacePath, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect workspace destination: %w", err)
	}

	stageName, stageRoot, err := createWorkspaceStageRoot(taskRoot)
	if err != nil {
		return "", err
	}
	stageInfo, err := stageRoot.Stat(".")
	if err != nil {
		cleanupErr := errors.Join(stageRoot.Close(), taskRoot.RemoveAll(stageName))
		return "", errors.Join(
			fmt.Errorf("inspect new workspace staging directory: %w", err),
			cleanupErr,
		)
	}
	stageOpen := true
	committed := false
	defer func() {
		var cleanupErr error
		if stageOpen {
			cleanupErr = errors.Join(cleanupErr, stageRoot.Close())
			stageOpen = false
		}
		if committed {
			return
		}
		cleanupErr = errors.Join(cleanupErr, cleanupProvisionedRoot(taskRoot, stageName, stageInfo))
		cleanupErr = errors.Join(cleanupErr, cleanupProvisionedRoot(taskRoot, segments[1], stageInfo))
		if cleanupErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("clean incomplete workspace provisioning: %w", cleanupErr))
		}
	}()

	directoryModes, err := provisionWorkspaceSource(ctx, stageRoot, plan.source)
	if err != nil {
		return "", err
	}
	if err := applyStagedDirectoryModes(ctx, stageRoot, directoryModes); err != nil {
		return "", fmt.Errorf("restore generated workspace directory modes: %w", err)
	}
	if err := contextError(ctx); err != nil {
		return "", err
	}
	closeErr := stageRoot.Close()
	stageOpen = false
	if closeErr != nil {
		return "", fmt.Errorf("close staged workspace: %w", closeErr)
	}
	if m != nil && m.beforeProvisionCommit != nil {
		m.beforeProvisionCommit()
	}
	if err := contextError(ctx); err != nil {
		return "", err
	}
	stagedPathInfo, err := taskRoot.Lstat(stageName)
	if err != nil || !os.SameFile(stageInfo, stagedPathInfo) {
		if err != nil {
			return "", fmt.Errorf("verify staged workspace before placement: %w", err)
		}
		return "", fmt.Errorf("staged workspace changed before placement")
	}
	if err := renameRootNoReplace(taskRoot, stageName, segments[1]); err != nil {
		return "", fmt.Errorf("place generated workspace: %w", err)
	}
	placedRoot, err := openChildRootMatching(taskRoot, segments[1], stageInfo)
	if err != nil {
		return "", fmt.Errorf("verify placed generated workspace: %w", err)
	}
	if err := placedRoot.Close(); err != nil {
		return "", fmt.Errorf("close placed generated workspace: %w", err)
	}
	if err := contextError(ctx); err != nil {
		return "", err
	}
	if m != nil && m.beforeProvisionReturn != nil {
		m.beforeProvisionReturn()
	}
	if err := verifyStableRootPath(workspacePath, stageInfo); err != nil {
		return "", fmt.Errorf("workspace destination changed during provisioning: %w", err)
	}
	committed = true
	return workspacePath, nil
}

func (m *WorkspaceManager) plannedWorkspacePath(taskID, runID string) (root, workspacePath, relativePath string, err error) {
	root, err = workspacecoord.CanonicalWorkspaceForCreation(m.root)
	if err != nil {
		return "", "", "", err
	}
	rootExists := false
	if info, statErr := os.Stat(root); statErr == nil {
		if !info.IsDir() {
			return "", "", "", fmt.Errorf("workspace root %q is not a directory", root)
		}
		rootExists = true
	} else if !os.IsNotExist(statErr) {
		return "", "", "", fmt.Errorf("inspect workspace root: %w", statErr)
	}
	taskSegment, err := workspacePathSegment("task id", taskID)
	if err != nil {
		return "", "", "", err
	}
	runSegment, err := workspacePathSegment("run id", runID)
	if err != nil {
		return "", "", "", err
	}
	relativePath = filepath.Join(taskSegment, runSegment)
	if rootExists {
		workspacePath, err = safeJoinWithinRoot(root, relativePath)
		if err != nil {
			return "", "", "", err
		}
	} else {
		workspacePath = filepath.Join(root, relativePath)
	}
	return root, workspacePath, relativePath, nil
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

func provisionWorkspaceSource(ctx context.Context, destination *os.Root, source workspaceSourceSpec) ([]stagedDirectoryMode, error) {
	switch source.kind {
	case "git":
		return provisionGitWorkspace(ctx, source.path, destination)
	case "directory":
		return provisionDirectoryWorkspace(ctx, source.path, destination)
	default:
		return nil, contextError(ctx)
	}
}

func provisionGitWorkspace(ctx context.Context, sourcePath string, destination *os.Root) ([]stagedDirectoryMode, error) {
	tempRoot, err := os.MkdirTemp("", "hecate-workspace-clone-")
	if err != nil {
		return nil, fmt.Errorf("create private Git clone staging directory: %w", err)
	}
	defer os.RemoveAll(tempRoot)
	canonicalTempRoot, err := filepath.EvalSymlinks(tempRoot)
	if err != nil {
		return nil, fmt.Errorf("canonicalize private Git clone staging directory: %w", err)
	}
	clonePath := filepath.Join(canonicalTempRoot, "workspace")
	result, err := gitrunner.NewLocalRunner().Clone(ctx, sourcePath, clonePath)
	if err != nil {
		output := strings.TrimSpace(result.Stdout)
		if stderr := strings.TrimSpace(result.Stderr); stderr != "" {
			if output != "" {
				output += "\n"
			}
			output += stderr
		}
		if output != "" {
			return nil, fmt.Errorf("clone workspace: %w: %s", err, output)
		}
		return nil, fmt.Errorf("clone workspace: %w", err)
	}
	cloneRoot, _, err := openStableExistingRoot(clonePath)
	if err != nil {
		return nil, fmt.Errorf("open staged Git workspace: %w", err)
	}
	defer cloneRoot.Close()
	return copyRootDirectory(ctx, cloneRoot, destination)
}

func provisionDirectoryWorkspace(ctx context.Context, sourcePath string, destination *os.Root) ([]stagedDirectoryMode, error) {
	sourceRoot, _, err := openStableExistingRoot(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("open workspace copy source: %w", err)
	}
	defer sourceRoot.Close()
	return copyRootDirectory(ctx, sourceRoot, destination)
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
	sourceRoot, _, err := openStableExistingRoot(sourcePath)
	if err != nil {
		return err
	}
	defer sourceRoot.Close()
	destinationRoot, _, err := openStableExistingRoot(destinationPath)
	if err != nil {
		return err
	}
	defer destinationRoot.Close()
	directoryModes, err := copyRootDirectory(context.Background(), sourceRoot, destinationRoot)
	if err != nil {
		return err
	}
	return applyStagedDirectoryModes(context.Background(), destinationRoot, directoryModes)
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

func localPathSegments(relativePath string) ([]string, error) {
	if !filepath.IsLocal(relativePath) || filepath.Clean(relativePath) != relativePath {
		return nil, fmt.Errorf("unsafe local path %q", relativePath)
	}
	segments := strings.Split(relativePath, string(filepath.Separator))
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." || segment != filepath.Base(segment) {
			return nil, fmt.Errorf("unsafe local path %q", relativePath)
		}
	}
	return segments, nil
}

// openOrCreatePlannedRoot opens the nearest existing ancestor before creating
// anything, verifies that its path has not become a symlink alias, then creates
// each missing component through stable root handles. No path-string mutation
// occurs before the admitted root identity is established.
func openOrCreatePlannedRoot(rootPath string) (*os.Root, error) {
	rootPath = filepath.Clean(rootPath)
	if !filepath.IsAbs(rootPath) {
		return nil, fmt.Errorf("workspace root %q is not absolute", rootPath)
	}
	candidate := rootPath
	missing := make([]string, 0, 4)
	for {
		if _, err := os.Lstat(candidate); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("inspect workspace root ancestor: %w", err)
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return nil, fmt.Errorf("workspace root %q has no existing ancestor", rootPath)
		}
		missing = append(missing, filepath.Base(candidate))
		candidate = parent
	}

	current, _, err := openStableExistingRoot(candidate)
	if err != nil {
		return nil, fmt.Errorf("open workspace root ancestor: %w", err)
	}
	for index := len(missing) - 1; index >= 0; index-- {
		next, createErr := openOrCreateChildRoot(current, missing[index], 0o755)
		closeErr := current.Close()
		if createErr != nil {
			return nil, errors.Join(fmt.Errorf("create workspace root component %q: %w", missing[index], createErr), closeErr)
		}
		if closeErr != nil {
			_ = next.Close()
			return nil, fmt.Errorf("close workspace root ancestor: %w", closeErr)
		}
		current = next
	}
	return current, nil
}

func openStableExistingRoot(path string) (*os.Root, fs.FileInfo, error) {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return nil, nil, fmt.Errorf("root path %q is not absolute", path)
	}
	initialInfo, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	if !initialInfo.IsDir() {
		return nil, nil, fmt.Errorf("root path %q is not a directory", path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, nil, err
	}
	if filepath.Clean(resolved) != path {
		return nil, nil, fmt.Errorf("workspace path uses symlink component %q", path)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, nil, err
	}
	openedInfo, err := root.Stat(".")
	if err != nil {
		_ = root.Close()
		return nil, nil, err
	}
	if !os.SameFile(initialInfo, openedInfo) {
		_ = root.Close()
		return nil, nil, fmt.Errorf("root path %q changed while it was opened", path)
	}
	return root, openedInfo, nil
}

func openOrCreateChildRoot(parent *os.Root, name string, mode fs.FileMode) (*os.Root, error) {
	info, err := parent.Lstat(name)
	if err == nil {
		return openChildRootMatching(parent, name, info)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := parent.Mkdir(name, mode.Perm()); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	info, err = parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	return openChildRootMatching(parent, name, info)
}

func createChildRoot(parent *os.Root, name string, mode fs.FileMode) (*os.Root, error) {
	if info, err := parent.Lstat(name); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("workspace path uses symlink component %q", name)
		}
		return nil, fmt.Errorf("create workspace directory %q: %w", name, os.ErrExist)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := parent.Mkdir(name, mode.Perm()); err != nil {
		return nil, err
	}
	info, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	return openChildRootMatching(parent, name, info)
}

func openChildRootMatching(parent *os.Root, name string, expected fs.FileInfo) (*os.Root, error) {
	if expected == nil || expected.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("workspace path uses symlink component %q", name)
	}
	if !expected.IsDir() {
		return nil, fmt.Errorf("workspace path component %q is not a directory", name)
	}
	child, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	openedInfo, err := child.Stat(".")
	if err != nil {
		_ = child.Close()
		return nil, err
	}
	if !os.SameFile(expected, openedInfo) {
		_ = child.Close()
		return nil, fmt.Errorf("workspace path component %q changed while it was opened", name)
	}
	return child, nil
}

func createWorkspaceStageRoot(parent *os.Root) (string, *os.Root, error) {
	for range 16 {
		var token [16]byte
		if _, err := rand.Read(token[:]); err != nil {
			return "", nil, fmt.Errorf("generate workspace staging name: %w", err)
		}
		name := ".hecate-provision-" + hex.EncodeToString(token[:])
		root, err := createChildRoot(parent, name, 0o700)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", nil, fmt.Errorf("create workspace staging directory: %w", err)
		}
		return name, root, nil
	}
	return "", nil, fmt.Errorf("create unique workspace staging directory")
}

func verifyStableRootPath(path string, expected fs.FileInfo) error {
	root, actual, err := openStableExistingRoot(path)
	if err != nil {
		return err
	}
	defer root.Close()
	if !os.SameFile(expected, actual) {
		return fmt.Errorf("workspace path no longer names the placed directory")
	}
	return nil
}

type stagedDirectoryMode struct {
	path string
	mode fs.FileMode
}

func copyRootDirectory(ctx context.Context, source, destination *os.Root) ([]stagedDirectoryMode, error) {
	return copyRootDirectoryAt(ctx, source, destination, "")
}

func copyRootDirectoryAt(ctx context.Context, source, destination *os.Root, relativePath string) ([]stagedDirectoryMode, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	directory, err := source.Open(".")
	if err != nil {
		return nil, err
	}
	entries, readErr := directory.ReadDir(-1)
	closeErr := directory.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, err
	}
	var directoryModes []stagedDirectoryMode
	for _, entry := range entries {
		if err := contextError(ctx); err != nil {
			return nil, err
		}
		name := entry.Name()
		info, err := source.Lstat(name)
		if err != nil {
			return nil, err
		}
		switch {
		case info.IsDir():
			sourceChild, err := openChildRootMatching(source, name, info)
			if err != nil {
				return nil, err
			}
			destinationChild, err := createChildRoot(destination, name, info.Mode().Perm()|0o700)
			if err != nil {
				_ = sourceChild.Close()
				return nil, err
			}
			childPath := name
			if relativePath != "" {
				childPath = filepath.Join(relativePath, name)
			}
			childModes, copyErr := copyRootDirectoryAt(ctx, sourceChild, destinationChild, childPath)
			closeErr := errors.Join(sourceChild.Close(), destinationChild.Close())
			if err := errors.Join(copyErr, closeErr); err != nil {
				return nil, err
			}
			directoryModes = append(directoryModes, childModes...)
			directoryModes = append(directoryModes, stagedDirectoryMode{path: childPath, mode: info.Mode().Perm()})
		case info.Mode()&os.ModeSymlink != 0:
			target, err := source.Readlink(name)
			if err != nil {
				return nil, err
			}
			if err := destination.Symlink(target, name); err != nil {
				return nil, err
			}
		case info.Mode().IsRegular():
			if err := copyRootFile(ctx, source, destination, name, info); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("workspace source entry %q has unsupported mode %s", name, info.Mode())
		}
	}
	return directoryModes, nil
}

func applyStagedDirectoryModes(ctx context.Context, root *os.Root, directoryModes []stagedDirectoryMode) error {
	for _, directoryMode := range directoryModes {
		if err := contextError(ctx); err != nil {
			return err
		}
		info, err := root.Lstat(directoryMode.path)
		if err != nil {
			return err
		}
		child, err := openChildRootMatching(root, directoryMode.path, info)
		if err != nil {
			return err
		}
		modeErr := child.Chmod(".", directoryMode.mode.Perm())
		closeErr := child.Close()
		if err := errors.Join(modeErr, closeErr); err != nil {
			return err
		}
	}
	return nil
}

func cleanupProvisionedRoot(parent *os.Root, name string, expected fs.FileInfo) error {
	info, err := parent.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if expected == nil || !os.SameFile(expected, info) {
		return nil
	}
	child, err := openChildRootMatching(parent, name, info)
	if err != nil {
		return err
	}
	permissionsErr := makeRootTreeOwnerWritable(child)
	closeErr := child.Close()
	removeErr := parent.RemoveAll(name)
	return errors.Join(permissionsErr, closeErr, removeErr)
}

func makeRootTreeOwnerWritable(root *os.Root) error {
	info, err := root.Stat(".")
	if err != nil {
		return err
	}
	if err := root.Chmod(".", info.Mode().Perm()|0o700); err != nil {
		return err
	}
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	entries, readErr := directory.ReadDir(-1)
	closeErr := directory.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return err
	}
	for _, entry := range entries {
		entryInfo, err := root.Lstat(entry.Name())
		if err != nil {
			return err
		}
		if entryInfo.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if entryInfo.Mode().IsRegular() {
			if err := root.Chmod(entry.Name(), entryInfo.Mode().Perm()|0o600); err != nil {
				return err
			}
			continue
		}
		if !entryInfo.IsDir() {
			continue
		}
		child, err := openChildRootMatching(root, entry.Name(), entryInfo)
		if err != nil {
			return err
		}
		permissionsErr := makeRootTreeOwnerWritable(child)
		closeErr := child.Close()
		if err := errors.Join(permissionsErr, closeErr); err != nil {
			return err
		}
	}
	return nil
}

func copyRootFile(ctx context.Context, source, destination *os.Root, name string, expected fs.FileInfo) error {
	sourceFile, err := source.OpenFile(name, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	openedInfo, err := sourceFile.Stat()
	if err != nil || !os.SameFile(expected, openedInfo) {
		_ = sourceFile.Close()
		if err != nil {
			return err
		}
		return fmt.Errorf("workspace source file %q changed while it was opened", name)
	}
	destinationFile, err := destination.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, expected.Mode().Perm())
	if err != nil {
		_ = sourceFile.Close()
		return err
	}
	_, copyErr := io.Copy(destinationFile, contextReader{ctx: ctx, reader: sourceFile})
	return errors.Join(copyErr, destinationFile.Close(), sourceFile.Close())
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextReader) Read(buffer []byte) (int, error) {
	if err := contextError(reader.ctx); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
