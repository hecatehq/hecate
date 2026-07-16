package projectassistantapp

import (
	"context"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projectassistant"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/projectworkapp"
)

type Application struct {
	service *projectassistant.Service
}

type Options struct {
	Projects                 projects.Store
	ProjectAuthority         projectassistant.ProjectAuthority
	Chats                    chat.Store
	Work                     projectwork.Store
	WorkAuthority            projectassistant.WorkAuthority
	WorkApplication          *projectworkapp.Application
	ProjectSkills            projectskills.Store
	Memory                   memory.Store
	MemoryCandidates         memory.CandidateStore
	MemoryCandidateAuthority projectassistant.MemoryCandidateAuthority
	Proposals                projectassistant.ProposalStore
	LLM                      projectassistant.LLMClient
	IDGenerator              projectassistant.IDGenerator
}

type ContextCommand struct {
	ProjectID  string
	WorkItemID string
	Request    string
	RoleID     string
	DriverKind string
}

type DraftCommand struct {
	ProjectID        string
	WorkItemID       string
	Request          string
	RoleID           string
	DriverKind       string
	DraftMode        string
	ReviewArtifactID string
	Provider         string
	Model            string
	RequestID        string
	TraceID          string
}

type ProposeCommand struct {
	ID        string
	ProjectID string
	Title     string
	Summary   string
	Actions   []projectassistant.Action
	TraceID   string
}

type ApplyCommand struct {
	Proposal projectassistant.Proposal
	Confirm  bool
}

func New(options Options) *Application {
	var workAuthority projectassistant.WorkAuthority
	if options.WorkAuthority != nil {
		workAuthority = options.WorkAuthority
	} else if options.WorkApplication != nil {
		workAuthority = projectWorkAuthority{app: options.WorkApplication}
	}
	return &Application{
		service: projectassistant.NewService(projectassistant.Stores{
			Projects:                 options.Projects,
			ProjectAuthority:         options.ProjectAuthority,
			Chats:                    options.Chats,
			Work:                     options.Work,
			WorkAuthority:            workAuthority,
			ProjectSkills:            options.ProjectSkills,
			Memory:                   options.Memory,
			MemoryCandidates:         options.MemoryCandidates,
			MemoryCandidateAuthority: options.MemoryCandidateAuthority,
			Proposals:                options.Proposals,
			LLM:                      options.LLM,
		}, options.IDGenerator),
	}
}

func (app *Application) Context(ctx context.Context, command ContextCommand) (projectassistant.DraftContext, error) {
	if app == nil || app.service == nil {
		return projectassistant.DraftContext{}, projectassistant.ErrStoreNotConfigured
	}
	return app.service.Context(ctx, projectassistant.ContextInput{
		ProjectID:  command.ProjectID,
		WorkItemID: command.WorkItemID,
		Request:    command.Request,
		RoleID:     command.RoleID,
		DriverKind: command.DriverKind,
	})
}

func (app *Application) Draft(ctx context.Context, command DraftCommand) (projectassistant.Proposal, error) {
	if app == nil || app.service == nil {
		return projectassistant.Proposal{}, projectassistant.ErrStoreNotConfigured
	}
	return app.service.Draft(ctx, projectassistant.DraftInput{
		ProjectID:        command.ProjectID,
		WorkItemID:       command.WorkItemID,
		Request:          command.Request,
		RoleID:           command.RoleID,
		DriverKind:       command.DriverKind,
		DraftMode:        command.DraftMode,
		ReviewArtifactID: command.ReviewArtifactID,
		Provider:         command.Provider,
		Model:            command.Model,
		RequestID:        command.RequestID,
		TraceID:          command.TraceID,
	})
}

func (app *Application) Propose(ctx context.Context, command ProposeCommand) (projectassistant.Proposal, error) {
	if app == nil || app.service == nil {
		return projectassistant.Proposal{}, projectassistant.ErrStoreNotConfigured
	}
	return app.service.Propose(ctx, projectassistant.ProposalInput{
		ID:        command.ID,
		ProjectID: command.ProjectID,
		Source:    projectassistant.ProposalSourceAPI,
		Title:     command.Title,
		Summary:   command.Summary,
		Actions:   command.Actions,
		TraceID:   command.TraceID,
	})
}

func (app *Application) PrepareProposal(command ProposeCommand) (projectassistant.Proposal, error) {
	if app == nil || app.service == nil {
		return projectassistant.Proposal{}, projectassistant.ErrStoreNotConfigured
	}
	return app.service.PrepareProposal(projectassistant.ProposalInput{
		ID:        command.ID,
		ProjectID: command.ProjectID,
		Source:    projectassistant.ProposalSourceAPI,
		Title:     command.Title,
		Summary:   command.Summary,
		Actions:   command.Actions,
		TraceID:   command.TraceID,
	})
}

func (app *Application) ProposePrepared(ctx context.Context, proposal projectassistant.Proposal, projectID string) (projectassistant.Proposal, error) {
	if app == nil || app.service == nil {
		return projectassistant.Proposal{}, projectassistant.ErrStoreNotConfigured
	}
	return app.service.ProposePrepared(ctx, proposal, projectID, projectassistant.ProposalSourceAPI, "")
}

func (app *Application) CanonicalizeProposal(proposal projectassistant.Proposal) (projectassistant.Proposal, error) {
	if app == nil || app.service == nil {
		return projectassistant.Proposal{}, projectassistant.ErrStoreNotConfigured
	}
	return app.service.CanonicalizeProposal(proposal)
}

func (app *Application) Apply(ctx context.Context, command ApplyCommand) (projectassistant.ApplyResult, error) {
	if app == nil || app.service == nil {
		return projectassistant.ApplyResult{}, projectassistant.ErrStoreNotConfigured
	}
	return app.service.Apply(ctx, command.Proposal, command.Confirm)
}

func (app *Application) Proposal(ctx context.Context, id string) (projectassistant.ProposalRecord, bool, error) {
	if app == nil || app.service == nil {
		return projectassistant.ProposalRecord{}, false, projectassistant.ErrStoreNotConfigured
	}
	return app.service.Proposal(ctx, id)
}
