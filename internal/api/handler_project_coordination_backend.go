package api

import (
	"net/http"
	"strings"
)

const projectCoordinationBackendReadinessURL = "/hecate/v1/projects/{id}/cairnline/read-model"
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
		BlocksAuthority:  true,
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
		BlocksAuthority:  true,
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
		Data:   h.projectCoordinationBackendStatus(),
	})
}

func (h *Handler) projectCoordinationBackendStatus() ProjectCoordinationBackendStatusResponse {
	configured := "hecate"
	storageBackend := ""
	readSource := "auto"
	connector := "embedded"
	if h != nil {
		configured = h.config.ProjectsCoordinationBackend()
		storageBackend = h.config.Projects.Backend
		readSource = h.config.ProjectsCairnlineReadSource()
		connector = h.config.ProjectsCairnlineConnector()
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
		writeAuthority := h.config.ProjectsCairnlineWriteAuthority()
		effectiveWriteAuthority := writeAuthority
		if !connectorReady {
			effectiveWriteAuthority = nil
		}
		response.WriteAdapterSeams = append([]string(nil), projectCairnlineWriteAdapterSeamNames...)
		response.WriteAdapterGaps = projectCairnlineWriteAdapterGapsSnapshot(effectiveWriteAuthority)
		response.WriteSwitchpoints = projectCairnlineWriteSwitchpointsSnapshot(effectiveWriteAuthority)
		response.ReplacementGates = projectCairnlineReplacementGates(readReady, response.WriteAdapterGaps)
		if !connectorReady {
			if h.projectCairnlineSidecarReadRoutesEnabled() {
				response.Status = "cairnline_sidecar_read_routes_ready"
				response.Detail = "Cairnline sidecar is configured as the project read source, so project-list, project-detail, setup-readiness, health, skills, memory, memory-candidate, roles, work-item, assignment-list, assignment-context, launch-readiness, assignment-preflight, artifact-list, handoff-list, activity, closeout-readiness, and operations-brief routes read through the persistent standalone Cairnline MCP client. Other Projects read routes, all writes, and migration remain on Hecate-native stores or existing embedded dogfood paths."
				response.ReadRoutes = append([]string(nil), projectCairnlineSidecarReadRouteNames...)
				response.ReplacementGates = projectCairnlineReplacementGates(false, response.WriteAdapterGaps)
				response.Warnings = []string{
					"Only project-list, project-detail, setup-readiness, health, skills, memory, memory-candidate, roles, work-item, assignment-list, assignment-context, launch-readiness, assignment-preflight, artifact-list, handoff-list, activity, closeout-readiness, and operations-brief use the Cairnline sidecar MCP client in this mode.",
					"Project writes still use Hecate-native stores unless a separate embedded Cairnline authority switchpoint is explicitly enabled.",
					"Full Cairnline read-model replacement remains blocked because sidecar adapters for assistant routes are not wired.",
				}
				return response
			}
			response.Status = "cairnline_connector_not_ready"
			response.Detail = projectCairnlineConnectorDetail(connector) + " Hecate keeps Projects reads and writes on Hecate-native stores in this mode; use HECATE_PROJECTS_CAIRNLINE_CONNECTOR=embedded for the current replacement-readiness dogfood path."
			response.ReplacementGates = projectCairnlineReplacementGates(false, response.WriteAdapterGaps)
			response.Warnings = []string{
				projectCairnlineConnectorWarning(connector),
				"Project reads and writes still use Hecate-native stores.",
			}
			return response
		}
		response.ReadModelSwitchReady = readReady
		if readReady {
			response.ReadRoutes = append([]string(nil), projectCairnlineReadRouteNames...)
			response.Status = "cairnline_read_routes_ready"
			response.Detail = "Cairnline is configured as the future Projects coordination backend, and the project-list, project-detail, setup-readiness, health, skills, memory, memory-candidate, roles, work-item, assignment-list, assignment-context, launch-readiness, assignment-preflight, artifact-list, handoff-list, project-assistant-context, project-assistant-proposal, activity, closeout-readiness, and operations brief read routes are served from the Cairnline read model. " + projectCairnlineReadSourceDetail(readSource) + " Hecate stores remain authoritative until the remaining live read routes, writes, and migration are ready."
			response.Warnings = []string{
				"Only the project-list, project-detail, setup-readiness, health, skills, memory, memory-candidate, roles, work-item, assignment-list, assignment-context, launch-readiness, assignment-preflight, artifact-list, handoff-list, project-assistant-context, project-assistant-proposal, activity, closeout-readiness, and operations brief live read routes use Cairnline.",
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
			return response
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
	return response
}

func projectCairnlineReplacementGates(readRoutesReady bool, writeGaps []string) []ProjectCoordinationBackendReplacementGate {
	writeGate := projectCairnlineWriteAuthorityReplacementGate(writeGaps)
	return []ProjectCoordinationBackendReplacementGate{
		{
			ID:     "read-routes",
			Ready:  readRoutesReady,
			Status: projectReplacementGateStatus(readRoutesReady),
			Detail: "Configured live project read families can be served from Cairnline's projected read model.",
		},
		{
			ID:     "strict-embedded-read-smoke",
			Ready:  false,
			Status: "operator_probe_required",
			Detail: "Run the embedded sync/parity/smoke probes with HECATE_PROJECTS_CAIRNLINE_READ_SOURCE=embedded before treating the mirror database as a cutover candidate.",
		},
		writeGate,
		{
			ID:     "migration-and-rollback",
			Ready:  false,
			Status: "rehearsal_available",
			Detail: "Embedded sync and project export return structured migration rehearsal evidence with rollback notes, but no authoritative Cairnline storage cutover switch exists yet.",
		},
	}
}

func projectCairnlineWriteAuthorityReplacementGate(writeGaps []string) ProjectCoordinationBackendReplacementGate {
	remaining := projectCairnlineWriteSwitchpointGaps(writeGaps)
	gate := ProjectCoordinationBackendReplacementGate{
		ID:     "write-authority-switchpoints",
		Ready:  false,
		Status: "blocked",
		Detail: "Live mutation routes still commit to Hecate-native stores first; Cairnline mirrors are replacement evidence, not write authority.",
	}
	switch {
	case len(remaining) == 0:
		gate.Ready = true
		gate.Status = "ready"
		gate.Detail = "All live mutation switchpoints that have landed are Cairnline-authoritative; migration, rollback, and final cutover still have separate gates."
	case len(remaining) < projectCairnlineWriteSwitchpointGapCount():
		gate.Status = "partial"
		gate.Detail = "Some live mutation switchpoints are Cairnline-authoritative; remaining write gaps: " + strings.Join(remaining, ", ") + "."
	}
	return gate
}

func projectCairnlineWriteSwitchpointGaps(writeGaps []string) []string {
	out := make([]string, 0, len(writeGaps))
	for _, gap := range writeGaps {
		if gap == "migration-cutover" {
			continue
		}
		out = append(out, gap)
	}
	return out
}

func projectCairnlineWriteSwitchpointGapCount() int {
	return len(projectCairnlineWriteAdapterGapNames) - 1
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
	projectAssistantWorkEffectsAuthoritative := projectCairnlineAssistantApplyWorkEffectsAuthoritative(writeAuthority)
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
			item.BlocksAuthority = true
			item.Gap = "roots"
			item.Detail = "Project root create/update/delete, root list replacement, discovery-result replacement, and worktree-created root record mutations commit to the embedded Cairnline database first, then best-effort shadow Hecate's compatibility row; Hecate still performs root discovery scans and Git worktree creation side effects."
		}
		if projectContextSourcesAuthoritative && item.Name == "context-sources" {
			item.CurrentAuthority = "cairnline"
			item.CairnlineState = "authoritative_opt_in"
			item.LiveMirror = true
			item.BlocksAuthority = false
			item.Gap = ""
			item.Detail = "Context-source create/update/delete, list replacement, and discovery-result replacement mutations commit to the embedded Cairnline database first, then best-effort shadow Hecate's compatibility row; Hecate still performs the workspace scan for its operator UI."
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
			item.Detail = "Project skill discovery and update mutations commit metadata-only skill records to the embedded Cairnline database first, then best-effort shadow them back into Hecate-native stores for compatibility."
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
			if projectAssistantWorkEffectsAuthoritative {
				item.Detail = "Project Assistant draft/propose/apply-attempt ledger records commit to the embedded Cairnline database first, then best-effort shadow Hecate's proposal store for compatibility; confirmed apply is mixed-authority when enabled work-family actions route through Cairnline."
			}
		}
		if projectAssistantWorkEffectsAuthoritative && item.Name == "project-assistant-apply-side-effects" {
			item.CurrentAuthority = "mixed"
			item.CairnlineState = "partial_authoritative_via_work_switchpoints"
			item.LiveMirror = true
			item.BlocksAuthority = true
			item.Gap = "project-assistant-apply-side-effects"
			item.Detail = "Project Assistant confirmed apply routes role, work-item, assignment, and handoff actions through the same opt-in Cairnline authority switchpoints when enabled; project/default/chat/memory/runtime side effects still keep apply as a blocking mixed-authority gap."
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
			item.Detail = "Accepted project memory entry mutations commit to the embedded Cairnline database first, then best-effort shadow durable memory state back into Hecate-native stores for compatibility."
		}
		if memoryCandidatesAuthoritative && item.Name == "memory-candidates" {
			item.CurrentAuthority = "cairnline"
			item.CairnlineState = "authoritative_opt_in"
			item.LiveMirror = true
			item.BlocksAuthority = false
			item.Gap = ""
			item.Detail = "Project memory-candidate create, promote, and reject mutations commit to the embedded Cairnline database first, then best-effort shadow review state and promoted-memory references back into Hecate-native stores for compatibility."
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
		return "Accepted project memory entry and memory-candidate mutations are opt-in Cairnline-authoritative and then best-effort shadowed into Hecate-native stores; candidate promotion also creates accepted memory through Cairnline."
	}
	if projectCairnlineWriteAuthorityEnabled(writeAuthority, "project-memory") {
		return "Accepted project memory entry mutations are opt-in Cairnline-authoritative and then best-effort shadowed into Hecate-native stores; memory-candidate mutations still write Hecate-native stores first, then mirror reviewable candidate state into Cairnline."
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
		return "Project root create/update/delete, root list replacement, discovery-result replacement, and worktree-created root record mutations are opt-in Cairnline-authoritative and then best-effort shadowed into Hecate-native stores for compatibility; Hecate still performs root discovery scans and Git worktree creation side effects."
	}
	return "Root create/update/delete, root list replacement, root discovery, and worktree-created root record mutations still write Hecate-native stores first, then best-effort mirror through Cairnline's root-level API; Hecate owns the Git worktree creation side effect."
}

func projectCairnlineProjectContextSourceWriteWarning(writeAuthority []string) string {
	if projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectContextSources) {
		return "Context-source create/update/delete, list replacement, and discovery-result replacement mutations are opt-in Cairnline-authoritative and then best-effort shadowed into Hecate-native stores for compatibility; Hecate still performs the workspace scan for its operator UI."
	}
	return "Direct context-source create/update/delete, context-source list replacement, and discovery mutations still write Hecate-native stores first, then best-effort mirror through Cairnline's source-level API."
}

func projectCairnlineProjectSkillWriteWarning(writeAuthority []string) string {
	if projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectSkills) {
		return "Project skill discovery and metadata updates are opt-in Cairnline-authoritative and then best-effort shadowed into Hecate-native stores for compatibility."
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
		if projectCairnlineAssistantApplyWorkEffectsAuthoritative(writeAuthority) {
			return "Project Assistant proposal ledger mutations are opt-in Cairnline-authoritative and then best-effort shadowed into Hecate-native stores for compatibility; confirmed apply is mixed-authority when enabled work-family actions route through Cairnline."
		}
		return "Project Assistant proposal ledger mutations are opt-in Cairnline-authoritative and then best-effort shadowed into Hecate-native stores for compatibility; confirmed apply side effects still execute through Hecate-owned project mutation services."
	}
	return "Project Assistant proposal draft/propose/apply-attempt ledger mutations still write Hecate-native stores first, then best-effort mirror proposal records into Cairnline."
}

