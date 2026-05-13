package agentadapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	acp "github.com/coder/acp-go-sdk"

	"github.com/hecate/agent-runtime/internal/agentcontrols"
	"github.com/hecate/agent-runtime/internal/telemetry"
)

const DriverKindACP = "acp"

var ErrSessionNotActive = errors.New("agent chat session is not active")

// thoughtFallbackBlockID is the per-turn sentinel prefix used
// when ACP omits messageId on an `agent_thought_chunk`. Fallback
// activity rows come out as `thinking:__fallback-<n>` where <n>
// is a counter that increments once per boundary-triggered
// fallback episode in the turn. The leading double-underscore
// guarantees we never collide with a spec-conformant ACP
// messageId — ACP requires messageIds to be UUIDs (per the SDK
// comment on SessionUpdateAgentThoughtChunk.MessageId), and
// UUIDs are hex+dashes only, so an underscore-led prefix cannot
// appear in any spec-conformant id.
//
// The counter exists because mergeAgentChatActivity dedupes by
// Activity.ID and *replaces* Detail wholesale on merge: if two
// distinct fallback episodes shared one id (e.g. an adapter
// mixing messageId-bearing and no-id chunks like
// fallback → real → empty), the second episode's Detail would
// overwrite the first's, silently losing the earlier reasoning.
// Counter-suffixed ids keep each episode on its own row.
//
// Within one fallback episode, all chunks reuse the same
// counter value and merge naturally (continuation case in
// appendAgentThoughtChunk).
//
// Boundary detection itself does NOT sniff this id: see
// `agentThoughtFallback` for the source of truth that a buggy
// adapter cannot impersonate.
const thoughtFallbackBlockID = "__fallback"

// thoughtMaxBytesPerBlock caps the per-block thought accumulator.
// Each chunk of an `agent_thought_chunk` stream re-emits the full
// accumulated `Detail` (mergeAgentChatActivity replaces the row's
// Detail wholesale by ID), so an unbounded accumulator would
// inflate the persisted activities JSON and the websocket payload
// with every chunk. 32 KiB is comfortably above any practical
// thought size while keeping the worst-case row small. When the
// cap is hit, `Detail` is suffixed with a truncation marker so
// operators can see that they are looking at a partial reasoning
// block, not the whole thing.
const thoughtMaxBytesPerBlock = 32 * 1024

// thoughtTruncationSuffix is appended to a thought activity's
// Detail when its accumulator hits thoughtMaxBytesPerBlock.
const thoughtTruncationSuffix = "\n… (thought truncated)"

const (
	acpShutdownCancelTimeout = 2 * time.Second
	acpShutdownCloseTimeout  = 2 * time.Second
)

type Runner interface {
	PrepareSession(context.Context, PrepareSessionRequest) (PrepareSessionResult, error)
	Run(context.Context, RunRequest) (RunResult, error)
	SetSessionConfigOption(context.Context, SetSessionConfigOptionRequest) (SetSessionConfigOptionResult, error)
	CloseSession(context.Context, string) error
	Shutdown(context.Context) error
}

type SessionManager struct {
	mu            sync.Mutex
	sessions      map[string]*acpSession
	starts        map[string]*sessionStart
	logger        *slog.Logger
	coordinator   *ApprovalCoordinator
	credentialEnv CredentialEnvProvider
	// metrics carries the AgentAdapterMetrics used by every
	// acpChatClient created from this manager. Optional — nil is
	// safe (every Record* method is nil-tolerant) and matches the
	// pre-existing constructor surface that older tests rely on.
	metrics *telemetry.AgentAdapterMetrics
	closed  bool
}

type CredentialEnvProvider func(ctx context.Context, adapterID string) []string

func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*acpSession),
		starts:   make(map[string]*sessionStart),
	}
}

func (m *SessionManager) SetLogger(logger *slog.Logger) {
	if logger == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logger = logger
}

// SetApprovalCoordinator installs the coordinator used to handle ACP
// RequestPermission calls. When unset, the legacy auto-approve
// behavior is preserved (matches existing tests + dev workflows that
// build a SessionManager without going through internal/config).
func (m *SessionManager) SetApprovalCoordinator(coordinator *ApprovalCoordinator) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.coordinator = coordinator
}

// SetAdapterMetrics installs the AgentAdapterMetrics used by every
// acpChatClient spawned from this manager (currently for the
// terminal-RPC-unsupported counter; probe metrics are wired separately
// via SetProbeMetrics). Nil is safe and matches the construction
// pattern used by tests that don't care about metrics.
func (m *SessionManager) SetAdapterMetrics(metrics *telemetry.AgentAdapterMetrics) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.metrics = metrics
}

func (m *SessionManager) SetCredentialEnvProvider(provider CredentialEnvProvider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.credentialEnv = provider
}

// shutdownCancelHook is invoked once per adapter id torn down via
// Shutdown so the handler can fire the agent-chat-cancelled counter
// with reason="shutdown". atomic.Pointer to keep Shutdown lock-free
// on the hot path; nil is the no-op default.
var shutdownCancelHook atomic.Pointer[func(adapterID string)]

