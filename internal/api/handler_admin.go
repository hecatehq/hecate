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

	"github.com/hecate/agent-runtime/internal/gateway"
	"github.com/hecate/agent-runtime/internal/governor"
	mcpclient "github.com/hecate/agent-runtime/internal/mcp/client"
	"github.com/hecate/agent-runtime/internal/orchestrator"
	"github.com/hecate/agent-runtime/internal/retention"
	"github.com/hecate/agent-runtime/internal/sandbox"
	"github.com/hecate/agent-runtime/internal/secrets"
	"github.com/hecate/agent-runtime/internal/telemetry"
	"github.com/hecate/agent-runtime/pkg/types"
)

func (h *Handler) HandleProviderStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	result, err := h.service.ProviderStatus(ctx)
	if err != nil {
		telemetry.Error(h.logger, ctx, "gateway.providers.status.failed",
			slog.String("event.name", "gateway.providers.status.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}

	data := make([]ProviderStatusResponseItem, 0, len(result.Providers))
	for _, provider := range result.Providers {
		item := ProviderStatusResponseItem{
			Name:                provider.Name,
			Kind:                provider.Kind,
			BaseURL:             provider.BaseURL,
			CredentialState:     provider.CredentialState,
			CredentialReady:     provider.CredentialReady,
			Healthy:             provider.Healthy,
			Status:              provider.Status,
			RoutingReady:        provider.RoutingReady,
			RoutingBlocked:      provider.RoutingBlocked,
			DefaultModel:        provider.DefaultModel,
			Models:              provider.Models,
			ModelCount:          len(provider.Models),
			DiscoverySource:     provider.DiscoverySource,
			LastError:           provider.LastError,
			LastErrorClass:      provider.LastErrorClass,
			LastLatencyMS:       provider.LastLatencyMS,
			ConsecutiveFailures: provider.ConsecutiveFailures,
			TotalSuccesses:      provider.TotalSuccesses,
			TotalFailures:       provider.TotalFailures,
			Timeouts:            provider.Timeouts,
			ServerErrors:        provider.ServerErrors,
			RateLimits:          provider.RateLimits,
			Readiness:           renderReadinessSummary(provider.Readiness),
			ReadinessChecks:     renderProviderReadinessChecks(provider.ReadinessChecks),
		}
		if !provider.RefreshedAt.IsZero() {
			item.RefreshedAt = provider.RefreshedAt.UTC().Format(time.RFC3339)
		}
		if !provider.LastCheckedAt.IsZero() {
			item.LastCheckedAt = provider.LastCheckedAt.UTC().Format(time.RFC3339)
		}
		if !provider.OpenUntil.IsZero() {
			item.OpenUntil = provider.OpenUntil.UTC().Format(time.RFC3339)
		}
		data = append(data, item)
	}

	WriteJSON(w, http.StatusOK, ProviderStatusResponse{
		Object: "provider_status",
		Data:   data,
	})
}

func renderReadinessSummary(summary types.ReadinessSummary) ReadinessSummaryResponseItem {
	return ReadinessSummaryResponseItem{
		Status:         summary.Status,
		Reason:         summary.Reason,
		Message:        summary.Message,
		OperatorAction: summary.OperatorAction,
	}
}

func renderProviderReadinessChecks(checks []types.ProviderReadinessCheck) []ProviderReadinessCheckResponseItem {
	if len(checks) == 0 {
		return nil
	}
	out := make([]ProviderReadinessCheckResponseItem, 0, len(checks))
	for _, check := range checks {
		out = append(out, ProviderReadinessCheckResponseItem{
			Name:           check.Name,
			Status:         check.Status,
			Reason:         check.Reason,
			Message:        check.Message,
			OperatorAction: check.OperatorAction,
		})
	}
	return out
}

func (h *Handler) HandleProviderHealthHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit := h.config.Provider.HistoryLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	provider := strings.TrimSpace(r.URL.Query().Get("provider"))

	result, err := h.service.ProviderHealthHistory(ctx, provider, limit)
	if err != nil {
		telemetry.Error(h.logger, ctx, "gateway.providers.history.failed",
			slog.String("event.name", "gateway.providers.history.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}

	data := make([]ProviderHealthHistoryResponseItem, 0, len(result.Entries))
	for _, entry := range result.Entries {
		item := ProviderHealthHistoryResponseItem{
			Provider:            entry.Provider,
			ProviderKind:        entry.ProviderKind,
			Model:               entry.Model,
			Event:               entry.Event,
			Status:              entry.Status,
			Available:           entry.Available,
			Error:               entry.Error,
			ErrorClass:          entry.ErrorClass,
			Reason:              entry.Reason,
			RouteReason:         entry.RouteReason,
			RequestID:           entry.RequestID,
			TraceID:             entry.TraceID,
			PeerProvider:        entry.PeerProvider,
			PeerModel:           entry.PeerModel,
			PeerRouteReason:     entry.PeerRouteReason,
			HealthStatus:        entry.HealthStatus,
			PeerHealthStatus:    entry.PeerHealthStatus,
			LatencyMS:           entry.LatencyMS,
			ConsecutiveFailures: entry.ConsecutiveFailures,
			TotalSuccesses:      entry.TotalSuccesses,
			TotalFailures:       entry.TotalFailures,
			Timeouts:            entry.Timeouts,
			ServerErrors:        entry.ServerErrors,
			RateLimits:          entry.RateLimits,
			AttemptCount:        entry.AttemptCount,
			EstimatedMicrosUSD:  entry.EstimatedMicrosUSD,
		}
		if !entry.OpenUntil.IsZero() {
			item.OpenUntil = entry.OpenUntil.UTC().Format(time.RFC3339Nano)
		}
		if !entry.Timestamp.IsZero() {
			item.Timestamp = entry.Timestamp.UTC().Format(time.RFC3339Nano)
		}
		data = append(data, item)
	}

	WriteJSON(w, http.StatusOK, ProviderHealthHistoryResponse{
		Object: "provider_health_history",
		Data:   data,
	})
}

func (h *Handler) HandleRuntimeStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	h.writeRuntimeStats(w, ctx)
}

func (h *Handler) writeRuntimeStats(w http.ResponseWriter, ctx context.Context) {
	if h.taskRunner == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task runner is not configured")
		return
	}

	stats, err := h.taskRunner.RuntimeStats(ctx)
	if err != nil {
		telemetry.Error(h.logger, ctx, "gateway.runtime.stats.failed",
			slog.String("event.name", "gateway.runtime.stats.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}

	rtkPath, rtkAvailable := sandbox.RTKAvailable()

	WriteJSON(w, http.StatusOK, RuntimeStatsResponse{
		Object: "runtime_stats",
		Data: RuntimeStatsResponseItem{
			CheckedAt:               stats.CheckedAt.UTC().Format(time.RFC3339Nano),
			QueueDepth:              stats.QueueDepth,
			QueueCapacity:           stats.QueueCapacity,
			QueueBackend:            stats.QueueBackend,
			WorkerCount:             stats.WorkerCount,
			InFlightJobs:            stats.InFlightJobs,
			QueuedRuns:              stats.QueuedRuns,
			RunningRuns:             stats.RunningRuns,
			AwaitingApprovalRuns:    stats.AwaitingApprovalRuns,
			OldestQueuedAgeSeconds:  stats.OldestQueuedAgeSeconds,
			OldestRunningAgeSeconds: stats.OldestRunningAgeSeconds,
			StoreBackend:            stats.StoreBackend,
			// Surface the configured external-agent approval mode so
			// the UI can render a danger banner when "auto" is set
			// (every adapter tool call is permitted without operator
			// review). Empty when the handler was built without an
			// approval coordinator (test fixtures bypass NewHandler).
			AgentAdapterApprovalMode: string(h.approvalConfig.mode),
			RTKAvailable:             rtkAvailable,
			RTKPath:                  rtkPath,
		},
	})
}

// HandleMCPProbe is the dry-run discovery endpoint for MCP server
// configs. POST /hecate/v1/mcp/probe accepts a single MCPServerConfig-shaped
// body, brings the server up exactly the way an agent_loop run would
// (same secret resolution, same uncached spawn path), calls
// tools/list, and tears it down. Returns the upstream's tool catalog
// so operators can confirm a config before committing it to a task.
//
// Auth matches POST /hecate/v1/tasks (requireAny): if a principal can create
// a task with mcp_servers configured, it can probe with the same
// config. Both paths exec the same arbitrary command; probe just
// returns earlier.
//
// Bounded by a 10s deadline derived from the request context — a
// stuck upstream surfaces as a clean error rather than wedging the
// caller. Callers can pass a shorter deadline by setting their own
// timeout on the HTTP client; we don't extend.
func (h *Handler) HandleMCPProbe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req MCPProbeRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	cfg, err := normalizeMCPProbeRequest(req, h.secretCipher)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}

	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	result, err := orchestrator.ProbeMCPServer(probeCtx, cfg, h.secretCipher)
	if err != nil {
		// Probe failures are operator-actionable (wrong command,
		// missing dep, bad URL) — surface as 400 with the diagnostic
		// rather than 500. The orchestrator helper already wraps
		// stderr from stdio servers and HTTP status from URL
		// servers, so the message is concrete.
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, MCPProbeResponse{
		Object: "mcp_probe",
		Data: MCPProbeResponseItem{
			// ServerName / ServerVersion not surfaced from Pool.Tools
			// today (the namespacing is alias-based); leaving them
			// empty until the client package exposes the upstream
			// initialize result. Tools alone is the headline value
			// operators want.
			Tools: renderMCPProbeTools(result.Tools),
		},
	})
}

// normalizeMCPProbeRequest validates and converts the wire shape into
// the internal types.MCPServerConfig. Mirrors normalizeMCPServerConfigs's
// rules for one row: non-empty name (default "probe" if missing —
// the operator typically doesn't care about the alias for a dry-run),
// command XOR url, secrets resolved via the shared storeSecretMap helper.
func normalizeMCPProbeRequest(req MCPProbeRequest, cipher secrets.Cipher) (types.MCPServerConfig, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "probe"
	}
	command := strings.TrimSpace(req.Command)
	rawURL := strings.TrimSpace(req.URL)
	if command != "" && rawURL != "" {
		return types.MCPServerConfig{}, fmt.Errorf("command and url are mutually exclusive")
	}
	if command == "" && rawURL == "" {
		return types.MCPServerConfig{}, fmt.Errorf("either command or url is required")
	}
	args := append([]string(nil), req.Args...)
	env, err := storeSecretMap(req.Env, cipher, "env")
	if err != nil {
		return types.MCPServerConfig{}, err
	}
	headers, err := storeSecretMap(req.Headers, cipher, "headers")
	if err != nil {
		return types.MCPServerConfig{}, err
	}
	return types.MCPServerConfig{
		Name:    name,
		Command: command,
		Args:    args,
		Env:     env,
		URL:     rawURL,
		Headers: headers,
	}, nil
}

// renderMCPProbeTools converts the orchestrator's namespaced-tool list
// into the wire descriptor. Schema is forwarded verbatim because
// operators want to see exactly what the upstream declared (for docs,
// for fixture-building, for sanity checks).
func renderMCPProbeTools(tools []mcpclient.NamespacedTool) []MCPProbeToolDescriptor {
	out := make([]MCPProbeToolDescriptor, 0, len(tools))
	for _, t := range tools {
		out = append(out, MCPProbeToolDescriptor{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.Schema,
		})
	}
	return out
}

// HandleMCPCacheStats returns a snapshot of the shared MCP client
// cache: distinct cached upstream count, total in-flight refcount,
// and idle (refcount=0) entry count. Lets operators answer
// "is the cache doing useful work?" without scraping OTLP.
//
// Configured=false when no cache is wired (deploys that explicitly
// disabled it via SetMCPClientCache(nil), or test fixtures that
// bypass the setter); the data block still carries zeros so clients
// can render a "no cache" cell instead of error-handling a 4xx.
func (h *Handler) HandleMCPCacheStats(w http.ResponseWriter, r *http.Request) {
	_ = r.Context() // for parity with other admin handlers; no downstream use yet

	item := MCPCacheStatsResponseItem{
		CheckedAt:  time.Now().UTC().Format(time.RFC3339Nano),
		Configured: h.mcpClientCache != nil,
	}
	if h.mcpClientCache != nil {
		s := h.mcpClientCache.Stats()
		item.Entries = s.Entries
		item.InUse = s.InUse
		item.Idle = s.Idle
	}
	WriteJSON(w, http.StatusOK, MCPCacheStatsResponse{
		Object: "mcp_cache_stats",
		Data:   item,
	})
}

// HandleSystemShutdown requests an orderly process shutdown. The
// desktop app (Tauri) calls this from its window-close handler so the
// gateway runs the same drain path SIGINT/SIGTERM takes — retention
// cancel, runner drain (MCP subprocess teardown), HTTP server shutdown
// — instead of being SIGKILL'd by the child-process handle. The
// shipped cmd/hecate binary wires SetQuitFunc unconditionally, so the
// endpoint is available in every standard deployment (Tauri sidecar,
// Docker, systemd) — operators can also POST it as an alternative to
// signalling the process. The 503 path is for test harnesses and
// custom embedders that build a Handler without wiring quit.
//
// The response is 202 Accepted: the signal is fired asynchronously
// after a short delay so the response can flush before the HTTP server
// stops accepting writes. Clients that need to observe the gateway
// actually exiting should poll /healthz until it stops responding.
func (h *Handler) HandleSystemShutdown(w http.ResponseWriter, r *http.Request) {
	if h.quitFunc == nil {
		WriteError(w, http.StatusServiceUnavailable, errCodeGatewayError,
			"shutdown endpoint not wired; quit via signal (SIGINT/SIGTERM) instead")
		return
	}

	telemetry.Info(h.logger, r.Context(), "gateway.system.shutdown.requested",
		slog.String("event.name", "gateway.system.shutdown.requested"),
		slog.String("remote_addr", r.RemoteAddr),
	)

	WriteJSON(w, http.StatusAccepted, struct {
		Object string `json:"object"`
	}{Object: "system_shutdown"})

	// Fire the quit signal on a short delay so the 202 has time to
	// flush back to the caller before the server stops accepting
	// connections. 50ms is plenty for loopback; the drain itself
	// runs in main.go with a 10s deadline.
	go func() {
		time.Sleep(50 * time.Millisecond)
		h.quitFunc()
	}()
}

func (h *Handler) HandleRetentionRuns(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit := 20
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "limit query parameter must be a non-negative integer")
			return
		}
		if value > 200 {
			value = 200
		}
		limit = value
	}

	result, err := h.service.ListRetentionRuns(ctx, limit)
	if err != nil {
		telemetry.Error(h.logger, ctx, "gateway.retention.list.failed",
			slog.String("event.name", "gateway.retention.list.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}

	items := make([]RetentionRunData, 0, len(result.Runs))
	for _, run := range result.Runs {
		items = append(items, renderRetentionRunData(run.StartedAt, run.FinishedAt, run.Trigger, run.Actor, run.RequestID, run.Results))
	}

	WriteJSON(w, http.StatusOK, RetentionRunsResponse{
		Object: "retention_runs",
		Data:   items,
	})
}

type RetentionRunRequest struct {
	Subsystems []string `json:"subsystems"`
}

func (h *Handler) HandleRetentionRun(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req RetentionRunRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "request body must be valid JSON")
			return
		}
	}

	result, err := h.service.RunRetention(ctx, retention.RunRequest{
		Trigger:    "manual",
		Subsystems: req.Subsystems,
		Actor:      settingsActor(r),
		RequestID:  strings.TrimSpace(RequestIDFromContext(r.Context())),
	})
	if err != nil {
		telemetry.Error(h.logger, ctx, "gateway.retention.run.failed",
			slog.String("event.name", "gateway.retention.run.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, RetentionRunResponse{
		Object: "retention_run",
		Data: renderRetentionRunData(
			result.Run.StartedAt.UTC().Format(time.RFC3339Nano),
			result.Run.FinishedAt.UTC().Format(time.RFC3339Nano),
			result.Run.Trigger,
			settingsActor(r),
			strings.TrimSpace(RequestIDFromContext(r.Context())),
			result.Run.Results,
		),
	})
}

