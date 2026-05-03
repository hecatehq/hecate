package agentadapters

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	StatusAvailable = "available"
	StatusMissing   = "missing"
)

type Adapter struct {
	ID             string
	Name           string
	Command        string
	CandidatePaths []string
	Kind           string
	Description    string
	CostMode       string
	DocsURL        string
}

type Status struct {
	Adapter
	Available bool
	Status    string
	Path      string
	Error     string
}

type LookupFunc func(file string) (string, error)

type RunRequest struct {
	AdapterID      string
	Workspace      string
	Prompt         string
	Timeout        time.Duration
	MaxOutputBytes int64
	OnOutput       func(string)
}

type RunResult struct {
	Adapter     Adapter
	Output      string
	RawOutput   string
	ExitCode    int
	StartedAt   time.Time
	CompletedAt time.Time
	DiffStat    string
	Diff        string
}

func BuiltIns() []Adapter {
	return []Adapter{
		{
			ID:      "codex",
			Name:    "Codex",
			Command: "codex",
			CandidatePaths: []string{
				"/Applications/Codex.app/Contents/Resources/codex",
				"${HOME}/.local/bin/codex",
				"/opt/homebrew/bin/codex",
				"/usr/local/bin/codex",
			},
			Kind:        "process",
			Description: "Run Codex CLI as an external coding-agent process supervised by Hecate.",
			CostMode:    "external",
			DocsURL:     "https://github.com/openai/codex",
		},
		{
			ID:      "claude_code",
			Name:    "Claude Code",
			Command: "claude",
			CandidatePaths: []string{
				"${HOME}/.volta/bin/claude",
				"${HOME}/.local/bin/claude",
				"/opt/homebrew/bin/claude",
				"/usr/local/bin/claude",
			},
			Kind:        "process",
			Description: "Run Claude Code as an external coding-agent process supervised by Hecate.",
			CostMode:    "external",
			DocsURL:     "https://docs.anthropic.com/claude-code",
		},
		{
			ID:      "cursor_agent",
			Name:    "Cursor Agent",
			Command: "cursor-agent",
			CandidatePaths: []string{
				"${HOME}/.local/bin/cursor-agent",
				"/opt/homebrew/bin/cursor-agent",
				"/usr/local/bin/cursor-agent",
			},
			Kind:        "process",
			Description: "Run Cursor Agent CLI as an external coding-agent process supervised by Hecate.",
			CostMode:    "external",
			DocsURL:     "https://cursor.com/cli",
		},
	}
}

func List(ctx context.Context) []Status {
	return ListWithLookup(ctx, exec.LookPath)
}

func ListWithLookup(ctx context.Context, lookup LookupFunc) []Status {
	if lookup == nil {
		lookup = exec.LookPath
	}

	items := BuiltIns()
	out := make([]Status, 0, len(items))
	for _, item := range items {
		status := Status{
			Adapter: item,
			Status:  StatusMissing,
		}
		if err := ctx.Err(); err != nil {
			status.Error = err.Error()
			out = append(out, status)
			continue
		}
		path, err := resolveExecutable(item, lookup)
		if err != nil {
			status.Error = err.Error()
			out = append(out, status)
			continue
		}
		status.Available = true
		status.Status = StatusAvailable
		status.Path = path
		out = append(out, status)
	}
	return out
}

func resolveExecutable(adapter Adapter, lookup LookupFunc) (string, error) {
	if lookup == nil {
		lookup = exec.LookPath
	}
	path, err := lookup(adapter.Command)
	if err == nil {
		return path, nil
	}
	var firstErr error = err
	for _, candidate := range adapter.CandidatePaths {
		path := expandPath(candidate)
		if path == "" {
			continue
		}
		info, statErr := os.Stat(path)
		if statErr != nil {
			continue
		}
		if info.IsDir() {
			continue
		}
		if info.Mode()&0o111 == 0 {
			continue
		}
		return path, nil
	}
	return "", firstErr
}

func Run(ctx context.Context, req RunRequest) (RunResult, error) {
	adapter, ok := BuiltInByID(req.AdapterID)
	if !ok {
		return RunResult{}, fmt.Errorf("agent adapter %q not found", req.AdapterID)
	}
	return RunAdapter(ctx, adapter, req)
}

func BuiltInByID(id string) (Adapter, bool) {
	id = strings.TrimSpace(id)
	for _, item := range BuiltIns() {
		if item.ID == id {
			return item, true
		}
	}
	return Adapter{}, false
}

