package projectworkapp

import (
	"context"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/pkg/types"
)

const (
	AssignmentExecutionKindTaskRun         = projectwork.AssignmentExecutionKindTaskRun
	AssignmentExecutionKindChatSession     = projectwork.AssignmentExecutionKindChatSession
	AssignmentExecutionKindContextSnapshot = projectwork.AssignmentExecutionKindContextSnapshot
)

type AssignmentProjectionStore interface {
	TaskRunLookupStore
	GetTask(ctx context.Context, taskID string) (types.Task, bool, error)
	ListApprovals(ctx context.Context, taskID string) ([]types.TaskApproval, error)
}

type AssignmentExecutionSummary struct {
	TaskID               string
	RunID                string
	TaskStatus           string
	RunStatus            string
	Status               string
	PendingApprovalCount int
	StepCount            int
	ApprovalCount        int
	ArtifactCount        int
	Model                string
	Provider             string
	LastError            string
	StartedAt            time.Time
	FinishedAt           time.Time
	TraceID              string
	Missing              bool
}

type AssignmentExecutionRef = projectwork.AssignmentExecutionRef

type AssignmentExecutionProjection struct {
	Execution   AssignmentExecutionSummary
	Status      string
	StartedAt   time.Time
	CompletedAt time.Time
}

func ProjectAssignmentExecution(ctx context.Context, store AssignmentProjectionStore, assignment projectwork.Assignment) (*AssignmentExecutionProjection, error) {
	ref := projectwork.NormalizeAssignmentExecutionRef(assignment.ExecutionRef)
	taskID := strings.TrimSpace(ref.TaskID)
	runID := strings.TrimSpace(ref.RunID)
	if taskID == "" {
		return nil, nil
	}
	projection := &AssignmentExecutionProjection{
		Status:    assignment.Status,
		StartedAt: assignment.StartedAt,
		Execution: AssignmentExecutionSummary{
			TaskID: taskID,
			RunID:  runID,
		},
	}
	if store == nil {
		projection.Execution.Missing = true
		return projection, nil
	}

	foundTask, found, err := store.GetTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if !found {
		projection.Execution.Missing = true
		return projection, nil
	}
	task := foundTask
	projection.Execution.TaskStatus = task.Status
	if runID == "" {
		runID = strings.TrimSpace(task.LatestRunID)
		projection.Execution.RunID = runID
	}

	if runID == "" {
		status := AssignmentStatusFromRun(task.Status)
		projection.Execution.Status = status
		projection.Status = ProjectedAssignmentStatus(assignment, status, task.UpdatedAt)
		return projection, nil
	}

	run, found, err := store.GetRun(ctx, taskID, runID)
	if err != nil {
		return nil, err
	}
	if !found {
		projection.Execution.Missing = true
		return projection, nil
	}

	status := AssignmentStatusFromRun(run.Status)
	pendingApprovalCount := 0
	if status == projectwork.AssignmentStatusAwaitingApproval {
		pendingCount, err := PendingApprovalCount(ctx, store, taskID, runID)
		if err != nil {
			return nil, err
		}
		pendingApprovalCount = pendingCount
	}
	projection.Status = ProjectedAssignmentStatus(assignment, status, RunProjectionTime(run))
	projection.StartedAt = FirstNonZeroTime(assignment.StartedAt, run.StartedAt)
	if types.IsTerminalTaskRunStatus(run.Status) {
		projection.CompletedAt = FirstNonZeroTime(assignment.CompletedAt, run.FinishedAt)
	} else {
		projection.CompletedAt = assignment.CompletedAt
	}
	projection.Execution = AssignmentExecutionSummary{
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
		StartedAt:            run.StartedAt,
		FinishedAt:           run.FinishedAt,
		TraceID:              run.TraceID,
	}
	return projection, nil
}

func AssignmentExecutionRefFor(assignment projectwork.Assignment, execution *AssignmentExecutionSummary, status string) *AssignmentExecutionRef {
	base := projectwork.NormalizeAssignmentExecutionRef(assignment.ExecutionRef)
	taskID := strings.TrimSpace(base.TaskID)
	runID := strings.TrimSpace(base.RunID)
	status = firstNonEmpty(status, base.Status)
	pendingApprovalCount := base.PendingApprovalCount
	traceID := base.TraceID
	missing := base.Missing
	if execution != nil {
		taskID = firstNonEmpty(execution.TaskID, taskID)
		runID = firstNonEmpty(execution.RunID, runID)
		status = firstNonEmpty(execution.Status, status)
		pendingApprovalCount = execution.PendingApprovalCount
		traceID = execution.TraceID
		missing = execution.Missing
	}
	chatSessionID := strings.TrimSpace(base.ChatSessionID)
	messageID := strings.TrimSpace(base.MessageID)
	contextSnapshotID := strings.TrimSpace(base.ContextSnapshotID)
	kind := firstNonEmpty(base.Kind, AssignmentExecutionRefKind(taskID, runID, chatSessionID, messageID, contextSnapshotID))
	if kind == "" {
		return nil
	}
	return &AssignmentExecutionRef{
		Kind:                 kind,
		TaskID:               taskID,
		RunID:                runID,
		ChatSessionID:        chatSessionID,
		MessageID:            messageID,
		ContextSnapshotID:    contextSnapshotID,
		Status:               strings.TrimSpace(status),
		PendingApprovalCount: pendingApprovalCount,
		TraceID:              traceID,
		Missing:              missing,
	}
}

func AssignmentExecutionRefKind(taskID, runID, chatSessionID, messageID, contextSnapshotID string) string {
	return projectwork.AssignmentExecutionRefKind(taskID, runID, chatSessionID, messageID, contextSnapshotID)
}

func ProjectedAssignmentStatus(assignment projectwork.Assignment, projectedStatus string, projectedAt time.Time) string {
	projectedStatus = strings.TrimSpace(projectedStatus)
	if projectedStatus == "" {
		return assignment.Status
	}
	if AssignmentIsTerminal(assignment.Status) && assignment.Status != projectedStatus {
		if projectedAt.IsZero() || !projectedAt.After(assignment.UpdatedAt) {
			return assignment.Status
		}
	}
	return projectedStatus
}

func RunProjectionTime(run types.TaskRun) time.Time {
	if !run.FinishedAt.IsZero() {
		return run.FinishedAt
	}
	return run.StartedAt
}

func FirstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func PendingApprovalCount(ctx context.Context, store AssignmentProjectionStore, taskID, runID string) (int, error) {
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
