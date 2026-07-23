package agentadapters

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/hecatehq/hecate/internal/secrets"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/internal/workspacecoord"
	"github.com/hecatehq/hecate/pkg/types"
)

type SessionManager struct {
	mu           sync.Mutex
	sessions     map[string]*acpSession
	starts       map[string]*sessionStart
	logger       *slog.Logger
	coordinator  *ApprovalCoordinator
	secretCipher secrets.Cipher
	// metrics carries the AgentAdapterMetrics used by every
	// acpChatClient created from this manager. Optional — nil is
	// safe (every Record* method is nil-tolerant) and matches the
	// pre-existing constructor surface that older tests rely on.
	metrics              *telemetry.AgentAdapterMetrics
	onAvailableCommands  func(AvailableCommandsUpdate)
	workspaceCoordinator *workspacecoord.Registry
	terminalSupport      bool
	closed               bool
}

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

func (m *SessionManager) SetSecretCipher(cipher secrets.Cipher) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.secretCipher = cipher
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

func (m *SessionManager) SetAvailableCommandsUpdateHook(hook func(AvailableCommandsUpdate)) {
	m.mu.Lock()
	m.onAvailableCommands = hook
	sessions := make([]*acpSession, 0, len(m.sessions))
	if hook != nil && !m.closed {
		for _, session := range m.sessions {
			sessions = append(sessions, session)
		}
	}
	m.mu.Unlock()

	// Handler construction normally installs this hook before sessions start,
	// but the public setter also supports dynamic runner wiring. Replaying the
	// current snapshots ensures a catalog learned before the hook existed is
	// not silently lost. activateSessionAvailableCommands rechecks peer
	// identity before persistence, so sessions replaced after the copy are safe.
	for _, session := range sessions {
		m.activateSessionAvailableCommands(session)
	}
}

// activateSessionAvailableCommands releases a session's retained ACP command
// snapshot only after the manager has installed that exact peer. The callback
// checks identity while holding m.mu, which serializes publication with every
// detach, replacement, and shutdown path below.
func (m *SessionManager) activateSessionAvailableCommands(session *acpSession) {
	if m == nil || session == nil {
		return
	}
	session.activateAvailableCommands(func(update AvailableCommandsUpdate) {
		m.publishAvailableCommands(session, update)
	})
}

func (m *SessionManager) publishAvailableCommands(session *acpSession, update AvailableCommandsUpdate) {
	if m == nil || session == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || update.SessionID != session.sessionID || m.sessions[update.SessionID] != session {
		return
	}
	if hook := m.onAvailableCommands; hook != nil {
		// This hook is Hecate's persistence boundary. Keep m.mu through the call
		// so a previous peer cannot pass the identity check and then write after
		// a replacement or close removes it from m.sessions.
		session.publishAvailableCommands(update, hook)
	}
}

// SetTerminalSupportEnabled controls whether ACP sessions advertise and honor
// client-side terminal/* callbacks. It is intentionally opt-in because
// terminal/create is a command-execution surface.
func (m *SessionManager) SetTerminalSupportEnabled(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.terminalSupport = enabled
}

// SetWorkspaceCoordinator installs the process-scoped registry used by ACP
// terminal callbacks. A terminal receives its own writer lease before spawn so
// it remains visible to destructive workspace operations even after the ACP
// turn that created it has returned. Wire it before preparing or running
// sessions; like the other manager launch settings, an existing session keeps
// the composition it started with.
func (m *SessionManager) SetWorkspaceCoordinator(registry *workspacecoord.Registry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.workspaceCoordinator = registry
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
	if _, err := validateRemoteCredentialForRequest(ctx, adapter); err != nil {
		return PrepareSessionResult{}, err
	}
	if err := validateLaunchConfig(adapter, req.ConfigOptions); err != nil {
		return PrepareSessionResult{}, err
	}
	adapter = adapterWithLaunchConfig(adapter, req.ConfigOptions)
	req.AdapterID = adapter.ID
	workspace, err := ValidateWorkspace(req.Workspace)
	if err != nil {
		return PrepareSessionResult{}, err
	}
	req.Workspace = workspace
	if strings.TrimSpace(req.SessionID) == "" {
		return PrepareSessionResult{}, fmt.Errorf("agent chat session id is required")
	}
	mcpServers, err := m.resolveMCPServerConfigs(req.MCPServers)
	if err != nil {
		return PrepareSessionResult{}, err
	}
	req.MCPServers = mcpServers
	session, started, resumed, recovery, err := m.session(ctx, adapter, RunRequest{
		SessionID:               req.SessionID,
		AdapterID:               req.AdapterID,
		Workspace:               req.Workspace,
		PreviousNativeSessionID: req.PreviousNativeSessionID,
		ConfigOptions:           req.ConfigOptions,
		MCPServers:              req.MCPServers,
	})
	if err != nil {
		return PrepareSessionResult{}, err
	}
	availableCommands, availableCommandsKnown := session.availableCommandsSnapshot()
	return PrepareSessionResult{
		Adapter:                adapter,
		DriverKind:             DriverKindACP,
		NativeSessionID:        session.nativeID,
		AgentInfo:              session.agentInfoSnapshot(),
		SessionStarted:         started,
		SessionResumed:         resumed,
		SessionRecovery:        recovery,
		ConfigOptions:          session.configOptionsSnapshot(),
		AvailableCommands:      availableCommands,
		AvailableCommandsKnown: availableCommandsKnown,
	}, nil
}

