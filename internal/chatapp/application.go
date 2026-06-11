package chatapp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/agentcontrols"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/pkg/types"
)

var (
	ErrStoreNotConfigured  = errors.New("agent chat store is not configured")
	ErrRunnerNotConfigured = errors.New("agent chat runner is not configured")
	ErrExternalSessionOnly = errors.New("agent chat config options are only available for external-agent sessions")
	ErrHecateSessionOnly   = errors.New("Hecate Chat settings are not available for external-agent sessions")
	ErrNoSettingsProvided  = errors.New("no settings provided")
)

type ValidationError struct {
	err error
}

func (e ValidationError) Error() string {
	return e.err.Error()
}

func (e ValidationError) Unwrap() error {
	return e.err
}

func Validation(err error) error {
	if err == nil {
		return nil
	}
	return ValidationError{err: err}
}

func IsValidationError(err error) bool {
	var validation ValidationError
	return errors.As(err, &validation)
}

type SessionStore interface {
	Create(ctx context.Context, session chat.Session) (chat.Session, error)
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
