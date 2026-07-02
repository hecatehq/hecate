package api

import (
	"context"
	"strings"

	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
)

func (h *Handler) projectChatStrictEmbeddedCairnlineReads() bool {
	return h != nil && h.projectReadRoutesUseCairnlineReadModel() && h.requiresEmbeddedCairnlineProjectReads()
}

func (h *Handler) projectChatSidecarCairnlineReads() bool {
	return h != nil && h.projectCairnlineSidecarReadRoutesEnabled()
}

func (h *Handler) strictEmbeddedCairnlineProjectChatView(ctx context.Context, projectID string) (*cairnlineProjectWorkView, bool) {
	projectID = strings.TrimSpace(projectID)
	if !h.projectChatStrictEmbeddedCairnlineReads() || projectID == "" {
		return nil, false
	}
	view, err := h.cairnlineProjectWorkView(ctx, projectID)
	if err != nil {
		return nil, true
	}
	return view, true
}

func (h *Handler) sidecarCairnlineProjectSummary(ctx context.Context, projectID string) (*projects.Project, bool) {
	projectID = strings.TrimSpace(projectID)
	if !h.projectChatSidecarCairnlineReads() || projectID == "" {
		return nil, false
	}
	item, ok, err := h.cairnlineSidecarProject(ctx, projectID)
	if err != nil || !ok {
		return nil, true
	}
	project := projectFromCairnlineSidecar(item)
	return &project, true
}

func (h *Handler) strictEmbeddedCairnlineProjectSummary(ctx context.Context, projectID string) (*projects.Project, bool) {
	view, ok := h.strictEmbeddedCairnlineProjectChatView(ctx, projectID)
	if !ok {
		return nil, false
	}
	if view == nil {
		return nil, true
	}
	defer view.Close()
	item := view.snapshot.Project
	return &item, true
}

func (h *Handler) sidecarCairnlineProjectChatRoles(ctx context.Context, projectID string) ([]projectwork.AgentRoleProfile, bool) {
	projectID = strings.TrimSpace(projectID)
	if !h.projectChatSidecarCairnlineReads() || projectID == "" {
		return nil, false
	}
	roles, err := h.cairnlineSidecarProjectRoles(ctx, projectID)
	if err != nil {
		return nil, true
	}
	return projectRolesFromCairnlineSidecar(roles), true
}

func (h *Handler) strictEmbeddedCairnlineProjectChatRoles(ctx context.Context, projectID string) ([]projectwork.AgentRoleProfile, bool) {
	view, ok := h.strictEmbeddedCairnlineProjectChatView(ctx, projectID)
	if !ok {
		return nil, false
	}
	if view == nil {
		return nil, true
	}
	defer view.Close()
	roles, err := view.service.ListRoles(ctx, view.snapshot.Project.ID)
	if err != nil {
		return nil, true
	}
	executionProfiles, err := view.service.ListExecutionProfiles(ctx)
	if err != nil {
		return nil, true
	}
	executionProfilesByID := cairnlineExecutionProfilesByID(executionProfiles)
	out := make([]projectwork.AgentRoleProfile, 0, len(roles))
	for _, role := range roles {
		out = append(out, projectWorkRoleFromCairnline(role, executionProfilesByID, projectwork.AgentRoleProfile{}))
	}
	return out, true
}

func (h *Handler) sidecarCairnlineProjectChatSkills(ctx context.Context, projectID string) ([]projectskills.Skill, bool) {
	projectID = strings.TrimSpace(projectID)
	if !h.projectChatSidecarCairnlineReads() || projectID == "" {
		return nil, false
	}
	items, err := h.cairnlineSidecarProjectSkills(ctx, projectID)
	if err != nil {
		return nil, true
	}
	out := make([]projectskills.Skill, 0, len(items))
	for _, skill := range projectSkillsFromCairnlineSidecar(items) {
		if !skill.Enabled {
			continue
		}
		status := strings.TrimSpace(skill.Status)
		if status != "" && status != projectskills.StatusAvailable {
			continue
		}
		out = append(out, skill)
	}
	sortProjectChatSkills(out)
	return out, true
}

func (h *Handler) strictEmbeddedCairnlineProjectChatSkills(ctx context.Context, projectID string) ([]projectskills.Skill, bool) {
	view, ok := h.strictEmbeddedCairnlineProjectChatView(ctx, projectID)
	if !ok {
		return nil, false
	}
	if view == nil {
		return nil, true
	}
	defer view.Close()
	items, err := view.service.ListProjectSkills(ctx, view.snapshot.Project.ID)
	if err != nil {
		return nil, true
	}
	out := make([]projectskills.Skill, 0, len(items))
	for _, item := range items {
		skill := projectSkillFromCairnline(item, projectskills.Skill{})
		if !skill.Enabled {
			continue
		}
		status := strings.TrimSpace(skill.Status)
		if status != "" && status != projectskills.StatusAvailable {
			continue
		}
		out = append(out, skill)
	}
	sortProjectChatSkills(out)
	return out, true
}

