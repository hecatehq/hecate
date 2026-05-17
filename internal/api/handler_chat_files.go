package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/hecate/agent-runtime/internal/chat"
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

func ensureGitWorkspace(ctx context.Context, workspace string) error {
	out, err := runGit(ctx, workspace, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(out) != "true" {
		return errors.New("agent chat revert requires a git workspace")
	}
	return nil
}

func runGit(ctx context.Context, workspace string, args ...string) (string, error) {
	if strings.TrimSpace(workspace) == "" {
		return "", errors.New("workspace is required")
	}
	cmdArgs := append([]string{"-C", workspace}, args...)
	out, err := exec.CommandContext(ctx, "git", cmdArgs...).CombinedOutput()
	return string(out), err
}

func renderChatChangedFile(file chat.ChangedFile) ChatChangedFileItem {
	return ChatChangedFileItem{
		Path:      file.Path,
		Additions: file.Additions,
		Deletions: file.Deletions,
		Status:    file.Status,
	}
}
