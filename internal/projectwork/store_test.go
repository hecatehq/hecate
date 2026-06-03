package projectwork

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/storage"
)

func TestStoreConformance_ProjectWorkLifecycle(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		new  func(*testing.T) Store
	}{
		{name: "memory", new: func(t *testing.T) Store { return NewMemoryStore() }},
		{name: "sqlite", new: func(t *testing.T) Store { return newSQLiteTestStore(t) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			store := tc.new(t)

			roles, err := store.ListRoles(ctx, "proj_alpha")
			if err != nil {
				t.Fatalf("ListRoles built-ins: %v", err)
			}
			if len(roles) < 8 || !roleIDExists(roles, "product_manager") || !roleIDExists(roles, "reviewer_qa") {
				t.Fatalf("built-in roles = %+v, want default project team roles", roles)
			}

			custom, err := store.CreateRole(ctx, AgentRoleProfile{
				ID:           "role_release_captain",
				ProjectID:    "proj_alpha",
				Name:         " Release Captain ",
				Description:  " Coordinates releases ",
				Instructions: "Keep release notes current.",
			})
			if err != nil {
				t.Fatalf("CreateRole: %v", err)
			}
			if custom.Name != "Release Captain" || custom.BuiltIn {
				t.Fatalf("custom role = %+v, want normalized custom role", custom)
			}
			if _, err := store.CreateRole(ctx, AgentRoleProfile{ID: "product_manager", ProjectID: "proj_alpha", Name: "Override"}); !errors.Is(err, ErrBuiltInRole) {
				t.Fatalf("CreateRole built-in error = %v, want ErrBuiltInRole", err)
			}
			if err := store.DeleteRole(ctx, "proj_alpha", "product_manager"); !errors.Is(err, ErrBuiltInRole) {
				t.Fatalf("DeleteRole built-in error = %v, want ErrBuiltInRole", err)
			}

			work, err := store.CreateWorkItem(ctx, WorkItem{
				ID:              "work_api",
				ProjectID:       "proj_alpha",
				Title:           " Add project work API ",
				Brief:           "Persist work coordination metadata.",
				Priority:        "high",
				OwnerRoleID:     "software_developer",
				ReviewerRoleIDs: []string{"reviewer_qa", "architect", "reviewer_qa"},
			})
			if err != nil {
				t.Fatalf("CreateWorkItem: %v", err)
			}
			if work.Status != WorkItemStatusBacklog || len(work.ReviewerRoleIDs) != 2 || work.ReviewerRoleIDs[0] != "architect" {
				t.Fatalf("work item = %+v, want defaults and normalized reviewers", work)
			}

			got, ok, err := store.GetWorkItem(ctx, "proj_alpha", "work_api")
			if err != nil || !ok {
				t.Fatalf("GetWorkItem ok=%v err=%v, want work item", ok, err)
			}
			got.ReviewerRoleIDs[0] = "mutated"
			gotAgain, _, err := store.GetWorkItem(ctx, "proj_alpha", "work_api")
			if err != nil {
				t.Fatalf("GetWorkItem after mutation: %v", err)
			}
			if gotAgain.ReviewerRoleIDs[0] != "architect" {
				t.Fatalf("stored reviewers mutated to %+v", gotAgain.ReviewerRoleIDs)
			}

			updatedWork, err := store.UpdateWorkItem(ctx, "proj_alpha", "work_api", func(item *WorkItem) {
				item.Status = WorkItemStatusReady
				item.ReviewerRoleIDs = []string{"reviewer_qa"}
			})
			if err != nil {
				t.Fatalf("UpdateWorkItem: %v", err)
			}
			if updatedWork.Status != WorkItemStatusReady || len(updatedWork.ReviewerRoleIDs) != 1 {
				t.Fatalf("updated work item = %+v, want ready with one reviewer", updatedWork)
			}

			assignment, err := store.CreateAssignment(ctx, Assignment{
				ID:                "asgn_impl",
				ProjectID:         "proj_alpha",
				WorkItemID:        "work_api",
				RoleID:            "software_developer",
				TaskID:            "task_123",
				RunID:             "run_123",
				ContextSnapshotID: "ctx_123",
			})
			if err != nil {
				t.Fatalf("CreateAssignment: %v", err)
			}
			if assignment.Status != AssignmentStatusQueued {
				t.Fatalf("assignment status = %q, want queued", assignment.Status)
			}
			if assignment.DriverKind != AssignmentDriverHecateTask {
				t.Fatalf("assignment driver_kind = %q, want hecate_task", assignment.DriverKind)
			}

			startedAt := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
			updatedAssignment, err := store.UpdateAssignment(ctx, "proj_alpha", "asgn_impl", func(item *Assignment) {
				item.DriverKind = AssignmentDriverExternalAgent
				item.Status = AssignmentStatusRunning
				item.StartedAt = startedAt
			})
			if err != nil {
				t.Fatalf("UpdateAssignment: %v", err)
			}
			if updatedAssignment.DriverKind != AssignmentDriverExternalAgent || updatedAssignment.Status != AssignmentStatusRunning || !updatedAssignment.StartedAt.Equal(startedAt) {
				t.Fatalf("updated assignment = %+v, want running with start time", updatedAssignment)
			}
			if _, err := store.UpdateAssignment(ctx, "proj_alpha", "asgn_impl", func(item *Assignment) {
				item.DriverKind = "unsupported"
			}); !errors.Is(err, ErrInvalid) {
				t.Fatalf("UpdateAssignment unsupported driver error = %v, want ErrInvalid", err)
			}

			artifact, err := store.CreateArtifact(ctx, CollaborationArtifact{
				ID:           "art_brief",
				ProjectID:    "proj_alpha",
				WorkItemID:   "work_api",
				AssignmentID: "asgn_impl",
				Kind:         ArtifactKindBrief,
				Title:        "Implementation brief",
				Body:         "Build the backend substrate only.",
				AuthorRoleID: "product_manager",
			})
			if err != nil {
				t.Fatalf("CreateArtifact: %v", err)
			}
			if artifact.Kind != ArtifactKindBrief || artifact.Body == "" {
				t.Fatalf("artifact = %+v, want brief artifact", artifact)
			}

			if _, err := store.CreateWorkItem(ctx, WorkItem{ID: "work_api", ProjectID: "proj_alpha", Title: "Duplicate"}); !errors.Is(err, ErrDuplicate) {
				t.Fatalf("duplicate CreateWorkItem error = %v, want ErrDuplicate", err)
			}
			if _, err := store.CreateAssignment(ctx, Assignment{ID: "asgn_impl", ProjectID: "proj_alpha", WorkItemID: "work_api", RoleID: "software_developer"}); !errors.Is(err, ErrDuplicate) {
				t.Fatalf("duplicate CreateAssignment error = %v, want ErrDuplicate", err)
			}
			if _, err := store.CreateArtifact(ctx, CollaborationArtifact{ID: "art_brief", ProjectID: "proj_alpha", WorkItemID: "work_api", Kind: ArtifactKindBrief, Body: "Duplicate"}); !errors.Is(err, ErrDuplicate) {
				t.Fatalf("duplicate CreateArtifact error = %v, want ErrDuplicate", err)
			}

			assignments, err := store.ListAssignments(ctx, AssignmentFilter{ProjectID: "proj_alpha", WorkItemID: "work_api"})
			if err != nil {
				t.Fatalf("ListAssignments: %v", err)
			}
			if len(assignments) != 1 || assignments[0].ID != "asgn_impl" {
				t.Fatalf("assignments = %+v, want created assignment", assignments)
			}
			artifacts, err := store.ListArtifacts(ctx, ArtifactFilter{ProjectID: "proj_alpha", WorkItemID: "work_api"})
			if err != nil {
				t.Fatalf("ListArtifacts: %v", err)
			}
			if len(artifacts) != 1 || artifacts[0].ID != "art_brief" {
				t.Fatalf("artifacts = %+v, want created artifact", artifacts)
			}

			if err := store.DeleteWorkItem(ctx, "proj_alpha", "work_api"); err != nil {
				t.Fatalf("DeleteWorkItem: %v", err)
			}
			assignments, err = store.ListAssignments(ctx, AssignmentFilter{ProjectID: "proj_alpha"})
			if err != nil {
				t.Fatalf("ListAssignments after delete: %v", err)
			}
			artifacts, err = store.ListArtifacts(ctx, ArtifactFilter{ProjectID: "proj_alpha"})
			if err != nil {
				t.Fatalf("ListArtifacts after delete: %v", err)
			}
			if len(assignments) != 0 || len(artifacts) != 0 {
				t.Fatalf("work item delete left assignments=%+v artifacts=%+v", assignments, artifacts)
			}
		})
	}
}

