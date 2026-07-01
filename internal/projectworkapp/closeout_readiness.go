package projectworkapp

import (
	"context"

	"github.com/hecatehq/hecate/internal/projectwork"
)

var ErrWorkItemCloseoutBlocked = projectwork.ErrWorkItemCloseoutBlocked

type WorkItemReadiness = projectwork.WorkItemReadiness
type ReviewFollowUpReadiness = projectwork.ReviewFollowUpReadiness
type WorkItemCloseoutBlockedError = projectwork.WorkItemCloseoutBlockedError

func (app *Application) WorkItemReadiness(ctx context.Context, projectID, workItemID string) (WorkItemReadiness, error) {
	if app == nil || app.store == nil {
		return WorkItemReadiness{}, ErrStoreNotConfigured
	}
	workItem, ok, err := app.store.GetWorkItem(ctx, projectID, workItemID)
	if err != nil {
		return WorkItemReadiness{}, err
	}
	if !ok {
		return WorkItemReadiness{}, projectwork.ErrNotFound
	}
	assignments, err := app.store.ListAssignments(ctx, projectwork.AssignmentFilter{ProjectID: projectID, WorkItemID: workItemID})
	if err != nil {
		return WorkItemReadiness{}, err
	}
	assignments, err = app.ApplyAssignmentsRuntime(ctx, assignments)
	if err != nil {
		return WorkItemReadiness{}, err
	}
	artifacts, err := app.store.ListArtifacts(ctx, projectwork.ArtifactFilter{ProjectID: projectID, WorkItemID: workItemID})
	if err != nil {
		return WorkItemReadiness{}, err
	}
	handoffs, err := app.store.ListHandoffs(ctx, projectwork.HandoffFilter{ProjectID: projectID, WorkItemID: workItemID})
	if err != nil {
		return WorkItemReadiness{}, err
	}
	return projectwork.EvaluateWorkItemReadiness(workItem, assignments, artifacts, handoffs), nil
}

func EvaluateWorkItemReadiness(workItem projectwork.WorkItem, assignments []projectwork.Assignment, artifacts []projectwork.CollaborationArtifact, handoffs []projectwork.Handoff) WorkItemReadiness {
	return projectwork.EvaluateWorkItemReadiness(workItem, assignments, artifacts, handoffs)
}

func ReviewFollowUpArtifact(artifacts []projectwork.CollaborationArtifact, handoffs []projectwork.Handoff) *projectwork.CollaborationArtifact {
	return projectwork.ReviewFollowUpArtifact(artifacts, handoffs)
}

func ReviewArtifactNeedsFollowUpPath(artifact projectwork.CollaborationArtifact, handoffs []projectwork.Handoff) bool {
	return projectwork.ReviewArtifactNeedsFollowUpPath(artifact, handoffs)
}

func ReviewArtifactRequiresFollowUp(artifact projectwork.CollaborationArtifact) bool {
	return projectwork.ReviewArtifactRequiresFollowUp(artifact)
}

func ReviewArtifactHasLinkedFollowUpPath(artifactID string, handoffs []projectwork.Handoff) bool {
	return projectwork.ReviewArtifactHasLinkedFollowUpPath(artifactID, handoffs)
}

func ReviewFollowUpBlocker(artifact projectwork.CollaborationArtifact, handoffs []projectwork.Handoff, assignmentsByID map[string]projectwork.Assignment) string {
	return projectwork.ReviewFollowUpBlocker(artifact, handoffs, assignmentsByID)
}

func AssignmentReadinessStatus(assignment projectwork.Assignment) string {
	return projectwork.AssignmentReadinessStatus(assignment)
}

func IsActiveAssignmentStatus(status string) bool {
	return projectwork.IsActiveAssignmentStatus(status)
}

func IsUnresolvedAssignmentStatus(status string) bool {
	return projectwork.IsUnresolvedAssignmentStatus(status)
}

func AssignmentHasCloseoutEvidence(assignment projectwork.Assignment, artifacts []projectwork.CollaborationArtifact) bool {
	return projectwork.AssignmentHasCloseoutEvidence(assignment, artifacts)
}

func AssignmentsByID(assignments []projectwork.Assignment) map[string]projectwork.Assignment {
	return projectwork.AssignmentsByID(assignments)
}

func WorkItemClosed(status string) bool {
	return projectwork.WorkItemClosed(status)
}

func UniqueReadinessStrings(values []string) []string {
	return projectwork.UniqueReadinessStrings(values)
}
