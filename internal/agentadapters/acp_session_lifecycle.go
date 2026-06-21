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
	"github.com/hecatehq/hecate/pkg/types"
)

const (
	acpShutdownCancelTimeout = 2 * time.Second
	acpShutdownCloseTimeout  = 2 * time.Second
	acpInitialCommandTimeout = 500 * time.Millisecond
)

type acpSessionShutdownMode string

const (
	acpSessionShutdownClose  acpSessionShutdownMode = "close"
	acpSessionShutdownDelete acpSessionShutdownMode = "delete"
)

type acpSession struct {
	sessionID           string
	adapter             Adapter
	workspace           string
	mcpServers          []types.MCPServerConfig
	cmd                 *exec.Cmd
	conn                *acp.ClientSideConnection
	client              *acpChatClient
	nativeID            string
	logger              *slog.Logger
	envCleanup          func()
	onAvailableCommands func(AvailableCommandsUpdate)

	configMu      sync.Mutex
	configOptions []agentcontrols.ConfigOption
	managedConfig map[string]struct{}

	commandMu              sync.Mutex
	availableCommands      []agentcontrols.Command
	availableCommandsKnown bool
	commandUpdate          chan struct{}

	turnMu sync.Mutex

	activeMu     sync.Mutex
	activeCancel context.CancelFunc
	activeDone   chan struct{}
}

func startACPSession(ctx context.Context, adapter Adapter, sessionID, workspace, previousNativeSessionID string, selectedOptions []agentcontrols.ConfigOption, mcpServers []types.MCPServerConfig, logger *slog.Logger, coordinator *ApprovalCoordinator, metrics *telemetry.AgentAdapterMetrics, onAvailableCommands func(AvailableCommandsUpdate)) (*acpSession, bool, string, error) {
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
	processEnv, err := prepareAdapterProcessEnv(ctx, adapter, os.Environ())
	if err != nil {
		return nil, false, "", err
	}
	cleanupEnvOnError := true
	defer func() {
		if cleanupEnvOnError && processEnv.cleanup != nil {
			processEnv.cleanup()
		}
	}()

	cmd := exec.CommandContext(context.Background(), command, args...)
	configureCommandProcessGroup(cmd)
	cmd.Dir = workspace
	cmd.Env = processEnv.values

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
	session := &acpSession{
		sessionID:           sessionID,
		adapter:             adapter,
		workspace:           workspace,
		mcpServers:          cloneMCPServerConfigs(mcpServers),
		cmd:                 cmd,
		client:              client,
		logger:              sessionLogger,
		envCleanup:          processEnv.cleanup,
		onAvailableCommands: onAvailableCommands,
		commandUpdate:       make(chan struct{}),
	}
	client.onAvailableCommands = session.setAvailableCommands
	client.onConfigOptions = session.applyConfigOptionsUpdate
	conn := acp.NewClientSideConnection(client, stdin, stdout)
	session.conn = conn
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
			McpServers: acpMCPServers(mcpServers),
		})
		if loadErr == nil {
			nativeID = previousNativeSessionID
			resumed = true
			configOptions = agentcontrols.FromACPOptions(loaded.ConfigOptions)
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
			if nativeID != "" {
				configOptions, loadErr = applySelectedACPConfigOptions(loadCtx, conn, nativeID, adapter, configOptions, selectedOptions)
				if loadErr != nil {
					recovery = fmt.Sprintf("could not restore ACP session %s: %v", previousNativeSessionID, loadErr)
					if sessionLogger != nil {
						sessionLogger.Warn("previous ACP session config selection failed",
							slog.String("native_session_id", previousNativeSessionID),
							slog.Any("error", loadErr),
						)
					}
					nativeID = ""
					resumed = false
				}
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
			McpServers: acpMCPServers(mcpServers),
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
		configOptions = agentcontrols.FromACPOptions(created.ConfigOptions)
		configOptions, managedConfig = appendLaunchConfigOptions(ctx, command, adapter, configOptions, selectedOptions)
		configOptions, err = applySelectedACPModel(newCtx, conn, nativeID, adapter, configOptions, selectedOptions)
		if err != nil {
			cancel()
			if sessionLogger != nil {
				sessionLogger.Warn("ACP session model selection failed", slog.Any("error", err))
			}
			terminateProcess(cmd)
			return nil, false, "", fmt.Errorf("select ACP model for %q: %w%s", adapter.ID, err, stderrSuffix(stderr.String()))
		}
		configOptions, err = applySelectedACPConfigOptions(newCtx, conn, nativeID, adapter, configOptions, selectedOptions)
		cancel()
		if err != nil {
			if sessionLogger != nil {
				sessionLogger.Warn("ACP session config selection failed", slog.Any("error", err))
			}
			terminateProcess(cmd)
			return nil, false, "", fmt.Errorf("select ACP config for %q: %w%s", adapter.ID, err, stderrSuffix(stderr.String()))
		}
		if sessionLogger != nil {
			sessionLogger.Info("ACP session created",
				slog.String("native_session_id", nativeID),
				slog.Int("config_options", len(configOptions)),
			)
		}
	}
	session.nativeID = nativeID
	session.configOptions = configOptions
	session.managedConfig = managedConfig
	session.waitForInitialAvailableCommands(ctx)
	cleanupEnvOnError = false
	return session, resumed, recovery, nil
}

