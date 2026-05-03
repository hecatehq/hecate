package acp

import "context"

// GatewayClient is the subset of the gateway HTTP API the bridge
// depends on. Defining it as an interface lets internal/acp/ stay
// pure-go (no net/http imports) and lets tests substitute a fake.
// Concrete impl lives in cmd/hecate-acp/.
//
// Single-user mode: no auth, no tenant. The bridge connects to a
// loopback gateway and forwards everything as-is.
type GatewayClient interface {
	// ListModels calls GET /v1/models and returns the model IDs the
	// gateway can serve. The bridge advertises these to the editor
	// in the InitializeResult.
	ListModels(ctx context.Context) ([]string, error)

	// CreateAgentLoopTask creates an agent_loop task and starts its
	// first run. Used on the first session/prompt of a session.
	CreateAgentLoopTask(ctx context.Context, req CreateTaskRequest) (CreateTaskResult, error)

	// ContinueAgentLoopTask appends a new user prompt to an existing
	// agent_loop conversation and starts the next run for that task.
	ContinueAgentLoopTask(ctx context.Context, taskID, runID, prompt string) (string, error)

	// CancelRun calls POST /v1/tasks/{taskID}/runs/{runID}/cancel.
	CancelRun(ctx context.Context, taskID, runID, reason string) error

	// ResolveApproval posts an approval decision.
	ResolveApproval(ctx context.Context, taskID, runID, approvalID string, decision ApprovalDecision) error

	// StreamRunEvents subscribes to the per-run SSE stream.
	StreamRunEvents(ctx context.Context, taskID, runID string) (<-chan RunEvent, error)
}

// CreateTaskRequest is the typed input to CreateAgentLoopTask.
type CreateTaskRequest struct {
	Model            string
	WorkingDirectory string
	Prompt           string
}

// CreateTaskResult captures the IDs the bridge needs to follow up.
type CreateTaskResult struct {
	TaskID string
	RunID  string
}

// ApprovalDecision is the typed enum for ResolveApproval.
type ApprovalDecision string

const (
	ApprovalAllow ApprovalDecision = "allow"
	ApprovalDeny  ApprovalDecision = "deny"
)

// RunEvent is the bridge's view of a gateway SSE event.
type RunEvent struct {
	Type string
	Data []byte
	Err  error
}
