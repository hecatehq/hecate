package api

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hecate/agent-runtime/internal/chatstate"
	"github.com/hecate/agent-runtime/internal/telemetry"
	"github.com/hecate/agent-runtime/pkg/types"
)

func (h *Handler) HandleCreateChatSession(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requireAny(w, r)
	if !ok {
		return
	}
	ctx := h.contextWithPrincipal(r.Context(), principal)

	var req CreateChatSessionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "New chat"
	}

	session := types.ChatSession{
		ID:        newChatSessionID(),
		Title:     title,
		Tenant:    principal.Tenant,
		User:      principal.Name,
		CreatedAt: time.Now().UTC(),
	}
	result, err := h.service.CreateChatSession(ctx, session)
	if err != nil {
		telemetry.Error(h.logger, ctx, "gateway.chat.sessions.create.failed",
			slog.String("event.name", "gateway.chat.sessions.create.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, ChatSessionResponse{
		Object: "chat_session",
		Data:   renderChatSession(result.Session),
	})
}

func (h *Handler) HandleChatSessions(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requireAny(w, r)
	if !ok {
		return
	}
	ctx := h.contextWithPrincipal(r.Context(), principal)

	limit := h.config.Chat.SessionLimit
	if limit <= 0 {
		limit = 50
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "limit query parameter must be a non-negative integer")
			return
		}
		if value > 200 {
			value = 200
		}
		limit = value
	}

	offset := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "offset query parameter must be a non-negative integer")
			return
		}
		offset = value
	}

	// Fetch limit+1 to determine whether more sessions exist.
	filter := chatstate.Filter{Limit: limit + 1, Offset: offset}
	if principal.IsAdmin() {
		filter.Tenant = strings.TrimSpace(r.URL.Query().Get("tenant"))
	} else {
		filter.Tenant = principal.Tenant
	}

	result, err := h.service.ListChatSessions(ctx, filter)
	if err != nil {
		telemetry.Error(h.logger, ctx, "gateway.chat.sessions.list.failed",
			slog.String("event.name", "gateway.chat.sessions.list.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}

	hasMore := len(result.Sessions) > limit
	sessions := result.Sessions
	if hasMore {
		sessions = sessions[:limit]
	}

	items := make([]ChatSessionSummaryItem, 0, len(sessions))
	for _, session := range sessions {
		if !principal.IsAdmin() && principal.Tenant != "" && session.Tenant != principal.Tenant {
			continue
		}
		items = append(items, renderChatSessionSummary(session))
	}
	WriteJSON(w, http.StatusOK, ChatSessionsResponse{
		Object:  "chat_sessions",
		Data:    items,
		HasMore: hasMore,
	})
}

func (h *Handler) HandleChatSession(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requireAny(w, r)
	if !ok {
		return
	}
	ctx := h.contextWithPrincipal(r.Context(), principal)

	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "session id is required")
		return
	}

	result, err := h.service.GetChatSession(ctx, id)
	if err != nil {
		telemetry.Error(h.logger, ctx, "gateway.chat.sessions.get.failed",
			slog.String("event.name", "gateway.chat.sessions.get.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusNotFound, errCodeInvalidRequest, "chat session not found")
		return
	}
	if !principal.IsAdmin() && principal.Tenant != "" && result.Session.Tenant != principal.Tenant {
		WriteError(w, http.StatusForbidden, errCodeForbidden, "chat session is outside the active tenant scope")
		return
	}

	WriteJSON(w, http.StatusOK, ChatSessionResponse{
		Object: "chat_session",
		Data:   renderChatSession(result.Session),
	})
}

func (h *Handler) HandleDeleteChatSession(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requireAny(w, r)
	if !ok {
		return
	}
	ctx := h.contextWithPrincipal(r.Context(), principal)

	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "session id is required")
		return
	}

	result, err := h.service.GetChatSession(ctx, id)
	if err != nil {
		WriteError(w, http.StatusNotFound, errCodeInvalidRequest, "chat session not found")
		return
	}
	if !principal.IsAdmin() && principal.Tenant != "" && result.Session.Tenant != principal.Tenant {
		WriteError(w, http.StatusForbidden, errCodeForbidden, "chat session is outside the active tenant scope")
		return
	}

	if err := h.service.DeleteChatSession(ctx, id); err != nil {
		telemetry.Error(h.logger, ctx, "gateway.chat.sessions.delete.failed",
			slog.String("event.name", "gateway.chat.sessions.delete.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) HandleUpdateChatSession(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requireAny(w, r)
	if !ok {
		return
	}
	ctx := h.contextWithPrincipal(r.Context(), principal)

	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "session id is required")
		return
	}

	var req UpdateChatSessionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Title == nil && req.SystemPrompt == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "request must include at least one of title, system_prompt")
		return
	}
	if req.Title != nil && strings.TrimSpace(*req.Title) == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "title cannot be set to an empty string")
		return
	}

	existing, err := h.service.GetChatSession(ctx, id)
	if err != nil {
		WriteError(w, http.StatusNotFound, errCodeInvalidRequest, "chat session not found")
		return
	}
	if !principal.IsAdmin() && principal.Tenant != "" && existing.Session.Tenant != principal.Tenant {
		WriteError(w, http.StatusForbidden, errCodeForbidden, "chat session is outside the active tenant scope")
		return
	}

	// Apply each field that the client included. Title and system_prompt
	// are independent UPDATEs in the store; doing them in sequence keeps
	// the storage interface simple at the cost of two round trips when a
	// client patches both at once. PATCH semantics — fields not included
	// stay as they were.
	updatedSession := existing.Session
	if req.Title != nil {
		result, err := h.service.UpdateChatSessionTitle(ctx, id, strings.TrimSpace(*req.Title))
		if err != nil {
			telemetry.Error(h.logger, ctx, "gateway.chat.sessions.update.failed",
				slog.String("event.name", "gateway.chat.sessions.update.failed"),
				slog.Any("error", err),
			)
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
			return
		}
		updatedSession = result.Session
	}
	if req.SystemPrompt != nil {
		result, err := h.service.UpdateChatSessionSystemPrompt(ctx, id, *req.SystemPrompt)
		if err != nil {
			telemetry.Error(h.logger, ctx, "gateway.chat.sessions.update.failed",
				slog.String("event.name", "gateway.chat.sessions.update.failed"),
				slog.Any("error", err),
			)
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
			return
		}
		updatedSession = result.Session
	}
	WriteJSON(w, http.StatusOK, ChatSessionResponse{
		Object: "chat_session",
		Data:   renderChatSession(updatedSession),
	})
}

