package governor

import (
	"context"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/controlplane"
	"github.com/hecatehq/hecate/pkg/types"
)

type ControlPlaneGovernor struct {
	config  config.GovernorConfig
	store   UsageStore
	history UsageEventStore
	cpStore controlplane.Store
}

func NewControlPlaneGovernor(cfg config.GovernorConfig, store UsageStore, historyStore UsageEventStore, cpStore controlplane.Store) *ControlPlaneGovernor {
	var history UsageEventStore
	if historyStore != nil {
		history = historyStore
	} else if candidate, ok := store.(UsageEventStore); ok {
		history = candidate
	}
	return &ControlPlaneGovernor{
		config:  cfg,
		store:   store,
		history: history,
		cpStore: cpStore,
	}
}

func (g *ControlPlaneGovernor) Check(ctx context.Context, req types.ChatRequest) error {
	return g.current(ctx).Check(ctx, req)
}

func (g *ControlPlaneGovernor) CheckRoute(ctx context.Context, req types.ChatRequest, decision types.RouteDecision, providerKind string, estimatedCostMicros int64) error {
	return g.current(ctx).CheckRoute(ctx, req, decision, providerKind, estimatedCostMicros)
}

func (g *ControlPlaneGovernor) RecordUsage(ctx context.Context, req types.ChatRequest, decision types.RouteDecision, usage types.Usage, costMicros int64) error {
	return g.current(ctx).RecordUsage(ctx, req, decision, usage, costMicros)
}

func (g *ControlPlaneGovernor) UsageSummary(ctx context.Context, filter UsageFilter) (types.UsageSummary, error) {
	return g.current(ctx).UsageSummary(ctx, filter)
}

func (g *ControlPlaneGovernor) RecentUsageEvents(ctx context.Context, limit int) ([]types.UsageEventEntry, error) {
	return g.current(ctx).RecentUsageEvents(ctx, limit)
}

func (g *ControlPlaneGovernor) Rewrite(req types.ChatRequest) types.ChatRequest {
	return g.current(context.Background()).Rewrite(req)
}

func (g *ControlPlaneGovernor) RewriteResult(req types.ChatRequest) RewriteResult {
	return g.current(context.Background()).RewriteResult(req)
}

func (g *ControlPlaneGovernor) current(ctx context.Context) *StaticGovernor {
	cfg := g.config
	if g.cpStore != nil {
		if state, err := g.cpStore.Snapshot(ctx); err == nil && len(state.PolicyRules) > 0 {
			cfg.PolicyRules = append(append([]config.PolicyRuleConfig(nil), cfg.PolicyRules...), state.PolicyRules...)
		}
	}
	return NewStaticGovernor(cfg, g.store, g.history)
}
