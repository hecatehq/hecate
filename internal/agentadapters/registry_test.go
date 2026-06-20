package agentadapters

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/remoteruntime"
)

func TestBuiltInsIncludeInitialExternalAgents(t *testing.T) {
	t.Parallel()

	items := BuiltIns()
	if len(items) != 4 {
		t.Fatalf("built-in adapter count = %d, want 4", len(items))
	}

	found := map[string]Adapter{}
	for _, item := range items {
		found[item.ID] = item
	}

	if got := found["codex"]; got.Command != "codex-acp-adapter" || got.Kind != DriverKindACP || got.CostMode != "external" {
		t.Fatalf("codex adapter = %#v", got)
	}
	if got := found["codex"]; got.SupportedRange != ">=0.1.0-alpha.17" {
		t.Fatalf("codex supported range = %q, want current Go adapter alpha range", got.SupportedRange)
	}
	if got := found["codex"]; !got.SupportsAuthenticate {
		t.Fatalf("codex supports authenticate = false, want true")
	}
	if got := found["codex"]; !got.SupportsLogout {
		t.Fatalf("codex supports logout = false, want true")
	}
	if got := found["claude_code"]; got.Command != "claude-code-acp-adapter" || got.Kind != DriverKindACP || got.CostMode != "external" {
		t.Fatalf("claude_code adapter = %#v", got)
	}
	if got := found["claude_code"]; got.SupportedRange != ">=0.1.0-alpha.19" {
		t.Fatalf("claude_code supported range = %q, want current Go adapter alpha range", got.SupportedRange)
	}
	if got := found["claude_code"]; !got.SupportsAuthenticate {
		t.Fatalf("claude_code supports authenticate = false, want true")
	}
	if got := found["claude_code"]; !got.SupportsLogout {
		t.Fatalf("claude_code supports logout = false, want true")
	}
	if got := found["cursor_agent"]; got.Command != "cursor-agent" || got.Kind != DriverKindACP || got.CostMode != "external" {
		t.Fatalf("cursor_agent adapter = %#v", got)
	}
	if got := found["cursor_agent"]; got.SupportsLogout {
		t.Fatalf("cursor_agent supports logout = true, want false until the adapter advertises ACP logout")
	}
	if got := found["cursor_agent"]; got.SupportsAuthenticate {
		t.Fatalf("cursor_agent supports authenticate = true, want false until the adapter advertises ACP authenticate")
	}
	if got := found["cursor_agent"]; len(got.Args) != 1 || got.Args[0] != "acp" {
		t.Fatalf("cursor_agent adapter = %#v", got)
	}
	if got := found["grok_build"]; got.Command != "grok" || got.Kind != DriverKindACP || got.CostMode != "external" {
		t.Fatalf("grok_build adapter = %#v", got)
	}
	if got := found["grok_build"]; got.SupportsLogout {
		t.Fatalf("grok_build supports logout = true, want false until the adapter advertises ACP logout")
	}
	if got := found["grok_build"]; got.SupportsAuthenticate {
		t.Fatalf("grok_build supports authenticate = true, want false until the adapter advertises ACP authenticate")
	}
	if got := found["grok_build"]; strings.Join(got.Args, " ") != "agent stdio" || len(got.LaunchSuffixArgs) != 0 {
		t.Fatalf("grok_build adapter args = %#v", got.Args)
	}
	if got := found["grok_build"]; got.LaunchModel.ConfigID != "" || len(got.LaunchModel.ListArgs) != 0 || len(got.LaunchModel.ArgTemplate) != 0 {
		t.Fatalf("grok_build launch model config = %#v, want ACP-owned model state", got.LaunchModel)
	}
	if got := found["grok_build"]; len(got.LaunchOptions) != 0 {
		t.Fatalf("grok_build launch options = %#v, want ACP-owned controls only", got.LaunchOptions)
	}
	for _, tc := range []struct {
		id      string
		envKeys []string
	}{
		{id: "codex", envKeys: []string{"OPENAI_API_KEY", "CODEX_API_KEY"}},
		{id: "claude_code", envKeys: []string{"ANTHROPIC_API_KEY"}},
		{id: "cursor_agent", envKeys: []string{"CURSOR_API_KEY"}},
		{id: "grok_build", envKeys: []string{"XAI_API_KEY", "PROVIDER_XAI_API_KEY"}},
	} {
		adapter := found[tc.id]
		if !hasCredentialMode(adapter, CredentialModeAPIKey, true, tc.envKeys...) {
			t.Fatalf("%s credential modes = %#v, want cloud API key mode with %v", tc.id, adapter.CredentialModes, tc.envKeys)
		}
		if remoteRuntimeBuild {
			if hasCredentialMode(adapter, CredentialModeLocalLogin, false) {
				t.Fatalf("%s credential modes = %#v, want hecate_remote build to omit local login", tc.id, adapter.CredentialModes)
			}
		} else if !hasCredentialMode(adapter, CredentialModeLocalLogin, false) {
			t.Fatalf("%s credential modes = %#v, want local login mode in default build", tc.id, adapter.CredentialModes)
		}
	}
}

func TestLimitedBufferReadFromUsesLimitedWritePath(t *testing.T) {
	t.Parallel()

	var writes []string
	buf := limitedBuffer{
		limit:   4,
		onWrite: func(chunk string) { writes = append(writes, chunk) },
	}
	n, err := buf.ReadFrom(strings.NewReader("abcdef"))
	if err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if n != 6 {
		t.Fatalf("ReadFrom bytes = %d, want 6", n)
	}
	if got := buf.String(); got != "abcd" {
		t.Fatalf("buffer = %q, want abcd", got)
	}
	if !buf.truncated {
		t.Fatalf("truncated = false, want true")
	}
	if len(writes) != 1 || writes[0] != "abcd" {
		t.Fatalf("onWrite chunks = %#v, want [abcd]", writes)
	}
}

