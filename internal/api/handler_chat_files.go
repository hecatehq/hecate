package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/gitrunner"
	"github.com/hecatehq/hecate/internal/workspacecoord"
	"github.com/hecatehq/hecate/internal/workspacefs"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

const (
	chatWorkspaceFilesMaxEntries              = 5000
	chatWorkspaceReviewMaxEntries             = 5000
	chatWorkspaceReviewMaxPreviewBytesPerFile = 256 * 1024
	chatWorkspaceReviewMaxPreviewBytesTotal   = 4 * 1024 * 1024
	chatWorkspaceReviewPreviewReadTimeout     = 2 * time.Second
	chatWorkspaceRevertMaxBodyBytes           = 1024 * 1024
	chatWorkspaceDiscardMutationTimeout       = 30 * time.Second
)

var chatWorkspaceTracer = otel.Tracer("github.com/hecatehq/hecate/internal/api/chat-workspace")

var (
	errChatWorkspaceInaccessible         = errors.New("chat workspace is not accessible")
	errChatWorkspaceNotGit               = errors.New("chat workspace is not a git worktree")
	errChatWorkspaceReviewInventoryLarge = errors.New("chat workspace review inventory is too large")
	errChatWorkspaceReviewPathsUnsafe    = errors.New("chat workspace review paths are unsafe")
	errChatWorkspaceReviewChanged        = errors.New("chat workspace changed during review")
	errChatWorkspaceReviewCaptureFailed  = errors.New("chat workspace review capture failed")
	errChatWorkspaceFileInventoryLimit   = errors.New("chat workspace file inventory limit reached")
)

type chatWorkspaceGitRunner interface {
	IsWorkTree(context.Context, string) bool
	SnapshotDiff(context.Context, string, int64) (gitrunner.DiffSnapshot, error)
	SnapshotReview(context.Context, string, int64) (gitrunner.ReviewSnapshot, error)
	SnapshotReviewFile(context.Context, string, string, int64) (gitrunner.ReviewLayerSnapshot, gitrunner.ReviewStatusEntry, bool, error)
	ReviewAndDiffMatchWorkspace(string, gitrunner.ReviewSnapshot, gitrunner.DiffSnapshot) bool
	ReviewMatchesWorkspace(string, gitrunner.ReviewSnapshot) bool
	StatusPorcelain(context.Context, string, int64) (string, error)
	ReverseApplySnapshot(context.Context, string, gitrunner.DiffSnapshot, []string) (gitrunner.Result, error)
}

func (h *Handler) HandleChatMessageFiles(w http.ResponseWriter, r *http.Request) {
	_, message, ok := h.loadChatMessage(r.Context(), w, r)
	if !ok {
		return
	}
	files := chat.ParseChangedFiles(message.Diff, message.DiffStat)
	items := make([]ChatChangedFileItem, 0, len(files))
	for _, file := range files {
		items = append(items, renderChatChangedFile(file))
	}
	WriteJSON(w, http.StatusOK, ChatChangedFilesResponse{
		Object: "chat_changed_files",
		Data:   items,
	})
}

func (h *Handler) HandleChatMessageFileDiff(w http.ResponseWriter, r *http.Request) {
	_, message, ok := h.loadChatMessage(r.Context(), w, r)
	if !ok {
		return
	}
	path := r.PathValue("path")
	file, found := chat.ExtractFileDiff(message.Diff, path)
	if !found {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "changed file not found")
		return
	}
	WriteJSON(w, http.StatusOK, ChatChangedFileDiffResponse{
		Object: "chat_changed_file_diff",
		Data: ChatChangedFileDiffItem{
			Path:      file.Path,
			Additions: file.Additions,
			Deletions: file.Deletions,
			Status:    file.Status,
			Diff:      file.Diff,
		},
	})
}

func (h *Handler) HandleChatWorkspaceDiff(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	session, ok := h.loadChatSession(r.Context(), w, r)
	if !ok {
		return
	}
	item, ok := h.currentChatWorkspaceDiff(r.Context(), w, session)
	if !ok {
		return
	}
	WriteJSON(w, http.StatusOK, ChatWorkspaceDiffResponse{
		Object: "chat_workspace_diff",
		Data:   item,
	})
}

func (h *Handler) HandleChatWorkspaceFiles(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	session, ok := h.loadChatSession(r.Context(), w, r)
	if !ok {
		return
	}
	item, ok := h.currentChatWorkspaceFiles(r.Context(), w, session)
	if !ok {
		return
	}
	WriteJSON(w, http.StatusOK, ChatWorkspaceFilesResponse{
		Object: "chat_workspace_files",
		Data:   item,
	})
}

func (h *Handler) HandleChatWorkspaceFileDiff(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	session, ok := h.loadChatSession(r.Context(), w, r)
	if !ok {
		return
	}
	path := r.PathValue("path")
	file, found, ok := h.currentChatWorkspaceFileDiff(r.Context(), w, session, path)
	if !ok {
		return
	}
	if !found {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "workspace changed file not found")
		return
	}
	if file.Preview.Kind != "text_diff" {
		WriteErrorDetails(w, http.StatusUnprocessableEntity, errCodeInvalidRequest, "workspace file has no bounded text diff", ErrorDetails{
			UserMessage:    "This working-tree entry is available as metadata only (" + file.Preview.Kind + ").",
			OperatorAction: "Use the layered Workspace review for its exact preview state, or inspect the file with a workspace tool.",
		})
		return
	}
	WriteJSON(w, http.StatusOK, ChatChangedFileDiffResponse{
		Object: "chat_workspace_file_diff",
		Data: ChatChangedFileDiffItem{
			Path:      file.Path,
			Additions: file.Additions,
			Deletions: file.Deletions,
			Status:    file.Status,
			Diff:      file.Preview.Content,
		},
	})
}

func (h *Handler) currentChatWorkspaceFileDiff(ctx context.Context, w http.ResponseWriter, session chat.Session, path string) (ChatWorkspaceReviewFileItem, bool, bool) {
	ctx, span := chatWorkspaceTracer.Start(ctx, "chat.workspace.file_review")
	defer span.End()
	if !h.chatWorkspaceLinkReady(ctx, w, session) {
		return ChatWorkspaceReviewFileItem{}, false, false
	}
	workspace := strings.TrimSpace(session.Workspace)
	if workspace == "" {
		return ChatWorkspaceReviewFileItem{}, false, true
	}
	canonicalWorkspace, err := workspacecoord.CanonicalWorkspace(workspace)
	if err != nil {
		writeChatWorkspaceReviewCaptureError(w, fmt.Errorf("%w: %v", errChatWorkspaceInaccessible, err))
		return ChatWorkspaceReviewFileItem{}, false, false
	}
	runner := h.chatWorkspaceGitRunner()
	if !runner.IsWorkTree(ctx, canonicalWorkspace) {
		writeChatWorkspaceReviewCaptureError(w, errChatWorkspaceNotGit)
		return ChatWorkspaceReviewFileItem{}, false, false
	}
	var layer gitrunner.ReviewLayerSnapshot
	var status gitrunner.ReviewStatusEntry
	var found bool
	for attempt := 0; attempt < 2; attempt++ {
		layer, status, found, err = runner.SnapshotReviewFile(ctx, canonicalWorkspace, path, chatWorkspaceReviewMaxPreviewBytesPerFile)
		if !errors.Is(err, gitrunner.ErrReviewSnapshotChanged) {
			break
		}
	}
	if errors.Is(err, gitrunner.ErrReviewSnapshotTooLarge) {
		err = errChatWorkspaceReviewInventoryLarge
	} else if errors.Is(err, gitrunner.ErrReviewSnapshotInvalid) || errors.Is(err, gitrunner.ErrDiffSnapshotInvalid) {
		err = errChatWorkspaceReviewPathsUnsafe
	} else if errors.Is(err, gitrunner.ErrReviewSnapshotChanged) {
		err = errChatWorkspaceReviewChanged
	} else if err != nil {
		err = fmt.Errorf("%w: %v", errChatWorkspaceReviewCaptureFailed, err)
	}
	if err != nil {
		writeChatWorkspaceReviewCaptureError(w, err)
		return ChatWorkspaceReviewFileItem{}, false, false
	}
	statuses := map[string]gitrunner.ReviewStatusEntry{}
	if found {
		statuses[path] = status
	}
	file, found, err := projectTrackedWorkspaceReviewFile("working_tree", layer, statuses, path)
	if err != nil {
		writeChatWorkspaceReviewCaptureError(w, fmt.Errorf("%w: %v", errChatWorkspaceReviewPathsUnsafe, err))
		return ChatWorkspaceReviewFileItem{}, false, false
	}
	span.SetAttributes(
		attribute.Bool("hecate.workspace.file_review.found", found),
		attribute.String("hecate.workspace.file_review.preview_kind", file.Preview.Kind),
	)
	return file, found, true
}

