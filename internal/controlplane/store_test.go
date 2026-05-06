package controlplane

import (
	"context"
	"testing"

	"github.com/hecate/agent-runtime/internal/config"
)

func TestMemoryStoreAuditEventsCaptureActorAndMutationTrail(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore()

	ctx := WithActor(context.Background(), "operator:req-123")
	rule, err := store.UpsertPolicyRule(ctx, config.PolicyRuleConfig{
		ID:     "deny-cloud",
		Action: "deny",
		Reason: "test",
	})
	if err != nil {
		t.Fatalf("UpsertPolicyRule() error = %v", err)
	}
	if _, err := store.UpsertPricebookEntry(ctx, config.ModelPriceConfig{
		Provider:                        "openai",
		Model:                           "gpt-4o-mini",
		InputMicrosUSDPerMillionTokens:  150_000,
		OutputMicrosUSDPerMillionTokens: 600_000,
	}); err != nil {
		t.Fatalf("UpsertPricebookEntry() error = %v", err)
	}
	if err := store.DeletePolicyRule(ctx, rule.ID); err != nil {
		t.Fatalf("DeletePolicyRule() error = %v", err)
	}

	state, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(state.Events) != 3 {
		t.Fatalf("event count = %d, want 3", len(state.Events))
	}
	if state.Events[0].Actor != "operator:req-123" {
		t.Fatalf("event actor = %q, want operator:req-123", state.Events[0].Actor)
	}
	if state.Events[0].Action != "policy_rule.created" {
		t.Fatalf("first event action = %q, want policy_rule.created", state.Events[0].Action)
	}
	if state.Events[2].Action != "policy_rule.deleted" {
		t.Fatalf("third event action = %q, want policy_rule.deleted", state.Events[2].Action)
	}
}

func runStoreModelCapabilityLifecycle(t *testing.T, store Store) {
	t.Helper()
	ctx := WithActor(context.Background(), "operator:req-cap")
	streaming := true

	probe, err := store.UpsertModelCapabilityProbe(ctx, ModelCapabilityRecord{
		Provider:         "ollama",
		Model:            "llama3.1:8b",
		ToolCalling:      "basic",
		Streaming:        &streaming,
		MaxContextTokens: 128000,
		Note:             "manual smoke passed",
	})
	if err != nil {
		t.Fatalf("UpsertModelCapabilityProbe: %v", err)
	}
	if probe.Source != "probe" || probe.UpdatedAt.IsZero() {
		t.Fatalf("probe record = %+v, want source=probe with timestamp", probe)
	}

	override, err := store.UpsertModelCapabilityOverride(ctx, ModelCapabilityRecord{
		Provider:    "ollama",
		Model:       "llama3.1:8b",
		ToolCalling: "parallel",
		Note:        "operator knows this model",
	})
	if err != nil {
		t.Fatalf("UpsertModelCapabilityOverride: %v", err)
	}
	if override.Source != "operator_override" || override.UpdatedAt.IsZero() {
		t.Fatalf("override record = %+v, want source=operator_override with timestamp", override)
	}

	state, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(state.ModelCapabilityProbeState) != 1 || state.ModelCapabilityProbeState[0].ToolCalling != "basic" {
		t.Fatalf("probe snapshot = %+v", state.ModelCapabilityProbeState)
	}
	if len(state.ModelCapabilityOverrides) != 1 || state.ModelCapabilityOverrides[0].ToolCalling != "parallel" {
		t.Fatalf("override snapshot = %+v", state.ModelCapabilityOverrides)
	}

	if err := store.DeleteModelCapabilityOverride(ctx, "ollama", "llama3.1:8b"); err != nil {
		t.Fatalf("DeleteModelCapabilityOverride: %v", err)
	}
	state, err = store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot after delete: %v", err)
	}
	if len(state.ModelCapabilityOverrides) != 0 {
		t.Fatalf("overrides after delete = %+v, want empty", state.ModelCapabilityOverrides)
	}
	if len(state.ModelCapabilityProbeState) != 1 {
		t.Fatalf("probe state after override delete = %+v, want preserved probe", state.ModelCapabilityProbeState)
	}
}
