package api

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
)

func newCairnlineMirrorHealthClock(start time.Time) (*cairnlineMirrorHealth, func(time.Duration)) {
	current := start
	tracker := &cairnlineMirrorHealth{}
	tracker.now = func() time.Time { return current }
	advance := func(d time.Duration) { current = current.Add(d) }
	return tracker, advance
}

func TestCairnlineMirrorHealth_RecordsPerFamilyFailures(t *testing.T) {
	tracker, advance := newCairnlineMirrorHealthClock(time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC))

	tracker.recordFailure(cairnlineMirrorFamilySkills, "project_skill_update", errors.New("disk full"))
	advance(time.Second)
	tracker.recordFailure(cairnlineMirrorFamilySkills, "project_skills_discover", errors.New("still failing"))
	advance(time.Second)
	tracker.recordFailure(cairnlineMirrorFamilyRoots, "project_root_mutation", errors.New("locked"))
	tracker.recordFailure("", "ignored", errors.New("no family"))

	health := tracker.snapshot()
	if health.TotalFailureCount != 3 {
		t.Fatalf("total failure count = %d, want 3", health.TotalFailureCount)
	}
	if len(health.Families) != 2 {
		t.Fatalf("families = %+v, want skills and roots aggregates only", health.Families)
	}
	skills := findMirrorWriteFamilyHealth(health.Families, cairnlineMirrorFamilySkills)
	if skills == nil || skills.FailureCount != 2 || skills.LastError != "still failing" || skills.LastFailedOperation != "project_skills_discover" {
		t.Fatalf("skills health = %+v, want two aggregated failures with latest error and operation", skills)
	}
	if skills.LastFailureAt != "2026-07-08T12:00:01Z" || skills.LastSuccessAt != "" {
		t.Fatalf("skills timestamps = %q / %q, want latest failure timestamp and no success", skills.LastFailureAt, skills.LastSuccessAt)
	}
	roots := findMirrorWriteFamilyHealth(health.Families, cairnlineMirrorFamilyRoots)
	if roots == nil || roots.FailureCount != 1 || roots.LastError != "locked" {
		t.Fatalf("roots health = %+v, want single recorded failure", roots)
	}
	if !containsString(health.DriftingFamilies, cairnlineMirrorFamilyRoots) || !containsString(health.DriftingFamilies, cairnlineMirrorFamilySkills) {
		t.Fatalf("drifting families = %+v, want both failing families", health.DriftingFamilies)
	}
}

func TestCairnlineMirrorHealth_DriftClearsAfterLaterSuccess(t *testing.T) {
	tracker, advance := newCairnlineMirrorHealthClock(time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC))

	tracker.recordFailure(cairnlineMirrorFamilyAssignments, "project_assignment_update", errors.New("boom"))
	advance(time.Second)
	tracker.recordSuccess(cairnlineMirrorFamilyAssignments)
	advance(time.Second)
	tracker.recordSuccess(cairnlineMirrorFamilyMemory)
	advance(time.Second)
	tracker.recordFailure(cairnlineMirrorFamilyMemory, "project_memory_update", errors.New("late failure"))

	health := tracker.snapshot()
	assignments := findMirrorWriteFamilyHealth(health.Families, cairnlineMirrorFamilyAssignments)
	if assignments == nil || assignments.Drifting || assignments.FailureCount != 1 || assignments.LastSuccessAt == "" {
		t.Fatalf("assignments health = %+v, want recovered non-drifting family with retained failure evidence", assignments)
	}
	memoryFamily := findMirrorWriteFamilyHealth(health.Families, cairnlineMirrorFamilyMemory)
	if memoryFamily == nil || !memoryFamily.Drifting {
		t.Fatalf("memory health = %+v, want drifting family after failure that followed a success", memoryFamily)
	}
	if len(health.DriftingFamilies) != 1 || health.DriftingFamilies[0] != cairnlineMirrorFamilyMemory {
		t.Fatalf("drifting families = %+v, want only memory", health.DriftingFamilies)
	}
	if health.TotalFailureCount != 2 {
		t.Fatalf("total failure count = %d, want 2", health.TotalFailureCount)
	}
}

