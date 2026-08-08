package gitrunner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/hecatehq/hecate/internal/localfs"
)

const (
	readOnlyViewMetadataLimit    = 1024 * 1024
	trackedPathOutputLimit       = 8 * 1024 * 1024
	attributeMetadataOutputLimit = trackedPathOutputLimit * 3
	singlePathAttributeLimit     = 256 * 1024
	stagedChangeProbeOutputLimit = 4 * 1024
)

// ReadOnlyView is an immutable Git control-plane snapshot for passive
// inspection. The worktree, index, and object database remain the source
// repository's read-only data, but Git reads configuration, HEAD, refs, and
// info attributes from a private temporary gitdir. Repository config changes
// therefore cannot introduce executable helpers between validation and use.
type ReadOnlyView struct {
	runner          *LocalRunner
	workspace       string
	workTree        string
	workspacePrefix string
	indexPath       string
	tempDir         string
	workspaceRoot   *workspaceRootIdentity
}

type workspaceRootIdentity struct {
	path     string
	info     fs.FileInfo
	material string
}

// indexMutationLease reserves Git's conventional index lock while Hecate
// validates and mutates the worktree. The reverse apply itself does not change
// the index, but holding this lock prevents a concurrent well-behaved Git
// writer from staging between the final index check and the worktree mutation.
type indexMutationLease struct {
	root     *os.Root
	file     *os.File
	lockName string
}

