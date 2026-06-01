package governor

import (
	"context"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestMemoryUsageStore_RecordUsageAccumulates(t *testing.T) {
	t.Parallel()
	store := NewMemoryUsageStore()
	ctx := context.Background()

	state, err := store.RecordUsage(ctx, UsageEvent{UsageKey: "global", CostMicros: 500_000, OccurredAt: time.Now()})
	if err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}
	if state.UsedMicrosUSD != 500_000 {
		t.Fatalf("used = %d, want 500000", state.UsedMicrosUSD)
	}

	state, err = store.RecordUsage(ctx, UsageEvent{UsageKey: "global", CostMicros: 250_000, OccurredAt: time.Now()})
	if err != nil {
		t.Fatalf("RecordUsage second call: %v", err)
	}
	if state.UsedMicrosUSD != 750_000 {
		t.Fatalf("used after second call = %d, want 750000", state.UsedMicrosUSD)
	}
}

func TestMemoryUsageStore_RecordUsageIgnoresInvalidEvents(t *testing.T) {
	t.Parallel()
	store := NewMemoryUsageStore()
	ctx := context.Background()

	for _, event := range []UsageEvent{
		{CostMicros: 100},
		{UsageKey: "k"},
		{UsageKey: "k", CostMicros: -50},
	} {
		if _, err := store.RecordUsage(ctx, event); err != nil {
			t.Fatalf("RecordUsage(%+v): %v", event, err)
		}
	}
	if _, ok, err := store.Snapshot(ctx, "k"); err != nil || ok {
		t.Fatalf("invalid events created state: ok=%v err=%v", ok, err)
	}
}

func TestMemoryUsageStore_SnapshotMissing(t *testing.T) {
	t.Parallel()
	store := NewMemoryUsageStore()
	_, ok, err := store.Snapshot(context.Background(), "never-set")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if ok {
		t.Fatal("ok = true for missing key")
	}
}

func TestMemoryUsageStore_AppendAndListEvents(t *testing.T) {
	t.Parallel()
	store := NewMemoryUsageStore()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_ = store.AppendEvent(ctx, UsageHistoryEvent{
			Key: "global", Type: "usage", AmountMicrosUSD: int64(i * 100), OccurredAt: time.Unix(int64(i), 0),
		})
	}

	events, err := store.ListEvents(ctx, "global", 2)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("len = %d, want 2", len(events))
	}
	if events[0].AmountMicrosUSD != 400 {
		t.Fatalf("newest amount = %d, want 400", events[0].AmountMicrosUSD)
	}

	_ = store.AppendEvent(ctx, UsageHistoryEvent{Key: "other", Type: "usage", OccurredAt: time.Unix(10, 0)})
	all, err := store.ListRecentEvents(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecentEvents: %v", err)
	}
	if len(all) != 6 || all[0].Key != "other" {
		t.Fatalf("recent events = %+v", all)
	}
}

func TestMemoryUsageStore_Prune(t *testing.T) {
	t.Parallel()
	store := NewMemoryUsageStore()
	ctx := context.Background()

	old := time.Now().Add(-time.Hour)
	fresh := time.Now()
	_ = store.AppendEvent(ctx, UsageHistoryEvent{Key: "k", OccurredAt: old})
	_ = store.AppendEvent(ctx, UsageHistoryEvent{Key: "k", OccurredAt: fresh})
	deleted, err := store.Prune(ctx, 30*time.Minute, 0)
	if err != nil {
		t.Fatalf("Prune by age: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("age deleted = %d, want 1", deleted)
	}

	for i := 0; i < 10; i++ {
		_ = store.AppendEvent(ctx, UsageHistoryEvent{Key: "k", OccurredAt: time.Now().Add(time.Duration(i) * time.Second)})
	}
	deleted, err = store.Prune(ctx, 0, 3)
	if err != nil {
		t.Fatalf("Prune by count: %v", err)
	}
	if deleted != 8 {
		t.Fatalf("count deleted = %d, want 8", deleted)
	}
}

func TestMemoryUsageStore_ConcurrentRecordUsage(t *testing.T) {
	t.Parallel()
	store := NewMemoryUsageStore()
	ctx := context.Background()

	const goroutines = 50
	done := make(chan struct{}, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			_, _ = store.RecordUsage(ctx, UsageEvent{UsageKey: "k", CostMicros: 1_000})
			done <- struct{}{}
		}()
	}
	for i := 0; i < goroutines; i++ {
		<-done
	}
	state, ok, err := store.Snapshot(ctx, "k")
	if err != nil || !ok {
		t.Fatalf("Snapshot: ok=%v err=%v", ok, err)
	}
	if state.UsedMicrosUSD != goroutines*1_000 {
		t.Fatalf("used = %d, want %d", state.UsedMicrosUSD, goroutines*1_000)
	}
}

func defaultGovernorCfg() config.GovernorConfig {
	return config.GovernorConfig{
		MaxPromptTokens: 64_000,
		UsageBackend:    "memory",
		UsageKey:        "global",
		UsageScope:      "global",
	}
}

func TestStaticGovernor_RewriteIsIdentityWhenNoRules(t *testing.T) {
	t.Parallel()
	g := NewStaticGovernor(defaultGovernorCfg(), NewMemoryUsageStore(), NewMemoryUsageStore())
	req := types.ChatRequest{Model: "gpt-4o-mini", Messages: []types.Message{{Role: "user", Content: "hi"}}}
	out := g.Rewrite(req)
	if out.Model != req.Model {
		t.Fatalf("Rewrite changed model: %q -> %q", req.Model, out.Model)
	}
}

func TestStaticGovernor_CheckEnforcesPromptTokenCap(t *testing.T) {
	t.Parallel()
	cfg := defaultGovernorCfg()
	cfg.MaxPromptTokens = 10
	g := NewStaticGovernor(cfg, NewMemoryUsageStore(), NewMemoryUsageStore())
	req := types.ChatRequest{
		Messages: []types.Message{
			{Role: "user", Content: "this is a much longer message that exceeds the cap of 10 tokens"},
		},
	}
	if err := g.Check(context.Background(), req); err == nil {
		t.Fatal("expected error for prompt over cap")
	}
}
