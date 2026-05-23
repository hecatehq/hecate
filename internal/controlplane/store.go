package controlplane

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/config"
)

type Provider struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	PresetID string `json:"preset_id,omitempty"`
	// CustomName is an optional operator-supplied label that appears
	// alongside Name in the providers table, used to tell two
	// instances of the same preset apart ("Anthropic" + "Prod" vs
	// "Anthropic" + "Dev"). Name itself stays fixed for preset-based
	// providers; this is the disambiguator.
	CustomName     string    `json:"custom_name,omitempty"`
	Kind           string    `json:"kind"`
	Protocol       string    `json:"protocol"`
	BaseURL        string    `json:"base_url"`
	APIVersion     string    `json:"api_version,omitempty"`
	DefaultModel   string    `json:"default_model,omitempty"`
	ExplicitFields []string  `json:"explicit_fields,omitempty"`
	Enabled        bool      `json:"enabled"`
	CredentialID   string    `json:"credential_id,omitempty"`
	CreatedAt      time.Time `json:"created_at,omitempty"`
	UpdatedAt      time.Time `json:"updated_at,omitempty"`
}

type ProviderSecret struct {
	ID              string    `json:"id"`
	ProviderID      string    `json:"provider_id"`
	APIKeyEncrypted string    `json:"api_key_encrypted"`
	APIKeyPreview   string    `json:"api_key_preview,omitempty"`
	CreatedAt       time.Time `json:"created_at,omitempty"`
	RotatedAt       time.Time `json:"rotated_at,omitempty"`
}

type AuditEvent struct {
	Timestamp  time.Time `json:"timestamp"`
	Actor      string    `json:"actor"`
	Action     string    `json:"action"`
	TargetType string    `json:"target_type"`
	TargetID   string    `json:"target_id"`
	Detail     string    `json:"detail,omitempty"`
}

type State struct {
	Providers       []Provider                `json:"providers,omitempty"`
	ProviderSecrets []ProviderSecret          `json:"provider_secrets,omitempty"`
	PolicyRules     []config.PolicyRuleConfig `json:"policy_rules,omitempty"`
	Events          []AuditEvent              `json:"events,omitempty"`
}

type Store interface {
	Backend() string
	Snapshot(ctx context.Context) (State, error)
	UpsertProvider(ctx context.Context, provider Provider, secret *ProviderSecret) (Provider, error)
	RotateProviderSecret(ctx context.Context, id string, secret ProviderSecret) (Provider, error)
	DeleteProviderCredential(ctx context.Context, id string) (Provider, error)
	DeleteProvider(ctx context.Context, id string) error
	UpsertPolicyRule(ctx context.Context, rule config.PolicyRuleConfig) (config.PolicyRuleConfig, error)
	DeletePolicyRule(ctx context.Context, id string) error
	Prune(ctx context.Context, maxAge time.Duration, maxCount int) (int, error)
}

type actorContextKey struct{}

const maxAuditEvents = 100

func WithActor(ctx context.Context, actor string) context.Context {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return ctx
	}
	return context.WithValue(ctx, actorContextKey{}, actor)
}

func cloneState(state State) State {
	out := State{
		Providers:       make([]Provider, 0, len(state.Providers)),
		ProviderSecrets: make([]ProviderSecret, 0, len(state.ProviderSecrets)),
		PolicyRules:     make([]config.PolicyRuleConfig, 0, len(state.PolicyRules)),
		Events:          make([]AuditEvent, 0, len(state.Events)),
	}
	for _, provider := range state.Providers {
		out.Providers = append(out.Providers, Provider{
			ID:             provider.ID,
			Name:           provider.Name,
			PresetID:       provider.PresetID,
			CustomName:     provider.CustomName,
			Kind:           provider.Kind,
			Protocol:       provider.Protocol,
			BaseURL:        provider.BaseURL,
			APIVersion:     provider.APIVersion,
			DefaultModel:   provider.DefaultModel,
			ExplicitFields: append([]string(nil), provider.ExplicitFields...),
			Enabled:        provider.Enabled,
			CredentialID:   provider.CredentialID,
			CreatedAt:      provider.CreatedAt,
			UpdatedAt:      provider.UpdatedAt,
		})
	}
	for _, secret := range state.ProviderSecrets {
		out.ProviderSecrets = append(out.ProviderSecrets, ProviderSecret{
			ID:              secret.ID,
			ProviderID:      secret.ProviderID,
			APIKeyEncrypted: secret.APIKeyEncrypted,
			APIKeyPreview:   secret.APIKeyPreview,
			CreatedAt:       secret.CreatedAt,
			RotatedAt:       secret.RotatedAt,
		})
	}
	for _, rule := range state.PolicyRules {
		out.PolicyRules = append(out.PolicyRules, clonePolicyRule(rule))
	}
	for _, event := range state.Events {
		out.Events = append(out.Events, AuditEvent{
			Timestamp:  event.Timestamp,
			Actor:      event.Actor,
			Action:     event.Action,
			TargetType: event.TargetType,
			TargetID:   event.TargetID,
			Detail:     event.Detail,
		})
	}
	return out
}

