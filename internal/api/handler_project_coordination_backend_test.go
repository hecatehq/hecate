package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
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
	if status.CairnlineConnector != "embedded" || !status.CairnlineConnectorReady {
		t.Fatalf("cairnline connector = %q ready=%t, want embedded ready", status.CairnlineConnector, status.CairnlineConnectorReady)
	}
	if status.CairnlineSidecarProbeURL != projectCoordinationBackendSidecarProbeURL {
		t.Fatalf("sidecar probe URL = %q, want %q", status.CairnlineSidecarProbeURL, projectCoordinationBackendSidecarProbeURL)
	}
	if status.CairnlineSidecarConnectURL != projectCoordinationBackendSidecarConnectURL {
		t.Fatalf("sidecar connect URL = %q, want %q", status.CairnlineSidecarConnectURL, projectCoordinationBackendSidecarConnectURL)
	}
	if status.CairnlineSidecarReadURL != projectCoordinationBackendSidecarReadURL {
		t.Fatalf("sidecar read URL = %q, want %q", status.CairnlineSidecarReadURL, projectCoordinationBackendSidecarReadURL)
	}
	if status.CairnlineSidecarDetailURL != projectCoordinationBackendSidecarDetailURL {
		t.Fatalf("sidecar detail URL = %q, want %q", status.CairnlineSidecarDetailURL, projectCoordinationBackendSidecarDetailURL)
	}
	if status.CairnlineSidecarCoordinationURL != projectCoordinationBackendSidecarCoordinationURL {
		t.Fatalf("sidecar coordination URL = %q, want %q", status.CairnlineSidecarCoordinationURL, projectCoordinationBackendSidecarCoordinationURL)
	}
	if status.CairnlineSidecarAssignmentContextURL != projectCoordinationBackendSidecarAssignmentContextURL {
		t.Fatalf("sidecar assignment context URL = %q, want %q", status.CairnlineSidecarAssignmentContextURL, projectCoordinationBackendSidecarAssignmentContextURL)
	}
	if status.CairnlineSidecarLaunchPacketURL != projectCoordinationBackendSidecarLaunchPacketURL {
		t.Fatalf("sidecar launch packet URL = %q, want %q", status.CairnlineSidecarLaunchPacketURL, projectCoordinationBackendSidecarLaunchPacketURL)
	}
	if status.CairnlineSidecarLifecycleURL != projectCoordinationBackendSidecarLifecycleURL {
		t.Fatalf("sidecar lifecycle URL = %q, want %q", status.CairnlineSidecarLifecycleURL, projectCoordinationBackendSidecarLifecycleURL)
	}
	if status.CairnlineSidecarSetupURL != projectCoordinationBackendSidecarSetupURL {
		t.Fatalf("sidecar setup URL = %q, want %q", status.CairnlineSidecarSetupURL, projectCoordinationBackendSidecarSetupURL)
	}
	if status.CairnlineSidecarWriteURL != projectCoordinationBackendSidecarWriteURL {
		t.Fatalf("sidecar write URL = %q, want %q", status.CairnlineSidecarWriteURL, projectCoordinationBackendSidecarWriteURL)
	}
	if status.CairnlineSidecarWorkURL != projectCoordinationBackendSidecarWorkURL {
		t.Fatalf("sidecar work URL = %q, want %q", status.CairnlineSidecarWorkURL, projectCoordinationBackendSidecarWorkURL)
	}
	if status.CairnlineSidecarCollaborationURL != projectCoordinationBackendSidecarCollaborationURL {
		t.Fatalf("sidecar collaboration URL = %q, want %q", status.CairnlineSidecarCollaborationURL, projectCoordinationBackendSidecarCollaborationURL)
	}
	if status.CairnlineSidecarMemoryURL != projectCoordinationBackendSidecarMemoryURL {
		t.Fatalf("sidecar memory URL = %q, want %q", status.CairnlineSidecarMemoryURL, projectCoordinationBackendSidecarMemoryURL)
	}
	if status.CairnlineSidecarAssistantURL != projectCoordinationBackendSidecarAssistantURL {
		t.Fatalf("sidecar assistant URL = %q, want %q", status.CairnlineSidecarAssistantURL, projectCoordinationBackendSidecarAssistantURL)
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
	if gate := findReplacementGate(status.ReplacementGates, "migration-and-rollback"); gate == nil || gate.Ready || gate.Status != "rehearsal_available" {
		t.Fatalf("migration gate = %+v, want rehearsal-available blocker", gate)
	}
	if point := findWriteSwitchpoint(status.WriteSwitchpoints, "assignment-start-dispatch"); point == nil || point.CurrentAuthority != "hecate" || point.CairnlineState != "result_mirror_only" || !point.LiveMirror || !point.BlocksAuthority || point.Gap != "assignment-start" {
		t.Fatalf("assignment-start switchpoint = %+v, want Hecate-owned result mirror blocker", point)
	}
}

