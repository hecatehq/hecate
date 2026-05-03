package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/hecate/agent-runtime/internal/agentadapters"
	"github.com/hecate/agent-runtime/internal/agentchat"
)

const (
	agentChatTimeout        = 30 * time.Minute
	agentChatMaxOutputBytes = 4 * 1024 * 1024
)

func (h *Handler) HandleAgentChatSessions(w http.ResponseWriter, r *http.Request) {
	items := h.agentChat.List()
	data := make([]AgentChatSessionSummaryItem, 0, len(items))
	for _, item := range items {
		data = append(data, renderAgentChatSessionSummary(item))
	}
	WriteJSON(w, http.StatusOK, AgentChatSessionsResponse{Object: "agent_chat_sessions", Data: data})
}

func (h *Handler) HandleCreateAgentChatSession(w http.ResponseWriter, r *http.Request) {
	var req CreateAgentChatSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "invalid JSON body")
		return
	}
	adapter, ok := agentadapters.BuiltInByID(strings.TrimSpace(req.AdapterID))
	if !ok {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, fmt.Sprintf("agent adapter %q not found", req.AdapterID))
		return
	}
	workspace := strings.TrimSpace(req.Workspace)
	if workspace == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "workspace is required")
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = adapter.Name + " chat"
	}
	session, err := h.agentChat.Create(agentchat.Session{
		ID:        newAgentChatID("agent_chat"),
		Title:     title,
		AdapterID: adapter.ID,
		Workspace: workspace,
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "gateway_error", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, AgentChatSessionResponse{Object: "agent_chat_session", Data: renderAgentChatSession(session)})
}

func (h *Handler) HandleAgentChatSession(w http.ResponseWriter, r *http.Request) {
	session, ok := h.agentChat.Get(r.PathValue("id"))
	if !ok {
		WriteError(w, http.StatusNotFound, "not_found", "agent chat session not found")
		return
	}
	WriteJSON(w, http.StatusOK, AgentChatSessionResponse{Object: "agent_chat_session", Data: renderAgentChatSession(session)})
}

func (h *Handler) HandleDeleteAgentChatSession(w http.ResponseWriter, r *http.Request) {
	h.agentChat.Delete(r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) HandleCreateAgentChatMessage(w http.ResponseWriter, r *http.Request) {
	session, ok := h.agentChat.Get(r.PathValue("id"))
	if !ok {
		WriteError(w, http.StatusNotFound, "not_found", "agent chat session not found")
		return
	}
	var req CreateAgentChatMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "invalid JSON body")
		return
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "content is required")
		return
	}

	adapter, ok := agentadapters.BuiltInByID(session.AdapterID)
	if !ok {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, fmt.Sprintf("agent adapter %q not found", session.AdapterID))
		return
	}
	if _, err := h.agentChat.AppendMessage(session.ID, agentchat.Message{
		ID:        newAgentChatID("msg"),
		Role:      "user",
		Content:   content,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		WriteError(w, http.StatusInternalServerError, "gateway_error", err.Error())
		return
	}
	assistantID := newAgentChatID("msg")
	if _, err := h.agentChat.AppendMessage(session.ID, agentchat.Message{
		ID:          assistantID,
		Role:        "assistant",
		Content:     "",
		AdapterID:   adapter.ID,
		AdapterName: adapter.Name,
		Status:      "running",
		CostMode:    adapter.CostMode,
		Workspace:   session.Workspace,
		CreatedAt:   time.Now().UTC(),
	}); err != nil {
		WriteError(w, http.StatusInternalServerError, "gateway_error", err.Error())
		return
	}

	runCtx, cancel := context.WithTimeout(r.Context(), agentChatTimeout)
	defer cancel()
	result, runErr := agentadapters.RunAdapter(runCtx, adapter, agentadapters.RunRequest{
		AdapterID:      adapter.ID,
		Workspace:      session.Workspace,
		Prompt:         content,
		Timeout:        agentChatTimeout,
		MaxOutputBytes: agentChatMaxOutputBytes,
		OnOutput: func(chunk string) {
			if chunk == "" {
				return
			}
			_, _ = h.agentChat.UpdateMessage(session.ID, assistantID, func(message *agentchat.Message) {
				message.Content += chunk
			})
		},
	})
	status := "completed"
	if runErr != nil {
		status = "failed"
	}
	output := strings.TrimSpace(result.Output)
	if output == "" && runErr != nil {
		output = runErr.Error()
	} else if runErr != nil {
		output = output + "\n\n" + runErr.Error()
	}
	if output == "" {
		output = "(agent completed without output)"
	}

	updated, err := h.agentChat.UpdateMessage(session.ID, assistantID, func(message *agentchat.Message) {
		if strings.TrimSpace(message.Content) == "" || runErr != nil {
			message.Content = output
		}
		message.Status = status
		message.ExitCode = result.ExitCode
		message.DiffStat = result.DiffStat
		message.Diff = result.Diff
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "gateway_error", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, AgentChatSessionResponse{Object: "agent_chat_session", Data: renderAgentChatSession(updated)})
}

func renderAgentChatSessionSummary(session agentchat.Session) AgentChatSessionSummaryItem {
	return AgentChatSessionSummaryItem{
		ID:              session.ID,
		Title:           session.Title,
		AdapterID:       session.AdapterID,
		Workspace:       session.Workspace,
		WorkspaceBranch: workspaceGitBranch(session.Workspace),
		Status:          session.Status,
		MessageCount:    len(session.Messages),
		CreatedAt:       formatOptionalTime(session.CreatedAt),
		UpdatedAt:       formatOptionalTime(session.UpdatedAt),
	}
}

func renderAgentChatSession(session agentchat.Session) AgentChatSessionItem {
	messages := make([]AgentChatMessageItem, 0, len(session.Messages))
	for _, message := range session.Messages {
		messages = append(messages, AgentChatMessageItem{
			ID:          message.ID,
			Role:        message.Role,
			Content:     message.Content,
			AdapterID:   message.AdapterID,
			AdapterName: message.AdapterName,
			Status:      message.Status,
			ExitCode:    message.ExitCode,
			CostMode:    message.CostMode,
			Workspace:   message.Workspace,
			DiffStat:    message.DiffStat,
			Diff:        message.Diff,
			CreatedAt:   formatOptionalTime(message.CreatedAt),
		})
	}
	return AgentChatSessionItem{
		ID:              session.ID,
		Title:           session.Title,
		AdapterID:       session.AdapterID,
		Workspace:       session.Workspace,
		WorkspaceBranch: workspaceGitBranch(session.Workspace),
		Status:          session.Status,
		CreatedAt:       formatOptionalTime(session.CreatedAt),
		UpdatedAt:       formatOptionalTime(session.UpdatedAt),
		Messages:        messages,
	}
}

func workspaceGitBranch(workspace string) string {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	out, err := exec.CommandContext(ctx, "git", "-C", workspace, "branch", "--show-current").Output()
	if err == nil {
		if branch := strings.TrimSpace(string(out)); branch != "" {
			return branch
		}
	}
	out, err = exec.CommandContext(ctx, "git", "-C", workspace, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	commit := strings.TrimSpace(string(out))
	if commit == "" {
		return ""
	}
	return "detached@" + commit
}

func newAgentChatID(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "id"
	}
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return prefix + "_" + strings.ToLower(fmt.Sprintf("%x", time.Now().UTC().UnixNano()))
	}
	return prefix + "_" + hex.EncodeToString(buf)
}
