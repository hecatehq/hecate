package gateway

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hecate/agent-runtime/internal/catalog"
	"github.com/hecate/agent-runtime/internal/providers"
	"github.com/hecate/agent-runtime/pkg/types"
)

func (s *Service) ListModels(ctx context.Context) (*ModelsResult, error) {
	seen := make(map[string]struct{})
	modelsOut := make([]types.ModelInfo, 0, 16)

	for _, entry := range s.catalog.Snapshot(ctx) {
		for _, modelID := range entry.Models {
			key := entry.Name + "/" + modelID
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}

			modelsOut = append(modelsOut, types.ModelInfo{
				ID:              modelID,
				Provider:        entry.Name,
				Kind:            string(entry.Kind),
				OwnedBy:         entry.Name,
				Default:         modelID == entry.DefaultModel,
				DiscoverySource: entry.DiscoverySource,
				Readiness:       providerModelReadinessForEntry(entry, entry.Name, modelID).ToModelReadiness(),
			})
		}
	}

	return &ModelsResult{Models: modelsOut}, nil
}

func (s *Service) ProviderStatus(ctx context.Context) (*ProviderStatusResult, error) {
	entries := s.catalog.Snapshot(ctx)
	statuses := make([]types.ProviderStatus, 0, len(entries))
	for _, entry := range entries {
		status := types.ProviderStatus{
			Name:                entry.Name,
			Kind:                string(entry.Kind),
			BaseURL:             entry.BaseURL,
			CredentialState:     entry.CredentialState,
			CredentialReady:     providerCredentialReady(entry.CredentialState),
			Healthy:             entry.Healthy,
			Status:              entry.Status,
			RoutingReady:        providerRoutingReady(entry),
			RoutingBlocked:      providerRoutingBlockedReason(entry),
			DefaultModel:        entry.DefaultModel,
			Models:              append([]string(nil), entry.Models...),
			DiscoverySource:     entry.DiscoverySource,
			LastError:           entry.LastError,
			LastErrorClass:      entry.HealthReason,
			LastLatencyMS:       entry.LastLatencyMS,
			ConsecutiveFailures: entry.ConsecutiveFailures,
			TotalSuccesses:      entry.TotalSuccesses,
			TotalFailures:       entry.TotalFailures,
			Timeouts:            entry.Timeouts,
			ServerErrors:        entry.ServerErrors,
			RateLimits:          entry.RateLimits,
			Error:               entry.Error,
		}
		status.ReadinessChecks = providerReadinessChecks(entry)
		status.Readiness = providerReadinessSummary(entry, status.ReadinessChecks)
		if entry.RefreshedAt != "" {
			if ts, err := time.Parse(time.RFC3339, entry.RefreshedAt); err == nil {
				status.RefreshedAt = ts
			}
		}
		if entry.LastCheckedAt != "" {
			if ts, err := time.Parse(time.RFC3339, entry.LastCheckedAt); err == nil {
				status.LastCheckedAt = ts
			}
		}
		if entry.OpenUntil != "" {
			if ts, err := time.Parse(time.RFC3339, entry.OpenUntil); err == nil {
				status.OpenUntil = ts
			}
		}
		statuses = append(statuses, status)
	}

	return &ProviderStatusResult{Providers: statuses}, nil
}

func (s *Service) ProviderModelReadiness(ctx context.Context, provider, model string) (*ProviderModelReadinessResult, error) {
	model = strings.TrimSpace(model)
	provider = strings.TrimSpace(provider)
	if strings.EqualFold(provider, "auto") {
		provider = ""
	}
	readiness := providerModelReadiness(s.catalog.Snapshot(ctx), provider, model)
	return &ProviderModelReadinessResult{Readiness: readiness}, nil
}

