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
		if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() || got.CreatedAt.Year() < 1 || got.UpdatedAt.Year() < 1 || got.CreatedAt.Location() != time.UTC || got.UpdatedAt.Location() != time.UTC {
			t.Fatalf("legacy timestamps were not normalized to valid UTC values: created=%v updated=%v", got.CreatedAt, got.UpdatedAt)
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

func TestPostgresStore_ProfileTimestampRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value time.Time
	}{
		{"current", time.Date(2026, 11, 5, 12, 0, 0, 123456000, time.UTC)},
		{"historical", time.Date(1, 1, 1, 12, 0, 0, 123456000, time.UTC)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newPostgresProfileTestStore(t)
			ctx := t.Context()
			value := tc.value.In(time.FixedZone("second offset", -884))
			created, err := store.Create(ctx, Profile{ID: "timestamp", Name: "Timestamp", CreatedAt: value, UpdatedAt: value})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			assertTime := func(label string, got time.Time) {
				t.Helper()
				if !got.Equal(tc.value) || got.Location() != time.UTC {
					t.Fatalf("%s = %v, want UTC %v", label, got, tc.value)
				}
			}
			got, found, err := store.Get(ctx, created.ID)
			if err != nil || !found {
				t.Fatalf("Get: found=%v err=%v", found, err)
			}
			assertTime("Get CreatedAt", got.CreatedAt)
			assertTime("Get UpdatedAt", got.UpdatedAt)
			items, err := store.List(ctx)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			listed := false
			for _, item := range items {
				if item.ID == created.ID {
					listed = true
					assertTime("List CreatedAt", item.CreatedAt)
					assertTime("List UpdatedAt", item.UpdatedAt)
				}
			}
			if !listed {
				t.Fatal("List omitted the timestamp profile")
			}
			updated, err := store.Update(ctx, created.ID, func(profile *Profile) { profile.Name = "Updated timestamp" })
			if err != nil {
				t.Fatalf("Update: %v", err)
			}
			assertTime("Update CreatedAt", updated.CreatedAt)
			got, found, err = store.Get(ctx, created.ID)
			if err != nil || !found {
				t.Fatalf("Get after Update: found=%v err=%v", found, err)
			}
			assertTime("persisted CreatedAt", got.CreatedAt)
			if !got.UpdatedAt.Equal(updated.UpdatedAt.Truncate(time.Microsecond)) || got.UpdatedAt.Location() != time.UTC {
				t.Fatalf("persisted UpdatedAt = %v, want UTC %v at Postgres precision", got.UpdatedAt, updated.UpdatedAt)
			}
		})
	}
}

func TestPostgresStore_UpsertZeroTimestamps(t *testing.T) {
	store := newPostgresProfileTestStore(t)
	ctx := t.Context()
	zero := time.Time{}.In(time.FixedZone("historical Madrid", -884))
	if err := store.upsert(ctx, Profile{ID: "zero", Name: "Zero", CreatedAt: zero, UpdatedAt: zero}); err != nil {
		t.Fatalf("upsert zero timestamps: %v", err)
	}
	var createdAt, updatedAt time.Time
	if err := store.client.DB().QueryRowContext(ctx, "SELECT created_at, updated_at FROM "+store.table+" WHERE id = ?", "zero").Scan(&createdAt, &updatedAt); err != nil {
		t.Fatalf("read stored zero timestamps: %v", err)
	}
	if !createdAt.IsZero() || !updatedAt.IsZero() {
		t.Fatalf("stored timestamps = %v, %v; want zero instants", createdAt, updatedAt)
	}
}