func TestProjectCoordinationBackendStatus_CairnlineSidecarConnectorBlocksEmbeddedRoutes(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			Backend:                 "sqlite",
			CoordinationBackend:     "cairnline",
			CairnlineConnector:      "sidecar",
			CairnlineReadSource:     "embedded",
			CairnlineWriteAuthority: strings.Join([]string{projectCairnlineWriteAuthorityProjectIdentity, projectCairnlineWriteAuthorityProjectMetadataDefaults, projectCairnlineWriteAuthorityProjectAssignments, "project-memory", "memory-candidates"}, ","),
		},
	}, quietLogger(), nil, nil, nil, nil)

	status := handler.projectCoordinationBackendStatus()
	if status.CairnlineConnector != "sidecar" || status.CairnlineConnectorReady {
		t.Fatalf("cairnline connector = %q ready=%t, want sidecar not ready for live routing", status.CairnlineConnector, status.CairnlineConnectorReady)
	}
	if status.Status != "cairnline_connector_not_ready" || status.CairnlineAuthoritative || status.ReadModelSwitchReady || status.WriteAdapterReady || status.ReplacementReady {
		t.Fatalf("status = %+v, want sidecar connector mode to block live Cairnline routing", status)
	}
	if !strings.Contains(status.CairnlineConnectorDetail, "lifecycle/write/setup/work/collaboration/memory/assistant diagnostics") || strings.Contains(status.CairnlineConnectorDetail, "read-smoke surfaces only") {
		t.Fatalf("connector detail = %q, want full sidecar diagnostic surface", status.CairnlineConnectorDetail)
	}
	if handler.projectReadRoutesUseCairnlineReadModel() || handler.projectIdentityWritesUseCairnlineAuthority() || handler.projectMemoryWritesUseCairnlineAuthority() || handler.projectAssignmentWritesUseCairnlineAuthority() {
		t.Fatal("sidecar connector enabled embedded Cairnline read/write route predicates, want connect/probe-only mode")
	}
	if status.CairnlineSidecarProbeURL != projectCoordinationBackendSidecarProbeURL {
		t.Fatalf("sidecar probe URL = %q, want %q", status.CairnlineSidecarProbeURL, projectCoordinationBackendSidecarProbeURL)
	}
	if status.CairnlineSidecarConnectURL != projectCoordinationBackendSidecarConnectURL {
		t.Fatalf("sidecar connect URL = %q, want %q", status.CairnlineSidecarConnectURL, projectCoordinationBackendSidecarConnectURL)
	}
	if status.CairnlineSidecarReadURL != projectCoordinationBackendSidecarReadURL {
		t.Fatalf("sidecar read URL = %q, want %q", status.CairnlineSidecarReadURL, projectCoordinationBackendSidecarReadURL)
	}
	if status.CairnlineSidecarDetailURL != projectCoordinationBackendSidecarDetailURL {
		t.Fatalf("sidecar detail URL = %q, want %q", status.CairnlineSidecarDetailURL, projectCoordinationBackendSidecarDetailURL)
	}
	if status.CairnlineSidecarCoordinationURL != projectCoordinationBackendSidecarCoordinationURL {
		t.Fatalf("sidecar coordination URL = %q, want %q", status.CairnlineSidecarCoordinationURL, projectCoordinationBackendSidecarCoordinationURL)
	}
	if status.CairnlineSidecarAssignmentContextURL != projectCoordinationBackendSidecarAssignmentContextURL {
		t.Fatalf("sidecar assignment context URL = %q, want %q", status.CairnlineSidecarAssignmentContextURL, projectCoordinationBackendSidecarAssignmentContextURL)
	}
	if status.CairnlineSidecarLaunchPacketURL != projectCoordinationBackendSidecarLaunchPacketURL {
		t.Fatalf("sidecar launch packet URL = %q, want %q", status.CairnlineSidecarLaunchPacketURL, projectCoordinationBackendSidecarLaunchPacketURL)
	}
	if status.CairnlineSidecarLifecycleURL != projectCoordinationBackendSidecarLifecycleURL {
		t.Fatalf("sidecar lifecycle URL = %q, want %q", status.CairnlineSidecarLifecycleURL, projectCoordinationBackendSidecarLifecycleURL)
	}
	if status.CairnlineSidecarSetupURL != projectCoordinationBackendSidecarSetupURL {
		t.Fatalf("sidecar setup URL = %q, want %q", status.CairnlineSidecarSetupURL, projectCoordinationBackendSidecarSetupURL)
	}
	if status.CairnlineSidecarWriteURL != projectCoordinationBackendSidecarWriteURL {
		t.Fatalf("sidecar write URL = %q, want %q", status.CairnlineSidecarWriteURL, projectCoordinationBackendSidecarWriteURL)
	}
	if status.CairnlineSidecarWorkURL != projectCoordinationBackendSidecarWorkURL {
		t.Fatalf("sidecar work URL = %q, want %q", status.CairnlineSidecarWorkURL, projectCoordinationBackendSidecarWorkURL)
	}
	if status.CairnlineSidecarCollaborationURL != projectCoordinationBackendSidecarCollaborationURL {
		t.Fatalf("sidecar collaboration URL = %q, want %q", status.CairnlineSidecarCollaborationURL, projectCoordinationBackendSidecarCollaborationURL)
	}
	if len(status.ReadRoutes) != 0 {
		t.Fatalf("read routes = %+v, want none in sidecar connector mode", status.ReadRoutes)
	}
	if !containsString(status.WriteAdapterGaps, "projects") || !containsString(status.WriteAdapterGaps, "memory") || !containsString(status.WriteAdapterGaps, "memory-candidates") || !containsString(status.WriteAdapterGaps, "assignment-start") || !containsString(status.WriteAdapterGaps, "migration-cutover") {
		t.Fatalf("write gaps = %+v, want configured write authority ignored until a live sidecar connector exists", status.WriteAdapterGaps)
	}
	if gate := findReplacementGate(status.ReplacementGates, "read-routes"); gate == nil || gate.Ready || gate.Status != "blocked" {
		t.Fatalf("read-routes gate = %+v, want blocked sidecar connector gate", gate)
	}
	if point := findWriteSwitchpoint(status.WriteSwitchpoints, "project-identity"); point == nil || point.CurrentAuthority != "hecate" || !point.BlocksAuthority || point.Gap != "projects" {
		t.Fatalf("project-identity switchpoint = %+v, want Hecate authority while sidecar routing is not live", point)
	}
	warnings := strings.Join(status.Warnings, "\n")
	if !strings.Contains(warnings, "HECATE_PROJECTS_CAIRNLINE_CONNECTOR=sidecar") || !strings.Contains(warnings, "lifecycle/write/setup/work/collaboration/memory/assistant diagnostics") || strings.Contains(warnings, "read-smoke surfaces only") || !strings.Contains(warnings, "Hecate-native stores") {
		t.Fatalf("warnings = %+v, want full sidecar diagnostic warning", status.Warnings)
	}
}

