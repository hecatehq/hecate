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
	"runtime"
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
	Args           []string
	CandidatePaths []string
	Managed        ManagedLauncher
	Kind           string
	Description    string
	CostMode       string
	DocsURL        string
}

type ManagedLauncher struct {
	Package string
	Runners []ManagedRunner
}

type ManagedRunner struct {
	Command        string
	Args           []string
	CandidatePaths []string
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
	SessionID               string
	AdapterID               string
	Workspace               string
	PreviousNativeSessionID string
	Prompt                  string
	Timeout                 time.Duration
	MaxOutputBytes          int64
	OnOutput                func(string)
	OnActivity              func(Activity)
}

type RunResult struct {
	Adapter         Adapter
	DriverKind      string
	NativeSessionID string
	SessionStarted  bool
	SessionResumed  bool
	SessionRecovery string
	Output          string
	RawOutput       string
	ExitCode        int
	StartedAt       time.Time
	CompletedAt     time.Time
	DiffStat        string
	Diff            string
	Usage           Usage
}

type Usage struct {
	ContextSize          int
	ContextUsed          int
	ReportedCostAmount   string
	ReportedCostCurrency string
}

type Activity struct {
	ID     string
	Type   string
	Status string
	Kind   string
	Title  string
	Detail string
}

func (u Usage) Empty() bool {
	return u.ContextSize == 0 && u.ContextUsed == 0 && u.ReportedCostAmount == "" && u.ReportedCostCurrency == ""
}

func BuiltIns() []Adapter {
	return []Adapter{
		{
			ID:      "codex",
			Name:    "Codex",
			Command: "codex-acp",
			CandidatePaths: []string{
				"${HOME}/.local/bin/codex-acp",
				"/opt/homebrew/bin/codex-acp",
				"/usr/local/bin/codex-acp",
			},
			Managed: ManagedLauncher{
				Package: "@zed-industries/codex-acp",
				Runners: []ManagedRunner{
					{Command: "npx", Args: []string{"-y", "@zed-industries/codex-acp"}, CandidatePaths: managedNPXCandidates()},
				},
			},
			Kind:        "acp",
			Description: "Run Codex through its ACP adapter as a long-lived external coding-agent session supervised by Hecate.",
			CostMode:    "external",
			DocsURL:     "https://github.com/zed-industries/codex-acp",
		},
		{
			ID:      "claude_code",
			Name:    "Claude Code",
			Command: "claude-agent-acp",
			CandidatePaths: []string{
				"${HOME}/.local/bin/claude-agent-acp",
				"/opt/homebrew/bin/claude-agent-acp",
				"/usr/local/bin/claude-agent-acp",
			},
			Managed: ManagedLauncher{
				Package: "@agentclientprotocol/claude-agent-acp",
				Runners: []ManagedRunner{
					{Command: "npx", Args: []string{"-y", "@agentclientprotocol/claude-agent-acp"}, CandidatePaths: managedNPXCandidates()},
				},
			},
			Kind:        "acp",
			Description: "Run Claude Agent through ACP as a long-lived external coding-agent session supervised by Hecate.",
			CostMode:    "external",
			DocsURL:     "https://github.com/agentclientprotocol/claude-agent-acp",
		},
		{
			ID:      "cursor_agent",
			Name:    "Cursor Agent",
			Command: "cursor-agent",
			Args:    []string{"acp"},
			CandidatePaths: []string{
				"${HOME}/.local/bin/cursor-agent",
				"/opt/homebrew/bin/cursor-agent",
				"/usr/local/bin/cursor-agent",
			},
			Kind:        "acp",
			Description: "Run Cursor Agent through ACP as a long-lived external coding-agent session supervised by Hecate.",
			CostMode:    "external",
			DocsURL:     "https://cursor.com/cli",
		},
	}
}

// FindAdapter returns the built-in adapter matching id (exact match,
// case-sensitive — adapter ids are stable identifiers chosen by the
// gateway, not user-typed). Second return is false when id doesn't
// match any registered adapter.
func FindAdapter(id string) (Adapter, bool) {
	for _, a := range BuiltIns() {
		if a.ID == id {
			return a, true
		}
	}
	return Adapter{}, false
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
		path, err := resolveExecutableForStatus(item, lookup)
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
	return resolveExecutableWithManaged(adapter, lookup, true)
}

func resolveExecutableForStatus(adapter Adapter, lookup LookupFunc) (string, error) {
	return resolveExecutableWithManaged(adapter, lookup, false)
}

func resolveExecutableWithManaged(adapter Adapter, lookup LookupFunc, createManaged bool) (string, error) {
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
	if adapter.Managed.Package != "" {
		var path string
		var err error
		if createManaged {
			path, err = ensureManagedLauncher(adapter, lookup)
		} else {
			path, err = plannedManagedLauncher(adapter, lookup)
		}
		if err == nil {
			return path, nil
		}
		return "", fmt.Errorf("%w; managed launcher unavailable: %v", firstErr, err)
	}
	return "", firstErr
}

