package gitrunner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/processrunner"
	"github.com/hecatehq/hecate/internal/sandbox"
)

const command = "git"

type Result = processrunner.Result

type Runner interface {
	Run(ctx context.Context, workspace string, args ...string) (Result, error)
	CurrentRef(ctx context.Context, workspace string) string
	IsWorkTree(ctx context.Context, workspace string) bool
	Worktrees(ctx context.Context, workspace string) ([]Worktree, error)
	Diff(ctx context.Context, workspace string, maxBytes int64) (string, string)
	Restore(ctx context.Context, workspace string, paths []string) (Result, error)
	Clone(ctx context.Context, sourcePath, workspacePath string) (Result, error)
}

type Worktree struct {
	Path     string
	Head     string
	Branch   string
	Detached bool
	Bare     bool
}

type LocalRunner struct {
	Process       processrunner.Runner
	Env           []string
	ReadOnlyPaths []string
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

func (r *LocalRunner) Restore(ctx context.Context, workspace string, paths []string) (Result, error) {
	cleanedPaths := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path != "" {
			cleanedPaths = append(cleanedPaths, path)
		}
	}
	if len(cleanedPaths) == 0 {
		return Result{ExitCode: -1}, errors.New("at least one path is required")
	}
	args := append([]string{"restore", "--"}, cleanedPaths...)
	return r.Run(ctx, workspace, args...)
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
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
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