// SetShutdownCancelHook installs the callback fired once per active
// session being torn down via SessionManager.Shutdown. The handler
// wires this so the agent-chat-cancelled counter fires with
// reason="shutdown" without coupling the agentadapters package to
// the handler's metrics struct directly.
func SetShutdownCancelHook(hook func(adapterID string)) {
	if hook == nil {
		shutdownCancelHook.Store(nil)
		return
	}
	shutdownCancelHook.Store(&hook)
}

// Coordinator returns the installed approval coordinator (or nil).
// HTTP handlers route operator resolve/cancel calls through this.
func (m *SessionManager) Coordinator() *ApprovalCoordinator {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.coordinator
}

func (m *SessionManager) PrepareSession(ctx context.Context, req PrepareSessionRequest) (PrepareSessionResult, error) {
	if m == nil {
		return PrepareSessionResult{}, fmt.Errorf("agent session manager is required")
	}
	adapter, ok := BuiltInByID(req.AdapterID)
	if !ok {
		return PrepareSessionResult{}, fmt.Errorf("agent adapter %q not found", req.AdapterID)
	}
	req.AdapterID = adapter.ID
	workspace, err := ValidateWorkspace(req.Workspace)
	if err != nil {
		return PrepareSessionResult{}, err
	}
	req.Workspace = workspace
	if strings.TrimSpace(req.SessionID) == "" {
		return PrepareSessionResult{}, fmt.Errorf("agent chat session id is required")
	}
	session, started, resumed, recovery, err := m.session(ctx, adapter, RunRequest{
		SessionID:               req.SessionID,
		AdapterID:               req.AdapterID,
		Workspace:               req.Workspace,
		PreviousNativeSessionID: req.PreviousNativeSessionID,
	})
	if err != nil {
		return PrepareSessionResult{}, err
	}
	return PrepareSessionResult{
		Adapter:         adapter,
		DriverKind:      DriverKindACP,
		NativeSessionID: session.nativeID,
		SessionStarted:  started,
		SessionResumed:  resumed,
		SessionRecovery: recovery,
		ConfigOptions:   session.configOptionsSnapshot(),
	}, nil
}

func (m *SessionManager) Run(ctx context.Context, req RunRequest) (RunResult, error) {
	if m == nil {
		return RunResult{}, fmt.Errorf("agent session manager is required")
	}
	adapter, ok := BuiltInByID(req.AdapterID)
	if !ok {
		return RunResult{}, fmt.Errorf("agent adapter %q not found", req.AdapterID)
	}
	req.AdapterID = adapter.ID
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		return RunResult{}, fmt.Errorf("prompt is required")
	}
	workspace, err := ValidateWorkspace(req.Workspace)
	if err != nil {
		return RunResult{}, err
	}
	req.Workspace = workspace
	if req.SessionID == "" {
		return RunResult{}, fmt.Errorf("agent chat session id is required")
	}
	if req.Timeout <= 0 {
		req.Timeout = 10 * time.Minute
	}
	if req.MaxOutputBytes <= 0 {
		req.MaxOutputBytes = 1024 * 1024
	}
	session, started, resumed, recovery, err := m.session(ctx, adapter, req)
	if err != nil {
		return RunResult{}, err
	}
	result, err := session.RunTurn(ctx, req)
	result.SessionStarted = started
	result.SessionResumed = resumed
	result.SessionRecovery = recovery
	return result, err
}

type sessionStart struct {
	done   chan struct{}
	cancel context.CancelFunc
}

func (m *SessionManager) session(ctx context.Context, adapter Adapter, req RunRequest) (*acpSession, bool, bool, string, error) {
	for {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return nil, false, false, "", fmt.Errorf("agent session manager is shut down")
		}
		existing := m.sessions[req.SessionID]
		if existing != nil && existing.adapter.ID == adapter.ID && existing.workspace == req.Workspace {
			m.mu.Unlock()
			return existing, false, false, "", nil
		}
		if start := m.starts[req.SessionID]; start != nil {
			done := start.done
			m.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return nil, false, false, "", ctx.Err()
			}
		}
		if m.starts == nil {
			m.starts = make(map[string]*sessionStart)
		}
		startCtx, startCancel := context.WithCancel(ctx)
		start := &sessionStart{done: make(chan struct{}), cancel: startCancel}
		m.starts[req.SessionID] = start
		logger := m.logger
		coordinator := m.coordinator
		metrics := m.metrics
		credentialEnv := m.credentialEnv
		m.mu.Unlock()

		var extraEnv []string
		if credentialEnv != nil {
			extraEnv = credentialEnv(startCtx, adapter.ID)
		}
		started, resumed, recovery, err := startACPSession(startCtx, adapter, req.SessionID, req.Workspace, req.PreviousNativeSessionID, logger, coordinator, metrics, extraEnv)
		startCancel()

		var previous *acpSession
		m.mu.Lock()
		delete(m.starts, req.SessionID)
		if err == nil && m.closed {
			close(start.done)
			m.mu.Unlock()
			_ = started.Close(context.Background())
			return nil, false, false, "", fmt.Errorf("agent session manager is shut down")
		}
		if err == nil {
			previous = m.sessions[req.SessionID]
			m.sessions[req.SessionID] = started
		}
		close(start.done)
		m.mu.Unlock()

		if previous != nil {
			_ = previous.Close(context.Background())
		}
		if err != nil {
			return nil, false, false, "", err
		}
		return started, true, resumed, recovery, nil
	}
}

