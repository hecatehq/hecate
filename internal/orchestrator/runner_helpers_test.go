package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hecate/agent-runtime/pkg/types"
)

func TestFirstNonEmptyTrimsAndPicksFirst(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"all empty", []string{"", "  ", "\t"}, ""},
		{"first wins", []string{"first", "second"}, "first"},
		{"trims whitespace", []string{"  trimmed  "}, "trimmed"},
		{"skips empty until first non-empty", []string{"", "  ", "third"}, "third"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := firstNonEmpty(tc.in...); got != tc.want {
				t.Errorf("firstNonEmpty(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestMaxIntFallsBackOnNonPositive(t *testing.T) {
	cases := []struct {
		value, fallback, want int
	}{
		{0, 7, 7},
		{-1, 7, 7},
		{12, 7, 12},
	}
	for _, tc := range cases {
		if got := maxInt(tc.value, tc.fallback); got != tc.want {
			t.Errorf("maxInt(%d, %d) = %d, want %d", tc.value, tc.fallback, got, tc.want)
		}
	}
}

func TestDefaultResourceIDIsUniqueUnderConcurrency(t *testing.T) {
	const workers = 32
	const perWorker = 512

	ids := make(chan string, workers*perWorker)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				ids <- defaultResourceID("artifact")
			}
		}()
	}
	wg.Wait()
	close(ids)

	seen := make(map[string]struct{}, workers*perWorker)
	for id := range ids {
		if !strings.HasPrefix(id, "artifact_") {
			t.Fatalf("id = %q, want artifact_ prefix", id)
		}
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicate id generated: %s", id)
		}
		seen[id] = struct{}{}
	}
	if got, want := len(seen), workers*perWorker; got != want {
		t.Fatalf("ids = %d, want %d", got, want)
	}
}

func TestFindOldestRunStartIgnoresZeroTimes(t *testing.T) {
	t1 := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	t2 := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	t3 := time.Date(2025, 1, 3, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		runs []types.TaskRun
		want time.Time
	}{
		{"empty returns zero", nil, time.Time{}},
		{"all zero returns zero", []types.TaskRun{{}, {}}, time.Time{}},
		{"picks earliest non-zero", []types.TaskRun{{StartedAt: t2}, {StartedAt: t1}, {StartedAt: t3}}, t1},
		{"skips zero entries", []types.TaskRun{{}, {StartedAt: t2}, {}}, t2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := findOldestRunStart(tc.runs)
			if !got.Equal(tc.want) {
				t.Errorf("findOldestRunStart = %v, want %v", got, tc.want)
			}
		})
	}
}

// runnerWithPolicies builds a minimally-initialized Runner that bypasses
// NewRunner — we only need the policies map and executor pointers for the
// helpers under test, not the goroutines and queue NewRunner spins up.
func runnerWithPolicies(policies ...string) *Runner {
	r := &Runner{policies: make(map[string]struct{})}
	for _, p := range policies {
		r.policies[p] = struct{}{}
	}
	return r
}

func TestApprovalSpecForTask(t *testing.T) {
	cases := []struct {
		name       string
		policies   []string
		task       types.Task
		wantKind   string
		wantReason bool
	}{
		{
			name:       "shell policy + matching task → shell_command",
			policies:   []string{"shell_exec"},
			task:       types.Task{ExecutionKind: "shell", ShellCommand: "ls"},
			wantKind:   "shell_command",
			wantReason: true,
		},
		{
			name:     "shell task without policy → no approval",
			policies: nil,
			task:     types.Task{ExecutionKind: "shell", ShellCommand: "ls"},
			wantKind: "",
		},
		{
			name:       "git policy + matching task → git_exec",
			policies:   []string{"git_exec"},
			task:       types.Task{ExecutionKind: "git", GitCommand: "status"},
			wantKind:   "git_exec",
			wantReason: true,
		},
		{
			name:       "file policy + matching task → file_write",
			policies:   []string{"file_write"},
			task:       types.Task{ExecutionKind: "file", FilePath: "/tmp/x"},
			wantKind:   "file_write",
			wantReason: true,
		},
		{
			name:       "network egress policy fires regardless of execution kind",
			policies:   []string{"network_egress"},
			task:       types.Task{ExecutionKind: "shell", ShellCommand: "ls", SandboxNetwork: true},
			wantKind:   "network_egress",
			wantReason: true,
		},
		{
			name:     "shell task with empty command does not require approval",
			policies: []string{"shell_exec"},
			task:     types.Task{ExecutionKind: "shell", ShellCommand: "  "},
			wantKind: "",
		},
		{
			name:       "all_tools + shell task → shell_command",
			policies:   []string{"all_tools"},
			task:       types.Task{ExecutionKind: "shell", ShellCommand: "ls"},
			wantKind:   "shell_command",
			wantReason: true,
		},
		{
			name:       "all_tools + git task → git_exec",
			policies:   []string{"all_tools"},
			task:       types.Task{ExecutionKind: "git", GitCommand: "status"},
			wantKind:   "git_exec",
			wantReason: true,
		},
		{
			name:       "all_tools + file task → file_write",
			policies:   []string{"all_tools"},
			task:       types.Task{ExecutionKind: "file", FilePath: "/tmp/x"},
			wantKind:   "file_write",
			wantReason: true,
		},
		{
			// shell check precedes network check in approvalSpecForTask, so
			// shell_command fires first even when all_tools enables both gates.
			name:       "all_tools + shell+network task → shell_command (shell checked first)",
			policies:   []string{"all_tools"},
			task:       types.Task{ExecutionKind: "shell", ShellCommand: "ls", SandboxNetwork: true},
			wantKind:   "shell_command",
			wantReason: true,
		},
		{
			// When execution kind is not shell/git/file, network gate fires.
			name:       "all_tools + network-only task → network_egress",
			policies:   []string{"all_tools"},
			task:       types.Task{ExecutionKind: "agent_loop", SandboxNetwork: true},
			wantKind:   "network_egress",
			wantReason: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := runnerWithPolicies(tc.policies...)
			kind, reason := r.approvalSpecForTask(tc.task)
			if kind != tc.wantKind {
				t.Errorf("kind = %q, want %q", kind, tc.wantKind)
			}
			if (reason != "") != tc.wantReason {
				t.Errorf("reason presence = %v, want %v (got %q)", reason != "", tc.wantReason, reason)
			}
			if got := r.approvalRequiredForTask(tc.task); got != tc.wantReason {
				t.Errorf("approvalRequiredForTask = %v, want %v", got, tc.wantReason)
			}
		})
	}
}

