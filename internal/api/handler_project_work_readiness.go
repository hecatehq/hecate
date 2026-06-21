package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectwork"
)

type ProjectWorkItemReadinessEnvelope struct {
	Object string                           `json:"object"`
	Data   ProjectWorkItemReadinessResponse `json:"data"`
}

type ProjectWorkItemReadinessResponse struct {
	ProjectID                    string                                  `json:"project_id"`
	WorkItemID                   string                                  `json:"work_item_id"`
	Ready                        bool                                    `json:"ready"`
	Status                       string                                  `json:"status"`
	Title                        string                                  `json:"title"`
	Detail                       string                                  `json:"detail"`
	Blockers                     []string                                `json:"blockers"`
	Warnings                     []string                                `json:"warnings"`
	AssignmentCount              int                                     `json:"assignment_count"`
	CompletedAssignments         int                                     `json:"completed_assignments"`
	ReviewFollowUpCount          int                                     `json:"review_follow_up_count"`
	ReviewFollowUpArtifactIDs    []string                                `json:"review_follow_up_artifact_ids,omitempty"`
	ReviewFollowUps              []ProjectWorkItemReviewFollowUpResponse `json:"review_follow_ups,omitempty"`
	MissingEvidenceAssignmentIDs []string                                `json:"missing_evidence_assignment_ids,omitempty"`
}

type ProjectWorkItemReviewFollowUpResponse struct {
	ArtifactID           string `json:"artifact_id"`
	Title                string `json:"title"`
	Status               string `json:"status"`
	Blocker              string `json:"blocker,omitempty"`
	ReviewedAssignmentID string `json:"reviewed_assignment_id,omitempty"`
	ReviewVerdict        string `json:"review_verdict,omitempty"`
	ReviewRisk           string `json:"review_risk,omitempty"`
}

func (h *Handler) HandleProjectWorkItemReadiness(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if !h.requireProject(w, r, projectID) {
		return
	}
	readiness, err := h.renderProjectWorkItemReadiness(r.Context(), projectID, r.PathValue("work_item_id"))
	if err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			WriteError(w, http.StatusNotFound, errCodeNotFound, "work item not found")
			return
		}
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, ProjectWorkItemReadinessEnvelope{Object: "project_work_item_readiness", Data: readiness})
}

func (h *Handler) renderProjectWorkItemReadiness(ctx context.Context, projectID, workItemID string) (ProjectWorkItemReadinessResponse, error) {
	if _, ok, err := h.projects.Get(ctx, projectID); err != nil {
		return ProjectWorkItemReadinessResponse{}, err
	} else if !ok {
		return ProjectWorkItemReadinessResponse{}, projects.ErrNotFound
	}
	workItem, ok, err := h.projectWork.GetWorkItem(ctx, projectID, workItemID)
	if err != nil {
		return ProjectWorkItemReadinessResponse{}, err
	}
	if !ok {
		return ProjectWorkItemReadinessResponse{}, projects.ErrNotFound
	}
	assignments, err := h.projectWork.ListAssignments(ctx, projectwork.AssignmentFilter{ProjectID: projectID, WorkItemID: workItemID})
	if err != nil {
		return ProjectWorkItemReadinessResponse{}, err
	}
	artifacts, err := h.projectWork.ListArtifacts(ctx, projectwork.ArtifactFilter{ProjectID: projectID, WorkItemID: workItemID})
	if err != nil {
		return ProjectWorkItemReadinessResponse{}, err
	}
	handoffs, err := h.projectWork.ListHandoffs(ctx, projectwork.HandoffFilter{ProjectID: projectID, WorkItemID: workItemID})
	if err != nil {
		return ProjectWorkItemReadinessResponse{}, err
	}
	return renderProjectWorkItemReadiness(workItem, assignments, artifacts, handoffs), nil
}

