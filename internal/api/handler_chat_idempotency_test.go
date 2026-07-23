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

type controlledDisconnectAgentChatRunner struct {
	*fakeAgentChatRunner

	started          chan struct{}
	progress         chan struct{}
	progressEmitted  chan struct{}
	complete         chan struct{}
	cancelled        chan error
	startedOnce      sync.Once
	progressOnce     sync.Once
	progressEmitOnce sync.Once
	completeOnce     sync.Once
	cancellationOnce sync.Once
	runCalls         atomic.Int32
}

func newControlledDisconnectAgentChatRunner() *controlledDisconnectAgentChatRunner {
	return &controlledDisconnectAgentChatRunner{
		fakeAgentChatRunner: &fakeAgentChatRunner{},
		started:             make(chan struct{}),
		progress:            make(chan struct{}),
		progressEmitted:     make(chan struct{}),
		complete:            make(chan struct{}),
		cancelled:           make(chan error, 1),
	}
}

func (runner *controlledDisconnectAgentChatRunner) Run(ctx context.Context, req agentadapters.RunRequest) (agentadapters.RunResult, error) {
	startedAt := time.Now().UTC()
	runner.runCalls.Add(1)
	runner.startedOnce.Do(func() { close(runner.started) })

	select {
	case <-ctx.Done():
		runner.recordCancellation(ctx.Err())
		return runner.fakeAgentChatRunner.result(req, "", startedAt, 1), ctx.Err()
	case <-runner.progress:
	}

	const progress = "continued after disconnect"
	if req.OnOutput != nil {
		req.OnOutput(progress)
	}
	if req.OnActivity != nil {
		req.OnActivity(agentadapters.Activity{
			ID:     "tool:detached",
			Type:   "tool_call",
			Status: "completed",
			Kind:   "execute",
			Title:  "Detached progress",
		})
	}
	runner.progressEmitOnce.Do(func() { close(runner.progressEmitted) })

	select {
	case <-ctx.Done():
		runner.recordCancellation(ctx.Err())
		return runner.fakeAgentChatRunner.result(req, progress, startedAt, 1), ctx.Err()
	case <-runner.complete:
		return runner.fakeAgentChatRunner.result(req, "completed after disconnect", startedAt, 0), nil
	}
}

func (runner *controlledDisconnectAgentChatRunner) requestProgress() {
	runner.progressOnce.Do(func() { close(runner.progress) })
}

func (runner *controlledDisconnectAgentChatRunner) completeRun() {
	runner.requestProgress()
	runner.completeOnce.Do(func() { close(runner.complete) })
}

func (runner *controlledDisconnectAgentChatRunner) recordCancellation(err error) {
	runner.cancellationOnce.Do(func() { runner.cancelled <- err })
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
	userCommitContext       persistenceContextObservation
	runningAssistantContext persistenceContextObservation
	terminalContext         persistenceContextObservation
	terminalSessionContext  persistenceContextObservation
}

type persistenceContextObservation struct {
	err        error
	observedAt time.Time
	deadline   time.Time
}

func observePersistenceContext(ctx context.Context) persistenceContextObservation {
	deadline, _ := ctx.Deadline()
	return persistenceContextObservation{
		err:        ctx.Err(),
		observedAt: time.Now(),
		deadline:   deadline,
	}
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
	store.mu.Lock()
	store.userCommitContext = observePersistenceContext(ctx)
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
		store.mu.Lock()
		store.runningAssistantContext = observePersistenceContext(ctx)
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
		store.mu.Lock()
		store.terminalContext = observePersistenceContext(ctx)
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
		store.mu.Lock()
		store.terminalSessionContext = observePersistenceContext(ctx)
		store.mu.Unlock()
	}
	return updated, err
}

