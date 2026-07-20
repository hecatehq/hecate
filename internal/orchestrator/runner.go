package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/hecatehq/hecate/internal/browserrunner"
	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/runtimeevents"
	"github.com/hecatehq/hecate/internal/taskruncoord"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/internal/taskworkflow"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/internal/websearch"
	"github.com/hecatehq/hecate/internal/workspace"
	"github.com/hecatehq/hecate/internal/workspacecoord"
	"github.com/hecatehq/hecate/pkg/types"
)

// ErrAgentLoopMisconfigured is returned by StartTask when an agent_loop
// task cannot be started due to missing configuration detectable before
// the run is created. Callers should surface this as a client error
// (HTTP 422) rather than a gateway error (500).
var (
	ErrAgentLoopMisconfigured = errors.New("agent_loop misconfigured")
	ErrActiveRun              = taskstate.ErrActiveRun
	ErrBudgetLower            = taskstate.ErrBudgetLower
)

const scheduledWorkspaceCleanupTimeout = 3 * time.Second

var resourceIDCounter atomic.Uint64

var stepTelemetryAttrKeys = []string{
	telemetry.AttrHecateSandboxWrapperKind,
	telemetry.AttrHecateSandboxRTKEnabled,
	telemetry.AttrHecateSandboxRTKCommandBefore,
	telemetry.AttrHecateSandboxRTKCommandAfter,
	telemetry.AttrHecateSandboxNetworkEnabled,
	telemetry.AttrHecateSandboxReadOnly,
	telemetry.AttrHecateSandboxOutputLimit,
	telemetry.AttrHecateToolTimeoutMS,
	telemetry.AttrHecateToolExitCode,
	telemetry.AttrHecateToolStdoutBytes,
	telemetry.AttrHecateToolStderrBytes,
	telemetry.AttrHecateToolTimedOut,
	telemetry.AttrHecateToolCancelled,
	telemetry.AttrHecateToolOutputTruncated,
	telemetry.AttrHecateToolFileOperation,
	telemetry.AttrHecateToolFileBytesWritten,
	telemetry.AttrHecateToolFileBeforeExisted,
	telemetry.AttrHecateToolFileDiffBytes,
	telemetry.AttrHecateToolFileArtifactStatus,
}

type Config struct {
	DefaultModel           string
	ApprovalPolicies       []string
	QueueBackend           string
	QueueWorkers           int
	QueueBuffer            int
	QueueLeaseSeconds      int
	MaxConcurrentPerTenant int
	// DeferQueueStart lets composition wire durable origin validators and
	// owner stores before any worker can claim or reconcile persisted work.
	// Standalone runner callers retain the historical auto-start default.
	DeferQueueStart bool
	// AgentLoopMaxModelCalls caps how many LLM round-trips a single
	// agent_loop run can make. Default 8 (set in NewAgentLoopExecutor
	// when zero or negative). Acts as a runaway-cost safety net.
	AgentLoopMaxModelCalls int
	// HTTPPolicy bounds the agent_loop `http_request` tool: timeout,
	// response size cap, SSRF guards, optional host allowlist. Zero
	// values fall back to safe defaults inside the executor (30s,
	// 256 KiB, private-IPs blocked, all public hosts allowed).
	HTTPPolicy HTTPRequestPolicy
	// WebSearch enables the optional agent_loop `web_search` tool.
	// Nil means the tool is omitted from the LLM catalog.
	WebSearch websearch.Client
	// BrowserInspector enables the deliberately narrow native browser evidence
	// tool. Nil keeps it out of every agent catalog.
	BrowserInspector browserrunner.Inspector
	// ShellNetwork mirrors HTTPPolicy's host/IP rules onto shell_exec
	// and git_exec when SandboxNetwork is enabled on the task. The
	// master gate is still task.SandboxNetwork — these only refine
	// which destinations are reachable when network IS allowed.
	ShellNetwork ShellNetworkPolicy
	// ReconcileInterval controls how often the periodic reconciliation
	// loop scans for runs stuck in "running" state. Default 30s.
	// Set via StartReconcileLoop; zero falls back to the default.
	ReconcileInterval time.Duration
}

// HTTPRequestPolicy is the agent-runtime-side projection of the
// gateway's `HECATE_TASK_HTTP_*` env knobs. Lives here (not in
// config) because the orchestrator package shouldn't import config —
// the API handler translates env into this struct at startup.
type HTTPRequestPolicy struct {
	Timeout          time.Duration
	MaxResponseBytes int
	AllowPrivateIPs  bool
	// AllowedHosts is the hostname allowlist. Non-empty means "only
	// these hosts"; empty means "all public hosts". Subdomain matches
	// are NOT inferred — entries must be exact (e.g. "api.openai.com",
	// not "openai.com" wildcarded).
	AllowedHosts []string
}

// ShellNetworkPolicy is the projection of `HECATE_TASK_SHELL_*` env
// knobs. Used to refine egress when a task has SandboxNetwork=true:
// the static URL parser in sandbox.validateCommand() rejects http(s)
// URLs whose host is in a blocked range or outside the allowlist.
// Best-effort — clever obfuscation bypasses it; for hard isolation
// run the gateway in a network namespace or behind a filtering proxy.
type ShellNetworkPolicy struct {
	AllowPrivateIPs bool
	AllowedHosts    []string
}

// SystemPromptResolver composes the four-layer agent_loop system
// prompt for one execution. It's called by the runner before
// dispatching the executor — implementations live outside the
// orchestrator package because the per-tenant lookup needs the
// controlplane store, which the runner deliberately doesn't depend on.
//
//   - tenantID is the task's tenant (may be empty)
//   - perTaskPrompt is task.SystemPrompt (may be empty)
//   - workspacePath is the run's WorkspacePath (where CLAUDE.md /
//     AGENTS.md may live; may be empty)
//
// Implementations concatenate non-empty layers broadest → narrowest:
// global, tenant, workspace file, per-task. Empty result means "no
// system prompt" — agent loop just uses the user prompt as the only
// initial message.
type SystemPromptResolver func(ctx context.Context, tenantID, perTaskPrompt, workspacePath string) string

// AgentInput is rich, execution-time input resolved from an opaque TaskRun
// reference. Release drops any admission permit or other transient resource
// held while the executor retains the hydrated body.
type AgentInput struct {
	Message      types.Message
	Requirements types.ChatRequestRequirements
	Release      func()
}

// AgentInputResolver keeps application-owned binary storage outside the task
// runtime. It is invoked only for agent_loop runs with a non-empty InputRef,
// immediately before execution.
type AgentInputResolver func(ctx context.Context, task types.Task, run types.TaskRun) (AgentInput, error)

type Runner struct {
	logger            *slog.Logger
	store             taskstate.Store
	tracer            profiler.Tracer
	exec              Executor
	shell             Executor
	file              Executor
	git               Executor
	agent             Executor
	workspaces        *WorkspaceManager
	config            Config
	queueCoordinator  *runQueueCoordinator
	policies          map[string]struct{}
	metrics           *telemetry.OrchestratorMetrics
	resolveSysPrompt  SystemPromptResolver
	resolveAgentInput AgentInputResolver
	// mcpHostFactory is the factory used when building or rebuilding the
	// agent_loop executor. Stored here so SetAgentLLMClient (which
	// rebuilds the executor from scratch) can re-bind the same factory
	// instead of resetting to the no-cipher default.
	mcpHostFactory AgentMCPHostFactory
	// projectAssistantDraftTool is also rebound across agent_loop
	// executor rebuilds. The API layer owns the actual drafting
	// boundary; the runner just forwards the seam.
	projectAssistantDraftTool ProjectAssistantDraftTool
	// originRunGate is process-scoped coordination shared with taskapp's
	// destructive origin cleanup. Keeping admission at this single run-create
	// choke point covers API, Chat, and Project launch callers alike.
	originRunGate atomic.Pointer[taskruncoord.Gate]
	// workspaceCoordinator is shared with every process-local destructive
	// workspace mutation. Execution acquires a writer lease before reading the
	// workspace or dispatching an executor, then holds it through result
	// collection so revert cannot race an active run.
	workspaceCoordinator atomic.Pointer[workspacecoord.Registry]
	// taskStarts serializes the full preflight/provision/commit path per task in
	// this process. ApplyRunStartTransition remains the cross-process authority.
	taskStarts taskStartGate
}

type agentTerminalRunCloser interface {
	CloseTerminalsForRun(ctx context.Context, runID string)
}

type agentTerminalShutdownCloser interface {
	CloseAllTerminals(ctx context.Context)
}

