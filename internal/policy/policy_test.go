package policy

import (
	"testing"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestEvaluateDenyMatchesProviderKindAndRouteCost(t *testing.T) {
	t.Parallel()

	rules := FromConfig([]config.PolicyRuleConfig{
		{
			ID:                     "cloud-cost-cap",
			Action:                 ActionDeny,
			Reason:                 "expensive cloud routes blocked above cost ceiling",
			ProviderKinds:          []string{"cloud"},
			MinEstimatedCostMicros: 100,
		},
	})

	subject := BuildRouteSubject(types.ChatRequest{
		Model: "gpt-4o-mini",
	}, types.RouteDecision{
		Provider: "openai",
		Model:    "gpt-4o-mini",
		Reason:   "fallback",
	}, "cloud", 250)

	err := EvaluateDeny(rules, subject)
	if err == nil {
		t.Fatal("EvaluateDeny() error = nil, want match")
	}
	if err.Evaluation.RuleID != "cloud-cost-cap" {
		t.Fatalf("rule_id = %q, want cloud-cost-cap", err.Evaluation.RuleID)
	}
}

func TestEvaluateRewriteRewritesModel(t *testing.T) {
	t.Parallel()

	rules := FromConfig([]config.PolicyRuleConfig{
		{
			ID:             "downgrade-gpt4o",
			Action:         ActionRewriteModel,
			Models:         []string{"gpt-4o"},
			RewriteModelTo: "gpt-4o-mini",
		},
	})

	eval, rewritten, ok := EvaluateRewrite(rules, BuildRequestSubject(types.ChatRequest{Model: "gpt-4o"}))
	if !ok {
		t.Fatal("EvaluateRewrite() ok = false, want true")
	}
	if eval.RuleID != "downgrade-gpt4o" {
		t.Fatalf("rule_id = %q, want downgrade-gpt4o", eval.RuleID)
	}
	if rewritten != "gpt-4o-mini" {
		t.Fatalf("rewritten model = %q, want gpt-4o-mini", rewritten)
	}
}
