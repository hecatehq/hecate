package agentadapters

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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

	if got := found["codex"]; got.Command != "codex-acp" || got.Kind != DriverKindACP || got.Managed.Package != "@zed-industries/codex-acp" || got.CostMode != "external" {
		t.Fatalf("codex adapter = %#v", got)
	}
	if got := found["claude_code"]; got.Command != "claude-agent-acp" || got.Kind != DriverKindACP || got.Managed.Package != "@agentclientprotocol/claude-agent-acp" || got.CostMode != "external" {
		t.Fatalf("claude_code adapter = %#v", got)
	}
	if got := found["cursor_agent"]; got.Command != "cursor-agent" || got.Kind != DriverKindACP || got.CostMode != "external" {
		t.Fatalf("cursor_agent adapter = %#v", got)
	}
	if got := found["cursor_agent"]; len(got.Args) != 1 || got.Args[0] != "acp" {
		t.Fatalf("cursor_agent adapter = %#v", got)
	}
	if got := found["grok_build"]; got.Command != "grok" || got.Kind != DriverKindACP || got.CostMode != "external" {
		t.Fatalf("grok_build adapter = %#v", got)
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
}

func TestListWithLookupReportsAvailability(t *testing.T) {
	t.Parallel()

	response := ListWithLookup(context.Background(), func(file string) (string, error) {
		if file == "codex-acp" {
			return "/usr/local/bin/codex-acp", nil
		}
		return "", errors.New("not found")
	})

	byID := map[string]Status{}
	for _, item := range response {
		byID[item.ID] = item
	}

	codex := byID["codex"]
	if !codex.Available || codex.Status != StatusAvailable || codex.Path != "/usr/local/bin/codex-acp" {
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
	}, statusProbeOptions{})
	if status.Available || status.Status != StatusMissing || !strings.Contains(status.Error, "not found") {
		t.Fatalf("status = %#v, want normal missing lookup", status)
	}
}

func TestListWithLookupUsesCandidatePathFallback(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	exe := filepath.Join(dir, "codex-acp")
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
	exe := filepath.Join(bin, "codex-acp")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}

	path, err := resolveExecutable(Adapter{
		ID:             "codex",
		Command:        "codex-missing",
		CandidatePaths: []string{"${HOME}/.local/bin/codex-acp"},
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

func TestResolveExecutableForStatusReportsManagedLauncherWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HECATE_AGENT_ADAPTERS_DIR", dir)

	adapter := Adapter{
		ID:      "codex",
		Command: "codex-acp",
		Managed: ManagedLauncher{
			Package: "@zed-industries/codex-acp",
			Runners: []ManagedRunner{{Command: "npx", Args: []string{"-y", "@zed-industries/codex-acp"}}},
		},
	}
	path, err := resolveExecutableForStatus(adapter, func(file string) (string, error) {
		if file == "npx" {
			return "/usr/local/bin/npx", nil
		}
		return "", errors.New("not found on PATH")
	})
	if err != nil {
		t.Fatalf("resolve executable for status: %v", err)
	}
	want := filepath.Join(dir, managedLauncherName("codex-acp"))
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("managed status lookup wrote launcher, stat error = %v", err)
	}
}

func TestShouldProbeVersionForStatusSkipsPlannedManagedLauncher(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HECATE_AGENT_ADAPTERS_DIR", dir)

	adapter := Adapter{
		ID:      "codex",
		Command: "codex-acp",
		Managed: ManagedLauncher{
			Package: "@zed-industries/codex-acp",
			Runners: []ManagedRunner{{Command: "npx", Args: []string{"-y", "@zed-industries/codex-acp"}}},
		},
	}
	path := filepath.Join(dir, managedLauncherName("codex-acp"))
	if err := os.WriteFile(path, []byte("#!/bin/sh\nsleep 60\n"), 0o755); err != nil {
		t.Fatalf("write planned launcher: %v", err)
	}

	lookup := func(file string) (string, error) {
		if file == "npx" {
			return "/usr/local/bin/npx", nil
		}
		return "", errors.New("not found on PATH")
	}
	if shouldProbeAdapterVersionForStatus(adapter, path, lookup, false) {
		t.Fatalf("shouldProbeAdapterVersionForStatus returned true for planned managed launcher")
	}
}