type StartTaskResult struct {
	Task      types.Task
	Run       types.TaskRun
	Steps     []types.TaskStep
	Artifacts []types.TaskArtifact
	TraceID   string
	SpanID    string
}

// ScheduledTaskStart carries the durable occurrence claim that must be
// admitted atomically with Run creation. It is separate from RunInitializer:
// the claim owner is a persistence fence, not caller-owned Run metadata.
type ScheduledTaskStart struct {
	ScheduleID           string
	ScheduleOccurrenceID string
	ScheduledFor         time.Time
	ClaimOwner           string
}

type startTaskOptions struct {
	ResumeFromRun *types.TaskRun
	// RetryFromRun carries same-input state and any retained workflow snapshot
	// into a fresh Run. Unlike ResumeFromRun, it does not inherit workspace,
	// cost, or conversation checkpoints and emits no resume event.
	RetryFromRun   *types.TaskRun
	ResumeReason   string
	AppendPrompt   string
	RunInitializer func(*types.TaskRun)
	// SourceModelCallIndex, when > 0, signals the new run should resume from
	// the source run's conversation truncated to right before Run-local model
	// call N.
	// Used by the retry-from-model-call-N code path; ignored when
	// ResumeFromRun is nil. The runner persists this on the new run's
	// `run.resumed_from_event` event so the worker that later claims the run can
	// rebuild the truncated checkpoint without keeping in-memory state.
	SourceModelCallIndex int
	// BudgetMicrosUSD raises the durable task ceiling while the same origin
	// lease that creates the resumed run is held.
	BudgetMicrosUSD int64
	Schedule        *ScheduledTaskStart
}

type RuntimeStats struct {
	CheckedAt               time.Time
	QueueDepth              int
	QueueCapacity           int
	QueueBackend            string
	WorkerCount             int
	InFlightJobs            int
	QueuedRuns              int
	RunningRuns             int
	AwaitingApprovalRuns    int
	OldestQueuedAgeSeconds  int64
	OldestRunningAgeSeconds int64
	StoreBackend            string
}

func NewRunner(logger *slog.Logger, store taskstate.Store, tracer profiler.Tracer, cfg Config) *Runner {
	if tracer == nil {
		tracer = profiler.NewInMemoryTracer(nil)
	}
	worker := workspace.NewLocalWorkspace()
	queueBuffer := cfg.QueueBuffer
	if queueBuffer <= 0 {
		queueBuffer = 128
	}
	queueLease := time.Duration(cfg.QueueLeaseSeconds) * time.Second
	if queueLease <= 0 {
		queueLease = 30 * time.Second
	}
	reconcileInterval := cfg.ReconcileInterval
	if reconcileInterval <= 0 {
		reconcileInterval = 30 * time.Second
	}
	queue := NewMemoryRunQueue(queueBuffer, queueLease)
	runner := &Runner{
		logger:     logger,
		store:      store,
		tracer:     tracer,
		exec:       NewStubExecutor(),
		shell:      NewShellExecutor(worker),
		file:       NewFileExecutor(worker),
		git:        NewGitExecutor(worker),
		workspaces: NewWorkspaceManager(""),
		config:     cfg,
		policies:   make(map[string]struct{}),
	}
	runner.queueCoordinator = newRunQueueCoordinator(runner, runQueueCoordinatorConfig{
		Queue:             queue,
		Lease:             queueLease,
		ReconcileInterval: reconcileInterval,
		WorkerID:          defaultWorkerID(),
	})
	// LLM client + max-model-calls are wired post-construction via
	// SetAgentLLMClient — main.go injects the gateway.Service after
	// it's built. nil here means the loop falls back to a pass-through
	// step until configured (see executor_agent_loop.go runWithoutLLM).
	// Gated tools come from the same approval policies as
	// task-level gating, so an operator who approves shell at the
	// task layer also approves it inside agent_loop tool calls.
	agent := NewAgentLoopExecutor(nil, runner.shell, runner.file, runner.git, cfg.AgentLoopMaxModelCalls, agentLoopGatedTools(runner.policies), cfg.HTTPPolicy, WithWebSearchClient(cfg.WebSearch), WithBrowserInspector(cfg.BrowserInspector))
	agent.SetMCPHostFactory(DefaultMCPHostFactory)
	runner.mcpHostFactory = DefaultMCPHostFactory
	runner.agent = agent
	for _, policy := range cfg.ApprovalPolicies {
		policy = strings.TrimSpace(policy)
		if policy == "" {
			continue
		}
		runner.policies[policy] = struct{}{}
	}
	// No silent fallback: config.Validate() rejects unknown names at boot.
	// An empty HECATE_TASK_APPROVAL_POLICIES is the documented "no gates"
	// path for fully-trusted environments.
	workers := cfg.QueueWorkers
	if workers <= 0 {
		workers = 1
	}
	if !cfg.DeferQueueStart {
		runner.queueCoordinator.StartWorkers(workers)
	}
	return runner
}

func (r *Runner) SetExecutor(exec Executor) {
	if exec == nil {
		return
	}
	r.exec = exec
}

func (r *Runner) SetShellExecutor(exec Executor) {
	if exec == nil {
		return
	}
	r.shell = exec
}

func (r *Runner) SetFileExecutor(exec Executor) {
	if exec == nil {
		return
	}
	r.file = exec
}

func (r *Runner) SetGitExecutor(exec Executor) {
	if exec == nil {
		return
	}
	r.git = exec
}

// SetSystemPromptResolver wires the four-layer composer used by the
// agent_loop executor. Safe to call after NewRunner; nil = no
// composition (agent_loop runs with no system prompt).
func (r *Runner) SetSystemPromptResolver(resolver SystemPromptResolver) {
	r.resolveSysPrompt = resolver
}

// SetAgentInputResolver wires application-owned rich prompt hydration. Call it
// before queue workers start; nil keeps ordinary text-only task behavior.
func (r *Runner) SetAgentInputResolver(resolver AgentInputResolver) {
	r.resolveAgentInput = resolver
}

// SetAgentLLMClient wires the LLM seam into the agent_loop executor.
// Safe to call after NewRunner — main.go invokes this once the gateway
// service is constructed, since the chat path needs its own deps that
// the runner doesn't otherwise know about. Nil unwires the loop.
func (r *Runner) SetAgentLLMClient(llm AgentLLMClient) {
	agent := NewAgentLoopExecutor(llm, r.shell, r.file, r.git, r.config.AgentLoopMaxModelCalls, agentLoopGatedTools(r.policies), r.config.HTTPPolicy, WithWebSearchClient(r.config.WebSearch), WithBrowserInspector(r.config.BrowserInspector))
	// Re-bind the stored MCP factory — the executor is rebuilt from
	// scratch above so the prior binding is gone. Fall back to the
	// no-cipher default if SetMCPHostFactory was never called.
	factory := r.mcpHostFactory
	if factory == nil {
		factory = DefaultMCPHostFactory
	}
	agent.SetMCPHostFactory(factory)
	agent.SetWorkspaceCoordinator(r.workspaceCoordinator.Load())
	if r.projectAssistantDraftTool != nil {
		agent.SetProjectAssistantDraftTool(r.projectAssistantDraftTool)
	}
	// Same story for metrics — the rebuild lost the prior wiring.
	if r.metrics != nil {
		agent.SetMetrics(r.metrics)
	}
	r.agent = agent
}

// SetProjectAssistantDraftTool wires the proposal-only Project
// Assistant tool into project-linked Hecate Chat agent_loop runs.
// Safe to call after NewRunner; nil clears the current binding.
func (r *Runner) SetProjectAssistantDraftTool(tool ProjectAssistantDraftTool) {
	r.projectAssistantDraftTool = tool
	if agent, ok := r.agent.(*AgentLoopExecutor); ok {
		agent.SetProjectAssistantDraftTool(tool)
	}
}

// SetMCPHostFactory updates the MCP host factory on both the stored
// field (for future SetAgentLLMClient rebuilds) and the current
// agent_loop executor if it is an *AgentLoopExecutor. Safe to call
// after NewRunner; intended for wiring the cipher-aware factory once
// the control-plane key becomes available.
func (r *Runner) SetMCPHostFactory(factory AgentMCPHostFactory) {
	r.mcpHostFactory = factory
	if agent, ok := r.agent.(*AgentLoopExecutor); ok {
		agent.SetMCPHostFactory(factory)
	}
}

// hasPolicy reports whether name is in the runner's active policy set.
func (r *Runner) hasPolicy(name string) bool {
	_, ok := r.policies[name]
	return ok
}

