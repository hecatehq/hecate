package api

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestAgentChatLiveSessionReplacementPreservesApprovalEvents(t *testing.T) {
	t.Parallel()
	live := newAgentChatLive(agentChatSnapshotConfig{})
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
	live := newAgentChatLive(agentChatSnapshotConfig{})
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

func TestAgentChatLiveExclusiveMutationInvalidatesStaleTurnsWithoutBecomingCancellable(t *testing.T) {
	t.Parallel()
	live := newAgentChatLive(agentChatSnapshotConfig{})
	staleTurn := live.snapshotLifecycle("s")
	defer staleTurn.release()
	mutationSnapshot := live.snapshotLifecycle("s")
	defer mutationSnapshot.release()

	releaseMutation, admission := live.beginExclusiveMutation(mutationSnapshot)
	if admission != agentChatTurnAccepted {
		t.Fatalf("beginExclusiveMutation = %v, want accepted", admission)
	}
	currentTurn := live.snapshotLifecycle("s")
	defer currentTurn.release()
	if got := live.registerTurn(currentTurn, func() {}); got != agentChatTurnBusy {
		t.Fatalf("registerTurn during mutation = %v, want busy", got)
	}
	if live.cancelTurn("s") {
		t.Fatal("cancelTurn reported an exclusive settings mutation as a turn")
	}

	releaseMutation()
	if got := live.registerTurn(staleTurn, func() {}); got != agentChatTurnAdmissionClosed {
		t.Fatalf("registerTurn with pre-mutation snapshot = %v, want admission closed", got)
	}
	freshTurn := live.snapshotLifecycle("s")
	defer freshTurn.release()
	if got := live.registerTurn(freshTurn, func() {}); got != agentChatTurnAccepted {
		t.Fatalf("registerTurn after mutation = %v, want accepted", got)
	}
	live.clearTurn("s")
}

func TestAgentChatLiveShutdownCancellationClosesTurnAdmission(t *testing.T) {
	t.Parallel()
	live := newAgentChatLive(agentChatSnapshotConfig{})
	turnCtx, turnCancel := context.WithCancel(context.Background())
	snapshot := live.snapshotLifecycle("s")
	defer snapshot.release()
	if got := live.registerTurn(snapshot, turnCancel); got != agentChatTurnAccepted {
		t.Fatalf("registerTurn before shutdown = %v, want accepted", got)
	}
	defer live.clearTurn("s")

	live.cancelAllTurns("shutdown")

	select {
	case <-turnCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel the turn admitted before its snapshot")
	}
	if got := live.turnCancelReason("s"); got != "shutdown" {
		t.Fatalf("turn cancellation reason = %q, want shutdown", got)
	}
	afterShutdown := live.snapshotLifecycle("after_shutdown")
	defer afterShutdown.release()
	if got := live.registerTurn(afterShutdown, func() {}); got != agentChatTurnAdmissionClosed {
		t.Fatalf("registerTurn after shutdown = %v, want admission closed", got)
	}

	// Shutdown is idempotent and its admission fence is permanent.
	live.cancelAllTurns("shutdown")
	afterSecondShutdown := live.snapshotLifecycle("after_second_shutdown")
	defer afterSecondShutdown.release()
	if got := live.registerTurn(afterSecondShutdown, func() {}); got != agentChatTurnAdmissionClosed {
		t.Fatalf("registerTurn after second shutdown = %v, want admission closed", got)
	}
}

func TestAgentChatLiveOperatorReasonSurvivesOverlappingShutdown(t *testing.T) {
	t.Parallel()
	live := newAgentChatLive(agentChatSnapshotConfig{})
	cancelEntered := make(chan struct{})
	releaseCancel := make(chan struct{})
	defer func() {
		select {
		case <-releaseCancel:
		default:
			close(releaseCancel)
		}
	}()
	var firstCancel atomic.Bool
	snapshot := live.snapshotLifecycle("s")
	defer snapshot.release()
	if got := live.registerTurn(snapshot, func() {
		if firstCancel.CompareAndSwap(false, true) {
			close(cancelEntered)
			<-releaseCancel
		}
	}); got != agentChatTurnAccepted {
		t.Fatalf("registerTurn = %v, want accepted", got)
	}
	defer live.clearTurn("s")

	operatorDone := make(chan bool, 1)
	go func() {
		operatorDone <- live.cancelTurn("s")
	}()
	select {
	case <-cancelEntered:
	case <-time.After(time.Second):
		t.Fatal("operator cancellation did not reach the turn callback")
	}

	// Stop has already selected the turn and committed its reason. Shutdown
	// overlaps the still-running callback and must preserve that first writer.
	live.cancelAllTurns("shutdown")
	reason := live.turnCancelReason("s")
	close(releaseCancel)
	select {
	case ok := <-operatorDone:
		if !ok {
			t.Fatal("cancelTurn = false, want true")
		}
	case <-time.After(time.Second):
		t.Fatal("operator cancellation did not return")
	}
	if reason != "operator" {
		t.Fatalf("overlapping cancellation reason = %q, want operator", reason)
	}
}