func (m *SessionManager) Run(ctx context.Context, req RunRequest) (RunResult, error) {
	if m == nil {
		return RunResult{}, fmt.Errorf("agent session manager is required")
	}
	if err := ctx.Err(); err != nil {
		return RunResult{}, err
	}
	adapter, ok := BuiltInByID(req.AdapterID)
	if !ok {
		return RunResult{}, fmt.Errorf("agent adapter %q not found", req.AdapterID)
	}
	if _, err := validateRemoteCredentialForRequest(ctx, adapter); err != nil {
		return RunResult{}, err
	}
	if err := validateLaunchConfig(adapter, req.ConfigOptions); err != nil {
		return RunResult{}, err
	}
	adapter = adapterWithLaunchConfig(adapter, req.ConfigOptions)
	req.AdapterID = adapter.ID
	normalizedPrompt, err := normalizePromptInput(req.Prompt)
	if err != nil {
		return RunResult{}, err
	}
	req.Prompt = normalizedPrompt
	defer clearPromptInput(&req.Prompt)
	workspace, err := ValidateWorkspace(req.Workspace)
	if err != nil {
		return RunResult{}, err
	}
	req.Workspace = workspace
	if req.SessionID == "" {
		return RunResult{}, fmt.Errorf("agent chat session id is required")
	}
	mcpServers, err := m.resolveMCPServerConfigs(req.MCPServers)
	if err != nil {
		return RunResult{}, err
	}
	req.MCPServers = mcpServers
	if req.Timeout <= 0 {
		req.Timeout = 10 * time.Minute
	}
	if req.MaxOutputBytes <= 0 {
		req.MaxOutputBytes = 1024 * 1024
	}
	for {
		session, started, resumed, recovery, err := m.session(ctx, adapter, req)
		if err != nil {
			if session == nil {
				return RunResult{}, err
			}
			return decorateSessionRunResult(sessionRunResult(session), session, started, resumed, recovery), err
		}
		session.turnMu.Lock()
		if !m.sessionIsCurrent(req.SessionID, session) {
			session.turnMu.Unlock()
			if recovery != "" {
				return decorateSessionRunResult(sessionRunResult(session), session, started, resumed, recovery),
					fmt.Errorf("agent session changed after process-scoped native-session replacement was persisted")
			}
			continue
		}
		if recovery != "" {
			replacement := NativeSessionReplacement{
				PreviousNativeSessionID: strings.TrimSpace(req.PreviousNativeSessionID),
				NativeSessionID:         session.nativeID,
				Reason:                  recovery,
			}
			emitNativeSessionRecovery(req.OnActivity, replacement)
		}

		attemptReq := req
		attemptReq.Prompt = clonePromptInput(req.Prompt)
		attemptReq.OnActivity = func(activity Activity) {
			if req.OnActivity != nil {
				req.OnActivity(activity)
			}
		}
		result, runErr := session.runTurnLocked(ctx, attemptReq)
		clearPromptInput(&attemptReq.Prompt)
		if !canReplaceMissingNativeSession(ctx, req, result, runErr) {
			session.turnMu.Unlock()
			return decorateSessionRunResult(result, session, started, resumed, recovery), runErr
		}

		start, reserveErr := m.reserveNativeSessionReplacement(ctx, req.SessionID, session)
		session.turnMu.Unlock()
		if reserveErr != nil {
			return decorateSessionRunResult(result, session, started, resumed, recovery), reserveErr
		}
		fresh, replacement, replaceErr := m.completeNativeSessionReplacement(adapter, req, session, start)
		if replaceErr != nil {
			failed := decorateSessionRunResult(result, session, started, resumed, recovery)
			if replacement.NativeSessionID != "" {
				failed.NativeSessionID = replacement.NativeSessionID
				failed.SessionStarted = true
				failed.SessionResumed = false
				failed.SessionRecovery = replacement.Reason
			}
			return failed, replaceErr
		}
		// completeNativeSessionReplacement returns the fresh turn lock held so
		// queued callers cannot disclose another prompt before this retry.
		emitNativeSessionRecovery(req.OnActivity, replacement)
		if err := ctx.Err(); err != nil {
			fresh.turnMu.Unlock()
			cancelled := decorateSessionRunResult(result, fresh, true, false, replacement.Reason)
			cancelled.NativeSessionID = replacement.NativeSessionID
			return cancelled, err
		}
		retryReq := req
		retryReq.Prompt = clonePromptInput(req.Prompt)
		retryReq.AllowNativeSessionReplacement = false
		retryReq.OnNativeSessionReplaced = nil
		result, runErr = fresh.runTurnLocked(ctx, retryReq)
		clearPromptInput(&retryReq.Prompt)
		fresh.turnMu.Unlock()
		return decorateSessionRunResult(result, fresh, true, false, replacement.Reason), runErr
	}
}

