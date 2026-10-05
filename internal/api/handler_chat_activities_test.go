package api

import (
	"encoding/json"
	"testing"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/chat"
)

func TestAgentChatActivityFromAdapterPreservesArtifactPreview(t *testing.T) {
	activity := agentChatActivityFromAdapter(agentadapters.Activity{
		ID:              "tool:call_1",
		Type:            "tool_call",
		Status:          "completed",
		Kind:            "execute",
		Title:           "call_1",
		Detail:          "execute · output: summarized",
		ArtifactPreview: "  full output\nline two\n",
	})

	if activity.ArtifactPreview != "  full output\nline two" {
		t.Fatalf("artifact preview = %q", activity.ArtifactPreview)
	}
	if activity.StepID != "" {
		t.Fatalf("adapter activity step_id = %q, want absent native provenance", activity.StepID)
	}
}

func TestChatActivity_NativeStepProvenance(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"completed", "failed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			items := []TaskActivityItem{
				{ID: "step:step_browser", Type: "tool_call", Kind: "tool", Title: "Browser flow", Status: status, StepID: " step_browser "},
				{ID: "artifact:art_browser", Type: "artifact", Kind: "browser_flow_evidence", Title: "Browser flow evidence", Status: "ready", StepID: "step_browser", ArtifactID: "art_browser"},
			}
			activities := agentChatActivitiesFromTaskActivity(items)
			if len(activities) != 2 || activities[0].StepID != "step_browser" || activities[1].StepID != "step_browser" {
				t.Fatalf("mapped native activities = %+v, want shared step provenance", activities)
			}
			rendered := renderAgentChatActivities(activities)
			if len(rendered) != 2 || rendered[0].StepID != "step_browser" || rendered[1].StepID != "step_browser" {
				t.Fatalf("rendered native activities = %+v, want shared step provenance", rendered)
			}
			if rendered[0].Status != status || rendered[1].Status != "ready" {
				t.Fatalf("rendered statuses = %q/%q, want %q/ready", rendered[0].Status, rendered[1].Status, status)
			}
			payload, err := json.Marshal(rendered)
			if err != nil {
				t.Fatalf("marshal activities: %v", err)
			}
			var wire []map[string]any
			if err := json.Unmarshal(payload, &wire); err != nil {
				t.Fatalf("decode activities: %v", err)
			}
			if wire[0]["step_id"] != "step_browser" || wire[1]["step_id"] != "step_browser" {
				t.Fatalf("activity step_id wire fields = %+v", wire)
			}
		})
	}
}

func TestChatActivity_DoesNotInferStepProvenance(t *testing.T) {
	t.Parallel()
	activities := []chat.Activity{
		agentChatActivityFromTaskActivity(TaskActivityItem{
			ID: "step:step_legacy", Type: "tool_call", Title: "Browser flow", Status: "completed",
		}),
		agentChatActivityFromAdapter(agentadapters.Activity{
			ID: "step:step_external", Type: "tool_call", Title: "Browser flow", Status: "completed",
		}),
	}
	for _, activity := range activities {
		if activity.StepID != "" {
			t.Fatalf("activity %+v fabricated native step provenance", activity)
		}
	}
	payload, err := json.Marshal(renderAgentChatActivities(activities))
	if err != nil {
		t.Fatalf("marshal activities: %v", err)
	}
	var wire []map[string]any
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatalf("decode activities: %v", err)
	}
	for _, activity := range wire {
		if _, present := activity["step_id"]; present {
			t.Fatalf("unproven activity contains step_id: %+v", activity)
		}
	}
}

func TestMergeChatActivity_PreservesStepProvenance(t *testing.T) {
	t.Parallel()
	items := []chat.Activity{{ID: "task:step:step_browser", Type: "tool_call", Title: "Browser flow", Status: "running"}}
	items = mergeChatActivity(items, chat.Activity{
		ID: items[0].ID, Type: "tool_call", Status: "failed", StepID: "step_browser",
	})
	items = mergeChatActivity(items, chat.Activity{
		ID: items[0].ID, Type: "tool_call", Detail: "Partial evidence retained",
	})
	if len(items) != 1 || items[0].StepID != "step_browser" || items[0].Status != "failed" {
		t.Fatalf("merged activities = %+v, want retained failed step provenance", items)
	}
}
