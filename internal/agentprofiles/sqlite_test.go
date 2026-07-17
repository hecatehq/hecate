package agentprofiles

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/storage"
)

func newSQLiteProfileTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	client, err := storage.NewSQLiteClient(context.Background(), storage.SQLiteConfig{
		Path:        filepath.Join(t.TempDir(), "profiles.db"),
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

func TestSQLiteStore_ProfileRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newSQLiteProfileTestStore(t)

	created, err := store.Create(ctx, Profile{
		ID:               "prof_reviewer",
		Name:             "Reviewer",
		Description:      "Production-risk review",
		Instructions:     "Call out risk first.",
		Surface:          SurfaceHecateTask,
		ProviderHint:     "openai",
		ModelHint:        "gpt-4.1",
		ExecutionProfile: "review",
		ToolsEnabled:     true,
		BrowserAllowed:   true,
		BrowserAllowedOrigins: []string{
			"https://app.example.test/",
		},
		ApprovalPolicy:      ApprovalRequire,
		ProjectMemoryPolicy: MemoryVisibleOnly,
		ContextSourcePolicy: ContextIncludeEnabled,
		SkillIDs:            []string{"review", "security-audit"},
		ExternalAgentKind:   "claude",
		ExternalAgentOptions: map[string]string{
			"permission_mode": "plan",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("timestamps were not initialized: %+v", created)
	}

	got, ok, err := store.Get(ctx, "prof_reviewer")
	if err != nil || !ok {
		t.Fatalf("Get ok=%v err=%v, want profile", ok, err)
	}
	if got.Name != "Reviewer" || got.ExternalAgentOptions["permission_mode"] != "plan" {
		t.Fatalf("profile = %+v, want persisted profile", got)
	}
	if !got.BrowserAllowed || len(got.BrowserAllowedOrigins) != 1 || got.BrowserAllowedOrigins[0] != "https://app.example.test" {
		t.Fatalf("browser profile = %+v, want persisted normalized browser posture", got)
	}

	updated, err := store.Update(ctx, "prof_reviewer", func(profile *Profile) {
		profile.Name = "Reviewer V2"
		profile.NetworkAllowed = true
		profile.BrowserAllowedOrigins = []string{"https://console.example.test"}
		profile.ExternalAgentOptions = map[string]string{"permission_mode": "accept_edits"}
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "Reviewer V2" || !updated.NetworkAllowed || updated.ExternalAgentOptions["permission_mode"] != "accept_edits" || len(updated.BrowserAllowedOrigins) != 1 || updated.BrowserAllowedOrigins[0] != "https://console.example.test" {
		t.Fatalf("updated = %+v, want patched profile", updated)
	}

	items, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !profileIDExists(items, "review_qa") || !profileIDExists(items, "prof_reviewer") {
		t.Fatalf("items = %+v, want built-ins plus persisted profile", items)
	}
}

func TestSQLiteStore_BuiltInProfiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newSQLiteProfileTestStore(t)

	profile, ok, err := store.Get(ctx, "safe_external_review")
	if err != nil || !ok {
		t.Fatalf("Get built-in ok=%v err=%v, want safe external review profile", ok, err)
	}
	if !profile.BuiltIn || profile.Surface != SurfaceExternalAgent || profile.WritesAllowed {
		t.Fatalf("built-in profile = %+v, want safe external review posture", profile)
	}
	if _, err := store.Create(ctx, Profile{ID: "safe_external_review", Name: "Override"}); !errors.Is(err, ErrBuiltIn) {
		t.Fatalf("Create built-in error = %v, want ErrBuiltIn", err)
	}
	if _, err := store.Update(ctx, "safe_external_review", func(profile *Profile) {
		profile.Name = "Override"
	}); !errors.Is(err, ErrBuiltIn) {
		t.Fatalf("Update built-in error = %v, want ErrBuiltIn", err)
	}
	if err := store.Delete(ctx, "safe_external_review"); !errors.Is(err, ErrBuiltIn) {
		t.Fatalf("Delete built-in error = %v, want ErrBuiltIn", err)
	}
	legacy := normalizeProfile(Profile{
		ID:   "safe_external_review",
		Name: "Legacy Collision",
	}, time.Now().UTC())
	if err := store.upsert(ctx, legacy); err != nil {
		t.Fatalf("seed legacy built-in collision: %v", err)
	}
	items, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List with legacy built-in collision: %v", err)
	}
	if countProfileID(items, "safe_external_review") != 1 {
		t.Fatalf("items = %+v, want exactly one safe_external_review built-in", items)
	}
	profile, ok, err = store.Get(ctx, "safe_external_review")
	if err != nil || !ok {
		t.Fatalf("Get colliding built-in ok=%v err=%v, want safe external review profile", ok, err)
	}
	if !profile.BuiltIn || profile.Name == "Legacy Collision" {
		t.Fatalf("colliding profile = %+v, want built-in to shadow stored row", profile)
	}
}

func TestSQLiteStore_MigratesBrowserEvidenceColumns(t *testing.T) {
	ctx := context.Background()
	client, err := storage.NewSQLiteClient(ctx, storage.SQLiteConfig{
		Path:        filepath.Join(t.TempDir(), "profiles.db"),
		TablePrefix: "test",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	_, err = client.DB().ExecContext(ctx, `
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
    approval_policy TEXT NOT NULL DEFAULT 'inherit',
    project_memory_policy TEXT NOT NULL DEFAULT 'inherit',
    context_source_policy TEXT NOT NULL DEFAULT 'inherit',
    skill_ids TEXT NOT NULL DEFAULT '[]',
    external_agent_kind TEXT NOT NULL DEFAULT '',
    external_agent_options TEXT NOT NULL DEFAULT '{}',
    created_at TIMESTAMP NOT NULL DEFAULT '',
    updated_at TIMESTAMP NOT NULL DEFAULT ''
)`)
	if err != nil {
		t.Fatalf("seed legacy agent_profiles schema: %v", err)
	}

	store, err := NewSQLiteStore(ctx, client)
	if err != nil {
		t.Fatalf("NewSQLiteStore migration: %v", err)
	}
	created, err := store.Create(ctx, Profile{
		ID:                    "prof_browser_migration",
		Name:                  "Browser migration",
		Surface:               SurfaceHecateTask,
		ToolsEnabled:          true,
		BrowserAllowed:        true,
		BrowserAllowedOrigins: []string{"https://app.example.test"},
	})
	if err != nil {
		t.Fatalf("Create after migration: %v", err)
	}
	if !created.BrowserAllowed || len(created.BrowserAllowedOrigins) != 1 {
		t.Fatalf("created browser posture = %+v", created)
	}
}

func TestSQLiteStoreUpdate_SerializesBrowserGrantAndPrerequisiteRevocation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	store := newSQLiteProfileTestStore(t)
	const profileID = "prof_browser_concurrency"
	if _, err := store.Create(ctx, Profile{
		ID:                    profileID,
		Name:                  "Browser concurrency",
		Surface:               SurfaceHecateTask,
		ToolsEnabled:          true,
		BrowserAllowed:        true,
		BrowserAllowedOrigins: []string{"https://before.example.test"},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	grantRead := make(chan struct{})
	releaseGrant := make(chan struct{})
	grantDone := make(chan error, 1)
	go func() {
		_, err := store.Update(ctx, profileID, func(profile *Profile) {
			close(grantRead)
			<-releaseGrant
			profile.BrowserAllowedOrigins = []string{"https://after.example.test"}
		})
		grantDone <- err
	}()
	select {
	case <-grantRead:
	case <-ctx.Done():
		t.Fatalf("browser-grant update did not read profile: %v", ctx.Err())
	}

	revocationRead := make(chan struct{})
	revocationDone := make(chan error, 1)
	go func() {
		_, err := store.Update(ctx, profileID, func(profile *Profile) {
			close(revocationRead)
			profile.ToolsEnabled = false
			profile.BrowserAllowed = false
			profile.BrowserAllowedOrigins = nil
		})
		revocationDone <- err
	}()

	// The first callback is deliberately parked after its read. SQLite's
	// BEGIN IMMEDIATE and PostgreSQL's row lock must keep the revocation from
	// observing or writing concurrently; otherwise the stale grant can win
	// after the revocation commits.
	readWhileGrantHeld := false
	select {
	case <-revocationRead:
		readWhileGrantHeld = true
	case <-time.After(150 * time.Millisecond):
	}
	close(releaseGrant)

	select {
	case err := <-grantDone:
		if err != nil {
			t.Fatalf("browser-grant Update: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("browser-grant Update did not finish: %v", ctx.Err())
	}
	select {
	case err := <-revocationDone:
		if err != nil {
			t.Fatalf("revocation Update: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("revocation Update did not finish: %v", ctx.Err())
	}
	if readWhileGrantHeld {
		t.Fatal("revocation read profile while the browser-grant update held its transaction")
	}

	profile, ok, err := store.Get(ctx, profileID)
	if err != nil || !ok {
		t.Fatalf("Get after concurrent updates ok=%v err=%v", ok, err)
	}
	if profile.ToolsEnabled || profile.BrowserAllowed || len(profile.BrowserAllowedOrigins) != 0 {
		t.Fatalf("profile restored browser access after revocation: %+v", profile)
	}
}
