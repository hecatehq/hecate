package api

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/cairnlinebridge"
	"github.com/hecatehq/hecate/internal/projects"
)

func (h *Handler) HandleExportProjectToCairnline(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.PathValue("id"))
	dbPath, err := h.cairnlineExportPath(projectID)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	snapshot, err := cairnlinebridge.LoadSnapshot(r.Context(), h.cairnlineSnapshotSources(), projectID)
	if errors.Is(err, projects.ErrNotFound) {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	// This local-only experiment owns its project export file. Replacing it
	// keeps repeated operator-triggered exports deterministic while avoiding a
	// live-backend switch or any mutation of Hecate's authoritative stores.
	if err := removeCairnlineExportFiles(dbPath); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	service, store, err := cairnline.NewSQLiteService(r.Context(), dbPath)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	defer store.Close()
	if err := cairnlinebridge.Seed(r.Context(), service, snapshot); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, ProjectCairnlineExportResponse{
		Object: "project_cairnline_export",
		Data: ProjectCairnlineExportResponseItem{
			ProjectID:            snapshot.Project.ID,
			DatabasePath:         dbPath,
			AgentProfileCount:    len(snapshot.AgentProfiles),
			SkillCount:           len(snapshot.Skills),
			RoleCount:            len(snapshot.Roles),
			WorkItemCount:        len(snapshot.WorkItems),
			AssignmentCount:      len(snapshot.Assignments),
			ArtifactCount:        len(snapshot.Artifacts),
			HandoffCount:         len(snapshot.Handoffs),
			MemoryEntryCount:     len(snapshot.MemoryEntries),
			MemoryCandidateCount: len(snapshot.MemoryCandidates),
		},
	})
}

func (h *Handler) cairnlineSnapshotSources() cairnlinebridge.SnapshotSources {
	return cairnlinebridge.SnapshotSources{
		Projects:         h.projects,
		AgentProfiles:    h.agentProfiles,
		Skills:           h.projectSkills,
		Work:             h.projectWork,
		Memory:           h.memory,
		MemoryCandidates: h.memoryCandidates,
	}
}

func (h *Handler) cairnlineExportPath(projectID string) (string, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return "", errors.New("project id is required")
	}
	dataDir := strings.TrimSpace(h.config.Server.DataDir)
	if dataDir == "" {
		dataDir = ".data"
	}
	path := filepath.Join(dataDir, "cairnline", "projects", safeCairnlineExportName(projectID)+".db")
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return path, nil
}

func safeCairnlineExportName(value string) string {
	var b strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "project"
	}
	return out
}

func removeCairnlineExportFiles(dbPath string) error {
	for _, path := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
