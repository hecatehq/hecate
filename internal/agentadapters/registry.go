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

	"github.com/hecate/agent-runtime/internal/agentcontrols"
)

const (
	StatusAvailable = "available"
	StatusMissing   = "missing"
)

// ErrLaunchModelRequired means Hecate owns the model launch flag for an
// adapter and the operator has not selected a concrete model yet.
var ErrLaunchModelRequired = errors.New("launch model required")

// adapterDiscoveryOverrideEnv is intentionally narrower than
// adapterDevOverrideEnv: keep it for backend tests that only need catalog
// states, while UI/dev smoke fixtures should use the dev override so catalog
// and probe visuals stay aligned.
const adapterDiscoveryOverrideEnv = "HECATE_AGENT_ADAPTER_DISCOVERY_OVERRIDES"
const adapterDevOverrideEnv = "HECATE_AGENT_ADAPTER_DEV_OVERRIDES"

const (
	adapterDevOverrideMissing      = "missing"
	adapterDevOverrideAvailable    = "available"
	adapterDevOverrideReady        = "ready"
	adapterDevOverrideAuthRequired = "auth_required"
	adapterDevOverrideBilling      = "billing"
	adapterDevOverrideAppMissing   = "app_missing"
	adapterDevOverrideError        = "error"
)

const (
	AuthStatusOK              = "ok"
	AuthStatusUnauthenticated = "unauthenticated"
	AuthStatusBilling         = "billing"
	AuthStatusUnknown         = "unknown"
)

type Adapter struct {
	ID               string
	Name             string
	Command          string
	Args             []string
	CandidatePaths   []string
	Managed          ManagedLauncher
	LaunchSuffixArgs []string
	LaunchModel      LaunchModelConfig
	LaunchOptions    []LaunchSelectConfig
	Kind             string
	Description      string
	CostMode         string
	DocsURL          string
	SupportedRange   string
}

type LaunchModelConfig struct {
	ConfigID       string
	ArgTemplate    []string
	ListArgs       []string
	FallbackModels []LaunchModel
}

type LaunchModel struct {
	ID          string
	Name        string
	Description string
}

type LaunchSelectConfig struct {
	ConfigID         string
	Name             string
	Description      string
	Category         string
	Default          string
	UnsetValue       string
	UnsetName        string
	UnsetDescription string
	ArgTemplate      []string
	Options          []LaunchSelectOption
}

type LaunchSelectOption struct {
	ID          string
	Name        string
	Description string
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
	Available           bool
	Status              string
	Path                string
	Error               string
	Version             string
	VersionOutsideRange bool
	AuthStatus          string
	AuthError           string
	ClaudeCodeCLI       SetupCommandStatus
}

type LookupFunc func(file string) (string, error)

type RunRequest struct {
	SessionID               string
	AdapterID               string
	Workspace               string
	PreviousNativeSessionID string
	Prompt                  string
	ConfigOptions           []agentcontrols.ConfigOption
	Timeout                 time.Duration
	MaxOutputBytes          int64
	OnOutput                func(string)
	OnActivity              func(Activity)
}

type PrepareSessionRequest struct {
	SessionID               string
	AdapterID               string
	Workspace               string
	PreviousNativeSessionID string
	ConfigOptions           []agentcontrols.ConfigOption
}

type PrepareSessionResult struct {
	Adapter         Adapter
	DriverKind      string
	NativeSessionID string
	SessionStarted  bool
	SessionResumed  bool
	SessionRecovery string
	ConfigOptions   []agentcontrols.ConfigOption
}

type SetSessionConfigOptionRequest struct {
	SessionID string
	ConfigID  string
	Value     string
	BoolValue *bool
}

