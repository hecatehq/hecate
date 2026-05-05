package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/hecate/agent-runtime/internal/agentchat"
)

func (h *Handler) HandleAgentChatMessageFiles(w http.ResponseWriter, r *http.Request) {
	_, message, ok := h.loadAgentChatMessage(r.Context(), w, r)
	if !ok {
		return
	}
	files := agentchat.ParseChangedFiles(message.Diff, message.DiffStat)
	items := make([]AgentChatChangedFileItem, 0, len(files))
	for _, file := range files {
		items = append(items, renderAgentChatChangedFile(file))
	}
	WriteJSON(w, http.StatusOK, AgentChatChangedFilesResponse{
		Object: "agent_chat_changed_files",
		Data:   items,
	})
}

func (h *Handler) HandleAgentChatMessageFileDiff(w http.ResponseWriter, r *http.Request) {
	_, message, ok := h.loadAgentChatMessage(r.Context(), w, r)
	if !ok {
		return
	}
	path := strings.TrimSpace(r.PathValue("path"))
	file, found := agentchat.ExtractFileDiff(message.Diff, path)
	if !found {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "changed file not found")
		return
	}
	WriteJSON(w, http.StatusOK, AgentChatChangedFileDiffResponse{
		Object: "agent_chat_changed_file_diff",
		Data: AgentChatChangedFileDiffItem{
			Path:      file.Path,
			Additions: file.Additions,
			Deletions: file.Deletions,
			Status:    file.Status,
			Diff:      file.Diff,
		},
	})
}

func (h *Handler) loadAgentChatMessage(ctx context.Context, w http.ResponseWriter, r *http.Request) (agentchat.Session, agentchat.Message, bool) {
	sessionID := strings.TrimSpace(r.PathValue("id"))
	messageID := strings.TrimSpace(r.PathValue("message_id"))
	if sessionID == "" || messageID == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "session id and message id are required")
		return agentchat.Session{}, agentchat.Message{}, false
	}
	session, ok, err := h.agentChat.Get(ctx, sessionID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return agentchat.Session{}, agentchat.Message{}, false
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "agent chat session not found")
		return agentchat.Session{}, agentchat.Message{}, false
	}
	for _, message := range session.Messages {
		if message.ID == messageID {
			return session, message, true
		}
	}
	WriteError(w, http.StatusNotFound, errCodeNotFound, "agent chat message not found")
	return agentchat.Session{}, agentchat.Message{}, false
}

func renderAgentChatChangedFile(file agentchat.ChangedFile) AgentChatChangedFileItem {
	return AgentChatChangedFileItem{
		Path:      file.Path,
		Additions: file.Additions,
		Deletions: file.Deletions,
		Status:    file.Status,
	}
}
