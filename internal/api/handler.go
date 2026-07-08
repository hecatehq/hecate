package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/controlplane"
	"github.com/hecatehq/hecate/internal/gateway"
	mcpclient "github.com/hecatehq/hecate/internal/mcp/client"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/modelapp"
	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/internal/pluginregistry"
	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/projectassistant"
	"github.com/hecatehq/hecate/internal/projectassistantapp"
	"github.com/hecatehq/hecate/internal/projectruntime"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/ratelimit"
	"github.com/hecatehq/hecate/internal/remoteruntime"
	"github.com/hecatehq/hecate/internal/sandbox"
	"github.com/hecatehq/hecate/internal/secrets"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/internal/terminalapp"
	"github.com/hecatehq/hecate/internal/version"
	"github.com/hecatehq/hecate/internal/websearch"
	"github.com/hecatehq/hecate/pkg/types"
)

type Handler struct {
	config                    config.Config
	logger                    *slog.Logger
	service                   *gateway.Service
	controlPlane              controlplane.Store
	providerRuntime           ProviderRuntime
	taskStore                 taskstate.Store
	taskRunner                *orchestrator.Runner
	tracer                    profiler.Tracer
	agentChat                 chat.Store
	projects                  projects.Store
	memory                    memory.Store
	memoryCandidates          memory.CandidateStore
	projectWork               projectwork.Store
	projectRuntime            projectruntime.Store
	projectSkills             projectskills.Store
	projectAssistantProposals projectassistant.ProposalStore
	pluginRegistry            pluginregistry.Store
	projectAssistantMu        sync.Mutex
	projectAssistant          *projectassistantapp.Application
	agentProfiles             agentprofiles.Store
	agentChatRunner           agentadapters.Runner
	agentChatLive             *agentChatLive
	agentChatIdleSweepCancel  context.CancelFunc
	operatorTerminals         *terminalapp.Application
	rateLimiter               *ratelimit.Store
	// secretCipher encrypts literal MCP server env values at task-creation
	// time and wires the matching decrypting factory into the runner. nil
	// when no settings key is configured — values are stored as-is
	// and $VAR_NAME references are the only safe option in that case.
	secretCipher secrets.Cipher
	// mcpClientCache amortizes MCP subprocess spawn cost across runs.
	// When set, the runner's host factory acquires per-server clients
	// from the cache (sharing subprocesses between tasks with the same
	// upstream config); when nil, every run spawns and tears down its
	// own subprocesses. Owned by the handler — Shutdown closes it
	// after the runner has drained.
	mcpClientCache *mcpclient.SharedClientCache
	// projectCairnlineSidecarCache is separate from the task MCP
	// cache because a future Cairnline connector is Hecate operator
	// infrastructure, not task-scoped model/tool context. Keeping the
	// caches distinct prevents a project-coordination sidecar from
	// sharing lifecycle or metrics assumptions with arbitrary task MCP
	// servers.
	projectCairnlineSidecarMu    sync.Mutex
	projectCairnlineSidecarCache *mcpclient.SharedClientCache
	// cairnlineMirrorHealth tracks shadow-mirror write outcomes per portable
	// write family so backend-status and mirror-parity surface failures
	// instead of leaving them as log-only warnings. In-memory on purpose:
	// this is runtime observability for the current process, not persisted
	// coordination state.
	cairnlineMirrorHealth cairnlineMirrorHealth
	// orchestratorMetrics is shared between the runner and the MCP
	// client cache observer. Built once in NewHandler so a second
	// NewOrchestratorMetrics() can't register duplicate instruments;
	// exposed via OrchestratorMetrics() so main.go can plumb the
	// same instance into the cache.
	orchestratorMetrics *telemetry.OrchestratorMetrics
	agentChatMetrics    *telemetry.AgentChatMetrics
	// approvalConfig retains the inputs needed to rebuild the
	// ApprovalCoordinator if SetAgentApprovalStore replaces its
	// backing store after NewHandler returned.
	approvalConfig approvalConfig
	// agentAdapterProbe is the override hook used by
	// HandleAgentAdapterHealth. nil means production callers fall
	// through to agentadapters.Probe; tests install a fake via
	// SetAgentAdapterProbe so they can exercise the handler without
	// spawning real ACP binaries.
	agentAdapterProbe        AgentAdapterProbe
	agentAdapterLogout       AgentAdapterLogout
	agentAdapterAuthenticate AgentAdapterAuthenticate
	stateCleaner             StateCleaner
	// quitFunc is wired by main.go to request an orderly process
	// shutdown — used by HandleSystemShutdown when the desktop app's
	// close-window confirmation flow asks the gateway to quit. nil in
	// tests and when no quit signal is wired (the endpoint then returns
	// 503). The callback should be cheap and non-blocking: it typically
	// just sends on a buffered channel that main.go selects on alongside
	// SIGINT/SIGTERM, so the same drain path (retention cancel, runner
	// shutdown, http server shutdown) runs regardless of trigger.
	quitFunc func()
}

