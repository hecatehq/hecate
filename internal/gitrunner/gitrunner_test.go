package gitrunner

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/processrunner"
	"github.com/hecatehq/hecate/internal/sandbox"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
}

func initRepo(t *testing.T) string {
	t.Helper()
	requireGit(t)
	dir := t.TempDir()
	if err := exec.Command("git", "-C", dir, "init", "-b", "main").Run(); err != nil {
		t.Fatalf("git init: %v", err)
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

func TestLocalRunner_CurrentRef(t *testing.T) {
	dir := initRepo(t)
	runner := NewLocalRunner()

	if got := runner.CurrentRef(context.Background(), dir); got != "main" {
		t.Fatalf("CurrentRef = %q, want main", got)
	}
}

func TestLocalRunner_Worktrees(t *testing.T) {
	dir := initRepo(t)
	worktree := filepath.Join(t.TempDir(), "feature worktree")
	if err := exec.Command("git", "-C", dir, "worktree", "add", "-b", "feature/worktrees", worktree).Run(); err != nil {
		t.Fatalf("git worktree add: %v", err)
	}
	runner := NewLocalRunner()

	items, err := runner.Worktrees(context.Background(), dir)
	if err != nil {
		t.Fatalf("Worktrees: %v", err)
	}

	byPath := make(map[string]Worktree)
	for _, item := range items {
		byPath[canonicalTestPath(t, item.Path)] = item
	}
	if got := byPath[canonicalTestPath(t, dir)]; got.Branch != "main" {
		t.Fatalf("main worktree = %+v, want main branch", got)
	}
	if got := byPath[canonicalTestPath(t, worktree)]; got.Branch != "feature/worktrees" {
		t.Fatalf("linked worktree = %+v, want feature/worktrees branch", got)
	}
}

func TestLocalRunner_WorktreesUsesPorcelainZ(t *testing.T) {
	dir := t.TempDir()
	process := &recordingProcessRunner{}
	runner := &LocalRunner{Process: process}

	if _, err := runner.Worktrees(context.Background(), dir); err != nil {
		t.Fatalf("Worktrees: %v", err)
	}
	if got := strings.Join(process.request.Args, " "); got != "worktree list --porcelain -z" {
		t.Fatalf("git args = %q, want porcelain -z worktree list", got)
	}
}

func TestParseWorktreeListPorcelain(t *testing.T) {
	items := parseWorktreeListPorcelain(strings.Join([]string{
		"worktree /tmp/project main",
		"HEAD abc123",
		"branch refs/heads/main",
		"",
		"worktree /tmp/project-detached",
		"HEAD def456",
		"detached",
		"",
	}, "\n"))

	if len(items) != 2 {
		t.Fatalf("items = %+v, want two worktrees", items)
	}
	if items[0].Path != "/tmp/project main" || items[0].Branch != "main" || items[0].Head != "abc123" {
		t.Fatalf("first item = %+v, want path with spaces and main branch", items[0])
	}
	if items[1].Path != "/tmp/project-detached" || !items[1].Detached || items[1].Head != "def456" {
		t.Fatalf("second item = %+v, want detached worktree", items[1])
	}
}

func TestParseWorktreeListPorcelainNUL(t *testing.T) {
	items := parseWorktreeListPorcelain(strings.Join([]string{
		"worktree /tmp/project",
		"HEAD abc123",
		"branch refs/heads/main",
		"",
		"worktree /tmp/project\nnewline",
		"HEAD def456",
		"detached",
		"",
		"",
	}, "\x00"))

	if len(items) != 2 {
		t.Fatalf("items = %+v, want two worktrees", items)
	}
	if items[0].Path != "/tmp/project" || items[0].Branch != "main" || items[0].Head != "abc123" {
		t.Fatalf("first item = %+v, want main branch", items[0])
	}
	if items[1].Path != "/tmp/project\nnewline" || !items[1].Detached || items[1].Head != "def456" {
		t.Fatalf("second item = %+v, want NUL-delimited detached worktree", items[1])
	}
}

func TestLocalRunner_DiffCapturesStatAndPatch(t *testing.T) {
	dir := initRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runner := NewLocalRunner()

	stat, diff := runner.Diff(context.Background(), dir, 64*1024)

	if !strings.Contains(stat, "README.md") {
		t.Fatalf("stat = %q, want README.md", stat)
	}
	if !strings.Contains(diff, "+world") {
		t.Fatalf("diff = %q, want added line", diff)
	}
}

func TestLocalRunner_SnapshotDiffReturnsExactRevision(t *testing.T) {
	dir := initRepo(t)
	path := filepath.Join(dir, "README.md")
	if err := os.WriteFile(path, []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runner := NewLocalRunner()

	first, err := runner.SnapshotDiff(context.Background(), dir, 64*1024)
	if err != nil {
		t.Fatalf("SnapshotDiff: %v", err)
	}
	if !strings.Contains(first.Stat, "README.md") || !strings.Contains(first.Diff, "+world") {
		t.Fatalf("snapshot = %+v, want README patch", first)
	}
	if !strings.HasPrefix(first.Revision, "sha256:") || len(first.Revision) != len("sha256:")+sha256.Size*2 {
		t.Fatalf("revision = %q, want typed SHA-256 digest", first.Revision)
	}
	if got := strings.Join(first.Paths, "\x00"); got != "README.md" {
		t.Fatalf("snapshot paths = %q, want byte-exact README path", got)
	}

	if !strings.HasSuffix(first.Diff, "\n") {
		t.Fatalf("snapshot diff dropped its final newline: %q", first.Diff)
	}
	// The authoritative raw patch must distinguish an edit that changes only
	// trailing whitespace.
	if err := os.WriteFile(path, []byte("hello\nworld \n"), 0o644); err != nil {
		t.Fatalf("rewrite file: %v", err)
	}
	second, err := runner.SnapshotDiff(context.Background(), dir, 64*1024)
	if err != nil {
		t.Fatalf("SnapshotDiff after rewrite: %v", err)
	}
	if second.Revision == first.Revision {
		t.Fatalf("revision stayed %q after trailing-whitespace drift", first.Revision)
	}
}

func TestLocalRunner_SnapshotDiffDoesNotExecuteRepositoryFSMonitor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX fsmonitor hook")
	}
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()

	dir := initRepo(t)
	marker := filepath.Join(t.TempDir(), "fsmonitor-called")
	helper := filepath.Join(t.TempDir(), "fsmonitor")
	script := fmt.Sprintf("#!/bin/sh\nprintf called > %q\n", marker)
	if err := os.WriteFile(helper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := NewLocalRunner()
	if result, err := runner.Run(context.Background(), dir, "config", "core.fsmonitor", helper); err != nil {
		t.Fatalf("git config fsmonitor: %v: %s", err, result.Stderr)
	}
	if result, err := runner.Run(context.Background(), dir, "config", "core.fsmonitorHookVersion", "2"); err != nil {
		t.Fatalf("git config fsmonitor version: %v: %s", err, result.Stderr)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	snapshot, err := runner.SnapshotDiff(context.Background(), dir, 64*1024)
	if err != nil {
		t.Fatalf("SnapshotDiff: %v", err)
	}
	if !strings.Contains(snapshot.Diff, "+changed") {
		t.Fatalf("snapshot diff = %q, want changed README", snapshot.Diff)
	}
	status, err := runner.StatusPorcelain(context.Background(), dir, 64*1024)
	if err != nil {
		t.Fatalf("StatusPorcelain: %v", err)
	}
	if !strings.Contains(status, " M README.md\x00") {
		t.Fatalf("status = %q, want modified README", status)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("repository fsmonitor helper ran during passive snapshot; stat error = %v", err)
	}
}

func TestLocalRunner_SnapshotDiffRefusesContentFilterWithoutExecutingIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX content-filter helper")
	}
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()

	dir := initRepo(t)
	runner := NewLocalRunner()
	if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("README.md filter=evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if result, err := runner.Run(context.Background(), dir, "add", ".gitattributes"); err != nil {
		t.Fatalf("git add attributes: %v: %s", err, result.Stderr)
	}
	if result, err := runner.Run(context.Background(), dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "attributes"); err != nil {
		t.Fatalf("git commit attributes: %v: %s", err, result.Stderr)
	}
	marker := filepath.Join(t.TempDir(), "filter-called")
	helper := filepath.Join(t.TempDir(), "filter")
	script := fmt.Sprintf("#!/bin/sh\nprintf called > %q\ncat\n", marker)
	if err := os.WriteFile(helper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if result, err := runner.Run(context.Background(), dir, "config", "filter.evil.clean", helper); err != nil {
		t.Fatalf("git config filter: %v: %s", err, result.Stderr)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := runner.SnapshotDiff(context.Background(), dir, 64*1024)
	if err == nil || !strings.Contains(err.Error(), "content-conversion filter") {
		t.Fatalf("SnapshotDiff error = %v, want content-filter refusal", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("repository content filter ran during passive snapshot; stat error = %v", err)
	}
}

func TestLocalRunner_SnapshotDiffRejectsScopedStagedChanges(t *testing.T) {
	dir := initRepo(t)
	path := filepath.Join(dir, "README.md")
	if err := os.WriteFile(path, []byte("staged change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := NewLocalRunner()
	if result, err := runner.Run(context.Background(), dir, "add", "--", "README.md"); err != nil {
		t.Fatalf("git add staged change: %v: %s", err, result.Stderr)
	}

	_, err := runner.SnapshotDiff(context.Background(), dir, 64*1024)
	if !errors.Is(err, ErrStagedChangesUnsupported) {
		t.Fatalf("SnapshotDiff error = %v, want ErrStagedChangesUnsupported", err)
	}
}

func TestLocalRunner_SnapshotDiffScopesStagedChangeGuardToNestedWorkspace(t *testing.T) {
	dir := initRepo(t)
	nested := filepath.Join(dir, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "inside.txt"), []byte("inside before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sibling.txt"), []byte("sibling before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := NewLocalRunner()
	if result, err := runner.Run(context.Background(), dir, "add", "--", "nested/inside.txt", "sibling.txt"); err != nil {
		t.Fatalf("git add nested fixture: %v: %s", err, result.Stderr)
	}
	if result, err := runner.Run(context.Background(), dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "nested fixture"); err != nil {
		t.Fatalf("git commit nested fixture: %v: %s", err, result.Stderr)
	}
	if err := os.WriteFile(filepath.Join(dir, "sibling.txt"), []byte("staged outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if result, err := runner.Run(context.Background(), dir, "add", "--", "sibling.txt"); err != nil {
		t.Fatalf("git add staged sibling: %v: %s", err, result.Stderr)
	}
	if err := os.WriteFile(filepath.Join(nested, "inside.txt"), []byte("unstaged inside\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	snapshot, err := runner.SnapshotDiff(context.Background(), nested, 64*1024)
	if err != nil {
		t.Fatalf("SnapshotDiff with staged sibling: %v", err)
	}
	if !strings.Contains(snapshot.Diff, "+unstaged inside") || strings.Contains(snapshot.Diff, "staged outside") {
		t.Fatalf("nested snapshot = %q, want only unstaged nested change", snapshot.Diff)
	}
	if result, err := runner.Run(context.Background(), dir, "add", "--", "nested/inside.txt"); err != nil {
		t.Fatalf("git add staged nested change: %v: %s", err, result.Stderr)
	}
	_, err = runner.SnapshotDiff(context.Background(), nested, 64*1024)
	if !errors.Is(err, ErrStagedChangesUnsupported) {
		t.Fatalf("SnapshotDiff with staged nested change error = %v, want ErrStagedChangesUnsupported", err)
	}
}

func TestLocalRunner_SnapshotDiffPathsIncludeBinaryAndModeOnlyChanges(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture requires executable mode changes")
	}
	dir := initRepo(t)
	runner := NewLocalRunner()
	if result, err := runner.Run(context.Background(), dir, "config", "core.filemode", "true"); err != nil {
		t.Fatalf("enable file mode tracking: %v: %s", err, result.Stderr)
	}
	binaryPath := filepath.Join(dir, "binary.dat")
	modePath := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(binaryPath, []byte{'b', 'e', 'f', 'o', 'r', 'e', 0}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modePath, []byte("#!/bin/sh\nexit 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if result, err := runner.Run(context.Background(), dir, "add", "--", "binary.dat", "script.sh"); err != nil {
		t.Fatalf("git add path fixtures: %v: %s", err, result.Stderr)
	}
	if result, err := runner.Run(context.Background(), dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "path fixtures"); err != nil {
		t.Fatalf("git commit path fixtures: %v: %s", err, result.Stderr)
	}
	if err := os.WriteFile(binaryPath, []byte{'a', 'f', 't', 'e', 'r', 0}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(modePath, 0o755); err != nil {
		t.Fatal(err)
	}

	snapshot, err := runner.SnapshotDiff(context.Background(), dir, 64*1024)
	if err != nil {
		t.Fatalf("SnapshotDiff: %v", err)
	}
	want := []string{"binary.dat", "script.sh"}
	if !equalExactPaths(snapshot.Paths, want) {
		t.Fatalf("snapshot paths = %#v, want binary and mode-only paths %#v", snapshot.Paths, want)
	}
}

func TestReadOnlyView_RejectStagedChangesDoesNotTreatBareExitOneAsGitDifference(t *testing.T) {
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()

	sentinel := errors.New("sandbox wrapper failed")
	process := &fixedResultProcessRunner{
		result: processrunner.Result{ExitCode: 1, Stderr: "wrapper setup failed"},
		err:    sentinel,
	}
	view := &ReadOnlyView{
		runner:    &LocalRunner{Process: process, Env: []string{"PATH=/bin"}},
		workspace: t.TempDir(),
	}

	err := view.RejectStagedChanges(context.Background())
	if errors.Is(err, ErrStagedChangesUnsupported) {
		t.Fatalf("RejectStagedChanges error = %v, must not classify a bare exit 1 as staged changes", err)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("RejectStagedChanges error = %v, want wrapped process failure", err)
	}
}

func TestLocalRunner_SnapshotDiffAndStatusScopeNestedWorkspace(t *testing.T) {
	dir := initRepo(t)
	nested := filepath.Join(dir, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "inside.txt"), []byte("inside before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sibling.txt"), []byte("sibling before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := NewLocalRunner()
	if result, err := runner.Run(context.Background(), dir, "add", "nested/inside.txt", "sibling.txt"); err != nil {
		t.Fatalf("git add nested fixture: %v: %s", err, result.Stderr)
	}
	if result, err := runner.Run(context.Background(), dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "nested fixture"); err != nil {
		t.Fatalf("git commit nested fixture: %v: %s", err, result.Stderr)
	}
	if err := os.WriteFile(filepath.Join(nested, "inside.txt"), []byte("inside after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sibling.txt"), []byte("sibling secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	snapshot, err := runner.SnapshotDiff(context.Background(), nested, 64*1024)
	if err != nil {
		t.Fatalf("SnapshotDiff(nested): %v", err)
	}
	if !strings.Contains(snapshot.Diff, "diff --git a/inside.txt b/inside.txt") || !strings.Contains(snapshot.Diff, "+inside after") {
		t.Fatalf("nested diff = %q, want workspace-relative inside patch", snapshot.Diff)
	}
	for _, leaked := range []string{"sibling.txt", "sibling secret", "a/nested/inside.txt"} {
		if strings.Contains(snapshot.Diff, leaked) || strings.Contains(snapshot.Stat, leaked) {
			t.Fatalf("nested snapshot leaked or mis-scoped %q: %+v", leaked, snapshot)
		}
	}
	if got := strings.Join(snapshot.Paths, "\x00"); got != "inside.txt" {
		t.Fatalf("nested snapshot paths = %q, want workspace-relative inside path", got)
	}
	status, err := runner.StatusPorcelain(context.Background(), nested, 64*1024)
	if err != nil {
		t.Fatalf("StatusPorcelain(nested): %v", err)
	}
	if status != " M inside.txt\x00" {
		t.Fatalf("nested status = %q, want only workspace-relative inside path", status)
	}
	if _, err := runner.ReverseApplySnapshot(context.Background(), nested, snapshot, []string{"inside.txt"}); err != nil {
		t.Fatalf("ReverseApplySnapshot(nested): %v", err)
	}
	assertFileContent(t, filepath.Join(nested, "inside.txt"), "inside before\n")
	assertFileContent(t, filepath.Join(dir, "sibling.txt"), "sibling secret\n")
}

func TestLocalRunner_SnapshotDiffFailsClosedWhenPatchIsTruncated(t *testing.T) {
	dir := initRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(strings.Repeat("changed", 64)+"\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := NewLocalRunner().SnapshotDiff(context.Background(), dir, 32)

	if !errors.Is(err, ErrDiffSnapshotTooLarge) {
		t.Fatalf("SnapshotDiff error = %v, want ErrDiffSnapshotTooLarge", err)
	}
}

func TestLocalRunner_StatusPorcelainFailsClosedWhenOutputIsTruncated(t *testing.T) {
	dir := initRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := NewLocalRunner().StatusPorcelain(context.Background(), dir, 3)
	if !errors.Is(err, ErrStatusSnapshotTooLarge) {
		t.Fatalf("StatusPorcelain error = %v, want ErrStatusSnapshotTooLarge", err)
	}
}

func TestDiffRevisionIsDeterministicForEmptyPatch(t *testing.T) {
	const want = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got := DiffRevision(""); got != want {
		t.Fatalf("DiffRevision(empty) = %q, want %q", got, want)
	}
}

func TestLocalRunner_ReverseApplySnapshotRestoresOnlySelectedPatch(t *testing.T) {
	dir := initRepo(t)
	runner := NewLocalRunner()
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("notes before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if result, err := runner.Run(context.Background(), dir, "add", "notes.md"); err != nil {
		t.Fatalf("git add notes: %v: %s", err, result.Stderr)
	}
	if result, err := runner.Run(context.Background(), dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "notes"); err != nil {
		t.Fatalf("git commit notes: %v: %s", err, result.Stderr)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("readme changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("notes changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := runner.SnapshotDiff(context.Background(), dir, 64*1024)
	if err != nil {
		t.Fatalf("SnapshotDiff: %v", err)
	}

	if _, err := runner.ReverseApplySnapshot(context.Background(), dir, snapshot, []string{"README.md"}); err != nil {
		t.Fatalf("ReverseApplySnapshot: %v", err)
	}
	assertFileContent(t, filepath.Join(dir, "README.md"), "hello\n")
	assertFileContent(t, filepath.Join(dir, "notes.md"), "notes changed\n")
}

func TestLocalRunner_ReverseApplySnapshotRejectsStagingAfterReview(t *testing.T) {
	dir := initRepo(t)
	path := filepath.Join(dir, "README.md")
	if err := os.WriteFile(path, []byte("reviewed change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := NewLocalRunner()
	snapshot, err := runner.SnapshotDiff(context.Background(), dir, 64*1024)
	if err != nil {
		t.Fatalf("SnapshotDiff: %v", err)
	}
	if result, err := runner.Run(context.Background(), dir, "add", "--", "README.md"); err != nil {
		t.Fatalf("git add after review: %v: %s", err, result.Stderr)
	}

	_, err = runner.ReverseApplySnapshot(context.Background(), dir, snapshot, []string{"README.md"})
	if !errors.Is(err, ErrDiffSnapshotNotApplicable) || !errors.Is(err, ErrStagedChangesUnsupported) {
		t.Fatalf("ReverseApplySnapshot error = %v, want not-applicable staged-change conflict", err)
	}
	assertFileContent(t, path, "reviewed change\n")
	staged, stagedErr := runner.Run(context.Background(), dir, "diff", "--cached", "--", "README.md")
	if stagedErr != nil || !strings.Contains(staged.Stdout, "+reviewed change") {
		t.Fatalf("staged change was not preserved: result=%+v error=%v", staged, stagedErr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".git", "index.lock")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("conditional apply leaked Git index lock: %v", statErr)
	}
}

func TestLocalRunner_ReverseApplySnapshotRejectsCommittedIndexBaselineAfterReview(t *testing.T) {
	dir := initRepo(t)
	path := filepath.Join(dir, "README.md")
	if err := os.WriteFile(path, []byte("reviewed change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := NewLocalRunner()
	snapshot, err := runner.SnapshotDiff(context.Background(), dir, 64*1024)
	if err != nil {
		t.Fatalf("SnapshotDiff: %v", err)
	}
	if result, err := runner.Run(context.Background(), dir, "add", "--", "README.md"); err != nil {
		t.Fatalf("git add after review: %v: %s", err, result.Stderr)
	}
	if result, err := runner.Run(context.Background(), dir,
		"-c", "user.name=Test", "-c", "user.email=test@example.com",
		"commit", "-m", "commit reviewed change",
	); err != nil {
		t.Fatalf("git commit after review: %v: %s", err, result.Stderr)
	}

	_, err = runner.ReverseApplySnapshot(context.Background(), dir, snapshot, []string{"README.md"})
	if !errors.Is(err, ErrDiffSnapshotNotApplicable) {
		t.Fatalf("ReverseApplySnapshot error = %v, want ErrDiffSnapshotNotApplicable", err)
	}
	assertFileContent(t, path, "reviewed change\n")
	status, statusErr := runner.Run(context.Background(), dir, "status", "--porcelain=v1", "--", "README.md")
	if statusErr != nil || status.Stdout != "" {
		t.Fatalf("workspace after committed-baseline conflict = result %+v error %v, want clean", status, statusErr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".git", "index.lock")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("committed-baseline conflict leaked Git index lock: %v", statErr)
	}
}

func TestLocalRunner_ReverseApplySnapshotRejectsBusyGitIndex(t *testing.T) {
	dir := initRepo(t)
	path := filepath.Join(dir, "README.md")
	if err := os.WriteFile(path, []byte("reviewed change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := NewLocalRunner()
	snapshot, err := runner.SnapshotDiff(context.Background(), dir, 64*1024)
	if err != nil {
		t.Fatalf("SnapshotDiff: %v", err)
	}
	lockPath := filepath.Join(dir, ".git", "index.lock")
	if err := os.WriteFile(lockPath, []byte("external Git writer"), 0o600); err != nil {
		t.Fatalf("create external Git index lock: %v", err)
	}
	defer os.Remove(lockPath)

	_, err = runner.ReverseApplySnapshot(context.Background(), dir, snapshot, []string{"README.md"})
	if !errors.Is(err, ErrDiffSnapshotNotApplicable) {
		t.Fatalf("ReverseApplySnapshot error = %v, want ErrDiffSnapshotNotApplicable", err)
	}
	assertFileContent(t, path, "reviewed change\n")
	data, readErr := os.ReadFile(lockPath)
	if readErr != nil || string(data) != "external Git writer" {
		t.Fatalf("conditional apply disturbed external Git index lock: data=%q error=%v", data, readErr)
	}
}

func TestLocalRunner_ReverseApplySnapshotFencesConcurrentGitAddDuringApply(t *testing.T) {
	dir := initRepo(t)
	path := filepath.Join(dir, "README.md")
	if err := os.WriteFile(path, []byte("reviewed change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := NewLocalRunner()
	snapshot, err := runner.SnapshotDiff(context.Background(), dir, 64*1024)
	if err != nil {
		t.Fatalf("SnapshotDiff: %v", err)
	}
	type gitAddAttempt struct {
		output []byte
		err    error
	}
	var attempt gitAddAttempt
	runner.beforeReverseApply = func() {
		if _, statErr := os.Stat(filepath.Join(dir, ".git", "index.lock")); statErr != nil {
			t.Fatalf("Git index was not reserved at reverse-apply seam: %v", statErr)
		}
		attempt.output, attempt.err = exec.Command("git", "-C", dir, "add", "--", "README.md").CombinedOutput()
	}

	if _, err := runner.ReverseApplySnapshot(context.Background(), dir, snapshot, []string{"README.md"}); err != nil {
		t.Fatalf("ReverseApplySnapshot: %v", err)
	}
	if attempt.err == nil || !strings.Contains(string(attempt.output), "index.lock") {
		t.Fatalf("concurrent git add = error %v output %q, want index-lock refusal", attempt.err, attempt.output)
	}
	assertFileContent(t, path, "hello\n")
	status, statusErr := runner.Run(context.Background(), dir, "status", "--porcelain=v1", "--", "README.md")
	if statusErr != nil || status.Stdout != "" {
		t.Fatalf("workspace after fenced reverse apply = result %+v error %v, want clean without MM state", status, statusErr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".git", "index.lock")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("conditional apply leaked Git index lock: %v", statErr)
	}
}

func TestLocalRunner_ReverseApplySnapshotRejectsOverlappingLaterEditAtomically(t *testing.T) {
	dir := initRepo(t)
	runner := NewLocalRunner()
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("notes before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if result, err := runner.Run(context.Background(), dir, "add", "notes.md"); err != nil {
		t.Fatalf("git add notes: %v: %s", err, result.Stderr)
	}
	if result, err := runner.Run(context.Background(), dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "notes"); err != nil {
		t.Fatalf("git commit notes: %v: %s", err, result.Stderr)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("reviewed README\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("reviewed notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := runner.SnapshotDiff(context.Background(), dir, 64*1024)
	if err != nil {
		t.Fatalf("SnapshotDiff: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("new overlapping README edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = runner.ReverseApplySnapshot(context.Background(), dir, snapshot, []string{"README.md", "notes.md"})
	if !errors.Is(err, ErrDiffSnapshotNotApplicable) {
		t.Fatalf("ReverseApplySnapshot error = %v, want ErrDiffSnapshotNotApplicable", err)
	}
	assertFileContent(t, filepath.Join(dir, "README.md"), "new overlapping README edit\n")
	assertFileContent(t, filepath.Join(dir, "notes.md"), "reviewed notes\n")
}

func TestLocalRunner_ReverseApplySnapshotPreservesNonOverlappingLaterEdit(t *testing.T) {
	dir := initRepo(t)
	runner := NewLocalRunner()
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %02d", i+1)
	}
	original := strings.Join(lines, "\n") + "\n"
	path := filepath.Join(dir, "spaced.txt")
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if result, err := runner.Run(context.Background(), dir, "add", "spaced.txt"); err != nil {
		t.Fatalf("git add spaced fixture: %v: %s", err, result.Stderr)
	}
	if result, err := runner.Run(context.Background(), dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "spaced fixture"); err != nil {
		t.Fatalf("git commit spaced fixture: %v: %s", err, result.Stderr)
	}
	lines[1] = "reviewed line 02"
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := runner.SnapshotDiff(context.Background(), dir, 64*1024)
	if err != nil {
		t.Fatalf("SnapshotDiff: %v", err)
	}
	lines[34] = "later line 35"
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := runner.ReverseApplySnapshot(context.Background(), dir, snapshot, []string{"spaced.txt"}); err != nil {
		t.Fatalf("ReverseApplySnapshot: %v", err)
	}
	lines[1] = "line 02"
	assertFileContent(t, path, strings.Join(lines, "\n")+"\n")
}

func TestLocalRunner_ReverseApplySnapshotRejectsAlteredPatch(t *testing.T) {
	dir := initRepo(t)
	path := filepath.Join(dir, "README.md")
	if err := os.WriteFile(path, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := NewLocalRunner()
	snapshot, err := runner.SnapshotDiff(context.Background(), dir, 64*1024)
	if err != nil {
		t.Fatalf("SnapshotDiff: %v", err)
	}
	snapshot.Diff += "\n"

	_, err = runner.ReverseApplySnapshot(context.Background(), dir, snapshot, []string{"README.md"})
	if !errors.Is(err, ErrDiffSnapshotInvalid) {
		t.Fatalf("ReverseApplySnapshot error = %v, want ErrDiffSnapshotInvalid", err)
	}
	assertFileContent(t, path, "changed\n")
}

func TestLocalRunner_ReverseApplySnapshotRejectsAlteredPathAuthority(t *testing.T) {
	dir := initRepo(t)
	path := filepath.Join(dir, "README.md")
	if err := os.WriteFile(path, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := NewLocalRunner()
	snapshot, err := runner.SnapshotDiff(context.Background(), dir, 64*1024)
	if err != nil {
		t.Fatalf("SnapshotDiff: %v", err)
	}
	snapshot.Paths = []string{"different.txt"}

	_, err = runner.ReverseApplySnapshot(context.Background(), dir, snapshot, []string{"README.md"})
	if !errors.Is(err, ErrDiffSnapshotInvalid) {
		t.Fatalf("ReverseApplySnapshot error = %v, want ErrDiffSnapshotInvalid", err)
	}
	assertFileContent(t, path, "changed\n")
}

func TestLocalRunner_ReverseApplySnapshotRejectsSelectedPathAbsentFromPatch(t *testing.T) {
	dir := initRepo(t)
	path := filepath.Join(dir, "README.md")
	if err := os.WriteFile(path, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := NewLocalRunner()
	snapshot, err := runner.SnapshotDiff(context.Background(), dir, 64*1024)
	if err != nil {
		t.Fatalf("SnapshotDiff: %v", err)
	}

	_, err = runner.ReverseApplySnapshot(context.Background(), dir, snapshot, []string{"missing.txt"})
	if !errors.Is(err, ErrDiffSnapshotInvalid) {
		t.Fatalf("ReverseApplySnapshot error = %v, want ErrDiffSnapshotInvalid", err)
	}
	assertFileContent(t, path, "changed\n")
}

func TestLocalRunner_ReverseApplySnapshotPreservesWhitespaceOnlyFilename(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows normalizes trailing spaces in filenames")
	}
	dir := initRepo(t)
	runner := NewLocalRunner()
	const name = "   "
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if result, err := runner.Run(context.Background(), dir, "add", "--", name); err != nil {
		t.Fatalf("git add whitespace path: %v: %s", err, result.Stderr)
	}
	if result, err := runner.Run(context.Background(), dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "whitespace path"); err != nil {
		t.Fatalf("git commit whitespace path: %v: %s", err, result.Stderr)
	}
	if err := os.WriteFile(path, []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := runner.SnapshotDiff(context.Background(), dir, 64*1024)
	if err != nil {
		t.Fatalf("SnapshotDiff: %v", err)
	}

	if _, err := runner.ReverseApplySnapshot(context.Background(), dir, snapshot, []string{name}); err != nil {
		t.Fatalf("ReverseApplySnapshot: %v", err)
	}
	assertFileContent(t, path, "before\n")
}

func TestLocalRunner_ReverseApplySnapshotTreatsSelectedPathAsLiteral(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses filename characters reserved by Windows")
	}
	dir := initRepo(t)
	runner := NewLocalRunner()
	selectedName := "literal[1]*?.txt"
	otherName := "literal1-other.txt"
	for _, name := range []string{selectedName, otherName} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("before\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if result, err := runner.Run(context.Background(), dir, "add", selectedName, otherName); err != nil {
		t.Fatalf("git add literal-path fixture: %v: %s", err, result.Stderr)
	}
	if result, err := runner.Run(context.Background(), dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "literal path fixture"); err != nil {
		t.Fatalf("git commit literal-path fixture: %v: %s", err, result.Stderr)
	}
	for _, name := range []string{selectedName, otherName} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("changed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := runner.SnapshotDiff(context.Background(), dir, 64*1024)
	if err != nil {
		t.Fatalf("SnapshotDiff: %v", err)
	}

	if _, err := runner.ReverseApplySnapshot(context.Background(), dir, snapshot, []string{selectedName}); err != nil {
		t.Fatalf("ReverseApplySnapshot: %v", err)
	}
	assertFileContent(t, filepath.Join(dir, selectedName), "before\n")
	assertFileContent(t, filepath.Join(dir, otherName), "changed\n")
}

func TestLocalRunner_ReverseApplySnapshotDoesNotExecuteLaterContentFilter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX content-filter helper")
	}
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()

	dir := initRepo(t)
	path := filepath.Join(dir, "README.md")
	if err := os.WriteFile(path, []byte("reviewed change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := NewLocalRunner()
	snapshot, err := runner.SnapshotDiff(context.Background(), dir, 64*1024)
	if err != nil {
		t.Fatalf("SnapshotDiff: %v", err)
	}
	marker := filepath.Join(t.TempDir(), "filter-called")
	helper := filepath.Join(t.TempDir(), "filter")
	script := fmt.Sprintf("#!/bin/sh\nprintf called > %q\ncat\n", marker)
	if err := os.WriteFile(helper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("README.md filter=evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if result, err := runner.Run(context.Background(), dir, "config", "filter.evil.smudge", helper); err != nil {
		t.Fatalf("git config filter: %v: %s", err, result.Stderr)
	}

	if _, err := runner.ReverseApplySnapshot(context.Background(), dir, snapshot, []string{"README.md"}); err != nil {
		t.Fatalf("ReverseApplySnapshot: %v", err)
	}
	assertFileContent(t, path, "hello\n")
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("later repository content filter ran during reverse apply; stat error = %v", err)
	}
}

func TestReadOnlyView_RunWorkTreeInputCapsProcessOutput(t *testing.T) {
	process := &recordingProcessRunner{}
	view := &ReadOnlyView{
		runner:   &LocalRunner{Process: process, Env: []string{"PATH=/bin"}},
		workTree: t.TempDir(),
	}

	if _, err := view.runWorkTreeInput(context.Background(), 1234, "patch", "apply", "-"); err != nil {
		t.Fatalf("runWorkTreeInput: %v", err)
	}
	if process.request.MaxStdoutBytes != 1234 || process.request.MaxStderrBytes != 1234 {
		t.Fatalf("output limits = (%d, %d), want 1234/1234", process.request.MaxStdoutBytes, process.request.MaxStderrBytes)
	}
	if process.request.Stdin != "patch" {
		t.Fatalf("stdin = %q, want exact patch", process.request.Stdin)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != want {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}

func TestSanitizedEnvDropsProviderSecrets(t *testing.T) {
	env := SanitizedEnv([]string{
		"PATH=/bin",
		"OPENAI_API_KEY=secret",
		"PROVIDER_XAI_API_KEY=secret",
		"HOME=/tmp/home",
	})

	got := strings.Join(env, "\n")
	if strings.Contains(got, "secret") {
		t.Fatalf("sanitized env leaked secret: %q", got)
	}
	if !strings.Contains(got, "PATH=/bin") || !strings.Contains(got, "HOME=/tmp/home") {
		t.Fatalf("sanitized env = %q, want PATH and HOME", got)
	}
}

func TestLocalRunner_RunLimitedReadOnlyUsesReadOnlyOfflineWrapper(t *testing.T) {
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperBwrap)
	defer reset()

	dir := t.TempDir()
	process := &recordingProcessRunner{}
	extra := filepath.Join(t.TempDir(), "metadata ")
	if err := os.Mkdir(extra, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &LocalRunner{Process: process, Env: []string{"PATH=/bin"}, ReadOnlyPaths: []string{extra}}

	if _, err := runner.RunLimitedReadOnly(context.Background(), dir, 1024, "status", "--porcelain=v1"); err != nil {
		t.Fatalf("RunLimitedReadOnly: %v", err)
	}
	argv := append([]string{process.request.Command}, process.request.Args...)
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "--ro-bind / /") {
		t.Fatalf("wrapped argv = %q, want read-only host root", joined)
	}
	if !strings.Contains(joined, "--unshare-net") {
		t.Fatalf("wrapped argv = %q, want network namespace disabled", joined)
	}
	if !strings.Contains(joined, "--ro-bind "+dir+" "+dir) {
		t.Fatalf("wrapped argv = %q, want workspace rebound read-only", joined)
	}
	if strings.Contains(joined, "--bind "+dir+" "+dir) {
		t.Fatalf("wrapped argv = %q, workspace must not be rebound writable", joined)
	}
	if !strings.Contains(joined, "--ro-bind "+extra+" "+extra) {
		t.Fatalf("wrapped argv = %q, want auxiliary metadata rebound read-only", joined)
	}
	if got := strings.Join(argv[len(argv)-3:], " "); got != "git status --porcelain=v1" {
		t.Fatalf("wrapped argv tail = %q, want fixed Git argv", got)
	}
	if process.request.MaxStdoutBytes != 1024 || process.request.MaxStderrBytes != 1024 {
		t.Fatalf("output limits = (%d, %d), want (1024, 1024)", process.request.MaxStdoutBytes, process.request.MaxStderrBytes)
	}
}

func TestLocalRunner_RunLimitedReadOnlyInputPreservesBinaryPaths(t *testing.T) {
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()
	dir := t.TempDir()
	process := &recordingProcessRunner{}
	runner := &LocalRunner{Process: process, Env: []string{"PATH=/bin"}}
	input := " first path \x00second\x00"
	if _, err := runner.RunLimitedReadOnlyInput(context.Background(), dir, 1024, input, "check-attr", "-z", "--stdin", "filter"); err != nil {
		t.Fatalf("RunLimitedReadOnlyInput: %v", err)
	}
	if process.request.Stdin != input {
		t.Fatalf("stdin = %q, want %q", process.request.Stdin, input)
	}
}

func TestReadOnlyViewDoesNotReloadRepositoryConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX content-conversion helper")
	}
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()

	dir := initRepo(t)
	if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("*.txt filter=evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if result, err := NewLocalRunner().Run(context.Background(), dir, "add", ".gitattributes", "tracked.txt"); err != nil {
		t.Fatalf("git add: %v: %s", err, result.Stderr)
	}
	if result, err := NewLocalRunner().Run(context.Background(), dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "attributes"); err != nil {
		t.Fatalf("git commit: %v: %s", err, result.Stderr)
	}
	runner := NewLocalRunner()
	view, err := runner.NewReadOnlyView(context.Background(), dir)
	if err != nil {
		t.Fatalf("NewReadOnlyView: %v", err)
	}
	defer view.Close()

	marker := filepath.Join(t.TempDir(), "filter-called")
	helper := filepath.Join(t.TempDir(), "filter")
	script := fmt.Sprintf("#!/bin/sh\nprintf called > %q\ncat\n", marker)
	if err := os.WriteFile(helper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if result, err := runner.Run(context.Background(), dir, "config", "filter.evil.clean", helper); err != nil {
		t.Fatalf("git config: %v: %s", err, result.Stderr)
	}
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := view.RunLimited(context.Background(), 4096, "--no-pager", "diff", "--no-ext-diff", "--no-textconv")
	if err != nil {
		t.Fatalf("passive git diff: %v: %s", err, result.Stderr)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("repository config added after snapshot executed a helper; stat error = %v", err)
	}
	if !strings.Contains(result.Stdout, "+after") {
		t.Fatalf("passive diff = %q, want worktree change", result.Stdout)
	}
	env := strings.Join(view.runner.Env, "\n")
	for _, want := range []string{
		"GIT_DIR=" + view.tempDir,
		"GIT_COMMON_DIR=" + view.tempDir,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_CONFIG_SYSTEM=" + os.DevNull,
		"GIT_ATTR_NOSYSTEM=1",
	} {
		if !strings.Contains(env, want) {
			t.Errorf("passive view environment omitted %q:\n%s", want, env)
		}
	}
}

func TestReadOnlyViewSnapshotsSafeCoreConfig(t *testing.T) {
	dir := initRepo(t)
	runner := NewLocalRunner()
	if result, err := runner.Run(context.Background(), dir, "config", "core.autocrlf", "input"); err != nil {
		t.Fatalf("git config autocrlf: %v: %s", err, result.Stderr)
	}
	if result, err := runner.Run(context.Background(), dir, "config", "core.ignorecase", "yes"); err != nil {
		t.Fatalf("git config ignorecase: %v: %s", err, result.Stderr)
	}
	if result, err := runner.Run(context.Background(), dir, "config", "core.longpaths", "true"); err != nil {
		t.Fatalf("git config longpaths: %v: %s", err, result.Stderr)
	}
	view, err := runner.NewReadOnlyView(context.Background(), dir)
	if err != nil {
		t.Fatalf("NewReadOnlyView: %v", err)
	}
	defer view.Close()
	config, err := os.ReadFile(filepath.Join(view.tempDir, "config"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(config)
	if !strings.Contains(text, `autocrlf = "input"`) || !strings.Contains(text, `ignorecase = "true"`) || !strings.Contains(text, `longpaths = "true"`) {
		t.Fatalf("passive config = %q, want normalized safe core settings", text)
	}
}

func TestReadOnlyViewUsesRepositoryTopLevelForNestedWorkspace(t *testing.T) {
	dir := initRepo(t)
	nested := filepath.Join(dir, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "file.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := NewLocalRunner()
	if result, err := runner.Run(context.Background(), dir, "add", "nested/file.txt"); err != nil {
		t.Fatalf("git add: %v: %s", err, result.Stderr)
	}
	if result, err := runner.Run(context.Background(), dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "nested"); err != nil {
		t.Fatalf("git commit: %v: %s", err, result.Stderr)
	}

	view, err := runner.NewReadOnlyView(context.Background(), nested)
	if err != nil {
		t.Fatalf("NewReadOnlyView: %v", err)
	}
	defer view.Close()
	if got := canonicalTestPath(t, view.workTree); got != canonicalTestPath(t, dir) {
		t.Fatalf("workTree = %q, want %q", got, canonicalTestPath(t, dir))
	}
	if got := filepath.ToSlash(view.WorkspacePrefix()); got != "nested" {
		t.Fatalf("WorkspacePrefix() = %q, want nested", got)
	}
	if env := strings.Join(view.runner.Env, "\n"); !strings.Contains(env, "GIT_WORK_TREE="+canonicalTestPath(t, dir)) {
		t.Fatalf("passive view environment omitted repository top-level:\n%s", env)
	}
}

func TestReadOnlyViewUsesGitCanonicalPrefixOnCaseInsensitiveFilesystem(t *testing.T) {
	dir := initRepo(t)
	canonical := filepath.Join(dir, "Sub")
	if err := os.Mkdir(canonical, 0o755); err != nil {
		t.Fatal(err)
	}
	alternate := filepath.Join(dir, "sub")
	if _, err := os.Stat(alternate); err != nil {
		t.Skip("filesystem is case-sensitive")
	}
	view, err := NewLocalRunner().NewReadOnlyView(context.Background(), alternate)
	if err != nil {
		t.Fatalf("NewReadOnlyView: %v", err)
	}
	defer view.Close()
	if got := filepath.ToSlash(view.WorkspacePrefix()); got != "Sub" {
		t.Fatalf("WorkspacePrefix() = %q, want Git-canonical Sub", got)
	}
}

func TestReadOnlyViewPreservesNewlineInGitPrefix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("newline directory fixture is not portable to Windows")
	}
	dir := initRepo(t)
	nested := filepath.Join(dir, "line\nbreak")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	view, err := NewLocalRunner().NewReadOnlyView(context.Background(), nested)
	if err != nil {
		t.Fatalf("NewReadOnlyView: %v", err)
	}
	defer view.Close()
	if got := filepath.ToSlash(view.WorkspacePrefix()); got != "line\nbreak" {
		t.Fatalf("WorkspacePrefix() = %q, want newline-preserving prefix", got)
	}
}

func TestReadOnlyViewPreservesWhitespaceInRepositoryRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("trailing whitespace directory fixture is not portable to Windows")
	}
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()
	for _, tc := range []struct {
		name string
		root string
	}{
		{name: "space and newline", root: "repo \n"},
		{name: "carriage return", root: "repo\r"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := filepath.Join(t.TempDir(), tc.root)
			if err := os.Mkdir(repo, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := exec.Command("git", "-C", repo, "init", "-b", "main").Run(); err != nil {
				t.Fatalf("git init: %v", err)
			}
			if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("tracked\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			runner := NewLocalRunner()
			if result, err := runner.Run(context.Background(), repo, "add", "tracked.txt"); err != nil {
				t.Fatalf("git add: %v: %s", err, result.Stderr)
			}
			if result, err := runner.Run(context.Background(), repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "initial"); err != nil {
				t.Fatalf("git commit: %v: %s", err, result.Stderr)
			}
			view, err := runner.NewReadOnlyView(context.Background(), repo)
			if err != nil {
				t.Fatalf("NewReadOnlyView: %v", err)
			}
			defer view.Close()
			if !os.SameFile(mustStat(t, view.workTree), mustStat(t, repo)) {
				t.Fatalf("workTree = %q, want byte-preserving %q", view.workTree, repo)
			}
			if _, err := view.RunLimited(context.Background(), 4096, "status", "--porcelain=v1", "-b", "--", "."); err != nil {
				t.Fatalf("passive git status: %v", err)
			}
		})
	}
}

func mustStat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func TestReadBoundedOptionalFileRejectsNonRegularAndOversizedFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := readBoundedOptionalFile(dir, 32); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("directory read error = %v, want regular-file refusal", err)
	}
	path := filepath.Join(dir, "metadata")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 33)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readBoundedOptionalFile(path, 32); err == nil || !strings.Contains(err.Error(), "exceeds 32 bytes") {
		t.Fatalf("oversized read error = %v, want bounded refusal", err)
	}
	if runtime.GOOS != "windows" {
		fifo := filepath.Join(dir, "attributes.fifo")
		if err := exec.Command("mkfifo", fifo).Run(); err != nil {
			t.Skipf("mkfifo unavailable: %v", err)
		}
		started := time.Now()
		if _, err := readBoundedOptionalFile(fifo, 32); err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("FIFO read error = %v, want regular-file refusal", err)
		}
		if elapsed := time.Since(started); elapsed > time.Second {
			t.Fatalf("FIFO metadata refusal took %v, want nonblocking open", elapsed)
		}
	}
}

type recordingProcessRunner struct {
	request processrunner.Request
}

type fixedResultProcessRunner struct {
	result processrunner.Result
	err    error
}

func (r *fixedResultProcessRunner) Run(_ context.Context, _ processrunner.Request) (processrunner.Result, error) {
	return r.result, r.err
}

func (r *fixedResultProcessRunner) RunStreaming(_ context.Context, _ processrunner.Request, _ func(processrunner.Chunk)) (processrunner.Result, error) {
	return r.result, r.err
}

func canonicalTestPath(t *testing.T, path string) string {
	t.Helper()
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

func (r *recordingProcessRunner) Run(_ context.Context, req processrunner.Request) (processrunner.Result, error) {
	r.request = req
	return processrunner.Result{}, nil
}

func (r *recordingProcessRunner) RunStreaming(ctx context.Context, req processrunner.Request, _ func(processrunner.Chunk)) (processrunner.Result, error) {
	return r.Run(ctx, req)
}