func (store *cancelAfterKeyedCommitStore) persistenceContexts() (persistenceContextObservation, persistenceContextObservation, persistenceContextObservation, persistenceContextObservation) {
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

func TestKeyedExternalAgentTurnContinuesAndReplaysAfterRequestDisconnect(t *testing.T) {
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
	runner := newControlledDisconnectAgentChatRunner()
	apiHandler.SetAgentChatRunner(runner)
	handler := NewServer(logger, apiHandler)
	body := `{"execution_mode":"external_agent","content":"cancel after commit","client_request_id":"queued-external-disconnect"}`
	recorder, done := startCancelledKeyedChatRequest(handler, requestCtx, sessionID, body)
	t.Cleanup(func() {
		runner.completeRun()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	})

	awaitSignal(t, runner.started, "external agent runner did not start after request disconnect")
	assertRequestDisconnected(t, requestCtx)
	select {
	case err := <-runner.cancelled:
		t.Fatalf("external agent run context cancelled with request: %v", err)
	default:
	}

	replay := assertKeyedChatReplayDoesNotDispatch(t, handler, sessionID, body, func() int { return int(runner.runCalls.Load()) })
	if len(replay.Data.Messages) != 2 {
		t.Fatalf("active replay messages = %+v, want exactly one user and one assistant", replay.Data.Messages)
	}
	assistant := replay.Data.Messages[1]
	if assistant.Role != "assistant" || assistant.Status != "running" || assistant.CompletedAt != "" {
		t.Fatalf("active replay assistant = %+v, want running authoritative transcript", assistant)
	}

	runner.requestProgress()
	awaitSignal(t, runner.progressEmitted, "external agent runner did not emit progress")
	waitForDisconnectedExternalAgentProgress(t, baseStore, sessionID)
	select {
	case err := <-runner.cancelled:
		t.Fatalf("external agent run context cancelled while persisting detached progress: %v", err)
	default:
	}

	runner.completeRun()
	awaitDisconnectedChatHandler(t, done, recorder)
	assertDisconnectedKeyedTurnTerminal(t, baseStore, store, sessionID, "completed", "completed after disconnect")
	if got := runner.runCalls.Load(); got != 1 {
		t.Fatalf("ACP run requests = %d, want one dispatch", got)
	}
	assertKeyedChatReplayDoesNotDispatch(t, handler, sessionID, body, func() int { return int(runner.runCalls.Load()) })
}

func TestKeyedExternalAgentTurnOperatorStopAfterRequestDisconnect(t *testing.T) {
	logger := quietLogger()
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{name: "openai", response: &types.ChatResponse{}}}, config.Config{}, nil)
	baseStore := chat.NewMemoryStore()
	const sessionID = "chat_keyed_external_stop_after_disconnect"
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
	runner := newControlledDisconnectAgentChatRunner()
	apiHandler.SetAgentChatRunner(runner)
	handler := NewServer(logger, apiHandler)
	body := `{"execution_mode":"external_agent","content":"stop after disconnect","client_request_id":"queued-external-stop-after-disconnect"}`
	recorder, done := startCancelledKeyedChatRequest(handler, requestCtx, sessionID, body)
	t.Cleanup(func() {
		runner.completeRun()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	})

	awaitSignal(t, runner.started, "external agent runner did not start after request disconnect")
	assertRequestDisconnected(t, requestCtx)
	stop := performRequest(t, handler, http.MethodPost, "/hecate/v1/chat/sessions/"+sessionID+"/cancel", `{}`)
	if stop.Code != http.StatusAccepted {
		t.Fatalf("operator Stop status = %d, want 202; body=%s", stop.Code, stop.Body.String())
	}
	select {
	case err := <-runner.cancelled:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("external agent Stop context error = %v, want context cancellation", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("operator Stop did not cancel detached external agent run")
	}
	awaitDisconnectedChatHandler(t, done, recorder)

	assertDisconnectedKeyedTurnTerminal(t, baseStore, store, sessionID, "cancelled", "")
	if got := runner.runCalls.Load(); got != 1 {
		t.Fatalf("ACP run requests = %d, want one dispatch", got)
	}
	assertKeyedChatReplayDoesNotDispatch(t, handler, sessionID, body, func() int { return int(runner.runCalls.Load()) })
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
	recorder, done := startCancelledKeyedChatRequest(handler, requestCtx, sessionID, body)
	awaitDisconnectedChatHandler(t, done, recorder)
	assertRequestDisconnected(t, requestCtx)
}

func startCancelledKeyedChatRequest(handler http.Handler, requestCtx context.Context, sessionID, body string) (*httptest.ResponseRecorder, <-chan struct{}) {
	request := httptest.NewRequest(http.MethodPost, "/hecate/v1/chat/sessions/"+sessionID+"/messages", strings.NewReader(body)).WithContext(requestCtx)
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(recorder, request)
		close(done)
	}()
	return recorder, done
}

func awaitDisconnectedChatHandler(t *testing.T, done <-chan struct{}, recorder *httptest.ResponseRecorder) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("message handler did not settle after request disconnect")
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("disconnected response body = %q, want no response", recorder.Body.String())
	}
}

func assertRequestDisconnected(t *testing.T, requestCtx context.Context) {
	t.Helper()
	if !errors.Is(requestCtx.Err(), context.Canceled) {
		t.Fatalf("request context error = %v, want cancellation after keyed user commit", requestCtx.Err())
	}
}

func awaitSignal(t *testing.T, signal <-chan struct{}, failure string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal(failure)
	}
}

func waitForDisconnectedExternalAgentProgress(t *testing.T, store chat.Store, sessionID string) chat.Session {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		stored, ok, err := store.Get(t.Context(), sessionID)
		if err != nil {
			t.Fatalf("Get session while waiting for detached progress: %v", err)
		}
		if ok && len(stored.Messages) == 2 {
			assistant := stored.Messages[1]
			activityFound := false
			for _, activity := range assistant.Activities {
				if activity.ID == "tool:detached" && activity.Status == "completed" && activity.Title == "Detached progress" {
					activityFound = true
					break
				}
			}
			if assistant.Status == "running" && strings.Contains(assistant.Content, "continued after disconnect") && activityFound {
				return stored
			}
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("detached external agent progress was not persisted for session %q", sessionID)
		}
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
	commitContext, runningContext, terminalContext, sessionContext := store.persistenceContexts()
	for name, observed := range map[string]persistenceContextObservation{
		"keyed user commit":  commitContext,
		"running assistant":  runningContext,
		"terminal assistant": terminalContext,
		"terminal session":   sessionContext,
	} {
		remaining := observed.deadline.Sub(observed.observedAt)
		if observed.err != nil ||
			observed.observedAt.IsZero() ||
			observed.deadline.IsZero() ||
			remaining <= 0 ||
			remaining > agentChatTerminalWriteTimeout+time.Second {
			t.Fatalf("%s persistence context = %+v (remaining %s), want live bounded detached context at call time", name, observed, remaining)
		}
	}
	return stored
}

func assertKeyedChatReplayDoesNotDispatch(t *testing.T, handler http.Handler, sessionID, body string, dispatchCount func() int) ChatSessionResponse {
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
	return response
}
