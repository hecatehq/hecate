package agentadapters

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/hecate/agent-runtime/internal/storage"
)

func newSQLiteTestStore(t *testing.T) *SQLiteApprovalStore {
	t.Helper()
	dir := t.TempDir()
	client, err := storage.NewSQLiteClient(context.Background(), storage.SQLiteConfig{
		Path:        filepath.Join(dir, "approvals.db"),
		TablePrefix: "test",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	store, err := NewSQLiteApprovalStore(context.Background(), client)
	if err != nil {
		t.Fatalf("NewSQLiteApprovalStore: %v", err)
	}
	return store
}

func TestSQLiteStoreConformance(t *testing.T) {
	RunConformanceTests(t, "SQLiteStore", func(t *testing.T) ApprovalRetentionStore {
		return newSQLiteTestStore(t)
	})
}

func TestSQLiteApprovalStorePersistsAcrossInstances(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "approvals.db")
	client, err := storage.NewSQLiteClient(context.Background(), storage.SQLiteConfig{
		Path:        path,
		TablePrefix: "test",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient: %v", err)
	}

	store, err := NewSQLiteApprovalStore(context.Background(), client)
	if err != nil {
		t.Fatalf("NewSQLiteApprovalStore: %v", err)
	}
	now := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)
	row, _ := store.CreateApproval(context.Background(), Approval{
		SessionID: "s", AdapterID: "codex", Status: ApprovalStatusPending,
		ACPOptions:   []ApprovalOption{{OptionID: "a", Kind: "allow_once", Name: "Allow"}},
		ScopeChoices: defaultScopeChoices(),
		CreatedAt:    now,
		ExpiresAt:    now.Add(time.Hour),
	})
	_ = client.Close()

	client2, err := storage.NewSQLiteClient(context.Background(), storage.SQLiteConfig{
		Path:        path,
		TablePrefix: "test",
	})
	if err != nil {
		t.Fatalf("re-open NewSQLiteClient: %v", err)
	}
	t.Cleanup(func() { _ = client2.Close() })

	store2, err := NewSQLiteApprovalStore(context.Background(), client2)
	if err != nil {
		t.Fatalf("re-open NewSQLiteApprovalStore: %v", err)
	}
	got, err := store2.GetApproval(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("GetApproval after re-open: %v", err)
	}
	if got.SessionID != "s" || got.Status != ApprovalStatusPending {
		t.Fatalf("post-reopen row corrupted: %+v", got)
	}
	// Reconcile-on-reopen pins the canonical disposition for orphaned
	// waiters across restart.
	reconcileAt := now.Add(time.Minute)
	count, err := store2.ReconcilePending(context.Background(), reconcileAt)
	if err != nil {
		t.Fatalf("ReconcilePending on reopen: %v", err)
	}
	if count != 1 {
		t.Fatalf("reconciled = %d, want 1", count)
	}
	final, err := store2.GetApproval(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("GetApproval after reconcile: %v", err)
	}
	if final.Status != ApprovalStatusTimedOut {
		t.Fatalf("post-reconcile status = %q, want timed_out", final.Status)
	}
	if final.Path != ApprovalResolutionPath("startup_reconcile") {
		t.Fatalf("post-reconcile path = %q, want startup_reconcile", final.Path)
	}
	if final.ResolvedAt == nil || !final.ResolvedAt.Equal(reconcileAt) {
		t.Fatalf("post-reconcile resolved_at = %v, want %v", final.ResolvedAt, reconcileAt)
	}
	if final.DecisionNote == "" {
		t.Fatal("post-reconcile decision_note must explain the disposition")
	}
}

// Compile-time guard: both backends must satisfy ApprovalRetentionStore.
var (
	_ ApprovalRetentionStore = (*MemoryApprovalStore)(nil)
	_ ApprovalRetentionStore = (*SQLiteApprovalStore)(nil)
)
