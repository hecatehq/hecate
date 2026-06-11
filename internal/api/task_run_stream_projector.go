package api

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hecatehq/hecate/internal/runtimeevents"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

type taskRunStreamProjector struct {
	store taskstate.Store
}

func newTaskRunStreamProjector(store taskstate.Store) taskRunStreamProjector {
	return taskRunStreamProjector{store: store}
}

func (p taskRunStreamProjector) projectEvent(ctx context.Context, taskID, runID string, event types.TaskRunEvent) (TaskRunStreamEventData, error) {
	state, ok, err := p.decodeEventData(event)
	if err != nil {
		return TaskRunStreamEventData{}, err
	}
	if !ok {
		overlayTurn := state.Turn
		state, err = p.liveState(ctx, taskID, runID)
		if err != nil {
			return TaskRunStreamEventData{}, err
		}
		state.Turn = overlayTurn
	}
	state.Sequence = int(event.Sequence)
	state.EventType = event.EventType
	state.Terminal = types.IsTerminalTaskRunStatus(state.Run.Status)
	return state, nil
}

func (p taskRunStreamProjector) terminalLiveState(ctx context.Context, taskID, runID string, sequence int64, eventType string) (TaskRunStreamEventData, bool) {
	state, err := p.liveState(ctx, taskID, runID)
	if err != nil {
		return TaskRunStreamEventData{}, false
	}
	state.Sequence = int(sequence)
	state.EventType = eventType
	state.Terminal = true
	return state, true
}

func (p taskRunStreamProjector) liveState(ctx context.Context, taskID, runID string) (TaskRunStreamEventData, error) {
	run, found, err := p.store.GetRun(ctx, taskID, runID)
	if err != nil {
		return TaskRunStreamEventData{}, err
	}
	if !found {
		return TaskRunStreamEventData{}, fmt.Errorf("task run not found")
	}
	steps, err := p.store.ListSteps(ctx, runID)
	if err != nil {
		return TaskRunStreamEventData{}, err
	}
	artifacts, err := p.store.ListArtifacts(ctx, taskstate.ArtifactFilter{TaskID: taskID, RunID: runID})
	if err != nil {
		return TaskRunStreamEventData{}, err
	}
	// Approvals are listed per-task by the store; filter to the
	// current run because the streamed state is run-scoped. Failure is
	// non-fatal: keep run/step progress flowing, and let the UI's
	// separate approvals fetch act as fallback for that edge case.
	taskApprovals, err := p.store.ListApprovals(ctx, taskID)
	if err != nil {
		taskApprovals = nil
	}

	stepItems := make([]TaskStepItem, 0, len(steps))
	for _, step := range steps {
		stepItems = append(stepItems, renderTaskStep(step))
	}
	artifactItems := make([]TaskArtifactItem, 0, len(artifacts))
	for _, artifact := range artifacts {
		artifactItems = append(artifactItems, renderTaskArtifact(artifact))
	}
	approvalItems := make([]TaskApprovalItem, 0, len(taskApprovals))
	for _, approval := range taskApprovals {
		if approval.RunID != runID {
			continue
		}
		approvalItems = append(approvalItems, renderTaskApproval(approval))
	}
	activityItems := buildTaskActivityItems(stepItems, artifactItems, approvalItems, run)
	return TaskRunStreamEventData{
		Run:       renderTaskRun(run),
		Steps:     stepItems,
		Artifacts: artifactItems,
		Activity:  activityItems,
		Approvals: approvalItems,
	}, nil
}

func (p taskRunStreamProjector) snapshotEventData(state TaskRunStreamEventData) (map[string]any, error) {
	stateJSON, err := taskRunStreamStateJSON(state)
	if err != nil {
		return nil, err
	}
	var snapshot map[string]any
	if err := json.Unmarshal(stateJSON, &snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (p taskRunStreamProjector) decodeEventData(event types.TaskRunEvent) (TaskRunStreamEventData, bool, error) {
	if event.Data == nil {
		return TaskRunStreamEventData{}, false, nil
	}
	// `turn.completed` is a flat per-turn cost payload. It is not a
	// full stream snapshot, but the Turn overlay must survive the
	// live-state rebuild so the UI can render the latest turn cost.
	if event.EventType == runtimeevents.EventTurnCompleted.String() {
		turn := decodeTurnCostFromEventData(event.Data)
		return TaskRunStreamEventData{Turn: turn}, false, nil
	}
	snapshot, ok := event.Data["snapshot"]
	if ok {
		raw, err := json.Marshal(snapshot)
		if err != nil {
			return TaskRunStreamEventData{}, false, err
		}
		var decoded TaskRunStreamEventData
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return TaskRunStreamEventData{}, false, err
		}
		return decoded, true, nil
	}
	raw, err := json.Marshal(event.Data)
	if err != nil {
		return TaskRunStreamEventData{}, false, err
	}
	var typed struct {
		Run       types.TaskRun        `json:"run"`
		Steps     []types.TaskStep     `json:"steps"`
		Artifacts []types.TaskArtifact `json:"artifacts"`
	}
	if err := json.Unmarshal(raw, &typed); err == nil && typed.Run.ID != "" {
		stepItems := make([]TaskStepItem, 0, len(typed.Steps))
		for _, step := range typed.Steps {
			stepItems = append(stepItems, renderTaskStep(step))
		}
		artifactItems := make([]TaskArtifactItem, 0, len(typed.Artifacts))
		for _, artifact := range typed.Artifacts {
			artifactItems = append(artifactItems, renderTaskArtifact(artifact))
		}
		return TaskRunStreamEventData{
			Run:       renderTaskRun(typed.Run),
			Steps:     stepItems,
			Artifacts: artifactItems,
		}, true, nil
	}
	var decoded TaskRunStreamEventData
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return TaskRunStreamEventData{}, false, nil
	}
	if decoded.Run.ID == "" {
		return TaskRunStreamEventData{}, false, nil
	}
	return decoded, true, nil
}

// decodeTurnCostFromEventData lifts per-turn cost figures out of the
// turn.completed event payload. Numerics are tolerant of both in-process
// integers and JSON-roundtripped float64 values.
func decodeTurnCostFromEventData(data map[string]any) *TaskRunStreamTurnCost {
	if data == nil {
		return nil
	}
	asInt := func(v any) int {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		case int64:
			return int(n)
		}
		return 0
	}
	asInt64 := func(v any) int64 {
		switch n := v.(type) {
		case float64:
			return int64(n)
		case int:
			return int64(n)
		case int64:
			return n
		}
		return 0
	}
	asString := func(v any) string {
		if s, ok := v.(string); ok {
			return s
		}
		return ""
	}
	return &TaskRunStreamTurnCost{
		Turn:                    asInt(data["turn_index"]),
		StepID:                  asString(data["step_id"]),
		CostMicrosUSD:           asInt64(data["cost_micros_usd"]),
		RunCumulativeMicrosUSD:  asInt64(data["run_cumulative_cost_micros_usd"]),
		TaskCumulativeMicrosUSD: asInt64(data["task_cumulative_cost_micros_usd"]),
		ToolCallCount:           asInt(data["tool_calls"]),
	}
}
