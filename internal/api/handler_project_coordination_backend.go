package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

const projectCoordinationBackendReadinessURL = "/hecate/v1/projects/{id}/cairnline/read-model"
const projectCoordinationBackendExportURL = "/hecate/v1/projects/{id}/cairnline/export"
const projectCoordinationBackendSidecarProbeURL = "/hecate/v1/projects/cairnline/sidecar-probe"
const projectCoordinationBackendSidecarConnectURL = "/hecate/v1/projects/cairnline/sidecar-connect"
const projectCoordinationBackendSidecarReadURL = "/hecate/v1/projects/cairnline/sidecar-read-smoke"
const projectCoordinationBackendSidecarDetailURL = "/hecate/v1/projects/cairnline/sidecar-detail-smoke"
const projectCoordinationBackendSidecarCoordinationURL = "/hecate/v1/projects/cairnline/sidecar-coordination-smoke"
const projectCoordinationBackendSidecarAssignmentContextURL = "/hecate/v1/projects/cairnline/sidecar-assignment-context-smoke"
const projectCoordinationBackendSidecarLaunchPacketURL = "/hecate/v1/projects/cairnline/sidecar-launch-packet-smoke"
const projectCoordinationBackendSidecarLifecycleURL = "/hecate/v1/projects/cairnline/sidecar-lifecycle-smoke"
const projectCoordinationBackendSidecarSetupURL = "/hecate/v1/projects/cairnline/sidecar-setup-smoke"
const projectCoordinationBackendSidecarWriteURL = "/hecate/v1/projects/cairnline/sidecar-write-smoke"
const projectCoordinationBackendSidecarWorkURL = "/hecate/v1/projects/cairnline/sidecar-work-smoke"
const projectCoordinationBackendSidecarCollaborationURL = "/hecate/v1/projects/cairnline/sidecar-collaboration-smoke"
const projectCoordinationBackendSidecarMemoryURL = "/hecate/v1/projects/cairnline/sidecar-memory-smoke"
const projectCoordinationBackendSidecarAssistantURL = "/hecate/v1/projects/cairnline/sidecar-assistant-smoke"
const projectCoordinationBackendEmbeddedReadModelURL = "/hecate/v1/projects/{id}/cairnline/embedded-read-model"
const projectCoordinationBackendEmbeddedParityReportURL = "/hecate/v1/projects/{id}/cairnline/embedded-parity-report"
const projectCoordinationBackendSyncReadinessURL = "/hecate/v1/projects/cairnline/sync"
const projectCoordinationBackendMirrorParityURL = "/hecate/v1/projects/cairnline/mirror-parity"

var projectCairnlineReadRouteNames = []string{
	"project-list",
	"project-detail",
	"setup-readiness",
	"health",
	"skills",
	"memory",
	"memory-candidate",
	"roles",
	"work-item",
	"assignment-list",
	"assignment-context",
	"launch-readiness",
	"assignment-preflight",
	"artifact-list",
	"handoff-list",
	"project-assistant-context",
	"project-assistant-proposal",
	"project-chat-prelude",
	"project-chat-context",
	"activity",
	"closeout-readiness",
	"operations-brief",
}

var projectCairnlineSidecarReadRouteNames = []string{
	"project-list",
	"project-detail",
	"setup-readiness",
	"health",
	"skills",
	"memory",
	"memory-candidate",
	"roles",
	"work-item",
	"assignment-list",
	"assignment-context",
	"launch-readiness",
	"assignment-preflight",
	"artifact-list",
	"handoff-list",
	"project-assistant-context",
	"project-assistant-proposal",
	"activity",
	"closeout-readiness",
	"operations-brief",
}

var projectCairnlineWriteAdapterSeamNames = []string{
	"projects",
	"roots",
	"project-roots-live-mirror",
	"context-sources",
	"project-context-sources-live-mirror",
	"project-metadata-live-mirror",
	"project-defaults",
	"project-defaults-live-mirror",
	"project-identity-live-mirror",
	"agent-profiles",
	"agent-profiles-live-mirror",
	"skills",
	"project-skills-live-mirror",
	"roles",
	"project-roles-live-mirror",
	"work-items",
	"project-work-items-live-mirror",
	"assignments",
	"assignment-status",
	"project-assignments-live-mirror",
	"project-assignment-start-result-live-mirror",
	"project-assignment-chat-reconcile-live-mirror",
	"artifacts-create",
	"evidence-create",
	"reviews-create",
	"project-collaboration-live-mirror",
	"handoffs",
	"project-handoffs-live-mirror",
	"memory",
	"project-memory-live-mirror",
	"memory-candidates",
	"project-memory-candidates-live-mirror",
	"project-assistant-proposal-ledger-import",
	"project-assistant-proposal-ledger-live-mirror",
	"project-assistant-apply-side-effects-live-mirror",
	"sync-rehearsal",
}

var projectCairnlineWriteAdapterGapNames = []string{
	"projects",
	"roots",
	"context-sources",
	"agent-profiles",
	"skills",
	"memory",
	"memory-candidates",
	"roles",
	"work-items",
	"assignments",
	"assignment-start",
	"artifacts",
	"handoffs",
	"project-assistant-proposals",
	"project-assistant-apply-side-effects",
	"migration-cutover",
}