func TestBuiltInGoAdaptersUseReleasedBinaries(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		id           string
		command      string
		agentCommand string
		docsURL      string
	}{
		{
			id:           "codex",
			command:      "codex-acp-adapter",
			agentCommand: "codex",
			docsURL:      "https://github.com/hecatehq/codex-acp-adapter",
		},
		{
			id:           "claude_code",
			command:      "claude-code-acp-adapter",
			agentCommand: "claude",
			docsURL:      "https://github.com/hecatehq/claude-code-acp-adapter",
		},
	} {
		adapter, ok := BuiltInByID(tc.id)
		if !ok {
			t.Fatalf("missing built-in adapter %q", tc.id)
		}
		if adapter.Command != tc.command || len(adapter.Args) != 0 {
			t.Fatalf("%s command = %q args=%#v, want released Go adapter binary without wrapper args", tc.id, adapter.Command, adapter.Args)
		}
		if adapter.AgentVersion.Command != tc.agentCommand || len(adapter.AgentVersion.Args) != 1 || adapter.AgentVersion.Args[0] != "--version" {
			t.Fatalf("%s agent version probe = %#v, want native provider CLI version probe", tc.id, adapter.AgentVersion)
		}
		if adapter.DocsURL != tc.docsURL {
			t.Fatalf("%s docs url = %q, want %q", tc.id, adapter.DocsURL, tc.docsURL)
		}
		assertNotPackageRunnerCommand(t, tc.id, adapter.Command)
		assertNotPackageRunnerCommand(t, tc.id+" agent version probe", adapter.AgentVersion.Command)
		for _, candidate := range adapter.CandidatePaths {
			if !strings.HasSuffix(candidate, "/"+tc.command) {
				t.Fatalf("%s candidate path = %q, want path ending in %s", tc.id, candidate, tc.command)
			}
		}
	}
}

func TestRemoteRuntimeCredentialsRejectLocalLoginFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("CODEX_API_KEY", "")
	authPath := filepath.Join(home, ".codex", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o755); err != nil {
		t.Fatalf("mkdir codex auth dir: %v", err)
	}
	if err := os.WriteFile(authPath, []byte(`{"token":"local"}`), 0o600); err != nil {
		t.Fatalf("write codex auth file: %v", err)
	}

	adapter, ok := BuiltInByID("codex")
	if !ok {
		t.Fatalf("missing codex adapter")
	}
	if _, err := validateRemoteCredentialForRequest(remoteIdentityContext(), adapter); !errors.Is(err, ErrRemoteCredentialRequired) {
		t.Fatalf("validateRemoteCredentialForRequest error = %v, want ErrRemoteCredentialRequired", err)
	}

	t.Setenv(personalRemoteExternalAgentLoginsEnv, "1")
	mode, err := validateRemoteCredentialForRequest(remoteIdentityContext(), adapter)
	if remoteRuntimeBuild {
		if !errors.Is(err, ErrRemoteCredentialRequired) {
			t.Fatalf("hecate_remote validateRemoteCredentialForRequest error = %v, want ErrRemoteCredentialRequired", err)
		}
	} else {
		if err != nil {
			t.Fatalf("validateRemoteCredentialForRequest with personal remote login opt-in: %v", err)
		}
		if mode.ID != CredentialModeLocalLogin {
			t.Fatalf("credential mode = %#v, want local login", mode)
		}
	}

	t.Setenv("OPENAI_API_KEY", "sk-cloud")
	mode, err = validateRemoteCredentialForRequest(remoteIdentityContext(), adapter)
	if err != nil {
		t.Fatalf("validateRemoteCredentialForRequest with API key: %v", err)
	}
	if mode.ID != CredentialModeAPIKey {
		t.Fatalf("credential mode = %#v, want API key", mode)
	}
}

func TestRemoteRuntimeCredentialsAllowCursorAndGrokAPIKeys(t *testing.T) {
	t.Setenv("CURSOR_API_KEY", "")
	t.Setenv("XAI_API_KEY", "")
	t.Setenv("PROVIDER_XAI_API_KEY", "")
	ctx := remoteIdentityContext()

	cursor, ok := BuiltInByID("cursor_agent")
	if !ok {
		t.Fatalf("missing cursor_agent adapter")
	}
	if _, err := validateRemoteCredentialForRequest(ctx, cursor); !errors.Is(err, ErrRemoteCredentialRequired) {
		t.Fatalf("cursor remote credential error = %v, want ErrRemoteCredentialRequired", err)
	}
	t.Setenv("CURSOR_API_KEY", "cursor-cloud")
	mode, err := validateRemoteCredentialForRequest(ctx, cursor)
	if err != nil {
		t.Fatalf("cursor remote credential with API key: %v", err)
	}
	if mode.ID != CredentialModeAPIKey {
		t.Fatalf("cursor credential mode = %#v, want API key", mode)
	}

	grok, ok := BuiltInByID("grok_build")
	if !ok {
		t.Fatalf("missing grok_build adapter")
	}
	if _, err := validateRemoteCredentialForRequest(ctx, grok); !errors.Is(err, ErrRemoteCredentialRequired) {
		t.Fatalf("grok remote credential error = %v, want ErrRemoteCredentialRequired", err)
	}
	t.Setenv("PROVIDER_XAI_API_KEY", "xai-provider-cloud")
	mode, err = validateRemoteCredentialForRequest(ctx, grok)
	if err != nil {
		t.Fatalf("grok remote credential with provider API key: %v", err)
	}
	if mode.ID != CredentialModeAPIKey {
		t.Fatalf("grok credential mode = %#v, want API key", mode)
	}
}