func (h *Handler) HandleRevertChatWorkspaceFiles(w http.ResponseWriter, r *http.Request) {
	ctx, span := chatWorkspaceTracer.Start(r.Context(), "chat.workspace.discard")
	defer span.End()
	r = r.WithContext(ctx)
	w.Header().Set("Cache-Control", "private, no-store")
	session, ok := h.loadChatSession(r.Context(), w, r)
	if !ok {
		return
	}
	var req RevertChatWorkspaceFilesRequest
	r.Body = http.MaxBytesReader(w, r.Body, chatWorkspaceRevertMaxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			WriteError(w, http.StatusRequestEntityTooLarge, errCodeInvalidRequest, "request body is too large")
			return
		}
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "invalid JSON body")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			WriteError(w, http.StatusRequestEntityTooLarge, errCodeInvalidRequest, "request body is too large")
			return
		}
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "request body must contain exactly one JSON object")
		return
	}
	expectedRevision := strings.TrimSpace(req.ExpectedRevision)
	if expectedRevision == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "expected_revision is required")
		return
	}
	if req.Paths == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "paths must be an array; use an explicit empty array to discard every reviewed path")
		return
	}
	requestedPaths := *req.Paths
	if busy, status := h.chatWorkspaceRevertBusy(r.Context(), session); busy {
		writeChatWorkspaceRevertBusy(w, status)
		return
	}
	canonicalWorkspace, snapshot, ok := h.currentChatWorkspaceDiscardSnapshot(r.Context(), w, session)
	if !ok {
		return
	}
	if expectedRevision != snapshot.DiscardRevision {
		writeChatWorkspaceDiffConflict(w)
		return
	}
	if len(snapshot.Paths) == 0 {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "workspace has no current git diff")
		return
	}
	allowed := make(map[string]struct{}, len(snapshot.Paths))
	for _, path := range snapshot.Paths {
		allowed[path] = struct{}{}
	}
	for _, path := range requestedPaths {
		if path == "" {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "discard paths must be non-empty workspace-relative paths")
			return
		}
	}
	paths := normalizeRevertPaths(requestedPaths)
	if len(paths) == 0 {
		paths = append(paths, snapshot.Paths...)
	}
	span.SetAttributes(attribute.Int("hecate.workspace.discard.path_count", len(paths)))
	for _, path := range paths {
		if _, ok := allowed[path]; !ok {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "path is not present in the current workspace diff: "+path)
			return
		}
	}
	if h.agentChatLive == nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "chat lifecycle coordination is unavailable")
		return
	}
	lifecycleClosure := h.agentChatLive.closeSessionLifecycle(session.ID)
	defer lifecycleClosure.release()
	operationCtx, operationCancel := context.WithTimeout(r.Context(), 3*time.Second)
	operationsDrained := lifecycleClosure.waitForOperations(operationCtx)
	operationCancel()
	if !operationsDrained {
		writeChatWorkspaceRevertBusy(w, "settling")
		return
	}

	workspaceClosure, ok := h.closeWorkspaceForRevert(w, r.Context(), session.Workspace)
	if !ok {
		return
	}
	defer workspaceClosure.Release()
	if workspaceClosure.Workspace() != canonicalWorkspace {
		writeChatWorkspaceDiffConflict(w)
		return
	}

	// Re-read every durable owner after both admission domains are closed. A
	// browser confirmation is not authority to discard edits while another chat
	// or Task has queued, running, or approval-blocked work for the same path.
	latestSession, ok := h.loadChatSession(r.Context(), w, r)
	if !ok {
		return
	}
	latestCanonicalWorkspace, err := workspacecoord.CanonicalWorkspace(latestSession.Workspace)
	if err != nil || latestCanonicalWorkspace != workspaceClosure.Workspace() {
		writeChatWorkspaceDiffConflict(w)
		return
	}
	if busy, status := h.chatWorkspaceRevertBusy(r.Context(), latestSession); busy {
		writeChatWorkspaceRevertBusy(w, status)
		return
	}
	if status, active, err := h.chatWorkspaceDurableOwner(r.Context(), workspaceClosure.Workspace(), session.ID); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "failed to verify workspace activity")
		return
	} else if active {
		writeChatWorkspaceRevertBusy(w, status)
		return
	}
	// Once every authority and ownership check passes, client disconnect must
	// not interrupt a destructive Git subprocess. Keep the lifecycle and
	// workspace closures held under a short server-owned deadline through the
	// exact recapture, mutation, and best-effort refresh.
	mutationCtx, mutationCancel := context.WithTimeout(context.WithoutCancel(r.Context()), chatWorkspaceDiscardMutationTimeout)
	defer mutationCancel()
	current, err := h.chatWorkspaceGitRunner().SnapshotDiff(mutationCtx, workspaceClosure.Workspace(), agentChatMaxOutputBytes)
	if err != nil {
		if errors.Is(err, gitrunner.ErrStagedChangesUnsupported) || errors.Is(err, gitrunner.ErrIndexVisibilityUnsupported) || errors.Is(err, gitrunner.ErrSubmoduleChangesUnsupported) || errors.Is(err, gitrunner.ErrTrackedPathTopologyUnsafe) || errors.Is(err, gitrunner.ErrDiffSnapshotNotApplicable) || errors.Is(err, gitrunner.ErrDiffSnapshotTooLarge) || errors.Is(err, gitrunner.ErrDiffSnapshotInvalid) {
			writeChatWorkspaceDiffConflict(w)
			return
		}
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "failed to verify the workspace before discard")
		return
	}
	if expectedRevision != current.DiscardRevision {
		writeChatWorkspaceDiffConflict(w)
		return
	}
	_, err = h.chatWorkspaceGitRunner().ReverseApplySnapshot(mutationCtx, workspaceClosure.Workspace(), current, paths)
	committedCleanupWarning := errors.Is(err, gitrunner.ErrDiffSnapshotApplied)
	cleanupFailed := committedCleanupWarning || errors.Is(err, gitrunner.ErrDiffSnapshotCleanupFailed)
	outcomeUnknown := errors.Is(err, gitrunner.ErrDiffSnapshotOutcomeUnknown)
	if cleanupFailed {
		span.SetAttributes(attribute.Bool("hecate.workspace.discard.cleanup_failed", true))
		h.logger.WarnContext(context.WithoutCancel(r.Context()), "chat.workspace.discard.cleanup_failed", "session_id", session.ID)
	}
	if errors.Is(err, gitrunner.ErrDiffSnapshotNotApplicable) {
		if cleanupFailed {
			writeChatWorkspaceDiscardCleanupFailure(w)
			return
		}
		writeChatWorkspaceDiffConflict(w)
		return
	}
	if err != nil && !committedCleanupWarning && !outcomeUnknown {
		if cleanupFailed {
			writeChatWorkspaceDiscardCleanupFailure(w)
			return
		}
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "failed to discard the selected workspace changes")
		return
	}
	outcome := "applied"
	if outcomeUnknown {
		outcome = "outcome_unknown"
	}
	span.SetAttributes(
		attribute.Bool("hecate.workspace.discard.succeeded", !outcomeUnknown),
		attribute.String("hecate.workspace.discard.outcome", outcome),
	)
	if outcomeUnknown {
		span.SetAttributes(attribute.Bool("hecate.workspace.discard.outcome_unknown", true))
		h.logger.WarnContext(context.WithoutCancel(r.Context()), "chat.workspace.discard.outcome_unknown", "session_id", session.ID)
	}
	next, refreshErr := h.captureChatWorkspaceDiff(mutationCtx, latestSession, true)
	refreshRequired := refreshErr != nil || cleanupFailed || outcomeUnknown
	if refreshErr != nil {
		refreshReason := chatWorkspaceReviewCaptureReason(refreshErr)
		span.SetAttributes(
			attribute.Bool("hecate.workspace.discard.refresh_succeeded", false),
			attribute.String("hecate.workspace.discard.refresh_reason", refreshReason),
		)
		h.logger.WarnContext(context.WithoutCancel(r.Context()), "chat.workspace.discard.refresh_failed", "session_id", session.ID, "reason", refreshReason)
		reason := "refresh_required"
		if cleanupFailed {
			reason = "cleanup_failed"
		}
		if outcomeUnknown {
			reason = "mutation_outcome_unknown"
		}
		next = refreshRequiredChatWorkspaceReview(latestSession.Workspace, reason)
	} else {
		span.SetAttributes(attribute.Bool("hecate.workspace.discard.refresh_succeeded", true))
		if cleanupFailed {
			next.Discard = ChatWorkspaceDiscardItem{Available: false, Reason: "cleanup_failed"}
		}
		if outcomeUnknown {
			next.Discard = ChatWorkspaceDiscardItem{Available: false, Reason: "mutation_outcome_unknown"}
		}
		setChatWorkspaceReviewSpanAttributes(span, next)
	}
	// The mutation and refresh are complete. Do not retain either admission
	// closure while a slow client receives a response that may approach the
	// bounded multi-megabyte preview limit.
	workspaceClosure.Release()
	lifecycleClosure.release()
	WriteJSON(w, http.StatusOK, ChatWorkspaceDiffResponse{
		Object: "chat_workspace_diff",
		Data:   next,
		DiscardResult: &ChatWorkspaceDiscardResultItem{
			Outcome:         outcome,
			RefreshRequired: refreshRequired,
			CleanupFailed:   cleanupFailed,
		},
	})
}

