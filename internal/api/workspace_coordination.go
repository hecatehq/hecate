package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/internal/workspacecoord"
	"github.com/hecatehq/hecate/pkg/types"
)

const workspaceOwnerScanPageSize = 200

func (h *Handler) acquireWorkspaceWriter(w http.ResponseWriter, ctx context.Context, workspace string) (*workspacecoord.WriterLease, bool) {
	if h == nil || h.workspaceCoordinator == nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "workspace coordination is unavailable")
		return nil, false
	}
	lease, err := h.workspaceCoordinator.AcquireWriter(ctx, workspace)
	if err == nil {
		return lease, true
	}
	if errors.Is(err, workspacecoord.ErrClosed) {
		writeWorkspaceMutationConflict(w)
		return nil, false
	}
	if ctx != nil && ctx.Err() != nil {
		WriteError(w, http.StatusConflict, errCodeConflict, "workspace operation was cancelled before admission")
		return nil, false
	}
	WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "failed to coordinate workspace operation")
	return nil, false
}

func (h *Handler) closeWorkspaceForRevert(w http.ResponseWriter, ctx context.Context, workspace string) (*workspacecoord.ExclusiveLease, bool) {
	if h == nil || h.workspaceCoordinator == nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "workspace coordination is unavailable")
		return nil, false
	}
	lease, err := h.workspaceCoordinator.TryClose(ctx, workspace)
	if err == nil {
		return lease, true
	}
	if errors.Is(err, workspacecoord.ErrBusy) || errors.Is(err, workspacecoord.ErrClosed) {
		writeChatWorkspaceRevertBusy(w, "workspace_active")
		return nil, false
	}
	if ctx != nil && ctx.Err() != nil {
		writeChatWorkspaceRevertBusy(w, "cancelled")
		return nil, false
	}
	WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "failed to coordinate workspace discard")
	return nil, false
}

func writeWorkspaceMutationConflict(w http.ResponseWriter) {
	WriteErrorDetails(w, http.StatusConflict, errCodeConflict, "workspace is temporarily closed for a destructive operation", ErrorDetails{
		UserMessage:    "Another workspace operation is finishing.",
		OperatorAction: "Wait for it to finish, refresh the workspace state, and try again.",
	})
}

// chatWorkspaceDurableOwner checks persisted owners while an exclusive
// workspace lease blocks every in-process run or mutation admission. The scan
// closes the gap between a short run-creation admission and later execution,
// including queued work recovered from durable storage after a restart.
func (h *Handler) chatWorkspaceDurableOwner(ctx context.Context, workspaceKey, currentSessionID string) (string, bool, error) {
	if h == nil || h.taskStore == nil {
		return "", false, errors.New("task store is unavailable")
	}
	afterID := ""
	for {
		runs, err := h.taskStore.ListRunsByFilter(ctx, taskstate.RunFilter{
			Limit:     workspaceOwnerScanPageSize,
			OrderByID: true,
			AfterID:   afterID,
		})
		if err != nil {
			return "", false, fmt.Errorf("list task runs: %w", err)
		}
		for _, run := range runs {
			if types.IsTerminalTaskRunStatus(strings.TrimSpace(run.Status)) {
				continue
			}
			matches, err := workspacePathOverlapsCanonical(run.WorkspacePath, workspaceKey)
			if err != nil {
				return "", false, fmt.Errorf("verify task workspace: %w", err)
			}
			if matches {
				return strings.TrimSpace(run.Status), true, nil
			}
		}
		if len(runs) < workspaceOwnerScanPageSize {
			break
		}
		nextID := strings.TrimSpace(runs[len(runs)-1].ID)
		if nextID == "" || nextID <= afterID {
			return "", false, errors.New("task run scan did not advance")
		}
		afterID = nextID
	}

	if h.agentChat == nil {
		return "", false, errors.New("chat store is unavailable")
	}
	sessions, err := h.agentChat.List(ctx)
	if err != nil {
		return "", false, fmt.Errorf("list chat sessions: %w", err)
	}
	for _, session := range sessions {
		if strings.TrimSpace(session.ID) == strings.TrimSpace(currentSessionID) {
			continue
		}
		matches, err := workspacePathOverlapsCanonical(session.Workspace, workspaceKey)
		if err != nil {
			return "", false, fmt.Errorf("verify chat workspace: %w", err)
		}
		if !matches {
			continue
		}
		if chatWorkspaceActiveStatus(session.Status) {
			return strings.TrimSpace(session.Status), true, nil
		}
		for i := len(session.Messages) - 1; i >= 0; i-- {
			if chatWorkspaceActiveStatus(session.Messages[i].Status) {
				return strings.TrimSpace(session.Messages[i].Status), true, nil
			}
		}
	}
	return "", false, nil
}

func workspacePathOverlapsCanonical(candidate, workspaceKey string) (bool, error) {
	candidate = strings.TrimSpace(candidate)
	workspaceKey = strings.TrimSpace(workspaceKey)
	if candidate == "" || workspaceKey == "" {
		return false, nil
	}
	canonical, err := workspacecoord.CanonicalWorkspace(candidate)
	if err == nil {
		return workspacecoord.CanonicalKeysOverlap(canonical, workspaceKey), nil
	}
	// A vanished unrelated workspace must not wedge every discard forever. A
	// lexical match to the protected path is different: identity cannot be
	// disproved, so fail closed and ask the operator to retry after recovery.
	abs, absErr := filepath.Abs(candidate)
	if absErr == nil && workspacecoord.CanonicalKeysOverlap(filepath.Clean(abs), workspaceKey) {
		return false, err
	}
	return false, nil
}