func (s *Service) ProviderHealthHistory(ctx context.Context, provider string, limit int) (*ProviderHealthHistoryResult, error) {
	if s.providerHistory == nil {
		return &ProviderHealthHistoryResult{}, nil
	}
	records, err := s.providerHistory.List(ctx, providers.HealthHistoryFilter{
		Provider: provider,
		Limit:    limit,
	})
	if err != nil {
		return nil, err
	}
	kindByProvider := make(map[string]string, 8)
	for _, entry := range s.catalog.Snapshot(ctx) {
		kindByProvider[entry.Name] = string(entry.Kind)
	}
	out := make([]types.ProviderHealthHistoryEntry, 0, len(records))
	for _, record := range records {
		item := types.ProviderHealthHistoryEntry{
			Provider:            record.Provider,
			ProviderKind:        kindByProvider[record.Provider],
			Model:               record.Model,
			Event:               record.Event,
			Status:              record.Status,
			Available:           record.Available,
			Error:               record.Error,
			ErrorClass:          record.ErrorClass,
			Reason:              record.Reason,
			RouteReason:         record.RouteReason,
			RequestID:           record.RequestID,
			TraceID:             record.TraceID,
			PeerProvider:        record.PeerProvider,
			PeerModel:           record.PeerModel,
			PeerRouteReason:     record.PeerRouteReason,
			HealthStatus:        record.HealthStatus,
			PeerHealthStatus:    record.PeerHealthStatus,
			LatencyMS:           record.LatencyMS,
			ConsecutiveFailures: record.ConsecutiveFailures,
			TotalSuccesses:      record.TotalSuccesses,
			TotalFailures:       record.TotalFailures,
			Timeouts:            record.Timeouts,
			ServerErrors:        record.ServerErrors,
			RateLimits:          record.RateLimits,
			AttemptCount:        record.AttemptCount,
			EstimatedMicrosUSD:  record.EstimatedMicrosUSD,
		}
		if record.OpenUntil != "" {
			if ts, err := time.Parse(time.RFC3339Nano, record.OpenUntil); err == nil {
				item.OpenUntil = ts
			}
		}
		if record.Timestamp != "" {
			if ts, err := time.Parse(time.RFC3339Nano, record.Timestamp); err == nil {
				item.Timestamp = ts
			}
		}
		out = append(out, item)
	}
	return &ProviderHealthHistoryResult{Entries: out}, nil
}

func providerCredentialReady(state string) bool {
	switch state {
	case "", "unknown", "configured", "not_required":
		return true
	default:
		return false
	}
}

func providerRoutingReady(entry catalog.Entry) bool {
	return providerRoutingBlockedReason(entry) == ""
}

func providerRoutingBlockedReason(entry catalog.Entry) string {
	if entry.Status == "disabled" {
		return "provider_disabled"
	}
	if !providerCredentialReady(entry.CredentialState) {
		return "credential_missing"
	}
	if entry.Status == "open" {
		if entry.HealthReason == "rate_limit" {
			return "provider_rate_limited"
		}
		return "circuit_open"
	}
	if !entry.Healthy && entry.Status != "half_open" {
		if entry.HealthReason == "rate_limit" {
			return "provider_rate_limited"
		}
		return "provider_unhealthy"
	}
	if entry.DefaultModel == "" && len(entry.Models) == 0 {
		return "no_models"
	}
	return ""
}

func providerReadinessChecks(entry catalog.Entry) []types.ProviderReadinessCheck {
	return []types.ProviderReadinessCheck{
		providerCredentialCheck(entry),
		providerModelDiscoveryCheck(entry),
		providerHealthCheck(entry),
		providerRoutingCheck(entry),
	}
}

func providerReadinessSummary(entry catalog.Entry, checks []types.ProviderReadinessCheck) types.ReadinessSummary {
	if len(checks) == 0 {
		return types.ReadinessSummary{
			Status:         "unknown",
			Reason:         "unknown",
			Message:        "Provider readiness has not been checked yet.",
			OperatorAction: "Refresh provider status after configuration or startup settles.",
		}
	}

	var routingBlocked, firstBlocked, firstWarning, firstUnknown *types.ProviderReadinessCheck
	for i := range checks {
		check := &checks[i]
		if check.Name == "routing" && check.Status == "blocked" {
			routingBlocked = check
			continue
		}
		if check.Status == "blocked" && firstBlocked == nil {
			firstBlocked = check
		}
		if check.Status == "warning" && firstWarning == nil {
			firstWarning = check
		}
		if check.Status == "unknown" && firstUnknown == nil {
			firstUnknown = check
		}
	}
	if routingBlocked != nil {
		return providerReadinessSummaryFromCheck("blocked", *routingBlocked)
	}
	if firstBlocked != nil {
		return providerReadinessSummaryFromCheck("blocked", *firstBlocked)
	}
	if firstWarning != nil {
		return providerReadinessSummaryFromCheck("warning", *firstWarning)
	}
	if firstUnknown != nil {
		return providerReadinessSummaryFromCheck("unknown", *firstUnknown)
	}
	return types.ReadinessSummary{
		Status:  "ok",
		Reason:  "ready",
		Message: fmt.Sprintf("Provider %q is ready for routing.", entry.Name),
	}
}

