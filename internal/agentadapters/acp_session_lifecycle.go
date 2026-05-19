package agentadapters

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/hecate/agent-runtime/internal/agentcontrols"
	"github.com/hecate/agent-runtime/internal/telemetry"
)

const (
	acpShutdownCancelTimeout = 2 * time.Second
	acpShutdownCloseTimeout  = 2 * time.Second
)

type acpSession struct {
	adapter   Adapter
	workspace string
	cmd       *exec.Cmd
	conn      *acp.ClientSideConnection
	client    *acpChatClient
	nativeID  string

	configMu      sync.Mutex
	configOptions []agentcontrols.ConfigOption

	turnMu sync.Mutex

	activeMu     sync.Mutex
	activeCancel context.CancelFunc
	activeDone   chan struct{}
}

func startACPSession(ctx context.Context, adapter Adapter, sessionID, workspace, previousNativeSessionID string, logger *slog.Logger, coordinator *ApprovalCoordinator, metrics *telemetry.AgentAdapterMetrics) (*acpSession, bool, string, error) {
	command, err := resolveExecutable(adapter, exec.LookPath)
	if err != nil {
		return nil, false, "", err
	}
	args := append([]string(nil), adapter.Args...)
	cmd := exec.CommandContext(context.Background(), command, args...)
	configureCommandProcessGroup(cmd)
	cmd.Dir = workspace
	cmd.Env = sanitizedEnv(os.Environ())

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, false, "", fmt.Errorf("create ACP stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false, "", fmt.Errorf("create ACP stdout pipe: %w", err)
	}
	var stderr limitedBuffer
	stderr.limit = 256 * 1024
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, false, "", fmt.Errorf("start ACP adapter %q: %w", adapter.ID, err)
	}

	client := &acpChatClient{
		sessionID:   sessionID,
		adapterID:   adapter.ID,
		workspace:   workspace,
		coordinator: coordinator,
		metrics:     metrics,
	}
	conn := acp.NewClientSideConnection(client, stdin, stdout)
	if logger != nil {
		conn.SetLogger(logger.With("component", "agent_adapters.acp", "adapter_id", adapter.ID))
	}
	initCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	initResp, err := conn.Initialize(initCtx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientInfo: &acp.Implementation{
			Name:    "hecate",
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
	if err != nil {
		terminateProcess(cmd)
		return nil, false, "", fmt.Errorf("initialize ACP adapter %q: %w%s", adapter.ID, err, stderrSuffix(stderr.String()))
	}

	nativeID := ""
	resumed := false
	recovery := ""
	var configOptions []agentcontrols.ConfigOption
	previousNativeSessionID = strings.TrimSpace(previousNativeSessionID)
	if previousNativeSessionID != "" && initResp.AgentCapabilities.LoadSession {
		loadCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		loaded, loadErr := conn.LoadSession(loadCtx, acp.LoadSessionRequest{
			SessionId:  acp.SessionId(previousNativeSessionID),
			Cwd:        workspace,
			McpServers: []acp.McpServer{},
		})
		cancel()
		if loadErr == nil {
			nativeID = previousNativeSessionID
			resumed = true
			configOptions = agentcontrols.FromACPOptions(loaded.ConfigOptions)
		} else {
			recovery = fmt.Sprintf("could not restore ACP session %s: %v", previousNativeSessionID, loadErr)
		}
	}
	if nativeID == "" {
		newCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		created, err := conn.NewSession(newCtx, acp.NewSessionRequest{
			Cwd:        workspace,
			McpServers: []acp.McpServer{},
		})
		cancel()
		if err != nil {
			terminateProcess(cmd)
			return nil, false, "", fmt.Errorf("create ACP session for %q: %w%s", adapter.ID, err, stderrSuffix(stderr.String()))
		}
		nativeID = string(created.SessionId)
		configOptions = agentcontrols.FromACPOptions(created.ConfigOptions)
	}
	return &acpSession{
		adapter:       adapter,
		workspace:     workspace,
		cmd:           cmd,
		conn:          conn,
		client:        client,
		nativeID:      nativeID,
		configOptions: configOptions,
	}, resumed, recovery, nil
}

func (s *acpSession) RunTurn(ctx context.Context, req RunRequest) (RunResult, error) {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()

	started := time.Now().UTC()
	turn := newACPTurn(req.MaxOutputBytes, req.OnOutput)
	turn.setActivityCallback(req.OnActivity)
	s.client.setTurn(turn)
	defer s.client.clearTurn(turn)

	promptBaseCtx, timeoutCancel := context.WithTimeout(ctx, req.Timeout)
	promptCtx, activeCancel := context.WithCancel(promptBaseCtx)
	activeDone := make(chan struct{})
	s.setActiveTurn(activeCancel, activeDone)
	defer func() {
		activeCancel()
		timeoutCancel()
		close(activeDone)
		s.clearActiveTurn(activeDone)
	}()
	resp, runErr := s.conn.Prompt(promptCtx, acp.PromptRequest{
		SessionId: acp.SessionId(s.nativeID),
		Prompt:    []acp.ContentBlock{acp.TextBlock(req.Prompt)},
	})
	if runErr == nil && resp.StopReason == acp.StopReasonCancelled {
		runErr = context.Canceled
	}
	completed := time.Now().UTC()
	exitCode := 0
	if runErr != nil {
		exitCode = 1
		if errors.Is(promptCtx.Err(), context.DeadlineExceeded) {
			runErr = fmt.Errorf("agent adapter timed out after %s", req.Timeout)
		} else if errors.Is(promptCtx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			runErr = context.Canceled
		}
	}
	if turn.truncated() {
		if runErr == nil {
			runErr = fmt.Errorf("agent adapter output exceeded %d bytes", req.MaxOutputBytes)
		} else {
			runErr = fmt.Errorf("%w; output exceeded %d bytes", runErr, req.MaxOutputBytes)
		}
	}
	output, raw, usage := turn.snapshot()
	result, err := captureACPTurnResult(ctx, s.adapter, req, s.nativeID, output, raw, usage, exitCode, started, completed, runErr)
	result.ConfigOptions = s.configOptionsSnapshot()
	return result, err
}

func (m *SessionManager) SetSessionConfigOption(ctx context.Context, req SetSessionConfigOptionRequest) (SetSessionConfigOptionResult, error) {
	if m == nil {
		return SetSessionConfigOptionResult{}, fmt.Errorf("agent session manager is required")
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.ConfigID = strings.TrimSpace(req.ConfigID)
	if req.SessionID == "" {
		return SetSessionConfigOptionResult{}, fmt.Errorf("agent chat session id is required")
	}
	m.mu.Lock()
	session := m.sessions[req.SessionID]
	m.mu.Unlock()
	if session == nil {
		return SetSessionConfigOptionResult{}, fmt.Errorf("%w: %q", ErrSessionNotActive, req.SessionID)
	}
	return session.SetConfigOption(ctx, req)
}

func (s *acpSession) SetConfigOption(ctx context.Context, req SetSessionConfigOptionRequest) (SetSessionConfigOptionResult, error) {
	acpReq, err := agentcontrols.BuildACPSetRequest(agentcontrols.SetConfigOptionRequest{
		SessionID: s.nativeID,
		ConfigID:  req.ConfigID,
		Value:     req.Value,
		BoolValue: req.BoolValue,
	})
	if err != nil {
		return SetSessionConfigOptionResult{}, err
	}
	resp, err := s.conn.SetSessionConfigOption(ctx, acpReq)
	if err != nil {
		return SetSessionConfigOptionResult{}, err
	}
	options := agentcontrols.FromACPOptions(resp.ConfigOptions)
	if resp.ConfigOptions != nil && options == nil {
		options = []agentcontrols.ConfigOption{}
	}
	s.setConfigOptions(options)
	return SetSessionConfigOptionResult{ConfigOptions: options}, nil
}

func (s *acpSession) setConfigOptions(options []agentcontrols.ConfigOption) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	if options == nil {
		s.configOptions = nil
		return
	}
	s.configOptions = cloneConfigOptions(options)
}

func (s *acpSession) configOptionsSnapshot() []agentcontrols.ConfigOption {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	if s.configOptions == nil {
		return nil
	}
	return cloneConfigOptions(s.configOptions)
}

func cloneConfigOptions(options []agentcontrols.ConfigOption) []agentcontrols.ConfigOption {
	if options == nil {
		return nil
	}
	out := make([]agentcontrols.ConfigOption, len(options))
	copy(out, options)
	for i := range out {
		if options[i].Options == nil {
			continue
		}
		out[i].Options = make([]agentcontrols.ConfigSelectOption, len(options[i].Options))
		copy(out[i].Options, options[i].Options)
	}
	return out
}

func (s *acpSession) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	cancelCtx, cancel := context.WithTimeout(ctx, acpShutdownCancelTimeout)
	_ = s.cancelActiveTurn(cancelCtx)
	cancel()
	if s.conn != nil && s.nativeID != "" {
		closeCtx, cancel := context.WithTimeout(ctx, acpShutdownCloseTimeout)
		_, _ = s.conn.CloseSession(closeCtx, acp.CloseSessionRequest{SessionId: acp.SessionId(s.nativeID)})
		cancel()
	}
	if s.cmd != nil {
		terminateProcess(s.cmd)
	}
	return nil
}

