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

type blockedBeforeAgentChatGetStore struct {
	chat.Store
	sessionID string
	armed     atomic.Bool
	once      sync.Once
	entered   chan struct{}
	release   chan struct{}
}

func (s *blockedBeforeAgentChatGetStore) Get(ctx context.Context, id string) (chat.Session, bool, error) {
	if id != s.sessionID || !s.armed.Load() {
		return s.Store.Get(ctx, id)
	}
	block := false
	s.once.Do(func() { block = true })
	if !block {
		return s.Store.Get(ctx, id)
	}
	close(s.entered)
	select {
	case <-s.release:
		return s.Store.Get(ctx, id)
	case <-ctx.Done():
		return chat.Session{}, false, ctx.Err()
	}
}

type blockedNthAgentChatGetStore struct {
	chat.Store
	sessionID string
	blockOn   int64
	calls     atomic.Int64
	entered   chan struct{}
	release   chan struct{}
}

func (s *blockedNthAgentChatGetStore) Get(ctx context.Context, id string) (chat.Session, bool, error) {
	if id != s.sessionID || s.calls.Add(1) != s.blockOn {
		return s.Store.Get(ctx, id)
	}
	close(s.entered)
	select {
	case <-s.release:
		return s.Store.Get(ctx, id)
	case <-ctx.Done():
		return chat.Session{}, false, ctx.Err()
	}
}

type skewedAgentChatGetStore struct {
	chat.Store
	runningUpdatedAt  time.Time
	terminalUpdatedAt time.Time
}

func (s *skewedAgentChatGetStore) Get(ctx context.Context, id string) (chat.Session, bool, error) {
	session, ok, err := s.Store.Get(ctx, id)
	if err != nil || !ok {
		return session, ok, err
	}
	if isTerminalAgentChatStatus(session.Status) {
		session.UpdatedAt = s.terminalUpdatedAt
	} else {
		session.UpdatedAt = s.runningUpdatedAt
	}
	return session, true, nil
}

type synchronizedSSERecorder struct {
	mu      sync.Mutex
	header  http.Header
	body    strings.Builder
	status  int
	flushes chan struct{}
}

func newSynchronizedSSERecorder() *synchronizedSSERecorder {
	return &synchronizedSSERecorder{
		header:  make(http.Header),
		flushes: make(chan struct{}, 16),
	}
}

func (r *synchronizedSSERecorder) Header() http.Header {
	return r.header
}

func (r *synchronizedSSERecorder) WriteHeader(status int) {
	r.mu.Lock()
	if r.status == 0 {
		r.status = status
	}
	r.mu.Unlock()
}

func (r *synchronizedSSERecorder) Write(body []byte) (int, error) {
	r.mu.Lock()
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.body.Write(body)
	r.mu.Unlock()
	return n, err
}

func (r *synchronizedSSERecorder) Flush() {
	r.flushes <- struct{}{}
}

func (r *synchronizedSSERecorder) waitForFlush(t *testing.T) {
	t.Helper()
	select {
	case <-r.flushes:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for SSE flush")
	}
}

func (r *synchronizedSSERecorder) bodyString() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.String()
}

