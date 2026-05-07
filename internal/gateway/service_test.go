package gateway

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/hecate/agent-runtime/internal/billing"
	"github.com/hecate/agent-runtime/internal/catalog"
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

func TestProviderModelDiscoveryCheckDistinguishesSkippedDiscovery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		entry      catalog.Entry
		wantStatus string
		wantReason string
	}{
		{
			name: "disabled provider",
			entry: catalog.Entry{
				Status: "disabled",
			},
			wantStatus: "blocked",
			wantReason: "provider_disabled",
		},
		{
			name: "self referential provider",
			entry: catalog.Entry{
				DiscoverySource: "self_referential",
				Status:          "degraded",
			},
			wantStatus: "blocked",
			wantReason: "self_referential",
		},
		{
			name: "discovery error without fallback",
			entry: catalog.Entry{
				Status:    "degraded",
				LastError: "dial tcp: connection refused",
			},
			wantStatus: "unknown",
			wantReason: "discovery_failed",
		},
		{
			name: "configured fallback remains warning",
			entry: catalog.Entry{
				DefaultModel: "llama3.1:8b",
				LastError:    "dial tcp: connection refused",
			},
			wantStatus: "warning",
			wantReason: "default_model_only",
		},
		{
			name:       "empty successful discovery",
			entry:      catalog.Entry{},
			wantStatus: "blocked",
			wantReason: "no_models",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := providerModelDiscoveryCheck(tt.entry)
			assertReadinessCheck(t, got, "models", tt.wantStatus, tt.wantReason)
		})
	}
}

func TestProviderHealthCheckTreatsRoutableDegradedProvidersAsWarnings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		entry      catalog.Entry
		wantStatus string
		wantReason string
	}{
		{
			name: "latency degraded but healthy",
			entry: catalog.Entry{
				Status:       "degraded",
				Healthy:      true,
				HealthReason: "latency",
			},
			wantStatus: "warning",
			wantReason: "provider_slow",
		},
		{
			name: "degraded and unavailable",
			entry: catalog.Entry{
				Status:       "degraded",
				Healthy:      false,
				HealthReason: "timeout",
			},
			wantStatus: "blocked",
			wantReason: "provider_unhealthy",
		},
		{
			name: "rate limited and unavailable",
			entry: catalog.Entry{
				Status:       "degraded",
				Healthy:      false,
				HealthReason: "rate_limit",
			},
			wantStatus: "blocked",
			wantReason: "provider_rate_limited",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := providerHealthCheck(tt.entry)
			assertReadinessCheck(t, got, "health", tt.wantStatus, tt.wantReason)
		})
	}
}

func assertReadinessCheck(t *testing.T, got types.ProviderReadinessCheck, wantName, wantStatus, wantReason string) {
	t.Helper()
	if got.Name != wantName {
		t.Fatalf("Name = %q, want %q", got.Name, wantName)
	}
	if got.Status != wantStatus {
		t.Fatalf("Status = %q, want %q", got.Status, wantStatus)
	}
	if got.Reason != wantReason {
		t.Fatalf("Reason = %q, want %q", got.Reason, wantReason)
	}
	if got.Message == "" {
		t.Fatal("Message is empty")
	}
}
