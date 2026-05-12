package agentadapters

import (
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

func TestDetectAuthStatusClaudeUnknownWithoutMarker(t *testing.T) {
	isolatedAuthHome(t)
	withClaudeAuthStatus(t, "", os.ErrNotExist)

	status, hint := DetectAuthStatus(Adapter{ID: "claude_code"})
	if status != AuthStatusUnknown {
		t.Fatalf("status = %q, want %q", status, AuthStatusUnknown)
	}
	// The hint points at the in-app guided setup card rather than
	// enumerating env vars. The card itself documents the env-var
	// alternative for power users.
	if !strings.Contains(hint, "guided setup card") {
		t.Fatalf("hint = %q, want guided-setup card guidance", hint)
	}
	if !strings.Contains(hint, "claude setup-token") {
		t.Fatalf("hint = %q, want the `claude setup-token` command callout", hint)
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
	// Same in-app guidance — but mention the on-disk config so the
	// operator understands why we still flag this as needing
	// verification despite the file being present.
	if !strings.Contains(hint, "could not verify the CLI auth status") || !strings.Contains(hint, "guided setup card") {
		t.Fatalf("hint = %q, want CLI-verification guided-setup guidance", hint)
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
	if !strings.Contains(hint, "claude auth login") {
		t.Fatalf("hint = %q, want claude auth login guidance", hint)
	}
}

func TestDetectAuthStatusClaudeUsesAdapterVisibleAuth(t *testing.T) {
	isolatedAuthHome(t)
	withClaudeAuthStatus(t, `{"loggedIn":false}`, nil)
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "oauth-token")

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
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	t.Setenv("CURSOR_API_KEY", "")
	return home
}

func withClaudeAuthStatus(t *testing.T, output string, err error) {
	t.Helper()
	old := runClaudeAuthStatus
	runClaudeAuthStatus = func() (string, error) { return output, err }
	t.Cleanup(func() { runClaudeAuthStatus = old })
}
