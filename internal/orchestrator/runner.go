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
	"time"

	"github.com/hecate/agent-runtime/internal/profiler"
	"github.com/hecate/agent-runtime/internal/sandbox"
	"github.com/hecate/agent-runtime/internal/taskstate"
	"github.com/hecate/agent-runtime/internal/telemetry"
	"github.com/hecate/agent-runtime/pkg/types"
)

// ErrAgentLoopMisconfigured is returned by StartTask when an agent_loop
// task cannot be started due to missing configuration detectable before
// the run is created. Callers should surface this as a client error
// (HTTP 422) rather than a gateway error (500).
var ErrAgentLoopMisconfigured = errors.New("agent_loop misconfigured")

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
// gateway's `GATEWAY_TASK_HTTP_*` env knobs. Lives here (not in
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

// ShellNetworkPolicy is the projection of `GATEWAY_TASK_SHELL_*` env
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
	worker := sandbox.NewLocalExecutor()
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
	// An empty GATEWAY_TASK_APPROVAL_POLICIES is the documented "no gates"
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
// Network egress maps to http_request; read_file maps to read_file.
// all_tools short-circuits to the full set of every agent tool.
func agentLoopGatedTools(policies map[string]struct{}) []string {
	// all_tools gates every tool the agent can call — no need to enumerate.
	if _, ok := policies["all_tools"]; ok {
		return []string{"shell_exec", "git_exec", "file_write", "file_edit", "read_file", "list_dir", "http_request"}
	}
	out := make([]string, 0, len(policies))
	for p := range policies {
		switch p {
		case "shell_exec", "git_exec", "read_file":
			out = append(out, p)
		case "file_write":
			out = append(out, "file_write", "file_edit")
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

func (r *Runner) getQueue() RunQueue {
	r.queueMu.RLock()
	q := r.queue
	r.queueMu.RUnlock()
	return q
}

func (r *Runner) SetQueue(queue RunQueue) {
	if queue == nil {
		return
	}
	r.queueMu.Lock()
	r.queue = queue
	r.queueMu.Unlock()
}

func (r *Runner) ReconcilePendingRuns(ctx context.Context) error {
	if r.store == nil {
		return nil
	}
	runs, err := r.store.ListRunsByFilter(ctx, taskstate.RunFilter{
		Statuses: []string{"queued", "running"},
		Limit:    500,
	})
	if err != nil {
		return err
	}
	for _, run := range runs {
		task, found, err := r.store.GetTask(ctx, run.TaskID)
		if err != nil || !found {
			continue
		}
		priorStatus := run.Status
		now := time.Now().UTC()
		run.Status = "queued"
		run.LastError = ""
		run.FinishedAt = time.Time{}
		run.OtelStatusCode = ""
		run.OtelStatusMessage = ""
		if _, updateErr := r.store.UpdateRun(ctx, run); updateErr != nil {
			continue
		}
		task.Status = "queued"
		task.LatestRunID = run.ID
		task.LastError = ""
		task.UpdatedAt = now
		task.FinishedAt = time.Time{}
		_, _ = r.store.UpdateTask(ctx, task)
		_ = r.enqueueRun(task.ID, run.ID)
		_, _ = r.emitRunEvent(ctx, task.ID, run.ID, "gap.run_disconnected", "", "", map[string]any{
			"reason":            "boot_reconcile",
			"action":            "requeued",
			"prior_status":      priorStatus,
			"recovered_status":  "queued",
			"recovery_strategy": "requeue",
		})
	}
	return nil
}

// StartReconcileLoop starts a background goroutine that periodically scans
// for runs stuck in "running" state and re-enqueues them. It is distinct from
// the boot-time ReconcilePendingRuns: it only targets runs whose StartedAt is
// older than 3× the queue lease (i.e., the worker should have heartbeated or
// completed by now), so it does not race with legitimately in-flight workers.
//
// The loop is tied to the runner's worker context and stops automatically when
// Shutdown is called. It is safe to call once at startup after the boot-time
// reconcile.
func (r *Runner) StartReconcileLoop() {
	staleThreshold := r.queueLease * 3
	if staleThreshold <= 0 {
		staleThreshold = 90 * time.Second
	}
	r.workerWg.Add(1)
	go func() {
		defer r.workerWg.Done()
		ticker := time.NewTicker(r.reconcileInterval)
		defer ticker.Stop()
		for {
			select {
			case <-r.workerCtx.Done():
				return
			case <-ticker.C:
				if err := r.reconcileStaleRuns(r.workerCtx, staleThreshold); err != nil {
					r.logger.Warn("periodic task reconcile failed", slog.Any("error", err))
				}
			}
		}
	}()
}

// reconcileStaleRuns re-enqueues runs that are stuck in "running" state
// with a StartedAt older than staleThreshold. Unlike ReconcilePendingRuns
// (which is a boot-time sweep of all non-terminal runs), this targets only
// runs that an active worker should have completed or heartbeated by now.
func (r *Runner) reconcileStaleRuns(ctx context.Context, staleThreshold time.Duration) error {
	if r.store == nil {
		return nil
	}
	runs, err := r.store.ListRunsByFilter(ctx, taskstate.RunFilter{
		Statuses: []string{"running"},
		Limit:    500,
	})
	if err != nil {
		return err
	}
	cutoff := time.Now().UTC().Add(-staleThreshold)
	for _, run := range runs {
		if ctx.Err() != nil {
			return nil
		}
		if run.StartedAt.IsZero() || run.StartedAt.After(cutoff) {
			continue
		}
		task, found, err := r.store.GetTask(ctx, run.TaskID)
		if err != nil || !found {
			continue
		}
		priorStatus := run.Status
		now := time.Now().UTC()
		run.Status = "queued"
		run.LastError = ""
		run.FinishedAt = time.Time{}
		run.OtelStatusCode = ""
		run.OtelStatusMessage = ""
		if _, updateErr := r.store.UpdateRun(ctx, run); updateErr != nil {
			continue
		}
		task.Status = "queued"
		task.LatestRunID = run.ID
		task.LastError = ""
		task.UpdatedAt = now
		task.FinishedAt = time.Time{}
		_, _ = r.store.UpdateTask(ctx, task)
		_ = r.enqueueRun(task.ID, run.ID)
		_, _ = r.emitRunEvent(ctx, task.ID, run.ID, "gap.run_disconnected", "", "", map[string]any{
			"reason":             "worker_lease_expired",
			"action":             "requeued",
			"prior_status":       priorStatus,
			"recovered_status":   "queued",
			"recovery_strategy":  "periodic_requeue",
			"stale_threshold_ms": staleThreshold.Milliseconds(),
		})
	}
	return nil
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
		if firstNonEmpty(task.RequestedModel, r.config.DefaultModel) == "" {
			return nil, fmt.Errorf("%w: no model configured — set task.RequestedModel or GATEWAY_DEFAULT_MODEL", ErrAgentLoopMisconfigured)
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
		// Emit run.queued before Enqueue. The in-memory queue can dispatch
		// to a worker synchronously, so emitting after Enqueue risks the
		// worker writing run.started before run.queued is persisted.
		_, _ = r.emitRunEvent(ctx, task.ID, run.ID, "run.queued", requestID, trace.TraceID, nil)
		trace.Record(telemetry.EventQueueEnqueued, map[string]any{
			telemetry.AttrHecateTaskID:       task.ID,
			telemetry.AttrHecateRunID:        run.ID,
			telemetry.AttrHecateQueueBackend: r.getQueue().Backend(),
		})
		if err := r.enqueueRun(task.ID, run.ID); err != nil {
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

func (r *Runner) ResumeTaskAfterApproval(ctx context.Context, task types.Task, approval types.TaskApproval, idgen func(prefix string) string) (*StartTaskResult, error) {
	if r.store == nil {
		return nil, fmt.Errorf("task store is not configured")
	}
	if idgen == nil {
		return nil, fmt.Errorf("resource id generator is required")
	}
	run, found, err := r.store.GetRun(ctx, task.ID, approval.RunID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("task run %q not found", approval.RunID)
	}
	if run.Status != "awaiting_approval" {
		return nil, fmt.Errorf("task run %q is not awaiting approval", run.ID)
	}

	requestID := strings.TrimSpace(telemetry.RequestIDFromContext(ctx))
	if requestID == "" {
		requestID = idgen("request")
	}

	trace := r.tracer.Start(requestID)
	defer trace.Finalize()

	ctx = telemetry.WithTraceIDs(ctx, trace.TraceID, trace.RootSpanID())
	now := time.Now().UTC()
	approvalWaitMS := int64(0)
	if !approval.CreatedAt.IsZero() {
		resolvedAt := approval.ResolvedAt
		if resolvedAt.IsZero() {
			resolvedAt = time.Now().UTC()
		}
		approvalWaitMS = resolvedAt.Sub(approval.CreatedAt).Milliseconds()
	}
	approvalAttrs := map[string]any{
		telemetry.AttrHecatePhase:          "approval",
		telemetry.AttrHecateResult:         telemetry.ResultSuccess,
		telemetry.AttrHecateTaskID:         task.ID,
		telemetry.AttrHecateRunID:          run.ID,
		telemetry.AttrHecateApprovalID:     approval.ID,
		telemetry.AttrHecateApprovalKind:   approval.Kind,
		telemetry.AttrHecateApprovalStatus: approval.Status,
	}
	if approvalWaitMS > 0 {
		approvalAttrs[telemetry.AttrHecateApprovalWaitMS] = approvalWaitMS
	}
	trace.Record(telemetry.EventOrchestratorApprovalResolved, approvalAttrs)
	r.metrics.RecordApproval(ctx, telemetry.ApprovalMetricsRecord{
		TaskID:       task.ID,
		RunID:        run.ID,
		ApprovalKind: approval.Kind,
		Decision:     "approved",
		WaitMS:       approvalWaitMS,
	})

	run.Status = "queued"
	run.RequestID = requestID
	run.TraceID = trace.TraceID
	run.RootSpanID = trace.RootSpanID()
	run.LastError = ""
	run.FinishedAt = time.Time{}
	if _, err := r.store.UpdateRun(ctx, run); err != nil {
		return nil, err
	}
	_, _ = r.emitRunEvent(ctx, task.ID, run.ID, "run.queued", requestID, trace.TraceID, map[string]any{"resume": true})

	task.Status = "queued"
	task.LatestRunID = run.ID
	task.UpdatedAt = now
	task.FinishedAt = time.Time{}
	task.LastError = ""
	task.LatestTraceID = trace.TraceID
	task.LatestRequestID = requestID
	if _, err := r.store.UpdateTask(ctx, task); err != nil {
		return nil, err
	}

	if err := r.enqueueRun(task.ID, run.ID); err != nil {
		return nil, err
	}

	return &StartTaskResult{
		Task:    task,
		Run:     run,
		TraceID: trace.TraceID,
		SpanID:  trace.RootSpanID(),
	}, nil
}

func (r *Runner) RejectTaskAfterApproval(ctx context.Context, task types.Task, approval types.TaskApproval, idgen func(prefix string) string) (*StartTaskResult, error) {
	if r.store == nil {
		return nil, fmt.Errorf("task store is not configured")
	}
	if idgen == nil {
		return nil, fmt.Errorf("resource id generator is required")
	}
	run, found, err := r.store.GetRun(ctx, task.ID, approval.RunID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("task run %q not found", approval.RunID)
	}
	if run.Status != "awaiting_approval" {
		return nil, fmt.Errorf("task run %q is not awaiting approval", run.ID)
	}

	requestID := strings.TrimSpace(telemetry.RequestIDFromContext(ctx))
	if requestID == "" {
		requestID = firstNonEmpty(approval.RequestID, run.RequestID)
	}
	if requestID == "" {
		requestID = idgen("request")
	}

	trace := r.tracer.Start(requestID)
	defer trace.Finalize()

	ctx = telemetry.WithTraceIDs(ctx, trace.TraceID, trace.RootSpanID())
	rejectWaitMS := int64(0)
	if !approval.CreatedAt.IsZero() {
		resolvedAt := approval.ResolvedAt
		if resolvedAt.IsZero() {
			resolvedAt = time.Now().UTC()
		}
		rejectWaitMS = resolvedAt.Sub(approval.CreatedAt).Milliseconds()
	}
	rejectApprovalAttrs := map[string]any{
		telemetry.AttrHecatePhase:          "approval",
		telemetry.AttrHecateResult:         telemetry.ResultSuccess,
		telemetry.AttrHecateTaskID:         task.ID,
		telemetry.AttrHecateRunID:          run.ID,
		telemetry.AttrHecateApprovalID:     approval.ID,
		telemetry.AttrHecateApprovalKind:   approval.Kind,
		telemetry.AttrHecateApprovalStatus: approval.Status,
	}
	if rejectWaitMS > 0 {
		rejectApprovalAttrs[telemetry.AttrHecateApprovalWaitMS] = rejectWaitMS
	}
	trace.Record(telemetry.EventOrchestratorApprovalResolved, rejectApprovalAttrs)
	r.metrics.RecordApproval(ctx, telemetry.ApprovalMetricsRecord{
		TaskID:       task.ID,
		RunID:        run.ID,
		ApprovalKind: approval.Kind,
		Decision:     "rejected",
		WaitMS:       rejectWaitMS,
	})

	run, err = r.cancelRunWithMessage(ctx, task, run, "approval rejected", requestID, trace.TraceID)
	if err != nil {
		return nil, err
	}
	task, _, err = r.store.GetTask(ctx, task.ID)
	if err != nil {
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
	r.jobMu.Lock()
	cancel, ok := r.jobs[run.ID]
	r.jobMu.Unlock()
	if ok {
		cancel()
	}

	now := time.Now().UTC()
	run.Status = "cancelled"
	run.LastError = message
	run.FinishedAt = now
	run.OtelStatusCode = "error"
	run.OtelStatusMessage = message
	if requestID != "" {
		run.RequestID = requestID
	}
	if traceID != "" {
		run.TraceID = traceID
	}
	var err error
	run, err = r.store.UpdateRun(ctx, run)
	if err != nil {
		return types.TaskRun{}, err
	}
	_, _ = r.emitRunEvent(ctx, task.ID, run.ID, "run.cancelled", requestID, traceID, map[string]any{"reason": message})

	steps, _ := r.store.ListSteps(ctx, run.ID)
	for _, step := range steps {
		if step.Status == "running" {
			step.Status = "cancelled"
			step.Result = telemetry.ResultError
			step.Error = message
			step.ErrorKind = "run_cancelled"
			step.FinishedAt = now
			_, _ = r.store.UpdateStep(ctx, step)
		}
	}

	artifacts, _ := r.store.ListArtifacts(ctx, taskstate.ArtifactFilter{TaskID: task.ID, RunID: run.ID})
	for _, artifact := range artifacts {
		if artifact.Status == "streaming" {
			artifact.Status = "cancelled"
			_, _ = r.store.UpdateArtifact(ctx, artifact)
		}
	}

	task.Status = "cancelled"
	task.LatestRunID = run.ID
	task.LastError = message
	if task.StartedAt.IsZero() {
		task.StartedAt = run.StartedAt
	}
	task.FinishedAt = now
	task.UpdatedAt = now
	if requestID != "" {
		task.LatestRequestID = requestID
	}
	if traceID != "" {
		task.LatestTraceID = traceID
	}
	if _, err := r.store.UpdateTask(ctx, task); err != nil {
		return types.TaskRun{}, err
	}
	_, _ = r.emitRunEvent(ctx, task.ID, run.ID, "task.updated", requestID, traceID, nil)
	return run, nil
}

func (r *Runner) processQueue() {
	defer r.workerWg.Done()
	for {
		// Fast-exit when shutdown has fired. The two select-on-ctx
		// blocks below also catch this, but checking up-front keeps a
		// freshly-cancelled worker from issuing one last Claim against
		// the store on its way out.
		if r.workerCtx.Err() != nil {
			return
		}
		q := r.getQueue()
		if q == nil {
			// No queue wired (transient during boot). Bounded sleep
			// instead of a hot loop, but unblock immediately on
			// shutdown so the goroutine returns inside the deadline.
			select {
			case <-time.After(200 * time.Millisecond):
			case <-r.workerCtx.Done():
				return
			}
			continue
		}
		claim, ok, err := q.Claim(r.workerCtx, r.workerID, 2*time.Second)
		if err != nil {
			// Claim failure may be transient (lock contention, brief
			// store hiccup) OR shutdown — distinguish so a real error
			// gets a brief backoff while a cancelled context exits.
			if r.workerCtx.Err() != nil {
				return
			}
			select {
			case <-time.After(150 * time.Millisecond):
			case <-r.workerCtx.Done():
				return
			}
			continue
		}
		if !ok {
			continue
		}
		r.processQueuedRun(claim)
	}
}

func (r *Runner) processQueuedRun(claim QueueClaim) {
	q := r.getQueue()
	task, found, err := r.store.GetTask(context.Background(), claim.Job.TaskID)
	if err != nil || !found {
		_ = q.Ack(context.Background(), claim.ClaimID)
		return
	}
	run, found, err := r.store.GetRun(context.Background(), claim.Job.TaskID, claim.Job.RunID)
	if err != nil || !found {
		_ = q.Ack(context.Background(), claim.ClaimID)
		return
	}
	if run.Status != "queued" {
		_ = q.Ack(context.Background(), claim.ClaimID)
		return
	}
	requestID := strings.TrimSpace(run.RequestID)
	if requestID == "" {
		requestID = defaultResourceID("request")
	}
	trace := r.tracer.Start(requestID)
	defer trace.Finalize()

	// Parent jobCtx off the runner's worker context so Shutdown
	// cascades cancellation into the agent loop, which in turn closes
	// its MCP host (via the existing defer host.Close chain) — that's
	// what stops orphaned subprocesses on gateway exit. workerWg counts
	// this job so Shutdown's drain wait covers it as well as the
	// claiming goroutine itself.
	jobCtx, jobCancel := context.WithCancel(r.workerCtx)
	r.workerWg.Add(1)
	defer r.workerWg.Done()
	r.registerJob(run.ID, jobCancel)
	defer r.unregisterJob(run.ID)
	defer jobCancel()

	stopHeartbeat := make(chan struct{})
	go r.heartbeatClaim(claim.ClaimID, stopHeartbeat)
	defer close(stopHeartbeat)

	ctx := telemetry.WithTraceIDs(jobCtx, trace.TraceID, trace.RootSpanID())
	now := time.Now().UTC()

	// Compute queue wait before overwriting run.StartedAt.
	var queueWaitMS int64
	if !run.StartedAt.IsZero() {
		queueWaitMS = now.Sub(run.StartedAt).Milliseconds()
	}
	queueBackend := ""
	if q != nil {
		queueBackend = q.Backend()
	}
	trace.Record(telemetry.EventQueueClaimed, map[string]any{
		telemetry.AttrHecateTaskID:       task.ID,
		telemetry.AttrHecateRunID:        run.ID,
		telemetry.AttrHecateQueueBackend: queueBackend,
		telemetry.AttrHecateQueueWaitMS:  queueWaitMS,
		telemetry.AttrHecateWorkerID:     r.workerID,
	})
	r.metrics.RecordQueueWait(ctx, telemetry.QueueWaitRecord{
		TaskID:       task.ID,
		RunID:        run.ID,
		QueueBackend: queueBackend,
		WaitMS:       queueWaitMS,
	})

	run.Status = "running"
	run.RequestID = requestID
	run.TraceID = trace.TraceID
	run.RootSpanID = trace.RootSpanID()
	if run.StartedAt.IsZero() {
		run.StartedAt = now
	}
	run.LastError = ""
	run.FinishedAt = time.Time{}
	if _, err := r.store.UpdateRun(ctx, run); err != nil {
		return
	}
	task.Status = "running"
	task.LatestRunID = run.ID
	if task.StartedAt.IsZero() {
		task.StartedAt = now
	}
	task.UpdatedAt = now
	task.FinishedAt = time.Time{}
	task.LastError = ""
	task.RootTraceID = trace.TraceID
	task.LatestTraceID = trace.TraceID
	task.LatestRequestID = requestID
	if _, err := r.store.UpdateTask(ctx, task); err != nil {
		return
	}

	recordOrchestratorRunStarted(trace, task.ID, run)

	resumeCheckpoint, checkpointErr := r.resumeCheckpointForRun(ctx, task.ID, run.ID)
	if checkpointErr != nil {
		_, _ = r.emitRunEvent(ctx, task.ID, run.ID, "gap.run_disconnected", requestID, trace.TraceID, map[string]any{
			"reason":  "resume_checkpoint_unavailable",
			"action":  "start_fresh",
			"message": checkpointErr.Error(),
		})
	}
	runEvent := map[string]any{}
	if resumeCheckpoint != nil {
		runEvent["resume_from_run_id"] = resumeCheckpoint.SourceRunID
		runEvent["resume_from_step_id"] = resumeCheckpoint.LastCompletedStepID
		runEvent["resume_from_event_sequence"] = resumeCheckpoint.LastEventSequence
	}
	_, _ = r.emitRunEvent(ctx, task.ID, run.ID, "run.started", requestID, trace.TraceID, runEvent)

	if _, err := r.executeRun(ctx, trace, task, run, requestID, resumeCheckpoint); err != nil {
		finalStatus := "failed"
		lastError := err.Error()
		if jobCtx.Err() != nil {
			finalStatus = "cancelled"
			lastError = "run cancelled"
		}
		_ = r.finalizeFailedRun(ctx, trace, task, run, requestID, finalStatus, lastError)
	}
	trace.Record(telemetry.EventQueueAcked, map[string]any{
		telemetry.AttrHecateTaskID:       task.ID,
		telemetry.AttrHecateRunID:        run.ID,
		telemetry.AttrHecateQueueBackend: queueBackend,
	})
	_ = q.Ack(context.Background(), claim.ClaimID)
}

func (r *Runner) enqueueRun(taskID, runID string) error {
	q := r.getQueue()
	if q == nil {
		return fmt.Errorf("run queue is not configured")
	}
	return q.Enqueue(context.Background(), QueueJob{TaskID: taskID, RunID: runID})
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
func (r *Runner) Shutdown(ctx context.Context) error {
	r.shutdownOnce.Do(func() {
		// Cancel the worker lifetime context first — this stops new
		// queue claims and (because every jobCtx is parented from it)
		// cascades cancellation into running agent loops.
		r.workerCancel()
		// Belt-and-braces: also fire each registered job's cancel
		// directly, in case any future code path detaches a jobCtx
		// from workerCtx. Iterating r.jobs under jobMu is what the
		// existing CancelRun path does.
		r.jobMu.Lock()
		for _, cancel := range r.jobs {
			cancel()
		}
		r.jobMu.Unlock()
	})
	// Wait for all worker goroutines AND in-flight jobs to finish,
	// or for the caller's deadline to expire.
	done := make(chan struct{})
	go func() {
		r.workerWg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Runner) registerJob(runID string, cancel context.CancelFunc) {
	r.jobMu.Lock()
	defer r.jobMu.Unlock()
	r.jobs[runID] = cancel
}

func (r *Runner) unregisterJob(runID string) {
	r.jobMu.Lock()
	defer r.jobMu.Unlock()
	delete(r.jobs, runID)
}

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
		EmitRunEvent: func(eventType string, data map[string]any) {
			_, _ = r.emitRunEvent(ctx, task.ID, run.ID, eventType, requestID, trace.TraceID, data)
		},
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

	// Per-turn cost telemetry. The agent loop reports TurnCosts —
	// one entry per LLM round-trip — and we emit a `turn.completed`
	// event for each. Operators replay these via the events feed to
	// see how spend evolved across the run; the cumulative figure
	// includes prior runs in the resume chain so a long chain shows
	// total task spend, not just per-run.
	for _, tc := range execution.TurnCosts {
		_, _ = r.emitRunEvent(ctx, task.ID, run.ID, "turn.completed", requestID, trace.TraceID, map[string]any{
			"turn_index":                      tc.Turn,
			"step_id":                         tc.StepID,
			"cost_micros_usd":                 tc.CostMicrosUSD,
			"run_cumulative_cost_micros_usd":  tc.CumulativeMicrosUSD,
			"task_cumulative_cost_micros_usd": run.PriorCostMicrosUSD + tc.CumulativeMicrosUSD,
			"tool_calls":                      tc.ToolCallCount,
		})
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
		_, _ = r.emitRunEvent(ctx, task.ID, run.ID, "approval.requested", requestID, trace.TraceID, approvalRequestedEventData(approval))
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
	emitTerminalEvent := true
	if currentRun, found, err := r.store.GetRun(ctx, task.ID, run.ID); err == nil && found {
		// CancelRun persists the terminal cancelled state and emits the
		// authoritative run.cancelled event while the worker may still be
		// unwinding. Keep persisting the latest run/task snapshots here, but
		// don't emit a duplicate terminal event if the same status is already
		// stored.
		if types.IsTerminalTaskRunStatus(currentRun.Status) && currentRun.Status == run.Status {
			emitTerminalEvent = false
		}
	}
	if _, err := r.store.UpdateRun(ctx, run); err != nil {
		return nil, err
	}

	task.LatestRunID = run.ID
	task.Status = run.Status
	if task.StartedAt.IsZero() {
		task.StartedAt = run.StartedAt
	}
	task.FinishedAt = finishedAt
	task.UpdatedAt = finishedAt
	task.RootTraceID = trace.TraceID
	task.LatestTraceID = trace.TraceID
	task.LatestRequestID = requestID
	task.LastError = execution.LastError
	if _, err := r.store.UpdateTask(ctx, task); err != nil {
		return nil, err
	}
	if emitTerminalEvent {
		_, _ = r.emitRunEvent(ctx, task.ID, run.ID, terminalRunEventType(run.Status), requestID, trace.TraceID, map[string]any{
			"error":  run.LastError,
			"status": run.Status,
		})
	}

	return &StartTaskResult{
		Task:      task,
		Run:       run,
		Steps:     persistedSteps,
		Artifacts: persistedArtifacts,
		TraceID:   trace.TraceID,
		SpanID:    trace.RootSpanID(),
	}, nil
}

func (r *Runner) finalizeFailedRun(ctx context.Context, trace *profiler.Trace, task types.Task, run types.TaskRun, requestID, status, message string) error {
	if currentRun, found, err := r.store.GetRun(ctx, task.ID, run.ID); err == nil && found {
		// Cancellation can arrive through the HTTP API while the worker is
		// still unwinding a cancelled executor. In that case CancelRun has
		// already persisted the terminal run/task state and emitted the
		// authoritative run.cancelled event; re-emitting it here creates
		// duplicate terminal events under racey shutdown/cancel timing.
		if types.IsTerminalTaskRunStatus(currentRun.Status) && currentRun.Status == status {
			return nil
		}
	}
	now := time.Now().UTC()
	var failedRunDurationMS int64
	if !run.StartedAt.IsZero() {
		failedRunDurationMS = now.Sub(run.StartedAt).Milliseconds()
	}
	run.Status = status
	run.LastError = message
	run.FinishedAt = now
	run.OtelStatusCode = "error"
	run.OtelStatusMessage = message
	if _, err := r.store.UpdateRun(ctx, run); err != nil {
		return err
	}
	r.metrics.RecordRun(ctx, telemetry.RunMetricsRecord{
		TaskID:        task.ID,
		RunID:         run.ID,
		Status:        status,
		ExecutionKind: task.ExecutionKind,
		Model:         run.Model,
		DurationMS:    failedRunDurationMS,
	})
	_, _ = r.emitRunEvent(ctx, task.ID, run.ID, terminalRunEventType(status), requestID, trace.TraceID, map[string]any{"error": message, "status": status})
	task.Status = status
	task.LatestRunID = run.ID
	task.LastError = message
	task.FinishedAt = now
	task.UpdatedAt = now
	task.LatestTraceID = trace.TraceID
	task.LatestRequestID = requestID
	_, err := r.store.UpdateTask(ctx, task)
	return err
}

func (r *Runner) upsertStep(ctx context.Context, step types.TaskStep) error {
	if existing, found, err := r.store.GetStep(ctx, step.RunID, step.ID); err != nil {
		return err
	} else if found {
		step.SpanID = firstNonEmpty(step.SpanID, existing.SpanID)
		step.ParentSpanID = firstNonEmpty(step.ParentSpanID, existing.ParentSpanID)
		_, err = r.store.UpdateStep(ctx, step)
		return err
	}
	_, err := r.store.AppendStep(ctx, step)
	return err
}

func (r *Runner) upsertArtifact(ctx context.Context, artifact types.TaskArtifact) error {
	if existing, found, err := r.store.GetArtifact(ctx, artifact.TaskID, artifact.ID); err != nil {
		return err
	} else if found {
		artifact.SpanID = firstNonEmpty(artifact.SpanID, existing.SpanID)
		_, err = r.store.UpdateArtifact(ctx, artifact)
		return err
	}
	_, err := r.store.CreateArtifact(ctx, artifact)
	return err
}

func taskForRun(task types.Task, run types.TaskRun) types.Task {
	executionTask := task
	if strings.TrimSpace(run.WorkspacePath) != "" {
		executionTask.WorkingDirectory = run.WorkspacePath
		executionTask.SandboxAllowedRoot = run.WorkspacePath
	}
	return executionTask
}

func (r *Runner) resumeCheckpointForRun(ctx context.Context, taskID, runID string) (*ResumeCheckpoint, error) {
	if r.store == nil {
		return nil, nil
	}
	events, err := r.store.ListRunEvents(ctx, taskID, runID, 0, 500)
	if err != nil {
		return nil, err
	}
	sourceRunID := ""
	reason := ""
	retryFromTurn := 0
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if event.EventType != "run.resumed_from_event" {
			continue
		}
		value, ok := event.Data["resumed_from_run_id"]
		if !ok {
			continue
		}
		candidate, _ := value.(string)
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		sourceRunID = candidate
		if rawReason, ok := event.Data["reason"]; ok {
			reason, _ = rawReason.(string)
		}
		// retry_from_turn is event-data JSON-decoded — depending on
		// the store it may come back as float64 (JSON-roundtripped)
		// or int. Accept both. Zero/missing means a regular resume,
		// not a retry-from-turn.
		if raw, ok := event.Data["retry_from_turn"]; ok {
			switch v := raw.(type) {
			case int:
				retryFromTurn = v
			case int64:
				retryFromTurn = int(v)
			case float64:
				retryFromTurn = int(v)
			}
		}
		break
	}
	// If no separate source run, the caller might still be resuming
	// the SAME run after a mid-loop approval pause. The agent loop
	// persists conversation as an artifact on its own run; pull that
	// into a checkpoint so the loop can hydrate state and pick up
	// from the trailing tool_calls.
	if sourceRunID == "" {
		ownArtifacts, err := r.store.ListArtifacts(ctx, taskstate.ArtifactFilter{TaskID: taskID, RunID: runID})
		if err != nil {
			return nil, err
		}
		for _, art := range ownArtifacts {
			if art.Kind == "agent_conversation" && len(art.ContentText) > 0 {
				cp := &ResumeCheckpoint{
					SourceRunID:       runID,
					Reason:            "approved_mid_loop",
					AgentConversation: []byte(art.ContentText),
				}
				// Same-run mid-approval resume: surface BOTH the
				// chain-prior cost (so the ceiling holds across the
				// task lifecycle) AND this run's pre-pause spend
				// (so the loop seeds costSpent with it instead of
				// resetting to 0). Without ThisRunCostMicrosUSD the
				// pre-pause LLM spend would be lost when the runner
				// overwrites Total on finalization.
				if currentRun, found, err := r.store.GetRun(ctx, taskID, runID); err == nil && found {
					cp.PriorCostMicrosUSD = currentRun.PriorCostMicrosUSD
					cp.ThisRunCostMicrosUSD = currentRun.TotalCostMicrosUSD
				}
				return cp, nil
			}
		}
		return nil, nil
	}
	steps, err := r.store.ListSteps(ctx, sourceRunID)
	if err != nil {
		return nil, err
	}
	artifacts, err := r.store.ListArtifacts(ctx, taskstate.ArtifactFilter{TaskID: taskID, RunID: sourceRunID})
	if err != nil {
		return nil, err
	}
	sourceEvents, err := r.store.ListRunEvents(ctx, taskID, sourceRunID, 0, 5000)
	if err != nil {
		return nil, err
	}
	checkpoint := &ResumeCheckpoint{
		SourceRunID:   sourceRunID,
		Reason:        strings.TrimSpace(reason),
		LastStepIndex: 0,
		ArtifactCount: len(artifacts),
		RetryFromTurn: retryFromTurn,
	}
	// Surface the new run's PriorCostMicrosUSD so the agent loop can
	// apply the per-task cost ceiling against the cumulative spend
	// across the entire resume chain. We populated this on the run
	// at create time (startTaskWithOptions) by summing the source's
	// prior + total. Re-reading from the store here keeps that
	// value the single source of truth. ThisRunCostMicrosUSD is
	// always 0 for a cross-run resume (the new run hasn't run yet).
	if currentRun, found, lookupErr := r.store.GetRun(ctx, taskID, runID); lookupErr == nil && found {
		checkpoint.PriorCostMicrosUSD = currentRun.PriorCostMicrosUSD
		checkpoint.ThisRunCostMicrosUSD = currentRun.TotalCostMicrosUSD
	}
	// Pull the agent-conversation artifact (if any) so the agent loop
	// can hydrate state on resume. We use a stable kind + ID
	// convention so the lookup is a single linear scan rather than a
	// new store method. Non-agent_loop runs simply won't have one.
	for _, art := range artifacts {
		if art.Kind == "agent_conversation" && len(art.ContentText) > 0 {
			checkpoint.AgentConversation = []byte(art.ContentText)
			break
		}
	}
	var lastSequence int64
	maxCompletedIndex := 0
	for _, event := range sourceEvents {
		if event.Sequence > lastSequence {
			lastSequence = event.Sequence
		}
	}
	checkpoint.LastEventSequence = lastSequence
	for _, step := range steps {
		if step.Index > checkpoint.LastStepIndex {
			checkpoint.LastStepIndex = step.Index
		}
		if step.Status == "completed" {
			checkpoint.CompletedStepCount++
			if checkpoint.LastCompletedStepID == "" || step.Index >= maxCompletedIndex {
				maxCompletedIndex = step.Index
				checkpoint.LastCompletedStepID = step.ID
			}
		}
	}
	// Retry-from-turn-N: truncate the saved conversation to right
	// before turn N's assistant message and reset step-index
	// continuity. The new run's step indices start at 1 instead of
	// continuing the source's count — semantically this is a fresh
	// run that happens to share prior conversation context, not a
	// continuation.
	if retryFromTurn > 0 && len(checkpoint.AgentConversation) > 0 {
		var saved []types.Message
		if err := json.Unmarshal(checkpoint.AgentConversation, &saved); err != nil {
			return nil, fmt.Errorf("decode source conversation for retry: %w", err)
		}
		truncated, err := truncateConversationToTurn(saved, retryFromTurn)
		if err != nil {
			return nil, fmt.Errorf("retry-from-turn truncation: %w", err)
		}
		payload, err := json.Marshal(truncated)
		if err != nil {
			return nil, fmt.Errorf("encode truncated conversation: %w", err)
		}
		checkpoint.AgentConversation = payload
		checkpoint.LastStepIndex = 0
		checkpoint.LastCompletedStepID = ""
		checkpoint.CompletedStepCount = 0
	}
	return checkpoint, nil
}

func (r *Runner) approvalRequiredForTask(task types.Task) bool {
	_, reason := r.approvalSpecForTask(task)
	return reason != ""
}

func (r *Runner) approvalSpecForTask(task types.Task) (kind string, reason string) {
	if task.ExecutionKind == "shell" && strings.TrimSpace(task.ShellCommand) != "" {
		if r.hasPolicy("shell_exec") || r.hasPolicy("all_tools") {
			return "shell_command", "Shell execution requires approval before execution."
		}
	}
	if task.ExecutionKind == "git" && strings.TrimSpace(task.GitCommand) != "" {
		if r.hasPolicy("git_exec") || r.hasPolicy("all_tools") {
			return "git_exec", "Git execution requires approval before execution."
		}
	}
	if task.ExecutionKind == "file" && strings.TrimSpace(task.FilePath) != "" {
		if r.hasPolicy("file_write") || r.hasPolicy("all_tools") {
			return "file_write", "File writes require approval before execution."
		}
	}
	if task.SandboxNetwork {
		if r.hasPolicy("network_egress") || r.hasPolicy("all_tools") {
			return "network_egress", "Network-enabled tasks require approval before execution."
		}
	}
	return "", ""
}

func (r *Runner) createApprovalForTask(ctx context.Context, trace *profiler.Trace, task types.Task, run types.TaskRun, requestID string, createdAt time.Time, idgen func(prefix string) string) (types.TaskApproval, error) {
	kind, reason := r.approvalSpecForTask(task)
	approval := types.TaskApproval{
		ID:          idgen("approval"),
		TaskID:      task.ID,
		RunID:       run.ID,
		Kind:        kind,
		Status:      "pending",
		Reason:      reason,
		RequestedBy: "operator",
		CreatedAt:   createdAt,
		RequestID:   requestID,
		TraceID:     trace.TraceID,
	}
	trace.Record(telemetry.EventOrchestratorApprovalRequested, map[string]any{
		telemetry.AttrHecatePhase:        "approval",
		telemetry.AttrHecateResult:       telemetry.ResultSuccess,
		telemetry.AttrHecateTaskID:       task.ID,
		telemetry.AttrHecateRunID:        run.ID,
		telemetry.AttrHecateApprovalID:   approval.ID,
		telemetry.AttrHecateApprovalKind: approval.Kind,
		telemetry.AttrHecateShellCommand: task.ShellCommand,
	})
	approval.SpanID = spanIDByName(trace, "orchestrator.approval")
	approval, err := r.store.CreateApproval(ctx, approval)
	if err != nil {
		trace.Record(telemetry.EventOrchestratorApprovalFailed, map[string]any{
			telemetry.AttrHecatePhase:      "approval",
			telemetry.AttrHecateResult:     telemetry.ResultError,
			telemetry.AttrHecateErrorKind:  "approval_create_failed",
			telemetry.AttrErrorType:        "approval_create_failed",
			telemetry.AttrErrorMessage:     err.Error(),
			telemetry.AttrHecateTaskID:     task.ID,
			telemetry.AttrHecateRunID:      run.ID,
			telemetry.AttrHecateApprovalID: approval.ID,
		})
		return types.TaskApproval{}, err
	}
	_, _ = r.emitRunEvent(ctx, task.ID, run.ID, "approval.requested", requestID, trace.TraceID, approvalRequestedEventData(approval))
	return approval, nil
}

func approvalRequestedEventData(approval types.TaskApproval) map[string]any {
	data := map[string]any{
		"approval_id":   approval.ID,
		"kind":          approval.Kind,
		"status":        approval.Status,
		"policy_reason": approval.Reason,
	}
	if approval.StepID != "" {
		data["step_id"] = approval.StepID
	}
	if approval.RequestedBy != "" {
		data["requested_by"] = approval.RequestedBy
	}
	return data
}

func (r *Runner) emitRunEvent(ctx context.Context, taskID, runID, eventType, requestID, traceID string, extra map[string]any) (types.TaskRunEvent, error) {
	if r.store == nil || runID == "" {
		return types.TaskRunEvent{}, nil
	}
	run, _, err := r.store.GetRun(ctx, taskID, runID)
	if err != nil {
		return types.TaskRunEvent{}, err
	}
	steps, _ := r.store.ListSteps(ctx, runID)
	artifacts, _ := r.store.ListArtifacts(ctx, taskstate.ArtifactFilter{TaskID: taskID, RunID: runID})
	data := map[string]any{
		"run":       run,
		"steps":     steps,
		"artifacts": artifacts,
	}
	for key, value := range extra {
		data[key] = value
	}
	return r.store.AppendRunEvent(ctx, types.TaskRunEvent{
		TaskID:    taskID,
		RunID:     runID,
		EventType: eventType,
		Data:      data,
		RequestID: requestID,
		TraceID:   traceID,
		CreatedAt: time.Now().UTC(),
	})
}

func (r *Runner) heartbeatClaim(claimID string, stop <-chan struct{}) {
	if r.getQueue() == nil || claimID == "" {
		return
	}
	interval := r.queueLease / 2
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if q := r.getQueue(); q != nil {
				if err := q.ExtendLease(context.Background(), claimID, r.queueLease); err != nil {
					r.metrics.RecordLeaseExtendFailed(context.Background())
				}
			}
		}
	}
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
	return prefix + "_" + strconv.FormatInt(time.Now().UTC().UnixNano(), 36)
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

func (r *Runner) executorForTask(task types.Task) Executor {
	if task.ExecutionKind == "agent_loop" && r.agent != nil {
		return r.agent
	}
	if task.ExecutionKind == "shell" && strings.TrimSpace(task.ShellCommand) != "" && r.shell != nil {
		return r.shell
	}
	if task.ExecutionKind == "file" && strings.TrimSpace(task.FilePath) != "" && r.file != nil {
		return r.file
	}
	if task.ExecutionKind == "git" && strings.TrimSpace(task.GitCommand) != "" && r.git != nil {
		return r.git
	}
	return r.exec
}