func projectCairnlineProjectAssistantApplyWriteWarning(writeAuthority []string) string {
	if projectCairnlineAssistantApplyWorkEffectsAuthoritative(writeAuthority) {
		return "Project Assistant confirmed apply uses the enabled Cairnline authority seams for role, work-item, assignment, and handoff actions, but project/default/chat/memory/runtime side effects still keep apply as a mixed-authority replacement blocker."
	}
	return "Project Assistant confirmed apply side effects still execute through Hecate-owned mutation services, then best-effort mirror committed results into Cairnline."
}

func projectCairnlineAssistantApplyWorkEffectsAuthoritative(writeAuthority []string) bool {
	return projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectRoles) ||
		projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectWorkItems) ||
		projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectAssignments) ||
		projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectCollaboration)
}

func projectCairnlineReadSourceDetail(source string) string {
	switch source {
	case "embedded":
		return "Configured read routes require the embedded mirror database and requested project row or proposal record; if the mirror is missing or stale, the route fails loudly instead of falling back to a Hecate snapshot."
	case "sidecar":
		return "Configured read routes call the standalone Cairnline MCP sidecar through the persistent local client; only project-list, project-detail, setup-readiness, health, skills, memory, memory-candidate, roles, work-item, assignment-list, assignment-context, launch-readiness, assignment-preflight, artifact-list, handoff-list, activity, closeout-readiness, and operations-brief use this source."
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
		return "HECATE_PROJECTS_CAIRNLINE_READ_SOURCE=sidecar routes only project-list, project-detail, setup-readiness, health, skills, memory, memory-candidate, roles, work-item, assignment-list, assignment-context, launch-readiness, assignment-preflight, artifact-list, handoff-list, activity, closeout-readiness, and operations-brief through the standalone Cairnline MCP client; assistant read routes are not sidecar-backed yet."
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
