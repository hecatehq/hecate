package providers

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/pkg/types"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	oteltrace "go.opentelemetry.io/otel/trace"
)

const wantProviderTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

func TestOpenAIProviderInjectsTraceContext(t *testing.T) {
	setupProviderTracePropagator(t)

	var seen []http.Header
	transport := testRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		seen = append(seen, r.Header.Clone())
		switch r.URL.Path {
		case "/v1/models":
			return jsonResponse(`{"data":[{"id":"gpt-4o-mini"}]}`), nil
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				return nil, err
			}
			if bytes.Contains(body, []byte(`"stream":true`)) {
				return sseResponse("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\ndata: [DONE]\n\n"), nil
			}
			return jsonResponse(`{"id":"chatcmpl-1","created":1700000000,"model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`), nil
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
			return nil, nil
		}
	})
	provider := NewOpenAICompatibleProvider(config.OpenAICompatibleProviderConfig{
		Name:         "openai",
		Kind:         "cloud",
		BaseURL:      "https://example.test",
		APIKey:       "test-key",
		Timeout:      time.Second,
		DefaultModel: "gpt-4o-mini",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider.httpClient.Transport = transport

	ctx := providerTraceContext(t)
	if _, err := provider.Capabilities(ctx); err != nil {
		t.Fatalf("Capabilities() error = %v", err)
	}
	if _, err := provider.Chat(ctx, types.ChatRequest{
		Model: "gpt-4o-mini",
		Messages: []types.Message{
			{Role: "user", Content: "hello"},
		},
	}); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	var out bytes.Buffer
	if err := provider.ChatStream(ctx, types.ChatRequest{
		Model: "gpt-4o-mini",
		Messages: []types.Message{
			{Role: "user", Content: "hello"},
		},
	}, &out); err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}

	assertTraceHeaders(t, seen, 3)
}

func TestAnthropicProviderInjectsTraceContext(t *testing.T) {
	setupProviderTracePropagator(t)

	var seen []http.Header
	transport := testRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		seen = append(seen, r.Header.Clone())
		switch r.URL.Path {
		case "/v1/models":
			return jsonResponse(`{"data":[{"id":"claude-sonnet-4-20250514"}]}`), nil
		case "/v1/messages":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				return nil, err
			}
			if bytes.Contains(body, []byte(`"stream":true`)) {
				return sseResponse("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"claude-sonnet-4-20250514\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"), nil
			}
			return jsonResponse(`{"id":"msg_1","model":"claude-sonnet-4-20250514","role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":1,"output_tokens":1}}`), nil
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
			return nil, nil
		}
	})
	provider := NewAnthropicProvider(config.OpenAICompatibleProviderConfig{
		Name:         "anthropic",
		Kind:         "cloud",
		Protocol:     "anthropic",
		BaseURL:      "https://api.anthropic.test",
		APIKey:       "secret",
		APIVersion:   "2023-06-01",
		Timeout:      time.Second,
		DefaultModel: "claude-sonnet-4-20250514",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider.httpClient.Transport = transport

	ctx := providerTraceContext(t)
	if _, err := provider.Capabilities(ctx); err != nil {
		t.Fatalf("Capabilities() error = %v", err)
	}
	if _, err := provider.Chat(ctx, types.ChatRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []types.Message{
			{Role: "user", Content: "hello"},
		},
	}); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	var out bytes.Buffer
	if err := provider.ChatStream(ctx, types.ChatRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []types.Message{
			{Role: "user", Content: "hello"},
		},
	}, &out); err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}

	assertTraceHeaders(t, seen, 3)
}

func setupProviderTracePropagator(t *testing.T) {
	t.Helper()
	old := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	t.Cleanup(func() {
		otel.SetTextMapPropagator(old)
	})
}

func providerTraceContext(t *testing.T) context.Context {
	t.Helper()
	traceID, err := oteltrace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatalf("TraceIDFromHex() error = %v", err)
	}
	spanID, err := oteltrace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatalf("SpanIDFromHex() error = %v", err)
	}
	ctx := oteltrace.ContextWithSpanContext(context.Background(), oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: oteltrace.FlagsSampled,
	}))
	bag, err := baggage.Parse("tenant=local")
	if err != nil {
		t.Fatalf("baggage.Parse() error = %v", err)
	}
	return baggage.ContextWithBaggage(ctx, bag)
}

func assertTraceHeaders(t *testing.T, seen []http.Header, want int) {
	t.Helper()
	if len(seen) != want {
		t.Fatalf("seen requests = %d, want %d", len(seen), want)
	}
	for i, header := range seen {
		if got := header.Get("traceparent"); got != wantProviderTraceparent {
			t.Fatalf("request %d traceparent = %q, want %q", i, got, wantProviderTraceparent)
		}
		if got := header.Get("baggage"); got != "tenant=local" {
			t.Fatalf("request %d baggage = %q, want tenant=local", i, got)
		}
	}
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func sseResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
