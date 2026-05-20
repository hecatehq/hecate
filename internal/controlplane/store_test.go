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
