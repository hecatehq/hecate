package chatapp

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/agentcontrols"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/pkg/types"
)

type recordingAgentRunner struct {
	prepareCalls   int
	closeCalls     int
	prepareErr     error
	setCalls       int
	setErr         error
	prepareReq     agentadapters.PrepareSessionRequest
	setReq         agentadapters.SetSessionConfigOptionRequest
	closedSessions []string
	configOptions  []agentcontrols.ConfigOption
}

func (r *recordingAgentRunner) PrepareSession(_ context.Context, req agentadapters.PrepareSessionRequest) (agentadapters.PrepareSessionResult, error) {
	r.prepareCalls++
	r.prepareReq = req
	if r.prepareErr != nil {
		return agentadapters.PrepareSessionResult{}, r.prepareErr
	}
	return agentadapters.PrepareSessionResult{
		DriverKind:      agentadapters.DriverKindACP,
		NativeSessionID: "native_" + req.SessionID,
		ConfigOptions:   r.configOptions,
	}, nil
}

func (r *recordingAgentRunner) SetSessionConfigOption(_ context.Context, req agentadapters.SetSessionConfigOptionRequest) (agentadapters.SetSessionConfigOptionResult, error) {
	r.setCalls++
	r.setReq = req
	if r.setErr != nil {
		return agentadapters.SetSessionConfigOptionResult{}, r.setErr
	}
	return agentadapters.SetSessionConfigOptionResult{ConfigOptions: r.configOptions}, nil
}

func (r *recordingAgentRunner) CloseSession(_ context.Context, sessionID string) error {
	r.closeCalls++
	r.closedSessions = append(r.closedSessions, sessionID)
	return nil
}

func TestApplication_CreateSessionPersistsBuiltInSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := chat.NewMemoryStore()
	runner := &recordingAgentRunner{}
	app := New(Options{Store: store, Runner: runner})

	result, err := app.CreateSession(ctx, CreateSessionCommand{
		Session: chat.Session{
			ID:      "chat_hecate",
			Title:   "Hecate Chat",
			AgentID: chat.DefaultAgentID,
			Model:   "qwen2.5-coder",
		},
	})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if result.Session.ID != "chat_hecate" || result.Session.AgentID != chat.DefaultAgentID {
		t.Fatalf("session = %+v, want persisted built-in session", result.Session)
	}
	if runner.prepareCalls != 0 || runner.closeCalls != 0 {
		t.Fatalf("runner prepare/close = %d/%d, want unused", runner.prepareCalls, runner.closeCalls)
	}
	if _, ok, err := store.Get(ctx, "chat_hecate"); err != nil || !ok {
		t.Fatalf("Get(chat_hecate) ok=%v err=%v, want persisted session", ok, err)
	}
}

func TestApplication_CreateSessionPreparesExternalSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := chat.NewMemoryStore()
	runner := &recordingAgentRunner{
		configOptions: []agentcontrols.ConfigOption{{
			ID:           "model",
			Name:         "Model",
			CurrentValue: "sonnet",
		}},
	}
	app := New(Options{Store: store, Runner: runner, PrepareTimeout: time.Second})

	result, err := app.CreateSession(ctx, CreateSessionCommand{
		PrepareExternal: true,
		Session: chat.Session{
			ID:              "chat_ext",
			AgentID:         "codex",
			Workspace:       "/tmp/hecate",
			NativeSessionID: "native_previous",
			ConfigOptions: []agentcontrols.ConfigOption{{
				ID:           "model",
				Name:         "Model",
				CurrentValue: "opus",
			}},
		},
	})
	if err != nil {
		t.Fatalf("CreateSession(external) error = %v", err)
	}
	if runner.prepareCalls != 1 {
		t.Fatalf("prepareCalls = %d, want 1", runner.prepareCalls)
	}
	if runner.prepareReq.SessionID != "chat_ext" || runner.prepareReq.AdapterID != "codex" || runner.prepareReq.PreviousNativeSessionID != "native_previous" {
		t.Fatalf("prepare request = %+v, want session/adapter/previous native id", runner.prepareReq)
	}
	if result.Session.DriverKind != agentadapters.DriverKindACP || result.Session.NativeSessionID != "native_chat_ext" {
		t.Fatalf("prepared session = %+v, want native metadata", result.Session)
	}
	if len(result.Session.ConfigOptions) != 1 || result.Session.ConfigOptions[0].CurrentValue != "sonnet" {
		t.Fatalf("config options = %+v, want prepared options", result.Session.ConfigOptions)
	}
}

