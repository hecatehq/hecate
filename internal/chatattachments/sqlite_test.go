package chatattachments

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hecatehq/hecate/internal/storage"
)

func TestSQLiteStore_Conformance(t *testing.T) {
	RunConformanceTests(t, "SQLiteStore", func(t *testing.T) Store {
		t.Helper()
		client, err := storage.NewSQLiteClient(context.Background(), storage.SQLiteConfig{
			Path:        filepath.Join(t.TempDir(), "attachments.db"),
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
	})
}

func TestSQLiteStore_MigratesStaleDraftReclaimIndex(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client, err := storage.NewSQLiteClient(ctx, storage.SQLiteConfig{
		Path:        filepath.Join(t.TempDir(), "attachments.db"),
		TablePrefix: "test",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if _, err := NewSQLiteStore(ctx, client); err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}

	rows, err := client.DB().QueryContext(ctx, `PRAGMA index_list(`+client.QualifiedTable("chat_attachments")+`)`)
	if err != nil {
		t.Fatalf("PRAGMA index_list: %v", err)
	}
	defer rows.Close()
	want := client.TableName("chat_attachments") + "_lifecycle_created_idx"
	for rows.Next() {
		var sequence int
		var name string
		var unique int
		var origin string
		var partial int
		if err := rows.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			t.Fatalf("scan index: %v", err)
		}
		if name == want {
			return
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate indexes: %v", err)
	}
	t.Fatalf("missing reclaim index %q", want)
}

func TestSQLiteStore_PersistsAcrossInstances(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "attachments.db")
	client, err := storage.NewSQLiteClient(ctx, storage.SQLiteConfig{Path: path, TablePrefix: "test"})
	if err != nil {
		t.Fatalf("NewSQLiteClient: %v", err)
	}
	store, err := NewSQLiteStore(ctx, client)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	if _, err := store.Create(ctx, StoredAttachment{
		Attachment: Attachment{
			ID:        "attachment_persisted",
			SessionID: "session_persisted",
			Filename:  "persisted.png",
			MediaType: "image/png",
			SizeBytes: 9,
			SHA256:    "sha-persisted",
		},
		Data: []byte("persisted"),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	claimRef := ClaimRef{
		SessionID:     "session_persisted",
		MessageID:     "message_persisted",
		AttachmentIDs: []string{"attachment_persisted"},
	}
	if _, err := store.Claim(ctx, claimRef); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close first client: %v", err)
	}

	client, err = storage.NewSQLiteClient(ctx, storage.SQLiteConfig{Path: path, TablePrefix: "test"})
	if err != nil {
		t.Fatalf("NewSQLiteClient reopen: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	reopened, err := NewSQLiteStore(ctx, client)
	if err != nil {
		t.Fatalf("NewSQLiteStore reopen: %v", err)
	}
	got, ok, err := reopened.Get(ctx, "session_persisted", "attachment_persisted")
	if err != nil || !ok {
		t.Fatalf("Get after reopen: ok=%v err=%v", ok, err)
	}
	if string(got.Data) != "persisted" || got.Filename != "persisted.png" {
		t.Fatalf("Get after reopen = %#v", got)
	}
	pending, err := reopened.ListPendingClaims(ctx)
	if err != nil {
		t.Fatalf("ListPendingClaims after reopen: %v", err)
	}
	if len(pending) != 1 || pending[0].Ref.MessageID != claimRef.MessageID || len(pending[0].Ref.AttachmentIDs) != 1 || pending[0].Ref.AttachmentIDs[0] != "attachment_persisted" {
		t.Fatalf("pending after reopen = %#v, want persisted claim fence", pending)
	}
}
