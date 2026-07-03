package api

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/cairnlinebridge"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/projectworkapp"
)

const projectCairnlineWriteAuthorityProjectWorkItems = "project-work-items"

func (h *Handler) projectWorkItemWritesUseCairnlineAuthority() bool {
	return h != nil &&
		h.projectCairnlineEmbeddedConnectorEnabled() &&
		h.config.ProjectsCairnlineWriteAuthorityEnabled(projectCairnlineWriteAuthorityProjectWorkItems)
}

func (h *Handler) createProjectWorkItemWithCairnlineAuthority(ctx context.Context, projectID string, cmd projectworkapp.CreateWorkItemCommand) (projectwork.WorkItem, error) {
	project, err := h.projectForCairnlineWriteAuthority(ctx, projectID)
	if err != nil {
		return projectwork.WorkItem{}, err
	}
	item := projectwork.WorkItem{
		ID:              firstNonEmptyString(strings.TrimSpace(cmd.ID), newOpaqueTaskResourceID("work")),
		ProjectID:       projectID,
		Title:           cmd.Title,
		Brief:           cmd.Brief,
		Status:          cmd.Status,
		Priority:        cmd.Priority,
		OwnerRoleID:     cmd.OwnerRoleID,
		RootID:          cmd.RootID,
		ReviewerRoleIDs: append([]string(nil), cmd.ReviewerRoleIDs...),
	}
	item = normalizeProjectWorkItemForCairnlineAuthority(item)
	if err := validateProjectWorkItemForCairnlineAuthority(item); err != nil {
		return projectwork.WorkItem{}, err
	}

	var created projectwork.WorkItem
	err = h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		if err := h.seedProjectWorkItemDependenciesForCairnlineAuthority(ctx, service, project, item); err != nil {
			return err
		}
		recorded, err := service.CreateWorkItem(ctx, cairnlinebridge.WorkItem(item))
		if err != nil {
			return err
		}
		created = projectWorkItemFromCairnline(recorded)
		return nil
	})
	if err != nil {
		return projectwork.WorkItem{}, err
	}
	h.shadowProjectWorkItemToHecate(ctx, "project_work_item_cairnline_authority_create", created)
	return created, nil
}

func (h *Handler) updateProjectWorkItemWithCairnlineAuthority(ctx context.Context, projectID, workItemID string, cmd projectworkapp.UpdateWorkItemCommand) (projectwork.WorkItem, error) {
	if cmd.Status != nil && strings.TrimSpace(*cmd.Status) == projectwork.WorkItemStatusDone {
		readiness, err := h.projectWorkItemCloseoutReadinessForCairnlineAuthority(ctx, projectID, workItemID)
		if err != nil {
			return projectwork.WorkItem{}, err
		}
		if readiness.Status != "done" && !readiness.Ready {
			return projectwork.WorkItem{}, projectworkapp.WorkItemCloseoutBlockedError{Readiness: readiness}
		}
	}
	project, err := h.projectForCairnlineWriteAuthority(ctx, projectID)
	if err != nil {
		return projectwork.WorkItem{}, err
	}

	var updated projectwork.WorkItem
	err = h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		existing, err := service.GetWorkItem(ctx, projectID, workItemID)
		if err != nil {
			return err
		}
		item := projectWorkItemFromCairnline(existing)
		applyProjectWorkItemUpdate(&item, cmd)
		item = normalizeProjectWorkItemForCairnlineAuthority(item)
		if err := validateProjectWorkItemForCairnlineAuthority(item); err != nil {
			return err
		}
		if err := h.seedProjectWorkItemDependenciesForCairnlineAuthority(ctx, service, project, item); err != nil {
			return err
		}
		recorded, err := service.UpdateWorkItem(ctx, cairnlinebridge.WorkItem(item))
		if err != nil {
			return err
		}
		updated = projectWorkItemFromCairnline(recorded)
		return nil
	})
	if err != nil {
		return projectwork.WorkItem{}, err
	}
	h.shadowProjectWorkItemToHecate(ctx, "project_work_item_cairnline_authority_update", updated)
	return updated, nil
}

