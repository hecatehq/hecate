package chat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/storage"
)

func TestPostgresStoreConformance(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("HECATE_POSTGRES_TEST_URL"))
	if databaseURL == "" {
		t.Skip("set HECATE_POSTGRES_TEST_URL to run Postgres chat-store conformance")
	}
	var sequence atomic.Uint64
	RunConformanceTests(t, "PostgresStore", func(t *testing.T) Store {
		t.Helper()
		prefix := fmt.Sprintf("chat_test_%d_%d", time.Now().UnixNano(), sequence.Add(1))
		client, err := storage.NewPostgresClient(context.Background(), storage.PostgresConfig{
			DatabaseURL: databaseURL,
			TablePrefix: prefix,
		})
		if err != nil {
			t.Fatalf("NewPostgresClient: %v", err)
		}
		store, err := NewPostgresStore(context.Background(), client)
		if err != nil {
			_ = client.Close()
			t.Fatalf("NewPostgresStore: %v", err)
		}
		t.Cleanup(func() {
			for _, table := range []string{"chat_message_requests", "chat_messages", "chat_sessions"} {
				_, _ = client.DB().ExecContext(context.Background(), "DROP TABLE IF EXISTS "+client.QualifiedTable(table))
			}
			_ = client.Close()
		})
		return store
	})
}

func TestPostgresStoreMessageRequestForeignOwnerLease(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("HECATE_POSTGRES_TEST_URL"))
	if databaseURL == "" {
		t.Skip("set HECATE_POSTGRES_TEST_URL to run Postgres chat-store lease conformance")
	}
	prefix := fmt.Sprintf("chat_lease_test_%d", time.Now().UnixNano())
	client, err := storage.NewPostgresClient(context.Background(), storage.PostgresConfig{
		DatabaseURL: databaseURL,
		TablePrefix: prefix,
	})
	if err != nil {
		t.Fatalf("NewPostgresClient: %v", err)
	}
	t.Cleanup(func() {
		for _, table := range []string{"chat_message_requests", "chat_messages", "chat_sessions"} {
			_, _ = client.DB().ExecContext(context.Background(), "DROP TABLE IF EXISTS "+client.QualifiedTable(table))
		}
		_ = client.Close()
	})
	first, err := NewPostgresStore(context.Background(), client)
	if err != nil {
		t.Fatalf("NewPostgresStore first: %v", err)
	}
	if _, err := first.Create(context.Background(), Session{ID: "chat_foreign_lease", AgentID: DefaultAgentID}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	fingerprint := messageRequestTestFingerprint("foreign owner payload")
	firstClaim, err := first.ClaimMessageRequest(context.Background(), "chat_foreign_lease", "queued-foreign", fingerprint)
	if err != nil {
		t.Fatalf("ClaimMessageRequest first: %v", err)
	}
	second, err := NewPostgresStore(context.Background(), client)
	if err != nil {
		t.Fatalf("NewPostgresStore second: %v", err)
	}
	waitCtx, cancelWait := context.WithTimeout(context.Background(), 75*time.Millisecond)
	_, err = second.ClaimMessageRequest(waitCtx, "chat_foreign_lease", "queued-foreign", fingerprint)
	cancelWait()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("fresh foreign claim error = %v, want deadline without takeover", err)
	}
	if _, err := client.DB().ExecContext(
		context.Background(),
		"UPDATE "+first.messageRequestsTable+" SET updated_at = ? WHERE session_id = ? AND client_request_id = ?",
		time.Now().UTC().Add(-messageRequestStaleAfter-time.Minute),
		"chat_foreign_lease",
		"queued-foreign",
	); err != nil {
		t.Fatalf("expire pending message request: %v", err)
	}
	reclaimed, err := second.ClaimMessageRequest(context.Background(), "chat_foreign_lease", "queued-foreign", fingerprint)
	if err != nil {
		t.Fatalf("ClaimMessageRequest stale takeover: %v", err)
	}
	if reclaimed.Replay || reclaimed.Lease.Empty() {
		t.Fatalf("stale takeover = %+v, want fresh owned lease", reclaimed)
	}
	if _, err := second.CommitMessageRequest(context.Background(), reclaimed.Lease, Message{ID: "msg_reclaimed_owner", Role: "user", Content: "reclaimed once"}); err != nil {
		t.Fatalf("reclaimed owner commit: %v", err)
	}
	if _, err := first.CommitMessageRequest(context.Background(), firstClaim.Lease, Message{ID: "msg_stale_owner", Role: "user", Content: "must not append"}); !errors.Is(err, ErrMessageRequestLeaseInvalid) {
		t.Fatalf("stale owner commit error = %v, want invalid lease", err)
	}
	got, ok, err := second.Get(context.Background(), "chat_foreign_lease")
	if err != nil || !ok {
		t.Fatalf("Get after stale owner: found=%v err=%v", ok, err)
	}
	if len(got.Messages) != 1 || got.Messages[0].ID != "msg_reclaimed_owner" {
		t.Fatalf("messages after stale owner = %+v, want reclaimed row only", got.Messages)
	}
	if err := second.ReleaseMessageRequest(context.Background(), reclaimed.Lease); err != nil {
		t.Fatalf("ReleaseMessageRequest: %v", err)
	}
}

