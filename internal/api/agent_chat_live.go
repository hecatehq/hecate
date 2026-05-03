package api

import (
	"context"
	"sync"

	"github.com/hecate/agent-runtime/internal/agentchat"
)

type agentChatLive struct {
	mu          sync.Mutex
	subscribers map[string]map[chan AgentChatSessionResponse]struct{}
	running     map[string]context.CancelFunc
}

func newAgentChatLive() *agentChatLive {
	return &agentChatLive{
		subscribers: make(map[string]map[chan AgentChatSessionResponse]struct{}),
		running:     make(map[string]context.CancelFunc),
	}
}

func (l *agentChatLive) subscribe(sessionID string) (<-chan AgentChatSessionResponse, func()) {
	ch := make(chan AgentChatSessionResponse, 16)
	l.mu.Lock()
	if l.subscribers[sessionID] == nil {
		l.subscribers[sessionID] = make(map[chan AgentChatSessionResponse]struct{})
	}
	l.subscribers[sessionID][ch] = struct{}{}
	l.mu.Unlock()

	return ch, func() {
		l.mu.Lock()
		if subs := l.subscribers[sessionID]; subs != nil {
			delete(subs, ch)
			if len(subs) == 0 {
				delete(l.subscribers, sessionID)
			}
		}
		close(ch)
		l.mu.Unlock()
	}
}

func (l *agentChatLive) publish(session agentchat.Session) {
	payload := AgentChatSessionResponse{
		Object: "agent_chat_session",
		Data:   renderAgentChatSession(session),
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	for ch := range l.subscribers[session.ID] {
		select {
		case ch <- payload:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- payload:
			default:
			}
		}
	}
}

func (l *agentChatLive) registerRun(sessionID string, cancel context.CancelFunc) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, exists := l.running[sessionID]; exists {
		return false
	}
	l.running[sessionID] = cancel
	return true
}

func (l *agentChatLive) clearRun(sessionID string) {
	l.mu.Lock()
	delete(l.running, sessionID)
	l.mu.Unlock()
}

func (l *agentChatLive) cancelRun(sessionID string) bool {
	l.mu.Lock()
	cancel, ok := l.running[sessionID]
	l.mu.Unlock()
	if !ok {
		return false
	}
	cancel()
	return true
}
