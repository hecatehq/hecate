package taskstate

import (
	"context"
	"time"

	"github.com/hecate/agent-runtime/pkg/types"
)

type TaskFilter struct {
	Status string
	Limit  int
}

type ArtifactFilter struct {
	TaskID string
	RunID  string
	StepID string
	Kind   string
	Limit  int
}

type RunFilter struct {
	TaskID   string
	Statuses []string
	Limit    int
}

// EventFilter scopes a cross-run event query for the public events
// stream. All fields are AND-combined (an event must satisfy every
// non-empty filter); within a slice field the match is OR.
//
// AfterSequence is strictly-greater (so a client passes the last
// sequence they've seen and receives only newer events). Sequence is
// global across runs, so this acts as a single resumable cursor — no
// (task_id, sequence) tuple bookkeeping needed by callers.
//
// TaskIDs lets the handler pre-scope by tenant (looking up the
// tenant's tasks once, then constraining events to those IDs).
// Stores translate this to `task_id IN (...)` in their native
// query language; an empty slice means no constraint.
type EventFilter struct {
	EventTypes    []string
	TaskIDs       []string
	AfterSequence int64
	Limit         int
}

type Store interface {
	Backend() string
	CreateTask(ctx context.Context, task types.Task) (types.Task, error)
	GetTask(ctx context.Context, id string) (types.Task, bool, error)
	ListTasks(ctx context.Context, filter TaskFilter) ([]types.Task, error)
	UpdateTask(ctx context.Context, task types.Task) (types.Task, error)
	DeleteTask(ctx context.Context, id string) error

	CreateRun(ctx context.Context, run types.TaskRun) (types.TaskRun, error)
	GetRun(ctx context.Context, taskID, runID string) (types.TaskRun, bool, error)
	ListRuns(ctx context.Context, taskID string) ([]types.TaskRun, error)
	ListRunsByFilter(ctx context.Context, filter RunFilter) ([]types.TaskRun, error)
	UpdateRun(ctx context.Context, run types.TaskRun) (types.TaskRun, error)

	AppendStep(ctx context.Context, step types.TaskStep) (types.TaskStep, error)
	GetStep(ctx context.Context, runID, stepID string) (types.TaskStep, bool, error)
	ListSteps(ctx context.Context, runID string) ([]types.TaskStep, error)
	UpdateStep(ctx context.Context, step types.TaskStep) (types.TaskStep, error)

	CreateApproval(ctx context.Context, approval types.TaskApproval) (types.TaskApproval, error)
	GetApproval(ctx context.Context, taskID, approvalID string) (types.TaskApproval, bool, error)
	ListApprovals(ctx context.Context, taskID string) ([]types.TaskApproval, error)
	UpdateApproval(ctx context.Context, approval types.TaskApproval) (types.TaskApproval, error)

	CreateArtifact(ctx context.Context, artifact types.TaskArtifact) (types.TaskArtifact, error)
	GetArtifact(ctx context.Context, taskID, artifactID string) (types.TaskArtifact, bool, error)
	ListArtifacts(ctx context.Context, filter ArtifactFilter) ([]types.TaskArtifact, error)
	UpdateArtifact(ctx context.Context, artifact types.TaskArtifact) (types.TaskArtifact, error)

	AppendRunEvent(ctx context.Context, event types.TaskRunEvent) (types.TaskRunEvent, error)
	ListRunEvents(ctx context.Context, taskID, runID string, afterSequence int64, limit int) ([]types.TaskRunEvent, error)
	// ListEvents returns events across runs/tasks ordered by ascending
	// global sequence. Used by the public events stream so external
	// dashboards can subscribe to a single feed instead of polling
	// per-run. The handler enforces tenant scoping via filter.TaskIDs
	// — passing an empty slice means "no task constraint" (admin).
	ListEvents(ctx context.Context, filter EventFilter) ([]types.TaskRunEvent, error)

	// PruneTurnEvents deletes `turn.completed` rows that are
	// older than maxAge or, if maxCount > 0, beyond the most recent
	// maxCount rows (ordered by sequence DESC). Run-level events
	// (run.started, run.finished, approval.*) are never touched. The
	// retention worker calls this on its scheduled tick.
	PruneTurnEvents(ctx context.Context, maxAge time.Duration, maxCount int) (int, error)
}
