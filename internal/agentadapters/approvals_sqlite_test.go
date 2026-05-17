package agentadapters

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/hecate/agent-runtime/internal/storage"
)

// newSQLiteTestStore opens an in-tempdir SQLite-backed approval store
// scrubbed at test cleanup. Same shape as the chat package
// test helpers so the parity suite can take either backend.
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

// ─── Parity suite ────────────────────────────────────────────────────────────
//
// One set of assertions against both backends. Each test in this file
// runs as t.Run("memory", …) and t.Run("sqlite", …) so backend
// behavior cannot drift silently.

func RunConformanceTests(t *testing.T, fn func(t *testing.T, store ApprovalStore)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) {
		t.Parallel()
		fn(t, NewMemoryApprovalStore())
	})
	t.Run("sqlite", func(t *testing.T) {
		t.Parallel()
		fn(t, newSQLiteTestStore(t))
	})
}

func TestParityCreateAndGetApproval(t *testing.T) {
	RunConformanceTests(t, func(t *testing.T, store ApprovalStore) {
		ctx := context.Background()
		now := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)
		row := Approval{
			SessionID:    "s1",
			AdapterID:    "codex",
			Workspace:    "/tmp/w",
			ToolKind:     ToolKindFileWrite,
			Status:       ApprovalStatusPending,
			ACPOptions:   []ApprovalOption{{OptionID: "allow_once_id", Kind: "allow_once", Name: "Allow once"}},
			ScopeChoices: defaultScopeChoices(),
			CreatedAt:    now,
			ExpiresAt:    now.Add(5 * time.Minute),
		}
		created, err := store.CreateApproval(ctx, row)
		if err != nil {
			t.Fatalf("CreateApproval: %v", err)
		}
		if created.ID == "" {
			t.Fatal("expected backend to assign id")
		}
		got, err := store.GetApproval(ctx, created.ID)
		if err != nil {
			t.Fatalf("GetApproval: %v", err)
		}
		if got.SessionID != "s1" || got.AdapterID != "codex" || got.ToolKind != ToolKindFileWrite {
			t.Fatalf("round-trip lost fields: %+v", got)
		}
		if len(got.ACPOptions) != 1 || got.ACPOptions[0].OptionID != "allow_once_id" {
			t.Fatalf("acp_options round-trip lost: %+v", got.ACPOptions)
		}
		if !got.CreatedAt.Equal(now) {
			t.Fatalf("created_at = %s, want %s", got.CreatedAt, now)
		}
	})
}

func TestParityGetMissingReturnsNotFound(t *testing.T) {
	RunConformanceTests(t, func(t *testing.T, store ApprovalStore) {
		_, err := store.GetApproval(context.Background(), "missing")
		if !errors.Is(err, ErrApprovalNotFound) {
			t.Fatalf("got %v, want ErrApprovalNotFound", err)
		}
	})
}

func TestParityResolveTransitionsAndDoubleResolveFails(t *testing.T) {
	RunConformanceTests(t, func(t *testing.T, store ApprovalStore) {
		ctx := context.Background()
		now := time.Now().UTC()
		row, _ := store.CreateApproval(ctx, Approval{
			SessionID: "s", AdapterID: "codex", Status: ApprovalStatusPending,
			ACPOptions: []ApprovalOption{}, ScopeChoices: defaultScopeChoices(),
			CreatedAt: now, ExpiresAt: now.Add(time.Minute),
		})
		resolved, err := store.ResolveApproval(ctx, row.ID, ApprovalStatusApproved, ApprovalDecisionApprove, "x", ApprovalScopeOnce, PathOperator, "ok", now.Add(time.Second))
		if err != nil {
			t.Fatalf("ResolveApproval: %v", err)
		}
		if resolved.Status != ApprovalStatusApproved {
			t.Fatalf("status = %q, want approved", resolved.Status)
		}
		if resolved.ResolvedAt == nil {
			t.Fatal("expected ResolvedAt to be set")
		}

		// Second resolve must fail with the shared sentinel — backends
		// must agree on append-only semantics.
		_, err = store.ResolveApproval(ctx, row.ID, ApprovalStatusDenied, ApprovalDecisionDeny, "", ApprovalScopeOnce, PathOperator, "", now)
		if !errors.Is(err, ErrApprovalAlreadyResolved) {
			t.Fatalf("got %v, want ErrApprovalAlreadyResolved", err)
		}
	})
}