func TestPostgresStoreMessageUpdateAndTaskLinkUseOneLockOrder(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("HECATE_POSTGRES_TEST_URL"))
	if databaseURL == "" {
		t.Skip("set HECATE_POSTGRES_TEST_URL to run Postgres chat-store lock-order regression")
	}
	prefix := fmt.Sprintf("chat_lock_order_test_%d", time.Now().UnixNano())
	client, err := storage.NewPostgresClient(context.Background(), storage.PostgresConfig{
		DatabaseURL: databaseURL,
		TablePrefix: prefix,
	})
	if err != nil {
		t.Fatalf("NewPostgresClient: %v", err)
	}
	t.Cleanup(func() {
		for _, table := range []string{"chat_message_requests", "chat_messages", "chat_sessions"} {
			_, _ = client.DB().ExecContext(context.Background(), "DROP TABLE IF EXISTS "+client.QualifiedTable(table))
		}
		_ = client.Close()
	})
	first, err := NewPostgresStore(context.Background(), client)
	if err != nil {
		t.Fatalf("NewPostgresStore first: %v", err)
	}
	second, err := NewPostgresStore(context.Background(), client)
	if err != nil {
		t.Fatalf("NewPostgresStore second: %v", err)
	}
	const sessionID = "chat_lock_order"
	if _, err := first.Create(context.Background(), Session{ID: sessionID, AgentID: DefaultAgentID}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := first.AppendMessage(context.Background(), sessionID, Message{ID: "msg_user", Role: "user"}); err != nil {
		t.Fatalf("AppendMessage user: %v", err)
	}
	if _, err := first.AppendMessage(context.Background(), sessionID, Message{ID: "msg_assistant", Role: "assistant", Status: "running"}); err != nil {
		t.Fatalf("AppendMessage assistant: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	updateStarted := make(chan struct{})
	releaseUpdate := make(chan struct{})
	updateDone := make(chan error, 1)
	go func() {
		_, updateErr := first.UpdateMessage(ctx, sessionID, "msg_assistant", func(message *Message) {
			close(updateStarted)
			<-releaseUpdate
			message.Content = "settled"
		})
		updateDone <- updateErr
	}()
	select {
	case <-updateStarted:
	case <-ctx.Done():
		t.Fatalf("UpdateMessage did not acquire its row locks: %v", ctx.Err())
	}

	linkDone := make(chan error, 1)
	go func() {
		_, linkErr := second.LinkTaskRun(ctx, sessionID, "msg_user", "msg_assistant", func(session *Session, user, assistant *Message) {
			session.TaskID = "task_lock_order"
			session.LatestRunID = "run_lock_order"
			user.TaskID = session.TaskID
			user.RunID = session.LatestRunID
			assistant.TaskID = session.TaskID
			assistant.RunID = session.LatestRunID
		})
		linkDone <- linkErr
	}()

	// Give the second store time to reach the row-lock boundary. With the old
	// message-then-session order it held the session while waiting on the
	// assistant message, forming a cycle when the first update resumed.
	time.Sleep(150 * time.Millisecond)
	close(releaseUpdate)
	if err := <-updateDone; err != nil {
		t.Fatalf("UpdateMessage: %v", err)
	}
	if err := <-linkDone; err != nil {
		t.Fatalf("LinkTaskRun: %v", err)
	}
	got, found, err := first.Get(ctx, sessionID)
	if err != nil || !found {
		t.Fatalf("Get: found=%v err=%v", found, err)
	}
	if got.TaskID != "task_lock_order" || got.LatestRunID != "run_lock_order" {
		t.Fatalf("session link = task %q run %q", got.TaskID, got.LatestRunID)
	}
	if len(got.Messages) != 2 || got.Messages[1].Content != "settled" || got.Messages[1].TaskID != "task_lock_order" || got.Messages[1].RunID != "run_lock_order" {
		t.Fatalf("assistant projection = %+v", got.Messages)
	}
}
