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

	store := NewMemoryBudgetStore()
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

func TestStaticGovernorBudgetTracking(t *testing.T) {
	t.Parallel()

	store := NewMemoryBudgetStore()
	gov := NewStaticGovernor(config.GovernorConfig{
		MaxTotalBudgetMicros: 100,
		BudgetKey:            "global",
	}, store, store)

	if err := gov.RecordUsage(context.Background(), types.ChatRequest{}, types.RouteDecision{Provider: "openai"}, types.Usage{TotalTokens: 10}, 60); err != nil {
		t.Fatalf("RecordUsage() error = %v", err)
	}

	err := gov.CheckRoute(context.Background(), types.ChatRequest{}, types.RouteDecision{
		Provider: "openai",
		Model:    "gpt-4o-mini",
	}, "cloud", 50)
	if err == nil {
		t.Fatal("CheckRoute() error = nil, want budget denial")
	}
}

func TestStaticGovernorBudgetTopUpOverridesConfigLimit(t *testing.T) {
	t.Parallel()

	store := NewMemoryBudgetStore()
	gov := NewStaticGovernor(config.GovernorConfig{
		MaxTotalBudgetMicros: 100,
		BudgetKey:            "global",
	}, store, store)

	if err := gov.TopUpBudget(context.Background(), BudgetFilter{Scope: "global"}, 200); err != nil {
		t.Fatalf("TopUpBudget() error = %v", err)
	}

	if err := gov.RecordUsage(context.Background(), types.ChatRequest{}, types.RouteDecision{Provider: "openai"}, types.Usage{TotalTokens: 20}, 250); err != nil {
		t.Fatalf("RecordUsage() error = %v", err)
	}

	if err := gov.CheckRoute(context.Background(), types.ChatRequest{}, types.RouteDecision{
		Provider: "openai",
		Model:    "gpt-4o-mini",
	}, "cloud", 40); err == nil {
		t.Fatal("CheckRoute() error = nil, want limit denial after top-up-adjusted budget")
	}

	status, err := gov.BudgetStatus(context.Background(), BudgetFilter{Scope: "global"})
	if err != nil {
		t.Fatalf("BudgetStatus() error = %v", err)
	}
	if status.CreditedMicrosUSD != 200 {
		t.Fatalf("credited_micros_usd = %d, want 200", status.CreditedMicrosUSD)
	}
	if status.BalanceSource != "store" {
		t.Fatalf("balance_source = %q, want store", status.BalanceSource)
	}
	if len(status.History) != 2 {
		t.Fatalf("history length = %d, want 2", len(status.History))
	}
	if status.History[0].Type != "debit" {
		t.Fatalf("latest history type = %q, want debit", status.History[0].Type)
	}
	if status.History[1].Type != "top_up" {
		t.Fatalf("older history type = %q, want top_up", status.History[1].Type)
	}
}

func TestStaticGovernorBudgetWarningsAndHistory(t *testing.T) {
	t.Parallel()

	store := NewMemoryBudgetStore()
	gov := NewStaticGovernor(config.GovernorConfig{
		MaxTotalBudgetMicros:    1_000,
		BudgetKey:               "global",
		BudgetWarningThresholds: []int{50, 90},
		BudgetHistoryLimit:      5,
	}, store, store)

	req := types.ChatRequest{
		RequestID: "req_123",
		Scope:     types.RequestScope{},
	}
	decision := types.RouteDecision{Provider: "openai", Model: "gpt-4o-mini"}

	if err := gov.TopUpBudget(context.Background(), BudgetFilter{Scope: "global"}, 2_000); err != nil {
		t.Fatalf("TopUpBudget() error = %v", err)
	}
	if err := gov.RecordUsage(context.Background(), req, decision, types.Usage{PromptTokens: 100, CompletionTokens: 25, TotalTokens: 125}, 1_850); err != nil {
		t.Fatalf("RecordUsage() error = %v", err)
	}

	status, err := gov.BudgetStatus(context.Background(), BudgetFilter{Scope: "global"})
	if err != nil {
		t.Fatalf("BudgetStatus() error = %v", err)
	}
	if len(status.Warnings) != 2 {
		t.Fatalf("warnings length = %d, want 2", len(status.Warnings))
	}
	if !status.Warnings[0].Triggered || !status.Warnings[1].Triggered {
		t.Fatalf("warnings = %#v, want both thresholds triggered", status.Warnings)
	}
	if len(status.History) != 2 {
		t.Fatalf("history length = %d, want 2", len(status.History))
	}
	if status.History[0].Type != "debit" {
		t.Fatalf("latest history type = %q, want debit", status.History[0].Type)
	}
	if status.History[0].RequestID != "req_123" {
		t.Fatalf("history request_id = %q, want req_123", status.History[0].RequestID)
	}
	if status.History[0].TotalTokens != 125 {
		t.Fatalf("history total_tokens = %d, want 125", status.History[0].TotalTokens)
	}
	if status.History[0].Timestamp.IsZero() {
		t.Fatal("history timestamp is zero")
	}
	if time.Since(status.History[0].Timestamp) > time.Minute {
		t.Fatalf("history timestamp = %v, looks stale", status.History[0].Timestamp)
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
	}, NewMemoryBudgetStore(), NewMemoryBudgetStore())

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
	}, NewMemoryBudgetStore(), NewMemoryBudgetStore())

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
	}, NewMemoryBudgetStore(), NewMemoryBudgetStore())

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

	gov := NewControlPlaneGovernor(config.GovernorConfig{}, NewMemoryBudgetStore(), NewMemoryBudgetStore(), store)
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
	}, NewMemoryBudgetStore(), NewMemoryBudgetStore(), store)

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