func TestParityResolveUnknownIDReturnsNotFound(t *testing.T) {
	RunConformanceTests(t, func(t *testing.T, store ApprovalStore) {
		_, err := store.ResolveApproval(context.Background(), "missing",
			ApprovalStatusApproved, ApprovalDecisionApprove, "x",
			ApprovalScopeOnce, PathOperator, "", time.Now())
		if !errors.Is(err, ErrApprovalNotFound) {
			t.Fatalf("got %v, want ErrApprovalNotFound", err)
		}
	})
}

func TestParityListSortsOldestFirstAndFiltersStatus(t *testing.T) {
	RunConformanceTests(t, func(t *testing.T, store ApprovalStore) {
		ctx := context.Background()
		t0 := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)
		for i := 0; i < 3; i++ {
			_, _ = store.CreateApproval(ctx, Approval{
				SessionID: "s", AdapterID: "codex", Status: ApprovalStatusPending,
				ACPOptions: []ApprovalOption{}, ScopeChoices: defaultScopeChoices(),
				CreatedAt: t0.Add(time.Duration(i) * time.Second),
				ExpiresAt: t0.Add(time.Hour),
			})
		}
		rows, err := store.ListApprovals(ctx, "s", "")
		if err != nil {
			t.Fatalf("ListApprovals: %v", err)
		}
		if len(rows) != 3 {
			t.Fatalf("got %d rows, want 3", len(rows))
		}
		for i := 1; i < len(rows); i++ {
			if rows[i-1].CreatedAt.After(rows[i].CreatedAt) {
				t.Fatalf("rows not sorted oldest-first: %+v", rows)
			}
		}

		// Resolve one and re-query by status.
		_, _ = store.ResolveApproval(ctx, rows[0].ID, ApprovalStatusApproved, ApprovalDecisionApprove, "x", ApprovalScopeOnce, PathOperator, "", t0.Add(time.Hour))
		pending, _ := store.ListApprovals(ctx, "s", ApprovalStatusPending)
		if len(pending) != 2 {
			t.Fatalf("pending = %d, want 2", len(pending))
		}
		approved, _ := store.ListApprovals(ctx, "s", ApprovalStatusApproved)
		if len(approved) != 1 {
			t.Fatalf("approved = %d, want 1", len(approved))
		}
	})
}

func TestParityFindMatchingGrantSpecificityAndExpiry(t *testing.T) {
	RunConformanceTests(t, func(t *testing.T, store ApprovalStore) {
		ctx := context.Background()
		now := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)

		// Three grants of increasing breadth — most-specific must win.
		_, _ = store.CreateGrant(ctx, Grant{
			Scope: ApprovalScopeAdapterTool, AdapterID: "codex", ToolKind: ToolKindFileWrite,
			Decision: ApprovalDecisionApprove, GrantedAt: now,
		})
		_, _ = store.CreateGrant(ctx, Grant{
			Scope: ApprovalScopeWorkspaceTool, AdapterID: "codex", ToolKind: ToolKindFileWrite,
			Workspace: "/tmp/w", Decision: ApprovalDecisionDeny, GrantedAt: now,
		})
		_, _ = store.CreateGrant(ctx, Grant{
			Scope: ApprovalScopeSession, AdapterID: "codex", ToolKind: ToolKindFileWrite,
			SessionID: "s1", Decision: ApprovalDecisionApprove, GrantedAt: now,
		})

		got, ok, err := store.FindMatchingGrant(ctx, "s1", "/tmp/w", "codex", ToolKindFileWrite, now)
		if err != nil || !ok {
			t.Fatalf("expected match; ok=%v err=%v", ok, err)
		}
		if got.Scope != ApprovalScopeSession {
			t.Fatalf("scope = %q, want session (most specific)", got.Scope)
		}

		// Without the matching session, workspace_tool wins next.
		got, ok, _ = store.FindMatchingGrant(ctx, "other-session", "/tmp/w", "codex", ToolKindFileWrite, now)
		if !ok || got.Scope != ApprovalScopeWorkspaceTool {
			t.Fatalf("scope = %q, want workspace_tool", got.Scope)
		}

		// Without matching workspace, adapter_tool wins.
		got, ok, _ = store.FindMatchingGrant(ctx, "other", "/elsewhere", "codex", ToolKindFileWrite, now)
		if !ok || got.Scope != ApprovalScopeAdapterTool {
			t.Fatalf("scope = %q, want adapter_tool", got.Scope)
		}

		// Expired grant must not match.
		past := now.Add(-time.Hour)
		_, _ = store.CreateGrant(ctx, Grant{
			Scope: ApprovalScopeAdapterTool, AdapterID: "claude_code", ToolKind: ToolKindFileWrite,
			Decision: ApprovalDecisionApprove, GrantedAt: now.Add(-2 * time.Hour), ExpiresAt: &past,
		})
		_, ok, _ = store.FindMatchingGrant(ctx, "s", "", "claude_code", ToolKindFileWrite, now)
		if ok {
			t.Fatal("expired grant must not match")
		}
	})
}

