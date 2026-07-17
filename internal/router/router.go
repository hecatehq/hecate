package router

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hecatehq/hecate/internal/catalog"
	"github.com/hecatehq/hecate/internal/modelcaps"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/internal/requestscope"
	"github.com/hecatehq/hecate/pkg/types"
)

type Router interface {
	Route(ctx context.Context, req types.ChatRequest) (types.RouteDecision, error)
	Fallbacks(ctx context.Context, req types.ChatRequest, current types.RouteDecision) []types.RouteDecision
}

type RuleRouter struct {
	defaultModel string
	catalog      catalog.Catalog
}

type routeCandidate struct {
	Provider         providers.Provider
	ProviderInstance types.ProviderInstanceIdentity
	Name             string
	Kind             providers.Kind
	Model            string
	Reason           string
	Status           string
	HealthReason     string
	Healthy          bool
	LastLatencyMS    int64
	Stability        int64
}

func NewRuleRouter(defaultModel string, catalog catalog.Catalog) *RuleRouter {
	return &RuleRouter{
		defaultModel: defaultModel,
		catalog:      catalog,
	}
}

func (r *RuleRouter) Route(ctx context.Context, req types.ChatRequest) (types.RouteDecision, error) {
	scope := requestscope.Normalize(req.Scope)
	model := req.Model
	if model == "" {
		model = r.defaultModel
	}
	if model == "" {
		return types.RouteDecision{}, fmt.Errorf("no model available for routing")
	}

	if scope.ProviderHint != "" {
		return r.routeExplicitProvider(ctx, req, scope.ProviderHint, model)
	}

	var (
		candidate routeCandidate
		ok        bool
	)
	if req.Model != "" {
		candidate, ok = r.selectCandidate(r.explicitModelCandidates(ctx, req, model))
		if !ok {
			return types.RouteDecision{}, fmt.Errorf("no provider supports explicit model %q", model)
		}
	} else {
		candidate, ok = r.selectCandidate(r.defaultCandidates(ctx, req, model))
		if !ok {
			return types.RouteDecision{}, fmt.Errorf("no provider available for default routing")
		}
	}

	return types.RouteDecision{
		Provider:         candidate.Name,
		ProviderKind:     string(candidate.Kind),
		ProviderInstance: candidate.ProviderInstance,
		Model:            candidate.Model,
		Reason:           candidate.Reason,
	}, nil
}

