package projects

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hecate/agent-runtime/internal/storage"
)

func newSQLiteTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	client, err := storage.NewSQLiteClient(context.Background(), storage.SQLiteConfig{
		Path:        filepath.Join(t.TempDir(), "projects.db"),
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

func TestSQLiteStore_ProjectRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newSQLiteTestStore(t)
	toolsEnabled := true
	compactOutput := false
	created, err := store.Create(ctx, Project{
		ID:                       "proj_alpha",
		Name:                     "Alpha",
		Description:              "primary workspace",
		DefaultRootID:            "root_alpha",
		DefaultProvider:          "ollama",
		DefaultModel:             "llama3.1:8b",
		DefaultAgentProfile:      "profile_backend",
		DefaultToolsEnabled:      &toolsEnabled,
		DefaultWorkspaceMode:     "shared",
		DefaultSystemPrompt:      "Prefer small commits.",
		DefaultCompactToolOutput: &compactOutput,
		Roots: []Root{{
			ID:        "root_alpha",
			Path:      "/tmp/hecate",
			Kind:      "local",
			GitRemote: "git@example.com:hecate/hecate.git",
			GitBranch: "main",
			Active:    true,
		}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() || created.LastOpenedAt.IsZero() {
		t.Fatalf("timestamps were not initialized: %+v", created)
	}

	got, ok, err := store.Get(ctx, "proj_alpha")
	if err != nil || !ok {
		t.Fatalf("Get ok=%v err=%v, want project", ok, err)
	}
	if got.DefaultToolsEnabled == nil || !*got.DefaultToolsEnabled {
		t.Fatalf("DefaultToolsEnabled = %v, want true", got.DefaultToolsEnabled)
	}
	if got.DefaultCompactToolOutput == nil || *got.DefaultCompactToolOutput {
		t.Fatalf("DefaultCompactToolOutput = %v, want false", got.DefaultCompactToolOutput)
	}
	if len(got.Roots) != 1 || got.Roots[0].GitRemote == "" {
		t.Fatalf("roots = %+v, want persisted git metadata", got.Roots)
	}

	updated, err := store.Update(ctx, "proj_alpha", func(item *Project) {
		item.Name = "Renamed"
		item.Roots = []Root{{ID: "root_beta", Path: "/tmp/renamed", Active: true}}
		item.DefaultRootID = "root_beta"
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "Renamed" || len(updated.Roots) != 1 || updated.Roots[0].ID != "root_beta" {
		t.Fatalf("updated = %+v, want renamed project with replaced root", updated)
	}

	items, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 || items[0].ID != "proj_alpha" {
		t.Fatalf("items = %+v, want one project", items)
	}

	if err := store.Delete(ctx, "proj_alpha"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := store.Delete(ctx, "proj_alpha"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete missing error = %v, want ErrNotFound", err)
	}
}

func TestSQLiteStore_RejectsNilClient(t *testing.T) {
	t.Parallel()
	_, err := NewSQLiteStore(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil client")
	}
}

func TestSQLiteStore_UpdateRejectsProjectIDChange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newSQLiteTestStore(t)
	if _, err := store.Create(ctx, Project{ID: "proj_alpha", Name: "Alpha"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err := store.Update(ctx, "proj_alpha", func(item *Project) {
		item.ID = "proj_beta"
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Update id change error = %v, want ErrInvalid", err)
	}
	if _, ok, err := store.Get(ctx, "proj_beta"); err != nil || ok {
		t.Fatalf("Get rewritten project ok=%v err=%v, want not found", ok, err)
	}
	got, ok, err := store.Get(ctx, "proj_alpha")
	if err != nil || !ok {
		t.Fatalf("Get original project ok=%v err=%v, want project", ok, err)
	}
	if got.ID != "proj_alpha" {
		t.Fatalf("project ID = %q, want proj_alpha", got.ID)
	}
}

func TestSQLiteStore_UpdatePreservesCreatedAt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newSQLiteTestStore(t)
	createdAt := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC)
	if _, err := store.Create(ctx, Project{ID: "proj_alpha", Name: "Alpha", CreatedAt: createdAt}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := store.Update(ctx, "proj_alpha", func(item *Project) {
		item.CreatedAt = createdAt.Add(24 * time.Hour)
		item.Name = "Renamed"
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !updated.CreatedAt.Equal(createdAt) {
		t.Fatalf("CreatedAt = %s, want %s", updated.CreatedAt, createdAt)
	}
}

func TestSQLiteStore_RejectsInvalidProject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newSQLiteTestStore(t)

	_, err := store.Create(ctx, Project{
		ID:   "proj_invalid",
		Name: "Invalid",
		Roots: []Root{
			{ID: "root_dup", Path: "/tmp/one", Active: true},
			{ID: "root_dup", Path: "/tmp/two", Active: true},
		},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Create invalid project error = %v, want ErrInvalid", err)
	}
}

func TestSQLiteStore_AllowsSameRootIDAcrossProjects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newSQLiteTestStore(t)

	for _, project := range []Project{
		{
			ID:   "proj_alpha",
			Name: "Alpha",
			Roots: []Root{{
				ID:     "root_shared",
				Path:   "/tmp/alpha",
				Active: true,
			}},
		},
		{
			ID:   "proj_beta",
			Name: "Beta",
			Roots: []Root{{
				ID:     "root_shared",
				Path:   "/tmp/beta",
				Active: true,
			}},
		},
	} {
		if _, err := store.Create(ctx, project); err != nil {
			t.Fatalf("Create(%s): %v", project.ID, err)
		}
	}

	items, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %+v, want two projects sharing a root id", items)
	}
	for _, item := range items {
		if len(item.Roots) != 1 || item.Roots[0].ID != "root_shared" {
			t.Fatalf("project %s roots = %+v, want shared root id", item.ID, item.Roots)
		}
	}
}

func TestSQLiteStore_RejectsInvalidDefaultRootID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newSQLiteTestStore(t)

	_, err := store.Create(ctx, Project{
		ID:            "proj_alpha",
		Name:          "Alpha",
		DefaultRootID: "missing",
		Roots: []Root{{
			ID:     "root_alpha",
			Path:   "/tmp/alpha",
			Active: true,
		}},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Create invalid default root error = %v, want ErrInvalid", err)
	}
}
