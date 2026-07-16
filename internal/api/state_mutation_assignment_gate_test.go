package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projectassistant"
	"github.com/hecatehq/hecate/internal/projectruntime"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
)

type assignmentMutationFixture struct {
	handler    *Handler
	server     http.Handler
	runner     *concurrentProjectExternalPrepareRunner
	startPath  string
	deletePath string
}

func TestExternalAssignmentChatCreateSerializesWithProjectDelete(t *testing.T) {
	for _, backend := range []struct {
		name    string
		fixture func(*testing.T) assignmentMutationFixture
	}{
		{name: "native project work", fixture: newNativeAssignmentMutationFixture},
		{name: "strict embedded Cairnline", fixture: newStrictCairnlineAssignmentMutationFixture},
	} {
		for _, destructive := range []struct {
			name   string
			method string
			path   func(assignmentMutationFixture) string
		}{
			{name: "project delete", method: http.MethodDelete, path: func(f assignmentMutationFixture) string { return f.deletePath }},
		} {
			t.Run(backend.name+"/"+destructive.name, func(t *testing.T) {
				fixture := backend.fixture(t)
				initialEpoch := fixture.handler.stateMutationGate.snapshot()
				startBody := `{"driver_kind":"external_agent"}`
				startDone := make(chan *httptest.ResponseRecorder, 1)
				go func() {
					startDone <- performRequest(t, fixture.server, http.MethodPost, fixture.startPath, startBody)
				}()
				select {
				case <-fixture.runner.prepareStarted:
				case <-time.After(2 * time.Second):
					t.Fatal("timed out waiting for assignment chat prepare")
				}
				released := false
				defer func() {
					if !released {
						close(fixture.runner.releasePrepare)
					}
				}()

				destructiveDone := make(chan *httptest.ResponseRecorder, 1)
				go func() {
					destructiveDone <- performRequest(t, fixture.server, destructive.method, destructive.path(fixture), "")
				}()
				waitForStateMutationEpochAdvance(t, &fixture.handler.stateMutationGate, initialEpoch)

				lateDone := make(chan *httptest.ResponseRecorder, 1)
				go func() {
					lateDone <- performRequest(t, fixture.server, http.MethodPost, fixture.startPath, startBody)
				}()
				select {
				case late := <-lateDone:
					assertStateMutationChatCreateConflict(t, late)
				case <-fixture.runner.prepareStarted:
					t.Fatal("late assignment launch reached ACP prepare during destructive mutation")
				case <-time.After(2 * time.Second):
					t.Fatal("timed out waiting for late assignment launch conflict")
				}
				select {
				case result := <-destructiveDone:
					t.Fatalf("destructive mutation completed before reserved assignment launch: status=%d body=%s", result.Code, result.Body.String())
				case <-time.After(50 * time.Millisecond):
				}

				close(fixture.runner.releasePrepare)
				released = true
				started := waitForStateMutationRecorder(t, startDone, "assignment launch")
				if started.Code != http.StatusOK {
					t.Fatalf("assignment launch status = %d body=%s, want 200", started.Code, started.Body.String())
				}
				mutation := waitForStateMutationRecorder(t, destructiveDone, destructive.name)
				if mutation.Code != http.StatusOK {
					t.Fatalf("%s status = %d body=%s, want 200", destructive.name, mutation.Code, mutation.Body.String())
				}
				sessions, err := fixture.handler.agentChat.List(t.Context())
				if err != nil {
					t.Fatalf("List chats: %v", err)
				}
				if len(sessions) != 0 {
					t.Fatalf("chats after %s = %+v, want no assignment-chat survivor", destructive.name, sessions)
				}
				if got := fixture.runner.prepareCount(); got != 1 {
					t.Fatalf("prepare requests = %d, want only the reserved assignment launch", got)
				}
				if got := fixture.runner.deletedCount(); got != 1 {
					t.Fatalf("deleted external sessions = %d, want destructive cleanup of linked assignment chat", got)
				}
			})
		}
	}
}

