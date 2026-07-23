package agentadapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DetectAuthStatus is an explicit diagnostic. Most branches inspect local
// credential hints, while Claude may run its bounded `auth status` command.
// Callers must keep it behind an operator-owned execution boundary.
func DetectAuthStatus(adapter Adapter) (string, string) {
	switch adapter.ID {
	case "codex":
		if envAny("OPENAI_API_KEY", "CODEX_AUTH_TOKEN", "CODEX_API_KEY") || fileAny("${HOME}/.codex/auth.json") {
			return AuthStatusOK, ""
		}
		return AuthStatusUnauthenticated, "Run codex login, or set OPENAI_API_KEY for the adapter environment."
	case "claude_code":
		if envAny("ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN") {
			return AuthStatusOK, ""
		}
		if ok, checked := detectClaudeCLIAuthStatus(); ok {
			return AuthStatusOK, ""
		} else if checked {
			return AuthStatusUnauthenticated, adapterSignInHint(adapter)
		}
		if fileAny("${HOME}/.claude.json", "${HOME}/.claude/settings.json", "${HOME}/.claude/.credentials.json") {
			return AuthStatusUnknown, "Claude Code config is present on disk. Hecate verifies CLI auth when Claude Code handles the first message; Connections diagnostics are optional."
		}
		return AuthStatusUnknown, "Send a message in a Claude Code chat to verify it. If it reports a sign-in error, run `claude /login` in Terminal or set ANTHROPIC_API_KEY or ANTHROPIC_AUTH_TOKEN for the adapter environment."
	case "cursor_agent":
		if envAny("CURSOR_API_KEY") || fileAny("${HOME}/.cursor", "${HOME}/Library/Application Support/Cursor") {
			return AuthStatusOK, ""
		}
		return AuthStatusUnauthenticated, "Run cursor-agent login, or set CURSOR_API_KEY for the adapter environment."
	case "grok_build":
		if envAny("XAI_API_KEY", "PROVIDER_XAI_API_KEY") || fileAny("${HOME}/.grok") {
			return AuthStatusOK, ""
		}
		return AuthStatusUnauthenticated, "Run grok login, or set XAI_API_KEY for the adapter environment."
	default:
		return AuthStatusUnknown, "No auth heuristic is available for this adapter."
	}
}

func adapterSignInHint(adapter Adapter) string {
	switch adapter.ID {
	case "codex":
		return "Run codex login, or set OPENAI_API_KEY for the adapter environment."
	case "claude_code":
		return "Run `claude /login` in Terminal, or set ANTHROPIC_API_KEY or ANTHROPIC_AUTH_TOKEN for the adapter environment."
	case "cursor_agent":
		return "Run cursor-agent login, or set CURSOR_API_KEY for the adapter environment."
	case "grok_build":
		return "Run grok login, or set XAI_API_KEY for the adapter environment."
	default:
		return fmt.Sprintf("Sign in to %s, then retry the chat.", adapter.Name)
	}
}

func adapterAppMissingHint(adapter Adapter) string {
	switch adapter.ID {
	case "codex":
		return "Install Codex CLI, then sign in with Codex."
	case "claude_code":
		return "Install Claude Code, then sign in with Claude Code."
	case "cursor_agent":
		return "Install Cursor with Agent support, then sign in with Cursor Agent."
	case "grok_build":
		return "Install Grok Build, then sign in with Grok."
	default:
		return fmt.Sprintf("Install %s, then start a chat to verify it.", adapter.Name)
	}
}

// claudeCodeAuthErrorMessage is the user-facing message rendered in the chat
// when a Claude Code Chat Turn fails because the adapter couldn't sign in. The
// UI pattern-matches the marker token to render the Connections shortcut.
//
// Keep `claude_code_auth_required` verbatim. TranscriptMessageRow strips the
// trailing marker from visible copy; ChatView uses it to decide whether to show
// the button.
func claudeCodeAuthErrorMessage() string {
	return "Claude Code isn't signed in. This is separate from the Anthropic key in the Providers tab — Claude Code runs as its own program and uses the operator's local Claude auth. Run `claude /login`, or set ANTHROPIC_API_KEY or ANTHROPIC_AUTH_TOKEN for the adapter environment, then retry the chat. (claude_code_auth_required)"
}

var runClaudeAuthStatus = defaultRunClaudeAuthStatus

type claudeAuthStatusPayload struct {
	LoggedIn bool `json:"loggedIn"`
}

func detectClaudeCLIAuthStatus() (ok bool, checked bool) {
	out, _ := runClaudeAuthStatus()
	if strings.TrimSpace(out) == "" {
		return false, false
	}
	var payload claudeAuthStatusPayload
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		return false, false
	}
	return payload.LoggedIn, true
}

func defaultRunClaudeAuthStatus() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	adapter, ok := BuiltInByID("claude_code")
	if !ok {
		return "", errors.New("Claude Code adapter is unavailable")
	}
	path, err := resolveExecutable(adapter, nil)
	if err != nil {
		return "", err
	}
	processEnv, err := prepareAdapterProcessEnv(ctx, adapter, os.Environ())
	if err != nil {
		return "", err
	}
	if processEnv.cleanup != nil {
		defer processEnv.cleanup()
	}
	out, err := runAgentDiagnostic(ctx, path, []string{"auth", "status"}, processEnv.values)
	return out.stdout, err
}

func envAny(names ...string) bool {
	for _, name := range names {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return true
		}
	}
	return false
}

func fileAny(paths ...string) bool {
	for _, path := range paths {
		resolved := expandPath(path)
		if resolved == "" {
			continue
		}
		if _, err := os.Stat(filepath.Clean(resolved)); err == nil {
			return true
		}
	}
	return false
}