func TestCairnlineMirrorHealth_ConcurrentRecordingIsSafe(t *testing.T) {
	tracker := &cairnlineMirrorHealth{}
	families := []string{
		cairnlineMirrorFamilyProjects,
		cairnlineMirrorFamilyRoots,
		cairnlineMirrorFamilySkills,
		cairnlineMirrorFamilyAssignments,
	}

	var wg sync.WaitGroup
	for _, family := range families {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				tracker.recordFailure(family, "op", errors.New("boom"))
				tracker.recordSuccess(family)
				_ = tracker.snapshot()
			}
		}()
	}
	wg.Wait()

	health := tracker.snapshot()
	if health.TotalFailureCount != int64(len(families)*50) {
		t.Fatalf("total failure count = %d, want %d", health.TotalFailureCount, len(families)*50)
	}
	if len(health.Families) != len(families) {
		t.Fatalf("families = %+v, want one aggregate per recorded family", health.Families)
	}
}

func TestCairnlineMirrorHealth_MirrorHookFailureIsRecorded(t *testing.T) {
	// A regular file where the mirror expects its data directory forces the
	// embedded mirror write itself to fail, exercising the wrapper-level
	// recording path rather than the tracker directly.
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: blocked},
		Projects: config.ProjectsConfig{
			Backend:             "sqlite",
			CoordinationBackend: "cairnline",
		},
	}, quietLogger(), nil, nil, nil, nil)

	handler.mirrorProjectIdentityToCairnline(t.Context(), "project_create", projects.Project{ID: "proj_mirror_fail"})

	health := handler.cairnlineMirrorHealth.snapshot()
	family := findMirrorWriteFamilyHealth(health.Families, cairnlineMirrorFamilyProjects)
	if health.TotalFailureCount != 1 || family == nil || family.FailureCount != 1 || family.LastFailedOperation != "project_create" || family.LastError == "" || !family.Drifting {
		t.Fatalf("mirror health = %+v, want projects family failure recorded by the mirror hook", health)
	}
}

func TestProjectCoordinationBackendStatus_MirrorWriteFailuresSurfaceInStatusPayload(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			Backend:             "sqlite",
			CoordinationBackend: "cairnline",
		},
	}, quietLogger(), nil, nil, nil, nil)
	handler.cairnlineMirrorHealth.recordFailure(cairnlineMirrorFamilySkills, "project_skill_update", errors.New("mirror write exploded"))
	server := NewServer(quietLogger(), handler)
	client := newAPITestClient(t, server)

	response := mustRequestJSONStatus[ProjectCoordinationBackendStatusEnvelope](client, http.StatusOK, http.MethodGet, "/hecate/v1/projects/backend-status", "")
	if response.Object != "project_coordination_backend_status" {
		t.Fatalf("object = %q, want project_coordination_backend_status", response.Object)
	}
	health := response.Data.MirrorWriteHealth
	if health.TotalFailureCount != 1 || len(health.DriftingFamilies) != 1 || health.DriftingFamilies[0] != cairnlineMirrorFamilySkills {
		t.Fatalf("mirror write health = %+v, want one drifting skills failure", health)
	}
	family := findMirrorWriteFamilyHealth(health.Families, cairnlineMirrorFamilySkills)
	if family == nil || !family.Drifting || family.FailureCount != 1 || family.LastError != "mirror write exploded" || family.LastFailedOperation != "project_skill_update" || family.LastFailureAt == "" {
		t.Fatalf("skills family health = %+v, want failure evidence in backend-status payload", family)
	}
	gate := findReplacementGate(response.Data.ReplacementGates, "mirror-write-health")
	if gate == nil || gate.Ready || gate.Status != "drifting" {
		t.Fatalf("mirror write health gate = %+v, want drifting non-ready gate", gate)
	}
	if !containsString(gate.ProbeURLs, projectCoordinationBackendStatusURL) || !containsString(gate.ProbeURLs, projectCoordinationBackendMirrorParityURL) {
		t.Fatalf("mirror write health gate probes = %+v, want status and mirror-parity probes", gate.ProbeURLs)
	}
}

