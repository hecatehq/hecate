package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/hecate/agent-runtime/internal/agentadapters"
	"github.com/hecate/agent-runtime/internal/chat"
	"github.com/hecate/agent-runtime/internal/requestscope"
	"github.com/hecate/agent-runtime/internal/telemetry"
	"github.com/hecate/agent-runtime/pkg/types"
)

const (
	agentChatTimeout             = 30 * time.Minute
	agentChatPrepareTimeout      = 10 * time.Second
	agentChatConfigOptionTimeout = 10 * time.Second
	agentChatMaxOutputBytes      = 4 * 1024 * 1024
)

func (h *Handler) HandleChatSessions(w http.ResponseWriter, r *http.Request) {
	items, err := h.agentChat.List(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	data := make([]ChatSessionSummaryItem, 0, len(items))
	for _, item := range items {
		data = append(data, renderChatSessionSummary(item))
	}
	WriteJSON(w, http.StatusOK, ChatSessionsResponse{Object: "chat_sessions", Data: data})
}

func (h *Handler) HandleCreateChatSession(w http.ResponseWriter, r *http.Request) {
	var req CreateChatSessionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	runtimeKind := normalizeAgentChatRuntimeKind(req.RuntimeKind, req.AdapterID)
	if !isValidAgentChatRuntimeKind(runtimeKind) {
		writeAgentChatRuntimeKindInvalid(w)
		return
	}
	workspace := strings.TrimSpace(req.Workspace)
	if runtimeKind != "model" {
		if workspace == "" {
			writeAgentChatWorkspaceRequired(w, runtimeKind)
			return
		}
		var err error
		workspace, err = agentadapters.ValidateWorkspace(workspace)
		if err != nil {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
			return
		}
	} else if workspace != "" {
		if resolved, err := agentadapters.ValidateWorkspace(workspace); err == nil {
			workspace = resolved
		}
	}
	workspaceBranch := workspaceGitBranch(workspace)
	title := strings.TrimSpace(req.Title)

	session := chat.Session{
		ID:              newChatID("chat"),
		Title:           title,
		RuntimeKind:     runtimeKind,
		Workspace:       workspace,
		WorkspaceBranch: workspaceBranch,
		RTKEnabled:      req.RTKEnabled,
	}
	var err error
	var externalAdapter agentadapters.Adapter
	switch runtimeKind {
	case "model":
		provider := strings.TrimSpace(req.Provider)
		model := strings.TrimSpace(req.Model)
		if model == "" {
			writeAgentChatModelRequired(w, "model")
			return
		}
		caps, err := h.resolveModelCapabilities(r.Context(), provider, model)
		if err != nil {
			writeAgentChatModelResolutionError(w, err)
			return
		}
		if session.Title == "" {
			session.Title = "Hecate Chat"
		}
		session.Provider = provider
		session.Model = model
		session.Capabilities = caps
	case "external_agent":
		adapter, ok := agentadapters.BuiltInByID(strings.TrimSpace(req.AdapterID))
		if !ok {
			writeAgentChatAdapterNotFound(w, req.AdapterID)
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
		session.AdapterID = adapter.ID
		session.DriverKind = agentadapters.DriverKindACP
	case "agent":
		provider := strings.TrimSpace(req.Provider)
		model := strings.TrimSpace(req.Model)
		if model == "" {
			writeAgentChatModelRequired(w, "agent")
			return
		}
		caps, err := h.resolveModelCapabilities(r.Context(), provider, model)
		if err != nil {
			writeAgentChatModelResolutionError(w, err)
			return
		}
		if session.Title == "" {
			session.Title = "Hecate Agent chat"
		}
		session.Provider = provider
		session.Model = model
		session.Capabilities = caps
	default:
		writeAgentChatRuntimeKindInvalid(w)
		return
	}
	session, err = h.agentChat.Create(r.Context(), session)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if runtimeKind == "external_agent" {
		prepareCtx, cancel := context.WithTimeout(r.Context(), agentChatPrepareTimeout)
		result, prepareErr := h.agentChatRunner.PrepareSession(prepareCtx, agentadapters.PrepareSessionRequest{
			SessionID:               session.ID,
			AdapterID:               session.AdapterID,
			Workspace:               session.Workspace,
			PreviousNativeSessionID: session.NativeSessionID,
		})
		cancel()
		if prepareErr != nil {
			_ = h.agentChat.Delete(context.Background(), session.ID)
			writeAgentChatPrepareError(w, externalAdapter.Name, prepareErr)
			return
		}
		session, err = h.agentChat.UpdateSession(r.Context(), session.ID, func(item *chat.Session) {
			item.DriverKind = result.DriverKind
			item.NativeSessionID = result.NativeSessionID
			item.ConfigOptions = result.ConfigOptions
		})
		if err != nil {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), agentChatPrepareTimeout)
			_ = h.agentChatRunner.CloseSession(cleanupCtx, session.ID)
			cleanupCancel()
			_ = h.agentChat.Delete(context.Background(), session.ID)
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
			return
		}
	}
	WriteJSON(w, http.StatusOK, ChatSessionResponse{Object: "chat_session", Data: renderChatSession(session, h.agentChatSnapshotConfig())})
}

