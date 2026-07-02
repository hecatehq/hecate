package api

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/cairnlinebridge"
	"github.com/hecatehq/hecate/internal/projects"
)

const projectCairnlineWriteAuthorityProjectMetadataDefaults = "project-metadata-defaults"

func (h *Handler) projectMetadataDefaultsWritesUseCairnlineAuthority() bool {
	return h != nil &&
		h.projectCairnlineEmbeddedConnectorEnabled() &&
		h.config.ProjectsCairnlineWriteAuthorityEnabled(projectCairnlineWriteAuthorityProjectMetadataDefaults)
}

func projectUpdateCanUseCairnlineMetadataDefaultsAuthority(req updateProjectRequest) bool {
	if req.Roots != nil || req.ContextSources != nil || req.LastOpenedAt != nil {
		return false
	}
	return projectUpdateTouchesPortableMetadata(req) || projectUpdateTouchesPortableDefaults(req)
}

func (h *Handler) updateProjectMetadataDefaultsWithCairnlineAuthority(ctx context.Context, projectID string, req updateProjectRequest) (projects.Project, error) {
	project, err := h.projectForCairnlineWriteAuthority(ctx, projectID)
	if err != nil {
		return projects.Project{}, err
	}
	applyProjectMetadataDefaultsUpdate(&project, req)
	if err := validateProjectDefaultRoot(project.DefaultRootID, project.Roots); err != nil {
		return projects.Project{}, errors.Join(projects.ErrInvalid, err)
	}
	if err := h.validateProjectMetadataDefaultsHecateCompatibility(ctx, project); err != nil {
		return projects.Project{}, err
	}

	err = h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		if err := h.seedProjectMetadataDefaultsDependenciesForCairnlineAuthority(ctx, service, project); err != nil {
			return err
		}
		if projectUpdateTouchesPortableMetadata(req) {
			if _, err := cairnlinebridge.UpsertProjectMetadata(ctx, service, project); err != nil {
				return err
			}
		}
		if projectUpdateTouchesPortableDefaults(req) {
			if _, err := cairnlinebridge.UpsertProjectDefaults(ctx, service, project); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return projects.Project{}, err
	}
	if shadowed, ok := h.shadowProjectMetadataDefaultsToHecate(ctx, "project_metadata_defaults_cairnline_authority_update", project); ok {
		project = shadowed
	}
	return project, nil
}

func applyProjectMetadataDefaultsUpdate(project *projects.Project, req updateProjectRequest) {
	if project == nil {
		return
	}
	if req.Name != nil {
		project.Name = strings.TrimSpace(*req.Name)
	}
	if req.Description != nil {
		project.Description = strings.TrimSpace(*req.Description)
	}
	if req.DefaultRootID != nil {
		project.DefaultRootID = strings.TrimSpace(*req.DefaultRootID)
	}
	if req.DefaultProvider != nil {
		project.DefaultProvider = strings.TrimSpace(*req.DefaultProvider)
	}
	if req.DefaultModel != nil {
		project.DefaultModel = strings.TrimSpace(*req.DefaultModel)
	}
	if req.DefaultAgentProfile != nil {
		project.DefaultAgentProfile = strings.TrimSpace(*req.DefaultAgentProfile)
	}
	if req.DefaultToolsEnabled != nil {
		project.DefaultToolsEnabled = cloneBool(req.DefaultToolsEnabled)
	}
	if req.DefaultWorkspaceMode != nil {
		project.DefaultWorkspaceMode = strings.TrimSpace(*req.DefaultWorkspaceMode)
	}
	if req.DefaultSystemPrompt != nil {
		project.DefaultSystemPrompt = strings.TrimSpace(*req.DefaultSystemPrompt)
	}
	if req.DefaultCompactToolOutput != nil {
		project.DefaultCompactToolOutput = cloneBool(req.DefaultCompactToolOutput)
	}
}

