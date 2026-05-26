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

	"github.com/hecatehq/hecate/internal/agentcontrols"
	"github.com/hecatehq/hecate/internal/telemetry"
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
	logger    *slog.Logger

	configMu      sync.Mutex
	configOptions []agentcontrols.ConfigOption
	managedConfig map[string]struct{}

	turnMu sync.Mutex

	activeMu     sync.Mutex
	activeCancel context.CancelFunc
	activeDone   chan struct{}
}

func startACPSession(ctx context.Context, adapter Adapter, sessionID, workspace, previousNativeSessionID string, selectedOptions []agentcontrols.ConfigOption, logger *slog.Logger, coordinator *ApprovalCoordinator, metrics *telemetry.AgentAdapterMetrics) (*acpSession, bool, string, error) {
	command, err := resolveExecutable(adapter, exec.LookPath)
	if err != nil {
		return nil, false, "", err
	}
	args := append([]string(nil), adapter.Args...)
	sessionLogger := logger
	if sessionLogger != nil {
		sessionLogger = sessionLogger.With(
			slog.String("component", "agent_adapters.acp_session"),
			slog.String("adapter_id", adapter.ID),
			slog.String("session_id", sessionID),
			slog.String("workspace", workspace),
		)
		sessionLogger.Info("starting ACP adapter process",
			slog.String("command", command),
			slog.Any("args", args),
			slog.Bool("resume_requested", strings.TrimSpace(previousNativeSessionID) != ""),
		)
	}
	cmd := exec.CommandContext(context.Background(), command, args...)
	configureCommandProcessGroup(cmd)
	cmd.Dir = workspace
	cmd.Env = sanitizedEnvForAdapter(adapter.ID, os.Environ())

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
	if sessionLogger != nil {
		sessionLogger.Info("ACP adapter process started", slog.Int("pid", cmd.Process.Pid))
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
		conn.SetLogger(logger.With(
			slog.String("component", "agent_adapters.acp"),
			slog.String("adapter_id", adapter.ID),
			slog.String("session_id", sessionID),
			slog.String("workspace", workspace),
		))
	}
	initCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if sessionLogger != nil {
		sessionLogger.Info("initializing ACP adapter")
	}
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
		if sessionLogger != nil {
			sessionLogger.Warn("ACP adapter initialize failed", slog.Any("error", err))
		}
		terminateProcess(cmd)
		return nil, false, "", fmt.Errorf("initialize ACP adapter %q: %w%s", adapter.ID, err, stderrSuffix(stderr.String()))
	}
	if sessionLogger != nil {
		sessionLogger.Info("ACP adapter initialized",
			slog.Bool("load_session_supported", initResp.AgentCapabilities.LoadSession),
		)
	}

	nativeID := ""
	resumed := false
	recovery := ""
	var configOptions []agentcontrols.ConfigOption
	var managedConfig map[string]struct{}
	previousNativeSessionID = strings.TrimSpace(previousNativeSessionID)
	if previousNativeSessionID != "" && initResp.AgentCapabilities.LoadSession {
		if sessionLogger != nil {
			sessionLogger.Info("loading previous ACP session", slog.String("native_session_id", previousNativeSessionID))
		}
		loadCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		loaded, loadErr := conn.LoadSession(loadCtx, acp.LoadSessionRequest{
			SessionId:  acp.SessionId(previousNativeSessionID),
			Cwd:        workspace,
			McpServers: []acp.McpServer{},
		})
		if loadErr == nil {
			nativeID = previousNativeSessionID
			resumed = true
			configOptions = mergeACPSessionConfigOptions(
				agentcontrols.FromACPOptions(loaded.ConfigOptions),
				loaded.Models,
			)
			configOptions, managedConfig = appendLaunchConfigOptions(ctx, command, adapter, configOptions, selectedOptions)
			configOptions, loadErr = applySelectedACPModel(loadCtx, conn, nativeID, adapter, configOptions, selectedOptions)
			if loadErr != nil {
				recovery = fmt.Sprintf("could not restore ACP session %s: %v", previousNativeSessionID, loadErr)
				if sessionLogger != nil {
					sessionLogger.Warn("previous ACP session model selection failed",
						slog.String("native_session_id", previousNativeSessionID),
						slog.Any("error", loadErr),
					)
				}
				nativeID = ""
				resumed = false
			}
		}
		cancel()
		if loadErr == nil && nativeID != "" {
			if sessionLogger != nil {
				sessionLogger.Info("previous ACP session loaded",
					slog.String("native_session_id", nativeID),
					slog.Int("config_options", len(configOptions)),
				)
			}
		} else {
			recovery = fmt.Sprintf("could not restore ACP session %s: %v", previousNativeSessionID, loadErr)
			if sessionLogger != nil {
				sessionLogger.Warn("previous ACP session load failed",
					slog.String("native_session_id", previousNativeSessionID),
					slog.Any("error", loadErr),
				)
			}
		}
	}
	if nativeID == "" {
		if sessionLogger != nil {
			sessionLogger.Info("creating ACP session")
		}
		newCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		created, err := conn.NewSession(newCtx, acp.NewSessionRequest{
			Cwd:        workspace,
			McpServers: []acp.McpServer{},
		})
		if err != nil {
			cancel()
			if sessionLogger != nil {
				sessionLogger.Warn("ACP session creation failed", slog.Any("error", err))
			}
			terminateProcess(cmd)
			return nil, false, "", fmt.Errorf("create ACP session for %q: %w%s", adapter.ID, err, stderrSuffix(stderr.String()))
		}
		nativeID = string(created.SessionId)
		configOptions = mergeACPSessionConfigOptions(
			agentcontrols.FromACPOptions(created.ConfigOptions),
			created.Models,
		)
		configOptions, managedConfig = appendLaunchConfigOptions(ctx, command, adapter, configOptions, selectedOptions)
		configOptions, err = applySelectedACPModel(newCtx, conn, nativeID, adapter, configOptions, selectedOptions)
		cancel()
		if err != nil {
			if sessionLogger != nil {
				sessionLogger.Warn("ACP session model selection failed", slog.Any("error", err))
			}
			terminateProcess(cmd)
			return nil, false, "", fmt.Errorf("select ACP model for %q: %w%s", adapter.ID, err, stderrSuffix(stderr.String()))
		}
		if sessionLogger != nil {
			sessionLogger.Info("ACP session created",
				slog.String("native_session_id", nativeID),
				slog.Int("config_options", len(configOptions)),
			)
		}
	}
	return &acpSession{
		adapter:       adapter,
		workspace:     workspace,
		cmd:           cmd,
		conn:          conn,
		client:        client,
		nativeID:      nativeID,
		logger:        sessionLogger,
		configOptions: configOptions,
		managedConfig: managedConfig,
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
	if session == nil {
		m.mu.Unlock()
		return SetSessionConfigOptionResult{}, fmt.Errorf("%w: %q", ErrSessionNotActive, req.SessionID)
	}
	if session.isManagedConfigOption(req.ConfigID) {
		result, err := session.SetManagedConfigOption(req)
		if err != nil {
			m.mu.Unlock()
			return SetSessionConfigOptionResult{}, err
		}
		delete(m.sessions, req.SessionID)
		m.mu.Unlock()
		closeCtx, cancel := context.WithTimeout(context.Background(), acpShutdownCloseTimeout)
		closeErr := session.Close(closeCtx)
		cancel()
		if closeErr != nil && session.logger != nil {
			session.logger.Warn("close ACP session after launch config change failed", slog.Any("error", closeErr))
		}
		return result, nil
	}
	if session.isACPModelConfigOption(req.ConfigID) {
		m.mu.Unlock()
		return session.SetACPModel(ctx, req)
	}
	m.mu.Unlock()
	return session.SetConfigOption(ctx, req)
}