func (m *SessionManager) CloseSession(ctx context.Context, sessionID string) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	session := m.sessions[sessionID]
	delete(m.sessions, sessionID)
	m.mu.Unlock()
	if session == nil {
		return nil
	}
	return session.Close(ctx)
}

func (m *SessionManager) Shutdown(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	items := make([]*acpSession, 0, len(m.sessions))
	for id, session := range m.sessions {
		items = append(items, session)
		delete(m.sessions, id)
	}
	starts := make([]*sessionStart, 0, len(m.starts))
	for _, start := range m.starts {
		starts = append(starts, start)
	}
	m.closed = true
	m.mu.Unlock()

	// Fire the shutdown cancellation hook once per active session
	// before tearing each down so dashboards see the operator-vs
	// shutdown split. No-op when no hook is installed.
	if hook := shutdownCancelHook.Load(); hook != nil {
		callback := *hook
		for _, session := range items {
			if session != nil {
				callback(session.adapter.ID)
			}
		}
	}

	for _, start := range starts {
		if start.cancel != nil {
			start.cancel()
		}
	}
	for _, start := range starts {
		select {
		case <-start.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	var firstErr error
	for _, session := range items {
		if err := session.Close(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

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

func startACPSession(ctx context.Context, adapter Adapter, sessionID, workspace, previousNativeSessionID string, logger *slog.Logger, coordinator *ApprovalCoordinator, metrics *telemetry.AgentAdapterMetrics, extraEnv []string) (*acpSession, bool, string, error) {
	command, err := resolveExecutable(adapter, exec.LookPath)
	if err != nil {
		return nil, false, "", err
	}
	args := append([]string(nil), adapter.Args...)
	cmd := exec.CommandContext(context.Background(), command, args...)
	configureCommandProcessGroup(cmd)
	cmd.Dir = workspace
	cmd.Env = mergeEnv(sanitizedEnv(os.Environ()), extraEnv)

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
	s.configOptions = make([]agentcontrols.ConfigOption, len(options))
	copy(s.configOptions, options)
}

func (s *acpSession) configOptionsSnapshot() []agentcontrols.ConfigOption {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	if s.configOptions == nil {
		return nil
	}
	options := make([]agentcontrols.ConfigOption, len(s.configOptions))
	copy(options, s.configOptions)
	return options
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

type acpChatClient struct {
	sessionID   string
	adapterID   string
	workspace   string
	coordinator *ApprovalCoordinator
	// metrics is optional; nil-safe across every Record* call.
	// Populated by the SessionManager when an *AgentAdapterMetrics
	// has been wired (see SessionManager.SetAdapterMetrics).
	metrics *telemetry.AgentAdapterMetrics

	mu   sync.Mutex
	turn *acpTurn
}

// terminalRPCUnsupported builds the typed JSON-RPC error returned by
// every terminal stub. The acp.RequestError carries code -32601
// ("Method not found") so JSON-RPC tooling that doesn't know about
// Hecate's sentinel can still classify the failure correctly; the
// wrap with ErrTerminalRPCUnsupported lets adapter callers detect
// the case via errors.Is without string-matching.
func terminalRPCUnsupported(method string) error {
	rpcErr := acp.NewMethodNotFound(method)
	return fmt.Errorf("%w: %w", ErrTerminalRPCUnsupported, rpcErr)
}

func (c *acpChatClient) setTurn(turn *acpTurn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.turn = turn
}

func (c *acpChatClient) clearTurn(turn *acpTurn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.turn == turn {
		c.turn = nil
	}
}

func (c *acpChatClient) currentTurn() *acpTurn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.turn
}

func (c *acpChatClient) SessionUpdate(_ context.Context, params acp.SessionNotification) error {
	turn := c.currentTurn()
	if turn == nil {
		return nil
	}
	turn.recordUpdate(params)
	return nil
}

func (c *acpChatClient) RequestPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	if c.coordinator != nil {
		return c.coordinator.RequestPermission(ctx, RecordingContext{
			SessionID: c.sessionID,
			AdapterID: c.adapterID,
			Workspace: c.workspace,
		}, params)
	}
	// Legacy auto-approve fallback. Preserved for callers that
	// construct an acpChatClient (or SessionManager) without an
	// approval coordinator — primarily existing unit tests and dev
	// scaffolding that pre-date the approval RFC.
	for _, option := range params.Options {
		if option.Kind == acp.PermissionOptionKindAllowOnce || option.Kind == acp.PermissionOptionKindAllowAlways {
			return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Selected: &acp.RequestPermissionOutcomeSelected{OptionId: option.OptionId}}}, nil
		}
	}
	if len(params.Options) > 0 {
		return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Selected: &acp.RequestPermissionOutcomeSelected{OptionId: params.Options[0].OptionId}}}, nil
	}
	return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}}}, nil
}

