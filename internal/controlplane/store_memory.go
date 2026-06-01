package controlplane

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hecatehq/hecate/internal/config"
)

// MemoryStore is an in-memory control plane store. State is lost on restart.
// It is used as the default backend when no persistent store is configured,
// allowing provider toggling and other control-plane operations without
// requiring external storage.
type MemoryStore struct {
	mu   sync.RWMutex
	data State
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{}
}

func (s *MemoryStore) Backend() string { return "memory" }

func (s *MemoryStore) Snapshot(_ context.Context) (State, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneState(s.data), nil
}

func (s *MemoryStore) UpsertProvider(ctx context.Context, provider Provider, secret *ProviderSecret) (Provider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := applyProviderUpsert(ctx, &s.data, provider, secret)
	return p, err
}

func (s *MemoryStore) RotateProviderSecret(ctx context.Context, id string, secret ProviderSecret) (Provider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return applyRotateProviderSecret(ctx, &s.data, id, secret)
}

func (s *MemoryStore) DeleteProviderCredential(ctx context.Context, id string) (Provider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return applyDeleteProviderCredential(ctx, &s.data, id)
}

func (s *MemoryStore) DeleteProvider(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return applyDeleteProvider(ctx, &s.data, id)
}

func (s *MemoryStore) UpsertPolicyRule(ctx context.Context, rule config.PolicyRuleConfig) (config.PolicyRuleConfig, error) {
	rule, err := normalizePolicyRule(rule)
	if err != nil {
		return config.PolicyRuleConfig{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	action := upsertPolicyRule(&s.data, rule)
	appendAuditEvent(&s.data, newAuditEvent(ctx, action, "policy_rule", rule.ID, rule.Action))
	return rule, nil
}

func (s *MemoryStore) DeletePolicyRule(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("policy rule id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index := policyRuleIndex(s.data.PolicyRules, id)
	if index < 0 {
		return fmt.Errorf("policy rule %q not found", id)
	}
	appendAuditEvent(&s.data, newAuditEvent(ctx, "policy_rule.deleted", "policy_rule", s.data.PolicyRules[index].ID, s.data.PolicyRules[index].Action))
	s.data.PolicyRules = append(s.data.PolicyRules[:index], s.data.PolicyRules[index+1:]...)
	return nil
}

func (s *MemoryStore) UpsertInstalledModel(ctx context.Context, model InstalledModel) (InstalledModel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return applyUpsertInstalledModel(ctx, &s.data, model)
}

func (s *MemoryStore) DeleteInstalledModel(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return applyDeleteInstalledModel(ctx, &s.data, id)
}

func (s *MemoryStore) Prune(_ context.Context, maxAge time.Duration, maxCount int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return pruneAuditEvents(&s.data, maxAge, maxCount), nil
}
