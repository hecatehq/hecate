package api

import (
	"testing"

	"github.com/hecatehq/hecate/internal/chat"
)

func TestChatContextPacketsUseVisibleTranscriptCount(t *testing.T) {
	session := chat.Session{
		ID:        "chat_1",
		Workspace: "/tmp/hecate",
		Messages: []chat.Message{
			{ID: "sys", Role: "system", Content: "hidden"},
			{ID: "u1", Role: "user", Content: "first"},
			{ID: "a1", Role: "assistant", Content: "done", Status: "completed"},
			{ID: "a2", Role: "assistant", Content: "still running", Status: "running"},
			{ID: "empty", Role: "user", Content: "   "},
		},
	}

	packets := []struct {
		name   string
		packet chat.ContextPacket
	}{
		{
			name:   "direct model",
			packet: directModelContextPacket(session, "ollama", "llama3.1:8b", "system"),
		},
		{
			name:   "hecate task",
			packet: hecateTaskContextPacket(session, "ollama", "llama3.1:8b", "system", false),
		},
		{
			name:   "external agent",
			packet: externalAgentContextPacket(session, "Cursor Agent"),
		},
	}

	for _, tc := range packets {
		t.Run(tc.name, func(t *testing.T) {
			if tc.packet.MessageCount != 3 {
				t.Fatalf("MessageCount = %d, want 3 visible terminal messages including current user turn", tc.packet.MessageCount)
			}
		})
	}
}