// NewReadOnlyView snapshots the non-executable repository metadata needed by
// status/diff and returns a runner that never loads the source repository's
// mutable config. The caller must Close the view.
func (r *LocalRunner) NewReadOnlyView(ctx context.Context, workspace string) (*ReadOnlyView, error) {
	// Passive inspection must not inherit Git settings that can update the
	// source index, lazily contact a remote, or prompt for credentials. Apply
	// these overrides before the metadata probes as well as to the completed
	// view so every command in the construction sequence has the same posture.
	passiveRunner := *r
	passiveRunner.Env = replaceEnvironment(r.env(), map[string]string{
		"GIT_OPTIONAL_LOCKS":  "0",
		"GIT_NO_LAZY_FETCH":   "1",
		"GIT_TERMINAL_PROMPT": "0",
	})
	r = &passiveRunner

	inspector, err := localfs.NewInspector()
	if err != nil {
		return nil, fmt.Errorf("inspect passive Git filesystem boundaries: %w", err)
	}
	if err := inspector.EnsurePath(workspace); err != nil {
		return nil, fmt.Errorf("passive Git review requires a supported local workspace filesystem: %w", err)
	}
	workspace, err = cleanWorkspace(workspace)
	if err != nil {
		return nil, err
	}
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace identity: %w", err)
	}
	workspace = filepath.Clean(workspace)
	if err := inspector.EnsurePath(workspace); err != nil {
		return nil, fmt.Errorf("passive Git review requires a supported local workspace filesystem: %w", err)
	}
	if err := inspector.EnsureTree(workspace); err != nil {
		return nil, fmt.Errorf("passive Git review requires a workspace tree without network, userspace, or unknown descendant mounts: %w", err)
	}
	workspaceRoot, err := captureWorkspaceRootIdentity(workspace)
	if err != nil {
		return nil, fmt.Errorf("capture workspace identity: %w", err)
	}
	workTree, err := r.probePathValue(ctx, workspace, false, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	workTree = normalizeProbedGitPath(workspace, workTree)
	if resolved, resolveErr := filepath.EvalSymlinks(workTree); resolveErr == nil {
		workTree = resolved
	}
	if err := inspector.EnsurePath(workTree); err != nil {
		return nil, fmt.Errorf("passive Git review requires a supported local worktree filesystem: %w", err)
	}
	gitPrefix, err := r.probePathValue(ctx, workspace, true, "rev-parse", "--show-prefix")
	if err != nil {
		return nil, err
	}
	gitPrefix = strings.TrimSuffix(gitPrefix, "/")
	workspacePrefix := filepath.Clean(filepath.FromSlash(gitPrefix))
	if gitPrefix == "" {
		workspacePrefix = "."
	}
	if !filepath.IsLocal(workspacePrefix) {
		return nil, fmt.Errorf("unsafe Git workspace prefix %q", gitPrefix)
	}
	workspaceInfo, err := os.Stat(workspace)
	if err != nil {
		return nil, fmt.Errorf("inspect workspace %q: %w", workspace, err)
	}
	prefixInfo, err := os.Stat(filepath.Join(workTree, workspacePrefix))
	if err != nil || !os.SameFile(workspaceInfo, prefixInfo) {
		return nil, fmt.Errorf("workspace %q is outside Git worktree %q", workspace, workTree)
	}
	gitDir, err := r.probePathValue(ctx, workspace, false, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return nil, err
	}
	commonDir, err := r.probePathValue(ctx, workspace, false, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	indexPath, err := r.probePathValue(ctx, workspace, false, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return nil, err
	}
	infoAttributesPath, err := r.probePathValue(ctx, workspace, false, "rev-parse", "--path-format=absolute", "--git-path", "info/attributes")
	if err != nil {
		return nil, err
	}
	infoExcludePath, err := r.probePathValue(ctx, workspace, false, "rev-parse", "--path-format=absolute", "--git-path", "info/exclude")
	if err != nil {
		return nil, err
	}
	gitDir, err = validatePassiveGitDirectory(inspector, "Git directory", normalizeProbedGitPath(workspace, gitDir))
	if err != nil {
		return nil, err
	}
	commonDir, err = validatePassiveGitDirectory(inspector, "Git common directory", normalizeProbedGitPath(workspace, commonDir))
	if err != nil {
		return nil, err
	}
	indexPath, err = validatePassiveGitFilePath(inspector, "Git index", normalizeProbedGitPath(workspace, indexPath))
	if err != nil {
		return nil, err
	}
	objectDir, err := validatePassiveGitDirectory(inspector, "Git object directory", filepath.Join(commonDir, "objects"))
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"alternates", "http-alternates"} {
		alternates, readErr := readBoundedOptionalFileContext(ctx, filepath.Join(objectDir, "info", name), readOnlyViewMetadataLimit)
		if readErr != nil {
			return nil, fmt.Errorf("inspect Git object alternates: %w", readErr)
		}
		if len(bytes.TrimSpace(alternates)) > 0 {
			return nil, errors.New("passive Git review does not support alternate object databases")
		}
	}
	objectFormat, err := r.probeValue(ctx, workspace, "rev-parse", "--show-object-format")
	if err != nil {
		return nil, err
	}
	objectFormat = strings.ToLower(strings.TrimSpace(objectFormat))
	if objectFormat != "sha1" && objectFormat != "sha256" {
		return nil, fmt.Errorf("unsupported Git object format %q", objectFormat)
	}

	branchRef, err := r.probeOptionalValue(ctx, workspace, "symbolic-ref", "--quiet", "HEAD")
	if err != nil {
		return nil, err
	}
	headOID, err := r.probeOptionalValue(ctx, workspace, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		return nil, err
	}
	if err := validateReadOnlyViewHead(branchRef, headOID, objectFormat); err != nil {
		return nil, err
	}

	coreConfig, err := r.safeCoreConfig(ctx, workspace)
	if err != nil {
		return nil, err
	}
	// Passive completeness must not inherit repository settings that suppress
	// same-size or stat-only worktree changes from ordinary status/diff output.
	coreConfig["core.ignorestat"] = "false"
	coreConfig["core.trustctime"] = "true"
	coreConfig["core.checkstat"] = "default"
	infoAttributes, err := readBoundedOptionalFileContext(ctx, infoAttributesPath, readOnlyViewMetadataLimit)
	if err != nil {
		return nil, fmt.Errorf("read Git info attributes: %w", err)
	}
	infoExclude, err := readBoundedOptionalFileContext(ctx, infoExcludePath, readOnlyViewMetadataLimit)
	if err != nil {
		return nil, fmt.Errorf("read Git info excludes: %w", err)
	}
	effectiveExcludesPath, excludesConfigured, err := r.probeOptionalConfigPath(ctx, workspace, "core.excludesFile")
	if err != nil {
		return nil, err
	}
	if excludesConfigured && effectiveExcludesPath != "" {
		effectiveExcludesPath = normalizeProbedGitPath(workTree, effectiveExcludesPath)
	} else {
		if !excludesConfigured {
			effectiveExcludesPath = defaultGitExcludesPath(workTree, r.env())
		}
	}
	effectiveExcludes, err := readBoundedOptionalFileContext(ctx, effectiveExcludesPath, readOnlyViewMetadataLimit)
	if err != nil {
		return nil, fmt.Errorf("read effective Git excludes: %w", err)
	}
	infoExclude = combineGitExcludeSources(effectiveExcludes, infoExclude)
	if len(infoExclude) > readOnlyViewMetadataLimit {
		return nil, fmt.Errorf("combined Git excludes exceed %d bytes", readOnlyViewMetadataLimit)
	}

	tempBase, err := filepath.Abs(r.passiveTempDir())
	if err != nil {
		return nil, fmt.Errorf("resolve passive Git temporary directory: %w", err)
	}
	tempBase = filepath.Clean(tempBase)
	if err := inspector.EnsurePath(tempBase); err != nil {
		return nil, fmt.Errorf("passive Git metadata requires a supported local temporary directory: %w", err)
	}
	resolvedTempBase, err := filepath.EvalSymlinks(tempBase)
	if err != nil {
		return nil, fmt.Errorf("resolve passive Git temporary directory: %w", err)
	}
	if err := inspector.EnsurePath(resolvedTempBase); err != nil {
		return nil, fmt.Errorf("passive Git metadata requires a supported local temporary directory: %w", err)
	}
	tempDir, err := os.MkdirTemp(resolvedTempBase, "hecate-git-read-")
	if err != nil {
		return nil, fmt.Errorf("create passive Git metadata view: %w", err)
	}
	cleanup := func(err error) (*ReadOnlyView, error) {
		_ = os.RemoveAll(tempDir)
		return nil, err
	}
	if pathWithinDirectory(workTree, tempDir) {
		return cleanup(errors.New("passive Git metadata directory overlaps the inspected worktree"))
	}
	tempHandle, err := os.Open(tempDir)
	if err != nil {
		return cleanup(fmt.Errorf("open passive Git metadata directory: %w", err))
	}
	tempInfo, tempInfoErr := tempHandle.Stat()
	tempFilesystemErr := localfs.EnsureBoundedFile(tempHandle)
	tempCloseErr := tempHandle.Close()
	if tempInfoErr != nil || tempFilesystemErr != nil || tempCloseErr != nil || tempInfo == nil || !tempInfo.IsDir() {
		return cleanup(errors.New("passive Git metadata directory is not a supported local directory"))
	}
	for _, rel := range []string{"objects", "refs", "info"} {
		if err := os.MkdirAll(filepath.Join(tempDir, rel), 0o700); err != nil {
			return cleanup(fmt.Errorf("create passive Git metadata view: %w", err))
		}
	}
	// Use ordinary private empty files instead of platform null devices. Git for
	// Windows can diagnose device-style config and attribute paths on stderr,
	// while these files have identical semantics and remain inside the bounded
	// passive metadata view.
	neutralConfigPath := filepath.Join(tempDir, "global-config")
	neutralAttributesPath := filepath.Join(tempDir, "global-attributes")
	neutralExcludesPath := filepath.Join(tempDir, "global-exclude")
	for _, neutralPath := range []string{neutralConfigPath, neutralAttributesPath, neutralExcludesPath} {
		if err := os.WriteFile(neutralPath, nil, 0o600); err != nil {
			return cleanup(fmt.Errorf("create passive Git neutral metadata file: %w", err))
		}
	}
	// Git otherwise consults its live XDG default ignore and attribute files.
	// Their effective repository-owned sources are inspected separately; keep
	// ambient user files out of the immutable view.
	coreConfig["core.attributesfile"] = neutralAttributesPath
	coreConfig["core.excludesfile"] = neutralExcludesPath
	if err := writeReadOnlyViewConfig(filepath.Join(tempDir, "config"), objectFormat, coreConfig); err != nil {
		return cleanup(err)
	}
	if err := writeReadOnlyViewHead(tempDir, branchRef, headOID); err != nil {
		return cleanup(err)
	}
	if len(infoAttributes) > 0 {
		if err := os.WriteFile(filepath.Join(tempDir, "info", "attributes"), infoAttributes, 0o600); err != nil {
			return cleanup(fmt.Errorf("snapshot Git info attributes: %w", err))
		}
	}
	if len(infoExclude) > 0 {
		if err := os.WriteFile(filepath.Join(tempDir, "info", "exclude"), infoExclude, 0o600); err != nil {
			return cleanup(fmt.Errorf("snapshot Git info excludes: %w", err))
		}
	}

	viewRunner := *r
	viewRunner.Env = replaceEnvironment(r.env(), map[string]string{
		"GIT_DIR":                          tempDir,
		"GIT_COMMON_DIR":                   tempDir,
		"GIT_WORK_TREE":                    workTree,
		"GIT_INDEX_FILE":                   indexPath,
		"GIT_OBJECT_DIRECTORY":             objectDir,
		"GIT_ALTERNATE_OBJECT_DIRECTORIES": "",
		"GIT_CONFIG_NOSYSTEM":              "1",
		"GIT_CONFIG_GLOBAL":                neutralConfigPath,
		"GIT_CONFIG_SYSTEM":                neutralConfigPath,
		"GIT_ATTR_NOSYSTEM":                "1",
	})
	viewRunner.ReadOnlyPaths = appendUniquePaths(viewRunner.ReadOnlyPaths,
		tempDir,
		workTree,
		gitDir,
		commonDir,
	)
	view := &ReadOnlyView{
		runner:          &viewRunner,
		workspace:       workspace,
		workTree:        workTree,
		workspacePrefix: workspacePrefix,
		indexPath:       indexPath,
		tempDir:         tempDir,
		workspaceRoot:   workspaceRoot,
	}
	if !view.workspaceIdentityMatches(workspaceRoot) {
		return cleanup(ErrReviewSnapshotChanged)
	}
	return view, nil
}

