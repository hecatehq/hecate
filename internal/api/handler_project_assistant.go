package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/hecatehq/hecate/internal/projectassistant"
	"go.opentelemetry.io/otel/trace"
)

type projectAssistantProposeRequest struct {
	ID      string                    `json:"id,omitempty"`
	Title   string                    `json:"title,omitempty"`
	Summary string                    `json:"summary,omitempty"`
	Actions []projectassistant.Action `json:"actions"`
}

type projectAssistantApplyRequest struct {
	Proposal projectassistant.Proposal `json:"proposal"`
	Confirm  bool                      `json:"confirm,omitempty"`
}

func (h *Handler) HandleProjectAssistantPropose(w http.ResponseWriter, r *http.Request) {
	var req projectAssistantProposeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "invalid project assistant proposal request")
		return
	}
	proposal, err := h.projectAssistantService().Propose(r.Context(), projectassistant.ProposalInput{
		ID:      req.ID,
		Title:   req.Title,
		Summary: req.Summary,
		Actions: req.Actions,
		TraceID: requestTraceID(r),
	})
	if err != nil {
		writeProjectAssistantError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"object": "project_assistant.proposal",
		"data":   proposal,
	})
}

func (h *Handler) HandleProjectAssistantApply(w http.ResponseWriter, r *http.Request) {
	var req projectAssistantApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "invalid project assistant apply request")
		return
	}
	result, err := h.projectAssistantService().Apply(r.Context(), req.Proposal, req.Confirm)
	if err != nil {
		writeProjectAssistantError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"object": "project_assistant.apply_result",
		"data":   result,
	})
}

func (h *Handler) projectAssistantService() *projectassistant.Service {
	h.projectAssistantMu.Lock()
	defer h.projectAssistantMu.Unlock()
	if h.projectAssistant == nil {
		h.projectAssistant = projectassistant.NewService(projectassistant.Stores{
			Projects:         h.projects,
			Chats:            h.agentChat,
			Work:             h.projectWork,
			MemoryCandidates: h.memoryCandidates,
		}, newOpaqueTaskResourceID)
	}
	return h.projectAssistant
}

func writeProjectAssistantError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, projectassistant.ErrUnknownActionKind), errors.Is(err, projectassistant.ErrInvalid):
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
	case errors.Is(err, projectassistant.ErrNotFound):
		WriteError(w, http.StatusNotFound, errCodeNotFound, err.Error())
	case errors.Is(err, projectassistant.ErrConfirmationRequired), errors.Is(err, projectassistant.ErrConflict):
		WriteError(w, http.StatusConflict, errCodeConflict, err.Error())
	default:
		WriteError(w, http.StatusInternalServerError, errCodeInternalError, err.Error())
	}
}

func requestTraceID(r *http.Request) string {
	if r == nil {
		return ""
	}
	spanCtx := trace.SpanContextFromContext(r.Context())
	if !spanCtx.HasTraceID() {
		return ""
	}
	return spanCtx.TraceID().String()
}
