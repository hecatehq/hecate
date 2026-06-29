package api

import (
	"context"
	"errors"
	"fmt"

	"github.com/hecatehq/hecate/internal/projectassistant"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/projectworkapp"
)

type projectAssistantWorkAuthority struct {
	handler *Handler
}

func (h *Handler) projectAssistantWorkAuthorityForApplication() projectassistant.WorkAuthority {
	if h == nil {
		return nil
	}
	return projectAssistantWorkAuthority{handler: h}
}

func (authority projectAssistantWorkAuthority) CreateRole(ctx context.Context, projectID string, cmd projectassistant.WorkRoleCommand) (projectwork.AgentRoleProfile, error) {
	h := authority.handler
	if h == nil {
		return projectwork.AgentRoleProfile{}, projectassistant.ErrStoreNotConfigured
	}
	appCmd := projectworkapp.CreateRoleCommand{
		ID:                  cmd.ID,
		Name:                cmd.Name,
		Description:         cmd.Description,
		Instructions:        cmd.Instructions,
		DefaultDriverKind:   cmd.DefaultDriverKind,
		DefaultProvider:     cmd.DefaultProvider,
		DefaultModel:        cmd.DefaultModel,
		DefaultAgentProfile: cmd.DefaultAgentProfile,
		SkillIDs:            append([]string(nil), cmd.SkillIDs...),
	}
	var (
		role projectwork.AgentRoleProfile
		err  error
	)
	if h.projectRoleWritesUseCairnlineAuthority() {
		role, err = h.createProjectWorkRoleWithCairnlineAuthority(ctx, projectID, appCmd)
	} else {
		role, err = h.projectWorkApplication().CreateRole(ctx, projectID, appCmd)
	}
	return role, projectAssistantApplyWorkError(err)
}

func (authority projectAssistantWorkAuthority) CreateWorkItem(ctx context.Context, projectID string, cmd projectassistant.WorkItemCommand) (projectwork.WorkItem, error) {
	h := authority.handler
	if h == nil {
		return projectwork.WorkItem{}, projectassistant.ErrStoreNotConfigured
	}
	appCmd := projectworkapp.CreateWorkItemCommand{
		ID:              cmd.ID,
		Title:           cmd.Title,
		Brief:           cmd.Brief,
		Status:          cmd.Status,
		Priority:        cmd.Priority,
		OwnerRoleID:     cmd.OwnerRoleID,
		ReviewerRoleIDs: append([]string(nil), cmd.ReviewerRoleIDs...),
	}
	var (
		item projectwork.WorkItem
		err  error
	)
	if h.projectWorkItemWritesUseCairnlineAuthority() {
		item, err = h.createProjectWorkItemWithCairnlineAuthority(ctx, projectID, appCmd)
	} else {
		item, err = h.projectWorkApplication().CreateWorkItem(ctx, projectID, appCmd)
	}
	return item, projectAssistantApplyWorkError(err)
}

func (authority projectAssistantWorkAuthority) UpdateWorkItem(ctx context.Context, projectID, workItemID string, cmd projectassistant.WorkItemUpdateCommand) (projectwork.WorkItem, error) {
	h := authority.handler
	if h == nil {
		return projectwork.WorkItem{}, projectassistant.ErrStoreNotConfigured
	}
	appCmd := projectworkapp.UpdateWorkItemCommand{
		Title:       cmd.Title,
		Brief:       cmd.Brief,
		Status:      cmd.Status,
		Priority:    cmd.Priority,
		OwnerRoleID: cmd.OwnerRoleID,
	}
	if cmd.ReviewerRoleIDs != nil {
		reviewerRoleIDs := append([]string(nil), *cmd.ReviewerRoleIDs...)
		appCmd.ReviewerRoleIDs = &reviewerRoleIDs
	}
	var (
		item projectwork.WorkItem
		err  error
	)
	if h.projectWorkItemWritesUseCairnlineAuthority() {
		item, err = h.updateProjectWorkItemWithCairnlineAuthority(ctx, projectID, workItemID, appCmd)
	} else {
		item, err = h.projectWorkApplication().UpdateWorkItem(ctx, projectID, workItemID, appCmd)
	}
	return item, projectAssistantApplyWorkError(err)
}

