package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/chatapp"
	"github.com/hecatehq/hecate/internal/chatcontext"
	"github.com/hecatehq/hecate/internal/gitrunner"
	"github.com/hecatehq/hecate/internal/modelcaps"
	"github.com/hecatehq/hecate/internal/requestscope"
	"github.com/hecatehq/hecate/internal/taskapp"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

const (
	agentChatTimeout             = 30 * time.Minute
	agentChatPrepareTimeout      = 10 * time.Second
	agentChatConfigOptionTimeout = 10 * time.Second
	agentChatMaxOutputBytes      = 4 * 1024 * 1024
)

const (
	agentChatManualCompactRetainMessages = chat.DefaultCompactRetainMessages
	agentChatAutoCompactRetainMessages   = chat.DefaultCompactRetainMessages
	agentChatAutoCompactMinMessages      = chat.DefaultCompactMinMessages
)

func (h *Handler) HandleChatSessions(w http.ResponseWriter, r *http.Request) {
	result, err := h.chatApplication().ListSessions(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	data := make([]ChatSessionSummaryItem, 0, len(result.Sessions))
	for _, item := range result.Sessions {
		data = append(data, renderChatSessionSummary(item))
	}
	WriteJSON(w, http.StatusOK, ChatSessionsResponse{Object: "chat_sessions", Data: data})
}

func (h *Handler) HandleCreateChatSession(w http.ResponseWriter, r *http.Request) {
	var req CreateChatSessionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	agentID := normalizeChatAgentID(req.AgentID)
	if !h.isValidChatAgentID(agentID) {
		writeChatAgentIDInvalid(w)
		return
	}
	projectID := strings.TrimSpace(req.ProjectID)
	if projectID != "" {
		if _, ok, err := h.projects.Get(r.Context(), projectID); err != nil {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
			return
		} else if !ok {
			WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
			return
		}
	}
	isExternalAgent := agentID != chat.DefaultAgentID
	workspace := strings.TrimSpace(req.Workspace)
	if isExternalAgent {
		if workspace == "" {
			writeAgentChatWorkspaceRequired(w, chat.ExecutionModeExternalAgent)
			return
		}
		var err error
		workspace, err = agentadapters.ValidateWorkspace(workspace)
		if err != nil {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
			return
		}
	} else if workspace != "" {
		resolved, err := agentadapters.ValidateWorkspace(workspace)
		if err != nil {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
			return
		}
		workspace = resolved
	}
	workspaceBranch := workspaceGitBranch(workspace)
	title := strings.TrimSpace(req.Title)
	var mcpServers []types.MCPServerConfig
	if len(req.MCPServers) > 0 {
		if !isExternalAgent {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "chat session mcp_servers are only supported for external agents; pass mcp_servers on Hecate tool turns")
			return
		}
		var err error
		mcpServers, err = taskapp.NormalizeMCPServerConfigs(taskMCPServerCommandsFromRequest(req.MCPServers), h.secretCipher, h.config.Server.TaskMaxMCPServersPerTask)
		if err != nil {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
			return
		}
	}

	session := chat.Session{
		ID:              newChatID("chat"),
		Title:           title,
		ProjectID:       projectID,
		AgentID:         agentID,
		Workspace:       workspace,
		WorkspaceBranch: workspaceBranch,
		RTKEnabled:      req.RTKEnabled,
		MCPServers:      mcpServers,
	}
	var err error
	var externalAdapter agentadapters.Adapter
	switch {
	case agentID == chat.DefaultAgentID:
		provider := strings.TrimSpace(req.Provider)
		model := strings.TrimSpace(req.Model)
		var caps types.ModelCapabilities
		if model != "" {
			caps, err = h.resolveModelCapabilities(r.Context(), provider, model)
			if err != nil {
				writeAgentChatModelResolutionError(w, err)
				return
			}
		}
		if session.Title == "" {
			session.Title = "Hecate Chat"
		}
		session.Provider = provider
		session.Model = model
		session.Capabilities = caps
	default:
		adapter, ok := agentadapters.BuiltInByID(agentID)
		if !ok {
			writeAgentChatAdapterNotFound(w, agentID)
			return
		}
		if h.agentChatRunner == nil {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "agent chat runner is not configured")
			return
		}
		externalAdapter = adapter
		if session.Title == "" {
			session.Title = adapter.Name + " chat"
		}
		session.DriverKind = agentadapters.DriverKindACP
		session.ConfigOptions = req.ConfigOptions
	}
	result, err := h.chatApplication().CreateSession(r.Context(), chatapp.CreateSessionCommand{
		Session:         session,
		PrepareExternal: isExternalAgent,
	})
	if err != nil {
		var prepareErr chatapp.ExternalPrepareError
		if errors.As(err, &prepareErr) {
			writeAgentChatPrepareError(w, externalAdapter.Name, prepareErr.Unwrap())
			return
		}
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, ChatSessionResponse{Object: "chat_session", Data: renderChatSession(result.Session, h.agentChatSnapshotConfig())})
}

func normalizeChatAgentID(agentID string) string {
	if agentID = strings.TrimSpace(agentID); agentID != "" {
		return agentID
	}
	return chat.DefaultAgentID
}

func (h *Handler) isValidChatAgentID(agentID string) bool {
	if agentID == chat.DefaultAgentID {
		return true
	}
	_, ok := agentadapters.BuiltInByID(agentID)
	return ok
}