type SetSessionConfigOptionResult struct {
	ConfigOptions []agentcontrols.ConfigOption
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
	ConfigOptions   []agentcontrols.ConfigOption
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
			Kind:           "acp",
			Description:    "Run Codex through its ACP adapter as a long-lived external coding-agent session supervised by Hecate.",
			CostMode:       "external",
			DocsURL:        "https://github.com/zed-industries/codex-acp",
			SupportedRange: ">=0.1.0",
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
			Kind:           "acp",
			Description:    "Run Claude Agent through ACP as a long-lived external coding-agent session supervised by Hecate.",
			CostMode:       "external",
			DocsURL:        "https://github.com/agentclientprotocol/claude-agent-acp",
			SupportedRange: ">=0.1.0",
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
			Kind:           "acp",
			Description:    "Run Cursor Agent through ACP as a long-lived external coding-agent session supervised by Hecate.",
			CostMode:       "external",
			DocsURL:        "https://cursor.com/cli",
			SupportedRange: ">=0.1.0",
		},
		{
			ID:               "grok_build",
			Name:             "Grok Build",
			Command:          "grok",
			Args:             []string{"agent"},
			LaunchSuffixArgs: []string{"stdio"},
			CandidatePaths: []string{
				"${HOME}/.local/bin/grok",
				"/opt/homebrew/bin/grok",
				"/usr/local/bin/grok",
			},
			LaunchModel: LaunchModelConfig{
				ConfigID:    "model",
				ArgTemplate: []string{"--model", "{model}"},
				ListArgs:    []string{"models"},
			},
			LaunchOptions: []LaunchSelectConfig{
				{
					ConfigID:         "reasoning_effort",
					Name:             "Reasoning",
					Description:      "Reasoning effort passed to Grok Build when Hecate starts or restarts it.",
					Category:         "thought_level",
					UnsetValue:       "__hecate_no_reasoning_selected__",
					UnsetName:        "Pick reasoning",
					UnsetDescription: "Use Grok Build's default reasoning effort.",
					ArgTemplate:      []string{"--reasoning-effort", "{reasoning_effort}"},
					Options: []LaunchSelectOption{
						{ID: "low", Name: "Low"},
						{ID: "medium", Name: "Medium"},
						{ID: "high", Name: "High"},
						{ID: "xhigh", Name: "XHigh"},
						{ID: "max", Name: "Max"},
					},
				},
			},
			Kind:           "acp",
			Description:    "Run Grok Build through its ACP mode as a long-lived external coding-agent session supervised by Hecate.",
			CostMode:       "external",
			DocsURL:        "https://docs.x.ai/build/cli/headless-scripting#acp",
			SupportedRange: ">=0.1.0",
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
		out = append(out, statusForAdapter(ctx, item, lookup))
	}
	return out
}

func StatusForAdapter(ctx context.Context, id string, lookup LookupFunc) (Status, bool) {
	if lookup == nil {
		lookup = exec.LookPath
	}
	for _, item := range BuiltIns() {
		if item.ID != strings.TrimSpace(id) {
			continue
		}
		return statusForAdapter(ctx, item, lookup), true
	}
	return Status{}, false
}

func statusForAdapter(ctx context.Context, item Adapter, lookup LookupFunc) Status {
	status := Status{
		Adapter:    item,
		Status:     StatusMissing,
		AuthStatus: AuthStatusUnknown,
	}
	if item.ID == "claude_code" {
		status.ClaudeCodeCLI = DetectClaudeCodeCLI(lookup)
	}
	if err := ctx.Err(); err != nil {
		status.Error = err.Error()
		return status
	}
	if override, ok := adapterDevOverride(item.ID); ok {
		return applyAdapterDevOverride(status, override)
	}
	if override, ok := adapterDiscoveryOverride(item.ID); ok {
		return applyAdapterDiscoveryOverride(status, override)
	}
	path, err := resolveExecutableForStatus(item, lookup)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.Available = true
	status.Status = StatusAvailable
	status.Path = path
	if shouldProbeVersionForStatus(item, path, lookup) {
		v := DetectVersion(ctx, path)
		status.Version = v
		status.VersionOutsideRange = !satisfiesRange(v, item.SupportedRange)
	}
	status.AuthStatus, status.AuthError = DetectAuthStatus(item)
	return status
}

