package agentadapters

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	acp "github.com/coder/acp-go-sdk"

	"github.com/hecatehq/hecate/internal/agentcontrols"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/internal/workspacefs"
)

type acpChatClient struct {
	sessionID           string
	adapterID           string
	workspace           string
	coordinator         *ApprovalCoordinator
	onAvailableCommands func([]agentcontrols.Command)
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
	if params.Update.AvailableCommandsUpdate != nil && c.onAvailableCommands != nil {
		c.onAvailableCommands(agentcontrols.FromACPCommands(params.Update.AvailableCommandsUpdate.AvailableCommands))
	}
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
	fsys, path, err := c.workspaceFS(params.Path)
	if err != nil {
		return acp.ReadTextFileResponse{}, err
	}
	data, _, err := fsys.ReadFile(path)
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
	fsys, path, err := c.workspaceFS(params.Path)
	if err != nil {
		return acp.WriteTextFileResponse{}, err
	}
	if _, err := fsys.WriteFile(path, []byte(params.Content), 0o644); err != nil {
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

func (c *acpChatClient) workspaceFS(path string) (*workspacefs.FS, string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, "", fmt.Errorf("path is required")
	}
	fsys, err := workspacefs.New(c.workspace)
	if err != nil {
		return nil, "", err
	}
	if filepath.IsAbs(path) {
		root := fsys.Root()
		clean := filepath.Clean(path)
		rel, err := filepath.Rel(root, clean)
		if err != nil {
			return nil, "", err
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, "", fmt.Errorf("path %q escapes workspace", path)
		}
		if _, err := fsys.Resolve(rel); err != nil {
			return nil, "", err
		}
		return fsys, rel, nil
	}
	if _, err := fsys.Resolve(path); err != nil {
		return nil, "", err
	}
	return fsys, path, nil
}
