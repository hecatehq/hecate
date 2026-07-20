package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestRenderTaskRun_ExposesScheduleProvenance(t *testing.T) {
	t.Parallel()

	scheduledFor := time.Date(2026, time.July, 20, 8, 30, 0, 0, time.FixedZone("local", 2*60*60))
	item := renderTaskRun(types.TaskRun{
		ID:                   "run_1",
		TaskID:               "task_1",
		ScheduleID:           "schedule_1",
		ScheduleOccurrenceID: "occurrence_1",
		ScheduledFor:         scheduledFor,
	})

	if item.ScheduleID != "schedule_1" || item.ScheduleOccurrenceID != "occurrence_1" {
		t.Fatalf("schedule provenance = %q/%q, want schedule_1/occurrence_1", item.ScheduleID, item.ScheduleOccurrenceID)
	}
	if item.ScheduledFor != "2026-07-20T06:30:00Z" {
		t.Fatalf("scheduled_for = %q, want UTC timestamp", item.ScheduledFor)
	}
}

func TestRenderTaskRunUsesDirectRunProjectLinkage(t *testing.T) {
	t.Parallel()

	item := renderTaskRun(types.TaskRun{
		ID:           "run_1",
		TaskID:       "task_1",
		ProjectID:    "proj_run",
		WorkItemID:   "work_run",
		AssignmentID: "asgn_run",
		Status:       "running",
	}, types.Task{
		ID:           "task_1",
		ProjectID:    "proj_task",
		WorkItemID:   "work_task",
		AssignmentID: "asgn_task",
	})
	if item.ProjectID != "proj_run" || item.WorkItemID != "work_run" || item.AssignmentID != "asgn_run" {
		t.Fatalf("run linkage = project %q work %q assignment %q, want direct run linkage", item.ProjectID, item.WorkItemID, item.AssignmentID)
	}
}

func TestRenderTaskRun_ExposesAuthoritativeModelCallCount(t *testing.T) {
	t.Parallel()

	item := renderTaskRun(types.TaskRun{
		ID:             "run_1",
		TaskID:         "task_1",
		Status:         "completed",
		ModelCallCount: 2,
	})
	if item.ModelCallCount != 2 {
		t.Fatalf("model_call_count = %d, want 2", item.ModelCallCount)
	}

	zeroPayload, err := json.Marshal(renderTaskRun(types.TaskRun{
		ID:     "run_0",
		TaskID: "task_1",
		Status: "failed",
	}))
	if err != nil {
		t.Fatalf("marshal zero-count run: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(zeroPayload, &fields); err != nil {
		t.Fatalf("decode zero-count run: %v", err)
	}
	if got, ok := fields["model_call_count"]; !ok || got != float64(0) {
		t.Fatalf("model_call_count field = %#v, present=%v; want explicit 0", got, ok)
	}
}

func TestBuildTaskItem_UsesLatestRunCounts(t *testing.T) {
	t.Parallel()

	store := taskstate.NewMemoryStore()
	task := types.Task{ID: "task_1", LatestRunID: "run_2", Status: "completed"}
	if _, err := store.CreateTask(t.Context(), task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	for _, run := range []types.TaskRun{
		{ID: "run_1", TaskID: task.ID, Number: 1, StepCount: 7, ArtifactCount: 5},
		{ID: "run_2", TaskID: task.ID, Number: 2, StepCount: 2, ArtifactCount: 1},
	} {
		if _, err := store.CreateRun(t.Context(), run); err != nil {
			t.Fatalf("CreateRun(%s): %v", run.ID, err)
		}
	}

	item := buildTaskItem(t.Context(), store, task)
	if item.LatestRunStepCount != 2 || item.LatestRunArtifactCount != 1 {
		t.Fatalf("latest run counts = steps:%d artifacts:%d, want 2/1", item.LatestRunStepCount, item.LatestRunArtifactCount)
	}
	payload, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal task item: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatalf("decode task item: %v", err)
	}
	if _, ok := fields["step_count"]; ok {
		t.Fatal("legacy task-level step_count is present")
	}
	if _, ok := fields["artifact_count"]; ok {
		t.Fatal("legacy task-level artifact_count is present")
	}
}

func TestRenderTaskRunUsesParentTaskProjectLinkage(t *testing.T) {
	t.Parallel()

	item := renderTaskRun(types.TaskRun{
		ID:     "run_1",
		TaskID: "task_1",
		Status: "running",
	}, types.Task{
		ID:           "task_1",
		ProjectID:    "proj_1",
		WorkItemID:   "work_1",
		AssignmentID: "asgn_1",
	})
	if item.ProjectID != "proj_1" || item.WorkItemID != "work_1" || item.AssignmentID != "asgn_1" {
		t.Fatalf("run linkage = project %q work %q assignment %q, want proj_1/work_1/asgn_1", item.ProjectID, item.WorkItemID, item.AssignmentID)
	}
}

func TestRenderTaskRunUsesContextPacketProjectLinkageFallback(t *testing.T) {
	t.Parallel()

	packet, err := json.Marshal(chat.ContextPacket{
		Version: "chat.context.v1",
		Refs: &chat.ContextRefs{
			ProjectID:    "proj_context",
			WorkItemID:   "work_context",
			AssignmentID: "asgn_context",
		},
	})
	if err != nil {
		t.Fatalf("marshal context packet: %v", err)
	}

	item := renderTaskRun(types.TaskRun{
		ID:            "run_1",
		TaskID:        "task_1",
		Status:        "running",
		ContextPacket: packet,
	})
	if item.ProjectID != "proj_context" || item.WorkItemID != "work_context" || item.AssignmentID != "asgn_context" {
		t.Fatalf("run linkage = project %q work %q assignment %q, want context fallback linkage", item.ProjectID, item.WorkItemID, item.AssignmentID)
	}
}

func TestRenderTaskRun_ExposesCanonicalChatTurnSource(t *testing.T) {
	t.Parallel()

	packet, err := json.Marshal(chat.ContextPacket{
		Version: "chat.context.v1",
		Refs: &chat.ContextRefs{
			SessionID: "chat_1",
			TurnID:    "turn_1",
			MessageID: "message_1",
			TaskID:    "task_1",
			RunID:     "run_2",
		},
	})
	if err != nil {
		t.Fatalf("marshal context packet: %v", err)
	}

	item := renderTaskRun(types.TaskRun{
		ID:            "run_2",
		TaskID:        "task_1",
		Status:        "completed",
		ContextPacket: packet,
	})
	if item.SourceRef == nil {
		t.Fatal("source_ref = nil, want canonical chat turn source")
	}
	if got := *item.SourceRef; got.Kind != "chat_turn" || got.ChatSessionID != "chat_1" || got.TurnID != "turn_1" || got.MessageID != "message_1" {
		t.Fatalf("source_ref = %+v, want exact chat/turn/message", got)
	}
}

func TestRenderTaskRun_OmitsMismatchedChatTurnSource(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		refs chat.ContextRefs
	}{
		{
			name: "different task",
			refs: chat.ContextRefs{SessionID: "chat_1", TurnID: "turn_1", MessageID: "message_1", TaskID: "task_other", RunID: "run_1"},
		},
		{
			name: "different run",
			refs: chat.ContextRefs{SessionID: "chat_1", TurnID: "turn_1", MessageID: "message_1", TaskID: "task_1", RunID: "run_other"},
		},
		{
			name: "incomplete chat identity",
			refs: chat.ContextRefs{SessionID: "chat_1", TurnID: "turn_1", TaskID: "task_1", RunID: "run_1"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			packet, err := json.Marshal(chat.ContextPacket{Version: "chat.context.v1", Refs: &tc.refs})
			if err != nil {
				t.Fatalf("marshal context packet: %v", err)
			}
			item := renderTaskRun(types.TaskRun{ID: "run_1", TaskID: "task_1", ContextPacket: packet})
			if item.SourceRef != nil {
				t.Fatalf("source_ref = %+v, want omitted", item.SourceRef)
			}
		})
	}
}

