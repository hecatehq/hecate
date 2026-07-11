package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/cairnlinebridge"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
)

const projectCairnlineWriteAuthorityProjectSkills = "project-skills"

func (h *Handler) projectSkillWritesUseCairnlineAuthority() bool {
	return h != nil &&
		h.projectCairnlineEmbeddedConnectorEnabled() &&
		h.config.ProjectsCairnlineWriteAuthorityEnabled(projectCairnlineWriteAuthorityProjectSkills)
}

func (h *Handler) projectForProjectSkillMutation(ctx context.Context, projectID string, usesCairnlineAuthority bool) (projects.Project, error) {
	if usesCairnlineAuthority {
		return h.projectForCairnlineWriteAuthority(ctx, projectID)
	}
	if h == nil || h.projects == nil {
		return projects.Project{}, errors.New("project store is not configured")
	}
	project, ok, err := h.projects.Get(ctx, projectID)
	if err != nil {
		return projects.Project{}, err
	}
	if !ok {
		return projects.Project{}, projects.ErrNotFound
	}
	return project, nil
}

func (h *Handler) discoverProjectSkillsWithCairnlineAuthority(ctx context.Context, project projects.Project) ([]projectskills.Skill, error) {
	var recorded []projectskills.Skill
	err := h.withCairnlineEmbeddedService(ctx, func(service *cairnline.Service) error {
		if _, err := cairnlinebridge.UpsertProjectMetadata(ctx, service, project); err != nil {
			return err
		}
		items, err := service.DiscoverProjectSkills(ctx, project.ID)
		if err != nil {
			return err
		}
		recorded = projectSkillsFromCairnline(items)
		return nil
	})
	if err != nil {
		return nil, err
	}
	h.shadowProjectSkillsToHecate(ctx, "project_skills_cairnline_authority_discover", project.ID, recorded)
	return recorded, nil
}

func (h *Handler) updateProjectSkillWithCairnlineAuthority(ctx context.Context, project projects.Project, skillID string, req updateProjectSkillRequest) (projectskills.Skill, error) {
	var recorded projectskills.Skill
	err := h.withCairnlineEmbeddedService(ctx, func(service *cairnline.Service) error {
		if _, err := cairnlinebridge.UpsertProjectMetadata(ctx, service, project); err != nil {
			return err
		}
		existing, err := service.GetProjectSkill(ctx, project.ID, skillID)
		if err != nil {
			return err
		}
		item := projectSkillFromCairnline(existing, projectskills.Skill{})
		applyProjectSkillUpdate(&item, req)
		updated, err := cairnlinebridge.UpsertProjectSkill(ctx, service, item)
		if err != nil {
			return err
		}
		recorded = projectSkillFromCairnline(updated, projectskills.Skill{})
		return nil
	})
	if err != nil {
		return projectskills.Skill{}, err
	}
	h.shadowProjectSkillUpdateToHecate(ctx, "project_skill_cairnline_authority_update", project.ID, recorded)
	return recorded, nil
}

func applyProjectSkillUpdate(item *projectskills.Skill, req updateProjectSkillRequest) {
	if item == nil {
		return
	}
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
}

func projectSkillsFromCairnline(items []cairnline.ProjectSkill) []projectskills.Skill {
	out := make([]projectskills.Skill, 0, len(items))
	for _, item := range items {
		out = append(out, projectSkillFromCairnline(item, projectskills.Skill{}))
	}
	return out
}

func (h *Handler) shadowProjectSkillsToHecate(ctx context.Context, operation, projectID string, skills []projectskills.Skill) {
	if h == nil || h.projectSkills == nil || len(skills) == 0 {
		return
	}
	if h.projectCairnlineEmbeddedReplacementModeArmed() {
		return
	}
	if _, err := h.projectSkills.UpsertDiscovered(ctx, projectID, skills); err != nil {
		h.logCairnlineMirrorError(ctx, operation, projectID, err)
	}
}

func (h *Handler) shadowProjectSkillUpdateToHecate(ctx context.Context, operation, projectID string, skill projectskills.Skill) {
	if h == nil || h.projectSkills == nil {
		return
	}
	if h.projectCairnlineEmbeddedReplacementModeArmed() {
		return
	}
	_, err := h.projectSkills.Update(ctx, projectID, skill.ID, func(item *projectskills.Skill) {
		item.Title = skill.Title
		item.Description = skill.Description
		item.Path = skill.Path
		item.RootID = skill.RootID
		item.Format = skill.Format
		item.SuggestedTools = append([]string(nil), skill.SuggestedTools...)
		item.RequiredPermissions = skill.RequiredPermissions
		item.Enabled = skill.Enabled
		item.Status = skill.Status
		item.TrustLabel = skill.TrustLabel
		item.SourceContextSourceIDs = append([]string(nil), skill.SourceContextSourceIDs...)
		item.Warnings = append([]string(nil), skill.Warnings...)
	})
	if err == nil {
		return
	}
	if errors.Is(err, projectskills.ErrNotFound) {
		h.shadowProjectSkillsToHecate(ctx, operation, projectID, []projectskills.Skill{skill})
		return
	}
	h.logCairnlineMirrorError(ctx, operation, projectID, err)
}

func writeProjectSkillProjectError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, projects.ErrNotFound) || errors.Is(err, cairnline.ErrNotFound) {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
		return true
	}
	WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
	return true
}

func writeProjectSkillAuthorityError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, projectskills.ErrNotFound) || errors.Is(err, cairnline.ErrNotFound) {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project skill not found")
		return true
	}
	if errors.Is(err, projectskills.ErrInvalid) || errors.Is(err, cairnline.ErrInvalid) {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return true
	}
	if errors.Is(err, cairnline.ErrDuplicate) || errors.Is(err, cairnline.ErrConflict) {
		WriteError(w, http.StatusConflict, errCodeConflict, err.Error())
		return true
	}
	WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
	return true
}
