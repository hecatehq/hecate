package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/hecatehq/hecate/internal/catalog"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/gateway"
	"github.com/hecatehq/hecate/internal/governor"
	"github.com/hecatehq/hecate/internal/modelcaps"
	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/internal/router"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

type unsupportedAfterDispatchStreamingProvider struct {
	fakeProvider
	streamCalls int
}

func (p *unsupportedAfterDispatchStreamingProvider) ChatStream(_ context.Context, _ types.ChatRequest, _ io.Writer) error {
	p.streamCalls++
	return errors.New("provider does not support streaming after accepting the request")
}

func TestGatewayAgentLLMClientDoesNotFallbackAfterStreamingDispatch(t *testing.T) {
	t.Parallel()

	provider := &unsupportedAfterDispatchStreamingProvider{fakeProvider: fakeProvider{
		name:         "vision",
		defaultModel: "model-a",
		capabilities: providers.Capabilities{
			Name:         "vision",
			Kind:         providers.KindCloud,
			DefaultModel: "model-a",
			Models:       []string{"model-a"},
			ModelCapabilities: map[string]types.ModelCapabilities{
				"model-a": {ImageInput: modelcaps.ImageInputSupported, Source: modelcaps.SourceProvider},
			},
		},
		response: &types.ChatResponse{Model: "model-a", Choices: []types.ChatChoice{{
			Message: types.Message{Role: "assistant", Content: "should not be called"}, FinishReason: "stop",
		}}},
	}}
	registry := providers.NewRegistry(provider)
	instance, found := registry.GetInstance("vision")
	if !found {
		t.Fatal("provider instance not found")
	}
	providerCatalog := catalog.NewRegistryCatalog(registry, nil)
	usageStore := governor.NewMemoryUsageStore()
	service := gateway.NewService(gateway.Dependencies{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Router:    router.NewRuleRouter("model-a", providerCatalog),
		Catalog:   providerCatalog,
		Governor:  governor.NewStaticGovernor(config.GovernorConfig{MaxPromptTokens: 64_000}, usageStore, usageStore),
		Providers: registry,
		Tracer:    profiler.NewInMemoryTracer(nil),
		Metrics:   telemetry.NewMetrics(),
	})

	response, err := (gatewayAgentLLMClient{service: service}).ChatStream(context.Background(), types.ChatRequest{
		RequestID: "req-stream-after-dispatch",
		Model:     "model-a",
		Messages:  []types.Message{{Role: "user", Content: "private image"}},
		Scope:     types.RequestScope{ProviderHint: "vision"},
		Requirements: types.ChatRequestRequirements{
			ImageInput:         true,
			NoProviderFailover: true,
			ExactProvider:      true,
			ProviderInstance:   instance.Identity,
		},
	}, nil)
	if err == nil || response == nil {
		t.Fatalf("ChatStream() response=%+v err=%v, want attempted-route response and stream failure", response, err)
	}
	if response.Route.Provider != "vision" || response.Route.ProviderInstance != instance.Identity {
		t.Fatalf("attempted route = %+v, want vision/%+v", response.Route, instance.Identity)
	}
	if provider.streamCalls != 1 {
		t.Fatalf("stream calls = %d, want one attempted stream", provider.streamCalls)
	}
	if provider.CallCount() != 0 {
		t.Fatalf("non-stream provider calls = %d, want no fallback after attempted dispatch", provider.CallCount())
	}
}
