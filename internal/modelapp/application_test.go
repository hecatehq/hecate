package modelapp

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/catalog"
	"github.com/hecatehq/hecate/internal/gateway"
	"github.com/hecatehq/hecate/internal/modelcaps"
	"github.com/hecatehq/hecate/internal/modelprobe"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestApplication_ListModelsResolvesCapabilities(t *testing.T) {
	t.Parallel()

	service := &fakeModelService{
		models: []types.ModelInfo{
			{
				ID:              "llama3.1:8b",
				Provider:        "ollama",
				Kind:            string(providers.KindLocal),
				DiscoverySource: "provider",
				Capabilities: types.ModelCapabilities{
					ToolCalling: modelcaps.ToolCallingBasic,
					Streaming:   true,
				},
				Readiness: types.ModelReadiness{
					Ready:           true,
					Status:          "ok",
					Reason:          "model_available",
					SuggestedModels: []string{"other"},
				},
			},
		},
	}

	models, err := New(Options{Service: service}).ListModels(context.Background(), ListModelsCommand{})
	if err != nil {
		t.Fatalf("ListModels returned error: %v", err)
	}
	if service.listCalls != 1 || service.refreshCalls != 0 {
		t.Fatalf("service calls list=%d refresh=%d, want list only", service.listCalls, service.refreshCalls)
	}
	if len(models) != 1 {
		t.Fatalf("models len = %d, want 1", len(models))
	}
	caps := models[0].Capabilities
	if caps.ToolCalling != modelcaps.ToolCallingBasic || !caps.Streaming || caps.Source != modelcaps.SourceMixed {
		t.Fatalf("capabilities = %+v, want provider tool metadata plus catalog image provenance", caps)
	}
	models[0].Readiness.SuggestedModels[0] = "mutated"
	if service.models[0].Readiness.SuggestedModels[0] != "other" {
		t.Fatalf("ListModels returned readiness suggestions aliased to service state")
	}
}

