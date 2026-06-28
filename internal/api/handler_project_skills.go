package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/projectskills"
)

type updateProjectSkillRequest struct {
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
	Enabled     *bool   `json:"enabled,omitempty"`
	TrustLabel  *string `json:"trust_label,omitempty"`
}

func formatProjectSkillTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func (h *Handler) HandleProjectSkills(w http.ResponseWriter, r *http.Request) {
	if h.projectSkills == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "project skills store is not configured")
		return
	}
	if !h.requireProjectExists(w, r, r.PathValue("id")) {
		return
	}
	items, err := h.renderProjectSkills(r.Context(), r.PathValue("id"))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, ProjectSkillsResponse{Object: "project_skills", Data: items})
}

func (h *Handler) HandleDiscoverProjectSkills(w http.ResponseWriter, r *http.Request) {
	if h.projectSkills == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "project skills store is not configured")
		return
	}
	project, ok, err := h.projects.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
		return
	}
	discovered, warnings := projectskills.Discover(r.Context(), project)
	items, err := h.projectSkills.UpsertDiscovered(r.Context(), project.ID, discovered)
	if errors.Is(err, projectskills.ErrInvalid) {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if len(warnings) > 0 {
		for idx := range items {
			items[idx].Warnings = appendUniqueProjectSkillWarnings(items[idx].Warnings, warnings...)
		}
	}
	h.mirrorProjectSkillsToCairnline(r.Context(), "project_skills_discover", project, items)
	WriteJSON(w, http.StatusOK, ProjectSkillsResponse{Object: "project_skills", Data: renderProjectSkills(items, "hecate")})
}

func (h *Handler) HandleUpdateProjectSkill(w http.ResponseWriter, r *http.Request) {
	if h.projectSkills == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "project skills store is not configured")
		return
	}
	project, ok, err := h.projects.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
		return
	}
	var req updateProjectSkillRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	item, err := h.projectSkills.Update(r.Context(), r.PathValue("id"), r.PathValue("skill_id"), func(item *projectskills.Skill) {
		if req.Title != nil {
			item.Title = strings.TrimSpace(*req.Title)
		}
		if req.Description != nil {
			item.Description = strings.TrimSpace(*req.Description)
		}
		if req.Enabled != nil {
			item.Enabled = *req.Enabled
		}
		if req.TrustLabel != nil {
			item.TrustLabel = strings.TrimSpace(*req.TrustLabel)
		}
	})
	if errors.Is(err, projectskills.ErrNotFound) {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project skill not found")
		return
	}
	if errors.Is(err, projectskills.ErrInvalid) {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	h.mirrorProjectSkillsToCairnline(r.Context(), "project_skill_update", project, []projectskills.Skill{item})
	WriteJSON(w, http.StatusOK, ProjectSkillResponse{Object: "project_skill", Data: renderProjectSkill(item, "hecate")})
}

func (h *Handler) renderProjectSkills(ctx context.Context, projectID string) ([]ProjectSkillResponseItem, error) {
	if h.projectReadRoutesUseCairnlineReadModel() {
		return h.renderCairnlineProjectSkills(ctx, projectID)
	}
	items, err := h.projectSkills.List(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return renderProjectSkills(items, "hecate"), nil
}

func (h *Handler) renderCairnlineProjectSkills(ctx context.Context, projectID string) ([]ProjectSkillResponseItem, error) {
	view, err := h.cairnlineProjectWorkView(ctx, projectID)
	if err != nil {
		return nil, err
	}
	defer view.Close()
	items, err := view.service.ListProjectSkills(ctx, view.snapshot.Project.ID)
	if err != nil {
		return nil, err
	}
	nativeByID := projectSkillsByID(view.snapshot.Skills)
	out := make([]ProjectSkillResponseItem, 0, len(items))
	for _, item := range items {
		out = append(out, renderProjectSkill(projectSkillFromCairnline(item, nativeByID[item.ID]), "cairnline"))
	}
	return out, nil
}

func renderProjectSkills(items []projectskills.Skill, readBackend string) []ProjectSkillResponseItem {
	out := make([]ProjectSkillResponseItem, 0, len(items))
	for _, item := range items {
		out = append(out, renderProjectSkill(item, readBackend))
	}
	return out
}

func renderProjectSkill(item projectskills.Skill, readBackend string) ProjectSkillResponseItem {
	return ProjectSkillResponseItem{
		ID:                     item.ID,
		ProjectID:              item.ProjectID,
		ReadBackend:            readBackend,
		Title:                  item.Title,
		Description:            item.Description,
		Path:                   item.Path,
		RootID:                 item.RootID,
		Format:                 item.Format,
		SuggestedTools:         append([]string(nil), item.SuggestedTools...),
		RequiredPermissions:    renderProjectSkillRequiredPermissions(item.RequiredPermissions),
		Enabled:                item.Enabled,
		Status:                 item.Status,
		TrustLabel:             item.TrustLabel,
		SourceContextSourceIDs: append([]string(nil), item.SourceContextSourceIDs...),
		Warnings:               append([]string(nil), item.Warnings...),
		DiscoveredAt:           formatProjectSkillTime(item.DiscoveredAt),
		CreatedAt:              formatProjectSkillTime(item.CreatedAt),
		UpdatedAt:              formatProjectSkillTime(item.UpdatedAt),
	}
}

func projectSkillsByID(items []projectskills.Skill) map[string]projectskills.Skill {
	out := make(map[string]projectskills.Skill, len(items))
	for _, item := range items {
		out[item.ID] = item
	}
	return out
}

func projectSkillFromCairnline(item cairnline.ProjectSkill, native projectskills.Skill) projectskills.Skill {
	return projectskills.Skill{
		ID:                     item.ID,
		ProjectID:              item.ProjectID,
		Title:                  item.Title,
		Description:            item.Description,
		Path:                   item.Path,
		RootID:                 item.RootID,
		Format:                 item.Format,
		SuggestedTools:         append([]string(nil), native.SuggestedTools...),
		RequiredPermissions:    native.RequiredPermissions,
		Enabled:                item.Enabled,
		Status:                 item.Status,
		TrustLabel:             item.TrustLabel,
		SourceContextSourceIDs: append([]string(nil), item.SourceRefs...),
		Warnings:               append([]string(nil), item.Warnings...),
		DiscoveredAt:           item.DiscoveredAt,
		CreatedAt:              item.CreatedAt,
		UpdatedAt:              item.UpdatedAt,
	}
}

func renderProjectSkillRequiredPermissions(permissions projectskills.RequiredPermissions) *ProjectSkillRequiredPermissionsResponseItem {
	if permissions.Empty() {
		return nil
	}
	return &ProjectSkillRequiredPermissionsResponseItem{
		Tools:   cloneBoolResponseValue(permissions.Tools),
		Writes:  cloneBoolResponseValue(permissions.Writes),
		Network: cloneBoolResponseValue(permissions.Network),
	}
}

func cloneBoolResponseValue(value *bool) *bool {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func appendUniqueProjectSkillWarnings(items []string, values ...string) []string {
	if len(values) == 0 {
		return items
	}
	seen := make(map[string]bool, len(items)+len(values))
	out := make([]string, 0, len(items)+len(values))
	for _, value := range append(items, values...) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
