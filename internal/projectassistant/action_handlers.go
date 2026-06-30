package projectassistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectwork"
)

func (s *Service) applyCreateProject(ctx context.Context, action Action) (ActionResult, error) {
	if s.projects == nil {
		return ActionResult{}, ErrStoreNotConfigured
	}
	var patch projectPatch
	if err := decodePatch(action, &patch); err != nil {
		return ActionResult{}, err
	}
	id := strings.TrimSpace(patch.ID)
	if id == "" {
		id = s.idgen("proj")
	} else if _, ok, err := s.projects.Get(ctx, id); err != nil {
		return ActionResult{}, err
	} else if ok {
		return ActionResult{}, fmt.Errorf("%w: project %q already exists", ErrConflict, id)
	}
	roots, err := rootsForProjectPatch(patch, s.idgen)
	if err != nil {
		return ActionResult{}, err
	}
	project := projects.Project{
		ID:                       id,
		Name:                     patch.Name,
		Description:              patch.Description,
		Roots:                    roots,
		DefaultProvider:          patch.DefaultProvider,
		DefaultModel:             patch.DefaultModel,
		DefaultAgentProfile:      patch.DefaultAgentProfile,
		DefaultToolsEnabled:      patch.DefaultToolsEnabled,
		DefaultWorkspaceMode:     patch.DefaultWorkspaceMode,
		DefaultSystemPrompt:      patch.DefaultSystemPrompt,
		DefaultCompactToolOutput: patch.DefaultCompactToolOutput,
	}
	created, err := s.projects.Create(ctx, project)
	if err != nil {
		return ActionResult{}, mapProjectErr(err)
	}
	return ActionResult{Kind: ActionCreateProject, ID: created.ID, Data: map[string]string{"project_id": created.ID}}, nil
}

func (s *Service) applyUpdateProject(ctx context.Context, action Action) (ActionResult, error) {
	projectID := targetValue(action, "project_id")
	if projectID == "" {
		return ActionResult{}, fmt.Errorf("%w: target.project_id is required", ErrInvalid)
	}
	if _, err := s.requireProject(ctx, projectID); err != nil {
		return ActionResult{}, err
	}
	var patch updateProjectPatch
	if err := decodePatch(action, &patch); err != nil {
		return ActionResult{}, err
	}
	updated, err := s.projects.Update(ctx, projectID, func(project *projects.Project) {
		if patch.Name != nil {
			project.Name = *patch.Name
		}
		if patch.Description != nil {
			project.Description = *patch.Description
		}
	})
	if err != nil {
		return ActionResult{}, mapProjectErr(err)
	}
	return ActionResult{Kind: ActionUpdateProject, ID: updated.ID, Data: map[string]string{"project_id": updated.ID}}, nil
}

func (s *Service) applyAttachProjectRoot(ctx context.Context, action Action) (ActionResult, error) {
	projectID := targetValue(action, "project_id")
	if projectID == "" {
		return ActionResult{}, fmt.Errorf("%w: target.project_id is required", ErrInvalid)
	}
	if _, err := s.requireProject(ctx, projectID); err != nil {
		return ActionResult{}, err
	}
	var patch rootPatch
	if err := decodePatch(action, &patch); err != nil {
		return ActionResult{}, err
	}
	root := rootFromPatch(patch, s.idgen)
	updated, err := s.projects.Update(ctx, projectID, func(project *projects.Project) {
		project.Roots = append(project.Roots, root)
	})
	if err != nil {
		return ActionResult{}, mapProjectErr(err)
	}
	return ActionResult{Kind: ActionAttachProjectRoot, ID: root.ID, Data: map[string]string{"project_id": updated.ID, "root_id": root.ID}}, nil
}

