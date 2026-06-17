package chatapp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/agentcontrols"
	"github.com/hecatehq/hecate/internal/apperrors"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/pkg/types"
)

var (
	ErrStoreNotConfigured      = errors.New("agent chat store is not configured")
	ErrRunnerNotConfigured     = errors.New("agent chat runner is not configured")
	ErrExternalSessionOnly     = errors.New("agent chat config options are only available for external-agent sessions")
	ErrHecateSessionOnly       = errors.New("Hecate Chat settings are not available for external-agent sessions")
	ErrNoSettingsProvided      = errors.New("no settings provided")
	ErrContentRequired         = errors.New("content is required")
	ErrExecutionModeInvalid    = errors.New("execution_mode must be hecate_task or external_agent")
	ErrExternalCannotRunHecate = errors.New("external agent sessions cannot run Hecate Chat turns")
	ErrHecateCannotRunExternal = errors.New("Hecate Chat sessions cannot run external-agent turns")
	ErrSessionNotFound         = errors.New("agent chat session not found")
	ErrSessionIDRequired       = errors.New("session id is required")
	ErrTitleRequired           = errors.New("request must include title")
	ErrTitleEmpty              = errors.New("title cannot be set to an empty string")
	ErrNothingToCompact        = errors.New("chat transcript has no older context to compact")
)

type ValidationError = apperrors.ValidationError

func Validation(err error) error {
	return apperrors.Validation(err)
}

func IsValidationError(err error) bool {
	return apperrors.IsValidationError(err)
}

type SessionStore interface {
	Create(ctx context.Context, session chat.Session) (chat.Session, error)
	Get(ctx context.Context, id string) (chat.Session, bool, error)
	List(ctx context.Context) ([]chat.Session, error)
	UpdateSession(ctx context.Context, id string, update func(*chat.Session)) (chat.Session, error)
	Delete(ctx context.Context, id string) error
}

type TaskStore interface {
	GetTask(ctx context.Context, id string) (types.Task, bool, error)
	UpdateTask(ctx context.Context, task types.Task) (types.Task, error)
}

type AgentRunner interface {
	PrepareSession(context.Context, agentadapters.PrepareSessionRequest) (agentadapters.PrepareSessionResult, error)
	CloseSession(context.Context, string) error
	SetSessionConfigOption(context.Context, agentadapters.SetSessionConfigOptionRequest) (agentadapters.SetSessionConfigOptionResult, error)
}

type Application struct {
	store               SessionStore
	taskStore           TaskStore
	runner              AgentRunner
	prepareTimeout      time.Duration
	configOptionTimeout time.Duration
}

type Options struct {
	Store               SessionStore
	TaskStore           TaskStore
	Runner              AgentRunner
	PrepareTimeout      time.Duration
	ConfigOptionTimeout time.Duration
}

type CreateSessionCommand struct {
	Session         chat.Session
	PrepareExternal bool
}

type CreateSessionResult struct {
	Session chat.Session
}

type DeleteSessionCommand struct {
	Session     chat.Session
	CloseNative bool
}

type CloseNativeSessionCommand struct {
	Session chat.Session
}

type CloseNativeSessionResult struct {
	Session chat.Session
}

type SetConfigOptionCommand struct {
	Session  chat.Session
	ConfigID string
	Value    any
}

type SetConfigOptionResult struct {
	Session chat.Session
}

type SetHecateSettingsCommand struct {
	Session    chat.Session
	RTKEnabled *bool
}

type SetHecateSettingsResult struct {
	Session chat.Session
}

type RenameSessionCommand struct {
	ID    string
	Title *string
}

type CompactSessionCommand struct {
	ID               string
	RetainMessages   int
	MinMessages      int
	HecateOnly       bool
	RequireCompacted bool
	Now              time.Time
}

type CompactSessionSummaryFunc func(context.Context, chat.Session, chat.CompactTranscriptResult) (chat.ContextSummary, error)

