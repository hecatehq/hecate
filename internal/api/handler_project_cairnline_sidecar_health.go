package api

import (
	"context"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/projects"
)

func (h *Handler) renderCairnlineSidecarProjectHealth(ctx context.Context, projectID string) (ProjectHealthResponse, error) {
	projectItem, ok, err := h.cairnlineSidecarProject(ctx, projectID)
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	if !ok {
		return ProjectHealthResponse{}, projects.ErrNotFound
	}
	project := projectFromCairnlineSidecar(projectItem)
	roles, err := h.cairnlineSidecarProjectRoles(ctx, project.ID)
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	workItems, err := h.cairnlineSidecarProjectWorkItems(ctx, project.ID)
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	assignments, err := h.cairnlineSidecarProjectAssignments(ctx, project.ID)
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	artifacts, err := h.cairnlineSidecarProjectArtifacts(ctx, project.ID)
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	evidence, err := h.cairnlineSidecarProjectEvidence(ctx, project.ID)
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	reviews, err := h.cairnlineSidecarProjectReviews(ctx, project.ID)
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	handoffs, err := h.cairnlineSidecarProjectHandoffs(ctx, project.ID)
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	memoryEntries, err := h.cairnlineSidecarProjectMemoryEntries(ctx, project.ID)
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	memoryCandidates, err := h.cairnlineSidecarProjectMemoryCandidates(ctx, project.ID)
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	profiles, err := h.cairnlineSidecarAgentProfiles(ctx)
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	skills, err := h.cairnlineSidecarProjectSkills(ctx, project.ID)
	if err != nil {
		return ProjectHealthResponse{}, err
	}

	now := time.Now().UTC()
	activity := ProjectActivityDataResponse{
		ProjectID:   project.ID,
		ReadBackend: "cairnline",
	}
	convertedWorkItems := projectWorkItemsFromCairnlineSidecar(workItems)
	convertedAssignments := projectAssignmentsFromCairnlineSidecar(assignments)
	convertedHandoffs := projectHandoffsFromCairnlineSidecar(handoffs)
	convertedArtifacts := projectArtifactsFromCairnlineSidecar(artifacts, evidence, reviews)
	convertedMemoryEntries := projectMemoryEntriesFromCairnlineSidecar(memoryEntries)
	convertedMemoryCandidates := projectMemoryCandidatesFromCairnlineSidecar(memoryCandidates)
	staleAssignments := projectHealthStaleAssignments(project, activity, convertedWorkItems, convertedAssignments, now)
	summary := projectHealthSummary(project, convertedMemoryEntries, convertedMemoryCandidates, convertedHandoffs, convertedArtifacts, staleAssignments)
	attention := projectHealthAttentionItems(
		project,
		activity,
		convertedWorkItems,
		projectRolesFromCairnlineSidecar(roles),
		convertedHandoffs,
		convertedArtifacts,
		convertedMemoryEntries,
		convertedMemoryCandidates,
		projectAgentProfilesFromCairnlineSidecar(profiles),
		projectSkillsFromCairnlineSidecar(skills),
		staleAssignments,
	)
	availableAttentionCount := len(attention)
	attention = boundedProjectHealthAttention(attention, projectHealthAttentionLimit)
	summary.AttentionCount = len(attention)
	summary.AvailableAttentionCount = availableAttentionCount
	summary.OmittedAttentionCount = availableAttentionCount - len(attention)
	summary.AttentionLimit = projectHealthAttentionLimit
	return ProjectHealthResponse{
		ProjectID:   project.ID,
		GeneratedAt: formatOptionalTime(now),
		ReadBackend: "cairnline",
		Summary:     summary,
		Attention:   attention,
	}, nil
}

func (h *Handler) cairnlineSidecarProjectAssignments(ctx context.Context, projectID string) ([]ProjectCairnlineSidecarAssignmentItem, error) {
	result, err := h.callProjectCairnlineSidecarProjectReadTool(ctx, "assignments.list", map[string]string{"project_id": strings.TrimSpace(projectID)})
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, projectCairnlineSidecarReadFailure("assignments.list returned a tool-level error: " + strings.TrimSpace(result.Text))
	}
	assignments, structuredReady, structuredErr := projectCairnlineSidecarStructuredAssignments(result.Result.StructuredContent)
	if structuredErr != nil {
		return nil, projectCairnlineSidecarReadFailure("assignments.list structuredContent parse failed: " + structuredErr.Error())
	}
	if !structuredReady {
		return nil, projectCairnlineSidecarReadFailure("assignments.list did not return typed structuredContent")
	}
	return assignments, nil
}

