package api

import (
	"context"
	"errors"
	"strings"

	"github.com/hecatehq/hecate/internal/projectruntime"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectwork"
)

func (h *Handler) projectWithHecateRuntimeOverlay(ctx context.Context, project projects.Project) (projects.Project, error) {
	if h == nil || project.ID == "" {
		return project, nil
	}
	if h.projectRuntime != nil {
		defaults, ok, err := h.projectRuntime.GetProjectDefaults(ctx, project.ID)
		if err != nil {
			return project, err
		}
		if ok {
			return overlayProjectRuntimeDefaults(project, defaults), nil
		}
	}
	if h.projects == nil {
		return project, nil
	}
	native, ok, err := h.projects.Get(ctx, project.ID)
	if err != nil || !ok {
		return project, err
	}
	return overlayProjectHecateRuntime(project, native), nil
}

func overlayProjectHecateRuntime(project, native projects.Project) projects.Project {
	project.DefaultProvider = native.DefaultProvider
	project.DefaultModel = native.DefaultModel
	if project.DefaultAgentProfile == "" {
		project.DefaultAgentProfile = native.DefaultAgentProfile
	}
	project.DefaultToolsEnabled = cloneBool(native.DefaultToolsEnabled)
	project.DefaultWorkspaceMode = native.DefaultWorkspaceMode
	project.DefaultSystemPrompt = native.DefaultSystemPrompt
	project.DefaultCompactToolOutput = cloneBool(native.DefaultCompactToolOutput)
	return project
}

func overlayProjectRuntimeDefaults(project projects.Project, defaults projectruntime.ProjectDefaults) projects.Project {
	project.DefaultProvider = defaults.DefaultProvider
	project.DefaultModel = defaults.DefaultModel
	if project.DefaultAgentProfile == "" {
		project.DefaultAgentProfile = defaults.DefaultAgentProfile
	}
	project.DefaultToolsEnabled = cloneBool(defaults.DefaultToolsEnabled)
	project.DefaultWorkspaceMode = defaults.DefaultWorkspaceMode
	project.DefaultSystemPrompt = defaults.DefaultSystemPrompt
	project.DefaultCompactToolOutput = cloneBool(defaults.DefaultCompactToolOutput)
	return project
}

func (h *Handler) projectRoleWithHecateRuntimeOverlay(ctx context.Context, role projectwork.AgentRoleProfile) (projectwork.AgentRoleProfile, error) {
	if h == nil || role.ProjectID == "" || role.ID == "" {
		return role, nil
	}
	if h.projectRuntime != nil {
		defaults, ok, err := h.projectRuntime.GetRoleDefaults(ctx, role.ProjectID, role.ID)
		if err != nil {
			return role, err
		}
		if ok {
			return overlayProjectRoleRuntimeDefaults(role, defaults), nil
		}
	}
	if h.projectWork == nil {
		return role, nil
	}
	native, ok, err := h.loadProjectWorkRoleForCairnlineMirror(ctx, role.ProjectID, role.ID)
	if err != nil || !ok {
		return role, err
	}
	return overlayProjectRoleHecateRuntime(role, native), nil
}

func (h *Handler) projectRolesWithHecateRuntimeOverlay(ctx context.Context, roles []projectwork.AgentRoleProfile) ([]projectwork.AgentRoleProfile, error) {
	out := make([]projectwork.AgentRoleProfile, 0, len(roles))
	for _, role := range roles {
		overlaid, err := h.projectRoleWithHecateRuntimeOverlay(ctx, role)
		if err != nil {
			return nil, err
		}
		out = append(out, overlaid)
	}
	return out, nil
}

func overlayProjectRoleRuntimeDefaults(role projectwork.AgentRoleProfile, defaults projectruntime.RoleDefaults) projectwork.AgentRoleProfile {
	role.DefaultProvider = defaults.DefaultProvider
	role.DefaultModel = defaults.DefaultModel
	if role.DefaultAgentProfile == "" {
		role.DefaultAgentProfile = defaults.DefaultAgentProfile
	}
	return role
}