type SessionResult struct {
	Session chat.Session
}

type ListSessionsResult struct {
	Sessions []chat.Session
}

type MessageLimits struct {
	MaxTurnsPerSession int
	MaxSessionDuration time.Duration
	IdleTimeout        time.Duration
}

type AdmitMessageCommand struct {
	Session       chat.Session
	Content       string
	ExecutionMode string
	ToolsEnabled  *bool
	Limits        MessageLimits
	Now           time.Time
}

type AdmitMessageResult struct {
	Content       string
	ExecutionMode string
	ToolsEnabled  bool
}

type MessageDispatchRoute string

const (
	MessageDispatchHecateTask    MessageDispatchRoute = "hecate_task"
	MessageDispatchDirectModel   MessageDispatchRoute = "direct_model"
	MessageDispatchExternalAgent MessageDispatchRoute = "external_agent"
)

type MessageDispatchPlan struct {
	Content       string
	ExecutionMode string
	ToolsEnabled  bool
	Route         MessageDispatchRoute
}

type MessageLimitError struct {
	Code      string
	Message   string
	Limit     int
	LimitMS   int64
	StartedAt time.Time
	UpdatedAt time.Time
	TurnsUsed int
}

func (e MessageLimitError) Error() string {
	return e.Message
}

type ExternalPrepareError struct {
	Err error
}

func (e ExternalPrepareError) Error() string {
	if e.Err == nil {
		return "external agent prepare failed"
	}
	return e.Err.Error()
}

func (e ExternalPrepareError) Unwrap() error {
	return e.Err
}

func New(opts Options) *Application {
	return &Application{
		store:               opts.Store,
		taskStore:           opts.TaskStore,
		runner:              opts.Runner,
		prepareTimeout:      opts.PrepareTimeout,
		configOptionTimeout: opts.ConfigOptionTimeout,
	}
}

func (app *Application) ListSessions(ctx context.Context) (*ListSessionsResult, error) {
	if app == nil || app.store == nil {
		return nil, ErrStoreNotConfigured
	}
	sessions, err := app.store.List(ctx)
	if err != nil {
		return nil, err
	}
	return &ListSessionsResult{Sessions: sessions}, nil
}

func (app *Application) GetSession(ctx context.Context, id string) (*SessionResult, error) {
	if app == nil || app.store == nil {
		return nil, ErrStoreNotConfigured
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, Validation(ErrSessionIDRequired)
	}
	session, ok, err := app.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrSessionNotFound
	}
	return &SessionResult{Session: session}, nil
}

func (app *Application) RenameSession(ctx context.Context, cmd RenameSessionCommand) (*SessionResult, error) {
	if app == nil || app.store == nil {
		return nil, ErrStoreNotConfigured
	}
	id := strings.TrimSpace(cmd.ID)
	if id == "" {
		return nil, Validation(ErrSessionIDRequired)
	}
	if cmd.Title == nil {
		return nil, Validation(ErrTitleRequired)
	}
	title := strings.TrimSpace(*cmd.Title)
	if title == "" {
		return nil, Validation(ErrTitleEmpty)
	}
	if _, ok, err := app.store.Get(ctx, id); err != nil {
		return nil, err
	} else if !ok {
		return nil, ErrSessionNotFound
	}
	updated, err := app.store.UpdateSession(ctx, id, func(item *chat.Session) {
		item.Title = title
	})
	if err != nil {
		return nil, err
	}
	return &SessionResult{Session: updated}, nil
}

func (app *Application) CompactSession(ctx context.Context, cmd CompactSessionCommand) (*SessionResult, error) {
	return app.CompactSessionWithSummary(ctx, cmd, nil)
}