var projectCairnlineWriteSwitchpoints = []ProjectCoordinationBackendWriteSwitchpoint{
	{
		Name:             "project-identity",
		CurrentAuthority: "hecate",
		CairnlineState:   "live_mirror_non_authoritative",
		LiveMirror:       true,
		BlocksAuthority:  true,
		Seams:            []string{"project-identity-live-mirror"},
		Gap:              "projects",
		Detail:           "Project create/delete still commit to Hecate stores first, then best-effort mirror portable identity records into the embedded Cairnline database.",
	},
	{
		Name:             "project-metadata-defaults",
		CurrentAuthority: "hecate",
		CairnlineState:   "live_mirror_non_authoritative",
		LiveMirror:       true,
		BlocksAuthority:  true,
		Seams:            []string{"project-metadata-live-mirror", "project-defaults-live-mirror"},
		Gap:              "projects",
		Detail:           "Project metadata and default posture mutations still commit to Hecate first, then mirror through Cairnline metadata/default seams.",
	},
	{
		Name:             "roots-and-worktrees",
		CurrentAuthority: "hecate",
		CairnlineState:   "live_mirror_non_authoritative",
		LiveMirror:       true,
		BlocksAuthority:  true,
		Seams:            []string{"project-roots-live-mirror"},
		Gap:              "roots",
		Detail:           "Root create/update/delete, root discovery, root list replacement, and worktree-created root record mutations still commit to Hecate first, then mirror through Cairnline root APIs; Hecate owns the Git worktree creation side effect.",
	},
	{
		Name:             "context-sources",
		CurrentAuthority: "hecate",
		CairnlineState:   "live_mirror_non_authoritative",
		LiveMirror:       true,
		BlocksAuthority:  true,
		Seams:            []string{"project-context-sources-live-mirror"},
		Gap:              "context-sources",
		Detail:           "Context-source discovery and direct source mutations still commit to Hecate first, then mirror through Cairnline source APIs.",
	},
	{
		Name:             "agent-profiles",
		CurrentAuthority: "hecate",
		CairnlineState:   "live_mirror_non_authoritative",
		LiveMirror:       true,
		BlocksAuthority:  true,
		Seams:            []string{"agent-profiles-live-mirror"},
		Gap:              "agent-profiles",
		Detail:           "Global agent-profile CRUD still commits to Hecate first, then mirrors portable profile metadata and execution posture into Cairnline.",
	},
	{
		Name:             "skills",
		CurrentAuthority: "hecate",
		CairnlineState:   "live_mirror_non_authoritative",
		LiveMirror:       true,
		BlocksAuthority:  true,
		Seams:            []string{"project-skills-live-mirror"},
		Gap:              "skills",
		Detail:           "Project skill discovery/update still commits metadata to Hecate first, then mirrors metadata-only skill records into Cairnline.",
	},
	{
		Name:             "roles",
		CurrentAuthority: "hecate",
		CairnlineState:   "live_mirror_non_authoritative",
		LiveMirror:       true,
		BlocksAuthority:  true,
		Seams:            []string{"project-roles-live-mirror"},
		Gap:              "roles",
		Detail:           "Role mutations still commit to Hecate first, then mirror coordination metadata and referenced portable profile posture into Cairnline.",
	},
	{
		Name:             "work-items",
		CurrentAuthority: "hecate",
		CairnlineState:   "live_mirror_non_authoritative",
		LiveMirror:       true,
		BlocksAuthority:  true,
		Seams:            []string{"project-work-items-live-mirror"},
		Gap:              "work-items",
		Detail:           "Work-item create/update/delete still commit to Hecate first, then mirror portable coordination metadata into Cairnline.",
	},
	{
		Name:             "assignments",
		CurrentAuthority: "hecate",
		CairnlineState:   "live_mirror_non_authoritative",
		LiveMirror:       true,
		BlocksAuthority:  true,
		Seams:            []string{"project-assignments-live-mirror", "project-assignment-chat-reconcile-live-mirror"},
		Gap:              "assignments",
		Detail:           "Assignment create/update/delete and linked-chat reconciliation still commit to Hecate first, then mirror portable metadata/status into Cairnline.",
	},
	{
		Name:             "assignment-start-dispatch",
		CurrentAuthority: "hecate",
		CairnlineState:   "result_mirror_only",
		LiveMirror:       true,
		BlocksAuthority:  false,
		Seams:            []string{"project-assignment-start-result-live-mirror"},
		Gap:              "assignment-start",
		Detail:           "Assignment start still dispatches through Hecate runtime/task/external-agent authority; Cairnline receives only committed start results and cleanup/conflict states.",
	},
	{
		Name:             "collaboration-artifacts",
		CurrentAuthority: "hecate",
		CairnlineState:   "live_mirror_non_authoritative",
		LiveMirror:       true,
		BlocksAuthority:  true,
		Seams:            []string{"project-collaboration-live-mirror"},
		Gap:              "artifacts",
		Detail:           "Generic artifact, evidence, and review creation still commits to Hecate first, then mirrors portable collaboration metadata into Cairnline.",
	},
	{
		Name:             "handoffs",
		CurrentAuthority: "hecate",
		CairnlineState:   "live_mirror_non_authoritative",
		LiveMirror:       true,
		BlocksAuthority:  true,
		Seams:            []string{"project-handoffs-live-mirror"},
		Gap:              "handoffs",
		Detail:           "Handoff create/update/delete still commits to Hecate first, then mirrors portable handoff metadata into Cairnline.",
	},
	{
		Name:             "project-memory",
		CurrentAuthority: "hecate",
		CairnlineState:   "live_mirror_non_authoritative",
		LiveMirror:       true,
		BlocksAuthority:  true,
		Seams:            []string{"project-memory-live-mirror"},
		Gap:              "memory",
		Detail:           "Accepted project memory mutations still commit to Hecate first, then mirror durable memory state into Cairnline.",
	},
	{
		Name:             "memory-candidates",
		CurrentAuthority: "hecate",
		CairnlineState:   "live_mirror_non_authoritative",
		LiveMirror:       true,
		BlocksAuthority:  true,
		Seams:            []string{"project-memory-candidates-live-mirror"},
		Gap:              "memory-candidates",
		Detail:           "Memory-candidate create/promote/reject/delete still commits to Hecate first, then mirrors review state and promoted-memory references into Cairnline.",
	},
	{
		Name:             "project-assistant-proposal-ledger",
		CurrentAuthority: "hecate",
		CairnlineState:   "live_mirror_non_authoritative",
		LiveMirror:       true,
		BlocksAuthority:  true,
		Seams:            []string{"project-assistant-proposal-ledger-live-mirror"},
		Gap:              "project-assistant-proposals",
		Detail:           "Project Assistant draft/propose/apply-attempt ledger records still commit to Hecate first, then mirror into the portable proposal ledger.",
	},
	{
		Name:             "project-assistant-apply-side-effects",
		CurrentAuthority: "hecate",
		CairnlineState:   "side_effect_mirror_only",
		LiveMirror:       true,
		BlocksAuthority:  false,
		Seams:            []string{"project-assistant-apply-side-effects-live-mirror"},
		Gap:              "project-assistant-apply-side-effects",
		Detail:           "Project Assistant confirmed apply still executes Hecate-owned project mutations, then mirrors committed side effects into Cairnline as replacement evidence.",
	},
	{
		Name:             "migration-cutover",
		CurrentAuthority: "hecate",
		CairnlineState:   "snapshot_import_rehearsal_available",
		BlocksAuthority:  true,
		Seams:            []string{"sync-rehearsal"},
		Gap:              "migration-cutover",
		Detail:           "Snapshot import/export rehearsal and rollback notes exist, but no authoritative Cairnline storage cutover switch exists yet.",
	},
}

func (h *Handler) HandleProjectCoordinationBackendStatus(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, ProjectCoordinationBackendStatusEnvelope{
		Object: "project_coordination_backend_status",
		Data:   h.projectCoordinationBackendStatusWithContext(r.Context()),
	})
}

func (h *Handler) projectCoordinationBackendStatus() ProjectCoordinationBackendStatusResponse {
	return h.projectCoordinationBackendStatusWithContext(context.Background())
}

