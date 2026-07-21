package api

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/modelcaps"
	"github.com/hecatehq/hecate/internal/modelprobe"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestModelToolCapabilityProbeEndpointVerifiesUnknownConfiguredModel(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		name:         "local-runtime",
		defaultModel: "custom-tool-model",
		response: &types.ChatResponse{Choices: []types.ChatChoice{{
			Message: types.Message{Role: "assistant", ToolCalls: []types.ToolCall{{
				ID:   "call_probe",
				Type: "function",
				Function: types.ToolCallFunction{
					Name:      "hecate_capability_probe",
					Arguments: "{}",
				},
			}}},
		}}},
		capabilities: providers.Capabilities{
			Name:         "local-runtime",
			Kind:         providers.KindLocal,
			DefaultModel: "custom-tool-model",
			Models:       []string{"custom-tool-model"},
			ModelCapabilities: map[string]types.ModelCapabilities{
				"custom-tool-model": {ToolCalling: modelcaps.ToolCallingUnknown},
			},
		},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := newTestHTTPHandlerForProviders(logger, []providers.Provider{provider}, config.Config{})
	client := newAPITestClient(t, handler)

	firstRecorder := client.mustRequest(http.MethodPost, "/hecate/v1/model-capabilities/tool-probes", `{"provider":"local-runtime","model":"custom-tool-model"}`)
	response := decodeRecorder[ModelToolCapabilityProbeResponse](t, firstRecorder)
	if response.Object != "model_tool_capability_probe" || !response.Data.Performed || response.Data.Provider != "local-runtime" || response.Data.Model != "custom-tool-model" {
		t.Fatalf("probe response = %+v", response)
	}
	if response.Data.Capabilities.ToolCalling != modelcaps.ToolCallingBasic || response.Data.Verification == nil || response.Data.Verification.Status != modelprobe.StatusSupported || response.Data.TraceID == "" {
		t.Fatalf("probe response capabilities = %+v verification=%+v trace=%q, want verified basic", response.Data.Capabilities, response.Data.Verification, response.Data.TraceID)
	}
	if strings.Contains(firstRecorder.Body.String(), "instance_id") || strings.Contains(firstRecorder.Body.String(), "provider_instance") {
		t.Fatalf("probe response exposed internal provider generation: %s", firstRecorder.Body.String())
	}

	provider.mu.Lock()
	request := provider.lastRequest
	calls := provider.calls
	provider.mu.Unlock()
	if calls != 1 || request.Model != "custom-tool-model" || request.Scope.ProviderHint != "local-runtime" || len(request.Tools) != 1 || request.Tools[0].Function.Name != "hecate_capability_probe" {
		t.Fatalf("provider calls=%d request=%+v, want one static probe request", calls, request)
	}

	cached := mustRequestJSON[ModelToolCapabilityProbeResponse](client, http.MethodPost, "/hecate/v1/model-capabilities/tool-probes", `{"provider":"local-runtime","model":"custom-tool-model"}`)
	if cached.Data.Performed || cached.Data.Capabilities.ToolCalling != modelcaps.ToolCallingBasic || cached.Data.Verification == nil || cached.Data.Verification.Status != modelprobe.StatusSupported {
		t.Fatalf("cached probe response = %+v, want cached verified result", cached.Data)
	}
	provider.mu.Lock()
	calls = provider.calls
	provider.mu.Unlock()
	if calls != 1 {
		t.Fatalf("provider calls after cached probe = %d, want 1", calls)
	}

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"hecate","workspace":%q,"provider":"local-runtime","model":"custom-tool-model"}`, t.TempDir()))
	if session.Data.Capabilities.ToolCalling != modelcaps.ToolCallingBasic || session.Data.Capabilities.ToolVerification == nil || session.Data.Capabilities.ToolVerification.Status != modelprobe.StatusSupported {
		t.Fatalf("Hecate Chat session capabilities = %+v, want the verified tool capability", session.Data.Capabilities)
	}
}

func TestModelToolCapabilityProbeEndpointRejectsAutoRoute(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewServer(logger, NewHandler(config.Config{}, logger, nil, nil, nil, nil))
	recorder := performRequest(t, handler, http.MethodPost, "/hecate/v1/model-capabilities/tool-probes", `{"provider":"auto","model":"custom-tool-model"}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "a configured provider and model are required") {
		t.Fatalf("error response = %s", recorder.Body.String())
	}
}
