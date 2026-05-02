package governor

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/hecate/agent-runtime/internal/config"
	"github.com/hecate/agent-runtime/internal/policy"
	"github.com/hecate/agent-runtime/pkg/types"
)

type Governor interface {
	Check(ctx context.Context, req types.ChatRequest) error
	CheckRoute(ctx context.Context, req types.ChatRequest, decision types.RouteDecision, providerKind string, estimatedCostMicros int64) error
	RecordUsage(ctx context.Context, req types.ChatRequest, decision types.RouteDecision, usage types.Usage, costMicros int64) error
	BudgetStatus(ctx context.Context, filter BudgetFilter) (types.BudgetStatus, error)
	RecentBudgetHistory(ctx context.Context, limit int) ([]types.BudgetHistoryEntry, error)
	TopUpBudget(ctx context.Context, filter BudgetFilter, deltaMicros int64) error
	SetBudgetBalance(ctx context.Context, filter BudgetFilter, balanceMicros int64) error
	ResetBudget(ctx context.Context, filter BudgetFilter) error
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

// BudgetFilter selects a budget account. In single-user mode the only
// useful axes are global (one bucket for the whole gateway) and per
// provider (one bucket per upstream).
type BudgetFilter struct {
	Key      string
	Scope    string
	Provider string
}

type StaticGovernor struct {
	config  config.GovernorConfig
	store   AccountStore
	history BudgetHistoryStore
	rules   []policy.Rule
}

func NewStaticGovernor(cfg config.GovernorConfig, store AccountStore, historyStore BudgetHistoryStore) *StaticGovernor {
	var history BudgetHistoryStore
	if historyStore != nil {
		history = historyStore
	} else if candidate, ok := store.(BudgetHistoryStore); ok {
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

func (g *StaticGovernor) CheckRoute(ctx context.Context, req types.ChatRequest, decision types.RouteDecision, providerKind string, estimatedCostMicros int64) error {
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

	budgetKey := g.budgetKeyForRequest(req, decision)
	if g.store != nil {
		balance, enforced, err := g.effectiveBudgetBalance(ctx, budgetKey)
		if err != nil {
			return fmt.Errorf("read budget balance: %w", err)
		}
		if !enforced {
			return nil
		}
		if estimatedCostMicros > balance {
			return &BudgetExceededError{BalanceMicrosUSD: balance}
		}
	}

	return nil
}

func (g *StaticGovernor) RecordUsage(ctx context.Context, req types.ChatRequest, decision types.RouteDecision, usage types.Usage, costMicros int64) error {
	if g.store == nil || costMicros <= 0 {
		return nil
	}
	event := UsageEvent{
		BudgetKey:  g.budgetKeyForRequest(req, decision),
		RequestID:  req.RequestID,
		Provider:   decision.Provider,
		Model:      decision.Model,
		Usage:      usage,
		CostMicros: costMicros,
		OccurredAt: time.Now().UTC(),
	}
	if _, exists, err := g.store.Snapshot(ctx, event.BudgetKey); err != nil {
		return fmt.Errorf("read budget account before debit: %w", err)
	} else if !exists {
		if g.config.MaxTotalBudgetMicros <= 0 {
			return nil
		}
		if _, err := g.store.Credit(ctx, event.BudgetKey, g.config.MaxTotalBudgetMicros); err != nil {
			return fmt.Errorf("initialize budget account: %w", err)
		}
	}
	account, err := g.store.Debit(ctx, event)
	if err != nil {
		return fmt.Errorf("record budget usage for provider %q: %w", decision.Provider, err)
	}
	if err := g.appendBudgetEvent(ctx, BudgetEvent{
		Key:               event.BudgetKey,
		Type:              "debit",
		Scope:             g.config.BudgetScope,
		Provider:          decision.Provider,
		Model:             decision.Model,
		RequestID:         req.RequestID,
		AmountMicrosUSD:   costMicros,
		BalanceMicrosUSD:  account.BalanceMicrosUSD,
		CreditedMicrosUSD: account.CreditedMicrosUSD,
		DebitedMicrosUSD:  account.DebitedMicrosUSD,
		PromptTokens:      usage.PromptTokens,
		CompletionTokens:  usage.CompletionTokens,
		TotalTokens:       usage.TotalTokens,
		OccurredAt:        event.OccurredAt,
	}); err != nil {
		return fmt.Errorf("append budget usage history: %w", err)
	}
	return nil
}

func (g *StaticGovernor) BudgetStatus(ctx context.Context, filter BudgetFilter) (types.BudgetStatus, error) {
	resolved := g.resolveBudgetFilter(filter)
	status := types.BudgetStatus{
		Key:      resolved.Key,
		Scope:    resolved.Scope,
		Provider: resolved.Provider,
		Backend:  g.config.BudgetBackend,
	}
	if g.store == nil {
		if status.Backend == "" {
			status.Backend = "none"
		}
		status.BalanceMicrosUSD = g.config.MaxTotalBudgetMicros
		status.AvailableMicrosUSD = g.config.MaxTotalBudgetMicros
		status.CreditedMicrosUSD = g.config.MaxTotalBudgetMicros
		status.BalanceSource = "config"
		status.Enforced = status.BalanceMicrosUSD > 0
		return status, nil
	}

	account, exists, err := g.store.Snapshot(ctx, resolved.Key)
	if err != nil {
		return types.BudgetStatus{}, fmt.Errorf("read budget account: %w", err)
	}
	if !exists && g.config.MaxTotalBudgetMicros > 0 {
		account = AccountState{
			Key:               resolved.Key,
			BalanceMicrosUSD:  g.config.MaxTotalBudgetMicros,
			CreditedMicrosUSD: g.config.MaxTotalBudgetMicros,
		}
		status.BalanceSource = "config"
	} else {
		status.BalanceSource = "store"
	}
	status.DebitedMicrosUSD = account.DebitedMicrosUSD
	status.CreditedMicrosUSD = account.CreditedMicrosUSD
	status.BalanceMicrosUSD = account.BalanceMicrosUSD
	status.AvailableMicrosUSD = account.BalanceMicrosUSD
	status.Enforced = status.BalanceMicrosUSD > 0 || status.CreditedMicrosUSD > 0 || g.config.MaxTotalBudgetMicros > 0
	status.Warnings = g.buildWarnings(account.CreditedMicrosUSD, account.BalanceMicrosUSD)
	history, err := g.budgetHistory(ctx, resolved.Key, status, g.historyLimit())
	if err != nil {
		return types.BudgetStatus{}, fmt.Errorf("read budget history: %w", err)
	}
	status.History = history
	return status, nil
}

func (g *StaticGovernor) RecentBudgetHistory(ctx context.Context, limit int) ([]types.BudgetHistoryEntry, error) {
	if g.history == nil {
		return nil, nil
	}
	events, err := g.history.ListRecentEvents(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]types.BudgetHistoryEntry, 0, len(events))
	for _, event := range events {
		out = append(out, types.BudgetHistoryEntry{
			Type:              event.Type,
			Scope:             event.Scope,
			Provider:          event.Provider,
			Model:             event.Model,
			RequestID:         event.RequestID,
			Actor:             event.Actor,
			Detail:            event.Detail,
			AmountMicrosUSD:   event.AmountMicrosUSD,
			BalanceMicrosUSD:  event.BalanceMicrosUSD,
			CreditedMicrosUSD: event.CreditedMicrosUSD,
			DebitedMicrosUSD:  event.DebitedMicrosUSD,
			PromptTokens:      event.PromptTokens,
			CompletionTokens:  event.CompletionTokens,
			TotalTokens:       event.TotalTokens,
			Timestamp:         event.OccurredAt,
		})
	}
	return out, nil
}

func (g *StaticGovernor) TopUpBudget(ctx context.Context, filter BudgetFilter, deltaMicros int64) error {
	if g.store == nil || deltaMicros <= 0 {
		return nil
	}
	resolved := g.resolveBudgetFilter(filter)
	account, err := g.store.Credit(ctx, resolved.Key, deltaMicros)
	if err != nil {
		return fmt.Errorf("top up budget balance: %w", err)
	}
	if err := g.appendBudgetEvent(ctx, BudgetEvent{
		Key:               resolved.Key,
		Type:              "top_up",
		Scope:             resolved.Scope,
		Provider:          resolved.Provider,
		AmountMicrosUSD:   deltaMicros,
		BalanceMicrosUSD:  account.BalanceMicrosUSD,
		CreditedMicrosUSD: account.CreditedMicrosUSD,
		DebitedMicrosUSD:  account.DebitedMicrosUSD,
		OccurredAt:        time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("append top-up history: %w", err)
	}
	return nil
}

func (g *StaticGovernor) SetBudgetBalance(ctx context.Context, filter BudgetFilter, balanceMicros int64) error {
	if g.store == nil || balanceMicros < 0 {
		return nil
	}
	resolved := g.resolveBudgetFilter(filter)
	account, err := g.store.SetBalance(ctx, resolved.Key, balanceMicros)
	if err != nil {
		return fmt.Errorf("set budget balance: %w", err)
	}
	if err := g.appendBudgetEvent(ctx, BudgetEvent{
		Key:               resolved.Key,
		Type:              "set_balance",
		Scope:             resolved.Scope,
		Provider:          resolved.Provider,
		AmountMicrosUSD:   balanceMicros,
		BalanceMicrosUSD:  account.BalanceMicrosUSD,
		CreditedMicrosUSD: account.CreditedMicrosUSD,
		DebitedMicrosUSD:  account.DebitedMicrosUSD,
		OccurredAt:        time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("append balance history: %w", err)
	}
	return nil
}

func (g *StaticGovernor) ResetBudget(ctx context.Context, filter BudgetFilter) error {
	if g.store == nil {
		return nil
	}
	resolved := g.resolveBudgetFilter(filter)
	if err := g.store.Reset(ctx, resolved.Key); err != nil {
		return fmt.Errorf("reset budget state: %w", err)
	}
	if err := g.appendBudgetEvent(ctx, BudgetEvent{
		Key:              resolved.Key,
		Type:             "reset",
		Scope:            resolved.Scope,
		Provider:         resolved.Provider,
		BalanceMicrosUSD: 0,
		OccurredAt:       time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("append reset history: %w", err)
	}
	return nil
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

func (g *StaticGovernor) budgetKeyForRequest(_ types.ChatRequest, decision types.RouteDecision) string {
	return g.resolveBudgetFilter(BudgetFilter{
		Scope:    g.config.BudgetScope,
		Provider: decision.Provider,
	}).Key
}

func (g *StaticGovernor) resolveBudgetFilter(filter BudgetFilter) BudgetFilter {
	if filter.Key != "" {
		if filter.Scope == "" {
			filter.Scope = "custom"
		}
		return filter
	}

	baseKey := g.config.BudgetKey
	if baseKey == "" {
		baseKey = "global"
	}

	scope := filter.Scope
	if scope == "" {
		scope = g.config.BudgetScope
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

func (g *StaticGovernor) effectiveBudgetBalance(ctx context.Context, key string) (int64, bool, error) {
	account, exists, err := g.store.Snapshot(ctx, key)
	if err != nil {
		return 0, false, err
	}
	if exists {
		return account.BalanceMicrosUSD, true, nil
	}
	if g.config.MaxTotalBudgetMicros > 0 {
		return g.config.MaxTotalBudgetMicros, true, nil
	}
	return 0, false, nil
}

func (g *StaticGovernor) appendBudgetEvent(ctx context.Context, event BudgetEvent) error {
	if g.history == nil {
		return nil
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	if g.store != nil && (event.BalanceMicrosUSD == 0 || event.CreditedMicrosUSD == 0 || event.DebitedMicrosUSD == 0) {
		account, _, err := g.store.Snapshot(ctx, event.Key)
		if err == nil {
			if event.BalanceMicrosUSD == 0 {
				event.BalanceMicrosUSD = account.BalanceMicrosUSD
			}
			if event.CreditedMicrosUSD == 0 {
				event.CreditedMicrosUSD = account.CreditedMicrosUSD
			}
			if event.DebitedMicrosUSD == 0 {
				event.DebitedMicrosUSD = account.DebitedMicrosUSD
			}
		}
	}
	return g.history.AppendEvent(ctx, event)
}

func (g *StaticGovernor) budgetHistory(ctx context.Context, key string, status types.BudgetStatus, limit int) ([]types.BudgetHistoryEntry, error) {
	if g.history == nil {
		return nil, nil
	}

	events, err := g.history.ListEvents(ctx, key, limit)
	if err != nil {
		return nil, err
	}

	out := make([]types.BudgetHistoryEntry, 0, len(events))
	for _, event := range events {
		out = append(out, types.BudgetHistoryEntry{
			Type:              event.Type,
			Scope:             event.Scope,
			Provider:          firstNonEmpty(event.Provider, status.Provider),
			Model:             event.Model,
			RequestID:         event.RequestID,
			Actor:             event.Actor,
			Detail:            event.Detail,
			AmountMicrosUSD:   event.AmountMicrosUSD,
			BalanceMicrosUSD:  event.BalanceMicrosUSD,
			CreditedMicrosUSD: event.CreditedMicrosUSD,
			DebitedMicrosUSD:  event.DebitedMicrosUSD,
			PromptTokens:      event.PromptTokens,
			CompletionTokens:  event.CompletionTokens,
			TotalTokens:       event.TotalTokens,
			Timestamp:         event.OccurredAt,
		})
	}
	return out, nil
}

func (g *StaticGovernor) buildWarnings(credited, balance int64) []types.BudgetWarning {
	if credited <= 0 || balance < 0 {
		return nil
	}

	thresholds := g.config.BudgetWarningThresholds
	if len(thresholds) == 0 {
		thresholds = []int{50, 80, 95}
	}

	out := make([]types.BudgetWarning, 0, len(thresholds))
	for _, threshold := range thresholds {
		if threshold <= 0 {
			continue
		}
		thresholdMicros := (credited * int64(threshold)) / 100
		out = append(out, types.BudgetWarning{
			ThresholdPercent:   threshold,
			ThresholdMicrosUSD: thresholdMicros,
			BalanceMicrosUSD:   balance,
			AvailableMicrosUSD: balance,
			Triggered:          balance <= thresholdMicros,
		})
	}
	return out
}

func (g *StaticGovernor) historyLimit() int {
	if g.config.BudgetHistoryLimit <= 0 {
		return 20
	}
	return g.config.BudgetHistoryLimit
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
