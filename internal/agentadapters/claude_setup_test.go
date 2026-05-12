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

	status = DetectClaudeCodeCLI(func(file string) (string, error) {
		switch file {
		case "claude":
			return "", errors.New("not found")
		case "npx":
			return "/tmp/bin/npx", nil
		default:
			t.Fatalf("lookup called with %q", file)
			return "", errors.New("unexpected")
		}
	})
	if !status.Available || status.Command != "/tmp/bin/npx -y @anthropic-ai/claude-code" || status.ExecutablePath != "/tmp/bin/npx" {
		t.Fatalf("DetectClaudeCodeCLI() = %+v, want npx-managed path", status)
	}

	status = DetectClaudeCodeCLI(func(string) (string, error) {
		return "", errors.New("not found")
	})
	if status.Available || status.Command != "" || status.ExecutablePath != "" {
		t.Fatalf("DetectClaudeCodeCLI() = %+v, want unavailable", status)
	}
}
