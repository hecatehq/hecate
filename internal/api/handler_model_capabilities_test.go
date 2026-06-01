package api

import (
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/controlplane"
	"github.com/hecatehq/hecate/internal/modelcaps"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestModelsExposeCapabilityPayloads(t *testing.T) {
	t.Parallel()

	handler := newModelCapabilityTestHandler(t)
	models := mustRequestJSON[OpenAIModelsResponse](newTaskTestClient(t, handler), http.MethodGet, "/v1/models", "")
	caps := modelCapabilitiesFromModels(t, models, "llama3.1:8b")
	readiness := modelReadinessFromModels(t, models, "llama3.1:8b")

	if caps["tool_calling"] != modelcaps.ToolCallingUnknown {
		t.Fatalf("tool_calling = %#v, want unknown", caps["tool_calling"])
	}
	if caps["streaming"] != true {
		t.Fatalf("streaming = %#v, want true", caps["streaming"])
	}
	if caps["source"] != modelcaps.SourceProvider {
		t.Fatalf("source = %#v, want provider", caps["source"])
	}
	if readiness["ready"] != true || readiness["status"] != "ok" || readiness["reason"] != "model_available" {
		t.Fatalf("readiness = %#v, want ready model_available", readiness)
	}
}

func TestModelsExposeProviderDiscoveredCapabilityPayloads(t *testing.T) {
	t.Parallel()

	handler := newModelCapabilityTestHandlerWithCapabilities(t, map[string]types.ModelCapabilities{
		"llama3.1:8b": {
			ToolCalling: modelcaps.ToolCallingBasic,
			Streaming:   true,
			Source:      modelcaps.SourceProvider,
		},
	})
	models := mustRequestJSON[OpenAIModelsResponse](newTaskTestClient(t, handler), http.MethodGet, "/v1/models", "")
	caps := modelCapabilitiesFromModels(t, models, "llama3.1:8b")

	if caps["tool_calling"] != modelcaps.ToolCallingBasic {
		t.Fatalf("tool_calling = %#v, want basic", caps["tool_calling"])
	}
	if caps["source"] != modelcaps.SourceProvider {
		t.Fatalf("source = %#v, want provider", caps["source"])
	}
}

func TestResolveModelCapabilitiesTreatsAutoProviderAsUnpinned(t *testing.T) {
	t.Parallel()

	handler := newModelCapabilityTestAPIHandlerWithCapabilities(t, map[string]types.ModelCapabilities{
		"llama3.1:8b": {
			ToolCalling: modelcaps.ToolCallingBasic,
			Streaming:   true,
			Source:      modelcaps.SourceProvider,
		},
	})

	caps, err := handler.resolveModelCapabilities(t.Context(), "auto", "llama3.1:8b")
	if err != nil {
		t.Fatalf("resolveModelCapabilities returned error: %v", err)
	}
	if caps.ToolCalling != modelcaps.ToolCallingBasic {
		t.Fatalf("tool_calling = %q, want basic", caps.ToolCalling)
	}
}

func newModelCapabilityTestHandler(t *testing.T) http.Handler {
	t.Helper()
	return newModelCapabilityTestHandlerWithCapabilities(t, nil)
}

func newModelCapabilityTestHandlerWithCapabilities(t *testing.T, modelCapabilities map[string]types.ModelCapabilities) http.Handler {
	t.Helper()
	return NewServer(slog.New(slog.NewJSONHandler(io.Discard, nil)), newModelCapabilityTestAPIHandlerWithCapabilities(t, modelCapabilities))
}

func newModelCapabilityTestAPIHandlerWithCapabilities(t *testing.T, modelCapabilities map[string]types.ModelCapabilities) *Handler {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cpStore := controlplane.NewMemoryStore()
	provider := &fakeProvider{
		name: "ollama",
		capabilities: providers.Capabilities{
			Name:              "ollama",
			Kind:              providers.KindLocal,
			DefaultModel:      "llama3.1:8b",
			Models:            []string{"llama3.1:8b"},
			ModelCapabilities: modelCapabilities,
		},
	}
	return newTestAPIHandlerWithSettings(logger, []providers.Provider{provider}, config.Config{}, cpStore)
}

func modelCapabilitiesFromModels(t *testing.T, models OpenAIModelsResponse, modelID string) map[string]any {
	t.Helper()
	for _, item := range models.Data {
		if item.ID != modelID {
			continue
		}
		raw, ok := item.Metadata["capabilities"].(map[string]any)
		if !ok {
			t.Fatalf("capabilities metadata for %s = %#v", modelID, item.Metadata["capabilities"])
		}
		return raw
	}
	t.Fatalf("model %q not found in %+v", modelID, models.Data)
	return nil
}

func modelReadinessFromModels(t *testing.T, models OpenAIModelsResponse, modelID string) map[string]any {
	t.Helper()
	for _, item := range models.Data {
		if item.ID != modelID {
			continue
		}
		raw, ok := item.Metadata["readiness"].(map[string]any)
		if !ok {
			t.Fatalf("readiness metadata for %s = %#v", modelID, item.Metadata["readiness"])
		}
		return raw
	}
	t.Fatalf("model %q not found in %+v", modelID, models.Data)
	return nil
}
