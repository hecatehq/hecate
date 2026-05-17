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
	if err := store.DeletePolicyRule(ctx, rule.ID); err != nil {
		t.Fatalf("DeletePolicyRule() error = %v", err)
	}

	state, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(state.Events) != 2 {
		t.Fatalf("event count = %d, want 2", len(state.Events))
	}
	if state.Events[0].Actor != "operator:req-123" {
		t.Fatalf("event actor = %q, want operator:req-123", state.Events[0].Actor)
	}
	if state.Events[0].Action != "policy_rule.created" {
		t.Fatalf("first event action = %q, want policy_rule.created", state.Events[0].Action)
	}
	if state.Events[1].Action != "policy_rule.deleted" {
		t.Fatalf("second event action = %q, want policy_rule.deleted", state.Events[1].Action)
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

// runStoreInstalledModelLifecycle exercises the create / update /
// delete path on the InstalledModel surface. Run by both the memory
// and sqlite store test suites so the audit semantics and field
// preservation stay in sync.
func runStoreInstalledModelLifecycle(t *testing.T, store Store) {
	t.Helper()
	ctx := WithActor(context.Background(), "operator:installer-test")

	first, err := store.UpsertInstalledModel(ctx, InstalledModel{
		ID:                 "qwen2.5-0_5b-q4_k_m",
		DisplayName:        "Qwen 2.5 0.5B Instruct (Q4_K_M)",
		FilePath:           "models/qwen2.5-0_5b-q4_k_m.gguf",
		SourceURL:          "https://huggingface.co/example/repo/resolve/main/qwen.gguf",
		SHA256:             "deadbeef",
		SizeBytes:          398_000_000,
		RecommendedContext: 8192,
		Capabilities: InstalledModelCapabilities{
			Streaming:        true,
			ToolCalling:      "none",
			MaxContextTokens: 32768,
		},
	})
	if err != nil {
		t.Fatalf("Upsert (create): %v", err)
	}
	if first.InstalledAt.IsZero() {
		t.Fatal("InstalledAt should default to time.Now on first write")
	}

	// Refresh the same row — InstalledAt must be preserved, audit
	// event must record an update rather than a second create.
	original := first.InstalledAt
	updated, err := store.UpsertInstalledModel(ctx, InstalledModel{
		ID:        first.ID,
		FilePath:  first.FilePath,
		SourceURL: first.SourceURL,
		SHA256:    "newhash",
	})
	if err != nil {
		t.Fatalf("Upsert (update): %v", err)
	}
	if !updated.InstalledAt.Equal(original) {
		t.Fatalf("InstalledAt mutated on update: %v → %v", original, updated.InstalledAt)
	}
	if updated.SHA256 != "newhash" {
		t.Fatalf("update did not persist SHA256: got %q", updated.SHA256)
	}

	state, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(state.InstalledModels) != 1 {
		t.Fatalf("InstalledModels count = %d, want 1", len(state.InstalledModels))
	}

	// Two audit events expected: created + updated.
	createdCount, updatedCount := 0, 0
	for _, event := range state.Events {
		if event.TargetType != "installed_model" || event.TargetID != first.ID {
			continue
		}
		switch event.Action {
		case "installed_model.created":
			createdCount++
		case "installed_model.updated":
			updatedCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("created audit events = %d, want 1", createdCount)
	}
	if updatedCount != 1 {
		t.Fatalf("updated audit events = %d, want 1", updatedCount)
	}

	if err := store.DeleteInstalledModel(ctx, first.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	state, _ = store.Snapshot(context.Background())
	if len(state.InstalledModels) != 0 {
		t.Fatalf("InstalledModels after delete = %+v, want empty", state.InstalledModels)
	}

	// Delete is idempotent — second call on the same id is a no-op.
	if err := store.DeleteInstalledModel(ctx, first.ID); err != nil {
		t.Fatalf("second Delete should no-op, got: %v", err)
	}

	// Empty id is rejected so the handler can rely on it.
	if _, err := store.UpsertInstalledModel(ctx, InstalledModel{FilePath: "models/x.gguf"}); err == nil {
		t.Fatal("Upsert with empty id should error")
	}
	if err := store.DeleteInstalledModel(ctx, ""); err == nil {
		t.Fatal("Delete with empty id should error")
	}
}