func writeChatWorkspaceDiscardCleanupFailure(w http.ResponseWriter) {
	WriteErrorDetails(w, http.StatusInternalServerError, errCodeGatewayError, "workspace discard cleanup did not complete", ErrorDetails{
		UserMessage:    "Hecate could not safely release its Git index reservation.",
		OperatorAction: "Inspect the workspace Git state and index lock before retrying or running another Git command.",
	})
}

func (h *Handler) currentChatWorkspaceDiscardSnapshot(ctx context.Context, w http.ResponseWriter, session chat.Session) (string, gitrunner.DiffSnapshot, bool) {
	if !h.chatWorkspaceLinkReady(ctx, w, session) {
		return "", gitrunner.DiffSnapshot{}, false
	}
	workspace := strings.TrimSpace(session.Workspace)
	if workspace == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "chat workspace is required")
		return "", gitrunner.DiffSnapshot{}, false
	}
	canonicalWorkspace, err := workspacecoord.CanonicalWorkspace(workspace)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "chat workspace is not accessible")
		return "", gitrunner.DiffSnapshot{}, false
	}
	runner := h.chatWorkspaceGitRunner()
	if !runner.IsWorkTree(ctx, canonicalWorkspace) {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "chat workspace is not a git worktree")
		return "", gitrunner.DiffSnapshot{}, false
	}
	snapshot, err := runner.SnapshotDiff(ctx, canonicalWorkspace, agentChatMaxOutputBytes)
	if err != nil {
		writeChatWorkspaceDiscardCaptureError(w, err)
		return "", gitrunner.DiffSnapshot{}, false
	}
	return canonicalWorkspace, snapshot, true
}

func writeChatWorkspaceDiscardCaptureError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, gitrunner.ErrDiffSnapshotNotApplicable):
		writeChatWorkspaceDiffConflict(w)
	case errors.Is(err, gitrunner.ErrStagedChangesUnsupported):
		WriteErrorDetails(w, http.StatusUnprocessableEntity, errCodeInvalidRequest, "staged workspace changes cannot be discarded", ErrorDetails{
			UserMessage:    "Staged changes are visible in Review but cannot be discarded by Hecate.",
			OperatorAction: "Unstage the scoped changes, refresh Workspace changes, and review the working-tree layer again.",
		})
	case errors.Is(err, gitrunner.ErrIndexVisibilityUnsupported):
		WriteErrorDetails(w, http.StatusUnprocessableEntity, errCodeInvalidRequest, "workspace discard is unavailable while Git hides tracked paths", ErrorDetails{
			UserMessage:    "Git index flags make the working-tree review incomplete.",
			OperatorAction: "Clear assume-unchanged, skip-worktree, or intent-to-add state before discarding changes.",
		})
	case errors.Is(err, gitrunner.ErrSubmoduleChangesUnsupported):
		WriteErrorDetails(w, http.StatusUnprocessableEntity, errCodeInvalidRequest, "workspace discard is unavailable for Git submodule changes", ErrorDetails{
			UserMessage:    "Submodule changes are visible in Review but cannot be discarded by Hecate.",
			OperatorAction: "Inspect or restore the nested repository directly, then refresh Workspace changes.",
		})
	case errors.Is(err, gitrunner.ErrTrackedPathTopologyUnsafe):
		WriteErrorDetails(w, http.StatusUnprocessableEntity, errCodeInvalidRequest, "workspace discard is unavailable for an unsafe tracked path topology", ErrorDetails{
			UserMessage:    "A tracked path is not a single-link regular file that Hecate can restore safely.",
			OperatorAction: "Inspect hardlinks or special tracked paths, replace them with ordinary workspace files, and refresh Workspace changes.",
		})
	case errors.Is(err, gitrunner.ErrDiffSnapshotTooLarge):
		WriteError(w, http.StatusUnprocessableEntity, errCodeInvalidRequest, "workspace diff is too large to discard safely")
	case errors.Is(err, gitrunner.ErrDiffSnapshotInvalid):
		WriteError(w, http.StatusUnprocessableEntity, errCodeInvalidRequest, "workspace diff paths cannot be represented safely for discard")
	default:
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "failed to capture workspace discard capability")
	}
}

func (h *Handler) loadChatSession(ctx context.Context, w http.ResponseWriter, r *http.Request) (chat.Session, bool) {
	sessionID := strings.TrimSpace(r.PathValue("id"))
	if sessionID == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "session id is required")
		return chat.Session{}, false
	}
	session, ok, err := h.agentChat.Get(ctx, sessionID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return chat.Session{}, false
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "agent chat session not found")
		return chat.Session{}, false
	}
	return session, true
}

func (h *Handler) currentChatWorkspaceDiff(ctx context.Context, w http.ResponseWriter, session chat.Session) (ChatWorkspaceDiffItem, bool) {
	return h.currentChatWorkspaceDiffWithUntrackedPreviews(ctx, w, session, true)
}

func (h *Handler) currentChatWorkspaceDiffWithUntrackedPreviews(ctx context.Context, w http.ResponseWriter, session chat.Session, includeUntrackedPreviews bool) (ChatWorkspaceDiffItem, bool) {
	ctx, span := chatWorkspaceTracer.Start(ctx, "chat.workspace.review")
	defer span.End()
	if !h.chatWorkspaceLinkReady(ctx, w, session) {
		return ChatWorkspaceDiffItem{}, false
	}
	item, err := h.captureChatWorkspaceDiff(ctx, session, includeUntrackedPreviews)
	if err != nil {
		writeChatWorkspaceReviewCaptureError(w, err)
		return ChatWorkspaceDiffItem{}, false
	}
	setChatWorkspaceReviewSpanAttributes(span, item)
	return item, true
}