func (app *Application) CompactSessionWithSummary(ctx context.Context, cmd CompactSessionCommand, summarize CompactSessionSummaryFunc) (*SessionResult, error) {
	if app == nil || app.store == nil {
		return nil, ErrStoreNotConfigured
	}
	id := strings.TrimSpace(cmd.ID)
	if id == "" {
		return nil, Validation(ErrSessionIDRequired)
	}
	session, ok, err := app.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrSessionNotFound
	}
	if cmd.HecateOnly && isExternalSession(session) {
		return nil, ErrHecateSessionOnly
	}
	result := chat.CompactTranscriptSummary(session, chat.CompactTranscriptOptions{
		Now:            cmd.Now,
		RetainMessages: cmd.RetainMessages,
		MinMessages:    cmd.MinMessages,
	})
	if !result.Compacted {
		if cmd.RequireCompacted {
			return nil, ErrNothingToCompact
		}
		return &SessionResult{Session: session}, nil
	}
	summary := result.Summary
	if summarize != nil {
		customSummary, err := summarize(ctx, session, result)
		if err != nil {
			return nil, err
		}
		summary = customSummary
	}
	updated, err := app.store.UpdateSession(ctx, id, func(item *chat.Session) {
		item.ContextSummary = summary
	})
	if err != nil {
		return nil, err
	}
	return &SessionResult{Session: updated}, nil
}

func (app *Application) CreateSession(ctx context.Context, cmd CreateSessionCommand) (*CreateSessionResult, error) {
	if app == nil || app.store == nil {
		return nil, ErrStoreNotConfigured
	}
	if cmd.PrepareExternal && app.runner == nil {
		return nil, ErrRunnerNotConfigured
	}
	session, err := app.store.Create(ctx, cmd.Session)
	if err != nil {
		return nil, err
	}
	if !cmd.PrepareExternal {
		return &CreateSessionResult{Session: session}, nil
	}

	prepareCtx := ctx
	cancel := func() {}
	if app.prepareTimeout > 0 {
		prepareCtx, cancel = context.WithTimeout(ctx, app.prepareTimeout)
	}
	prepared, prepareErr := app.runner.PrepareSession(prepareCtx, agentadapters.PrepareSessionRequest{
		SessionID:               session.ID,
		AdapterID:               session.AgentID,
		Workspace:               session.Workspace,
		PreviousNativeSessionID: session.NativeSessionID,
		ConfigOptions:           session.ConfigOptions,
	})
	cancel()
	if prepareErr != nil {
		_ = app.store.Delete(context.Background(), session.ID)
		return &CreateSessionResult{Session: session}, ExternalPrepareError{Err: prepareErr}
	}

	sessionID := session.ID
	session, err = app.store.UpdateSession(ctx, session.ID, func(item *chat.Session) {
		item.DriverKind = prepared.DriverKind
		item.NativeSessionID = prepared.NativeSessionID
		item.ConfigOptions = prepared.ConfigOptions
		if prepared.AvailableCommandsKnown {
			item.AvailableCommands = prepared.AvailableCommands
		}
	})
	if err != nil {
		app.cleanupExternalSession(sessionID)
		return &CreateSessionResult{Session: session}, err
	}
	return &CreateSessionResult{Session: session}, nil
}

func (app *Application) DeleteSession(ctx context.Context, cmd DeleteSessionCommand) error {
	if app == nil || app.store == nil {
		return ErrStoreNotConfigured
	}
	if cmd.CloseNative && app.runner != nil {
		_ = app.runner.CloseSession(ctx, cmd.Session.ID)
	}
	return app.store.Delete(ctx, cmd.Session.ID)
}

func (app *Application) CloseNativeSession(ctx context.Context, cmd CloseNativeSessionCommand) (*CloseNativeSessionResult, error) {
	if app == nil || app.store == nil {
		return nil, ErrStoreNotConfigured
	}
	if app.runner != nil {
		_ = app.runner.CloseSession(ctx, cmd.Session.ID)
	}
	session, err := app.store.UpdateSession(ctx, cmd.Session.ID, func(item *chat.Session) {
		item.DriverKind = ""
		item.NativeSessionID = ""
	})
	if err != nil {
		return nil, err
	}
	return &CloseNativeSessionResult{Session: session}, nil
}

