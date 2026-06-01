package runtimeevents_test

import (
	"reflect"
	"testing"

	"github.com/hecatehq/hecate/internal/runtimeevents"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestPayloads_ApprovalRequested(t *testing.T) {
	t.Parallel()

	approval := types.TaskApproval{
		ID:          "approval-1",
		Kind:        "agent_loop_tool_call",
		Status:      "pending",
		Reason:      "tool requires approval",
		StepID:      "step-1",
		RequestedBy: "agent",
	}

	got := runtimeevents.ApprovalRequested(approval)
	want := map[string]any{
		"approval_id":   "approval-1",
		"kind":          "agent_loop_tool_call",
		"status":        "pending",
		"policy_reason": "tool requires approval",
		"step_id":       "step-1",
		"requested_by":  "agent",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ApprovalRequested() = %+v, want %+v", got, want)
	}
}

func TestPayloads_ApprovalRequestedOmitsEmptyOptionalFields(t *testing.T) {
	t.Parallel()

	got := runtimeevents.ApprovalRequested(types.TaskApproval{
		ID:     "approval-1",
		Kind:   "shell_command",
		Status: "pending",
		Reason: "shell execution requires approval",
	})

	if _, ok := got["step_id"]; ok {
		t.Fatalf("step_id present in %+v, want omitted", got)
	}
	if _, ok := got["requested_by"]; ok {
		t.Fatalf("requested_by present in %+v, want omitted", got)
	}
}

func TestPayloads_ApprovalResolved(t *testing.T) {
	t.Parallel()

	approval := types.TaskApproval{
		ID:             "approval-1",
		Kind:           "agent_loop_tool_call",
		Status:         "rejected",
		ResolvedBy:     "operator",
		ResolutionNote: "not safe",
	}

	got := runtimeevents.ApprovalResolved(approval)
	want := map[string]any{
		"approval_id": "approval-1",
		"decision":    "rejected",
		"by":          "operator",
		"comment":     "not safe",
		"scope":       "once",
		"kind":        "agent_loop_tool_call",
		"status":      "rejected",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ApprovalResolved() = %+v, want %+v", got, want)
	}
}

func TestPayloads_TurnCompleted(t *testing.T) {
	t.Parallel()

	got := runtimeevents.TurnCompleted(runtimeevents.TurnCompletedFields{
		TurnIndex:                   2,
		StepID:                      "step-model-2",
		CostMicrosUSD:               42,
		RunCumulativeCostMicrosUSD:  100,
		TaskCumulativeCostMicrosUSD: 250,
		ToolCalls:                   3,
	})
	want := map[string]any{
		"turn_index":                      2,
		"step_id":                         "step-model-2",
		"cost_micros_usd":                 int64(42),
		"run_cumulative_cost_micros_usd":  int64(100),
		"task_cumulative_cost_micros_usd": int64(250),
		"tool_calls":                      3,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TurnCompleted() = %+v, want %+v", got, want)
	}
}

func TestPayloads_PatchApplied(t *testing.T) {
	t.Parallel()

	artifact := types.TaskArtifact{
		ID:     "artifact-1",
		Path:   "src/app.go",
		Status: "applied",
	}

	applied := runtimeevents.PatchApplied(artifact)
	wantApplied := map[string]any{
		"artifact_id":     "artifact-1",
		"path":            "src/app.go",
		"artifact_status": "applied",
	}
	if !reflect.DeepEqual(applied, wantApplied) {
		t.Fatalf("PatchApplied() = %+v, want %+v", applied, wantApplied)
	}
}

func TestPayloads_PatchReverted(t *testing.T) {
	t.Parallel()

	artifact := types.TaskArtifact{
		ID:     "artifact-1",
		Path:   "src/app.go",
		Status: "reverted",
	}

	reverted := runtimeevents.PatchReverted(artifact, true)
	wantReverted := map[string]any{
		"artifact_id":     "artifact-1",
		"path":            "src/app.go",
		"artifact_status": "reverted",
		"before_existed":  true,
	}
	if !reflect.DeepEqual(reverted, wantReverted) {
		t.Fatalf("PatchReverted() = %+v, want %+v", reverted, wantReverted)
	}
}
