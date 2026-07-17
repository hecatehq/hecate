package api

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/projectruntime"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/providers"
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
	if _, err := handler.agentProfiles.Create(t.Context(), agentprofiles.Profile{
		ID:                    "browser_review",
		Name:                  "Browser review",
		Surface:               agentprofiles.SurfaceHecateTask,
		ExecutionProfile:      "coding_agent",
		ToolsEnabled:          true,
		WritesAllowed:         true,
		BrowserAllowed:        true,
		BrowserAllowedOrigins: []string{"https://qa.example.test"},
	}); err != nil {
		t.Fatalf("Create browser review preset: %v", err)
	}
	if _, err := handler.projectWork.UpdateRole(t.Context(), "proj_start", "role_backend", func(role *projectwork.AgentRoleProfile) {
		role.DefaultAgentProfile = "browser_review"
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
	if readiness.Data.ProfilePosture == nil || readiness.Data.ProfilePosture.ID != "browser_review" || !readiness.Data.ProfilePosture.ToolsEnabled || !readiness.Data.ProfilePosture.WritesAllowed || readiness.Data.ProfilePosture.NetworkAllowed || readiness.Data.ProfilePosture.BrowserEvidenceStatus != projectAssignmentBrowserEvidenceStatusEnabled || !readiness.Data.ProfilePosture.BrowserAllowed || !reflect.DeepEqual(readiness.Data.ProfilePosture.BrowserAllowedOrigins, []string{"https://qa.example.test"}) {
		t.Fatalf("profile_posture = %+v, want browser-enabled native task posture with tools/writes on and network off", readiness.Data.ProfilePosture)
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

func TestProjectWorkAPI_AssignmentLaunchReadinessAcceptsProviderIDForNamedProvider(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServerWithProviders(&fakeProvider{
		name: "Fake Dogfood",
		capabilities: providers.Capabilities{
			Name:         "Fake Dogfood",
			Kind:         providers.KindCloud,
			DefaultModel: "dogfood-model",
			Models:       []string{"dogfood-model"},
		},
	})
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace:           t.TempDir(),
		Driver:              projectwork.AssignmentDriverHecateTask,
		WithoutRoleDefaults: true,
	})
	if _, err := handler.projects.Update(t.Context(), "proj_start", func(project *projects.Project) {
		project.DefaultProvider = "fake-dogfood"
		project.DefaultModel = "dogfood-model"
	}); err != nil {
		t.Fatalf("Update project defaults: %v", err)
	}

	readiness := mustRequestJSON[ProjectAssignmentLaunchReadinessEnvelope](newAPITestClient(t, server), http.MethodGet, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/launch-readiness", "")
	if !readiness.Data.Ready || readiness.Data.Provider != "fake-dogfood" || readiness.Data.Model != "dogfood-model" {
		t.Fatalf("readiness = %+v, want ready launch using saved provider id", readiness.Data)
	}
	if readiness.Data.ModelReadiness == nil || !readiness.Data.ModelReadiness.Ready || readiness.Data.ModelReadiness.MatchedProvider != "Fake Dogfood" {
		t.Fatalf("model_readiness = %+v, want provider id to resolve to named runtime provider", readiness.Data.ModelReadiness)
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

func TestProjectWorkAPI_AssignmentSurfaceMismatchBlocksHecateBackedLaunch(t *testing.T) {
	t.Parallel()

	handler, server := newProjectWorkTestServer()
	const profileID = "prof_external_only"
	if _, err := handler.agentProfiles.Create(t.Context(), agentprofiles.Profile{
		ID:                profileID,
		Name:              "External only",
		Surface:           agentprofiles.SurfaceExternalAgent,
		ExternalAgentKind: "codex",
	}); err != nil {
		t.Fatalf("Create external-only preset: %v", err)
	}
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace:        t.TempDir(),
		Driver:           projectwork.AssignmentDriverHecateTask,
		Status:           projectwork.AssignmentStatusQueued,
		RoleAgentProfile: profileID,
	})

	path := "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start"
	wantMessage := `agent preset "prof_external_only" targets surface "external_agent"; this assignment requires "hecate_task" or "any"`
	client := newAPITestClient(t, server)
	readiness := mustRequestJSON[ProjectAssignmentLaunchReadinessEnvelope](client, http.MethodGet, path+"/launch-readiness", "")
	if readiness.Data.ReadBackend != "hecate" || readiness.Data.Ready || readiness.Data.Status != projectAssignmentLaunchReadinessStatusBlocked {
		t.Fatalf("readiness = %+v, want blocked Hecate-backed launch", readiness.Data)
	}
	if !containsString(readiness.Data.Blockers, wantMessage) {
		t.Fatalf("readiness blockers = %+v, want %q", readiness.Data.Blockers, wantMessage)
	}

	preflight := mustRequestJSONStatus[projectWorkErrorResponse](client, http.StatusUnprocessableEntity, http.MethodGet, path+"/preflight", "")
	if preflight.Error.Type != errCodeInvalidRequest || preflight.Error.Message != wantMessage {
		t.Fatalf("preflight error = %+v, want typed surface mismatch", preflight.Error)
	}
	start := mustRequestJSONStatus[projectWorkErrorResponse](client, http.StatusUnprocessableEntity, http.MethodPost, path+"/start", `{}`)
	if start.Error.Type != errCodeInvalidRequest || start.Error.Message != wantMessage {
		t.Fatalf("start error = %+v, want typed surface mismatch", start.Error)
	}

	tasks, err := handler.taskStore.ListTasks(t.Context(), taskstateFilterAll())
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks = %+v, want no task created for incompatible preset", tasks)
	}
	sessions, err := handler.agentChat.List(t.Context())
	if err != nil {
		t.Fatalf("List chats: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("chat sessions = %+v, want no chat created for incompatible preset", sessions)
	}
	assignments, err := handler.projectWork.ListAssignments(t.Context(), projectwork.AssignmentFilter{ProjectID: "proj_start", WorkItemID: "work_start"})
	if err != nil {
		t.Fatalf("ListAssignments: %v", err)
	}
	if len(assignments) != 1 || assignments[0].Status != projectwork.AssignmentStatusQueued {
		t.Fatalf("assignments = %+v, want one queued assignment after blocked launch", assignments)
	}
	ref := assignments[0].ExecutionRef
	if ref.TaskID != "" || ref.RunID != "" || ref.ChatSessionID != "" || ref.ContextSnapshotID != "" {
		t.Fatalf("assignment execution ref = %+v, want empty after blocked launch", ref)
	}
}

func TestProjectWorkAPI_AssignmentSurfaceMismatchBlocksStrictEmbeddedCairnlineLaunch(t *testing.T) {
	t.Parallel()

	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend: "cairnline",
			CairnlineReadSource: "embedded",
		},
	}, quietLogger(), nil, nil, nil, nil)
	runner := &fakeAgentChatRunner{}
	handler.SetAgentChatRunner(runner)
	server := NewServer(quietLogger(), handler)

	const (
		projectID    = "proj_surface_mismatch_embedded"
		profileID    = "prof_native_only"
		roleID       = "role_surface_mismatch_embedded"
		workItemID   = "work_surface_mismatch_embedded"
		assignmentID = "asgn_surface_mismatch_embedded"
	)
	workspace := t.TempDir()
	if _, err := handler.agentProfiles.Create(t.Context(), agentprofiles.Profile{
		ID:                profileID,
		Name:              "Native only",
		Surface:           agentprofiles.SurfaceHecateTask,
		ExternalAgentKind: "codex",
	}); err != nil {
		t.Fatalf("Create native-only preset: %v", err)
	}
	if _, err := handler.projectRuntime.UpsertRoleDefaults(t.Context(), projectruntime.RoleDefaults{
		ProjectID:           projectID,
		RoleID:              roleID,
		DefaultAgentProfile: profileID,
	}); err != nil {
		t.Fatalf("Upsert role runtime defaults: %v", err)
	}
	seedCairnlineOnlyProjectWorkGraphForTest(t, handler, cairnline.Project{
		ID:            projectID,
		Name:          "Surface mismatch embedded",
		DefaultRootID: "root_surface_mismatch_embedded",
		Roots: []cairnline.Root{{
			ID:     "root_surface_mismatch_embedded",
			Path:   workspace,
			Kind:   "git",
			Active: true,
		}},
	}, []cairnline.Role{{
		ID:                   roleID,
		ProjectID:            projectID,
		Name:                 "External reviewer",
		DefaultExecutionMode: cairnline.ExecutionExternalAdapter,
	}}, []cairnline.WorkItem{{
		ID:          workItemID,
		ProjectID:   projectID,
		Title:       "Block incompatible external launch",
		Status:      cairnline.WorkStatusReady,
		Priority:    cairnline.PriorityNormal,
		OwnerRoleID: roleID,
		RootID:      "root_surface_mismatch_embedded",
	}}, []cairnline.Assignment{{
		ID:            assignmentID,
		ProjectID:     projectID,
		WorkItemID:    workItemID,
		RoleID:        roleID,
		RootID:        "root_surface_mismatch_embedded",
		ExecutionMode: cairnline.ExecutionExternalAdapter,
	}})
	requireCairnlineOnlyProjectReadsForTest(t, handler, projectID)

	path := "/hecate/v1/projects/" + projectID + "/work-items/" + workItemID + "/assignments/" + assignmentID
	wantMessage := `agent preset "prof_native_only" targets surface "hecate_task"; this assignment requires "external_agent" or "any"`
	client := newAPITestClient(t, server)
	readiness := mustRequestJSON[ProjectAssignmentLaunchReadinessEnvelope](client, http.MethodGet, path+"/launch-readiness", "")
	if readiness.Data.ReadBackend != "cairnline" || readiness.Data.Ready || readiness.Data.Status != projectAssignmentLaunchReadinessStatusBlocked {
		t.Fatalf("readiness = %+v, want blocked strict embedded Cairnline launch", readiness.Data)
	}
	if !containsString(readiness.Data.Blockers, wantMessage) {
		t.Fatalf("readiness blockers = %+v, want %q", readiness.Data.Blockers, wantMessage)
	}

	preflight := mustRequestJSONStatus[projectWorkErrorResponse](client, http.StatusUnprocessableEntity, http.MethodGet, path+"/preflight", "")
	if preflight.Error.Type != errCodeInvalidRequest || preflight.Error.Message != wantMessage {
		t.Fatalf("preflight error = %+v, want typed surface mismatch", preflight.Error)
	}
	start := mustRequestJSONStatus[projectWorkErrorResponse](client, http.StatusUnprocessableEntity, http.MethodPost, path+"/start", `{"driver_kind":"external_agent"}`)
	if start.Error.Type != errCodeInvalidRequest || start.Error.Message != wantMessage {
		t.Fatalf("start error = %+v, want typed surface mismatch", start.Error)
	}

	tasks, err := handler.taskStore.ListTasks(t.Context(), taskstateFilterAll())
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks = %+v, want no task created for incompatible preset", tasks)
	}
	sessions, err := handler.agentChat.List(t.Context())
	if err != nil {
		t.Fatalf("List chats: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("chat sessions = %+v, want no chat created for incompatible preset", sessions)
	}
	if len(runner.prepareRequests) != 0 || len(runner.runRequests) != 0 {
		t.Fatalf("external-agent requests = prepare %+v run %+v, want none", runner.prepareRequests, runner.runRequests)
	}
	if _, ok, err := handler.projectRuntime.Get(t.Context(), projectID, assignmentID); err != nil || ok {
		t.Fatalf("assignment runtime overlay ok=%v err=%v, want none after blocked launch", ok, err)
	}
	assignment := getMirroredCairnlineAssignmentForTest(t, handler, projectID, assignmentID)
	if assignment.Status != cairnline.AssignmentQueued || assignment.ClaimedBy != "" || !assignment.ExecutionRef.Empty() || assignment.ContextSnapshotID != "" {
		t.Fatalf("Cairnline assignment = %+v, want queued and unclaimed after blocked launch", assignment)
	}
}