func TestApplication_ListModelsUsesProviderFamilyWithoutChangingRoutingIdentity(t *testing.T) {
	t.Parallel()
	openAIInstance := types.ProviderInstanceIdentity{ID: "configuration-openai", Kind: types.ProviderInstanceIdentityConfiguration}

	service := &fakeModelService{models: []types.ModelInfo{
		{
			ID:               "gpt-4o",
			Provider:         "OpenAI Production",
			ProviderAliases:  []string{"openai-prod", "openai"},
			ProviderFamily:   "openai",
			ProviderInstance: openAIInstance,
			Kind:             string(providers.KindCloud),
			Readiness:        types.ModelReadiness{Ready: true, RoutingReady: true},
		},
		{
			ID:              "claude-sonnet-4-6",
			Provider:        "Anthropic Production",
			ProviderAliases: []string{"anthropic-prod", "anthropic"},
			ProviderFamily:  "anthropic",
			Kind:            string(providers.KindCloud),
			Readiness:       types.ModelReadiness{Ready: true, RoutingReady: true},
		},
	}}

	models, err := New(Options{Service: service}).ListModels(context.Background(), ListModelsCommand{})
	if err != nil {
		t.Fatalf("ListModels returned error: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("models len = %d, want 2", len(models))
	}
	for _, model := range models {
		if model.Capabilities.ImageInput != modelcaps.ImageInputSupported {
			t.Fatalf("%s image_input = %q, want supported", model.Provider, model.Capabilities.ImageInput)
		}
	}
	if models[0].Provider != "OpenAI Production" || models[1].Provider != "Anthropic Production" {
		t.Fatalf("configured provider identities changed: %#v", []string{models[0].Provider, models[1].Provider})
	}

	caps, err := New(Options{Service: service}).ResolveCapabilities(context.Background(), "openai-prod", "gpt-4o")
	if err != nil || caps.ImageInput != modelcaps.ImageInputSupported {
		t.Fatalf("ResolveCapabilities(alias) = %+v, %v, want image-capable OpenAI route", caps, err)
	}
	supported, err := New(Options{Service: service}).SupportsImageInput(context.Background(), "anthropic-prod", "claude-sonnet-4-6")
	if err != nil || !supported {
		t.Fatalf("SupportsImageInput(alias) = %v, %v, want true", supported, err)
	}
	providerName, err := New(Options{Service: service}).ResolveProviderName(context.Background(), "openai-prod", "gpt-4o")
	if err != nil || providerName != "OpenAI Production" {
		t.Fatalf("ResolveProviderName(alias) = %q, %v, want runtime name", providerName, err)
	}
	providerRoute, err := New(Options{Service: service}).ResolveProviderRoute(context.Background(), "openai-prod", "gpt-4o")
	if err != nil || providerRoute.Name != "OpenAI Production" || providerRoute.Instance != openAIInstance {
		t.Fatalf("ResolveProviderRoute(alias) = %+v, %v, want canonical provider and instance", providerRoute, err)
	}

	models[0].ProviderAliases[0] = "mutated"
	if service.models[0].ProviderAliases[0] != "openai-prod" {
		t.Fatal("ListModels returned provider aliases aliased to service state")
	}
}

func TestApplication_CanonicalProviderNameWinsAliasCollision(t *testing.T) {
	t.Parallel()

	service := &fakeModelService{models: []types.ModelInfo{
		{
			ID:              "shared-model",
			Provider:        "vision-a",
			ProviderAliases: []string{"vision-b"},
			Capabilities:    types.ModelCapabilities{ImageInput: modelcaps.ImageInputSupported},
			Readiness:       types.ModelReadiness{Ready: true, RoutingReady: true},
		},
		{
			ID:              "shared-model",
			Provider:        "vision-b",
			ProviderAliases: []string{"provider-b"},
			Capabilities:    types.ModelCapabilities{ImageInput: modelcaps.ImageInputNone},
			Readiness:       types.ModelReadiness{Ready: true, RoutingReady: true},
		},
	}}
	app := New(Options{Service: service})

	providerName, err := app.ResolveProviderName(context.Background(), "vision-b", "shared-model")
	if err != nil || providerName != "vision-b" {
		t.Fatalf("ResolveProviderName() = %q, %v, want canonical vision-b", providerName, err)
	}
	caps, err := app.ResolveCapabilities(context.Background(), "vision-b", "shared-model")
	if err != nil {
		t.Fatalf("ResolveCapabilities() error = %v", err)
	}
	if caps.ImageInput != modelcaps.ImageInputNone {
		t.Fatalf("ResolveCapabilities() image_input = %q, want canonical provider's none", caps.ImageInput)
	}
	supported, err := app.SupportsImageInput(context.Background(), "vision-b", "shared-model")
	if err != nil {
		t.Fatalf("SupportsImageInput() error = %v", err)
	}
	if supported {
		t.Fatal("SupportsImageInput() = true, want canonical text-only provider to win alias collision")
	}
}

func TestApplication_ProviderResolutionPrecedesModelMatching(t *testing.T) {
	t.Parallel()

	service := &fakeModelService{models: []types.ModelInfo{
		{
			ID:              "alias-only-model",
			Provider:        "vision-a",
			ProviderAliases: []string{"vision-b"},
			Readiness:       types.ModelReadiness{Ready: true, RoutingReady: true},
		},
		{
			ID:        "canonical-only-model",
			Provider:  "vision-b",
			Readiness: types.ModelReadiness{Ready: true, RoutingReady: true},
		},
	}}

	_, err := New(Options{Service: service}).ResolveProviderName(context.Background(), "vision-b", "alias-only-model")
	if err == nil || !strings.Contains(err.Error(), `model "alias-only-model" is not available from provider "vision-b"`) {
		t.Fatalf("ResolveProviderName() error = %v, want canonical provider model-unavailable error", err)
	}
}

func TestApplication_RejectsAmbiguousProviderAlias(t *testing.T) {
	t.Parallel()

	service := &fakeModelService{models: []types.ModelInfo{
		{
			ID:              "shared-model",
			Provider:        "vision-a",
			ProviderAliases: []string{"shared-vision"},
			Capabilities:    types.ModelCapabilities{ImageInput: modelcaps.ImageInputSupported},
			Readiness:       types.ModelReadiness{Ready: true, RoutingReady: true},
		},
		{
			ID:              "shared-model",
			Provider:        "vision-b",
			ProviderAliases: []string{"shared-vision"},
			Capabilities:    types.ModelCapabilities{ImageInput: modelcaps.ImageInputSupported},
			Readiness:       types.ModelReadiness{Ready: true, RoutingReady: true},
		},
	}}
	app := New(Options{Service: service})
	wantError := `provider "shared-vision" matches multiple configured providers`

	_, err := app.ResolveProviderName(context.Background(), "shared-vision", "shared-model")
	if err == nil || err.Error() != wantError {
		t.Fatalf("ResolveProviderName() error = %v, want %q", err, wantError)
	}
	if !errors.Is(err, ErrProviderAmbiguous) {
		t.Fatalf("ResolveProviderName() error = %v, want ErrProviderAmbiguous", err)
	}
	if _, err := app.ResolveCapabilities(context.Background(), "shared-vision", "shared-model"); err == nil || err.Error() != wantError {
		t.Fatalf("ResolveCapabilities() error = %v, want %q", err, wantError)
	}
	if _, err := app.SupportsImageInput(context.Background(), "shared-vision", "shared-model"); err == nil || err.Error() != wantError {
		t.Fatalf("SupportsImageInput() error = %v, want %q", err, wantError)
	}
}

func TestApplication_RejectsAliasCollisionWithConfiguredZeroModelProvider(t *testing.T) {
	t.Parallel()

	service := &fakeModelService{
		models: []types.ModelInfo{
			{
				ID:              "shared-model",
				Provider:        "vision-a",
				ProviderAliases: []string{"shared-vision"},
				Capabilities:    types.ModelCapabilities{ImageInput: modelcaps.ImageInputSupported},
				Readiness:       types.ModelReadiness{Ready: true, RoutingReady: true},
			},
		},
		providerIdentities: []catalog.ProviderIdentity{
			{Name: "vision-a", Aliases: []string{"shared-vision"}},
			{Name: "vision-b", Aliases: []string{"shared-vision"}},
		},
	}
	app := New(Options{Service: service})

	for _, resolve := range []struct {
		name string
		call func() error
	}{
		{name: "provider name", call: func() error {
			_, err := app.ResolveProviderName(context.Background(), "shared-vision", "shared-model")
			return err
		}},
		{name: "capabilities", call: func() error {
			_, err := app.ResolveCapabilities(context.Background(), "shared-vision", "shared-model")
			return err
		}},
		{name: "image admission", call: func() error {
			_, err := app.SupportsImageInput(context.Background(), "shared-vision", "shared-model")
			return err
		}},
	} {
		t.Run(resolve.name, func(t *testing.T) {
			err := resolve.call()
			if !errors.Is(err, ErrProviderAmbiguous) {
				t.Fatalf("error = %v, want ErrProviderAmbiguous from zero-model alias collision", err)
			}
		})
	}
}

func TestApplication_ListModelsRefreshesWhenRequested(t *testing.T) {
	t.Parallel()

	service := &fakeModelService{}
	if _, err := New(Options{Service: service}).ListModels(context.Background(), ListModelsCommand{Refresh: true}); err != nil {
		t.Fatalf("ListModels(refresh) returned error: %v", err)
	}
	if service.refreshCalls != 1 || service.listCalls != 0 {
		t.Fatalf("service calls list=%d refresh=%d, want refresh only", service.listCalls, service.refreshCalls)
	}
}

func TestApplication_ResolveCapabilitiesTreatsAutoProviderAsUnpinned(t *testing.T) {
	t.Parallel()

	service := &fakeModelService{
		models: []types.ModelInfo{
			{
				ID:       "llama3.1:8b",
				Provider: "ollama",
				Kind:     string(providers.KindLocal),
				Capabilities: types.ModelCapabilities{
					ToolCalling: modelcaps.ToolCallingBasic,
					Streaming:   true,
				},
			},
		},
	}

	caps, err := New(Options{Service: service}).ResolveCapabilities(context.Background(), "auto", "llama3.1:8b")
	if err != nil {
		t.Fatalf("ResolveCapabilities returned error: %v", err)
	}
	if caps.ToolCalling != modelcaps.ToolCallingBasic {
		t.Fatalf("tool_calling = %q, want basic", caps.ToolCalling)
	}
}

func TestApplication_ResolveCapabilitiesWrapsReadinessForUnavailableModel(t *testing.T) {
	t.Parallel()

	service := &fakeModelService{
		readiness: gateway.ProviderModelReadiness{
			Provider:              "ollama",
			Model:                 "missing-model",
			Reason:                "model_not_discovered",
			Message:               "Model is not discovered.",
			OperatorAction:        "Refresh provider status.",
			ProviderStatus:        "healthy",
			ProviderBlockedReason: "",
			SuggestedModels:       []string{"llama3.1:8b"},
		},
	}

	_, err := New(Options{Service: service}).ResolveCapabilities(context.Background(), "ollama", "missing-model")
	if err == nil {
		t.Fatal("ResolveCapabilities returned nil error, want readiness error")
	}
	var readinessErr ReadinessError
	if !errors.As(err, &readinessErr) {
		t.Fatalf("error = %T %v, want ReadinessError", err, err)
	}
	if readinessErr.Readiness.Status != "blocked" || readinessErr.Readiness.Reason != "model_not_discovered" || readinessErr.Readiness.SuggestedModels[0] != "llama3.1:8b" {
		t.Fatalf("readiness = %+v, want blocked model_not_discovered with suggestions", readinessErr.Readiness)
	}
	if service.readinessProvider != "ollama" || service.readinessModel != "missing-model" {
		t.Fatalf("readiness lookup = provider %q model %q, want explicit provider/model", service.readinessProvider, service.readinessModel)
	}
}

func TestApplication_ResolveCapabilitiesWithoutServiceFallsBackToStaticRules(t *testing.T) {
	t.Parallel()

	caps, err := New(Options{}).ResolveCapabilities(context.Background(), "openai", "gpt-4o")
	if err != nil {
		t.Fatalf("ResolveCapabilities(no service) returned error: %v", err)
	}
	if caps.ToolCalling != modelcaps.ToolCallingParallel {
		t.Fatalf("tool_calling = %q, want parallel from static rules", caps.ToolCalling)
	}

	_, err = New(Options{}).ResolveCapabilities(context.Background(), "openai", " ")
	if err == nil || !strings.Contains(err.Error(), "model is required") {
		t.Fatalf("missing model error = %v, want model is required", err)
	}
}

func TestApplication_SupportsImageInputChecksEveryAutoRoute(t *testing.T) {
	t.Parallel()

	service := &fakeModelService{models: []types.ModelInfo{
		{
			ID:       "shared-model",
			Provider: "text-only",
			Capabilities: types.ModelCapabilities{
				ImageInput: modelcaps.ImageInputNone,
			},
			Readiness: types.ModelReadiness{Ready: true, RoutingReady: true},
		},
		{
			ID:       "shared-model",
			Provider: "vision",
			Capabilities: types.ModelCapabilities{
				ImageInput: modelcaps.ImageInputSupported,
			},
			Readiness: types.ModelReadiness{Ready: true, RoutingReady: true},
		},
	}}
	app := New(Options{Service: service})

	got, err := app.SupportsImageInput(context.Background(), "auto", "shared-model")
	if err != nil {
		t.Fatalf("SupportsImageInput(auto) returned error: %v", err)
	}
	if !got {
		t.Fatal("SupportsImageInput(auto) = false, want true from second matching route")
	}

	got, err = app.SupportsImageInput(context.Background(), "text-only", "shared-model")
	if err != nil {
		t.Fatalf("SupportsImageInput(pinned) returned error: %v", err)
	}
	if got {
		t.Fatal("SupportsImageInput(text-only) = true, want false")
	}
}

func TestApplication_ImageAdmissionIgnoresCapableButUnreadyAutoRoute(t *testing.T) {
	t.Parallel()

	service := &fakeModelService{models: []types.ModelInfo{
		{
			ID:           "shared-model",
			Provider:     "ready-text",
			Capabilities: types.ModelCapabilities{ImageInput: modelcaps.ImageInputNone},
			Readiness:    types.ModelReadiness{Ready: true, RoutingReady: true},
		},
		{
			ID:           "shared-model",
			Provider:     "blocked-vision",
			Capabilities: types.ModelCapabilities{ImageInput: modelcaps.ImageInputSupported},
			Readiness:    types.ModelReadiness{Ready: false, RoutingReady: false},
		},
	}}
	app := New(Options{Service: service})

	supported, err := app.SupportsImageInput(context.Background(), "auto", "shared-model")
	if err != nil {
		t.Fatalf("SupportsImageInput(auto) returned error: %v", err)
	}
	if supported {
		t.Fatal("SupportsImageInput(auto) = true, want blocked capable route ignored")
	}
	caps, err := app.ResolveCapabilities(context.Background(), "auto", "shared-model")
	if err != nil {
		t.Fatalf("ResolveCapabilities(auto) returned error: %v", err)
	}
	if caps.ImageInput != modelcaps.ImageInputNone {
		t.Fatalf("aggregate image_input = %q, want none from the only routable route", caps.ImageInput)
	}
}

func TestApplication_SupportsImageInputFailsClosed(t *testing.T) {
	t.Parallel()

	got, err := New(Options{}).SupportsImageInput(context.Background(), "openai", "gpt-4o")
	if err != nil || !got {
		t.Fatalf("SupportsImageInput(static gpt-4o) = %v, %v, want true, nil", got, err)
	}
	got, err = New(Options{}).SupportsImageInput(context.Background(), "custom", "unknown")
	if err != nil {
		t.Fatalf("SupportsImageInput(static unknown) returned error: %v", err)
	}
	if got {
		t.Fatal("SupportsImageInput(static unknown) = true, want false")
	}
}

func TestApplication_ListModelsProjectsActiveToolVerification(t *testing.T) {
	t.Parallel()

	store := modelprobe.NewMemoryStore()
	unknownInstance := types.ProviderInstanceIdentity{ID: "unknown-generation", Kind: types.ProviderInstanceIdentityConfiguration}
	knownInstance := types.ProviderInstanceIdentity{ID: "known-generation", Kind: types.ProviderInstanceIdentityConfiguration}
	seedToolProbeRecord(t, store, modelprobe.Key{
		Provider: "local-runtime",
		Model:    "custom-tool-model",
		Instance: unknownInstance,
		Version:  modelprobe.ProbeVersion,
	}, modelprobe.StatusSupported)
	seedToolProbeRecord(t, store, modelprobe.Key{
		Provider: "local-runtime",
		Model:    "known-no-tools-model",
		Instance: knownInstance,
		Version:  modelprobe.ProbeVersion,
	}, modelprobe.StatusSupported)

	service := &fakeModelService{models: []types.ModelInfo{
		{
			ID:               "custom-tool-model",
			Provider:         "local-runtime",
			ProviderInstance: unknownInstance,
			Kind:             string(providers.KindLocal),
			Capabilities:     types.ModelCapabilities{ToolCalling: modelcaps.ToolCallingUnknown},
			Readiness:        types.ModelReadiness{Ready: true, RoutingReady: true},
		},
		{
			ID:               "known-no-tools-model",
			Provider:         "local-runtime",
			ProviderInstance: knownInstance,
			Kind:             string(providers.KindLocal),
			Capabilities:     types.ModelCapabilities{ToolCalling: modelcaps.ToolCallingNone},
			Readiness:        types.ModelReadiness{Ready: true, RoutingReady: true},
		},
	}}

	models, err := New(Options{Service: service, ToolProbeStore: store}).ListModels(t.Context(), ListModelsCommand{})
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("models len = %d, want 2", len(models))
	}
	if got := models[0].Capabilities; got.ToolCalling != modelcaps.ToolCallingBasic || got.ToolVerification == nil || got.ToolVerification.Status != modelprobe.StatusSupported {
		t.Fatalf("unknown model capabilities = %+v, want verified basic tool support", got)
	}
	if got := models[1].Capabilities; got.ToolCalling != modelcaps.ToolCallingNone || got.ToolVerification == nil || got.ToolVerification.Status != modelprobe.StatusSupported {
		t.Fatalf("known model capabilities = %+v, want provider-known none preserved with verification provenance", got)
	}
}

