package governor

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/hecate/agent-runtime/pkg/types"
)

type AccountState struct {
	Key               string    `json:"key"`
	BalanceMicrosUSD  int64     `json:"balance_micros_usd"`
	CreditedMicrosUSD int64     `json:"credited_micros_usd"`
	DebitedMicrosUSD  int64     `json:"debited_micros_usd"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type UsageEvent struct {
	BudgetKey  string
	RequestID  string
	Provider   string
	Model      string
	Usage      types.Usage
	CostMicros int64
	OccurredAt time.Time
}

type AccountStore interface {
	Snapshot(ctx context.Context, key string) (AccountState, bool, error)
	Debit(ctx context.Context, event UsageEvent) (AccountState, error)
	Credit(ctx context.Context, key string, delta int64) (AccountState, error)
	SetBalance(ctx context.Context, key string, value int64) (AccountState, error)
	Reset(ctx context.Context, key string) error
}

type BudgetEvent struct {
	Key               string    `json:"key"`
	Type              string    `json:"type"`
	Scope             string    `json:"scope,omitempty"`
	Provider          string    `json:"provider,omitempty"`
	Model             string    `json:"model,omitempty"`
	RequestID         string    `json:"request_id,omitempty"`
	Actor             string    `json:"actor,omitempty"`
	Detail            string    `json:"detail,omitempty"`
	AmountMicrosUSD   int64     `json:"amount_micros_usd"`
	BalanceMicrosUSD  int64     `json:"balance_micros_usd"`
	CreditedMicrosUSD int64     `json:"credited_micros_usd"`
	DebitedMicrosUSD  int64     `json:"debited_micros_usd"`
	PromptTokens      int       `json:"prompt_tokens,omitempty"`
	CompletionTokens  int       `json:"completion_tokens,omitempty"`
	TotalTokens       int       `json:"total_tokens,omitempty"`
	OccurredAt        time.Time `json:"occurred_at"`
}

type BudgetHistoryStore interface {
	AppendEvent(ctx context.Context, event BudgetEvent) error
	ListEvents(ctx context.Context, key string, limit int) ([]BudgetEvent, error)
	ListRecentEvents(ctx context.Context, limit int) ([]BudgetEvent, error)
	PruneEvents(ctx context.Context, maxAge time.Duration, maxCount int) (int, error)
}

type BudgetStore interface {
	AccountStore
	BudgetHistoryStore
}

type MemoryBudgetStore struct {
	mu       sync.Mutex
	accounts map[string]AccountState
	events   map[string][]BudgetEvent
}

func NewMemoryBudgetStore() *MemoryBudgetStore {
	return &MemoryBudgetStore{
		accounts: make(map[string]AccountState),
		events:   make(map[string][]BudgetEvent),
	}
}

func (s *MemoryBudgetStore) Snapshot(_ context.Context, key string) (AccountState, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	account, ok := s.accounts[key]
	return account, ok, nil
}

func (s *MemoryBudgetStore) Debit(_ context.Context, event UsageEvent) (AccountState, error) {
	if event.BudgetKey == "" || event.CostMicros <= 0 {
		return AccountState{Key: event.BudgetKey}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	account := s.accounts[event.BudgetKey]
	account.Key = event.BudgetKey
	account.BalanceMicrosUSD -= event.CostMicros
	account.DebitedMicrosUSD += event.CostMicros
	account.UpdatedAt = nowUTC(event.OccurredAt)
	s.accounts[event.BudgetKey] = account
	return account, nil
}

func (s *MemoryBudgetStore) Credit(_ context.Context, key string, delta int64) (AccountState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	account := s.accounts[key]
	account.Key = key
	account.BalanceMicrosUSD += delta
	if delta > 0 {
		account.CreditedMicrosUSD += delta
	}
	account.UpdatedAt = time.Now().UTC()
	s.accounts[key] = account
	return account, nil
}

func (s *MemoryBudgetStore) Reset(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accounts[key] = AccountState{
		Key:       key,
		UpdatedAt: time.Now().UTC(),
	}
	return nil
}

func (s *MemoryBudgetStore) SetBalance(_ context.Context, key string, value int64) (AccountState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	account := s.accounts[key]
	account.Key = key
	account.BalanceMicrosUSD = value
	account.UpdatedAt = time.Now().UTC()
	s.accounts[key] = account
	return account, nil
}

func (s *MemoryBudgetStore) AppendEvent(_ context.Context, event BudgetEvent) error {
	if event.Key == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	events := append(s.events[event.Key], event)
	if len(events) > 200 {
		events = append([]BudgetEvent(nil), events[len(events)-200:]...)
	}
	s.events[event.Key] = events
	return nil
}

func (s *MemoryBudgetStore) ListEvents(_ context.Context, key string, limit int) ([]BudgetEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	events := s.events[key]
	if limit <= 0 || limit > len(events) {
		limit = len(events)
	}

	out := make([]BudgetEvent, 0, limit)
	for i := len(events) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, events[i])
	}
	return out, nil
}

func (s *MemoryBudgetStore) ListRecentEvents(_ context.Context, limit int) ([]BudgetEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	all := make([]BudgetEvent, 0, 32)
	for _, events := range s.events {
		all = append(all, events...)
	}
	sortBudgetEventsDesc(all)
	if limit <= 0 || limit > len(all) {
		limit = len(all)
	}
	return append([]BudgetEvent(nil), all[:limit]...), nil
}

func (s *MemoryBudgetStore) PruneEvents(_ context.Context, maxAge time.Duration, maxCount int) (int, error) {
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
			kept = append([]BudgetEvent(nil), kept[len(kept)-maxCount:]...)
		}
		s.events[key] = append([]BudgetEvent(nil), kept...)
	}
	return deleted, nil
}

func nowUTC(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}

func sortBudgetEventsDesc(events []BudgetEvent) {
	sort.Slice(events, func(i, j int) bool {
		return events[i].OccurredAt.After(events[j].OccurredAt)
	})
}
