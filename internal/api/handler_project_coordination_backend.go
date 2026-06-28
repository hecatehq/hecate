package api

import "net/http"

const projectCoordinationBackendReadinessURL = "/hecate/v1/projects/{id}/cairnline/read-model"
const projectCoordinationBackendSyncReadinessURL = "/hecate/v1/projects/cairnline/sync"

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

var projectCairnlineWriteAdapterSeamNames = []string{
	"projects",
	"roots",
	"project-roots-live-mirror",
	"context-sources",
	"project-context-sources-live-mirror",
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
	"migration-cutover",
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
	if h != nil {
		configured = h.config.ProjectsCoordinationBackend()
		storageBackend = h.config.Projects.Backend
	}
	response := ProjectCoordinationBackendStatusResponse{
		ConfiguredBackend:       configured,
		AuthoritativeBackend:    "hecate",
		StorageBackend:          storageBackend,
		CairnlineBridgeReady:    true,
		CairnlineAuthoritative:  false,
		WriteAdapterReady:       false,
		ReplacementReadinessURL: projectCoordinationBackendReadinessURL,
		SyncReadinessURL:        projectCoordinationBackendSyncReadinessURL,
	}
	switch configured {
	case "cairnline":
		readReady, sourceWarnings := h.cairnlineReadModelReadiness()
		response.WriteAdapterSeams = append([]string(nil), projectCairnlineWriteAdapterSeamNames...)
		response.WriteAdapterGaps = append([]string(nil), projectCairnlineWriteAdapterGapNames...)
		response.ReadModelSwitchReady = readReady
		if readReady {
			response.ReadRoutes = append([]string(nil), projectCairnlineReadRouteNames...)
			response.Status = "cairnline_read_routes_ready"
			response.Detail = "Cairnline is configured as the future Projects coordination backend, and the project-list, project-detail, setup-readiness, health, skills, memory, memory-candidate, roles, work-item, assignment-list, assignment-context, launch-readiness, assignment-preflight, artifact-list, handoff-list, project-assistant-context, project-assistant-proposal, activity, closeout-readiness, and operations brief read routes are served from the Cairnline read model. Hecate stores remain authoritative until the remaining live read routes, writes, and migration are ready."
			response.Warnings = []string{
				"Only the project-list, project-detail, setup-readiness, health, skills, memory, memory-candidate, roles, work-item, assignment-list, assignment-context, launch-readiness, assignment-preflight, artifact-list, handoff-list, project-assistant-context, project-assistant-proposal, activity, closeout-readiness, and operations brief live read routes use Cairnline.",
				"Project identity and root discovery/worktree-creation still write Hecate-native stores first, then best-effort mirror portable project identity into the embedded Cairnline database.",
				"Direct root create/update/delete mutations still write Hecate-native stores first, then best-effort mirror through Cairnline's root-level API.",
				"Direct context-source create/update/delete and discovery mutations still write Hecate-native stores first, then best-effort mirror through Cairnline's source-level API.",
				"Default-only project updates still write Hecate-native stores first, then best-effort mirror portable launch defaults through Cairnline's project-defaults seam.",
				"Agent profile create/update/delete mutations still write Hecate-native stores first, then best-effort mirror portable profile metadata and execution posture into Cairnline.",
				"Project skill discovery and metadata updates still write Hecate-native stores first, then best-effort mirror metadata-only skill records into Cairnline.",
				"Project role and work-item mutations still write Hecate-native stores first, then best-effort mirror coordination metadata into Cairnline.",
				"Project assignment create/update/delete mutations still write Hecate-native stores first, then best-effort mirror coordination metadata into Cairnline; assignment start remains Hecate-owned and best-effort mirrors committed start and linked-chat reconciliation results.",
				"Project collaboration artifact creation and handoff mutations still write Hecate-native stores first, then best-effort mirror portable collaboration metadata into Cairnline.",
				"Project memory entry and memory-candidate mutations still write Hecate-native stores first, then best-effort mirror accepted memory and reviewable candidate state into Cairnline.",
				"Project Assistant proposal draft/propose/apply ledger mutations still write Hecate-native stores first, then best-effort mirror proposal records plus committed apply side effects into Cairnline.",
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
