package agentadapters

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectAuthStatusCodex(t *testing.T) {
	home := isolatedAuthHome(t)
	adapter := Adapter{ID: "codex"}

	status, hint := DetectAuthStatus(adapter)
	if status != AuthStatusUnauthenticated {
		t.Fatalf("status = %q, want %q", status, AuthStatusUnauthenticated)
	}
	if !strings.Contains(hint, "codex login") {
		t.Fatalf("hint = %q, want codex login guidance", hint)
	}

	authPath := filepath.Join(home, ".codex", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o755); err != nil {
		t.Fatalf("mkdir auth dir: %v", err)
	}
	if err := os.WriteFile(authPath, []byte(`{"token":"test"}`), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}

	status, hint = DetectAuthStatus(adapter)
	if status != AuthStatusOK || hint != "" {
		t.Fatalf("status/hint = %q/%q, want ok/empty", status, hint)
	}
}

func TestDetectAuthStatusCursorUsesEnv(t *testing.T) {
	isolatedAuthHome(t)
	t.Setenv("CURSOR_API_KEY", "cursor-test-key")

	status, hint := DetectAuthStatus(Adapter{ID: "cursor_agent"})
	if status != AuthStatusOK || hint != "" {
		t.Fatalf("status/hint = %q/%q, want ok/empty", status, hint)
	}
}

func TestDetectAuthStatusGrokBuild(t *testing.T) {
	home := isolatedAuthHome(t)

	status, hint := DetectAuthStatus(Adapter{ID: "grok_build"})
	if status != AuthStatusUnauthenticated {
		t.Fatalf("status = %q, want %q", status, AuthStatusUnauthenticated)
	}
	if !strings.Contains(hint, "grok login") || !strings.Contains(hint, "XAI_API_KEY") {
		t.Fatalf("hint = %q, want grok login and XAI_API_KEY guidance", hint)
	}

	t.Setenv("XAI_API_KEY", "xai-test-key")
	status, hint = DetectAuthStatus(Adapter{ID: "grok_build"})
	if status != AuthStatusOK || hint != "" {
		t.Fatalf("status/hint with env = %q/%q, want ok/empty", status, hint)
	}

	t.Setenv("XAI_API_KEY", "")
	t.Setenv("PROVIDER_XAI_API_KEY", "provider-xai-test-key")
	status, hint = DetectAuthStatus(Adapter{ID: "grok_build"})
	if status != AuthStatusOK || hint != "" {
		t.Fatalf("status/hint with provider env = %q/%q, want ok/empty", status, hint)
	}

	t.Setenv("PROVIDER_XAI_API_KEY", "")
	if err := os.MkdirAll(filepath.Join(home, ".grok"), 0o700); err != nil {
		t.Fatalf("mkdir grok config: %v", err)
	}
	status, hint = DetectAuthStatus(Adapter{ID: "grok_build"})
	if status != AuthStatusOK || hint != "" {
		t.Fatalf("status/hint with config = %q/%q, want ok/empty", status, hint)
	}
}

func TestDetectAuthStatusClaudeUnknownWithoutMarker(t *testing.T) {
	isolatedAuthHome(t)
	withClaudeAuthStatus(t, "", os.ErrNotExist)

	status, hint := DetectAuthStatus(Adapter{ID: "claude_code"})
	if status != AuthStatusUnknown {
		t.Fatalf("status = %q, want %q", status, AuthStatusUnknown)
	}
	if !strings.Contains(hint, "Open Connections") {
		t.Fatalf("hint = %q, want Connections guidance", hint)
	}
	if !strings.Contains(hint, "claude /login") {
		t.Fatalf("hint = %q, want the `claude /login` command callout", hint)
	}
	if !strings.Contains(hint, "ANTHROPIC_API_KEY") || !strings.Contains(hint, "ANTHROPIC_AUTH_TOKEN") {
		t.Fatalf("hint = %q, want Anthropic env auth alternatives", hint)
	}
}