func (s *acpSession) setActiveTurn(cancel context.CancelFunc, done chan struct{}) {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	s.activeCancel = cancel
	s.activeDone = done
}

func (s *acpSession) clearActiveTurn(done chan struct{}) {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	if s.activeDone == done {
		s.activeCancel = nil
		s.activeDone = nil
	}
}

func (s *acpSession) cancelActiveTurn(ctx context.Context) error {
	s.activeMu.Lock()
	cancel := s.activeCancel
	done := s.activeDone
	conn := s.conn
	nativeID := s.nativeID
	s.activeMu.Unlock()
	if done == nil {
		return nil
	}
	if conn != nil && nativeID != "" {
		_ = conn.Cancel(ctx, acp.CancelNotification{SessionId: acp.SessionId(nativeID)})
	}
	if cancel != nil {
		cancel()
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func captureACPTurnResult(ctx context.Context, adapter Adapter, req RunRequest, nativeSessionID, output, rawOutput string, usage Usage, exitCode int, started, completed time.Time, runErr error) (RunResult, error) {
	maxOutput := req.MaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = 1024 * 1024
	}
	diffStat, diff := captureGitDiff(ctx, req.Workspace, maxOutput)
	return RunResult{
		Adapter:         adapter,
		DriverKind:      DriverKindACP,
		NativeSessionID: nativeSessionID,
		Output:          normalizeOutput(adapter.ID, output),
		RawOutput:       rawOutput,
		ExitCode:        exitCode,
		StartedAt:       started,
		CompletedAt:     completed,
		DiffStat:        diffStat,
		Diff:            diff,
		Usage:           usage,
	}, runErr
}
