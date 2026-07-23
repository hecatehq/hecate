package api

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/gateway"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestExternalAgentTurnOutcomeCompletedUsesAdapterTimesAndPlaceholder(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	completed := started.Add(2 * time.Second)

	outcome := newExternalAgentTurnOutcome("Codex", agentadapters.RunResult{
		StartedAt:   started,
		CompletedAt: completed,
	}, nil, nil, time.Time{}, time.Time{})

	if outcome.Status != "completed" {
		t.Fatalf("status = %q, want completed", outcome.Status)
	}
	if outcome.Output != "(agent completed without output)" {
		t.Fatalf("output = %q, want empty-output placeholder", outcome.Output)
	}
	if outcome.ResultLabel != telemetry.ResultSuccess || outcome.DurationMS != 2000 {
		t.Fatalf("result/duration = %q/%d, want success/2000", outcome.ResultLabel, outcome.DurationMS)
	}
}

func TestExternalAgentTurnOutcomeFailureAppendsNormalizedError(t *testing.T) {
	t.Parallel()

	err := errors.New(`{"message":"Internal error: tool failed","data":{"errorKind":"tool"}}`)
	outcome := newExternalAgentTurnOutcome("Codex", agentadapters.RunResult{
		Output: "partial output",
	}, err, nil, time.Unix(10, 0).UTC(), time.Unix(11, 0).UTC())

	wantErr := "Codex error (tool): tool failed"
	if outcome.Status != "failed" || outcome.ErrorText != wantErr || outcome.DisplayErr != wantErr {
		t.Fatalf("outcome = %+v, want failed with normalized error %q", outcome, wantErr)
	}
	if outcome.Output != "partial output\n\n"+wantErr {
		t.Fatalf("output = %q, want partial output plus normalized error", outcome.Output)
	}
	if outcome.ResultLabel != telemetry.ResultError {
		t.Fatalf("result label = %q, want error", outcome.ResultLabel)
	}
}

func TestExternalAgentTurnOutcomeCancelledDoesNotInventOutput(t *testing.T) {
	t.Parallel()

	outcome := newExternalAgentTurnOutcome("Codex", agentadapters.RunResult{}, errors.New("boom"), context.Canceled, time.Unix(10, 0).UTC(), time.Unix(11, 0).UTC())

	if outcome.Status != "cancelled" {
		t.Fatalf("status = %q, want cancelled", outcome.Status)
	}
	if outcome.Output != "" || outcome.ErrorText != "" {
		t.Fatalf("output/error = %q/%q, want empty output/error on cancellation", outcome.Output, outcome.ErrorText)
	}
	if outcome.ResultLabel != telemetry.ResultError {
		t.Fatalf("result label = %q, want error", outcome.ResultLabel)
	}
}

func TestExternalAgentTurnOutcomeRuntimeCancellationIsCancelled(t *testing.T) {
	t.Parallel()

	outcome := newExternalAgentTurnOutcome(
		"Claude Code",
		agentadapters.RunResult{Output: "partial output"},
		context.Canceled,
		nil,
		time.Unix(10, 0).UTC(),
		time.Unix(11, 0).UTC(),
	)

	if outcome.Status != "cancelled" {
		t.Fatalf("status = %q, want cancelled", outcome.Status)
	}
	if outcome.Output != "partial output" {
		t.Fatalf("output = %q, want partial output preserved", outcome.Output)
	}
	if outcome.ErrorText != "" {
		t.Fatalf("error text = %q, want empty for cancellation", outcome.ErrorText)
	}
}

