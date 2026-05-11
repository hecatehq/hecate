package router

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/hecate/agent-runtime/internal/catalog"
	"github.com/hecate/agent-runtime/internal/providers"
	"github.com/hecate/agent-runtime/pkg/types"
)

func TestRuleRouterRoute(t *testing.T) {
	t.Parallel()

	registry := providers.NewRegistry(
		&fakeProvider{name: "openai", kind: providers.KindCloud, defaultModel: "gpt-4o-mini", supportedModels: []string{"gpt-4o-mini", "gpt-4.1-mini"}},
		&fakeProvider{name: "local", kind: providers.KindLocal, defaultModel: "llama3.1:8b", supportedModels: []string{"llama3.1:8b"}},
	)
	router := NewRuleRouter("gpt-4o-mini", catalog.NewRegistryCatalog(registry, nil))

	tests := []struct {
		name       string
		req        types.ChatRequest
		wantModel  string
		wantReason string
	}{
		{
			name: "explicit model wins",
			req: types.ChatRequest{
				Model: "gpt-4.1-mini",
			},
			wantModel:  "gpt-4.1-mini",
			wantReason: "requested_model",
		},
		{
			name:       "default model is selected",
			req:        types.ChatRequest{},
			wantModel:  "llama3.1:8b",
			wantReason: "provider_default_model",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := router.Route(context.Background(), tt.req)
			if err != nil {
				t.Fatalf("Route() error = %v", err)
			}
			if got.Model != tt.wantModel {
				t.Fatalf("Route() model = %q, want %q", got.Model, tt.wantModel)
			}
			if got.Reason != tt.wantReason {
				t.Fatalf("Route() reason = %q, want %q", got.Reason, tt.wantReason)
			}
		})
	}
}

func TestRuleRouterRouteLocalFirst(t *testing.T) {
	t.Parallel()

	registry := providers.NewRegistry(
		&fakeProvider{name: "openai", kind: providers.KindCloud, defaultModel: "gpt-4o-mini"},
		&fakeProvider{name: "local", kind: providers.KindLocal, defaultModel: "llama3.1:8b", supportedModels: []string{"llama3.1:8b"}},
	)
	router := NewRuleRouter("gpt-4o-mini", catalog.NewRegistryCatalog(registry, nil))

	got, err := router.Route(context.Background(), types.ChatRequest{})
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if got.Provider != "local" {
		t.Fatalf("Route() provider = %q, want local", got.Provider)
	}
	if got.Model != "llama3.1:8b" {
		t.Fatalf("Route() model = %q, want llama3.1:8b", got.Model)
	}
}

func TestRuleRouterOrdersProvidersAlphabetically(t *testing.T) {
	t.Parallel()

	registry := providers.NewRegistry(
		&fakeProvider{name: "openai", kind: providers.KindCloud, defaultModel: "gpt-5.4-mini"},
		&fakeProvider{name: "anthropic", kind: providers.KindCloud, defaultModel: "claude-sonnet-4-6", supportedModels: []string{"claude-sonnet-4-6"}},
		&fakeProvider{name: "ollama", kind: providers.KindLocal, defaultModel: "llama3.1:8b", supportedModels: []string{"llama3.1:8b"}},
	)
	router := NewRuleRouter("gpt-5.4-mini", catalog.NewRegistryCatalog(registry, nil))

	got, err := router.Route(context.Background(), types.ChatRequest{})
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if got.Provider != "anthropic" {
		t.Fatalf("Route() provider = %q, want anthropic", got.Provider)
	}

	fallbacks := router.Fallbacks(context.Background(), types.ChatRequest{}, got)
	if len(fallbacks) < 2 {
		t.Fatalf("Fallbacks() count = %d, want at least 2", len(fallbacks))
	}
	if fallbacks[0].Provider != "ollama" {
		t.Fatalf("Fallbacks()[0] provider = %q, want ollama", fallbacks[0].Provider)
	}
	if fallbacks[0].Model != "llama3.1:8b" {
		t.Fatalf("Fallbacks()[0] model = %q, want llama3.1:8b", fallbacks[0].Model)
	}
	if fallbacks[1].Provider != "openai" {
		t.Fatalf("Fallbacks()[1] provider = %q, want openai", fallbacks[1].Provider)
	}
	if fallbacks[1].Model != "gpt-5.4-mini" {
		t.Fatalf("Fallbacks()[1] model = %q, want gpt-5.4-mini", fallbacks[1].Model)
	}
}

