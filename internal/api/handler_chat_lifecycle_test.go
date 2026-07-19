package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/providers"
)

type staleFirstAgentChatGetStore struct {
	chat.Store
	sessionID string
	armed     atomic.Bool
	once      sync.Once
	captured  chan struct{}
	release   chan struct{}
}

func (s *staleFirstAgentChatGetStore) Get(ctx context.Context, id string) (chat.Session, bool, error) {
	if id != s.sessionID || !s.armed.Load() {
		return s.Store.Get(ctx, id)
	}
	block := false
	s.once.Do(func() { block = true })
	if !block {
		return s.Store.Get(ctx, id)
	}
	session, ok, err := s.Store.Get(ctx, id)
	close(s.captured)
	select {
	case <-s.release:
		return session, ok, err
	case <-ctx.Done():
		return chat.Session{}, false, ctx.Err()
	}
}

func TestAgentChatLiveLifecycleSnapshotsStayStaleAcrossClose(t *testing.T) {
	live := newAgentChatLive(agentChatSnapshotConfig{})
	beforeClose := live.snapshotLifecycle("session_1")
	closure := live.closeSessionLifecycle("session_1")
	duringClose := live.snapshotLifecycle("session_1")

	if got := live.registerTurn(beforeClose, func() {}); got != agentChatTurnAdmissionClosed {
		t.Fatalf("registerTurn(snapshot before close) = %v, want admission closed", got)
	}
	if got := live.registerTurn(duringClose, func() {}); got != agentChatTurnAdmissionClosed {
		t.Fatalf("registerTurn(snapshot during close) = %v, want admission closed", got)
	}

	closure.release()
	if got := live.registerTurn(beforeClose, func() {}); got != agentChatTurnAdmissionClosed {
		t.Fatalf("registerTurn(snapshot before released close) = %v, want admission closed", got)
	}
	if got := live.registerTurn(duringClose, func() {}); got != agentChatTurnAdmissionClosed {
		t.Fatalf("registerTurn(snapshot during released close) = %v, want admission closed", got)
	}

	current := live.snapshotLifecycle("session_1")
	if got := live.registerTurn(current, func() {}); got != agentChatTurnAccepted {
		t.Fatalf("registerTurn(current snapshot) = %v, want accepted", got)
	}
	live.clearTurn("session_1")

	live.mu.Lock()
	retained := len(live.lifecycles)
	live.mu.Unlock()
	if retained != 1 {
		t.Fatalf("lifecycle states while snapshots are leased = %d, want 1", retained)
	}
	current.release()
	duringClose.release()
	beforeClose.release()
	live.mu.Lock()
	retained = len(live.lifecycles)
	live.mu.Unlock()
	if retained != 0 {
		t.Fatalf("lifecycle states after snapshot leases drained = %d, want 0", retained)
	}
}

func TestAgentChatLiveLifecycleRejectsReleasedSnapshotAfterReclamation(t *testing.T) {
	live := newAgentChatLive(agentChatSnapshotConfig{})
	snapshot := live.snapshotLifecycle("session_released")
	snapshot.release()

	if got := live.registerTurn(snapshot, func() {}); got != agentChatTurnAdmissionClosed {
		t.Fatalf("registerTurn(released snapshot) = %v, want admission closed", got)
	}
	live.mu.Lock()
	retained := len(live.lifecycles)
	live.mu.Unlock()
	if retained != 0 {
		t.Fatalf("lifecycle states after released snapshot = %d, want 0", retained)
	}
}

func TestAgentChatLiveLifecycleClosureWaitsForCountedOperations(t *testing.T) {
	live := newAgentChatLive(agentChatSnapshotConfig{})
	snapshot := live.snapshotLifecycle("session_1")
	defer snapshot.release()
	releaseOperation, accepted := live.beginLifecycleOperation(snapshot)
	if !accepted {
		t.Fatal("beginLifecycleOperation() = rejected, want accepted")
	}
	closure := live.closeSessionLifecycle("session_1")
	defer closure.release()

	select {
	case <-closure.drained:
		t.Fatal("lifecycle operation drain closed before the admitted operation released")
	default:
	}

	releaseOperation()
	select {
	case <-closure.drained:
	case <-time.After(time.Second):
		t.Fatal("lifecycle operation drain did not close after the operation released")
	}
	if !closure.waitForOperations(context.Background()) {
		t.Fatal("waitForOperations() = false after drain, want true")
	}
}

