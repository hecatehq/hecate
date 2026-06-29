package api

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/cairnlinebridge"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/projectworkapp"
)

const projectCairnlineWriteAuthorityProjectAssignments = "project-assignments"

func (h *Handler) projectAssignmentWritesUseCairnlineAuthority() bool {
	return h != nil &&
		h.projectCairnlineEmbeddedConnectorEnabled() &&
		h.config.ProjectsCairnlineWriteAuthorityEnabled(projectCairnlineWriteAuthorityProjectAssignments)
}

func (h *Handler) createProjectWorkAssignmentWithCairnlineAuthority(ctx context.Context, projectID, workItemID string, cmd projectworkapp.CreateAssignmentCommand) (projectwork.Assignment, error) {
	project, err := h.projectForCairnlineWriteAuthority(ctx, projectID)
	if err != nil {
		return projectwork.Assignment{}, err
	}
	assignment := projectwork.Assignment{
		ID:           firstNonEmptyString(strings.TrimSpace(cmd.ID), newOpaqueTaskResourceID("asgn")),
		ProjectID:    projectID,
		WorkItemID:   workItemID,
		RoleID:       strings.TrimSpace(cmd.RoleID),
		RootID:       strings.TrimSpace(cmd.RootID),
		DriverKind:   strings.TrimSpace(cmd.DriverKind),
		Status:       strings.TrimSpace(cmd.Status),
		ExecutionRef: cmd.ExecutionRef,
		StartedAt:    cmd.StartedAt,
		CompletedAt:  cmd.CompletedAt,
	}
	if err := validateProjectAssignmentForCairnlineAuthority(assignment); err != nil {
		return projectwork.Assignment{}, err
	}
	var recorded projectwork.Assignment
	err = h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		role, profile, err := h.seedProjectAssignmentDependenciesForCairnlineAuthority(ctx, service, project, assignment)
		if err != nil {
			return err
		}
		assignment.DriverKind = resolvedProjectAssignmentDriverKind(assignment.DriverKind, role.DefaultDriverKind)
		written, err := cairnlinebridge.UpsertAssignment(ctx, service, assignment, role, profile)
		if err != nil {
			return err
		}
		recorded = projectWorkAssignmentFromCairnlineAuthority(written, assignment)
		return nil
	})
	if err != nil {
		return projectwork.Assignment{}, err
	}
	h.shadowProjectAssignmentToHecate(ctx, "project_assignment_cairnline_authority_create", recorded)
	return recorded, nil
}

func (h *Handler) updateProjectWorkAssignmentWithCairnlineAuthority(ctx context.Context, projectID, workItemID, assignmentID string, cmd projectworkapp.UpdateAssignmentCommand) (projectwork.Assignment, error) {
	project, err := h.projectForCairnlineWriteAuthority(ctx, projectID)
	if err != nil {
		return projectwork.Assignment{}, err
	}
	var recorded projectwork.Assignment
	err = h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		existing, err := service.GetAssignment(ctx, projectID, assignmentID)
		if err != nil {
			return err
		}
		if strings.TrimSpace(existing.WorkItemID) != strings.TrimSpace(workItemID) {
			return cairnline.ErrNotFound
		}
		assignment := projectWorkAssignmentFromCairnline(existing)
		if shadow, ok, err := h.loadProjectWorkAssignment(ctx, projectID, workItemID, assignmentID); err != nil {
			return err
		} else if ok {
			assignment.ExecutionRef = shadow.ExecutionRef
		}
		applyProjectAssignmentUpdate(&assignment, cmd)
		if err := validateProjectAssignmentForCairnlineAuthority(assignment); err != nil {
			return err
		}
		role, profile, err := h.seedProjectAssignmentDependenciesForCairnlineAuthority(ctx, service, project, assignment)
		if err != nil {
			return err
		}
		written, err := cairnlinebridge.UpsertAssignment(ctx, service, assignment, role, profile)
		if err != nil {
			return err
		}
		recorded = projectWorkAssignmentFromCairnlineAuthority(written, assignment)
		return nil
	})
	if err != nil {
		return projectwork.Assignment{}, err
	}
	h.shadowProjectAssignmentToHecate(ctx, "project_assignment_cairnline_authority_update", recorded)
	return recorded, nil
}

func (h *Handler) deleteProjectWorkAssignmentWithCairnlineAuthority(ctx context.Context, projectID, workItemID, assignmentID string) error {
	var deleted projectwork.Assignment
	err := h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		existing, err := service.GetAssignment(ctx, projectID, assignmentID)
		if err != nil {
			return err
		}
		if strings.TrimSpace(existing.WorkItemID) != strings.TrimSpace(workItemID) {
			return cairnline.ErrNotFound
		}
		deleted = projectWorkAssignmentFromCairnline(existing)
		return cairnlinebridge.DeleteAssignment(ctx, service, projectID, assignmentID)
	})
	if err != nil {
		return err
	}
	h.shadowProjectAssignmentDeleteToHecate(ctx, "project_assignment_cairnline_authority_delete", deleted.ProjectID, deleted.WorkItemID, deleted.ID)
	return nil
}

