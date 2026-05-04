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
	"time"

	acp "github.com/coder/acp-go-sdk"
)

const DriverKindACP = "acp"

type Runner interface {
	Run(context.Context, RunRequest) (RunResult, error)
	CloseSession(context.Context, string) error
	Shutdown(context.Context) error
}

type SessionManager struct {
	mu       sync.Mutex
	sessions map[string]*acpSession
	starts   map[string]*sessionStart
	logger   *slog.Logger
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
	session, started, resumed, err := m.session(ctx, adapter, req)
	if err != nil {
		return RunResult{}, err
	}
	result, err := session.RunTurn(ctx, req)
	result.SessionStarted = started
	result.SessionResumed = resumed
	return result, err
}

type sessionStart struct {
	done chan struct{}
}

func (m *SessionManager) session(ctx context.Context, adapter Adapter, req RunRequest) (*acpSession, bool, bool, error) {
	for {
		m.mu.Lock()
		existing := m.sessions[req.SessionID]
		if existing != nil && existing.adapter.ID == adapter.ID && existing.workspace == req.Workspace {
			m.mu.Unlock()
			return existing, false, false, nil
		}
		if start := m.starts[req.SessionID]; start != nil {
			done := start.done
			m.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return nil, false, false, ctx.Err()
			}
		}
		if m.starts == nil {
			m.starts = make(map[string]*sessionStart)
		}
		start := &sessionStart{done: make(chan struct{})}
		m.starts[req.SessionID] = start
		logger := m.logger
		m.mu.Unlock()

		started, resumed, err := startACPSession(ctx, adapter, req.Workspace, req.PreviousNativeSessionID, logger)

		var previous *acpSession
		m.mu.Lock()
		delete(m.starts, req.SessionID)
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
			return nil, false, false, err
		}
		return started, true, resumed, nil
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
	m.mu.Unlock()
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

	turnMu sync.Mutex
}

func startACPSession(ctx context.Context, adapter Adapter, workspace, previousNativeSessionID string, logger *slog.Logger) (*acpSession, bool, error) {
	command, err := resolveExecutable(adapter, exec.LookPath)
	if err != nil {
		return nil, false, err
	}
	args := append([]string(nil), adapter.Args...)
	cmd := exec.CommandContext(context.Background(), command, args...)
	configureCommandProcessGroup(cmd)
	cmd.Dir = workspace
	cmd.Env = sanitizedEnvForAdapter(adapter.ID, os.Environ())

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, false, fmt.Errorf("create ACP stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false, fmt.Errorf("create ACP stdout pipe: %w", err)
	}
	var stderr limitedBuffer
	stderr.limit = 256 * 1024
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, false, fmt.Errorf("start ACP adapter %q: %w", adapter.ID, err)
	}

	client := &acpChatClient{workspace: workspace}
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
		return nil, false, fmt.Errorf("initialize ACP adapter %q: %w%s", adapter.ID, err, stderrSuffix(stderr.String()))
	}

	nativeID := ""
	resumed := false
	previousNativeSessionID = strings.TrimSpace(previousNativeSessionID)
	if previousNativeSessionID != "" && initResp.AgentCapabilities.LoadSession {
		loadCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		_, err = conn.LoadSession(loadCtx, acp.LoadSessionRequest{
			SessionId:  acp.SessionId(previousNativeSessionID),
			Cwd:        workspace,
			McpServers: []acp.McpServer{},
		})
		cancel()
		if err == nil {
			nativeID = previousNativeSessionID
			resumed = true
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
			return nil, false, fmt.Errorf("create ACP session for %q: %w%s", adapter.ID, err, stderrSuffix(stderr.String()))
		}
		nativeID = string(created.SessionId)
	}
	return &acpSession{
		adapter:   adapter,
		workspace: workspace,
		cmd:       cmd,
		conn:      conn,
		client:    client,
		nativeID:  nativeID,
	}, resumed, nil
}

func (s *acpSession) RunTurn(ctx context.Context, req RunRequest) (RunResult, error) {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()

	started := time.Now().UTC()
	turn := newACPTurn(req.MaxOutputBytes, req.OnOutput)
	s.client.setTurn(turn)
	defer s.client.clearTurn(turn)

	promptCtx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()
	_, runErr := s.conn.Prompt(promptCtx, acp.PromptRequest{
		SessionId: acp.SessionId(s.nativeID),
		Prompt:    []acp.ContentBlock{acp.TextBlock(req.Prompt)},
	})
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
	return captureACPTurnResult(ctx, s.adapter, req, s.nativeID, output, raw, usage, exitCode, started, completed, runErr)
}

func (s *acpSession) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if s.conn != nil && s.nativeID != "" {
		closeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		_, _ = s.conn.CloseSession(closeCtx, acp.CloseSessionRequest{SessionId: acp.SessionId(s.nativeID)})
		cancel()
	}
	if s.cmd != nil {
		terminateProcess(s.cmd)
	}
	return nil
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
	workspace string

	mu   sync.Mutex
	turn *acpTurn
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

func (c *acpChatClient) RequestPermission(_ context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
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

func (c *acpChatClient) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, fmt.Errorf("ACP terminal requests are not supported by Hecate agent chat yet")
}

func (c *acpChatClient) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, fmt.Errorf("ACP terminal requests are not supported by Hecate agent chat yet")
}

func (c *acpChatClient) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, fmt.Errorf("ACP terminal requests are not supported by Hecate agent chat yet")
}

func (c *acpChatClient) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, fmt.Errorf("ACP terminal requests are not supported by Hecate agent chat yet")
}

func (c *acpChatClient) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, fmt.Errorf("ACP terminal requests are not supported by Hecate agent chat yet")
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
	onOutput       func(string)

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

func (t *acpTurn) recordUpdate(params acp.SessionNotification) {
	raw, _ := json.Marshal(params)
	if len(raw) > 0 {
		t.appendRaw(append(raw, '\n'))
	}
	update := params.Update
	switch {
	case update.AgentMessageChunk != nil:
		t.appendAgentMessageChunk(update.AgentMessageChunk)
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
