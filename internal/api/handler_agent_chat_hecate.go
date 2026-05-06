package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hecate/agent-runtime/internal/agentadapters"
	"github.com/hecate/agent-runtime/internal/agentchat"
	"github.com/hecate/agent-runtime/internal/modelcaps"
	"github.com/hecate/agent-runtime/internal/taskstate"
	"github.com/hecate/agent-runtime/pkg/types"
)

const hecateAgentPollInterval = 250 * time.Millisecond

func (h *Handler) handleCreateHecateAgentChatMessage(w http.ResponseWriter, r *http.Request, session agentchat.Session, req CreateAgentChatMessageRequest) {
	content := strings.TrimSpace(req.Content)
	session.RuntimeKind = "hecate_agent"
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
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "workspace is required for Hecate Agent chat")
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
			WriteError(w, http.StatusUnprocessableEntity, errCodeModelCapability, err.Error())
			return
		}
		caps = resolved
	}
	if !modelcaps.ToolCapable(caps) {
		WriteJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": map[string]any{
				"type":         errCodeModelCapability,
				"message":      "This model has unknown or no tool-calling support. Test it or override capabilities in Settings.",
				"provider":     session.Provider,
				"model":        session.Model,
				"capabilities": caps,
			},
		})
		return
	}

	if busy, runStatus := h.hecateAgentSessionBusy(r.Context(), session); busy {
		WriteJSON(w, http.StatusConflict, map[string]any{
			"error": map[string]any{
				"type":          errCodeAgentSessionBusy,
				"message":       "Hecate Agent is already running for this chat session.",
				"task_id":       session.TaskID,
				"latest_run_id": session.LatestRunID,
				"run_status":    runStatus,
			},
		})
		return
	}

	assistantID := newAgentChatID("msg")
	startedAt := time.Now().UTC()
	trace, traceCtx := h.startAgentChatTrace(w, r)
	defer trace.Finalize()

	runCtx, cancel := context.WithTimeout(traceCtx, agentChatTimeout)
	if !h.agentChatLive.registerRun(session.ID, cancel) {
		cancel()
		WriteError(w, http.StatusConflict, errCodeAgentSessionBusy, "agent chat session is already running")
		return
	}
	defer h.agentChatLive.clearRun(session.ID)
	defer cancel()

	userID := newAgentChatID("msg")
	segmentID := hecateAgentSegmentID(session)
	messageSnapshot := hecateAgentMessageSnapshot(session, caps, segmentID)
	updated, err := h.agentChat.AppendMessage(r.Context(), session.ID, agentchat.Message{
		ID:           userID,
		RuntimeKind:  messageSnapshot.RuntimeKind,
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

	updated, err = h.agentChat.AppendMessage(r.Context(), session.ID, agentchat.Message{
		ID:           assistantID,
		RuntimeKind:  messageSnapshot.RuntimeKind,
		SegmentID:    messageSnapshot.SegmentID,
		TaskID:       messageSnapshot.TaskID,
		RequestID:    RequestIDFromContext(r.Context()),
		TraceID:      trace.TraceID,
		SpanID:       trace.RootSpanID(),
		Provider:     messageSnapshot.Provider,
		Model:        messageSnapshot.Model,
		Capabilities: messageSnapshot.Capabilities,
		Role:         "assistant",
		Content:      "",
		Status:       "running",
		CostMode:     "hecate",
		Workspace:    session.Workspace,
		CreatedAt:    startedAt,
		StartedAt:    startedAt,
		Activities: []agentchat.Activity{
			newAgentChatActivity("started", "running", "Starting Hecate Agent", "Creating or continuing the backing task run"),
		},
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	h.agentChatLive.publishSession(updated)

	forceNewTask := shouldStartNewHecateAgentSegment(session)
	task, run, err := h.startOrContinueHecateAgentRun(runCtx, session, content, forceNewTask)
	if err != nil {
		h.finishHecateAgentMessage(r.Context(), session.ID, assistantID, "failed", "", err.Error(), startedAt, time.Now().UTC(), nil)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	segmentID = "task:" + task.ID
	messageSnapshot = hecateAgentMessageSnapshot(agentchat.Session{
		ID:           session.ID,
		RuntimeKind:  session.RuntimeKind,
		TaskID:       task.ID,
		Provider:     session.Provider,
		Model:        session.Model,
		Capabilities: caps,
	}, caps, segmentID)
	if userUpdated, userUpdateErr := h.agentChat.UpdateMessage(r.Context(), session.ID, userID, func(message *agentchat.Message) {
		message.RuntimeKind = messageSnapshot.RuntimeKind
		message.SegmentID = messageSnapshot.SegmentID
		message.TaskID = messageSnapshot.TaskID
		message.Provider = messageSnapshot.Provider
		message.Model = messageSnapshot.Model
		message.Capabilities = messageSnapshot.Capabilities
	}); userUpdateErr == nil {
		h.agentChatLive.publishSession(userUpdated)
	}
	updated, err = h.agentChat.UpdateMessage(r.Context(), session.ID, assistantID, func(message *agentchat.Message) {
		message.RuntimeKind = messageSnapshot.RuntimeKind
		message.SegmentID = messageSnapshot.SegmentID
		message.TaskID = messageSnapshot.TaskID
		message.RunID = run.ID
		message.Provider = messageSnapshot.Provider
		message.Model = messageSnapshot.Model
		message.Capabilities = messageSnapshot.Capabilities
		message.Activities = mergeAgentChatActivity(message.Activities, newHecateAgentRunActivity(task.ID, run.ID, run.Status))
	})
	if err == nil {
		h.agentChatLive.publishSession(updated)
	}
	updated, err = h.agentChat.UpdateSession(r.Context(), session.ID, func(item *agentchat.Session) {
		item.RuntimeKind = "hecate_agent"
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
	updated, err = h.finishHecateAgentMessage(r.Context(), session.ID, assistantID, status, output, errorText, startedAt, completedAt, &finalRun)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if inc, incErr := h.agentChat.UpdateSession(r.Context(), session.ID, func(item *agentchat.Session) {
		item.TurnsUsed++
		item.TaskID = task.ID
		item.LatestRunID = finalRun.ID
	}); incErr == nil {
		updated = inc
	} else {
		h.logger.WarnContext(r.Context(), "agent_chat.hecate_agent.turn_counter_increment_failed", "session_id", session.ID, "error", incErr)
	}
	h.agentChatLive.publishSession(updated)
	WriteJSON(w, http.StatusOK, AgentChatSessionResponse{Object: "agent_chat_session", Data: renderAgentChatSession(updated, h.agentChatSnapshotConfig())})
}

func hecateAgentSegmentID(session agentchat.Session) string {
	if strings.TrimSpace(session.TaskID) != "" {
		return "task:" + strings.TrimSpace(session.TaskID)
	}
	return newAgentChatID("segment")
}

func hecateAgentMessageSnapshot(session agentchat.Session, caps types.ModelCapabilities, segmentID string) agentchat.Message {
	runtimeKind := session.RuntimeKind
	if runtimeKind == "" {
		runtimeKind = "hecate_agent"
	}
	return agentchat.Message{
		RuntimeKind:  runtimeKind,
		SegmentID:    segmentID,
		TaskID:       session.TaskID,
		Provider:     session.Provider,
		Model:        session.Model,
		Capabilities: caps,
	}
}

func (h *Handler) hecateAgentSessionBusy(ctx context.Context, session agentchat.Session) (bool, string) {
	if session.TaskID == "" || session.LatestRunID == "" || h.taskStore == nil {
		return false, ""
	}
	run, found, err := h.taskStore.GetRun(ctx, session.TaskID, session.LatestRunID)
	if err != nil || !found {
		return false, ""
	}
	return !types.IsTerminalTaskRunStatus(run.Status), run.Status
}

func shouldStartNewHecateAgentSegment(session agentchat.Session) bool {
	if session.TaskID == "" {
		return true
	}
	for i := len(session.Messages) - 1; i >= 0; i-- {
		message := session.Messages[i]
		if strings.TrimSpace(message.Content) == "" && message.Role == "assistant" {
			continue
		}
		return message.RuntimeKind != "hecate_agent"
	}
	return false
}

func (h *Handler) startOrContinueHecateAgentRun(ctx context.Context, session agentchat.Session, prompt string, forceNewTask bool) (types.Task, types.TaskRun, error) {
	if h.taskStore == nil || h.taskRunner == nil {
		return types.Task{}, types.TaskRun{}, fmt.Errorf("task runtime is not configured")
	}
	if session.TaskID == "" || forceNewTask {
		now := time.Now().UTC()
		title := strings.TrimSpace(session.Title)
		if title == "" {
			title = "Hecate Agent chat"
		}
		task := types.Task{
			ID:                 newTaskID(),
			Title:              title,
			Prompt:             prompt,
			ExecutionKind:      "agent_loop",
			ExecutionProfile:   "chat_hecate_agent",
			OriginKind:         "agent_chat",
			OriginID:           session.ID,
			WorkspaceMode:      "in_place",
			WorkingDirectory:   session.Workspace,
			SandboxAllowedRoot: session.Workspace,
			Status:             "queued",
			Priority:           "normal",
			RequestedProvider:  session.Provider,
			RequestedModel:     session.Model,
			CreatedAt:          now,
			UpdatedAt:          now,
		}
		task, err := h.taskStore.CreateTask(ctx, task)
		if err != nil {
			return types.Task{}, types.TaskRun{}, err
		}
		result, err := h.taskRunner.StartTask(ctx, task, newOpaqueTaskResourceID)
		if err != nil {
			return types.Task{}, types.TaskRun{}, err
		}
		return result.Task, result.Run, nil
	}

	task, found, err := h.taskStore.GetTask(ctx, session.TaskID)
	if err != nil {
		return types.Task{}, types.TaskRun{}, err
	}
	if !found {
		return types.Task{}, types.TaskRun{}, fmt.Errorf("backing task %q not found", session.TaskID)
	}
	run, found, err := h.taskStore.GetRun(ctx, task.ID, session.LatestRunID)
	if err != nil {
		return types.Task{}, types.TaskRun{}, err
	}
	if !found {
		return types.Task{}, types.TaskRun{}, fmt.Errorf("latest task run %q not found", session.LatestRunID)
	}
	result, err := h.taskRunner.ContinueAgentTask(ctx, task, run, prompt, newOpaqueTaskResourceID)
	if err != nil {
		return types.Task{}, types.TaskRun{}, err
	}
	return result.Task, result.Run, nil
}

func (h *Handler) waitForHecateAgentRun(ctx context.Context, taskID, runID, sessionID, messageID string) (types.TaskRun, error) {
	ticker := time.NewTicker(hecateAgentPollInterval)
	defer ticker.Stop()
	var lastStatus string
	var lastActivitySignature string
	for {
		run, found, err := h.taskStore.GetRun(ctx, taskID, runID)
		if err != nil {
			return types.TaskRun{}, err
		}
		if !found {
			return types.TaskRun{}, fmt.Errorf("task run %q not found", runID)
		}
		taskActivities := []agentchat.Activity(nil)
		activitySignature := ""
		if state, stateErr := h.buildTaskRunStreamState(ctx, taskID, runID); stateErr == nil {
			taskActivities = agentChatActivitiesFromTaskActivity(state.Activity)
			activitySignature = agentChatActivitySignature(taskActivities)
		}
		if run.Status != lastStatus || (activitySignature != "" && activitySignature != lastActivitySignature) {
			lastStatus = run.Status
			if activitySignature != "" {
				lastActivitySignature = activitySignature
			}
			updated, updateErr := h.agentChat.UpdateMessage(ctx, sessionID, messageID, func(message *agentchat.Message) {
				message.RunID = run.ID
				message.Status = agentChatStatusFromTaskRun(run.Status)
				message.Activities = mergeAgentChatActivity(message.Activities, newHecateAgentRunActivity(taskID, run.ID, run.Status))
				for _, activity := range taskActivities {
					message.Activities = mergeAgentChatActivity(message.Activities, activity)
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

func (h *Handler) finishHecateAgentMessage(ctx context.Context, sessionID, messageID, status, output, errorText string, startedAt, completedAt time.Time, run *types.TaskRun) (agentchat.Session, error) {
	return h.agentChat.UpdateMessage(ctx, sessionID, messageID, func(message *agentchat.Message) {
		message.Status = agentChatStatusFromTaskRun(status)
		message.Content = output
		message.Error = errorText
		message.StartedAt = startedAt
		message.CompletedAt = completedAt
		if run != nil {
			message.RunID = run.ID
			message.CostMode = "hecate"
		}
		message.Activities = append(message.Activities, newAgentChatActivity(message.Status, message.Status, finalAgentChatActivityTitle(message.Status), errorText))
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

func newHecateAgentRunActivity(taskID, runID, status string) agentchat.Activity {
	detailParts := []string{humanHecateAgentRunStatus(status)}
	if strings.TrimSpace(taskID) != "" {
		detailParts = append(detailParts, taskID)
	}
	if strings.TrimSpace(runID) != "" {
		detailParts = append(detailParts, runID)
	}
	activity := newAgentChatActivity("task_run", agentChatStatusFromTaskRun(status), "Backing task", strings.Join(detailParts, " · "))
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
		return fmt.Sprintf("Hecate Agent is awaiting approval in task `%s`, run `%s`.", taskID, runID)
	case "queued", "running":
		return fmt.Sprintf("Hecate Agent task `%s` is still %s.", taskID, status)
	case "failed":
		if strings.TrimSpace(errorText) != "" {
			return errorText
		}
		return "Hecate Agent run failed."
	case "cancelled":
		return "Hecate Agent run cancelled."
	default:
		return "Hecate Agent run completed."
	}
}
