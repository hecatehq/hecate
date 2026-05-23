package governor

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/policy"
	"github.com/hecatehq/hecate/pkg/types"
)

type Governor interface {
	Check(ctx context.Context, req types.ChatRequest) error
	CheckRoute(ctx context.Context, req types.ChatRequest, decision types.RouteDecision, providerKind string, estimatedCostMicros int64) error
	RecordUsage(ctx context.Context, req types.ChatRequest, decision types.RouteDecision, usage types.Usage, costMicros int64) error
	UsageSummary(ctx context.Context, filter UsageFilter) (types.UsageSummary, error)
	RecentUsageEvents(ctx context.Context, limit int) ([]types.UsageEventEntry, error)
	Rewrite(req types.ChatRequest) types.ChatRequest
	RewriteResult(req types.ChatRequest) RewriteResult
}

type RewriteResult struct {
	Request        types.ChatRequest
	Applied        bool
	OriginalModel  string
	RewrittenModel string
	PolicyRuleID   string
	PolicyAction   string
	PolicyReason   string
}

// UsageFilter selects a usage bucket. In single-user mode the useful axes are
// global (one bucket for the whole gateway) and per provider (one bucket per
// upstream).
type UsageFilter struct {
	Key      string
	Scope    string
	Provider string
}

type StaticGovernor struct {
	config  config.GovernorConfig
	store   UsageStore
	history UsageEventStore
	rules   []policy.Rule
}

func NewStaticGovernor(cfg config.GovernorConfig, store UsageStore, historyStore UsageEventStore) *StaticGovernor {
	var history UsageEventStore
	if historyStore != nil {
		history = historyStore
	} else if candidate, ok := store.(UsageEventStore); ok {
		history = candidate
	}
	return &StaticGovernor{
		config:  cfg,
		store:   store,
		history: history,
		rules:   policy.FromConfig(cfg.PolicyRules),
	}
}

func (g *StaticGovernor) Check(_ context.Context, req types.ChatRequest) error {
	if g.config.DenyAll {
		return denyPolicyError("requests are disabled by policy")
	}

	promptEstimate := 0
	for _, msg := range req.Messages {
		promptEstimate += len(msg.Content) / 4
	}
	if promptEstimate > g.config.MaxPromptTokens {
		return denyPolicyError(fmt.Sprintf("estimated prompt tokens %d exceed limit %d", promptEstimate, g.config.MaxPromptTokens))
	}

	if err := policy.EvaluateDeny(g.rules, policy.BuildRequestSubject(req)); err != nil {
		return err
	}

	return nil
}

func (g *StaticGovernor) CheckRoute(_ context.Context, req types.ChatRequest, decision types.RouteDecision, providerKind string, estimatedCostMicros int64) error {
	if len(g.config.AllowedProviders) > 0 && !slices.Contains(g.config.AllowedProviders, decision.Provider) {
		return denyPolicyError(fmt.Sprintf("provider %q is not allowed by policy", decision.Provider))
	}
	if slices.Contains(g.config.DeniedProviders, decision.Provider) {
		return denyPolicyError(fmt.Sprintf("provider %q is denied by policy", decision.Provider))
	}

	model := decision.Model
	if len(g.config.AllowedModels) > 0 && !slices.Contains(g.config.AllowedModels, model) {
		return denyPolicyError(fmt.Sprintf("model %q is not allowed by policy", model))
	}
	if slices.Contains(g.config.DeniedModels, model) {
		return denyPolicyError(fmt.Sprintf("model %q is denied by policy", model))
	}

	if len(g.config.AllowedProviderKinds) > 0 && !slices.Contains(g.config.AllowedProviderKinds, providerKind) {
		return denyPolicyError(fmt.Sprintf("provider kind %q is not allowed by policy", providerKind))
	}
	switch g.config.RouteMode {
	case "local_only":
		if providerKind != "local" {
			return denyPolicyError(fmt.Sprintf("route mode %q denies provider kind %q", g.config.RouteMode, providerKind))
		}
	case "cloud_only":
		if providerKind != "cloud" {
			return denyPolicyError(fmt.Sprintf("route mode %q denies provider kind %q", g.config.RouteMode, providerKind))
		}
	}

	if err := policy.EvaluateDeny(g.rules, policy.BuildRouteSubject(req, decision, providerKind, estimatedCostMicros)); err != nil {
		return err
	}

	return nil
}

func (g *StaticGovernor) RecordUsage(ctx context.Context, req types.ChatRequest, decision types.RouteDecision, usage types.Usage, costMicros int64) error {
	if g.store == nil && g.history == nil {
		return nil
	}
	event := UsageEvent{
		UsageKey:   g.usageKeyForRequest(req, decision),
		RequestID:  req.RequestID,
		Provider:   decision.Provider,
		Model:      decision.Model,
		Usage:      usage,
		CostMicros: costMicros,
		OccurredAt: time.Now().UTC(),
	}
	if g.store != nil && costMicros > 0 {
		state, err := g.store.RecordUsage(ctx, event)
		if err != nil {
			return fmt.Errorf("record usage for provider %q: %w", decision.Provider, err)
		}
		_ = state
	}
	if err := g.appendUsageEvent(ctx, UsageHistoryEvent{
		Key:              event.UsageKey,
		Type:             "usage",
		Scope:            g.config.UsageScope,
		Provider:         decision.Provider,
		Model:            decision.Model,
		RequestID:        req.RequestID,
		AmountMicrosUSD:  costMicros,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usage.TotalTokens,
		OccurredAt:       event.OccurredAt,
	}); err != nil {
		return fmt.Errorf("append usage event: %w", err)
	}
	return nil
}

