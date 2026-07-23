package api

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/providers"
)

type blockingSettlementStore struct {
	chat.Store

	mu         sync.Mutex
	calls      int
	active     int
	maxActive  int
	blockFirst bool
	entered    chan struct{}
	release    chan struct{}
	enterOnce  sync.Once
}

type settlementDeleteOrderStore struct {
	chat.Store
	mu                  sync.Mutex
	settlementSeen      bool
	deleteSawSettlement bool
}

func (s *settlementDeleteOrderStore) UpdateMessage(ctx context.Context, sessionID, messageID string, update func(*chat.Message)) (chat.Session, error) {
	updated, err := s.Store.UpdateMessage(ctx, sessionID, messageID, update)
	if err != nil {
		return updated, err
	}
	for _, message := range updated.Messages {
		for _, activity := range message.Activities {
			if activity.ID == "terminal:destructive" && activity.Status == "completed" {
				s.mu.Lock()
				s.settlementSeen = true
				s.mu.Unlock()
			}
		}
	}
	return updated, nil
}

func (s *settlementDeleteOrderStore) Delete(ctx context.Context, sessionID string) error {
	s.mu.Lock()
	s.deleteSawSettlement = s.settlementSeen
	s.mu.Unlock()
	return s.Store.Delete(ctx, sessionID)
}

func (s *settlementDeleteOrderStore) deletionObservedSettlement() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleteSawSettlement
}

type destructiveTerminalSettlementRunner struct {
	fakeAgentChatRunner
}

func (r *destructiveTerminalSettlementRunner) CloseSession(ctx context.Context, sessionID string) error {
	err := r.fakeAgentChatRunner.CloseSession(ctx, sessionID)
	r.settleLatestTerminal()
	return err
}

func (r *destructiveTerminalSettlementRunner) DeleteSession(ctx context.Context, sessionID string) error {
	err := r.fakeAgentChatRunner.DeleteSession(ctx, sessionID)
	r.settleLatestTerminal()
	return err
}

func (r *destructiveTerminalSettlementRunner) settleLatestTerminal() {
	if len(r.runRequests) == 0 {
		return
	}
	req := r.runRequests[len(r.runRequests)-1]
	if req.OnTerminalActivity != nil {
		req.OnTerminalActivity(agentadapters.Activity{
			ID:              "terminal:destructive",
			Type:            "terminal",
			Status:          "completed",
			Kind:            "execute",
			Title:           "Terminal command",
			Detail:          "exit code 0",
			ArtifactPreview: "settled before destructive mutation",
		})
	}
	if req.OnTerminalClosed != nil {
		req.OnTerminalClosed("destructive")
	}
}

func (s *blockingSettlementStore) UpdateMessage(ctx context.Context, sessionID, messageID string, update func(*chat.Message)) (chat.Session, error) {
	s.mu.Lock()
	s.calls++
	call := s.calls
	s.active++
	if s.active > s.maxActive {
		s.maxActive = s.active
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.active--
		s.mu.Unlock()
	}()

	if s.blockFirst && call == 1 {
		s.enterOnce.Do(func() { close(s.entered) })
		select {
		case <-s.release:
		case <-ctx.Done():
			return chat.Session{}, ctx.Err()
		}
	}
	return s.Store.UpdateMessage(ctx, sessionID, messageID, update)
}

func (s *blockingSettlementStore) maximumActive() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxActive
}

func newSettlementTestHandler(t *testing.T, store chat.Store) *Handler {
	t.Helper()
	return &Handler{
		logger:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
		agentChat:     store,
		agentChatLive: newAgentChatLive(agentChatSnapshotConfig{}),
	}
}