func emitNativeSessionRecovery(onActivity func(Activity), replacement NativeSessionReplacement) {
	if onActivity == nil {
		return
	}
	onActivity(Activity{
		ID:     "native-session-replacement:" + replacement.NativeSessionID,
		Type:   "session_recovery",
		Status: "completed",
		Title:  "Started fresh external session",
		Detail: replacement.Reason,
	})
}

const nativeSessionReplacementReason = "the native conversation was missing before any successful external-agent turn; Hecate persisted a fresh ACP session before retrying"

func decorateSessionRunResult(result RunResult, session *acpSession, started, resumed bool, recovery string) RunResult {
	result.AgentInfo = session.agentInfoSnapshot()
	result.SessionStarted = started
	result.SessionResumed = resumed
	result.SessionRecovery = recovery
	return result
}

func sessionRunResult(session *acpSession) RunResult {
	commands, commandsKnown := session.availableCommandsSnapshot()
	return RunResult{
		Adapter:                session.adapter,
		DriverKind:             DriverKindACP,
		NativeSessionID:        session.nativeID,
		AgentInfo:              session.agentInfoSnapshot(),
		ConfigOptions:          session.configOptionsSnapshot(),
		AvailableCommands:      commands,
		AvailableCommandsKnown: commandsKnown,
	}
}

func canReplaceMissingNativeSession(ctx context.Context, req RunRequest, result RunResult, runErr error) bool {
	return ctx.Err() == nil &&
		req.AllowNativeSessionReplacement &&
		req.OnNativeSessionReplaced != nil &&
		strings.TrimSpace(result.Output) == "" &&
		result.promptCommandFailureLifecycle &&
		strings.TrimSpace(result.DiffStat) == "" &&
		strings.TrimSpace(result.Diff) == "" &&
		isNativeSessionMissingError(runErr)
}

func isNativeSessionMissingError(err error) bool {
	var rpcErr *acp.RequestError
	if !errors.As(err, &rpcErr) || err != rpcErr || rpcErr.Code != -32000 || rpcErr.Message != "prompt command failed" {
		return false
	}
	data, ok := rpcErr.Data.(map[string]any)
	if !ok {
		return false
	}
	kind, _ := data["errorKind"].(string)
	return kind == "native_session_missing"
}

func (m *SessionManager) resolveMCPServerConfigs(configs []types.MCPServerConfig) ([]types.MCPServerConfig, error) {
	if len(configs) == 0 {
		return configs, nil
	}
	m.mu.Lock()
	cipher := m.secretCipher
	m.mu.Unlock()
	return resolveMCPServerConfigs(configs, cipher)
}

type sessionStart struct {
	ctx    context.Context
	done   chan struct{}
	cancel context.CancelFunc
}

func (m *SessionManager) sessionIsCurrent(sessionID string, session *acpSession) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.closed && m.sessions[sessionID] == session
}

