package gitrunner

import (
	"context"
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

func TestLocalRunner_Restore(t *testing.T) {
	dir := initRepo(t)
	path := filepath.Join(dir, "README.md")
	if err := os.WriteFile(path, []byte("changed\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runner := NewLocalRunner()

	if _, err := runner.Restore(context.Background(), dir, []string{"README.md"}); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if got := string(data); got != "hello\n" {
		t.Fatalf("file = %q, want original", got)
	}
}

func TestLocalRunner_RestoreUsesPathspecSeparator(t *testing.T) {
	dir := t.TempDir()
	process := &recordingProcessRunner{}
	runner := &LocalRunner{Process: process}

	if _, err := runner.Restore(context.Background(), dir, []string{"README.md", "docs/guide.md"}); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	got := strings.Join(process.request.Args, "\x00")
	want := strings.Join([]string{"restore", "--", "README.md", "docs/guide.md"}, "\x00")
	if got != want {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

func TestLocalRunner_RestoreRejectsEmptyPathList(t *testing.T) {
	runner := NewLocalRunner()

	_, err := runner.Restore(context.Background(), t.TempDir(), []string{" ", ""})

	if err == nil || !strings.Contains(err.Error(), "at least one path is required") {
		t.Fatalf("error = %v, want path required", err)
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
	repo := filepath.Join(t.TempDir(), "repo \n")
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
		t.Fatalf("workTree = %q, want whitespace-preserving %q", view.workTree, repo)
	}
	if _, err := view.RunLimited(context.Background(), 4096, "status", "--porcelain=v1", "-b", "--", "."); err != nil {
		t.Fatalf("passive git status: %v", err)
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