func renderProjectWorkItemReadiness(workItem projectwork.WorkItem, assignments []projectwork.Assignment, artifacts []projectwork.CollaborationArtifact, handoffs []projectwork.Handoff) ProjectWorkItemReadinessResponse {
	statuses := make([]string, 0, len(assignments))
	assignmentsByID := projectWorkReadinessAssignmentsByID(assignments)
	closed := projectWorkItemClosed(workItem.Status)
	readiness := ProjectWorkItemReadinessResponse{
		ProjectID:            workItem.ProjectID,
		WorkItemID:           workItem.ID,
		Status:               "ready",
		Title:                "Ready to mark done",
		Detail:               "Assignments, evidence, handoffs, and review follow-up are clear. The operator can mark this work item done.",
		AssignmentCount:      len(assignments),
		CompletedAssignments: 0,
	}

	for _, assignment := range assignments {
		status := projectWorkReadinessAssignmentStatus(assignment)
		statuses = append(statuses, status)
		if status != projectwork.AssignmentStatusCompleted {
			continue
		}
		readiness.CompletedAssignments++
		if !closed && !projectWorkReadinessAssignmentHasEvidence(assignment, artifacts) {
			readiness.MissingEvidenceAssignmentIDs = append(readiness.MissingEvidenceAssignmentIDs, assignment.ID)
		}
	}
	if closed {
		readiness.Status = "done"
		readiness.Title = "Work item is done"
		readiness.Detail = "This work item has already been marked done by the operator."
		return readiness
	}

	activeAssignments := projectWorkReadinessStatusCount(statuses, projectWorkReadinessActiveAssignmentStatus)
	failedAssignments := projectWorkReadinessStatusCount(statuses, func(status string) bool {
		return status == projectwork.AssignmentStatusFailed
	})
	cancelledAssignments := projectWorkReadinessStatusCount(statuses, func(status string) bool {
		return status == projectwork.AssignmentStatusCancelled
	})
	unresolvedAssignments := projectWorkReadinessStatusCount(statuses, projectWorkReadinessUnresolvedAssignmentStatus)
	pendingHandoffs := 0
	for _, handoff := range handoffs {
		if handoff.Status == projectwork.HandoffStatusPending {
			pendingHandoffs++
		}
	}
	if activeAssignments > 0 {
		readiness.Blockers = append(readiness.Blockers, projectWorkReadinessPlural(activeAssignments, "assignment is still active", "assignments are still active"))
	}
	if failedAssignments > 0 {
		readiness.Blockers = append(readiness.Blockers, projectWorkReadinessPlural(failedAssignments, "assignment failed", "assignments failed"))
	}
	if cancelledAssignments > 0 {
		readiness.Blockers = append(readiness.Blockers, projectWorkReadinessPlural(cancelledAssignments, "assignment was cancelled", "assignments were cancelled"))
	}
	if unresolvedAssignments > 0 {
		readiness.Blockers = append(readiness.Blockers, projectWorkReadinessPlural(unresolvedAssignments, "assignment is not complete", "assignments are not complete"))
	}
	if pendingHandoffs > 0 {
		readiness.Blockers = append(readiness.Blockers, projectWorkReadinessPlural(pendingHandoffs, "handoff is pending", "handoffs are pending"))
	}
	if len(readiness.MissingEvidenceAssignmentIDs) > 0 {
		readiness.Blockers = append(readiness.Blockers, projectWorkReadinessPlural(len(readiness.MissingEvidenceAssignmentIDs), "completed assignment is missing evidence", "completed assignments are missing evidence"))
	}
	if len(assignments) == 0 {
		readiness.Warnings = append(readiness.Warnings, "No assignments are linked to this work item; closeout is manual.")
	}
	for _, artifact := range artifacts {
		if projectWorkReadinessReviewArtifactNeedsPath(artifact, handoffs) {
			blocker := projectWorkReadinessReviewFollowUpBlocker(artifact, handoffs, assignmentsByID)
			readiness.ReviewFollowUpArtifactIDs = append(readiness.ReviewFollowUpArtifactIDs, artifact.ID)
			readiness.ReviewFollowUps = append(readiness.ReviewFollowUps, renderProjectWorkItemReviewFollowUp(artifact, blocker))
		}
		if blocker := projectWorkReadinessReviewFollowUpBlocker(artifact, handoffs, assignmentsByID); blocker != "" {
			readiness.Blockers = append(readiness.Blockers, blocker)
		}
	}
	readiness.ReviewFollowUpCount = len(readiness.ReviewFollowUpArtifactIDs)
	readiness.Blockers = projectWorkReadinessUniqueStrings(readiness.Blockers)
	readiness.Warnings = projectWorkReadinessUniqueStrings(readiness.Warnings)
	if len(readiness.Blockers) > 0 {
		readiness.Status = "blocked"
		readiness.Title = "Closeout is blocked"
		readiness.Detail = "Resolve the listed assignment, evidence, handoff, or review follow-up items before marking this work done."
	}
	readiness.Ready = readiness.Status == "ready"
	return readiness
}

func renderProjectWorkItemReviewFollowUp(artifact projectwork.CollaborationArtifact, blocker string) ProjectWorkItemReviewFollowUpResponse {
	return ProjectWorkItemReviewFollowUpResponse{
		ArtifactID:           artifact.ID,
		Title:                firstNonEmpty(artifact.Title, artifact.ID),
		Status:               "needs_path",
		Blocker:              strings.TrimSpace(blocker),
		ReviewedAssignmentID: artifact.ReviewedAssignmentID,
		ReviewVerdict:        artifact.ReviewVerdict,
		ReviewRisk:           artifact.ReviewRisk,
	}
}

