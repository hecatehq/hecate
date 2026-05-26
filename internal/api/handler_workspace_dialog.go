package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ncruces/zenity"
)

const workspaceDialogTimeout = 2 * time.Minute

var workspaceDialogActive atomic.Bool

func (h *Handler) HandleWorkspaceDialog(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRemoteAddr(r.RemoteAddr) {
		WriteError(w, http.StatusForbidden, errCodeInvalidRequest, "workspace folder dialog is only available to local loopback clients")
		return
	}
	if hasForwardedClientHeaders(r) {
		WriteError(w, http.StatusForbidden, errCodeInvalidRequest, "workspace folder dialog rejects forwarded client headers")
		return
	}
	path, err := chooseWorkspaceDirectory(r.Context())
	if err != nil {
		status := http.StatusBadRequest
		code := errCodeInvalidRequest
		if errors.Is(err, errWorkspaceDialogUnsupported) {
			status = http.StatusNotImplemented
		}
		if errors.Is(err, errWorkspaceDialogAlreadyOpen) {
			status = http.StatusConflict
			code = errCodeConflict
		}
		WriteError(w, status, code, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, WorkspaceDialogResponse{
		Object: "workspace_dialog",
		Data: WorkspaceDialogResponseItem{
			Path:   path,
			Branch: workspaceGitBranch(path),
		},
	})
}

func chooseWorkspaceDirectory(ctx context.Context) (string, error) {
	return chooseWorkspaceDirectorySingleFlight(ctx, selectWorkspaceDirectory, &workspaceDialogActive)
}

type workspaceDirectoryPicker func(context.Context) (string, error)

var selectWorkspaceDirectory workspaceDirectoryPicker = func(ctx context.Context) (string, error) {
	if !zenity.IsAvailable() {
		return "", errWorkspaceDialogUnsupported
	}
	return zenity.SelectFile(
		zenity.Context(ctx),
		zenity.Directory(),
		zenity.Title("Choose a workspace for Hecate Chat"),
	)
}

func chooseWorkspaceDirectorySingleFlight(ctx context.Context, picker workspaceDirectoryPicker, active *atomic.Bool) (string, error) {
	if !active.CompareAndSwap(false, true) {
		return "", errWorkspaceDialogAlreadyOpen
	}
	defer active.Store(false)
	return chooseWorkspaceDirectoryWithPicker(ctx, picker)
}

func chooseWorkspaceDirectoryWithPicker(ctx context.Context, picker workspaceDirectoryPicker) (string, error) {
	dialogCtx, cancel := context.WithTimeout(ctx, workspaceDialogTimeout)
	defer cancel()
	path, err := picker(dialogCtx)
	if errors.Is(err, zenity.ErrCanceled) {
		return "", nil
	}
	if errors.Is(err, zenity.ErrUnsupported) {
		return "", errWorkspaceDialogUnsupported
	}
	if err != nil {
		return "", err
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	cleaned, err := canonicalWorkspaceDialogPath(path)
	if err != nil {
		return "", err
	}
	return cleaned, nil
}

type workspaceDialogUnsupportedError struct{}

func (workspaceDialogUnsupportedError) Error() string {
	return "workspace folder dialog is not available on this system"
}

var errWorkspaceDialogUnsupported error = workspaceDialogUnsupportedError{}

type workspaceDialogAlreadyOpenError struct{}

func (workspaceDialogAlreadyOpenError) Error() string {
	return "workspace folder dialog is already open"
}

var errWorkspaceDialogAlreadyOpen error = workspaceDialogAlreadyOpenError{}

func canonicalWorkspaceDialogPath(path string) (string, error) {
	path = filepath.Clean(path)
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("selected workspace is not a directory")
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return abs, nil
}
