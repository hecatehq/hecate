package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/config"
)

func TestProjectCoordinationBackendStatus_DefaultHecateAuthoritative(t *testing.T) {
	handler := &Handler{config: config.Config{
		Projects: config.ProjectsConfig{Backend: "sqlite"},
	}}

	status := handler.projectCoordinationBackendStatus()
	if status.ConfiguredBackend != "hecate" || status.AuthoritativeBackend != "hecate" || status.StorageBackend != "sqlite" {
		t.Fatalf("status = %+v, want Hecate authoritative over sqlite project storage", status)
	}
	if status.CairnlineReadSource != "auto" {
		t.Fatalf("cairnline read source = %q, want auto", status.CairnlineReadSource)
	}
	if !status.CairnlineBridgeReady || status.CairnlineAuthoritative || status.ReadModelSwitchReady || status.WriteAdapterReady || status.ReplacementReady || len(status.Warnings) != 0 {
		t.Fatalf("status = %+v, want bridge-ready but inactive Cairnline adapter flags", status)
	}
	if len(status.ReadRoutes) != 0 || len(status.WriteAdapterSeams) != 0 || len(status.WriteAdapterGaps) != 0 || len(status.ReplacementGates) != 0 || len(status.WriteSwitchpoints) != 0 {
		t.Fatalf("status = %+v, want no Cairnline route/seam/gap lists until Cairnline is configured", status)
	}
}

func TestProjectCoordinationBackendStatus_CairnlineConfiguredMissingSources(t *testing.T) {
	handler := &Handler{config: config.Config{
		Projects: config.ProjectsConfig{
			Backend:             "sqlite",
			CoordinationBackend: "cairnline",
		},
	}}

	status := handler.projectCoordinationBackendStatus()
	if status.ConfiguredBackend != "cairnline" || status.AuthoritativeBackend != "hecate" || status.Status != "cairnline_configured_read_adapter_missing_sources" {
		t.Fatalf("status = %+v, want configured Cairnline with missing read-adapter sources", status)
	}
	if status.CairnlineAuthoritative || status.ReadModelSwitchReady || status.WriteAdapterReady || status.ReplacementReady || len(status.Warnings) == 0 {
		t.Fatalf("status = %+v, want read adapter missing-source warnings", status)
	}
	if !strings.Contains(strings.Join(status.Warnings, "\n"), "project assistant proposal store") {
		t.Fatalf("warnings = %+v, want missing assistant proposal store warning", status.Warnings)
	}
	if len(status.ReadRoutes) != 0 {
		t.Fatalf("read routes = %+v, want none until the read adapter is ready", status.ReadRoutes)
	}
	if !containsString(status.WriteAdapterGaps, "projects") || !containsString(status.WriteAdapterGaps, "migration-cutover") {
		t.Fatalf("write gaps = %+v, want structured write-adapter gaps", status.WriteAdapterGaps)
	}
	if !containsString(status.WriteAdapterSeams, "projects") || !containsString(status.WriteAdapterSeams, "project-identity-live-mirror") || !containsString(status.WriteAdapterSeams, "project-roots-live-mirror") || !containsString(status.WriteAdapterSeams, "project-context-sources-live-mirror") || !containsString(status.WriteAdapterSeams, "project-defaults-live-mirror") || !containsString(status.WriteAdapterSeams, "agent-profiles-live-mirror") || !containsString(status.WriteAdapterSeams, "project-skills-live-mirror") || !containsString(status.WriteAdapterSeams, "project-roles-live-mirror") || !containsString(status.WriteAdapterSeams, "project-work-items-live-mirror") || !containsString(status.WriteAdapterSeams, "assignments") || !containsString(status.WriteAdapterSeams, "project-assignments-live-mirror") || !containsString(status.WriteAdapterSeams, "project-assignment-start-result-live-mirror") || !containsString(status.WriteAdapterSeams, "project-assignment-chat-reconcile-live-mirror") || !containsString(status.WriteAdapterSeams, "project-collaboration-live-mirror") || !containsString(status.WriteAdapterSeams, "project-handoffs-live-mirror") || !containsString(status.WriteAdapterSeams, "project-memory-live-mirror") || !containsString(status.WriteAdapterSeams, "project-memory-candidates-live-mirror") || !containsString(status.WriteAdapterSeams, "project-assistant-proposal-ledger-live-mirror") || !containsString(status.WriteAdapterSeams, "project-assistant-apply-side-effects-live-mirror") || !containsString(status.WriteAdapterSeams, "sync-rehearsal") {
		t.Fatalf("write seams = %+v, want structured non-authoritative write-adapter seam coverage", status.WriteAdapterSeams)
	}
	if gate := findReplacementGate(status.ReplacementGates, "read-routes"); gate == nil || gate.Ready || gate.Status != "blocked" {
		t.Fatalf("read-routes gate = %+v, want blocked when read adapter sources are missing", gate)
	}
	if gate := findReplacementGate(status.ReplacementGates, "write-authority-switchpoints"); gate == nil || gate.Ready || gate.Status != "blocked" {
		t.Fatalf("write-authority gate = %+v, want blocking gate", gate)
	}
	if point := findWriteSwitchpoint(status.WriteSwitchpoints, "assignment-start-dispatch"); point == nil || point.CurrentAuthority != "hecate" || point.CairnlineState != "result_mirror_only" || !point.LiveMirror || !point.BlocksAuthority || point.Gap != "assignment-start" {
		t.Fatalf("assignment-start switchpoint = %+v, want Hecate-owned result mirror blocker", point)
	}
}