func (g *StaticGovernor) UsageSummary(ctx context.Context, filter UsageFilter) (types.UsageSummary, error) {
	resolved := g.resolveUsageFilter(filter)
	summary := types.UsageSummary{
		Key:      resolved.Key,
		Scope:    resolved.Scope,
		Provider: resolved.Provider,
		Backend:  g.config.UsageBackend,
	}
	if g.store == nil {
		if summary.Backend == "" {
			summary.Backend = "none"
		}
		return summary, nil
	}

	state, _, err := g.store.Snapshot(ctx, resolved.Key)
	if err != nil {
		return types.UsageSummary{}, fmt.Errorf("read usage state: %w", err)
	}
	summary.UsedMicrosUSD = state.UsedMicrosUSD
	return summary, nil
}

func (g *StaticGovernor) RecentUsageEvents(ctx context.Context, limit int) ([]types.UsageEventEntry, error) {
	if g.history == nil {
		return nil, nil
	}
	events, err := g.history.ListRecentEvents(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]types.UsageEventEntry, 0, len(events))
	for _, event := range events {
		out = append(out, types.UsageEventEntry{
			Type:             event.Type,
			Scope:            event.Scope,
			Provider:         event.Provider,
			Model:            event.Model,
			RequestID:        event.RequestID,
			Actor:            event.Actor,
			Detail:           event.Detail,
			AmountMicrosUSD:  event.AmountMicrosUSD,
			PromptTokens:     event.PromptTokens,
			CompletionTokens: event.CompletionTokens,
			TotalTokens:      event.TotalTokens,
			Timestamp:        event.OccurredAt,
		})
	}
	return out, nil
}

func (g *StaticGovernor) Rewrite(req types.ChatRequest) types.ChatRequest {
	return g.RewriteResult(req).Request
}

func (g *StaticGovernor) RewriteResult(req types.ChatRequest) RewriteResult {
	result := RewriteResult{
		Request:       req,
		OriginalModel: req.Model,
	}
	if eval, rewritten, ok := policy.EvaluateRewrite(g.rules, policy.BuildRequestSubject(req)); ok {
		result.Request.Model = rewritten
		result.Applied = rewritten != req.Model
		result.RewrittenModel = rewritten
		if eval != nil {
			result.PolicyRuleID = eval.RuleID
			result.PolicyAction = eval.Action
			result.PolicyReason = eval.Reason
		}
		return result
	}

	if g.config.ModelRewriteTo == "" {
		return result
	}
	result.Request.Model = g.config.ModelRewriteTo
	result.Applied = g.config.ModelRewriteTo != req.Model
	result.RewrittenModel = g.config.ModelRewriteTo
	result.PolicyAction = policy.ActionRewriteModel
	result.PolicyReason = "configured model rewrite"
	return result
}

func denyPolicyError(message string) error {
	return &policy.Error{
		Evaluation: policy.Evaluation{
			Action:  policy.ActionDeny,
			Reason:  message,
			Message: message,
		},
	}
}

func (g *StaticGovernor) usageKeyForRequest(_ types.ChatRequest, decision types.RouteDecision) string {
	return g.resolveUsageFilter(UsageFilter{
		Scope:    g.config.UsageScope,
		Provider: decision.Provider,
	}).Key
}

func (g *StaticGovernor) resolveUsageFilter(filter UsageFilter) UsageFilter {
	if filter.Key != "" {
		if filter.Scope == "" {
			filter.Scope = "custom"
		}
		return filter
	}

	baseKey := g.config.UsageKey
	if baseKey == "" {
		baseKey = "global"
	}

	scope := filter.Scope
	if scope == "" {
		scope = g.config.UsageScope
	}
	if scope == "" {
		scope = "global"
	}

	provider := filter.Provider

	switch scope {
	case "provider":
		filter.Key = baseKey + ":provider:" + provider
	default:
		scope = "global"
		filter.Key = baseKey
	}

	filter.Scope = scope
	filter.Provider = provider
	return filter
}

func (g *StaticGovernor) appendUsageEvent(ctx context.Context, event UsageHistoryEvent) error {
	if g.history == nil {
		return nil
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	return g.history.AppendEvent(ctx, event)
}

func (g *StaticGovernor) usageEvents(ctx context.Context, key string, summary types.UsageSummary, limit int) ([]types.UsageEventEntry, error) {
	if g.history == nil {
		return nil, nil
	}

	events, err := g.history.ListEvents(ctx, key, limit)
	if err != nil {
		return nil, err
	}

	out := make([]types.UsageEventEntry, 0, len(events))
	for _, event := range events {
		out = append(out, types.UsageEventEntry{
			Type:             event.Type,
			Scope:            event.Scope,
			Provider:         firstNonEmpty(event.Provider, summary.Provider),
			Model:            event.Model,
			RequestID:        event.RequestID,
			Actor:            event.Actor,
			Detail:           event.Detail,
			AmountMicrosUSD:  event.AmountMicrosUSD,
			PromptTokens:     event.PromptTokens,
			CompletionTokens: event.CompletionTokens,
			TotalTokens:      event.TotalTokens,
			Timestamp:        event.OccurredAt,
		})
	}
	return out, nil
}

func (g *StaticGovernor) historyLimit() int {
	if g.config.UsageHistoryLimit <= 0 {
		return 20
	}
	return g.config.UsageHistoryLimit
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
