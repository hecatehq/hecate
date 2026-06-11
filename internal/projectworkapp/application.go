package projectworkapp

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/projectwork"
)

var ErrStoreNotConfigured = errors.New("project work store is not configured")

type Application struct {
	store projectwork.Store
	idgen func(string) string
}

type Options struct {
	Store       projectwork.Store
	IDGenerator func(string) string
}

type CreateRoleCommand struct {
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

type UpdateRoleCommand struct {
	Name                *string
	Description         *string
	Instructions        *string
	DefaultDriverKind   *string
	DefaultProvider     *string
	DefaultModel        *string
	DefaultAgentProfile *string
	SkillIDs            []string
}

type CreateWorkItemCommand struct {
	ID              string
	Title           string
	Brief           string
	Status          string
	Priority        string
	OwnerRoleID     string
	ReviewerRoleIDs []string
}

type UpdateWorkItemCommand struct {
	Title           *string
	Brief           *string
	Status          *string
	Priority        *string
	OwnerRoleID     *string
	ReviewerRoleIDs *[]string
}

type CreateAssignmentCommand struct {
	ID                string
	RoleID            string
	DriverKind        string
	Status            string
	TaskID            string
	RunID             string
	ChatSessionID     string
	MessageID         string
	ContextSnapshotID string
	StartedAt         time.Time
	CompletedAt       time.Time
}

type UpdateAssignmentCommand struct {
	RoleID            *string
	DriverKind        *string
	Status            *string
	TaskID            *string
	RunID             *string
	ChatSessionID     *string
	MessageID         *string
	ContextSnapshotID *string
	StartedAt         *time.Time
	CompletedAt       *time.Time
}

func New(opts Options) *Application {
	app := &Application{
		store: opts.Store,
		idgen: opts.IDGenerator,
	}
	if app.idgen == nil {
		app.idgen = func(prefix string) string { return strings.TrimSpace(prefix) }
	}
	return app
}

func (app *Application) CreateRole(ctx context.Context, projectID string, cmd CreateRoleCommand) (projectwork.AgentRoleProfile, error) {
	if app == nil || app.store == nil {
		return projectwork.AgentRoleProfile{}, ErrStoreNotConfigured
	}
	id := strings.TrimSpace(cmd.ID)
	if id == "" {
		id = app.idgen("role")
	}
	return app.store.CreateRole(ctx, projectwork.AgentRoleProfile{
		ID:                  id,
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

func (app *Application) UpdateRole(ctx context.Context, projectID, roleID string, cmd UpdateRoleCommand) (projectwork.AgentRoleProfile, error) {
	if app == nil || app.store == nil {
		return projectwork.AgentRoleProfile{}, ErrStoreNotConfigured
	}
	return app.store.UpdateRole(ctx, projectID, roleID, func(item *projectwork.AgentRoleProfile) {
		if cmd.Name != nil {
			item.Name = *cmd.Name
		}
		if cmd.Description != nil {
			item.Description = *cmd.Description
		}
		if cmd.Instructions != nil {
			item.Instructions = *cmd.Instructions
		}
		if cmd.DefaultDriverKind != nil {
			item.DefaultDriverKind = *cmd.DefaultDriverKind
		}
		if cmd.DefaultProvider != nil {
			item.DefaultProvider = *cmd.DefaultProvider
		}
		if cmd.DefaultModel != nil {
			item.DefaultModel = *cmd.DefaultModel
		}
		if cmd.DefaultAgentProfile != nil {
			item.DefaultAgentProfile = *cmd.DefaultAgentProfile
		}
		if cmd.SkillIDs != nil {
			item.SkillIDs = append([]string(nil), cmd.SkillIDs...)
		}
	})
}

func (app *Application) DeleteRole(ctx context.Context, projectID, roleID string) error {
	if app == nil || app.store == nil {
		return ErrStoreNotConfigured
	}
	return app.store.DeleteRole(ctx, projectID, roleID)
}

func (app *Application) CreateWorkItem(ctx context.Context, projectID string, cmd CreateWorkItemCommand) (projectwork.WorkItem, error) {
	if app == nil || app.store == nil {
		return projectwork.WorkItem{}, ErrStoreNotConfigured
	}
	id := strings.TrimSpace(cmd.ID)
	if id == "" {
		id = app.idgen("work")
	}
	return app.store.CreateWorkItem(ctx, projectwork.WorkItem{
		ID:              id,
		ProjectID:       projectID,
		Title:           cmd.Title,
		Brief:           cmd.Brief,
		Status:          cmd.Status,
		Priority:        cmd.Priority,
		OwnerRoleID:     cmd.OwnerRoleID,
		ReviewerRoleIDs: append([]string(nil), cmd.ReviewerRoleIDs...),
	})
}

func (app *Application) UpdateWorkItem(ctx context.Context, projectID, workItemID string, cmd UpdateWorkItemCommand) (projectwork.WorkItem, error) {
	if app == nil || app.store == nil {
		return projectwork.WorkItem{}, ErrStoreNotConfigured
	}
	return app.store.UpdateWorkItem(ctx, projectID, workItemID, func(item *projectwork.WorkItem) {
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

func (app *Application) DeleteWorkItem(ctx context.Context, projectID, workItemID string) error {
	if app == nil || app.store == nil {
		return ErrStoreNotConfigured
	}
	return app.store.DeleteWorkItem(ctx, projectID, workItemID)
}

func (app *Application) CreateAssignment(ctx context.Context, projectID, workItemID string, cmd CreateAssignmentCommand) (projectwork.Assignment, error) {
	if app == nil || app.store == nil {
		return projectwork.Assignment{}, ErrStoreNotConfigured
	}
	id := strings.TrimSpace(cmd.ID)
	if id == "" {
		id = app.idgen("asgn")
	}
	driverKind := strings.TrimSpace(cmd.DriverKind)
	if driverKind == "" {
		if role, ok, err := app.loadRole(ctx, projectID, cmd.RoleID); err != nil {
			return projectwork.Assignment{}, err
		} else if ok {
			driverKind = role.DefaultDriverKind
		}
	}
	return app.store.CreateAssignment(ctx, projectwork.Assignment{
		ID:                id,
		ProjectID:         projectID,
		WorkItemID:        workItemID,
		RoleID:            cmd.RoleID,
		DriverKind:        driverKind,
		Status:            cmd.Status,
		TaskID:            cmd.TaskID,
		RunID:             cmd.RunID,
		ChatSessionID:     cmd.ChatSessionID,
		MessageID:         cmd.MessageID,
		ContextSnapshotID: cmd.ContextSnapshotID,
		StartedAt:         cmd.StartedAt,
		CompletedAt:       cmd.CompletedAt,
	})
}

func (app *Application) UpdateAssignment(ctx context.Context, projectID, assignmentID string, cmd UpdateAssignmentCommand) (projectwork.Assignment, error) {
	if app == nil || app.store == nil {
		return projectwork.Assignment{}, ErrStoreNotConfigured
	}
	return app.store.UpdateAssignment(ctx, projectID, assignmentID, func(item *projectwork.Assignment) {
		if cmd.RoleID != nil {
			item.RoleID = *cmd.RoleID
		}
		if cmd.DriverKind != nil {
			item.DriverKind = *cmd.DriverKind
		}
		if cmd.Status != nil {
			item.Status = *cmd.Status
		}
		if cmd.TaskID != nil {
			item.TaskID = *cmd.TaskID
		}
		if cmd.RunID != nil {
			item.RunID = *cmd.RunID
		}
		if cmd.ChatSessionID != nil {
			item.ChatSessionID = *cmd.ChatSessionID
		}
		if cmd.MessageID != nil {
			item.MessageID = *cmd.MessageID
		}
		if cmd.ContextSnapshotID != nil {
			item.ContextSnapshotID = *cmd.ContextSnapshotID
		}
		if cmd.StartedAt != nil {
			item.StartedAt = *cmd.StartedAt
		}
		if cmd.CompletedAt != nil {
			item.CompletedAt = *cmd.CompletedAt
		}
	})
}

func (app *Application) DeleteAssignment(ctx context.Context, projectID, workItemID, assignmentID string) error {
	if app == nil || app.store == nil {
		return ErrStoreNotConfigured
	}
	return app.store.DeleteAssignment(ctx, projectID, workItemID, assignmentID)
}

func (app *Application) loadRole(ctx context.Context, projectID, roleID string) (projectwork.AgentRoleProfile, bool, error) {
	roles, err := app.store.ListRoles(ctx, projectID)
	if err != nil {
		return projectwork.AgentRoleProfile{}, false, err
	}
	roleID = strings.TrimSpace(roleID)
	for _, role := range roles {
		if role.ID == roleID {
			return role, true, nil
		}
	}
	return projectwork.AgentRoleProfile{}, false, nil
}
