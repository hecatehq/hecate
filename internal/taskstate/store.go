package taskstate

import (
	"context"
	"errors"
	"time"

	"github.com/hecatehq/hecate/pkg/types"
)

var (
	// ErrActiveRun is returned when an atomic run-start transition finds a
	// durable non-terminal run for the same task.
	ErrActiveRun = errors.New("task already has an active run")
	// ErrBudgetLower protects the durable per-task ceiling from stale or
	// concurrent resume requests that would lower it.
	ErrBudgetLower = errors.New("budget_micros_usd cannot be lower than the current task ceiling")
)

type TaskFilter struct {
	Status    string
	ProjectID *string
	Limit     int
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
	// OrderByID switches to stable id-ascending order for cursor scans.
	// AfterID is exclusive and is only applied with OrderByID.
	OrderByID bool
	AfterID   string
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

type RunEventSpec struct {
	EventType          string
	Data               map[string]any
	RequestID          string
	TraceID            string
	CreatedAt          time.Time
	IncludeRunSnapshot bool
}

// PendingApprovalResolution is the only mutable portion of a pending approval
// accepted by the atomic run transitions. The store loads the approval by ID
// under the task/run lock, preserves its immutable request provenance, and
// derives the approval.resolved event from the merged authoritative record.
type PendingApprovalResolution struct {
	ApprovalID     string
	Status         string
	ResolvedBy     string
	ResolutionNote string
	ResolvedAt     time.Time
	RequestID      string
	TraceID        string
}

// RunStartTransition atomically verifies that a task has no non-terminal run,
// preserves or raises its durable budget ceiling, assigns the next run number,
// creates the run, and advances the task's runtime projection. The candidate
// Task carries only the runtime fields the caller wants advanced; stores merge
// those fields into the authoritative task so unrelated concurrent edits are
// not overwritten.
type RunStartTransition struct {
	Task            types.Task
	Run             types.TaskRun
	BudgetMicrosUSD int64
}

type RunStartTransitionResult struct {
	Task types.Task
	Run  types.TaskRun
}

// RunStateTransition atomically changes one non-terminal run and its parent
// task only when the durable run still has one of ExpectedRunStatuses. Queue
// claim and reconciliation paths use this compare-and-swap boundary so stale
// snapshots cannot resurrect a run that cancellation already settled.
type RunStateTransition struct {
	Task                types.Task
	Run                 types.TaskRun
	ExpectedRunStatuses []string
	// ApprovalResolution atomically resolves a pending approval together with
	// an awaiting-approval run transition. In this mode the store derives the
	// mandatory approval.resolved and run.queued snapshot events, and Events
	// must be empty.
	ApprovalResolution *PendingApprovalResolution
	Events             []RunEventSpec
}

type RunStateTransitionResult struct {
	Task     types.Task
	Run      types.TaskRun
	Approval types.TaskApproval
	Events   []types.TaskRunEvent
	Applied  bool
}

// TerminalRunSupplementalMetadata contains executor-observed fields that may
// safely enrich an already-terminal run without changing its winning status,
// reason, timestamps, task projection, or events.
type TerminalRunSupplementalMetadata struct {
	Provider           string
	ProviderKind       string
	Model              string
	StepCount          int
	ArtifactCount      int
	TotalCostMicrosUSD int64
}

// TerminalRunTransition atomically settles a run and its parent projection.
// The first terminal status is authoritative. A same-status replay may settle
// active children left by executor drain without emitting another terminal or
// task event. Execution-result finalization may also enrich any terminal
// winner with explicitly trusted route and monotonic accounting metadata; a
// different-status loser performs no child cleanup.
type TerminalRunTransition struct {
	Task       types.Task
	Run        types.TaskRun
	FinishedAt time.Time
	// ApprovalResolution atomically rejects the target approval, cancels the
	// run/task and active children, cancels other pending approvals, and derives
	// run.cancelled, task.updated, and approval.resolved events. Immutable
	// approval request provenance comes from the locked stored row.
	ApprovalResolution *PendingApprovalResolution
	// TrustedSupplementalRunMetadata may be set only by execution-result
	// finalization. Operator cancellation and cleanup replays must leave it nil
	// so stale run snapshots cannot replace the actual provider route.
	TrustedSupplementalRunMetadata *TerminalRunSupplementalMetadata
	// PreserveTaskProjection applies run-child cleanup without overwriting the
	// authoritative parent task. Post-drain cancellation cleanup uses this when
	// a newer run may already own the task's runtime projection.
	PreserveTaskProjection bool

	CancelActiveSteps             bool
	ActiveStepError               string
	ActiveStepErrorKind           string
	ActiveStepResult              string
	CancelStreamingArtifacts      bool
	CancelPendingApprovals        bool
	PendingApprovalStatus         string
	PendingApprovalResolvedBy     string
	PendingApprovalResolutionNote string

	ApprovalResolvedEventType string
	TerminalEvent             *RunEventSpec
	TaskUpdatedEvent          *RunEventSpec
}

type TerminalRunTransitionResult struct {
	Task               types.Task
	Run                types.TaskRun
	Approval           types.TaskApproval
	Steps              []types.TaskStep
	Artifacts          []types.TaskArtifact
	CancelledApprovals []types.TaskApproval
	Events             []types.TaskRunEvent
	Applied            bool
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
	ApplyRunStartTransition(ctx context.Context, transition RunStartTransition) (RunStartTransitionResult, error)
	ApplyRunStateTransition(ctx context.Context, transition RunStateTransition) (RunStateTransitionResult, error)

	AppendStep(ctx context.Context, step types.TaskStep) (types.TaskStep, error)
	GetStep(ctx context.Context, runID, stepID string) (types.TaskStep, bool, error)
	ListSteps(ctx context.Context, runID string) ([]types.TaskStep, error)
	UpdateStep(ctx context.Context, step types.TaskStep) (types.TaskStep, error)

	CreateApproval(ctx context.Context, approval types.TaskApproval) (types.TaskApproval, error)
	GetApproval(ctx context.Context, taskID, approvalID string) (types.TaskApproval, bool, error)
	ListApprovals(ctx context.Context, taskID string) ([]types.TaskApproval, error)
	UpdateApproval(ctx context.Context, approval types.TaskApproval) (types.TaskApproval, error)
	UpdatePendingApproval(ctx context.Context, approval types.TaskApproval) (types.TaskApproval, bool, error)

	CreateArtifact(ctx context.Context, artifact types.TaskArtifact) (types.TaskArtifact, error)
	GetArtifact(ctx context.Context, taskID, artifactID string) (types.TaskArtifact, bool, error)
	ListArtifacts(ctx context.Context, filter ArtifactFilter) ([]types.TaskArtifact, error)
	UpdateArtifact(ctx context.Context, artifact types.TaskArtifact) (types.TaskArtifact, error)

	AppendRunEvent(ctx context.Context, event types.TaskRunEvent) (types.TaskRunEvent, error)
	ApplyRunTerminalTransition(ctx context.Context, transition TerminalRunTransition) (TerminalRunTransitionResult, error)
	ListRunEvents(ctx context.Context, taskID, runID string, afterSequence int64, limit int) ([]types.TaskRunEvent, error)
	// ListEvents returns events across runs/tasks ordered by ascending
	// global sequence. Used by the public events stream so external
	// dashboards can subscribe to a single feed instead of polling
	// per-run. The handler enforces tenant scoping via filter.TaskIDs
	// — passing an empty slice means "no task constraint" (admin).
	ListEvents(ctx context.Context, filter EventFilter) ([]types.TaskRunEvent, error)

	// Prune deletes `turn.completed` rows that are
	// older than maxAge or, if maxCount > 0, beyond the most recent
	// maxCount rows (ordered by sequence DESC). Run-level events
	// (run.started, run.finished, approval.*) are never touched. The
	// retention worker calls this on its scheduled tick.
	Prune(ctx context.Context, maxAge time.Duration, maxCount int) (int, error)
}
