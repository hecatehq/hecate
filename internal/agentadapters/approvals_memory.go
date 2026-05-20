package agentadapters

import (
	"context"
	"sort"
	"sync"
	"time"
)

// MemoryApprovalStore is a goroutine-safe in-process ApprovalStore.
// All state lives in maps and is discarded on process exit. Suitable for
// tests, dev, and anyone running with HECATE_BACKEND=memory (the default).
type MemoryApprovalStore struct {
	mu        sync.Mutex
	approvals map[string]Approval
	grants    map[string]Grant
}

// NewMemoryApprovalStore returns an empty in-memory store.
func NewMemoryApprovalStore() *MemoryApprovalStore {
	return &MemoryApprovalStore{
		approvals: make(map[string]Approval),
		grants:    make(map[string]Grant),
	}
}

func (s *MemoryApprovalStore) CreateApproval(_ context.Context, a Approval) (Approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.ID == "" {
		a.ID = newApprovalID()
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	if a.Status == "" {
		a.Status = ApprovalStatusPending
	}
	s.approvals[a.ID] = a
	return a, nil
}

func (s *MemoryApprovalStore) ResolveApproval(_ context.Context, id string, status ApprovalStatus, decision ApprovalDecision, selectedOption string, scope ApprovalScope, path ApprovalResolutionPath, note string, resolvedAt time.Time) (Approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.approvals[id]
	if !ok {
		return Approval{}, ErrApprovalNotFound
	}
	if row.Status != ApprovalStatusPending {
		return row, ErrApprovalAlreadyResolved
	}
	if resolvedAt.IsZero() {
		resolvedAt = time.Now().UTC()
	}
	row.Status = status
	row.Decision = decision
	row.SelectedOption = selectedOption
	row.Scope = scope
	row.Path = path
	row.DecisionNote = note
	row.ResolvedAt = &resolvedAt
	s.approvals[id] = row
	return row, nil
}

func (s *MemoryApprovalStore) GetApproval(_ context.Context, id string) (Approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.approvals[id]
	if !ok {
		return Approval{}, ErrApprovalNotFound
	}
	return row, nil
}

func (s *MemoryApprovalStore) ListApprovals(_ context.Context, sessionID string, status ApprovalStatus) ([]Approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Approval, 0)
	for _, row := range s.approvals {
		if row.SessionID != sessionID {
			continue
		}
		if status != "" && row.Status != status {
			continue
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// CreateGrant inserts a grant row. Used by tests + the resolve handler
// when an operator picks a scope broader than `once`.
func (s *MemoryApprovalStore) CreateGrant(_ context.Context, g Grant) (Grant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g.ID == "" {
		g.ID = newGrantID()
	}
	if g.GrantedAt.IsZero() {
		g.GrantedAt = time.Now().UTC()
	}
	s.grants[g.ID] = g
	return g, nil
}

func (s *MemoryApprovalStore) FindMatchingGrant(_ context.Context, sessionID, workspace, adapterID, toolKind string, now time.Time) (Grant, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Lookup walks scopes from most-specific to broadest, matching the
	// operator's mental model. Within a scope, the most recently
	// granted entry wins on ties (later overrides earlier intent).
	type candidate struct {
		grant Grant
		rank  int // lower = more specific
	}
	scopeRank := map[ApprovalScope]int{
		ApprovalScopeSession:       0,
		ApprovalScopeWorkspaceTool: 1,
		ApprovalScopeAdapterTool:   2,
	}
	best := candidate{rank: 99}
	bestSet := false
	for _, g := range s.grants {
		if g.AdapterID != adapterID {
			continue
		}
		if g.ToolKind != toolKind {
			continue
		}
		if g.ExpiresAt != nil && !g.ExpiresAt.After(now) {
			continue
		}
		switch g.Scope {
		case ApprovalScopeSession:
			if g.SessionID != sessionID {
				continue
			}
		case ApprovalScopeWorkspaceTool:
			if g.Workspace != workspace {
				continue
			}
		case ApprovalScopeAdapterTool:
			// no extra constraint
		default:
			continue
		}
		rank, ok := scopeRank[g.Scope]
		if !ok {
			continue
		}
		if !bestSet || rank < best.rank || (rank == best.rank && g.GrantedAt.After(best.grant.GrantedAt)) {
			best = candidate{grant: g, rank: rank}
			bestSet = true
		}
	}
	if !bestSet {
		return Grant{}, false, nil
	}
	return best.grant, true, nil
}

// ListGrants returns grants matching the filter. Expired grants are
// dropped using the supplied now timestamp.
func (s *MemoryApprovalStore) ListGrants(_ context.Context, filter GrantFilter, now time.Time) ([]Grant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Grant, 0, len(s.grants))
	for _, g := range s.grants {
		if g.ExpiresAt != nil && !g.ExpiresAt.After(now) {
			continue
		}
		if filter.AdapterID != "" && g.AdapterID != filter.AdapterID {
			continue
		}
		if filter.Scope != "" && g.Scope != filter.Scope {
			continue
		}
		if filter.ToolKind != "" && g.ToolKind != filter.ToolKind {
			continue
		}
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GrantedAt.After(out[j].GrantedAt) })
	return out, nil
}

// DeleteGrant removes a grant by id. Returns ErrApprovalNotFound (the
// shared not-found sentinel) when the id is unknown.
func (s *MemoryApprovalStore) DeleteGrant(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.grants[id]; !ok {
		return ErrApprovalNotFound
	}
	delete(s.grants, id)
	return nil
}

// PruneApprovals deletes resolved approval rows older than maxAge or
// beyond maxCount. Mirrors the SQLite store's behavior so the
// retention worker can dispatch through ApprovalRetentionStore
// without caring which backend is wired. Pending rows are never
// auto-pruned.
func (s *MemoryApprovalStore) PruneApprovals(_ context.Context, now time.Time, maxAge time.Duration, maxCount int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var deleted int64
	if maxAge > 0 {
		cutoff := now.Add(-maxAge)
		for id, row := range s.approvals {
			if row.Status == ApprovalStatusPending {
				continue
			}
			if row.CreatedAt.Before(cutoff) {
				delete(s.approvals, id)
				deleted++
			}
		}
	}
	if maxCount > 0 {
		// Collect non-pending rows, sort newest-first, drop the tail.
		resolved := make([]Approval, 0, len(s.approvals))
		for _, row := range s.approvals {
			if row.Status != ApprovalStatusPending {
				resolved = append(resolved, row)
			}
		}
		sort.Slice(resolved, func(i, j int) bool { return resolved[i].CreatedAt.After(resolved[j].CreatedAt) })
		for i := maxCount; i < len(resolved); i++ {
			delete(s.approvals, resolved[i].ID)
			deleted++
		}
	}
	return deleted, nil
}

// PruneExpiredGrants removes grants whose ExpiresAt has passed. Live
// grants are never touched; the retention worker must not erase
// operator-authored intent.
func (s *MemoryApprovalStore) PruneExpiredGrants(_ context.Context, now time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var deleted int64
	for id, g := range s.grants {
		if g.ExpiresAt != nil && !g.ExpiresAt.After(now) {
			delete(s.grants, id)
			deleted++
		}
	}
	return deleted, nil
}

// Prune implements retention.Pruner. See ApprovalRetentionStore.Prune
// for the contract.
func (s *MemoryApprovalStore) Prune(ctx context.Context, maxAge time.Duration, maxCount int) (int, error) {
	return pruneApprovalsAndGrants(ctx, s, maxAge, maxCount)
}

// ReconcilePending sweeps pending rows and marks them timed_out
// with path=startup_reconcile. The memory backend never has rows
// surviving a restart in practice (the map is process-local), but
// the method exists so memory and sqlite share the
// ApprovalRetentionStore surface. Returns 0 on a normal startup;
// non-zero only if the same process is restarted in-place (rare;
// e.g. tests).
func (s *MemoryApprovalStore) ReconcilePending(_ context.Context, now time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	const note = "process-local waiter lost on restart; reconciled at startup"
	var rec int64
	for id, row := range s.approvals {
		if row.Status != ApprovalStatusPending {
			continue
		}
		row.Status = ApprovalStatusTimedOut
		row.Path = ApprovalResolutionPath("startup_reconcile")
		row.DecisionNote = note
		t := now.UTC()
		row.ResolvedAt = &t
		s.approvals[id] = row
		rec++
	}
	return rec, nil
}
