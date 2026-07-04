package projectruntime

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/storage"
)

func TestStoreConformance_AssignmentRuntimeLifecycle(t *testing.T) {
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
			startedAt := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
			completedAt := startedAt.Add(3 * time.Minute)

			runtime, err := store.Upsert(ctx, AssignmentRuntime{
				ProjectID:    " proj_alpha ",
				AssignmentID: " asgn_impl ",
				ExecutionRef: projectwork.AssignmentExecutionRef{
					Kind:              projectwork.AssignmentExecutionKindTaskRun,
					TaskID:            "task_123",
					RunID:             "run_123",
					ContextSnapshotID: "ctx_123",
					Status:            projectwork.AssignmentStatusRunning,
				},
				ContextPacket: []byte(`{"id":"ctx_123"}`),
				StartedAt:     startedAt,
				CompletedAt:   completedAt,
			})
			if err != nil {
				t.Fatalf("Upsert() error = %v", err)
			}
			if runtime.ProjectID != "proj_alpha" || runtime.AssignmentID != "asgn_impl" || runtime.ExecutionRef.TaskID != "task_123" || string(runtime.ContextPacket) != `{"id":"ctx_123"}` {
				t.Fatalf("runtime = %+v, want normalized runtime links", runtime)
			}
			if !runtime.StartedAt.Equal(startedAt) || !runtime.CompletedAt.Equal(completedAt) || runtime.UpdatedAt.IsZero() {
				t.Fatalf("runtime times = started %v completed %v updated %v, want persisted timestamps", runtime.StartedAt, runtime.CompletedAt, runtime.UpdatedAt)
			}

			got, ok, err := store.Get(ctx, "proj_alpha", "asgn_impl")
			if err != nil || !ok {
				t.Fatalf("Get() ok=%v error=%v, want runtime", ok, err)
			}
			got.ContextPacket[0] = '!'
			gotAgain, ok, err := store.Get(ctx, "proj_alpha", "asgn_impl")
			if err != nil || !ok {
				t.Fatalf("Get() after mutation ok=%v error=%v, want runtime", ok, err)
			}
			if string(gotAgain.ContextPacket) != `{"id":"ctx_123"}` {
				t.Fatalf("stored context packet mutated to %q", string(gotAgain.ContextPacket))
			}

			updated, err := store.Upsert(ctx, AssignmentRuntime{
				ProjectID:    "proj_alpha",
				AssignmentID: "asgn_impl",
				ExecutionRef: projectwork.AssignmentExecutionRef{
					Kind:          projectwork.AssignmentExecutionKindChatSession,
					ChatSessionID: "chat_123",
					Status:        projectwork.AssignmentStatusRunning,
				},
				ContextPacket: []byte(`{"id":"ctx_chat"}`),
				StartedAt:     startedAt,
			})
			if err != nil {
				t.Fatalf("Upsert(update) error = %v", err)
			}
			if updated.ExecutionRef.ChatSessionID != "chat_123" || updated.ExecutionRef.TaskID != "" || string(updated.ContextPacket) != `{"id":"ctx_chat"}` || !updated.CompletedAt.IsZero() {
				t.Fatalf("updated runtime = %+v, want replaced chat runtime", updated)
			}

			if _, err := store.Upsert(ctx, AssignmentRuntime{AssignmentID: "asgn_missing_project"}); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Upsert(missing project) error = %v, want ErrInvalid", err)
			}
			if err := store.Delete(ctx, "proj_alpha", "asgn_impl"); err != nil {
				t.Fatalf("Delete() error = %v", err)
			}
			if _, ok, err := store.Get(ctx, "proj_alpha", "asgn_impl"); err != nil || ok {
				t.Fatalf("Get(deleted) ok=%v error=%v, want not found", ok, err)
			}
			if err := store.Delete(ctx, "proj_alpha", "asgn_impl"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("Delete(missing) error = %v, want ErrNotFound", err)
			}

			if _, err := store.Upsert(ctx, AssignmentRuntime{ProjectID: "proj_alpha", AssignmentID: "asgn_one"}); err != nil {
				t.Fatalf("Upsert(one) error = %v", err)
			}
			if _, err := store.Upsert(ctx, AssignmentRuntime{ProjectID: "proj_alpha", AssignmentID: "asgn_two"}); err != nil {
				t.Fatalf("Upsert(two) error = %v", err)
			}
			if _, err := store.Upsert(ctx, AssignmentRuntime{ProjectID: "proj_other", AssignmentID: "asgn_other"}); err != nil {
				t.Fatalf("Upsert(other) error = %v", err)
			}
			deleted, err := store.DeleteProject(ctx, "proj_alpha")
			if err != nil {
				t.Fatalf("DeleteProject() error = %v", err)
			}
			if deleted != 2 {
				t.Fatalf("DeleteProject() deleted = %d, want 2", deleted)
			}
			if _, ok, err := store.Get(ctx, "proj_other", "asgn_other"); err != nil || !ok {
				t.Fatalf("Get(other) ok=%v error=%v, want untouched other project", ok, err)
			}
			deleted, err = store.Clear(ctx)
			if err != nil {
				t.Fatalf("Clear() error = %v", err)
			}
			if deleted != 1 {
				t.Fatalf("Clear() deleted = %d, want 1", deleted)
			}
		})
	}
}