func (h *Handler) projectCoordinationBackendStatusWithContext(ctx context.Context) ProjectCoordinationBackendStatusResponse {
	configured := "hecate"
	storageBackend := ""
	readSource := "auto"
	connector := "embedded"
	replacementMode := "disabled"
	if h != nil {
		configured = h.config.ProjectsCoordinationBackend()
		storageBackend = h.config.Projects.Backend
		readSource = h.config.ProjectsCairnlineReadSource()
		connector = h.config.ProjectsCairnlineConnector()
		replacementMode = h.config.ProjectsCairnlineReplacementMode()
	}
	connectorReady := projectCairnlineConnectorReady(connector)
	response := ProjectCoordinationBackendStatusResponse{
		ConfiguredBackend:                    configured,
		AuthoritativeBackend:                 "hecate",
		StorageBackend:                       storageBackend,
		CairnlineConnector:                   connector,
		CairnlineConnectorReady:              connectorReady,
		CairnlineConnectorDetail:             projectCairnlineConnectorDetail(connector),
		CairnlineReadSource:                  readSource,
		CairnlineBridgeReady:                 true,
		CairnlineAuthoritative:               false,
		WriteAdapterReady:                    false,
		ReplacementReadinessURL:              projectCoordinationBackendReadinessURL,
		ReplacementTarget:                    "embedded_cairnline_first",
		ReplacementTargetDetail:              "Hecate's Projects replacement path targets embedded Cairnline as the first source of truth; the standalone sidecar remains an interoperability and future external-server boundary.",
		ReplacementMode:                      replacementMode,
		ReplacementModeArmed:                 replacementMode == "embedded",
		ReplacementModeDetail:                projectCairnlineReplacementModeDetail(replacementMode),
		CairnlineSidecarProbeURL:             projectCoordinationBackendSidecarProbeURL,
		CairnlineSidecarConnectURL:           projectCoordinationBackendSidecarConnectURL,
		CairnlineSidecarReadURL:              projectCoordinationBackendSidecarReadURL,
		CairnlineSidecarDetailURL:            projectCoordinationBackendSidecarDetailURL,
		CairnlineSidecarCoordinationURL:      projectCoordinationBackendSidecarCoordinationURL,
		CairnlineSidecarAssignmentContextURL: projectCoordinationBackendSidecarAssignmentContextURL,
		CairnlineSidecarLaunchPacketURL:      projectCoordinationBackendSidecarLaunchPacketURL,
		CairnlineSidecarLifecycleURL:         projectCoordinationBackendSidecarLifecycleURL,
		CairnlineSidecarSetupURL:             projectCoordinationBackendSidecarSetupURL,
		CairnlineSidecarWriteURL:             projectCoordinationBackendSidecarWriteURL,
		CairnlineSidecarWorkURL:              projectCoordinationBackendSidecarWorkURL,
		CairnlineSidecarCollaborationURL:     projectCoordinationBackendSidecarCollaborationURL,
		CairnlineSidecarMemoryURL:            projectCoordinationBackendSidecarMemoryURL,
		CairnlineSidecarAssistantURL:         projectCoordinationBackendSidecarAssistantURL,
		EmbeddedReadModelURL:                 projectCoordinationBackendEmbeddedReadModelURL,
		EmbeddedParityReportURL:              projectCoordinationBackendEmbeddedParityReportURL,
		SyncReadinessURL:                     projectCoordinationBackendSyncReadinessURL,
		MirrorParityURL:                      projectCoordinationBackendMirrorParityURL,
	}
	switch configured {
	case "cairnline":
		readReady, sourceWarnings := h.cairnlineReadModelReadiness()
		strictEmbeddedReadGate := h.projectCairnlineStrictEmbeddedReadSmokeReplacementGate(ctx, connectorReady, readSource, readReady)
		writeAuthority := h.config.ProjectsCairnlineWriteAuthority()
		effectiveWriteAuthority := writeAuthority
		if !connectorReady {
			effectiveWriteAuthority = nil
		}
		response.WriteAdapterSeams = append([]string(nil), projectCairnlineWriteAdapterSeamNames...)
		response.WriteAdapterGaps = projectCairnlineWriteAdapterGapsSnapshot(effectiveWriteAuthority)
		response.PortableWriteGaps = projectCairnlinePortableWriteGapsSnapshot(effectiveWriteAuthority, response.WriteAdapterGaps)
		response.OrchestratorCapabilities = projectCairnlineOrchestratorCapabilitiesSnapshot(response.WriteAdapterGaps)
		response.MigrationBlockers = projectCairnlineMigrationBlockersSnapshot(response.WriteAdapterGaps)
		response.WriteAdapterReady = len(response.PortableWriteGaps) == 0
		response.WriteSwitchpoints = projectCairnlineWriteSwitchpointsSnapshot(effectiveWriteAuthority)
		response.ReplacementGates = projectCairnlineReplacementGates(readReady, response.PortableWriteGaps, replacementMode, strictEmbeddedReadGate)
		if !connectorReady {
			if h.projectCairnlineSidecarReadRoutesEnabled() {
				response.Status = "cairnline_sidecar_read_routes_ready"
				response.Detail = "Cairnline sidecar is configured as the project read source, so " + projectCairnlineReadRouteList(projectCairnlineSidecarReadRouteNames) + " routes read through the persistent standalone Cairnline MCP client. Project writes and migration remain on Hecate-native stores or existing embedded dogfood paths."
				response.ReadRoutes = append([]string(nil), projectCairnlineSidecarReadRouteNames...)
				response.ReplacementGates = projectCairnlineReplacementGates(false, response.PortableWriteGaps, replacementMode, strictEmbeddedReadGate)
				response.Warnings = []string{
					"Only " + projectCairnlineReadRouteList(projectCairnlineSidecarReadRouteNames) + " use the Cairnline sidecar MCP client in this mode.",
					"Project writes still use Hecate-native stores unless a separate embedded Cairnline authority switchpoint is explicitly enabled.",
					"Full Cairnline replacement remains blocked on authoritative write migration.",
				}
				return projectCairnlineStatusWithNextAction(response)
			}
			response.Status = "cairnline_connector_not_ready"
			response.Detail = projectCairnlineConnectorDetail(connector) + " Hecate keeps Projects reads and writes on Hecate-native stores in this mode; use HECATE_PROJECTS_CAIRNLINE_CONNECTOR=embedded for the current replacement-readiness dogfood path."
			response.ReplacementGates = projectCairnlineReplacementGates(false, response.PortableWriteGaps, replacementMode, strictEmbeddedReadGate)
			response.Warnings = []string{
				projectCairnlineConnectorWarning(connector),
				"Project reads and writes still use Hecate-native stores.",
			}
			return projectCairnlineStatusWithNextAction(response)
		}
		response.ReadModelSwitchReady = readReady
		if readReady {
			response.ReadRoutes = append([]string(nil), projectCairnlineReadRouteNames...)
			response.Status = "cairnline_read_routes_ready"
			response.Detail = "Cairnline is configured as the future Projects coordination backend, and the " + projectCairnlineReadRouteList(projectCairnlineReadRouteNames) + " read routes are served from the Cairnline read model. " + projectCairnlineReadSourceDetail(readSource) + " Hecate stores remain authoritative until the remaining writes and migration are ready."
			response.Warnings = []string{
				"Only the " + projectCairnlineReadRouteList(projectCairnlineReadRouteNames) + " live read routes use Cairnline.",
				projectCairnlineReadSourceWarning(readSource),
				projectCairnlineProjectIdentityWriteWarning(writeAuthority),
				projectCairnlineProjectMetadataDefaultsWriteWarning(writeAuthority),
				projectCairnlineProjectRootWriteWarning(writeAuthority),
				projectCairnlineProjectContextSourceWriteWarning(writeAuthority),
				projectCairnlineAgentProfileWriteWarning(writeAuthority),
				projectCairnlineProjectSkillWriteWarning(writeAuthority),
				projectCairnlineProjectWorkItemWriteWarning(writeAuthority),
				projectCairnlineProjectAssignmentWriteWarning(writeAuthority),
				projectCairnlineProjectCollaborationWriteWarning(writeAuthority),
				projectCairnlineProjectMemoryWriteWarning(writeAuthority),
				projectCairnlineProjectAssistantProposalWriteWarning(writeAuthority),
				projectCairnlineProjectAssistantApplyWriteWarning(writeAuthority),
				"Other project mutation routes still write only Hecate-native stores.",
				"Cairnline write-adapter seams are non-authoritative proofs; live write authority and migration path are not ready.",
			}
			return projectCairnlineStatusWithNextAction(response)
		}
		response.Status = "cairnline_configured_read_adapter_missing_sources"
		response.Detail = "Cairnline is configured as the future Projects coordination backend, but the read adapter cannot project the full Hecate project graph from the currently wired stores."
		response.Warnings = []string{
			"Project reads and writes still use Hecate-native stores.",
		}
		response.Warnings = append(response.Warnings, sourceWarnings...)
	default:
		response.Status = "hecate_authoritative"
		response.Detail = "Hecate-native project stores are authoritative. Cairnline bridge endpoints are available for replacement-readiness checks."
	}
	return projectCairnlineStatusWithNextAction(response)
}

func projectCairnlineStatusWithNextAction(response ProjectCoordinationBackendStatusResponse) ProjectCoordinationBackendStatusResponse {
	response.ReplacementReady = projectCairnlineReplacementGatesReady(response.ReplacementGates)
	if response.ReplacementReady {
		response.AuthoritativeBackend = "cairnline"
		response.CairnlineAuthoritative = true
	}
	response.NextReplacementAction = projectCairnlineNextReplacementAction(response)
	return response
}

