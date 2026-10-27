package agentprofiles

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

var postgresProfileTestSequence atomic.Uint64

func newPostgresProfileTestClient(t *testing.T) *storage.PostgresClient {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv("HECATE_POSTGRES_TEST_URL"))
	if databaseURL == "" {
		t.Skip("set HECATE_POSTGRES_TEST_URL to run Postgres agent-preset conformance")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	client, err := storage.NewPostgresClient(ctx, storage.PostgresConfig{
		DatabaseURL: databaseURL,
		TablePrefix: fmt.Sprintf("profile_test_%d_%d", time.Now().UnixNano(), postgresProfileTestSequence.Add(1)),
	})
	if err != nil {
		t.Fatalf("NewPostgresClient: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := client.DB().ExecContext(ctx, "DROP TABLE IF EXISTS "+client.QualifiedTable("agent_profiles")); err != nil {
			t.Errorf("clean up Postgres agent-preset table: %v", err)
		}
		_ = client.Close()
	})
	return client
}

func newPostgresProfileTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	client := newPostgresProfileTestClient(t)
	store, err := NewPostgresStore(t.Context(), client)
	if err != nil {
		t.Fatalf("NewPostgresStore: %v", err)
	}
	return store
}

func TestPostgresStore_MigratesBrowserTextEntryDefaultDeny(t *testing.T) {
	client := newPostgresProfileTestClient(t)
	ctx := t.Context()
	// Reproduce the immediately preceding schema, where click/wait authority
	// already existed but must not silently become text-entry authority.
	_, err := client.DB().ExecContext(ctx, `
CREATE TABLE `+client.QualifiedTable("agent_profiles")+` (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    instructions TEXT NOT NULL DEFAULT '',
    surface TEXT NOT NULL DEFAULT 'any',
    provider_hint TEXT NOT NULL DEFAULT '',
    model_hint TEXT NOT NULL DEFAULT '',
    execution_profile TEXT NOT NULL DEFAULT '',
    tools_enabled INTEGER NOT NULL DEFAULT 0,
    writes_allowed INTEGER NOT NULL DEFAULT 0,
    network_allowed INTEGER NOT NULL DEFAULT 0,
    browser_allowed INTEGER NOT NULL DEFAULT 0,
    browser_interactions_allowed INTEGER NOT NULL DEFAULT 0,
    browser_allowed_origins TEXT NOT NULL DEFAULT '[]',
    approval_policy TEXT NOT NULL DEFAULT 'inherit',
    project_memory_policy TEXT NOT NULL DEFAULT 'inherit',
    context_source_policy TEXT NOT NULL DEFAULT 'inherit',
    skill_ids TEXT NOT NULL DEFAULT '[]',
    external_agent_kind TEXT NOT NULL DEFAULT '',
    external_agent_options TEXT NOT NULL DEFAULT '{}',
    created_at `+storage.TimestampColumnDefaultZero(client)+`,
    updated_at `+storage.TimestampColumnDefaultZero(client)+`
)`)
	if err != nil {
		t.Fatalf("seed prior schema: %v", err)
	}
	_, err = client.DB().ExecContext(ctx, `INSERT INTO `+client.QualifiedTable("agent_profiles")+`
    (id, name, surface, tools_enabled, browser_interactions_allowed, browser_allowed_origins)
    VALUES (?, ?, ?, ?, ?, ?)`, "existing", "Existing browser preset", SurfaceHecateChat, 1, 1, `["https://app.example.test"]`)
	if err != nil {
		t.Fatalf("seed existing browser preset: %v", err)
	}
	store, err := NewPostgresStore(ctx, client)
	if err != nil {
		t.Fatalf("migrate prior schema: %v", err)
	}
	assertPolicy := func(wantTextEntry bool) {
		t.Helper()
		got, found, err := store.Get(ctx, "existing")
		if err != nil || !found || got.BrowserTextEntryAllowed != wantTextEntry || !got.BrowserInteractionsAllowed || !got.ToolsEnabled || len(got.BrowserAllowedOrigins) != 1 || got.BrowserAllowedOrigins[0] != "https://app.example.test" {
			t.Fatalf("stored policy = %+v found=%v err=%v, want text entry=%v with existing grants intact", got, found, err, wantTextEntry)
		}
	}
	assertPolicy(false)
	if _, err := store.Update(ctx, "existing", func(profile *Profile) { profile.BrowserTextEntryAllowed = true }); err != nil {
		t.Fatalf("grant after migration: %v", err)
	}
	// Reinitialization must be idempotent and preserve explicit new authority.
	store, err = NewPostgresStore(ctx, client)
	if err != nil {
		t.Fatalf("reinitialize migrated store: %v", err)
	}
	assertPolicy(true)
	if _, err := store.Update(ctx, "existing", func(profile *Profile) { profile.BrowserTextEntryAllowed = false }); err != nil {
		t.Fatalf("revoke after migration: %v", err)
	}
	assertPolicy(false)
}