// approvalConfig bundles everything the coordinator needs apart from
// the store, so SetAgentApprovalStore can swap stores without
// re-deriving mode/timeout/hook closures. Also retains the live bus +
// metrics so a coordinator rebuild keeps publishing to the same
// per-session subscribers and OTel instruments.
type approvalConfig struct {
	mode    agentadapters.ApprovalMode
	timeout time.Duration
	logger  *slog.Logger
	hooks   agentadapters.CoordinatorHooks
	live    *agentChatLive
	metrics *telemetry.AgentAdapterApprovalMetrics
}

// OrchestratorMetrics returns the metrics instance the runner is using.
// main.go reads this to wire the same instance into the MCP client
// cache observer so cache hit/miss/evict events show up alongside
// run/step/approval metrics on a single instrument set. nil when the
// handler hasn't been wired yet (test fixtures that bypass NewHandler).
func (h *Handler) OrchestratorMetrics() *telemetry.OrchestratorMetrics {
	return h.orchestratorMetrics
}

func (h *Handler) gatewayErrorDetails(ctx context.Context, requestID string) ErrorDetails {
	details := ErrorDetails{RequestID: requestID}
	if h == nil || h.service == nil || requestID == "" {
		return details
	}
	trace, err := h.service.Trace(ctx, requestID)
	if err == nil && trace != nil {
		details.TraceID = trace.TraceID
	}
	return details
}

type ProviderRuntime interface {
	Reload(ctx context.Context) error
	SecretStorageEnabled() bool
	Upsert(ctx context.Context, provider controlplane.Provider, apiKey string) (controlplane.Provider, error)
	RotateSecret(ctx context.Context, id, apiKey string) (controlplane.Provider, error)
	DeleteCredential(ctx context.Context, id string) error
	Delete(ctx context.Context, id string) error
}

type StateCleaner interface {
	ClearData(ctx context.Context) (int, error)
}

