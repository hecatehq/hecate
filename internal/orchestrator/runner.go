package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/runtimeevents"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/internal/workspace"
	"github.com/hecatehq/hecate/pkg/types"
)

// ErrAgentLoopMisconfigured is returned by StartTask when an agent_loop
// task cannot be started due to missing configuration detectable before
// the run is created. Callers should surface this as a client error
// (HTTP 422) rather than a gateway error (500).
var ErrAgentLoopMisconfigured = errors.New("agent_loop misconfigured")

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
	// AgentLoopMaxTurns caps how many LLM round-trips a single
	// agent_loop run can make. Default 8 (set in NewAgentLoopExecutor
	// when zero or negative). Acts as a runaway-cost safety net.
	AgentLoopMaxTurns int
	// HTTPPolicy bounds the agent_loop `http_request` tool: timeout,
	// response size cap, SSRF guards, optional host allowlist. Zero
	// values fall back to safe defaults inside the executor (30s,
	// 256 KiB, private-IPs blocked, all public hosts allowed).
	HTTPPolicy HTTPRequestPolicy
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
	queue             RunQueue
	queueLease        time.Duration
	reconcileInterval time.Duration
	workerID          string
	jobMu             sync.Mutex
	queueMu           sync.RWMutex
	jobs              map[string]context.CancelFunc
	// workerCtx is the lifetime context for queue-worker goroutines and
	// every in-flight job they process. Shutdown cancels this; processQueue
	// observes the cancel and stops claiming new work, and every job's
	// context is parented from it so cancellation cascades into running
	// agent loops (which in turn close their MCP hosts via the existing
	// defer chain).
	workerCtx    context.Context
	workerCancel context.CancelFunc
	// workerWg tracks both the worker goroutines and in-flight jobs.
	// Shutdown waits on it so the gateway doesn't return from main()
	// while a run's still finalizing into the store.
	workerWg         sync.WaitGroup
	shutdownOnce     sync.Once
	policies         map[string]struct{}
	metrics          *telemetry.OrchestratorMetrics
	resolveSysPrompt SystemPromptResolver
	// mcpHostFactory is the factory used when building or rebuilding the
	// agent_loop executor. Stored here so SetAgentLLMClient (which
	// rebuilds the executor from scratch) can re-bind the same factory
	// instead of resetting to the no-cipher default.
	mcpHostFactory AgentMCPHostFactory
}

type StartTaskResult struct {
	Task      types.Task
	Run       types.TaskRun
	Steps     []types.TaskStep
	Artifacts []types.TaskArtifact
	TraceID   string
	SpanID    string
}