func (h *Handler) cairnlineSidecarProjectArtifacts(ctx context.Context, projectID string) ([]ProjectCairnlineSidecarArtifactItem, error) {
	result, err := h.callProjectCairnlineSidecarProjectReadTool(ctx, "artifacts.list", map[string]string{"project_id": strings.TrimSpace(projectID)})
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, projectCairnlineSidecarReadFailure("artifacts.list returned a tool-level error: " + strings.TrimSpace(result.Text))
	}
	artifacts, structuredReady, structuredErr := projectCairnlineSidecarStructuredArtifacts(result.Result.StructuredContent)
	if structuredErr != nil {
		return nil, projectCairnlineSidecarReadFailure("artifacts.list structuredContent parse failed: " + structuredErr.Error())
	}
	if !structuredReady {
		return nil, projectCairnlineSidecarReadFailure("artifacts.list did not return typed structuredContent")
	}
	return artifacts, nil
}

func (h *Handler) cairnlineSidecarProjectEvidence(ctx context.Context, projectID string) ([]ProjectCairnlineSidecarEvidenceItem, error) {
	result, err := h.callProjectCairnlineSidecarProjectReadTool(ctx, "evidence.list", map[string]string{"project_id": strings.TrimSpace(projectID)})
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, projectCairnlineSidecarReadFailure("evidence.list returned a tool-level error: " + strings.TrimSpace(result.Text))
	}
	evidence, structuredReady, structuredErr := projectCairnlineSidecarStructuredEvidence(result.Result.StructuredContent)
	if structuredErr != nil {
		return nil, projectCairnlineSidecarReadFailure("evidence.list structuredContent parse failed: " + structuredErr.Error())
	}
	if !structuredReady {
		return nil, projectCairnlineSidecarReadFailure("evidence.list did not return typed structuredContent")
	}
	return evidence, nil
}

func (h *Handler) cairnlineSidecarProjectReviews(ctx context.Context, projectID string) ([]ProjectCairnlineSidecarReviewItem, error) {
	result, err := h.callProjectCairnlineSidecarProjectReadTool(ctx, "reviews.list", map[string]string{"project_id": strings.TrimSpace(projectID)})
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, projectCairnlineSidecarReadFailure("reviews.list returned a tool-level error: " + strings.TrimSpace(result.Text))
	}
	reviews, structuredReady, structuredErr := projectCairnlineSidecarStructuredReviews(result.Result.StructuredContent)
	if structuredErr != nil {
		return nil, projectCairnlineSidecarReadFailure("reviews.list structuredContent parse failed: " + structuredErr.Error())
	}
	if !structuredReady {
		return nil, projectCairnlineSidecarReadFailure("reviews.list did not return typed structuredContent")
	}
	return reviews, nil
}

func (h *Handler) cairnlineSidecarProjectHandoffs(ctx context.Context, projectID string) ([]ProjectCairnlineSidecarHandoffItem, error) {
	result, err := h.callProjectCairnlineSidecarProjectReadTool(ctx, "handoffs.list", map[string]string{"project_id": strings.TrimSpace(projectID)})
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, projectCairnlineSidecarReadFailure("handoffs.list returned a tool-level error: " + strings.TrimSpace(result.Text))
	}
	handoffs, structuredReady, structuredErr := projectCairnlineSidecarStructuredHandoffs(result.Result.StructuredContent)
	if structuredErr != nil {
		return nil, projectCairnlineSidecarReadFailure("handoffs.list structuredContent parse failed: " + structuredErr.Error())
	}
	if !structuredReady {
		return nil, projectCairnlineSidecarReadFailure("handoffs.list did not return typed structuredContent")
	}
	return handoffs, nil
}

func (h *Handler) cairnlineSidecarProjectMemoryCandidates(ctx context.Context, projectID string) ([]ProjectCairnlineSidecarMemoryCandidateItem, error) {
	result, err := h.callProjectCairnlineSidecarProjectReadTool(ctx, "memory_candidates.list", map[string]any{
		"project_id":       strings.TrimSpace(projectID),
		"include_resolved": true,
	})
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

func (h *Handler) cairnlineSidecarAgentProfiles(ctx context.Context) ([]ProjectCairnlineSidecarAgentProfileItem, error) {
	result, err := h.callProjectCairnlineSidecarProjectReadTool(ctx, "profiles.list", map[string]string{})
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, projectCairnlineSidecarReadFailure("profiles.list returned a tool-level error: " + strings.TrimSpace(result.Text))
	}
	profiles, structuredReady, structuredErr := projectCairnlineSidecarStructuredAgentProfiles(result.Result.StructuredContent)
	if structuredErr != nil {
		return nil, projectCairnlineSidecarReadFailure("profiles.list structuredContent parse failed: " + structuredErr.Error())
	}
	if !structuredReady {
		return nil, projectCairnlineSidecarReadFailure("profiles.list did not return typed structuredContent")
	}
	return profiles, nil
}