func TestProjectCoordinationBackendStatus_CairnlineConfiguredReadRoutesReady(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			Backend:             "sqlite",
			CoordinationBackend: "cairnline",
			CairnlineReadSource: "embedded",
		},
	}, quietLogger(), nil, nil, nil, nil)

	status := handler.projectCoordinationBackendStatus()
	if status.ConfiguredBackend != "cairnline" || status.AuthoritativeBackend != "hecate" || status.Status != "cairnline_read_routes_ready" {
		t.Fatalf("status = %+v, want configured Cairnline with read routes ready", status)
	}
	if status.CairnlineReadSource != "embedded" {
		t.Fatalf("cairnline read source = %q, want embedded", status.CairnlineReadSource)
	}
	if status.CairnlineAuthoritative || !status.ReadModelSwitchReady || status.WriteAdapterReady || status.ReplacementReady {
		t.Fatalf("status = %+v, want read adapter ready but Hecate still authoritative", status)
	}
	if len(status.Warnings) == 0 {
		t.Fatalf("status = %+v, want write-adapter/migration warning", status)
	}
	if !strings.Contains(strings.Join(status.Warnings, "\n"), "HECATE_PROJECTS_CAIRNLINE_READ_SOURCE=embedded") {
		t.Fatalf("warnings = %+v, want embedded read-source warning", status.Warnings)
	}
	if !containsString(status.ReadRoutes, "project-list") || !containsString(status.ReadRoutes, "assignment-context") || !containsString(status.ReadRoutes, "launch-readiness") || !containsString(status.ReadRoutes, "assignment-preflight") || !containsString(status.ReadRoutes, "project-assistant-context") || !containsString(status.ReadRoutes, "project-assistant-proposal") || !containsString(status.ReadRoutes, "operations-brief") {
		t.Fatalf("read routes = %+v, want structured Cairnline read-route coverage", status.ReadRoutes)
	}
	if !containsString(status.WriteAdapterGaps, "agent-profiles") || !containsString(status.WriteAdapterGaps, "assignments") || !containsString(status.WriteAdapterGaps, "project-assistant-proposals") || !containsString(status.WriteAdapterGaps, "migration-cutover") {
		t.Fatalf("write gaps = %+v, want structured remaining write-adapter gaps", status.WriteAdapterGaps)
	}
	if !containsString(status.WriteAdapterSeams, "project-identity-live-mirror") || !containsString(status.WriteAdapterSeams, "project-roots-live-mirror") || !containsString(status.WriteAdapterSeams, "project-context-sources-live-mirror") || !containsString(status.WriteAdapterSeams, "project-defaults-live-mirror") || !containsString(status.WriteAdapterSeams, "agent-profiles-live-mirror") || !containsString(status.WriteAdapterSeams, "project-skills-live-mirror") || !containsString(status.WriteAdapterSeams, "project-roles-live-mirror") || !containsString(status.WriteAdapterSeams, "project-work-items-live-mirror") || !containsString(status.WriteAdapterSeams, "project-assignments-live-mirror") || !containsString(status.WriteAdapterSeams, "project-assignment-start-result-live-mirror") || !containsString(status.WriteAdapterSeams, "project-assignment-chat-reconcile-live-mirror") || !containsString(status.WriteAdapterSeams, "project-collaboration-live-mirror") || !containsString(status.WriteAdapterSeams, "project-handoffs-live-mirror") || !containsString(status.WriteAdapterSeams, "project-memory-live-mirror") || !containsString(status.WriteAdapterSeams, "project-memory-candidates-live-mirror") || !containsString(status.WriteAdapterSeams, "project-assistant-proposal-ledger-live-mirror") || !containsString(status.WriteAdapterSeams, "project-assistant-apply-side-effects-live-mirror") || !containsString(status.WriteAdapterSeams, "assignment-status") || !containsString(status.WriteAdapterSeams, "project-assistant-proposal-ledger-import") || !containsString(status.WriteAdapterSeams, "memory-candidates") {
		t.Fatalf("write seams = %+v, want structured non-authoritative write-adapter seam coverage", status.WriteAdapterSeams)
	}
	if gate := findReplacementGate(status.ReplacementGates, "read-routes"); gate == nil || !gate.Ready || gate.Status != "ready" {
		t.Fatalf("read-routes gate = %+v, want ready gate", gate)
	}
	if gate := findReplacementGate(status.ReplacementGates, "strict-embedded-read-smoke"); gate == nil || gate.Ready || gate.Status != "operator_probe_required" {
		t.Fatalf("strict embedded gate = %+v, want operator probe gate", gate)
	}
	if point := findWriteSwitchpoint(status.WriteSwitchpoints, "project-memory"); point == nil || point.CurrentAuthority != "hecate" || point.CairnlineState != "live_mirror_non_authoritative" || !point.LiveMirror || !point.BlocksAuthority || point.Gap != "memory" {
		t.Fatalf("project-memory switchpoint = %+v, want Hecate-owned live mirror blocker", point)
	}
	if point := findWriteSwitchpoint(status.WriteSwitchpoints, "migration-cutover"); point == nil || point.CurrentAuthority != "hecate" || point.CairnlineState != "missing_authoritative_switchpoint" || point.LiveMirror || !point.BlocksAuthority || point.Gap != "migration-cutover" {
		t.Fatalf("migration switchpoint = %+v, want missing authoritative switchpoint blocker", point)
	}
	if status.SyncReadinessURL != projectCoordinationBackendSyncReadinessURL {
		t.Fatalf("sync readiness URL = %q, want %q", status.SyncReadinessURL, projectCoordinationBackendSyncReadinessURL)
	}
	if status.EmbeddedReadModelURL != projectCoordinationBackendEmbeddedReadModelURL {
		t.Fatalf("embedded read-model URL = %q, want %q", status.EmbeddedReadModelURL, projectCoordinationBackendEmbeddedReadModelURL)
	}
	if status.EmbeddedParityReportURL != projectCoordinationBackendEmbeddedParityReportURL {
		t.Fatalf("embedded parity report URL = %q, want %q", status.EmbeddedParityReportURL, projectCoordinationBackendEmbeddedParityReportURL)
	}
	if status.MirrorParityURL != projectCoordinationBackendMirrorParityURL {
		t.Fatalf("mirror parity URL = %q, want %q", status.MirrorParityURL, projectCoordinationBackendMirrorParityURL)
	}
}

