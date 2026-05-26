package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/gitrunner"
)

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
	path := strings.TrimSpace(r.PathValue("path"))
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

func (h *Handler) HandleChatWorkspaceFileDiff(w http.ResponseWriter, r *http.Request) {
	session, ok := h.loadChatSession(r.Context(), w, r)
	if !ok {
		return
	}
	item, ok := h.currentChatWorkspaceDiff(r.Context(), w, session)
	if !ok {
		return
	}
	path := strings.TrimSpace(r.PathValue("path"))
	file, found := findChatChangedFile(item.Files, path)
	if !found {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "workspace changed file not found")
		return
	}
	diff, err := runGit(r.Context(), session.Workspace, "diff", "--no-ext-diff", "--binary", "--", file.Path)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, strings.TrimSpace(diff))
		return
	}
	parsed, parsedFound := chat.ExtractFileDiff(strings.TrimSpace(diff), file.Path)
	if parsedFound {
		file.Additions = parsed.Additions
		file.Deletions = parsed.Deletions
		file.Status = parsed.Status
	}
	WriteJSON(w, http.StatusOK, ChatChangedFileDiffResponse{
		Object: "chat_workspace_file_diff",
		Data: ChatChangedFileDiffItem{
			Path:      file.Path,
			Additions: file.Additions,
			Deletions: file.Deletions,
			Status:    file.Status,
			Diff:      strings.TrimSpace(diff),
		},
	})
}

func (h *Handler) HandleRevertChatWorkspaceFiles(w http.ResponseWriter, r *http.Request) {
	session, ok := h.loadChatSession(r.Context(), w, r)
	if !ok {
		return
	}
	item, ok := h.currentChatWorkspaceDiff(r.Context(), w, session)
	if !ok {
		return
	}
	if len(item.Files) == 0 {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "workspace has no current git diff")
		return
	}
	var req RevertChatMessageFilesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "invalid JSON body")
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
	result, err := gitrunner.NewLocalRunner().Restore(r.Context(), session.Workspace, paths)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, strings.TrimSpace(combinedProcessOutput(result)))
		return
	}
	next, ok := h.currentChatWorkspaceDiff(r.Context(), w, session)
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
	workspace := strings.TrimSpace(session.Workspace)
	if workspace == "" {
		return ChatWorkspaceDiffItem{Workspace: workspace, Files: []ChatChangedFileItem{}}, true
	}
	runner := gitrunner.NewLocalRunner()
	if !runner.IsWorkTree(ctx, workspace) {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "chat workspace is not a git worktree")
		return ChatWorkspaceDiffItem{}, false
	}
	diffStat, diff := runner.Diff(ctx, workspace, agentChatMaxOutputBytes)
	files := chat.ParseChangedFiles(diff, diffStat)
	items := make([]ChatChangedFileItem, 0, len(files))
	for _, file := range files {
		items = append(items, renderChatChangedFile(file))
	}
	return ChatWorkspaceDiffItem{
		Workspace:  workspace,
		DiffStat:   diffStat,
		Diff:       diff,
		HasChanges: strings.TrimSpace(diffStat) != "" || strings.TrimSpace(diff) != "",
		Files:      items,
	}, true
}

func (h *Handler) HandleRevertChatMessageFiles(w http.ResponseWriter, r *http.Request) {
	session, message, ok := h.loadChatMessage(r.Context(), w, r)
	if !ok {
		return
	}
	files := chat.ParseChangedFiles(message.Diff, message.DiffStat)
	if len(files) == 0 {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "agent chat message has no captured diff")
		return
	}
	var req RevertChatMessageFilesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "invalid JSON body")
		return
	}
	allowed := make(map[string]struct{}, len(files))
	allPaths := make([]string, 0, len(files))
	for _, file := range files {
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
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "path is not present in the captured agent diff: "+path)
			return
		}
	}
	if err := ensureGitWorkspace(r.Context(), session.Workspace); err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	if out, err := runGit(r.Context(), session.Workspace, append([]string{"restore", "--"}, paths...)...); err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, strings.TrimSpace(out))
		return
	}
	diffStat, err := runGit(r.Context(), session.Workspace, append([]string{"diff", "--stat", "--"}, allPaths...)...)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, strings.TrimSpace(diffStat))
		return
	}
	diff, err := runGit(r.Context(), session.Workspace, append([]string{"diff", "--binary", "--"}, allPaths...)...)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, strings.TrimSpace(diff))
		return
	}
	now := time.Now().UTC()
	updated, err := h.agentChat.UpdateMessage(r.Context(), session.ID, message.ID, func(item *chat.Message) {
		item.DiffStat = strings.TrimSpace(diffStat)
		item.Diff = strings.TrimSpace(diff)
		item.Activities = append(item.Activities, chat.Activity{
			Type:      "files_reverted",
			Status:    "completed",
			Title:     "Files reverted",
			Detail:    strings.Join(paths, "\n"),
			CreatedAt: now,
		})
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	h.agentChatLive.publishSession(updated)
	remaining := chat.ParseChangedFiles(strings.TrimSpace(diff), strings.TrimSpace(diffStat))
	items := make([]ChatChangedFileItem, 0, len(remaining))
	for _, file := range remaining {
		items = append(items, renderChatChangedFile(file))
	}
	WriteJSON(w, http.StatusOK, ChatRevertResponse{
		Object: "chat_revert",
		Data: ChatRevertItem{
			Reverted: true,
			Paths:    paths,
			DiffStat: strings.TrimSpace(diffStat),
			Files:    items,
		},
	})
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
		path = strings.TrimSpace(path)
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
	path = strings.TrimSpace(path)
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

func ensureGitWorkspace(ctx context.Context, workspace string) error {
	if !gitrunner.NewLocalRunner().IsWorkTree(ctx, workspace) {
		return errors.New("agent chat revert requires a git workspace")
	}
	return nil
}

func runGit(ctx context.Context, workspace string, args ...string) (string, error) {
	if strings.TrimSpace(workspace) == "" {
		return "", errors.New("workspace is required")
	}
	result, err := gitrunner.NewLocalRunner().Run(ctx, workspace, args...)
	return combinedProcessOutput(result), err
}

func combinedProcessOutput(result gitrunner.Result) string {
	out := strings.TrimSpace(result.Stdout)
	errText := strings.TrimSpace(result.Stderr)
	switch {
	case out != "" && errText != "":
		return out + "\n" + errText
	case out != "":
		return out
	default:
		return errText
	}
}

func renderChatChangedFile(file chat.ChangedFile) ChatChangedFileItem {
	return ChatChangedFileItem{
		Path:      file.Path,
		Additions: file.Additions,
		Deletions: file.Deletions,
		Status:    file.Status,
	}
}
