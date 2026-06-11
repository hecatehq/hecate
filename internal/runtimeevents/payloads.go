package runtimeevents

import "github.com/hecatehq/hecate/pkg/types"

type EventType string

const (
	EventRunCreated          EventType = "run.created"
	EventRunQueued           EventType = "run.queued"
	EventRunStarted          EventType = "run.started"
	EventRunAwaitingApproval EventType = "run.awaiting_approval"
	EventRunResumedFromEvent EventType = "run.resumed_from_event"
	EventRunFinished         EventType = "run.finished"
	EventRunFailed           EventType = "run.failed"
	EventRunCancelled        EventType = "run.cancelled"
	EventTaskUpdated         EventType = "task.updated"
	EventGapRunDisconnected  EventType = "gap.run_disconnected"

	EventTurnStarted   EventType = "turn.started"
	EventTurnCompleted EventType = "turn.completed"

	EventAssistantTextComplete     EventType = "assistant.text_complete"
	EventAssistantToolCallProposed EventType = "assistant.tool_call_proposed"
	EventAssistantFinalAnswer      EventType = "assistant.final_answer"

	EventToolInvoked          EventType = "tool.invoked"
	EventToolStarted          EventType = "tool.started"
	EventToolShellCommand     EventType = "tool.shell.command"
	EventToolShellOutputChunk EventType = "tool.shell.output_chunk"
	EventToolShellExited      EventType = "tool.shell.exited"
	EventToolCompleted        EventType = "tool.completed"
	EventToolTimedOut         EventType = "tool.timed_out"
	EventToolCancelled        EventType = "tool.cancelled"
	EventToolFailed           EventType = "tool.failed"
	EventFilePatch            EventType = "tool.file.patch"
	EventPatchApplied         EventType = "tool.file.applied"
	EventPatchReverted        EventType = "tool.file.reverted"

	EventApprovalRequested EventType = "approval.requested"
	EventApprovalResolved  EventType = "approval.resolved"
)

func (t EventType) String() string {
	return string(t)
}

type TurnCompletedFields struct {
	TurnIndex                   int
	StepID                      string
	CostMicrosUSD               int64
	RunCumulativeCostMicrosUSD  int64
	TaskCumulativeCostMicrosUSD int64
	ToolCalls                   int
}

func ApprovalRequested(approval types.TaskApproval) map[string]any {
	data := map[string]any{
		"approval_id":   approval.ID,
		"kind":          approval.Kind,
		"status":        approval.Status,
		"policy_reason": approval.Reason,
	}
	if approval.StepID != "" {
		data["step_id"] = approval.StepID
	}
	if approval.RequestedBy != "" {
		data["requested_by"] = approval.RequestedBy
	}
	return data
}

func ApprovalResolved(approval types.TaskApproval) map[string]any {
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

func TurnCompleted(fields TurnCompletedFields) map[string]any {
	return map[string]any{
		"turn_index":                      fields.TurnIndex,
		"step_id":                         fields.StepID,
		"cost_micros_usd":                 fields.CostMicrosUSD,
		"run_cumulative_cost_micros_usd":  fields.RunCumulativeCostMicrosUSD,
		"task_cumulative_cost_micros_usd": fields.TaskCumulativeCostMicrosUSD,
		"tool_calls":                      fields.ToolCalls,
	}
}

func PatchApplied(artifact types.TaskArtifact) map[string]any {
	return map[string]any{
		"artifact_id":     artifact.ID,
		"path":            artifact.Path,
		"artifact_status": artifact.Status,
	}
}

func PatchReverted(artifact types.TaskArtifact, beforeExisted bool) map[string]any {
	return map[string]any{
		"artifact_id":     artifact.ID,
		"path":            artifact.Path,
		"artifact_status": artifact.Status,
		"before_existed":  beforeExisted,
	}
}
