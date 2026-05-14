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
	if len(state.Providers) != 0 || len(state.PolicyRules) != 0 {
		t.Fatalf("expected empty state, got %+v", state)
	}
}

func TestSQLiteStore_ModelCapabilityLifecycle(t *testing.T) {
	t.Parallel()
	runStoreModelCapabilityLifecycle(t, newSQLiteTestStore(t))
}

func TestSQLiteStore_InstalledModelLifecycle(t *testing.T) {
	t.Parallel()
	runStoreInstalledModelLifecycle(t, newSQLiteTestStore(t))
}

// Verifies the JSON round-trip preserves InstalledModel rows across
// a Snapshot, matching the existing pattern used by
// TestSQLiteStore_PolicyRuleRoundTrip.
func TestSQLiteStore_InstalledModelRoundTrip(t *testing.T) {
	t.Parallel()
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	written, err := store.UpsertInstalledModel(ctx, InstalledModel{
		ID:       "round-trip",
		FilePath: "models/round-trip.gguf",
		SHA256:   "abc123",
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	state, err := store.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(state.InstalledModels) != 1 {
		t.Fatalf("InstalledModels round-trip count = %d, want 1", len(state.InstalledModels))
	}
	got := state.InstalledModels[0]
	if got.ID != "round-trip" || got.SHA256 != "abc123" || got.FilePath != "models/round-trip.gguf" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if got.InstalledAt.IsZero() || !got.InstalledAt.Equal(written.InstalledAt) {
		t.Fatalf("InstalledAt round-trip mismatch: write=%v read=%v", written.InstalledAt, got.InstalledAt)
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
