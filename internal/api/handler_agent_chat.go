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
	"github.com/hecate/agent-runtime/internal/requestscope"
	"github.com/hecate/agent-runtime/internal/telemetry"
	"github.com/hecate/agent-runtime/pkg/types"
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
	runtimeKind := normalizeAgentChatRuntimeKind(req.RuntimeKind, req.AdapterID)
	workspace := strings.TrimSpace(req.Workspace)
	if runtimeKind != "model" {
		if workspace == "" {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "workspace is required")
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

	session := agentchat.Session{
		ID:              newAgentChatID("agent_chat"),
		Title:           title,
		RuntimeKind:     runtimeKind,
		Workspace:       workspace,
		WorkspaceBranch: workspaceBranch,
	}
	var err error
	switch runtimeKind {
	case "model":
		provider := strings.TrimSpace(req.Provider)
		model := strings.TrimSpace(req.Model)
		if model == "" {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "model is required for model chat")
			return
		}
		caps, err := h.resolveModelCapabilities(r.Context(), provider, model)
		if err != nil {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
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
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, fmt.Sprintf("agent adapter %q not found", req.AdapterID))
			return
		}
		if session.Title == "" {
			session.Title = adapter.Name + " chat"
		}
		session.AdapterID = adapter.ID
		session.DriverKind = agentadapters.DriverKindACP
	case "agent":
		provider := strings.TrimSpace(req.Provider)
		model := strings.TrimSpace(req.Model)
		if model == "" {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "model is required for Hecate Agent chat")
			return
		}
		caps, err := h.resolveModelCapabilities(r.Context(), provider, model)
		if err != nil {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
			return
		}
		if session.Title == "" {
			session.Title = "Hecate Agent chat"
		}
		session.Provider = provider
		session.Model = model
		session.Capabilities = caps
	default:
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "runtime_kind must be model, agent, or external_agent")
		return
	}
	session, err = h.agentChat.Create(r.Context(), session)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "gateway_error", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, AgentChatSessionResponse{Object: "agent_chat_session", Data: renderAgentChatSession(session, h.agentChatSnapshotConfig())})
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
	WriteJSON(w, http.StatusOK, AgentChatSessionResponse{Object: "agent_chat_session", Data: renderAgentChatSession(session, h.agentChatSnapshotConfig())})
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
		Data:   renderAgentChatSession(session, h.agentChatSnapshotConfig()),
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

func (h *Handler) HandleDeleteAgentChatSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	cancelCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	settled := h.agentChatLive.cancelRunAndWait(cancelCtx, sessionID)
	cancel()
	if !settled {
		WriteError(w, http.StatusConflict, errCodeInvalidRequest, "agent chat session is still stopping")
		return
	}
	if h.agentChatRunner != nil {
		_ = h.agentChatRunner.CloseSession(r.Context(), sessionID)
	}
	if err := h.agentChat.Delete(r.Context(), sessionID); err != nil {
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
	if renderAgentChatRuntimeKind(session) == "agent" && h.taskStore != nil && h.taskRunner != nil && session.TaskID != "" && session.LatestRunID != "" {
		task, found, err := h.taskStore.GetTask(r.Context(), session.TaskID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "gateway_error", err.Error())
			return
		}
		if found {
			run, err := h.taskRunner.CancelRun(r.Context(), task, session.LatestRunID, "operator")
			if err == nil {
				updated, updateErr := h.agentChat.UpdateSession(r.Context(), session.ID, func(item *agentchat.Session) {
					item.Status = run.Status
				})
				if updateErr == nil {
					h.agentChatLive.publishSession(updated)
					WriteJSON(w, http.StatusAccepted, AgentChatSessionResponse{Object: "agent_chat_session", Data: renderAgentChatSession(updated, h.agentChatSnapshotConfig())})
					return
				}
			}
			if err != nil && !strings.Contains(err.Error(), "already terminal") {
				WriteError(w, http.StatusConflict, errCodeInvalidRequest, err.Error())
				return
			}
		}
	}
	if !h.agentChatLive.cancelRun(session.ID) {
		WriteError(w, http.StatusConflict, errCodeInvalidRequest, "agent chat session is not running")
		return
	}
	WriteJSON(w, http.StatusAccepted, AgentChatSessionResponse{Object: "agent_chat_session", Data: renderAgentChatSession(session, h.agentChatSnapshotConfig())})
}

