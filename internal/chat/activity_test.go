package chat

import (
	"encoding/json"
	"testing"
)

func runStoreActivityStepProvenanceRoundTrip(t *testing.T, store Store) {
	t.Helper()
	ctx := t.Context()
	const sessionID = "chat_browser_provenance"
	const messageID = "msg_browser"
	if _, err := store.Create(ctx, Session{ID: sessionID, AgentID: DefaultAgentID}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.AppendMessage(ctx, sessionID, Message{
		ID: messageID, Role: "assistant", Status: "failed", TaskID: "task_browser", RunID: "run_browser",
		Activities: []Activity{
			{ID: "task:step:step_browser", Type: "tool_call", Kind: "tool", Title: "Browser flow", Status: "failed", StepID: "step_browser"},
			{ID: "task:artifact:art_browser", Type: "artifact", Kind: "browser_flow_evidence", Title: "Browser flow evidence", Status: "ready", StepID: "step_browser", ArtifactID: "art_browser"},
			{ID: "task:step:step_legacy", Type: "tool_call", Title: "Legacy activity", Status: "completed"},
		},
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	assertProvenance := func(session Session) {
		t.Helper()
		if len(session.Messages) != 1 || len(session.Messages[0].Activities) != 3 {
			t.Fatalf("stored message activities = %+v", session.Messages)
		}
		activities := session.Messages[0].Activities
		if activities[0].StepID != "step_browser" || activities[1].StepID != "step_browser" || activities[2].StepID != "" {
			t.Fatalf("stored step provenance = %+v", activities)
		}
		if activities[0].Status != "failed" || activities[1].Status != "ready" {
			t.Fatalf("stored step/artifact statuses = %q/%q, want failed/ready", activities[0].Status, activities[1].Status)
		}
	}
	got, found, err := store.Get(ctx, sessionID)
	if err != nil || !found {
		t.Fatalf("Get: found=%v err=%v", found, err)
	}
	assertProvenance(got)
	got.Messages[0].Activities[0].StepID = "mutated_read"
	updated, err := store.UpdateMessage(ctx, sessionID, messageID, func(message *Message) {
		message.Content = "Browser flow stopped after a completed action."
	})
	if err != nil {
		t.Fatalf("UpdateMessage: %v", err)
	}
	assertProvenance(updated)
	got, found, err = store.Get(ctx, sessionID)
	if err != nil || !found {
		t.Fatalf("Get after update: found=%v err=%v", found, err)
	}
	assertProvenance(got)
}

func TestActivity_JSONStepProvenance(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{name: "native", body: `{"id":"task:step:step_browser","type":"tool_call","title":"Browser flow","step_id":"step_browser"}`, want: "step_browser"},
		{name: "legacy", body: `{"id":"task:step:step_legacy","type":"tool_call","title":"Browser flow"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var activity Activity
			if err := json.Unmarshal([]byte(test.body), &activity); err != nil {
				t.Fatalf("decode activity: %v", err)
			}
			if activity.StepID != test.want {
				t.Fatalf("step_id = %q, want %q", activity.StepID, test.want)
			}
			payload, err := json.Marshal(activity)
			if err != nil {
				t.Fatalf("marshal activity: %v", err)
			}
			var wire map[string]any
			if err := json.Unmarshal(payload, &wire); err != nil {
				t.Fatalf("decode wire activity: %v", err)
			}
			value, present := wire["step_id"]
			if test.want == "" && present || test.want != "" && value != test.want {
				t.Fatalf("wire step_id = %v present=%v, want %q", value, present, test.want)
			}
		})
	}
}