type startTaskOptions struct {
	ResumeFromRun *types.TaskRun
	ResumeReason  string
	AppendPrompt  string
	// RetryFromTurn, when > 0, signals the new run should resume from
	// the source run's conversation truncated to right before turn N.
	// Used by the retry-from-turn-N code path; ignored when
	// ResumeFromRun is nil. The runner persists this on the new run's
	// `run.created` event so the worker that later claims the run can
	// rebuild the truncated checkpoint without keeping in-memory state.
	RetryFromTurn int
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
	workerCtx, workerCancel := context.WithCancel(context.Background())
	runner := &Runner{
		logger:            logger,
		store:             store,
		tracer:            tracer,
		exec:              NewStubExecutor(),
		shell:             NewShellExecutor(worker),
		file:              NewFileExecutor(worker),
		git:               NewGitExecutor(worker),
		workspaces:        NewWorkspaceManager(""),
		config:            cfg,
		queue:             queue,
		queueLease:        queueLease,
		reconcileInterval: reconcileInterval,
		workerID:          defaultWorkerID(),
		jobs:              make(map[string]context.CancelFunc),
		policies:          make(map[string]struct{}),
		workerCtx:         workerCtx,
		workerCancel:      workerCancel,
	}
	// LLM client + max-turns are wired post-construction via
	// SetAgentLLMClient — main.go injects the gateway.Service after
	// it's built. nil here means the loop falls back to a pass-through
	// step until configured (see executor_agent_loop.go runWithoutLLM).
	// Gated tools come from the same approval policies as
	// task-level gating, so an operator who approves shell at the
	// task layer also approves it inside agent_loop tool calls.
	agent := NewAgentLoopExecutor(nil, runner.shell, runner.file, runner.git, cfg.AgentLoopMaxTurns, agentLoopGatedTools(runner.policies), cfg.HTTPPolicy)
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
	for worker := 0; worker < workers; worker++ {
		runner.workerWg.Add(1)
		go runner.processQueue()
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

// SetAgentLLMClient wires the LLM seam into the agent_loop executor.
// Safe to call after NewRunner — main.go invokes this once the gateway
// service is constructed, since the chat path needs its own deps that
// the runner doesn't otherwise know about. Nil unwires the loop.
func (r *Runner) SetAgentLLMClient(llm AgentLLMClient) {
	agent := NewAgentLoopExecutor(llm, r.shell, r.file, r.git, r.config.AgentLoopMaxTurns, agentLoopGatedTools(r.policies), r.config.HTTPPolicy)
	// Re-bind the stored MCP factory — the executor is rebuilt from
	// scratch above so the prior binding is gone. Fall back to the
	// no-cipher default if SetMCPHostFactory was never called.
	factory := r.mcpHostFactory
	if factory == nil {
		factory = DefaultMCPHostFactory
	}
	agent.SetMCPHostFactory(factory)
	// Same story for metrics — the rebuild lost the prior wiring.
	if r.metrics != nil {
		agent.SetMetrics(r.metrics)
	}
	r.agent = agent
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
		return []string{"shell_exec", "git_exec", "git_status", "git_diff", "file_write", "file_edit", "apply_patch", "read_file", "grep", "glob", "artifact_read", "list_dir", "http_request"}
	}
	out := make([]string, 0, len(policies))
	for p := range policies {
		switch p {
		case "shell_exec":
			out = append(out, p)
		case "git_exec":
			out = append(out, "git_exec", "git_status", "git_diff")
		case "file_write":
			out = append(out, "file_write", "file_edit", "apply_patch")
		case "read_file":
			out = append(out, "read_file", "grep", "glob", "artifact_read")
		case "network_egress":
			// `network_egress` is the historical name for the
			// outbound-network policy applied to shell tasks. We
			// reuse it here so an operator who already gates
			// network on shell automatically gates the agent's
			// HTTP tool too — no second toggle to remember.
			out = append(out, "http_request")
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
	r.jobMu.Lock()
	stats.InFlightJobs = len(r.jobs)
	r.jobMu.Unlock()
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

func (r *Runner) ResumeTask(ctx context.Context, task types.Task, run types.TaskRun, reason string, idgen func(prefix string) string) (*StartTaskResult, error) {
	if !types.IsTerminalTaskRunStatus(run.Status) {
		return nil, fmt.Errorf("task run %q is not resumable", run.ID)
	}
	return r.startTaskWithOptions(ctx, task, idgen, startTaskOptions{
		ResumeFromRun: &run,
		ResumeReason:  strings.TrimSpace(reason),
	})
}

func (r *Runner) ContinueAgentTask(ctx context.Context, task types.Task, run types.TaskRun, prompt string, idgen func(prefix string) string) (*StartTaskResult, error) {
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
			var saved []types.Message
			if err := json.Unmarshal([]byte(artifact.ContentText), &saved); err != nil {
				return nil, fmt.Errorf("task run %q has malformed agent_conversation artifact: %w", run.ID, err)
			}
			foundConversation = true
			break
		}
		if !foundConversation {
			return nil, fmt.Errorf("task run %q has no agent_conversation artifact to continue", run.ID)
		}
	}
	return r.startTaskWithOptions(ctx, task, idgen, startTaskOptions{
		ResumeFromRun: &run,
		ResumeReason:  "session_prompt",
		AppendPrompt:  prompt,
	})
}

// RetryTaskFromTurn creates a new run that re-issues turn N of the
// source run with the prior conversation context preserved. Validates
// the source is a terminal agent_loop run with at least N completed
// assistant turns, then enqueues a new run whose checkpoint will carry
// the truncated conversation. The actual truncation happens later in
// resumeCheckpointForRun (worker side) so failures during truncation
// surface as run-level errors with full event context, not as
// pre-create API errors that lose tracing.
func (r *Runner) RetryTaskFromTurn(ctx context.Context, task types.Task, run types.TaskRun, turn int, reason string, idgen func(prefix string) string) (*StartTaskResult, error) {
	if !types.IsTerminalTaskRunStatus(run.Status) {
		return nil, fmt.Errorf("task run %q is not retryable", run.ID)
	}
	if turn < 1 {
		return nil, fmt.Errorf("turn must be >= 1, got %d", turn)
	}
	// Validate the source has a conversation we can truncate. We do
	// this up-front so the API returns a clean 4xx rather than the
	// run failing post-enqueue with a confusing error in the timeline.
	if r.store != nil {
		artifacts, err := r.store.ListArtifacts(ctx, taskstate.ArtifactFilter{TaskID: task.ID, RunID: run.ID})
		if err != nil {
			return nil, err
		}
		var convo []byte
		for _, art := range artifacts {
			if art.Kind == "agent_conversation" && len(art.ContentText) > 0 {
				convo = []byte(art.ContentText)
				break
			}
		}
		if len(convo) == 0 {
			return nil, fmt.Errorf("task run %q has no agent_conversation artifact to truncate", run.ID)
		}
		var saved []types.Message
		if err := json.Unmarshal(convo, &saved); err != nil {
			return nil, fmt.Errorf("task run %q has malformed agent_conversation artifact: %w", run.ID, err)
		}
		turns := countAssistantTurns(saved)
		if turn > turns {
			return nil, fmt.Errorf("turn %d not found: source run has %d assistant turn(s)", turn, turns)
		}
	}
	return r.startTaskWithOptions(ctx, task, idgen, startTaskOptions{
		ResumeFromRun: &run,
		ResumeReason:  strings.TrimSpace(reason),
		RetryFromTurn: turn,
	})
}

func (r *Runner) startTaskWithOptions(ctx context.Context, task types.Task, idgen func(prefix string) string, options startTaskOptions) (*StartTaskResult, error) {
	if r.store == nil {
		return nil, fmt.Errorf("task store is not configured")
	}
	if idgen == nil {
		return nil, fmt.Errorf("resource id generator is required")
	}
	if r.exec == nil {
		return nil, fmt.Errorf("executor is not configured")
	}
	if r.workspaces == nil {
		return nil, fmt.Errorf("workspace manager is not configured")
	}

	// Preflight: agent_loop needs a model before we create the run.
	// Failing here (before the run row exists) gives the API caller a
	// clean 422 and avoids a run that would immediately fail on its
	// first LLM call with a confusing "no route" error.
	if task.ExecutionKind == "agent_loop" {
		if strings.TrimSpace(task.RequestedModel) == "" {
			return nil, fmt.Errorf("%w: no model configured; set task.RequestedModel", ErrAgentLoopMisconfigured)
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

	trace.Record(telemetry.EventOrchestratorTaskStarted, map[string]any{
		telemetry.AttrHecatePhase:          "orchestration",
		telemetry.AttrHecateResult:         telemetry.ResultSuccess,
		telemetry.AttrHecateTaskID:         task.ID,
		telemetry.AttrHecateTaskStatus:     task.Status,
		telemetry.AttrHecateTaskRepo:       task.Repo,
		telemetry.AttrHecateTaskBaseBranch: task.BaseBranch,
	})

	runs, err := r.store.ListRuns(ctx, task.ID)
	if err != nil {
		recordOrchestratorRunFailed(trace, task.ID, "", "run_list_failed", err)
		return nil, err
	}

	run := types.TaskRun{
		ID:           idgen("run"),
		TaskID:       task.ID,
		Number:       len(runs) + 1,
		Status:       "queued",
		Orchestrator: "builtin",
		Model:        firstNonEmpty(task.RequestedModel, r.config.DefaultModel),
		Provider:     task.RequestedProvider,
		WorkspaceID:  "workspace_" + task.ID,
		StartedAt:    now,
		RequestID:    requestID,
		TraceID:      trace.TraceID,
		RootSpanID:   trace.RootSpanID(),
	}
	if r.approvalRequiredForTask(task) {
		run.Status = "awaiting_approval"
	}
	if options.ResumeFromRun != nil {
		prior := *options.ResumeFromRun
		if strings.TrimSpace(prior.WorkspacePath) != "" {
			run.WorkspacePath = prior.WorkspacePath
			run.WorkspaceID = firstNonEmpty(prior.WorkspaceID, run.WorkspaceID)
		}
		// Inherit cumulative cost from the source run so the per-task
		// cost ceiling holds across the entire resume chain. Source's
		// PriorCost (chain so far excluding source) + Total (source's
		// own spend) gives the new run its accurate prior accumulator.
		run.PriorCostMicrosUSD = prior.PriorCostMicrosUSD + prior.TotalCostMicrosUSD
	}
	if strings.TrimSpace(run.WorkspacePath) == "" {
		run.WorkspacePath, err = r.workspaces.Provision(ctx, task, run)
		if err != nil {
			recordOrchestratorRunFailed(trace, task.ID, run.ID, "workspace_provision_failed", err)
			return nil, err
		}
	}
	run, err = r.store.CreateRun(ctx, run)
	if err != nil {
		recordOrchestratorRunFailed(trace, task.ID, run.ID, "run_create_failed", err)
		return nil, err
	}
	_, _ = r.emitRunEvent(ctx, task.ID, run.ID, "run.created", requestID, trace.TraceID, nil)
	if options.ResumeFromRun != nil {
		resumedData := map[string]any{
			"resumed_from_run_id": options.ResumeFromRun.ID,
			"reason":              options.ResumeReason,
		}
		if strings.TrimSpace(options.AppendPrompt) != "" {
			resumedData["append_user_prompt"] = options.AppendPrompt
		}
		if options.RetryFromTurn > 0 {
			resumedData["retry_from_turn"] = options.RetryFromTurn
		}
		_, _ = r.emitRunEvent(ctx, task.ID, run.ID, "run.resumed_from_event", requestID, trace.TraceID, resumedData)
	}

	recordOrchestratorRunStarted(trace, task.ID, run)

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

	if r.approvalRequiredForTask(task) {
		if _, err := r.createApprovalForTask(ctx, trace, task, run, requestID, now, idgen); err != nil {
			return nil, err
		}
		run.ApprovalCount = 1
		run, err = r.store.UpdateRun(ctx, run)
		if err != nil {
			return nil, err
		}
		_, _ = r.emitRunEvent(ctx, task.ID, run.ID, "run.awaiting_approval", requestID, trace.TraceID, nil)
		task.Status = "awaiting_approval"
	} else {
		trace.Record(telemetry.EventQueueEnqueued, map[string]any{
			telemetry.AttrHecateTaskID:       task.ID,
			telemetry.AttrHecateRunID:        run.ID,
			telemetry.AttrHecateQueueBackend: r.getQueue().Backend(),
		})
		if err := r.emitRunQueuedAndEnqueue(ctx, task.ID, run.ID, requestID, trace.TraceID, nil); err != nil {
			return nil, err
		}
	}

	if _, err := r.store.UpdateTask(ctx, task); err != nil {
		return nil, err
	}

	return &StartTaskResult{
		Task:    task,
		Run:     run,
		TraceID: trace.TraceID,
		SpanID:  trace.RootSpanID(),
	}, nil
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
		return run, nil
	}

	message := "run cancelled"
	if r := strings.TrimSpace(reason); r != "" {
		message = "run cancelled: " + r
	}
	requestID := strings.TrimSpace(telemetry.RequestIDFromContext(ctx))
	traceIDs := telemetry.TraceIDsFromContext(ctx)
	return r.cancelRunWithMessage(ctx, task, run, message, requestID, traceIDs.TraceID)
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

	r.jobMu.Lock()
	cancel, ok := r.jobs[run.ID]
	r.jobMu.Unlock()
	if ok {
		cancel()
	}

	now := time.Now().UTC()
	result, err := r.applyTerminalRunTransition(ctx, terminalRunTransition{
		Task:                     task,
		Run:                      run,
		Status:                   "cancelled",
		Message:                  message,
		RequestID:                requestID,
		TraceID:                  traceID,
		Trace:                    trace,
		Now:                      now,
		CancelActiveSteps:        true,
		CancelStreamingArtifacts: true,
		CancelPendingApprovals:   true,
		EmitTaskUpdated:          true,
		EventData:                map[string]any{"reason": message},
	})
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
	executor := r.executorForTask(task)
	systemPrompt := ""
	if r.resolveSysPrompt != nil {
		systemPrompt = r.resolveSysPrompt(ctx, "", task.SystemPrompt, run.WorkspacePath)
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
	})
	if err != nil {
		recordOrchestratorRunFailed(trace, task.ID, run.ID, "executor_failed", err)
		return nil, err
	}

	persistedSteps := make([]types.TaskStep, 0, len(execution.Steps))
	for _, step := range execution.Steps {
		eventName := telemetry.EventOrchestratorStepCompleted
		if step.Status == "failed" || step.Status == "cancelled" || step.Result == telemetry.ResultError {
			eventName = telemetry.EventOrchestratorStepFailed
		}
		var stepDurationMS int64
		if !step.StartedAt.IsZero() && !step.FinishedAt.IsZero() {
			stepDurationMS = step.FinishedAt.Sub(step.StartedAt).Milliseconds()
		}
		stepAttrs := map[string]any{
			telemetry.AttrHecatePhase:        firstNonEmpty(step.Phase, "execution"),
			telemetry.AttrHecateResult:       firstNonEmpty(step.Result, telemetry.ResultSuccess),
			telemetry.AttrHecateTaskID:       task.ID,
			telemetry.AttrHecateRunID:        run.ID,
			telemetry.AttrHecateStepID:       step.ID,
			telemetry.AttrHecateStepKind:     step.Kind,
			telemetry.AttrHecateStepIndex:    step.Index,
			telemetry.AttrHecateStepToolName: step.ToolName,
		}
		if stepDurationMS > 0 {
			stepAttrs[telemetry.AttrHecateStepDurationMS] = stepDurationMS
		}
		mergeStepTelemetryAttrs(stepAttrs, step.Input)
		mergeStepTelemetryAttrs(stepAttrs, step.OutputSummary)
		trace.Record(eventName, stepAttrs)
		r.metrics.RecordStep(ctx, telemetry.StepMetricsRecord{
			TaskID:     task.ID,
			RunID:      run.ID,
			StepKind:   step.Kind,
			Result:     firstNonEmpty(step.Result, telemetry.ResultSuccess),
			DurationMS: stepDurationMS,
		})
		step.SpanID = spanIDByName(trace, "orchestrator.step")
		step.ParentSpanID = trace.RootSpanID()
		if err := r.upsertStep(ctx, step); err != nil {
			return nil, err
		}
		persistedSteps = append(persistedSteps, step)
	}

	persistedArtifacts := make([]types.TaskArtifact, 0, len(execution.Artifacts))
	for _, artifact := range execution.Artifacts {
		trace.Record(telemetry.EventOrchestratorArtifactCreated, map[string]any{
			telemetry.AttrHecatePhase:             "artifact",
			telemetry.AttrHecateResult:            telemetry.ResultSuccess,
			telemetry.AttrHecateTaskID:            task.ID,
			telemetry.AttrHecateRunID:             run.ID,
			telemetry.AttrHecateStepID:            artifact.StepID,
			telemetry.AttrHecateArtifactID:        artifact.ID,
			telemetry.AttrHecateArtifactKind:      artifact.Kind,
			telemetry.AttrHecateArtifactSizeBytes: artifact.SizeBytes,
		})
		artifact.SpanID = spanIDByName(trace, "orchestrator.artifact")
		if err := r.upsertArtifact(ctx, artifact); err != nil {
			return nil, err
		}
		persistedArtifacts = append(persistedArtifacts, artifact)
	}
	if artifact, ok := r.gitSummaryArtifact(ctx, task, run, requestID, trace.TraceID); ok {
		trace.Record(telemetry.EventOrchestratorArtifactCreated, map[string]any{
			telemetry.AttrHecatePhase:             "artifact",
			telemetry.AttrHecateResult:            telemetry.ResultSuccess,
			telemetry.AttrHecateTaskID:            task.ID,
			telemetry.AttrHecateRunID:             run.ID,
			telemetry.AttrHecateArtifactID:        artifact.ID,
			telemetry.AttrHecateArtifactKind:      artifact.Kind,
			telemetry.AttrHecateArtifactSizeBytes: artifact.SizeBytes,
		})
		artifact.SpanID = spanIDByName(trace, "orchestrator.artifact")
		if err := r.upsertArtifact(ctx, artifact); err != nil {
			return nil, err
		}
		persistedArtifacts = append(persistedArtifacts, artifact)
	}

	// Per-turn cost telemetry. The agent loop reports TurnCosts —
	// one entry per LLM round-trip — and we emit a `turn.completed`
	// event for each. Operators replay these via the events feed to
	// see how spend evolved across the run; the cumulative figure
	// includes prior runs in the resume chain so a long chain shows
	// total task spend, not just per-run.
	for _, tc := range execution.TurnCosts {
		_, _ = r.emitRunEvent(ctx, task.ID, run.ID, "turn.completed", requestID, trace.TraceID, runtimeevents.TurnCompleted(runtimeevents.TurnCompletedFields{
			TurnIndex:                   tc.Turn,
			StepID:                      tc.StepID,
			CostMicrosUSD:               tc.CostMicrosUSD,
			RunCumulativeCostMicrosUSD:  tc.CumulativeMicrosUSD,
			TaskCumulativeCostMicrosUSD: run.PriorCostMicrosUSD + tc.CumulativeMicrosUSD,
			ToolCalls:                   tc.ToolCallCount,
		}))
	}

	// Persist mid-loop approvals the executor emitted (agent_loop
	// pauses on gated tool calls). The runner owns the store
	// touch-points, so executors return the approvals via
	// ExecutionResult and we write them here. Skipped on non-paused
	// executions — PendingApprovals is empty.
	for _, approval := range execution.PendingApprovals {
		if approval.SpanID == "" {
			approval.SpanID = trace.RootSpanID()
		}
		if _, err := r.store.CreateApproval(ctx, approval); err != nil {
			return nil, err
		}
		_, _ = r.emitRunEvent(ctx, task.ID, run.ID, "approval.requested", requestID, trace.TraceID, runtimeevents.ApprovalRequested(approval))
	}

	resultKind := telemetry.ResultSuccess
	if execution.Status == "failed" || execution.Status == "cancelled" {
		resultKind = telemetry.ResultError
	}
	finishedAt := time.Now().UTC()
	var runDurationMS int64
	if !run.StartedAt.IsZero() {
		runDurationMS = finishedAt.Sub(run.StartedAt).Milliseconds()
	}
	runFinishedAttrs := map[string]any{
		telemetry.AttrHecatePhase:  "orchestration",
		telemetry.AttrHecateResult: resultKind,
		telemetry.AttrHecateTaskID: task.ID,
		telemetry.AttrHecateRunID:  run.ID,
	}
	if runDurationMS > 0 {
		runFinishedAttrs[telemetry.AttrHecateRunDurationMS] = runDurationMS
	}
	trace.Record(telemetry.EventOrchestratorRunFinished, runFinishedAttrs)
	trace.Record(telemetry.EventOrchestratorTaskFinished, map[string]any{
		telemetry.AttrHecatePhase:  "orchestration",
		telemetry.AttrHecateResult: resultKind,
		telemetry.AttrHecateTaskID: task.ID,
	})
	run.Provider = firstNonEmpty(execution.Provider, run.Provider)
	run.ProviderKind = firstNonEmpty(execution.ProviderKind, run.ProviderKind)
	run.Model = firstNonEmpty(execution.Model, run.Model)
	r.metrics.RecordRun(ctx, telemetry.RunMetricsRecord{
		TaskID:        task.ID,
		RunID:         run.ID,
		Status:        firstNonEmpty(execution.Status, "completed"),
		ExecutionKind: task.ExecutionKind,
		Model:         run.Model,
		DurationMS:    runDurationMS,
	})

	run.Status = firstNonEmpty(execution.Status, "completed")
	run.StepCount = len(persistedSteps)
	run.ArtifactCount = len(persistedArtifacts)
	run.FinishedAt = finishedAt
	run.LastError = execution.LastError
	run.OtelStatusCode = firstNonEmpty(execution.OtelStatusCode, "ok")
	run.OtelStatusMessage = execution.OtelStatusMessage
	if execution.CostMicrosUSD > 0 {
		// Agent loop accumulates per-turn LLM cost and surfaces the
		// total here. Other executors don't talk to the LLM and leave
		// CostMicrosUSD zero — preserving an existing TotalCostMicrosUSD
		// (e.g. set by an older execution kind not yet wired) rather
		// than overwriting it with zero.
		run.TotalCostMicrosUSD = execution.CostMicrosUSD
	}
	transition, err := r.applyTerminalRunTransition(ctx, terminalRunTransition{
		Task:                   task,
		Run:                    run,
		Status:                 run.Status,
		Message:                execution.LastError,
		RequestID:              requestID,
		TraceID:                trace.TraceID,
		Trace:                  trace,
		Now:                    finishedAt,
		SuppressDuplicateEvent: true,
		UpdateRun: func(target *types.TaskRun) {
			target.Provider = run.Provider
			target.ProviderKind = run.ProviderKind
			target.Model = run.Model
			target.StepCount = len(persistedSteps)
			target.ArtifactCount = len(persistedArtifacts)
			target.TotalCostMicrosUSD = run.TotalCostMicrosUSD
			target.OtelStatusCode = run.OtelStatusCode
			target.OtelStatusMessage = run.OtelStatusMessage
		},
		UpdateTask: func(target *types.Task) {
			target.RootTraceID = trace.TraceID
		},
	})
	if err != nil {
		return nil, err
	}
	task = transition.Task
	run = transition.Run

	return &StartTaskResult{
		Task:      task,
		Run:       run,
		Steps:     persistedSteps,
		Artifacts: persistedArtifacts,
		TraceID:   trace.TraceID,
		SpanID:    trace.RootSpanID(),
	}, nil
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
		return "run.finished"
	}
	return "run." + status
}

func recordOrchestratorRunStarted(trace *profiler.Trace, taskID string, run types.TaskRun) {
	if trace == nil {
		return
	}
	trace.Record(telemetry.EventOrchestratorRunStarted, map[string]any{
		telemetry.AttrHecatePhase:       "orchestration",
		telemetry.AttrHecateResult:      telemetry.ResultSuccess,
		telemetry.AttrHecateTaskID:      taskID,
		telemetry.AttrHecateRunID:       run.ID,
		telemetry.AttrHecateRunNumber:   run.Number,
		telemetry.AttrHecateRunStatus:   run.Status,
		telemetry.AttrGenAIRequestModel: run.Model,
	})
}

func recordOrchestratorRunFailed(trace *profiler.Trace, taskID, runID, errorKind string, err error) {
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
	if strings.TrimSpace(runID) != "" {
		attrs[telemetry.AttrHecateRunID] = runID
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