func TestApplication_ListModelsBatchesToolVerificationReads(t *testing.T) {
	t.Parallel()

	backing := modelprobe.NewMemoryStore()
	store := &countingBatchProbeStore{Store: backing}
	first := modelprobe.Key{
		Provider: "local-runtime",
		Model:    "custom-one",
		Instance: types.ProviderInstanceIdentity{ID: "generation-one", Kind: types.ProviderInstanceIdentityConfiguration},
		Version:  modelprobe.ProbeVersion,
	}
	second := modelprobe.Key{
		Provider: "local-runtime",
		Model:    "custom-two",
		Instance: types.ProviderInstanceIdentity{ID: "generation-two", Kind: types.ProviderInstanceIdentityConfiguration},
		Version:  modelprobe.ProbeVersion,
	}
	seedToolProbeRecord(t, backing, first, modelprobe.StatusSupported)
	service := &fakeModelService{models: []types.ModelInfo{
		{
			ID:               first.Model,
			Provider:         first.Provider,
			ProviderInstance: first.Instance,
			Kind:             string(providers.KindLocal),
			Capabilities:     types.ModelCapabilities{ToolCalling: modelcaps.ToolCallingUnknown},
			Readiness:        types.ModelReadiness{Ready: true, RoutingReady: true},
		},
		{
			ID:               second.Model,
			Provider:         second.Provider,
			ProviderInstance: second.Instance,
			Kind:             string(providers.KindLocal),
			Capabilities:     types.ModelCapabilities{ToolCalling: modelcaps.ToolCallingUnknown},
			Readiness:        types.ModelReadiness{Ready: true, RoutingReady: true},
		},
	}}

	models, err := New(Options{Service: service, ToolProbeStore: store}).ListModels(t.Context(), ListModelsCommand{})
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if store.batchCalls.Load() != 1 || store.getCalls.Load() != 0 {
		t.Fatalf("probe reads = batch %d single %d, want one batch and no per-model reads", store.batchCalls.Load(), store.getCalls.Load())
	}
	if len(models) != 2 || models[0].Capabilities.ToolCalling != modelcaps.ToolCallingBasic || models[1].Capabilities.ToolVerification != nil {
		t.Fatalf("projected models = %+v, want one exact verified record", models)
	}
}

