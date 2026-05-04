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
	"time"

	"github.com/hecate/agent-runtime/internal/agentadapters"
	"github.com/hecate/agent-runtime/internal/agentchat"
	"github.com/hecate/agent-runtime/internal/config"
	"github.com/hecate/agent-runtime/internal/controlplane"
	"github.com/hecate/agent-runtime/internal/gateway"
	mcpclient "github.com/hecate/agent-runtime/internal/mcp/client"
	"github.com/hecate/agent-runtime/internal/orchestrator"
	"github.com/hecate/agent-runtime/internal/profiler"
	"github.com/hecate/agent-runtime/internal/ratelimit"
	"github.com/hecate/agent-runtime/internal/sandbox"
	"github.com/hecate/agent-runtime/internal/secrets"
	"github.com/hecate/agent-runtime/internal/taskstate"
	"github.com/hecate/agent-runtime/internal/telemetry"
	"github.com/hecate/agent-runtime/internal/version"
	"github.com/hecate/agent-runtime/pkg/types"
)

type Handler struct {
	config          config.Config
	logger          *slog.Logger
	service         *gateway.Service
	controlPlane    controlplane.Store
	providerRuntime ProviderRuntime
	taskStore       taskstate.Store
	taskRunner      *orchestrator.Runner
	tracer          profiler.Tracer
	agentChat       agentchat.Store
	agentChatRunner agentadapters.Runner
	agentChatLive   *agentChatLive
	rateLimiter     *ratelimit.Store
	// secretCipher encrypts literal MCP server env values at task-creation
	// time and wires the matching decrypting factory into the runner. nil
	// when no control-plane key is configured — values are stored as-is
	// and $VAR_NAME references are the only safe option in that case.
	secretCipher secrets.Cipher
	// mcpClientCache amortizes MCP subprocess spawn cost across runs.
	// When set, the runner's host factory acquires per-server clients
	// from the cache (sharing subprocesses between tasks with the same
	// upstream config); when nil, every run spawns and tears down its
	// own subprocesses. Owned by the handler — Shutdown closes it
	// after the runner has drained.
	mcpClientCache *mcpclient.SharedClientCache
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

type ProviderRuntime interface {
	Reload(ctx context.Context) error
	SecretStorageEnabled() bool
	Upsert(ctx context.Context, provider controlplane.Provider, apiKey string) (controlplane.Provider, error)
	RotateSecret(ctx context.Context, id, apiKey string) (controlplane.Provider, error)
	DeleteCredential(ctx context.Context, id string) error
	Delete(ctx context.Context, id string) error
}

// NewHandler wires the api.Handler from already-constructed dependencies.
// Storage backends (taskStore, taskQueue) are built by cmd/gateway/main.go
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
	// Wire the four-layer agent_loop system-prompt composer. Layers
	// are concatenated broadest-first:
	//   1. global default — operator's GATEWAY_TASK_AGENT_SYSTEM_PROMPT
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
	// the loop expects.
	//
	// Tests that build handlers with `nil` providers and don't
	// exercise agent_loop are unaffected — only agent_loop tasks
	// invoke this path, and agent_loop tasks with no providers
	// configured surface a clean error to the operator rather than
	// silently doing nothing.
	runner.SetAgentLLMClient(orchestrator.AgentLLMClientFunc(func(ctx context.Context, req types.ChatRequest) (*types.ChatResponse, error) {
		result, err := service.HandleChat(ctx, req)
		if err != nil {
			return nil, err
		}
		if result == nil {
			return nil, nil
		}
		return result.Response, nil
	}))
	reconcileCtx := context.Background()
	if err := runner.ReconcilePendingRuns(reconcileCtx); err != nil {
		telemetry.Warn(logger, reconcileCtx, "task runner reconciliation failed", slog.Any("error", err))
	}
	runner.StartReconcileLoop()

	agentChatRunner := agentadapters.NewSessionManager()
	agentChatRunner.SetLogger(logger)
	// Approval recorder: applies GATEWAY_AGENT_ADAPTER_APPROVAL_MODE to
	// each ACP RequestPermission, records the approval row, and emits
	// approval.* metrics. Default mode is `prompt`; until the operator
	// UI ships, prompt mode resolves to ACP Cancelled after the
	// configured timeout. Operators who depend on the prior auto-approve
	// behavior must set GATEWAY_AGENT_ADAPTER_APPROVAL_MODE=auto
	// explicitly.
	approvalMode := agentadapters.ApprovalMode(strings.TrimSpace(cfg.Server.AgentAdapterApprovalMode))
	if approvalMode == "" {
		approvalMode = agentadapters.ModePrompt
	}
	if approvalMode == agentadapters.ModeAuto {
		telemetry.Warn(logger, context.Background(), "agent_adapter.approval_mode.auto",
			slog.String("event.name", "agent_adapter.approval_mode.auto"),
			slog.String("warning", "GATEWAY_AGENT_ADAPTER_APPROVAL_MODE=auto: every adapter RequestPermission is auto-approved with no operator review"),
		)
	}
	// agentChatLive is constructed before the hook builder so the
	// approval coordinator can publish SSE events on the same bus
	// used for chat-session updates.
	agentChatLive := newAgentChatLive()
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

	return &Handler{
		config:              cfg,
		logger:              logger,
		service:             service,
		controlPlane:        cpStore,
		providerRuntime:     providerRuntime,
		taskStore:           taskStore,
		taskRunner:          runner,
		tracer:              tracer,
		rateLimiter:         rl,
		agentChat:           agentchat.NewMemoryStore(),
		agentChatRunner:     agentChatRunner,
		agentChatLive:       agentChatLive,
		orchestratorMetrics: orchestratorMetrics,
		agentChatMetrics:    agentChatMetrics,
		approvalConfig:      approvalCfg,
	}
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
}

