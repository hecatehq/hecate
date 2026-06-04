package memory

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
		Path:        filepath.Join(t.TempDir(), "memory.db"),
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

func TestSQLiteStore_EntryRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newSQLiteTestStore(t)
	createdAt := time.Date(2026, 6, 4, 10, 0, 0, 123, time.UTC)
	created, err := store.Create(ctx, Entry{
		ID:         "mem_alpha",
		ProjectID:  "proj_alpha",
		Title:      "Architecture notes",
		Body:       "Use the storage-tier rule.",
		TrustLabel: "generated_summary",
		SourceKind: "handoff",
		SourceID:   "artifact_1",
		Enabled:    true,
		CreatedAt:  createdAt,
		UpdatedAt:  createdAt,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.CreatedAt.Location() != time.UTC {
		t.Fatalf("CreatedAt location = %v, want UTC", created.CreatedAt.Location())
	}

	got, ok, err := store.Get(ctx, "proj_alpha", "mem_alpha")
	if err != nil || !ok {
		t.Fatalf("Get ok=%v err=%v, want entry", ok, err)
	}
	if got.SourceKind != "handoff" || got.SourceID != "artifact_1" || got.TrustLabel != "generated_summary" {
		t.Fatalf("got provenance = %+v, want saved provenance", got)
	}

	updated, err := store.Update(ctx, "proj_alpha", "mem_alpha", func(item *Entry) {
		item.Title = "Updated notes"
		item.Body = "Keep memory explicit."
		item.Enabled = false
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Enabled || updated.Title != "Updated notes" {
		t.Fatalf("updated = %+v, want disabled updated title", updated)
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

func TestSQLiteStore_PersistsAcrossReopen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "memory.db")
	client, err := storage.NewSQLiteClient(ctx, storage.SQLiteConfig{
		Path:        dbPath,
		TablePrefix: "test",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient: %v", err)
	}
	store, err := NewSQLiteStore(ctx, client)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	if _, err := store.Create(ctx, Entry{ID: "mem_alpha", ProjectID: "proj_alpha", Title: "Alpha", Body: "Body", Enabled: true}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close first client: %v", err)
	}

	reopenedClient, err := storage.NewSQLiteClient(ctx, storage.SQLiteConfig{
		Path:        dbPath,
		TablePrefix: "test",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopenedClient.Close() })
	reopened, err := NewSQLiteStore(ctx, reopenedClient)
	if err != nil {
		t.Fatalf("NewSQLiteStore reopen: %v", err)
	}
	got, ok, err := reopened.Get(ctx, "proj_alpha", "mem_alpha")
	if err != nil || !ok {
		t.Fatalf("Get reopened ok=%v err=%v, want entry", ok, err)
	}
	if got.Body != "Body" || got.Scope != ScopeProject {
		t.Fatalf("reopened entry = %+v, want saved body and project scope", got)
	}
}

func TestSQLiteStore_ProjectScopingAndDeleteByProject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newSQLiteTestStore(t)
	_, _ = store.Create(ctx, Entry{ID: "mem_a", ProjectID: "proj_a", Title: "A", Body: "Alpha", Enabled: true})
	_, _ = store.Create(ctx, Entry{ID: "mem_b", ProjectID: "proj_b", Title: "B", Body: "Beta", Enabled: true})

	if _, ok, err := store.Get(ctx, "proj_b", "mem_a"); err != nil || ok {
		t.Fatalf("Get cross-project ok=%v err=%v, want not found", ok, err)
	}
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

func TestSQLiteStore_ListWithoutProjectReturnsAllProjects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newSQLiteTestStore(t)
	_, _ = store.Create(ctx, Entry{ID: "mem_a", ProjectID: "proj_a", Title: "A", Body: "Alpha", Enabled: true})
	_, _ = store.Create(ctx, Entry{ID: "mem_b", ProjectID: "proj_b", Title: "B", Body: "Beta", Enabled: true})
	_, _ = store.Create(ctx, Entry{ID: "mem_disabled", ProjectID: "proj_b", Title: "Disabled", Body: "Hidden", Enabled: false})

	active, err := store.List(ctx, Filter{})
	if err != nil {
		t.Fatalf("List active all projects: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("active all-project entries = %+v, want two enabled entries", active)
	}
	all, err := store.List(ctx, Filter{IncludeDisabled: true})
	if err != nil {
		t.Fatalf("List all projects: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("all-project entries = %+v, want three entries including disabled", all)
	}
}

func TestSQLiteStore_RejectsDuplicateID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newSQLiteTestStore(t)
	if _, err := store.Create(ctx, Entry{ID: "mem_alpha", ProjectID: "proj_alpha", Title: "Alpha", Body: "Body"}); err != nil {
		t.Fatalf("Create first entry: %v", err)
	}
	for _, projectID := range []string{"proj_alpha", "proj_beta"} {
		if _, err := store.Create(ctx, Entry{ID: "mem_alpha", ProjectID: projectID, Title: "Duplicate", Body: "Body"}); !errors.Is(err, ErrAlreadyExists) {
			t.Fatalf("Create duplicate in %s error = %v, want ErrAlreadyExists", projectID, err)
		}
	}
	got, ok, err := store.Get(ctx, "proj_alpha", "mem_alpha")
	if err != nil || !ok {
		t.Fatalf("Get original ok=%v err=%v, want entry", ok, err)
	}
	if got.Title != "Alpha" {
		t.Fatalf("duplicate create replaced original title = %q, want Alpha", got.Title)
	}
}

func TestSQLiteStore_ConstraintErrorDetection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newSQLiteTestStore(t)
	if _, err := store.db.ExecContext(ctx, `INSERT INTO `+store.entries+` (
		id, scope, project_id, title, body, trust_label, source_kind,
		source_id, enabled, created_at, updated_at
	) VALUES ('mem_alpha', 'project', 'proj_alpha', 'Alpha', 'Body',
		'operator_memory', 'operator', '', 1,
		'2026-06-04T10:00:00Z', '2026-06-04T10:00:00Z'
	)`); err != nil {
		t.Fatalf("insert first row: %v", err)
	}
	_, err := store.db.ExecContext(ctx, `INSERT INTO `+store.entries+` (
		id, scope, project_id, title, body, trust_label, source_kind,
		source_id, enabled, created_at, updated_at
	) VALUES ('mem_alpha', 'project', 'proj_beta', 'Duplicate', 'Body',
		'operator_memory', 'operator', '', 1,
		'2026-06-04T10:00:00Z', '2026-06-04T10:00:00Z'
	)`)
	if !isSQLiteConstraintError(err) {
		t.Fatalf("isSQLiteConstraintError(%v) = false, want true", err)
	}
}

func TestSQLiteStore_RejectsNilClient(t *testing.T) {
	t.Parallel()
	if _, err := NewSQLiteStore(context.Background(), nil); err == nil {
		t.Fatal("expected error for nil client")
	}
}

func TestSQLiteStore_CandidateRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newSQLiteTestStore(t)
	created, err := store.CreateCandidate(ctx, Candidate{
		ID:                  "memcand_alpha",
		ProjectID:           "proj_alpha",
		Title:               "Candidate",
		Body:                "Promote only after review.",
		SuggestedKind:       "note",
		SuggestedTrustLabel: TrustLabelGenerated,
		SuggestedSourceKind: "task_output",
		SuggestedSourceID:   "run_1",
		SourceRefs: []CandidateSourceRef{
			{Kind: "task_run", ID: "run_1", Title: "Run 1"},
			{Kind: "chat_message", ID: "msg_1"},
		},
	})
	if err != nil {
		t.Fatalf("CreateCandidate: %v", err)
	}
	if created.Status != CandidateStatusPending {
		t.Fatalf("created status = %q, want pending", created.Status)
	}

	got, ok, err := store.GetCandidate(ctx, "proj_alpha", "memcand_alpha")
	if err != nil || !ok {
		t.Fatalf("GetCandidate ok=%v err=%v, want candidate", ok, err)
	}
	if len(got.SourceRefs) != 2 || got.SourceRefs[0].ID != "run_1" || got.SourceRefs[1].ID != "msg_1" {
		t.Fatalf("source refs = %+v, want persisted refs", got.SourceRefs)
	}

	updated, err := store.UpdateCandidate(ctx, "proj_alpha", "memcand_alpha", func(item *Candidate) {
		item.Status = CandidateStatusRejected
		item.StatusReason = "Too speculative"
	})
	if err != nil {
		t.Fatalf("UpdateCandidate: %v", err)
	}
	if updated.Status != CandidateStatusRejected || updated.StatusReason != "Too speculative" {
		t.Fatalf("updated candidate = %+v, want rejected reason", updated)
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
	if len(all) != 1 || all[0].Status != CandidateStatusRejected {
		t.Fatalf("all candidates = %+v, want rejected candidate", all)
	}
}

func TestSQLiteStore_CandidatesPersistAcrossReopen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "memory.db")
	client, err := storage.NewSQLiteClient(ctx, storage.SQLiteConfig{
		Path:        dbPath,
		TablePrefix: "test",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient: %v", err)
	}
	store, err := NewSQLiteStore(ctx, client)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	if _, err := store.CreateCandidate(ctx, Candidate{ID: "memcand_alpha", ProjectID: "proj_alpha", Title: "Alpha", Body: "Body"}); err != nil {
		t.Fatalf("CreateCandidate: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close first client: %v", err)
	}

	reopenedClient, err := storage.NewSQLiteClient(ctx, storage.SQLiteConfig{
		Path:        dbPath,
		TablePrefix: "test",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopenedClient.Close() })
	reopened, err := NewSQLiteStore(ctx, reopenedClient)
	if err != nil {
		t.Fatalf("NewSQLiteStore reopen: %v", err)
	}
	got, ok, err := reopened.GetCandidate(ctx, "proj_alpha", "memcand_alpha")
	if err != nil || !ok {
		t.Fatalf("GetCandidate reopened ok=%v err=%v, want candidate", ok, err)
	}
	if got.Status != CandidateStatusPending || got.SuggestedTrustLabel != TrustLabelGenerated {
		t.Fatalf("reopened candidate = %+v, want pending generated defaults", got)
	}
}
