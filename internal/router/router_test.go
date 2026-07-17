package router

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/catalog"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/pkg/types"
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
	aliases         []string
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
func (p *fakeProvider) Aliases() []string    { return append([]string(nil), p.aliases...) }
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

func TestRuleRouterFiltersImageInputCandidatesAndFallbacks(t *testing.T) {
	t.Parallel()

	capabilities := func(name, imageInput string) providers.Capabilities {
		return providers.Capabilities{
			Name:            name,
			Kind:            providers.KindCloud,
			DefaultModel:    "shared-model",
			Models:          []string{"shared-model"},
			DiscoverySource: "provider",
			ModelCapabilities: map[string]types.ModelCapabilities{
				"shared-model": {ImageInput: imageInput, Source: "provider"},
			},
		}
	}
	registry := providers.NewRegistry(
		&fakeProvider{name: "a-text", kind: providers.KindCloud, defaultModel: "shared-model", supportedModels: []string{"shared-model"}, capabilities: capabilities("a-text", "none")},
		&fakeProvider{name: "b-vision", kind: providers.KindCloud, defaultModel: "shared-model", supportedModels: []string{"shared-model"}, capabilities: capabilities("b-vision", "supported")},
		&fakeProvider{name: "c-vision", kind: providers.KindCloud, defaultModel: "shared-model", supportedModels: []string{"shared-model"}, capabilities: capabilities("c-vision", "supported")},
	)
	router := NewRuleRouter("shared-model", catalog.NewRegistryCatalog(registry, nil))
	req := types.ChatRequest{
		Model:        "shared-model",
		Requirements: types.ChatRequestRequirements{ImageInput: true},
		Messages: []types.Message{{
			Role: "user",
			ContentBlocks: []types.ContentBlock{{
				Type:  "image_url",
				Image: &types.ContentImage{URL: "data:image/png;base64,aW1hZ2U="},
			}},
		}},
	}

	got, err := router.Route(context.Background(), req)
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if got.Provider != "b-vision" {
		t.Fatalf("Route() provider = %q, want b-vision", got.Provider)
	}
	fallbacks := router.Fallbacks(context.Background(), req, got)
	if len(fallbacks) != 1 || fallbacks[0].Provider != "c-vision" {
		t.Fatalf("Fallbacks() = %+v, want only c-vision", fallbacks)
	}

	req.Scope.ProviderHint = "a-text"
	if _, err := router.Route(context.Background(), req); err == nil {
		t.Fatal("Route() pinned text-only provider error = nil")
	}
}

func TestRuleRouterRequiresImageAndToolCapabilitiesOnSameCandidate(t *testing.T) {
	t.Parallel()

	capabilities := func(name, imageInput, toolCalling string) providers.Capabilities {
		return providers.Capabilities{
			Name:            name,
			Kind:            providers.KindCloud,
			DefaultModel:    "shared-model",
			Models:          []string{"shared-model"},
			DiscoverySource: "provider",
			ModelCapabilities: map[string]types.ModelCapabilities{
				"shared-model": {
					ImageInput:  imageInput,
					ToolCalling: toolCalling,
					Source:      "provider",
				},
			},
		}
	}
	newProvider := func(name, imageInput, toolCalling string) *fakeProvider {
		return &fakeProvider{
			name:            name,
			kind:            providers.KindCloud,
			defaultModel:    "shared-model",
			supportedModels: []string{"shared-model"},
			capabilities:    capabilities(name, imageInput, toolCalling),
		}
	}
	req := types.ChatRequest{
		Model: "shared-model",
		Requirements: types.ChatRequestRequirements{
			ImageInput:  true,
			ToolCalling: true,
		},
	}

	t.Run("selects only the capability intersection", func(t *testing.T) {
		t.Parallel()

		registry := providers.NewRegistry(
			newProvider("a-tools-only", "none", "parallel"),
			newProvider("b-image-only", "supported", "none"),
			newProvider("c-image-tools", "supported", "basic"),
		)
		router := NewRuleRouter("shared-model", catalog.NewRegistryCatalog(registry, nil))

		got, err := router.Route(context.Background(), req)
		if err != nil {
			t.Fatalf("Route() error = %v", err)
		}
		if got.Provider != "c-image-tools" {
			t.Fatalf("Route() provider = %q, want c-image-tools", got.Provider)
		}
		if fallbacks := router.Fallbacks(context.Background(), req, got); len(fallbacks) != 0 {
			t.Fatalf("Fallbacks() = %+v, want no split-capability candidates", fallbacks)
		}
	})

	t.Run("rejects split capabilities across providers", func(t *testing.T) {
		t.Parallel()

		registry := providers.NewRegistry(
			newProvider("a-tools-only", "none", "parallel"),
			newProvider("b-image-only", "supported", "none"),
		)
		router := NewRuleRouter("shared-model", catalog.NewRegistryCatalog(registry, nil))

		if _, err := router.Route(context.Background(), req); err == nil {
			t.Fatal("Route() error = nil, want split capabilities rejected")
		}
	})
}