func TestDetectAuthStatusClaudeConfigIsNotEnoughForACP(t *testing.T) {
	home := isolatedAuthHome(t)
	withClaudeAuthStatus(t, "", os.ErrNotExist)
	configPath := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(configPath, []byte(`{"hasCompletedOnboarding":true}`), 0o600); err != nil {
		t.Fatalf("write claude config: %v", err)
	}

	status, hint := DetectAuthStatus(Adapter{ID: "claude_code"})
	if status != AuthStatusUnknown {
		t.Fatalf("status = %q, want %q", status, AuthStatusUnknown)
	}
	if !strings.Contains(hint, "has not verified CLI auth yet") ||
		!strings.Contains(hint, "Open Connections") {
		t.Fatalf("hint = %q, want CLI-verification Connections guidance", hint)
	}
}

func TestDetectAuthStatusClaudeUsesCLIAuthStatus(t *testing.T) {
	isolatedAuthHome(t)
	withClaudeAuthStatus(t, `{"loggedIn":true,"authMethod":"claude.ai"}`, nil)

	status, hint := DetectAuthStatus(Adapter{ID: "claude_code"})
	if status != AuthStatusOK || hint != "" {
		t.Fatalf("status/hint = %q/%q, want ok/empty", status, hint)
	}
}

func TestDetectAuthStatusClaudeReportsUnauthenticatedFromCLI(t *testing.T) {
	isolatedAuthHome(t)
	withClaudeAuthStatus(t, `{"loggedIn":false}`, nil)

	status, hint := DetectAuthStatus(Adapter{ID: "claude_code"})
	if status != AuthStatusUnauthenticated {
		t.Fatalf("status = %q, want %q", status, AuthStatusUnauthenticated)
	}
	if !strings.Contains(hint, "claude /login") {
		t.Fatalf("hint = %q, want claude /login guidance", hint)
	}
	if !strings.Contains(hint, "ANTHROPIC_API_KEY") || !strings.Contains(hint, "ANTHROPIC_AUTH_TOKEN") {
		t.Fatalf("hint = %q, want Anthropic env auth alternatives", hint)
	}
}

func TestDetectAuthStatusClaudeParsesStatusOutputOnNonZeroExit(t *testing.T) {
	isolatedAuthHome(t)
	withClaudeAuthStatus(t, `{"loggedIn":false,"authMethod":"none"}`, errors.New("exit status 1"))

	status, hint := DetectAuthStatus(Adapter{ID: "claude_code"})
	if status != AuthStatusUnauthenticated {
		t.Fatalf("status = %q, want %q", status, AuthStatusUnauthenticated)
	}
	if !strings.Contains(hint, "claude /login") {
		t.Fatalf("hint = %q, want claude /login guidance", hint)
	}
	if !strings.Contains(hint, "ANTHROPIC_API_KEY") || !strings.Contains(hint, "ANTHROPIC_AUTH_TOKEN") {
		t.Fatalf("hint = %q, want Anthropic env auth alternatives", hint)
	}
}

func TestDetectAuthStatusClaudeUsesInheritedAnthropicAuth(t *testing.T) {
	isolatedAuthHome(t)
	withClaudeAuthStatus(t, `{"loggedIn":false}`, nil)
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "auth-token")

	status, hint := DetectAuthStatus(Adapter{ID: "claude_code"})
	if status != AuthStatusOK || hint != "" {
		t.Fatalf("status/hint = %q/%q, want ok/empty", status, hint)
	}
}

func isolatedAuthHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("CODEX_AUTH_TOKEN", "")
	t.Setenv("CODEX_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("CURSOR_API_KEY", "")
	t.Setenv("XAI_API_KEY", "")
	t.Setenv("PROVIDER_XAI_API_KEY", "")
	return home
}

func withClaudeAuthStatus(t *testing.T, output string, err error) {
	t.Helper()
	old := runClaudeAuthStatus
	runClaudeAuthStatus = func() (string, error) { return output, err }
	t.Cleanup(func() { runClaudeAuthStatus = old })
}
