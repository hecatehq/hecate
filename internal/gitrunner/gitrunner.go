package gitrunner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hecatehq/hecate/internal/localfs"
	"github.com/hecatehq/hecate/internal/processrunner"
	"github.com/hecatehq/hecate/internal/sandbox"
	"github.com/hecatehq/hecate/internal/workspacefs"
)

const (
	command                         = "git"
	passiveGitTimeout               = 15 * time.Second
	passiveReviewTimeout            = 30 * time.Second
	patchPathMetadataMinOutputLimit = 4 * 1024
	reverseApplyOutputLimit         = 1024 * 1024
	trackedPathTopologyMaxEntries   = 5000
)

var (
	ErrDiffSnapshotTooLarge        = errors.New("git diff exceeds the safe snapshot limit")
	ErrReviewSnapshotTooLarge      = errors.New("git workspace review exceeds the safe snapshot limit")
	ErrReviewSnapshotChanged       = errors.New("git workspace changed while its review snapshot was captured")
	ErrReviewSnapshotInvalid       = errors.New("git workspace review snapshot is invalid")
	ErrDiffSnapshotApplied         = errors.New("git diff snapshot was applied but cleanup did not complete")
	ErrDiffSnapshotCleanupFailed   = errors.New("git diff snapshot cleanup did not complete")
	ErrDiffSnapshotOutcomeUnknown  = errors.New("git diff snapshot mutation outcome is unknown")
	ErrIndexVisibilityUnsupported  = errors.New("git index visibility flags are unsupported by workspace review and discard")
	ErrSubmoduleChangesUnsupported = errors.New("git submodule changes are unsupported by workspace discard")
	ErrTrackedPathTopologyUnsafe   = errors.New("tracked workspace path topology is unsafe for discard")
	ErrStagedChangesUnsupported    = errors.New("staged git changes are not supported by workspace discard")
	ErrStatusSnapshotTooLarge      = errors.New("git status exceeds the safe snapshot limit")
	ErrDiffSnapshotInvalid         = errors.New("git diff snapshot is invalid")
	ErrDiffSnapshotNotApplicable   = errors.New("git diff snapshot no longer applies cleanly")
	errInspectionMetadataTooLarge  = errors.New("git inspection metadata exceeds its safe limit")
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
// path set, and its digests. Diff is intentionally not trimmed: callers that
// need a display projection may trim their copy, while mutation callers retain
// the byte-exact patch covered by DiscardRevision. Paths is derived from Diff
// by Git's own NUL-delimited patch parser; it is not independently authoritative.
// Revision is display-only legacy evidence. DiscardRevision additionally binds
// the patch to the reviewed workspace-root identity and is the only token that
// callers may use as a mutation precondition; truncated patches fail closed.
type DiffSnapshot struct {
	Stat            string
	Diff            string
	Revision        string
	DiscardRevision string
	Paths           []string

	workspaceRoot *workspaceRootIdentity
}

// ReviewLayerSnapshot is one bounded tracked-change layer captured for operator
// review. Complete means its inventory/display projection was captured without
// truncation; Exact separately records whether its patch is byte-exact (binary
// metadata can be complete but non-exact). Unlike DiffSnapshot it is read-only
// evidence and must never be accepted as mutation authority.
type ReviewLayerSnapshot struct {
	Stat             string
	Diff             string
	Paths            []string
	Complete         bool
	Exact            bool
	IncompleteReason string
}

// ReviewSnapshot describes the staged (HEAD to index), unstaged (index to
// worktree), and untracked path layers visible in a workspace. It deliberately
// carries no mutation precondition: only DiffSnapshot can authorize discard.
type ReviewSnapshot struct {
	Staged    ReviewLayerSnapshot
	Unstaged  ReviewLayerSnapshot
	Untracked []ReviewUntrackedEntry
	Hidden    []IndexVisibilityEntry
	Status    []ReviewStatusEntry
	Complete  bool

	workspaceRoot *workspaceRootIdentity
}

type ReviewUntrackedEntry struct {
	Path string
	Kind string
}

// IndexVisibilityEntry identifies a tracked path whose index flags can hide a
// worktree edit from ordinary status and diff output.
type IndexVisibilityEntry struct {
	Path string
	Kind string
}

// ReviewStatusEntry retains Git's two-layer porcelain status for one scoped
// path. Status bytes use Git porcelain-v1 semantics and are never trimmed.
type ReviewStatusEntry struct {
	Path           string
	IndexStatus    byte
	WorktreeStatus byte
	UntrackedKind  string
	Conflict       bool
}

type LocalRunner struct {
	Process       processrunner.Runner
	Env           []string
	ReadOnlyPaths []string

	// beforeReverseApply is a package-test seam invoked after the real Git
	// index is reserved and rechecked but before worktree mutation. Production
	// constructors leave it nil.
	beforeReverseApply func()
	// releaseIndexMutationLease is a package-test seam for failures after a
	// successful worktree mutation. Production constructors leave it nil.
	releaseIndexMutationLease func(*indexMutationLease) error
	// applyReversePatch is a package-test seam for an execution failure after
	// Git's non-mutating preflight succeeds. Production constructors leave it nil.
	applyReversePatch func(context.Context, *ReadOnlyView, string, []string) (Result, error)
	// beforeReviewVerification is a package-test seam invoked between the
	// first and verifying tracked-layer captures. Production leaves it nil.
	beforeReviewVerification func()
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
	if err := localfs.EnsureBoundedPath(workspace); err != nil {
		return false
	}
	probeCtx, cancel := context.WithTimeout(ctx, passiveGitTimeout)
	defer cancel()
	result, err := r.RunLimitedReadOnly(probeCtx, workspace, patchPathMetadataMinOutputLimit, passiveInspectionArgs(
		"rev-parse", "--is-inside-work-tree",
	)...)
	return err == nil && !result.StdoutTruncated && !result.StderrTruncated && result.Stderr == "" && strings.TrimSpace(result.Stdout) == "true"
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
		if errors.Is(err, ErrReviewSnapshotChanged) {
			return DiffSnapshot{}, fmt.Errorf("%w: workspace identity changed during snapshot", ErrDiffSnapshotNotApplicable)
		}
		return DiffSnapshot{}, fmt.Errorf("create passive Git diff view: %w", err)
	}
	defer view.Close()
	if err := view.RejectContentConversionAttributes(diffCtx); err != nil {
		if errors.Is(err, errInspectionMetadataTooLarge) {
			return DiffSnapshot{}, ErrDiffSnapshotTooLarge
		}
		return DiffSnapshot{}, err
	}
	if hidden, err := view.IndexVisibilityEntries(diffCtx); err != nil {
		if errors.Is(err, errInspectionMetadataTooLarge) {
			return DiffSnapshot{}, ErrDiffSnapshotTooLarge
		}
		return DiffSnapshot{}, err
	} else if len(hidden) > 0 {
		return DiffSnapshot{}, ErrIndexVisibilityUnsupported
	}
	if err := view.RejectStagedChanges(diffCtx); err != nil {
		return DiffSnapshot{}, err
	}
	if err := view.RejectSubmoduleChanges(diffCtx); err != nil {
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
	for _, path := range paths {
		if err := validateReviewPath(path); err != nil {
			return DiffSnapshot{}, fmt.Errorf("%w: %v", ErrDiffSnapshotInvalid, err)
		}
	}
	if err := validateTrackedPathTopology(diffCtx, workspace, paths); err != nil {
		return DiffSnapshot{}, err
	}
	if !view.workspaceIdentityMatches(view.workspaceRoot) {
		return DiffSnapshot{}, fmt.Errorf("%w: workspace identity changed during snapshot", ErrDiffSnapshotNotApplicable)
	}
	discardRevision, err := diffDiscardRevision(view.workspaceRoot, diff.Stdout)
	if err != nil {
		return DiffSnapshot{}, err
	}
	return DiffSnapshot{
		Stat:            strings.TrimSpace(stat.Stdout),
		Diff:            diff.Stdout,
		Revision:        DiffRevision(diff.Stdout),
		DiscardRevision: discardRevision,
		Paths:           paths,
		workspaceRoot:   view.workspaceRoot,
	}, nil
}

// SnapshotReview captures all three Git workspace-review layers without
// granting mutation authority to any of them. Each tracked patch and the
// NUL-delimited status inventory must fit maxBytes. The path sets derived from
// the exact patches must agree with the final status observation; otherwise a
// concurrent workspace transition fails this read rather than rendering a
// falsely complete review.
func (r *LocalRunner) SnapshotReview(ctx context.Context, workspace string, maxBytes int64) (ReviewSnapshot, error) {
	if maxBytes <= 0 {
		return ReviewSnapshot{}, errors.New("git workspace review limit must be positive")
	}
	reviewCtx, cancel := context.WithTimeout(ctx, passiveReviewTimeout)
	defer cancel()
	view, err := r.NewReadOnlyView(reviewCtx, workspace)
	if err != nil {
		return ReviewSnapshot{}, fmt.Errorf("create passive Git review view: %w", err)
	}
	defer view.Close()
	if err := view.RejectContentConversionAttributes(reviewCtx); err != nil {
		if errors.Is(err, errInspectionMetadataTooLarge) {
			return ReviewSnapshot{}, ErrReviewSnapshotTooLarge
		}
		return ReviewSnapshot{}, err
	}
	hiddenBefore, err := view.IndexVisibilityEntries(reviewCtx)
	if err != nil {
		if errors.Is(err, errInspectionMetadataTooLarge) {
			return ReviewSnapshot{}, ErrReviewSnapshotTooLarge
		}
		return ReviewSnapshot{}, err
	}
	statusBefore, err := view.captureReviewStatus(reviewCtx, maxBytes)
	if err != nil {
		return ReviewSnapshot{}, err
	}
	parsedBefore, err := parseReviewStatusPaths(statusBefore)
	if err != nil {
		return ReviewSnapshot{}, err
	}
	staged := ReviewLayerSnapshot{Paths: append([]string(nil), parsedBefore.staged...), IncompleteReason: "unmerged_state"}
	unstaged := ReviewLayerSnapshot{Paths: append([]string(nil), parsedBefore.unstaged...), IncompleteReason: "unmerged_state"}
	if len(parsedBefore.conflicts) == 0 {
		staged, err = view.captureReviewLayer(reviewCtx, maxBytes, true)
		if err != nil {
			return ReviewSnapshot{}, err
		}
		remainingPatchBytes := maxBytes - int64(len(staged.Diff))
		if !staged.Complete {
			remainingPatchBytes = 0
		}
		unstaged, err = view.captureReviewLayer(reviewCtx, remainingPatchBytes, false)
		if err != nil {
			return ReviewSnapshot{}, err
		}
	}
	statusAfter, err := view.captureReviewStatus(reviewCtx, maxBytes)
	if err != nil {
		return ReviewSnapshot{}, err
	}
	if statusBefore != statusAfter {
		return ReviewSnapshot{}, ErrReviewSnapshotChanged
	}
	// Porcelain status records carry path and state bytes, not content. Verify
	// every complete tracked layer a second time so an index/worktree rewrite
	// that preserves the same XY status cannot produce a mixed-generation
	// review marked complete.
	if len(parsedBefore.conflicts) == 0 && staged.Complete && unstaged.Complete {
		if r.beforeReviewVerification != nil {
			r.beforeReviewVerification()
		}
		verifiedStaged, verifyErr := view.captureReviewLayer(reviewCtx, maxBytes, true)
		if verifyErr != nil {
			return ReviewSnapshot{}, verifyErr
		}
		remainingPatchBytes := maxBytes - int64(len(verifiedStaged.Diff))
		if !verifiedStaged.Complete {
			remainingPatchBytes = 0
		}
		verifiedUnstaged, verifyErr := view.captureReviewLayer(reviewCtx, remainingPatchBytes, false)
		if verifyErr != nil {
			return ReviewSnapshot{}, verifyErr
		}
		statusVerified, verifyErr := view.captureReviewStatus(reviewCtx, maxBytes)
		if verifyErr != nil {
			return ReviewSnapshot{}, verifyErr
		}
		if statusAfter != statusVerified || !equalReviewLayerSnapshot(staged, verifiedStaged) || !equalReviewLayerSnapshot(unstaged, verifiedUnstaged) {
			return ReviewSnapshot{}, ErrReviewSnapshotChanged
		}
		staged = verifiedStaged
		unstaged = verifiedUnstaged
		statusAfter = statusVerified
	}
	statusPaths, err := parseReviewStatusPaths(statusAfter)
	if err != nil {
		return ReviewSnapshot{}, err
	}
	if staged.Complete && !equalExactPaths(staged.Paths, statusPaths.staged) {
		return ReviewSnapshot{}, ErrReviewSnapshotChanged
	}
	if unstaged.Complete && !equalExactPaths(unstaged.Paths, statusPaths.unstaged) {
		return ReviewSnapshot{}, ErrReviewSnapshotChanged
	}
	if !staged.Complete {
		staged.Paths = append([]string(nil), statusPaths.staged...)
		staged.Complete = len(staged.Paths) == 0
		if staged.Complete {
			staged.IncompleteReason = ""
		}
	}
	if !unstaged.Complete {
		unstaged.Paths = append([]string(nil), statusPaths.unstaged...)
		unstaged.Complete = len(unstaged.Paths) == 0
		if unstaged.Complete {
			unstaged.IncompleteReason = ""
		}
	}
	hidden, err := view.IndexVisibilityEntries(reviewCtx)
	if err != nil {
		if errors.Is(err, errInspectionMetadataTooLarge) {
			return ReviewSnapshot{}, ErrReviewSnapshotTooLarge
		}
		return ReviewSnapshot{}, err
	}
	if !equalIndexVisibilityEntries(hiddenBefore, hidden) {
		return ReviewSnapshot{}, ErrReviewSnapshotChanged
	}
	for _, path := range statusPaths.intentToAdd {
		hidden = append(hidden, IndexVisibilityEntry{Path: path, Kind: "intent_to_add"})
	}
	for _, path := range statusPaths.conflicts {
		hidden = append(hidden, IndexVisibilityEntry{Path: path, Kind: "unmerged"})
	}
	sort.Slice(hidden, func(i, j int) bool {
		if hidden[i].Path == hidden[j].Path {
			return hidden[i].Kind < hidden[j].Kind
		}
		return hidden[i].Path < hidden[j].Path
	})

	snapshot := ReviewSnapshot{
		Staged:        staged,
		Unstaged:      unstaged,
		Untracked:     statusPaths.untracked,
		Hidden:        hidden,
		Status:        statusPaths.entries,
		Complete:      staged.Complete && unstaged.Complete && len(hidden) == 0,
		workspaceRoot: view.workspaceRoot,
	}
	if !view.workspaceIdentityMatches(view.workspaceRoot) {
		return ReviewSnapshot{}, ErrReviewSnapshotChanged
	}
	return snapshot, nil
}

// SnapshotReviewFile captures one tracked working-tree entry without charging
// unrelated files against its bounded patch budget. It grants no mutation
// authority and deliberately ignores the staged layer.
func (r *LocalRunner) SnapshotReviewFile(ctx context.Context, workspace, path string, maxBytes int64) (ReviewLayerSnapshot, ReviewStatusEntry, bool, error) {
	if maxBytes <= 0 {
		return ReviewLayerSnapshot{}, ReviewStatusEntry{}, false, errors.New("git workspace file review limit must be positive")
	}
	if err := validateReviewPath(path); err != nil {
		return ReviewLayerSnapshot{}, ReviewStatusEntry{}, false, err
	}
	reviewCtx, cancel := context.WithTimeout(ctx, passiveReviewTimeout)
	defer cancel()
	view, err := r.NewReadOnlyView(reviewCtx, workspace)
	if err != nil {
		return ReviewLayerSnapshot{}, ReviewStatusEntry{}, false, fmt.Errorf("create passive Git file-review view: %w", err)
	}
	defer view.Close()
	if err := view.RejectContentConversionAttributesForPath(reviewCtx, path); err != nil {
		if errors.Is(err, errInspectionMetadataTooLarge) {
			return ReviewLayerSnapshot{}, ReviewStatusEntry{}, false, ErrReviewSnapshotTooLarge
		}
		return ReviewLayerSnapshot{}, ReviewStatusEntry{}, false, err
	}
	statusBefore, err := view.captureReviewStatusForPath(reviewCtx, maxBytes, path)
	if err != nil {
		return ReviewLayerSnapshot{}, ReviewStatusEntry{}, false, err
	}
	parsed, err := parseReviewStatusPaths(statusBefore)
	if err != nil {
		return ReviewLayerSnapshot{}, ReviewStatusEntry{}, false, err
	}
	var status ReviewStatusEntry
	found := false
	for _, candidate := range parsed.entries {
		if candidate.Path == path && candidate.UntrackedKind == "" && (candidate.WorktreeStatus != ' ' || candidate.Conflict) {
			status = candidate
			found = true
			break
		}
	}
	if !found {
		return ReviewLayerSnapshot{}, ReviewStatusEntry{}, false, nil
	}
	if status.Conflict {
		return ReviewLayerSnapshot{Paths: []string{path}, Complete: false, IncompleteReason: "unmerged_state"}, status, true, nil
	}
	first, err := view.captureReviewLayerForPath(reviewCtx, maxBytes, path)
	if err != nil {
		return ReviewLayerSnapshot{}, ReviewStatusEntry{}, false, err
	}
	second, err := view.captureReviewLayerForPath(reviewCtx, maxBytes, path)
	if err != nil {
		return ReviewLayerSnapshot{}, ReviewStatusEntry{}, false, err
	}
	statusAfter, err := view.captureReviewStatusForPath(reviewCtx, maxBytes, path)
	if err != nil {
		return ReviewLayerSnapshot{}, ReviewStatusEntry{}, false, err
	}
	if statusBefore != statusAfter || !equalReviewLayerSnapshot(first, second) || !view.workspaceIdentityMatches(view.workspaceRoot) {
		return ReviewLayerSnapshot{}, ReviewStatusEntry{}, false, ErrReviewSnapshotChanged
	}
	if second.Complete && !equalExactPaths(second.Paths, []string{path}) {
		return ReviewLayerSnapshot{}, ReviewStatusEntry{}, false, ErrReviewSnapshotChanged
	}
	if !second.Complete {
		second.Paths = []string{path}
	}
	return second, status, true, nil
}

func equalReviewLayerSnapshot(first, second ReviewLayerSnapshot) bool {
	return first.Stat == second.Stat && first.Diff == second.Diff && first.Complete == second.Complete && first.Exact == second.Exact &&
		first.IncompleteReason == second.IncompleteReason && slices.Equal(first.Paths, second.Paths)
}

func (v *ReadOnlyView) captureReviewStatus(ctx context.Context, maxBytes int64) (string, error) {
	statusResult, err := v.RunLimited(ctx, maxBytes, passiveInspectionArgs(
		"status", "--porcelain=v1", "-z", "--untracked-files=all", "--no-renames", "--", ".",
	)...)
	if err != nil {
		return "", fmt.Errorf("capture git workspace review status: %w", err)
	}
	if statusResult.StdoutTruncated {
		return "", ErrReviewSnapshotTooLarge
	}
	if statusResult.Stderr != "" || statusResult.StderrTruncated {
		return "", errors.New("capture git workspace review status: unexpected diagnostics")
	}
	scoped, err := scopeStatusPorcelain(statusResult.Stdout, v.WorkspacePrefix())
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrReviewSnapshotInvalid, err)
	}
	return scoped, nil
}

