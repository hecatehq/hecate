package acp

import (
	"fmt"
	"strings"
)

// Workspace ownership modes. The bridge advertises one of these to the
// orchestrator (via Dispatcher.WorkspaceMode) after capability
// negotiation completes; the orchestrator then picks LocalWorkspace
// (hecate-owned) or ACPWorkspace (editor-owned) when transport for the
// latter ships in a follow-up. Today the wire is negotiated; the
// orchestrator-side routing is still hecate-owned in all modes.
const (
	WorkspaceModeAuto        = "auto"
	WorkspaceModeHecateOwned = "hecate-owned"
	WorkspaceModeEditorOwned = "editor-owned"
)

// ResolveWorkspaceMode picks the effective workspace ownership for one
// bridge connection. `configured` is the HECATE_WORKSPACE_MODE value
// (empty == auto, case-insensitive); `caps` is what the editor
// advertised during initialize.
//
//   - hecate-owned: the gateway host owns the workspace. No editor
//     capabilities required.
//   - editor-owned: the editor owns file writes and terminal execution
//     via ACP reverse-RPC. Requires fs.readTextFile, fs.writeTextFile,
//     and terminal on the client.
//   - auto: prefer editor-owned when the editor declares all required
//     reverse-RPC capabilities; otherwise fall back to hecate-owned.
//
// Returns an error only when the operator forces `editor-owned` but the
// editor is missing the capabilities required to honour it. We fail the
// initialize handshake in that case rather than silently downgrading —
// the config is wrong and the operator wants to see it immediately.
func ResolveWorkspaceMode(configured string, caps ClientCapabilities) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(configured))
	switch normalized {
	case "", WorkspaceModeAuto:
		if editorCanHostWorkspace(caps) {
			return WorkspaceModeEditorOwned, nil
		}
		return WorkspaceModeHecateOwned, nil
	case WorkspaceModeHecateOwned:
		return WorkspaceModeHecateOwned, nil
	case WorkspaceModeEditorOwned:
		if !editorCanHostWorkspace(caps) {
			return "", fmt.Errorf("HECATE_WORKSPACE_MODE=editor-owned requires the editor to declare clientCapabilities.fs.readTextFile, clientCapabilities.fs.writeTextFile, and clientCapabilities.terminal during initialize")
		}
		return WorkspaceModeEditorOwned, nil
	default:
		return "", fmt.Errorf("invalid HECATE_WORKSPACE_MODE %q: must be one of auto, hecate-owned, editor-owned", configured)
	}
}

func editorCanHostWorkspace(caps ClientCapabilities) bool {
	if caps.FS == nil || caps.Terminal == nil {
		return false
	}
	return caps.FS.ReadTextFile && caps.FS.WriteTextFile
}
