package chatapp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/agentcontrols"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/chatattachments"
	"github.com/hecatehq/hecate/pkg/types"
)

type recordingAgentRunner struct {
	prepareCalls    int
	closeCalls      int
	deleteCalls     int
	prepareErr      error
	setCalls        int
	setErr          error
	prepareReq      agentadapters.PrepareSessionRequest
	setReq          agentadapters.SetSessionConfigOptionRequest
	closedSessions  []string
	deletedSessions []string
	configOptions   []agentcontrols.ConfigOption
}

type blockingAttachmentCreateStore struct {
	chatattachments.Store
	created chan struct{}
	release chan struct{}
}

type deadlineAttachmentResolveStore struct {
	chatattachments.Store
	cancelRequest context.CancelFunc
	deadlineSeen  chan bool
	resolveErr    chan error
}

type cancelAfterAttachmentCreateStore struct {
	chatattachments.Store
	cancelRequest context.CancelFunc
	cleanupLive   chan bool
}

type cancelAwareSessionGetStore struct {
	SessionStore
}

type attachmentCreateErrorStore struct {
	chatattachments.Store
	err error
}

type ownerDeletingAttachmentStore struct {
	chatattachments.Store
	sessions   SessionStore
	cleanupErr error
}

type failFirstAttachmentSessionDeleteStore struct {
	chatattachments.Store
	deleteCalls int
	deleteErr   error
}

func (s *blockingAttachmentCreateStore) Create(ctx context.Context, attachment chatattachments.StoredAttachment) (chatattachments.StoredAttachment, error) {
	created, err := s.Store.Create(ctx, attachment)
	close(s.created)
	<-s.release
	return created, err
}

func (s *deadlineAttachmentResolveStore) Claim(ctx context.Context, ref chatattachments.ClaimRef) ([]chatattachments.StoredAttachment, error) {
	attachments, err := s.Store.Claim(ctx, ref)
	s.cancelRequest()
	return attachments, err
}

func (s *deadlineAttachmentResolveStore) ResolveClaim(ctx context.Context, _ chatattachments.ClaimRef, _ chatattachments.ClaimResolution) error {
	_, hasDeadline := ctx.Deadline()
	s.deadlineSeen <- hasDeadline
	<-ctx.Done()
	err := ctx.Err()
	s.resolveErr <- err
	return err
}

func (s *cancelAfterAttachmentCreateStore) Create(ctx context.Context, attachment chatattachments.StoredAttachment) (chatattachments.StoredAttachment, error) {
	created, err := s.Store.Create(ctx, attachment)
	s.cancelRequest()
	return created, err
}

func (s *cancelAfterAttachmentCreateStore) DeleteDraft(ctx context.Context, sessionID, id string) error {
	_, hasDeadline := ctx.Deadline()
	s.cleanupLive <- ctx.Err() == nil && hasDeadline
	return s.Store.DeleteDraft(ctx, sessionID, id)
}

func (s cancelAwareSessionGetStore) Get(ctx context.Context, id string) (chat.Session, bool, error) {
	if err := ctx.Err(); err != nil {
		return chat.Session{}, false, err
	}
	return s.SessionStore.Get(ctx, id)
}

func (s attachmentCreateErrorStore) Create(context.Context, chatattachments.StoredAttachment) (chatattachments.StoredAttachment, error) {
	return chatattachments.StoredAttachment{}, s.err
}

func (s *ownerDeletingAttachmentStore) Create(ctx context.Context, attachment chatattachments.StoredAttachment) (chatattachments.StoredAttachment, error) {
	created, err := s.Store.Create(ctx, attachment)
	if err != nil {
		return chatattachments.StoredAttachment{}, err
	}
	if err := s.sessions.Delete(ctx, attachment.SessionID); err != nil {
		return chatattachments.StoredAttachment{}, err
	}
	return created, nil
}

func (s *ownerDeletingAttachmentStore) DeleteDraft(context.Context, string, string) error {
	return s.cleanupErr
}

