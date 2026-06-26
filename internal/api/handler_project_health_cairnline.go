package api

import (
	"context"
	"strings"
	"time"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/projectwork"
)

func (h *Handler) renderCairnlineProjectHealth(ctx context.Context, projectID string) (ProjectHealthResponse, error) {
	service, snapshot, err := h.cairnlineProjectWorkService(ctx, projectID)
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	activity, err := h.renderCairnlineProjectActivity(ctx, projectID)
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
		evidence, err := service.ListEvidence(ctx, projectID, workItem.ID)
		if err != nil {
			return nil, err
		}
		for _, item := range evidence {
			out = append(out, projectHealthEvidenceFromCairnline(item))
		}
		reviews, err := service.ListReviews(ctx, projectID, workItem.ID)
		if err != nil {
			return nil, err
		}
		for _, item := range reviews {
			out = append(out, projectHealthReviewFromCairnline(item))
		}
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
		if nativeItem, ok := nativeByID[item.ID]; ok {
			out = append(out, nativeItem)
			continue
		}
		out = append(out, projectWorkAssignmentFromCairnline(item))
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
		EvidenceURL:        item.Locator,
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
		ReviewFollowUpRequired: item.Verdict != cairnline.ReviewVerdictPass,
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
		StatusChangedAt:       item.UpdatedAt,
	}
}

func projectHealthReviewVerdictFromCairnline(verdict string) string {
	switch strings.TrimSpace(verdict) {
	case cairnline.ReviewVerdictPass:
		return projectwork.ReviewVerdictApproved
	case cairnline.ReviewVerdictBlocked:
		return projectwork.ReviewVerdictBlocked
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
