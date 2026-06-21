package projectassistant

import (
	"context"
	"fmt"
	"strings"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectwork"
)

type applyPreflight struct {
	service          *Service
	projects         map[string]projects.Project
	roles            map[string]projectwork.AgentRoleProfile
	workItems        map[string]projectwork.WorkItem
	assignments      map[string]projectwork.Assignment
	handoffs         map[string]projectwork.Handoff
	memoryCandidates map[string]memory.Candidate
}

func (s *Service) preflightApply(ctx context.Context, actions []Action, startIndex int) (int, error) {
	preflight := &applyPreflight{
		service:          s,
		projects:         make(map[string]projects.Project),
		roles:            make(map[string]projectwork.AgentRoleProfile),
		workItems:        make(map[string]projectwork.WorkItem),
		assignments:      make(map[string]projectwork.Assignment),
		handoffs:         make(map[string]projectwork.Handoff),
		memoryCandidates: make(map[string]memory.Candidate),
	}
	for idx := startIndex; idx < len(actions); idx++ {
		if err := preflight.action(ctx, actions[idx]); err != nil {
			return idx, err
		}
	}
	return -1, nil
}

func (p *applyPreflight) action(ctx context.Context, action Action) error {
	spec, ok := lookupApplyActionSpec(action.Kind)
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownActionKind, action.Kind)
	}
	return spec.preflight(p, ctx, action)
}

func (p *applyPreflight) createProject(ctx context.Context, action Action) error {
	if p.service.projects == nil {
		return ErrStoreNotConfigured
	}
	var patch projectPatch
	if err := decodePatch(action, &patch); err != nil {
		return err
	}
	if _, err := preflightRootsForProjectPatch(patch); err != nil {
		return err
	}
	id := strings.TrimSpace(patch.ID)
	if id == "" {
		return nil
	}
	if exists, err := p.projectExists(ctx, id); err != nil {
		return err
	} else if exists {
		return fmt.Errorf("%w: project %q already exists", ErrConflict, id)
	}
	p.projects[id] = projects.Project{
		ID:                       id,
		Name:                     patch.Name,
		Description:              patch.Description,
		Roots:                    preflightRootsFromPatch(patch.Roots),
		DefaultProvider:          patch.DefaultProvider,
		DefaultModel:             patch.DefaultModel,
		DefaultAgentProfile:      patch.DefaultAgentProfile,
		DefaultToolsEnabled:      patch.DefaultToolsEnabled,
		DefaultWorkspaceMode:     patch.DefaultWorkspaceMode,
		DefaultSystemPrompt:      patch.DefaultSystemPrompt,
		DefaultCompactToolOutput: patch.DefaultCompactToolOutput,
	}
	return nil
}

func (p *applyPreflight) updateProject(ctx context.Context, action Action) error {
	projectID := targetValue(action, "project_id")
	project, err := p.requireProject(ctx, projectID)
	if err != nil {
		return err
	}
	var patch updateProjectPatch
	if err := decodePatch(action, &patch); err != nil {
		return err
	}
	if patch.Name != nil {
		project.Name = *patch.Name
	}
	if patch.Description != nil {
		project.Description = *patch.Description
	}
	p.projects[project.ID] = project
	return nil
}

func (p *applyPreflight) attachProjectRoot(ctx context.Context, action Action) error {
	projectID := targetValue(action, "project_id")
	project, err := p.requireProject(ctx, projectID)
	if err != nil {
		return err
	}
	var patch rootPatch
	if err := decodePatch(action, &patch); err != nil {
		return err
	}
	root := preflightRootFromPatch(patch)
	if root.ID == "" {
		return nil
	}
	if projectHasRoot(project, root.ID) {
		return fmt.Errorf("%w: root %q already exists", ErrConflict, root.ID)
	}
	project.Roots = append(project.Roots, root)
	p.projects[project.ID] = project
	return nil
}