// agentLoopGatedTools translates the runner's task-level approval
// policy set into the agent-loop tool gating set. The mapping:
// task policy "shell_exec" gates the agent's shell_exec tool, etc.
// Network egress maps to http_request; read_file maps to read-only
// workspace/artifact inspection tools.
// all_tools short-circuits to the full set of every agent tool.
func agentLoopGatedTools(policies map[string]struct{}) []string {
	// all_tools gates every tool the agent can call — no need to enumerate.
	if _, ok := policies["all_tools"]; ok {
		out := []string{"shell_exec", "git_exec", "git_status", "git_diff", "file_write", "file_edit", "apply_patch", "read_file", "grep", "glob", "artifact_read", "list_dir", AgentToolCodeIntelligence, "http_request", AgentToolWebSearch, AgentToolDraftProjectProposal}
		return append(out, agentLoopTerminalToolNames()...)
	}
	out := make([]string, 0, len(policies))
	for p := range policies {
		switch p {
		case "shell_exec":
			out = append(out, p)
			out = append(out, agentLoopTerminalToolNames()...)
		case "git_exec":
			out = append(out, "git_exec", "git_status", "git_diff")
		case "file_write":
			out = append(out, "file_write", "file_edit", "apply_patch")
		case "read_file":
			out = append(out, "read_file", "grep", "glob", "artifact_read", AgentToolCodeIntelligence)
		case "network_egress":
			// `network_egress` is the historical name for the
			// outbound-network policy applied to shell tasks. We
			// reuse it here so an operator who already gates
			// network on shell automatically gates the agent's
			// HTTP and web-search tools too — no second toggle to remember.
			out = append(out, "http_request", AgentToolWebSearch)
		}
	}
	return out
}

// SetMetrics wires in an OrchestratorMetrics instance. Safe to call after
// NewRunner; a nil argument is silently ignored.
//
// Forwards the same instance to the agent_loop executor so MCP tool
// calls and cache observers share the same instruments as
// run/step/approval metrics — operators see one coherent set, not
// two parallel registrations.
func (r *Runner) SetMetrics(m *telemetry.OrchestratorMetrics) {
	if m == nil {
		return
	}
	r.metrics = m
	if agent, ok := r.agent.(*AgentLoopExecutor); ok {
		agent.SetMetrics(m)
	}
}

func (r *Runner) RuntimeStats(ctx context.Context) (RuntimeStats, error) {
	q := r.getQueue()
	queueDepth := 0
	queueCapacity := 0
	if q != nil {
		if depth, err := q.Depth(ctx); err == nil {
			queueDepth = depth
		}
		queueCapacity = q.Capacity()
	}
	stats := RuntimeStats{
		CheckedAt:     time.Now().UTC(),
		QueueDepth:    queueDepth,
		QueueCapacity: queueCapacity,
		WorkerCount:   maxInt(r.config.QueueWorkers, 1),
	}
	if q != nil {
		stats.QueueBackend = q.Backend()
	}
	stats.InFlightJobs = r.inFlightJobCount()
	if r.store == nil {
		return stats, nil
	}
	stats.StoreBackend = r.store.Backend()
	now := time.Now().UTC()

	queuedRuns, err := r.store.ListRunsByFilter(ctx, taskstate.RunFilter{Statuses: []string{"queued"}, Limit: 2000})
	if err != nil {
		return RuntimeStats{}, err
	}
	stats.QueuedRuns = len(queuedRuns)
	oldestQueued := findOldestRunStart(queuedRuns)
	if !oldestQueued.IsZero() {
		stats.OldestQueuedAgeSeconds = int64(now.Sub(oldestQueued).Seconds())
	}

	runningRuns, err := r.store.ListRunsByFilter(ctx, taskstate.RunFilter{Statuses: []string{"running"}, Limit: 2000})
	if err != nil {
		return RuntimeStats{}, err
	}
	stats.RunningRuns = len(runningRuns)
	oldestRunning := findOldestRunStart(runningRuns)
	if !oldestRunning.IsZero() {
		stats.OldestRunningAgeSeconds = int64(now.Sub(oldestRunning).Seconds())
	}

	awaitingApprovals, err := r.store.ListRunsByFilter(ctx, taskstate.RunFilter{Statuses: []string{"awaiting_approval"}, Limit: 2000})
	if err != nil {
		return RuntimeStats{}, err
	}
	stats.AwaitingApprovalRuns = len(awaitingApprovals)
	return stats, nil
}

func (r *Runner) StartTask(ctx context.Context, task types.Task, idgen func(prefix string) string) (*StartTaskResult, error) {
	return r.startTaskWithOptions(ctx, task, idgen, startTaskOptions{})
}

// RetryTask creates a fresh Run while retaining canonical source provenance
// and any bounded workflow contract recorded by the selected Run. It
// intentionally does not resume execution state or emit run.resumed_from_event.
func (r *Runner) RetryTask(ctx context.Context, task types.Task, run types.TaskRun, idgen func(prefix string) string) (*StartTaskResult, error) {
	if !types.IsTerminalTaskRunStatus(run.Status) {
		return nil, fmt.Errorf("task run %q is not retryable", run.ID)
	}
	return r.startTaskWithOptions(ctx, task, idgen, startTaskOptions{RetryFromRun: &run})
}

func (r *Runner) SetOriginRunGate(gate *taskruncoord.Gate) {
	if r == nil {
		return
	}
	r.originRunGate.Store(gate)
}

// SetWorkspaceCoordinator wires the process-scoped registry shared with
// destructive workspace mutations. Composition must call this before queue
// workers start so every workspace-backed run uses the same registry.
func (r *Runner) SetWorkspaceCoordinator(registry *workspacecoord.Registry) {
	if r == nil {
		return
	}
	r.workspaceCoordinator.Store(registry)
	if agent, ok := r.agent.(*AgentLoopExecutor); ok {
		agent.SetWorkspaceCoordinator(registry)
	}
}

func (r *Runner) beginWorkspaceWrite(ctx context.Context, workspacePath string) (*workspacecoord.WriterLease, error) {
	if r == nil || strings.TrimSpace(workspacePath) == "" {
		return nil, nil
	}
	registry := r.workspaceCoordinator.Load()
	if registry == nil {
		return nil, nil
	}
	return registry.WaitWriter(ctx, workspacePath)
}

func (r *Runner) beginWorkspaceProvisioningWrite(ctx context.Context, workspacePath string) (*workspacecoord.WriterLease, error) {
	if r == nil || strings.TrimSpace(workspacePath) == "" {
		return nil, nil
	}
	registry := r.workspaceCoordinator.Load()
	if registry == nil {
		return nil, nil
	}
	return registry.WaitWriterForCreation(ctx, workspacePath)
}

// validateQAWorkspace binds a report-only QA Run to the generated workspace
// for its own Task/Run IDs. Stored Run.WorkspacePath is otherwise an
// execution-time input, so this check is intentionally repeated when a worker
// claims a Run after a restart.
func (r *Runner) validateQAWorkspace(task types.Task, run types.TaskRun) (types.TaskRun, error) {
	if !taskworkflow.IsQAExecution(task, run) {
		return run, nil
	}
	if r == nil || r.workspaces == nil {
		return run, taskworkflow.ErrQAWorkspaceProvenance
	}
	managedPath, err := r.workspaces.managedRunWorkspacePath(task, run)
	if err != nil {
		// The attempted path can be an arbitrary persisted local path. Do not
		// carry it into a durable run error, UI response, or telemetry field.
		return run, taskworkflow.ErrQAWorkspaceProvenance
	}
	run.WorkspacePath = managedPath
	return run, nil
}

func (r *Runner) beginOriginRunMutation(ctx context.Context, task types.Task) (*taskruncoord.Lease, error) {
	if r == nil {
		return nil, nil
	}
	gate := r.originRunGate.Load()
	if gate == nil {
		return nil, nil
	}
	return gate.Begin(ctx, taskruncoord.Origin{Kind: task.OriginKind, ID: task.OriginID})
}

func (r *Runner) StartTaskWithRunInitializer(ctx context.Context, task types.Task, idgen func(prefix string) string, init func(*types.TaskRun)) (*StartTaskResult, error) {
	return r.startTaskWithOptions(ctx, task, idgen, startTaskOptions{RunInitializer: init})
}

func (r *Runner) StartScheduledTask(ctx context.Context, task types.Task, idgen func(prefix string) string, schedule ScheduledTaskStart) (*StartTaskResult, error) {
	schedule.ScheduleID = strings.TrimSpace(schedule.ScheduleID)
	schedule.ScheduleOccurrenceID = strings.TrimSpace(schedule.ScheduleOccurrenceID)
	schedule.ClaimOwner = strings.TrimSpace(schedule.ClaimOwner)
	schedule.ScheduledFor = schedule.ScheduledFor.UTC()
	if schedule.ScheduleID == "" || schedule.ScheduleOccurrenceID == "" || schedule.ScheduledFor.IsZero() || schedule.ClaimOwner == "" {
		return nil, fmt.Errorf("schedule id, occurrence id, scheduled time, and claim owner are required")
	}
	return r.startTaskWithOptions(ctx, task, idgen, startTaskOptions{Schedule: &schedule})
}

