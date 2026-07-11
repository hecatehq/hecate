package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/cairnlinebridge"
	"github.com/hecatehq/hecate/internal/projectassistant"
	"github.com/hecatehq/hecate/internal/projectassistantapp"
	"github.com/hecatehq/hecate/internal/projectworkapp"
	"go.opentelemetry.io/otel/trace"
)

type projectAssistantProposeRequest struct {
	ID      string                    `json:"id,omitempty"`
	Title   string                    `json:"title,omitempty"`
	Summary string                    `json:"summary,omitempty"`
	Actions []projectassistant.Action `json:"actions"`
}

type projectAssistantDraftRequest struct {
	ProjectID        string `json:"project_id"`
	WorkItemID       string `json:"work_item_id,omitempty"`
	Request          string `json:"request"`
	RoleID           string `json:"role_id,omitempty"`
	DriverKind       string `json:"driver_kind,omitempty"`
	DraftMode        string `json:"draft_mode,omitempty"`
	ReviewArtifactID string `json:"review_artifact_id,omitempty"`
	Provider         string `json:"provider,omitempty"`
	Model            string `json:"model,omitempty"`
}

type chatProjectAssistantDraftRequest struct {
	WorkItemID string `json:"work_item_id,omitempty"`
	Request    string `json:"request"`
	RoleID     string `json:"role_id,omitempty"`
	DriverKind string `json:"driver_kind,omitempty"`
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
	var context projectassistant.DraftContext
	var err error
	if h.projectReadRoutesUseCairnlineReadModel() {
		context, err = h.cairnlineProjectAssistantContext(r.Context(), projectassistant.ContextInput{
			ProjectID:  req.ProjectID,
			WorkItemID: req.WorkItemID,
			Request:    req.Request,
			RoleID:     req.RoleID,
			DriverKind: req.DriverKind,
		})
	} else {
		context, err = h.projectAssistantApplication().Context(r.Context(), projectassistantapp.ContextCommand{
			ProjectID:  req.ProjectID,
			WorkItemID: req.WorkItemID,
			Request:    req.Request,
			RoleID:     req.RoleID,
			DriverKind: req.DriverKind,
		})
		context.ReadBackend = "hecate"
	}
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
	proposal, err := h.projectAssistantDraft(r.Context(), projectassistantapp.DraftCommand{
		ProjectID:        req.ProjectID,
		WorkItemID:       req.WorkItemID,
		Request:          req.Request,
		RoleID:           req.RoleID,
		DriverKind:       req.DriverKind,
		DraftMode:        req.DraftMode,
		ReviewArtifactID: req.ReviewArtifactID,
		Provider:         req.Provider,
		Model:            req.Model,
		RequestID:        RequestIDFromContext(r.Context()),
		TraceID:          requestTraceID(r),
	})
	if err != nil {
		writeProjectAssistantError(w, err)
		return
	}
	h.mirrorProjectAssistantProposalByIDToCairnline(r.Context(), "project_assistant_draft", proposal.ID)
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
	proposal, err := h.projectAssistantDraft(r.Context(), projectassistantapp.DraftCommand{
		ProjectID:  projectID,
		WorkItemID: req.WorkItemID,
		Request:    req.Request,
		RoleID:     req.RoleID,
		DriverKind: req.DriverKind,
		DraftMode:  projectassistant.DraftModeDeterministic,
		RequestID:  RequestIDFromContext(r.Context()),
		TraceID:    requestTraceID(r),
	})
	if err != nil {
		writeProjectAssistantError(w, err)
		return
	}
	h.mirrorProjectAssistantProposalByIDToCairnline(r.Context(), "project_assistant_chat_draft", proposal.ID)
	WriteJSON(w, http.StatusOK, map[string]any{
		"object": "project_assistant.proposal",
		"data":   proposal,
	})
}

func (h *Handler) projectAssistantDraft(ctx context.Context, command projectassistantapp.DraftCommand) (projectassistant.Proposal, error) {
	if h.projectReadRoutesUseCairnlineReadModel() {
		return h.cairnlineProjectAssistantDraft(ctx, command)
	}
	return h.projectAssistantApplication().Draft(ctx, command)
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
	h.mirrorProjectAssistantProposalByIDToCairnline(r.Context(), "project_assistant_propose", proposal.ID)
	WriteJSON(w, http.StatusOK, map[string]any{
		"object": "project_assistant.proposal",
		"data":   proposal,
	})
}