func TestApplication_CreateSessionPrepareFailureDeletesSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := chat.NewMemoryStore()
	runner := &recordingAgentRunner{prepareErr: fmt.Errorf("prepare boom")}
	app := New(Options{Store: store, Runner: runner})

	_, err := app.CreateSession(ctx, CreateSessionCommand{
		PrepareExternal: true,
		Session:         chat.Session{ID: "chat_ext", AgentID: "codex", Workspace: "/tmp/hecate"},
	})
	var prepareErr ExternalPrepareError
	if !errors.As(err, &prepareErr) || !errors.Is(prepareErr.Unwrap(), runner.prepareErr) {
		t.Fatalf("CreateSession(prepare failure) error = %v, want ExternalPrepareError", err)
	}
	if runner.closeCalls != 0 {
		t.Fatalf("closeCalls = %d, want no close after failed prepare", runner.closeCalls)
	}
	if _, ok, err := store.Get(ctx, "chat_ext"); err != nil || ok {
		t.Fatalf("Get(chat_ext) ok=%v err=%v, want deleted session", ok, err)
	}
}

type failingUpdateStore struct {
	SessionStore
	err        error
	deletedIDs []string
}

func (s *failingUpdateStore) UpdateSession(context.Context, string, func(*chat.Session)) (chat.Session, error) {
	return chat.Session{}, s.err
}

func (s *failingUpdateStore) Delete(ctx context.Context, id string) error {
	s.deletedIDs = append(s.deletedIDs, id)
	return s.SessionStore.Delete(ctx, id)
}

func TestApplication_CreateSessionUpdateFailureCleansPreparedSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseStore := chat.NewMemoryStore()
	store := &failingUpdateStore{SessionStore: baseStore, err: fmt.Errorf("update boom")}
	runner := &recordingAgentRunner{}
	app := New(Options{Store: store, Runner: runner, PrepareTimeout: time.Second})

	_, err := app.CreateSession(ctx, CreateSessionCommand{
		PrepareExternal: true,
		Session:         chat.Session{ID: "chat_ext", AgentID: "codex", Workspace: "/tmp/hecate"},
	})
	if err == nil || !errors.Is(err, store.err) {
		t.Fatalf("CreateSession(update failure) error = %v, want update error", err)
	}
	if runner.closeCalls != 1 || len(runner.closedSessions) != 1 || runner.closedSessions[0] != "chat_ext" {
		t.Fatalf("closed sessions = %+v closeCalls=%d, want chat_ext closed once", runner.closedSessions, runner.closeCalls)
	}
	if len(store.deletedIDs) != 1 || store.deletedIDs[0] != "chat_ext" {
		t.Fatalf("deleted ids = %+v, want chat_ext", store.deletedIDs)
	}
	if _, ok, err := baseStore.Get(ctx, "chat_ext"); err != nil || ok {
		t.Fatalf("Get(chat_ext) ok=%v err=%v, want cleaned session", ok, err)
	}
}

func TestApplication_CreateSessionNilDependencies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	if _, err := New(Options{}).CreateSession(ctx, CreateSessionCommand{}); !errors.Is(err, ErrStoreNotConfigured) {
		t.Fatalf("CreateSession(nil store) error = %v, want ErrStoreNotConfigured", err)
	}

	store := chat.NewMemoryStore()
	_, err := New(Options{Store: store}).CreateSession(ctx, CreateSessionCommand{
		PrepareExternal: true,
		Session:         chat.Session{ID: "chat_ext", AgentID: "codex", Workspace: "/tmp/hecate"},
	})
	if !errors.Is(err, ErrRunnerNotConfigured) {
		t.Fatalf("CreateSession(nil runner) error = %v, want ErrRunnerNotConfigured", err)
	}
}