func TestApplication_ResolveCapabilitiesDoesNotProjectManualVerificationOntoAuto(t *testing.T) {
	t.Parallel()

	store := modelprobe.NewMemoryStore()
	instance := types.ProviderInstanceIdentity{ID: "verified-auto-generation", Kind: types.ProviderInstanceIdentityConfiguration}
	key := modelprobe.Key{
		Provider: "local-runtime",
		Model:    "custom-tool-model",
		Instance: instance,
		Version:  modelprobe.ProbeVersion,
	}
	seedToolProbeRecord(t, store, key, modelprobe.StatusSupported)
	service := &fakeModelService{models: []types.ModelInfo{{
		ID:               key.Model,
		Provider:         key.Provider,
		ProviderInstance: instance,
		Kind:             string(providers.KindLocal),
		Capabilities:     types.ModelCapabilities{ToolCalling: modelcaps.ToolCallingUnknown},
		Readiness:        types.ModelReadiness{Ready: true, RoutingReady: true},
	}}}
	app := New(Options{Service: service, ToolProbeStore: store})

	auto, err := app.ResolveCapabilities(t.Context(), "auto", key.Model)
	if err != nil {
		t.Fatalf("ResolveCapabilities(auto) error = %v", err)
	}
	if auto.ToolCalling != modelcaps.ToolCallingUnknown || auto.ToolVerification != nil {
		t.Fatalf("auto capabilities = %+v, want route-bound proof withheld", auto)
	}

	exact, err := app.ResolveCapabilities(t.Context(), key.Provider, key.Model)
	if err != nil {
		t.Fatalf("ResolveCapabilities(exact) error = %v", err)
	}
	if exact.ToolCalling != modelcaps.ToolCallingBasic || exact.Source != modelcaps.SourceMixed || exact.ToolVerification == nil || !exact.ToolCallingVerificationApplied {
		t.Fatalf("exact capabilities = %+v, want verified exact route", exact)
	}
}