func (p *applyPreflight) removeProjectRoot(ctx context.Context, action Action) error {
	projectID := targetValue(action, "project_id")
	rootID := targetValue(action, "root_id")
	if projectID == "" || rootID == "" {
		return fmt.Errorf("%w: target.project_id and target.root_id are required", ErrInvalid)
	}
	project, err := p.requireProject(ctx, projectID)
	if err != nil {
		return err
	}
	if !projectHasRoot(project, rootID) {
		return fmt.Errorf("%w: root %q", ErrNotFound, rootID)
	}
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
	p.projects[project.ID] = project
	return nil
}

func (p *applyPreflight) setProjectDefaults(ctx context.Context, action Action) error {
	projectID := targetValue(action, "project_id")
	project, err := p.requireProject(ctx, projectID)
	if err != nil {
		return err
	}
	var patch defaultsPatch
	if err := decodePatch(action, &patch); err != nil {
		return err
	}
	if patch.DefaultRootID != nil && *patch.DefaultRootID != "" && !projectHasRoot(project, *patch.DefaultRootID) {
		return fmt.Errorf("%w: root %q", ErrNotFound, *patch.DefaultRootID)
	}
	if patch.DefaultRootID != nil {
		project.DefaultRootID = *patch.DefaultRootID
	}
	p.projects[project.ID] = project
	return nil
}

func (p *applyPreflight) moveChatSession(ctx context.Context, action Action) error {
	if p.service.chats == nil {
		return ErrStoreNotConfigured
	}
	sessionID := targetValue(action, "chat_session_id")
	if sessionID == "" {
		return fmt.Errorf("%w: target.chat_session_id is required", ErrInvalid)
	}
	var patch moveChatSessionPatch
	if err := decodePatch(action, &patch); err != nil {
		return err
	}
	if strings.TrimSpace(patch.ProjectID) != "" {
		if _, err := p.requireProject(ctx, patch.ProjectID); err != nil {
			return err
		}
	}
	if _, err := p.requireChatSession(ctx, sessionID); err != nil {
		return err
	}
	return nil
}

func (p *applyPreflight) createRole(ctx context.Context, action Action) error {
	if p.service.work == nil {
		return ErrStoreNotConfigured
	}
	var patch rolePatch
	if err := decodePatch(action, &patch); err != nil {
		return err
	}
	projectID := firstNonEmpty(patch.ProjectID, targetValue(action, "project_id"))
	if _, err := p.requireProject(ctx, projectID); err != nil {
		return err
	}
	id := strings.TrimSpace(patch.ID)
	if id == "" {
		return nil
	}
	if exists, err := p.roleExists(ctx, projectID, id); err != nil {
		return err
	} else if exists {
		return fmt.Errorf("%w: role %q already exists", ErrConflict, id)
	}
	p.roles[scopedKey(projectID, id)] = projectwork.AgentRoleProfile{ID: id, ProjectID: projectID}
	return nil
}

func (p *applyPreflight) createWorkItem(ctx context.Context, action Action) error {
	if p.service.work == nil {
		return ErrStoreNotConfigured
	}
	var patch workItemPatch
	if err := decodePatch(action, &patch); err != nil {
		return err
	}
	projectID := firstNonEmpty(patch.ProjectID, targetValue(action, "project_id"))
	if _, err := p.requireProject(ctx, projectID); err != nil {
		return err
	}
	id := strings.TrimSpace(patch.ID)
	if id == "" {
		return nil
	}
	if exists, err := p.workItemExists(ctx, projectID, id); err != nil {
		return err
	} else if exists {
		return fmt.Errorf("%w: work item %q already exists", ErrConflict, id)
	}
	p.workItems[scopedKey(projectID, id)] = projectwork.WorkItem{ID: id, ProjectID: projectID, Title: patch.Title, Status: patch.Status}
	return nil
}