func scheduledApprovalID(runID string) string {
	return "approval_schedule_" + strings.TrimPrefix(strings.TrimSpace(runID), "run_")
}

func (r *Runner) ResumeTask(ctx context.Context, task types.Task, run types.TaskRun, reason string, idgen func(prefix string) string) (*StartTaskResult, error) {
	return r.ResumeTaskWithBudget(ctx, task, run, reason, 0, idgen)
}

func (r *Runner) ResumeTaskWithBudget(ctx context.Context, task types.Task, run types.TaskRun, reason string, budgetMicrosUSD int64, idgen func(prefix string) string) (*StartTaskResult, error) {
	if !types.IsTerminalTaskRunStatus(run.Status) {
		return nil, fmt.Errorf("task run %q is not resumable", run.ID)
	}
	return r.startTaskWithOptions(ctx, task, idgen, startTaskOptions{
		ResumeFromRun:   &run,
		ResumeReason:    strings.TrimSpace(reason),
		BudgetMicrosUSD: budgetMicrosUSD,
	})
}

func (r *Runner) ContinueAgentTask(ctx context.Context, task types.Task, run types.TaskRun, prompt string, idgen func(prefix string) string) (*StartTaskResult, error) {
	return r.continueAgentTaskWithOptions(ctx, task, run, prompt, idgen, startTaskOptions{})
}

func (r *Runner) ContinueAgentTaskWithRunInitializer(ctx context.Context, task types.Task, run types.TaskRun, prompt string, idgen func(prefix string) string, init func(*types.TaskRun)) (*StartTaskResult, error) {
	return r.continueAgentTaskWithOptions(ctx, task, run, prompt, idgen, startTaskOptions{RunInitializer: init})
}

