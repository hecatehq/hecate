package controlplane

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hecate/agent-runtime/internal/config"
	"github.com/hecate/agent-runtime/internal/storage"
)

func newSQLiteTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dir := t.TempDir()
	client, err := storage.NewSQLiteClient(context.Background(), storage.SQLiteConfig{
		Path:        filepath.Join(dir, "controlplane.db"),
		TablePrefix: "test",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	store, err := NewSQLiteStore(context.Background(), client, "")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	return store
}

func TestSQLiteStore_RejectsNilClient(t *testing.T) {
	_, err := NewSQLiteStore(context.Background(), nil, "")
	if err == nil {
		t.Fatal("expected error for nil client")
	}
}

func TestSQLiteStore_BackendName(t *testing.T) {
	t.Parallel()
	store := newSQLiteTestStore(t)
	if got := store.Backend(); got != "sqlite" {
		t.Fatalf("Backend() = %q, want sqlite", got)
	}
}

func TestSQLiteStore_SnapshotEmptyOnFreshDatabase(t *testing.T) {
	t.Parallel()
	// A freshly-migrated SQLite store with no rows yet must return a
	// zero-value State, not an error — the gateway boots with an empty
	// control plane on day one.
	store := newSQLiteTestStore(t)
	state, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(state.Providers) != 0 || len(state.PolicyRules) != 0 || len(state.Pricebook) != 0 {
		t.Fatalf("expected empty state, got %+v", state)
	}
}

func TestSQLiteStore_PolicyRuleRoundTrip(t *testing.T) {
	t.Parallel()
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	rule, err := store.UpsertPolicyRule(ctx, config.PolicyRuleConfig{
		ID:     "deny-cloud-no-creds",
		Action: "deny",
		Reason: "no credentials configured",
	})
	if err != nil {
		t.Fatalf("UpsertPolicyRule: %v", err)
	}

	state, err := store.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(state.PolicyRules) != 1 || state.PolicyRules[0].ID != rule.ID {
		t.Fatalf("snapshot rules = %+v", state.PolicyRules)
	}
	// Every mutation appends an audit event; the JSON round-trip
	// must preserve them.
	if len(state.Events) == 0 {
		t.Fatalf("expected audit events, got 0")
	}

	if err := store.DeletePolicyRule(ctx, rule.ID); err != nil {
		t.Fatalf("DeletePolicyRule: %v", err)
	}
	state, _ = store.Snapshot(ctx)
	if len(state.PolicyRules) != 0 {
		t.Fatalf("expected 0 rules after delete, got %d", len(state.PolicyRules))
	}
}
