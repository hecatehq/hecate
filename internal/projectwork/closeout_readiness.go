package projectwork

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

var ErrWorkItemCloseoutBlocked = errors.New("project work item closeout blocked")

type WorkItemReadiness struct {
	ProjectID                    string
	WorkItemID                   string
	Ready                        bool
	Status                       string
	Title                        string
	Detail                       string
	Blockers                     []string
	Warnings                     []string
	AssignmentCount              int
	CompletedAssignments         int
	ReviewFollowUpCount          int
	ReviewFollowUpArtifactIDs    []string
	ReviewFollowUps              []ReviewFollowUpReadiness
	MissingEvidenceAssignmentIDs []string
	OpenHandoffIDs               []string
}

type ReviewFollowUpReadiness struct {
	ArtifactID           string
	Title                string
	Status               string
	Blocker              string
	ReviewedAssignmentID string
	ReviewVerdict        string
	ReviewRisk           string
}

type WorkItemCloseoutBlockedError struct {
	Readiness WorkItemReadiness
}

func (e WorkItemCloseoutBlockedError) Error() string {
	return ErrWorkItemCloseoutBlocked.Error()
}

func (e WorkItemCloseoutBlockedError) Unwrap() error {
	return ErrWorkItemCloseoutBlocked
}

func EvaluateWorkItemReadiness(workItem WorkItem, assignments []Assignment, artifacts []CollaborationArtifact, handoffs []Handoff) WorkItemReadiness {
	statuses := make([]string, 0, len(assignments))
	assignmentsByID := AssignmentsByID(assignments)
	closed := WorkItemClosed(workItem.Status)
	readiness := WorkItemReadiness{
		ProjectID:            workItem.ProjectID,
		WorkItemID:           workItem.ID,
		Status:               "ready",
		Title:                "Ready to mark done",
		Detail:               "Assignments, evidence, handoffs, and review follow-up are clear. The operator can mark this work item done.",
		AssignmentCount:      len(assignments),
		CompletedAssignments: 0,
	}

	for _, assignment := range assignments {
		status := AssignmentReadinessStatus(assignment)
		statuses = append(statuses, status)
		if status != AssignmentStatusCompleted {
			continue
		}
		readiness.CompletedAssignments++
		if !closed && !AssignmentHasCloseoutEvidence(assignment, artifacts) {
			readiness.MissingEvidenceAssignmentIDs = append(readiness.MissingEvidenceAssignmentIDs, assignment.ID)
		}
	}
	if closed {
		readiness.Status = "done"
		readiness.Title = "Work item is done"
		readiness.Detail = "This work item has already been marked done by the operator."
		return readiness
	}

	activeAssignments := readinessStatusCount(statuses, IsActiveAssignmentStatus)
	failedAssignments := readinessStatusCount(statuses, func(status string) bool {
		return status == AssignmentStatusFailed
	})
	cancelledAssignments := readinessStatusCount(statuses, func(status string) bool {
		return status == AssignmentStatusCancelled
	})
	unresolvedAssignments := readinessStatusCount(statuses, IsUnresolvedAssignmentStatus)
	pendingHandoffs := 0
	for _, handoff := range handoffs {
		if handoff.Status == HandoffStatusPending {
			pendingHandoffs++
			readiness.OpenHandoffIDs = append(readiness.OpenHandoffIDs, handoff.ID)
		}
	}
	if activeAssignments > 0 {
		readiness.Blockers = append(readiness.Blockers, readinessPlural(activeAssignments, "assignment is still active", "assignments are still active"))
	}
	if failedAssignments > 0 {
		readiness.Blockers = append(readiness.Blockers, readinessPlural(failedAssignments, "assignment failed", "assignments failed"))
	}
	if cancelledAssignments > 0 {
		readiness.Blockers = append(readiness.Blockers, readinessPlural(cancelledAssignments, "assignment was cancelled", "assignments were cancelled"))
	}
	if unresolvedAssignments > 0 {
		readiness.Blockers = append(readiness.Blockers, readinessPlural(unresolvedAssignments, "assignment is not complete", "assignments are not complete"))
	}
	if pendingHandoffs > 0 {
		readiness.Blockers = append(readiness.Blockers, readinessPlural(pendingHandoffs, "handoff is pending", "handoffs are pending"))
	}
	if len(readiness.MissingEvidenceAssignmentIDs) > 0 {
		readiness.Blockers = append(readiness.Blockers, readinessPlural(len(readiness.MissingEvidenceAssignmentIDs), "completed assignment is missing evidence", "completed assignments are missing evidence"))
	}
	if len(assignments) == 0 {
		readiness.Warnings = append(readiness.Warnings, "No assignments are linked to this work item; closeout is manual.")
	}
	for _, artifact := range artifacts {
		if ReviewArtifactNeedsFollowUpPath(artifact, handoffs) {
			blocker := ReviewFollowUpBlocker(artifact, handoffs, assignmentsByID)
			readiness.ReviewFollowUpArtifactIDs = append(readiness.ReviewFollowUpArtifactIDs, artifact.ID)
			readiness.ReviewFollowUps = append(readiness.ReviewFollowUps, renderReviewFollowUpReadiness(artifact, blocker))
		}
		if blocker := ReviewFollowUpBlocker(artifact, handoffs, assignmentsByID); blocker != "" {
			readiness.Blockers = append(readiness.Blockers, blocker)
		}
	}
	readiness.ReviewFollowUpCount = len(readiness.ReviewFollowUpArtifactIDs)
	readiness.Blockers = UniqueReadinessStrings(readiness.Blockers)
	readiness.Warnings = UniqueReadinessStrings(readiness.Warnings)
	if len(readiness.Blockers) > 0 {
		readiness.Status = "blocked"
		readiness.Title = "Closeout is blocked"
		readiness.Detail = "Resolve the listed assignment, evidence, handoff, or review follow-up items before marking this work done."
	}
	readiness.Ready = readiness.Status == "ready"
	return readiness
}

