package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	items, err := h.agentChat.List(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "gateway_error", err.Error())
		return
	}
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
	workspace, err := agentadapters.ValidateWorkspace(workspace)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = adapter.Name + " chat"
	}
	session, err := h.agentChat.Create(r.Context(), agentchat.Session{
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
	session, ok, err := h.agentChat.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "gateway_error", err.Error())
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, "not_found", "agent chat session not found")
		return
	}
	WriteJSON(w, http.StatusOK, AgentChatSessionResponse{Object: "agent_chat_session", Data: renderAgentChatSession(session)})
}

func (h *Handler) HandleAgentChatSessionStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "streaming not supported by server")
		return
	}
	session, ok, err := h.agentChat.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "gateway_error", err.Error())
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, "not_found", "agent chat session not found")
		return
	}
	updates, unsubscribe := h.agentChatLive.subscribe(session.ID)
	defer unsubscribe()

	writeSSEHeaders(w)
	sendAgentChatSSE(w, flusher, "snapshot", AgentChatSessionResponse{
		Object: "agent_chat_session",
		Data:   renderAgentChatSession(session),
	})
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	observedRun := session.Status == "running"
	for {
		select {
		case <-r.Context().Done():
			return
		case payload, ok := <-updates:
			if !ok {
				return
			}
			sendAgentChatSSE(w, flusher, "snapshot", payload)
			if payload.Data.Status == "running" {
				observedRun = true
			}
			if observedRun && isTerminalAgentChatStatus(payload.Data.Status) {
				sendAgentChatSSE(w, flusher, "done", payload)
				return
			}
		case <-heartbeat.C:
			fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

func (h *Handler) HandleDeleteAgentChatSession(w http.ResponseWriter, r *http.Request) {
	if err := h.agentChat.Delete(r.Context(), r.PathValue("id")); err != nil {
		WriteError(w, http.StatusInternalServerError, "gateway_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) HandleCancelAgentChatSession(w http.ResponseWriter, r *http.Request) {
	session, ok, err := h.agentChat.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "gateway_error", err.Error())
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, "not_found", "agent chat session not found")
		return
	}
	if !h.agentChatLive.cancelRun(session.ID) {
		WriteError(w, http.StatusConflict, errCodeInvalidRequest, "agent chat session is not running")
		return
	}
	WriteJSON(w, http.StatusAccepted, AgentChatSessionResponse{Object: "agent_chat_session", Data: renderAgentChatSession(session)})
}

func (h *Handler) HandleCreateAgentChatMessage(w http.ResponseWriter, r *http.Request) {
	session, ok, err := h.agentChat.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "gateway_error", err.Error())
		return
	}
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
	runCtx, cancel := context.WithTimeout(r.Context(), agentChatTimeout)
	if !h.agentChatLive.registerRun(session.ID, cancel) {
		cancel()
		WriteError(w, http.StatusConflict, errCodeInvalidRequest, "agent chat session is already running")
		return
	}
	defer h.agentChatLive.clearRun(session.ID)
	defer cancel()

	updated, err := h.agentChat.AppendMessage(r.Context(), session.ID, agentchat.Message{
		ID:        newAgentChatID("msg"),
		Role:      "user",
		Content:   content,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "gateway_error", err.Error())
		return
	}
	h.agentChatLive.publish(updated)
	assistantID := newAgentChatID("msg")
	runID := newAgentChatID("agent_run")
	startedAt := time.Now().UTC()
	updated, err = h.agentChat.AppendMessage(r.Context(), session.ID, agentchat.Message{
		ID:          assistantID,
		RunID:       runID,
		Role:        "assistant",
		Content:     "",
		AdapterID:   adapter.ID,
		AdapterName: adapter.Name,
		Status:      "running",
		CostMode:    adapter.CostMode,
		Workspace:   session.Workspace,
		CreatedAt:   time.Now().UTC(),
		StartedAt:   startedAt,
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "gateway_error", err.Error())
		return
	}
	h.agentChatLive.publish(updated)

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
			updated, updateErr := h.agentChat.UpdateMessage(runCtx, session.ID, assistantID, func(message *agentchat.Message) {
				message.Content += chunk
			})
			if updateErr == nil {
				h.agentChatLive.publish(updated)
			}
		},
	})
	status := "completed"
	if runErr != nil {
		status = "failed"
	}
	if errors.Is(runCtx.Err(), context.Canceled) {
		status = "cancelled"
	}
	output := strings.TrimSpace(result.Output)
	if status == "cancelled" {
		if output == "" {
			output = "agent run cancelled"
		}
	} else if output == "" && runErr != nil {
		output = runErr.Error()
	} else if runErr != nil {
		output = output + "\n\n" + runErr.Error()
	}
	if output == "" {
		output = "(agent completed without output)"
	}
	completedAt := time.Now().UTC()
	if !result.StartedAt.IsZero() {
		startedAt = result.StartedAt
	}
	if !result.CompletedAt.IsZero() {
		completedAt = result.CompletedAt
	}
	errorText := ""
	if runErr != nil {
		errorText = runErr.Error()
	}
	if status == "cancelled" {
		errorText = "cancelled"
	}

	updated, err = h.agentChat.UpdateMessage(r.Context(), session.ID, assistantID, func(message *agentchat.Message) {
		if strings.TrimSpace(message.Content) == "" || runErr != nil {
			message.Content = output
		}
		message.Status = status
		message.ExitCode = result.ExitCode
		message.DiffStat = result.DiffStat
		message.Diff = result.Diff
		message.StartedAt = startedAt
		message.CompletedAt = completedAt
		message.Error = errorText
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "gateway_error", err.Error())
		return
	}
	h.agentChatLive.publish(updated)
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
			RunID:       message.RunID,
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
			StartedAt:   formatOptionalTime(message.StartedAt),
			CompletedAt: formatOptionalTime(message.CompletedAt),
			DurationMS:  durationMillis(message.StartedAt, message.CompletedAt),
			Error:       message.Error,
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

func durationMillis(startedAt, completedAt time.Time) int64 {
	if startedAt.IsZero() || completedAt.IsZero() || completedAt.Before(startedAt) {
		return 0
	}
	return completedAt.Sub(startedAt).Milliseconds()
}

func sendAgentChatSSE(w http.ResponseWriter, flusher http.Flusher, event string, payload AgentChatSessionResponse) {
	data, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: {\"error\":{\"message\":%q}}\n\n", err.Error())
		flusher.Flush()
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	flusher.Flush()
}

func isTerminalAgentChatStatus(status string) bool {
	switch status {
	case "completed", "failed", "cancelled":
		return true
	default:
		return false
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