func providerReadinessSummaryFromCheck(status string, check types.ProviderReadinessCheck) types.ReadinessSummary {
	return types.ReadinessSummary{
		Status:         status,
		Reason:         check.Reason,
		Message:        check.Message,
		OperatorAction: check.OperatorAction,
	}
}

func providerCredentialCheck(entry catalog.Entry) types.ProviderReadinessCheck {
	switch entry.CredentialState {
	case "configured":
		return providerReadinessCheck("credentials", "ok", "configured", "Credentials are configured.")
	case "not_required":
		return providerReadinessCheck("credentials", "ok", "not_required", "No credentials are required for this provider.")
	case "missing":
		return providerReadinessCheck("credentials", "blocked", "credential_missing", "Add credentials before Hecate can route requests to this provider.")
	case "", "unknown":
		return providerReadinessCheck("credentials", "unknown", "unknown", "Hecate could not determine credential state yet.")
	default:
		return providerReadinessCheck("credentials", "blocked", "credential_missing", fmt.Sprintf("Credential state %q blocks routing.", entry.CredentialState))
	}
}

func providerModelDiscoveryCheck(entry catalog.Entry) types.ProviderReadinessCheck {
	switch {
	case entry.Status == "disabled":
		return providerReadinessCheck("models", "blocked", "provider_disabled", "Model discovery is skipped while this provider is disabled.")
	case entry.DiscoverySource == "self_referential":
		return providerReadinessCheck("models", "blocked", "self_referential", "Model discovery is skipped because this provider points back to Hecate.")
	}

	count := entry.DiscoveredModelCount
	switch {
	case count > 0:
		return providerReadinessCheck("models", "ok", "models_discovered", fmt.Sprintf("%d model%s discovered.", count, pluralS(count)))
	case entry.DefaultModel != "":
		return providerReadinessCheck("models", "warning", "default_model_only", fmt.Sprintf("No models were discovered; Hecate can still try the configured default model %q.", entry.DefaultModel))
	case entry.LastError != "" || entry.Error != "":
		return providerReadinessCheck("models", "unknown", "discovery_failed", fmt.Sprintf("Model discovery failed before returning a model list: %s", firstNonEmpty(entry.LastError, entry.Error)))
	default:
		return providerReadinessCheck("models", "blocked", "no_models", "No models were discovered and no default model is configured.")
	}
}

func providerHealthCheck(entry catalog.Entry) types.ProviderReadinessCheck {
	switch entry.Status {
	case "healthy":
		if entry.Healthy {
			return providerReadinessCheck("health", "ok", "healthy", "Provider health checks are passing.")
		}
		return providerReadinessCheck("health", "unknown", providerReadinessHealthReason(entry.HealthReason), providerHealthPendingMessage(entry.HealthReason))
	case "degraded":
		if entry.Healthy {
			return providerReadinessCheck("health", "warning", providerReadinessHealthReason(entry.HealthReason), providerHealthDegradedMessage(entry.HealthReason))
		}
		if entry.HealthReason == "rate_limit" {
			return providerReadinessCheck("health", "blocked", "provider_rate_limited", "Provider is cooling down after an upstream rate limit.")
		}
		return providerReadinessCheck("health", "blocked", "provider_unhealthy", "Provider is degraded after recent failures.")
	case "half_open":
		return providerReadinessCheck("health", "warning", "recovery_probe", "Circuit is half-open; Hecate is testing recovery.")
	case "open":
		if entry.HealthReason == "rate_limit" {
			return providerReadinessCheck("health", "blocked", "provider_rate_limited", "Provider is cooling down after an upstream rate limit.")
		}
		return providerReadinessCheck("health", "blocked", "circuit_open", "Provider circuit is open after recent failures.")
	case "unhealthy":
		return providerReadinessCheck("health", "blocked", providerReadinessHealthReason(entry.HealthReason), providerHealthFailureMessage(entry.HealthReason))
	case "disabled":
		return providerReadinessCheck("health", "blocked", "provider_disabled", "Provider is disabled.")
	default:
		if !entry.Healthy {
			return providerReadinessCheck("health", "unknown", providerReadinessHealthReason(entry.HealthReason), providerHealthPendingMessage(entry.HealthReason))
		}
		return providerReadinessCheck("health", "unknown", firstNonEmpty(entry.Status, "unknown"), "Provider health has not been checked yet.")
	}
}

