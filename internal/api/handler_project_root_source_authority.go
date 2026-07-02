package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/cairnlinebridge"
	"github.com/hecatehq/hecate/internal/projectapp"
	"github.com/hecatehq/hecate/internal/projects"
)

const (
	projectCairnlineWriteAuthorityProjectRoots          = "project-roots"
	projectCairnlineWriteAuthorityProjectContextSources = "project-context-sources"
)

var errProjectStoreNotConfigured = errors.New("project store is not configured")

func (h *Handler) projectRootWritesUseCairnlineAuthority() bool {
	return h != nil &&
		h.projectCairnlineEmbeddedConnectorEnabled() &&
		h.config.ProjectsCairnlineWriteAuthorityEnabled(projectCairnlineWriteAuthorityProjectRoots)
}

func (h *Handler) projectContextSourceWritesUseCairnlineAuthority() bool {
	return h != nil &&
		h.projectCairnlineEmbeddedConnectorEnabled() &&
		h.config.ProjectsCairnlineWriteAuthorityEnabled(projectCairnlineWriteAuthorityProjectContextSources)
}

func (h *Handler) projectForRootSourceMutation(ctx context.Context, projectID string, usesCairnlineAuthority bool) (projects.Project, error) {
	if usesCairnlineAuthority {
		return h.projectForCairnlineWriteAuthority(ctx, projectID)
	}
	if h == nil || h.projects == nil {
		return projects.Project{}, errProjectStoreNotConfigured
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

func (h *Handler) projectRootSourceListUpdateWritesUseCairnlineAuthority(req updateProjectRequest) bool {
	if req.Roots == nil && req.ContextSources == nil {
		return false
	}
	if req.LastOpenedAt != nil || projectUpdateTouchesPortableMetadata(req) {
		return false
	}
	if req.DefaultProvider != nil ||
		req.DefaultModel != nil ||
		req.DefaultAgentProfile != nil ||
		req.DefaultToolsEnabled != nil ||
		req.DefaultWorkspaceMode != nil ||
		req.DefaultSystemPrompt != nil ||
		req.DefaultCompactToolOutput != nil {
		return false
	}
	if req.DefaultRootID != nil && req.Roots == nil {
		return false
	}
	if req.Roots != nil && !h.projectRootWritesUseCairnlineAuthority() {
		return false
	}
	if req.ContextSources != nil && !h.projectContextSourceWritesUseCairnlineAuthority() {
		return false
	}
	return true
}

func (h *Handler) updateProjectRootSourceListsWithCairnlineAuthority(ctx context.Context, projectID string, req updateProjectRequest, roots []projects.Root, sources []projects.ContextSource) (projects.Project, error) {
	project, err := h.projectForCairnlineWriteAuthority(ctx, projectID)
	if err != nil {
		return projects.Project{}, projectAppErrorFromCairnlineAuthority(err, "project")
	}
	if req.Roots != nil {
		project.Roots = append([]projects.Root(nil), roots...)
		if req.DefaultRootID != nil {
			project.DefaultRootID = strings.TrimSpace(*req.DefaultRootID)
		} else if !projectRootIDExists(project.DefaultRootID, project.Roots) {
			project.DefaultRootID = ""
		}
		if project.DefaultRootID == "" && len(project.Roots) > 0 {
			project.DefaultRootID = project.Roots[0].ID
		}
	}
	if req.ContextSources != nil {
		project.ContextSources = append([]projects.ContextSource(nil), sources...)
	}
	if err := h.validateProjectRootsHecateCompatibility(ctx, project); err != nil {
		return projects.Project{}, err
	}
	if err := validateProjectContextSourcesHecateCompatibility(project); err != nil {
		return projects.Project{}, err
	}

	var written cairnline.Project
	err = h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		if _, err := cairnlinebridge.UpsertProjectMetadata(ctx, service, project); err != nil {
			return err
		}
		next := cairnline.Project{}
		if req.Roots != nil {
			updated, err := cairnlinebridge.ReplaceProjectRoots(ctx, service, project, project.Roots)
			if err != nil {
				return err
			}
			next = updated
		}
		if req.ContextSources != nil {
			updated, err := cairnlinebridge.ReplaceProjectContextSources(ctx, service, project, project.ContextSources)
			if err != nil {
				return err
			}
			next = updated
		}
		written = next
		return nil
	})
	if err != nil {
		return projects.Project{}, projectAppErrorFromCairnlineAuthority(err, "project")
	}
	if shadowed, ok := h.shadowProjectRootSourceListsToHecate(ctx, "project_root_source_lists_cairnline_authority_replace", project, written, req.Roots != nil, req.ContextSources != nil); ok {
		return shadowed, nil
	}
	return projectFromCairnlineRootSourceListReplace(project, written, req.Roots != nil, req.ContextSources != nil), nil
}