func TestProjectCoordinationBackendStatus_CairnlineSidecarReadRoutesReady(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			Backend:             "sqlite",
			CoordinationBackend: "cairnline",
			CairnlineConnector:  "sidecar",
			CairnlineReadSource: "sidecar",
		},
	}, quietLogger(), nil, nil, nil, nil)

	status := handler.projectCoordinationBackendStatus()
	if status.Status != "cairnline_sidecar_read_routes_ready" {
		t.Fatalf("status = %+v, want sidecar read-route readiness", status)
	}
	if status.CairnlineConnector != "sidecar" || status.CairnlineConnectorReady || status.CairnlineReadSource != "sidecar" {
		t.Fatalf("connector/read source = %q ready=%t source=%q, want sidecar connector with sidecar read source but no full connector readiness", status.CairnlineConnector, status.CairnlineConnectorReady, status.CairnlineReadSource)
	}
	if status.CairnlineAuthoritative || status.ReadModelSwitchReady || status.WriteAdapterReady || status.ReplacementReady {
		t.Fatalf("status = %+v, want sidecar project reads without full replacement readiness", status)
	}
	if handler.projectReadRoutesUseCairnlineReadModel() {
		t.Fatal("sidecar project reads enabled embedded Cairnline read-model routes")
	}
	if !handler.projectCairnlineSidecarReadRoutesEnabled() {
		t.Fatal("projectCairnlineSidecarReadRoutesEnabled() = false, want true")
	}
	if !reflect.DeepEqual(status.ReadRoutes, projectCairnlineSidecarReadRouteNames) {
		t.Fatalf("read routes = %+v, want sidecar read routes", status.ReadRoutes)
	}
	if gate := findReplacementGate(status.ReplacementGates, "read-routes"); gate == nil || gate.Ready || gate.Status != "blocked" {
		t.Fatalf("read-routes gate = %+v, want blocked because only partial read routes are sidecar-backed", gate)
	}
	warnings := strings.Join(status.Warnings, "\n")
	if !strings.Contains(warnings, "project-assistant-context, project-assistant-proposal") || !strings.Contains(warnings, "authoritative write migration") {
		t.Fatalf("warnings = %+v, want sidecar read routes with write-migration warning", status.Warnings)
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
	if !containsString(status.WriteAdapterGaps, "agent-profiles") || !containsString(status.WriteAdapterGaps, "assignments") || !containsString(status.WriteAdapterGaps, "project-assistant-proposals") || !containsString(status.WriteAdapterGaps, "project-assistant-apply-side-effects") || !containsString(status.WriteAdapterGaps, "migration-cutover") {
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
	if point := findWriteSwitchpoint(status.WriteSwitchpoints, "migration-cutover"); point == nil || point.CurrentAuthority != "hecate" || point.CairnlineState != "snapshot_import_rehearsal_available" || point.LiveMirror || !point.BlocksAuthority || point.Gap != "migration-cutover" || !containsString(point.Seams, "sync-rehearsal") {
		t.Fatalf("migration switchpoint = %+v, want snapshot-import rehearsal blocker", point)
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
	if gate := findReplacementGate(status.ReplacementGates, "write-authority-switchpoints"); gate == nil || gate.Ready || gate.Status != "partial" || strings.Contains(gate.Detail, "memory,") || !strings.Contains(gate.Detail, "memory-candidates") {
		t.Fatalf("write-authority gate = %+v, want partial write authority with remaining candidate gap", gate)
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

func TestProjectCoordinationBackendStatus_CairnlineProjectCollaborationAuthorityConfigured(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			Backend:                 "sqlite",
			CoordinationBackend:     "cairnline",
			CairnlineReadSource:     "embedded",
			CairnlineWriteAuthority: "project-collaboration",
		},
	}, quietLogger(), nil, nil, nil, nil)

	status := handler.projectCoordinationBackendStatus()
	if containsString(status.WriteAdapterGaps, "artifacts") || containsString(status.WriteAdapterGaps, "handoffs") {
		t.Fatalf("write gaps = %+v, want artifacts and handoffs removed for opt-in Cairnline authority", status.WriteAdapterGaps)
	}
	for _, name := range []string{"collaboration-artifacts", "handoffs"} {
		point := findWriteSwitchpoint(status.WriteSwitchpoints, name)
		if point == nil || point.CurrentAuthority != "cairnline" || point.CairnlineState != "authoritative_opt_in" || !point.LiveMirror || point.BlocksAuthority || point.Gap != "" {
			t.Fatalf("%s switchpoint = %+v, want opt-in Cairnline authority", name, point)
		}
	}
	if status.ReplacementReady {
		t.Fatalf("replacement_ready = true, want false until remaining write and migration gates are ready")
	}
	if !strings.Contains(strings.Join(status.Warnings, "\n"), "Project collaboration artifact creation and handoff mutations are opt-in Cairnline-authoritative") {
		t.Fatalf("warnings = %+v, want collaboration authority warning", status.Warnings)
	}
}

func TestProjectCoordinationBackendStatus_CairnlineProjectMetadataDefaultsAuthorityConfigured(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			Backend:                 "sqlite",
			CoordinationBackend:     "cairnline",
			CairnlineReadSource:     "embedded",
			CairnlineWriteAuthority: "project-metadata-defaults",
		},
	}, quietLogger(), nil, nil, nil, nil)

	status := handler.projectCoordinationBackendStatus()
	if !containsString(status.WriteAdapterGaps, "projects") {
		t.Fatalf("write gaps = %+v, want projects gap to remain until project identity create/delete authority exists", status.WriteAdapterGaps)
	}
	point := findWriteSwitchpoint(status.WriteSwitchpoints, "project-metadata-defaults")
	if point == nil || point.CurrentAuthority != "cairnline" || point.CairnlineState != "authoritative_opt_in" || !point.LiveMirror || point.BlocksAuthority || point.Gap != "" {
		t.Fatalf("project-metadata-defaults switchpoint = %+v, want opt-in Cairnline authority", point)
	}
	identityPoint := findWriteSwitchpoint(status.WriteSwitchpoints, "project-identity")
	if identityPoint == nil || identityPoint.CurrentAuthority != "hecate" || !identityPoint.BlocksAuthority || identityPoint.Gap != "projects" {
		t.Fatalf("project-identity switchpoint = %+v, want create/delete to remain Hecate-owned and blocking", identityPoint)
	}
	applyPoint := findWriteSwitchpoint(status.WriteSwitchpoints, "project-assistant-apply-side-effects")
	if applyPoint == nil || applyPoint.CurrentAuthority != "mixed" || applyPoint.CairnlineState != "partial_authoritative_via_portable_switchpoints" || !applyPoint.BlocksAuthority || applyPoint.Gap != "project-assistant-apply-side-effects" {
		t.Fatalf("project-assistant-apply-side-effects switchpoint = %+v, want mixed authority through metadata/default switchpoint", applyPoint)
	}
	if status.ReplacementReady {
		t.Fatalf("replacement_ready = true, want false until remaining write and migration gates are ready")
	}
	warnings := strings.Join(status.Warnings, "\n")
	if !strings.Contains(warnings, "Project metadata/default-only update mutations are opt-in Cairnline-authoritative") || !strings.Contains(warnings, "controlled by separate switchpoints") || !strings.Contains(warnings, "chat/runtime") {
		t.Fatalf("warnings = %+v, want metadata/default authority plus separate-switchpoint caveat", status.Warnings)
	}
}