type fakeProvider struct {
	name            string
	kind            providers.Kind
	defaultModel    string
	supportedModels []string
	capabilities    providers.Capabilities
	capabilitiesErr error
}

type staticHealthTracker struct {
	states map[string]providers.HealthState
}

func (t staticHealthTracker) Observe(string, providers.HealthObservation) {}
func (t staticHealthTracker) RecordSuccess(string)                        {}
func (t staticHealthTracker) RecordFailure(string, error)                 {}
func (t staticHealthTracker) State(provider string) providers.HealthState {
	if state, ok := t.states[provider]; ok {
		return state
	}
	return providers.HealthState{Available: true, Status: providers.HealthStatusHealthy}
}

func (p *fakeProvider) Name() string         { return p.name }
func (p *fakeProvider) Kind() providers.Kind { return p.kind }
func (p *fakeProvider) DefaultModel() string { return p.defaultModel }
func (p *fakeProvider) Chat(_ context.Context, _ types.ChatRequest) (*types.ChatResponse, error) {
	return nil, nil
}
func (p *fakeProvider) Capabilities(_ context.Context) (providers.Capabilities, error) {
	if p.capabilitiesErr != nil {
		return providers.Capabilities{
			Name:         p.name,
			Kind:         p.kind,
			DefaultModel: p.defaultModel,
			Models:       append([]string(nil), p.supportedModels...),
		}, p.capabilitiesErr
	}
	if p.capabilities.Name != "" || len(p.capabilities.Models) > 0 || p.capabilities.DefaultModel != "" {
		return p.capabilities, nil
	}
	return providers.Capabilities{
		Name:         p.name,
		Kind:         p.kind,
		DefaultModel: p.defaultModel,
		Models:       append([]string(nil), p.supportedModels...),
	}, nil
}
func (p *fakeProvider) Supports(model string) bool {
	for _, candidate := range p.supportedModels {
		if candidate == model {
			return true
		}
	}
	return p.defaultModel != "" && p.defaultModel == model
}

func TestRuleRouterUsesDiscoveredCapabilities(t *testing.T) {
	t.Parallel()

	registry := providers.NewRegistry(
		&fakeProvider{
			name:         "local",
			kind:         providers.KindLocal,
			defaultModel: "configured-model",
			capabilities: providers.Capabilities{
				Name:         "local",
				Kind:         providers.KindLocal,
				DefaultModel: "discovered-model",
				Models:       []string{"discovered-model", "specialized-model"},
			},
		},
	)
	router := NewRuleRouter("configured-model", catalog.NewRegistryCatalog(registry, nil))

	got, err := router.Route(context.Background(), types.ChatRequest{})
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if got.Model != "discovered-model" {
		t.Fatalf("Route() model = %q, want discovered-model", got.Model)
	}

	got, err = router.Route(context.Background(), types.ChatRequest{Model: "specialized-model"})
	if err != nil {
		t.Fatalf("Route() explicit error = %v", err)
	}
	if got.Provider != "local" {
		t.Fatalf("Route() provider = %q, want local", got.Provider)
	}
}

func TestRuleRouterHonorsExplicitProvider(t *testing.T) {
	t.Parallel()

	registry := providers.NewRegistry(
		&fakeProvider{name: "openai", kind: providers.KindCloud, defaultModel: "gpt-4o-mini"},
		&fakeProvider{name: "ollama", kind: providers.KindLocal, defaultModel: "llama3.1:8b", supportedModels: []string{"llama3.1:8b", "llama3.2:3b"}},
	)
	router := NewRuleRouter("gpt-4o-mini", catalog.NewRegistryCatalog(registry, nil))

	got, err := router.Route(context.Background(), types.ChatRequest{
		Scope: types.RequestScope{
			ProviderHint: "ollama",
		},
	})
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if got.Provider != "ollama" {
		t.Fatalf("Route() provider = %q, want ollama", got.Provider)
	}
	if got.Model != "llama3.1:8b" {
		t.Fatalf("Route() model = %q, want llama3.1:8b", got.Model)
	}
	if got.Reason != "pinned_provider" {
		t.Fatalf("Route() reason = %q, want pinned_provider", got.Reason)
	}
}

