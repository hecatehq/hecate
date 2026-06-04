package memory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrNotFound      = errors.New("memory entry not found")
	ErrAlreadyExists = errors.New("memory entry already exists")
	ErrInvalid       = errors.New("invalid memory entry")
)

const (
	ScopeProject             = "project"
	TrustLabelOperatorMemory = "operator_memory"
	SourceKindOperator       = "operator"
)

type Entry struct {
	ID         string
	Scope      string
	ProjectID  string
	Title      string
	Body       string
	TrustLabel string
	SourceKind string
	SourceID   string
	Enabled    bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type Filter struct {
	ProjectID       string
	IncludeDisabled bool
}

type Store interface {
	Backend() string
	Create(ctx context.Context, entry Entry) (Entry, error)
	Get(ctx context.Context, projectID, id string) (Entry, bool, error)
	List(ctx context.Context, filter Filter) ([]Entry, error)
	Update(ctx context.Context, projectID, id string, update func(*Entry)) (Entry, error)
	Delete(ctx context.Context, projectID, id string) error
	DeleteByProjectID(ctx context.Context, projectID string) (int, error)
}

type MemoryStore struct {
	mu      sync.Mutex
	entries map[string]Entry
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{entries: make(map[string]Entry)}
}

func (s *MemoryStore) Backend() string {
	return "memory"
}

func (s *MemoryStore) Create(_ context.Context, entry Entry) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry = normalizeEntry(entry, time.Now().UTC())
	if err := validateEntry(entry); err != nil {
		return Entry{}, err
	}
	if _, ok := s.entries[entry.ID]; ok {
		return Entry{}, ErrAlreadyExists
	}
	s.entries[entry.ID] = entry
	return entry, nil
}

func (s *MemoryStore) Get(_ context.Context, projectID, id string) (Entry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[strings.TrimSpace(id)]
	if !ok || strings.TrimSpace(projectID) != entry.ProjectID {
		return Entry{}, false, nil
	}
	return entry, true, nil
}

func (s *MemoryStore) List(_ context.Context, filter Filter) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projectID := strings.TrimSpace(filter.ProjectID)
	items := make([]Entry, 0, len(s.entries))
	for _, entry := range s.entries {
		if projectID != "" && entry.ProjectID != projectID {
			continue
		}
		if !filter.IncludeDisabled && !entry.Enabled {
			continue
		}
		items = append(items, entry)
	}
	sortEntries(items)
	return items, nil
}

func (s *MemoryStore) Update(_ context.Context, projectID, id string, update func(*Entry)) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projectID = strings.TrimSpace(projectID)
	id = strings.TrimSpace(id)
	entry, ok := s.entries[id]
	if !ok || entry.ProjectID != projectID {
		return Entry{}, ErrNotFound
	}
	originalID := entry.ID
	originalProjectID := entry.ProjectID
	originalScope := entry.Scope
	originalCreatedAt := entry.CreatedAt
	if update != nil {
		update(&entry)
	}
	if strings.TrimSpace(entry.ID) != originalID {
		return Entry{}, fmt.Errorf("%w: memory entry id cannot be changed", ErrInvalid)
	}
	if strings.TrimSpace(entry.ProjectID) != originalProjectID {
		return Entry{}, fmt.Errorf("%w: project_id cannot be changed", ErrInvalid)
	}
	if strings.TrimSpace(entry.Scope) != originalScope {
		return Entry{}, fmt.Errorf("%w: scope cannot be changed", ErrInvalid)
	}
	entry.ID = originalID
	entry.ProjectID = originalProjectID
	entry.Scope = originalScope
	entry.CreatedAt = originalCreatedAt
	entry.UpdatedAt = time.Now().UTC()
	entry = normalizeEntry(entry, entry.UpdatedAt)
	if err := validateEntry(entry); err != nil {
		return Entry{}, err
	}
	s.entries[id] = entry
	return entry, nil
}

func (s *MemoryStore) Delete(_ context.Context, projectID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	projectID = strings.TrimSpace(projectID)
	id = strings.TrimSpace(id)
	entry, ok := s.entries[id]
	if !ok || entry.ProjectID != projectID {
		return ErrNotFound
	}
	delete(s.entries, id)
	return nil
}

func (s *MemoryStore) DeleteByProjectID(_ context.Context, projectID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return 0, nil
	}
	deleted := 0
	for id, entry := range s.entries {
		if entry.ProjectID != projectID {
			continue
		}
		delete(s.entries, id)
		deleted++
	}
	return deleted, nil
}

func normalizeEntry(entry Entry, now time.Time) Entry {
	entry.ID = strings.TrimSpace(entry.ID)
	entry.Scope = strings.TrimSpace(entry.Scope)
	if entry.Scope == "" {
		entry.Scope = ScopeProject
	}
	entry.ProjectID = strings.TrimSpace(entry.ProjectID)
	entry.Title = strings.TrimSpace(entry.Title)
	entry.Body = strings.TrimSpace(entry.Body)
	entry.TrustLabel = strings.TrimSpace(entry.TrustLabel)
	if entry.TrustLabel == "" {
		entry.TrustLabel = TrustLabelOperatorMemory
	}
	entry.SourceKind = strings.TrimSpace(entry.SourceKind)
	if entry.SourceKind == "" {
		entry.SourceKind = SourceKindOperator
	}
	entry.SourceID = strings.TrimSpace(entry.SourceID)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = now
	}
	if entry.UpdatedAt.IsZero() {
		entry.UpdatedAt = entry.CreatedAt
	}
	return entry
}

func validateEntry(entry Entry) error {
	if entry.ID == "" {
		return fmt.Errorf("%w: memory entry id is required", ErrInvalid)
	}
	if entry.Scope != ScopeProject {
		return fmt.Errorf("%w: only project-scoped memory is supported", ErrInvalid)
	}
	if entry.ProjectID == "" {
		return fmt.Errorf("%w: project_id is required", ErrInvalid)
	}
	if entry.Title == "" {
		return fmt.Errorf("%w: title is required", ErrInvalid)
	}
	if entry.Body == "" {
		return fmt.Errorf("%w: body is required", ErrInvalid)
	}
	if len(entry.Body) > 20000 {
		return fmt.Errorf("%w: body must be 20000 characters or fewer", ErrInvalid)
	}
	if entry.TrustLabel == "" {
		return fmt.Errorf("%w: trust_label is required", ErrInvalid)
	}
	if entry.SourceKind == "" {
		return fmt.Errorf("%w: source_kind is required", ErrInvalid)
	}
	return nil
}

func sortEntries(items []Entry) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Enabled != items[j].Enabled {
			return items[i].Enabled
		}
		if !items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		}
		if items[i].Title != items[j].Title {
			return items[i].Title < items[j].Title
		}
		return items[i].ID < items[j].ID
	})
}