func TestProjectCoordinationBackendStatus_CairnlineProjectMemoryAuthorityConfigured(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			Backend:                 "sqlite",
			CoordinationBackend:     "cairnline",
			CairnlineReadSource:     "embedded",
			CairnlineWriteAuthority: "project-memory",
		},
	}, quietLogger(), nil, nil, nil, nil)

	status := handler.projectCoordinationBackendStatus()
	if status.ConfiguredBackend != "cairnline" || status.AuthoritativeBackend != "hecate" || !status.ReadModelSwitchReady || status.ReplacementReady {
		t.Fatalf("status = %+v, want Cairnline read routes ready, partial write authority, and no full replacement readiness", status)
	}
	if containsString(status.WriteAdapterGaps, "memory") {
		t.Fatalf("write gaps = %+v, want accepted project memory removed while memory-candidates still block", status.WriteAdapterGaps)
	}
	if !containsString(status.WriteAdapterGaps, "memory-candidates") {
		t.Fatalf("write gaps = %+v, want memory-candidates to remain blocking", status.WriteAdapterGaps)
	}
	point := findWriteSwitchpoint(status.WriteSwitchpoints, "project-memory")
	if point == nil || point.CurrentAuthority != "cairnline" || point.CairnlineState != "authoritative_opt_in" || !point.LiveMirror || point.BlocksAuthority || point.Gap != "" {
		t.Fatalf("project-memory switchpoint = %+v, want opt-in Cairnline authority", point)
	}
	if gate := findReplacementGate(status.ReplacementGates, "write-authority-switchpoints"); gate == nil || gate.Ready || gate.Status != "blocked" {
		t.Fatalf("write-authority gate = %+v, want full replacement still blocked", gate)
	}
	if !strings.Contains(strings.Join(status.Warnings, "\n"), "Accepted project memory entry mutations are opt-in Cairnline-authoritative") {
		t.Fatalf("warnings = %+v, want project-memory authority warning", status.Warnings)
	}
}

