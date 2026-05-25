package gitrunner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/processrunner"
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

type recordingProcessRunner struct {
	request processrunner.Request
}

func (r *recordingProcessRunner) Run(_ context.Context, req processrunner.Request) (processrunner.Result, error) {
	r.request = req
	return processrunner.Result{}, nil
}

func (r *recordingProcessRunner) RunStreaming(ctx context.Context, req processrunner.Request, _ func(processrunner.Chunk)) (processrunner.Result, error) {
	return r.Run(ctx, req)
}
