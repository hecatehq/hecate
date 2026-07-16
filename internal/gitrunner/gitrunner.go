package gitrunner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/processrunner"
	"github.com/hecatehq/hecate/internal/sandbox"
)

const (
	command                         = "git"
	passiveGitTimeout               = 5 * time.Second
	patchPathMetadataMinOutputLimit = 4 * 1024
	reverseApplyOutputLimit         = 1024 * 1024
)

var (
	ErrDiffSnapshotTooLarge      = errors.New("git diff exceeds the safe snapshot limit")
	ErrStagedChangesUnsupported  = errors.New("staged git changes are not supported by workspace review")
	ErrStatusSnapshotTooLarge    = errors.New("git status exceeds the safe snapshot limit")
	ErrDiffSnapshotInvalid       = errors.New("git diff snapshot is invalid")
	ErrDiffSnapshotNotApplicable = errors.New("git diff snapshot no longer applies cleanly")
)

type Result = processrunner.Result

type Runner interface {
	Run(ctx context.Context, workspace string, args ...string) (Result, error)
	CurrentRef(ctx context.Context, workspace string) string
	IsWorkTree(ctx context.Context, workspace string) bool
	Worktrees(ctx context.Context, workspace string) ([]Worktree, error)
	Diff(ctx context.Context, workspace string, maxBytes int64) (string, string)
	Clone(ctx context.Context, sourcePath, workspacePath string) (Result, error)
}

type Worktree struct {
	Path     string
	Head     string
	Branch   string
	Detached bool
	Bare     bool
}

// DiffSnapshot contains a bounded, exact raw Git patch, its byte-exact sorted
// path set, and its digest. Diff is intentionally not trimmed: callers that
// need a display projection may trim their copy, while mutation callers retain
// the byte-exact patch covered by Revision. Paths is derived from Diff by Git's
// own NUL-delimited patch parser; it is not independently authoritative.
// Revision is safe to use as a mutation precondition only when SnapshotDiff
// succeeds; truncated patches fail closed.
type DiffSnapshot struct {
	Stat     string
	Diff     string
	Revision string
	Paths    []string
}

type LocalRunner struct {
	Process       processrunner.Runner
	Env           []string
	ReadOnlyPaths []string

	// beforeReverseApply is a package-test seam invoked after the real Git
	// index is reserved and rechecked but before worktree mutation. Production
	// constructors leave it nil.
	beforeReverseApply func()
}

func NewLocalRunner() *LocalRunner {
	return &LocalRunner{Process: processrunner.NewLocalRunner()}
}

func (r *LocalRunner) Run(ctx context.Context, workspace string, args ...string) (Result, error) {
	workspace, err := cleanWorkspace(workspace)
	if err != nil {
		return Result{ExitCode: -1}, err
	}
	return r.run(ctx, processrunner.Request{
		Command: command,
		Args:    args,
		Dir:     workspace,
		Env:     r.env(),
	})
}

func (r *LocalRunner) CurrentRef(ctx context.Context, workspace string) string {
	refCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	result, err := r.Run(refCtx, workspace, "branch", "--show-current")
	if err == nil {
		if branch := strings.TrimSpace(result.Stdout); branch != "" {
			return branch
		}
	}
	result, err = r.Run(refCtx, workspace, "rev-parse", "--short", "HEAD")
	if err != nil {
		return ""
	}
	commit := strings.TrimSpace(result.Stdout)
	if commit == "" {
		return ""
	}
	return "detached@" + commit
}

