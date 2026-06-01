package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"testing"

	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestProviderStatusAndModelsRefreshQueryBypassesProviderCache(t *testing.T) {
	t.Parallel()

	provider := &refreshableProvider{}
	handler := newTestHTTPHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), provider)
	client := newTaskTestClient(t, handler)

	cached := mustRequestJSON[OpenAIModelsResponse](client, http.MethodGet, "/v1/models", "")
	if cached.Data[0].ID != "cached-model" {
		t.Fatalf("cached model = %q, want cached-model", cached.Data[0].ID)
	}

	refreshed := mustRequestJSON[OpenAIModelsResponse](client, http.MethodGet, "/v1/models?refresh=true", "")
	if refreshed.Data[0].ID != "refreshed-model" {
		t.Fatalf("refreshed model = %q, want refreshed-model", refreshed.Data[0].ID)
	}

	status := mustRequestJSON[ProviderStatusResponse](client, http.MethodGet, "/hecate/v1/providers/status?refresh=true", "")
	if status.Data[0].Models[0] != "refreshed-model" {
		t.Fatalf("refreshed status model = %q, want refreshed-model", status.Data[0].Models[0])
	}

	normalCalls, refreshCalls := provider.callCounts()
	if normalCalls != 1 {
		t.Fatalf("normal capability calls = %d, want 1", normalCalls)
	}
	if refreshCalls != 2 {
		t.Fatalf("refresh capability calls = %d, want 2", refreshCalls)
	}
}

type refreshableProvider struct {
	mu           sync.Mutex
	normalCalls  int
	refreshCalls int
}

func (p *refreshableProvider) Name() string {
	return "refreshable"
}

func (p *refreshableProvider) Kind() providers.Kind {
	return providers.KindLocal
}

func (p *refreshableProvider) DefaultModel() string {
	return "cached-model"
}

func (p *refreshableProvider) Capabilities(context.Context) (providers.Capabilities, error) {
	p.mu.Lock()
	p.normalCalls++
	p.mu.Unlock()
	return providerRefreshTestCapabilities("cached-model"), nil
}

func (p *refreshableProvider) RefreshCapabilities(context.Context) (providers.Capabilities, error) {
	p.mu.Lock()
	p.refreshCalls++
	p.mu.Unlock()
	return providerRefreshTestCapabilities("refreshed-model"), nil
}

func (p *refreshableProvider) Chat(context.Context, types.ChatRequest) (*types.ChatResponse, error) {
	return &types.ChatResponse{}, nil
}

func (p *refreshableProvider) Supports(string) bool {
	return true
}

func (p *refreshableProvider) callCounts() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.normalCalls, p.refreshCalls
}

func providerRefreshTestCapabilities(model string) providers.Capabilities {
	return providers.Capabilities{
		Name:            "refreshable",
		Kind:            providers.KindLocal,
		DefaultModel:    model,
		Models:          []string{model},
		Discoverable:    true,
		DiscoverySource: "upstream_v1_models",
	}
}