// NewHandler wires the api.Handler from already-constructed dependencies.
// Storage backends (taskStore, taskQueue) are built by cmd/hecate/main.go
// alongside every other backend the gateway uses, so all dispatch lives in
// one place. taskQueue may be nil — the runner falls back to its default
// in-process queue, which is what the test fixtures rely on.
func NewHandler(cfg config.Config, logger *slog.Logger, service *gateway.Service, cpStore controlplane.Store, taskStore taskstate.Store, taskQueue orchestrator.RunQueue, providerRuntimes ...ProviderRuntime) *Handler {
	var providerRuntime ProviderRuntime
	if len(providerRuntimes) > 0 {
		providerRuntime = providerRuntimes[0]
	}
	if taskStore == nil {
		taskStore = taskstate.NewMemoryStore()
	}

	var rl *ratelimit.Store
	if cfg.Server.RateLimit.Enabled {
		rpm := cfg.Server.RateLimit.RequestsPerMinute
		burst := cfg.Server.RateLimit.BurstSize
		if rpm <= 0 {
			rpm = 60
		}
		if burst <= 0 {
			burst = rpm
		}
		rl = ratelimit.NewStore(burst, rpm)
	}

	tracer := profiler.Tracer(nil)
	if service != nil {
		tracer = service.Tracer()
	}
	if tracer == nil {
		tracer = profiler.NewInMemoryTracer(nil)
	}

	runner := orchestrator.NewRunner(logger, taskStore, tracer, orchestrator.Config{
		DefaultModel:           cfg.Router.DefaultModel,
		ApprovalPolicies:       cfg.Server.TaskApprovalPolicies,
		QueueBackend:           cfg.Server.TaskQueueBackend,
		QueueWorkers:           cfg.Server.TaskQueueWorkers,
		QueueBuffer:            cfg.Server.TaskQueueBuffer,
		QueueLeaseSeconds:      cfg.Server.TaskQueueLeaseSeconds,
		ReconcileInterval:      cfg.Server.TaskReconcileInterval,
		MaxConcurrentPerTenant: cfg.Server.TaskMaxConcurrentPerTenant,
		AgentLoopMaxTurns:      cfg.Server.TaskAgentLoopMaxTurns,
		HTTPPolicy: orchestrator.HTTPRequestPolicy{
			Timeout:          cfg.Server.TaskHTTPTimeout,
			MaxResponseBytes: cfg.Server.TaskHTTPMaxResponseBytes,
			AllowPrivateIPs:  cfg.Server.TaskHTTPAllowPrivateIPs,
			AllowedHosts:     cfg.Server.TaskHTTPAllowedHosts,
		},
		WebSearch: webSearchClientFromConfig(cfg, logger),
		ShellNetwork: orchestrator.ShellNetworkPolicy{
			AllowPrivateIPs: cfg.Server.TaskShellAllowPrivateIPs,
			AllowedHosts:    cfg.Server.TaskShellAllowedHosts,
		},
	})
	if taskQueue != nil {
		runner.SetQueue(taskQueue)
	}
	// Metrics: built once and exposed via Handler.OrchestratorMetrics()
	// so main.go can plumb the same instance into the MCP client cache
	// observer. A second NewOrchestratorMetrics() would register
	// duplicate instruments against the meter provider — same names,
	// different *Counter pointers — which is a real (if rarely-fatal)
	// metrics-SDK footgun on some providers.
	orchestratorMetrics := telemetry.NewOrchestratorMetrics()
	runner.SetMetrics(orchestratorMetrics)
	agentChatMetrics := telemetry.NewAgentChatMetrics()
	agentApprovalMetrics := telemetry.NewAgentAdapterApprovalMetrics()
	agentAdapterMetrics := telemetry.NewAgentAdapterMetrics()
	// Probe metrics are wired via a package-level setter because
	// agentadapters.Probe is invoked by name from the handler — the
	// alternative (pass an explicit pointer through every Probe call
	// site) would change a stable signature. Setter is atomic.Pointer
	// so it's safe to install after handlers are already serving.
	agentadapters.SetProbeMetrics(agentAdapterMetrics)
	// Shutdown-path cancellations route through the same package-level
	// setter pattern: SessionManager.Shutdown fires this once per
	// active session being torn down so the agent-chat-cancelled
	// counter labels them reason="shutdown".
	agentadapters.SetShutdownCancelHook(func(adapterID string) {
		agentChatMetrics.RecordChatCancelled(context.Background(), telemetry.AgentChatCancelledRecord{
			AdapterID: adapterID,
			Reason:    "shutdown",
		})
	})
	// Wire the four-layer agent_loop system-prompt composer. Layers
	// are concatenated broadest-first:
	//   1. global default — operator's HECATE_TASK_AGENT_SYSTEM_PROMPT
	//   2. tenant — controlplane Tenant.SystemPrompt
	//   3. workspace — CLAUDE.md or AGENTS.md in the workspace root
	//      (matches what Claude Code / Codex CLI users already write)
	//   4. per-task — Task.SystemPrompt
	runner.SetSystemPromptResolver(buildSystemPromptResolver(cfg.Server.TaskAgentSystemPrompt))
	// Wire the gateway's chat path as the agent loop's LLM seam. The
	// agent runtime issues its model calls through the same service
	// that handles external client traffic — same routing, same
	// caching, same budget enforcement, same audit trail. The
	// adapter unwraps gateway.ChatResult into the bare ChatResponse
	// the loop expects and uses the gateway streaming path when the
	// provider supports SSE.
	//
	// Tests that build handlers with `nil` providers and don't
	// exercise agent_loop are unaffected — only agent_loop tasks
	// invoke this path, and agent_loop tasks with no providers
	// configured surface a clean error to the operator rather than
	// silently doing nothing.
	runner.SetAgentLLMClient(gatewayAgentLLMClient{service: service})
	reconcileCtx := context.Background()
	if err := runner.ReconcilePendingRuns(reconcileCtx); err != nil {
		telemetry.Warn(logger, reconcileCtx, "task runner reconciliation failed", slog.Any("error", err))
	}
	runner.StartReconcileLoop()

	agentChatRunner := agentadapters.NewSessionManager()
	agentChatRunner.SetLogger(logger)
	agentChatRunner.SetAdapterMetrics(agentAdapterMetrics)
	agentChatRunner.SetTerminalSupportEnabled(cfg.Server.AgentAdapterTerminals)
	// Approval coordinator: applies HECATE_AGENT_ADAPTER_APPROVAL_MODE
	// to each ACP RequestPermission, records the approval row, exposes
	// it to the operator UI/API, and emits approval.* metrics. Default
	// mode is `prompt`; headless operators who want the old
	// auto-approve behavior must set
	// HECATE_AGENT_ADAPTER_APPROVAL_MODE=auto explicitly.
	approvalMode := agentadapters.ApprovalMode(strings.TrimSpace(cfg.Server.AgentAdapterApprovalMode))
	if approvalMode == "" {
		approvalMode = agentadapters.ModePrompt
	}
	if approvalMode == agentadapters.ModeAuto {
		telemetry.Warn(logger, context.Background(), "agent_adapter.approval_mode.auto",
			slog.String("event.name", "agent_adapter.approval_mode.auto"),
			slog.String("warning", "HECATE_AGENT_ADAPTER_APPROVAL_MODE=auto: every adapter RequestPermission is auto-approved with no operator review"),
		)
	}
	// agentChatLive is constructed before the hook builder so the
	// approval coordinator can publish SSE events on the same bus
	// used for chat-session updates.
	agentChatLive := newAgentChatLive(agentChatSnapshotConfigFromServer(cfg.Server))
	approvalHooks := buildApprovalCoordinatorHooks(approvalMode, agentApprovalMetrics, agentChatLive)
	approvalCfg := approvalConfig{
		mode:    approvalMode,
		timeout: cfg.Server.AgentAdapterApprovalTimeout,
		logger:  logger,
		hooks:   approvalHooks,
		// Stash the bus + metrics on the config so SetAgentApprovalStore
		// can rebuild the coordinator without re-deriving them.
		live:    agentChatLive,
		metrics: agentApprovalMetrics,
	}
	approvalCoordinator := agentadapters.NewApprovalCoordinator(agentadapters.CoordinatorOptions{
		Mode:    approvalCfg.mode,
		Timeout: approvalCfg.timeout,
		Logger:  approvalCfg.logger,
		Hooks:   approvalCfg.hooks,
	})
	agentChatRunner.SetApprovalCoordinator(approvalCoordinator)

	memoryStore := memory.NewMemoryStore()
	h := &Handler{
		config:                    cfg,
		logger:                    logger,
		service:                   service,
		controlPlane:              cpStore,
		providerRuntime:           providerRuntime,
		taskStore:                 taskStore,
		taskRunner:                runner,
		tracer:                    tracer,
		rateLimiter:               rl,
		agentChat:                 chat.NewMemoryStore(),
		projects:                  projects.NewMemoryStore(),
		memory:                    memoryStore,
		memoryCandidates:          memoryStore,
		projectWork:               projectwork.NewMemoryStore(),
		projectRuntime:            projectruntime.NewMemoryStore(),
		projectSkills:             projectskills.NewMemoryStore(),
		projectAssistantProposals: projectassistant.NewMemoryProposalStore(),
		pluginRegistry:            pluginregistry.NewMemoryStore(),
		agentProfiles:             agentprofiles.NewMemoryStore(),
		agentChatRunner:           agentChatRunner,
		agentChatLive:             agentChatLive,
		operatorTerminals:         terminalapp.New(terminalapp.Options{Enabled: cfg.Server.OperatorTerminals}),
		orchestratorMetrics:       orchestratorMetrics,
		agentChatMetrics:          agentChatMetrics,
		approvalConfig:            approvalCfg,
	}
	h.wireAgentChatRunnerHooks(agentChatRunner)
	runner.SetProjectAssistantDraftTool(h)
	h.startAgentChatIdleSweeper()
	return h
}