func TestProjectCoordinationBackendStatus_MirrorDriftBlocksReplacementReady(t *testing.T) {
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			Backend:                  "sqlite",
			CoordinationBackend:      "cairnline",
			CairnlineConnector:       "embedded",
			CairnlineReadSource:      "embedded",
			CairnlineReplacementMode: "embedded",
		},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetProjectWorkStore(projectwork.NewMemoryStore())
	handler.SetProjectSkillStore(projectskills.NewMemoryStore())
	handler.SetMemoryStore(memory.NewMemoryStore())
	handler.SetAgentProfileStore(agentprofiles.NewMemoryStore())
	server := NewServer(quietLogger(), handler)
	client := newAPITestClient(t, server)

	mustRequestJSONStatus[ProjectResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects", projectJourneyJSON(t, map[string]any{
		"name":             "Mirror Drift Blocks Replacement",
		"default_provider": "anthropic",
		"default_model":    "claude-sonnet-4",
	}))

	status := handler.projectCoordinationBackendStatusWithContext(t.Context())
	if !status.ReplacementReady {
		t.Fatalf("status = %+v, want replacement ready before any mirror failure is recorded", status)
	}
	if gate := findReplacementGate(status.ReplacementGates, "mirror-write-health"); gate == nil || !gate.Ready || gate.Status != "healthy" {
		t.Fatalf("mirror write health gate = %+v, want healthy ready gate without failures", gate)
	}

	handler.cairnlineMirrorHealth.recordFailure(cairnlineMirrorFamilyAssignments, "project_assignment_update", errors.New("mirror down"))
	status = handler.projectCoordinationBackendStatusWithContext(t.Context())
	if status.ReplacementReady || status.AuthoritativeBackend != "hecate" || status.CairnlineAuthoritative {
		t.Fatalf("status = %+v, want outstanding mirror drift to block replacement readiness", status)
	}
	gate := findReplacementGate(status.ReplacementGates, "mirror-write-health")
	if gate == nil || gate.Ready || gate.Status != "drifting" {
		t.Fatalf("mirror write health gate = %+v, want drifting gate blocking replacement", gate)
	}

	handler.cairnlineMirrorHealth.recordSuccess(cairnlineMirrorFamilyAssignments)
	status = handler.projectCoordinationBackendStatusWithContext(t.Context())
	if !status.ReplacementReady {
		t.Fatalf("status = %+v, want replacement ready again after the failing family recovered", status)
	}
	if gate := findReplacementGate(status.ReplacementGates, "mirror-write-health"); gate == nil || !gate.Ready || gate.Status != "recovered" {
		t.Fatalf("mirror write health gate = %+v, want ready recovered gate after later success", gate)
	}
}

func TestProjectCairnlineMirrorParityAPI_IncludesMirrorWriteHealth(t *testing.T) {
	handler := NewHandler(config.Config{
		Server:   config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{CoordinationBackend: "cairnline"},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetProjectWorkStore(projectwork.NewMemoryStore())
	handler.SetProjectSkillStore(projectskills.NewMemoryStore())
	handler.SetMemoryStore(memory.NewMemoryStore())
	handler.SetAgentProfileStore(agentprofiles.NewMemoryStore())
	handler.cairnlineMirrorHealth.recordFailure(cairnlineMirrorFamilyHandoffs, "project_handoff_update", errors.New("mirror rejected handoff"))
	server := NewServer(quietLogger(), handler)
	client := newAPITestClient(t, server)

	mustRequestJSONStatus[ProjectResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects", projectJourneyJSON(t, map[string]any{
		"name": "Mirror Parity Health",
	}))
	response := mustRequestJSONStatus[ProjectCairnlineSyncResponse](client, http.StatusOK, http.MethodGet, "/hecate/v1/projects/cairnline/mirror-parity", "")
	health := response.Data.MirrorWriteHealth
	if health == nil || health.TotalFailureCount != 1 {
		t.Fatalf("mirror write health = %+v, want recorded failure in parity output", health)
	}
	if len(health.DriftingFamilies) != 1 || health.DriftingFamilies[0] != cairnlineMirrorFamilyHandoffs {
		t.Fatalf("drifting families = %+v, want handoffs", health.DriftingFamilies)
	}
	family := findMirrorWriteFamilyHealth(health.Families, cairnlineMirrorFamilyHandoffs)
	if family == nil || !family.Drifting || family.LastError != "mirror rejected handoff" {
		t.Fatalf("handoffs family health = %+v, want drifting failure evidence in parity output", family)
	}
}

func findMirrorWriteFamilyHealth(items []ProjectCairnlineMirrorWriteFamilyHealth, family string) *ProjectCairnlineMirrorWriteFamilyHealth {
	for idx := range items {
		if items[idx].Family == family {
			return &items[idx]
		}
	}
	return nil
}
