// Package acpserver exposes Hecate as an Agent Client Protocol (ACP) agent.
//
// It deliberately owns only the protocol-facing session bridge. Durable task
// execution, policy, approval, and observability remain in Hecate's native
// task runtime, reached through the Runtime interface below.
package acpserver

import "context"

// Runtime is the small Hecate task-runtime surface required by the ACP
// bridge. Keeping it local to this package lets the stdio protocol layer stay
// independently testable and prevents ACP transport details leaking into API
// handlers or the external-agent adapter boundary.
type Runtime interface {
	EnsureReady(ctx context.Context) error
	CreateTask(ctx context.Context, request CreateTaskRequest) (Task, error)
	StartTask(ctx context.Context, taskID string) (Run, error)
	ContinueTask(ctx context.Context, taskID, runID, prompt string) (Run, error)
	CancelRun(ctx context.Context, taskID, runID, reason string) error
	ListRunEvents(ctx context.Context, taskID, runID string, afterSequence int64) ([]RunEvent, error)
}

// CreateTaskRequest expresses the deliberately narrow task contract used by
// ACP sessions. The bridge always creates an agent_loop task in the editor's
// real workspace; it never creates a Hecate-owned project or a clone. Routing
// stays Hecate-owned: ACP leaves provider/model blank instead of selecting a
// local route from provider-status data.
type CreateTaskRequest struct {
	Title            string
	Prompt           string
	WorkingDirectory string
}

type Task struct {
	ID string
}

type Run struct {
	ID     string
	Status string
}

// RunEvent is Hecate's canonical ordered runtime event. Data is intentionally
// untyped at this boundary because task events are a public, evolving event
// contract; the ACP bridge maps only the stable fields it understands.
type RunEvent struct {
	Sequence int64
	Type     string
	Data     map[string]any
}