func (h *Handler) replaceProjectContextSourcesWithCairnlineAuthority(ctx context.Context, projectID string, sources []projects.ContextSource, operation string) (projects.Project, error) {
	project, err := h.projectForCairnlineWriteAuthority(ctx, projectID)
	if err != nil {
		return projects.Project{}, projectAppErrorFromCairnlineAuthority(err, "project")
	}
	project.ContextSources = append([]projects.ContextSource(nil), sources...)
	if err := validateProjectContextSourcesHecateCompatibility(project); err != nil {
		return projects.Project{}, err
	}
	var written cairnline.Project
	err = h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		if _, err := cairnlinebridge.UpsertProjectMetadata(ctx, service, project); err != nil {
			return err
		}
		updated, err := cairnlinebridge.ReplaceProjectContextSources(ctx, service, project, project.ContextSources)
		if err != nil {
			return err
		}
		written = updated
		return nil
	})
	if err != nil {
		return projects.Project{}, projectAppErrorFromCairnlineAuthority(err, "project")
	}
	if shadowed, ok := h.shadowProjectRootSourceListsToHecate(ctx, operation, project, written, false, true); ok {
		return shadowed, nil
	}
	return projectFromCairnlineRootSourceListReplace(project, written, false, true), nil
}

func (h *Handler) replaceProjectRootsWithCairnlineAuthority(ctx context.Context, projectID string, roots []projects.Root, defaultRootID string, operation string) (projects.Project, error) {
	project, err := h.projectForCairnlineWriteAuthority(ctx, projectID)
	if err != nil {
		return projects.Project{}, projectAppErrorFromCairnlineAuthority(err, "project")
	}
	project.Roots = append([]projects.Root(nil), roots...)
	project.DefaultRootID = strings.TrimSpace(defaultRootID)
	if err := h.validateProjectRootsHecateCompatibility(ctx, project); err != nil {
		return projects.Project{}, err
	}
	var written cairnline.Project
	err = h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		if _, err := cairnlinebridge.UpsertProjectMetadata(ctx, service, project); err != nil {
			return err
		}
		updated, err := cairnlinebridge.ReplaceProjectRoots(ctx, service, project, project.Roots)
		if err != nil {
			return err
		}
		written = updated
		return nil
	})
	if err != nil {
		return projects.Project{}, projectAppErrorFromCairnlineAuthority(err, "project")
	}
	if shadowed, ok := h.shadowProjectRootSourceListsToHecate(ctx, operation, project, written, true, false); ok {
		return shadowed, nil
	}
	return projectFromCairnlineRootSourceListReplace(project, written, true, false), nil
}

func (h *Handler) createProjectRootWithCairnlineAuthority(ctx context.Context, projectID string, root projects.Root) (projects.Project, projects.Root, error) {
	project, err := h.projectForCairnlineWriteAuthority(ctx, projectID)
	if err != nil {
		return projects.Project{}, projects.Root{}, projectAppErrorFromCairnlineAuthority(err, "project")
	}
	root.ID = strings.TrimSpace(root.ID)
	if root.ID == "" {
		return projects.Project{}, projects.Root{}, fmt.Errorf("%w: project root id is required", projects.ErrInvalid)
	}
	if projectRootIDExists(root.ID, project.Roots) {
		return projects.Project{}, projects.Root{}, fmt.Errorf("%w: project root %q already exists", projectapp.ErrProjectRootConflict, root.ID)
	}
	candidate := project
	candidate.Roots = append(append([]projects.Root(nil), project.Roots...), root)
	if err := h.validateProjectRootsHecateCompatibility(ctx, candidate); err != nil {
		return projects.Project{}, projects.Root{}, err
	}

	var written cairnline.Project
	var created cairnline.Root
	err = h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		if _, err := cairnlinebridge.UpsertProjectMetadata(ctx, service, project); err != nil {
			return err
		}
		next, item, err := service.CreateRoot(ctx, project.ID, cairnlinebridge.Root(root))
		if err != nil {
			return err
		}
		written = next
		created = item
		return nil
	})
	if err != nil {
		return projects.Project{}, projects.Root{}, projectAppErrorFromCairnlineAuthority(err, "root")
	}
	project, createdRoot := h.shadowProjectRootsToHecate(ctx, "project_root_cairnline_authority_create", project, written, created.ID)
	return project, createdRoot, nil
}

