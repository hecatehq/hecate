package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/controlplane"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestAgentChatTraceAttrsUseChatTurnIdentity(t *testing.T) {
	t.Parallel()

	attrs := agentChatTraceAttrs(
		chat.Session{ID: "chat_1", Workspace: "/workspace"},
		agentadapters.Adapter{ID: "codex", Name: "Codex", Kind: "acp"},
		"turn_1",
		"msg_1",
		map[string]any{telemetry.AttrHecateChatTurnStatus: "running"},
	)
	if got := attrs[telemetry.AttrHecateChatTurnID]; got != "turn_1" {
		t.Fatalf("%s = %#v, want turn_1", telemetry.AttrHecateChatTurnID, got)
	}
	for _, key := range []string{
		telemetry.AttrHecateRunID,
		telemetry.AttrHecateRunStatus,
		telemetry.AttrHecateRunDurationMS,
	} {
		if _, ok := attrs[key]; ok {
			t.Fatalf("external-agent Chat Turn must not emit Task Run attr %s: %#v", key, attrs)
		}
	}
}

func TestHecateAgentChatTraceAttrsKeepTurnAndTaskRunDistinct(t *testing.T) {
	t.Parallel()

	session := chat.Session{ID: "chat_1", Workspace: "/workspace"}
	beforeTask := hecateAgentChatTraceAttrs(session, "turn_1", "", "", "msg_1", nil)
	if got := beforeTask[telemetry.AttrHecateChatTurnID]; got != "turn_1" {
		t.Fatalf("%s = %#v, want turn_1", telemetry.AttrHecateChatTurnID, got)
	}
	for _, key := range []string{telemetry.AttrHecateTaskID, telemetry.AttrHecateRunID} {
		if _, ok := beforeTask[key]; ok {
			t.Fatalf("pre-Task Chat Turn must omit %s: %#v", key, beforeTask)
		}
	}

	withTaskRun := hecateAgentChatTraceAttrs(session, "turn_1", "task_1", "run_1", "msg_1", nil)
	if got := withTaskRun[telemetry.AttrHecateChatTurnID]; got != "turn_1" {
		t.Fatalf("%s = %#v, want turn_1", telemetry.AttrHecateChatTurnID, got)
	}
	if got := withTaskRun[telemetry.AttrHecateTaskID]; got != "task_1" {
		t.Fatalf("%s = %#v, want task_1", telemetry.AttrHecateTaskID, got)
	}
	if got := withTaskRun[telemetry.AttrHecateRunID]; got != "run_1" {
		t.Fatalf("%s = %#v, want run_1", telemetry.AttrHecateRunID, got)
	}
}

func TestDirectModelChatTurnUsesCanonicalChatTraceAndMetrics(t *testing.T) {
	provider := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:        "chatcmpl-direct-turn-telemetry",
			Model:     "gpt-4o-mini",
			CreatedAt: time.Now().UTC(),
			Choices: []types.ChatChoice{{
				Index:        0,
				Message:      types.Message{Role: "assistant", Content: "Direct answer."},
				FinishReason: "stop",
			}},
		},
	}
	logger := quietLogger()
	handler := newTestAPIHandlerWithSettings(logger, []providers.Provider{provider}, config.Config{}, controlplane.NewMemoryStore())
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	metrics, err := telemetry.NewAgentChatMetricsWithMeterProvider(meterProvider)
	if err != nil {
		t.Fatalf("NewAgentChatMetricsWithMeterProvider: %v", err)
	}
	handler.agentChatMetrics = metrics
	client := newAPITestClient(t, NewServer(logger, handler))

	created := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"openai","model":"gpt-4o-mini"}`)
	recorder := client.mustRequest(http.MethodPost, "/hecate/v1/chat/sessions/"+created.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"content":"answer directly"}`)
	updated := decodeRecorder[ChatSessionResponse](t, recorder)
	assistant := updated.Data.Messages[len(updated.Data.Messages)-1]
	if assistant.TurnID == "" || assistant.RunID != "" {
		t.Fatalf("direct-model identity = turn %q run %q, want Chat Turn and no Task Run", assistant.TurnID, assistant.RunID)
	}
	if assistant.TraceID == "" || assistant.SpanID == "" {
		t.Fatalf("direct-model assistant missing Chat Turn trace: %#v", assistant)
	}
	if got := recorder.Header().Get("X-Trace-Id"); got != assistant.TraceID {
		t.Fatalf("X-Trace-Id = %q, want assistant trace %q", got, assistant.TraceID)
	}
	if got := recorder.Header().Get("X-Span-Id"); got != assistant.SpanID {
		t.Fatalf("X-Span-Id = %q, want assistant span %q", got, assistant.SpanID)
	}

	tracePayload := mustRequestJSON[TraceResponse](client, http.MethodGet, "/hecate/v1/traces?request_id="+assistant.RequestID, "")
	if tracePayload.Data.TraceID != assistant.TraceID {
		t.Fatalf("trace endpoint returned trace %q, want Chat Turn trace %q", tracePayload.Data.TraceID, assistant.TraceID)
	}
	events := make(map[string]TraceEventRecord)
	for _, span := range tracePayload.Data.Spans {
		for _, event := range span.Events {
			events[event.Name] = event
		}
	}
	for _, eventName := range []string{telemetry.EventAgentChatTurnStarted, telemetry.EventAgentChatTurnFinished} {
		event, ok := events[eventName]
		if !ok {
			t.Fatalf("direct-model trace missing %s: %#v", eventName, tracePayload.Data.Spans)
		}
		if missing := telemetry.ValidateEventAttrs(event.Name, event.Attributes); len(missing) != 0 {
			t.Fatalf("direct-model event %s missing attrs %v: %#v", event.Name, missing, event.Attributes)
		}
		if got := event.Attributes[telemetry.AttrHecateChatTurnID]; got != assistant.TurnID {
			t.Fatalf("direct-model event turn_id = %#v, want %q", got, assistant.TurnID)
		}
		for _, key := range []string{telemetry.AttrHecateTaskID, telemetry.AttrHecateRunID} {
			if _, exists := event.Attributes[key]; exists {
				t.Fatalf("direct-model Chat Turn must omit %s: %#v", key, event.Attributes)
			}
		}
	}

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("Collect metrics: %v", err)
	}
	foundTurn := false
	for _, scope := range collected.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name != telemetry.MetricAgentChatTurnsTotal {
				continue
			}
			sum, ok := metric.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("turn metric type = %T, want int64 sum", metric.Data)
			}
			for _, point := range sum.DataPoints {
				if chatMetricStringAttribute(point.Attributes, telemetry.AttrHecateAgentAdapterID) == "hecate" &&
					chatMetricStringAttribute(point.Attributes, telemetry.AttrHecateAgentDriverKind) == "hecate" &&
					chatMetricStringAttribute(point.Attributes, telemetry.AttrHecateChatTurnStatus) == "completed" &&
					chatMetricStringAttribute(point.Attributes, telemetry.AttrHecateResult) == telemetry.ResultSuccess && point.Value == 1 {
					foundTurn = true
				}
			}
		}
	}
	if !foundTurn {
		t.Fatalf("missing completed direct-model Chat Turn metric: %+v", collected)
	}
}