func (s *acpSession) isManagedConfigOption(configID string) bool {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	if s.managedConfig == nil {
		return false
	}
	_, ok := s.managedConfig[strings.TrimSpace(configID)]
	return ok
}

func (s *acpSession) isACPModelConfigOption(configID string) bool {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	configID = strings.TrimSpace(configID)
	for _, option := range s.configOptions {
		if option.ID == configID && option.Source == agentcontrols.ConfigOptionSourceACPModel {
			return true
		}
	}
	return false
}

func (s *acpSession) SetManagedConfigOption(req SetSessionConfigOptionRequest) (SetSessionConfigOptionResult, error) {
	if req.BoolValue != nil {
		return SetSessionConfigOptionResult{}, fmt.Errorf("launch config option %q requires a string value", req.ConfigID)
	}
	value := strings.TrimSpace(req.Value)
	if value == "" {
		return SetSessionConfigOptionResult{}, fmt.Errorf("value is required")
	}
	s.configMu.Lock()
	defer s.configMu.Unlock()
	if s.managedConfig == nil {
		return SetSessionConfigOptionResult{}, fmt.Errorf("config option %q is not managed by Hecate", req.ConfigID)
	}
	if _, ok := s.managedConfig[req.ConfigID]; !ok {
		return SetSessionConfigOptionResult{}, fmt.Errorf("config option %q is not managed by Hecate", req.ConfigID)
	}
	options := cloneConfigOptions(s.configOptions)
	for i := range options {
		if options[i].ID != req.ConfigID {
			continue
		}
		if !configOptionAllowsValue(options[i], value) {
			return SetSessionConfigOptionResult{}, fmt.Errorf("value %q is not available for %s %s", value, s.adapter.Name, options[i].Name)
		}
		options[i].CurrentValue = value
		s.configOptions = options
		return SetSessionConfigOptionResult{ConfigOptions: cloneConfigOptions(options)}, nil
	}
	return SetSessionConfigOptionResult{}, fmt.Errorf("config option %q not found", req.ConfigID)
}