func webSearchClientFromConfig(cfg config.Config, logger *slog.Logger) websearch.Client {
	client, err := websearch.NewClient(websearch.Config{
		Provider:   cfg.Server.TaskWebSearchProvider,
		APIKey:     cfg.Server.TaskWebSearchAPIKey,
		Endpoint:   cfg.Server.TaskWebSearchEndpoint,
		Timeout:    cfg.Server.TaskWebSearchTimeout,
		MaxResults: cfg.Server.TaskWebSearchMaxResults,
		SafeSearch: cfg.Server.TaskWebSearchSafeSearch,
		Country:    cfg.Server.TaskWebSearchCountry,
		SearchLang: cfg.Server.TaskWebSearchSearchLang,
	})
	if err != nil {
		if logger != nil {
			logger.Warn("agent web search disabled", "error", err)
		}
		return nil
	}
	return client
}

// SetAgentApprovalStore swaps in a durable approval store and rebuilds
// the coordinator that's already wired into the SessionManager. Called
// from cmd/hecate after the store is constructed (and after startup
// reconcile has run). Safe to call repeatedly; the previous coordinator
// is replaced atomically inside the SessionManager.
//
// Hooks, mode, and timeout are reused from the original NewHandler call
// — this method only swaps the persistence layer. Tests that don't call
// it keep the default in-memory store wired during construction.
func (h *Handler) SetAgentApprovalStore(store agentadapters.ApprovalStore) {
	if store == nil {
		return
	}
	mgr, ok := h.agentChatRunner.(*agentadapters.SessionManager)
	if !ok {
		return
	}
	coord := agentadapters.NewApprovalCoordinator(agentadapters.CoordinatorOptions{
		Mode:    h.approvalConfig.mode,
		Timeout: h.approvalConfig.timeout,
		Store:   store,
		Logger:  h.approvalConfig.logger,
		Hooks:   h.approvalConfig.hooks,
	})
	mgr.SetApprovalCoordinator(coord)

	// Seed the grants_active UpDownCounter from the live store so a
	// SQLite restart doesn't reset the dashboard line to zero. The
	// in-memory backend is empty at this point and the seed is a
	// no-op; with SQLite there may be persisted grants from a prior
	// process. We deliberately do NOT subtract on subsequent
	// SetAgentApprovalStore calls — replacing the store is a test/dev
	// path; in production the seed runs once.
	if metrics := h.approvalConfig.metrics; metrics != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if grants, err := store.ListGrants(ctx, agentadapters.GrantFilter{}, time.Now().UTC()); err == nil {
			metrics.SeedGrantsActive(ctx, int64(len(grants)))
		} else {
			telemetry.Warn(h.logger, ctx, "agent_adapter.grants_active.seed_failed",
				slog.String("event.name", "agent_adapter.grants_active.seed_failed"),
				slog.Any("error", err),
			)
		}
	}
}