func projectCairnlineNextReplacementAction(status ProjectCoordinationBackendStatusResponse) *ProjectCoordinationBackendNextAction {
	if status.ReplacementReady {
		return &ProjectCoordinationBackendNextAction{
			ID:     "replace-projects-backend",
			Label:  "Replace the Projects backend",
			Detail: "All Cairnline replacement gates are ready; the remaining action is an explicit operator cutover.",
			Target: "cutover",
		}
	}
	if status.ConfiguredBackend != "cairnline" {
		return &ProjectCoordinationBackendNextAction{
			ID:     "enable-cairnline-dogfood",
			Label:  "Enable Cairnline dogfood",
			Detail: "Configure Cairnline as the project coordination backend in a local dogfood runtime before moving any authority.",
			Target: "configuration",
			ConfigHints: []ProjectCoordinationBackendActionConfigHint{
				projectCairnlineConfigHint("HECATE_PROJECTS_COORDINATION_BACKEND", "cairnline", "Enable Cairnline as the Projects coordination backend for local dogfooding."),
				projectCairnlineConfigHint("HECATE_PROJECTS_CAIRNLINE_CONNECTOR", "embedded", "Use the embedded connector for the current write-authority dogfood path."),
			},
			ProbeURLs: []string{
				projectCoordinationBackendSidecarProbeURL,
				projectCoordinationBackendSidecarConnectURL,
			},
		}
	}
	if !status.CairnlineConnectorReady {
		return &ProjectCoordinationBackendNextAction{
			ID:     "use-embedded-cairnline-connector",
			Label:  "Use the embedded Cairnline connector",
			Detail: "Sidecar mode is useful for MCP diagnostics and read smoke tests, but write-authority dogfood currently requires the embedded Cairnline connector.",
			Target: "connector",
			ConfigHints: []ProjectCoordinationBackendActionConfigHint{
				projectCairnlineConfigHint("HECATE_PROJECTS_CAIRNLINE_CONNECTOR", "embedded", "Use the embedded connector before enabling Cairnline write-authority switchpoints."),
			},
			ProbeURLs: []string{
				projectCoordinationBackendSidecarProbeURL,
				projectCoordinationBackendSidecarConnectURL,
			},
		}
	}
	if !status.ReadModelSwitchReady && len(status.ReadRoutes) == 0 {
		return &ProjectCoordinationBackendNextAction{
			ID:     "complete-read-model-sources",
			Label:  "Complete Cairnline read-model sources",
			Detail: "Wire the remaining Hecate project stores into the Cairnline projection so live read routes can switch safely.",
			Target: "read-routes",
			ProbeURLs: []string{
				projectCoordinationBackendReadinessURL,
				projectCoordinationBackendEmbeddedReadModelURL,
				projectCoordinationBackendEmbeddedParityReportURL,
			},
		}
	}
	if len(status.PortableWriteGaps) > 0 {
		target := status.PortableWriteGaps[0]
		return &ProjectCoordinationBackendNextAction{
			ID:          "move-portable-write-authority",
			Label:       "Move the next portable write authority",
			Detail:      "Close the next portable project-state gap by adding a Cairnline-authoritative switchpoint while keeping Hecate as compatibility shadow.",
			Target:      target,
			ConfigHints: projectCairnlineWriteAuthorityHintsForGap(target),
		}
	}
	if len(status.MigrationBlockers) > 0 {
		if gate := projectCoordinationBackendReplacementGateByID(status.ReplacementGates, "migration-and-rollback"); gate != nil && gate.Status == "cutover_switch_missing" {
			return &ProjectCoordinationBackendNextAction{
				ID:     "implement-migration-cutover",
				Label:  "Implement migration cutover",
				Detail: "Strict embedded mirror parity and read smoke are verified; the remaining migration blocker is an explicit authoritative cutover and rollback switch.",
				Target: status.MigrationBlockers[0],
				ProbeURLs: []string{
					projectCoordinationBackendSyncReadinessURL,
					projectCoordinationBackendMirrorParityURL,
					projectCoordinationBackendExportURL,
				},
			}
		}
		return &ProjectCoordinationBackendNextAction{
			ID:          "rehearse-migration-cutover",
			Label:       "Rehearse migration and rollback",
			Detail:      "Run strict embedded sync/parity/export rehearsal paths and document the explicit cutover and rollback procedure before replacement.",
			Target:      status.MigrationBlockers[0],
			ConfigHints: projectCairnlineMigrationCutoverHints(),
			ProbeURLs: []string{
				projectCoordinationBackendSyncReadinessURL,
				projectCoordinationBackendMirrorParityURL,
				projectCoordinationBackendExportURL,
			},
		}
	}
	if status.ReplacementMode != "embedded" {
		return &ProjectCoordinationBackendNextAction{
			ID:          "arm-embedded-replacement-mode",
			Label:       "Arm embedded Cairnline replacement mode",
			Detail:      "All status-derived blocker groups are clear; explicitly arm embedded Cairnline replacement mode before final probes or cutover.",
			Target:      "embedded-replacement-mode",
			ConfigHints: projectCairnlineReplacementModeHints(),
		}
	}
	return &ProjectCoordinationBackendNextAction{
		ID:     "run-final-replacement-probes",
		Label:  "Run final replacement probes",
		Detail: "No blocker groups remain in the status snapshot; rerun read, parity, sync, and migration probes before marking the backend replaceable.",
		Target: "replacement-gates",
		ProbeURLs: []string{
			projectCoordinationBackendReadinessURL,
			projectCoordinationBackendEmbeddedParityReportURL,
			projectCoordinationBackendSyncReadinessURL,
			projectCoordinationBackendMirrorParityURL,
		},
	}
}

func projectCoordinationBackendReplacementGateByID(gates []ProjectCoordinationBackendReplacementGate, id string) *ProjectCoordinationBackendReplacementGate {
	for i := range gates {
		if gates[i].ID == id {
			return &gates[i]
		}
	}
	return nil
}

func projectCairnlineReplacementModeHints() []ProjectCoordinationBackendActionConfigHint {
	return []ProjectCoordinationBackendActionConfigHint{
		projectCairnlineConfigHint("HECATE_PROJECTS_CAIRNLINE_REPLACEMENT_MODE", "embedded", "Arm the explicit embedded Cairnline replacement contract after read, write-authority, migration, and rollback gates are ready."),
	}
}

func projectCairnlineMigrationCutoverHints() []ProjectCoordinationBackendActionConfigHint {
	return []ProjectCoordinationBackendActionConfigHint{
		projectCairnlineConfigHint("HECATE_PROJECTS_CAIRNLINE_CONNECTOR", "embedded", "Use the embedded connector for current write-authority and migration rehearsal paths."),
		projectCairnlineConfigHint("HECATE_PROJECTS_CAIRNLINE_READ_SOURCE", "embedded", "Force configured project reads to fail loudly when the embedded mirror is missing or stale during cutover rehearsal."),
		projectCairnlineConfigHint("HECATE_PROJECTS_CAIRNLINE_WRITE_AUTHORITY", "all-portable", "Keep all portable write-authority switchpoints enabled while rehearsing migration and rollback."),
	}
}

func projectCairnlineConfigHint(env, value, detail string) ProjectCoordinationBackendActionConfigHint {
	return ProjectCoordinationBackendActionConfigHint{
		Env:    env,
		Value:  value,
		Detail: detail,
	}
}

func projectCairnlineWriteAuthorityHintsForGap(gap string) []ProjectCoordinationBackendActionConfigHint {
	values := projectCairnlineWriteAuthorityValuesForGap(gap)
	if len(values) == 0 {
		return nil
	}
	detail := "Add this value to HECATE_PROJECTS_CAIRNLINE_WRITE_AUTHORITY for embedded Cairnline write-authority dogfooding."
	if len(values) > 1 {
		detail = "Add these comma-separated values to HECATE_PROJECTS_CAIRNLINE_WRITE_AUTHORITY; this gap requires the switchpoints together."
	}
	return []ProjectCoordinationBackendActionConfigHint{
		projectCairnlineConfigHint("HECATE_PROJECTS_CAIRNLINE_WRITE_AUTHORITY", strings.Join(values, ","), detail),
	}
}

func projectCairnlineWriteAuthorityValuesForGap(gap string) []string {
	switch gap {
	case "projects":
		return []string{projectCairnlineWriteAuthorityProjectIdentity, projectCairnlineWriteAuthorityProjectMetadataDefaults}
	case "roots":
		return []string{projectCairnlineWriteAuthorityProjectRoots}
	case "context-sources":
		return []string{projectCairnlineWriteAuthorityProjectContextSources}
	case "agent-profiles":
		return []string{projectCairnlineWriteAuthorityAgentProfiles}
	case "skills":
		return []string{projectCairnlineWriteAuthorityProjectSkills}
	case "memory":
		return []string{"project-memory"}
	case "memory-candidates":
		return []string{"project-memory", "memory-candidates"}
	case "roles":
		return []string{projectCairnlineWriteAuthorityProjectRoles}
	case "work-items":
		return []string{projectCairnlineWriteAuthorityProjectWorkItems}
	case "assignments":
		return []string{projectCairnlineWriteAuthorityProjectAssignments}
	case "artifacts", "handoffs":
		return []string{projectCairnlineWriteAuthorityProjectCollaboration}
	case "project-assistant-proposals":
		return []string{projectCairnlineWriteAuthorityProjectAssistantProposals}
	default:
		return nil
	}
}

