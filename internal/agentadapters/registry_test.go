package agentadapters

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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

	if got := found["codex"]; got.Command != "codex" || got.Kind != "process" || got.CostMode != "external" {
		t.Fatalf("codex adapter = %#v", got)
	}
	if got := found["claude_code"]; got.Command != "claude" || got.Kind != "process" || got.CostMode != "external" {
		t.Fatalf("claude_code adapter = %#v", got)
	}
	if got := found["cursor_agent"]; got.Command != "cursor-agent" || got.Kind != "process" || got.CostMode != "external" {
		t.Fatalf("cursor_agent adapter = %#v", got)
	}
}

func TestListWithLookupReportsAvailability(t *testing.T) {
	t.Parallel()

	response := ListWithLookup(context.Background(), func(file string) (string, error) {
		if file == "codex" {
			return "/usr/local/bin/codex", nil
		}
		return "", errors.New("not found")
	})

	byID := map[string]Status{}
	for _, item := range response {
		byID[item.ID] = item
	}

	codex := byID["codex"]
	if !codex.Available || codex.Status != StatusAvailable || codex.Path != "/usr/local/bin/codex" {
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
	exe := filepath.Join(dir, "codex")
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
	exe := filepath.Join(bin, "codex")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}

	path, err := resolveExecutable(Adapter{
		ID:             "codex",
		Command:        "codex-missing",
		CandidatePaths: []string{"${HOME}/.local/bin/codex"},
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

func TestCommandForAdapterPinsHeadlessInvocations(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"codex", "claude", "cursor-agent"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	tests := []struct {
		name    string
		adapter Adapter
		want    []string
	}{
		{
			name:    "codex",
			adapter: Adapter{ID: "codex", Command: "codex"},
			want: []string{
				"--ask-for-approval", "never",
				"exec",
				"--cd", "/tmp/workspace",
				"--sandbox", "workspace-write",
				"--json",
				"hello",
			},
		},
		{
			name:    "claude_code",
			adapter: Adapter{ID: "claude_code", Command: "claude"},
			want: []string{
				"-p",
				"--permission-mode", "acceptEdits",
				"--output-format", "text",
				"hello",
			},
		},
		{
			name:    "cursor_agent",
			adapter: Adapter{ID: "cursor_agent", Command: "cursor-agent"},
			want: []string{
				"--print",
				"--output-format", "text",
				"--workspace", "/tmp/workspace",
				"--trust",
				"--force",
				"hello",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command, args, err := commandForAdapter(tt.adapter, "/tmp/workspace", "hello")
			if err != nil {
				t.Fatalf("commandForAdapter: %v", err)
			}
			if filepath.Base(command) != tt.adapter.Command {
				t.Fatalf("command = %q, want executable named %q", command, tt.adapter.Command)
			}
			if !reflect.DeepEqual(args, tt.want) {
				t.Fatalf("args = %#v, want %#v", args, tt.want)
			}
		})
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