func TestRemoteRuntimeStatusRequiresRemoteCredentialBeforeDiscoveryOverrides(t *testing.T) {
	t.Setenv(adapterDevOverrideEnv, "cursor_agent=ready")
	t.Setenv("CURSOR_API_KEY", "")

	status, ok := StatusForAdapter(remoteIdentityContext(), "cursor_agent", func(file string) (string, error) {
		return "/usr/local/bin/" + file, nil
	})
	if !ok {
		t.Fatalf("StatusForAdapter(cursor_agent) ok = false")
	}
	if status.Available || status.Status != StatusMissing {
		t.Fatalf("status = %#v, want remote credential gate to keep adapter unavailable", status)
	}
	if status.AuthStatus != AuthStatusUnauthenticated || !strings.Contains(status.AuthError, "CURSOR_API_KEY") {
		t.Fatalf("auth status/error = %q/%q, want remote credential hint", status.AuthStatus, status.AuthError)
	}

	t.Setenv("CURSOR_API_KEY", "cursor-cloud")
	status, ok = StatusForAdapter(remoteIdentityContext(), "cursor_agent", func(file string) (string, error) {
		return "/usr/local/bin/" + file, nil
	})
	if !ok {
		t.Fatalf("StatusForAdapter(cursor_agent) ok = false")
	}
	if !status.Available || status.RemoteCredentialMode != CredentialModeAPIKey || !status.RemoteCredentialOK {
		t.Fatalf("status = %#v, want remote credential ready status", status)
	}
}

func TestRemoteRuntimeStatusCanOptIntoPersonalRemoteLogins(t *testing.T) {
	if remoteRuntimeBuild {
		t.Skip("hecate_remote build omits local-login credential modes")
	}
	t.Setenv(adapterDevOverrideEnv, "codex=ready")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("CODEX_API_KEY", "")
	t.Setenv(personalRemoteExternalAgentLoginsEnv, "1")

	status, ok := StatusForAdapter(remoteIdentityContext(), "codex", func(file string) (string, error) {
		return "/usr/local/bin/" + file, nil
	})
	if !ok {
		t.Fatalf("StatusForAdapter(codex) ok = false")
	}
	if !status.Available || !status.RemoteCredentialOK || status.RemoteCredentialMode != CredentialModeLocalLogin {
		t.Fatalf("status = %#v, want personal remote login credential ready status", status)
	}
}

func TestListWithLookupReportsAvailability(t *testing.T) {
	t.Parallel()

	response := ListWithLookup(context.Background(), func(file string) (string, error) {
		if file == "codex-acp-adapter" {
			return "/usr/local/bin/codex-acp-adapter", nil
		}
		return "", errors.New("not found")
	})

	byID := map[string]Status{}
	for _, item := range response {
		byID[item.ID] = item
	}

	codex := byID["codex"]
	if !codex.Available || codex.Status != StatusAvailable || codex.Path != "/usr/local/bin/codex-acp-adapter" {
		t.Fatalf("codex status = %#v", codex)
	}

	if _, ok := byID["claude_code"]; !ok {
		t.Fatalf("missing claude_code status in %#v", response)
	}
	if _, ok := byID["cursor_agent"]; !ok {
		t.Fatalf("missing cursor_agent status in %#v", response)
	}
	if _, ok := byID["grok_build"]; !ok {
		t.Fatalf("missing grok_build status in %#v", response)
	}
}

func TestListWithLookupHonorsDiscoveryOverrideAllMissing(t *testing.T) {
	t.Setenv(adapterDiscoveryOverrideEnv, "all=missing")

	response := ListWithLookup(context.Background(), func(file string) (string, error) {
		return "/usr/local/bin/" + file, nil
	})

	if len(response) == 0 {
		t.Fatalf("ListWithLookup returned no adapters")
	}
	for _, item := range response {
		if item.Available || item.Status != StatusMissing || item.Path != "" {
			t.Fatalf("%s status = %#v, want forced missing", item.ID, item)
		}
		if !strings.Contains(item.Error, adapterDiscoveryOverrideEnv) {
			t.Fatalf("%s error = %q, want override marker", item.ID, item.Error)
		}
	}
}

func TestStatusForAdapterHonorsDiscoveryOverrideAvailable(t *testing.T) {
	t.Setenv(adapterDiscoveryOverrideEnv, "codex=available")

	status, ok := StatusForAdapter(context.Background(), "codex", func(file string) (string, error) {
		return "", errors.New("not found on PATH")
	})
	if !ok {
		t.Fatalf("StatusForAdapter(codex) ok = false")
	}
	if !status.Available || status.Status != StatusAvailable || status.Path != "dev-override://codex" {
		t.Fatalf("status = %#v, want forced available", status)
	}
	if status.AuthStatus != AuthStatusUnknown {
		t.Fatalf("auth status = %q, want unknown for discovery-only override", status.AuthStatus)
	}
}

func TestStatusForAdapterHonorsDevOverrideAuthRequired(t *testing.T) {
	t.Setenv(adapterDevOverrideEnv, "codex=auth_required")

	status, ok := StatusForAdapter(context.Background(), "codex", func(file string) (string, error) {
		return "", errors.New("not found on PATH")
	})
	if !ok {
		t.Fatalf("StatusForAdapter(codex) ok = false")
	}
	if !status.Available || status.Status != StatusAvailable || status.Path != "dev-override://codex" {
		t.Fatalf("status = %#v, want forced available", status)
	}
	if status.AuthStatus != AuthStatusUnauthenticated {
		t.Fatalf("auth status = %q, want unauthenticated", status.AuthStatus)
	}
	if !strings.Contains(status.AuthError, "codex login") {
		t.Fatalf("auth error = %q, want codex login guidance", status.AuthError)
	}
}