func (h *Handler) SetAgentChatStore(store chat.Store) {
	if store == nil {
		return
	}
	h.agentChat = store
	h.projectAssistantMu.Lock()
	h.projectAssistant = nil
	h.projectAssistantMu.Unlock()
	h.reconcileAgentChatStore(context.Background())
}

func (h *Handler) SetProjectStore(store projects.Store) {
	if store == nil {
		return
	}
	h.projects = store
	h.projectAssistantMu.Lock()
	h.projectAssistant = nil
	h.projectAssistantMu.Unlock()
}

func (h *Handler) SetMemoryStore(store memory.Store) {
	if store == nil {
		return
	}
	h.memory = store
	if candidates, ok := store.(memory.CandidateStore); ok {
		h.memoryCandidates = candidates
	} else {
		h.memoryCandidates = nil
	}
	h.projectAssistantMu.Lock()
	h.projectAssistant = nil
	h.projectAssistantMu.Unlock()
}

func (h *Handler) SetProjectWorkStore(store projectwork.Store) {
	if store == nil {
		return
	}
	h.projectWork = store
	h.projectAssistantMu.Lock()
	h.projectAssistant = nil
	h.projectAssistantMu.Unlock()
}

func (h *Handler) SetProjectRuntimeStore(store projectruntime.Store) {
	if store == nil {
		return
	}
	h.projectRuntime = store
	h.projectAssistantMu.Lock()
	h.projectAssistant = nil
	h.projectAssistantMu.Unlock()
}

func (h *Handler) SetProjectSkillStore(store projectskills.Store) {
	if store == nil {
		return
	}
	h.projectSkills = store
	h.projectAssistantMu.Lock()
	h.projectAssistant = nil
	h.projectAssistantMu.Unlock()
}

func (h *Handler) SetProjectAssistantProposalStore(store projectassistant.ProposalStore) {
	if store == nil {
		return
	}
	h.projectAssistantProposals = store
	h.projectAssistantMu.Lock()
	h.projectAssistant = nil
	h.projectAssistantMu.Unlock()
}

func (h *Handler) SetPluginRegistryStore(store pluginregistry.Store) {
	if store == nil {
		return
	}
	h.pluginRegistry = store
}

func (h *Handler) SetAgentProfileStore(store agentprofiles.Store) {
	if store == nil {
		return
	}
	h.agentProfiles = store
}

func (h *Handler) SetAgentChatRunner(runner agentadapters.Runner) {
	if runner == nil {
		return
	}
	h.agentChatRunner = runner
	h.wireAgentChatRunnerHooks(runner)
}

func (h *Handler) wireAgentChatRunnerHooks(runner agentadapters.Runner) {
	mgr, ok := runner.(*agentadapters.SessionManager)
	if !ok {
		return
	}
	mgr.SetAvailableCommandsUpdateHook(h.handleAgentChatAvailableCommandsUpdate)
}

func (h *Handler) SetStateCleaner(cleaner StateCleaner) {
	h.stateCleaner = cleaner
}

func (h *Handler) reconcileAgentChatStore(ctx context.Context) {
	if h.agentChat == nil {
		return
	}
	count, err := chat.ReconcileInterruptedRuns(ctx, h.agentChat, time.Now().UTC())
	if err != nil {
		telemetry.Warn(h.logger, ctx, "agent chat reconciliation failed", slog.Any("error", err))
		return
	}
	if count > 0 {
		telemetry.Info(h.logger, ctx, "agent chat reconciliation completed", slog.Int("interrupted_runs", count))
		h.reconcileProjectAssignmentsForAllChats(ctx)
	}
}

