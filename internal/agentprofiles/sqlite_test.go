package agentprofiles

import (
	"context"
	"path/filepath"
	"testing"

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
	if len(items) != 1 || items[0].ID != "prof_reviewer" {
		t.Fatalf("items = %+v, want one profile", items)
	}
}
