package agentadapters

import (
	"context"
	"errors"
	"testing"
	"time"
)

// StoreFactory builds a fresh ApprovalRetentionStore for one
// conformance subtest. Each subtest gets its own factory invocation
// so backends with per-instance state (sqlite file under t.TempDir,
// fresh memory map) start clean. The factory is t.Helper()-friendly
// and may use t.Cleanup for teardown.
type StoreFactory func(t *testing.T) ApprovalRetentionStore

// RunConformanceTests exercises every ApprovalRetentionStore-interface
// contract against the backend the factory produces. Memory + sqlite
// invoke this with their own factory; new backends added later only
// need to supply a factory + one entry-point test, not duplicate every
// case body.
//
// Per-backend tests that exercise something the contract doesn't
// describe (sqlite reopen across instances, etc.) stay as standalone
// tests in their backend's _test.go.
func RunConformanceTests(t *testing.T, name string, factory StoreFactory) {
	t.Helper()
	t.Run(name+"/CreateAndGetApproval", func(t *testing.T) {
		t.Parallel()
		runStoreCreateAndGetApproval(t, factory(t))
	})
	t.Run(name+"/GetMissingReturnsNotFound", func(t *testing.T) {
		t.Parallel()
		runStoreGetMissingReturnsNotFound(t, factory(t))
	})
	t.Run(name+"/ResolveTransitionsAndDoubleResolveFails", func(t *testing.T) {
		t.Parallel()
		runStoreResolveTransitionsAndDoubleResolveFails(t, factory(t))
	})
	t.Run(name+"/ResolveUnknownIDReturnsNotFound", func(t *testing.T) {
		t.Parallel()
		runStoreResolveUnknownIDReturnsNotFound(t, factory(t))
	})
	t.Run(name+"/ListSortsOldestFirstAndFiltersStatus", func(t *testing.T) {
		t.Parallel()
		runStoreListSortsOldestFirstAndFiltersStatus(t, factory(t))
	})
	t.Run(name+"/FindMatchingGrantSpecificityAndExpiry", func(t *testing.T) {
		t.Parallel()
		runStoreFindMatchingGrantSpecificityAndExpiry(t, factory(t))
	})
	t.Run(name+"/ListGrantsFiltersAndSorts", func(t *testing.T) {
		t.Parallel()
		runStoreListGrantsFiltersAndSorts(t, factory(t))
	})
	t.Run(name+"/DeleteGrant", func(t *testing.T) {
		t.Parallel()
		runStoreDeleteGrant(t, factory(t))
	})
	t.Run(name+"/ReconcilePendingFlipsToTimedOut", func(t *testing.T) {
		t.Parallel()
		runStoreReconcilePendingFlipsToTimedOut(t, factory(t))
	})
	t.Run(name+"/PruneApprovalsByAge", func(t *testing.T) {
		t.Parallel()
		runStorePruneApprovalsByAge(t, factory(t))
	})
	t.Run(name+"/PruneApprovalsByCount", func(t *testing.T) {
		t.Parallel()
		runStorePruneApprovalsByCount(t, factory(t))
	})
	t.Run(name+"/NormalRetentionDoesNotPruneNonExpiringGrants", func(t *testing.T) {
		t.Parallel()
		runStoreNormalRetentionDoesNotPruneNonExpiringGrants(t, factory(t))
	})
	t.Run(name+"/PruneExpiredGrantsLeavesLiveOnes", func(t *testing.T) {
		t.Parallel()
		runStorePruneExpiredGrantsLeavesLiveOnes(t, factory(t))
	})
}

func runStoreCreateAndGetApproval(t *testing.T, store ApprovalRetentionStore) {
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
}

func runStoreGetMissingReturnsNotFound(t *testing.T, store ApprovalRetentionStore) {
	_, err := store.GetApproval(context.Background(), "missing")
	if !errors.Is(err, ErrApprovalNotFound) {
		t.Fatalf("got %v, want ErrApprovalNotFound", err)
	}
}

func runStoreResolveTransitionsAndDoubleResolveFails(t *testing.T, store ApprovalRetentionStore) {
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

	_, err = store.ResolveApproval(ctx, row.ID, ApprovalStatusDenied, ApprovalDecisionDeny, "", ApprovalScopeOnce, PathOperator, "", now)
	if !errors.Is(err, ErrApprovalAlreadyResolved) {
		t.Fatalf("got %v, want ErrApprovalAlreadyResolved", err)
	}
}

func runStoreResolveUnknownIDReturnsNotFound(t *testing.T, store ApprovalRetentionStore) {
	_, err := store.ResolveApproval(context.Background(), "missing",
		ApprovalStatusApproved, ApprovalDecisionApprove, "x",
		ApprovalScopeOnce, PathOperator, "", time.Now())
	if !errors.Is(err, ErrApprovalNotFound) {
		t.Fatalf("got %v, want ErrApprovalNotFound", err)
	}
}

