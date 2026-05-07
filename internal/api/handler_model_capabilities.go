package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/hecate/agent-runtime/internal/controlplane"
	"github.com/hecate/agent-runtime/internal/modelcaps"
)

func (h *Handler) HandleUpsertModelCapabilityOverride(w http.ResponseWriter, r *http.Request) {
	if !h.requireSettings(w, r) {
		return
	}
	req, ok := decodeModelCapabilityRequest(w, r)
	if !ok {
		return
	}
	req.ToolCalling = modelcaps.NormalizeToolCalling(req.ToolCalling)
	if req.ToolCalling == modelcaps.ToolCallingUnknown {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "tool_calling must be one of unknown, none, basic, parallel")
		return
	}
	record, err := h.controlPlane.UpsertModelCapabilityOverride(r.Context(), modelCapabilityRecordFromRequest(req, modelcaps.SourceOperatorOverride))
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, ModelCapabilityResponse{Object: "model_capability", Data: renderModelCapabilityRecord(record)})
}

func (h *Handler) HandleDeleteModelCapabilityOverride(w http.ResponseWriter, r *http.Request) {
	if !h.requireSettings(w, r) {
		return
	}
	provider := strings.TrimSpace(r.URL.Query().Get("provider"))
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	if provider == "" || model == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "provider and model query parameters are required")
		return
	}
	if err := h.controlPlane.DeleteModelCapabilityOverride(r.Context(), provider, model); err != nil {
		WriteError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) HandleRecordModelCapabilityProbe(w http.ResponseWriter, r *http.Request) {
	if !h.requireSettings(w, r) {
		return
	}
	req, ok := decodeModelCapabilityRequest(w, r)
	if !ok {
		return
	}
	req.ToolCalling = modelcaps.NormalizeToolCalling(req.ToolCalling)
	if req.ToolCalling == modelcaps.ToolCallingUnknown {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "manual probe result must set tool_calling to none, basic, or parallel")
		return
	}
	record, err := h.controlPlane.UpsertModelCapabilityProbe(r.Context(), modelCapabilityRecordFromRequest(req, modelcaps.SourceProbe))
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, ModelCapabilityResponse{Object: "model_capability", Data: renderModelCapabilityRecord(record)})
}

func decodeModelCapabilityRequest(w http.ResponseWriter, r *http.Request) (ModelCapabilityUpsertRequest, bool) {
	var req ModelCapabilityUpsertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "invalid JSON body")
		return ModelCapabilityUpsertRequest{}, false
	}
	req.Provider = strings.TrimSpace(req.Provider)
	req.Model = strings.TrimSpace(req.Model)
	req.Note = strings.TrimSpace(req.Note)
	if req.Provider == "" || req.Model == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "provider and model are required")
		return ModelCapabilityUpsertRequest{}, false
	}
	return req, true
}

func modelCapabilityRecordFromRequest(req ModelCapabilityUpsertRequest, source string) controlplane.ModelCapabilityRecord {
	return controlplane.ModelCapabilityRecord{
		Provider:         req.Provider,
		Model:            req.Model,
		ToolCalling:      req.ToolCalling,
		Streaming:        req.Streaming,
		MaxContextTokens: req.MaxContextTokens,
		Source:           source,
		Note:             req.Note,
	}
}

func renderModelCapabilityRecord(record controlplane.ModelCapabilityRecord) ModelCapabilityItem {
	streaming := false
	if record.Streaming != nil {
		streaming = *record.Streaming
	}
	return ModelCapabilityItem{
		Provider:         record.Provider,
		Model:            record.Model,
		ToolCalling:      modelcaps.NormalizeToolCalling(record.ToolCalling),
		Streaming:        streaming,
		MaxContextTokens: record.MaxContextTokens,
		Source:           record.Source,
		Note:             record.Note,
		UpdatedAt:        formatOptionalTime(record.UpdatedAt),
	}
}
