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

	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
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
	if status.ReplacementTarget != "embedded_cairnline_first" || !strings.Contains(status.ReplacementTargetDetail, "embedded Cairnline") || !strings.Contains(status.ReplacementTargetDetail, "sidecar") {
		t.Fatalf("replacement target/detail = %q/%q, want embedded-first target with sidecar boundary detail", status.ReplacementTarget, status.ReplacementTargetDetail)
	}
	if status.ReplacementMode != "disabled" || status.ReplacementModeArmed || !strings.Contains(status.ReplacementModeDetail, "disabled") {
		t.Fatalf("replacement mode/detail = %q/%v/%q, want disabled mode", status.ReplacementMode, status.ReplacementModeArmed, status.ReplacementModeDetail)
	}
	if len(status.ReadRoutes) != 0 || len(status.WriteAdapterSeams) != 0 || len(status.WriteAdapterGaps) != 0 || len(status.PortableWriteGaps) != 0 || len(status.OrchestratorCapabilities) != 0 || len(status.SideEffectBlockers) != 0 || len(status.MigrationBlockers) != 0 || len(status.ReplacementGates) != 0 || len(status.WriteSwitchpoints) != 0 {
		t.Fatalf("status = %+v, want no Cairnline route/seam/gap lists until Cairnline is configured", status)
	}
	if status.NextReplacementAction == nil || status.NextReplacementAction.ID != "enable-cairnline-dogfood" || status.NextReplacementAction.Target != "configuration" {
		t.Fatalf("next action = %+v, want enable-cairnline-dogfood configuration action", status.NextReplacementAction)
	}
	if hint := findConfigHint(status.NextReplacementAction.ConfigHints, "HECATE_PROJECTS_COORDINATION_BACKEND"); hint == nil || hint.Value != "cairnline" {
		t.Fatalf("next action config hints = %+v, want coordination backend=cairnline", status.NextReplacementAction.ConfigHints)
	}
	if !hasEmbeddedDogfoodProbes(status.NextReplacementAction.Probes) {
		t.Fatalf("next action probes = %+v, want embedded dogfood status/read-model probes", status.NextReplacementAction.Probes)
	}
}

