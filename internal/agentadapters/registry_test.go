package agentadapters

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltInsIncludeInitialExternalAgents(t *testing.T) {
	t.Parallel()

	items := BuiltIns()
	if len(items) != 3 {
		t.Fatalf("built-in adapter count = %d, want 3", len(items))
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
		"VOLTA_HOME=/Users/alice/.volta",
		"GATEWAY_AUTH_TOKEN=secret",
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
	if got["GATEWAY_AUTH_TOKEN=secret"] {
		t.Fatalf("gateway secret leaked into adapter env: %#v", env)
	}
}

func TestMergeEnvOverridesSanitizedBase(t *testing.T) {
	t.Parallel()

	got := mergeEnv([]string{
		"PATH=/bin",
		"CLAUDE_CODE_OAUTH_TOKEN=old",
		"HOME=/tmp/home",
	}, []string{
		"CLAUDE_CODE_OAUTH_TOKEN=new",
		"BAD_ENTRY",
		"CURSOR_API_KEY=cursor-token",
	})
	want := []string{
		"PATH=/bin",
		"HOME=/tmp/home",
		"CLAUDE_CODE_OAUTH_TOKEN=new",
		"CURSOR_API_KEY=cursor-token",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("mergeEnv() = %#v, want %#v", got, want)
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
