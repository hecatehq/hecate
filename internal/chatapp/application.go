package chatapp

import (
	"context"
	"errors"
	"time"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/chat"
)

var (
	ErrStoreNotConfigured  = errors.New("agent chat store is not configured")
	ErrRunnerNotConfigured = errors.New("agent chat runner is not configured")
)

type SessionStore interface {
	Create(ctx context.Context, session chat.Session) (chat.Session, error)
	UpdateSession(ctx context.Context, id string, update func(*chat.Session)) (chat.Session, error)
	Delete(ctx context.Context, id string) error
}

type AgentRunner interface {
	PrepareSession(context.Context, agentadapters.PrepareSessionRequest) (agentadapters.PrepareSessionResult, error)
	CloseSession(context.Context, string) error
}

type Application struct {
	store          SessionStore
	runner         AgentRunner
	prepareTimeout time.Duration
}

type Options struct {
	Store          SessionStore
	Runner         AgentRunner
	PrepareTimeout time.Duration
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
		store:          opts.Store,
		runner:         opts.Runner,
		prepareTimeout: opts.PrepareTimeout,
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