func validatePassiveGitDirectory(inspector *localfs.Inspector, label, path string) (string, error) {
	path = filepath.Clean(path)
	if inspector == nil {
		return "", errors.New("passive Git filesystem inspector is unavailable")
	}
	if err := inspector.EnsurePath(path); err != nil {
		return "", fmt.Errorf("passive Git review requires a supported local %s: %w", strings.ToLower(label), err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", strings.ToLower(label), err)
	}
	resolved = filepath.Clean(resolved)
	if err := inspector.EnsureTree(resolved); err != nil {
		return "", fmt.Errorf("passive Git review requires a bounded local %s tree: %w", strings.ToLower(label), err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", strings.ToLower(label), err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", label)
	}
	return resolved, nil
}

func validatePassiveGitFilePath(inspector *localfs.Inspector, label, path string) (string, error) {
	path = filepath.Clean(path)
	if inspector == nil {
		return "", errors.New("passive Git filesystem inspector is unavailable")
	}
	if err := inspector.EnsurePath(path); err != nil {
		return "", fmt.Errorf("passive Git review requires a supported local %s: %w", strings.ToLower(label), err)
	}
	parent, err := validatePassiveGitDirectory(inspector, label+" parent", filepath.Dir(path))
	if err != nil {
		return "", err
	}
	path = filepath.Join(parent, filepath.Base(path))
	file, err := openReadOnlyMetadata(path)
	if errors.Is(err, os.ErrNotExist) {
		return path, nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", strings.ToLower(label), err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close %s: %w", strings.ToLower(label), err)
	}
	return path, nil
}

