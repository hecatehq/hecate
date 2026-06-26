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
		ReadModelSwitchReady:    false,
		WriteAdapterReady:       false,
		ReplacementReadinessURL: projectCoordinationBackendReadinessURL,
	}
	switch configured {
	case "cairnline":
		response.Status = "cairnline_configured_inactive"
		response.Detail = "Cairnline is configured as the future Projects coordination backend, but Hecate stores remain authoritative until the feature-flagged adapter and migration path land."
		response.Warnings = []string{
			"Project reads and writes still use Hecate-native stores.",
			"Cairnline bridge endpoints are replacement-readiness proofs, not the live backend.",
		}
	default:
		response.Status = "hecate_authoritative"
		response.Detail = "Hecate-native project stores are authoritative. Cairnline bridge endpoints are available for replacement-readiness checks."
	}
	return response
}
