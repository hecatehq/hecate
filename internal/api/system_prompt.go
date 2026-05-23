package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/hecatehq/hecate/internal/orchestrator"
)

// agentWorkspacePromptFiles are the file names the agent loop honors
// inside a run's workspace as the third layer of the system prompt.
// Order matters: the first file found wins. We support both the
// Claude Code (`CLAUDE.md`) and Codex CLI (`AGENTS.md`) conventions
// so developers who already write these for their team get
// out-of-the-box context for Hecate agents too.
var agentWorkspacePromptFiles = []string{"CLAUDE.md", "AGENTS.md"}

// agentWorkspacePromptMaxBytes caps how much of the workspace prompt
// file we read. 8 KiB is generous for a directives doc — anything
// longer is probably the file accidentally containing the whole
// codebase, and we don't want to push that into every LLM turn.
const agentWorkspacePromptMaxBytes = 8 * 1024

// buildSystemPromptResolver returns the three-layer composer the
// orchestrator uses for agent_loop runs:
//
//  1. Global default from operator config (env)
//  2. Workspace CLAUDE.md or AGENTS.md (whichever is found first)
//  3. Per-task from Task.SystemPrompt
//
// Layers are concatenated broadest-first with blank lines between
// non-empty parts. Empty layers are silently skipped — having any one
// is enough; having none yields an empty string and the agent loop
// runs with no system prompt at all.
func buildSystemPromptResolver(globalDefault string) orchestrator.SystemPromptResolver {
	globalDefault = strings.TrimSpace(globalDefault)
	return func(_ context.Context, _, perTaskPrompt, workspacePath string) string {
		layers := make([]string, 0, 3)
		if globalDefault != "" {
			layers = append(layers, globalDefault)
		}
		if workspacePrompt := loadWorkspaceSystemPrompt(workspacePath); workspacePrompt != "" {
			layers = append(layers, workspacePrompt)
		}
		if perTask := strings.TrimSpace(perTaskPrompt); perTask != "" {
			layers = append(layers, perTask)
		}
		return strings.Join(layers, "\n\n")
	}
}

// loadWorkspaceSystemPrompt reads CLAUDE.md or AGENTS.md from the
// workspace root, capped at agentWorkspacePromptMaxBytes. Returns
// empty when no file is present, the path is empty, or the file is
// too short to be meaningful.
//
// Path safety isn't a concern here — we're reading files inside the
// workspace the runner provisioned; an attacker who can write a
// CLAUDE.md inside the workspace can already do worse via the agent's
// tools. The cap is purely about token cost / prompt sanity.
func loadWorkspaceSystemPrompt(workspacePath string) string {
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" {
		return ""
	}
	for _, name := range agentWorkspacePromptFiles {
		path := filepath.Join(workspacePath, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if len(raw) > agentWorkspacePromptMaxBytes {
			raw = raw[:agentWorkspacePromptMaxBytes]
		}
		text := strings.TrimSpace(string(raw))
		if text != "" {
			return text
		}
	}
	return ""
}