func overlayProjectRoleHecateRuntime(role, native projectwork.AgentRoleProfile) projectwork.AgentRoleProfile {
	role.DefaultProvider = native.DefaultProvider
	role.DefaultModel = native.DefaultModel
	if role.DefaultAgentProfile == "" {
		role.DefaultAgentProfile = native.DefaultAgentProfile
	}
	role.BuiltIn = native.BuiltIn
	role.CreatedAt = native.CreatedAt
	role.UpdatedAt = native.UpdatedAt
	return role
}

func projectRuntimeDefaultsFromProject(project projects.Project) projectruntime.ProjectDefaults {
	return projectruntime.ProjectDefaults{
		ProjectID:                project.ID,
		DefaultProvider:          project.DefaultProvider,
		DefaultModel:             project.DefaultModel,
		DefaultAgentProfile:      project.DefaultAgentProfile,
		DefaultToolsEnabled:      cloneBool(project.DefaultToolsEnabled),
		DefaultWorkspaceMode:     project.DefaultWorkspaceMode,
		DefaultSystemPrompt:      project.DefaultSystemPrompt,
		DefaultCompactToolOutput: cloneBool(project.DefaultCompactToolOutput),
	}
}

func projectRuntimeDefaultsFromRole(role projectwork.AgentRoleProfile) projectruntime.RoleDefaults {
	return projectruntime.RoleDefaults{
		ProjectID:           role.ProjectID,
		RoleID:              role.ID,
		DefaultProvider:     role.DefaultProvider,
		DefaultModel:        role.DefaultModel,
		DefaultAgentProfile: role.DefaultAgentProfile,
	}
}

func (h *Handler) upsertProjectRuntimeDefaults(ctx context.Context, project projects.Project) error {
	if h == nil || h.projectRuntime == nil {
		return nil
	}
	defaults := projectRuntimeDefaultsFromProject(project)
	if !projectRuntimeDefaultsHasValues(defaults) {
		if _, ok, err := h.projectRuntime.GetProjectDefaults(ctx, defaults.ProjectID); err != nil || !ok {
			return err
		}
	}
	_, err := h.projectRuntime.UpsertProjectDefaults(ctx, defaults)
	return err
}

func (h *Handler) upsertProjectRoleRuntimeDefaults(ctx context.Context, role projectwork.AgentRoleProfile) error {
	if h == nil || h.projectRuntime == nil {
		return nil
	}
	defaults := projectRuntimeDefaultsFromRole(role)
	if !projectRoleRuntimeDefaultsHasValues(defaults) {
		if _, ok, err := h.projectRuntime.GetRoleDefaults(ctx, defaults.ProjectID, defaults.RoleID); err != nil || !ok {
			return err
		}
	}
	_, err := h.projectRuntime.UpsertRoleDefaults(ctx, defaults)
	return err
}

func (h *Handler) deleteProjectRoleRuntimeDefaults(ctx context.Context, projectID, roleID string) error {
	if h == nil || h.projectRuntime == nil {
		return nil
	}
	err := h.projectRuntime.DeleteRoleDefaults(ctx, projectID, roleID)
	if errors.Is(err, projectruntime.ErrNotFound) {
		return nil
	}
	return err
}

func projectRuntimeDefaultsHasValues(defaults projectruntime.ProjectDefaults) bool {
	return strings.TrimSpace(defaults.DefaultProvider) != "" ||
		strings.TrimSpace(defaults.DefaultModel) != "" ||
		strings.TrimSpace(defaults.DefaultAgentProfile) != "" ||
		defaults.DefaultToolsEnabled != nil ||
		strings.TrimSpace(defaults.DefaultWorkspaceMode) != "" ||
		strings.TrimSpace(defaults.DefaultSystemPrompt) != "" ||
		defaults.DefaultCompactToolOutput != nil
}

func projectRoleRuntimeDefaultsHasValues(defaults projectruntime.RoleDefaults) bool {
	return strings.TrimSpace(defaults.DefaultProvider) != "" ||
		strings.TrimSpace(defaults.DefaultModel) != "" ||
		strings.TrimSpace(defaults.DefaultAgentProfile) != ""
}
