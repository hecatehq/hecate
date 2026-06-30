package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
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
	if _, err := handler.projectWork.UpdateRole(t.Context(), "proj_start", "role_backend", func(role *projectwork.AgentRoleProfile) {
		role.SkillIDs = []string{"network"}
	}); err != nil {
		t.Fatalf("Update role skills: %v", err)
	}
	if _, err := handler.projectSkills.UpsertDiscovered(t.Context(), "proj_start", []projectskills.Skill{{
		ID:         "network",
		ProjectID:  "proj_start",
		Title:      "Network",
		Path:       ".hecate/skills/network/SKILL.md",
		Format:     projectskills.FormatSkillMD,
		Enabled:    true,
		Status:     projectskills.StatusAvailable,
		TrustLabel: projectskills.TrustWorkspaceSkill,
		RequiredPermissions: projectskills.RequiredPermissions{
			Network: boolForLaunchReadinessTest(true),
		},
	}}); err != nil {
		t.Fatalf("UpsertDiscovered skills: %v", err)
	}

	readiness := mustRequestJSON[ProjectAssignmentLaunchReadinessEnvelope](newAPITestClient(t, server), http.MethodGet, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/launch-readiness", "")
	if readiness.Object != "project_assignment_launch_readiness" {
		t.Fatalf("object = %q, want project_assignment_launch_readiness", readiness.Object)
	}
	if readiness.Data.ReadBackend != "hecate" {
		t.Fatalf("read_backend = %q, want hecate", readiness.Data.ReadBackend)
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
	if readiness.Data.ProfilePosture == nil || readiness.Data.ProfilePosture.ID != "project_assignment" || !readiness.Data.ProfilePosture.ToolsEnabled || !readiness.Data.ProfilePosture.WritesAllowed || readiness.Data.ProfilePosture.NetworkAllowed {
		t.Fatalf("profile_posture = %+v, want project_assignment posture with tools/writes on and network off", readiness.Data.ProfilePosture)
	}
	if readiness.Data.ModelReadiness == nil || !readiness.Data.ModelReadiness.Ready {
		t.Fatalf("model_readiness = %+v, want ready", readiness.Data.ModelReadiness)
	}
	if !launchReadinessWarningContains(readiness.Data.Warnings, "Project skill Network (network) declares network enabled") {
		t.Fatalf("warnings = %+v, want project skill network posture warning", readiness.Data.Warnings)
	}
	tasks, err := handler.taskStore.ListTasks(t.Context(), taskstateFilterAll())
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks = %+v, want no task created by launch readiness", tasks)
	}
}

func boolForLaunchReadinessTest(value bool) *bool {
	return &value
}

func launchReadinessWarningContains(items []string, want string) bool {
	for _, item := range items {
		if len(item) >= len(want) && item[:len(want)] == want {
			return true
		}
	}
	return false
}

func TestProjectWorkAPI_AssignmentLaunchReadinessUsesCairnlineReadModelWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkCairnlineReadTestServer()
	workspace := t.TempDir()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace: workspace,
		Driver:    projectwork.AssignmentDriverHecateTask,
	})

	readiness := mustRequestJSON[ProjectAssignmentLaunchReadinessEnvelope](newAPITestClient(t, server), http.MethodGet, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/launch-readiness", "")
	if readiness.Data.ReadBackend != "cairnline" {
		t.Fatalf("read_backend = %q, want cairnline", readiness.Data.ReadBackend)
	}
	if !readiness.Data.Ready || readiness.Data.Status != projectAssignmentLaunchReadinessStatusReady {
		t.Fatalf("readiness = %+v, want Cairnline-backed ready launch projection", readiness.Data)
	}
	if readiness.Data.ProjectID != "proj_start" || readiness.Data.WorkItemID != "work_start" || readiness.Data.AssignmentID != "asgn_start" {
		t.Fatalf("refs = %+v, want Cairnline-projected project/work/assignment refs", readiness.Data)
	}
	if readiness.Data.DriverKind != projectwork.AssignmentDriverHecateTask || readiness.Data.Workspace != workspace || readiness.Data.RootID != "root_start" {
		t.Fatalf("launch target = %+v, want Cairnline-projected workspace/root with Hecate runtime validation", readiness.Data)
	}
	if readiness.Data.Provider != "anthropic" || readiness.Data.Model != "claude-sonnet-4" || readiness.Data.ExecutionProfile != "coding_agent" {
		t.Fatalf("launch hints = provider/model/profile %q/%q/%q, want Cairnline-projected role defaults", readiness.Data.Provider, readiness.Data.Model, readiness.Data.ExecutionProfile)
	}
	tasks, err := handler.taskStore.ListTasks(t.Context(), taskstateFilterAll())
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks = %+v, want no task created by Cairnline launch readiness", tasks)
	}
}

