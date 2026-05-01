package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hecate/agent-runtime/internal/auth"
	"github.com/hecate/agent-runtime/internal/config"
	"github.com/hecate/agent-runtime/internal/controlplane"
	"github.com/hecate/agent-runtime/internal/gateway"
	mcpclient "github.com/hecate/agent-runtime/internal/mcp/client"
	"github.com/hecate/agent-runtime/internal/orchestrator"
	"github.com/hecate/agent-runtime/internal/ratelimit"
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
	authenticator   *auth.Authenticator
	controlPlane    controlplane.Store
	providerRuntime ProviderRuntime
	taskStore       taskstate.Store
	taskRunner      *orchestrator.Runner
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
	// bootstrapTokenExposable controls whether GET /v1/bootstrap-token
	// returns the admin token to a loopback caller. True when the token
	// was auto-generated (gateway-managed); false when the operator
	// supplied GATEWAY_AUTH_TOKEN at boot — the gateway doesn't hand
	// out tokens it doesn't own. Set via SetBootstrapTokenExposable
	// from main.go after bootstrap.Resolve.
	bootstrapTokenExposable bool
}

// SetBootstrapTokenExposable controls whether GET /v1/bootstrap-token
// hands out the admin token on loopback requests. main.go enables it
// when the token is gateway-managed (auto-generated bootstrap),
// disables it when GATEWAY_AUTH_TOKEN was supplied — that token
// belongs to the operator, not the gateway.
func (h *Handler) SetBootstrapTokenExposable(v bool) {
	h.bootstrapTokenExposable = v
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

	runner := orchestrator.NewRunner(logger, taskStore, service.Tracer(), orchestrator.Config{
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
	// Wire the four-layer agent_loop system-prompt composer. Layers
	// are concatenated broadest-first:
	//   1. global default — operator's GATEWAY_TASK_AGENT_SYSTEM_PROMPT
	//   2. tenant — controlplane Tenant.SystemPrompt
	//   3. workspace — CLAUDE.md or AGENTS.md in the workspace root
	//      (matches what Claude Code / Codex CLI users already write)
	//   4. per-task — Task.SystemPrompt
	runner.SetSystemPromptResolver(buildSystemPromptResolver(cfg.Server.TaskAgentSystemPrompt, cpStore))
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
	if err := runner.ReconcilePendingRuns(context.Background()); err != nil {
		logger.Warn("task runner reconciliation failed", slog.Any("error", err))
	}
	runner.StartReconcileLoop()

	return &Handler{
		config:              cfg,
		logger:              logger,
		service:             service,
		authenticator:       auth.NewAuthenticator(cfg.Server, cpStore),
		controlPlane:        cpStore,
		providerRuntime:     providerRuntime,
		taskStore:           taskStore,
		taskRunner:          runner,
		rateLimiter:         rl,
		orchestratorMetrics: orchestratorMetrics,
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
	switch {
	case runnerErr != nil && cacheErr != nil:
		return fmt.Errorf("runner shutdown: %w; mcp cache close: %v", runnerErr, cacheErr)
	case runnerErr != nil:
		return runnerErr
	case cacheErr != nil:
		return cacheErr
	default:
		return nil
	}
}

func (h *Handler) HandleHealth(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"time":    time.Now().UTC().Format(time.RFC3339),
		"version": version.Version,
	})
}

func (h *Handler) HandleSession(w http.ResponseWriter, r *http.Request) {
	introspection := h.authenticator.Introspect(r)
	WriteJSON(w, http.StatusOK, SessionResponse{
		Object: "session",
		Data: SessionResponseItem{
			Authenticated:    introspection.Authenticated,
			InvalidToken:     introspection.InvalidToken,
			Role:             introspection.Principal.Role,
			Name:             introspection.Principal.Name,
			Tenant:           introspection.Principal.Tenant,
			Source:           introspection.Principal.Source,
			KeyID:            introspection.Principal.KeyID,
			AllowedProviders: introspection.Principal.AllowedProviders,
			AllowedModels:    introspection.Principal.AllowedModels,
			Features: SessionFeatures{
				MultiTenant:  h.config.Server.MultiTenant,
				AuthDisabled: !h.authenticator.Enabled(),
			},
		},
	})
}

func (h *Handler) HandleModels(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requireAny(w, r)
	if !ok {
		return
	}
	ctx := h.contextWithPrincipal(r.Context(), principal)

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
		if !principal.IsAdmin() && !modelAllowedForPrincipal(principal, model.Provider, model.ID) {
			continue
		}
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

// contextWithPrincipal attaches principal identity to the context for telemetry.
func (h *Handler) contextWithPrincipal(ctx context.Context, principal auth.Principal) context.Context {
	return telemetry.WithPrincipal(ctx, telemetry.Principal{
		Name:     principal.Name,
		Role:     principal.Role,
		TenantID: principal.Tenant,
		Source:   principal.Source,
		KeyID:    principal.KeyID,
	})
}

func (h *Handler) authorizeAny(r *http.Request) (auth.Principal, bool) {
	return h.authenticator.Authenticate(r)
}

func (h *Handler) authorizeAdmin(r *http.Request) (auth.Principal, bool) {
	if h.authenticator == nil || !h.authenticator.Enabled() {
		return auth.Principal{Role: "admin"}, true
	}
	principal, ok := h.authorizeAny(r)
	if !ok || !principal.IsAdmin() {
		return auth.Principal{}, false
	}
	return principal, true
}

// requireAny authenticates any valid principal and writes a 401 on failure.
func (h *Handler) requireAny(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	principal, ok := h.authorizeAny(r)
	if !ok {
		WriteError(w, http.StatusUnauthorized, errCodeUnauthorized, "missing or invalid bearer token")
		return auth.Principal{}, false
	}
	return principal, true
}

// requireAdmin authenticates an admin principal and writes a 401 on failure.
func (h *Handler) requireAdmin(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	principal, ok := h.authorizeAdmin(r)
	if !ok {
		WriteError(w, http.StatusUnauthorized, errCodeUnauthorized, "missing or invalid bearer token")
		return auth.Principal{}, false
	}
	return principal, true
}

// requireControlPlane authenticates an admin and verifies the control plane is configured.
func (h *Handler) requireControlPlane(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	principal, ok := h.requireAdmin(w, r)
	if !ok {
		return auth.Principal{}, false
	}
	if h.controlPlane == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "control plane backend is not configured")
		return auth.Principal{}, false
	}
	return principal, true
}

// controlPlaneActor builds an actor string for audit log entries.
func controlPlaneActor(principal auth.Principal, r *http.Request) string {
	actor := strings.TrimSpace(principal.Name)
	if actor == "" {
		actor = principal.Role
	}
	if actor == "" {
		actor = "admin"
	}
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
