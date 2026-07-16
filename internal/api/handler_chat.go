package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"mime"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/chatapp"
	"github.com/hecatehq/hecate/internal/chatattachments"
	"github.com/hecatehq/hecate/internal/chatcontext"
	"github.com/hecatehq/hecate/internal/gitrunner"
	"github.com/hecatehq/hecate/internal/modelcaps"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/requestscope"
	"github.com/hecatehq/hecate/internal/taskapp"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

const (
	agentChatTimeout              = 30 * time.Minute
	agentChatPrepareTimeout       = 10 * time.Second
	agentChatConfigOptionTimeout  = 10 * time.Second
	agentChatTerminalWriteTimeout = 10 * time.Second
	agentChatMaxOutputBytes       = 4 * 1024 * 1024
	agentChatMaxImageHistoryBytes = chatapp.MaxMessageAttachmentBytes
)

// newAgentChatPersistenceContext gives already-admitted transcript work a
// short, server-owned window to settle without extending model or ACP
// execution beyond its existing cancellation boundary. Callers create a fresh
// context for each immediate or terminal persistence window; never hold one
// across a long-running turn.
func newAgentChatPersistenceContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), agentChatTerminalWriteTimeout)
}

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
	requestMutationEpoch := h.stateMutationGate.snapshot()
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
	isExternalAgent := agentID != chat.DefaultAgentID
	workspace := strings.TrimSpace(req.Workspace)
	workspaceMode, err := chatapp.NormalizeWorkspaceMode(req.WorkspaceMode)
	if err != nil {
		writeChatAppError(w, err)
		return
	}
	if isExternalAgent && workspaceMode != chat.WorkspaceModeInPlace {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "external-agent sessions require workspace_mode=in_place")
		return
	}
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
		WorkspaceMode:   workspaceMode,
		WorkspaceBranch: workspaceBranch,
		RTKEnabled:      req.RTKEnabled,
		MCPServers:      mcpServers,
	}
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
	releaseChatCreate, err := h.stateMutationGate.beginChatCreate(requestMutationEpoch)
	if err != nil {
		WriteError(w, http.StatusConflict, errCodeSessionCreateConflict, "chat session creation conflicts with project deletion")
		return
	}
	defer releaseChatCreate()
	if projectID != "" {
		if ok, err := h.chatSessionProjectExists(r.Context(), projectID); err != nil {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
			return
		} else if !ok {
			WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
			return
		}
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

func (h *Handler) chatSessionProjectExists(ctx context.Context, projectID string) (bool, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return true, nil
	}
	if h.requiresEmbeddedCairnlineProjectReads() {
		view, err := h.cairnlineEmbeddedProjectWorkView(ctx, projectID)
		if err != nil {
			if errors.Is(err, projects.ErrNotFound) {
				return false, nil
			}
			return false, err
		}
		if view != nil {
			_ = view.Close()
		}
		return true, nil
	}
	if h == nil || h.projects == nil {
		return false, errors.New("project store is not configured")
	}
	_, ok, err := h.projects.Get(ctx, projectID)
	return ok, err
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
		// Re-enter the idempotent application delete so already-missing sessions
		// still reconcile any independently persisted attachment bodies.
		if err := h.chatApplication().DeleteSession(r.Context(), chatapp.DeleteSessionCommand{SessionID: sessionID}); err != nil {
			writeChatSessionDeleteError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	stopping, err := h.deleteExistingChatSession(r.Context(), session)
	if errors.Is(err, errChatSessionDeleteConflict) {
		WriteError(w, http.StatusConflict, errCodeConflict, err.Error())
		return
	}
	if err != nil {
		writeChatSessionDeleteError(w, err)
		return
	}
	if stopping {
		writeChatSessionStopping(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeChatSessionDeleteError(w http.ResponseWriter, err error) {
	if errors.Is(err, chatapp.ErrAttachmentSessionCleanup) {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, chatapp.ErrAttachmentSessionCleanup.Error())
		return
	}
	WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
}

var errChatSessionDeleteConflict = errors.New("chat session delete conflict")

func (h *Handler) deleteExistingChatSession(ctx context.Context, session chat.Session) (bool, error) {
	sessionID := session.ID
	lifecycleClosure := h.agentChatLive.closeSessionLifecycle(sessionID)
	releaseLifecycle := true
	var settlementClaim *agentChatSettlementClaim
	claimNeedsRelease := false
	defer func() {
		if claimNeedsRelease {
			settlementClaim.releaseLifecycleAfterRelinquish(lifecycleClosure)
			return
		}
		if releaseLifecycle {
			lifecycleClosure.release()
		}
	}()

	operationCtx, operationCancel := context.WithTimeout(ctx, 3*time.Second)
	operationsDrained := lifecycleClosure.waitForOperations(operationCtx)
	operationCancel()
	if !operationsDrained {
		return true, nil
	}
	latest, ok, err := h.agentChat.Get(ctx, sessionID)
	if err != nil {
		return false, err
	}
	if !ok {
		// A serialized delete that won first already removed the transcript.
		// Re-enter idempotent cleanup without dispatching through stale task or
		// native-session fields captured before this lifecycle closure.
		if err := h.chatApplication().DeleteSession(ctx, chatapp.DeleteSessionCommand{
			SessionID:    sessionID,
			DeleteNative: isExternalChatSession(session),
		}); err != nil {
			return false, err
		}
		return false, nil
	}
	session = latest
	if isHecateChatSession(session) {
		if _, _, err := h.cancelHecateChatTaskRun(ctx, session); err != nil {
			return false, fmt.Errorf("%w: %v", errChatSessionDeleteConflict, err)
		}
	}
	settlementClaim = h.agentChatSettlements.claimSession(sessionID, lifecycleClosure)
	claimNeedsRelease = true
	cancelCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	settled := h.agentChatLive.cancelRunAndWait(cancelCtx, sessionID)
	cancel()
	if !settled {
		return true, nil
	}
	var originSettlement *taskapp.OriginRunSettlement
	if isHecateChatSession(session) {
		_, settlement, err := h.taskApplication().CancelNonTerminalRunsByOrigin(ctx, taskapp.CancelRunsByOriginCommand{
			OriginKind: "chat",
			OriginID:   sessionID,
			Reason:     "operator",
		})
		originSettlement = settlement
		if originSettlement != nil {
			defer originSettlement.Release()
		}
		if err != nil {
			return false, fmt.Errorf("%w: cancel task runs owned by chat %q: %v", errChatSessionDeleteConflict, sessionID, err)
		}
	}
	if isExternalChatSession(session) && h.agentChatRunner != nil {
		// Close while the destructive owner is installed. ACP terminal finals
		// enqueue under that owner even though closeSessionLifecycle already
		// advanced the ordinary admission epoch.
		_ = h.agentChatRunner.DeleteSession(ctx, sessionID)
	}
	drainCtx, drainCancel := context.WithTimeout(ctx, 3*time.Second)
	drained := settlementClaim.sealAndDrain(drainCtx)
	drainCancel()
	if !drained {
		settlementClaim.releaseLifecycleAfterDrain(lifecycleClosure)
		claimNeedsRelease = false
		releaseLifecycle = false
		return true, nil
	}
	claimNeedsRelease = false
	if err := h.chatApplication().DeleteSession(ctx, chatapp.DeleteSessionCommand{
		SessionID:    session.ID,
		DeleteNative: false,
	}); err != nil {
		return false, err
	}
	if originSettlement != nil {
		originSettlement.Commit()
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
		// The task cancellation and the chat watcher are separate ownership
		// boundaries. Stamp the operator reason and wake the detached watcher so
		// its terminal metric cannot misclassify this as a request disconnect.
		h.agentChatLive.cancelRun(session.ID)
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
	lifecycleClosure := h.agentChatLive.closeSessionLifecycle(session.ID)
	releaseLifecycle := true
	var settlementClaim *agentChatSettlementClaim
	claimNeedsRelease := false
	defer func() {
		if claimNeedsRelease {
			settlementClaim.releaseLifecycleAfterRelinquish(lifecycleClosure)
			return
		}
		if releaseLifecycle {
			lifecycleClosure.release()
		}
	}()

	cancelCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	if !lifecycleClosure.waitForOperations(cancelCtx) {
		cancel()
		writeChatSessionStopping(w)
		return
	}
	settlementClaim = h.agentChatSettlements.claimSession(session.ID, lifecycleClosure)
	claimNeedsRelease = true
	settled := h.agentChatLive.cancelRunAndWait(cancelCtx, session.ID)
	cancel()
	if !settled {
		writeChatSessionStopping(w)
		return
	}
	latest, ok, err := h.agentChat.Get(r.Context(), session.ID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "agent chat session not found")
		return
	}
	session = latest
	if h.agentChatRunner != nil {
		_ = h.agentChatRunner.CloseSession(r.Context(), session.ID)
	}
	drainCtx, drainCancel := context.WithTimeout(r.Context(), 3*time.Second)
	drained := settlementClaim.sealAndDrain(drainCtx)
	drainCancel()
	if !drained {
		settlementClaim.releaseLifecycleAfterDrain(lifecycleClosure)
		claimNeedsRelease = false
		releaseLifecycle = false
		writeChatSessionStopping(w)
		return
	}
	claimNeedsRelease = false
	result, err := h.chatApplication().CloseNativeSession(r.Context(), chatapp.CloseNativeSessionCommand{
		Session:             session,
		NativeAlreadyClosed: true,
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
	lifecycle := h.agentChatLive.snapshotLifecycle(sessionID)
	defer lifecycle.release()
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
	releaseOperation, accepted := h.agentChatLive.beginLifecycleOperation(lifecycle)
	if !accepted {
		writeChatSessionStopping(w)
		return
	}
	defer releaseOperation()
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
	lifecycle := h.agentChatLive.snapshotLifecycle(sessionID)
	defer lifecycle.release()
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
	settingsCtx := r.Context()
	if req.WorkspaceMode != nil || req.RTKEnabled != nil {
		releaseMutation, admission := h.agentChatLive.beginExclusiveMutation(lifecycle)
		switch admission {
		case agentChatRunAdmissionClosed:
			writeChatSessionStopping(w)
			return
		case agentChatRunBusy:
			WriteErrorDetails(w, http.StatusConflict, errCodeAgentSessionBusy, "chat session is busy", ErrorDetails{
				UserMessage:    "This chat is busy.",
				OperatorAction: "Wait for the active turn to finish before changing chat settings.",
			})
			return
		}
		defer releaseMutation()

		// Winning exclusive admission only proves no turn can start now. Reload
		// after admission so a task linked by the previous winner cannot be
		// missed by the immutable-workspace-mode check.
		session, ok, err = h.agentChat.Get(settingsCtx, sessionID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
			return
		}
		if !ok {
			WriteError(w, http.StatusNotFound, errCodeNotFound, "agent chat session not found")
			return
		}
		// A task is created before the transcript link is committed. If that
		// atomic link ever exhausts its retries, the task's durable chat origin
		// remains the fail-closed authority for the immutable mode boundary.
		if req.WorkspaceMode != nil && strings.TrimSpace(session.TaskID) == "" {
			originTask, taskExists, taskErr := h.hecateChatOriginTask(settingsCtx, session.ID)
			if taskErr != nil {
				h.logger.ErrorContext(context.WithoutCancel(settingsCtx), "chat.hecate.origin_task_lookup_failed", "session_id", session.ID, "error", taskErr)
				WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "failed to verify chat task linkage")
				return
			}
			if taskExists {
				session.TaskID = originTask.ID
			}
		}
	}
	result, err := h.chatApplication().SetHecateSettings(settingsCtx, chatapp.SetHecateSettingsCommand{
		Session:       session,
		RTKEnabled:    req.RTKEnabled,
		WorkspaceMode: req.WorkspaceMode,
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

func (h *Handler) hecateChatOriginTask(ctx context.Context, sessionID string) (types.Task, bool, error) {
	if h.taskStore == nil || strings.TrimSpace(sessionID) == "" {
		return types.Task{}, false, nil
	}
	tasks, err := h.taskStore.ListTasks(ctx, taskstate.TaskFilter{})
	if err != nil {
		return types.Task{}, false, fmt.Errorf("list chat-origin tasks: %w", err)
	}
	for _, task := range tasks {
		if task.OriginKind == "chat" && task.OriginID == sessionID {
			return task, true, nil
		}
	}
	return types.Task{}, false, nil
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
	if !isHecateChatSession(session) || h.taskStore == nil || h.taskRunner == nil {
		return types.TaskRun{}, false, nil
	}
	task, activeRun, found, err := h.activeHecateChatTaskRun(ctx, session)
	if err != nil {
		return types.TaskRun{}, false, err
	}
	if !found {
		return types.TaskRun{}, false, nil
	}
	run, err := h.taskRunner.CancelRun(ctx, task, activeRun.ID, "operator")
	if err != nil {
		if strings.Contains(err.Error(), "already terminal") {
			return types.TaskRun{}, false, nil
		}
		return types.TaskRun{}, true, err
	}
	return run, true, nil
}

func (h *Handler) activeHecateChatTaskRun(ctx context.Context, session chat.Session) (types.Task, types.TaskRun, bool, error) {
	if h.taskStore == nil || strings.TrimSpace(session.ID) == "" {
		return types.Task{}, types.TaskRun{}, false, nil
	}
	taskID := strings.TrimSpace(session.TaskID)
	runID := strings.TrimSpace(session.LatestRunID)
	if taskID != "" && runID != "" {
		task, found, err := h.taskStore.GetTask(ctx, taskID)
		if err != nil {
			return types.Task{}, types.TaskRun{}, false, err
		}
		if found {
			run, runFound, err := h.taskStore.GetRun(ctx, taskID, runID)
			if err != nil {
				return types.Task{}, types.TaskRun{}, false, err
			}
			if runFound && !types.IsTerminalTaskRunStatus(run.Status) {
				return task, run, true, nil
			}
		}
	}

	// A task exists before the atomic chat link commits. Keep it authoritative
	// for admission and cancellation when the first or a later segment leaves
	// the transcript pointing at no task or an older terminal task.
	tasks, err := h.taskStore.ListTasks(ctx, taskstate.TaskFilter{})
	if err != nil {
		return types.Task{}, types.TaskRun{}, false, err
	}
	for _, candidate := range tasks {
		candidateRunID := strings.TrimSpace(candidate.LatestRunID)
		if candidate.OriginKind != "chat" || candidate.OriginID != session.ID || candidateRunID == "" {
			continue
		}
		candidateRun, found, err := h.taskStore.GetRun(ctx, candidate.ID, candidateRunID)
		if err != nil {
			return types.Task{}, types.TaskRun{}, false, err
		}
		if found && !types.IsTerminalTaskRunStatus(candidateRun.Status) {
			return candidate, candidateRun, true, nil
		}
	}
	return types.Task{}, types.TaskRun{}, false, nil
}

func (h *Handler) HandleCreateChatMessage(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("id"))
	lifecycle := h.agentChatLive.snapshotLifecycle(sessionID)
	defer lifecycle.release()
	session, ok, err := h.agentChat.Get(r.Context(), sessionID)
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
	requestGuard, requestResult, err := beginChatMessageRequest(r.Context(), h.chatApplication(), session.ID, req)
	if err != nil {
		if writeChatMessageRequestError(w, err) {
			return
		}
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "failed to reserve chat message request")
		return
	}
	if requestResult != nil {
		session = requestResult.Session
		if requestResult.Replay {
			if len(req.AttachmentIDs) > 0 {
				releaseOperation, accepted := h.agentChatLive.beginLifecycleOperation(lifecycle)
				if !accepted {
					writeChatSessionStopping(w)
					return
				}
				defer releaseOperation()
				repairCtx, repairCancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
				repairErr := h.chatApplication().ResolveAttachmentClaim(repairCtx, chatattachments.ClaimRef{
					SessionID:     session.ID,
					MessageID:     requestResult.CommittedMessageID,
					AttachmentIDs: req.AttachmentIDs,
				}, chatattachments.ClaimLinked)
				repairCancel()
				if repairErr != nil {
					WriteError(w, http.StatusInternalServerError, errCodeGatewayError, chatAttachmentFinalizeFailureMessage)
					return
				}
			}
			WriteJSON(w, http.StatusOK, ChatSessionResponse{
				Object:         "chat_session",
				Data:           renderChatSession(session, h.agentChatSnapshotConfig()),
				MessageRequest: requestGuard.responseMetadata(true, requestResult.CommittedMessageID),
			})
			return
		}
	}
	defer func() {
		if releaseErr := requestGuard.release(r.Context()); releaseErr != nil {
			h.logger.WarnContext(context.WithoutCancel(r.Context()), "chat.message_request_release_failed", "session_id", session.ID, "error", releaseErr)
		}
	}()
	limits := h.agentChatSnapshotConfig()
	admission, err := h.chatApplication().AdmitMessage(chatapp.AdmitMessageCommand{
		Session:         session,
		Content:         req.Content,
		ExecutionMode:   req.ExecutionMode,
		ToolsEnabled:    req.ToolsEnabled,
		AttachmentCount: len(req.AttachmentIDs),
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
		h.handleCreateHecateChatMessage(w, r, session, req, plan.ToolsEnabled, lifecycle, requestGuard)
		return
	case chatapp.MessageDispatchExternalAgent:
		h.handleCreateExternalAgentChatMessage(w, r, session, req, plan, lifecycle, requestGuard)
		return
	}
	writeChatExecutionModeInvalid(w)
}

func (h *Handler) handleCreateExternalAgentChatMessage(w http.ResponseWriter, r *http.Request, session chat.Session, req CreateChatMessageRequest, plan chatapp.MessageDispatchPlan, lifecycle agentChatLifecycleSnapshot, requestGuard *chatMessageRequestGuard) {
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
	switch h.agentChatLive.registerRun(lifecycle, cancel) {
	case agentChatRunAdmissionClosed:
		cancel()
		writeChatSessionStopping(w)
		return
	case agentChatRunBusy:
		cancel()
		WriteErrorDetails(w, http.StatusConflict, errCodeAgentSessionBusy, "agent chat session is already running", ErrorDetails{
			UserMessage:    "This chat is already running.",
			OperatorAction: "Wait for the active run to finish or stop it before sending another message.",
		})
		return
	}
	defer h.agentChatLive.clearRun(session.ID)
	defer cancel()
	settlementTurn, settlementErr := h.agentChatSettlements.acquireTurn(h, session.ID, assistantID)
	if settlementErr != nil {
		if r.Context().Err() == nil {
			WriteError(w, http.StatusServiceUnavailable, errCodeGatewayError, settlementErr.Error())
		}
		return
	}
	defer settlementTurn.finish()
	workspaceLease, admitted := h.acquireWorkspaceWriter(w, runCtx, session.Workspace)
	if !admitted {
		return
	}
	defer workspaceLease.Release()
	externalFileTurnPermitHeld := false
	if len(req.AttachmentIDs) > 0 {
		if h.chatExternalFileTurnAdmission == nil || !h.chatExternalFileTurnAdmission.TryAcquire() {
			writeChatExternalFileTurnBusy(w)
			return
		}
		externalFileTurnPermitHeld = true
		defer func() {
			if externalFileTurnPermitHeld {
				h.chatExternalFileTurnAdmission.Release()
			}
		}()
	}

	userID := newChatID("msg")
	claimRef := chatattachments.ClaimRef{
		SessionID:     session.ID,
		MessageID:     userID,
		AttachmentIDs: req.AttachmentIDs,
	}
	var resolvedAttachments []chatattachments.StoredAttachment
	var err error
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
		for _, attachment := range resolvedAttachments {
			if err := validateStoredChatAttachment(attachment); err != nil {
				WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "stored chat attachment failed integrity validation")
				return
			}
		}
	}
	appendAttempted = true
	updated, err := requestGuard.appendUserMessage(r.Context(), session.ID, chat.Message{
		ID:            userID,
		ExecutionMode: chat.ExecutionModeExternalAgent,
		Role:          "user",
		Content:       plan.Content,
		Attachments:   chatMessageAttachments(resolvedAttachments),
		CreatedAt:     time.Now().UTC(),
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
	startedAt := time.Now().UTC()
	trace.Record(telemetry.EventAgentChatRunStarted, agentChatTraceAttrs(session, adapter, runID, assistantID, map[string]any{
		telemetry.AttrHecateRunStatus: "running",
	}))
	initialWriteCtx, initialWriteCancel := newAgentChatPersistenceContext(r.Context())
	contextPacket := h.externalAgentContextPacket(initialWriteCtx, session, adapter.Name)
	contextPacket.ID = newChatID("ctx")
	contextPacket = chatcontext.Normalize(contextPacket, chatcontext.MergeRefs(
		chatcontext.ChatMessageRefs(session.ID, assistantID, session.ProjectID),
		chatcontext.TaskRunRefs("", runID, session.ProjectID),
	))
	updated, err = h.agentChat.AppendMessage(initialWriteCtx, session.ID, chat.Message{
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
	initialWriteCancel()
	if err != nil {
		if r.Context().Err() == nil {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		}
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
	// Timer-driven flushes remain request-bound while ACP execution is active.
	// After Run returns, closeWithFlush explicitly switches the last buffered
	// batch to the fresh bounded terminal context; neither path extends ACP
	// execution beyond runCtx.
	streamFlush := func(writeCtx context.Context, display string, haveContent bool, activities []agentadapters.Activity) {
		_, updateErr := settlementTurn.updateMessage(writeCtx, true, func(message *chat.Message) {
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
		if updateErr != nil && writeCtx.Err() == nil {
			h.logger.WarnContext(writeCtx, "chat.external_agent.stream_update_failed",
				"session_id", session.ID,
				"message_id", assistantID,
				"error", updateErr,
			)
		}
	}
	// ACP terminals can outlive the prompt turn that created them. Give each
	// terminal an immutable, message-owned persistence sink instead of routing
	// its eventual exit through the stream coalescer (which deliberately closes
	// when Run returns) or whichever session turn happens to be current later.
	terminalActivitySink := func(activity agentadapters.Activity) {
		settlementTurn.terminalActivity(activity)
	}
	// Coalesce the per-token OnOutput/OnActivity callbacks into at most
	// one persist+publish per window. close() after Run flushes the
	// trailing batch so buffered activities land before the finalize.
	streamCoalescer := newChatStreamCoalescer(agentChatStreamCoalesceInterval, func(display string, haveContent bool, activities []agentadapters.Activity) {
		streamFlush(r.Context(), display, haveContent, activities)
	})
	result, runErr := runner.Run(runCtx, agentadapters.RunRequest{
		SessionID:               session.ID,
		AdapterID:               adapter.ID,
		Workspace:               session.Workspace,
		PreviousNativeSessionID: session.NativeSessionID,
		ConfigOptions:           session.ConfigOptions,
		MCPServers:              session.MCPServers,
		Prompt:                  chatapp.BuildExternalPromptInput(plan.Content, resolvedAttachments),
		Timeout:                 agentChatTimeout,
		MaxOutputBytes:          agentChatMaxOutputBytes,
		OnOutput: func(display string) {
			streamCoalescer.output(display)
		},
		OnActivity: func(activity agentadapters.Activity) {
			streamCoalescer.activity(activity)
		},
		OnTerminalActivity: terminalActivitySink,
		OnTerminalClosed:   settlementTurn.terminalClosed,
	})
	// Drop Hecate's hydrated body references before another External file turn
	// is admitted. The synchronous runner owns any private copies it retains.
	resolvedAttachments = nil
	if externalFileTurnPermitHeld {
		h.chatExternalFileTurnAdmission.Release()
		externalFileTurnPermitHeld = false
	}
	terminalCtx, terminalCancel := newAgentChatPersistenceContext(r.Context())
	defer terminalCancel()
	streamCoalescer.closeWithFlush(func(display string, haveContent bool, activities []agentadapters.Activity) {
		streamFlush(terminalCtx, display, haveContent, activities)
	})
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

	updated, err = settlementTurn.updateMessage(terminalCtx, false, func(message *chat.Message) {
		if output != "" {
			message.Content = output
		}
		message.RawOutput = result.RawOutput
		if strings.TrimSpace(message.RawOutput) == "" && runErr != nil {
			message.RawOutput = runErr.Error()
		}
		message.DriverKind = result.DriverKind
		message.NativeSessionID = result.NativeSessionID
		message.AgentInfo = result.AgentInfo
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
		h.logger.ErrorContext(terminalCtx, "chat.external_agent.assistant_terminal_update_failed",
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
	if result.DriverKind != "" || result.NativeSessionID != "" || result.AgentInfo != nil || result.ConfigOptions != nil || result.AvailableCommandsKnown {
		updated, err = settlementTurn.updateSession(terminalCtx, func(item *chat.Session) {
			if result.DriverKind != "" {
				item.DriverKind = result.DriverKind
			}
			if result.NativeSessionID != "" {
				item.NativeSessionID = result.NativeSessionID
			}
			if result.AgentInfo != nil {
				item.AgentInfo = result.AgentInfo
			}
			if result.ConfigOptions != nil {
				item.ConfigOptions = result.ConfigOptions
			}
			if result.AvailableCommandsKnown {
				item.AvailableCommands = result.AvailableCommands
			}
		})
		if err != nil {
			if r.Context().Err() == nil {
				WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
			}
			return
		}
	}
	// Increment after every completed round-trip, even when no ceiling is set.
	// Best-effort: the turn result itself was already committed above.
	if inc, incErr := settlementTurn.updateSession(terminalCtx, func(item *chat.Session) {
		item.TurnsUsed++
	}); incErr == nil {
		updated = inc
	} else {
		h.logger.WarnContext(terminalCtx, "chat.turn_counter_increment_failed", "session_id", session.ID, "error", incErr)
	}
	h.reconcileProjectAssignmentsForChat(terminalCtx, updated)
	if current, publishErr := settlementTurn.currentSession(terminalCtx, true); publishErr == nil {
		updated = current
	} else if terminalCtx.Err() == nil {
		h.logger.WarnContext(terminalCtx, "chat.external_agent.final_publish_failed", "session_id", session.ID, "error", publishErr)
	}
	if r.Context().Err() != nil {
		return
	}
	WriteJSON(w, http.StatusOK, ChatSessionResponse{
		Object:         "chat_session",
		Data:           renderChatSession(updated, h.agentChatSnapshotConfig()),
		MessageRequest: requestGuard.responseMetadata(false, ""),
	})
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
func (h *Handler) handleDirectModelTurn(w http.ResponseWriter, r *http.Request, session chat.Session, req CreateChatMessageRequest, lifecycle agentChatLifecycleSnapshot, requestGuard *chatMessageRequestGuard) {
	if busy, runStatus := h.hecateAgentSessionBusy(r.Context(), session); busy {
		writeHecateAgentBusy(w, session, runStatus)
		return
	}

	requestedProvider := strings.TrimSpace(req.Provider)
	if requestedProvider == "" {
		requestedProvider = session.Provider
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = session.Model
	}
	if model == "" {
		writeAgentChatModelRequired(w, "model")
		return
	}
	// Routing accepts stable control-plane ids and preset aliases, but direct
	// turn snapshots and every subsequent admission check use the configured
	// runtime name. Canonicalize first so a live alias reload cannot split
	// capability admission, history hydration, and dispatch across providers.
	provider := requestedProvider
	historicalProvider := ""
	providerInstance := types.ProviderInstanceIdentity{}
	if requestedProvider != "" {
		resolvedRoute, resolveErr := h.modelApplication().ResolveProviderRoute(r.Context(), requestedProvider, model)
		if resolveErr != nil {
			writeAgentChatModelResolutionError(w, resolveErr)
			return
		}
		if resolvedRoute.Name != "" {
			provider = resolvedRoute.Name
			historicalProvider = resolvedRoute.Name
			providerInstance = resolvedRoute.Instance
		}
	}
	caps, err := h.resolveModelCapabilities(r.Context(), provider, model)
	if err != nil {
		writeAgentChatModelResolutionError(w, err)
		return
	}
	imageCapable := false
	if len(req.AttachmentIDs) > 0 || chatSessionHasAttachments(session) {
		imageCapable, err = h.modelApplication().SupportsImageInput(r.Context(), provider, model)
		if err != nil {
			writeAgentChatModelResolutionError(w, err)
			return
		}
	}
	if len(req.AttachmentIDs) > 0 && !imageCapable {
		writeAgentChatImageCapabilityRequired(w)
		return
	}
	imageTurnPermitHeld := false
	if directModelTurnMayUseImageBodies(session, req.AttachmentIDs, imageCapable, historicalProvider, providerInstance) {
		if h.chatImageTurnAdmission == nil || !h.chatImageTurnAdmission.TryAcquire() {
			writeChatImageTurnBusy(w)
			return
		}
		imageTurnPermitHeld = true
		defer func() {
			if imageTurnPermitHeld {
				h.chatImageTurnAdmission.Release()
			}
		}()
	}
	userID := newChatID("msg")
	assistantID := newChatID("msg")
	runID := newChatID("model_run")
	startedAt := time.Now().UTC()
	runCtx, cancel := context.WithTimeout(r.Context(), agentChatTimeout)
	switch h.agentChatLive.registerRun(lifecycle, cancel) {
	case agentChatRunAdmissionClosed:
		cancel()
		writeChatSessionStopping(w)
		return
	case agentChatRunBusy:
		cancel()
		WriteErrorDetails(w, http.StatusConflict, errCodeAgentSessionBusy, "chat session is already running", ErrorDetails{
			UserMessage:    "This chat is already running.",
			OperatorAction: "Wait for the active run to finish or stop it before sending another message.",
		})
		return
	}
	defer h.agentChatLive.clearRun(session.ID)
	defer cancel()

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
		}
		attachmentClaimPending = true
		defer func() {
			if !attachmentClaimPending || appendAttempted {
				return
			}
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
			defer cancel()
			if err := h.chatApplication().ResolveAttachmentClaim(cleanupCtx, claimRef, chatattachments.ClaimReleased); err != nil {
				h.logger.WarnContext(cleanupCtx, "chat.attachment_claim_release_failed",
					"session_id", session.ID,
					"message_id", userID,
					"error", err,
				)
			}
		}()
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
	effectiveSystemPrompt := h.hecateChatEffectiveSystemPrompt(r.Context(), session, req.SystemPrompt)
	history, requiresImageInput, err := h.agentChatModelHistoryWithAttachments(
		r.Context(), session, effectiveSystemPrompt, content, resolvedAttachments, imageCapable, historicalProvider, providerInstance,
	)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "failed to prepare chat image context")
		return
	}
	segmentID := modelSegmentID(session, provider, model)
	persistedUserProviderInstance := providerInstance
	if len(resolvedAttachments) > 0 {
		// Historical image hydration treats the user row's provider instance as
		// evidence of the disclosure boundary. Admission alone is not enough: an
		// assistant-row write, governor, route, or final dispatch fence can still
		// fail before any provider call. Stamp the attempted instance only after
		// the gateway returns call metadata below.
		persistedUserProviderInstance = types.ProviderInstanceIdentity{}
	}
	appendAttempted = true
	updated, err := requestGuard.appendUserMessage(r.Context(), session.ID, chat.Message{
		ID:            userID,
		ExecutionMode: chat.ExecutionModeHecateTask,
		// The model-chat handler dispatches when the operator submitted
		// with tools off (or when the runtime downgraded a hecate_task
		// turn because the model can't run tools). Either way, the
		// persisted Message records ToolsEnabled=false so a future
		// read against this row recovers the original intent without
		// having to parse the execution_mode string.
		ToolsEnabled:     false,
		SegmentID:        segmentID,
		Provider:         provider,
		ProviderInstance: persistedUserProviderInstance,
		Model:            model,
		Capabilities:     caps,
		Role:             "user",
		Content:          content,
		Attachments:      chatMessageAttachments(resolvedAttachments),
		CreatedAt:        startedAt,
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
	// Once the transcript owns the immutable metadata, a failed lifecycle
	// finalize must fail closed with the body retained rather than making the
	// image deletable again.
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

	initialWriteCtx, initialWriteCancel := newAgentChatPersistenceContext(r.Context())
	contextPacket := h.directModelContextPacket(initialWriteCtx, session, provider, model, effectiveSystemPrompt)
	contextPacket.ID = newChatID("ctx")
	contextPacket = chatcontext.Normalize(contextPacket, chatcontext.MergeRefs(
		chatcontext.ChatMessageRefs(session.ID, assistantID, session.ProjectID),
		chatcontext.TaskRunRefs("", runID, session.ProjectID),
	))
	updated, err = h.agentChat.AppendMessage(initialWriteCtx, session.ID, chat.Message{
		ID:               assistantID,
		ExecutionMode:    chat.ExecutionModeHecateTask,
		ToolsEnabled:     false,
		SegmentID:        segmentID,
		RunID:            runID,
		RequestID:        RequestIDFromContext(r.Context()),
		Provider:         provider,
		ProviderInstance: providerInstance,
		Model:            model,
		Capabilities:     caps,
		Role:             "assistant",
		Content:          "",
		Status:           "running",
		CostMode:         "hecate",
		Workspace:        session.Workspace,
		Context:          contextPacket,
		CreatedAt:        startedAt,
		StartedAt:        startedAt,
		Activities: []chat.Activity{
			newChatActivity("model_request", "running", "Model request", "Waiting for provider response"),
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

	routeProvider := requestedProvider
	if provider != "" {
		// Admission used this canonical provider boundary. Pin dispatch to the
		// same identity so a concurrent runtime reload cannot reassign the
		// requested alias; this is mandatory once image bytes exist and keeps
		// ordinary direct turns consistent with their durable route snapshot.
		routeProvider = provider
	}
	requestProviderInstance := types.ProviderInstanceIdentity{}
	if requiresImageInput {
		requestProviderInstance = providerInstance
	}
	chatReq := types.ChatRequest{
		RequestID: RequestIDFromContext(r.Context()),
		Model:     model,
		Messages:  history,
		Requirements: types.ChatRequestRequirements{
			ImageInput:         requiresImageInput,
			NoProviderFailover: requiresImageInput,
			ExactProvider:      requiresImageInput && routeProvider != "",
			ProviderInstance:   requestProviderInstance,
		},
		Scope: requestscope.Build(routeProvider),
	}
	result, runErr := h.service.HandleChat(runCtx, chatReq)
	// Drop the handler's references to both binary and expanded image bodies
	// before admitting another image turn. Providers own any copies they retain
	// after their synchronous call returns.
	chatReq.Messages = nil
	history = nil
	resolvedAttachments = nil
	if imageTurnPermitHeld {
		h.chatImageTurnAdmission.Release()
		imageTurnPermitHeld = false
	}
	completedAt := time.Now().UTC()
	outcome := newDirectModelTurnOutcome(result, runErr, runCtx.Err())
	status := outcome.Status
	output := outcome.Output
	errorText := outcome.ErrorText
	// Provider execution remains tied to the request through runCtx, but once it
	// returns the transcript's terminal state is authoritative. Give those
	// bounded writes enough time to finish even when the browser disconnects and
	// cancels r.Context(), otherwise the durable assistant row can remain running.
	terminalCtx, terminalCancel := newAgentChatPersistenceContext(r.Context())
	defer terminalCancel()
	routeSnapshot := h.directModelResultRouteSnapshot(terminalCtx, provider, providerInstance, model, caps, result)
	if result != nil && (routeSnapshot.Provider != provider || routeSnapshot.ProviderInstance != persistedUserProviderInstance || routeSnapshot.Model != model || routeSnapshot.Capabilities != caps) {
		_, routeUpdateErr := h.agentChat.UpdateMessage(terminalCtx, session.ID, userID, func(message *chat.Message) {
			message.Provider = routeSnapshot.Provider
			message.ProviderInstance = routeSnapshot.ProviderInstance
			message.Model = routeSnapshot.Model
			message.Capabilities = routeSnapshot.Capabilities
		})
		if routeUpdateErr != nil {
			// The assistant terminal row is authoritative for run completion. A
			// stale user-route annotation must not strand that row in "running".
			h.logger.WarnContext(terminalCtx, "chat.direct_model.user_route_snapshot_update_failed",
				"session_id", session.ID,
				"message_id", userID,
				"error", routeUpdateErr,
			)
		}
	}
	updated, err = h.agentChat.UpdateMessage(terminalCtx, session.ID, assistantID, func(message *chat.Message) {
		message.Status = status
		message.Content = output
		message.Error = errorText
		message.StartedAt = startedAt
		message.CompletedAt = completedAt
		message.Provider = routeSnapshot.Provider
		message.ProviderInstance = routeSnapshot.ProviderInstance
		message.Model = routeSnapshot.Model
		message.Capabilities = routeSnapshot.Capabilities
		if result != nil {
			message.TraceID = result.Metadata.TraceID
			message.SpanID = result.Metadata.SpanID
			message.Usage = chat.Usage{
				ContextUsed: result.Metadata.TotalTokens,
			}
		}
		message.Context.Provider = routeSnapshot.Provider
		message.Context.Model = routeSnapshot.Model
		message.Context = chatcontext.Normalize(message.Context, chatcontext.MergeRefs(
			chatcontext.ChatMessageRefs(session.ID, assistantID, session.ProjectID),
			chatcontext.TaskRunRefs("", message.RunID, session.ProjectID),
		))
		message.Activities = append(message.Activities, newChatActivity(status, status, finalChatActivityTitle(status), errorText))
	})
	if err != nil {
		h.logger.ErrorContext(terminalCtx, "chat.direct_model.assistant_terminal_update_failed",
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
	if inc, incErr := h.agentChat.UpdateSession(terminalCtx, session.ID, func(item *chat.Session) {
		item.Provider = routeSnapshot.Provider
		item.Model = routeSnapshot.Model
		item.Capabilities = routeSnapshot.Capabilities
		item.TurnsUsed++
	}); incErr == nil {
		updated = inc
	} else {
		h.logger.WarnContext(terminalCtx, "chat.turn_snapshot_update_failed", "session_id", session.ID, "error", incErr)
	}
	h.agentChatLive.publishSession(updated)
	// A disconnected caller has nowhere to receive the terminal snapshot. Live
	// subscribers were notified above and a later GET reads the durable state.
	if r.Context().Err() != nil {
		return
	}
	WriteJSON(w, http.StatusOK, ChatSessionResponse{
		Object:         "chat_session",
		Data:           renderChatSession(updated, h.agentChatSnapshotConfig()),
		MessageRequest: requestGuard.responseMetadata(false, ""),
	})
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

func (h *Handler) agentChatModelHistoryWithAttachments(
	ctx context.Context,
	session chat.Session,
	systemPrompt string,
	content string,
	current []chatattachments.StoredAttachment,
	includeHistoricalImages bool,
	historicalProvider string,
	historicalProviderInstance types.ProviderInstanceIdentity,
) ([]types.Message, bool, error) {
	if len(current) == 0 && !chatSessionHasAttachments(session) {
		return agentChatModelHistory(session, systemPrompt, content), false, nil
	}

	selected := make(map[string]struct{})
	omissionReasons := make(map[string]string)
	remaining := agentChatMaxImageHistoryBytes
	for _, attachment := range current {
		if err := validateStoredChatImageAttachment(attachment); err != nil {
			return nil, false, err
		}
		remaining -= int64(len(attachment.Data))
	}
	if remaining < 0 {
		return nil, false, errors.New("current image attachments exceed the model history limit")
	}

	skipThroughIndex := compactedTranscriptMessageIndex(session.Messages, session.ContextSummary.ThroughMessageID)
	historicalProvider = strings.TrimSpace(historicalProvider)
	for i := len(session.Messages) - 1; i > skipThroughIndex; i-- {
		message := session.Messages[i]
		if message.Role != "user" {
			continue
		}
		for j := len(message.Attachments) - 1; j >= 0; j-- {
			attachment := message.Attachments[j]
			key := chatAttachmentSelectionKey(message.ID, attachment.ID)
			switch {
			case !includeHistoricalImages:
				omissionReasons[key] = "the active route does not support image input"
			case historicalProvider == "" || !historicalProviderInstance.Valid() || strings.TrimSpace(message.Provider) != historicalProvider || message.ProviderInstance != historicalProviderInstance:
				omissionReasons[key] = "the active provider differs from the route that previously received it"
			case attachment.SizeBytes <= 0 || attachment.SizeBytes > remaining:
				omissionReasons[key] = fmt.Sprintf("the %d MiB image-history limit was reached", agentChatMaxImageHistoryBytes>>20)
			default:
				selected[key] = struct{}{}
				remaining -= attachment.SizeBytes
			}
		}
	}

	messages := make([]types.Message, 0, len(session.Messages)+2)
	if systemPrompt = strings.TrimSpace(systemPrompt); systemPrompt != "" {
		messages = append(messages, types.Message{Role: "system", Content: systemPrompt})
	}
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
		if message.Role != "user" || len(message.Attachments) == 0 {
			if text != "" {
				messages = append(messages, types.Message{Role: message.Role, Content: text})
			}
			continue
		}

		stored := make([]chatattachments.StoredAttachment, 0, len(message.Attachments))
		omitted := make([]string, 0, len(message.Attachments))
		for _, attachment := range message.Attachments {
			key := chatAttachmentSelectionKey(message.ID, attachment.ID)
			if _, ok := selected[key]; !ok {
				reason := omissionReasons[key]
				if reason == "" {
					reason = "it was not selected by the image-history safety policy"
				}
				omitted = append(omitted, reason)
				continue
			}
			item, err := h.chatApplication().GetAttachment(ctx, chatapp.AttachmentCommand{
				SessionID:    session.ID,
				AttachmentID: attachment.ID,
			})
			if err != nil {
				return nil, false, err
			}
			if err := validateStoredChatAttachmentTranscript(session.ID, attachment, item); err != nil {
				delete(selected, key)
				omitted = append(omitted, err.Error())
				continue
			}
			if err := validateStoredChatImageAttachment(item); err != nil {
				return nil, false, err
			}
			stored = append(stored, item)
		}
		messages = append(messages, chatModelMessageWithAttachments(text, stored, omitted))
	}
	messages = append(messages, chatModelMessageWithAttachments(content, current, nil))
	return messages, len(current) > 0 || len(selected) > 0, nil
}

func chatSessionHasAttachments(session chat.Session) bool {
	for _, message := range session.Messages {
		if len(message.Attachments) > 0 {
			return true
		}
	}
	return false
}

func chatAttachmentSelectionKey(messageID, attachmentID string) string {
	return messageID + "\x00" + attachmentID
}

func chatModelMessageWithAttachments(text string, attachments []chatattachments.StoredAttachment, omissionReasons []string) types.Message {
	text = strings.TrimSpace(text)
	if len(omissionReasons) > 0 {
		markers := make([]string, 0, len(omissionReasons))
		for _, reason := range omissionReasons {
			markers = append(markers, "[Earlier image omitted from model context because "+reason+".]")
		}
		if text != "" {
			text += "\n\n"
		}
		text += strings.Join(markers, "\n")
	}
	message := types.Message{Role: "user", Content: text}
	if len(attachments) == 0 {
		return message
	}
	message.ContentBlocks = make([]types.ContentBlock, 0, len(attachments)+1)
	if text != "" {
		message.ContentBlocks = append(message.ContentBlocks, types.ContentBlock{Type: "text", Text: text})
	}
	for _, attachment := range attachments {
		config, _, _ := image.DecodeConfig(bytes.NewReader(attachment.Data))
		message.ContentBlocks = append(message.ContentBlocks, types.ContentBlock{
			Type: "image_url",
			Image: &types.ContentImage{
				URL:       "data:" + attachment.MediaType + ";base64," + base64.StdEncoding.EncodeToString(attachment.Data),
				MediaType: attachment.MediaType,
				Width:     config.Width,
				Height:    config.Height,
			},
		})
	}
	return message
}

func validateStoredChatAttachment(attachment chatattachments.StoredAttachment) error {
	if attachment.SizeBytes <= 0 || attachment.SizeBytes > maxChatImageAttachmentBytes {
		return errors.New("stored chat attachment has invalid size metadata")
	}
	mediaType, _, err := mime.ParseMediaType(attachment.MediaType)
	if err != nil || mediaType == "" || mediaType != attachment.MediaType {
		return errors.New("stored chat attachment has invalid media type metadata")
	}
	if attachment.Filename == "" || attachment.Filename != normalizeChatAttachmentFilename(attachment.Filename, attachment.MediaType) {
		return errors.New("stored chat attachment has invalid filename metadata")
	}
	if int64(len(attachment.Data)) != attachment.SizeBytes {
		return errors.New("stored chat attachment size does not match its metadata")
	}
	digest := sha256.Sum256(attachment.Data)
	if hex.EncodeToString(digest[:]) != attachment.SHA256 {
		return errors.New("stored chat attachment digest does not match its metadata")
	}
	return nil
}

func validateStoredChatImageAttachment(attachment chatattachments.StoredAttachment) error {
	if _, ok := supportedChatImageFormats[attachment.MediaType]; !ok {
		return errors.New("stored chat attachment has an unsupported image media type")
	}
	return validateStoredChatAttachment(attachment)
}

var errStoredChatAttachmentTranscriptMismatch = errors.New("its stored metadata no longer matches the immutable transcript record")

func validateStoredChatAttachmentTranscript(
	sessionID string,
	transcript chat.MessageAttachment,
	stored chatattachments.StoredAttachment,
) error {
	if stored.SessionID != sessionID ||
		stored.ID != transcript.ID ||
		stored.Filename != transcript.Filename ||
		stored.MediaType != transcript.MediaType ||
		stored.SizeBytes != transcript.SizeBytes ||
		stored.SHA256 != transcript.SHA256 ||
		!stored.CreatedAt.Equal(transcript.CreatedAt) {
		return errStoredChatAttachmentTranscriptMismatch
	}
	return nil
}

func chatMessageAttachments(attachments []chatattachments.StoredAttachment) []chat.MessageAttachment {
	if len(attachments) == 0 {
		return nil
	}
	items := make([]chat.MessageAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		items = append(items, chat.MessageAttachment{
			ID:        attachment.ID,
			Filename:  attachment.Filename,
			MediaType: attachment.MediaType,
			SizeBytes: attachment.SizeBytes,
			SHA256:    attachment.SHA256,
			CreatedAt: attachment.CreatedAt,
		})
	}
	return items
}

func (h *Handler) reconcileChatAttachmentClaim(
	ctx context.Context,
	ref chatattachments.ClaimRef,
	attachments []chatattachments.StoredAttachment,
) error {
	if len(ref.AttachmentIDs) == 0 {
		return nil
	}
	metadata := make([]chatattachments.Attachment, 0, len(attachments))
	for _, attachment := range attachments {
		metadata = append(metadata, attachment.Attachment)
	}
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	result, err := chatapp.ReconcileAttachmentClaim(
		reconcileCtx,
		h.agentChat,
		h.chatAttachments,
		chatattachments.PendingClaim{Ref: ref, Attachments: metadata},
	)
	if err != nil {
		return err
	}
	if result.Conflict {
		return errors.New("chat attachment claim conflicts with transcript metadata")
	}
	return nil
}

func writeAgentChatImageCapabilityRequired(w http.ResponseWriter) {
	WriteErrorDetails(w, http.StatusUnprocessableEntity, errCodeImageCapability, "selected model route does not declare image-input support", ErrorDetails{
		UserMessage:    "The selected model is not marked as image-capable.",
		OperatorAction: "Choose a model with image-input support, or remove the images.",
	})
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
