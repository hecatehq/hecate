package governor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hecate/agent-runtime/internal/config"
	"github.com/hecate/agent-runtime/internal/controlplane"
	"github.com/hecate/agent-runtime/internal/policy"
	"github.com/hecate/agent-runtime/pkg/types"
)

func TestStaticGovernorCheckRoutePolicy(t *testing.T) {
	t.Parallel()

	store := NewMemoryUsageStore()
	gov := NewStaticGovernor(config.GovernorConfig{
		RouteMode:        "local_only",
		AllowedProviders: []string{"ollama"},
		DeniedModels:     []string{"gpt-4o-mini"},
	}, store, store)

	err := gov.CheckRoute(context.Background(), types.ChatRequest{}, types.RouteDecision{
		Provider: "openai",
		Model:    "gpt-4o-mini",
	}, "cloud", 0)
	if err == nil {
		t.Fatal("CheckRoute() error = nil, want policy denial")
	}
}

func TestStaticGovernorRecordsUsageEvents(t *testing.T) {
	t.Parallel()

	store := NewMemoryUsageStore()
	gov := NewStaticGovernor(config.GovernorConfig{
		UsageKey:          "global",
		UsageHistoryLimit: 5,
	}, store, store)

	req := types.ChatRequest{RequestID: "req_123"}
	decision := types.RouteDecision{Provider: "openai", Model: "gpt-4o-mini"}
	if err := gov.RecordUsage(context.Background(), req, decision, types.Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10}, 60); err != nil {
		t.Fatalf("RecordUsage() error = %v", err)
	}

	status, err := gov.UsageSummary(context.Background(), UsageFilter{Scope: "global"})
	if err != nil {
		t.Fatalf("UsageSummary() error = %v", err)
	}
	if status.UsedMicrosUSD != 60 {
		t.Fatalf("used micros = %d, want 60", status.UsedMicrosUSD)
	}
	events, err := gov.RecentUsageEvents(context.Background(), 5)
	if err != nil {
		t.Fatalf("RecentUsageEvents() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events length = %d, want 1", len(events))
	}
	if events[0].Type != "usage" {
		t.Fatalf("latest event type = %q, want usage", events[0].Type)
	}
	if events[0].RequestID != "req_123" {
		t.Fatalf("event request_id = %q, want req_123", events[0].RequestID)
	}
	if events[0].TotalTokens != 10 {
		t.Fatalf("event total_tokens = %d, want 10", events[0].TotalTokens)
	}
	if events[0].Timestamp.IsZero() {
		t.Fatal("event timestamp is zero")
	}
	if time.Since(events[0].Timestamp) > time.Minute {
		t.Fatalf("event timestamp = %v, looks stale", events[0].Timestamp)
	}
}

func TestStaticGovernorRequestPolicyRewrite(t *testing.T) {
	t.Parallel()

	gov := NewStaticGovernor(config.GovernorConfig{
		PolicyRules: []config.PolicyRuleConfig{
			{
				ID:             "tenant-default-downgrade",
				Action:         "rewrite_model",
				Models:         []string{"gpt-4o"},
				RewriteModelTo: "gpt-4o-mini",
			},
		},
	}, NewMemoryUsageStore(), NewMemoryUsageStore())

	rewritten := gov.Rewrite(types.ChatRequest{
		Model: "gpt-4o",
		Scope: types.RequestScope{},
	})
	if rewritten.Model != "gpt-4o-mini" {
		t.Fatalf("rewritten model = %q, want gpt-4o-mini", rewritten.Model)
	}
}

func TestStaticGovernorRewriteResultIncludesPolicyMetadata(t *testing.T) {
	t.Parallel()

	gov := NewStaticGovernor(config.GovernorConfig{
		PolicyRules: []config.PolicyRuleConfig{
			{
				ID:             "tenant-default-downgrade",
				Action:         "rewrite_model",
				Models:         []string{"gpt-4o"},
				Reason:         "tenant default downgrade",
				RewriteModelTo: "gpt-4o-mini",
			},
		},
	}, NewMemoryUsageStore(), NewMemoryUsageStore())

	result := gov.RewriteResult(types.ChatRequest{
		Model: "gpt-4o",
		Scope: types.RequestScope{},
	})
	if !result.Applied {
		t.Fatal("Applied = false, want true")
	}
	if result.Request.Model != "gpt-4o-mini" {
		t.Fatalf("rewritten model = %q, want gpt-4o-mini", result.Request.Model)
	}
	if result.PolicyRuleID != "tenant-default-downgrade" {
		t.Fatalf("policy rule id = %q, want tenant-default-downgrade", result.PolicyRuleID)
	}
	if result.PolicyAction != policy.ActionRewriteModel {
		t.Fatalf("policy action = %q, want rewrite_model", result.PolicyAction)
	}
	if result.PolicyReason != "tenant default downgrade" {
		t.Fatalf("policy reason = %q, want tenant default downgrade", result.PolicyReason)
	}
}

