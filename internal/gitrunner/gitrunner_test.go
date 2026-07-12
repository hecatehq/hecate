package gitrunner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
	runner := &LocalRunner{Process: process, Env: []string{"PATH=/bin"}}

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
	if got := strings.Join(argv[len(argv)-3:], " "); got != "git status --porcelain=v1" {
		t.Fatalf("wrapped argv tail = %q, want fixed Git argv", got)
	}
	if process.request.MaxStdoutBytes != 1024 || process.request.MaxStderrBytes != 1024 {
		t.Fatalf("output limits = (%d, %d), want (1024, 1024)", process.request.MaxStdoutBytes, process.request.MaxStderrBytes)
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
