package gateway

import (
	"context"
	"io"
	"log/slog"
	"strings"
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

func TestProviderModelReadiness(t *testing.T) {
	t.Parallel()

	entries := []catalog.Entry{
		{
			Name:            "ollama",
			Kind:            providers.KindLocal,
			Status:          "healthy",
			Healthy:         true,
			CredentialState: "not_required",
			Models:          []string{"llama3.1:8b", "mistral:latest"},
		},
		{
			Name:            "anthropic",
			Kind:            providers.KindCloud,
			Status:          "healthy",
			Healthy:         true,
			CredentialState: "missing",
			Models:          []string{"claude-sonnet-4-5"},
		},
	}

	tests := []struct {
		name                string
		provider            string
		model               string
		wantReady           bool
		wantProvider        string
		wantReason          string
		wantMatchedProvider string
		wantBlockedReason   string
		wantSuggestions     bool
		rejectSuggestion    string
	}{
		{
			name:                "explicit provider reports model",
			provider:            "ollama",
			model:               "llama3.1:8b",
			wantReady:           true,
			wantProvider:        "ollama",
			wantReason:          "model_available",
			wantMatchedProvider: "ollama",
		},
		{
			name:                "explicit provider match returns canonical id",
			provider:            "Ollama",
			model:               "llama3.1:8b",
			wantReady:           true,
			wantProvider:        "ollama",
			wantReason:          "model_available",
			wantMatchedProvider: "ollama",
		},
		{
			name:                "explicit provider missing selected model",
			provider:            "ollama",
			model:               "gpt-5.4-mini",
			wantProvider:        "ollama",
			wantReason:          "model_not_discovered",
			wantMatchedProvider: "ollama",
		},
		{
			name:                "explicit provider blocked before model use",
			provider:            "anthropic",
			model:               "claude-sonnet-4-5",
			wantProvider:        "anthropic",
			wantReason:          "provider_not_ready",
			wantMatchedProvider: "anthropic",
			wantBlockedReason:   "credential_missing",
			wantSuggestions:     true,
		},
		{
			name:         "missing provider",
			provider:     "missing",
			model:        "llama3.1:8b",
			wantProvider: "missing",
			wantReason:   "provider_missing",
		},
		{
			name:                "auto finds routable provider",
			model:               "mistral:latest",
			wantReady:           true,
			wantProvider:        "auto",
			wantReason:          "auto_route_available",
			wantMatchedProvider: "ollama",
		},
		{
			name:            "auto cannot find selected model",
			model:           "gpt-5.4-mini",
			wantProvider:    "auto",
			wantReason:      "model_not_discovered",
			wantSuggestions: true,
		},
		{
			name:             "explicit provider model required",
			provider:         "ollama",
			wantProvider:     "ollama",
			wantReason:       "model_required",
			wantSuggestions:  true,
			rejectSuggestion: "claude-sonnet-4-5",
		},
		{
			name:            "auto model required",
			wantProvider:    "auto",
			wantReason:      "model_required",
			wantSuggestions: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := providerModelReadiness(entries, tt.provider, tt.model)
			if got.Ready != tt.wantReady {
				t.Fatalf("ready = %v, want %v", got.Ready, tt.wantReady)
			}
			if got.Reason != tt.wantReason {
				t.Fatalf("reason = %q, want %q", got.Reason, tt.wantReason)
			}
			if got.Provider != tt.wantProvider {
				t.Fatalf("provider = %q, want %q", got.Provider, tt.wantProvider)
			}
			if got.MatchedProvider != tt.wantMatchedProvider {
				t.Fatalf("matched_provider = %q, want %q", got.MatchedProvider, tt.wantMatchedProvider)
			}
			if got.ProviderBlockedReason != tt.wantBlockedReason {
				t.Fatalf("provider_blocked_reason = %q, want %q", got.ProviderBlockedReason, tt.wantBlockedReason)
			}
			if !got.Ready && got.OperatorAction == "" {
				t.Fatalf("operator_action is empty for blocked readiness: %#v", got)
			}
			if got.Ready && len(got.SuggestedModels) > 0 {
				t.Fatalf("suggested_models = %#v for ready response, want none", got.SuggestedModels)
			}
			if tt.wantSuggestions && len(got.SuggestedModels) == 0 {
				t.Fatalf("suggested_models is empty, want repair suggestions")
			}
			for _, suggestion := range got.SuggestedModels {
				if strings.EqualFold(suggestion, tt.rejectSuggestion) {
					t.Fatalf("suggested_models = %#v includes rejected suggestion %q", got.SuggestedModels, tt.rejectSuggestion)
				}
			}
		})
	}
}

func TestProviderModelReadinessAutoSelectionIsDeterministic(t *testing.T) {
	t.Parallel()

	entries := []catalog.Entry{
		{
			Name:            "zeta",
			Kind:            providers.KindLocal,
			Status:          "healthy",
			Healthy:         true,
			CredentialState: "not_required",
			Models:          []string{"shared-model"},
		},
		{
			Name:            "alpha",
			Kind:            providers.KindLocal,
			Status:          "healthy",
			Healthy:         true,
			CredentialState: "not_required",
			Models:          []string{"shared-model"},
		},
	}

	got := providerModelReadiness(entries, "", "shared-model")
	if !got.Ready {
		t.Fatalf("ready = false, want true: %#v", got)
	}
	if got.MatchedProvider != "alpha" {
		t.Fatalf("matched_provider = %q, want alpha", got.MatchedProvider)
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