func projectCairnlineReplacementGates(readRoutesReady bool, portableWriteGaps []string, replacementMode string, strictEmbeddedReadGate ProjectCoordinationBackendReplacementGate) []ProjectCoordinationBackendReplacementGate {
	if strings.TrimSpace(strictEmbeddedReadGate.ID) == "" {
		strictEmbeddedReadGate = projectCairnlineStrictEmbeddedReadSmokeDefaultGate()
	}
	writeGate := projectCairnlineWriteAuthorityReplacementGate(portableWriteGaps)
	return []ProjectCoordinationBackendReplacementGate{
		{
			ID:        "read-routes",
			Ready:     readRoutesReady,
			Status:    projectReplacementGateStatus(readRoutesReady),
			Detail:    "Configured live project read families can be served from Cairnline's projected read model.",
			ProbeURLs: []string{projectCoordinationBackendReadinessURL, projectCoordinationBackendEmbeddedReadModelURL, projectCoordinationBackendEmbeddedParityReportURL},
		},
		strictEmbeddedReadGate,
		writeGate,
		projectCairnlineMigrationRollbackReplacementGate(strictEmbeddedReadGate),
		projectCairnlineReplacementModeGate(replacementMode),
	}
}

func projectCairnlineStrictEmbeddedReadSmokeDefaultGate() ProjectCoordinationBackendReplacementGate {
	return ProjectCoordinationBackendReplacementGate{
		ID:        "strict-embedded-read-smoke",
		Ready:     false,
		Status:    "operator_probe_required",
		Detail:    "Run the embedded sync/parity/smoke probes with HECATE_PROJECTS_CAIRNLINE_READ_SOURCE=embedded before treating the mirror database as a cutover candidate.",
		ProbeURLs: []string{projectCoordinationBackendSyncReadinessURL, projectCoordinationBackendMirrorParityURL},
	}
}

func (h *Handler) projectCairnlineStrictEmbeddedReadSmokeReplacementGate(ctx context.Context, connectorReady bool, readSource string, readRoutesReady bool) ProjectCoordinationBackendReplacementGate {
	gate := projectCairnlineStrictEmbeddedReadSmokeDefaultGate()
	if !connectorReady {
		gate.Status = "blocked"
		gate.Detail = "Strict embedded read smoke requires the embedded Cairnline connector; configure HECATE_PROJECTS_CAIRNLINE_CONNECTOR=embedded before using the embedded mirror as a replacement candidate."
		return gate
	}
	if readSource != "embedded" {
		gate.Detail = "Set HECATE_PROJECTS_CAIRNLINE_READ_SOURCE=embedded and run the embedded sync/parity/smoke probes before treating the mirror database as a cutover candidate."
		return gate
	}
	if !readRoutesReady {
		gate.Status = "blocked"
		gate.Detail = "Strict embedded read smoke cannot run until the Cairnline read adapter can load the full Hecate project graph."
		return gate
	}
	if strings.TrimSpace(h.config.Server.DataDir) == "" {
		gate.Detail = "Run backend status with a configured Hecate data directory, then run the embedded sync/parity/smoke probes with HECATE_PROJECTS_CAIRNLINE_READ_SOURCE=embedded before treating the mirror database as a cutover candidate."
		return gate
	}
	item, err := h.projectCairnlineMirrorParity(ctx)
	if err != nil {
		gate.Status = "probe_error"
		gate.Detail = "Strict embedded read smoke could not inspect the embedded Cairnline mirror: " + err.Error()
		return gate
	}
	if !item.DatabaseExists {
		gate.Status = "not_run"
		gate.Detail = "No embedded Cairnline mirror database exists yet; run " + projectCoordinationBackendSyncReadinessURL + " and then " + projectCoordinationBackendMirrorParityURL + "."
		return gate
	}
	if !item.Match {
		gate.Status = "drift_detected"
		gate.Detail = "The embedded Cairnline mirror exists but no longer matches Hecate's authoritative stores; rerun the sync and mirror-parity probes before cutover."
		return gate
	}
	smoke := item.MigrationRehearsal.EmbeddedSmoke
	if smoke == nil {
		gate.Status = "operator_probe_required"
		gate.Detail = "The embedded Cairnline mirror matches Hecate stores, but strict embedded read smoke evidence is missing; rerun the mirror-parity probe."
		return gate
	}
	if smoke.Status != "passed" {
		gate.Status = smoke.Status
		if gate.Status == "" {
			gate.Status = "failed"
		}
		gate.Detail = fmt.Sprintf("The embedded Cairnline mirror matches Hecate stores, but strict embedded read smoke reported %s with %d error(s).", gate.Status, len(smoke.Errors))
		return gate
	}
	gate.Ready = true
	gate.Status = "verified"
	gate.Detail = fmt.Sprintf("Existing embedded Cairnline mirror matches Hecate stores and strict embedded read smoke passed across %d project(s) and %d route check(s).", smoke.ProjectCount, smoke.ReadRouteChecks)
	return gate
}

func projectCairnlineMigrationRollbackReplacementGate(strictEmbeddedReadGate ProjectCoordinationBackendReplacementGate) ProjectCoordinationBackendReplacementGate {
	gate := ProjectCoordinationBackendReplacementGate{
		ID:        "migration-and-rollback",
		Ready:     false,
		Status:    "waiting_for_read_smoke",
		Detail:    "Strict embedded mirror parity and read smoke must be verified before migration and rollback can be treated as rehearsed.",
		ProbeURLs: []string{projectCoordinationBackendSyncReadinessURL, projectCoordinationBackendMirrorParityURL, projectCoordinationBackendExportURL},
	}
	if strictEmbeddedReadGate.Ready {
		gate.Status = "cutover_switch_missing"
		gate.Detail = "Strict embedded mirror parity and read smoke are verified, and rehearsal endpoints expose rollback notes, but no authoritative Cairnline storage cutover switch exists yet."
	}
	return gate
}

func projectCairnlineReplacementModeGate(replacementMode string) ProjectCoordinationBackendReplacementGate {
	if replacementMode == "embedded" {
		return ProjectCoordinationBackendReplacementGate{
			ID:     "embedded-replacement-mode",
			Ready:  true,
			Status: "armed",
			Detail: "HECATE_PROJECTS_CAIRNLINE_REPLACEMENT_MODE=embedded is set, so the operator has explicitly armed the embedded Cairnline cutover contract. This does not bypass read, write, migration, rollback, or Hecate-owned runtime side-effect gates.",
		}
	}
	return ProjectCoordinationBackendReplacementGate{
		ID:     "embedded-replacement-mode",
		Ready:  false,
		Status: "disabled",
		Detail: "Embedded Cairnline replacement mode is disabled. Keep it disabled until read routes, portable write authority, migration rehearsal, and rollback checks are ready; then set HECATE_PROJECTS_CAIRNLINE_REPLACEMENT_MODE=embedded to arm the explicit cutover contract.",
	}
}

func projectCairnlineReplacementModeDetail(replacementMode string) string {
	if replacementMode == "embedded" {
		return "Embedded Cairnline replacement mode is armed, but status still requires the read, write-authority, migration, rollback, and runtime-boundary gates before any backend can be considered replaceable."
	}
	return "Embedded Cairnline replacement mode is disabled; Hecate will not report Projects as replaceable without an explicit operator cutover-mode opt-in."
}

func projectCairnlineReplacementGatesReady(gates []ProjectCoordinationBackendReplacementGate) bool {
	if len(gates) == 0 {
		return false
	}
	for _, gate := range gates {
		if !gate.Ready {
			return false
		}
	}
	return true
}

func projectCairnlineWriteAuthorityReplacementGate(portableWriteGaps []string) ProjectCoordinationBackendReplacementGate {
	remaining := append([]string(nil), portableWriteGaps...)
	gate := ProjectCoordinationBackendReplacementGate{
		ID:     "write-authority-switchpoints",
		Ready:  false,
		Status: "blocked",
		Detail: "Portable project-state mutation routes still commit to Hecate-native stores first; Cairnline mirrors are replacement evidence, not write authority.",
	}
	switch {
	case len(remaining) == 0:
		gate.Ready = true
		gate.Status = "ready"
		gate.Detail = "All portable project-state mutation switchpoints that have landed are Cairnline-authoritative; Hecate-owned orchestrator capabilities, migration, rollback, and final cutover stay outside this gate."
	case len(remaining) < projectCairnlinePortableWriteGapCount():
		gate.Status = "partial"
		gate.Detail = "Some portable project-state mutation switchpoints are Cairnline-authoritative; remaining portable write gaps: " + strings.Join(remaining, ", ") + "."
	}
	return gate
}

func projectCairnlinePortableWriteGapCount() int {
	count := 0
	for _, gap := range projectCairnlineWriteAdapterGapNames {
		switch gap {
		case "migration-cutover", "assignment-start", "project-assistant-apply-side-effects":
			continue
		}
		count++
	}
	return count
}

