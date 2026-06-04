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
	TrustLabelGenerated      = "generated_summary"
	SourceKindOperator       = "operator"
	SourceKindGenerated      = "generated"
	CandidateStatusPending   = "pending"
	CandidateStatusPromoted  = "promoted"
	CandidateStatusRejected  = "rejected"
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

type Candidate struct {
	ID                  string
	ProjectID           string
	Title               string
	Body                string
	SuggestedKind       string
	SuggestedTrustLabel string
	SuggestedSourceKind string
	SuggestedSourceID   string
	SourceRefs          []CandidateSourceRef
	Status              string
	StatusReason        string
	PromotedMemoryID    string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type CandidateSourceRef struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	URL   string `json:"url,omitempty"`
}

type Filter struct {
	ProjectID       string
	IncludeDisabled bool
}

type CandidateFilter struct {
	ProjectID string
	Status    string
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

type CandidateStore interface {
	Backend() string
	CreateCandidate(ctx context.Context, candidate Candidate) (Candidate, error)
	GetCandidate(ctx context.Context, projectID, id string) (Candidate, bool, error)
	ListCandidates(ctx context.Context, filter CandidateFilter) ([]Candidate, error)
	UpdateCandidate(ctx context.Context, projectID, id string, update func(*Candidate)) (Candidate, error)
	DeleteCandidatesByProjectID(ctx context.Context, projectID string) (int, error)
}

type MemoryStore struct {
	mu         sync.Mutex
	entries    map[string]Entry
	candidates map[string]Candidate
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		entries:    make(map[string]Entry),
		candidates: make(map[string]Candidate),
	}
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

func (s *MemoryStore) CreateCandidate(_ context.Context, candidate Candidate) (Candidate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate = normalizeCandidate(candidate, time.Now().UTC())
	if err := validateCandidate(candidate); err != nil {
		return Candidate{}, err
	}
	if _, ok := s.candidates[candidate.ID]; ok {
		return Candidate{}, ErrAlreadyExists
	}
	s.candidates[candidate.ID] = candidate
	return candidate, nil
}

func (s *MemoryStore) GetCandidate(_ context.Context, projectID, id string) (Candidate, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate, ok := s.candidates[strings.TrimSpace(id)]
	if !ok || strings.TrimSpace(projectID) != candidate.ProjectID {
		return Candidate{}, false, nil
	}
	return cloneCandidate(candidate), true, nil
}

func (s *MemoryStore) ListCandidates(_ context.Context, filter CandidateFilter) ([]Candidate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projectID := strings.TrimSpace(filter.ProjectID)
	status := strings.TrimSpace(filter.Status)
	items := make([]Candidate, 0, len(s.candidates))
	for _, candidate := range s.candidates {
		if projectID != "" && candidate.ProjectID != projectID {
			continue
		}
		if status != "" && candidate.Status != status {
			continue
		}
		items = append(items, cloneCandidate(candidate))
	}
	sortCandidates(items)
	return items, nil
}

func (s *MemoryStore) UpdateCandidate(_ context.Context, projectID, id string, update func(*Candidate)) (Candidate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projectID = strings.TrimSpace(projectID)
	id = strings.TrimSpace(id)
	candidate, ok := s.candidates[id]
	if !ok || candidate.ProjectID != projectID {
		return Candidate{}, ErrNotFound
	}
	originalID := candidate.ID
	originalProjectID := candidate.ProjectID
	originalCreatedAt := candidate.CreatedAt
	if update != nil {
		update(&candidate)
	}
	if strings.TrimSpace(candidate.ID) != originalID {
		return Candidate{}, fmt.Errorf("%w: memory candidate id cannot be changed", ErrInvalid)
	}
	if strings.TrimSpace(candidate.ProjectID) != originalProjectID {
		return Candidate{}, fmt.Errorf("%w: project_id cannot be changed", ErrInvalid)
	}
	candidate.ID = originalID
	candidate.ProjectID = originalProjectID
	candidate.CreatedAt = originalCreatedAt
	candidate.UpdatedAt = time.Now().UTC()
	candidate = normalizeCandidate(candidate, candidate.UpdatedAt)
	if err := validateCandidate(candidate); err != nil {
		return Candidate{}, err
	}
	s.candidates[id] = candidate
	return cloneCandidate(candidate), nil
}

func (s *MemoryStore) DeleteCandidatesByProjectID(_ context.Context, projectID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return 0, nil
	}
	deleted := 0
	for id, candidate := range s.candidates {
		if candidate.ProjectID != projectID {
			continue
		}
		delete(s.candidates, id)
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

func normalizeCandidate(candidate Candidate, now time.Time) Candidate {
	candidate.ID = strings.TrimSpace(candidate.ID)
	candidate.ProjectID = strings.TrimSpace(candidate.ProjectID)
	candidate.Title = strings.TrimSpace(candidate.Title)
	candidate.Body = strings.TrimSpace(candidate.Body)
	candidate.SuggestedKind = strings.TrimSpace(candidate.SuggestedKind)
	candidate.SuggestedTrustLabel = strings.TrimSpace(candidate.SuggestedTrustLabel)
	if candidate.SuggestedTrustLabel == "" {
		candidate.SuggestedTrustLabel = TrustLabelGenerated
	}
	candidate.SuggestedSourceKind = strings.TrimSpace(candidate.SuggestedSourceKind)
	if candidate.SuggestedSourceKind == "" {
		candidate.SuggestedSourceKind = SourceKindGenerated
	}
	candidate.SuggestedSourceID = strings.TrimSpace(candidate.SuggestedSourceID)
	candidate.Status = strings.TrimSpace(candidate.Status)
	if candidate.Status == "" {
		candidate.Status = CandidateStatusPending
	}
	candidate.StatusReason = strings.TrimSpace(candidate.StatusReason)
	candidate.PromotedMemoryID = strings.TrimSpace(candidate.PromotedMemoryID)
	candidate.SourceRefs = normalizeSourceRefs(candidate.SourceRefs)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if candidate.CreatedAt.IsZero() {
		candidate.CreatedAt = now
	}
	if candidate.UpdatedAt.IsZero() {
		candidate.UpdatedAt = candidate.CreatedAt
	}
	return candidate
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

func validateCandidate(candidate Candidate) error {
	if candidate.ID == "" {
		return fmt.Errorf("%w: memory candidate id is required", ErrInvalid)
	}
	if candidate.ProjectID == "" {
		return fmt.Errorf("%w: project_id is required", ErrInvalid)
	}
	if candidate.Title == "" {
		return fmt.Errorf("%w: title is required", ErrInvalid)
	}
	if candidate.Body == "" {
		return fmt.Errorf("%w: body is required", ErrInvalid)
	}
	if len(candidate.Body) > 20000 {
		return fmt.Errorf("%w: body must be 20000 characters or fewer", ErrInvalid)
	}
	if candidate.SuggestedTrustLabel == "" {
		return fmt.Errorf("%w: suggested_trust_label is required", ErrInvalid)
	}
	if candidate.SuggestedSourceKind == "" {
		return fmt.Errorf("%w: suggested_source_kind is required", ErrInvalid)
	}
	switch candidate.Status {
	case CandidateStatusPending, CandidateStatusPromoted, CandidateStatusRejected:
	default:
		return fmt.Errorf("%w: unsupported memory candidate status", ErrInvalid)
	}
	if candidate.Status != CandidateStatusPromoted && candidate.PromotedMemoryID != "" {
		return fmt.Errorf("%w: promoted_memory_id requires promoted status", ErrInvalid)
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

func sortCandidates(items []Candidate) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Status != items[j].Status {
			return candidateStatusRank(items[i].Status) < candidateStatusRank(items[j].Status)
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

func candidateStatusRank(status string) int {
	switch status {
	case CandidateStatusPending:
		return 0
	case CandidateStatusPromoted:
		return 1
	case CandidateStatusRejected:
		return 2
	default:
		return 3
	}
}

func normalizeSourceRefs(refs []CandidateSourceRef) []CandidateSourceRef {
	if len(refs) == 0 {
		return nil
	}
	out := make([]CandidateSourceRef, 0, len(refs))
	for _, ref := range refs {
		ref.Kind = strings.TrimSpace(ref.Kind)
		ref.ID = strings.TrimSpace(ref.ID)
		ref.Title = strings.TrimSpace(ref.Title)
		ref.URL = strings.TrimSpace(ref.URL)
		if ref.Kind == "" && ref.ID == "" && ref.Title == "" && ref.URL == "" {
			continue
		}
		out = append(out, ref)
	}
	return out
}

func cloneCandidate(candidate Candidate) Candidate {
	candidate.SourceRefs = append([]CandidateSourceRef(nil), candidate.SourceRefs...)
	return candidate
}