func TestProjectCoordinationBackendStatus_CairnlineProjectIdentityAuthorityConfigured(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			Backend:                 "sqlite",
			CoordinationBackend:     "cairnline",
			CairnlineReadSource:     "embedded",
			CairnlineWriteAuthority: "project-identity",
		},
	}, quietLogger(), nil, nil, nil, nil)

	status := handler.projectCoordinationBackendStatus()
	if !containsString(status.WriteAdapterGaps, "projects") {
		t.Fatalf("write gaps = %+v, want projects gap to remain until metadata/default authority is also enabled", status.WriteAdapterGaps)
	}
	point := findWriteSwitchpoint(status.WriteSwitchpoints, "project-identity")
	if point == nil || point.CurrentAuthority != "cairnline" || point.CairnlineState != "authoritative_opt_in" || !point.LiveMirror || point.BlocksAuthority || point.Gap != "" {
		t.Fatalf("project-identity switchpoint = %+v, want opt-in Cairnline authority", point)
	}
	if status.ReplacementReady {
		t.Fatalf("replacement_ready = true, want false until remaining write and migration gates are ready")
	}
	warnings := strings.Join(status.Warnings, "\n")
	if !strings.Contains(warnings, "Project create/delete mutations are opt-in Cairnline-authoritative") || !strings.Contains(warnings, "delete restores the Cairnline snapshot") {
		t.Fatalf("warnings = %+v, want project identity authority plus rollback caveat", status.Warnings)
	}
}

