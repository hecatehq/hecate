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

type projectAssistantDraftRequest struct {
	ProjectID  string `json:"project_id"`
	WorkItemID string `json:"work_item_id,omitempty"`
	Request    string `json:"request"`
	RoleID     string `json:"role_id,omitempty"`
	DriverKind string `json:"driver_kind,omitempty"`
	DraftMode  string `json:"draft_mode,omitempty"`
	Provider   string `json:"provider,omitempty"`
	Model      string `json:"model,omitempty"`
}

type projectAssistantContextRequest struct {
	ProjectID  string `json:"project_id"`
	WorkItemID string `json:"work_item_id,omitempty"`
	Request    string `json:"request"`
	RoleID     string `json:"role_id,omitempty"`
	DriverKind string `json:"driver_kind,omitempty"`
}

type projectAssistantApplyRequest struct {
	Proposal projectassistant.Proposal `json:"proposal"`
	Confirm  bool                      `json:"confirm,omitempty"`
}

func (h *Handler) HandleProjectAssistantContext(w http.ResponseWriter, r *http.Request) {
	var req projectAssistantContextRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "invalid project assistant context request")
		return
	}
	context, err := h.projectAssistantService().Context(r.Context(), projectassistant.ContextInput{
		ProjectID:  req.ProjectID,
		WorkItemID: req.WorkItemID,
		Request:    req.Request,
		RoleID:     req.RoleID,
		DriverKind: req.DriverKind,
	})
	if err != nil {
		writeProjectAssistantError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"object": "project_assistant.context",
		"data":   context,
	})
}

func (h *Handler) HandleProjectAssistantDraft(w http.ResponseWriter, r *http.Request) {
	var req projectAssistantDraftRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "invalid project assistant draft request")
		return
	}
	proposal, err := h.projectAssistantService().Draft(r.Context(), projectassistant.DraftInput{
		ProjectID:  req.ProjectID,
		WorkItemID: req.WorkItemID,
		Request:    req.Request,
		RoleID:     req.RoleID,
		DriverKind: req.DriverKind,
		DraftMode:  req.DraftMode,
		Provider:   req.Provider,
		Model:      req.Model,
		RequestID:  RequestIDFromContext(r.Context()),
		TraceID:    requestTraceID(r),
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
		writeProjectAssistantApplyError(w, err)
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
			ProjectSkills:    h.projectSkills,
			Memory:           h.memory,
			MemoryCandidates: h.memoryCandidates,
			LLM:              gatewayAgentLLMClient{service: h.service},
		}, newOpaqueTaskResourceID)
	}
	return h.projectAssistant
}

func writeProjectAssistantApplyError(w http.ResponseWriter, err error) {
	var applyErr *projectassistant.ApplyError
	if !errors.As(err, &applyErr) {
		writeProjectAssistantError(w, err)
		return
	}
	status, code := projectAssistantErrorStatusCode(err)
	WriteErrorDetails(w, status, code, err.Error(), ErrorDetails{
		Fields: map[string]any{
			"failed_action_index": applyErr.FailedActionIndex,
			"partial_result":      applyErr.Result,
		},
	})
}

func writeProjectAssistantError(w http.ResponseWriter, err error) {
	status, code := projectAssistantErrorStatusCode(err)
	WriteError(w, status, code, err.Error())
}

func projectAssistantErrorStatusCode(err error) (int, string) {
	switch {
	case errors.Is(err, projectassistant.ErrUnknownActionKind), errors.Is(err, projectassistant.ErrInvalid):
		return http.StatusBadRequest, errCodeInvalidRequest
	case errors.Is(err, projectassistant.ErrNotFound):
		return http.StatusNotFound, errCodeNotFound
	case errors.Is(err, projectassistant.ErrConfirmationRequired), errors.Is(err, projectassistant.ErrConflict):
		return http.StatusConflict, errCodeConflict
	default:
		return http.StatusInternalServerError, errCodeInternalError
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
