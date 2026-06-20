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

func TestApplication_ListAndGetSessions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := chat.NewMemoryStore()
	if _, err := store.Create(ctx, chat.Session{ID: "chat_1", Title: "One"}); err != nil {
		t.Fatalf("Create chat_1: %v", err)
	}
	app := New(Options{Store: store})

	list, err := app.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(list.Sessions) != 1 || list.Sessions[0].ID != "chat_1" {
		t.Fatalf("sessions = %+v, want chat_1", list.Sessions)
	}
	got, err := app.GetSession(ctx, " chat_1 ")
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}
	if got.Session.Title != "One" {
		t.Fatalf("session = %+v, want title One", got.Session)
	}
}

func TestApplication_GetSessionValidationAndNotFound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	app := New(Options{Store: chat.NewMemoryStore()})
	if _, err := app.GetSession(ctx, " "); !errors.Is(err, ErrSessionIDRequired) || !IsValidationError(err) {
		t.Fatalf("GetSession(empty) error = %v, want session id validation", err)
	}
	if _, err := app.GetSession(ctx, "missing"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("GetSession(missing) error = %v, want ErrSessionNotFound", err)
	}
	if _, err := New(Options{}).GetSession(ctx, "chat_1"); !errors.Is(err, ErrStoreNotConfigured) {
		t.Fatalf("GetSession(no store) error = %v, want ErrStoreNotConfigured", err)
	}
}

func TestApplication_RenameSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := chat.NewMemoryStore()
	if _, err := store.Create(ctx, chat.Session{ID: "chat_1", Title: "Old"}); err != nil {
		t.Fatalf("Create chat_1: %v", err)
	}
	app := New(Options{Store: store})
	title := " New title "

	result, err := app.RenameSession(ctx, RenameSessionCommand{ID: " chat_1 ", Title: &title})
	if err != nil {
		t.Fatalf("RenameSession() error = %v", err)
	}
	if result.Session.Title != "New title" {
		t.Fatalf("title = %q, want trimmed New title", result.Session.Title)
	}
}

func TestApplication_RenameSessionValidation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := chat.NewMemoryStore()
	app := New(Options{Store: store})
	if _, err := app.RenameSession(ctx, RenameSessionCommand{ID: " "}); !errors.Is(err, ErrSessionIDRequired) || !IsValidationError(err) {
		t.Fatalf("RenameSession(empty id) error = %v, want id validation", err)
	}
	if _, err := app.RenameSession(ctx, RenameSessionCommand{ID: "chat_1"}); !errors.Is(err, ErrTitleRequired) || !IsValidationError(err) {
		t.Fatalf("RenameSession(no title) error = %v, want title required", err)
	}
	blank := " "
	if _, err := app.RenameSession(ctx, RenameSessionCommand{ID: "chat_1", Title: &blank}); !errors.Is(err, ErrTitleEmpty) || !IsValidationError(err) {
		t.Fatalf("RenameSession(blank title) error = %v, want title empty", err)
	}
	title := "New"
	if _, err := app.RenameSession(ctx, RenameSessionCommand{ID: "missing", Title: &title}); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("RenameSession(missing) error = %v, want ErrSessionNotFound", err)
	}
}