func runStoreListSortsOldestFirstAndFiltersStatus(t *testing.T, store ApprovalRetentionStore) {
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

	_, _ = store.ResolveApproval(ctx, rows[0].ID, ApprovalStatusApproved, ApprovalDecisionApprove, "x", ApprovalScopeOnce, PathOperator, "", t0.Add(time.Hour))
	pending, _ := store.ListApprovals(ctx, "s", ApprovalStatusPending)
	if len(pending) != 2 {
		t.Fatalf("pending = %d, want 2", len(pending))
	}
	approved, _ := store.ListApprovals(ctx, "s", ApprovalStatusApproved)
	if len(approved) != 1 {
		t.Fatalf("approved = %d, want 1", len(approved))
	}
}

func runStoreFindMatchingGrantSpecificityAndExpiry(t *testing.T, store ApprovalRetentionStore) {
	ctx := context.Background()
	now := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)

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

	got, ok, _ = store.FindMatchingGrant(ctx, "other-session", "/tmp/w", "codex", ToolKindFileWrite, now)
	if !ok || got.Scope != ApprovalScopeWorkspaceTool {
		t.Fatalf("scope = %q, want workspace_tool", got.Scope)
	}

	got, ok, _ = store.FindMatchingGrant(ctx, "other", "/elsewhere", "codex", ToolKindFileWrite, now)
	if !ok || got.Scope != ApprovalScopeAdapterTool {
		t.Fatalf("scope = %q, want adapter_tool", got.Scope)
	}

	past := now.Add(-time.Hour)
	_, _ = store.CreateGrant(ctx, Grant{
		Scope: ApprovalScopeAdapterTool, AdapterID: "claude_code", ToolKind: ToolKindFileWrite,
		Decision: ApprovalDecisionApprove, GrantedAt: now.Add(-2 * time.Hour), ExpiresAt: &past,
	})
	_, ok, _ = store.FindMatchingGrant(ctx, "s", "", "claude_code", ToolKindFileWrite, now)
	if ok {
		t.Fatal("expired grant must not match")
	}
}

func runStoreListGrantsFiltersAndSorts(t *testing.T, store ApprovalRetentionStore) {
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
}

func runStoreDeleteGrant(t *testing.T, store ApprovalRetentionStore) {
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
}

func runStoreReconcilePendingFlipsToTimedOut(t *testing.T, store ApprovalRetentionStore) {
	ctx := context.Background()
	now := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)

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

	row, _ := store.GetApproval(ctx, alreadyResolved.ID)
	if row.Status != ApprovalStatusApproved {
		t.Fatalf("resolved row status = %q, want approved (untouched)", row.Status)
	}
}

func runStorePruneApprovalsByAge(t *testing.T, store ApprovalRetentionStore) {
	ctx := context.Background()
	now := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)

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
}

func runStorePruneApprovalsByCount(t *testing.T, store ApprovalRetentionStore) {
	ctx := context.Background()
	now := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)

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
	for i, id := range ids {
		_, err := store.GetApproval(ctx, id)
		if i < 3 && !errors.Is(err, ErrApprovalNotFound) {
			t.Fatalf("row %d (oldest) survived prune: err=%v", i, err)
		}
		if i >= 3 && err != nil {
			t.Fatalf("row %d (newest) erroneously pruned: %v", i, err)
		}
	}
}

// Operator-authored intent must outlive the retention window.
// PruneApprovals(MaxAge, MaxCount) only touches resolved approvals;
// grants are deleted exclusively by ExpiresAt via PruneExpiredGrants.
// This test pins that contract so a future "let's collapse pruning
// into one call" refactor can't quietly erase grants on a long-lived
// runtime.
func runStoreNormalRetentionDoesNotPruneNonExpiringGrants(t *testing.T, store ApprovalRetentionStore) {
	ctx := context.Background()
	now := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)

	_, _ = store.CreateGrant(ctx, Grant{
		Scope: ApprovalScopeAdapterTool, AdapterID: "codex", ToolKind: ToolKindFileWrite,
		Decision: ApprovalDecisionApprove, GrantedAt: now.Add(-365 * 24 * time.Hour),
	})

	if _, err := store.PruneApprovals(ctx, now, time.Hour, 1); err != nil {
		t.Fatalf("PruneApprovals: %v", err)
	}
	grants, _ := store.ListGrants(ctx, GrantFilter{}, now)
	if len(grants) != 1 {
		t.Fatalf("normal retention pruned a non-expiring grant; got %d, want 1", len(grants))
	}
}

func runStorePruneExpiredGrantsLeavesLiveOnes(t *testing.T, store ApprovalRetentionStore) {
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
}
