package api

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/cairnlinebridge"
	"github.com/hecatehq/hecate/internal/projectapp"
	"github.com/hecatehq/hecate/internal/projects"
)

const projectCairnlineWriteAuthorityProjectIdentity = "project-identity"

func (h *Handler) projectIdentityWritesUseCairnlineAuthority() bool {
	return h != nil &&
		h.projectCairnlineEmbeddedConnectorEnabled() &&
		h.config.ProjectsCairnlineWriteAuthorityEnabled(projectCairnlineWriteAuthorityProjectIdentity)
}

func (h *Handler) projectCairnlineEmbeddedReplacementModeArmed() bool {
	return h != nil && h.config.ProjectsUseCairnlineOnly()
}

func (h *Handler) createProjectWithCairnlineAuthority(ctx context.Context, project projects.Project) (projects.Project, error) {
	if err := h.validateProjectMetadataDefaultsHecateCompatibility(ctx, project); err != nil {
		return projects.Project{}, err
	}
	if err := h.validateProjectRootsHecateCompatibility(ctx, project); err != nil {
		return projects.Project{}, err
	}
	if err := validateProjectContextSourcesHecateCompatibility(project); err != nil {
		return projects.Project{}, err
	}

	var written cairnline.Project
	err := h.withCairnlineEmbeddedService(ctx, func(service *cairnline.Service) error {
		created, err := service.CreateProject(ctx, cairnlinebridge.Project(project))
		if err != nil {
			return err
		}
		written = created
		return nil
	})
	if err != nil {
		return projects.Project{}, err
	}
	shadow := projectFromCairnlineProjectCreate(project, written)
	if err := h.upsertProjectRuntimeDefaults(ctx, shadow); err != nil {
		return projects.Project{}, err
	}
	if h.projectCairnlineEmbeddedReplacementModeArmed() {
		return shadow, nil
	}
	if shadowed, ok := h.shadowProjectCreateToHecate(ctx, "project_identity_cairnline_authority_create", shadow); ok {
		return shadowed, nil
	}
	return shadow, nil
}

func (h *Handler) deleteProjectWithCairnlineAuthority(ctx context.Context, projectID string) (projectapp.DeleteProjectResult, error) {
	snapshot, err := cairnlinebridge.LoadSnapshot(ctx, h.cairnlineSnapshotSources(), projectID)
	if errors.Is(err, projects.ErrNotFound) {
		return h.deleteCairnlineOnlyProjectWithAuthority(ctx, projectID)
	}
	if err != nil {
		return projectapp.DeleteProjectResult{}, err
	}
	if err := h.deleteProjectIdentityFromCairnline(ctx, snapshot.Project); err != nil {
		return projectapp.DeleteProjectResult{}, projectAppErrorFromCairnlineAuthority(err, "project")
	}
	result, err := h.projectApplication().DeleteProject(ctx, projectID)
	if err == nil {
		return result, nil
	}
	if restoreErr := h.restoreProjectSnapshotToCairnline(ctx, snapshot); restoreErr != nil {
		h.logCairnlineMirrorError(ctx, "project_identity_cairnline_authority_delete_rollback", snapshot.Project.ID, restoreErr)
		return result, errors.Join(err, fmt.Errorf("restore Cairnline project snapshot after failed delete: %w", restoreErr))
	}
	return result, err
}

func (h *Handler) deleteCairnlineOnlyProjectWithAuthority(ctx context.Context, projectID string) (projectapp.DeleteProjectResult, error) {
	var project projects.Project
	var rollback cairnline.Snapshot
	err := h.withCairnlineEmbeddedService(ctx, func(service *cairnline.Service) error {
		item, err := service.GetProject(ctx, strings.TrimSpace(projectID))
		if err != nil {
			return err
		}
		project = projectFromCairnline(item, projects.Project{})
		rollback, err = service.ExportSnapshot(ctx)
		if err != nil {
			return err
		}
		rollback = cairnlinebridge.CairnlineSnapshotForProject(rollback, project.ID)
		return cairnlinebridge.DeleteProject(ctx, service, project)
	})
	if errors.Is(err, cairnline.ErrNotFound) {
		return projectapp.DeleteProjectResult{}, projectapp.ErrProjectNotFound
	}
	if err != nil {
		return projectapp.DeleteProjectResult{}, projectAppErrorFromCairnlineAuthority(err, "project")
	}
	result, err := h.projectApplication().DeleteProjectScopedRows(ctx, project)
	if err == nil {
		return result, nil
	}
	if restoreErr := h.restoreCairnlineSnapshot(ctx, rollback); restoreErr != nil {
		h.logCairnlineMirrorError(ctx, "project_identity_cairnline_authority_delete_cairnline_only_rollback", project.ID, restoreErr)
		return result, errors.Join(err, fmt.Errorf("restore Cairnline snapshot after failed delete: %w", restoreErr))
	}
	return result, err
}

func (h *Handler) restoreCairnlineSnapshot(ctx context.Context, snapshot cairnline.Snapshot) error {
	return h.withCairnlineEmbeddedService(ctx, func(service *cairnline.Service) error {
		_, err := service.ImportSnapshot(ctx, snapshot)
		return err
	})
}

func (h *Handler) restoreProjectSnapshotToCairnline(ctx context.Context, snapshot cairnlinebridge.Snapshot) error {
	return h.withCairnlineEmbeddedService(ctx, func(service *cairnline.Service) error {
		return cairnlinebridge.Seed(ctx, service, snapshot)
	})
}

func projectFromCairnlineProjectCreate(native projects.Project, written cairnline.Project) projects.Project {
	project := native
	project.ID = written.ID
	project.Name = written.Name
	project.Description = written.Description
	project.Roots = projectRootsFromCairnline(written.Roots, nil)
	project.ContextSources = projectContextSourcesFromCairnline(written.ContextSources)
	project.DefaultRootID = written.DefaultRootID
	project.CreatedAt = written.CreatedAt
	project.UpdatedAt = written.UpdatedAt
	return project
}

func (h *Handler) shadowProjectCreateToHecate(ctx context.Context, operation string, project projects.Project) (projects.Project, bool) {
	if h == nil || h.projects == nil {
		return project, false
	}
	created, err := h.projects.Create(ctx, project)
	if err != nil {
		h.logCairnlineMirrorError(ctx, operation, project.ID, err)
		return project, false
	}
	return created, true
}
