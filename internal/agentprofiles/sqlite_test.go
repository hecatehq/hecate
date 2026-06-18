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
		ID:                  "prof_reviewer",
		Name:                "Reviewer",
		Description:         "Production-risk review",
		Instructions:        "Call out risk first.",
		Surface:             SurfaceHecateTask,
		ProviderHint:        "openai",
		ModelHint:           "gpt-4.1",
		ExecutionProfile:    "review",
		ToolsEnabled:        true,
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

	updated, err := store.Update(ctx, "prof_reviewer", func(profile *Profile) {
		profile.Name = "Reviewer V2"
		profile.NetworkAllowed = true
		profile.ExternalAgentOptions = map[string]string{"permission_mode": "accept_edits"}
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "Reviewer V2" || !updated.NetworkAllowed || updated.ExternalAgentOptions["permission_mode"] != "accept_edits" {
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
