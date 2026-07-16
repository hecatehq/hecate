package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/hecatehq/hecate/internal/projectassistant"
)

func sameProjectMutationIDs(left, right []string) bool {
	left = normalizeProjectMutationIDs(left)
	right = normalizeProjectMutationIDs(right)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (h *Handler) beginProjectAssistantMutation(
	ctx context.Context,
	proposal projectassistant.Proposal,
	knownProjectIDs ...string,
) (context.Context, func(), string, error) {
	if h == nil {
		return ctx, nil, "", projectassistant.ErrStoreNotConfigured
	}
	if err := projectassistant.ValidateProposalActions(proposal); err != nil {
		return ctx, nil, "", err
	}
	projectIDs, err := h.projectAssistantMutationProjectIDs(ctx, proposal, knownProjectIDs...)
	if err != nil {
		return ctx, nil, "", err
	}
	projectID := projectIDs[0]
	mutationCtx, release, err := h.projectMutationGate.beginMany(ctx, projectIDs)
	if err != nil {
		return ctx, nil, "", err
	}
	return mutationCtx, release, projectID, nil
}

func (h *Handler) projectAssistantMutationProjectIDs(
	ctx context.Context,
	proposal projectassistant.Proposal,
	knownProjectIDs ...string,
) ([]string, error) {
	projectIDs := make([]string, 0, len(knownProjectIDs)+len(proposal.Actions))
	seen := make(map[string]struct{}, cap(projectIDs))
	appendProjectID := func(projectID string) {
		projectID = strings.TrimSpace(projectID)
		if projectID == "" {
			return
		}
		if _, ok := seen[projectID]; ok {
			return
		}
		seen[projectID] = struct{}{}
		projectIDs = append(projectIDs, projectID)
	}
	for _, projectID := range projectassistant.ProposalProjectIDs(proposal) {
		appendProjectID(projectID)
	}
	for _, projectID := range knownProjectIDs {
		appendProjectID(projectID)
	}
	for _, action := range proposal.Actions {
		if strings.TrimSpace(action.Kind) != projectassistant.ActionMoveChatSession {
			continue
		}
		sessionID := strings.TrimSpace(action.Target["chat_session_id"])
		if sessionID == "" {
			continue
		}
		if h == nil || h.agentChat == nil {
			return nil, projectassistant.ErrStoreNotConfigured
		}
		session, ok, err := h.agentChat.Get(ctx, sessionID)
		if err != nil {
			return nil, err
		}
		if ok {
			appendProjectID(session.ProjectID)
		}
	}

	if len(projectIDs) == 0 {
		return nil, fmt.Errorf("%w: project assistant mutation must identify at least one project", projectassistant.ErrInvalid)
	}
	return projectIDs, nil
}
