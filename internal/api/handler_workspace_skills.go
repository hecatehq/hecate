package api

import (
	"context"
	"net/http"
	"time"

	"github.com/hecatehq/hecate/internal/workspaceskills"
	"github.com/hecatehq/hecate/pkg/types"
)

func (h *Handler) HandleWorkspaceSkills(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Workspace string `json:"workspace"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	if !decodeJSON(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	skills, err := workspaceskills.Catalog(ctx, req.Workspace)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "Workspace skills could not be inspected. Choose an accessible local workspace with bounded SKILL.md files.")
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	WriteJSON(w, http.StatusOK, struct {
		Object string                     `json:"object"`
		Data   []types.WorkspaceSkillInfo `json:"data"`
	}{"workspace_skills", skills})
}