func TestProjectCoordinationBackendStatus_CairnlineProjectIdentityAndMetadataAuthorityConfigured(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			Backend:                 "sqlite",
			CoordinationBackend:     "cairnline",
			CairnlineReadSource:     "embedded",
			CairnlineWriteAuthority: "project-identity,project-metadata-defaults",
		},
	}, quietLogger(), nil, nil, nil, nil)

	status := handler.projectCoordinationBackendStatus()
	if containsString(status.WriteAdapterGaps, "projects") {
		t.Fatalf("write gaps = %+v, want projects gap removed when identity and metadata/defaults are authoritative", status.WriteAdapterGaps)
	}
	for _, name := range []string{"project-identity", "project-metadata-defaults"} {
		point := findWriteSwitchpoint(status.WriteSwitchpoints, name)
		if point == nil || point.CurrentAuthority != "cairnline" || point.CairnlineState != "authoritative_opt_in" || !point.LiveMirror || point.BlocksAuthority || point.Gap != "" {
			t.Fatalf("%s switchpoint = %+v, want opt-in Cairnline authority", name, point)
		}
	}
}

func TestProjectCoordinationBackendStatus_CairnlineDirectRootSourceAuthorityConfigured(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			Backend:                 "sqlite",
			CoordinationBackend:     "cairnline",
			CairnlineReadSource:     "embedded",
			CairnlineWriteAuthority: "project-roots,project-context-sources",
		},
	}, quietLogger(), nil, nil, nil, nil)

	status := handler.projectCoordinationBackendStatus()
	if !containsString(status.WriteAdapterGaps, "roots") || containsString(status.WriteAdapterGaps, "context-sources") {
		t.Fatalf("write gaps = %+v, want roots to remain blocking and context-sources removed for discovery-result authority", status.WriteAdapterGaps)
	}
	rootPoint := findWriteSwitchpoint(status.WriteSwitchpoints, "roots-and-worktrees")
	if rootPoint == nil || rootPoint.CurrentAuthority != "mixed" || rootPoint.CairnlineState != "partial_authoritative_opt_in" || !rootPoint.LiveMirror || !rootPoint.BlocksAuthority || rootPoint.Gap != "roots" {
		t.Fatalf("roots-and-worktrees switchpoint = %+v, want partial opt-in authority with roots gap still blocking", rootPoint)
	}
	sourcePoint := findWriteSwitchpoint(status.WriteSwitchpoints, "context-sources")
	if sourcePoint == nil || sourcePoint.CurrentAuthority != "cairnline" || sourcePoint.CairnlineState != "authoritative_opt_in" || !sourcePoint.LiveMirror || sourcePoint.BlocksAuthority || sourcePoint.Gap != "" {
		t.Fatalf("context-sources switchpoint = %+v, want opt-in authority including discovery-result replacement", sourcePoint)
	}
	if status.ReplacementReady {
		t.Fatalf("replacement_ready = true, want false until remaining write and migration gates are ready")
	}
	warnings := strings.Join(status.Warnings, "\n")
	if !strings.Contains(warnings, "Project root create/update/delete, root list replacement, discovery-result replacement, and worktree-created root record mutations are opt-in Cairnline-authoritative") || !strings.Contains(warnings, "Hecate still performs root discovery scans and Git worktree creation side effects") {
		t.Fatalf("warnings = %+v, want root authority plus scan/worktree caveat", status.Warnings)
	}
	if !strings.Contains(warnings, "Context-source create/update/delete, list replacement, and discovery-result replacement mutations are opt-in Cairnline-authoritative") || !strings.Contains(warnings, "Hecate still performs the workspace scan") {
		t.Fatalf("warnings = %+v, want context-source authority plus scan caveat", status.Warnings)
	}
}