func (app *Application) SetConfigOption(ctx context.Context, cmd SetConfigOptionCommand) (*SetConfigOptionResult, error) {
	if app == nil || app.store == nil {
		return nil, ErrStoreNotConfigured
	}
	if !isExternalSession(cmd.Session) {
		return nil, ErrExternalSessionOnly
	}
	if app.runner == nil {
		return nil, ErrRunnerNotConfigured
	}
	setReq, err := configOptionSetRequest(cmd.Session.ID, cmd.ConfigID, cmd.Value)
	if err != nil {
		return nil, Validation(err)
	}

	setCtx := ctx
	cancel := func() {}
	if app.configOptionTimeout > 0 {
		setCtx, cancel = context.WithTimeout(ctx, app.configOptionTimeout)
	}
	result, err := app.runner.SetSessionConfigOption(setCtx, setReq)
	cancel()
	if err != nil {
		allowStoredOption := errors.Is(err, agentadapters.ErrSessionNotActive) ||
			agentadapters.IsLaunchConfigOption(cmd.Session.AgentID, setReq.ConfigID)
		configOptions, updateErr := updateStoredConfigOption(
			seedLaunchConfigOptionForSet(cmd.Session.ConfigOptions, cmd.Session.AgentID, setReq),
			setReq,
			allowStoredOption,
		)
		if updateErr == nil {
			session, updateErr := app.store.UpdateSession(ctx, cmd.Session.ID, func(item *chat.Session) {
				item.ConfigOptions = configOptions
			})
			if updateErr != nil {
				return nil, updateErr
			}
			return &SetConfigOptionResult{Session: session}, nil
		}
		return nil, err
	}
	session, err := app.store.UpdateSession(ctx, cmd.Session.ID, func(item *chat.Session) {
		item.ConfigOptions = result.ConfigOptions
		if result.AvailableCommandsKnown {
			item.AvailableCommands = result.AvailableCommands
		}
	})
	if err != nil {
		return nil, err
	}
	return &SetConfigOptionResult{Session: session}, nil
}

func (app *Application) SetHecateSettings(ctx context.Context, cmd SetHecateSettingsCommand) (*SetHecateSettingsResult, error) {
	if app == nil || app.store == nil {
		return nil, ErrStoreNotConfigured
	}
	if isExternalSession(cmd.Session) {
		return nil, ErrHecateSessionOnly
	}
	if cmd.RTKEnabled == nil {
		return nil, ErrNoSettingsProvided
	}

	rtkEnabled := *cmd.RTKEnabled
	// Update the task row first, then the session row. The two writes
	// are NOT atomic because chat/task stores do not share a transaction
	// boundary today. Task-first keeps existing continuations aligned
	// with the executor's sandbox-arg construction.
	if cmd.Session.TaskID != "" && app.taskStore != nil {
		task, found, err := app.taskStore.GetTask(ctx, cmd.Session.TaskID)
		if err != nil {
			return nil, err
		}
		if found {
			task.RTKEnabled = rtkEnabled
			if _, err := app.taskStore.UpdateTask(ctx, task); err != nil {
				return nil, err
			}
		}
	}

	session, err := app.store.UpdateSession(ctx, cmd.Session.ID, func(item *chat.Session) {
		item.RTKEnabled = rtkEnabled
	})
	if err != nil {
		return nil, err
	}
	return &SetHecateSettingsResult{Session: session}, nil
}

