package agentadapters

import "errors"

// ErrTerminalRPCUnsupported is the sentinel returned by every
// acpChatClient terminal RPC stub. Hecate does not yet route shell
// through ACP terminal methods (CreateTerminal, KillTerminal,
// TerminalOutput, ReleaseTerminal, WaitForTerminalExit); adapters
// such as Cursor and Codex that probe these methods receive this
// sentinel so they can fall back to direct shell execution cleanly
// instead of string-matching the error message.
//
// Adapters MAY use errors.Is to detect this case:
//
//	if errors.Is(err, agentadapters.ErrTerminalRPCUnsupported) {
//	    // pick the non-terminal codepath
//	}
//
// The wrapped acp.RequestError carries JSON-RPC code -32601
// ("Method not found") so downstream JSON-RPC tooling that doesn't
// know about Hecate's sentinel can still classify the failure
// correctly via the standard JSON-RPC method-not-found code.
var ErrTerminalRPCUnsupported = errors.New("hecate: ACP terminal RPC is not implemented")