func (h *Handler) captureChatWorkspaceDiff(ctx context.Context, session chat.Session, includeUntrackedPreviews bool) (ChatWorkspaceDiffItem, error) {
	workspace := strings.TrimSpace(session.Workspace)
	if workspace == "" {
		return emptyChatWorkspaceReview(workspace), nil
	}
	canonicalWorkspace, err := workspacecoord.CanonicalWorkspace(workspace)
	if err != nil {
		return ChatWorkspaceDiffItem{}, fmt.Errorf("%w: %v", errChatWorkspaceInaccessible, err)
	}
	runner := h.chatWorkspaceGitRunner()
	if !runner.IsWorkTree(ctx, canonicalWorkspace) {
		return ChatWorkspaceDiffItem{}, errChatWorkspaceNotGit
	}
	for attempt := 0; attempt < 2; attempt++ {
		var review gitrunner.ReviewSnapshot
		review, err = runner.SnapshotReview(ctx, canonicalWorkspace, agentChatMaxOutputBytes)
		if errors.Is(err, gitrunner.ErrReviewSnapshotChanged) && attempt == 0 {
			continue
		}
		if errors.Is(err, gitrunner.ErrReviewSnapshotTooLarge) {
			return ChatWorkspaceDiffItem{}, errChatWorkspaceReviewInventoryLarge
		}
		if errors.Is(err, gitrunner.ErrReviewSnapshotInvalid) || errors.Is(err, gitrunner.ErrDiffSnapshotInvalid) {
			return ChatWorkspaceDiffItem{}, errChatWorkspaceReviewPathsUnsafe
		}
		if errors.Is(err, gitrunner.ErrReviewSnapshotChanged) {
			return ChatWorkspaceDiffItem{}, errChatWorkspaceReviewChanged
		}
		if err != nil {
			return ChatWorkspaceDiffItem{}, fmt.Errorf("%w: %v", errChatWorkspaceReviewCaptureFailed, err)
		}

		discardSnapshot, discardReason, drifted, discardErr := chatWorkspaceDiscardSnapshot(ctx, runner, canonicalWorkspace, review)
		if drifted {
			if attempt == 0 {
				continue
			}
			return ChatWorkspaceDiffItem{}, errChatWorkspaceReviewChanged
		}
		if discardErr != nil {
			discardSnapshot = nil
			discardReason = "discard_capture_failed"
			h.logger.WarnContext(context.WithoutCancel(ctx), "chat.workspace.review.discard_capture_failed",
				"session_id", session.ID,
				"reason", "discard_capture_failed",
			)
		}
		var previewFS *workspacefs.FS
		if includeUntrackedPreviews {
			previewFS, err = workspacefs.NewPinned(canonicalWorkspace)
			if err != nil {
				return ChatWorkspaceDiffItem{}, fmt.Errorf("%w: %v", errChatWorkspaceInaccessible, err)
			}
			if !runner.ReviewMatchesWorkspace(canonicalWorkspace, review) {
				if attempt == 0 {
					continue
				}
				return ChatWorkspaceDiffItem{}, errChatWorkspaceReviewChanged
			}
		}
		if discardSnapshot != nil && !runner.ReviewAndDiffMatchWorkspace(canonicalWorkspace, review, *discardSnapshot) {
			if attempt == 0 {
				continue
			}
			return ChatWorkspaceDiffItem{}, errChatWorkspaceReviewChanged
		}
		item, projectErr := projectChatWorkspaceReviewWithUntrackedPreviews(ctx, workspace, review, discardSnapshot, discardReason, previewFS, includeUntrackedPreviews)
		if projectErr != nil {
			return ChatWorkspaceDiffItem{}, fmt.Errorf("%w: %v", errChatWorkspaceReviewPathsUnsafe, projectErr)
		}
		return item, nil
	}
	return ChatWorkspaceDiffItem{}, errChatWorkspaceReviewChanged
}

func setChatWorkspaceReviewSpanAttributes(span interface{ SetAttributes(...attribute.KeyValue) }, item ChatWorkspaceDiffItem) {
	stagedCount, workingCount, untrackedCount := 0, 0, 0
	for _, layer := range item.Layers {
		switch layer.Kind {
		case "staged":
			stagedCount = len(layer.Files) + layer.OmittedCount
		case "working_tree":
			workingCount = len(layer.Files) + layer.OmittedCount
		case "untracked":
			untrackedCount = len(layer.Files) + layer.OmittedCount
		}
	}
	span.SetAttributes(
		attribute.Int("hecate.workspace.review.staged_count", stagedCount),
		attribute.Int("hecate.workspace.review.working_tree_count", workingCount),
		attribute.Int("hecate.workspace.review.untracked_count", untrackedCount),
		attribute.Bool("hecate.workspace.review.complete", item.ReviewComplete),
		attribute.Bool("hecate.workspace.discard.available", item.Discard.Available),
		attribute.String("hecate.workspace.discard.reason", item.Discard.Reason),
	)
}

func writeChatWorkspaceReviewCaptureError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errChatWorkspaceInaccessible):
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "chat workspace is not accessible")
	case errors.Is(err, errChatWorkspaceNotGit):
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "chat workspace is not a git worktree")
	case errors.Is(err, errChatWorkspaceReviewInventoryLarge):
		WriteError(w, http.StatusUnprocessableEntity, errCodeInvalidRequest, "workspace change inventory is too large to review safely")
	case errors.Is(err, errChatWorkspaceReviewPathsUnsafe):
		WriteError(w, http.StatusUnprocessableEntity, errCodeInvalidRequest, "workspace change paths cannot be represented safely for review")
	case errors.Is(err, errChatWorkspaceReviewChanged):
		WriteErrorDetails(w, http.StatusConflict, errCodeConflict, "workspace changed while its review was captured", ErrorDetails{
			UserMessage:    "The workspace changed while Hecate was preparing the review.",
			OperatorAction: "Refresh Workspace changes after the current writer settles.",
		})
	default:
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "failed to capture workspace review")
	}
}

func chatWorkspaceReviewCaptureReason(err error) string {
	switch {
	case errors.Is(err, errChatWorkspaceInaccessible):
		return "workspace_inaccessible"
	case errors.Is(err, errChatWorkspaceNotGit):
		return "not_git"
	case errors.Is(err, errChatWorkspaceReviewInventoryLarge):
		return "inventory_too_large"
	case errors.Is(err, errChatWorkspaceReviewPathsUnsafe):
		return "unsafe_paths"
	case errors.Is(err, errChatWorkspaceReviewChanged):
		return "workspace_changed"
	default:
		return "review_capture_failed"
	}
}

func emptyChatWorkspaceReview(workspace string) ChatWorkspaceDiffItem {
	layers := []ChatWorkspaceReviewLayerItem{
		{Kind: "staged", Complete: true, Files: []ChatWorkspaceReviewFileItem{}},
		{Kind: "working_tree", Complete: true, Files: []ChatWorkspaceReviewFileItem{}},
		{Kind: "untracked", Complete: true, Files: []ChatWorkspaceReviewFileItem{}},
	}
	return ChatWorkspaceDiffItem{
		Workspace:      workspace,
		ReviewComplete: true,
		HasChanges:     false,
		Files:          []ChatChangedFileItem{},
		Layers:         layers,
		Discard:        ChatWorkspaceDiscardItem{Available: false, Reason: "no_working_tree_changes"},
	}
}

func refreshRequiredChatWorkspaceReview(workspace, reason string) ChatWorkspaceDiffItem {
	if strings.TrimSpace(reason) == "" {
		reason = "refresh_required"
	}
	layers := []ChatWorkspaceReviewLayerItem{
		{Kind: "staged", Complete: false, Files: []ChatWorkspaceReviewFileItem{}},
		{Kind: "working_tree", Complete: false, Files: []ChatWorkspaceReviewFileItem{}},
		{Kind: "untracked", Complete: false, Files: []ChatWorkspaceReviewFileItem{}},
	}
	return ChatWorkspaceDiffItem{
		Workspace:      workspace,
		ReviewComplete: false,
		HasChanges:     true,
		Files:          []ChatChangedFileItem{},
		Layers:         layers,
		Discard:        ChatWorkspaceDiscardItem{Available: false, Reason: reason},
	}
}

func chatWorkspaceDiscardSnapshot(ctx context.Context, runner chatWorkspaceGitRunner, workspace string, review gitrunner.ReviewSnapshot) (*gitrunner.DiffSnapshot, string, bool, error) {
	if len(review.Hidden) > 0 {
		return nil, "review_incomplete", false, nil
	}
	if len(review.Staged.Paths) > 0 {
		return nil, "staged_changes", false, nil
	}
	if !review.Unstaged.Complete {
		return nil, "working_tree_too_large", false, nil
	}
	// A bounded review intentionally carries metadata only for binary files.
	// Keep that evidence visible, but do not spend the passive-view deadline
	// generating a potentially multi-megabyte binary patch or grant mutation
	// authority for evidence that cannot be compared byte-for-byte.
	if !review.Unstaged.Exact {
		return nil, "working_tree_preview_incomplete", false, nil
	}
	snapshot, err := runner.SnapshotDiff(ctx, workspace, agentChatMaxOutputBytes)
	switch {
	case errors.Is(err, gitrunner.ErrStagedChangesUnsupported):
		return nil, "", true, nil
	case errors.Is(err, gitrunner.ErrIndexVisibilityUnsupported):
		return nil, "", true, nil
	case errors.Is(err, gitrunner.ErrSubmoduleChangesUnsupported):
		if trackedWorkspacePatchHasGitlinkMode(review.Unstaged.Diff) {
			return nil, "working_tree_preview_incomplete", false, nil
		}
		return nil, "", true, nil
	case errors.Is(err, gitrunner.ErrTrackedPathTopologyUnsafe):
		return nil, "working_tree_preview_incomplete", false, nil
	case errors.Is(err, gitrunner.ErrDiffSnapshotTooLarge):
		return nil, "working_tree_too_large", false, nil
	case errors.Is(err, gitrunner.ErrDiffSnapshotInvalid):
		return nil, "", false, err
	case errors.Is(err, gitrunner.ErrDiffSnapshotNotApplicable):
		return nil, "", true, nil
	case err != nil:
		return nil, "", false, err
	}
	if !chatWorkspaceReviewMatchesDiscard(review.Unstaged, snapshot) {
		return nil, "", true, nil
	}
	if len(snapshot.Paths) == 0 {
		return &snapshot, "no_working_tree_changes", false, nil
	}
	return &snapshot, "", false, nil
}