func (app *Application) AdmitMessage(cmd AdmitMessageCommand) (*AdmitMessageResult, error) {
	content := strings.TrimSpace(cmd.Content)
	if content == "" {
		return nil, Validation(ErrContentRequired)
	}
	now := cmd.Now
	if now.IsZero() {
		now = time.Now()
	}
	limits := cmd.Limits
	if limits.MaxTurnsPerSession > 0 && cmd.Session.TurnsUsed >= limits.MaxTurnsPerSession {
		return nil, MessageLimitError{
			Code:      "turns",
			Message:   fmt.Sprintf("session has reached the %d-turn limit; start a new session to continue", limits.MaxTurnsPerSession),
			Limit:     limits.MaxTurnsPerSession,
			TurnsUsed: cmd.Session.TurnsUsed,
		}
	}
	if limits.MaxSessionDuration > 0 && !cmd.Session.CreatedAt.IsZero() && now.Sub(cmd.Session.CreatedAt) >= limits.MaxSessionDuration {
		return nil, MessageLimitError{
			Code:      "duration",
			Message:   fmt.Sprintf("session has reached the %s wall-clock limit; start a new session to continue", limits.MaxSessionDuration),
			LimitMS:   limits.MaxSessionDuration.Milliseconds(),
			StartedAt: cmd.Session.CreatedAt,
			TurnsUsed: cmd.Session.TurnsUsed,
		}
	}
	if limits.IdleTimeout > 0 && !cmd.Session.UpdatedAt.IsZero() && now.Sub(cmd.Session.UpdatedAt) >= limits.IdleTimeout {
		return nil, MessageLimitError{
			Code:      "idle",
			Message:   fmt.Sprintf("session was idle for at least %s; start a new session to continue", limits.IdleTimeout),
			LimitMS:   limits.IdleTimeout.Milliseconds(),
			UpdatedAt: cmd.Session.UpdatedAt,
			TurnsUsed: cmd.Session.TurnsUsed,
		}
	}

	executionMode := normalizeExecutionMode(cmd.ExecutionMode, cmd.Session)
	switch executionMode {
	case chat.ExecutionModeHecateTask:
		if isExternalSession(cmd.Session) {
			return nil, ErrExternalCannotRunHecate
		}
	case chat.ExecutionModeExternalAgent:
		if !isExternalSession(cmd.Session) {
			return nil, ErrHecateCannotRunExternal
		}
	default:
		return nil, Validation(ErrExecutionModeInvalid)
	}

	toolsEnabled := true
	if cmd.ToolsEnabled != nil {
		toolsEnabled = *cmd.ToolsEnabled
	}
	return &AdmitMessageResult{
		Content:       content,
		ExecutionMode: executionMode,
		ToolsEnabled:  toolsEnabled,
	}, nil
}

func ResolveMessageDispatch(session chat.Session, admission AdmitMessageResult, hecateToolsUnavailable bool) MessageDispatchPlan {
	toolsEnabled := admission.ToolsEnabled
	route := MessageDispatchHecateTask
	switch admission.ExecutionMode {
	case chat.ExecutionModeExternalAgent:
		route = MessageDispatchExternalAgent
	case chat.ExecutionModeHecateTask:
		if toolsEnabled && !isExternalSession(session) && hecateToolsUnavailable {
			toolsEnabled = false
		}
		if !toolsEnabled {
			route = MessageDispatchDirectModel
		}
	}
	return MessageDispatchPlan{
		Content:       admission.Content,
		ExecutionMode: admission.ExecutionMode,
		ToolsEnabled:  toolsEnabled,
		Route:         route,
	}
}

func (app *Application) cleanupExternalSession(sessionID string) {
	cleanupCtx := context.Background()
	cancel := func() {}
	if app.prepareTimeout > 0 {
		cleanupCtx, cancel = context.WithTimeout(cleanupCtx, app.prepareTimeout)
	}
	_ = app.runner.CloseSession(cleanupCtx, sessionID)
	cancel()
	_ = app.store.Delete(context.Background(), sessionID)
}

func isExternalSession(session chat.Session) bool {
	return session.AgentID != "" && session.AgentID != chat.DefaultAgentID
}

