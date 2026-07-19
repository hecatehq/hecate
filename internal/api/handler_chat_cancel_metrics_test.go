package api

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/controlplane"
	"github.com/hecatehq/hecate/internal/modelcaps"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestHecateAgentChatCancellationReason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		run        types.TaskRun
		liveReason string
		want       string
	}{
		{
			name: "durable operator cancellation wins before live wake",
			run: types.TaskRun{
				Status:    "cancelled",
				LastError: "run cancelled: operator",
			},
			want: "operator",
		},
		{
			name: "live cancellation covers non task turn",
			run: types.TaskRun{
				Status:    "cancelled",
				LastError: "cancelled",
			},
			liveReason: "operator",
			want:       "operator",
		},
		{
			name: "request context remains fallback",
			run: types.TaskRun{
				Status:    "cancelled",
				LastError: "cancelled",
			},
			want: "request_cancelled",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hecateAgentChatCancellationReason(tt.run, tt.liveReason); got != tt.want {
				t.Fatalf("hecateAgentChatCancellationReason() = %q, want %q", got, tt.want)
			}
		})
	}
}

type blockingOperatorCancelTerminalStore struct {
	taskstate.Store
	durableCommitted chan struct{}
	release          <-chan struct{}
	once             sync.Once
}

func (s *blockingOperatorCancelTerminalStore) ApplyRunTerminalTransition(ctx context.Context, transition taskstate.TerminalRunTransition) (taskstate.TerminalRunTransitionResult, error) {
	result, err := s.Store.ApplyRunTerminalTransition(ctx, transition)
	if err != nil || result.Run.Status != "cancelled" || result.Run.LastError != "run cancelled: operator" {
		return result, err
	}
	s.once.Do(func() {
		close(s.durableCommitted)
		select {
		case <-s.release:
		case <-ctx.Done():
		}
	})
	return result, err
}

func TestHecateChatOperatorCancelRecordsOperatorMetricWithDetachedWatcher(t *testing.T) {
	provider := &cancelBlockingProvider{
		fakeProvider: &fakeProvider{name: "openai"},
		started:      make(chan struct{}),
		cancelled:    make(chan error, 1),
	}
	release := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	store := &blockingOperatorCancelTerminalStore{
		Store:            taskstate.NewMemoryStore(),
		durableCommitted: make(chan struct{}),
		release:          release,
	}
	handler := newTestAPIHandlerWithSettingsAndTaskStore(quietLogger(), []providers.Provider{provider}, config.Config{}, controlplane.NewMemoryStore(), store)
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	metrics, err := telemetry.NewAgentChatMetricsWithMeterProvider(meterProvider)
	if err != nil {
		t.Fatalf("NewAgentChatMetricsWithMeterProvider: %v", err)
	}
	handler.agentChatMetrics = metrics

	const sessionID = "chat_operator_cancel_metric"
	if _, err := handler.agentChat.Create(t.Context(), chat.Session{
		ID:           sessionID,
		AgentID:      chat.DefaultAgentID,
		Workspace:    t.TempDir(),
		Provider:     "openai",
		Model:        "gpt-4o-mini",
		Capabilities: types.ModelCapabilities{ToolCalling: modelcaps.ToolCallingParallel, Streaming: true, Source: modelcaps.SourceCatalog},
	}); err != nil {
		t.Fatalf("Create session: %v", err)
	}
	server := NewServer(quietLogger(), handler)
	messageDone := make(chan *chatMessageHTTPResult, 1)
	go func() {
		recorder := performRequest(t, server, http.MethodPost, "/hecate/v1/chat/sessions/"+sessionID+"/messages",
			`{"execution_mode":"hecate_task","content":"wait for operator cancel","client_request_id":"queued-operator-cancel"}`)
		messageDone <- &chatMessageHTTPResult{status: recorder.Code, body: recorder.Body.Bytes()}
	}()
	select {
	case <-provider.started:
	case <-time.After(3 * time.Second):
		t.Fatal("backing provider did not start")
	}
	waitForChatTaskLink(t, handler.agentChat, sessionID)
	cancelDone := make(chan *chatMessageHTTPResult, 1)
	go func() {
		recorder := performRequest(t, server, http.MethodPost, "/hecate/v1/chat/sessions/"+sessionID+"/cancel", `{}`)
		cancelDone <- &chatMessageHTTPResult{status: recorder.Code, body: recorder.Body.Bytes()}
	}()
	select {
	case <-store.durableCommitted:
	case <-time.After(3 * time.Second):
		t.Fatal("operator cancellation did not persist its terminal task outcome")
	}
	select {
	case result := <-messageDone:
		if result.status != http.StatusOK {
			t.Fatalf("message status = %d, want 200; body=%s", result.status, result.body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("detached watcher did not finalize from the durable operator cancellation")
	}

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("Collect metrics: %v", err)
	}
	foundOperator := false
	for _, scope := range collected.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name != telemetry.MetricAgentChatCancelledTotal {
				continue
			}
			sum, ok := metric.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("cancel metric type = %T, want int64 sum", metric.Data)
			}
			for _, point := range sum.DataPoints {
				adapter := chatMetricStringAttribute(point.Attributes, telemetry.AttrHecateAgentAdapterID)
				reason := chatMetricStringAttribute(point.Attributes, telemetry.AttrHecateChatCancelReason)
				if adapter == "hecate" && reason == "request_cancelled" {
					t.Fatalf("detached Hecate watcher misclassified operator cancellation: %+v", point)
				}
				if adapter == "hecate" && reason == "operator" && point.Value == 1 {
					foundOperator = true
				}
			}
		}
	}
	if !foundOperator {
		t.Fatalf("missing hecate/operator cancellation metric: %+v", collected)
	}
	close(release)
	released = true
	select {
	case result := <-cancelDone:
		if result.status != http.StatusAccepted {
			t.Fatalf("cancel status = %d, want 202; body=%s", result.status, result.body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel handler did not finish after terminal transition release")
	}
}

func waitForChatTaskLink(t *testing.T, store chat.Store, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		session, ok, err := store.Get(t.Context(), sessionID)
		if err != nil {
			t.Fatalf("Get session: %v", err)
		}
		if ok && session.TaskID != "" && session.LatestRunID != "" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("chat did not persist task/run linkage")
}

func chatMetricStringAttribute(set attribute.Set, key string) string {
	value, ok := set.Value(attribute.Key(key))
	if !ok || value.Type() != attribute.STRING {
		return ""
	}
	return value.AsString()
}
