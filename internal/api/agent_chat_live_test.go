package api

import "testing"

func TestAgentChatLiveSessionReplacementPreservesApprovalEvents(t *testing.T) {
	t.Parallel()
	live := newAgentChatLive(0)
	updates, unsubscribe := live.subscribe("s")
	defer unsubscribe()

	live.publish("s", AgentChatLiveEvent{Type: AgentChatLiveEventApprovalRequested}, false)
	for i := 0; i < cap(updates)-1; i++ {
		live.publish("s", AgentChatLiveEvent{Type: AgentChatLiveEventSessionUpdate}, false)
	}
	live.publish("s", AgentChatLiveEvent{Type: AgentChatLiveEventSessionUpdate}, true)

	got := drainAgentChatLiveEvents(updates, cap(updates))
	approvalCount := 0
	sessionCount := 0
	for _, event := range got {
		switch event.Type {
		case AgentChatLiveEventApprovalRequested:
			approvalCount++
		case AgentChatLiveEventSessionUpdate:
			sessionCount++
		}
	}
	if approvalCount != 1 {
		t.Fatalf("approval events preserved = %d, want 1", approvalCount)
	}
	if sessionCount != cap(updates)-1 {
		t.Fatalf("session updates = %d, want %d", sessionCount, cap(updates)-1)
	}
}

func TestAgentChatLiveSessionReplacementDropsNewSessionWhenOnlyApprovalsBuffered(t *testing.T) {
	t.Parallel()
	live := newAgentChatLive(0)
	updates, unsubscribe := live.subscribe("s")
	defer unsubscribe()

	for i := 0; i < cap(updates); i++ {
		live.publish("s", AgentChatLiveEvent{Type: AgentChatLiveEventApprovalRequested}, false)
	}
	live.publish("s", AgentChatLiveEvent{Type: AgentChatLiveEventSessionUpdate}, true)

	got := drainAgentChatLiveEvents(updates, cap(updates))
	for _, event := range got {
		if event.Type == AgentChatLiveEventSessionUpdate {
			t.Fatal("session update evicted approval event from full buffer")
		}
	}
}

func drainAgentChatLiveEvents(updates <-chan AgentChatLiveEvent, n int) []AgentChatLiveEvent {
	out := make([]AgentChatLiveEvent, 0, n)
	for i := 0; i < n; i++ {
		select {
		case event := <-updates:
			out = append(out, event)
		default:
			return out
		}
	}
	return out
}
