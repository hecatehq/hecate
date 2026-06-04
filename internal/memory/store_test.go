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

func TestMemoryStore_CandidateLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	created, err := store.CreateCandidate(ctx, Candidate{
		ID:                  "memcand_alpha",
		ProjectID:           " proj_alpha ",
		Title:               " Review finding ",
		Body:                " Keep generated content labelled. ",
		SuggestedKind:       "note",
		SuggestedTrustLabel: " generated_summary ",
		SuggestedSourceKind: " task_output ",
		SuggestedSourceID:   " run_1 ",
		SourceRefs: []CandidateSourceRef{
			{Kind: "task_run", ID: "run_1", Title: "Run 1"},
		},
	})
	if err != nil {
		t.Fatalf("CreateCandidate: %v", err)
	}
	if created.Status != CandidateStatusPending || created.ProjectID != "proj_alpha" {
		t.Fatalf("created candidate = %+v, want pending scoped candidate", created)
	}
	if created.Title != "Review finding" || created.Body != "Keep generated content labelled." {
		t.Fatalf("trimmed candidate = %+v, want trimmed title/body", created)
	}

	got, ok, err := store.GetCandidate(ctx, "proj_alpha", "memcand_alpha")
	if err != nil || !ok {
		t.Fatalf("GetCandidate ok=%v err=%v, want candidate", ok, err)
	}
	if len(got.SourceRefs) != 1 || got.SourceRefs[0].ID != "run_1" {
		t.Fatalf("source refs = %+v, want run_1", got.SourceRefs)
	}
	got.SourceRefs[0].ID = "mutated"
	again, ok, err := store.GetCandidate(ctx, "proj_alpha", "memcand_alpha")
	if err != nil || !ok {
		t.Fatalf("GetCandidate again ok=%v err=%v, want candidate", ok, err)
	}
	if again.SourceRefs[0].ID != "run_1" {
		t.Fatalf("stored source ref mutated through caller: %+v", again.SourceRefs)
	}

	updated, err := store.UpdateCandidate(ctx, "proj_alpha", "memcand_alpha", func(item *Candidate) {
		item.Status = CandidateStatusPromoted
		item.PromotedMemoryID = "mem_alpha"
	})
	if err != nil {
		t.Fatalf("UpdateCandidate: %v", err)
	}
	if updated.Status != CandidateStatusPromoted || updated.PromotedMemoryID != "mem_alpha" {
		t.Fatalf("updated candidate = %+v, want promoted marker", updated)
	}

	pending, err := store.ListCandidates(ctx, CandidateFilter{ProjectID: "proj_alpha", Status: CandidateStatusPending})
	if err != nil {
		t.Fatalf("ListCandidates pending: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending candidates = %+v, want none", pending)
	}
	all, err := store.ListCandidates(ctx, CandidateFilter{ProjectID: "proj_alpha"})
	if err != nil {
		t.Fatalf("ListCandidates all: %v", err)
	}
	if len(all) != 1 || all[0].ID != "memcand_alpha" {
		t.Fatalf("all candidates = %+v, want memcand_alpha", all)
	}

	deleted, err := store.DeleteCandidatesByProjectID(ctx, "proj_alpha")
	if err != nil {
		t.Fatalf("DeleteCandidatesByProjectID: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
}

func TestMemoryStore_CandidateValidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	if _, err := store.CreateCandidate(ctx, Candidate{ID: "memcand", ProjectID: "proj", Title: "T", Body: "B", Status: "unknown"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("CreateCandidate unsupported status error = %v, want ErrInvalid", err)
	}
	if _, err := store.CreateCandidate(ctx, Candidate{ID: "memcand_promoted", ProjectID: "proj", Title: "T", Body: "B", PromotedMemoryID: "mem"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("CreateCandidate promoted id without status error = %v, want ErrInvalid", err)
	}
}
