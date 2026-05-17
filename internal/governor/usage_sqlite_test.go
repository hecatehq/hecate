package governor

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/hecate/agent-runtime/internal/storage"
)

func newSQLiteUsageTestStore(t *testing.T) *SQLiteUsageStore {
	t.Helper()
	dir := t.TempDir()
	client, err := storage.NewSQLiteClient(context.Background(), storage.SQLiteConfig{
		Path:        filepath.Join(dir, "usage.db"),
		TablePrefix: "test",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	store, err := NewSQLiteUsageStore(context.Background(), client)
	if err != nil {
		t.Fatalf("NewSQLiteUsageStore: %v", err)
	}
	return store
}

func TestSQLiteUsageStore_RejectsNilClient(t *testing.T) {
	_, err := NewSQLiteUsageStore(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil client")
	}
}

func TestSQLiteUsageStore_SnapshotMissing(t *testing.T) {
	t.Parallel()
	store := newSQLiteUsageTestStore(t)
	_, ok, err := store.Snapshot(context.Background(), "never-set")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if ok {
		t.Fatal("ok = true for missing key")
	}
}

func TestSQLiteUsageStore_RecordUsageRoundTrip(t *testing.T) {
	t.Parallel()
	store := newSQLiteUsageTestStore(t)
	ctx := context.Background()

	state, err := store.RecordUsage(ctx, UsageEvent{UsageKey: "global", CostMicros: 500_000, OccurredAt: time.Now()})
	if err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}
	if state.UsedMicrosUSD != 500_000 {
		t.Fatalf("used after first call = %d, want 500000", state.UsedMicrosUSD)
	}

	state, err = store.RecordUsage(ctx, UsageEvent{UsageKey: "global", CostMicros: 250_000, OccurredAt: time.Now()})
	if err != nil {
		t.Fatalf("RecordUsage second call: %v", err)
	}
	if state.UsedMicrosUSD != 750_000 {
		t.Fatalf("used after second call = %d, want 750000", state.UsedMicrosUSD)
	}

	got, ok, err := store.Snapshot(ctx, "global")
	if err != nil || !ok {
		t.Fatalf("Snapshot: ok=%v err=%v", ok, err)
	}
	if got.UsedMicrosUSD != 750_000 {
		t.Fatalf("snapshot used = %d, want 750000", got.UsedMicrosUSD)
	}
}

func TestSQLiteUsageStore_RecordUsageNoOpOnEmptyKeyOrZeroCost(t *testing.T) {
	t.Parallel()
	store := newSQLiteUsageTestStore(t)
	ctx := context.Background()

	if _, err := store.RecordUsage(ctx, UsageEvent{UsageKey: "", CostMicros: 100}); err != nil {
		t.Fatalf("RecordUsage(empty key): %v", err)
	}
	if _, err := store.RecordUsage(ctx, UsageEvent{UsageKey: "k", CostMicros: 0}); err != nil {
		t.Fatalf("RecordUsage(zero cost): %v", err)
	}
	if _, ok, _ := store.Snapshot(ctx, "k"); ok {
		t.Fatal("zero-cost usage created a row")
	}
}

func TestSQLiteUsageStore_SequentialUsageAccumulates(t *testing.T) {
	t.Parallel()
	store := newSQLiteUsageTestStore(t)
	ctx := context.Background()

	const records = 50
	for i := 0; i < records; i++ {
		if _, err := store.RecordUsage(ctx, UsageEvent{UsageKey: "k", CostMicros: 1_000}); err != nil {
			t.Fatalf("RecordUsage[%d]: %v", i, err)
		}
	}

	state, ok, err := store.Snapshot(ctx, "k")
	if err != nil || !ok {
		t.Fatalf("Snapshot: ok=%v err=%v", ok, err)
	}
	if state.UsedMicrosUSD != int64(records*1_000) {
		t.Fatalf("used = %d, want %d", state.UsedMicrosUSD, records*1_000)
	}
}

func TestSQLiteUsageStore_AppendAndListEvents(t *testing.T) {
	t.Parallel()
	store := newSQLiteUsageTestStore(t)
	ctx := context.Background()

	base := time.Now().UTC()
	for i := 0; i < 5; i++ {
		if err := store.AppendEvent(ctx, UsageHistoryEvent{
			Key:             "global",
			Type:            "usage",
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
	if events[0].AmountMicrosUSD != 400 {
		t.Fatalf("ListEvents[0].Amount = %d, want 400", events[0].AmountMicrosUSD)
	}
	if events[0].Provider != "openai" || events[0].Model != "gpt-4o" {
		t.Fatalf("event round-trip lost columns: %+v", events[0])
	}

	if err := store.AppendEvent(ctx, UsageHistoryEvent{Key: "other", Type: "usage", OccurredAt: base.Add(10 * time.Second)}); err != nil {
		t.Fatalf("AppendEvent(other): %v", err)
	}
	all, err := store.ListRecentEvents(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecentEvents: %v", err)
	}
	if len(all) != 6 || all[0].Key != "other" {
		t.Fatalf("recent events = %+v", all)
	}
}

func TestSQLiteUsageStore_AppendEventEmptyKeyNoOp(t *testing.T) {
	t.Parallel()
	store := newSQLiteUsageTestStore(t)
	ctx := context.Background()

	if err := store.AppendEvent(ctx, UsageHistoryEvent{Key: "", Type: "usage"}); err != nil {
		t.Fatalf("AppendEvent(empty key): %v", err)
	}
	all, _ := store.ListRecentEvents(ctx, 10)
	if len(all) != 0 {
		t.Fatalf("empty-key event was persisted: %+v", all)
	}
}

func TestSQLiteUsageStore_Prune(t *testing.T) {
	t.Parallel()
	store := newSQLiteUsageTestStore(t)
	ctx := context.Background()

	old := time.Now().Add(-time.Hour).UTC()
	fresh := time.Now().UTC()
	if err := store.AppendEvent(ctx, UsageHistoryEvent{Key: "k", Type: "usage", OccurredAt: old}); err != nil {
		t.Fatalf("AppendEvent(old): %v", err)
	}
	if err := store.AppendEvent(ctx, UsageHistoryEvent{Key: "k", Type: "usage", OccurredAt: fresh}); err != nil {
		t.Fatalf("AppendEvent(fresh): %v", err)
	}

	deleted, err := store.Prune(ctx, 30*time.Minute, 0)
	if err != nil {
		t.Fatalf("Prune by age: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("age deleted = %d, want 1", deleted)
	}

	base := time.Now().UTC()
	for i := 0; i < 10; i++ {
		if err := store.AppendEvent(ctx, UsageHistoryEvent{
			Key:        "k",
			Type:       "usage",
			OccurredAt: base.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("AppendEvent[%d]: %v", i, err)
		}
	}

	deleted, err = store.Prune(ctx, 0, 3)
	if err != nil {
		t.Fatalf("Prune by count: %v", err)
	}
	if deleted != 8 {
		t.Fatalf("count deleted = %d, want 8", deleted)
	}
	remaining, err := store.ListEvents(ctx, "k", 100)
	if err != nil {
		t.Fatalf("ListEvents after prune: %v", err)
	}
	if len(remaining) != 3 {
		t.Fatalf("remaining len = %d, want 3", len(remaining))
	}
}
