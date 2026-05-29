package runtimeevents

import "github.com/hecatehq/hecate/pkg/types"

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