func (h *Handler) updateProjectRootWithCairnlineAuthority(ctx context.Context, projectID, rootID string, root projects.Root) (projects.Project, projects.Root, error) {
	project, err := h.projectForCairnlineWriteAuthority(ctx, projectID)
	if err != nil {
		return projects.Project{}, projects.Root{}, projectAppErrorFromCairnlineAuthority(err, "project")
	}
	rootID = strings.TrimSpace(rootID)
	if rootID == "" {
		return projects.Project{}, projects.Root{}, projectapp.ErrProjectRootNotFound
	}
	if _, ok := findProjectRootByID(project.Roots, rootID); !ok {
		return projects.Project{}, projects.Root{}, projectapp.ErrProjectRootNotFound
	}
	root.ID = rootID
	candidate := project
	candidate.Roots = replaceProjectRootByID(project.Roots, rootID, root)
	if err := h.validateProjectRootsHecateCompatibility(ctx, candidate); err != nil {
		return projects.Project{}, projects.Root{}, err
	}

	var written cairnline.Project
	var updated cairnline.Root
	err = h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		if _, err := cairnlinebridge.UpsertProjectMetadata(ctx, service, project); err != nil {
			return err
		}
		next, item, err := service.UpdateRoot(ctx, project.ID, rootID, cairnlinebridge.Root(root))
		if err != nil {
			return err
		}
		written = next
		updated = item
		return nil
	})
	if err != nil {
		return projects.Project{}, projects.Root{}, projectAppErrorFromCairnlineAuthority(err, "root")
	}
	project, updatedRoot := h.shadowProjectRootsToHecate(ctx, "project_root_cairnline_authority_update", project, written, updated.ID)
	return project, updatedRoot, nil
}

func (h *Handler) deleteProjectRootWithCairnlineAuthority(ctx context.Context, projectID, rootID string) (projects.Project, projects.Root, error) {
	project, err := h.projectForCairnlineWriteAuthority(ctx, projectID)
	if err != nil {
		return projects.Project{}, projects.Root{}, projectAppErrorFromCairnlineAuthority(err, "project")
	}
	rootID = strings.TrimSpace(rootID)
	if rootID == "" {
		return projects.Project{}, projects.Root{}, projectapp.ErrProjectRootNotFound
	}
	deleted, ok := findProjectRootByID(project.Roots, rootID)
	if !ok {
		return projects.Project{}, projects.Root{}, projectapp.ErrProjectRootNotFound
	}
	var written cairnline.Project
	err = h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		if _, err := cairnlinebridge.UpsertProjectMetadata(ctx, service, project); err != nil {
			return err
		}
		next, _, err := service.DeleteRoot(ctx, project.ID, rootID)
		if err != nil {
			return err
		}
		written = next
		return nil
	})
	if err != nil {
		return projects.Project{}, projects.Root{}, projectAppErrorFromCairnlineAuthority(err, "root")
	}
	project, _ = h.shadowProjectRootsToHecate(ctx, "project_root_cairnline_authority_delete", project, written, rootID)
	return project, deleted, nil
}