func projectWorkReadinessReviewFollowUpArtifact(artifacts []projectwork.CollaborationArtifact, handoffs []projectwork.Handoff) *projectwork.CollaborationArtifact {
	items := append([]projectwork.CollaborationArtifact(nil), artifacts...)
	sort.SliceStable(items, func(i, j int) bool {
		left, right := projectworkTime(items[i].UpdatedAt, items[i].CreatedAt), projectworkTime(items[j].UpdatedAt, items[j].CreatedAt)
		if !left.Equal(right) {
			return left.After(right)
		}
		return items[i].ID < items[j].ID
	})
	for i := range items {
		if projectWorkReadinessReviewArtifactNeedsPath(items[i], handoffs) {
			return &items[i]
		}
	}
	return nil
}

func projectWorkReadinessReviewArtifactNeedsPath(artifact projectwork.CollaborationArtifact, handoffs []projectwork.Handoff) bool {
	return projectwork.ReviewArtifactNeedsFollowUpPath(artifact, handoffs)
}

func projectWorkReadinessReviewArtifactRequiresFollowUp(artifact projectwork.CollaborationArtifact) bool {
	return projectwork.ReviewArtifactRequiresFollowUp(artifact)
}

func projectWorkReadinessArtifactHasLinkedFollowUpPath(artifactID string, handoffs []projectwork.Handoff) bool {
	return projectwork.ReviewArtifactHasLinkedFollowUpPath(artifactID, handoffs)
}

func projectWorkReadinessReviewFollowUpBlocker(artifact projectwork.CollaborationArtifact, handoffs []projectwork.Handoff, assignmentsByID map[string]projectwork.Assignment) string {
	if !projectWorkReadinessReviewArtifactRequiresFollowUp(artifact) {
		return ""
	}
	title := firstNonEmpty(artifact.Title, artifact.ID)
	linked := make([]projectwork.Handoff, 0)
	for _, handoff := range handoffs {
		for _, artifactID := range handoff.LinkedArtifactIDs {
			if strings.TrimSpace(artifactID) == artifact.ID {
				linked = append(linked, handoff)
				break
			}
		}
	}
	if len(linked) == 0 {
		return fmt.Sprintf("Review follow-up %q is not triaged", title)
	}
	hasTargetAssignment := false
	hasCompletedTarget := false
	hasDismissedOrSuperseded := false
	for _, handoff := range linked {
		if handoff.Status == projectwork.HandoffStatusPending {
			return fmt.Sprintf("Review follow-up %q has a pending handoff", title)
		}
		if handoff.Status == projectwork.HandoffStatusDismissed || handoff.Status == projectwork.HandoffStatusSuperseded {
			hasDismissedOrSuperseded = true
		}
		if strings.TrimSpace(handoff.TargetAssignmentID) == "" {
			continue
		}
		hasTargetAssignment = true
		if assignment, ok := assignmentsByID[handoff.TargetAssignmentID]; ok {
			hasCompletedTarget = hasCompletedTarget || projectWorkReadinessAssignmentStatus(assignment) == projectwork.AssignmentStatusCompleted
		}
	}
	if hasCompletedTarget {
		return ""
	}
	if hasTargetAssignment {
		return fmt.Sprintf("Review follow-up %q assignment is not completed", title)
	}
	if hasDismissedOrSuperseded {
		return ""
	}
	return fmt.Sprintf("Review follow-up %q is not triaged", title)
}

func projectWorkReadinessAssignmentStatus(assignment projectwork.Assignment) string {
	return firstNonEmpty(assignment.ExecutionRef.Status, assignment.Status)
}

func projectWorkReadinessActiveAssignmentStatus(status string) bool {
	return status == projectwork.AssignmentStatusQueued || status == projectwork.AssignmentStatusRunning || status == projectwork.AssignmentStatusAwaitingApproval
}

func projectWorkReadinessUnresolvedAssignmentStatus(status string) bool {
	return status != projectwork.AssignmentStatusCompleted &&
		status != projectwork.AssignmentStatusFailed &&
		status != projectwork.AssignmentStatusCancelled &&
		!projectWorkReadinessActiveAssignmentStatus(status)
}

func projectWorkReadinessAssignmentHasEvidence(assignment projectwork.Assignment, artifacts []projectwork.CollaborationArtifact) bool {
	for _, artifact := range artifacts {
		if artifact.Kind != projectwork.ArtifactKindEvidenceLink {
			continue
		}
		if artifact.AssignmentID == "" || artifact.AssignmentID == assignment.ID {
			return true
		}
	}
	return false
}

func projectWorkReadinessStatusCount(statuses []string, predicate func(string) bool) int {
	count := 0
	for _, status := range statuses {
		if predicate(status) {
			count++
		}
	}
	return count
}

func projectWorkReadinessAssignmentsByID(assignments []projectwork.Assignment) map[string]projectwork.Assignment {
	byID := make(map[string]projectwork.Assignment, len(assignments))
	for _, assignment := range assignments {
		byID[assignment.ID] = assignment
	}
	return byID
}

func projectWorkReadinessPlural(count int, singular, plural string) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", singular)
	}
	return fmt.Sprintf("%d %s", count, plural)
}

func projectWorkReadinessUniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}
