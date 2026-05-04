package api

import (
	"context"
	"sync"

	"github.com/hecate/agent-runtime/internal/agentchat"
)

type agentChatLive struct {
	mu          sync.Mutex
	subscribers map[string]map[chan AgentChatSessionResponse]struct{}
	running     map[string]agentChatRunControl
}

type agentChatRunControl struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func newAgentChatLive() *agentChatLive {
	return &agentChatLive{
		subscribers: make(map[string]map[chan AgentChatSessionResponse]struct{}),
		running:     make(map[string]agentChatRunControl),
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
	l.running[sessionID] = agentChatRunControl{cancel: cancel, done: make(chan struct{})}
	return true
}

func (l *agentChatLive) clearRun(sessionID string) {
	l.mu.Lock()
	run, ok := l.running[sessionID]
	if ok {
		delete(l.running, sessionID)
		close(run.done)
	}
	l.mu.Unlock()
}

func (l *agentChatLive) cancelRun(sessionID string) bool {
	l.mu.Lock()
	run, ok := l.running[sessionID]
	l.mu.Unlock()
	if !ok {
		return false
	}
	run.cancel()
	return true
}

func (l *agentChatLive) cancelRunAndWait(ctx context.Context, sessionID string) bool {
	l.mu.Lock()
	run, ok := l.running[sessionID]
	l.mu.Unlock()
	if !ok {
		return false
	}
	run.cancel()
	select {
	case <-run.done:
	case <-ctx.Done():
	}
	return true
}