func (m *SessionManager) reserveNativeSessionReplacement(ctx context.Context, sessionID string, stale *acpSession) (*sessionStart, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, fmt.Errorf("agent session manager is shut down")
	}
	if m.sessions[sessionID] != stale {
		return nil, fmt.Errorf("agent session changed before native-session replacement")
	}
	if m.starts[sessionID] != nil {
		return nil, fmt.Errorf("agent session replacement is already in progress")
	}
	startCtx, cancel := context.WithCancel(ctx)
	start := &sessionStart{ctx: startCtx, done: make(chan struct{}), cancel: cancel}
	m.starts[sessionID] = start
	delete(m.sessions, sessionID)
	return start, nil
}

// completeNativeSessionReplacement returns the fresh session with turnMu held.
// The caller must unlock it after retrying or observing cancellation.
func (m *SessionManager) completeNativeSessionReplacement(adapter Adapter, req RunRequest, stale *acpSession, start *sessionStart) (*acpSession, NativeSessionReplacement, error) {
	m.mu.Lock()
	logger := m.logger
	coordinator := m.coordinator
	metrics := m.metrics
	workspaceCoordinator := m.workspaceCoordinator
	terminalSupport := m.terminalSupport
	m.mu.Unlock()
	fresh, _, _, err := startACPSession(start.ctx, adapter, req.SessionID, req.Workspace, "", req.ConfigOptions, req.MCPServers, logger, coordinator, metrics, workspaceCoordinator, terminalSupport)
	if err != nil {
		start.cancel()
		m.abortNativeSessionReplacement(req.SessionID, start, stale)
		return nil, NativeSessionReplacement{}, fmt.Errorf("start replacement ACP session: %w", err)
	}
	replacement := NativeSessionReplacement{
		PreviousNativeSessionID: stale.nativeID,
		NativeSessionID:         fresh.nativeID,
		Reason:                  nativeSessionReplacementReason,
	}
	if err := start.ctx.Err(); err != nil {
		start.cancel()
		closeACPSessionBounded(fresh)
		m.abortNativeSessionReplacement(req.SessionID, start, stale)
		return nil, NativeSessionReplacement{}, err
	}
	if err := req.OnNativeSessionReplaced(replacement); err != nil {
		start.cancel()
		closeACPSessionBounded(fresh)
		m.abortNativeSessionReplacement(req.SessionID, start, stale)
		return nil, NativeSessionReplacement{}, fmt.Errorf("persist native-session replacement: %w", err)
	}
	// Persistence is the commit point. After it succeeds, finish installing
	// the fresh session even if the request is cancelled: reverting to stale
	// in-memory state would disagree with the durable native ID. Run checks the
	// caller context before retrying, so cancellation still prevents a second
	// prompt disclosure.
	start.cancel()

	fresh.turnMu.Lock()
	m.mu.Lock()
	if m.closed || m.starts[req.SessionID] != start {
		m.mu.Unlock()
		fresh.turnMu.Unlock()
		closeACPSessionBounded(fresh)
		closeACPSessionBounded(stale)
		m.mu.Lock()
		if m.starts[req.SessionID] == start {
			delete(m.starts, req.SessionID)
		}
		m.mu.Unlock()
		close(start.done)
		return nil, replacement, fmt.Errorf("agent session manager shut down after native-session replacement was persisted")
	}
	m.sessions[req.SessionID] = fresh
	m.mu.Unlock()
	m.activateSessionAvailableCommands(fresh)
	closeACPSessionBounded(stale)
	m.mu.Lock()
	if m.starts[req.SessionID] == start {
		delete(m.starts, req.SessionID)
	}
	m.mu.Unlock()
	close(start.done)
	return fresh, replacement, nil
}

func (m *SessionManager) abortNativeSessionReplacement(sessionID string, start *sessionStart, stale *acpSession) {
	m.mu.Lock()
	closed := m.closed
	restored := false
	if m.starts[sessionID] == start {
		delete(m.starts, sessionID)
		if !closed {
			m.sessions[sessionID] = stale
			restored = true
		}
	}
	m.mu.Unlock()
	if restored {
		m.activateSessionAvailableCommands(stale)
	}
	if closed {
		closeACPSessionBounded(stale)
	}
	close(start.done)
}

func closeACPSessionBounded(session *acpSession) {
	ctx, cancel := context.WithTimeout(context.Background(), acpShutdownCloseTimeout)
	defer cancel()
	_ = session.Close(ctx)
}