func (h *Handler) SetAgentChatStore(store agentchat.Store) {
	if store == nil {
		return
	}
	h.agentChat = store
	h.reconcileAgentChatStore(context.Background())
}

func (h *Handler) SetAgentChatRunner(runner agentadapters.Runner) {
	if runner == nil {
		return
	}
	h.agentChatRunner = runner
}

func (h *Handler) reconcileAgentChatStore(ctx context.Context) {
	if h.agentChat == nil {
		return
	}
	count, err := agentchat.ReconcileInterruptedRuns(ctx, h.agentChat, time.Now().UTC())
	if err != nil {
		telemetry.Warn(h.logger, ctx, "agent chat reconciliation failed", slog.Any("error", err))
		return
	}
	if count > 0 {
		telemetry.Info(h.logger, ctx, "agent chat reconciliation completed", slog.Int("interrupted_runs", count))
	}
}

// SetSecretCipher wires the control-plane AES-GCM cipher into the
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
// MCP client cache. Bounded by ctx; called from cmd/gateway/main.go on
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
	var runnerErr error
	if h.taskRunner != nil {
		runnerErr = h.taskRunner.Shutdown(ctx)
	}
	var cacheErr error
	if h.mcpClientCache != nil {
		cacheErr = h.mcpClientCache.Close()
	}
	var agentChatErr error
	if h.agentChatRunner != nil {
		agentChatErr = h.agentChatRunner.Shutdown(ctx)
	}
	var shutdownErrs []error
	if runnerErr != nil {
		shutdownErrs = append(shutdownErrs, fmt.Errorf("runner shutdown: %w", runnerErr))
	}
	if cacheErr != nil {
		shutdownErrs = append(shutdownErrs, fmt.Errorf("mcp cache close: %w", cacheErr))
	}
	if agentChatErr != nil {
		shutdownErrs = append(shutdownErrs, fmt.Errorf("agent chat shutdown: %w", agentChatErr))
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
	WriteJSON(w, http.StatusOK, SessionResponse{
		Object: "session",
		Data:   SessionResponseItem{Role: "operator"},
	})
}

func (h *Handler) HandleModels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	result, err := h.service.ListModels(ctx)
	if err != nil {
		telemetry.Error(h.logger, ctx, "gateway.models.list.failed",
			slog.String("event.name", "gateway.models.list.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, "gateway_error", err.Error())
		return
	}

	data := make([]OpenAIModelData, 0, len(result.Models))
	for _, model := range result.Models {
		data = append(data, OpenAIModelData{
			ID:      model.ID,
			Object:  "model",
			OwnedBy: model.OwnedBy,
			Metadata: map[string]any{
				"provider":         model.Provider,
				"provider_kind":    model.Kind,
				"default":          model.Default,
				"discovery_source": model.DiscoverySource,
			},
		})
	}

	WriteJSON(w, http.StatusOK, OpenAIModelsResponse{
		Object: "list",
		Data:   data,
	})
}

// requireControlPlane verifies the control plane is configured and writes a
// 400 on failure. Single-user mode has no auth gate, so the only check left
// is whether the operator wired up a control-plane store at boot.
func (h *Handler) requireControlPlane(w http.ResponseWriter, r *http.Request) bool {
	if h.controlPlane == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "control plane backend is not configured")
		return false
	}
	return true
}

// controlPlaneActor builds an actor string for audit log entries.
func controlPlaneActor(r *http.Request) string {
	actor := "operator"
	requestID := strings.TrimSpace(RequestIDFromContext(r.Context()))
	if requestID == "" {
		return actor
	}
	return actor + ":" + requestID
}

// decodeJSON decodes the request body into v and writes a 400 on failure.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON")
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
		WriteError(w, http.StatusTooManyRequests, "rate_limit_exceeded", err.Error())
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
				live.publishApprovalRequested(AgentChatApprovalRequestedEvent{
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
			metrics.RecordResolved(context.Background(), telemetry.AgentAdapterApprovalResolveRecord{
				AdapterID:  a.AdapterID,
				ToolKind:   a.ToolKind,
				Mode:       string(mode),
				Path:       string(agentadapters.PathTimeout),
				Status:     string(agentadapters.ApprovalStatusTimedOut),
				DurationMS: durationMS,
			})
			if live != nil {
				live.publishApprovalResolved(approvalResolvedEventFromRow(a))
			}
		},
	}
}

// approvalResolvedEventFromRow projects the coordinator's full
// Approval shape down to the SSE payload. Frontends that need more
// detail (acp_options, full payload) follow up with
// GET /v1/agent-chat/sessions/{id}/approvals/{id}.
func approvalResolvedEventFromRow(a agentadapters.Approval) AgentChatApprovalResolvedEvent {
	out := AgentChatApprovalResolvedEvent{
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