func (h *Handler) validateProjectMetadataDefaultsHecateCompatibility(ctx context.Context, project projects.Project) error {
	if strings.TrimSpace(project.Name) == "" {
		return fmt.Errorf("%w: project name is required", projects.ErrInvalid)
	}
	if h != nil && h.projects != nil {
		items, err := h.projects.List(ctx)
		if err != nil {
			return err
		}
		projectID := strings.TrimSpace(project.ID)
		nameKey := strings.ToLower(strings.TrimSpace(project.Name))
		for _, existing := range items {
			if strings.TrimSpace(existing.ID) == projectID {
				continue
			}
			if strings.ToLower(strings.TrimSpace(existing.Name)) == nameKey {
				return fmt.Errorf("%w: project name %q already exists", projects.ErrAlreadyExists, project.Name)
			}
		}
	}
	return h.validateProjectNameCairnlineAuthorityCompatibility(ctx, project)
}

func (h *Handler) validateProjectNameCairnlineAuthorityCompatibility(ctx context.Context, project projects.Project) error {
	name := strings.TrimSpace(project.Name)
	if name == "" {
		return fmt.Errorf("%w: project name is required", projects.ErrInvalid)
	}
	if h == nil || !h.projectCairnlineEmbeddedConnectorEnabled() {
		return nil
	}
	projectID := strings.TrimSpace(project.ID)
	nameKey := strings.ToLower(name)
	var conflict string
	err := h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		items, err := service.ListProjects(ctx)
		if err != nil {
			return err
		}
		for _, existing := range items {
			if strings.TrimSpace(existing.ID) == projectID {
				continue
			}
			if strings.ToLower(strings.TrimSpace(existing.Name)) == nameKey {
				conflict = existing.Name
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if conflict != "" {
		return fmt.Errorf("%w: project name %q already exists", projects.ErrAlreadyExists, name)
	}
	return nil
}

func (h *Handler) seedProjectMetadataDefaultsDependenciesForCairnlineAuthority(ctx context.Context, service *cairnline.Service, project projects.Project) error {
	if strings.TrimSpace(project.DefaultRootID) != "" {
		root, ok := projectRootForCairnlineMirror(project, project.DefaultRootID)
		if !ok {
			return errors.Join(projects.ErrInvalid, errors.New("default project root not found for Cairnline authority"))
		}
		if _, err := cairnlinebridge.UpsertRoot(ctx, service, project, root); err != nil {
			return err
		}
	}
	if strings.TrimSpace(project.DefaultAgentProfile) != "" {
		if h == nil || h.agentProfiles == nil {
			return nil
		}
		profile, ok, err := h.agentProfiles.Get(ctx, project.DefaultAgentProfile)
		if err != nil {
			return err
		}
		if ok {
			if _, err := cairnlinebridge.UpsertAgentProfile(ctx, service, profile); err != nil {
				return err
			}
		}
	}
	return nil
}

func (h *Handler) shadowProjectMetadataDefaultsToHecate(ctx context.Context, operation string, project projects.Project) (projects.Project, bool) {
	if h == nil || h.projects == nil {
		return project, false
	}
	updated, err := h.projects.Update(ctx, project.ID, func(item *projects.Project) {
		item.Name = project.Name
		item.Description = project.Description
		item.DefaultRootID = project.DefaultRootID
		item.DefaultProvider = project.DefaultProvider
		item.DefaultModel = project.DefaultModel
		item.DefaultAgentProfile = project.DefaultAgentProfile
		item.DefaultToolsEnabled = cloneBool(project.DefaultToolsEnabled)
		item.DefaultWorkspaceMode = project.DefaultWorkspaceMode
		item.DefaultSystemPrompt = project.DefaultSystemPrompt
		item.DefaultCompactToolOutput = cloneBool(project.DefaultCompactToolOutput)
	})
	if err != nil {
		h.logCairnlineMirrorError(ctx, operation, project.ID, err)
		return project, false
	}
	return updated, true
}