func (p *applyPreflight) updateWorkItem(ctx context.Context, action Action) error {
	if p.service.work == nil {
		return ErrStoreNotConfigured
	}
	projectID := targetValue(action, "project_id")
	workItemID := targetValue(action, "work_item_id")
	item, err := p.requireWorkItem(ctx, projectID, workItemID)
	if err != nil {
		return err
	}
	var patch updateWorkItemPatch
	if err := decodePatch(action, &patch); err != nil {
		return err
	}
	if patch.Status != nil && strings.TrimSpace(*patch.Status) == projectwork.WorkItemStatusDone {
		readiness, err := p.workItemReadiness(ctx, item)
		if err != nil {
			return err
		}
		if readiness.Status != "done" && !readiness.Ready {
			return fmt.Errorf("%w: %w", ErrConflict, projectwork.WorkItemCloseoutBlockedError{Readiness: readiness})
		}
	}
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
	p.workItems[scopedKey(projectID, workItemID)] = item
	return nil
}

func (p *applyPreflight) createAssignment(ctx context.Context, action Action) error {
	if p.service.work == nil {
		return ErrStoreNotConfigured
	}
	var patch assignmentPatch
	if err := decodePatch(action, &patch); err != nil {
		return err
	}
	projectID := firstNonEmpty(patch.ProjectID, targetValue(action, "project_id"))
	item, err := p.requireWorkItem(ctx, projectID, patch.WorkItemID)
	if err != nil {
		return err
	}
	projectID = item.ProjectID
	if rootID := strings.TrimSpace(patch.RootID); rootID != "" {
		project, err := p.requireProject(ctx, projectID)
		if err != nil {
			return err
		}
		if !projectHasRoot(project, rootID) {
			return fmt.Errorf("%w: root %q", ErrNotFound, rootID)
		}
	}
	id := strings.TrimSpace(patch.ID)
	if id != "" {
		if exists, err := p.assignmentExists(ctx, projectID, id); err != nil {
			return err
		} else if exists {
			return fmt.Errorf("%w: assignment %q already exists", ErrConflict, id)
		}
	} else {
		id = preflightSyntheticID("assignment", len(p.assignments))
	}
	status := strings.TrimSpace(patch.Status)
	if status == "" {
		status = projectwork.AssignmentStatusQueued
	}
	p.assignments[scopedKey(projectID, id)] = projectwork.Assignment{ID: id, ProjectID: projectID, WorkItemID: item.ID, RoleID: patch.RoleID, RootID: strings.TrimSpace(patch.RootID), DriverKind: strings.TrimSpace(patch.DriverKind), Status: status}
	return nil
}

func (p *applyPreflight) createHandoff(ctx context.Context, action Action) error {
	if p.service.work == nil {
		return ErrStoreNotConfigured
	}
	var patch handoffPatch
	if err := decodePatch(action, &patch); err != nil {
		return err
	}
	projectID := firstNonEmpty(patch.ProjectID, targetValue(action, "project_id"))
	item, err := p.requireWorkItem(ctx, projectID, patch.WorkItemID)
	if err != nil {
		return err
	}
	projectID = item.ProjectID
	if sourceAssignmentID := strings.TrimSpace(patch.SourceAssignmentID); sourceAssignmentID != "" {
		assignment, err := p.requireAssignment(ctx, projectID, sourceAssignmentID)
		if err != nil {
			return err
		}
		if assignment.WorkItemID != item.ID {
			return fmt.Errorf("%w: source assignment %q", ErrNotFound, sourceAssignmentID)
		}
	}
	if targetAssignmentID := strings.TrimSpace(patch.TargetAssignmentID); targetAssignmentID != "" {
		assignment, err := p.requireAssignment(ctx, projectID, targetAssignmentID)
		if err != nil {
			return err
		}
		if targetWorkItemID := strings.TrimSpace(patch.TargetWorkItemID); targetWorkItemID != "" && assignment.WorkItemID != targetWorkItemID {
			return fmt.Errorf("%w: target assignment %q", ErrNotFound, targetAssignmentID)
		}
	}
	id := strings.TrimSpace(patch.ID)
	if id != "" {
		if exists, err := p.handoffExists(ctx, projectID, id); err != nil {
			return err
		} else if exists {
			return fmt.Errorf("%w: handoff %q already exists", ErrConflict, id)
		}
	} else {
		id = preflightSyntheticID("handoff", len(p.handoffs))
	}
	status := strings.TrimSpace(patch.Status)
	if status == "" {
		status = projectwork.HandoffStatusPending
	}
	p.handoffs[scopedKey(projectID, id)] = projectwork.Handoff{
		ID:                 id,
		ProjectID:          projectID,
		WorkItemID:         item.ID,
		TargetRoleID:       strings.TrimSpace(patch.TargetRoleID),
		TargetAssignmentID: strings.TrimSpace(patch.TargetAssignmentID),
		TargetWorkItemID:   strings.TrimSpace(patch.TargetWorkItemID),
		LinkedArtifactIDs:  append([]string(nil), patch.LinkedArtifactIDs...),
		Status:             status,
	}
	return nil
}

