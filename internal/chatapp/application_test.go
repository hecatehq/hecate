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
)

type recordingAgentRunner struct {
	prepareCalls   int
	closeCalls     int
	prepareErr     error
	prepareReq     agentadapters.PrepareSessionRequest
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