func (s *acpSession) RunTurn(ctx context.Context, req RunRequest) (RunResult, error) {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()

	started := time.Now().UTC()
	maxOutput := maxTurnOutputBytes(req)
	initialDiffStat, initialDiff := captureGitDiff(ctx, req.Workspace, maxOutput)
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
	stopReason := string(resp.StopReason)
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
	result, err := captureACPTurnResult(ctx, s.adapter, req, s.nativeID, stopReason, output, raw, usage, exitCode, started, completed, initialDiffStat, initialDiff, runErr)
	result.ConfigOptions = s.configOptionsSnapshot()
	result.AvailableCommands, result.AvailableCommandsKnown = s.availableCommandsSnapshot()
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
		return s.setSessionConfigOptionResult(cloneConfigOptions(options)), nil
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

	acpReq, err := agentcontrols.BuildACPSetRequest(agentcontrols.SetConfigOptionRequest{
		SessionID: s.nativeID,
		ConfigID:  req.ConfigID,
		Value:     value,
	})
	if err != nil {
		return SetSessionConfigOptionResult{}, err
	}
	resp, err := s.conn.SetSessionConfigOption(ctx, acpReq)
	if err != nil {
		return SetSessionConfigOptionResult{}, fmt.Errorf("select ACP model for %q: %w", s.adapter.ID, err)
	}

	s.configMu.Lock()
	defer s.configMu.Unlock()
	options = cloneConfigOptions(s.configOptions)
	updated := agentcontrols.FromACPOptions(resp.ConfigOptions)
	if resp.ConfigOptions != nil {
		updated = preserveACPModelConfigOption(updated, options)
		s.configOptions = updated
		return s.setSessionConfigOptionResult(cloneConfigOptions(updated)), nil
	}
	for i := range options {
		if options[i].ID == req.ConfigID && options[i].Source == agentcontrols.ConfigOptionSourceACPModel {
			options[i].CurrentValue = value
			s.configOptions = options
			return s.setSessionConfigOptionResult(cloneConfigOptions(options)), nil
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
	return s.setSessionConfigOptionResult(options), nil
}

func (s *acpSession) setSessionConfigOptionResult(options []agentcontrols.ConfigOption) SetSessionConfigOptionResult {
	commands, commandsKnown := s.availableCommandsSnapshot()
	return SetSessionConfigOptionResult{
		ConfigOptions:          options,
		AvailableCommands:      commands,
		AvailableCommandsKnown: commandsKnown,
	}
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

func (s *acpSession) applyConfigOptionsUpdate(options []agentcontrols.ConfigOption) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	previous := cloneConfigOptions(s.configOptions)
	options = preserveACPModelConfigOption(options, previous)
	options = preserveManagedConfigOptions(options, previous, s.managedConfig)
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

func (s *acpSession) setAvailableCommands(commands []agentcontrols.Command) {
	commands = cloneCommands(commands)
	s.commandMu.Lock()
	s.availableCommands = commands
	s.availableCommandsKnown = true
	if s.commandUpdate != nil {
		close(s.commandUpdate)
	}
	s.commandUpdate = make(chan struct{})
	s.commandMu.Unlock()

	if s.onAvailableCommands != nil {
		s.onAvailableCommands(AvailableCommandsUpdate{
			SessionID: s.sessionID,
			AdapterID: s.adapter.ID,
			Commands:  cloneCommands(commands),
		})
	}
}

func (s *acpSession) availableCommandsSnapshot() ([]agentcontrols.Command, bool) {
	s.commandMu.Lock()
	defer s.commandMu.Unlock()
	if !s.availableCommandsKnown {
		return nil, false
	}
	return cloneCommands(s.availableCommands), true
}

func (s *acpSession) waitForInitialAvailableCommands(ctx context.Context) {
	s.commandMu.Lock()
	if s.availableCommandsKnown {
		s.commandMu.Unlock()
		return
	}
	update := s.commandUpdate
	s.commandMu.Unlock()
	if update == nil {
		return
	}

	waitCtx, cancel := context.WithTimeout(ctx, acpInitialCommandTimeout)
	defer cancel()
	select {
	case <-waitCtx.Done():
	case <-update:
	}
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

func cloneConfigOption(option agentcontrols.ConfigOption) agentcontrols.ConfigOption {
	return cloneConfigOptions([]agentcontrols.ConfigOption{option})[0]
}

func cloneCommands(commands []agentcontrols.Command) []agentcontrols.Command {
	if commands == nil {
		return nil
	}
	out := make([]agentcontrols.Command, len(commands))
	copy(out, commands)
	return out
}

func preserveACPModelConfigOption(options, previous []agentcontrols.ConfigOption) []agentcontrols.ConfigOption {
	if hasModelConfigOption(options) {
		return options
	}
	for _, option := range previous {
		if option.Source == agentcontrols.ConfigOptionSourceACPModel {
			return append(options, cloneConfigOption(option))
		}
	}
	return options
}

func preserveManagedConfigOptions(options, previous []agentcontrols.ConfigOption, managed map[string]struct{}) []agentcontrols.ConfigOption {
	if len(managed) == 0 {
		return options
	}
	out := cloneConfigOptions(options)
	present := make(map[string]struct{}, len(out))
	for _, option := range out {
		present[option.ID] = struct{}{}
	}
	for _, option := range previous {
		if _, ok := managed[option.ID]; !ok {
			continue
		}
		if _, ok := present[option.ID]; ok {
			continue
		}
		out = append(out, cloneConfigOption(option))
	}
	return out
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
		acpReq, err := agentcontrols.BuildACPSetRequest(agentcontrols.SetConfigOptionRequest{
			SessionID: nativeID,
			ConfigID:  out[i].ID,
			Value:     value,
		})
		if err != nil {
			return nil, err
		}
		resp, err := conn.SetSessionConfigOption(ctx, acpReq)
		if err != nil {
			return nil, err
		}
		updated := agentcontrols.FromACPOptions(resp.ConfigOptions)
		if resp.ConfigOptions != nil {
			return preserveACPModelConfigOption(updated, out), nil
		}
		out[i].CurrentValue = value
		return out, nil
	}
	return options, nil
}

func applySelectedACPConfigOptions(ctx context.Context, conn *acp.ClientSideConnection, nativeID string, adapter Adapter, options, selected []agentcontrols.ConfigOption) ([]agentcontrols.ConfigOption, error) {
	if len(selected) == 0 || conn == nil || strings.TrimSpace(nativeID) == "" {
		return options, nil
	}
	out := cloneConfigOptions(options)
	for _, selectedOption := range selected {
		index := configOptionIndex(out, selectedOption.ID)
		if index < 0 {
			continue
		}
		current := out[index]
		if current.Source == agentcontrols.ConfigOptionSourceLaunch {
			continue
		}
		if current.ID == "model" && current.Source == agentcontrols.ConfigOptionSourceACPModel {
			continue
		}
		var (
			req agentcontrols.SetConfigOptionRequest
			ok  bool
		)
		switch current.Type {
		case agentcontrols.ConfigOptionTypeSelect:
			value := strings.TrimSpace(selectedOption.CurrentValue)
			if value == "" || strings.HasPrefix(value, "__hecate_no_") || value == current.CurrentValue {
				continue
			}
			if !configOptionAllowsValue(current, value) {
				return nil, fmt.Errorf("value %q is not available for %s %s", value, adapter.Name, current.Name)
			}
			req = agentcontrols.SetConfigOptionRequest{
				SessionID: nativeID,
				ConfigID:  current.ID,
				Value:     value,
			}
			ok = true
		case agentcontrols.ConfigOptionTypeBoolean:
			if selectedOption.CurrentBool == nil {
				continue
			}
			if current.CurrentBool != nil && *current.CurrentBool == *selectedOption.CurrentBool {
				continue
			}
			req = agentcontrols.SetConfigOptionRequest{
				SessionID: nativeID,
				ConfigID:  current.ID,
				BoolValue: selectedOption.CurrentBool,
			}
			ok = true
		}
		if !ok {
			continue
		}
		acpReq, err := agentcontrols.BuildACPSetRequest(req)
		if err != nil {
			return nil, err
		}
		resp, err := conn.SetSessionConfigOption(ctx, acpReq)
		if err != nil {
			return nil, fmt.Errorf("select ACP config option %q for %q: %w", current.ID, adapter.ID, err)
		}
		out = applyACPConfigOptionSetResponse(out, resp, req)
	}
	return out, nil
}

func configOptionIndex(options []agentcontrols.ConfigOption, id string) int {
	id = strings.TrimSpace(id)
	if id == "" {
		return -1
	}
	for i := range options {
		if options[i].ID == id {
			return i
		}
	}
	return -1
}

func applyACPConfigOptionSetResponse(previous []agentcontrols.ConfigOption, resp acp.SetSessionConfigOptionResponse, req agentcontrols.SetConfigOptionRequest) []agentcontrols.ConfigOption {
	if resp.ConfigOptions != nil {
		updated := agentcontrols.FromACPOptions(resp.ConfigOptions)
		if updated == nil {
			updated = []agentcontrols.ConfigOption{}
		}
		return mergeConfigOptions(previous, updated)
	}
	out := cloneConfigOptions(previous)
	if i := configOptionIndex(out, req.ConfigID); i >= 0 {
		if req.BoolValue != nil {
			value := *req.BoolValue
			out[i].CurrentBool = &value
		} else {
			out[i].CurrentValue = strings.TrimSpace(req.Value)
		}
	}
	return out
}

func mergeConfigOptions(previous, updated []agentcontrols.ConfigOption) []agentcontrols.ConfigOption {
	out := cloneConfigOptions(previous)
	for _, option := range updated {
		if i := configOptionIndex(out, option.ID); i >= 0 {
			out[i] = cloneConfigOption(option)
			continue
		}
		out = append(out, cloneConfigOption(option))
	}
	return out
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
	return s.shutdown(ctx, acpSessionShutdownClose)
}

func (s *acpSession) Delete(ctx context.Context) error {
	return s.shutdown(ctx, acpSessionShutdownDelete)
}

func (s *acpSession) shutdown(ctx context.Context, mode acpSessionShutdownMode) error {
	if s == nil {
		return nil
	}
	if s.logger != nil {
		pid := 0
		if s.cmd != nil && s.cmd.Process != nil {
			pid = s.cmd.Process.Pid
		}
		s.logger.Info("shutting down ACP adapter session",
			slog.String("mode", string(mode)),
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
		if mode == acpSessionShutdownDelete {
			deleted := s.deleteNativeSession(ctx)
			if !deleted {
				s.closeNativeSession(ctx)
			}
		} else {
			s.closeNativeSession(ctx)
		}
	}
	if s.cmd != nil {
		terminateProcess(s.cmd)
		if s.logger != nil {
			s.logger.Info("ACP adapter process terminated",
				slog.String("mode", string(mode)),
				slog.String("native_session_id", s.nativeID),
			)
		}
	}
	if s.envCleanup != nil {
		s.envCleanup()
		s.envCleanup = nil
	}
	return nil
}

func (s *acpSession) closeNativeSession(ctx context.Context) {
	closeCtx, cancel := context.WithTimeout(ctx, acpShutdownCloseTimeout)
	defer cancel()
	if _, err := s.conn.CloseSession(closeCtx, acp.CloseSessionRequest{SessionId: acp.SessionId(s.nativeID)}); err != nil && s.logger != nil {
		if isACPMethodNotFound(err, acp.AgentMethodSessionClose) {
			s.logger.Debug("ACP session close RPC unsupported", slog.String("native_session_id", s.nativeID))
		} else {
			s.logger.Warn("close ACP session RPC failed", slog.String("native_session_id", s.nativeID), slog.Any("error", err))
		}
	}
}

func (s *acpSession) deleteNativeSession(ctx context.Context) bool {
	deleteCtx, cancel := context.WithTimeout(ctx, acpShutdownCloseTimeout)
	defer cancel()
	if _, err := s.conn.UnstableDeleteSession(deleteCtx, acp.UnstableDeleteSessionRequest{SessionId: acp.SessionId(s.nativeID)}); err != nil {
		if s.logger != nil {
			if isACPMethodNotFound(err, acp.AgentMethodSessionDelete) {
				s.logger.Debug("ACP session delete RPC unsupported", slog.String("native_session_id", s.nativeID))
			} else {
				s.logger.Warn("delete ACP session RPC failed", slog.String("native_session_id", s.nativeID), slog.Any("error", err))
			}
		}
		return false
	}
	return true
}

func isACPMethodNotFound(err error, method string) bool {
	if err == nil {
		return false
	}
	var rpcErr *acp.RequestError
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32601 {
		return false
	}
	if method == "" {
		return true
	}
	if data, ok := rpcErr.Data.(map[string]any); ok {
		if got, ok := data["method"].(string); ok {
			return got == method
		}
	}
	return strings.Contains(rpcErr.Error(), method)
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

func captureACPTurnResult(ctx context.Context, adapter Adapter, req RunRequest, nativeSessionID, stopReason, output, rawOutput string, usage Usage, exitCode int, started, completed time.Time, initialDiffStat, initialDiff string, runErr error) (RunResult, error) {
	maxOutput := maxTurnOutputBytes(req)
	diffStat, diff := captureGitDiff(ctx, req.Workspace, maxOutput)
	if sameCapturedDiff(initialDiffStat, initialDiff, diffStat, diff) {
		diffStat = ""
		diff = ""
	}
	return RunResult{
		Adapter:         adapter,
		DriverKind:      DriverKindACP,
		NativeSessionID: nativeSessionID,
		StopReason:      strings.TrimSpace(stopReason),
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

func maxTurnOutputBytes(req RunRequest) int64 {
	if req.MaxOutputBytes > 0 {
		return req.MaxOutputBytes
	}
	return 1024 * 1024
}

func sameCapturedDiff(beforeStat, beforeDiff, afterStat, afterDiff string) bool {
	return strings.TrimSpace(beforeStat) == strings.TrimSpace(afterStat) &&
		strings.TrimSpace(beforeDiff) == strings.TrimSpace(afterDiff)
}