func (h *Handler) sidecarCairnlineProjectChatWorkSnapshot(ctx context.Context, projectID string) (projectChatWorkSnapshot, bool) {
	projectID = strings.TrimSpace(projectID)
	if !h.projectChatSidecarCairnlineReads() || projectID == "" {
		return projectChatWorkSnapshot{}, false
	}
	workItems, err := h.cairnlineSidecarProjectWorkItems(ctx, projectID)
	if err != nil {
		return projectChatWorkSnapshot{}, true
	}
	filteredWorkItems := make([]projectwork.WorkItem, 0, len(workItems))
	for _, item := range projectWorkItemsFromCairnlineSidecar(workItems) {
		if projectChatStatusAllowed(item.Status, projectChatPromptWorkItemStatuses) {
			filteredWorkItems = append(filteredWorkItems, item)
		}
	}
	workItemsTruncated := len(filteredWorkItems) > projectChatPromptWorkMaxItems
	if workItemsTruncated {
		filteredWorkItems = filteredWorkItems[:projectChatPromptWorkMaxItems]
	}

	assignments, err := h.cairnlineSidecarProjectAssignments(ctx, projectID)
	if err != nil {
		assignments = nil
	}
	filteredAssignments := make([]projectwork.Assignment, 0, len(assignments))
	for _, assignment := range projectAssignmentsFromCairnlineSidecar(assignments) {
		if projectChatStatusAllowed(assignment.Status, projectChatPromptAssignmentStatuses) {
			filteredAssignments = append(filteredAssignments, assignment)
		}
	}
	assignmentsTruncated := len(filteredAssignments) > projectChatPromptAssignmentMaxItems
	if assignmentsTruncated {
		filteredAssignments = filteredAssignments[:projectChatPromptAssignmentMaxItems]
	}
	return projectChatWorkSnapshot{
		WorkItems:            filteredWorkItems,
		Assignments:          filteredAssignments,
		WorkItemsTruncated:   workItemsTruncated,
		AssignmentsTruncated: assignmentsTruncated,
	}, true
}

func (h *Handler) strictEmbeddedCairnlineProjectChatWorkSnapshot(ctx context.Context, projectID string) (projectChatWorkSnapshot, bool) {
	view, ok := h.strictEmbeddedCairnlineProjectChatView(ctx, projectID)
	if !ok {
		return projectChatWorkSnapshot{}, false
	}
	if view == nil {
		return projectChatWorkSnapshot{}, true
	}
	defer view.Close()
	projectID = view.snapshot.Project.ID
	workItems, err := view.service.ListWorkItems(ctx, projectID)
	if err != nil {
		workItems = nil
	}
	filteredWorkItems := make([]projectwork.WorkItem, 0, len(workItems))
	for _, item := range workItems {
		workItem := projectWorkItemFromCairnline(item)
		if projectChatStatusAllowed(workItem.Status, projectChatPromptWorkItemStatuses) {
			filteredWorkItems = append(filteredWorkItems, workItem)
		}
	}
	workItemsTruncated := len(filteredWorkItems) > projectChatPromptWorkMaxItems
	if workItemsTruncated {
		filteredWorkItems = filteredWorkItems[:projectChatPromptWorkMaxItems]
	}

	assignments, err := view.service.ListAssignments(ctx, projectID)
	if err != nil {
		assignments = nil
	}
	filteredAssignments := make([]projectwork.Assignment, 0, len(assignments))
	for _, item := range assignments {
		assignment := projectWorkAssignmentFromCairnline(item)
		if projectChatStatusAllowed(assignment.Status, projectChatPromptAssignmentStatuses) {
			filteredAssignments = append(filteredAssignments, assignment)
		}
	}
	assignmentsTruncated := len(filteredAssignments) > projectChatPromptAssignmentMaxItems
	if assignmentsTruncated {
		filteredAssignments = filteredAssignments[:projectChatPromptAssignmentMaxItems]
	}
	return projectChatWorkSnapshot{
		WorkItems:            filteredWorkItems,
		Assignments:          filteredAssignments,
		WorkItemsTruncated:   workItemsTruncated,
		AssignmentsTruncated: assignmentsTruncated,
	}, true
}

func (h *Handler) sidecarCairnlineEnabledProjectMemoryEntries(ctx context.Context, projectID string) ([]memory.Entry, bool) {
	projectID = strings.TrimSpace(projectID)
	if !h.projectChatSidecarCairnlineReads() || projectID == "" {
		return nil, false
	}
	items, err := h.cairnlineSidecarProjectMemoryEntryList(ctx, projectID, false)
	if err != nil {
		return nil, true
	}
	return projectMemoryEntriesFromCairnlineSidecar(items), true
}

func (h *Handler) strictEmbeddedCairnlineEnabledProjectMemoryEntries(ctx context.Context, projectID string) ([]memory.Entry, bool) {
	view, ok := h.strictEmbeddedCairnlineProjectChatView(ctx, projectID)
	if !ok {
		return nil, false
	}
	if view == nil {
		return nil, true
	}
	defer view.Close()
	items, err := view.service.ListMemoryEntries(ctx, view.snapshot.Project.ID, true)
	if err != nil {
		return nil, true
	}
	out := make([]memory.Entry, 0, len(items))
	for _, item := range items {
		out = append(out, projectMemoryFromCairnline(item))
	}
	return out, true
}

func projectChatStatusAllowed(status string, allowed []string) bool {
	status = strings.TrimSpace(status)
	for _, item := range allowed {
		if status == item {
			return true
		}
	}
	return false
}