func TestStoreConformance_RuntimeDefaultsLifecycle(t *testing.T) {
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
			toolsEnabled := false
			compactOutput := true

			projectDefaults, err := store.UpsertProjectDefaults(ctx, ProjectDefaults{
				ProjectID:                " proj_alpha ",
				DefaultProvider:          " openai ",
				DefaultModel:             " gpt-5 ",
				DefaultAgentProfile:      " implementation ",
				DefaultToolsEnabled:      &toolsEnabled,
				DefaultWorkspaceMode:     " worktree ",
				DefaultSystemPrompt:      " Stay crisp. ",
				DefaultCompactToolOutput: &compactOutput,
			})
			if err != nil {
				t.Fatalf("UpsertProjectDefaults() error = %v", err)
			}
			if projectDefaults.ProjectID != "proj_alpha" || projectDefaults.DefaultProvider != "openai" || projectDefaults.DefaultModel != "gpt-5" || projectDefaults.DefaultWorkspaceMode != "worktree" || projectDefaults.DefaultSystemPrompt != "Stay crisp." {
				t.Fatalf("project defaults = %+v, want normalized runtime defaults", projectDefaults)
			}
			if projectDefaults.DefaultToolsEnabled == nil || *projectDefaults.DefaultToolsEnabled || projectDefaults.DefaultCompactToolOutput == nil || !*projectDefaults.DefaultCompactToolOutput {
				t.Fatalf("project bool defaults = tools %v compact %v, want false/true", projectDefaults.DefaultToolsEnabled, projectDefaults.DefaultCompactToolOutput)
			}
			projectDefaults.DefaultToolsEnabled = nil
			gotProjectDefaults, ok, err := store.GetProjectDefaults(ctx, "proj_alpha")
			if err != nil || !ok {
				t.Fatalf("GetProjectDefaults() ok=%v error=%v, want defaults", ok, err)
			}
			if gotProjectDefaults.DefaultToolsEnabled == nil || *gotProjectDefaults.DefaultToolsEnabled {
				t.Fatalf("stored project defaults mutated to %+v", gotProjectDefaults.DefaultToolsEnabled)
			}

			roleDefaults, err := store.UpsertRoleDefaults(ctx, RoleDefaults{
				ProjectID:           " proj_alpha ",
				RoleID:              " role_impl ",
				DefaultProvider:     " anthropic ",
				DefaultModel:        " claude-sonnet-4 ",
				DefaultAgentProfile: " architecture ",
			})
			if err != nil {
				t.Fatalf("UpsertRoleDefaults() error = %v", err)
			}
			if roleDefaults.ProjectID != "proj_alpha" || roleDefaults.RoleID != "role_impl" || roleDefaults.DefaultProvider != "anthropic" || roleDefaults.DefaultModel != "claude-sonnet-4" {
				t.Fatalf("role defaults = %+v, want normalized role runtime defaults", roleDefaults)
			}
			if _, ok, err := store.GetRoleDefaults(ctx, "proj_alpha", "role_impl"); err != nil || !ok {
				t.Fatalf("GetRoleDefaults() ok=%v error=%v, want defaults", ok, err)
			}

			if _, err := store.UpsertProjectDefaults(ctx, ProjectDefaults{}); !errors.Is(err, ErrInvalid) {
				t.Fatalf("UpsertProjectDefaults(missing project) error = %v, want ErrInvalid", err)
			}
			if _, err := store.UpsertRoleDefaults(ctx, RoleDefaults{ProjectID: "proj_alpha"}); !errors.Is(err, ErrInvalid) {
				t.Fatalf("UpsertRoleDefaults(missing role) error = %v, want ErrInvalid", err)
			}

			if err := store.DeleteRoleDefaults(ctx, "proj_alpha", "role_impl"); err != nil {
				t.Fatalf("DeleteRoleDefaults() error = %v", err)
			}
			if _, ok, err := store.GetRoleDefaults(ctx, "proj_alpha", "role_impl"); err != nil || ok {
				t.Fatalf("GetRoleDefaults(deleted) ok=%v error=%v, want not found", ok, err)
			}
			if err := store.DeleteRoleDefaults(ctx, "proj_alpha", "role_impl"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("DeleteRoleDefaults(missing) error = %v, want ErrNotFound", err)
			}

			if _, err := store.UpsertRoleDefaults(ctx, RoleDefaults{ProjectID: "proj_alpha", RoleID: "role_one"}); err != nil {
				t.Fatalf("UpsertRoleDefaults(one) error = %v", err)
			}
			if _, err := store.UpsertRoleDefaults(ctx, RoleDefaults{ProjectID: "proj_other", RoleID: "role_other"}); err != nil {
				t.Fatalf("UpsertRoleDefaults(other) error = %v", err)
			}
			if _, err := store.Upsert(ctx, AssignmentRuntime{ProjectID: "proj_alpha", AssignmentID: "asgn_one"}); err != nil {
				t.Fatalf("Upsert(runtime) error = %v", err)
			}
			deleted, err := store.DeleteProject(ctx, "proj_alpha")
			if err != nil {
				t.Fatalf("DeleteProject() error = %v", err)
			}
			if deleted != 3 {
				t.Fatalf("DeleteProject() deleted = %d, want project defaults + role defaults + assignment runtime", deleted)
			}
			if _, ok, err := store.GetProjectDefaults(ctx, "proj_alpha"); err != nil || ok {
				t.Fatalf("GetProjectDefaults(deleted project) ok=%v error=%v, want not found", ok, err)
			}
			if _, ok, err := store.GetRoleDefaults(ctx, "proj_other", "role_other"); err != nil || !ok {
				t.Fatalf("GetRoleDefaults(other) ok=%v error=%v, want untouched", ok, err)
			}
			deleted, err = store.Clear(ctx)
			if err != nil {
				t.Fatalf("Clear() error = %v", err)
			}
			if deleted != 1 {
				t.Fatalf("Clear() deleted = %d, want remaining role defaults", deleted)
			}
		})
	}
}

