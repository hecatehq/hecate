package api

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projectapp"
	"github.com/hecatehq/hecate/internal/projectassistant"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/projectworkapp"
)

type projectAssistantProjectAuthority struct {
	handler *Handler
}

type projectAssistantWorkAuthority struct {
	handler *Handler
}

func (h *Handler) projectAssistantProjectAuthorityForApplication() projectassistant.ProjectAuthority {
	if h == nil {
		return nil
	}
	return projectAssistantProjectAuthority{handler: h}
}

func (h *Handler) projectAssistantWorkAuthorityForApplication() projectassistant.WorkAuthority {
	if h == nil {
		return nil
	}
	return projectAssistantWorkAuthority{handler: h}
}

func (h *Handler) projectAssistantMemoryCandidateAuthorityForApplication() projectassistant.MemoryCandidateAuthority {
	if h == nil {
		return nil
	}
	return projectAssistantMemoryCandidateAuthority{handler: h}
}

func (authority projectAssistantProjectAuthority) CreateProject(ctx context.Context, project projects.Project) (projects.Project, error) {
	h := authority.handler
	if h == nil || h.projects == nil {
		return projects.Project{}, projectassistant.ErrStoreNotConfigured
	}
	if h.projectIdentityWritesUseCairnlineAuthority() {
		created, err := h.createProjectWithCairnlineAuthority(ctx, project)
		return created, projectAssistantApplyProjectError(err)
	}
	created, err := h.projects.Create(ctx, project)
	return created, projectAssistantApplyProjectError(err)
}

func (authority projectAssistantProjectAuthority) UpdateProject(ctx context.Context, projectID string, cmd projectassistant.ProjectUpdateCommand) (projects.Project, error) {
	h := authority.handler
	if h == nil || h.projects == nil {
		return projects.Project{}, projectassistant.ErrStoreNotConfigured
	}
	req := updateProjectRequest{Name: cmd.Name, Description: cmd.Description}
	if h.projectMetadataDefaultsWritesUseCairnlineAuthority() && projectUpdateCanUseCairnlineMetadataDefaultsAuthority(req) {
		updated, err := h.updateProjectMetadataDefaultsWithCairnlineAuthority(ctx, projectID, req)
		return updated, projectAssistantApplyProjectError(err)
	}
	updated, err := h.projects.Update(ctx, projectID, func(project *projects.Project) {
		if cmd.Name != nil {
			project.Name = strings.TrimSpace(*cmd.Name)
		}
		if cmd.Description != nil {
			project.Description = strings.TrimSpace(*cmd.Description)
		}
	})
	return updated, projectAssistantApplyProjectError(err)
}

func (authority projectAssistantProjectAuthority) AttachProjectRoot(ctx context.Context, projectID string, root projects.Root) (projects.Project, error) {
	h := authority.handler
	if h == nil || h.projects == nil {
		return projects.Project{}, projectassistant.ErrStoreNotConfigured
	}
	if h.projectRootWritesUseCairnlineAuthority() {
		updated, _, err := h.createProjectRootWithCairnlineAuthority(ctx, projectID, root)
		return updated, projectAssistantApplyProjectError(err)
	}
	updated, err := h.projects.Update(ctx, projectID, func(project *projects.Project) {
		project.Roots = append(project.Roots, root)
	})
	return updated, projectAssistantApplyProjectError(err)
}

