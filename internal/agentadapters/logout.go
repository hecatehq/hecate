package agentadapters

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

const LogoutStatusLoggedOut = "logged_out"

type LogoutResult struct {
	AdapterID  string `json:"adapter_id"`
	Status     string `json:"status"`
	Path       string `json:"path,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

func Logout(ctx context.Context, adapterID string) (LogoutResult, error) {
	start := time.Now()
	adapter, ok := BuiltInByID(adapterID)
	if !ok {
		return LogoutResult{}, fmt.Errorf("agent adapter %q not found", strings.TrimSpace(adapterID))
	}
	res := LogoutResult{AdapterID: adapter.ID}

	path, err := resolveExecutable(adapter, exec.LookPath)
	if err != nil {
		res.DurationMS = elapsedMS(start)
		return res, err
	}
	res.Path = path

	logoutCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	workspace, err := os.MkdirTemp("", "hecate-adapter-logout-*")
	if err != nil {
		res.DurationMS = elapsedMS(start)
		return res, fmt.Errorf("create logout workspace: %w", err)
	}
	defer func() { _ = os.RemoveAll(workspace) }()

	processEnv, err := prepareAdapterProcessEnv(ctx, adapter, os.Environ())
	if err != nil {
		res.DurationMS = elapsedMS(start)
		return res, err
	}
	if processEnv.cleanup != nil {
		defer processEnv.cleanup()
	}

	cmd := exec.CommandContext(context.Background(), path, append([]string(nil), adapter.Args...)...)
	configureCommandProcessGroup(cmd)
	cmd.Dir = workspace
	cmd.Env = processEnv.values

	stdin, err := cmd.StdinPipe()
	if err != nil {
		res.DurationMS = elapsedMS(start)
		return res, fmt.Errorf("create ACP stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		res.DurationMS = elapsedMS(start)
		return res, fmt.Errorf("create ACP stdout pipe: %w", err)
	}
	var stderr limitedBuffer
	stderr.limit = 256 * 1024
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		res.DurationMS = elapsedMS(start)
		return res, fmt.Errorf("start ACP adapter %q: %w", adapter.ID, err)
	}
	processTerminated := false
	cleanupProcess := func() {
		if processTerminated {
			return
		}
		processTerminated = true
		terminateProcess(cmd)
	}
	defer cleanupProcess()

	conn := acp.NewClientSideConnection(probeClient{}, stdin, stdout)
	initCtx, initCancel := context.WithTimeout(logoutCtx, 10*time.Second)
	_, err = conn.Initialize(initCtx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientInfo: &acp.Implementation{
			Name:    "hecate-logout",
			Version: "alpha",
		},
		ClientCapabilities: acp.ClientCapabilities{
			Fs: acp.FileSystemCapabilities{
				ReadTextFile:  true,
				WriteTextFile: true,
			},
			Terminal: false,
		},
	})
	initCancel()
	if err != nil {
		cleanupProcess()
		res.DurationMS = elapsedMS(start)
		return res, fmt.Errorf("initialize ACP adapter %q: %w%s", adapter.ID, err, stderrSuffix(stderr.String()))
	}

	callCtx, callCancel := context.WithTimeout(logoutCtx, 10*time.Second)
	_, err = conn.Logout(callCtx, acp.LogoutRequest{})
	callCancel()
	if err != nil {
		cleanupProcess()
		res.DurationMS = elapsedMS(start)
		return res, fmt.Errorf("logout ACP adapter %q: %w%s", adapter.ID, err, stderrSuffix(stderr.String()))
	}
	res.Status = LogoutStatusLoggedOut
	res.DurationMS = elapsedMS(start)
	return res, nil
}