func seedSettlementMessage(t *testing.T, store chat.Store, sessionID, messageID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.Create(ctx, chat.Session{ID: sessionID, AgentID: "codex", Status: "idle"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.AppendMessage(ctx, sessionID, chat.Message{ID: messageID, Role: "assistant", Status: "running"}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
}

func TestAgentChatSettlementSerializesSlowTerminalAndFinalWrites(t *testing.T) {
	t.Parallel()

	base := chat.NewMemoryStore()
	store := &blockingSettlementStore{
		Store:      base,
		blockFirst: true,
		entered:    make(chan struct{}),
		release:    make(chan struct{}),
	}
	seedSettlementMessage(t, store, "chat_serial", "msg_origin")
	h := newSettlementTestHandler(t, store)
	turn, err := h.agentChatSettlements.acquireTurn(h, "chat_serial", "msg_origin")
	if err != nil {
		t.Fatalf("acquireTurn: %v", err)
	}

	started := time.Now()
	turn.terminalActivity(agentadapters.Activity{ID: "terminal:serial", Type: "terminal", Status: "running", Title: "Terminal command"})
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("terminal callback blocked for %s, want enqueue-only", elapsed)
	}
	select {
	case <-store.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for slow terminal write")
	}

	finalDone := make(chan error, 1)
	go func() {
		_, updateErr := turn.updateMessage(context.Background(), false, func(message *chat.Message) {
			message.Content = "final output"
			message.Status = "completed"
		})
		finalDone <- updateErr
	}()
	time.Sleep(20 * time.Millisecond)
	if got := store.maximumActive(); got != 1 {
		t.Fatalf("maximum concurrent UpdateMessage calls = %d, want 1", got)
	}
	close(store.release)
	if err := <-finalDone; err != nil {
		t.Fatalf("final UpdateMessage: %v", err)
	}
	turn.terminalClosed("serial")
	turn.finish()
	select {
	case <-turn.dispatcher.done:
	case <-time.After(2 * time.Second):
		t.Fatal("settlement dispatcher did not stop after terminal close and turn finish")
	}

	got, ok, err := base.Get(context.Background(), "chat_serial")
	if err != nil || !ok {
		t.Fatalf("Get: found=%v err=%v", ok, err)
	}
	if got.Status != "completed" || len(got.Messages) != 1 || got.Messages[0].Content != "final output" {
		t.Fatalf("settled session = %+v, want completed final output", got)
	}
	activity := findChatActivity(got.Messages[0].Activities, "terminal:serial")
	if activity == nil || activity.Status != "running" {
		t.Fatalf("terminal activity = %+v, want retained running activity", activity)
	}
	h.agentChatSettlements.mu.Lock()
	_, leaked := h.agentChatSettlements.dispatchers["chat_serial"]
	h.agentChatSettlements.mu.Unlock()
	if leaked {
		t.Fatal("settlement dispatcher leaked after its last turn and terminal closed")
	}
}

func TestAgentChatSettlementClaimDrainsBeforeDeleteAndRejectsLatePublish(t *testing.T) {
	t.Parallel()

	store := chat.NewMemoryStore()
	seedSettlementMessage(t, store, "chat_delete", "msg_origin")
	h := newSettlementTestHandler(t, store)
	turn, err := h.agentChatSettlements.acquireTurn(h, "chat_delete", "msg_origin")
	if err != nil {
		t.Fatalf("acquireTurn: %v", err)
	}
	updates, unsubscribe := h.agentChatLive.subscribe("chat_delete")
	defer unsubscribe()

	turn.terminalActivity(agentadapters.Activity{ID: "terminal:delete", Type: "terminal", Status: "running", Title: "Terminal command"})
	if _, err := turn.updateMessage(context.Background(), false, func(*chat.Message) {}); err != nil {
		t.Fatalf("running activity barrier: %v", err)
	}
	drainLiveEvents(updates)
	turn.finish()

	closure := h.agentChatLive.closeSessionLifecycle("chat_delete")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	if !closure.waitForOperations(ctx) {
		cancel()
		closure.release()
		t.Fatal("destructive lifecycle owner did not drain operations")
	}
	cancel()
	claim := h.agentChatSettlements.claimSession("chat_delete", closure)
	turn.terminalActivity(agentadapters.Activity{ID: "terminal:delete", Type: "terminal", Status: "completed", Title: "Terminal command"})
	turn.terminalClosed("delete")
	drainCtx, drainCancel := context.WithTimeout(context.Background(), time.Second)
	if !claim.sealAndDrain(drainCtx) {
		drainCancel()
		closure.release()
		t.Fatal("close-generated terminal settlement did not drain")
	}
	drainCancel()

	select {
	case event := <-updates:
		if event.SessionUpdate == nil || len(event.SessionUpdate.Data.Messages) != 1 {
			t.Fatalf("terminal settlement event = %+v, want originating transcript", event)
		}
		var activity *ChatActivityItem
		for i := range event.SessionUpdate.Data.Messages[0].Activities {
			candidate := &event.SessionUpdate.Data.Messages[0].Activities[i]
			if candidate.ID == "terminal:delete" {
				activity = candidate
				break
			}
		}
		if activity == nil || activity.Status != "completed" {
			t.Fatalf("terminal settlement event activity = %+v, want completed", activity)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for close-generated settlement publish")
	}
	if err := store.Delete(context.Background(), "chat_delete"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	closure.release()

	// A buggy adapter callback after OnTerminalClosed cannot re-enter the sealed
	// dispatcher or publish a deleted transcript snapshot.
	turn.terminalActivity(agentadapters.Activity{ID: "terminal:delete", Type: "terminal", Status: "failed", Title: "Terminal command"})
	select {
	case event := <-updates:
		t.Fatalf("live event after transcript delete = %+v, want none", event)
	case <-time.After(50 * time.Millisecond):
	}
	if _, ok, err := store.Get(context.Background(), "chat_delete"); err != nil || ok {
		t.Fatalf("Get after delete: found=%v err=%v, want absent", ok, err)
	}
}

func TestAgentChatSettlementShutdownCancelsSlowSinkWithinDeadline(t *testing.T) {
	t.Parallel()

	base := chat.NewMemoryStore()
	store := &blockingSettlementStore{
		Store:      base,
		blockFirst: true,
		entered:    make(chan struct{}),
		release:    make(chan struct{}),
	}
	seedSettlementMessage(t, store, "chat_shutdown", "msg_origin")
	h := newSettlementTestHandler(t, store)
	turn, err := h.agentChatSettlements.acquireTurn(h, "chat_shutdown", "msg_origin")
	if err != nil {
		t.Fatalf("acquireTurn: %v", err)
	}
	turn.terminalActivity(agentadapters.Activity{ID: "terminal:shutdown", Type: "terminal", Status: "running", Title: "Terminal command"})
	select {
	case <-store.entered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for slow terminal sink")
	}
	turn.finish()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	started := time.Now()
	err = h.agentChatSettlements.shutdown(ctx)
	cancel()
	if err == nil {
		t.Fatal("settlement shutdown error = nil, want deadline exceeded")
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("settlement shutdown took %s, want bounded return", elapsed)
	}
	select {
	case <-turn.dispatcher.done:
	case <-time.After(time.Second):
		t.Fatal("slow settlement worker did not stop after shutdown cancellation")
	}

	turn.terminalActivity(agentadapters.Activity{ID: "terminal:shutdown", Type: "terminal", Status: "completed", Title: "Terminal command"})
	got, ok, getErr := base.Get(context.Background(), "chat_shutdown")
	if getErr != nil || !ok {
		t.Fatalf("Get: found=%v err=%v", ok, getErr)
	}
	if activity := findChatActivity(got.Messages[0].Activities, "terminal:shutdown"); activity != nil {
		t.Fatalf("terminal activity after cancelled slow sink = %+v, want no post-shutdown write", activity)
	}
}

func TestAgentChatSettlementShutdownLetsAdmittedTurnFinalize(t *testing.T) {
	t.Parallel()

	store := chat.NewMemoryStore()
	seedSettlementMessage(t, store, "chat_shutdown_turn", "msg_origin")
	h := newSettlementTestHandler(t, store)
	turn, err := h.agentChatSettlements.acquireTurn(h, "chat_shutdown_turn", "msg_origin")
	if err != nil {
		t.Fatalf("acquireTurn: %v", err)
	}
	shutdownDone := make(chan error, 1)
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
	defer shutdownCancel()
	go func() { shutdownDone <- h.agentChatSettlements.shutdown(shutdownCtx) }()
	deadline := time.Now().Add(time.Second)
	for {
		h.agentChatSettlements.mu.Lock()
		closed := h.agentChatSettlements.closed
		h.agentChatSettlements.mu.Unlock()
		if closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("shutdown did not close new settlement admission")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-shutdownDone:
		t.Fatalf("shutdown returned before admitted turn finalized: %v", err)
	default:
	}

	if _, err := turn.updateMessage(context.Background(), false, func(message *chat.Message) {
		message.Status = "completed"
		message.Content = "settled during shutdown"
	}); err != nil {
		t.Fatalf("final update during shutdown: %v", err)
	}
	turn.finish()
	if err := <-shutdownDone; err != nil {
		t.Fatalf("shutdown after admitted turn finish: %v", err)
	}
	got, ok, err := store.Get(context.Background(), "chat_shutdown_turn")
	if err != nil || !ok || got.Status != "completed" || got.Messages[0].Content != "settled during shutdown" {
		t.Fatalf("session after shutdown drain = %+v found=%v err=%v", got, ok, err)
	}
}

func TestAgentChatSettlementDestructiveOwnerUnblocksActiveTurnFinal(t *testing.T) {
	t.Parallel()

	store := chat.NewMemoryStore()
	seedSettlementMessage(t, store, "chat_owner_unblock", "msg_origin")
	h := newSettlementTestHandler(t, store)
	turn, err := h.agentChatSettlements.acquireTurn(h, "chat_owner_unblock", "msg_origin")
	if err != nil {
		t.Fatalf("acquireTurn: %v", err)
	}
	lifecycle := h.agentChatLive.snapshotLifecycle("chat_owner_unblock")
	_, runCancel := context.WithCancel(context.Background())
	defer runCancel()
	if got := h.agentChatLive.registerTurn(lifecycle, runCancel); got != agentChatTurnAccepted {
		lifecycle.release()
		t.Fatalf("registerTurn = %v, want accepted", got)
	}
	lifecycle.release()
	defer h.agentChatLive.clearTurn("chat_owner_unblock")

	closure := h.agentChatLive.closeSessionLifecycle("chat_owner_unblock")
	drainCtx, drainCancel := context.WithTimeout(context.Background(), time.Second)
	if !closure.waitForOperations(drainCtx) {
		drainCancel()
		closure.release()
		t.Fatal("waitForOperations failed")
	}
	drainCancel()
	// This detached job dequeues against the newly-closed epoch and blocks at
	// the head of the queue. The active turn's final write queues behind it.
	turn.terminalActivity(agentadapters.Activity{ID: "terminal:owner", Type: "terminal", Status: "completed", Title: "Terminal command"})
	deadline := time.Now().Add(time.Second)
	for {
		turn.dispatcher.mu.Lock()
		queued := len(turn.dispatcher.queue)
		turn.dispatcher.mu.Unlock()
		if queued == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("detached terminal job was not dequeued against closed epoch")
		}
		time.Sleep(time.Millisecond)
	}
	finalDone := make(chan error, 1)
	go func() {
		_, updateErr := turn.updateMessage(context.Background(), false, func(message *chat.Message) {
			message.Status = "cancelled"
		})
		finalDone <- updateErr
	}()
	deadline = time.Now().Add(time.Second)
	for {
		turn.dispatcher.mu.Lock()
		queued := len(turn.dispatcher.queue)
		turn.dispatcher.mu.Unlock()
		if queued == 1 {
			break
		}
		select {
		case err := <-finalDone:
			t.Fatalf("active final passed closed-epoch terminal head before owner install: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("active final did not queue behind blocked terminal settlement")
		}
		time.Sleep(time.Millisecond)
	}

	claim := h.agentChatSettlements.claimSession("chat_owner_unblock", closure)
	select {
	case err := <-finalDone:
		if err != nil {
			t.Fatalf("active final after owner install: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("destructive owner did not unblock terminal settlement and active final")
	}
	h.agentChatLive.clearTurn("chat_owner_unblock")
	turn.finish()
	closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
	if !claim.sealAndDrain(closeCtx) {
		closeCancel()
		closure.release()
		t.Fatal("sealAndDrain failed")
	}
	closeCancel()
	closure.release()
	got, ok, err := store.Get(context.Background(), "chat_owner_unblock")
	if err != nil || !ok || got.Status != "cancelled" {
		t.Fatalf("session after owner drain = %+v found=%v err=%v", got, ok, err)
	}
	if activity := findChatActivity(got.Messages[0].Activities, "terminal:owner"); activity == nil || activity.Status != "completed" {
		t.Fatalf("terminal activity after owner drain = %+v, want completed", activity)
	}
}

func TestAgentChatSettlementAbortedClaimRelinquishesOwnerAndReaps(t *testing.T) {
	t.Parallel()

	store := chat.NewMemoryStore()
	seedSettlementMessage(t, store, "chat_claim_abort", "msg_origin")
	h := newSettlementTestHandler(t, store)
	turn, err := h.agentChatSettlements.acquireTurn(h, "chat_claim_abort", "msg_origin")
	if err != nil {
		t.Fatalf("acquireTurn: %v", err)
	}
	turn.terminalActivity(agentadapters.Activity{ID: "terminal:claim_abort", Type: "terminal", Status: "running", Title: "Terminal command"})
	if _, err := turn.currentSession(context.Background(), false); err != nil {
		t.Fatalf("terminal running barrier: %v", err)
	}
	turn.finish()

	closure := h.agentChatLive.closeSessionLifecycle("chat_claim_abort")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	if !closure.waitForOperations(ctx) {
		cancel()
		closure.release()
		t.Fatal("waitForOperations failed")
	}
	cancel()
	claim := h.agentChatSettlements.claimSession("chat_claim_abort", closure)
	claim.releaseLifecycleAfterRelinquish(closure)

	deadline := time.Now().Add(time.Second)
	for {
		lifecycle := h.agentChatLive.snapshotLifecycle("chat_claim_abort")
		release, accepted := h.agentChatLive.beginLifecycleOperation(lifecycle)
		lifecycle.release()
		if accepted {
			release()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("aborted claim did not reopen lifecycle admission")
		}
		time.Sleep(5 * time.Millisecond)
	}
	turn.terminalClosed("claim_abort")
	select {
	case <-turn.dispatcher.done:
	case <-time.After(time.Second):
		t.Fatal("dispatcher retained aborted destructive owner")
	}
	h.agentChatSettlements.mu.Lock()
	_, leaked := h.agentChatSettlements.dispatchers["chat_claim_abort"]
	h.agentChatSettlements.mu.Unlock()
	if leaked {
		t.Fatal("dispatcher leaked after aborted claim and terminal close")
	}
}

func TestAgentChatSettlementFinalReadPublishesAuthoritativeSettledSnapshot(t *testing.T) {
	t.Parallel()

	store := chat.NewMemoryStore()
	seedSettlementMessage(t, store, "chat_publish_current", "msg_origin")
	h := newSettlementTestHandler(t, store)
	turn, err := h.agentChatSettlements.acquireTurn(h, "chat_publish_current", "msg_origin")
	if err != nil {
		t.Fatalf("acquireTurn: %v", err)
	}
	updates, unsubscribe := h.agentChatLive.subscribe("chat_publish_current")
	defer unsubscribe()
	stale, err := turn.updateSession(context.Background(), func(session *chat.Session) {
		session.TurnsUsed++
	})
	if err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	if findChatActivity(stale.Messages[0].Activities, "terminal:publish") != nil {
		t.Fatal("pre-interleaving snapshot unexpectedly contains terminal activity")
	}
	turn.terminalActivity(agentadapters.Activity{ID: "terminal:publish", Type: "terminal", Status: "completed", Title: "Terminal command"})
	current, err := turn.settledSession(context.Background())
	if err != nil {
		t.Fatalf("settledSession: %v", err)
	}
	turn.finish()
	if activity := findChatActivity(current.Messages[0].Activities, "terminal:publish"); activity == nil || activity.Status != "completed" {
		t.Fatalf("authoritative current session activity = %+v, want completed", activity)
	}
	var last AgentChatLiveEvent
	for {
		select {
		case event := <-updates:
			last = event
		default:
			goto drained
		}
	}
drained:
	if last.SessionUpdate == nil {
		t.Fatal("final authoritative session publish missing")
	}
	if !last.turnSettled {
		t.Fatal("final authoritative session publish was not marked settled")
	}
	if last.settledMessageID != "msg_origin" {
		t.Fatalf("settled message id = %q, want msg_origin", last.settledMessageID)
	}
	activity := findChatActivityByType(last.SessionUpdate.Data.Messages[0], "terminal")
	if activity.ID != "terminal:publish" || activity.Status != "completed" {
		t.Fatalf("last live snapshot terminal activity = %+v, want authoritative completion", activity)
	}
}

func TestAgentChatClosedSettlementAdmissionDoesNotAppendRunningPlaceholder(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	runner := &fakeAgentChatRunner{output: "must not run"}
	handler.SetAgentChatRunner(runner)
	if err := handler.agentChatSettlements.shutdown(context.Background()); err != nil {
		t.Fatalf("close settlement admission: %v", err)
	}
	client := newAPITestClient(t, NewServer(logger, handler))
	created := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"codex","workspace":%q}`, t.TempDir()))
	client.mustRequestStatus(http.StatusServiceUnavailable, http.MethodPost, "/hecate/v1/chat/sessions/"+created.Data.ID+"/messages", `{"content":"do not persist"}`)
	hydrated := mustRequestJSON[ChatSessionResponse](client, http.MethodGet, "/hecate/v1/chat/sessions/"+created.Data.ID, "")
	if len(hydrated.Data.Messages) != 0 || hydrated.Data.Status == "running" {
		t.Fatalf("session after closed settlement admission = %+v, want no running placeholder", hydrated.Data)
	}
	if len(runner.runRequests) != 0 {
		t.Fatalf("runner requests = %d, want no dispatch", len(runner.runRequests))
	}
}

func TestAgentChatDeleteAndQuiescedCleanupDrainTerminalSettlement(t *testing.T) {
	for _, test := range []struct {
		name   string
		delete func(*testing.T, *Handler, apiTestClient, string)
	}{
		{
			name: "delete endpoint",
			delete: func(t *testing.T, _ *Handler, client apiTestClient, sessionID string) {
				t.Helper()
				client.mustRequestStatus(http.StatusNoContent, http.MethodDelete, "/hecate/v1/chat/sessions/"+sessionID, "")
			},
		},
		{
			name: "quiesced reset chat cleanup",
			delete: func(t *testing.T, handler *Handler, _ apiTestClient, _ string) {
				t.Helper()
				deleted, err := handler.resetChatSessions(context.Background())
				if err != nil || deleted != 1 {
					t.Fatalf("resetChatSessions: deleted=%d err=%v, want 1 nil", deleted, err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
			base := chat.NewMemoryStore()
			store := &settlementDeleteOrderStore{Store: base}
			handler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
			handler.SetAgentChatStore(store)
			runner := &destructiveTerminalSettlementRunner{fakeAgentChatRunner: fakeAgentChatRunner{
				output: "done\n",
				terminalActivities: [][]agentadapters.Activity{{{
					ID: "terminal:destructive", Type: "terminal", Status: "running", Kind: "execute", Title: "Terminal command",
				}}},
			}}
			handler.SetAgentChatRunner(runner)
			client := newAPITestClient(t, NewServer(logger, handler))
			created := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"codex","workspace":%q}`, t.TempDir()))
			mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+created.Data.ID+"/messages", `{"content":"start terminal"}`)
			updates, unsubscribe := handler.agentChatLive.subscribe(created.Data.ID)
			defer unsubscribe()
			drainLiveEvents(updates)

			test.delete(t, handler, client, created.Data.ID)
			if !store.deletionObservedSettlement() {
				t.Fatal("transcript delete ran before close-generated terminal settlement")
			}
			if _, ok, err := store.Get(context.Background(), created.Data.ID); err != nil || ok {
				t.Fatalf("Get after destructive delete: found=%v err=%v, want absent", ok, err)
			}
			if got := runner.deletedSessions; len(got) != 1 || got[0] != created.Data.ID {
				t.Fatalf("runner deleted sessions = %v, want [%s]", got, created.Data.ID)
			}
			drainLiveEvents(updates)
			request := runner.runRequests[0]
			request.OnTerminalActivity(agentadapters.Activity{ID: "terminal:destructive", Type: "terminal", Status: "failed", Title: "Terminal command"})
			request.OnTerminalClosed("destructive")
			select {
			case event := <-updates:
				t.Fatalf("stale live event after destructive delete = %+v", event)
			case <-time.After(50 * time.Millisecond):
			}
			handler.agentChatSettlements.mu.Lock()
			_, leaked := handler.agentChatSettlements.dispatchers[created.Data.ID]
			handler.agentChatSettlements.mu.Unlock()
			if leaked {
				t.Fatal("settlement dispatcher leaked after destructive delete")
			}
		})
	}
}

