package agentadapters

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	acp "github.com/coder/acp-go-sdk"

	"github.com/hecatehq/hecate/internal/agentcontrols"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/internal/workspace"
	"github.com/hecatehq/hecate/internal/workspacecoord"
	"github.com/hecatehq/hecate/internal/workspacefs"
)

type acpChatClient struct {
	sessionID           string
	adapterID           string
	workspace           string
	coordinator         *ApprovalCoordinator
	onAvailableCommands func([]agentcontrols.Command)
	onConfigOptions     func([]agentcontrols.ConfigOption)
	// metrics is optional; nil-safe across every Record* call.
	// Populated by the SessionManager when an *AgentAdapterMetrics
	// has been wired (see SessionManager.SetAdapterMetrics).
	metrics *telemetry.AgentAdapterMetrics

	mu   sync.Mutex
	turn *acpTurn

	promptStageMu         sync.RWMutex
	promptStageNamespaces map[*acpPromptStageNamespace]struct{}
	// promptStageRedactorSet is session-owned rather than namespace-owned.
	// Cleanup proof may release a filesystem fence while the live ACP peer can
	// still send delayed typed metadata containing a previously disclosed alias.
	promptStageRedactorSet map[*acpPromptRedactor]struct{}

	terminalMu           sync.Mutex
	terminalsEnabled     bool
	workspaceCoordinator *workspacecoord.Registry
	terminalsClosed      bool
	terminalCreates      int
	terminalCreatesDone  chan struct{}
	openTerminal         func(context.Context, workspace.TerminalOptions) (workspace.Terminal, error)
	terminals            map[string]*acpTerminal
	terminalPreviews     map[string]string
}

