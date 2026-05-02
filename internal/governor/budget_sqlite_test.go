package governor

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/hecate/agent-runtime/internal/storage"
)

func newSQLiteBudgetTestStore(t *testing.T) *SQLiteBudgetStore {
	t.Helper()
	dir := t.TempDir()
	client, err := storage.NewSQLiteClient(context.Background(), storage.SQLiteConfig{
		Path:        filepath.Join(dir, "budget.db"),
		TablePrefix: "test",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	store, err := NewSQLiteBudgetStore(context.Background(), client)
	if err != nil {
		t.Fatalf("NewSQLiteBudgetStore: %v", err)
	}
	return store
}

func TestSQLiteBudgetStore_RejectsNilClient(t *testing.T) {
	_, err := NewSQLiteBudgetStore(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil client")
	}
}

func TestSQLiteBudgetStore_SnapshotMissing(t *testing.T) {
	t.Parallel()
	store := newSQLiteBudgetTestStore(t)
	_, ok, err := store.Snapshot(context.Background(), "never-set")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if ok {
		t.Fatal("ok = true for missing key")
	}
}

func TestSQLiteBudgetStore_DebitCreditRoundTrip(t *testing.T) {
	t.Parallel()
	store := newSQLiteBudgetTestStore(t)
	ctx := context.Background()

	// Initial credit of $1.00.
	state, err := store.Credit(ctx, "global", 1_000_000)
	if err != nil {
		t.Fatalf("Credit: %v", err)
	}
	if state.BalanceMicrosUSD != 1_000_000 {
		t.Fatalf("balance after credit = %d, want 1_000_000", state.BalanceMicrosUSD)
	}
	if state.CreditedMicrosUSD != 1_000_000 {
		t.Fatalf("credited = %d, want 1_000_000", state.CreditedMicrosUSD)
	}

	// Snapshot round-trip.
	got, ok, err := store.Snapshot(ctx, "global")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !ok {
		t.Fatal("Snapshot ok = false after Credit")
	}
	if got.BalanceMicrosUSD != 1_000_000 || got.CreditedMicrosUSD != 1_000_000 {
		t.Fatalf("snapshot mismatch: %+v", got)
	}

	// Debit half.
	state, err = store.Debit(ctx, UsageEvent{
		BudgetKey:  "global",
		CostMicros: 500_000,
		OccurredAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("Debit: %v", err)
	}
	if state.BalanceMicrosUSD != 500_000 {
		t.Fatalf("balance after debit = %d, want 500_000", state.BalanceMicrosUSD)
	}
	if state.DebitedMicrosUSD != 500_000 {
		t.Fatalf("debited = %d, want 500_000", state.DebitedMicrosUSD)
	}
	if state.CreditedMicrosUSD != 1_000_000 {
		t.Fatalf("credited unchanged after debit = %d, want 1_000_000", state.CreditedMicrosUSD)
	}
}

func TestSQLiteBudgetStore_DebitNoOpOnEmptyKeyOrZeroCost(t *testing.T) {
	t.Parallel()
	store := newSQLiteBudgetTestStore(t)
	ctx := context.Background()

	// Empty key: no row written.
	if _, err := store.Debit(ctx, UsageEvent{BudgetKey: "", CostMicros: 100}); err != nil {
		t.Fatalf("Debit(empty key): %v", err)
	}
	// Zero cost: no row written.
	if _, err := store.Debit(ctx, UsageEvent{BudgetKey: "k", CostMicros: 0}); err != nil {
		t.Fatalf("Debit(zero cost): %v", err)
	}
	if _, ok, _ := store.Snapshot(ctx, "k"); ok {
		t.Fatal("zero-cost debit created a row")
	}
}

func TestSQLiteBudgetStore_SequentialDebitsAccumulate(t *testing.T) {
	t.Parallel()
	store := newSQLiteBudgetTestStore(t)
	ctx := context.Background()

	if _, err := store.Credit(ctx, "k", 1_000_000); err != nil {
		t.Fatalf("Credit: %v", err)
	}

	// Sequential debits exercise the atomic-UPDATE path. With the
	// single-statement upsert this round-trips cleanly; a regression
	// to read-modify-write would still pass single-threaded but lose
	// updates under contention.
	const debits = 50
	for i := 0; i < debits; i++ {
		if _, err := store.Debit(ctx, UsageEvent{BudgetKey: "k", CostMicros: 1_000}); err != nil {
			t.Fatalf("Debit[%d]: %v", i, err)
		}
	}

	state, ok, err := store.Snapshot(ctx, "k")
	if err != nil || !ok {
		t.Fatalf("Snapshot: ok=%v err=%v", ok, err)
	}
	wantBalance := int64(1_000_000 - debits*1_000)
	if state.BalanceMicrosUSD != wantBalance {
		t.Fatalf("balance = %d, want %d (no lost updates)", state.BalanceMicrosUSD, wantBalance)
	}
	if state.DebitedMicrosUSD != int64(debits*1_000) {
		t.Fatalf("debited = %d, want %d", state.DebitedMicrosUSD, debits*1_000)
	}
}

func TestSQLiteBudgetStore_SetBalanceOverwrites(t *testing.T) {
	t.Parallel()
	store := newSQLiteBudgetTestStore(t)
	ctx := context.Background()

	if _, err := store.Credit(ctx, "k", 100); err != nil {
		t.Fatalf("Credit: %v", err)
	}
	state, err := store.SetBalance(ctx, "k", 50_000)
	if err != nil {
		t.Fatalf("SetBalance: %v", err)
	}
	if state.BalanceMicrosUSD != 50_000 {
		t.Fatalf("balance after SetBalance = %d, want 50_000", state.BalanceMicrosUSD)
	}

	// SetBalance on a missing key creates the row.
	state, err = store.SetBalance(ctx, "fresh", 7)
	if err != nil {
		t.Fatalf("SetBalance(fresh): %v", err)
	}
	if state.BalanceMicrosUSD != 7 || state.Key != "fresh" {
		t.Fatalf("SetBalance(fresh) = %+v, want balance=7 key=fresh", state)
	}
}

func TestSQLiteBudgetStore_Reset(t *testing.T) {
	t.Parallel()
	store := newSQLiteBudgetTestStore(t)
	ctx := context.Background()

	if _, err := store.Credit(ctx, "k", 1_000); err != nil {
		t.Fatalf("Credit: %v", err)
	}
	if _, err := store.Debit(ctx, UsageEvent{BudgetKey: "k", CostMicros: 200}); err != nil {
		t.Fatalf("Debit: %v", err)
	}
	if err := store.Reset(ctx, "k"); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	state, ok, err := store.Snapshot(ctx, "k")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !ok {
		t.Fatal("Reset removed the row instead of zeroing it")
	}
	if state.BalanceMicrosUSD != 0 || state.CreditedMicrosUSD != 0 || state.DebitedMicrosUSD != 0 {
		t.Fatalf("Reset did not zero state: %+v", state)
	}
}

func TestSQLiteBudgetStore_AppendAndListEvents(t *testing.T) {
	t.Parallel()
	store := newSQLiteBudgetTestStore(t)
	ctx := context.Background()

	base := time.Now().UTC()
	for i := 0; i < 5; i++ {
		if err := store.AppendEvent(ctx, BudgetEvent{
			Key:             "global",
			Type:            "debit",
			Provider:        "openai",
			Model:           "gpt-4o",
			AmountMicrosUSD: int64(i * 100),
			OccurredAt:      base.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("AppendEvent[%d]: %v", i, err)
		}
	}

	events, err := store.ListEvents(ctx, "global", 10)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("ListEvents len = %d, want 5", len(events))
	}
	// Newest first — index 4 (the largest amount) should land at events[0].
	if events[0].AmountMicrosUSD != 400 {
		t.Fatalf("ListEvents[0].Amount = %d, want 400 (newest first)", events[0].AmountMicrosUSD)
	}
	// Round-trip a non-trivial column.
	if events[0].Provider != "openai" || events[0].Model != "gpt-4o" {
		t.Fatalf("event round-trip lost columns: %+v", events[0])
	}

	// Recent across all keys.
	if err := store.AppendEvent(ctx, BudgetEvent{
		Key: "other", Type: "credit", OccurredAt: base.Add(10 * time.Second),
	}); err != nil {
		t.Fatalf("AppendEvent(other): %v", err)
	}
	all, err := store.ListRecentEvents(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecentEvents: %v", err)
	}
	if len(all) != 6 {
		t.Fatalf("ListRecentEvents len = %d, want 6", len(all))
	}
	if all[0].Key != "other" {
		t.Fatalf("ListRecentEvents[0].Key = %q, want %q", all[0].Key, "other")
	}
}

func TestSQLiteBudgetStore_AppendEventEmptyKeyNoOp(t *testing.T) {
	t.Parallel()
	store := newSQLiteBudgetTestStore(t)
	ctx := context.Background()

	if err := store.AppendEvent(ctx, BudgetEvent{Key: "", Type: "debit"}); err != nil {
		t.Fatalf("AppendEvent(empty key): %v", err)
	}
	all, _ := store.ListRecentEvents(ctx, 10)
	if len(all) != 0 {
		t.Fatalf("empty-key event was persisted: %+v", all)
	}
}

func TestSQLiteBudgetStore_PruneEventsByAge(t *testing.T) {
	t.Parallel()
	store := newSQLiteBudgetTestStore(t)
	ctx := context.Background()

	old := time.Now().Add(-time.Hour).UTC()
	fresh := time.Now().UTC()
	if err := store.AppendEvent(ctx, BudgetEvent{Key: "k", Type: "debit", OccurredAt: old}); err != nil {
		t.Fatalf("AppendEvent(old): %v", err)
	}
	if err := store.AppendEvent(ctx, BudgetEvent{Key: "k", Type: "debit", OccurredAt: fresh}); err != nil {
		t.Fatalf("AppendEvent(fresh): %v", err)
	}

	deleted, err := store.PruneEvents(ctx, 30*time.Minute, 0)
	if err != nil {
		t.Fatalf("PruneEvents: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}

	remaining, _ := store.ListEvents(ctx, "k", 10)
	if len(remaining) != 1 {
		t.Fatalf("remaining len = %d, want 1", len(remaining))
	}
}

func TestSQLiteBudgetStore_PruneEventsByCount(t *testing.T) {
	t.Parallel()
	store := newSQLiteBudgetTestStore(t)
	ctx := context.Background()

	base := time.Now().UTC()
	for i := 0; i < 10; i++ {
		if err := store.AppendEvent(ctx, BudgetEvent{
			Key:        "k",
			Type:       "debit",
			OccurredAt: base.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("AppendEvent[%d]: %v", i, err)
		}
	}

	deleted, err := store.PruneEvents(ctx, 0, 3)
	if err != nil {
		t.Fatalf("PruneEvents: %v", err)
	}
	if deleted != 7 {
		t.Fatalf("deleted = %d, want 7 (kept 3)", deleted)
	}
	remaining, _ := store.ListEvents(ctx, "k", 100)
	if len(remaining) != 3 {
		t.Fatalf("remaining len = %d, want 3", len(remaining))
	}
}