func TestProjectCoordinationBackendStatus_EnableDogfoodHintsEmbeddedReadSourceFromSidecar(t *testing.T) {
	handler := &Handler{config: config.Config{
		Projects: config.ProjectsConfig{
			Backend:             "sqlite",
			CairnlineConnector:  "sidecar",
			CairnlineReadSource: "sidecar",
		},
	}}

	status := handler.projectCoordinationBackendStatus()
	if status.ConfiguredBackend != "hecate" || status.NextReplacementAction == nil || status.NextReplacementAction.ID != "enable-cairnline-dogfood" {
		t.Fatalf("status = %+v, want Hecate-authoritative enable-dogfood next action", status)
	}
	if hint := findConfigHint(status.NextReplacementAction.ConfigHints, "HECATE_PROJECTS_COORDINATION_BACKEND"); hint == nil || hint.Value != "cairnline" {
		t.Fatalf("next action config hints = %+v, want coordination backend=cairnline", status.NextReplacementAction.ConfigHints)
	}
	if hint := findConfigHint(status.NextReplacementAction.ConfigHints, "HECATE_PROJECTS_CAIRNLINE_CONNECTOR"); hint == nil || hint.Value != "embedded" {
		t.Fatalf("next action config hints = %+v, want embedded connector hint", status.NextReplacementAction.ConfigHints)
	}
	if hint := findConfigHint(status.NextReplacementAction.ConfigHints, "HECATE_PROJECTS_CAIRNLINE_READ_SOURCE"); hint == nil || hint.Value != "embedded" {
		t.Fatalf("next action config hints = %+v, want embedded read-source hint", status.NextReplacementAction.ConfigHints)
	}
	if !hasEmbeddedDogfoodProbes(status.NextReplacementAction.Probes) {
		t.Fatalf("next action probes = %+v, want embedded dogfood status/read-model probes", status.NextReplacementAction.Probes)
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
	if gate := findReplacementGate(status.ReplacementGates, "migration-and-rollback"); gate == nil || gate.Ready || gate.Status != "waiting_for_read_smoke" {
		t.Fatalf("migration gate = %+v, want read-smoke waiting blocker", gate)
	}
	if point := findWriteSwitchpoint(status.WriteSwitchpoints, "assignment-start-dispatch"); point == nil || point.CurrentAuthority != "hecate" || point.CairnlineState != "result_mirror_only" || !point.LiveMirror || point.BlocksAuthority || point.Gap != "assignment-start" {
		t.Fatalf("assignment-start switchpoint = %+v, want Hecate-owned result mirror capability outside portable write authority", point)
	}
	if status.NextReplacementAction == nil || status.NextReplacementAction.ID != "complete-read-model-sources" || status.NextReplacementAction.Target != "read-routes" || !containsString(status.NextReplacementAction.ProbeURLs, projectCoordinationBackendEmbeddedParityReportURL) {
		t.Fatalf("next action = %+v, want read-model source action with embedded parity probe", status.NextReplacementAction)
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
	if !strings.Contains(status.CairnlineConnectorDetail, "project-assistant-context, project-assistant-proposal") {
		t.Fatalf("connector detail = %q, want canonical sidecar read-route list", status.CairnlineConnectorDetail)
	}
	if !strings.Contains(strings.Join(status.Warnings, "\n"), "project-assistant-context, project-assistant-proposal") {
		t.Fatalf("warnings = %+v, want sidecar connector warning to include canonical read-route list", status.Warnings)
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
	if status.NextReplacementAction == nil || status.NextReplacementAction.ID != "use-embedded-cairnline-connector" || status.NextReplacementAction.Target != "connector" {
		t.Fatalf("next action = %+v, want embedded connector action for sidecar mode", status.NextReplacementAction)
	}
	if hint := findConfigHint(status.NextReplacementAction.ConfigHints, "HECATE_PROJECTS_CAIRNLINE_CONNECTOR"); hint == nil || hint.Value != "embedded" {
		t.Fatalf("next action config hints = %+v, want embedded connector hint", status.NextReplacementAction.ConfigHints)
	}
	if hint := findConfigHint(status.NextReplacementAction.ConfigHints, "HECATE_PROJECTS_CAIRNLINE_READ_SOURCE"); hint != nil {
		t.Fatalf("next action config hints = %+v, did not expect read-source hint when source is already embedded", status.NextReplacementAction.ConfigHints)
	}
	if !hasEmbeddedDogfoodProbes(status.NextReplacementAction.Probes) {
		t.Fatalf("next action probes = %+v, want embedded connector status/read-model probes", status.NextReplacementAction.Probes)
	}
	warnings := strings.Join(status.Warnings, "\n")
	if !strings.Contains(warnings, "HECATE_PROJECTS_CAIRNLINE_CONNECTOR=sidecar") || !strings.Contains(warnings, "lifecycle/write/setup/work/collaboration/memory/assistant diagnostics") || strings.Contains(warnings, "read-smoke surfaces only") || !strings.Contains(warnings, "ignored in sidecar connector mode") || !strings.Contains(warnings, projectCairnlineWriteAuthorityProjectAssignments) {
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
			CairnlineWriteAuthority: strings.Join([]string{
				projectCairnlineWriteAuthorityProjectIdentity,
				projectCairnlineWriteAuthorityProjectMetadataDefaults,
				projectCairnlineWriteAuthorityProjectAssignments,
			}, ","),
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
	if len(status.PortableWriteGaps) == 0 || !containsString(status.PortableWriteGaps, "projects") || !containsString(status.PortableWriteGaps, "assignments") {
		t.Fatalf("portable write gaps = %+v, want sidecar mode to ignore configured write authority", status.PortableWriteGaps)
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
	if !containsString(status.ReadRoutes, "project-chat-prelude") || !containsString(status.ReadRoutes, "project-chat-context") {
		t.Fatalf("sidecar read routes = %+v, want project chat prelude/context included", status.ReadRoutes)
	}
	if gate := findReplacementGate(status.ReplacementGates, "read-routes"); gate == nil || gate.Ready || gate.Status != "blocked" {
		t.Fatalf("read-routes gate = %+v, want blocked while sidecar reads remain an interoperability mode", gate)
	}
	warnings := strings.Join(status.Warnings, "\n")
	if !strings.Contains(warnings, "project-assistant-context, project-assistant-proposal") || !strings.Contains(warnings, "project-chat-prelude, project-chat-context") || !strings.Contains(warnings, "ignored in sidecar connector mode") || !strings.Contains(warnings, projectCairnlineWriteAuthorityProjectIdentity) || !strings.Contains(warnings, "authoritative write migration") {
		t.Fatalf("warnings = %+v, want sidecar read routes with write-migration warning", status.Warnings)
	}
	if status.NextReplacementAction == nil || status.NextReplacementAction.ID != "use-embedded-cairnline-connector" || status.NextReplacementAction.Target != "connector" {
		t.Fatalf("next action = %+v, want embedded connector action even when sidecar read routes are active", status.NextReplacementAction)
	}
	if hint := findConfigHint(status.NextReplacementAction.ConfigHints, "HECATE_PROJECTS_CAIRNLINE_CONNECTOR"); hint == nil || hint.Value != "embedded" {
		t.Fatalf("next action config hints = %+v, want embedded connector hint", status.NextReplacementAction.ConfigHints)
	}
	if hint := findConfigHint(status.NextReplacementAction.ConfigHints, "HECATE_PROJECTS_CAIRNLINE_READ_SOURCE"); hint == nil || hint.Value != "embedded" {
		t.Fatalf("next action config hints = %+v, want embedded read-source hint before embedded dogfood probes", status.NextReplacementAction.ConfigHints)
	}
	if !hasEmbeddedDogfoodProbes(status.NextReplacementAction.Probes) {
		t.Fatalf("next action probes = %+v, want embedded connector status/read-model probes", status.NextReplacementAction.Probes)
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
	if !reflect.DeepEqual(status.ReadRoutes, projectCairnlineReadRouteNames) {
		t.Fatalf("read routes = %+v, want canonical Cairnline read routes %+v", status.ReadRoutes, projectCairnlineReadRouteNames)
	}
	if !containsString(status.WriteAdapterGaps, "agent-profiles") || !containsString(status.WriteAdapterGaps, "assignments") || !containsString(status.WriteAdapterGaps, "project-assistant-proposals") || !containsString(status.WriteAdapterGaps, "project-assistant-apply-side-effects") || !containsString(status.WriteAdapterGaps, "migration-cutover") {
		t.Fatalf("write gaps = %+v, want structured remaining write-adapter gaps", status.WriteAdapterGaps)
	}
	if !containsString(status.PortableWriteGaps, "projects") || !containsString(status.PortableWriteGaps, "roots") || !containsString(status.PortableWriteGaps, "agent-profiles") || containsString(status.PortableWriteGaps, "assignment-start") || containsString(status.PortableWriteGaps, "project-assistant-apply-side-effects") || containsString(status.PortableWriteGaps, "migration-cutover") {
		t.Fatalf("portable write gaps = %+v, want portable gaps separated from side-effect and migration blockers", status.PortableWriteGaps)
	}
	if !reflect.DeepEqual(status.OrchestratorCapabilities, []string{"roots", "assignment-start", "project-assistant-apply-side-effects"}) {
		t.Fatalf("orchestrator capabilities = %+v, want root/worktree, dispatch, and assistant apply capabilities", status.OrchestratorCapabilities)
	}
	if !reflect.DeepEqual(status.MigrationBlockers, []string{"migration-cutover"}) {
		t.Fatalf("migration blockers = %+v, want migration cutover blocker", status.MigrationBlockers)
	}
	if !containsString(status.WriteAdapterSeams, "project-identity-live-mirror") || !containsString(status.WriteAdapterSeams, "project-roots-live-mirror") || !containsString(status.WriteAdapterSeams, "project-context-sources-live-mirror") || !containsString(status.WriteAdapterSeams, "project-defaults-live-mirror") || !containsString(status.WriteAdapterSeams, "agent-profiles-live-mirror") || !containsString(status.WriteAdapterSeams, "project-skills-live-mirror") || !containsString(status.WriteAdapterSeams, "project-roles-live-mirror") || !containsString(status.WriteAdapterSeams, "project-work-items-live-mirror") || !containsString(status.WriteAdapterSeams, "project-assignments-live-mirror") || !containsString(status.WriteAdapterSeams, "project-assignment-start-result-live-mirror") || !containsString(status.WriteAdapterSeams, "project-assignment-chat-reconcile-live-mirror") || !containsString(status.WriteAdapterSeams, "project-collaboration-live-mirror") || !containsString(status.WriteAdapterSeams, "project-handoffs-live-mirror") || !containsString(status.WriteAdapterSeams, "project-memory-live-mirror") || !containsString(status.WriteAdapterSeams, "project-memory-candidates-live-mirror") || !containsString(status.WriteAdapterSeams, "project-assistant-proposal-ledger-live-mirror") || !containsString(status.WriteAdapterSeams, "project-assistant-apply-side-effects-live-mirror") || !containsString(status.WriteAdapterSeams, "assignment-status") || !containsString(status.WriteAdapterSeams, "project-assistant-proposal-ledger-import") || !containsString(status.WriteAdapterSeams, "memory-candidates") {
		t.Fatalf("write seams = %+v, want structured non-authoritative write-adapter seam coverage", status.WriteAdapterSeams)
	}
	if gate := findReplacementGate(status.ReplacementGates, "read-routes"); gate == nil || !gate.Ready || gate.Status != "ready" {
		t.Fatalf("read-routes gate = %+v, want ready gate", gate)
	} else if !containsString(gate.ProbeURLs, projectCoordinationBackendReadinessURL) || !containsString(gate.ProbeURLs, projectCoordinationBackendEmbeddedReadModelURL) || !containsString(gate.ProbeURLs, projectCoordinationBackendEmbeddedParityReportURL) {
		t.Fatalf("read-routes gate probe URLs = %+v, want read-model and embedded parity probes", gate.ProbeURLs)
	}
	if gate := findReplacementGate(status.ReplacementGates, "strict-embedded-read-smoke"); gate == nil || gate.Ready || gate.Status != "operator_probe_required" {
		t.Fatalf("strict embedded gate = %+v, want operator probe gate", gate)
	} else if !containsString(gate.ProbeURLs, projectCoordinationBackendSyncReadinessURL) || !containsString(gate.ProbeURLs, projectCoordinationBackendMirrorParityURL) {
		t.Fatalf("strict embedded gate probe URLs = %+v, want sync and mirror parity probes", gate.ProbeURLs)
	}
	if gate := findReplacementGate(status.ReplacementGates, "embedded-replacement-mode"); gate == nil || gate.Ready || gate.Status != "disabled" || !strings.Contains(gate.Detail, "HECATE_PROJECTS_CAIRNLINE_REPLACEMENT_MODE=embedded") {
		t.Fatalf("embedded replacement mode gate = %+v, want disabled gate with config hint detail", gate)
	}
	if point := findWriteSwitchpoint(status.WriteSwitchpoints, "project-memory"); point == nil || point.CurrentAuthority != "hecate" || point.CairnlineState != "live_mirror_non_authoritative" || !point.LiveMirror || !point.BlocksAuthority || point.Gap != "memory" {
		t.Fatalf("project-memory switchpoint = %+v, want Hecate-owned live mirror blocker", point)
	}
	if point := findWriteSwitchpoint(status.WriteSwitchpoints, "memory-candidates"); point == nil || !strings.Contains(point.Detail, "create/promote/reject still commits") || strings.Contains(point.Detail, "delete") {
		t.Fatalf("memory-candidates switchpoint = %+v, want live Hecate API surface limited to create/promote/reject", point)
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
	if status.NextReplacementAction == nil || status.NextReplacementAction.ID != "move-portable-write-authority" || status.NextReplacementAction.Target != "projects" {
		t.Fatalf("next action = %+v, want first portable write authority gap", status.NextReplacementAction)
	}
	if hint := findConfigHint(status.NextReplacementAction.ConfigHints, "HECATE_PROJECTS_CAIRNLINE_WRITE_AUTHORITY"); hint == nil || hint.Value != "project-identity,project-metadata-defaults" {
		t.Fatalf("next action config hints = %+v, want project identity + metadata defaults write authority", status.NextReplacementAction.ConfigHints)
	}
}

func TestProjectCoordinationBackendStatus_StrictEmbeddedReadSmokeGateMissingMirror(t *testing.T) {
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			Backend:             "sqlite",
			CoordinationBackend: "cairnline",
			CairnlineReadSource: "embedded",
		},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetProjectWorkStore(projectwork.NewMemoryStore())
	handler.SetProjectSkillStore(projectskills.NewMemoryStore())
	handler.SetMemoryStore(memory.NewMemoryStore())
	handler.SetAgentProfileStore(agentprofiles.NewMemoryStore())

	status := handler.projectCoordinationBackendStatusWithContext(t.Context())
	gate := findReplacementGate(status.ReplacementGates, "strict-embedded-read-smoke")
	if gate == nil || gate.Ready || gate.Status != "not_run" {
		t.Fatalf("strict embedded gate = %+v, want missing-mirror not_run gate", gate)
	}
	if status.MigrationRehearsal == nil || status.MigrationRehearsal.Operation != "mirror_parity" || status.MigrationRehearsal.Status != "not_run" || status.MigrationRehearsal.CutoverReady {
		t.Fatalf("migration rehearsal = %+v, want not-run mirror parity evidence while mirror is missing", status.MigrationRehearsal)
	}
	if !containsString(gate.ProbeURLs, projectCoordinationBackendSyncReadinessURL) || !strings.Contains(gate.Detail, projectCoordinationBackendSyncReadinessURL) {
		t.Fatalf("strict embedded gate = %+v, want sync probe guidance", gate)
	}
	if status.ReplacementReady {
		t.Fatalf("replacement_ready = true, want false while strict embedded smoke is not run")
	}
}

func TestProjectCoordinationBackendStatus_StrictEmbeddedReadSmokeGateVerifiedAfterSync(t *testing.T) {
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			Backend:                 "sqlite",
			CoordinationBackend:     "cairnline",
			CairnlineConnector:      "embedded",
			CairnlineReadSource:     "embedded",
			CairnlineWriteAuthority: "all-portable",
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
		"name": "Backend Status Strict Embedded",
	}))
	mustRequestJSONStatus[ProjectCairnlineSyncResponse](client, http.StatusOK, http.MethodPost, "/hecate/v1/projects/cairnline/sync", "")

	status := handler.projectCoordinationBackendStatusWithContext(t.Context())
	gate := findReplacementGate(status.ReplacementGates, "strict-embedded-read-smoke")
	if gate == nil || !gate.Ready || gate.Status != "verified" || !strings.Contains(gate.Detail, "strict embedded read smoke passed") {
		t.Fatalf("strict embedded gate = %+v, want verified strict embedded smoke gate", gate)
	}
	if status.MigrationRehearsal == nil || status.MigrationRehearsal.Operation != "mirror_parity" || status.MigrationRehearsal.Status != "verified" || !projectCairnlineMigrationRollbackEvidenceReady(status.MigrationRehearsal) {
		t.Fatalf("migration rehearsal = %+v, want backend status to expose verified mirror-parity rollback evidence", status.MigrationRehearsal)
	}
	if status.ReplacementReady {
		t.Fatalf("replacement_ready = true, want false while migration/cutover gate remains blocked")
	}
	if migrationGate := findReplacementGate(status.ReplacementGates, "migration-and-rollback"); migrationGate == nil || migrationGate.Ready || migrationGate.Status != "cutover_switch_missing" {
		t.Fatalf("migration gate = %+v, want cutover-switch blocker after strict embedded verification", migrationGate)
	} else if !strings.Contains(migrationGate.Detail, "rollback notes") {
		t.Fatalf("migration gate = %+v, want rollback evidence detail", migrationGate)
	}
	if status.NextReplacementAction == nil || status.NextReplacementAction.ID != "implement-migration-cutover" {
		t.Fatalf("next action = %+v, want cutover implementation after read/write gates are ready", status.NextReplacementAction)
	}
	if hint := findConfigHint(status.NextReplacementAction.ConfigHints, "HECATE_PROJECTS_CAIRNLINE_REPLACEMENT_MODE"); hint == nil || hint.Value != "embedded" {
		t.Fatalf("next action config hints = %+v, want embedded replacement mode hint", status.NextReplacementAction.ConfigHints)
	}
}

func TestProjectCoordinationBackendStatus_EmbeddedReplacementModeReportsCairnlineAuthoritativeAfterVerifiedCutover(t *testing.T) {
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			Backend:                  "sqlite",
			CoordinationBackend:      "cairnline",
			CairnlineConnector:       "embedded",
			CairnlineReadSource:      "embedded",
			CairnlineWriteAuthority:  "all-portable",
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
		"name": "Backend Status Embedded Replacement",
	}))
	mustRequestJSONStatus[ProjectCairnlineSyncResponse](client, http.StatusOK, http.MethodPost, "/hecate/v1/projects/cairnline/sync", "")

	status := handler.projectCoordinationBackendStatusWithContext(t.Context())
	if !status.ReplacementReady || status.AuthoritativeBackend != "cairnline" || !status.CairnlineAuthoritative {
		t.Fatalf("status = %+v, want verified embedded replacement to report Cairnline authoritative", status)
	}
	if status.Status != "cairnline_authoritative" || !strings.Contains(status.Detail, "Cairnline as authoritative") {
		t.Fatalf("status/detail = %q/%q, want authoritative Cairnline replacement wording", status.Status, status.Detail)
	}
	for _, warning := range status.Warnings {
		if strings.Contains(warning, "Hecate stores remain authoritative") || strings.Contains(warning, "Other project mutation routes still write only Hecate-native stores") {
			t.Fatalf("warning = %q, want replacement-ready runtime-boundary warning without stale Hecate-authoritative wording", warning)
		}
	}
	if len(status.Warnings) == 0 || !strings.Contains(strings.Join(status.Warnings, "\n"), "Hecate still owns runtime/workspace side effects") {
		t.Fatalf("warnings = %+v, want runtime/workspace side-effect boundary after replacement is ready", status.Warnings)
	}
	if point := findWriteSwitchpoint(status.WriteSwitchpoints, "skills"); point == nil || !strings.Contains(point.Detail, "skip native project-skill compatibility rows") {
		t.Fatalf("skills switchpoint = %+v, want armed replacement-mode no-native-skill-shadow detail", point)
	}
	for _, tc := range []struct {
		name string
		want string
	}{
		{name: "project-identity", want: "skips native project identity compatibility rows"},
		{name: "roles", want: "skip native project-work role compatibility rows"},
		{name: "work-items", want: "skip native project-work item compatibility rows"},
		{name: "assignments", want: "skip native project-work assignment compatibility rows"},
		{name: "collaboration-artifacts", want: "skips native project-work artifact compatibility rows"},
		{name: "handoffs", want: "skip native project-work handoff compatibility rows"},
		{name: "project-assistant-proposal-ledger", want: "skip native proposal ledger compatibility rows"},
	} {
		point := findWriteSwitchpoint(status.WriteSwitchpoints, tc.name)
		if point == nil || !strings.Contains(point.Detail, tc.want) {
			t.Fatalf("%s switchpoint = %+v, want detail containing %q", tc.name, point, tc.want)
		}
	}
	if len(status.MigrationBlockers) != 0 {
		t.Fatalf("migration blockers = %+v, want none after embedded replacement cutover is armed", status.MigrationBlockers)
	}
	if containsString(status.WriteAdapterGaps, "migration-cutover") {
		t.Fatalf("write adapter gaps = %+v, want migration-cutover cleared after embedded replacement cutover is armed", status.WriteAdapterGaps)
	}
	if !containsString(status.WriteAdapterGaps, "assignment-start") || !containsString(status.OrchestratorCapabilities, "assignment-start") {
		t.Fatalf("write adapter gaps = %+v orchestrator = %+v, want Hecate-owned runtime capability still reported after cutover", status.WriteAdapterGaps, status.OrchestratorCapabilities)
	}
	if status.MigrationRehearsal == nil || !projectCairnlineMigrationRollbackEvidenceReady(status.MigrationRehearsal) || len(status.MigrationRehearsal.Rollback) == 0 {
		t.Fatalf("migration rehearsal = %+v, want replacement-ready status to expose verified rollback evidence", status.MigrationRehearsal)
	}
	if gate := findReplacementGate(status.ReplacementGates, "migration-and-rollback"); gate == nil || !gate.Ready || gate.Status != "ready" {
		t.Fatalf("migration gate = %+v, want ready migration gate after embedded replacement cutover is armed", gate)
	}
	if point := findWriteSwitchpoint(status.WriteSwitchpoints, "migration-cutover"); point == nil || point.CurrentAuthority != "cairnline" || point.CairnlineState != "embedded_cutover_armed" || point.BlocksAuthority || point.Gap != "" {
		t.Fatalf("migration cutover switchpoint = %+v, want armed Cairnline cutover switchpoint after replacement is ready", point)
	}
	if status.NextReplacementAction == nil || status.NextReplacementAction.ID != "monitor-cairnline-backend" {
		t.Fatalf("next action = %+v, want monitor action after replacement is ready", status.NextReplacementAction)
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
	if applyPoint == nil || applyPoint.CurrentAuthority != "mixed" || applyPoint.CairnlineState != "partial_authoritative_via_portable_switchpoints" || applyPoint.BlocksAuthority || applyPoint.Gap != "project-assistant-apply-side-effects" {
		t.Fatalf("project-assistant-apply-side-effects switchpoint = %+v, want mixed orchestrator capability through metadata/default switchpoint", applyPoint)
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
	applyPoint := findWriteSwitchpoint(status.WriteSwitchpoints, "project-assistant-apply-side-effects")
	if applyPoint == nil || applyPoint.CurrentAuthority != "mixed" || applyPoint.CairnlineState != "partial_authoritative_via_portable_switchpoints" || applyPoint.BlocksAuthority || applyPoint.Gap != "project-assistant-apply-side-effects" {
		t.Fatalf("project-assistant-apply-side-effects switchpoint = %+v, want mixed orchestrator capability through project identity switchpoint", applyPoint)
	}
	if status.ReplacementReady {
		t.Fatalf("replacement_ready = true, want false until remaining write and migration gates are ready")
	}
	warnings := strings.Join(status.Warnings, "\n")
	if !strings.Contains(warnings, "Project create/delete mutations are opt-in Cairnline-authoritative") || !strings.Contains(warnings, "delete restores the Cairnline snapshot") || !strings.Contains(warnings, "chat/runtime") {
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
	if containsString(status.PortableWriteGaps, "roots") || containsString(status.PortableWriteGaps, "context-sources") {
		t.Fatalf("portable write gaps = %+v, want root/source portable switchpoint gaps removed", status.PortableWriteGaps)
	}
	if !containsString(status.OrchestratorCapabilities, "roots") {
		t.Fatalf("orchestrator capabilities = %+v, want root scan/worktree capability to remain", status.OrchestratorCapabilities)
	}
	rootPoint := findWriteSwitchpoint(status.WriteSwitchpoints, "roots-and-worktrees")
	if rootPoint == nil || rootPoint.CurrentAuthority != "mixed" || rootPoint.CairnlineState != "partial_authoritative_opt_in" || !rootPoint.LiveMirror || rootPoint.BlocksAuthority || rootPoint.Gap != "roots" {
		t.Fatalf("roots-and-worktrees switchpoint = %+v, want partial opt-in authority with root orchestration outside portable write authority", rootPoint)
	}
	if !strings.Contains(rootPoint.Detail, "resolve project identity and roots from the embedded Cairnline graph") {
		t.Fatalf("roots-and-worktrees detail = %q, want Cairnline-owned graph note", rootPoint.Detail)
	}
	sourcePoint := findWriteSwitchpoint(status.WriteSwitchpoints, "context-sources")
	if sourcePoint == nil || sourcePoint.CurrentAuthority != "cairnline" || sourcePoint.CairnlineState != "authoritative_opt_in" || !sourcePoint.LiveMirror || sourcePoint.BlocksAuthority || sourcePoint.Gap != "" {
		t.Fatalf("context-sources switchpoint = %+v, want opt-in authority including discovery-result replacement", sourcePoint)
	}
	if !strings.Contains(sourcePoint.Detail, "resolve project identity, roots, and existing sources from the embedded Cairnline graph") {
		t.Fatalf("context-sources detail = %q, want Cairnline-owned graph note", sourcePoint.Detail)
	}
	if status.ReplacementReady {
		t.Fatalf("replacement_ready = true, want false until remaining write and migration gates are ready")
	}
	warnings := strings.Join(status.Warnings, "\n")
	if !strings.Contains(warnings, "Project root create/update/delete, root list replacement, discovery-result replacement, and worktree-created root record mutations are opt-in Cairnline-authoritative") || !strings.Contains(warnings, "resolve project identity and roots from the embedded Cairnline graph") || !strings.Contains(warnings, "Hecate still performs root discovery scans and Git worktree creation side effects") {
		t.Fatalf("warnings = %+v, want root authority plus scan/worktree caveat", status.Warnings)
	}
	if !strings.Contains(warnings, "Context-source create/update/delete, list replacement, and discovery-result replacement mutations are opt-in Cairnline-authoritative") || !strings.Contains(warnings, "resolve project identity, roots, and existing sources from the embedded Cairnline graph") || !strings.Contains(warnings, "Hecate still performs the workspace scan") {
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
	if !strings.Contains(strings.Join(status.Warnings, "\n"), "Agent Preset create/update/delete mutations are opt-in Cairnline-authoritative") {
		t.Fatalf("warnings = %+v, want agent-preset authority warning", status.Warnings)
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
		t.Fatalf("write gaps = %+v, want assignment-start to remain visible as a Hecate-owned orchestrator capability", status.WriteAdapterGaps)
	}
	point := findWriteSwitchpoint(status.WriteSwitchpoints, "assignments")
	if point == nil || point.CurrentAuthority != "cairnline" || point.CairnlineState != "authoritative_opt_in" || !point.LiveMirror || point.BlocksAuthority || point.Gap != "" {
		t.Fatalf("assignments switchpoint = %+v, want opt-in Cairnline authority", point)
	}
	startPoint := findWriteSwitchpoint(status.WriteSwitchpoints, "assignment-start-dispatch")
	if startPoint == nil || startPoint.CurrentAuthority != "hecate" || startPoint.CairnlineState != "result_mirror_only" || startPoint.BlocksAuthority || startPoint.Gap != "assignment-start" {
		t.Fatalf("assignment-start switchpoint = %+v, want Hecate start authority outside portable write authority", startPoint)
	}
	if status.ReplacementReady {
		t.Fatalf("replacement_ready = true, want false until remaining write and migration gates are ready")
	}
	warnings := strings.Join(status.Warnings, "\n")
	if !strings.Contains(warnings, "Project assignment create/update/delete record mutations are opt-in Cairnline-authoritative") ||
		!strings.Contains(warnings, "assignment start claims the Cairnline coordination record before Hecate-owned dispatch") ||
		!strings.Contains(warnings, "releases that claim on pre-runtime setup failure") {
		t.Fatalf("warnings = %+v, want assignment authority plus Cairnline claim/Hecate-owned dispatch warning", status.Warnings)
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
		t.Fatalf("write gaps = %+v, want assistant apply side effects to remain visible as a Hecate-owned orchestrator capability", status.WriteAdapterGaps)
	}
	point := findWriteSwitchpoint(status.WriteSwitchpoints, "project-assistant-proposal-ledger")
	if point == nil || point.CurrentAuthority != "cairnline" || point.CairnlineState != "authoritative_opt_in" || !point.LiveMirror || point.BlocksAuthority || point.Gap != "" {
		t.Fatalf("project-assistant-proposal-ledger switchpoint = %+v, want opt-in Cairnline authority", point)
	}
	applyPoint := findWriteSwitchpoint(status.WriteSwitchpoints, "project-assistant-apply-side-effects")
	if applyPoint == nil || applyPoint.CurrentAuthority != "hecate" || applyPoint.CairnlineState != "side_effect_mirror_only" || applyPoint.BlocksAuthority || applyPoint.Gap != "project-assistant-apply-side-effects" {
		t.Fatalf("project-assistant-apply-side-effects switchpoint = %+v, want Hecate-owned side-effect mirror capability outside portable write authority", applyPoint)
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
				projectCairnlineWriteAuthorityProjectIdentity,
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
		t.Fatalf("write gaps = %+v, want assistant apply side effects to remain visible as a Hecate-owned capability", status.WriteAdapterGaps)
	}
	applyPoint := findWriteSwitchpoint(status.WriteSwitchpoints, "project-assistant-apply-side-effects")
	if applyPoint == nil || applyPoint.CurrentAuthority != "mixed" || applyPoint.CairnlineState != "partial_authoritative_via_portable_switchpoints" || applyPoint.BlocksAuthority || applyPoint.Gap != "project-assistant-apply-side-effects" {
		t.Fatalf("project-assistant-apply-side-effects switchpoint = %+v, want mixed orchestrator capability through enabled portable switchpoints", applyPoint)
	}
	for _, name := range []string{"roles", "work-items", "assignments", "artifacts", "handoffs", "memory", "memory-candidates", "project-assistant-proposals"} {
		if containsString(status.WriteAdapterGaps, name) {
			t.Fatalf("write gaps = %+v, did not expect %q when underlying work/proposal switchpoints are enabled", status.WriteAdapterGaps, name)
		}
	}
	warnings := strings.Join(status.Warnings, "\n")
	if !strings.Contains(warnings, "Project Assistant proposal ledger mutations are opt-in Cairnline-authoritative") || !strings.Contains(warnings, "confirmed apply is mixed-authority") || !strings.Contains(warnings, "Project Assistant confirmed apply uses enabled Cairnline authority seams") || !strings.Contains(warnings, "orchestrator capabilities") {
		t.Fatalf("warnings = %+v, want assistant proposal authority and mixed apply authority caveats", status.Warnings)
	}
	if status.ReplacementReady {
		t.Fatalf("replacement_ready = true, want false until remaining write and migration gates are ready")
	}
	if gate := findReplacementGate(status.ReplacementGates, "write-authority-switchpoints"); gate == nil || gate.Ready || gate.Status != "partial" || strings.Contains(gate.Detail, "project-assistant-apply-side-effects") || !strings.Contains(gate.Detail, "agent-profiles") {
		t.Fatalf("write-authority gate = %+v, want partial portable write authority without assistant side-effect capability", gate)
	}
}

func TestProjectCoordinationBackendStatus_CairnlineAllPortableWriteAuthorityAliasConfigured(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			Backend:                 "sqlite",
			CoordinationBackend:     "cairnline",
			CairnlineReadSource:     "embedded",
			CairnlineWriteAuthority: "all-portable",
		},
	}, quietLogger(), nil, nil, nil, nil)

	status := handler.projectCoordinationBackendStatus()
	for _, name := range []string{
		"projects",
		"context-sources",
		"agent-profiles",
		"skills",
		"memory",
		"memory-candidates",
		"roles",
		"work-items",
		"assignments",
		"artifacts",
		"handoffs",
		"project-assistant-proposals",
	} {
		if containsString(status.WriteAdapterGaps, name) {
			t.Fatalf("write gaps = %+v, did not expect portable gap %q with all-portable authority alias", status.WriteAdapterGaps, name)
		}
	}
	for _, name := range []string{"roots", "assignment-start", "project-assistant-apply-side-effects", "migration-cutover"} {
		if !containsString(status.WriteAdapterGaps, name) {
			t.Fatalf("write gaps = %+v, want remaining non-portable/migration gap %q with all-portable authority alias", status.WriteAdapterGaps, name)
		}
	}
	if len(status.PortableWriteGaps) != 0 {
		t.Fatalf("portable write gaps = %+v, want all portable gaps removed with all-portable authority alias", status.PortableWriteGaps)
	}
	if !status.WriteAdapterReady {
		t.Fatalf("write_adapter_ready = false, want true when all portable write gaps are closed")
	}
	if status.ReplacementReady {
		t.Fatalf("replacement_ready = true, want false until migration and cutover gates are ready")
	}
	if status.ReplacementTarget != "embedded_cairnline_first" {
		t.Fatalf("replacement target = %q, want embedded-first Cairnline target", status.ReplacementTarget)
	}
	if status.ReplacementMode != "disabled" || status.ReplacementModeArmed {
		t.Fatalf("replacement mode = %q armed=%v, want disabled by default", status.ReplacementMode, status.ReplacementModeArmed)
	}
	if !reflect.DeepEqual(status.OrchestratorCapabilities, []string{"roots", "assignment-start", "project-assistant-apply-side-effects"}) {
		t.Fatalf("orchestrator capabilities = %+v, want remaining Hecate-owned orchestrator capabilities", status.OrchestratorCapabilities)
	}
	if !reflect.DeepEqual(status.MigrationBlockers, []string{"migration-cutover"}) {
		t.Fatalf("migration blockers = %+v, want migration cutover blocker", status.MigrationBlockers)
	}
	if gate := findReplacementGate(status.ReplacementGates, "write-authority-switchpoints"); gate == nil || !gate.Ready || gate.Status != "ready" || !strings.Contains(gate.Detail, "orchestrator capabilities") {
		t.Fatalf("write-authority gate = %+v, want ready portable write authority with orchestrator capability caveat", gate)
	}
	if status.NextReplacementAction == nil || status.NextReplacementAction.ID != "run-strict-embedded-read-smoke" || status.NextReplacementAction.Target != "strict-embedded-read-smoke" {
		t.Fatalf("next action = %+v, want strict embedded smoke after portable authority gaps close", status.NextReplacementAction)
	}
	if !containsString(status.NextReplacementAction.ProbeURLs, projectCoordinationBackendSyncReadinessURL) || !containsString(status.NextReplacementAction.ProbeURLs, projectCoordinationBackendMirrorParityURL) {
		t.Fatalf("next action probe URLs = %+v, want strict embedded sync and mirror-parity probes", status.NextReplacementAction.ProbeURLs)
	}
	if !hasProbe(status.NextReplacementAction.Probes, http.MethodPost, projectCoordinationBackendSyncReadinessURL) || !hasProbe(status.NextReplacementAction.Probes, http.MethodGet, projectCoordinationBackendMirrorParityURL) {
		t.Fatalf("next action probes = %+v, want POST sync and GET mirror-parity probes", status.NextReplacementAction.Probes)
	}
	if hint := findConfigHint(status.NextReplacementAction.ConfigHints, "HECATE_PROJECTS_CAIRNLINE_CONNECTOR"); hint == nil || hint.Value != "embedded" {
		t.Fatalf("next action config hints = %+v, want embedded connector hint", status.NextReplacementAction.ConfigHints)
	}
	if hint := findConfigHint(status.NextReplacementAction.ConfigHints, "HECATE_PROJECTS_CAIRNLINE_READ_SOURCE"); hint == nil || hint.Value != "embedded" {
		t.Fatalf("next action config hints = %+v, want strict embedded read-source hint", status.NextReplacementAction.ConfigHints)
	}
	if hint := findConfigHint(status.NextReplacementAction.ConfigHints, "HECATE_PROJECTS_CAIRNLINE_WRITE_AUTHORITY"); hint == nil || hint.Value != "all-portable" {
		t.Fatalf("next action config hints = %+v, want all-portable authority hint", status.NextReplacementAction.ConfigHints)
	}
}

func TestProjectCoordinationBackendStatus_CairnlineEmbeddedReplacementModeArmed(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			Backend:                  "sqlite",
			CoordinationBackend:      "cairnline",
			CairnlineConnector:       "embedded",
			CairnlineReadSource:      "embedded",
			CairnlineWriteAuthority:  "all-portable",
			CairnlineReplacementMode: "embedded",
		},
	}, quietLogger(), nil, nil, nil, nil)

	status := handler.projectCoordinationBackendStatus()
	if status.ReplacementMode != "embedded" || !status.ReplacementModeArmed || !strings.Contains(status.ReplacementModeDetail, "armed") {
		t.Fatalf("replacement mode/detail = %q/%v/%q, want armed embedded mode", status.ReplacementMode, status.ReplacementModeArmed, status.ReplacementModeDetail)
	}
	if status.ReplacementReady {
		t.Fatalf("replacement_ready = true, want false while migration/cutover blockers remain")
	}
	if gate := findReplacementGate(status.ReplacementGates, "embedded-replacement-mode"); gate == nil || !gate.Ready || gate.Status != "armed" || !strings.Contains(gate.Detail, "does not bypass") {
		t.Fatalf("embedded replacement mode gate = %+v, want armed explicit-mode gate", gate)
	}
	for _, tc := range []struct {
		name string
		want string
	}{
		{name: "project-identity", want: "skips native project identity compatibility rows"},
		{name: "skills", want: "skip native project-skill compatibility rows"},
		{name: "roles", want: "skip native project-work role compatibility rows"},
		{name: "work-items", want: "skip native project-work item compatibility rows"},
		{name: "assignments", want: "skip native project-work assignment compatibility rows"},
		{name: "collaboration-artifacts", want: "skips native project-work artifact compatibility rows"},
		{name: "handoffs", want: "skip native project-work handoff compatibility rows"},
	} {
		point := findWriteSwitchpoint(status.WriteSwitchpoints, tc.name)
		if point == nil || !strings.Contains(point.Detail, tc.want) {
			t.Fatalf("%s switchpoint = %+v, want armed replacement-mode detail containing %q", tc.name, point, tc.want)
		}
	}
	warnings := strings.Join(status.Warnings, "\n")
	if !strings.Contains(warnings, "skip native proposal ledger compatibility rows in armed embedded replacement mode") {
		t.Fatalf("warnings = %+v, want Project Assistant proposal warning to report no native proposal shadow", status.Warnings)
	}
	if strings.Contains(warnings, "Project Assistant proposal ledger mutations are opt-in Cairnline-authoritative and then best-effort shadowed into Hecate-native stores") {
		t.Fatalf("warnings = %+v, want no stale Project Assistant native-shadow wording", status.Warnings)
	}
	if status.NextReplacementAction == nil || status.NextReplacementAction.ID != "run-strict-embedded-read-smoke" {
		t.Fatalf("next action = %+v, want strict embedded smoke still prioritized while mode is armed", status.NextReplacementAction)
	}
	if !containsString(status.NextReplacementAction.ProbeURLs, projectCoordinationBackendSyncReadinessURL) || !containsString(status.NextReplacementAction.ProbeURLs, projectCoordinationBackendMirrorParityURL) {
		t.Fatalf("next action probe URLs = %+v, want strict embedded sync and mirror-parity probes", status.NextReplacementAction.ProbeURLs)
	}
	if !hasProbe(status.NextReplacementAction.Probes, http.MethodPost, projectCoordinationBackendSyncReadinessURL) || !hasProbe(status.NextReplacementAction.Probes, http.MethodGet, projectCoordinationBackendMirrorParityURL) {
		t.Fatalf("next action probes = %+v, want POST sync and GET mirror-parity probes", status.NextReplacementAction.Probes)
	}
}

