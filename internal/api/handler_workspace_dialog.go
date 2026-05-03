package api

import (
	"context"
	"errors"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const workspaceDialogTimeout = 2 * time.Minute

func (h *Handler) HandleWorkspaceDialog(w http.ResponseWriter, r *http.Request) {
	path, err := chooseWorkspaceDirectory(r.Context())
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errWorkspaceDialogUnsupported) {
			status = http.StatusNotImplemented
		}
		WriteError(w, status, errCodeInvalidRequest, err.Error())
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
	switch runtime.GOOS {
	case "darwin":
		dialogCtx, cancel := context.WithTimeout(ctx, workspaceDialogTimeout)
		defer cancel()
		out, err := exec.CommandContext(
			dialogCtx,
			"osascript",
			"-e",
			`POSIX path of (choose folder with prompt "Choose a workspace for Hecate Agent Chat")`,
		).Output()
		if err != nil {
			return "", err
		}
		path := strings.TrimSpace(string(out))
		if path == "" {
			return "", errors.New("no workspace folder was selected")
		}
		return strings.TrimSuffix(path, "/"), nil
	default:
		return "", errWorkspaceDialogUnsupported
	}
}

type workspaceDialogUnsupportedError struct{}

func (workspaceDialogUnsupportedError) Error() string {
	return "workspace folder dialog is only available on macOS right now"
}

var errWorkspaceDialogUnsupported error = workspaceDialogUnsupportedError{}