func TestDevOverrideActive(t *testing.T) {
	t.Setenv(adapterDevOverrideEnv, "all=missing,codex=ready")

	if !DevOverrideActive("codex") {
		t.Fatalf("DevOverrideActive(codex) = false, want true")
	}
	if !DevOverrideActive("claude_code") {
		t.Fatalf("DevOverrideActive(claude_code) = false, want true from all=missing")
	}
	t.Setenv(adapterDevOverrideEnv, "fake=broken")
	if DevOverrideActive("fake") {
		t.Fatalf("DevOverrideActive(fake) = true for invalid override value")
	}
}

func TestStatusForAdapterHonorsDevOverrideAppMissing(t *testing.T) {
	t.Setenv(adapterDevOverrideEnv, "all=ready,claude_code=app_missing")

	status, ok := StatusForAdapter(context.Background(), "claude_code", func(file string) (string, error) {
		return "/usr/local/bin/" + file, nil
	})
	if !ok {
		t.Fatalf("StatusForAdapter(claude_code) ok = false")
	}
	if !status.Available || status.Status != StatusAvailable || status.Path != "dev-override://claude_code" {
		t.Fatalf("status = %#v, want forced available app-missing fixture", status)
	}
	if status.AuthStatus != AuthStatusUnknown {
		t.Fatalf("auth status = %q, want unknown for app-missing fixture", status.AuthStatus)
	}
	if !strings.Contains(status.Error, "app CLI missing") {
		t.Fatalf("error = %q, want app CLI missing marker", status.Error)
	}
}

func TestStatusForAdapterDiscoveryOverridePrefersExactMatch(t *testing.T) {
	t.Setenv(adapterDiscoveryOverrideEnv, "all=missing,codex=available")

	status, ok := StatusForAdapter(context.Background(), "codex", func(file string) (string, error) {
		return "", errors.New("not found on PATH")
	})
	if !ok {
		t.Fatalf("StatusForAdapter(codex) ok = false")
	}
	if !status.Available || status.Status != StatusAvailable {
		t.Fatalf("codex status = %#v, want exact adapter override to win over all", status)
	}

	status, ok = StatusForAdapter(context.Background(), "claude_code", func(file string) (string, error) {
		return "/usr/local/bin/" + file, nil
	})
	if !ok {
		t.Fatalf("StatusForAdapter(claude_code) ok = false")
	}
	if status.Available || status.Status != StatusMissing {
		t.Fatalf("claude_code status = %#v, want all override fallback", status)
	}
}

func TestStatusForAdapterIgnoresInvalidDiscoveryOverride(t *testing.T) {
	t.Setenv(adapterDiscoveryOverrideEnv, "fake=broken")

	status := statusForAdapter(context.Background(), Adapter{
		ID:      "fake",
		Name:    "Fake",
		Command: "fake-adapter",
	}, func(file string) (string, error) {
		return "", errors.New("not found on PATH")
	})
	if status.Available || status.Status != StatusMissing || !strings.Contains(status.Error, "not found") {
		t.Fatalf("status = %#v, want normal missing lookup", status)
	}
}

func TestListWithLookupUsesCandidatePathFallback(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	exe := filepath.Join(dir, "codex-acp-adapter")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}

	response := ListWithLookup(context.Background(), func(file string) (string, error) {
		return "", errors.New("not found on PATH")
	})
	found := false
	for _, item := range response {
		if item.ID != "codex" {
			continue
		}
		item.CandidatePaths = []string{exe}
		path, err := resolveExecutable(item.Adapter, func(file string) (string, error) {
			return "", errors.New("not found on PATH")
		})
		if err != nil {
			t.Fatalf("resolve executable: %v", err)
		}
		if path != exe {
			t.Fatalf("path = %q, want %q", path, exe)
		}
		found = true
	}
	if !found {
		t.Fatalf("missing codex adapter in %#v", response)
	}
}

func TestResolveExecutableExpandsHomeCandidate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	exe := filepath.Join(bin, "direct-test-acp")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}

	path, err := resolveExecutable(Adapter{
		ID:             "codex",
		Command:        "codex-missing",
		CandidatePaths: []string{"${HOME}/.local/bin/direct-test-acp"},
	}, func(file string) (string, error) {
		return "", errors.New("not found on PATH")
	})
	if err != nil {
		t.Fatalf("resolve executable: %v", err)
	}
	if path != exe {
		t.Fatalf("path = %q, want %q", path, exe)
	}
}

func TestStatusForAdapterReportsAdapterAndAgentVersionsForBridgeBinary(t *testing.T) {
	dir := t.TempDir()
	adapterBinary := writeFakeBinary(t, dir, "codex-acp-adapter", "codex-acp-adapter 0.1.0-alpha.2")
	agent := writeFakeBinary(t, dir, "codex", "codex 9.8.7")
	adapter := Adapter{
		ID:      "codex",
		Name:    "Codex",
		Command: "codex-acp-adapter",
		AgentVersion: VersionProbe{
			Command: "codex",
			Args:    []string{"--version"},
		},
		SupportedRange: ">=0.0.0-dev",
	}
	status := statusForAdapter(context.Background(), adapter, func(file string) (string, error) {
		switch file {
		case "codex-acp-adapter":
			return adapterBinary, nil
		case "codex":
			return agent, nil
		default:
			return "", errors.New("not found on PATH")
		}
	})
	if status.AdapterVersion != "0.1.0-alpha.2" {
		t.Fatalf("status.AdapterVersion = %q, want 0.1.0-alpha.2", status.AdapterVersion)
	}
	if status.AgentVersion != "9.8.7" {
		t.Fatalf("status.AgentVersion = %q, want 9.8.7", status.AgentVersion)
	}
	if status.VersionOutsideRange {
		t.Fatalf("status.VersionOutsideRange = true, want version in supported range")
	}
}