func TestProjectCoordinationBackendStatus_WriteAuthorityGateUsesPortableGaps(t *testing.T) {
	gate := projectCairnlineWriteAuthorityReplacementGate(nil)
	if !gate.Ready || gate.Status != "ready" || !strings.Contains(gate.Detail, "orchestrator capabilities") {
		t.Fatalf("write-authority gate = %+v, want ready when portable write gaps are closed", gate)
	}
	gate = projectCairnlineWriteAuthorityReplacementGate([]string{"memory-candidates"})
	if gate.Ready || gate.Status != "partial" || !strings.Contains(gate.Detail, "memory-candidates") {
		t.Fatalf("write-authority gate = %+v, want partial when portable write gaps remain", gate)
	}
}

func TestProjectCoordinationBackendStatus_MigrationRollbackGateRequiresRehearsalEvidence(t *testing.T) {
	strictGate := ProjectCoordinationBackendReplacementGate{ID: "strict-embedded-read-smoke", Ready: true, Status: "verified"}
	gate := projectCairnlineMigrationRollbackReplacementGate(strictGate, nil, false)
	if gate.Ready || gate.Status != "rehearsal_incomplete" || !strings.Contains(gate.Detail, "does not include mirror-parity") {
		t.Fatalf("migration gate = %+v, want missing rehearsal evidence blocker", gate)
	}

	rehearsal := projectCairnlineMigrationRehearsal("mirror_parity", true, true, nil)
	gate = projectCairnlineMigrationRollbackReplacementGate(strictGate, &rehearsal, true)
	if gate.Ready || gate.Status != "rehearsal_incomplete" || !strings.Contains(gate.Detail, "strict-embedded-read-smoke") {
		t.Fatalf("migration gate = %+v, want incomplete smoke evidence to block even when replacement mode is armed", gate)
	}
}