func TestApplication_CompactSessionWithSummaryPersistsCustomSummary(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := chat.NewMemoryStore()
	app := New(Options{Store: store})
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	if _, err := store.Create(ctx, chat.Session{
		ID:      "chat_hecate",
		AgentID: chat.DefaultAgentID,
		Messages: []chat.Message{
			{ID: "msg_1", Role: "user", Content: "first", Status: "completed"},
			{ID: "msg_2", Role: "assistant", Content: "second", Status: "completed"},
			{ID: "msg_3", Role: "user", Content: "third", Status: "completed"},
			{ID: "msg_4", Role: "assistant", Content: "fourth", Status: "completed"},
		},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	called := false
	result, err := app.CompactSessionWithSummary(ctx, CompactSessionCommand{
		ID:               " chat_hecate ",
		RetainMessages:   2,
		MinMessages:      3,
		HecateOnly:       true,
		RequireCompacted: true,
		Now:              now,
	}, func(_ context.Context, session chat.Session, selection chat.CompactTranscriptResult) (chat.ContextSummary, error) {
		called = true
		if session.ID != "chat_hecate" {
			t.Fatalf("callback session id = %q, want chat_hecate", session.ID)
		}
		if len(selection.Messages) != 2 || selection.Messages[0].ID != "msg_1" || selection.Messages[1].ID != "msg_2" {
			t.Fatalf("callback compacted messages = %+v, want msg_1/msg_2", selection.Messages)
		}
		summary := selection.Summary
		summary.Content = "semantic summary"
		summary.Strategy = chat.ContextSummaryStrategySemantic
		return summary, nil
	})
	if err != nil {
		t.Fatalf("CompactSessionWithSummary() error = %v", err)
	}
	if !called {
		t.Fatal("summary callback was not called")
	}
	if result.Session.ContextSummary.Content != "semantic summary" ||
		result.Session.ContextSummary.MessageCount != 2 ||
		result.Session.ContextSummary.ThroughMessageID != "msg_2" ||
		result.Session.ContextSummary.Strategy != chat.ContextSummaryStrategySemantic ||
		!result.Session.ContextSummary.CompactedAt.Equal(now) {
		t.Fatalf("context summary = %+v, want custom semantic summary", result.Session.ContextSummary)
	}
	stored, ok, err := store.Get(ctx, "chat_hecate")
	if err != nil || !ok {
		t.Fatalf("Get(chat_hecate) ok=%v err=%v", ok, err)
	}
	if stored.ContextSummary.Content != "semantic summary" {
		t.Fatalf("stored context summary = %+v, want custom semantic summary", stored.ContextSummary)
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
			MCPServers: []types.MCPServerConfig{{
				Name:    "weather",
				URL:     "https://example.com/mcp",
				Headers: map[string]string{"Authorization": "$MCP_TOKEN"},
			}},
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
	if got := runner.prepareReq.MCPServers; len(got) != 1 || got[0].Name != "weather" || got[0].Headers["Authorization"] != "$MCP_TOKEN" {
		t.Fatalf("prepare MCP servers = %+v, want weather server", got)
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

func TestApplication_AdmitMessageDefaultsAndTrims(t *testing.T) {
	t.Parallel()

	app := New(Options{})
	toolsEnabled := false
	result, err := app.AdmitMessage(AdmitMessageCommand{
		Session:      chat.Session{ID: "chat_hecate", AgentID: chat.DefaultAgentID},
		Content:      "  hello  ",
		ToolsEnabled: &toolsEnabled,
	})
	if err != nil {
		t.Fatalf("AdmitMessage() error = %v", err)
	}
	if result.Content != "hello" || result.ExecutionMode != chat.ExecutionModeHecateTask || result.ToolsEnabled {
		t.Fatalf("admission = %+v, want trimmed Hecate tools-off message", result)
	}
}

func TestApplication_AdmitMessageExplicitExecutionModeWins(t *testing.T) {
	t.Parallel()

	result, err := New(Options{}).AdmitMessage(AdmitMessageCommand{
		Session:       chat.Session{ID: "chat_hecate", AgentID: chat.DefaultAgentID},
		Content:       "hello",
		ExecutionMode: chat.ExecutionModeHecateTask,
	})
	if err != nil {
		t.Fatalf("AdmitMessage(hecate explicit) error = %v", err)
	}
	if result.ExecutionMode != chat.ExecutionModeHecateTask {
		t.Fatalf("execution mode = %q, want hecate_task", result.ExecutionMode)
	}

	result, err = New(Options{}).AdmitMessage(AdmitMessageCommand{
		Session:       chat.Session{ID: "chat_ext", AgentID: "codex"},
		Content:       "hello",
		ExecutionMode: chat.ExecutionModeExternalAgent,
	})
	if err != nil {
		t.Fatalf("AdmitMessage(external explicit) error = %v", err)
	}
	if result.ExecutionMode != chat.ExecutionModeExternalAgent {
		t.Fatalf("execution mode = %q, want external_agent", result.ExecutionMode)
	}
}

func TestApplication_AdmitMessageExternalDefault(t *testing.T) {
	t.Parallel()

	result, err := New(Options{}).AdmitMessage(AdmitMessageCommand{
		Session: chat.Session{ID: "chat_ext", AgentID: "codex"},
		Content: "hello",
	})
	if err != nil {
		t.Fatalf("AdmitMessage(external) error = %v", err)
	}
	if result.ExecutionMode != chat.ExecutionModeExternalAgent || !result.ToolsEnabled {
		t.Fatalf("admission = %+v, want external-agent tools-on default", result)
	}
}

func TestResolveMessageDispatchRoutesHecateToolsToTask(t *testing.T) {
	t.Parallel()

	plan := ResolveMessageDispatch(chat.Session{AgentID: chat.DefaultAgentID}, AdmitMessageResult{
		Content:       "run tests",
		ExecutionMode: chat.ExecutionModeHecateTask,
		ToolsEnabled:  true,
	}, false)

	if plan.Route != MessageDispatchHecateTask || !plan.ToolsEnabled {
		t.Fatalf("plan = %+v, want tools-on hecate task", plan)
	}
}

func TestResolveMessageDispatchDowngradesHecateWithoutTools(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name             string
		admission        AdmitMessageResult
		toolsUnavailable bool
	}{
		{
			name: "explicit tools off",
			admission: AdmitMessageResult{
				Content:       "hello",
				ExecutionMode: chat.ExecutionModeHecateTask,
				ToolsEnabled:  false,
			},
		},
		{
			name: "capability fallback",
			admission: AdmitMessageResult{
				Content:       "hello",
				ExecutionMode: chat.ExecutionModeHecateTask,
				ToolsEnabled:  true,
			},
			toolsUnavailable: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			plan := ResolveMessageDispatch(chat.Session{AgentID: chat.DefaultAgentID}, tc.admission, tc.toolsUnavailable)
			if plan.Route != MessageDispatchDirectModel || plan.ToolsEnabled {
				t.Fatalf("plan = %+v, want direct model with tools disabled", plan)
			}
		})
	}
}

func TestResolveMessageDispatchRoutesExternalAgent(t *testing.T) {
	t.Parallel()

	plan := ResolveMessageDispatch(chat.Session{AgentID: "codex"}, AdmitMessageResult{
		Content:       "work",
		ExecutionMode: chat.ExecutionModeExternalAgent,
		ToolsEnabled:  true,
	}, true)

	if plan.Route != MessageDispatchExternalAgent || !plan.ToolsEnabled {
		t.Fatalf("plan = %+v, want external-agent route preserving tools flag", plan)
	}
}

func TestApplication_AdmitMessageValidationAndRuntimeMismatch(t *testing.T) {
	t.Parallel()

	app := New(Options{})
	if _, err := app.AdmitMessage(AdmitMessageCommand{
		Session: chat.Session{ID: "chat_hecate", AgentID: chat.DefaultAgentID},
		Content: " ",
	}); !errors.Is(err, ErrContentRequired) || !IsValidationError(err) {
		t.Fatalf("AdmitMessage(blank content) error = %v, want content validation", err)
	}
	if _, err := app.AdmitMessage(AdmitMessageCommand{
		Session:       chat.Session{ID: "chat_hecate", AgentID: chat.DefaultAgentID},
		Content:       "hello",
		ExecutionMode: "unknown",
	}); !errors.Is(err, ErrExecutionModeInvalid) || !IsValidationError(err) {
		t.Fatalf("AdmitMessage(invalid mode) error = %v, want mode validation", err)
	}
	if _, err := app.AdmitMessage(AdmitMessageCommand{
		Session:       chat.Session{ID: "chat_ext", AgentID: "codex"},
		Content:       "hello",
		ExecutionMode: chat.ExecutionModeHecateTask,
	}); !errors.Is(err, ErrExternalCannotRunHecate) {
		t.Fatalf("AdmitMessage(external/hecate) error = %v, want ErrExternalCannotRunHecate", err)
	}
	if _, err := app.AdmitMessage(AdmitMessageCommand{
		Session:       chat.Session{ID: "chat_hecate", AgentID: chat.DefaultAgentID},
		Content:       "hello",
		ExecutionMode: chat.ExecutionModeExternalAgent,
	}); !errors.Is(err, ErrHecateCannotRunExternal) {
		t.Fatalf("AdmitMessage(hecate/external) error = %v, want ErrHecateCannotRunExternal", err)
	}
}

func TestApplication_AdmitMessageLimits(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	app := New(Options{})
	tests := []struct {
		name string
		cmd  AdmitMessageCommand
		code string
	}{
		{
			name: "turns",
			cmd: AdmitMessageCommand{
				Session: chat.Session{ID: "chat_hecate", AgentID: chat.DefaultAgentID, TurnsUsed: 3},
				Content: "hello",
				Limits:  MessageLimits{MaxTurnsPerSession: 3},
				Now:     now,
			},
			code: "turns",
		},
		{
			name: "duration",
			cmd: AdmitMessageCommand{
				Session: chat.Session{ID: "chat_hecate", AgentID: chat.DefaultAgentID, CreatedAt: now.Add(-2 * time.Hour), TurnsUsed: 1},
				Content: "hello",
				Limits:  MessageLimits{MaxSessionDuration: time.Hour},
				Now:     now,
			},
			code: "duration",
		},
		{
			name: "idle",
			cmd: AdmitMessageCommand{
				Session: chat.Session{ID: "chat_hecate", AgentID: chat.DefaultAgentID, UpdatedAt: now.Add(-30 * time.Minute), TurnsUsed: 1},
				Content: "hello",
				Limits:  MessageLimits{IdleTimeout: 5 * time.Minute},
				Now:     now,
			},
			code: "idle",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := app.AdmitMessage(tc.cmd)
			var limitErr MessageLimitError
			if !errors.As(err, &limitErr) || limitErr.Code != tc.code {
				t.Fatalf("AdmitMessage() error = %v (%+v), want limit code %s", err, limitErr, tc.code)
			}
		})
	}
}
