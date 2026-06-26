package api

import (
	"context"
	"errors"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/cairnlinebridge"
	"github.com/hecatehq/hecate/internal/projectwork"
)

func (h *Handler) renderProjectWorkItems(ctx context.Context, projectID string) ([]ProjectWorkItemResponse, error) {
	if h.projectReadRoutesUseCairnlineReadModel() {
		return h.renderCairnlineProjectWorkItems(ctx, projectID)
	}
	items, err := h.projectWork.ListWorkItems(ctx, projectID)
	if err != nil {
		return nil, err
	}
	assignments, err := h.projectWork.ListAssignments(ctx, projectwork.AssignmentFilter{ProjectID: projectID})
	if err != nil {
		return nil, err
	}
	assignmentsByWorkItem := groupProjectWorkAssignmentsByWorkItem(assignments)
	data := make([]ProjectWorkItemResponse, 0, len(items))
	for _, item := range items {
		projected, err := h.renderProjectedProjectWorkItemWithAssignments(ctx, item, assignmentsByWorkItem[item.ID])
		if err != nil {
			return nil, err
		}
		projected.ReadBackend = "hecate"
		markProjectWorkAssignmentReadBackend(projected.Assignments, "hecate")
		data = append(data, projected)
	}
	return data, nil
}

func (h *Handler) renderCairnlineProjectWorkItems(ctx context.Context, projectID string) ([]ProjectWorkItemResponse, error) {
	service, snapshot, err := h.cairnlineProjectWorkService(ctx, projectID)
	if err != nil {
		return nil, err
	}
	items, err := service.ListWorkItems(ctx, snapshot.Project.ID)
	if err != nil {
		return nil, err
	}
	assignmentsByWorkItem := groupProjectWorkAssignmentsByWorkItem(snapshot.Assignments)
	data := make([]ProjectWorkItemResponse, 0, len(items))
	for _, item := range items {
		projected, err := h.renderProjectedProjectWorkItemWithAssignments(ctx, projectWorkItemFromCairnline(item), assignmentsByWorkItem[item.ID])
		if err != nil {
			return nil, err
		}
		projected.ReadBackend = "cairnline"
		markProjectWorkAssignmentReadBackend(projected.Assignments, "cairnline")
		data = append(data, projected)
	}
	return data, nil
}

func (h *Handler) renderCairnlineProjectWorkItem(ctx context.Context, projectID, workItemID string) (ProjectWorkItemResponse, error) {
	service, snapshot, err := h.cairnlineProjectWorkService(ctx, projectID)
	if err != nil {
		return ProjectWorkItemResponse{}, err
	}
	items, err := service.ListWorkItems(ctx, snapshot.Project.ID)
	if err != nil {
		return ProjectWorkItemResponse{}, err
	}
	assignmentsByWorkItem := groupProjectWorkAssignmentsByWorkItem(snapshot.Assignments)
	for _, item := range items {
		if item.ID != workItemID {
			continue
		}
		projected, err := h.renderProjectedProjectWorkItemWithAssignments(ctx, projectWorkItemFromCairnline(item), assignmentsByWorkItem[item.ID])
		if err != nil {
			return ProjectWorkItemResponse{}, err
		}
		projected.ReadBackend = "cairnline"
		markProjectWorkAssignmentReadBackend(projected.Assignments, "cairnline")
		return projected, nil
	}
	return ProjectWorkItemResponse{}, projectwork.ErrNotFound
}

func (h *Handler) renderCairnlineProjectWorkAssignments(ctx context.Context, projectID, workItemID string) ([]ProjectWorkAssignmentResponse, error) {
	service, snapshot, err := h.cairnlineProjectWorkService(ctx, projectID)
	if err != nil {
		return nil, err
	}
	items, err := service.ListAssignments(ctx, snapshot.Project.ID)
	if err != nil {
		return nil, err
	}
	nativeByID := projectWorkAssignmentsByID(snapshot.Assignments)
	data := make([]ProjectWorkAssignmentResponse, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.WorkItemID) != workItemID {
			continue
		}
		assignment := projectWorkAssignmentFromCairnline(item)
		if native, ok := nativeByID[item.ID]; ok {
			assignment = native
		}
		projected, err := h.renderProjectedProjectWorkAssignment(ctx, assignment)
		if err != nil {
			return nil, err
		}
		projected.ReadBackend = "cairnline"
		data = append(data, projected)
	}
	return data, nil
}

func markProjectWorkAssignmentReadBackend(items []ProjectWorkAssignmentResponse, backend string) {
	for index := range items {
		items[index].ReadBackend = backend
	}
}

func (h *Handler) renderCairnlineProjectWorkItemReadiness(ctx context.Context, projectID, workItemID string) (ProjectWorkItemReadinessResponse, error) {
	service, snapshot, err := h.cairnlineProjectWorkService(ctx, projectID)
	if err != nil {
		return ProjectWorkItemReadinessResponse{}, err
	}
	readiness, err := service.WorkItemCloseoutReadiness(ctx, snapshot.Project.ID, workItemID)
	if errors.Is(err, cairnline.ErrNotFound) {
		return ProjectWorkItemReadinessResponse{}, projectwork.ErrNotFound
	}
	if err != nil {
		return ProjectWorkItemReadinessResponse{}, err
	}
	return renderCairnlineProjectWorkItemReadiness(readiness), nil
}

