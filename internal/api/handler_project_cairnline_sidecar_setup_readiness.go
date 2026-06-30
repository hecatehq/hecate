package api

import (
	"context"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/projects"
)

func (h *Handler) renderCairnlineSidecarProjectSetupReadiness(ctx context.Context, projectID string) (ProjectSetupReadinessResponse, error) {
	projectItem, ok, err := h.cairnlineSidecarProject(ctx, projectID)
	if err != nil {
		return ProjectSetupReadinessResponse{}, err
	}
	if !ok {
		return ProjectSetupReadinessResponse{}, projects.ErrNotFound
	}
	project := projectFromCairnlineSidecar(projectItem)
	roles, err := h.cairnlineSidecarProjectRoles(ctx, project.ID)
	if err != nil {
		return ProjectSetupReadinessResponse{}, err
	}
	workItems, err := h.cairnlineSidecarProjectWorkItems(ctx, project.ID)
	if err != nil {
		return ProjectSetupReadinessResponse{}, err
	}
	skills, err := h.cairnlineSidecarProjectSkills(ctx, project.ID)
	if err != nil {
		return ProjectSetupReadinessResponse{}, err
	}
	entries, err := h.cairnlineSidecarProjectMemoryEntries(ctx, project.ID)
	if err != nil {
		return ProjectSetupReadinessResponse{}, err
	}
	candidates, err := h.cairnlineSidecarProjectPendingMemoryCandidates(ctx, project.ID)
	if err != nil {
		return ProjectSetupReadinessResponse{}, err
	}

	summary := projectSetupReadinessSummary(
		project,
		projectWorkItemsFromCairnlineSidecar(workItems),
		projectRolesFromCairnlineSidecar(roles),
		projectSkillsFromCairnlineSidecar(skills),
		projectMemoryEntriesFromCairnlineSidecar(entries),
		projectMemoryCandidatesFromCairnlineSidecar(candidates),
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

func (h *Handler) cairnlineSidecarProjectRoles(ctx context.Context, projectID string) ([]ProjectCairnlineSidecarRoleItem, error) {
	result, err := h.callProjectCairnlineSidecarProjectReadTool(ctx, "roles.list", map[string]string{"project_id": strings.TrimSpace(projectID)})
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, projectCairnlineSidecarReadFailure("roles.list returned a tool-level error: " + strings.TrimSpace(result.Text))
	}
	roles, structuredReady, structuredErr := projectCairnlineSidecarStructuredRoles(result.Result.StructuredContent)
	if structuredErr != nil {
		return nil, projectCairnlineSidecarReadFailure("roles.list structuredContent parse failed: " + structuredErr.Error())
	}
	if !structuredReady {
		return nil, projectCairnlineSidecarReadFailure("roles.list did not return typed structuredContent")
	}
	return roles, nil
}

func (h *Handler) cairnlineSidecarProjectWorkItems(ctx context.Context, projectID string) ([]ProjectCairnlineSidecarWorkItem, error) {
	result, err := h.callProjectCairnlineSidecarProjectReadTool(ctx, "work_items.list", map[string]string{"project_id": strings.TrimSpace(projectID)})
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, projectCairnlineSidecarReadFailure("work_items.list returned a tool-level error: " + strings.TrimSpace(result.Text))
	}
	items, structuredReady, structuredErr := projectCairnlineSidecarStructuredWorkItems(result.Result.StructuredContent)
	if structuredErr != nil {
		return nil, projectCairnlineSidecarReadFailure("work_items.list structuredContent parse failed: " + structuredErr.Error())
	}
	if !structuredReady {
		return nil, projectCairnlineSidecarReadFailure("work_items.list did not return typed structuredContent")
	}
	return items, nil
}

func (h *Handler) cairnlineSidecarProjectSkills(ctx context.Context, projectID string) ([]ProjectCairnlineSidecarSkillItem, error) {
	result, err := h.callProjectCairnlineSidecarProjectReadTool(ctx, "skills.list", map[string]string{"project_id": strings.TrimSpace(projectID)})
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, projectCairnlineSidecarReadFailure("skills.list returned a tool-level error: " + strings.TrimSpace(result.Text))
	}
	skills, structuredReady, structuredErr := projectCairnlineSidecarStructuredSkills(result.Result.StructuredContent)
	if structuredErr != nil {
		return nil, projectCairnlineSidecarReadFailure("skills.list structuredContent parse failed: " + structuredErr.Error())
	}
	if !structuredReady {
		return nil, projectCairnlineSidecarReadFailure("skills.list did not return typed structuredContent")
	}
	return skills, nil
}

func (h *Handler) cairnlineSidecarProjectMemoryEntries(ctx context.Context, projectID string) ([]ProjectCairnlineSidecarMemoryEntryItem, error) {
	return h.cairnlineSidecarProjectMemoryEntryList(ctx, projectID, true)
}

func (h *Handler) cairnlineSidecarProjectMemoryEntryList(ctx context.Context, projectID string, includeDisabled bool) ([]ProjectCairnlineSidecarMemoryEntryItem, error) {
	result, err := h.callProjectCairnlineSidecarProjectReadTool(ctx, "memory_entries.list", map[string]any{
		"project_id":       strings.TrimSpace(projectID),
		"include_disabled": includeDisabled,
	})
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, projectCairnlineSidecarReadFailure("memory_entries.list returned a tool-level error: " + strings.TrimSpace(result.Text))
	}
	entries, structuredReady, structuredErr := projectCairnlineSidecarStructuredMemoryEntries(result.Result.StructuredContent)
	if structuredErr != nil {
		return nil, projectCairnlineSidecarReadFailure("memory_entries.list structuredContent parse failed: " + structuredErr.Error())
	}
	if !structuredReady {
		return nil, projectCairnlineSidecarReadFailure("memory_entries.list did not return typed structuredContent")
	}
	return entries, nil
}

func (h *Handler) cairnlineSidecarProjectPendingMemoryCandidates(ctx context.Context, projectID string) ([]ProjectCairnlineSidecarMemoryCandidateItem, error) {
	return h.cairnlineSidecarProjectMemoryCandidateList(ctx, projectID, "pending", false)
}

func (h *Handler) cairnlineSidecarProjectMemoryCandidateList(ctx context.Context, projectID, status string, includeResolved bool) ([]ProjectCairnlineSidecarMemoryCandidateItem, error) {
	args := map[string]any{
		"project_id": strings.TrimSpace(projectID),
	}
	if strings.TrimSpace(status) != "" {
		args["status"] = strings.TrimSpace(status)
	}
	if includeResolved {
		args["include_resolved"] = true
	}
	result, err := h.callProjectCairnlineSidecarProjectReadTool(ctx, "memory_candidates.list", args)
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, projectCairnlineSidecarReadFailure("memory_candidates.list returned a tool-level error: " + strings.TrimSpace(result.Text))
	}
	candidates, structuredReady, structuredErr := projectCairnlineSidecarStructuredMemoryCandidates(result.Result.StructuredContent)
	if structuredErr != nil {
		return nil, projectCairnlineSidecarReadFailure("memory_candidates.list structuredContent parse failed: " + structuredErr.Error())
	}
	if !structuredReady {
		return nil, projectCairnlineSidecarReadFailure("memory_candidates.list did not return typed structuredContent")
	}
	return candidates, nil
}
