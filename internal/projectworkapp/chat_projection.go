package projectworkapp

import (
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/projectwork"
)

type AssignmentChatProjection struct {
	ChatSessionID string
	MessageID     string
	Status        string
	TraceID       string
	StartedAt     time.Time
	CompletedAt   time.Time
	ProjectedAt   time.Time
	Missing       bool
}

func ProjectAssignmentChatExecution(assignment projectwork.Assignment, session chat.Session) *AssignmentChatProjection {
	ref := projectwork.NormalizeAssignmentExecutionRef(assignment.ExecutionRef)
	chatSessionID := strings.TrimSpace(ref.ChatSessionID)
	if chatSessionID == "" {
		return nil
	}
	projection := &AssignmentChatProjection{
		ChatSessionID: chatSessionID,
		Status:        assignment.Status,
		StartedAt:     assignment.StartedAt,
		CompletedAt:   assignment.CompletedAt,
	}
	if strings.TrimSpace(session.ID) != chatSessionID {
		projection.Missing = true
		return projection
	}

	latest := LatestAssignmentChatMessage(session)
	status, projectedAt := assignmentStatusFromChatSession(session, latest)
	if status != "" {
		projection.Status = ProjectedAssignmentStatus(assignment, status, projectedAt)
		projection.ProjectedAt = projectedAt
	}
	if latest != nil {
		projection.MessageID = latest.ID
		projection.TraceID = latest.TraceID
		projection.StartedAt = FirstNonZeroTime(assignment.StartedAt, latest.StartedAt, latest.CreatedAt)
		if AssignmentIsTerminal(projection.Status) {
			projection.CompletedAt = FirstNonZeroTime(assignment.CompletedAt, latest.CompletedAt, projectedAt)
		}
	} else if AssignmentIsTerminal(projection.Status) {
		projection.CompletedAt = FirstNonZeroTime(assignment.CompletedAt, session.UpdatedAt, projectedAt)
	}
	return projection
}

func AssignmentExecutionRefForChat(assignment projectwork.Assignment, projection *AssignmentChatProjection, status string) *AssignmentExecutionRef {
	if projection == nil {
		return AssignmentExecutionRefFor(assignment, nil, status)
	}
	ref := AssignmentExecutionRefFor(assignment, nil, firstNonEmpty(status, projection.Status))
	if ref == nil {
		ref = &AssignmentExecutionRef{}
	}
	ref.Kind = projectwork.AssignmentExecutionKindChatSession
	ref.ChatSessionID = firstNonEmpty(projection.ChatSessionID, ref.ChatSessionID)
	ref.MessageID = firstNonEmpty(projection.MessageID, ref.MessageID)
	ref.Status = firstNonEmpty(status, projection.Status, ref.Status)
	ref.TraceID = firstNonEmpty(projection.TraceID, ref.TraceID)
	ref.Missing = projection.Missing
	if ref.ChatSessionID == "" {
		return nil
	}
	return ref
}

func LatestAssignmentChatMessage(session chat.Session) *chat.Message {
	for i := len(session.Messages) - 1; i >= 0; i-- {
		if strings.TrimSpace(session.Messages[i].ID) == "" || strings.TrimSpace(session.Messages[i].Role) != "assistant" {
			continue
		}
		return &session.Messages[i]
	}
	return nil
}

func AssignmentStatusFromChatStatus(status string) string {
	switch strings.TrimSpace(status) {
	case "queued":
		return projectwork.AssignmentStatusQueued
	case "running":
		return projectwork.AssignmentStatusRunning
	case "awaiting_approval":
		return projectwork.AssignmentStatusAwaitingApproval
	case "completed":
		return projectwork.AssignmentStatusCompleted
	case "failed":
		return projectwork.AssignmentStatusFailed
	case "cancelled", "closed":
		return projectwork.AssignmentStatusCancelled
	default:
		return ""
	}
}

func assignmentStatusFromChatSession(session chat.Session, latest *chat.Message) (string, time.Time) {
	if latest != nil {
		if status := AssignmentStatusFromChatStatus(latest.Status); status != "" {
			return status, FirstNonZeroTime(latest.CompletedAt, latest.StartedAt, latest.CreatedAt, session.UpdatedAt, session.CreatedAt)
		}
	}
	if status := AssignmentStatusFromChatStatus(session.Status); status != "" {
		return status, FirstNonZeroTime(session.UpdatedAt, session.CreatedAt)
	}
	return "", FirstNonZeroTime(session.UpdatedAt, session.CreatedAt)
}
