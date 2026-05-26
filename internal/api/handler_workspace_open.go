package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
)

type workspaceOpenTarget string

const (
	workspaceOpenVSCode         workspaceOpenTarget = "vscode"
	workspaceOpenVSCodeInsiders workspaceOpenTarget = "vscode_insiders"
	workspaceOpenCursor         workspaceOpenTarget = "cursor"
	workspaceOpenZed            workspaceOpenTarget = "zed"
	workspaceOpenFinder         workspaceOpenTarget = "finder"
	workspaceOpenTerminal       workspaceOpenTarget = "terminal"
	workspaceOpenITerm2         workspaceOpenTarget = "iterm2"
	workspaceOpenXcode          workspaceOpenTarget = "xcode"
)

type workspaceOpenLauncher func(path string, target workspaceOpenTarget) error

var launchWorkspaceOpenTarget workspaceOpenLauncher = openWorkspaceTargetPath

func (h *Handler) HandleWorkspaceOpen(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRemoteAddr(r.RemoteAddr) {
		WriteError(w, http.StatusForbidden, errCodeInvalidRequest, "workspace open is only available to local loopback clients")
		return
	}
	var req WorkspaceOpenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "invalid JSON body")
		return
	}
	path, target, err := validateWorkspaceOpenRequest(req.Path, req.Target)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	if err := launchWorkspaceOpenTarget(path, target); err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, WorkspaceOpenResponse{
		Object: "workspace_open",
		Data: WorkspaceOpenResponseItem{
			Path:   path,
			Target: string(target),
		},
	})
}

func isLoopbackRemoteAddr(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err != nil {
		host = strings.TrimSpace(remoteAddr)
	}
	if host == "" {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validateWorkspaceOpenRequest(path, target string) (string, workspaceOpenTarget, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", "", errors.New("workspace path is required")
	}
	resolved, err := canonicalWorkspaceDialogPath(path)
	if err != nil {
		return "", "", err
	}
	parsed, err := parseWorkspaceOpenTarget(target)
	if err != nil {
		return "", "", err
	}
	return resolved, parsed, nil
}

func parseWorkspaceOpenTarget(target string) (workspaceOpenTarget, error) {
	switch workspaceOpenTarget(strings.TrimSpace(target)) {
	case workspaceOpenVSCode:
		return workspaceOpenVSCode, nil
	case workspaceOpenVSCodeInsiders:
		return workspaceOpenVSCodeInsiders, nil
	case workspaceOpenCursor:
		return workspaceOpenCursor, nil
	case workspaceOpenZed:
		return workspaceOpenZed, nil
	case workspaceOpenFinder:
		return workspaceOpenFinder, nil
	case workspaceOpenTerminal:
		return workspaceOpenTerminal, nil
	case workspaceOpenITerm2:
		return workspaceOpenITerm2, nil
	case workspaceOpenXcode:
		return workspaceOpenXcode, nil
	default:
		return "", fmt.Errorf("unknown workspace open target %q", target)
	}
}

func openWorkspaceTargetPath(path string, target workspaceOpenTarget) error {
	switch target {
	case workspaceOpenFinder:
		return openWorkspacePath(path)
	case workspaceOpenTerminal:
		return openWorkspaceTerminal(path)
	case workspaceOpenITerm2:
		if runtime.GOOS != "darwin" {
			return errors.New("iTerm2 is only available on macOS")
		}
		return openWorkspaceWithApp(path, "iTerm")
	case workspaceOpenXcode:
		if runtime.GOOS != "darwin" {
			return errors.New("Xcode is only available on macOS")
		}
		return openWorkspaceWithApp(path, "Xcode")
	case workspaceOpenVSCode:
		if runtime.GOOS == "darwin" {
			return openWorkspaceWithApp(path, "Visual Studio Code")
		}
		return spawnWorkspaceOpenCommand("code", path)
	case workspaceOpenVSCodeInsiders:
		if runtime.GOOS == "darwin" {
			return openWorkspaceWithApp(path, "Visual Studio Code - Insiders")
		}
		return spawnWorkspaceOpenCommand("code-insiders", path)
	case workspaceOpenCursor:
		if runtime.GOOS == "darwin" {
			return openWorkspaceWithApp(path, "Cursor")
		}
		return spawnWorkspaceOpenCommand("cursor", path)
	case workspaceOpenZed:
		if runtime.GOOS == "darwin" {
			return openWorkspaceWithApp(path, "Zed")
		}
		return spawnWorkspaceOpenCommand("zed", path)
	default:
		return fmt.Errorf("unknown workspace open target %q", target)
	}
}

func openWorkspacePath(path string) error {
	switch runtime.GOOS {
	case "darwin":
		return spawnWorkspaceOpenCommand("open", path)
	case "windows":
		return spawnWorkspaceOpenCommand("explorer", path)
	default:
		return spawnWorkspaceOpenCommand("xdg-open", path)
	}
}

func openWorkspaceWithApp(path, appName string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("%s is only available on macOS", appName)
	}
	return spawnWorkspaceOpenCommand("open", "-a", appName, path)
}

func openWorkspaceTerminal(path string) error {
	switch runtime.GOOS {
	case "darwin":
		return openWorkspaceWithApp(path, "Terminal")
	case "windows":
		return spawnWorkspaceOpenCommand("cmd", "/C", "start", "", "wt", "-d", path)
	default:
		attempts := []struct {
			command string
			args    []string
		}{
			{command: "x-terminal-emulator", args: []string{"--working-directory", path}},
			{command: "gnome-terminal", args: []string{"--working-directory", path}},
			{command: "konsole", args: []string{"--workdir", path}},
			{command: "xfce4-terminal", args: []string{"--working-directory", path}},
		}
		var lastErr error
		for _, attempt := range attempts {
			if err := spawnWorkspaceOpenCommand(attempt.command, attempt.args...); err == nil {
				return nil
			} else {
				lastErr = err
			}
		}
		if lastErr != nil {
			return fmt.Errorf("failed to open workspace in Terminal: %w", lastErr)
		}
		return errors.New("failed to open workspace in Terminal: no terminal command configured")
	}
}

func spawnWorkspaceOpenCommand(command string, args ...string) error {
	if err := exec.Command(command, args...).Start(); err != nil {
		return fmt.Errorf("failed to open workspace with %s: %w", command, err)
	}
	return nil
}