// chatWorkspaceReviewMatchesDiscard keeps the operator review and mutation
// authority on one workspace generation. A metadata-only binary review is not
// exact and therefore never authorizes mutation.
func chatWorkspaceReviewMatchesDiscard(review gitrunner.ReviewLayerSnapshot, snapshot gitrunner.DiffSnapshot) bool {
	return review.Exact && snapshot.Diff == review.Diff && slices.Equal(snapshot.Paths, review.Paths)
}

func projectChatWorkspaceReview(ctx context.Context, workspace string, review gitrunner.ReviewSnapshot, discardSnapshot *gitrunner.DiffSnapshot, discardReason string, previewFS *workspacefs.FS) (ChatWorkspaceDiffItem, error) {
	return projectChatWorkspaceReviewWithUntrackedPreviews(ctx, workspace, review, discardSnapshot, discardReason, previewFS, true)
}

func projectChatWorkspaceReviewWithUntrackedPreviews(ctx context.Context, workspace string, review gitrunner.ReviewSnapshot, discardSnapshot *gitrunner.DiffSnapshot, discardReason string, previewFS *workspacefs.FS, includeUntrackedPreviews bool) (ChatWorkspaceDiffItem, error) {
	issues := make([]ChatWorkspaceReviewIssueItem, 0, len(review.Hidden))
	for index, entry := range review.Hidden {
		if index >= chatWorkspaceReviewMaxEntries {
			break
		}
		issues = append(issues, ChatWorkspaceReviewIssueItem{Kind: entry.Kind, Path: entry.Path})
	}
	omittedIssueCount := max(0, len(review.Hidden)-len(issues))

	remainingPreviewBytes := int64(chatWorkspaceReviewMaxPreviewBytesTotal)
	statusIndex := indexWorkspaceReviewStatuses(review.Status)
	stagedLayer, err := projectTrackedWorkspaceReviewLayer("staged", review.Staged, statusIndex, &remainingPreviewBytes, 1)
	if err != nil {
		return ChatWorkspaceDiffItem{}, err
	}
	workingSource := review.Unstaged
	if discardSnapshot != nil {
		// Bind every displayed working-tree text hunk to the exact raw patch
		// covered by the discard revision. Binary payloads are stripped below.
		workingSource = gitrunner.ReviewLayerSnapshot{
			Diff:     discardSnapshot.Diff,
			Paths:    append([]string(nil), discardSnapshot.Paths...),
			Complete: true,
		}
	}
	// Working-tree text appears in both the layered contract and the legacy
	// compatibility projection, so charge it twice against the one response
	// content budget.
	workingLayer, err := projectTrackedWorkspaceReviewLayer("working_tree", workingSource, statusIndex, &remainingPreviewBytes, 2)
	if err != nil {
		return ChatWorkspaceDiffItem{}, err
	}
	var untrackedLayer ChatWorkspaceReviewLayerItem
	if includeUntrackedPreviews {
		untrackedLayer = projectUntrackedWorkspaceReviewLayer(ctx, previewFS, review.Untracked, &remainingPreviewBytes)
	} else {
		untrackedLayer = projectUntrackedWorkspaceReviewMetadata(review.Untracked)
	}

	if len(review.Hidden) > 0 {
		workingLayer.Complete = false
		for _, issue := range review.Hidden {
			if issue.Kind == "unmerged" {
				stagedLayer.Complete = false
			}
		}
	}
	layers := []ChatWorkspaceReviewLayerItem{stagedLayer, workingLayer, untrackedLayer}
	hasChanges := len(issues) > 0
	for _, layer := range layers {
		hasChanges = hasChanges || len(layer.Files) > 0 || layer.OmittedCount > 0
	}
	reviewComplete := review.Complete && len(issues) == 0
	for _, layer := range layers {
		reviewComplete = reviewComplete && layer.Complete
	}

	legacyFiles := make([]ChatChangedFileItem, 0, len(workingLayer.Files))
	var legacyPatch strings.Builder
	for _, file := range workingLayer.Files {
		legacyFiles = append(legacyFiles, ChatChangedFileItem{
			Path: file.Path, Additions: file.Additions, Deletions: file.Deletions, Status: file.Status,
		})
		if patch := safeLegacyWorkspacePatch(file); patch != "" {
			if legacyPatch.Len() > 0 {
				legacyPatch.WriteByte('\n')
			}
			legacyPatch.WriteString(patch)
		}
	}

	discard := ChatWorkspaceDiscardItem{Reason: discardReason}
	legacyRevision := ""
	diffStat := ""
	if discardSnapshot != nil && !workingLayer.Complete && len(discardSnapshot.Paths) > 0 {
		discard.Reason = "working_tree_preview_incomplete"
	}
	discardSnapshotAuthoritative := discardSnapshot != nil && workingLayer.Complete && strings.TrimSpace(discardSnapshot.DiscardRevision) != ""
	if discardSnapshot != nil && workingLayer.Complete && strings.TrimSpace(discardSnapshot.DiscardRevision) == "" && discard.Reason == "" {
		discard.Reason = "discard_capture_failed"
	}
	if discardSnapshotAuthoritative {
		legacyRevision = discardSnapshot.Revision
		if len(discardSnapshot.Paths) > 0 {
			diffStat = discardSnapshot.Stat
		}
	}
	if discardSnapshotAuthoritative && len(discardSnapshot.Paths) > 0 && discard.Reason == "" {
		discard.Available = true
		discard.Revision = discardSnapshot.DiscardRevision
	}
	return ChatWorkspaceDiffItem{
		Workspace:                workspace,
		Revision:                 legacyRevision,
		ReviewComplete:           reviewComplete,
		ReviewIssues:             issues,
		ReviewIssuesOmittedCount: omittedIssueCount,
		DiffStat:                 diffStat,
		Diff:                     legacyPatch.String(),
		HasChanges:               hasChanges,
		Files:                    legacyFiles,
		Layers:                   layers,
		Discard:                  discard,
	}, nil
}

func projectUntrackedWorkspaceReviewMetadata(entries []gitrunner.ReviewUntrackedEntry) ChatWorkspaceReviewLayerItem {
	result := ChatWorkspaceReviewLayerItem{Kind: "untracked", Complete: true, Files: []ChatWorkspaceReviewFileItem{}}
	if len(entries) > chatWorkspaceReviewMaxEntries {
		result.OmittedCount = len(entries) - chatWorkspaceReviewMaxEntries
		entries = entries[:chatWorkspaceReviewMaxEntries]
		result.Complete = false
	}
	for _, entry := range entries {
		item := ChatWorkspaceReviewFileItem{
			ID:      chatWorkspaceReviewEntryID("untracked", entry.Path),
			Layer:   "untracked",
			Path:    entry.Path,
			Status:  "untracked",
			Preview: ChatWorkspaceReviewPreviewItem{Kind: "unavailable", Reason: "not_requested"},
		}
		if entry.Kind == "nested_repository" {
			item.Preview = ChatWorkspaceReviewPreviewItem{Kind: "nested_repository"}
		}
		result.Files = append(result.Files, item)
	}
	return result
}