func pathWithinDirectory(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func captureWorkspaceRootIdentity(path string) (*workspaceRootIdentity, error) {
	path = filepath.Clean(path)
	entry, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if entry.Mode()&os.ModeSymlink != 0 || !entry.IsDir() {
		return nil, errors.New("workspace root is not a stable directory")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Stat(".")
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || !os.SameFile(entry, info) {
		return nil, errors.New("workspace root identity changed")
	}
	material, err := workspaceRootIdentityMaterial(path, info)
	if err != nil {
		return nil, err
	}
	return &workspaceRootIdentity{path: path, info: info, material: material}, nil
}

func (v *ReadOnlyView) workspaceIdentityMatches(expected *workspaceRootIdentity) bool {
	if v == nil || expected == nil || expected.info == nil || filepath.Clean(v.workspace) != expected.path {
		return false
	}
	current, err := captureWorkspaceRootIdentity(expected.path)
	return err == nil && current.path == expected.path && os.SameFile(expected.info, current.info)
}

func (v *ReadOnlyView) Close() error {
	if v == nil || strings.TrimSpace(v.tempDir) == "" {
		return nil
	}
	err := os.RemoveAll(v.tempDir)
	v.tempDir = ""
	return err
}

func (v *ReadOnlyView) lockIndexForMutation(ctx context.Context) (*indexMutationLease, error) {
	if v == nil || strings.TrimSpace(v.indexPath) == "" {
		return nil, errors.New("passive Git metadata view has no index path")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	indexRoot, err := os.OpenRoot(filepath.Dir(v.indexPath))
	if err != nil {
		return nil, fmt.Errorf("open Git index directory for conditional apply: %w", err)
	}
	lockName := filepath.Base(v.indexPath) + ".lock"
	file, err := indexRoot.OpenFile(lockName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_ = indexRoot.Close()
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("%w: Git index is busy", ErrDiffSnapshotNotApplicable)
		}
		return nil, fmt.Errorf("reserve Git index for conditional apply: %w", err)
	}
	lease := &indexMutationLease{root: indexRoot, file: file, lockName: lockName}
	if err := ctx.Err(); err != nil {
		_ = lease.Release()
		return nil, err
	}
	return lease, nil
}

func (l *indexMutationLease) Release() error {
	if l == nil || (l.file == nil && l.root == nil) {
		return nil
	}
	var closeErr error
	if l.file != nil {
		closeErr = l.file.Close()
	}
	l.file = nil
	var removeErr error
	if l.root != nil {
		removeErr = l.root.Remove(l.lockName)
	}
	if errors.Is(removeErr, os.ErrNotExist) {
		removeErr = nil
	}
	var rootCloseErr error
	if l.root != nil {
		rootCloseErr = l.root.Close()
	}
	l.root = nil
	return errors.Join(closeErr, removeErr, rootCloseErr)
}

func (v *ReadOnlyView) RunLimited(ctx context.Context, maxBytes int64, args ...string) (Result, error) {
	if v == nil || v.runner == nil {
		return Result{ExitCode: -1}, errors.New("passive Git metadata view is not configured")
	}
	return v.runner.RunLimitedReadOnly(ctx, v.workspace, maxBytes, args...)
}

// RunLimitedInput runs a fixed passive Git command with caller-provided input.
func (v *ReadOnlyView) RunLimitedInput(ctx context.Context, maxBytes int64, stdin string, args ...string) (Result, error) {
	if v == nil || v.runner == nil {
		return Result{ExitCode: -1}, errors.New("passive Git metadata view is not configured")
	}
	return v.runner.RunLimitedReadOnlyInput(ctx, v.workspace, maxBytes, stdin, args...)
}

// RejectContentConversionAttributes fails closed when a tracked path in the
// scoped workspace has an effective filter attribute. Git's clean/process
// filters are executable repository configuration, so a passive status or
// diff must not continue when conversion behavior is effective or ambiguous.
func (v *ReadOnlyView) RejectContentConversionAttributes(ctx context.Context) error {
	if v == nil || v.runner == nil {
		return errors.New("passive Git metadata view is not configured")
	}
	result, err := v.RunLimited(ctx, trackedPathOutputLimit,
		"--no-pager", "ls-files", "-z", "--", ".",
	)
	if err != nil {
		return fmt.Errorf("inspect tracked paths for passive Git attributes: %w", err)
	}
	if result.StdoutTruncated || result.StderrTruncated {
		return fmt.Errorf("%w: tracked path metadata exceeded %d bytes", errInspectionMetadataTooLarge, trackedPathOutputLimit)
	}
	if result.Stdout == "" {
		return nil
	}
	attributes, err := v.RunLimitedInput(ctx, attributeMetadataOutputLimit, result.Stdout,
		"--no-pager",
		"check-attr", "-z", "--stdin", "--all",
	)
	if err != nil {
		return fmt.Errorf("resolve passive Git attributes: %w", err)
	}
	if attributes.StdoutTruncated || attributes.StderrTruncated {
		return fmt.Errorf("%w: effective attribute metadata exceeded %d bytes", errInspectionMetadataTooLarge, attributeMetadataOutputLimit)
	}
	return RejectEffectiveContentConversionFilters(ctx, attributes.Stdout)
}

// RejectContentConversionAttributesForPath applies the same passive filter
// policy without enumerating unrelated tracked paths for a targeted review.
func (v *ReadOnlyView) RejectContentConversionAttributesForPath(ctx context.Context, path string) error {
	if v == nil || v.runner == nil {
		return errors.New("passive Git metadata view is not configured")
	}
	attributes, err := v.RunLimitedInput(ctx, singlePathAttributeLimit, path+"\x00",
		"--no-pager",
		"check-attr", "-z", "--stdin", "--all",
	)
	if err != nil {
		return fmt.Errorf("resolve passive Git attributes for reviewed path: %w", err)
	}
	if attributes.StdoutTruncated || attributes.StderrTruncated {
		return fmt.Errorf("%w: selected-path attribute metadata exceeded %d bytes", errInspectionMetadataTooLarge, singlePathAttributeLimit)
	}
	return RejectEffectiveContentConversionFilters(ctx, attributes.Stdout)
}

// RejectEffectiveContentConversionFilters validates the NUL-delimited triples
// emitted by `git check-attr -z --all` and rejects any effective filter.
func RejectEffectiveContentConversionFilters(ctx context.Context, output string) error {
	records := strings.Split(output, "\x00")
	if len(records) == 0 || records[len(records)-1] != "" || (len(records)-1)%3 != 0 {
		return errors.New("passive Git inspection refused malformed effective attribute metadata")
	}
	for i := 0; i < len(records)-1; i += 3 {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("resolve passive Git attributes: %w", err)
		}
		path, attribute := records[i], records[i+1]
		if path == "" || attribute == "" {
			return errors.New("passive Git inspection refused malformed effective attribute metadata")
		}
		if attribute == "filter" {
			return errors.New("passive Git inspection refused: a scoped path has an effective or ambiguous content-conversion filter")
		}
	}
	return nil
}

// IndexVisibilityEntries returns scoped index flags that can suppress ordinary
// status and diff output. Callers must not claim a complete review or grant
// discard authority while any entry is present.
func (v *ReadOnlyView) IndexVisibilityEntries(ctx context.Context) ([]IndexVisibilityEntry, error) {
	if v == nil || v.runner == nil {
		return nil, errors.New("passive Git metadata view is not configured")
	}
	result, err := v.RunLimited(ctx, trackedPathOutputLimit, passiveInspectionArgs(
		"ls-files", "-v", "-z", "--", ".",
	)...)
	if err != nil {
		return nil, fmt.Errorf("inspect Git index visibility flags: %w", err)
	}
	if result.StdoutTruncated || result.StderrTruncated {
		return nil, fmt.Errorf("%w: Git index visibility metadata exceeded %d bytes", errInspectionMetadataTooLarge, trackedPathOutputLimit)
	}
	if result.Stderr != "" {
		return nil, errors.New("inspect Git index visibility flags: unexpected diagnostics")
	}
	if result.Stdout == "" {
		return []IndexVisibilityEntry{}, nil
	}
	if !strings.HasSuffix(result.Stdout, "\x00") {
		return nil, errors.New("inspect Git index visibility flags: malformed ls-files output")
	}
	entries := make([]IndexVisibilityEntry, 0)
	for _, record := range strings.Split(strings.TrimSuffix(result.Stdout, "\x00"), "\x00") {
		if len(record) < 3 || record[1] != ' ' {
			return nil, errors.New("inspect Git index visibility flags: malformed ls-files record")
		}
		path := record[2:]
		if err := validateReviewPath(path); err != nil {
			return nil, err
		}
		tag := record[0]
		kind := ""
		switch {
		case tag == 'S':
			kind = "skip_worktree"
		case tag == 's':
			kind = "skip_worktree_assume_unchanged"
		case tag >= 'a' && tag <= 'z':
			kind = "assume_unchanged"
		}
		if kind != "" {
			entries = append(entries, IndexVisibilityEntry{Path: path, Kind: kind})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Path == entries[j].Path {
			return entries[i].Kind < entries[j].Kind
		}
		return entries[i].Path < entries[j].Path
	})
	return entries, nil
}

// UnmergedPaths reads conflict stages directly from the index. Git porcelain
// status can depend on repository-state files outside the index; the passive
// view intentionally snapshots only the minimum immutable metadata, so
// destructive review must independently fail closed on any unmerged entry.
func (v *ReadOnlyView) UnmergedPaths(ctx context.Context) ([]string, error) {
	if v == nil || v.runner == nil {
		return nil, errors.New("passive Git metadata view is not configured")
	}
	result, err := v.RunLimited(ctx, trackedPathOutputLimit, passiveInspectionArgs(
		"ls-files", "--unmerged", "--stage", "--full-name", "-z", "--", ".",
	)...)
	if err != nil {
		return nil, fmt.Errorf("inspect unmerged Git index entries: %w", err)
	}
	if result.StdoutTruncated || result.StderrTruncated {
		return nil, fmt.Errorf("%w: unmerged index metadata exceeded %d bytes", errInspectionMetadataTooLarge, trackedPathOutputLimit)
	}
	if result.Stderr != "" {
		return nil, errors.New("inspect unmerged Git index entries: unexpected diagnostics")
	}
	if result.Stdout == "" {
		return []string{}, nil
	}
	if !strings.HasSuffix(result.Stdout, "\x00") {
		return nil, errors.New("inspect unmerged Git index entries: malformed ls-files output")
	}
	paths := make(map[string]struct{})
	for _, record := range strings.Split(strings.TrimSuffix(result.Stdout, "\x00"), "\x00") {
		metadata, path, ok := strings.Cut(record, "\t")
		fields := strings.Fields(metadata)
		if !ok || len(fields) != 3 || (fields[2] != "1" && fields[2] != "2" && fields[2] != "3") {
			return nil, errors.New("inspect unmerged Git index entries: malformed stage record")
		}
		path, err = statusPathRelativeToWorkspace(path, v.WorkspacePrefix())
		if err != nil {
			return nil, err
		}
		if err := validateReviewPath(path); err != nil {
			return nil, err
		}
		paths[path] = struct{}{}
	}
	return sortedReviewPaths(paths), nil
}

// RejectStagedChanges ensures a successful SnapshotDiff covers every tracked
// change in its scoped workspace. Conditional reverse apply currently owns the
// worktree layer only; accepting an index-only change would report a false
// clean snapshot, while folding HEAD-relative changes into the worktree patch
// would leave the index changed. Staged and mixed-layer support therefore
// remains an explicit follow-up rather than weakening discard semantics here.
func (v *ReadOnlyView) RejectStagedChanges(ctx context.Context) error {
	if v == nil || v.runner == nil {
		return errors.New("passive Git metadata view is not configured")
	}
	result, err := v.RunLimited(ctx, stagedChangeProbeOutputLimit, passiveInspectionArgs(
		"diff", "--cached", "--ita-visible-in-index", "--exit-code", "--no-renames", "--no-ext-diff", "--no-textconv", "--binary", "--", ".",
	)...)
	if err == nil {
		if result.Stdout != "" || result.Stderr != "" || result.StdoutTruncated || result.StderrTruncated {
			return errors.New("inspect staged Git changes: unexpected successful diff output")
		}
		return nil
	}
	// `git diff --exit-code` uses exit 1 for differences. Require the
	// canonical patch prefix as well so an exit 1 from an OS sandbox wrapper
	// or another pre-Git failure is not mistaken for a staged change.
	if result.ExitCode == 1 && strings.HasPrefix(result.Stdout, "diff --git ") && result.Stderr == "" && !result.StderrTruncated {
		return ErrStagedChangesUnsupported
	}
	return fmt.Errorf("inspect staged Git changes: %w", err)
}

// RejectSubmoduleChanges prevents a root-worktree patch from becoming discard
// authority for Gitlink state. Reverse-applying that patch cannot reliably
// restore the nested repository's checked-out commit or internal dirtiness.
func (v *ReadOnlyView) RejectSubmoduleChanges(ctx context.Context) error {
	if v == nil || v.runner == nil {
		return errors.New("passive Git metadata view is not configured")
	}
	result, err := v.RunLimited(ctx, trackedPathOutputLimit, passiveInspectionArgs(
		"diff", "--raw", "-z", "--relative", "--no-renames", "--no-ext-diff", "--no-textconv", "--ignore-submodules=none", "--", ".",
	)...)
	if err != nil {
		return fmt.Errorf("inspect Git submodule changes: %w", err)
	}
	if result.StdoutTruncated || result.StderrTruncated || result.Stderr != "" {
		return errors.New("inspect Git submodule changes: unexpected or oversized diagnostics")
	}
	if result.Stdout == "" {
		return nil
	}
	if !strings.HasSuffix(result.Stdout, "\x00") {
		return errors.New("inspect Git submodule changes: malformed raw diff")
	}
	records := strings.Split(strings.TrimSuffix(result.Stdout, "\x00"), "\x00")
	if len(records)%2 != 0 {
		return errors.New("inspect Git submodule changes: malformed raw diff records")
	}
	for index := 0; index < len(records); index += 2 {
		fields := strings.Fields(records[index])
		if len(fields) != 5 || len(fields[0]) != 7 || fields[0][0] != ':' || len(fields[1]) != 6 {
			return errors.New("inspect Git submodule changes: malformed raw diff header")
		}
		if err := validateReviewPath(records[index+1]); err != nil {
			return err
		}
		if fields[0][1:] == "160000" || fields[1] == "160000" {
			return ErrSubmoduleChangesUnsupported
		}
	}
	return nil
}

// WorkspacePrefix returns Workspace relative to WorkTree. Git paths emitted
// with --full-name are relative to this same root.
func (v *ReadOnlyView) WorkspacePrefix() string {
	if v == nil {
		return ""
	}
	return v.workspacePrefix
}

func (r *LocalRunner) probeValue(ctx context.Context, workspace string, args ...string) (string, error) {
	result, err := r.RunLimitedReadOnly(ctx, workspace, readOnlyViewMetadataLimit, append([]string{"--no-pager"}, args...)...)
	if err != nil {
		return "", gitProbeError(args, result, err)
	}
	if result.StdoutTruncated || result.StderrTruncated {
		return "", fmt.Errorf("Git metadata probe output exceeded %d bytes", readOnlyViewMetadataLimit)
	}
	value := strings.TrimSpace(result.Stdout)
	if value == "" {
		return "", fmt.Errorf("Git metadata probe %q returned no value", strings.Join(args, " "))
	}
	return value, nil
}

func (r *LocalRunner) probeOptionalValue(ctx context.Context, workspace string, args ...string) (string, error) {
	result, err := r.RunLimitedReadOnly(ctx, workspace, readOnlyViewMetadataLimit, append([]string{"--no-pager"}, args...)...)
	if err != nil {
		if result.ExitCode == 1 && strings.TrimSpace(result.Stdout) == "" && strings.TrimSpace(result.Stderr) == "" {
			return "", nil
		}
		return "", gitProbeError(args, result, err)
	}
	return strings.TrimSpace(result.Stdout), nil
}

func (r *LocalRunner) probeOptionalConfigPath(ctx context.Context, workspace, key string) (string, bool, error) {
	args := []string{"config", "--path", "--get", key}
	result, err := r.RunLimitedReadOnly(ctx, workspace, readOnlyViewMetadataLimit, append([]string{"--no-pager"}, args...)...)
	if err != nil {
		if result.ExitCode == 1 && result.Stdout == "" && result.Stderr == "" && !result.StdoutTruncated && !result.StderrTruncated {
			return "", false, nil
		}
		return "", false, gitProbeError(args, result, err)
	}
	if result.StdoutTruncated || result.StderrTruncated || result.Stderr != "" {
		return "", false, fmt.Errorf("Git configuration path probe for %s returned unexpected or oversized diagnostics", key)
	}
	if !strings.HasSuffix(result.Stdout, "\n") {
		return "", false, fmt.Errorf("Git configuration path probe for %s returned a malformed path", key)
	}
	value := strings.TrimSuffix(result.Stdout, "\n")
	if runtime.GOOS == "windows" {
		value = strings.TrimSuffix(value, "\r")
	}
	if strings.ContainsRune(value, '\x00') || strings.ContainsAny(value, "\r\n") {
		return "", false, fmt.Errorf("Git configuration path probe for %s returned a malformed path", key)
	}
	if value == "" {
		return "", true, nil
	}
	return value, true, nil
}

func defaultGitExcludesPath(workspace string, env []string) string {
	return defaultGitExcludesPathForOS(workspace, env, runtime.GOOS)
}

func defaultGitExcludesPathForOS(workspace string, env []string, goos string) string {
	if configHome := environmentValueForOS(env, "XDG_CONFIG_HOME", goos); strings.TrimSpace(configHome) != "" {
		return normalizeProbedGitPath(workspace, filepath.Join(configHome, "git", "ignore"))
	}
	if home := environmentValueForOS(env, "HOME", goos); strings.TrimSpace(home) != "" {
		return normalizeProbedGitPath(workspace, filepath.Join(home, ".config", "git", "ignore"))
	}
	if goos == "windows" {
		if home := environmentValueForOS(env, "USERPROFILE", goos); strings.TrimSpace(home) != "" {
			return normalizeProbedGitPath(workspace, filepath.Join(home, ".config", "git", "ignore"))
		}
		drive := environmentValueForOS(env, "HOMEDRIVE", goos)
		homePath := environmentValueForOS(env, "HOMEPATH", goos)
		if strings.TrimSpace(drive) != "" && strings.TrimSpace(homePath) != "" {
			return normalizeProbedGitPath(workspace, filepath.Join(drive+homePath, ".config", "git", "ignore"))
		}
	}
	return ""
}

func environmentValue(env []string, key string) string {
	return environmentValueForOS(env, key, runtime.GOOS)
}

func environmentValueForOS(env []string, key, goos string) string {
	for index := len(env) - 1; index >= 0; index-- {
		candidate, value, ok := strings.Cut(env[index], "=")
		if !ok {
			continue
		}
		if candidate == key || (goos == "windows" && strings.EqualFold(candidate, key)) {
			return value
		}
	}
	return ""
}

func (r *LocalRunner) probePathValue(ctx context.Context, workspace string, allowEmpty bool, args ...string) (string, error) {
	result, err := r.RunLimitedReadOnly(ctx, workspace, readOnlyViewMetadataLimit, append([]string{"--no-pager"}, args...)...)
	if err != nil {
		return "", gitProbeError(args, result, err)
	}
	if result.StdoutTruncated || result.StderrTruncated {
		return "", fmt.Errorf("Git metadata probe output exceeded %d bytes", readOnlyViewMetadataLimit)
	}
	if !strings.HasSuffix(result.Stdout, "\n") {
		return "", fmt.Errorf("Git metadata probe %q returned a malformed path", strings.Join(args, " "))
	}
	value := strings.TrimSuffix(result.Stdout, "\n")
	if runtime.GOOS == "windows" {
		value = strings.TrimSuffix(value, "\r")
	}
	if strings.ContainsRune(value, '\x00') {
		return "", fmt.Errorf("Git metadata probe %q returned a malformed path", strings.Join(args, " "))
	}
	if value == "" && !allowEmpty {
		return "", fmt.Errorf("Git metadata probe %q returned no value", strings.Join(args, " "))
	}
	return value, nil
}

func gitProbeError(args []string, result Result, err error) error {
	detail := strings.TrimSpace(result.Stderr)
	if detail == "" {
		detail = strings.TrimSpace(result.Stdout)
	}
	if detail == "" {
		return fmt.Errorf("Git metadata probe %q: %w", strings.Join(args, " "), err)
	}
	return fmt.Errorf("Git metadata probe %q: %w: %s", strings.Join(args, " "), err, detail)
}

func (r *LocalRunner) safeCoreConfig(ctx context.Context, workspace string) (map[string]string, error) {
	const pattern = `^core\.(filemode|ignorecase|ignorestat|symlinks|autocrlf|eol|safecrlf|checkstat|trustctime|quotepath|precomposeunicode|longpaths)$`
	result, err := r.RunLimitedReadOnly(ctx, workspace, readOnlyViewMetadataLimit,
		"--no-pager", "config", "-z", "--no-includes", "--get-regexp", pattern,
	)
	if err != nil {
		if result.ExitCode == 1 && strings.TrimSpace(result.Stdout) == "" && strings.TrimSpace(result.Stderr) == "" {
			return map[string]string{}, nil
		}
		return nil, gitProbeError([]string{"config", "--get-regexp", pattern}, result, err)
	}
	if result.StdoutTruncated || result.StderrTruncated {
		return nil, fmt.Errorf("safe Git configuration exceeded %d bytes", readOnlyViewMetadataLimit)
	}
	out := make(map[string]string)
	for _, entry := range strings.Split(result.Stdout, "\x00") {
		if entry == "" {
			continue
		}
		key, value, ok := strings.Cut(entry, "\n")
		if !ok {
			return nil, fmt.Errorf("malformed safe Git configuration entry %q", entry)
		}
		value, ok = normalizeSafeCoreConfig(strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(value))
		if !ok {
			return nil, fmt.Errorf("unsupported value for passive Git configuration %s", strings.TrimSpace(key))
		}
		out[strings.ToLower(strings.TrimSpace(key))] = value
	}
	return out, nil
}

func normalizeSafeCoreConfig(key, value string) (string, bool) {
	lower := strings.ToLower(value)
	switch key {
	case "core.filemode", "core.ignorecase", "core.ignorestat", "core.symlinks", "core.trustctime", "core.quotepath", "core.precomposeunicode", "core.longpaths":
		return normalizeGitBool(lower)
	case "core.autocrlf":
		if lower == "input" {
			return lower, true
		}
		return normalizeGitBool(lower)
	case "core.eol":
		if lower == "lf" || lower == "crlf" || lower == "native" {
			return lower, true
		}
	case "core.safecrlf":
		if lower == "warn" {
			return lower, true
		}
		return normalizeGitBool(lower)
	case "core.checkstat":
		if lower == "default" || lower == "minimal" {
			return lower, true
		}
	}
	return "", false
}

func normalizeGitBool(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "yes", "on", "1":
		return "true", true
	case "false", "no", "off", "0":
		return "false", true
	default:
		return "", false
	}
}

