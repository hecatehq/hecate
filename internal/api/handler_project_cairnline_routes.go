package api

func (h *Handler) projectCairnlineEmbeddedConnectorEnabled() bool {
	return h != nil &&
		h.config.ProjectsCoordinationBackend() == "cairnline" &&
		h.config.ProjectsCairnlineConnector() == "embedded"
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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