func (c *acpChatClient) setTurn(turn *acpTurn) {
	if turn != nil {
		turn.setTerminalOutputLookup(c.terminalToolOutputPreview)
	}
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

func (c *acpChatClient) registerPromptStageNamespace(stage *acpPromptStage, redactor *acpPromptRedactor) {
	if c == nil || stage == nil || stage.dir == "" || stage.namespace != nil {
		return
	}
	namespace := newACPPromptStageNamespace(stage.dir)
	if current := currentPrivateACPPromptStageDirectory(stage.identity); current != "" {
		namespace.addDirectory(current)
	}
	namespace.register = func(dir string) {
		c.promptStageMu.Lock()
		defer c.promptStageMu.Unlock()
		if _, registered := c.promptStageNamespaces[namespace]; registered {
			namespace.addDirectory(dir)
		}
	}
	namespace.release = func() {
		c.promptStageMu.Lock()
		delete(c.promptStageNamespaces, namespace)
		c.promptStageMu.Unlock()
	}
	c.promptStageMu.Lock()
	if c.promptStageNamespaces == nil {
		c.promptStageNamespaces = make(map[*acpPromptStageNamespace]struct{})
	}
	c.promptStageNamespaces[namespace] = struct{}{}
	if redactor != nil {
		if c.promptStageRedactorSet == nil {
			c.promptStageRedactorSet = make(map[*acpPromptRedactor]struct{})
		}
		c.promptStageRedactorSet[redactor] = struct{}{}
	}
	c.promptStageMu.Unlock()
	stage.namespace = namespace
	setPrivateACPPromptStageQuarantineObserver(stage.identity, namespace.registerDirectory)
}

func (c *acpChatClient) containsPromptStagePath(path string) bool {
	if c == nil {
		return false
	}
	c.promptStageMu.RLock()
	defer c.promptStageMu.RUnlock()
	return c.containsPromptStagePathLocked(path)
}

// containsPromptStagePathLocked requires promptStageMu to be held for reading
// or writing. Keeping that lock through WorkspaceFS fallback prevents cleanup
// from registering and renaming a quarantine alias between a deny miss and the
// actual filesystem operation.
func (c *acpChatClient) containsPromptStagePathLocked(path string) bool {
	paths := acpPromptStageCallbackPaths(path, c.workspace)
	for namespace := range c.promptStageNamespaces {
		for _, candidate := range paths {
			if namespace.contains(candidate) {
				return true
			}
		}
	}
	return false
}

func (c *acpChatClient) withPromptStageWorkspaceFallback(path, deniedMessage string, fallback func() error) error {
	c.promptStageMu.RLock()
	defer c.promptStageMu.RUnlock()
	if c.containsPromptStagePathLocked(path) {
		return errors.New(deniedMessage)
	}
	return fallback()
}

func (c *acpChatClient) promptStageRedactors(turn *acpTurn) []*acpPromptRedactor {
	var redactors []*acpPromptRedactor
	seen := make(map[*acpPromptRedactor]struct{})
	if turn != nil {
		if redactor := turn.redactor(); redactor != nil {
			redactors = append(redactors, redactor)
			seen[redactor] = struct{}{}
		}
	}
	c.promptStageMu.RLock()
	for redactor := range c.promptStageRedactorSet {
		if _, ok := seen[redactor]; !ok {
			redactors = append(redactors, redactor)
			seen[redactor] = struct{}{}
		}
	}
	c.promptStageMu.RUnlock()
	return redactors
}

func (c *acpChatClient) redactPromptStageConfigOptions(options []agentcontrols.ConfigOption) []agentcontrols.ConfigOption {
	if c == nil {
		return options
	}
	for _, redactor := range c.promptStageRedactors(nil) {
		options = redactor.redactConfigOptions(options)
	}
	return options
}

func (c *acpChatClient) redactPromptStageError(err error) error {
	if c == nil {
		return err
	}
	return redactACPPromptStageError(c.promptStageRedactors(nil), err)
}

func redactACPPromptStageError(redactors []*acpPromptRedactor, err error) error {
	for _, redactor := range redactors {
		err = redactor.redactError(err)
	}
	return err
}

func (c *acpChatClient) SessionUpdate(_ context.Context, params acp.SessionNotification) error {
	turn := c.currentTurn()
	redactors := c.promptStageRedactors(turn)
	typedControlUpdate := params.Update.AvailableCommandsUpdate != nil || params.Update.ConfigOptionUpdate != nil
	if params.Update.AvailableCommandsUpdate != nil && c.onAvailableCommands != nil {
		commands := agentcontrols.FromACPCommands(params.Update.AvailableCommandsUpdate.AvailableCommands)
		for _, redactor := range redactors {
			commands = redactor.redactCommands(commands)
		}
		c.onAvailableCommands(commands)
	}
	if params.Update.ConfigOptionUpdate != nil && c.onConfigOptions != nil {
		options := agentcontrols.FromACPOptions(params.Update.ConfigOptionUpdate.ConfigOptions)
		for _, redactor := range redactors {
			options = redactor.redactConfigOptions(options)
		}
		c.onConfigOptions(options)
	}
	if turn == nil {
		return nil
	}
	if typedControlUpdate && len(redactors) > 0 {
		// The typed projections above are the durable/operator-visible authority.
		// Do not also retain the original peer-controlled wire record in a later
		// turn whose own prompt did not install the historical stage redactor.
		return nil
	}
	turn.recordUpdate(params)
	return nil
}

func (c *acpChatClient) RequestPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	redactors := c.promptStageRedactors(c.currentTurn())
	for _, redactor := range redactors {
		safeParams, err := redactor.redactRequestPermission(params)
		if err != nil {
			// Fail before the coordinator sees the request: an approval payload that
			// cannot be safely reconstructed must never reach durable storage.
			return acp.RequestPermissionResponse{}, err
		}
		params = safeParams
	}
	if c.coordinator != nil {
		response, requestErr := c.coordinator.RequestPermission(ctx, RecordingContext{
			SessionID: c.sessionID,
			AdapterID: c.adapterID,
			Workspace: c.workspace,
		}, params)
		return response, redactACPPromptStageError(redactors, requestErr)
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

func (c *acpChatClient) UnstableCreateElicitation(context.Context, acp.UnstableCreateElicitationRequest) (acp.UnstableCreateElicitationResponse, error) {
	// Hecate does not advertise elicitation support yet. Return the ACP
	// "cancel" outcome so experimental agents degrade gracefully instead of
	// seeing JSON-RPC method-not-found.
	return acp.NewUnstableCreateElicitationResponseCancel(), nil
}

func (c *acpChatClient) UnstableCompleteElicitation(context.Context, acp.UnstableCompleteElicitationNotification) error {
	return nil
}

func (c *acpChatClient) ReadTextFile(_ context.Context, params acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	if turn := c.currentTurn(); turn != nil {
		if data, ok := turn.promptFile(params.Path); ok {
			if !utf8.Valid(data) {
				return acp.ReadTextFileResponse{}, fmt.Errorf("staged prompt input is not UTF-8 text")
			}
			return acp.ReadTextFileResponse{Content: selectTextFileLines(string(data), params.Line, params.Limit)}, nil
		}
		if turn.containsPromptStagePath(params.Path) {
			return acp.ReadTextFileResponse{}, fmt.Errorf("staged prompt input is not available")
		}
	}
	var data []byte
	err := c.withPromptStageWorkspaceFallback(params.Path, "staged prompt input is not available", func() error {
		fsys, path, err := c.workspaceFS(params.Path)
		if err != nil {
			return err
		}
		data, _, err = fsys.ReadFile(path)
		return err
	})
	if err != nil {
		return acp.ReadTextFileResponse{}, err
	}
	return acp.ReadTextFileResponse{Content: selectTextFileLines(string(data), params.Line, params.Limit)}, nil
}

func selectTextFileLines(content string, line, limit *int) string {
	if line == nil && limit == nil {
		return content
	}
	lines := strings.Split(content, "\n")
	start := 0
	if line != nil && *line > 0 {
		start = min(*line-1, len(lines))
	}
	end := len(lines)
	if limit != nil && *limit > 0 && start+*limit < end {
		end = start + *limit
	}
	return strings.Join(lines[start:end], "\n")
}

func (c *acpChatClient) WriteTextFile(_ context.Context, params acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	if turn := c.currentTurn(); turn != nil && turn.containsPromptStagePath(params.Path) {
		return acp.WriteTextFileResponse{}, fmt.Errorf("staged prompt inputs are read-only")
	}
	err := c.withPromptStageWorkspaceFallback(params.Path, "staged prompt inputs are read-only", func() error {
		fsys, path, err := c.workspaceFS(params.Path)
		if err != nil {
			return err
		}
		_, err = fsys.WriteFile(path, []byte(params.Content), 0o644)
		return err
	})
	if err != nil {
		return acp.WriteTextFileResponse{}, err
	}
	return acp.WriteTextFileResponse{}, nil
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