func TestExternalAgentTurnOutcomeDeadlineExceededWinsOverRuntimeCancellation(t *testing.T) {
	t.Parallel()

	outcome := newExternalAgentTurnOutcome(
		"Claude Code",
		agentadapters.RunResult{Output: "partial output"},
		context.Canceled,
		context.DeadlineExceeded,
		time.Unix(10, 0).UTC(),
		time.Unix(11, 0).UTC(),
	)

	if outcome.Status != "failed" {
		t.Fatalf("status = %q, want failed", outcome.Status)
	}
	if outcome.Output != "partial output\n\ncontext deadline exceeded" {
		t.Fatalf("output = %q, want partial output plus normalized timeout failure", outcome.Output)
	}
	if outcome.ErrorText != "context deadline exceeded" {
		t.Fatalf("error text = %q, want normalized runtime failure", outcome.ErrorText)
	}
	if outcome.ResultLabel != telemetry.ResultError {
		t.Fatalf("result label = %q, want error", outcome.ResultLabel)
	}
}

func TestDirectModelTurnOutcomeCompletedUsesChoiceOrPlaceholder(t *testing.T) {
	t.Parallel()

	outcome := newDirectModelTurnOutcome(&gateway.ChatResult{
		Response: &types.ChatResponse{Choices: []types.ChatChoice{{Message: types.Message{Content: "  hello  "}}}},
	}, nil, nil)
	if outcome.Status != "completed" || outcome.Output != "hello" || outcome.ErrorText != "" {
		t.Fatalf("outcome = %+v, want completed hello", outcome)
	}

	outcome = newDirectModelTurnOutcome(&gateway.ChatResult{Response: &types.ChatResponse{}}, nil, nil)
	if outcome.Output != "(model completed without output)" {
		t.Fatalf("empty output = %q, want model placeholder", outcome.Output)
	}
}

func TestDirectModelTurnOutcomeFailureAndCancellation(t *testing.T) {
	t.Parallel()

	err := errors.New("provider failed")
	outcome := newDirectModelTurnOutcome(nil, err, nil)
	if outcome.Status != "failed" || outcome.Output != "provider failed" || outcome.ErrorText != "provider failed" {
		t.Fatalf("failure outcome = %+v, want failed provider error", outcome)
	}

	outcome = newDirectModelTurnOutcome(nil, err, context.Canceled)
	if outcome.Status != "cancelled" || outcome.Output != "model request cancelled" || outcome.ErrorText != "cancelled" {
		t.Fatalf("cancel outcome = %+v, want cancellation result", outcome)
	}
}

func TestDirectModelTurnOutcomeRedactsInlineImagePayload(t *testing.T) {
	t.Parallel()

	payload := strings.Repeat("A", 128)
	outcome := newDirectModelTurnOutcome(nil, errors.New("bad data:image/png;base64,"+payload), nil)
	if strings.Contains(outcome.Output, payload) || strings.Contains(outcome.ErrorText, payload) ||
		!strings.Contains(outcome.ErrorText, "[redacted inline image]") {
		t.Fatalf("outcome = %+v", outcome)
	}
}

type cancelBlockingProvider struct {
	*fakeProvider
	startedOnce sync.Once
	started     chan struct{}
	cancelled   chan error
}

func (p *cancelBlockingProvider) Chat(ctx context.Context, _ types.ChatRequest) (*types.ChatResponse, error) {
	p.startedOnce.Do(func() { close(p.started) })
	<-ctx.Done()
	p.cancelled <- ctx.Err()
	return nil, ctx.Err()
}

type terminalContextChatStore struct {
	chat.Store
	mu                 sync.Mutex
	terminalObserved   bool
	terminalContextErr error
	terminalDeadline   time.Time
}

func (s *terminalContextChatStore) UpdateMessage(ctx context.Context, sessionID, messageID string, update func(*chat.Message)) (chat.Session, error) {
	if err := ctx.Err(); err != nil {
		return chat.Session{}, err
	}
	deadline, hasDeadline := ctx.Deadline()
	updated, err := s.Store.UpdateMessage(ctx, sessionID, messageID, update)
	if err != nil {
		return chat.Session{}, err
	}
	for _, message := range updated.Messages {
		if message.ID != messageID || message.Role != "assistant" || message.Status != "cancelled" {
			continue
		}
		s.mu.Lock()
		s.terminalObserved = true
		s.terminalContextErr = ctx.Err()
		if hasDeadline {
			s.terminalDeadline = deadline
		}
		s.mu.Unlock()
		break
	}
	return updated, nil
}