func TestStatusForAdapterWarnsOnOldGoAdapterRelease(t *testing.T) {
	dir := t.TempDir()
	adapterBinary := writeFakeBinary(t, dir, "codex-acp-adapter", "codex-acp-adapter 0.1.0-alpha.10")
	agent := writeFakeBinary(t, dir, "codex", "codex 9.8.7")
	adapter := Adapter{
		ID:             "codex",
		Name:           "Codex",
		Command:        "codex-acp-adapter",
		SupportedRange: ">=0.1.0-alpha.11",
		AgentVersion: VersionProbe{
			Command: "codex",
			Args:    []string{"--version"},
		},
	}

	status := statusForAdapter(context.Background(), adapter, func(file string) (string, error) {
		switch file {
		case "codex-acp-adapter":
			return adapterBinary, nil
		case "codex":
			return agent, nil
		default:
			return "", errors.New("not found on PATH")
		}
	})

	if status.AdapterVersion != "0.1.0-alpha.10" {
		t.Fatalf("status.AdapterVersion = %q, want 0.1.0-alpha.10", status.AdapterVersion)
	}
	if !status.VersionOutsideRange {
		t.Fatal("status.VersionOutsideRange = false, want warning for pre-alpha.11 adapter")
	}
}

func TestValidateWorkspaceCanonicalizesSymlink(t *testing.T) {
	t.Parallel()

	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "workspace-link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	got, err := ValidateWorkspace(link)
	if err != nil {
		t.Fatalf("ValidateWorkspace: %v", err)
	}
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("EvalSymlinks(target): %v", err)
	}
	if got != want {
		t.Fatalf("workspace = %q, want canonical target %q", got, want)
	}
}