func shouldProbeVersionForStatus(adapter Adapter, path string, lookup LookupFunc) bool {
	if !shouldProbeVersion(path) {
		return false
	}
	if adapter.Managed.Package == "" {
		return true
	}
	planned, err := plannedManagedLauncher(adapter, lookup)
	if err != nil {
		return true
	}
	return filepath.Clean(planned) != filepath.Clean(path)
}

func shouldProbeVersion(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(filepath.Clean(path))
	return err == nil && !info.IsDir()
}

func adapterDiscoveryOverride(adapterID string) (string, bool) {
	return adapterOverride(adapterDiscoveryOverrideEnv, adapterID, normalizeAdapterDiscoveryOverride)
}

func adapterDevOverride(adapterID string) (string, bool) {
	return adapterOverride(adapterDevOverrideEnv, adapterID, normalizeAdapterDevOverride)
}

// DevOverrideActive reports whether HECATE_AGENT_ADAPTER_DEV_OVERRIDES has
// a valid fixture for adapterID. API handlers use it to keep visual smoke-test
// state synthetic end-to-end instead of letting a probe response "correct" the
// catalog row from the real machine.
func DevOverrideActive(adapterID string) bool {
	_, ok := adapterDevOverride(adapterID)
	return ok
}

func adapterOverride(envName, adapterID string, normalize func(string) (string, bool)) (string, bool) {
	raw := strings.TrimSpace(os.Getenv(envName))
	if raw == "" {
		return "", false
	}
	var allOverride string
	for _, part := range strings.Split(raw, ",") {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.ToLower(strings.TrimSpace(value))
		if key == "" || value == "" {
			continue
		}
		normalized, ok := normalize(value)
		if !ok {
			continue
		}
		if key == adapterID {
			return normalized, true
		}
		if key == "all" {
			allOverride = normalized
		}
	}
	if allOverride != "" {
		return allOverride, true
	}
	return "", false
}

func normalizeAdapterDiscoveryOverride(value string) (string, bool) {
	switch value {
	case StatusAvailable, StatusMissing:
		return value, true
	default:
		return "", false
	}
}

func normalizeAdapterDevOverride(value string) (string, bool) {
	switch value {
	case "missing", "connector_missing", "acp_missing":
		return adapterDevOverrideMissing, true
	case "available", "unknown":
		return adapterDevOverrideAvailable, true
	case "ready", "ok", "auth_ok":
		return adapterDevOverrideReady, true
	case "auth_required", "unauthenticated", "no_auth":
		return adapterDevOverrideAuthRequired, true
	case "billing":
		return adapterDevOverrideBilling, true
	case "app_missing", "cli_missing":
		return adapterDevOverrideAppMissing, true
	case "error":
		return adapterDevOverrideError, true
	default:
		return "", false
	}
}