func TestAgentChatLiveLifecycleSerializesDestructiveClosures(t *testing.T) {
	live := newAgentChatLive(agentChatSnapshotConfig{})
	first := live.closeSessionLifecycle("session_serial")
	second := live.closeSessionLifecycle("session_serial")
	if !first.waitForOperations(context.Background()) {
		t.Fatal("first waitForOperations() = false, want true")
	}

	secondReady := make(chan bool, 1)
	go func() {
		secondReady <- second.waitForOperations(context.Background())
	}()
	select {
	case ready := <-secondReady:
		t.Fatalf("second destructive closure acquired before first released: ready=%v", ready)
	case <-time.After(100 * time.Millisecond):
	}

	first.release()
	select {
	case ready := <-secondReady:
		if !ready {
			t.Fatal("second waitForOperations() = false after first released, want true")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("second destructive closure did not acquire after first released")
	}
	second.release()

	live.mu.Lock()
	retained := len(live.lifecycles)
	live.mu.Unlock()
	if retained != 0 {
		t.Fatalf("lifecycle states after destructive closures released = %d, want 0", retained)
	}
}

func TestAgentChatMessageStaleExternalSnapshotRejectedAfterClose(t *testing.T) {
	baseStore := chat.NewMemoryStore()
	now := time.Now().UTC()
	session, err := baseStore.Create(context.Background(), chat.Session{
		ID:              "chat_stale_external",
		Title:           "Stale external chat",
		AgentID:         "codex",
		DriverKind:      agentadapters.DriverKindACP,
		NativeSessionID: "native_before_close",
		Workspace:       t.TempDir(),
		Status:          "idle",
		CreatedAt:       now,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	store := &staleFirstAgentChatGetStore{
		Store:     baseStore,
		sessionID: session.ID,
		captured:  make(chan struct{}),
		release:   make(chan struct{}),
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	runner := &fakeAgentChatRunner{}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatStore(store)
	apiHandler.SetAgentChatRunner(runner)
	handler := NewServer(logger, apiHandler)
	store.armed.Store(true)

	messageDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := httptest.NewRequest(http.MethodPost, "/hecate/v1/chat/sessions/"+session.ID+"/messages", strings.NewReader(`{"content":"do not dispatch"}`))
		request.RemoteAddr = "127.0.0.1:1234"
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		messageDone <- recorder
	}()

	select {
	case <-store.captured:
	case <-time.After(3 * time.Second):
		t.Fatal("message request did not capture the pre-close session")
	}
	closed := performRequest(t, handler, http.MethodPost, "/hecate/v1/chat/sessions/"+session.ID+"/close", `{}`)
	if closed.Code != http.StatusOK {
		t.Fatalf("close status = %d, want %d, body=%s", closed.Code, http.StatusOK, closed.Body.String())
	}
	close(store.release)

	var message *httptest.ResponseRecorder
	select {
	case message = <-messageDone:
	case <-time.After(3 * time.Second):
		t.Fatal("stale message request did not finish after session read released")
	}
	if message.Code != http.StatusConflict {
		t.Fatalf("message status = %d, want %d, body=%s", message.Code, http.StatusConflict, message.Body.String())
	}
	var response chatAttachmentErrorResponse
	if err := json.Unmarshal(message.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode message error: %v, body=%s", err, message.Body.String())
	}
	if response.Error.Type != errCodeSessionStopping {
		t.Fatalf("message error type = %q, want %q", response.Error.Type, errCodeSessionStopping)
	}
	if len(runner.runRequests) != 0 {
		t.Fatalf("runner requests = %d, want zero stale native dispatches", len(runner.runRequests))
	}
	got, ok, err := baseStore.Get(context.Background(), session.ID)
	if err != nil || !ok {
		t.Fatalf("Get() after close = ok %v, error %v", ok, err)
	}
	if got.NativeSessionID != "" || got.DriverKind != "" || len(got.Messages) != 0 {
		t.Fatalf("session after stale message = native %q driver %q messages %d, want closed without appended turn", got.NativeSessionID, got.DriverKind, len(got.Messages))
	}
}

func TestAgentChatCloseRereadsSessionAfterConcurrentDelete(t *testing.T) {
	baseStore := chat.NewMemoryStore()
	now := time.Now().UTC()
	session, err := baseStore.Create(context.Background(), chat.Session{
		ID:              "chat_stale_destructive",
		Title:           "Stale destructive chat",
		AgentID:         "codex",
		DriverKind:      agentadapters.DriverKindACP,
		NativeSessionID: "native_before_delete",
		Workspace:       t.TempDir(),
		Status:          "idle",
		CreatedAt:       now,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	store := &staleFirstAgentChatGetStore{
		Store:     baseStore,
		sessionID: session.ID,
		captured:  make(chan struct{}),
		release:   make(chan struct{}),
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	runner := &fakeAgentChatRunner{}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatStore(store)
	apiHandler.SetAgentChatRunner(runner)
	handler := NewServer(logger, apiHandler)
	store.armed.Store(true)

	closeDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := httptest.NewRequest(http.MethodPost, "/hecate/v1/chat/sessions/"+session.ID+"/close", strings.NewReader(`{}`))
		request.RemoteAddr = "127.0.0.1:1234"
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		closeDone <- recorder
	}()
	select {
	case <-store.captured:
	case <-time.After(3 * time.Second):
		t.Fatal("close request did not capture the pre-delete session")
	}

	deleted := performRequest(t, handler, http.MethodDelete, "/hecate/v1/chat/sessions/"+session.ID, ``)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d, body=%s", deleted.Code, http.StatusNoContent, deleted.Body.String())
	}
	close(store.release)

	var closed *httptest.ResponseRecorder
	select {
	case closed = <-closeDone:
	case <-time.After(3 * time.Second):
		t.Fatal("stale close request did not finish after session read released")
	}
	if closed.Code != http.StatusNotFound {
		t.Fatalf("close status = %d, want %d, body=%s", closed.Code, http.StatusNotFound, closed.Body.String())
	}
	if len(runner.closedSessions) != 0 {
		t.Fatalf("closed sessions = %#v, want no stale close dispatch", runner.closedSessions)
	}
	if len(runner.deletedSessions) != 1 || runner.deletedSessions[0] != session.ID {
		t.Fatalf("deleted sessions = %#v, want one authoritative delete", runner.deletedSessions)
	}
}

func TestAgentChatIdleSweepSkipsTurnThatRegisteredFirst(t *testing.T) {
	store := chat.NewMemoryStore()
	now := time.Now().UTC()
	old := now.Add(-2 * time.Hour)
	session, err := store.Create(context.Background(), chat.Session{
		ID:              "chat_idle_registered_run",
		Title:           "Active despite stale transcript timestamp",
		AgentID:         "codex",
		DriverKind:      agentadapters.DriverKindACP,
		NativeSessionID: "native_active",
		Workspace:       t.TempDir(),
		Status:          "idle",
		CreatedAt:       old,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	runner := &fakeAgentChatRunner{}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatStore(store)
	apiHandler.SetAgentChatRunner(runner)

	if got := apiHandler.agentChatLive.registerTurn(apiHandler.agentChatLive.snapshotLifecycle(session.ID), func() {}); got != agentChatTurnAccepted {
		t.Fatalf("registerTurn() = %v, want accepted", got)
	}
	defer apiHandler.agentChatLive.clearTurn(session.ID)

	apiHandler.closeIdleChatSessions(context.Background(), time.Hour, now)

	got, ok, err := store.Get(context.Background(), session.ID)
	if err != nil || !ok {
		t.Fatalf("Get() after idle sweep = ok %v, error %v", ok, err)
	}
	if got.Status != "idle" || got.NativeSessionID != "native_active" || got.DriverKind != agentadapters.DriverKindACP {
		t.Fatalf("session after idle sweep = status %q native %q driver %q, want active handles preserved", got.Status, got.NativeSessionID, got.DriverKind)
	}
	if len(runner.closedSessions) != 0 {
		t.Fatalf("closed sessions = %#v, want registered turn skipped", runner.closedSessions)
	}
}