func (h *Handler) seedProjectAssignmentDependenciesForCairnlineAuthority(ctx context.Context, service *cairnline.Service, project projects.Project, assignment projectwork.Assignment) (projectwork.AgentRoleProfile, agentprofiles.Profile, error) {
	if _, err := cairnlinebridge.UpsertProjectMetadata(ctx, service, project); err != nil {
		return projectwork.AgentRoleProfile{}, agentprofiles.Profile{}, err
	}
	workItem, err := h.projectWorkItemForCairnlineAssignmentAuthority(ctx, service, assignment.ProjectID, assignment.WorkItemID)
	if err != nil {
		return projectwork.AgentRoleProfile{}, agentprofiles.Profile{}, err
	}
	if err := h.seedProjectWorkItemDependenciesForCairnlineAuthority(ctx, service, project, workItem); err != nil {
		return projectwork.AgentRoleProfile{}, agentprofiles.Profile{}, err
	}
	if err := h.seedProjectAssignmentRootForCairnlineAuthority(ctx, service, project, assignment.RootID); err != nil {
		return projectwork.AgentRoleProfile{}, agentprofiles.Profile{}, err
	}
	role, profile, err := h.projectRoleForCairnlineAssignmentAuthority(ctx, service, assignment.ProjectID, assignment.RoleID)
	if err != nil {
		return projectwork.AgentRoleProfile{}, agentprofiles.Profile{}, err
	}
	return role, profile, nil
}

func (h *Handler) projectWorkItemForCairnlineAssignmentAuthority(ctx context.Context, service *cairnline.Service, projectID, workItemID string) (projectwork.WorkItem, error) {
	if h != nil && h.projectWork != nil {
		if item, ok, err := h.projectWork.GetWorkItem(ctx, projectID, workItemID); err != nil {
			return projectwork.WorkItem{}, err
		} else if ok {
			return item, nil
		}
	}
	item, err := service.GetWorkItem(ctx, projectID, workItemID)
	if err != nil {
		return projectwork.WorkItem{}, err
	}
	return projectWorkItemFromCairnline(item), nil
}

func (h *Handler) projectRoleForCairnlineAssignmentAuthority(ctx context.Context, service *cairnline.Service, projectID, roleID string) (projectwork.AgentRoleProfile, agentprofiles.Profile, error) {
	if role, ok, err := h.loadProjectWorkRoleForCairnlineMirror(ctx, projectID, roleID); err != nil {
		return projectwork.AgentRoleProfile{}, agentprofiles.Profile{}, err
	} else if ok {
		profile, err := h.writeRoleAgentProfileToCairnline(ctx, service, role)
		if err != nil {
			return projectwork.AgentRoleProfile{}, agentprofiles.Profile{}, err
		}
		if _, err := cairnlinebridge.UpsertRole(ctx, service, role); err != nil {
			return projectwork.AgentRoleProfile{}, agentprofiles.Profile{}, err
		}
		return role, profile, nil
	}
	role, err := getCairnlineProjectRoleForAuthority(ctx, service, projectID, roleID)
	if err != nil {
		return projectwork.AgentRoleProfile{}, agentprofiles.Profile{}, err
	}
	native, err := h.projectWorkRoleFromCairnlineAuthority(ctx, service, role, projectwork.AgentRoleProfile{})
	if err != nil {
		return projectwork.AgentRoleProfile{}, agentprofiles.Profile{}, err
	}
	return native, agentprofiles.Profile{}, nil
}

func resolvedProjectAssignmentDriverKind(driverKind, roleDefault string) string {
	return firstNonEmptyString(
		strings.TrimSpace(driverKind),
		strings.TrimSpace(roleDefault),
		projectwork.AssignmentDriverHecateTask,
	)
}

func (h *Handler) seedProjectAssignmentRootForCairnlineAuthority(ctx context.Context, service *cairnline.Service, project projects.Project, rootID string) error {
	rootID = strings.TrimSpace(rootID)
	if rootID == "" {
		return nil
	}
	root, ok := projectRootForCairnlineMirror(project, rootID)
	if !ok {
		return errors.Join(cairnline.ErrNotFound, errors.New("project root not found for Cairnline assignment authority"))
	}
	_, err := cairnlinebridge.UpsertRoot(ctx, service, project, root)
	return err
}