func (c *acpChatClient) ReadTextFile(_ context.Context, params acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	path, err := c.workspacePath(params.Path)
	if err != nil {
		return acp.ReadTextFileResponse{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return acp.ReadTextFileResponse{}, err
	}
	content := string(data)
	if params.Line != nil || params.Limit != nil {
		lines := strings.Split(content, "\n")
		start := 0
		if params.Line != nil && *params.Line > 0 {
			start = min(*params.Line-1, len(lines))
		}
		end := len(lines)
		if params.Limit != nil && *params.Limit > 0 && start+*params.Limit < end {
			end = start + *params.Limit
		}
		content = strings.Join(lines[start:end], "\n")
	}
	return acp.ReadTextFileResponse{Content: content}, nil
}

func (c *acpChatClient) WriteTextFile(_ context.Context, params acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	path, err := c.workspacePath(params.Path)
	if err != nil {
		return acp.WriteTextFileResponse{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return acp.WriteTextFileResponse{}, err
	}
	if err := os.WriteFile(path, []byte(params.Content), 0o644); err != nil {
		return acp.WriteTextFileResponse{}, err
	}
	return acp.WriteTextFileResponse{}, nil
}

func (c *acpChatClient) CreateTerminal(ctx context.Context, _ acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	c.metrics.RecordTerminalRPCUnsupported(ctx, c.adapterID, "create")
	return acp.CreateTerminalResponse{}, terminalRPCUnsupported("terminal/create")
}

func (c *acpChatClient) KillTerminal(ctx context.Context, _ acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	c.metrics.RecordTerminalRPCUnsupported(ctx, c.adapterID, "kill")
	return acp.KillTerminalResponse{}, terminalRPCUnsupported("terminal/kill")
}

func (c *acpChatClient) TerminalOutput(ctx context.Context, _ acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	c.metrics.RecordTerminalRPCUnsupported(ctx, c.adapterID, "output")
	return acp.TerminalOutputResponse{}, terminalRPCUnsupported("terminal/output")
}

func (c *acpChatClient) ReleaseTerminal(ctx context.Context, _ acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	c.metrics.RecordTerminalRPCUnsupported(ctx, c.adapterID, "release")
	return acp.ReleaseTerminalResponse{}, terminalRPCUnsupported("terminal/release")
}

func (c *acpChatClient) WaitForTerminalExit(ctx context.Context, _ acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	c.metrics.RecordTerminalRPCUnsupported(ctx, c.adapterID, "wait")
	return acp.WaitForTerminalExitResponse{}, terminalRPCUnsupported("terminal/wait")
}

func (c *acpChatClient) workspacePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(c.workspace, path)
	}
	clean := filepath.Clean(path)
	rel, err := filepath.Rel(c.workspace, clean)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes workspace", path)
	}
	return clean, nil
}

type acpTurn struct {
	output         limitedBuffer
	raw            limitedBuffer
	usage          Usage
	agentMessageID string
	// agentThoughtID is the ACP messageId carrying the current
	// `agent_thought_chunk` block. ACP emits thoughts as a chunk
	// stream, sharing a messageId across chunks of the same thought
	// and bumping it when a new thought block starts. We use it to
	// keep one merged "thinking" activity per block instead of one
	// per chunk; mergeAgentChatActivity dedupes by Activity.ID
	// downstream.
	agentThoughtID string
	// agentThoughtFallback is the source of truth for whether the
	// active block was opened with a Hecate-minted fallback id.
	// Boundary detection used to sniff the id's prefix, which a
	// non-spec-conformant adapter could spoof by sending a real
	// messageId that happened to look like the fallback shape;
	// tracking the property explicitly removes that risk.
	agentThoughtFallback bool
	// agentThoughtFallbackCount counts boundary-triggered fallback
	// episodes within this turn so each gets a unique Activity.ID
	// (`__fallback-1`, `__fallback-2`, …). See thoughtFallbackBlockID
	// for why uniqueness is load-bearing on the merge path.
	agentThoughtFallbackCount int
	agentThoughtText          strings.Builder
	agentThoughtTruncated     bool
	// toolKindByCall caches the last-known ToolKind for each
	// ToolCallId in this turn. ACP `SessionToolCallUpdate.Kind` is
	// optional — adapters may emit a kind on the initial ToolCall
	// and omit it on the matching completion update. Without the
	// cache, emitFileChangeActivities would compute kind == "" on
	// the completion update and skip per-file emission for an edit
	// that genuinely happened.
	toolKindByCall map[string]acp.ToolKind
	toolSeen       bool
	postToolText   bool
	onOutput       func(string)
	onActivity     func(Activity)

	mu sync.Mutex
}

func newACPTurn(maxOutput int64, onOutput func(string)) *acpTurn {
	if maxOutput <= 0 {
		maxOutput = 1024 * 1024
	}
	turn := &acpTurn{onOutput: onOutput}
	turn.output.limit = maxOutput
	turn.raw.limit = maxOutput
	return turn
}

func (t *acpTurn) setActivityCallback(onActivity func(Activity)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onActivity = onActivity
}

func (t *acpTurn) recordUpdate(params acp.SessionNotification) {
	raw, _ := json.Marshal(params)
	if len(raw) > 0 {
		t.appendRaw(append(raw, '\n'))
	}
	update := params.Update
	switch {
	case update.AgentMessageChunk != nil:
		t.appendAgentMessageChunk(update.AgentMessageChunk)
	case update.AgentThoughtChunk != nil:
		t.appendAgentThoughtChunk(update.AgentThoughtChunk)
	case update.ToolCall != nil:
		t.recordToolCall(update.ToolCall)
	case update.ToolCallUpdate != nil:
		t.recordToolCallUpdate(update.ToolCallUpdate)
	case update.Plan != nil:
		t.recordPlan(update.Plan)
	case update.UsageUpdate != nil:
		t.recordUsage(update.UsageUpdate)
	}
}

