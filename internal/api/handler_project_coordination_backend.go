package api

import "net/http"

const projectCoordinationBackendReadinessURL = "/hecate/v1/projects/{id}/cairnline/read-model"

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
	}
	switch configured {
	case "cairnline":
		readReady, sourceWarnings := h.cairnlineReadModelReadiness()
		response.ReadModelSwitchReady = readReady
		if readReady {
			response.Status = "cairnline_operations_read_route_ready"
			response.Detail = "Cairnline is configured as the future Projects coordination backend, and the operations brief read route is served from the Cairnline read model. Hecate stores remain authoritative until the remaining live read routes, writes, and migration are ready."
			response.Warnings = []string{
				"Only the project operations brief live read route uses Cairnline.",
				"Other project APIs still read and write Hecate-native stores.",
				"Cairnline write adapter and migration path are not ready.",
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
	missing := make([]string, 0, 6)
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
	if len(missing) == 0 {
		return true, nil
	}
	warnings := make([]string, 0, len(missing))
	for _, name := range missing {
		warnings = append(warnings, "Cairnline read adapter is missing "+name+".")
	}
	return false, warnings
}
