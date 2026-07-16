package api

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

var (
	errProjectMutationFenceOrder = errors.New("a project mutation cannot expand an active project fence")
	errProjectMutationClosed     = errors.New("project deletion is in progress")
)

type projectMutationLeaseContextKey struct{}

type projectMutationState struct {
	active      int
	destructive bool
	changed     chan struct{}
}

// projectMutationGate admits ordinary facade mutations concurrently but gives
// project deletion an exclusive closure over one portable project. The fence
// belongs to API composition: Cairnline owns the graph, while Hecate cleanup
// and compatibility shadows cannot share its transaction.
//
// A context-carried lease makes nested application/facade calls re-entrant.
// Multi-project operations acquire their complete, sorted key set atomically;
// nested calls may use a subset but cannot incrementally expand it. Project
// deletion has one additional ordering rule: acquire the process-wide
// destructive state closure before closing its project key.
type projectMutationGate struct {
	mu     sync.Mutex
	states map[string]*projectMutationState
}

type projectMutationLease struct {
	gate       *projectMutationGate
	projectIDs []string
	active     atomic.Bool
}

func (g *projectMutationGate) begin(ctx context.Context, projectID string) (context.Context, func(), error) {
	return g.beginMany(ctx, []string{projectID})
}

func (g *projectMutationGate) beginMany(ctx context.Context, projectIDs []string) (context.Context, func(), error) {
	ctx, projectIDs, nested, err := g.prepareMany(ctx, projectIDs)
	if err != nil || nested {
		return ctx, func() {}, err
	}
	if len(projectIDs) == 0 {
		return ctx, func() {}, nil
	}

	g.mu.Lock()
	for _, projectID := range projectIDs {
		if state := g.states[projectID]; state != nil && state.destructive {
			g.mu.Unlock()
			return ctx, nil, errProjectMutationClosed
		}
	}
	states := make([]*projectMutationState, len(projectIDs))
	for index, projectID := range projectIDs {
		state := g.stateLocked(projectID)
		state.active++
		states[index] = state
	}
	g.mu.Unlock()
	mutationCtx, release := g.withLease(ctx, projectIDs, func() { g.releaseMutations(projectIDs, states) })
	return mutationCtx, release, nil
}

// tryBegin is for best-effort runtime projections that must never join a
// destructive lifecycle wait cycle. A skipped projection is reconsidered by
// the next terminal or read reconciliation.
func (g *projectMutationGate) tryBegin(ctx context.Context, projectID string) (context.Context, func(), bool, error) {
	ctx, projectIDs, nested, err := g.prepareMany(ctx, []string{projectID})
	if err != nil {
		return ctx, nil, false, err
	}
	if nested || len(projectIDs) == 0 {
		return ctx, func() {}, true, nil
	}
	projectID = projectIDs[0]

	g.mu.Lock()
	state := g.stateLocked(projectID)
	if state.destructive {
		g.pruneLocked(projectID, state)
		g.mu.Unlock()
		return ctx, nil, false, nil
	}
	state.active++
	g.mu.Unlock()
	mutationCtx, release := g.withLease(ctx, projectIDs, func() { g.releaseMutations(projectIDs, []*projectMutationState{state}) })
	return mutationCtx, release, true, nil
}

func (g *projectMutationGate) beginDestructive(ctx context.Context, projectID string) (context.Context, func(), error) {
	ctx, projectIDs, nested, err := g.prepareMany(ctx, []string{projectID})
	if err != nil || nested {
		if err != nil {
			return ctx, nil, err
		}
		return ctx, nil, errProjectMutationFenceOrder
	}
	if len(projectIDs) == 0 {
		return ctx, func() {}, nil
	}
	projectID = projectIDs[0]

	g.mu.Lock()
	state := g.stateLocked(projectID)
	if state.destructive {
		g.mu.Unlock()
		return ctx, nil, errProjectMutationClosed
	}
	state.destructive = true
	g.notifyLocked(state)
	for state.active > 0 {
		changed := state.changed
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			g.finishDestructive(projectID, state)
			return ctx, nil, ctx.Err()
		case <-changed:
		}
		g.mu.Lock()
	}
	// A release notification and cancellation can become ready together. Check
	// the caller once more while the closure is still owned so a canceled
	// deletion never slips through merely because select observed the release.
	if err := ctx.Err(); err != nil {
		state.destructive = false
		g.notifyLocked(state)
		g.pruneLocked(projectID, state)
		g.mu.Unlock()
		return ctx, nil, err
	}
	g.mu.Unlock()
	destructiveCtx, release := g.withLease(ctx, projectIDs, func() { g.finishDestructive(projectID, state) })
	return destructiveCtx, release, nil
}