// SetQuitFunc wires a programmatic shutdown trigger. When set, a
// POST /hecate/v1/system/shutdown call invokes f after acknowledging the
// request. cmd/hecate/main.go provides a closure that signals the same
// channel its SIGINT/SIGTERM handler selects on, so the existing drain
// path runs regardless of trigger — that wiring is unconditional, so
// every standard gateway deployment (Docker, systemd, Tauri sidecar)
// exposes the endpoint. nil is a valid argument and is the default;
// the endpoint then returns 503. This path is for test harnesses and
// custom embedders that build a Handler without wiring quit — never
// reached by the shipped cmd/hecate binary.
func (h *Handler) SetQuitFunc(f func()) {
	h.quitFunc = f
}

// SetSecretCipher wires the settings AES-GCM cipher into the
// handler and its underlying runner. The handler uses it to encrypt
// MCP server env values at task-creation time; the runner passes it
// to NewDefaultMCPHostFactory so the same key decrypts them at spawn.
// Safe to call after NewHandler; intended for main.go to call once the
// bootstrap key is resolved. A nil argument is a no-op.
func (h *Handler) SetSecretCipher(cipher secrets.Cipher) {
	if cipher == nil {
		return
	}
	h.secretCipher = cipher
	if mgr, ok := h.agentChatRunner.(*agentadapters.SessionManager); ok {
		mgr.SetSecretCipher(cipher)
	}
	h.rebuildMCPHostFactory()
}

// SetMCPClientCache wires a SharedClientCache into the runner so MCP
// subprocesses are reused across runs instead of spawned-and-torn-down
// per run. nil is a valid argument — it disables caching, which is
// the existing per-run behavior. Like SetSecretCipher, intended for
// main.go to call once during bootstrap; the cache itself is owned by
// the handler and torn down by Shutdown after the runner drains.
func (h *Handler) SetMCPClientCache(cache *mcpclient.SharedClientCache) {
	h.mcpClientCache = cache
	h.rebuildMCPHostFactory()
}

// rebuildMCPHostFactory rebuilds the runner's MCP host factory using
// the handler's current cipher + cache fields. Called from the
// SetSecretCipher / SetMCPClientCache setters so either one can be
// updated without clobbering the other.
func (h *Handler) rebuildMCPHostFactory() {
	if h.taskRunner == nil {
		return
	}
	h.taskRunner.SetMCPHostFactory(orchestrator.NewDefaultMCPHostFactory(h.secretCipher, h.mcpClientCache))
}

// Shutdown stops the underlying task runner and tears down the shared
// MCP client cache. Bounded by ctx; called from cmd/hecate/main.go on
// SIGTERM so in-flight agent loops cancel cleanly and any spawned MCP
// subprocesses don't orphan when the gateway exits.
//
// Order matters: the runner is shut down FIRST so in-flight runs unwind
// (their pools release cached clients back to the cache), THEN the
// cache is closed so all cached subprocesses are torn down. Closing
// the cache before the runner drains would tear down clients that
// in-flight runs are still calling.
//
// If the runner shutdown fails (deadline exceeded, etc.), the cache
// is still closed — orphaning subprocesses on top of a wedged runner
// is the worst-of-both-worlds outcome we explicitly avoid.
func (h *Handler) Shutdown(ctx context.Context) error {
	if h.agentChatIdleSweepCancel != nil {
		h.agentChatIdleSweepCancel()
	}
	var runnerErr error
	if h.taskRunner != nil {
		runnerErr = h.taskRunner.Shutdown(ctx)
	}
	var cacheErr error
	if h.mcpClientCache != nil {
		cacheErr = h.mcpClientCache.Close()
	}
	var cairnlineSidecarCacheErr error
	h.projectCairnlineSidecarMu.Lock()
	cairnlineSidecarCache := h.projectCairnlineSidecarCache
	h.projectCairnlineSidecarMu.Unlock()
	if cairnlineSidecarCache != nil {
		cairnlineSidecarCacheErr = cairnlineSidecarCache.Close()
	}
	var agentChatErr error
	if h.agentChatRunner != nil {
		agentChatErr = h.agentChatRunner.Shutdown(ctx)
	}
	var terminalErr error
	if h.operatorTerminals != nil {
		terminalErr = h.operatorTerminals.Shutdown(ctx)
	}
	var shutdownErrs []error
	if runnerErr != nil {
		shutdownErrs = append(shutdownErrs, fmt.Errorf("runner shutdown: %w", runnerErr))
	}
	if cacheErr != nil {
		shutdownErrs = append(shutdownErrs, fmt.Errorf("mcp cache close: %w", cacheErr))
	}
	if cairnlineSidecarCacheErr != nil {
		shutdownErrs = append(shutdownErrs, fmt.Errorf("cairnline sidecar cache close: %w", cairnlineSidecarCacheErr))
	}
	if agentChatErr != nil {
		shutdownErrs = append(shutdownErrs, fmt.Errorf("agent chat shutdown: %w", agentChatErr))
	}
	if terminalErr != nil {
		shutdownErrs = append(shutdownErrs, fmt.Errorf("operator terminals shutdown: %w", terminalErr))
	}
	return errors.Join(shutdownErrs...)
}