func TestParityListGrantsFiltersAndSorts(t *testing.T) {
	RunConformanceTests(t, func(t *testing.T, store ApprovalStore) {
		ctx := context.Background()
		t0 := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)

		_, _ = store.CreateGrant(ctx, Grant{
			Scope: ApprovalScopeAdapterTool, AdapterID: "codex", ToolKind: ToolKindFileWrite,
			Decision: ApprovalDecisionApprove, GrantedAt: t0,
		})
		_, _ = store.CreateGrant(ctx, Grant{
			Scope: ApprovalScopeAdapterTool, AdapterID: "claude_code", ToolKind: ToolKindShellExec,
			Decision: ApprovalDecisionDeny, GrantedAt: t0.Add(time.Second),
		})

		all, _ := store.ListGrants(ctx, GrantFilter{}, t0.Add(time.Hour))
		if len(all) != 2 {
			t.Fatalf("got %d, want 2", len(all))
		}
		if !all[0].GrantedAt.After(all[1].GrantedAt) {
			t.Fatal("expected newest-first")
		}

		filtered, _ := store.ListGrants(ctx, GrantFilter{AdapterID: "codex"}, t0.Add(time.Hour))
		if len(filtered) != 1 || filtered[0].AdapterID != "codex" {
			t.Fatalf("filter by adapter failed: %+v", filtered)
		}
	})
}

func TestParityDeleteGrant(t *testing.T) {
	RunConformanceTests(t, func(t *testing.T, store ApprovalStore) {
		ctx := context.Background()
		g, _ := store.CreateGrant(ctx, Grant{
			Scope: ApprovalScopeAdapterTool, AdapterID: "codex", ToolKind: ToolKindFileWrite,
			Decision: ApprovalDecisionApprove, GrantedAt: time.Now().UTC(),
		})
		if err := store.DeleteGrant(ctx, g.ID); err != nil {
			t.Fatalf("DeleteGrant: %v", err)
		}
		if err := store.DeleteGrant(ctx, g.ID); !errors.Is(err, ErrApprovalNotFound) {
			t.Fatalf("second delete = %v, want ErrApprovalNotFound", err)
		}
	})
}

// ─── Reconcile + retention parity ────────────────────────────────────────────

func runRetentionParity(t *testing.T, fn func(t *testing.T, store ApprovalRetentionStore)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) {
		t.Parallel()
		fn(t, NewMemoryApprovalStore())
	})
	t.Run("sqlite", func(t *testing.T) {
		t.Parallel()
		fn(t, newSQLiteTestStore(t))
	})
}

func TestParityReconcilePendingFlipsToTimedOut(t *testing.T) {
	runRetentionParity(t, func(t *testing.T, store ApprovalRetentionStore) {
		ctx := context.Background()
		now := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)

		// Two pending rows + one already-resolved row; reconcile must
		// flip only the pending ones.
		pending1, _ := store.CreateApproval(ctx, Approval{
			SessionID: "s", AdapterID: "codex", Status: ApprovalStatusPending,
			ACPOptions: []ApprovalOption{}, ScopeChoices: defaultScopeChoices(),
			CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Minute),
		})
		pending2, _ := store.CreateApproval(ctx, Approval{
			SessionID: "s", AdapterID: "codex", Status: ApprovalStatusPending,
			ACPOptions: []ApprovalOption{}, ScopeChoices: defaultScopeChoices(),
			CreatedAt: now.Add(-30 * time.Minute), ExpiresAt: now.Add(time.Minute),
		})
		alreadyResolved, _ := store.CreateApproval(ctx, Approval{
			SessionID: "s", AdapterID: "codex", Status: ApprovalStatusPending,
			ACPOptions: []ApprovalOption{}, ScopeChoices: defaultScopeChoices(),
			CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(time.Minute),
		})
		_, _ = store.ResolveApproval(ctx, alreadyResolved.ID, ApprovalStatusApproved, ApprovalDecisionApprove, "x", ApprovalScopeOnce, PathOperator, "", now.Add(-time.Hour))

		count, err := store.ReconcilePending(ctx, now)
		if err != nil {
			t.Fatalf("ReconcilePending: %v", err)
		}
		if count != 2 {
			t.Fatalf("reconciled %d, want 2 (only pending rows)", count)
		}

		for _, id := range []string{pending1.ID, pending2.ID} {
			row, _ := store.GetApproval(ctx, id)
			if row.Status != ApprovalStatusTimedOut {
				t.Fatalf("row %s status = %q, want timed_out", id, row.Status)
			}
			if row.Path != ApprovalResolutionPath("startup_reconcile") {
				t.Fatalf("row %s path = %q, want startup_reconcile", id, row.Path)
			}
			if row.ResolvedAt == nil || !row.ResolvedAt.Equal(now) {
				t.Fatalf("row %s resolved_at = %v, want %v", id, row.ResolvedAt, now)
			}
			if row.DecisionNote == "" {
				t.Fatalf("row %s missing reconcile note", id)
			}
		}

		// The previously-resolved row must NOT be touched.
		row, _ := store.GetApproval(ctx, alreadyResolved.ID)
		if row.Status != ApprovalStatusApproved {
			t.Fatalf("resolved row status = %q, want approved (untouched)", row.Status)
		}
	})
}