func (p *applyPreflight) updateHandoff(ctx context.Context, action Action) error {
	if p.service.work == nil {
		return ErrStoreNotConfigured
	}
	projectID := targetValue(action, "project_id")
	workItemID := targetValue(action, "work_item_id")
	handoffID := targetValue(action, "handoff_id")
	if projectID == "" || workItemID == "" || handoffID == "" {
		return fmt.Errorf("%w: target.project_id, target.work_item_id, and target.handoff_id are required", ErrInvalid)
	}
	if _, err := p.requireWorkItem(ctx, projectID, workItemID); err != nil {
		return err
	}
	handoff, err := p.requireHandoff(ctx, projectID, workItemID, handoffID)
	if err != nil {
		return err
	}
	var patch updateHandoffPatch
	if err := decodePatch(action, &patch); err != nil {
		return err
	}
	if patch.TargetAssignmentID == nil && patch.TargetRoleID == nil && patch.Status == nil {
		return fmt.Errorf("%w: update_handoff patch must set at least one mutable field", ErrInvalid)
	}
	if patch.TargetAssignmentID != nil && strings.TrimSpace(*patch.TargetAssignmentID) != "" {
		assignment, err := p.requireAssignment(ctx, projectID, *patch.TargetAssignmentID)
		if err != nil {
			return err
		}
		if handoff.TargetWorkItemID != "" && assignment.WorkItemID != handoff.TargetWorkItemID {
			return fmt.Errorf("%w: target assignment %q", ErrNotFound, *patch.TargetAssignmentID)
		}
		handoff.TargetAssignmentID = strings.TrimSpace(*patch.TargetAssignmentID)
	}
	if patch.TargetAssignmentID != nil && strings.TrimSpace(*patch.TargetAssignmentID) == "" {
		handoff.TargetAssignmentID = ""
	}
	if patch.TargetRoleID != nil {
		handoff.TargetRoleID = strings.TrimSpace(*patch.TargetRoleID)
	}
	if patch.Status != nil {
		status := strings.TrimSpace(*patch.Status)
		if status == "" {
			status = projectwork.HandoffStatusPending
		}
		handoff.Status = status
	}
	p.handoffs[scopedKey(projectID, handoffID)] = handoff
	return nil
}

func (p *applyPreflight) createMemoryCandidate(ctx context.Context, action Action) error {
	if p.service.memoryCandidates == nil {
		return ErrStoreNotConfigured
	}
	var patch memoryCandidatePatch
	if err := decodePatch(action, &patch); err != nil {
		return err
	}
	projectID := firstNonEmpty(patch.ProjectID, targetValue(action, "project_id"))
	if _, err := p.requireProject(ctx, projectID); err != nil {
		return err
	}
	id := strings.TrimSpace(patch.ID)
	if id == "" {
		return nil
	}
	if exists, err := p.memoryCandidateExists(ctx, projectID, id); err != nil {
		return err
	} else if exists {
		return fmt.Errorf("%w: memory candidate %q already exists", ErrConflict, id)
	}
	p.memoryCandidates[scopedKey(projectID, id)] = memory.Candidate{ID: id, ProjectID: projectID}
	return nil
}

