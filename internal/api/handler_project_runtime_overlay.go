package api

import (
	"context"

	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectwork"
)

func (h *Handler) projectWithHecateRuntimeOverlay(ctx context.Context, project projects.Project) (projects.Project, error) {
	if h == nil || h.projects == nil || project.ID == "" {
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

func (h *Handler) projectRoleWithHecateRuntimeOverlay(ctx context.Context, role projectwork.AgentRoleProfile) (projectwork.AgentRoleProfile, error) {
	if h == nil || h.projectWork == nil || role.ProjectID == "" || role.ID == "" {
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
