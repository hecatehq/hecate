package taskstate

import (
	"context"
	"errors"
	"time"

	"github.com/hecatehq/hecate/pkg/types"
)

var (
	// ErrTaskNotFound is returned when an atomic task-scoped transition can no
	// longer find its parent Task.
	ErrTaskNotFound = errors.New("task not found")
	// ErrActiveRun is returned when an atomic Run admission or Task deletion
	// finds a durable non-terminal Run for the same Task.
	ErrActiveRun = errors.New("task already has an active run")
	// ErrBudgetLower protects the durable per-task ceiling from stale or
	// concurrent resume requests that would lower it.
	ErrBudgetLower = errors.New("budget_micros_usd cannot be lower than the current task ceiling")
	// ErrScheduleOccurrenceClaimLost is returned when a scheduled Run start no
	// longer owns the durable occurrence claim. Callers must not create or
	// enqueue a Run after this fence has been lost.
	ErrScheduleOccurrenceClaimLost = errors.New("task schedule occurrence claim is no longer owned")
	// ErrRichInputProviderRouteConflict prevents a provider-bound rich input
	// from being sent through a different durable route after admission.
	ErrRichInputProviderRouteConflict = errors.New("rich input provider route conflicts with the admitted route")
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

const (
	TaskScheduleKindOnce = "once"
	TaskScheduleKindCron = "cron"

	TaskScheduleOccurrenceClaimed = "claimed"
	TaskScheduleOccurrenceStarted = "started"
	TaskScheduleOccurrenceSkipped = "skipped"
	TaskScheduleOccurrenceFailed  = "failed"
)

// TaskSchedule is the durable trigger configuration for one Task. TaskID is
// unique across schedules; changing a Task's schedule updates this record
// without replacing its identity or occurrence history.
type TaskSchedule struct {
	ID             string
	TaskID         string
	Kind           string
	CronExpression string
	Timezone       string
	RunAt          time.Time
	Enabled        bool
	NextRunAt      time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
	// Revision advances on every schedule definition change and occurrence
	// claim. It is an internal CAS token and is intentionally omitted from the
	// public schedule wire shape.
	Revision int64
}

type TaskScheduleFilter struct {
	TaskIDs []string
	Enabled *bool
	Limit   int
}

// TaskScheduleOccurrence records the durable handoff of one scheduled fire.
// ID is its provenance identity; ScheduleID and ScheduledFor are also unique
// and form the idempotency key used by schedule claims.
type TaskScheduleOccurrence struct {
	ID           string
	TaskID       string
	ScheduleID   string
	ScheduledFor time.Time
	Status       string
	ClaimOwner   string
	ClaimedAt    time.Time
	RunID        string
	Error        string
	CompletedAt  time.Time
}

// TaskScheduleOccurrenceClaim advances a schedule only if ScheduledFor still
// equals its durable NextRunAt. A zero NextRunAt disables the schedule.
type TaskScheduleOccurrenceClaim struct {
	OccurrenceID string
	ScheduleID   string
	// ExpectedScheduleRevision fences a manager snapshot so an operator edit
	// that preserves NextRunAt cannot be advanced using the stale definition.
	ExpectedScheduleRevision int64
	ScheduledFor             time.Time
	NextRunAt                time.Time
	ClaimOwner               string
	ClaimedAt                time.Time
}

type TaskScheduleOccurrenceReclaim struct {
	ScheduleID   string
	ScheduledFor time.Time
	StaleBefore  time.Time
	ClaimOwner   string
	ClaimedAt    time.Time
}

// TaskScheduleOccurrenceRenewal keeps a live occurrence claim from becoming
// eligible for stale recovery while its owner is still dispatching the Run.
// OccurrenceID and ClaimOwner are compare-and-swap fences: a displaced worker
// cannot extend a claim after another owner has reclaimed it.
type TaskScheduleOccurrenceRenewal struct {
	OccurrenceID string
	ScheduleID   string
	ScheduledFor time.Time
	ClaimOwner   string
	ClaimedAt    time.Time
}

// TaskScheduleOccurrenceCompletion settles ownership of a claimed occurrence.
// Status must be started, skipped, or failed. ClaimOwner is a compare-and-swap
// fence so a stale worker cannot overwrite a reclaimed occurrence.
type TaskScheduleOccurrenceCompletion struct {
	ScheduleID   string
	ScheduledFor time.Time
	ClaimOwner   string
	Status       string
	RunID        string
	Error        string
	CompletedAt  time.Time
}

type TaskScheduleOccurrenceFilter struct {
	ScheduleID    string
	Status        string
	ClaimedBefore time.Time
	Limit         int
}

// ScheduleStore is optional so narrow Store test doubles and consumers do not
// need to implement scheduling before they use the core task-state contract.
type ScheduleStore interface {
	CompareAndSwapTaskSchedule(ctx context.Context, mutation TaskScheduleCompareAndSwap) (TaskSchedule, bool, error)
	GetTaskSchedule(ctx context.Context, id string) (TaskSchedule, bool, error)
	GetTaskScheduleByTask(ctx context.Context, taskID string) (TaskSchedule, bool, error)
	ListTaskSchedules(ctx context.Context, filter TaskScheduleFilter) ([]TaskSchedule, error)
	DeleteTaskSchedule(ctx context.Context, id string) error
	ListDueTaskSchedules(ctx context.Context, dueAt time.Time, limit int) ([]TaskSchedule, error)
	ClaimTaskScheduleOccurrence(ctx context.Context, claim TaskScheduleOccurrenceClaim) (TaskScheduleOccurrence, bool, error)
	ReclaimTaskScheduleOccurrence(ctx context.Context, reclaim TaskScheduleOccurrenceReclaim) (TaskScheduleOccurrence, bool, error)
	RenewTaskScheduleOccurrence(ctx context.Context, renewal TaskScheduleOccurrenceRenewal) (TaskScheduleOccurrence, bool, error)
	CompleteTaskScheduleOccurrence(ctx context.Context, completion TaskScheduleOccurrenceCompletion) (TaskScheduleOccurrence, bool, error)
	ListTaskScheduleOccurrences(ctx context.Context, filter TaskScheduleOccurrenceFilter) ([]TaskScheduleOccurrence, error)
}

type TaskScheduleCompareAndSwap struct {
	Schedule         TaskSchedule
	ExpectedRevision int64
}

// TaskScheduleRunAdmission is the atomic handoff from a claimed schedule
// occurrence to the ordinary Task Run lifecycle. The candidate Task and Run
// follow RunStartTransition's merge rules; ClaimOwner fences stale workers,
// while CompletedAt timestamps either the started link or an overlap skip.
// Approval is required for an awaiting-approval candidate and is committed
// together with that Run and its initial durable lifecycle events.
type TaskScheduleRunAdmission struct {
	Task            types.Task
	Run             types.TaskRun
	Approval        *types.TaskApproval
	BudgetMicrosUSD int64
	ClaimOwner      string
	CompletedAt     time.Time
}

// TaskScheduleRunPreflight closes the common overlap/replay cases before
// workspace provisioning. Ready means no active Run existed at this instant;
// ApplyTaskScheduleRunAdmission must still recheck after provisioning because
// another process can start a Run in between.
type TaskScheduleRunPreflight struct {
	TaskID               string
	ScheduleID           string
	ScheduleOccurrenceID string
	ScheduledFor         time.Time
	ClaimOwner           string
	CompletedAt          time.Time
}

type TaskScheduleRunPreflightResult struct {
	Task        types.Task
	Run         types.TaskRun
	Occurrence  TaskScheduleOccurrence
	Ready       bool
	ExistingRun bool
	Skipped     bool
}

// TaskScheduleRunAdmissionResult returns the authoritative durable state.
// Applied means a new Run was inserted. ExistingRun means this occurrence was
// already represented by Run and callers must not repeat durable post-create
// effects; an existing queued Run may still be safely re-enqueued. Skipped
// means an overlapping active Run won and the occurrence was settled without
// creating a Run.
type TaskScheduleRunAdmissionResult struct {
	Task        types.Task
	Run         types.TaskRun
	Occurrence  TaskScheduleOccurrence
	Applied     bool
	ExistingRun bool
	Skipped     bool
}

// ScheduledRunStore is optional so the core Store interface and its narrow
// test doubles remain stable. Every production schedule-capable store
// implements it; scheduled dispatch fails closed when it is unavailable.
type ScheduledRunStore interface {
	PreflightTaskScheduleRunAdmission(ctx context.Context, preflight TaskScheduleRunPreflight) (TaskScheduleRunPreflightResult, error)
	ApplyTaskScheduleRunAdmission(ctx context.Context, admission TaskScheduleRunAdmission) (TaskScheduleRunAdmissionResult, error)
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
	Task        types.Task
	Run         types.TaskRun
	ExistingRun bool
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

// RichInputProviderAttempt records the concrete provider route immediately
// before it can receive a provider-bound input. The store owns the
// empty-or-equal route invariant so concurrent workers cannot overwrite one
// another's rich-input fence with stale run snapshots.
type RichInputProviderAttempt struct {
	TaskID           string
	RunID            string
	Provider         string
	ProviderKind     string
	Model            string
	ProviderInstance types.ProviderInstanceIdentity
}

// RichInputProviderAttemptResult returns the authoritative run. Applied is
// true only when the run was still eligible for provider dispatch; callers
// must not send the rich input when it is false.
type RichInputProviderAttemptResult struct {
	Run     types.TaskRun
	Applied bool
}

// TerminalRunSupplementalMetadata contains executor-observed fields that may
// safely enrich an already-terminal run without changing its winning status,
// reason, timestamps, task projection, or events. It is used both at normal
// execution finalization and when an observed rich-input provider attempt
// races a terminal transition.
type TerminalRunSupplementalMetadata struct {
	Provider                       string
	ProviderKind                   string
	InputProviderInstance          types.ProviderInstanceIdentity
	InputProviderDispatchRecorded  bool
	InputProviderDisclosedInstance types.ProviderInstanceIdentity
	Model                          string
	StepCount                      int
	ModelCallCount                 int
	ArtifactCount                  int
	TotalCostMicrosUSD             int64
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
	// TrustedSupplementalRunMetadata may be set only by trusted executor
	// observation. Operator cancellation and cleanup replays must leave it nil
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
	// DeleteTask serializes with Run admission, rejects a Task with any
	// non-terminal Run, and cascades all durable Task children atomically.
	DeleteTask(ctx context.Context, id string) error

	CreateRun(ctx context.Context, run types.TaskRun) (types.TaskRun, error)
	GetRun(ctx context.Context, taskID, runID string) (types.TaskRun, bool, error)
	ListRuns(ctx context.Context, taskID string) ([]types.TaskRun, error)
	ListRunsByFilter(ctx context.Context, filter RunFilter) ([]types.TaskRun, error)
	UpdateRun(ctx context.Context, run types.TaskRun) (types.TaskRun, error)
	ApplyRunStartTransition(ctx context.Context, transition RunStartTransition) (RunStartTransitionResult, error)
	ApplyRunStateTransition(ctx context.Context, transition RunStateTransition) (RunStateTransitionResult, error)
	RecordRichInputProviderAttempt(ctx context.Context, attempt RichInputProviderAttempt) (RichInputProviderAttemptResult, error)

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

	// Prune deletes `model.call.completed` rows that are
	// older than maxAge or, if maxCount > 0, beyond the most recent
	// maxCount rows (ordered by sequence DESC). Run-level events
	// (run.started, run.finished, approval.*) are never touched. The
	// retention worker calls this on its scheduled tick.
	Prune(ctx context.Context, maxAge time.Duration, maxCount int) (int, error)
}