func (m *SessionManager) session(ctx context.Context, adapter Adapter, req RunRequest) (*acpSession, bool, bool, string, error) {
	for {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return nil, false, false, "", fmt.Errorf("agent session manager is shut down")
		}
		existing := m.sessions[req.SessionID]
		if existing != nil && existing.adapter.ID == adapter.ID && existing.workspace == req.Workspace && sameArgs(existing.adapter.Args, adapter.Args) && sameMCPServerConfigs(existing.mcpServers, req.MCPServers) {
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
		start := &sessionStart{ctx: startCtx, done: make(chan struct{}), cancel: startCancel}
		m.starts[req.SessionID] = start
		logger := m.logger
		coordinator := m.coordinator
		metrics := m.metrics
		workspaceCoordinator := m.workspaceCoordinator
		terminalSupport := m.terminalSupport
		m.mu.Unlock()

		started, resumed, recovery, err := startACPSession(startCtx, adapter, req.SessionID, req.Workspace, req.PreviousNativeSessionID, req.ConfigOptions, req.MCPServers, logger, coordinator, metrics, workspaceCoordinator, terminalSupport)
		committedReplacement := false
		if err == nil && recovery != "" {
			replacement := NativeSessionReplacement{
				PreviousNativeSessionID: strings.TrimSpace(req.PreviousNativeSessionID),
				NativeSessionID:         started.nativeID,
				Reason:                  recovery,
			}
			switch {
			case req.OnNativeSessionReplaced == nil || replacement.PreviousNativeSessionID == "" || replacement.NativeSessionID == "" || replacement.PreviousNativeSessionID == replacement.NativeSessionID:
				err = fmt.Errorf("persist process-scoped native-session replacement: replacement callback and distinct native session ids are required")
			case startCtx.Err() != nil:
				err = startCtx.Err()
			default:
				// Call once and retain the exact failure without redisclosing the
				// prompt. The callback is the durable commit point.
				if persistErr := req.OnNativeSessionReplaced(replacement); persistErr != nil {
					err = fmt.Errorf("persist process-scoped native-session replacement: %w", persistErr)
				} else {
					committedReplacement = true
				}
			}
		}
		startCancel()

		if err != nil {
			if started != nil {
				closeACPSessionBounded(started)
			}
			m.mu.Lock()
			if m.starts[req.SessionID] == start {
				delete(m.starts, req.SessionID)
			}
			m.mu.Unlock()
			close(start.done)
			return nil, false, false, "", err
		}

		var previous *acpSession
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			closeACPSessionBounded(started)
			m.mu.Lock()
			if m.starts[req.SessionID] == start {
				delete(m.starts, req.SessionID)
			}
			m.mu.Unlock()
			close(start.done)
			if committedReplacement {
				return started, true, false, recovery, fmt.Errorf("agent session manager shut down after native-session replacement was persisted")
			}
			return nil, false, false, "", fmt.Errorf("agent session manager is shut down")
		}
		if m.starts[req.SessionID] != start {
			m.mu.Unlock()
			closeACPSessionBounded(started)
			close(start.done)
			if committedReplacement {
				return started, true, false, recovery, fmt.Errorf("agent session start changed after native-session replacement was persisted")
			}
			return nil, false, false, "", fmt.Errorf("agent session start changed before publication")
		}
		previous = m.sessions[req.SessionID]
		m.sessions[req.SessionID] = started
		delete(m.starts, req.SessionID)
		m.mu.Unlock()
		m.activateSessionAvailableCommands(started)

		if previous != nil {
			closeACPSessionBounded(previous)
		}
		close(start.done)
		return started, true, resumed, recovery, nil
	}
}

func sameArgs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (m *SessionManager) CloseSession(ctx context.Context, sessionID string) error {
	if m == nil {
		return nil
	}
	session := m.takeSession(sessionID)
	if session == nil {
		return nil
	}
	return session.Close(ctx)
}

func (m *SessionManager) DeleteSession(ctx context.Context, sessionID string) error {
	if m == nil {
		return nil
	}
	session := m.takeSession(sessionID)
	if session == nil {
		return nil
	}
	return session.Delete(ctx)
}

func (m *SessionManager) takeSession(sessionID string) *acpSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.sessions[sessionID]
	delete(m.sessions, sessionID)
	return session
}

func (m *SessionManager) Shutdown(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
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

	for _, start := range starts {
		if start.cancel != nil {
			start.cancel()
		}
	}
	var firstErr error
waitForStarts:
	for _, start := range starts {
		select {
		case <-start.done:
		case <-ctx.Done():
			firstErr = ctx.Err()
			break waitForStarts
		}
	}

	for _, session := range items {
		if err := session.Close(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