func (s *Service) applyRemoveProjectRoot(ctx context.Context, action Action) (ActionResult, error) {
	projectID := targetValue(action, "project_id")
	rootID := targetValue(action, "root_id")
	if projectID == "" || rootID == "" {
		return ActionResult{}, fmt.Errorf("%w: target.project_id and target.root_id are required", ErrInvalid)
	}
	project, err := s.requireProject(ctx, projectID)
	if err != nil {
		return ActionResult{}, err
	}
	if !projectHasRoot(project, rootID) {
		return ActionResult{}, fmt.Errorf("%w: root %q", ErrNotFound, rootID)
	}
	updated, err := s.projects.Update(ctx, projectID, func(project *projects.Project) {
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
	if err != nil {
		return ActionResult{}, mapProjectErr(err)
	}
	return ActionResult{Kind: ActionRemoveProjectRoot, ID: rootID, Data: map[string]string{"project_id": updated.ID, "root_id": rootID}}, nil
}

func (s *Service) applySetProjectDefaults(ctx context.Context, action Action) (ActionResult, error) {
	projectID := targetValue(action, "project_id")
	if projectID == "" {
		return ActionResult{}, fmt.Errorf("%w: target.project_id is required", ErrInvalid)
	}
	project, err := s.requireProject(ctx, projectID)
	if err != nil {
		return ActionResult{}, err
	}
	var patch defaultsPatch
	if err := decodePatch(action, &patch); err != nil {
		return ActionResult{}, err
	}
	if patch.DefaultRootID != nil && *patch.DefaultRootID != "" && !projectHasRoot(project, *patch.DefaultRootID) {
		return ActionResult{}, fmt.Errorf("%w: root %q", ErrNotFound, *patch.DefaultRootID)
	}
	updated, err := s.projects.Update(ctx, projectID, func(project *projects.Project) {
		if patch.DefaultRootID != nil {
			project.DefaultRootID = *patch.DefaultRootID
		}
		if patch.DefaultProvider != nil {
			project.DefaultProvider = *patch.DefaultProvider
		}
		if patch.DefaultModel != nil {
			project.DefaultModel = *patch.DefaultModel
		}
		if patch.DefaultAgentProfile != nil {
			project.DefaultAgentProfile = *patch.DefaultAgentProfile
		}
		if patch.DefaultToolsEnabled != nil {
			project.DefaultToolsEnabled = patch.DefaultToolsEnabled
		}
		if patch.DefaultWorkspaceMode != nil {
			project.DefaultWorkspaceMode = *patch.DefaultWorkspaceMode
		}
		if patch.DefaultSystemPrompt != nil {
			project.DefaultSystemPrompt = *patch.DefaultSystemPrompt
		}
		if patch.DefaultCompactToolOutput != nil {
			project.DefaultCompactToolOutput = patch.DefaultCompactToolOutput
		}
	})
	if err != nil {
		return ActionResult{}, mapProjectErr(err)
	}
	return ActionResult{Kind: ActionSetProjectDefaults, ID: updated.ID, Data: map[string]string{"project_id": updated.ID}}, nil
}

func (s *Service) applyMoveChatSession(ctx context.Context, action Action) (ActionResult, error) {
	if s.chats == nil {
		return ActionResult{}, ErrStoreNotConfigured
	}
	sessionID := targetValue(action, "chat_session_id")
	if sessionID == "" {
		return ActionResult{}, fmt.Errorf("%w: target.chat_session_id is required", ErrInvalid)
	}
	var patch moveChatSessionPatch
	if err := decodePatch(action, &patch); err != nil {
		return ActionResult{}, err
	}
	projectID := strings.TrimSpace(patch.ProjectID)
	if projectID != "" {
		if _, err := s.requireProject(ctx, projectID); err != nil {
			return ActionResult{}, err
		}
	}
	if _, ok, err := s.chats.Get(ctx, sessionID); err != nil {
		return ActionResult{}, err
	} else if !ok {
		return ActionResult{}, fmt.Errorf("%w: chat session %q", ErrNotFound, sessionID)
	}
	updated, err := s.chats.UpdateSession(ctx, sessionID, func(session *chat.Session) {
		session.ProjectID = projectID
	})
	if err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Kind: ActionMoveChatSession, ID: updated.ID, Data: map[string]string{"chat_session_id": updated.ID, "project_id": updated.ProjectID}}, nil
}

func (s *Service) applyCreateRole(ctx context.Context, action Action) (ActionResult, error) {
	if s.workAuthority == nil {
		return ActionResult{}, ErrStoreNotConfigured
	}
	var patch rolePatch
	if err := decodePatch(action, &patch); err != nil {
		return ActionResult{}, err
	}
	projectID := patch.ProjectID
	if projectID == "" {
		projectID = targetValue(action, "project_id")
	}
	if _, err := s.requireProject(ctx, projectID); err != nil {
		return ActionResult{}, err
	}
	id := strings.TrimSpace(patch.ID)
	if id == "" {
		id = s.idgen("role")
	}
	role, err := s.workAuthority.CreateRole(ctx, projectID, WorkRoleCommand{
		ID:                  id,
		Name:                patch.Name,
		Description:         patch.Description,
		Instructions:        patch.Instructions,
		DefaultDriverKind:   patch.DefaultDriverKind,
		DefaultProvider:     patch.DefaultProvider,
		DefaultModel:        patch.DefaultModel,
		DefaultAgentProfile: patch.DefaultAgentProfile,
		SkillIDs:            append([]string(nil), patch.SkillIDs...),
	})
	if err != nil {
		return ActionResult{}, mapProjectWorkErr(err)
	}
	return ActionResult{Kind: ActionCreateRole, ID: role.ID, Data: map[string]string{"project_id": role.ProjectID, "role_id": role.ID}}, nil
}

func (s *Service) applyCreateWorkItem(ctx context.Context, action Action) (ActionResult, error) {
	if s.workAuthority == nil {
		return ActionResult{}, ErrStoreNotConfigured
	}
	var patch workItemPatch
	if err := decodePatch(action, &patch); err != nil {
		return ActionResult{}, err
	}
	projectID := patch.ProjectID
	if projectID == "" {
		projectID = targetValue(action, "project_id")
	}
	if _, err := s.requireProject(ctx, projectID); err != nil {
		return ActionResult{}, err
	}
	id := strings.TrimSpace(patch.ID)
	if id == "" {
		id = s.idgen("work")
	}
	item, err := s.workAuthority.CreateWorkItem(ctx, projectID, WorkItemCommand{
		ID:              id,
		Title:           patch.Title,
		Brief:           patch.Brief,
		Status:          patch.Status,
		Priority:        patch.Priority,
		OwnerRoleID:     patch.OwnerRoleID,
		ReviewerRoleIDs: append([]string(nil), patch.ReviewerRoleIDs...),
	})
	if err != nil {
		return ActionResult{}, mapProjectWorkErr(err)
	}
	return ActionResult{Kind: ActionCreateWorkItem, ID: item.ID, Data: map[string]string{"project_id": item.ProjectID, "work_item_id": item.ID}}, nil
}

func (s *Service) applyUpdateWorkItem(ctx context.Context, action Action) (ActionResult, error) {
	if s.workAuthority == nil {
		return ActionResult{}, ErrStoreNotConfigured
	}
	projectID := targetValue(action, "project_id")
	workItemID := targetValue(action, "work_item_id")
	if projectID == "" || workItemID == "" {
		return ActionResult{}, fmt.Errorf("%w: target.project_id and target.work_item_id are required", ErrInvalid)
	}
	if _, err := s.requireWorkItem(ctx, projectID, workItemID); err != nil {
		return ActionResult{}, err
	}
	var patch updateWorkItemPatch
	if err := decodePatch(action, &patch); err != nil {
		return ActionResult{}, err
	}
	update := WorkItemUpdateCommand{
		Title:       patch.Title,
		Brief:       patch.Brief,
		Status:      patch.Status,
		Priority:    patch.Priority,
		OwnerRoleID: patch.OwnerRoleID,
	}
	if patch.ReviewerRoleIDs != nil {
		reviewerRoleIDs := append([]string(nil), patch.ReviewerRoleIDs...)
		update.ReviewerRoleIDs = &reviewerRoleIDs
	}
	item, err := s.workAuthority.UpdateWorkItem(ctx, projectID, workItemID, update)
	if err != nil {
		return ActionResult{}, mapProjectWorkErr(err)
	}
	return ActionResult{Kind: ActionUpdateWorkItem, ID: item.ID, Data: map[string]string{"project_id": item.ProjectID, "work_item_id": item.ID}}, nil
}

func (s *Service) applyCreateAssignment(ctx context.Context, action Action) (ActionResult, error) {
	if s.workAuthority == nil {
		return ActionResult{}, ErrStoreNotConfigured
	}
	var patch assignmentPatch
	if err := decodePatch(action, &patch); err != nil {
		return ActionResult{}, err
	}
	projectID := patch.ProjectID
	if projectID == "" {
		projectID = targetValue(action, "project_id")
	}
	item, err := s.requireWorkItem(ctx, projectID, patch.WorkItemID)
	if err != nil {
		return ActionResult{}, err
	}
	projectID = item.ProjectID
	rootID := strings.TrimSpace(patch.RootID)
	if rootID != "" {
		project, err := s.requireProject(ctx, projectID)
		if err != nil {
			return ActionResult{}, err
		}
		if !projectHasRoot(project, rootID) {
			return ActionResult{}, fmt.Errorf("%w: root %q", ErrNotFound, rootID)
		}
	}
	id := strings.TrimSpace(patch.ID)
	if id == "" {
		id = s.idgen("asgn")
	}
	assignment, err := s.workAuthority.CreateAssignment(ctx, projectID, item.ID, WorkAssignmentCommand{
		ID:         id,
		RoleID:     patch.RoleID,
		RootID:     rootID,
		DriverKind: patch.DriverKind,
		Status:     patch.Status,
	})
	if err != nil {
		return ActionResult{}, mapProjectWorkErr(err)
	}
	return ActionResult{Kind: ActionCreateAssignment, ID: assignment.ID, Data: map[string]string{"project_id": assignment.ProjectID, "assignment_id": assignment.ID}}, nil
}

func (s *Service) applyCreateHandoff(ctx context.Context, action Action) (ActionResult, error) {
	if s.workAuthority == nil {
		return ActionResult{}, ErrStoreNotConfigured
	}
	var patch handoffPatch
	if err := decodePatch(action, &patch); err != nil {
		return ActionResult{}, err
	}
	projectID := patch.ProjectID
	if projectID == "" {
		projectID = targetValue(action, "project_id")
	}
	if _, err := s.requireWorkItem(ctx, projectID, patch.WorkItemID); err != nil {
		return ActionResult{}, err
	}
	id := strings.TrimSpace(patch.ID)
	if id == "" {
		id = s.idgen("handoff")
	}
	handoff, err := s.workAuthority.CreateHandoff(ctx, projectID, patch.WorkItemID, WorkHandoffCommand{
		ID:                    id,
		SourceAssignmentID:    patch.SourceAssignmentID,
		SourceRunID:           patch.SourceRunID,
		SourceChatSessionID:   patch.SourceChatSessionID,
		SourceMessageID:       patch.SourceMessageID,
		TargetRoleID:          patch.TargetRoleID,
		TargetAssignmentID:    patch.TargetAssignmentID,
		TargetWorkItemID:      patch.TargetWorkItemID,
		Title:                 patch.Title,
		Summary:               patch.Summary,
		RecommendedNextAction: patch.RecommendedNextAction,
		LinkedArtifactIDs:     append([]string(nil), patch.LinkedArtifactIDs...),
		LinkedMemoryIDs:       append([]string(nil), patch.LinkedMemoryIDs...),
		ContextRefs:           append([]string(nil), patch.ContextRefs...),
		Status:                patch.Status,
		ProvenanceKind:        patch.ProvenanceKind,
		TrustLabel:            patch.TrustLabel,
		CreatedByRoleID:       patch.CreatedByRoleID,
	})
	if err != nil {
		return ActionResult{}, mapProjectWorkErr(err)
	}
	return ActionResult{Kind: ActionCreateHandoff, ID: handoff.ID, Data: map[string]string{"project_id": handoff.ProjectID, "handoff_id": handoff.ID}}, nil
}

func (s *Service) applyUpdateHandoff(ctx context.Context, action Action) (ActionResult, error) {
	if s.workAuthority == nil {
		return ActionResult{}, ErrStoreNotConfigured
	}
	projectID := targetValue(action, "project_id")
	workItemID := targetValue(action, "work_item_id")
	handoffID := targetValue(action, "handoff_id")
	if projectID == "" || workItemID == "" || handoffID == "" {
		return ActionResult{}, fmt.Errorf("%w: target.project_id, target.work_item_id, and target.handoff_id are required", ErrInvalid)
	}
	if _, err := s.requireWorkItem(ctx, projectID, workItemID); err != nil {
		return ActionResult{}, err
	}
	var patch updateHandoffPatch
	if err := decodePatch(action, &patch); err != nil {
		return ActionResult{}, err
	}
	if patch.TargetAssignmentID == nil && patch.TargetRoleID == nil && patch.Status == nil {
		return ActionResult{}, fmt.Errorf("%w: update_handoff patch must set at least one mutable field", ErrInvalid)
	}
	handoff, err := s.workAuthority.UpdateHandoff(ctx, projectID, workItemID, handoffID, WorkHandoffUpdateCommand{
		TargetAssignmentID: patch.TargetAssignmentID,
		TargetRoleID:       patch.TargetRoleID,
		Status:             patch.Status,
	})
	if err != nil {
		return ActionResult{}, mapProjectWorkErr(err)
	}
	return ActionResult{
		Kind: ActionUpdateHandoff,
		ID:   handoff.ID,
		Data: map[string]string{
			"project_id":           handoff.ProjectID,
			"work_item_id":         handoff.WorkItemID,
			"handoff_id":           handoff.ID,
			"target_assignment_id": handoff.TargetAssignmentID,
		},
	}, nil
}

func (s *Service) applyCreateMemoryCandidate(ctx context.Context, action Action) (ActionResult, error) {
	if s.memoryCandidateAuthority == nil {
		return ActionResult{}, ErrStoreNotConfigured
	}
	var patch memoryCandidatePatch
	if err := decodePatch(action, &patch); err != nil {
		return ActionResult{}, err
	}
	projectID := patch.ProjectID
	if projectID == "" {
		projectID = targetValue(action, "project_id")
	}
	if _, err := s.requireProject(ctx, projectID); err != nil {
		return ActionResult{}, err
	}
	id := strings.TrimSpace(patch.ID)
	if id == "" {
		id = s.idgen("memcand")
	}
	candidate, err := s.memoryCandidateAuthority.CreateMemoryCandidate(ctx, projectID, MemoryCandidateCommand{
		ID:                  id,
		Title:               patch.Title,
		Body:                patch.Body,
		SuggestedKind:       patch.SuggestedKind,
		SuggestedTrustLabel: patch.SuggestedTrustLabel,
		SuggestedSourceKind: patch.SuggestedSourceKind,
		SuggestedSourceID:   patch.SuggestedSourceID,
		SourceRefs:          append([]memory.CandidateSourceRef(nil), patch.SourceRefs...),
	})
	if err != nil {
		return ActionResult{}, mapMemoryErr(err)
	}
	return ActionResult{Kind: ActionCreateMemoryCandidate, ID: candidate.ID, Data: map[string]string{"project_id": candidate.ProjectID, "candidate_id": candidate.ID}}, nil
}

func (s *Service) requireProject(ctx context.Context, projectID string) (projects.Project, error) {
	if s.projects == nil {
		return projects.Project{}, ErrStoreNotConfigured
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return projects.Project{}, fmt.Errorf("%w: project_id is required", ErrInvalid)
	}
	project, ok, err := s.projects.Get(ctx, projectID)
	if err != nil {
		return projects.Project{}, err
	}
	if !ok {
		return projects.Project{}, fmt.Errorf("%w: project %q", ErrNotFound, projectID)
	}
	return project, nil
}

func (s *Service) requireWorkItem(ctx context.Context, projectID, workItemID string) (projectwork.WorkItem, error) {
	if s.work == nil {
		return projectwork.WorkItem{}, ErrStoreNotConfigured
	}
	if _, err := s.requireProject(ctx, projectID); err != nil {
		return projectwork.WorkItem{}, err
	}
	workItemID = strings.TrimSpace(workItemID)
	if workItemID == "" {
		return projectwork.WorkItem{}, fmt.Errorf("%w: work_item_id is required", ErrInvalid)
	}
	item, ok, err := s.work.GetWorkItem(ctx, projectID, workItemID)
	if err != nil {
		return projectwork.WorkItem{}, err
	}
	if !ok {
		return projectwork.WorkItem{}, fmt.Errorf("%w: work item %q", ErrNotFound, workItemID)
	}
	return item, nil
}

type projectPatch struct {
	ID                       string      `json:"id,omitempty"`
	Name                     string      `json:"name,omitempty"`
	Description              string      `json:"description,omitempty"`
	WorkspacePath            string      `json:"workspace_path,omitempty"`
	WorkspaceKind            string      `json:"workspace_kind,omitempty"`
	Roots                    []rootPatch `json:"roots,omitempty"`
	DefaultProvider          string      `json:"default_provider,omitempty"`
	DefaultModel             string      `json:"default_model,omitempty"`
	DefaultAgentProfile      string      `json:"default_agent_profile,omitempty"`
	DefaultToolsEnabled      *bool       `json:"default_tools_enabled,omitempty"`
	DefaultWorkspaceMode     string      `json:"default_workspace_mode,omitempty"`
	DefaultSystemPrompt      string      `json:"default_system_prompt,omitempty"`
	DefaultCompactToolOutput *bool       `json:"default_compact_tool_output,omitempty"`
}

type updateProjectPatch struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

type rootPatch struct {
	ID        string `json:"id,omitempty"`
	Path      string `json:"path,omitempty"`
	Kind      string `json:"kind,omitempty"`
	GitRemote string `json:"git_remote,omitempty"`
	GitBranch string `json:"git_branch,omitempty"`
	Active    *bool  `json:"active,omitempty"`
}

type defaultsPatch struct {
	DefaultRootID            *string `json:"default_root_id,omitempty"`
	DefaultProvider          *string `json:"default_provider,omitempty"`
	DefaultModel             *string `json:"default_model,omitempty"`
	DefaultAgentProfile      *string `json:"default_agent_profile,omitempty"`
	DefaultToolsEnabled      *bool   `json:"default_tools_enabled,omitempty"`
	DefaultWorkspaceMode     *string `json:"default_workspace_mode,omitempty"`
	DefaultSystemPrompt      *string `json:"default_system_prompt,omitempty"`
	DefaultCompactToolOutput *bool   `json:"default_compact_tool_output,omitempty"`
}

type moveChatSessionPatch struct {
	ProjectID string `json:"project_id"`
}

type rolePatch struct {
	ID                  string   `json:"id,omitempty"`
	ProjectID           string   `json:"project_id,omitempty"`
	Name                string   `json:"name,omitempty"`
	Description         string   `json:"description,omitempty"`
	Instructions        string   `json:"instructions,omitempty"`
	DefaultDriverKind   string   `json:"default_driver_kind,omitempty"`
	DefaultProvider     string   `json:"default_provider,omitempty"`
	DefaultModel        string   `json:"default_model,omitempty"`
	DefaultAgentProfile string   `json:"default_agent_profile,omitempty"`
	SkillIDs            []string `json:"skill_ids,omitempty"`
}

type workItemPatch struct {
	ID              string   `json:"id,omitempty"`
	ProjectID       string   `json:"project_id,omitempty"`
	Title           string   `json:"title,omitempty"`
	Brief           string   `json:"brief,omitempty"`
	Status          string   `json:"status,omitempty"`
	Priority        string   `json:"priority,omitempty"`
	OwnerRoleID     string   `json:"owner_role_id,omitempty"`
	ReviewerRoleIDs []string `json:"reviewer_role_ids,omitempty"`
}

type updateWorkItemPatch struct {
	Title           *string  `json:"title,omitempty"`
	Brief           *string  `json:"brief,omitempty"`
	Status          *string  `json:"status,omitempty"`
	Priority        *string  `json:"priority,omitempty"`
	OwnerRoleID     *string  `json:"owner_role_id,omitempty"`
	ReviewerRoleIDs []string `json:"reviewer_role_ids,omitempty"`
}

type assignmentPatch struct {
	ID           string          `json:"id,omitempty"`
	ProjectID    string          `json:"project_id,omitempty"`
	WorkItemID   string          `json:"work_item_id,omitempty"`
	RoleID       string          `json:"role_id,omitempty"`
	RootID       string          `json:"root_id,omitempty"`
	DriverKind   string          `json:"driver_kind,omitempty"`
	Status       string          `json:"status,omitempty"`
	ExecutionRef json.RawMessage `json:"execution_ref,omitempty"`
}

type handoffPatch struct {
	ID                    string   `json:"id,omitempty"`
	ProjectID             string   `json:"project_id,omitempty"`
	WorkItemID            string   `json:"work_item_id,omitempty"`
	SourceAssignmentID    string   `json:"source_assignment_id,omitempty"`
	SourceRunID           string   `json:"source_run_id,omitempty"`
	SourceChatSessionID   string   `json:"source_chat_session_id,omitempty"`
	SourceMessageID       string   `json:"source_message_id,omitempty"`
	TargetRoleID          string   `json:"target_role_id,omitempty"`
	TargetAssignmentID    string   `json:"target_assignment_id,omitempty"`
	TargetWorkItemID      string   `json:"target_work_item_id,omitempty"`
	Title                 string   `json:"title,omitempty"`
	Summary               string   `json:"summary,omitempty"`
	RecommendedNextAction string   `json:"recommended_next_action,omitempty"`
	LinkedArtifactIDs     []string `json:"linked_artifact_ids,omitempty"`
	LinkedMemoryIDs       []string `json:"linked_memory_ids,omitempty"`
	ContextRefs           []string `json:"context_refs,omitempty"`
	Status                string   `json:"status,omitempty"`
	ProvenanceKind        string   `json:"provenance_kind,omitempty"`
	TrustLabel            string   `json:"trust_label,omitempty"`
	CreatedByRoleID       string   `json:"created_by_role_id,omitempty"`
}

type updateHandoffPatch struct {
	TargetAssignmentID *string `json:"target_assignment_id,omitempty"`
	TargetRoleID       *string `json:"target_role_id,omitempty"`
	Status             *string `json:"status,omitempty"`
}

type memoryCandidatePatch struct {
	ID                  string                      `json:"id,omitempty"`
	ProjectID           string                      `json:"project_id,omitempty"`
	Title               string                      `json:"title,omitempty"`
	Body                string                      `json:"body,omitempty"`
	SuggestedKind       string                      `json:"suggested_kind,omitempty"`
	SuggestedTrustLabel string                      `json:"suggested_trust_label,omitempty"`
	SuggestedSourceKind string                      `json:"suggested_source_kind,omitempty"`
	SuggestedSourceID   string                      `json:"suggested_source_id,omitempty"`
	SourceRefs          []memory.CandidateSourceRef `json:"source_refs,omitempty"`
}

func rootFromPatch(patch rootPatch, idgen IDGenerator) projects.Root {
	active := true
	if patch.Active != nil {
		active = *patch.Active
	}
	id := strings.TrimSpace(patch.ID)
	if id == "" {
		id = idgen("root")
	}
	return projects.Root{
		ID:        id,
		Path:      patch.Path,
		Kind:      patch.Kind,
		GitRemote: patch.GitRemote,
		GitBranch: patch.GitBranch,
		Active:    active,
	}
}

func rootsFromPatch(patches []rootPatch, idgen IDGenerator) []projects.Root {
	roots := make([]projects.Root, 0, len(patches))
	for _, patch := range patches {
		roots = append(roots, rootFromPatch(patch, idgen))
	}
	return roots
}

func rootsForProjectPatch(patch projectPatch, idgen IDGenerator) ([]projects.Root, error) {
	roots := rootsFromPatch(patch.Roots, idgen)
	workspacePath := strings.TrimSpace(patch.WorkspacePath)
	workspaceKind := strings.TrimSpace(patch.WorkspaceKind)
	if workspacePath != "" {
		if len(roots) > 0 {
			return nil, fmt.Errorf("%w: workspace_path cannot be combined with roots", ErrInvalid)
		}
		roots = []projects.Root{{
			ID:     idgen("root"),
			Path:   workspacePath,
			Kind:   workspaceKind,
			Active: true,
		}}
	} else if workspaceKind != "" {
		return nil, fmt.Errorf("%w: workspace_kind requires workspace_path", ErrInvalid)
	}
	return roots, nil
}

func projectHasRoot(project projects.Project, rootID string) bool {
	rootID = strings.TrimSpace(rootID)
	for _, root := range project.Roots {
		if root.ID == rootID {
			return true
		}
	}
	return false
}

func mapProjectErr(err error) error {
	switch {
	case errors.Is(err, projects.ErrNotFound):
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	case errors.Is(err, projects.ErrInvalid):
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	case errors.Is(err, projects.ErrAlreadyExists):
		return fmt.Errorf("%w: %v", ErrConflict, err)
	default:
		return err
	}
}

func mapProjectWorkErr(err error) error {
	switch {
	case errors.Is(err, projectwork.ErrNotFound):
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	case errors.Is(err, projectwork.ErrInvalid):
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	case errors.Is(err, projectwork.ErrDuplicate), errors.Is(err, projectwork.ErrDuplicateRole):
		return fmt.Errorf("%w: %v", ErrConflict, err)
	case errors.Is(err, projectwork.ErrBuiltInRole):
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	default:
		return err
	}
}

func mapMemoryErr(err error) error {
	switch {
	case errors.Is(err, memory.ErrNotFound):
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	case errors.Is(err, memory.ErrInvalid):
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	case errors.Is(err, memory.ErrAlreadyExists), errors.Is(err, memory.ErrConflict):
		return fmt.Errorf("%w: %v", ErrConflict, err)
	default:
		return err
	}
}
