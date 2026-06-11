package api

import (
	"context"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/pkg/types"
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
	projection, err := h.projectWorkAssignmentExecution(ctx, item)
	if err != nil {
		return ProjectWorkAssignmentResponse{}, err
	}
	if projection == nil {
		return response, nil
	}
	response.Execution = &projection.Execution
	if projection.Status != "" {
		response.Status = projection.Status
	}
	if response.StartedAt == "" && !projection.StartedAt.IsZero() {
		response.StartedAt = formatOptionalTime(projection.StartedAt)
	}
	if response.CompletedAt == "" && !projection.CompletedAt.IsZero() {
		response.CompletedAt = formatOptionalTime(projection.CompletedAt)
	}
	response.ExecutionRef = projectWorkAssignmentExecutionRef(response)
	return response, nil
}

type projectWorkAssignmentExecutionProjection struct {
	Execution   ProjectWorkAssignmentExecutionResponse
	Status      string
	StartedAt   time.Time
	CompletedAt time.Time
}

func (h *Handler) projectWorkAssignmentExecution(ctx context.Context, assignment projectwork.Assignment) (*projectWorkAssignmentExecutionProjection, error) {
	taskID := strings.TrimSpace(assignment.TaskID)
	runID := strings.TrimSpace(assignment.RunID)
	if taskID == "" {
		return nil, nil
	}
	projection := &projectWorkAssignmentExecutionProjection{
		Status:    assignment.Status,
		StartedAt: assignment.StartedAt,
		Execution: ProjectWorkAssignmentExecutionResponse{
			TaskID: taskID,
			RunID:  runID,
		},
	}
	if h == nil || h.taskStore == nil {
		projection.Execution.Missing = true
		return projection, nil
	}

	var task types.Task
	if taskID != "" {
		foundTask, found, err := h.taskStore.GetTask(ctx, taskID)
		if err != nil {
			return nil, err
		}
		if !found {
			projection.Execution.Missing = true
			return projection, nil
		}
		task = foundTask
		projection.Execution.TaskStatus = task.Status
		if runID == "" {
			runID = strings.TrimSpace(task.LatestRunID)
			projection.Execution.RunID = runID
		}
	}

	if runID == "" {
		status := projectWorkAssignmentStatusFromRun(task.Status)
		projection.Execution.Status = status
		projection.Status = projectWorkProjectedAssignmentStatus(assignment, status, task.UpdatedAt)
		return projection, nil
	}

	run, found, err := h.taskStore.GetRun(ctx, taskID, runID)
	if err != nil {
		return nil, err
	}
	if !found {
		projection.Execution.Missing = true
		return projection, nil
	}

	status := projectWorkAssignmentStatusFromRun(run.Status)
	pendingApprovalCount := 0
	if status == projectwork.AssignmentStatusAwaitingApproval {
		pendingCount, err := projectWorkPendingApprovalCount(ctx, h.taskStore, taskID, runID)
		if err != nil {
			return nil, err
		}
		pendingApprovalCount = pendingCount
	}
	projection.Status = projectWorkProjectedAssignmentStatus(assignment, status, projectWorkRunProjectionTime(run))
	projection.StartedAt = firstNonZeroTime(assignment.StartedAt, run.StartedAt)
	if types.IsTerminalTaskRunStatus(run.Status) {
		projection.CompletedAt = firstNonZeroTime(assignment.CompletedAt, run.FinishedAt)
	} else {
		projection.CompletedAt = assignment.CompletedAt
	}
	projection.Execution = ProjectWorkAssignmentExecutionResponse{
		TaskID:               taskID,
		RunID:                runID,
		TaskStatus:           task.Status,
		RunStatus:            run.Status,
		Status:               status,
		PendingApprovalCount: pendingApprovalCount,
		StepCount:            run.StepCount,
		ApprovalCount:        run.ApprovalCount,
		ArtifactCount:        run.ArtifactCount,
		Model:                run.Model,
		Provider:             run.Provider,
		LastError:            run.LastError,
		StartedAt:            formatOptionalTime(run.StartedAt),
		FinishedAt:           formatOptionalTime(run.FinishedAt),
		TraceID:              run.TraceID,
	}
	return projection, nil
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

func projectWorkProjectedAssignmentStatus(assignment projectwork.Assignment, projectedStatus string, projectedAt time.Time) string {
	projectedStatus = strings.TrimSpace(projectedStatus)
	if projectedStatus == "" {
		return assignment.Status
	}
	if projectWorkAssignmentIsTerminal(assignment.Status) && assignment.Status != projectedStatus {
		if projectedAt.IsZero() || !projectedAt.After(assignment.UpdatedAt) {
			return assignment.Status
		}
	}
	return projectedStatus
}

func projectWorkRunProjectionTime(run types.TaskRun) time.Time {
	if !run.FinishedAt.IsZero() {
		return run.FinishedAt
	}
	return run.StartedAt
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func projectWorkPendingApprovalCount(ctx context.Context, store taskRunApprovalStore, taskID, runID string) (int, error) {
	if store == nil {
		return 0, nil
	}
	approvals, err := store.ListApprovals(ctx, taskID)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, approval := range approvals {
		if approval.RunID == runID && approval.Status == "pending" {
			count++
		}
	}
	return count, nil
}

type taskRunApprovalStore interface {
	ListApprovals(ctx context.Context, taskID string) ([]types.TaskApproval, error)
}
