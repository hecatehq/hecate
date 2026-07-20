package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/pkg/types"
)

func TestRenderTaskApprovalIncludesIndependentActionSummary(t *testing.T) {
	t.Parallel()

	approval := types.TaskApproval{
		ID:                      "approval-1",
		TaskID:                  "task-1",
		RunID:                   "run-1",
		Kind:                    "agent_loop_tool_call",
		Status:                  "pending",
		ActionSummary:           []string{"git branch -vv", "file_write write path=out.txt content_bytes=2"},
		ActionSummaryIncomplete: true,
	}

	item := renderTaskApproval(approval)
	if !reflect.DeepEqual(item.ActionSummary, approval.ActionSummary) || !item.ActionSummaryIncomplete {
		t.Fatalf("renderTaskApproval() action summary = %#v incomplete=%v", item.ActionSummary, item.ActionSummaryIncomplete)
	}
	item.ActionSummary[0] = "mutated API response"
	if approval.ActionSummary[0] != "git branch -vv" {
		t.Fatalf("renderTaskApproval() aliased source summary: %#v", approval.ActionSummary)
	}
	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("json.Marshal(TaskApprovalItem): %v", err)
	}
	if text := string(raw); !strings.Contains(text, `"action_summary"`) || !strings.Contains(text, `"action_summary_incomplete":true`) {
		t.Fatalf("TaskApprovalItem JSON = %s, want action summary fields", text)
	}
}