func TestProjectWorkAPI_AssignmentLaunchReadinessUsesCairnlineSidecarWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineSidecarReadTestServer(t, "full+temp-root")
	if handler.projectReadRoutesUseCairnlineReadModel() {
		t.Fatal("sidecar launch readiness enabled embedded Cairnline read-model routes")
	}
	if !handler.projectCairnlineSidecarReadRoutesEnabled() {
		t.Fatal("sidecar read-route predicate = false, want true")
	}

	readiness := mustRequestJSON[ProjectAssignmentLaunchReadinessEnvelope](newAPITestClient(t, server), http.MethodGet, "/hecate/v1/projects/proj_fixture/work-items/work_fixture/assignments/asg_fixture/launch-readiness", "")
	if readiness.Object != "project_assignment_launch_readiness" {
		t.Fatalf("object = %q, want project_assignment_launch_readiness", readiness.Object)
	}
	if readiness.Data.ReadBackend != "cairnline" {
		t.Fatalf("read_backend = %q, want cairnline", readiness.Data.ReadBackend)
	}
	if !readiness.Data.Ready || readiness.Data.Status != projectAssignmentLaunchReadinessStatusReady {
		t.Fatalf("readiness = %+v, want sidecar Cairnline-backed ready launch projection", readiness.Data)
	}
	if readiness.Data.ProjectID != "proj_fixture" || readiness.Data.WorkItemID != "work_fixture" || readiness.Data.AssignmentID != "asg_fixture" {
		t.Fatalf("refs = %+v, want sidecar project/work/assignment refs", readiness.Data)
	}
	if readiness.Data.DriverKind != projectwork.AssignmentDriverHecateTask || readiness.Data.RootID != "root_fixture" || readiness.Data.Workspace == "" {
		t.Fatalf("launch target = %+v, want sidecar root plus Hecate runtime validation", readiness.Data)
	}
	if readiness.Data.ProfilePosture == nil || readiness.Data.ProfilePosture.ID != "profile_fixture" || !readiness.Data.ProfilePosture.Missing {
		t.Fatalf("profile_posture = %+v, want missing sidecar profile ref to remain explicit", readiness.Data.ProfilePosture)
	}
	tasks, err := handler.taskStore.ListTasks(t.Context(), taskstateFilterAll())
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks = %+v, want no task created by sidecar launch readiness", tasks)
	}
}

func TestProjectWorkAPI_AssignmentLaunchReadinessCairnlineSidecarRequiresStructuredLaunchPacket(t *testing.T) {
	t.Parallel()
	_, server := newProjectsCairnlineSidecarReadTestServer(t, "assignments.launch_packet-text-only")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/work-items/work_fixture/assignments/asg_fixture/launch-readiness", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("launch readiness status = %d body=%s, want 502", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "structuredContent") {
		t.Fatalf("error body = %s, want structuredContent diagnostic", rec.Body.String())
	}
}

func TestProjectWorkAPI_AssignmentLaunchReadinessCairnlineSidecarRejectsRouteMismatch(t *testing.T) {
	t.Parallel()
	_, server := newProjectsCairnlineSidecarReadTestServer(t, "full+temp-root+launch-packet-route-mismatch")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/work-items/work_fixture/assignments/asg_fixture/launch-readiness", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("launch readiness status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "assignment not found") {
		t.Fatalf("error body = %s, want scoped assignment not found", rec.Body.String())
	}
}

func TestProjectWorkAPI_AssignmentLaunchReadinessCairnlineSidecarRejectsProjectMismatch(t *testing.T) {
	t.Parallel()
	_, server := newProjectsCairnlineSidecarReadTestServer(t, "full+temp-root+project-route-mismatch")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/work-items/work_fixture/assignments/asg_fixture/launch-readiness", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("launch readiness status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "project not found") {
		t.Fatalf("error body = %s, want scoped project not found", rec.Body.String())
	}
}

func TestProjectWorkAPI_AssignmentLaunchReadinessCairnlineSidecarRejectsLaunchPacketProjectMismatch(t *testing.T) {
	t.Parallel()
	_, server := newProjectsCairnlineSidecarReadTestServer(t, "full+temp-root+launch-packet-project-mismatch")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/work-items/work_fixture/assignments/asg_fixture/launch-readiness", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("launch readiness status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "assignment not found") {
		t.Fatalf("error body = %s, want scoped assignment not found", rec.Body.String())
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