func (h *Handler) handleAgentChatAvailableCommandsUpdate(update agentadapters.AvailableCommandsUpdate) {
	if h == nil || h.agentChat == nil {
		return
	}
	sessionID := strings.TrimSpace(update.SessionID)
	if sessionID == "" {
		return
	}
	commands := slices.Clone(update.Commands)
	ctx, cancel := context.WithTimeout(context.Background(), agentChatConfigOptionTimeout)
	defer cancel()
	session, ok, err := h.agentChat.Get(ctx, sessionID)
	if err != nil {
		telemetry.Warn(h.logger, ctx, "agent chat available commands load failed", slog.String("session_id", sessionID), slog.Any("error", err))
		return
	}
	if !ok {
		return
	}
	if update.AdapterID != "" && session.AgentID != update.AdapterID {
		return
	}
	if slices.Equal(session.AvailableCommands, commands) {
		return
	}
	updated, err := h.agentChat.UpdateSession(ctx, sessionID, func(item *chat.Session) {
		item.AvailableCommands = commands
	})
	if err != nil {
		telemetry.Warn(h.logger, ctx, "agent chat available commands update failed", slog.String("session_id", sessionID), slog.Any("error", err))
		return
	}
	if h.agentChatLive != nil {
		h.agentChatLive.publishSession(updated)
	}
}

func (h *Handler) HandleChatSession(w http.ResponseWriter, r *http.Request) {
	result, err := h.chatApplication().GetSession(r.Context(), r.PathValue("id"))
	if err != nil {
		if writeChatAppError(w, err) {
			return
		}
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, ChatSessionResponse{Object: "chat_session", Data: renderChatSession(result.Session, h.agentChatSnapshotConfig())})
}

func (h *Handler) HandleUpdateChatSession(w http.ResponseWriter, r *http.Request) {
	var req UpdateChatSessionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := h.chatApplication().RenameSession(r.Context(), chatapp.RenameSessionCommand{
		ID:    r.PathValue("id"),
		Title: req.Title,
	})
	if err != nil {
		if writeChatAppError(w, err) {
			return
		}
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	h.agentChatLive.publishSession(result.Session)
	WriteJSON(w, http.StatusOK, ChatSessionResponse{Object: "chat_session", Data: renderChatSession(result.Session, h.agentChatSnapshotConfig())})
}

func (h *Handler) HandleCompactChatSession(w http.ResponseWriter, r *http.Request) {
	session, err := h.compactChatSession(r.Context(), r.PathValue("id"), compactChatSessionOptions{
		RetainMessages:   agentChatManualCompactRetainMessages,
		RequireCompacted: true,
		Now:              time.Now().UTC(),
	})
	if err != nil {
		if writeChatAppError(w, err) {
			return
		}
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	h.agentChatLive.publishSession(session)
	WriteJSON(w, http.StatusOK, ChatSessionResponse{Object: "chat_session", Data: renderChatSession(session, h.agentChatSnapshotConfig())})
}

func (h *Handler) HandleChatSessionStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "streaming not supported by server")
		return
	}
	session, ok, err := h.agentChat.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "agent chat session not found")
		return
	}
	updates, unsubscribe := h.agentChatLive.subscribe(session.ID)
	defer unsubscribe()

	writeSSEHeaders(w)
	projector := newAgentChatStreamProjector(session, h.agentChatSnapshotConfig())
	initial := projector.initialFrame(session)
	sendAgentChatSSE(w, flusher, initial.Event, initial.Data)
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case payload, ok := <-updates:
			if !ok {
				return
			}
			for _, frame := range projector.project(payload) {
				sendAgentChatSSE(w, flusher, frame.Event, frame.Data)
				if frame.Done {
					return
				}
			}
		case <-heartbeat.C:
			fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

func (h *Handler) HandleDeleteChatSession(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("id"))
	session, ok, err := h.agentChat.Get(r.Context(), sessionID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "agent chat session not found")
		return
	}
	stopping, err := h.deleteExistingChatSession(r.Context(), session)
	if errors.Is(err, errChatSessionDeleteConflict) {
		WriteError(w, http.StatusConflict, errCodeConflict, err.Error())
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if stopping {
		writeChatSessionStopping(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

var errChatSessionDeleteConflict = errors.New("chat session delete conflict")

func (h *Handler) deleteExistingChatSession(ctx context.Context, session chat.Session) (bool, error) {
	sessionID := session.ID
	if isHecateChatSession(session) {
		if _, _, err := h.cancelHecateChatTaskRun(ctx, session); err != nil {
			return false, fmt.Errorf("%w: %v", errChatSessionDeleteConflict, err)
		}
	}
	cancelCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	settled := h.agentChatLive.cancelRunAndWait(cancelCtx, sessionID)
	cancel()
	if !settled {
		return true, nil
	}
	if err := h.chatApplication().DeleteSession(ctx, chatapp.DeleteSessionCommand{
		Session:     session,
		CloseNative: isExternalChatSession(session),
	}); err != nil {
		return false, err
	}
	return false, nil
}

func (h *Handler) HandleCancelChatSession(w http.ResponseWriter, r *http.Request) {
	session, ok, err := h.agentChat.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "agent chat session not found")
		return
	}
	if run, found, err := h.cancelHecateChatTaskRun(r.Context(), session); err != nil {
		WriteError(w, http.StatusConflict, errCodeConflict, err.Error())
		return
	} else if found {
		updated, updateErr := h.agentChat.UpdateSession(r.Context(), session.ID, func(item *chat.Session) {
			item.Status = run.Status
		})
		if updateErr == nil {
			h.agentChatLive.publishSession(updated)
			WriteJSON(w, http.StatusAccepted, ChatSessionResponse{Object: "chat_session", Data: renderChatSession(updated, h.agentChatSnapshotConfig())})
			return
		}
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, updateErr.Error())
		return
	}
	if !h.agentChatLive.cancelRun(session.ID) {
		writeChatSessionNotRunning(w)
		return
	}
	WriteJSON(w, http.StatusAccepted, ChatSessionResponse{Object: "chat_session", Data: renderChatSession(session, h.agentChatSnapshotConfig())})
}