func TestProjectCoordinationBackendStatus_ReplacementGatesReadyRequiresAllGates(t *testing.T) {
	if projectCairnlineReplacementGatesReady(nil) {
		t.Fatal("replacement gates ready = true, want false for empty gate list")
	}
	if projectCairnlineReplacementGatesReady([]ProjectCoordinationBackendReplacementGate{{ID: "read", Ready: true}, {ID: "write", Ready: false}}) {
		t.Fatal("replacement gates ready = true, want false when any gate is blocked")
	}
	if !projectCairnlineReplacementGatesReady([]ProjectCoordinationBackendReplacementGate{{ID: "read", Ready: true}, {ID: "write", Ready: true}}) {
		t.Fatal("replacement gates ready = false, want true when every gate is ready")
	}
}

func TestProjectCoordinationBackendStatus_NextActionWriteAuthorityHints(t *testing.T) {
	tests := []struct {
		gap  string
		want string
	}{
		{gap: "projects", want: "project-identity,project-metadata-defaults"},
		{gap: "memory-candidates", want: "project-memory,memory-candidates"},
		{gap: "artifacts", want: "project-collaboration"},
		{gap: "project-assistant-proposals", want: "project-assistant-proposals"},
	}
	for _, tt := range tests {
		t.Run(tt.gap, func(t *testing.T) {
			hints := projectCairnlineWriteAuthorityHintsForGap(tt.gap)
			hint := findConfigHint(hints, "HECATE_PROJECTS_CAIRNLINE_WRITE_AUTHORITY")
			if hint == nil || hint.Value != tt.want || hint.Detail == "" {
				t.Fatalf("hints = %+v, want write authority value %q with detail", hints, tt.want)
			}
		})
	}
	if hints := projectCairnlineWriteAuthorityHintsForGap("assignment-start"); len(hints) != 0 {
		t.Fatalf("assignment-start hints = %+v, want none because dispatch is not a portable write-authority switchpoint", hints)
	}
}