func TestStatusForAdapterDoesNotRunManagedPackageForVersion(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HECATE_AGENT_ADAPTERS_DIR", dir)
	marker := filepath.Join(dir, "runner-called")

	runnerPath := writeGoHelperBinary(t, dir, "fake-npx", fmt.Sprintf(`package main

import "os"

func main() {
	if err := os.WriteFile(%q, []byte("called"), 0o644); err != nil {
		panic(err)
	}
}
`, marker))
	adapter := Adapter{
		ID:      "managed_test",
		Name:    "Managed Test",
		Command: "managed-test-acp",
		Managed: ManagedLauncher{
			Package: "@example/managed-test-acp",
			Runners: []ManagedRunner{{Command: "npx", Args: []string{"-y", "@example/managed-test-acp"}}},
		},
		SupportedRange: ">=4.0.0",
	}
	status := statusForAdapter(context.Background(), adapter, func(file string) (string, error) {
		if file == "npx" {
			return runnerPath, nil
		}
		return "", errors.New("not found on PATH")
	}, statusProbeOptions{})
	if !status.Available {
		t.Fatalf("status = %#v, want managed adapter available", status)
	}
	if status.AdapterVersion != "" {
		t.Fatalf("status.AdapterVersion = %q, want passive status to avoid package-manager execution", status.AdapterVersion)
	}
	if status.VersionOutsideRange {
		t.Fatalf("status.VersionOutsideRange = true, want false when version is unknown")
	}
	if _, err := os.Stat(status.Path); !os.IsNotExist(err) {
		t.Fatalf("managed status lookup wrote launcher at %q, stat error = %v", status.Path, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("passive managed status probe executed package runner, marker stat error = %v", err)
	}
}

func TestStatusForAdapterReportsManagedAgentVersionWithoutRunningPackage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HECATE_AGENT_ADAPTERS_DIR", dir)
	marker := filepath.Join(dir, "runner-called")
	agent := writeFakeBinary(t, dir, "codex", "codex 9.8.7")
	runnerPath := writeGoHelperBinary(t, dir, "fake-npx", fmt.Sprintf(`package main

import "os"

func main() {
	if err := os.WriteFile(%q, []byte("called"), 0o644); err != nil {
		panic(err)
	}
}
`, marker))
	adapter := Adapter{
		ID:      "managed_test",
		Name:    "Managed Test",
		Command: "managed-test-acp",
		AgentVersion: VersionProbe{
			Command: "codex",
			Args:    []string{"--version"},
		},
		Managed: ManagedLauncher{
			Package: "@example/managed-test-acp",
			Runners: []ManagedRunner{{Command: "npx", Args: []string{"-y", "@example/managed-test-acp"}}},
		},
		SupportedRange: ">=4.0.0",
	}
	status := statusForAdapter(context.Background(), adapter, func(file string) (string, error) {
		switch file {
		case "codex":
			return agent, nil
		case "npx":
			return runnerPath, nil
		default:
			return "", errors.New("not found on PATH")
		}
	}, statusProbeOptions{})
	if status.AgentVersion != "9.8.7" {
		t.Fatalf("status.AgentVersion = %q, want 9.8.7", status.AgentVersion)
	}
	if status.AdapterVersion != "" {
		t.Fatalf("status.AdapterVersion = %q, want passive status to avoid package-manager execution", status.AdapterVersion)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("passive managed status probe executed package runner, marker stat error = %v", err)
	}
}

func TestStatusForAdapterExplicitProbeAllowsManagedAdapterVersion(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HECATE_AGENT_ADAPTERS_DIR", dir)

	adapter := Adapter{
		ID:      "managed_test",
		Name:    "Managed Test",
		Command: "managed-test-acp",
		Managed: ManagedLauncher{
			Package: "@example/managed-test-acp",
			Runners: []ManagedRunner{{Command: "npx", Args: []string{"-y", "@example/managed-test-acp"}}},
		},
		SupportedRange: ">=4.0.0",
	}
	path := filepath.Join(dir, managedLauncherName(adapter.Command))
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho managed-test-acp 4.5.6\n"), 0o755); err != nil {
		t.Fatalf("write planned launcher: %v", err)
	}
	status := statusForAdapter(context.Background(), adapter, func(file string) (string, error) {
		if file == "npx" {
			return "/usr/local/bin/npx", nil
		}
		return "", errors.New("not found on PATH")
	}, statusProbeOptions{allowManagedAdapterVersion: true})
	if status.AdapterVersion != "4.5.6" {
		t.Fatalf("status.AdapterVersion = %q, want 4.5.6", status.AdapterVersion)
	}
	if status.VersionOutsideRange {
		t.Fatalf("status.VersionOutsideRange = true, want version in supported range")
	}
}

func TestShouldProbeVersionForStatusAllowsDirectBinary(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "codex-acp")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\necho 1.2.3\n"), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}

	if !shouldProbeAdapterVersionForStatus(Adapter{Command: "codex-acp"}, exe, func(file string) (string, error) {
		return "", errors.New("not found on PATH")
	}, false) {
		t.Fatalf("shouldProbeAdapterVersionForStatus returned false for direct binary")
	}
}

