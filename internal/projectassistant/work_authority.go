package projectassistant

import (
	"context"
	"strings"

	"github.com/hecatehq/hecate/internal/projectwork"
)

type WorkAuthority interface {
	CreateRole(ctx context.Context, projectID string, cmd WorkRoleCommand) (projectwork.AgentRoleProfile, error)
	CreateWorkItem(ctx context.Context, projectID string, cmd WorkItemCommand) (projectwork.WorkItem, error)
	UpdateWorkItem(ctx context.Context, projectID, workItemID string, cmd WorkItemUpdateCommand) (projectwork.WorkItem, error)
	CreateAssignment(ctx context.Context, projectID, workItemID string, cmd WorkAssignmentCommand) (projectwork.Assignment, error)
}

type WorkRoleCommand struct {
	ID                  string
	Name                string
	Description         string
	Instructions        string
	DefaultDriverKind   string
	DefaultProvider     string
	DefaultModel        string
	DefaultAgentProfile string
	SkillIDs            []string
}

type WorkItemCommand struct {
	ID              string
	Title           string
	Brief           string
	Status          string
	Priority        string
	OwnerRoleID     string
	ReviewerRoleIDs []string
}

type WorkItemUpdateCommand struct {
	Title           *string
	Brief           *string
	Status          *string
	Priority        *string
	OwnerRoleID     *string
	ReviewerRoleIDs *[]string
}

type WorkAssignmentCommand struct {
	ID         string
	RoleID     string
	RootID     string
	DriverKind string
	Status     string
}

func workAuthorityForStores(stores Stores) WorkAuthority {
	if stores.WorkAuthority != nil {
		return stores.WorkAuthority
	}
	if stores.Work == nil {
		return nil
	}
	return storeWorkAuthority{store: stores.Work}
}

type storeWorkAuthority struct {
	store projectwork.Store
}

func (authority storeWorkAuthority) CreateRole(ctx context.Context, projectID string, cmd WorkRoleCommand) (projectwork.AgentRoleProfile, error) {
	return authority.store.CreateRole(ctx, projectwork.AgentRoleProfile{
		ID:                  cmd.ID,
		ProjectID:           projectID,
		Name:                cmd.Name,
		Description:         cmd.Description,
		Instructions:        cmd.Instructions,
		DefaultDriverKind:   cmd.DefaultDriverKind,
		DefaultProvider:     cmd.DefaultProvider,
		DefaultModel:        cmd.DefaultModel,
		DefaultAgentProfile: cmd.DefaultAgentProfile,
		SkillIDs:            append([]string(nil), cmd.SkillIDs...),
	})
}

func (authority storeWorkAuthority) CreateWorkItem(ctx context.Context, projectID string, cmd WorkItemCommand) (projectwork.WorkItem, error) {
	return authority.store.CreateWorkItem(ctx, projectwork.WorkItem{
		ID:              cmd.ID,
		ProjectID:       projectID,
		Title:           cmd.Title,
		Brief:           cmd.Brief,
		Status:          cmd.Status,
		Priority:        cmd.Priority,
		OwnerRoleID:     cmd.OwnerRoleID,
		ReviewerRoleIDs: append([]string(nil), cmd.ReviewerRoleIDs...),
	})
}

func (authority storeWorkAuthority) UpdateWorkItem(ctx context.Context, projectID, workItemID string, cmd WorkItemUpdateCommand) (projectwork.WorkItem, error) {
	return authority.store.UpdateWorkItem(ctx, projectID, workItemID, func(item *projectwork.WorkItem) {
		if cmd.Title != nil {
			item.Title = *cmd.Title
		}
		if cmd.Brief != nil {
			item.Brief = *cmd.Brief
		}
		if cmd.Status != nil {
			item.Status = *cmd.Status
		}
		if cmd.Priority != nil {
			item.Priority = *cmd.Priority
		}
		if cmd.OwnerRoleID != nil {
			item.OwnerRoleID = *cmd.OwnerRoleID
		}
		if cmd.ReviewerRoleIDs != nil {
			item.ReviewerRoleIDs = append([]string(nil), *cmd.ReviewerRoleIDs...)
		}
	})
}

func (authority storeWorkAuthority) CreateAssignment(ctx context.Context, projectID, workItemID string, cmd WorkAssignmentCommand) (projectwork.Assignment, error) {
	driverKind := strings.TrimSpace(cmd.DriverKind)
	if driverKind == "" {
		if roles, err := authority.store.ListRoles(ctx, projectID); err != nil {
			return projectwork.Assignment{}, err
		} else {
			for _, role := range roles {
				if role.ID == strings.TrimSpace(cmd.RoleID) {
					driverKind = role.DefaultDriverKind
					break
				}
			}
		}
	}
	return authority.store.CreateAssignment(ctx, projectwork.Assignment{
		ID:         cmd.ID,
		ProjectID:  projectID,
		WorkItemID: workItemID,
		RoleID:     cmd.RoleID,
		RootID:     cmd.RootID,
		DriverKind: driverKind,
		Status:     cmd.Status,
	})
}