func renderChatSessionSummary(session types.ChatSession) ChatSessionSummaryItem {
	item := ChatSessionSummaryItem{
		ID:                session.ID,
		Title:             session.Title,
		Tenant:            session.Tenant,
		User:              session.User,
		MessageCount:      len(session.Messages),
		ProviderCallCount: len(session.ProviderCalls),
	}
	if !session.CreatedAt.IsZero() {
		item.CreatedAt = session.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	if !session.UpdatedAt.IsZero() {
		item.UpdatedAt = session.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	if len(session.ProviderCalls) > 0 {
		last := session.ProviderCalls[len(session.ProviderCalls)-1]
		item.LastModel = last.Model
		item.LastProvider = last.Provider
		item.LastCostUSD = formatUSD(last.CostMicrosUSD)
		item.LastRequestID = last.RequestID
	}
	return item
}

func renderChatSession(session types.ChatSession) ChatSessionItem {
	item := ChatSessionItem{
		ID:            session.ID,
		Title:         session.Title,
		SystemPrompt:  session.SystemPrompt,
		Tenant:        session.Tenant,
		User:          session.User,
		Messages:      make([]ChatSessionMessageItem, 0, len(session.Messages)),
		ProviderCalls: make([]ChatProviderCallItem, 0, len(session.ProviderCalls)),
	}
	if !session.CreatedAt.IsZero() {
		item.CreatedAt = session.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	if !session.UpdatedAt.IsZero() {
		item.UpdatedAt = session.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	for _, msg := range session.Messages {
		record := ChatSessionMessageItem{
			ID:                msg.ID,
			Sequence:          msg.Sequence,
			ProducedByCallID:  msg.ProducedByCallID,
			OpenAIChatMessage: messageToWire(msg.Message),
		}
		if !msg.CreatedAt.IsZero() {
			record.CreatedAt = msg.CreatedAt.UTC().Format(time.RFC3339Nano)
		}
		item.Messages = append(item.Messages, record)
	}
	for _, call := range session.ProviderCalls {
		record := ChatProviderCallItem{
			ID:                call.ID,
			RequestID:         call.RequestID,
			RequestedProvider: call.RequestedProvider,
			Provider:          call.Provider,
			ProviderKind:      call.ProviderKind,
			RequestedModel:    call.RequestedModel,
			Model:             call.Model,
			CostMicrosUSD:     call.CostMicrosUSD,
			CostUSD:           formatUSD(call.CostMicrosUSD),
			PromptTokens:      call.PromptTokens,
			CompletionTokens:  call.CompletionTokens,
			TotalTokens:       call.TotalTokens,
		}
		if !call.CreatedAt.IsZero() {
			record.CreatedAt = call.CreatedAt.UTC().Format(time.RFC3339Nano)
		}
		item.ProviderCalls = append(item.ProviderCalls, record)
	}
	return item
}

func newChatSessionID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "chat-" + time.Now().UTC().Format("20060102150405.000000000")
	}
	return "chat_" + hex.EncodeToString(buf)
}