func (r *Runner) continueAgentTaskWithOptions(ctx context.Context, task types.Task, run types.TaskRun, prompt string, idgen func(prefix string) string, options startTaskOptions) (*StartTaskResult, error) {
	if task.ExecutionKind != "agent_loop" {
		return nil, fmt.Errorf("task %q is not an agent_loop task", task.ID)
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	if !types.IsTerminalTaskRunStatus(run.Status) {
		return nil, fmt.Errorf("task run %q is not continuable until it reaches a terminal state", run.ID)
	}
	if r.store != nil {
		artifacts, err := r.store.ListArtifacts(ctx, taskstate.ArtifactFilter{TaskID: task.ID, RunID: run.ID})
		if err != nil {
			return nil, err
		}
		foundConversation := false
		for _, artifact := range artifacts {
			if artifact.Kind != "agent_conversation" || len(artifact.ContentText) == 0 {
				continue
			}
			if _, _, err := completedConversationMessages(artifact); err != nil {
				return nil, fmt.Errorf("task run %q has malformed agent_conversation artifact: %w", run.ID, err)
			}
			foundConversation = true
			break
		}
		if !foundConversation {
			return nil, fmt.Errorf("task run %q has no agent_conversation artifact to continue", run.ID)
		}
	}
	options.ResumeFromRun = &run
	options.ResumeReason = "session_prompt"
	options.AppendPrompt = prompt
	return r.startTaskWithOptions(ctx, task, idgen, options)
}

// RetryTaskFromModelCall creates a new run that re-issues Run-local model call
// N of the source run with the prior conversation context preserved. It
// validates against the source Run's authoritative ModelCallCount, then
// enqueues a new run whose checkpoint will carry
// the truncated conversation. The actual truncation happens later in
// resumeCheckpointForRun (worker side) so failures during truncation
// surface as run-level errors with full event context, not as
// pre-create API errors that lose tracing.
func (r *Runner) RetryTaskFromModelCall(ctx context.Context, task types.Task, run types.TaskRun, modelCallIndex int, reason string, idgen func(prefix string) string) (*StartTaskResult, error) {
	if !types.IsTerminalTaskRunStatus(run.Status) {
		return nil, fmt.Errorf("task run %q is not retryable", run.ID)
	}
	if modelCallIndex < 1 {
		return nil, fmt.Errorf("model_call_index must be >= 1, got %d", modelCallIndex)
	}
	if run.ModelCallCount < 1 {
		return nil, fmt.Errorf("source run has no completed model calls")
	}
	if modelCallIndex > run.ModelCallCount {
		return nil, fmt.Errorf("model call %d not found: source run has %d completed model call(s)", modelCallIndex, run.ModelCallCount)
	}
	// Validate the source has a conversation we can truncate. We do
	// this up-front so the API returns a clean 4xx rather than the
	// run failing post-enqueue with a confusing error in the timeline.
	if r.store != nil {
		artifacts, err := r.store.ListArtifacts(ctx, taskstate.ArtifactFilter{TaskID: task.ID, RunID: run.ID})
		if err != nil {
			return nil, err
		}
		var conversationArtifact *types.TaskArtifact
		for _, art := range artifacts {
			if art.Kind == "agent_conversation" && len(art.ContentText) > 0 {
				copy := art
				conversationArtifact = &copy
				break
			}
		}
		if conversationArtifact == nil {
			return nil, fmt.Errorf("task run %q has no agent_conversation artifact to truncate", run.ID)
		}
		saved, _, err := completedConversationMessages(*conversationArtifact)
		if err != nil {
			return nil, fmt.Errorf("task run %q has malformed agent_conversation artifact: %w", run.ID, err)
		}
		if _, err := truncateConversationToRunModelCall(saved, run.ModelCallCount, modelCallIndex); err != nil {
			return nil, err
		}
	}
	return r.startTaskWithOptions(ctx, task, idgen, startTaskOptions{
		ResumeFromRun:        &run,
		ResumeReason:         strings.TrimSpace(reason),
		SourceModelCallIndex: modelCallIndex,
	})
}

func (r *Runner) startTaskWithOptions(ctx context.Context, task types.Task, idgen func(prefix string) string, options startTaskOptions) (*StartTaskResult, error) {
	if r.store == nil {
		return nil, fmt.Errorf("task store is not configured")
	}
	if idgen == nil {
		return nil, fmt.Errorf("resource id generator is required")
	}
	releaseTaskStart := r.taskStarts.lock(task.ID)
	defer releaseTaskStart()
	currentTask, found, err := r.store.GetTask(ctx, task.ID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%w: %q", taskstate.ErrTaskNotFound, task.ID)
	}
	task = currentTask
	lease, err := r.beginOriginRunMutation(ctx, task)
	if err != nil {
		return nil, err
	}
	if lease != nil {
		defer lease.Release()
	}
	if r.exec == nil {
		return nil, fmt.Errorf("executor is not configured")
	}
	if r.workspaces == nil {
		return nil, fmt.Errorf("workspace manager is not configured")
	}
	var scheduledStore taskstate.ScheduledRunStore
	if options.Schedule != nil {
		var ok bool
		scheduledStore, ok = r.store.(taskstate.ScheduledRunStore)
		if !ok {
			return nil, fmt.Errorf("scheduled run store is not configured")
		}
	}

	if options.BudgetMicrosUSD > 0 {
		if options.BudgetMicrosUSD < task.BudgetMicrosUSD {
			return nil, ErrBudgetLower
		}
		task.BudgetMicrosUSD = options.BudgetMicrosUSD
	}
	if options.Schedule != nil {
		preflight, err := scheduledStore.PreflightTaskScheduleRunAdmission(ctx, taskstate.TaskScheduleRunPreflight{
			TaskID:               task.ID,
			ScheduleID:           options.Schedule.ScheduleID,
			ScheduleOccurrenceID: options.Schedule.ScheduleOccurrenceID,
			ScheduledFor:         options.Schedule.ScheduledFor,
			ClaimOwner:           options.Schedule.ClaimOwner,
			CompletedAt:          time.Now().UTC(),
		})
		if err != nil {
			return nil, err
		}
		if preflight.ExistingRun {
			if preflight.Run.Status == "queued" {
				if err := r.enqueueRunWithReconcile(preflight.Task.ID, preflight.Run.ID); err != nil {
					return nil, err
				}
			}
			return &StartTaskResult{
				Task: preflight.Task, Run: preflight.Run,
				TraceID: preflight.Run.TraceID, SpanID: preflight.Run.RootSpanID,
			}, nil
		}
		if preflight.Skipped {
			return nil, ErrActiveRun
		}
		if !preflight.Ready {
			return nil, taskstate.ErrScheduleOccurrenceClaimLost
		}
	}

	requestID := strings.TrimSpace(telemetry.RequestIDFromContext(ctx))
	if requestID == "" {
		requestID = idgen("request")
	}

	trace := r.tracer.Start(requestID)
	defer trace.Finalize()

	ctx = telemetry.WithTraceIDs(ctx, trace.TraceID, trace.RootSpanID())
	now := time.Now().UTC()

	runs, err := r.store.ListRuns(ctx, task.ID)
	if err != nil {
		recordOrchestratorRunFailed(trace, task.ID, types.TaskRun{WorkflowMode: task.WorkflowMode}, "run_list_failed", err)
		return nil, err
	}
	if options.Schedule == nil {
		for _, existingRun := range runs {
			if !types.IsTerminalTaskRunStatus(existingRun.Status) {
				return nil, ErrActiveRun
			}
		}
	}

	requestedModel := strings.TrimSpace(task.RequestedModel)
	requestedProvider := strings.TrimSpace(task.RequestedProvider)
	// An empty provider/model pair is Hecate's auto-routing contract. Preserve
	// the empty model into the first agent-loop request so RuleRouter can choose
	// each eligible provider's default and retain its normal failover set. A
	// bridge/client must never preselect a provider just to make this runnable.
	initialModel := firstNonEmpty(requestedModel, r.config.DefaultModel)
	if task.ExecutionKind == "agent_loop" && requestedModel == "" && requestedProvider == "" {
		initialModel = ""
	}
	run := types.TaskRun{
		ID:              idgen("run"),
		TaskID:          task.ID,
		ProjectID:       strings.TrimSpace(task.ProjectID),
		WorkItemID:      strings.TrimSpace(task.WorkItemID),
		AssignmentID:    strings.TrimSpace(task.AssignmentID),
		Number:          len(runs) + 1,
		Status:          "queued",
		Orchestrator:    "builtin",
		WorkflowMode:    task.WorkflowMode,
		WorkflowVersion: task.WorkflowVersion,
		Model:           initialModel,
		Provider:        requestedProvider,
		WorkspaceID:     "workspace_" + task.ID,
		StartedAt:       now,
		RequestID:       requestID,
		TraceID:         trace.TraceID,
		RootSpanID:      trace.RootSpanID(),
	}
	approvalRequired := r.approvalRequiredForTask(task)
	if approvalRequired {
		run.Status = "awaiting_approval"
		if options.Schedule != nil {
			run.ApprovalCount = 1
		}
	}
	if options.Schedule != nil {
		run.ScheduleID = options.Schedule.ScheduleID
		run.ScheduleOccurrenceID = options.Schedule.ScheduleOccurrenceID
		run.ScheduledFor = options.Schedule.ScheduledFor
	}
	if options.ResumeFromRun != nil {
		prior := *options.ResumeFromRun
		run.ProjectID = firstNonEmpty(run.ProjectID, strings.TrimSpace(prior.ProjectID))
		run.WorkItemID = firstNonEmpty(run.WorkItemID, strings.TrimSpace(prior.WorkItemID))
		run.AssignmentID = firstNonEmpty(run.AssignmentID, strings.TrimSpace(prior.AssignmentID))
		if strings.TrimSpace(prior.WorkspacePath) != "" {
			run.WorkspacePath = prior.WorkspacePath
			run.WorkspaceID = firstNonEmpty(prior.WorkspaceID, run.WorkspaceID)
		}
		// A retained run keeps the exact workflow contract it began with.
		// This matters if a future task edit or migration changes defaults:
		// resume must not silently broaden a report-only run's capabilities.
		if taskworkflow.HasWorkflowSnapshot(prior) {
			run.WorkflowMode = prior.WorkflowMode
			run.WorkflowVersion = prior.WorkflowVersion
		}
		// Inherit cumulative cost from the source run so the per-task
		// cost ceiling holds across the entire resume chain. Source's
		// PriorCost (chain so far excluding source) + Total (source's
		// own spend) gives the new run its accurate prior accumulator.
		run.PriorCostMicrosUSD = prior.PriorCostMicrosUSD + prior.TotalCostMicrosUSD
	}
	sameInputSource := options.RetryFromRun
	if sameInputSource == nil {
		sameInputSource = options.ResumeFromRun
	}
	if sameInputSource != nil && task.ExecutionKind == "agent_loop" && strings.TrimSpace(options.AppendPrompt) == "" {
		prior := *sameInputSource
		run.InputRef = strings.TrimSpace(prior.InputRef)
		if run.InputRef != "" && prior.InputProviderInstance.Valid() {
			run.InputProviderInstance = prior.InputProviderInstance
			run.InputProviderDispatchRecorded = prior.InputProviderDispatchRecorded
			run.Provider = strings.TrimSpace(prior.Provider)
			run.ProviderKind = strings.TrimSpace(prior.ProviderKind)
			run.Model = firstNonEmpty(strings.TrimSpace(prior.Model), run.Model)
		}
	}
	if options.RetryFromRun != nil && taskworkflow.HasWorkflowSnapshot(*options.RetryFromRun) {
		// Retries are fresh attempts, but an existing bounded workflow must not
		// silently adopt a later Task edit or future contract version.
		run.WorkflowMode = options.RetryFromRun.WorkflowMode
		run.WorkflowVersion = options.RetryFromRun.WorkflowVersion
	}
	// This is deliberately after the source-Run snapshot has been applied and
	// before workspace planning/provisioning. Creation validation alone is not
	// sufficient because persisted rows may originate from migrations or a
	// different server version.
	if err := taskworkflow.ValidateExecution(task, run); err != nil {
		recordOrchestratorRunFailed(trace, task.ID, run, "workflow_policy_invalid", err)
		return nil, fmt.Errorf("validate workflow execution: %w", err)
	}
	if taskworkflow.IsQAExecution(task, run) {
		// Resumes and continuations normally reuse the preceding workspace.
		// QA is read-only and has no work product to preserve, so a retained
		// report-only contract instead receives a fresh managed workspace for
		// this Run. That keeps its generated task/run path authoritative.
		run.WorkspacePath = ""
		run.WorkspaceID = "workspace_" + task.ID
	}

	// Preflight: an explicit provider without a model is ambiguous. The empty
	// provider/model pair is intentionally different: it is Hecate auto
	// routing, resolved by the router on the first model call so provider
	// defaults and cross-provider failover remain available.
	if task.ExecutionKind == "agent_loop" {
		if requestedModel == "" && requestedProvider != "" {
			return nil, fmt.Errorf("%w: no model configured; set task.RequestedModel", ErrAgentLoopMisconfigured)
		}
	}
	taskStartedAttrs := map[string]any{
		telemetry.AttrHecatePhase:          "orchestration",
		telemetry.AttrHecateResult:         telemetry.ResultSuccess,
		telemetry.AttrHecateTaskID:         task.ID,
		telemetry.AttrHecateTaskStatus:     task.Status,
		telemetry.AttrHecateTaskRepo:       task.Repo,
		telemetry.AttrHecateTaskBaseBranch: task.BaseBranch,
	}
	if run.WorkflowMode != "" {
		taskStartedAttrs[telemetry.AttrHecateWorkflowMode] = string(run.WorkflowMode)
	}
	trace.Record(telemetry.EventOrchestratorTaskStarted, taskStartedAttrs)
	var provisionPlan workspaceProvisionPlan
	if strings.TrimSpace(run.WorkspacePath) == "" {
		provisionPlan, err = r.workspaces.planProvision(task, run)
		if err != nil {
			recordOrchestratorRunFailed(trace, task.ID, run, "workspace_provision_failed", err)
			return nil, err
		}
		run.WorkspacePath = provisionPlan.workspacePath
	}
	var workspaceStartLease *workspacecoord.WriterLease
	if provisionPlan.requiresWrite {
		workspaceStartLease, err = r.beginWorkspaceProvisioningWrite(ctx, run.WorkspacePath)
	} else {
		workspaceStartLease, err = r.beginWorkspaceWrite(ctx, run.WorkspacePath)
	}
	if err != nil {
		return nil, err
	}
	if workspaceStartLease != nil {
		defer workspaceStartLease.Release()
	}
	var publishedManagedWorkspace *managedWorkspaceProvision
	managedWorkspaceTaskID := task.ID
	managedWorkspaceRunID := run.ID
	if provisionPlan.requiresWrite {
		if workspaceStartLease != nil && workspaceStartLease.Workspace() != filepath.Clean(run.WorkspacePath) {
			return nil, fmt.Errorf("workspace destination changed during provisioning admission")
		}
		run.WorkspacePath, err = r.workspaces.provisionPlannedTracked(ctx, provisionPlan, &publishedManagedWorkspace)
		if err != nil {
			recordOrchestratorRunFailed(trace, task.ID, run, "workspace_provision_failed", err)
			return nil, err
		}
		if options.Schedule != nil && publishedManagedWorkspace != nil {
			// ApplyTaskScheduleRunAdmission is the cross-replica fence. If this
			// candidate loses after publishing its workspace, remove that exact
			// directory only after confirming no durable Run references it.
			defer func() {
				if publishedManagedWorkspace == nil {
					return
				}
				if cleanupErr := r.cleanupUnreferencedScheduledWorkspace(
					ctx,
					managedWorkspaceTaskID,
					managedWorkspaceRunID,
					publishedManagedWorkspace,
				); cleanupErr != nil && r.logger != nil {
					telemetry.Warn(
						r.logger,
						context.WithoutCancel(ctx),
						"scheduled managed workspace cleanup failed",
						slog.String(telemetry.AttrHecateTaskID, managedWorkspaceTaskID),
						slog.String(telemetry.AttrHecateRunID, managedWorkspaceRunID),
						slog.Any("error", cleanupErr),
					)
				}
			}()
		}
	}
	if run, err = r.validateQAWorkspace(task, run); err != nil {
		recordOrchestratorRunFailed(trace, task.ID, run, "workspace_policy_invalid", err)
		return nil, err
	}
	contextSource := options.RetryFromRun
	if contextSource == nil {
		contextSource = options.ResumeFromRun
	}
	if contextSource != nil && len(run.ContextPacket) == 0 && len(contextSource.ContextPacket) > 0 {
		run.ContextPacket = cloneRunContextPacketForNewRun(contextSource.ContextPacket, task.ID, run.ID, run.WorkspacePath, idgen)
	}
	if options.RunInitializer != nil {
		// Initializers enrich a freshly prepared Run with caller-owned context
		// (for example, a chat context packet). They are not an execution-policy
		// authority: preserve the already validated workflow snapshot and, for
		// QA, the generated managed workspace after the callback returns.
		workflowMode := run.WorkflowMode
		workflowVersion := run.WorkflowVersion
		qaWorkspacePath := ""
		qaWorkspaceID := ""
		if taskworkflow.IsQAExecution(task, run) {
			qaWorkspacePath = run.WorkspacePath
			qaWorkspaceID = run.WorkspaceID
		}
		options.RunInitializer(&run)
		run.WorkflowMode = workflowMode
		run.WorkflowVersion = workflowVersion
		if qaWorkspacePath != "" {
			run.WorkspacePath = qaWorkspacePath
			run.WorkspaceID = qaWorkspaceID
		}
		if err := taskworkflow.ValidateExecution(task, run); err != nil {
			recordOrchestratorRunFailed(trace, task.ID, run, "workflow_policy_invalid", err)
			return nil, fmt.Errorf("validate workflow execution after run initialization: %w", err)
		}
		if run, err = r.validateQAWorkspace(task, run); err != nil {
			recordOrchestratorRunFailed(trace, task.ID, run, "workspace_policy_invalid", err)
			return nil, err
		}
	}
	task.LatestRunID = run.ID
	task.Status = run.Status
	if task.StartedAt.IsZero() {
		task.StartedAt = now
	}
	task.FinishedAt = time.Time{}
	task.UpdatedAt = now
	task.RootTraceID = trace.TraceID
	task.LatestTraceID = trace.TraceID
	task.LatestRequestID = requestID

	var started taskstate.RunStartTransitionResult
	var scheduledApproval *types.TaskApproval
	if options.Schedule != nil {
		admittedAt := time.Now().UTC()
		if approvalRequired {
			approval := r.prepareApprovalForTask(trace, task, run, requestID, admittedAt, scheduledApprovalID(run.ID))
			scheduledApproval = &approval
		}
		admitted, admissionErr := scheduledStore.ApplyTaskScheduleRunAdmission(ctx, taskstate.TaskScheduleRunAdmission{
			Task:            task,
			Run:             run,
			Approval:        scheduledApproval,
			BudgetMicrosUSD: options.BudgetMicrosUSD,
			ClaimOwner:      options.Schedule.ClaimOwner,
			CompletedAt:     admittedAt,
		})
		err = admissionErr
		if err == nil && admitted.Skipped {
			err = ErrActiveRun
		}
		if err == nil && !admitted.Applied && !admitted.ExistingRun {
			err = taskstate.ErrScheduleOccurrenceClaimLost
		}
		if err == nil {
			started = taskstate.RunStartTransitionResult{
				Task: admitted.Task, Run: admitted.Run, ExistingRun: admitted.ExistingRun,
			}
		}
	} else {
		started, err = r.store.ApplyRunStartTransition(ctx, taskstate.RunStartTransition{
			Task:            task,
			Run:             run,
			BudgetMicrosUSD: options.BudgetMicrosUSD,
		})
	}
	if err != nil {
		recordOrchestratorRunFailed(trace, task.ID, run, "run_create_failed", err)
		return nil, err
	}
	task = started.Task
	run = started.Run
	if publishedManagedWorkspace != nil {
		if referenced, referenceErr := managedWorkspaceProvisionReferencesRun(publishedManagedWorkspace, run); referenceErr == nil && referenced {
			// The admission result is authoritative durable state. Once that Run
			// owns this exact workspace, later enqueue failures must retain it.
			publishedManagedWorkspace = nil
		}
	}
	if started.ExistingRun {
		if options.Schedule != nil && run.Status == "queued" {
			if err := r.enqueueRunWithReconcile(task.ID, run.ID); err != nil {
				return nil, err
			}
		}
		return &StartTaskResult{
			Task: task, Run: run, TraceID: run.TraceID, SpanID: run.RootSpanID,
		}, nil
	}
	if options.Schedule == nil {
		_, _ = r.emitRunEvent(ctx, task.ID, run.ID, runtimeevents.EventRunCreated.String(), requestID, trace.TraceID, nil)
	}
	if options.ResumeFromRun != nil {
		resumedData := map[string]any{
			"from_run_id": options.ResumeFromRun.ID,
			"reason":      options.ResumeReason,
		}
		if strings.TrimSpace(options.AppendPrompt) != "" {
			resumedData["append_user_prompt"] = options.AppendPrompt
		}
		if options.SourceModelCallIndex > 0 {
			resumedData["source_model_call_index"] = options.SourceModelCallIndex
		}
		_, _ = r.emitRunEvent(ctx, task.ID, run.ID, runtimeevents.EventRunResumedFromEvent.String(), requestID, trace.TraceID, resumedData)
	}

	recordOrchestratorRunStarted(trace, task.ID, run)

	if approvalRequired {
		if options.Schedule == nil {
			if _, err := r.createApprovalForTask(ctx, trace, task, run, requestID, now, idgen); err != nil {
				return nil, err
			}
			run.ApprovalCount = 1
			run, err = r.store.UpdateRun(ctx, run)
			if err != nil {
				return nil, err
			}
			_, _ = r.emitRunEvent(ctx, task.ID, run.ID, runtimeevents.EventRunAwaitingApproval.String(), requestID, trace.TraceID, nil)
		}
		task.Status = "awaiting_approval"
	} else {
		trace.Record(telemetry.EventQueueEnqueued, map[string]any{
			telemetry.AttrHecateTaskID:       task.ID,
			telemetry.AttrHecateRunID:        run.ID,
			telemetry.AttrHecateQueueBackend: r.getQueue().Backend(),
		})
		var enqueueErr error
		if options.Schedule != nil {
			enqueueErr = r.enqueueRunWithReconcile(task.ID, run.ID)
		} else {
			enqueueErr = r.emitRunQueuedAndEnqueue(ctx, task.ID, run.ID, requestID, trace.TraceID, nil)
		}
		if enqueueErr != nil {
			return nil, enqueueErr
		}
	}

	return &StartTaskResult{
		Task:    task,
		Run:     run,
		TraceID: trace.TraceID,
		SpanID:  trace.RootSpanID(),
	}, nil
}

func (r *Runner) cleanupUnreferencedScheduledWorkspace(
	ctx context.Context,
	taskID string,
	runID string,
	published *managedWorkspaceProvision,
) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), scheduledWorkspaceCleanupTimeout)
	defer cancel()

	// Admission can commit and still return an ambiguous error. Inspect every
	// durable Run before removing the candidate so a cross-task reference or
	// an admitted candidate is preserved as well as the ordinary Task-local
	// cases.
	runs, err := r.store.ListRunsByFilter(cleanupCtx, taskstate.RunFilter{})
	if err != nil {
		return fmt.Errorf("list durable runs before managed workspace cleanup: %w", err)
	}
	for _, durableRun := range runs {
		referenced, err := managedWorkspaceProvisionReferencesRun(published, durableRun)
		if err != nil {
			return fmt.Errorf("inspect durable run %q workspace before cleanup: %w", durableRun.ID, err)
		}
		if referenced {
			return nil
		}
	}
	return r.workspaces.cleanupManagedWorkspaceProvision(taskID, runID, published)
}

