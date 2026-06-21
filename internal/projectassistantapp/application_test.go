package projectassistantapp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hecatehq/hecate/internal/projectassistant"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/projectworkapp"
)

func TestApplication_ContextDelegatesToProjectAssistantService(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	projectStore := projects.NewMemoryStore()
	workStore := projectwork.NewMemoryStore()
	if _, err := projectStore.Create(ctx, projects.Project{ID: "proj_1", Name: "Hecate"}); err != nil {
		t.Fatalf("Create(project) error = %v", err)
	}
	if _, err := workStore.CreateRole(ctx, projectwork.AgentRoleProfile{
		ID:        "role_pm",
		ProjectID: "proj_1",
		Name:      "Product Manager",
	}); err != nil {
		t.Fatalf("CreateRole() error = %v", err)
	}
	workItem, err := workStore.CreateWorkItem(ctx, projectwork.WorkItem{
		ID:          "work_1",
		ProjectID:   "proj_1",
		Title:       "Plan work",
		Status:      projectwork.WorkItemStatusReady,
		OwnerRoleID: "role_pm",
	})
	if err != nil {
		t.Fatalf("CreateWorkItem() error = %v", err)
	}
	app := New(Options{Projects: projectStore, Work: workStore})

	got, err := app.Context(ctx, ContextCommand{
		ProjectID:  "proj_1",
		WorkItemID: workItem.ID,
		Request:    "Queue the owner",
	})
	if err != nil {
		t.Fatalf("Context() error = %v", err)
	}
	if got.Project.ID != "proj_1" || got.SelectedWork == nil || got.SelectedWork.ID != "work_1" {
		t.Fatalf("context = %+v, want project and selected work", got)
	}
	if got.Selection.RoleID != "role_pm" || got.Selection.DriverKind != projectwork.AssignmentDriverHecateTask {
		t.Fatalf("selection = %+v, want owner role and Hecate task default", got.Selection)
	}
}

func TestApplication_ProposeUsesIDGeneratorAndTrace(t *testing.T) {
	t.Parallel()

	app := New(Options{IDGenerator: func(prefix string) string { return prefix + "_fixed" }})
	proposal, err := app.Propose(context.Background(), ProposeCommand{
		Title:   "Create role",
		Summary: "Create a role proposal.",
		TraceID: "trace_1",
		Actions: []projectassistant.Action{{
			Kind:   projectassistant.ActionCreateRole,
			Target: map[string]string{"project_id": "proj_1"},
			Patch:  rawJSON(t, map[string]any{"id": "role_1", "name": "Reviewer"}),
		}},
	})
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	if proposal.ID != "pa_fixed" || proposal.TraceID != "trace_1" || !proposal.RequiresConfirmation {
		t.Fatalf("proposal = %+v, want generated id, trace, and confirmation", proposal)
	}
}

func TestApplication_ApplyKeepsProgressInCachedApplication(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	projectStore := projects.NewMemoryStore()
	app := New(Options{
		Projects:    projectStore,
		IDGenerator: func(prefix string) string { return prefix + "_fixed" },
	})
	proposal := projectassistant.Proposal{
		ID:                   "pa_apply",
		Title:                "Create project",
		RequiresConfirmation: true,
		Actions: []projectassistant.Action{{
			Kind:   projectassistant.ActionCreateProject,
			Target: map[string]string{"project_id": "proj_created"},
			Patch:  rawJSON(t, map[string]any{"id": "proj_created", "name": "Created"}),
		}},
	}

	if _, err := app.Apply(ctx, ApplyCommand{Proposal: proposal}); !errors.Is(err, projectassistant.ErrConfirmationRequired) {
		t.Fatalf("Apply(unconfirmed) error = %v, want ErrConfirmationRequired", err)
	}
	result, err := app.Apply(ctx, ApplyCommand{Proposal: proposal, Confirm: true})
	if err != nil {
		t.Fatalf("Apply(confirmed) error = %v", err)
	}
	if !result.Applied || len(result.Actions) != 1 || result.Actions[0].ID != "proj_created" {
		t.Fatalf("apply result = %+v, want created project", result)
	}
	if _, ok, err := projectStore.Get(ctx, "proj_created"); err != nil || !ok {
		t.Fatalf("created project ok=%v err=%v, want persisted", ok, err)
	}
	if _, err := app.Apply(ctx, ApplyCommand{Proposal: proposal, Confirm: true}); !errors.Is(err, projectassistant.ErrConflict) {
		t.Fatalf("Apply(repeated) error = %v, want ErrConflict", err)
	}
}

func TestApplication_ApplyWorkItemDoneUsesProjectWorkAuthority(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	projectStore := projects.NewMemoryStore()
	workStore := projectwork.NewMemoryStore()
	if _, err := projectStore.Create(ctx, projects.Project{ID: "proj_closeout", Name: "Closeout"}); err != nil {
		t.Fatalf("Create(project) error = %v", err)
	}
	if _, err := workStore.CreateWorkItem(ctx, projectwork.WorkItem{
		ID:        "work_closeout",
		ProjectID: "proj_closeout",
		Title:     "Closeout gated work",
		Status:    projectwork.WorkItemStatusReview,
	}); err != nil {
		t.Fatalf("CreateWorkItem() error = %v", err)
	}
	if _, err := workStore.CreateAssignment(ctx, projectwork.Assignment{
		ID:         "asgn_closeout",
		ProjectID:  "proj_closeout",
		WorkItemID: "work_closeout",
		RoleID:     "software_developer",
		Status:     projectwork.AssignmentStatusCompleted,
	}); err != nil {
		t.Fatalf("CreateAssignment() error = %v", err)
	}
	app := New(Options{
		Projects:        projectStore,
		Work:            workStore,
		WorkApplication: projectworkapp.New(projectworkapp.Options{Store: workStore}),
	})
	proposal := projectassistant.Proposal{
		ID:                   "pa_closeout",
		Title:                "Mark done",
		RequiresConfirmation: true,
		Actions: []projectassistant.Action{{
			Kind:   projectassistant.ActionUpdateWorkItem,
			Target: map[string]string{"project_id": "proj_closeout", "work_item_id": "work_closeout"},
			Patch:  rawJSON(t, map[string]string{"status": projectwork.WorkItemStatusDone}),
		}},
	}

	result, err := app.Apply(ctx, ApplyCommand{Proposal: proposal, Confirm: true})
	if !errors.Is(err, projectassistant.ErrConflict) || !errors.Is(err, projectworkapp.ErrWorkItemCloseoutBlocked) {
		t.Fatalf("Apply(done) result=%+v error=%v, want Project Assistant conflict wrapping closeout blocker", result, err)
	}
	stored, ok, err := workStore.GetWorkItem(ctx, "proj_closeout", "work_closeout")
	if err != nil || !ok {
		t.Fatalf("GetWorkItem() ok=%v err=%v, want stored work item", ok, err)
	}
	if stored.Status == projectwork.WorkItemStatusDone {
		t.Fatalf("stored status = %q, want closeout guard to keep item open", stored.Status)
	}
}

func rawJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal(%#v) error = %v", value, err)
	}
	return data
}
