package agentadapters

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DetectAuthStatus is a lightweight dashboard hint. It deliberately avoids
// spawning the adapter; Settings refreshes the full ACP probe when opened.
func DetectAuthStatus(adapter Adapter) (string, string) {
	switch adapter.ID {
	case "codex":
		if envAny("OPENAI_API_KEY", "CODEX_AUTH_TOKEN", "CODEX_API_KEY") || fileAny("${HOME}/.codex/auth.json") {
			return AuthStatusOK, ""
		}
		return AuthStatusUnauthenticated, "Run codex login, or set OPENAI_API_KEY for the adapter environment."
	case "claude_code":
		if envAny("ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN") {
			return AuthStatusOK, ""
		}
		if ok, checked := detectClaudeCLIAuthStatus(); ok {
			return AuthStatusOK, ""
		} else if checked {
			return AuthStatusUnauthenticated, "Run `claude auth login` or use the guided setup card below to paste the setup token from `claude setup-token`."
		}
		if fileAny("${HOME}/.claude.json", "${HOME}/.claude/settings.json", "${HOME}/.claude/.credentials.json") {
			return AuthStatusUnknown, "Claude Code config is present on disk, but Hecate could not verify the CLI auth status. Open Settings to refresh adapter readiness; if it fails, use the guided setup card below."
		}
		return AuthStatusUnknown, "Open Settings to verify Claude Code. If it reports a sign-in error, use the guided setup card below to paste the setup token from `claude setup-token`."
	case "cursor_agent":
		if envAny("CURSOR_API_KEY") || fileAny("${HOME}/.cursor", "${HOME}/Library/Application Support/Cursor") {
			return AuthStatusOK, ""
		}
		return AuthStatusUnauthenticated, "Run cursor-agent login, or set CURSOR_API_KEY for the adapter environment."
	default:
		return AuthStatusUnknown, "No auth heuristic is available for this adapter."
	}
}

// claudeCodeAuthErrorMessage is the user-facing message rendered in
// the chat when a Claude Code agent run fails because the adapter
// couldn't sign in. Two priorities behind the wording:
//
//  1. ui/src/features/chats/ChatView.tsx pattern-matches the
//     `claude_code_auth_required` token in this string (via
//     .includes()) to decide whether to render an inline
//     "Open Claude Code setup" button that deep-links to the guided
//     setup card in Settings → External agents. Keep the token in
//     the string verbatim — the UI handler depends on it.
//     (TranscriptMessageRow only strips the trailing parenthetical
//     marker from the visible copy; the match-and-render-button
//     decision lives in ChatView.)
//  2. Distinguish this credential from the Anthropic key in the
//     Providers tab. Operators who paste a key into Providers tab
//     reasonably expect Claude Code to "just work" — it doesn't,
//     because Claude Code runs as its own subprocess with its own
//     credentials.
//
// The message stays deliberately short: the UI button does the
// heavy lifting (one click → Settings → External agents → Claude
// Code, with the setup card scrolled into view).
func claudeCodeAuthErrorMessage() string {
	return "Claude Code isn't signed in. This is separate from the Anthropic key in the Providers tab — Claude Code runs as its own program and Anthropic controls its billing/credits. Click the button below to paste the setup token from `claude setup-token` into the guided setup card. No restart needed. (claude_code_auth_required)"
}

var runClaudeAuthStatus = defaultRunClaudeAuthStatus

type claudeAuthStatusPayload struct {
	LoggedIn bool `json:"loggedIn"`
}

func detectClaudeCLIAuthStatus() (ok bool, checked bool) {
	out, err := runClaudeAuthStatus()
	if err != nil || strings.TrimSpace(out) == "" {
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
	cmd := exec.CommandContext(ctx, "claude", "auth", "status")
	cmd.Env = sanitizedEnv(os.Environ())
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return stdout.String(), nil
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