func TestProjectCoordinationBackendStatus_CairnlineProjectMemoryCandidateAuthorityConfigured(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			Backend:                 "sqlite",
			CoordinationBackend:     "cairnline",
			CairnlineReadSource:     "embedded",
			CairnlineWriteAuthority: "project-memory,memory-candidates",
		},
	}, quietLogger(), nil, nil, nil, nil)

	status := handler.projectCoordinationBackendStatus()
	if containsString(status.WriteAdapterGaps, "memory") || containsString(status.WriteAdapterGaps, "memory-candidates") {
		t.Fatalf("write gaps = %+v, want memory and memory-candidates removed for opt-in Cairnline authority", status.WriteAdapterGaps)
	}
	for _, name := range []string{"project-memory", "memory-candidates"} {
		point := findWriteSwitchpoint(status.WriteSwitchpoints, name)
		if point == nil || point.CurrentAuthority != "cairnline" || point.CairnlineState != "authoritative_opt_in" || !point.LiveMirror || point.BlocksAuthority || point.Gap != "" {
			t.Fatalf("%s switchpoint = %+v, want opt-in Cairnline authority", name, point)
		}
	}
	if status.ReplacementReady {
		t.Fatalf("replacement_ready = true, want false until remaining write and migration gates are ready")
	}
	if !strings.Contains(strings.Join(status.Warnings, "\n"), "candidate promotion also creates accepted memory through Cairnline") {
		t.Fatalf("warnings = %+v, want memory-candidate authority warning", status.Warnings)
	}
}

