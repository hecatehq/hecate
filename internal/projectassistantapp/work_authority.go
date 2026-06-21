package projectassistantapp

import (
	"context"
	"errors"
	"fmt"

	"github.com/hecatehq/hecate/internal/projectassistant"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/projectworkapp"
)

type projectWorkAuthority struct {
	app *projectworkapp.Application
}

func (authority projectWorkAuthority) CreateRole(ctx context.Context, projectID string, cmd projectassistant.WorkRoleCommand) (projectwork.AgentRoleProfile, error) {
	role, err := authority.app.CreateRole(ctx, projectID, projectworkapp.CreateRoleCommand{
		ID:                  cmd.ID,
		Name:                cmd.Name,
		Description:         cmd.Description,
		Instructions:        cmd.Instructions,
		DefaultDriverKind:   cmd.DefaultDriverKind,
		DefaultProvider:     cmd.DefaultProvider,
		DefaultModel:        cmd.DefaultModel,
		DefaultAgentProfile: cmd.DefaultAgentProfile,
		SkillIDs:            append([]string(nil), cmd.SkillIDs...),
	})
	if err != nil {
		return projectwork.AgentRoleProfile{}, mapProjectWorkApplicationErr(err)
	}
	return role, nil
}

func (authority projectWorkAuthority) CreateWorkItem(ctx context.Context, projectID string, cmd projectassistant.WorkItemCommand) (projectwork.WorkItem, error) {
	item, err := authority.app.CreateWorkItem(ctx, projectID, projectworkapp.CreateWorkItemCommand{
		ID:              cmd.ID,
		Title:           cmd.Title,
		Brief:           cmd.Brief,
		Status:          cmd.Status,
		Priority:        cmd.Priority,
		OwnerRoleID:     cmd.OwnerRoleID,
		ReviewerRoleIDs: append([]string(nil), cmd.ReviewerRoleIDs...),
	})
	if err != nil {
		return projectwork.WorkItem{}, mapProjectWorkApplicationErr(err)
	}
	return item, nil
}

func (authority projectWorkAuthority) UpdateWorkItem(ctx context.Context, projectID, workItemID string, cmd projectassistant.WorkItemUpdateCommand) (projectwork.WorkItem, error) {
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
	item, err := authority.app.UpdateWorkItem(ctx, projectID, workItemID, appCmd)
	if err != nil {
		return projectwork.WorkItem{}, mapProjectWorkApplicationErr(err)
	}
	return item, nil
}

func (authority projectWorkAuthority) CreateAssignment(ctx context.Context, projectID, workItemID string, cmd projectassistant.WorkAssignmentCommand) (projectwork.Assignment, error) {
	assignment, err := authority.app.CreateAssignment(ctx, projectID, workItemID, projectworkapp.CreateAssignmentCommand{
		ID:         cmd.ID,
		RoleID:     cmd.RoleID,
		RootID:     cmd.RootID,
		DriverKind: cmd.DriverKind,
		Status:     cmd.Status,
	})
	if err != nil {
		return projectwork.Assignment{}, mapProjectWorkApplicationErr(err)
	}
	return assignment, nil
}

func (authority projectWorkAuthority) CreateHandoff(ctx context.Context, projectID, workItemID string, cmd projectassistant.WorkHandoffCommand) (projectwork.Handoff, error) {
	handoff, err := authority.app.CreateHandoff(ctx, projectID, workItemID, projectworkapp.CreateHandoffCommand{
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
	})
	if err != nil {
		return projectwork.Handoff{}, mapProjectWorkApplicationErr(err)
	}
	return handoff, nil
}

func (authority projectWorkAuthority) UpdateHandoff(ctx context.Context, projectID, workItemID, handoffID string, cmd projectassistant.WorkHandoffUpdateCommand) (projectwork.Handoff, error) {
	handoff, err := authority.app.UpdateHandoff(ctx, projectID, workItemID, handoffID, projectworkapp.UpdateHandoffCommand{
		TargetAssignmentID: cmd.TargetAssignmentID,
		TargetRoleID:       cmd.TargetRoleID,
		Status:             cmd.Status,
	})
	if err != nil {
		return projectwork.Handoff{}, mapProjectWorkApplicationErr(err)
	}
	return handoff, nil
}

func mapProjectWorkApplicationErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, projectworkapp.ErrWorkItemCloseoutBlocked) {
		return fmt.Errorf("%w: %w", projectassistant.ErrConflict, err)
	}
	return err
}
