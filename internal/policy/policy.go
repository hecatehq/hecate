package policy

import (
	"fmt"
	"slices"
	"strings"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/prompttokens"
	"github.com/hecatehq/hecate/pkg/types"
)

const (
	ActionDeny         = "deny"
	ActionRewriteModel = "rewrite_model"
)

type Rule struct {
	ID                     string
	Action                 string
	Reason                 string
	Providers              []string
	ProviderKinds          []string
	Models                 []string
	RouteReasons           []string
	MinPromptTokens        int
	MinEstimatedCostMicros int64
	RewriteModelTo         string
}

type Subject struct {
	Model               string
	Provider            string
	ProviderKind        string
	RouteReason         string
	PromptTokens        int
	EstimatedCostMicros int64
}

type Evaluation struct {
	RuleID  string
	Action  string
	Reason  string
	Message string
}

type Error struct {
	Evaluation Evaluation
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Evaluation.Message != "" {
		return e.Evaluation.Message
	}
	if e.Evaluation.Reason != "" {
		return e.Evaluation.Reason
	}
	if e.Evaluation.RuleID != "" {
		return fmt.Sprintf("request denied by policy rule %q", e.Evaluation.RuleID)
	}
	return "request denied by policy"
}

func FromConfig(cfg []config.PolicyRuleConfig) []Rule {
	out := make([]Rule, 0, len(cfg))
	for _, item := range cfg {
		action := strings.TrimSpace(item.Action)
		if action == "" {
			continue
		}
		out = append(out, Rule{
			ID:                     strings.TrimSpace(item.ID),
			Action:                 action,
			Reason:                 strings.TrimSpace(item.Reason),
			Providers:              slices.Clone(item.Providers),
			ProviderKinds:          slices.Clone(item.ProviderKinds),
			Models:                 slices.Clone(item.Models),
			RouteReasons:           slices.Clone(item.RouteReasons),
			MinPromptTokens:        item.MinPromptTokens,
			MinEstimatedCostMicros: item.MinEstimatedCostMicros,
			RewriteModelTo:         strings.TrimSpace(item.RewriteModelTo),
		})
	}
	return out
}

func BuildRequestSubject(req types.ChatRequest) Subject {
	return Subject{
		Model:        req.Model,
		PromptTokens: promptEstimate(req),
	}
}

func BuildRouteSubject(req types.ChatRequest, decision types.RouteDecision, providerKind string, estimatedCostMicros int64) Subject {
	subject := BuildRequestSubject(req)
	subject.Model = firstNonEmpty(decision.Model, req.Model)
	subject.Provider = decision.Provider
	subject.ProviderKind = firstNonEmpty(providerKind, decision.ProviderKind)
	subject.RouteReason = decision.Reason
	subject.EstimatedCostMicros = estimatedCostMicros
	return subject
}

func EvaluateDeny(rules []Rule, subject Subject) *Error {
	for _, rule := range rules {
		if rule.Action != ActionDeny {
			continue
		}
		if !matches(rule, subject) {
			continue
		}
		return &Error{
			Evaluation: Evaluation{
				RuleID:  rule.ID,
				Action:  rule.Action,
				Reason:  rule.Reason,
				Message: denyMessage(rule),
			},
		}
	}
	return nil
}

func EvaluateRewrite(rules []Rule, subject Subject) (*Evaluation, string, bool) {
	for _, rule := range rules {
		if rule.Action != ActionRewriteModel || rule.RewriteModelTo == "" {
			continue
		}
		if !matches(rule, subject) {
			continue
		}
		return &Evaluation{
			RuleID:  rule.ID,
			Action:  rule.Action,
			Reason:  rule.Reason,
			Message: rewriteMessage(rule),
		}, rule.RewriteModelTo, true
	}
	return nil, "", false
}

func matches(rule Rule, subject Subject) bool {
	if len(rule.Models) > 0 && !contains(rule.Models, subject.Model) {
		return false
	}
	if len(rule.Providers) > 0 && !contains(rule.Providers, subject.Provider) {
		return false
	}
	if len(rule.ProviderKinds) > 0 && !contains(rule.ProviderKinds, subject.ProviderKind) {
		return false
	}
	if len(rule.RouteReasons) > 0 && !contains(rule.RouteReasons, subject.RouteReason) {
		return false
	}
	if rule.MinPromptTokens > 0 && subject.PromptTokens < rule.MinPromptTokens {
		return false
	}
	if rule.MinEstimatedCostMicros > 0 && subject.EstimatedCostMicros < rule.MinEstimatedCostMicros {
		return false
	}
	return true
}

func denyMessage(rule Rule) string {
	if rule.Reason != "" {
		return rule.Reason
	}
	if rule.ID != "" {
		return fmt.Sprintf("request denied by policy rule %q", rule.ID)
	}
	return "request denied by policy"
}

func rewriteMessage(rule Rule) string {
	if rule.Reason != "" {
		return rule.Reason
	}
	if rule.ID != "" {
		return fmt.Sprintf("model rewritten by policy rule %q", rule.ID)
	}
	return "model rewritten by policy"
}

func promptEstimate(req types.ChatRequest) int {
	return prompttokens.EstimateMessages(req.Messages)
}

func contains(values []string, target string) bool {
	return target != "" && slices.Contains(values, target)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
