package policy

import (
	"testing"

	"github.com/hecate/agent-runtime/internal/config"
)

func TestErrorErrorMessageFallback(t *testing.T) {
	cases := []struct {
		name string
		err  *Error
		want string
	}{
		{"nil receiver", nil, ""},
		{"explicit message wins", &Error{Evaluation{Message: "explicit", Reason: "ignored"}}, "explicit"},
		{"reason fills in when message is empty", &Error{Evaluation{Reason: "no perms"}}, "no perms"},
		{"rule id used when message and reason missing", &Error{Evaluation{RuleID: "r1"}}, `request denied by policy rule "r1"`},
		{"final fallback when nothing set", &Error{Evaluation{}}, "request denied by policy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.want {
				t.Errorf("Error() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDenyMessageFallback(t *testing.T) {
	if got := denyMessage(Rule{Reason: "no admin"}); got != "no admin" {
		t.Errorf("denyMessage(Reason) = %q, want no admin", got)
	}
	if got := denyMessage(Rule{ID: "r1"}); got != `request denied by policy rule "r1"` {
		t.Errorf("denyMessage(ID only) = %q", got)
	}
	if got := denyMessage(Rule{}); got != "request denied by policy" {
		t.Errorf("denyMessage(empty) = %q", got)
	}
}

func TestMatchesAllConditions(t *testing.T) {
	rule := Rule{
		Models:                 []string{"gpt-4o"},
		Providers:              []string{"openai"},
		ProviderKinds:          []string{"cloud"},
		RouteReasons:           []string{"explicit"},
		MinPromptTokens:        100,
		MinEstimatedCostMicros: 50_000,
	}
	matchingSubject := Subject{
		Model:               "gpt-4o",
		Provider:            "openai",
		ProviderKind:        "cloud",
		RouteReason:         "explicit",
		PromptTokens:        200,
		EstimatedCostMicros: 100_000,
	}
	if !matches(rule, matchingSubject) {
		t.Error("expected match for fully-aligned subject")
	}

	cases := []struct {
		name   string
		mutate func(*Subject)
	}{
		{"model mismatch", func(s *Subject) { s.Model = "claude-haiku" }},
		{"provider mismatch", func(s *Subject) { s.Provider = "anthropic" }},
		{"provider kind mismatch", func(s *Subject) { s.ProviderKind = "local" }},
		{"route reason mismatch", func(s *Subject) { s.RouteReason = "fallback" }},
		{"prompt tokens too low", func(s *Subject) { s.PromptTokens = 50 }},
		{"cost too low", func(s *Subject) { s.EstimatedCostMicros = 1_000 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subject := matchingSubject
			tc.mutate(&subject)
			if matches(rule, subject) {
				t.Errorf("expected mismatch when %s", tc.name)
			}
		})
	}
}

func TestEvaluateDenyMultipleRules(t *testing.T) {
	rules := []Rule{
		{ID: "rewrite", Action: ActionRewriteModel, Models: []string{"gpt-4o"}, RewriteModelTo: "gpt-4o-mini"},
		{ID: "narrow-deny", Action: ActionDeny, Providers: []string{"anthropic"}},
		{ID: "broad-deny", Action: ActionDeny, Models: []string{"gpt-4o"}},
	}

	t.Run("rewrite rule is skipped by deny evaluation", func(t *testing.T) {
		got := EvaluateDeny(rules, Subject{Model: "gpt-4o"})
		if got == nil || got.Evaluation.RuleID != "broad-deny" {
			t.Errorf("expected broad-deny, got %+v", got)
		}
	})

	t.Run("first matching deny wins", func(t *testing.T) {
		got := EvaluateDeny(rules, Subject{Provider: "anthropic", Model: "gpt-4o"})
		if got == nil || got.Evaluation.RuleID != "narrow-deny" {
			t.Errorf("expected narrow-deny (first matching), got %+v", got)
		}
	})

	t.Run("no rules match returns nil", func(t *testing.T) {
		if got := EvaluateDeny(rules, Subject{Model: "claude-haiku"}); got != nil {
			t.Errorf("expected nil for no match, got %+v", got)
		}
	})

	t.Run("empty rules slice returns nil", func(t *testing.T) {
		if got := EvaluateDeny(nil, Subject{Model: "gpt-4o"}); got != nil {
			t.Errorf("expected nil for empty rules, got %+v", got)
		}
	})
}

func TestEvaluateRewriteSkipsRulesWithEmptyTarget(t *testing.T) {
	rules := []Rule{
		{ID: "no-target", Action: ActionRewriteModel, RewriteModelTo: ""},
		{ID: "deny", Action: ActionDeny, Models: []string{"gpt-4o"}},
		{ID: "good", Action: ActionRewriteModel, Models: []string{"gpt-4o"}, RewriteModelTo: "gpt-4o-mini"},
	}
	eval, target, ok := EvaluateRewrite(rules, Subject{Model: "gpt-4o"})
	if !ok {
		t.Fatal("expected rewrite to fire")
	}
	if eval.RuleID != "good" {
		t.Errorf("expected RuleID 'good', got %q", eval.RuleID)
	}
	if target != "gpt-4o-mini" {
		t.Errorf("target = %q, want gpt-4o-mini", target)
	}
}

func TestEvaluateRewriteNoMatch(t *testing.T) {
	rules := []Rule{{Action: ActionRewriteModel, Models: []string{"gpt-4o"}, RewriteModelTo: "gpt-4o-mini"}}
	if _, _, ok := EvaluateRewrite(rules, Subject{Model: "claude-haiku"}); ok {
		t.Error("expected no rewrite for unrelated model")
	}
}

func TestFromConfigSkipsBlankActions(t *testing.T) {
	cfg := []config.PolicyRuleConfig{
		{ID: "valid", Action: "deny", Providers: []string{"openai"}},
		{ID: "blank", Action: ""},
		{ID: "whitespace", Action: "   "},
	}
	rules := FromConfig(cfg)
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule (blank/whitespace skipped), got %d", len(rules))
	}
	if rules[0].ID != "valid" {
		t.Errorf("expected 'valid' to survive, got %+v", rules[0])
	}
}

func TestFromConfigClonesNestedSlices(t *testing.T) {
	providers := []string{"openai"}
	cfg := []config.PolicyRuleConfig{{ID: "r", Action: "deny", Providers: providers}}
	rules := FromConfig(cfg)
	rules[0].Providers[0] = "MUTATED"
	if providers[0] == "MUTATED" {
		t.Error("FromConfig did not clone Providers slice — mutation leaked back to caller")
	}
}
