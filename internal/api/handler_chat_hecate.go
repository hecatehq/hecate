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
	"github.com/hecatehq/hecate/internal/chatattachments"
	"github.com/hecatehq/hecate/internal/chatcontext"
	"github.com/hecatehq/hecate/internal/modelcaps"
	"github.com/hecatehq/hecate/internal/taskapp"
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
func (h *Handler) handleCreateHecateChatMessage(w http.ResponseWriter, r *http.Request, session chat.Session, req CreateChatMessageRequest, toolsEnabled bool, lifecycle agentChatLifecycleSnapshot, requestGuard *chatMessageRequestGuard) {
	if !toolsEnabled {
		h.handleDirectModelTurn(w, r, session, req, lifecycle, requestGuard)
		return
	}
	content := strings.TrimSpace(req.Content)
	if workspace := strings.TrimSpace(req.Workspace); workspace != "" {
		resolved, err := agentadapters.ValidateWorkspace(workspace)
		if err != nil {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
			return
		}
		// Once a managed task exists, the durable session path is the
		// runtime-owned workspace. Older clients may keep resending the original
		// source folder; accepting it here would reclone away dirty managed work.
		if strings.TrimSpace(session.TaskID) == "" || chat.EffectiveWorkspaceMode(session.WorkspaceMode) == chat.WorkspaceModeInPlace {
			session.Workspace = resolved
			session.WorkspaceBranch = workspaceGitBranch(resolved)
		}
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
	admittedInputProviderInstance := types.ProviderInstanceIdentity{}
	if len(req.AttachmentIDs) > 0 {
		if strings.TrimSpace(session.Model) == "" {
			writeAgentChatModelRequired(w, "model")
			return
		}
		requestedProvider := strings.TrimSpace(session.Provider)
		if strings.EqualFold(requestedProvider, "auto") {
			requestedProvider = ""
			session.Provider = ""
		}
		if requestedProvider != "" {
			resolvedRoute, resolveErr := h.modelApplication().ResolveProviderRoute(r.Context(), requestedProvider, session.Model)
			if resolveErr != nil {
				writeAgentChatModelResolutionError(w, resolveErr)
				return
			}
			if resolvedRoute.Name != "" {
				session.Provider = resolvedRoute.Name
			}
			admittedInputProviderInstance = resolvedRoute.Instance
		}
		imageCapable, imageErr := h.modelApplication().SupportsImageInput(r.Context(), session.Provider, session.Model)
		if imageErr != nil {
			writeAgentChatModelResolutionError(w, imageErr)
			return
		}
		if !imageCapable {
			writeAgentChatImageCapabilityRequired(w)
			return
		}
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

	mcpServers, err := taskapp.NormalizeMCPServerConfigs(taskMCPServerCommandsFromRequest(req.MCPServers), h.secretCipher, h.config.Server.TaskMaxMCPServersPerTask)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}

	if busy, runStatus := h.hecateAgentSessionBusy(r.Context(), session); busy {
		writeHecateAgentBusy(w, session, runStatus)
		return
	}

	turnID := newChatID("turn")
	assistantID := newChatID("msg")
	startedAt := time.Now().UTC()
	trace, traceCtx := h.startAgentChatTrace(w, r)
	defer trace.Finalize()

	// The task-backed chat watcher is server-owned once the user row commits.
	// Keep its wait lifetime independent of the browser connection while the
	// registered live-turn cancel hook and explicit 30-minute watcher ceiling
	// remain authoritative; the orchestrator-owned Task can outlive that ceiling.
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(traceCtx), agentChatTimeout)
	switch h.agentChatLive.registerTurn(lifecycle, cancel) {
	case agentChatTurnAdmissionClosed:
		cancel()
		writeChatSessionStopping(w)
		return
	case agentChatTurnBusy:
		cancel()
		WriteErrorDetails(w, http.StatusConflict, errCodeAgentSessionBusy, "agent chat session is already running", ErrorDetails{
			UserMessage:    "This chat is already running.",
			OperatorAction: "Wait for the active turn to finish or stop it before sending another message.",
		})
		return
	}
	defer h.agentChatLive.clearTurn(session.ID)
	defer cancel()

	userID := newChatID("msg")
	claimRef := chatattachments.ClaimRef{
		SessionID:     session.ID,
		MessageID:     userID,
		AttachmentIDs: req.AttachmentIDs,
	}
	var resolvedAttachments []chatattachments.StoredAttachment
	attachmentClaimPending := false
	appendAttempted := false
	if len(req.AttachmentIDs) > 0 {
		resolvedAttachments, err = h.chatApplication().ClaimAttachments(r.Context(), claimRef)
		if err != nil {
			writeChatAttachmentAppError(w, err, chatAttachmentClaimFailureMessage)
			return
		}
		claimRef.AttachmentIDs = make([]string, 0, len(resolvedAttachments))
		for _, attachment := range resolvedAttachments {
			claimRef.AttachmentIDs = append(claimRef.AttachmentIDs, attachment.ID)
			if err := validateStoredChatImageAttachment(attachment); err != nil {
				WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "stored image attachment failed integrity validation")
				return
			}
		}
		attachmentClaimPending = true
		defer func() {
			if !attachmentClaimPending || appendAttempted {
				return
			}
			cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
			defer cleanupCancel()
			if err := h.chatApplication().ResolveAttachmentClaim(cleanupCtx, claimRef, chatattachments.ClaimReleased); err != nil {
				h.logger.WarnContext(cleanupCtx, "chat.attachment_claim_release_failed",
					"session_id", session.ID,
					"message_id", userID,
					"error", err,
				)
			}
		}()
	}
	forceNewTask := shouldStartNewHecateAgentSegment(session, session.Provider, session.Model) || len(mcpServers) > 0
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
	appendAttempted = true
	updated, err := requestGuard.appendUserMessage(r.Context(), session.ID, chat.Message{
		ID:            userID,
		TurnID:        turnID,
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
		Attachments:  chatMessageAttachments(resolvedAttachments),
		CreatedAt:    startedAt,
	})
	if err != nil {
		if attachmentClaimPending {
			if reconcileErr := h.reconcileChatAttachmentClaim(r.Context(), claimRef, resolvedAttachments); reconcileErr != nil {
				h.logger.WarnContext(r.Context(), "chat.attachment_claim_reconcile_failed",
					"session_id", session.ID,
					"message_id", userID,
					"error", reconcileErr,
				)
			} else {
				attachmentClaimPending = false
			}
		}
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	finalizeCtx, finalizeCancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
	finalizeErr := h.chatApplication().ResolveAttachmentClaim(finalizeCtx, claimRef, chatattachments.ClaimLinked)
	finalizeCancel()
	if finalizeErr != nil {
		if reconcileErr := h.reconcileChatAttachmentClaim(r.Context(), claimRef, resolvedAttachments); reconcileErr != nil {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, chatAttachmentFinalizeFailureMessage)
			return
		}
	}
	attachmentClaimPending = false
	h.agentChatLive.publishSession(updated)
	trace.Record(telemetry.EventAgentChatTurnStarted, hecateAgentChatTraceAttrs(session, turnID, "", "", assistantID, map[string]any{
		telemetry.AttrHecateChatTurnStatus: "running",
	}))

	initialWriteCtx, initialWriteCancel := newAgentChatPersistenceContext(r.Context())
	taskSystemPrompt := h.hecateChatTaskSystemPrompt(initialWriteCtx, session, req.SystemPrompt, forceNewTask)
	contextPacket := h.hecateTaskContextPacket(initialWriteCtx, session, messageSnapshot.Provider, messageSnapshot.Model, taskSystemPrompt, forceNewTask)
	contextPacket.ID = newChatID("ctx")
	contextPacket = chatcontext.Normalize(contextPacket, chatcontext.MergeRefs(
		chatcontext.ChatMessageRefs(session.ID, turnID, assistantID, session.ProjectID),
		chatcontext.TaskRunRefs(messageSnapshot.TaskID, "", session.ProjectID),
	))
	updated, err = h.agentChat.AppendMessage(initialWriteCtx, session.ID, chat.Message{
		ID:            assistantID,
		TurnID:        turnID,
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
	initialWriteCancel()
	if err != nil {
		if r.Context().Err() == nil {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		}
		return
	}
	h.agentChatLive.publishSession(updated)

	inputRef := ""
	if len(resolvedAttachments) > 0 {
		inputRef = userID
	}
	task, run, err := h.hecateAgentTaskOrchestrator().StartOrContinue(runCtx, hecateAgentTaskRunCommand{
		Session:               session,
		Prompt:                content,
		InputRef:              inputRef,
		InputProviderInstance: admittedInputProviderInstance,
		SystemPrompt:          taskSystemPrompt,
		ForceNewTask:          forceNewTask,
		MCPServers:            mcpServers,
		ContextPacket:         contextPacket,
	})
	if err != nil {
		completedAt := time.Now().UTC()
		status := "failed"
		errorText := err.Error()
		if errors.Is(runCtx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
			status = "cancelled"
			errorText = "cancelled"
		}
		trace.Record(agentChatTerminalEvent(status), hecateAgentChatTraceAttrs(session, turnID, "", "", assistantID, map[string]any{
			telemetry.AttrHecateChatTurnStatus:     status,
			telemetry.AttrHecateChatTurnDurationMS: completedAt.Sub(startedAt).Milliseconds(),
			telemetry.AttrHecateResult:             telemetry.ResultError,
			telemetry.AttrHecateErrorKind:          telemetry.ErrorKindOther,
			telemetry.AttrErrorType:                "agent_start_failed",
			telemetry.AttrErrorMessage:             errorText,
		}))
		h.agentChatMetrics.RecordTurn(traceCtx, telemetry.AgentChatTurnMetricsRecord{
			AdapterID:  "hecate",
			DriverKind: "hecate",
			Status:     status,
			Result:     telemetry.ResultError,
			DurationMS: completedAt.Sub(startedAt).Milliseconds(),
		})
		terminalCtx, terminalCancel := newAgentChatPersistenceContext(r.Context())
		terminal, terminalErr := h.finishHecateAgentMessage(
			terminalCtx,
			session.ID,
			assistantID,
			status,
			hecateAgentFallbackOutput(status, "", "", errorText),
			errorText,
			startedAt,
			completedAt,
			nil,
			chat.Timing{},
		)
		terminalCancel()
		if terminalErr != nil {
			h.logger.ErrorContext(context.WithoutCancel(r.Context()), "chat.hecate.assistant_start_failure_update_failed",
				"session_id", session.ID,
				"message_id", assistantID,
				"status", status,
				"error", terminalErr,
			)
		} else {
			h.agentChatLive.publishSession(terminal)
		}
		if r.Context().Err() == nil {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		}
		return
	}
	segmentID = "task:" + task.ID
	executionWorkspace := strings.TrimSpace(run.WorkspacePath)
	if executionWorkspace == "" {
		executionWorkspace = session.Workspace
	}
	executionWorkspaceBranch := session.WorkspaceBranch
	if executionWorkspace != session.Workspace {
		executionWorkspaceBranch = workspaceGitBranch(executionWorkspace)
	}
	session.Workspace = executionWorkspace
	session.WorkspaceBranch = executionWorkspaceBranch
	messageSnapshot = hecateAgentMessageSnapshot(chat.Session{
		ID:           session.ID,
		TaskID:       task.ID,
		Provider:     session.Provider,
		Model:        session.Model,
		Capabilities: caps,
	}, caps, segmentID)
	linkCtx, linkCancel := newAgentChatPersistenceContext(r.Context())
	updated, err = h.linkHecateTaskRun(linkCtx, session, userID, assistantID, task, run, messageSnapshot, caps)
	linkCancel()
	if err != nil {
		h.logger.ErrorContext(context.WithoutCancel(r.Context()), "chat.hecate.task_link_failed", "session_id", session.ID, "task_id", task.ID, "run_id", run.ID, "error", err)
		cancelConfirmed, cancelErr := h.cancelUnlinkedHecateTaskRun(r.Context(), task, run.ID)
		if cancelErr != nil {
			h.logger.ErrorContext(context.WithoutCancel(r.Context()), "chat.hecate.task_link_cancel_failed", "session_id", session.ID, "task_id", task.ID, "run_id", run.ID, "error", cancelErr)
		}
		completedAt := time.Now().UTC()
		failureOutput := "Hecate could not safely link the managed workspace to this chat. The backing run was stopped."
		operatorAction := "Retry after checking storage health. Workspace review is disabled until the link is repaired."
		if !cancelConfirmed {
			failureOutput = "Hecate could not safely link the managed workspace to this chat, and could not confirm that the backing run stopped."
			operatorAction = "Check task-runtime storage health and cancel the newest chat-origin Run from Tasks before retrying. Workspace review remains disabled."
		}
		terminalCtx, terminalCancel := newAgentChatPersistenceContext(r.Context())
		terminal, terminalErr := h.finishHecateAgentMessage(terminalCtx, session.ID, assistantID, "failed", failureOutput, "managed workspace link failed", startedAt, completedAt, &run, chat.Timing{})
		terminalCancel()
		if terminalErr != nil {
			h.logger.ErrorContext(context.WithoutCancel(r.Context()), "chat.hecate.task_link_terminal_update_failed", "session_id", session.ID, "message_id", assistantID, "error", terminalErr)
		} else {
			h.agentChatLive.publishSession(terminal)
		}
		trace.Record(agentChatTerminalEvent("failed"), hecateAgentChatTraceAttrs(session, turnID, task.ID, run.ID, assistantID, map[string]any{
			telemetry.AttrHecateChatTurnStatus:     "failed",
			telemetry.AttrHecateChatTurnDurationMS: completedAt.Sub(startedAt).Milliseconds(),
			telemetry.AttrHecateResult:             telemetry.ResultError,
			telemetry.AttrHecateErrorKind:          telemetry.ErrorKindOther,
			telemetry.AttrErrorType:                "chat_task_link_failed",
		}))
		h.agentChatMetrics.RecordTurn(traceCtx, telemetry.AgentChatTurnMetricsRecord{
			AdapterID:  "hecate",
			DriverKind: "hecate",
			Status:     "failed",
			Result:     telemetry.ResultError,
			DurationMS: completedAt.Sub(startedAt).Milliseconds(),
		})
		if r.Context().Err() == nil {
			WriteErrorDetails(w, http.StatusInternalServerError, errCodeGatewayError, "failed to persist the chat task workspace link", ErrorDetails{
				UserMessage:    "The managed workspace started, but Hecate could not link it safely to this chat.",
				OperatorAction: operatorAction,
			})
		}
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
			// A watcher error or explicit live-turn cancellation must settle
			// the transcript instead of restoring a stale non-terminal task
			// snapshot over the local terminal outcome.
			if err == nil {
				status = finalRun.Status
			}
		}
	}
	if finalRun.LastError != "" && errorText == "" {
		errorText = finalRun.LastError
	}

	terminalCtx, terminalCancel := newAgentChatPersistenceContext(r.Context())
	defer terminalCancel()
	output := ""
	if status == "completed" {
		output = h.finalHecateAgentAnswer(terminalCtx, task.ID, finalRun.ID)
		if commandOutput := h.finalHecateAgentCommandOutput(terminalCtx, task.ID, finalRun.ID); commandOutput != "" {
			output = mergeHecateAgentAnswerWithCommandOutput(output, commandOutput)
		}
	}
	if output == "" {
		output = hecateAgentFallbackOutput(status, task.ID, finalRun.ID, errorText)
	}
	completedAt := time.Now().UTC()
	durationMS := completedAt.Sub(startedAt).Milliseconds()
	timing := h.hecateAgentTiming(terminalCtx, finalRun, startedAt, completedAt)
	resultLabel := telemetry.ResultSuccess
	if status == "failed" || status == "cancelled" {
		resultLabel = telemetry.ResultError
	}
	terminalAttrs := hecateAgentChatTraceAttrs(session, turnID, task.ID, finalRun.ID, assistantID, map[string]any{
		telemetry.AttrHecateChatTurnStatus:     status,
		telemetry.AttrHecateChatTurnDurationMS: durationMS,
		telemetry.AttrHecateAgentOutputBytes:   int64(len(output)),
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
	h.agentChatMetrics.RecordTurn(traceCtx, telemetry.AgentChatTurnMetricsRecord{
		AdapterID:  "hecate",
		DriverKind: "hecate",
		Status:     status,
		Result:     resultLabel,
		DurationMS: durationMS,
		Timing:     agentChatTurnTimingMetrics(timing),
	})
	if status == "cancelled" {
		reason := hecateAgentChatCancellationReason(finalRun, h.agentChatLive.turnCancelReason(session.ID))
		h.agentChatMetrics.RecordChatCancelled(traceCtx, telemetry.AgentChatCancelledRecord{
			AdapterID: "hecate",
			Reason:    reason,
		})
	}
	updated, err = h.finishHecateAgentMessage(terminalCtx, session.ID, assistantID, status, output, errorText, startedAt, completedAt, &finalRun, timing)
	if err != nil {
		h.logger.ErrorContext(terminalCtx, "chat.hecate.assistant_terminal_update_failed",
			"session_id", session.ID,
			"message_id", assistantID,
			"status", status,
			"error", err,
		)
		if r.Context().Err() == nil {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		}
		return
	}
	// The watcher snapshots this marker while the run is live, but the terminal
	// path gets one bounded reconciliation pass as well. A transient transcript
	// write failure must not make an already-completed provider turn fail, yet
	// leaving the marker behind forever would conservatively omit a legitimately
	// disclosed image from later same-route history.
	if routeErr := h.persistHecateAgentInputRouteWithRetry(terminalCtx, session.ID, finalRun); routeErr != nil {
		h.logger.WarnContext(terminalCtx, "chat.hecate.input_route_terminal_reconcile_failed",
			"session_id", session.ID,
			"message_id", finalRun.InputRef,
			"run_id", finalRun.ID,
			"error", routeErr,
		)
	}
	if inc, incErr := h.agentChat.UpdateSession(terminalCtx, session.ID, func(item *chat.Session) {
		item.TurnsUsed++
		item.TaskID = task.ID
		item.LatestRunID = finalRun.ID
		item.Provider = messageSnapshot.Provider
		item.Model = messageSnapshot.Model
		item.Capabilities = caps
		item.Workspace = session.Workspace
		item.WorkspaceBranch = session.WorkspaceBranch
	}); incErr == nil {
		updated = inc
	} else {
		h.logger.WarnContext(terminalCtx, "chat.agent.turn_counter_increment_failed", "session_id", session.ID, "error", incErr)
	}
	h.agentChatLive.publishSession(updated)
	if r.Context().Err() != nil {
		return
	}
	WriteJSON(w, http.StatusOK, ChatSessionResponse{
		Object:         "chat_session",
		Data:           renderChatSession(updated, h.agentChatSnapshotConfig()),
		MessageRequest: requestGuard.responseMetadata(false, ""),
	})
}

// hecateAgentChatCancellationReason prefers the runner's durable operator
// outcome because CancelRun persists it before it can wake the detached chat
// watcher. The live reason remains the authority for non-task chat cancels.
func hecateAgentChatCancellationReason(run types.TaskRun, liveReason string) string {
	if run.LastError == "run cancelled: operator" {
		return "operator"
	}
	if liveReason != "" {
		return liveReason
	}
	return "request_cancelled"
}

func (h *Handler) linkHecateTaskRun(
	ctx context.Context,
	session chat.Session,
	userMessageID string,
	assistantMessageID string,
	task types.Task,
	run types.TaskRun,
	messageSnapshot chat.Message,
	caps types.ModelCapabilities,
) (chat.Session, error) {
	const maxAttempts = 3
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		updated, err := h.agentChat.LinkTaskRun(ctx, session.ID, userMessageID, assistantMessageID, func(item *chat.Session, userMessage *chat.Message, assistantMessage *chat.Message) {
			item.TaskID = task.ID
			item.LatestRunID = run.ID
			item.Provider = session.Provider
			item.Model = session.Model
			item.Capabilities = caps
			item.Workspace = session.Workspace
			item.WorkspaceBranch = session.WorkspaceBranch

			userMessage.ExecutionMode = messageSnapshot.ExecutionMode
			userMessage.SegmentID = messageSnapshot.SegmentID
			userMessage.TaskID = messageSnapshot.TaskID
			userMessage.RunID = run.ID
			userMessage.Provider = messageSnapshot.Provider
			userMessage.Model = messageSnapshot.Model
			userMessage.Capabilities = messageSnapshot.Capabilities
			userMessage.Workspace = session.Workspace
			userMessage.Context.Workspace = session.Workspace
			userMessage.Context = chatcontext.Normalize(userMessage.Context, chatcontext.MergeRefs(
				chatcontext.ChatMessageRefs(session.ID, userMessage.TurnID, userMessageID, session.ProjectID),
				chatcontext.TaskRunRefs(task.ID, run.ID, session.ProjectID),
			))

			assistantMessage.ExecutionMode = messageSnapshot.ExecutionMode
			assistantMessage.SegmentID = messageSnapshot.SegmentID
			assistantMessage.TaskID = messageSnapshot.TaskID
			assistantMessage.RunID = run.ID
			assistantMessage.RequestID = firstNonEmpty(run.RequestID, assistantMessage.RequestID)
			assistantMessage.TraceID = firstNonEmpty(run.TraceID, assistantMessage.TraceID)
			assistantMessage.SpanID = firstNonEmpty(run.RootSpanID, assistantMessage.SpanID)
			assistantMessage.Provider = messageSnapshot.Provider
			assistantMessage.Model = messageSnapshot.Model
			assistantMessage.Capabilities = messageSnapshot.Capabilities
			assistantMessage.Workspace = session.Workspace
			assistantMessage.Context.Workspace = session.Workspace
			assistantMessage.Context = chatcontext.Normalize(assistantMessage.Context, chatcontext.MergeRefs(
				chatcontext.ChatMessageRefs(session.ID, assistantMessage.TurnID, assistantMessageID, session.ProjectID),
				chatcontext.TaskRunRefs(task.ID, run.ID, session.ProjectID),
			))
			assistantMessage.Activities = mergeChatActivity(assistantMessage.Activities, newHecateAgentRunActivity(task.ID, run.ID, run.Status))
		})
		if err == nil {
			return updated, nil
		}
		lastErr = err
		if ctx.Err() != nil || attempt == maxAttempts-1 {
			break
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return chat.Session{}, errors.Join(lastErr, ctx.Err())
		case <-timer.C:
		}
	}
	verifyCtx, verifyCancel := newAgentChatPersistenceContext(ctx)
	authoritative, found, readErr := h.agentChat.Get(verifyCtx, session.ID)
	verifyCancel()
	if readErr == nil && found && hecateTaskRunLinkMatches(authoritative, userMessageID, assistantMessageID, task.ID, run.ID, session.Workspace) {
		return authoritative, nil
	}
	if readErr != nil {
		lastErr = errors.Join(lastErr, fmt.Errorf("verify task-run link after ambiguous write: %w", readErr))
	}
	return chat.Session{}, lastErr
}

func hecateTaskRunLinkMatches(session chat.Session, userMessageID, assistantMessageID, taskID, runID, workspace string) bool {
	if session.TaskID != taskID || session.LatestRunID != runID || strings.TrimSpace(session.Workspace) != strings.TrimSpace(workspace) {
		return false
	}
	userLinked := false
	assistantLinked := false
	for _, message := range session.Messages {
		switch message.ID {
		case userMessageID:
			userLinked = message.TaskID == taskID && message.RunID == runID && strings.TrimSpace(message.Workspace) == strings.TrimSpace(workspace)
		case assistantMessageID:
			assistantLinked = message.TaskID == taskID && message.RunID == runID && strings.TrimSpace(message.Workspace) == strings.TrimSpace(workspace)
		}
	}
	return userLinked && assistantLinked
}

func (h *Handler) cancelUnlinkedHecateTaskRun(ctx context.Context, task types.Task, runID string) (bool, error) {
	if h.taskRunner == nil {
		return false, fmt.Errorf("task runner is not configured")
	}
	const maxAttempts = 3
	durableCtx := context.WithoutCancel(ctx)
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		cancelCtx, cancel := newAgentChatPersistenceContext(durableCtx)
		_, err := h.taskRunner.CancelRun(cancelCtx, task, runID, "chat transcript link failed")
		cancel()
		if err == nil || strings.Contains(err.Error(), "already terminal") {
			return true, nil
		}
		lastErr = err
		if h.taskStore != nil {
			verifyCtx, verifyCancel := newAgentChatPersistenceContext(durableCtx)
			current, found, getErr := h.taskStore.GetRun(verifyCtx, task.ID, runID)
			verifyCancel()
			if getErr == nil && found && types.IsTerminalTaskRunStatus(current.Status) {
				return true, nil
			}
			if getErr != nil {
				lastErr = errors.Join(lastErr, getErr)
			}
		}
		if attempt < maxAttempts-1 {
			timer := time.NewTimer(time.Duration(attempt+1) * 25 * time.Millisecond)
			<-timer.C
		}
	}
	return false, lastErr
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
	_, run, found, err := h.activeHecateChatTaskRun(ctx, session)
	if err != nil {
		// A task-store failure cannot prove that no unlinked backing run is
		// active. Hold admission closed until the runtime can be inspected.
		return true, "unknown"
	}
	return found, run.Status
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
		if chat.MessageTurnKind(session, message) != chat.TurnKindHecateTask {
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

func (h *Handler) waitForHecateAgentRun(ctx context.Context, taskID, runID, sessionID, messageID string) (types.TaskRun, error) {
	ticker := time.NewTicker(hecateAgentPollInterval)
	defer ticker.Stop()
	projector := newTaskRunStreamProjector(h.taskStore)
	var lastStatus string
	var lastActivitySignature string
	var lastContent string
	lastInputProviderInstance := types.ProviderInstanceIdentity{}
	for {
		run, found, err := h.taskStore.GetRun(ctx, taskID, runID)
		if err != nil {
			return types.TaskRun{}, err
		}
		if !found {
			return types.TaskRun{}, fmt.Errorf("task run %q not found", runID)
		}
		if run.InputProviderDisclosedInstance.Valid() && run.InputProviderDisclosedInstance != lastInputProviderInstance {
			if routeErr := h.persistHecateAgentInputRoute(ctx, sessionID, run); routeErr != nil {
				h.logger.WarnContext(ctx, "chat.hecate.input_route_snapshot_update_failed",
					"session_id", sessionID,
					"message_id", run.InputRef,
					"run_id", run.ID,
					"error", routeErr,
				)
			} else {
				lastInputProviderInstance = run.InputProviderDisclosedInstance
			}
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
				message.Context = chatcontext.Normalize(message.Context, chatcontext.MergeRefs(
					chatcontext.ChatMessageRefs(sessionID, message.TurnID, messageID, ""),
					chatcontext.TaskRunRefs(taskID, run.ID, ""),
				))
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

func (h *Handler) persistHecateAgentInputRoute(ctx context.Context, sessionID string, run types.TaskRun) error {
	inputRef := strings.TrimSpace(run.InputRef)
	provider := strings.TrimSpace(run.Provider)
	if inputRef == "" || provider == "" || !run.InputProviderDisclosedInstance.Valid() {
		return nil
	}
	conflict := false
	_, err := h.agentChat.UpdateMessage(ctx, sessionID, inputRef, func(message *chat.Message) {
		if (strings.TrimSpace(message.Provider) != "" && strings.TrimSpace(message.Provider) != provider) ||
			(message.ProviderInstance.Valid() && message.ProviderInstance != run.InputProviderDisclosedInstance) {
			conflict = true
			return
		}
		message.Provider = provider
		message.ProviderInstance = run.InputProviderDisclosedInstance
		message.Model = firstNonEmpty(run.Model, message.Model)
	})
	if err != nil {
		return err
	}
	if conflict {
		return fmt.Errorf("stored rich-input route conflicts with the execution route")
	}
	return nil
}

func (h *Handler) persistHecateAgentInputRouteWithRetry(ctx context.Context, sessionID string, run types.TaskRun) error {
	const attempts = 3
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		persistCtx, cancel := newAgentChatPersistenceContext(ctx)
		err := h.persistHecateAgentInputRoute(persistCtx, sessionID, run)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	return lastErr
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
			message.Provider = firstNonEmpty(run.Provider, message.Provider)
			if run.InputProviderDisclosedInstance.Valid() {
				message.ProviderInstance = run.InputProviderDisclosedInstance
			}
			message.Model = firstNonEmpty(run.Model, message.Model)
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
		return "Hecate Chat turn failed."
	case "cancelled":
		return "Hecate Chat turn cancelled."
	default:
		return "Hecate Chat turn completed."
	}
}