func RunAdapter(ctx context.Context, adapter Adapter, req RunRequest) (RunResult, error) {
	workspace, err := ValidateWorkspace(req.Workspace)
	if err != nil {
		return RunResult{}, err
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return RunResult{}, errors.New("prompt is required")
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	maxOutput := req.MaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = 1024 * 1024
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	started := time.Now().UTC()
	command, args, err := commandForAdapter(adapter, workspace, prompt)
	if err != nil {
		return RunResult{}, err
	}
	cmd := exec.CommandContext(runCtx, command, args...)
	cmd.Dir = workspace
	cmd.Env = sanitizedEnv(os.Environ())

	var out limitedBuffer
	out.limit = maxOutput
	out.onWrite = req.OnOutput
	cmd.Stdout = &out
	cmd.Stderr = &out
	runErr := cmd.Run()
	completed := time.Now().UTC()

	exitCode := 0
	if runErr != nil {
		exitCode = 1
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			runErr = fmt.Errorf("agent adapter timed out after %s", timeout)
		} else if errors.Is(runCtx.Err(), context.Canceled) {
			runErr = context.Canceled
		}
	}
	if out.truncated {
		if runErr == nil {
			runErr = fmt.Errorf("agent adapter output exceeded %d bytes", maxOutput)
		} else {
			runErr = fmt.Errorf("%w; output exceeded %d bytes", runErr, maxOutput)
		}
	}

	diffStat, diff := captureGitDiff(ctx, workspace, maxOutput)
	return RunResult{
		Adapter:     adapter,
		Output:      normalizeOutput(adapter.ID, out.String()),
		RawOutput:   out.String(),
		ExitCode:    exitCode,
		StartedAt:   started,
		CompletedAt: completed,
		DiffStat:    diffStat,
		Diff:        diff,
	}, runErr
}

func commandForAdapter(adapter Adapter, workspace string, prompt string) (string, []string, error) {
	command, err := resolveExecutable(adapter, exec.LookPath)
	if err != nil {
		return "", nil, err
	}
	switch adapter.ID {
	case "codex":
		return command, []string{
			"--ask-for-approval", "never",
			"exec",
			"--cd", workspace,
			"--sandbox", "workspace-write",
			"--json",
			prompt,
		}, nil
	case "claude_code":
		return command, []string{
			"-p",
			"--permission-mode", "acceptEdits",
			"--output-format", "text",
			prompt,
		}, nil
	case "cursor_agent":
		return command, []string{
			"--print",
			"--output-format", "text",
			"--workspace", workspace,
			"--trust",
			"--force",
			prompt,
		}, nil
	default:
		return "", nil, fmt.Errorf("agent adapter %q has no process invocation", adapter.ID)
	}
}

func expandPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "${HOME}/") {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		return filepath.Join(home, strings.TrimPrefix(path, "${HOME}/"))
	}
	return path
}

func ValidateWorkspace(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("workspace is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("workspace %q is not accessible: %w", abs, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("workspace %q is not accessible: %w", resolved, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace %q is not a directory", resolved)
	}
	return resolved, nil
}

func sanitizedEnv(env []string) []string {
	allowedPrefixes := []string{
		"ANTHROPIC_",
		"CODEX_",
		"CLAUDE_",
		"CURSOR_",
		"OPENAI_",
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

func captureGitDiff(ctx context.Context, workspace string, maxBytes int64) (string, string) {
	if _, err := os.Stat(filepath.Join(workspace, ".git")); err != nil {
		return "", ""
	}
	diffCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return runGitCapture(diffCtx, workspace, maxBytes, "diff", "--stat"), runGitCapture(diffCtx, workspace, maxBytes, "diff", "--no-ext-diff", "--binary")
}

func runGitCapture(ctx context.Context, workspace string, maxBytes int64, args ...string) string {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workspace
	cmd.Env = sanitizedEnv(os.Environ())
	var out limitedBuffer
	out.limit = maxBytes
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}

type limitedBuffer struct {
	bytes.Buffer
	mu        sync.Mutex
	limit     int64
	truncated bool
	onWrite   func(string)
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	accepted := p
	if b.limit <= 0 {
		n, err := b.Buffer.Write(p)
		if b.onWrite != nil && n > 0 {
			b.onWrite(string(p[:n]))
		}
		return n, err
	}
	remaining := b.limit - int64(b.Buffer.Len())
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if int64(len(p)) > remaining {
		b.truncated = true
		accepted = p[:remaining]
		_, _ = b.Buffer.Write(accepted)
		if b.onWrite != nil && len(accepted) > 0 {
			b.onWrite(string(accepted))
		}
		return len(p), nil
	}
	n, err := b.Buffer.Write(accepted)
	if b.onWrite != nil && n > 0 {
		b.onWrite(string(accepted[:n]))
	}
	return n, err
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}
