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

func mapProjectWorkApplicationErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, projectworkapp.ErrWorkItemCloseoutBlocked) {
		return fmt.Errorf("%w: %w", projectassistant.ErrConflict, err)
	}
	return err
}