func TestHasPolicy(t *testing.T) {
	r := runnerWithPolicies("shell_exec", "git_exec")
	if !r.hasPolicy("shell_exec") {
		t.Error("hasPolicy(shell_exec) = false, want true")
	}
	if r.hasPolicy("file_write") {
		t.Error("hasPolicy(file_write) = true, want false")
	}
}

func TestAgentLoopGatedTools(t *testing.T) {
	cases := []struct {
		name     string
		policies []string
		want     []string
	}{
		{
			name:     "all_tools short-circuits to full set",
			policies: []string{"all_tools"},
			want:     []string{"file_edit", "file_write", "git_exec", "http_request", "list_dir", "read_file", "shell_exec"},
		},
		{
			name:     "file_write gates write and exact edit tools",
			policies: []string{"file_write"},
			want:     []string{"file_edit", "file_write"},
		},
		{
			name:     "read_file adds read_file tool",
			policies: []string{"read_file"},
			want:     []string{"read_file"},
		},
		{
			name:     "network_egress maps to http_request",
			policies: []string{"network_egress"},
			want:     []string{"http_request"},
		},
		{
			name:     "shell_exec and git_exec pass through",
			policies: []string{"shell_exec", "git_exec"},
			want:     []string{"git_exec", "shell_exec"},
		},
		{
			name:     "unknown policy produces no tools",
			policies: []string{},
			want:     []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pm := make(map[string]struct{})
			for _, p := range tc.policies {
				pm[p] = struct{}{}
			}
			got := agentLoopGatedTools(pm)
			sort.Strings(got)
			want := tc.want
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("agentLoopGatedTools = %v, want %v", got, want)
			}
		})
	}
}

func TestGitSummaryArtifactCapturesChangedFiles(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "main.go")
	runGit(t, dir, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &Runner{}
	artifact, ok := r.gitSummaryArtifact(context.Background(), types.Task{ID: "task-1"}, types.TaskRun{ID: "run-1", WorkspacePath: dir}, "req-1", "trace-1")
	if !ok {
		t.Fatal("git summary artifact missing")
	}
	if artifact.Kind != "git_summary" || artifact.MimeType != "application/json" {
		t.Fatalf("artifact shape = %+v", artifact)
	}
	var payload gitSummaryArtifactPayload
	if err := json.Unmarshal([]byte(artifact.ContentText), &payload); err != nil {
		t.Fatalf("Unmarshal payload: %v\n%s", err, artifact.ContentText)
	}
	if len(payload.Files) != 2 {
		t.Fatalf("files = %+v, want modified and untracked", payload.Files)
	}
	seen := map[string]string{}
	for _, file := range payload.Files {
		seen[file.Path] = file.Status
	}
	if seen["main.go"] != "M" {
		t.Fatalf("main.go status = %q, want M; payload=%+v", seen["main.go"], payload.Files)
	}
	if seen["new.txt"] != "??" {
		t.Fatalf("new.txt status = %q, want ??; payload=%+v", seen["new.txt"], payload.Files)
	}
	if !strings.Contains(payload.DiffStat, "main.go") {
		t.Fatalf("diff_stat missing tracked change: %q", payload.DiffStat)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}

// stubExec is a minimal Executor implementation used as a sentinel so
// executorForTask's branch selection can be observed without invoking real
// sandbox code.
type stubExec struct{ name string }

func (s *stubExec) Execute(context.Context, ExecutionSpec) (*ExecutionResult, error) {
	return nil, nil
}

func TestExecutorForTaskRouting(t *testing.T) {
	exec := &stubExec{name: "exec"}
	shell := &stubExec{name: "shell"}
	file := &stubExec{name: "file"}
	git := &stubExec{name: "git"}
	agent := &stubExec{name: "agent"}

	r := &Runner{exec: exec, shell: shell, file: file, git: git, agent: agent}

	cases := []struct {
		name string
		task types.Task
		want Executor
	}{
		{"agent_loop routes to agent", types.Task{ExecutionKind: "agent_loop"}, agent},
		{"shell with command routes to shell", types.Task{ExecutionKind: "shell", ShellCommand: "ls"}, shell},
		{"shell with empty command falls through to default exec", types.Task{ExecutionKind: "shell"}, exec},
		{"file with path routes to file", types.Task{ExecutionKind: "file", FilePath: "/tmp/x"}, file},
		{"git with command routes to git", types.Task{ExecutionKind: "git", GitCommand: "status"}, git},
		{"unknown kind falls through to default exec", types.Task{ExecutionKind: "weird"}, exec},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := r.executorForTask(tc.task)
			if got != tc.want {
				t.Errorf("executorForTask kind=%q got=%v, want %v", tc.task.ExecutionKind, got, tc.want)
			}
		})
	}
}