func plannedManagedLauncher(adapter Adapter, lookup LookupFunc) (string, error) {
	if adapter.Managed.Package == "" {
		return "", errors.New("adapter has no managed launcher")
	}
	if _, _, err := resolveManagedRunner(adapter.Managed, lookup); err != nil {
		return "", err
	}
	dir, err := managedLauncherDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, managedLauncherName(adapter.Command)), nil
}

func ensureManagedLauncher(adapter Adapter, lookup LookupFunc) (string, error) {
	if adapter.Managed.Package == "" {
		return "", errors.New("adapter has no managed launcher")
	}
	runner, runnerPath, err := resolveManagedRunner(adapter.Managed, lookup)
	if err != nil {
		return "", err
	}
	dir, err := managedLauncherDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create managed adapter directory: %w", err)
	}
	path := filepath.Join(dir, managedLauncherName(adapter.Command))
	content := managedLauncherContent(runnerPath, runner.Args)
	if existing, err := os.ReadFile(path); err == nil && string(existing) == content {
		return path, nil
	}
	mode := os.FileMode(0o755)
	if runtime.GOOS == "windows" {
		mode = 0o644
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		return "", fmt.Errorf("write managed adapter launcher: %w", err)
	}
	return path, nil
}

func resolveManagedRunner(managed ManagedLauncher, lookup LookupFunc) (ManagedRunner, string, error) {
	if lookup == nil {
		lookup = exec.LookPath
	}
	var errorsText []string
	for _, runner := range managed.Runners {
		if strings.TrimSpace(runner.Command) == "" {
			continue
		}
		path, err := lookup(runner.Command)
		if err == nil {
			return runner, path, nil
		}
		for _, candidate := range runner.CandidatePaths {
			path := expandPath(candidate)
			if path == "" {
				continue
			}
			info, statErr := os.Stat(path)
			if statErr != nil || info.IsDir() || info.Mode()&0o111 == 0 {
				continue
			}
			return runner, path, nil
		}
		errorsText = append(errorsText, fmt.Sprintf("%s: %v", runner.Command, err))
	}
	if len(errorsText) == 0 {
		return ManagedRunner{}, "", fmt.Errorf("no runner configured for managed adapter package %s", managed.Package)
	}
	return ManagedRunner{}, "", fmt.Errorf("no local package runner found for %s (%s)", managed.Package, strings.Join(errorsText, "; "))
}

func managedLauncherDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("HECATE_AGENT_ADAPTERS_DIR")); dir != "" {
		return filepath.Abs(filepath.Clean(dir))
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache directory: %w", err)
	}
	return filepath.Join(dir, "hecate", "agent-adapters"), nil
}

func managedNPXCandidates() []string {
	return []string{
		"${HOME}/.volta/bin/npx",
		"${HOME}/.local/share/mise/shims/npx",
		"${HOME}/.asdf/shims/npx",
		"/opt/homebrew/bin/npx",
		"/usr/local/bin/npx",
		"/usr/bin/npx",
	}
}

func managedLauncherName(command string) string {
	if runtime.GOOS == "windows" {
		return command + ".cmd"
	}
	return command
}

func managedLauncherContent(runnerPath string, args []string) string {
	if runtime.GOOS == "windows" {
		parts := []string{windowsQuote(runnerPath)}
		for _, arg := range args {
			parts = append(parts, windowsQuote(arg))
		}
		parts = append(parts, "%*")
		return "@echo off\r\n" + strings.Join(parts, " ") + "\r\n"
	}
	parts := []string{"exec", shellQuote(runnerPath)}
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}
	parts = append(parts, "\"$@\"")
	return "#!/bin/sh\n" + strings.Join(parts, " ") + "\n"
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func windowsQuote(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

func Run(ctx context.Context, req RunRequest) (RunResult, error) {
	return NewSessionManager().Run(ctx, req)
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
	if strings.ContainsRune(path, 0) {
		return "", errors.New("workspace path contains a NUL byte")
	}
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("workspace %q is not accessible: %w", abs, err)
	}
	resolved = filepath.Clean(resolved)
	root, err := os.OpenRoot(resolved)
	if err != nil {
		return "", fmt.Errorf("workspace %q is not accessible: %w", resolved, err)
	}
	defer root.Close()
	info, err := root.Stat(".")
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

func sanitizedEnvForAdapter(adapterID string, env []string) []string {
	out := sanitizedEnv(env)
	if adapterID != "claude_code" {
		return out
	}
	filtered := out[:0]
	for _, entry := range out {
		// Claude Code subscription auth is file-backed. Forwarding Anthropic API
		// variables makes the ACP runner prefer Console credits over /login.
		if strings.HasPrefix(entry, "ANTHROPIC_") {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func captureGitDiff(ctx context.Context, workspace string, maxBytes int64) (string, string) {
	diffCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if strings.TrimSpace(runGitCapture(diffCtx, workspace, 1024, "rev-parse", "--is-inside-work-tree")) != "true" {
		return "", ""
	}
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
