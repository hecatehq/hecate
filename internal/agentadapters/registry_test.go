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

	if got := found["codex"]; got.Command != "codex-acp" || got.Kind != DriverKindACP || got.CostMode != "external" {
		t.Fatalf("codex adapter = %#v", got)
	}
	if got := found["claude_code"]; got.Command != "claude-agent-acp" || got.Kind != DriverKindACP || got.CostMode != "external" {
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
