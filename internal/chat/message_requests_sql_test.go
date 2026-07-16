package chat

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/hecatehq/hecate/internal/storage"
)

type cancelAfterCommitSQLClient struct {
	storage.SQLClient

	mu     sync.Mutex
	cancel context.CancelFunc
}

func (client *cancelAfterCommitSQLClient) DB() storage.DB {
	return &cancelAfterCommitDB{DB: client.SQLClient.DB(), client: client}
}

func (client *cancelAfterCommitSQLClient) arm(cancel context.CancelFunc) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.cancel = cancel
}

func (client *cancelAfterCommitSQLClient) takeCancel() context.CancelFunc {
	client.mu.Lock()
	defer client.mu.Unlock()
	cancel := client.cancel
	client.cancel = nil
	return cancel
}

type cancelAfterCommitDB struct {
	storage.DB
	client *cancelAfterCommitSQLClient
}

func (db *cancelAfterCommitDB) BeginTx(ctx context.Context, opts *sql.TxOptions) (storage.Tx, error) {
	tx, err := db.DB.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &cancelAfterCommitTx{Tx: tx, client: db.client}, nil
}

type cancelAfterCommitTx struct {
	storage.Tx
	client *cancelAfterCommitSQLClient
}

func (tx *cancelAfterCommitTx) Commit() error {
	if err := tx.Tx.Commit(); err != nil {
		return err
	}
	if cancel := tx.client.takeCancel(); cancel != nil {
		cancel()
	}
	return nil
}

func TestSQLStoreCommitMessageRequestReturnsSessionWhenContextCancelsAfterCommit(t *testing.T) {
	baseClient, err := storage.NewSQLiteClient(t.Context(), storage.SQLiteConfig{
		Path:        filepath.Join(t.TempDir(), "chat-message-request.db"),
		TablePrefix: "cancel_after_commit",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient: %v", err)
	}
	t.Cleanup(func() { _ = baseClient.Close() })

	client := &cancelAfterCommitSQLClient{SQLClient: baseClient}
	store, err := newSQLStore(t.Context(), client)
	if err != nil {
		t.Fatalf("newSQLStore: %v", err)
	}
	if _, err := store.Create(t.Context(), Session{ID: "chat_cancel_after_commit", AgentID: DefaultAgentID}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	fingerprint := messageRequestTestFingerprint("cancel after durable commit")
	claim, err := store.ClaimMessageRequest(t.Context(), "chat_cancel_after_commit", "queued-cancel-after-commit", fingerprint)
	if err != nil {
		t.Fatalf("ClaimMessageRequest: %v", err)
	}

	commitCtx, cancelCommit := context.WithCancel(t.Context())
	client.arm(cancelCommit)
	committed, err := store.CommitMessageRequest(commitCtx, claim.Lease, Message{
		ID:      "msg_committed_before_cancel",
		Role:    "user",
		Content: "dispatch this turn exactly once",
	})
	if err != nil {
		t.Fatalf("CommitMessageRequest after durable commit cancellation: %v", err)
	}
	if !errors.Is(commitCtx.Err(), context.Canceled) {
		t.Fatalf("commit context error = %v, want cancellation immediately after SQL commit", commitCtx.Err())
	}
	if len(committed.Messages) != 1 || committed.Messages[0].ID != "msg_committed_before_cancel" {
		t.Fatalf("committed session messages = %+v, want durable user row returned", committed.Messages)
	}

	replay, err := store.ClaimMessageRequest(t.Context(), "chat_cancel_after_commit", "queued-cancel-after-commit", fingerprint)
	if err != nil {
		t.Fatalf("ClaimMessageRequest replay: %v", err)
	}
	if !replay.Replay || replay.CommittedMessageID != "msg_committed_before_cancel" || len(replay.Session.Messages) != 1 {
		t.Fatalf("replay = %+v, want one committed user row", replay)
	}
}