func (h *Handler) createProjectContextSourceWithCairnlineAuthority(ctx context.Context, projectID string, source projects.ContextSource) (projects.Project, projects.ContextSource, error) {
	project, err := h.projectForCairnlineWriteAuthority(ctx, projectID)
	if err != nil {
		return projects.Project{}, projects.ContextSource{}, projectAppErrorFromCairnlineAuthority(err, "project")
	}
	source.ID = strings.TrimSpace(source.ID)
	if source.ID == "" {
		return projects.Project{}, projects.ContextSource{}, fmt.Errorf("%w: context source id is required", projects.ErrInvalid)
	}
	if projectContextSourceIDExists(source.ID, project.ContextSources) {
		return projects.Project{}, projects.ContextSource{}, fmt.Errorf("%w: context source %q already exists", projectapp.ErrProjectContextSourceConflict, source.ID)
	}
	candidate := project
	candidate.ContextSources = append(append([]projects.ContextSource(nil), project.ContextSources...), source)
	if err := validateProjectContextSourcesHecateCompatibility(candidate); err != nil {
		return projects.Project{}, projects.ContextSource{}, err
	}

	var written cairnline.Project
	var created cairnline.Source
	err = h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		if _, err := cairnlinebridge.UpsertProjectMetadata(ctx, service, project); err != nil {
			return err
		}
		next, item, err := service.CreateContextSource(ctx, project.ID, cairnlinebridge.Source(source))
		if err != nil {
			return err
		}
		written = next
		created = item
		return nil
	})
	if err != nil {
		return projects.Project{}, projects.ContextSource{}, projectAppErrorFromCairnlineAuthority(err, "context-source")
	}
	project, createdSource := h.shadowProjectContextSourcesToHecate(ctx, "project_context_source_cairnline_authority_create", project, written, created.ID)
	return project, createdSource, nil
}

func (h *Handler) updateProjectContextSourceWithCairnlineAuthority(ctx context.Context, projectID, sourceID string, source projects.ContextSource) (projects.Project, projects.ContextSource, error) {
	project, err := h.projectForCairnlineWriteAuthority(ctx, projectID)
	if err != nil {
		return projects.Project{}, projects.ContextSource{}, projectAppErrorFromCairnlineAuthority(err, "project")
	}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return projects.Project{}, projects.ContextSource{}, projectapp.ErrProjectContextSourceNotFound
	}
	if _, ok := findProjectContextSourceByID(project.ContextSources, sourceID); !ok {
		return projects.Project{}, projects.ContextSource{}, projectapp.ErrProjectContextSourceNotFound
	}
	source.ID = sourceID
	candidate := project
	candidate.ContextSources = replaceProjectContextSourceByID(project.ContextSources, sourceID, source)
	if err := validateProjectContextSourcesHecateCompatibility(candidate); err != nil {
		return projects.Project{}, projects.ContextSource{}, err
	}

	var written cairnline.Project
	var updated cairnline.Source
	err = h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		if _, err := cairnlinebridge.UpsertProjectMetadata(ctx, service, project); err != nil {
			return err
		}
		next, item, err := service.UpdateContextSource(ctx, project.ID, sourceID, cairnlinebridge.Source(source))
		if err != nil {
			return err
		}
		written = next
		updated = item
		return nil
	})
	if err != nil {
		return projects.Project{}, projects.ContextSource{}, projectAppErrorFromCairnlineAuthority(err, "context-source")
	}
	project, updatedSource := h.shadowProjectContextSourcesToHecate(ctx, "project_context_source_cairnline_authority_update", project, written, updated.ID)
	return project, updatedSource, nil
}

func (h *Handler) deleteProjectContextSourceWithCairnlineAuthority(ctx context.Context, projectID, sourceID string) (projects.Project, projects.ContextSource, error) {
	project, err := h.projectForCairnlineWriteAuthority(ctx, projectID)
	if err != nil {
		return projects.Project{}, projects.ContextSource{}, projectAppErrorFromCairnlineAuthority(err, "project")
	}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return projects.Project{}, projects.ContextSource{}, projectapp.ErrProjectContextSourceNotFound
	}
	deleted, ok := findProjectContextSourceByID(project.ContextSources, sourceID)
	if !ok {
		return projects.Project{}, projects.ContextSource{}, projectapp.ErrProjectContextSourceNotFound
	}
	var written cairnline.Project
	err = h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		if _, err := cairnlinebridge.UpsertProjectMetadata(ctx, service, project); err != nil {
			return err
		}
		next, _, err := service.DeleteContextSource(ctx, project.ID, sourceID)
		if err != nil {
			return err
		}
		written = next
		return nil
	})
	if err != nil {
		return projects.Project{}, projects.ContextSource{}, projectAppErrorFromCairnlineAuthority(err, "context-source")
	}
	project, _ = h.shadowProjectContextSourcesToHecate(ctx, "project_context_source_cairnline_authority_delete", project, written, sourceID)
	return project, deleted, nil
}

