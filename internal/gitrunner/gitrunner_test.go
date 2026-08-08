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
	if !strings.HasPrefix(first.DiscardRevision, "workspace-sha256:") || first.DiscardRevision == first.Revision {
		t.Fatalf("discard revision = %q, want distinct workspace-bound digest", first.DiscardRevision)
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

func TestLocalRunner_SnapshotReviewSeparatesStagedUnstagedAndUntrackedLayers(t *testing.T) {
	dir := initRepo(t)
	for path, content := range map[string]string{
		"mixed.txt":  "mixed before\n",
		"staged.txt": "staged before\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runner := NewLocalRunner()
	if result, err := runner.Run(context.Background(), dir, "add", "--", "mixed.txt", "staged.txt"); err != nil {
		t.Fatalf("git add fixtures: %v: %s", err, result.Stderr)
	}
	if result, err := runner.Run(context.Background(), dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "review fixtures"); err != nil {
		t.Fatalf("git commit fixtures: %v: %s", err, result.Stderr)
	}
	if err := os.WriteFile(filepath.Join(dir, "staged.txt"), []byte("staged after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mixed.txt"), []byte("mixed staged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if result, err := runner.Run(context.Background(), dir, "add", "--", "staged.txt", "mixed.txt"); err != nil {
		t.Fatalf("git add staged edits: %v: %s", err, result.Stderr)
	}
	if err := os.WriteFile(filepath.Join(dir, "mixed.txt"), []byte("mixed working tree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\nworking tree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("new file\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	snapshot, err := runner.SnapshotReview(context.Background(), dir, 64*1024)
	if err != nil {
		t.Fatalf("SnapshotReview: %v", err)
	}
	if !snapshot.Complete {
		t.Fatalf("review snapshot = %+v, want complete", snapshot)
	}
	if !equalExactPaths(snapshot.Staged.Paths, []string{"mixed.txt", "staged.txt"}) {
		t.Fatalf("staged paths = %#v", snapshot.Staged.Paths)
	}
	if !equalExactPaths(snapshot.Unstaged.Paths, []string{"README.md", "mixed.txt"}) {
		t.Fatalf("unstaged paths = %#v", snapshot.Unstaged.Paths)
	}
	if len(snapshot.Untracked) != 1 || snapshot.Untracked[0] != (ReviewUntrackedEntry{Path: "untracked.txt", Kind: "file"}) {
		t.Fatalf("untracked paths = %#v", snapshot.Untracked)
	}
	if !strings.Contains(snapshot.Staged.Diff, "+staged after") || !strings.Contains(snapshot.Unstaged.Diff, "+mixed working tree") {
		t.Fatalf("review patches = staged %q, unstaged %q", snapshot.Staged.Diff, snapshot.Unstaged.Diff)
	}
	if _, err := runner.SnapshotDiff(context.Background(), dir, 64*1024); !errors.Is(err, ErrStagedChangesUnsupported) {
		t.Fatalf("SnapshotDiff error = %v, want staged discard refusal", err)
	}
}

func TestLocalRunner_SnapshotReviewRejectsSameStatusContentDrift(t *testing.T) {
	dir := initRepo(t)
	path := filepath.Join(dir, "README.md")
	runner := NewLocalRunner()
	if err := os.WriteFile(path, []byte("staged one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if result, err := runner.Run(t.Context(), dir, "add", "README.md"); err != nil {
		t.Fatalf("git add staged one: %v: %s", err, result.Stderr)
	}
	if err := os.WriteFile(path, []byte("working one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner.beforeReviewVerification = func() {
		if err := os.WriteFile(path, []byte("staged two\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if result, err := runner.Run(t.Context(), dir, "add", "README.md"); err != nil {
			t.Fatalf("git add staged two: %v: %s", err, result.Stderr)
		}
		if err := os.WriteFile(path, []byte("working two\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	_, err := runner.SnapshotReview(t.Context(), dir, 64*1024)
	if !errors.Is(err, ErrReviewSnapshotChanged) {
		t.Fatalf("SnapshotReview error = %v, want same-status content drift refusal", err)
	}
}

func TestLocalRunner_SnapshotReviewFileTreatsPathspecMagicAsLiteral(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows filenames cannot contain a colon")
	}
	dir := initRepo(t)
	const name = ":(literal)real.txt"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "-C", dir, "--literal-pathspecs", "add", "--", name).CombinedOutput(); err != nil {
		t.Fatalf("git add literal path: %v: %s", err, output)
	}
	if output, err := exec.Command("git", "-C", dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "literal path").CombinedOutput(); err != nil {
		t.Fatalf("git commit literal path: %v: %s", err, output)
	}
	if err := os.WriteFile(path, []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	layer, status, found, err := NewLocalRunner().SnapshotReviewFile(t.Context(), dir, name, 256*1024)
	if err != nil {
		t.Fatalf("SnapshotReviewFile: %v", err)
	}
	if !found || !layer.Complete || !equalExactPaths(layer.Paths, []string{name}) || status.Path != name || !strings.Contains(layer.Diff, "+after") {
		t.Fatalf("literal path review = layer %+v status %+v found %v", layer, status, found)
	}
}

func TestLocalRunner_SnapshotReviewMarksIntentToAddIncompleteAndBlocksDiscard(t *testing.T) {
	dir := initRepo(t)
	path := filepath.Join(dir, "intent.txt")
	if err := os.WriteFile(path, []byte("intent content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := NewLocalRunner()
	if result, err := runner.Run(context.Background(), dir, "add", "-N", "--", "intent.txt"); err != nil {
		t.Fatalf("git add -N: %v: %s", err, result.Stderr)
	}

	snapshot, err := runner.SnapshotReview(context.Background(), dir, 64*1024)
	if err != nil {
		t.Fatalf("SnapshotReview: %v", err)
	}
	if snapshot.Complete || !equalExactPaths(snapshot.Unstaged.Paths, []string{"intent.txt"}) {
		t.Fatalf("intent-to-add review = %+v, want visible incomplete working-tree layer", snapshot)
	}
	if len(snapshot.Hidden) != 1 || snapshot.Hidden[0] != (IndexVisibilityEntry{Path: "intent.txt", Kind: "intent_to_add"}) {
		t.Fatalf("hidden entries = %#v, want intent_to_add", snapshot.Hidden)
	}
	if _, err := runner.SnapshotDiff(context.Background(), dir, 64*1024); !errors.Is(err, ErrStagedChangesUnsupported) {
		t.Fatalf("SnapshotDiff error = %v, want intent-to-add discard refusal", err)
	}
	if content, err := os.ReadFile(path); err != nil || string(content) != "intent content\n" {
		t.Fatalf("intent file changed during review: %q, %v", content, err)
	}
}

func TestLocalRunner_SnapshotReviewReportsIndexVisibilityFlagsAndBlocksDiscard(t *testing.T) {
	for _, tc := range []struct {
		name string
		flag string
		kind string
	}{
		{name: "assume_unchanged", flag: "--assume-unchanged", kind: "assume_unchanged"},
		{name: "skip_worktree", flag: "--skip-worktree", kind: "skip_worktree"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := initRepo(t)
			runner := NewLocalRunner()
			if result, err := runner.Run(context.Background(), dir, "update-index", tc.flag, "--", "README.md"); err != nil {
				t.Fatalf("git update-index: %v: %s", err, result.Stderr)
			}
			if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hidden edit\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			snapshot, err := runner.SnapshotReview(context.Background(), dir, 64*1024)
			if err != nil {
				t.Fatalf("SnapshotReview: %v", err)
			}
			if snapshot.Complete || len(snapshot.Hidden) != 1 || snapshot.Hidden[0].Path != "README.md" || snapshot.Hidden[0].Kind != tc.kind {
				t.Fatalf("review = %+v, want incomplete %s entry", snapshot, tc.kind)
			}
			if _, err := runner.SnapshotDiff(context.Background(), dir, 64*1024); !errors.Is(err, ErrIndexVisibilityUnsupported) {
				t.Fatalf("SnapshotDiff error = %v, want index visibility refusal", err)
			}
		})
	}
}

func TestLocalRunner_SnapshotReviewKeepsOversizedTrackedLayerVisibleButIncomplete(t *testing.T) {
	dir := initRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(strings.Repeat("changed\n", 128)), 0o644); err != nil {
		t.Fatal(err)
	}

	snapshot, err := NewLocalRunner().SnapshotReview(context.Background(), dir, 64)
	if err != nil {
		t.Fatalf("SnapshotReview: %v", err)
	}
	if snapshot.Complete || snapshot.Unstaged.Complete || snapshot.Unstaged.Diff != "" || !equalExactPaths(snapshot.Unstaged.Paths, []string{"README.md"}) {
		t.Fatalf("oversized review = %+v, want incomplete README metadata without a patch prefix", snapshot)
	}
}

func TestLocalRunner_SnapshotReviewKeepsKnownEmptyLayerCompleteAtExactBudget(t *testing.T) {
	dir := initRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("staged only\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := NewLocalRunner()
	if result, err := runner.Run(context.Background(), dir, "add", "--", "README.md"); err != nil {
		t.Fatalf("git add: %v: %s", err, result.Stderr)
	}
	baseline, err := runner.SnapshotReview(context.Background(), dir, 64*1024)
	if err != nil {
		t.Fatalf("baseline SnapshotReview: %v", err)
	}
	if len(baseline.Staged.Diff) == 0 || len(baseline.Unstaged.Paths) != 0 {
		t.Fatalf("baseline review = %+v", baseline)
	}

	snapshot, err := runner.SnapshotReview(context.Background(), dir, int64(len(baseline.Staged.Diff)))
	if err != nil {
		t.Fatalf("exact-budget SnapshotReview: %v", err)
	}
	if !snapshot.Unstaged.Complete || len(snapshot.Unstaged.Paths) != 0 || !snapshot.Complete {
		t.Fatalf("exact-budget review = %+v, want known-empty working layer complete", snapshot)
	}
}

func TestLocalRunner_SnapshotReviewHonorsEffectiveExternalExcludes(t *testing.T) {
	dir := initRepo(t)
	excludes := filepath.Join(t.TempDir(), "global-ignore")
	if err := os.WriteFile(excludes, []byte(".env\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := NewLocalRunner()
	if result, err := runner.Run(context.Background(), dir, "config", "core.excludesFile", excludes); err != nil {
		t.Fatalf("configure excludesFile: %v: %s", err, result.Stderr)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("TOKEN=must-not-preview\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	snapshot, err := runner.SnapshotReview(context.Background(), dir, 64*1024)
	if err != nil {
		t.Fatalf("SnapshotReview: %v", err)
	}
	if len(snapshot.Untracked) != 0 || len(snapshot.Status) != 0 {
		t.Fatalf("review exposed effectively ignored file: %+v", snapshot)
	}
}

func TestLocalRunner_SnapshotReviewResolvesRelativeExternalExcludesFromWorkTree(t *testing.T) {
	dir := initRepo(t)
	nested := filepath.Join(dir, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	excludes := filepath.Join(filepath.Dir(dir), "global-ignore")
	if err := os.WriteFile(excludes, []byte("secret.txt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := NewLocalRunner()
	if result, err := runner.Run(context.Background(), dir, "config", "core.excludesFile", "../global-ignore"); err != nil {
		t.Fatalf("configure relative excludesFile: %v: %s", err, result.Stderr)
	}
	for _, path := range []string{"secret.txt", "visible.txt"} {
		if err := os.WriteFile(filepath.Join(nested, path), []byte("private\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	snapshot, err := runner.SnapshotReview(context.Background(), nested, 64*1024)
	if err != nil {
		t.Fatalf("SnapshotReview: %v", err)
	}
	if len(snapshot.Untracked) != 1 || snapshot.Untracked[0].Path != "visible.txt" {
		t.Fatalf("nested untracked entries = %#v, want only visible.txt", snapshot.Untracked)
	}
}

func TestLocalRunner_SnapshotReviewSnapshotsDefaultGlobalExcludes(t *testing.T) {
	dir := initRepo(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	excludes := filepath.Join(home, ".config", "git", "ignore")
	if err := os.MkdirAll(filepath.Dir(excludes), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(excludes, []byte("hidden.txt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"hidden.txt", "visible.txt"} {
		if err := os.WriteFile(filepath.Join(dir, path), []byte("private\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	snapshot, err := NewLocalRunner().SnapshotReview(context.Background(), dir, 64*1024)
	if err != nil {
		t.Fatalf("SnapshotReview: %v", err)
	}
	if len(snapshot.Untracked) != 1 || snapshot.Untracked[0].Path != "visible.txt" {
		t.Fatalf("untracked entries = %#v, want only visible.txt", snapshot.Untracked)
	}
}

func TestLocalRunner_SnapshotReviewDoesNotReenableDefaultExcludesAfterExplicitOverride(t *testing.T) {
	dir := initRepo(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	defaultExcludes := filepath.Join(home, ".config", "git", "ignore")
	if err := os.MkdirAll(filepath.Dir(defaultExcludes), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(defaultExcludes, []byte("default-only.txt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	explicitExcludes := filepath.Join(t.TempDir(), "explicit-ignore")
	if err := os.WriteFile(explicitExcludes, []byte("explicit-only.txt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := NewLocalRunner()
	if result, err := runner.Run(context.Background(), dir, "config", "core.excludesFile", explicitExcludes); err != nil {
		t.Fatalf("configure excludesFile: %v: %s", err, result.Stderr)
	}
	for _, path := range []string{"default-only.txt", "explicit-only.txt"} {
		if err := os.WriteFile(filepath.Join(dir, path), []byte("private\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	snapshot, err := runner.SnapshotReview(context.Background(), dir, 64*1024)
	if err != nil {
		t.Fatalf("SnapshotReview: %v", err)
	}
	if len(snapshot.Untracked) != 1 || snapshot.Untracked[0].Path != "default-only.txt" {
		t.Fatalf("untracked entries = %#v, want explicit override to leave default-only.txt visible", snapshot.Untracked)
	}
}

func TestLocalRunner_ReadOnlyViewKeepsDefaultExcludesSnapshotStable(t *testing.T) {
	dir := initRepo(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	excludes := filepath.Join(home, ".config", "git", "ignore")
	if err := os.MkdirAll(filepath.Dir(excludes), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(excludes, []byte("hidden-before.txt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"hidden-before.txt", "hidden-after.txt"} {
		if err := os.WriteFile(filepath.Join(dir, path), []byte("private\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runner := NewLocalRunner()
	view, err := runner.NewReadOnlyView(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	if err := os.WriteFile(excludes, []byte("hidden-after.txt\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	status, err := view.captureReviewStatus(context.Background(), 64*1024)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseReviewStatusPaths(status)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.untracked) != 1 || parsed.untracked[0].Path != "hidden-after.txt" {
		t.Fatalf("snapshotted untracked entries = %#v, want only hidden-after.txt", parsed.untracked)
	}
}

func TestLocalRunner_SnapshotReviewFiltersExternalExcludesBeforeInventoryLimit(t *testing.T) {
	dir := initRepo(t)
	excludes := filepath.Join(t.TempDir(), "global-ignore")
	if err := os.WriteFile(excludes, []byte("ignored-*\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := NewLocalRunner()
	if result, err := runner.Run(context.Background(), dir, "config", "core.excludesFile", excludes); err != nil {
		t.Fatalf("configure excludesFile: %v: %s", err, result.Stderr)
	}
	for index := 0; index < 100; index++ {
		path := filepath.Join(dir, fmt.Sprintf("ignored-%03d", index))
		if err := os.WriteFile(path, []byte("private\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	snapshot, err := runner.SnapshotReview(context.Background(), dir, 128)
	if err != nil {
		t.Fatalf("SnapshotReview with bounded ignored tree: %v", err)
	}
	if len(snapshot.Untracked) != 0 || len(snapshot.Status) != 0 || !snapshot.Complete {
		t.Fatalf("review exposed or counted externally ignored tree: %+v", snapshot)
	}
}

func TestLocalRunner_SnapshotReviewClassifiesEmbeddedRepositoryWithoutRecursing(t *testing.T) {
	dir := initRepo(t)
	embedded := filepath.Join(dir, "embedded")
	if err := os.Mkdir(embedded, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", embedded, "init", "-b", "main").Run(); err != nil {
		t.Fatalf("init embedded repository: %v", err)
	}
	if err := os.WriteFile(filepath.Join(embedded, "private.txt"), []byte("nested content\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	snapshot, err := NewLocalRunner().SnapshotReview(context.Background(), dir, 64*1024)
	if err != nil {
		t.Fatalf("SnapshotReview: %v", err)
	}
	if len(snapshot.Untracked) != 1 || snapshot.Untracked[0] != (ReviewUntrackedEntry{Path: "embedded", Kind: "nested_repository"}) {
		t.Fatalf("untracked entries = %#v, want opaque embedded repository", snapshot.Untracked)
	}
}

func TestLocalRunner_SnapshotReviewShowsTrackedSubmoduleButBlocksDiscard(t *testing.T) {
	source := initRepo(t)
	dir := initRepo(t)
	if output, err := exec.Command(
		"git", "-C", dir, "-c", "protocol.file.allow=always",
		"submodule", "add", "--", source, "dependency",
	).CombinedOutput(); err != nil {
		t.Fatalf("git submodule add: %v: %s", err, output)
	}
	if output, err := exec.Command(
		"git", "-C", dir, "-c", "user.name=Test", "-c", "user.email=test@example.com",
		"commit", "-am", "add dependency",
	).CombinedOutput(); err != nil {
		t.Fatalf("commit submodule: %v: %s", err, output)
	}
	if err := os.WriteFile(filepath.Join(dir, "dependency", "README.md"), []byte("dirty submodule\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := NewLocalRunner()
	snapshot, err := runner.SnapshotReview(context.Background(), dir, 64*1024)
	if err != nil {
		t.Fatalf("SnapshotReview: %v", err)
	}
	if !snapshot.Complete || !equalExactPaths(snapshot.Unstaged.Paths, []string{"dependency"}) || !strings.Contains(snapshot.Unstaged.Diff, "Subproject commit") {
		t.Fatalf("submodule review = %+v, want visible complete working-tree evidence", snapshot)
	}
	if _, err := runner.SnapshotDiff(context.Background(), dir, 64*1024); !errors.Is(err, ErrSubmoduleChangesUnsupported) {
		t.Fatalf("SnapshotDiff error = %v, want submodule discard refusal", err)
	}
}

func TestLocalRunner_SnapshotReviewSurfacesUnmergedStateWithoutParsingCombinedPatch(t *testing.T) {
	dir := initRepo(t)
	runner := NewLocalRunner()
	if result, err := runner.Run(context.Background(), dir, "checkout", "-b", "other"); err != nil {
		t.Fatalf("checkout other: %v: %s", err, result.Stderr)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if result, err := runner.Run(context.Background(), dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-am", "other"); err != nil {
		t.Fatalf("commit other: %v: %s", err, result.Stderr)
	}
	if result, err := runner.Run(context.Background(), dir, "checkout", "main"); err != nil {
		t.Fatalf("checkout main: %v: %s", err, result.Stderr)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if result, err := runner.Run(context.Background(), dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-am", "main"); err != nil {
		t.Fatalf("commit main: %v: %s", err, result.Stderr)
	}
	if result, err := runner.Run(context.Background(), dir, "merge", "other"); err == nil || result.ExitCode == 0 {
		t.Fatalf("merge result = %+v, %v, want conflict", result, err)
	}

	snapshot, err := runner.SnapshotReview(context.Background(), dir, 64*1024)
	if err != nil {
		t.Fatalf("SnapshotReview with conflict: %v", err)
	}
	if snapshot.Complete || len(snapshot.Hidden) != 1 || snapshot.Hidden[0] != (IndexVisibilityEntry{Path: "README.md", Kind: "unmerged"}) {
		t.Fatalf("conflicted review = %+v", snapshot)
	}
	if snapshot.Staged.Diff != "" || snapshot.Unstaged.Diff != "" {
		t.Fatalf("conflicted review projected combined patch: %+v", snapshot)
	}
	if snapshot.Staged.IncompleteReason != "unmerged_state" || snapshot.Unstaged.IncompleteReason != "unmerged_state" {
		t.Fatalf("conflicted review reasons = staged %q unstaged %q, want unmerged_state", snapshot.Staged.IncompleteReason, snapshot.Unstaged.IncompleteReason)
	}
}

func TestLocalRunner_ReadOnlyViewForcesCompleteStatChecks(t *testing.T) {
	dir := initRepo(t)
	runner := NewLocalRunner()
	for key, value := range map[string]string{
		"core.ignorestat": "true",
		"core.trustctime": "false",
		"core.checkstat":  "minimal",
	} {
		if result, err := runner.Run(context.Background(), dir, "config", key, value); err != nil {
			t.Fatalf("configure %s: %v: %s", key, err, result.Stderr)
		}
	}
	path := filepath.Join(dir, "README.md")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("HELLO\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}

	snapshot, err := runner.SnapshotReview(context.Background(), dir, 64*1024)
	if err != nil {
		t.Fatalf("SnapshotReview: %v", err)
	}
	if !strings.Contains(snapshot.Unstaged.Diff, "+HELLO") {
		t.Fatalf("review missed same-size rewrite under weak repository stat config: %+v", snapshot)
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

func TestLocalRunner_SnapshotReviewRejectsTemporaryDirectoryInsideWorktree(t *testing.T) {
	dir := initRepo(t)
	t.Setenv("TMPDIR", dir)

	_, err := NewLocalRunner().SnapshotReview(t.Context(), dir, 64*1024)
	if err == nil || !strings.Contains(err.Error(), "overlaps the inspected worktree") {
		t.Fatalf("SnapshotReview error = %v, want overlapping temporary-directory refusal", err)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "hecate-git-read-") {
			t.Fatalf("temporary passive Git directory leaked into worktree: %s", entry.Name())
		}
	}
}

func TestLocalRunner_SnapshotReviewRejectsRelativeTemporaryDirectoryInsideWorktree(t *testing.T) {
	dir := initRepo(t)
	t.Chdir(dir)
	t.Setenv("TMPDIR", ".")

	_, err := NewLocalRunner().SnapshotReview(t.Context(), dir, 64*1024)
	if err == nil || !strings.Contains(err.Error(), "overlaps the inspected worktree") {
		t.Fatalf("SnapshotReview error = %v, want relative overlapping temporary-directory refusal", err)
	}
}

func TestLocalRunner_SnapshotDiffRejectsNonUTF8PatchPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows filenames are UTF-16")
	}
	dir := initRepo(t)
	runner := NewLocalRunner()
	name := "invalid-" + string([]byte{0xff}) + ".txt"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Skipf("filesystem does not permit a non-UTF-8 filename: %v", err)
	}
	if result, err := runner.Run(t.Context(), dir, "add", "--", name); err != nil {
		t.Fatalf("git add invalid path: %v: %s", err, result.Stderr)
	}
	if result, err := runner.Run(t.Context(), dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "invalid path fixture"); err != nil {
		t.Fatalf("git commit invalid path: %v: %s", err, result.Stderr)
	}
	if err := os.WriteFile(path, []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := runner.SnapshotDiff(t.Context(), dir, 64*1024)
	if !errors.Is(err, ErrDiffSnapshotInvalid) {
		t.Fatalf("SnapshotDiff error = %v, want ErrDiffSnapshotInvalid", err)
	}
}

func TestLocalRunner_SnapshotDiffRejectsTrackedHardlinkTopology(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hardlink mode semantics differ on Windows")
	}
	dir := initRepo(t)
	runner := NewLocalRunner()
	externalPath := filepath.Join(filepath.Dir(dir), "outside-hardlink.txt")
	if err := os.WriteFile(externalPath, []byte("shared\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkedPath := filepath.Join(dir, "linked.txt")
	if err := os.Link(externalPath, linkedPath); err != nil {
		t.Skipf("hardlinks unavailable: %v", err)
	}
	if result, err := runner.Run(t.Context(), dir, "add", "--", "linked.txt"); err != nil {
		t.Fatalf("git add hardlink: %v: %s", err, result.Stderr)
	}
	if result, err := runner.Run(t.Context(), dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "hardlink fixture"); err != nil {
		t.Fatalf("git commit hardlink: %v: %s", err, result.Stderr)
	}
	if err := os.Chmod(linkedPath, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := runner.SnapshotDiff(t.Context(), dir, 64*1024)
	if !errors.Is(err, ErrTrackedPathTopologyUnsafe) {
		t.Fatalf("SnapshotDiff error = %v, want ErrTrackedPathTopologyUnsafe", err)
	}
	info, statErr := os.Stat(externalPath)
	if statErr != nil {
		t.Fatalf("stat external hardlink: %v", statErr)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("external hardlink mode = %v; want untouched 0755", info.Mode().Perm())
	}
}

func TestValidateTrackedPathTopologyHonorsContextAndEntryLimit(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := validateTrackedPathTopology(ctx, t.TempDir(), []string{"note.txt"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled topology validation error = %v, want context.Canceled", err)
	}
	paths := make([]string, trackedPathTopologyMaxEntries+1)
	if err := validateTrackedPathTopology(t.Context(), t.TempDir(), paths); !errors.Is(err, ErrTrackedPathTopologyUnsafe) {
		t.Fatalf("oversized topology validation error = %v, want ErrTrackedPathTopologyUnsafe", err)
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

func TestLocalRunner_ReverseApplySnapshotReportsCommittedCleanupFailure(t *testing.T) {
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
	runner.releaseIndexMutationLease = func(lease *indexMutationLease) error {
		if err := lease.Release(); err != nil {
			return err
		}
		return errors.New("injected cleanup failure")
	}

	result, err := runner.ReverseApplySnapshot(context.Background(), dir, snapshot, []string{"README.md"})
	if result.ExitCode != 0 || !errors.Is(err, ErrDiffSnapshotApplied) {
		t.Fatalf("ReverseApplySnapshot = result %+v error %v, want committed cleanup warning", result, err)
	}
	assertFileContent(t, path, "hello\n")
	if _, statErr := os.Stat(filepath.Join(dir, ".git", "index.lock")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("injected cleanup failure left real Git index lock: %v", statErr)
	}
}

func TestLocalRunner_ReverseApplySnapshotRejectsReplacedWorkspaceRoot(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "workspace")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	runner := NewLocalRunner()
	if result, err := runner.Run(context.Background(), dir, "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %v: %s", err, result.Stderr)
	}
	path := filepath.Join(dir, "README.md")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if result, err := runner.Run(context.Background(), dir, "add", "README.md"); err != nil {
		t.Fatalf("git add: %v: %s", err, result.Stderr)
	}
	if result, err := runner.Run(context.Background(), dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "initial"); err != nil {
		t.Fatalf("git commit: %v: %s", err, result.Stderr)
	}
	if err := os.WriteFile(path, []byte("reviewed change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := runner.SnapshotDiff(context.Background(), dir, 64*1024)
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(parent, "original")
	runner.beforeReverseApply = func() {
		if err := os.Rename(dir, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if result, err := runner.Run(context.Background(), dir, "init", "-b", "main"); err != nil {
			t.Fatalf("replacement git init: %v: %s", err, result.Stderr)
		}
		replacement := filepath.Join(dir, "README.md")
		if err := os.WriteFile(replacement, []byte("hello\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if result, err := runner.Run(context.Background(), dir, "add", "README.md"); err != nil {
			t.Fatalf("replacement git add: %v: %s", err, result.Stderr)
		}
		if result, err := runner.Run(context.Background(), dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "initial"); err != nil {
			t.Fatalf("replacement git commit: %v: %s", err, result.Stderr)
		}
		if err := os.WriteFile(replacement, []byte("reviewed change\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	_, err = runner.ReverseApplySnapshot(context.Background(), dir, snapshot, []string{"README.md"})
	if !errors.Is(err, ErrDiffSnapshotNotApplicable) {
		t.Fatalf("ReverseApplySnapshot error = %v, want replaced-root conflict", err)
	}
	assertFileContent(t, filepath.Join(dir, "README.md"), "reviewed change\n")
	assertFileContent(t, filepath.Join(moved, "README.md"), "reviewed change\n")
	replacementSnapshot, snapshotErr := runner.SnapshotDiff(context.Background(), dir, 64*1024)
	if snapshotErr != nil {
		t.Fatalf("replacement SnapshotDiff: %v", snapshotErr)
	}
	if replacementSnapshot.Revision != snapshot.Revision || replacementSnapshot.DiscardRevision == snapshot.DiscardRevision {
		t.Fatalf("replacement revisions = legacy %q discard %q, want same display digest and distinct root-bound discard token", replacementSnapshot.Revision, replacementSnapshot.DiscardRevision)
	}
	if _, statErr := os.Stat(filepath.Join(moved, ".git", "index.lock")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("replaced-root conflict leaked pinned Git index lock: %v", statErr)
	}
}

func TestLocalRunner_ReverseApplySnapshotClassifiesPostPreflightFailureAsOutcomeUnknown(t *testing.T) {
	dir := initRepo(t)
	runner := NewLocalRunner()
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("notes before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if result, err := runner.Run(context.Background(), dir, "add", "notes.md"); err != nil {
		t.Fatalf("git add: %v: %s", err, result.Stderr)
	}
	if result, err := runner.Run(context.Background(), dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "notes"); err != nil {
		t.Fatalf("git commit: %v: %s", err, result.Stderr)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("README changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("notes changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := runner.SnapshotDiff(context.Background(), dir, 64*1024)
	if err != nil {
		t.Fatal(err)
	}
	runner.applyReversePatch = func(context.Context, *ReadOnlyView, string, []string) (Result, error) {
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return Result{ExitCode: 1}, errors.New("injected failure after first path")
	}

	_, err = runner.ReverseApplySnapshot(context.Background(), dir, snapshot, []string{"README.md", "notes.md"})
	if !errors.Is(err, ErrDiffSnapshotOutcomeUnknown) || errors.Is(err, ErrDiffSnapshotNotApplicable) {
		t.Fatalf("ReverseApplySnapshot error = %v, want outcome unknown", err)
	}
	assertFileContent(t, filepath.Join(dir, "README.md"), "hello\n")
	assertFileContent(t, filepath.Join(dir, "notes.md"), "notes changed\n")
}

func TestLocalRunner_ReverseApplySnapshotReportsOutcomeUnknownWithCleanupFailure(t *testing.T) {
	dir := initRepo(t)
	path := filepath.Join(dir, "README.md")
	if err := os.WriteFile(path, []byte("reviewed change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := NewLocalRunner()
	snapshot, err := runner.SnapshotDiff(t.Context(), dir, 64*1024)
	if err != nil {
		t.Fatal(err)
	}
	runner.applyReversePatch = func(context.Context, *ReadOnlyView, string, []string) (Result, error) {
		return Result{ExitCode: 1}, errors.New("injected apply failure")
	}
	runner.releaseIndexMutationLease = func(lease *indexMutationLease) error {
		if err := lease.Release(); err != nil {
			return err
		}
		return errors.New("injected cleanup failure")
	}

	_, err = runner.ReverseApplySnapshot(t.Context(), dir, snapshot, []string{"README.md"})
	if !errors.Is(err, ErrDiffSnapshotOutcomeUnknown) || !errors.Is(err, ErrDiffSnapshotCleanupFailed) || errors.Is(err, ErrDiffSnapshotApplied) {
		t.Fatalf("ReverseApplySnapshot error = %v, want outcome-unknown plus cleanup-failed without confirmed apply", err)
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

func TestReverseApplyPathsRejectsNonUTF8Path(t *testing.T) {
	_, err := reverseApplyPaths([]string{"invalid-" + string([]byte{0xff})})
	if !errors.Is(err, ErrDiffSnapshotInvalid) {
		t.Fatalf("reverseApplyPaths error = %v, want ErrDiffSnapshotInvalid", err)
	}
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

func TestSanitizedEnvForWindowsPreservesNativeHomeCaseInsensitively(t *testing.T) {
	env := sanitizedEnvForOS([]string{
		"Path=C:\\Tools",
		"userprofile=C:\\Users\\Agent",
		"HomeDrive=C:",
		"homepath=\\Users\\Agent",
		"OPENAI_API_KEY=secret",
	}, "windows")

	got := strings.Join(env, "\n")
	for _, want := range []string{"Path=C:\\Tools", "userprofile=C:\\Users\\Agent", "HomeDrive=C:", "homepath=\\Users\\Agent"} {
		if !strings.Contains(got, want) {
			t.Fatalf("sanitized Windows env = %q, want %q", got, want)
		}
	}
	if strings.Contains(got, "secret") {
		t.Fatalf("sanitized Windows env leaked secret: %q", got)
	}
}

func TestDefaultGitExcludesPathForWindowsHomeFallbacks(t *testing.T) {
	workspace := t.TempDir()
	cases := []struct {
		name string
		env  []string
		want string
	}{
		{
			name: "XDG takes precedence",
			env:  []string{"XDG_CONFIG_HOME=/xdg", "HOME=/home", "USERPROFILE=/profile"},
			want: filepath.Join("/xdg", "git", "ignore"),
		},
		{
			name: "HOME takes precedence over USERPROFILE",
			env:  []string{"HOME=/home", "USERPROFILE=/profile"},
			want: filepath.Join("/home", ".config", "git", "ignore"),
		},
		{
			name: "USERPROFILE fallback is case insensitive",
			env:  []string{"userprofile=/profile"},
			want: filepath.Join("/profile", ".config", "git", "ignore"),
		},
		{
			name: "drive and path fallback",
			env:  []string{"HOMEDRIVE=/volume", "HOMEPATH=/users/agent"},
			want: filepath.Join("/volume", "users", "agent", ".config", "git", "ignore"),
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := defaultGitExcludesPathForOS(workspace, test.env, "windows"); got != test.want {
				t.Fatalf("defaultGitExcludesPathForOS() = %q, want %q", got, test.want)
			}
		})
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

func TestReadOnlyViewRejectsAlternateObjectDatabase(t *testing.T) {
	dir := initRepo(t)
	objectInfo := filepath.Join(dir, ".git", "objects", "info")
	if err := os.MkdirAll(objectInfo, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(objectInfo, "alternates"), []byte(t.TempDir()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	view, err := NewLocalRunner().NewReadOnlyView(context.Background(), dir)
	if view != nil {
		_ = view.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "does not support alternate object databases") {
		t.Fatalf("NewReadOnlyView error = %v, want alternate-object refusal", err)
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

func TestReadBoundedOptionalFileRejectsAtomicReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exclude")
	replacement := filepath.Join(dir, "replacement")
	if err := os.WriteFile(path, []byte("ignored/**\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacement, []byte("ignored/**\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := readBoundedOptionalFileWithHook(path, 1024, func() {
		if removeErr := os.Remove(path); removeErr != nil {
			t.Fatal(removeErr)
		}
		if renameErr := os.Rename(replacement, path); renameErr != nil {
			t.Fatal(renameErr)
		}
	})
	if err == nil || !strings.Contains(err.Error(), "changed while it was read") {
		t.Fatalf("replacement read error = %v, want stable-snapshot refusal", err)
	}
}

func TestReadBoundedOptionalFileRejectsReplacementAfterVerificationRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exclude")
	replacement := filepath.Join(dir, "replacement")
	if err := os.WriteFile(path, []byte("ignored/**\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacement, []byte("ignored/**\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := readBoundedOptionalFileWithHooks(path, 1024, nil, func() {
		if removeErr := os.Remove(path); removeErr != nil {
			t.Fatal(removeErr)
		}
		if renameErr := os.Rename(replacement, path); renameErr != nil {
			t.Fatal(renameErr)
		}
	})
	if err == nil || !strings.Contains(err.Error(), "changed while it was read") {
		t.Fatalf("replacement read error = %v, want final pathname refusal", err)
	}
}

func TestReadBoundedOptionalFileRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "metadata")
	if err := os.WriteFile(target, []byte("ignored/**\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	if _, err := readBoundedOptionalFile(link, 1024); err == nil {
		t.Fatal("symlink metadata read succeeded, want no-follow refusal")
	}
}

func TestReadBoundedOptionalFileHonorsPreCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readBoundedOptionalFileContext(ctx, filepath.Join(t.TempDir(), "metadata"), 1024); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled metadata read error = %v, want context.Canceled", err)
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
