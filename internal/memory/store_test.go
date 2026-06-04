package memory

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryStore_EntryLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	created, err := store.Create(ctx, Entry{
		ID:        "mem_alpha",
		ProjectID: " proj_alpha ",
		Title:     " Commit style ",
		Body:      " Use conventional commits. ",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Scope != ScopeProject || created.ProjectID != "proj_alpha" {
		t.Fatalf("scope/project = %q/%q, want project/proj_alpha", created.Scope, created.ProjectID)
	}
	if created.Title != "Commit style" || created.Body != "Use conventional commits." {
		t.Fatalf("trimmed entry = %+v, want trimmed title/body", created)
	}
	if created.TrustLabel != TrustLabelOperatorMemory || created.SourceKind != SourceKindOperator {
		t.Fatalf("trust/source = %q/%q, want operator defaults", created.TrustLabel, created.SourceKind)
	}

	got, ok, err := store.Get(ctx, "proj_alpha", "mem_alpha")
	if err != nil || !ok {
		t.Fatalf("Get ok=%v err=%v, want entry", ok, err)
	}
	if got.Title != "Commit style" {
		t.Fatalf("Get title = %q, want Commit style", got.Title)
	}
	if _, ok, err := store.Get(ctx, "proj_other", "mem_alpha"); err != nil || ok {
		t.Fatalf("Get cross-project ok=%v err=%v, want not found", ok, err)
	}

	updated, err := store.Update(ctx, "proj_alpha", "mem_alpha", func(item *Entry) {
		item.Body = "Prefer small, reviewable changes."
		item.Enabled = false
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Enabled || updated.Body != "Prefer small, reviewable changes." {
		t.Fatalf("updated = %+v, want disabled changed body", updated)
	}

	active, err := store.List(ctx, Filter{ProjectID: "proj_alpha"})
	if err != nil {
		t.Fatalf("List active: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("active entries = %+v, want none", active)
	}
	all, err := store.List(ctx, Filter{ProjectID: "proj_alpha", IncludeDisabled: true})
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if len(all) != 1 || all[0].ID != "mem_alpha" {
		t.Fatalf("all entries = %+v, want mem_alpha", all)
	}

	if err := store.Delete(ctx, "proj_alpha", "mem_alpha"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := store.Delete(ctx, "proj_alpha", "mem_alpha"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete missing error = %v, want ErrNotFound", err)
	}
}

func TestMemoryStore_ProjectScopingAndDeleteByProject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	_, _ = store.Create(ctx, Entry{ID: "mem_a", ProjectID: "proj_a", Title: "A", Body: "Alpha", Enabled: true})
	_, _ = store.Create(ctx, Entry{ID: "mem_b", ProjectID: "proj_b", Title: "B", Body: "Beta", Enabled: true})

	deleted, err := store.DeleteByProjectID(ctx, "proj_a")
	if err != nil {
		t.Fatalf("DeleteByProjectID: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	items, err := store.List(ctx, Filter{ProjectID: "proj_b"})
	if err != nil {
		t.Fatalf("List proj_b: %v", err)
	}
	if len(items) != 1 || items[0].ID != "mem_b" {
		t.Fatalf("proj_b entries = %+v, want mem_b", items)
	}
}

func TestMemoryStore_ListOrdersEnabledNewestFirst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	base := time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC)
	_, _ = store.Create(ctx, Entry{ID: "mem_old", ProjectID: "proj", Title: "Old", Body: "Old", Enabled: true, CreatedAt: base, UpdatedAt: base})
	_, _ = store.Create(ctx, Entry{ID: "mem_new", ProjectID: "proj", Title: "New", Body: "New", Enabled: true, CreatedAt: base, UpdatedAt: base.Add(time.Hour)})
	_, _ = store.Create(ctx, Entry{ID: "mem_disabled", ProjectID: "proj", Title: "Disabled", Body: "Disabled", Enabled: false, CreatedAt: base, UpdatedAt: base.Add(2 * time.Hour)})

	items, err := store.List(ctx, Filter{ProjectID: "proj", IncludeDisabled: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 3 || items[0].ID != "mem_new" || items[1].ID != "mem_old" || items[2].ID != "mem_disabled" {
		t.Fatalf("items = %+v, want enabled newest first then disabled", items)
	}
}

func TestMemoryStore_Validation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	tests := []struct {
		name  string
		entry Entry
	}{
		{name: "missing id", entry: Entry{ProjectID: "proj", Title: "Title", Body: "Body"}},
		{name: "missing project", entry: Entry{ID: "mem", Title: "Title", Body: "Body"}},
		{name: "missing title", entry: Entry{ID: "mem", ProjectID: "proj", Body: "Body"}},
		{name: "missing body", entry: Entry{ID: "mem", ProjectID: "proj", Title: "Title"}},
		{name: "global scope", entry: Entry{ID: "mem", Scope: "global", ProjectID: "proj", Title: "Title", Body: "Body"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := store.Create(ctx, test.entry); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Create error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestMemoryStore_RejectsDuplicateID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	if _, err := store.Create(ctx, Entry{ID: "mem_alpha", ProjectID: "proj_alpha", Title: "Alpha", Body: "Body"}); err != nil {
		t.Fatalf("Create first entry: %v", err)
	}
	if _, err := store.Create(ctx, Entry{ID: "mem_alpha", ProjectID: "proj_alpha", Title: "Duplicate", Body: "Body"}); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("Create duplicate error = %v, want ErrAlreadyExists", err)
	}
	got, ok, err := store.Get(ctx, "proj_alpha", "mem_alpha")
	if err != nil || !ok {
		t.Fatalf("Get original ok=%v err=%v, want entry", ok, err)
	}
	if got.Title != "Alpha" {
		t.Fatalf("duplicate create replaced original title = %q, want Alpha", got.Title)
	}
}

func TestMemoryStore_UpdateRejectsImmutableFields(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	if _, err := store.Create(ctx, Entry{ID: "mem_alpha", ProjectID: "proj_alpha", Title: "Alpha", Body: "Body"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	tests := []struct {
		name   string
		update func(*Entry)
	}{
		{name: "id", update: func(item *Entry) { item.ID = "mem_beta" }},
		{name: "project", update: func(item *Entry) { item.ProjectID = "proj_beta" }},
		{name: "scope", update: func(item *Entry) { item.Scope = "global" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := store.Update(ctx, "proj_alpha", "mem_alpha", test.update)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Update error = %v, want ErrInvalid", err)
			}
		})
	}
}
