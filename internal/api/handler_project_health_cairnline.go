package api

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/cairnlinebridge"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectwork"
)

func (h *Handler) renderCairnlineProjectHealth(ctx context.Context, projectID string) (ProjectHealthResponse, error) {
	if h.requiresEmbeddedCairnlineProjectReads() {
		return h.renderStrictEmbeddedCairnlineProjectHealth(ctx, projectID)
	}
	view, err := h.cairnlineProjectWorkView(ctx, projectID)
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	defer view.Close()
	return h.renderCairnlineProjectHealthFromService(ctx, view.service, view.snapshot)
}

func (h *Handler) renderStrictEmbeddedCairnlineProjectHealth(ctx context.Context, projectID string) (ProjectHealthResponse, error) {
	_, service, store, err := h.openCairnlineEmbeddedService(ctx)
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	defer store.Close()
	item, err := service.GetProject(ctx, projectID)
	if errors.Is(err, cairnline.ErrNotFound) {
		return ProjectHealthResponse{}, projects.ErrNotFound
	}
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	executionProfile, err := cairnlineExecutionProfileByID(ctx, service, item.DefaultExecutionProfileID)
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	return h.renderCairnlineProjectHealthFromService(ctx, service, cairnlinebridge.Snapshot{
		Project: projectFromCairnline(item, executionProfile, projects.Project{}),
	})
}