func actorFromContext(ctx context.Context) string {
	actor, _ := ctx.Value(actorContextKey{}).(string)
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return "system"
	}
	return actor
}

func newAuditEvent(ctx context.Context, action, targetType, targetID, detail string) AuditEvent {
	return AuditEvent{
		Timestamp:  time.Now().UTC(),
		Actor:      actorFromContext(ctx),
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Detail:     detail,
	}
}

func appendAuditEvent(state *State, event AuditEvent) {
	if state == nil {
		return
	}
	state.Events = append(state.Events, event)
	if len(state.Events) > maxAuditEvents {
		state.Events = append([]AuditEvent(nil), state.Events[len(state.Events)-maxAuditEvents:]...)
	}
}

func pruneAuditEvents(state *State, maxAge time.Duration, maxCount int) int {
	if state == nil {
		return 0
	}

	now := time.Now()
	deleted := 0
	kept := state.Events[:0]
	for _, event := range state.Events {
		if maxAge > 0 && !event.Timestamp.IsZero() && event.Timestamp.Before(now.Add(-maxAge)) {
			deleted++
			continue
		}
		kept = append(kept, event)
	}
	if maxCount > 0 && len(kept) > maxCount {
		deleted += len(kept) - maxCount
		kept = append([]AuditEvent(nil), kept[len(kept)-maxCount:]...)
	} else {
		kept = append([]AuditEvent(nil), kept...)
	}
	state.Events = kept
	return deleted
}

func canonicalID(id, name string) string {
	value := strings.TrimSpace(id)
	if value == "" {
		value = strings.TrimSpace(name)
	}
	value = strings.ToLower(value)
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func normalizePolicyRule(rule config.PolicyRuleConfig) (config.PolicyRuleConfig, error) {
	rule.ID = strings.TrimSpace(rule.ID)
	rule.Action = strings.TrimSpace(rule.Action)
	rule.Reason = strings.TrimSpace(rule.Reason)
	rule.Providers = normalizeStringList(rule.Providers)
	rule.ProviderKinds = normalizeStringList(rule.ProviderKinds)
	rule.Models = normalizeStringList(rule.Models)
	rule.RouteReasons = normalizeStringList(rule.RouteReasons)
	rule.RewriteModelTo = strings.TrimSpace(rule.RewriteModelTo)
	if rule.ID == "" {
		return config.PolicyRuleConfig{}, fmt.Errorf("policy rule id is required")
	}
	if rule.Action == "" {
		return config.PolicyRuleConfig{}, fmt.Errorf("policy rule action is required")
	}
	return rule, nil
}

func upsertPolicyRule(state *State, rule config.PolicyRuleConfig) string {
	index := policyRuleIndex(state.PolicyRules, rule.ID)
	if index >= 0 {
		state.PolicyRules[index] = clonePolicyRule(rule)
		return "policy_rule.updated"
	}
	state.PolicyRules = append(state.PolicyRules, clonePolicyRule(rule))
	return "policy_rule.created"
}

func policyRuleIndex(items []config.PolicyRuleConfig, id string) int {
	for i := range items {
		if items[i].ID == id {
			return i
		}
	}
	return -1
}

func normalizeStringList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func clonePolicyRule(rule config.PolicyRuleConfig) config.PolicyRuleConfig {
	rule.Providers = append([]string(nil), rule.Providers...)
	rule.ProviderKinds = append([]string(nil), rule.ProviderKinds...)
	rule.Models = append([]string(nil), rule.Models...)
	rule.RouteReasons = append([]string(nil), rule.RouteReasons...)
	return rule
}