func TestProjectCoordinationBackendStatus_CairnlineProjectSkillsAuthorityConfigured(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			Backend:                 "sqlite",
			CoordinationBackend:     "cairnline",
			CairnlineReadSource:     "embedded",
			CairnlineWriteAuthority: "project-skills",
		},
	}, quietLogger(), nil, nil, nil, nil)

	status := handler.projectCoordinationBackendStatus()
	if containsString(status.WriteAdapterGaps, "skills") {
		t.Fatalf("write gaps = %+v, want skills removed for opt-in Cairnline authority", status.WriteAdapterGaps)
	}
	point := findWriteSwitchpoint(status.WriteSwitchpoints, "skills")
	if point == nil || point.CurrentAuthority != "cairnline" || point.CairnlineState != "authoritative_opt_in" || !point.LiveMirror || point.BlocksAuthority || point.Gap != "" {
		t.Fatalf("skills switchpoint = %+v, want opt-in Cairnline authority", point)
	}
	if status.ReplacementReady {
		t.Fatalf("replacement_ready = true, want false until remaining write and migration gates are ready")
	}
	if !strings.Contains(strings.Join(status.Warnings, "\n"), "Project skill discovery and metadata updates are opt-in Cairnline-authoritative") {
		t.Fatalf("warnings = %+v, want project-skills authority warning", status.Warnings)
	}
}

func TestProjectCoordinationBackendStatus_CairnlineAgentProfilesAuthorityConfigured(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			Backend:                 "sqlite",
			CoordinationBackend:     "cairnline",
			CairnlineReadSource:     "embedded",
			CairnlineWriteAuthority: "agent-profiles",
		},
	}, quietLogger(), nil, nil, nil, nil)

	status := handler.projectCoordinationBackendStatus()
	if containsString(status.WriteAdapterGaps, "agent-profiles") {
		t.Fatalf("write gaps = %+v, want agent-profiles removed for opt-in Cairnline authority", status.WriteAdapterGaps)
	}
	point := findWriteSwitchpoint(status.WriteSwitchpoints, "agent-profiles")
	if point == nil || point.CurrentAuthority != "cairnline" || point.CairnlineState != "authoritative_opt_in" || !point.LiveMirror || point.BlocksAuthority || point.Gap != "" {
		t.Fatalf("agent-profiles switchpoint = %+v, want opt-in Cairnline authority", point)
	}
	if status.ReplacementReady {
		t.Fatalf("replacement_ready = true, want false until remaining write and migration gates are ready")
	}
	if !strings.Contains(strings.Join(status.Warnings, "\n"), "Agent profile create/update/delete mutations are opt-in Cairnline-authoritative") {
		t.Fatalf("warnings = %+v, want agent-profile authority warning", status.Warnings)
	}
}

func TestProjectCoordinationBackendStatus_CairnlineProjectWorkItemsAuthorityConfigured(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			Backend:                 "sqlite",
			CoordinationBackend:     "cairnline",
			CairnlineReadSource:     "embedded",
			CairnlineWriteAuthority: "project-work-items",
		},
	}, quietLogger(), nil, nil, nil, nil)

	status := handler.projectCoordinationBackendStatus()
	if containsString(status.WriteAdapterGaps, "work-items") {
		t.Fatalf("write gaps = %+v, want work-items removed for opt-in Cairnline authority", status.WriteAdapterGaps)
	}
	point := findWriteSwitchpoint(status.WriteSwitchpoints, "work-items")
	if point == nil || point.CurrentAuthority != "cairnline" || point.CairnlineState != "authoritative_opt_in" || !point.LiveMirror || point.BlocksAuthority || point.Gap != "" {
		t.Fatalf("work-items switchpoint = %+v, want opt-in Cairnline authority", point)
	}
	if status.ReplacementReady {
		t.Fatalf("replacement_ready = true, want false until remaining write and migration gates are ready")
	}
	warnings := strings.Join(status.Warnings, "\n")
	if !strings.Contains(warnings, "Project work-item mutations are opt-in Cairnline-authoritative") || !strings.Contains(warnings, "role mutations still write Hecate-native stores first, then mirror portable role defaults into Cairnline") {
		t.Fatalf("warnings = %+v, want work-item authority plus role mirror warning", status.Warnings)
	}
}

func TestProjectCoordinationBackendStatus_CairnlineProjectRolesAuthorityConfigured(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			Backend:                 "sqlite",
			CoordinationBackend:     "cairnline",
			CairnlineReadSource:     "embedded",
			CairnlineWriteAuthority: "project-roles,project-work-items",
		},
	}, quietLogger(), nil, nil, nil, nil)

	status := handler.projectCoordinationBackendStatus()
	if containsString(status.WriteAdapterGaps, "roles") || containsString(status.WriteAdapterGaps, "work-items") {
		t.Fatalf("write gaps = %+v, want roles and work-items removed for opt-in Cairnline authority", status.WriteAdapterGaps)
	}
	for _, name := range []string{"roles", "work-items"} {
		point := findWriteSwitchpoint(status.WriteSwitchpoints, name)
		if point == nil || point.CurrentAuthority != "cairnline" || point.CairnlineState != "authoritative_opt_in" || !point.LiveMirror || point.BlocksAuthority || point.Gap != "" {
			t.Fatalf("%s switchpoint = %+v, want opt-in Cairnline authority", name, point)
		}
	}
	if status.ReplacementReady {
		t.Fatalf("replacement_ready = true, want false until remaining write and migration gates are ready")
	}
	if !strings.Contains(strings.Join(status.Warnings, "\n"), "Project role and work-item mutations are opt-in Cairnline-authoritative") {
		t.Fatalf("warnings = %+v, want role/work-item authority warning", status.Warnings)
	}
}