func TestValidateWorkspaceRejectsFiles(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(path, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := ValidateWorkspace(path); err == nil {
		t.Fatalf("ValidateWorkspace(file) error = nil, want error")
	}
}

func TestCaptureGitDiffWorksFromRepositorySubdirectory(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	if err := exec.Command("git", "-C", root, "init", "-b", "main").Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	subdir := filepath.Join(root, "packages", "app")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	file := filepath.Join(subdir, "README.md")
	if err := os.WriteFile(file, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := exec.Command("git", "-C", root, "add", ".").Run(); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := os.WriteFile(file, []byte("hello\nfrom subdir\n"), 0o644); err != nil {
		t.Fatalf("modify file: %v", err)
	}

	stat, diff := captureGitDiff(context.Background(), subdir, 64*1024)
	if !strings.Contains(stat, "README.md") {
		t.Fatalf("diff stat = %q, want README.md", stat)
	}
	if !strings.Contains(diff, "+from subdir") {
		t.Fatalf("diff = %q, want added line", diff)
	}
}

func TestCaptureACPTurnResultOmitsUnchangedPreexistingDiff(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := initializedGitWorkspace(t)
	file := filepath.Join(root, "README.md")
	if err := os.WriteFile(file, []byte("hello\nalready dirty\n"), 0o644); err != nil {
		t.Fatalf("modify file: %v", err)
	}
	initialStat, initialDiff := captureGitDiff(context.Background(), root, 64*1024)
	if initialStat == "" || initialDiff == "" {
		t.Fatal("initial dirty diff is empty")
	}

	result, err := captureACPTurnResult(
		context.Background(),
		Adapter{ID: "grok_build"},
		RunRequest{Workspace: root, MaxOutputBytes: 64 * 1024},
		"native_1",
		"done",
		"done",
		Usage{},
		0,
		timeNowForTest(),
		timeNowForTest(),
		initialStat,
		initialDiff,
		nil,
	)
	if err != nil {
		t.Fatalf("captureACPTurnResult: %v", err)
	}
	if result.DiffStat != "" || result.Diff != "" {
		t.Fatalf("unchanged diff attached to turn: stat=%q diff=%q", result.DiffStat, result.Diff)
	}
}

func TestCaptureACPTurnResultIncludesDiffChangedDuringTurn(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := initializedGitWorkspace(t)
	initialStat, initialDiff := captureGitDiff(context.Background(), root, 64*1024)
	file := filepath.Join(root, "README.md")
	if err := os.WriteFile(file, []byte("hello\nchanged during turn\n"), 0o644); err != nil {
		t.Fatalf("modify file: %v", err)
	}

	result, err := captureACPTurnResult(
		context.Background(),
		Adapter{ID: "grok_build"},
		RunRequest{Workspace: root, MaxOutputBytes: 64 * 1024},
		"native_1",
		"done",
		"done",
		Usage{},
		0,
		timeNowForTest(),
		timeNowForTest(),
		initialStat,
		initialDiff,
		nil,
	)
	if err != nil {
		t.Fatalf("captureACPTurnResult: %v", err)
	}
	if !strings.Contains(result.DiffStat, "README.md") {
		t.Fatalf("diff stat = %q, want README.md", result.DiffStat)
	}
	if !strings.Contains(result.Diff, "+changed during turn") {
		t.Fatalf("diff = %q, want turn change", result.Diff)
	}
}

func initializedGitWorkspace(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	if err := exec.Command("git", "-C", root, "init", "-b", "main").Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if err := exec.Command("git", "-C", root, "config", "user.email", "test@example.com").Run(); err != nil {
		t.Fatalf("git config email: %v", err)
	}
	if err := exec.Command("git", "-C", root, "config", "user.name", "Test User").Run(); err != nil {
		t.Fatalf("git config name: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := exec.Command("git", "-C", root, "add", ".").Run(); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := exec.Command("git", "-C", root, "commit", "-m", "init").Run(); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	return root
}

func timeNowForTest() time.Time {
	return time.Now().UTC()
}

func TestSanitizedEnvPreservesRuntimeEssentialsOnlyWithoutAdapter(t *testing.T) {
	t.Parallel()

	env := sanitizedEnv([]string{
		"PATH=/bin",
		"HOME=/Users/alice",
		"OPENAI_API_KEY=sk-test",
		"ANTHROPIC_API_KEY=sk-ant-test",
		"CLAUDE_CONFIG_DIR=/tmp/claude",
		"CODEX_HOME=/tmp/codex",
		"CURSOR_API_KEY=cursor-test",
		"PROVIDER_XAI_API_KEY=provider-xai-test",
		"XAI_API_KEY=xai-test",
		"VOLTA_HOME=/Users/alice/.volta",
		"APPDATA=C:\\Users\\alice\\AppData\\Roaming",
		"LOCALAPPDATA=C:\\Users\\alice\\AppData\\Local",
		"SSL_CERT_FILE=/etc/ssl/corp.pem",
		"NODE_EXTRA_CA_CERTS=/etc/ssl/node-corp.pem",
		"HTTPS_PROXY=http://proxy.local:8080",
		"HECATE_AUTH_TOKEN=secret",
	})

	got := map[string]bool{}
	for _, item := range env {
		got[item] = true
	}
	for _, want := range []string{
		"PATH=/bin",
		"HOME=/Users/alice",
		"VOLTA_HOME=/Users/alice/.volta",
		"APPDATA=C:\\Users\\alice\\AppData\\Roaming",
		"LOCALAPPDATA=C:\\Users\\alice\\AppData\\Local",
		"SSL_CERT_FILE=/etc/ssl/corp.pem",
		"NODE_EXTRA_CA_CERTS=/etc/ssl/node-corp.pem",
	} {
		if !got[want] {
			t.Fatalf("missing allowed env %q in %#v", want, env)
		}
	}
	for _, leaked := range []string{
		"OPENAI_API_KEY=sk-test",
		"ANTHROPIC_API_KEY=sk-ant-test",
		"CLAUDE_CONFIG_DIR=/tmp/claude",
		"CODEX_HOME=/tmp/codex",
		"CURSOR_API_KEY=cursor-test",
		"HECATE_AUTH_TOKEN=secret",
		"XAI_API_KEY=xai-test",
		"HTTPS_PROXY=http://proxy.local:8080",
	} {
		if got[leaked] {
			t.Fatalf("credential env %q leaked into generic adapter env: %#v", leaked, env)
		}
	}
}

func TestSanitizedEnvUsesPerAdapterCredentialPrefixes(t *testing.T) {
	t.Parallel()

	input := []string{
		"PATH=/bin",
		"OPENAI_API_KEY=sk-test",
		"ANTHROPIC_API_KEY=sk-ant-test",
		"CLAUDE_CONFIG_DIR=/tmp/claude",
		"CODEX_HOME=/tmp/codex",
		"CURSOR_API_KEY=cursor-test",
		"HECATE_AUTH_TOKEN=secret",
	}
	cases := []struct {
		name    string
		adapter string
		want    []string
		block   []string
	}{
		{
			name:    "codex",
			adapter: "codex",
			want:    []string{"PATH=/bin", "OPENAI_API_KEY=sk-test", "CODEX_HOME=/tmp/codex"},
			block:   []string{"ANTHROPIC_API_KEY=sk-ant-test", "CLAUDE_CONFIG_DIR=/tmp/claude", "CURSOR_API_KEY=cursor-test", "HECATE_AUTH_TOKEN=secret"},
		},
		{
			name:    "claude",
			adapter: "claude_code",
			want:    []string{"PATH=/bin", "ANTHROPIC_API_KEY=sk-ant-test", "CLAUDE_CONFIG_DIR=/tmp/claude"},
			block:   []string{"OPENAI_API_KEY=sk-test", "CODEX_HOME=/tmp/codex", "CURSOR_API_KEY=cursor-test", "HECATE_AUTH_TOKEN=secret"},
		},
		{
			name:    "cursor",
			adapter: "cursor_agent",
			want:    []string{"PATH=/bin", "CURSOR_API_KEY=cursor-test"},
			block:   []string{"OPENAI_API_KEY=sk-test", "CODEX_HOME=/tmp/codex", "ANTHROPIC_API_KEY=sk-ant-test", "CLAUDE_CONFIG_DIR=/tmp/claude", "HECATE_AUTH_TOKEN=secret"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := sanitizedEnvForAdapter(tc.adapter, input)
			got := map[string]bool{}
			for _, item := range env {
				got[item] = true
			}
			for _, want := range tc.want {
				if !got[want] {
					t.Fatalf("missing adapter env %q in %#v", want, env)
				}
			}
			for _, blocked := range tc.block {
				if got[blocked] {
					t.Fatalf("blocked env %q leaked into %s env: %#v", blocked, tc.adapter, env)
				}
			}
		})
	}
}

func TestSanitizedEnvMapsProviderXAIKeyOnlyForGrokBuild(t *testing.T) {
	t.Parallel()

	env := sanitizedEnvForAdapter("grok_build", []string{
		"PATH=/bin",
		"PROVIDER_XAI_API_KEY=provider-xai-test",
	})

	got := map[string]bool{}
	for _, item := range env {
		got[item] = true
	}
	if !got["XAI_API_KEY=provider-xai-test"] {
		t.Fatalf("missing XAI_API_KEY bridge in %#v", env)
	}
	if got["PROVIDER_XAI_API_KEY=provider-xai-test"] {
		t.Fatalf("provider-scoped key leaked into adapter env: %#v", env)
	}

	env = sanitizedEnvForAdapter("codex", []string{
		"PATH=/bin",
		"PROVIDER_XAI_API_KEY=provider-xai-test",
	})
	got = map[string]bool{}
	for _, item := range env {
		got[item] = true
	}
	if got["XAI_API_KEY=provider-xai-test"] || got["PROVIDER_XAI_API_KEY=provider-xai-test"] {
		t.Fatalf("xAI key leaked into non-Grok adapter env: %#v", env)
	}
}

func TestRemoteRuntimeAdapterEnvUsesOnlyRemoteCredentialKeysAndEphemeralHome(t *testing.T) {
	t.Parallel()

	adapter, ok := BuiltInByID("codex")
	if !ok {
		t.Fatal("missing codex adapter")
	}
	mode := CredentialMode{ID: CredentialModeAPIKey, EnvKeys: []string{"OPENAI_API_KEY", "CODEX_API_KEY"}}
	env := remoteRuntimeAdapterEnv(adapter, mode, []string{
		"PATH=/bin",
		"HOME=/Users/alice",
		"XDG_CONFIG_HOME=/Users/alice/.config",
		"LANG=en_US.UTF-8",
		"OPENAI_API_KEY=sk-openai",
		"CODEX_API_KEY=sk-codex",
		"CODEX_AUTH_TOKEN=local-token",
		"CODEX_HOME=/Users/alice/.codex",
		"ANTHROPIC_API_KEY=sk-anthropic",
		"CLAUDE_CONFIG_DIR=/Users/alice/.claude",
		"HECATE_RUNTIME_TOKEN=runtime-token",
	}, "/tmp/hecate-cloud-home")

	got := map[string]bool{}
	for _, item := range env {
		got[item] = true
	}
	for _, want := range []string{
		"HOME=/tmp/hecate-cloud-home",
		"USERPROFILE=/tmp/hecate-cloud-home",
		"XDG_CONFIG_HOME=" + filepath.Join("/tmp/hecate-cloud-home", ".config"),
		"XDG_CACHE_HOME=" + filepath.Join("/tmp/hecate-cloud-home", ".cache"),
		"XDG_DATA_HOME=" + filepath.Join("/tmp/hecate-cloud-home", ".local", "share"),
		"PATH=/bin",
		"LANG=en_US.UTF-8",
		"OPENAI_API_KEY=sk-openai",
		"CODEX_API_KEY=sk-codex",
	} {
		if !got[want] {
			t.Fatalf("missing cloud env %q in %#v", want, env)
		}
	}
	for _, blocked := range []string{
		"HOME=/Users/alice",
		"XDG_CONFIG_HOME=/Users/alice/.config",
		"CODEX_AUTH_TOKEN=local-token",
		"CODEX_HOME=/Users/alice/.codex",
		"ANTHROPIC_API_KEY=sk-anthropic",
		"CLAUDE_CONFIG_DIR=/Users/alice/.claude",
		"HECATE_RUNTIME_TOKEN=runtime-token",
	} {
		if got[blocked] {
			t.Fatalf("blocked cloud env %q leaked into %#v", blocked, env)
		}
	}
}

func TestRemoteRuntimeAdapterEnvMapsProviderXAIKeyWithoutLeakingBridge(t *testing.T) {
	t.Parallel()

	adapter, ok := BuiltInByID("grok_build")
	if !ok {
		t.Fatal("missing grok_build adapter")
	}
	mode := CredentialMode{ID: CredentialModeAPIKey, EnvKeys: []string{"XAI_API_KEY", "PROVIDER_XAI_API_KEY"}}
	env := remoteRuntimeAdapterEnv(adapter, mode, []string{
		"PATH=/bin",
		"XAI_API_KEY=",
		"PROVIDER_XAI_API_KEY=provider-xai-test",
	}, "/tmp/hecate-cloud-home")

	got := map[string]bool{}
	for _, item := range env {
		got[item] = true
	}
	if !got["XAI_API_KEY=provider-xai-test"] {
		t.Fatalf("missing XAI_API_KEY bridge in %#v", env)
	}
	if got["PROVIDER_XAI_API_KEY=provider-xai-test"] {
		t.Fatalf("provider-scoped key leaked into cloud adapter env: %#v", env)
	}
}

func TestPrepareAdapterProcessEnvUsesPersistentHomeForPersonalRemoteLogins(t *testing.T) {
	if remoteRuntimeBuild {
		t.Skip("hecate_remote build omits local-login credential modes")
	}
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("CODEX_API_KEY", "")
	t.Setenv(personalRemoteExternalAgentLoginsEnv, "1")

	adapter, ok := BuiltInByID("codex")
	if !ok {
		t.Fatal("missing codex adapter")
	}
	processEnv, err := prepareAdapterProcessEnv(remoteIdentityContext(), adapter, []string{
		"PATH=/bin",
		"HOME=/workspace/.hecate/home",
		"XDG_CONFIG_HOME=/workspace/.hecate/config",
		"XDG_CACHE_HOME=/workspace/.hecate/cache",
		"XDG_DATA_HOME=/workspace/.hecate/share",
		"NPM_CONFIG_CACHE=/workspace/.hecate/npm-cache",
		"OPENAI_API_KEY=",
		"CODEX_AUTH_TOKEN=local-token",
		"HECATE_RUNTIME_TOKEN=runtime-token",
	})
	if err != nil {
		t.Fatalf("prepareAdapterProcessEnv: %v", err)
	}
	if processEnv.cleanup != nil {
		t.Fatal("cleanup set for persistent personal remote login env, want no temp home cleanup")
	}

	got := map[string]bool{}
	for _, item := range processEnv.values {
		got[item] = true
	}
	for _, want := range []string{
		"HOME=/workspace/.hecate/home",
		"USERPROFILE=/workspace/.hecate/home",
		"XDG_CONFIG_HOME=/workspace/.hecate/config",
		"XDG_CACHE_HOME=/workspace/.hecate/cache",
		"XDG_DATA_HOME=/workspace/.hecate/share",
		"NPM_CONFIG_CACHE=/workspace/.hecate/npm-cache",
		"PATH=/bin",
	} {
		if !got[want] {
			t.Fatalf("missing personal remote login env %q in %#v", want, processEnv.values)
		}
	}
	for _, blocked := range []string{
		"CODEX_AUTH_TOKEN=local-token",
		"HECATE_RUNTIME_TOKEN=runtime-token",
	} {
		if got[blocked] {
			t.Fatalf("blocked personal remote login env %q leaked into %#v", blocked, processEnv.values)
		}
	}
}

func TestNormalizeOutputPreservesPlainText(t *testing.T) {
	t.Parallel()

	raw := "plain text from claude\nwith another line"
	if got := normalizeOutput("claude_code", raw); got != raw {
		t.Fatalf("normalizeOutput plain text = %q, want %q", got, raw)
	}
}

func TestNormalizeOutputExtractsCodexJSONLText(t *testing.T) {
	t.Parallel()

	raw := strings.Join([]string{
		`{"type":"session.started","id":"s1"}`,
		`{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"First paragraph."}]}}`,
		`{"type":"message","content":"Second paragraph."}`,
		`{"type":"usage","input_tokens":10,"output_tokens":5}`,
	}, "\n")

	got := normalizeOutput("codex", raw)
	want := "First paragraph.\nSecond paragraph."
	if got != want {
		t.Fatalf("normalizeOutput codex JSONL = %q, want %q", got, want)
	}
}

func TestNormalizeOutputFallsBackToRawCodexWhenNoText(t *testing.T) {
	t.Parallel()

	raw := `{"type":"usage","input_tokens":10}`
	if got := normalizeOutput("codex", raw); got != raw {
		t.Fatalf("normalizeOutput fallback = %q, want raw %q", got, raw)
	}
}

func TestNormalizeErrorTagsClaudeBillingJSONRPC(t *testing.T) {
	t.Parallel()

	err := errors.New(`{"code":-32603,"message":"Internal error: Credit balance is too low","data":{"errorKind":"billing_error"}}`)
	got := NormalizeError("Claude Code", err)
	want := "Claude Code error (billing_error): Credit balance is too low"
	if got != want {
		t.Fatalf("NormalizeError = %q, want %q", got, want)
	}
}

func TestNormalizeErrorExplainsClaudeAuthRequirement(t *testing.T) {
	t.Parallel()

	got := NormalizeError("Claude Code", errors.New(`{"code":-32603,"message":"Authentication required"}`))
	// Three load-bearing markers in the rewritten copy:
	//   - "Claude Code isn't signed in" — the friendly headline.
	//   - "Providers tab" — explicit separation from the Anthropic
	//     provider key (the original message conflated them, which
	//     misled operators who had configured Anthropic in the UI).
	//   - "claude_code_auth_required" — the stable token that
	//     ui/src/features/chats/ChatView.tsx pattern-matches on
	//     (via .includes()) when building the setupAction prop for
	//     TranscriptMessageRow. TranscriptMessageRow itself only
	//     strips the marker from the visible message text; the
	//     match-and-render-button decision lives in ChatView. Don't
	//     change this token without updating the ChatView handler.
	if !strings.Contains(got, "isn't signed in") {
		t.Fatalf("NormalizeError = %q, want friendly 'isn't signed in' headline", got)
	}
	if !strings.Contains(got, "Providers tab") {
		t.Fatalf("NormalizeError = %q, want explicit separation from the Anthropic Providers-tab key", got)
	}
	if !strings.Contains(got, "claude_code_auth_required") {
		t.Fatalf("NormalizeError = %q, want claude_code_auth_required token for UI pattern-match", got)
	}
}

func TestNormalizeErrorExtractsJSONRPCStringData(t *testing.T) {
	t.Parallel()

	err := errors.New(`{"code":-32603,"message":"Internal error","data":"stream error (api_error): no healthy upstream"}`)
	got := NormalizeError("Grok Build", err)
	if !strings.Contains(got, "Grok Build error: stream error (api_error): no healthy upstream") {
		t.Fatalf("NormalizeError = %q, want Grok Build upstream diagnostic", got)
	}
	if !strings.Contains(got, "XAI_API_KEY") {
		t.Fatalf("NormalizeError = %q, want XAI_API_KEY recovery hint", got)
	}
	if strings.Contains(got, `{"code"`) {
		t.Fatalf("NormalizeError = %q, want parsed message instead of raw JSON", got)
	}
}

func hasCredentialMode(adapter Adapter, id string, cloudAllowed bool, envKeys ...string) bool {
	for _, mode := range adapter.CredentialModes {
		if mode.ID != id || mode.RemoteAllowed != cloudAllowed {
			continue
		}
		for _, envKey := range envKeys {
			found := false
			for _, got := range mode.EnvKeys {
				if got == envKey {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}
	return false
}

func assertNotPackageRunnerCommand(t testing.TB, label string, command string) {
	t.Helper()
	switch command {
	case "npx", "npm", "node", "bun", "sh", "bash", "zsh", "cmd", "powershell", "pwsh":
		t.Fatalf("%s command = %q, want direct binary without package runner or shell", label, command)
	}
}

func remoteIdentityContext() context.Context {
	return remoteruntime.WithIdentity(context.Background(), remoteruntime.Identity{
		ActorID:   "actor_test",
		OrgID:     "org_test",
		ProjectID: "project_test",
		RuntimeID: "runtime_test",
	})
}
