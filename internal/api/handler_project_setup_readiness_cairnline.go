package api

import (
	"context"
	"time"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
)

func (h *Handler) renderCairnlineProjectSetupReadiness(ctx context.Context, projectID string) (ProjectSetupReadinessResponse, error) {
	service, snapshot, err := h.cairnlineProjectWorkService(ctx, projectID)
	if err != nil {
		return ProjectSetupReadinessResponse{}, err
	}
	roles, err := service.ListRoles(ctx, snapshot.Project.ID)
	if err != nil {
		return ProjectSetupReadinessResponse{}, err
	}
	workItems, err := service.ListWorkItems(ctx, snapshot.Project.ID)
	if err != nil {
		return ProjectSetupReadinessResponse{}, err
	}
	entries, err := service.ListMemoryEntries(ctx, snapshot.Project.ID, true)
	if err != nil {
		return ProjectSetupReadinessResponse{}, err
	}
	candidates, err := service.ListMemoryCandidates(ctx, cairnline.MemoryCandidateFilter{
		ProjectID: snapshot.Project.ID,
		Status:    cairnline.MemoryCandidatePending,
	})
	if err != nil {
		return ProjectSetupReadinessResponse{}, err
	}
	skills, err := service.ListProjectSkills(ctx, snapshot.Project.ID)
	if err != nil {
		return ProjectSetupReadinessResponse{}, err
	}

	project := snapshot.Project
	summary := projectSetupReadinessSummary(
		project,
		projectWorkItemsFromCairnline(workItems),
		projectSetupRolesFromCairnline(roles),
		projectSetupSkillsFromCairnline(skills),
		projectSetupMemoryEntriesFromCairnline(entries),
		projectSetupMemoryCandidatesFromCairnline(candidates),
	)
	setupStarted := summary.EnabledContextSourceCount > 0 ||
		summary.RoleCount > 0 ||
		summary.SkillCount > 0 ||
		summary.SavedMemoryCount > 0 ||
		summary.PendingMemoryCandidateCount > 0
	firstWorkReady := summary.WorkItemCount == 0 && setupStarted
	return ProjectSetupReadinessResponse{
		ProjectID:      project.ID,
		GeneratedAt:    formatOptionalTime(time.Now().UTC()),
		ReadBackend:    "cairnline",
		ShowOnboarding: summary.WorkItemCount == 0 && !setupStarted,
		SetupStarted:   setupStarted,
		FirstWorkReady: firstWorkReady,
		Summary:        summary,
		PrimaryAction:  projectSetupReadinessAction(projectSetupReadinessActionBootstrap, project.ID, "Set up project"),
		Checks:         projectSetupReadinessChecks(project, summary),
	}, nil
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
			ID:                  item.ID,
			ProjectID:           item.ProjectID,
			Name:                item.Name,
			Description:         item.Description,
			Instructions:        item.Instructions,
			DefaultAgentProfile: item.DefaultProfileID,
			DefaultDriverKind:   item.DefaultExecutionMode,
			SkillIDs:            append([]string(nil), item.DefaultSkillIDs...),
		})
	}
	return out
}

func projectSetupSkillsFromCairnline(items []cairnline.ProjectSkill) []projectskills.Skill {
	out := make([]projectskills.Skill, 0, len(items))
	for _, item := range items {
		out = append(out, projectskills.Skill{
			ID:          item.ID,
			ProjectID:   item.ProjectID,
			Title:       item.Title,
			Description: item.Description,
			Path:        item.Path,
			RootID:      item.RootID,
			Format:      item.Format,
			Enabled:     item.Enabled,
			Status:      item.Status,
			TrustLabel:  item.TrustLabel,
		})
	}
	return out
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
