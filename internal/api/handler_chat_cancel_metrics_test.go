package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/controlplane"
	"github.com/hecatehq/hecate/internal/modelcaps"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestHecateChatOperatorCancelRecordsOperatorMetricWithDetachedWatcher(t *testing.T) {
	provider := &cancelBlockingProvider{
		fakeProvider: &fakeProvider{name: "openai"},
		started:      make(chan struct{}),
		cancelled:    make(chan error, 1),
	}
	handler := newTestAPIHandlerWithSettings(quietLogger(), []providers.Provider{provider}, config.Config{}, controlplane.NewMemoryStore())
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
	cancelResponse := performRequest(t, server, http.MethodPost, "/hecate/v1/chat/sessions/"+sessionID+"/cancel", `{}`)
	if cancelResponse.Code != http.StatusAccepted {
		t.Fatalf("cancel status = %d, want 202; body=%s", cancelResponse.Code, cancelResponse.Body.String())
	}
	select {
	case result := <-messageDone:
		if result.status != http.StatusOK {
			t.Fatalf("message status = %d, want 200; body=%s", result.status, result.body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("message handler did not finalize after operator cancel")
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