func TestProjectCoordinationBackendStatusRoute(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := config.Config{
		Projects: config.ProjectsConfig{
			Backend:             "sqlite",
			CoordinationBackend: "cairnline",
		},
	}
	server := newTestHTTPHandlerForProviders(logger, nil, cfg)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/backend-status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("backend status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}

	var response ProjectCoordinationBackendStatusEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode backend status: %v", err)
	}
	if response.Object != "project_coordination_backend_status" || response.Data.ConfiguredBackend != "cairnline" || response.Data.AuthoritativeBackend != "hecate" {
		t.Fatalf("response = %+v, want Cairnline configured but Hecate authoritative", response)
	}
	if !response.Data.ReadModelSwitchReady || response.Data.Status != "cairnline_read_routes_ready" {
		t.Fatalf("response = %+v, want Cairnline read routes ready for fully wired test handler", response)
	}
	if !containsString(response.Data.ReadRoutes, "project-detail") || !containsString(response.Data.ReadRoutes, "launch-readiness") || !containsString(response.Data.ReadRoutes, "assignment-preflight") || !containsString(response.Data.ReadRoutes, "project-assistant-context") || !containsString(response.Data.ReadRoutes, "project-assistant-proposal") || !containsString(response.Data.WriteAdapterSeams, "project-identity-live-mirror") || !containsString(response.Data.WriteAdapterSeams, "project-roots-live-mirror") || !containsString(response.Data.WriteAdapterSeams, "project-context-sources-live-mirror") || !containsString(response.Data.WriteAdapterSeams, "project-defaults-live-mirror") || !containsString(response.Data.WriteAdapterSeams, "project-skills-live-mirror") || !containsString(response.Data.WriteAdapterSeams, "project-roles-live-mirror") || !containsString(response.Data.WriteAdapterSeams, "project-work-items-live-mirror") || !containsString(response.Data.WriteAdapterSeams, "project-assignments-live-mirror") || !containsString(response.Data.WriteAdapterSeams, "project-assignment-start-result-live-mirror") || !containsString(response.Data.WriteAdapterSeams, "project-assignment-chat-reconcile-live-mirror") || !containsString(response.Data.WriteAdapterSeams, "project-collaboration-live-mirror") || !containsString(response.Data.WriteAdapterSeams, "project-handoffs-live-mirror") || !containsString(response.Data.WriteAdapterSeams, "project-memory-live-mirror") || !containsString(response.Data.WriteAdapterSeams, "project-memory-candidates-live-mirror") || !containsString(response.Data.WriteAdapterSeams, "project-assistant-proposal-ledger-live-mirror") || !containsString(response.Data.WriteAdapterSeams, "project-assistant-apply-side-effects-live-mirror") || !containsString(response.Data.WriteAdapterSeams, "handoffs") || !containsString(response.Data.WriteAdapterGaps, "memory-candidates") {
		t.Fatalf("response routes/seams/gaps = %+v / %+v / %+v, want structured readiness details", response.Data.ReadRoutes, response.Data.WriteAdapterSeams, response.Data.WriteAdapterGaps)
	}
	if response.Data.ReplacementReady {
		t.Fatalf("response replacement_ready = true, want false until write authority and migration gates are ready")
	}
	if findReplacementGate(response.Data.ReplacementGates, "write-authority-switchpoints") == nil || findWriteSwitchpoint(response.Data.WriteSwitchpoints, "project-assistant-proposals") == nil {
		t.Fatalf("response gates/switchpoints = %+v / %+v, want structured replacement blockers", response.Data.ReplacementGates, response.Data.WriteSwitchpoints)
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func findReplacementGate(items []ProjectCoordinationBackendReplacementGate, id string) *ProjectCoordinationBackendReplacementGate {
	for idx := range items {
		if items[idx].ID == id {
			return &items[idx]
		}
	}
	return nil
}

func findWriteSwitchpoint(items []ProjectCoordinationBackendWriteSwitchpoint, name string) *ProjectCoordinationBackendWriteSwitchpoint {
	for idx := range items {
		if items[idx].Name == name {
			return &items[idx]
		}
	}
	return nil
}