func TestApplication_VerifyToolCallingCachesExactRouteResult(t *testing.T) {
	t.Parallel()

	instance := types.ProviderInstanceIdentity{ID: "custom-generation", Kind: types.ProviderInstanceIdentityConfiguration}
	service := &fakeModelService{models: []types.ModelInfo{{
		ID:               "custom-tool-model",
		Provider:         "Local Runtime",
		ProviderAliases:  []string{"local-runtime"},
		ProviderInstance: instance,
		Kind:             string(providers.KindLocal),
		Capabilities:     types.ModelCapabilities{ToolCalling: modelcaps.ToolCallingUnknown},
		Readiness:        types.ModelReadiness{Ready: true, RoutingReady: true},
	}}}
	service.toolProbe = func(_ context.Context, input gateway.ToolCallingProbeRequest) (gateway.ToolCallingProbeResult, error) {
		service.probeRequests = append(service.probeRequests, input)
		return gateway.ToolCallingProbeResult{
			Provider: input.Provider,
			Model:    input.Model,
			Status:   gateway.ToolProbeSupported,
			TraceID:  "trace_tool_probe",
		}, nil
	}
	app := New(Options{Service: service, ToolProbeStore: modelprobe.NewMemoryStore()})

	result, err := app.VerifyToolCalling(t.Context(), "local-runtime", "custom-tool-model")
	if err != nil {
		t.Fatalf("VerifyToolCalling() error = %v", err)
	}
	if !result.Performed || result.Provider != "Local Runtime" || result.Model != "custom-tool-model" || result.TraceID != "trace_tool_probe" {
		t.Fatalf("VerifyToolCalling() result = %+v", result)
	}
	if result.Capabilities.ToolCalling != modelcaps.ToolCallingBasic || result.Capabilities.Source != modelcaps.SourceMixed || result.Verification == nil || result.Verification.Status != modelprobe.StatusSupported {
		t.Fatalf("VerifyToolCalling() capabilities = %+v verification=%+v, want verified basic", result.Capabilities, result.Verification)
	}
	if len(service.probeRequests) != 1 || service.probeRequests[0].Provider != "Local Runtime" || service.probeRequests[0].Model != "custom-tool-model" || service.probeRequests[0].ProviderInstance != instance {
		t.Fatalf("probe requests = %+v, want one canonical pinned route", service.probeRequests)
	}

	cached, err := app.VerifyToolCalling(t.Context(), "local-runtime", "custom-tool-model")
	if !errors.Is(err, ErrToolProbeNotNeeded) {
		t.Fatalf("VerifyToolCalling(cached) error = %v, want ErrToolProbeNotNeeded", err)
	}
	if cached.Performed || cached.Capabilities.ToolCalling != modelcaps.ToolCallingBasic || len(service.probeRequests) != 1 {
		t.Fatalf("VerifyToolCalling(cached) = %+v requests=%d, want cached result without dispatch", cached, len(service.probeRequests))
	}
}

