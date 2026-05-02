package governor

import (
	"context"
	"testing"
	"time"

	"github.com/hecate/agent-runtime/internal/config"
)

func TestMemoryBudgetStoreDebitIgnoresInvalidEvents(t *testing.T) {
	store := NewMemoryBudgetStore()
	ctx := context.Background()

	cases := []struct {
		name  string
		event UsageEvent
	}{
		{"empty key", UsageEvent{CostMicros: 100}},
		{"zero cost", UsageEvent{BudgetKey: "k"}},
		{"negative cost", UsageEvent{BudgetKey: "k", CostMicros: -50}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			account, err := store.Debit(ctx, tc.event)
			if err != nil {
				t.Fatalf("Debit: %v", err)
			}
			if account.BalanceMicrosUSD != 0 || account.DebitedMicrosUSD != 0 {
				t.Errorf("invalid event mutated balances: %+v", account)
			}
		})
	}
}

func TestMemoryBudgetStoreCreditNegativeDoesNotIncrementCredited(t *testing.T) {
	store := NewMemoryBudgetStore()
	ctx := context.Background()

	// Negative deltas reduce balance but should NOT count toward
	// CreditedMicrosUSD — that field tracks lifetime credit, not net.
	account, err := store.Credit(ctx, "k", -100)
	if err != nil {
		t.Fatalf("Credit: %v", err)
	}
	if account.BalanceMicrosUSD != -100 {
		t.Errorf("balance = %d, want -100", account.BalanceMicrosUSD)
	}
	if account.CreditedMicrosUSD != 0 {
		t.Errorf("credited = %d, want 0 (negative deltas don't accumulate)", account.CreditedMicrosUSD)
	}
}

func TestMemoryBudgetStoreListEventsRespectsLimit(t *testing.T) {
	store := NewMemoryBudgetStore()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if err := store.AppendEvent(ctx, BudgetEvent{Key: "acc", Type: "debit", AmountMicrosUSD: int64(i + 1), OccurredAt: time.Unix(int64(i), 0)}); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}

	for _, tc := range []struct {
		limit, want int
	}{
		{0, 5},  // 0 means no limit
		{-1, 5}, // negative also means no limit
		{2, 2},
		{99, 5}, // larger than available clamps down
	} {
		got, err := store.ListEvents(ctx, "acc", tc.limit)
		if err != nil {
			t.Fatalf("ListEvents(limit=%d): %v", tc.limit, err)
		}
		if len(got) != tc.want {
			t.Errorf("ListEvents(limit=%d) returned %d events, want %d", tc.limit, len(got), tc.want)
		}
	}
}

func TestMemoryBudgetStoreListRecentEventsAcrossKeys(t *testing.T) {
	store := NewMemoryBudgetStore()
	ctx := context.Background()

	if err := store.AppendEvent(ctx, BudgetEvent{Key: "a", Type: "debit", OccurredAt: time.Unix(1, 0)}); err != nil {
		t.Fatalf("AppendEvent a: %v", err)
	}
	if err := store.AppendEvent(ctx, BudgetEvent{Key: "b", Type: "credit", OccurredAt: time.Unix(2, 0)}); err != nil {
		t.Fatalf("AppendEvent b: %v", err)
	}

	got, err := store.ListRecentEvents(ctx, 0)
	if err != nil {
		t.Fatalf("ListRecentEvents: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d events, want 2", len(got))
	}
	// Most recent first.
	if !got[0].OccurredAt.After(got[1].OccurredAt) {
		t.Errorf("expected descending order, got %v then %v", got[0].OccurredAt, got[1].OccurredAt)
	}
}

func TestResolveBudgetFilterScopes(t *testing.T) {
	g := NewStaticGovernor(config.GovernorConfig{
		BudgetKey: "billing",
	}, NewMemoryBudgetStore(), nil)

	cases := []struct {
		name    string
		filter  BudgetFilter
		wantKey string
	}{
		{"explicit key passes through and gets custom scope",
			BudgetFilter{Key: "manual"},
			"manual"},
		{"provider scope appends provider name",
			BudgetFilter{Scope: "provider", Provider: "openai"},
			"billing:provider:openai"},
		{"unknown scope falls through to global baseKey",
			BudgetFilter{Scope: "weird"},
			"billing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := g.resolveBudgetFilter(tc.filter)
			if got.Key != tc.wantKey {
				t.Errorf("Key = %q, want %q", got.Key, tc.wantKey)
			}
		})
	}
}

func TestResolveBudgetFilterDefaultsBaseKey(t *testing.T) {
	// With no BudgetKey configured, baseKey should default to "global".
	g := NewStaticGovernor(config.GovernorConfig{}, NewMemoryBudgetStore(), nil)
	got := g.resolveBudgetFilter(BudgetFilter{Scope: "provider", Provider: "openai"})
	if got.Key != "global:provider:openai" {
		t.Errorf("Key = %q, want global:provider:openai", got.Key)
	}
}

func TestBuildWarnings(t *testing.T) {
	g := NewStaticGovernor(config.GovernorConfig{}, NewMemoryBudgetStore(), nil)

	t.Run("zero credited returns nil", func(t *testing.T) {
		if got := g.buildWarnings(0, 100); got != nil {
			t.Errorf("got %d warnings, want nil for zero credited", len(got))
		}
	})

	t.Run("negative balance returns nil", func(t *testing.T) {
		if got := g.buildWarnings(1000, -1); got != nil {
			t.Errorf("got %d warnings, want nil for negative balance", len(got))
		}
	})

	t.Run("default thresholds 50/80/95 — high balance triggers none", func(t *testing.T) {
		// At balance=1000 (100%) of credited=1000, all thresholds (50/80/95%)
		// are below balance, so none are triggered.
		got := g.buildWarnings(1000, 1000)
		if len(got) != 3 {
			t.Fatalf("got %d warnings, want 3", len(got))
		}
		for _, w := range got {
			if w.Triggered {
				t.Errorf("threshold %d triggered with balance=credited (should be safe)", w.ThresholdPercent)
			}
		}
	})

	t.Run("low balance triggers all thresholds", func(t *testing.T) {
		// balance=10 / credited=1000 = 1% — below 50/80/95% thresholds.
		got := g.buildWarnings(1000, 10)
		for _, w := range got {
			if !w.Triggered {
				t.Errorf("threshold %d should be triggered, got Triggered=false", w.ThresholdPercent)
			}
		}
	})
}

func TestBuildWarningsCustomThresholdsSkipNonPositive(t *testing.T) {
	g := NewStaticGovernor(config.GovernorConfig{
		BudgetWarningThresholds: []int{0, -10, 25, 75},
	}, NewMemoryBudgetStore(), nil)

	got := g.buildWarnings(1000, 100)
	// Only 25 and 75 are positive — the 0 and -10 entries must be skipped.
	if len(got) != 2 {
		t.Fatalf("got %d warnings, want 2 (non-positive thresholds skipped)", len(got))
	}
	if got[0].ThresholdPercent != 25 || got[1].ThresholdPercent != 75 {
		t.Errorf("thresholds = %d, %d, want 25, 75", got[0].ThresholdPercent, got[1].ThresholdPercent)
	}
}
