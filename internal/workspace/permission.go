package workspace

import "context"

// PermissionRequest is the dynamic-gate input the agent asks the
// workspace about before taking a potentially risky action. Today's
// LocalWorkspace evaluates these against the static Policy; the
// future ACPWorkspace forwards them to session/request_permission on
// the connected editor and blocks on the operator's answer.
//
// The structure is intentionally narrow: Tool + Action + a small
// Details map. Adding fields requires touching every implementation
// and every caller, so the shape is kept minimal and the structured
// payload rides in Details.
type PermissionRequest struct {
	// Tool is the action class. Stable string vocabulary so the
	// ACP editor side can route to consistent UI: "shell",
	// "file_write", "file_delete", "terminal_create",
	// "network_fetch", and so on.
	Tool string

	// Action is the human-readable summary the editor renders. Free
	// text in the operator's language ("write README.md", "run `git
	// push`"). Not used for policy decisions.
	Action string

	// Details carries structured arguments — the path being written,
	// the command being run, the URL being fetched. LocalWorkspace
	// reads specific keys ("path", "command", "url"); ACPWorkspace
	// forwards the whole map verbatim.
	Details map[string]any

	// RiskLevel hints to the editor UI how prominently to render the
	// prompt. "low" / "medium" / "high". Optional; defaults to
	// "medium" when empty. Not used for the grant decision.
	RiskLevel string
}

// PermissionDecision is the gate output. The orchestrator inspects
// Granted; when false, the requested action MUST NOT happen and the
// agent receives Reason as the rejection message.
type PermissionDecision struct {
	// Granted is the bottom line. When true, the agent proceeds with
	// the requested action. When false, the agent surfaces Reason to
	// the model and either retries with a different action or
	// terminates the step.
	Granted bool

	// Reason is the human-readable explanation. For grants, it's
	// usually empty or "auto-approved by policy"; for denials, it's
	// the operator's typed-in reason (ACPWorkspace) or the policy
	// rule name (LocalWorkspace).
	Reason string
}

// Permitter is the optional permission-gate surface. Workspace
// implementations that don't enforce dynamic permissions (or that
// rely entirely on Policy evaluated up front) can omit this
// interface; callers use a type assertion. Today LocalWorkspace
// implements Permitter as a thin wrapper around Policy, and
// ACPWorkspace will implement it by forwarding to the editor.
//
// The interface is split from Workspace so the broader Workspace
// surface stays stable when permission semantics evolve.
type Permitter interface {
	RequestPermission(ctx context.Context, req PermissionRequest) (PermissionDecision, error)
}