func (h *Handler) HandleUsageSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	result, err := h.service.UsageSummaryWithFilter(ctx, usageFilterFromRequest(r))
	if err != nil {
		telemetry.Error(h.logger, ctx, "gateway.usage.summary.failed",
			slog.String("event.name", "gateway.usage.summary.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, renderUsageSummaryResponse(result))
}

func (h *Handler) HandleUsageEvents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	h.writeUsageEvents(w, r, ctx)
}

func (h *Handler) writeUsageEvents(w http.ResponseWriter, r *http.Request, ctx context.Context) {
	limit := 20
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "limit query parameter must be a non-negative integer")
			return
		}
		if value > 200 {
			value = 200
		}
		limit = value
	}

	result, err := h.service.UsageEvents(ctx, limit)
	if err != nil {
		telemetry.Error(h.logger, ctx, "gateway.usage.events.failed",
			slog.String("event.name", "gateway.usage.events.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, UsageEventsResponse{
		Object: "usage_events",
		Data:   renderUsageEventRecords(result.Entries),
	})
}

func renderRetentionRunData(startedAt, finishedAt, trigger, actor, requestID string, results []retention.SubsystemResult) RetentionRunData {
	items := make([]RetentionRunResultRecord, 0, len(results))
	for _, item := range results {
		record := RetentionRunResultRecord{
			Name:     item.Name,
			Deleted:  item.Deleted,
			MaxCount: item.MaxCount,
			Error:    item.Error,
			Skipped:  item.Skipped,
		}
		if item.MaxAge > 0 {
			record.MaxAge = item.MaxAge.String()
		}
		items = append(items, record)
	}
	return RetentionRunData{
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		Trigger:    trigger,
		Actor:      actor,
		RequestID:  requestID,
		Results:    items,
	}
}

