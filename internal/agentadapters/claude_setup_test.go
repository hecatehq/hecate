package agentadapters

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDetectClaudeCodeCLI(t *testing.T) {
	status := DetectClaudeCodeCLI(VersionProbe{Command: "claude"}, func(file string) (string, error) {
		if file != "claude" {
			t.Fatalf("lookup called with %q", file)
		}
		return "/tmp/bin/claude", nil
	})
	if !status.Available || status.Command != "/tmp/bin/claude" || status.ExecutablePath != "/tmp/bin/claude" {
		t.Fatalf("DetectClaudeCodeCLI() = %+v, want available path", status)
	}

	status = DetectClaudeCodeCLI(VersionProbe{Command: "claude"}, func(string) (string, error) {
		return "", errors.New("not found")
	})
	if status.Available || status.Command != "" || status.ExecutablePath != "" {
		t.Fatalf("DetectClaudeCodeCLI() = %+v, want unavailable", status)
	}
}

func TestDetectClaudeCodeCLI_DoesNotFallbackToNPX(t *testing.T) {
	status := DetectClaudeCodeCLI(VersionProbe{Command: "claude"}, func(file string) (string, error) {
		switch file {
		case "claude":
			return "", errors.New("not found")
		case "npx":
			t.Fatal("lookup should not probe npx")
		default:
			t.Fatalf("lookup called with %q", file)
		}
		return "", errors.New("unexpected")
	})
	if status.Available || status.Command != "" || status.ExecutablePath != "" {
		t.Fatalf("DetectClaudeCodeCLI() = %+v, want unavailable without npx fallback", status)
	}
}

func TestDetectClaudeCodeCLIUsesCandidatePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	want := filepath.Join(home, ".volta", "bin", "claude")
	if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
		t.Fatalf("mkdir candidate directory: %v", err)
	}
	if err := os.WriteFile(want, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write candidate: %v", err)
	}

	status := DetectClaudeCodeCLI(VersionProbe{
		Command:        "claude",
		CandidatePaths: []string{"${HOME}/.volta/bin/claude"},
	}, func(file string) (string, error) {
		if file == want {
			return file, nil
		}
		return "", errors.New("not found")
	})
	if !status.Available || status.ExecutablePath != want {
		t.Fatalf("DetectClaudeCodeCLI() = %+v, want candidate %q", status, want)
	}
}
