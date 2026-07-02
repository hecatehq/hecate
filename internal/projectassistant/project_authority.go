package projectassistant

import (
	"context"

	"github.com/hecatehq/hecate/internal/projects"
)

type ProjectAuthority interface {
	GetProject(ctx context.Context, projectID string) (projects.Project, bool, error)
	CreateProject(ctx context.Context, project projects.Project) (projects.Project, error)
	UpdateProject(ctx context.Context, projectID string, cmd ProjectUpdateCommand) (projects.Project, error)
	AttachProjectRoot(ctx context.Context, projectID string, root projects.Root) (projects.Project, error)
	RemoveProjectRoot(ctx context.Context, projectID, rootID string) (projects.Project, error)
	SetProjectDefaults(ctx context.Context, projectID string, cmd ProjectDefaultsCommand) (projects.Project, error)
}

type ProjectUpdateCommand struct {
	Name        *string
	Description *string
}

type ProjectDefaultsCommand struct {
	DefaultRootID            *string
	DefaultProvider          *string
	DefaultModel             *string
	DefaultAgentProfile      *string
	DefaultToolsEnabled      *bool
	DefaultWorkspaceMode     *string
	DefaultSystemPrompt      *string
	DefaultCompactToolOutput *bool
}

func projectAuthorityForStores(stores Stores) ProjectAuthority {
	if stores.ProjectAuthority != nil {
		return stores.ProjectAuthority
	}
	if stores.Projects == nil {
		return nil
	}
	return storeProjectAuthority{store: stores.Projects}
}

type storeProjectAuthority struct {
	store projects.Store
}

func (authority storeProjectAuthority) GetProject(ctx context.Context, projectID string) (projects.Project, bool, error) {
	return authority.store.Get(ctx, projectID)
}

func (authority storeProjectAuthority) CreateProject(ctx context.Context, project projects.Project) (projects.Project, error) {
	return authority.store.Create(ctx, project)
}

func (authority storeProjectAuthority) UpdateProject(ctx context.Context, projectID string, cmd ProjectUpdateCommand) (projects.Project, error) {
	return authority.store.Update(ctx, projectID, func(project *projects.Project) {
		if cmd.Name != nil {
			project.Name = *cmd.Name
		}
		if cmd.Description != nil {
			project.Description = *cmd.Description
		}
	})
}

func (authority storeProjectAuthority) AttachProjectRoot(ctx context.Context, projectID string, root projects.Root) (projects.Project, error) {
	return authority.store.Update(ctx, projectID, func(project *projects.Project) {
		project.Roots = append(project.Roots, root)
	})
}

func (authority storeProjectAuthority) RemoveProjectRoot(ctx context.Context, projectID, rootID string) (projects.Project, error) {
	return authority.store.Update(ctx, projectID, func(project *projects.Project) {
		roots := project.Roots[:0]
		for _, root := range project.Roots {
			if root.ID != rootID {
				roots = append(roots, root)
			}
		}
		project.Roots = roots
		if project.DefaultRootID == rootID {
			project.DefaultRootID = ""
		}
	})
}

func (authority storeProjectAuthority) SetProjectDefaults(ctx context.Context, projectID string, cmd ProjectDefaultsCommand) (projects.Project, error) {
	return authority.store.Update(ctx, projectID, func(project *projects.Project) {
		if cmd.DefaultRootID != nil {
			project.DefaultRootID = *cmd.DefaultRootID
		}
		if cmd.DefaultProvider != nil {
			project.DefaultProvider = *cmd.DefaultProvider
		}
		if cmd.DefaultModel != nil {
			project.DefaultModel = *cmd.DefaultModel
		}
		if cmd.DefaultAgentProfile != nil {
			project.DefaultAgentProfile = *cmd.DefaultAgentProfile
		}
		if cmd.DefaultToolsEnabled != nil {
			project.DefaultToolsEnabled = cmd.DefaultToolsEnabled
		}
		if cmd.DefaultWorkspaceMode != nil {
			project.DefaultWorkspaceMode = *cmd.DefaultWorkspaceMode
		}
		if cmd.DefaultSystemPrompt != nil {
			project.DefaultSystemPrompt = *cmd.DefaultSystemPrompt
		}
		if cmd.DefaultCompactToolOutput != nil {
			project.DefaultCompactToolOutput = cmd.DefaultCompactToolOutput
		}
	})
}