func TestApplication_VerifyToolCallingRejectsStaleGenerationResult(t *testing.T) {
	t.Parallel()

	initial := types.ProviderInstanceIdentity{ID: "generation-a", Kind: types.ProviderInstanceIdentityConfiguration}
	replacement := types.ProviderInstanceIdentity{ID: "generation-b", Kind: types.ProviderInstanceIdentityConfiguration}
	service := &fakeModelService{models: []types.ModelInfo{{
		ID:               "custom-tool-model",
		Provider:         "local-runtime",
		ProviderInstance: initial,
		Kind:             string(providers.KindLocal),
		Capabilities:     types.ModelCapabilities{ToolCalling: modelcaps.ToolCallingUnknown},
		Readiness:        types.ModelReadiness{Ready: true, RoutingReady: true},
	}}}
	service.toolProbe = func(_ context.Context, input gateway.ToolCallingProbeRequest) (gateway.ToolCallingProbeResult, error) {
		service.models[0].ProviderInstance = replacement
		return gateway.ToolCallingProbeResult{Provider: input.Provider, Model: input.Model, Status: gateway.ToolProbeSupported}, nil
	}
	app := New(Options{Service: service, ToolProbeStore: modelprobe.NewMemoryStore()})

	_, err := app.VerifyToolCalling(t.Context(), "local-runtime", "custom-tool-model")
	if !errors.Is(err, ErrToolProbeRouteChanged) {
		t.Fatalf("VerifyToolCalling() error = %v, want ErrToolProbeRouteChanged", err)
	}
	models, listErr := app.ListModels(t.Context(), ListModelsCommand{})
	if listErr != nil {
		t.Fatalf("ListModels() error = %v", listErr)
	}
	if got := models[0].Capabilities; got.ToolCalling != modelcaps.ToolCallingUnknown || got.ToolVerification != nil {
		t.Fatalf("replacement generation capabilities = %+v, want no stale verification", got)
	}
}

