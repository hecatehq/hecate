package projectassistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectwork"
)

const (
	ActionCreateProject         = "create_project"
	ActionUpdateProject         = "update_project"
	ActionAttachProjectRoot     = "attach_project_root"
	ActionRemoveProjectRoot     = "remove_project_root"
	ActionSetProjectDefaults    = "set_project_defaults"
	ActionMoveChatSession       = "move_chat_session"
	ActionCreateWorkItem        = "create_work_item"
	ActionUpdateWorkItem        = "update_work_item"
	ActionCreateAssignment      = "create_assignment"
	ActionCreateHandoff         = "create_handoff"
	ActionCreateMemoryCandidate = "create_memory_candidate"
)

var (
	ErrInvalid              = errors.New("invalid project assistant proposal")
	ErrUnknownActionKind    = errors.New("unknown project assistant action kind")
	ErrNotFound             = errors.New("project assistant target not found")
	ErrConflict             = errors.New("project assistant conflict")
	ErrConfirmationRequired = errors.New("project assistant confirmation required")
	ErrStoreNotConfigured   = errors.New("project assistant store not configured")
)

type IDGenerator func(prefix string) string

type Service struct {
	mu               sync.Mutex
	projects         projects.Store
	chats            chat.Store
	work             projectwork.Store
	memoryCandidates memory.CandidateStore
	idgen            IDGenerator
	applied          map[string]struct{}
}

type Stores struct {
	Projects         projects.Store
	Chats            chat.Store
	Work             projectwork.Store
	MemoryCandidates memory.CandidateStore
}

type ProposalInput struct {
	ID      string
	Title   string
	Summary string
	Actions []Action
	TraceID string
}

type Proposal struct {
	ID                   string   `json:"id"`
	Title                string   `json:"title"`
	Summary              string   `json:"summary"`
	Actions              []Action `json:"actions"`
	Warnings             []string `json:"warnings,omitempty"`
	RequiresConfirmation bool     `json:"requires_confirmation"`
	TraceID              string   `json:"trace_id,omitempty"`
}

type Action struct {
	Kind   string            `json:"kind"`
	Target map[string]string `json:"target,omitempty"`
	Patch  json.RawMessage   `json:"patch,omitempty"`
	Reason string            `json:"reason,omitempty"`
}

type ApplyResult struct {
	ProposalID string         `json:"proposal_id"`
	Applied    bool           `json:"applied"`
	Actions    []ActionResult `json:"actions"`
}

type ActionResult struct {
	Kind string            `json:"kind"`
	ID   string            `json:"id,omitempty"`
	Data map[string]string `json:"data,omitempty"`
}

var defaultIDCounter atomic.Uint64

func NewService(stores Stores, idgen IDGenerator) *Service {
	if idgen == nil {
		idgen = func(prefix string) string {
			return fmt.Sprintf("%s_%d", strings.TrimSpace(prefix), defaultIDCounter.Add(1))
		}
	}
	return &Service{
		projects:         stores.Projects,
		chats:            stores.Chats,
		work:             stores.Work,
		memoryCandidates: stores.MemoryCandidates,
		idgen:            idgen,
		applied:          make(map[string]struct{}),
	}
}

func (s *Service) Propose(_ context.Context, input ProposalInput) (Proposal, error) {
	if s == nil {
		return Proposal{}, ErrStoreNotConfigured
	}
	actions := cloneActions(input.Actions)
	if len(actions) == 0 {
		return Proposal{}, fmt.Errorf("%w: actions are required", ErrInvalid)
	}
	for _, action := range actions {
		if err := validateActionShape(action); err != nil {
			return Proposal{}, err
		}
	}
	id := strings.TrimSpace(input.ID)
	if id == "" {
		id = s.idgen("pa")
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = "Project operation proposal"
	}
	return Proposal{
		ID:                   id,
		Title:                title,
		Summary:              strings.TrimSpace(input.Summary),
		Actions:              actions,
		RequiresConfirmation: true,
		TraceID:              strings.TrimSpace(input.TraceID),
	}, nil
}