func (s *terminalContextChatStore) terminalContextSnapshot() (bool, error, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.terminalObserved, s.terminalContextErr, s.terminalDeadline
}

type failFirstUserMessageUpdateStore struct {
	chat.Store
	mu       sync.Mutex
	failures int
}

func (s *failFirstUserMessageUpdateStore) UpdateMessage(ctx context.Context, sessionID, messageID string, update func(*chat.Message)) (chat.Session, error) {
	stored, ok, err := s.Store.Get(ctx, sessionID)
	if err != nil {
		return chat.Session{}, err
	}
	if ok {
		for _, message := range stored.Messages {
			if message.ID != messageID || message.Role != "user" {
				continue
			}
			s.mu.Lock()
			if s.failures == 0 {
				s.failures++
				s.mu.Unlock()
				return chat.Session{}, errors.New("injected user route snapshot failure")
			}
			s.mu.Unlock()
			break
		}
	}
	return s.Store.UpdateMessage(ctx, sessionID, messageID, update)
}

func (s *failFirstUserMessageUpdateStore) failureCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failures
}

func TestDirectModelTurnTerminalizesAssistantWhenUserRouteSnapshotUpdateFails(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	provider := &fakeProvider{
		name: "actual-provider",
		response: &types.ChatResponse{
			ID:    "chatcmpl-route-snapshot",
			Model: "gpt-4o-mini",
			Choices: []types.ChatChoice{{
				Index:        0,
				Message:      types.Message{Role: "assistant", Content: "route selected"},
				FinishReason: "stop",
			}},
		},
	}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{provider}, config.Config{}, nil)
	baseStore := chat.NewMemoryStore()
	store := &failFirstUserMessageUpdateStore{Store: baseStore}
	apiHandler.SetAgentChatStore(store)

	const sessionID = "chat_direct_user_route_update_failure"
	if _, err := baseStore.Create(t.Context(), chat.Session{
		ID:      sessionID,
		AgentID: chat.DefaultAgentID,
		Model:   "gpt-4o-mini",
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	client := newTaskTestClient(t, NewServer(logger, apiHandler))
	response := mustRequestJSON[ChatSessionResponse](
		client,
		http.MethodPost,
		"/hecate/v1/chat/sessions/"+sessionID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"content":"select a route"}`,
	)

	if store.failureCount() != 1 {
		t.Fatalf("injected user update failures = %d, want 1", store.failureCount())
	}
	if len(response.Data.Messages) != 2 {
		t.Fatalf("response messages = %+v, want user and assistant", response.Data.Messages)
	}
	user := response.Data.Messages[0]
	assistant := response.Data.Messages[1]
	if user.Role != "user" || user.Provider != "" {
		t.Fatalf("user route snapshot = %+v, want failed update to leave original Auto route", user)
	}
	if assistant.Role != "assistant" || assistant.Status != "completed" || assistant.Content != "route selected" || assistant.Provider != "actual-provider" {
		t.Fatalf("assistant = %+v, want completed actual-provider terminal state", assistant)
	}
	if response.Data.Provider != "actual-provider" || response.Data.TurnsUsed != 1 {
		t.Fatalf("session route/turn snapshot = provider %q turns %d, want actual-provider/1", response.Data.Provider, response.Data.TurnsUsed)
	}
	if !strings.Contains(logs.String(), "chat.direct_model.user_route_snapshot_update_failed") {
		t.Fatalf("logs = %q, want user route snapshot failure event", logs.String())
	}
}

func TestDirectModelTurnPersistsCancellationAfterRequestDisconnect(t *testing.T) {
	provider := &cancelBlockingProvider{
		fakeProvider: &fakeProvider{name: "openai"},
		started:      make(chan struct{}),
		cancelled:    make(chan error, 1),
	}
	apiHandler := newTestAPIHandlerWithSettings(quietLogger(), []providers.Provider{provider}, config.Config{}, nil)
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	metrics, err := telemetry.NewAgentChatMetricsWithMeterProvider(meterProvider)
	if err != nil {
		t.Fatalf("NewAgentChatMetricsWithMeterProvider: %v", err)
	}
	apiHandler.agentChatMetrics = metrics
	baseStore := chat.NewMemoryStore()
	store := &terminalContextChatStore{Store: baseStore}
	apiHandler.SetAgentChatStore(store)

	const sessionID = "chat_direct_request_disconnect"
	if _, err := baseStore.Create(t.Context(), chat.Session{
		ID:       sessionID,
		AgentID:  chat.DefaultAgentID,
		Provider: "openai",
		Model:    "gpt-4o-mini",
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	handler := NewServer(quietLogger(), apiHandler)
	requestCtx, cancelRequest := context.WithCancel(t.Context())
	request := httptest.NewRequest(
		http.MethodPost,
		"/hecate/v1/chat/sessions/"+sessionID+"/messages",
		strings.NewReader(`{"execution_mode":"hecate_task","tools_enabled":false,"content":"wait for cancellation"}`),
	).WithContext(requestCtx)
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(recorder, request)
		close(done)
	}()

	select {
	case <-provider.started:
	case <-time.After(3 * time.Second):
		t.Fatal("provider did not start")
	}
	cancelRequest()
	select {
	case err := <-provider.cancelled:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("provider context error = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("provider did not observe request cancellation")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("message handler did not finish after request cancellation")
	}

	if recorder.Body.Len() != 0 {
		t.Fatalf("response body = %q, want no response after caller disconnected", recorder.Body.String())
	}
	stored, ok, err := baseStore.Get(t.Context(), sessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if !ok || len(stored.Messages) != 2 {
		t.Fatalf("stored session = %+v, want user and assistant messages", stored)
	}
	assistant := stored.Messages[1]
	if assistant.Role != "assistant" || assistant.Status != "cancelled" || assistant.Content != "model request cancelled" {
		t.Fatalf("assistant = %+v, want persisted cancelled terminal state", assistant)
	}
	if stored.TurnsUsed != 1 {
		t.Fatalf("turns_used = %d, want 1", stored.TurnsUsed)
	}
	observed, contextErr, deadline := store.terminalContextSnapshot()
	if !observed {
		t.Fatal("terminal UpdateMessage was not observed")
	}
	if contextErr != nil {
		t.Fatalf("terminal UpdateMessage context error = %v, want live context", contextErr)
	}
	if deadline.IsZero() || !deadline.After(time.Now()) {
		t.Fatalf("terminal UpdateMessage deadline = %s, want a live bounded deadline", deadline)
	}

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("Collect metrics: %v", err)
	}
	foundCancelledTurn := false
	foundRequestCancellation := false
	for _, scope := range collected.ScopeMetrics {
		for _, metric := range scope.Metrics {
			sum, ok := metric.Data.(metricdata.Sum[int64])
			if !ok {
				continue
			}
			for _, point := range sum.DataPoints {
				adapter := chatMetricStringAttribute(point.Attributes, telemetry.AttrHecateAgentAdapterID)
				switch metric.Name {
				case telemetry.MetricAgentChatTurnsTotal:
					if adapter == "hecate" &&
						chatMetricStringAttribute(point.Attributes, telemetry.AttrHecateChatTurnStatus) == "cancelled" &&
						chatMetricStringAttribute(point.Attributes, telemetry.AttrHecateResult) == telemetry.ResultError && point.Value == 1 {
						foundCancelledTurn = true
					}
				case telemetry.MetricAgentChatCancelledTotal:
					if adapter == "hecate" &&
						chatMetricStringAttribute(point.Attributes, telemetry.AttrHecateChatCancelReason) == "request_cancelled" && point.Value == 1 {
						foundRequestCancellation = true
					}
				}
			}
		}
	}
	if !foundCancelledTurn || !foundRequestCancellation {
		t.Fatalf("direct-model cancellation metrics = turn:%t cancellation:%t; metrics=%+v", foundCancelledTurn, foundRequestCancellation, collected)
	}
}
