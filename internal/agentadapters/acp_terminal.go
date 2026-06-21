package agentadapters

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	acp "github.com/coder/acp-go-sdk"

	"github.com/hecatehq/hecate/internal/sandbox"
	"github.com/hecatehq/hecate/internal/workspace"
)

const defaultACPTerminalOutputByteLimit = 1024 * 1024

const (
	acpTerminalAllowOnceOptionID    = "hecate_terminal_allow_once"
	acpTerminalAllowAlwaysOptionID  = "hecate_terminal_allow_always"
	acpTerminalRejectOnceOptionID   = "hecate_terminal_reject_once"
	acpTerminalRejectAlwaysOptionID = "hecate_terminal_reject_always"
)

var nextACPTerminalApprovalID atomic.Uint64

type acpTerminal struct {
	id       string
	term     workspace.Terminal
	output   *acpTerminalOutputBuffer
	done     chan struct{}
	exitCode *int
	waitErr  error
}

func (c *acpChatClient) CreateTerminal(ctx context.Context, params acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	if !c.terminalsEnabled {
		return acp.CreateTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalCreate)
	}
	command := strings.TrimSpace(params.Command)
	if command == "" {
		return acp.CreateTerminalResponse{}, fmt.Errorf("command is required")
	}
	cwd, err := c.terminalWorkingDirectory(params.Cwd)
	if err != nil {
		return acp.CreateTerminalResponse{}, err
	}
	env, err := acpTerminalEnv(params.Env)
	if err != nil {
		return acp.CreateTerminalResponse{}, err
	}
	limit := defaultACPTerminalOutputByteLimit
	if params.OutputByteLimit != nil && *params.OutputByteLimit > 0 {
		limit = *params.OutputByteLimit
	}
	if err := c.approveTerminalCreate(ctx, params, cwd, limit); err != nil {
		return acp.CreateTerminalResponse{}, err
	}
	ws := workspace.NewLocalWorkspace()
	term, err := ws.OpenTerminal(ctx, workspace.TerminalOptions{
		Command:          command,
		Args:             append([]string(nil), params.Args...),
		WorkingDirectory: cwd,
		Policy: workspace.Policy{
			AllowedRoot: c.workspace,
			Network:     true,
		},
		Env: env,
	})
	if err != nil {
		return acp.CreateTerminalResponse{}, err
	}

	item := &acpTerminal{
		id:     term.ID(),
		term:   term,
		output: newACPTerminalOutputBuffer(limit),
		done:   make(chan struct{}),
	}
	c.storeTerminal(item)
	go item.watch()
	return acp.CreateTerminalResponse{TerminalId: item.id}, nil
}

func (c *acpChatClient) KillTerminal(ctx context.Context, params acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	if !c.terminalsEnabled {
		return acp.KillTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalKill)
	}
	item, err := c.lookupTerminal(params.TerminalId)
	if err != nil {
		return acp.KillTerminalResponse{}, err
	}
	if err := item.term.Kill(ctx); err != nil {
		return acp.KillTerminalResponse{}, err
	}
	return acp.KillTerminalResponse{}, nil
}

func (c *acpChatClient) TerminalOutput(_ context.Context, params acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	if !c.terminalsEnabled {
		return acp.TerminalOutputResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalOutput)
	}
	item, err := c.lookupTerminal(params.TerminalId)
	if err != nil {
		return acp.TerminalOutputResponse{}, err
	}
	output, truncated := item.output.snapshot()
	resp := acp.TerminalOutputResponse{
		Output:    output,
		Truncated: truncated,
	}
	select {
	case <-item.done:
		resp.ExitStatus = &acp.TerminalExitStatus{ExitCode: item.exitCode}
	default:
	}
	return resp, nil
}