func (s *Service) Apply(ctx context.Context, proposal Proposal, confirmed bool) (ApplyResult, error) {
	if s == nil {
		return ApplyResult{}, ErrStoreNotConfigured
	}
	proposal.ID = strings.TrimSpace(proposal.ID)
	if proposal.ID == "" {
		return ApplyResult{}, fmt.Errorf("%w: proposal id is required", ErrInvalid)
	}
	if proposal.RequiresConfirmation && !confirmed {
		return ApplyResult{}, ErrConfirmationRequired
	}
	for _, action := range proposal.Actions {
		if err := validateActionShape(action); err != nil {
			return ApplyResult{}, err
		}
	}

	// Hold the apply lock through the mutation sequence so a proposal ID cannot
	// race itself into duplicate durable writes.
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.applied[proposal.ID]; ok {
		return ApplyResult{}, fmt.Errorf("%w: proposal %q was already applied", ErrConflict, proposal.ID)
	}

	results := make([]ActionResult, 0, len(proposal.Actions))
	for _, action := range proposal.Actions {
		result, err := s.applyAction(ctx, action)
		if err != nil {
			return ApplyResult{}, err
		}
		results = append(results, result)
	}

	s.applied[proposal.ID] = struct{}{}

	return ApplyResult{ProposalID: proposal.ID, Applied: true, Actions: results}, nil
}

func (s *Service) applyAction(ctx context.Context, action Action) (ActionResult, error) {
	switch normalizeKind(action.Kind) {
	case ActionCreateProject:
		return s.applyCreateProject(ctx, action)
	case ActionUpdateProject:
		return s.applyUpdateProject(ctx, action)
	case ActionAttachProjectRoot:
		return s.applyAttachProjectRoot(ctx, action)
	case ActionRemoveProjectRoot:
		return s.applyRemoveProjectRoot(ctx, action)
	case ActionSetProjectDefaults:
		return s.applySetProjectDefaults(ctx, action)
	case ActionMoveChatSession:
		return s.applyMoveChatSession(ctx, action)
	case ActionCreateWorkItem:
		return s.applyCreateWorkItem(ctx, action)
	case ActionUpdateWorkItem:
		return s.applyUpdateWorkItem(ctx, action)
	case ActionCreateAssignment:
		return s.applyCreateAssignment(ctx, action)
	case ActionCreateHandoff:
		return s.applyCreateHandoff(ctx, action)
	case ActionCreateMemoryCandidate:
		return s.applyCreateMemoryCandidate(ctx, action)
	default:
		return ActionResult{}, fmt.Errorf("%w: %s", ErrUnknownActionKind, action.Kind)
	}
}

func validateActionShape(action Action) error {
	kind := normalizeKind(action.Kind)
	switch kind {
	case ActionCreateProject, ActionUpdateProject, ActionAttachProjectRoot, ActionRemoveProjectRoot,
		ActionSetProjectDefaults, ActionMoveChatSession, ActionCreateWorkItem, ActionUpdateWorkItem,
		ActionCreateAssignment, ActionCreateHandoff, ActionCreateMemoryCandidate:
		if len(action.Patch) == 0 && kind != ActionRemoveProjectRoot {
			return fmt.Errorf("%w: action %q patch is required", ErrInvalid, kind)
		}
		return nil
	default:
		return fmt.Errorf("%w: %s", ErrUnknownActionKind, action.Kind)
	}
}

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

