package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/hecatehq/hecate/internal/modelapp"
	"github.com/hecatehq/hecate/pkg/types"
)

const maxModelToolProbeRequestBytes = 4 << 10

type ModelToolCapabilityProbeRequest struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type ModelToolCapabilityProbeResponse struct {
	Object string                               `json:"object"`
	Data   ModelToolCapabilityProbeResponseItem `json:"data"`
}

type ModelToolCapabilityProbeResponseItem struct {
	Provider     string                            `json:"provider"`
	Model        string                            `json:"model"`
	Capabilities types.ModelCapabilities           `json:"capabilities"`
	Verification *types.ToolCapabilityVerification `json:"verification,omitempty"`
	TraceID      string                            `json:"trace_id,omitempty"`
	Performed    bool                              `json:"performed"`
}

// HandleModelToolCapabilityProbe performs one explicit, harmless diagnostic
// call for an otherwise-unknown configured provider/model. The input contains
// no workspace or message data; Hecate supplies the static probe request.
//
// POST /hecate/v1/model-capabilities/tool-probes
func (h *Handler) HandleModelToolCapabilityProbe(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxModelToolProbeRequestBytes)
	var request ModelToolCapabilityProbeRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	request.Provider = strings.TrimSpace(request.Provider)
	request.Model = strings.TrimSpace(request.Model)
	if request.Provider == "" || strings.EqualFold(request.Provider, "auto") || request.Model == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "a configured provider and model are required")
		return
	}

	result, err := h.modelApplication().VerifyToolCalling(r.Context(), request.Provider, request.Model)
	if err != nil && !errors.Is(err, modelapp.ErrToolProbeNotNeeded) {
		var readinessErr modelapp.ReadinessError
		switch {
		case errors.Is(err, modelapp.ErrToolProbeUnavailable):
			WriteError(w, http.StatusServiceUnavailable, errCodeModelToolProbeUnavailable, "model tool capability probing is unavailable")
		case errors.Is(err, modelapp.ErrProviderAmbiguous):
			WriteError(w, http.StatusConflict, errCodeProviderAmbiguous, "provider identity is ambiguous")
		case errors.Is(err, modelapp.ErrToolProbeRouteChanged):
			WriteError(w, http.StatusConflict, errCodeModelNotConfigured, "selected provider/model changed while verification was running")
		case errors.As(err, &readinessErr):
			WriteError(w, http.StatusConflict, errCodeModelNotConfigured, "selected provider/model is not routable")
		default:
			WriteError(w, http.StatusConflict, errCodeModelNotConfigured, "selected provider/model is not available for verification")
		}
		return
	}

	WriteJSON(w, http.StatusOK, ModelToolCapabilityProbeResponse{
		Object: "model_tool_capability_probe",
		Data: ModelToolCapabilityProbeResponseItem{
			Provider:     result.Provider,
			Model:        result.Model,
			Capabilities: result.Capabilities,
			Verification: result.Verification,
			TraceID:      result.TraceID,
			Performed:    result.Performed,
		},
	})
}
