package chat

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/storage"
	"github.com/hecatehq/hecate/pkg/types"
)

func newSQLiteTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dir := t.TempDir()
	client, err := storage.NewSQLiteClient(context.Background(), storage.SQLiteConfig{
		Path:        filepath.Join(dir, "chat.db"),
		TablePrefix: "test",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	store, err := NewSQLiteStore(context.Background(), client)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	return store
}

func TestSQLiteStoreConformance(t *testing.T) {
	RunConformanceTests(t, "SQLiteStore", func(t *testing.T) Store {
		return newSQLiteTestStore(t)
	})
}

func TestSQLiteStoreMigratesProviderInstanceColumn(t *testing.T) {
	t.Parallel()

	store := newSQLiteTestStore(t)
	if _, err := store.Create(context.Background(), Session{ID: "chat_legacy_generation", Title: "Legacy", AgentID: DefaultAgentID}); err != nil {
		t.Fatalf("Create legacy session: %v", err)
	}
	if _, err := store.AppendMessage(context.Background(), "chat_legacy_generation", Message{
		ID:      "msg_legacy_generation",
		Role:    "user",
		Content: "preserve me",
	}); err != nil {
		t.Fatalf("AppendMessage legacy row: %v", err)
	}
	if _, err := store.client.DB().ExecContext(context.Background(), "ALTER TABLE "+store.messagesTable+" DROP COLUMN provider_instance"); err != nil {
		t.Fatalf("drop provider_instance test column: %v", err)
	}
	exists, err := store.columnExists(context.Background(), store.messagesTable, "provider_instance")
	if err != nil || exists {
		t.Fatalf("column before migration exists=%v err=%v, want absent", exists, err)
	}

	migrated, err := newSQLStore(context.Background(), store.client)
	if err != nil {
		t.Fatalf("newSQLStore migration: %v", err)
	}
	exists, err = migrated.columnExists(context.Background(), migrated.messagesTable, "provider_instance")
	if err != nil || !exists {
		t.Fatalf("column after migration exists=%v err=%v, want present", exists, err)
	}
	got, ok, err := migrated.Get(context.Background(), "chat_legacy_generation")
	if err != nil || !ok {
		t.Fatalf("Get legacy session after migration: found=%v err=%v", ok, err)
	}
	if len(got.Messages) != 1 || got.Messages[0].Content != "preserve me" || got.Messages[0].ProviderInstance.Valid() {
		t.Fatalf("legacy message after migration = %+v, want preserved row with empty provider instance", got.Messages)
	}
}

func TestSQLiteStorePersistsAcrossInstances(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "chat.db")
	client, err := storage.NewSQLiteClient(context.Background(), storage.SQLiteConfig{
		Path:        path,
		TablePrefix: "test",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient: %v", err)
	}
	store, err := NewSQLiteStore(context.Background(), client)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	if _, err := store.Create(context.Background(), Session{
		ID:              "chat_1",
		Title:           "Persist me",
		ProjectID:       "proj_sqlite",
		AgentID:         "cursor_agent",
		Workspace:       "/tmp/hecate",
		WorkspaceMode:   WorkspaceModeInPlace,
		WorkspaceBranch: "feature/sqlite",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.AppendMessage(context.Background(), "chat_1", Message{
		ID:               "msg_1",
		Role:             "user",
		Content:          "hello",
		ProviderInstance: types.ProviderInstanceIdentity{ID: "runtime-before-update", Kind: types.ProviderInstanceIdentityRuntime},
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	wantProviderInstance := types.ProviderInstanceIdentity{ID: "configuration-after-update", Kind: types.ProviderInstanceIdentityConfiguration}
	if _, err := store.UpdateMessage(context.Background(), "chat_1", "msg_1", func(message *Message) {
		message.ProviderInstance = wantProviderInstance
	}); err != nil {
		t.Fatalf("UpdateMessage provider instance: %v", err)
	}
	if _, err := store.AppendMessage(context.Background(), "chat_1", Message{
		ID:            "msg_2",
		ExecutionMode: ExecutionModeExternalAgent,
		Role:          "assistant",
		Content:       "hello from cursor",
		Context: ContextPacket{
			Version:       "chat.context.v1",
			ExecutionMode: ExecutionModeExternalAgent,
			Workspace:     "/tmp/hecate",
			MessageCount:  2,
			Sources: []ContextSource{
				{
					Kind:  "adapter_session",
					Label: "Cursor Agent ACP session",
					Trust: "adapter",
				},
			},
			Items: []ContextItem{
				{
					Kind:       "external_agent_session",
					TrustLevel: "runtime_state",
					Origin:     "adapter:Cursor Agent",
					Title:      "Cursor Agent ACP session",
					Body:       "Hecate cannot inspect the external agent private prompt.",
					Included:   true,
				},
			},
		},
	}); err != nil {
		t.Fatalf("AppendMessage assistant: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close first client: %v", err)
	}

	client, err = storage.NewSQLiteClient(context.Background(), storage.SQLiteConfig{
		Path:        path,
		TablePrefix: "test",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient reopen: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	reopened, err := NewSQLiteStore(context.Background(), client)
	if err != nil {
		t.Fatalf("NewSQLiteStore reopen: %v", err)
	}
	got, ok, err := reopened.Get(context.Background(), "chat_1")
	if err != nil || !ok {
		t.Fatalf("Get after reopen: ok=%v err=%v", ok, err)
	}
	if got.ProjectID != "proj_sqlite" || got.AgentID != "cursor_agent" || got.WorkspaceMode != WorkspaceModeInPlace || got.WorkspaceBranch != "feature/sqlite" || got.Messages[0].Content != "hello" {
		t.Fatalf("reopened session mismatch: %+v", got)
	}
	if got.Messages[0].ProviderInstance != wantProviderInstance {
		t.Fatalf("reopened provider instance = %+v, want %+v", got.Messages[0].ProviderInstance, wantProviderInstance)
	}
	if len(got.Messages) != 2 || got.Messages[1].Context.Version != "chat.context.v1" || got.Messages[1].Context.Sources[0].Label != "Cursor Agent ACP session" || got.Messages[1].Context.Items[0].Kind != "external_agent_session" {
		t.Fatalf("reopened context packet mismatch: %+v", got.Messages)
	}
}

func TestSQLiteStoreMessageRequestSurvivesRestartAndReclaimsPendingOwner(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	client, err := storage.NewSQLiteClient(context.Background(), storage.SQLiteConfig{
		Path:        filepath.Join(dir, "chat-idempotency.db"),
		TablePrefix: "test",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	first, err := NewSQLiteStore(context.Background(), client)
	if err != nil {
		t.Fatalf("NewSQLiteStore first: %v", err)
	}
	if _, err := first.Create(context.Background(), Session{ID: "chat_restart", AgentID: DefaultAgentID}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	fingerprint := messageRequestTestFingerprint("persisted payload")
	committedClaim, err := first.ClaimMessageRequest(context.Background(), "chat_restart", "queued-committed", fingerprint)
	if err != nil {
		t.Fatalf("ClaimMessageRequest committed: %v", err)
	}
	if _, err := first.CommitMessageRequest(context.Background(), committedClaim.Lease, Message{ID: "msg_persisted", Role: "user", Content: "persist me"}); err != nil {
		t.Fatalf("CommitMessageRequest: %v", err)
	}
	interrupted, err := first.ClaimMessageRequest(context.Background(), "chat_restart", "queued-interrupted", fingerprint)
	if err != nil {
		t.Fatalf("ClaimMessageRequest interrupted: %v", err)
	}

	// A second store may be another live runtime: it must replay committed keys
	// but must not steal a fresh pre-dispatch reservation.
	restarted, err := newSQLStore(context.Background(), client)
	if err != nil {
		t.Fatalf("newSQLStore restart: %v", err)
	}
	replay, err := restarted.ClaimMessageRequest(context.Background(), "chat_restart", "queued-committed", fingerprint)
	if err != nil {
		t.Fatalf("ClaimMessageRequest replay after restart: %v", err)
	}
	if !replay.Replay || len(replay.Session.Messages) != 1 || replay.Session.Messages[0].ID != "msg_persisted" {
		t.Fatalf("restart replay = %+v, want persisted authoritative session", replay)
	}
	waitCtx, cancelWait := context.WithTimeout(context.Background(), 60*time.Millisecond)
	_, err = restarted.ClaimMessageRequest(waitCtx, "chat_restart", "queued-interrupted", fingerprint)
	cancelWait()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("fresh foreign ClaimMessageRequest error = %v, want deadline without takeover", err)
	}
	if _, err := client.DB().ExecContext(
		context.Background(),
		"UPDATE "+first.messageRequestsTable+" SET updated_at = ? WHERE session_id = ? AND client_request_id = ?",
		time.Now().UTC().Add(-messageRequestStaleAfter-time.Minute),
		"chat_restart",
		"queued-interrupted",
	); err != nil {
		t.Fatalf("expire pending message request: %v", err)
	}
	reclaimed, err := restarted.ClaimMessageRequest(context.Background(), "chat_restart", "queued-interrupted", fingerprint)
	if err != nil {
		t.Fatalf("ClaimMessageRequest reclaim after restart: %v", err)
	}
	if reclaimed.Replay || reclaimed.Lease.Empty() {
		t.Fatalf("restart reclaim = %+v, want fresh lease", reclaimed)
	}
	if _, err := restarted.CommitMessageRequest(context.Background(), reclaimed.Lease, Message{ID: "msg_reclaimed_owner", Role: "user", Content: "reclaimed once"}); err != nil {
		t.Fatalf("reclaimed owner commit: %v", err)
	}
	if _, err := first.CommitMessageRequest(context.Background(), interrupted.Lease, Message{ID: "msg_stale_owner", Role: "user", Content: "must not append"}); !errors.Is(err, ErrMessageRequestLeaseInvalid) {
		t.Fatalf("stale owner commit error = %v, want invalid lease", err)
	}
	got, ok, err := restarted.Get(context.Background(), "chat_restart")
	if err != nil || !ok {
		t.Fatalf("Get after stale owner: found=%v err=%v", ok, err)
	}
	if len(got.Messages) != 2 || got.Messages[1].ID != "msg_reclaimed_owner" {
		t.Fatalf("messages after stale owner = %+v, want only committed and reclaimed rows", got.Messages)
	}
	if err := restarted.ReleaseMessageRequest(context.Background(), reclaimed.Lease); err != nil {
		t.Fatalf("ReleaseMessageRequest: %v", err)
	}
}
