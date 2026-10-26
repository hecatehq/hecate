package chatattachments

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

var postgresTestSequence atomic.Uint64

func newPostgresTestStore(t *testing.T) *SQLStore {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv("HECATE_POSTGRES_TEST_URL"))
	if databaseURL == "" {
		t.Skip("set HECATE_POSTGRES_TEST_URL to run Postgres attachment-store conformance")
	}
	prefix := fmt.Sprintf("attachment_test_%d_%d", time.Now().UnixNano(), postgresTestSequence.Add(1))
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
		_, _ = client.DB().ExecContext(
			context.Background(),
			"DROP TABLE IF EXISTS "+client.QualifiedTable("chat_attachments"),
		)
		_ = client.Close()
	})
	return store
}

func TestPostgresStore_Conformance(t *testing.T) {
	RunConformanceTests(t, "PostgresStore", func(t *testing.T) Store { return newPostgresTestStore(t) })
}

func TestPostgresStore_TransactionLockScopes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		global    bool
		blockedID string
	}{
		{name: "session", blockedID: "owner"},
		{name: "total quota", global: true, blockedID: "other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newPostgresTestStore(t)
			held, err := store.beginLockedTx(t.Context(), "owner", tc.global)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Rollback()

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			independent, err := store.beginSessionTx(ctx, "other")
			if err != nil {
				t.Fatalf("unrelated session blocked: %v", err)
			}
			_ = independent.Rollback()

			blockedCtx, stop := context.WithTimeout(t.Context(), 200*time.Millisecond)
			defer stop()
			blocked, err := store.beginLockedTx(blockedCtx, tc.blockedID, tc.global)
			if err == nil {
				_ = blocked.Rollback()
				t.Fatal("conflicting transaction acquired the held lock")
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("blocked transaction error = %v, want deadline", err)
			}
			if err := held.Rollback(); err != nil {
				t.Fatal(err)
			}
			released, err := store.beginLockedTx(ctx, tc.blockedID, tc.global)
			if err != nil {
				t.Fatalf("lock not released by rollback: %v", err)
			}
			_ = released.Rollback()
		})
	}

	t.Run("session name cannot alias total quota", func(t *testing.T) {
		store := newPostgresTestStore(t)
		held, err := store.beginSessionTx(t.Context(), "total_quota")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Rollback()
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		other, err := store.beginCreateTx(ctx, "other")
		if err != nil {
			t.Fatalf("session key aliased global quota key: %v", err)
		}
		_ = other.Rollback()
	})
}