func TestResolveExecutableCreatesManagedLauncher(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HECATE_AGENT_ADAPTERS_DIR", dir)

	adapter := Adapter{
		ID:      "codex",
		Command: "codex-acp",
		Managed: ManagedLauncher{
			Package: "@zed-industries/codex-acp",
			Runners: []ManagedRunner{{Command: "npx", Args: []string{"-y", "@zed-industries/codex-acp"}}},
		},
	}
	path, err := resolveExecutable(adapter, func(file string) (string, error) {
		if file == "npx" {
			return "/usr/local/bin/npx", nil
		}
		return "", errors.New("not found on PATH")
	})
	if err != nil {
		t.Fatalf("resolve executable: %v", err)
	}
	want := filepath.Join(dir, managedLauncherName("codex-acp"))
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat launcher: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("launcher mode = %v, want executable", info.Mode())
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read launcher: %v", err)
	}
	text := string(content)
	if !strings.Contains(text, "/usr/local/bin/npx") || !strings.Contains(text, "@zed-industries/codex-acp") {
		t.Fatalf("launcher content = %q", text)
	}
}

func TestRefreshManagedLauncherRewritesLauncher(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HECATE_AGENT_ADAPTERS_DIR", dir)

	stale := filepath.Join(dir, managedLauncherName("codex-acp"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir launcher dir: %v", err)
	}
	if err := os.WriteFile(stale, []byte("#!/bin/sh\nexit 99\n"), 0o755); err != nil {
		t.Fatalf("write stale launcher: %v", err)
	}

	status, err := RefreshManagedLauncher(context.Background(), "codex", func(file string) (string, error) {
		if file == "npx" {
			return "/usr/local/bin/npx", nil
		}
		return "", errors.New("not found on PATH")
	})
	if err != nil {
		t.Fatalf("RefreshManagedLauncher: %v", err)
	}
	if !status.Available || status.Path != stale {
		t.Fatalf("status = %#v, want available refreshed launcher at %q", status, stale)
	}
	content, err := os.ReadFile(stale)
	if err != nil {
		t.Fatalf("read refreshed launcher: %v", err)
	}
	text := string(content)
	if strings.Contains(text, "exit 99") || !strings.Contains(text, "@zed-industries/codex-acp") {
		t.Fatalf("refreshed launcher content = %q", text)
	}
}

func TestGCManagedLaunchersRemovesStaleFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HECATE_AGENT_ADAPTERS_DIR", dir)

	known := filepath.Join(dir, managedLauncherName("codex-acp"))
	stale := filepath.Join(dir, "old-adapter-acp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir launcher dir: %v", err)
	}
	if err := os.WriteFile(known, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write known launcher: %v", err)
	}
	if err := os.WriteFile(stale, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write stale launcher: %v", err)
	}

	removed, err := GCManagedLaunchers()
	if err != nil {
		t.Fatalf("GCManagedLaunchers: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(known); err != nil {
		t.Fatalf("known launcher stat: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale launcher stat error = %v, want not exist", err)
	}
}

func TestResolveManagedRunnerUsesCandidatePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := filepath.Join(home, ".volta", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir volta bin: %v", err)
	}
	npx := filepath.Join(bin, "npx")
	if err := os.WriteFile(npx, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write npx: %v", err)
	}

	_, path, err := resolveManagedRunner(ManagedLauncher{
		Package: "@zed-industries/codex-acp",
		Runners: []ManagedRunner{{Command: "npx", CandidatePaths: []string{"${HOME}/.volta/bin/npx"}}},
	}, func(file string) (string, error) {
		return "", errors.New("not found on PATH")
	})
	if err != nil {
		t.Fatalf("resolve managed runner: %v", err)
	}
	if path != npx {
		t.Fatalf("path = %q, want %q", path, npx)
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

func TestSanitizedEnvPreservesAgentAndRuntimeEssentials(t *testing.T) {
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
		"HECATE_AUTH_TOKEN=secret",
	})

	got := map[string]bool{}
	for _, item := range env {
		got[item] = true
	}
	for _, want := range []string{
		"PATH=/bin",
		"HOME=/Users/alice",
		"OPENAI_API_KEY=sk-test",
		"ANTHROPIC_API_KEY=sk-ant-test",
		"CLAUDE_CONFIG_DIR=/tmp/claude",
		"CODEX_HOME=/tmp/codex",
		"CURSOR_API_KEY=cursor-test",
		"VOLTA_HOME=/Users/alice/.volta",
	} {
		if !got[want] {
			t.Fatalf("missing allowed env %q in %#v", want, env)
		}
	}
	if got["HECATE_AUTH_TOKEN=secret"] {
		t.Fatalf("gateway secret leaked into adapter env: %#v", env)
	}
	if got["XAI_API_KEY=xai-test"] {
		t.Fatalf("xAI secret leaked into generic adapter env: %#v", env)
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