func (v *ReadOnlyView) captureReviewStatusForPath(ctx context.Context, maxBytes int64, path string) (string, error) {
	statusResult, err := v.RunLimited(ctx, maxBytes, passiveInspectionArgs(
		"status", "--porcelain=v1", "-z", "--untracked-files=all", "--no-renames", "--", literalGitPathspec(path),
	)...)
	if err != nil {
		return "", fmt.Errorf("capture git workspace file-review status: %w", err)
	}
	if statusResult.StdoutTruncated {
		return "", ErrReviewSnapshotTooLarge
	}
	if statusResult.Stderr != "" || statusResult.StderrTruncated {
		return "", errors.New("capture git workspace file-review status: unexpected diagnostics")
	}
	scoped, err := scopeStatusPorcelain(statusResult.Stdout, v.WorkspacePrefix())
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrReviewSnapshotInvalid, err)
	}
	return scoped, nil
}

func (v *ReadOnlyView) captureReviewLayer(ctx context.Context, maxBytes int64, cached bool) (ReviewLayerSnapshot, error) {
	if maxBytes <= 0 {
		return ReviewLayerSnapshot{Complete: false, IncompleteReason: "size_limit"}, nil
	}
	args := []string{"diff"}
	if cached {
		args = append(args, "--cached")
	}
	// Review capture deliberately omits --binary. Git's ordinary diff is
	// byte-exact for text while reducing binary changes to bounded metadata;
	// mutation authority is captured separately by SnapshotDiff with --binary.
	// This keeps a multi-megabyte binary from spending the entire passive-view
	// deadline before the operator can even see that the file changed.
	common := []string{"--relative", "--no-renames", "--no-ext-diff", "--no-textconv"}
	patchArgs := append(append([]string{}, args...), common...)
	patchArgs = append(patchArgs, "--", ".")
	patchResult, err := v.RunLimited(ctx, maxBytes, passiveInspectionArgs(patchArgs...)...)
	if err != nil {
		return ReviewLayerSnapshot{}, fmt.Errorf("capture git workspace review layer: %w", err)
	}
	if patchResult.StdoutTruncated {
		return ReviewLayerSnapshot{Complete: false, IncompleteReason: "size_limit"}, nil
	}
	if patchResult.Stderr != "" || patchResult.StderrTruncated {
		return ReviewLayerSnapshot{}, errors.New("capture git workspace review layer: unexpected diagnostics")
	}
	return v.reviewLayerFromPatch(ctx, patchResult.Stdout, reviewPatchIsExact(patchResult.Stdout))
}