func (s *failFirstAttachmentSessionDeleteStore) DeleteBySessionID(ctx context.Context, sessionID string) error {
	s.deleteCalls++
	if s.deleteCalls == 1 {
		return s.deleteErr
	}
	return s.Store.DeleteBySessionID(ctx, sessionID)
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
		AgentInfo: &agentcontrols.ImplementationInfo{
			Name:    "codex-acp-adapter",
			Title:   "Codex ACP Adapter",
			Version: "0.1.0-alpha.28",
		},
		ConfigOptions: r.configOptions,
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

func (r *recordingAgentRunner) DeleteSession(_ context.Context, sessionID string) error {
	r.deleteCalls++
	r.deletedSessions = append(r.deletedSessions, sessionID)
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
	if runner.prepareCalls != 0 || runner.closeCalls != 0 || runner.deleteCalls != 0 {
		t.Fatalf("runner prepare/close/delete = %d/%d/%d, want unused", runner.prepareCalls, runner.closeCalls, runner.deleteCalls)
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
	if result.Session.AgentInfo == nil || result.Session.AgentInfo.Title != "Codex ACP Adapter" || result.Session.AgentInfo.Version != "0.1.0-alpha.28" {
		t.Fatalf("agent info = %#v, want prepared adapter metadata", result.Session.AgentInfo)
	}
	if len(result.Session.ConfigOptions) != 1 || result.Session.ConfigOptions[0].CurrentValue != "sonnet" {
		t.Fatalf("config options = %+v, want prepared options", result.Session.ConfigOptions)
	}
}

func TestApplication_ReplaceNativeSessionPersistsWithCompareAndSwap(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := chat.NewMemoryStore()
	if _, err := store.Create(ctx, chat.Session{ID: "chat_ext", NativeSessionID: "native_stale"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	app := New(Options{Store: store})
	result, err := app.ReplaceNativeSession(ctx, ReplaceNativeSessionCommand{
		SessionID:               "chat_ext",
		PreviousNativeSessionID: "native_stale",
		NativeSessionID:         "native_fresh",
	})
	if err != nil {
		t.Fatalf("ReplaceNativeSession: %v", err)
	}
	if result.Session.NativeSessionID != "native_fresh" {
		t.Fatalf("native session = %q, want native_fresh", result.Session.NativeSessionID)
	}
	_, err = app.ReplaceNativeSession(ctx, ReplaceNativeSessionCommand{
		SessionID:               "chat_ext",
		PreviousNativeSessionID: "native_stale",
		NativeSessionID:         "native_other",
	})
	if !errors.Is(err, ErrNativeSessionChanged) {
		t.Fatalf("stale ReplaceNativeSession error = %v, want ErrNativeSessionChanged", err)
	}
	stored, ok, err := store.Get(ctx, "chat_ext")
	if err != nil || !ok {
		t.Fatalf("Get(chat_ext) ok=%v err=%v", ok, err)
	}
	if stored.NativeSessionID != "native_fresh" {
		t.Fatalf("stored native session = %q, want native_fresh", stored.NativeSessionID)
	}
}

func TestApplication_ReplaceNativeSessionValidatesIDs(t *testing.T) {
	t.Parallel()
	app := New(Options{Store: chat.NewMemoryStore()})
	for name, command := range map[string]ReplaceNativeSessionCommand{
		"session":  {PreviousNativeSessionID: "old", NativeSessionID: "new"},
		"previous": {SessionID: "chat", NativeSessionID: "new"},
		"new":      {SessionID: "chat", PreviousNativeSessionID: "old"},
	} {
		command := command
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := app.ReplaceNativeSession(context.Background(), command); !IsValidationError(err) {
				t.Fatalf("ReplaceNativeSession error = %v, want validation error", err)
			}
		})
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
	if runner.closeCalls != 0 || runner.deleteCalls != 0 {
		t.Fatalf("close/delete calls = %d/%d, want no cleanup after failed prepare", runner.closeCalls, runner.deleteCalls)
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
	if runner.deleteCalls != 1 || len(runner.deletedSessions) != 1 || runner.deletedSessions[0] != "chat_ext" {
		t.Fatalf("deleted sessions = %+v deleteCalls=%d, want chat_ext deleted once", runner.deletedSessions, runner.deleteCalls)
	}
	if runner.closeCalls != 0 {
		t.Fatalf("closeCalls = %d, want no non-destructive close during failed create cleanup", runner.closeCalls)
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

func TestApplication_DeleteSessionDeletesNativeSessionWhenRequested(t *testing.T) {
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
		AgentInfo:       &agentcontrols.ImplementationInfo{Name: "codex-acp-adapter"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := app.DeleteSession(ctx, DeleteSessionCommand{SessionID: session.ID, DeleteNative: true}); err != nil {
		t.Fatalf("DeleteSession() error = %v", err)
	}
	if runner.deleteCalls != 1 || runner.deletedSessions[0] != "chat_ext" {
		t.Fatalf("deleted sessions = %+v deleteCalls=%d, want chat_ext deleted once", runner.deletedSessions, runner.deleteCalls)
	}
	if runner.closeCalls != 0 {
		t.Fatalf("closeCalls = %d, want delete path not close", runner.closeCalls)
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

	if err := app.DeleteSession(ctx, DeleteSessionCommand{SessionID: session.ID, DeleteNative: true}); err != nil {
		t.Fatalf("DeleteSession(no runner) error = %v", err)
	}
	if _, ok, err := store.Get(ctx, "chat_ext"); err != nil || ok {
		t.Fatalf("Get(chat_ext) ok=%v err=%v, want deleted session", ok, err)
	}
}

func TestApplication_DeleteSessionRetryFinishesAttachmentCleanupAfterTranscriptDelete(t *testing.T) {
	ctx := context.Background()
	sessions := chat.NewMemoryStore()
	baseAttachments := chatattachments.NewMemoryStore()
	attachments := &failFirstAttachmentSessionDeleteStore{
		Store:     baseAttachments,
		deleteErr: errors.New("injected attachment delete failure"),
	}
	app := New(Options{Store: sessions, Attachments: attachments})
	session, err := sessions.Create(ctx, chat.Session{ID: "chat_delete_retry", AgentID: chat.DefaultAgentID})
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}
	created, err := baseAttachments.Create(ctx, chatattachments.StoredAttachment{
		Attachment: chatattachments.Attachment{ID: "att_delete_retry", SessionID: session.ID, SizeBytes: 3},
		Data:       []byte("png"),
	})
	if err != nil {
		t.Fatalf("Create attachment: %v", err)
	}

	cmd := DeleteSessionCommand{SessionID: session.ID}
	if err := app.DeleteSession(ctx, cmd); !errors.Is(err, attachments.deleteErr) {
		t.Fatalf("first DeleteSession error = %v, want injected attachment failure", err)
	}
	if _, ok, err := sessions.Get(ctx, session.ID); err != nil || ok {
		t.Fatalf("Get session after first delete = ok %v, err %v", ok, err)
	}
	if _, ok, err := baseAttachments.Get(ctx, session.ID, created.ID); err != nil || !ok {
		t.Fatalf("Get attachment after failed cleanup = ok %v, err %v", ok, err)
	}

	if err := app.DeleteSession(ctx, cmd); err != nil {
		t.Fatalf("retry DeleteSession: %v", err)
	}
	if _, ok, err := baseAttachments.Get(ctx, session.ID, created.ID); err != nil || ok {
		t.Fatalf("Get attachment after retry = ok %v, err %v", ok, err)
	}
	if err := app.DeleteSession(ctx, cmd); err != nil {
		t.Fatalf("idempotent DeleteSession: %v", err)
	}
	if attachments.deleteCalls != 3 {
		t.Fatalf("attachment delete calls = %d, want 3", attachments.deleteCalls)
	}
}

func TestApplication_AttachmentLifecycleIsSessionScoped(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := chat.NewMemoryStore()
	attachmentStore := chatattachments.NewMemoryStore()
	app := New(Options{Store: store, Attachments: attachmentStore})
	if _, err := store.Create(ctx, chat.Session{ID: "chat_images", AgentID: chat.DefaultAgentID}); err != nil {
		t.Fatalf("Create session: %v", err)
	}
	created, err := app.CreateAttachment(ctx, CreateAttachmentCommand{Attachment: chatattachments.StoredAttachment{
		Attachment: chatattachments.Attachment{
			ID:        "att_1",
			SessionID: "chat_images",
			Filename:  "diagram.png",
			MediaType: "image/png",
			SizeBytes: 3,
			SHA256:    "digest",
		},
		Data: []byte("png"),
	}})
	if err != nil {
		t.Fatalf("CreateAttachment: %v", err)
	}
	claim := chatattachments.ClaimRef{SessionID: "chat_images", MessageID: "msg_1", AttachmentIDs: []string{"att_1"}}
	resolved, err := app.ClaimAttachments(ctx, claim)
	if err != nil {
		t.Fatalf("ClaimAttachments: %v", err)
	}
	if len(resolved) != 1 || !bytes.Equal(resolved[0].Data, created.Data) {
		t.Fatalf("resolved = %#v, want created attachment", resolved)
	}
	if err := app.ResolveAttachmentClaim(ctx, claim, chatattachments.ClaimLinked); err != nil {
		t.Fatalf("LinkAttachments: %v", err)
	}
	if _, err := app.ClaimAttachments(ctx, chatattachments.ClaimRef{SessionID: "chat_images", MessageID: "msg_2", AttachmentIDs: []string{"att_1", "att_1"}}); !errors.Is(err, ErrDuplicateAttachmentID) {
		t.Fatalf("ClaimAttachments duplicate error = %v, want ErrDuplicateAttachmentID", err)
	}
	if _, err := app.ClaimAttachments(ctx, chatattachments.ClaimRef{SessionID: "other_chat", MessageID: "msg_3", AttachmentIDs: []string{"att_1"}}); !errors.Is(err, ErrAttachmentNotFound) {
		t.Fatalf("ClaimAttachments other session error = %v, want ErrAttachmentNotFound", err)
	}

	if _, err := store.AppendMessage(ctx, "chat_images", chat.Message{
		ID:      "msg_1",
		Role:    "user",
		Content: "inspect",
		Attachments: []chat.MessageAttachment{{
			ID:        created.ID,
			Filename:  created.Filename,
			MediaType: created.MediaType,
			SizeBytes: created.SizeBytes,
			SHA256:    created.SHA256,
			CreatedAt: created.CreatedAt,
		}},
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := app.DeleteAttachment(ctx, AttachmentCommand{SessionID: "chat_images", AttachmentID: "att_1"}); !errors.Is(err, ErrAttachmentInUse) {
		t.Fatalf("DeleteAttachment linked error = %v, want ErrAttachmentInUse", err)
	}
	session, ok, err := store.Get(ctx, "chat_images")
	if err != nil || !ok {
		t.Fatalf("Get session: ok=%v err=%v", ok, err)
	}
	if err := app.DeleteSession(ctx, DeleteSessionCommand{SessionID: session.ID}); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, ok, err := attachmentStore.Get(ctx, "chat_images", "att_1"); err != nil || ok {
		t.Fatalf("attachment after session delete: ok=%v err=%v", ok, err)
	}
}

func TestApplication_ExternalAttachmentLifecycleUsesSamePrivateStoreBoundary(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	sessions := chat.NewMemoryStore()
	attachments := chatattachments.NewMemoryStore()
	app := New(Options{Store: sessions, Attachments: attachments})
	if _, err := sessions.Create(ctx, chat.Session{ID: "chat_external_files", AgentID: "codex"}); err != nil {
		t.Fatalf("Create session: %v", err)
	}
	created, err := app.CreateAttachment(ctx, CreateAttachmentCommand{Attachment: chatattachments.StoredAttachment{
		Attachment: chatattachments.Attachment{
			ID:        "att_external",
			SessionID: "chat_external_files",
			Filename:  "notes.txt",
			MediaType: "text/plain",
			SizeBytes: 5,
			SHA256:    "digest",
		},
		Data: []byte("notes"),
	}})
	if err != nil {
		t.Fatalf("CreateAttachment: %v", err)
	}
	if got, err := app.GetAttachment(ctx, AttachmentCommand{SessionID: created.SessionID, AttachmentID: created.ID}); err != nil || !bytes.Equal(got.Data, created.Data) {
		t.Fatalf("GetAttachment = %#v, %v", got, err)
	}
	ref := chatattachments.ClaimRef{SessionID: created.SessionID, MessageID: "msg_external", AttachmentIDs: []string{created.ID}}
	claimed, err := app.ClaimAttachments(ctx, ref)
	if err != nil || len(claimed) != 1 || !bytes.Equal(claimed[0].Data, created.Data) {
		t.Fatalf("ClaimAttachments = %#v, %v", claimed, err)
	}
	if err := app.ResolveAttachmentClaim(ctx, ref, chatattachments.ClaimLinked); err != nil {
		t.Fatalf("ResolveAttachmentClaim: %v", err)
	}
	if err := app.DeleteAttachment(ctx, AttachmentCommand{SessionID: created.SessionID, AttachmentID: created.ID}); !errors.Is(err, ErrAttachmentInUse) {
		t.Fatalf("DeleteAttachment error = %v, want ErrAttachmentInUse", err)
	}
}

func TestApplication_CreateAttachmentRechecksOwnerAfterConcurrentSessionDelete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	sessions := chat.NewMemoryStore()
	baseAttachments := chatattachments.NewMemoryStore()
	attachments := &blockingAttachmentCreateStore{
		Store:   baseAttachments,
		created: make(chan struct{}),
		release: make(chan struct{}),
	}
	app := New(Options{Store: sessions, Attachments: attachments})
	session, err := sessions.Create(ctx, chat.Session{ID: "chat_upload_delete", AgentID: chat.DefaultAgentID})
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}

	result := make(chan error, 1)
	go func() {
		_, err := app.CreateAttachment(ctx, CreateAttachmentCommand{Attachment: chatattachments.StoredAttachment{
			Attachment: chatattachments.Attachment{ID: "att_race", SessionID: session.ID, SizeBytes: 1},
			Data:       []byte("x"),
		}})
		result <- err
	}()
	<-attachments.created
	if err := app.DeleteSession(ctx, DeleteSessionCommand{SessionID: session.ID}); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	close(attachments.release)
	if err := <-result; !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("CreateAttachment concurrent delete error = %v, want ErrSessionNotFound", err)
	}
	if _, ok, err := baseAttachments.Get(ctx, session.ID, "att_race"); err != nil || ok {
		t.Fatalf("orphan attachment = ok %v, err %v", ok, err)
	}
}

func TestApplication_CreateAttachmentCleansUpAfterRequestCancelBeforeOwnerRecheck(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	baseSessions := chat.NewMemoryStore()
	sessions := cancelAwareSessionGetStore{SessionStore: baseSessions}
	baseAttachments := chatattachments.NewMemoryStore()
	attachments := &cancelAfterAttachmentCreateStore{
		Store:         baseAttachments,
		cancelRequest: cancel,
		cleanupLive:   make(chan bool, 1),
	}
	app := New(Options{Store: sessions, Attachments: attachments})
	if _, err := baseSessions.Create(context.Background(), chat.Session{ID: "chat_cancelled_upload", AgentID: chat.DefaultAgentID}); err != nil {
		t.Fatalf("Create session: %v", err)
	}

	_, err := app.CreateAttachment(ctx, CreateAttachmentCommand{Attachment: chatattachments.StoredAttachment{
		Attachment: chatattachments.Attachment{ID: "att_cancelled_upload", SessionID: "chat_cancelled_upload", SizeBytes: 1},
		Data:       []byte("x"),
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CreateAttachment error = %v, want request cancellation", err)
	}
	if live := <-attachments.cleanupLive; !live {
		t.Fatal("post-create rollback did not use a live bounded detached context")
	}
	if _, ok, getErr := baseAttachments.Get(context.Background(), "chat_cancelled_upload", "att_cancelled_upload"); getErr != nil || ok {
		t.Fatalf("Get after cancelled upload rollback = ok %v, err %v", ok, getErr)
	}
}

func TestApplication_CreateAttachmentReturnsSafeTypedErrorWhenOwnerRollbackFails(t *testing.T) {
	ctx := context.Background()
	sessions := chat.NewMemoryStore()
	if _, err := sessions.Create(ctx, chat.Session{ID: "chat_failed_rollback", AgentID: chat.DefaultAgentID}); err != nil {
		t.Fatalf("Create session: %v", err)
	}
	cleanupErr := errors.New("rollback exposed att_private private.png digest-private body-private")
	attachments := &ownerDeletingAttachmentStore{
		Store:      chatattachments.NewMemoryStore(),
		sessions:   sessions,
		cleanupErr: cleanupErr,
	}
	app := New(Options{Store: sessions, Attachments: attachments})

	_, err := app.CreateAttachment(ctx, CreateAttachmentCommand{Attachment: chatattachments.StoredAttachment{
		Attachment: chatattachments.Attachment{
			ID:        "att_private",
			SessionID: "chat_failed_rollback",
			Filename:  "private.png",
			SHA256:    "digest-private",
			SizeBytes: 12,
		},
		Data: []byte("body-private"),
	}})
	var rollbackErr *AttachmentRollbackError
	if !errors.As(err, &rollbackErr) {
		t.Fatalf("CreateAttachment error = %T %v, want AttachmentRollbackError", err, err)
	}
	if !errors.Is(err, ErrAttachmentRollback) || !errors.Is(err, ErrSessionNotFound) || !errors.Is(err, cleanupErr) {
		t.Fatalf("CreateAttachment error chain = %v, want rollback, owner, and cleanup causes", err)
	}
	for _, sensitive := range []string{"att_private", "private.png", "digest-private", "body-private"} {
		if strings.Contains(err.Error(), sensitive) {
			t.Fatalf("safe rollback error exposed %q: %v", sensitive, err)
		}
	}
	if _, ok, getErr := attachments.Store.Get(ctx, "chat_failed_rollback", "att_private"); getErr != nil || !ok {
		t.Fatalf("attachment after failed rollback = ok %v, err %v", ok, getErr)
	}
}

func TestApplication_CreateAttachmentPreservesEffectiveTotalQuotaLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sessions := chat.NewMemoryStore()
	if _, err := sessions.Create(ctx, chat.Session{ID: "chat_total_quota", AgentID: chat.DefaultAgentID}); err != nil {
		t.Fatalf("Create session: %v", err)
	}
	const limit = int64(1234)
	app := New(Options{
		Store: sessions,
		Attachments: attachmentCreateErrorStore{
			Store: chatattachments.NewMemoryStore(),
			err:   chatattachments.TotalQuotaError{LimitBytes: limit},
		},
	})

	_, err := app.CreateAttachment(ctx, CreateAttachmentCommand{Attachment: chatattachments.StoredAttachment{
		Attachment: chatattachments.Attachment{ID: "att_total_quota", SessionID: "chat_total_quota", SizeBytes: 1},
		Data:       []byte("x"),
	}})
	if !errors.Is(err, ErrAttachmentTotalQuota) {
		t.Fatalf("CreateAttachment error = %v, want ErrAttachmentTotalQuota", err)
	}
	var quota AttachmentTotalQuotaError
	if !errors.As(err, &quota) || quota.LimitBytes != limit {
		t.Fatalf("CreateAttachment quota = %#v, want limit %d", quota, limit)
	}
}

func TestApplication_GetAttachmentRequiresLiveHecateOwner(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	attachments := chatattachments.NewMemoryStore()
	if _, err := attachments.Create(ctx, chatattachments.StoredAttachment{
		Attachment: chatattachments.Attachment{ID: "att_orphan", SessionID: "missing", SizeBytes: 1},
		Data:       []byte("x"),
	}); err != nil {
		t.Fatalf("Create orphan fixture: %v", err)
	}
	app := New(Options{Store: chat.NewMemoryStore(), Attachments: attachments})
	if _, err := app.GetAttachment(ctx, AttachmentCommand{SessionID: "missing", AttachmentID: "att_orphan"}); !errors.Is(err, ErrAttachmentNotFound) {
		t.Fatalf("GetAttachment orphan error = %v, want ErrAttachmentNotFound", err)
	}
}

func TestApplication_ClaimAttachmentsReleasesDraftsAboveCombinedMessageLimit(t *testing.T) {
	ctx := context.Background()
	sessions := chat.NewMemoryStore()
	attachments := chatattachments.NewMemoryStore()
	app := New(Options{Store: sessions, Attachments: attachments})
	if _, err := sessions.Create(ctx, chat.Session{ID: "chat_combined_limit", AgentID: chat.DefaultAgentID}); err != nil {
		t.Fatalf("Create session: %v", err)
	}

	ids := []string{"att_1", "att_2", "att_3"}
	sizes := []int{4 << 20, 4 << 20, (4 << 20) + 1}
	for i, id := range ids {
		data := make([]byte, sizes[i])
		if _, err := app.CreateAttachment(ctx, CreateAttachmentCommand{Attachment: chatattachments.StoredAttachment{
			Attachment: chatattachments.Attachment{ID: id, SessionID: "chat_combined_limit", SizeBytes: int64(len(data))},
			Data:       data,
		}}); err != nil {
			t.Fatalf("CreateAttachment(%s): %v", id, err)
		}
	}

	if _, err := app.ClaimAttachments(ctx, chatattachments.ClaimRef{SessionID: "chat_combined_limit", MessageID: "msg_limit", AttachmentIDs: ids}); !errors.Is(err, ErrAttachmentMessageBytes) {
		t.Fatalf("ClaimAttachments error = %v, want ErrAttachmentMessageBytes", err)
	}
	for _, id := range ids {
		if err := app.DeleteAttachment(ctx, AttachmentCommand{SessionID: "chat_combined_limit", AttachmentID: id}); err != nil {
			t.Fatalf("DeleteAttachment(%s) after rejected claim: %v", id, err)
		}
	}
}

func TestApplication_ClaimAttachmentsBoundsDetachedReleaseAfterRequestCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sessions := chat.NewMemoryStore()
	baseAttachments := chatattachments.NewMemoryStore()
	attachments := &deadlineAttachmentResolveStore{
		Store:         baseAttachments,
		cancelRequest: cancel,
		deadlineSeen:  make(chan bool, 1),
		resolveErr:    make(chan error, 1),
	}
	app := New(Options{
		Store:                    sessions,
		Attachments:              attachments,
		AttachmentCleanupTimeout: 20 * time.Millisecond,
	})
	if _, err := sessions.Create(context.Background(), chat.Session{ID: "chat_cleanup_deadline", AgentID: chat.DefaultAgentID}); err != nil {
		t.Fatalf("Create session: %v", err)
	}
	body := make([]byte, MaxMessageAttachmentBytes+1)
	if _, err := baseAttachments.Create(context.Background(), chatattachments.StoredAttachment{
		Attachment: chatattachments.Attachment{ID: "att_large", SessionID: "chat_cleanup_deadline", SizeBytes: int64(len(body))},
		Data:       body,
	}); err != nil {
		t.Fatalf("Create attachment: %v", err)
	}

	_, err := app.ClaimAttachments(ctx, chatattachments.ClaimRef{
		SessionID:     "chat_cleanup_deadline",
		MessageID:     "msg_cleanup_deadline",
		AttachmentIDs: []string{"att_large"},
	})
	if !errors.Is(err, ErrAttachmentMessageBytes) {
		t.Fatalf("ClaimAttachments error = %v, want ErrAttachmentMessageBytes", err)
	}
	if ctx.Err() == nil {
		t.Fatal("claim fixture did not cancel the request context")
	}
	if hasDeadline := <-attachments.deadlineSeen; !hasDeadline {
		t.Fatal("detached claim release had no deadline")
	}
	if resolveErr := <-attachments.resolveErr; !errors.Is(resolveErr, context.DeadlineExceeded) {
		t.Fatalf("ResolveClaim context error = %v, want deadline exceeded", resolveErr)
	}
	pending, pendingErr := baseAttachments.ListPendingClaims(context.Background())
	if pendingErr != nil {
		t.Fatalf("ListPendingClaims: %v", pendingErr)
	}
	if len(pending) != 1 || pending[0].Ref.MessageID != "msg_cleanup_deadline" {
		t.Fatalf("pending claims = %#v, want fenced claim left for startup reconciliation", pending)
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
	if result.Session.DriverKind != "" || result.Session.NativeSessionID != "" || result.Session.AgentInfo != nil {
		t.Fatalf("session runtime handles = %q/%q/%#v, want cleared", result.Session.DriverKind, result.Session.NativeSessionID, result.Session.AgentInfo)
	}
}

func TestApplication_DeleteAndCloseNativeSessionNilStore(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	app := New(Options{})
	if err := app.DeleteSession(ctx, DeleteSessionCommand{SessionID: "chat"}); !errors.Is(err, ErrStoreNotConfigured) {
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

func TestApplication_SetHecateSettingsWorkspaceMode(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := chat.NewMemoryStore()
	app := New(Options{Store: store})
	session, err := store.Create(ctx, chat.Session{
		ID:            "chat_workspace_mode",
		AgentID:       chat.DefaultAgentID,
		WorkspaceMode: chat.WorkspaceModePersistent,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	inPlace := chat.WorkspaceModeInPlace
	result, err := app.SetHecateSettings(ctx, SetHecateSettingsCommand{
		Session:       session,
		WorkspaceMode: &inPlace,
	})
	if err != nil {
		t.Fatalf("SetHecateSettings(workspace mode) error = %v", err)
	}
	if result.Session.WorkspaceMode != chat.WorkspaceModeInPlace {
		t.Fatalf("workspace mode = %q, want in_place", result.Session.WorkspaceMode)
	}

	invalid := "shared"
	if _, err := app.SetHecateSettings(ctx, SetHecateSettingsCommand{
		Session:       result.Session,
		WorkspaceMode: &invalid,
	}); !errors.Is(err, ErrWorkspaceModeInvalid) {
		t.Fatalf("SetHecateSettings(invalid workspace mode) error = %v, want ErrWorkspaceModeInvalid", err)
	}

	locked := result.Session
	locked.TaskID = "task_started"
	persistent := chat.WorkspaceModePersistent
	if _, err := app.SetHecateSettings(ctx, SetHecateSettingsCommand{
		Session:       locked,
		WorkspaceMode: &persistent,
	}); !errors.Is(err, ErrWorkspaceModeLocked) {
		t.Fatalf("SetHecateSettings(locked workspace mode) error = %v, want ErrWorkspaceModeLocked", err)
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

func TestApplication_AdmitMessageAllowsImageOnlyDirectTurns(t *testing.T) {
	t.Parallel()

	toolsOff := false
	result, err := New(Options{}).AdmitMessage(AdmitMessageCommand{
		Session:         chat.Session{AgentID: chat.DefaultAgentID},
		ToolsEnabled:    &toolsOff,
		AttachmentCount: 1,
	})
	if err != nil {
		t.Fatalf("AdmitMessage(image only) error = %v", err)
	}
	if result.Content != "" || result.ToolsEnabled {
		t.Fatalf("result = %+v, want empty content and tools off", result)
	}
}

func TestApplication_AdmitMessageAllowsHecateToolsOnAndExternalAgentAttachments(t *testing.T) {
	t.Parallel()

	toolsOn := true
	toolsOff := false
	cmd := AdmitMessageCommand{Session: chat.Session{AgentID: chat.DefaultAgentID}, ToolsEnabled: &toolsOn, AttachmentCount: 1}
	hecate, err := New(Options{}).AdmitMessage(cmd)
	if err != nil {
		t.Fatalf("AdmitMessage(Hecate tools-on attachment) error = %v", err)
	}
	if !hecate.ToolsEnabled || hecate.ExecutionMode != chat.ExecutionModeHecateTask {
		t.Fatalf("Hecate admission = %+v, want tools-on Hecate task", hecate)
	}
	external, err := New(Options{}).AdmitMessage(AdmitMessageCommand{
		Session:         chat.Session{AgentID: "codex"},
		ExecutionMode:   chat.ExecutionModeExternalAgent,
		ToolsEnabled:    &toolsOff,
		AttachmentCount: 1,
	})
	if err != nil {
		t.Fatalf("AdmitMessage(external attachment) error = %v", err)
	}
	if external.Content != "" || external.ExecutionMode != chat.ExecutionModeExternalAgent {
		t.Fatalf("external admission = %+v", external)
	}
	tooMany := AdmitMessageCommand{Session: chat.Session{AgentID: chat.DefaultAgentID}, ToolsEnabled: &toolsOff, AttachmentCount: MaxMessageAttachments + 1}
	if _, err := New(Options{}).AdmitMessage(tooMany); !errors.Is(err, ErrTooManyAttachments) {
		t.Fatalf("AdmitMessage(too many) error = %v, want ErrTooManyAttachments", err)
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