func (t *acpTurn) appendAgentMessageChunk(update *acp.SessionUpdateAgentMessageChunk) {
	if update == nil {
		return
	}
	text := contentBlockText(update.Content)
	if text == "" {
		return
	}

	var snapshot string
	t.mu.Lock()
	if t.toolSeen && !t.postToolText && isLikelyProgressNarration(t.output.String()) {
		t.output.Buffer.Reset()
		t.postToolText = true
	}
	if update.MessageId != nil {
		nextID := strings.TrimSpace(*update.MessageId)
		if nextID != "" {
			// ACP messageId is unstable but specifically exists to mark message
			// boundaries. Codex sends short progress narration as one assistant
			// message and the actual answer as another; when the id changes, the
			// transcript should follow the latest visible assistant message.
			if t.agentMessageID != "" && t.agentMessageID != nextID {
				t.output.Buffer.Reset()
			}
			t.agentMessageID = nextID
		}
	}
	_, _ = t.output.Write([]byte(text))
	snapshot = t.output.String()
	t.mu.Unlock()

	if t.onOutput != nil {
		t.onOutput(snapshot)
	}
}

// appendAgentThoughtChunk routes ACP `agent_thought_chunk` updates
// to the activity stream as `thinking` records. Thoughts are
// internal reasoning, not visible transcript text — `output` stays
// untouched. ACP streams a thought block as multiple chunks sharing
// a `messageId`; we accumulate chunks per messageId and emit one
// activity row per block, refreshing its `Detail` as new chunks
// arrive (mergeAgentChatActivity dedupes by Activity.ID downstream).
// Block boundaries are detected by the four-case transition table
// inside the function (real → real, real → empty, empty → real,
// continuation); the goal is that Activity.ID stays stable for the
// lifetime of every emitted block.
func (t *acpTurn) appendAgentThoughtChunk(update *acp.SessionUpdateAgentThoughtChunk) {
	if update == nil {
		return
	}
	text := contentBlockText(update.Content)
	if text == "" {
		return
	}

	t.mu.Lock()
	nextID := ""
	if update.MessageId != nil {
		nextID = strings.TrimSpace(*update.MessageId)
	}
	// Resolve the active block id with explicit boundary detection.
	// Each transition is decided by what we know now vs. what was
	// active before — the goal is that Activity.ID is stable for the
	// lifetime of every emitted block (mergeAgentChatActivity dedupes
	// by id downstream, so a mid-block id flip would split one
	// thought into two timeline rows or — worse — silently merge two
	// thoughts into one row).
	//
	// Cases:
	//   1. First chunk in the turn: adopt the real id when present;
	//      otherwise mint a counter-suffixed fallback id.
	//   2. Real id that differs from the active id: real-A → real-B
	//      is an explicit ACP-level block boundary.
	//   3. Empty id while a *real* id is active: real-A → ∅. Treat as
	//      a new block (defensive — adapters that consistently send
	//      messageIds shouldn't drop them mid-block, so an absence
	//      after a real id is more plausibly a new block than a
	//      continuation of the old one). Mint the next fallback
	//      counter so the new row never collides on Activity.ID
	//      with a prior fallback episode in the same turn —
	//      mergeAgentChatActivity replaces Detail wholesale on
	//      collision, so reusing an id would silently lose the
	//      earlier episode's reasoning.
	//   4. Empty id while a *fallback* id is active, OR matching
	//      real id, OR matching fallback id: continuation; same row.
	blockChanged := false
	switch {
	case t.agentThoughtID == "":
		blockChanged = true
		if nextID != "" {
			t.agentThoughtID = nextID
			t.agentThoughtFallback = false
		} else {
			t.agentThoughtFallbackCount++
			t.agentThoughtID = fmt.Sprintf("%s-%d", thoughtFallbackBlockID, t.agentThoughtFallbackCount)
			t.agentThoughtFallback = true
		}
	case nextID != "" && nextID != t.agentThoughtID:
		blockChanged = true
		t.agentThoughtID = nextID
		t.agentThoughtFallback = false
	case nextID == "" && !t.agentThoughtFallback:
		blockChanged = true
		t.agentThoughtFallbackCount++
		t.agentThoughtID = fmt.Sprintf("%s-%d", thoughtFallbackBlockID, t.agentThoughtFallbackCount)
		t.agentThoughtFallback = true
	}
	if blockChanged {
		t.agentThoughtText.Reset()
		t.agentThoughtTruncated = false
	}
	t.appendBoundedThoughtText(text)
	id := t.agentThoughtID
	detail := t.agentThoughtText.String()
	if t.agentThoughtTruncated {
		detail += thoughtTruncationSuffix
	}
	t.mu.Unlock()

	t.emitActivity(Activity{
		ID:     "thinking:" + id,
		Type:   "thinking",
		Status: "completed",
		Title:  "Thinking",
		Detail: detail,
	})
}

