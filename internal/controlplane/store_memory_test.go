package controlplane

import (
	"context"
	"testing"

	"github.com/hecate/agent-runtime/internal/config"
)

func TestMemoryStore_PolicyRuleLifecycle(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	ctx := context.Background()

	rule, err := store.UpsertPolicyRule(ctx, config.PolicyRuleConfig{
		ID:     "deny-cloud-no-creds",
		Action: "deny",
		Reason: "no credentials configured",
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if rule.ID != "deny-cloud-no-creds" {
		t.Fatalf("ID = %q", rule.ID)
	}
	if err := store.DeletePolicyRule(ctx, rule.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestMemoryStore_ProviderUpsertAndDelete(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	ctx := context.Background()

	prov, err := store.UpsertProvider(ctx, Provider{
		Name:    "custom",
		Kind:    "cloud",
		BaseURL: "https://custom.example.com/v1",
		Enabled: true,
	}, &ProviderSecret{
		ProviderID:      "custom",
		APIKeyEncrypted: "ciphertext",
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if prov.ID == "" {
		t.Fatal("provider id not generated")
	}
	if err := store.DeleteProvider(ctx, prov.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	state, _ := store.Snapshot(ctx)
	for _, p := range state.Providers {
		if p.ID == prov.ID {
			t.Fatal("provider not deleted")
		}
	}
}

func TestMemoryStore_ModelCapabilityLifecycle(t *testing.T) {
	t.Parallel()
	runStoreModelCapabilityLifecycle(t, NewMemoryStore())
}

func TestMemoryStore_Prune(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	ctx := context.Background()

	// Generate audit events via policy-rule lifecycle.
	for i := 0; i < 5; i++ {
		_, _ = store.UpsertPolicyRule(ctx, config.PolicyRuleConfig{
			ID:     "rule-" + string(rune('a'+i)),
			Action: "deny",
			Reason: "test",
		})
	}
	state, _ := store.Snapshot(ctx)
	if len(state.Events) == 0 {
		t.Fatal("expected audit events from policy-rule lifecycle, got none")
	}

	// Prune to keep only most recent 1.
	deleted, err := store.Prune(ctx, 0, 1)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if deleted == 0 {
		t.Fatal("expected count-based prune to delete entries, got 0")
	}
}