func reviewPatchIsExact(patch string) bool {
	return !strings.HasPrefix(patch, "Binary files ") && !strings.Contains(patch, "\nBinary files ")
}

func (v *ReadOnlyView) reviewLayerFromPatch(ctx context.Context, patch string, exact bool) (ReviewLayerSnapshot, error) {
	paths := []string{}
	if patch != "" {
		var err error
		paths, err = v.patchPaths(ctx, patch)
		if err != nil {
			return ReviewLayerSnapshot{}, fmt.Errorf("%w: %v", ErrReviewSnapshotInvalid, err)
		}
		if len(paths) == 0 {
			return ReviewLayerSnapshot{}, fmt.Errorf("%w: non-empty review patch has no parsed paths", ErrReviewSnapshotInvalid)
		}
	}
	for _, path := range paths {
		if err := validateReviewPath(path); err != nil {
			return ReviewLayerSnapshot{}, err
		}
	}
	return ReviewLayerSnapshot{Diff: patch, Paths: paths, Complete: true, Exact: exact}, nil
}

func (v *ReadOnlyView) captureReviewLayerForPath(ctx context.Context, maxBytes int64, path string) (ReviewLayerSnapshot, error) {
	patchResult, err := v.RunLimited(ctx, maxBytes, passiveInspectionArgs(
		"diff", "--relative", "--no-renames", "--no-ext-diff", "--no-textconv", "--", literalGitPathspec(path),
	)...)
	if err != nil {
		return ReviewLayerSnapshot{}, fmt.Errorf("capture git workspace file-review layer: %w", err)
	}
	if patchResult.StdoutTruncated {
		return ReviewLayerSnapshot{Paths: []string{path}, Complete: false, IncompleteReason: "size_limit"}, nil
	}
	if patchResult.Stderr != "" || patchResult.StderrTruncated {
		return ReviewLayerSnapshot{}, errors.New("capture git workspace file-review layer: unexpected diagnostics")
	}
	paths := []string{}
	if patchResult.Stdout != "" {
		paths, err = v.patchPaths(ctx, patchResult.Stdout)
		if err != nil {
			return ReviewLayerSnapshot{}, fmt.Errorf("%w: %v", ErrReviewSnapshotInvalid, err)
		}
	}
	return ReviewLayerSnapshot{Diff: patchResult.Stdout, Paths: paths, Complete: true, Exact: reviewPatchIsExact(patchResult.Stdout)}, nil
}

