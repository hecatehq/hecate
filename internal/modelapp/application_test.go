package modelapp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/gateway"
	"github.com/hecatehq/hecate/internal/modelcaps"
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
	if caps.ToolCalling != modelcaps.ToolCallingBasic || !caps.Streaming || caps.Source != modelcaps.SourceProvider {
		t.Fatalf("capabilities = %+v, want provider-resolved basic streaming", caps)
	}
	models[0].Readiness.SuggestedModels[0] = "mutated"
	if service.models[0].Readiness.SuggestedModels[0] != "other" {
		t.Fatalf("ListModels returned readiness suggestions aliased to service state")
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

type fakeModelService struct {
	models            []types.ModelInfo
	listErr           error
	refreshErr        error
	readiness         gateway.ProviderModelReadiness
	readinessErr      error
	listCalls         int
	refreshCalls      int
	readinessProvider string
	readinessModel    string
}

func (s *fakeModelService) ListModels(context.Context) (*gateway.ModelsResult, error) {
	s.listCalls++
	if s.listErr != nil {
		return nil, s.listErr
	}
	return &gateway.ModelsResult{Models: append([]types.ModelInfo(nil), s.models...)}, nil
}

func (s *fakeModelService) RefreshModels(context.Context) (*gateway.ModelsResult, error) {
	s.refreshCalls++
	if s.refreshErr != nil {
		return nil, s.refreshErr
	}
	return &gateway.ModelsResult{Models: append([]types.ModelInfo(nil), s.models...)}, nil
}

func (s *fakeModelService) ProviderModelReadiness(_ context.Context, provider, model string) (*gateway.ProviderModelReadinessResult, error) {
	s.readinessProvider = provider
	s.readinessModel = model
	if s.readinessErr != nil {
		return nil, s.readinessErr
	}
	return &gateway.ProviderModelReadinessResult{Readiness: s.readiness}, nil
}
