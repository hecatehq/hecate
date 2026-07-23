package api

import "github.com/hecatehq/hecate/internal/chat"

type agentChatStreamFrame struct {
	Event string
	Data  any
	Done  bool
}

type agentChatStreamProjector struct {
	snapshot     agentChatSnapshotConfig
	observedTurn bool
}

func newAgentChatStreamProjector(session chat.Session, snapshot agentChatSnapshotConfig) *agentChatStreamProjector {
	return &agentChatStreamProjector{
		snapshot:     snapshot,
		observedTurn: session.Status == "running",
	}
}

func (p *agentChatStreamProjector) observeTurn() {
	p.observedTurn = true
}

func (p *agentChatStreamProjector) initialFrame(session chat.Session) agentChatStreamFrame {
	return agentChatStreamFrame{
		Event: "snapshot",
		Data: ChatSessionResponse{
			Object: "chat_session",
			Data:   renderChatSession(session, p.snapshot),
		},
	}
}

func (p *agentChatStreamProjector) project(payload AgentChatLiveEvent) []agentChatStreamFrame {
	switch payload.Type {
	case AgentChatLiveEventSessionUpdate:
		if payload.SessionUpdate == nil {
			return nil
		}
		frame := agentChatStreamFrame{Event: "snapshot", Data: *payload.SessionUpdate}
		frames := []agentChatStreamFrame{frame}
		if payload.SessionUpdate.Data.Status == "running" {
			p.observedTurn = true
		}
		if p.observedTurn && isTerminalAgentChatStatus(payload.SessionUpdate.Data.Status) {
			frames = append(frames, agentChatStreamFrame{Event: "done", Data: *payload.SessionUpdate, Done: true})
		}
		return frames
	case AgentChatLiveEventApprovalRequested:
		if payload.ApprovalRequested == nil {
			return nil
		}
		return []agentChatStreamFrame{{Event: string(AgentChatLiveEventApprovalRequested), Data: *payload.ApprovalRequested}}
	case AgentChatLiveEventApprovalResolved:
		if payload.ApprovalResolved == nil {
			return nil
		}
		return []agentChatStreamFrame{{Event: string(AgentChatLiveEventApprovalResolved), Data: *payload.ApprovalResolved}}
	default:
		return nil
	}
}