func TestRuleRouterLocalFirstFallsBackWhenLocalIsUnhealthy(t *testing.T) {
	t.Parallel()

	registry := providers.NewRegistry(
		&fakeProvider{name: "openai", kind: providers.KindCloud, defaultModel: "gpt-4o-mini"},
		&fakeProvider{
			name:            "ollama",
			kind:            providers.KindLocal,
			defaultModel:    "llama3.1:8b",
			supportedModels: []string{"llama3.1:8b"},
			capabilitiesErr: context.DeadlineExceeded,
		},
	)
	router := NewRuleRouter("gpt-4o-mini", catalog.NewRegistryCatalog(registry, nil))

	got, err := router.Route(context.Background(), types.ChatRequest{})
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if got.Provider != "openai" {
		t.Fatalf("Route() provider = %q, want openai", got.Provider)
	}
	if got.Reason != "provider_default_model" {
		t.Fatalf("Route() reason = %q, want provider_default_model", got.Reason)
	}
}

func TestRuleRouterFallbacksUseAlphabeticalProviderOrder(t *testing.T) {
	t.Parallel()

	registry := providers.NewRegistry(
		&fakeProvider{name: "openai", kind: providers.KindCloud, defaultModel: "gpt-4o-mini"},
		&fakeProvider{name: "anthropic", kind: providers.KindCloud, defaultModel: "claude-sonnet", supportedModels: []string{"claude-sonnet"}},
		&fakeProvider{name: "ollama", kind: providers.KindLocal, defaultModel: "llama3.1:8b", supportedModels: []string{"llama3.1:8b"}},
	)
	router := NewRuleRouter("gpt-4o-mini", catalog.NewRegistryCatalog(registry, nil))

	current := types.RouteDecision{Provider: "ollama", Model: "llama3.1:8b", Reason: "provider_default_model"}
	fallbacks := router.Fallbacks(context.Background(), types.ChatRequest{}, current)
	if len(fallbacks) < 2 {
		t.Fatalf("Fallbacks() count = %d, want at least 2", len(fallbacks))
	}
	if fallbacks[0].Provider != "anthropic" {
		t.Fatalf("Fallbacks()[0] provider = %q, want anthropic", fallbacks[0].Provider)
	}
	if fallbacks[0].Model != "claude-sonnet" {
		t.Fatalf("Fallbacks()[0] model = %q, want claude-sonnet", fallbacks[0].Model)
	}
	if fallbacks[0].Reason != "provider_default_model_failover" {
		t.Fatalf("Fallbacks()[0] reason = %q, want failover reason", fallbacks[0].Reason)
	}
}

func TestRuleRouterFallbacksEmptyForExplicitProvider(t *testing.T) {
	t.Parallel()

	registry := providers.NewRegistry(
		&fakeProvider{name: "openai", kind: providers.KindCloud, defaultModel: "gpt-4o-mini"},
		&fakeProvider{name: "ollama", kind: providers.KindLocal, defaultModel: "llama3.1:8b", supportedModels: []string{"llama3.1:8b"}},
	)
	router := NewRuleRouter("gpt-4o-mini", catalog.NewRegistryCatalog(registry, nil))

	fallbacks := router.Fallbacks(context.Background(), types.ChatRequest{
		Scope: types.RequestScope{ProviderHint: "ollama"},
	}, types.RouteDecision{Provider: "ollama", Model: "llama3.1:8b", Reason: "pinned_provider"})
	if len(fallbacks) != 0 {
		t.Fatalf("Fallbacks() count = %d, want 0 for explicit provider", len(fallbacks))
	}
}

