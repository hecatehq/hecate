package api

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// Portable write family names tracked by the shadow-mirror health monitor.
// They reuse the family vocabulary of the coordination backend gap and
// switchpoint reporting so operators can correlate mirror drift with the
// portable write authority it endangers.
const (
	cairnlineMirrorFamilyProjects           = "projects"
	cairnlineMirrorFamilyRoots              = "roots"
	cairnlineMirrorFamilyContextSources     = "context-sources"
	cairnlineMirrorFamilySkills             = "skills"
	cairnlineMirrorFamilyRoles              = "roles"
	cairnlineMirrorFamilyWorkItems          = "work-items"
	cairnlineMirrorFamilyAssignments        = "assignments"
	cairnlineMirrorFamilyArtifacts          = "artifacts"
	cairnlineMirrorFamilyHandoffs           = "handoffs"
	cairnlineMirrorFamilyMemory             = "memory"
	cairnlineMirrorFamilyMemoryCandidates   = "memory-candidates"
	cairnlineMirrorFamilyAssistantProposals = "project-assistant-proposals"
)

// cairnlineMirrorHealth aggregates shadow-mirror write outcomes per portable
// write family. It is intentionally in-memory: the counters are process-local
// runtime observability for the best-effort mirror hooks, not persisted
// coordination state, and they reset on restart together with the mirror
// process they observe. The zero value is usable so handler construction
// paths (including test fixtures) need no extra wiring.
type cairnlineMirrorHealth struct {
	mu       sync.Mutex
	families map[string]*cairnlineMirrorFamilyState
	// now is a test seam for deterministic drift ordering; nil falls back
	// to time.Now.
	now func() time.Time
}

type cairnlineMirrorFamilyState struct {
	failureCount        int64
	lastFailedOperation string
	lastError           string
	lastFailureAt       time.Time
	lastSuccessAt       time.Time
}

func (m *cairnlineMirrorHealth) timestamp() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

func (m *cairnlineMirrorHealth) familyState(family string) *cairnlineMirrorFamilyState {
	if m.families == nil {
		m.families = make(map[string]*cairnlineMirrorFamilyState)
	}
	state, ok := m.families[family]
	if !ok {
		state = &cairnlineMirrorFamilyState{}
		m.families[family] = state
	}
	return state
}

func (m *cairnlineMirrorHealth) recordFailure(family, operation string, err error) {
	family = strings.TrimSpace(family)
	if m == nil || family == "" {
		return
	}
	message := ""
	if err != nil {
		message = err.Error()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.familyState(family)
	state.failureCount++
	state.lastFailedOperation = operation
	state.lastError = message
	state.lastFailureAt = m.timestamp()
}

func (m *cairnlineMirrorHealth) recordSuccess(family string) {
	family = strings.TrimSpace(family)
	if m == nil || family == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.familyState(family).lastSuccessAt = m.timestamp()
}

// snapshot projects the tracker into the API shape. A family is drifting when
// its most recent mirror write failed: until a later write for that family
// succeeds (or the operator resyncs the mirror), the embedded Cairnline copy
// cannot be assumed to match Hecate's authoritative stores.
func (m *cairnlineMirrorHealth) snapshot() ProjectCairnlineMirrorWriteHealth {
	out := ProjectCairnlineMirrorWriteHealth{}
	if m == nil {
		return out
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make([]string, 0, len(m.families))
	for name := range m.families {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		state := m.families[name]
		item := ProjectCairnlineMirrorWriteFamilyHealth{
			Family:              name,
			FailureCount:        state.failureCount,
			LastError:           state.lastError,
			LastFailedOperation: state.lastFailedOperation,
		}
		if !state.lastFailureAt.IsZero() {
			item.LastFailureAt = state.lastFailureAt.UTC().Format(time.RFC3339Nano)
		}
		if !state.lastSuccessAt.IsZero() {
			item.LastSuccessAt = state.lastSuccessAt.UTC().Format(time.RFC3339Nano)
		}
		// A same-instant failure/success tie counts as drifting on purpose:
		// when ordering is ambiguous the safe read is that the mirror may be
		// behind.
		item.Drifting = state.failureCount > 0 &&
			(state.lastSuccessAt.IsZero() || !state.lastFailureAt.Before(state.lastSuccessAt))
		out.TotalFailureCount += state.failureCount
		if item.Drifting {
			out.DriftingFamilies = append(out.DriftingFamilies, name)
		}
		out.Families = append(out.Families, item)
	}
	return out
}

// recordCairnlineMirrorFailure keeps the existing operator log signal and
// additionally records the failure against its write family so backend-status
// and mirror-parity output can surface the evidence.
func (h *Handler) recordCairnlineMirrorFailure(ctx context.Context, family, operation, projectID string, err error) {
	h.logCairnlineMirrorError(ctx, operation, projectID, err)
	if h != nil {
		h.cairnlineMirrorHealth.recordFailure(family, operation, err)
	}
}

// recordCairnlineMirrorResult funnels every shadow-mirror write outcome into
// the health tracker. Successes matter as much as failures: the drifting
// classification depends on whether a family has mirrored successfully since
// its last failure.
func (h *Handler) recordCairnlineMirrorResult(ctx context.Context, family, operation, projectID string, err error) {
	if err != nil {
		h.recordCairnlineMirrorFailure(ctx, family, operation, projectID, err)
		return
	}
	// A disabled embedded connector returns nil without attempting the
	// write; recording success there would fabricate mirror freshness.
	if h == nil || !h.projectCairnlineEmbeddedConnectorEnabled() {
		return
	}
	h.cairnlineMirrorHealth.recordSuccess(family)
}
