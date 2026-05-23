package governor

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/hecatehq/hecate/pkg/types"
)

type UsageState struct {
	Key           string    `json:"key"`
	UsedMicrosUSD int64     `json:"used_micros_usd"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type UsageEvent struct {
	UsageKey   string
	RequestID  string
	Provider   string
	Model      string
	Usage      types.Usage
	CostMicros int64
	OccurredAt time.Time
}

type UsageStore interface {
	Snapshot(ctx context.Context, key string) (UsageState, bool, error)
	RecordUsage(ctx context.Context, event UsageEvent) (UsageState, error)
}

type UsageHistoryEvent struct {
	Key              string    `json:"key"`
	Type             string    `json:"type"`
	Scope            string    `json:"scope,omitempty"`
	Provider         string    `json:"provider,omitempty"`
	Model            string    `json:"model,omitempty"`
	RequestID        string    `json:"request_id,omitempty"`
	Actor            string    `json:"actor,omitempty"`
	Detail           string    `json:"detail,omitempty"`
	AmountMicrosUSD  int64     `json:"amount_micros_usd"`
	PromptTokens     int       `json:"prompt_tokens,omitempty"`
	CompletionTokens int       `json:"completion_tokens,omitempty"`
	TotalTokens      int       `json:"total_tokens,omitempty"`
	OccurredAt       time.Time `json:"occurred_at"`
}

type UsageEventStore interface {
	AppendEvent(ctx context.Context, event UsageHistoryEvent) error
	ListEvents(ctx context.Context, key string, limit int) ([]UsageHistoryEvent, error)
	ListRecentEvents(ctx context.Context, limit int) ([]UsageHistoryEvent, error)
	Prune(ctx context.Context, maxAge time.Duration, maxCount int) (int, error)
}

type UsageRepository interface {
	UsageStore
	UsageEventStore
}

type MemoryUsageStore struct {
	mu     sync.Mutex
	usage  map[string]UsageState
	events map[string][]UsageHistoryEvent
}

func NewMemoryUsageStore() *MemoryUsageStore {
	return &MemoryUsageStore{
		usage:  make(map[string]UsageState),
		events: make(map[string][]UsageHistoryEvent),
	}
}

func (s *MemoryUsageStore) Snapshot(_ context.Context, key string) (UsageState, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.usage[key]
	return state, ok, nil
}

func (s *MemoryUsageStore) RecordUsage(_ context.Context, event UsageEvent) (UsageState, error) {
	if event.UsageKey == "" || event.CostMicros <= 0 {
		return UsageState{Key: event.UsageKey}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.usage[event.UsageKey]
	state.Key = event.UsageKey
	state.UsedMicrosUSD += event.CostMicros
	state.UpdatedAt = nowUTC(event.OccurredAt)
	s.usage[event.UsageKey] = state
	return state, nil
}

func (s *MemoryUsageStore) AppendEvent(_ context.Context, event UsageHistoryEvent) error {
	if event.Key == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	events := append(s.events[event.Key], event)
	if len(events) > 200 {
		events = append([]UsageHistoryEvent(nil), events[len(events)-200:]...)
	}
	s.events[event.Key] = events
	return nil
}

func (s *MemoryUsageStore) ListEvents(_ context.Context, key string, limit int) ([]UsageHistoryEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	events := s.events[key]
	if limit <= 0 || limit > len(events) {
		limit = len(events)
	}

	out := make([]UsageHistoryEvent, 0, limit)
	for i := len(events) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, events[i])
	}
	return out, nil
}

func (s *MemoryUsageStore) ListRecentEvents(_ context.Context, limit int) ([]UsageHistoryEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	all := make([]UsageHistoryEvent, 0, 32)
	for _, events := range s.events {
		all = append(all, events...)
	}
	sortUsageEventsDesc(all)
	if limit <= 0 || limit > len(all) {
		limit = len(all)
	}
	return append([]UsageHistoryEvent(nil), all[:limit]...), nil
}

func (s *MemoryUsageStore) Prune(_ context.Context, maxAge time.Duration, maxCount int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	deleted := 0
	for key, events := range s.events {
		kept := events[:0]
		for _, event := range events {
			if maxAge > 0 && !event.OccurredAt.IsZero() && event.OccurredAt.Before(now.Add(-maxAge)) {
				deleted++
				continue
			}
			kept = append(kept, event)
		}
		if maxCount > 0 && len(kept) > maxCount {
			deleted += len(kept) - maxCount
			kept = append([]UsageHistoryEvent(nil), kept[len(kept)-maxCount:]...)
		}
		s.events[key] = append([]UsageHistoryEvent(nil), kept...)
	}
	return deleted, nil
}

func nowUTC(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}

func sortUsageEventsDesc(events []UsageHistoryEvent) {
	sort.Slice(events, func(i, j int) bool {
		return events[i].OccurredAt.After(events[j].OccurredAt)
	})
}
