package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/cairnlinebridge"
	"github.com/hecatehq/hecate/internal/projectruntime"
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

func (h *Handler) validateProjectHumanAssignmentAuthority(ctx context.Context, projectID, roleID, driverKind string) error {
	if h.projectAssignmentWritesUseCairnlineAuthority() {
		return nil
	}
	resolvedDriver := strings.TrimSpace(driverKind)
	if resolvedDriver == "" && h != nil && h.projectWork != nil {
		if role, ok, err := h.loadProjectWorkRole(ctx, projectID, roleID); err != nil {
			return err
		} else if ok {
			resolvedDriver = strings.TrimSpace(role.DefaultDriverKind)
		}
	}
	if resolvedDriver == projectwork.AssignmentDriverManual {
		return errors.Join(cairnline.ErrConflict, errors.New("Human assignments require Cairnline assignment authority"))
	}
	return nil
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
	err = h.withCairnlineEmbeddedService(ctx, func(service *cairnline.Service) error {
		if _, getErr := service.GetAssignment(ctx, projectID, assignment.ID); getErr == nil {
			return cairnline.ErrDuplicate
		} else if !errors.Is(getErr, cairnline.ErrNotFound) {
			return getErr
		}
		role, err := h.seedProjectAssignmentDependenciesForCairnlineAuthority(ctx, service, project, assignment)
		if err != nil {
			return err
		}
		assignment.DriverKind = resolvedProjectAssignmentDriverKind(assignment.DriverKind, role.DefaultDriverKind)
		if assignment.DriverKind == projectwork.AssignmentDriverManual {
			if status := strings.TrimSpace(assignment.Status); status != "" && status != projectwork.AssignmentStatusQueued {
				return errors.Join(projectwork.ErrInvalid, errors.New("Human assignments must be created ready to start"))
			}
			if !projectAssignmentExecutionRefEmpty(projectwork.NormalizeAssignmentExecutionRef(assignment.ExecutionRef)) ||
				!assignment.StartedAt.IsZero() || !assignment.CompletedAt.IsZero() {
				return errors.Join(projectwork.ErrInvalid, errors.New("Human assignments cannot be created with execution or lifecycle details"))
			}
			assignment.Status = projectwork.AssignmentStatusQueued
		}
		written, err := cairnlinebridge.CreateAssignment(ctx, service, assignment, role)
		if err != nil {
			return err
		}
		assignment.StartedAt = time.Time{}
		assignment.CompletedAt = time.Time{}
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
	err = h.withCairnlineEmbeddedService(ctx, func(service *cairnline.Service) error {
		existing, err := service.GetAssignment(ctx, projectID, assignmentID)
		if err != nil {
			return err
		}
		if strings.TrimSpace(existing.WorkItemID) != strings.TrimSpace(workItemID) {
			return cairnline.ErrNotFound
		}
		portableAssignment := projectWorkAssignmentFromCairnline(existing)
		runtimeShadow := portableAssignment
		if h.projectRuntime != nil {
			runtime, ok, runtimeErr := h.projectRuntime.Get(ctx, projectID, assignmentID)
			if runtimeErr != nil {
				return runtimeErr
			}
			if ok {
				runtimeShadow = projectruntime.Apply(runtimeShadow, runtime)
			}
		}
		requestedStatus := ""
		if cmd.Status != nil {
			requestedStatus = strings.TrimSpace(*cmd.Status)
		}
		if requestedStatus != "" && !validProjectAssignmentStatusForCairnlineAuthority(requestedStatus) {
			return fmt.Errorf("%w: unsupported assignment status %q", projectwork.ErrInvalid, requestedStatus)
		}
		existingStatus := cairnlinebridge.AssignmentStatusFromCairnline(existing.Status)
		desiredCoordination := portableAssignment
		applyProjectAssignmentUpdate(&desiredCoordination, projectworkapp.UpdateAssignmentCommand{
			RoleID:     cmd.RoleID,
			RootID:     cmd.RootID,
			DriverKind: cmd.DriverKind,
		})
		coordinationRequested := projectAssignmentUpdateIncludesCoordination(cmd)
		if coordinationRequested && existing.Status != cairnline.AssignmentQueued {
			if existing.Status == cairnline.AssignmentClaimed && existing.ExecutionMode == cairnline.ExecutionManual {
				return errors.Join(cairnline.ErrConflict, errors.New("Human assignment is already being started; refresh before updating it"))
			}
			if existing.ExecutionMode == cairnline.ExecutionManual {
				return errors.Join(cairnline.ErrConflict, errors.New("Human assignment details cannot change after work starts"))
			}
			return errors.Join(cairnline.ErrConflict, errors.New("assignment destination cannot change after work starts"))
		}
		coordinationUpdate := coordinationRequested &&
			projectAssignmentCoordinationChanged(portableAssignment, desiredCoordination)
		lifecycleUpdate := projectAssignmentUpdateIncludesLifecycle(cmd)
		if coordinationUpdate && (lifecycleUpdate || (requestedStatus != "" && requestedStatus != existingStatus)) {
			return errors.Join(cairnline.ErrConflict, errors.New("change the assignment destination separately from its progress"))
		}
		if lifecycleUpdate && requestedStatus == "" {
			return errors.Join(cairnline.ErrConflict, errors.New("assignment execution details require an execution-managed status update"))
		}
		if coordinationUpdate {
			desired := desiredCoordination
			if err := validateProjectAssignmentForCairnlineAuthority(desired); err != nil {
				return err
			}
			role, err := h.seedProjectAssignmentDependenciesForCairnlineAuthority(ctx, service, project, desired)
			if err != nil {
				return err
			}
			desired.DriverKind = resolvedProjectAssignmentDriverKind(desired.DriverKind, role.DefaultDriverKind)
			replacement := cairnlinebridge.Assignment(desired, role).Coordination()
			if !assignmentCoordinationMatches(existing.Coordination(), replacement) {
				existing, err = service.UpdateQueuedAssignment(ctx, projectID, assignmentID, cairnline.QueuedAssignmentUpdate{
					Expected:          existing.Coordination(),
					ExpectedUpdatedAt: existing.UpdatedAt,
					Replacement:       replacement,
				})
				if err != nil {
					return errors.Join(err, errors.New("assignment destination cannot change after work starts or another edit wins; refresh and try again"))
				}
				portableAssignment = projectWorkAssignmentFromCairnline(existing)
				existingStatus = cairnlinebridge.AssignmentStatusFromCairnline(existing.Status)
			}
		}
		if requestedStatus == "" || (!lifecycleUpdate && existing.Status != cairnline.AssignmentClaimed && requestedStatus == existingStatus) {
			recorded = projectWorkAssignmentFromCairnlineAuthority(existing, runtimeShadow)
			return nil
		}
		if existing.ExecutionMode == cairnline.ExecutionManual {
			if existing.Status == cairnline.AssignmentClaimed {
				return errors.Join(cairnline.ErrConflict, errors.New("Human assignment is already being started; refresh before updating it"))
			}
			written, statusErr := updateManualProjectAssignmentStatusWithCairnlineAuthority(ctx, service, existing, requestedStatus)
			if statusErr != nil {
				return statusErr
			}
			recorded = projectWorkAssignmentFromCairnlineAuthority(written, runtimeShadow)
			return nil
		}
		desired := runtimeShadow
		applyProjectAssignmentUpdate(&desired, cmd)
		written, statusErr := updateAgentProjectAssignmentStatusWithCairnlineAuthority(ctx, service, existing, desired)
		if statusErr != nil {
			return statusErr
		}
		// Cairnline owns lifecycle timestamps. The Hecate runtime shadow keeps
		// host-only links such as message ids, but cannot replace those stamps.
		desired.StartedAt = time.Time{}
		desired.CompletedAt = time.Time{}
		recorded = projectWorkAssignmentFromCairnlineAuthority(written, desired)
		return nil
	})
	if err != nil {
		return projectwork.Assignment{}, err
	}
	h.shadowProjectAssignmentToHecate(ctx, "project_assignment_cairnline_authority_update", recorded)
	return recorded, nil
}

func updateAgentProjectAssignmentStatusWithCairnlineAuthority(
	ctx context.Context,
	service *cairnline.Service,
	existing cairnline.Assignment,
	desired projectwork.Assignment,
) (cairnline.Assignment, error) {
	status := cairnlinebridge.AssignmentStatus(desired.Status)
	if status == cairnline.AssignmentQueued {
		if existing.Status == cairnline.AssignmentQueued {
			return existing, nil
		}
		return cairnline.Assignment{}, errors.Join(cairnline.ErrConflict, errors.New("agent assignment progress cannot move back to ready"))
	}
	if assignmentTerminalStatusForAuthority(existing.Status) {
		if existing.Status == status {
			return existing, nil
		}
		return cairnline.Assignment{}, errors.Join(cairnline.ErrConflict, errors.New("finished assignment progress cannot change"))
	}

	executionRef := cairnlinebridge.ExecutionRef(desired.ExecutionRef)
	contextSnapshotID := strings.TrimSpace(desired.ExecutionRef.ContextSnapshotID)
	claimedBy := projectAssignmentStartClaimedBy(desired)
	if existing.Status == cairnline.AssignmentQueued {
		if assignmentTerminalStatusForAuthority(status) && executionRef.Empty() && contextSnapshotID == "" {
			return service.CompleteAssignment(ctx, existing.ProjectID, existing.ID, status, executionRef)
		}
		claimed, err := service.ClaimAssignment(ctx, existing.ProjectID, existing.ID, claimedBy)
		if err != nil {
			return cairnline.Assignment{}, err
		}
		existing = claimed
	}
	if existing.Status == cairnline.AssignmentClaimed {
		if strings.TrimSpace(existing.ClaimedBy) != strings.TrimSpace(claimedBy) {
			return cairnline.Assignment{}, cairnline.ErrConflict
		}
		if !executionRef.Empty() || contextSnapshotID != "" {
			prepared, err := service.PrepareAssignment(ctx, existing.ProjectID, existing.ID, cairnline.AssignmentPreparation{
				ClaimedBy:         existing.ClaimedBy,
				ExecutionRef:      executionRef,
				ContextSnapshotID: contextSnapshotID,
			})
			if err != nil {
				return cairnline.Assignment{}, err
			}
			existing = prepared
		}
	} else if contextSnapshotID != "" && contextSnapshotID != existing.ContextSnapshotID {
		return cairnline.Assignment{}, cairnline.ErrConflict
	}

	switch status {
	case cairnline.AssignmentRunning, cairnline.AssignmentAwaitingApproval, cairnline.AssignmentReview:
		return service.UpdateAssignmentStatus(ctx, existing.ProjectID, existing.ID, status, executionRef)
	case cairnline.AssignmentCompleted, cairnline.AssignmentFailed, cairnline.AssignmentCancelled:
		return service.CompleteAssignment(ctx, existing.ProjectID, existing.ID, status, executionRef)
	default:
		return cairnline.Assignment{}, cairnline.ErrInvalid
	}
}

func assignmentTerminalStatusForAuthority(status string) bool {
	switch strings.TrimSpace(status) {
	case cairnline.AssignmentCompleted, cairnline.AssignmentFailed, cairnline.AssignmentCancelled:
		return true
	default:
		return false
	}
}

func projectAssignmentUpdateIncludesCoordination(cmd projectworkapp.UpdateAssignmentCommand) bool {
	return cmd.RoleID != nil ||
		cmd.RootID != nil ||
		cmd.DriverKind != nil
}

func projectAssignmentCoordinationChanged(existing, desired projectwork.Assignment) bool {
	return strings.TrimSpace(existing.RoleID) != strings.TrimSpace(desired.RoleID) ||
		strings.TrimSpace(existing.RootID) != strings.TrimSpace(desired.RootID) ||
		strings.TrimSpace(existing.DriverKind) != strings.TrimSpace(desired.DriverKind)
}

func projectAssignmentUpdateIncludesLifecycle(cmd projectworkapp.UpdateAssignmentCommand) bool {
	return cmd.ExecutionRef != nil ||
		cmd.StartedAt != nil ||
		cmd.CompletedAt != nil
}

func assignmentCoordinationMatches(a, b cairnline.AssignmentCoordination) bool {
	if a.WorkItemID != b.WorkItemID || a.RoleID != b.RoleID || a.RootID != b.RootID ||
		a.ExecutionMode != b.ExecutionMode || a.DesiredAgent.Kind != b.DesiredAgent.Kind ||
		len(a.DesiredAgent.SkillIDs) != len(b.DesiredAgent.SkillIDs) {
		return false
	}
	for index := range a.DesiredAgent.SkillIDs {
		if a.DesiredAgent.SkillIDs[index] != b.DesiredAgent.SkillIDs[index] {
			return false
		}
	}
	return true
}

func updateManualProjectAssignmentStatusWithCairnlineAuthority(
	ctx context.Context,
	service *cairnline.Service,
	existing cairnline.Assignment,
	requestedStatus string,
) (cairnline.Assignment, error) {
	requestedStatus = strings.TrimSpace(requestedStatus)
	currentStatus := cairnlinebridge.AssignmentStatusFromCairnline(existing.Status)
	if existing.Status == cairnline.AssignmentClaimed {
		return cairnline.Assignment{}, errors.Join(cairnline.ErrConflict, errors.New("Human assignment is already being started; refresh before updating it"))
	}
	if requestedStatus == currentStatus {
		return existing, nil
	}
	if existing.Status == cairnline.AssignmentCompleted || existing.Status == cairnline.AssignmentFailed || existing.Status == cairnline.AssignmentCancelled {
		return cairnline.Assignment{}, errors.Join(cairnline.ErrConflict, errors.New("finished Human work cannot change status; create another assignment to continue"))
	}
	switch requestedStatus {
	case projectwork.AssignmentStatusRunning:
		if currentStatus != projectwork.AssignmentStatusAwaitingApproval {
			return cairnline.Assignment{}, errors.Join(cairnline.ErrConflict, errors.New("start Human assignments through the assignment start action"))
		}
		return service.UpdateAssignmentStatus(ctx, existing.ProjectID, existing.ID, cairnline.AssignmentRunning, cairnline.ExecutionRef{})
	case projectwork.AssignmentStatusAwaitingApproval:
		if currentStatus != projectwork.AssignmentStatusRunning {
			return cairnline.Assignment{}, errors.Join(cairnline.ErrConflict, errors.New("only active Human work can be sent for review"))
		}
		return service.UpdateAssignmentStatus(ctx, existing.ProjectID, existing.ID, cairnline.AssignmentReview, cairnline.ExecutionRef{})
	case projectwork.AssignmentStatusCompleted, projectwork.AssignmentStatusFailed:
		if currentStatus != projectwork.AssignmentStatusRunning && currentStatus != projectwork.AssignmentStatusAwaitingApproval {
			return cairnline.Assignment{}, errors.Join(cairnline.ErrConflict, errors.New("start Human work before finishing it"))
		}
		return service.CompleteAssignment(ctx, existing.ProjectID, existing.ID, cairnlinebridge.AssignmentStatus(requestedStatus), cairnline.ExecutionRef{})
	case projectwork.AssignmentStatusCancelled:
		return service.CompleteAssignment(ctx, existing.ProjectID, existing.ID, cairnline.AssignmentCancelled, cairnline.ExecutionRef{})
	default:
		return cairnline.Assignment{}, errors.Join(cairnline.ErrInvalid, errors.New("unsupported Human assignment status"))
	}
}

func (h *Handler) deleteProjectWorkAssignmentWithCairnlineAuthority(ctx context.Context, projectID, workItemID, assignmentID string) error {
	var deleted projectwork.Assignment
	err := h.withCairnlineEmbeddedService(ctx, func(service *cairnline.Service) error {
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

func (h *Handler) seedProjectAssignmentDependenciesForCairnlineAuthority(ctx context.Context, service *cairnline.Service, project projects.Project, assignment projectwork.Assignment) (projectwork.AgentRoleProfile, error) {
	if h.requiresEmbeddedCairnlineProjectReads() || h.projectCairnlineEmbeddedReplacementModeArmed() {
		if _, err := service.GetWorkItem(ctx, assignment.ProjectID, assignment.WorkItemID); err != nil {
			return projectwork.AgentRoleProfile{}, err
		}
		if rootID := strings.TrimSpace(assignment.RootID); rootID != "" {
			if _, err := service.GetRoot(ctx, assignment.ProjectID, rootID); err != nil {
				return projectwork.AgentRoleProfile{}, err
			}
		}
		role, err := getCairnlineProjectRoleForAuthority(ctx, service, assignment.ProjectID, assignment.RoleID)
		if err != nil {
			return projectwork.AgentRoleProfile{}, err
		}
		return projectWorkRoleFromCairnline(role, projectwork.AgentRoleProfile{}), nil
	}
	if _, err := cairnlinebridge.UpsertProjectMetadata(ctx, service, project); err != nil {
		return projectwork.AgentRoleProfile{}, err
	}
	workItem, err := h.projectWorkItemForCairnlineAssignmentAuthority(ctx, service, assignment.ProjectID, assignment.WorkItemID)
	if err != nil {
		return projectwork.AgentRoleProfile{}, err
	}
	if err := h.seedProjectWorkItemDependenciesForCairnlineAuthority(ctx, service, project, workItem); err != nil {
		return projectwork.AgentRoleProfile{}, err
	}
	if _, err := cairnlinebridge.UpsertWorkItem(ctx, service, workItem); err != nil {
		return projectwork.AgentRoleProfile{}, err
	}
	if err := h.seedProjectAssignmentRootForCairnlineAuthority(ctx, service, project, assignment.RootID); err != nil {
		return projectwork.AgentRoleProfile{}, err
	}
	role, err := h.projectRoleForCairnlineAssignmentAuthority(ctx, service, assignment.ProjectID, assignment.RoleID)
	if err != nil {
		return projectwork.AgentRoleProfile{}, err
	}
	return role, nil
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

func (h *Handler) projectRoleForCairnlineAssignmentAuthority(ctx context.Context, service *cairnline.Service, projectID, roleID string) (projectwork.AgentRoleProfile, error) {
	if h != nil && h.projectWork != nil {
		if role, ok, err := h.loadProjectWorkRoleForCairnlineMirror(ctx, projectID, roleID); err != nil {
			return projectwork.AgentRoleProfile{}, err
		} else if ok {
			if _, err := cairnlinebridge.UpsertRole(ctx, service, role); err != nil {
				return projectwork.AgentRoleProfile{}, err
			}
			return role, nil
		}
	}
	role, err := getCairnlineProjectRoleForAuthority(ctx, service, projectID, roleID)
	if err != nil {
		return projectwork.AgentRoleProfile{}, err
	}
	native, err := h.projectWorkRoleFromCairnlineAuthority(ctx, service, role, projectwork.AgentRoleProfile{})
	if err != nil {
		return projectwork.AgentRoleProfile{}, err
	}
	return native, nil
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
	overlayProjectAssignmentRuntimeShadow(&out, native)
	if out.ExecutionRef.ContextSnapshotID == "" {
		out.ExecutionRef.ContextSnapshotID = strings.TrimSpace(item.ContextSnapshotID)
	}
	return out
}

func overlayProjectAssignmentRuntimeShadow(item *projectwork.Assignment, shadow projectwork.Assignment) {
	if item == nil || strings.TrimSpace(item.DriverKind) == projectwork.AssignmentDriverManual {
		return
	}
	if ref := projectwork.NormalizeAssignmentExecutionRef(shadow.ExecutionRef); !projectAssignmentExecutionRefEmpty(ref) {
		item.ExecutionRef = ref
	}
	item.ContextPacket = append([]byte(nil), shadow.ContextPacket...)
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
	if h == nil {
		return
	}
	if h.projectCairnlineEmbeddedReplacementModeArmed() {
		h.shadowProjectAssignmentRuntimeToHecate(ctx, operation, assignment)
		return
	}
	if h.projectWork == nil {
		h.shadowProjectAssignmentRuntimeToHecate(ctx, operation, assignment)
		return
	}
	if _, err := h.projectWork.CreateAssignment(ctx, assignment); err == nil {
		h.shadowProjectAssignmentRuntimeToHecate(ctx, operation, assignment)
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
		item.ContextPacket = append([]byte(nil), assignment.ContextPacket...)
		item.StartedAt = assignment.StartedAt
		item.CompletedAt = assignment.CompletedAt
	})
	if err != nil {
		h.logCairnlineMirrorError(ctx, operation, assignment.ProjectID, err)
		return
	}
	h.shadowProjectAssignmentRuntimeToHecate(ctx, operation, assignment)
}

func (h *Handler) shadowProjectAssignmentDeleteToHecate(ctx context.Context, operation, projectID, workItemID, assignmentID string) {
	if h == nil {
		return
	}
	if h.projectRuntime != nil {
		err := h.projectRuntime.Delete(ctx, projectID, assignmentID)
		if err != nil && !errors.Is(err, projectruntime.ErrNotFound) {
			h.logCairnlineMirrorError(ctx, operation, projectID, err)
		}
	}
	if h.projectCairnlineEmbeddedReplacementModeArmed() {
		return
	}
	if h.projectWork == nil {
		return
	}
	if err := h.projectWork.DeleteAssignment(ctx, projectID, workItemID, assignmentID); err != nil && !errors.Is(err, projectwork.ErrNotFound) {
		h.logCairnlineMirrorError(ctx, operation, projectID, err)
	}
}

func (h *Handler) shadowProjectAssignmentRuntimeToHecate(ctx context.Context, operation string, assignment projectwork.Assignment) {
	if h == nil || h.projectRuntime == nil {
		return
	}
	if assignment.DriverKind == projectwork.AssignmentDriverManual &&
		projectAssignmentExecutionRefEmpty(projectwork.NormalizeAssignmentExecutionRef(assignment.ExecutionRef)) &&
		len(assignment.ContextPacket) == 0 {
		if err := h.projectRuntime.Delete(ctx, assignment.ProjectID, assignment.ID); err != nil && !errors.Is(err, projectruntime.ErrNotFound) {
			h.logCairnlineMirrorError(ctx, operation, assignment.ProjectID, err)
		}
		return
	}
	if _, err := h.projectRuntime.Upsert(ctx, projectruntime.FromAssignment(assignment)); err != nil {
		h.logCairnlineMirrorError(ctx, operation, assignment.ProjectID, err)
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
	case projectwork.AssignmentDriverHecateTask, projectwork.AssignmentDriverExternalAgent, projectwork.AssignmentDriverManual:
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