func TestProjectCoordinationBackendStatus_CairnlineProjectAssignmentsAuthorityConfigured(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			Backend:                 "sqlite",
			CoordinationBackend:     "cairnline",
			CairnlineReadSource:     "embedded",
			CairnlineWriteAuthority: "project-assignments",
		},
	}, quietLogger(), nil, nil, nil, nil)

	status := handler.projectCoordinationBackendStatus()
	if containsString(status.WriteAdapterGaps, "assignments") {
		t.Fatalf("write gaps = %+v, want assignments removed for opt-in Cairnline authority", status.WriteAdapterGaps)
	}
	if !containsString(status.WriteAdapterGaps, "assignment-start") {
		t.Fatalf("write gaps = %+v, want assignment-start to remain Hecate-owned and blocking", status.WriteAdapterGaps)
	}
	point := findWriteSwitchpoint(status.WriteSwitchpoints, "assignments")
	if point == nil || point.CurrentAuthority != "cairnline" || point.CairnlineState != "authoritative_opt_in" || !point.LiveMirror || point.BlocksAuthority || point.Gap != "" {
		t.Fatalf("assignments switchpoint = %+v, want opt-in Cairnline authority", point)
	}
	startPoint := findWriteSwitchpoint(status.WriteSwitchpoints, "assignment-start-dispatch")
	if startPoint == nil || startPoint.CurrentAuthority != "hecate" || startPoint.CairnlineState != "result_mirror_only" || !startPoint.BlocksAuthority || startPoint.Gap != "assignment-start" {
		t.Fatalf("assignment-start switchpoint = %+v, want Hecate start authority to remain blocking", startPoint)
	}
	if status.ReplacementReady {
		t.Fatalf("replacement_ready = true, want false until remaining write and migration gates are ready")
	}
	warnings := strings.Join(status.Warnings, "\n")
	if !strings.Contains(warnings, "Project assignment create/update/delete record mutations are opt-in Cairnline-authoritative") || !strings.Contains(warnings, "assignment start remains Hecate-owned") {
		t.Fatalf("warnings = %+v, want assignment authority plus Hecate-owned start warning", status.Warnings)
	}
}

func TestProjectCoordinationBackendStatus_CairnlineProjectAssistantProposalAuthorityConfigured(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			Backend:                 "sqlite",
			CoordinationBackend:     "cairnline",
			CairnlineReadSource:     "embedded",
			CairnlineWriteAuthority: "project-assistant-proposals",
		},
	}, quietLogger(), nil, nil, nil, nil)

	status := handler.projectCoordinationBackendStatus()
	if containsString(status.WriteAdapterGaps, "project-assistant-proposals") {
		t.Fatalf("write gaps = %+v, want assistant proposal ledger removed for opt-in Cairnline authority", status.WriteAdapterGaps)
	}
	if !containsString(status.WriteAdapterGaps, "project-assistant-apply-side-effects") {
		t.Fatalf("write gaps = %+v, want assistant apply side effects to remain Hecate-owned and blocking", status.WriteAdapterGaps)
	}
	point := findWriteSwitchpoint(status.WriteSwitchpoints, "project-assistant-proposal-ledger")
	if point == nil || point.CurrentAuthority != "cairnline" || point.CairnlineState != "authoritative_opt_in" || !point.LiveMirror || point.BlocksAuthority || point.Gap != "" {
		t.Fatalf("project-assistant-proposal-ledger switchpoint = %+v, want opt-in Cairnline authority", point)
	}
	applyPoint := findWriteSwitchpoint(status.WriteSwitchpoints, "project-assistant-apply-side-effects")
	if applyPoint == nil || applyPoint.CurrentAuthority != "hecate" || applyPoint.CairnlineState != "side_effect_mirror_only" || !applyPoint.BlocksAuthority || applyPoint.Gap != "project-assistant-apply-side-effects" {
		t.Fatalf("project-assistant-apply-side-effects switchpoint = %+v, want Hecate-owned side-effect mirror blocker", applyPoint)
	}
	if status.ReplacementReady {
		t.Fatalf("replacement_ready = true, want false until remaining write and migration gates are ready")
	}
	warnings := strings.Join(status.Warnings, "\n")
	if !strings.Contains(warnings, "Project Assistant proposal ledger mutations are opt-in Cairnline-authoritative") || !strings.Contains(warnings, "confirmed apply side effects still execute through Hecate-owned project mutation services") {
		t.Fatalf("warnings = %+v, want assistant proposal authority plus side-effect caveat", status.Warnings)
	}
}

