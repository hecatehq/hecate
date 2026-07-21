package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/governor"
	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/providerdispatch"
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
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "hello" {
		t.Fatalf("choices = %+v, want captured no-space SSE content", resp.Choices)
	}
}

func TestServiceHandleChatStreamCaptureRejectsPartialProviderStream(t *testing.T) {
	t.Parallel()

	provider := &truncatedStreamingProvider{
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

	var deltas []string
	response, err := service.HandleChatStreamCapture(context.Background(), types.ChatRequest{
		RequestID: "req-stream-partial",
		Model:     "operator-model",
		Messages:  []types.Message{{Role: "user", Content: "hello"}},
	}, func(delta string) {
		deltas = append(deltas, delta)
	})
	if response == nil || response.Route.Provider != "fake" || len(response.Choices) != 0 {
		t.Fatalf("HandleChatStreamCapture() response = %+v, want route-only attempted-provider metadata", response)
	}
	var upstreamErr *providers.UpstreamError
	if !errors.As(err, &upstreamErr) {
		t.Fatalf("HandleChatStreamCapture() error = %v, want *providers.UpstreamError", err)
	}
	if upstreamErr.StatusCode != http.StatusBadGateway || upstreamErr.Type != "upstream_error" ||
		upstreamErr.Message != "OpenAI-compatible stream ended before [DONE]" {
		t.Fatalf("HandleChatStreamCapture() error = %+v, want typed upstream stream failure", upstreamErr)
	}
	if strings.Join(deltas, "") != "partial" {
		t.Fatalf("content deltas = %#v, want already-observed partial delta without a synthesized success", deltas)
	}
}

func TestServiceHandleChatStreamCaptureKeepsPreDispatchValidationFailureUndisclosed(t *testing.T) {
	t.Parallel()

	provider := &validationFailBeforeStreamProvider{
		streamingSequenceProvider: streamingSequenceProvider{
			sequenceProvider: sequenceProvider{name: "vision", kind: providers.KindCloud},
		},
	}
	registry := providers.NewRegistry(provider)
	instance, ok := registry.GetInstance("vision")
	if !ok {
		t.Fatal("provider instance not found")
	}
	store := governor.NewMemoryUsageStore()
	service := NewService(Dependencies{
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Router: staticFallbackRouter{
			route: types.RouteDecision{Provider: "vision", ProviderInstance: instance.Identity, Model: "model-a"},
		},
		Governor:   governor.NewStaticGovernor(config.GovernorConfig{MaxPromptTokens: 64_000}, store, store),
		Providers:  registry,
		Tracer:     profiler.NewInMemoryTracer(nil),
		Metrics:    telemetry.NewMetrics(),
		Resilience: ResilienceOptions{MaxAttempts: 1, RetryBackoff: time.Millisecond},
	})

	response, err := service.HandleChatStreamCapture(context.Background(), types.ChatRequest{
		RequestID: "req-stream-validation-before-dispatch",
		Model:     "model-a",
		Messages:  []types.Message{{Role: "user", Content: "private image"}},
		Requirements: types.ChatRequestRequirements{
			ImageInput:         true,
			NoProviderFailover: true,
			ProviderInstance:   instance.Identity,
		},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "stream validator not ready") {
		t.Fatalf("HandleChatStreamCapture() error = %v, want pre-dispatch validation failure", err)
	}
	if response != nil {
		t.Fatalf("HandleChatStreamCapture() response = %+v, want no attempted-provider metadata before dispatch", response)
	}
	if provider.streamCallCount != 0 {
		t.Fatalf("stream calls = %d, want no provider dispatch", provider.streamCallCount)
	}
}

func TestServiceHandleChatStreamCaptureDoesNotDispatchWhenAttemptRecorderFails(t *testing.T) {
	t.Parallel()

	provider := &streamingSequenceProvider{
		sequenceProvider: sequenceProvider{name: "vision", kind: providers.KindCloud},
	}
	registry := providers.NewRegistry(provider)
	instance, ok := registry.GetInstance("vision")
	if !ok {
		t.Fatal("provider instance not found")
	}
	store := governor.NewMemoryUsageStore()
	tracer := profiler.NewInMemoryTracer(nil)
	service := NewService(Dependencies{
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Router: staticFallbackRouter{
			route: types.RouteDecision{Provider: "vision", ProviderInstance: instance.Identity, Model: "model-a"},
		},
		Governor:   governor.NewStaticGovernor(config.GovernorConfig{MaxPromptTokens: 64_000}, store, store),
		Providers:  registry,
		Tracer:     tracer,
		Metrics:    telemetry.NewMetrics(),
		Resilience: ResilienceOptions{MaxAttempts: 1, RetryBackoff: time.Millisecond},
	})

	response, err := service.HandleChatStreamCapture(providerdispatch.WithAttemptRecorder(context.Background(), func(types.RouteDecision) error {
		return errors.New("durable rich-input fence unavailable")
	}), types.ChatRequest{
		RequestID: "req-stream-attempt-recorder-failed",
		Model:     "model-a",
		Messages:  []types.Message{{Role: "user", Content: "private image"}},
		Requirements: types.ChatRequestRequirements{
			ImageInput:         true,
			NoProviderFailover: true,
			ProviderInstance:   instance.Identity,
		},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "durable rich-input fence unavailable") {
		t.Fatalf("HandleChatStreamCapture() error = %v, want recorder failure", err)
	}
	if response != nil {
		t.Fatalf("HandleChatStreamCapture() response = %+v, want no attempted-route metadata before dispatch", response)
	}
	if provider.streamCallCount != 0 {
		t.Fatalf("stream calls = %d, want no provider dispatch", provider.streamCallCount)
	}
	assertRichInputRouteFenceBlocked(t, tracer, "req-stream-attempt-recorder-failed")
}

func TestRouteForStreamRejectsProviderReplacementForProviderBoundRequest(t *testing.T) {
	t.Parallel()

	admitted := &streamingSequenceProvider{
		sequenceProvider: sequenceProvider{name: "vision", kind: providers.KindCloud},
	}
	replacement := &streamingSequenceProvider{
		sequenceProvider: sequenceProvider{name: "vision", kind: providers.KindCloud},
	}
	registry := providers.NewMutableRegistry(admitted)
	admittedInstance, ok := registry.GetInstance("vision")
	if !ok {
		t.Fatal("admitted provider instance not found")
	}
	store := governor.NewMemoryUsageStore()
	tracer := profiler.NewInMemoryTracer(nil)
	service := NewService(Dependencies{
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Router: staticFallbackRouter{
			route: types.RouteDecision{
				Provider:         "vision",
				ProviderInstance: admittedInstance.Identity,
				Model:            "model-a",
				Reason:           "auto_image",
			},
		},
		Governor:  governor.NewStaticGovernor(config.GovernorConfig{MaxPromptTokens: 64_000}, store, store),
		Providers: registry,
		Tracer:    tracer,
		Metrics:   telemetry.NewMetrics(),
	})

	handle, _, err := service.RouteForStream(context.Background(), types.ChatRequest{
		RequestID: "req-stream-image-replaced",
		Model:     "model-a",
		Messages:  []types.Message{{Role: "user", Content: "private image"}},
		Requirements: types.ChatRequestRequirements{
			NoProviderFailover: true,
			ProviderInstance:   admittedInstance.Identity,
		},
	})
	if err != nil {
		t.Fatalf("RouteForStream() error = %v", err)
	}

	registry.Replace(replacement)
	err = handle.Execute(io.Discard)
	if err == nil || !strings.Contains(err.Error(), "changed after bound route admission") {
		t.Fatalf("Execute() error = %v, want provider-instance replacement rejection", err)
	}
	if admitted.streamCallCount != 0 || replacement.streamCallCount != 0 {
		t.Fatalf("stream calls admitted=%d replacement=%d, want no disclosure", admitted.streamCallCount, replacement.streamCallCount)
	}
	assertStreamProviderCallBlocked(t, tracer, "req-stream-image-replaced", RoutePreflightProviderChanged, admittedInstance.Identity.ID, "private image")
}

func TestRouteForStreamRejectsVerifiedRichInputAfterProofExpires(t *testing.T) {
	t.Parallel()

	provider := &streamingSequenceProvider{
		sequenceProvider: sequenceProvider{name: "vision", kind: providers.KindCloud},
	}
	registry := providers.NewRegistry(provider)
	instance, ok := registry.GetInstance("vision")
	if !ok {
		t.Fatal("provider instance not found")
	}
	store := governor.NewMemoryUsageStore()
	service := NewService(Dependencies{
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Router: staticFallbackRouter{route: types.RouteDecision{
			Provider:         "vision",
			ProviderInstance: instance.Identity,
			Model:            "model-a",
			Reason:           "verified_rich_input",
		}},
		Governor:  governor.NewStaticGovernor(config.GovernorConfig{MaxPromptTokens: 64_000}, store, store),
		Providers: registry,
		Tracer:    profiler.NewInMemoryTracer(nil),
		Metrics:   telemetry.NewMetrics(),
	})
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	clock := now
	service.now = func() time.Time { return clock }

	handle, _, err := service.RouteForStream(context.Background(), types.ChatRequest{
		RequestID: "req-stream-verified-proof-expired",
		Model:     "model-a",
		Scope:     types.RequestScope{ProviderHint: "vision"},
		Messages:  []types.Message{{Role: "user", Content: "private image"}},
		Requirements: types.ChatRequestRequirements{
			ImageInput:               true,
			ToolCalling:              true,
			ToolCallingVerified:      true,
			ToolCallingVerifiedModel: "model-a",
			ToolCallingVerifiedUntil: now.Add(time.Minute),
			NoProviderFailover:       true,
			ExactProvider:            true,
			ProviderInstance:         instance.Identity,
		},
	})
	if err != nil {
		t.Fatalf("RouteForStream() error = %v", err)
	}
	clock = now.Add(time.Hour)
	if err := handle.Execute(io.Discard); err == nil || !strings.Contains(err.Error(), "expired before dispatch") {
		t.Fatalf("Execute() error = %v, want expired verification rejection", err)
	}
	if provider.streamCallCount != 0 {
		t.Fatalf("stream calls = %d, want expired proof blocked before disclosure", provider.streamCallCount)
	}
}

func TestRouteForStreamRecordsImageProviderRemovalBeforeExecute(t *testing.T) {
	t.Parallel()

	admitted := &streamingSequenceProvider{
		sequenceProvider: sequenceProvider{name: "vision", kind: providers.KindCloud},
	}
	registry := providers.NewMutableRegistry(admitted)
	admittedInstance, ok := registry.GetInstance("vision")
	if !ok {
		t.Fatal("admitted provider instance not found")
	}
	store := governor.NewMemoryUsageStore()
	tracer := profiler.NewInMemoryTracer(nil)
	service := NewService(Dependencies{
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Router: staticFallbackRouter{
			route: types.RouteDecision{
				Provider:         "vision",
				ProviderInstance: admittedInstance.Identity,
				Model:            "model-a",
				Reason:           "auto_image",
			},
		},
		Governor:  governor.NewStaticGovernor(config.GovernorConfig{MaxPromptTokens: 64_000}, store, store),
		Providers: registry,
		Tracer:    tracer,
		Metrics:   telemetry.NewMetrics(),
	})

	handle, _, err := service.RouteForStream(context.Background(), types.ChatRequest{
		RequestID: "req-stream-image-removed",
		Model:     "model-a",
		Messages:  []types.Message{{Role: "user", Content: "private removed image"}},
		Requirements: types.ChatRequestRequirements{
			ImageInput:         true,
			NoProviderFailover: true,
			ProviderInstance:   admittedInstance.Identity,
		},
	})
	if err != nil {
		t.Fatalf("RouteForStream() error = %v", err)
	}

	registry.Replace()
	err = handle.Execute(io.Discard)
	if err == nil || !strings.Contains(err.Error(), `provider "vision" not found`) {
		t.Fatalf("Execute() error = %v, want provider removal rejection", err)
	}
	if admitted.streamCallCount != 0 {
		t.Fatalf("stream calls admitted=%d, want no disclosure", admitted.streamCallCount)
	}
	assertStreamProviderCallBlocked(t, tracer, "req-stream-image-removed", RoutePreflightProviderNotFound, admittedInstance.Identity.ID, "private removed image")
}

func assertStreamProviderCallBlocked(
	t *testing.T,
	tracer *profiler.InMemoryTracer,
	requestID string,
	wantReason RoutePreflightErrorKind,
	providerInstanceID string,
	privateContent string,
) {
	t.Helper()
	trace, ok := tracer.Get(requestID)
	if !ok {
		t.Fatalf("trace %q not found", requestID)
	}
	var blocked []types.TraceEvent
	for _, event := range trace.Events() {
		if event.Name == "provider.call.blocked" {
			blocked = append(blocked, event)
		}
	}
	if len(blocked) != 1 {
		t.Fatalf("provider.call.blocked events = %+v, want exactly one", blocked)
	}
	attributes := blocked[0].Attributes
	if got := attributes[telemetry.AttrGenAIProviderName]; got != "vision" {
		t.Fatalf("blocked provider = %v, want vision", got)
	}
	if got := attributes[telemetry.AttrGenAIRequestModel]; got != "model-a" {
		t.Fatalf("blocked model = %v, want model-a", got)
	}
	if got := attributes[telemetry.AttrHecateProviderIndex]; got != 0 {
		t.Fatalf("blocked provider index = %v, want 0", got)
	}
	if got := attributes[telemetry.AttrHecateRouteSkipReason]; got != string(wantReason) {
		t.Fatalf("blocked skip reason = %v, want %q", got, wantReason)
	}
	encoded, err := json.Marshal(blocked[0])
	if err != nil {
		t.Fatalf("json.Marshal(blocked event) error = %v", err)
	}
	serialized := string(encoded)
	if strings.Contains(serialized, providerInstanceID) || strings.Contains(serialized, privateContent) || strings.Contains(strings.ToLower(serialized), `"provider_instance":`) {
		t.Fatalf("blocked event exposed provider identity or image content: %s", serialized)
	}
}

func assertRichInputRouteFenceBlocked(t *testing.T, tracer *profiler.InMemoryTracer, requestID string) {
	t.Helper()
	trace, ok := tracer.Get(requestID)
	if !ok {
		t.Fatalf("trace %q not found", requestID)
	}
	var blocked []types.TraceEvent
	for _, event := range trace.Events() {
		if event.Name == "provider.call.blocked" {
			blocked = append(blocked, event)
		}
	}
	if len(blocked) != 1 {
		t.Fatalf("provider.call.blocked events = %+v, want exactly one", blocked)
	}
	attrs := blocked[0].Attributes
	if got := attrs[telemetry.AttrHecateErrorKind]; got != telemetry.ErrorKindRichInputRouteFence {
		t.Fatalf("blocked error kind = %v, want %q", got, telemetry.ErrorKindRichInputRouteFence)
	}
	if got := attrs[telemetry.AttrHecateRouteSkipReason]; got != richInputRouteFenceSkipReason {
		t.Fatalf("blocked skip reason = %v, want %q", got, richInputRouteFenceSkipReason)
	}
}

type streamingSequenceProvider struct {
	sequenceProvider
	streamCallCount int
}

type truncatedStreamingProvider struct {
	sequenceProvider
}

type validationFailBeforeStreamProvider struct {
	streamingSequenceProvider
	validateCalls int
}

func (p *validationFailBeforeStreamProvider) Validate() error {
	p.validateCalls++
	if p.validateCalls > 1 {
		return errors.New("stream validator not ready")
	}
	return nil
}

func (p *truncatedStreamingProvider) ChatStream(_ context.Context, _ types.ChatRequest, w io.Writer) error {
	if _, err := io.WriteString(w, `data: {"id":"chatcmpl-partial","model":"model-b","choices":[{"delta":{"content":"partial"},"finish_reason":null}]}`+"\n\n"); err != nil {
		return err
	}
	return &providers.UpstreamError{
		StatusCode: http.StatusBadGateway,
		Message:    "OpenAI-compatible stream ended before [DONE]",
		Type:       "upstream_error",
	}
}

func (p *streamingSequenceProvider) ChatStream(_ context.Context, _ types.ChatRequest, w io.Writer) error {
	p.streamCallCount++
	chunks := []string{
		`data:{"id":"chatcmpl-stream-route","model":"model-b","choices":[{"delta":{"content":"hello"},"finish_reason":null}]}` + "\n\n",
		`data:{"id":"chatcmpl-stream-route","model":"model-b","choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n",
		"data:[DONE]\n\n",
	}
	for _, chunk := range chunks {
		if _, err := io.WriteString(w, chunk); err != nil {
			return err
		}
	}
	return nil
}