func (h *Handler) validateProjectRootsHecateCompatibility(ctx context.Context, project projects.Project) error {
	if strings.TrimSpace(project.ID) == "" {
		return fmt.Errorf("%w: project id is required", projects.ErrInvalid)
	}
	rootIDs := make(map[string]struct{}, len(project.Roots))
	rootPathKeys := make(map[string]string, len(project.Roots))
	for _, root := range project.Roots {
		rootID := strings.TrimSpace(root.ID)
		if rootID == "" {
			return fmt.Errorf("%w: project root id is required", projects.ErrInvalid)
		}
		rootPath := strings.TrimSpace(root.Path)
		if rootPath == "" {
			return fmt.Errorf("%w: project root path is required", projects.ErrInvalid)
		}
		if _, ok := rootIDs[rootID]; ok {
			return fmt.Errorf("%w: duplicate project root id %q", projects.ErrInvalid, rootID)
		}
		rootIDs[rootID] = struct{}{}
		pathKey := projectRootPathKeyForCairnlineAuthority(rootPath)
		if prior, ok := rootPathKeys[pathKey]; ok {
			return fmt.Errorf("%w: duplicate project root path %q", projects.ErrInvalid, prior)
		}
		rootPathKeys[pathKey] = rootPath
	}
	if project.DefaultRootID != "" {
		if _, ok := rootIDs[strings.TrimSpace(project.DefaultRootID)]; !ok {
			return fmt.Errorf("%w: default_root_id %q does not match a project root", projects.ErrInvalid, project.DefaultRootID)
		}
	}
	if h != nil && h.projects != nil && len(rootPathKeys) > 0 {
		items, err := h.projects.List(ctx)
		if err != nil {
			return err
		}
		projectID := strings.TrimSpace(project.ID)
		for _, existing := range items {
			if strings.TrimSpace(existing.ID) == projectID {
				continue
			}
			for _, root := range existing.Roots {
				if path, ok := rootPathKeys[projectRootPathKeyForCairnlineAuthority(root.Path)]; ok {
					return fmt.Errorf("%w: project root path %q already belongs to project %q", projects.ErrAlreadyExists, path, existing.ID)
				}
			}
		}
	}
	return h.validateProjectRootPathsCairnlineAuthorityCompatibility(ctx, project, rootPathKeys)
}

