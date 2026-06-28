package api

import "net/http"

const projectCoordinationBackendReadinessURL = "/hecate/v1/projects/{id}/cairnline/read-model"
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
		Detail:           "Root create/update/delete, root discovery, root list replacement, and worktree-root creation still commit to Hecate first, then mirror through Cairnline root APIs.",
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
		Name:             "project-assistant-proposals",
		CurrentAuthority: "hecate",
		CairnlineState:   "live_mirror_non_authoritative",
		LiveMirror:       true,
		BlocksAuthority:  true,
		Seams:            []string{"project-assistant-proposal-ledger-live-mirror", "project-assistant-apply-side-effects-live-mirror"},
		Gap:              "project-assistant-proposals",
		Detail:           "Project Assistant draft/propose/apply records and committed side effects still commit to Hecate first, then mirror into the portable proposal ledger.",
	},
	{
		Name:             "migration-cutover",
		CurrentAuthority: "hecate",
		CairnlineState:   "missing_authoritative_switchpoint",
		BlocksAuthority:  true,
		Gap:              "migration-cutover",
		Detail:           "No import/export cutover, rollback, or authoritative Cairnline storage switch exists yet.",
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
	if h != nil {
		configured = h.config.ProjectsCoordinationBackend()
		storageBackend = h.config.Projects.Backend
		readSource = h.config.ProjectsCairnlineReadSource()
	}
	response := ProjectCoordinationBackendStatusResponse{
		ConfiguredBackend:       configured,
		AuthoritativeBackend:    "hecate",
		StorageBackend:          storageBackend,
		CairnlineReadSource:     readSource,
		CairnlineBridgeReady:    true,
		CairnlineAuthoritative:  false,
		WriteAdapterReady:       false,
		ReplacementReadinessURL: projectCoordinationBackendReadinessURL,
		EmbeddedReadModelURL:    projectCoordinationBackendEmbeddedReadModelURL,
		EmbeddedParityReportURL: projectCoordinationBackendEmbeddedParityReportURL,
		SyncReadinessURL:        projectCoordinationBackendSyncReadinessURL,
		MirrorParityURL:         projectCoordinationBackendMirrorParityURL,
	}
	switch configured {
	case "cairnline":
		readReady, sourceWarnings := h.cairnlineReadModelReadiness()
		response.WriteAdapterSeams = append([]string(nil), projectCairnlineWriteAdapterSeamNames...)
		response.WriteAdapterGaps = append([]string(nil), projectCairnlineWriteAdapterGapNames...)
		response.WriteSwitchpoints = projectCairnlineWriteSwitchpointsSnapshot()
		response.ReplacementGates = projectCairnlineReplacementGates(readReady)
		response.ReadModelSwitchReady = readReady
		if readReady {
			response.ReadRoutes = append([]string(nil), projectCairnlineReadRouteNames...)
			response.Status = "cairnline_read_routes_ready"
			response.Detail = "Cairnline is configured as the future Projects coordination backend, and the project-list, project-detail, setup-readiness, health, skills, memory, memory-candidate, roles, work-item, assignment-list, assignment-context, launch-readiness, assignment-preflight, artifact-list, handoff-list, project-assistant-context, project-assistant-proposal, activity, closeout-readiness, and operations brief read routes are served from the Cairnline read model. " + projectCairnlineReadSourceDetail(readSource) + " Hecate stores remain authoritative until the remaining live read routes, writes, and migration are ready."
			response.Warnings = []string{
				"Only the project-list, project-detail, setup-readiness, health, skills, memory, memory-candidate, roles, work-item, assignment-list, assignment-context, launch-readiness, assignment-preflight, artifact-list, handoff-list, project-assistant-context, project-assistant-proposal, activity, closeout-readiness, and operations brief live read routes use Cairnline.",
				projectCairnlineReadSourceWarning(readSource),
				"Project create/delete still write Hecate-native stores first, then best-effort mirror portable project identity into the embedded Cairnline database.",
				"Project metadata updates still write Hecate-native stores first, then best-effort mirror through Cairnline's project-metadata seam.",
				"Root create/update/delete, root list replacement, root discovery, and worktree-root creation mutations still write Hecate-native stores first, then best-effort mirror through Cairnline's root-level API.",
				"Direct context-source create/update/delete, context-source list replacement, and discovery mutations still write Hecate-native stores first, then best-effort mirror through Cairnline's source-level API.",
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

func projectCairnlineReplacementGates(readRoutesReady bool) []ProjectCoordinationBackendReplacementGate {
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
		{
			ID:     "write-authority-switchpoints",
			Ready:  false,
			Status: "blocked",
			Detail: "Live mutation routes still commit to Hecate-native stores first; Cairnline mirrors are replacement evidence, not write authority.",
		},
		{
			ID:     "migration-and-rollback",
			Ready:  false,
			Status: "blocked",
			Detail: "No migration cutover, rollback, or authoritative Cairnline storage switch exists yet.",
		},
	}
}

func projectReplacementGateStatus(ready bool) string {
	if ready {
		return "ready"
	}
	return "blocked"
}

func projectCairnlineWriteSwitchpointsSnapshot() []ProjectCoordinationBackendWriteSwitchpoint {
	out := make([]ProjectCoordinationBackendWriteSwitchpoint, 0, len(projectCairnlineWriteSwitchpoints))
	for _, item := range projectCairnlineWriteSwitchpoints {
		item.Seams = append([]string(nil), item.Seams...)
		out = append(out, item)
	}
	return out
}

func projectCairnlineReadSourceDetail(source string) string {
	switch source {
	case "embedded":
		return "Configured read routes require the embedded mirror database and requested project row or proposal record; if the mirror is missing or stale, the route fails loudly instead of falling back to a Hecate snapshot."
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
