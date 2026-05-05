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
		if envAny("ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN") || fileAny("${HOME}/.claude.json", "${HOME}/.claude/settings.json") {
			return AuthStatusOK, ""
		}
		return AuthStatusUnknown, "Run claude /status or claude login if the ACP probe reports auth or billing errors."
	case "cursor_agent":
		if envAny("CURSOR_API_KEY") || fileAny("${HOME}/.cursor", "${HOME}/Library/Application Support/Cursor") {
			return AuthStatusOK, ""
		}
		return AuthStatusUnauthenticated, "Run cursor-agent login, or set CURSOR_API_KEY for the adapter environment."
	default:
		return AuthStatusUnknown, "No auth heuristic is available for this adapter."
	}
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