func seedToolProbeRecord(t *testing.T, store modelprobe.Store, key modelprobe.Key, status string) {
	t.Helper()
	now := time.Now().UTC()
	record, acquired, err := store.Acquire(t.Context(), key, now, now.Add(time.Minute), "seed-lease")
	if err != nil || !acquired {
		t.Fatalf("seed Acquire() = %+v, %t, %v", record, acquired, err)
	}
	record.Status = status
	record.Reason = modelprobe.ReasonNone
	record.CheckedAt = now
	record.ExpiresAt = now.Add(time.Hour)
	if _, err := store.Complete(t.Context(), record); err != nil {
		t.Fatalf("seed Complete() error = %v", err)
	}
}

type countingBatchProbeStore struct {
	modelprobe.Store
	getCalls   atomic.Int32
	batchCalls atomic.Int32
}

func (s *countingBatchProbeStore) Get(ctx context.Context, key modelprobe.Key) (modelprobe.Record, bool, error) {
	s.getCalls.Add(1)
	return s.Store.Get(ctx, key)
}

func (s *countingBatchProbeStore) GetMany(ctx context.Context, keys []modelprobe.Key) (map[modelprobe.Key]modelprobe.Record, error) {
	s.batchCalls.Add(1)
	return s.Store.(modelprobe.BatchStore).GetMany(ctx, keys)
}

