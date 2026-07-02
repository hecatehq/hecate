package projectworkapp

import (
	"context"
	"strings"

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
	assignments, err = app.ApplyAssignmentsExecutionProjection(ctx, assignments)
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

func (app *Application) ApplyAssignmentsRuntimeProjection(ctx context.Context, assignments []projectwork.Assignment) ([]projectwork.Assignment, error) {
	assignments, err := app.ApplyAssignmentsRuntime(ctx, assignments)
	if err != nil {
		return nil, err
	}
	return app.ApplyAssignmentsExecutionProjection(ctx, assignments)
}

func (app *Application) ApplyAssignmentsExecutionProjection(ctx context.Context, assignments []projectwork.Assignment) ([]projectwork.Assignment, error) {
	if len(assignments) == 0 {
		return assignments, nil
	}
	out := make([]projectwork.Assignment, 0, len(assignments))
	for _, assignment := range assignments {
		projected, err := app.ApplyAssignmentExecutionProjection(ctx, assignment)
		if err != nil {
			return nil, err
		}
		out = append(out, projected)
	}
	return out, nil
}

func (app *Application) ApplyAssignmentExecutionProjection(ctx context.Context, assignment projectwork.Assignment) (projectwork.Assignment, error) {
	projection, err := ProjectAssignmentExecution(ctx, app.taskStore, assignment)
	if err != nil {
		return projectwork.Assignment{}, err
	}
	if projection != nil {
		if projection.Execution.Missing && strings.TrimSpace(assignment.ExecutionRef.Status) != "" {
			return assignment, nil
		}
		if strings.TrimSpace(projection.Status) != "" {
			assignment.Status = projection.Status
		}
		if assignment.StartedAt.IsZero() && !projection.StartedAt.IsZero() {
			assignment.StartedAt = projection.StartedAt
		}
		if assignment.CompletedAt.IsZero() && !projection.CompletedAt.IsZero() {
			assignment.CompletedAt = projection.CompletedAt
		}
		if ref := AssignmentExecutionRefFor(assignment, &projection.Execution, assignment.Status); ref != nil {
			normalized := projectwork.NormalizeAssignmentExecutionRef(*ref)
			normalized.Status = assignment.Status
			assignment.ExecutionRef = normalized
		}
		return assignment, nil
	}

	chatProjection := app.assignmentChatProjection(ctx, assignment)
	if chatProjection == nil {
		return assignment, nil
	}
	if chatProjection.Missing && strings.TrimSpace(assignment.ExecutionRef.Status) != "" {
		return assignment, nil
	}
	if strings.TrimSpace(chatProjection.Status) != "" {
		assignment.Status = chatProjection.Status
	}
	if assignment.StartedAt.IsZero() && !chatProjection.StartedAt.IsZero() {
		assignment.StartedAt = chatProjection.StartedAt
	}
	if assignment.CompletedAt.IsZero() && !chatProjection.CompletedAt.IsZero() {
		assignment.CompletedAt = chatProjection.CompletedAt
	}
	if ref := AssignmentExecutionRefForChat(assignment, chatProjection, assignment.Status); ref != nil {
		assignment.ExecutionRef = projectwork.NormalizeAssignmentExecutionRef(*ref)
	}
	return assignment, nil
}

func (app *Application) assignmentChatProjection(ctx context.Context, assignment projectwork.Assignment) *AssignmentChatProjection {
	ref := projectwork.NormalizeAssignmentExecutionRef(assignment.ExecutionRef)
	chatSessionID := strings.TrimSpace(ref.ChatSessionID)
	if chatSessionID == "" {
		return nil
	}
	missing := &AssignmentChatProjection{
		ChatSessionID: chatSessionID,
		Status:        assignment.Status,
		Missing:       true,
	}
	if app == nil || app.chatStore == nil {
		return missing
	}
	session, ok, err := app.chatStore.Get(ctx, chatSessionID)
	if err != nil || !ok || strings.TrimSpace(session.ProjectID) != strings.TrimSpace(assignment.ProjectID) {
		return missing
	}
	return ProjectAssignmentChatExecution(assignment, session)
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