func projectTrackedWorkspaceReviewLayer(kind string, layer gitrunner.ReviewLayerSnapshot, statuses map[string]gitrunner.ReviewStatusEntry, remainingPreviewBytes *int64, responseCopies int64) (ChatWorkspaceReviewLayerItem, error) {
	if responseCopies < 1 {
		responseCopies = 1
	}
	result := ChatWorkspaceReviewLayerItem{Kind: kind, Complete: layer.Complete, Files: []ChatWorkspaceReviewFileItem{}}
	if layer.Complete {
		parsed := chat.ParseChangedFiles(layer.Diff, "")
		if len(parsed) != len(layer.Paths) {
			return ChatWorkspaceReviewLayerItem{}, errors.New("tracked review patch paths do not match inventory")
		}
		for index, file := range parsed {
			if !validChatWorkspaceReviewPath(file.Path) {
				return ChatWorkspaceReviewLayerItem{}, errors.New("invalid tracked review path")
			}
			if file.Path != layer.Paths[index] {
				return ChatWorkspaceReviewLayerItem{}, errors.New("tracked review patch paths do not match inventory")
			}
		}
		visibleCount := min(len(parsed), chatWorkspaceReviewMaxEntries)
		result.OmittedCount = len(parsed) - visibleCount
		if result.OmittedCount > 0 {
			result.Complete = false
		}
		for _, file := range parsed[:visibleCount] {
			preview := trackedWorkspacePreview(file.Diff)
			if preview.Kind == "text_diff" {
				cost := int64(len(preview.Content)) * responseCopies
				if remainingPreviewBytes == nil || cost > *remainingPreviewBytes {
					preview = ChatWorkspaceReviewPreviewItem{Kind: "too_large", Reason: "total_limit"}
				} else {
					*remainingPreviewBytes -= cost
				}
			}
			if preview.Kind == "too_large" || preview.Kind == "nested_repository" {
				result.Complete = false
			}
			status := reviewStatusForPath(statuses, kind, file.Path, file.Status)
			result.Files = append(result.Files, ChatWorkspaceReviewFileItem{
				ID:        chatWorkspaceReviewEntryID(kind, file.Path),
				Layer:     kind,
				Path:      file.Path,
				Additions: file.Additions,
				Deletions: file.Deletions,
				Status:    status,
				Preview:   preview,
			})
		}
	} else {
		visibleCount := min(len(layer.Paths), chatWorkspaceReviewMaxEntries)
		result.OmittedCount = len(layer.Paths) - visibleCount
		for _, path := range layer.Paths[:visibleCount] {
			if !validChatWorkspaceReviewPath(path) {
				return ChatWorkspaceReviewLayerItem{}, errors.New("invalid tracked review path")
			}
			previewKind := "too_large"
			previewReason := "layer_limit"
			if layer.IncompleteReason == "unmerged_state" {
				previewKind = "unavailable"
				previewReason = "unmerged_state"
			}
			if reviewPathHasConflict(statuses, path) {
				previewKind = "conflict"
				previewReason = "unmerged"
			}
			result.Files = append(result.Files, ChatWorkspaceReviewFileItem{
				ID:      chatWorkspaceReviewEntryID(kind, path),
				Layer:   kind,
				Path:    path,
				Status:  reviewStatusForPath(statuses, kind, path, "modified"),
				Preview: ChatWorkspaceReviewPreviewItem{Kind: previewKind, Reason: previewReason},
			})
		}
	}
	return result, nil
}

func projectTrackedWorkspaceReviewFile(kind string, layer gitrunner.ReviewLayerSnapshot, statuses map[string]gitrunner.ReviewStatusEntry, path string) (ChatWorkspaceReviewFileItem, bool, error) {
	if !validChatWorkspaceReviewPath(path) {
		return ChatWorkspaceReviewFileItem{}, false, nil
	}
	index, found := slices.BinarySearch(layer.Paths, path)
	if !found {
		return ChatWorkspaceReviewFileItem{}, false, nil
	}
	if !layer.Complete {
		preview := ChatWorkspaceReviewPreviewItem{Kind: "too_large", Reason: "layer_limit"}
		if layer.IncompleteReason == "unmerged_state" {
			preview = ChatWorkspaceReviewPreviewItem{Kind: "unavailable", Reason: "unmerged_state"}
		}
		if reviewPathHasConflict(statuses, path) {
			preview = ChatWorkspaceReviewPreviewItem{Kind: "conflict", Reason: "unmerged"}
		}
		return ChatWorkspaceReviewFileItem{
			ID:      chatWorkspaceReviewEntryID(kind, path),
			Layer:   kind,
			Path:    path,
			Status:  reviewStatusForPath(statuses, kind, path, "modified"),
			Preview: preview,
		}, true, nil
	}
	parsed := chat.ParseChangedFiles(layer.Diff, "")
	if len(parsed) != len(layer.Paths) {
		return ChatWorkspaceReviewFileItem{}, false, errors.New("tracked review patch paths do not match inventory")
	}
	for parsedIndex, file := range parsed {
		if !validChatWorkspaceReviewPath(file.Path) || file.Path != layer.Paths[parsedIndex] {
			return ChatWorkspaceReviewFileItem{}, false, errors.New("tracked review patch paths do not match inventory")
		}
	}
	file := parsed[index]
	return ChatWorkspaceReviewFileItem{
		ID:        chatWorkspaceReviewEntryID(kind, file.Path),
		Layer:     kind,
		Path:      file.Path,
		Additions: file.Additions,
		Deletions: file.Deletions,
		Status:    reviewStatusForPath(statuses, kind, file.Path, file.Status),
		Preview:   trackedWorkspacePreview(file.Diff),
	}, true, nil
}

func validChatWorkspaceReviewPath(path string) bool {
	if path == "" || strings.ContainsRune(path, '\x00') || !utf8.ValidString(path) {
		return false
	}
	local := filepath.FromSlash(path)
	return filepath.IsLocal(local) && filepath.ToSlash(filepath.Clean(local)) == path
}

func trackedWorkspacePreview(patch string) ChatWorkspaceReviewPreviewItem {
	if trackedWorkspacePatchHasGitlinkMode(patch) {
		return ChatWorkspaceReviewPreviewItem{Kind: "nested_repository", Reason: "gitlink"}
	}
	if strings.Contains(patch, "\nGIT binary patch\n") || strings.Contains(patch, "\nBinary files ") {
		return ChatWorkspaceReviewPreviewItem{Kind: "binary"}
	}
	if !utf8.ValidString(patch) {
		return ChatWorkspaceReviewPreviewItem{Kind: "binary"}
	}
	if len(patch) > chatWorkspaceReviewMaxPreviewBytesPerFile {
		return ChatWorkspaceReviewPreviewItem{Kind: "too_large", Reason: "file_limit"}
	}
	return ChatWorkspaceReviewPreviewItem{Kind: "text_diff", Content: patch}
}

func trackedWorkspacePatchHasGitlinkMode(patch string) bool {
	for _, line := range strings.Split(patch, "\n") {
		fields := strings.Fields(line)
		switch {
		case strings.HasPrefix(line, "index ") && len(fields) == 3:
			if fields[2] == "160000" {
				return true
			}
		case strings.HasPrefix(line, "old mode "), strings.HasPrefix(line, "new mode "), strings.HasPrefix(line, "new file mode "), strings.HasPrefix(line, "deleted file mode "):
			if len(fields) > 0 && fields[len(fields)-1] == "160000" {
				return true
			}
		}
	}
	return false
}

type chatWorkspaceStablePreviewFS interface {
	OpenStableRegularRead(string) (*os.File, fs.FileInfo, string, error)
	ReopenStableRegularRead(string, fs.FileInfo) (*os.File, fs.FileInfo, error)
}

