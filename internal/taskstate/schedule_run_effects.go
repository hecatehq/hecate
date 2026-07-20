package taskstate

import (
	"fmt"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/runtimeevents"
	"github.com/hecatehq/hecate/pkg/types"
)

func validateTaskScheduleRunInitialEffects(admission TaskScheduleRunAdmission) error {
	switch admission.Run.Status {
	case "queued":
		if admission.Approval != nil {
			return fmt.Errorf("queued scheduled run must not include an approval")
		}
	case "awaiting_approval":
		if admission.Approval == nil {
			return fmt.Errorf("awaiting-approval scheduled run requires a pending approval")
		}
		approval := *admission.Approval
		if strings.TrimSpace(approval.ID) == "" || approval.TaskID != admission.Task.ID || approval.RunID != admission.Run.ID || approval.Status != "pending" {
			return fmt.Errorf("scheduled run approval must be pending and belong to the admitted task and run")
		}
		if admission.Run.ApprovalCount != 1 {
			return fmt.Errorf("awaiting-approval scheduled run approval count must be 1")
		}
	default:
		return fmt.Errorf("scheduled run initial status must be queued or awaiting_approval")
	}
	return nil
}

func alignTaskScheduleRunAdmissionTime(admission TaskScheduleRunAdmission, occurrence TaskScheduleOccurrence) TaskScheduleRunAdmission {
	if admission.CompletedAt.Before(occurrence.ClaimedAt) {
		admission.CompletedAt = occurrence.ClaimedAt
	}
	if admission.Approval != nil && admission.Approval.CreatedAt.Before(admission.CompletedAt) {
		approval := *admission.Approval
		approval.CreatedAt = admission.CompletedAt
		admission.Approval = &approval
	}
	return admission
}

func taskScheduleRunInitialEvents(admission TaskScheduleRunAdmission, run types.TaskRun) []types.TaskRunEvent {
	createdAt := admission.CompletedAt
	events := []types.TaskRunEvent{taskScheduleRunInitialEvent(
		run, runtimeevents.EventRunCreated.String(), nil, run.RequestID, run.TraceID, createdAt,
	)}
	if admission.Approval == nil {
		return append(events, taskScheduleRunInitialEvent(
			run, runtimeevents.EventRunQueued.String(), nil, run.RequestID, run.TraceID, createdAt,
		))
	}
	approval := *admission.Approval
	events = append(events,
		taskScheduleRunInitialEvent(
			run, runtimeevents.EventApprovalRequested.String(), runtimeevents.ApprovalRequested(approval),
			approval.RequestID, approval.TraceID, approval.CreatedAt,
		),
		taskScheduleRunInitialEvent(
			run, runtimeevents.EventRunAwaitingApproval.String(), nil, run.RequestID, run.TraceID, createdAt,
		),
	)
	return events
}

func taskScheduleRunInitialEvent(run types.TaskRun, eventType string, extra map[string]any, requestID, traceID string, createdAt time.Time) types.TaskRunEvent {
	data := make(map[string]any, len(extra)+3)
	data["run"] = run
	data["steps"] = []types.TaskStep{}
	data["artifacts"] = []types.TaskArtifact{}
	for key, value := range extra {
		data[key] = value
	}
	return types.TaskRunEvent{
		TaskID: run.TaskID, RunID: run.ID, EventType: eventType, Data: data,
		RequestID: requestID, TraceID: traceID, CreatedAt: createdAt,
	}
}
