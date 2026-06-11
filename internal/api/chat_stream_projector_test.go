package api

import (
	"testing"

	"github.com/hecatehq/hecate/internal/chat"
)

func TestAgentChatStreamProjectorInitialFrame(t *testing.T) {
	t.Parallel()

	session := chat.Session{ID: "chat_1", AgentID: chat.DefaultAgentID, Title: "Chat", Status: "idle"}
	projector := newAgentChatStreamProjector(session, agentChatSnapshotConfig{MaxTurnsPerSession: 7})

	frame := projector.initialFrame(session)
	if frame.Event != "snapshot" || frame.Done {
		t.Fatalf("frame = %+v, want snapshot non-terminal", frame)
	}
	payload, ok := frame.Data.(ChatSessionResponse)
	if !ok {
		t.Fatalf("frame data = %T, want ChatSessionResponse", frame.Data)
	}
	if payload.Data.ID != "chat_1" || payload.Data.MaxTurnsPerSession != 7 {
		t.Fatalf("payload = %+v, want rendered session with limits", payload.Data)
	}
}

func TestAgentChatStreamProjectorEmitsDoneAfterObservedRunTerminates(t *testing.T) {
	t.Parallel()

	session := chat.Session{ID: "chat_1", AgentID: chat.DefaultAgentID, Status: "idle"}
	projector := newAgentChatStreamProjector(session, agentChatSnapshotConfig{})

	running := ChatSessionResponse{Object: "chat_session", Data: ChatSessionItem{ID: "chat_1", Status: "running"}}
	frames := projector.project(AgentChatLiveEvent{
		Type:          AgentChatLiveEventSessionUpdate,
		SessionUpdate: &running,
	})
	if len(frames) != 1 || frames[0].Event != "snapshot" || frames[0].Done {
		t.Fatalf("running frames = %+v, want one snapshot", frames)
	}

	completed := ChatSessionResponse{Object: "chat_session", Data: ChatSessionItem{ID: "chat_1", Status: "completed"}}
	frames = projector.project(AgentChatLiveEvent{
		Type:          AgentChatLiveEventSessionUpdate,
		SessionUpdate: &completed,
	})
	if len(frames) != 2 || frames[0].Event != "snapshot" || frames[1].Event != "done" || !frames[1].Done {
		t.Fatalf("terminal frames = %+v, want snapshot then done", frames)
	}
}

func TestAgentChatStreamProjectorDoesNotEmitDoneForUnobservedTerminalSnapshot(t *testing.T) {
	t.Parallel()

	session := chat.Session{ID: "chat_1", AgentID: chat.DefaultAgentID, Status: "idle"}
	projector := newAgentChatStreamProjector(session, agentChatSnapshotConfig{})
	completed := ChatSessionResponse{Object: "chat_session", Data: ChatSessionItem{ID: "chat_1", Status: "completed"}}

	frames := projector.project(AgentChatLiveEvent{
		Type:          AgentChatLiveEventSessionUpdate,
		SessionUpdate: &completed,
	})
	if len(frames) != 1 || frames[0].Event != "snapshot" || frames[0].Done {
		t.Fatalf("frames = %+v, want one snapshot without done", frames)
	}
}

func TestAgentChatStreamProjectorPassesApprovalEvents(t *testing.T) {
	t.Parallel()

	projector := newAgentChatStreamProjector(chat.Session{ID: "chat_1"}, agentChatSnapshotConfig{})
	requested := ChatApprovalRequestedEvent{ApprovalID: "approval_1", SessionID: "chat_1"}
	resolved := ChatApprovalResolvedEvent{ApprovalID: "approval_1", SessionID: "chat_1", Status: "approved"}

	frames := projector.project(AgentChatLiveEvent{
		Type:              AgentChatLiveEventApprovalRequested,
		ApprovalRequested: &requested,
	})
	if len(frames) != 1 || frames[0].Event != string(AgentChatLiveEventApprovalRequested) || frames[0].Data.(ChatApprovalRequestedEvent).ApprovalID != "approval_1" {
		t.Fatalf("requested frames = %+v, want approval requested payload", frames)
	}

	frames = projector.project(AgentChatLiveEvent{
		Type:             AgentChatLiveEventApprovalResolved,
		ApprovalResolved: &resolved,
	})
	if len(frames) != 1 || frames[0].Event != string(AgentChatLiveEventApprovalResolved) || frames[0].Data.(ChatApprovalResolvedEvent).Status != "approved" {
		t.Fatalf("resolved frames = %+v, want approval resolved payload", frames)
	}
}
