package gateway

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/catalog"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/governor"
	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestServiceHandleChatFallsBackWhenPrimaryFails(t *testing.T) {
	t.Parallel()

	primary := &sequenceProvider{
		name: "openai",
		kind: providers.KindCloud,
		responses: []providerResponse{
			{err: &providers.UpstreamError{StatusCode: 503}},
		},
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
	store := governor.NewMemoryUsageStore()
	router := staticFallbackRouter{
		route: types.RouteDecision{Provider: "openai", Model: "model-x", Reason: "primary"},
		fallbacks: []types.RouteDecision{
			{Provider: "ollama", Model: "model-b", Reason: "fallback"},
		},
	}
	service := NewService(Dependencies{
		Logger:     slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Router:     router,
		Governor:   governor.NewStaticGovernor(config.GovernorConfig{MaxPromptTokens: 64_000}, store, store),
		Providers:  registry,
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

func TestServiceHandleChatReturnsAttemptedRouteMetadataOnProviderError(t *testing.T) {
	t.Parallel()

	provider := &sequenceProvider{
		name: "vision",
		kind: providers.KindCloud,
		responses: []providerResponse{
			{err: &providers.UpstreamError{StatusCode: 400, Message: "image rejected"}},
		},
	}
	registry := providers.NewRegistry(provider)
	providerInstance, _ := registry.GetInstance("vision")
	store := governor.NewMemoryUsageStore()
	service := NewService(Dependencies{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Router:    staticFallbackRouter{route: types.RouteDecision{Provider: "vision", ProviderKind: string(providers.KindCloud), ProviderInstance: providerInstance.Identity, Model: "model-a", Reason: "auto_image"}},
		Governor:  governor.NewStaticGovernor(config.GovernorConfig{MaxPromptTokens: 64_000}, store, store),
		Providers: registry,
		Tracer:    profiler.NewInMemoryTracer(nil),
		Resilience: ResilienceOptions{
			MaxAttempts: 1,
		},
	})

	result, err := service.HandleChat(context.Background(), types.ChatRequest{
		RequestID:    "req-failed-image",
		Model:        "model-a",
		Messages:     []types.Message{{Role: "user", Content: "image"}},
		Requirements: types.ChatRequestRequirements{ImageInput: true, NoProviderFailover: true},
	})
	if err == nil {
		t.Fatal("HandleChat() error = nil, want provider failure")
	}
	if result == nil || result.Response != nil {
		t.Fatalf("HandleChat() result = %+v, want metadata-only failure result", result)
	}
	if result.Metadata.RequestID != "req-failed-image" || result.Metadata.Provider != "vision" || result.Metadata.Model != "model-a" {
		t.Fatalf("attempted route metadata = %+v, want request/provider/model attribution", result.Metadata)
	}
	if result.Metadata.AttemptCount != 1 || result.Metadata.TraceID == "" || result.Metadata.SpanID == "" {
		t.Fatalf("attempt/trace metadata = %+v, want one correlated provider attempt", result.Metadata)
	}
}

func TestServiceListModelsCarriesCapabilityFamilySeparatelyFromProvider(t *testing.T) {
	t.Parallel()

	provider := providers.NewOpenAICompatibleProvider(config.OpenAICompatibleProviderConfig{
		Name:           "OpenAI Production",
		Aliases:        []string{"openai-prod", "openai"},
		ProviderFamily: "openai",
		Kind:           "cloud",
		Protocol:       "openai",
		StubMode:       true,
		KnownModels:    []string{"gpt-4o"},
		DefaultModel:   "gpt-4o",
		Enabled:        true,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	registry := providers.NewRegistry(provider)
	service := NewService(Dependencies{Providers: registry})

	result, err := service.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(result.Models) != 1 {
		t.Fatalf("models len = %d, want 1", len(result.Models))
	}
	if got := result.Models[0]; got.Provider != "OpenAI Production" || got.ProviderFamily != "openai" || len(got.ProviderAliases) != 2 || got.ProviderAliases[0] != "openai-prod" {
		t.Fatalf("model provider identity = provider %q aliases %#v family %q, want configured provider plus stable aliases and canonical family", got.Provider, got.ProviderAliases, got.ProviderFamily)
	}
	if len(result.ProviderIdentities) != 1 || result.ProviderIdentities[0].Name != "OpenAI Production" || len(result.ProviderIdentities[0].Aliases) != 2 {
		t.Fatalf("provider identity inventory = %+v, want configured provider independent of model rows", result.ProviderIdentities)
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
		tt := tt
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
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := providerHealthCheck(tt.entry)
			assertReadinessCheck(t, got, "health", tt.wantStatus, tt.wantReason)
		})
	}
}

func TestProviderReadinessSummary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		entry      catalog.Entry
		checks     []types.ProviderReadinessCheck
		wantStatus string
		wantReason string
	}{
		{
			name:       "unknown when no checks exist",
			entry:      catalog.Entry{Name: "ollama"},
			wantStatus: "unknown",
			wantReason: "unknown",
		},
		{
			name: "routing block wins over earlier block",
			entry: catalog.Entry{
				Name: "anthropic",
			},
			checks: []types.ProviderReadinessCheck{
				{Name: "credentials", Status: "blocked", Reason: "credential_missing", Message: "Credentials are missing.", OperatorAction: "Add an API key."},
				{Name: "routing", Status: "blocked", Reason: "credential_missing", Message: "Credentials are missing.", OperatorAction: "Add an API key."},
			},
			wantStatus: "blocked",
			wantReason: "credential_missing",
		},
		{
			name: "first warning is surfaced when routable",
			entry: catalog.Entry{
				Name: "openrouter",
			},
			checks: []types.ProviderReadinessCheck{
				{Name: "credentials", Status: "ok", Reason: "configured", Message: "Credentials configured."},
				{Name: "models", Status: "warning", Reason: "default_model_only", Message: "Default model only."},
				{Name: "routing", Status: "ok", Reason: "routable", Message: "Provider is routable."},
			},
			wantStatus: "warning",
			wantReason: "default_model_only",
		},
		{
			name: "all ok is ready",
			entry: catalog.Entry{
				Name: "ollama",
			},
			checks: []types.ProviderReadinessCheck{
				{Name: "credentials", Status: "ok", Reason: "not_required", Message: "No credentials required."},
				{Name: "models", Status: "ok", Reason: "models_discovered", Message: "Models discovered."},
				{Name: "health", Status: "ok", Reason: "healthy", Message: "Provider is healthy."},
				{Name: "routing", Status: "ok", Reason: "routable", Message: "Provider is routable."},
			},
			wantStatus: "ok",
			wantReason: "ready",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := providerReadinessSummary(tt.entry, tt.checks)
			if got.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q", got.Status, tt.wantStatus)
			}
			if got.Reason != tt.wantReason {
				t.Fatalf("reason = %q, want %q", got.Reason, tt.wantReason)
			}
			if got.Message == "" {
				t.Fatal("message is empty")
			}
			if got.Status == "blocked" && got.OperatorAction == "" {
				t.Fatalf("operator_action is empty for blocked summary: %#v", got)
			}
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
		{
			Name:            "Fake Dogfood",
			Kind:            providers.KindCloud,
			Status:          "healthy",
			Healthy:         true,
			CredentialState: "configured",
			Models:          []string{"dogfood-model"},
		},
		{
			Provider:        &sequenceProvider{name: "Runtime Custom", aliases: []string{"custom-provider-id"}, kind: providers.KindCloud},
			Name:            "Runtime Custom",
			Kind:            providers.KindCloud,
			Status:          "healthy",
			Healthy:         true,
			CredentialState: "configured",
			Models:          []string{"custom-model"},
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
		wantStatus          string
		wantSuggestions     bool
		rejectSuggestion    string
		wantActionContains  string
	}{
		{
			name:                "explicit provider reports model",
			provider:            "ollama",
			model:               "llama3.1:8b",
			wantReady:           true,
			wantProvider:        "ollama",
			wantReason:          "model_available",
			wantMatchedProvider: "ollama",
			wantStatus:          "ok",
		},
		{
			name:                "explicit provider match returns canonical id",
			provider:            "Ollama",
			model:               "llama3.1:8b",
			wantReady:           true,
			wantProvider:        "ollama",
			wantReason:          "model_available",
			wantMatchedProvider: "ollama",
			wantStatus:          "ok",
		},
		{
			name:                "explicit provider id matches display name slug",
			provider:            "fake-dogfood",
			model:               "dogfood-model",
			wantReady:           true,
			wantProvider:        "Fake Dogfood",
			wantReason:          "model_available",
			wantMatchedProvider: "Fake Dogfood",
			wantStatus:          "ok",
		},
		{
			name:                "explicit provider id matches reported alias",
			provider:            "custom-provider-id",
			model:               "custom-model",
			wantReady:           true,
			wantProvider:        "Runtime Custom",
			wantReason:          "model_available",
			wantMatchedProvider: "Runtime Custom",
			wantStatus:          "ok",
		},
		{
			name:                "explicit provider missing selected model",
			provider:            "ollama",
			model:               "gpt-5.4-mini",
			wantProvider:        "ollama",
			wantReason:          "model_not_discovered",
			wantMatchedProvider: "ollama",
			wantStatus:          "blocked",
		},
		{
			name:                "explicit provider blocked before model use",
			provider:            "anthropic",
			model:               "claude-sonnet-4-5",
			wantProvider:        "anthropic",
			wantReason:          "provider_not_ready",
			wantMatchedProvider: "anthropic",
			wantBlockedReason:   "credential_missing",
			wantStatus:          "blocked",
			wantSuggestions:     true,
		},
		{
			name:               "missing provider",
			provider:           "missing",
			model:              "llama3.1:8b",
			wantProvider:       "missing",
			wantReason:         "provider_missing",
			wantStatus:         "blocked",
			wantActionContains: "Connections",
		},
		{
			name:                "auto finds routable provider",
			model:               "mistral:latest",
			wantReady:           true,
			wantProvider:        "auto",
			wantReason:          "auto_route_available",
			wantMatchedProvider: "ollama",
			wantStatus:          "ok",
		},
		{
			name:            "auto cannot find selected model",
			model:           "gpt-5.4-mini",
			wantProvider:    "auto",
			wantReason:      "model_not_discovered",
			wantStatus:      "blocked",
			wantSuggestions: true,
		},
		{
			name:             "explicit provider model required",
			provider:         "ollama",
			wantProvider:     "ollama",
			wantReason:       "model_required",
			wantStatus:       "blocked",
			wantSuggestions:  true,
			rejectSuggestion: "claude-sonnet-4-5",
		},
		{
			name:            "auto model required",
			wantProvider:    "auto",
			wantReason:      "model_required",
			wantStatus:      "blocked",
			wantSuggestions: true,
		},
	}

	for _, tt := range tests {
		tt := tt
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
			if rendered := got.ToModelReadiness(); rendered.Status != tt.wantStatus {
				t.Fatalf("rendered status = %q, want %q for %#v", rendered.Status, tt.wantStatus, rendered)
			}
			if !got.Ready && got.OperatorAction == "" {
				t.Fatalf("operator_action is empty for blocked readiness: %#v", got)
			}
			if tt.wantActionContains != "" && !strings.Contains(got.OperatorAction, tt.wantActionContains) {
				t.Fatalf("operator_action = %q, want substring %q", got.OperatorAction, tt.wantActionContains)
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

func TestProviderModelReadinessPrefersCanonicalProviderOverAlias(t *testing.T) {
	t.Parallel()

	entries := []catalog.Entry{
		{
			Name:            "vision-a",
			ProviderAliases: []string{"vision-b"},
			Kind:            providers.KindCloud,
			Status:          "healthy",
			Healthy:         true,
			CredentialState: "configured",
			Models:          []string{"alias-model"},
		},
		{
			Name:            "vision-b",
			Kind:            providers.KindCloud,
			Status:          "healthy",
			Healthy:         true,
			CredentialState: "configured",
			Models:          []string{"canonical-model"},
		},
	}

	readiness := providerModelReadiness(entries, "vision-b", "canonical-model")
	if !readiness.Ready || readiness.MatchedProvider != "vision-b" || readiness.Reason != "model_available" {
		t.Fatalf("providerModelReadiness() = %+v, want canonical vision-b ready", readiness)
	}

	missingModel := providerModelReadiness(entries, "vision-b", "")
	if missingModel.Reason != "model_required" || len(missingModel.SuggestedModels) != 1 || missingModel.SuggestedModels[0] != "canonical-model" {
		t.Fatalf("providerModelReadiness(model required) = %+v, want only canonical provider suggestions", missingModel)
	}
}

func TestProviderModelReadinessRejectsAmbiguousProviderAlias(t *testing.T) {
	t.Parallel()

	entries := []catalog.Entry{
		{
			Name:            "vision-a",
			ProviderAliases: []string{"shared-vision"},
			Kind:            providers.KindCloud,
			Status:          "healthy",
			Healthy:         true,
			CredentialState: "configured",
			Models:          []string{"vision-model"},
		},
		{
			Name:            "vision-b",
			ProviderAliases: []string{"shared-vision"},
			Kind:            providers.KindCloud,
			Status:          "healthy",
			Healthy:         true,
			CredentialState: "configured",
			Models:          []string{"vision-model"},
		},
	}

	for _, model := range []string{"vision-model", ""} {
		readiness := providerModelReadiness(entries, "shared-vision", model)
		if readiness.Ready || readiness.Reason != "provider_ambiguous" || readiness.OperatorAction == "" || len(readiness.SuggestedModels) != 0 {
			t.Fatalf("providerModelReadiness(model=%q) = %+v, want fail-closed ambiguous provider without suggestions", model, readiness)
		}
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