func literalGitPathspec(path string) string {
	return ":(literal)" + path
}

type reviewStatusPaths struct {
	staged      []string
	unstaged    []string
	untracked   []ReviewUntrackedEntry
	intentToAdd []string
	conflicts   []string
	entries     []ReviewStatusEntry
}

func parseReviewStatusPaths(raw string) (reviewStatusPaths, error) {
	paths := reviewStatusPaths{staged: []string{}, unstaged: []string{}, untracked: []ReviewUntrackedEntry{}}
	if raw == "" {
		return paths, nil
	}
	if !strings.HasSuffix(raw, "\x00") {
		return reviewStatusPaths{}, fmt.Errorf("%w: malformed porcelain status", ErrReviewSnapshotInvalid)
	}
	staged := map[string]struct{}{}
	unstaged := map[string]struct{}{}
	untracked := map[string]string{}
	intentToAdd := map[string]struct{}{}
	conflicts := map[string]struct{}{}
	for _, record := range strings.Split(strings.TrimSuffix(raw, "\x00"), "\x00") {
		if len(record) < 4 || record[2] != ' ' || strings.ContainsAny(record[:2], "RC") {
			return reviewStatusPaths{}, fmt.Errorf("%w: malformed or renamed porcelain status record", ErrReviewSnapshotInvalid)
		}
		code := record[:2]
		path := record[3:]
		untrackedKind := ""
		if code == "??" && strings.HasSuffix(path, "/") {
			path = strings.TrimSuffix(path, "/")
			untrackedKind = "nested_repository"
		}
		if err := validateReviewPath(path); err != nil {
			return reviewStatusPaths{}, err
		}
		conflict := isUnmergedStatus(code)
		paths.entries = append(paths.entries, ReviewStatusEntry{
			Path:           path,
			IndexStatus:    code[0],
			WorktreeStatus: code[1],
			UntrackedKind:  untrackedKind,
			Conflict:       conflict,
		})
		switch code {
		case "??":
			if _, duplicate := untracked[path]; duplicate {
				return reviewStatusPaths{}, fmt.Errorf("%w: duplicate untracked porcelain path", ErrReviewSnapshotInvalid)
			}
			if untrackedKind == "" {
				untrackedKind = "file"
			}
			untracked[path] = untrackedKind
		case "!!":
			return reviewStatusPaths{}, fmt.Errorf("%w: ignored path appeared in workspace review", ErrReviewSnapshotInvalid)
		default:
			if conflict {
				conflicts[path] = struct{}{}
			}
			if code == " A" {
				if _, duplicate := intentToAdd[path]; duplicate {
					return reviewStatusPaths{}, fmt.Errorf("%w: duplicate intent-to-add porcelain path", ErrReviewSnapshotInvalid)
				}
				intentToAdd[path] = struct{}{}
			}
			if code[0] != ' ' {
				if _, duplicate := staged[path]; duplicate {
					return reviewStatusPaths{}, fmt.Errorf("%w: duplicate staged porcelain path", ErrReviewSnapshotInvalid)
				}
				staged[path] = struct{}{}
			}
			if code[1] != ' ' {
				if _, duplicate := unstaged[path]; duplicate {
					return reviewStatusPaths{}, fmt.Errorf("%w: duplicate unstaged porcelain path", ErrReviewSnapshotInvalid)
				}
				unstaged[path] = struct{}{}
			}
		}
	}
	paths.staged = sortedReviewPaths(staged)
	paths.unstaged = sortedReviewPaths(unstaged)
	for path, kind := range untracked {
		paths.untracked = append(paths.untracked, ReviewUntrackedEntry{Path: path, Kind: kind})
	}
	sort.Slice(paths.untracked, func(i, j int) bool { return paths.untracked[i].Path < paths.untracked[j].Path })
	paths.intentToAdd = sortedReviewPaths(intentToAdd)
	paths.conflicts = sortedReviewPaths(conflicts)
	sort.Slice(paths.entries, func(i, j int) bool { return paths.entries[i].Path < paths.entries[j].Path })
	return paths, nil
}

