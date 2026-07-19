package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/hecatehq/hecate/internal/controlplane"
	"github.com/hecatehq/hecate/internal/modelcaps"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

type blockingIdempotencyProvider struct {
	*fakeProvider
	started     chan struct{}
	release     chan struct{}
	startedOnce sync.Once
	calls       atomic.Int32
}

type blockingSemanticCompactionProvider struct {
	*fakeProvider
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (provider *blockingSemanticCompactionProvider) Chat(ctx context.Context, req types.ChatRequest) (*types.ChatResponse, error) {
	if !strings.HasPrefix(req.RequestID, "compact_") {
		return provider.fakeProvider.Chat(ctx, req)
	}
	provider.once.Do(func() { close(provider.started) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-provider.release:
	}
	return &types.ChatResponse{
		ID:    "chatcmpl-semantic-summary",
		Model: req.Model,
		Choices: []types.ChatChoice{{
			Index:        0,
			Message:      types.Message{Role: "assistant", Content: "## Goal\n- Preserve the active request."},
			FinishReason: "stop",
		}},
	}, nil
}

type shortMessageRequestLeaseStore struct {
	chat.Store
	renewed chan struct{}
}

func (store *shortMessageRequestLeaseStore) MessageRequestLeaseTTL() time.Duration {
	return 30 * time.Millisecond
}

func (store *shortMessageRequestLeaseStore) RenewMessageRequest(ctx context.Context, req chat.RenewMessageRequestRequest) error {
	if err := store.Store.RenewMessageRequest(ctx, req); err != nil {
		return err
	}
	select {
	case store.renewed <- struct{}{}:
	default:
	}
	return nil
}

func (provider *blockingIdempotencyProvider) Chat(ctx context.Context, _ types.ChatRequest) (*types.ChatResponse, error) {
	provider.calls.Add(1)
	provider.startedOnce.Do(func() { close(provider.started) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-provider.release:
	}
	return provider.response, nil
}

type chatMessageHTTPResult struct {
	status int
	body   []byte
	err    error
}

type failingMessageRequestCommitStore struct {
	chat.Store
	err error
}

type cancelAfterKeyedCommitStore struct {
	chat.Store
	cancel context.CancelFunc
	once   sync.Once

	mu                      sync.Mutex
	userCommitContext       time.Time
	runningAssistantContext time.Time
	terminalContext         time.Time
	terminalSessionContext  time.Time
}

type cancelBeforeUnkeyedAppendStore struct {
	chat.Store
	cancel context.CancelFunc
	once   sync.Once
}

func (store *cancelBeforeUnkeyedAppendStore) AppendMessage(ctx context.Context, sessionID string, message chat.Message) (chat.Session, error) {
	if message.Role == "user" {
		store.once.Do(store.cancel)
		if err := ctx.Err(); err != nil {
			return chat.Session{}, err
		}
	}
	return store.Store.AppendMessage(ctx, sessionID, message)
}

func (store *cancelAfterKeyedCommitStore) CommitMessageRequest(ctx context.Context, lease chat.MessageRequestLease, message chat.Message) (chat.Session, error) {
	updated, err := store.Store.CommitMessageRequest(ctx, lease, message)
	if err != nil {
		return chat.Session{}, err
	}
	deadline, _ := ctx.Deadline()
	store.mu.Lock()
	store.userCommitContext = deadline
	store.mu.Unlock()
	store.once.Do(store.cancel)
	// Model the SQL store's former post-commit load: cancellation after the
	// durable commit must not make the admitted owner observe a failed commit.
	if err := ctx.Err(); err != nil {
		return chat.Session{}, err
	}
	return updated, nil
}

func (store *cancelAfterKeyedCommitStore) AppendMessage(ctx context.Context, sessionID string, message chat.Message) (chat.Session, error) {
	if err := ctx.Err(); err != nil {
		return chat.Session{}, err
	}
	updated, err := store.Store.AppendMessage(ctx, sessionID, message)
	if err == nil && message.Role == "assistant" && message.Status == "running" {
		deadline, _ := ctx.Deadline()
		store.mu.Lock()
		store.runningAssistantContext = deadline
		store.mu.Unlock()
	}
	return updated, err
}

func (store *cancelAfterKeyedCommitStore) UpdateMessage(ctx context.Context, sessionID, messageID string, update func(*chat.Message)) (chat.Session, error) {
	if err := ctx.Err(); err != nil {
		return chat.Session{}, err
	}
	updated, err := store.Store.UpdateMessage(ctx, sessionID, messageID, update)
	if err != nil {
		return chat.Session{}, err
	}
	for _, message := range updated.Messages {
		if message.ID != messageID || message.Role != "assistant" || message.CompletedAt.IsZero() || !isTerminalAgentChatMessageStatus(message.Status) {
			continue
		}
		deadline, _ := ctx.Deadline()
		store.mu.Lock()
		store.terminalContext = deadline
		store.mu.Unlock()
		break
	}
	return updated, nil
}

func (store *cancelAfterKeyedCommitStore) UpdateSession(ctx context.Context, sessionID string, update func(*chat.Session)) (chat.Session, error) {
	if err := ctx.Err(); err != nil {
		return chat.Session{}, err
	}
	updated, err := store.Store.UpdateSession(ctx, sessionID, update)
	if err == nil && updated.TurnsUsed > 0 {
		deadline, _ := ctx.Deadline()
		store.mu.Lock()
		store.terminalSessionContext = deadline
		store.mu.Unlock()
	}
	return updated, err
}

func (store *cancelAfterKeyedCommitStore) persistenceDeadlines() (time.Time, time.Time, time.Time, time.Time) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.userCommitContext, store.runningAssistantContext, store.terminalContext, store.terminalSessionContext
}

func isTerminalAgentChatMessageStatus(status string) bool {
	return status == "completed" || status == "failed" || status == "cancelled"
}

func (store failingMessageRequestCommitStore) CommitMessageRequest(context.Context, chat.MessageRequestLease, chat.Message) (chat.Session, error) {
	return chat.Session{}, store.err
}

func postChatMessage(url, body string) chatMessageHTTPResult {
	response, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		return chatMessageHTTPResult{err: err}
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	return chatMessageHTTPResult{status: response.StatusCode, body: payload, err: err}
}

func TestCreateChatMessageClientRequestIDConcurrentReplayDoesNotDispatchTwice(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &blockingIdempotencyProvider{
		fakeProvider: &fakeProvider{
			name: "openai",
			response: &types.ChatResponse{
				ID:    "chatcmpl-idempotent",
				Model: "gpt-4o-mini",
				Choices: []types.ChatChoice{{
					Index:        0,
					Message:      types.Message{Role: "assistant", Content: "once"},
					FinishReason: "stop",
				}},
			},
		},
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{provider}, config.Config{}, nil)
	store := chat.NewMemoryStore()
	const sessionID = "chat_client_request_id"
	if _, err := store.Create(t.Context(), chat.Session{
		ID:       sessionID,
		AgentID:  chat.DefaultAgentID,
		Provider: "openai",
		Model:    "gpt-4o-mini",
	}); err != nil {
		t.Fatalf("Create session: %v", err)
	}
	apiHandler.SetAgentChatStore(store)
	server := httptest.NewServer(NewServer(logger, apiHandler))
	t.Cleanup(server.Close)
	defer func() {
		select {
		case <-provider.release:
		default:
			close(provider.release)
		}
	}()

	endpoint := server.URL + "/hecate/v1/chat/sessions/" + sessionID + "/messages"
	requestBody := `{"content":"deliver exactly once","client_request_id":"queued-chat-123","tools_enabled":false}`
	firstDone := make(chan chatMessageHTTPResult, 1)
	go func() { firstDone <- postChatMessage(endpoint, requestBody) }()
	select {
	case <-provider.started:
	case <-time.After(3 * time.Second):
		t.Fatal("first request did not reach provider")
	}

	// The user row and idempotency key are committed before backing-turn provider dispatch,
	// so the concurrent tab receives the current transcript while the original
	// provider call remains in flight.
	secondDone := make(chan chatMessageHTTPResult, 1)
	go func() { secondDone <- postChatMessage(endpoint, requestBody) }()
	select {
	case second := <-secondDone:
		if second.err != nil || second.status != http.StatusOK {
			t.Fatalf("concurrent replay status=%d err=%v body=%s", second.status, second.err, second.body)
		}
		var response ChatSessionResponse
		if err := json.Unmarshal(second.body, &response); err != nil {
			t.Fatalf("decode concurrent replay: %v", err)
		}
		if len(response.Data.Messages) != 2 || response.Data.Messages[0].Content != "deliver exactly once" {
			t.Fatalf("concurrent replay messages = %+v, want authoritative in-flight transcript", response.Data.Messages)
		}
		if response.MessageRequest == nil || !response.MessageRequest.Replay || response.MessageRequest.CommittedMessageID != response.Data.Messages[0].ID {
			t.Fatalf("concurrent replay metadata = %+v, want replay with committed user message %q", response.MessageRequest, response.Data.Messages[0].ID)
		}
		for _, privateField := range []string{`"client_request_id":`, `"owner_token":`, `"payload_fingerprint":`} {
			if strings.Contains(string(second.body), privateField) {
				t.Fatalf("concurrent replay body exposes private idempotency field %q: %s", privateField, second.body)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent replay waited for provider instead of returning committed session")
	}

	mismatch := postChatMessage(endpoint, `{"content":"changed payload","client_request_id":"queued-chat-123","tools_enabled":false}`)
	if mismatch.err != nil || mismatch.status != http.StatusConflict {
		t.Fatalf("mismatched reuse status=%d err=%v body=%s", mismatch.status, mismatch.err, mismatch.body)
	}
	var mismatchPayload struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(mismatch.body, &mismatchPayload); err != nil {
		t.Fatalf("decode mismatch response: %v", err)
	}
	if mismatchPayload.Error.Type != errCodeClientRequestConflict {
		t.Fatalf("mismatch error type = %q, want %q", mismatchPayload.Error.Type, errCodeClientRequestConflict)
	}

	close(provider.release)
	select {
	case first := <-firstDone:
		if first.err != nil || first.status != http.StatusOK {
			t.Fatalf("first request status=%d err=%v body=%s", first.status, first.err, first.body)
		}
		var response ChatSessionResponse
		if err := json.Unmarshal(first.body, &response); err != nil {
			t.Fatalf("decode first request: %v", err)
		}
		if response.MessageRequest == nil || response.MessageRequest.Replay || len(response.Data.Messages) != 2 || response.MessageRequest.CommittedMessageID != response.Data.Messages[0].ID {
			t.Fatalf("first request metadata = %+v messages=%+v, want original committed user identity", response.MessageRequest, response.Data.Messages)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first request did not finish after provider release")
	}
	if got := provider.calls.Load(); got != 1 {
		t.Fatalf("provider calls = %d, want exactly 1", got)
	}
	authoritative, ok, err := store.Get(t.Context(), sessionID)
	if err != nil || !ok {
		t.Fatalf("Get session: found=%v err=%v", ok, err)
	}
	if len(authoritative.Messages) != 2 || authoritative.Messages[0].Role != "user" || authoritative.Messages[1].Role != "assistant" {
		t.Fatalf("authoritative transcript = %+v, want one user/assistant turn", authoritative.Messages)
	}
}

func TestCreateChatMessageClientRequestIDRenewsLeaseDuringSemanticCompaction(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &blockingSemanticCompactionProvider{
		fakeProvider: &fakeProvider{
			name: "openai",
			response: &types.ChatResponse{
				ID:    "chatcmpl-after-compaction",
				Model: "gpt-4o-mini",
				Choices: []types.ChatChoice{{
					Index:        0,
					Message:      types.Message{Role: "assistant", Content: "committed once"},
					FinishReason: "stop",
				}},
			},
		},
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{provider}, config.Config{}, controlplane.NewMemoryStore())
	store := &shortMessageRequestLeaseStore{Store: chat.NewMemoryStore(), renewed: make(chan struct{}, 8)}
	apiHandler.SetAgentChatStore(store)
	server := httptest.NewServer(NewServer(logger, apiHandler))
	t.Cleanup(server.Close)

	created := mustRequestJSON[ChatSessionResponse](newTaskTestClient(t, NewServer(logger, apiHandler)), http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"openai","model":"gpt-4o-mini"}`)
	for i := 0; i < chat.DefaultCompactMinMessages; i++ {
		role := "user"
		status := ""
		if i%2 == 1 {
			role = "assistant"
			status = "completed"
		}
		if _, err := store.AppendMessage(t.Context(), created.Data.ID, chat.Message{
			ID:      fmt.Sprintf("msg_seed_%02d", i),
			Role:    role,
			Status:  status,
			Content: fmt.Sprintf("seed transcript %02d", i),
		}); err != nil {
			t.Fatalf("AppendMessage seed %d: %v", i, err)
		}
	}

	resultCh := make(chan chatMessageHTTPResult, 1)
	go func() {
		resultCh <- postChatMessage(
			server.URL+"/hecate/v1/chat/sessions/"+created.Data.ID+"/messages",
			`{"execution_mode":"hecate_task","tools_enabled":false,"content":"after a slow compaction","client_request_id":"queued-slow-compaction"}`,
		)
	}()
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("semantic compaction did not start")
	}
	select {
	case <-store.renewed:
	case <-time.After(time.Second):
		t.Fatal("keyed message lease was not renewed while semantic compaction was blocked")
	}
	close(provider.release)
	select {
	case result := <-resultCh:
		if result.err != nil || result.status != http.StatusOK {
			t.Fatalf("chat message result = status %d err %v body=%s", result.status, result.err, result.body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("chat message did not finish after semantic compaction resumed")
	}

	persisted, ok, err := store.Get(t.Context(), created.Data.ID)
	if err != nil || !ok {
		t.Fatalf("Get: found=%v err=%v", ok, err)
	}
	var submitted int
	for _, message := range persisted.Messages {
		if message.Role == "user" && message.Content == "after a slow compaction" {
			submitted++
		}
	}
	if submitted != 1 || provider.CallCount() != 1 {
		t.Fatalf("submitted rows = %d, model calls after compaction = %d; want one each", submitted, provider.CallCount())
	}
}

func TestCreateChatMessageClientRequestIDValidation(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{name: "openai", response: &types.ChatResponse{}}}, config.Config{}, nil)
	store := chat.NewMemoryStore()
	if _, err := store.Create(t.Context(), chat.Session{ID: "chat_invalid_client_request", AgentID: chat.DefaultAgentID}); err != nil {
		t.Fatalf("Create session: %v", err)
	}
	apiHandler.SetAgentChatStore(store)
	recorder := performRequest(t, NewServer(logger, apiHandler), http.MethodPost,
		"/hecate/v1/chat/sessions/chat_invalid_client_request/messages",
		`{"content":"hello","client_request_id":"contains a space","tools_enabled":false}`,
	)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestCreateChatMessageClientRequestIDReleasesPreCommitRejection(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:    "chatcmpl-after-release",
			Model: "gpt-4o-mini",
			Choices: []types.ChatChoice{{
				Index:        0,
				Message:      types.Message{Role: "assistant", Content: "accepted"},
				FinishReason: "stop",
			}},
		},
	}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{provider}, config.Config{}, nil)
	store := chat.NewMemoryStore()
	const sessionID = "chat_client_request_release"
	if _, err := store.Create(t.Context(), chat.Session{
		ID:       sessionID,
		AgentID:  chat.DefaultAgentID,
		Provider: "openai",
		Model:    "gpt-4o-mini",
	}); err != nil {
		t.Fatalf("Create session: %v", err)
	}
	apiHandler.SetAgentChatStore(store)
	handler := NewServer(logger, apiHandler)

	rejected := performRequest(t, handler, http.MethodPost,
		"/hecate/v1/chat/sessions/"+sessionID+"/messages",
		`{"content":"","client_request_id":"queued-chat-released","tools_enabled":false}`,
	)
	if rejected.Code != http.StatusBadRequest {
		t.Fatalf("pre-commit rejection status = %d, want 400; body=%s", rejected.Code, rejected.Body.String())
	}
	accepted := performRequest(t, handler, http.MethodPost,
		"/hecate/v1/chat/sessions/"+sessionID+"/messages",
		`{"content":"valid after rejection","client_request_id":"queued-chat-released","tools_enabled":false}`,
	)
	if accepted.Code != http.StatusOK {
		t.Fatalf("request after release status = %d, want 200; body=%s", accepted.Code, accepted.Body.String())
	}
	if got := provider.CallCount(); got != 1 {
		t.Fatalf("provider calls = %d, want 1 after released pre-commit rejection", got)
	}
}

func TestCreateHecateChatMessageStaleRequestLeaseDoesNotRecordRunStarted(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{name: "openai"}}, config.Config{}, nil)
	baseStore := chat.NewMemoryStore()
	const sessionID = "chat_commit_failure_trace"
	if _, err := baseStore.Create(t.Context(), chat.Session{
		ID:           sessionID,
		AgentID:      chat.DefaultAgentID,
		Workspace:    t.TempDir(),
		Provider:     "openai",
		Model:        "gpt-4o-mini",
		Capabilities: types.ModelCapabilities{ToolCalling: modelcaps.ToolCallingParallel, Streaming: true, Source: modelcaps.SourceCatalog},
	}); err != nil {
		t.Fatalf("Create session: %v", err)
	}
	apiHandler.SetAgentChatStore(failingMessageRequestCommitStore{
		Store: baseStore,
		err:   chat.ErrMessageRequestLeaseInvalid,
	})

	recorder := performRequest(t, NewServer(logger, apiHandler), http.MethodPost,
		"/hecate/v1/chat/sessions/"+sessionID+"/messages",
		`{"execution_mode":"hecate_task","content":"do not dispatch","client_request_id":"queued-failing-commit"}`,
	)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("stale-owner commit status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}

	traces := apiHandler.tracer.List(0)
	if len(traces) == 0 {
		t.Fatal("stale-owner commit produced no request trace")
	}
	for _, trace := range traces {
		for _, event := range trace.Events() {
			if event.Name == telemetry.EventAgentChatTurnStarted {
				t.Fatalf("stale-owner commit recorded phantom %q event: %+v", event.Name, event)
			}
		}
	}
	persisted, ok, err := baseStore.Get(t.Context(), sessionID)
	if err != nil || !ok {
		t.Fatalf("Get session: found=%v err=%v", ok, err)
	}
	if len(persisted.Messages) != 0 {
		t.Fatalf("stale-owner commit transcript = %+v, want no admitted turn", persisted.Messages)
	}
}

func TestKeyedHecateTaskTurnCompletesAfterRequestDisconnect(t *testing.T) {
	logger := quietLogger()
	provider := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:    "chatcmpl-keyed-hecate-disconnect",
			Model: "gpt-4o-mini",
			Choices: []types.ChatChoice{{
				Message:      types.Message{Role: "assistant", Content: "completed once"},
				FinishReason: "stop",
			}},
		},
	}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{provider}, config.Config{}, controlplane.NewMemoryStore())
	baseStore := chat.NewMemoryStore()
	const sessionID = "chat_keyed_hecate_disconnect"
	if _, err := baseStore.Create(t.Context(), chat.Session{
		ID:           sessionID,
		AgentID:      chat.DefaultAgentID,
		Workspace:    t.TempDir(),
		Provider:     "openai",
		Model:        "gpt-4o-mini",
		Capabilities: types.ModelCapabilities{ToolCalling: modelcaps.ToolCallingParallel, Streaming: true, Source: modelcaps.SourceCatalog},
	}); err != nil {
		t.Fatalf("Create session: %v", err)
	}
	requestCtx, cancelRequest := context.WithCancel(t.Context())
	store := &cancelAfterKeyedCommitStore{Store: baseStore, cancel: cancelRequest}
	apiHandler.SetAgentChatStore(store)
	handler := NewServer(logger, apiHandler)
	body := `{"execution_mode":"hecate_task","content":"complete despite disconnect","client_request_id":"queued-hecate-disconnect"}`
	serveCancelledKeyedChatRequest(t, handler, requestCtx, sessionID, body)

	stored := assertDisconnectedKeyedTurnTerminal(t, baseStore, store, sessionID, "completed", "completed once")
	if stored.TaskID == "" || stored.LatestRunID == "" {
		t.Fatalf("Hecate task linkage = task %q run %q, want durable backing execution", stored.TaskID, stored.LatestRunID)
	}
	if got := provider.CallCount(); got != 1 {
		t.Fatalf("provider calls = %d, want one server-owned task dispatch", got)
	}
	assertKeyedChatReplayDoesNotDispatch(t, handler, sessionID, body, func() int { return provider.CallCount() })
}

func TestUnkeyedUserAppendRemainsRequestBound(t *testing.T) {
	logger := quietLogger()
	provider := &fakeProvider{name: "openai", response: &types.ChatResponse{}}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{provider}, config.Config{}, nil)
	baseStore := chat.NewMemoryStore()
	const sessionID = "chat_unkeyed_request_cancel"
	if _, err := baseStore.Create(t.Context(), chat.Session{
		ID:       sessionID,
		AgentID:  chat.DefaultAgentID,
		Provider: "openai",
		Model:    "gpt-4o-mini",
	}); err != nil {
		t.Fatalf("Create session: %v", err)
	}
	requestCtx, cancelRequest := context.WithCancel(t.Context())
	apiHandler.SetAgentChatStore(&cancelBeforeUnkeyedAppendStore{Store: baseStore, cancel: cancelRequest})
	request := httptest.NewRequest(
		http.MethodPost,
		"/hecate/v1/chat/sessions/"+sessionID+"/messages",
		strings.NewReader(`{"execution_mode":"hecate_task","tools_enabled":false,"content":"do not detach this append"}`),
	).WithContext(requestCtx)
	recorder := httptest.NewRecorder()
	NewServer(logger, apiHandler).ServeHTTP(recorder, request)

	if !errors.Is(requestCtx.Err(), context.Canceled) {
		t.Fatalf("request context error = %v, want cancellation at unkeyed user append", requestCtx.Err())
	}
	stored, ok, err := baseStore.Get(t.Context(), sessionID)
	if err != nil || !ok {
		t.Fatalf("Get session: found=%v err=%v", ok, err)
	}
	if len(stored.Messages) != 0 {
		t.Fatalf("messages = %+v, want no unkeyed row after request cancellation", stored.Messages)
	}
	if got := provider.CallCount(); got != 0 {
		t.Fatalf("provider calls = %d, want no dispatch", got)
	}
}

func TestKeyedExternalAgentTurnCancelsAndFinalizesAfterRequestDisconnect(t *testing.T) {
	logger := quietLogger()
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{name: "openai", response: &types.ChatResponse{}}}, config.Config{}, nil)
	baseStore := chat.NewMemoryStore()
	const sessionID = "chat_keyed_external_disconnect"
	if _, err := baseStore.Create(t.Context(), chat.Session{
		ID:         sessionID,
		AgentID:    "codex",
		DriverKind: agentadapters.DriverKindACP,
		Workspace:  t.TempDir(),
	}); err != nil {
		t.Fatalf("Create session: %v", err)
	}
	requestCtx, cancelRequest := context.WithCancel(t.Context())
	store := &cancelAfterKeyedCommitStore{Store: baseStore, cancel: cancelRequest}
	apiHandler.SetAgentChatStore(store)
	runner := &fakeAgentChatRunner{waitForCancel: true}
	apiHandler.SetAgentChatRunner(runner)
	handler := NewServer(logger, apiHandler)
	body := `{"execution_mode":"external_agent","content":"cancel after commit","client_request_id":"queued-external-disconnect"}`
	serveCancelledKeyedChatRequest(t, handler, requestCtx, sessionID, body)

	assertDisconnectedKeyedTurnTerminal(t, baseStore, store, sessionID, "cancelled", "started")
	if got := len(runner.runRequests); got != 1 {
		t.Fatalf("ACP run requests = %d, want one dispatch", got)
	}
	assertKeyedChatReplayDoesNotDispatch(t, handler, sessionID, body, func() int { return len(runner.runRequests) })
}

func TestKeyedDirectModelTurnFinalizesWhenRequestDisconnectsAfterCommit(t *testing.T) {
	logger := quietLogger()
	provider := &blockingIdempotencyProvider{
		fakeProvider: &fakeProvider{
			name:     "openai",
			response: &types.ChatResponse{},
		},
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{provider}, config.Config{}, nil)
	baseStore := chat.NewMemoryStore()
	const sessionID = "chat_keyed_direct_disconnect"
	if _, err := baseStore.Create(t.Context(), chat.Session{
		ID:       sessionID,
		AgentID:  chat.DefaultAgentID,
		Provider: "openai",
		Model:    "gpt-4o-mini",
	}); err != nil {
		t.Fatalf("Create session: %v", err)
	}
	requestCtx, cancelRequest := context.WithCancel(t.Context())
	store := &cancelAfterKeyedCommitStore{Store: baseStore, cancel: cancelRequest}
	apiHandler.SetAgentChatStore(store)
	handler := NewServer(logger, apiHandler)
	body := `{"execution_mode":"hecate_task","tools_enabled":false,"content":"cancel after commit","client_request_id":"queued-direct-disconnect"}`
	serveCancelledKeyedChatRequest(t, handler, requestCtx, sessionID, body)

	assertDisconnectedKeyedTurnTerminal(t, baseStore, store, sessionID, "cancelled", "model request cancelled")
	assertKeyedChatReplayDoesNotDispatch(t, handler, sessionID, body, func() int { return int(provider.calls.Load()) })
}

func serveCancelledKeyedChatRequest(t *testing.T, handler http.Handler, requestCtx context.Context, sessionID, body string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/hecate/v1/chat/sessions/"+sessionID+"/messages", strings.NewReader(body)).WithContext(requestCtx)
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(recorder, request)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("message handler did not settle after request disconnect")
	}
	if requestCtx.Err() == nil {
		t.Fatal("test store did not cancel the request after keyed user commit")
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("disconnected response body = %q, want no response", recorder.Body.String())
	}
}

func assertDisconnectedKeyedTurnTerminal(t *testing.T, baseStore chat.Store, store *cancelAfterKeyedCommitStore, sessionID, wantStatus, wantContent string) chat.Session {
	t.Helper()
	stored, ok, err := baseStore.Get(t.Context(), sessionID)
	if err != nil || !ok {
		t.Fatalf("Get session: found=%v err=%v", ok, err)
	}
	if len(stored.Messages) != 2 {
		t.Fatalf("messages = %+v, want exactly one user and one assistant", stored.Messages)
	}
	assistant := stored.Messages[1]
	if assistant.Role != "assistant" || assistant.Status != wantStatus || !strings.Contains(assistant.Content, wantContent) || assistant.CompletedAt.IsZero() {
		t.Fatalf("assistant = %+v, want terminal %s containing %q", assistant, wantStatus, wantContent)
	}
	if stored.TurnsUsed != 1 {
		t.Fatalf("turns_used = %d, want 1", stored.TurnsUsed)
	}
	commitDeadline, runningDeadline, terminalDeadline, sessionDeadline := store.persistenceDeadlines()
	for name, deadline := range map[string]time.Time{
		"keyed user commit":  commitDeadline,
		"running assistant":  runningDeadline,
		"terminal assistant": terminalDeadline,
		"terminal session":   sessionDeadline,
	} {
		if deadline.IsZero() || time.Until(deadline) <= 0 || time.Until(deadline) > agentChatTerminalWriteTimeout+time.Second {
			t.Fatalf("%s persistence deadline = %s, want live bounded detached context", name, deadline)
		}
	}
	return stored
}

func assertKeyedChatReplayDoesNotDispatch(t *testing.T, handler http.Handler, sessionID, body string, dispatchCount func() int) {
	t.Helper()
	before := dispatchCount()
	replay := performRequest(t, handler, http.MethodPost, "/hecate/v1/chat/sessions/"+sessionID+"/messages", body)
	if replay.Code != http.StatusOK {
		t.Fatalf("replay status = %d, want 200; body=%s", replay.Code, replay.Body.String())
	}
	var response ChatSessionResponse
	if err := json.Unmarshal(replay.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode replay: %v", err)
	}
	if response.MessageRequest == nil || !response.MessageRequest.Replay || response.MessageRequest.CommittedMessageID == "" {
		t.Fatalf("replay metadata = %+v, want committed replay", response.MessageRequest)
	}
	if got := dispatchCount(); got != before {
		t.Fatalf("dispatches after replay = %d, want unchanged %d", got, before)
	}
}