func (h *Handler) HandleCloseChatSession(w http.ResponseWriter, r *http.Request) {
	session, ok, err := h.agentChat.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "agent chat session not found")
		return
	}
	cancelCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	settled := h.agentChatLive.cancelRunAndWait(cancelCtx, session.ID)
	cancel()
	if !settled {
		writeChatSessionStopping(w)
		return
	}
	result, err := h.chatApplication().CloseNativeSession(r.Context(), chatapp.CloseNativeSessionCommand{
		Session: session,
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, ChatSessionResponse{Object: "chat_session", Data: renderChatSession(result.Session, h.agentChatSnapshotConfig())})
}

func (h *Handler) HandleSetAgentChatConfigOption(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("id"))
	configID := strings.TrimSpace(r.PathValue("config_id"))
	session, ok, err := h.agentChat.Get(r.Context(), sessionID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "agent chat session not found")
		return
	}
	if !isExternalChatSession(session) {
		WriteError(w, http.StatusConflict, errCodeRuntimeMismatch, "agent chat config options are only available for external-agent sessions")
		return
	}
	var req SetAgentChatConfigOptionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := h.chatApplication().SetConfigOption(r.Context(), chatapp.SetConfigOptionCommand{
		Session:  session,
		ConfigID: configID,
		Value:    req.Value,
	})
	if err != nil {
		if errors.Is(err, chatapp.ErrStoreNotConfigured) || errors.Is(err, chatapp.ErrRunnerNotConfigured) {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
			return
		}
		if writeChatAppError(w, err) {
			return
		}
		writeAgentChatConfigOptionError(w, session, err)
		return
	}
	h.agentChatLive.publishSession(result.Session)
	WriteJSON(w, http.StatusOK, ChatSessionResponse{Object: "chat_session", Data: renderChatSession(result.Session, h.agentChatSnapshotConfig())})
}

func (h *Handler) HandleSetAgentChatSettings(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("id"))
	session, ok, err := h.agentChat.Get(r.Context(), sessionID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "agent chat session not found")
		return
	}
	if isExternalChatSession(session) {
		WriteError(w, http.StatusConflict, errCodeRuntimeMismatch, "Hecate Chat settings are not available for external-agent sessions")
		return
	}
	var req SetAgentChatSettingsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := h.chatApplication().SetHecateSettings(r.Context(), chatapp.SetHecateSettingsCommand{
		Session:    session,
		RTKEnabled: req.RTKEnabled,
	})
	if err != nil {
		if errors.Is(err, chatapp.ErrStoreNotConfigured) {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
			return
		}
		if writeChatAppError(w, err) {
			return
		}
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	h.agentChatLive.publishSession(result.Session)
	WriteJSON(w, http.StatusOK, ChatSessionResponse{Object: "chat_session", Data: renderChatSession(result.Session, h.agentChatSnapshotConfig())})
}

func writeAgentChatPrepareError(w http.ResponseWriter, adapterName string, err error) {
	if errors.Is(err, agentadapters.ErrLaunchModelRequired) {
		WriteErrorDetails(w, http.StatusBadRequest, errCodeModelRequired, err.Error(), ErrorDetails{
			UserMessage:    "Choose a model before starting this external-agent chat.",
			OperatorAction: "Use the model picker in the composer, then start the chat again.",
		})
		return
	}
	if errors.Is(err, agentadapters.ErrRemoteCredentialRequired) {
		WriteErrorDetails(w, http.StatusForbidden, errCodeForbidden, err.Error(), ErrorDetails{
			UserMessage:    "This hosted runtime needs remote-safe credentials for the selected external agent.",
			OperatorAction: "Set a scoped API key or enterprise token for this runtime instead of using local CLI login files.",
		})
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		WriteErrorDetails(w, http.StatusGatewayTimeout, errCodeAgentAdapterUnavailable, err.Error(), ErrorDetails{
			UserMessage:    "The external agent did not respond while starting the session.",
			OperatorAction: "Try again, or test the adapter from Settings if it keeps hanging.",
		})
		return
	}
	WriteError(w, http.StatusBadGateway, errCodeAgentAdapterUnavailable, agentadapters.NormalizeError(adapterName, err))
}