func projectUntrackedWorkspaceReviewLayer(ctx context.Context, fsys chatWorkspaceStablePreviewFS, entries []gitrunner.ReviewUntrackedEntry, remainingPreviewBytes *int64) ChatWorkspaceReviewLayerItem {
	if remainingPreviewBytes == nil {
		remainingPreviewBytes = new(int64)
	}
	previewCtx, cancel := context.WithTimeout(ctx, chatWorkspaceReviewPreviewReadTimeout)
	defer cancel()
	result := ChatWorkspaceReviewLayerItem{Kind: "untracked", Complete: true, Files: []ChatWorkspaceReviewFileItem{}}
	if len(entries) > chatWorkspaceReviewMaxEntries {
		result.OmittedCount = len(entries) - chatWorkspaceReviewMaxEntries
		entries = entries[:chatWorkspaceReviewMaxEntries]
		result.Complete = false
	}
	if fsys == nil {
		for _, entry := range entries {
			result.Files = append(result.Files, unavailableUntrackedWorkspaceReview(entry, "workspace_unavailable"))
		}
		return result
	}
	for _, entry := range entries {
		item := ChatWorkspaceReviewFileItem{
			ID:      chatWorkspaceReviewEntryID("untracked", entry.Path),
			Layer:   "untracked",
			Path:    entry.Path,
			Status:  "untracked",
			Preview: ChatWorkspaceReviewPreviewItem{Kind: "unavailable", Reason: "read_failed"},
		}
		if entry.Kind == "nested_repository" {
			item.Preview = ChatWorkspaceReviewPreviewItem{Kind: "nested_repository"}
			result.Files = append(result.Files, item)
			continue
		}
		if previewCtx.Err() != nil {
			item.Preview = ChatWorkspaceReviewPreviewItem{Kind: "unavailable", Reason: "read_timeout"}
			result.Files = append(result.Files, item)
			continue
		}
		file, info, _, openErr := fsys.OpenStableRegularRead(entry.Path)
		if openErr != nil {
			switch {
			case errors.Is(openErr, workspacefs.ErrSymlinkComponent):
				item.Preview = ChatWorkspaceReviewPreviewItem{Kind: "symlink"}
			case errors.Is(openErr, workspacefs.ErrNotStableRegularFile):
				item.Preview = ChatWorkspaceReviewPreviewItem{Kind: "special"}
			case errors.Is(openErr, workspacefs.ErrStableReadUnsupported):
				item.Preview = ChatWorkspaceReviewPreviewItem{Kind: "unavailable", Reason: "filesystem_unsupported"}
			}
			result.Files = append(result.Files, item)
			continue
		}
		item.SizeBytes = info.Size()
		remaining := int64(0)
		if remainingPreviewBytes != nil {
			remaining = *remainingPreviewBytes
		}
		if info.Size() > chatWorkspaceReviewMaxPreviewBytesPerFile || info.Size() > remaining {
			_ = file.Close()
			reason := "file_limit"
			if info.Size() > remaining {
				reason = "total_limit"
			}
			item.Preview = ChatWorkspaceReviewPreviewItem{Kind: "too_large", Reason: reason}
			result.Files = append(result.Files, item)
			continue
		}
		data, readErr := readBoundedWorkspaceReviewPreview(previewCtx, file, chatWorkspaceReviewMaxPreviewBytesPerFile+1)
		if errors.Is(readErr, context.Canceled) || errors.Is(readErr, context.DeadlineExceeded) {
			_ = file.Close()
			item.Preview = ChatWorkspaceReviewPreviewItem{Kind: "unavailable", Reason: "read_timeout"}
			result.Files = append(result.Files, item)
			continue
		}
		after, statErr := file.Stat()
		closeErr := file.Close()
		if readErr != nil || statErr != nil || closeErr != nil || int64(len(data)) != info.Size() ||
			!os.SameFile(info, after) || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
			item.Preview = ChatWorkspaceReviewPreviewItem{Kind: "unavailable", Reason: "changed_during_read"}
			result.Files = append(result.Files, item)
			continue
		}
		verifyFile, verifyInfo, verifyOpenErr := fsys.ReopenStableRegularRead(entry.Path, info)
		if verifyOpenErr != nil {
			item.Preview = ChatWorkspaceReviewPreviewItem{Kind: "unavailable", Reason: "changed_during_read"}
			result.Files = append(result.Files, item)
			continue
		}
		verifiedData, verifyReadErr := readBoundedWorkspaceReviewPreview(previewCtx, verifyFile, chatWorkspaceReviewMaxPreviewBytesPerFile+1)
		verifyAfter, verifyStatErr := verifyFile.Stat()
		verifyCloseErr := verifyFile.Close()
		if verifyReadErr != nil || verifyStatErr != nil || verifyCloseErr != nil || !bytes.Equal(data, verifiedData) ||
			!os.SameFile(verifyInfo, verifyAfter) || verifyAfter.Size() != verifyInfo.Size() || !verifyAfter.ModTime().Equal(verifyInfo.ModTime()) {
			item.Preview = ChatWorkspaceReviewPreviewItem{Kind: "unavailable", Reason: "changed_during_read"}
			result.Files = append(result.Files, item)
			continue
		}
		finalFile, _, finalOpenErr := fsys.ReopenStableRegularRead(entry.Path, info)
		finalCloseErr := error(nil)
		if finalFile != nil {
			finalCloseErr = finalFile.Close()
		}
		if finalOpenErr != nil || finalCloseErr != nil {
			item.Preview = ChatWorkspaceReviewPreviewItem{Kind: "unavailable", Reason: "changed_during_read"}
			result.Files = append(result.Files, item)
			continue
		}
		*remainingPreviewBytes -= int64(len(data))
		if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
			item.Preview = ChatWorkspaceReviewPreviewItem{Kind: "binary"}
		} else {
			item.Preview = ChatWorkspaceReviewPreviewItem{Kind: "text", Content: string(data)}
		}
		result.Files = append(result.Files, item)
	}
	return result
}

func readBoundedWorkspaceReviewPreview(ctx context.Context, file *os.File, limit int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(file, limit))
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}

func unavailableUntrackedWorkspaceReview(entry gitrunner.ReviewUntrackedEntry, reason string) ChatWorkspaceReviewFileItem {
	return ChatWorkspaceReviewFileItem{
		ID:      chatWorkspaceReviewEntryID("untracked", entry.Path),
		Layer:   "untracked",
		Path:    entry.Path,
		Status:  "untracked",
		Preview: ChatWorkspaceReviewPreviewItem{Kind: "unavailable", Reason: reason},
	}
}

func indexWorkspaceReviewStatuses(statuses []gitrunner.ReviewStatusEntry) map[string]gitrunner.ReviewStatusEntry {
	indexed := make(map[string]gitrunner.ReviewStatusEntry, len(statuses))
	for _, status := range statuses {
		indexed[status.Path] = status
	}
	return indexed
}

func reviewStatusForPath(statuses map[string]gitrunner.ReviewStatusEntry, layer, path, fallback string) string {
	status, ok := statuses[path]
	if !ok {
		if fallback == "" || fallback == "binary" {
			return "modified"
		}
		return fallback
	}
	if status.Conflict {
		return "conflict"
	}
	code := status.WorktreeStatus
	if layer == "staged" {
		code = status.IndexStatus
	}
	switch code {
	case 'A':
		return "added"
	case 'D':
		return "deleted"
	case 'T':
		return "type_changed"
	case 'R':
		return "renamed"
	case 'C':
		return "copied"
	default:
		return "modified"
	}
}

func reviewPathHasConflict(statuses map[string]gitrunner.ReviewStatusEntry, path string) bool {
	return statuses[path].Conflict
}

func safeLegacyWorkspacePatch(file ChatWorkspaceReviewFileItem) string {
	if file.Preview.Kind == "text_diff" {
		return file.Preview.Content
	}
	return ""
}

func chatWorkspaceReviewEntryID(layer, path string) string {
	sum := sha256.Sum256([]byte("hecate.workspace-review-entry.v1\x00" + layer + "\x00" + path))
	return fmt.Sprintf("review-entry-%x", sum[:16])
}

func (h *Handler) chatWorkspaceGitRunner() chatWorkspaceGitRunner {
	if h.chatWorkspaceGit != nil {
		return h.chatWorkspaceGit
	}
	return gitrunner.NewLocalRunner()
}

func (h *Handler) chatWorkspaceRevertBusy(ctx context.Context, session chat.Session) (bool, string) {
	if h.agentChatLive != nil && h.agentChatLive.hasTurn(session.ID) {
		return true, "running"
	}
	if busy, status := h.hecateAgentSessionBusy(ctx, session); busy {
		return true, status
	}
	if chatWorkspaceActiveStatus(session.Status) {
		return true, session.Status
	}
	for i := len(session.Messages) - 1; i >= 0; i-- {
		if chatWorkspaceActiveStatus(session.Messages[i].Status) {
			return true, session.Messages[i].Status
		}
	}
	return false, ""
}

func chatWorkspaceActiveStatus(status string) bool {
	return chat.IsPotentialWorkspaceOwnerStatus(status)
}

func writeChatWorkspaceRevertBusy(w http.ResponseWriter, status string) {
	WriteErrorDetails(w, http.StatusConflict, errCodeAgentSessionBusy, "workspace changes cannot be discarded while agent work is active", ErrorDetails{
		UserMessage:    "Workspace changes cannot be discarded while the agent is still working.",
		OperatorAction: "Wait for the current turn to finish, resolve its approval, or stop it before discarding changes.",
		Fields: map[string]any{
			"status": strings.TrimSpace(status),
		},
	})
}

