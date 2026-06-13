package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/hecatehq/hecate/internal/projectassistant"
	"github.com/hecatehq/hecate/internal/projectassistantapp"
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

type chatProjectAssistantDraftRequest struct {
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
	context, err := h.projectAssistantApplication().Context(r.Context(), projectassistantapp.ContextCommand{
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
	proposal, err := h.projectAssistantApplication().Draft(r.Context(), projectassistantapp.DraftCommand{
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

func (h *Handler) HandleChatProjectAssistantDraft(w http.ResponseWriter, r *http.Request) {
	result, err := h.chatApplication().GetSession(r.Context(), r.PathValue("id"))
	if err != nil {
		if writeChatAppError(w, err) {
			return
		}
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	session := result.Session
	if isExternalChatSession(session) {
		WriteError(w, http.StatusConflict, errCodeRuntimeMismatch, "Project Assistant chat drafts are available for Hecate Chat sessions")
		return
	}
	projectID := strings.TrimSpace(session.ProjectID)
	if projectID == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "Project Assistant chat drafts require a project-linked chat session")
		return
	}
	var req chatProjectAssistantDraftRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "invalid chat project assistant draft request")
		return
	}
	req.Request = strings.TrimSpace(req.Request)
	if req.Request == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "request is required")
		return
	}
	proposal, err := h.projectAssistantApplication().Draft(r.Context(), projectassistantapp.DraftCommand{
		ProjectID:  projectID,
		WorkItemID: req.WorkItemID,
		Request:    req.Request,
		RoleID:     req.RoleID,
		DriverKind: req.DriverKind,
		DraftMode:  req.DraftMode,
		Provider:   firstNonEmpty(req.Provider, session.Provider),
		Model:      firstNonEmpty(req.Model, session.Model),
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
	proposal, err := h.projectAssistantApplication().Propose(r.Context(), projectassistantapp.ProposeCommand{
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
	result, err := h.projectAssistantApplication().Apply(r.Context(), projectassistantapp.ApplyCommand{
		Proposal: req.Proposal,
		Confirm:  req.Confirm,
	})
	if err != nil {
		writeProjectAssistantApplyError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"object": "project_assistant.apply_result",
		"data":   result,
	})
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