// ─── Money-math defenses ─────────────────────────────────────────────────────
//
// Three invariants worth pinning so a refactor of the budget path can't
// silently overcharge or fail-open:
//
//   1. CheckRoute returns nil when budget enforcement is disabled —
//      neither MaxTotalBudgetMicros set NOR an explicit credit in the
//      store. This is the "anonymous developer with no budget config"
//      flow and must never block requests.
//
//   2. CheckRoute is read-not-reservation: two concurrent requests both
//      observing a balance that covers each individually but not their
//      sum will both pass. This is by design — the alternative
//      (locking-then-reserving the estimated cost) blocks routing on a
//      DB write per request and requires unwinding on partial failure.
//      RecordUsage debits actual cost after the request completes, so
//      the budget can briefly go negative under contention. Pin the
//      design so a "fix" doesn't accidentally introduce a lock
//      contention bottleneck.
//
//   3. Concurrent RecordUsage from multiple goroutines doesn't lose
//      updates at the governor level. The store-level concurrency test
//      proves the bucket is atomic; this proves the governor wraps it
//      correctly (no read-modify-write outside the store's lock).

func TestStaticGovernor_CheckRouteAllowsWhenBudgetDisabled(t *testing.T) {
	t.Parallel()
	// No MaxTotalBudgetMicros, no SetBalance — enforcement fully off.
	store := NewMemoryBudgetStore()
	gov := NewStaticGovernor(config.GovernorConfig{
		BudgetKey: "global",
	}, store, store)

	// Even a wildly expensive estimate must not block when no budget is
	// configured. A regression that defaults to "always enforce with
	// max=0" would deny every request.
	err := gov.CheckRoute(context.Background(), types.ChatRequest{}, types.RouteDecision{
		Provider: "openai",
		Model:    "gpt-4o-mini",
	}, "cloud", 1_000_000_000)
	if err != nil {
		t.Errorf("CheckRoute() error = %v, want nil (budget enforcement is off)", err)
	}
}

func TestStaticGovernor_CheckRouteIsReadNotReservation(t *testing.T) {
	t.Parallel()
	// Pin the design contract: CheckRoute does NOT reserve the estimated
	// cost. Two concurrent CheckRoutes both pass when balance covers
	// each individually, even if cost(A)+cost(B) > balance. RecordUsage
	// debits the actual cost later, so budget can briefly go negative —
	// that's intentional vs. blocking routing on a write.
	store := NewMemoryBudgetStore()
	gov := NewStaticGovernor(config.GovernorConfig{
		MaxTotalBudgetMicros: 100,
		BudgetKey:            "global",
	}, store, store)

	decision := types.RouteDecision{Provider: "openai", Model: "gpt-4o-mini"}
	if err := gov.CheckRoute(context.Background(), types.ChatRequest{}, decision, "cloud", 60); err != nil {
		t.Fatalf("first CheckRoute: %v", err)
	}
	if err := gov.CheckRoute(context.Background(), types.ChatRequest{}, decision, "cloud", 60); err != nil {
		t.Errorf("second CheckRoute: %v, want nil (no reservation between Check and RecordUsage)", err)
	}
}

func TestStaticGovernor_RecordUsageNoLostUpdatesUnderConcurrency(t *testing.T) {
	t.Parallel()
	// Wraps the store's atomicity guarantee at the governor level.
	// 50 concurrent debits of 1k from a 1M starting budget → 1M-50k=950k.
	// A regression that read-modify-writes outside the store's lock
	// would lose updates and leave the operator with underbilling
	// proportional to the concurrency level.
	store := NewMemoryBudgetStore()
	gov := NewStaticGovernor(config.GovernorConfig{
		BudgetKey: "global",
	}, store, store)
	ctx := context.Background()

	if err := gov.TopUpBudget(ctx, BudgetFilter{Scope: "global"}, 1_000_000); err != nil {
		t.Fatalf("TopUpBudget: %v", err)
	}

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

	status, err := gov.BudgetStatus(ctx, BudgetFilter{Scope: "global"})
	if err != nil {
		t.Fatalf("BudgetStatus: %v", err)
	}
	const want = 1_000_000 - goroutines*1_000
	if status.BalanceMicrosUSD != want {
		t.Errorf("balance = %d, want %d (%d goroutines, no lost updates)",
			status.BalanceMicrosUSD, want, goroutines)
	}
}

// TestStaticGovernor_TenantImpersonationDebitsBoundTenant verifies the
// budget filter resolution for a tenant-bound request: the resolved key
// uses scope.Tenant (set by the handler from principal.Tenant), not the
// wire `User` field. Defends against a regression that reads tenant from
// the wire payload — letting a key bound to "team-a" silently debit
// "team-b"'s budget by passing user="team-b" in the request.
