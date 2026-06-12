package agentadapters

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hecatehq/hecate/internal/telemetry"
)

type SessionManager struct {
	mu          sync.Mutex
	sessions    map[string]*acpSession
	starts      map[string]*sessionStart
	logger      *slog.Logger
	coordinator *ApprovalCoordinator
	// metrics carries the AgentAdapterMetrics used by every
	// acpChatClient created from this manager. Optional — nil is
	// safe (every Record* method is nil-tolerant) and matches the
	// pre-existing constructor surface that older tests rely on.
	metrics *telemetry.AgentAdapterMetrics
	closed  bool
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
	session, started, resumed, recovery, err := m.session(ctx, adapter, RunRequest{
		SessionID:               req.SessionID,
		AdapterID:               req.AdapterID,
		Workspace:               req.Workspace,
		PreviousNativeSessionID: req.PreviousNativeSessionID,
		ConfigOptions:           req.ConfigOptions,
	})
	if err != nil {
		return PrepareSessionResult{}, err
	}
	availableCommands, availableCommandsUpdated := session.availableCommandsSnapshot()
	return PrepareSessionResult{
		Adapter:                  adapter,
		DriverKind:               DriverKindACP,
		NativeSessionID:          session.nativeID,
		SessionStarted:           started,
		SessionResumed:           resumed,
		SessionRecovery:          recovery,
		ConfigOptions:            session.configOptionsSnapshot(),
		AvailableCommands:        availableCommands,
		AvailableCommandsUpdated: availableCommandsUpdated,
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
	if err := validateLaunchConfig(adapter, req.ConfigOptions); err != nil {
		return RunResult{}, err
	}
	adapter = adapterWithLaunchConfig(adapter, req.ConfigOptions)
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
		if existing != nil && existing.adapter.ID == adapter.ID && existing.workspace == req.Workspace && sameArgs(existing.adapter.Args, adapter.Args) {
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
		m.mu.Unlock()

		started, resumed, recovery, err := startACPSession(startCtx, adapter, req.SessionID, req.Workspace, req.PreviousNativeSessionID, req.ConfigOptions, logger, coordinator, metrics)
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
