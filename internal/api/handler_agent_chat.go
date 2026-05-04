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
	"github.com/hecate/agent-runtime/internal/profiler"
	"github.com/hecate/agent-runtime/internal/telemetry"
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
	workspaceBranch := workspaceGitBranch(workspace)
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = adapter.Name + " chat"
	}
	session, err := h.agentChat.Create(r.Context(), agentchat.Session{
		ID:              newAgentChatID("agent_chat"),
		Title:           title,
		AdapterID:       adapter.ID,
		DriverKind:      agentadapters.DriverKindACP,
		Workspace:       workspace,
		WorkspaceBranch: workspaceBranch,
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
	sessionID := r.PathValue("id")
	cancelCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	_ = h.agentChatLive.cancelRunAndWait(cancelCtx, sessionID)
	cancel()
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
	if !h.agentChatLive.cancelRun(session.ID) {
		WriteError(w, http.StatusConflict, errCodeInvalidRequest, "agent chat session is not running")
		return
	}
	WriteJSON(w, http.StatusAccepted, AgentChatSessionResponse{Object: "agent_chat_session", Data: renderAgentChatSession(session)})
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
	_ = h.agentChatLive.cancelRunAndWait(cancelCtx, session.ID)
	cancel()
	if h.agentChatRunner != nil {
		_ = h.agentChatRunner.CloseSession(r.Context(), session.ID)
	}
	updated, found, err := h.agentChat.Get(r.Context(), session.ID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "gateway_error", err.Error())
		return
	}
	if !found {
		WriteError(w, http.StatusNotFound, "not_found", "agent chat session not found")
		return
	}
	WriteJSON(w, http.StatusOK, AgentChatSessionResponse{Object: "agent_chat_session", Data: renderAgentChatSession(updated)})
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
	h.agentChatLive.publish(updated)
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
	h.agentChatLive.publish(updated)

	outputSeen := false
	runner := h.agentChatRunner
	if runner == nil {
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
			if display == "" {
				return
			}
			updated, updateErr := h.agentChat.UpdateMessage(runCtx, session.ID, assistantID, func(message *agentchat.Message) {
				message.Content = display
				if !outputSeen {
					message.Activities = append(message.Activities, newAgentChatActivity("output", "running", "ACP output", "Streaming normalized transcript"))
					trace.Record(telemetry.EventAgentChatOutputStarted, agentChatTraceAttrs(session, adapter, runID, assistantID, map[string]any{
						telemetry.AttrHecateRunStatus:        "running",
						telemetry.AttrHecateAgentOutputBytes: int64(len(display)),
					}))
					outputSeen = true
				}
			})
			if updateErr == nil {
				h.agentChatLive.publish(updated)
			}
		},
		OnActivity: func(activity agentadapters.Activity) {
			updated, updateErr := h.agentChat.UpdateMessage(runCtx, session.ID, assistantID, func(message *agentchat.Message) {
				message.Activities = mergeAgentChatActivity(message.Activities, agentChatActivityFromAdapter(activity))
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
	terminalAttrs := agentChatTraceAttrs(session, adapter, runID, assistantID, map[string]any{
		telemetry.AttrHecateRunStatus:            status,
		telemetry.AttrHecateRunDurationMS:        completedAt.Sub(startedAt).Milliseconds(),
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
	h.agentChatLive.publish(updated)
	WriteJSON(w, http.StatusOK, AgentChatSessionResponse{Object: "agent_chat_session", Data: renderAgentChatSession(updated)})
}

func renderAgentChatSessionSummary(session agentchat.Session) AgentChatSessionSummaryItem {
	return AgentChatSessionSummaryItem{
		ID:              session.ID,
		Title:           session.Title,
		AdapterID:       session.AdapterID,
		DriverKind:      session.DriverKind,
		NativeSessionID: session.NativeSessionID,
		Workspace:       session.Workspace,
		WorkspaceBranch: session.WorkspaceBranch,
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
			ID:              message.ID,
			RunID:           message.RunID,
			RequestID:       message.RequestID,
			TraceID:         message.TraceID,
			SpanID:          message.SpanID,
			Role:            message.Role,
			Content:         message.Content,
			RawOutput:       message.RawOutput,
			AdapterID:       message.AdapterID,
			AdapterName:     message.AdapterName,
			DriverKind:      message.DriverKind,
			NativeSessionID: message.NativeSessionID,
			Status:          message.Status,
			ExitCode:        message.ExitCode,
			CostMode:        message.CostMode,
			Workspace:       message.Workspace,
			DiffStat:        message.DiffStat,
			Diff:            message.Diff,
			CreatedAt:       formatOptionalTime(message.CreatedAt),
			StartedAt:       formatOptionalTime(message.StartedAt),
			CompletedAt:     formatOptionalTime(message.CompletedAt),
			DurationMS:      durationMillis(message.StartedAt, message.CompletedAt),
			Error:           message.Error,
			Activities:      renderAgentChatActivities(message.Activities),
			Usage:           renderAgentChatUsage(message.Usage),
		})
	}
	return AgentChatSessionItem{
		ID:              session.ID,
		Title:           session.Title,
		AdapterID:       session.AdapterID,
		DriverKind:      session.DriverKind,
		NativeSessionID: session.NativeSessionID,
		Workspace:       session.Workspace,
		WorkspaceBranch: session.WorkspaceBranch,
		Status:          session.Status,
		CreatedAt:       formatOptionalTime(session.CreatedAt),
		UpdatedAt:       formatOptionalTime(session.UpdatedAt),
		Messages:        messages,
	}
}

func agentChatUsageFromResult(usage agentadapters.Usage) agentchat.Usage {
	return agentchat.Usage{
		ContextSize:          usage.ContextSize,
		ContextUsed:          usage.ContextUsed,
		ReportedCostAmount:   usage.ReportedCostAmount,
		ReportedCostCurrency: usage.ReportedCostCurrency,
	}
}

func renderAgentChatUsage(usage agentchat.Usage) *AgentChatUsageItem {
	if usage.Empty() {
		return nil
	}
	return &AgentChatUsageItem{
		ContextSize:          usage.ContextSize,
		ContextUsed:          usage.ContextUsed,
		ReportedCostAmount:   usage.ReportedCostAmount,
		ReportedCostCurrency: usage.ReportedCostCurrency,
	}
}

func (h *Handler) startAgentChatTrace(w http.ResponseWriter, r *http.Request) (*profiler.Trace, context.Context) {
	requestID := RequestIDFromContext(r.Context())
	if requestID == "" {
		requestID = newRequestID()
	}
	trace := h.tracer.Start(requestID)
	ctx := telemetry.WithTraceIDs(r.Context(), trace.TraceID, trace.RootSpanID())
	w.Header().Set("X-Trace-Id", trace.TraceID)
	w.Header().Set("X-Span-Id", trace.RootSpanID())
	return trace, ctx
}

func agentChatTraceAttrs(session agentchat.Session, adapter agentadapters.Adapter, runID, messageID string, attrs map[string]any) map[string]any {
	out := map[string]any{
		telemetry.AttrHecateAgentChatSessionID:  session.ID,
		telemetry.AttrHecateAgentChatMessageID:  messageID,
		telemetry.AttrHecateRunID:               runID,
		telemetry.AttrHecateExecutionKind:       "agent_chat",
		telemetry.AttrHecateAgentAdapterID:      adapter.ID,
		telemetry.AttrHecateAgentAdapterName:    adapter.Name,
		telemetry.AttrHecateAgentAdapterCommand: adapter.Command,
		telemetry.AttrHecateAgentDriverKind:     adapter.Kind,
		telemetry.AttrHecateWorkspacePath:       session.Workspace,
		telemetry.AttrHecateResult:              telemetry.ResultSuccess,
	}
	if session.NativeSessionID != "" {
		out[telemetry.AttrHecateAgentNativeSessionID] = session.NativeSessionID
	}
	for key, value := range attrs {
		out[key] = value
	}
	return out
}

func agentChatTerminalEvent(status string) string {
	switch status {
	case "cancelled":
		return telemetry.EventAgentChatRunCancelled
	case "failed":
		return telemetry.EventAgentChatRunFailed
	default:
		return telemetry.EventAgentChatRunFinished
	}
}

func renderAgentChatActivities(items []agentchat.Activity) []AgentChatActivityItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]AgentChatActivityItem, 0, len(items))
	for _, item := range items {
		out = append(out, AgentChatActivityItem{
			ID:        item.ID,
			Type:      item.Type,
			Status:    item.Status,
			Kind:      item.Kind,
			Title:     item.Title,
			Detail:    item.Detail,
			CreatedAt: formatOptionalTime(item.CreatedAt),
		})
	}
	return out
}