func ReviewFollowUpArtifact(artifacts []CollaborationArtifact, handoffs []Handoff) *CollaborationArtifact {
	items := append([]CollaborationArtifact(nil), artifacts...)
	sort.SliceStable(items, func(i, j int) bool {
		left, right := firstNonZeroReadinessTime(items[i].UpdatedAt, items[i].CreatedAt), firstNonZeroReadinessTime(items[j].UpdatedAt, items[j].CreatedAt)
		if !left.Equal(right) {
			return left.After(right)
		}
		return items[i].ID < items[j].ID
	})
	for i := range items {
		if ReviewArtifactNeedsFollowUpPath(items[i], handoffs) {
			return &items[i]
		}
	}
	return nil
}

func ReviewFollowUpBlocker(artifact CollaborationArtifact, handoffs []Handoff, assignmentsByID map[string]Assignment) string {
	if !ReviewArtifactRequiresFollowUp(artifact) {
		return ""
	}
	title := firstNonEmptyReadinessString(artifact.Title, artifact.ID)
	linked := make([]Handoff, 0)
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
		if handoff.Status == HandoffStatusPending {
			return fmt.Sprintf("Review follow-up %q has a pending handoff", title)
		}
		if handoff.Status == HandoffStatusDismissed || handoff.Status == HandoffStatusSuperseded {
			hasDismissedOrSuperseded = true
		}
		if strings.TrimSpace(handoff.TargetAssignmentID) == "" {
			continue
		}
		hasTargetAssignment = true
		if assignment, ok := assignmentsByID[handoff.TargetAssignmentID]; ok {
			hasCompletedTarget = hasCompletedTarget || AssignmentReadinessStatus(assignment) == AssignmentStatusCompleted
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

func AssignmentReadinessStatus(assignment Assignment) string {
	return firstNonEmptyReadinessString(assignment.ExecutionRef.Status, assignment.Status)
}

func IsActiveAssignmentStatus(status string) bool {
	return status == AssignmentStatusQueued || status == AssignmentStatusRunning || status == AssignmentStatusAwaitingApproval
}

func IsUnresolvedAssignmentStatus(status string) bool {
	return status != AssignmentStatusCompleted &&
		status != AssignmentStatusFailed &&
		status != AssignmentStatusCancelled &&
		!IsActiveAssignmentStatus(status)
}

func AssignmentHasCloseoutEvidence(assignment Assignment, artifacts []CollaborationArtifact) bool {
	for _, artifact := range artifacts {
		if artifact.Kind != ArtifactKindEvidenceLink {
			continue
		}
		if artifact.AssignmentID == "" || artifact.AssignmentID == assignment.ID {
			return true
		}
	}
	return false
}

func AssignmentsByID(assignments []Assignment) map[string]Assignment {
	byID := make(map[string]Assignment, len(assignments))
	for _, assignment := range assignments {
		byID[assignment.ID] = assignment
	}
	return byID
}

func WorkItemClosed(status string) bool {
	switch strings.TrimSpace(status) {
	case WorkItemStatusDone, WorkItemStatusCancelled:
		return true
	default:
		return false
	}
}

func UniqueReadinessStrings(values []string) []string {
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

func renderReviewFollowUpReadiness(artifact CollaborationArtifact, blocker string) ReviewFollowUpReadiness {
	return ReviewFollowUpReadiness{
		ArtifactID:           artifact.ID,
		Title:                firstNonEmptyReadinessString(artifact.Title, artifact.ID),
		Status:               "needs_path",
		Blocker:              strings.TrimSpace(blocker),
		ReviewedAssignmentID: artifact.ReviewedAssignmentID,
		ReviewVerdict:        artifact.ReviewVerdict,
		ReviewRisk:           artifact.ReviewRisk,
	}
}

func readinessStatusCount(statuses []string, predicate func(string) bool) int {
	count := 0
	for _, status := range statuses {
		if predicate(status) {
			count++
		}
	}
	return count
}

func readinessPlural(count int, singular, plural string) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", singular)
	}
	return fmt.Sprintf("%d %s", count, plural)
}

func firstNonEmptyReadinessString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func firstNonZeroReadinessTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}
