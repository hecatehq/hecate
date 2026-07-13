package api

import (
	"context"
	"time"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
)

func (h *Handler) renderCairnlineProjectSetupReadiness(ctx context.Context, projectID string) (ProjectSetupReadinessResponse, error) {
	view, err := h.cairnlineProjectWorkView(ctx, projectID)
	if err != nil {
		return ProjectSetupReadinessResponse{}, err
	}
	defer view.Close()
	roles, err := view.service.ListRoles(ctx, view.snapshot.Project.ID)
	if err != nil {
		return ProjectSetupReadinessResponse{}, err
	}
	workItems, err := view.service.ListWorkItems(ctx, view.snapshot.Project.ID)
	if err != nil {
		return ProjectSetupReadinessResponse{}, err
	}
	entries, err := view.service.ListMemoryEntries(ctx, view.snapshot.Project.ID, true)
	if err != nil {
		return ProjectSetupReadinessResponse{}, err
	}
	candidates, err := view.service.ListMemoryCandidates(ctx, cairnline.MemoryCandidateFilter{
		ProjectID: view.snapshot.Project.ID,
		Status:    cairnline.MemoryCandidatePending,
	})
	if err != nil {
		return ProjectSetupReadinessResponse{}, err
	}
	skills, err := view.service.ListProjectSkills(ctx, view.snapshot.Project.ID)
	if err != nil {
		return ProjectSetupReadinessResponse{}, err
	}

	return renderCairnlineProjectSetupReadinessFromRows(view.snapshot.Project, roles, workItems, entries, candidates, skills), nil
}

func renderCairnlineProjectSetupReadinessFromRows(project projects.Project, roles []cairnline.Role, workItems []cairnline.WorkItem, entries []cairnline.MemoryEntry, candidates []cairnline.MemoryCandidate, skills []cairnline.ProjectSkill) ProjectSetupReadinessResponse {
	summary := projectSetupReadinessSummary(
		project,
		projectWorkItemsFromCairnline(workItems),
		projectSetupRolesFromCairnline(roles),
		projectSetupSkillsFromCairnline(skills),
		projectSetupMemoryEntriesFromCairnline(entries),
		projectSetupMemoryCandidatesFromCairnline(candidates),
	)
	setupStarted := projectSetupReadinessStarted(summary)
	showOnboarding := summary.WorkItemCount == 0 && !setupStarted
	firstWorkReady := summary.WorkItemCount == 0 && setupStarted
	return ProjectSetupReadinessResponse{
		ProjectID:      project.ID,
		GeneratedAt:    formatOptionalTime(time.Now().UTC()),
		ReadBackend:    "cairnline",
		ShowOnboarding: showOnboarding,
		SetupStarted:   setupStarted,
		FirstWorkReady: firstWorkReady,
		Summary:        summary,
		PrimaryAction:  projectSetupReadinessPrimaryAction(project.ID, summary, showOnboarding, firstWorkReady),
		Checks:         projectSetupReadinessChecks(project, summary),
	}
}

func projectWorkItemsFromCairnline(items []cairnline.WorkItem) []projectwork.WorkItem {
	out := make([]projectwork.WorkItem, 0, len(items))
	for _, item := range items {
		out = append(out, projectWorkItemFromCairnline(item))
	}
	return out
}

func projectSetupRolesFromCairnline(items []cairnline.Role) []projectwork.AgentRoleProfile {
	out := make([]projectwork.AgentRoleProfile, 0, len(items))
	for _, item := range items {
		out = append(out, projectwork.AgentRoleProfile{
			ID:                item.ID,
			ProjectID:         item.ProjectID,
			Name:              item.Name,
			Description:       item.Description,
			Instructions:      item.Instructions,
			DefaultDriverKind: item.DefaultExecutionMode,
			SkillIDs:          append([]string(nil), item.DefaultSkillIDs...),
		})
	}
	return out
}

func projectSetupSkillsFromCairnline(items []cairnline.ProjectSkill) []projectskills.Skill {
	out := make([]projectskills.Skill, 0, len(items))
	for _, item := range items {
		out = append(out, projectskills.Skill{
			ID:                     item.ID,
			ProjectID:              item.ProjectID,
			Title:                  item.Title,
			Description:            item.Description,
			Path:                   item.Path,
			RootID:                 item.RootID,
			Format:                 item.Format,
			SuggestedTools:         append([]string(nil), item.SuggestedTools...),
			RequiredPermissions:    projectSkillRequiredPermissionsFromCairnline(item.RequiredPermissions),
			Enabled:                item.Enabled,
			Status:                 item.Status,
			TrustLabel:             item.TrustLabel,
			SourceContextSourceIDs: append([]string(nil), item.SourceRefs...),
			Warnings:               append([]string(nil), item.Warnings...),
			DiscoveredAt:           item.DiscoveredAt,
			CreatedAt:              item.CreatedAt,
			UpdatedAt:              item.UpdatedAt,
		})
	}
	return out
}

func projectSkillRequiredPermissionsFromCairnline(permissions cairnline.RequiredPermissions) projectskills.RequiredPermissions {
	return projectskills.RequiredPermissions{
		Tools:   cloneBoolPointer(permissions.Tools),
		Writes:  cloneBoolPointer(permissions.Writes),
		Network: cloneBoolPointer(permissions.Network),
	}
}

func cloneBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func projectSetupMemoryEntriesFromCairnline(items []cairnline.MemoryEntry) []memory.Entry {
	out := make([]memory.Entry, 0, len(items))
	for _, item := range items {
		out = append(out, memory.Entry{
			ID:        item.ID,
			ProjectID: item.ProjectID,
			Title:     item.Title,
			Body:      item.Body,
			Enabled:   item.Enabled,
		})
	}
	return out
}

func projectSetupMemoryCandidatesFromCairnline(items []cairnline.MemoryCandidate) []memory.Candidate {
	out := make([]memory.Candidate, 0, len(items))
	for _, item := range items {
		out = append(out, memory.Candidate{
			ID:        item.ID,
			ProjectID: item.ProjectID,
			Title:     item.Title,
			Body:      item.Body,
			Status:    item.Status,
		})
	}
	return out
}