func newAgentChatActivity(kind, status, title, detail string) agentchat.Activity {
	return agentchat.Activity{
		Type:      kind,
		Status:    status,
		Title:     title,
		Detail:    strings.TrimSpace(detail),
		CreatedAt: time.Now().UTC(),
	}
}

func agentChatActivityFromAdapter(activity agentadapters.Activity) agentchat.Activity {
	return agentchat.Activity{
		ID:        strings.TrimSpace(activity.ID),
		Type:      strings.TrimSpace(activity.Type),
		Status:    strings.TrimSpace(activity.Status),
		Kind:      strings.TrimSpace(activity.Kind),
		Title:     strings.TrimSpace(activity.Title),
		Detail:    strings.TrimSpace(activity.Detail),
		CreatedAt: time.Now().UTC(),
	}
}

func mergeAgentChatActivity(items []agentchat.Activity, next agentchat.Activity) []agentchat.Activity {
	if next.Type == "" || (next.ID == "" && next.Title == "") {
		return items
	}
	if next.ID != "" {
		for i := range items {
			if items[i].ID == next.ID {
				if next.Status != "" {
					items[i].Status = next.Status
				}
				if next.Kind != "" {
					items[i].Kind = next.Kind
				}
				if next.Title != "" {
					items[i].Title = next.Title
				}
				if next.Detail != "" {
					items[i].Detail = next.Detail
				}
				items[i].CreatedAt = next.CreatedAt
				return items
			}
		}
	}
	if next.Title == "" {
		return items
	}
	return append(items, next)
}

func finalAgentChatActivityTitle(status string) string {
	switch status {
	case "completed":
		return "Final answer"
	case "failed":
		return "Failed"
	case "cancelled":
		return "Cancelled"
	default:
		return status
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