func TestParityPruneApprovalsByAge(t *testing.T) {
	runRetentionParity(t, func(t *testing.T, store ApprovalRetentionStore) {
		ctx := context.Background()
		now := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)

		// One old resolved + one fresh resolved + one pending
		// (must NEVER be pruned regardless of age).
		old, _ := store.CreateApproval(ctx, Approval{
			SessionID: "s", AdapterID: "codex", Status: ApprovalStatusPending,
			ACPOptions: []ApprovalOption{}, ScopeChoices: defaultScopeChoices(),
			CreatedAt: now.Add(-72 * time.Hour), ExpiresAt: now,
		})
		_, _ = store.ResolveApproval(ctx, old.ID, ApprovalStatusApproved, ApprovalDecisionApprove, "x", ApprovalScopeOnce, PathOperator, "", now.Add(-71*time.Hour))

		fresh, _ := store.CreateApproval(ctx, Approval{
			SessionID: "s", AdapterID: "codex", Status: ApprovalStatusPending,
			ACPOptions: []ApprovalOption{}, ScopeChoices: defaultScopeChoices(),
			CreatedAt: now.Add(-time.Hour), ExpiresAt: now,
		})
		_, _ = store.ResolveApproval(ctx, fresh.ID, ApprovalStatusApproved, ApprovalDecisionApprove, "x", ApprovalScopeOnce, PathOperator, "", now)

		pending, _ := store.CreateApproval(ctx, Approval{
			SessionID: "s", AdapterID: "codex", Status: ApprovalStatusPending,
			ACPOptions: []ApprovalOption{}, ScopeChoices: defaultScopeChoices(),
			CreatedAt: now.Add(-100 * time.Hour), ExpiresAt: now,
		})

		deleted, err := store.PruneApprovals(ctx, now, 24*time.Hour, 0)
		if err != nil {
			t.Fatalf("PruneApprovals: %v", err)
		}
		if deleted != 1 {
			t.Fatalf("deleted = %d, want 1 (the old resolved row)", deleted)
		}
		if _, err := store.GetApproval(ctx, old.ID); !errors.Is(err, ErrApprovalNotFound) {
			t.Fatalf("expected old row deleted; got %v", err)
		}
		if _, err := store.GetApproval(ctx, fresh.ID); err != nil {
			t.Fatalf("fresh row erroneously deleted: %v", err)
		}
		if _, err := store.GetApproval(ctx, pending.ID); err != nil {
			t.Fatalf("PENDING row deleted by age prune (must never happen): %v", err)
		}
	})
}

func TestParityPruneApprovalsByCount(t *testing.T) {
	runRetentionParity(t, func(t *testing.T, store ApprovalRetentionStore) {
		ctx := context.Background()
		now := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)

		// Five resolved rows with monotonic created_at; keep newest 2.
		ids := make([]string, 5)
		for i := 0; i < 5; i++ {
			row, _ := store.CreateApproval(ctx, Approval{
				SessionID: "s", AdapterID: "codex", Status: ApprovalStatusPending,
				ACPOptions: []ApprovalOption{}, ScopeChoices: defaultScopeChoices(),
				CreatedAt: now.Add(time.Duration(i) * time.Minute),
				ExpiresAt: now.Add(time.Hour),
			})
			ids[i] = row.ID
			_, _ = store.ResolveApproval(ctx, row.ID, ApprovalStatusApproved, ApprovalDecisionApprove, "x", ApprovalScopeOnce, PathOperator, "", now.Add(time.Duration(i)*time.Minute+time.Second))
		}
		deleted, err := store.PruneApprovals(ctx, now, 0, 2)
		if err != nil {
			t.Fatalf("PruneApprovals: %v", err)
		}
		if deleted != 3 {
			t.Fatalf("deleted = %d, want 3 (5 - kept 2)", deleted)
		}
		// Three oldest must be gone; two newest must survive.
		for i, id := range ids {
			_, err := store.GetApproval(ctx, id)
			if i < 3 && !errors.Is(err, ErrApprovalNotFound) {
				t.Fatalf("row %d (oldest) survived prune: err=%v", i, err)
			}
			if i >= 3 && err != nil {
				t.Fatalf("row %d (newest) erroneously pruned: %v", i, err)
			}
		}
	})
}

