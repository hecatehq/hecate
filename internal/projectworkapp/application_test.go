package projectworkapp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/projectwork"
)

func newTestApplication(store projectwork.Store) *Application {
	return New(Options{
		Store:       store,
		IDGenerator: func(prefix string) string { return prefix + "_fixed" },
	})
}

func TestApplication_CreateRoleGeneratesIDAndCopiesSkills(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := projectwork.NewMemoryStore()
	app := newTestApplication(store)
	skills := []string{"backend", "ui"}

	role, err := app.CreateRole(ctx, "proj_1", CreateRoleCommand{
		Name:     "Builder",
		SkillIDs: skills,
	})
	if err != nil {
		t.Fatalf("CreateRole() error = %v", err)
	}
	if role.ID != "role_fixed" || role.ProjectID != "proj_1" || role.Name != "Builder" {
		t.Fatalf("role = %+v, want generated id, project, and name", role)
	}
	skills[0] = "mutated"
	if role.SkillIDs[0] != "backend" {
		t.Fatalf("role skills mutated through command slice: %+v", role.SkillIDs)
	}
}

func TestApplication_CreateAssignmentUsesRoleDefaultDriver(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := projectwork.NewMemoryStore()
	app := newTestApplication(store)
	if _, err := app.CreateWorkItem(ctx, "proj_1", CreateWorkItemCommand{ID: "work_1", Title: "Build"}); err != nil {
		t.Fatalf("CreateWorkItem() error = %v", err)
	}
	if _, err := app.CreateRole(ctx, "proj_1", CreateRoleCommand{
		ID:                "role_ext",
		Name:              "External",
		DefaultDriverKind: projectwork.AssignmentDriverExternalAgent,
	}); err != nil {
		t.Fatalf("CreateRole() error = %v", err)
	}

	assignment, err := app.CreateAssignment(ctx, "proj_1", "work_1", CreateAssignmentCommand{
		RoleID: "role_ext",
	})
	if err != nil {
		t.Fatalf("CreateAssignment() error = %v", err)
	}
	if assignment.ID != "asgn_fixed" || assignment.DriverKind != projectwork.AssignmentDriverExternalAgent {
		t.Fatalf("assignment = %+v, want generated id and role default driver", assignment)
	}
}

func TestApplication_UpdateAssignmentAppliesOptionalFields(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := projectwork.NewMemoryStore()
	app := newTestApplication(store)
	if _, err := app.CreateWorkItem(ctx, "proj_1", CreateWorkItemCommand{ID: "work_1", Title: "Build"}); err != nil {
		t.Fatalf("CreateWorkItem() error = %v", err)
	}
	assignment, err := app.CreateAssignment(ctx, "proj_1", "work_1", CreateAssignmentCommand{
		ID:     "asgn_1",
		RoleID: "software_developer",
	})
	if err != nil {
		t.Fatalf("CreateAssignment() error = %v", err)
	}

	status := projectwork.AssignmentStatusRunning
	runID := "run_1"
	startedAt := time.Date(2026, 6, 11, 10, 0, 0, 0, time.UTC)
	updated, err := app.UpdateAssignment(ctx, "proj_1", assignment.ID, UpdateAssignmentCommand{
		Status:    &status,
		RunID:     &runID,
		StartedAt: &startedAt,
	})
	if err != nil {
		t.Fatalf("UpdateAssignment() error = %v", err)
	}
	if updated.Status != status || updated.RunID != runID || !updated.StartedAt.Equal(startedAt) {
		t.Fatalf("updated assignment = %+v, want optional fields applied", updated)
	}
}

func TestApplication_NilStore(t *testing.T) {
	t.Parallel()

	app := New(Options{})
	if _, err := app.CreateRole(context.Background(), "proj", CreateRoleCommand{Name: "Role"}); !errors.Is(err, ErrStoreNotConfigured) {
		t.Fatalf("CreateRole(nil store) error = %v, want ErrStoreNotConfigured", err)
	}
	if err := app.DeleteAssignment(context.Background(), "proj", "work", "asgn"); !errors.Is(err, ErrStoreNotConfigured) {
		t.Fatalf("DeleteAssignment(nil store) error = %v, want ErrStoreNotConfigured", err)
	}
}