func TestApplication_DeleteSessionClosesNativeSessionWhenRequested(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := chat.NewMemoryStore()
	runner := &recordingAgentRunner{}
	app := New(Options{Store: store, Runner: runner})
	session, err := store.Create(ctx, chat.Session{
		ID:              "chat_ext",
		AgentID:         "codex",
		DriverKind:      agentadapters.DriverKindACP,
		NativeSessionID: "native_chat_ext",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := app.DeleteSession(ctx, DeleteSessionCommand{Session: session, CloseNative: true}); err != nil {
		t.Fatalf("DeleteSession() error = %v", err)
	}
	if runner.closeCalls != 1 || runner.closedSessions[0] != "chat_ext" {
		t.Fatalf("closed sessions = %+v closeCalls=%d, want chat_ext closed once", runner.closedSessions, runner.closeCalls)
	}
	if _, ok, err := store.Get(ctx, "chat_ext"); err != nil || ok {
		t.Fatalf("Get(chat_ext) ok=%v err=%v, want deleted session", ok, err)
	}
}

func TestApplication_DeleteSessionWithoutRunnerStillDeletes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := chat.NewMemoryStore()
	app := New(Options{Store: store})
	session, err := store.Create(ctx, chat.Session{ID: "chat_ext", AgentID: "codex"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := app.DeleteSession(ctx, DeleteSessionCommand{Session: session, CloseNative: true}); err != nil {
		t.Fatalf("DeleteSession(no runner) error = %v", err)
	}
	if _, ok, err := store.Get(ctx, "chat_ext"); err != nil || ok {
		t.Fatalf("Get(chat_ext) ok=%v err=%v, want deleted session", ok, err)
	}
}

func TestApplication_CloseNativeSessionClearsRuntimeHandles(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := chat.NewMemoryStore()
	runner := &recordingAgentRunner{}
	app := New(Options{Store: store, Runner: runner})
	session, err := store.Create(ctx, chat.Session{
		ID:              "chat_ext",
		AgentID:         "codex",
		DriverKind:      agentadapters.DriverKindACP,
		NativeSessionID: "native_chat_ext",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	result, err := app.CloseNativeSession(ctx, CloseNativeSessionCommand{Session: session})
	if err != nil {
		t.Fatalf("CloseNativeSession() error = %v", err)
	}
	if runner.closeCalls != 1 || runner.closedSessions[0] != "chat_ext" {
		t.Fatalf("closed sessions = %+v closeCalls=%d, want chat_ext closed once", runner.closedSessions, runner.closeCalls)
	}
	if result.Session.DriverKind != "" || result.Session.NativeSessionID != "" {
		t.Fatalf("session runtime handles = %q/%q, want cleared", result.Session.DriverKind, result.Session.NativeSessionID)
	}
}

func TestApplication_DeleteAndCloseNativeSessionNilStore(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	app := New(Options{})
	if err := app.DeleteSession(ctx, DeleteSessionCommand{Session: chat.Session{ID: "chat"}}); !errors.Is(err, ErrStoreNotConfigured) {
		t.Fatalf("DeleteSession(nil store) error = %v, want ErrStoreNotConfigured", err)
	}
	if _, err := app.CloseNativeSession(ctx, CloseNativeSessionCommand{Session: chat.Session{ID: "chat"}}); !errors.Is(err, ErrStoreNotConfigured) {
		t.Fatalf("CloseNativeSession(nil store) error = %v, want ErrStoreNotConfigured", err)
	}
}

func TestApplication_SetConfigOptionPersistsRunnerOptions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := chat.NewMemoryStore()
	runner := &recordingAgentRunner{
		configOptions: []agentcontrols.ConfigOption{{
			ID:           "model",
			Type:         agentcontrols.ConfigOptionTypeSelect,
			CurrentValue: "sonnet",
		}},
	}
	app := New(Options{Store: store, Runner: runner, ConfigOptionTimeout: time.Second})
	session, err := store.Create(ctx, chat.Session{ID: "chat_ext", AgentID: "codex"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	result, err := app.SetConfigOption(ctx, SetConfigOptionCommand{
		Session:  session,
		ConfigID: "model",
		Value:    "sonnet",
	})
	if err != nil {
		t.Fatalf("SetConfigOption() error = %v", err)
	}
	if runner.setCalls != 1 || runner.setReq.SessionID != "chat_ext" || runner.setReq.ConfigID != "model" || runner.setReq.Value != "sonnet" {
		t.Fatalf("set request = %+v calls=%d, want model update", runner.setReq, runner.setCalls)
	}
	if len(result.Session.ConfigOptions) != 1 || result.Session.ConfigOptions[0].CurrentValue != "sonnet" {
		t.Fatalf("config options = %+v, want runner options persisted", result.Session.ConfigOptions)
	}
}

func TestApplication_SetConfigOptionInactiveSessionUpdatesStoredLaunchOption(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := chat.NewMemoryStore()
	runner := &recordingAgentRunner{setErr: agentadapters.ErrSessionNotActive}
	app := New(Options{Store: store, Runner: runner})
	session, err := store.Create(ctx, chat.Session{
		ID:      "chat_ext",
		AgentID: "codex",
		ConfigOptions: []agentcontrols.ConfigOption{{
			ID:           "model",
			Name:         "Model",
			Source:       agentcontrols.ConfigOptionSourceLaunch,
			Type:         agentcontrols.ConfigOptionTypeSelect,
			CurrentValue: "old",
		}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	result, err := app.SetConfigOption(ctx, SetConfigOptionCommand{
		Session:  session,
		ConfigID: "model",
		Value:    "new",
	})
	if err != nil {
		t.Fatalf("SetConfigOption(inactive launch option) error = %v", err)
	}
	if len(result.Session.ConfigOptions) != 1 || result.Session.ConfigOptions[0].CurrentValue != "new" {
		t.Fatalf("config options = %+v, want stored launch option updated", result.Session.ConfigOptions)
	}
}

func TestApplication_SetConfigOptionValidationAndRuntime(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := chat.NewMemoryStore()
	runner := &recordingAgentRunner{}
	app := New(Options{Store: store, Runner: runner})

	if _, err := app.SetConfigOption(ctx, SetConfigOptionCommand{
		Session:  chat.Session{ID: "chat", AgentID: chat.DefaultAgentID},
		ConfigID: "model",
		Value:    "sonnet",
	}); !errors.Is(err, ErrExternalSessionOnly) {
		t.Fatalf("SetConfigOption(hecate) error = %v, want ErrExternalSessionOnly", err)
	}
	if _, err := app.SetConfigOption(ctx, SetConfigOptionCommand{
		Session:  chat.Session{ID: "chat_ext", AgentID: "codex"},
		ConfigID: " ",
		Value:    "sonnet",
	}); !IsValidationError(err) {
		t.Fatalf("SetConfigOption(blank config id) error = %v, want validation", err)
	}
	if _, err := New(Options{Store: store}).SetConfigOption(ctx, SetConfigOptionCommand{
		Session:  chat.Session{ID: "chat_ext", AgentID: "codex"},
		ConfigID: "model",
		Value:    "sonnet",
	}); !errors.Is(err, ErrRunnerNotConfigured) {
		t.Fatalf("SetConfigOption(no runner) error = %v, want ErrRunnerNotConfigured", err)
	}
}

type recordingTaskStore struct {
	tasks   map[string]types.Task
	updates []types.Task
}

func (s *recordingTaskStore) GetTask(_ context.Context, id string) (types.Task, bool, error) {
	if s == nil {
		return types.Task{}, false, nil
	}
	task, ok := s.tasks[id]
	return task, ok, nil
}

func (s *recordingTaskStore) UpdateTask(_ context.Context, task types.Task) (types.Task, error) {
	s.updates = append(s.updates, task)
	s.tasks[task.ID] = task
	return task, nil
}

func TestApplication_SetHecateSettingsUpdatesTaskBeforeSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := chat.NewMemoryStore()
	taskStore := &recordingTaskStore{tasks: map[string]types.Task{
		"task_chat": {ID: "task_chat", RTKEnabled: false},
	}}
	app := New(Options{Store: store, TaskStore: taskStore})
	session, err := store.Create(ctx, chat.Session{ID: "chat_hecate", AgentID: chat.DefaultAgentID, TaskID: "task_chat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	rtkEnabled := true

	result, err := app.SetHecateSettings(ctx, SetHecateSettingsCommand{Session: session, RTKEnabled: &rtkEnabled})
	if err != nil {
		t.Fatalf("SetHecateSettings() error = %v", err)
	}
	if len(taskStore.updates) != 1 || !taskStore.updates[0].RTKEnabled {
		t.Fatalf("task updates = %+v, want RTK enabled task update", taskStore.updates)
	}
	if !result.Session.RTKEnabled {
		t.Fatalf("session RTKEnabled = false, want true")
	}
}

func TestApplication_SetHecateSettingsValidation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := chat.NewMemoryStore()
	app := New(Options{Store: store})
	if _, err := app.SetHecateSettings(ctx, SetHecateSettingsCommand{
		Session: chat.Session{ID: "chat_ext", AgentID: "codex"},
	}); !errors.Is(err, ErrHecateSessionOnly) {
		t.Fatalf("SetHecateSettings(external) error = %v, want ErrHecateSessionOnly", err)
	}
	if _, err := app.SetHecateSettings(ctx, SetHecateSettingsCommand{
		Session: chat.Session{ID: "chat_hecate", AgentID: chat.DefaultAgentID},
	}); !errors.Is(err, ErrNoSettingsProvided) {
		t.Fatalf("SetHecateSettings(no settings) error = %v, want ErrNoSettingsProvided", err)
	}
}