func TestParityNormalRetentionDoesNotPruneNonExpiringGrants(t *testing.T) {
	// Operator-authored intent must outlive the retention window.
	// PruneApprovals(MaxAge, MaxCount) only touches resolved
	// approvals; grants are deleted exclusively by ExpiresAt via
	// PruneExpiredGrants. This test pins that contract so a future
	// "let's collapse pruning into one call" refactor can't quietly
	// erase grants on a long-lived gateway.
	runRetentionParity(t, func(t *testing.T, store ApprovalRetentionStore) {
		ctx := context.Background()
		now := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)

		// One grant with no ExpiresAt, granted long ago.
		_, _ = store.CreateGrant(ctx, Grant{
			Scope: ApprovalScopeAdapterTool, AdapterID: "codex", ToolKind: ToolKindFileWrite,
			Decision: ApprovalDecisionApprove, GrantedAt: now.Add(-365 * 24 * time.Hour),
		})

		// Aggressive normal retention pass: 1h max age, 1 max row.
		// PruneApprovals must not touch the grants table at all.
		if _, err := store.PruneApprovals(ctx, now, time.Hour, 1); err != nil {
			t.Fatalf("PruneApprovals: %v", err)
		}
		grants, _ := store.ListGrants(ctx, GrantFilter{}, now)
		if len(grants) != 1 {
			t.Fatalf("normal retention pruned a non-expiring grant; got %d, want 1", len(grants))
		}
	})
}

func TestParityPruneExpiredGrantsLeavesLiveOnes(t *testing.T) {
	runRetentionParity(t, func(t *testing.T, store ApprovalRetentionStore) {
		ctx := context.Background()
		now := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)

		past := now.Add(-time.Hour)
		future := now.Add(time.Hour)
		expired, _ := store.CreateGrant(ctx, Grant{
			Scope: ApprovalScopeAdapterTool, AdapterID: "codex", ToolKind: ToolKindFileWrite,
			Decision: ApprovalDecisionApprove, GrantedAt: now.Add(-2 * time.Hour), ExpiresAt: &past,
		})
		live, _ := store.CreateGrant(ctx, Grant{
			Scope: ApprovalScopeAdapterTool, AdapterID: "codex", ToolKind: ToolKindShellExec,
			Decision: ApprovalDecisionApprove, GrantedAt: now, ExpiresAt: &future,
		})
		eternal, _ := store.CreateGrant(ctx, Grant{
			Scope: ApprovalScopeAdapterTool, AdapterID: "claude_code", ToolKind: ToolKindFileRead,
			Decision: ApprovalDecisionApprove, GrantedAt: now,
		})

		deleted, err := store.PruneExpiredGrants(ctx, now)
		if err != nil {
			t.Fatalf("PruneExpiredGrants: %v", err)
		}
		if deleted != 1 {
			t.Fatalf("deleted = %d, want 1 (only the expired grant)", deleted)
		}

		grants, _ := store.ListGrants(ctx, GrantFilter{}, now)
		ids := make(map[string]bool, len(grants))
		for _, g := range grants {
			ids[g.ID] = true
		}
		if ids[expired.ID] {
			t.Fatalf("expired grant survived prune: %+v", grants)
		}
		if !ids[live.ID] {
			t.Fatal("live grant erroneously pruned")
		}
		if !ids[eternal.ID] {
			t.Fatal("never-expires grant erroneously pruned")
		}
	})
}

// ─── End-to-end SQLite flow ──────────────────────────────────────────────────

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

	// Re-open against the same path.
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
	// And the reconcile pass on second open should flip the surviving
	// pending row to timed_out / path=startup_reconcile. Locks the
	// canonical disposition for orphaned waiters across restart.
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

// avoid "imported and not used" if a future refactor drops a symbol.
var _ = acp.PermissionOptionKindAllowOnce
