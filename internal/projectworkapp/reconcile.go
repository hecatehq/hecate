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
			current := ProjectAssignmentChatExecution(*item, session)
			if current == nil || current.Missing || strings.TrimSpace(current.Status) == "" {
				return
			}
			item.Status = current.Status
			if ref := AssignmentExecutionRefForChat(*item, current, current.Status); ref != nil {
				item.ExecutionRef = projectwork.NormalizeAssignmentExecutionRef(*ref)
			}
			if item.StartedAt.IsZero() && !current.StartedAt.IsZero() {
				item.StartedAt = current.StartedAt
			}
			if AssignmentIsTerminal(current.Status) {
				item.CompletedAt = FirstNonZeroTime(item.CompletedAt, current.CompletedAt, current.ProjectedAt, app.now().UTC())
			}
		})
		if err != nil {
			return result, err
		}
		result.Updated = append(result.Updated, updated)
	}
	return result, nil
}
