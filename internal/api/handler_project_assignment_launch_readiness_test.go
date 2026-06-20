package api

import (
	"net/http"
	"testing"

	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectwork"
)

func TestProjectWorkAPI_AssignmentLaunchReadinessReturnsNativePlanWithoutSideEffects(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServerWithProviders(&fakeProvider{
		name: "anthropic",
	})
	workspace := t.TempDir()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace:           workspace,
		Driver:              projectwork.AssignmentDriverHecateTask,
		WithoutRoleDefaults: true,
	})
	if _, err := handler.projects.Update(t.Context(), "proj_start", func(project *projects.Project) {
		project.DefaultProvider = "anthropic"
		project.DefaultModel = "gpt-4o-mini"
	}); err != nil {
		t.Fatalf("Update project defaults: %v", err)
	}

	readiness := mustRequestJSON[ProjectAssignmentLaunchReadinessEnvelope](newAPITestClient(t, server), http.MethodGet, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/launch-readiness", "")
	if readiness.Object != "project_assignment_launch_readiness" {
		t.Fatalf("object = %q, want project_assignment_launch_readiness", readiness.Object)
	}
	if !readiness.Data.Ready || readiness.Data.Status != projectAssignmentLaunchReadinessStatusReady {
		t.Fatalf("readiness = %+v, want ready", readiness.Data)
	}
	if readiness.Data.ProjectID != "proj_start" || readiness.Data.WorkItemID != "work_start" || readiness.Data.AssignmentID != "asgn_start" {
		t.Fatalf("refs = %+v, want project/work/assignment refs", readiness.Data)
	}
	if readiness.Data.DriverKind != projectwork.AssignmentDriverHecateTask || readiness.Data.Workspace != workspace || readiness.Data.RootID != "root_start" {
		t.Fatalf("launch target = %+v, want native workspace/root", readiness.Data)
	}
	if readiness.Data.Provider != "anthropic" || readiness.Data.Model != "gpt-4o-mini" || readiness.Data.ExecutionProfile != "coding_agent" {
		t.Fatalf("launch hints = provider/model/profile %q/%q/%q, want anthropic/gpt-4o-mini/coding_agent", readiness.Data.Provider, readiness.Data.Model, readiness.Data.ExecutionProfile)
	}
	if readiness.Data.ModelReadiness == nil || !readiness.Data.ModelReadiness.Ready {
		t.Fatalf("model_readiness = %+v, want ready", readiness.Data.ModelReadiness)
	}
	tasks, err := handler.taskStore.ListTasks(t.Context(), taskstateFilterAll())
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks = %+v, want no task created by launch readiness", tasks)
	}
}

func TestProjectWorkAPI_AssignmentLaunchReadinessSurfacesBlockedModel(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServerWithProviders(&fakeProvider{
		name:         "openai",
		defaultModel: "gpt-4o-mini",
	})
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace:           t.TempDir(),
		Driver:              projectwork.AssignmentDriverHecateTask,
		WithoutRoleDefaults: true,
	})
	if _, err := handler.projects.Update(t.Context(), "proj_start", func(project *projects.Project) {
		project.DefaultProvider = ""
		project.DefaultModel = "dogfood-model"
	}); err != nil {
		t.Fatalf("Update project defaults: %v", err)
	}

	readiness := mustRequestJSON[ProjectAssignmentLaunchReadinessEnvelope](newAPITestClient(t, server), http.MethodGet, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/launch-readiness", "")
	if readiness.Data.Ready || readiness.Data.Status != projectAssignmentLaunchReadinessStatusBlocked {
		t.Fatalf("readiness = %+v, want blocked", readiness.Data)
	}
	if readiness.Data.ModelReadiness == nil || readiness.Data.ModelReadiness.Ready || readiness.Data.ModelReadiness.Reason != "model_not_discovered" {
		t.Fatalf("model_readiness = %+v, want blocked model_not_discovered", readiness.Data.ModelReadiness)
	}
	if len(readiness.Data.Blockers) == 0 || readiness.Data.Blockers[0] == "" {
		t.Fatalf("blockers = %+v, want blocked model explanation", readiness.Data.Blockers)
	}
}

func TestProjectWorkAPI_AssignmentLaunchReadinessReportsMissingRole(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServerWithProviders(&fakeProvider{})
	workspace := t.TempDir()
	if _, err := handler.projects.Create(t.Context(), projects.Project{
		ID:              "proj_missing_role",
		Name:            "Missing Role",
		DefaultProvider: "openai",
		DefaultModel:    "gpt-4o-mini",
		DefaultRootID:   "root_missing_role",
		Roots: []projects.Root{{
			ID:     "root_missing_role",
			Path:   workspace,
			Kind:   "git",
			Active: true,
		}},
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	if _, err := handler.projectWork.CreateWorkItem(t.Context(), projectwork.WorkItem{
		ID:        "work_missing_role",
		ProjectID: "proj_missing_role",
		Title:     "Launch missing role",
		Status:    projectwork.WorkItemStatusReady,
	}); err != nil {
		t.Fatalf("CreateWorkItem: %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(t.Context(), projectwork.Assignment{
		ID:         "asgn_missing_role",
		ProjectID:  "proj_missing_role",
		WorkItemID: "work_missing_role",
		RoleID:     "role_missing",
		DriverKind: projectwork.AssignmentDriverHecateTask,
		Status:     projectwork.AssignmentStatusQueued,
	}); err != nil {
		t.Fatalf("CreateAssignment: %v", err)
	}

	readiness := mustRequestJSON[ProjectAssignmentLaunchReadinessEnvelope](newAPITestClient(t, server), http.MethodGet, "/hecate/v1/projects/proj_missing_role/work-items/work_missing_role/assignments/asgn_missing_role/launch-readiness", "")
	if readiness.Data.Ready {
		t.Fatalf("readiness = %+v, want missing role blocker", readiness.Data)
	}
	if len(readiness.Data.Blockers) != 1 || readiness.Data.Blockers[0] != "Assignment role not found." {
		t.Fatalf("blockers = %+v, want missing role blocker", readiness.Data.Blockers)
	}
	tasks, err := handler.taskStore.ListTasks(t.Context(), taskstateFilterAll())
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks = %+v, want no task created by launch readiness", tasks)
	}
}
