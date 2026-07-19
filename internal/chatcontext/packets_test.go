package chatcontext

import (
	"encoding/json"
	"testing"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestFromSessionMessage(t *testing.T) {
	t.Parallel()

	packet := chat.ContextPacket{Version: "chat.context.v1", MessageCount: 2}
	got, ok := FromSessionMessage(chat.Session{Messages: []chat.Message{
		{ID: "msg_other", Context: chat.ContextPacket{Version: "chat.context.v1"}},
		{ID: " msg_1 ", Context: packet},
	}}, "msg_1")
	if !ok || got.MessageCount != 2 {
		t.Fatalf("FromSessionMessage() = %+v ok=%v, want packet", got, ok)
	}
}

func TestFromSessionRun(t *testing.T) {
	t.Parallel()

	packet := chat.ContextPacket{Version: "chat.context.v1", MessageCount: 3}
	got, ok := FromSessionRun(chat.Session{Messages: []chat.Message{
		{TaskID: "task_1", RunID: "run_other", Context: chat.ContextPacket{Version: "chat.context.v1"}},
		{TaskID: " task_1 ", RunID: " run_1 ", Context: packet},
	}}, "task_1", "run_1")
	if !ok || got.MessageCount != 3 {
		t.Fatalf("FromSessionRun() = %+v ok=%v, want packet", got, ok)
	}
}

func TestFromTaskRun(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(chat.ContextPacket{Version: "chat.context.v1", MessageCount: 4})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, ok, err := FromTaskRun(types.TaskRun{ContextPacket: raw})
	if err != nil || !ok || got.MessageCount != 4 {
		t.Fatalf("FromTaskRun() = %+v ok=%v err=%v, want decoded packet", got, ok, err)
	}
}

func TestFromProjectAssignmentPayload(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(chat.ContextPacket{Version: "chat.context.v1", MessageCount: 5})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, ok, err := FromProjectAssignmentPayload(raw)
	if err != nil || !ok || got.MessageCount != 5 {
		t.Fatalf("FromProjectAssignmentPayload() = %+v ok=%v err=%v, want decoded packet", got, ok, err)
	}
}

func TestNormalizeMergesRefsAndDefaultSections(t *testing.T) {
	t.Parallel()

	originalRefs := &chat.ContextRefs{SessionID: "session_original"}
	packet := chat.ContextPacket{
		Version: "chat.context.v1",
		Refs:    originalRefs,
		Items: []chat.ContextItem{
			{Kind: "system_prompt", Title: "System prompt"},
			{Kind: "workspace_doc", Title: "Workspace doc"},
			{Kind: "unknown_runtime", Title: "Runtime"},
		},
	}

	got := Normalize(packet, chat.ContextRefs{
		SessionID: "session_new",
		TurnID:    "turn_1",
		MessageID: "msg_1",
		TaskID:    "task_1",
		RunID:     "run_1",
		ProjectID: "proj_1",
	})
	if got.Refs == nil {
		t.Fatalf("Normalize refs are nil, want merged refs")
	}
	if got.Refs.SessionID != "session_original" || got.Refs.TurnID != "turn_1" || got.Refs.MessageID != "msg_1" || got.Refs.TaskID != "task_1" || got.Refs.RunID != "run_1" || got.Refs.ProjectID != "proj_1" {
		t.Fatalf("Normalize refs = %+v, want original session plus supplied refs", *got.Refs)
	}
	if got.Items[0].Section != "instructions" || got.Items[1].Section != "sources" || got.Items[2].Section != "runtime" {
		t.Fatalf("Normalize sections = %q/%q/%q, want instructions/sources/runtime", got.Items[0].Section, got.Items[1].Section, got.Items[2].Section)
	}
	if got.Refs == originalRefs {
		t.Fatalf("Normalize reused refs pointer, want cloned packet")
	}
	if packet.Items[0].Section != "" {
		t.Fatalf("Normalize mutated original packet item section = %q", packet.Items[0].Section)
	}
}

func TestRefsMergeCanonicalContextRefs(t *testing.T) {
	t.Parallel()

	refs := MergeRefs(
		ChatMessageRefs(" session_1 ", " turn_1 ", " msg_1 ", " proj_1 "),
		TaskRunRefs(" task_1 ", " run_1 ", "ignored_project"),
		ProjectAssignmentRefs("proj_1", " work_1 ", " asgn_1 ", " role_1 "),
	)
	if refs.SessionID != "session_1" || refs.TurnID != "turn_1" || refs.MessageID != "msg_1" || refs.TaskID != "task_1" || refs.RunID != "run_1" {
		t.Fatalf("runtime refs = %+v, want trimmed chat/task refs", refs)
	}
	if refs.ProjectID != "proj_1" || refs.WorkItemID != "work_1" || refs.AssignmentID != "asgn_1" || refs.RoleID != "role_1" {
		t.Fatalf("project refs = %+v, want project assignment refs", refs)
	}
}

func TestMarshalEmptyAndNonEmptyPackets(t *testing.T) {
	t.Parallel()

	if got := Marshal(chat.ContextPacket{}); got != nil {
		t.Fatalf("Marshal(empty) = %s, want nil", string(got))
	}
	raw := Marshal(chat.ContextPacket{Version: "chat.context.v1", MessageCount: 1})
	if len(raw) == 0 {
		t.Fatalf("Marshal(non-empty) returned empty payload")
	}
	var got chat.ContextPacket
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode Marshal output: %v", err)
	}
	if got.Version != "chat.context.v1" || got.MessageCount != 1 {
		t.Fatalf("Marshal decoded packet = %+v, want source fields", got)
	}
}