type fakeModelService struct {
	models             []types.ModelInfo
	providerIdentities []catalog.ProviderIdentity
	listErr            error
	refreshErr         error
	readiness          gateway.ProviderModelReadiness
	readinessErr       error
	listCalls          int
	refreshCalls       int
	readinessProvider  string
	readinessModel     string
	toolProbe          func(context.Context, gateway.ToolCallingProbeRequest) (gateway.ToolCallingProbeResult, error)
	probeRequests      []gateway.ToolCallingProbeRequest
}

func (s *fakeModelService) ListModels(context.Context) (*gateway.ModelsResult, error) {
	s.listCalls++
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.modelsResult(), nil
}

func (s *fakeModelService) RefreshModels(context.Context) (*gateway.ModelsResult, error) {
	s.refreshCalls++
	if s.refreshErr != nil {
		return nil, s.refreshErr
	}
	return s.modelsResult(), nil
}

func (s *fakeModelService) modelsResult() *gateway.ModelsResult {
	identities := s.providerIdentities
	if identities == nil {
		indexes := make(map[string]int, len(s.models))
		identities = make([]catalog.ProviderIdentity, 0, len(s.models))
		for _, model := range s.models {
			index, ok := indexes[model.Provider]
			if !ok {
				index = len(identities)
				indexes[model.Provider] = index
				identities = append(identities, catalog.ProviderIdentity{Name: model.Provider})
			}
			identities[index].Aliases = append(identities[index].Aliases, model.ProviderAliases...)
		}
	}
	providerIdentities := make([]catalog.ProviderIdentity, 0, len(identities))
	for _, identity := range identities {
		identity.Aliases = append([]string(nil), identity.Aliases...)
		providerIdentities = append(providerIdentities, identity)
	}
	return &gateway.ModelsResult{
		Models:             append([]types.ModelInfo(nil), s.models...),
		ProviderIdentities: providerIdentities,
	}
}

func (s *fakeModelService) ProviderModelReadiness(_ context.Context, provider, model string) (*gateway.ProviderModelReadinessResult, error) {
	s.readinessProvider = provider
	s.readinessModel = model
	if s.readinessErr != nil {
		return nil, s.readinessErr
	}
	return &gateway.ProviderModelReadinessResult{Readiness: s.readiness}, nil
}

func (s *fakeModelService) ProbeToolCalling(ctx context.Context, input gateway.ToolCallingProbeRequest) (gateway.ToolCallingProbeResult, error) {
	if s.toolProbe == nil {
		return gateway.ToolCallingProbeResult{}, gateway.ErrToolProbeUnavailable
	}
	return s.toolProbe(ctx, input)
}
