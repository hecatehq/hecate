package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/gitrunner"
	"github.com/hecatehq/hecate/internal/workspacefs"
)

const chatWorkspaceFilesMaxEntries = 5000

type chatWorkspaceGitRunner interface {
	IsWorkTree(context.Context, string) bool
	SnapshotDiff(context.Context, string, int64) (gitrunner.DiffSnapshot, error)
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
	session, ok := h.loadChatSession(r.Context(), w, r)
	if !ok {
		return
	}
	item, ok := h.currentChatWorkspaceDiff(r.Context(), w, session)
	if !ok {
		return
	}
	path := r.PathValue("path")
	file, found := findChatChangedFile(item.Files, path)
	if !found {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "workspace changed file not found")
		return
	}
	parsed, parsedFound := chat.ExtractFileDiff(item.Diff, file.Path)
	if !parsedFound {
		WriteError(w, http.StatusConflict, errCodeConflict, "workspace diff changed before the selected file could be rendered")
		return
	}
	file.Additions = parsed.Additions
	file.Deletions = parsed.Deletions
	file.Status = parsed.Status
	WriteJSON(w, http.StatusOK, ChatChangedFileDiffResponse{
		Object: "chat_workspace_file_diff",
		Data: ChatChangedFileDiffItem{
			Path:      file.Path,
			Additions: file.Additions,
			Deletions: file.Deletions,
			Status:    file.Status,
			Diff:      parsed.Diff,
		},
	})
}

func (h *Handler) HandleRevertChatWorkspaceFiles(w http.ResponseWriter, r *http.Request) {
	session, ok := h.loadChatSession(r.Context(), w, r)
	if !ok {
		return
	}
	var req RevertChatWorkspaceFilesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "invalid JSON body")
		return
	}
	expectedRevision := strings.TrimSpace(req.ExpectedRevision)
	if expectedRevision == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "expected_revision is required")
		return
	}
	if busy, status := h.chatWorkspaceRevertBusy(r.Context(), session); busy {
		writeChatWorkspaceRevertBusy(w, status)
		return
	}
	item, ok := h.currentChatWorkspaceDiff(r.Context(), w, session)
	if !ok {
		return
	}
	if expectedRevision != item.Revision {
		writeChatWorkspaceDiffConflict(w)
		return
	}
	if len(item.Files) == 0 {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "workspace has no current git diff")
		return
	}
	allowed := make(map[string]struct{}, len(item.Files))
	allPaths := make([]string, 0, len(item.Files))
	for _, file := range item.Files {
		if file.Path == "" {
			continue
		}
		allowed[file.Path] = struct{}{}
		allPaths = append(allPaths, file.Path)
	}
	paths := normalizeRevertPaths(req.Paths)
	if len(paths) == 0 {
		paths = append(paths, allPaths...)
	}
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

	// Re-read every durable owner after both admission domains are closed. A
	// browser confirmation is not authority to discard edits while another chat
	// or Task has queued, running, or approval-blocked work for the same path.
	latestSession, ok := h.loadChatSession(r.Context(), w, r)
	if !ok {
		return
	}
	if latestSession.Workspace != session.Workspace {
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
	current, ok := h.currentChatWorkspaceDiff(r.Context(), w, latestSession)
	if !ok {
		return
	}
	if expectedRevision != current.Revision {
		writeChatWorkspaceDiffConflict(w)
		return
	}
	snapshot := gitrunner.DiffSnapshot{
		Diff:     current.Diff,
		Revision: current.Revision,
	}
	_, err := h.chatWorkspaceGitRunner().ReverseApplySnapshot(r.Context(), latestSession.Workspace, snapshot, paths)
	if errors.Is(err, gitrunner.ErrDiffSnapshotNotApplicable) {
		writeChatWorkspaceDiffConflict(w)
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "failed to discard the selected workspace changes")
		return
	}
	next, ok := h.currentChatWorkspaceDiff(r.Context(), w, latestSession)
	if !ok {
		return
	}
	WriteJSON(w, http.StatusOK, ChatWorkspaceDiffResponse{
		Object: "chat_workspace_diff",
		Data:   next,
	})
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
	if !h.chatWorkspaceLinkReady(ctx, w, session) {
		return ChatWorkspaceDiffItem{}, false
	}
	workspace := strings.TrimSpace(session.Workspace)
	if workspace == "" {
		return ChatWorkspaceDiffItem{
			Workspace: workspace,
			Revision:  gitrunner.DiffRevision(""),
			Files:     []ChatChangedFileItem{},
		}, true
	}
	runner := h.chatWorkspaceGitRunner()
	if !runner.IsWorkTree(ctx, workspace) {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "chat workspace is not a git worktree")
		return ChatWorkspaceDiffItem{}, false
	}
	snapshot, err := runner.SnapshotDiff(ctx, workspace, agentChatMaxOutputBytes)
	if errors.Is(err, gitrunner.ErrDiffSnapshotTooLarge) {
		WriteError(w, http.StatusUnprocessableEntity, errCodeInvalidRequest, "workspace diff is too large to review and revert safely")
		return ChatWorkspaceDiffItem{}, false
	}
	if errors.Is(err, gitrunner.ErrStagedChangesUnsupported) {
		WriteErrorDetails(w, http.StatusUnprocessableEntity, errCodeInvalidRequest, "staged workspace changes cannot be reviewed and discarded safely", ErrorDetails{
			UserMessage:    "This workspace has staged Git changes.",
			OperatorAction: "Unstage the changes, refresh the workspace diff, and review discard again.",
		})
		return ChatWorkspaceDiffItem{}, false
	}
	if errors.Is(err, gitrunner.ErrDiffSnapshotInvalid) {
		WriteError(w, http.StatusUnprocessableEntity, errCodeInvalidRequest, "workspace diff paths cannot be represented safely for review and discard")
		return ChatWorkspaceDiffItem{}, false
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return ChatWorkspaceDiffItem{}, false
	}
	// DiffStat is presentation-only because it is captured by a separate Git
	// process. Only paths parsed from the exact patch covered by Revision may
	// become restore authority.
	files := chat.ParseChangedFiles(snapshot.Diff, "")
	parsedPaths := make([]string, 0, len(files))
	for _, file := range files {
		if !utf8.ValidString(file.Path) || strings.ContainsRune(file.Path, '\x00') {
			WriteError(w, http.StatusUnprocessableEntity, errCodeInvalidRequest, "workspace diff paths cannot be represented safely for review and discard")
			return ChatWorkspaceDiffItem{}, false
		}
		parsedPaths = append(parsedPaths, file.Path)
	}
	if !slices.Equal(parsedPaths, snapshot.Paths) {
		WriteError(w, http.StatusUnprocessableEntity, errCodeInvalidRequest, "workspace diff paths cannot be represented safely for review and discard")
		return ChatWorkspaceDiffItem{}, false
	}
	items := make([]ChatChangedFileItem, 0, len(files))
	for _, file := range files {
		items = append(items, renderChatChangedFile(file))
	}
	diffStat := snapshot.Stat
	hasChanges := strings.TrimSpace(snapshot.Diff) != ""
	if !hasChanges {
		// A stat-only result can only be an observation from before the exact
		// patch capture. Do not surface it as part of the authoritative snapshot.
		diffStat = ""
	}
	return ChatWorkspaceDiffItem{
		Workspace:  workspace,
		Revision:   snapshot.Revision,
		DiffStat:   diffStat,
		Diff:       snapshot.Diff,
		HasChanges: hasChanges,
		Files:      items,
	}, true
}