func TestProjectCoordinationBackendStatus_PortableWriteGapsHaveEffectiveAuthorityHints(t *testing.T) {
	nonPortableGaps := map[string]bool{
		"assignment-start":                     true,
		"project-assistant-apply-side-effects": true,
		"migration-cutover":                    true,
	}
	for _, gap := range projectCairnlineWriteAdapterGapNames {
		if nonPortableGaps[gap] {
			continue
		}
		t.Run(gap, func(t *testing.T) {
			values := projectCairnlineWriteAuthorityValuesForGap(gap)
			if len(values) == 0 {
				t.Fatalf("write authority values = nil, want config hint for portable gap %q", gap)
			}
			cfg := config.Config{
				Projects: config.ProjectsConfig{
					CairnlineWriteAuthority: strings.Join(values, ","),
				},
			}
			writeAuthority := cfg.ProjectsCairnlineWriteAuthority()
			for _, value := range values {
				if !cfg.ProjectsCairnlineWriteAuthorityEnabled(value) {
					t.Fatalf("write authority = %+v, want parsed value %q enabled", writeAuthority, value)
				}
			}
			writeGaps := projectCairnlineWriteAdapterGapsSnapshot(writeAuthority)
			portableGaps := projectCairnlinePortableWriteGapsSnapshot(writeAuthority, writeGaps)
			if containsString(portableGaps, gap) {
				t.Fatalf("portable gaps = %+v, want write authority values %+v to close %q", portableGaps, values, gap)
			}
			if gap == "roots" && (!containsString(writeGaps, "roots") || !containsString(projectCairnlineOrchestratorCapabilitiesSnapshot(writeGaps), "roots")) {
				t.Fatalf("write gaps = %+v, want roots to remain visible as Hecate-owned Git/workspace capability after portable root metadata closes", writeGaps)
			}
		})
	}
}