func applyAdapterDevOverride(status Status, override string) Status {
	status.Version = ""
	status.VersionOutsideRange = false
	status.AuthStatus = AuthStatusUnknown
	status.AuthError = ""
	status.ClaudeCodeCLI = SetupCommandStatus{}
	switch override {
	case adapterDevOverrideMissing:
		status.Available = false
		status.Status = StatusMissing
		status.Path = ""
		status.Error = "forced ACP connector missing by " + adapterDevOverrideEnv
	case adapterDevOverrideReady:
		status.Available = true
		status.Status = StatusAvailable
		status.Path = "dev-override://" + status.ID
		status.Error = "forced ready by " + adapterDevOverrideEnv
		status.AuthStatus = AuthStatusOK
	case adapterDevOverrideAuthRequired:
		status.Available = true
		status.Status = StatusAvailable
		status.Path = "dev-override://" + status.ID
		status.Error = "forced auth_required by " + adapterDevOverrideEnv
		status.AuthStatus = AuthStatusUnauthenticated
		status.AuthError = adapterSignInHint(status.Adapter)
	case adapterDevOverrideBilling:
		status.Available = true
		status.Status = StatusAvailable
		status.Path = "dev-override://" + status.ID
		status.Error = "forced billing by " + adapterDevOverrideEnv
		status.AuthStatus = AuthStatusBilling
		status.AuthError = "Billing or usage limit requires attention."
	case adapterDevOverrideAppMissing:
		status.Available = true
		status.Status = StatusAvailable
		status.Path = "dev-override://" + status.ID
		status.Error = "forced app CLI missing by " + adapterDevOverrideEnv
	case adapterDevOverrideError:
		status.Available = true
		status.Status = StatusAvailable
		status.Path = "dev-override://" + status.ID
		status.Error = "forced probe error by " + adapterDevOverrideEnv
	default:
		status.Available = true
		status.Status = StatusAvailable
		status.Path = "dev-override://" + status.ID
		status.Error = "forced available by " + adapterDevOverrideEnv
	}
	return status
}

func applyAdapterDiscoveryOverride(status Status, override string) Status {
	status.Version = ""
	status.VersionOutsideRange = false
	status.AuthStatus = AuthStatusUnknown
	status.AuthError = ""
	status.ClaudeCodeCLI = SetupCommandStatus{}
	switch override {
	case StatusAvailable:
		status.Available = true
		status.Status = StatusAvailable
		status.Path = "dev-override://" + status.ID
		status.Error = "forced available by " + adapterDiscoveryOverrideEnv
	case StatusMissing:
		status.Available = false
		status.Status = StatusMissing
		status.Path = ""
		status.Error = "forced missing by " + adapterDiscoveryOverrideEnv
	}
	return status
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

func RefreshManagedLauncher(ctx context.Context, id string, lookup LookupFunc) (Status, error) {
	adapter, ok := FindAdapter(id)
	if !ok {
		return Status{}, fmt.Errorf("unknown adapter %q", id)
	}
	if adapter.Managed.Package == "" {
		return Status{}, fmt.Errorf("adapter %q does not use a managed launcher", id)
	}
	dir, err := managedLauncherDir()
	if err != nil {
		return Status{}, err
	}
	_ = os.Remove(filepath.Join(dir, managedLauncherName(adapter.Command)))
	if _, err := ensureManagedLauncher(adapter, lookup); err != nil {
		return Status{}, err
	}
	return statusForAdapter(ctx, adapter, lookup), nil
}

func GCManagedLaunchers() (int, error) {
	dir, err := managedLauncherDir()
	if err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	keep := make(map[string]struct{})
	for _, adapter := range BuiltIns() {
		if adapter.Managed.Package != "" {
			keep[managedLauncherName(adapter.Command)] = struct{}{}
		}
	}
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if _, ok := keep[entry.Name()]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err == nil {
			removed++
		}
	}
	return removed, nil
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
		"XAI_",
		"XDG_",
		"VOLTA_",
	}
	out := make([]string, 0, len(env))
	hasXAIAPIKey := false
	providerXAIAPIKey := ""
	for _, entry := range env {
		if strings.HasPrefix(entry, "XAI_API_KEY=") && strings.TrimSpace(strings.TrimPrefix(entry, "XAI_API_KEY=")) != "" {
			hasXAIAPIKey = true
		}
		if strings.HasPrefix(entry, "PROVIDER_XAI_API_KEY=") {
			providerXAIAPIKey = strings.TrimSpace(strings.TrimPrefix(entry, "PROVIDER_XAI_API_KEY="))
		}
		for _, prefix := range allowedPrefixes {
			if strings.HasPrefix(entry, prefix) {
				out = append(out, entry)
				break
			}
		}
	}
	if !hasXAIAPIKey && providerXAIAPIKey != "" {
		out = append(out, "XAI_API_KEY="+providerXAIAPIKey)
	}
	return out
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
