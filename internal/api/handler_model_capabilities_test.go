package api

import (
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/hecate/agent-runtime/internal/config"
	"github.com/hecate/agent-runtime/internal/controlplane"
	"github.com/hecate/agent-runtime/internal/modelcaps"
	"github.com/hecate/agent-runtime/internal/providers"
)

func TestModelsExposeCapabilityPayloads(t *testing.T) {
	t.Parallel()

	handler := newModelCapabilityTestHandler(t)
	models := mustRequestJSON[OpenAIModelsResponse](newTaskTestClient(t, handler), http.MethodGet, "/v1/models", "")
	caps := modelCapabilitiesFromModels(t, models, "llama3.1:8b")

	if caps["tool_calling"] != modelcaps.ToolCallingUnknown {
		t.Fatalf("tool_calling = %#v, want unknown", caps["tool_calling"])
	}
	if caps["streaming"] != true {
		t.Fatalf("streaming = %#v, want true", caps["streaming"])
	}
	if caps["source"] != modelcaps.SourceProvider {
		t.Fatalf("source = %#v, want provider", caps["source"])
	}
}

func TestModelCapabilityOverrideAffectsModelsResponse(t *testing.T) {
	t.Parallel()

	handler := newModelCapabilityTestHandler(t)
	client := newTaskTestClient(t, handler)

	updated := mustRequestJSON[ModelCapabilityResponse](client, http.MethodPut, "/hecate/v1/model-capabilities/overrides",
		`{"provider":"ollama","model":"llama3.1:8b","tool_calling":"basic","max_context_tokens":128000,"note":"operator verified tools"}`)
	if updated.Data.ToolCalling != modelcaps.ToolCallingBasic || updated.Data.Source != modelcaps.SourceOperatorOverride {
		t.Fatalf("override response = %+v", updated.Data)
	}

	models := mustRequestJSON[OpenAIModelsResponse](client, http.MethodGet, "/v1/models", "")
	caps := modelCapabilitiesFromModels(t, models, "llama3.1:8b")
	if caps["tool_calling"] != modelcaps.ToolCallingBasic {
		t.Fatalf("tool_calling = %#v, want basic", caps["tool_calling"])
	}
	if caps["source"] != modelcaps.SourceOperatorOverride {
		t.Fatalf("source = %#v, want operator_override", caps["source"])
	}
	if caps["max_context_tokens"] != float64(128000) {
		t.Fatalf("max_context_tokens = %#v, want 128000", caps["max_context_tokens"])
	}
}

func TestModelCapabilityProbeIsRecorded(t *testing.T) {
	t.Parallel()

	handler := newModelCapabilityTestHandler(t)
	client := newTaskTestClient(t, handler)

	updated := mustRequestJSON[ModelCapabilityResponse](client, http.MethodPost, "/hecate/v1/model-capabilities/probes",
		`{"provider":"ollama","model":"llama3.1:8b","tool_calling":"parallel","note":"manual probe passed"}`)
	if updated.Data.ToolCalling != modelcaps.ToolCallingParallel || updated.Data.Source != modelcaps.SourceProbe {
		t.Fatalf("probe response = %+v", updated.Data)
	}

	models := mustRequestJSON[OpenAIModelsResponse](client, http.MethodGet, "/v1/models", "")
	caps := modelCapabilitiesFromModels(t, models, "llama3.1:8b")
	if caps["tool_calling"] != modelcaps.ToolCallingParallel {
		t.Fatalf("tool_calling = %#v, want parallel", caps["tool_calling"])
	}
	if caps["source"] != modelcaps.SourceProbe {
		t.Fatalf("source = %#v, want probe", caps["source"])
	}
}

func newModelCapabilityTestHandler(t *testing.T) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cpStore := controlplane.NewMemoryStore()
	provider := &fakeProvider{
		name: "ollama",
		capabilities: providers.Capabilities{
			Name:         "ollama",
			Kind:         providers.KindLocal,
			DefaultModel: "llama3.1:8b",
			Models:       []string{"llama3.1:8b"},
		},
	}
	return newTestHTTPHandlerWithSettings(logger, []providers.Provider{provider}, config.Config{}, cpStore)
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