func managedWorkspaceProvisionReferencesRun(published *managedWorkspaceProvision, run types.TaskRun) (bool, error) {
	if published == nil {
		return false, nil
	}
	workspacePath := strings.TrimSpace(run.WorkspacePath)
	if workspacePath == "" {
		return false, nil
	}
	absolutePath, err := filepath.Abs(workspacePath)
	if err != nil {
		return false, fmt.Errorf("resolve absolute workspace path: %w", err)
	}
	if filepath.Clean(absolutePath) == published.workspacePath {
		return true, nil
	}
	canonicalPath, err := workspacecoord.CanonicalWorkspace(workspacePath)
	if errors.Is(err, os.ErrNotExist) {
		// A non-existent, lexically different path cannot currently alias the
		// published directory. This permits cleanup despite stale old Runs.
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return canonicalPath == published.workspacePath, nil
}

func cloneRunContextPacketForNewRun(raw json.RawMessage, taskID, runID, workspacePath string, idgen func(string) string) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var packet map[string]any
	if err := json.Unmarshal(raw, &packet); err != nil {
		return append(json.RawMessage(nil), raw...)
	}
	if len(packet) == 0 {
		return append(json.RawMessage(nil), raw...)
	}
	if idgen != nil {
		packet["id"] = idgen("ctx")
	}
	refs, _ := packet["refs"].(map[string]any)
	if refs == nil {
		refs = map[string]any{}
	}
	refs["task_id"] = taskID
	refs["run_id"] = runID
	packet["refs"] = refs
	if workspacePath = strings.TrimSpace(workspacePath); workspacePath != "" {
		packet["workspace"] = workspacePath
	} else {
		delete(packet, "workspace")
	}
	updated, err := json.Marshal(packet)
	if err != nil {
		return append(json.RawMessage(nil), raw...)
	}
	return updated
}

