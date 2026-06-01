package gateway

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/governor"
	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestStreamHandleExecuteAndCaptureDeltasCapturesContentAndToolCalls(t *testing.T) {
	handle := &StreamHandle{
		stream: func(w io.Writer) error {
			chunks := []string{
				`data: {"id":"chatcmpl_1","model":"gpt-test","choices":[{"delta":{"content":"Hello "},"finish_reason":null}]}` + "\n\n",
				`data: {"id":"chatcmpl_1","model":"gpt-test","choices":[{"delta":{"content":"there"},"finish_reason":null}]}` + "\n\n",
				`data: {"id":"chatcmpl_1","model":"gpt-test","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"shell_exec","arguments":"{\"command\""}}]},"finish_reason":null}]}` + "\n\n",
				`data: {"id":"chatcmpl_1","model":"gpt-test","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":":\"git status\"}"}}]},"finish_reason":null}]}` + "\n\n",
				`data: {"id":"chatcmpl_1","model":"gpt-test","choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n",
				"data: [DONE]\n\n",
			}
			for _, chunk := range chunks {
				if _, err := io.WriteString(w, chunk); err != nil {
					return err
				}
			}
			return nil
		},
	}
	var forwarded bytes.Buffer
	var deltas []string
	captured, err := handle.ExecuteAndCaptureDeltas(&forwarded, func(delta string) {
		deltas = append(deltas, delta)
	})
	if err != nil {
		t.Fatalf("ExecuteAndCaptureDeltas: %v", err)
	}
	if captured.ID != "chatcmpl_1" || captured.Model != "gpt-test" || captured.FinishReason != "tool_calls" {
		t.Fatalf("captured metadata = id %q model %q finish %q", captured.ID, captured.Model, captured.FinishReason)
	}
	if captured.Content != "Hello there" {
		t.Fatalf("content = %q, want Hello there", captured.Content)
	}
	if strings.Join(deltas, "") != "Hello there" {
		t.Fatalf("deltas = %#v, want streamed content chunks", deltas)
	}
	if len(captured.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1: %+v", len(captured.ToolCalls), captured.ToolCalls)
	}
	call := captured.ToolCalls[0]
	if call.ID != "call_1" || call.Type != "function" || call.Function.Name != "shell_exec" || call.Function.Arguments != `{"command":"git status"}` {
		t.Fatalf("tool call = %+v, want reconstructed shell_exec call", call)
	}
	if !strings.Contains(forwarded.String(), "Hello ") || !strings.Contains(forwarded.String(), "[DONE]") {
		t.Fatalf("forwarded stream missing original chunks:\n%s", forwarded.String())
	}
}

func TestServiceHandleChatStreamCaptureIncludesRouteMetadata(t *testing.T) {
	t.Parallel()

	provider := &streamingSequenceProvider{
		sequenceProvider: sequenceProvider{name: "fake", kind: providers.KindLocal},
	}
	registry := providers.NewRegistry(provider)
	store := governor.NewMemoryUsageStore()
	service := NewService(Dependencies{
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Router: staticFallbackRouter{
			route: types.RouteDecision{Provider: "fake", Model: "model-b", Reason: "test-route"},
		},
		Governor:   governor.NewStaticGovernor(config.GovernorConfig{MaxPromptTokens: 64_000}, store, store),
		Providers:  registry,
		Tracer:     profiler.NewInMemoryTracer(nil),
		Metrics:    telemetry.NewMetrics(),
		Resilience: ResilienceOptions{MaxAttempts: 1, RetryBackoff: time.Millisecond},
	})

	resp, err := service.HandleChatStreamCapture(context.Background(), types.ChatRequest{
		RequestID: "req-stream-route",
		Model:     "operator-model",
		Messages:  []types.Message{{Role: "user", Content: "hello"}},
	}, nil)
	if err != nil {
		t.Fatalf("HandleChatStreamCapture: %v", err)
	}
	if resp.Route.Provider != "fake" || resp.Route.ProviderKind != "local" || resp.Route.Model != "model-b" || resp.Route.Reason != "test-route" {
		t.Fatalf("route = %+v, want fake/local/model-b/test-route", resp.Route)
	}
	if resp.Model != "model-b" {
		t.Fatalf("model = %q, want streamed model-b", resp.Model)
	}
}

type streamingSequenceProvider struct {
	sequenceProvider
}

func (p *streamingSequenceProvider) ChatStream(_ context.Context, _ types.ChatRequest, w io.Writer) error {
	chunks := []string{
		`data: {"id":"chatcmpl-stream-route","model":"model-b","choices":[{"delta":{"content":"hello"},"finish_reason":null}]}` + "\n\n",
		`data: {"id":"chatcmpl-stream-route","model":"model-b","choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n",
		"data: [DONE]\n\n",
	}
	for _, chunk := range chunks {
		if _, err := io.WriteString(w, chunk); err != nil {
			return err
		}
	}
	return nil
}