func TestStoreConformance_ProjectCleanup(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		new  func(*testing.T) Store
	}{
		{name: "memory", new: func(t *testing.T) Store { return NewMemoryStore() }},
		{name: "sqlite", new: func(t *testing.T) Store { return newSQLiteTestStore(t) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			store := tc.new(t)
			if _, err := store.CreateRole(ctx, AgentRoleProfile{ID: "role_custom", ProjectID: "proj_alpha", Name: "Custom"}); err != nil {
				t.Fatalf("CreateRole: %v", err)
			}
			if _, err := store.CreateWorkItem(ctx, WorkItem{ID: "work_alpha", ProjectID: "proj_alpha", Title: "Alpha"}); err != nil {
				t.Fatalf("CreateWorkItem alpha: %v", err)
			}
			if _, err := store.CreateWorkItem(ctx, WorkItem{ID: "work_beta", ProjectID: "proj_beta", Title: "Beta"}); err != nil {
				t.Fatalf("CreateWorkItem beta: %v", err)
			}
			if _, err := store.CreateAssignment(ctx, Assignment{ID: "asgn_alpha", ProjectID: "proj_alpha", WorkItemID: "work_alpha", RoleID: "software_developer"}); err != nil {
				t.Fatalf("CreateAssignment: %v", err)
			}
			if _, err := store.CreateArtifact(ctx, CollaborationArtifact{ID: "art_alpha", ProjectID: "proj_alpha", WorkItemID: "work_alpha", Kind: ArtifactKindDecisionNote, Body: "Ship it."}); err != nil {
				t.Fatalf("CreateArtifact: %v", err)
			}

			deleted, err := store.DeleteProject(ctx, "proj_alpha")
			if err != nil {
				t.Fatalf("DeleteProject: %v", err)
			}
			if deleted != 4 {
				t.Fatalf("DeleteProject deleted = %d, want 4", deleted)
			}
			if roles, err := store.ListRoles(ctx, "proj_alpha"); err != nil || roleIDExists(roles, "role_custom") {
				t.Fatalf("roles after project delete = %+v err=%v, want only built-ins", roles, err)
			}
			if items, err := store.ListWorkItems(ctx, "proj_alpha"); err != nil || len(items) != 0 {
				t.Fatalf("alpha work items after delete = %+v err=%v, want none", items, err)
			}
			if items, err := store.ListWorkItems(ctx, "proj_beta"); err != nil || len(items) != 1 {
				t.Fatalf("beta work items after alpha delete = %+v err=%v, want retained", items, err)
			}

			cleared, err := store.Clear(ctx)
			if err != nil {
				t.Fatalf("Clear: %v", err)
			}
			if cleared != 1 {
				t.Fatalf("Clear deleted = %d, want remaining beta work item", cleared)
			}
		})
	}
}