func TestStaticGovernorRouteModeDenialReturnsPolicyError(t *testing.T) {
	t.Parallel()

	gov := NewStaticGovernor(config.GovernorConfig{
		RouteMode: "cloud_only",
	}, NewMemoryUsageStore(), NewMemoryUsageStore())

	err := gov.CheckRoute(context.Background(), types.ChatRequest{}, types.RouteDecision{
		Provider: "ollama",
		Model:    "llama3.1:8b",
	}, "local", 0)
	if err == nil {
		t.Fatal("CheckRoute() error = nil, want policy denial")
	}
	var policyErr *policy.Error
	if !errors.As(err, &policyErr) {
		t.Fatalf("error = %T, want *policy.Error", err)
	}
	if policyErr.Evaluation.Action != policy.ActionDeny {
		t.Fatalf("policy action = %q, want deny", policyErr.Evaluation.Action)
	}
	if policyErr.Evaluation.Reason == "" {
		t.Fatal("policy reason is empty")
	}
}

func TestControlPlaneGovernorUsesPersistedPolicyRule(t *testing.T) {
	t.Parallel()

	store := controlplane.NewMemoryStore()
	if _, err := store.UpsertPolicyRule(context.Background(), config.PolicyRuleConfig{
		ID:            "deny-cloud",
		Action:        "deny",
		Reason:        "cloud denied from control plane",
		ProviderKinds: []string{"cloud"},
	}); err != nil {
		t.Fatalf("UpsertPolicyRule() error = %v", err)
	}

	gov := NewControlPlaneGovernor(config.GovernorConfig{}, NewMemoryUsageStore(), NewMemoryUsageStore(), store)
	err := gov.CheckRoute(context.Background(), types.ChatRequest{}, types.RouteDecision{
		Provider: "openai",
		Model:    "gpt-4o-mini",
	}, "cloud", 0)
	if err == nil {
		t.Fatal("CheckRoute() error = nil, want persisted policy denial")
	}
	if err.Error() != "cloud denied from control plane" {
		t.Fatalf("error = %q, want persisted policy reason", err.Error())
	}
}

func TestControlPlaneGovernorReflectsLivePolicyUpdatesAndKeepsConfiguredRules(t *testing.T) {
	t.Parallel()

	store := controlplane.NewMemoryStore()

	gov := NewControlPlaneGovernor(config.GovernorConfig{
		PolicyRules: []config.PolicyRuleConfig{
			{
				ID:             "tenant-default-downgrade",
				Action:         "rewrite_model",
				Models:         []string{"gpt-4o"},
				RewriteModelTo: "gpt-4o-mini",
			},
		},
	}, NewMemoryUsageStore(), NewMemoryUsageStore(), store)

	req := types.ChatRequest{
		Model: "gpt-4o",
		Scope: types.RequestScope{},
	}
	rewritten := gov.Rewrite(req)
	if rewritten.Model != "gpt-4o-mini" {
		t.Fatalf("rewritten model = %q, want gpt-4o-mini from configured rule", rewritten.Model)
	}

	decision := types.RouteDecision{
		Provider: "openai",
		Model:    "gpt-4o-mini",
	}
	if err := gov.CheckRoute(context.Background(), rewritten, decision, "cloud", 0); err != nil {
		t.Fatalf("CheckRoute(before control plane rule) error = %v, want nil", err)
	}

	if _, err := store.UpsertPolicyRule(context.Background(), config.PolicyRuleConfig{
		ID:            "deny-cloud",
		Action:        "deny",
		Reason:        "cloud denied from control plane",
		ProviderKinds: []string{"cloud"},
	}); err != nil {
		t.Fatalf("UpsertPolicyRule() error = %v", err)
	}

	err := gov.CheckRoute(context.Background(), rewritten, decision, "cloud", 0)
	if err == nil {
		t.Fatal("CheckRoute(after control plane rule) error = nil, want denial")
	}
	if err.Error() != "cloud denied from control plane" {
		t.Fatalf("error = %q, want control plane denial reason", err.Error())
	}

	if err := store.DeletePolicyRule(context.Background(), "deny-cloud"); err != nil {
		t.Fatalf("DeletePolicyRule() error = %v", err)
	}

	if err := gov.CheckRoute(context.Background(), rewritten, decision, "cloud", 0); err != nil {
		t.Fatalf("CheckRoute(after delete) error = %v, want nil", err)
	}
}