func (r *Runner) CancelRun(ctx context.Context, task types.Task, runID string, reason string) (types.TaskRun, error) {
	run, found, err := r.store.GetRun(ctx, task.ID, runID)
	if err != nil {
		return types.TaskRun{}, err
	}
	if !found {
		return types.TaskRun{}, fmt.Errorf("task run %q not found", runID)
	}
	if types.IsTerminalTaskRunStatus(run.Status) {
		if err := r.WaitRunExit(ctx, run.ID); err != nil {
			return types.TaskRun{}, fmt.Errorf("wait for terminal run %q executor exit: %w", run.ID, err)
		}
		return r.cleanupCancelledRunAfterDrain(ctx, task, run, nil)
	}

	message := "run cancelled"
	if r := strings.TrimSpace(reason); r != "" {
		message = "run cancelled: " + r
	}
	requestID := strings.TrimSpace(telemetry.RequestIDFromContext(ctx))
	traceIDs := telemetry.TraceIDsFromContext(ctx)
	return r.cancelRunWithMessage(ctx, task, run, message, requestID, traceIDs.TraceID)
}

// WaitRunExit cancels and drains any process-local executor still registered
// for a durable terminal run. Owner-deletion retries use this after an earlier
// cancellation timed out: terminal persistence alone is not proof that the
// executor goroutine has exited.
func (r *Runner) WaitRunExit(ctx context.Context, runID string) error {
	r.cancelInFlightJob(runID)
	if closer, ok := r.agent.(agentTerminalRunCloser); ok {
		closer.CloseTerminalsForRun(ctx, runID)
	}
	return r.cancelAndWaitForInFlightJob(ctx, runID)
}

func (r *Runner) cancelRunWithMessage(ctx context.Context, task types.Task, run types.TaskRun, message, requestID, traceID string) (types.TaskRun, error) {
	var trace *profiler.Trace
	if requestID != "" && r.tracer != nil {
		if existing, found := r.tracer.Get(requestID); found {
			trace = existing
			traceID = trace.TraceID
			ctx = telemetry.WithTraceIDs(ctx, trace.TraceID, trace.RootSpanID())
		} else {
			trace = r.tracer.Start(requestID)
			defer trace.Finalize()
			traceID = trace.TraceID
			ctx = telemetry.WithTraceIDs(ctx, trace.TraceID, trace.RootSpanID())
		}
	}

	now := time.Now().UTC()
	result, err := r.applyTerminalRunTransition(ctx, cancelRunTerminalTransition(task, run, message, requestID, traceID, trace, now))
	if err != nil {
		return types.TaskRun{}, err
	}

	// Make the operator's terminal decision durable before waking the
	// executor's cancellation finalizer. Otherwise both paths can observe a
	// running row and race to append the same terminal event.
	r.cancelInFlightJob(run.ID)
	if closer, ok := r.agent.(agentTerminalRunCloser); ok {
		closer.CloseTerminalsForRun(ctx, run.ID)
	}
	drainErr := r.cancelAndWaitForInFlightJob(ctx, run.ID)
	if drainErr != nil {
		return types.TaskRun{}, fmt.Errorf("wait for cancelled run %q executor exit: %w", run.ID, drainErr)
	}
	return r.cleanupCancelledRunAfterDrain(ctx, result.Task, result.Run, trace)
}

func (r *Runner) cleanupCancelledRunAfterDrain(ctx context.Context, task types.Task, run types.TaskRun, trace *profiler.Trace) (types.TaskRun, error) {
	if run.Status != "cancelled" {
		return run, nil
	}
	currentTask, found, err := r.store.GetTask(ctx, task.ID)
	if err != nil {
		return types.TaskRun{}, err
	}
	if !found {
		return types.TaskRun{}, fmt.Errorf("task %q not found", task.ID)
	}
	currentRun, found, err := r.store.GetRun(ctx, task.ID, run.ID)
	if err != nil {
		return types.TaskRun{}, err
	}
	if !found {
		return types.TaskRun{}, fmt.Errorf("task run %q not found", run.ID)
	}
	if currentRun.Status != "cancelled" {
		return currentRun, nil
	}
	// Children can be persisted while the cancelled executor drains. Settle
	// those late arrivals at cleanup time; the task-state transition preserves
	// the original run/task terminal timestamps for a same-status replay.
	cleanupAt := time.Now().UTC()
	cleanup := cancelRunTerminalTransition(
		currentTask,
		currentRun,
		currentRun.LastError,
		currentRun.RequestID,
		currentRun.TraceID,
		trace,
		cleanupAt,
	)
	cleanup.SuppressDuplicateEvent = true
	cleanup.EmitTaskUpdated = false
	cleanup.PreserveTaskProjection = true
	result, err := r.applyTerminalRunTransition(ctx, cleanup)
	if err != nil {
		return types.TaskRun{}, err
	}
	return result.Run, nil
}

// Shutdown stops the runner's queue workers, cancels every in-flight
// agent loop, and waits for them to finalize. Two reasons it matters:
//
//   - In-flight runs may have spawned MCP subprocesses. Without
//     cancellation those subprocesses orphan when the gateway exits;
//     cancelling jobCtx propagates through the agent loop to its
//     deferred host.Close, which tears the subprocesses down.
//   - Even non-MCP runs need to flush their final UpdateRun /
//     UpdateTask calls so an SIGTERM mid-execution doesn't leave the
//     run row stuck in "running".
//
// Bounded by ctx — callers pass a deadline (10–30s is typical). On
// deadline expiry Shutdown returns ctx.Err() and the caller can decide
// whether to force-exit; the in-flight goroutines remain cancelled and
// will continue draining in the background until the process exits.
//
// Idempotent: a second call after the first returns immediately with
// the same drain semantics (any goroutines already finished are not
// re-waited). Safe to call from multiple goroutines.