func TestMemoryStore_UpdateRejectsIDChanges(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	if _, err := store.CreateWorkItem(ctx, WorkItem{ID: "work_alpha", ProjectID: "proj_alpha", Title: "Alpha"}); err != nil {
		t.Fatalf("CreateWorkItem: %v", err)
	}
	if _, err := store.UpdateWorkItem(ctx, "proj_alpha", "work_alpha", func(item *WorkItem) {
		item.ID = "work_beta"
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("UpdateWorkItem id change error = %v, want ErrInvalid", err)
	}
}

func TestSQLiteStore_AddsAssignmentDriverKindToExistingTable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client, err := storage.NewSQLiteClient(ctx, storage.SQLiteConfig{
		Path:        filepath.Join(t.TempDir(), "projectwork.db"),
		TablePrefix: "test",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	assignmentsTbl := client.QualifiedTable("project_work_assignments")
	if _, err := client.DB().ExecContext(ctx, `
CREATE TABLE `+assignmentsTbl+` (
	id TEXT NOT NULL,
	project_id TEXT NOT NULL,
	work_item_id TEXT NOT NULL,
	role_id TEXT NOT NULL,
	status TEXT NOT NULL,
	task_id TEXT NOT NULL DEFAULT '',
	run_id TEXT NOT NULL DEFAULT '',
	chat_session_id TEXT NOT NULL DEFAULT '',
	message_id TEXT NOT NULL DEFAULT '',
	context_snapshot_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	started_at TEXT NOT NULL DEFAULT '',
	completed_at TEXT NOT NULL DEFAULT '',
	PRIMARY KEY(project_id, id)
)`); err != nil {
		t.Fatalf("create legacy assignments table: %v", err)
	}
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if _, err := client.DB().ExecContext(ctx, `
INSERT INTO `+assignmentsTbl+` (
	id, project_id, work_item_id, role_id, status, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"asgn_legacy", "proj_alpha", "work_alpha", "software_developer", AssignmentStatusQueued, now, now,
	); err != nil {
		t.Fatalf("insert legacy assignment: %v", err)
	}

	store, err := NewSQLiteStore(ctx, client)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	assignments, err := store.ListAssignments(ctx, AssignmentFilter{ProjectID: "proj_alpha"})
	if err != nil {
		t.Fatalf("ListAssignments: %v", err)
	}
	if len(assignments) != 1 || assignments[0].DriverKind != AssignmentDriverHecateTask {
		t.Fatalf("assignments = %+v, want legacy assignment backfilled to hecate_task", assignments)
	}
}

func newSQLiteTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	client, err := storage.NewSQLiteClient(context.Background(), storage.SQLiteConfig{
		Path:        filepath.Join(t.TempDir(), "projectwork.db"),
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

func roleIDExists(roles []AgentRoleProfile, id string) bool {
	for _, role := range roles {
		if role.ID == id {
			return true
		}
	}
	return false
}