func isUnmergedStatus(code string) bool {
	switch code {
	case "DD", "AU", "UD", "UA", "DU", "AA", "UU":
		return true
	default:
		return false
	}
}

func equalIndexVisibilityEntries(first, second []IndexVisibilityEntry) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index] != second[index] {
			return false
		}
	}
	return true
}

func validateReviewPath(path string) error {
	if path == "" || strings.ContainsRune(path, '\x00') || !utf8.ValidString(path) {
		return fmt.Errorf("%w: workspace-relative path must be non-empty, UTF-8, and NUL-free", ErrReviewSnapshotInvalid)
	}
	local := filepath.FromSlash(path)
	if !filepath.IsLocal(local) || filepath.ToSlash(filepath.Clean(local)) != path {
		return fmt.Errorf("%w: unsafe workspace-relative path %q", ErrReviewSnapshotInvalid, path)
	}
	return nil
}

func sortedReviewPaths(paths map[string]struct{}) []string {
	result := make([]string, 0, len(paths))
	for path := range paths {
		result = append(result, path)
	}
	sort.Strings(result)
	return result
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

// DiffRevision returns the legacy patch-only display digest. It is not a
// mutation precondition; discard authority additionally binds workspace-root
// identity through diffDiscardRevision. The empty patch has one deterministic
// display revision.
func DiffRevision(rawDiff string) string {
	sum := sha256.Sum256([]byte(rawDiff))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// diffDiscardRevision binds a browser confirmation to both the exact patch
// bytes and the filesystem identity of the reviewed workspace root. It is
// deliberately distinct from Revision, which remains display-only legacy
// evidence and must never authorize a mutation.
func diffDiscardRevision(root *workspaceRootIdentity, rawDiff string) (string, error) {
	if root == nil || root.material == "" {
		return "", fmt.Errorf("%w: workspace identity is unavailable", ErrDiffSnapshotInvalid)
	}
	digest := sha256.New()
	_, _ = digest.Write([]byte("hecate.workspace-discard.v1\x00"))
	_, _ = digest.Write([]byte(root.material))
	_, _ = digest.Write([]byte("\x00"))
	_, _ = digest.Write([]byte(rawDiff))
	return "workspace-sha256:" + hex.EncodeToString(digest.Sum(nil)), nil
}

// ReviewAndDiffMatchWorkspace proves that independently captured review and
// discard snapshots came from the directory currently reachable at workspace.
// The filesystem identity remains internal rather than becoming HTTP state.
func (r *LocalRunner) ReviewAndDiffMatchWorkspace(workspace string, review ReviewSnapshot, diff DiffSnapshot) bool {
	if review.workspaceRoot == nil || diff.workspaceRoot == nil || review.workspaceRoot.info == nil || diff.workspaceRoot.info == nil {
		return false
	}
	workspace = filepath.Clean(workspace)
	if workspace != review.workspaceRoot.path || workspace != diff.workspaceRoot.path || !os.SameFile(review.workspaceRoot.info, diff.workspaceRoot.info) {
		return false
	}
	view := &ReadOnlyView{workspace: workspace}
	return view.workspaceIdentityMatches(review.workspaceRoot) && view.workspaceIdentityMatches(diff.workspaceRoot)
}

// ReviewMatchesWorkspace binds a separately pinned preview reader to the same
// directory that produced the Git review evidence.
func (r *LocalRunner) ReviewMatchesWorkspace(workspace string, review ReviewSnapshot) bool {
	if review.workspaceRoot == nil || review.workspaceRoot.info == nil {
		return false
	}
	workspace = filepath.Clean(workspace)
	return workspace == review.workspaceRoot.path && (&ReadOnlyView{workspace: workspace}).workspaceIdentityMatches(review.workspaceRoot)
}

// ReverseApplySnapshot conditionally removes selected changes from the exact
// patch and workspace root covered by snapshot.DiscardRevision. Git applies
// the reverse hunks directly to the current worktree: overlapping edits made
// after the snapshot cause the
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
		if errors.Is(err, ErrReviewSnapshotChanged) {
			return Result{ExitCode: -1}, fmt.Errorf("%w: workspace identity changed after review", ErrDiffSnapshotNotApplicable)
		}
		return Result{ExitCode: -1}, fmt.Errorf("create conditional Git apply view: %w", err)
	}
	defer view.Close()
	if snapshot.workspaceRoot == nil {
		return Result{ExitCode: -1}, fmt.Errorf("%w: snapshot has no workspace identity", ErrDiffSnapshotInvalid)
	}
	wantDiscardRevision, err := diffDiscardRevision(snapshot.workspaceRoot, snapshot.Diff)
	if err != nil || snapshot.DiscardRevision != wantDiscardRevision {
		return Result{ExitCode: -1}, fmt.Errorf("%w: discard revision does not match the reviewed workspace", ErrDiffSnapshotInvalid)
	}
	if !view.workspaceIdentityMatches(snapshot.workspaceRoot) {
		return Result{ExitCode: -1}, fmt.Errorf("%w: workspace identity changed after review", ErrDiffSnapshotNotApplicable)
	}
	if err := validateTrackedPathTopology(ctx, workspace, cleanedPaths); err != nil {
		return Result{ExitCode: -1}, fmt.Errorf("%w: %w", ErrDiffSnapshotNotApplicable, err)
	}
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
	mutationApplied := false
	defer func() {
		release := indexLease.Release
		if r.releaseIndexMutationLease != nil {
			release = func() error { return r.releaseIndexMutationLease(indexLease) }
		}
		if releaseErr := release(); releaseErr != nil {
			cleanupErr := fmt.Errorf("%w: release Git index reservation: %v", ErrDiffSnapshotCleanupFailed, releaseErr)
			if mutationApplied {
				committedErr := errors.Join(ErrDiffSnapshotApplied, cleanupErr)
				if returnErr == nil {
					returnErr = committedErr
				} else {
					returnErr = errors.Join(returnErr, committedErr)
				}
			} else if returnErr == nil {
				returnErr = cleanupErr
			} else {
				returnErr = errors.Join(returnErr, cleanupErr)
			}
		}
	}()
	if hidden, err := view.IndexVisibilityEntries(ctx); err != nil {
		return Result{ExitCode: -1}, fmt.Errorf("verify Git index visibility before conditional apply: %w", err)
	} else if len(hidden) > 0 {
		return Result{ExitCode: -1}, fmt.Errorf("%w: %w", ErrDiffSnapshotNotApplicable, ErrIndexVisibilityUnsupported)
	}
	if err := view.RejectStagedChanges(ctx); err != nil {
		if errors.Is(err, ErrStagedChangesUnsupported) {
			return Result{ExitCode: -1}, fmt.Errorf("%w: %w", ErrDiffSnapshotNotApplicable, err)
		}
		return Result{ExitCode: -1}, fmt.Errorf("verify Git index before conditional apply: %w", err)
	}
	if err := view.RejectSubmoduleChanges(ctx); err != nil {
		if errors.Is(err, ErrSubmoduleChangesUnsupported) {
			return Result{ExitCode: -1}, fmt.Errorf("%w: %w", ErrDiffSnapshotNotApplicable, err)
		}
		return Result{ExitCode: -1}, err
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
	if !view.workspaceIdentityMatches(snapshot.workspaceRoot) {
		return Result{ExitCode: -1}, fmt.Errorf("%w: workspace identity changed before conditional apply", ErrDiffSnapshotNotApplicable)
	}
	if err := validateTrackedPathTopology(ctx, workspace, cleanedPaths); err != nil {
		return Result{ExitCode: -1}, fmt.Errorf("%w: %w", ErrDiffSnapshotNotApplicable, err)
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
	checkArgs := append(append([]string(nil), args...), "--check", "-")
	checkResult, checkErr := view.runWorkTreeInput(ctx, reverseApplyOutputLimit, snapshot.Diff, checkArgs...)
	if checkErr != nil || checkResult.Stdout != "" || checkResult.Stderr != "" || checkResult.StdoutTruncated || checkResult.StderrTruncated {
		if checkErr == nil {
			checkErr = errors.New("Git reverse-apply preflight emitted unexpected diagnostics")
		}
		return checkResult, fmt.Errorf("%w: %w", ErrDiffSnapshotNotApplicable, checkErr)
	}
	if !view.workspaceIdentityMatches(snapshot.workspaceRoot) {
		return Result{ExitCode: -1}, fmt.Errorf("%w: workspace identity changed during conditional apply preflight", ErrDiffSnapshotNotApplicable)
	}
	args = append(args, "-")
	if r.applyReversePatch != nil {
		result, err = r.applyReversePatch(ctx, view, snapshot.Diff, args)
	} else {
		result, err = view.runWorkTreeInput(ctx, reverseApplyOutputLimit, snapshot.Diff, args...)
	}
	if err != nil {
		return result, fmt.Errorf("%w: %w", ErrDiffSnapshotOutcomeUnknown, err)
	}
	mutationApplied = true
	if !view.workspaceIdentityMatches(snapshot.workspaceRoot) {
		return result, fmt.Errorf("%w: workspace identity changed while the patch was applied", ErrDiffSnapshotOutcomeUnknown)
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
		if err := validateReviewPath(candidate); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrDiffSnapshotInvalid, err)
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

func validateTrackedPathTopology(ctx context.Context, workspace string, paths []string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(paths) > trackedPathTopologyMaxEntries {
		return fmt.Errorf("%w: tracked path inventory exceeds %d entries", ErrTrackedPathTopologyUnsafe, trackedPathTopologyMaxEntries)
	}
	fsys, err := workspacefs.NewPinned(workspace)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrTrackedPathTopologyUnsafe, err)
	}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		file, _, _, openErr := fsys.OpenStableRegularRead(path)
		if errors.Is(openErr, os.ErrNotExist) {
			// A tracked deletion has no live endpoint to alias. Conditional apply
			// will recreate it beneath the pinned root only if the patch preflight
			// still matches.
			continue
		}
		if openErr != nil {
			return fmt.Errorf("%w: %v", ErrTrackedPathTopologyUnsafe, openErr)
		}
		if closeErr := file.Close(); closeErr != nil {
			return fmt.Errorf("%w: close tracked workspace path", ErrTrackedPathTopologyUnsafe)
		}
	}
	return nil
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
	return sanitizedEnvForOS(env, runtime.GOOS)
}

func sanitizedEnvForOS(env []string, goos string) []string {
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
	if goos == "windows" {
		allowedPrefixes = append(allowedPrefixes,
			"USERPROFILE=",
			"HOMEDRIVE=",
			"HOMEPATH=",
		)
	}
	out := make([]string, 0, len(env))
	for _, entry := range env {
		for _, prefix := range allowedPrefixes {
			if strings.HasPrefix(entry, prefix) || (goos == "windows" && len(entry) >= len(prefix) && strings.EqualFold(entry[:len(prefix)], prefix)) {
				out = append(out, entry)
				break
			}
		}
	}
	return out
}
