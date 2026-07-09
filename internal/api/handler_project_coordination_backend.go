package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/hecatehq/cairnline"
)

const projectCoordinationBackendStatusURL = "/hecate/v1/projects/backend-status"
const projectCoordinationBackendReadinessURL = "/hecate/v1/projects/{id}/cairnline/read-model"
const projectCoordinationBackendExportURL = "/hecate/v1/projects/{id}/cairnline/export"
const projectCoordinationBackendSidecarProbeURL = "/hecate/v1/projects/cairnline/sidecar-probe"
const projectCoordinationBackendSidecarConnectURL = "/hecate/v1/projects/cairnline/sidecar-connect"
const projectCoordinationBackendSidecarReadURL = "/hecate/v1/projects/cairnline/sidecar-read-smoke"
const projectCoordinationBackendSidecarDetailURL = "/hecate/v1/projects/cairnline/sidecar-detail-smoke"
const projectCoordinationBackendSidecarResourceURL = "/hecate/v1/projects/cairnline/sidecar-resource-smoke"
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
	"project-chat-prelude",
	"project-chat-context",
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

var projectCairnlinePortableWriteRecommendationOrder = []string{
	"context-sources",
	"skills",
	"roles",
	"work-items",
	"assignments",
	"artifacts",
	"handoffs",
	"memory",
	"memory-candidates",
	"roots",
	"projects",
	"project-assistant-proposals",
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
		Detail:           "Project metadata and default-root mutations still commit to Hecate first, then mirror through Cairnline metadata/default seams; provider/model/preset posture stays in Hecate runtime overlays.",
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
		Detail:           "Role mutations still commit to Hecate first, then mirror portable coordination metadata into Cairnline while Agent Preset/provider/model posture stays in Hecate runtime overlays.",
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
		Detail:           "Assignment start still dispatches through Hecate runtime/task/external-agent authority; strict embedded starts may claim/progress assignments in Cairnline while Hecate owns runtime refs, cleanup, and conflict handling.",
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
		Detail:           "Memory-candidate create/promote/reject still commits to Hecate first, then mirrors review state and promoted-memory references into Cairnline.",
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
		CairnlineState:   "authoritative_migration_available",
		BlocksAuthority:  true,
		Seams:            []string{"sync-rehearsal"},
		Gap:              "migration-cutover",
		Detail:           "One-way migration endpoint POST /hecate/v1/projects/cairnline/migrate rebuilds and verifies the embedded Cairnline store from native sources; run it to arm cutover.",
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
	var mirrorWriteHealth ProjectCairnlineMirrorWriteHealth
	if h != nil {
		configured = h.config.ProjectsCoordinationBackend()
		storageBackend = h.config.Projects.Backend
		readSource = h.config.ProjectsCairnlineReadSource()
		connector = h.config.ProjectsCairnlineConnector()
		replacementMode = h.config.ProjectsCairnlineReplacementMode()
		mirrorWriteHealth = h.cairnlineMirrorHealth.snapshot()
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
		CairnlineSidecarResourceURL:          projectCoordinationBackendSidecarResourceURL,
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
		MirrorWriteHealth:                    mirrorWriteHealth,
	}
	switch configured {
	case "cairnline":
		readReady, sourceWarnings := h.cairnlineReadModelReadiness()
		strictEmbeddedReadGate, migrationRehearsal := h.projectCairnlineStrictEmbeddedReadSmokeReplacementGate(ctx, connectorReady, readSource, readReady, replacementMode)
		response.MigrationRehearsal = migrationRehearsal
		writeAuthority := h.config.ProjectsCairnlineWriteAuthority()
		effectiveWriteAuthority := writeAuthority
		if !connectorReady {
			effectiveWriteAuthority = nil
		}
		response.WriteAdapterSeams = append([]string(nil), projectCairnlineWriteAdapterSeamNames...)
		writeAdapterGaps := projectCairnlineWriteAdapterGapsSnapshot(effectiveWriteAuthority)
		response.PortableWriteGaps = projectCairnlinePortableWriteGapsSnapshot(effectiveWriteAuthority, writeAdapterGaps)
		response.OrchestratorCapabilities = projectCairnlineOrchestratorCapabilitiesSnapshot(writeAdapterGaps)
		response.WriteAdapterReady = len(response.PortableWriteGaps) == 0
		migrationCutoverArmed := replacementMode == "embedded" && strictEmbeddedReadGate.Ready && projectCairnlineMigrationRollbackEvidenceReady(migrationRehearsal) && len(response.PortableWriteGaps) == 0
		// A durable migration record moves migration-cutover from a declared gap
		// to an implemented, evidenced capability independent of the armed
		// replacement-mode heuristic.
		migrationCutover := h.projectCairnlineMigrationCutoverStatus(h.cairnlineEmbeddedDatabasePath())
		response.MigrationCutover = migrationCutover
		migrationExecutedVerified := migrationCutover != nil && migrationCutover.Status == "migrated_verified"
		response.WriteAdapterGaps = projectCairnlineWriteAdapterGapsAfterMigrationCutover(writeAdapterGaps, migrationCutoverArmed, migrationExecutedVerified)
		nativeShadowSkipArmed := replacementMode == "embedded" && len(response.PortableWriteGaps) == 0
		response.WriteSwitchpoints = projectCairnlineWriteSwitchpointsSnapshot(effectiveWriteAuthority, nativeShadowSkipArmed, migrationCutoverArmed, migrationCutover)
		response.MigrationBlockers = projectCairnlineMigrationBlockersSnapshot(response.WriteAdapterGaps, migrationCutoverArmed, migrationExecutedVerified)
		response.ReplacementGates = projectCairnlineReplacementGates(readReady, response.PortableWriteGaps, replacementMode, strictEmbeddedReadGate, migrationRehearsal, migrationCutoverArmed, mirrorWriteHealth, migrationCutover)
		if !connectorReady {
			if h.projectCairnlineSidecarReadRoutesEnabled() {
				response.Status = "cairnline_sidecar_read_routes_ready"
				response.Detail = "Cairnline sidecar is configured as the project read source, so " + projectCairnlineReadRouteList(projectCairnlineSidecarReadRouteNames) + " routes read through the persistent standalone Cairnline MCP client. Project writes and migration remain on Hecate-native stores or existing embedded dogfood paths."
				response.ReadRoutes = append([]string(nil), projectCairnlineSidecarReadRouteNames...)
				response.ReplacementGates = projectCairnlineReplacementGates(false, response.PortableWriteGaps, replacementMode, strictEmbeddedReadGate, migrationRehearsal, migrationCutoverArmed, mirrorWriteHealth, migrationCutover)
				response.Warnings = []string{
					"Only " + projectCairnlineReadRouteList(projectCairnlineSidecarReadRouteNames) + " use the Cairnline sidecar MCP client in this mode.",
					projectCairnlineSidecarWriteAuthorityWarning(writeAuthority),
					"Full Cairnline replacement remains blocked on authoritative write migration.",
				}
				return projectCairnlineStatusWithNextAction(response, effectiveWriteAuthority)
			}
			response.Status = "cairnline_connector_not_ready"
			response.Detail = projectCairnlineConnectorDetail(connector) + " Hecate keeps Projects reads and writes on Hecate-native stores in this mode; use HECATE_PROJECTS_CAIRNLINE_CONNECTOR=embedded for the current replacement-readiness dogfood path."
			response.ReplacementGates = projectCairnlineReplacementGates(false, response.PortableWriteGaps, replacementMode, strictEmbeddedReadGate, migrationRehearsal, migrationCutoverArmed, mirrorWriteHealth, migrationCutover)
			response.Warnings = []string{
				projectCairnlineConnectorWarning(connector),
				projectCairnlineSidecarWriteAuthorityWarning(writeAuthority),
			}
			return projectCairnlineStatusWithNextAction(response, effectiveWriteAuthority)
		}
		response.ReadModelSwitchReady = readReady
		if readReady {
			response.ReadRoutes = append([]string(nil), projectCairnlineReadRouteNames...)
			response.Status = "cairnline_read_routes_ready"
			response.Detail = projectCairnlineReadRoutesReadyDetail(readSource, response.WriteAdapterReady, replacementMode)
			response.Warnings = []string{
				"Only the " + projectCairnlineReadRouteList(projectCairnlineReadRouteNames) + " live read routes use Cairnline.",
				projectCairnlineReadSourceWarning(readSource),
				projectCairnlineProjectIdentityWriteWarning(effectiveWriteAuthority, nativeShadowSkipArmed),
				projectCairnlineProjectMetadataDefaultsWriteWarning(effectiveWriteAuthority, nativeShadowSkipArmed),
				projectCairnlineProjectRootWriteWarning(effectiveWriteAuthority, nativeShadowSkipArmed),
				projectCairnlineProjectContextSourceWriteWarning(effectiveWriteAuthority, nativeShadowSkipArmed),
				projectCairnlineProjectSkillWriteWarning(effectiveWriteAuthority),
				projectCairnlineProjectWorkItemWriteWarning(effectiveWriteAuthority),
				projectCairnlineProjectAssignmentWriteWarning(effectiveWriteAuthority),
				projectCairnlineProjectCollaborationWriteWarning(effectiveWriteAuthority),
				projectCairnlineProjectMemoryWriteWarning(effectiveWriteAuthority),
				projectCairnlineProjectAssistantProposalWriteWarning(effectiveWriteAuthority, nativeShadowSkipArmed),
				projectCairnlineProjectAssistantApplyWriteWarning(effectiveWriteAuthority),
			}
			if response.WriteAdapterReady {
				response.Warnings = append(response.Warnings,
					"Portable project-state write authority is Cairnline-ready; Hecate-owned runtime/workspace side effects and migration/rollback gates remain separate.",
				)
			} else {
				response.Warnings = append(response.Warnings,
					"Other project mutation routes still write only Hecate-native stores.",
					"Cairnline write-adapter seams are non-authoritative proofs; live write authority and migration path are not ready.",
				)
			}
			return projectCairnlineStatusWithNextAction(response, effectiveWriteAuthority)
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
	return projectCairnlineStatusWithNextAction(response, nil)
}

func projectCairnlineStatusWithNextAction(response ProjectCoordinationBackendStatusResponse, writeAuthority []string) ProjectCoordinationBackendStatusResponse {
	response.ReplacementReady = projectCairnlineReplacementGatesReady(response.ReplacementGates)
	if response.ReplacementReady {
		response.AuthoritativeBackend = "cairnline"
		response.CairnlineAuthoritative = true
		response.Status = "cairnline_authoritative"
		response.Detail = projectCairnlineReplacementReadyDetail()
		response.Warnings = projectCairnlineReplacementReadyWarnings(response.OrchestratorCapabilities)
	}
	response.NextReplacementAction = projectCairnlineNextReplacementAction(response, writeAuthority)
	projectCairnlineHydrateProbeMetadata(&response)
	return response
}

func projectCairnlineHydrateProbeMetadata(response *ProjectCoordinationBackendStatusResponse) {
	if response == nil {
		return
	}
	for i := range response.ReplacementGates {
		if len(response.ReplacementGates[i].Probes) == 0 {
			response.ReplacementGates[i].Probes = projectCairnlineProbesForURLs(response.ReplacementGates[i].ProbeURLs)
		}
	}
	if response.NextReplacementAction != nil && len(response.NextReplacementAction.Probes) == 0 {
		response.NextReplacementAction.Probes = projectCairnlineProbesForURLs(response.NextReplacementAction.ProbeURLs)
	}
	if response.NextReplacementAction != nil && strings.TrimSpace(response.NextReplacementAction.ConfigBlock) == "" {
		response.NextReplacementAction.ConfigBlock = projectCairnlineConfigBlock(response.NextReplacementAction.ConfigHints)
	}
}

func projectCairnlineConfigBlock(hints []ProjectCoordinationBackendActionConfigHint) string {
	if len(hints) == 0 {
		return ""
	}
	lines := make([]string, 0, len(hints))
	for _, hint := range hints {
		env := strings.TrimSpace(hint.Env)
		if env == "" {
			continue
		}
		lines = append(lines, env+"="+hint.Value)
	}
	return strings.Join(lines, "\n")
}

func projectCairnlineProbesForURLs(urls []string) []ProjectCoordinationBackendProbe {
	if len(urls) == 0 {
		return nil
	}
	probes := make([]ProjectCoordinationBackendProbe, 0, len(urls))
	for _, url := range urls {
		url = strings.TrimSpace(url)
		if url == "" {
			continue
		}
		probes = append(probes, ProjectCoordinationBackendProbe{
			Method: projectCairnlineProbeMethod(url),
			URL:    url,
		})
	}
	return probes
}

func projectCairnlineProbeMethod(url string) string {
	switch strings.TrimSpace(url) {
	case projectCoordinationBackendSyncReadinessURL,
		projectCoordinationBackendExportURL,
		projectCoordinationBackendSidecarProbeURL,
		projectCoordinationBackendSidecarConnectURL,
		projectCoordinationBackendSidecarReadURL,
		projectCoordinationBackendSidecarDetailURL,
		projectCoordinationBackendSidecarResourceURL,
		projectCoordinationBackendSidecarCoordinationURL,
		projectCoordinationBackendSidecarAssignmentContextURL,
		projectCoordinationBackendSidecarLaunchPacketURL,
		projectCoordinationBackendSidecarLifecycleURL,
		projectCoordinationBackendSidecarSetupURL,
		projectCoordinationBackendSidecarWriteURL,
		projectCoordinationBackendSidecarWorkURL,
		projectCoordinationBackendSidecarCollaborationURL,
		projectCoordinationBackendSidecarMemoryURL,
		projectCoordinationBackendSidecarAssistantURL:
		return http.MethodPost
	default:
		return http.MethodGet
	}
}

func projectCairnlineReplacementReadyDetail() string {
	return "All Cairnline replacement gates are ready and embedded replacement mode is armed; Hecate is reporting Cairnline as authoritative for portable Projects coordination state."
}

func projectCairnlineReplacementReadyWarnings(orchestratorCapabilities []string) []string {
	warnings := []string{
		"Hecate still owns runtime/workspace side effects such as task/chat execution, External Agent supervision, approvals, traces, root discovery, and Git worktree creation.",
	}
	if len(orchestratorCapabilities) > 0 {
		warnings = append(warnings, "Remaining Hecate-owned orchestrator capabilities: "+strings.Join(orchestratorCapabilities, ", ")+".")
	}
	return warnings
}

func projectCairnlineNextReplacementAction(status ProjectCoordinationBackendStatusResponse, writeAuthority []string) *ProjectCoordinationBackendNextAction {
	if status.ReplacementReady {
		return &ProjectCoordinationBackendNextAction{
			ID:     "monitor-cairnline-backend",
			Label:  "Monitor Cairnline backend",
			Detail: "All Cairnline replacement gates are ready and embedded replacement mode is armed; Projects are reporting Cairnline as authoritative.",
			Target: "cairnline",
		}
	}
	if status.ConfiguredBackend != "cairnline" {
		return &ProjectCoordinationBackendNextAction{
			ID:     "enable-cairnline-dogfood",
			Label:  "Enable Cairnline dogfood",
			Detail: "Configure Cairnline as the project coordination backend in a local dogfood runtime before moving any authority.",
			Target: "configuration",
			ConfigHints: append([]ProjectCoordinationBackendActionConfigHint{
				projectCairnlineConfigHint("HECATE_PROJECTS_COORDINATION_BACKEND", "cairnline", "Enable Cairnline as the Projects coordination backend for local dogfooding."),
			}, projectCairnlineEmbeddedDogfoodHints(status.CairnlineReadSource)...),
			ProbeURLs: []string{
				projectCoordinationBackendStatusURL,
				projectCoordinationBackendReadinessURL,
				projectCoordinationBackendEmbeddedReadModelURL,
				projectCoordinationBackendEmbeddedParityReportURL,
			},
		}
	}
	if !status.CairnlineConnectorReady {
		return &ProjectCoordinationBackendNextAction{
			ID:          "use-embedded-cairnline-connector",
			Label:       "Use the embedded Cairnline connector",
			Detail:      "Sidecar mode is useful for MCP diagnostics and read smoke tests, but write-authority dogfood currently requires the embedded Cairnline connector.",
			Target:      "connector",
			ConfigHints: projectCairnlineEmbeddedDogfoodHints(status.CairnlineReadSource),
			ProbeURLs: []string{
				projectCoordinationBackendStatusURL,
				projectCoordinationBackendReadinessURL,
				projectCoordinationBackendEmbeddedReadModelURL,
				projectCoordinationBackendEmbeddedParityReportURL,
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
		target := projectCairnlineNextPortableWriteGap(status.PortableWriteGaps)
		return &ProjectCoordinationBackendNextAction{
			ID:          "move-portable-write-authority",
			Label:       "Move the next portable write authority",
			Detail:      "Close the next recommended portable project-state gap by adding a Cairnline-authoritative switchpoint while keeping Hecate as compatibility shadow.",
			Target:      target,
			ConfigHints: projectCairnlineWriteAuthorityHintsForGapWithExisting(target, writeAuthority),
		}
	}
	if gate := projectCoordinationBackendReplacementGateByID(status.ReplacementGates, "strict-embedded-read-smoke"); gate != nil && !gate.Ready {
		if status.ReplacementMode == "embedded" {
			return &ProjectCoordinationBackendNextAction{
				ID:        "run-strict-embedded-read-smoke",
				Label:     "Run strict embedded read smoke",
				Detail:    "Portable project-state writes are Cairnline-authoritative; rerun backend status to smoke live embedded Cairnline project state before treating the backend as fully delegated.",
				Target:    "strict-embedded-read-smoke",
				ProbeURLs: append([]string(nil), gate.ProbeURLs...),
			}
		}
		return &ProjectCoordinationBackendNextAction{
			ID:          "run-strict-embedded-read-smoke",
			Label:       "Run strict embedded read smoke",
			Detail:      "Portable write authority is clear; verify the embedded Cairnline mirror and strict read-smoke evidence before treating migration cutover as the next blocker.",
			Target:      "strict-embedded-read-smoke",
			ConfigHints: projectCairnlineMigrationCutoverHints(),
			ProbeURLs:   append([]string(nil), gate.ProbeURLs...),
		}
	}
	if len(status.MigrationBlockers) > 0 {
		if gate := projectCoordinationBackendReplacementGateByID(status.ReplacementGates, "migration-and-rollback"); gate != nil && gate.Status == "cutover_switch_missing" {
			return &ProjectCoordinationBackendNextAction{
				ID:          "implement-migration-cutover",
				Label:       "Implement migration cutover",
				Detail:      "Strict embedded mirror parity and read smoke are verified; the remaining migration blocker is an explicit authoritative cutover and rollback switch.",
				Target:      status.MigrationBlockers[0],
				ConfigHints: projectCairnlineReplacementModeHints(),
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

func projectCairnlineNextPortableWriteGap(gaps []string) string {
	remaining := make(map[string]struct{}, len(gaps))
	for _, gap := range gaps {
		gap = strings.TrimSpace(gap)
		if gap == "" {
			continue
		}
		remaining[gap] = struct{}{}
	}
	for _, candidate := range projectCairnlinePortableWriteRecommendationOrder {
		if _, ok := remaining[candidate]; ok {
			return candidate
		}
	}
	for _, gap := range gaps {
		if strings.TrimSpace(gap) != "" {
			return gap
		}
	}
	return ""
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
		projectCairnlineConfigHint("HECATE_PROJECTS_CAIRNLINE_REPLACEMENT_MODE", "embedded", "Arm the explicit embedded Cairnline replacement contract after read, migration, and rollback gates are ready; portable write authority is implied by this mode."),
	}
}

func projectCairnlineMigrationCutoverHints() []ProjectCoordinationBackendActionConfigHint {
	return []ProjectCoordinationBackendActionConfigHint{
		projectCairnlineConfigHint("HECATE_PROJECTS_CAIRNLINE_CONNECTOR", "embedded", "Use the embedded connector for current write-authority and migration rehearsal paths."),
		projectCairnlineConfigHint("HECATE_PROJECTS_CAIRNLINE_READ_SOURCE", "embedded", "Force configured project reads to fail loudly when the embedded mirror is missing or stale during cutover rehearsal."),
	}
}

func projectCairnlineEmbeddedDogfoodHints(readSource string) []ProjectCoordinationBackendActionConfigHint {
	hints := []ProjectCoordinationBackendActionConfigHint{
		projectCairnlineConfigHint("HECATE_PROJECTS_CAIRNLINE_CONNECTOR", "embedded", "Use the embedded connector for current write-authority and embedded read-model dogfood paths."),
	}
	if readSource == "sidecar" {
		hints = append(hints, projectCairnlineConfigHint("HECATE_PROJECTS_CAIRNLINE_READ_SOURCE", "embedded", "Switch project reads back to the embedded read source before running embedded dogfood status/read-model probes."))
	}
	return hints
}

func projectCairnlineConfigHint(env, value, detail string) ProjectCoordinationBackendActionConfigHint {
	return ProjectCoordinationBackendActionConfigHint{
		Env:    env,
		Value:  value,
		Detail: detail,
	}
}

func projectCairnlineWriteAuthorityHintsForGap(gap string) []ProjectCoordinationBackendActionConfigHint {
	return projectCairnlineWriteAuthorityHintsForGapWithExisting(gap, nil)
}

func projectCairnlineWriteAuthorityHintsForGapWithExisting(gap string, existing []string) []ProjectCoordinationBackendActionConfigHint {
	values := projectCairnlineWriteAuthorityValuesForGap(gap)
	if len(values) == 0 {
		return nil
	}
	nextValue := projectCairnlineMergedWriteAuthorityValue(existing, values)
	detail := "Add this value to HECATE_PROJECTS_CAIRNLINE_WRITE_AUTHORITY for embedded Cairnline write-authority dogfooding."
	if len(values) > 1 {
		detail = "Add these comma-separated values to HECATE_PROJECTS_CAIRNLINE_WRITE_AUTHORITY; this gap requires the switchpoints together."
	}
	if len(existing) > 0 {
		detail = "Set HECATE_PROJECTS_CAIRNLINE_WRITE_AUTHORITY to this additive comma-separated value; it preserves already-enabled Cairnline write-authority switchpoints."
	}
	return []ProjectCoordinationBackendActionConfigHint{
		projectCairnlineConfigHint("HECATE_PROJECTS_CAIRNLINE_WRITE_AUTHORITY", nextValue, detail),
	}
}

func projectCairnlineMergedWriteAuthorityValue(existing, values []string) string {
	out := make([]string, 0, len(existing))
	seen := make(map[string]struct{}, len(existing))
	appendValue := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || value == "none" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	for _, value := range existing {
		appendValue(value)
	}
	for _, value := range values {
		appendValue(value)
	}
	return strings.Join(out, ",")
}

func projectCairnlineWriteAuthorityValuesForGap(gap string) []string {
	switch gap {
	case "projects":
		return []string{projectCairnlineWriteAuthorityProjectIdentity, projectCairnlineWriteAuthorityProjectMetadataDefaults}
	case "roots":
		return []string{projectCairnlineWriteAuthorityProjectRoots}
	case "context-sources":
		return []string{projectCairnlineWriteAuthorityProjectContextSources}
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

func projectCairnlineReplacementGates(readRoutesReady bool, portableWriteGaps []string, replacementMode string, strictEmbeddedReadGate ProjectCoordinationBackendReplacementGate, migrationRehearsal *ProjectCairnlineMigrationRehearsal, migrationCutoverArmed bool, mirrorWriteHealth ProjectCairnlineMirrorWriteHealth, migrationCutover *ProjectCairnlineMigrationCutoverStatus) []ProjectCoordinationBackendReplacementGate {
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
		projectCairnlineMirrorWriteHealthGate(mirrorWriteHealth),
		projectCairnlineMigrationRollbackReplacementGate(strictEmbeddedReadGate, migrationRehearsal, migrationCutoverArmed, migrationCutover),
		projectCairnlineReplacementModeGate(replacementMode),
	}
}

// projectCairnlineMirrorWriteHealthGate turns live shadow-mirror failure
// evidence into a replacement gate. A backend whose portable write families
// have failed to mirror since their last success is demonstrably behind
// Hecate's stores, so replacement readiness must not be reported while that
// evidence is outstanding.
func projectCairnlineMirrorWriteHealthGate(health ProjectCairnlineMirrorWriteHealth) ProjectCoordinationBackendReplacementGate {
	gate := ProjectCoordinationBackendReplacementGate{
		ID:        "mirror-write-health",
		Ready:     true,
		Status:    "healthy",
		Detail:    "No Cairnline shadow-mirror write failures have been recorded in this runtime.",
		ProbeURLs: []string{projectCoordinationBackendStatusURL, projectCoordinationBackendMirrorParityURL},
	}
	if len(health.DriftingFamilies) > 0 {
		gate.Ready = false
		gate.Status = "drifting"
		gate.Detail = fmt.Sprintf("Cairnline shadow-mirror writes recorded %d failure(s) in this runtime, and these write families have not mirrored successfully since their last failure: %s. Resolve the mirror errors or resync before treating the mirror as replacement evidence.", health.TotalFailureCount, strings.Join(health.DriftingFamilies, ", "))
		return gate
	}
	if health.TotalFailureCount > 0 {
		gate.Status = "recovered"
		gate.Detail = fmt.Sprintf("Cairnline shadow-mirror writes recorded %d failure(s) in this runtime, but every affected write family has mirrored successfully since its last failure.", health.TotalFailureCount)
	}
	return gate
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

func (h *Handler) projectCairnlineStrictEmbeddedReadSmokeReplacementGate(ctx context.Context, connectorReady bool, readSource string, readRoutesReady bool, replacementMode string) (ProjectCoordinationBackendReplacementGate, *ProjectCairnlineMigrationRehearsal) {
	gate := projectCairnlineStrictEmbeddedReadSmokeDefaultGate()
	if !connectorReady {
		gate.Status = "blocked"
		gate.Detail = "Strict embedded read smoke requires the embedded Cairnline connector; configure HECATE_PROJECTS_CAIRNLINE_CONNECTOR=embedded before using the embedded mirror as a replacement candidate."
		return gate, nil
	}
	if readSource != "embedded" {
		gate.Detail = "Set HECATE_PROJECTS_CAIRNLINE_READ_SOURCE=embedded and run the embedded sync/parity/smoke probes before treating the mirror database as a cutover candidate."
		return gate, nil
	}
	if !readRoutesReady {
		gate.Status = "blocked"
		gate.Detail = "Strict embedded read smoke cannot run until the Cairnline read adapter can load the full Hecate project graph."
		return gate, nil
	}
	if strings.TrimSpace(h.config.Server.DataDir) == "" {
		gate.Detail = "Run backend status with a configured Hecate data directory, then run the embedded sync/parity/smoke probes with HECATE_PROJECTS_CAIRNLINE_READ_SOURCE=embedded before treating the mirror database as a cutover candidate."
		return gate, nil
	}
	if replacementMode == "embedded" {
		gate.ProbeURLs = []string{projectCoordinationBackendStatusURL}
		dbPath, smoke, err := h.projectCairnlineStrictEmbeddedLiveSmoke(ctx)
		if err != nil {
			migrationRehearsal := projectCairnlineEmbeddedReplacementModeRehearsal(dbPath, false, nil)
			if errors.Is(err, cairnline.ErrNotFound) {
				gate.Status = "not_run"
				gate.Detail = "No embedded Cairnline replacement database exists yet; create or import portable project state before treating Cairnline as the authoritative Projects backend."
				return gate, &migrationRehearsal
			}
			gate.Status = "probe_error"
			gate.Detail = "Strict embedded live read smoke could not inspect embedded Cairnline state: " + err.Error()
			return gate, &migrationRehearsal
		}
		migrationRehearsal := projectCairnlineEmbeddedReplacementModeRehearsal(dbPath, true, smoke)
		if smoke == nil || smoke.Status != "passed" {
			gate.Status = "failed"
			if smoke != nil && smoke.Status != "" {
				gate.Status = smoke.Status
			}
			errorCount := 0
			if smoke != nil {
				errorCount = len(smoke.Errors)
			}
			gate.Detail = fmt.Sprintf("Embedded Cairnline replacement-mode live read smoke reported %s with %d error(s).", gate.Status, errorCount)
			return gate, &migrationRehearsal
		}
		gate.Ready = true
		gate.Status = "verified"
		gate.Detail = fmt.Sprintf("Embedded Cairnline replacement-mode live read smoke passed across %d project(s) and %d route check(s).", smoke.ProjectCount, smoke.ReadRouteChecks)
		return gate, &migrationRehearsal
	}
	item, err := h.projectCairnlineMirrorParity(ctx)
	if err != nil {
		gate.Status = "probe_error"
		gate.Detail = "Strict embedded read smoke could not inspect the embedded Cairnline mirror: " + err.Error()
		return gate, nil
	}
	migrationRehearsal := item.MigrationRehearsal
	if !item.DatabaseExists {
		gate.Status = "not_run"
		gate.Detail = "No embedded Cairnline mirror database exists yet; run " + projectCoordinationBackendSyncReadinessURL + " and then " + projectCoordinationBackendMirrorParityURL + "."
		return gate, &migrationRehearsal
	}
	if !item.Match {
		gate.Status = "drift_detected"
		gate.Detail = "The embedded Cairnline mirror exists but no longer matches Hecate's authoritative stores; rerun the sync and mirror-parity probes before cutover."
		return gate, &migrationRehearsal
	}
	smoke := item.MigrationRehearsal.EmbeddedSmoke
	if smoke == nil {
		gate.Status = "operator_probe_required"
		gate.Detail = "The embedded Cairnline mirror matches Hecate stores, but strict embedded read smoke evidence is missing; rerun the mirror-parity probe."
		return gate, &migrationRehearsal
	}
	if smoke.Status != "passed" {
		gate.Status = smoke.Status
		if gate.Status == "" {
			gate.Status = "failed"
		}
		gate.Detail = fmt.Sprintf("The embedded Cairnline mirror matches Hecate stores, but strict embedded read smoke reported %s with %d error(s).", gate.Status, len(smoke.Errors))
		return gate, &migrationRehearsal
	}
	gate.Ready = true
	gate.Status = "verified"
	gate.Detail = fmt.Sprintf("Existing embedded Cairnline mirror matches Hecate stores and strict embedded read smoke passed across %d project(s) and %d route check(s).", smoke.ProjectCount, smoke.ReadRouteChecks)
	return gate, &migrationRehearsal
}

func projectCairnlineEmbeddedReplacementModeRehearsal(dbPath string, databaseExists bool, smoke *ProjectCairnlineMigrationEmbeddedSmoke) ProjectCairnlineMigrationRehearsal {
	status := "not_run"
	if databaseExists {
		status = "failed"
		if smoke != nil && smoke.Status != "" {
			status = smoke.Status
		}
		if smoke != nil && smoke.Status == "passed" {
			status = "verified"
		}
	}
	return ProjectCairnlineMigrationRehearsal{
		Operation:       "embedded_replacement_smoke",
		ImportMode:      "embedded_cairnline_live",
		SnapshotVersion: cairnline.SnapshotVersion,
		SourceAuthority: "embedded_cairnline_authoritative",
		Target:          "embedded_cairnline_sqlite",
		RefreshesTarget: false,
		Authoritative:   databaseExists,
		CutoverReady:    status == "verified",
		Status:          status,
		Checklist: []ProjectCairnlineMigrationRehearsalCheck{
			{
				ID:     "open-embedded-cairnline",
				Status: projectCairnlineMigrationCheckStatus(databaseExists, "complete", "not_run"),
				Detail: "Opened the embedded Cairnline SQLite database that now owns portable project state in replacement mode.",
			},
			{
				ID:     "strict-embedded-read-smoke",
				Status: projectCairnlineMigrationSmokeStatus(smoke),
				Detail: "Exercised Hecate's configured strict embedded read routes against live Cairnline project state.",
			},
			{
				ID:     "rollback-plan",
				Status: "documented",
				Detail: "Rollback disables embedded replacement mode and keeps Hecate runtime/workspace side effects on Hecate-owned overlays.",
			},
			{
				ID:     "authoritative-switchpoint",
				Status: projectCairnlineMigrationCheckStatus(status == "verified", "complete", "blocked"),
				Detail: "HECATE_PROJECTS_CAIRNLINE_REPLACEMENT_MODE=embedded is the explicit operator cutover switch for portable project state.",
			},
		},
		Rollback: []string{
			"Disable HECATE_PROJECTS_CAIRNLINE_REPLACEMENT_MODE=embedded to stop reporting Cairnline as the authoritative Projects backend.",
			"Keep Hecate-owned runtime/workspace side effects such as task/chat execution, approvals, traces, root discovery, and Git worktree creation on Hecate overlays.",
			"Restore or replace the embedded Cairnline SQLite database at " + strings.TrimSpace(dbPath) + " if portable project state must be rolled back.",
		},
		EmbeddedSmoke: smoke,
	}
}

func projectCairnlineMigrationRollbackReplacementGate(strictEmbeddedReadGate ProjectCoordinationBackendReplacementGate, migrationRehearsal *ProjectCairnlineMigrationRehearsal, migrationCutoverArmed bool, migrationCutover *ProjectCairnlineMigrationCutoverStatus) (gate ProjectCoordinationBackendReplacementGate) {
	migrationExecutedVerified := migrationCutover != nil && migrationCutover.Status == "migrated_verified"
	// A verified authoritative migration is durable, executed evidence, so it
	// enriches the gate detail at every exit without weakening the armed-mode
	// readiness semantics.
	if migrationExecutedVerified {
		defer func() {
			backup := strings.TrimSpace(migrationCutover.RollbackBackupPath)
			note := " An authoritative one-way migration has been executed and verified with parity matched"
			if backup != "" {
				note += " and a rollback backup at " + backup + "."
			} else {
				// First migration with no prior embedded database: nothing was backed up.
				note += "; no prior embedded store existed, so no rollback backup was created."
			}
			gate.Detail = strings.TrimRight(gate.Detail, " ") + note
		}()
	}
	gate = ProjectCoordinationBackendReplacementGate{
		ID:        "migration-and-rollback",
		Ready:     false,
		Status:    "waiting_for_read_smoke",
		Detail:    "Strict embedded mirror parity and read smoke must be verified before migration and rollback can be treated as rehearsed.",
		ProbeURLs: []string{projectCoordinationBackendSyncReadinessURL, projectCoordinationBackendMirrorParityURL, projectCoordinationBackendExportURL},
	}
	if migrationRehearsal != nil && migrationRehearsal.Operation == "embedded_replacement_smoke" {
		gate.Detail = "Embedded Cairnline live read smoke must be verified before replacement-mode rollback evidence can be treated as rehearsed."
		gate.ProbeURLs = []string{projectCoordinationBackendStatusURL}
	}
	if migrationCutoverArmed && projectCairnlineMigrationRollbackEvidenceReady(migrationRehearsal) {
		gate.Ready = true
		gate.Status = "ready"
		if migrationRehearsal != nil && migrationRehearsal.Operation == "embedded_replacement_smoke" {
			gate.Detail = "Embedded Cairnline live route smoke and rollback notes are verified; embedded replacement mode is the explicit cutover switch and implies portable write authority."
			return gate
		}
		gate.Detail = "Strict embedded mirror parity, route smoke, snapshot-import evidence, and rollback notes are verified; embedded replacement mode is the explicit cutover switch and implies portable write authority."
		return gate
	}
	if !strictEmbeddedReadGate.Ready {
		return gate
	}
	if blocker := projectCairnlineMigrationRollbackEvidenceBlocker(migrationRehearsal); blocker != "" {
		gate.Status = "rehearsal_incomplete"
		gate.Detail = "Strict embedded read smoke is verified, but migration and rollback evidence is incomplete: " + blocker + "."
		return gate
	}
	gate.Status = "cutover_switch_missing"
	if migrationRehearsal != nil && migrationRehearsal.Operation == "embedded_replacement_smoke" {
		gate.Detail = "Embedded Cairnline live route smoke and rollback notes are verified, but embedded replacement mode is not armed."
		return gate
	}
	gate.Detail = "Strict embedded mirror parity, route smoke, snapshot-import evidence, and rollback notes are verified, but no explicit authoritative Cairnline storage cutover switch is armed yet."
	return gate
}

func projectCairnlineMigrationRollbackEvidenceReady(migrationRehearsal *ProjectCairnlineMigrationRehearsal) bool {
	return projectCairnlineMigrationRollbackEvidenceBlocker(migrationRehearsal) == ""
}

func projectCairnlineMigrationRollbackEvidenceBlocker(migrationRehearsal *ProjectCairnlineMigrationRehearsal) string {
	if migrationRehearsal == nil {
		return "backend status does not include migration or replacement smoke evidence"
	}
	if migrationRehearsal.Operation == "embedded_replacement_smoke" {
		return projectCairnlineEmbeddedReplacementModeEvidenceBlocker(migrationRehearsal)
	}
	if migrationRehearsal.Operation != "mirror_parity" {
		return "expected mirror_parity rehearsal evidence, got " + migrationRehearsal.Operation
	}
	if migrationRehearsal.Status != "verified" {
		return "mirror-parity rehearsal status is " + migrationRehearsal.Status
	}
	requiredChecks := []ProjectCairnlineMigrationRehearsalCheck{
		{ID: "load-hecate-stores", Status: "complete"},
		{ID: "native-snapshot-import", Status: "complete"},
		{ID: "parity-check", Status: "complete"},
		{ID: "strict-embedded-read-smoke", Status: "complete"},
		{ID: "rollback-plan", Status: "documented"},
	}
	for _, check := range requiredChecks {
		if !projectCairnlineMigrationChecklistHas(migrationRehearsal.Checklist, check.ID, check.Status) {
			return fmt.Sprintf("check %q is not %q", check.ID, check.Status)
		}
	}
	if len(migrationRehearsal.Rollback) == 0 {
		return "rollback steps are missing"
	}
	if migrationRehearsal.EmbeddedSmoke == nil {
		return "strict embedded smoke details are missing"
	}
	if migrationRehearsal.EmbeddedSmoke.Status != "passed" {
		return "strict embedded smoke status is " + migrationRehearsal.EmbeddedSmoke.Status
	}
	return ""
}

func projectCairnlineEmbeddedReplacementModeEvidenceBlocker(migrationRehearsal *ProjectCairnlineMigrationRehearsal) string {
	if migrationRehearsal.Status != "verified" {
		return "embedded replacement smoke status is " + migrationRehearsal.Status
	}
	requiredChecks := []ProjectCairnlineMigrationRehearsalCheck{
		{ID: "open-embedded-cairnline", Status: "complete"},
		{ID: "strict-embedded-read-smoke", Status: "complete"},
		{ID: "rollback-plan", Status: "documented"},
		{ID: "authoritative-switchpoint", Status: "complete"},
	}
	for _, check := range requiredChecks {
		if !projectCairnlineMigrationChecklistHas(migrationRehearsal.Checklist, check.ID, check.Status) {
			return fmt.Sprintf("check %q is not %q", check.ID, check.Status)
		}
	}
	if len(migrationRehearsal.Rollback) == 0 {
		return "rollback steps are missing"
	}
	if migrationRehearsal.EmbeddedSmoke == nil {
		return "strict embedded smoke details are missing"
	}
	if migrationRehearsal.EmbeddedSmoke.Status != "passed" {
		return "strict embedded smoke status is " + migrationRehearsal.EmbeddedSmoke.Status
	}
	return ""
}

func projectCairnlineMigrationChecklistHas(items []ProjectCairnlineMigrationRehearsalCheck, id, status string) bool {
	for _, item := range items {
		if item.ID == id && item.Status == status {
			return true
		}
	}
	return false
}

func projectCairnlineReplacementModeGate(replacementMode string) ProjectCoordinationBackendReplacementGate {
	if replacementMode == "embedded" {
		return ProjectCoordinationBackendReplacementGate{
			ID:     "embedded-replacement-mode",
			Ready:  true,
			Status: "armed",
			Detail: "HECATE_PROJECTS_CAIRNLINE_REPLACEMENT_MODE=embedded is set, so the operator has explicitly armed the embedded Cairnline cutover contract and the portable write-authority switchpoints. This does not bypass read, migration, rollback, or Hecate-owned runtime side-effect gates.",
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
		return "Embedded Cairnline replacement mode is armed and implies all portable write-authority switchpoints, but status still requires the read, migration, rollback, and runtime-boundary gates before any backend can be considered replaceable."
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

func projectCairnlineMigrationBlockersSnapshot(writeGaps []string, migrationCutoverArmed, migrationExecutedVerified bool) []string {
	if migrationCutoverArmed || migrationExecutedVerified {
		return nil
	}
	out := make([]string, 0, 1)
	for _, item := range writeGaps {
		if item == "migration-cutover" {
			out = append(out, item)
		}
	}
	return out
}

func projectCairnlineWriteAdapterGapsAfterMigrationCutover(writeGaps []string, migrationCutoverArmed, migrationExecutedVerified bool) []string {
	if !migrationCutoverArmed && !migrationExecutedVerified {
		return append([]string(nil), writeGaps...)
	}
	out := make([]string, 0, len(writeGaps))
	for _, item := range writeGaps {
		if item == "migration-cutover" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func projectCairnlineWriteSwitchpointsSnapshot(writeAuthority []string, nativeShadowSkipArmed, migrationCutoverArmed bool, migrationCutover *ProjectCairnlineMigrationCutoverStatus) []ProjectCoordinationBackendWriteSwitchpoint {
	out := make([]ProjectCoordinationBackendWriteSwitchpoint, 0, len(projectCairnlineWriteSwitchpoints))
	migrationExecuted := migrationCutover != nil && migrationCutover.Migrated
	migrationExecutedVerified := migrationCutover != nil && migrationCutover.Status == "migrated_verified"
	projectMemoryAuthoritative := projectCairnlineWriteAuthorityEnabled(writeAuthority, "project-memory")
	memoryCandidatesAuthoritative := projectMemoryAuthoritative && projectCairnlineWriteAuthorityEnabled(writeAuthority, "memory-candidates")
	projectCollaborationAuthoritative := projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectCollaboration)
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
			item.Detail = "Project create/delete commits portable identity, roots, context sources, default-root metadata, and project identity removal to the embedded Cairnline database first, then best-effort shadows Hecate's compatibility row; Hecate runtime defaults stay in the project runtime overlay and delete rolls the Cairnline snapshot back if Hecate compatibility cleanup fails."
			if nativeShadowSkipArmed {
				item.Detail = "Project create/delete commits portable identity, roots, context sources, default-root metadata, and project identity removal to the embedded Cairnline database first and, in armed embedded replacement mode, skips native project identity compatibility rows; Hecate runtime defaults stay in the project runtime overlay."
			}
		}
		if projectMetadataDefaultsAuthoritative && item.Name == "project-metadata-defaults" {
			item.CurrentAuthority = "cairnline"
			item.CairnlineState = "authoritative_opt_in"
			item.LiveMirror = true
			item.BlocksAuthority = false
			item.Gap = ""
			item.Detail = "Project metadata/default-root update mutations commit portable project metadata and default-root metadata to the embedded Cairnline database first, persist Hecate runtime defaults in the project runtime overlay, then best-effort shadow Hecate's compatibility row back into Hecate-native stores; project identity, roots, context sources, and mixed metadata/root/source replacement routes are controlled by separate switchpoints."
			if nativeShadowSkipArmed {
				item.Detail = "Project metadata/default-root update mutations commit portable project metadata and default-root metadata to the embedded Cairnline database first, persist Hecate runtime defaults in the project runtime overlay, and, in armed embedded replacement mode, skip native project-row compatibility shadows; project identity, roots, context sources, and mixed metadata/root/source replacement routes are controlled by separate switchpoints."
			}
		}
		if projectRootsAuthoritative && item.Name == "roots-and-worktrees" {
			item.CurrentAuthority = "mixed"
			item.CairnlineState = "partial_authoritative_opt_in"
			item.LiveMirror = true
			item.BlocksAuthority = false
			item.Gap = "roots"
			item.Detail = "Project root create/update/delete, root list replacement, discovery-result replacement, and worktree-created root record mutations commit to the embedded Cairnline database first, then best-effort shadow Hecate's compatibility row; discovery and worktree-created root record mutations can resolve project identity and roots from the embedded Cairnline graph, while Hecate still performs root discovery scans and Git worktree creation side effects."
			if nativeShadowSkipArmed {
				item.Detail = "Project root create/update/delete, root list replacement, discovery-result replacement, and worktree-created root record mutations commit to the embedded Cairnline database first and, in armed embedded replacement mode, skip native project-row root compatibility shadows; discovery and worktree-created root record mutations can resolve project identity and roots from the embedded Cairnline graph, while Hecate still performs root discovery scans and Git worktree creation side effects."
			}
		}
		if projectContextSourcesAuthoritative && item.Name == "context-sources" {
			item.CurrentAuthority = "cairnline"
			item.CairnlineState = "authoritative_opt_in"
			item.LiveMirror = true
			item.BlocksAuthority = false
			item.Gap = ""
			item.Detail = "Context-source create/update/delete, list replacement, and discovery-result replacement mutations commit to the embedded Cairnline database first, then best-effort shadow Hecate's compatibility row; discovery can resolve project identity, roots, and existing sources from the embedded Cairnline graph, while Hecate still performs the workspace scan for its operator UI."
			if nativeShadowSkipArmed {
				item.Detail = "Context-source create/update/delete, list replacement, and discovery-result replacement mutations commit to the embedded Cairnline database first and, in armed embedded replacement mode, skip native project-row context-source compatibility shadows; discovery can resolve project identity, roots, and existing sources from the embedded Cairnline graph, while Hecate still performs the workspace scan for its operator UI."
			}
		}
		if projectSkillsAuthoritative && item.Name == "skills" {
			item.CurrentAuthority = "cairnline"
			item.CairnlineState = "authoritative_opt_in"
			item.LiveMirror = true
			item.BlocksAuthority = false
			item.Gap = ""
			item.Detail = "Project skill discovery and update mutations commit metadata-only skill records to the embedded Cairnline database first, using Cairnline-owned roots and context sources when no Hecate-native project row exists, then best-effort shadow them back into Hecate-native stores for compatibility."
			if nativeShadowSkipArmed {
				item.Detail = "Project skill discovery and update mutations commit metadata-only skill records to the embedded Cairnline database first and, in armed embedded replacement mode, skip native project-skill compatibility rows."
			}
		}
		if projectRolesAuthoritative && item.Name == "roles" {
			item.CurrentAuthority = "cairnline"
			item.CairnlineState = "authoritative_opt_in"
			item.LiveMirror = true
			item.BlocksAuthority = false
			item.Gap = ""
			item.Detail = "Project role create, update, and delete mutations commit to the embedded Cairnline database first, then best-effort shadow portable role defaults back into Hecate-native stores for compatibility."
			if nativeShadowSkipArmed {
				item.Detail = "Project role create, update, and delete mutations commit to the embedded Cairnline database first and, in armed embedded replacement mode, skip native project-work role compatibility rows."
			}
		}
		if projectWorkItemsAuthoritative && item.Name == "work-items" {
			item.CurrentAuthority = "cairnline"
			item.CairnlineState = "authoritative_opt_in"
			item.LiveMirror = true
			item.BlocksAuthority = false
			item.Gap = ""
			item.Detail = "Work-item create, update, and delete mutations commit to the embedded Cairnline database first, then best-effort shadow portable work-item state back into Hecate-native stores for compatibility."
			if nativeShadowSkipArmed {
				item.Detail = "Work-item create, update, and delete mutations commit to the embedded Cairnline database first and, in armed embedded replacement mode, skip native project-work item compatibility rows."
			}
		}
		if projectAssignmentsAuthoritative && item.Name == "assignments" {
			item.CurrentAuthority = "cairnline"
			item.CairnlineState = "authoritative_opt_in"
			item.LiveMirror = true
			item.BlocksAuthority = false
			item.Gap = ""
			item.Detail = "Assignment create, update, and delete record mutations commit to the embedded Cairnline database first, then best-effort shadow portable assignment state back into Hecate-native stores for compatibility; assignment start claims the Cairnline coordination record before Hecate-owned dispatch and releases that claim when launch setup fails before a runtime record is committed."
			if nativeShadowSkipArmed {
				item.Detail = "Assignment create, update, and delete record mutations commit to the embedded Cairnline database first and, in armed embedded replacement mode, skip native project-work assignment compatibility rows while Hecate keeps assignment execution refs, context packets, and launch timestamps in its runtime overlay."
			}
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
			if nativeShadowSkipArmed {
				item.Detail = "Project Assistant draft/propose/apply-attempt ledger records commit to the embedded Cairnline database first and, in armed embedded replacement mode, skip native proposal ledger compatibility rows; confirmed apply remains mixed-authority when enabled portable actions route through Cairnline."
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
			if nativeShadowSkipArmed {
				item.Detail = "Generic artifact, evidence, and review creation commits to the embedded Cairnline database first and, in armed embedded replacement mode, skips native project-work artifact compatibility rows."
			}
		}
		if projectCollaborationAuthoritative && item.Name == "handoffs" {
			item.CurrentAuthority = "cairnline"
			item.CairnlineState = "authoritative_opt_in"
			item.LiveMirror = true
			item.BlocksAuthority = false
			item.Gap = ""
			item.Detail = "Handoff create, update, status, and delete mutations commit to the embedded Cairnline database first, then best-effort shadow portable handoff state back into Hecate-native stores for compatibility."
			if nativeShadowSkipArmed {
				item.Detail = "Handoff create, update, status, and delete mutations commit to the embedded Cairnline database first and, in armed embedded replacement mode, skip native project-work handoff compatibility rows."
			}
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
		if item.Name == "migration-cutover" {
			// The one-way migration endpoint exists, so the baseline reports the
			// capability as available; a durable migration record upgrades the
			// per-request state to reflect what has actually been executed. The
			// armed-mode override below still wins when embedded replacement mode
			// is armed.
			item.Detail = "One-way migration endpoint POST /hecate/v1/projects/cairnline/migrate rebuilds and verifies the embedded Cairnline store from native sources; run it to arm cutover."
			if migrationExecuted {
				item.LiveMirror = true
				if strings.TrimSpace(migrationCutover.RollbackBackupPath) != "" {
					item.Detail += " A verified authoritative migration has been executed with a rollback backup."
				} else {
					// First migration with no prior embedded database: nothing was backed up.
					item.Detail += " A verified authoritative migration has been executed; no prior embedded store existed, so no rollback backup was created."
				}
			}
			if migrationExecutedVerified {
				item.CairnlineState = "migrated_verified"
			}
		}
		if migrationCutoverArmed && item.Name == "migration-cutover" {
			item.CurrentAuthority = "cairnline"
			item.CairnlineState = "embedded_cutover_armed"
			item.BlocksAuthority = false
			item.Gap = ""
			item.Detail = "Strict embedded mirror parity and read smoke are verified, and embedded replacement mode is armed as the explicit Cairnline cutover switch with portable write authority implied."
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

func projectCairnlineProjectMetadataDefaultsWriteWarning(writeAuthority []string, nativeShadowSkipArmed bool) string {
	if projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectMetadataDefaults) {
		if nativeShadowSkipArmed {
			return "Project metadata/default-root update mutations are opt-in Cairnline-authoritative and skip native project-row compatibility shadows in armed embedded replacement mode; provider/model/preset posture stays in Hecate runtime overlays, and project identity, roots, context sources, and mixed root/source replacement routes are controlled by separate switchpoints."
		}
		return "Project metadata/default-root update mutations are opt-in Cairnline-authoritative and then best-effort shadowed into Hecate-native stores for compatibility; provider/model/preset posture stays in Hecate runtime overlays, and project identity, roots, context sources, and mixed root/source replacement routes are controlled by separate switchpoints."
	}
	return "Project metadata/default-root updates still write Hecate-native stores first, then best-effort mirror through Cairnline's project-metadata and project-defaults seams; provider/model/preset posture stays in Hecate runtime overlays."
}

func projectCairnlineProjectIdentityWriteWarning(writeAuthority []string, nativeShadowSkipArmed bool) string {
	if projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectIdentity) {
		if nativeShadowSkipArmed {
			return "Project create/delete mutations are opt-in Cairnline-authoritative and skip native project identity compatibility rows in armed embedded replacement mode; Hecate runtime defaults stay in the project runtime overlay."
		}
		return "Project create/delete mutations are opt-in Cairnline-authoritative and then best-effort shadowed into Hecate-native stores for compatibility; delete restores the Cairnline snapshot if Hecate compatibility cleanup fails."
	}
	return "Project create/delete still write Hecate-native stores first, then best-effort mirror portable project identity into the embedded Cairnline database."
}

func projectCairnlineProjectRootWriteWarning(writeAuthority []string, nativeShadowSkipArmed bool) string {
	if projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectRoots) {
		if nativeShadowSkipArmed {
			return "Project root create/update/delete, root list replacement, discovery-result replacement, and worktree-created root record mutations are opt-in Cairnline-authoritative and skip native project-row root compatibility shadows in armed embedded replacement mode; discovery and worktree-created root record mutations can resolve project identity and roots from the embedded Cairnline graph, while Hecate still performs root discovery scans and Git worktree creation side effects."
		}
		return "Project root create/update/delete, root list replacement, discovery-result replacement, and worktree-created root record mutations are opt-in Cairnline-authoritative and then best-effort shadowed into Hecate-native stores for compatibility; discovery and worktree-created root record mutations can resolve project identity and roots from the embedded Cairnline graph, while Hecate still performs root discovery scans and Git worktree creation side effects."
	}
	return "Root create/update/delete, root list replacement, root discovery, and worktree-created root record mutations still write Hecate-native stores first, then best-effort mirror through Cairnline's root-level API; Hecate owns the Git worktree creation side effect."
}

func projectCairnlineProjectContextSourceWriteWarning(writeAuthority []string, nativeShadowSkipArmed bool) string {
	if projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectContextSources) {
		if nativeShadowSkipArmed {
			return "Context-source create/update/delete, list replacement, and discovery-result replacement mutations are opt-in Cairnline-authoritative and skip native project-row context-source compatibility shadows in armed embedded replacement mode; discovery can resolve project identity, roots, and existing sources from the embedded Cairnline graph, while Hecate still performs the workspace scan for its operator UI."
		}
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
		return "Project assignment create/update/delete record mutations are opt-in Cairnline-authoritative and then best-effort shadowed into Hecate-native stores for compatibility; assignment start claims the Cairnline coordination record before Hecate-owned dispatch, releases that claim on pre-runtime setup failure, and best-effort mirrors committed start and linked-chat reconciliation results."
	}
	return "Project assignment create/update/delete mutations still write Hecate-native stores first, then best-effort mirror coordination metadata into Cairnline; assignment start remains Hecate-owned and best-effort mirrors committed start and linked-chat reconciliation results."
}

func projectCairnlineProjectAssistantProposalWriteWarning(writeAuthority []string, nativeShadowSkipArmed bool) string {
	if projectCairnlineWriteAuthorityEnabled(writeAuthority, projectCairnlineWriteAuthorityProjectAssistantProposals) {
		if nativeShadowSkipArmed {
			if projectCairnlineAssistantApplyPortableEffectsAuthoritative(writeAuthority) {
				return "Project Assistant proposal ledger mutations are opt-in Cairnline-authoritative and skip native proposal ledger compatibility rows in armed embedded replacement mode; confirmed apply is mixed-authority when enabled portable actions route through Cairnline."
			}
			return "Project Assistant proposal ledger mutations are opt-in Cairnline-authoritative and skip native proposal ledger compatibility rows in armed embedded replacement mode; confirmed apply side effects still execute through Hecate-owned project mutation services."
		}
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

func projectCairnlineReadRoutesReadyDetail(readSource string, writeAdapterReady bool, replacementMode string) string {
	detail := "Cairnline is configured as the future Projects coordination backend, and the " + projectCairnlineReadRouteList(projectCairnlineReadRouteNames) + " read routes are served from the Cairnline read model. " + projectCairnlineReadSourceDetail(readSource) + " "
	if writeAdapterReady && replacementMode == "embedded" {
		return detail + "Portable project-state writes are Cairnline-authoritative in embedded replacement mode; Hecate still owns runtime/workspace side effects until strict read smoke, migration, and rollback gates are ready."
	}
	if writeAdapterReady {
		return detail + "Portable project-state write authority is Cairnline-ready, but Hecate remains the reported backend until replacement mode, migration, and rollback gates are ready."
	}
	return detail + "Hecate stores remain authoritative until the remaining portable writes and migration are ready."
}

func projectCairnlineReadSourceWarning(source string) string {
	switch source {
	case "embedded":
		return "HECATE_PROJECTS_CAIRNLINE_READ_SOURCE=embedded requires a populated embedded Cairnline mirror database and fails configured read routes when the database, project row, or proposal record is missing."
	case "sidecar":
		return "HECATE_PROJECTS_CAIRNLINE_READ_SOURCE=sidecar routes only " + projectCairnlineReadRouteList(projectCairnlineSidecarReadRouteNames) + " through the standalone Cairnline MCP client; writes remain Hecate-owned because current write-authority switchpoints require HECATE_PROJECTS_CAIRNLINE_CONNECTOR=embedded."
	case "snapshot":
		return "HECATE_PROJECTS_CAIRNLINE_READ_SOURCE=snapshot keeps configured read routes on the snapshot-seeded in-memory Cairnline bridge projection even when an embedded mirror database exists."
	default:
		return "HECATE_PROJECTS_CAIRNLINE_READ_SOURCE=auto makes configured Cairnline read-model service reads prefer the embedded mirror database when it already contains the requested project or proposal record, and otherwise use a snapshot-seeded in-memory Cairnline bridge projection."
	}
}

func projectCairnlineSidecarWriteAuthorityWarning(writeAuthority []string) string {
	if len(writeAuthority) > 0 {
		return "Configured HECATE_PROJECTS_CAIRNLINE_WRITE_AUTHORITY values (" + strings.Join(writeAuthority, ", ") + ") are ignored in sidecar connector mode; project writes still use Hecate-native stores unless HECATE_PROJECTS_CAIRNLINE_CONNECTOR=embedded is used for embedded Cairnline write-authority dogfooding."
	}
	return "Project writes still use Hecate-native stores; current write-authority switchpoints require HECATE_PROJECTS_CAIRNLINE_CONNECTOR=embedded."
}

func (h *Handler) cairnlineReadModelReadiness() (bool, []string) {
	if h == nil {
		return false, []string{"Hecate handler is not configured."}
	}
	sources := h.cairnlineSnapshotSources()
	missing := make([]string, 0, 6)
	if sources.Projects == nil {
		missing = append(missing, "projects store")
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