func TestApply_OverlaysRuntimeOnAssignment(t *testing.T) {
	assignment := projectwork.Assignment{
		ID:          "asgn_1",
		ProjectID:   "proj_1",
		StartedAt:   time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC),
		CompletedAt: time.Date(2026, 6, 1, 11, 0, 0, 0, time.UTC),
		ExecutionRef: projectwork.AssignmentExecutionRef{
			Kind:   projectwork.AssignmentExecutionKindTaskRun,
			TaskID: "task_old",
		},
		ContextPacket: []byte(`{"id":"old"}`),
	}
	runtime := AssignmentRuntime{
		ProjectID:    "proj_1",
		AssignmentID: "asgn_1",
		ExecutionRef: projectwork.AssignmentExecutionRef{
			Kind:          projectwork.AssignmentExecutionKindChatSession,
			ChatSessionID: "chat_new",
		},
		ContextPacket: []byte(`{"id":"new"}`),
		StartedAt:     time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	}

	overlaid := Apply(assignment, runtime)
	if overlaid.ExecutionRef.ChatSessionID != "chat_new" || overlaid.ExecutionRef.TaskID != "" || string(overlaid.ContextPacket) != `{"id":"new"}` || !overlaid.CompletedAt.IsZero() {
		t.Fatalf("Apply() = %+v, want runtime overlay", overlaid)
	}
	if unchanged := Apply(assignment, AssignmentRuntime{ProjectID: "proj_other", AssignmentID: "asgn_1"}); unchanged.ExecutionRef.TaskID != "task_old" {
		t.Fatalf("Apply(mismatched) = %+v, want original assignment", unchanged)
	}
}

func newSQLiteTestStore(t *testing.T) Store {
	t.Helper()
	ctx := context.Background()
	client, err := storage.NewSQLiteClient(ctx, storage.SQLiteConfig{
		Path:        filepath.Join(t.TempDir(), "projectruntime.db"),
		TablePrefix: "test",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	store, err := NewSQLiteStore(ctx, client)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	return store
}