func (h *Handler) HandleCloseAgentChatSession(w http.ResponseWriter, r *http.Request) {
	session, ok, err := h.agentChat.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "gateway_error", err.Error())
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, "not_found", "agent chat session not found")
		return
	}
	cancelCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	settled := h.agentChatLive.cancelRunAndWait(cancelCtx, session.ID)
	cancel()
	if !settled {
		WriteError(w, http.StatusConflict, errCodeInvalidRequest, "agent chat session is still stopping")
		return
	}
	if h.agentChatRunner != nil {
		_ = h.agentChatRunner.CloseSession(r.Context(), session.ID)
	}
	updated, err := h.agentChat.UpdateSession(r.Context(), session.ID, func(item *agentchat.Session) {
		item.DriverKind = ""
		item.NativeSessionID = ""
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "gateway_error", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, AgentChatSessionResponse{Object: "agent_chat_session", Data: renderAgentChatSession(updated, h.agentChatSnapshotConfig())})
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

	limits := h.agentChatSnapshotConfig()
	maxTurns := limits.MaxTurnsPerSession
	if maxTurns > 0 && session.TurnsUsed >= maxTurns {
		WriteJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": map[string]any{
				"type":       errCodeSessionLimitExceeded,
				"message":    fmt.Sprintf("session has reached the %d-turn limit; start a new session to continue", maxTurns),
				"limit":      maxTurns,
				"turns_used": session.TurnsUsed,
			},
		})
		return
	}
	if limits.MaxSessionDuration > 0 && !session.CreatedAt.IsZero() && time.Since(session.CreatedAt) >= limits.MaxSessionDuration {
		WriteJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": map[string]any{
				"type":       errCodeSessionDurationLimit,
				"message":    fmt.Sprintf("session has reached the %s wall-clock limit; start a new session to continue", limits.MaxSessionDuration),
				"limit_ms":   limits.MaxSessionDuration.Milliseconds(),
				"started_at": formatOptionalTime(session.CreatedAt),
				"turns_used": session.TurnsUsed,
			},
		})
		return
	}
	if limits.IdleTimeout > 0 && !session.UpdatedAt.IsZero() && time.Since(session.UpdatedAt) >= limits.IdleTimeout {
		WriteJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": map[string]any{
				"type":       errCodeSessionIdleTimeout,
				"message":    fmt.Sprintf("session was idle for at least %s; start a new session to continue", limits.IdleTimeout),
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
		h.handleCreateModelAgentChatMessage(w, r, session, req)
		return
	case "agent":
		if renderAgentChatRuntimeKind(session) == "external_agent" {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "external agent sessions cannot run Hecate Agent turns")
			return
		}
		h.handleCreateHecateAgentChatMessage(w, r, session, req)
		return
	case "external_agent":
		if renderAgentChatRuntimeKind(session) != "external_agent" {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "Hecate Chat sessions cannot run external-agent turns")
			return
		}
	default:
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "runtime_kind must be model, agent, or external_agent")
		return
	}

	adapter, ok := agentadapters.BuiltInByID(session.AdapterID)
	if !ok {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, fmt.Sprintf("agent adapter %q not found", session.AdapterID))
		return
	}
	assistantID := newAgentChatID("msg")
	runID := newAgentChatID("agent_run")
	trace, traceCtx := h.startAgentChatTrace(w, r)
	defer trace.Finalize()

	runCtx, cancel := context.WithTimeout(traceCtx, agentChatTimeout)
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
	h.agentChatLive.publishSession(updated)
	startedAt := time.Now().UTC()
	trace.Record(telemetry.EventAgentChatRunStarted, agentChatTraceAttrs(session, adapter, runID, assistantID, map[string]any{
		telemetry.AttrHecateRunStatus: "running",
	}))
	updated, err = h.agentChat.AppendMessage(r.Context(), session.ID, agentchat.Message{
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
		Activities: []agentchat.Activity{
			newAgentChatActivity("running", "running", "Running", "Waiting for ACP output"),
		},
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "gateway_error", err.Error())
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
			updated, updateErr := h.agentChat.UpdateMessage(runCtx, session.ID, assistantID, func(message *agentchat.Message) {
				message.Content = display
				if strings.TrimSpace(display) != "" && !outputSeen {
					message.Activities = append(message.Activities, newAgentChatActivity("output", "running", "ACP output", "Streaming normalized transcript"))
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
			updated, updateErr := h.agentChat.UpdateMessage(runCtx, session.ID, assistantID, func(message *agentchat.Message) {
				message.Activities = mergeAgentChatActivity(message.Activities, agentChatActivityFromAdapter(activity))
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
	if status == "cancelled" {
		if output == "" {
			output = "agent run cancelled"
		}
	} else if output == "" && runErr != nil {
		output = displayErr
	} else if runErr != nil {
		output = output + "\n\n" + displayErr
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
		errorText = displayErr
	}
	if status == "cancelled" {
		errorText = "cancelled"
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

	updated, err = h.agentChat.UpdateMessage(r.Context(), session.ID, assistantID, func(message *agentchat.Message) {
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
			message.Activities = append([]agentchat.Activity{newAgentChatActivity("resumed", "completed", "Resumed external session", adapter.Name+" restored "+result.NativeSessionID)}, message.Activities...)
		} else if result.SessionStarted {
			activities := []agentchat.Activity{newAgentChatActivity("started", "completed", "Starting external agent", adapter.Name+" in "+session.Workspace)}
			if result.SessionRecovery != "" {
				activities = append(activities, newAgentChatActivity("recovered", "completed", "Started fresh external session", result.SessionRecovery))
			}
			message.Activities = append(activities, message.Activities...)
		}
		if result.DiffStat != "" {
			message.Activities = append(message.Activities, newAgentChatActivity("files_changed", "completed", "Files changed", result.DiffStat))
		}
		message.Activities = append(message.Activities, newAgentChatActivity(status, status, finalAgentChatActivityTitle(status), errorText))
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "gateway_error", err.Error())
		return
	}
	if result.DriverKind != "" || result.NativeSessionID != "" {
		updated, err = h.agentChat.UpdateSession(r.Context(), session.ID, func(item *agentchat.Session) {
			if result.DriverKind != "" {
				item.DriverKind = result.DriverKind
			}
			if result.NativeSessionID != "" {
				item.NativeSessionID = result.NativeSessionID
			}
		})
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "gateway_error", err.Error())
			return
		}
	}
	// Increment after every completed round-trip, even when no ceiling is set.
	// Best-effort: the turn result itself was already committed above.
	if inc, incErr := h.agentChat.UpdateSession(r.Context(), session.ID, func(item *agentchat.Session) {
		item.TurnsUsed++
	}); incErr == nil {
		updated = inc
	} else {
		h.logger.WarnContext(r.Context(), "agent_chat.turn_counter_increment_failed", "session_id", session.ID, "error", incErr)
	}
	h.agentChatLive.publishSession(updated)
	WriteJSON(w, http.StatusOK, AgentChatSessionResponse{Object: "agent_chat_session", Data: renderAgentChatSession(updated, h.agentChatSnapshotConfig())})
}

func normalizeAgentChatTurnRuntimeKind(runtimeKind string, session agentchat.Session) string {
	runtimeKind = strings.TrimSpace(runtimeKind)
	if runtimeKind != "" {
		return runtimeKind
	}
	return renderAgentChatRuntimeKind(session)
}

func (h *Handler) handleCreateModelAgentChatMessage(w http.ResponseWriter, r *http.Request, session agentchat.Session, req CreateAgentChatMessageRequest) {
	provider := strings.TrimSpace(req.Provider)
	if provider == "" {
		provider = session.Provider
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = session.Model
	}
	if model == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "model is required for model chat")
		return
	}
	caps, err := h.resolveModelCapabilities(r.Context(), provider, model)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	content := strings.TrimSpace(req.Content)
	assistantID := newAgentChatID("msg")
	runID := newAgentChatID("model_run")
	startedAt := time.Now().UTC()
	runCtx, cancel := context.WithTimeout(r.Context(), agentChatTimeout)
	if !h.agentChatLive.registerRun(session.ID, cancel) {
		cancel()
		WriteError(w, http.StatusConflict, errCodeAgentSessionBusy, "chat session is already running")
		return
	}
	defer h.agentChatLive.clearRun(session.ID)
	defer cancel()

	segmentID := modelSegmentID(session, provider, model)
	updated, err := h.agentChat.AppendMessage(r.Context(), session.ID, agentchat.Message{
		ID:           newAgentChatID("msg"),
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

	updated, err = h.agentChat.AppendMessage(r.Context(), session.ID, agentchat.Message{
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
		Activities: []agentchat.Activity{
			newAgentChatActivity("model_request", "running", "Model request", "Waiting for provider response"),
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
	updated, err = h.agentChat.UpdateMessage(r.Context(), session.ID, assistantID, func(message *agentchat.Message) {
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
			message.Usage = agentchat.Usage{
				ContextUsed: result.Metadata.TotalTokens,
			}
		}
		message.Activities = append(message.Activities, newAgentChatActivity(status, status, finalAgentChatActivityTitle(status), errorText))
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if inc, incErr := h.agentChat.UpdateSession(r.Context(), session.ID, func(item *agentchat.Session) {
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
		WriteJSON(w, http.StatusOK, AgentChatSessionResponse{Object: "agent_chat_session", Data: renderAgentChatSession(updated, h.agentChatSnapshotConfig())})
		return
	}
	WriteJSON(w, http.StatusOK, AgentChatSessionResponse{Object: "agent_chat_session", Data: renderAgentChatSession(updated, h.agentChatSnapshotConfig())})
}

func agentChatModelHistory(session agentchat.Session, systemPrompt, content string) []types.Message {
	messages := make([]types.Message, 0, len(session.Messages)+1)
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

func modelSegmentID(session agentchat.Session, provider, model string) string {
	for i := len(session.Messages) - 1; i >= 0; i-- {
		message := session.Messages[i]
		if message.RuntimeKind != "model" {
			break
		}
		if message.Provider == provider && message.Model == model && message.SegmentID != "" {
			return message.SegmentID
		}
	}
	return newAgentChatID("segment")
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
