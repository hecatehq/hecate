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