func TestAgentChatStreamCannotMissTerminalPublishDuringInitialSnapshot(t *testing.T) {
	const sessionID = "chat_stream_snapshot_race"
	baseStore := chat.NewMemoryStore()
	store := &staleFirstAgentChatGetStore{
		Store:     baseStore,
		sessionID: sessionID,
		captured:  make(chan struct{}),
		release:   make(chan struct{}),
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatStore(store)
	handler := NewServer(logger, apiHandler)
	session, err := baseStore.Create(t.Context(), chat.Session{
		ID:         sessionID,
		Title:      "Stream snapshot race",
		AgentID:    "claude_code",
		DriverKind: agentadapters.DriverKindACP,
		Workspace:  t.TempDir(),
		Status:     "running",
		Messages: []chat.Message{{
			ID:        "msg_running",
			Role:      "assistant",
			Status:    "running",
			CreatedAt: time.Now().UTC(),
		}},
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	store.armed.Store(true)

	requestCtx, cancelRequest := context.WithCancel(t.Context())
	defer cancelRequest()
	request := httptest.NewRequest(http.MethodGet, "/hecate/v1/chat/sessions/"+session.ID+"/stream", nil).WithContext(requestCtx)
	recorder := httptest.NewRecorder()
	streamDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(recorder, request)
		close(streamDone)
	}()

	select {
	case <-store.captured:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not capture its initial running snapshot")
	}
	apiHandler.agentChatLive.mu.Lock()
	subscriberCount := len(apiHandler.agentChatLive.subscribers[session.ID])
	apiHandler.agentChatLive.mu.Unlock()
	if subscriberCount != 1 {
		t.Fatalf("subscribers during blocked snapshot = %d, want 1", subscriberCount)
	}
	terminal, err := baseStore.UpdateSession(t.Context(), session.ID, func(item *chat.Session) {
		item.Status = "completed"
		item.Messages[0].Status = "completed"
		item.Messages[0].Content = "finished while the initial read was blocked"
		item.Messages[0].CompletedAt = time.Now().UTC()
	})
	if err != nil {
		t.Fatalf("UpdateSession() terminal state error = %v", err)
	}
	apiHandler.agentChatLive.publishSession(terminal)
	close(store.release)

	select {
	case <-streamDone:
	case <-time.After(3 * time.Second):
		cancelRequest()
		select {
		case <-streamDone:
		case <-time.After(3 * time.Second):
			t.Fatal("stream did not stop after its request was cancelled")
		}
		t.Fatalf("stream missed the terminal publication and did not emit done; body=%q", recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "event: snapshot\n") {
		t.Fatalf("stream body = %q, want initial snapshot", body)
	}
	if !strings.Contains(body, "event: done\n") {
		t.Fatalf("stream body = %q, want terminal done event", body)
	}
	if !strings.Contains(body, `"status":"completed"`) ||
		!strings.Contains(body, "finished while the initial read was blocked") {
		t.Fatalf("stream body = %q, want authoritative terminal snapshot", body)
	}
}

func TestAgentChatStreamHeartbeatReconcilesUnpublishedTerminalCommit(t *testing.T) {
	const sessionID = "chat_stream_unpublished_terminal"
	baseStore := chat.NewMemoryStore()
	store := &staleFirstAgentChatGetStore{
		Store:     baseStore,
		sessionID: sessionID,
		captured:  make(chan struct{}),
		release:   make(chan struct{}),
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatStore(store)
	heartbeat := make(chan time.Time, 1)
	apiHandler.agentChatStreamHeartbeatC = heartbeat
	handler := NewServer(logger, apiHandler)
	session, err := baseStore.Create(t.Context(), chat.Session{
		ID:         sessionID,
		Title:      "Unpublished terminal",
		AgentID:    "claude_code",
		DriverKind: agentadapters.DriverKindACP,
		Workspace:  t.TempDir(),
		Status:     "running",
		Messages: []chat.Message{{
			ID:        "msg_running",
			Role:      "assistant",
			Status:    "running",
			CreatedAt: time.Now().UTC(),
		}},
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	store.armed.Store(true)

	requestCtx, cancelRequest := context.WithCancel(t.Context())
	defer cancelRequest()
	request := httptest.NewRequest(http.MethodGet, "/hecate/v1/chat/sessions/"+session.ID+"/stream", nil).WithContext(requestCtx)
	recorder := httptest.NewRecorder()
	streamDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(recorder, request)
		close(streamDone)
	}()

	select {
	case <-store.captured:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not capture its initial running snapshot")
	}
	_, err = baseStore.UpdateSession(t.Context(), session.ID, func(item *chat.Session) {
		item.Status = "completed"
		item.Messages[0].Status = "completed"
		item.Messages[0].Content = "recovered from durable state"
		item.Messages[0].CompletedAt = time.Now().UTC()
	})
	if err != nil {
		t.Fatalf("UpdateSession() terminal state error = %v", err)
	}
	// Deliberately omit agentChatLive.publishSession: this is the failed final
	// publish/read condition that heartbeat reconciliation must recover.
	close(store.release)
	heartbeat <- time.Now()

	select {
	case <-streamDone:
	case <-time.After(3 * time.Second):
		cancelRequest()
		select {
		case <-streamDone:
		case <-time.After(3 * time.Second):
			t.Fatal("stream did not stop after its request was cancelled")
		}
		t.Fatalf("stream did not reconcile the unpublished terminal commit; body=%q", recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "event: snapshot\n") {
		t.Fatalf("stream body = %q, want snapshots", body)
	}
	if !strings.Contains(body, "event: done\n") {
		t.Fatalf("stream body = %q, want terminal done event", body)
	}
	if !strings.Contains(body, `"status":"completed"`) ||
		!strings.Contains(body, "recovered from durable state") {
		t.Fatalf("stream body = %q, want reconciled durable terminal snapshot", body)
	}
}

func TestAgentChatStreamDefersTerminalSnapshotUntilLiveTurnSettles(t *testing.T) {
	const sessionID = "chat_stream_terminal_settlement"
	baseStore := chat.NewMemoryStore()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatStore(baseStore)
	heartbeat := make(chan time.Time, 1)
	apiHandler.agentChatStreamHeartbeatC = heartbeat
	handler := NewServer(logger, apiHandler)
	session, err := baseStore.Create(t.Context(), chat.Session{
		ID:              sessionID,
		Title:           "Terminal settlement",
		AgentID:         "claude_code",
		DriverKind:      agentadapters.DriverKindACP,
		NativeSessionID: "",
		Workspace:       t.TempDir(),
		Status:          "completed",
		TurnsUsed:       0,
		Messages: []chat.Message{{
			ID:          "msg_terminal",
			Role:        "assistant",
			Status:      "completed",
			Content:     "terminal row committed before session settlement",
			CreatedAt:   time.Now().UTC(),
			CompletedAt: time.Now().UTC(),
		}},
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	lifecycle := apiHandler.agentChatLive.snapshotLifecycle(session.ID)
	if got := apiHandler.agentChatLive.registerTurn(lifecycle, func() {}); got != agentChatTurnAccepted {
		lifecycle.release()
		t.Fatalf("registerTurn() = %v, want accepted", got)
	}
	lifecycle.release()
	defer apiHandler.agentChatLive.clearTurn(session.ID)

	requestCtx, cancelRequest := context.WithCancel(t.Context())
	defer cancelRequest()
	request := httptest.NewRequest(http.MethodGet, "/hecate/v1/chat/sessions/"+session.ID+"/stream", nil).WithContext(requestCtx)
	recorder := newSynchronizedSSERecorder()
	streamDone := make(chan struct{})
	heartbeat <- time.Now()
	go func() {
		handler.ServeHTTP(recorder, request)
		close(streamDone)
	}()

	recorder.waitForFlush(t)
	if body := recorder.bodyString(); strings.Contains(body, "event: snapshot\n") || strings.Contains(body, "event: done\n") {
		t.Fatalf("stream exposed terminal state before settlement completed; body=%q", body)
	}
	select {
	case <-streamDone:
		t.Fatal("stream closed while the live turn was still settling")
	default:
	}
	apiHandler.agentChatLive.publishSession(session)
	// A delayed settlement marker from an older turn must not authorize this
	// turn's intermediate terminal row after the handler rereads the session.
	apiHandler.agentChatLive.publishSettledSession(session, "msg_previous_turn")
	apiHandler.agentChatLive.publishApprovalResolved(ChatApprovalResolvedEvent{
		ApprovalID: "approval_settlement_fence",
		SessionID:  session.ID,
		Status:     "approved",
		Path:       "operator",
	})
	recorder.waitForFlush(t)
	if body := recorder.bodyString(); strings.Contains(body, "event: snapshot\n") || strings.Contains(body, "event: done\n") {
		t.Fatalf("stream exposed a non-final terminal publication while the turn was settling; body=%q", body)
	}

	settled, err := baseStore.UpdateSession(t.Context(), session.ID, func(item *chat.Session) {
		item.NativeSessionID = "native_final"
		item.TurnsUsed = 1
	})
	if err != nil {
		t.Fatalf("UpdateSession() settlement metadata error = %v", err)
	}
	apiHandler.agentChatLive.publishSettledSession(settled, "msg_terminal")

	select {
	case <-streamDone:
	case <-time.After(3 * time.Second):
		t.Fatalf("stream did not close after final settlement publication; body=%q", recorder.bodyString())
	}
	body := recorder.bodyString()
	if !strings.Contains(body, "event: snapshot\n") || !strings.Contains(body, "event: done\n") {
		t.Fatalf("stream body = %q, want final snapshot and done", body)
	}
	if !strings.Contains(body, `"native_session_id":"native_final"`) || !strings.Contains(body, `"turns_used":1`) {
		t.Fatalf("stream body = %q, want fully settled session metadata", body)
	}
}

func TestAgentChatStreamKeepsBufferedTerminalDeferredAfterTurnClears(t *testing.T) {
	const sessionID = "chat_stream_buffered_terminal_after_clear"
	baseStore := chat.NewMemoryStore()
	store := &blockedNthAgentChatGetStore{
		Store:     baseStore,
		sessionID: sessionID,
		blockOn:   2,
		entered:   make(chan struct{}),
		release:   make(chan struct{}),
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatStore(store)
	handler := NewServer(logger, apiHandler)
	session, err := baseStore.Create(t.Context(), chat.Session{
		ID:         sessionID,
		Title:      "Buffered terminal settlement",
		AgentID:    "claude_code",
		DriverKind: agentadapters.DriverKindACP,
		Workspace:  t.TempDir(),
		Status:     "completed",
		Messages: []chat.Message{{
			ID:          "msg_terminal",
			Role:        "assistant",
			Status:      "completed",
			Content:     "intermediate terminal output",
			CreatedAt:   time.Now().UTC(),
			CompletedAt: time.Now().UTC(),
		}},
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	lifecycle := apiHandler.agentChatLive.snapshotLifecycle(session.ID)
	if got := apiHandler.agentChatLive.registerTurn(lifecycle, func() {}); got != agentChatTurnAccepted {
		lifecycle.release()
		t.Fatalf("registerTurn() = %v, want accepted", got)
	}
	lifecycle.release()

	requestCtx, cancelRequest := context.WithCancel(t.Context())
	defer cancelRequest()
	request := httptest.NewRequest(http.MethodGet, "/hecate/v1/chat/sessions/"+session.ID+"/stream", nil).WithContext(requestCtx)
	recorder := newSynchronizedSSERecorder()
	streamDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(recorder, request)
		close(streamDone)
	}()

	deadline := time.Now().Add(3 * time.Second)
	for store.calls.Load() < 1 {
		if time.Now().After(deadline) {
			apiHandler.agentChatLive.clearTurn(session.ID)
			t.Fatal("stream did not complete its initial authoritative read")
		}
		time.Sleep(time.Millisecond)
	}
	apiHandler.agentChatLive.publishSession(session)
	select {
	case <-store.entered:
	case <-time.After(3 * time.Second):
		apiHandler.agentChatLive.clearTurn(session.ID)
		t.Fatal("stream did not begin reconciling the buffered terminal update")
	}
	// The live event was selected while the turn existed, but its durable read
	// completes only after clearTurn. Settlement provenance, not registry state
	// at consumption time, must keep it from becoming an early done frame.
	apiHandler.agentChatLive.clearTurn(session.ID)
	close(store.release)
	apiHandler.agentChatLive.publishApprovalResolved(ChatApprovalResolvedEvent{
		ApprovalID: "approval_after_turn_clear",
		SessionID:  session.ID,
		Status:     "approved",
		Path:       "operator",
	})
	recorder.waitForFlush(t)
	if body := recorder.bodyString(); strings.Contains(body, "event: snapshot\n") || strings.Contains(body, "event: done\n") {
		t.Fatalf("stream exposed buffered terminal state after turn clear; body=%q", body)
	}

	settled, err := baseStore.UpdateSession(t.Context(), session.ID, func(item *chat.Session) {
		item.NativeSessionID = "native_settled"
		item.TurnsUsed = 1
	})
	if err != nil {
		t.Fatalf("UpdateSession() settlement metadata error = %v", err)
	}
	apiHandler.agentChatLive.publishSettledSession(settled, "msg_terminal")
	select {
	case <-streamDone:
	case <-time.After(3 * time.Second):
		t.Fatalf("stream did not close after settled publication; body=%q", recorder.bodyString())
	}
	body := recorder.bodyString()
	if !strings.Contains(body, `"native_session_id":"native_settled"`) ||
		!strings.Contains(body, "event: done\n") {
		t.Fatalf("stream body = %q, want settled snapshot and done", body)
	}
}

func TestAgentChatStreamDoesNotDeferHecateTerminalPublication(t *testing.T) {
	const sessionID = "chat_stream_hecate_terminal"
	store := chat.NewMemoryStore()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatStore(store)
	handler := NewServer(logger, apiHandler)
	session, err := store.Create(t.Context(), chat.Session{
		ID:        sessionID,
		Title:     "Hecate terminal",
		AgentID:   chat.DefaultAgentID,
		Workspace: t.TempDir(),
		Status:    "running",
		Messages: []chat.Message{{
			ID:        "msg_running",
			Role:      "assistant",
			Status:    "running",
			Content:   "working",
			CreatedAt: time.Now().UTC(),
		}},
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	lifecycle := apiHandler.agentChatLive.snapshotLifecycle(session.ID)
	if got := apiHandler.agentChatLive.registerTurn(lifecycle, func() {}); got != agentChatTurnAccepted {
		lifecycle.release()
		t.Fatalf("registerTurn() = %v, want accepted", got)
	}
	lifecycle.release()
	defer apiHandler.agentChatLive.clearTurn(session.ID)

	requestCtx, cancelRequest := context.WithCancel(t.Context())
	defer cancelRequest()
	request := httptest.NewRequest(http.MethodGet, "/hecate/v1/chat/sessions/"+session.ID+"/stream", nil).WithContext(requestCtx)
	recorder := newSynchronizedSSERecorder()
	streamDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(recorder, request)
		close(streamDone)
	}()
	recorder.waitForFlush(t)

	completed, err := store.UpdateSession(t.Context(), session.ID, func(item *chat.Session) {
		item.Status = "completed"
		item.Messages[0].Status = "completed"
		item.Messages[0].Content = "done"
		item.Messages[0].CompletedAt = time.Now().UTC()
	})
	if err != nil {
		t.Fatalf("UpdateSession() terminal error = %v", err)
	}
	apiHandler.agentChatLive.publishSession(completed)
	select {
	case <-streamDone:
	case <-time.After(3 * time.Second):
		t.Fatalf("Hecate stream waited for heartbeat instead of live terminal publication; body=%q", recorder.bodyString())
	}
	if body := recorder.bodyString(); !strings.Contains(body, "event: done\n") || !strings.Contains(body, `"status":"completed"`) {
		t.Fatalf("stream body = %q, want immediate Hecate terminal snapshot", body)
	}
}

func TestAgentChatStreamAcceptsAuthoritativeTerminalAfterClockMovesBackward(t *testing.T) {
	const sessionID = "chat_stream_clock_skew"
	baseStore := chat.NewMemoryStore()
	store := &skewedAgentChatGetStore{
		Store:             baseStore,
		runningUpdatedAt:  time.Now().UTC().Add(time.Hour),
		terminalUpdatedAt: time.Now().UTC().Add(-time.Hour),
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatStore(store)
	handler := NewServer(logger, apiHandler)
	session, err := baseStore.Create(t.Context(), chat.Session{
		ID:         sessionID,
		Title:      "Clock skew",
		AgentID:    "claude_code",
		DriverKind: agentadapters.DriverKindACP,
		Workspace:  t.TempDir(),
		Status:     "running",
		Messages: []chat.Message{{
			ID:        "msg_running",
			Role:      "assistant",
			Status:    "running",
			Content:   "working before clock adjustment",
			CreatedAt: time.Now().UTC(),
		}},
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	lifecycle := apiHandler.agentChatLive.snapshotLifecycle(session.ID)
	if got := apiHandler.agentChatLive.registerTurn(lifecycle, func() {}); got != agentChatTurnAccepted {
		lifecycle.release()
		t.Fatalf("registerTurn() = %v, want accepted", got)
	}
	lifecycle.release()
	defer apiHandler.agentChatLive.clearTurn(session.ID)

	requestCtx, cancelRequest := context.WithCancel(t.Context())
	defer cancelRequest()
	request := httptest.NewRequest(http.MethodGet, "/hecate/v1/chat/sessions/"+session.ID+"/stream", nil).WithContext(requestCtx)
	recorder := newSynchronizedSSERecorder()
	streamDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(recorder, request)
		close(streamDone)
	}()
	recorder.waitForFlush(t)

	completed, err := baseStore.UpdateSession(t.Context(), session.ID, func(item *chat.Session) {
		item.Status = "completed"
		item.Messages[0].Status = "completed"
		item.Messages[0].Content = "completed after clock adjustment"
		item.Messages[0].CompletedAt = time.Now().UTC()
	})
	if err != nil {
		t.Fatalf("UpdateSession() terminal error = %v", err)
	}
	apiHandler.agentChatLive.publishSettledSession(completed, "msg_running")
	select {
	case <-streamDone:
	case <-time.After(3 * time.Second):
		t.Fatalf("stream rejected the authoritative terminal state after clock skew; body=%q", recorder.bodyString())
	}
	body := recorder.bodyString()
	if !strings.Contains(body, "completed after clock adjustment") || !strings.Contains(body, "event: done\n") {
		t.Fatalf("stream body = %q, want terminal snapshot despite earlier updated_at", body)
	}
}

func TestAgentChatStreamRejectsOrphanedTrailingUserTurn(t *testing.T) {
	const sessionID = "chat_stream_orphaned_user"
	store := chat.NewMemoryStore()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatStore(store)
	handler := NewServer(logger, apiHandler)
	_, err := store.Create(t.Context(), chat.Session{
		ID:         sessionID,
		Title:      "Orphaned turn",
		AgentID:    "claude_code",
		DriverKind: agentadapters.DriverKindACP,
		Workspace:  t.TempDir(),
		Status:     "idle",
		Messages: []chat.Message{{
			ID:        "msg_user",
			Role:      "user",
			Status:    "completed",
			Content:   "message persisted before the runtime stopped",
			CreatedAt: time.Now().UTC(),
		}},
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/hecate/v1/chat/sessions/"+sessionID+"/stream", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("stream status = %d, want 409; body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), errCodeSessionNotRunning) {
		t.Fatalf("stream body = %q, want typed orphaned-turn error", recorder.Body.String())
	}
}

func TestAgentChatStreamRecognizesTurnAdmittedBetweenSubscribeAndInitialRead(t *testing.T) {
	const sessionID = "chat_stream_admission_after_subscribe"
	baseStore := chat.NewMemoryStore()
	store := &blockedBeforeAgentChatGetStore{
		Store:     baseStore,
		sessionID: sessionID,
		entered:   make(chan struct{}),
		release:   make(chan struct{}),
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatStore(store)
	handler := NewServer(logger, apiHandler)
	_, err := baseStore.Create(t.Context(), chat.Session{
		ID:         sessionID,
		Title:      "Admission after subscribe",
		AgentID:    "claude_code",
		DriverKind: agentadapters.DriverKindACP,
		Workspace:  t.TempDir(),
		Status:     "idle",
		CreatedAt:  time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	store.armed.Store(true)

	requestCtx, cancelRequest := context.WithCancel(t.Context())
	defer cancelRequest()
	request := httptest.NewRequest(http.MethodGet, "/hecate/v1/chat/sessions/"+sessionID+"/stream", nil).WithContext(requestCtx)
	recorder := newSynchronizedSSERecorder()
	streamDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(recorder, request)
		close(streamDone)
	}()
	select {
	case <-store.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not subscribe before its initial read")
	}

	lifecycle := apiHandler.agentChatLive.snapshotLifecycle(sessionID)
	if got := apiHandler.agentChatLive.registerTurn(lifecycle, func() {}); got != agentChatTurnAccepted {
		lifecycle.release()
		t.Fatalf("registerTurn() = %v, want accepted", got)
	}
	lifecycle.release()
	defer apiHandler.agentChatLive.clearTurn(sessionID)
	if _, err := baseStore.AppendMessage(t.Context(), sessionID, chat.Message{
		ID:        "msg_user",
		Role:      "user",
		Status:    "completed",
		Content:   "admitted after subscribe",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("AppendMessage() error = %v", err)
	}
	close(store.release)

	recorder.waitForFlush(t)
	if recorder.status != http.StatusOK {
		t.Fatalf("stream status = %d, want 200; body=%s", recorder.status, recorder.bodyString())
	}
	if body := recorder.bodyString(); !strings.Contains(body, "admitted after subscribe") || strings.Contains(body, errCodeSessionNotRunning) {
		t.Fatalf("stream body = %q, want admitted user snapshot without orphan error", body)
	}
	cancelRequest()
	select {
	case <-streamDone:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not stop after cancellation")
	}
}

func TestAgentChatStreamRejectsBufferedSnapshotOlderThanInitialRead(t *testing.T) {
	const sessionID = "chat_stream_stale_buffer"
	baseStore := chat.NewMemoryStore()
	store := &blockedBeforeAgentChatGetStore{
		Store:     baseStore,
		sessionID: sessionID,
		entered:   make(chan struct{}),
		release:   make(chan struct{}),
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatStore(store)
	handler := NewServer(logger, apiHandler)
	stale, err := baseStore.Create(t.Context(), chat.Session{
		ID:         sessionID,
		Title:      "Stale buffered snapshot",
		AgentID:    "claude_code",
		DriverKind: agentadapters.DriverKindACP,
		Workspace:  t.TempDir(),
		Status:     "completed",
		Messages: []chat.Message{{
			ID:          "msg_stale",
			Role:        "assistant",
			Status:      "completed",
			Content:     "stale terminal output",
			CreatedAt:   time.Now().UTC(),
			CompletedAt: time.Now().UTC(),
		}},
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	store.armed.Store(true)

	requestCtx, cancelRequest := context.WithCancel(t.Context())
	request := httptest.NewRequest(http.MethodGet, "/hecate/v1/chat/sessions/"+sessionID+"/stream", nil).WithContext(requestCtx)
	recorder := newSynchronizedSSERecorder()
	streamDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(recorder, request)
		close(streamDone)
	}()

	select {
	case <-store.entered:
	case <-time.After(3 * time.Second):
		cancelRequest()
		t.Fatal("stream did not block before its authoritative initial read")
	}
	apiHandler.agentChatLive.publishSession(stale)
	_, err = baseStore.UpdateSession(t.Context(), sessionID, func(item *chat.Session) {
		item.Status = "running"
		item.Messages[0].Status = "running"
		item.Messages[0].Content = "authoritative running output"
		item.Messages[0].CompletedAt = time.Time{}
	})
	if err != nil {
		cancelRequest()
		t.Fatalf("UpdateSession() authoritative state error = %v", err)
	}
	apiHandler.agentChatLive.publishApprovalResolved(ChatApprovalResolvedEvent{
		ApprovalID: "approval_ordering_fence",
		SessionID:  sessionID,
		Status:     "approved",
		Path:       "operator",
	})
	close(store.release)

	recorder.waitForFlush(t) // authoritative initial snapshot
	recorder.waitForFlush(t) // sentinel approval after the stale queued update
	body := recorder.bodyString()
	if !strings.Contains(body, "authoritative running output") {
		t.Fatalf("stream body = %q, want authoritative initial snapshot", body)
	}
	if strings.Contains(body, "stale terminal output") || strings.Contains(body, "event: done\n") {
		t.Fatalf("stream regressed to an older buffered snapshot; body=%q", body)
	}
	select {
	case <-streamDone:
		t.Fatal("stream closed after an older buffered terminal snapshot")
	default:
	}

	cancelRequest()
	select {
	case <-streamDone:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not stop after request cancellation")
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
