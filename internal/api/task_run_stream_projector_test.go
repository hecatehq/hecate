package api

import (
	"context"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestTaskRunStreamProjector_ModelCallCompletedMergesOverlayWithLiveState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	seedTaskRunStreamProjectorRun(t, ctx, store, "task-model-call", "run-model-call", "running")
	projector := newTaskRunStreamProjector(store)

	state, err := projector.projectEvent(ctx, "task-model-call", "run-model-call", types.TaskRunEvent{
		Sequence:  7,
		EventType: "model.call.completed",
		Data: map[string]any{
			"model_call_index":                float64(3),
			"step_id":                         "step-model-call",
			"cost_micros_usd":                 float64(4200),
			"run_cumulative_cost_micros_usd":  float64(8200),
			"task_cumulative_cost_micros_usd": float64(12000),
			"tool_calls":                      float64(2),
		},
	})
	if err != nil {
		t.Fatalf("projectEvent() error = %v", err)
	}
	if state.Run.ID != "run-model-call" {
		t.Fatalf("Run.ID = %q, want run-model-call", state.Run.ID)
	}
	if state.Sequence != 7 || state.EventType != "model.call.completed" {
		t.Fatalf("stream metadata = sequence %d event_type %q, want 7 model.call.completed", state.Sequence, state.EventType)
	}
	if state.Terminal {
		t.Fatal("Terminal = true, want false for running live state")
	}
	if state.ModelCall == nil {
		t.Fatal("ModelCall is nil, want model-call overlay")
	}
	if state.ModelCall.ModelCall != 3 || state.ModelCall.StepID != "step-model-call" || state.ModelCall.CostMicrosUSD != 4200 {
		t.Fatalf("ModelCall = %+v, want model_call=3 step=step-model-call cost=4200", state.ModelCall)
	}
	if state.Run.ModelCallCount != 3 {
		t.Fatalf("Run.ModelCallCount = %d, want live projection 3", state.Run.ModelCallCount)
	}
}

func TestTaskRunStreamProjector_LiveStateUsesParentTaskProjectLinkage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	now := time.Now().UTC()
	if _, err := store.CreateTask(ctx, types.Task{
		ID:           "task-project",
		Title:        "task-project",
		Prompt:       "projector test",
		Status:       "running",
		ProjectID:    "proj_1",
		WorkItemID:   "work_1",
		AssignmentID: "asgn_1",
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if _, err := store.CreateRun(ctx, types.TaskRun{
		ID:        "run-project",
		TaskID:    "task-project",
		Number:    1,
		Status:    "running",
		StartedAt: now,
	}); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	projector := newTaskRunStreamProjector(store)

	state, err := projector.projectEvent(ctx, "task-project", "run-project", types.TaskRunEvent{
		Sequence:  8,
		EventType: "run.updated",
	})
	if err != nil {
		t.Fatalf("projectEvent() error = %v", err)
	}
	if state.Run.ProjectID != "proj_1" || state.Run.WorkItemID != "work_1" || state.Run.AssignmentID != "asgn_1" {
		t.Fatalf("stream run linkage = project %q work %q assignment %q, want proj_1/work_1/asgn_1", state.Run.ProjectID, state.Run.WorkItemID, state.Run.AssignmentID)
	}
}

func TestTaskRunStreamProjector_DoesNotTopUpSnapshotApprovals(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	seedTaskRunStreamProjectorRun(t, ctx, store, "task-approval", "run-approval", "awaiting_approval")
	_, err := store.CreateApproval(ctx, types.TaskApproval{
		ID:          "approval-1",
		TaskID:      "task-approval",
		RunID:       "run-approval",
		StepID:      "step-approval",
		Kind:        "shell_exec",
		Status:      "pending",
		Reason:      "shell command",
		RequestedBy: "agent_loop",
		CreatedAt:   time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateApproval() error = %v", err)
	}

	snapshot := map[string]any{
		"run": map[string]any{
			"id":      "run-approval",
			"task_id": "task-approval",
			"status":  "awaiting_approval",
		},
	}
	projector := newTaskRunStreamProjector(store)
	state, err := projector.projectEvent(ctx, "task-approval", "run-approval", types.TaskRunEvent{
		Sequence:  11,
		EventType: "snapshot",
		Data:      map[string]any{"snapshot": snapshot},
	})
	if err != nil {
		t.Fatalf("projectEvent() error = %v", err)
	}
	if len(state.Approvals) != 0 {
		t.Fatalf("Approvals = %+v, want none because persisted snapshot omitted them", state.Approvals)
	}
	if _, ok := snapshot["approvals"]; ok {
		t.Fatal("snapshot was mutated with approvals")
	}
}

func TestTaskRunStreamProjector_SnapshotEventDataUsesCurrentStreamShape(t *testing.T) {
	t.Parallel()

	projector := newTaskRunStreamProjector(nil)
	state := TaskRunStreamEventData{
		Run: renderTaskRun(types.TaskRun{
			ID:     "run-shape",
			TaskID: "task-shape",
			Status: "running",
		}),
		Approvals: []TaskApprovalItem{{
			ID:     "approval-shape",
			TaskID: "task-shape",
			RunID:  "run-shape",
			Status: "pending",
		}},
	}
	snapshot, err := projector.snapshotEventData(state)
	if err != nil {
		t.Fatalf("snapshotEventData() error = %v", err)
	}
	approvals, ok := snapshot["approvals"].([]any)
	if !ok {
		t.Fatalf("snapshot approvals = %#v, want JSON array", snapshot["approvals"])
	}
	if len(approvals) != 1 {
		t.Fatalf("snapshot approvals = %d, want 1", len(approvals))
	}
}

func seedTaskRunStreamProjectorRun(t *testing.T, ctx context.Context, store taskstate.Store, taskID, runID, status string) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := store.CreateTask(ctx, types.Task{
		ID:        taskID,
		Title:     taskID,
		Prompt:    "projector test",
		Status:    status,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if _, err := store.CreateRun(ctx, types.TaskRun{
		ID:        runID,
		TaskID:    taskID,
		Number:    1,
		Status:    status,
		StartedAt: now,
		RequestID: "req-" + runID,
		TraceID:   "trace-" + runID,
	}); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
}