func (r *LocalRunner) IsWorkTree(ctx context.Context, workspace string) bool {
	result, err := r.Run(ctx, workspace, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(result.Stdout) == "true"
}

func (r *LocalRunner) Worktrees(ctx context.Context, workspace string) ([]Worktree, error) {
	result, err := r.Run(ctx, workspace, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	return parseWorktreeListPorcelain(result.Stdout), nil
}

func parseWorktreeListPorcelain(stdout string) []Worktree {
	var out []Worktree
	var current Worktree
	flush := func() {
		if strings.TrimSpace(current.Path) == "" {
			current = Worktree{}
			return
		}
		current.Path = strings.TrimSpace(current.Path)
		current.Head = strings.TrimSpace(current.Head)
		current.Branch = strings.TrimSpace(current.Branch)
		out = append(out, current)
		current = Worktree{}
	}
	for _, rawLine := range splitWorktreeListPorcelain(stdout) {
		line := strings.TrimRight(rawLine, "\r\n")
		if line == "" {
			flush()
			continue
		}
		key, value, ok := strings.Cut(line, " ")
		if !ok {
			key = line
			value = ""
		}
		switch key {
		case "worktree":
			flush()
			current.Path = strings.TrimSpace(value)
		case "HEAD":
			current.Head = strings.TrimSpace(value)
		case "branch":
			branch := strings.TrimSpace(value)
			branch = strings.TrimPrefix(branch, "refs/heads/")
			current.Branch = branch
		case "detached":
			current.Detached = true
		case "bare":
			current.Bare = true
		}
	}
	flush()
	return out
}

func splitWorktreeListPorcelain(stdout string) []string {
	if strings.Contains(stdout, "\x00") {
		return strings.Split(stdout, "\x00")
	}
	return strings.Split(strings.ReplaceAll(stdout, "\r\n", "\n"), "\n")
}

func (r *LocalRunner) Diff(ctx context.Context, workspace string, maxBytes int64) (string, string) {
	diffCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if !r.IsWorkTree(diffCtx, workspace) {
		return "", ""
	}
	stat, _ := r.RunLimited(diffCtx, workspace, maxBytes, "diff", "--stat")
	diff, _ := r.RunLimited(diffCtx, workspace, maxBytes, "diff", "--no-ext-diff", "--binary")
	return strings.TrimSpace(stat.Stdout), strings.TrimSpace(diff.Stdout)
}

// SnapshotDiff captures the complete scoped tracked working-tree patch up to
// maxBytes and returns a content revision over Git's raw stdout, including its
// final newline. Scoped staged changes fail closed until index-aware discard is
// supported. A truncated display must never become mutation authority because
// edits beyond the retained prefix would otherwise share a revision.
func (r *LocalRunner) SnapshotDiff(ctx context.Context, workspace string, maxBytes int64) (DiffSnapshot, error) {
	if maxBytes <= 0 {
		return DiffSnapshot{}, errors.New("git diff snapshot limit must be positive")
	}
	diffCtx, cancel := context.WithTimeout(ctx, passiveGitTimeout)
	defer cancel()
	view, err := r.NewReadOnlyView(diffCtx, workspace)
	if err != nil {
		return DiffSnapshot{}, fmt.Errorf("create passive Git diff view: %w", err)
	}
	defer view.Close()
	if err := view.RejectContentConversionAttributes(diffCtx); err != nil {
		return DiffSnapshot{}, err
	}
	if err := view.RejectStagedChanges(diffCtx); err != nil {
		return DiffSnapshot{}, err
	}
	stat, _ := view.RunLimited(diffCtx, maxBytes, passiveInspectionArgs(
		"diff", "--relative", "--no-renames", "--stat", "--no-ext-diff", "--no-textconv", "--", ".",
	)...)
	diff, err := view.RunLimited(diffCtx, maxBytes, passiveInspectionArgs(
		"diff", "--relative", "--no-renames", "--no-ext-diff", "--no-textconv", "--binary", "--", ".",
	)...)
	if err != nil {
		return DiffSnapshot{}, fmt.Errorf("capture git diff snapshot: %w", err)
	}
	if diff.StdoutTruncated {
		return DiffSnapshot{}, ErrDiffSnapshotTooLarge
	}
	paths := []string{}
	if diff.Stdout != "" {
		paths, err = view.patchPaths(diffCtx, diff.Stdout)
		if err != nil {
			return DiffSnapshot{}, err
		}
		if len(paths) == 0 {
			return DiffSnapshot{}, fmt.Errorf("%w: non-empty patch has no parsed paths", ErrDiffSnapshotInvalid)
		}
	}
	return DiffSnapshot{
		Stat:     strings.TrimSpace(stat.Stdout),
		Diff:     diff.Stdout,
		Revision: DiffRevision(diff.Stdout),
		Paths:    paths,
	}, nil
}

// StatusPorcelain captures a passive, NUL-delimited porcelain-v1 status for
// exactly Workspace. Returned paths are normalized relative to Workspace even
// when it is nested inside a larger Git worktree.
func (r *LocalRunner) StatusPorcelain(ctx context.Context, workspace string, maxBytes int64) (string, error) {
	if maxBytes <= 0 {
		return "", errors.New("git status snapshot limit must be positive")
	}
	statusCtx, cancel := context.WithTimeout(ctx, passiveGitTimeout)
	defer cancel()
	view, err := r.NewReadOnlyView(statusCtx, workspace)
	if err != nil {
		return "", fmt.Errorf("create passive Git status view: %w", err)
	}
	defer view.Close()
	if err := view.RejectContentConversionAttributes(statusCtx); err != nil {
		return "", err
	}
	result, err := view.RunLimited(statusCtx, maxBytes, passiveInspectionArgs(
		"status", "--porcelain=v1", "-z", "--untracked-files=all", "--no-renames", "--", ".",
	)...)
	if err != nil {
		return "", fmt.Errorf("capture git status snapshot: %w", err)
	}
	if result.StdoutTruncated {
		return "", ErrStatusSnapshotTooLarge
	}
	return scopeStatusPorcelain(result.Stdout, view.WorkspacePrefix())
}

// DiffRevision returns a self-describing digest suitable for typed API
// preconditions. The empty patch therefore has one deterministic revision.
func DiffRevision(rawDiff string) string {
	sum := sha256.Sum256([]byte(rawDiff))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ReverseApplySnapshot conditionally removes selected changes from the exact
// patch covered by snapshot.Revision. Git applies the reverse hunks directly
// to the current worktree: overlapping edits made after the snapshot cause the
// whole operation to fail, while unrelated and non-overlapping edits survive.
func (r *LocalRunner) ReverseApplySnapshot(ctx context.Context, workspace string, snapshot DiffSnapshot, paths []string) (result Result, returnErr error) {
	if strings.TrimSpace(snapshot.Revision) == "" || snapshot.Revision != DiffRevision(snapshot.Diff) {
		return Result{ExitCode: -1}, ErrDiffSnapshotInvalid
	}
	if snapshot.Diff == "" {
		return Result{ExitCode: -1}, fmt.Errorf("%w: patch is empty", ErrDiffSnapshotInvalid)
	}
	cleanedPaths, err := reverseApplyPaths(paths)
	if err != nil {
		return Result{ExitCode: -1}, err
	}
	view, err := r.NewReadOnlyView(ctx, workspace)
	if err != nil {
		return Result{ExitCode: -1}, fmt.Errorf("create conditional Git apply view: %w", err)
	}
	defer view.Close()
	patchPaths, err := view.patchPaths(ctx, snapshot.Diff)
	if err != nil {
		return Result{ExitCode: -1}, err
	}
	if snapshot.Paths != nil && !equalExactPaths(snapshot.Paths, patchPaths) {
		return Result{ExitCode: -1}, fmt.Errorf("%w: snapshot paths do not match the reviewed patch", ErrDiffSnapshotInvalid)
	}
	patchPathSet := make(map[string]struct{}, len(patchPaths))
	for _, path := range patchPaths {
		patchPathSet[path] = struct{}{}
	}
	for _, path := range cleanedPaths {
		if _, ok := patchPathSet[path]; !ok {
			return Result{ExitCode: -1}, fmt.Errorf("%w: selected path %q is absent from the reviewed patch", ErrDiffSnapshotInvalid, path)
		}
	}

	indexLease, err := view.lockIndexForMutation(ctx)
	if err != nil {
		return Result{ExitCode: -1}, err
	}
	defer func() {
		if releaseErr := indexLease.Release(); releaseErr != nil {
			cleanupErr := fmt.Errorf("release Git index reservation: %w", releaseErr)
			if returnErr == nil {
				returnErr = cleanupErr
			} else {
				returnErr = errors.Join(returnErr, cleanupErr)
			}
		}
	}()
	if err := view.RejectStagedChanges(ctx); err != nil {
		if errors.Is(err, ErrStagedChangesUnsupported) {
			return Result{ExitCode: -1}, fmt.Errorf("%w: %w", ErrDiffSnapshotNotApplicable, err)
		}
		return Result{ExitCode: -1}, fmt.Errorf("verify Git index before conditional apply: %w", err)
	}
	// RejectStagedChanges proves that this view's HEAD and the live index still
	// agree, but a complete add+commit can happen before the view is created and
	// leave both clean at a different baseline. Check that the reviewed forward
	// patch still applies to the live index while its conventional lock is held.
	// The actual reverse operation remains worktree-only.
	if err := view.validateIndexBaseline(ctx, snapshot.Diff); err != nil {
		return Result{ExitCode: -1}, err
	}
	if r.beforeReverseApply != nil {
		r.beforeReverseApply()
	}

	args := passiveInspectionArgs("apply", "--reverse", "--whitespace=nowarn")
	includePrefix := ""
	if prefix := filepath.ToSlash(view.WorkspacePrefix()); prefix != "." && prefix != "" {
		args = append(args, "--directory="+prefix)
		includePrefix = strings.TrimSuffix(prefix, "/") + "/"
	}
	for _, path := range cleanedPaths {
		args = append(args, "--include="+escapeGitApplyPattern(includePrefix+path))
	}
	args = append(args, "-")
	result, err = view.runWorkTreeInput(ctx, reverseApplyOutputLimit, snapshot.Diff, args...)
	if err != nil {
		return result, fmt.Errorf("%w: %w", ErrDiffSnapshotNotApplicable, err)
	}
	return result, nil
}

// validateIndexBaseline proves that the old side of the reviewed worktree
// patch still matches the live index. Callers must hold index.lock so a
// cooperating Git writer cannot change that baseline between this check and
// the worktree mutation.
func (v *ReadOnlyView) validateIndexBaseline(ctx context.Context, patch string) error {
	if v == nil || v.runner == nil {
		return errors.New("passive Git metadata view is not configured")
	}
	args := passiveInspectionArgs("apply", "--cached", "--check", "--whitespace=nowarn")
	if prefix := filepath.ToSlash(v.WorkspacePrefix()); prefix != "." && prefix != "" {
		args = append(args, "--directory="+prefix)
	}
	args = append(args, "-")
	result, err := v.runWorkTreeInput(ctx, reverseApplyOutputLimit, patch, args...)
	if err != nil {
		return fmt.Errorf("%w: reviewed Git index baseline changed: %w", ErrDiffSnapshotNotApplicable, err)
	}
	if result.Stdout != "" || result.Stderr != "" || result.StdoutTruncated || result.StderrTruncated {
		return fmt.Errorf("%w: Git index baseline check emitted unexpected diagnostics", ErrDiffSnapshotNotApplicable)
	}
	return nil
}

func equalExactPaths(first, second []string) bool {
	if len(first) != len(second) {
		return false
	}
	for i := range first {
		if first[i] != second[i] {
			return false
		}
	}
	return true
}

// patchPaths asks Git to parse the exact patch and returns its byte-exact,
// workspace-relative paths. --numstat disables application, while -z keeps
// whitespace, newlines, and other legal filename bytes unambiguous. This
// prevents git apply's successful no-op behavior when an --include pattern
// matches no reviewed path.
func (v *ReadOnlyView) patchPaths(ctx context.Context, patch string) ([]string, error) {
	if v == nil || v.runner == nil {
		return nil, fmt.Errorf("%w: passive Git metadata view is not configured", ErrDiffSnapshotInvalid)
	}
	// Every path appears at least twice in a no-renames patch header, so a
	// NUL-delimited numstat projection cannot exceed the patch that produced it.
	// Keep a small floor for bounded diagnostics from malformed input.
	outputLimit := int64(len(patch))
	if outputLimit < patchPathMetadataMinOutputLimit {
		outputLimit = patchPathMetadataMinOutputLimit
	}
	result, err := v.runner.RunLimitedReadOnlyInput(ctx, v.workTree, outputLimit, patch, passiveInspectionArgs(
		"apply", "--numstat", "-z", "--whitespace=nowarn", "-",
	)...)
	if err != nil {
		return nil, fmt.Errorf("%w: parse reviewed patch paths: %w", ErrDiffSnapshotInvalid, err)
	}
	if result.StdoutTruncated || result.StderrTruncated {
		return nil, fmt.Errorf("%w: reviewed patch path metadata exceeded %d bytes", ErrDiffSnapshotInvalid, outputLimit)
	}
	if result.Stderr != "" {
		return nil, fmt.Errorf("%w: reviewed patch path parser emitted unexpected diagnostics", ErrDiffSnapshotInvalid)
	}
	if result.Stdout != "" && !strings.HasSuffix(result.Stdout, "\x00") {
		return nil, fmt.Errorf("%w: malformed reviewed patch path metadata", ErrDiffSnapshotInvalid)
	}
	pathSet := make(map[string]struct{})
	for _, record := range strings.Split(strings.TrimSuffix(result.Stdout, "\x00"), "\x00") {
		if record == "" {
			continue
		}
		_, remainder, ok := strings.Cut(record, "\t")
		if !ok {
			return nil, fmt.Errorf("%w: malformed reviewed patch path metadata", ErrDiffSnapshotInvalid)
		}
		_, path, ok := strings.Cut(remainder, "\t")
		if !ok || path == "" || strings.ContainsRune(path, '\x00') {
			return nil, fmt.Errorf("%w: malformed reviewed patch path metadata", ErrDiffSnapshotInvalid)
		}
		pathSet[path] = struct{}{}
	}
	paths := make([]string, 0, len(pathSet))
	for path := range pathSet {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

func passiveInspectionArgs(args ...string) []string {
	prefix := []string{
		"--no-pager",
		"-c", "core.fsmonitor=false",
		"-c", "core.untrackedCache=false",
		"-c", "core.attributesFile=" + os.DevNull,
		"-c", "submodule.recurse=false",
		"-c", "fetch.recurseSubmodules=false",
	}
	return append(prefix, args...)
}

func (v *ReadOnlyView) runWorkTreeInput(ctx context.Context, maxBytes int64, stdin string, args ...string) (Result, error) {
	if v == nil || v.runner == nil {
		return Result{ExitCode: -1}, errors.New("passive Git metadata view is not configured")
	}
	return v.runner.run(ctx, processrunner.Request{
		Command:        command,
		Args:           args,
		Dir:            v.workTree,
		Env:            v.runner.env(),
		Stdin:          stdin,
		MaxStdoutBytes: maxBytes,
		MaxStderrBytes: maxBytes,
	})
}

func reverseApplyPaths(paths []string) ([]string, error) {
	cleaned := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, candidate := range paths {
		if candidate == "" || strings.ContainsRune(candidate, '\x00') {
			return nil, fmt.Errorf("%w: workspace-relative path must be non-empty and NUL-free", ErrDiffSnapshotInvalid)
		}
		localPath := filepath.FromSlash(candidate)
		if !filepath.IsLocal(localPath) || filepath.ToSlash(filepath.Clean(localPath)) != candidate {
			return nil, fmt.Errorf("%w: unsafe workspace-relative path %q", ErrDiffSnapshotInvalid, candidate)
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		cleaned = append(cleaned, candidate)
	}
	if len(cleaned) == 0 {
		return nil, fmt.Errorf("%w: at least one path is required", ErrDiffSnapshotInvalid)
	}
	return cleaned, nil
}

func escapeGitApplyPattern(path string) string {
	var b strings.Builder
	b.Grow(len(path))
	for i := 0; i < len(path); i++ {
		char := path[i]
		switch char {
		case '\\', '*', '?', '[':
			b.WriteByte('\\')
		}
		b.WriteByte(char)
	}
	return b.String()
}

func scopeStatusPorcelain(raw, workspacePrefix string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if !strings.HasSuffix(raw, "\x00") {
		return "", errors.New("capture git status snapshot: malformed porcelain output")
	}
	records := strings.Split(raw, "\x00")
	var out strings.Builder
	for i := 0; i < len(records)-1; i++ {
		record := records[i]
		if len(record) < 4 || record[2] != ' ' {
			return "", errors.New("capture git status snapshot: malformed porcelain record")
		}
		relative, err := statusPathRelativeToWorkspace(record[3:], workspacePrefix)
		if err != nil {
			return "", err
		}
		out.WriteString(record[:3])
		out.WriteString(relative)
		out.WriteByte(0)
		if strings.ContainsAny(record[:2], "RC") {
			i++
			if i >= len(records)-1 {
				return "", errors.New("capture git status snapshot: malformed rename record")
			}
			relative, err = statusPathRelativeToWorkspace(records[i], workspacePrefix)
			if err != nil {
				return "", err
			}
			out.WriteString(relative)
			out.WriteByte(0)
		}
	}
	return out.String(), nil
}

func statusPathRelativeToWorkspace(path, workspacePrefix string) (string, error) {
	prefix := filepath.ToSlash(workspacePrefix)
	if prefix == "" || prefix == "." {
		return path, nil
	}
	prefix = strings.TrimSuffix(prefix, "/") + "/"
	if !strings.HasPrefix(path, prefix) || len(path) == len(prefix) {
		return "", fmt.Errorf("capture git status snapshot: path %q is outside the workspace", path)
	}
	return strings.TrimPrefix(path, prefix), nil
}

func (r *LocalRunner) Clone(ctx context.Context, sourcePath, workspacePath string) (Result, error) {
	sourcePath = strings.TrimSpace(sourcePath)
	workspacePath = strings.TrimSpace(workspacePath)
	if sourcePath == "" {
		return Result{ExitCode: -1}, errors.New("git clone source path is required")
	}
	if workspacePath == "" {
		return Result{ExitCode: -1}, errors.New("git clone workspace path is required")
	}
	workspacePath = filepath.Clean(workspacePath)
	if abs, err := filepath.Abs(workspacePath); err == nil {
		workspacePath = abs
	}
	if err := os.MkdirAll(filepath.Dir(workspacePath), 0o755); err != nil {
		return Result{ExitCode: -1}, err
	}
	return r.run(ctx, processrunner.Request{
		Command: command,
		Args:    []string{"clone", "--quiet", "--no-hardlinks", "--", sourcePath, workspacePath},
		Env:     r.env(),
	})
}

func (r *LocalRunner) RunLimited(ctx context.Context, workspace string, maxBytes int64, args ...string) (Result, error) {
	workspace, err := cleanWorkspace(workspace)
	if err != nil {
		return Result{ExitCode: -1}, err
	}
	return r.run(ctx, processrunner.Request{
		Command:        command,
		Args:           args,
		Dir:            workspace,
		Env:            r.env(),
		MaxStdoutBytes: maxBytes,
		MaxStderrBytes: maxBytes,
	})
}

// RunLimitedReadOnly executes a fixed Git invocation with OS-level network
// isolation and, under bwrap, a read-only host filesystem. Callers must still
// disable Git features such as fsmonitor and optional index locks because the
// wrapper is best-effort on platforms where no kernel sandbox is available.
func (r *LocalRunner) RunLimitedReadOnly(ctx context.Context, workspace string, maxBytes int64, args ...string) (Result, error) {
	return r.runLimitedReadOnly(ctx, workspace, maxBytes, "", args...)
}

// RunLimitedReadOnlyInput is RunLimitedReadOnly with caller-provided standard
// input. Callers must cap that input; this avoids platform command-line limits
// for fixed Git commands such as check-attr.
func (r *LocalRunner) RunLimitedReadOnlyInput(ctx context.Context, workspace string, maxBytes int64, stdin string, args ...string) (Result, error) {
	return r.runLimitedReadOnly(ctx, workspace, maxBytes, stdin, args...)
}

func (r *LocalRunner) runLimitedReadOnly(ctx context.Context, workspace string, maxBytes int64, stdin string, args ...string) (Result, error) {
	workspace, err := cleanWorkspace(workspace)
	if err != nil {
		return Result{ExitCode: -1}, err
	}
	argv := sandbox.WrapReadOnlyArgv(append([]string{command}, args...), workspace, false, r.ReadOnlyPaths...)
	return r.run(ctx, processrunner.Request{
		Command:        argv[0],
		Args:           argv[1:],
		Dir:            workspace,
		Env:            r.env(),
		Stdin:          stdin,
		MaxStdoutBytes: maxBytes,
		MaxStderrBytes: maxBytes,
	})
}

func (r *LocalRunner) run(ctx context.Context, req processrunner.Request) (Result, error) {
	process := r.Process
	if process == nil {
		process = processrunner.NewLocalRunner()
	}
	return process.Run(ctx, req)
}

func (r *LocalRunner) env() []string {
	if r.Env != nil {
		return r.Env
	}
	return SanitizedEnv(os.Environ())
}

func cleanWorkspace(workspace string) (string, error) {
	if strings.TrimSpace(workspace) == "" {
		return "", errors.New("workspace is required")
	}
	workspace = filepath.Clean(workspace)
	if abs, err := filepath.Abs(workspace); err == nil {
		workspace = abs
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return "", fmt.Errorf("workspace %q is not accessible: %w", workspace, err)
	}
	defer root.Close()
	info, err := root.Stat(".")
	if err != nil {
		return "", fmt.Errorf("workspace %q is not accessible: %w", workspace, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace %q is not a directory", workspace)
	}
	return workspace, nil
}

func SanitizedEnv(env []string) []string {
	allowedPrefixes := []string{
		"PATH=",
		"HOME=",
		"TMPDIR=",
		"TEMP=",
		"TMP=",
		"LANG=",
		"LC_",
		"TERM=",
		"USER=",
		"LOGNAME=",
		"XDG_",
		"VOLTA_",
	}
	out := make([]string, 0, len(env))
	for _, entry := range env {
		for _, prefix := range allowedPrefixes {
			if strings.HasPrefix(entry, prefix) {
				out = append(out, entry)
				break
			}
		}
	}
	return out
}
