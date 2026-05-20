package projects

import (
	"context"
	"errors"
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

	project.Roots[0].Path = "/mutated"
	got, ok, err := store.Get(ctx, "proj_alpha")
	if err != nil || !ok {
		t.Fatalf("Get ok=%v err=%v, want project", ok, err)
	}
	if got.Roots[0].Path != "/tmp/hecate" {
		t.Fatalf("stored root path mutated to %q", got.Roots[0].Path)
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