func newNativeAssignmentMutationFixture(t *testing.T) assignmentMutationFixture {
	t.Helper()
	handler := NewHandler(config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetProjectWorkStore(projectwork.NewMemoryStore())
	handler.SetAgentProfileStore(agentprofiles.NewMemoryStore())
	runner := newConcurrentProjectExternalPrepareRunner()
	handler.SetAgentChatRunner(runner)
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace:           t.TempDir(),
		Driver:              projectwork.AssignmentDriverExternalAgent,
		WithoutRoleDefaults: true,
	})
	if _, err := handler.agentProfiles.Create(t.Context(), agentprofiles.Profile{
		ID:                "prof_assignment_mutation",
		Name:              "Assignment mutation external agent",
		Surface:           agentprofiles.SurfaceExternalAgent,
		ExternalAgentKind: "codex",
	}); err != nil {
		t.Fatalf("Create external profile: %v", err)
	}
	if _, err := handler.projectWork.UpdateRole(t.Context(), "proj_start", "role_backend", func(role *projectwork.AgentRoleProfile) {
		role.DefaultAgentProfile = "prof_assignment_mutation"
	}); err != nil {
		t.Fatalf("Update role profile: %v", err)
	}
	return assignmentMutationFixture{
		handler:    handler,
		server:     NewServer(quietLogger(), handler),
		runner:     runner,
		startPath:  "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start",
		deletePath: "/hecate/v1/projects/proj_start",
	}
}

func newStrictCairnlineAssignmentMutationFixture(t *testing.T) assignmentMutationFixture {
	t.Helper()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend:      "cairnline",
			CairnlineConnector:       "embedded",
			CairnlineReadSource:      "embedded",
			CairnlineReplacementMode: "embedded",
		},
	}, quietLogger(), nil, nil, nil, nil)
	// Compatibility stores stay empty. They let project deletion take its
	// Cairnline-only rollback path while strict launch reads and writes remain
	// authoritative in embedded Cairnline.
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetProjectWorkStore(projectwork.NewMemoryStore())
	handler.SetProjectSkillStore(projectskills.NewMemoryStore())
	handler.SetMemoryStore(memory.NewMemoryStore())
	handler.SetProjectAssistantProposalStore(projectassistant.NewMemoryProposalStore())
	handler.SetAgentProfileStore(agentprofiles.NewMemoryStore())
	runner := newConcurrentProjectExternalPrepareRunner()
	handler.SetAgentChatRunner(runner)
	const (
		projectID    = "proj_assignment_mutation_cairnline"
		roleID       = "role_assignment_mutation_cairnline"
		workItemID   = "work_assignment_mutation_cairnline"
		assignmentID = "asgn_assignment_mutation_cairnline"
		rootID       = "root_assignment_mutation_cairnline"
		profileID    = "prof_assignment_mutation_cairnline"
	)
	if !handler.projectAssignmentStartUsesStrictEmbeddedCairnlineRuntime(true) {
		t.Fatal("strict embedded assignment runtime is not active")
	}
	if _, err := handler.agentProfiles.Create(t.Context(), agentprofiles.Profile{
		ID:                profileID,
		Name:              "Strict Cairnline external agent",
		Surface:           agentprofiles.SurfaceExternalAgent,
		ExternalAgentKind: "codex",
	}); err != nil {
		t.Fatalf("Create external profile: %v", err)
	}
	if _, err := handler.projectRuntime.UpsertRoleDefaults(t.Context(), projectruntime.RoleDefaults{
		ProjectID:           projectID,
		RoleID:              roleID,
		DefaultAgentProfile: profileID,
	}); err != nil {
		t.Fatalf("Upsert role defaults: %v", err)
	}
	workspace := t.TempDir()
	seedCairnlineOnlyProjectWorkGraphForTest(
		t,
		handler,
		cairnline.Project{
			ID:            projectID,
			Name:          "Assignment mutation Cairnline",
			DefaultRootID: rootID,
			Roots: []cairnline.Root{{
				ID: rootID, Path: workspace, Kind: "git", Active: true,
			}},
		},
		[]cairnline.Role{{
			ID: roleID, ProjectID: projectID, Name: "External implementer", DefaultExecutionMode: cairnline.ExecutionExternalAdapter,
		}},
		[]cairnline.WorkItem{{
			ID: workItemID, ProjectID: projectID, Title: "Prepare external assignment", Status: cairnline.WorkStatusReady, Priority: cairnline.PriorityNormal, OwnerRoleID: roleID, RootID: rootID,
		}},
		[]cairnline.Assignment{{
			ID: assignmentID, ProjectID: projectID, WorkItemID: workItemID, RoleID: roleID, RootID: rootID, ExecutionMode: cairnline.ExecutionExternalAdapter,
		}},
	)
	startPath := "/hecate/v1/projects/" + projectID + "/work-items/" + workItemID + "/assignments/" + assignmentID + "/start"
	return assignmentMutationFixture{
		handler:    handler,
		server:     NewServer(quietLogger(), handler),
		runner:     runner,
		startPath:  startPath,
		deletePath: "/hecate/v1/projects/" + projectID,
	}
}
