package agentadapters

import (
	"errors"
	"testing"
)

func TestDetectClaudeCodeCLI(t *testing.T) {
	status := DetectClaudeCodeCLI(func(file string) (string, error) {
		if file != "claude" {
			t.Fatalf("lookup called with %q", file)
		}
		return "/tmp/bin/claude", nil
	})
	if !status.Available || status.Command != "/tmp/bin/claude" || status.ExecutablePath != "/tmp/bin/claude" {
		t.Fatalf("DetectClaudeCodeCLI() = %+v, want available path", status)
	}

	status = DetectClaudeCodeCLI(func(string) (string, error) {
		return "", errors.New("not found")
	})
	if status.Available || status.Command != "" || status.ExecutablePath != "" {
		t.Fatalf("DetectClaudeCodeCLI() = %+v, want unavailable", status)
	}
}

func TestDetectClaudeCodeCLI_DoesNotFallbackToNPX(t *testing.T) {
	status := DetectClaudeCodeCLI(func(file string) (string, error) {
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