func (g *projectMutationGate) prepareMany(ctx context.Context, projectIDs []string) (context.Context, []string, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	projectIDs = normalizeProjectMutationIDs(projectIDs)
	if err := ctx.Err(); err != nil {
		return ctx, projectIDs, false, err
	}
	if len(projectIDs) == 0 {
		return ctx, nil, false, nil
	}
	if existing, ok := ctx.Value(projectMutationLeaseContextKey{}).(*projectMutationLease); ok && existing != nil && existing.active.Load() {
		if existing.gate != g || !existing.containsAll(projectIDs) {
			return ctx, projectIDs, false, errProjectMutationFenceOrder
		}
		return ctx, projectIDs, true, nil
	}
	return ctx, projectIDs, false, nil
}

func normalizeProjectMutationIDs(projectIDs []string) []string {
	seen := make(map[string]struct{}, len(projectIDs))
	normalized := make([]string, 0, len(projectIDs))
	for _, projectID := range projectIDs {
		projectID = strings.TrimSpace(projectID)
		if projectID == "" {
			continue
		}
		if _, ok := seen[projectID]; ok {
			continue
		}
		seen[projectID] = struct{}{}
		normalized = append(normalized, projectID)
	}
	sort.Strings(normalized)
	return normalized
}

func (l *projectMutationLease) containsAll(projectIDs []string) bool {
	if l == nil || len(projectIDs) > len(l.projectIDs) {
		return false
	}
	for _, projectID := range projectIDs {
		index := sort.SearchStrings(l.projectIDs, projectID)
		if index >= len(l.projectIDs) || l.projectIDs[index] != projectID {
			return false
		}
	}
	return true
}

func (g *projectMutationGate) withLease(ctx context.Context, projectIDs []string, finish func()) (context.Context, func()) {
	lease := &projectMutationLease{gate: g, projectIDs: append([]string(nil), projectIDs...)}
	lease.active.Store(true)
	var once sync.Once
	release := func() {
		once.Do(func() {
			lease.active.Store(false)
			finish()
		})
	}
	return context.WithValue(ctx, projectMutationLeaseContextKey{}, lease), release
}

func (g *projectMutationGate) stateLocked(projectID string) *projectMutationState {
	if g.states == nil {
		g.states = make(map[string]*projectMutationState)
	}
	state := g.states[projectID]
	if state == nil {
		state = &projectMutationState{changed: make(chan struct{})}
		g.states[projectID] = state
	}
	return state
}

func (g *projectMutationGate) releaseMutations(projectIDs []string, states []*projectMutationState) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for index, projectID := range projectIDs {
		state := states[index]
		if state.active > 0 {
			state.active--
		}
		g.notifyLocked(state)
		g.pruneLocked(projectID, state)
	}
}

func (g *projectMutationGate) finishDestructive(projectID string, state *projectMutationState) {
	g.mu.Lock()
	defer g.mu.Unlock()
	state.destructive = false
	g.notifyLocked(state)
	g.pruneLocked(projectID, state)
}

func (g *projectMutationGate) notifyLocked(state *projectMutationState) {
	close(state.changed)
	state.changed = make(chan struct{})
}

func (g *projectMutationGate) pruneLocked(projectID string, state *projectMutationState) {
	if state.active == 0 && !state.destructive && g.states[projectID] == state {
		delete(g.states, projectID)
	}
}

func (h *Handler) projectMutationHandler(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, release, err := h.projectMutationGate.begin(r.Context(), r.PathValue("id"))
		if err != nil {
			WriteError(w, http.StatusConflict, errCodeConflict, err.Error())
			return
		}
		defer release()
		next(w, r.WithContext(ctx))
	}
}