func TestRuleRouterRejectsExpectedProviderInstanceAfterSameNameReplacement(t *testing.T) {
	t.Parallel()

	newVisionProvider := func() *fakeProvider {
		return &fakeProvider{
			name:            "vision",
			kind:            providers.KindCloud,
			defaultModel:    "vision-model",
			supportedModels: []string{"vision-model"},
			capabilities: providers.Capabilities{
				Name:            "vision",
				Kind:            providers.KindCloud,
				DefaultModel:    "vision-model",
				Models:          []string{"vision-model"},
				DiscoverySource: "provider",
				ModelCapabilities: map[string]types.ModelCapabilities{
					"vision-model": {ImageInput: "supported", Source: "provider"},
				},
			},
		}
	}
	registry := providers.NewMutableRegistry(newVisionProvider())
	router := NewRuleRouter("", catalog.NewRegistryCatalog(registry, nil))
	request := types.ChatRequest{
		Model: "vision-model",
		Scope: types.RequestScope{ProviderHint: "vision"},
		Requirements: types.ChatRequestRequirements{
			ImageInput:    true,
			ExactProvider: true,
		},
	}
	admitted, err := router.Route(context.Background(), request)
	if err != nil {
		t.Fatalf("initial Route() error = %v", err)
	}
	if !admitted.ProviderInstance.Valid() {
		t.Fatalf("initial provider instance = %+v, want execution fence", admitted.ProviderInstance)
	}

	request.Requirements.ProviderInstance = admitted.ProviderInstance
	registry.Replace(newVisionProvider())
	if _, err := router.Route(context.Background(), request); err == nil || !strings.Contains(err.Error(), "configuration changed during image admission") {
		t.Fatalf("Route() error = %v, want expected-instance rejection", err)
	}
}

func TestRuleRouterUsesCanonicalProviderFamilyForImageInput(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	openAI := providers.NewOpenAICompatibleProvider(config.OpenAICompatibleProviderConfig{
		Name:           "OpenAI Production",
		ProviderFamily: "openai",
		Kind:           "cloud",
		Protocol:       "openai",
		StubMode:       true,
		KnownModels:    []string{"gpt-4o"},
		DefaultModel:   "gpt-4o",
		Enabled:        true,
	}, logger)
	anthropic := providers.NewAnthropicProvider(config.OpenAICompatibleProviderConfig{
		Name:         "Anthropic Production",
		Kind:         "cloud",
		Protocol:     "anthropic",
		StubMode:     true,
		KnownModels:  []string{"claude-sonnet-4-6"},
		DefaultModel: "claude-sonnet-4-6",
		Enabled:      true,
	}, logger)
	router := NewRuleRouter("", catalog.NewRegistryCatalog(providers.NewRegistry(openAI, anthropic), nil))

	for _, tt := range []struct {
		name     string
		provider string
		model    string
	}{
		{name: "renamed OpenAI preset", provider: "OpenAI Production", model: "gpt-4o"},
		{name: "renamed Anthropic protocol", provider: "Anthropic Production", model: "claude-sonnet-4-6"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := router.Route(context.Background(), types.ChatRequest{
				Model:        tt.model,
				Scope:        types.RequestScope{ProviderHint: tt.provider},
				Requirements: types.ChatRequestRequirements{ImageInput: true},
			})
			if err != nil {
				t.Fatalf("Route() error = %v", err)
			}
			if got.Provider != tt.provider {
				t.Fatalf("Route() provider = %q, want configured routing id %q", got.Provider, tt.provider)
			}
		})
	}
}