// ─── Usage-recording defenses ────────────────────────────────────────────────
//
// Three invariants worth pinning so a refactor of the usage path can't silently
// stop routing or lose usage:
//
//   1. CheckRoute does not enforce a global spend ceiling. Usage tracking is
//      append-only observability, not a preflight account gate.
//
//   2. CheckRoute remains read-only. RecordUsage runs after the request
//      completes so routing never blocks on a usage write.
//
//   3. Concurrent RecordUsage from multiple goroutines doesn't lose
//      updates at the governor level. The store-level concurrency test
//      proves the bucket is atomic; this proves the governor wraps it
//      correctly (no read-modify-write outside the store's lock).

func TestStaticGovernor_CheckRouteDoesNotEnforceUsageLimit(t *testing.T) {
	t.Parallel()
	store := NewMemoryUsageStore()
	gov := NewStaticGovernor(config.GovernorConfig{
		UsageKey: "global",
	}, store, store)

	// Usage is now append-only reporting, not a budget gate. Even a wildly
	// expensive estimate must not block routing.
	err := gov.CheckRoute(context.Background(), types.ChatRequest{}, types.RouteDecision{
		Provider: "openai",
		Model:    "gpt-4o-mini",
	}, "cloud", 1_000_000_000)
	if err != nil {
		t.Errorf("CheckRoute() error = %v, want nil", err)
	}
}

func TestStaticGovernor_CheckRouteIsPurePolicyCheck(t *testing.T) {
	t.Parallel()
	store := NewMemoryUsageStore()
	gov := NewStaticGovernor(config.GovernorConfig{
		UsageKey: "global",
	}, store, store)

	decision := types.RouteDecision{Provider: "openai", Model: "gpt-4o-mini"}
	if err := gov.CheckRoute(context.Background(), types.ChatRequest{}, decision, "cloud", 60); err != nil {
		t.Fatalf("first CheckRoute: %v", err)
	}
	if err := gov.CheckRoute(context.Background(), types.ChatRequest{}, decision, "cloud", 60); err != nil {
		t.Errorf("second CheckRoute: %v, want nil", err)
	}
}

func TestStaticGovernor_RecordUsageNoLostUpdatesUnderConcurrency(t *testing.T) {
	t.Parallel()
	// Wraps the store's atomicity guarantee at the governor level.
	// 50 concurrent usage records of 1k from a 1M limit → 50k used.
	// A regression that read-modify-writes outside the store's lock
	// would lose updates and leave the operator with undercounted usage
	// proportional to the concurrency level.
	store := NewMemoryUsageStore()
	gov := NewStaticGovernor(config.GovernorConfig{
		UsageKey: "global",
	}, store, store)
	ctx := context.Background()

	const goroutines = 50
	done := make(chan struct{}, goroutines)
	decision := types.RouteDecision{Provider: "openai", Model: "gpt-4o-mini"}
	for i := 0; i < goroutines; i++ {
		go func() {
			_ = gov.RecordUsage(ctx, types.ChatRequest{}, decision,
				types.Usage{TotalTokens: 1}, 1_000)
			done <- struct{}{}
		}()
	}
	for i := 0; i < goroutines; i++ {
		<-done
	}

	status, err := gov.UsageSummary(ctx, UsageFilter{Scope: "global"})
	if err != nil {
		t.Fatalf("UsageSummary: %v", err)
	}
	const wantUsed = goroutines * 1_000
	if status.UsedMicrosUSD != wantUsed {
		t.Errorf("used = %d, want %d (%d goroutines, no lost updates)",
			status.UsedMicrosUSD, wantUsed, goroutines)
	}
}

// TestStaticGovernor_TenantImpersonationDebitsBoundTenant verifies that usage
// key resolution uses the server-side request scope, not user-controlled wire
// fields. Defends against a regression that lets a caller attribute usage to a
// different key by passing a crafted user field.
