package api

import (
	"context"
	"log/slog"
	"strings"

	"github.com/hecatehq/hecate/internal/chat"
)

func (h *Handler) reconcileProjectAssignmentsForChat(ctx context.Context, session chat.Session) {
	if h == nil || h.projectWork == nil || strings.TrimSpace(session.ProjectID) == "" || strings.TrimSpace(session.ID) == "" {
		return
	}
	if _, err := h.projectWorkApplication().ReconcileChatSessionAssignments(ctx, session); err != nil && h.logger != nil {
		h.logger.WarnContext(ctx, "project.assignment_chat_reconcile_failed",
			slog.String("project_id", session.ProjectID),
			slog.String("chat_session_id", session.ID),
			slog.Any("error", err),
		)
	}
}

func (h *Handler) reconcileProjectAssignmentsForAllChats(ctx context.Context) {
	if h == nil || h.agentChat == nil || h.projectWork == nil {
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