func writeReadOnlyViewConfig(path, objectFormat string, coreConfig map[string]string) error {
	if _, ok := coreConfig["core.filemode"]; !ok {
		coreConfig["core.filemode"] = "true"
	}
	keys := make([]string, 0, len(coreConfig))
	for key := range coreConfig {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("[core]\n")
	if objectFormat == "sha256" {
		b.WriteString("\trepositoryformatversion = 1\n")
	} else {
		b.WriteString("\trepositoryformatversion = 0\n")
	}
	b.WriteString("\tbare = false\n")
	for _, key := range keys {
		fmt.Fprintf(&b, "\t%s = %q\n", strings.TrimPrefix(key, "core."), coreConfig[key])
	}
	if objectFormat == "sha256" {
		b.WriteString("[extensions]\n\tobjectFormat = sha256\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write passive Git config: %w", err)
	}
	return nil
}

func writeReadOnlyViewHead(gitDir, branchRef, headOID string) error {
	head := strings.TrimSpace(headOID)
	if branchRef != "" {
		head = "ref: " + branchRef
		branchPath := filepath.FromSlash(strings.TrimPrefix(branchRef, "refs/heads/"))
		refPath := filepath.Join(gitDir, "refs", "heads", branchPath)
		if headOID != "" {
			if err := os.MkdirAll(filepath.Dir(refPath), 0o700); err != nil {
				return fmt.Errorf("create passive Git branch ref: %w", err)
			}
			if err := os.WriteFile(refPath, []byte(headOID+"\n"), 0o600); err != nil {
				return fmt.Errorf("write passive Git branch ref: %w", err)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte(head+"\n"), 0o600); err != nil {
		return fmt.Errorf("write passive Git HEAD: %w", err)
	}
	return nil
}

func validateReadOnlyViewHead(branchRef, headOID, objectFormat string) error {
	if branchRef == "" && headOID == "" {
		return errors.New("Git repository has neither a branch nor HEAD commit")
	}
	if branchRef != "" {
		if !strings.HasPrefix(branchRef, "refs/heads/") {
			return fmt.Errorf("unsupported Git symbolic HEAD %q", branchRef)
		}
		branchPath := filepath.FromSlash(strings.TrimPrefix(branchRef, "refs/heads/"))
		if !filepath.IsLocal(branchPath) {
			return fmt.Errorf("unsafe Git branch ref %q", branchRef)
		}
	}
	if headOID != "" {
		wantLen := 40
		if objectFormat == "sha256" {
			wantLen = 64
		}
		if len(headOID) != wantLen {
			return fmt.Errorf("invalid %s HEAD object id", objectFormat)
		}
		for _, r := range headOID {
			if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
				return fmt.Errorf("invalid %s HEAD object id", objectFormat)
			}
		}
	}
	return nil
}

func readBoundedOptionalFile(path string, maxBytes int64) ([]byte, error) {
	return readBoundedOptionalFileContext(context.Background(), path, maxBytes)
}

func readBoundedOptionalFileWithHook(path string, maxBytes int64, afterFirstRead func()) ([]byte, error) {
	return readBoundedOptionalFileContextWithHooks(context.Background(), path, maxBytes, afterFirstRead, nil)
}

func readBoundedOptionalFileWithHooks(path string, maxBytes int64, afterFirstRead, afterSecondRead func()) ([]byte, error) {
	return readBoundedOptionalFileContextWithHooks(context.Background(), path, maxBytes, afterFirstRead, afterSecondRead)
}

func readBoundedOptionalFileContext(ctx context.Context, path string, maxBytes int64) ([]byte, error) {
	return readBoundedOptionalFileContextWithHooks(ctx, path, maxBytes, nil, nil)
}

func readBoundedOptionalFileContextWithHook(ctx context.Context, path string, maxBytes int64, afterFirstRead func()) ([]byte, error) {
	return readBoundedOptionalFileContextWithHooks(ctx, path, maxBytes, afterFirstRead, nil)
}

func readBoundedOptionalFileContextWithHooks(ctx context.Context, path string, maxBytes int64, afterFirstRead, afterSecondRead func()) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	file, err := openReadOnlyMetadata(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if info.Size() > maxBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, maxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, maxBytes)
	}
	if int64(len(data)) != info.Size() {
		return nil, fmt.Errorf("%s changed while it was read", path)
	}
	if afterFirstRead != nil {
		afterFirstRead()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	secondFile, err := openReadOnlyMetadata(path)
	if err != nil {
		return nil, err
	}
	defer secondFile.Close()
	secondInfo, err := secondFile.Stat()
	if err != nil {
		return nil, err
	}
	if !secondInfo.Mode().IsRegular() || secondInfo.Size() > maxBytes {
		return nil, fmt.Errorf("%s changed while it was read", path)
	}
	second, err := io.ReadAll(io.LimitReader(secondFile, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if afterSecondRead != nil {
		afterSecondRead()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	firstAfter, err := file.Stat()
	if err != nil {
		return nil, err
	}
	secondAfter, err := secondFile.Stat()
	if err != nil {
		return nil, err
	}
	if int64(len(second)) != secondInfo.Size() || !bytes.Equal(data, second) || !os.SameFile(info, firstAfter) ||
		!os.SameFile(info, secondInfo) || !os.SameFile(secondInfo, secondAfter) || firstAfter.Size() != info.Size() ||
		firstAfter.Mode() != info.Mode() || !firstAfter.ModTime().Equal(info.ModTime()) ||
		secondAfter.Size() != secondInfo.Size() || secondAfter.Mode() != secondInfo.Mode() || !secondAfter.ModTime().Equal(secondInfo.ModTime()) {
		return nil, fmt.Errorf("%s changed while it was read", path)
	}
	finalFile, err := openReadOnlyMetadata(path)
	if err != nil {
		return nil, err
	}
	finalInfo, statErr := finalFile.Stat()
	closeErr := finalFile.Close()
	if statErr != nil || closeErr != nil || !finalInfo.Mode().IsRegular() || !os.SameFile(secondInfo, finalInfo) ||
		finalInfo.Size() != secondInfo.Size() || finalInfo.Mode() != secondInfo.Mode() || !finalInfo.ModTime().Equal(secondInfo.ModTime()) {
		return nil, fmt.Errorf("%s changed while it was read", path)
	}
	return data, nil
}

func combineGitExcludeSources(lowerPrecedence, higherPrecedence []byte) []byte {
	if len(lowerPrecedence) == 0 {
		return higherPrecedence
	}
	if len(higherPrecedence) == 0 {
		return lowerPrecedence
	}
	combined := make([]byte, 0, len(lowerPrecedence)+len(higherPrecedence)+1)
	combined = append(combined, lowerPrecedence...)
	if combined[len(combined)-1] != '\n' {
		combined = append(combined, '\n')
	}
	combined = append(combined, higherPrecedence...)
	return combined
}

func normalizeProbedGitPath(workspace, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(workspace, path))
}

func replaceEnvironment(env []string, values map[string]string) []string {
	out := make([]string, 0, len(env)+len(values))
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			if _, replaced := values[key]; replaced {
				continue
			}
		}
		out = append(out, entry)
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		out = append(out, key+"="+values[key])
	}
	return out
}

func appendUniquePaths(paths []string, values ...string) []string {
	seen := make(map[string]struct{}, len(paths)+len(values))
	out := make([]string, 0, len(paths)+len(values))
	for _, path := range append(append([]string(nil), paths...), values...) {
		if strings.TrimSpace(path) == "" {
			continue
		}
		path = filepath.Clean(path)
		if path == "." {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}