func TestProjectWorkAPI_AssignmentSurfaceMismatchDoesNotWriteCompatibilityShadows(t *testing.T) {
	t.Parallel()

	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend: "cairnline",
			CairnlineReadSource: "embedded",
		},
	}, quietLogger(), nil, nil, nil, nil)
	runner := &fakeAgentChatRunner{}
	handler.SetAgentChatRunner(runner)
	server := NewServer(quietLogger(), handler)

	const (
		projectID    = "proj_surface_mismatch_compatibility"
		profileID    = "prof_native_only_compatibility"
		roleID       = "role_surface_mismatch_compatibility"
		workItemID   = "work_surface_mismatch_compatibility"
		assignmentID = "asgn_surface_mismatch_compatibility"
	)
	workspace := t.TempDir()
	if _, err := handler.agentProfiles.Create(t.Context(), agentprofiles.Profile{
		ID:                profileID,
		Name:              "Native only compatibility",
		Surface:           agentprofiles.SurfaceHecateTask,
		ExternalAgentKind: "codex",
	}); err != nil {
		t.Fatalf("Create native-only preset: %v", err)
	}
	if _, err := handler.projectRuntime.UpsertRoleDefaults(t.Context(), projectruntime.RoleDefaults{
		ProjectID:           projectID,
		RoleID:              roleID,
		DefaultAgentProfile: profileID,
	}); err != nil {
		t.Fatalf("Upsert role runtime defaults: %v", err)
	}
	seedCairnlineOnlyProjectWorkGraphForTest(t, handler, cairnline.Project{
		ID:            projectID,
		Name:          "Surface mismatch compatibility",
		DefaultRootID: "root_surface_mismatch_compatibility",
		Roots: []cairnline.Root{{
			ID:     "root_surface_mismatch_compatibility",
			Path:   workspace,
			Kind:   "git",
			Active: true,
		}},
	}, []cairnline.Role{{
		ID:                   roleID,
		ProjectID:            projectID,
		Name:                 "External compatibility reviewer",
		DefaultExecutionMode: cairnline.ExecutionExternalAdapter,
	}}, []cairnline.WorkItem{{
		ID:          workItemID,
		ProjectID:   projectID,
		Title:       "Block incompatible compatibility launch",
		Status:      cairnline.WorkStatusReady,
		Priority:    cairnline.PriorityNormal,
		OwnerRoleID: roleID,
		RootID:      "root_surface_mismatch_compatibility",
	}}, []cairnline.Assignment{{
		ID:            assignmentID,
		ProjectID:     projectID,
		WorkItemID:    workItemID,
		RoleID:        roleID,
		RootID:        "root_surface_mismatch_compatibility",
		ExecutionMode: cairnline.ExecutionExternalAdapter,
	}})
	if !handler.projectReadRoutesUseCairnlineReadModel() || !handler.requiresEmbeddedCairnlineProjectReads() {
		t.Fatal("handler is not using embedded Cairnline reads")
	}
	if handler.projectAssignmentStartUsesStrictEmbeddedCairnlineRuntime(true) {
		t.Fatal("strict embedded runtime = true, want compatibility runtime with native project-work store")
	}
	rolesBefore, err := handler.projectWork.ListRoles(t.Context(), projectID)
	if err != nil {
		t.Fatalf("List native roles before launch: %v", err)
	}
	itemsBefore, err := handler.projectWork.ListWorkItems(t.Context(), projectID)
	if err != nil {
		t.Fatalf("List native work items before launch: %v", err)
	}
	assignmentsBefore, err := handler.projectWork.ListAssignments(t.Context(), projectwork.AssignmentFilter{ProjectID: projectID})
	if err != nil {
		t.Fatalf("List native assignments before launch: %v", err)
	}

	path := "/hecate/v1/projects/" + projectID + "/work-items/" + workItemID + "/assignments/" + assignmentID + "/start"
	wantMessage := `agent preset "prof_native_only_compatibility" targets surface "hecate_task"; this assignment requires "external_agent" or "any"`
	start := mustRequestJSONStatus[projectWorkErrorResponse](newAPITestClient(t, server), http.StatusUnprocessableEntity, http.MethodPost, path, `{"driver_kind":"external_agent"}`)
	if start.Error.Type != errCodeInvalidRequest || start.Error.Message != wantMessage {
		t.Fatalf("start error = %+v, want typed surface mismatch", start.Error)
	}

	roles, err := handler.projectWork.ListRoles(t.Context(), projectID)
	if err != nil {
		t.Fatalf("List native roles: %v", err)
	}
	items, err := handler.projectWork.ListWorkItems(t.Context(), projectID)
	if err != nil {
		t.Fatalf("List native work items: %v", err)
	}
	assignments, err := handler.projectWork.ListAssignments(t.Context(), projectwork.AssignmentFilter{ProjectID: projectID})
	if err != nil {
		t.Fatalf("List native assignments: %v", err)
	}
	if !reflect.DeepEqual(roles, rolesBefore) || !reflect.DeepEqual(items, itemsBefore) || !reflect.DeepEqual(assignments, assignmentsBefore) {
		t.Fatalf("native compatibility stores changed after rejected launch: roles before=%+v after=%+v work_items before=%+v after=%+v assignments before=%+v after=%+v", rolesBefore, roles, itemsBefore, items, assignmentsBefore, assignments)
	}
	if len(runner.prepareRequests) != 0 || len(runner.runRequests) != 0 {
		t.Fatalf("external-agent requests = prepare %+v run %+v, want none", runner.prepareRequests, runner.runRequests)
	}
	if _, ok, err := handler.projectRuntime.Get(t.Context(), projectID, assignmentID); err != nil || ok {
		t.Fatalf("assignment runtime overlay ok=%v err=%v, want none after rejected launch", ok, err)
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
