package agentadapters

import (
	"os"
	"path/filepath"
	"strings"
)

// DetectAuthStatus is a lightweight dashboard hint. It deliberately avoids
// spawning the adapter; the Settings "Test" action runs the full ACP probe.
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
		if fileAny("${HOME}/.claude.json", "${HOME}/.claude/settings.json", "${HOME}/.claude/.credentials.json") {
			return AuthStatusUnknown, "Claude Code config is present on disk, but the ACP adapter may need its own token. Use Test adapter to check; if it fails, open the guided setup card below."
		}
		return AuthStatusUnknown, "Use Test adapter to check. If Claude Code reports a sign-in error, open the guided setup card below to paste a token from `claude setup-token`."
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
//  1. The chat UI pattern-matches `claude_code_auth_required` (the
//     trailing token in the parenthetical below) to render an inline
//     "Open Claude Code setup" button that deep-links to the guided
//     setup card in Settings → External agents. Keep that token in
//     the string verbatim — the UI handler depends on it.
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
	return "Claude Code isn't signed in. This is a separate credential from the Anthropic key in the Providers tab — Claude Code runs as its own program. Click the button below to paste a token from `claude setup-token` into the guided setup card. No restart needed. (claude_code_auth_required)"
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
