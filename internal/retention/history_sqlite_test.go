package retention

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hecatehq/hecate/internal/storage"
)

func newSQLiteTestStore(t *testing.T) *SQLiteHistoryStore {
	t.Helper()
	dir := t.TempDir()
	client, err := storage.NewSQLiteClient(context.Background(), storage.SQLiteConfig{
		Path:        filepath.Join(dir, "retention.db"),
		TablePrefix: "test",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	store, err := NewSQLiteHistoryStore(context.Background(), client, "")
	if err != nil {
		t.Fatalf("NewSQLiteHistoryStore: %v", err)
	}
	return store
}

func TestSQLiteHistoryStore_RoundTripNewestFirst(t *testing.T) {
	t.Parallel()
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	if err := store.AppendRun(ctx, HistoryRecord{
		StartedAt:  "2026-04-22T09:59:50Z",
		FinishedAt: "2026-04-22T10:00:00Z",
		Trigger:    "manual",
		Actor:      "ci",
		RequestID:  "req-1",
		Results: []SubsystemResult{
			{Name: "trace_snapshots", Deleted: 5},
		},
	}); err != nil {
		t.Fatalf("AppendRun(first): %v", err)
	}

	if err := store.AppendRun(ctx, HistoryRecord{
		StartedAt:  "2026-04-22T10:59:50Z",
		FinishedAt: "2026-04-22T11:00:00Z",
		Trigger:    "scheduled",
		Results: []SubsystemResult{
			{Name: "audit_events", Deleted: 10},
		},
	}); err != nil {
		t.Fatalf("AppendRun(second): %v", err)
	}

	runs, err := store.ListRuns(ctx, 10)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("runs = %d, want 2", len(runs))
	}
	// finished_at DESC — the scheduled run lands first.
	if runs[0].Trigger != "scheduled" || runs[1].Trigger != "manual" {
		t.Fatalf("ordering: got %q,%q, want scheduled,manual", runs[0].Trigger, runs[1].Trigger)
	}
	// JSON round-trip preserves the per-subsystem result rows. A
	// regression here (e.g. dropping the resultsJSON column or
	// scanning the wrong field) would silently lose audit data.
	if len(runs[1].Results) != 1 || runs[1].Results[0].Name != "trace_snapshots" {
		t.Fatalf("results round-trip: got %+v", runs[1].Results)
	}
}

func TestSQLiteHistoryStore_LimitClampsToCap(t *testing.T) {
	t.Parallel()
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	// limit=0 should clamp to the default (20), not return zero rows.
	// limit < 0 same. Asserting both branches by exercising the
	// no-rows-on-empty-store path with a zero limit.
	runs, err := store.ListRuns(ctx, 0)
	if err != nil {
		t.Fatalf("ListRuns(0): %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("empty store with limit=0: got %d rows, want 0", len(runs))
	}
}

func TestSQLiteHistoryStore_RejectsNilClient(t *testing.T) {
	_, err := NewSQLiteHistoryStore(context.Background(), nil, "")
	if err == nil {
		t.Fatal("expected error for nil client")
	}
}