func TestAgentChatCloseDrainsTerminalSettlementBeforeClearingNativeState(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	runner := &destructiveTerminalSettlementRunner{fakeAgentChatRunner: fakeAgentChatRunner{
		output:          "done\n",
		nativeSessionID: "native_destructive_close",
		terminalActivities: [][]agentadapters.Activity{{{
			ID: "terminal:destructive", Type: "terminal", Status: "running", Kind: "execute", Title: "Terminal command",
		}}},
	}}
	handler.SetAgentChatRunner(runner)
	client := newAPITestClient(t, NewServer(logger, handler))
	created := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"codex","workspace":%q}`, t.TempDir()))
	mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+created.Data.ID+"/messages", `{"content":"start terminal"}`)

	closed := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+created.Data.ID+"/close", "")
	if closed.Data.NativeSessionID != "" || closed.Data.DriverKind != "" {
		t.Fatalf("closed native state = kind %q id %q, want cleared", closed.Data.DriverKind, closed.Data.NativeSessionID)
	}
	if len(closed.Data.Messages) != 2 {
		t.Fatalf("closed transcript messages = %d, want 2", len(closed.Data.Messages))
	}
	activity := findChatActivityByType(closed.Data.Messages[1], "terminal")
	if activity.ID != "terminal:destructive" || activity.Status != "completed" {
		t.Fatalf("closed terminal activity = %+v, want completed settlement", activity)
	}
	if got := runner.closedSessions; len(got) != 1 || got[0] != created.Data.ID {
		t.Fatalf("runner closed sessions = %v, want exactly [%s]", got, created.Data.ID)
	}
	handler.agentChatSettlements.mu.Lock()
	_, leaked := handler.agentChatSettlements.dispatchers[created.Data.ID]
	handler.agentChatSettlements.mu.Unlock()
	if leaked {
		t.Fatal("settlement dispatcher leaked after native close")
	}
}

func findChatActivity(activities []chat.Activity, id string) *chat.Activity {
	for i := range activities {
		if activities[i].ID == id {
			return &activities[i]
		}
	}
	return nil
}

func drainLiveEvents(updates <-chan AgentChatLiveEvent) {
	for {
		select {
		case <-updates:
		default:
			return
		}
	}
}