// appendBoundedThoughtText writes text into the thought
// accumulator, stopping at thoughtMaxBytesPerBlock. The cut is
// rolled back to the nearest UTF-8 rune boundary so the
// JSON-serialized Activity.Detail stays valid (slicing mid-rune
// would emit a stray continuation byte). Once the block is
// truncated, further chunks for the same block are dropped on the
// floor — the suffix in Detail tells the operator the row is
// partial; appending more truncated bytes would not help.
//
// Caller MUST hold t.mu.
func (t *acpTurn) appendBoundedThoughtText(text string) {
	if t.agentThoughtTruncated {
		return
	}
	remaining := thoughtMaxBytesPerBlock - t.agentThoughtText.Len()
	if remaining <= 0 {
		t.agentThoughtTruncated = true
		return
	}
	if len(text) <= remaining {
		t.agentThoughtText.WriteString(text)
		return
	}
	cut := remaining
	for cut > 0 && (text[cut]&0xC0) == 0x80 {
		cut--
	}
	if cut > 0 {
		t.agentThoughtText.WriteString(text[:cut])
	}
	t.agentThoughtTruncated = true
}

func (t *acpTurn) recordToolCall(update *acp.SessionUpdateToolCall) {
	if update == nil {
		return
	}
	t.markToolSeen()
	status := acpToolStatus(string(update.Status))
	t.rememberToolKind(string(update.ToolCallId), update.Kind)
	t.emitActivity(Activity{
		ID:     "tool:" + string(update.ToolCallId),
		Type:   "tool_call",
		Status: status,
		Kind:   string(update.Kind),
		Title:  firstNonEmpty(update.Title, string(update.ToolCallId)),
		Detail: toolCallDetail(update.Kind, update.Locations, update.Content, update.RawInput),
	})
	t.emitFileChangeActivities(string(update.ToolCallId), update.Kind, status, update.Locations)
}

func (t *acpTurn) recordToolCallUpdate(update *acp.SessionToolCallUpdate) {
	if update == nil {
		return
	}
	t.markToolSeen()
	title := ""
	if update.Title != nil {
		title = *update.Title
	}
	// SessionToolCallUpdate.Title is optional. mergeAgentChatActivity
	// drops an emission whose Title is empty when there is no prior
	// row with the same Activity.ID to merge into — that loses tool-call
	// state updates that arrive before (or instead of) a matching
	// SessionUpdateToolCall (e.g. an adapter that sends the start
	// event in a previous turn but a status update now). Default the
	// Title to the ToolCallId — the same fallback recordToolCall uses
	// at the start side — so the activity always carries something
	// renderable and never gets silently dropped on the merge path.
	if title == "" {
		title = string(update.ToolCallId)
	}
	status := ""
	if update.Status != nil {
		status = string(*update.Status)
	}
	// SessionToolCallUpdate.Kind is optional. Adapters routinely
	// emit kind on the initial ToolCall and drop it on the
	// completion update. Without the per-turn cache, a completed
	// edit whose update omits Kind would compute kind == "" and
	// skip emitFileChangeActivities — silently losing the per-file
	// rows for an edit that actually happened. Update the cache
	// when Kind is present so a later in_progress → completed
	// transition resolves correctly even if the adapter changes
	// its mind about the tool's category.
	var kind acp.ToolKind
	if update.Kind != nil {
		kind = *update.Kind
		t.rememberToolKind(string(update.ToolCallId), kind)
	} else {
		kind = t.lookupToolKind(string(update.ToolCallId))
	}
	normalizedStatus := acpToolStatus(status)
	t.emitActivity(Activity{
		ID:     "tool:" + string(update.ToolCallId),
		Type:   "tool_call",
		Status: normalizedStatus,
		Kind:   string(kind),
		Title:  title,
		Detail: toolCallDetail(kind, update.Locations, update.Content, update.RawInput),
	})
	t.emitFileChangeActivities(string(update.ToolCallId), kind, normalizedStatus, update.Locations)
}

// rememberToolKind caches the latest known kind for a tool call so
// a later update that omits SessionToolCallUpdate.Kind can still
// resolve the right category. Acquires t.mu internally — call
// sites MUST NOT hold t.mu (sync.Mutex is non-reentrant; reentry
// deadlocks). recordToolCall and recordToolCallUpdate match this
// pattern: they extract fields from the ACP update without holding
// t.mu and let each helper (markToolSeen, rememberToolKind,
// lookupToolKind, emitActivity) lock-and-release internally.
func (t *acpTurn) rememberToolKind(toolCallID string, kind acp.ToolKind) {
	if toolCallID == "" || kind == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.toolKindByCall == nil {
		t.toolKindByCall = make(map[string]acp.ToolKind)
	}
	t.toolKindByCall[toolCallID] = kind
}