func renderUsageSummaryResponse(result *gateway.UsageSummaryResult) UsageSummaryResponse {
	return UsageSummaryResponse{
		Object: "usage_summary",
		Data:   renderUsageSummaryRecord(result.Summary),
	}
}

func renderUsageSummaryRecord(summary types.UsageSummary) UsageSummaryResponseItem {
	return UsageSummaryResponseItem{
		Key:           summary.Key,
		Scope:         summary.Scope,
		Provider:      summary.Provider,
		Backend:       summary.Backend,
		UsedMicrosUSD: summary.UsedMicrosUSD,
		UsedUSD:       formatUSD(summary.UsedMicrosUSD),
	}
}

func renderUsageEventRecords(entries []types.UsageEventEntry) []UsageEventRecord {
	history := make([]UsageEventRecord, 0, len(entries))
	for _, entry := range entries {
		item := UsageEventRecord{
			Type:             entry.Type,
			Scope:            entry.Scope,
			Provider:         entry.Provider,
			Model:            entry.Model,
			RequestID:        entry.RequestID,
			Actor:            entry.Actor,
			Detail:           entry.Detail,
			AmountMicrosUSD:  entry.AmountMicrosUSD,
			AmountUSD:        formatUSD(entry.AmountMicrosUSD),
			PromptTokens:     entry.PromptTokens,
			CompletionTokens: entry.CompletionTokens,
			TotalTokens:      entry.TotalTokens,
		}
		if !entry.Timestamp.IsZero() {
			item.Timestamp = entry.Timestamp.UTC().Format(time.RFC3339Nano)
		}
		history = append(history, item)
	}
	return history
}

func usageFilterFromRequest(r *http.Request) governor.UsageFilter {
	query := r.URL.Query()
	return governor.UsageFilter{
		Key:      query.Get("key"),
		Scope:    query.Get("scope"),
		Provider: query.Get("provider"),
	}
}
