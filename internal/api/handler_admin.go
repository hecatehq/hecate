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
		},
	})
}

// HandleMCPProbe is the dry-run discovery endpoint for MCP server
// configs. POST /v1/mcp/probe accepts a single MCPServerConfig-shaped
// body, brings the server up exactly the way an agent_loop run would
// (same secret resolution, same uncached spawn path), calls
// tools/list, and tears it down. Returns the upstream's tool catalog
// so operators can confirm a config before committing it to a task.
//
// Auth matches POST /v1/tasks (requireAny): if a principal can create
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
		Actor:      controlPlaneActor(r),
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
			controlPlaneActor(r),
			strings.TrimSpace(RequestIDFromContext(r.Context())),
			result.Run.Results,
		),
	})
}

func (h *Handler) HandleBudgetStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	result, err := h.service.BudgetStatusWithFilter(ctx, budgetFilterFromRequest(r))
	if err != nil {
		telemetry.Error(h.logger, ctx, "gateway.budget.status.failed",
			slog.String("event.name", "gateway.budget.status.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, renderBudgetStatusResponse(result))
}

func (h *Handler) HandleAccountSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	filter := budgetFilterFromRequest(r)
	result, err := h.service.AccountSummaryWithFilter(ctx, filter)
	if err != nil {
		telemetry.Error(h.logger, ctx, "gateway.accounts.summary.failed",
			slog.String("event.name", "gateway.accounts.summary.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}

	estimates := make([]AccountModelEstimateRecord, 0, len(result.Estimates))
	for _, estimate := range result.Estimates {
		estimates = append(estimates, AccountModelEstimateRecord{
			Provider:                        estimate.Provider,
			ProviderKind:                    estimate.ProviderKind,
			Model:                           estimate.Model,
			Default:                         estimate.Default,
			DiscoverySource:                 estimate.DiscoverySource,
			Priced:                          estimate.Priced,
			InputMicrosUSDPerMillionTokens:  estimate.InputMicrosUSDPerMillionTokens,
			OutputMicrosUSDPerMillionTokens: estimate.OutputMicrosUSDPerMillionTokens,
			EstimatedRemainingPromptTokens:  estimate.EstimatedRemainingPromptTokens,
			EstimatedRemainingOutputTokens:  estimate.EstimatedRemainingOutputTokens,
		})
	}

	WriteJSON(w, http.StatusOK, AccountSummaryResponse{
		Object: "account_summary",
		Data: AccountSummaryResponseItem{
			Account:   renderBudgetStatusRecord(result.Status),
			Estimates: estimates,
		},
	})
}

func (h *Handler) HandleRequestLedger(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	h.writeRequestLedger(w, r, ctx)
}

func (h *Handler) writeRequestLedger(w http.ResponseWriter, r *http.Request, ctx context.Context) {

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

	result, err := h.service.RequestLedger(ctx, limit)
	if err != nil {
		telemetry.Error(h.logger, ctx, "gateway.requests.ledger.failed",
			slog.String("event.name", "gateway.requests.ledger.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, RequestLedgerResponse{
		Object: "request_ledger",
		Data:   renderBudgetHistoryRecords(result.Entries),
	})
}

func (h *Handler) HandleBudgetReset(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var resetReq BudgetResetRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&resetReq); err != nil {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "request body must be valid JSON")
			return
		}
	}

	filter := budgetFilterFromRequest(r)
	if resetReq.Key != "" {
		filter.Key = resetReq.Key
	}
	if resetReq.Scope != "" {
		filter.Scope = resetReq.Scope
	}
	if resetReq.Provider != "" {
		filter.Provider = resetReq.Provider
	}

	result, err := h.service.ResetBudgetWithFilter(ctx, filter)
	if err != nil {
		telemetry.Error(h.logger, ctx, "gateway.budget.reset.failed",
			slog.String("event.name", "gateway.budget.reset.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, renderBudgetStatusResponse(result))
}

func (h *Handler) HandleBudgetTopUp(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var topUpReq BudgetTopUpRequest
	if !decodeJSON(w, r, &topUpReq) {
		return
	}
	if topUpReq.AmountMicrosUSD <= 0 {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "amount_micros_usd must be greater than zero")
		return
	}

	filter := budgetFilterFromMutation(topUpReq.Key, topUpReq.Scope, topUpReq.Provider)
	result, err := h.service.TopUpBudgetWithFilter(ctx, filter, topUpReq.AmountMicrosUSD)
	if err != nil {
		telemetry.Error(h.logger, ctx, "gateway.budget.top_up.failed",
			slog.String("event.name", "gateway.budget.top_up.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, renderBudgetStatusResponse(result))
}

func (h *Handler) HandleBudgetSetLimit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var balanceReq BudgetBalanceRequest
	if !decodeJSON(w, r, &balanceReq) {
		return
	}
	if balanceReq.BalanceMicrosUSD < 0 {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "balance_micros_usd must be zero or greater")
		return
	}

	filter := budgetFilterFromMutation(balanceReq.Key, balanceReq.Scope, balanceReq.Provider)
	result, err := h.service.SetBudgetBalanceWithFilter(ctx, filter, balanceReq.BalanceMicrosUSD)
	if err != nil {
		telemetry.Error(h.logger, ctx, "gateway.budget.limit_set.failed",
			slog.String("event.name", "gateway.budget.limit_set.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, renderBudgetStatusResponse(result))
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

func renderBudgetStatusResponse(result *gateway.BudgetStatusResult) BudgetStatusResponse {
	return BudgetStatusResponse{
		Object: "budget_status",
		Data:   renderBudgetStatusRecord(result.Status),
	}
}

func renderBudgetStatusRecord(status types.BudgetStatus) BudgetStatusResponseItem {
	warnings := make([]BudgetWarningRecord, 0, len(status.Warnings))
	for _, warning := range status.Warnings {
		warnings = append(warnings, BudgetWarningRecord{
			ThresholdPercent:   warning.ThresholdPercent,
			ThresholdMicrosUSD: warning.ThresholdMicrosUSD,
			BalanceMicrosUSD:   warning.BalanceMicrosUSD,
			AvailableMicrosUSD: warning.AvailableMicrosUSD,
			Triggered:          warning.Triggered,
		})
	}

	return BudgetStatusResponseItem{
		Key:                status.Key,
		Scope:              status.Scope,
		Provider:           status.Provider,
		Backend:            status.Backend,
		BalanceSource:      status.BalanceSource,
		DebitedMicrosUSD:   status.DebitedMicrosUSD,
		DebitedUSD:         formatUSD(status.DebitedMicrosUSD),
		CreditedMicrosUSD:  status.CreditedMicrosUSD,
		CreditedUSD:        formatUSD(status.CreditedMicrosUSD),
		BalanceMicrosUSD:   status.BalanceMicrosUSD,
		BalanceUSD:         formatUSD(status.BalanceMicrosUSD),
		AvailableMicrosUSD: status.AvailableMicrosUSD,
		AvailableUSD:       formatUSD(status.AvailableMicrosUSD),
		Enforced:           status.Enforced,
		Warnings:           warnings,
		History:            renderBudgetHistoryRecords(status.History),
	}
}

func renderBudgetHistoryRecords(entries []types.BudgetHistoryEntry) []BudgetHistoryRecord {
	history := make([]BudgetHistoryRecord, 0, len(entries))
	for _, entry := range entries {
		item := BudgetHistoryRecord{
			Type:              entry.Type,
			Scope:             entry.Scope,
			Provider:          entry.Provider,
			Model:             entry.Model,
			RequestID:         entry.RequestID,
			Actor:             entry.Actor,
			Detail:            entry.Detail,
			AmountMicrosUSD:   entry.AmountMicrosUSD,
			AmountUSD:         formatUSD(entry.AmountMicrosUSD),
			BalanceMicrosUSD:  entry.BalanceMicrosUSD,
			BalanceUSD:        formatUSD(entry.BalanceMicrosUSD),
			CreditedMicrosUSD: entry.CreditedMicrosUSD,
			CreditedUSD:       formatUSD(entry.CreditedMicrosUSD),
			DebitedMicrosUSD:  entry.DebitedMicrosUSD,
			DebitedUSD:        formatUSD(entry.DebitedMicrosUSD),
			PromptTokens:      entry.PromptTokens,
			CompletionTokens:  entry.CompletionTokens,
			TotalTokens:       entry.TotalTokens,
		}
		if !entry.Timestamp.IsZero() {
			item.Timestamp = entry.Timestamp.UTC().Format(time.RFC3339Nano)
		}
		history = append(history, item)
	}
	return history
}

func budgetFilterFromMutation(key, scope, provider string) governor.BudgetFilter {
	return governor.BudgetFilter{
		Key:      key,
		Scope:    scope,
		Provider: provider,
	}
}

func budgetFilterFromRequest(r *http.Request) governor.BudgetFilter {
	query := r.URL.Query()
	return governor.BudgetFilter{
		Key:      query.Get("key"),
		Scope:    query.Get("scope"),
		Provider: query.Get("provider"),
	}
}
