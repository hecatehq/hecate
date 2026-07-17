package api

import (
	"encoding/json"
	"testing"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/pkg/types"
)

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
