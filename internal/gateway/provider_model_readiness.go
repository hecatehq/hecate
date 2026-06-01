package gateway

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hecatehq/hecate/internal/catalog"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/pkg/types"
)

type ProviderModelReadiness struct {
	Provider              string
	MatchedProvider       string
	Model                 string
	Ready                 bool
	Reason                string
	Message               string
	OperatorAction        string
	RoutingReady          bool
	ProviderStatus        string
	ProviderBlockedReason string
	SuggestedModels       []string
}

func (r ProviderModelReadiness) ToModelReadiness() types.ModelReadiness {
	return types.ModelReadiness{
		Provider:              r.Provider,
		MatchedProvider:       r.MatchedProvider,
		Model:                 r.Model,
		Ready:                 r.Ready,
		Status:                providerModelReadinessStatus(r),
		Reason:                r.Reason,
		Message:               r.Message,
		OperatorAction:        r.OperatorAction,
		RoutingReady:          r.RoutingReady,
		ProviderStatus:        r.ProviderStatus,
		ProviderBlockedReason: r.ProviderBlockedReason,
		SuggestedModels:       append([]string(nil), r.SuggestedModels...),
	}
}

func providerModelReadinessStatus(readiness ProviderModelReadiness) string {
	if readiness.Ready {
		return "ok"
	}
	if readiness.ProviderStatus == "" && readiness.Reason == "" {
		return "unknown"
	}
	return "blocked"
}

func providerModelReadiness(entries []catalog.Entry, provider, model string) ProviderModelReadiness {
	entries = sortedCatalogEntries(entries)
	out := ProviderModelReadiness{
		Provider: provider,
		Model:    model,
	}
	if model == "" {
		if provider == "" {
			out.Provider = "auto"
		}
		out.Reason = "model_required"
		out.Message = "No model is selected."
		out.OperatorAction = "Choose a discovered model before sending a chat message."
		out.SuggestedModels = suggestedModels(entries, provider)
		return out
	}
	if provider != "" {
		for _, entry := range entries {
			if strings.EqualFold(entry.Name, provider) {
				return providerModelReadinessForEntry(entry, entry.Name, model)
			}
		}
		out.Reason = "provider_missing"
		out.Message = fmt.Sprintf("Provider %q is not configured.", provider)
		out.OperatorAction = "Choose Auto routing, select a configured provider, or add this provider in Connections."
		out.SuggestedModels = suggestedModels(entries, "")
		return out
	}

	var blocked []catalog.Entry
	for _, entry := range entries {
		if !providerModelAvailable(entry, model) {
			continue
		}
		if reason := providerRoutingBlockedReason(entry); reason != "" {
			blocked = append(blocked, entry)
			continue
		}
		out.Provider = "auto"
		out.MatchedProvider = entry.Name
		out.Ready = true
		out.Reason = "auto_route_available"
		out.Message = fmt.Sprintf("Auto routing can use provider %q for model %q.", entry.Name, model)
		out.RoutingReady = true
		out.ProviderStatus = entry.Status
		return out
	}
	if len(blocked) > 0 {
		entry := blocked[0]
		reason := providerRoutingBlockedReason(entry)
		out.Provider = "auto"
		out.MatchedProvider = entry.Name
		out.Reason = "provider_not_ready"
		out.Message = fmt.Sprintf("Provider %q has model %q but is not routable.", entry.Name, model)
		out.OperatorAction = providerModelReadinessOperatorAction(entry, reason, model)
		out.ProviderStatus = entry.Status
		out.ProviderBlockedReason = reason
		out.SuggestedModels = suggestedModels(entries, "")
		return out
	}
	out.Provider = "auto"
	out.Reason = "model_not_discovered"
	out.Message = fmt.Sprintf("No routable provider reports model %q.", model)
	out.OperatorAction = "Pick one of the discovered models, refresh provider discovery, or load this model in a local provider."
	out.SuggestedModels = suggestedModels(entries, "")
	return out
}

func providerModelReadinessForEntry(entry catalog.Entry, provider, model string) ProviderModelReadiness {
	out := ProviderModelReadiness{
		Provider:        provider,
		MatchedProvider: entry.Name,
		Model:           model,
		ProviderStatus:  entry.Status,
	}
	if reason := providerRoutingBlockedReason(entry); reason != "" {
		out.Reason = "provider_not_ready"
		out.Message = fmt.Sprintf("Provider %q is not routable for model %q.", entry.Name, model)
		out.OperatorAction = providerModelReadinessOperatorAction(entry, reason, model)
		out.ProviderBlockedReason = reason
		out.SuggestedModels = suggestedModels([]catalog.Entry{entry}, "")
		return out
	}
	out.RoutingReady = true
	if providerModelAvailable(entry, model) {
		out.Ready = true
		out.Reason = "model_available"
		out.Message = fmt.Sprintf("Provider %q reports model %q.", entry.Name, model)
		return out
	}
	out.Reason = "model_not_discovered"
	out.Message = fmt.Sprintf("Provider %q does not report model %q.", entry.Name, model)
	out.OperatorAction = providerModelReadinessOperatorAction(entry, out.Reason, model)
	out.SuggestedModels = suggestedModels([]catalog.Entry{entry}, "")
	return out
}

func providerModelAvailable(entry catalog.Entry, model string) bool {
	for _, candidate := range entry.Models {
		if strings.EqualFold(candidate, model) {
			return true
		}
	}
	return false
}

func suggestedModels(entries []catalog.Entry, provider string) []string {
	out := make([]string, 0, 8)
	for _, entry := range entries {
		if provider != "" && !strings.EqualFold(entry.Name, provider) {
			continue
		}
		out = appendUniqueStrings(out, entry.Models...)
		if len(out) >= 5 {
			break
		}
	}
	if len(out) > 5 {
		out = out[:5]
	}
	sort.Strings(out)
	return out
}

func sortedCatalogEntries(entries []catalog.Entry) []catalog.Entry {
	if len(entries) == 0 {
		return nil
	}
	out := append([]catalog.Entry(nil), entries...)
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func appendUniqueStrings(out []string, values ...string) []string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		exists := false
		for _, existing := range out {
			if strings.EqualFold(existing, value) {
				exists = true
				break
			}
		}
		if !exists {
			out = append(out, value)
		}
	}
	return out
}

func providerModelReadinessOperatorAction(entry catalog.Entry, reason, model string) string {
	switch reason {
	case "provider_disabled":
		return "Enable this provider or choose Auto routing."
	case "credential_missing":
		return "Add or rotate this provider's API key, then retry this model."
	case "provider_rate_limited":
		return "Wait for cooldown, switch provider, or choose Auto routing."
	case "circuit_open", "provider_unhealthy":
		return "Inspect provider health, fix the upstream issue, then refresh provider status."
	case "no_models":
		return "Start the provider and pull or load at least one model."
	case "model_not_discovered":
		if entry.Kind == providers.KindLocal {
			return fmt.Sprintf("Pull or load %q locally, refresh provider status, or pick a discovered model.", model)
		}
		return "Pick a discovered model or confirm this account can access the requested model."
	default:
		return providerReadinessOperatorAction("routing", reason)
	}
}