func projectReplacementGateStatus(ready bool) string {
	if ready {
		return "ready"
	}
	return "blocked"
}

func projectCairnlineWriteAdapterGapsSnapshot(writeAuthority []string) []string {
	out := make([]string, 0, len(projectCairnlineWriteAdapterGapNames))
	projectIdentityAuthoritative := projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectIdentity)
	projectMetadataDefaultsAuthoritative := projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectMetadataDefaults)
	projectMemoryAuthoritative := projectCairnlineWriteAuthorityEnabled(writeAuthority, "project-memory")
	memoryCandidatesAuthoritative := projectMemoryAuthoritative && projectCairnlineWriteAuthorityEnabled(writeAuthority, "memory-candidates")
	projectCollaborationAuthoritative := projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectCollaboration)
	agentProfilesAuthoritative := projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityAgentProfiles)
	projectSkillsAuthoritative := projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectSkills)
	projectRolesAuthoritative := projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectRoles)
	projectWorkItemsAuthoritative := projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectWorkItems)
	projectAssignmentsAuthoritative := projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectAssignments)
	projectContextSourcesAuthoritative := projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectContextSources)
	projectAssistantProposalsAuthoritative := projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectAssistantProposals)
	for _, item := range projectCairnlineWriteAdapterGapNames {
		if projectIdentityAuthoritative && projectMetadataDefaultsAuthoritative && item == "projects" {
			continue
		}
		if projectMemoryAuthoritative && item == "memory" {
			continue
		}
		if memoryCandidatesAuthoritative && item == "memory-candidates" {
			continue
		}
		if projectCollaborationAuthoritative && (item == "artifacts" || item == "handoffs") {
			continue
		}
		if agentProfilesAuthoritative && item == "agent-profiles" {
			continue
		}
		if projectSkillsAuthoritative && item == "skills" {
			continue
		}
		if projectRolesAuthoritative && item == "roles" {
			continue
		}
		if projectWorkItemsAuthoritative && item == "work-items" {
			continue
		}
		if projectAssignmentsAuthoritative && item == "assignments" {
			continue
		}
		if projectContextSourcesAuthoritative && item == "context-sources" {
			continue
		}
		if projectAssistantProposalsAuthoritative && item == "project-assistant-proposals" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func projectCairnlinePortableWriteGapsSnapshot(writeAuthority, writeGaps []string) []string {
	out := make([]string, 0, len(writeGaps))
	projectRootsAuthoritative := projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectRoots)
	for _, item := range writeGaps {
		switch item {
		case "migration-cutover", "assignment-start", "project-assistant-apply-side-effects":
			continue
		case "roots":
			if projectRootsAuthoritative {
				continue
			}
		}
		out = append(out, item)
	}
	return out
}

func projectCairnlineOrchestratorCapabilitiesSnapshot(writeGaps []string) []string {
	out := make([]string, 0, 3)
	for _, item := range writeGaps {
		switch item {
		case "roots", "assignment-start", "project-assistant-apply-side-effects":
			out = append(out, item)
		}
	}
	return out
}

func projectCairnlineMigrationBlockersSnapshot(writeGaps []string) []string {
	out := make([]string, 0, 1)
	for _, item := range writeGaps {
		if item == "migration-cutover" {
			out = append(out, item)
		}
	}
	return out
}