func writeAgentChatConfigOptionError(w http.ResponseWriter, session chat.Session, err error) {
	switch {
	case errors.Is(err, agentadapters.ErrSessionNotActive):
		WriteErrorDetails(w, http.StatusConflict, errCodeSessionNotRunning, err.Error(), ErrorDetails{
			UserMessage:    "This external-agent session is not active anymore.",
			OperatorAction: "Start a new external-agent chat before changing adapter controls.",
		})
	case errors.Is(err, context.DeadlineExceeded):
		WriteErrorDetails(w, http.StatusGatewayTimeout, errCodeAgentAdapterUnavailable, err.Error(), ErrorDetails{
			UserMessage:    "The external agent did not respond while changing that control.",
			OperatorAction: "Try again, or restart the adapter if it stays stuck.",
		})
	default:
		WriteErrorDetails(w, http.StatusBadGateway, errCodeAgentAdapterUnavailable, agentadapters.NormalizeError(agentChatAdapterName(session.AgentID), err), ErrorDetails{
			UserMessage:    "The external agent could not change that control.",
			OperatorAction: "Check the adapter status, then retry the control change.",
		})
	}
}

func agentChatAdapterName(adapterID string) string {
	adapter, ok := agentadapters.BuiltInByID(adapterID)
	if !ok || adapter.Name == "" {
		return "Agent adapter"
	}
	return adapter.Name
}

func isExternalChatSession(session chat.Session) bool {
	return session.AgentID != "" && session.AgentID != chat.DefaultAgentID
}

func isHecateChatSession(session chat.Session) bool {
	return !isExternalChatSession(session)
}

func (h *Handler) cancelHecateChatTaskRun(ctx context.Context, session chat.Session) (types.TaskRun, bool, error) {
	if !isHecateChatSession(session) || h.taskStore == nil || h.taskRunner == nil || session.TaskID == "" || session.LatestRunID == "" {
		return types.TaskRun{}, false, nil
	}
	task, found, err := h.taskStore.GetTask(ctx, session.TaskID)
	if err != nil {
		return types.TaskRun{}, false, err
	}
	if !found {
		return types.TaskRun{}, false, nil
	}
	run, err := h.taskRunner.CancelRun(ctx, task, session.LatestRunID, "operator")
	if err != nil {
		if strings.Contains(err.Error(), "already terminal") {
			return types.TaskRun{}, false, nil
		}
		return types.TaskRun{}, true, err
	}
	return run, true, nil
}

func (h *Handler) HandleCreateChatMessage(w http.ResponseWriter, r *http.Request) {
	session, ok, err := h.agentChat.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "agent chat session not found")
		return
	}
	var req CreateChatMessageRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	limits := h.agentChatSnapshotConfig()
	admission, err := h.chatApplication().AdmitMessage(chatapp.AdmitMessageCommand{
		Session:       session,
		Content:       req.Content,
		ExecutionMode: req.ExecutionMode,
		ToolsEnabled:  req.ToolsEnabled,
		Limits: chatapp.MessageLimits{
			MaxTurnsPerSession: limits.MaxTurnsPerSession,
			MaxSessionDuration: limits.MaxSessionDuration,
			IdleTimeout:        limits.IdleTimeout,
		},
		Now: time.Now(),
	})
	if err != nil {
		if writeChatAdmissionError(w, err) {
			return
		}
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !isExternalChatSession(session) {
		if busy, runStatus := h.hecateAgentSessionBusy(r.Context(), session); busy {
			writeHecateAgentBusy(w, session, runStatus)
			return
		}
	}
	hecateToolsUnavailable := false
	if admission.ToolsEnabled && admission.ExecutionMode == chat.ExecutionModeHecateTask && !isExternalChatSession(session) {
		// Capability-driven downgrade lets a Hecate turn with tools on fall
		// back to the direct model path without changing its runtime owner.
		hecateToolsUnavailable = h.hecateTaskShouldFallbackToDirectModel(r.Context(), session, req)
	}
	plan := chatapp.ResolveMessageDispatch(session, *admission, hecateToolsUnavailable)
	switch plan.Route {
	case chatapp.MessageDispatchHecateTask, chatapp.MessageDispatchDirectModel:
		// One unified entry point for every Hecate-side turn,
		// regardless of tools_enabled. handleCreateHecateChatMessage
		// branches at the top: tools-off delegates to
		// `handleDirectModelTurn`; tools-on runs the existing
		// agent_loop task-creation path.
		h.handleCreateHecateChatMessage(w, r, session, req, plan.ToolsEnabled)
		return
	case chatapp.MessageDispatchExternalAgent:
		h.handleCreateExternalAgentChatMessage(w, r, session, req, plan)
		return
	}
	writeChatExecutionModeInvalid(w)
}

