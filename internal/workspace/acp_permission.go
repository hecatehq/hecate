package workspace

import (
	"context"
	"encoding/json"
)

type acpPermissionParams struct {
	SessionID string         `json:"sessionId"`
	Tool      string         `json:"tool"`
	Action    string         `json:"action"`
	Details   map[string]any `json:"details,omitempty"`
	RiskLevel string         `json:"riskLevel,omitempty"`
}

type acpPermissionResult struct {
	Granted bool   `json:"granted"`
	Reason  string `json:"reason,omitempty"`
}

// RequestPermission forwards the gate to session/request_permission
// on the editor side. The editor renders its own modal — Zed's
// project trust prompt, JetBrains' AI assistant approval card — and
// the operator's answer flows back here as the function's return
// value. Blocking on the operator is fine because this is per-tool
// invocation; the agent loop is already blocked waiting for the tool
// result and the editor controls the UI cadence.
//
// On the local side, LocalWorkspace evaluates a static Policy and
// returns synchronously; this method is intentionally not symmetric
// with that — the static Policy lives in the request's Details map
// for whichever workspace ends up handling it.
func (w *ACPWorkspace) RequestPermission(ctx context.Context, req PermissionRequest) (PermissionDecision, error) {
	if err := w.ensureWired(); err != nil {
		return PermissionDecision{}, err
	}
	raw, err := w.caller.Call(ctx, "session/request_permission", acpPermissionParams{
		SessionID: w.sessionID,
		Tool:      req.Tool,
		Action:    req.Action,
		Details:   req.Details,
		RiskLevel: req.RiskLevel,
	})
	if err != nil {
		return PermissionDecision{}, err
	}
	var result acpPermissionResult
	if jerr := json.Unmarshal(raw, &result); jerr != nil {
		return PermissionDecision{}, jerr
	}
	return PermissionDecision{
		Granted: result.Granted,
		Reason:  result.Reason,
	}, nil
}
