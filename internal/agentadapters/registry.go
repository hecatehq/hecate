package agentadapters

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/hecatehq/hecate/internal/agentcontrols"
	"github.com/hecatehq/hecate/internal/gitrunner"
	"github.com/hecatehq/hecate/internal/remoteruntime"
)

const (
	StatusAvailable = "available"
	StatusMissing   = "missing"
)

// ErrLaunchModelRequired means Hecate owns the model launch flag for an
// adapter and the operator has not selected a concrete model yet.
var ErrLaunchModelRequired = errors.New("launch model required")

// ErrRemoteCredentialRequired means a remote-mode request tried to start an
// external agent without an explicitly allowed credential mode.
var ErrRemoteCredentialRequired = errors.New("remote-safe external-agent credential required")

// adapterDiscoveryOverrideEnv is intentionally narrower than
// adapterDevOverrideEnv: keep it for backend tests that only need catalog
// states, while UI/dev smoke fixtures should use the dev override so catalog
// and probe visuals stay aligned.
const adapterDiscoveryOverrideEnv = "HECATE_AGENT_ADAPTER_DISCOVERY_OVERRIDES"
const adapterDevOverrideEnv = "HECATE_AGENT_ADAPTER_DEV_OVERRIDES"
const personalRemoteExternalAgentLoginsEnv = "HECATE_PERSONAL_REMOTE_EXTERNAL_AGENT_LOGINS"

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

const (
	CredentialModeLocalLogin      = "local_login"
	CredentialModeAPIKey          = "api_key"
	CredentialModeEnterpriseToken = "enterprise_token"
	CredentialModeVendorHosted    = "vendor_hosted"
)

type Adapter struct {
	ID               string
	Name             string
	Command          string
	Args             []string
	CandidatePaths   []string
	AgentVersion     VersionProbe
	Managed          ManagedLauncher
	LaunchSuffixArgs []string
	LaunchModel      LaunchModelConfig
	LaunchOptions    []LaunchSelectConfig
	Kind             string
	Description      string
	CostMode         string
	DocsURL          string
	SupportedRange   string
	CredentialModes  []CredentialMode
}

type CredentialMode struct {
	ID            string
	Name          string
	Description   string
	RemoteAllowed bool
	EnvKeys       []string
}

type VersionProbe struct {
	Command        string
	Args           []string
	CandidatePaths []string
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
	Default     bool
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
	Available            bool
	Status               string
	Path                 string
	Error                string
	AdapterVersion       string
	AgentVersion         string
	VersionOutsideRange  bool
	AuthStatus           string
	AuthError            string
	RemoteCredentialMode string
	RemoteCredentialOK   bool
	RemoteCredentialHint string
	ClaudeCodeCLI        SetupCommandStatus
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
	Adapter                Adapter
	DriverKind             string
	NativeSessionID        string
	SessionStarted         bool
	SessionResumed         bool
	SessionRecovery        string
	ConfigOptions          []agentcontrols.ConfigOption
	AvailableCommands      []agentcontrols.Command
	AvailableCommandsKnown bool
}

type AvailableCommandsUpdate struct {
	SessionID string
	AdapterID string
	Commands  []agentcontrols.Command
}

type SetSessionConfigOptionRequest struct {
	SessionID string
	ConfigID  string
	Value     string
	BoolValue *bool
}

type SetSessionConfigOptionResult struct {
	ConfigOptions          []agentcontrols.ConfigOption
	AvailableCommands      []agentcontrols.Command
	AvailableCommandsKnown bool
}

type RunResult struct {
	Adapter                Adapter
	DriverKind             string
	NativeSessionID        string
	SessionStarted         bool
	SessionResumed         bool
	SessionRecovery        string
	Output                 string
	RawOutput              string
	ExitCode               int
	StartedAt              time.Time
	CompletedAt            time.Time
	DiffStat               string
	Diff                   string
	Usage                  Usage
	ConfigOptions          []agentcontrols.ConfigOption
	AvailableCommands      []agentcontrols.Command
	AvailableCommandsKnown bool
}

type Usage struct {
	ContextSize          int
	ContextUsed          int
	ReportedCostAmount   string
	ReportedCostCurrency string
}

type Activity struct {
	ID              string
	Type            string
	Status          string
	Kind            string
	Title           string
	Detail          string
	ArtifactPreview string
}

func (u Usage) Empty() bool {
	return u.ContextSize == 0 && u.ContextUsed == 0 && u.ReportedCostAmount == "" && u.ReportedCostCurrency == ""
}