func TestRuleRouterSkipsDegradedAlphabeticalProvider(t *testing.T) {
	t.Parallel()

	registry := providers.NewRegistry(
		&fakeProvider{name: "openai", kind: providers.KindCloud, defaultModel: "gpt-4o-mini"},
		&fakeProvider{name: "anthropic", kind: providers.KindCloud, defaultModel: "claude-sonnet", supportedModels: []string{"claude-sonnet"}},
	)
	tracker := providers.NewMemoryHealthTracker(1, time.Minute)
	tracker.RecordFailure("openai", context.DeadlineExceeded)

	router := NewRuleRouter("gpt-4o-mini", catalog.NewRegistryCatalog(registry, tracker))

	got, err := router.Route(context.Background(), types.ChatRequest{})
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if got.Provider != "anthropic" {
		t.Fatalf("Route() provider = %q, want anthropic", got.Provider)
	}
	if got.Reason != "provider_default_model" {
		t.Fatalf("Route() reason = %q, want provider_default_model", got.Reason)
	}
}

func TestRuleRouterLocalFirstExplicitModelSkipsUnhealthyFallbackProvider(t *testing.T) {
	t.Parallel()

	registry := providers.NewRegistry(
		&fakeProvider{name: "openai", kind: providers.KindCloud, defaultModel: "gpt-4o-mini", supportedModels: []string{"gpt-4o-mini", "gpt-4.1-mini"}},
		&fakeProvider{name: "anthropic", kind: providers.KindCloud, defaultModel: "claude-sonnet", supportedModels: []string{"claude-sonnet", "gpt-4.1-mini"}},
		&fakeProvider{
			name:            "ollama",
			kind:            providers.KindLocal,
			defaultModel:    "llama3.1:8b",
			supportedModels: []string{"llama3.1:8b"},
			capabilitiesErr: context.DeadlineExceeded,
		},
	)
	tracker := providers.NewMemoryHealthTracker(1, time.Minute)
	tracker.RecordFailure("openai", context.DeadlineExceeded)

	router := NewRuleRouter("gpt-4o-mini", catalog.NewRegistryCatalog(registry, tracker))

	got, err := router.Route(context.Background(), types.ChatRequest{Model: "gpt-4.1-mini"})
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if got.Provider != "anthropic" {
		t.Fatalf("Route() provider = %q, want anthropic", got.Provider)
	}
	if got.Reason != "requested_model" {
		t.Fatalf("Route() reason = %q, want requested_model", got.Reason)
	}
}

func TestRuleRouterLocalFirstDefaultModelSkipsUnhealthyFallbackProvider(t *testing.T) {
	t.Parallel()

	registry := providers.NewRegistry(
		&fakeProvider{name: "openai", kind: providers.KindCloud, defaultModel: "gpt-4o-mini"},
		&fakeProvider{name: "anthropic", kind: providers.KindCloud, defaultModel: "claude-sonnet", supportedModels: []string{"claude-sonnet"}},
		&fakeProvider{
			name:            "ollama",
			kind:            providers.KindLocal,
			defaultModel:    "llama3.1:8b",
			supportedModels: []string{"llama3.1:8b"},
			capabilitiesErr: context.DeadlineExceeded,
		},
	)
	tracker := providers.NewMemoryHealthTracker(1, time.Minute)
	tracker.RecordFailure("openai", context.DeadlineExceeded)

	router := NewRuleRouter("gpt-4o-mini", catalog.NewRegistryCatalog(registry, tracker))

	got, err := router.Route(context.Background(), types.ChatRequest{})
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if got.Provider != "anthropic" {
		t.Fatalf("Route() provider = %q, want anthropic", got.Provider)
	}
	if got.Reason != "provider_default_model" {
		t.Fatalf("Route() reason = %q, want provider_default_model", got.Reason)
	}
}