func (h *Handler) chatWorkspaceGitRunner() chatWorkspaceGitRunner {
	if h.chatWorkspaceGit != nil {
		return h.chatWorkspaceGit
	}
	return gitrunner.NewLocalRunner()
}

func (h *Handler) chatWorkspaceRevertBusy(ctx context.Context, session chat.Session) (bool, string) {
	if h.agentChatLive != nil && h.agentChatLive.hasRun(session.ID) {
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
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pending", "queued", "running", "in_progress", "awaiting_approval":
		return true
	default:
		return false
	}
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
	fsys, err := workspacefs.New(workspace)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return ChatWorkspaceFilesItem{}, false
	}

	statuses := workspaceGitStatus(ctx, h.chatWorkspaceGitRunner(), workspace)
	files := make([]ChatWorkspaceFileItem, 0, 256)
	truncated := false
	err = fsys.WalkDir(".", func(_ string, relPath string, entry workspacefs.DirEntry) error {
		path := filepath.ToSlash(relPath)
		if path == "" || path == "." {
			return nil
		}
		if entry.IsDir && shouldSkipWorkspaceTreeDir(entry.Name) {
			return filepath.SkipDir
		}
		if len(files) >= chatWorkspaceFilesMaxEntries {
			truncated = true
			if entry.IsDir {
				return filepath.SkipDir
			}
			return nil
		}
		kind := "file"
		if entry.IsDir {
			kind = "directory"
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
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return ChatWorkspaceFilesItem{}, false
	}
	sort.Slice(files, func(i, j int) bool {
		leftDir := files[i].Kind == "directory"
		rightDir := files[j].Kind == "directory"
		if leftDir != rightDir {
			return leftDir
		}
		return files[i].Path < files[j].Path
	})
	return ChatWorkspaceFilesItem{
		Workspace: workspace,
		Files:     files,
		Truncated: truncated,
	}, true
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
		OperatorAction: "Retry after checking storage health. Do not review or discard source-folder changes as this run's output.",
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

func renderChatChangedFile(file chat.ChangedFile) ChatChangedFileItem {
	return ChatChangedFileItem{
		Path:      file.Path,
		Additions: file.Additions,
		Deletions: file.Deletions,
		Status:    file.Status,
	}
}
