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
	extra := t.TempDir()
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
	if !strings.Contains(text, `autocrlf = "input"`) || !strings.Contains(text, `ignorecase = "true"`) {
		t.Fatalf("passive config = %q, want normalized safe core settings", text)
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