func TestRuleRouterFallbacksSkipDegradedProviders(t *testing.T) {
	t.Parallel()

	registry := providers.NewRegistry(
		&fakeProvider{name: "openai", kind: providers.KindCloud, defaultModel: "gpt-4o-mini"},
		&fakeProvider{name: "anthropic", kind: providers.KindCloud, defaultModel: "claude-sonnet", supportedModels: []string{"claude-sonnet"}},
		&fakeProvider{name: "ollama", kind: providers.KindLocal, defaultModel: "llama3.1:8b", supportedModels: []string{"llama3.1:8b"}},
	)
	tracker := providers.NewMemoryHealthTracker(1, time.Minute)
	tracker.RecordFailure("openai", context.DeadlineExceeded)

	router := NewRuleRouter("gpt-4o-mini", catalog.NewRegistryCatalog(registry, tracker))

	current := types.RouteDecision{Provider: "ollama", Model: "llama3.1:8b", Reason: "provider_default_model"}
	fallbacks := router.Fallbacks(context.Background(), types.ChatRequest{}, current)
	if len(fallbacks) != 1 {
		t.Fatalf("Fallbacks() count = %d, want 1", len(fallbacks))
	}
	if fallbacks[0].Provider != "anthropic" {
		t.Fatalf("Fallbacks()[0] provider = %q, want anthropic", fallbacks[0].Provider)
	}
}

func TestRuleRouterPrefersHealthyOverHalfOpen(t *testing.T) {
	t.Parallel()

	registry := providers.NewRegistry(
		&fakeProvider{name: "openai", kind: providers.KindCloud, defaultModel: "gpt-4o-mini"},
		&fakeProvider{name: "anthropic", kind: providers.KindCloud, defaultModel: "claude-sonnet", supportedModels: []string{"claude-sonnet"}},
	)
	tracker := staticHealthTracker{states: map[string]providers.HealthState{
		"openai": {Available: true, Status: providers.HealthStatusHalfOpen},
	}}

	router := NewRuleRouter("gpt-4o-mini", catalog.NewRegistryCatalog(registry, tracker))

	got, err := router.Route(context.Background(), types.ChatRequest{})
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if got.Provider != "anthropic" {
		t.Fatalf("Route() provider = %q, want anthropic healthy provider ahead of half_open default", got.Provider)
	}
}

func TestRuleRouterUsesHalfOpenRecoveryWhenNoHealthyAlternativeExists(t *testing.T) {
	t.Parallel()

	registry := providers.NewRegistry(
		&fakeProvider{name: "openai", kind: providers.KindCloud, defaultModel: "gpt-4o-mini"},
	)
	tracker := staticHealthTracker{states: map[string]providers.HealthState{
		"openai": {Available: true, Status: providers.HealthStatusHalfOpen},
	}}

	router := NewRuleRouter("gpt-4o-mini", catalog.NewRegistryCatalog(registry, tracker))

	got, err := router.Route(context.Background(), types.ChatRequest{})
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if got.Provider != "openai" {
		t.Fatalf("Route() provider = %q, want openai half_open recovery route", got.Provider)
	}
	if got.Reason != "provider_default_model_half_open_recovery" {
		t.Fatalf("Route() reason = %q, want provider_default_model_half_open_recovery", got.Reason)
	}
}

func TestRuleRouterFallbacksPreferHealthyBeforeHalfOpen(t *testing.T) {
	t.Parallel()

	registry := providers.NewRegistry(
		&fakeProvider{name: "openai", kind: providers.KindCloud, defaultModel: "gpt-4o-mini"},
		&fakeProvider{name: "anthropic", kind: providers.KindCloud, defaultModel: "claude-sonnet", supportedModels: []string{"claude-sonnet"}},
		&fakeProvider{name: "gemini", kind: providers.KindCloud, defaultModel: "gemini-flash", supportedModels: []string{"gemini-flash"}},
	)
	tracker := staticHealthTracker{states: map[string]providers.HealthState{
		"openai": {Available: true, Status: providers.HealthStatusHalfOpen},
	}}

	router := NewRuleRouter("claude-sonnet", catalog.NewRegistryCatalog(registry, tracker))

	current := types.RouteDecision{Provider: "gemini", Model: "gemini-flash", Reason: "provider_default_model"}
	fallbacks := router.Fallbacks(context.Background(), types.ChatRequest{}, current)
	if len(fallbacks) < 2 {
		t.Fatalf("Fallbacks() count = %d, want at least 2", len(fallbacks))
	}
	if fallbacks[0].Provider != "anthropic" {
		t.Fatalf("Fallbacks()[0] provider = %q, want healthy anthropic first", fallbacks[0].Provider)
	}
	if fallbacks[1].Provider != "openai" {
		t.Fatalf("Fallbacks()[1] provider = %q, want half_open openai second", fallbacks[1].Provider)
	}
	if fallbacks[1].Reason != "provider_default_model_failover_half_open_recovery" {
		t.Fatalf("Fallbacks()[1] reason = %q, want half_open recovery failover reason", fallbacks[1].Reason)
	}
}