func (r *RuleRouter) Fallbacks(ctx context.Context, req types.ChatRequest, current types.RouteDecision) []types.RouteDecision {
	if requestscope.Normalize(req.Scope).ProviderHint != "" {
		return nil
	}

	explicitModel := req.Model != ""
	seen := map[string]struct{}{
		current.Provider + "/" + current.Model: {},
	}
	ordered := r.orderedProviders()
	out := make([]types.RouteDecision, 0, len(ordered))

	for _, provider := range ordered {
		if provider.Name == current.Provider {
			continue
		}
		if !isRoutableProvider(provider) {
			continue
		}

		model := ""
		if explicitModel {
			if !supportsModel(provider, req.Model) {
				continue
			}
			model = req.Model
		} else {
			model = provider.DefaultModel
			if model == "" {
				continue
			}
		}
		if !supportsRequest(provider, model, req) {
			continue
		}

		key := provider.Name + "/" + model
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		out = append(out, types.RouteDecision{
			Provider:         provider.Name,
			ProviderKind:     string(provider.Kind),
			ProviderInstance: provider.ProviderInstance,
			Model:            model,
			Reason:           routeReasonForHealth(current.Reason+"_failover", provider.Status),
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		return candidateRank(routeStatusFromReason(out[i].Reason)) < candidateRank(routeStatusFromReason(out[j].Reason))
	})

	return out
}

func (r *RuleRouter) routeExplicitProvider(ctx context.Context, req types.ChatRequest, explicitProvider, model string) (types.RouteDecision, error) {
	entry, ok, ambiguous := r.lookupProvider(ctx, explicitProvider, req.Requirements.ExactProvider)
	if ambiguous {
		return types.RouteDecision{}, fmt.Errorf("provider %q matches multiple configured providers", explicitProvider)
	}
	if !ok {
		return types.RouteDecision{}, fmt.Errorf("provider %q not found", explicitProvider)
	}

	routedModel := model
	reason := "pinned_provider"
	if req.Model != "" {
		reason = "pinned_provider_model"
		if !supportsModel(entry, model) {
			return types.RouteDecision{}, fmt.Errorf("provider %q does not support explicit model %q", explicitProvider, model)
		}
	} else {
		routedModel = entry.DefaultModel
		if routedModel == "" {
			return types.RouteDecision{}, fmt.Errorf("provider %q has no default model for routing", explicitProvider)
		}
	}
	if req.Requirements.ProviderInstance.Valid() && entry.ProviderInstance != req.Requirements.ProviderInstance {
		return types.RouteDecision{}, fmt.Errorf("provider %q configuration changed during image admission", explicitProvider)
	}
	if !supportsRequest(entry, routedModel, req) {
		return types.RouteDecision{}, fmt.Errorf("provider %q model %q does not satisfy required %s support", explicitProvider, routedModel, requestCapabilityLabel(req.Requirements))
	}

	return types.RouteDecision{
		Provider:         entry.Name,
		ProviderKind:     string(entry.Kind),
		ProviderInstance: entry.ProviderInstance,
		Model:            routedModel,
		Reason:           routeReasonForHealth(reason, entry.Status),
	}, nil
}

func (r *RuleRouter) lookupProvider(ctx context.Context, provider string, exact bool) (catalog.Entry, bool, bool) {
	entry, ok := r.catalog.Get(ctx, provider)
	if ok || exact {
		return entry, ok, false
	}

	entries := r.catalog.Snapshot(ctx)
	identities := make([]catalog.ProviderIdentity, 0, len(entries))
	for _, candidate := range entries {
		identities = append(identities, catalog.EntryProviderIdentity(candidate))
	}
	resolution := catalog.ResolveProviderIdentity(identities, provider)
	if resolution.Ambiguous {
		return catalog.Entry{}, false, true
	}
	if resolution.Found {
		return entries[resolution.Index], true, false
	}
	return catalog.Entry{}, false, false
}

func (r *RuleRouter) explicitModelCandidates(ctx context.Context, req types.ChatRequest, model string) []routeCandidate {
	entries := orderedEntriesByName(r.catalog.Snapshot(ctx))
	candidates := make([]routeCandidate, 0, len(entries)+2)

	for _, entry := range entries {
		if !isRoutableCandidate(entry, model) || !supportsRequest(entry, model, req) {
			continue
		}
		candidates = append(candidates, newRouteCandidate(entry, model, "requested_model"))
	}
	return orderCandidates(dedupeCandidates(candidates))
}

func (r *RuleRouter) defaultCandidates(ctx context.Context, req types.ChatRequest, model string) []routeCandidate {
	entries := orderedEntriesByName(r.catalog.Snapshot(ctx))
	candidates := make([]routeCandidate, 0, len(entries))
	for _, entry := range entries {
		if !isRoutableProvider(entry) {
			continue
		}

		if providerModel := entry.DefaultModel; providerModel != "" {
			if !supportsRequest(entry, providerModel, req) {
				continue
			}
			candidates = append(candidates, newRouteCandidate(entry, providerModel, "provider_default_model"))
			continue
		}
		if supportsModel(entry, model) && supportsRequest(entry, model, req) {
			candidates = append(candidates, newRouteCandidate(entry, model, "global_default_model"))
		}
	}

	return orderCandidates(dedupeCandidates(candidates))
}

func (r *RuleRouter) selectCandidate(candidates []routeCandidate) (routeCandidate, bool) {
	if len(candidates) == 0 {
		return routeCandidate{}, false
	}
	return candidates[0], true
}

func dedupeCandidates(candidates []routeCandidate) []routeCandidate {
	seen := make(map[string]struct{}, len(candidates))
	out := make([]routeCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Provider == nil || candidate.Model == "" {
			continue
		}
		key := candidate.Name + "/" + candidate.Model
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func supportsModel(entry catalog.Entry, model string) bool {
	for _, candidate := range entry.Models {
		if candidate == model {
			return true
		}
	}
	return entry.DefaultModel != "" && entry.DefaultModel == model
}

func supportsRequest(entry catalog.Entry, model string, req types.ChatRequest) bool {
	if req.Requirements.ProviderInstance.Valid() && entry.ProviderInstance != req.Requirements.ProviderInstance {
		return false
	}
	if !req.Requirements.ImageInput && !req.Requirements.ToolCalling {
		return true
	}
	providerCap := types.ModelCapabilities{}
	if entry.ModelCapabilities != nil {
		providerCap = entry.ModelCapabilities[model]
	}
	capability := modelcaps.ResolveWithProviderCapability(entry.ProviderFamily, string(entry.Kind), model, entry.DiscoverySource, providerCap)
	if req.Requirements.ImageInput && !modelcaps.ImageCapable(capability) {
		return false
	}
	return !req.Requirements.ToolCalling || modelcaps.ToolCapable(capability)
}

func requestCapabilityLabel(requirements types.ChatRequestRequirements) string {
	switch {
	case requirements.ImageInput && requirements.ToolCalling:
		return "image-input and tool-calling"
	case requirements.ToolCalling:
		return "tool-calling"
	default:
		return "image-input"
	}
}

func (r *RuleRouter) orderedProviders() []catalog.Entry {
	entries := orderedEntriesByName(r.catalog.Snapshot(context.Background()))
	candidates := make([]catalog.Entry, 0, len(entries))
	seen := make(map[string]struct{}, len(entries)+2)
	appendProvider := func(entry catalog.Entry) {
		if _, ok := seen[entry.Name]; ok {
			return
		}
		seen[entry.Name] = struct{}{}
		candidates = append(candidates, entry)
	}

	for _, entry := range entries {
		appendProvider(entry)
	}

	return candidates
}

func orderedEntriesByName(entries []catalog.Entry) []catalog.Entry {
	out := append([]catalog.Entry(nil), entries...)
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func newRouteCandidate(entry catalog.Entry, model, reason string) routeCandidate {
	return routeCandidate{
		Provider:         entry.Provider,
		ProviderInstance: entry.ProviderInstance,
		Name:             entry.Name,
		Kind:             entry.Kind,
		Model:            model,
		Reason:           routeReasonForHealth(reason, entry.Status),
		Status:           entry.Status,
		HealthReason:     entry.HealthReason,
		Healthy:          entry.Healthy,
		LastLatencyMS:    entry.LastLatencyMS,
		Stability:        stabilityPenalty(entry),
	}
}

func isRoutableCandidate(entry catalog.Entry, model string) bool {
	return isRoutableProvider(entry) && supportsModel(entry, model)
}

func isRoutableProvider(entry catalog.Entry) bool {
	status := strings.TrimSpace(entry.Status)
	if status == string(providers.HealthStatusOpen) {
		return false
	}
	if entry.Healthy {
		return true
	}
	return status == string(providers.HealthStatusHalfOpen)
}

func routeReasonForHealth(baseReason, status string) string {
	switch status {
	case string(providers.HealthStatusHalfOpen):
		return baseReason + "_half_open_recovery"
	case string(providers.HealthStatusDegraded):
		return baseReason + "_degraded"
	default:
		return baseReason
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func candidateRank(status string) int {
	switch status {
	case "", string(providers.HealthStatusHealthy):
		return 0
	case string(providers.HealthStatusHalfOpen):
		return 1
	case string(providers.HealthStatusDegraded):
		return 2
	default:
		return 3
	}
}

func orderCandidates(candidates []routeCandidate) []routeCandidate {
	sort.SliceStable(candidates, func(i, j int) bool {
		leftRank := candidateRank(candidates[i].Status)
		rightRank := candidateRank(candidates[j].Status)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		if candidates[i].Stability != candidates[j].Stability {
			return candidates[i].Stability < candidates[j].Stability
		}
		leftLatency := candidates[i].LastLatencyMS
		rightLatency := candidates[j].LastLatencyMS
		if leftLatency > 0 && rightLatency > 0 && leftLatency != rightLatency {
			return leftLatency < rightLatency
		}
		if candidates[i].Name != candidates[j].Name {
			return strings.ToLower(candidates[i].Name) < strings.ToLower(candidates[j].Name)
		}
		return false
	})
	return candidates
}

func stabilityPenalty(entry catalog.Entry) int64 {
	if entry.ConsecutiveFailures > 0 {
		return int64(entry.ConsecutiveFailures) * 1_000_000
	}
	return entry.RateLimits*100_000 + entry.Timeouts*10_000 + entry.ServerErrors*1_000 + entry.TotalFailures
}

func routeStatusFromReason(reason string) string {
	switch {
	case strings.HasSuffix(reason, "_half_open_recovery"):
		return string(providers.HealthStatusHalfOpen)
	case strings.HasSuffix(reason, "_degraded"):
		return string(providers.HealthStatusDegraded)
	default:
		return string(providers.HealthStatusHealthy)
	}
}