// lookupToolKind returns the cached kind for a tool call, or the
// zero value if no kind was ever cached. Acquires t.mu internally;
// call sites MUST NOT hold t.mu (see rememberToolKind for the
// rationale).
func (t *acpTurn) lookupToolKind(toolCallID string) acp.ToolKind {
	if toolCallID == "" {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.toolKindByCall[toolCallID]
}

// emitFileChangeActivities surfaces per-file edits as their own
// activity records when a mutating tool call (kind = edit / delete /
// move) reaches the completed state. Today the UI synthesises a
// single end-of-turn `files_changed` summary from the captured Git
// diff stat (see handler_agent_chat.go); the activity-stream
// counterparts let operators see *which* files were touched as the
// agent works, not only after the turn settles. The diff-stat
// aggregate keeps its role — it covers the case where the adapter
// edits files outside an ACP-reported location.
//
// IDs are scoped per (tool_call, path) so duplicate updates from the
// same tool reach the same activity row in mergeAgentChatActivity
// instead of stacking. Read / search / execute / fetch / think /
// other tool kinds are NOT promoted — they don't change files.
func (t *acpTurn) emitFileChangeActivities(toolCallID string, kind acp.ToolKind, status string, locations []acp.ToolCallLocation) {
	if status != "completed" {
		return
	}
	if !isFileMutatingToolKind(kind) {
		return
	}
	// Aggregate by path so that multiple ToolCallLocation entries for
	// the same file (e.g. several edited line ranges in one call)
	// collapse to a single activity row instead of colliding on a
	// shared Activity.ID — mergeAgentChatActivity dedupes by ID
	// downstream, so two emissions with the same id would overwrite
	// each other's title and timestamp instead of stacking. We retain
	// insertion order: the first time we see a path defines its row's
	// position, and subsequent same-path entries fold their line
	// numbers into the existing accumulator.
	type pathAccum struct {
		path  string
		lines []int
	}
	seen := make(map[string]int, len(locations))
	accums := make([]pathAccum, 0, len(locations))
	for _, loc := range locations {
		path := strings.TrimSpace(loc.Path)
		if path == "" {
			continue
		}
		idx, ok := seen[path]
		if !ok {
			seen[path] = len(accums)
			idx = len(accums)
			accums = append(accums, pathAccum{path: path})
		}
		if loc.Line != nil && *loc.Line > 0 {
			accums[idx].lines = append(accums[idx].lines, *loc.Line)
		}
	}
	for _, acc := range accums {
		title := acc.path
		switch len(acc.lines) {
		case 0:
			// No line info — title is just the path.
		case 1:
			title = fmt.Sprintf("%s:%d", acc.path, acc.lines[0])
		default:
			title = fmt.Sprintf("%s (%s)", acc.path, summarizeFileChangeLines(acc.lines))
		}
		t.emitActivity(Activity{
			ID:     "file_change:" + toolCallID + ":" + acc.path,
			Type:   "file_change",
			Status: "completed",
			Kind:   string(kind),
			Title:  title,
			Detail: string(kind),
		})
	}
}

// summarizeFileChangeLines renders a comma-separated list of the
// first few line numbers, with a "+N more" tail when an edit touches
// many ranges in the same file. Mirrors the bounded summary style
// used by summarizeToolLocations so file_change titles read
// consistently with the underlying tool_call detail.
func summarizeFileChangeLines(lines []int) string {
	const maxShown = 3
	limit := len(lines)
	if limit > maxShown {
		limit = maxShown
	}
	parts := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		parts = append(parts, strconv.Itoa(lines[i]))
	}
	out := strings.Join(parts, ", ")
	if len(lines) > maxShown {
		out = fmt.Sprintf("%s, +%d more", out, len(lines)-maxShown)
	}
	return out
}

func isFileMutatingToolKind(kind acp.ToolKind) bool {
	switch kind {
	case acp.ToolKindEdit, acp.ToolKindDelete, acp.ToolKindMove:
		return true
	default:
		return false
	}
}

func (t *acpTurn) markToolSeen() {
	var snapshot *string
	t.mu.Lock()
	t.toolSeen = true
	if !t.postToolText && t.output.Len() > 0 && isLikelyProgressNarration(t.output.String()) {
		t.output.Buffer.Reset()
		empty := ""
		snapshot = &empty
	}
	onOutput := t.onOutput
	t.mu.Unlock()
	if snapshot != nil && onOutput != nil {
		onOutput(*snapshot)
	}
}