func normalizeAgentChatRuntimeKind(runtimeKind, adapterID string) string {
	runtimeKind = strings.TrimSpace(runtimeKind)
	switch runtimeKind {
	case "":
		if strings.TrimSpace(adapterID) != "" {
			return "external_agent"
		}
		return "model"
	default:
		return runtimeKind
	}
}

func isValidAgentChatRuntimeKind(runtimeKind string) bool {
	switch runtimeKind {
	case "model", "agent", "external_agent":
		return true
	default:
		return false
	}
}

func (h *Handler) HandleChatSession(w http.ResponseWriter, r *http.Request) {
	session, ok, err := h.agentChat.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "agent chat session not found")
		return
	}
	WriteJSON(w, http.StatusOK, ChatSessionResponse{Object: "chat_session", Data: renderChatSession(session, h.agentChatSnapshotConfig())})
}

func (h *Handler) HandleUpdateChatSession(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("id"))
	if sessionID == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "session id is required")
		return
	}
	var req UpdateChatSessionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Title == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "request must include title")
		return
	}
	title := strings.TrimSpace(*req.Title)
	if title == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "title cannot be set to an empty string")
		return
	}
	if _, ok, err := h.agentChat.Get(r.Context(), sessionID); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	} else if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "agent chat session not found")
		return
	}
	updated, err := h.agentChat.UpdateSession(r.Context(), sessionID, func(item *chat.Session) {
		item.Title = title
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	h.agentChatLive.publishSession(updated)
	WriteJSON(w, http.StatusOK, ChatSessionResponse{Object: "chat_session", Data: renderChatSession(updated, h.agentChatSnapshotConfig())})
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
	sendAgentChatSSE(w, flusher, "snapshot", ChatSessionResponse{
		Object: "chat_session",
		Data:   renderChatSession(session, h.agentChatSnapshotConfig()),
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
			switch payload.Type {
			case AgentChatLiveEventSessionUpdate:
				if payload.SessionUpdate == nil {
					continue
				}
				sendAgentChatSSE(w, flusher, "snapshot", *payload.SessionUpdate)
				if payload.SessionUpdate.Data.Status == "running" {
					observedRun = true
				}
				if observedRun && isTerminalAgentChatStatus(payload.SessionUpdate.Data.Status) {
					sendAgentChatSSE(w, flusher, "done", *payload.SessionUpdate)
					return
				}
			case AgentChatLiveEventApprovalRequested:
				if payload.ApprovalRequested == nil {
					continue
				}
				sendAgentChatSSE(w, flusher, string(AgentChatLiveEventApprovalRequested), *payload.ApprovalRequested)
			case AgentChatLiveEventApprovalResolved:
				if payload.ApprovalResolved == nil {
					continue
				}
				sendAgentChatSSE(w, flusher, string(AgentChatLiveEventApprovalResolved), *payload.ApprovalResolved)
			}
		case <-heartbeat.C:
			fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

func (h *Handler) HandleDeleteChatSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	cancelCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	settled := h.agentChatLive.cancelRunAndWait(cancelCtx, sessionID)
	cancel()
	if !settled {
		writeChatSessionStopping(w)
		return
	}
	if h.agentChatRunner != nil {
		_ = h.agentChatRunner.CloseSession(r.Context(), sessionID)
	}
	if err := h.agentChat.Delete(r.Context(), sessionID); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	if renderAgentChatRuntimeKind(session) == "agent" && h.taskStore != nil && h.taskRunner != nil && session.TaskID != "" && session.LatestRunID != "" {
		task, found, err := h.taskStore.GetTask(r.Context(), session.TaskID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
			return
		}
		if found {
			run, err := h.taskRunner.CancelRun(r.Context(), task, session.LatestRunID, "operator")
			if err == nil {
				updated, updateErr := h.agentChat.UpdateSession(r.Context(), session.ID, func(item *chat.Session) {
					item.Status = run.Status
				})
				if updateErr == nil {
					h.agentChatLive.publishSession(updated)
					WriteJSON(w, http.StatusAccepted, ChatSessionResponse{Object: "chat_session", Data: renderChatSession(updated, h.agentChatSnapshotConfig())})
					return
				}
			}
			if err != nil && !strings.Contains(err.Error(), "already terminal") {
				WriteError(w, http.StatusConflict, errCodeConflict, err.Error())
				return
			}
		}
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
	if h.agentChatRunner != nil {
		_ = h.agentChatRunner.CloseSession(r.Context(), session.ID)
	}
	updated, err := h.agentChat.UpdateSession(r.Context(), session.ID, func(item *chat.Session) {
		item.DriverKind = ""
		item.NativeSessionID = ""
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, ChatSessionResponse{Object: "chat_session", Data: renderChatSession(updated, h.agentChatSnapshotConfig())})
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
	if renderAgentChatRuntimeKind(session) != "external_agent" {
		WriteError(w, http.StatusConflict, errCodeRuntimeMismatch, "agent chat config options are only available for external-agent sessions")
		return
	}
	var req SetAgentChatConfigOptionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	setReq, err := agentChatConfigOptionSetRequest(sessionID, configID, req.Value)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	if h.agentChatRunner == nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "agent chat runner is not configured")
		return
	}
	setCtx, cancel := context.WithTimeout(r.Context(), agentChatConfigOptionTimeout)
	result, err := h.agentChatRunner.SetSessionConfigOption(setCtx, setReq)
	cancel()
	if err != nil {
		writeAgentChatConfigOptionError(w, session, err)
		return
	}
	updated, err := h.agentChat.UpdateSession(r.Context(), session.ID, func(item *chat.Session) {
		item.ConfigOptions = result.ConfigOptions
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	h.agentChatLive.publishSession(updated)
	WriteJSON(w, http.StatusOK, ChatSessionResponse{Object: "chat_session", Data: renderChatSession(updated, h.agentChatSnapshotConfig())})
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
	if renderAgentChatRuntimeKind(session) == "external_agent" {
		WriteError(w, http.StatusConflict, errCodeRuntimeMismatch, "Hecate Chat settings are not available for external-agent sessions")
		return
	}
	var req SetAgentChatSettingsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.RTKEnabled == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "no settings provided")
		return
	}

	rtkEnabled := *req.RTKEnabled
	// Update the task row first, then the session row. The two writes
	// are NOT atomic — Hecate's controlplane has no cross-table
	// transactions today and this handler matches that pattern. The
	// task-first order is deliberate: task.RTKEnabled drives the
	// executor's sandbox-arg construction for existing continuations.
	// The reverse order — session first, task fails — would leave the
	// UI reporting RTK on while the backing task still runs without it.
	if session.TaskID != "" && h.taskStore != nil {
		task, found, err := h.taskStore.GetTask(r.Context(), session.TaskID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
			return
		}
		if found {
			task.RTKEnabled = rtkEnabled
			if _, err := h.taskStore.UpdateTask(r.Context(), task); err != nil {
				WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
				return
			}
		}
	}

	updated, err := h.agentChat.UpdateSession(r.Context(), session.ID, func(item *chat.Session) {
		item.RTKEnabled = rtkEnabled
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	h.agentChatLive.publishSession(updated)
	WriteJSON(w, http.StatusOK, ChatSessionResponse{Object: "chat_session", Data: renderChatSession(updated, h.agentChatSnapshotConfig())})
}

func writeAgentChatPrepareError(w http.ResponseWriter, adapterName string, err error) {
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
		WriteErrorDetails(w, http.StatusBadGateway, errCodeAgentAdapterUnavailable, agentadapters.NormalizeError(agentChatAdapterName(session.AdapterID), err), ErrorDetails{
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

func agentChatConfigOptionSetRequest(sessionID, configID string, rawValue any) (agentadapters.SetSessionConfigOptionRequest, error) {
	if sessionID == "" {
		return agentadapters.SetSessionConfigOptionRequest{}, fmt.Errorf("agent chat session id is required")
	}
	if configID == "" {
		return agentadapters.SetSessionConfigOptionRequest{}, fmt.Errorf("config option id is required")
	}
	switch value := rawValue.(type) {
	case string:
		value = strings.TrimSpace(value)
		if value == "" {
			return agentadapters.SetSessionConfigOptionRequest{}, fmt.Errorf("value is required")
		}
		return agentadapters.SetSessionConfigOptionRequest{SessionID: sessionID, ConfigID: configID, Value: value}, nil
	case bool:
		return agentadapters.SetSessionConfigOptionRequest{SessionID: sessionID, ConfigID: configID, BoolValue: &value}, nil
	default:
		return agentadapters.SetSessionConfigOptionRequest{}, fmt.Errorf("value must be a string or boolean")
	}
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
	content := strings.TrimSpace(req.Content)
	if content == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "content is required")
		return
	}

	limits := h.agentChatSnapshotConfig()
	maxTurns := limits.MaxTurnsPerSession
	if maxTurns > 0 && session.TurnsUsed >= maxTurns {
		WriteErrorDetails(w, http.StatusUnprocessableEntity, errCodeSessionLimitExceeded, fmt.Sprintf("session has reached the %d-turn limit; start a new session to continue", maxTurns), ErrorDetails{
			Fields: map[string]any{
				"limit":      maxTurns,
				"turns_used": session.TurnsUsed,
			},
		})
		return
	}
	if limits.MaxSessionDuration > 0 && !session.CreatedAt.IsZero() && time.Since(session.CreatedAt) >= limits.MaxSessionDuration {
		WriteErrorDetails(w, http.StatusUnprocessableEntity, errCodeSessionDurationLimit, fmt.Sprintf("session has reached the %s wall-clock limit; start a new session to continue", limits.MaxSessionDuration), ErrorDetails{
			Fields: map[string]any{
				"limit_ms":   limits.MaxSessionDuration.Milliseconds(),
				"started_at": formatOptionalTime(session.CreatedAt),
				"turns_used": session.TurnsUsed,
			},
		})
		return
	}
	if limits.IdleTimeout > 0 && !session.UpdatedAt.IsZero() && time.Since(session.UpdatedAt) >= limits.IdleTimeout {
		WriteErrorDetails(w, http.StatusUnprocessableEntity, errCodeSessionIdleTimeout, fmt.Sprintf("session was idle for at least %s; start a new session to continue", limits.IdleTimeout), ErrorDetails{
			Fields: map[string]any{
				"limit_ms":   limits.IdleTimeout.Milliseconds(),
				"updated_at": formatOptionalTime(session.UpdatedAt),
				"turns_used": session.TurnsUsed,
			},
		})
		return
	}
	turnRuntimeKind := normalizeAgentChatTurnRuntimeKind(req.RuntimeKind, session)
	switch turnRuntimeKind {
	case "model":
		h.handleCreateModelChatMessage(w, r, session, req)
		return
	case "agent":
		if renderAgentChatRuntimeKind(session) == "external_agent" {
			writeAgentChatRuntimeMismatch(w, "external agent sessions cannot run Hecate Agent turns")
			return
		}
		h.handleCreateHecateChatMessage(w, r, session, req)
		return
	case "external_agent":
		if renderAgentChatRuntimeKind(session) != "external_agent" {
			writeAgentChatRuntimeMismatch(w, "Hecate Chat sessions cannot run external-agent turns")
			return
		}
	default:
		writeAgentChatRuntimeKindInvalid(w)
		return
	}

	adapter, ok := agentadapters.BuiltInByID(session.AdapterID)
	if !ok {
		writeAgentChatAdapterNotFound(w, session.AdapterID)
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
		ID:        newChatID("msg"),
		Role:      "user",
		Content:   content,
		CreatedAt: time.Now().UTC(),
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
	updated, err = h.agentChat.AppendMessage(r.Context(), session.ID, chat.Message{
		ID:          assistantID,
		RunID:       runID,
		RequestID:   RequestIDFromContext(r.Context()),
		TraceID:     trace.TraceID,
		SpanID:      trace.RootSpanID(),
		Role:        "assistant",
		Content:     "",
		AdapterID:   adapter.ID,
		AdapterName: adapter.Name,
		DriverKind:  agentadapters.DriverKindACP,
		Status:      "running",
		CostMode:    adapter.CostMode,
		Workspace:   session.Workspace,
		CreatedAt:   time.Now().UTC(),
		StartedAt:   startedAt,
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
		runner = agentadapters.NewSessionManager()
	}
	result, runErr := runner.Run(runCtx, agentadapters.RunRequest{
		SessionID:               session.ID,
		AdapterID:               adapter.ID,
		Workspace:               session.Workspace,
		PreviousNativeSessionID: session.NativeSessionID,
		Prompt:                  content,
		Timeout:                 agentChatTimeout,
		MaxOutputBytes:          agentChatMaxOutputBytes,
		OnOutput: func(display string) {
			updated, updateErr := h.agentChat.UpdateMessage(runCtx, session.ID, assistantID, func(message *chat.Message) {
				message.Content = display
				if strings.TrimSpace(display) != "" && !outputSeen {
					message.Activities = append(message.Activities, newChatActivity("output", "running", "ACP output", "Streaming normalized transcript"))
					trace.Record(telemetry.EventAgentChatOutputStarted, agentChatTraceAttrs(session, adapter, runID, assistantID, map[string]any{
						telemetry.AttrHecateRunStatus:        "running",
						telemetry.AttrHecateAgentOutputBytes: int64(len(display)),
					}))
					outputSeen = true
				}
			})
			if updateErr == nil {
				h.agentChatLive.publishSession(updated)
			}
		},
		OnActivity: func(activity agentadapters.Activity) {
			updated, updateErr := h.agentChat.UpdateMessage(runCtx, session.ID, assistantID, func(message *chat.Message) {
				message.Activities = mergeChatActivity(message.Activities, agentChatActivityFromAdapter(activity))
			})
			if updateErr == nil {
				h.agentChatLive.publishSession(updated)
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
	displayErr := ""
	if runErr != nil {
		displayErr = agentadapters.NormalizeError(adapter.Name, runErr)
	}
	if status != "cancelled" && runErr != nil {
		if output == "" {
			output = displayErr
		} else {
			output = output + "\n\n" + displayErr
		}
	}
	if status != "cancelled" && output == "" {
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
	if runErr != nil && status != "cancelled" {
		errorText = displayErr
	}
	if result.DiffStat != "" {
		trace.Record(telemetry.EventAgentChatFilesChanged, agentChatTraceAttrs(session, adapter, runID, assistantID, map[string]any{
			telemetry.AttrHecateRunStatus:         status,
			telemetry.AttrHecateAgentDiffCaptured: true,
		}))
	}
	durationMS := completedAt.Sub(startedAt).Milliseconds()
	resultLabel := telemetry.ResultSuccess
	if runErr != nil || status == "cancelled" {
		resultLabel = telemetry.ResultError
	}
	terminalAttrs := agentChatTraceAttrs(session, adapter, runID, assistantID, map[string]any{
		telemetry.AttrHecateRunStatus:            status,
		telemetry.AttrHecateRunDurationMS:        durationMS,
		telemetry.AttrHecateAgentOutputBytes:     int64(len(output)),
		telemetry.AttrHecateAgentRawOutputBytes:  int64(len(result.RawOutput)),
		telemetry.AttrHecateAgentDiffCaptured:    result.Diff != "",
		telemetry.AttrHecateAgentDriverKind:      result.DriverKind,
		telemetry.AttrHecateAgentNativeSessionID: result.NativeSessionID,
		"process.exit.code":                      result.ExitCode,
	})
	if runErr != nil {
		terminalAttrs[telemetry.AttrHecateResult] = telemetry.ResultError
		terminalAttrs[telemetry.AttrHecateErrorKind] = telemetry.ErrorKindOther
		terminalAttrs[telemetry.AttrErrorType] = "agent_adapter_failed"
		terminalAttrs[telemetry.AttrErrorMessage] = displayErr
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
		Result:     resultLabel,
		DurationMS: durationMS,
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
		message.Activities = append(message.Activities, newChatActivity(status, status, finalChatActivityTitle(status), errorText))
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if result.DriverKind != "" || result.NativeSessionID != "" || result.ConfigOptions != nil {
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
	h.agentChatLive.publishSession(updated)
	WriteJSON(w, http.StatusOK, ChatSessionResponse{Object: "chat_session", Data: renderChatSession(updated, h.agentChatSnapshotConfig())})
}

func normalizeAgentChatTurnRuntimeKind(runtimeKind string, session chat.Session) string {
	runtimeKind = strings.TrimSpace(runtimeKind)
	if runtimeKind != "" {
		return runtimeKind
	}
	return renderAgentChatRuntimeKind(session)
}

func (h *Handler) handleCreateModelChatMessage(w http.ResponseWriter, r *http.Request, session chat.Session, req CreateChatMessageRequest) {
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
		ID:           newChatID("msg"),
		RuntimeKind:  "model",
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

	updated, err = h.agentChat.AppendMessage(r.Context(), session.ID, chat.Message{
		ID:           assistantID,
		RuntimeKind:  "model",
		SegmentID:    segmentID,
		RunID:        runID,
		RequestID:    RequestIDFromContext(r.Context()),
		Provider:     provider,
		Model:        model,
		Capabilities: caps,
		Role:         "assistant",
		Content:      "",
		Status:       "running",
		CostMode:     "hecate",
		Workspace:    session.Workspace,
		CreatedAt:    startedAt,
		StartedAt:    startedAt,
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
		Messages:  agentChatModelHistory(session, strings.TrimSpace(req.SystemPrompt), content),
		Scope:     requestscope.Build(provider),
	}
	result, runErr := h.service.HandleChat(runCtx, chatReq)
	completedAt := time.Now().UTC()
	status := "completed"
	output := ""
	errorText := ""
	if runErr != nil {
		status = "failed"
		errorText = runErr.Error()
		output = errorText
	}
	if errors.Is(runCtx.Err(), context.Canceled) {
		status = "cancelled"
		errorText = "cancelled"
		output = "model request cancelled"
	}
	if result != nil && result.Response != nil {
		if len(result.Response.Choices) > 0 {
			output = strings.TrimSpace(result.Response.Choices[0].Message.Content)
		}
		if output == "" {
			output = "(model completed without output)"
		}
	}
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
		message.Activities = append(message.Activities, newChatActivity(status, status, finalChatActivityTitle(status), errorText))
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if inc, incErr := h.agentChat.UpdateSession(r.Context(), session.ID, func(item *chat.Session) {
		item.RuntimeKind = "model"
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

func agentChatModelHistory(session chat.Session, systemPrompt, content string) []types.Message {
	messages := make([]types.Message, 0, len(session.Messages))
	if systemPrompt = strings.TrimSpace(systemPrompt); systemPrompt != "" {
		messages = append(messages, types.Message{Role: "system", Content: systemPrompt})
	}
	for _, message := range session.Messages {
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

func modelSegmentID(session chat.Session, provider, model string) string {
	for i := len(session.Messages) - 1; i >= 0; i-- {
		message := session.Messages[i]
		if message.RuntimeKind != "model" {
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
