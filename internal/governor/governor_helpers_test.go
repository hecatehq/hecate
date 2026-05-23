package governor

import (
	"context"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/config"
)

func TestMemoryUsageStoreListEventsRespectsLimit(t *testing.T) {
	store := NewMemoryUsageStore()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if err := store.AppendEvent(ctx, UsageHistoryEvent{Key: "acc", Type: "usage", AmountMicrosUSD: int64(i + 1), OccurredAt: time.Unix(int64(i), 0)}); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}

	for _, tc := range []struct {
		limit, want int
	}{
		{0, 5},
		{-1, 5},
		{2, 2},
		{99, 5},
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

func TestMemoryUsageStoreListRecentEventsAcrossKeys(t *testing.T) {
	store := NewMemoryUsageStore()
	ctx := context.Background()

	if err := store.AppendEvent(ctx, UsageHistoryEvent{Key: "a", Type: "usage", OccurredAt: time.Unix(1, 0)}); err != nil {
		t.Fatalf("AppendEvent a: %v", err)
	}
	if err := store.AppendEvent(ctx, UsageHistoryEvent{Key: "b", Type: "usage", OccurredAt: time.Unix(2, 0)}); err != nil {
		t.Fatalf("AppendEvent b: %v", err)
	}

	got, err := store.ListRecentEvents(ctx, 0)
	if err != nil {
		t.Fatalf("ListRecentEvents: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d events, want 2", len(got))
	}
	if !got[0].OccurredAt.After(got[1].OccurredAt) {
		t.Errorf("expected descending order, got %v then %v", got[0].OccurredAt, got[1].OccurredAt)
	}
}

func TestResolveUsageFilterScopes(t *testing.T) {
	g := NewStaticGovernor(config.GovernorConfig{
		UsageKey: "usage",
	}, NewMemoryUsageStore(), nil)

	cases := []struct {
		name    string
		filter  UsageFilter
		wantKey string
	}{
		{"explicit key passes through and gets custom scope", UsageFilter{Key: "manual"}, "manual"},
		{"provider scope appends provider name", UsageFilter{Scope: "provider", Provider: "openai"}, "usage:provider:openai"},
		{"unknown scope falls through to global baseKey", UsageFilter{Scope: "weird"}, "usage"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := g.resolveUsageFilter(tc.filter)
			if got.Key != tc.wantKey {
				t.Errorf("Key = %q, want %q", got.Key, tc.wantKey)
			}
		})
	}
}

func TestResolveUsageFilterDefaultsBaseKey(t *testing.T) {
	g := NewStaticGovernor(config.GovernorConfig{}, NewMemoryUsageStore(), nil)
	got := g.resolveUsageFilter(UsageFilter{Scope: "provider", Provider: "openai"})
	if got.Key != "global:provider:openai" {
		t.Errorf("Key = %q, want global:provider:openai", got.Key)
	}
}