func BuiltIns() []Adapter {
	return adaptersForBuild([]Adapter{
		{
			ID:      "codex",
			Name:    "Codex",
			Command: "codex-acp",
			CandidatePaths: []string{
				"${HOME}/.local/bin/codex-acp",
				"/opt/homebrew/bin/codex-acp",
				"/usr/local/bin/codex-acp",
			},
			AgentVersion: VersionProbe{
				Command: "codex",
				Args:    []string{"--version"},
				CandidatePaths: []string{
					"${HOME}/.local/bin/codex",
					"${HOME}/.volta/bin/codex",
					"/opt/homebrew/bin/codex",
					"/usr/local/bin/codex",
				},
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
			CredentialModes: []CredentialMode{
				{
					ID:          CredentialModeLocalLogin,
					Name:        "Local CLI login",
					Description: "Uses the operator's local Codex CLI login files. Local Hecate only.",
				},
				{
					ID:            CredentialModeAPIKey,
					Name:          "API key",
					Description:   "Uses a scoped OpenAI/Codex API key supplied to the adapter environment.",
					RemoteAllowed: true,
					EnvKeys:       []string{"OPENAI_API_KEY", "CODEX_API_KEY"},
				},
			},
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
			AgentVersion: VersionProbe{
				Command: "claude",
				Args:    []string{"--version"},
				CandidatePaths: []string{
					"${HOME}/.local/bin/claude",
					"${HOME}/.volta/bin/claude",
					"/opt/homebrew/bin/claude",
					"/usr/local/bin/claude",
				},
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
			CredentialModes: []CredentialMode{
				{
					ID:          CredentialModeLocalLogin,
					Name:        "Local CLI login",
					Description: "Uses the operator's local Claude Code login. Local Hecate only.",
				},
				{
					ID:            CredentialModeAPIKey,
					Name:          "API key",
					Description:   "Uses a scoped Anthropic API key supplied to the adapter environment.",
					RemoteAllowed: true,
					EnvKeys:       []string{"ANTHROPIC_API_KEY"},
				},
			},
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
			CredentialModes: []CredentialMode{
				{
					ID:          CredentialModeLocalLogin,
					Name:        "Local CLI login",
					Description: "Uses the operator's local Cursor Agent login. Local Hecate only.",
				},
				{
					ID:            CredentialModeAPIKey,
					Name:          "API key",
					Description:   "Uses a scoped Cursor API key supplied to the adapter environment.",
					RemoteAllowed: true,
					EnvKeys:       []string{"CURSOR_API_KEY"},
				},
			},
		},
		{
			ID:      "grok_build",
			Name:    "Grok Build",
			Command: "grok",
			Args:    []string{"agent", "stdio"},
			CandidatePaths: []string{
				"${HOME}/.local/bin/grok",
				"/opt/homebrew/bin/grok",
				"/usr/local/bin/grok",
			},
			Kind:           "acp",
			Description:    "Run Grok Build through its ACP mode as a long-lived external coding-agent session supervised by Hecate.",
			CostMode:       "external",
			DocsURL:        "https://docs.x.ai/build/cli/headless-scripting#acp",
			SupportedRange: ">=0.1.0",
			CredentialModes: []CredentialMode{
				{
					ID:          CredentialModeLocalLogin,
					Name:        "Local CLI login",
					Description: "Uses the operator's local Grok Build login. Local Hecate only.",
				},
				{
					ID:            CredentialModeAPIKey,
					Name:          "API key",
					Description:   "Uses a scoped xAI API key supplied to the adapter environment.",
					RemoteAllowed: true,
					EnvKeys:       []string{"XAI_API_KEY", "PROVIDER_XAI_API_KEY"},
				},
			},
		},
	})
}

func adaptersForBuild(items []Adapter) []Adapter {
	if !remoteRuntimeBuild {
		return items
	}
	filtered := make([]Adapter, 0, len(items))
	for _, item := range items {
		item.CredentialModes = cloudAllowedCredentialModes(item)
		if len(item.CredentialModes) == 0 {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func cloudAllowedCredentialModes(adapter Adapter) []CredentialMode {
	modes := make([]CredentialMode, 0, len(adapter.CredentialModes))
	for _, mode := range adapter.CredentialModes {
		if mode.RemoteAllowed {
			modes = append(modes, mode)
		}
	}
	return modes
}

func remoteCredentialStatus(adapter Adapter, getenv func(string) string) (CredentialMode, bool, string) {
	if getenv == nil {
		getenv = os.Getenv
	}
	modes := cloudAllowedCredentialModes(adapter)
	var envKeys []string
	for _, mode := range modes {
		envKeys = append(envKeys, mode.EnvKeys...)
		if credentialModeConfigured(mode, getenv) {
			return mode, true, ""
		}
	}
	if personalRemoteExternalAgentLoginsAllowed(getenv) {
		if mode, ok := localLoginCredentialMode(adapter); ok {
			return mode, true, ""
		}
	}
	if len(modes) == 0 {
		return CredentialMode{}, false, fmt.Sprintf("%s does not declare a remote-safe credential mode", adapter.Name)
	}
	return CredentialMode{}, false, fmt.Sprintf("%s requires one remote-safe credential environment variable: %s", adapter.Name, strings.Join(uniqueStrings(envKeys), ", "))
}

func localLoginCredentialMode(adapter Adapter) (CredentialMode, bool) {
	for _, mode := range adapter.CredentialModes {
		if mode.ID == CredentialModeLocalLogin {
			return mode, true
		}
	}
	return CredentialMode{}, false
}

func personalRemoteExternalAgentLoginsAllowed(getenv func(string) string) bool {
	if getenv == nil {
		getenv = os.Getenv
	}
	switch strings.ToLower(strings.TrimSpace(getenv(personalRemoteExternalAgentLoginsEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func credentialModeConfigured(mode CredentialMode, getenv func(string) string) bool {
	if len(mode.EnvKeys) == 0 {
		return true
	}
	for _, key := range mode.EnvKeys {
		if strings.TrimSpace(getenv(key)) != "" {
			return true
		}
	}
	return false
}

func validateRemoteCredentialForRequest(ctx context.Context, adapter Adapter) (CredentialMode, error) {
	if _, ok := remoteruntime.FromContext(ctx); !ok {
		return CredentialMode{}, nil
	}
	mode, ok, hint := remoteCredentialStatus(adapter, os.Getenv)
	if ok {
		return mode, nil
	}
	return CredentialMode{}, fmt.Errorf("%w: %s", ErrRemoteCredentialRequired, hint)
}

func remoteCredentialHint(adapter Adapter) string {
	_, ok, hint := remoteCredentialStatus(adapter, os.Getenv)
	if ok {
		return ""
	}
	return hint
}

func uniqueStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
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
		out = append(out, statusForAdapter(ctx, item, lookup, statusProbeOptions{}))
	}
	return out
}

func StatusForAdapter(ctx context.Context, id string, lookup LookupFunc) (Status, bool) {
	return statusForAdapterByID(ctx, id, lookup, statusProbeOptions{})
}

func StatusForAdapterAfterExplicitProbe(ctx context.Context, id string, lookup LookupFunc) (Status, bool) {
	return statusForAdapterByID(ctx, id, lookup, statusProbeOptions{allowManagedAdapterVersion: true})
}

type statusProbeOptions struct {
	allowManagedAdapterVersion bool
}

func statusForAdapterByID(ctx context.Context, id string, lookup LookupFunc, opts statusProbeOptions) (Status, bool) {
	if lookup == nil {
		lookup = exec.LookPath
	}
	for _, item := range BuiltIns() {
		if item.ID != strings.TrimSpace(id) {
			continue
		}
		return statusForAdapter(ctx, item, lookup, opts), true
	}
	return Status{}, false
}

func statusForAdapter(ctx context.Context, item Adapter, lookup LookupFunc, opts statusProbeOptions) Status {
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
	if _, ok := remoteruntime.FromContext(ctx); ok {
		mode, ready, hint := remoteCredentialStatus(item, os.Getenv)
		status.RemoteCredentialOK = ready
		status.RemoteCredentialHint = hint
		if ready {
			status.RemoteCredentialMode = mode.ID
		} else {
			status.Error = hint
			status.AuthStatus = AuthStatusUnauthenticated
			status.AuthError = hint
			return status
		}
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
	if item.Managed.Package != "" && shouldProbeAdapterVersionForStatus(item, path, lookup, opts.allowManagedAdapterVersion) {
		v := DetectVersion(ctx, path)
		status.AdapterVersion = v
	}
	status.AgentVersion = detectAgentVersionForStatus(ctx, item, path, lookup)
	status.VersionOutsideRange = !satisfiesRange(firstNonEmptyVersion(status.AdapterVersion, status.AgentVersion), item.SupportedRange)
	status.AuthStatus, status.AuthError = DetectAuthStatus(item)
	return status
}

func shouldProbeAdapterVersionForStatus(adapter Adapter, path string, lookup LookupFunc, allowManaged bool) bool {
	if !shouldProbeVersion(path) {
		return false
	}
	if adapter.Managed.Package == "" {
		return true
	}
	if allowManaged {
		return true
	}
	planned, err := plannedManagedLauncher(adapter, lookup)
	if err != nil {
		return true
	}
	return filepath.Clean(planned) != filepath.Clean(path)
}

func detectAgentVersionForStatus(ctx context.Context, adapter Adapter, path string, lookup LookupFunc) string {
	if adapter.AgentVersion.Command != "" {
		return DetectVersionProbe(ctx, adapter.AgentVersion, lookup)
	}
	if adapter.Managed.Package != "" {
		return ""
	}
	return DetectVersion(ctx, path)
}

func firstNonEmptyVersion(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
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
	status.AdapterVersion = ""
	status.AgentVersion = ""
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
	status.AdapterVersion = ""
	status.AgentVersion = ""
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
	return statusForAdapter(ctx, adapter, lookup, statusProbeOptions{}), nil
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
	return sanitizedEnvForAdapter("", env)
}

type adapterProcessEnv struct {
	values  []string
	cleanup func()
}

func prepareAdapterProcessEnv(ctx context.Context, adapter Adapter, env []string) (adapterProcessEnv, error) {
	mode, err := validateRemoteCredentialForRequest(ctx, adapter)
	if err != nil {
		return adapterProcessEnv{}, err
	}
	if _, ok := remoteruntime.FromContext(ctx); !ok {
		return adapterProcessEnv{values: sanitizedEnvForAdapter(adapter.ID, env)}, nil
	}
	if mode.ID == CredentialModeLocalLogin {
		home := remoteRuntimePersistentHome(env)
		if home == "" {
			return adapterProcessEnv{}, fmt.Errorf("%w: HOME or USERPROFILE is required when %s=1", ErrRemoteCredentialRequired, personalRemoteExternalAgentLoginsEnv)
		}
		return adapterProcessEnv{
			values: remoteRuntimeLocalLoginAdapterEnv(adapter, mode, env, home),
		}, nil
	}
	home, err := os.MkdirTemp("", "hecate-cloud-agent-home-*")
	if err != nil {
		return adapterProcessEnv{}, fmt.Errorf("create cloud adapter home: %w", err)
	}
	return adapterProcessEnv{
		values: remoteRuntimeAdapterEnv(adapter, mode, env, home),
		cleanup: func() {
			_ = os.RemoveAll(home)
		},
	}, nil
}

func remoteRuntimePersistentHome(env []string) string {
	for _, key := range []string{"HOME", "USERPROFILE"} {
		if value := strings.TrimSpace(envValue(env, key)); value != "" {
			return value
		}
	}
	return ""
}

func prepareGenericProcessEnv(ctx context.Context, env []string) (adapterProcessEnv, error) {
	if _, ok := remoteruntime.FromContext(ctx); !ok {
		return adapterProcessEnv{values: sanitizedEnv(env)}, nil
	}
	home, err := os.MkdirTemp("", "hecate-cloud-agent-home-*")
	if err != nil {
		return adapterProcessEnv{}, fmt.Errorf("create cloud adapter home: %w", err)
	}
	return adapterProcessEnv{
		values: remoteRuntimeBaseEnv(env, home, nil),
		cleanup: func() {
			_ = os.RemoveAll(home)
		},
	}, nil
}

func remoteRuntimeAdapterEnv(adapter Adapter, mode CredentialMode, env []string, home string) []string {
	allowedKeys := make(map[string]struct{}, len(mode.EnvKeys))
	for _, key := range mode.EnvKeys {
		key = strings.TrimSpace(key)
		if key != "" {
			allowedKeys[key] = struct{}{}
		}
	}
	out := remoteRuntimeBaseEnv(env, home, allowedKeys)
	if strings.EqualFold(strings.TrimSpace(adapter.ID), "grok_build") && !envContainsKey(out, "XAI_API_KEY") {
		if providerKey := envValue(env, "PROVIDER_XAI_API_KEY"); strings.TrimSpace(providerKey) != "" {
			out = append(out, "XAI_API_KEY="+providerKey)
		}
	}
	return out
}

func remoteRuntimeLocalLoginAdapterEnv(adapter Adapter, mode CredentialMode, env []string, home string) []string {
	out := remoteRuntimeAdapterEnv(adapter, mode, env, home)
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "NPM_CONFIG_CACHE"} {
		if value := strings.TrimSpace(envValue(env, key)); value != "" {
			out = replaceEnvValue(out, key, value)
		}
	}
	return out
}

func remoteRuntimeBaseEnv(env []string, home string, allowedKeys map[string]struct{}) []string {
	allowedPrefixes := []string{
		"PATH=",
		"Path=",
		"TMPDIR=",
		"TEMP=",
		"TMP=",
		"LANG=",
		"LC_",
		"SSL_CERT_FILE=",
		"SSL_CERT_DIR=",
		"NODE_EXTRA_CA_CERTS=",
		"SystemRoot=",
		"WINDIR=",
		"ComSpec=",
	}
	out := make([]string, 0, len(env))
	if strings.TrimSpace(home) != "" {
		out = append(out,
			"HOME="+home,
			"USERPROFILE="+home,
			"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
			"XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
			"XDG_DATA_HOME="+filepath.Join(home, ".local", "share"),
		)
	}
	for _, entry := range env {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || remoteRuntimeEnvNameIsEphemeral(name) {
			continue
		}
		if _, ok := allowedKeys[name]; ok && name != "PROVIDER_XAI_API_KEY" {
			if strings.TrimSpace(value) != "" {
				out = append(out, entry)
			}
			continue
		}
		for _, prefix := range allowedPrefixes {
			if strings.HasPrefix(entry, prefix) {
				out = append(out, entry)
				break
			}
		}
	}
	return out
}

func replaceEnvValue(env []string, key, value string) []string {
	prefix := key + "="
	entry := prefix + value
	for i, item := range env {
		if strings.HasPrefix(item, prefix) {
			env[i] = entry
			return env
		}
	}
	return append(env, entry)
}

func remoteRuntimeEnvNameIsEphemeral(name string) bool {
	switch name {
	case "HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME":
		return true
	default:
		return false
	}
}

func envContainsKey(env []string, key string) bool {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return true
		}
	}
	return false
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}

func sanitizedEnvForAdapter(adapterID string, env []string) []string {
	allowedPrefixes := []string{
		"PATH=",
		"Path=",
		"HOME=",
		"USERPROFILE=",
		"HOMEDRIVE=",
		"HOMEPATH=",
		"TMPDIR=",
		"TEMP=",
		"TMP=",
		"LANG=",
		"LC_",
		"TERM=",
		"USER=",
		"USERNAME=",
		"LOGNAME=",
		"APPDATA=",
		"LOCALAPPDATA=",
		"XDG_",
		"VOLTA_",
		"SSL_CERT_FILE=",
		"SSL_CERT_DIR=",
		"NODE_EXTRA_CA_CERTS=",
		"SystemRoot=",
		"WINDIR=",
		"ComSpec=",
	}
	switch strings.ToLower(strings.TrimSpace(adapterID)) {
	case "codex":
		allowedPrefixes = append(allowedPrefixes, "CODEX_", "OPENAI_")
	case "claude_code":
		allowedPrefixes = append(allowedPrefixes, "ANTHROPIC_", "CLAUDE_")
	case "cursor_agent":
		allowedPrefixes = append(allowedPrefixes, "CURSOR_")
	}
	includeXAI := strings.EqualFold(strings.TrimSpace(adapterID), "grok_build")
	if includeXAI {
		allowedPrefixes = append(allowedPrefixes, "XAI_")
	}
	out := make([]string, 0, len(env))
	hasXAIAPIKey := false
	providerXAIAPIKey := ""
	for _, entry := range env {
		if includeXAI && strings.HasPrefix(entry, "XAI_API_KEY=") && strings.TrimSpace(strings.TrimPrefix(entry, "XAI_API_KEY=")) != "" {
			hasXAIAPIKey = true
		}
		if includeXAI && strings.HasPrefix(entry, "PROVIDER_XAI_API_KEY=") {
			providerXAIAPIKey = strings.TrimSpace(strings.TrimPrefix(entry, "PROVIDER_XAI_API_KEY="))
		}
		for _, prefix := range allowedPrefixes {
			if strings.HasPrefix(entry, prefix) {
				out = append(out, entry)
				break
			}
		}
	}
	if includeXAI && !hasXAIAPIKey && providerXAIAPIKey != "" {
		out = append(out, "XAI_API_KEY="+providerXAIAPIKey)
	}
	return out
}

func captureGitDiff(ctx context.Context, workspace string, maxBytes int64) (string, string) {
	return gitrunner.NewLocalRunner().Diff(ctx, workspace, maxBytes)
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