func (h *Handler) HandleProjectAssistantProposal(w http.ResponseWriter, r *http.Request) {
	if h.projectReadRoutesUseCairnlineReadModel() {
		record, ok, err := h.cairnlineProjectAssistantProposal(r.Context(), r.PathValue("id"))
		if err != nil {
			writeProjectAssistantError(w, err)
			return
		}
		writeProjectAssistantProposalRecord(w, record, ok)
		return
	}
	record, ok, err := h.hecateProjectAssistantProposal(r.Context(), r.PathValue("id"))
	if err != nil {
		writeProjectAssistantError(w, err)
		return
	}
	writeProjectAssistantProposalRecord(w, record, ok)
}

func (h *Handler) hecateProjectAssistantProposal(ctx context.Context, id string) (projectassistant.ProposalRecord, bool, error) {
	return h.projectAssistantApplication().Proposal(ctx, id)
}

func writeProjectAssistantProposalRecord(w http.ResponseWriter, record projectassistant.ProposalRecord, ok bool) {
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project assistant proposal not found")
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"object": "project_assistant.proposal_record",
		"data":   record,
	})
}

func (h *Handler) cairnlineProjectAssistantProposal(ctx context.Context, id string) (projectassistant.ProposalRecord, bool, error) {
	strictEmbedded := h.requiresEmbeddedCairnlineProjectReads()
	if h.prefersEmbeddedCairnlineProjectReads() {
		_, service, store, err := h.openCairnlineEmbeddedService(ctx)
		if err == nil {
			defer store.Close()
			record, ok, err := cairnlineProjectAssistantProposalFromService(ctx, service, id)
			if err != nil || ok || strictEmbedded {
				return record, ok, err
			}
		} else if strictEmbedded || !errors.Is(err, cairnline.ErrNotFound) {
			return projectassistant.ProposalRecord{}, false, err
		}
	}

	snapshots, err := cairnlinebridge.LoadSnapshots(ctx, h.cairnlineSnapshotSources())
	if err != nil {
		return projectassistant.ProposalRecord{}, false, err
	}
	service := cairnline.NewMemoryService()
	if err := cairnlinebridge.SeedSnapshots(ctx, service, snapshots); err != nil {
		return projectassistant.ProposalRecord{}, false, err
	}
	return cairnlineProjectAssistantProposalFromService(ctx, service, id)
}

func cairnlineProjectAssistantProposalFromService(ctx context.Context, service *cairnline.Service, id string) (projectassistant.ProposalRecord, bool, error) {
	item, err := service.GetAssistantProposal(ctx, id)
	if errors.Is(err, cairnline.ErrNotFound) {
		return projectassistant.ProposalRecord{}, false, nil
	}
	if err != nil {
		return projectassistant.ProposalRecord{}, false, err
	}
	record, ok := cairnlinebridge.ProjectAssistantProposalRecord(item)
	return record, ok, nil
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
		var applyErr *projectassistant.ApplyError
		if errors.As(err, &applyErr) {
			h.mirrorProjectAssistantApplyResultToCairnline(r.Context(), "project_assistant_apply_partial", applyErr.Result)
		}
		h.mirrorProjectAssistantProposalByIDToCairnline(r.Context(), "project_assistant_apply_error", req.Proposal.ID)
		writeProjectAssistantApplyError(w, err)
		return
	}
	h.mirrorProjectAssistantApplyResultToCairnline(r.Context(), "project_assistant_apply_result", result)
	h.mirrorProjectAssistantProposalByIDToCairnline(r.Context(), "project_assistant_apply", firstNonEmpty(result.ProposalID, req.Proposal.ID))
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
	fields := map[string]any{
		"apply_status":           applyErr.Result.Status,
		"failed_action_index":    applyErr.FailedActionIndex,
		"total_action_count":     applyErr.Result.TotalActionCount,
		"committed_action_count": applyErr.Result.CommittedActionCount,
		"resume_action_index":    applyErr.Result.ResumeActionIndex,
		"partial_result":         applyErr.Result,
	}
	operatorAction := ""
	var closeoutErr projectworkapp.WorkItemCloseoutBlockedError
	if errors.As(err, &closeoutErr) {
		operatorAction = "Resolve closeout readiness blockers, refresh the work item, then retry."
		fields["readiness"] = renderProjectWorkItemReadiness(closeoutErr.Readiness)
	}
	WriteErrorDetails(w, status, code, err.Error(), ErrorDetails{
		OperatorAction: operatorAction,
		Fields:         fields,
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