func TestProjectCoordinationBackendStatus_CairnlineProjectAssistantApplyPortableAuthorityConfigured(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			Backend:             "sqlite",
			CoordinationBackend: "cairnline",
			CairnlineReadSource: "embedded",
			CairnlineWriteAuthority: strings.Join([]string{
				projectCairnlineWriteAuthorityProjectAssistantProposals,
				projectCairnlineWriteAuthorityProjectMetadataDefaults,
				projectCairnlineWriteAuthorityProjectRoots,
				projectCairnlineWriteAuthorityProjectRoles,
				projectCairnlineWriteAuthorityProjectWorkItems,
				projectCairnlineWriteAuthorityProjectAssignments,
				projectCairnlineWriteAuthorityProjectCollaboration,
				"project-memory",
				"memory-candidates",
			}, ","),
		},
	}, quietLogger(), nil, nil, nil, nil)

	status := handler.projectCoordinationBackendStatus()
	if !containsString(status.WriteAdapterGaps, "project-assistant-apply-side-effects") {
		t.Fatalf("write gaps = %+v, want assistant apply side effects to remain a blocking mixed-authority gap", status.WriteAdapterGaps)
	}
	applyPoint := findWriteSwitchpoint(status.WriteSwitchpoints, "project-assistant-apply-side-effects")
	if applyPoint == nil || applyPoint.CurrentAuthority != "mixed" || applyPoint.CairnlineState != "partial_authoritative_via_portable_switchpoints" || !applyPoint.BlocksAuthority || applyPoint.Gap != "project-assistant-apply-side-effects" {
		t.Fatalf("project-assistant-apply-side-effects switchpoint = %+v, want mixed authority through enabled portable switchpoints", applyPoint)
	}
	for _, name := range []string{"roles", "work-items", "assignments", "artifacts", "handoffs", "memory", "memory-candidates", "project-assistant-proposals"} {
		if containsString(status.WriteAdapterGaps, name) {
			t.Fatalf("write gaps = %+v, did not expect %q when underlying work/proposal switchpoints are enabled", status.WriteAdapterGaps, name)
		}
	}
	warnings := strings.Join(status.Warnings, "\n")
	if !strings.Contains(warnings, "Project Assistant proposal ledger mutations are opt-in Cairnline-authoritative") || !strings.Contains(warnings, "confirmed apply is mixed-authority") || !strings.Contains(warnings, "Project Assistant confirmed apply uses enabled Cairnline authority seams") || !strings.Contains(warnings, "chat/runtime") {
		t.Fatalf("warnings = %+v, want assistant proposal authority and mixed apply authority caveats", status.Warnings)
	}
	if status.ReplacementReady {
		t.Fatalf("replacement_ready = true, want false until remaining write and migration gates are ready")
	}
	if gate := findReplacementGate(status.ReplacementGates, "write-authority-switchpoints"); gate == nil || gate.Ready || gate.Status != "partial" || !strings.Contains(gate.Detail, "project-assistant-apply-side-effects") {
		t.Fatalf("write-authority gate = %+v, want partial write authority with mixed assistant apply gap", gate)
	}
}

func TestProjectCoordinationBackendStatus_WriteAuthorityGateIgnoresMigrationGap(t *testing.T) {
	gate := projectCairnlineWriteAuthorityReplacementGate([]string{"migration-cutover"})
	if !gate.Ready || gate.Status != "ready" || !strings.Contains(gate.Detail, "migration, rollback, and final cutover still have separate gates") {
		t.Fatalf("write-authority gate = %+v, want ready when only migration gate remains", gate)
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
	migrationGate := findReplacementGate(response.Data.ReplacementGates, "migration-and-rollback")
	if migrationGate == nil || migrationGate.Status != "rehearsal_available" {
		t.Fatalf("response migration gate = %+v, want rehearsal-available blocker", migrationGate)
	}
	migrationSwitchpoint := findWriteSwitchpoint(response.Data.WriteSwitchpoints, "migration-cutover")
	if migrationSwitchpoint == nil || migrationSwitchpoint.CairnlineState != "snapshot_import_rehearsal_available" || !containsString(migrationSwitchpoint.Seams, "sync-rehearsal") {
		t.Fatalf("response migration switchpoint = %+v, want snapshot-import rehearsal blocker", migrationSwitchpoint)
	}
	if findReplacementGate(response.Data.ReplacementGates, "write-authority-switchpoints") == nil || findWriteSwitchpoint(response.Data.WriteSwitchpoints, "project-assistant-proposal-ledger") == nil || findWriteSwitchpoint(response.Data.WriteSwitchpoints, "project-assistant-apply-side-effects") == nil {
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
