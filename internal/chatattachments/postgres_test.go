package chatattachments

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/storage"
)

func TestPostgresStore_Conformance(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("HECATE_POSTGRES_TEST_URL"))
	if databaseURL == "" {
		t.Skip("set HECATE_POSTGRES_TEST_URL to run Postgres attachment-store conformance")
	}
	var sequence atomic.Uint64
	RunConformanceTests(t, "PostgresStore", func(t *testing.T) Store {
		t.Helper()
		prefix := fmt.Sprintf("attachment_test_%d_%d", time.Now().UnixNano(), sequence.Add(1))
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
	})
}