func projectCairnlineWriteSwitchpointsSnapshot(writeAuthority []string) []ProjectCoordinationBackendWriteSwitchpoint {
	out := make([]ProjectCoordinationBackendWriteSwitchpoint, 0, len(projectCairnlineWriteSwitchpoints))
	projectMemoryAuthoritative := projectCairnlineWriteAuthorityEnabled(writeAuthority, "project-memory")
	memoryCandidatesAuthoritative := projectMemoryAuthoritative && projectCairnlineWriteAuthorityEnabled(writeAuthority, "memory-candidates")
	projectCollaborationAuthoritative := projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectCollaboration)
	agentProfilesAuthoritative := projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityAgentProfiles)
	projectIdentityAuthoritative := projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectIdentity)
	projectMetadataDefaultsAuthoritative := projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectMetadataDefaults)
	projectRootsAuthoritative := projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectRoots)
	projectContextSourcesAuthoritative := projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectContextSources)
	projectSkillsAuthoritative := projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectSkills)
	projectRolesAuthoritative := projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectRoles)
	projectWorkItemsAuthoritative := projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectWorkItems)
	projectAssignmentsAuthoritative := projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectAssignments)
	projectAssistantProposalsAuthoritative := projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectAssistantProposals)
	projectAssistantPortableEffectsAuthoritative := projectCairnlineAssistantApplyPortableEffectsAuthoritative(writeAuthority)
	for _, item := range projectCairnlineWriteSwitchpoints {
		if projectIdentityAuthoritative && item.Name == "project-identity" {
			item.CurrentAuthority = "cairnline"
			item.CairnlineState = "authoritative_opt_in"
			item.LiveMirror = true
			item.BlocksAuthority = false
			item.Gap = ""
			item.Detail = "Project create/delete commits portable identity, roots, context sources, launch defaults, and project identity removal to the embedded Cairnline database first, then best-effort shadows Hecate's compatibility row; delete rolls the Cairnline snapshot back if Hecate compatibility cleanup fails."
		}
		if projectMetadataDefaultsAuthoritative && item.Name == "project-metadata-defaults" {
			item.CurrentAuthority = "cairnline"
			item.CairnlineState = "authoritative_opt_in"
			item.LiveMirror = true
			item.BlocksAuthority = false
			item.Gap = ""
			item.Detail = "Project metadata/default-only update mutations commit portable project metadata and launch defaults to the embedded Cairnline database first, then best-effort shadow Hecate's compatibility row back into Hecate-native stores; project identity, roots, context sources, and mixed metadata/root/source replacement routes are controlled by separate switchpoints."
		}
		if projectRootsAuthoritative && item.Name == "roots-and-worktrees" {
			item.CurrentAuthority = "mixed"
			item.CairnlineState = "partial_authoritative_opt_in"
			item.LiveMirror = true
			item.BlocksAuthority = false
			item.Gap = "roots"
			item.Detail = "Project root create/update/delete, root list replacement, discovery-result replacement, and worktree-created root record mutations commit to the embedded Cairnline database first, then best-effort shadow Hecate's compatibility row; discovery and worktree-created root record mutations can resolve project identity and roots from the embedded Cairnline graph, while Hecate still performs root discovery scans and Git worktree creation side effects."
		}
		if projectContextSourcesAuthoritative && item.Name == "context-sources" {
			item.CurrentAuthority = "cairnline"
			item.CairnlineState = "authoritative_opt_in"
			item.LiveMirror = true
			item.BlocksAuthority = false
			item.Gap = ""
			item.Detail = "Context-source create/update/delete, list replacement, and discovery-result replacement mutations commit to the embedded Cairnline database first, then best-effort shadow Hecate's compatibility row; discovery can resolve project identity, roots, and existing sources from the embedded Cairnline graph, while Hecate still performs the workspace scan for its operator UI."
		}
		if agentProfilesAuthoritative && item.Name == "agent-profiles" {
			item.CurrentAuthority = "cairnline"
			item.CairnlineState = "authoritative_opt_in"
			item.LiveMirror = true
			item.BlocksAuthority = false
			item.Gap = ""
			item.Detail = "Agent profile create, update, and delete mutations commit portable profile metadata plus execution posture to the embedded Cairnline database first, then best-effort shadow Hecate's combined profile row back into Hecate-native stores for compatibility."
		}
		if projectSkillsAuthoritative && item.Name == "skills" {
			item.CurrentAuthority = "cairnline"
			item.CairnlineState = "authoritative_opt_in"
			item.LiveMirror = true
			item.BlocksAuthority = false
			item.Gap = ""
			item.Detail = "Project skill discovery and update mutations commit metadata-only skill records to the embedded Cairnline database first, using Cairnline-owned roots and context sources when no Hecate-native project row exists, then best-effort shadow them back into Hecate-native stores for compatibility."
		}
		if projectRolesAuthoritative && item.Name == "roles" {
			item.CurrentAuthority = "cairnline"
			item.CairnlineState = "authoritative_opt_in"
			item.LiveMirror = true
			item.BlocksAuthority = false
			item.Gap = ""
			item.Detail = "Project role create, update, and delete mutations commit to the embedded Cairnline database first, then best-effort shadow portable role defaults back into Hecate-native stores for compatibility."
		}
		if projectWorkItemsAuthoritative && item.Name == "work-items" {
			item.CurrentAuthority = "cairnline"
			item.CairnlineState = "authoritative_opt_in"
			item.LiveMirror = true
			item.BlocksAuthority = false
			item.Gap = ""
			item.Detail = "Work-item create, update, and delete mutations commit to the embedded Cairnline database first, then best-effort shadow portable work-item state back into Hecate-native stores for compatibility."
		}
		if projectAssignmentsAuthoritative && item.Name == "assignments" {
			item.CurrentAuthority = "cairnline"
			item.CairnlineState = "authoritative_opt_in"
			item.LiveMirror = true
			item.BlocksAuthority = false
			item.Gap = ""
			item.Detail = "Assignment create, update, and delete record mutations commit to the embedded Cairnline database first, then best-effort shadow portable assignment state back into Hecate-native stores for compatibility; assignment start remains Hecate-owned."
		}
		if projectAssistantProposalsAuthoritative && item.Name == "project-assistant-proposal-ledger" {
			item.CurrentAuthority = "cairnline"
			item.CairnlineState = "authoritative_opt_in"
			item.LiveMirror = true
			item.BlocksAuthority = false
			item.Gap = ""
			item.Detail = "Project Assistant draft/propose/apply-attempt ledger records commit to the embedded Cairnline database first, then best-effort shadow Hecate's proposal store for compatibility; confirmed apply side effects remain Hecate-owned."
			if projectAssistantPortableEffectsAuthoritative {
				item.Detail = "Project Assistant draft/propose/apply-attempt ledger records commit to the embedded Cairnline database first, then best-effort shadow Hecate's proposal store for compatibility; confirmed apply is mixed-authority when enabled portable actions route through Cairnline."
			}
		}
		if projectAssistantPortableEffectsAuthoritative && item.Name == "project-assistant-apply-side-effects" {
			item.CurrentAuthority = "mixed"
			item.CairnlineState = "partial_authoritative_via_portable_switchpoints"
			item.LiveMirror = true
			item.BlocksAuthority = false
			item.Gap = "project-assistant-apply-side-effects"
			item.Detail = "Project Assistant confirmed apply routes covered portable actions through their enabled Cairnline authority switchpoints: project create, project metadata/default, root, role, work-item, assignment, handoff, and memory-candidate; chat/runtime effects remain Hecate-owned orchestrator capabilities outside Cairnline core."
		}
		if projectCollaborationAuthoritative && item.Name == "collaboration-artifacts" {
			item.CurrentAuthority = "cairnline"
			item.CairnlineState = "authoritative_opt_in"
			item.LiveMirror = true
			item.BlocksAuthority = false
			item.Gap = ""
			item.Detail = "Generic artifact, evidence, and review creation commits to the embedded Cairnline database first, then best-effort shadows the portable collaboration record back into Hecate-native stores for compatibility."
		}
		if projectCollaborationAuthoritative && item.Name == "handoffs" {
			item.CurrentAuthority = "cairnline"
			item.CairnlineState = "authoritative_opt_in"
			item.LiveMirror = true
			item.BlocksAuthority = false
			item.Gap = ""
			item.Detail = "Handoff create, update, status, and delete mutations commit to the embedded Cairnline database first, then best-effort shadow portable handoff state back into Hecate-native stores for compatibility."
		}
		if projectMemoryAuthoritative && item.Name == "project-memory" {
			item.CurrentAuthority = "cairnline"
			item.CairnlineState = "authoritative_opt_in"
			item.LiveMirror = true
			item.BlocksAuthority = false
			item.Gap = ""
			item.Detail = "Accepted project memory entry mutations commit to the embedded Cairnline database first, validating project identity from Cairnline when no Hecate-native project row exists, then best-effort shadow durable memory state back into Hecate-native stores for compatibility."
		}
		if memoryCandidatesAuthoritative && item.Name == "memory-candidates" {
			item.CurrentAuthority = "cairnline"
			item.CairnlineState = "authoritative_opt_in"
			item.LiveMirror = true
			item.BlocksAuthority = false
			item.Gap = ""
			item.Detail = "Project memory-candidate create, promote, and reject mutations commit to the embedded Cairnline database first, validating project identity from Cairnline when no Hecate-native project row exists, then best-effort shadow review state and promoted-memory references back into Hecate-native stores for compatibility."
		}
		item.Seams = append([]string(nil), item.Seams...)
		out = append(out, item)
	}
	return out
}

func projectCairnlineWriteAuthorityEnabled(items []string, name string) bool {
	name = strings.TrimSpace(name)
	for _, item := range items {
		if item == name {
			return true
		}
	}
	return false
}

func projectCairnlineProjectMemoryWriteWarning(writeAuthority []string) string {
	if projectCairnlineWriteAuthorityEnabled(writeAuthority, "project-memory") && projectCairnlineWriteAuthorityEnabled(writeAuthority, "memory-candidates") {
		return "Accepted project memory entry and memory-candidate mutations are opt-in Cairnline-authoritative, can validate project identity from embedded Cairnline when no Hecate-native project row exists, and are then best-effort shadowed into Hecate-native stores; candidate promotion also creates accepted memory through Cairnline."
	}
	if projectCairnlineWriteAuthorityEnabled(writeAuthority, "project-memory") {
		return "Accepted project memory entry mutations are opt-in Cairnline-authoritative, can validate project identity from embedded Cairnline when no Hecate-native project row exists, and are then best-effort shadowed into Hecate-native stores; memory-candidate mutations still write Hecate-native stores first, then mirror reviewable candidate state into Cairnline."
	}
	return "Project memory entry and memory-candidate mutations still write Hecate-native stores first, then best-effort mirror accepted memory and reviewable candidate state into Cairnline."
}

func projectCairnlineProjectCollaborationWriteWarning(writeAuthority []string) string {
	if projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectCollaboration) {
		return "Project collaboration artifact creation and handoff mutations are opt-in Cairnline-authoritative and then best-effort shadowed into Hecate-native stores for compatibility."
	}
	return "Project collaboration artifact creation and handoff mutations still write Hecate-native stores first, then best-effort mirror portable collaboration metadata into Cairnline."
}

func projectCairnlineProjectMetadataDefaultsWriteWarning(writeAuthority []string) string {
	if projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectMetadataDefaults) {
		return "Project metadata/default-only update mutations are opt-in Cairnline-authoritative and then best-effort shadowed into Hecate-native stores for compatibility; project identity, roots, context sources, and mixed root/source replacement routes are controlled by separate switchpoints."
	}
	return "Project metadata/default updates still write Hecate-native stores first, then best-effort mirror through Cairnline's project-metadata and project-defaults seams."
}

func projectCairnlineProjectIdentityWriteWarning(writeAuthority []string) string {
	if projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectIdentity) {
		return "Project create/delete mutations are opt-in Cairnline-authoritative and then best-effort shadowed into Hecate-native stores for compatibility; delete restores the Cairnline snapshot if Hecate compatibility cleanup fails."
	}
	return "Project create/delete still write Hecate-native stores first, then best-effort mirror portable project identity into the embedded Cairnline database."
}

func projectCairnlineProjectRootWriteWarning(writeAuthority []string) string {
	if projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectRoots) {
		return "Project root create/update/delete, root list replacement, discovery-result replacement, and worktree-created root record mutations are opt-in Cairnline-authoritative and then best-effort shadowed into Hecate-native stores for compatibility; discovery and worktree-created root record mutations can resolve project identity and roots from the embedded Cairnline graph, while Hecate still performs root discovery scans and Git worktree creation side effects."
	}
	return "Root create/update/delete, root list replacement, root discovery, and worktree-created root record mutations still write Hecate-native stores first, then best-effort mirror through Cairnline's root-level API; Hecate owns the Git worktree creation side effect."
}