func TestRenderTaskItem_ExposesAgentPresetRuntimePolicySnapshot(t *testing.T) {
	t.Parallel()
	toolsEnabled := false
	browserAllowed := true

	item := renderTaskItem(types.Task{
		ID:                               "task_1",
		AgentPresetID:                    "review_qa",
		AgentPresetToolsEnabled:          &toolsEnabled,
		AgentPresetBrowserAllowed:        &browserAllowed,
		AgentPresetBrowserAllowedOrigins: []string{"https://app.example.test"},
		SandboxReadOnly:                  true,
		SandboxNetwork:                   false,
		Status:                           "queued",
	})
	if item.AgentPresetID != "review_qa" || item.AgentPresetToolsEnabled == nil || *item.AgentPresetToolsEnabled || item.AgentPresetBrowserAllowed == nil || !*item.AgentPresetBrowserAllowed || len(item.AgentPresetBrowserAllowedOrigins) != 1 || item.AgentPresetBrowserAllowedOrigins[0] != "https://app.example.test" || !item.SandboxReadOnly || item.SandboxNetwork {
		t.Fatalf("rendered policy snapshot = %+v, want independent browser snapshot and review posture", item)
	}
}

func TestTaskWorkflowModeFlowsThroughCreateAndRenderContracts(t *testing.T) {
	t.Parallel()

	command := taskCreateCommandFromRequest(CreateTaskRequest{
		Prompt:        "Inspect the workspace",
		ExecutionKind: "agent_loop",
		WorkflowMode:  "qa",
	})
	if command.WorkflowMode != "qa" {
		t.Fatalf("create command workflow_mode = %q, want qa", command.WorkflowMode)
	}
	taskItem := renderTaskItem(types.Task{
		ID:              "task_qa",
		WorkflowMode:    types.WorkflowModeQA,
		WorkflowVersion: "v0",
		Status:          "queued",
	})
	if taskItem.WorkflowMode != "qa" || taskItem.WorkflowVersion != "v0" {
		t.Fatalf("task item workflow = %q/%q, want qa/v0", taskItem.WorkflowMode, taskItem.WorkflowVersion)
	}
	runItem := renderTaskRun(types.TaskRun{
		ID:              "run_qa",
		TaskID:          "task_qa",
		WorkflowMode:    types.WorkflowModeQA,
		WorkflowVersion: "v0",
		Status:          "queued",
	})
	if runItem.WorkflowMode != "qa" || runItem.WorkflowVersion != "v0" {
		t.Fatalf("run item workflow = %q/%q, want qa/v0", runItem.WorkflowMode, runItem.WorkflowVersion)
	}
	payload, err := json.Marshal(runItem)
	if err != nil {
		t.Fatalf("marshal run item: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatalf("decode run item: %v", err)
	}
	if fields["workflow_mode"] != "qa" || fields["workflow_version"] != "v0" {
		t.Fatalf("run payload fields = %#v, want QA workflow snapshot", fields)
	}
}