func isLikelyProgressNarration(text string) bool {
	text = strings.TrimSpace(strings.ToLower(text))
	if text == "" {
		return false
	}
	prefixes := []string{
		"i'll ",
		"i’ll ",
		"i will ",
		"i'm going to ",
		"i’m going to ",
		"i’m checking ",
		"i'm checking ",
		"i’ll check ",
		"i'll check ",
		"i’ll inspect ",
		"i'll inspect ",
		"let me ",
		"checking ",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func (t *acpTurn) recordPlan(update *acp.SessionUpdatePlan) {
	if update == nil {
		return
	}
	for index, entry := range update.Entries {
		t.emitActivity(Activity{
			ID:     fmt.Sprintf("plan:%d:%s", index, entry.Content),
			Type:   "plan",
			Status: string(entry.Status),
			Kind:   string(entry.Priority),
			Title:  entry.Content,
			Detail: string(entry.Priority),
		})
	}
}

func (t *acpTurn) emitActivity(activity Activity) {
	if strings.TrimSpace(activity.ID) == "" && strings.TrimSpace(activity.Title) == "" {
		return
	}
	t.mu.Lock()
	onActivity := t.onActivity
	t.mu.Unlock()
	if onActivity != nil {
		onActivity(activity)
	}
}

func acpToolStatus(status string) string {
	switch strings.TrimSpace(status) {
	case string(acp.ToolCallStatusPending):
		return "pending"
	case string(acp.ToolCallStatusInProgress):
		return "running"
	case string(acp.ToolCallStatusCompleted):
		return "completed"
	case string(acp.ToolCallStatusFailed):
		return "failed"
	default:
		return strings.TrimSpace(status)
	}
}

func toolCallDetail(kind acp.ToolKind, locations []acp.ToolCallLocation, content []acp.ToolCallContent, rawInput any) string {
	parts := make([]string, 0, 3)
	if kind != "" {
		parts = append(parts, string(kind))
	}
	if summary := summarizeToolRawInput(rawInput); summary != "" {
		parts = append(parts, summary)
	}
	if len(locations) > 0 {
		parts = append(parts, summarizeToolLocations(locations))
	}
	if len(content) > 0 {
		if summary := summarizeToolContent(content); summary != "" {
			parts = append(parts, summary)
		}
	}
	return strings.Join(parts, " · ")
}

func summarizeToolRawInput(rawInput any) string {
	if rawInput == nil {
		return ""
	}
	flattened := flattenRawInput(rawInput)
	for _, key := range []string{"command", "cmd", "shell_command", "script", "query", "path"} {
		if value := firstRawInputValue(flattened, key); value != "" {
			return trimToolSummary(value)
		}
	}
	if value := firstRawInputString(rawInput); value != "" {
		return trimToolSummary(value)
	}
	return ""
}

func flattenRawInput(value any) map[string]string {
	out := map[string]string{}
	var visit func(prefix string, current any)
	visit = func(prefix string, current any) {
		switch typed := current.(type) {
		case map[string]any:
			for key, child := range typed {
				next := key
				if prefix != "" {
					next = prefix + "." + key
				}
				visit(next, child)
			}
		case map[string]string:
			for key, child := range typed {
				next := key
				if prefix != "" {
					next = prefix + "." + key
				}
				out[strings.ToLower(next)] = child
			}
		case string:
			if prefix != "" {
				out[strings.ToLower(prefix)] = typed
			}
		case fmt.Stringer:
			if prefix != "" {
				out[strings.ToLower(prefix)] = typed.String()
			}
		}
	}
	visit("", value)
	return out
}

func firstRawInputValue(values map[string]string, suffix string) string {
	for key, value := range values {
		if key == suffix || strings.HasSuffix(key, "."+suffix) {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstRawInputString(value any) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}

func summarizeToolLocations(locations []acp.ToolCallLocation) string {
	const maxLocations = 3
	parts := make([]string, 0, min(len(locations), maxLocations))
	for i, location := range locations {
		if i >= maxLocations {
			break
		}
		if location.Line != nil && *location.Line > 0 {
			parts = append(parts, fmt.Sprintf("%s:%d", location.Path, *location.Line))
			continue
		}
		parts = append(parts, location.Path)
	}
	if len(locations) > maxLocations {
		parts = append(parts, fmt.Sprintf("+%d more", len(locations)-maxLocations))
	}
	return strings.Join(parts, ", ")
}

func summarizeToolContent(content []acp.ToolCallContent) string {
	var diffs, terminals int
	var textPreview string
	var textCount int
	for _, item := range content {
		switch {
		case item.Diff != nil:
			diffs++
		case item.Terminal != nil:
			terminals++
		case item.Content != nil:
			textCount++
			if textPreview == "" {
				textPreview = contentBlockText(item.Content.Content)
			}
		}
	}
	parts := make([]string, 0, 3)
	if diffs > 0 {
		parts = append(parts, pluralize(diffs, "diff"))
	}
	if terminals > 0 {
		parts = append(parts, pluralize(terminals, "terminal"))
	}
	if textPreview != "" {
		label := "output"
		if textCount > 1 {
			label = fmt.Sprintf("output 1/%d", textCount)
		}
		parts = append(parts, fmt.Sprintf("%s: %s", label, trimToolSummary(textPreview)))
	} else if textCount > 0 {
		parts = append(parts, pluralize(textCount, "output"))
	}
	return strings.Join(parts, ", ")
}

func trimToolSummary(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if utf8.RuneCountInString(value) <= 120 {
		return value
	}
	runes := []rune(value)
	return string(runes[:117]) + "..."
}

func pluralize(count int, singular string) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", singular)
	}
	return fmt.Sprintf("%d %ss", count, singular)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (t *acpTurn) recordUsage(update *acp.SessionUsageUpdate) {
	if update == nil {
		return
	}
	usage := Usage{
		ContextSize: update.Size,
		ContextUsed: update.Used,
	}
	if update.Cost != nil {
		usage.ReportedCostAmount = strconv.FormatFloat(update.Cost.Amount, 'f', -1, 64)
		usage.ReportedCostCurrency = strings.ToUpper(strings.TrimSpace(update.Cost.Currency))
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.usage = usage
}

func (t *acpTurn) appendRaw(data []byte) {
	if len(data) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	_, _ = t.raw.Write(data)
}

func (t *acpTurn) snapshot() (string, string, Usage) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.output.String(), t.raw.String(), t.usage
}

func (t *acpTurn) truncated() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.output.truncated || t.raw.truncated
}

func contentBlockText(block acp.ContentBlock) string {
	if block.Text != nil {
		return block.Text.Text
	}
	if block.ResourceLink != nil {
		return block.ResourceLink.Uri
	}
	return ""
}

func stderrSuffix(stderr string) string {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return ""
	}
	return ": " + stderr
}

func terminateProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if cmd.Cancel != nil {
		_ = cmd.Cancel()
	} else {
		_ = cmd.Process.Kill()
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}