func writeChatWorkspaceDiffConflict(w http.ResponseWriter) {
	WriteErrorDetails(w, http.StatusConflict, errCodeConflict, "workspace diff changed after it was reviewed", ErrorDetails{
		UserMessage:    "The workspace changed after this diff was reviewed.",
		OperatorAction: "Refresh the workspace diff, review the latest changes, and confirm discard again.",
	})
}

func (h *Handler) currentChatWorkspaceFiles(ctx context.Context, w http.ResponseWriter, session chat.Session) (ChatWorkspaceFilesItem, bool) {
	if !h.chatWorkspaceLinkReady(ctx, w, session) {
		return ChatWorkspaceFilesItem{}, false
	}
	workspace := strings.TrimSpace(session.Workspace)
	if workspace == "" {
		return ChatWorkspaceFilesItem{Workspace: workspace, Files: []ChatWorkspaceFileItem{}}, true
	}
	fsys, err := workspacefs.NewPinned(workspace)
	if err != nil {
		writeChatWorkspaceFilesCaptureError(w, err)
		return ChatWorkspaceFilesItem{}, false
	}

	statuses := workspaceGitStatus(ctx, h.chatWorkspaceGitRunner(), workspace)
	files, truncated, err := collectChatWorkspaceFileInventory(ctx, fsys, statuses)
	if err != nil {
		writeChatWorkspaceFilesCaptureError(w, err)
		return ChatWorkspaceFilesItem{}, false
	}
	return ChatWorkspaceFilesItem{
		Workspace: workspace,
		Files:     files,
		Truncated: truncated,
	}, true
}

func writeChatWorkspaceFilesCaptureError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, workspacefs.ErrStableReadUnsupported):
		WriteError(w, http.StatusUnprocessableEntity, errCodeInvalidRequest, "workspace file browsing is unavailable on this filesystem")
	case errors.Is(err, workspacefs.ErrNotStableRegularFile), errors.Is(err, workspacefs.ErrSymlinkComponent):
		WriteErrorDetails(w, http.StatusConflict, errCodeConflict, "workspace changed while its file tree was captured", ErrorDetails{
			UserMessage:    "The workspace changed while Hecate was preparing the file tree.",
			OperatorAction: "Refresh Files after the current writer settles.",
		})
	default:
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "workspace file tree could not be inspected safely")
	}
}

type chatWorkspaceTreeWalker interface {
	WalkDirContext(context.Context, string, func(string, string, workspacefs.DirEntry) error) error
}

func collectChatWorkspaceFileInventory(ctx context.Context, fsys chatWorkspaceTreeWalker, statuses map[string]string) ([]ChatWorkspaceFileItem, bool, error) {
	files := make([]ChatWorkspaceFileItem, 0, 256)
	truncated := false
	err := fsys.WalkDirContext(ctx, ".", func(_ string, relPath string, entry workspacefs.DirEntry) error {
		path := filepath.ToSlash(relPath)
		if path == "" || path == "." {
			return nil
		}
		if entry.IsDir && shouldSkipWorkspaceTreeDir(entry.Name) {
			return filepath.SkipDir
		}
		if entry.TraversalBlocked {
			truncated = true
		}
		if len(files) >= chatWorkspaceFilesMaxEntries {
			return errChatWorkspaceFileInventoryLimit
		}
		kind := "file"
		if entry.IsDir {
			kind = "directory"
		} else if entry.TraversalBlocked {
			kind = "unavailable"
		}
		files = append(files, ChatWorkspaceFileItem{
			Path:      path,
			Name:      entry.Name,
			Kind:      kind,
			Status:    statuses[path],
			SizeBytes: entry.Size,
		})
		return nil
	})
	if errors.Is(err, errChatWorkspaceFileInventoryLimit) {
		truncated = true
	} else if err != nil {
		return nil, false, err
	}
	sort.Slice(files, func(i, j int) bool {
		leftDir := files[i].Kind == "directory"
		rightDir := files[j].Kind == "directory"
		if leftDir != rightDir {
			return leftDir
		}
		return files[i].Path < files[j].Path
	})
	return files, truncated, nil
}

func (h *Handler) chatWorkspaceLinkReady(ctx context.Context, w http.ResponseWriter, session chat.Session) bool {
	if !isHecateChatSession(session) || strings.TrimSpace(session.TaskID) != "" {
		return true
	}
	_, taskExists, err := h.hecateChatOriginTask(ctx, session.ID)
	if err != nil {
		h.logger.ErrorContext(context.WithoutCancel(ctx), "chat.workspace.origin_task_lookup_failed", "session_id", session.ID, "error", err)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "failed to verify chat workspace linkage")
		return false
	}
	if !taskExists {
		return true
	}
	WriteErrorDetails(w, http.StatusConflict, errCodeConflict, "chat workspace link is incomplete", ErrorDetails{
		UserMessage:    "Hecate cannot safely identify this chat's managed workspace yet.",
		OperatorAction: "Retry after checking storage health. Do not review or discard source-folder changes as this turn's output.",
	})
	return false
}

func shouldSkipWorkspaceTreeDir(name string) bool {
	switch name {
	case ".git", ".gocache", ".hecate", ".turbo", ".vite", "dist", "node_modules", "target":
		return true
	default:
		return false
	}
}

func workspaceGitStatus(ctx context.Context, runner chatWorkspaceGitRunner, workspace string) map[string]string {
	if runner == nil {
		return nil
	}
	if !runner.IsWorkTree(ctx, workspace) {
		return nil
	}
	status, err := runner.StatusPorcelain(ctx, workspace, agentChatMaxOutputBytes)
	if err != nil {
		return nil
	}
	return parseWorkspaceGitStatus(status)
}

func parseWorkspaceGitStatus(out string) map[string]string {
	statuses := map[string]string{}
	parts := strings.Split(out, "\x00")
	for i := 0; i < len(parts); i++ {
		record := parts[i]
		if len(record) < 4 {
			continue
		}
		code := strings.TrimSpace(record[:2])
		path := record[3:]
		if path == "" {
			continue
		}
		statuses[filepath.ToSlash(path)] = workspaceStatusLabel(code)
		if strings.ContainsAny(code, "RC") && i+1 < len(parts) {
			i++
		}
	}
	return statuses
}

func workspaceStatusLabel(code string) string {
	switch {
	case code == "??":
		return "untracked"
	case strings.Contains(code, "A"):
		return "added"
	case strings.Contains(code, "D"):
		return "deleted"
	case strings.Contains(code, "R"):
		return "renamed"
	case strings.Contains(code, "C"):
		return "copied"
	case strings.Contains(code, "M"):
		return "modified"
	default:
		return "changed"
	}
}

func (h *Handler) loadChatMessage(ctx context.Context, w http.ResponseWriter, r *http.Request) (chat.Session, chat.Message, bool) {
	sessionID := strings.TrimSpace(r.PathValue("id"))
	messageID := strings.TrimSpace(r.PathValue("message_id"))
	if sessionID == "" || messageID == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "session id and message id are required")
		return chat.Session{}, chat.Message{}, false
	}
	session, ok, err := h.agentChat.Get(ctx, sessionID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return chat.Session{}, chat.Message{}, false
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "agent chat session not found")
		return chat.Session{}, chat.Message{}, false
	}
	for _, message := range session.Messages {
		if message.ID == messageID {
			return session, message, true
		}
	}
	WriteError(w, http.StatusNotFound, errCodeNotFound, "agent chat message not found")
	return chat.Session{}, chat.Message{}, false
}

func normalizeRevertPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}

func findChatChangedFile(files []ChatChangedFileItem, path string) (ChatChangedFileItem, bool) {
	if path == "" {
		return ChatChangedFileItem{}, false
	}
	for _, file := range files {
		if file.Path == path {
			return file, true
		}
	}
	return ChatChangedFileItem{}, false
}

func findChatWorkspaceReviewFile(layers []ChatWorkspaceReviewLayerItem, layerKind, path string) (ChatWorkspaceReviewFileItem, bool) {
	if path == "" {
		return ChatWorkspaceReviewFileItem{}, false
	}
	for _, layer := range layers {
		if layer.Kind != layerKind {
			continue
		}
		for _, file := range layer.Files {
			if file.Path == path {
				return file, true
			}
		}
		return ChatWorkspaceReviewFileItem{}, false
	}
	return ChatWorkspaceReviewFileItem{}, false
}

func renderChatChangedFile(file chat.ChangedFile) ChatChangedFileItem {
	return ChatChangedFileItem{
		Path:      file.Path,
		Additions: file.Additions,
		Deletions: file.Deletions,
		Status:    file.Status,
	}
}