func (h *Handler) HandleHealth(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"time":    time.Now().UTC().Format(time.RFC3339),
		"version": version.Version,
		"sandbox": map[string]any{
			"os_isolation": sandbox.HealthInfo(),
		},
	})
}

func (h *Handler) HandleSession(w http.ResponseWriter, r *http.Request) {
	item := SessionResponseItem{
		Role: "operator",
		Capabilities: SessionCapabilitiesItem{
			LocalProvidersAllowed: h.config.LocalProvidersAllowed(),
		},
	}
	if identity, ok := remoteruntime.FromContext(r.Context()); ok {
		item.RemoteIdentity = &RemoteIdentityResponseItem{
			ActorID:   identity.ActorID,
			OrgID:     identity.OrgID,
			ProjectID: identity.ProjectID,
			RuntimeID: identity.RuntimeID,
		}
	}
	WriteJSON(w, http.StatusOK, SessionResponse{
		Object: "session",
		Data:   item,
	})
}

func (h *Handler) HandleModels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	models, err := h.modelApplication().ListModels(ctx, modelapp.ListModelsCommand{Refresh: requestRefresh(r)})
	if err != nil {
		telemetry.Error(h.logger, ctx, "gateway.models.list.failed",
			slog.String("event.name", "gateway.models.list.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}

	data := make([]OpenAIModelData, 0, len(models))
	for _, model := range models {
		data = append(data, OpenAIModelData{
			ID:      model.ID,
			Object:  "model",
			OwnedBy: model.OwnedBy,
			Metadata: map[string]any{
				"provider":         model.Provider,
				"provider_kind":    model.Kind,
				"default":          model.Default,
				"discovery_source": model.DiscoverySource,
				"capabilities":     model.Capabilities,
				"readiness":        renderModelReadiness(model.Readiness),
			},
		})
	}

	WriteJSON(w, http.StatusOK, OpenAIModelsResponse{
		Object: "list",
		Data:   data,
	})
}

func requestRefresh(r *http.Request) bool {
	// Accept explicit booleans only so accidental ?refresh does not bypass cache.
	raw := strings.TrimSpace(r.URL.Query().Get("refresh"))
	return raw == "1" || strings.EqualFold(raw, "true")
}

func renderModelReadiness(readiness types.ModelReadiness) ModelReadinessResponseItem {
	return ModelReadinessResponseItem{
		Provider:              readiness.Provider,
		MatchedProvider:       readiness.MatchedProvider,
		Model:                 readiness.Model,
		Ready:                 readiness.Ready,
		Status:                readiness.Status,
		Reason:                readiness.Reason,
		Message:               readiness.Message,
		OperatorAction:        readiness.OperatorAction,
		RoutingReady:          readiness.RoutingReady,
		ProviderStatus:        readiness.ProviderStatus,
		ProviderBlockedReason: readiness.ProviderBlockedReason,
		SuggestedModels:       readiness.SuggestedModels,
	}
}

// requireSettings verifies the settings backend is configured and writes a
// 400 on failure. Single-user mode has no auth gate, so the only check left
// is whether the operator wired up the internal settings store at boot.
func (h *Handler) requireSettings(w http.ResponseWriter, r *http.Request) bool {
	if h.controlPlane == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "settings backend is not configured")
		return false
	}
	return true
}

func (h *Handler) settingsState(ctx context.Context) (controlplane.State, error) {
	if h.controlPlane == nil {
		return controlplane.State{}, nil
	}
	return h.controlPlane.Snapshot(ctx)
}

// settingsActor builds an actor string for audit log entries.
func settingsActor(r *http.Request) string {
	actor := "operator"
	requestID := strings.TrimSpace(RequestIDFromContext(r.Context()))
	if requestID != "" {
		actor += ":" + requestID
	}
	return remoteruntime.ActorForAudit(r.Context(), actor)
}

// decodeJSON decodes the request body into v and writes a 400 on failure.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "request body must be valid JSON")
		return false
	}
	return true
}

func formatUSD(micros int64) string {
	return fmt.Sprintf("%.6f", float64(micros)/1_000_000)
}