func TestRuleRouterSkipsRateLimitedProviderImmediately(t *testing.T) {
	t.Parallel()

	registry := providers.NewRegistry(
		&fakeProvider{name: "openai", kind: providers.KindCloud, defaultModel: "gpt-4o-mini", supportedModels: []string{"gpt-4o-mini"}},
		&fakeProvider{name: "anthropic", kind: providers.KindCloud, defaultModel: "gpt-4o-mini", supportedModels: []string{"gpt-4o-mini"}},
	)
	tracker := providers.NewMemoryHealthTracker(3, time.Minute)
	tracker.RecordFailure("openai", &providers.UpstreamError{StatusCode: http.StatusTooManyRequests, Type: "rate_limit"})

	router := NewRuleRouter("gpt-4o-mini", catalog.NewRegistryCatalog(registry, tracker))
	got, err := router.Route(context.Background(), types.ChatRequest{})
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if got.Provider != "anthropic" {
		t.Fatalf("Route() provider = %q, want anthropic after openai 429 cooldown", got.Provider)
	}
}

func TestRuleRouterPrefersLowerLatencyWithinSameHealthTier(t *testing.T) {
	t.Parallel()

	registry := providers.NewRegistry(
		&fakeProvider{name: "anthropic", kind: providers.KindCloud, defaultModel: "claude-sonnet", supportedModels: []string{"claude-sonnet"}},
		&fakeProvider{name: "openai", kind: providers.KindCloud, defaultModel: "gpt-4o-mini", supportedModels: []string{"gpt-4o-mini"}},
	)
	tracker := staticHealthTracker{states: map[string]providers.HealthState{
		"anthropic": {Available: true, Status: providers.HealthStatusHealthy, LastLatency: 900 * time.Millisecond},
		"openai":    {Available: true, Status: providers.HealthStatusHealthy, LastLatency: 120 * time.Millisecond},
	}}

	router := NewRuleRouter("gpt-4o-mini", catalog.NewRegistryCatalog(registry, tracker))
	got, err := router.Route(context.Background(), types.ChatRequest{})
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if got.Provider != "openai" {
		t.Fatalf("Route() provider = %q, want lower-latency openai over alphabetical slower anthropic", got.Provider)
	}
}

func TestRuleRouterPrefersMoreStableProviderAtSameHealthTier(t *testing.T) {
	t.Parallel()

	registry := providers.NewRegistry(
		&fakeProvider{name: "anthropic", kind: providers.KindCloud, defaultModel: "claude-sonnet", supportedModels: []string{"claude-sonnet"}},
		&fakeProvider{name: "openai", kind: providers.KindCloud, defaultModel: "gpt-4o-mini", supportedModels: []string{"gpt-4o-mini"}},
	)
	tracker := staticHealthTracker{states: map[string]providers.HealthState{
		"anthropic": {Available: true, Status: providers.HealthStatusHealthy, TotalFailures: 12, ServerErrors: 4, LastLatency: 200 * time.Millisecond},
		"openai":    {Available: true, Status: providers.HealthStatusHealthy, LastLatency: 200 * time.Millisecond},
	}}

	router := NewRuleRouter("gpt-4o-mini", catalog.NewRegistryCatalog(registry, tracker))

	got, err := router.Route(context.Background(), types.ChatRequest{})
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if got.Provider != "openai" {
		t.Fatalf("Route() provider = %q, want more stable openai", got.Provider)
	}
}