func (c *acpChatClient) ReleaseTerminal(ctx context.Context, params acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	if !c.terminalsEnabled {
		return acp.ReleaseTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalRelease)
	}
	item, err := c.removeTerminal(params.TerminalId)
	if err != nil {
		return acp.ReleaseTerminalResponse{}, err
	}
	if err := item.term.Close(ctx); err != nil {
		return acp.ReleaseTerminalResponse{}, err
	}
	return acp.ReleaseTerminalResponse{}, nil
}

func (c *acpChatClient) WaitForTerminalExit(ctx context.Context, params acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	if !c.terminalsEnabled {
		return acp.WaitForTerminalExitResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalWaitForExit)
	}
	item, err := c.lookupTerminal(params.TerminalId)
	if err != nil {
		return acp.WaitForTerminalExitResponse{}, err
	}
	select {
	case <-item.done:
	case <-ctx.Done():
		return acp.WaitForTerminalExitResponse{}, ctx.Err()
	}
	return acp.WaitForTerminalExitResponse{ExitCode: item.exitCode}, item.waitErr
}

func (c *acpChatClient) approveTerminalCreate(ctx context.Context, params acp.CreateTerminalRequest, cwd string, outputByteLimit int) error {
	if c.coordinator == nil {
		return acp.NewRequestCancelled(map[string]any{"reason": "terminal approval coordinator is required"})
	}
	kind := acp.ToolKindExecute
	status := acp.ToolCallStatusPending
	title := "Run terminal command"
	commandLine := terminalCommandLine(params.Command, params.Args)
	if commandLine != "" {
		title = "Run " + commandLine
	}
	resp, err := c.coordinator.RequestPermission(ctx, RecordingContext{
		SessionID: c.sessionID,
		AdapterID: c.adapterID,
		Workspace: c.workspace,
	}, acp.RequestPermissionRequest{
		SessionId: params.SessionId,
		Options: []acp.PermissionOption{
			{OptionId: acpTerminalAllowOnceOptionID, Kind: acp.PermissionOptionKindAllowOnce, Name: "Allow once"},
			{OptionId: acpTerminalAllowAlwaysOptionID, Kind: acp.PermissionOptionKindAllowAlways, Name: "Always allow terminal commands"},
			{OptionId: acpTerminalRejectOnceOptionID, Kind: acp.PermissionOptionKindRejectOnce, Name: "Reject once"},
			{OptionId: acpTerminalRejectAlwaysOptionID, Kind: acp.PermissionOptionKindRejectAlways, Name: "Always reject terminal commands"},
		},
		ToolCall: acp.ToolCallUpdate{
			ToolCallId: acp.ToolCallId(fmt.Sprintf("hecate_terminal_create_%d", nextACPTerminalApprovalID.Add(1))),
			Kind:       &kind,
			Status:     &status,
			Title:      &title,
			RawInput: map[string]any{
				"command":           strings.TrimSpace(params.Command),
				"args":              append([]string(nil), params.Args...),
				"cwd":               cwd,
				"env_names":         acpTerminalEnvNames(params.Env),
				"output_byte_limit": outputByteLimit,
			},
		},
	})
	if err != nil {
		return err
	}
	if resp.Outcome.Selected == nil {
		return acp.NewRequestCancelled(map[string]any{"reason": "terminal command was not approved"})
	}
	switch string(resp.Outcome.Selected.OptionId) {
	case acpTerminalAllowOnceOptionID, acpTerminalAllowAlwaysOptionID:
		return nil
	default:
		return acp.NewRequestCancelled(map[string]any{"reason": "terminal command was rejected"})
	}
}

func terminalCommandLine(command string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	if command = strings.TrimSpace(command); command != "" {
		parts = append(parts, command)
	}
	for _, arg := range args {
		if arg = strings.TrimSpace(arg); arg != "" {
			parts = append(parts, arg)
		}
	}
	return strings.Join(parts, " ")
}

