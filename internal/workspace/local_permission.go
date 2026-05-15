package workspace

import (
	"context"
)

// RequestPermission evaluates the static Policy attached to the
// request's Details ("policy"-typed value when present) and returns
// a grant/deny decision synchronously. No operator prompt: the local
// workspace's gate is the same policy the one-shot Run path already
// enforces at spawn time.
//
// The ACPWorkspace counterpart (added later in the refactor) forwards
// the request to session/request_permission on the editor instead;
// the contract from the caller's perspective is unchanged either way
// — block until you have a PermissionDecision, then proceed only on
// Granted.
//
// Today's mapping (kept narrow on purpose):
//   - file_write / file_delete: denied when Policy.ReadOnly is true.
//   - network_fetch: denied when Policy.Network is false.
//   - everything else: granted, with reason "no static rule".
//
// LocalWorkspace deliberately doesn't try to be Hecate's full
// approval system — that lives in pkg/types.ApprovalState and the
// orchestrator's runner. Permitter is the workspace-level gate; the
// approval system is the operator-level one. The two run in series:
// workspace permit → approval queue → run.
func (w *LocalWorkspace) RequestPermission(_ context.Context, req PermissionRequest) (PermissionDecision, error) {
	policy, _ := req.Details["policy"].(Policy)
	switch req.Tool {
	case "file_write", "file_delete", "file_append":
		if policy.ReadOnly {
			return PermissionDecision{
				Granted: false,
				Reason:  "sandbox policy: read-only",
			}, nil
		}
	case "network_fetch":
		if !policy.Network {
			return PermissionDecision{
				Granted: false,
				Reason:  "sandbox policy: network disabled",
			}, nil
		}
	}
	return PermissionDecision{Granted: true, Reason: "local workspace: no static rule denied"}, nil
}

// Compile-time check that LocalWorkspace satisfies the optional
// Permitter interface. Callers that need the dynamic-gate surface
// can type-assert.
var _ Permitter = (*LocalWorkspace)(nil)