func (p *applyPreflight) requireProject(ctx context.Context, projectID string) (projects.Project, error) {
	if p.service.projects == nil {
		return projects.Project{}, ErrStoreNotConfigured
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return projects.Project{}, fmt.Errorf("%w: project_id is required", ErrInvalid)
	}
	if project, ok := p.projects[projectID]; ok {
		return project, nil
	}
	project, ok, err := p.service.projects.Get(ctx, projectID)
	if err != nil {
		return projects.Project{}, err
	}
	if !ok {
		return projects.Project{}, fmt.Errorf("%w: project %q", ErrNotFound, projectID)
	}
	p.projects[projectID] = project
	return project, nil
}

func (p *applyPreflight) projectExists(ctx context.Context, projectID string) (bool, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return false, nil
	}
	if _, ok := p.projects[projectID]; ok {
		return true, nil
	}
	_, ok, err := p.service.projects.Get(ctx, projectID)
	return ok, err
}

func (p *applyPreflight) requireWorkItem(ctx context.Context, projectID, workItemID string) (projectwork.WorkItem, error) {
	if p.service.work == nil {
		return projectwork.WorkItem{}, ErrStoreNotConfigured
	}
	if _, err := p.requireProject(ctx, projectID); err != nil {
		return projectwork.WorkItem{}, err
	}
	workItemID = strings.TrimSpace(workItemID)
	if workItemID == "" {
		return projectwork.WorkItem{}, fmt.Errorf("%w: work_item_id is required", ErrInvalid)
	}
	key := scopedKey(projectID, workItemID)
	if item, ok := p.workItems[key]; ok {
		return item, nil
	}
	item, ok, err := p.service.work.GetWorkItem(ctx, projectID, workItemID)
	if err != nil {
		return projectwork.WorkItem{}, err
	}
	if !ok {
		return projectwork.WorkItem{}, fmt.Errorf("%w: work item %q", ErrNotFound, workItemID)
	}
	p.workItems[key] = item
	return item, nil
}

func (p *applyPreflight) workItemReadiness(ctx context.Context, item projectwork.WorkItem) (projectwork.WorkItemReadiness, error) {
	assignments, err := p.assignmentsForWorkItem(ctx, item.ProjectID, item.ID)
	if err != nil {
		return projectwork.WorkItemReadiness{}, err
	}
	artifacts, err := p.service.work.ListArtifacts(ctx, projectwork.ArtifactFilter{ProjectID: item.ProjectID, WorkItemID: item.ID})
	if err != nil {
		return projectwork.WorkItemReadiness{}, err
	}
	handoffs, err := p.handoffsForWorkItem(ctx, item.ProjectID, item.ID)
	if err != nil {
		return projectwork.WorkItemReadiness{}, err
	}
	return projectwork.EvaluateWorkItemReadiness(item, assignments, artifacts, handoffs), nil
}

func (p *applyPreflight) assignmentsForWorkItem(ctx context.Context, projectID, workItemID string) ([]projectwork.Assignment, error) {
	items, err := p.service.work.ListAssignments(ctx, projectwork.AssignmentFilter{ProjectID: projectID, WorkItemID: workItemID})
	if err != nil {
		return nil, err
	}
	indexByID := make(map[string]int, len(items)+len(p.assignments))
	for idx, assignment := range items {
		indexByID[assignment.ID] = idx
	}
	for _, assignment := range p.assignments {
		if assignment.ProjectID != projectID || assignment.WorkItemID != workItemID {
			continue
		}
		if idx, ok := indexByID[assignment.ID]; ok {
			items[idx] = assignment
			continue
		}
		indexByID[assignment.ID] = len(items)
		items = append(items, assignment)
	}
	return items, nil
}

