package chat

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hecatehq/hecate/internal/storage"
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
		WorkspaceBranch: "feature/sqlite",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.AppendMessage(context.Background(), "chat_1", Message{
		ID:      "msg_1",
		Role:    "user",
		Content: "hello",
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
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
	if got.ProjectID != "proj_sqlite" || got.AgentID != "cursor_agent" || got.WorkspaceBranch != "feature/sqlite" || got.Messages[0].Content != "hello" {
		t.Fatalf("reopened session mismatch: %+v", got)
	}
	if len(got.Messages) != 2 || got.Messages[1].Context.Version != "chat.context.v1" || got.Messages[1].Context.Sources[0].Label != "Cursor Agent ACP session" {
		t.Fatalf("reopened context packet mismatch: %+v", got.Messages)
	}
}