func TestRuleRouterDoesNotTreatGenericOpenAIEndpointAsOpenAIFamily(t *testing.T) {
	t.Parallel()

	provider := providers.NewOpenAICompatibleProvider(config.OpenAICompatibleProviderConfig{
		Name:         "openai",
		Kind:         "cloud",
		Protocol:     "openai",
		StubMode:     true,
		KnownModels:  []string{"gpt-4o"},
		DefaultModel: "gpt-4o",
		Enabled:      true,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	router := NewRuleRouter("", catalog.NewRegistryCatalog(providers.NewRegistry(provider), nil))

	_, err := router.Route(context.Background(), types.ChatRequest{
		Model:        "gpt-4o",
		Scope:        types.RequestScope{ProviderHint: "openai"},
		Requirements: types.ChatRequestRequirements{ImageInput: true},
	})
	if err == nil {
		t.Fatal("Route() error = nil, want generic OpenAI-compatible endpoint to remain image-input unknown")
	}
}

func TestRuleRouterPreservesRichContentPassthroughWithoutInternalImageRequirement(t *testing.T) {
	t.Parallel()

	capabilities := func(name string) providers.Capabilities {
		return providers.Capabilities{
			Name:            name,
			Kind:            providers.KindCloud,
			DefaultModel:    "custom-multimodal",
			Models:          []string{"custom-multimodal"},
			DiscoverySource: "provider",
		}
	}
	registry := providers.NewRegistry(
		&fakeProvider{name: "a-custom", kind: providers.KindCloud, defaultModel: "custom-multimodal", supportedModels: []string{"custom-multimodal"}, capabilities: capabilities("a-custom")},
		&fakeProvider{name: "b-custom", kind: providers.KindCloud, defaultModel: "custom-multimodal", supportedModels: []string{"custom-multimodal"}, capabilities: capabilities("b-custom")},
	)
	router := NewRuleRouter("custom-multimodal", catalog.NewRegistryCatalog(registry, nil))
	req := types.ChatRequest{
		Model: "custom-multimodal",
		Messages: []types.Message{{
			Role: "user",
			ContentBlocks: []types.ContentBlock{{
				Type:  "image_url",
				Image: &types.ContentImage{URL: "https://example.com/image.png"},
			}},
		}},
	}

	got, err := router.Route(context.Background(), req)
	if err != nil {
		t.Fatalf("Route() error = %v, want public rich-content passthrough", err)
	}
	if got.Provider != "a-custom" {
		t.Fatalf("Route() provider = %q, want normal ordering without capability filtering", got.Provider)
	}
	if fallbacks := router.Fallbacks(context.Background(), req, got); len(fallbacks) != 1 || fallbacks[0].Provider != "b-custom" {
		t.Fatalf("Fallbacks() = %+v, want unknown-capability passthrough fallback", fallbacks)
	}
}

func TestRuleRouterHonorsExplicitProviderAlias(t *testing.T) {
	t.Parallel()

	registry := providers.NewRegistry(
		&fakeProvider{name: "Fake Dogfood", aliases: []string{"fake-dogfood"}, kind: providers.KindCloud, defaultModel: "dogfood-model"},
	)
	router := NewRuleRouter("gpt-4o-mini", catalog.NewRegistryCatalog(registry, nil))

	got, err := router.Route(context.Background(), types.ChatRequest{
		Scope: types.RequestScope{
			ProviderHint: "fake-dogfood",
		},
	})
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if got.Provider != "Fake Dogfood" || got.Model != "dogfood-model" || got.Reason != "pinned_provider" {
		t.Fatalf("Route() = %+v, want Fake Dogfood default model via alias", got)
	}
}

func TestRuleRouterExactProviderDoesNotFallBackToNormalizedNameOrAlias(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		provider *fakeProvider
	}{
		{
			name:     "normalized canonical name",
			provider: &fakeProvider{name: "VISION-A", kind: providers.KindCloud, defaultModel: "vision-model"},
		},
		{
			name:     "provider alias",
			provider: &fakeProvider{name: "vision-b", aliases: []string{"vision-a"}, kind: providers.KindCloud, defaultModel: "vision-model"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			router := NewRuleRouter("fallback-model", catalog.NewRegistryCatalog(providers.NewRegistry(tt.provider), nil))

			_, err := router.Route(context.Background(), types.ChatRequest{
				Requirements: types.ChatRequestRequirements{ExactProvider: true},
				Scope:        types.RequestScope{ProviderHint: "vision-a"},
			})
			if err == nil || err.Error() != `provider "vision-a" not found` {
				t.Fatalf("Route() error = %v, want exact-provider not-found error", err)
			}
		})
	}
}

func TestRuleRouterCanonicalProviderNameWinsAliasCollision(t *testing.T) {
	t.Parallel()

	registry := providers.NewRegistry(
		&fakeProvider{name: "vision-a", aliases: []string{"vision-b"}, kind: providers.KindCloud, defaultModel: "alias-model"},
		&fakeProvider{name: "vision-b", kind: providers.KindCloud, defaultModel: "canonical-model"},
	)
	router := NewRuleRouter("fallback-model", catalog.NewRegistryCatalog(registry, nil))

	got, err := router.Route(context.Background(), types.ChatRequest{
		Scope: types.RequestScope{ProviderHint: "vision-b"},
	})
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if got.Provider != "vision-b" || got.Model != "canonical-model" {
		t.Fatalf("Route() = %+v, want exact canonical provider rather than alias owner", got)
	}
}

func TestRuleRouterRejectsAmbiguousProviderAlias(t *testing.T) {
	t.Parallel()

	registry := providers.NewRegistry(
		&fakeProvider{name: "vision-a", aliases: []string{"shared-vision"}, kind: providers.KindCloud, defaultModel: "vision-model"},
		&fakeProvider{name: "vision-b", aliases: []string{"shared-vision"}, kind: providers.KindCloud, defaultModel: "vision-model"},
	)
	router := NewRuleRouter("fallback-model", catalog.NewRegistryCatalog(registry, nil))

	_, err := router.Route(context.Background(), types.ChatRequest{
		Scope: types.RequestScope{ProviderHint: "shared-vision"},
	})
	if err == nil || err.Error() != `provider "shared-vision" matches multiple configured providers` {
		t.Fatalf("Route() error = %v, want ambiguous provider alias error", err)
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
