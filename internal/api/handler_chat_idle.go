package api

import (
	"context"
	"log/slog"
	"time"

	"github.com/hecatehq/hecate/internal/chat"
)

func (h *Handler) startAgentChatIdleSweeper() {
	timeout := h.config.Server.ChatIdleTimeout
	if timeout <= 0 || h.agentChat == nil {
		return
	}
	cadence := timeout / 4
	if cadence <= 0 || cadence > 5*time.Minute {
		cadence = 5 * time.Minute
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.agentChatIdleSweepCancel = cancel
	go h.runAgentChatIdleSweeper(ctx, timeout, cadence)
}

func (h *Handler) runAgentChatIdleSweeper(ctx context.Context, timeout, cadence time.Duration) {
	ticker := time.NewTicker(cadence)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.closeIdleChatSessions(ctx, timeout, time.Now().UTC())
		}
	}
}

func (h *Handler) closeIdleChatSessions(ctx context.Context, timeout time.Duration, now time.Time) {
	items, err := h.agentChat.List(ctx)
	if err != nil {
		h.logger.WarnContext(ctx, "chat.idle_sweep.list_failed", slog.Any("error", err))
		return
	}
	cutoff := now.Add(-timeout)
	for _, summary := range items {
		if summary.Status == "cancelled" || summary.Status == "closed" || summary.Status == "running" {
			continue
		}
		if summary.UpdatedAt.IsZero() || summary.UpdatedAt.After(cutoff) {
			continue
		}
		session, ok, err := h.agentChat.Get(ctx, summary.ID)
		if err != nil || !ok {
			if err != nil {
				h.logger.WarnContext(ctx, "chat.idle_sweep.get_failed", slog.String("session_id", summary.ID), slog.Any("error", err))
			}
			continue
		}
		if session.UpdatedAt.IsZero() || session.UpdatedAt.After(cutoff) || session.Status == "running" {
			continue
		}
		lifecycleClosure := h.agentChatLive.closeSessionLifecycle(session.ID)
		releaseLifecycle := true
		release := func() {
			if releaseLifecycle {
				lifecycleClosure.release()
			}
		}
		settleCtx, settleCancel := context.WithTimeout(ctx, 3*time.Second)
		operationsDrained := lifecycleClosure.waitForOperations(settleCtx)
		settleCancel()
		if !operationsDrained || h.agentChatLive.hasRun(session.ID) {
			release()
			continue
		}
		latest, ok, err := h.agentChat.Get(ctx, session.ID)
		if err != nil || !ok {
			if err != nil {
				h.logger.WarnContext(ctx, "chat.idle_sweep.recheck_failed", slog.String("session_id", session.ID), slog.Any("error", err))
			}
			release()
			continue
		}
		if latest.UpdatedAt.IsZero() || latest.UpdatedAt.After(cutoff) || latest.Status == "running" {
			release()
			continue
		}
		session = latest
		settlementClaim := h.agentChatSettlements.claimSession(session.ID, lifecycleClosure)
		if h.agentChatRunner != nil {
			_ = h.agentChatRunner.CloseSession(ctx, session.ID)
		}
		drainCtx, drainCancel := context.WithTimeout(ctx, 3*time.Second)
		drained := settlementClaim.sealAndDrain(drainCtx)
		drainCancel()
		if !drained {
			settlementClaim.releaseLifecycleAfterDrain(lifecycleClosure)
			releaseLifecycle = false
			continue
		}
		updated, err := h.agentChat.UpdateSession(ctx, session.ID, func(item *chat.Session) {
			item.Status = "cancelled"
			item.DriverKind = ""
			item.NativeSessionID = ""
		})
		if err != nil {
			h.logger.WarnContext(ctx, "chat.idle_sweep.update_failed", slog.String("session_id", session.ID), slog.Any("error", err))
			release()
			continue
		}
		h.annotateChatIdleTimeout(ctx, session.ID, timeout, now)
		if latest, ok, getErr := h.agentChat.Get(ctx, session.ID); getErr == nil && ok {
			updated = latest
		}
		h.reconcileProjectAssignmentsForChat(ctx, updated)
		h.agentChatLive.publishSession(updated)
		release()
	}
}

func (h *Handler) annotateChatIdleTimeout(ctx context.Context, sessionID string, timeout time.Duration, now time.Time) {
	session, ok, err := h.agentChat.Get(ctx, sessionID)
	if err != nil || !ok {
		return
	}
	for i := len(session.Messages) - 1; i >= 0; i-- {
		message := session.Messages[i]
		if message.Role != "assistant" {
			continue
		}
		_, _ = h.agentChat.UpdateMessage(ctx, session.ID, message.ID, func(item *chat.Message) {
			item.Status = "cancelled"
			item.CompletedAt = now
			item.Error = "idle timeout"
			if item.Content == "" {
				item.Content = "Agent chat session closed after being idle."
			}
			item.Activities = append(item.Activities, chat.Activity{
				Type:      "interrupted",
				Status:    "cancelled",
				Title:     "Idle timeout",
				Detail:    "Hecate closed this external-agent session after " + timeout.String() + " without activity.",
				CreatedAt: now,
			})
		})
		return
	}
}