func (h *Handler) cairnlineProjectWorkService(ctx context.Context, projectID string) (*cairnline.Service, cairnlinebridge.Snapshot, error) {
	service := cairnline.NewMemoryService()
	snapshot, err := cairnlinebridge.SeedProjectFromStores(ctx, service, h.cairnlineSnapshotSources(), projectID)
	if err != nil {
		return nil, cairnlinebridge.Snapshot{}, err
	}
	return service, snapshot, nil
}

func projectWorkItemFromCairnline(item cairnline.WorkItem) projectwork.WorkItem {
	return projectwork.WorkItem{
		ID:              item.ID,
		ProjectID:       item.ProjectID,
		Title:           item.Title,
		Brief:           item.Brief,
		Status:          item.Status,
		Priority:        item.Priority,
		OwnerRoleID:     item.OwnerRoleID,
		RootID:          item.RootID,
		ReviewerRoleIDs: append([]string(nil), item.ReviewerRoleIDs...),
		CreatedAt:       item.CreatedAt,
		UpdatedAt:       item.UpdatedAt,
	}
}

func projectWorkAssignmentsByID(items []projectwork.Assignment) map[string]projectwork.Assignment {
	out := make(map[string]projectwork.Assignment, len(items))
	for _, item := range items {
		out[item.ID] = item
	}
	return out
}

func projectWorkAssignmentFromCairnline(item cairnline.Assignment) projectwork.Assignment {
	return projectwork.Assignment{
		ID:         item.ID,
		ProjectID:  item.ProjectID,
		WorkItemID: item.WorkItemID,
		RoleID:     item.RoleID,
		RootID:     item.RootID,
		DriverKind: projectWorkAssignmentDriverFromCairnline(item.ExecutionMode),
		Status:     projectWorkAssignmentStatusFromCairnline(item.Status),
		ExecutionRef: projectwork.NormalizeAssignmentExecutionRef(projectwork.AssignmentExecutionRef{
			ContextSnapshotID: item.ContextSnapshotID,
		}),
		CreatedAt: item.CreatedAt,
		UpdatedAt: item.UpdatedAt,
	}
}

func projectWorkAssignmentDriverFromCairnline(mode string) string {
	switch strings.TrimSpace(mode) {
	case cairnline.ExecutionExternalAdapter:
		return projectwork.AssignmentDriverExternalAgent
	default:
		return projectwork.AssignmentDriverHecateTask
	}
}

func projectWorkAssignmentStatusFromCairnline(status string) string {
	switch strings.TrimSpace(status) {
	case cairnline.AssignmentRunning, cairnline.AssignmentClaimed:
		return projectwork.AssignmentStatusRunning
	case cairnline.AssignmentReview:
		return projectwork.AssignmentStatusAwaitingApproval
	case cairnline.AssignmentCompleted:
		return projectwork.AssignmentStatusCompleted
	case cairnline.AssignmentFailed:
		return projectwork.AssignmentStatusFailed
	case cairnline.AssignmentCancelled:
		return projectwork.AssignmentStatusCancelled
	default:
		return projectwork.AssignmentStatusQueued
	}
}

func renderCairnlineProjectWorkItemReadiness(readiness cairnline.WorkItemCloseoutReadiness) ProjectWorkItemReadinessResponse {
	return ProjectWorkItemReadinessResponse{
		ProjectID:                    readiness.ProjectID,
		WorkItemID:                   readiness.WorkItemID,
		ReadBackend:                  "cairnline",
		Ready:                        readiness.Ready,
		Status:                       readiness.Status,
		Title:                        readiness.Title,
		Detail:                       readiness.Detail,
		Blockers:                     append([]string(nil), readiness.Blockers...),
		Warnings:                     append([]string(nil), readiness.Warnings...),
		AssignmentCount:              readiness.AssignmentCount,
		CompletedAssignments:         readiness.CompletedAssignments,
		ReviewFollowUpCount:          readiness.ReviewFollowUpCount,
		ReviewFollowUpArtifactIDs:    append([]string(nil), readiness.ReviewFollowUpArtifactIDs...),
		ReviewFollowUps:              renderCairnlineProjectWorkItemReviewFollowUps(readiness.ReviewFollowUps),
		MissingEvidenceAssignmentIDs: append([]string(nil), readiness.MissingEvidenceAssignmentIDs...),
	}
}

func renderCairnlineProjectWorkItemReviewFollowUps(items []cairnline.ReviewFollowUpReadiness) []ProjectWorkItemReviewFollowUpResponse {
	if len(items) == 0 {
		return nil
	}
	out := make([]ProjectWorkItemReviewFollowUpResponse, 0, len(items))
	for _, item := range items {
		out = append(out, ProjectWorkItemReviewFollowUpResponse{
			ArtifactID:           item.ArtifactID,
			Title:                item.Title,
			Status:               item.Status,
			Blocker:              item.Blocker,
			ReviewedAssignmentID: item.ReviewedAssignmentID,
			ReviewVerdict:        item.ReviewVerdict,
			ReviewRisk:           item.ReviewRisk,
		})
	}
	return out
}