func projectCairnlineProjectContextSourceWriteWarning(writeAuthority []string) string {
	if projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectContextSources) {
		return "Context-source create/update/delete, list replacement, and discovery-result replacement mutations are opt-in Cairnline-authoritative and then best-effort shadowed into Hecate-native stores for compatibility; discovery can resolve project identity, roots, and existing sources from the embedded Cairnline graph, while Hecate still performs the workspace scan for its operator UI."
	}
	return "Direct context-source create/update/delete, context-source list replacement, and discovery mutations still write Hecate-native stores first, then best-effort mirror through Cairnline's source-level API."
}

func projectCairnlineProjectSkillWriteWarning(writeAuthority []string) string {
	if projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectSkills) {
		return "Project skill discovery and metadata updates are opt-in Cairnline-authoritative, can use embedded Cairnline roots/context sources when no Hecate-native project row exists, and are then best-effort shadowed into Hecate-native stores for compatibility."
	}
	return "Project skill discovery and metadata updates still write Hecate-native stores first, then best-effort mirror metadata-only skill records into Cairnline."
}

func projectCairnlineAgentProfileWriteWarning(writeAuthority []string) string {
	if projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityAgentProfiles) {
		return "Agent profile create/update/delete mutations are opt-in Cairnline-authoritative and then best-effort shadowed into Hecate-native stores for compatibility; Cairnline stores portable profile metadata and execution posture as separate records."
	}
	return "Agent profile create/update/delete mutations still write Hecate-native stores first, then best-effort mirror portable profile metadata and execution posture into Cairnline."
}

func projectCairnlineProjectWorkItemWriteWarning(writeAuthority []string) string {
	rolesAuthoritative := projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectRoles)
	workItemsAuthoritative := projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectWorkItems)
	if rolesAuthoritative && workItemsAuthoritative {
		return "Project role and work-item mutations are opt-in Cairnline-authoritative and then best-effort shadowed into Hecate-native stores for compatibility."
	}
	if rolesAuthoritative {
		return "Project role mutations are opt-in Cairnline-authoritative and then best-effort shadowed into Hecate-native stores for compatibility; work-item mutations still write Hecate-native stores first, then mirror portable work-item state into Cairnline."
	}
	if projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectWorkItems) {
		return "Project work-item mutations are opt-in Cairnline-authoritative and then best-effort shadowed into Hecate-native stores for compatibility; role mutations still write Hecate-native stores first, then mirror portable role defaults into Cairnline."
	}
	return "Project role and work-item mutations still write Hecate-native stores first, then best-effort mirror coordination metadata into Cairnline."
}

func projectCairnlineProjectAssignmentWriteWarning(writeAuthority []string) string {
	if projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectAssignments) {
		return "Project assignment create/update/delete record mutations are opt-in Cairnline-authoritative and then best-effort shadowed into Hecate-native stores for compatibility; assignment start remains Hecate-owned and best-effort mirrors committed start and linked-chat reconciliation results."
	}
	return "Project assignment create/update/delete mutations still write Hecate-native stores first, then best-effort mirror coordination metadata into Cairnline; assignment start remains Hecate-owned and best-effort mirrors committed start and linked-chat reconciliation results."
}

func projectCairnlineProjectAssistantProposalWriteWarning(writeAuthority []string) string {
	if projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectAssistantProposals) {
		if projectCairnlineAssistantApplyPortableEffectsAuthoritative(writeAuthority) {
			return "Project Assistant proposal ledger mutations are opt-in Cairnline-authoritative and then best-effort shadowed into Hecate-native stores for compatibility; confirmed apply is mixed-authority when enabled portable actions route through Cairnline."
		}
		return "Project Assistant proposal ledger mutations are opt-in Cairnline-authoritative and then best-effort shadowed into Hecate-native stores for compatibility; confirmed apply side effects still execute through Hecate-owned project mutation services."
	}
	return "Project Assistant proposal draft/propose/apply-attempt ledger mutations still write Hecate-native stores first, then best-effort mirror proposal records into Cairnline."
}

func projectCairnlineProjectAssistantApplyWriteWarning(writeAuthority []string) string {
	if projectCairnlineAssistantApplyPortableEffectsAuthoritative(writeAuthority) {
		return "Project Assistant confirmed apply uses enabled Cairnline authority seams for the portable actions they cover: project create, project metadata/default, root, role, work-item, assignment, handoff, and memory-candidate; chat/runtime effects remain Hecate-owned orchestrator capabilities outside Cairnline core."
	}
	return "Project Assistant confirmed apply side effects still execute through Hecate-owned mutation services, then best-effort mirror committed results into Cairnline."
}

func projectCairnlineAssistantApplyPortableEffectsAuthoritative(writeAuthority []string) bool {
	return projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectIdentity) ||
		projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectMetadataDefaults) ||
		projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectRoots) ||
		projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectRoles) ||
		projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectWorkItems) ||
		projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectAssignments) ||
		projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectCollaboration) ||
		(projectCairnlineWriteAuthorityEnabled(writeAuthority, "project-memory") &&
			projectCairnlineWriteAuthorityEnabled(writeAuthority, "memory-candidates"))
}

func projectCairnlineReadRouteList(routes []string) string {
	return strings.Join(routes, ", ")
}

func projectCairnlineReadSourceDetail(source string) string {
	switch source {
	case "embedded":
		return "Configured read routes require the embedded mirror database and requested project row or proposal record; if the mirror is missing or stale, the route fails loudly instead of falling back to a Hecate snapshot."
	case "sidecar":
		return "Configured read routes call the standalone Cairnline MCP sidecar through the persistent local client; only " + projectCairnlineReadRouteList(projectCairnlineSidecarReadRouteNames) + " use this source."
	case "snapshot":
		return "Configured read routes use the snapshot-seeded in-memory Cairnline bridge projection and do not attempt the embedded mirror database."
	default:
		return "Configured read routes prefer the embedded mirror database when it already contains the requested project or proposal record; otherwise they fall back to the snapshot-seeded in-memory bridge projection."
	}
}

func projectCairnlineReadSourceWarning(source string) string {
	switch source {
	case "embedded":
		return "HECATE_PROJECTS_CAIRNLINE_READ_SOURCE=embedded requires a populated embedded Cairnline mirror database and fails configured read routes when the database, project row, or proposal record is missing."
	case "sidecar":
		return "HECATE_PROJECTS_CAIRNLINE_READ_SOURCE=sidecar routes only " + projectCairnlineReadRouteList(projectCairnlineSidecarReadRouteNames) + " through the standalone Cairnline MCP client; writes remain Hecate-owned unless a separate write-authority switchpoint is enabled."
	case "snapshot":
		return "HECATE_PROJECTS_CAIRNLINE_READ_SOURCE=snapshot keeps configured read routes on the snapshot-seeded in-memory Cairnline bridge projection even when an embedded mirror database exists."
	default:
		return "HECATE_PROJECTS_CAIRNLINE_READ_SOURCE=auto makes configured Cairnline read-model service reads prefer the embedded mirror database when it already contains the requested project or proposal record, and otherwise use a snapshot-seeded in-memory Cairnline bridge projection."
	}
}

func (h *Handler) cairnlineReadModelReadiness() (bool, []string) {
	if h == nil {
		return false, []string{"Hecate handler is not configured."}
	}
	sources := h.cairnlineSnapshotSources()
	missing := make([]string, 0, 7)
	if sources.Projects == nil {
		missing = append(missing, "projects store")
	}
	if sources.AgentProfiles == nil {
		missing = append(missing, "agent profiles store")
	}
	if sources.Skills == nil {
		missing = append(missing, "project skills store")
	}
	if sources.Work == nil {
		missing = append(missing, "project work store")
	}
	if sources.Memory == nil {
		missing = append(missing, "project memory store")
	}
	if sources.MemoryCandidates == nil {
		missing = append(missing, "project memory candidates store")
	}
	if sources.Proposals == nil {
		missing = append(missing, "project assistant proposal store")
	}
	if len(missing) == 0 {
		return true, nil
	}
	warnings := make([]string, 0, len(missing))
	for _, name := range missing {
		warnings = append(warnings, "Cairnline read adapter is missing "+name+".")
	}
	return false, warnings
}