func (h *Handler) handleCreateExternalAgentChatMessage(w http.ResponseWriter, r *http.Request, session chat.Session, req CreateChatMessageRequest, plan chatapp.MessageDispatchPlan) {
	adapter, ok := agentadapters.BuiltInByID(session.AgentID)
	if !ok {
		writeAgentChatAdapterNotFound(w, session.AgentID)
		return
	}
	assistantID := newChatID("msg")
	runID := newChatID("agent_run")
	trace, traceCtx := h.startAgentChatTrace(w, r)
	defer trace.Finalize()

	runCtx, cancel := context.WithTimeout(traceCtx, agentChatTimeout)
	if !h.agentChatLive.registerRun(session.ID, cancel) {
		cancel()
		WriteErrorDetails(w, http.StatusConflict, errCodeAgentSessionBusy, "agent chat session is already running", ErrorDetails{
			UserMessage:    "This chat is already running.",
			OperatorAction: "Wait for the active run to finish or stop it before sending another message.",
		})
		return
	}
	defer h.agentChatLive.clearRun(session.ID)
	defer cancel()

	updated, err := h.agentChat.AppendMessage(r.Context(), session.ID, chat.Message{
		ID:            newChatID("msg"),
		ExecutionMode: chat.ExecutionModeExternalAgent,
		Role:          "user",
		Content:       plan.Content,
		CreatedAt:     time.Now().UTC(),
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	h.agentChatLive.publishSession(updated)
	startedAt := time.Now().UTC()
	trace.Record(telemetry.EventAgentChatRunStarted, agentChatTraceAttrs(session, adapter, runID, assistantID, map[string]any{
		telemetry.AttrHecateRunStatus: "running",
	}))
	contextPacket := h.externalAgentContextPacket(r.Context(), session, adapter.Name)
	contextPacket.ID = newChatID("ctx")
	contextPacket = chatcontext.Normalize(contextPacket, chatcontext.MergeRefs(
		chatcontext.ChatMessageRefs(session.ID, assistantID, session.ProjectID),
		chatcontext.TaskRunRefs("", runID, session.ProjectID),
	))
	updated, err = h.agentChat.AppendMessage(r.Context(), session.ID, chat.Message{
		ID:            assistantID,
		ExecutionMode: chat.ExecutionModeExternalAgent,
		RunID:         runID,
		RequestID:     RequestIDFromContext(r.Context()),
		TraceID:       trace.TraceID,
		SpanID:        trace.RootSpanID(),
		Role:          "assistant",
		Content:       "",
		AgentID:       adapter.ID,
		AgentName:     adapter.Name,
		DriverKind:    agentadapters.DriverKindACP,
		Status:        "running",
		CostMode:      adapter.CostMode,
		Workspace:     session.Workspace,
		Context:       contextPacket,
		CreatedAt:     time.Now().UTC(),
		StartedAt:     startedAt,
		Activities: []chat.Activity{
			newChatActivity("running", "running", "Running", "Waiting for ACP output"),
		},
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	h.agentChatLive.publishSession(updated)

	outputSeen := false
	runner := h.agentChatRunner
	if runner == nil {
		// Defensive: the constructor in NewHandler always sets
		// agentChatRunner. This branch only fires for programmer error
		// (e.g. a test handler built without it). The fallback runner
		// has no approval coordinator installed and falls back to
		// auto-approve. Tighten in a follow-up if the fallback remains
		// in use.
		fallback := agentadapters.NewSessionManager()
		fallback.SetSecretCipher(h.secretCipher)
		runner = fallback
	}
	// streamFlush persists a coalesced batch of streamed updates in a
	// single chat.UpdateMessage and publishes once. Content is
	// last-write-wins (the full accumulated transcript); activities are
	// applied in arrival order via the same per-record merge the
	// adapter callbacks used to do inline.
	//
	// It persists under r.Context(), not runCtx: the trailing flush
	// (from the coalescer's timer or close()) can run as Run is
	// returning, so on cancel/deadline paths runCtx is already done and
	// persisting under it would silently drop the final buffered batch.
	// The terminal finalize below recovers content (it re-sets
	// message.Content from result.Output) but only appends activity
	// rows, so a dropped activity batch would be lost for good.
	// r.Context() matches that finalize and outlives run cancellation.
	streamFlush := func(display string, haveContent bool, activities []agentadapters.Activity) {
		updated, updateErr := h.agentChat.UpdateMessage(r.Context(), session.ID, assistantID, func(message *chat.Message) {
			if haveContent {
				message.Content = display
				if strings.TrimSpace(display) != "" && !outputSeen {
					message.Activities = append(message.Activities, newChatActivity("output", "running", "ACP output", "Streaming normalized transcript"))
					trace.Record(telemetry.EventAgentChatOutputStarted, agentChatTraceAttrs(session, adapter, runID, assistantID, map[string]any{
						telemetry.AttrHecateRunStatus:        "running",
						telemetry.AttrHecateAgentOutputBytes: int64(len(display)),
					}))
					outputSeen = true
				}
			}
			for _, activity := range activities {
				message.Activities = mergeChatActivity(message.Activities, agentChatActivityFromAdapter(activity))
			}
		})
		if updateErr == nil {
			h.agentChatLive.publishSession(updated)
		}
	}
	// Coalesce the per-token OnOutput/OnActivity callbacks into at most
	// one persist+publish per window. close() after Run flushes the
	// trailing batch so buffered activities land before the finalize.
	streamCoalescer := newChatStreamCoalescer(agentChatStreamCoalesceInterval, streamFlush)
	result, runErr := runner.Run(runCtx, agentadapters.RunRequest{
		SessionID:               session.ID,
		AdapterID:               adapter.ID,
		Workspace:               session.Workspace,
		PreviousNativeSessionID: session.NativeSessionID,
		ConfigOptions:           session.ConfigOptions,
		MCPServers:              session.MCPServers,
		Prompt:                  plan.Content,
		Timeout:                 agentChatTimeout,
		MaxOutputBytes:          agentChatMaxOutputBytes,
		OnOutput: func(display string) {
			streamCoalescer.output(display)
		},
		OnActivity: func(activity agentadapters.Activity) {
			streamCoalescer.activity(activity)
		},
	})
	streamCoalescer.close()
	outcome := newExternalAgentTurnOutcome(adapter.Name, result, runErr, runCtx.Err(), startedAt, time.Now().UTC())
	status := outcome.Status
	output := outcome.Output
	errorText := outcome.ErrorText
	startedAt = outcome.StartedAt
	completedAt := outcome.CompletedAt
	if result.DiffStat != "" {
		trace.Record(telemetry.EventAgentChatFilesChanged, agentChatTraceAttrs(session, adapter, runID, assistantID, map[string]any{
			telemetry.AttrHecateRunStatus:         status,
			telemetry.AttrHecateAgentDiffCaptured: true,
		}))
	}
	terminalAttrs := agentChatTraceAttrs(session, adapter, runID, assistantID, map[string]any{
		telemetry.AttrHecateRunStatus:            status,
		telemetry.AttrHecateRunDurationMS:        outcome.DurationMS,
		telemetry.AttrHecateAgentOutputBytes:     int64(len(output)),
		telemetry.AttrHecateAgentRawOutputBytes:  int64(len(result.RawOutput)),
		telemetry.AttrHecateAgentDiffCaptured:    result.Diff != "",
		telemetry.AttrHecateAgentDriverKind:      result.DriverKind,
		telemetry.AttrHecateAgentNativeSessionID: result.NativeSessionID,
		"process.exit.code":                      result.ExitCode,
	})
	if strings.TrimSpace(result.StopReason) != "" {
		terminalAttrs["hecate.agent.stop_reason"] = result.StopReason
	}
	if runErr != nil {
		terminalAttrs[telemetry.AttrHecateResult] = telemetry.ResultError
		terminalAttrs[telemetry.AttrHecateErrorKind] = telemetry.ErrorKindOther
		terminalAttrs[telemetry.AttrErrorType] = "agent_adapter_failed"
		terminalAttrs[telemetry.AttrErrorMessage] = outcome.DisplayErr
	}
	trace.Record(agentChatTerminalEvent(status), terminalAttrs)
	driverKind := result.DriverKind
	if driverKind == "" {
		driverKind = adapter.Kind
	}
	h.agentChatMetrics.RecordRun(traceCtx, telemetry.AgentChatRunMetricsRecord{
		AdapterID:  adapter.ID,
		DriverKind: driverKind,
		Status:     status,
		Result:     outcome.ResultLabel,
		DurationMS: outcome.DurationMS,
	})
	if status == "cancelled" {
		// Reason classification: cancelRun / cancelRunAndWait stamp
		// "operator" before tripping the cancel func; if nothing was
		// stamped, the parent r.Context() died first, which we label
		// "request_cancelled". Shutdown-path cancels fire from
		// SessionManager.Shutdown directly and don't reach this branch.
		reason := h.agentChatLive.cancelReasonFor(session.ID)
		if reason == "" {
			reason = "request_cancelled"
		}
		h.agentChatMetrics.RecordChatCancelled(traceCtx, telemetry.AgentChatCancelledRecord{
			AdapterID: adapter.ID,
			Reason:    reason,
		})
	}

	updated, err = h.agentChat.UpdateMessage(r.Context(), session.ID, assistantID, func(message *chat.Message) {
		if output != "" {
			message.Content = output
		}
		message.RawOutput = result.RawOutput
		if strings.TrimSpace(message.RawOutput) == "" && runErr != nil {
			message.RawOutput = runErr.Error()
		}
		message.DriverKind = result.DriverKind
		message.NativeSessionID = result.NativeSessionID
		message.Status = status
		message.ExitCode = result.ExitCode
		message.DiffStat = result.DiffStat
		message.Diff = result.Diff
		message.StartedAt = startedAt
		message.CompletedAt = completedAt
		message.Error = errorText
		message.Usage = agentChatUsageFromResult(result.Usage)
		if result.SessionResumed {
			message.Activities = append([]chat.Activity{newChatActivity("resumed", "completed", "Resumed external session", adapter.Name+" restored "+result.NativeSessionID)}, message.Activities...)
		} else if result.SessionStarted {
			activities := []chat.Activity{newChatActivity("started", "completed", "Starting external agent", adapter.Name+" in "+session.Workspace)}
			if result.SessionRecovery != "" {
				activities = append(activities, newChatActivity("recovered", "completed", "Started fresh external session", result.SessionRecovery))
			}
			message.Activities = append(activities, message.Activities...)
		}
		if result.DiffStat != "" {
			message.Activities = append(message.Activities, newChatActivity("files_changed", "completed", "Files changed", result.DiffStat))
		}
		if activity, ok := externalAgentStopReasonActivity(result.StopReason); ok {
			message.Activities = append(message.Activities, activity)
		}
		message.Context = chatcontext.Normalize(message.Context, chatcontext.MergeRefs(
			chatcontext.ChatMessageRefs(session.ID, assistantID, session.ProjectID),
			chatcontext.TaskRunRefs("", runID, session.ProjectID),
		))
		message.Activities = append(message.Activities, newChatActivity(status, status, finalChatActivityTitle(status), errorText))
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if result.DriverKind != "" || result.NativeSessionID != "" || result.ConfigOptions != nil || result.AvailableCommandsKnown {
		updated, err = h.agentChat.UpdateSession(r.Context(), session.ID, func(item *chat.Session) {
			if result.DriverKind != "" {
				item.DriverKind = result.DriverKind
			}
			if result.NativeSessionID != "" {
				item.NativeSessionID = result.NativeSessionID
			}
			if result.ConfigOptions != nil {
				item.ConfigOptions = result.ConfigOptions
			}
			if result.AvailableCommandsKnown {
				item.AvailableCommands = result.AvailableCommands
			}
		})
		if err != nil {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
			return
		}
	}
	// Increment after every completed round-trip, even when no ceiling is set.
	// Best-effort: the turn result itself was already committed above.
	if inc, incErr := h.agentChat.UpdateSession(r.Context(), session.ID, func(item *chat.Session) {
		item.TurnsUsed++
	}); incErr == nil {
		updated = inc
	} else {
		h.logger.WarnContext(r.Context(), "chat.turn_counter_increment_failed", "session_id", session.ID, "error", incErr)
	}
	h.reconcileProjectAssignmentsForChat(r.Context(), updated)
	h.agentChatLive.publishSession(updated)
	WriteJSON(w, http.StatusOK, ChatSessionResponse{Object: "chat_session", Data: renderChatSession(updated, h.agentChatSnapshotConfig())})
}

func (h *Handler) hecateTaskShouldFallbackToDirectModel(ctx context.Context, session chat.Session, req CreateChatMessageRequest) bool {
	provider := strings.TrimSpace(req.Provider)
	if provider == "" {
		provider = session.Provider
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = session.Model
	}
	if model == "" {
		return false
	}
	caps, err := h.resolveModelCapabilities(ctx, provider, model)
	if err != nil {
		return false
	}
	return !modelcaps.ToolCapable(caps)
}

// handleDirectModelTurn runs the tools-off sub-path of
// handleCreateHecateChatMessage. Called only from inside that
// handler when toolsEnabled is false (either because the client
// asked for tools off or because the capability-downgrade flipped
// it). Not invoked directly by the dispatcher.
func (h *Handler) handleDirectModelTurn(w http.ResponseWriter, r *http.Request, session chat.Session, req CreateChatMessageRequest) {
	if busy, runStatus := h.hecateAgentSessionBusy(r.Context(), session); busy {
		writeHecateAgentBusy(w, session, runStatus)
		return
	}

	provider := strings.TrimSpace(req.Provider)
	if provider == "" {
		provider = session.Provider
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = session.Model
	}
	if model == "" {
		writeAgentChatModelRequired(w, "model")
		return
	}
	caps, err := h.resolveModelCapabilities(r.Context(), provider, model)
	if err != nil {
		writeAgentChatModelResolutionError(w, err)
		return
	}
	content := strings.TrimSpace(req.Content)
	if compacted, err := h.compactChatSessionForModelTurn(r.Context(), session, provider, model); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	} else if compacted.ID != "" {
		if compacted.ContextSummary.ThroughMessageID != session.ContextSummary.ThroughMessageID || compacted.ContextSummary.Content != session.ContextSummary.Content {
			h.agentChatLive.publishSession(compacted)
		}
		session = compacted
	}
	assistantID := newChatID("msg")
	runID := newChatID("model_run")
	startedAt := time.Now().UTC()
	runCtx, cancel := context.WithTimeout(r.Context(), agentChatTimeout)
	if !h.agentChatLive.registerRun(session.ID, cancel) {
		cancel()
		WriteErrorDetails(w, http.StatusConflict, errCodeAgentSessionBusy, "chat session is already running", ErrorDetails{
			UserMessage:    "This chat is already running.",
			OperatorAction: "Wait for the active run to finish or stop it before sending another message.",
		})
		return
	}
	defer h.agentChatLive.clearRun(session.ID)
	defer cancel()

	segmentID := modelSegmentID(session, provider, model)
	updated, err := h.agentChat.AppendMessage(r.Context(), session.ID, chat.Message{
		ID:            newChatID("msg"),
		ExecutionMode: chat.ExecutionModeHecateTask,
		// The model-chat handler dispatches when the operator submitted
		// with tools off (or when the runtime downgraded a hecate_task
		// turn because the model can't run tools). Either way, the
		// persisted Message records ToolsEnabled=false so a future
		// read against this row recovers the original intent without
		// having to parse the execution_mode string.
		ToolsEnabled: false,
		SegmentID:    segmentID,
		Provider:     provider,
		Model:        model,
		Capabilities: caps,
		Role:         "user",
		Content:      content,
		CreatedAt:    startedAt,
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	h.agentChatLive.publishSession(updated)

	effectiveSystemPrompt := h.hecateChatEffectiveSystemPrompt(r.Context(), session, req.SystemPrompt)
	history := agentChatModelHistory(session, effectiveSystemPrompt, content)
	contextPacket := h.directModelContextPacket(r.Context(), session, provider, model, effectiveSystemPrompt)
	contextPacket.ID = newChatID("ctx")
	contextPacket = chatcontext.Normalize(contextPacket, chatcontext.MergeRefs(
		chatcontext.ChatMessageRefs(session.ID, assistantID, session.ProjectID),
		chatcontext.TaskRunRefs("", runID, session.ProjectID),
	))
	updated, err = h.agentChat.AppendMessage(r.Context(), session.ID, chat.Message{
		ID:            assistantID,
		ExecutionMode: chat.ExecutionModeHecateTask,
		ToolsEnabled:  false,
		SegmentID:     segmentID,
		RunID:         runID,
		RequestID:     RequestIDFromContext(r.Context()),
		Provider:      provider,
		Model:         model,
		Capabilities:  caps,
		Role:          "assistant",
		Content:       "",
		Status:        "running",
		CostMode:      "hecate",
		Workspace:     session.Workspace,
		Context:       contextPacket,
		CreatedAt:     startedAt,
		StartedAt:     startedAt,
		Activities: []chat.Activity{
			newChatActivity("model_request", "running", "Model request", "Waiting for provider response"),
		},
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	h.agentChatLive.publishSession(updated)

	chatReq := types.ChatRequest{
		RequestID: RequestIDFromContext(r.Context()),
		Model:     model,
		Messages:  history,
		Scope:     requestscope.Build(provider),
	}
	result, runErr := h.service.HandleChat(runCtx, chatReq)
	completedAt := time.Now().UTC()
	outcome := newDirectModelTurnOutcome(result, runErr, runCtx.Err())
	status := outcome.Status
	output := outcome.Output
	errorText := outcome.ErrorText
	updated, err = h.agentChat.UpdateMessage(r.Context(), session.ID, assistantID, func(message *chat.Message) {
		message.Status = status
		message.Content = output
		message.Error = errorText
		message.StartedAt = startedAt
		message.CompletedAt = completedAt
		if result != nil {
			message.TraceID = result.Metadata.TraceID
			message.SpanID = result.Metadata.SpanID
			if result.Metadata.Provider != "" {
				message.Provider = result.Metadata.Provider
			}
			if result.Metadata.Model != "" {
				message.Model = result.Metadata.Model
			}
			message.Usage = chat.Usage{
				ContextUsed: result.Metadata.TotalTokens,
			}
		}
		message.Context = chatcontext.Normalize(message.Context, chatcontext.MergeRefs(
			chatcontext.ChatMessageRefs(session.ID, assistantID, session.ProjectID),
			chatcontext.TaskRunRefs("", message.RunID, session.ProjectID),
		))
		message.Activities = append(message.Activities, newChatActivity(status, status, finalChatActivityTitle(status), errorText))
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if inc, incErr := h.agentChat.UpdateSession(r.Context(), session.ID, func(item *chat.Session) {
		item.Provider = provider
		item.Model = model
		item.Capabilities = caps
		item.TurnsUsed++
	}); incErr == nil {
		updated = inc
	}
	h.agentChatLive.publishSession(updated)
	if runErr != nil {
		WriteJSON(w, http.StatusOK, ChatSessionResponse{Object: "chat_session", Data: renderChatSession(updated, h.agentChatSnapshotConfig())})
		return
	}
	WriteJSON(w, http.StatusOK, ChatSessionResponse{Object: "chat_session", Data: renderChatSession(updated, h.agentChatSnapshotConfig())})
}

func (h *Handler) compactChatSessionForModelTurn(ctx context.Context, session chat.Session, provider, model string) (chat.Session, error) {
	return h.compactChatSession(ctx, session.ID, compactChatSessionOptions{
		RetainMessages: agentChatAutoCompactRetainMessages,
		MinMessages:    agentChatAutoCompactMinMessages,
		Provider:       provider,
		Model:          model,
		Now:            time.Now().UTC(),
	})
}

func agentChatModelHistory(session chat.Session, systemPrompt, content string) []types.Message {
	messages := make([]types.Message, 0, len(session.Messages))
	if systemPrompt = strings.TrimSpace(systemPrompt); systemPrompt != "" {
		messages = append(messages, types.Message{Role: "system", Content: systemPrompt})
	}
	skipThroughIndex := compactedTranscriptMessageIndex(session.Messages, session.ContextSummary.ThroughMessageID)
	if compacted := chat.TranscriptSummaryPrompt(session.ContextSummary); compacted != "" && skipThroughIndex >= 0 {
		messages = append(messages, types.Message{Role: "system", Content: compacted})
	}
	for i, message := range session.Messages {
		if skipThroughIndex >= 0 && i <= skipThroughIndex {
			continue
		}
		if message.Role != "user" && message.Role != "assistant" {
			continue
		}
		if message.Role == "assistant" && !isTerminalAgentChatStatus(message.Status) {
			continue
		}
		text := strings.TrimSpace(message.Content)
		if text == "" {
			continue
		}
		messages = append(messages, types.Message{Role: message.Role, Content: text})
	}
	messages = append(messages, types.Message{Role: "user", Content: content})
	return messages
}

func compactedTranscriptMessageIndex(messages []chat.Message, throughMessageID string) int {
	throughMessageID = strings.TrimSpace(throughMessageID)
	if throughMessageID == "" {
		return -1
	}
	for i, message := range messages {
		if message.ID == throughMessageID {
			return i
		}
	}
	return -1
}

func modelSegmentID(session chat.Session, provider, model string) string {
	for i := len(session.Messages) - 1; i >= 0; i-- {
		message := session.Messages[i]
		if message.ExecutionMode != chat.ExecutionModeHecateTask || message.ToolsEnabled {
			break
		}
		if message.Provider == provider && message.Model == model && message.SegmentID != "" {
			return message.SegmentID
		}
	}
	return newChatID("segment")
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
	return gitrunner.NewLocalRunner().CurrentRef(context.Background(), workspace)
}

func newChatID(prefix string) string {
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
