package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/modelcaps"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

const hecateAgentPollInterval = 250 * time.Millisecond

// handleCreateHecateChatMessage is the unified entry point for every
// Hecate-side chat-message submission. It branches on toolsEnabled:
//   - toolsEnabled=false → delegate to handleDirectModelTurn (a
//     direct LLM call with no agent_loop task).
//   - toolsEnabled=true → existing agent_loop task creation +
//     polling path that the Hecate Chat tools-on UX runs on.
//
// External-agent sessions never reach this function — the dispatcher
// pins them to the external_agent path before getting here.
func (h *Handler) handleCreateHecateChatMessage(w http.ResponseWriter, r *http.Request, session chat.Session, req CreateChatMessageRequest, toolsEnabled bool) {
	if !toolsEnabled {
		h.handleDirectModelTurn(w, r, session, req)
		return
	}
	content := strings.TrimSpace(req.Content)
	if workspace := strings.TrimSpace(req.Workspace); workspace != "" {
		resolved, err := agentadapters.ValidateWorkspace(workspace)
		if err != nil {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
			return
		}
		session.Workspace = resolved
		session.WorkspaceBranch = workspaceGitBranch(resolved)
	}
	if strings.TrimSpace(session.Workspace) == "" {
		writeAgentChatWorkspaceRequired(w, chat.ExecutionModeHecateTask)
		return
	}
	if provider := strings.TrimSpace(req.Provider); provider != "" {
		session.Provider = provider
	}
	if model := strings.TrimSpace(req.Model); model != "" {
		session.Model = model
		session.Capabilities = types.ModelCapabilities{}
	}
	caps := session.Capabilities
	if !modelcaps.ToolCapable(caps) {
		resolved, err := h.resolveModelCapabilities(r.Context(), session.Provider, session.Model)
		if err != nil {
			writeAgentChatModelResolutionError(w, err)
			return
		}
		caps = resolved
	}
	if !modelcaps.ToolCapable(caps) {
		WriteErrorDetails(w, http.StatusUnprocessableEntity, errCodeModelCapability, "Tools are unavailable for this model. Send as direct model chat or choose a tool-capable model.", ErrorDetails{
			Fields: map[string]any{
				"provider":     session.Provider,
				"model":        session.Model,
				"capabilities": caps,
			},
		})
		return
	}

	if busy, runStatus := h.hecateAgentSessionBusy(r.Context(), session); busy {
		writeHecateAgentBusy(w, session, runStatus)
		return
	}

	assistantID := newChatID("msg")
	startedAt := time.Now().UTC()
	trace, traceCtx := h.startAgentChatTrace(w, r)
	defer trace.Finalize()
	trace.Record(telemetry.EventAgentChatRunStarted, hecateAgentChatTraceAttrs(session, "", "", assistantID, map[string]any{
		telemetry.AttrHecateRunStatus: "running",
	}))

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

	userID := newChatID("msg")
	forceNewTask := shouldStartNewHecateAgentSegment(session, session.Provider, session.Model)
	segmentID := hecateAgentSegmentID(session)
	messageSnapshotSession := session
	if forceNewTask {
		// The live placeholder for a brand-new task segment must not borrow the
		// session's previous task pointer. It will be rewritten to task:<id>
		// immediately after the new backing task exists.
		segmentID = newChatID("segment")
		messageSnapshotSession.TaskID = ""
		messageSnapshotSession.LatestRunID = ""
	}
	messageSnapshot := hecateAgentMessageSnapshot(messageSnapshotSession, caps, segmentID)
	updated, err := h.agentChat.AppendMessage(r.Context(), session.ID, chat.Message{
		ID:            userID,
		ExecutionMode: messageSnapshot.ExecutionMode,
		// Hecate-task handler: the operator submitted with tools on
		// (otherwise the dispatcher routes to the model-chat handler
		// path). Record the intent so a future read against this row
		// doesn't have to interpret execution_mode to recover it.
		ToolsEnabled: true,
		SegmentID:    messageSnapshot.SegmentID,
		TaskID:       messageSnapshot.TaskID,
		Provider:     messageSnapshot.Provider,
		Model:        messageSnapshot.Model,
		Capabilities: messageSnapshot.Capabilities,
		Role:         "user",
		Content:      content,
		CreatedAt:    startedAt,
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	h.agentChatLive.publishSession(updated)

	contextPacket := h.hecateTaskContextPacket(r.Context(), session, messageSnapshot.Provider, messageSnapshot.Model, strings.TrimSpace(req.SystemPrompt), forceNewTask)
	contextPacket.ID = newChatID("ctx")
	contextPacket = normalizeContextPacket(contextPacket, chat.ContextRefs{
		SessionID: session.ID,
		MessageID: assistantID,
		TaskID:    messageSnapshot.TaskID,
		ProjectID: session.ProjectID,
	})
	updated, err = h.agentChat.AppendMessage(r.Context(), session.ID, chat.Message{
		ID:            assistantID,
		ExecutionMode: messageSnapshot.ExecutionMode,
		ToolsEnabled:  true,
		SegmentID:     messageSnapshot.SegmentID,
		TaskID:        messageSnapshot.TaskID,
		RequestID:     RequestIDFromContext(r.Context()),
		TraceID:       trace.TraceID,
		SpanID:        trace.RootSpanID(),
		Provider:      messageSnapshot.Provider,
		Model:         messageSnapshot.Model,
		Capabilities:  messageSnapshot.Capabilities,
		Role:          "assistant",
		Content:       "",
		Status:        "running",
		CostMode:      "hecate",
		Workspace:     session.Workspace,
		Context:       contextPacket,
		CreatedAt:     startedAt,
		StartedAt:     startedAt,
		Activities: []chat.Activity{
			newChatActivity("started", "running", "Starting Hecate Chat tools", "Creating or continuing the backing task run"),
		},
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	h.agentChatLive.publishSession(updated)

	task, run, err := h.hecateAgentTaskOrchestrator().StartOrContinue(runCtx, hecateAgentTaskRunCommand{
		Session:       session,
		Prompt:        content,
		SystemPrompt:  strings.TrimSpace(req.SystemPrompt),
		ForceNewTask:  forceNewTask,
		ContextPacket: contextPacket,
	})
	if err != nil {
		completedAt := time.Now().UTC()
		trace.Record(telemetry.EventAgentChatRunFailed, hecateAgentChatTraceAttrs(session, "", "", assistantID, map[string]any{
			telemetry.AttrHecateRunStatus:     "failed",
			telemetry.AttrHecateRunDurationMS: completedAt.Sub(startedAt).Milliseconds(),
			telemetry.AttrHecateResult:        telemetry.ResultError,
			telemetry.AttrHecateErrorKind:     telemetry.ErrorKindOther,
			telemetry.AttrErrorType:           "agent_start_failed",
			telemetry.AttrErrorMessage:        err.Error(),
		}))
		h.agentChatMetrics.RecordRun(traceCtx, telemetry.AgentChatRunMetricsRecord{
			AdapterID:  "hecate",
			DriverKind: "hecate",
			Status:     "failed",
			Result:     telemetry.ResultError,
			DurationMS: completedAt.Sub(startedAt).Milliseconds(),
		})
		h.finishHecateAgentMessage(r.Context(), session.ID, assistantID, "failed", "", err.Error(), startedAt, completedAt, nil, chat.Timing{})
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	segmentID = "task:" + task.ID
	messageSnapshot = hecateAgentMessageSnapshot(chat.Session{
		ID:           session.ID,
		TaskID:       task.ID,
		Provider:     session.Provider,
		Model:        session.Model,
		Capabilities: caps,
	}, caps, segmentID)
	if userUpdated, userUpdateErr := h.agentChat.UpdateMessage(r.Context(), session.ID, userID, func(message *chat.Message) {
		message.ExecutionMode = messageSnapshot.ExecutionMode
		message.SegmentID = messageSnapshot.SegmentID
		message.TaskID = messageSnapshot.TaskID
		message.Provider = messageSnapshot.Provider
		message.Model = messageSnapshot.Model
		message.Capabilities = messageSnapshot.Capabilities
	}); userUpdateErr == nil {
		h.agentChatLive.publishSession(userUpdated)
	}
	updated, err = h.agentChat.UpdateMessage(r.Context(), session.ID, assistantID, func(message *chat.Message) {
		message.ExecutionMode = messageSnapshot.ExecutionMode
		message.SegmentID = messageSnapshot.SegmentID
		message.TaskID = messageSnapshot.TaskID
		message.RunID = run.ID
		message.RequestID = firstNonEmpty(run.RequestID, message.RequestID)
		message.TraceID = firstNonEmpty(run.TraceID, message.TraceID)
		message.SpanID = firstNonEmpty(run.RootSpanID, message.SpanID)
		message.Provider = messageSnapshot.Provider
		message.Model = messageSnapshot.Model
		message.Capabilities = messageSnapshot.Capabilities
		message.Context = normalizeContextPacket(message.Context, chat.ContextRefs{
			SessionID: session.ID,
			MessageID: assistantID,
			TaskID:    task.ID,
			RunID:     run.ID,
			ProjectID: session.ProjectID,
		})
		message.Activities = mergeChatActivity(message.Activities, newHecateAgentRunActivity(task.ID, run.ID, run.Status))
	})
	if err == nil {
		h.agentChatLive.publishSession(updated)
	}
	updated, err = h.agentChat.UpdateSession(r.Context(), session.ID, func(item *chat.Session) {
		item.TaskID = task.ID
		item.LatestRunID = run.ID
		item.Provider = session.Provider
		item.Model = session.Model
		item.Capabilities = caps
		item.Workspace = session.Workspace
		item.WorkspaceBranch = session.WorkspaceBranch
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	session = updated
	h.agentChatLive.publishSession(updated)

	finalRun, err := h.waitForHecateAgentRun(runCtx, task.ID, run.ID, session.ID, assistantID)
	if finalRun.ID == "" {
		finalRun = run
	}
	status := "completed"
	errorText := ""
	if err != nil {
		status = "failed"
		errorText = err.Error()
	}
	if errors.Is(runCtx.Err(), context.Canceled) {
		status = "cancelled"
		errorText = "cancelled"
	}
	if finalRun.Status != "" {
		switch finalRun.Status {
		case "completed", "failed", "cancelled":
			status = finalRun.Status
		case "awaiting_approval", "queued", "running":
			status = finalRun.Status
		}
	}
	if finalRun.LastError != "" && errorText == "" {
		errorText = finalRun.LastError
	}

	output := ""
	if status == "completed" {
		output = h.finalHecateAgentAnswer(r.Context(), task.ID, finalRun.ID)
		if commandOutput := h.finalHecateAgentCommandOutput(r.Context(), task.ID, finalRun.ID); commandOutput != "" {
			output = mergeHecateAgentAnswerWithCommandOutput(output, commandOutput)
		}
	}
	if output == "" {
		output = hecateAgentFallbackOutput(status, task.ID, finalRun.ID, errorText)
	}
	completedAt := time.Now().UTC()
	durationMS := completedAt.Sub(startedAt).Milliseconds()
	timing := h.hecateAgentTiming(r.Context(), finalRun, startedAt, completedAt)
	resultLabel := telemetry.ResultSuccess
	if status == "failed" || status == "cancelled" {
		resultLabel = telemetry.ResultError
	}
	terminalAttrs := hecateAgentChatTraceAttrs(session, task.ID, finalRun.ID, assistantID, map[string]any{
		telemetry.AttrHecateRunStatus:        status,
		telemetry.AttrHecateRunDurationMS:    durationMS,
		telemetry.AttrHecateAgentOutputBytes: int64(len(output)),
	})
	addHecateAgentTimingTraceAttrs(terminalAttrs, timing)
	if status == "failed" && strings.TrimSpace(errorText) != "" {
		terminalAttrs[telemetry.AttrHecateResult] = telemetry.ResultError
		terminalAttrs[telemetry.AttrHecateErrorKind] = telemetry.ErrorKindOther
		terminalAttrs[telemetry.AttrErrorType] = "agent_failed"
		terminalAttrs[telemetry.AttrErrorMessage] = errorText
	}
	if status == "cancelled" {
		terminalAttrs[telemetry.AttrHecateResult] = telemetry.ResultError
	}
	trace.Record(agentChatTerminalEvent(status), terminalAttrs)
	h.agentChatMetrics.RecordRun(traceCtx, telemetry.AgentChatRunMetricsRecord{
		AdapterID:  "hecate",
		DriverKind: "hecate",
		Status:     status,
		Result:     resultLabel,
		DurationMS: durationMS,
		Timing:     agentChatRunTimingMetrics(timing),
	})
	if status == "cancelled" {
		reason := h.agentChatLive.cancelReasonFor(session.ID)
		if reason == "" {
			reason = "request_cancelled"
		}
		h.agentChatMetrics.RecordChatCancelled(traceCtx, telemetry.AgentChatCancelledRecord{
			AdapterID: "hecate",
			Reason:    reason,
		})
	}
	updated, err = h.finishHecateAgentMessage(r.Context(), session.ID, assistantID, status, output, errorText, startedAt, completedAt, &finalRun, timing)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if inc, incErr := h.agentChat.UpdateSession(r.Context(), session.ID, func(item *chat.Session) {
		item.TurnsUsed++
		item.TaskID = task.ID
		item.LatestRunID = finalRun.ID
	}); incErr == nil {
		updated = inc
	} else {
		h.logger.WarnContext(r.Context(), "chat.agent.turn_counter_increment_failed", "session_id", session.ID, "error", incErr)
	}
	h.agentChatLive.publishSession(updated)
	WriteJSON(w, http.StatusOK, ChatSessionResponse{Object: "chat_session", Data: renderChatSession(updated, h.agentChatSnapshotConfig())})
}

func hecateAgentSegmentID(session chat.Session) string {
	if strings.TrimSpace(session.TaskID) != "" {
		return "task:" + strings.TrimSpace(session.TaskID)
	}
	return newChatID("segment")
}

func hecateAgentMessageSnapshot(session chat.Session, caps types.ModelCapabilities, segmentID string) chat.Message {
	return chat.Message{
		ExecutionMode: chat.ExecutionModeHecateTask,
		ToolsEnabled:  true,
		SegmentID:     segmentID,
		TaskID:        session.TaskID,
		Provider:      session.Provider,
		Model:         session.Model,
		Capabilities:  caps,
	}
}

func (h *Handler) hecateAgentSessionBusy(ctx context.Context, session chat.Session) (bool, string) {
	if session.TaskID == "" || session.LatestRunID == "" || h.taskStore == nil {
		return false, ""
	}
	run, found, err := h.taskStore.GetRun(ctx, session.TaskID, session.LatestRunID)
	if err != nil || !found {
		return false, ""
	}
	return !types.IsTerminalTaskRunStatus(run.Status), run.Status
}

func shouldStartNewHecateAgentSegment(session chat.Session, provider, model string) bool {
	if session.TaskID == "" {
		return true
	}
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	for i := len(session.Messages) - 1; i >= 0; i-- {
		message := session.Messages[i]
		if strings.TrimSpace(message.Content) == "" && message.Role == "assistant" {
			continue
		}
		// Post-unification: every Hecate-side message persists as
		// `hecate_task`; the tools-on/off discriminant lives on the
		// per-message ToolsEnabled boolean. A tools-off turn breaks
		// the running tool-task segment, so the next tools-on turn gets
		// its own backing task.
		if !isHecateTaskToolsOnMessage(message) {
			return true
		}
		if provider != "" && strings.TrimSpace(message.Provider) != "" && strings.TrimSpace(message.Provider) != provider {
			return true
		}
		if model != "" && strings.TrimSpace(message.Model) != "" && strings.TrimSpace(message.Model) != model {
			return true
		}
		return false
	}
	return false
}

// isHecateTaskToolsOnMessage reports whether the persisted message
// is a tools-on Hecate-task turn — i.e., a turn that ran through the
// agent_loop runtime with tools. Used by segment continuation logic
// to detect when a turn breaks a running tool-task segment.
func isHecateTaskToolsOnMessage(message chat.Message) bool {
	if message.ExecutionMode != chat.ExecutionModeHecateTask {
		return false
	}
	return message.ToolsEnabled
}

func (h *Handler) waitForHecateAgentRun(ctx context.Context, taskID, runID, sessionID, messageID string) (types.TaskRun, error) {
	ticker := time.NewTicker(hecateAgentPollInterval)
	defer ticker.Stop()
	projector := newTaskRunStreamProjector(h.taskStore)
	var lastStatus string
	var lastActivitySignature string
	var lastContent string
	for {
		run, found, err := h.taskStore.GetRun(ctx, taskID, runID)
		if err != nil {
			return types.TaskRun{}, err
		}
		if !found {
			return types.TaskRun{}, fmt.Errorf("task run %q not found", runID)
		}
		taskActivities := []chat.Activity(nil)
		activitySignature := ""
		if state, stateErr := projector.liveState(ctx, taskID, runID); stateErr == nil {
			taskActivities = agentChatActivitiesFromTaskActivity(state.Activity)
			activitySignature = agentChatActivitySignature(taskActivities)
		}
		liveContent := h.finalHecateAgentAnswer(ctx, taskID, runID)
		contentChanged := liveContent != "" && liveContent != lastContent
		if run.Status != lastStatus || (activitySignature != "" && activitySignature != lastActivitySignature) || contentChanged {
			lastStatus = run.Status
			if activitySignature != "" {
				lastActivitySignature = activitySignature
			}
			if contentChanged {
				lastContent = liveContent
			}
			updated, updateErr := h.agentChat.UpdateMessage(ctx, sessionID, messageID, func(message *chat.Message) {
				message.RunID = run.ID
				message.RequestID = firstNonEmpty(run.RequestID, message.RequestID)
				message.TraceID = firstNonEmpty(run.TraceID, message.TraceID)
				message.SpanID = firstNonEmpty(run.RootSpanID, message.SpanID)
				message.Context = normalizeContextPacket(message.Context, chat.ContextRefs{
					SessionID: sessionID,
					MessageID: messageID,
					TaskID:    taskID,
					RunID:     run.ID,
				})
				message.Status = agentChatStatusFromTaskRun(run.Status)
				if liveContent != "" {
					message.Content = liveContent
				}
				message.Activities = mergeChatActivity(message.Activities, newHecateAgentRunActivity(taskID, run.ID, run.Status))
				for _, activity := range taskActivities {
					message.Activities = mergeChatActivity(message.Activities, activity)
				}
			})
			if updateErr == nil {
				h.agentChatLive.publishSession(updated)
			}
		}
		if types.IsTerminalTaskRunStatus(run.Status) {
			return run, nil
		}
		select {
		case <-ctx.Done():
			return run, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (h *Handler) finalHecateAgentAnswer(ctx context.Context, taskID, runID string) string {
	artifacts, err := h.taskStore.ListArtifacts(ctx, taskstate.ArtifactFilter{TaskID: taskID, RunID: runID, Kind: "agent_conversation"})
	if err != nil {
		return ""
	}
	for _, artifact := range artifacts {
		if artifact.Kind != "agent_conversation" || strings.TrimSpace(artifact.ContentText) == "" {
			continue
		}
		var messages []types.Message
		if err := json.Unmarshal([]byte(artifact.ContentText), &messages); err != nil {
			continue
		}
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == "assistant" && strings.TrimSpace(messages[i].Content) != "" {
				return strings.TrimSpace(messages[i].Content)
			}
		}
	}
	if answer := h.finalHecateAgentSummary(ctx, taskID, runID); answer != "" {
		return answer
	}
	return ""
}

func (h *Handler) finalHecateAgentSummary(ctx context.Context, taskID, runID string) string {
	artifacts, err := h.taskStore.ListArtifacts(ctx, taskstate.ArtifactFilter{TaskID: taskID, RunID: runID})
	if err != nil {
		return ""
	}
	for _, artifact := range artifacts {
		if artifact.Kind != "summary" || artifact.Name != "agent-final-answer.txt" {
			continue
		}
		if artifact.Status != "ready" && artifact.Status != "applied" {
			continue
		}
		if content := strings.TrimSpace(artifact.ContentText); content != "" {
			return content
		}
	}
	return ""
}

func (h *Handler) finalHecateAgentCommandOutput(ctx context.Context, taskID, runID string) string {
	artifacts, err := h.taskStore.ListArtifacts(ctx, taskstate.ArtifactFilter{TaskID: taskID, RunID: runID})
	if err != nil {
		return ""
	}
	var stdout, stderr string
	for _, artifact := range artifacts {
		if artifact.Status != "ready" && artifact.Status != "applied" {
			continue
		}
		content := strings.TrimSpace(artifact.ContentText)
		if content == "" {
			continue
		}
		switch artifact.Kind {
		case "stdout":
			if stdout == "" {
				stdout = content
			}
		case "stderr":
			if stderr == "" {
				stderr = content
			}
		}
	}
	return formatHecateAgentCommandOutput(stdout, stderr)
}

func mergeHecateAgentAnswerWithCommandOutput(answer, commandOutput string) string {
	answer = strings.TrimSpace(answer)
	commandOutput = strings.TrimSpace(commandOutput)
	if commandOutput == "" {
		return answer
	}
	if answer == "" || hecateAgentAnswerLooksLikeCommandIntro(answer) {
		return commandOutput
	}
	if strings.Contains(answer, commandOutput) {
		return answer
	}
	return answer + "\n\n" + commandOutput
}

func hecateAgentAnswerLooksLikeCommandIntro(answer string) bool {
	normalized := strings.ToLower(strings.TrimSpace(answer))
	return strings.Contains(normalized, "i'll run ") ||
		strings.Contains(normalized, "i will run ") ||
		strings.Contains(normalized, "i’ll run ") ||
		strings.HasSuffix(normalized, "for you:") ||
		strings.HasSuffix(normalized, "for you.")
}

func formatHecateAgentCommandOutput(stdout, stderr string) string {
	sections := make([]string, 0, 2)
	if stdout = strings.TrimSpace(stdout); stdout != "" {
		sections = append(sections, fencedCommandOutput("Command output", stdout))
	}
	if stderr = strings.TrimSpace(stderr); stderr != "" {
		sections = append(sections, fencedCommandOutput("stderr", stderr))
	}
	return strings.Join(sections, "\n\n")
}

func fencedCommandOutput(label, content string) string {
	lang := "text"
	if strings.HasPrefix(strings.TrimSpace(content), "diff --git ") {
		lang = "diff"
	}
	return fmt.Sprintf("%s:\n\n```%s\n%s\n```", label, lang, content)
}

func (h *Handler) finishHecateAgentMessage(ctx context.Context, sessionID, messageID, status, output, errorText string, startedAt, completedAt time.Time, run *types.TaskRun, timing chat.Timing) (chat.Session, error) {
	return h.agentChat.UpdateMessage(ctx, sessionID, messageID, func(message *chat.Message) {
		message.Status = agentChatStatusFromTaskRun(status)
		message.Content = output
		message.Error = errorText
		message.StartedAt = startedAt
		message.CompletedAt = completedAt
		if run != nil {
			message.RunID = run.ID
			message.RequestID = firstNonEmpty(run.RequestID, message.RequestID)
			message.TraceID = firstNonEmpty(run.TraceID, message.TraceID)
			message.SpanID = firstNonEmpty(run.RootSpanID, message.SpanID)
			message.CostMode = "hecate"
			message.Timing = timing
		}
		message.Activities = append(message.Activities, newChatActivity(message.Status, message.Status, finalChatActivityTitle(message.Status), errorText))
	})
}

func agentChatStatusFromTaskRun(status string) string {
	switch status {
	case "queued", "running", "awaiting_approval":
		return status
	case "completed", "failed", "cancelled":
		return status
	default:
		return "running"
	}
}

func newHecateAgentRunActivity(taskID, runID, status string) chat.Activity {
	activity := newChatActivity("task_run", agentChatStatusFromTaskRun(status), "Backing task", humanHecateAgentRunStatus(status))
	activity.ID = "hecate_task_run:" + strings.TrimSpace(runID)
	if strings.TrimSpace(runID) == "" {
		activity.ID = "hecate_task_run"
	}
	return activity
}

func humanHecateAgentRunStatus(status string) string {
	switch status {
	case "queued":
		return "waiting in queue"
	case "running":
		return "running"
	case "awaiting_approval":
		return "waiting for approval"
	case "completed":
		return "completed"
	case "failed":
		return "failed"
	case "cancelled":
		return "cancelled"
	default:
		return strings.ReplaceAll(status, "_", " ")
	}
}

func hecateAgentFallbackOutput(status, taskID, runID, errorText string) string {
	switch status {
	case "awaiting_approval":
		return fmt.Sprintf("Hecate Chat is awaiting approval in task `%s`, run `%s`.", taskID, runID)
	case "queued", "running":
		return fmt.Sprintf("Hecate Chat task `%s` is still %s.", taskID, status)
	case "failed":
		if strings.TrimSpace(errorText) != "" {
			return errorText
		}
		return "Hecate Chat run failed."
	case "cancelled":
		return "Hecate Chat run cancelled."
	default:
		return "Hecate Chat run completed."
	}
}