func normalizeExecutionMode(mode string, session chat.Session) string {
	mode = strings.TrimSpace(mode)
	if mode != "" {
		return mode
	}
	if isExternalSession(session) {
		return chat.ExecutionModeExternalAgent
	}
	return chat.ExecutionModeHecateTask
}

func configOptionSetRequest(sessionID, configID string, rawValue any) (agentadapters.SetSessionConfigOptionRequest, error) {
	if strings.TrimSpace(sessionID) == "" {
		return agentadapters.SetSessionConfigOptionRequest{}, fmt.Errorf("agent chat session id is required")
	}
	configID = strings.TrimSpace(configID)
	if configID == "" {
		return agentadapters.SetSessionConfigOptionRequest{}, fmt.Errorf("config option id is required")
	}
	switch value := rawValue.(type) {
	case string:
		value = strings.TrimSpace(value)
		if value == "" {
			return agentadapters.SetSessionConfigOptionRequest{}, fmt.Errorf("value is required")
		}
		return agentadapters.SetSessionConfigOptionRequest{SessionID: sessionID, ConfigID: configID, Value: value}, nil
	case bool:
		return agentadapters.SetSessionConfigOptionRequest{SessionID: sessionID, ConfigID: configID, BoolValue: &value}, nil
	default:
		return agentadapters.SetSessionConfigOptionRequest{}, fmt.Errorf("value must be a string or boolean")
	}
}

func seedLaunchConfigOptionForSet(options []agentcontrols.ConfigOption, agentID string, req agentadapters.SetSessionConfigOptionRequest) []agentcontrols.ConfigOption {
	if req.BoolValue != nil {
		return options
	}
	seed, ok := agentadapters.LaunchConfigOptionForSet(agentID, req.ConfigID, req.Value)
	if !ok {
		return options
	}
	out := append([]agentcontrols.ConfigOption(nil), options...)
	for i := range out {
		if out[i].ID != req.ConfigID {
			continue
		}
		if out[i].Source == "" {
			out[i].Source = agentcontrols.ConfigOptionSourceLaunch
		}
		if out[i].Type == agentcontrols.ConfigOptionTypeSelect && !storedConfigOptionAllowsValue(out[i], req.Value) {
			out[i].Options = seed.Options
		}
		return out
	}
	return append(out, seed)
}

func updateStoredConfigOption(options []agentcontrols.ConfigOption, req agentadapters.SetSessionConfigOptionRequest, allowInactiveAdapterOption bool) ([]agentcontrols.ConfigOption, error) {
	out := append([]agentcontrols.ConfigOption(nil), options...)
	for i := range out {
		if out[i].ID != req.ConfigID {
			continue
		}
		if !allowInactiveAdapterOption && out[i].Source != agentcontrols.ConfigOptionSourceLaunch {
			return nil, fmt.Errorf("config option %q is not launch-managed", req.ConfigID)
		}
		switch {
		case req.BoolValue != nil:
			if out[i].Type != agentcontrols.ConfigOptionTypeBoolean {
				return nil, fmt.Errorf("config option %q is not boolean", req.ConfigID)
			}
			value := *req.BoolValue
			out[i].CurrentBool = &value
		default:
			value := strings.TrimSpace(req.Value)
			if value == "" {
				return nil, fmt.Errorf("value is required")
			}
			if out[i].Type == agentcontrols.ConfigOptionTypeSelect && !storedConfigOptionAllowsValue(out[i], value) {
				return nil, fmt.Errorf("value %q is not available for %s", value, out[i].Name)
			}
			out[i].CurrentValue = value
		}
		return out, nil
	}
	return nil, fmt.Errorf("config option %q not found", req.ConfigID)
}

func storedConfigOptionAllowsValue(option agentcontrols.ConfigOption, value string) bool {
	if len(option.Options) == 0 {
		return true
	}
	for _, candidate := range option.Options {
		if candidate.Value == value {
			return true
		}
	}
	return false
}