func (authority projectAssistantWorkAuthority) CreateAssignment(ctx context.Context, projectID, workItemID string, cmd projectassistant.WorkAssignmentCommand) (projectwork.Assignment, error) {
	h := authority.handler
	if h == nil {
		return projectwork.Assignment{}, projectassistant.ErrStoreNotConfigured
	}
	appCmd := projectworkapp.CreateAssignmentCommand{
		ID:         cmd.ID,
		RoleID:     cmd.RoleID,
		RootID:     cmd.RootID,
		DriverKind: cmd.DriverKind,
		Status:     cmd.Status,
	}
	var (
		assignment projectwork.Assignment
		err        error
	)
	if h.projectAssignmentWritesUseCairnlineAuthority() {
		assignment, err = h.createProjectWorkAssignmentWithCairnlineAuthority(ctx, projectID, workItemID, appCmd)
	} else {
		assignment, err = h.projectWorkApplication().CreateAssignment(ctx, projectID, workItemID, appCmd)
	}
	return assignment, projectAssistantApplyWorkError(err)
}

func (authority projectAssistantWorkAuthority) CreateHandoff(ctx context.Context, projectID, workItemID string, cmd projectassistant.WorkHandoffCommand) (projectwork.Handoff, error) {
	h := authority.handler
	if h == nil {
		return projectwork.Handoff{}, projectassistant.ErrStoreNotConfigured
	}
	appCmd := projectworkapp.CreateHandoffCommand{
		ID:                    cmd.ID,
		SourceAssignmentID:    cmd.SourceAssignmentID,
		SourceRunID:           cmd.SourceRunID,
		SourceChatSessionID:   cmd.SourceChatSessionID,
		SourceMessageID:       cmd.SourceMessageID,
		TargetRoleID:          cmd.TargetRoleID,
		TargetAssignmentID:    cmd.TargetAssignmentID,
		TargetWorkItemID:      cmd.TargetWorkItemID,
		Title:                 cmd.Title,
		Summary:               cmd.Summary,
		RecommendedNextAction: cmd.RecommendedNextAction,
		LinkedArtifactIDs:     append([]string(nil), cmd.LinkedArtifactIDs...),
		LinkedMemoryIDs:       append([]string(nil), cmd.LinkedMemoryIDs...),
		ContextRefs:           append([]string(nil), cmd.ContextRefs...),
		Status:                cmd.Status,
		ProvenanceKind:        cmd.ProvenanceKind,
		TrustLabel:            cmd.TrustLabel,
		CreatedByRoleID:       cmd.CreatedByRoleID,
	}
	var (
		handoff projectwork.Handoff
		err     error
	)
	if h.projectCollaborationWritesUseCairnlineAuthority() {
		handoff, err = h.createProjectHandoffWithCairnlineAuthority(ctx, projectID, workItemID, appCmd)
	} else {
		handoff, err = h.projectWorkApplication().CreateHandoff(ctx, projectID, workItemID, appCmd)
	}
	return handoff, projectAssistantApplyWorkError(err)
}

func (authority projectAssistantWorkAuthority) UpdateHandoff(ctx context.Context, projectID, workItemID, handoffID string, cmd projectassistant.WorkHandoffUpdateCommand) (projectwork.Handoff, error) {
	h := authority.handler
	if h == nil {
		return projectwork.Handoff{}, projectassistant.ErrStoreNotConfigured
	}
	appCmd := projectworkapp.UpdateHandoffCommand{
		TargetAssignmentID: cmd.TargetAssignmentID,
		TargetRoleID:       cmd.TargetRoleID,
		Status:             cmd.Status,
	}
	var (
		handoff projectwork.Handoff
		err     error
	)
	if h.projectCollaborationWritesUseCairnlineAuthority() {
		handoff, err = h.updateProjectHandoffWithCairnlineAuthority(ctx, projectID, workItemID, handoffID, appCmd)
	} else {
		handoff, err = h.projectWorkApplication().UpdateHandoff(ctx, projectID, workItemID, handoffID, appCmd)
	}
	return handoff, projectAssistantApplyWorkError(err)
}

func projectAssistantApplyWorkError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, projectworkapp.ErrWorkItemCloseoutBlocked) {
		return fmt.Errorf("%w: %w", projectassistant.ErrConflict, err)
	}
	return err
}
