package browserapp

import (
	"context"
	"errors"
	"sync"
)

var ErrNotFound = errors.New("browser selection not found")

// Selection remembers an explicitly selected installation on one runtime host.
// It is configuration, not publisher evidence or executable identity approval.
type Selection struct {
	RuntimeHostID string
	Name          string
	Path          string
	CanonicalPath string
}

type Store interface {
	Backend() string
	Get(context.Context, string) (Selection, error)
	Put(context.Context, Selection) error
	Delete(context.Context, string) error
}

type MemoryStore struct {
	mu         sync.RWMutex
	selections map[string]Selection
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{selections: make(map[string]Selection)}
}

func (s *MemoryStore) Backend() string { return "memory" }

func (s *MemoryStore) Get(ctx context.Context, hostID string) (Selection, error) {
	if err := ctx.Err(); err != nil {
		return Selection{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	selection, ok := s.selections[hostID]
	if !ok {
		return Selection{}, ErrNotFound
	}
	return selection, nil
}

func (s *MemoryStore) Put(ctx context.Context, selection Selection) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.selections[selection.RuntimeHostID] = selection
	return nil
}

func (s *MemoryStore) Delete(ctx context.Context, hostID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.selections, hostID)
	return nil
}
