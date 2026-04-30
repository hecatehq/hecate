package controlplane

import (
	"context"
	"testing"

	"github.com/hecate/agent-runtime/internal/config"
)

func TestMemoryStore_TenantLifecycle(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	ctx := context.Background()

	tenant, err := store.UpsertTenant(ctx, Tenant{Name: "Acme", Description: "test"})
	if err != nil {
		t.Fatalf("UpsertTenant: %v", err)
	}
	if tenant.ID == "" {
		t.Fatal("tenant id not generated")
	}

	// Update.
	updated, err := store.UpsertTenant(ctx, Tenant{ID: tenant.ID, Name: "Acme Inc"})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "Acme Inc" {
		t.Fatalf("name = %q", updated.Name)
	}

	// Disable.
	disabled, err := store.SetTenantEnabled(ctx, tenant.ID, false)
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if disabled.Enabled {
		t.Fatal("should be disabled")
	}

	// Delete.
	if err := store.DeleteTenant(ctx, tenant.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	state, _ := store.Snapshot(ctx)
	if len(state.Tenants) != 0 {
		t.Fatalf("expected 0 tenants, got %d", len(state.Tenants))
	}
}

func TestMemoryStore_APIKeyLifecycle(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	ctx := context.Background()

	key, err := store.UpsertAPIKey(ctx, APIKey{
		Name: "ci-key",
		Key:  "hct_sk_initial_secret",
		Role: "tenant",
	})
	if err != nil {
		t.Fatalf("UpsertAPIKey: %v", err)
	}
	if key.ID == "" {
		t.Fatal("key id not generated")
	}

	// Disable.
	disabled, err := store.SetAPIKeyEnabled(ctx, key.ID, false)
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if disabled.Enabled {
		t.Fatal("should be disabled")
	}

	// Rotate.
	rotated, err := store.RotateAPIKey(ctx, key.ID, "hct_sk_new_secret")
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if rotated.Key == "hct_sk_initial_secret" {
		t.Fatal("key should have changed after rotate")
	}

	// Delete.
	if err := store.DeleteAPIKey(ctx, key.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	state, _ := store.Snapshot(ctx)
	for _, k := range state.APIKeys {
		if k.ID == key.ID {
			t.Fatal("key should be removed")
		}
	}
}

func TestMemoryStore_PolicyRuleLifecycle(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	ctx := context.Background()

	rule, err := store.UpsertPolicyRule(ctx, config.PolicyRuleConfig{
		ID:     "no-cloud-for-anon",
		Action: "deny",
		Reason: "anon users may not use cloud",
		Roles:  []string{"anonymous"},
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if rule.ID != "no-cloud-for-anon" {
		t.Fatalf("ID = %q", rule.ID)
	}
	if err := store.DeletePolicyRule(ctx, rule.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestMemoryStore_PricebookLifecycle(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	ctx := context.Background()

	entry, err := store.UpsertPricebookEntry(ctx, config.ModelPriceConfig{
		Provider:                        "openai",
		Model:                           "gpt-4o-mini",
		InputMicrosUSDPerMillionTokens:  150_000,
		OutputMicrosUSDPerMillionTokens: 600_000,
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := store.DeletePricebookEntry(ctx, entry.Provider, entry.Model); err != nil {
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

func TestMemoryStore_PruneAuditEvents(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	ctx := context.Background()

	// Generate some audit events via tenant lifecycle.
	for i := 0; i < 5; i++ {
		tenant, _ := store.UpsertTenant(ctx, Tenant{Name: "Acme"})
		_, _ = store.SetTenantEnabled(ctx, tenant.ID, i%2 == 0)
	}
	state, _ := store.Snapshot(ctx)
	// Memory store appends an audit event on every UpsertTenant /
	// SetTenantEnabled call (see appendAuditEvent in store_memory.go).
	// An empty slice here means the lifecycle hooks regressed.
	if len(state.Events) == 0 {
		t.Fatal("expected audit events from tenant lifecycle, got none")
	}

	// Prune to keep only most recent 1.
	deleted, err := store.PruneAuditEvents(ctx, 0, 1)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	// We just generated 5 lifecycle events × 2 (upsert + enable toggle),
	// so count-based pruning to 1 must have deleted at least one.
	if deleted == 0 {
		t.Fatal("expected count-based prune to delete entries, got 0")
	}
}