// checkRateLimit checks the per-key token bucket and sets X-RateLimit-* headers.
// Returns false (and writes a 429) when the key is out of tokens.
func (h *Handler) checkRateLimit(w http.ResponseWriter, keyID string) bool {
	if h.rateLimiter == nil {
		return true
	}
	if keyID == "" {
		keyID = "anonymous"
	}
	limit, remaining, resetAt, err := h.rateLimiter.Allow(keyID)
	w.Header().Set("X-RateLimit-Limit", strconv.FormatInt(limit, 10))
	w.Header().Set("X-RateLimit-Remaining", strconv.FormatInt(remaining, 10))
	w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetAt.Unix(), 10))
	if err != nil {
		WriteError(w, http.StatusTooManyRequests, errCodeRateLimitExceeded, err.Error())
		return false
	}
	return true
}

// buildApprovalCoordinatorHooks returns the OnRequested / OnResolved /
// OnTimedOut callbacks that emit approval.* OTel metrics AND publish
// SSE events on the per-session live bus so subscribers see approvals
// fire in real time. Extracted so SetAgentApprovalStore can rebuild
// the coordinator with the same hook set when it swaps the store.
//
// The hooks must NEVER block — the OnRequested path runs inline with
// the adapter's ACP RequestPermission and any blocking would stall
// the adapter. Both telemetry and the live bus are non-blocking by
// design (the bus drops on full subscriber buffer).
func buildApprovalCoordinatorHooks(
	mode agentadapters.ApprovalMode,
	metrics *telemetry.AgentAdapterApprovalMetrics,
	live *agentChatLive,
) agentadapters.CoordinatorHooks {
	return agentadapters.CoordinatorHooks{
		OnRequested: func(a agentadapters.Approval) {
			metrics.RecordRequested(context.Background(), telemetry.AgentAdapterApprovalRequestRecord{
				AdapterID: a.AdapterID,
				ToolKind:  a.ToolKind,
				Mode:      string(mode),
			})
			if live != nil {
				live.publishApprovalRequested(ChatApprovalRequestedEvent{
					ApprovalID:   a.ID,
					SessionID:    a.SessionID,
					AdapterID:    a.AdapterID,
					ToolKind:     a.ToolKind,
					ToolName:     a.ToolName,
					ScopeChoices: a.ScopeChoices,
					CreatedAt:    a.CreatedAt.UTC().Format(time.RFC3339Nano),
					ExpiresAt:    a.ExpiresAt.UTC().Format(time.RFC3339Nano),
				})
			}
		},
		OnResolved: func(a agentadapters.Approval, durationMS int64) {
			metrics.RecordResolved(context.Background(), telemetry.AgentAdapterApprovalResolveRecord{
				AdapterID:  a.AdapterID,
				ToolKind:   a.ToolKind,
				Mode:       string(mode),
				Decision:   string(a.Decision),
				Scope:      string(a.Scope),
				Path:       string(a.Path),
				Status:     string(a.Status),
				DurationMS: durationMS,
			})
			if live != nil {
				live.publishApprovalResolved(approvalResolvedEventFromRow(a))
			}
		},
		OnTimedOut: func(a agentadapters.Approval, durationMS int64) {
			rec := telemetry.AgentAdapterApprovalResolveRecord{
				AdapterID:  a.AdapterID,
				ToolKind:   a.ToolKind,
				Mode:       string(mode),
				Path:       string(agentadapters.PathTimeout),
				Status:     string(agentadapters.ApprovalStatusTimedOut),
				DurationMS: durationMS,
			}
			// Record via both the resolved-with-path-timeout
			// counter AND the dedicated timed-out counter so
			// dashboards can alert on timeout rate without
			// pivoting through a path label join.
			metrics.RecordResolved(context.Background(), rec)
			metrics.RecordTimedOut(context.Background(), rec)
			if live != nil {
				live.publishApprovalResolved(approvalResolvedEventFromRow(a))
			}
		},
		OnGrantCreated: func(_ agentadapters.Grant) {
			metrics.RecordGrantCreated(context.Background())
		},
		OnGrantDeleted: func() {
			metrics.RecordGrantDeleted(context.Background())
		},
	}
}

// approvalResolvedEventFromRow projects the coordinator's full
// Approval shape down to the SSE payload. Frontends that need more
// detail (acp_options, full payload) follow up with
// GET /hecate/v1/chat/sessions/{id}/approvals/{id}.
func approvalResolvedEventFromRow(a agentadapters.Approval) ChatApprovalResolvedEvent {
	out := ChatApprovalResolvedEvent{
		ApprovalID:     a.ID,
		SessionID:      a.SessionID,
		Status:         string(a.Status),
		Decision:       string(a.Decision),
		Scope:          string(a.Scope),
		Path:           string(a.Path),
		SelectedOption: a.SelectedOption,
	}
	if a.ResolvedAt != nil {
		out.ResolvedAt = a.ResolvedAt.UTC().Format(time.RFC3339Nano)
	}
	return out
}