func (h *Handler) validateProjectRootPathsCairnlineAuthorityCompatibility(ctx context.Context, project projects.Project, rootPathKeys map[string]string) error {
	if len(rootPathKeys) == 0 || h == nil || !h.projectCairnlineEmbeddedConnectorEnabled() {
		return nil
	}
	projectID := strings.TrimSpace(project.ID)
	var conflictPath, conflictProjectID string
	err := h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		items, err := service.ListProjects(ctx)
		if err != nil {
			return err
		}
		for _, existing := range items {
			if strings.TrimSpace(existing.ID) == projectID {
				continue
			}
			for _, root := range existing.Roots {
				if path, ok := rootPathKeys[projectRootPathKeyForCairnlineAuthority(root.Path)]; ok {
					conflictPath = path
					conflictProjectID = existing.ID
					return nil
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if conflictPath != "" {
		return fmt.Errorf("%w: project root path %q already belongs to project %q", projects.ErrAlreadyExists, conflictPath, conflictProjectID)
	}
	return nil
}

func validateProjectContextSourcesHecateCompatibility(project projects.Project) error {
	sourceIDs := make(map[string]struct{}, len(project.ContextSources))
	for _, source := range project.ContextSources {
		sourceID := strings.TrimSpace(source.ID)
		if sourceID == "" {
			return fmt.Errorf("%w: project context source id is required", projects.ErrInvalid)
		}
		if strings.TrimSpace(source.Path) == "" {
			return fmt.Errorf("%w: project context source path is required", projects.ErrInvalid)
		}
		if _, ok := sourceIDs[sourceID]; ok {
			return fmt.Errorf("%w: duplicate project context source id %q", projects.ErrInvalid, sourceID)
		}
		sourceIDs[sourceID] = struct{}{}
	}
	return nil
}

func projectRootPathKeyForCairnlineAuthority(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}

func findProjectRootByID(items []projects.Root, id string) (projects.Root, bool) {
	id = strings.TrimSpace(id)
	for _, item := range items {
		if strings.TrimSpace(item.ID) == id {
			return item, true
		}
	}
	return projects.Root{}, false
}

func replaceProjectRootByID(items []projects.Root, id string, replacement projects.Root) []projects.Root {
	id = strings.TrimSpace(id)
	out := append([]projects.Root(nil), items...)
	for idx := range out {
		if strings.TrimSpace(out[idx].ID) == id {
			out[idx] = replacement
			return out
		}
	}
	return out
}

func projectContextSourceIDExists(id string, items []projects.ContextSource) bool {
	_, ok := findProjectContextSourceByID(items, id)
	return ok
}

func findProjectContextSourceByID(items []projects.ContextSource, id string) (projects.ContextSource, bool) {
	id = strings.TrimSpace(id)
	for _, item := range items {
		if strings.TrimSpace(item.ID) == id {
			return item, true
		}
	}
	return projects.ContextSource{}, false
}

func replaceProjectContextSourceByID(items []projects.ContextSource, id string, replacement projects.ContextSource) []projects.ContextSource {
	id = strings.TrimSpace(id)
	out := append([]projects.ContextSource(nil), items...)
	for idx := range out {
		if strings.TrimSpace(out[idx].ID) == id {
			out[idx] = replacement
			return out
		}
	}
	return out
}

func (h *Handler) shadowProjectRootsToHecate(ctx context.Context, operation string, project projects.Project, written cairnline.Project, rootID string) (projects.Project, projects.Root) {
	project.Roots = projectRootsFromCairnline(written.Roots, project.Roots)
	project.DefaultRootID = strings.TrimSpace(written.DefaultRootID)
	if h == nil || h.projects == nil {
		root, _ := findProjectRootByID(project.Roots, rootID)
		return project, root
	}
	shadowed, err := h.projects.Update(ctx, project.ID, func(item *projects.Project) {
		item.Roots = append([]projects.Root(nil), project.Roots...)
		item.DefaultRootID = project.DefaultRootID
	})
	if err != nil {
		h.logCairnlineMirrorError(ctx, operation, project.ID, err)
		root, _ := findProjectRootByID(project.Roots, rootID)
		return project, root
	}
	root, _ := findProjectRootByID(shadowed.Roots, rootID)
	return shadowed, root
}

func (h *Handler) shadowProjectContextSourcesToHecate(ctx context.Context, operation string, project projects.Project, written cairnline.Project, sourceID string) (projects.Project, projects.ContextSource) {
	project.ContextSources = projectContextSourcesFromCairnline(written.ContextSources)
	if h == nil || h.projects == nil {
		source, _ := findProjectContextSourceByID(project.ContextSources, sourceID)
		return project, source
	}
	shadowed, err := h.projects.Update(ctx, project.ID, func(item *projects.Project) {
		item.ContextSources = append([]projects.ContextSource(nil), project.ContextSources...)
	})
	if err != nil {
		h.logCairnlineMirrorError(ctx, operation, project.ID, err)
		source, _ := findProjectContextSourceByID(project.ContextSources, sourceID)
		return project, source
	}
	source, _ := findProjectContextSourceByID(shadowed.ContextSources, sourceID)
	return shadowed, source
}

func projectFromCairnlineRootSourceListReplace(project projects.Project, written cairnline.Project, replaceRoots, replaceSources bool) projects.Project {
	if replaceRoots {
		project.Roots = projectRootsFromCairnline(written.Roots, project.Roots)
		project.DefaultRootID = strings.TrimSpace(written.DefaultRootID)
	}
	if replaceSources {
		project.ContextSources = projectContextSourcesFromCairnline(written.ContextSources)
	}
	project.UpdatedAt = written.UpdatedAt
	return project
}

func (h *Handler) shadowProjectRootSourceListsToHecate(ctx context.Context, operation string, project projects.Project, written cairnline.Project, replaceRoots, replaceSources bool) (projects.Project, bool) {
	project = projectFromCairnlineRootSourceListReplace(project, written, replaceRoots, replaceSources)
	if h == nil || h.projects == nil {
		return project, false
	}
	shadowed, err := h.projects.Update(ctx, project.ID, func(item *projects.Project) {
		if replaceRoots {
			item.Roots = append([]projects.Root(nil), project.Roots...)
			item.DefaultRootID = project.DefaultRootID
		}
		if replaceSources {
			item.ContextSources = append([]projects.ContextSource(nil), project.ContextSources...)
		}
	})
	if err != nil {
		h.logCairnlineMirrorError(ctx, operation, project.ID, err)
		return project, false
	}
	return shadowed, true
}

func projectAppErrorFromCairnlineAuthority(err error, missing string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, projectapp.ErrProjectNotFound),
		errors.Is(err, projectapp.ErrProjectRootNotFound),
		errors.Is(err, projectapp.ErrProjectContextSourceNotFound),
		errors.Is(err, projectapp.ErrProjectRootConflict),
		errors.Is(err, projectapp.ErrProjectContextSourceConflict),
		errors.Is(err, projects.ErrInvalid),
		errors.Is(err, projects.ErrAlreadyExists):
		return err
	case errors.Is(err, cairnline.ErrNotFound):
		switch missing {
		case "root":
			return projectapp.ErrProjectRootNotFound
		case "context-source":
			return projectapp.ErrProjectContextSourceNotFound
		default:
			return projectapp.ErrProjectNotFound
		}
	case errors.Is(err, cairnline.ErrDuplicate):
		switch missing {
		case "context-source":
			return projectapp.ErrProjectContextSourceConflict
		default:
			return projectapp.ErrProjectRootConflict
		}
	case errors.Is(err, cairnline.ErrInvalid):
		return errors.Join(projects.ErrInvalid, err)
	default:
		return err
	}
}

func writeProjectRootCairnlineAuthorityResponse(w http.ResponseWriter, status int, project projects.Project, err error) {
	if errors.Is(err, projectapp.ErrProjectNotFound) {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
		return
	}
	if errors.Is(err, projectapp.ErrProjectRootNotFound) {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project root not found")
		return
	}
	if errors.Is(err, projectapp.ErrProjectRootConflict) || errors.Is(err, projects.ErrAlreadyExists) {
		WriteError(w, http.StatusConflict, errCodeConflict, err.Error())
		return
	}
	if errors.Is(err, projects.ErrInvalid) {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, status, ProjectResponse{Object: "project", Data: renderProject(project)})
}

func writeProjectListReplaceCairnlineAuthorityResponse(w http.ResponseWriter, project projects.Project, err error) {
	if errors.Is(err, projectapp.ErrProjectNotFound) || errors.Is(err, projects.ErrNotFound) || errors.Is(err, cairnline.ErrNotFound) {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
		return
	}
	if errors.Is(err, projectapp.ErrProjectRootConflict) || errors.Is(err, projectapp.ErrProjectContextSourceConflict) || errors.Is(err, projects.ErrAlreadyExists) || errors.Is(err, cairnline.ErrDuplicate) {
		WriteError(w, http.StatusConflict, errCodeConflict, err.Error())
		return
	}
	if errors.Is(err, projects.ErrInvalid) || errors.Is(err, cairnline.ErrInvalid) {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, ProjectResponse{Object: "project", Data: renderProject(project)})
}

func writeProjectContextSourceCairnlineAuthorityResponse(w http.ResponseWriter, status int, project projects.Project, err error) {
	if errors.Is(err, projectapp.ErrProjectNotFound) {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
		return
	}
	if errors.Is(err, projectapp.ErrProjectContextSourceNotFound) {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project context source not found")
		return
	}
	if errors.Is(err, projectapp.ErrProjectContextSourceConflict) || errors.Is(err, projects.ErrAlreadyExists) {
		WriteError(w, http.StatusConflict, errCodeConflict, err.Error())
		return
	}
	if errors.Is(err, projects.ErrInvalid) {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, status, ProjectResponse{Object: "project", Data: renderProject(project)})
}
