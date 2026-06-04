package projects

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/storage"
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
		ContextSources: []ContextSource{{
			ID:      "ctx_readme",
			Kind:    "doc",
			Title:   "README",
			Path:    "README.md",
			Enabled: true,
		}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("timestamps were not initialized: %+v", created)
	}
	if !created.LastOpenedAt.IsZero() {
		t.Fatalf("LastOpenedAt = %s, want unset until explicitly opened", created.LastOpenedAt)
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
	if len(got.ContextSources) != 1 || got.ContextSources[0].Path != "README.md" || got.ContextSources[0].Kind != "doc" {
		t.Fatalf("context sources = %+v, want persisted README doc source", got.ContextSources)
	}

	updated, err := store.Update(ctx, "proj_alpha", func(item *Project) {
		item.Name = "Renamed"
		item.Roots = []Root{{ID: "root_beta", Path: "/tmp/renamed", Active: true}}
		item.DefaultRootID = "root_beta"
		item.ContextSources = []ContextSource{{ID: "ctx_architecture", Path: "docs/contributor/architecture.md", Enabled: true}}
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "Renamed" || len(updated.Roots) != 1 || updated.Roots[0].ID != "root_beta" {
		t.Fatalf("updated = %+v, want renamed project with replaced root", updated)
	}
	if len(updated.ContextSources) != 1 || updated.ContextSources[0].ID != "ctx_architecture" {
		t.Fatalf("updated context sources = %+v, want replaced source", updated.ContextSources)
	}

	items, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 || items[0].ID != "proj_alpha" {
		t.Fatalf("items = %+v, want one project", items)
	}
	if len(items[0].ContextSources) != 1 || items[0].ContextSources[0].Path != "docs/contributor/architecture.md" {
		t.Fatalf("listed context sources = %+v, want replaced source", items[0].ContextSources)
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

func TestSQLiteStore_ListFallsBackToUpdatedAtWhenLastOpenedAtEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newSQLiteTestStore(t)
	base := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	if _, err := store.Create(ctx, Project{ID: "proj_old", Name: "Old", CreatedAt: base, UpdatedAt: base}); err != nil {
		t.Fatalf("Create old: %v", err)
	}
	if _, err := store.Create(ctx, Project{ID: "proj_new", Name: "New", CreatedAt: base, UpdatedAt: base.Add(time.Hour)}); err != nil {
		t.Fatalf("Create new: %v", err)
	}

	items, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 2 || items[0].ID != "proj_new" {
		t.Fatalf("items = %+v, want updated-at fallback ordering", items)
	}
	if !items[0].LastOpenedAt.IsZero() || !items[1].LastOpenedAt.IsZero() {
		t.Fatalf("last-opened timestamps = %s / %s, want unset", items[0].LastOpenedAt, items[1].LastOpenedAt)
	}
}

func TestSQLiteStore_SortsContextSourcesLikeMemory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newSQLiteTestStore(t)
	created, err := store.Create(ctx, Project{
		ID:   "proj_alpha",
		Name: "Alpha",
		ContextSources: []ContextSource{
			{ID: "ctx_enabled_z", Path: "zeta.md", Enabled: true},
			{ID: "ctx_disabled_a", Path: "alpha.md", Enabled: false},
			{ID: "ctx_enabled_a", Path: "alpha.md", Enabled: true},
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	assertContextSourceIDs(t, created.ContextSources, []string{"ctx_enabled_a", "ctx_enabled_z", "ctx_disabled_a"})

	got, ok, err := store.Get(ctx, "proj_alpha")
	if err != nil || !ok {
		t.Fatalf("Get ok=%v err=%v, want project", ok, err)
	}
	assertContextSourceIDs(t, got.ContextSources, []string{"ctx_enabled_a", "ctx_enabled_z", "ctx_disabled_a"})
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

func TestSQLiteStore_UpdateUpsertsRootsWithoutResettingExistingRootCreatedAt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newSQLiteTestStore(t)
	rootCreatedAt := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC)
	if _, err := store.Create(ctx, Project{
		ID:            "proj_alpha",
		Name:          "Alpha",
		DefaultRootID: "root_alpha",
		Roots: []Root{{
			ID:        "root_alpha",
			Path:      "/tmp/alpha",
			Active:    true,
			CreatedAt: rootCreatedAt,
			UpdatedAt: rootCreatedAt,
		}},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := store.Update(ctx, "proj_alpha", func(item *Project) {
		item.Roots = []Root{
			{ID: "root_alpha", Path: "/tmp/renamed", Active: true},
			{ID: "root_beta", Path: "/tmp/beta", Active: true},
		}
		item.DefaultRootID = "root_alpha"
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	rootAlpha := mustFindRoot(t, updated.Roots, "root_alpha")
	if rootAlpha.Path != "/tmp/renamed" {
		t.Fatalf("root_alpha path = %q, want /tmp/renamed", rootAlpha.Path)
	}
	if !rootAlpha.CreatedAt.Equal(rootCreatedAt) {
		t.Fatalf("root_alpha CreatedAt = %s, want %s", rootAlpha.CreatedAt, rootCreatedAt)
	}
	if !rootAlpha.UpdatedAt.After(rootCreatedAt) {
		t.Fatalf("root_alpha UpdatedAt = %s, want after %s", rootAlpha.UpdatedAt, rootCreatedAt)
	}

	updated, err = store.Update(ctx, "proj_alpha", func(item *Project) {
		item.Roots = []Root{{ID: "root_beta", Path: "/tmp/beta", Active: true}}
		item.DefaultRootID = "root_beta"
	})
	if err != nil {
		t.Fatalf("Update removing root_alpha: %v", err)
	}
	if len(updated.Roots) != 1 || updated.Roots[0].ID != "root_beta" {
		t.Fatalf("roots after removal = %+v, want only root_beta", updated.Roots)
	}
}

func TestSQLiteStore_UpdateUpsertsContextSourcesWithoutResettingExistingCreatedAt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newSQLiteTestStore(t)
	sourceCreatedAt := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC)
	if _, err := store.Create(ctx, Project{
		ID:   "proj_alpha",
		Name: "Alpha",
		ContextSources: []ContextSource{{
			ID:        "ctx_readme",
			Kind:      "doc",
			Title:     "README",
			Path:      "README.md",
			Enabled:   true,
			CreatedAt: sourceCreatedAt,
			UpdatedAt: sourceCreatedAt,
		}},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := store.Update(ctx, "proj_alpha", func(item *Project) {
		item.ContextSources = []ContextSource{
			{ID: "ctx_readme", Kind: "doc", Title: "README", Path: "docs/README.md", Enabled: true},
			{ID: "ctx_notes", Kind: "doc", Title: "Notes", Path: "notes.md", Enabled: false},
		}
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	readme := mustFindContextSource(t, updated.ContextSources, "ctx_readme")
	if readme.Path != "docs/README.md" {
		t.Fatalf("ctx_readme path = %q, want docs/README.md", readme.Path)
	}
	if !readme.CreatedAt.Equal(sourceCreatedAt) {
		t.Fatalf("ctx_readme CreatedAt = %s, want %s", readme.CreatedAt, sourceCreatedAt)
	}
	if !readme.UpdatedAt.After(sourceCreatedAt) {
		t.Fatalf("ctx_readme UpdatedAt = %s, want after %s", readme.UpdatedAt, sourceCreatedAt)
	}

	updated, err = store.Update(ctx, "proj_alpha", func(item *Project) {
		item.ContextSources = []ContextSource{{ID: "ctx_notes", Kind: "doc", Title: "Notes", Path: "notes.md", Enabled: false}}
	})
	if err != nil {
		t.Fatalf("Update removing ctx_readme: %v", err)
	}
	if len(updated.ContextSources) != 1 || updated.ContextSources[0].ID != "ctx_notes" {
		t.Fatalf("context sources after removal = %+v, want only ctx_notes", updated.ContextSources)
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

func TestSQLiteStore_UpdatePreservesUnchangedContextSourceUpdatedAt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newSQLiteTestStore(t)
	sourceCreatedAt := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC)
	sourceUpdatedAt := sourceCreatedAt.Add(time.Hour)
	if _, err := store.Create(ctx, Project{
		ID:   "proj_alpha",
		Name: "Alpha",
		ContextSources: []ContextSource{{
			ID:        "ctx_readme",
			Kind:      "doc",
			Title:     "README",
			Path:      "README.md",
			Enabled:   true,
			CreatedAt: sourceCreatedAt,
			UpdatedAt: sourceUpdatedAt,
		}},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := store.Update(ctx, "proj_alpha", func(item *Project) {
		item.ContextSources = []ContextSource{{ID: "ctx_readme", Title: "README", Path: "README.md", Enabled: true}}
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	readme := mustFindContextSource(t, updated.ContextSources, "ctx_readme")
	if !readme.UpdatedAt.Equal(sourceUpdatedAt) {
		t.Fatalf("ctx_readme UpdatedAt = %s, want unchanged %s", readme.UpdatedAt, sourceUpdatedAt)
	}
}

func mustFindRoot(t *testing.T, roots []Root, id string) Root {
	t.Helper()
	for _, root := range roots {
		if root.ID == id {
			return root
		}
	}
	t.Fatalf("root %q not found in %+v", id, roots)
	return Root{}
}

func mustFindContextSource(t *testing.T, sources []ContextSource, id string) ContextSource {
	t.Helper()
	for _, source := range sources {
		if source.ID == id {
			return source
		}
	}
	t.Fatalf("context source %q not found in %+v", id, sources)
	return ContextSource{}
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
