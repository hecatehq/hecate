package api

import (
	"context"

	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/projectworkapp"
)

func (h *Handler) renderProjectedProjectWorkItem(ctx context.Context, item projectwork.WorkItem) (ProjectWorkItemResponse, error) {
	if h == nil || h.projectWork == nil {
		return renderProjectWorkItem(item), nil
	}
	assignments, err := h.projectWork.ListAssignments(ctx, projectwork.AssignmentFilter{
		ProjectID:  item.ProjectID,
		WorkItemID: item.ID,
	})
	if err != nil {
		return ProjectWorkItemResponse{}, err
	}
	return h.renderProjectedProjectWorkItemWithAssignments(ctx, item, assignments)
}

func (h *Handler) renderProjectedProjectWorkItemWithAssignments(ctx context.Context, item projectwork.WorkItem, assignments []projectwork.Assignment) (ProjectWorkItemResponse, error) {
	response := renderProjectWorkItem(item)
	if len(assignments) == 0 {
		return response, nil
	}
	projected := make([]ProjectWorkAssignmentResponse, 0, len(assignments))
	for _, assignment := range assignments {
		projectedAssignment, err := h.renderProjectedProjectWorkAssignment(ctx, assignment)
		if err != nil {
			return ProjectWorkItemResponse{}, err
		}
		projected = append(projected, projectedAssignment)
	}
	response.Assignments = projected
	response.Status = projectWorkItemStatusFromAssignments(item.Status, projected)
	return response, nil
}

func (h *Handler) renderProjectedProjectWorkAssignment(ctx context.Context, item projectwork.Assignment) (ProjectWorkAssignmentResponse, error) {
	response := renderProjectWorkAssignment(item)
	projection, err := projectworkapp.ProjectAssignmentExecution(ctx, h.taskStore, item)
	if err != nil {
		return ProjectWorkAssignmentResponse{}, err
	}
	if projection == nil {
		return response, nil
	}
	response.Execution = renderProjectWorkAssignmentExecution(projection.Execution)
	if projection.Status != "" {
		response.Status = projection.Status
	}
	if response.StartedAt == "" && !projection.StartedAt.IsZero() {
		response.StartedAt = formatOptionalTime(projection.StartedAt)
	}
	if response.CompletedAt == "" && !projection.CompletedAt.IsZero() {
		response.CompletedAt = formatOptionalTime(projection.CompletedAt)
	}
	response.ExecutionRef = renderProjectWorkAssignmentExecutionRef(projectworkapp.AssignmentExecutionRefFor(item, &projection.Execution, response.Status))
	return response, nil
}

func renderProjectWorkAssignmentExecution(execution projectworkapp.AssignmentExecutionSummary) *ProjectWorkAssignmentExecutionResponse {
	return &ProjectWorkAssignmentExecutionResponse{
		TaskID:               execution.TaskID,
		RunID:                execution.RunID,
		TaskStatus:           execution.TaskStatus,
		RunStatus:            execution.RunStatus,
		Status:               execution.Status,
		PendingApprovalCount: execution.PendingApprovalCount,
		StepCount:            execution.StepCount,
		ApprovalCount:        execution.ApprovalCount,
		ArtifactCount:        execution.ArtifactCount,
		Model:                execution.Model,
		Provider:             execution.Provider,
		LastError:            execution.LastError,
		StartedAt:            formatOptionalTime(execution.StartedAt),
		FinishedAt:           formatOptionalTime(execution.FinishedAt),
		TraceID:              execution.TraceID,
		Missing:              execution.Missing,
	}
}

func groupProjectWorkAssignmentsByWorkItem(assignments []projectwork.Assignment) map[string][]projectwork.Assignment {
	if len(assignments) == 0 {
		return map[string][]projectwork.Assignment{}
	}
	grouped := make(map[string][]projectwork.Assignment, len(assignments))
	for _, assignment := range assignments {
		grouped[assignment.WorkItemID] = append(grouped[assignment.WorkItemID], assignment)
	}
	return grouped
}