func (p *applyPreflight) handoffsForWorkItem(ctx context.Context, projectID, workItemID string) ([]projectwork.Handoff, error) {
	items, err := p.service.work.ListHandoffs(ctx, projectwork.HandoffFilter{ProjectID: projectID, WorkItemID: workItemID})
	if err != nil {
		return nil, err
	}
	indexByID := make(map[string]int, len(items)+len(p.handoffs))
	for idx, handoff := range items {
		indexByID[handoff.ID] = idx
	}
	for _, handoff := range p.handoffs {
		if handoff.ProjectID != projectID || handoff.WorkItemID != workItemID {
			continue
		}
		if idx, ok := indexByID[handoff.ID]; ok {
			items[idx] = handoff
			continue
		}
		indexByID[handoff.ID] = len(items)
		items = append(items, handoff)
	}
	return items, nil
}

func (p *applyPreflight) workItemExists(ctx context.Context, projectID, workItemID string) (bool, error) {
	if workItemID == "" {
		return false, nil
	}
	if _, ok := p.workItems[scopedKey(projectID, workItemID)]; ok {
		return true, nil
	}
	_, ok, err := p.service.work.GetWorkItem(ctx, projectID, workItemID)
	return ok, err
}

func (p *applyPreflight) roleExists(ctx context.Context, projectID, roleID string) (bool, error) {
	if roleID == "" {
		return false, nil
	}
	if _, ok := p.roles[scopedKey(projectID, roleID)]; ok {
		return true, nil
	}
	roles, err := p.service.work.ListRoles(ctx, projectID)
	if err != nil {
		return false, err
	}
	for _, role := range roles {
		if role.ID == roleID {
			return true, nil
		}
	}
	return false, nil
}

func (p *applyPreflight) requireAssignment(ctx context.Context, projectID, assignmentID string) (projectwork.Assignment, error) {
	assignmentID = strings.TrimSpace(assignmentID)
	if assignmentID == "" {
		return projectwork.Assignment{}, fmt.Errorf("%w: assignment_id is required", ErrInvalid)
	}
	key := scopedKey(projectID, assignmentID)
	if assignment, ok := p.assignments[key]; ok {
		return assignment, nil
	}
	assignments, err := p.service.work.ListAssignments(ctx, projectwork.AssignmentFilter{ProjectID: projectID})
	if err != nil {
		return projectwork.Assignment{}, err
	}
	for _, assignment := range assignments {
		if assignment.ID == assignmentID {
			p.assignments[key] = assignment
			return assignment, nil
		}
	}
	return projectwork.Assignment{}, fmt.Errorf("%w: assignment %q", ErrNotFound, assignmentID)
}

func (p *applyPreflight) assignmentExists(ctx context.Context, projectID, assignmentID string) (bool, error) {
	if assignmentID == "" {
		return false, nil
	}
	if _, ok := p.assignments[scopedKey(projectID, assignmentID)]; ok {
		return true, nil
	}
	assignments, err := p.service.work.ListAssignments(ctx, projectwork.AssignmentFilter{ProjectID: projectID})
	if err != nil {
		return false, err
	}
	for _, assignment := range assignments {
		if assignment.ID == assignmentID {
			return true, nil
		}
	}
	return false, nil
}

