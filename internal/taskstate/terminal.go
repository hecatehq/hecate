package taskstate

import (
	"fmt"
	"strings"
	"time"

	"github.com/hecatehq/hecate/pkg/types"
)

func validateTerminalTransition(tr TerminalRunTransition) error {
	if tr.Task.ID == "" {
		return fmt.Errorf("task id is required")
	}
	if tr.Run.ID == "" {
		return fmt.Errorf("run id is required")
	}
	if tr.Run.TaskID != "" && tr.Run.TaskID != tr.Task.ID {
		return fmt.Errorf("run %q does not belong to task %q", tr.Run.ID, tr.Task.ID)
	}
	return nil
}

func terminalTransitionFinishedAt(tr TerminalRunTransition) time.Time {
	if !tr.FinishedAt.IsZero() {
		return tr.FinishedAt
	}
	if !tr.Run.FinishedAt.IsZero() {
		return tr.Run.FinishedAt
	}
	if !tr.Task.FinishedAt.IsZero() {
		return tr.Task.FinishedAt
	}
	return time.Now().UTC()
}

func terminalEventFromSpec(spec RunEventSpec, taskID, runID string, createdAt time.Time) types.TaskRunEvent {
	if spec.CreatedAt.IsZero() {
		spec.CreatedAt = createdAt
	}
	return types.TaskRunEvent{
		TaskID:    taskID,
		RunID:     runID,
		EventType: spec.EventType,
		Data:      copyEventData(spec.Data),
		RequestID: spec.RequestID,
		TraceID:   spec.TraceID,
		CreatedAt: spec.CreatedAt,
	}
}

func terminalSnapshotData(run types.TaskRun, steps []types.TaskStep, artifacts []types.TaskArtifact, extra map[string]any) map[string]any {
	data := map[string]any{
		"run":       run,
		"steps":     steps,
		"artifacts": artifacts,
	}
	for key, value := range extra {
		data[key] = value
	}
	return data
}

func copyEventData(data map[string]any) map[string]any {
	if data == nil {
		return nil
	}
	copied := make(map[string]any, len(data))
	for key, value := range data {
		copied[key] = value
	}
	return copied
}

func approvalResolvedEventData(approval types.TaskApproval) map[string]any {
	return map[string]any{
		"approval_id": approval.ID,
		"decision":    approval.Status,
		"by":          approval.ResolvedBy,
		"comment":     approval.ResolutionNote,
		"scope":       "once",
		"kind":        approval.Kind,
		"status":      approval.Status,
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
