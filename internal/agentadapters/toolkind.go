package agentadapters

import (
	"strings"

	acp "github.com/coder/acp-go-sdk"
)

// Tool-kind taxonomy used by approval records and grants.
//
// We deliberately choose a small, stable closed set rather than the
// adapter's free-form tool names. Operators reason about "did Codex
// just want to write a file" not "did Codex just want to call its
// `apply_patch` v3.1 tool." The mapping below normalizes adapter
// terms into Hecate's set; unrecognized terms land as "other" and
// keep the raw adapter name in Approval.ToolName for diagnostics.
const (
	ToolKindFileWrite  = "file_write"
	ToolKindFileRead   = "file_read"
	ToolKindShellExec  = "shell_exec"
	ToolKindNetwork    = "network"
	ToolKindFileMove   = "file_move"
	ToolKindFileDelete = "file_delete"
	ToolKindSearch     = "search"
	ToolKindThink      = "think"
	ToolKindOther      = "other"
)

// extractToolKind returns the normalized tool kind for an ACP tool
// call. Resolution order:
//
//  1. The ACP-typed Kind field, when set to anything other than
//     ToolKindOther. This is the most reliable signal — it's a
//     closed enum the SDK already type-checks.
//  2. The adapter's free-form Title, run through a substring
//     heuristic (write/edit/patch → file_write, etc.). Adapters
//     that don't fill Kind often put a humanish phrase here.
//  3. Fall back to ToolKindOther.
//
// The matching is intentionally lenient: adapter ergonomics vary,
// and operators are better served by approximate matches that
// surface intent than by strict matches that always degrade to
// "other". The literal adapter title is preserved on the Approval
// row so the operator UI can still render the adapter's own
// language.
func extractToolKind(call acp.ToolCallUpdate) string {
	if call.Kind != nil {
		if mapped := mapACPToolKind(*call.Kind); mapped != "" {
			return mapped
		}
	}
	if call.Title != nil {
		if mapped := mapTitleToToolKind(*call.Title); mapped != "" {
			return mapped
		}
	}
	return ToolKindOther
}

// extractToolName returns a human-meaningful label for the tool the
// adapter is invoking. We prefer Title (adapter's own phrasing) and
// fall back to the ACP kind constant. Unlike extractToolKind, this is
// for display + audit, not for grant-matching.
func extractToolName(call acp.ToolCallUpdate) string {
	if call.Title != nil && strings.TrimSpace(*call.Title) != "" {
		return strings.TrimSpace(*call.Title)
	}
	if call.Kind != nil {
		return string(*call.Kind)
	}
	return ""
}

func mapACPToolKind(k acp.ToolKind) string {
	switch k {
	case acp.ToolKindEdit:
		return ToolKindFileWrite
	case acp.ToolKindRead:
		return ToolKindFileRead
	case acp.ToolKindExecute:
		return ToolKindShellExec
	case acp.ToolKindFetch:
		return ToolKindNetwork
	case acp.ToolKindMove:
		return ToolKindFileMove
	case acp.ToolKindDelete:
		return ToolKindFileDelete
	case acp.ToolKindSearch:
		return ToolKindSearch
	case acp.ToolKindThink:
		return ToolKindThink
	case acp.ToolKindOther, acp.ToolKindSwitchMode:
		return ""
	}
	return ""
}

// mapTitleToToolKind is the heuristic fallback for adapters that
// don't populate ACP Kind. The substrings are ordered by specificity:
// "delete" before "edit" so "delete file" doesn't fall through to
// file_write. Single-word matches at word boundaries; case-insensitive.
func mapTitleToToolKind(title string) string {
	t := " " + strings.ToLower(title) + " "
	switch {
	case containsAny(t, " delete ", " remove ", " rm "):
		return ToolKindFileDelete
	case containsAny(t, " move ", " rename "):
		return ToolKindFileMove
	case containsAny(t, " write ", " edit ", " patch ", " apply ", " modify ", " update file ", " create file "):
		return ToolKindFileWrite
	case containsAny(t, " read ", " open ", " view ", " show "):
		return ToolKindFileRead
	case containsAny(t, " run ", " exec ", " shell ", " bash ", " command "):
		return ToolKindShellExec
	case containsAny(t, " fetch ", " http ", " request ", " download ", " curl "):
		return ToolKindNetwork
	case containsAny(t, " search ", " grep ", " find "):
		return ToolKindSearch
	}
	return ""
}

func containsAny(haystack string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}