func configOptionAllowsValue(option agentcontrols.ConfigOption, value string) bool {
	for _, candidate := range option.Options {
		if candidate.Value == value {
			return true
		}
	}
	return false
}

func (s *acpSession) SetACPModel(ctx context.Context, req SetSessionConfigOptionRequest) (SetSessionConfigOptionResult, error) {
	if req.BoolValue != nil {
		return SetSessionConfigOptionResult{}, fmt.Errorf("ACP model option %q requires a string value", req.ConfigID)
	}
	value := strings.TrimSpace(req.Value)
	if value == "" {
		return SetSessionConfigOptionResult{}, fmt.Errorf("value is required")
	}
	s.configMu.Lock()
	options := cloneConfigOptions(s.configOptions)
	modelIndex := -1
	for i := range options {
		if options[i].ID == req.ConfigID && options[i].Source == agentcontrols.ConfigOptionSourceACPModel {
			modelIndex = i
			break
		}
	}
	if modelIndex < 0 {
		s.configMu.Unlock()
		return SetSessionConfigOptionResult{}, fmt.Errorf("ACP model option %q not found", req.ConfigID)
	}
	if !configOptionAllowsValue(options[modelIndex], value) {
		s.configMu.Unlock()
		return SetSessionConfigOptionResult{}, fmt.Errorf("value %q is not available for %s %s", value, s.adapter.Name, options[modelIndex].Name)
	}
	s.configMu.Unlock()

	if _, err := s.conn.UnstableSetSessionModel(ctx, acp.UnstableSetSessionModelRequest{
		SessionId: acp.SessionId(s.nativeID),
		ModelId:   acp.UnstableModelId(value),
	}); err != nil {
		return SetSessionConfigOptionResult{}, err
	}

	s.configMu.Lock()
	defer s.configMu.Unlock()
	options = cloneConfigOptions(s.configOptions)
	for i := range options {
		if options[i].ID == req.ConfigID && options[i].Source == agentcontrols.ConfigOptionSourceACPModel {
			options[i].CurrentValue = value
			s.configOptions = options
			return SetSessionConfigOptionResult{ConfigOptions: cloneConfigOptions(options)}, nil
		}
	}
	return SetSessionConfigOptionResult{}, fmt.Errorf("ACP model option %q not found", req.ConfigID)
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
	previousOptions := s.configOptionsSnapshot()
	resp, err := s.conn.SetSessionConfigOption(ctx, acpReq)
	if err != nil {
		return SetSessionConfigOptionResult{}, err
	}
	options := agentcontrols.FromACPOptions(resp.ConfigOptions)
	if resp.ConfigOptions != nil && options == nil {
		options = []agentcontrols.ConfigOption{}
	}
	options = preserveACPModelConfigOption(options, previousOptions)
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

func mergeACPSessionConfigOptions(options []agentcontrols.ConfigOption, models *acp.SessionModelState) []agentcontrols.ConfigOption {
	modelOption, ok := agentcontrols.FromACPModelState(models)
	if !ok || hasModelConfigOption(options) {
		return options
	}
	return append(options, modelOption)
}

func preserveACPModelConfigOption(options, previous []agentcontrols.ConfigOption) []agentcontrols.ConfigOption {
	if hasModelConfigOption(options) {
		return options
	}
	for _, option := range previous {
		if option.Source == agentcontrols.ConfigOptionSourceACPModel {
			return append(options, cloneConfigOptions([]agentcontrols.ConfigOption{option})...)
		}
	}
	return options
}

func applySelectedACPModel(ctx context.Context, conn *acp.ClientSideConnection, nativeID string, adapter Adapter, options, selected []agentcontrols.ConfigOption) ([]agentcontrols.ConfigOption, error) {
	value := selectedConfigOptionValue(selected, "model")
	if value == "" || strings.HasPrefix(value, "__hecate_no_") {
		return options, nil
	}
	out := cloneConfigOptions(options)
	for i := range out {
		if out[i].ID != "model" || out[i].Source != agentcontrols.ConfigOptionSourceACPModel {
			continue
		}
		if out[i].CurrentValue == value {
			return out, nil
		}
		if !configOptionAllowsValue(out[i], value) {
			return nil, fmt.Errorf("value %q is not available for %s %s", value, adapter.Name, out[i].Name)
		}
		if _, err := conn.UnstableSetSessionModel(ctx, acp.UnstableSetSessionModelRequest{
			SessionId: acp.SessionId(nativeID),
			ModelId:   acp.UnstableModelId(value),
		}); err != nil {
			return nil, err
		}
		out[i].CurrentValue = value
		return out, nil
	}
	return options, nil
}

func selectedConfigOptionValue(options []agentcontrols.ConfigOption, id string) string {
	for _, option := range options {
		if option.ID == id {
			return strings.TrimSpace(option.CurrentValue)
		}
	}
	return ""
}

func (s *acpSession) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if s.logger != nil {
		pid := 0
		if s.cmd != nil && s.cmd.Process != nil {
			pid = s.cmd.Process.Pid
		}
		s.logger.Info("closing ACP adapter session",
			slog.String("native_session_id", s.nativeID),
			slog.Int("pid", pid),
		)
	}
	cancelCtx, cancel := context.WithTimeout(ctx, acpShutdownCancelTimeout)
	if err := s.cancelActiveTurn(cancelCtx); err != nil && s.logger != nil {
		s.logger.Warn("cancel active ACP turn during close failed", slog.Any("error", err))
	}
	cancel()
	if s.conn != nil && s.nativeID != "" {
		closeCtx, cancel := context.WithTimeout(ctx, acpShutdownCloseTimeout)
		if _, err := s.conn.CloseSession(closeCtx, acp.CloseSessionRequest{SessionId: acp.SessionId(s.nativeID)}); err != nil && s.logger != nil {
			s.logger.Warn("close ACP session RPC failed", slog.String("native_session_id", s.nativeID), slog.Any("error", err))
		}
		cancel()
	}
	if s.cmd != nil {
		terminateProcess(s.cmd)
		if s.logger != nil {
			s.logger.Info("ACP adapter process terminated", slog.String("native_session_id", s.nativeID))
		}
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
