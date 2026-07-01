package projectworkapp

import (
	"context"
	"strings"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/projectwork"
)

type ReconcileChatSessionAssignmentsResult struct {
	Updated []projectwork.Assignment
}

func (app *Application) ReconcileChatSessionAssignments(ctx context.Context, session chat.Session) (ReconcileChatSessionAssignmentsResult, error) {
	if app == nil || app.store == nil {
		return ReconcileChatSessionAssignmentsResult{}, ErrStoreNotConfigured
	}
	projectID := strings.TrimSpace(session.ProjectID)
	sessionID := strings.TrimSpace(session.ID)
	if projectID == "" || sessionID == "" {
		return ReconcileChatSessionAssignmentsResult{}, nil
	}
	assignments, err := app.store.ListAssignments(ctx, projectwork.AssignmentFilter{ProjectID: projectID})
	if err != nil {
		return ReconcileChatSessionAssignmentsResult{}, err
	}
	assignments, err = app.ApplyAssignmentsRuntime(ctx, assignments)
	if err != nil {
		return ReconcileChatSessionAssignmentsResult{}, err
	}
	var result ReconcileChatSessionAssignmentsResult
	for _, assignment := range assignments {
		ref := projectwork.NormalizeAssignmentExecutionRef(assignment.ExecutionRef)
		if strings.TrimSpace(ref.ChatSessionID) != sessionID {
			continue
		}
		projection := ProjectAssignmentChatExecution(assignment, session)
		if projection == nil || projection.Missing || strings.TrimSpace(projection.Status) == "" {
			continue
		}
		updated, err := app.store.UpdateAssignment(ctx, projectID, assignment.ID, func(item *projectwork.Assignment) {
			item.Status = projection.Status
			if ref := AssignmentExecutionRefForChat(assignment, projection, projection.Status); ref != nil {
				item.ExecutionRef = projectwork.NormalizeAssignmentExecutionRef(*ref)
			}
			if item.StartedAt.IsZero() && !projection.StartedAt.IsZero() {
				item.StartedAt = projection.StartedAt
			}
			if AssignmentIsTerminal(projection.Status) {
				item.CompletedAt = FirstNonZeroTime(item.CompletedAt, projection.CompletedAt, projection.ProjectedAt, app.now().UTC())
			}
		})
		if err != nil {
			return result, err
		}
		updated, err = app.persistAssignmentRuntime(ctx, updated)
		if err != nil {
			return result, err
		}
		result.Updated = append(result.Updated, updated)
	}
	return result, nil
}
