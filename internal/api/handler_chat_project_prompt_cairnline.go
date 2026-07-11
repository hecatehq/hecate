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
	out := make([]projectwork.AgentRoleProfile, 0, len(roles))
	nativeByID := projectWorkRolesByID(view.snapshot.Roles)
	for _, role := range roles {
		out = append(out, projectWorkRoleFromCairnline(role, nativeByID[role.ID]))
	}
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