func (r *Runner) executeRun(ctx context.Context, trace *profiler.Trace, task types.Task, run types.TaskRun, requestID string, resumeCheckpoint *ResumeCheckpoint) (*StartTaskResult, error) {
	// Revalidate after a worker claims the durable row. This is a separate
	// boundary from StartTask: queued work can outlive deployments, migrations,
	// and direct store writes. Do not lease a workspace or select an executor
	// until the recorded contract is known safe.
	if err := taskworkflow.ValidateExecution(task, run); err != nil {
		recordOrchestratorRunFailed(trace, task.ID, run, "workflow_policy_invalid", err)
		return nil, fmt.Errorf("validate workflow execution: %w", err)
	}
	validatedRun, err := r.validateQAWorkspace(task, run)
	if err != nil {
		recordOrchestratorRunFailed(trace, task.ID, run, "workspace_policy_invalid", err)
		return nil, err
	}
	run = validatedRun
	workspaceLease, err := r.beginWorkspaceWrite(ctx, run.WorkspacePath)
	if err != nil {
		return nil, err
	}
	if workspaceLease != nil {
		defer workspaceLease.Release()
	}

	executor := r.executorForTask(task)
	systemPrompt := ""
	if r.resolveSysPrompt != nil {
		workspacePromptPath := run.WorkspacePath
		if task.WorkspaceSystemPromptPolicy == types.WorkspaceSystemPromptExclude {
			workspacePromptPath = ""
		}
		systemPrompt = r.resolveSysPrompt(ctx, "", task.SystemPrompt, workspacePromptPath)
	}
	var agentInput AgentInput
	if task.ExecutionKind == "agent_loop" && strings.TrimSpace(run.InputRef) != "" {
		if r.resolveAgentInput == nil {
			return nil, fmt.Errorf("agent input resolver is not configured for run %q", run.ID)
		}
		agentInput, err = r.resolveAgentInput(ctx, task, run)
		if err != nil {
			return nil, fmt.Errorf("resolve agent input: %w", err)
		}
		if agentInput.Release != nil {
			defer agentInput.Release()
		}
	}
	var inputMessage *types.Message
	if task.ExecutionKind == "agent_loop" && strings.TrimSpace(run.InputRef) != "" {
		inputMessage = &agentInput.Message
	}
	execution, err := executor.Execute(ctx, ExecutionSpec{
		Task:             taskForRun(task, run),
		Run:              run,
		RequestID:        requestID,
		TraceID:          trace.TraceID,
		RootSpanID:       trace.RootSpanID(),
		StartedAt:        time.Now().UTC(),
		ResumeCheckpoint: resumeCheckpoint,
		NewID:            defaultResourceID,
		UpsertStep:       func(step types.TaskStep) error { return r.upsertStep(ctx, step) },
		UpsertArtifact:   func(artifact types.TaskArtifact) error { return r.upsertArtifact(ctx, artifact) },
		RepairArtifact: func(artifact types.TaskArtifact) error {
			repairCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			return r.upsertArtifact(repairCtx, artifact)
		},
		GetArtifact: func(taskID, artifactID string) (types.TaskArtifact, bool, error) {
			return r.store.GetArtifact(ctx, taskID, artifactID)
		},
		EmitRunEvent: func(eventType string, data map[string]any) {
			_, _ = r.emitRunEvent(ctx, task.ID, run.ID, eventType, requestID, trace.TraceID, data)
		},
		RTKEnabled:                  task.RTKEnabled,
		SystemPrompt:                systemPrompt,
		ShellNetworkAllowedHosts:    r.config.ShellNetwork.AllowedHosts,
		ShellNetworkAllowPrivateIPs: r.config.ShellNetwork.AllowPrivateIPs,
		InputMessage:                inputMessage,
		ChatRequirements:            agentInput.Requirements,
		RecordProviderAttempt: func(route types.RouteDecision) error {
			return r.recordAgentInputProviderAttempt(ctx, task, run, route)
		},
	})
	if err != nil {
		recordOrchestratorRunFailed(trace, task.ID, run, "executor_failed", err)
		return nil, err
	}

	// Run settlement must outlive cancellation of the executor context. In
	// particular, an operator cancellation persists the terminal winner before
	// cancelling this context; the drained executor still has authoritative
	// completed-model-call accounting to merge into that durable winner.
	settlementCtx, cancelSettlement := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancelSettlement()
	return newExecutionResultPersister(r, trace, task, run, requestID).persist(settlementCtx, execution)
}

func mergeStepTelemetryAttrs(dst map[string]any, src map[string]any) {
	if len(src) == 0 {
		return
	}
	for _, key := range stepTelemetryAttrKeys {
		if value, ok := src[key]; ok {
			dst[key] = value
		}
	}
}

type gitSummaryArtifactPayload struct {
	WorkspacePath string                 `json:"workspace_path"`
	Files         []gitSummaryFileChange `json:"files"`
	DiffStat      string                 `json:"diff_stat,omitempty"`
}

type gitSummaryFileChange struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	Raw    string `json:"raw"`
}

func parseGitPorcelainStatus(output string) []gitSummaryFileChange {
	lines := strings.Split(output, "\n")
	changes := make([]gitSummaryFileChange, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		status := strings.TrimSpace(line[:min(len(line), 2)])
		path := ""
		if len(line) > 3 {
			path = strings.TrimSpace(line[3:])
		}
		if renamed := strings.Split(path, " -> "); len(renamed) == 2 {
			path = renamed[1]
		}
		changes = append(changes, gitSummaryFileChange{
			Path:   path,
			Status: status,
			Raw:    line,
		})
	}
	return changes
}

func taskForRun(task types.Task, run types.TaskRun) types.Task {
	executionTask := task
	if strings.TrimSpace(run.WorkspacePath) != "" {
		executionTask.WorkingDirectory = run.WorkspacePath
		executionTask.SandboxAllowedRoot = run.WorkspacePath
	}
	return executionTask
}

func defaultWorkerID() string {
	hostname, _ := os.Hostname()
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		hostname = "worker"
	}
	return hostname + "-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
}

func defaultResourceID(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "id"
	}
	now := strconv.FormatInt(time.Now().UTC().UnixNano(), 36)
	seq := strconv.FormatUint(resourceIDCounter.Add(1), 36)
	return prefix + "_" + now + "_" + seq
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func terminalRunEventType(status string) string {
	if status == "completed" {
		return runtimeevents.EventRunFinished.String()
	}
	if status == "failed" {
		return runtimeevents.EventRunFailed.String()
	}
	if status == "cancelled" {
		return runtimeevents.EventRunCancelled.String()
	}
	return "run." + status
}

func recordOrchestratorRunStarted(trace *profiler.Trace, taskID string, run types.TaskRun) {
	if trace == nil {
		return
	}
	attrs := map[string]any{
		telemetry.AttrHecatePhase:       "orchestration",
		telemetry.AttrHecateResult:      telemetry.ResultSuccess,
		telemetry.AttrHecateTaskID:      taskID,
		telemetry.AttrHecateRunID:       run.ID,
		telemetry.AttrHecateRunNumber:   run.Number,
		telemetry.AttrHecateRunStatus:   run.Status,
		telemetry.AttrGenAIRequestModel: run.Model,
	}
	if run.WorkflowMode != "" {
		attrs[telemetry.AttrHecateWorkflowMode] = string(run.WorkflowMode)
	}
	trace.Record(telemetry.EventOrchestratorRunStarted, attrs)
}

func recordOrchestratorRunFailed(trace *profiler.Trace, taskID string, run types.TaskRun, errorKind string, err error) {
	if trace == nil || err == nil {
		return
	}
	attrs := map[string]any{
		telemetry.AttrHecatePhase:     "orchestration",
		telemetry.AttrHecateResult:    telemetry.ResultError,
		telemetry.AttrHecateErrorKind: errorKind,
		telemetry.AttrErrorType:       errorKind,
		telemetry.AttrErrorMessage:    err.Error(),
		telemetry.AttrHecateTaskID:    taskID,
	}
	if strings.TrimSpace(run.ID) != "" {
		attrs[telemetry.AttrHecateRunID] = run.ID
	}
	// Failure telemetry can occur before a persisted snapshot validates. Emit
	// only the closed QA selector, never an arbitrary malformed stored value.
	if taskworkflow.IsQA(run.WorkflowMode) {
		attrs[telemetry.AttrHecateWorkflowMode] = string(types.WorkflowModeQA)
	}
	trace.Record(telemetry.EventOrchestratorRunFailed, attrs)
}

func spanIDByName(trace *profiler.Trace, name string) string {
	if trace == nil {
		return ""
	}
	for _, span := range trace.Spans() {
		if span.Name == name {
			return span.SpanID
		}
	}
	return ""
}

func maxInt(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func findOldestRunStart(runs []types.TaskRun) time.Time {
	var oldest time.Time
	for _, run := range runs {
		if run.StartedAt.IsZero() {
			continue
		}
		if oldest.IsZero() || run.StartedAt.Before(oldest) {
			oldest = run.StartedAt
		}
	}
	return oldest
}
