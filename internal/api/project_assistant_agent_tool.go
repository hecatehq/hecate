package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/internal/projectassistant"
	"github.com/hecatehq/hecate/internal/projectassistantapp"
)

func (h *Handler) DraftProjectProposal(ctx context.Context, input orchestrator.ProjectAssistantDraftInput) (orchestrator.ProjectAssistantDraftResult, error) {
	projectID := strings.TrimSpace(input.ProjectID)
	if projectID == "" {
		return orchestrator.ProjectAssistantDraftResult{}, fmt.Errorf("%w: project_id is required", projectassistant.ErrInvalid)
	}
	request := strings.TrimSpace(input.Request)
	if request == "" {
		return orchestrator.ProjectAssistantDraftResult{}, fmt.Errorf("%w: request is required", projectassistant.ErrInvalid)
	}
	sourceSessionID := strings.TrimSpace(input.SourceChatSessionID)
	if sourceSessionID != "" {
		sessionResult, err := h.chatApplication().GetSession(ctx, sourceSessionID)
		if err != nil {
			return orchestrator.ProjectAssistantDraftResult{}, err
		}
		session := sessionResult.Session
		if isExternalChatSession(session) {
			return orchestrator.ProjectAssistantDraftResult{}, fmt.Errorf("%w: Project Assistant draft tool requires a Hecate Chat session", projectassistant.ErrInvalid)
		}
		if got := strings.TrimSpace(session.ProjectID); got != projectID {
			return orchestrator.ProjectAssistantDraftResult{}, fmt.Errorf("%w: chat session project %q does not match task project %q", projectassistant.ErrInvalid, got, projectID)
		}
	}
	mutationCtx, release, _, err := h.beginProjectAssistantMutation(ctx, projectassistant.Proposal{}, projectID)
	if err != nil {
		return orchestrator.ProjectAssistantDraftResult{}, err
	}
	defer release()
	ctx = mutationCtx

	proposal, err := h.projectAssistantDraft(ctx, projectassistantapp.DraftCommand{
		ProjectID:  projectID,
		WorkItemID: input.WorkItemID,
		Request:    request,
		RoleID:     input.RoleID,
		DriverKind: input.DriverKind,
		DraftMode:  projectassistant.DraftModeDeterministic,
		RequestID:  input.RequestID,
		TraceID:    input.TraceID,
	})
	if err != nil {
		return orchestrator.ProjectAssistantDraftResult{}, err
	}
	rawProposal, err := json.Marshal(proposal)
	if err != nil {
		return orchestrator.ProjectAssistantDraftResult{}, fmt.Errorf("%w: encode proposal: %v", projectassistant.ErrInvalid, err)
	}
	return orchestrator.ProjectAssistantDraftResult{
		Object:              "project_assistant.chat_proposal",
		ProjectID:           projectID,
		SourceChatSessionID: sourceSessionID,
		Request:             request,
		ProposalID:          proposal.ID,
		Title:               proposal.Title,
		Summary:             proposal.Summary,
		ActionCount:         len(proposal.Actions),
		Proposal:            rawProposal,
	}, nil
}