func (s *Service) applyCreateWorkItem(ctx context.Context, action Action) (ActionResult, error) {
	if s.work == nil {
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
	item, err := s.work.CreateWorkItem(ctx, projectwork.WorkItem{
		ID:              id,
		ProjectID:       projectID,
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
	if s.work == nil {
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
	item, err := s.work.UpdateWorkItem(ctx, projectID, workItemID, func(item *projectwork.WorkItem) {
		if patch.Title != nil {
			item.Title = *patch.Title
		}
		if patch.Brief != nil {
			item.Brief = *patch.Brief
		}
		if patch.Status != nil {
			item.Status = *patch.Status
		}
		if patch.Priority != nil {
			item.Priority = *patch.Priority
		}
		if patch.OwnerRoleID != nil {
			item.OwnerRoleID = *patch.OwnerRoleID
		}
		if patch.ReviewerRoleIDs != nil {
			item.ReviewerRoleIDs = append([]string(nil), patch.ReviewerRoleIDs...)
		}
	})
	if err != nil {
		return ActionResult{}, mapProjectWorkErr(err)
	}
	return ActionResult{Kind: ActionUpdateWorkItem, ID: item.ID, Data: map[string]string{"project_id": item.ProjectID, "work_item_id": item.ID}}, nil
}

func (s *Service) applyCreateAssignment(ctx context.Context, action Action) (ActionResult, error) {
	if s.work == nil {
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
	if _, err := s.requireWorkItem(ctx, projectID, patch.WorkItemID); err != nil {
		return ActionResult{}, err
	}
	id := strings.TrimSpace(patch.ID)
	if id == "" {
		id = s.idgen("asgn")
	}
	assignment, err := s.work.CreateAssignment(ctx, projectwork.Assignment{
		ID:                id,
		ProjectID:         projectID,
		WorkItemID:        patch.WorkItemID,
		RoleID:            patch.RoleID,
		DriverKind:        patch.DriverKind,
		Status:            patch.Status,
		TaskID:            patch.TaskID,
		RunID:             patch.RunID,
		ChatSessionID:     patch.ChatSessionID,
		MessageID:         patch.MessageID,
		ContextSnapshotID: patch.ContextSnapshotID,
	})
	if err != nil {
		return ActionResult{}, mapProjectWorkErr(err)
	}
	return ActionResult{Kind: ActionCreateAssignment, ID: assignment.ID, Data: map[string]string{"project_id": assignment.ProjectID, "assignment_id": assignment.ID}}, nil
}

func (s *Service) applyCreateHandoff(ctx context.Context, action Action) (ActionResult, error) {
	if s.work == nil {
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
	handoff, err := s.work.CreateHandoff(ctx, projectwork.Handoff{
		ID:                    id,
		ProjectID:             projectID,
		WorkItemID:            patch.WorkItemID,
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

func (s *Service) applyCreateMemoryCandidate(ctx context.Context, action Action) (ActionResult, error) {
	if s.memoryCandidates == nil {
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
	candidate, err := s.memoryCandidates.CreateCandidate(ctx, memory.Candidate{
		ID:                  id,
		ProjectID:           projectID,
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
	ID                string `json:"id,omitempty"`
	ProjectID         string `json:"project_id,omitempty"`
	WorkItemID        string `json:"work_item_id,omitempty"`
	RoleID            string `json:"role_id,omitempty"`
	DriverKind        string `json:"driver_kind,omitempty"`
	Status            string `json:"status,omitempty"`
	TaskID            string `json:"task_id,omitempty"`
	RunID             string `json:"run_id,omitempty"`
	ChatSessionID     string `json:"chat_session_id,omitempty"`
	MessageID         string `json:"message_id,omitempty"`
	ContextSnapshotID string `json:"context_snapshot_id,omitempty"`
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

func decodePatch(action Action, target any) error {
	if len(action.Patch) == 0 {
		return nil
	}
	if err := json.Unmarshal(action.Patch, target); err != nil {
		return fmt.Errorf("%w: decode %s patch: %v", ErrInvalid, normalizeKind(action.Kind), err)
	}
	return nil
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

func targetValue(action Action, key string) string {
	if action.Target == nil {
		return ""
	}
	return strings.TrimSpace(action.Target[key])
}

func normalizeKind(kind string) string {
	return strings.TrimSpace(kind)
}

func cloneActions(actions []Action) []Action {
	if actions == nil {
		return nil
	}
	cloned := make([]Action, len(actions))
	for idx, action := range actions {
		cloned[idx] = Action{
			Kind:   action.Kind,
			Target: cloneStringMap(action.Target),
			Patch:  append(json.RawMessage(nil), action.Patch...),
			Reason: action.Reason,
		}
	}
	return cloned
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func mapProjectErr(err error) error {
	switch {
	case errors.Is(err, projects.ErrNotFound):
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	case errors.Is(err, projects.ErrInvalid):
		return fmt.Errorf("%w: %v", ErrInvalid, err)
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