func (h *Handler) projectWorkItemCloseoutReadinessForCairnlineAuthority(ctx context.Context, projectID, workItemID string) (projectwork.WorkItemReadiness, error) {
	if h != nil && h.projectReadRoutesUseCairnlineReadModel() {
		view, err := h.cairnlineProjectWorkView(ctx, projectID)
		if err != nil {
			return projectwork.WorkItemReadiness{}, err
		}
		defer view.Close()
		readiness, err := h.cairnlineProjectWorkItemReadiness(ctx, view.service, view.snapshot, workItemID)
		if err != nil {
			return projectwork.WorkItemReadiness{}, err
		}
		return readiness, nil
	}
	return h.projectWorkApplication().WorkItemReadiness(ctx, projectID, workItemID)
}

func (h *Handler) deleteProjectWorkItemWithCairnlineAuthority(ctx context.Context, projectID, workItemID string) error {
	if err := h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		return service.DeleteWorkItem(ctx, projectID, workItemID)
	}); err != nil {
		return err
	}
	h.shadowProjectWorkItemDeleteToHecate(ctx, "project_work_item_cairnline_authority_delete", projectID, workItemID)
	return nil
}

func (h *Handler) projectForCairnlineWriteAuthority(ctx context.Context, projectID string) (projects.Project, error) {
	if h == nil {
		return projects.Project{}, errors.New("handler is not configured")
	}
	projectID = strings.TrimSpace(projectID)
	if h.requiresEmbeddedCairnlineProjectReads() {
		return h.projectFromEmbeddedCairnlineWriteAuthority(ctx, projectID)
	}
	if h.projects != nil {
		project, ok, err := h.projects.Get(ctx, projectID)
		if err != nil {
			return projects.Project{}, err
		}
		if ok {
			return project, nil
		}
	}
	return h.projectFromEmbeddedCairnlineWriteAuthority(ctx, projectID)
}

func (h *Handler) projectFromEmbeddedCairnlineWriteAuthority(ctx context.Context, projectID string) (projects.Project, error) {
	var project projects.Project
	err := h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		item, err := service.GetProject(ctx, projectID)
		if err != nil {
			return err
		}
		executionProfile, err := cairnlineExecutionProfileByID(ctx, service, item.DefaultExecutionProfileID)
		if err != nil {
			return err
		}
		project = projectFromCairnline(item, executionProfile, projects.Project{})
		return nil
	})
	if err != nil {
		if errors.Is(err, cairnline.ErrNotFound) {
			return projects.Project{}, errors.Join(cairnline.ErrNotFound, errors.New("project not found for Cairnline write authority"))
		}
		return projects.Project{}, err
	}
	return project, nil
}

func (h *Handler) seedProjectWorkItemDependenciesForCairnlineAuthority(ctx context.Context, service *cairnline.Service, project projects.Project, item projectwork.WorkItem) error {
	if _, err := cairnlinebridge.UpsertProjectMetadata(ctx, service, project); err != nil {
		return err
	}
	if strings.TrimSpace(item.RootID) == "" {
		return nil
	}
	root, ok := projectRootForCairnlineMirror(project, item.RootID)
	if !ok {
		return errors.Join(cairnline.ErrNotFound, errors.New("project root not found for Cairnline work-item authority"))
	}
	_, err := cairnlinebridge.UpsertRoot(ctx, service, project, root)
	return err
}

func normalizeProjectWorkItemForCairnlineAuthority(item projectwork.WorkItem) projectwork.WorkItem {
	item.ID = strings.TrimSpace(item.ID)
	item.ProjectID = strings.TrimSpace(item.ProjectID)
	item.Title = strings.TrimSpace(item.Title)
	item.Brief = strings.TrimSpace(item.Brief)
	item.Status = strings.TrimSpace(item.Status)
	item.Priority = strings.TrimSpace(item.Priority)
	item.OwnerRoleID = strings.TrimSpace(item.OwnerRoleID)
	item.RootID = strings.TrimSpace(item.RootID)
	item.ReviewerRoleIDs = compactProjectWorkAuthorityStrings(item.ReviewerRoleIDs)
	if item.Status == "" {
		item.Status = projectwork.WorkItemStatusBacklog
	}
	if item.Priority == "" {
		item.Priority = "normal"
	}
	return item
}