func providerReadinessHealthReason(reason string) string {
	switch reason {
	case "rate_limit":
		return "provider_rate_limited"
	case "latency":
		return "provider_slow"
	case "timeout", "server_error", "other":
		return "provider_unhealthy"
	case "":
		return "health_pending"
	default:
		return "provider_unhealthy"
	}
}

func providerHealthFailureMessage(reason string) string {
	switch reason {
	case "rate_limit":
		return "Provider is cooling down after an upstream rate limit."
	case "latency":
		return "Provider is slower than the configured degraded-latency threshold."
	case "":
		return "Provider health checks are failing."
	default:
		return fmt.Sprintf("Provider health checks are failing with error class %q.", reason)
	}
}

func providerHealthDegradedMessage(reason string) string {
	switch reason {
	case "latency":
		return "Provider is routable but slower than the configured degraded-latency threshold."
	case "":
		return "Provider is routable but currently degraded."
	default:
		return fmt.Sprintf("Provider is routable but degraded after error class %q.", reason)
	}
}

func providerHealthPendingMessage(reason string) string {
	if reason == "" {
		return "Provider health is still settling."
	}
	return fmt.Sprintf("Provider health is still settling after error class %q.", reason)
}

func providerRoutingCheck(entry catalog.Entry) types.ProviderReadinessCheck {
	if reason := providerRoutingBlockedReason(entry); reason != "" {
		return providerReadinessCheck("routing", "blocked", reason, providerRoutingBlockedMessage(reason))
	}
	return providerReadinessCheck("routing", "ok", "routable", "Provider is eligible for routing.")
}

func providerRoutingBlockedMessage(reason string) string {
	switch reason {
	case "credential_missing":
		return "Routing is blocked until credentials are configured."
	case "provider_disabled":
		return "Routing is blocked because this provider is disabled."
	case "provider_rate_limited":
		return "Routing is blocked while the provider cools down after a rate limit."
	case "circuit_open":
		return "Routing is blocked while the provider circuit is open."
	case "provider_unhealthy":
		return "Routing is blocked because provider health checks are failing."
	case "no_models":
		return "Routing is blocked because no models are available."
	default:
		return "Routing is blocked."
	}
}

func providerReadinessCheck(name, status, reason, message string) types.ProviderReadinessCheck {
	return types.ProviderReadinessCheck{
		Name:           name,
		Status:         status,
		Reason:         reason,
		Message:        message,
		OperatorAction: providerReadinessOperatorAction(name, reason),
	}
}

func providerReadinessOperatorAction(name, reason string) string {
	switch reason {
	case "configured", "not_required", "healthy", "routable", "models_discovered":
		return ""
	case "credential_missing":
		return "Add or rotate this provider's API key, then refresh provider status."
	case "provider_disabled":
		if name == "models" {
			return "Enable the provider before model discovery can run."
		}
		return "Enable the provider when you want Hecate to route to it."
	case "self_referential":
		return "Change the base URL so it points at the provider, not Hecate."
	case "discovery_failed":
		return "Check the endpoint and refresh provider status after the server is reachable."
	case "default_model_only":
		return "Send a test request or refresh discovery to confirm the default model is real."
	case "no_models":
		return "Start the provider and pull or load at least one model."
	case "provider_slow":
		return "Keep this provider enabled if the latency is acceptable, or route to a faster provider."
	case "provider_rate_limited":
		return "Wait for cooldown or temporarily route to another provider."
	case "provider_unhealthy":
		return "Inspect the latest health error and provider server logs, then refresh provider status."
	case "circuit_open":
		return "Wait for recovery or test the provider after fixing the upstream issue."
	case "recovery_probe":
		return "Retry once the half-open probe succeeds."
	case "health_pending", "unknown":
		return "Refresh provider status after configuration or startup settles."
	default:
		if name == "routing" {
			return "Open Connections to inspect readiness checks and repair the blocked dependency."
		}
		return ""
	}
}

func pluralS(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}
