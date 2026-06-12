package projects

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestMemoryStore_ProjectLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	toolsEnabled := true
	project, err := store.Create(ctx, Project{
		ID:                  "proj_alpha",
		Name:                " Alpha ",
		DefaultToolsEnabled: &toolsEnabled,
		Roots: []Root{{
			ID:     "root_alpha",
			Path:   " /tmp/hecate ",
			Active: true,
		}},
		ContextSources: []ContextSource{{
			ID:      "ctx_readme",
			Path:    " README.md ",
			Enabled: true,
		}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if project.Name != "Alpha" {
		t.Fatalf("name = %q, want Alpha", project.Name)
	}
	if project.Roots[0].Path != "/tmp/hecate" || project.Roots[0].Kind != "local" {
		t.Fatalf("root = %+v, want normalized local root", project.Roots[0])
	}
	if project.ContextSources[0].Path != "README.md" || project.ContextSources[0].Kind != "doc" {
		t.Fatalf("context source = %+v, want normalized doc source", project.ContextSources[0])
	}

	project.Roots[0].Path = "/mutated"
	project.ContextSources[0].Path = "mutated.md"
	got, ok, err := store.Get(ctx, "proj_alpha")
	if err != nil || !ok {
		t.Fatalf("Get ok=%v err=%v, want project", ok, err)
	}
	if got.Roots[0].Path != "/tmp/hecate" {
		t.Fatalf("stored root path mutated to %q", got.Roots[0].Path)
	}
	if got.ContextSources[0].Path != "README.md" {
		t.Fatalf("stored context source path mutated to %q", got.ContextSources[0].Path)
	}

	updated, err := store.Update(ctx, "proj_alpha", func(item *Project) {
		item.Name = "Beta"
		item.Roots = append(item.Roots, Root{ID: "root_beta", Path: "/tmp/beta", Active: true})
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "Beta" || len(updated.Roots) != 2 {
		t.Fatalf("updated project = %+v, want renamed project with two roots", updated)
	}

	if err := store.Delete(ctx, "proj_alpha"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, ok, err = store.Get(ctx, "proj_alpha")
	if err != nil || ok {
		t.Fatalf("Get after delete ok=%v err=%v, want not found", ok, err)
	}
}

func TestMemoryStore_ListSortsByLastOpenedAt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	base := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	_, _ = store.Create(ctx, Project{ID: "proj_old", Name: "Old", CreatedAt: base, UpdatedAt: base, LastOpenedAt: base})
	_, _ = store.Create(ctx, Project{ID: "proj_new", Name: "New", CreatedAt: base, UpdatedAt: base.Add(time.Hour), LastOpenedAt: base.Add(time.Hour)})

	items, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 2 || items[0].ID != "proj_new" {
		t.Fatalf("items = %+v, want newest project first", items)
	}
}

func TestMemoryStore_ListFallsBackToUpdatedAtWhenLastOpenedAtEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	base := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	_, _ = store.Create(ctx, Project{ID: "proj_old", Name: "Old", CreatedAt: base, UpdatedAt: base})
	_, _ = store.Create(ctx, Project{ID: "proj_new", Name: "New", CreatedAt: base, UpdatedAt: base.Add(time.Hour)})

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

func TestMemoryStore_SortsContextSourcesLikeSQLite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
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

func TestMemoryStore_UpdateMissingProject(t *testing.T) {
	t.Parallel()
	_, err := NewMemoryStore().Update(context.Background(), "missing", nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update missing error = %v, want ErrNotFound", err)
	}
}

func TestMemoryStore_UpdateRejectsProjectIDChange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	if _, err := store.Create(ctx, Project{ID: "proj_alpha", Name: "Alpha"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err := store.Update(ctx, "proj_alpha", func(item *Project) {
		item.ID = "proj_beta"
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Update id change error = %v, want ErrInvalid", err)
	}
	got, ok, err := store.Get(ctx, "proj_alpha")
	if err != nil || !ok {
		t.Fatalf("Get original project ok=%v err=%v, want project", ok, err)
	}
	if got.ID != "proj_alpha" {
		t.Fatalf("project ID = %q, want proj_alpha", got.ID)
	}
}

func TestMemoryStore_RejectsDuplicateProjectIdentity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	root := t.TempDir()
	if _, err := store.Create(ctx, Project{
		ID:   "proj_alpha",
		Name: "Alpha",
		Roots: []Root{{
			ID:     "root_alpha",
			Path:   root,
			Active: true,
		}},
	}); err != nil {
		t.Fatalf("Create alpha: %v", err)
	}

	cases := []struct {
		name    string
		project Project
	}{
		{
			name:    "same id",
			project: Project{ID: "proj_alpha", Name: "Beta"},
		},
		{
			name:    "same name",
			project: Project{ID: "proj_beta", Name: " alpha "},
		},
		{
			name: "same root path",
			project: Project{
				ID:   "proj_beta",
				Name: "Beta",
				Roots: []Root{{
					ID:     "root_beta",
					Path:   root + string(filepath.Separator),
					Active: true,
				}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.Create(ctx, tc.project)
			if !errors.Is(err, ErrAlreadyExists) {
				t.Fatalf("Create duplicate error = %v, want ErrAlreadyExists", err)
			}
		})
	}

	if _, err := store.Create(ctx, Project{ID: "proj_gamma", Name: "Gamma"}); err != nil {
		t.Fatalf("Create gamma: %v", err)
	}
	_, err := store.Update(ctx, "proj_gamma", func(item *Project) {
		item.Name = "ALPHA"
	})
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("Update duplicate name error = %v, want ErrAlreadyExists", err)
	}
	_, err = store.Update(ctx, "proj_gamma", func(item *Project) {
		item.Roots = []Root{{ID: "root_gamma", Path: root + string(filepath.Separator), Active: true}}
	})
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("Update duplicate root path error = %v, want ErrAlreadyExists", err)
	}
}

func TestMemoryStore_UpdatePreservesCreatedAt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
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

func TestMemoryStore_UpdatePreservesExistingRootCreatedAt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	rootCreatedAt := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC)
	if _, err := store.Create(ctx, Project{
		ID:   "proj_alpha",
		Name: "Alpha",
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
		item.Roots = []Root{{ID: "root_alpha", Path: "/tmp/renamed", Active: true}}
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(updated.Roots) != 1 {
		t.Fatalf("roots = %+v, want one root", updated.Roots)
	}
	if !updated.Roots[0].CreatedAt.Equal(rootCreatedAt) {
		t.Fatalf("root CreatedAt = %s, want %s", updated.Roots[0].CreatedAt, rootCreatedAt)
	}
	if !updated.Roots[0].UpdatedAt.After(rootCreatedAt) {
		t.Fatalf("root UpdatedAt = %s, want after %s", updated.Roots[0].UpdatedAt, rootCreatedAt)
	}
}

func TestMemoryStore_UpdatePreservesExistingContextSourceCreatedAt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
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
		item.ContextSources = []ContextSource{{ID: "ctx_readme", Path: "docs/README.md", Enabled: true}}
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(updated.ContextSources) != 1 {
		t.Fatalf("context sources = %+v, want one source", updated.ContextSources)
	}
	if !updated.ContextSources[0].CreatedAt.Equal(sourceCreatedAt) {
		t.Fatalf("source CreatedAt = %s, want %s", updated.ContextSources[0].CreatedAt, sourceCreatedAt)
	}
	if !updated.ContextSources[0].UpdatedAt.After(sourceCreatedAt) {
		t.Fatalf("source UpdatedAt = %s, want after %s", updated.ContextSources[0].UpdatedAt, sourceCreatedAt)
	}
}

func TestMemoryStore_UpdatePreservesUnchangedContextSourceUpdatedAt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
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
	if len(updated.ContextSources) != 1 {
		t.Fatalf("context sources = %+v, want one source", updated.ContextSources)
	}
	if !updated.ContextSources[0].UpdatedAt.Equal(sourceUpdatedAt) {
		t.Fatalf("source UpdatedAt = %s, want unchanged %s", updated.ContextSources[0].UpdatedAt, sourceUpdatedAt)
	}
}

func TestMemoryStore_RejectsInvalidProject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()

	_, err := store.Create(ctx, Project{
		ID:   "proj_alpha",
		Name: "Alpha",
		Roots: []Root{
			{ID: "root_dup", Path: "/tmp/one", Active: true},
			{ID: "root_dup", Path: "/tmp/two", Active: true},
		},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Create duplicate root error = %v, want ErrInvalid", err)
	}

	_, err = store.Create(ctx, Project{
		ID:   "proj_beta",
		Name: "Beta",
		ContextSources: []ContextSource{
			{ID: "ctx_dup", Path: "README.md", Enabled: true},
			{ID: "ctx_dup", Path: "docs/README.md", Enabled: true},
		},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Create duplicate context source error = %v, want ErrInvalid", err)
	}

	root := t.TempDir()
	_, err = store.Create(ctx, Project{
		ID:   "proj_gamma",
		Name: "Gamma",
		Roots: []Root{
			{ID: "root_one", Path: root, Active: true},
			{ID: "root_two", Path: root + string(filepath.Separator), Active: true},
		},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Create duplicate root path error = %v, want ErrInvalid", err)
	}
}

func TestMemoryStore_RejectsInvalidDefaultRootID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()

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

func assertContextSourceIDs(t *testing.T, sources []ContextSource, want []string) {
	t.Helper()
	if len(sources) != len(want) {
		t.Fatalf("context sources = %+v, want ids %v", sources, want)
	}
	for idx, source := range sources {
		if source.ID != want[idx] {
			t.Fatalf("context source ids = %v, want %v", contextSourceIDs(sources), want)
		}
	}
}

func contextSourceIDs(sources []ContextSource) []string {
	ids := make([]string, 0, len(sources))
	for _, source := range sources {
		ids = append(ids, source.ID)
	}
	return ids
}