func validateProjectWorkItemForCairnlineAuthority(item projectwork.WorkItem) error {
	if item.ProjectID == "" {
		return fmt.Errorf("%w: project_id is required", projectwork.ErrInvalid)
	}
	if item.ID == "" {
		return fmt.Errorf("%w: work item id is required", projectwork.ErrInvalid)
	}
	if item.Title == "" {
		return fmt.Errorf("%w: work item title is required", projectwork.ErrInvalid)
	}
	if !validProjectWorkItemStatusForCairnlineAuthority(item.Status) {
		return fmt.Errorf("%w: unsupported work item status %q", projectwork.ErrInvalid, item.Status)
	}
	if !validProjectWorkItemPriorityForCairnlineAuthority(item.Priority) {
		return fmt.Errorf("%w: unsupported work item priority %q", projectwork.ErrInvalid, item.Priority)
	}
	return nil
}

func validProjectWorkItemStatusForCairnlineAuthority(status string) bool {
	switch strings.TrimSpace(status) {
	case projectwork.WorkItemStatusBacklog, projectwork.WorkItemStatusReady, projectwork.WorkItemStatusRunning, projectwork.WorkItemStatusReview, projectwork.WorkItemStatusBlocked, projectwork.WorkItemStatusDone, projectwork.WorkItemStatusCancelled:
		return true
	default:
		return false
	}
}

func validProjectWorkItemPriorityForCairnlineAuthority(priority string) bool {
	switch strings.TrimSpace(priority) {
	case "low", "normal", "high", "urgent":
		return true
	default:
		return false
	}
}

func applyProjectWorkItemUpdate(item *projectwork.WorkItem, cmd projectworkapp.UpdateWorkItemCommand) {
	if item == nil {
		return
	}
	if cmd.Title != nil {
		item.Title = *cmd.Title
	}
	if cmd.Brief != nil {
		item.Brief = *cmd.Brief
	}
	if cmd.Status != nil {
		item.Status = *cmd.Status
	}
	if cmd.Priority != nil {
		item.Priority = *cmd.Priority
	}
	if cmd.OwnerRoleID != nil {
		item.OwnerRoleID = *cmd.OwnerRoleID
	}
	if cmd.RootID != nil {
		item.RootID = *cmd.RootID
	}
	if cmd.ReviewerRoleIDs != nil {
		item.ReviewerRoleIDs = append([]string(nil), *cmd.ReviewerRoleIDs...)
	}
}

func compactProjectWorkAuthorityStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func (h *Handler) shadowProjectWorkItemToHecate(ctx context.Context, operation string, item projectwork.WorkItem) {
	if h == nil || h.projectWork == nil {
		return
	}
	if h.projectCairnlineEmbeddedReplacementModeArmed() {
		return
	}
	if _, err := h.projectWork.UpdateWorkItem(ctx, item.ProjectID, item.ID, func(existing *projectwork.WorkItem) {
		*existing = item
	}); err == nil {
		return
	} else if !errors.Is(err, projectwork.ErrNotFound) {
		h.logProjectWorkItemShadowError(ctx, operation, item.ProjectID, item.ID, err)
		return
	}
	if _, err := h.projectWork.CreateWorkItem(ctx, item); err != nil && !errors.Is(err, projectwork.ErrDuplicate) {
		h.logProjectWorkItemShadowError(ctx, operation, item.ProjectID, item.ID, err)
	}
}

func (h *Handler) shadowProjectWorkItemDeleteToHecate(ctx context.Context, operation, projectID, workItemID string) {
	if h == nil || h.projectWork == nil {
		return
	}
	if err := h.projectWork.DeleteWorkItem(ctx, projectID, workItemID); err != nil && !errors.Is(err, projectwork.ErrNotFound) {
		h.logProjectWorkItemShadowError(ctx, operation, projectID, workItemID, err)
	}
}

func (h *Handler) logProjectWorkItemShadowError(ctx context.Context, operation, projectID, workItemID string, err error) {
	if err == nil || h == nil || h.logger == nil {
		return
	}
	h.logger.WarnContext(ctx, "project work-item Hecate shadow failed", "operation", operation, "project_id", projectID, "work_item_id", workItemID, "error", err)
}