func (h *Handler) renderCairnlineProjectHealthFromService(ctx context.Context, service *cairnline.Service, snapshot cairnlinebridge.Snapshot) (ProjectHealthResponse, error) {
	activity, err := h.renderCairnlineProjectActivityFromService(ctx, service, snapshot)
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	roles, err := service.ListRoles(ctx, snapshot.Project.ID)
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	workItems, err := service.ListWorkItems(ctx, snapshot.Project.ID)
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	assignments, err := service.ListAssignments(ctx, snapshot.Project.ID)
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	handoffs, err := projectHealthCairnlineHandoffs(ctx, service, snapshot.Project.ID, workItems)
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	artifacts, err := projectHealthCairnlineArtifacts(ctx, service, snapshot.Project.ID, workItems)
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	memoryEntries, err := service.ListMemoryEntries(ctx, snapshot.Project.ID, true)
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	memoryCandidates, err := service.ListMemoryCandidates(ctx, cairnline.MemoryCandidateFilter{
		ProjectID:       snapshot.Project.ID,
		IncludeResolved: true,
	})
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	profiles, err := service.ListAgentProfiles(ctx)
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	skills, err := service.ListProjectSkills(ctx, snapshot.Project.ID)
	if err != nil {
		return ProjectHealthResponse{}, err
	}

	now := time.Now().UTC()
	project := snapshot.Project
	convertedWorkItems := projectWorkItemsFromCairnline(workItems)
	convertedAssignments := projectWorkAssignmentsFromCairnline(assignments, snapshot.Assignments)
	staleAssignments := projectHealthStaleAssignments(project, activity, convertedWorkItems, convertedAssignments, now)
	summary := projectHealthSummary(
		project,
		projectSetupMemoryEntriesFromCairnline(memoryEntries),
		projectSetupMemoryCandidatesFromCairnline(memoryCandidates),
		handoffs,
		artifacts,
		staleAssignments,
	)
	attention := projectHealthAttentionItems(
		project,
		activity,
		convertedWorkItems,
		projectSetupRolesFromCairnline(roles),
		handoffs,
		artifacts,
		projectSetupMemoryEntriesFromCairnline(memoryEntries),
		projectSetupMemoryCandidatesFromCairnline(memoryCandidates),
		projectHealthProfilesFromCairnline(profiles),
		projectSetupSkillsFromCairnline(skills),
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

func projectHealthCairnlineArtifacts(ctx context.Context, service *cairnline.Service, projectID string, workItems []cairnline.WorkItem) ([]projectwork.CollaborationArtifact, error) {
	out := make([]projectwork.CollaborationArtifact, 0)
	for _, workItem := range workItems {
		artifacts, err := cairnlineProjectWorkArtifacts(ctx, service, projectID, workItem.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, artifacts...)
	}
	return out, nil
}

func projectHealthCairnlineHandoffs(ctx context.Context, service *cairnline.Service, projectID string, workItems []cairnline.WorkItem) ([]projectwork.Handoff, error) {
	out := make([]projectwork.Handoff, 0)
	for _, workItem := range workItems {
		items, err := service.ListHandoffs(ctx, projectID, workItem.ID)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			out = append(out, projectHealthHandoffFromCairnline(item))
		}
	}
	return out, nil
}

func projectWorkAssignmentsFromCairnline(items []cairnline.Assignment, native []projectwork.Assignment) []projectwork.Assignment {
	nativeByID := projectWorkAssignmentsByID(native)
	out := make([]projectwork.Assignment, 0, len(items))
	for _, item := range items {
		projected := projectWorkAssignmentFromCairnline(item)
		if nativeItem, ok := nativeByID[item.ID]; ok {
			projected.ExecutionRef = nativeItem.ExecutionRef
			projected.ContextPacket = append([]byte(nil), nativeItem.ContextPacket...)
			if !nativeItem.CreatedAt.IsZero() {
				projected.CreatedAt = nativeItem.CreatedAt
			}
			if !nativeItem.UpdatedAt.IsZero() {
				projected.UpdatedAt = nativeItem.UpdatedAt
			}
			if !nativeItem.StartedAt.IsZero() {
				projected.StartedAt = nativeItem.StartedAt
			}
			if !nativeItem.CompletedAt.IsZero() {
				projected.CompletedAt = nativeItem.CompletedAt
			}
		}
		out = append(out, projected)
	}
	return out
}

func projectHealthProfilesFromCairnline(items []cairnline.AgentProfile) []agentprofiles.Profile {
	out := make([]agentprofiles.Profile, 0, len(items))
	for _, item := range items {
		out = append(out, agentprofiles.Profile{
			ID:       item.ID,
			Name:     item.Name,
			SkillIDs: append([]string(nil), item.SkillIDs...),
		})
	}
	return out
}

func projectHealthEvidenceFromCairnline(item cairnline.Evidence) projectwork.CollaborationArtifact {
	return projectwork.CollaborationArtifact{
		ID:                 item.ID,
		ProjectID:          item.ProjectID,
		WorkItemID:         item.WorkItemID,
		AssignmentID:       item.AssignmentID,
		Kind:               projectwork.ArtifactKindEvidenceLink,
		Title:              item.Title,
		Body:               item.Body,
		EvidenceSourceKind: firstNonEmptyString(strings.TrimSpace(item.SourceKind), projectwork.EvidenceSourceExternal),
		EvidenceURL:        item.Locator,
		EvidenceExternalID: item.ExternalID,
		EvidenceProvider:   item.Provider,
		EvidenceTrustLabel: item.TrustLabel,
		CreatedAt:          item.CreatedAt,
		UpdatedAt:          item.UpdatedAt,
	}
}

func projectHealthReviewFromCairnline(item cairnline.Review) projectwork.CollaborationArtifact {
	return projectwork.CollaborationArtifact{
		ID:                     item.ID,
		ProjectID:              item.ProjectID,
		WorkItemID:             item.WorkItemID,
		AssignmentID:           item.AssignmentID,
		Kind:                   projectwork.ArtifactKindReview,
		Title:                  item.Title,
		Body:                   item.Body,
		AuthorRoleID:           item.ReviewerRoleID,
		ReviewedAssignmentID:   item.AssignmentID,
		ReviewVerdict:          projectHealthReviewVerdictFromCairnline(item.Verdict),
		ReviewRisk:             projectHealthReviewRiskFromCairnline(item.Risk),
		ReviewFollowUpRequired: projectHealthReviewRequiresFollowUpFromCairnline(item.Verdict),
		CreatedAt:              item.CreatedAt,
		UpdatedAt:              item.UpdatedAt,
	}
}

func projectHealthHandoffFromCairnline(item cairnline.Handoff) projectwork.Handoff {
	return projectwork.Handoff{
		ID:                    item.ID,
		ProjectID:             item.ProjectID,
		WorkItemID:            item.WorkItemID,
		SourceAssignmentID:    item.SourceAssignmentID,
		SourceRunID:           item.SourceRunID,
		SourceChatSessionID:   item.SourceChatSessionID,
		SourceMessageID:       item.SourceMessageID,
		TargetRoleID:          item.ToRoleID,
		TargetAssignmentID:    item.TargetAssignmentID,
		TargetWorkItemID:      item.TargetWorkItemID,
		Title:                 item.Title,
		Summary:               item.Body,
		RecommendedNextAction: item.RecommendedNextAction,
		LinkedArtifactIDs:     append([]string(nil), item.LinkedArtifactIDs...),
		LinkedMemoryIDs:       append([]string(nil), item.LinkedMemoryIDs...),
		ContextRefs:           append([]string(nil), item.ContextRefs...),
		Status:                projectHealthHandoffStatusFromCairnline(item.Status),
		ProvenanceKind:        item.ProvenanceKind,
		TrustLabel:            item.TrustLabel,
		CreatedByRoleID:       item.FromRoleID,
		CreatedAt:             item.CreatedAt,
		UpdatedAt:             item.UpdatedAt,
		StatusChangedAt:       projectworkTime(item.StatusChangedAt, item.UpdatedAt),
	}
}

func projectHealthReviewVerdictFromCairnline(verdict string) string {
	switch strings.TrimSpace(verdict) {
	case cairnline.ReviewVerdictApproved:
		return projectwork.ReviewVerdictApproved
	case cairnline.ReviewVerdictChangesRequested:
		return projectwork.ReviewVerdictChangesRequested
	case cairnline.ReviewVerdictBlocked:
		return projectwork.ReviewVerdictBlocked
	case cairnline.ReviewVerdictRisk:
		return projectwork.ReviewVerdictRisk
	default:
		return projectwork.ReviewVerdictChangesRequested
	}
}

func projectHealthReviewRiskFromCairnline(risk string) string {
	switch strings.TrimSpace(risk) {
	case projectwork.ReviewRiskLow, projectwork.ReviewRiskMedium, projectwork.ReviewRiskHigh:
		return strings.TrimSpace(risk)
	default:
		return projectwork.ReviewRiskUnknown
	}
}

func projectHealthReviewRequiresFollowUpFromCairnline(verdict string) bool {
	switch strings.TrimSpace(verdict) {
	case cairnline.ReviewVerdictChangesRequested, cairnline.ReviewVerdictBlocked:
		return true
	default:
		return false
	}
}

func projectHealthHandoffStatusFromCairnline(status string) string {
	switch strings.TrimSpace(status) {
	case cairnline.HandoffStatusAccepted:
		return projectwork.HandoffStatusAccepted
	case cairnline.HandoffStatusSuperseded:
		return projectwork.HandoffStatusSuperseded
	case cairnline.HandoffStatusDismissed:
		return projectwork.HandoffStatusDismissed
	default:
		return projectwork.HandoffStatusPending
	}
}