func (p *applyPreflight) requireHandoff(ctx context.Context, projectID, workItemID, handoffID string) (projectwork.Handoff, error) {
	handoffID = strings.TrimSpace(handoffID)
	if handoffID == "" {
		return projectwork.Handoff{}, fmt.Errorf("%w: handoff_id is required", ErrInvalid)
	}
	key := scopedKey(projectID, handoffID)
	if handoff, ok := p.handoffs[key]; ok {
		if handoff.WorkItemID == workItemID {
			return handoff, nil
		}
		return projectwork.Handoff{}, fmt.Errorf("%w: handoff %q", ErrNotFound, handoffID)
	}
	handoffs, err := p.service.work.ListHandoffs(ctx, projectwork.HandoffFilter{ProjectID: projectID, WorkItemID: workItemID})
	if err != nil {
		return projectwork.Handoff{}, err
	}
	for _, handoff := range handoffs {
		if handoff.ID == handoffID {
			p.handoffs[key] = handoff
			return handoff, nil
		}
	}
	return projectwork.Handoff{}, fmt.Errorf("%w: handoff %q", ErrNotFound, handoffID)
}

func (p *applyPreflight) handoffExists(ctx context.Context, projectID, handoffID string) (bool, error) {
	if handoffID == "" {
		return false, nil
	}
	if _, ok := p.handoffs[scopedKey(projectID, handoffID)]; ok {
		return true, nil
	}
	handoffs, err := p.service.work.ListHandoffs(ctx, projectwork.HandoffFilter{ProjectID: projectID})
	if err != nil {
		return false, err
	}
	for _, handoff := range handoffs {
		if handoff.ID == handoffID {
			return true, nil
		}
	}
	return false, nil
}

func (p *applyPreflight) requireChatSession(ctx context.Context, sessionID string) (chat.Session, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return chat.Session{}, fmt.Errorf("%w: chat_session_id is required", ErrInvalid)
	}
	session, ok, err := p.service.chats.Get(ctx, sessionID)
	if err != nil {
		return chat.Session{}, err
	}
	if !ok {
		return chat.Session{}, fmt.Errorf("%w: chat session %q", ErrNotFound, sessionID)
	}
	return session, nil
}

func (p *applyPreflight) memoryCandidateExists(ctx context.Context, projectID, candidateID string) (bool, error) {
	if candidateID == "" {
		return false, nil
	}
	if _, ok := p.memoryCandidates[scopedKey(projectID, candidateID)]; ok {
		return true, nil
	}
	_, ok, err := p.service.memoryCandidates.GetCandidate(ctx, projectID, candidateID)
	return ok, err
}

func preflightRootsForProjectPatch(patch projectPatch) ([]projects.Root, error) {
	workspacePath := strings.TrimSpace(patch.WorkspacePath)
	workspaceKind := strings.TrimSpace(patch.WorkspaceKind)
	if workspacePath != "" && len(patch.Roots) > 0 {
		return nil, fmt.Errorf("%w: workspace_path cannot be combined with roots", ErrInvalid)
	}
	if workspacePath == "" && workspaceKind != "" {
		return nil, fmt.Errorf("%w: workspace_kind requires workspace_path", ErrInvalid)
	}
	return preflightRootsFromPatch(patch.Roots), nil
}

func preflightRootsFromPatch(patches []rootPatch) []projects.Root {
	roots := make([]projects.Root, 0, len(patches))
	for _, patch := range patches {
		root := preflightRootFromPatch(patch)
		if root.ID != "" {
			roots = append(roots, root)
		}
	}
	return roots
}

func preflightRootFromPatch(patch rootPatch) projects.Root {
	active := true
	if patch.Active != nil {
		active = *patch.Active
	}
	return projects.Root{
		ID:        strings.TrimSpace(patch.ID),
		Path:      patch.Path,
		Kind:      patch.Kind,
		GitRemote: patch.GitRemote,
		GitBranch: patch.GitBranch,
		Active:    active,
	}
}

func scopedKey(scope, id string) string {
	return strings.TrimSpace(scope) + "\x00" + strings.TrimSpace(id)
}

func preflightSyntheticID(kind string, count int) string {
	return fmt.Sprintf("__preflight_%s_%d", strings.TrimSpace(kind), count+1)
}
