package api

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/projectworkapp"
)

func (h *Handler) reconcileProjectAssignmentsForChat(ctx context.Context, session chat.Session) {
	if h == nil || strings.TrimSpace(session.ProjectID) == "" || strings.TrimSpace(session.ID) == "" {
		return
	}
	if h.projectWork == nil {
		if h.projectReadRoutesUseCairnlineReadModel() && h.requiresEmbeddedCairnlineProjectReads() {
			h.reconcileStrictEmbeddedCairnlineProjectAssignmentsForChat(ctx, session)
		}
		return
	}
	result, err := h.projectWorkApplication().ReconcileChatSessionAssignments(ctx, session)
	for _, assignment := range result.Updated {
		h.mirrorProjectAssignmentToCairnline(ctx, "project_assignment_chat_reconcile", assignment)
	}
	if err != nil && h.logger != nil {
		h.logger.WarnContext(ctx, "project.assignment_chat_reconcile_failed",
			slog.String("project_id", session.ProjectID),
			slog.String("chat_session_id", session.ID),
			slog.Any("error", err),
		)
	}
}

func (h *Handler) reconcileStrictEmbeddedCairnlineProjectAssignmentsForChat(ctx context.Context, session chat.Session) {
	projectID := strings.TrimSpace(session.ProjectID)
	sessionID := strings.TrimSpace(session.ID)
	err := h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		items, err := service.ListAssignments(ctx, projectID)
		if errors.Is(err, cairnline.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, item := range items {
			assignment := projectWorkAssignmentFromCairnline(item)
			assignment, err = h.projectWorkApplication().ApplyAssignmentRuntime(ctx, assignment)
			if err != nil {
				return err
			}
			ref := projectwork.NormalizeAssignmentExecutionRef(assignment.ExecutionRef)
			if strings.TrimSpace(ref.ChatSessionID) != sessionID {
				continue
			}
			projection := projectworkapp.ProjectAssignmentChatExecution(assignment, session)
			if projection == nil || projection.Missing || strings.TrimSpace(projection.Status) == "" {
				continue
			}
			updated := projectAssignmentWithChatProjection(assignment, projection, time.Now().UTC())
			recorded, err := h.writeStrictEmbeddedCairnlineAssignmentRuntime(ctx, service, updated)
			if err != nil {
				return err
			}
			h.shadowProjectAssignmentRuntimeToHecate(ctx, "project_assignment_chat_reconcile_cairnline_runtime", recorded)
		}
		return nil
	})
	if err != nil && h.logger != nil {
		h.logger.WarnContext(ctx, "project.assignment_chat_reconcile_cairnline_failed",
			slog.String("project_id", projectID),
			slog.String("chat_session_id", sessionID),
			slog.Any("error", err),
		)
	}
}

func projectAssignmentWithChatProjection(assignment projectwork.Assignment, projection *projectworkapp.AssignmentChatProjection, now time.Time) projectwork.Assignment {
	if projection == nil {
		return assignment
	}
	assignment.Status = projection.Status
	if ref := projectworkapp.AssignmentExecutionRefForChat(assignment, projection, projection.Status); ref != nil {
		assignment.ExecutionRef = projectwork.NormalizeAssignmentExecutionRef(*ref)
	}
	if assignment.StartedAt.IsZero() && !projection.StartedAt.IsZero() {
		assignment.StartedAt = projection.StartedAt
	}
	if projectworkapp.AssignmentIsTerminal(projection.Status) {
		assignment.CompletedAt = projectworkapp.FirstNonZeroTime(assignment.CompletedAt, projection.CompletedAt, projection.ProjectedAt, now)
	}
	return assignment
}

func (h *Handler) reconcileProjectAssignmentsForAllChats(ctx context.Context) {
	if h == nil || h.agentChat == nil {
		return
	}
	sessions, err := h.agentChat.List(ctx)
	if err != nil {
		if h.logger != nil {
			h.logger.WarnContext(ctx, "project.assignment_chat_reconcile_list_failed", slog.Any("error", err))
		}
		return
	}
	for _, summary := range sessions {
		if strings.TrimSpace(summary.ProjectID) == "" {
			continue
		}
		session, ok, err := h.agentChat.Get(ctx, summary.ID)
		if err != nil || !ok {
			if err != nil && h.logger != nil {
				h.logger.WarnContext(ctx, "project.assignment_chat_reconcile_get_failed", slog.String("chat_session_id", summary.ID), slog.Any("error", err))
			}
			continue
		}
		h.reconcileProjectAssignmentsForChat(ctx, session)
	}
}
