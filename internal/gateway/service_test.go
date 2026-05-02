package gateway

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/hecate/agent-runtime/internal/billing"
	"github.com/hecate/agent-runtime/internal/config"
	"github.com/hecate/agent-runtime/internal/governor"
	"github.com/hecate/agent-runtime/internal/profiler"
	"github.com/hecate/agent-runtime/internal/providers"
	"github.com/hecate/agent-runtime/internal/telemetry"
	"github.com/hecate/agent-runtime/pkg/types"
)

func TestServiceHandleChatFallsBackWhenPrimaryPriceMissing(t *testing.T) {
	t.Parallel()

	primary := &sequenceProvider{
		name: "openai",
		kind: providers.KindCloud,
	}
	fallback := &sequenceProvider{
		name: "ollama",
		kind: providers.KindLocal,
		responses: []providerResponse{
			{
				response: &types.ChatResponse{
					Model: "model-b",
					Choices: []types.ChatChoice{
						{Index: 0, Message: types.Message{Role: "assistant", Content: "hello"}},
					},
					Usage: types.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
				},
			},
		},
	}

	registry := providers.NewRegistry(primary, fallback)
	store := governor.NewMemoryBudgetStore()
	router := staticFallbackRouter{
		route: types.RouteDecision{Provider: "openai", Model: "model-x", Reason: "primary"},
		fallbacks: []types.RouteDecision{
			{Provider: "ollama", Model: "model-b", Reason: "fallback"},
		},
	}
	service := NewService(Dependencies{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Router:    router,
		Governor:  governor.NewStaticGovernor(config.GovernorConfig{MaxPromptTokens: 64_000}, store, store),
		Providers: registry,
		Pricebook: billing.NewStaticPricebook(config.ProvidersConfig{
			OpenAICompatible: []config.OpenAICompatibleProviderConfig{
				{
					Name:         "openai",
					Kind:         "cloud",
					DefaultModel: "priced-model",
				},
				{
					Name:         "ollama",
					Kind:         "local",
					DefaultModel: "model-b",
				},
			},
		}, config.PricebookConfig{}),
		Tracer:     profiler.NewInMemoryTracer(nil),
		Metrics:    telemetry.NewMetrics(),
		Resilience: ResilienceOptions{MaxAttempts: 1, RetryBackoff: time.Millisecond, FailoverEnabled: true},
	})

	result, err := service.HandleChat(context.Background(), types.ChatRequest{
		RequestID: "req-1",
		Model:     "model-x",
		Messages:  []types.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("HandleChat() error = %v, want fallback success", err)
	}
	if result.Metadata.Provider != "ollama" {
		t.Fatalf("provider = %q, want ollama", result.Metadata.Provider)
	}
	if result.Metadata.FallbackFromProvider != "openai" {
		t.Fatalf("fallback_from_provider = %q, want openai", result.Metadata.FallbackFromProvider)
	}
}