func applyProjectAssignmentUpdate(item *projectwork.Assignment, cmd projectworkapp.UpdateAssignmentCommand) {
	if item == nil {
		return
	}
	if cmd.RoleID != nil {
		item.RoleID = *cmd.RoleID
	}
	if cmd.RootID != nil {
		item.RootID = *cmd.RootID
	}
	if cmd.DriverKind != nil {
		item.DriverKind = *cmd.DriverKind
	}
	if cmd.Status != nil {
		item.Status = *cmd.Status
	}
	if cmd.ExecutionRef != nil {
		item.ExecutionRef = *cmd.ExecutionRef
	}
	if cmd.StartedAt != nil {
		item.StartedAt = *cmd.StartedAt
	}
	if cmd.CompletedAt != nil {
		item.CompletedAt = *cmd.CompletedAt
	}
}

func projectWorkAssignmentFromCairnlineAuthority(item cairnline.Assignment, native projectwork.Assignment) projectwork.Assignment {
	out := projectWorkAssignmentFromCairnline(item)
	if ref := projectwork.NormalizeAssignmentExecutionRef(native.ExecutionRef); !projectAssignmentExecutionRefEmpty(ref) {
		out.ExecutionRef = ref
	}
	if out.ExecutionRef.ContextSnapshotID == "" {
		out.ExecutionRef.ContextSnapshotID = strings.TrimSpace(item.ContextSnapshotID)
	}
	return out
}

func projectAssignmentExecutionRefEmpty(ref projectwork.AssignmentExecutionRef) bool {
	return ref.Kind == "" &&
		ref.TaskID == "" &&
		ref.RunID == "" &&
		ref.ChatSessionID == "" &&
		ref.MessageID == "" &&
		ref.ContextSnapshotID == "" &&
		ref.Status == "" &&
		ref.PendingApprovalCount == 0 &&
		ref.TraceID == "" &&
		!ref.Missing
}

func (h *Handler) shadowProjectAssignmentToHecate(ctx context.Context, operation string, assignment projectwork.Assignment) {
	if h == nil || h.projectWork == nil {
		return
	}
	if _, err := h.projectWork.CreateAssignment(ctx, assignment); err == nil {
		return
	} else if !errors.Is(err, projectwork.ErrDuplicate) {
		h.logCairnlineMirrorError(ctx, operation, assignment.ProjectID, err)
		return
	}
	_, err := h.projectWork.UpdateAssignment(ctx, assignment.ProjectID, assignment.ID, func(item *projectwork.Assignment) {
		item.WorkItemID = assignment.WorkItemID
		item.RoleID = assignment.RoleID
		item.RootID = assignment.RootID
		item.DriverKind = assignment.DriverKind
		item.Status = assignment.Status
		item.ExecutionRef = assignment.ExecutionRef
		item.StartedAt = assignment.StartedAt
		item.CompletedAt = assignment.CompletedAt
	})
	if err != nil {
		h.logCairnlineMirrorError(ctx, operation, assignment.ProjectID, err)
	}
}

func (h *Handler) shadowProjectAssignmentDeleteToHecate(ctx context.Context, operation, projectID, workItemID, assignmentID string) {
	if h == nil || h.projectWork == nil {
		return
	}
	if err := h.projectWork.DeleteAssignment(ctx, projectID, workItemID, assignmentID); err != nil && !errors.Is(err, projectwork.ErrNotFound) {
		h.logCairnlineMirrorError(ctx, operation, projectID, err)
	}
}

func validateProjectAssignmentForCairnlineAuthority(assignment projectwork.Assignment) error {
	if strings.TrimSpace(assignment.ProjectID) == "" {
		return fmt.Errorf("%w: project_id is required", projectwork.ErrInvalid)
	}
	if strings.TrimSpace(assignment.WorkItemID) == "" {
		return fmt.Errorf("%w: work_item_id is required", projectwork.ErrInvalid)
	}
	if strings.TrimSpace(assignment.RoleID) == "" {
		return fmt.Errorf("%w: role_id is required", projectwork.ErrInvalid)
	}
	if driverKind := strings.TrimSpace(assignment.DriverKind); driverKind != "" && !validProjectAssignmentDriverKindForCairnlineAuthority(driverKind) {
		return fmt.Errorf("%w: unsupported assignment driver_kind %q", projectwork.ErrInvalid, driverKind)
	}
	if status := strings.TrimSpace(assignment.Status); status != "" && !validProjectAssignmentStatusForCairnlineAuthority(status) {
		return fmt.Errorf("%w: unsupported assignment status %q", projectwork.ErrInvalid, status)
	}
	return nil
}

func validProjectAssignmentDriverKindForCairnlineAuthority(kind string) bool {
	switch kind {
	case projectwork.AssignmentDriverHecateTask, projectwork.AssignmentDriverExternalAgent:
		return true
	default:
		return false
	}
}

func validProjectAssignmentStatusForCairnlineAuthority(status string) bool {
	switch status {
	case projectwork.AssignmentStatusQueued, projectwork.AssignmentStatusRunning, projectwork.AssignmentStatusAwaitingApproval, projectwork.AssignmentStatusCompleted, projectwork.AssignmentStatusFailed, projectwork.AssignmentStatusCancelled:
		return true
	default:
		return false
	}
}