func TestProjectCoordinationBackendStatus_SidecarReadSourceWarningKeepsWritesEmbedded(t *testing.T) {
	warning := projectCairnlineReadSourceWarning("sidecar")
	if !strings.Contains(warning, "HECATE_PROJECTS_CAIRNLINE_READ_SOURCE=sidecar") ||
		!strings.Contains(warning, "project-chat-prelude, project-chat-context") ||
		!strings.Contains(warning, "HECATE_PROJECTS_CAIRNLINE_CONNECTOR=embedded") ||
		strings.Contains(warning, "unless a separate write-authority switchpoint is enabled") {
		t.Fatalf("warning = %q, want sidecar read guidance with embedded write-authority boundary", warning)
	}
}

func TestProjectCoordinationBackendStatus_NextActionReplacementModeHints(t *testing.T) {
	status := ProjectCoordinationBackendStatusResponse{
		ConfiguredBackend:       "cairnline",
		CairnlineConnectorReady: true,
		ReadModelSwitchReady:    true,
		WriteAdapterReady:       true,
		ReplacementMode:         "disabled",
	}

	action := projectCairnlineNextReplacementAction(status)
	if action == nil || action.ID != "arm-embedded-replacement-mode" || action.Target != "embedded-replacement-mode" {
		t.Fatalf("next action = %+v, want embedded replacement mode arming action", action)
	}
	if hint := findConfigHint(action.ConfigHints, "HECATE_PROJECTS_CAIRNLINE_REPLACEMENT_MODE"); hint == nil || hint.Value != "embedded" {
		t.Fatalf("replacement mode hints = %+v, want embedded replacement mode hint", action.ConfigHints)
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
	if !containsString(response.Data.ReadRoutes, "project-detail") || !containsString(response.Data.ReadRoutes, "launch-readiness") || !containsString(response.Data.ReadRoutes, "assignment-preflight") || !containsString(response.Data.ReadRoutes, "project-assistant-context") || !containsString(response.Data.ReadRoutes, "project-assistant-proposal") || !containsString(response.Data.ReadRoutes, "project-chat-prelude") || !containsString(response.Data.ReadRoutes, "project-chat-context") || !containsString(response.Data.WriteAdapterSeams, "project-identity-live-mirror") || !containsString(response.Data.WriteAdapterSeams, "project-roots-live-mirror") || !containsString(response.Data.WriteAdapterSeams, "project-context-sources-live-mirror") || !containsString(response.Data.WriteAdapterSeams, "project-defaults-live-mirror") || !containsString(response.Data.WriteAdapterSeams, "project-skills-live-mirror") || !containsString(response.Data.WriteAdapterSeams, "project-roles-live-mirror") || !containsString(response.Data.WriteAdapterSeams, "project-work-items-live-mirror") || !containsString(response.Data.WriteAdapterSeams, "project-assignments-live-mirror") || !containsString(response.Data.WriteAdapterSeams, "project-assignment-start-result-live-mirror") || !containsString(response.Data.WriteAdapterSeams, "project-assignment-chat-reconcile-live-mirror") || !containsString(response.Data.WriteAdapterSeams, "project-collaboration-live-mirror") || !containsString(response.Data.WriteAdapterSeams, "project-handoffs-live-mirror") || !containsString(response.Data.WriteAdapterSeams, "project-memory-live-mirror") || !containsString(response.Data.WriteAdapterSeams, "project-memory-candidates-live-mirror") || !containsString(response.Data.WriteAdapterSeams, "project-assistant-proposal-ledger-live-mirror") || !containsString(response.Data.WriteAdapterSeams, "project-assistant-apply-side-effects-live-mirror") || !containsString(response.Data.WriteAdapterSeams, "handoffs") || !containsString(response.Data.WriteAdapterGaps, "memory-candidates") || !containsString(response.Data.PortableWriteGaps, "memory-candidates") || !containsString(response.Data.OrchestratorCapabilities, "assignment-start") || !containsString(response.Data.MigrationBlockers, "migration-cutover") {
		t.Fatalf("response routes/seams/gaps = %+v / %+v / %+v portable=%+v orchestrator=%+v migration=%+v, want structured readiness details", response.Data.ReadRoutes, response.Data.WriteAdapterSeams, response.Data.WriteAdapterGaps, response.Data.PortableWriteGaps, response.Data.OrchestratorCapabilities, response.Data.MigrationBlockers)
	}
	if response.Data.ReplacementReady {
		t.Fatalf("response replacement_ready = true, want false until write authority and migration gates are ready")
	}
	migrationGate := findReplacementGate(response.Data.ReplacementGates, "migration-and-rollback")
	if migrationGate == nil || migrationGate.Status != "waiting_for_read_smoke" {
		t.Fatalf("response migration gate = %+v, want read-smoke waiting blocker", migrationGate)
	}
	if !containsString(migrationGate.ProbeURLs, projectCoordinationBackendSyncReadinessURL) || !containsString(migrationGate.ProbeURLs, projectCoordinationBackendMirrorParityURL) || !containsString(migrationGate.ProbeURLs, projectCoordinationBackendExportURL) {
		t.Fatalf("response migration gate probe URLs = %+v, want sync, mirror parity, and export probes", migrationGate.ProbeURLs)
	}
	if !hasProbe(migrationGate.Probes, http.MethodPost, projectCoordinationBackendSyncReadinessURL) || !hasProbe(migrationGate.Probes, http.MethodGet, projectCoordinationBackendMirrorParityURL) || !hasProbe(migrationGate.Probes, http.MethodPost, projectCoordinationBackendExportURL) {
		t.Fatalf("response migration gate probes = %+v, want POST sync, GET mirror parity, and POST export probes", migrationGate.Probes)
	}
	if response.Data.NextReplacementAction == nil || response.Data.NextReplacementAction.ID != "move-portable-write-authority" || response.Data.NextReplacementAction.Target == "" {
		t.Fatalf("response next action = %+v, want portable write-authority action", response.Data.NextReplacementAction)
	}
	if findConfigHint(response.Data.NextReplacementAction.ConfigHints, "HECATE_PROJECTS_CAIRNLINE_WRITE_AUTHORITY") == nil {
		t.Fatalf("response next action config hints = %+v, want write authority hint", response.Data.NextReplacementAction.ConfigHints)
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

func findConfigHint(items []ProjectCoordinationBackendActionConfigHint, env string) *ProjectCoordinationBackendActionConfigHint {
	for idx := range items {
		if items[idx].Env == env {
			return &items[idx]
		}
	}
	return nil
}

func hasProbe(items []ProjectCoordinationBackendProbe, method, url string) bool {
	for _, item := range items {
		if item.Method == method && item.URL == url {
			return true
		}
	}
	return false
}

func hasEmbeddedDogfoodProbes(items []ProjectCoordinationBackendProbe) bool {
	return hasProbe(items, http.MethodGet, projectCoordinationBackendStatusURL) &&
		hasProbe(items, http.MethodGet, projectCoordinationBackendReadinessURL) &&
		hasProbe(items, http.MethodGet, projectCoordinationBackendEmbeddedReadModelURL) &&
		hasProbe(items, http.MethodGet, projectCoordinationBackendEmbeddedParityReportURL) &&
		!hasProbe(items, http.MethodPost, projectCoordinationBackendSidecarProbeURL) &&
		!hasProbe(items, http.MethodPost, projectCoordinationBackendSidecarConnectURL)
}