func (c *acpChatClient) terminalWorkingDirectory(cwd *string) (string, error) {
	root := strings.TrimSpace(c.workspace)
	if root == "" {
		return "", fmt.Errorf("workspace is required")
	}
	if cwd == nil || strings.TrimSpace(*cwd) == "" {
		return root, nil
	}
	value := strings.TrimSpace(*cwd)
	if filepath.IsAbs(value) {
		value = filepath.Clean(value)
	} else {
		value = filepath.Join(root, value)
	}
	return sandbox.ResolveWorkingDirectory(value, sandbox.Policy{AllowedRoot: root})
}

func acpTerminalEnv(vars []acp.EnvVariable) (map[string]string, error) {
	if len(vars) == 0 {
		return nil, nil
	}
	env := make(map[string]string, len(vars))
	for _, item := range vars {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			return nil, fmt.Errorf("terminal env variable name is required")
		}
		if strings.Contains(name, "=") {
			return nil, fmt.Errorf("terminal env variable name %q is invalid", item.Name)
		}
		env[name] = item.Value
	}
	return env, nil
}

func acpTerminalEnvNames(vars []acp.EnvVariable) []string {
	if len(vars) == 0 {
		return nil
	}
	names := make([]string, 0, len(vars))
	for _, item := range vars {
		name := strings.TrimSpace(item.Name)
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

func (c *acpChatClient) storeTerminal(item *acpTerminal) {
	c.terminalMu.Lock()
	defer c.terminalMu.Unlock()
	if c.terminals == nil {
		c.terminals = make(map[string]*acpTerminal)
	}
	c.terminals[item.id] = item
}

func (c *acpChatClient) lookupTerminal(id string) (*acpTerminal, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("terminal id is required")
	}
	c.terminalMu.Lock()
	defer c.terminalMu.Unlock()
	item := c.terminals[id]
	if item == nil {
		return nil, fmt.Errorf("terminal %q not found", id)
	}
	return item, nil
}

func (c *acpChatClient) removeTerminal(id string) (*acpTerminal, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("terminal id is required")
	}
	c.terminalMu.Lock()
	defer c.terminalMu.Unlock()
	item := c.terminals[id]
	if item == nil {
		return nil, fmt.Errorf("terminal %q not found", id)
	}
	delete(c.terminals, id)
	return item, nil
}

func (c *acpChatClient) closeTerminals(ctx context.Context) error {
	c.terminalMu.Lock()
	items := make([]*acpTerminal, 0, len(c.terminals))
	for id, item := range c.terminals {
		items = append(items, item)
		delete(c.terminals, id)
	}
	c.terminalMu.Unlock()

	var firstErr error
	for _, item := range items {
		if err := item.term.Close(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (t *acpTerminal) watch() {
	outputDone := make(chan struct{})
	go func() {
		defer close(outputDone)
		for chunk := range t.term.Output() {
			t.output.append(chunk.Text)
		}
	}()
	result, err := t.term.WaitForExit(context.Background())
	<-outputDone
	code := result.ExitCode
	t.exitCode = &code
	t.waitErr = err
	close(t.done)
}

type acpTerminalOutputBuffer struct {
	mu        sync.Mutex
	limit     int
	output    string
	truncated bool
}

func newACPTerminalOutputBuffer(limit int) *acpTerminalOutputBuffer {
	if limit <= 0 {
		limit = defaultACPTerminalOutputByteLimit
	}
	return &acpTerminalOutputBuffer{limit: limit}
}

func (b *acpTerminalOutputBuffer) append(text string) {
	if text == "" {
		return
	}
	text = strings.ToValidUTF8(text, "\uFFFD")
	b.mu.Lock()
	defer b.mu.Unlock()
	b.output += text
	if len(b.output) <= b.limit {
		return
	}
	b.truncated = true
	drop := len(b.output) - b.limit
	for drop < len(b.output) && !utf8.RuneStart(b.output[drop]) {
		drop++
	}
	b.output = b.output[drop:]
}

func (b *acpTerminalOutputBuffer) snapshot() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.output, b.truncated
}