func (authority projectAssistantProjectAuthority) RemoveProjectRoot(ctx context.Context, projectID, rootID string) (projects.Project, error) {
	h := authority.handler
	if h == nil || h.projects == nil {
		return projects.Project{}, projectassistant.ErrStoreNotConfigured
	}
	if h.projectRootWritesUseCairnlineAuthority() {
		updated, _, err := h.deleteProjectRootWithCairnlineAuthority(ctx, projectID, rootID)
		return updated, projectAssistantApplyProjectError(err)
	}
	updated, err := h.projects.Update(ctx, projectID, func(project *projects.Project) {
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
	return updated, projectAssistantApplyProjectError(err)
}

func (authority projectAssistantProjectAuthority) SetProjectDefaults(ctx context.Context, projectID string, cmd projectassistant.ProjectDefaultsCommand) (projects.Project, error) {
	h := authority.handler
	if h == nil || h.projects == nil {
		return projects.Project{}, projectassistant.ErrStoreNotConfigured
	}
	req := updateProjectRequest{
		DefaultRootID:            cmd.DefaultRootID,
		DefaultProvider:          cmd.DefaultProvider,
		DefaultModel:             cmd.DefaultModel,
		DefaultAgentProfile:      cmd.DefaultAgentProfile,
		DefaultToolsEnabled:      cmd.DefaultToolsEnabled,
		DefaultWorkspaceMode:     cmd.DefaultWorkspaceMode,
		DefaultSystemPrompt:      cmd.DefaultSystemPrompt,
		DefaultCompactToolOutput: cmd.DefaultCompactToolOutput,
	}
	if h.projectMetadataDefaultsWritesUseCairnlineAuthority() && projectUpdateCanUseCairnlineMetadataDefaultsAuthority(req) {
		updated, err := h.updateProjectMetadataDefaultsWithCairnlineAuthority(ctx, projectID, req)
		return updated, projectAssistantApplyProjectError(err)
	}
	updated, err := h.projects.Update(ctx, projectID, func(project *projects.Project) {
		if cmd.DefaultRootID != nil {
			project.DefaultRootID = strings.TrimSpace(*cmd.DefaultRootID)
		}
		if cmd.DefaultProvider != nil {
			project.DefaultProvider = strings.TrimSpace(*cmd.DefaultProvider)
		}
		if cmd.DefaultModel != nil {
			project.DefaultModel = strings.TrimSpace(*cmd.DefaultModel)
		}
		if cmd.DefaultAgentProfile != nil {
			project.DefaultAgentProfile = strings.TrimSpace(*cmd.DefaultAgentProfile)
		}
		if cmd.DefaultToolsEnabled != nil {
			project.DefaultToolsEnabled = cloneBool(cmd.DefaultToolsEnabled)
		}
		if cmd.DefaultWorkspaceMode != nil {
			project.DefaultWorkspaceMode = strings.TrimSpace(*cmd.DefaultWorkspaceMode)
		}
		if cmd.DefaultSystemPrompt != nil {
			project.DefaultSystemPrompt = strings.TrimSpace(*cmd.DefaultSystemPrompt)
		}
		if cmd.DefaultCompactToolOutput != nil {
			project.DefaultCompactToolOutput = cloneBool(cmd.DefaultCompactToolOutput)
		}
	})
	return updated, projectAssistantApplyProjectError(err)
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

func projectAssistantApplyProjectError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, projectapp.ErrProjectNotFound),
		errors.Is(err, projectapp.ErrProjectRootNotFound),
		errors.Is(err, cairnline.ErrNotFound):
		return fmt.Errorf("%w: %w", projects.ErrNotFound, err)
	case errors.Is(err, projectapp.ErrProjectRootConflict),
		errors.Is(err, projectapp.ErrProjectContextSourceConflict),
		errors.Is(err, cairnline.ErrDuplicate),
		errors.Is(err, cairnline.ErrConflict):
		return fmt.Errorf("%w: %w", projects.ErrAlreadyExists, err)
	case errors.Is(err, cairnline.ErrInvalid):
		return fmt.Errorf("%w: %w", projects.ErrInvalid, err)
	default:
		return err
	}
}

type projectAssistantMemoryCandidateAuthority struct {
	handler *Handler
}

func (authority projectAssistantMemoryCandidateAuthority) CreateMemoryCandidate(ctx context.Context, projectID string, cmd projectassistant.MemoryCandidateCommand) (memory.Candidate, error) {
	h := authority.handler
	if h == nil {
		return memory.Candidate{}, projectassistant.ErrStoreNotConfigured
	}
	candidate := memory.Candidate{
		ID:                  cmd.ID,
		ProjectID:           projectID,
		Title:               cmd.Title,
		Body:                cmd.Body,
		SuggestedKind:       cmd.SuggestedKind,
		SuggestedTrustLabel: cmd.SuggestedTrustLabel,
		SuggestedSourceKind: cmd.SuggestedSourceKind,
		SuggestedSourceID:   cmd.SuggestedSourceID,
		SourceRefs:          append([]memory.CandidateSourceRef(nil), cmd.SourceRefs...),
		Status:              memory.CandidateStatusPending,
	}
	if h.projectMemoryCandidatesWriteUseCairnlineAuthority() {
		created, err := h.createProjectMemoryCandidateWithCairnlineAuthority(ctx, projectID, candidate)
		if err != nil {
			return memory.Candidate{}, projectAssistantApplyMemoryCandidateError(err)
		}
		h.shadowProjectMemoryCandidateToHecate(ctx, "project_assistant_memory_candidate_authority_create_shadow", created)
		return created, nil
	}
	if h.memoryCandidates == nil {
		return memory.Candidate{}, projectassistant.ErrStoreNotConfigured
	}
	created, err := h.memoryCandidates.CreateCandidate(ctx, candidate)
	return created, projectAssistantApplyMemoryCandidateError(err)
}

func projectAssistantApplyMemoryCandidateError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, cairnline.ErrNotFound):
		return fmt.Errorf("%w: %w", memory.ErrNotFound, err)
	case errors.Is(err, cairnline.ErrInvalid):
		return fmt.Errorf("%w: %w", memory.ErrInvalid, err)
	case errors.Is(err, cairnline.ErrDuplicate):
		return fmt.Errorf("%w: %w", memory.ErrAlreadyExists, err)
	case errors.Is(err, cairnline.ErrConflict):
		return fmt.Errorf("%w: %w", memory.ErrConflict, err)
	default:
		return err
	}
}
