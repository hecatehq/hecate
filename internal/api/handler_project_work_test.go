package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/agentcontrols"
	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/cairnlinebridge"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/chatcontext"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/internal/projectassistant"
	"github.com/hecatehq/hecate/internal/projectruntime"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/projectworkapp"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/internal/storage"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

func newProjectWorkTestServer() (*Handler, http.Handler) {
	handler := NewHandler(config.Config{}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetProjectWorkStore(projectwork.NewMemoryStore())
	handler.SetAgentProfileStore(agentprofiles.NewMemoryStore())
	return handler, NewServer(quietLogger(), handler)
}

func newProjectWorkTestServerWithProviders(items ...providers.Provider) (*Handler, http.Handler) {
	handler := newTestAPIHandlerWithSettings(quietLogger(), items, config.Config{}, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetProjectWorkStore(projectwork.NewMemoryStore())
	return handler, NewServer(quietLogger(), handler)
}

func newProjectWorkCairnlineReadTestServer() (*Handler, http.Handler) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{CoordinationBackend: "cairnline"},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetProjectWorkStore(projectwork.NewMemoryStore())
	handler.SetProjectSkillStore(projectskills.NewMemoryStore())
	handler.SetMemoryStore(memory.NewMemoryStore())
	handler.SetAgentProfileStore(agentprofiles.NewMemoryStore())
	handler.SetProjectAssistantProposalStore(projectassistant.NewMemoryProposalStore())
	return handler, NewServer(quietLogger(), handler)
}

func newProjectWorkCairnlineMirrorTestServer(t *testing.T) (*Handler, http.Handler) {
	t.Helper()
	handler := NewHandler(config.Config{
		Server:   config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{CoordinationBackend: "cairnline"},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetProjectWorkStore(projectwork.NewMemoryStore())
	handler.SetAgentProfileStore(agentprofiles.NewMemoryStore())
	return handler, NewServer(quietLogger(), handler)
}

func newProjectWorkCairnlineCollaborationAuthorityTestServer(t *testing.T) (*Handler, http.Handler) {
	t.Helper()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend:     "cairnline",
			CairnlineWriteAuthority: projectCairnlineWriteAuthorityProjectCollaboration,
		},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetProjectWorkStore(projectwork.NewMemoryStore())
	handler.SetAgentProfileStore(agentprofiles.NewMemoryStore())
	return handler, NewServer(quietLogger(), handler)
}

func newProjectWorkCairnlineWorkItemAuthorityTestServer(t *testing.T) (*Handler, http.Handler) {
	t.Helper()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend:     "cairnline",
			CairnlineWriteAuthority: projectCairnlineWriteAuthorityProjectWorkItems,
		},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetProjectWorkStore(projectwork.NewMemoryStore())
	handler.SetAgentProfileStore(agentprofiles.NewMemoryStore())
	return handler, NewServer(quietLogger(), handler)
}

func newProjectWorkCairnlineRoleAuthorityTestServer(t *testing.T) (*Handler, http.Handler) {
	t.Helper()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend:     "cairnline",
			CairnlineWriteAuthority: projectCairnlineWriteAuthorityProjectRoles,
		},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetProjectWorkStore(projectwork.NewMemoryStore())
	handler.SetAgentProfileStore(agentprofiles.NewMemoryStore())
	return handler, NewServer(quietLogger(), handler)
}

func newProjectWorkCairnlineAssignmentAuthorityTestServer(t *testing.T) (*Handler, http.Handler) {
	t.Helper()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend:     "cairnline",
			CairnlineWriteAuthority: projectCairnlineWriteAuthorityProjectAssignments,
		},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetProjectWorkStore(projectwork.NewMemoryStore())
	handler.SetAgentProfileStore(agentprofiles.NewMemoryStore())
	return handler, NewServer(quietLogger(), handler)
}

func seedCairnlineOnlyProjectWorkGraphForTest(t *testing.T, handler *Handler, project cairnline.Project, roles []cairnline.Role, workItems []cairnline.WorkItem, assignments []cairnline.Assignment) {
	t.Helper()
	if err := handler.withCairnlineEmbeddedMirrorService(t.Context(), func(service *cairnline.Service) error {
		if _, err := service.CreateProject(t.Context(), project); err != nil {
			return err
		}
		for _, role := range roles {
			if _, err := service.CreateRole(t.Context(), role); err != nil {
				return err
			}
		}
		for _, item := range workItems {
			if _, err := service.CreateWorkItem(t.Context(), item); err != nil {
				return err
			}
		}
		for _, assignment := range assignments {
			if _, err := service.CreateAssignment(t.Context(), assignment); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed Cairnline-only project graph: %v", err)
	}
	if handler.projects != nil {
		if _, ok, err := handler.projects.Get(t.Context(), project.ID); err != nil || ok {
			t.Fatalf("native project exists = %t err=%v, want missing native project", ok, err)
		}
	}
}

func requireCairnlineOnlyProjectReadsForTest(t *testing.T, handler *Handler, projectID string) {
	t.Helper()
	if handler == nil {
		t.Fatal("handler is nil")
	}
	projectID = strings.TrimSpace(projectID)
	if handler.projects != nil && projectID != "" {
		if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
			t.Fatalf("native project exists = %t err=%v, want missing native project", ok, err)
		}
	}
	disableNativeProjectStoresForTest(t, handler)
}

func disableNativeProjectStoresForTest(t *testing.T, handler *Handler) {
	t.Helper()
	if handler == nil {
		t.Fatal("handler is nil")
	}
	handler.projects = nil
	handler.projectWork = nil
	handler.projectSkills = nil
	handler.memory = nil
	handler.memoryCandidates = nil
}

type projectWorkErrorResponse struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func newProjectWorkCairnlineReadSourceTestHandler(t *testing.T, source string) *Handler {
	t.Helper()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend: "cairnline",
			CairnlineReadSource: source,
		},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetProjectWorkStore(projectwork.NewMemoryStore())
	handler.SetProjectSkillStore(projectskills.NewMemoryStore())
	handler.SetMemoryStore(memory.NewMemoryStore())
	handler.SetAgentProfileStore(agentprofiles.NewMemoryStore())
	handler.SetProjectAssistantProposalStore(projectassistant.NewMemoryProposalStore())
	if _, err := handler.projects.Create(t.Context(), projects.Project{
		ID:   "proj_read_source",
		Name: "Read source",
	}); err != nil {
		t.Fatalf("create read-source project: %v", err)
	}
	return handler
}

func TestProjectWorkAPI_CairnlineReadSourceSnapshotUsesSeededBridge(t *testing.T) {
	t.Parallel()
	handler := newProjectWorkCairnlineReadSourceTestHandler(t, "snapshot")
	if handler.prefersEmbeddedCairnlineProjectReads() {
		t.Fatal("prefersEmbeddedCairnlineProjectReads() = true, want false for snapshot read source")
	}

	view, err := handler.cairnlineProjectWorkView(t.Context(), "proj_read_source")
	if err != nil {
		t.Fatalf("cairnlineProjectWorkView(snapshot) error = %v, want nil", err)
	}
	defer view.Close()
	if _, err := view.service.GetProject(t.Context(), "proj_read_source"); err != nil {
		t.Fatalf("GetProject from snapshot-seeded view error = %v, want nil", err)
	}
}

func TestProjectWorkAPI_CairnlineReadSourceAutoFallsBackWhenMirrorMissing(t *testing.T) {
	t.Parallel()
	handler := newProjectWorkCairnlineReadSourceTestHandler(t, "auto")
	if !handler.prefersEmbeddedCairnlineProjectReads() {
		t.Fatal("prefersEmbeddedCairnlineProjectReads() = false, want true for auto read source with data dir")
	}

	view, err := handler.cairnlineProjectWorkView(t.Context(), "proj_read_source")
	if err != nil {
		t.Fatalf("cairnlineProjectWorkView(auto) error = %v, want nil fallback", err)
	}
	defer view.Close()
	if _, err := view.service.GetProject(t.Context(), "proj_read_source"); err != nil {
		t.Fatalf("GetProject from auto fallback view error = %v, want nil", err)
	}
}

func TestProjectWorkAPI_CairnlineReadSourceEmbeddedRequiresMirror(t *testing.T) {
	t.Parallel()
	handler := newProjectWorkCairnlineReadSourceTestHandler(t, "embedded")
	if !handler.requiresEmbeddedCairnlineProjectReads() {
		t.Fatal("requiresEmbeddedCairnlineProjectReads() = false, want true for embedded read source")
	}
	if !handler.projectReadRoutesUseCairnlineReadModel() {
		t.Fatal("projectReadRoutesUseCairnlineReadModel() = false, want true for strict embedded read source")
	}

	if _, err := handler.cairnlineProjectWorkView(t.Context(), "proj_read_source"); !errors.Is(err, cairnline.ErrNotFound) {
		t.Fatalf("cairnlineProjectWorkView(embedded missing db) error = %v, want ErrNotFound", err)
	}
	if err := os.MkdirAll(filepath.Dir(handler.cairnlineEmbeddedDatabasePath()), 0o700); err != nil {
		t.Fatalf("create embedded Cairnline mirror parent: %v", err)
	}
	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("create empty embedded Cairnline mirror: %v", err)
	}
	if _, err := service.ListProjects(t.Context()); err != nil {
		store.Close()
		t.Fatalf("ListProjects on empty embedded Cairnline mirror: %v", err)
	}
	store.Close()
	if _, err := handler.cairnlineProjectWorkView(t.Context(), "proj_read_source"); !errors.Is(err, cairnline.ErrNotFound) {
		t.Fatalf("cairnlineProjectWorkView(embedded missing project) error = %v, want ErrNotFound", err)
	}
}

func TestProjectWorkAPI_CairnlineReadSourceEmbeddedUsesCairnlineOnlyProject(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend: "cairnline",
			CairnlineReadSource: "embedded",
		},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	if err := os.MkdirAll(filepath.Dir(handler.cairnlineEmbeddedDatabasePath()), 0o700); err != nil {
		t.Fatalf("create embedded Cairnline mirror parent: %v", err)
	}
	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("create embedded Cairnline mirror: %v", err)
	}
	defer store.Close()
	if _, err := service.CreateProject(t.Context(), cairnline.Project{
		ID:   "proj_embedded_only",
		Name: "Embedded only",
	}); err != nil {
		t.Fatalf("CreateProject(cairnline only): %v", err)
	}
	if _, ok, err := handler.projects.Get(t.Context(), "proj_embedded_only"); err != nil || ok {
		t.Fatalf("native project exists = %t err=%v, want missing native project", ok, err)
	}

	view, err := handler.cairnlineProjectWorkView(t.Context(), "proj_embedded_only")
	if err != nil {
		t.Fatalf("cairnlineProjectWorkView(embedded only) error = %v, want nil", err)
	}
	defer view.Close()
	if view.snapshot.Project.ID != "proj_embedded_only" || view.snapshot.Project.Name != "Embedded only" {
		t.Fatalf("snapshot project = %+v, want embedded Cairnline project", view.snapshot.Project)
	}
}

func TestProjectWorkAPI_CairnlineReadSourceEmbeddedRoutesUseCairnlineOnlyProject(t *testing.T) {
	t.Parallel()
	const projectID = "proj_embedded_route"
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend: "cairnline",
			CairnlineReadSource: "embedded",
		},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetProjectWorkStore(projectwork.NewMemoryStore())
	handler.SetProjectSkillStore(projectskills.NewMemoryStore())
	handler.SetMemoryStore(memory.NewMemoryStore())
	handler.SetAgentProfileStore(agentprofiles.NewMemoryStore())
	handler.SetProjectAssistantProposalStore(projectassistant.NewMemoryProposalStore())

	if err := os.MkdirAll(filepath.Dir(handler.cairnlineEmbeddedDatabasePath()), 0o700); err != nil {
		t.Fatalf("create embedded Cairnline mirror parent: %v", err)
	}
	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("create embedded Cairnline mirror: %v", err)
	}
	defer store.Close()
	if _, err := service.CreateProject(t.Context(), cairnline.Project{
		ID:          projectID,
		Name:        "Embedded route",
		Description: "Project exists only in embedded Cairnline.",
	}); err != nil {
		t.Fatalf("CreateProject(cairnline only): %v", err)
	}
	if _, err := service.CreateRole(t.Context(), cairnline.Role{
		ID:                   "role_embedded_route",
		ProjectID:            projectID,
		Name:                 "Embedded route role",
		DefaultExecutionMode: cairnline.ExecutionOrchestrated,
	}); err != nil {
		t.Fatalf("CreateRole(cairnline only): %v", err)
	}
	if _, err := service.CreateWorkItem(t.Context(), cairnline.WorkItem{
		ID:          "work_embedded_route",
		ProjectID:   projectID,
		Title:       "Read embedded route work",
		Brief:       "Served from the embedded Cairnline project graph.",
		Status:      cairnline.WorkStatusReady,
		Priority:    cairnline.PriorityNormal,
		OwnerRoleID: "role_embedded_route",
	}); err != nil {
		t.Fatalf("CreateWorkItem(cairnline only): %v", err)
	}
	if _, err := service.CreateAssignment(t.Context(), cairnline.Assignment{
		ID:            "asgn_embedded_route",
		ProjectID:     projectID,
		WorkItemID:    "work_embedded_route",
		RoleID:        "role_embedded_route",
		ExecutionMode: cairnline.ExecutionOrchestrated,
		Status:        cairnline.AssignmentQueued,
	}); err != nil {
		t.Fatalf("CreateAssignment(cairnline only): %v", err)
	}
	if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("native project exists = %t err=%v, want missing native project", ok, err)
	}

	client := newAPITestClient(t, NewServer(quietLogger(), handler))
	listed := mustRequestJSON[ProjectWorkItemsResponse](client, http.MethodGet, "/hecate/v1/projects/"+projectID+"/work-items", "")
	listedItem := findProjectWorkItemForTest(t, listed.Data, "work_embedded_route")
	if listedItem.ProjectID != projectID || listedItem.ReadBackend != "cairnline" || len(listedItem.Assignments) != 1 || listedItem.Assignments[0].ID != "asgn_embedded_route" {
		t.Fatalf("listed work item = %+v, want Cairnline-only project work graph", listedItem)
	}
	if listedItem.Assignments[0].ReadBackend != "cairnline" || listedItem.Assignments[0].Status != projectwork.AssignmentStatusQueued {
		t.Fatalf("listed assignment = %+v, want queued Cairnline assignment", listedItem.Assignments[0])
	}

	detail := mustRequestJSON[ProjectWorkItemEnvelope](client, http.MethodGet, "/hecate/v1/projects/"+projectID+"/work-items/work_embedded_route", "")
	if detail.Data.ReadBackend != "cairnline" || detail.Data.Title != "Read embedded route work" || len(detail.Data.Assignments) != 1 {
		t.Fatalf("work item detail = %+v, want Cairnline-only detail projection", detail.Data)
	}

	assignments := mustRequestJSON[ProjectWorkAssignmentsResponse](client, http.MethodGet, "/hecate/v1/projects/"+projectID+"/work-items/work_embedded_route/assignments", "")
	if len(assignments.Data) != 1 || assignments.Data[0].ID != "asgn_embedded_route" || assignments.Data[0].ReadBackend != "cairnline" {
		t.Fatalf("assignment list = %+v, want Cairnline-only assignment projection", assignments.Data)
	}

	readiness := mustRequestJSON[ProjectWorkItemReadinessEnvelope](client, http.MethodGet, "/hecate/v1/projects/"+projectID+"/work-items/work_embedded_route/readiness", "")
	if readiness.Data.ReadBackend != "cairnline" || readiness.Data.WorkItemID != "work_embedded_route" || readiness.Data.Ready {
		t.Fatalf("readiness = %+v, want blocked Cairnline-only closeout readiness", readiness.Data)
	}

	activity := mustRequestJSON[ProjectActivityEnvelope](client, http.MethodGet, "/hecate/v1/projects/"+projectID+"/activity", "")
	if activity.Data.ReadBackend != "cairnline" || activity.Data.Summary.WorkItemCount != 1 || activity.Data.Summary.AssignmentCount != 1 {
		t.Fatalf("activity = %+v, want Cairnline-only project activity", activity.Data)
	}
	item := findProjectActivityItemForTest(t, activity.Data.Buckets.Blocked, "asgn_embedded_route")
	if item.WorkItem.ID != "work_embedded_route" || item.Role.ID != "role_embedded_route" {
		t.Fatalf("activity item = %+v, want Cairnline-only work/role projection", item)
	}
}

type launchContextContract struct {
	Sections []string            `json:"sections"`
	Fields   map[string][]string `json:"fields"`
}

func loadLaunchContextContract(t *testing.T) launchContextContract {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "ui", "src", "test", "fixtures", "launch-context-v1-contract.json"))
	if err != nil {
		t.Fatalf("Read launch context contract: %v", err)
	}
	var contract launchContextContract
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatalf("Decode launch context contract: %v", err)
	}
	return contract
}

func assertLaunchContextContract(t *testing.T, text string) {
	t.Helper()
	contract := loadLaunchContextContract(t)
	for _, section := range contract.Sections {
		if section == "Project" {
			if !strings.Contains(text, "Project:") {
				t.Fatalf("launch context missing project label: %q", text)
			}
			continue
		}
		if !strings.Contains(text, section) {
			t.Fatalf("launch context missing section %q: %q", section, text)
		}
	}
	for _, fields := range contract.Fields {
		for _, field := range fields {
			if !strings.Contains(text, "- "+field+":") {
				t.Fatalf("launch context missing field %q: %q", field, text)
			}
		}
	}
}

func TestProjectWorkAPI_CRUD(t *testing.T) {
	t.Parallel()
	_, server := newProjectWorkTestServer()
	project := createProjectForWorkTest(t, server)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+project.Data.ID+"/roles", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("roles status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var roles ProjectWorkRolesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &roles); err != nil {
		t.Fatalf("decode roles: %v", err)
	}
	if len(roles.Data) < 8 || !projectWorkRoleExists(roles.Data, "product_manager", true) {
		t.Fatalf("roles = %+v, want built-ins", roles.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/roles", bytes.NewReader([]byte(`{
		"id":"role_release",
		"name":"Release captain",
		"description":"Coordinates release work",
		"instructions":"Keep release notes current.",
		"default_driver_kind":"external_agent",
		"default_provider":"anthropic",
		"default_model":"claude-sonnet-4",
		"default_agent_profile":"safe_external_review"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create role status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var role ProjectWorkRoleEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &role); err != nil {
		t.Fatalf("decode role: %v", err)
	}
	if role.Data.ID != "role_release" || role.Data.BuiltIn {
		t.Fatalf("created role = %+v, want custom role", role.Data)
	}
	if role.Data.DefaultDriverKind != projectwork.AssignmentDriverExternalAgent || role.Data.DefaultProvider != "anthropic" || role.Data.DefaultModel != "claude-sonnet-4" || role.Data.DefaultAgentProfile != "safe_external_review" {
		t.Fatalf("created role defaults = %+v, want role execution defaults", role.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+project.Data.ID+"/roles/product_manager", bytes.NewReader([]byte(`{"name":"Override"}`))))
	if rec.Code != http.StatusConflict {
		t.Fatalf("patch built-in role status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/work-items", bytes.NewReader([]byte(`{
		"id":"work_backend",
		"title":"Backend substrate",
		"brief":"Persist coordination metadata only.",
		"priority":"high",
		"owner_role_id":"software_developer",
		"reviewer_role_ids":["reviewer_qa","architect"]
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create work item status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var work ProjectWorkItemEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &work); err != nil {
		t.Fatalf("decode work item: %v", err)
	}
	if work.Data.Status != projectwork.WorkItemStatusBacklog || work.Data.Priority != "high" {
		t.Fatalf("created work item = %+v, want backlog/high", work.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/work-items", bytes.NewReader([]byte(`{
		"id":"work_backend",
		"title":"Duplicate backend substrate"
	}`))))
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate work item status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_backend", bytes.NewReader([]byte(`{"status":"ready","reviewer_role_ids":["reviewer_qa"]}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("patch work item status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &work); err != nil {
		t.Fatalf("decode patched work item: %v", err)
	}
	if work.Data.Status != projectwork.WorkItemStatusReady || len(work.Data.ReviewerRoleIDs) != 1 {
		t.Fatalf("patched work item = %+v, want ready with one reviewer", work.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_backend/assignments", bytes.NewReader([]byte(`{
		"id":"asgn_backend",
		"role_id":"software_developer",
		"execution_ref":{
			"kind":"task_run",
			"task_id":"task_123",
			"run_id":"run_123",
			"context_snapshot_id":"ctx_123"
		}
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create assignment status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var assignment ProjectWorkAssignmentEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &assignment); err != nil {
		t.Fatalf("decode assignment: %v", err)
	}
	if assignment.Data.Status != projectwork.AssignmentStatusQueued || assignmentExecutionRefForTest(t, assignment.Data).TaskID != "task_123" {
		t.Fatalf("assignment = %+v, want queued linked task", assignment.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+project.Data.ID+"/work-items", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list work items with assignment summaries status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var listedWork ProjectWorkItemsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listedWork); err != nil {
		t.Fatalf("decode listed work items: %v", err)
	}
	if len(listedWork.Data) != 1 || len(listedWork.Data[0].Assignments) != 1 || listedWork.Data[0].Assignments[0].ID != "asgn_backend" {
		t.Fatalf("listed work assignments = %+v, want projected assignment summary", listedWork.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_backend/assignments/asgn_backend", bytes.NewReader([]byte(`{
		"status":"completed",
		"execution_ref":{
			"kind":"chat_session",
			"chat_session_id":"chat_123",
			"message_id":"msg_123"
		}
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("patch assignment status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &assignment); err != nil {
		t.Fatalf("decode patched assignment: %v", err)
	}
	if assignment.Data.Status != projectwork.AssignmentStatusCompleted || assignmentExecutionRefForTest(t, assignment.Data).ChatSessionID != "chat_123" {
		t.Fatalf("patched assignment = %+v, want completed linked chat", assignment.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_backend/artifacts", bytes.NewReader([]byte(`{
		"id":"art_handoff",
		"assignment_id":"asgn_backend",
		"kind":"handoff",
		"title":"Backend handoff",
		"body":"Store and API are ready for UI wiring.",
		"author_role_id":"software_developer"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create artifact status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var artifact ProjectWorkArtifactEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &artifact); err != nil {
		t.Fatalf("decode artifact: %v", err)
	}
	if artifact.Data.Kind != projectwork.ArtifactKindHandoff || artifact.Data.AssignmentID != "asgn_backend" {
		t.Fatalf("artifact = %+v, want handoff linked to assignment", artifact.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_backend/artifacts", bytes.NewReader([]byte(`{
		"id":"art_review",
		"assignment_id":"asgn_backend",
		"reviewed_assignment_id":"asgn_backend",
		"kind":"review",
		"title":"Backend review",
		"body":"Verdict: Changes requested",
		"author_role_id":"reviewer_qa",
		"review_verdict":"changes_requested",
		"review_risk":"medium",
		"review_follow_up_required":true
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create review artifact status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &artifact); err != nil {
		t.Fatalf("decode review artifact: %v", err)
	}
	if artifact.Data.ReviewedAssignmentID != "asgn_backend" || artifact.Data.ReviewVerdict != "changes_requested" || artifact.Data.ReviewRisk != "medium" || !artifact.Data.ReviewFollowUpRequired {
		t.Fatalf("review artifact = %+v, want structured review fields", artifact.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_backend/handoffs", bytes.NewReader([]byte(`{
		"id":"handoff_backend",
		"source_assignment_id":"asgn_backend",
		"source_run_id":"run_123",
		"source_chat_session_id":"chat_123",
		"source_message_id":"msg_123",
		"target_role_id":"reviewer_qa",
		"title":"Review backend substrate",
		"summary":"Store and API are ready for operator review.",
		"recommended_next_action":"Review the tests and start a QA assignment if acceptable.",
		"linked_artifact_ids":["art_handoff","art_handoff"],
		"linked_memory_ids":["mem_123"],
		"context_refs":["ctx_123"],
		"provenance_kind":"agent_draft",
		"trust_label":"operator_reviewed",
		"created_by_role_id":"software_developer"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create handoff status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var handoff ProjectHandoffEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &handoff); err != nil {
		t.Fatalf("decode handoff: %v", err)
	}
	if handoff.Data.Status != projectwork.HandoffStatusPending || handoff.Data.TargetRoleID != "reviewer_qa" || len(handoff.Data.LinkedArtifactIDs) != 1 {
		t.Fatalf("handoff = %+v, want pending structured handoff", handoff.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_backend/handoffs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list handoffs status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var handoffs ProjectHandoffsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &handoffs); err != nil {
		t.Fatalf("decode handoffs: %v", err)
	}
	if len(handoffs.Data) != 1 || handoffs.Data[0].ID != "handoff_backend" {
		t.Fatalf("handoffs = %+v, want created handoff", handoffs.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_backend/handoffs/handoff_backend/status", bytes.NewReader([]byte(`{"status":"accepted"}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("accept handoff status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &handoff); err != nil {
		t.Fatalf("decode accepted handoff: %v", err)
	}
	if handoff.Data.Status != projectwork.HandoffStatusAccepted || handoff.Data.StatusChangedAt == "" {
		t.Fatalf("accepted handoff = %+v, want status timestamp", handoff.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+project.Data.ID+"/handoffs?work_item_id=work_backend&status=accepted", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("project handoffs status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &handoffs); err != nil {
		t.Fatalf("decode project handoffs: %v", err)
	}
	if len(handoffs.Data) != 1 || handoffs.Data[0].ID != "handoff_backend" || handoffs.Data[0].Status != projectwork.HandoffStatusAccepted {
		t.Fatalf("project handoffs = %+v, want accepted handoff", handoffs.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_backend/handoffs/handoff_backend", bytes.NewReader([]byte(`{"target_assignment_id":"asgn_backend","recommended_next_action":"Start the linked follow-up assignment."}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("patch handoff status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &handoff); err != nil {
		t.Fatalf("decode patched handoff: %v", err)
	}
	if handoff.Data.TargetAssignmentID != "asgn_backend" || handoff.Data.RecommendedNextAction != "Start the linked follow-up assignment." {
		t.Fatalf("patched handoff = %+v, want linked target assignment", handoff.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_backend/assignments", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list assignments status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var assignments ProjectWorkAssignmentsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &assignments); err != nil {
		t.Fatalf("decode assignments: %v", err)
	}
	if len(assignments.Data) != 1 || assignments.Data[0].ID != "asgn_backend" {
		t.Fatalf("assignments = %+v, want created assignment", assignments.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+project.Data.ID+"/activity", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("activity with handoff status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var activity ProjectActivityEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &activity); err != nil {
		t.Fatalf("decode activity: %v", err)
	}
	if len(activity.Data.Recent) == 0 || activity.Data.Recent[0].HandoffSummary.Count != 1 || activity.Data.Recent[0].HandoffSummary.LatestStatus != projectwork.HandoffStatusAccepted {
		t.Fatalf("activity handoff summary = %+v, want accepted handoff signal", activity.Data.Recent)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/work-items", bytes.NewReader([]byte(`{
		"id":"work_other",
		"title":"Other work"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create other work item status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_other/assignments/asgn_backend", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete assignment with wrong work item status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_backend/handoffs/handoff_backend", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete handoff status = %d body=%s, want 204", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_backend/assignments/asgn_backend", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete assignment status = %d body=%s, want 204", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_backend/assignments", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list assignments after delete status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &assignments); err != nil {
		t.Fatalf("decode assignments after delete: %v", err)
	}
	if len(assignments.Data) != 0 {
		t.Fatalf("assignments after delete = %+v, want none", assignments.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_backend/assignments", bytes.NewReader([]byte(`{
		"id":"asgn_backend",
		"role_id":"software_developer"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("recreate assignment status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_backend/artifacts", bytes.NewReader([]byte(`{
		"id":"art_handoff",
		"assignment_id":"asgn_backend",
		"kind":"handoff",
		"title":"Backend handoff",
		"body":"Store and API are ready for UI wiring.",
		"author_role_id":"software_developer"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("recreate artifact status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_backend/artifacts", bytes.NewReader([]byte(`{
		"id":"art_evidence",
		"kind":"evidence_link",
		"title":"Operator source document",
		"body":"Source document used to validate the work item outcome.",
		"evidence_source_kind":"source_document",
		"evidence_url":"https://example.invalid/docs/hecate-work",
		"evidence_external_id":"DOC-42",
		"evidence_provider":"docs",
		"evidence_trust_label":"operator_provided"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create evidence artifact status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_backend/artifacts", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list artifacts status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var artifacts ProjectWorkArtifactsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &artifacts); err != nil {
		t.Fatalf("decode artifacts: %v", err)
	}
	if len(artifacts.Data) != 2 || artifacts.Data[0].ID != "art_handoff" || artifacts.Data[1].ID != "art_evidence" {
		t.Fatalf("artifacts = %+v, want handoff and evidence artifacts", artifacts.Data)
	}
	if artifacts.Data[1].Kind != projectwork.ArtifactKindEvidenceLink || artifacts.Data[1].EvidenceURL == "" || artifacts.Data[1].EvidenceExternalID != "DOC-42" || artifacts.Data[1].EvidenceProvider != "docs" {
		t.Fatalf("evidence artifact = %+v, want evidence metadata", artifacts.Data[1])
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_backend", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete work item status = %d body=%s, want 204", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_backend", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get deleted work item status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
}

func TestProjectWorkAPI_CairnlineCollaborationAuthorityWritesCairnlineAndShadowsHecate(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkCairnlineCollaborationAuthorityTestServer(t)
	client := newAPITestClient(t, server)
	project := mustRequestJSONStatus[ProjectResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects", projectJourneyJSON(t, map[string]any{
		"name": "Authority project",
	}))
	projectID := project.Data.ID

	mustRequestJSONStatus[ProjectWorkRoleEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/roles", projectJourneyJSON(t, map[string]any{
		"id":   "role_authority",
		"name": "Authority role",
	}))
	mustRequestJSONStatus[ProjectWorkItemEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items", projectJourneyJSON(t, map[string]any{
		"id":            "work_authority",
		"title":         "Exercise authority",
		"owner_role_id": "role_authority",
	}))
	mustRequestJSONStatus[ProjectWorkAssignmentEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_authority/assignments", projectJourneyJSON(t, map[string]any{
		"id":      "asgn_authority",
		"role_id": "role_authority",
	}))

	decision := mustRequestJSONStatus[ProjectWorkArtifactEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_authority/artifacts", projectJourneyJSON(t, map[string]any{
		"id":             "art_authority_decision",
		"assignment_id":  "asgn_authority",
		"kind":           "decision_note",
		"title":          "Authority decision",
		"body":           "Cairnline should store this first.",
		"author_role_id": "role_authority",
	}))
	if decision.Data.ID != "art_authority_decision" || decision.Data.Kind != projectwork.ArtifactKindDecisionNote {
		t.Fatalf("decision artifact = %+v, want decision note response", decision.Data)
	}
	mirroredDecision := getMirroredCairnlineArtifactForTest(t, handler, projectID, "work_authority", "art_authority_decision")
	if mirroredDecision.AssignmentID != "asgn_authority" || mirroredDecision.AuthorRoleID != "role_authority" {
		t.Fatalf("mirrored decision = %+v, want Cairnline authority record", mirroredDecision)
	}
	assertHecateShadowArtifactForTest(t, handler, projectID, "work_authority", "art_authority_decision", projectwork.ArtifactKindDecisionNote)

	evidence := mustRequestJSONStatus[ProjectWorkArtifactEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_authority/artifacts", projectJourneyJSON(t, map[string]any{
		"id":                   "art_authority_evidence",
		"assignment_id":        "asgn_authority",
		"kind":                 "evidence_link",
		"body":                 "External evidence was attached.",
		"evidence_url":         "https://example.invalid/authority",
		"evidence_external_id": "AUTH-1",
		"evidence_provider":    "example",
	}))
	if evidence.Data.Title != "Evidence link" || evidence.Data.EvidenceURL != "https://example.invalid/authority" {
		t.Fatalf("evidence response = %+v, want defaulted title and locator metadata", evidence.Data)
	}
	mirroredEvidence := getMirroredCairnlineEvidenceForTest(t, handler, projectID, "work_authority", "art_authority_evidence")
	if mirroredEvidence.Locator != "https://example.invalid/authority" || mirroredEvidence.ExternalID != "AUTH-1" || mirroredEvidence.Provider != "example" {
		t.Fatalf("mirrored evidence = %+v, want Cairnline evidence metadata", mirroredEvidence)
	}
	assertHecateShadowArtifactForTest(t, handler, projectID, "work_authority", "art_authority_evidence", projectwork.ArtifactKindEvidenceLink)

	missingVerdict := mustRequestJSONStatus[projectWorkErrorResponse](client, http.StatusBadRequest, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_authority/artifacts", projectJourneyJSON(t, map[string]any{
		"id":                     "art_authority_review_missing_verdict",
		"assignment_id":          "asgn_authority",
		"reviewed_assignment_id": "asgn_authority",
		"kind":                   "review",
		"body":                   "Missing verdict.",
	}))
	if missingVerdict.Error.Type != errCodeInvalidRequest || !strings.Contains(missingVerdict.Error.Message, "review_verdict is required") {
		t.Fatalf("missing verdict error = %+v, want invalid review verdict response", missingVerdict.Error)
	}
	missingAssignment := mustRequestJSONStatus[projectWorkErrorResponse](client, http.StatusNotFound, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_authority/artifacts", projectJourneyJSON(t, map[string]any{
		"id":            "art_authority_missing_assignment",
		"assignment_id": "asgn_missing",
		"kind":          "decision_note",
		"title":         "Missing assignment",
		"body":          "This artifact references an assignment that does not exist.",
	}))
	if missingAssignment.Error.Type != errCodeNotFound || !strings.Contains(missingAssignment.Error.Message, "assignment not found") {
		t.Fatalf("missing assignment error = %+v, want assignment dependency error", missingAssignment.Error)
	}

	review := mustRequestJSONStatus[ProjectWorkArtifactEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_authority/artifacts", projectJourneyJSON(t, map[string]any{
		"id":                     "art_authority_review",
		"assignment_id":          "asgn_authority",
		"reviewed_assignment_id": "asgn_authority",
		"kind":                   "review",
		"body":                   "Needs follow-up.",
		"author_role_id":         "role_authority",
		"review_verdict":         "changes_requested",
		"review_risk":            "medium",
	}))
	if review.Data.ReviewVerdict != projectwork.ReviewVerdictChangesRequested || !review.Data.ReviewFollowUpRequired {
		t.Fatalf("review response = %+v, want changes-requested follow-up", review.Data)
	}
	mirroredReview := getMirroredCairnlineReviewForTest(t, handler, projectID, "work_authority", "art_authority_review")
	if mirroredReview.Verdict != cairnline.ReviewVerdictChangesRequested || mirroredReview.Risk != cairnline.ReviewRiskMedium {
		t.Fatalf("mirrored review = %+v, want Cairnline review metadata", mirroredReview)
	}
	assertHecateShadowArtifactForTest(t, handler, projectID, "work_authority", "art_authority_review", projectwork.ArtifactKindReview)

	missingRole := mustRequestJSONStatus[projectWorkErrorResponse](client, http.StatusNotFound, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_authority/handoffs", projectJourneyJSON(t, map[string]any{
		"id":                      "handoff_authority_missing_role",
		"source_assignment_id":    "asgn_authority",
		"target_role_id":          "role_missing",
		"title":                   "Missing target role",
		"summary":                 "This handoff references a role that does not exist.",
		"recommended_next_action": "Find a valid target role.",
	}))
	if missingRole.Error.Type != errCodeNotFound || !strings.Contains(missingRole.Error.Message, "role not found") {
		t.Fatalf("missing role error = %+v, want role dependency error", missingRole.Error)
	}

	handoff := mustRequestJSONStatus[ProjectHandoffEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_authority/handoffs", projectJourneyJSON(t, map[string]any{
		"id":                      "handoff_authority",
		"source_assignment_id":    "asgn_authority",
		"target_role_id":          "role_authority",
		"title":                   "Authority handoff",
		"summary":                 "Cairnline should own this handoff.",
		"recommended_next_action": "Accept the handoff.",
		"created_by_role_id":      "role_authority",
	}))
	if handoff.Data.Status != projectwork.HandoffStatusPending {
		t.Fatalf("handoff response = %+v, want Hecate pending status projection", handoff.Data)
	}
	mirroredHandoff := getMirroredCairnlineHandoffForTest(t, handler, projectID, "work_authority", "handoff_authority")
	if mirroredHandoff.Status != cairnline.HandoffStatusOpen || mirroredHandoff.ToRoleID != "role_authority" {
		t.Fatalf("mirrored handoff = %+v, want open Cairnline handoff", mirroredHandoff)
	}
	assertHecateShadowHandoffStatusForTest(t, handler, projectID, "work_authority", "handoff_authority", projectwork.HandoffStatusPending)

	updated := mustRequestJSONStatus[ProjectHandoffEnvelope](client, http.StatusOK, http.MethodPatch, "/hecate/v1/projects/"+projectID+"/work-items/work_authority/handoffs/handoff_authority", projectJourneyJSON(t, map[string]any{
		"summary":                 "Cairnline updated this handoff.",
		"recommended_next_action": "Finish it.",
	}))
	if updated.Data.Summary != "Cairnline updated this handoff." || updated.Data.RecommendedNextAction != "Finish it." {
		t.Fatalf("updated handoff response = %+v, want edited text", updated.Data)
	}

	accepted := mustRequestJSONStatus[ProjectHandoffEnvelope](client, http.StatusOK, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_authority/handoffs/handoff_authority/status", projectJourneyJSON(t, map[string]any{
		"status": "accepted",
	}))
	if accepted.Data.Status != projectwork.HandoffStatusAccepted {
		t.Fatalf("accepted handoff response = %+v, want accepted", accepted.Data)
	}
	mirroredHandoff = getMirroredCairnlineHandoffForTest(t, handler, projectID, "work_authority", "handoff_authority")
	if mirroredHandoff.Status != cairnline.HandoffStatusAccepted || mirroredHandoff.Body != "Cairnline updated this handoff." {
		t.Fatalf("mirrored accepted handoff = %+v, want updated accepted Cairnline handoff", mirroredHandoff)
	}
	assertHecateShadowHandoffStatusForTest(t, handler, projectID, "work_authority", "handoff_authority", projectwork.HandoffStatusAccepted)

	client.mustRequestStatus(http.StatusNoContent, http.MethodDelete, "/hecate/v1/projects/"+projectID+"/work-items/work_authority/handoffs/handoff_authority", "")
	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	if _, err := service.GetHandoff(t.Context(), projectID, "work_authority", "handoff_authority"); !errors.Is(err, cairnline.ErrNotFound) {
		store.Close()
		t.Fatalf("deleted Cairnline handoff error = %v, want ErrNotFound", err)
	}
	store.Close()
	if _, err := handler.projectWork.UpdateHandoff(t.Context(), projectID, "work_authority", "handoff_authority", nil); !errors.Is(err, projectwork.ErrNotFound) {
		t.Fatalf("deleted Hecate shadow handoff error = %v, want ErrNotFound", err)
	}
	client.mustRequestStatus(http.StatusNotFound, http.MethodDelete, "/hecate/v1/projects/"+projectID+"/work-items/work_authority/handoffs/handoff_authority", "")
}

func TestProjectWorkAPI_CairnlineWorkItemAuthorityWritesCairnlineAndShadowsHecate(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkCairnlineWorkItemAuthorityTestServer(t)
	client := newAPITestClient(t, server)
	project := mustRequestJSONStatus[ProjectResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects", projectJourneyJSON(t, map[string]any{
		"name": "Work item authority project",
		"roots": []map[string]any{{
			"id":   "root_main",
			"path": "/tmp/hecate-work-item-authority",
			"kind": "local",
		}},
	}))
	projectID := project.Data.ID

	mustRequestJSONStatus[ProjectWorkRoleEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/roles", projectJourneyJSON(t, map[string]any{
		"id":   "role_authority",
		"name": "Authority role",
	}))

	work := mustRequestJSONStatus[ProjectWorkItemEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items", projectJourneyJSON(t, map[string]any{
		"id":                "work_authority",
		"title":             "Exercise work authority",
		"priority":          "high",
		"owner_role_id":     "role_authority",
		"root_id":           "root_main",
		"reviewer_role_ids": []string{"role_authority", " role_authority "},
	}))
	if work.Data.Status != projectwork.WorkItemStatusBacklog || work.Data.Priority != "high" || work.Data.RootID != "root_main" || len(work.Data.ReviewerRoleIDs) != 1 {
		t.Fatalf("work item response = %+v, want Hecate defaults, root, and compacted reviewers", work.Data)
	}
	mirroredWork := getMirroredCairnlineWorkItemForTest(t, handler, projectID, "work_authority")
	if mirroredWork.Status != projectwork.WorkItemStatusBacklog || mirroredWork.Priority != "high" || mirroredWork.RootID != "root_main" || len(mirroredWork.ReviewerRoleIDs) != 1 {
		t.Fatalf("mirrored work item = %+v, want Cairnline authority record with Hecate defaults", mirroredWork)
	}
	assertHecateShadowWorkItemForTest(t, handler, projectID, "work_authority", projectwork.WorkItemStatusBacklog)

	invalid := mustRequestJSONStatus[projectWorkErrorResponse](client, http.StatusBadRequest, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items", projectJourneyJSON(t, map[string]any{
		"id":     "work_bad_status",
		"title":  "Bad status",
		"status": "needs_triage",
	}))
	if invalid.Error.Type != errCodeInvalidRequest || !strings.Contains(invalid.Error.Message, "unsupported work item status") {
		t.Fatalf("invalid status response = %+v, want Hecate validation error", invalid.Error)
	}

	mustRequestJSONStatus[ProjectWorkAssignmentEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_authority/assignments", projectJourneyJSON(t, map[string]any{
		"id":      "asgn_authority",
		"role_id": "role_authority",
	}))
	blocked := mustRequestJSONStatus[projectWorkErrorResponse](client, http.StatusConflict, http.MethodPatch, "/hecate/v1/projects/"+projectID+"/work-items/work_authority", projectJourneyJSON(t, map[string]any{
		"status": "done",
	}))
	if blocked.Error.Type != errCodeConflict || !strings.Contains(blocked.Error.Message, "closeout blocked") {
		t.Fatalf("done blocker response = %+v, want closeout conflict", blocked.Error)
	}
	mirroredWork = getMirroredCairnlineWorkItemForTest(t, handler, projectID, "work_authority")
	if mirroredWork.Status == projectwork.WorkItemStatusDone {
		t.Fatalf("mirrored work item = %+v, want status unchanged after blocked closeout", mirroredWork)
	}

	updated := mustRequestJSONStatus[ProjectWorkItemEnvelope](client, http.StatusOK, http.MethodPatch, "/hecate/v1/projects/"+projectID+"/work-items/work_authority", projectJourneyJSON(t, map[string]any{
		"title":             "Updated work authority",
		"status":            "ready",
		"priority":          "urgent",
		"reviewer_role_ids": []string{},
	}))
	if updated.Data.Title != "Updated work authority" || updated.Data.Status != projectwork.WorkItemStatusReady || updated.Data.Priority != "urgent" || len(updated.Data.ReviewerRoleIDs) != 0 {
		t.Fatalf("updated response = %+v, want edited work item", updated.Data)
	}
	mirroredWork = getMirroredCairnlineWorkItemForTest(t, handler, projectID, "work_authority")
	if mirroredWork.Title != "Updated work authority" || mirroredWork.Status != projectwork.WorkItemStatusReady || mirroredWork.Priority != "urgent" || len(mirroredWork.ReviewerRoleIDs) != 0 {
		t.Fatalf("mirrored updated work item = %+v, want Cairnline authority edits", mirroredWork)
	}
	assertHecateShadowWorkItemForTest(t, handler, projectID, "work_authority", projectwork.WorkItemStatusReady)

	client.mustRequestStatus(http.StatusNoContent, http.MethodDelete, "/hecate/v1/projects/"+projectID+"/work-items/work_authority", "")
	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	if _, err := service.GetWorkItem(t.Context(), projectID, "work_authority"); !errors.Is(err, cairnline.ErrNotFound) {
		store.Close()
		t.Fatalf("deleted Cairnline work item error = %v, want ErrNotFound", err)
	}
	store.Close()
	if _, ok, err := handler.projectWork.GetWorkItem(t.Context(), projectID, "work_authority"); err != nil || ok {
		t.Fatalf("deleted Hecate shadow work item ok=%v err=%v, want not found", ok, err)
	}
	client.mustRequestStatus(http.StatusNotFound, http.MethodDelete, "/hecate/v1/projects/"+projectID+"/work-items/work_authority", "")
}

func TestProjectWorkAPI_CairnlineRoleAuthorityWritesCairnlineAndShadowsHecate(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkCairnlineRoleAuthorityTestServer(t)
	client := newAPITestClient(t, server)
	project := mustRequestJSONStatus[ProjectResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects", projectJourneyJSON(t, map[string]any{
		"name": "Role authority project",
	}))
	projectID := project.Data.ID

	created := mustRequestJSONStatus[ProjectWorkRoleEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/roles", projectJourneyJSON(t, map[string]any{
		"id":                    "role_authority",
		"name":                  "Authority reviewer",
		"description":           "Reviews Cairnline authority writes.",
		"instructions":          "Keep the historical assignment record intact.",
		"default_driver_kind":   projectwork.AssignmentDriverExternalAgent,
		"default_provider":      "anthropic",
		"default_model":         "claude-sonnet-4",
		"default_agent_profile": "safe_external_review",
		"skill_ids":             []string{"review", "review"},
	}))
	if created.Data.ID != "role_authority" || created.Data.DefaultDriverKind != projectwork.AssignmentDriverExternalAgent || created.Data.DefaultModel != "claude-sonnet-4" {
		t.Fatalf("created role response = %+v, want role authority defaults", created.Data)
	}
	if created.Data.CreatedAt == "" || created.Data.UpdatedAt == "" {
		t.Fatalf("created role timestamps = created:%q updated:%q, want Hecate shadow timestamps", created.Data.CreatedAt, created.Data.UpdatedAt)
	}
	mirroredRole := getMirroredCairnlineRoleForTest(t, handler, projectID, "role_authority")
	if mirroredRole.Name != "Authority reviewer" || mirroredRole.DefaultProfileID != "safe_external_review" || mirroredRole.DefaultExecutionMode != cairnline.ExecutionExternalAdapter {
		t.Fatalf("Cairnline role = %+v, want role authority record", mirroredRole)
	}
	if len(mirroredRole.DefaultSkillIDs) != 1 || mirroredRole.DefaultSkillIDs[0] != "review" {
		t.Fatalf("Cairnline role skills = %+v, want compacted review skill", mirroredRole.DefaultSkillIDs)
	}
	assertHecateShadowRoleForTest(t, handler, projectID, "role_authority")

	updated := mustRequestJSONStatus[ProjectWorkRoleEnvelope](client, http.StatusOK, http.MethodPatch, "/hecate/v1/projects/"+projectID+"/roles/role_authority", projectJourneyJSON(t, map[string]any{
		"name":          "Authority owner",
		"default_model": "claude-opus-4",
		"skill_ids":     []string{"review", "release"},
	}))
	if updated.Data.Name != "Authority owner" || updated.Data.DefaultModel != "claude-opus-4" || !containsString(updated.Data.SkillIDs, "release") {
		t.Fatalf("updated role response = %+v, want edited role authority defaults", updated.Data)
	}
	mirroredRole = getMirroredCairnlineRoleForTest(t, handler, projectID, "role_authority")
	if mirroredRole.Name != "Authority owner" || !containsString(mirroredRole.DefaultSkillIDs, "release") {
		t.Fatalf("updated Cairnline role = %+v, want edited role authority record", mirroredRole)
	}
	assertMirroredExecutionProfileForTest(t, handler, mirroredRole.DefaultExecutionProfileID, "anthropic", "claude-opus-4")

	work := mustRequestJSONStatus[ProjectWorkItemEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items", projectJourneyJSON(t, map[string]any{
		"id":            "work_role_authority",
		"title":         "Keep role history",
		"owner_role_id": "role_authority",
	}))
	if work.Data.OwnerRoleID != "role_authority" {
		t.Fatalf("work item response = %+v, want owner role", work.Data)
	}
	assignment := mustRequestJSONStatus[ProjectWorkAssignmentEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_role_authority/assignments", projectJourneyJSON(t, map[string]any{
		"id":          "asgn_role_authority",
		"role_id":     "role_authority",
		"driver_kind": projectwork.AssignmentDriverExternalAgent,
	}))
	if assignment.Data.RoleID != "role_authority" {
		t.Fatalf("assignment response = %+v, want role authority assignment", assignment.Data)
	}

	client.mustRequestStatus(http.StatusNoContent, http.MethodDelete, "/hecate/v1/projects/"+projectID+"/roles/role_authority", "")
	if role := mirroredCairnlineRoleForTest(t, handler, projectID, "role_authority"); role != nil {
		t.Fatalf("deleted Cairnline role = %+v, want missing", role)
	}
	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	historicalAssignment, err := service.GetAssignment(t.Context(), projectID, "asgn_role_authority")
	if err != nil {
		store.Close()
		t.Fatalf("GetAssignment() after role delete error = %v", err)
	}
	if historicalAssignment.RoleID != "role_authority" {
		store.Close()
		t.Fatalf("assignment after role delete = %+v, want historical role id", historicalAssignment)
	}
	store.Close()
	if hasHecateRoleForTest(t, handler, projectID, "role_authority") {
		t.Fatalf("Hecate shadow role still exists after Cairnline-authoritative delete")
	}

	client.mustRequestStatus(http.StatusConflict, http.MethodDelete, "/hecate/v1/projects/"+projectID+"/roles/product_manager", "")
}

func TestProjectWorkAPI_CairnlineAssignmentAuthorityWritesCairnlineAndShadowsHecate(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkCairnlineAssignmentAuthorityTestServer(t)
	client := newAPITestClient(t, server)
	project := mustRequestJSONStatus[ProjectResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects", projectJourneyJSON(t, map[string]any{
		"name": "Assignment authority project",
		"roots": []map[string]any{{
			"id":   "root_main",
			"path": "/tmp/hecate-assignment-authority",
			"kind": "local",
		}},
	}))
	projectID := project.Data.ID

	mustRequestJSONStatus[ProjectWorkRoleEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/roles", projectJourneyJSON(t, map[string]any{
		"id":   "role_authority",
		"name": "Authority implementer",
	}))
	mustRequestJSONStatus[ProjectWorkItemEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items", projectJourneyJSON(t, map[string]any{
		"id":      "work_authority",
		"title":   "Exercise assignment authority",
		"root_id": "root_main",
	}))

	invalidStatus := mustRequestJSONStatus[projectWorkErrorResponse](client, http.StatusBadRequest, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_authority/assignments", projectJourneyJSON(t, map[string]any{
		"id":      "asgn_bad_status",
		"role_id": "role_authority",
		"status":  "needs_triage",
	}))
	if invalidStatus.Error.Type != errCodeInvalidRequest || !strings.Contains(invalidStatus.Error.Message, "unsupported assignment status") {
		t.Fatalf("invalid status response = %+v, want Hecate validation error", invalidStatus.Error)
	}
	invalidDriver := mustRequestJSONStatus[projectWorkErrorResponse](client, http.StatusBadRequest, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_authority/assignments", projectJourneyJSON(t, map[string]any{
		"id":          "asgn_bad_driver",
		"role_id":     "role_authority",
		"driver_kind": "mcp_pull",
	}))
	if invalidDriver.Error.Type != errCodeInvalidRequest || !strings.Contains(invalidDriver.Error.Message, "unsupported assignment driver_kind") {
		t.Fatalf("invalid driver response = %+v, want Hecate validation error", invalidDriver.Error)
	}
	invalidRoot := mustRequestJSONStatus[projectWorkErrorResponse](client, http.StatusNotFound, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_authority/assignments", projectJourneyJSON(t, map[string]any{
		"id":      "asgn_bad_root",
		"role_id": "role_authority",
		"root_id": "root_missing",
	}))
	if invalidRoot.Error.Type != errCodeNotFound || !strings.Contains(invalidRoot.Error.Message, "project root not found") {
		t.Fatalf("invalid root response = %+v, want Cairnline root dependency error", invalidRoot.Error)
	}

	created := mustRequestJSONStatus[ProjectWorkAssignmentEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_authority/assignments", projectJourneyJSON(t, map[string]any{
		"id":      "asgn_authority",
		"role_id": "role_authority",
		"root_id": "root_main",
		"execution_ref": map[string]any{
			"kind":                "hecate_task",
			"context_snapshot_id": "ctx_authority",
			"trace_id":            "trace_authority",
		},
	}))
	if created.Data.ReadBackend != "cairnline" || created.Data.DriverKind != projectwork.AssignmentDriverHecateTask || created.Data.Status != projectwork.AssignmentStatusQueued || created.Data.RootID != "root_main" {
		t.Fatalf("created assignment = %+v, want Cairnline response with Hecate defaults and root", created.Data)
	}
	if created.Data.ExecutionRef == nil || created.Data.ExecutionRef.ContextSnapshotID != "ctx_authority" || created.Data.ExecutionRef.TraceID != "trace_authority" {
		t.Fatalf("created execution_ref = %+v, want Hecate ref shadowed through authority path", created.Data.ExecutionRef)
	}
	mirrored := getMirroredCairnlineAssignmentForTest(t, handler, projectID, "asgn_authority")
	if mirrored.ExecutionMode != cairnline.ExecutionOrchestrated || mirrored.Status != cairnline.AssignmentQueued || mirrored.WorkItemID != "work_authority" || mirrored.RootID != "root_main" || mirrored.ContextSnapshotID != "ctx_authority" {
		t.Fatalf("mirrored assignment = %+v, want Cairnline-authoritative queued orchestrated record", mirrored)
	}
	shadow := getStoredProjectWorkAssignmentForTest(t, handler, projectID, "work_authority", "asgn_authority")
	if shadow.DriverKind != projectwork.AssignmentDriverHecateTask || shadow.Status != projectwork.AssignmentStatusQueued || shadow.ExecutionRef.ContextSnapshotID != "ctx_authority" {
		t.Fatalf("Hecate shadow assignment = %+v, want compatibility shadow", shadow)
	}
	startedAt := time.Date(2026, 6, 30, 10, 30, 0, 0, time.UTC)
	completedAt := startedAt.Add(2 * time.Minute)
	contextPacket := []byte(`{"id":"ctx_authority","items":[{"kind":"project_work","body":"persisted"}]}`)
	if _, err := handler.projectRuntime.Upsert(t.Context(), projectruntime.AssignmentRuntime{
		ProjectID:     projectID,
		AssignmentID:  "asgn_authority",
		ContextPacket: contextPacket,
		StartedAt:     startedAt,
		CompletedAt:   completedAt,
	}); err != nil {
		t.Fatalf("seed assignment runtime overlay: %v", err)
	}

	updated := mustRequestJSONStatus[ProjectWorkAssignmentEnvelope](client, http.StatusOK, http.MethodPatch, "/hecate/v1/projects/"+projectID+"/work-items/work_authority/assignments/asgn_authority", projectJourneyJSON(t, map[string]any{
		"driver_kind": projectwork.AssignmentDriverExternalAgent,
		"status":      projectwork.AssignmentStatusCompleted,
		"execution_ref": map[string]any{
			"kind":            "external_agent",
			"chat_session_id": "chat_authority",
			"message_id":      "msg_authority",
			"status":          "completed",
		},
	}))
	if updated.Data.ReadBackend != "cairnline" || updated.Data.DriverKind != projectwork.AssignmentDriverExternalAgent || updated.Data.Status != projectwork.AssignmentStatusCompleted {
		t.Fatalf("updated assignment = %+v, want Cairnline response with external completed status", updated.Data)
	}
	if updated.Data.ExecutionRef == nil || updated.Data.ExecutionRef.ChatSessionID != "chat_authority" || updated.Data.ExecutionRef.MessageID != "msg_authority" {
		t.Fatalf("updated execution_ref = %+v, want native execution ref preserved", updated.Data.ExecutionRef)
	}
	mirrored = getMirroredCairnlineAssignmentForTest(t, handler, projectID, "asgn_authority")
	if mirrored.ExecutionMode != cairnline.ExecutionExternalAdapter || mirrored.Status != cairnline.AssignmentCompleted || !strings.Contains(mirrored.ExecutionRef, "chat_authority") {
		t.Fatalf("updated mirrored assignment = %+v, want Cairnline-authoritative external completion", mirrored)
	}
	shadow = getStoredProjectWorkAssignmentForTest(t, handler, projectID, "work_authority", "asgn_authority")
	if shadow.DriverKind != projectwork.AssignmentDriverExternalAgent || shadow.Status != projectwork.AssignmentStatusCompleted || shadow.ExecutionRef.ChatSessionID != "chat_authority" {
		t.Fatalf("updated Hecate shadow assignment = %+v, want compatibility shadow update", shadow)
	}
	if string(shadow.ContextPacket) != string(contextPacket) || !shadow.StartedAt.Equal(startedAt) || !shadow.CompletedAt.Equal(completedAt) {
		t.Fatalf("updated Hecate shadow runtime fields = packet %s started %v completed %v, want preserved packet/timestamps", string(shadow.ContextPacket), shadow.StartedAt, shadow.CompletedAt)
	}

	client.mustRequestStatus(http.StatusNoContent, http.MethodDelete, "/hecate/v1/projects/"+projectID+"/work-items/work_authority/assignments/asgn_authority", "")
	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	if _, err := service.GetAssignment(t.Context(), projectID, "asgn_authority"); !errors.Is(err, cairnline.ErrNotFound) {
		store.Close()
		t.Fatalf("deleted Cairnline assignment error = %v, want ErrNotFound", err)
	}
	store.Close()
	assignments, err := handler.projectWork.ListAssignments(t.Context(), projectwork.AssignmentFilter{ProjectID: projectID, WorkItemID: "work_authority"})
	if err != nil {
		t.Fatalf("ListAssignments() after delete: %v", err)
	}
	if len(assignments) != 0 {
		t.Fatalf("Hecate shadow assignments after delete = %+v, want empty", assignments)
	}
	client.mustRequestStatus(http.StatusNotFound, http.MethodDelete, "/hecate/v1/projects/"+projectID+"/work-items/work_authority/assignments/asgn_authority", "")
}

func TestProjectWorkAPI_CairnlineRoleAuthorityWritesCairnlineOnlyProject(t *testing.T) {
	t.Parallel()
	const projectID = "proj_role_authority_cairnline_only"
	handler, server := newProjectWorkCairnlineRoleAuthorityTestServer(t)
	client := newAPITestClient(t, server)
	seedCairnlineOnlyProjectWorkGraphForTest(t, handler, cairnline.Project{
		ID:   projectID,
		Name: "Role authority Cairnline only",
	}, nil, nil, nil)
	handler.projects = nil
	handler.projectWork = nil

	created := mustRequestJSONStatus[ProjectWorkRoleEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/roles", projectJourneyJSON(t, map[string]any{
		"id":                  "role_cairnline_only",
		"name":                "Cairnline-only reviewer",
		"default_driver_kind": projectwork.AssignmentDriverExternalAgent,
		"skill_ids":           []string{"review"},
	}))
	if created.Data.ID != "role_cairnline_only" || created.Data.ProjectID != projectID || created.Data.DefaultDriverKind != projectwork.AssignmentDriverExternalAgent {
		t.Fatalf("created role response = %+v, want Cairnline-only project role", created.Data)
	}
	mirroredRole := getMirroredCairnlineRoleForTest(t, handler, projectID, "role_cairnline_only")
	if mirroredRole.Name != "Cairnline-only reviewer" || mirroredRole.DefaultExecutionMode != cairnline.ExecutionExternalAdapter {
		t.Fatalf("mirrored role = %+v, want Cairnline-authoritative role", mirroredRole)
	}
	if handler.projects != nil || handler.projectWork != nil {
		t.Fatal("native project/work stores were unexpectedly configured")
	}

	updated := mustRequestJSONStatus[ProjectWorkRoleEnvelope](client, http.StatusOK, http.MethodPatch, "/hecate/v1/projects/"+projectID+"/roles/role_cairnline_only", projectJourneyJSON(t, map[string]any{
		"name":      "Updated Cairnline-only reviewer",
		"skill_ids": []string{"review", "qa"},
	}))
	if updated.Data.Name != "Updated Cairnline-only reviewer" || !containsString(updated.Data.SkillIDs, "qa") {
		t.Fatalf("updated role response = %+v, want edited Cairnline-only role", updated.Data)
	}
	client.mustRequestStatus(http.StatusNoContent, http.MethodDelete, "/hecate/v1/projects/"+projectID+"/roles/role_cairnline_only", "")
	if role := mirroredCairnlineRoleForTest(t, handler, projectID, "role_cairnline_only"); role != nil {
		t.Fatalf("deleted Cairnline-only role = %+v, want missing", role)
	}
}

func TestProjectWorkAPI_CairnlineWorkItemAuthorityWritesCairnlineOnlyProject(t *testing.T) {
	t.Parallel()
	const projectID = "proj_work_authority_cairnline_only"
	handler, server := newProjectWorkCairnlineWorkItemAuthorityTestServer(t)
	client := newAPITestClient(t, server)
	seedCairnlineOnlyProjectWorkGraphForTest(t, handler, cairnline.Project{
		ID:            projectID,
		Name:          "Work authority Cairnline only",
		DefaultRootID: "root_cairnline_only",
		Roots: []cairnline.Root{{
			ID:     "root_cairnline_only",
			Path:   "/workspace/cairnline-only",
			Kind:   "git",
			Active: true,
		}},
	}, []cairnline.Role{{
		ID:        "role_cairnline_only",
		ProjectID: projectID,
		Name:      "Cairnline-only owner",
	}}, nil, nil)
	handler.projects = nil
	handler.projectWork = nil

	created := mustRequestJSONStatus[ProjectWorkItemEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items", projectJourneyJSON(t, map[string]any{
		"id":            "work_cairnline_only",
		"title":         "Create work from Cairnline-only project",
		"owner_role_id": "role_cairnline_only",
		"root_id":       "root_cairnline_only",
	}))
	if created.Data.ID != "work_cairnline_only" || created.Data.RootID != "root_cairnline_only" || created.Data.Status != projectwork.WorkItemStatusBacklog {
		t.Fatalf("created work response = %+v, want Cairnline-only work item defaults", created.Data)
	}
	mirroredWork := getMirroredCairnlineWorkItemForTest(t, handler, projectID, "work_cairnline_only")
	if mirroredWork.Title != "Create work from Cairnline-only project" || mirroredWork.RootID != "root_cairnline_only" {
		t.Fatalf("mirrored work item = %+v, want Cairnline-authoritative work item", mirroredWork)
	}
	if handler.projects != nil || handler.projectWork != nil {
		t.Fatal("native project/work stores were unexpectedly configured")
	}

	updated := mustRequestJSONStatus[ProjectWorkItemEnvelope](client, http.StatusOK, http.MethodPatch, "/hecate/v1/projects/"+projectID+"/work-items/work_cairnline_only", projectJourneyJSON(t, map[string]any{
		"title":  "Updated Cairnline-only work",
		"status": projectwork.WorkItemStatusReady,
	}))
	if updated.Data.Title != "Updated Cairnline-only work" || updated.Data.Status != projectwork.WorkItemStatusReady {
		t.Fatalf("updated work response = %+v, want edited Cairnline-only work item", updated.Data)
	}
	client.mustRequestStatus(http.StatusNoContent, http.MethodDelete, "/hecate/v1/projects/"+projectID+"/work-items/work_cairnline_only", "")
}

func TestProjectWorkAPI_CairnlineWorkItemAuthorityStrictEmbeddedUsesCairnlineProjectOverStaleNative(t *testing.T) {
	t.Parallel()
	const projectID = "proj_work_authority_stale_native"
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend:     "cairnline",
			CairnlineReadSource:     "embedded",
			CairnlineWriteAuthority: projectCairnlineWriteAuthorityProjectWorkItems,
		},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetProjectWorkStore(projectwork.NewMemoryStore())
	handler.SetAgentProfileStore(agentprofiles.NewMemoryStore())
	if _, err := handler.projects.Create(t.Context(), projects.Project{
		ID:   projectID,
		Name: "Stale native project",
	}); err != nil {
		t.Fatalf("create stale native project: %v", err)
	}
	if err := handler.withCairnlineEmbeddedMirrorService(t.Context(), func(service *cairnline.Service) error {
		_, err := service.CreateProject(t.Context(), cairnline.Project{
			ID:            projectID,
			Name:          "Authoritative Cairnline project",
			DefaultRootID: "root_authoritative",
			Roots: []cairnline.Root{{
				ID:     "root_authoritative",
				Path:   "/workspace/authoritative",
				Kind:   "git",
				Active: true,
			}},
		})
		return err
	}); err != nil {
		t.Fatalf("seed authoritative Cairnline project: %v", err)
	}

	client := newAPITestClient(t, NewServer(quietLogger(), handler))
	created := mustRequestJSONStatus[ProjectWorkItemEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items", projectJourneyJSON(t, map[string]any{
		"id":      "work_authoritative_root",
		"title":   "Use authoritative root metadata",
		"root_id": "root_authoritative",
	}))
	if created.Data.RootID != "root_authoritative" {
		t.Fatalf("created work item = %+v, want item using authoritative Cairnline root", created.Data)
	}
	mirroredWork := getMirroredCairnlineWorkItemForTest(t, handler, projectID, "work_authoritative_root")
	if mirroredWork.RootID != "root_authoritative" {
		t.Fatalf("mirrored work item = %+v, want authoritative Cairnline root", mirroredWork)
	}
	native, ok, err := handler.projects.Get(t.Context(), projectID)
	if err != nil || !ok {
		t.Fatalf("native project ok=%v error=%v, want stale compatibility row still present", ok, err)
	}
	if len(native.Roots) != 0 {
		t.Fatalf("native project roots = %+v, want stale compatibility row not used for root validation", native.Roots)
	}
}

func TestProjectWorkAPI_CairnlineAssignmentAuthorityWritesCairnlineOnlyProject(t *testing.T) {
	t.Parallel()
	const projectID = "proj_assignment_authority_cairnline_only"
	handler, server := newProjectWorkCairnlineAssignmentAuthorityTestServer(t)
	client := newAPITestClient(t, server)
	seedCairnlineOnlyProjectWorkGraphForTest(t, handler, cairnline.Project{
		ID:            projectID,
		Name:          "Assignment authority Cairnline only",
		DefaultRootID: "root_cairnline_only",
		Roots: []cairnline.Root{{
			ID:     "root_cairnline_only",
			Path:   "/workspace/cairnline-only",
			Kind:   "git",
			Active: true,
		}},
	}, []cairnline.Role{{
		ID:                   "role_cairnline_only",
		ProjectID:            projectID,
		Name:                 "Cairnline-only implementer",
		DefaultExecutionMode: cairnline.ExecutionExternalAdapter,
	}}, []cairnline.WorkItem{{
		ID:        "work_cairnline_only",
		ProjectID: projectID,
		Title:     "Assign work from Cairnline-only project",
		Status:    cairnline.WorkStatusReady,
		Priority:  cairnline.PriorityNormal,
		RootID:    "root_cairnline_only",
	}}, nil)
	handler.projects = nil
	handler.projectWork = nil

	invalidRoot := mustRequestJSONStatus[projectWorkErrorResponse](client, http.StatusNotFound, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_cairnline_only/assignments", projectJourneyJSON(t, map[string]any{
		"id":      "asgn_bad_root",
		"role_id": "role_cairnline_only",
		"root_id": "root_missing",
	}))
	if invalidRoot.Error.Type != errCodeNotFound || !strings.Contains(invalidRoot.Error.Message, "project root not found") {
		t.Fatalf("invalid Cairnline-only root response = %+v, want Cairnline root dependency error", invalidRoot.Error)
	}

	created := mustRequestJSONStatus[ProjectWorkAssignmentEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_cairnline_only/assignments", projectJourneyJSON(t, map[string]any{
		"id":      "asgn_cairnline_only",
		"role_id": "role_cairnline_only",
		"root_id": "root_cairnline_only",
		"execution_ref": map[string]any{
			"kind":                "external_agent",
			"context_snapshot_id": "ctx_cairnline_only",
			"trace_id":            "trace_cairnline_only",
		},
	}))
	if created.Data.ReadBackend != "cairnline" || created.Data.DriverKind != projectwork.AssignmentDriverExternalAgent || created.Data.Status != projectwork.AssignmentStatusQueued {
		t.Fatalf("created assignment response = %+v, want Cairnline-only assignment with role default driver", created.Data)
	}
	if created.Data.ExecutionRef == nil || created.Data.ExecutionRef.ContextSnapshotID != "ctx_cairnline_only" {
		t.Fatalf("created assignment execution_ref = %+v, want runtime overlay persisted without native work store", created.Data.ExecutionRef)
	}
	mirrored := getMirroredCairnlineAssignmentForTest(t, handler, projectID, "asgn_cairnline_only")
	if mirrored.WorkItemID != "work_cairnline_only" || mirrored.ExecutionMode != cairnline.ExecutionExternalAdapter || mirrored.RootID != "root_cairnline_only" {
		t.Fatalf("mirrored assignment = %+v, want Cairnline-authoritative assignment", mirrored)
	}
	runtime, ok, err := handler.projectRuntime.Get(t.Context(), projectID, "asgn_cairnline_only")
	if err != nil || !ok || runtime.ExecutionRef.ContextSnapshotID != "ctx_cairnline_only" {
		t.Fatalf("runtime overlay after assignment create = %+v ok=%v err=%v, want persisted execution ref", runtime, ok, err)
	}

	updated := mustRequestJSONStatus[ProjectWorkAssignmentEnvelope](client, http.StatusOK, http.MethodPatch, "/hecate/v1/projects/"+projectID+"/work-items/work_cairnline_only/assignments/asgn_cairnline_only", projectJourneyJSON(t, map[string]any{
		"status": projectwork.AssignmentStatusCompleted,
		"execution_ref": map[string]any{
			"kind":            "external_agent",
			"chat_session_id": "chat_cairnline_only",
			"message_id":      "msg_cairnline_only",
			"status":          "completed",
		},
	}))
	if updated.Data.Status != projectwork.AssignmentStatusCompleted {
		t.Fatalf("updated assignment response = %+v, want completed Cairnline-only assignment", updated.Data)
	}
	if updated.Data.ExecutionRef == nil || updated.Data.ExecutionRef.ChatSessionID != "chat_cairnline_only" {
		t.Fatalf("updated assignment execution_ref = %+v, want runtime overlay updated without native work store", updated.Data.ExecutionRef)
	}
	client.mustRequestStatus(http.StatusNoContent, http.MethodDelete, "/hecate/v1/projects/"+projectID+"/work-items/work_cairnline_only/assignments/asgn_cairnline_only", "")
	if _, ok, err := handler.projectRuntime.Get(t.Context(), projectID, "asgn_cairnline_only"); err != nil || ok {
		t.Fatalf("runtime overlay after assignment delete ok=%v err=%v, want deleted", ok, err)
	}
}

func TestProjectWorkAPI_CairnlineCollaborationAuthorityWritesCairnlineOnlyProject(t *testing.T) {
	t.Parallel()
	const projectID = "proj_collaboration_authority_cairnline_only"
	handler, server := newProjectWorkCairnlineCollaborationAuthorityTestServer(t)
	client := newAPITestClient(t, server)
	seedCairnlineOnlyProjectWorkGraphForTest(t, handler, cairnline.Project{
		ID:   projectID,
		Name: "Collaboration authority Cairnline only",
	}, []cairnline.Role{{
		ID:        "role_cairnline_only",
		ProjectID: projectID,
		Name:      "Cairnline-only reviewer",
	}}, []cairnline.WorkItem{{
		ID:        "work_cairnline_only",
		ProjectID: projectID,
		Title:     "Review work from Cairnline-only project",
		Status:    cairnline.WorkStatusReady,
		Priority:  cairnline.PriorityNormal,
	}}, []cairnline.Assignment{{
		ID:            "asgn_cairnline_only",
		ProjectID:     projectID,
		WorkItemID:    "work_cairnline_only",
		RoleID:        "role_cairnline_only",
		ExecutionMode: cairnline.ExecutionManual,
		Status:        cairnline.AssignmentCompleted,
	}})
	handler.projects = nil
	handler.projectWork = nil

	missingWork := mustRequestJSONStatus[projectWorkErrorResponse](client, http.StatusNotFound, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_missing/artifacts", projectJourneyJSON(t, map[string]any{
		"id":                     "review_missing_work",
		"assignment_id":          "asgn_cairnline_only",
		"reviewed_assignment_id": "asgn_cairnline_only",
		"kind":                   "review",
		"body":                   "Review cannot attach to a missing work item.",
		"author_role_id":         "role_cairnline_only",
		"review_verdict":         projectwork.ReviewVerdictApproved,
		"review_risk":            projectwork.ReviewRiskLow,
	}))
	if missingWork.Error.Type != errCodeNotFound || !strings.Contains(missingWork.Error.Message, "work item not found") {
		t.Fatalf("missing Cairnline-only work item error = %+v, want work item dependency error", missingWork.Error)
	}

	review := mustRequestJSONStatus[ProjectWorkArtifactEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_cairnline_only/artifacts", projectJourneyJSON(t, map[string]any{
		"id":                     "review_cairnline_only",
		"assignment_id":          "asgn_cairnline_only",
		"reviewed_assignment_id": "asgn_cairnline_only",
		"kind":                   "review",
		"body":                   "Review recorded against a Cairnline-only assignment.",
		"author_role_id":         "role_cairnline_only",
		"review_verdict":         projectwork.ReviewVerdictApproved,
		"review_risk":            projectwork.ReviewRiskLow,
	}))
	if review.Data.ID != "review_cairnline_only" || review.Data.ReviewVerdict != projectwork.ReviewVerdictApproved {
		t.Fatalf("review response = %+v, want Cairnline-only review artifact", review.Data)
	}
	mirroredReview := getMirroredCairnlineReviewForTest(t, handler, projectID, "work_cairnline_only", "review_cairnline_only")
	if mirroredReview.AssignmentID != "asgn_cairnline_only" || mirroredReview.ReviewerRoleID != "role_cairnline_only" {
		t.Fatalf("mirrored review = %+v, want Cairnline-authoritative review", mirroredReview)
	}
	handoff := mustRequestJSONStatus[ProjectHandoffEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_cairnline_only/handoffs", projectJourneyJSON(t, map[string]any{
		"id":                      "handoff_cairnline_only",
		"source_assignment_id":    "asgn_cairnline_only",
		"target_role_id":          "role_cairnline_only",
		"title":                   "Cairnline-only handoff",
		"summary":                 "Hand off a portable assignment.",
		"recommended_next_action": "Continue from the recorded evidence.",
		"created_by_role_id":      "role_cairnline_only",
	}))
	if handoff.Data.ID != "handoff_cairnline_only" || handoff.Data.Status != projectwork.HandoffStatusPending {
		t.Fatalf("handoff response = %+v, want Cairnline-only pending handoff", handoff.Data)
	}
	updated := mustRequestJSONStatus[ProjectHandoffEnvelope](client, http.StatusOK, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_cairnline_only/handoffs/handoff_cairnline_only/status", projectJourneyJSON(t, map[string]any{
		"status": projectwork.HandoffStatusAccepted,
	}))
	if updated.Data.Status != projectwork.HandoffStatusAccepted {
		t.Fatalf("updated handoff response = %+v, want Cairnline-only accepted handoff", updated.Data)
	}
	client.mustRequestStatus(http.StatusNoContent, http.MethodDelete, "/hecate/v1/projects/"+projectID+"/work-items/work_cairnline_only/handoffs/handoff_cairnline_only", "")
	if handler.projects != nil || handler.projectWork != nil {
		t.Fatal("native project/work stores were unexpectedly configured")
	}
}

func TestProjectWorkAPI_MirrorsRoleAndWorkItemMutationsToCairnlineWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkCairnlineMirrorTestServer(t)
	project := createProjectForWorkTest(t, server)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/roles", bytes.NewReader([]byte(`{
		"id":"role_release",
		"name":"Release captain",
		"description":"Coordinates release work.",
		"instructions":"Keep release notes current.",
		"default_driver_kind":"external_agent",
		"default_provider":"anthropic",
		"default_model":"claude-sonnet-4",
		"default_agent_profile":"safe_external_review",
		"skill_ids":["release"]
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create role status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	mirroredRole := getMirroredCairnlineRoleForTest(t, handler, project.Data.ID, "role_release")
	if mirroredRole.Name != "Release captain" || mirroredRole.DefaultProfileID != "safe_external_review" || mirroredRole.DefaultExecutionMode != "external_adapter" {
		t.Fatalf("mirrored role = %+v, want release captain defaults", mirroredRole)
	}
	if len(mirroredRole.DefaultSkillIDs) != 1 || mirroredRole.DefaultSkillIDs[0] != "release" {
		t.Fatalf("mirrored role skill ids = %+v, want release", mirroredRole.DefaultSkillIDs)
	}
	assertMirroredExecutionProfileForTest(t, handler, mirroredRole.DefaultExecutionProfileID, "anthropic", "claude-sonnet-4")

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+project.Data.ID+"/roles/role_release", bytes.NewReader([]byte(`{
		"name":"Release owner",
		"default_model":"claude-opus-4",
		"skill_ids":["release","qa"]
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("update role status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	mirroredRole = getMirroredCairnlineRoleForTest(t, handler, project.Data.ID, "role_release")
	if mirroredRole.Name != "Release owner" || !containsString(mirroredRole.DefaultSkillIDs, "release") || !containsString(mirroredRole.DefaultSkillIDs, "qa") {
		t.Fatalf("mirrored updated role = %+v, want renamed role with two skills", mirroredRole)
	}
	assertMirroredExecutionProfileForTest(t, handler, mirroredRole.DefaultExecutionProfileID, "anthropic", "claude-opus-4")

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/work-items", bytes.NewReader([]byte(`{
		"id":"work_release",
		"title":"Prepare release",
		"brief":"Coordinate the release checklist.",
		"priority":"high",
		"owner_role_id":"role_release",
		"reviewer_role_ids":["role_release"]
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create work item status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	mirroredWork := getMirroredCairnlineWorkItemForTest(t, handler, project.Data.ID, "work_release")
	if mirroredWork.Title != "Prepare release" || mirroredWork.Priority != "high" || mirroredWork.OwnerRoleID != "role_release" || len(mirroredWork.ReviewerRoleIDs) != 1 {
		t.Fatalf("mirrored work item = %+v, want release-owned high-priority work", mirroredWork)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_release", bytes.NewReader([]byte(`{
		"title":"Prepare release notes",
		"status":"ready",
		"reviewer_role_ids":[]
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("update work item status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	mirroredWork = getMirroredCairnlineWorkItemForTest(t, handler, project.Data.ID, "work_release")
	if mirroredWork.Title != "Prepare release notes" || mirroredWork.Status != projectwork.WorkItemStatusReady || len(mirroredWork.ReviewerRoleIDs) != 0 {
		t.Fatalf("mirrored updated work item = %+v, want ready release notes without reviewers", mirroredWork)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_release/assignments", bytes.NewReader([]byte(`{
		"id":"asgn_release",
		"role_id":"role_release",
		"driver_kind":"external_agent",
		"status":"queued",
		"execution_ref":{"kind":"task_run","task_id":"task_release","run_id":"run_release","context_snapshot_id":"ctx_release"}
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create assignment status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	mirroredAssignment := getMirroredCairnlineAssignmentForTest(t, handler, project.Data.ID, "asgn_release")
	if mirroredAssignment.Status != cairnline.AssignmentQueued || mirroredAssignment.RoleID != "role_release" || mirroredAssignment.WorkItemID != "work_release" || mirroredAssignment.ProfileID != "safe_external_review" {
		t.Fatalf("mirrored assignment = %+v, want queued release assignment metadata", mirroredAssignment)
	}
	if mirroredAssignment.ExecutionMode != cairnline.ExecutionExternalAdapter || mirroredAssignment.DesiredAgent.Kind != cairnline.DesiredAgentAny || mirroredAssignment.ContextSnapshotID != "ctx_release" {
		t.Fatalf("mirrored assignment execution = %+v, want external adapter context snapshot", mirroredAssignment)
	}
	if len(mirroredAssignment.DesiredAgent.SkillIDs) != 2 || !containsString(mirroredAssignment.DesiredAgent.SkillIDs, "release") || !containsString(mirroredAssignment.DesiredAgent.SkillIDs, "qa") {
		t.Fatalf("mirrored assignment desired skills = %+v, want role skill ids", mirroredAssignment.DesiredAgent.SkillIDs)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_release/artifacts", bytes.NewReader([]byte(`{
		"id":"art_release_decision",
		"assignment_id":"asgn_release",
		"kind":"decision_note",
		"title":"Release decision",
		"body":"Ship after the release checklist is complete.",
		"author_role_id":"role_release"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create decision artifact status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	mirroredArtifact := getMirroredCairnlineArtifactForTest(t, handler, project.Data.ID, "work_release", "art_release_decision")
	if mirroredArtifact.Kind != projectwork.ArtifactKindDecisionNote || mirroredArtifact.AssignmentID != "asgn_release" || mirroredArtifact.AuthorRoleID != "role_release" {
		t.Fatalf("mirrored artifact = %+v, want release decision metadata", mirroredArtifact)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_release/artifacts", bytes.NewReader([]byte(`{
		"id":"art_release_evidence",
		"assignment_id":"asgn_release",
		"kind":"evidence_link",
		"title":"Release checklist",
		"body":"Checklist output was reviewed.",
		"evidence_url":"https://example.invalid/release/checklist",
		"evidence_trust_label":"operator_provided"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create evidence artifact status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	mirroredEvidence := getMirroredCairnlineEvidenceForTest(t, handler, project.Data.ID, "work_release", "art_release_evidence")
	if mirroredEvidence.AssignmentID != "asgn_release" || mirroredEvidence.Locator != "https://example.invalid/release/checklist" || mirroredEvidence.TrustLabel != cairnline.EvidenceTrustOperator {
		t.Fatalf("mirrored evidence = %+v, want release checklist evidence metadata", mirroredEvidence)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_release/artifacts", bytes.NewReader([]byte(`{
		"id":"art_release_review",
		"assignment_id":"asgn_release",
		"reviewed_assignment_id":"asgn_release",
		"kind":"review",
		"title":"Release review",
		"body":"Release assignment is ready.",
		"author_role_id":"role_release",
		"review_verdict":"approved",
		"review_risk":"low"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create review artifact status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	mirroredReview := getMirroredCairnlineReviewForTest(t, handler, project.Data.ID, "work_release", "art_release_review")
	if mirroredReview.AssignmentID != "asgn_release" || mirroredReview.ReviewerRoleID != "role_release" || mirroredReview.Verdict != cairnline.ReviewVerdictApproved || mirroredReview.Risk != cairnline.ReviewRiskLow {
		t.Fatalf("mirrored review = %+v, want approved release review metadata", mirroredReview)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_release/handoffs", bytes.NewReader([]byte(`{
		"id":"handoff_release",
		"source_assignment_id":"asgn_release",
		"target_role_id":"role_release",
		"target_assignment_id":"asgn_release",
		"target_work_item_id":"work_release",
		"title":"Release handoff",
		"summary":"Ready for release owner follow-through.",
		"recommended_next_action":"Publish the release notes.",
		"created_by_role_id":"role_release"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create handoff status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	mirroredHandoff := getMirroredCairnlineHandoffForTest(t, handler, project.Data.ID, "work_release", "handoff_release")
	if mirroredHandoff.Status != cairnline.HandoffStatusOpen || mirroredHandoff.SourceAssignmentID != "asgn_release" || mirroredHandoff.TargetAssignmentID != "asgn_release" || mirroredHandoff.ToRoleID != "role_release" {
		t.Fatalf("mirrored handoff = %+v, want open release handoff metadata", mirroredHandoff)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_release/handoffs/handoff_release/status", bytes.NewReader([]byte(`{"status":"accepted"}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("accept handoff status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	mirroredHandoff = getMirroredCairnlineHandoffForTest(t, handler, project.Data.ID, "work_release", "handoff_release")
	if mirroredHandoff.Status != cairnline.HandoffStatusAccepted {
		t.Fatalf("mirrored accepted handoff = %+v, want accepted", mirroredHandoff)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_release/handoffs/handoff_release", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete handoff status = %d body=%s, want 204", rec.Code, rec.Body.String())
	}
	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	if _, err := service.GetHandoff(t.Context(), project.Data.ID, "work_release", "handoff_release"); !errors.Is(err, cairnline.ErrNotFound) {
		store.Close()
		t.Fatalf("deleted mirrored handoff error = %v, want ErrNotFound", err)
	}
	store.Close()

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_release/assignments/asgn_release", bytes.NewReader([]byte(`{
		"status":"completed",
		"execution_ref":{"kind":"chat_session","chat_session_id":"chat_release","message_id":"msg_release"}
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("update assignment status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	mirroredAssignment = getMirroredCairnlineAssignmentForTest(t, handler, project.Data.ID, "asgn_release")
	if mirroredAssignment.Status != cairnline.AssignmentCompleted || mirroredAssignment.ExecutionRef != "chat_release" {
		t.Fatalf("mirrored completed assignment = %+v, want completed chat execution ref", mirroredAssignment)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_release/assignments/asgn_release", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete assignment status = %d body=%s, want 204", rec.Code, rec.Body.String())
	}
	service, store, err = cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	if _, err := service.GetAssignment(t.Context(), project.Data.ID, "asgn_release"); !errors.Is(err, cairnline.ErrNotFound) {
		store.Close()
		t.Fatalf("deleted mirrored assignment error = %v, want ErrNotFound", err)
	}
	store.Close()

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_release", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete work item status = %d body=%s, want 204", rec.Code, rec.Body.String())
	}
	service, store, err = cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	if _, err := service.GetWorkItem(t.Context(), project.Data.ID, "work_release"); !errors.Is(err, cairnline.ErrNotFound) {
		store.Close()
		t.Fatalf("deleted mirrored work item error = %v, want ErrNotFound", err)
	}
	store.Close()

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/projects/"+project.Data.ID+"/roles/role_release", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete role status = %d body=%s, want 204", rec.Code, rec.Body.String())
	}
	if role := mirroredCairnlineRoleForTest(t, handler, project.Data.ID, "role_release"); role != nil {
		t.Fatalf("mirrored deleted role = %+v, want absent", *role)
	}
}

func TestProjectWorkAPI_RolesUseCairnlineSidecarWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineSidecarReadTestServer(t, "full")
	if handler.projectReadRoutesUseCairnlineReadModel() {
		t.Fatal("sidecar role list enabled embedded Cairnline read-model routes")
	}
	if !handler.projectCairnlineSidecarReadRoutesEnabled() {
		t.Fatal("sidecar read-route predicate = false, want true")
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/roles", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("roles status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectWorkRolesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode roles response: %v", err)
	}
	if response.Object != "project_roles" {
		t.Fatalf("roles object = %q, want project_roles", response.Object)
	}
	role := findProjectWorkRoleForTest(t, response.Data, "role_fixture")
	if role.ProjectID != "proj_fixture" || role.ReadBackend != "cairnline" || role.BuiltIn {
		t.Fatalf("role = %+v, want sidecar Cairnline non-built-in fixture role", role)
	}
	if role.Name != "Fixture Reviewer" || role.DefaultDriverKind != "mcp_pull" || role.DefaultAgentProfile != "profile_fixture" {
		t.Fatalf("role defaults = %+v, want portable sidecar role defaults", role)
	}
	if !reflect.DeepEqual(role.SkillIDs, []string{"skill_fixture"}) {
		t.Fatalf("role skill ids = %+v, want fixture skill id", role.SkillIDs)
	}
}

func TestProjectWorkAPI_StrictEmbeddedReadModelReadsRolesWithoutHecateProject(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend: "cairnline",
			CairnlineReadSource: "embedded",
		},
	}, quietLogger(), nil, nil, nil, nil)
	server := NewServer(quietLogger(), handler)
	const projectID = "proj_embedded_roles"

	if err := handler.withCairnlineEmbeddedMirrorService(t.Context(), func(service *cairnline.Service) error {
		if _, err := service.CreateExecutionProfile(t.Context(), cairnline.ExecutionProfile{
			ID:           "exec_external",
			Name:         "External adapter",
			ProviderHint: "anthropic",
			ModelHint:    "claude-sonnet-4-5",
		}); err != nil {
			return err
		}
		if _, err := service.CreateAgentProfile(t.Context(), cairnline.AgentProfile{
			ID:   "profile_review",
			Name: "Review profile",
		}); err != nil {
			return err
		}
		if _, err := service.CreateProject(t.Context(), cairnline.Project{
			ID:   projectID,
			Name: "Embedded Roles",
		}); err != nil {
			return err
		}
		_, err := service.CreateRole(t.Context(), cairnline.Role{
			ID:                        "role_reviewer",
			ProjectID:                 projectID,
			Name:                      "Reviewer",
			Description:               "Reviews completed assignments.",
			Instructions:              "Inspect evidence before approving.",
			DefaultProfileID:          "profile_review",
			DefaultExecutionProfileID: "exec_external",
			DefaultSkillIDs:           []string{"review"},
			DefaultExecutionMode:      cairnline.ExecutionExternalAdapter,
		})
		return err
	}); err != nil {
		t.Fatalf("seed embedded Cairnline roles: %v", err)
	}
	if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("Hecate project store seeded ok=%v err=%v, want no project row", ok, err)
	}
	requireCairnlineOnlyProjectReadsForTest(t, handler, projectID)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+projectID+"/roles", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("roles status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectWorkRolesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode roles response: %v", err)
	}
	role := findProjectWorkRoleForTest(t, response.Data, "role_reviewer")
	if role.ProjectID != projectID || role.ReadBackend != "cairnline" || role.BuiltIn {
		t.Fatalf("role = %+v, want embedded Cairnline non-built-in role", role)
	}
	if role.Name != "Reviewer" || role.DefaultDriverKind != projectwork.AssignmentDriverExternalAgent || role.DefaultAgentProfile != "profile_review" {
		t.Fatalf("role defaults = %+v, want embedded Cairnline role defaults", role)
	}
	if role.DefaultProvider != "anthropic" || role.DefaultModel != "claude-sonnet-4-5" {
		t.Fatalf("role provider/model = %q/%q, want execution profile hints", role.DefaultProvider, role.DefaultModel)
	}
	if !reflect.DeepEqual(role.SkillIDs, []string{"review"}) {
		t.Fatalf("role skill ids = %+v, want review skill id", role.SkillIDs)
	}

	missingRec := httptest.NewRecorder()
	server.ServeHTTP(missingRec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_missing/roles", nil))
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d body=%s, want 404", missingRec.Code, missingRec.Body.String())
	}
}

func TestProjectWorkAPI_RolesCairnlineSidecarReadRequiresStructuredContent(t *testing.T) {
	t.Parallel()
	_, server := newProjectsCairnlineSidecarReadTestServer(t, "text-only")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/roles", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("roles status = %d body=%s, want 502", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "structuredContent") {
		t.Fatalf("error body = %s, want structuredContent diagnostic", rec.Body.String())
	}
}

func TestProjectWorkAPI_WorkItemsUseCairnlineSidecarWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineSidecarReadTestServer(t, "full")
	if handler.projectReadRoutesUseCairnlineReadModel() {
		t.Fatal("sidecar work-item list enabled embedded Cairnline read-model routes")
	}
	if !handler.projectCairnlineSidecarReadRoutesEnabled() {
		t.Fatal("sidecar read-route predicate = false, want true")
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/work-items", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("work-items status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectWorkItemsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode work-items response: %v", err)
	}
	if response.Object != "project_work_items" {
		t.Fatalf("work-items object = %q, want project_work_items", response.Object)
	}
	item := findProjectWorkItemForTest(t, response.Data, "work_fixture")
	if item.ProjectID != "proj_fixture" || item.ReadBackend != "cairnline" || item.Title != "Fixture Work" {
		t.Fatalf("work item = %+v, want sidecar Cairnline fixture work item", item)
	}
	if item.Status != "open" || item.Priority != "normal" {
		t.Fatalf("work item status/priority = %q/%q, want portable sidecar values", item.Status, item.Priority)
	}
	if len(item.Assignments) != 1 || item.Assignments[0].ID != "asg_fixture" || item.Assignments[0].ReadBackend != "cairnline" || item.Assignments[0].RoleID != "role_fixture" {
		t.Fatalf("work item assignments = %+v, want sidecar assignment summary", item.Assignments)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/work-items/work_fixture", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("work-item detail status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var detail ProjectWorkItemEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode work-item detail response: %v", err)
	}
	if detail.Object != "project_work_item" || detail.Data.ID != "work_fixture" || detail.Data.ReadBackend != "cairnline" || len(detail.Data.Assignments) != 1 {
		t.Fatalf("work-item detail = %+v, want sidecar Cairnline detail", detail)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/work-items/missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing work-item status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
}

func TestProjectWorkAPI_StrictEmbeddedReadModelReadsWorkItemsWithoutHecateProject(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend: "cairnline",
			CairnlineReadSource: "embedded",
		},
	}, quietLogger(), nil, nil, nil, nil)
	server := NewServer(quietLogger(), handler)
	const projectID = "proj_embedded_work"

	if err := handler.withCairnlineEmbeddedMirrorService(t.Context(), func(service *cairnline.Service) error {
		if _, err := service.CreateProject(t.Context(), cairnline.Project{
			ID:   projectID,
			Name: "Embedded Work",
		}); err != nil {
			return err
		}
		if _, err := service.CreateRole(t.Context(), cairnline.Role{
			ID:        "role_implementer",
			ProjectID: projectID,
			Name:      "Implementer",
		}); err != nil {
			return err
		}
		if _, err := service.CreateWorkItem(t.Context(), cairnline.WorkItem{
			ID:          "work_embedded",
			ProjectID:   projectID,
			Title:       "Wire direct work reads",
			Brief:       "Exercise embedded Cairnline work-item projection.",
			Status:      cairnline.WorkStatusReady,
			Priority:    cairnline.PriorityNormal,
			OwnerRoleID: "role_implementer",
		}); err != nil {
			return err
		}
		_, err := service.CreateAssignment(t.Context(), cairnline.Assignment{
			ID:            "asgn_embedded",
			ProjectID:     projectID,
			WorkItemID:    "work_embedded",
			RoleID:        "role_implementer",
			Status:        cairnline.AssignmentQueued,
			ExecutionMode: cairnline.ExecutionMCPPull,
		})
		return err
	}); err != nil {
		t.Fatalf("seed embedded Cairnline work items: %v", err)
	}
	if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("Hecate project store seeded ok=%v err=%v, want no project row", ok, err)
	}
	requireCairnlineOnlyProjectReadsForTest(t, handler, projectID)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+projectID+"/work-items", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("work-items status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectWorkItemsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode work-items response: %v", err)
	}
	item := findProjectWorkItemForTest(t, response.Data, "work_embedded")
	if item.ProjectID != projectID || item.ReadBackend != "cairnline" || item.Title != "Wire direct work reads" {
		t.Fatalf("work item = %+v, want embedded Cairnline work item", item)
	}
	if item.Status != cairnline.WorkStatusReady || item.Priority != cairnline.PriorityNormal || item.OwnerRoleID != "role_implementer" {
		t.Fatalf("work item metadata = %+v, want embedded Cairnline metadata", item)
	}
	if len(item.Assignments) != 1 || item.Assignments[0].ID != "asgn_embedded" || item.Assignments[0].ReadBackend != "cairnline" || item.Assignments[0].RoleID != "role_implementer" {
		t.Fatalf("work item assignments = %+v, want embedded Cairnline assignment summary", item.Assignments)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+projectID+"/work-items/work_embedded", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("work-item detail status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var detail ProjectWorkItemEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode work-item detail response: %v", err)
	}
	if detail.Object != "project_work_item" || detail.Data.ID != "work_embedded" || detail.Data.ReadBackend != "cairnline" || len(detail.Data.Assignments) != 1 {
		t.Fatalf("work-item detail = %+v, want embedded Cairnline detail", detail)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+projectID+"/work-items/missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing work-item status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_missing/work-items", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing project status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
}

func TestProjectWorkAPI_WorkItemsCairnlineSidecarReadRequiresStructuredContent(t *testing.T) {
	t.Parallel()
	_, server := newProjectsCairnlineSidecarReadTestServer(t, "text-only")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/work-items", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("work-items status = %d body=%s, want 502", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "structuredContent") {
		t.Fatalf("error body = %s, want structuredContent diagnostic", rec.Body.String())
	}
}

func TestProjectWorkAPI_AssignmentsUseCairnlineSidecarWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineSidecarReadTestServer(t, "full")
	if handler.projectReadRoutesUseCairnlineReadModel() {
		t.Fatal("sidecar assignment list enabled embedded Cairnline read-model routes")
	}
	if !handler.projectCairnlineSidecarReadRoutesEnabled() {
		t.Fatal("sidecar read-route predicate = false, want true")
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/work-items/work_fixture/assignments", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("assignments status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectWorkAssignmentsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode assignments response: %v", err)
	}
	if response.Object != "project_assignments" || len(response.Data) != 1 {
		t.Fatalf("assignments response = %+v, want one sidecar assignment", response)
	}
	assignment := response.Data[0]
	if assignment.ID != "asg_fixture" || assignment.ProjectID != "proj_fixture" || assignment.WorkItemID != "work_fixture" || assignment.ReadBackend != "cairnline" {
		t.Fatalf("assignment = %+v, want sidecar Cairnline fixture assignment", assignment)
	}
	if assignment.RoleID != "role_fixture" || assignment.DriverKind != projectwork.AssignmentDriverHecateTask || assignment.Status != projectwork.AssignmentStatusQueued {
		t.Fatalf("assignment defaults = %+v, want portable sidecar assignment metadata", assignment)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/work-items/missing/assignments", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing work-item assignments status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
}

func TestProjectWorkAPI_StrictEmbeddedReadModelReadsAssignmentsWithoutHecateProject(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend: "cairnline",
			CairnlineReadSource: "embedded",
		},
	}, quietLogger(), nil, nil, nil, nil)
	server := NewServer(quietLogger(), handler)
	const projectID = "proj_embedded_assignments"

	if err := handler.withCairnlineEmbeddedMirrorService(t.Context(), func(service *cairnline.Service) error {
		if _, err := service.CreateProject(t.Context(), cairnline.Project{
			ID:   projectID,
			Name: "Embedded Assignments",
		}); err != nil {
			return err
		}
		if _, err := service.CreateRole(t.Context(), cairnline.Role{
			ID:        "role_implementer",
			ProjectID: projectID,
			Name:      "Implementer",
		}); err != nil {
			return err
		}
		if _, err := service.CreateWorkItem(t.Context(), cairnline.WorkItem{
			ID:        "work_embedded",
			ProjectID: projectID,
			Title:     "Read embedded assignments",
			Status:    cairnline.WorkStatusReady,
			Priority:  cairnline.PriorityNormal,
		}); err != nil {
			return err
		}
		if _, err := service.CreateWorkItem(t.Context(), cairnline.WorkItem{
			ID:        "work_other",
			ProjectID: projectID,
			Title:     "Other work",
			Status:    cairnline.WorkStatusReady,
			Priority:  cairnline.PriorityNormal,
		}); err != nil {
			return err
		}
		if _, err := service.CreateAssignment(t.Context(), cairnline.Assignment{
			ID:            "asgn_embedded",
			ProjectID:     projectID,
			WorkItemID:    "work_embedded",
			RoleID:        "role_implementer",
			Status:        cairnline.AssignmentQueued,
			ExecutionMode: cairnline.ExecutionMCPPull,
		}); err != nil {
			return err
		}
		_, err := service.CreateAssignment(t.Context(), cairnline.Assignment{
			ID:            "asgn_other",
			ProjectID:     projectID,
			WorkItemID:    "work_other",
			RoleID:        "role_implementer",
			Status:        cairnline.AssignmentQueued,
			ExecutionMode: cairnline.ExecutionMCPPull,
		})
		return err
	}); err != nil {
		t.Fatalf("seed embedded Cairnline assignments: %v", err)
	}
	if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("Hecate project store seeded ok=%v err=%v, want no project row", ok, err)
	}
	requireCairnlineOnlyProjectReadsForTest(t, handler, projectID)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+projectID+"/work-items/work_embedded/assignments", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("assignments status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectWorkAssignmentsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode assignments response: %v", err)
	}
	if response.Object != "project_assignments" || len(response.Data) != 1 {
		t.Fatalf("assignments response = %+v, want one embedded Cairnline assignment", response)
	}
	assignment := response.Data[0]
	if assignment.ID != "asgn_embedded" || assignment.ProjectID != projectID || assignment.WorkItemID != "work_embedded" || assignment.ReadBackend != "cairnline" {
		t.Fatalf("assignment = %+v, want embedded Cairnline assignment", assignment)
	}
	if assignment.RoleID != "role_implementer" || assignment.DriverKind != projectwork.AssignmentDriverHecateTask || assignment.Status != projectwork.AssignmentStatusQueued {
		t.Fatalf("assignment defaults = %+v, want embedded Cairnline assignment metadata", assignment)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+projectID+"/work-items/missing/assignments", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing work-item assignments status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_missing/work-items/work_embedded/assignments", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing project assignments status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
}

func TestProjectWorkAPI_AssignmentsCairnlineSidecarReadRequiresStructuredContent(t *testing.T) {
	t.Parallel()
	_, server := newProjectsCairnlineSidecarReadTestServer(t, "text-only")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/work-items/work_fixture/assignments", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("assignments status = %d body=%s, want 502", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "structuredContent") {
		t.Fatalf("error body = %s, want structuredContent diagnostic", rec.Body.String())
	}
}

func TestProjectWorkAPI_ArtifactsUseCairnlineSidecarWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineSidecarReadTestServer(t, "collaboration-fixture")
	if handler.projectReadRoutesUseCairnlineReadModel() {
		t.Fatal("sidecar artifact list enabled embedded Cairnline read-model routes")
	}
	if !handler.projectCairnlineSidecarReadRoutesEnabled() {
		t.Fatal("sidecar read-route predicate = false, want true")
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/work-items/work_fixture/artifacts", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("artifacts status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectWorkArtifactsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode artifacts response: %v", err)
	}
	if response.Object != "project_collaboration_artifacts" || len(response.Data) != 3 {
		t.Fatalf("artifacts response = %+v, want generic artifact, evidence, and review", response)
	}
	if response.Data[0].ID != "artifact_fixture" || response.Data[0].Kind != projectwork.ArtifactKindDecisionNote || response.Data[0].ReadBackend != "cairnline" {
		t.Fatalf("generic artifact = %+v, want Cairnline-backed decision note", response.Data[0])
	}
	if response.Data[1].ID != "evidence_fixture" || response.Data[1].Kind != projectwork.ArtifactKindEvidenceLink || response.Data[1].EvidenceURL == "" || response.Data[1].ReadBackend != "cairnline" {
		t.Fatalf("evidence artifact = %+v, want Cairnline-backed evidence link", response.Data[1])
	}
	if response.Data[2].ID != "review_fixture" || response.Data[2].Kind != projectwork.ArtifactKindReview || response.Data[2].ReviewVerdict != projectwork.ReviewVerdictChangesRequested || !response.Data[2].ReviewFollowUpRequired || response.Data[2].ReadBackend != "cairnline" {
		t.Fatalf("review artifact = %+v, want Cairnline-backed review", response.Data[2])
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/work-items/missing/artifacts", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing work-item artifacts status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
}

func TestProjectWorkAPI_StrictEmbeddedReadModelReadsArtifactsWithoutHecateProject(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend: "cairnline",
			CairnlineReadSource: "embedded",
		},
	}, quietLogger(), nil, nil, nil, nil)
	server := NewServer(quietLogger(), handler)
	const projectID = "proj_embedded_artifacts"

	if err := handler.withCairnlineEmbeddedMirrorService(t.Context(), func(service *cairnline.Service) error {
		if _, err := service.CreateProject(t.Context(), cairnline.Project{
			ID:   projectID,
			Name: "Embedded Artifacts",
		}); err != nil {
			return err
		}
		if _, err := service.CreateRole(t.Context(), cairnline.Role{
			ID:        "role_reviewer",
			ProjectID: projectID,
			Name:      "Reviewer",
		}); err != nil {
			return err
		}
		if _, err := service.CreateWorkItem(t.Context(), cairnline.WorkItem{
			ID:        "work_embedded",
			ProjectID: projectID,
			Title:     "Read embedded artifacts",
			Status:    cairnline.WorkStatusReady,
			Priority:  cairnline.PriorityNormal,
		}); err != nil {
			return err
		}
		if _, err := service.CreateAssignment(t.Context(), cairnline.Assignment{
			ID:            "asgn_embedded",
			ProjectID:     projectID,
			WorkItemID:    "work_embedded",
			RoleID:        "role_reviewer",
			Status:        cairnline.AssignmentCompleted,
			ExecutionMode: cairnline.ExecutionMCPPull,
		}); err != nil {
			return err
		}
		if _, err := service.CreateArtifact(t.Context(), cairnline.Artifact{
			ID:           "artifact_embedded",
			ProjectID:    projectID,
			WorkItemID:   "work_embedded",
			AssignmentID: "asgn_embedded",
			Kind:         projectwork.ArtifactKindDecisionNote,
			Title:        "Decision",
			Body:         "Use direct embedded artifact reads.",
			AuthorRoleID: "role_reviewer",
		}); err != nil {
			return err
		}
		if _, err := service.CreateEvidence(t.Context(), cairnline.Evidence{
			ID:           "evidence_embedded",
			ProjectID:    projectID,
			WorkItemID:   "work_embedded",
			AssignmentID: "asgn_embedded",
			Title:        "Evidence",
			Body:         "Runtime evidence.",
			Locator:      "https://example.test/evidence",
			ExternalID:   "EVID-1",
			Provider:     "test",
			TrustLabel:   cairnline.EvidenceTrustOperator,
		}); err != nil {
			return err
		}
		_, err := service.CreateReview(t.Context(), cairnline.Review{
			ID:             "review_embedded",
			ProjectID:      projectID,
			WorkItemID:     "work_embedded",
			AssignmentID:   "asgn_embedded",
			ReviewerRoleID: "role_reviewer",
			Title:          "Review",
			Body:           "Needs a follow-up.",
			Verdict:        cairnline.ReviewVerdictChangesRequested,
			Risk:           cairnline.ReviewRiskMedium,
			Status:         cairnline.ReviewStatusRecorded,
		})
		return err
	}); err != nil {
		t.Fatalf("seed embedded Cairnline artifacts: %v", err)
	}
	if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("Hecate project store seeded ok=%v err=%v, want no project row", ok, err)
	}
	requireCairnlineOnlyProjectReadsForTest(t, handler, projectID)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+projectID+"/work-items/work_embedded/artifacts", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("artifacts status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectWorkArtifactsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode artifacts response: %v", err)
	}
	if response.Object != "project_collaboration_artifacts" || len(response.Data) != 3 {
		t.Fatalf("artifacts response = %+v, want embedded generic artifact, evidence, and review", response)
	}
	artifact := findProjectWorkArtifactForTest(t, response.Data, "artifact_embedded")
	if artifact.ReadBackend != "cairnline" || artifact.Kind != projectwork.ArtifactKindDecisionNote || artifact.AssignmentID != "asgn_embedded" {
		t.Fatalf("generic artifact = %+v, want embedded Cairnline decision artifact", artifact)
	}
	evidence := findProjectWorkArtifactForTest(t, response.Data, "evidence_embedded")
	if evidence.ReadBackend != "cairnline" || evidence.Kind != projectwork.ArtifactKindEvidenceLink || evidence.EvidenceURL != "https://example.test/evidence" || evidence.EvidenceExternalID != "EVID-1" || evidence.EvidenceProvider != "test" {
		t.Fatalf("evidence artifact = %+v, want embedded Cairnline evidence artifact", evidence)
	}
	review := findProjectWorkArtifactForTest(t, response.Data, "review_embedded")
	if review.ReadBackend != "cairnline" || review.Kind != projectwork.ArtifactKindReview || review.ReviewedAssignmentID != "asgn_embedded" || review.ReviewVerdict != projectwork.ReviewVerdictChangesRequested || review.ReviewRisk != projectwork.ReviewRiskMedium || !review.ReviewFollowUpRequired {
		t.Fatalf("review artifact = %+v, want embedded Cairnline review artifact", review)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+projectID+"/work-items/missing/artifacts", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing work-item artifacts status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_missing/work-items/work_embedded/artifacts", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing project artifacts status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
}

func TestProjectWorkAPI_ArtifactsCairnlineSidecarReadRequiresStructuredContent(t *testing.T) {
	t.Parallel()
	_, server := newProjectsCairnlineSidecarReadTestServer(t, "text-only")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/work-items/work_fixture/artifacts", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("artifacts status = %d body=%s, want 502", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "structuredContent") {
		t.Fatalf("error body = %s, want structuredContent diagnostic", rec.Body.String())
	}
}

func TestProjectWorkAPI_HandoffsUseCairnlineSidecarWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineSidecarReadTestServer(t, "collaboration-fixture")
	if handler.projectReadRoutesUseCairnlineReadModel() {
		t.Fatal("sidecar handoff list enabled embedded Cairnline read-model routes")
	}
	if !handler.projectCairnlineSidecarReadRoutesEnabled() {
		t.Fatal("sidecar read-route predicate = false, want true")
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/handoffs?work_item_id=work_fixture&status=pending", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("project handoffs status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectHandoffsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode project handoffs response: %v", err)
	}
	if response.Object != "project_handoffs" || len(response.Data) != 1 {
		t.Fatalf("project handoffs response = %+v, want one filtered handoff", response)
	}
	handoff := response.Data[0]
	if handoff.ID != "handoff_fixture" || handoff.ProjectID != "proj_fixture" || handoff.WorkItemID != "work_fixture" || handoff.Status != projectwork.HandoffStatusPending || handoff.ReadBackend != "cairnline" {
		t.Fatalf("project handoff = %+v, want sidecar Cairnline fixture handoff", handoff)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/work-items/work_fixture/handoffs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("work-item handoffs status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode work-item handoffs response: %v", err)
	}
	if len(response.Data) != 1 || response.Data[0].ID != "handoff_fixture" || response.Data[0].ReadBackend != "cairnline" {
		t.Fatalf("work-item handoffs response = %+v, want sidecar handoff", response)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/work-items/missing/handoffs", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing work-item handoffs status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
}

func TestProjectWorkAPI_StrictEmbeddedReadModelReadsHandoffsWithoutHecateProject(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend: "cairnline",
			CairnlineReadSource: "embedded",
		},
	}, quietLogger(), nil, nil, nil, nil)
	server := NewServer(quietLogger(), handler)
	const projectID = "proj_embedded_handoffs"

	if err := handler.withCairnlineEmbeddedMirrorService(t.Context(), func(service *cairnline.Service) error {
		if _, err := service.CreateProject(t.Context(), cairnline.Project{
			ID:   projectID,
			Name: "Embedded Handoffs",
		}); err != nil {
			return err
		}
		if _, err := service.CreateRole(t.Context(), cairnline.Role{
			ID:        "role_source",
			ProjectID: projectID,
			Name:      "Source",
		}); err != nil {
			return err
		}
		if _, err := service.CreateRole(t.Context(), cairnline.Role{
			ID:        "role_target",
			ProjectID: projectID,
			Name:      "Target",
		}); err != nil {
			return err
		}
		if _, err := service.CreateWorkItem(t.Context(), cairnline.WorkItem{
			ID:        "work_embedded",
			ProjectID: projectID,
			Title:     "Read embedded handoffs",
			Status:    cairnline.WorkStatusReady,
			Priority:  cairnline.PriorityNormal,
		}); err != nil {
			return err
		}
		if _, err := service.CreateWorkItem(t.Context(), cairnline.WorkItem{
			ID:        "work_other",
			ProjectID: projectID,
			Title:     "Other handoffs",
			Status:    cairnline.WorkStatusReady,
			Priority:  cairnline.PriorityNormal,
		}); err != nil {
			return err
		}
		if _, err := service.CreateAssignment(t.Context(), cairnline.Assignment{
			ID:            "asgn_embedded",
			ProjectID:     projectID,
			WorkItemID:    "work_embedded",
			RoleID:        "role_source",
			Status:        cairnline.AssignmentCompleted,
			ExecutionMode: cairnline.ExecutionMCPPull,
		}); err != nil {
			return err
		}
		if _, err := service.CreateHandoff(t.Context(), cairnline.Handoff{
			ID:                    "handoff_embedded",
			ProjectID:             projectID,
			WorkItemID:            "work_embedded",
			SourceAssignmentID:    "asgn_embedded",
			FromRoleID:            "role_source",
			ToRoleID:              "role_target",
			Title:                 "Continue embedded handoff reads",
			Body:                  "Pick up the next review step.",
			RecommendedNextAction: "Review evidence",
			LinkedArtifactIDs:     []string{"artifact_embedded"},
			ContextRefs:           []string{"ctx:embedded"},
			Status:                cairnline.HandoffStatusOpen,
			TrustLabel:            "operator",
		}); err != nil {
			return err
		}
		_, err := service.CreateHandoff(t.Context(), cairnline.Handoff{
			ID:         "handoff_other",
			ProjectID:  projectID,
			WorkItemID: "work_other",
			Title:      "Other handoff",
			Body:       "Should not appear in work filter.",
			Status:     cairnline.HandoffStatusOpen,
		})
		return err
	}); err != nil {
		t.Fatalf("seed embedded Cairnline handoffs: %v", err)
	}
	if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("Hecate project store seeded ok=%v err=%v, want no project row", ok, err)
	}
	requireCairnlineOnlyProjectReadsForTest(t, handler, projectID)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+projectID+"/handoffs?work_item_id=work_embedded&status=pending", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("project handoffs status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectHandoffsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode project handoffs response: %v", err)
	}
	if response.Object != "project_handoffs" || len(response.Data) != 1 {
		t.Fatalf("project handoffs response = %+v, want one filtered embedded handoff", response)
	}
	handoff := findProjectHandoffForTest(t, response.Data, "handoff_embedded")
	if handoff.ReadBackend != "cairnline" || handoff.ProjectID != projectID || handoff.WorkItemID != "work_embedded" || handoff.Status != projectwork.HandoffStatusPending {
		t.Fatalf("project handoff = %+v, want embedded Cairnline pending handoff", handoff)
	}
	if handoff.SourceAssignmentID != "asgn_embedded" || handoff.TargetRoleID != "role_target" || handoff.RecommendedNextAction != "Review evidence" || len(handoff.ContextRefs) != 1 {
		t.Fatalf("project handoff metadata = %+v, want embedded Cairnline handoff metadata", handoff)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+projectID+"/work-items/work_embedded/handoffs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("work-item handoffs status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode work-item handoffs response: %v", err)
	}
	if len(response.Data) != 1 || response.Data[0].ID != "handoff_embedded" || response.Data[0].ReadBackend != "cairnline" {
		t.Fatalf("work-item handoffs response = %+v, want embedded Cairnline handoff", response)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+projectID+"/work-items/missing/handoffs", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing work-item handoffs status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_missing/handoffs", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing project handoffs status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
}

func TestProjectWorkAPI_HandoffsCairnlineSidecarReadRequiresStructuredContent(t *testing.T) {
	t.Parallel()
	_, server := newProjectsCairnlineSidecarReadTestServer(t, "text-only")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/work-items/work_fixture/handoffs", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("handoffs status = %d body=%s, want 502", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "structuredContent") {
		t.Fatalf("error body = %s, want structuredContent diagnostic", rec.Body.String())
	}
}

func TestProjectWorkAPI_StrictEmbeddedReadModelReadsCloseoutReadinessWithoutHecateProject(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend: "cairnline",
			CairnlineReadSource: "embedded",
		},
	}, quietLogger(), nil, nil, nil, nil)
	server := NewServer(quietLogger(), handler)
	const projectID = "proj_embedded_readiness"

	if err := handler.withCairnlineEmbeddedMirrorService(t.Context(), func(service *cairnline.Service) error {
		if _, err := service.CreateProject(t.Context(), cairnline.Project{
			ID:   projectID,
			Name: "Embedded Readiness",
		}); err != nil {
			return err
		}
		if _, err := service.CreateRole(t.Context(), cairnline.Role{
			ID:        "role_owner",
			ProjectID: projectID,
			Name:      "Owner",
		}); err != nil {
			return err
		}
		if _, err := service.CreateWorkItem(t.Context(), cairnline.WorkItem{
			ID:        "work_embedded",
			ProjectID: projectID,
			Title:     "Read embedded closeout readiness",
			Status:    cairnline.WorkStatusReady,
			Priority:  cairnline.PriorityNormal,
		}); err != nil {
			return err
		}
		if _, err := service.CreateAssignment(t.Context(), cairnline.Assignment{
			ID:            "asgn_embedded",
			ProjectID:     projectID,
			WorkItemID:    "work_embedded",
			RoleID:        "role_owner",
			ExecutionMode: cairnline.ExecutionMCPPull,
		}); err != nil {
			return err
		}
		if _, err := service.CompleteAssignment(t.Context(), projectID, "asgn_embedded", cairnline.AssignmentCompleted, "run_embedded"); err != nil {
			return err
		}
		_, err := service.CreateHandoff(t.Context(), cairnline.Handoff{
			ID:                 "handoff_embedded",
			ProjectID:          projectID,
			WorkItemID:         "work_embedded",
			SourceAssignmentID: "asgn_embedded",
			FromRoleID:         "role_owner",
			ToRoleID:           "role_owner",
			Title:              "Follow up before closeout",
			Body:               "Keep this work item open until reviewed.",
			Status:             cairnline.HandoffStatusOpen,
		})
		return err
	}); err != nil {
		t.Fatalf("seed embedded Cairnline readiness: %v", err)
	}
	if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("Hecate project store seeded ok=%v err=%v, want no project row", ok, err)
	}
	requireCairnlineOnlyProjectReadsForTest(t, handler, projectID)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+projectID+"/work-items/work_embedded/readiness", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("readiness status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectWorkItemReadinessEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode readiness response: %v", err)
	}
	if response.Object != "project_work_item_readiness" || response.Data.ProjectID != projectID || response.Data.WorkItemID != "work_embedded" || response.Data.ReadBackend != "cairnline" {
		t.Fatalf("readiness response = %+v, want embedded Cairnline readiness", response)
	}
	if response.Data.Ready || response.Data.Status != "blocked" || response.Data.AssignmentCount != 1 || response.Data.CompletedAssignments != 1 {
		t.Fatalf("readiness = %+v, want blocked embedded closeout state", response.Data)
	}
	if !containsString(response.Data.MissingEvidenceAssignmentIDs, "asgn_embedded") || !containsString(response.Data.Blockers, "1 completed assignment is missing evidence") || !containsString(response.Data.Blockers, "1 handoff is pending") {
		t.Fatalf("readiness blockers = %+v missing=%+v, want missing evidence and pending handoff blockers", response.Data.Blockers, response.Data.MissingEvidenceAssignmentIDs)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+projectID+"/work-items/missing/readiness", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing work-item readiness status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_missing/work-items/work_embedded/readiness", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing project readiness status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
}

func TestProjectWorkAPI_CloseoutReadinessUsesCairnlineSidecarWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineSidecarReadTestServer(t, "collaboration-fixture")
	if handler.projectReadRoutesUseCairnlineReadModel() {
		t.Fatal("sidecar closeout readiness enabled embedded Cairnline read-model routes")
	}
	if !handler.projectCairnlineSidecarReadRoutesEnabled() {
		t.Fatal("sidecar read-route predicate = false, want true")
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/work-items/work_fixture/readiness", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("readiness status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectWorkItemReadinessEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode readiness response: %v", err)
	}
	if response.Object != "project_work_item_readiness" || response.Data.ProjectID != "proj_fixture" || response.Data.WorkItemID != "work_fixture" || response.Data.ReadBackend != "cairnline" {
		t.Fatalf("readiness response = %+v, want sidecar Cairnline readiness", response)
	}
	if response.Data.Ready || response.Data.Status != "blocked" || response.Data.AssignmentCount != 1 || response.Data.CompletedAssignments != 0 {
		t.Fatalf("readiness = %+v, want blocked sidecar closeout state", response.Data)
	}
	if !containsString(response.Data.Blockers, "1 assignment is still active") || !containsString(response.Data.Blockers, "1 handoff is pending") || response.Data.ReviewFollowUpCount != 1 {
		t.Fatalf("readiness blockers = %+v follow-ups=%d, want active assignment, pending handoff, and review follow-up", response.Data.Blockers, response.Data.ReviewFollowUpCount)
	}
	if len(response.Data.ReviewFollowUps) != 1 || response.Data.ReviewFollowUps[0].ArtifactID != "review_fixture" || response.Data.ReviewFollowUps[0].ReviewVerdict != projectwork.ReviewVerdictChangesRequested {
		t.Fatalf("review follow-ups = %+v, want sidecar review follow-up", response.Data.ReviewFollowUps)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/work-items/missing/readiness", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing work-item readiness status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
}

func TestProjectWorkAPI_CloseoutReadinessCairnlineSidecarReadRequiresStructuredContent(t *testing.T) {
	t.Parallel()
	_, server := newProjectsCairnlineSidecarReadTestServer(t, "text-only")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/work-items/work_fixture/readiness", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("readiness status = %d body=%s, want 502", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "structuredContent") {
		t.Fatalf("error body = %s, want structuredContent diagnostic", rec.Body.String())
	}
}

func TestProjectWorkAPI_CreateHandoffGeneratesOpaqueHandoffID(t *testing.T) {
	t.Parallel()
	_, server := newProjectWorkTestServer()
	project := createProjectForWorkTest(t, server)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/work-items", bytes.NewReader([]byte(`{
		"id":"work_generated_handoff",
		"title":"Generated handoff"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create work item status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_generated_handoff/handoffs", bytes.NewReader([]byte(`{
		"title":"Generated handoff",
		"summary":"Ready for the next operator step.",
		"recommended_next_action":"Review the generated handoff ID."
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create handoff status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created ProjectHandoffEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode handoff: %v", err)
	}
	if !strings.HasPrefix(created.Data.ID, "handoff_") {
		t.Fatalf("generated handoff id = %q, want handoff_ prefix", created.Data.ID)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_generated_handoff/handoffs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list handoffs status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var handoffs ProjectHandoffsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &handoffs); err != nil {
		t.Fatalf("decode handoffs: %v", err)
	}
	if len(handoffs.Data) != 1 || handoffs.Data[0].ID != created.Data.ID {
		t.Fatalf("handoffs = %+v, want generated handoff id %q", handoffs.Data, created.Data.ID)
	}
}

func TestProjectWorkAPI_PatchDoneRequiresCloseoutReadiness(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	if _, err := handler.projects.Create(t.Context(), projects.Project{
		ID:   "proj_closeout_guard",
		Name: "Closeout Guard",
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	if _, err := handler.projectWork.CreateWorkItem(t.Context(), projectwork.WorkItem{
		ID:        "work_guard",
		ProjectID: "proj_closeout_guard",
		Title:     "Guard closeout",
		Status:    projectwork.WorkItemStatusReview,
	}); err != nil {
		t.Fatalf("CreateWorkItem: %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(t.Context(), projectwork.Assignment{
		ID:         "asgn_guard",
		ProjectID:  "proj_closeout_guard",
		WorkItemID: "work_guard",
		RoleID:     "software_developer",
		Status:     projectwork.AssignmentStatusCompleted,
	}); err != nil {
		t.Fatalf("CreateAssignment: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/proj_closeout_guard/work-items/work_guard", bytes.NewReader([]byte(`{"status":"done"}`))))
	if rec.Code != http.StatusConflict {
		t.Fatalf("patch done status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}
	var blocked struct {
		Error struct {
			Type      string                           `json:"type"`
			Message   string                           `json:"message"`
			Readiness ProjectWorkItemReadinessResponse `json:"readiness"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &blocked); err != nil {
		t.Fatalf("decode blocked closeout: %v", err)
	}
	if blocked.Error.Type != errCodeConflict || blocked.Error.Message != projectworkapp.ErrWorkItemCloseoutBlocked.Error() {
		t.Fatalf("blocked error = %+v, want closeout conflict", blocked.Error)
	}
	if blocked.Error.Readiness.Ready || blocked.Error.Readiness.Status != "blocked" || len(blocked.Error.Readiness.MissingEvidenceAssignmentIDs) != 1 || blocked.Error.Readiness.MissingEvidenceAssignmentIDs[0] != "asgn_guard" {
		t.Fatalf("blocked readiness = %+v, want missing evidence blocker", blocked.Error.Readiness)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/proj_closeout_guard/work-items/work_guard", bytes.NewReader([]byte(`{"priority":"high"}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("patch priority status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var patched ProjectWorkItemEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode patched work item: %v", err)
	}
	if patched.Data.Priority != "high" {
		t.Fatalf("patched item = %+v, want priority update", patched.Data)
	}
	stored, ok, err := handler.projectWork.GetWorkItem(t.Context(), "proj_closeout_guard", "work_guard")
	if err != nil || !ok {
		t.Fatalf("GetWorkItem() ok=%v err=%v, want stored work item", ok, err)
	}
	if stored.Status == projectwork.WorkItemStatusDone {
		t.Fatalf("stored status = %q, want not done after priority-only update", stored.Status)
	}

	if _, err := handler.projectWork.CreateArtifact(t.Context(), projectwork.CollaborationArtifact{
		ID:           "artifact_guard_evidence",
		ProjectID:    "proj_closeout_guard",
		WorkItemID:   "work_guard",
		AssignmentID: "asgn_guard",
		Kind:         projectwork.ArtifactKindEvidenceLink,
		Title:        "Evidence",
		Body:         "Evidence recorded.",
		EvidenceURL:  "https://example.com/evidence",
	}); err != nil {
		t.Fatalf("CreateArtifact(evidence): %v", err)
	}
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/proj_closeout_guard/work-items/work_guard", bytes.NewReader([]byte(`{"status":"done"}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("patch done with evidence status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode closed work item: %v", err)
	}
	if patched.Data.Status != projectwork.WorkItemStatusDone {
		t.Fatalf("closed item status = %q, want done", patched.Data.Status)
	}
}

func TestProjectWorkAPI_WorkAndAssignmentRootIDs(t *testing.T) {
	t.Parallel()
	_, server := newProjectWorkTestServer()
	rootA := filepath.Join(t.TempDir(), "feature")
	rootB := filepath.Join(t.TempDir(), "review")
	projectBody := fmt.Sprintf(`{
		"name":"Rooted",
		"roots":[
			{"id":"root_feature","path":%q,"kind":"git","active":true},
			{"id":"root_review","path":%q,"kind":"git_worktree","active":true}
		],
		"default_root_id":"root_feature"
	}`, rootA, rootB)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(projectBody))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create project status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var project ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &project); err != nil {
		t.Fatalf("decode project: %v", err)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/work-items", bytes.NewReader([]byte(`{
		"id":"work_rooted",
		"title":"Rooted work",
		"root_id":"root_feature"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create rooted work status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var work ProjectWorkItemEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &work); err != nil {
		t.Fatalf("decode rooted work: %v", err)
	}
	if work.Data.RootID != "root_feature" {
		t.Fatalf("work root_id = %q, want root_feature", work.Data.RootID)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_rooted", bytes.NewReader([]byte(`{"root_id":"root_review"}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("patch rooted work status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &work); err != nil {
		t.Fatalf("decode patched rooted work: %v", err)
	}
	if work.Data.RootID != "root_review" {
		t.Fatalf("patched work root_id = %q, want root_review", work.Data.RootID)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_rooted/assignments", bytes.NewReader([]byte(`{
		"id":"asgn_missing_root",
		"role_id":"software_developer",
		"root_id":"root_missing"
	}`))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create assignment invalid root status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_rooted/assignments", bytes.NewReader([]byte(`{
		"id":"asgn_rooted",
		"role_id":"software_developer",
		"root_id":"root_feature"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create rooted assignment status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var assignment ProjectWorkAssignmentEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &assignment); err != nil {
		t.Fatalf("decode rooted assignment: %v", err)
	}
	if assignment.Data.RootID != "root_feature" {
		t.Fatalf("assignment root_id = %q, want root_feature", assignment.Data.RootID)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_rooted/assignments/asgn_rooted", bytes.NewReader([]byte(`{"root_id":"root_missing"}`))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("patch assignment invalid root status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
}

func TestProjectWorkAPI_ProjectDeletionCleansRows(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	project := createProjectForWorkTest(t, server)
	ctx := t.Context()
	if _, err := handler.projectWork.CreateRole(ctx, projectwork.AgentRoleProfile{ID: "role_custom", ProjectID: project.Data.ID, Name: "Custom"}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if _, err := handler.projectWork.CreateWorkItem(ctx, projectwork.WorkItem{ID: "work_cleanup", ProjectID: project.Data.ID, Title: "Cleanup"}); err != nil {
		t.Fatalf("CreateWorkItem: %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(ctx, projectwork.Assignment{ID: "asgn_cleanup", ProjectID: project.Data.ID, WorkItemID: "work_cleanup", RoleID: "software_developer"}); err != nil {
		t.Fatalf("CreateAssignment: %v", err)
	}
	if _, err := handler.projectRuntime.Upsert(ctx, projectruntime.AssignmentRuntime{ProjectID: project.Data.ID, AssignmentID: "asgn_cleanup"}); err != nil {
		t.Fatalf("UpsertRuntime: %v", err)
	}
	if _, err := handler.projectWork.CreateArtifact(ctx, projectwork.CollaborationArtifact{ID: "art_cleanup", ProjectID: project.Data.ID, WorkItemID: "work_cleanup", Kind: projectwork.ArtifactKindReview, Body: "Looks good."}); err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/projects/"+project.Data.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete project status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var deleted ProjectDeleteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &deleted); err != nil {
		t.Fatalf("decode delete response: %v", err)
	}
	if deleted.Data.ProjectID != project.Data.ID || deleted.Data.ProjectWorkRowsDeleted != 4 || deleted.Data.ProjectRuntimeRowsDeleted != 1 {
		t.Fatalf("delete response = %+v, want project id, 4 project work rows, and 1 runtime row", deleted)
	}
	if items, err := handler.projectWork.ListWorkItems(ctx, project.Data.ID); err != nil || len(items) != 0 {
		t.Fatalf("project work items after project delete = %+v err=%v, want none", items, err)
	}
	if roles, err := handler.projectWork.ListRoles(ctx, project.Data.ID); err != nil || projectWorkRoleExistsStore(roles, "role_custom", false) {
		t.Fatalf("project roles after project delete = %+v err=%v, want custom role gone", roles, err)
	}
	if _, ok, err := handler.projectRuntime.Get(ctx, project.Data.ID, "asgn_cleanup"); err != nil || ok {
		t.Fatalf("project runtime after project delete ok=%v err=%v, want none", ok, err)
	}
}

func TestProjectWorkAPI_CreateAssignmentUsesRoleDefaultDriver(t *testing.T) {
	t.Parallel()
	_, server := newProjectWorkTestServer()
	project := createProjectForWorkTest(t, server)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/roles", bytes.NewReader([]byte(`{
		"id":"role_external",
		"name":"External reviewer",
		"default_driver_kind":"external_agent"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create role status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/work-items", bytes.NewReader([]byte(`{
		"id":"work_external",
		"title":"External assignment default"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create work item status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_external/assignments", bytes.NewReader([]byte(`{
		"id":"asgn_external",
		"role_id":"role_external"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create assignment status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var assignment ProjectWorkAssignmentEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &assignment); err != nil {
		t.Fatalf("decode assignment: %v", err)
	}
	if assignment.Data.DriverKind != projectwork.AssignmentDriverExternalAgent {
		t.Fatalf("assignment driver_kind = %q, want role default external_agent", assignment.Data.DriverKind)
	}
}

func TestProjectWorkAPI_StartAssignmentCreatesNativeTaskRun(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	workspace := t.TempDir()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace: workspace,
		Driver:    projectwork.AssignmentDriverHecateTask,
	})

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("start assignment status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var assignment ProjectWorkAssignmentEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &assignment); err != nil {
		t.Fatalf("decode assignment: %v", err)
	}
	ref := assignmentExecutionRefForTest(t, assignment.Data)
	if ref.TaskID == "" || ref.RunID == "" {
		t.Fatalf("assignment execution_ref = %+v, want task and run links", ref)
	}
	if assignment.Data.DriverKind != projectwork.AssignmentDriverHecateTask {
		t.Fatalf("driver_kind = %q, want hecate_task", assignment.Data.DriverKind)
	}
	if assignment.Data.Execution == nil || assignment.Data.Execution.TaskID != ref.TaskID || assignment.Data.Execution.RunID != ref.RunID {
		t.Fatalf("assignment execution = %+v, want linked task/run summary", assignment.Data.Execution)
	}

	task, found, err := handler.taskStore.GetTask(t.Context(), ref.TaskID)
	if err != nil || !found {
		t.Fatalf("GetTask(%q) found=%v err=%v, want task", ref.TaskID, found, err)
	}
	if task.ExecutionKind != "agent_loop" || task.OriginKind != "project_work_item" || task.OriginID != "work_start" {
		t.Fatalf("task execution/origin = %q %q/%q, want agent_loop project_work_item/work_start", task.ExecutionKind, task.OriginKind, task.OriginID)
	}
	if task.ProjectID != "proj_start" || task.WorkItemID != "work_start" || task.AssignmentID != "asgn_start" {
		t.Fatalf("task project linkage = project %q work %q assignment %q, want proj_start/work_start/asgn_start", task.ProjectID, task.WorkItemID, task.AssignmentID)
	}
	client := newAPITestClient(t, server)
	taskResp := mustRequestJSON[TaskResponse](client, http.MethodGet, "/hecate/v1/tasks/"+ref.TaskID, "")
	if taskResp.Data.ProjectID != "proj_start" || taskResp.Data.WorkItemID != "work_start" || taskResp.Data.AssignmentID != "asgn_start" {
		t.Fatalf("task response linkage = project %q work %q assignment %q, want proj_start/work_start/asgn_start", taskResp.Data.ProjectID, taskResp.Data.WorkItemID, taskResp.Data.AssignmentID)
	}
	runResp := mustRequestJSON[TaskRunResponse](client, http.MethodGet, "/hecate/v1/tasks/"+ref.TaskID+"/runs/"+ref.RunID, "")
	if runResp.Data.ProjectID != "proj_start" || runResp.Data.WorkItemID != "work_start" || runResp.Data.AssignmentID != "asgn_start" {
		t.Fatalf("run response linkage = project %q work %q assignment %q, want proj_start/work_start/asgn_start", runResp.Data.ProjectID, runResp.Data.WorkItemID, runResp.Data.AssignmentID)
	}
	if task.RequestedProvider != "anthropic" || task.RequestedModel != "claude-sonnet-4" || task.ExecutionProfile != "coding_agent" {
		t.Fatalf("task provider/model/profile = %q/%q/%q, want role defaults", task.RequestedProvider, task.RequestedModel, task.ExecutionProfile)
	}
	if task.WorkingDirectory != workspace || task.SandboxAllowedRoot != workspace || task.WorkspaceMode != "in_place" {
		t.Fatalf("task workspace = dir %q root %q mode %q, want %q in_place", task.WorkingDirectory, task.SandboxAllowedRoot, task.WorkspaceMode, workspace)
	}
	if task.WorkspaceSystemPromptPolicy != types.WorkspaceSystemPromptExclude {
		t.Fatalf("task workspace prompt policy = %q, want exclude for profile-controlled project assignment context", task.WorkspaceSystemPromptPolicy)
	}
	for _, want := range []string{"Implement the native assignment start path.", "Follow backend invariants."} {
		if !strings.Contains(task.Prompt, want) {
			t.Fatalf("task prompt = %q, want %q", task.Prompt, want)
		}
	}
	assertLaunchContextContract(t, task.Prompt)
	for _, want := range []string{
		"Launch context",
		"Project: Hecate (proj_start)",
		"Work item:\n- Title: Native assignment start",
		"Assignment:\n- ID: asgn_start",
		"Role:\n- Name: Backend engineer",
		"Execution hints:\n- Driver: hecate_task\n- Provider: anthropic\n- Model: claude-sonnet-4\n- Profile: implementation",
		"Role defaults: provider=anthropic, model=claude-sonnet-4, profile=implementation",
		"Project defaults: provider=ollama, model=qwen2.5-coder, workspace_mode=in_place",
		"Request:\nExecute this assignment as a native agent_loop task.",
	} {
		if !strings.Contains(task.Prompt, want) {
			t.Fatalf("task prompt = %q, want launch context fragment %q", task.Prompt, want)
		}
	}
	if !strings.Contains(task.SystemPrompt, "Role instructions:\nFollow backend invariants.") || !strings.Contains(task.SystemPrompt, "Project system prompt:\nProject default system prompt.") {
		t.Fatalf("task system_prompt = %q, want role and project prompts", task.SystemPrompt)
	}
	if _, found, err := handler.taskStore.GetRun(t.Context(), task.ID, ref.RunID); err != nil || !found {
		t.Fatalf("GetRun(%q) found=%v err=%v, want run", ref.RunID, found, err)
	}
}

func TestProjectWorkAPI_StartAssignmentMirrorsResultToCairnline(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkCairnlineMirrorTestServer(t)
	workspace := t.TempDir()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace: workspace,
		Driver:    projectwork.AssignmentDriverHecateTask,
	})

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("start assignment status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var assignment ProjectWorkAssignmentEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &assignment); err != nil {
		t.Fatalf("decode assignment: %v", err)
	}
	ref := assignmentExecutionRefForTest(t, assignment.Data)
	if ref.RunID == "" || ref.ContextSnapshotID == "" {
		t.Fatalf("assignment execution_ref = %+v, want run and context snapshot links", ref)
	}

	stored := getStoredProjectWorkAssignmentForTest(t, handler, "proj_start", "work_start", "asgn_start")
	mirrored := getMirroredCairnlineAssignmentForTest(t, handler, "proj_start", "asgn_start")
	if mirrored.Status != cairnlinebridge.AssignmentStatus(stored.Status) || mirrored.ExecutionMode != cairnline.ExecutionOrchestrated {
		t.Fatalf("mirrored assignment status/mode = %q/%q, want %q/orchestrated", mirrored.Status, mirrored.ExecutionMode, cairnlinebridge.AssignmentStatus(stored.Status))
	}
	if mirrored.ExecutionRef != stored.ExecutionRef.RunID || mirrored.ContextSnapshotID != stored.ExecutionRef.ContextSnapshotID {
		t.Fatalf("mirrored assignment execution = ref %q context %q, want %q/%q", mirrored.ExecutionRef, mirrored.ContextSnapshotID, stored.ExecutionRef.RunID, stored.ExecutionRef.ContextSnapshotID)
	}
}

func TestProjectWorkAPI_PreflightAssignmentReturnsLaunchContextWithoutSideEffects(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	workspace := t.TempDir()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace: workspace,
		Driver:    projectwork.AssignmentDriverHecateTask,
	})

	packetResp := mustRequestJSON[ChatContextPacketResponse](newAPITestClient(t, server), http.MethodGet, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/preflight", "")
	if packetResp.Data.ExecutionMode != chat.ExecutionModeHecateTask || packetResp.Data.Provider != "anthropic" || packetResp.Data.Model != "claude-sonnet-4" {
		t.Fatalf("preflight mode/provider/model = %q/%q/%q, want native task launch hints", packetResp.Data.ExecutionMode, packetResp.Data.Provider, packetResp.Data.Model)
	}
	if packetResp.Data.ExecutionProfile != "coding_agent" || packetResp.Data.Workspace != workspace {
		t.Fatalf("preflight profile/workspace = %q/%q, want coding_agent/%q", packetResp.Data.ExecutionProfile, packetResp.Data.Workspace, workspace)
	}
	if packetResp.Data.Refs == nil || packetResp.Data.Refs.ProjectID != "proj_start" || packetResp.Data.Refs.WorkItemID != "work_start" || packetResp.Data.Refs.AssignmentID != "asgn_start" || packetResp.Data.Refs.RoleID != "role_backend" {
		t.Fatalf("preflight refs = %+v, want project/work/assignment/role refs", packetResp.Data.Refs)
	}
	if packetResp.Data.Refs.TaskID != "" || packetResp.Data.Refs.RunID != "" || packetResp.Data.Refs.SessionID != "" {
		t.Fatalf("preflight refs = %+v, want no task/run/session side effects", packetResp.Data.Refs)
	}
	item := findRenderedContextItemByOrigin(packetResp.Data, "project_assignment.preflight")
	if item == nil || item.Section != contextSectionRuntime || item.Included {
		t.Fatalf("preflight item = %+v, want inspect-only runtime item", item)
	}
	for _, want := range []string{"Preview only", "Task: created on start", "Run: created on start"} {
		if !strings.Contains(item.Body, want) {
			t.Fatalf("preflight body = %q, want %q", item.Body, want)
		}
	}
	tasks, err := handler.taskStore.ListTasks(t.Context(), taskstateFilterAll())
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks = %+v, want no task created by preflight", tasks)
	}
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/context", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("assignment context status = %d body=%s, want 404 after preflight-only request", rec.Code, rec.Body.String())
	}
}

func TestProjectWorkAPI_PreflightAssignmentShowsBlockedModelReadiness(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServerWithProviders(&fakeProvider{
		name:         "openai",
		defaultModel: "gpt-4o-mini",
	})
	workspace := t.TempDir()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace:           workspace,
		Driver:              projectwork.AssignmentDriverHecateTask,
		WithoutRoleDefaults: true,
	})
	if _, err := handler.projects.Update(t.Context(), "proj_start", func(project *projects.Project) {
		project.DefaultProvider = ""
		project.DefaultModel = "dogfood-model"
	}); err != nil {
		t.Fatalf("Update project defaults: %v", err)
	}

	packetResp := mustRequestJSON[ChatContextPacketResponse](newAPITestClient(t, server), http.MethodGet, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/preflight", "")
	readiness := findRenderedContextItemByKind(packetResp.Data, "launch_readiness")
	if readiness == nil || readiness.Section != contextSectionRuntime || readiness.Included {
		t.Fatalf("readiness item = %+v, want inspect-only runtime launch readiness", readiness)
	}
	if readiness.Metadata["ready"] != "false" || readiness.Metadata["status"] != "blocked" || readiness.Metadata["reason"] != "model_not_discovered" {
		t.Fatalf("readiness metadata = %+v, want blocked model_not_discovered", readiness.Metadata)
	}
	for _, want := range []string{
		"Ready: false",
		"Status: blocked",
		"Provider: auto",
		"Model: dogfood-model",
		"Reason: model_not_discovered",
		"No routable provider reports model \"dogfood-model\".",
		"Operator action:",
	} {
		if !strings.Contains(readiness.Body, want) {
			t.Fatalf("readiness body = %q, want %q", readiness.Body, want)
		}
	}
	if packetResp.Data.Refs == nil || packetResp.Data.Refs.TaskID != "" || packetResp.Data.Refs.RunID != "" {
		t.Fatalf("preflight refs = %+v, want no task/run side effects", packetResp.Data.Refs)
	}
	tasks, err := handler.taskStore.ListTasks(t.Context(), taskstateFilterAll())
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks = %+v, want no task created by blocked readiness preflight", tasks)
	}
}

func TestProjectWorkAPI_PreflightAndStartShareNativeLaunchPlan(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	workspace := t.TempDir()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace: workspace,
		Driver:    projectwork.AssignmentDriverHecateTask,
	})

	client := newAPITestClient(t, server)
	preflight := mustRequestJSON[ChatContextPacketResponse](client, http.MethodGet, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/preflight", "")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("start assignment status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var assignment ProjectWorkAssignmentEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &assignment); err != nil {
		t.Fatalf("decode assignment: %v", err)
	}
	ref := assignmentExecutionRefForTest(t, assignment.Data)
	task, found, err := handler.taskStore.GetTask(t.Context(), ref.TaskID)
	if err != nil || !found {
		t.Fatalf("GetTask(%q) found=%v err=%v, want task", ref.TaskID, found, err)
	}
	started := mustRequestJSON[ChatContextPacketResponse](client, http.MethodGet, "/hecate/v1/tasks/"+ref.TaskID+"/runs/"+ref.RunID+"/context", "")

	if preflight.Data.ExecutionMode != started.Data.ExecutionMode ||
		preflight.Data.Provider != started.Data.Provider ||
		preflight.Data.Model != started.Data.Model ||
		preflight.Data.ExecutionProfile != started.Data.ExecutionProfile ||
		preflight.Data.Workspace != started.Data.Workspace {
		t.Fatalf("preflight launch shape = mode/provider/model/profile/workspace %q/%q/%q/%q/%q, started = %q/%q/%q/%q/%q",
			preflight.Data.ExecutionMode, preflight.Data.Provider, preflight.Data.Model, preflight.Data.ExecutionProfile, preflight.Data.Workspace,
			started.Data.ExecutionMode, started.Data.Provider, started.Data.Model, started.Data.ExecutionProfile, started.Data.Workspace)
	}
	if task.RequestedProvider != preflight.Data.Provider || task.RequestedModel != preflight.Data.Model || task.ExecutionProfile != preflight.Data.ExecutionProfile || task.WorkingDirectory != preflight.Data.Workspace {
		t.Fatalf("task launch shape = provider/model/profile/workspace %q/%q/%q/%q, want preflight %q/%q/%q/%q",
			task.RequestedProvider, task.RequestedModel, task.ExecutionProfile, task.WorkingDirectory,
			preflight.Data.Provider, preflight.Data.Model, preflight.Data.ExecutionProfile, preflight.Data.Workspace)
	}
}

func TestProjectWorkAPI_PreflightAndStartIncludeCairnlineLaunchPacketEvidenceWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkCairnlineReadTestServer()
	workspace := t.TempDir()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace: workspace,
		Driver:    projectwork.AssignmentDriverHecateTask,
	})

	client := newAPITestClient(t, server)
	preflight := mustRequestJSON[ChatContextPacketResponse](client, http.MethodGet, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/preflight", "")
	if preflight.Data.ExecutionMode != chat.ExecutionModeHecateTask || preflight.Data.Provider != "anthropic" || preflight.Data.Model != "claude-sonnet-4" {
		t.Fatalf("Cairnline preflight launch shape = %q/%q/%q, want native task launch hints", preflight.Data.ExecutionMode, preflight.Data.Provider, preflight.Data.Model)
	}
	if preflight.Data.ExecutionProfile != "coding_agent" || preflight.Data.Workspace != workspace {
		t.Fatalf("Cairnline preflight profile/workspace = %q/%q, want coding_agent/%q", preflight.Data.ExecutionProfile, preflight.Data.Workspace, workspace)
	}
	if preflight.Data.Refs == nil || preflight.Data.Refs.ProjectID != "proj_start" || preflight.Data.Refs.WorkItemID != "work_start" || preflight.Data.Refs.AssignmentID != "asgn_start" || preflight.Data.Refs.RoleID != "role_backend" {
		t.Fatalf("Cairnline preflight refs = %+v, want project/work/assignment/role refs", preflight.Data.Refs)
	}
	assertCairnlineLaunchPacketEvidenceForTest(t, preflight.Data)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("start assignment status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var assignment ProjectWorkAssignmentEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &assignment); err != nil {
		t.Fatalf("decode assignment: %v", err)
	}
	ref := assignmentExecutionRefForTest(t, assignment.Data)
	started := mustRequestJSON[ChatContextPacketResponse](client, http.MethodGet, "/hecate/v1/tasks/"+ref.TaskID+"/runs/"+ref.RunID+"/context", "")
	assertCairnlineLaunchPacketEvidenceForTest(t, started.Data)
}

func TestProjectWorkAPI_PreflightAssignmentUsesCairnlineSidecarLaunchPacketWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineSidecarReadTestServer(t, "full+temp-root")
	if handler.projectReadRoutesUseCairnlineReadModel() {
		t.Fatal("sidecar preflight enabled embedded Cairnline read-model routes")
	}
	if !handler.projectCairnlineSidecarReadRoutesEnabled() {
		t.Fatal("sidecar read-route predicate = false, want true")
	}

	packetResp := mustRequestJSON[ChatContextPacketResponse](newAPITestClient(t, server), http.MethodGet, "/hecate/v1/projects/proj_fixture/work-items/work_fixture/assignments/asg_fixture/preflight", "")
	if packetResp.Data.ExecutionMode != chat.ExecutionModeHecateTask || packetResp.Data.Model != "fixture-model" {
		t.Fatalf("sidecar preflight mode/model = %q/%q, want Hecate task fixture-model", packetResp.Data.ExecutionMode, packetResp.Data.Model)
	}
	if packetResp.Data.ExecutionProfile != "profile_fixture" || packetResp.Data.Workspace == "" {
		t.Fatalf("sidecar preflight profile/workspace = %q/%q, want profile_fixture and resolved workspace", packetResp.Data.ExecutionProfile, packetResp.Data.Workspace)
	}
	if packetResp.Data.Refs == nil || packetResp.Data.Refs.ProjectID != "proj_fixture" || packetResp.Data.Refs.WorkItemID != "work_fixture" || packetResp.Data.Refs.AssignmentID != "asg_fixture" || packetResp.Data.Refs.RoleID != "role_fixture" {
		t.Fatalf("sidecar preflight refs = %+v, want project/work/assignment/role refs", packetResp.Data.Refs)
	}
	if packetResp.Data.Refs.TaskID != "" || packetResp.Data.Refs.RunID != "" || packetResp.Data.Refs.SessionID != "" {
		t.Fatalf("sidecar preflight refs = %+v, want no task/run/session side effects", packetResp.Data.Refs)
	}
	item := findRenderedContextItemByOrigin(packetResp.Data, "cairnline.assignment_launch_packet")
	if item == nil || item.Section != contextSectionRuntime || item.Included {
		t.Fatalf("sidecar launch packet item = %+v, want inspect-only runtime evidence", item)
	}
	for _, want := range []string{
		"Ready: true",
		"Project: proj_fixture",
		"Work item: work_fixture",
		"Assignment: asg_fixture",
		"Execution mode: mcp_pull",
		"Skills: 1; artifacts: 1; evidence: 1; reviews: 1; handoffs: 1; memory: 1; memory candidates: 1",
	} {
		if !strings.Contains(item.Body, want) {
			t.Fatalf("sidecar launch packet body = %q, want %q", item.Body, want)
		}
	}
	for key, want := range map[string]string{
		"read_backend":         "cairnline",
		"ready":                "true",
		"project_id":           "proj_fixture",
		"work_item_id":         "work_fixture",
		"assignment_id":        "asg_fixture",
		"role_id":              "role_fixture",
		"execution_mode":       "mcp_pull",
		"profile_id":           "profile_fixture",
		"execution_profile_id": "exec_fixture",
		"skill_count":          "1",
	} {
		if item.Metadata[key] != want {
			t.Fatalf("sidecar launch packet metadata[%q] = %q, want %q in %+v", key, item.Metadata[key], want, item.Metadata)
		}
	}
	tasks, err := handler.taskStore.ListTasks(t.Context(), taskstateFilterAll())
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks = %+v, want no task created by sidecar preflight", tasks)
	}
}

func TestProjectWorkAPI_PreflightAssignmentStrictEmbeddedReadModelReadsWithoutHecateProject(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend: "cairnline",
			CairnlineReadSource: "embedded",
		},
	}, quietLogger(), nil, nil, nil, nil)
	server := NewServer(quietLogger(), handler)
	const projectID = "proj_embedded_launch_preflight"
	workspace := t.TempDir()

	if err := handler.withCairnlineEmbeddedMirrorService(t.Context(), func(service *cairnline.Service) error {
		if _, err := service.CreateExecutionProfile(t.Context(), cairnline.ExecutionProfile{
			ID:           "exec_embedded_launch_preflight",
			Name:         "Embedded launch preflight",
			ProviderHint: "anthropic",
			ModelHint:    "claude-sonnet-4",
		}); err != nil {
			return err
		}
		if _, err := service.CreateProject(t.Context(), cairnline.Project{
			ID:                        projectID,
			Name:                      "Embedded Launch Preflight",
			Description:               "Coordinate launch preflight from embedded Cairnline.",
			DefaultRootID:             "root_embedded_launch_preflight",
			DefaultExecutionProfileID: "exec_embedded_launch_preflight",
			Roots: []cairnline.Root{{
				ID:     "root_embedded_launch_preflight",
				Path:   workspace,
				Kind:   "git",
				Active: true,
			}},
		}); err != nil {
			return err
		}
		if _, err := service.CreateRole(t.Context(), cairnline.Role{
			ID:                        "role_embedded_launch_preflight",
			ProjectID:                 projectID,
			Name:                      "Launch Reviewer",
			DefaultExecutionProfileID: "exec_embedded_launch_preflight",
			DefaultExecutionMode:      cairnline.ExecutionMCPPull,
		}); err != nil {
			return err
		}
		if _, err := service.CreateWorkItem(t.Context(), cairnline.WorkItem{
			ID:          "work_embedded_launch_preflight",
			ProjectID:   projectID,
			Title:       "Review embedded launch preflight",
			Brief:       "Exercise embedded Cairnline launch readiness and preflight projection.",
			Status:      cairnline.WorkStatusReady,
			Priority:    cairnline.PriorityNormal,
			OwnerRoleID: "role_embedded_launch_preflight",
			RootID:      "root_embedded_launch_preflight",
		}); err != nil {
			return err
		}
		_, err := service.CreateAssignment(t.Context(), cairnline.Assignment{
			ID:            "asgn_embedded_launch_preflight",
			ProjectID:     projectID,
			WorkItemID:    "work_embedded_launch_preflight",
			RoleID:        "role_embedded_launch_preflight",
			RootID:        "root_embedded_launch_preflight",
			ExecutionMode: cairnline.ExecutionMCPPull,
		})
		return err
	}); err != nil {
		t.Fatalf("seed embedded Cairnline launch preflight: %v", err)
	}
	if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("Hecate project store seeded ok=%v err=%v, want no project row", ok, err)
	}
	// Strict embedded launch-readiness and preflight are read-only Cairnline
	// projections; they should not require native Hecate project/work stores.
	handler.projects = nil
	handler.projectWork = nil

	client := newAPITestClient(t, server)
	readiness := mustRequestJSON[ProjectAssignmentLaunchReadinessEnvelope](client, http.MethodGet, "/hecate/v1/projects/"+projectID+"/work-items/work_embedded_launch_preflight/assignments/asgn_embedded_launch_preflight/launch-readiness", "")
	if readiness.Data.ReadBackend != "cairnline" || !readiness.Data.Ready || readiness.Data.Status != projectAssignmentLaunchReadinessStatusReady {
		t.Fatalf("embedded launch readiness = %+v, want ready Cairnline read", readiness.Data)
	}
	if readiness.Data.Workspace != workspace || readiness.Data.RootID != "root_embedded_launch_preflight" || readiness.Data.Provider != "anthropic" || readiness.Data.Model != "claude-sonnet-4" {
		t.Fatalf("embedded launch readiness target = workspace/provider/model/root %q/%q/%q/%q, want %q/anthropic/claude-sonnet-4/root", readiness.Data.Workspace, readiness.Data.Provider, readiness.Data.Model, readiness.Data.RootID, workspace)
	}

	packetResp := mustRequestJSON[ChatContextPacketResponse](client, http.MethodGet, "/hecate/v1/projects/"+projectID+"/work-items/work_embedded_launch_preflight/assignments/asgn_embedded_launch_preflight/preflight", "")
	if packetResp.Data.ExecutionMode != chat.ExecutionModeHecateTask || packetResp.Data.Provider != "anthropic" || packetResp.Data.Model != "claude-sonnet-4" || packetResp.Data.Workspace != workspace {
		t.Fatalf("embedded preflight launch shape = mode/provider/model/workspace %q/%q/%q/%q, want hecate_task/anthropic/claude-sonnet-4/%q", packetResp.Data.ExecutionMode, packetResp.Data.Provider, packetResp.Data.Model, packetResp.Data.Workspace, workspace)
	}
	if packetResp.Data.Refs == nil || packetResp.Data.Refs.ProjectID != projectID || packetResp.Data.Refs.WorkItemID != "work_embedded_launch_preflight" || packetResp.Data.Refs.AssignmentID != "asgn_embedded_launch_preflight" || packetResp.Data.Refs.RoleID != "role_embedded_launch_preflight" {
		t.Fatalf("embedded preflight refs = %+v, want embedded project/work/assignment/role refs", packetResp.Data.Refs)
	}
	if packetResp.Data.Refs.TaskID != "" || packetResp.Data.Refs.RunID != "" || packetResp.Data.Refs.SessionID != "" {
		t.Fatalf("embedded preflight refs = %+v, want no task/run/session side effects", packetResp.Data.Refs)
	}
	item := findRenderedContextItemByKind(packetResp.Data, "cairnline_launch_packet")
	if item == nil || item.Section != contextSectionRuntime || item.Included || item.Origin != "cairnline.assignment_launch_packet" {
		t.Fatalf("embedded launch packet evidence = %+v, want inspect-only Cairnline runtime evidence", item)
	}
	for _, want := range []string{
		"Ready: true",
		"Project: " + projectID,
		"Work item: work_embedded_launch_preflight",
		"Assignment: asgn_embedded_launch_preflight",
		"Execution mode: mcp_pull",
		"Root: root_embedded_launch_preflight",
	} {
		if !strings.Contains(item.Body, want) {
			t.Fatalf("embedded launch packet body = %q, want %q", item.Body, want)
		}
	}
	for key, want := range map[string]string{
		"read_backend":   "cairnline",
		"ready":          "true",
		"project_id":     projectID,
		"work_item_id":   "work_embedded_launch_preflight",
		"assignment_id":  "asgn_embedded_launch_preflight",
		"role_id":        "role_embedded_launch_preflight",
		"root_id":        "root_embedded_launch_preflight",
		"execution_mode": "mcp_pull",
	} {
		if item.Metadata[key] != want {
			t.Fatalf("embedded launch packet metadata[%q] = %q, want %q in %+v", key, item.Metadata[key], want, item.Metadata)
		}
	}
	tasks, err := handler.taskStore.ListTasks(t.Context(), taskstateFilterAll())
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks = %+v, want no task created by embedded preflight", tasks)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+projectID+"/work-items/work_other/assignments/asgn_embedded_launch_preflight/preflight", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("route-mismatched embedded preflight status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
}

func TestProjectWorkAPI_StrictEmbeddedLaunchInputsUseRuntimeOverlayWithoutNativeProjectStores(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend: "cairnline",
			CairnlineReadSource: "embedded",
		},
	}, quietLogger(), nil, nil, nil, nil)
	const projectID = "proj_embedded_launch_runtime"

	seedCairnlineOnlyProjectWorkGraphForTest(t, handler, cairnline.Project{
		ID:            projectID,
		Name:          "Embedded Launch Runtime",
		DefaultRootID: "root_embedded_launch_runtime",
		Roots: []cairnline.Root{{
			ID:     "root_embedded_launch_runtime",
			Path:   "/workspace/embedded-launch-runtime",
			Kind:   "git",
			Active: true,
		}},
	}, []cairnline.Role{{
		ID:        "role_embedded_launch_runtime",
		ProjectID: projectID,
		Name:      "Runtime Reviewer",
	}}, []cairnline.WorkItem{{
		ID:          "work_embedded_launch_runtime",
		ProjectID:   projectID,
		Title:       "Preserve launch runtime overlay",
		Status:      cairnline.WorkStatusReady,
		Priority:    cairnline.PriorityNormal,
		OwnerRoleID: "role_embedded_launch_runtime",
		RootID:      "root_embedded_launch_runtime",
	}}, []cairnline.Assignment{{
		ID:            "asgn_embedded_launch_runtime",
		ProjectID:     projectID,
		WorkItemID:    "work_embedded_launch_runtime",
		RoleID:        "role_embedded_launch_runtime",
		RootID:        "root_embedded_launch_runtime",
		ExecutionMode: cairnline.ExecutionOrchestrated,
	}})
	if _, err := handler.projectRuntime.Upsert(t.Context(), projectruntime.AssignmentRuntime{
		ProjectID:    projectID,
		AssignmentID: "asgn_embedded_launch_runtime",
		ExecutionRef: projectwork.AssignmentExecutionRef{
			Kind:              projectwork.AssignmentExecutionKindTaskRun,
			TaskID:            "task_runtime_overlay",
			RunID:             "run_runtime_overlay",
			ContextSnapshotID: "ctx_runtime_overlay",
			Status:            projectwork.AssignmentStatusRunning,
		},
		ContextPacket: []byte(`{"id":"ctx_runtime_overlay"}`),
	}); err != nil {
		t.Fatalf("seed assignment runtime overlay: %v", err)
	}
	requireCairnlineOnlyProjectReadsForTest(t, handler, projectID)

	inputs, err := handler.projectAssignmentStartInputs(t.Context(), projectID, "work_embedded_launch_runtime", "asgn_embedded_launch_runtime", true)
	if err != nil {
		t.Fatalf("projectAssignmentStartInputs() error = %v, want nil", err)
	}
	ref := inputs.Assignment.ExecutionRef
	if ref.TaskID != "task_runtime_overlay" || ref.RunID != "run_runtime_overlay" || ref.ContextSnapshotID != "ctx_runtime_overlay" || ref.Status != projectwork.AssignmentStatusRunning {
		t.Fatalf("assignment runtime ref = %+v, want runtime overlay without native project stores", ref)
	}
	if string(inputs.Assignment.ContextPacket) != `{"id":"ctx_runtime_overlay"}` {
		t.Fatalf("assignment context packet = %s, want runtime overlay packet", string(inputs.Assignment.ContextPacket))
	}
}

func TestProjectWorkAPI_StartAssignmentStrictEmbeddedReadModelLaunchesCairnlineOnlyProject(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend: "cairnline",
			CairnlineReadSource: "embedded",
		},
	}, quietLogger(), nil, nil, nil, nil)
	server := NewServer(quietLogger(), handler)
	const projectID = "proj_embedded_launch_start"
	workspace := t.TempDir()

	if err := handler.withCairnlineEmbeddedMirrorService(t.Context(), func(service *cairnline.Service) error {
		if _, err := service.CreateExecutionProfile(t.Context(), cairnline.ExecutionProfile{
			ID:           "exec_embedded_launch_start",
			Name:         "Embedded launch start",
			ProviderHint: "anthropic",
			ModelHint:    "claude-sonnet-4",
		}); err != nil {
			return err
		}
		if _, err := service.CreateProject(t.Context(), cairnline.Project{
			ID:                        projectID,
			Name:                      "Embedded Launch Start",
			Description:               "Coordinate assignment launch from embedded Cairnline.",
			DefaultRootID:             "root_embedded_launch_start",
			DefaultExecutionProfileID: "exec_embedded_launch_start",
			Roots: []cairnline.Root{{
				ID:     "root_embedded_launch_start",
				Path:   workspace,
				Kind:   "git",
				Active: true,
			}},
		}); err != nil {
			return err
		}
		if _, err := service.CreateRole(t.Context(), cairnline.Role{
			ID:                        "role_embedded_launch_start",
			ProjectID:                 projectID,
			Name:                      "Launch Implementer",
			Instructions:              "Use the embedded Cairnline graph.",
			DefaultExecutionProfileID: "exec_embedded_launch_start",
			DefaultExecutionMode:      cairnline.ExecutionOrchestrated,
		}); err != nil {
			return err
		}
		if _, err := service.CreateWorkItem(t.Context(), cairnline.WorkItem{
			ID:          "work_embedded_launch_start",
			ProjectID:   projectID,
			Title:       "Start embedded assignment",
			Brief:       "Launch a Hecate task from a Cairnline-only project graph.",
			Status:      cairnline.WorkStatusReady,
			Priority:    cairnline.PriorityNormal,
			OwnerRoleID: "role_embedded_launch_start",
			RootID:      "root_embedded_launch_start",
		}); err != nil {
			return err
		}
		_, err := service.CreateAssignment(t.Context(), cairnline.Assignment{
			ID:            "asgn_embedded_launch_start",
			ProjectID:     projectID,
			WorkItemID:    "work_embedded_launch_start",
			RoleID:        "role_embedded_launch_start",
			RootID:        "root_embedded_launch_start",
			ExecutionMode: cairnline.ExecutionOrchestrated,
		})
		return err
	}); err != nil {
		t.Fatalf("seed embedded Cairnline launch start: %v", err)
	}
	if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("Hecate project store seeded ok=%v err=%v, want no project row before launch", ok, err)
	}
	requireCairnlineOnlyProjectReadsForTest(t, handler, projectID)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_embedded_launch_start/assignments/asgn_embedded_launch_start/start", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("start embedded assignment status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var assignment ProjectWorkAssignmentEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &assignment); err != nil {
		t.Fatalf("decode assignment: %v", err)
	}
	ref := assignmentExecutionRefForTest(t, assignment.Data)
	if ref.TaskID == "" || ref.RunID == "" || ref.ContextSnapshotID == "" {
		t.Fatalf("assignment execution_ref = %+v, want task, run, and context links", ref)
	}
	if assignment.Data.ProjectID != projectID || assignment.Data.WorkItemID != "work_embedded_launch_start" || assignment.Data.RoleID != "role_embedded_launch_start" {
		t.Fatalf("assignment linkage = %+v, want embedded Cairnline project/work/role linkage", assignment.Data)
	}

	task, found, err := handler.taskStore.GetTask(t.Context(), ref.TaskID)
	if err != nil || !found {
		t.Fatalf("GetTask(%q) found=%v err=%v, want task", ref.TaskID, found, err)
	}
	if task.ProjectID != projectID || task.WorkItemID != "work_embedded_launch_start" || task.AssignmentID != "asgn_embedded_launch_start" {
		t.Fatalf("task linkage = project %q work %q assignment %q, want embedded Cairnline refs", task.ProjectID, task.WorkItemID, task.AssignmentID)
	}
	if task.RequestedProvider != "anthropic" || task.RequestedModel != "claude-sonnet-4" || task.WorkingDirectory != workspace {
		t.Fatalf("task provider/model/workspace = %q/%q/%q, want anthropic/claude-sonnet-4/%q", task.RequestedProvider, task.RequestedModel, task.WorkingDirectory, workspace)
	}
	if !strings.Contains(task.Prompt, "Start embedded assignment") || !strings.Contains(task.SystemPrompt, "Use the embedded Cairnline graph.") {
		t.Fatalf("task prompt/system = %q / %q, want Cairnline work and role context", task.Prompt, task.SystemPrompt)
	}

	runtime, ok, err := handler.projectRuntime.Get(t.Context(), projectID, "asgn_embedded_launch_start")
	if err != nil || !ok {
		t.Fatalf("Hecate assignment runtime ok=%v err=%v, want runtime overlay", ok, err)
	}
	if runtime.ExecutionRef.RunID != ref.RunID || runtime.ExecutionRef.ContextSnapshotID != ref.ContextSnapshotID {
		t.Fatalf("Hecate assignment runtime ref = %+v, want run/context %q/%q", runtime.ExecutionRef, ref.RunID, ref.ContextSnapshotID)
	}
	mirrored := getMirroredCairnlineAssignmentForTest(t, handler, projectID, "asgn_embedded_launch_start")
	if mirrored.Status != cairnlinebridge.AssignmentStatus(runtime.ExecutionRef.Status) || mirrored.ExecutionRef != ref.RunID || mirrored.ContextSnapshotID != ref.ContextSnapshotID {
		t.Fatalf("mirrored Cairnline assignment = status %q ref %q context %q, want %q/%q/%q", mirrored.Status, mirrored.ExecutionRef, mirrored.ContextSnapshotID, cairnlinebridge.AssignmentStatus(runtime.ExecutionRef.Status), ref.RunID, ref.ContextSnapshotID)
	}
}

func TestProjectWorkAPI_StartAssignmentStrictEmbeddedReadModelReleasesCairnlineClaimWhenTaskCreateFails(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend: "cairnline",
			CairnlineReadSource: "embedded",
		},
	}, quietLogger(), nil, nil, nil, nil)
	handler.taskStore = failingCreateTaskStore{Store: handler.taskStore}
	server := NewServer(quietLogger(), handler)
	const projectID = "proj_embedded_launch_task_create_fail"
	workspace := t.TempDir()

	if err := handler.withCairnlineEmbeddedMirrorService(t.Context(), func(service *cairnline.Service) error {
		if _, err := service.CreateExecutionProfile(t.Context(), cairnline.ExecutionProfile{
			ID:           "exec_embedded_launch_task_create_fail",
			Name:         "Embedded launch task create fail",
			ProviderHint: "anthropic",
			ModelHint:    "claude-sonnet-4",
		}); err != nil {
			return err
		}
		if _, err := service.CreateProject(t.Context(), cairnline.Project{
			ID:                        projectID,
			Name:                      "Embedded Launch Task Create Fail",
			Description:               "Coordinate failed assignment launch from embedded Cairnline.",
			DefaultRootID:             "root_embedded_launch_task_create_fail",
			DefaultExecutionProfileID: "exec_embedded_launch_task_create_fail",
			Roots: []cairnline.Root{{
				ID:     "root_embedded_launch_task_create_fail",
				Path:   workspace,
				Kind:   "git",
				Active: true,
			}},
		}); err != nil {
			return err
		}
		if _, err := service.CreateRole(t.Context(), cairnline.Role{
			ID:                        "role_embedded_launch_task_create_fail",
			ProjectID:                 projectID,
			Name:                      "Launch Implementer",
			Instructions:              "Use the embedded Cairnline graph.",
			DefaultExecutionProfileID: "exec_embedded_launch_task_create_fail",
			DefaultExecutionMode:      cairnline.ExecutionOrchestrated,
		}); err != nil {
			return err
		}
		if _, err := service.CreateWorkItem(t.Context(), cairnline.WorkItem{
			ID:          "work_embedded_launch_task_create_fail",
			ProjectID:   projectID,
			Title:       "Start embedded assignment with task create failure",
			Brief:       "Launch a Hecate task from a Cairnline-only project graph.",
			Status:      cairnline.WorkStatusReady,
			Priority:    cairnline.PriorityNormal,
			OwnerRoleID: "role_embedded_launch_task_create_fail",
			RootID:      "root_embedded_launch_task_create_fail",
		}); err != nil {
			return err
		}
		_, err := service.CreateAssignment(t.Context(), cairnline.Assignment{
			ID:            "asgn_embedded_launch_task_create_fail",
			ProjectID:     projectID,
			WorkItemID:    "work_embedded_launch_task_create_fail",
			RoleID:        "role_embedded_launch_task_create_fail",
			RootID:        "root_embedded_launch_task_create_fail",
			ExecutionMode: cairnline.ExecutionOrchestrated,
		})
		return err
	}); err != nil {
		t.Fatalf("seed embedded Cairnline launch task create failure: %v", err)
	}
	requireCairnlineOnlyProjectReadsForTest(t, handler, projectID)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_embedded_launch_task_create_fail/assignments/asgn_embedded_launch_task_create_fail/start", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("task create failure status = %d body=%s, want 500", rec.Code, rec.Body.String())
	}
	mirrored := getMirroredCairnlineAssignmentForTest(t, handler, projectID, "asgn_embedded_launch_task_create_fail")
	if mirrored.Status != cairnline.AssignmentQueued || mirrored.ClaimedBy != "" || mirrored.ExecutionRef != "" || mirrored.ContextSnapshotID != "" || !mirrored.StartedAt.IsZero() || !mirrored.CompletedAt.IsZero() {
		t.Fatalf("mirrored assignment = %+v, want released queued claim for retry", mirrored)
	}
	if _, ok, err := handler.projectRuntime.Get(t.Context(), projectID, "asgn_embedded_launch_task_create_fail"); err != nil || ok {
		t.Fatalf("Hecate assignment runtime ok=%v err=%v, want no runtime overlay on task create failure", ok, err)
	}
}

func TestProjectWorkAPI_StartExternalAgentAssignmentStrictEmbeddedReadModelLaunchesCairnlineOnlyProject(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend: "cairnline",
			CairnlineReadSource: "embedded",
		},
	}, quietLogger(), nil, nil, nil, nil)
	runner := &fakeAgentChatRunner{nativeSessionID: "native_embedded_external"}
	handler.SetAgentChatRunner(runner)
	// External Agent starts prepare chat sessions, so they should not require
	// the native Hecate task runtime to be configured.
	handler.taskStore = nil
	handler.taskRunner = nil
	server := NewServer(quietLogger(), handler)
	const projectID = "proj_embedded_external_start"
	workspace := t.TempDir()

	if _, err := handler.agentProfiles.Create(t.Context(), agentprofiles.Profile{
		ID:                "prof_embedded_external",
		Name:              "Embedded External Agent",
		Surface:           agentprofiles.SurfaceExternalAgent,
		ExecutionProfile:  "external_implementation",
		ExternalAgentKind: "codex",
	}); err != nil {
		t.Fatalf("Create external profile: %v", err)
	}
	if err := handler.withCairnlineEmbeddedMirrorService(t.Context(), func(service *cairnline.Service) error {
		if _, err := service.CreateProject(t.Context(), cairnline.Project{
			ID:            projectID,
			Name:          "Embedded External Launch",
			Description:   "Coordinate External Agent assignment launch from embedded Cairnline.",
			DefaultRootID: "root_embedded_external_start",
			Roots: []cairnline.Root{{
				ID:     "root_embedded_external_start",
				Path:   workspace,
				Kind:   "git",
				Active: true,
			}},
		}); err != nil {
			return err
		}
		if _, err := service.CreateRole(t.Context(), cairnline.Role{
			ID:                   "role_embedded_external_start",
			ProjectID:            projectID,
			Name:                 "External Implementer",
			Instructions:         "Use the embedded Cairnline graph before preparing the adapter session.",
			DefaultProfileID:     "prof_embedded_external",
			DefaultExecutionMode: cairnline.ExecutionExternalAdapter,
		}); err != nil {
			return err
		}
		if _, err := service.CreateWorkItem(t.Context(), cairnline.WorkItem{
			ID:          "work_embedded_external_start",
			ProjectID:   projectID,
			Title:       "Prepare embedded external assignment",
			Brief:       "Prepare an External Agent chat from a Cairnline-only project graph.",
			Status:      cairnline.WorkStatusReady,
			Priority:    cairnline.PriorityNormal,
			OwnerRoleID: "role_embedded_external_start",
			RootID:      "root_embedded_external_start",
		}); err != nil {
			return err
		}
		_, err := service.CreateAssignment(t.Context(), cairnline.Assignment{
			ID:            "asgn_embedded_external_start",
			ProjectID:     projectID,
			WorkItemID:    "work_embedded_external_start",
			RoleID:        "role_embedded_external_start",
			RootID:        "root_embedded_external_start",
			ExecutionMode: cairnline.ExecutionExternalAdapter,
		})
		return err
	}); err != nil {
		t.Fatalf("seed embedded Cairnline external launch: %v", err)
	}
	if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("Hecate project store seeded ok=%v err=%v, want no project row before launch", ok, err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_embedded_external_start/assignments/asgn_embedded_external_start/start", bytes.NewReader([]byte(`{"driver_kind":"external_agent"}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("start embedded external assignment status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var assignment ProjectWorkAssignmentEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &assignment); err != nil {
		t.Fatalf("decode assignment: %v", err)
	}
	ref := assignmentExecutionRefForTest(t, assignment.Data)
	if ref.ChatSessionID == "" || ref.ContextSnapshotID == "" || ref.TaskID != "" || ref.RunID != "" {
		t.Fatalf("assignment execution_ref = %+v, want external chat session and context only", ref)
	}
	if assignment.Data.ProjectID != projectID || assignment.Data.WorkItemID != "work_embedded_external_start" || assignment.Data.RoleID != "role_embedded_external_start" || assignment.Data.Status != projectwork.AssignmentStatusRunning {
		t.Fatalf("assignment = %+v, want running embedded Cairnline external-agent assignment", assignment.Data)
	}
	resolvedWorkspace, err := agentadapters.ValidateWorkspace(workspace)
	if err != nil {
		t.Fatalf("ValidateWorkspace: %v", err)
	}
	if len(runner.prepareRequests) != 1 || runner.prepareRequests[0].AdapterID != "codex" || runner.prepareRequests[0].SessionID != ref.ChatSessionID || runner.prepareRequests[0].Workspace != resolvedWorkspace {
		t.Fatalf("prepare requests = %+v, want one codex preparation in workspace %q", runner.prepareRequests, resolvedWorkspace)
	}
	if len(runner.runRequests) != 0 {
		t.Fatalf("run requests = %+v, want no automatic external-agent run", runner.runRequests)
	}
	session, ok, err := handler.agentChat.Get(t.Context(), ref.ChatSessionID)
	if err != nil || !ok {
		t.Fatalf("Get chat session found=%v err=%v, want linked session", ok, err)
	}
	if session.ProjectID != projectID || session.AgentID != "codex" || session.NativeSessionID != "native_embedded_external" {
		t.Fatalf("session = %+v, want prepared external agent session for Cairnline project", session)
	}
	if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("Hecate project store ok=%v err=%v after launch, want no native project row", ok, err)
	}
	shadow := getStoredProjectWorkAssignmentForTest(t, handler, projectID, "work_embedded_external_start", "asgn_embedded_external_start")
	if shadow.ExecutionRef.ChatSessionID != ref.ChatSessionID || shadow.ExecutionRef.ContextSnapshotID != ref.ContextSnapshotID {
		t.Fatalf("Hecate assignment shadow ref = %+v, want chat/context %q/%q", shadow.ExecutionRef, ref.ChatSessionID, ref.ContextSnapshotID)
	}
	mirrored := getMirroredCairnlineAssignmentForTest(t, handler, projectID, "asgn_embedded_external_start")
	if mirrored.Status != cairnlinebridge.AssignmentStatus(shadow.Status) || mirrored.ExecutionRef != ref.ChatSessionID || mirrored.ContextSnapshotID != ref.ContextSnapshotID {
		t.Fatalf("mirrored Cairnline assignment = status %q ref %q context %q, want %q/%q/%q", mirrored.Status, mirrored.ExecutionRef, mirrored.ContextSnapshotID, cairnlinebridge.AssignmentStatus(shadow.Status), ref.ChatSessionID, ref.ContextSnapshotID)
	}
}

func TestProjectWorkAPI_PreflightAssignmentCairnlineSidecarRequiresStructuredLaunchPacket(t *testing.T) {
	t.Parallel()
	_, server := newProjectsCairnlineSidecarReadTestServer(t, "assignments.launch_packet-text-only")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/work-items/work_fixture/assignments/asg_fixture/preflight", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("preflight status = %d body=%s, want 502", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "structuredContent") {
		t.Fatalf("error body = %s, want structuredContent diagnostic", rec.Body.String())
	}
}

func TestProjectWorkAPI_PreflightAssignmentCairnlineSidecarRejectsRouteMismatch(t *testing.T) {
	t.Parallel()
	_, server := newProjectsCairnlineSidecarReadTestServer(t, "full+temp-root+launch-packet-route-mismatch")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/work-items/work_fixture/assignments/asg_fixture/preflight", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("preflight status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "assignment not found") {
		t.Fatalf("error body = %s, want scoped assignment not found", rec.Body.String())
	}
}

func TestProjectWorkAPI_PreflightAssignmentCairnlineSidecarRejectsProjectMismatch(t *testing.T) {
	t.Parallel()
	_, server := newProjectsCairnlineSidecarReadTestServer(t, "full+temp-root+project-route-mismatch")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/work-items/work_fixture/assignments/asg_fixture/preflight", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("preflight status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "project not found") {
		t.Fatalf("error body = %s, want scoped project not found", rec.Body.String())
	}
}

func TestProjectWorkAPI_AssignmentContextUsesCairnlineSidecarWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineSidecarReadTestServer(t, "full")
	if handler.projectReadRoutesUseCairnlineReadModel() {
		t.Fatal("sidecar assignment context enabled embedded Cairnline read-model routes")
	}
	if !handler.projectCairnlineSidecarReadRoutesEnabled() {
		t.Fatal("sidecar read-route predicate = false, want true")
	}

	packetResp := mustRequestJSON[ChatContextPacketResponse](newAPITestClient(t, server), http.MethodGet, "/hecate/v1/projects/proj_fixture/work-items/work_fixture/assignments/asg_fixture/context", "")
	if packetResp.Data.ID != "ctx_fixture" || packetResp.Data.ExecutionMode != "mcp_pull" {
		t.Fatalf("sidecar assignment context id/mode = %q/%q, want ctx_fixture/mcp_pull", packetResp.Data.ID, packetResp.Data.ExecutionMode)
	}
	if packetResp.Data.Refs == nil || packetResp.Data.Refs.ProjectID != "proj_fixture" || packetResp.Data.Refs.WorkItemID != "work_fixture" || packetResp.Data.Refs.AssignmentID != "asg_fixture" || packetResp.Data.Refs.RoleID != "role_fixture" {
		t.Fatalf("sidecar assignment context refs = %+v, want project/work/assignment/role refs", packetResp.Data.Refs)
	}
	if packetResp.Data.Refs.TaskID != "" || packetResp.Data.Refs.RunID != "" || packetResp.Data.Refs.SessionID != "" {
		t.Fatalf("sidecar assignment context refs = %+v, want no task/run/session side effects", packetResp.Data.Refs)
	}
	item := findRenderedContextItemByOrigin(packetResp.Data, "cairnline.assignments.context")
	if item == nil || item.Section != contextSectionRuntime || item.Included || item.Metadata["source_tool"] != "assignments.context" {
		t.Fatalf("sidecar assignment context runtime item = %+v, want inspect-only assignments.context metadata", item)
	}
	for _, want := range []string{
		"Read backend: cairnline",
		"Source tool: assignments.context",
		"Portable execution mode: mcp_pull",
	} {
		if !strings.Contains(item.Body, want) {
			t.Fatalf("sidecar assignment context body = %q, want %q", item.Body, want)
		}
	}
	tasks, err := handler.taskStore.ListTasks(t.Context(), taskstateFilterAll())
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks = %+v, want no task created by sidecar assignment context", tasks)
	}
}

func TestProjectWorkAPI_AssignmentContextStrictEmbeddedReadModelReadsWithoutHecateProject(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend: "cairnline",
			CairnlineReadSource: "embedded",
		},
	}, quietLogger(), nil, nil, nil, nil)
	server := NewServer(quietLogger(), handler)
	const projectID = "proj_embedded_assignment_context"

	if err := handler.withCairnlineEmbeddedMirrorService(t.Context(), func(service *cairnline.Service) error {
		if _, err := service.CreateProject(t.Context(), cairnline.Project{
			ID:            projectID,
			Name:          "Embedded Assignment Context",
			Description:   "Coordinate assignment context from embedded Cairnline.",
			DefaultRootID: "root_embedded_assignment_context",
			Roots: []cairnline.Root{{
				ID:     "root_embedded_assignment_context",
				Path:   "/workspace/embedded-assignment-context",
				Kind:   "git",
				Active: true,
			}},
		}); err != nil {
			return err
		}
		if _, err := service.CreateRole(t.Context(), cairnline.Role{
			ID:        "role_embedded_assignment_context",
			ProjectID: projectID,
			Name:      "Context Reviewer",
		}); err != nil {
			return err
		}
		if _, err := service.CreateWorkItem(t.Context(), cairnline.WorkItem{
			ID:          "work_embedded_assignment_context",
			ProjectID:   projectID,
			Title:       "Review embedded assignment context",
			Brief:       "Exercise embedded Cairnline assignment context projection.",
			Status:      cairnline.WorkStatusReady,
			Priority:    cairnline.PriorityNormal,
			OwnerRoleID: "role_embedded_assignment_context",
			RootID:      "root_embedded_assignment_context",
		}); err != nil {
			return err
		}
		_, err := service.CreateAssignment(t.Context(), cairnline.Assignment{
			ID:            "asgn_embedded_assignment_context",
			ProjectID:     projectID,
			WorkItemID:    "work_embedded_assignment_context",
			RoleID:        "role_embedded_assignment_context",
			RootID:        "root_embedded_assignment_context",
			ExecutionMode: cairnline.ExecutionMCPPull,
		})
		return err
	}); err != nil {
		t.Fatalf("seed embedded Cairnline assignment context: %v", err)
	}
	if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("Hecate project store seeded ok=%v err=%v, want no project row", ok, err)
	}
	requireCairnlineOnlyProjectReadsForTest(t, handler, projectID)

	packetResp := mustRequestJSON[ChatContextPacketResponse](newAPITestClient(t, server), http.MethodGet, "/hecate/v1/projects/"+projectID+"/work-items/work_embedded_assignment_context/assignments/asgn_embedded_assignment_context/context", "")
	if packetResp.Data.ExecutionMode != cairnline.ExecutionMCPPull {
		t.Fatalf("assignment context execution mode = %q, want mcp_pull", packetResp.Data.ExecutionMode)
	}
	if packetResp.Data.Refs == nil || packetResp.Data.Refs.ProjectID != projectID || packetResp.Data.Refs.WorkItemID != "work_embedded_assignment_context" || packetResp.Data.Refs.AssignmentID != "asgn_embedded_assignment_context" || packetResp.Data.Refs.RoleID != "role_embedded_assignment_context" {
		t.Fatalf("assignment context refs = %+v, want embedded project/work/assignment/role refs", packetResp.Data.Refs)
	}
	if packetResp.Data.Refs.TaskID != "" || packetResp.Data.Refs.RunID != "" || packetResp.Data.Refs.SessionID != "" {
		t.Fatalf("assignment context refs = %+v, want no task/run/session side effects", packetResp.Data.Refs)
	}
	item := findRenderedContextItemByOrigin(packetResp.Data, "cairnline.assignments.context")
	if item == nil || item.Section != contextSectionRuntime || item.Included || item.Metadata["read_backend"] != "cairnline" || item.Metadata["source_tool"] != "assignments.context" {
		t.Fatalf("embedded assignment context runtime item = %+v, want inspect-only assignments.context metadata", item)
	}
	workItem := findRenderedContextItemByOrigin(packetResp.Data, "work_embedded_assignment_context")
	if workItem == nil || workItem.Section != contextSectionProjectWork || !workItem.Included {
		t.Fatalf("embedded assignment work item = %+v, want included project-work metadata", workItem)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+projectID+"/work-items/work_other/assignments/asgn_embedded_assignment_context/context", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("route-mismatched assignment context status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_missing/work-items/work_embedded_assignment_context/assignments/asgn_embedded_assignment_context/context", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing project assignment context status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
}

func TestProjectWorkAPI_AssignmentContextCairnlineSidecarRequiresStructuredContent(t *testing.T) {
	t.Parallel()
	_, server := newProjectsCairnlineSidecarReadTestServer(t, "assignments.context-text-only")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/work-items/work_fixture/assignments/asg_fixture/context", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("assignment context status = %d body=%s, want 502", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "structuredContent") {
		t.Fatalf("error body = %s, want structuredContent diagnostic", rec.Body.String())
	}
}

func TestProjectWorkAPI_AssignmentContextCairnlineSidecarRejectsRouteMismatch(t *testing.T) {
	t.Parallel()
	_, server := newProjectsCairnlineSidecarReadTestServer(t, "full+context-route-mismatch")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/work-items/work_fixture/assignments/asg_fixture/context", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("assignment context status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "project assignment context packet not found") {
		t.Fatalf("error body = %s, want scoped context not found", rec.Body.String())
	}
}

func TestProjectWorkAPI_AssignmentContextCairnlineSidecarRejectsProjectMismatch(t *testing.T) {
	t.Parallel()
	_, server := newProjectsCairnlineSidecarReadTestServer(t, "full+project-route-mismatch")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/work-items/work_fixture/assignments/asg_fixture/context", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("assignment context status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "project assignment context packet not found") {
		t.Fatalf("error body = %s, want scoped context not found", rec.Body.String())
	}
}

func TestProjectWorkAPI_AssignmentContextCairnlineSidecarRejectsContextProjectMismatch(t *testing.T) {
	t.Parallel()
	_, server := newProjectsCairnlineSidecarReadTestServer(t, "full+context-project-mismatch")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/work-items/work_fixture/assignments/asg_fixture/context", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("assignment context status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "project assignment context packet not found") {
		t.Fatalf("error body = %s, want scoped context not found", rec.Body.String())
	}
}

func TestProjectWorkAPI_PreflightAndStartSnapshotModelReadiness(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServerWithProviders(&fakeProvider{
		name:     "anthropic",
		response: &types.ChatResponse{},
		capabilities: providers.Capabilities{
			Name:         "anthropic",
			Kind:         providers.KindCloud,
			DefaultModel: "claude-sonnet-4",
			Models:       []string{"claude-sonnet-4"},
		},
	})
	workspace := t.TempDir()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace: workspace,
		Driver:    projectwork.AssignmentDriverHecateTask,
	})

	client := newAPITestClient(t, server)
	preflight := mustRequestJSON[ChatContextPacketResponse](client, http.MethodGet, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/preflight", "")
	preflightReadiness := findRenderedContextItemByKind(preflight.Data, "launch_readiness")
	if preflightReadiness == nil || !strings.Contains(preflightReadiness.Body, "Ready: true") || !strings.Contains(preflightReadiness.Body, "Matched provider: anthropic") {
		t.Fatalf("preflight readiness = %+v, want ready anthropic metadata", preflightReadiness)
	}
	if preflightReadiness.Metadata["ready"] != "true" || preflightReadiness.Metadata["matched_provider"] != "anthropic" {
		t.Fatalf("preflight readiness metadata = %+v, want ready anthropic", preflightReadiness.Metadata)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("start assignment status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var assignment ProjectWorkAssignmentEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &assignment); err != nil {
		t.Fatalf("decode assignment: %v", err)
	}
	ref := assignmentExecutionRefForTest(t, assignment.Data)
	started := mustRequestJSON[ChatContextPacketResponse](client, http.MethodGet, "/hecate/v1/tasks/"+ref.TaskID+"/runs/"+ref.RunID+"/context", "")
	startedReadiness := findRenderedContextItemByKind(started.Data, "launch_readiness")
	if startedReadiness == nil {
		t.Fatal("started context missing launch_readiness item")
	}
	if startedReadiness.Body != preflightReadiness.Body {
		t.Fatalf("started readiness body = %q, want preflight body %q", startedReadiness.Body, preflightReadiness.Body)
	}
}

func TestProjectWorkAPI_StartAssignmentUsesSelectedProjectRoot(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	defaultRoot := t.TempDir()
	workRoot := t.TempDir()
	assignmentRoot := t.TempDir()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace: defaultRoot,
		Driver:    projectwork.AssignmentDriverHecateTask,
	})
	if _, err := handler.projects.Update(t.Context(), "proj_start", func(project *projects.Project) {
		project.DefaultRootID = "root_default"
		project.Roots = []projects.Root{
			{ID: "root_default", Path: defaultRoot, Kind: "git", Active: true},
			{ID: "root_work", Path: workRoot, Kind: "git_worktree", GitBranch: "work-root", Active: true},
			{ID: "root_assignment", Path: assignmentRoot, Kind: "git_worktree", GitBranch: "assignment-root", Active: true},
		}
	}); err != nil {
		t.Fatalf("Update project roots: %v", err)
	}
	if _, err := handler.projectWork.UpdateWorkItem(t.Context(), "proj_start", "work_start", func(item *projectwork.WorkItem) {
		item.RootID = "root_work"
	}); err != nil {
		t.Fatalf("Update work root: %v", err)
	}
	if _, err := handler.projectWork.UpdateAssignment(t.Context(), "proj_start", "asgn_start", func(item *projectwork.Assignment) {
		item.RootID = "root_assignment"
	}); err != nil {
		t.Fatalf("Update assignment root: %v", err)
	}

	preflightResp := mustRequestJSON[ChatContextPacketResponse](newAPITestClient(t, server), http.MethodGet, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/preflight", "")
	preflightRoot := findRenderedContextItemByKind(preflightResp.Data, "project_root")
	if preflightRoot == nil || !strings.Contains(preflightRoot.Body, "Root ID: root_assignment") || !strings.Contains(preflightRoot.Body, assignmentRoot) || !strings.Contains(preflightRoot.Body, "Selection: assignment override") {
		t.Fatalf("preflight project_root item = %+v, want assignment root metadata", preflightRoot)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("start assignment status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var assignment ProjectWorkAssignmentEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &assignment); err != nil {
		t.Fatalf("decode assignment: %v", err)
	}
	ref := assignmentExecutionRefForTest(t, assignment.Data)
	task, found, err := handler.taskStore.GetTask(t.Context(), ref.TaskID)
	if err != nil || !found {
		t.Fatalf("GetTask(%q) found=%v err=%v, want task", ref.TaskID, found, err)
	}
	if task.WorkingDirectory != assignmentRoot || task.SandboxAllowedRoot != assignmentRoot {
		t.Fatalf("task workspace = (%q, %q), want assignment root %q", task.WorkingDirectory, task.SandboxAllowedRoot, assignmentRoot)
	}
	packetResp := mustRequestJSON[ChatContextPacketResponse](newAPITestClient(t, server), http.MethodGet, "/hecate/v1/tasks/"+ref.TaskID+"/runs/"+ref.RunID+"/context", "")
	startRoot := findRenderedContextItemByKind(packetResp.Data, "project_root")
	if startRoot == nil || !strings.Contains(startRoot.Body, "Git branch: assignment-root") {
		t.Fatalf("stored project_root item = %+v, want assignment root branch", startRoot)
	}
}

func TestProjectWorkAPI_StartAssignmentPersistsInspectableContextPacket(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	handler.SetMemoryStore(memory.NewMemoryStore())
	workspace := t.TempDir()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace:        workspace,
		Driver:           projectwork.AssignmentDriverHecateTask,
		RoleAgentProfile: "prof_packet_visible",
	})
	if _, err := handler.agentProfiles.Create(t.Context(), agentprofiles.Profile{
		ID:                  "prof_packet_visible",
		Name:                "Packet visible-only",
		Surface:             agentprofiles.SurfaceHecateTask,
		ExecutionProfile:    "repo_local",
		ProjectMemoryPolicy: agentprofiles.MemoryVisibleOnly,
		ContextSourcePolicy: agentprofiles.ContextVisibleOnly,
	}); err != nil {
		t.Fatalf("Create profile: %v", err)
	}
	if _, err := handler.projects.Update(t.Context(), "proj_start", func(project *projects.Project) {
		project.ContextSources = []projects.ContextSource{{
			ID:      "ctx_readme",
			Kind:    "doc",
			Title:   "README",
			Path:    "README.md",
			Enabled: true,
		}}
	}); err != nil {
		t.Fatalf("Update project context sources: %v", err)
	}
	if _, err := handler.memory.Create(t.Context(), memory.Entry{
		ID:         "mem_backend",
		ProjectID:  "proj_start",
		Title:      "Backend preference",
		Body:       "Prefer Go-first changes.",
		TrustLabel: memory.TrustLabelOperatorMemory,
		SourceKind: memory.SourceKindOperator,
		Enabled:    true,
	}); err != nil {
		t.Fatalf("Create project memory: %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(t.Context(), projectwork.Assignment{
		ID:         "asgn_pm",
		ProjectID:  "proj_start",
		WorkItemID: "work_start",
		RoleID:     "product_manager",
		DriverKind: projectwork.AssignmentDriverHecateTask,
		Status:     projectwork.AssignmentStatusCompleted,
	}); err != nil {
		t.Fatalf("Create source assignment: %v", err)
	}
	if _, err := handler.projectWork.CreateArtifact(t.Context(), projectwork.CollaborationArtifact{
		ID:         "art_brief",
		ProjectID:  "proj_start",
		WorkItemID: "work_start",
		Kind:       projectwork.ArtifactKindBrief,
		Title:      "Operator brief",
		Body:       "Do the backend slice only.",
	}); err != nil {
		t.Fatalf("Create artifact: %v", err)
	}
	if _, err := handler.projectWork.CreateHandoff(t.Context(), projectwork.Handoff{
		ID:                    "handoff_review",
		ProjectID:             "proj_start",
		WorkItemID:            "work_start",
		SourceAssignmentID:    "asgn_pm",
		TargetRoleID:          "role_backend",
		Title:                 "Backend handoff",
		Summary:               "Focus on the runtime contract.",
		RecommendedNextAction: "Start the native assignment and verify the packet.",
		TrustLabel:            "operator_reviewed",
		CreatedByRoleID:       "product_manager",
	}); err != nil {
		t.Fatalf("Create handoff: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("start assignment status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var assignment ProjectWorkAssignmentEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &assignment); err != nil {
		t.Fatalf("decode assignment: %v", err)
	}
	ref := assignmentExecutionRefForTest(t, assignment.Data)
	if ref.ContextSnapshotID == "" {
		t.Fatalf("execution_ref = %+v, want persisted packet id", ref)
	}

	packetResp := mustRequestJSON[ChatContextPacketResponse](newAPITestClient(t, server), http.MethodGet, "/hecate/v1/tasks/"+ref.TaskID+"/runs/"+ref.RunID+"/context", "")
	if packetResp.Data.ID != ref.ContextSnapshotID {
		t.Fatalf("task run context id = %q, want %q", packetResp.Data.ID, ref.ContextSnapshotID)
	}
	if packetResp.Data.ExecutionProfile != "repo_local" {
		t.Fatalf("execution_profile = %q, want repo_local", packetResp.Data.ExecutionProfile)
	}
	if packetResp.Data.Refs == nil || packetResp.Data.Refs.ProjectID != "proj_start" || packetResp.Data.Refs.WorkItemID != "work_start" || packetResp.Data.Refs.AssignmentID != "asgn_start" || packetResp.Data.Refs.RoleID != "role_backend" {
		t.Fatalf("packet refs = %+v, want project/work/assignment/role refs", packetResp.Data.Refs)
	}
	if item := findRenderedContextItemByOrigin(packetResp.Data, "mem_backend"); item == nil || item.Included || item.Section != contextSectionMemory {
		t.Fatalf("memory item = %+v, want excluded memory section item", item)
	}
	if item := findRenderedContextItemByOrigin(packetResp.Data, "README.md"); item == nil || item.Included || item.Section != contextSectionSources {
		t.Fatalf("context source item = %+v, want excluded sources section item", item)
	}
	if item := findRenderedContextItemByOrigin(packetResp.Data, "handoff_review"); item == nil || item.Included || item.Section != contextSectionProjectWork {
		t.Fatalf("handoff item = %+v, want excluded project_work section item", item)
	}
	if item := findRenderedContextItemByOrigin(packetResp.Data, "art_brief"); item == nil || item.Included || item.Section != contextSectionProjectWork {
		t.Fatalf("artifact item = %+v, want excluded project_work section item", item)
	}
	if item := findRenderedContextItemByOrigin(packetResp.Data, "project_assignment.execution_hints"); item == nil || !item.Included || item.Section != contextSectionProjectWork {
		t.Fatalf("execution hints item = %+v, want included project_work item", item)
	}

	assignmentPacket := mustRequestJSON[ChatContextPacketResponse](newAPITestClient(t, server), http.MethodGet, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/context", "")
	if assignmentPacket.Data.ID != ref.ContextSnapshotID {
		t.Fatalf("assignment context id = %q, want %q", assignmentPacket.Data.ID, ref.ContextSnapshotID)
	}
}

func TestProjectWorkAPI_StartAssignmentAppliesProfileContextPolicies(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name             string
		memoryPolicy     string
		sourcePolicy     string
		wantMemoryItem   bool
		wantMemoryActive bool
		wantSourceItem   bool
		wantSourceActive bool
		wantMemoryReason string
		wantSourceReason string
	}{
		{
			name:             "include",
			memoryPolicy:     agentprofiles.MemoryInclude,
			sourcePolicy:     agentprofiles.ContextIncludeEnabled,
			wantMemoryItem:   true,
			wantMemoryActive: true,
			wantSourceItem:   true,
			wantSourceActive: true,
			wantMemoryReason: "project_memory_policy=include",
			wantSourceReason: "context_source_policy=include_enabled",
		},
		{
			name:             "visible only",
			memoryPolicy:     agentprofiles.MemoryVisibleOnly,
			sourcePolicy:     agentprofiles.ContextVisibleOnly,
			wantMemoryItem:   true,
			wantMemoryActive: false,
			wantSourceItem:   true,
			wantSourceActive: false,
			wantMemoryReason: "project_memory_policy=visible_only",
			wantSourceReason: "context_source_policy=visible_only",
		},
		{
			name:             "inherit keeps visible only default",
			memoryPolicy:     agentprofiles.MemoryInherit,
			sourcePolicy:     agentprofiles.ContextInherit,
			wantMemoryItem:   true,
			wantSourceItem:   true,
			wantMemoryReason: "project_memory_policy=inherit",
			wantSourceReason: "context_source_policy=inherit",
		},
		{
			name:         "exclude",
			memoryPolicy: agentprofiles.MemoryExclude,
			sourcePolicy: agentprofiles.ContextExclude,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			handler, server := newProjectWorkTestServer()
			handler.SetMemoryStore(memory.NewMemoryStore())
			workspace := t.TempDir()
			profileID := "prof_policy_" + strings.ReplaceAll(tc.name, " ", "_")
			seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
				Workspace:        workspace,
				Driver:           projectwork.AssignmentDriverHecateTask,
				RoleAgentProfile: profileID,
			})
			if _, err := handler.agentProfiles.Create(t.Context(), agentprofiles.Profile{
				ID:                  profileID,
				Name:                "Implementation",
				Surface:             agentprofiles.SurfaceHecateTask,
				ExecutionProfile:    "repo_local",
				ToolsEnabled:        true,
				WritesAllowed:       true,
				ProjectMemoryPolicy: tc.memoryPolicy,
				ContextSourcePolicy: tc.sourcePolicy,
			}); err != nil {
				t.Fatalf("Create profile: %v", err)
			}
			if _, err := handler.projects.Update(t.Context(), "proj_start", func(project *projects.Project) {
				project.ContextSources = []projects.ContextSource{{
					ID:         "ctx_agents",
					Kind:       "workspace_instruction",
					Title:      "AGENTS.md",
					Path:       "AGENTS.md",
					Enabled:    true,
					TrustLabel: "workspace_guidance",
				}}
			}); err != nil {
				t.Fatalf("Update project context sources: %v", err)
			}
			if _, err := handler.memory.Create(t.Context(), memory.Entry{
				ID:         "mem_runtime",
				ProjectID:  "proj_start",
				Title:      "Runtime preference",
				Body:       "Keep context policy changes explicit.",
				TrustLabel: memory.TrustLabelOperatorMemory,
				SourceKind: memory.SourceKindOperator,
				Enabled:    true,
			}); err != nil {
				t.Fatalf("Create project memory: %v", err)
			}

			rec := httptest.NewRecorder()
			server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{}`))))
			if rec.Code != http.StatusOK {
				t.Fatalf("start assignment status = %d body=%s, want 200", rec.Code, rec.Body.String())
			}
			var assignment ProjectWorkAssignmentEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &assignment); err != nil {
				t.Fatalf("decode assignment: %v", err)
			}
			ref := assignmentExecutionRefForTest(t, assignment.Data)
			packetResp := mustRequestJSON[ChatContextPacketResponse](newAPITestClient(t, server), http.MethodGet, "/hecate/v1/tasks/"+ref.TaskID+"/runs/"+ref.RunID+"/context", "")

			memoryItem := findRenderedContextItemByOrigin(packetResp.Data, "mem_runtime")
			if tc.wantMemoryItem {
				if memoryItem == nil || memoryItem.Included != tc.wantMemoryActive || memoryItem.Section != contextSectionMemory || !strings.Contains(memoryItem.InclusionReason, tc.wantMemoryReason) {
					t.Fatalf("memory item = %+v, want present included=%v reason containing %q", memoryItem, tc.wantMemoryActive, tc.wantMemoryReason)
				}
			} else if memoryItem != nil {
				t.Fatalf("memory item = %+v, want omitted by profile policy", memoryItem)
			}

			sourceItem := findRenderedContextItemByOrigin(packetResp.Data, "AGENTS.md")
			if tc.wantSourceItem {
				if sourceItem == nil || sourceItem.Included != tc.wantSourceActive || sourceItem.Section != contextSectionSources || !strings.Contains(sourceItem.InclusionReason, tc.wantSourceReason) {
					t.Fatalf("source item = %+v, want present included=%v reason containing %q", sourceItem, tc.wantSourceActive, tc.wantSourceReason)
				}
			} else if sourceItem != nil {
				t.Fatalf("source item = %+v, want omitted by profile policy", sourceItem)
			}
		})
	}
}

func TestProjectWorkAPI_StartAssignmentIncludesExplicitPromptContext(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	handler.SetMemoryStore(memory.NewMemoryStore())
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("Use the portable workspace guidance."), 0o644); err != nil {
		t.Fatalf("Write AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "CLAUDE.md"), []byte("Host-specific Claude guidance should stay out of Hecate prompt context."), 0o644); err != nil {
		t.Fatalf("Write CLAUDE.md: %v", err)
	}
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace:        workspace,
		Driver:           projectwork.AssignmentDriverHecateTask,
		RoleAgentProfile: "prof_prompt_context",
	})
	if _, err := handler.agentProfiles.Create(t.Context(), agentprofiles.Profile{
		ID:                  "prof_prompt_context",
		Name:                "Implementation",
		Surface:             agentprofiles.SurfaceHecateTask,
		ExecutionProfile:    "repo_local",
		ProjectMemoryPolicy: agentprofiles.MemoryInclude,
		ContextSourcePolicy: agentprofiles.ContextIncludeEnabled,
	}); err != nil {
		t.Fatalf("Create profile: %v", err)
	}
	if _, err := handler.projects.Update(t.Context(), "proj_start", func(project *projects.Project) {
		project.ContextSources = []projects.ContextSource{
			{
				ID:             "ctx_agents",
				Kind:           "workspace_instruction",
				Title:          "AGENTS.md",
				Path:           "AGENTS.md",
				Enabled:        true,
				Format:         "agents_md",
				TrustLabel:     "workspace_guidance",
				SourceCategory: "workspace_guidance",
				Metadata:       map[string]string{"root_id": "root_start"},
			},
			{
				ID:             "ctx_claude",
				Kind:           "host_instruction",
				Title:          "CLAUDE.md",
				Path:           "CLAUDE.md",
				Enabled:        true,
				Format:         "claude_md",
				TrustLabel:     "workspace_guidance",
				SourceCategory: "workspace_guidance",
				Metadata:       map[string]string{"root_id": "root_start", "host": "claude"},
			},
		}
	}); err != nil {
		t.Fatalf("Update project context sources: %v", err)
	}
	if _, err := handler.memory.Create(t.Context(), memory.Entry{
		ID:         "mem_runtime",
		ProjectID:  "proj_start",
		Title:      "Runtime preference",
		Body:       "Keep explicit prompt context visible in the task system prompt.",
		TrustLabel: memory.TrustLabelOperatorMemory,
		SourceKind: memory.SourceKindOperator,
		Enabled:    true,
	}); err != nil {
		t.Fatalf("Create project memory: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("start assignment status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var assignment ProjectWorkAssignmentEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &assignment); err != nil {
		t.Fatalf("decode assignment: %v", err)
	}
	ref := assignmentExecutionRefForTest(t, assignment.Data)
	task, found, err := handler.taskStore.GetTask(t.Context(), ref.TaskID)
	if err != nil || !found {
		t.Fatalf("GetTask(%q) found=%v err=%v, want task", ref.TaskID, found, err)
	}
	for _, want := range []string{
		"Project memory: Runtime preference",
		"Keep explicit prompt context visible in the task system prompt.",
		"Workspace instruction: AGENTS.md",
		"Use the portable workspace guidance.",
	} {
		if !strings.Contains(task.SystemPrompt, want) {
			t.Fatalf("task system_prompt = %q, want explicit prompt context fragment %q", task.SystemPrompt, want)
		}
	}
	if strings.Contains(task.SystemPrompt, "Host-specific Claude guidance") {
		t.Fatalf("task system_prompt = %q, want host-specific source body omitted", task.SystemPrompt)
	}

	packetResp := mustRequestJSON[ChatContextPacketResponse](newAPITestClient(t, server), http.MethodGet, "/hecate/v1/tasks/"+ref.TaskID+"/runs/"+ref.RunID+"/context", "")
	promptItem := findRenderedContextItemByKind(packetResp.Data, "prompt_context")
	if promptItem == nil || !promptItem.Included || promptItem.Section != contextSectionInstructions {
		t.Fatalf("prompt context item = %+v, want included instructions item", promptItem)
	}
	for _, want := range []string{
		"Included project memory entries: 1",
		"Included workspace instruction sources: 1",
		"CLAUDE.md is metadata-only",
	} {
		if !strings.Contains(promptItem.Body, want) {
			t.Fatalf("prompt context body = %q, want %q", promptItem.Body, want)
		}
	}
}

func TestProjectWorkAPI_StartAssignmentKeepsVisibleOnlyPromptContextOutOfSystemPrompt(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	handler.SetMemoryStore(memory.NewMemoryStore())
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("Do not include this visible-only source body."), 0o644); err != nil {
		t.Fatalf("Write AGENTS.md: %v", err)
	}
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace:        workspace,
		Driver:           projectwork.AssignmentDriverHecateTask,
		RoleAgentProfile: "prof_visible_context",
	})
	if _, err := handler.agentProfiles.Create(t.Context(), agentprofiles.Profile{
		ID:                  "prof_visible_context",
		Name:                "Implementation",
		Surface:             agentprofiles.SurfaceHecateTask,
		ExecutionProfile:    "repo_local",
		ProjectMemoryPolicy: agentprofiles.MemoryVisibleOnly,
		ContextSourcePolicy: agentprofiles.ContextVisibleOnly,
	}); err != nil {
		t.Fatalf("Create profile: %v", err)
	}
	if _, err := handler.projects.Update(t.Context(), "proj_start", func(project *projects.Project) {
		project.ContextSources = []projects.ContextSource{{
			ID:             "ctx_agents",
			Kind:           "workspace_instruction",
			Title:          "AGENTS.md",
			Path:           "AGENTS.md",
			Enabled:        true,
			Format:         "agents_md",
			TrustLabel:     "workspace_guidance",
			SourceCategory: "workspace_guidance",
			Metadata:       map[string]string{"root_id": "root_start"},
		}}
	}); err != nil {
		t.Fatalf("Update project context sources: %v", err)
	}
	if _, err := handler.memory.Create(t.Context(), memory.Entry{
		ID:         "mem_runtime",
		ProjectID:  "proj_start",
		Title:      "Runtime preference",
		Body:       "Do not include this visible-only memory body.",
		TrustLabel: memory.TrustLabelOperatorMemory,
		SourceKind: memory.SourceKindOperator,
		Enabled:    true,
	}); err != nil {
		t.Fatalf("Create project memory: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("start assignment status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var assignment ProjectWorkAssignmentEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &assignment); err != nil {
		t.Fatalf("decode assignment: %v", err)
	}
	ref := assignmentExecutionRefForTest(t, assignment.Data)
	task, found, err := handler.taskStore.GetTask(t.Context(), ref.TaskID)
	if err != nil || !found {
		t.Fatalf("GetTask(%q) found=%v err=%v, want task", ref.TaskID, found, err)
	}
	for _, notWant := range []string{
		"Do not include this visible-only memory body.",
		"Do not include this visible-only source body.",
	} {
		if strings.Contains(task.SystemPrompt, notWant) {
			t.Fatalf("task system_prompt = %q, want visible-only body %q omitted", task.SystemPrompt, notWant)
		}
	}
	packetResp := mustRequestJSON[ChatContextPacketResponse](newAPITestClient(t, server), http.MethodGet, "/hecate/v1/tasks/"+ref.TaskID+"/runs/"+ref.RunID+"/context", "")
	if item := findRenderedContextItemByKind(packetResp.Data, "prompt_context"); item != nil {
		t.Fatalf("prompt context item = %+v, want omitted when no prompt context was loaded", item)
	}
}

func TestProjectWorkAPI_StartAssignmentTruncatesPromptContext(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	handler.SetMemoryStore(memory.NewMemoryStore())
	workspace := t.TempDir()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace:        workspace,
		Driver:           projectwork.AssignmentDriverHecateTask,
		RoleAgentProfile: "prof_truncate_context",
	})
	if _, err := handler.agentProfiles.Create(t.Context(), agentprofiles.Profile{
		ID:                  "prof_truncate_context",
		Name:                "Implementation",
		Surface:             agentprofiles.SurfaceHecateTask,
		ExecutionProfile:    "repo_local",
		ProjectMemoryPolicy: agentprofiles.MemoryInclude,
		ContextSourcePolicy: agentprofiles.ContextVisibleOnly,
	}); err != nil {
		t.Fatalf("Create profile: %v", err)
	}
	if _, err := handler.memory.Create(t.Context(), memory.Entry{
		ID:         "mem_long",
		ProjectID:  "proj_start",
		Title:      "Long memory",
		Body:       strings.Repeat("memory-context ", 400),
		TrustLabel: memory.TrustLabelOperatorMemory,
		SourceKind: memory.SourceKindOperator,
		Enabled:    true,
	}); err != nil {
		t.Fatalf("Create project memory: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("start assignment status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var assignment ProjectWorkAssignmentEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &assignment); err != nil {
		t.Fatalf("decode assignment: %v", err)
	}
	ref := assignmentExecutionRefForTest(t, assignment.Data)
	task, found, err := handler.taskStore.GetTask(t.Context(), ref.TaskID)
	if err != nil || !found {
		t.Fatalf("GetTask(%q) found=%v err=%v, want task", ref.TaskID, found, err)
	}
	if !strings.Contains(task.SystemPrompt, "[truncated]") {
		t.Fatalf("task system_prompt = %q, want truncated prompt context marker", task.SystemPrompt)
	}
	packetResp := mustRequestJSON[ChatContextPacketResponse](newAPITestClient(t, server), http.MethodGet, "/hecate/v1/tasks/"+ref.TaskID+"/runs/"+ref.RunID+"/context", "")
	promptItem := findRenderedContextItemByKind(packetResp.Data, "prompt_context")
	if promptItem == nil || !strings.Contains(promptItem.Body, "Truncated prompt context items: 1") || !strings.Contains(promptItem.Body, "mem_long was truncated") {
		t.Fatalf("prompt context item = %+v, want truncation summary", promptItem)
	}
}

func TestProjectWorkAPI_StartAssignmentSnapshotsResolvedAgentProfile(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	workspace := t.TempDir()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace:           workspace,
		Driver:              projectwork.AssignmentDriverHecateTask,
		WithoutRoleDefaults: true,
	})
	if _, err := handler.agentProfiles.Create(t.Context(), agentprofiles.Profile{
		ID:                  "prof_role",
		Name:                "Role profile",
		Instructions:        "Use the profile-specific review checklist.",
		Surface:             agentprofiles.SurfaceHecateTask,
		ProviderHint:        "anthropic",
		ModelHint:           "claude-sonnet-4",
		ExecutionProfile:    "role_profile",
		ToolsEnabled:        true,
		WritesAllowed:       true,
		NetworkAllowed:      false,
		ApprovalPolicy:      agentprofiles.ApprovalRequire,
		ProjectMemoryPolicy: agentprofiles.MemoryVisibleOnly,
		ContextSourcePolicy: agentprofiles.ContextIncludeEnabled,
		SkillIDs:            []string{"backend", "review"},
	}); err != nil {
		t.Fatalf("Create role profile: %v", err)
	}
	if _, err := handler.agentProfiles.Create(t.Context(), agentprofiles.Profile{
		ID:               "prof_project",
		Name:             "Project profile",
		Surface:          agentprofiles.SurfaceHecateTask,
		ModelHint:        "qwen2.5-coder",
		ExecutionProfile: "project_profile",
	}); err != nil {
		t.Fatalf("Create project profile: %v", err)
	}
	if _, err := handler.projects.Update(t.Context(), "proj_start", func(project *projects.Project) {
		project.DefaultAgentProfile = "prof_project"
		project.DefaultProvider = ""
		project.DefaultModel = ""
	}); err != nil {
		t.Fatalf("Update project defaults: %v", err)
	}
	if _, err := handler.projectWork.UpdateRole(t.Context(), "proj_start", "role_backend", func(role *projectwork.AgentRoleProfile) {
		role.DefaultAgentProfile = "prof_role"
		role.SkillIDs = []string{"backend"}
	}); err != nil {
		t.Fatalf("Update role defaults: %v", err)
	}
	if _, err := handler.projectSkills.UpsertDiscovered(t.Context(), "proj_start", []projectskills.Skill{
		{
			ID:         "backend",
			ProjectID:  "proj_start",
			Title:      "Backend",
			Path:       ".hecate/skills/backend/SKILL.md",
			RootID:     "root_start",
			Format:     projectskills.FormatSkillMD,
			Enabled:    true,
			Status:     projectskills.StatusAvailable,
			TrustLabel: projectskills.TrustWorkspaceSkill,
		},
		{
			ID:         "review",
			ProjectID:  "proj_start",
			Title:      "Review",
			Path:       ".hecate/skills/review/SKILL.md",
			RootID:     "root_start",
			Format:     projectskills.FormatSkillMD,
			Enabled:    false,
			Status:     projectskills.StatusAvailable,
			TrustLabel: projectskills.TrustWorkspaceSkill,
		},
	}); err != nil {
		t.Fatalf("UpsertDiscovered skills: %v", err)
	}

	preflightResp := mustRequestJSON[ChatContextPacketResponse](newAPITestClient(t, server), http.MethodGet, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/preflight", "")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("start assignment status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var assignment ProjectWorkAssignmentEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &assignment); err != nil {
		t.Fatalf("decode assignment: %v", err)
	}
	ref := assignmentExecutionRefForTest(t, assignment.Data)
	task, found, err := handler.taskStore.GetTask(t.Context(), ref.TaskID)
	if err != nil || !found {
		t.Fatalf("GetTask(%q) found=%v err=%v, want task", ref.TaskID, found, err)
	}
	if task.RequestedProvider != "anthropic" || task.RequestedModel != "claude-sonnet-4" || task.ExecutionProfile != "role_profile" {
		t.Fatalf("task provider/model/profile = %q/%q/%q, want role profile hints", task.RequestedProvider, task.RequestedModel, task.ExecutionProfile)
	}
	if !strings.Contains(task.SystemPrompt, "Agent profile instructions:\nUse the profile-specific review checklist.") {
		t.Fatalf("task system prompt = %q, want profile instructions", task.SystemPrompt)
	}

	packetResp := mustRequestJSON[ChatContextPacketResponse](newAPITestClient(t, server), http.MethodGet, "/hecate/v1/tasks/"+ref.TaskID+"/runs/"+ref.RunID+"/context", "")
	if packetResp.Data.ExecutionProfile != "role_profile" {
		t.Fatalf("packet execution_profile = %q, want role_profile", packetResp.Data.ExecutionProfile)
	}
	if preflightResp.Data.Provider != packetResp.Data.Provider || preflightResp.Data.Model != packetResp.Data.Model || preflightResp.Data.ExecutionProfile != packetResp.Data.ExecutionProfile || preflightResp.Data.Workspace != packetResp.Data.Workspace {
		t.Fatalf("preflight launch plan = %q/%q/%q/%q, persisted launch context = %q/%q/%q/%q", preflightResp.Data.Provider, preflightResp.Data.Model, preflightResp.Data.ExecutionProfile, preflightResp.Data.Workspace, packetResp.Data.Provider, packetResp.Data.Model, packetResp.Data.ExecutionProfile, packetResp.Data.Workspace)
	}
	profileItem := findRenderedContextItemByOrigin(packetResp.Data, "prof_role")
	if profileItem == nil || !profileItem.Included || profileItem.Section != contextSectionProfile {
		t.Fatalf("profile item = %+v, want included profile section item", profileItem)
	}
	for _, want := range []string{
		"ID: prof_role",
		"Source: role_default",
		"Provider hint: anthropic",
		"Model hint: claude-sonnet-4",
		"Execution profile: role_profile",
		"Instructions:\nUse the profile-specific review checklist.",
		"Skills: backend, review",
	} {
		if !strings.Contains(profileItem.Body, want) {
			t.Fatalf("profile body = %q, want %q", profileItem.Body, want)
		}
	}
	skillsItem := findRenderedContextItemByOrigin(packetResp.Data, "project_skills")
	if skillsItem == nil || !skillsItem.Included || skillsItem.Section != contextSectionSkills {
		t.Fatalf("project skills item = %+v, want included skills section item", skillsItem)
	}
	for _, want := range []string{
		"Requested: backend, review",
		"Resolved enabled skills: backend (.hecate/skills/backend/SKILL.md)",
		"review:disabled",
	} {
		if !strings.Contains(skillsItem.Body, want) {
			t.Fatalf("project skills body = %q, want %q", skillsItem.Body, want)
		}
	}
}

func TestProjectWorkAPI_StartExternalAgentAssignmentPreparesLinkedSession(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	runner := &fakeAgentChatRunner{nativeSessionID: "native_project_external"}
	handler.SetAgentChatRunner(runner)
	workspace := t.TempDir()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace:           workspace,
		Driver:              projectwork.AssignmentDriverExternalAgent,
		WithoutRoleDefaults: true,
	})
	if _, err := handler.memory.Create(t.Context(), memory.Entry{
		ID:         "mem_external",
		ProjectID:  "proj_start",
		Title:      "External boundary",
		Body:       "Do not inject this into external-agent prompts.",
		TrustLabel: memory.TrustLabelOperatorMemory,
		SourceKind: memory.SourceKindOperator,
		Enabled:    true,
	}); err != nil {
		t.Fatalf("Create external assignment memory: %v", err)
	}
	if _, err := handler.projects.Update(t.Context(), "proj_start", func(project *projects.Project) {
		project.ContextSources = []projects.ContextSource{{
			ID:         "ctx_external",
			Kind:       "workspace_instruction",
			Title:      "AGENTS.md",
			Path:       "AGENTS.md",
			Enabled:    true,
			TrustLabel: "workspace_guidance",
		}}
	}); err != nil {
		t.Fatalf("Update project context source: %v", err)
	}
	if _, err := handler.agentProfiles.Create(t.Context(), agentprofiles.Profile{
		ID:                  "prof_external",
		Name:                "External implementer",
		Instructions:        "Use the linked project context before editing.",
		Surface:             agentprofiles.SurfaceExternalAgent,
		ExecutionProfile:    "external_implementation",
		ExternalAgentKind:   "codex",
		ApprovalPolicy:      agentprofiles.ApprovalRequire,
		ProjectMemoryPolicy: agentprofiles.MemoryInclude,
		ContextSourcePolicy: agentprofiles.ContextIncludeEnabled,
		SkillIDs:            []string{"project-handoff"},
	}); err != nil {
		t.Fatalf("Create external profile: %v", err)
	}
	if _, err := handler.projectWork.UpdateRole(t.Context(), "proj_start", "role_backend", func(role *projectwork.AgentRoleProfile) {
		role.DefaultAgentProfile = "prof_external"
	}); err != nil {
		t.Fatalf("Update role profile: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{"driver_kind":"external_agent"}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("start assignment status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var assignment ProjectWorkAssignmentEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &assignment); err != nil {
		t.Fatalf("decode assignment: %v", err)
	}
	ref := assignmentExecutionRefForTest(t, assignment.Data)
	if ref.ChatSessionID == "" || ref.ContextSnapshotID == "" {
		t.Fatalf("assignment execution_ref = %+v, want linked session and context", ref)
	}
	if ref.TaskID != "" || ref.RunID != "" || ref.MessageID != "" {
		t.Fatalf("assignment execution_ref = %+v, want no task run or dispatched message", ref)
	}
	if assignment.Data.Status != projectwork.AssignmentStatusRunning {
		t.Fatalf("assignment status = %q, want running", assignment.Data.Status)
	}
	if len(runner.prepareRequests) != 1 {
		t.Fatalf("prepare requests = %d, want 1", len(runner.prepareRequests))
	}
	if len(runner.runRequests) != 0 {
		t.Fatalf("run requests = %d, want no automatic external-agent turn", len(runner.runRequests))
	}
	prepare := runner.prepareRequests[0]
	resolvedWorkspace, err := agentadapters.ValidateWorkspace(workspace)
	if err != nil {
		t.Fatalf("ValidateWorkspace: %v", err)
	}
	if prepare.AdapterID != "codex" || prepare.SessionID != ref.ChatSessionID || prepare.Workspace != resolvedWorkspace {
		t.Fatalf("prepare request = %+v, want codex session in workspace", prepare)
	}
	session, ok, err := handler.agentChat.Get(t.Context(), ref.ChatSessionID)
	if err != nil || !ok {
		t.Fatalf("Get chat session found=%v err=%v, want linked session", ok, err)
	}
	if session.AgentID != "codex" || session.DriverKind != agentadapters.DriverKindACP || session.NativeSessionID != "native_project_external" || session.ProjectID != "proj_start" {
		t.Fatalf("session = %+v, want prepared codex project session", session)
	}

	packetResp := mustRequestJSON[ChatContextPacketResponse](newAPITestClient(t, server), http.MethodGet, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/context", "")
	if packetResp.Data.ID != ref.ContextSnapshotID {
		t.Fatalf("assignment context id = %q, want %q", packetResp.Data.ID, ref.ContextSnapshotID)
	}
	if packetResp.Data.ExecutionMode != chat.ExecutionModeExternalAgent || packetResp.Data.Refs == nil || packetResp.Data.Refs.SessionID != ref.ChatSessionID {
		t.Fatalf("packet execution/refs = %q/%+v, want external agent session refs", packetResp.Data.ExecutionMode, packetResp.Data.Refs)
	}
	profileItem := findRenderedContextItemByOrigin(packetResp.Data, "prof_external")
	if profileItem == nil || profileItem.Section != contextSectionProfile || !strings.Contains(profileItem.Body, "External agent: codex") {
		t.Fatalf("profile item = %+v, want external profile metadata", profileItem)
	}
	memoryItem := findRenderedContextItemByOrigin(packetResp.Data, "mem_external")
	if memoryItem == nil || memoryItem.Included {
		t.Fatalf("external assignment memory item = %+v, want visible-only memory despite include policy", memoryItem)
	}
	if !strings.Contains(memoryItem.InclusionReason, "does not inject memory bodies into adapter prompts") {
		t.Fatalf("external assignment memory reason = %q, want adapter prompt boundary", memoryItem.InclusionReason)
	}
	sourceItem := findRenderedContextItemByOrigin(packetResp.Data, "AGENTS.md")
	if sourceItem == nil || sourceItem.Included {
		t.Fatalf("external assignment source item = %+v, want visible-only source despite include policy", sourceItem)
	}
	if !strings.Contains(sourceItem.InclusionReason, "does not inject source bodies into adapter prompts") {
		t.Fatalf("external assignment source reason = %q, want adapter prompt boundary", sourceItem.InclusionReason)
	}
	policyItem := findRenderedContextItemByOrigin(packetResp.Data, "external_agent_assignment.prompt_context")
	if policyItem == nil || policyItem.Included {
		t.Fatalf("external assignment prompt policy item = %+v, want inspect-only policy note", policyItem)
	}
	for _, want := range []string{"does not dispatch an adapter prompt", "Profile project_memory_policy: include", "Profile context_source_policy: include_enabled"} {
		if !strings.Contains(policyItem.Body, want) {
			t.Fatalf("external assignment prompt policy body = %q, want %q", policyItem.Body, want)
		}
	}
}

func TestProjectWorkAPI_StartExternalAgentAssignmentMirrorsResultToCairnline(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkCairnlineMirrorTestServer(t)
	runner := &fakeAgentChatRunner{nativeSessionID: "native_project_external"}
	handler.SetAgentChatRunner(runner)
	workspace := t.TempDir()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace:           workspace,
		Driver:              projectwork.AssignmentDriverExternalAgent,
		WithoutRoleDefaults: true,
	})
	if _, err := handler.agentProfiles.Create(t.Context(), agentprofiles.Profile{
		ID:                  "prof_external",
		Name:                "External implementer",
		Surface:             agentprofiles.SurfaceExternalAgent,
		ExecutionProfile:    "external_implementation",
		ExternalAgentKind:   "codex",
		ProjectMemoryPolicy: agentprofiles.MemoryVisibleOnly,
		ContextSourcePolicy: agentprofiles.ContextVisibleOnly,
	}); err != nil {
		t.Fatalf("Create external profile: %v", err)
	}
	if _, err := handler.projectWork.UpdateRole(t.Context(), "proj_start", "role_backend", func(role *projectwork.AgentRoleProfile) {
		role.DefaultAgentProfile = "prof_external"
	}); err != nil {
		t.Fatalf("Update role profile: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{"driver_kind":"external_agent"}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("start external assignment status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var assignment ProjectWorkAssignmentEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &assignment); err != nil {
		t.Fatalf("decode assignment: %v", err)
	}
	ref := assignmentExecutionRefForTest(t, assignment.Data)
	if ref.ChatSessionID == "" || ref.ContextSnapshotID == "" {
		t.Fatalf("assignment execution_ref = %+v, want linked session and context snapshot", ref)
	}

	stored := getStoredProjectWorkAssignmentForTest(t, handler, "proj_start", "work_start", "asgn_start")
	mirrored := getMirroredCairnlineAssignmentForTest(t, handler, "proj_start", "asgn_start")
	if mirrored.Status != cairnlinebridge.AssignmentStatus(stored.Status) || mirrored.ExecutionMode != cairnline.ExecutionExternalAdapter {
		t.Fatalf("mirrored assignment status/mode = %q/%q, want %q/external_adapter", mirrored.Status, mirrored.ExecutionMode, cairnlinebridge.AssignmentStatus(stored.Status))
	}
	if mirrored.ExecutionRef != stored.ExecutionRef.ChatSessionID || mirrored.ContextSnapshotID != stored.ExecutionRef.ContextSnapshotID {
		t.Fatalf("mirrored assignment execution = ref %q context %q, want %q/%q", mirrored.ExecutionRef, mirrored.ContextSnapshotID, stored.ExecutionRef.ChatSessionID, stored.ExecutionRef.ContextSnapshotID)
	}
}

func TestProjectWorkAPI_ExternalAgentChatTurnReconcilesAssignmentStatus(t *testing.T) {
	t.Parallel()

	handler, server := newProjectWorkCairnlineMirrorTestServer(t)
	handler.SetAgentChatRunner(&fakeAgentChatRunner{output: "implementation complete"})
	ctx := t.Context()
	workspace := t.TempDir()
	if _, err := handler.projects.Create(ctx, projects.Project{ID: "proj_chat_reconcile", Name: "Chat reconcile"}); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	if _, err := handler.projectWork.CreateRole(ctx, projectwork.AgentRoleProfile{ID: "role_chat_reconcile", ProjectID: "proj_chat_reconcile", Name: "External implementer"}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if _, err := handler.projectWork.CreateWorkItem(ctx, projectwork.WorkItem{ID: "work_chat_reconcile", ProjectID: "proj_chat_reconcile", Title: "Reconcile chat"}); err != nil {
		t.Fatalf("CreateWorkItem: %v", err)
	}
	if _, err := handler.agentChat.Create(ctx, chat.Session{
		ID:              "chat_project_reconcile",
		ProjectID:       "proj_chat_reconcile",
		AgentID:         "codex",
		DriverKind:      agentadapters.DriverKindACP,
		NativeSessionID: "native_project_reconcile",
		Workspace:       workspace,
		Status:          "idle",
	}); err != nil {
		t.Fatalf("Create chat session: %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(ctx, projectwork.Assignment{
		ID:         "asgn_chat_reconcile",
		ProjectID:  "proj_chat_reconcile",
		WorkItemID: "work_chat_reconcile",
		RoleID:     "role_chat_reconcile",
		DriverKind: projectwork.AssignmentDriverExternalAgent,
		Status:     projectwork.AssignmentStatusRunning,
		ExecutionRef: projectwork.AssignmentExecutionRef{
			Kind:          projectwork.AssignmentExecutionKindChatSession,
			ChatSessionID: "chat_project_reconcile",
			Status:        projectwork.AssignmentStatusRunning,
		},
	}); err != nil {
		t.Fatalf("CreateAssignment: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/chat/sessions/chat_project_reconcile/messages", strings.NewReader(`{"content":"finish the assignment"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("create chat message status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var chatResp ChatSessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &chatResp); err != nil {
		t.Fatalf("decode chat response: %v", err)
	}
	if chatResp.Data.Status != "completed" {
		t.Fatalf("chat status = %q, want completed", chatResp.Data.Status)
	}

	assignments, err := handler.projectWork.ListAssignments(ctx, projectwork.AssignmentFilter{ProjectID: "proj_chat_reconcile", WorkItemID: "work_chat_reconcile"})
	if err != nil {
		t.Fatalf("ListAssignments: %v", err)
	}
	if len(assignments) != 1 {
		t.Fatalf("assignments = %+v, want one linked assignment", assignments)
	}
	if assignments[0].Status != projectwork.AssignmentStatusCompleted || assignments[0].ExecutionRef.MessageID == "" || assignments[0].ExecutionRef.Status != projectwork.AssignmentStatusCompleted {
		t.Fatalf("stored assignment = %+v, want reconciled completed chat assignment", assignments[0])
	}
	mirrored := getMirroredCairnlineAssignmentForTest(t, handler, "proj_chat_reconcile", "asgn_chat_reconcile")
	if mirrored.Status != cairnline.AssignmentCompleted || mirrored.ExecutionRef != "chat_project_reconcile" {
		t.Fatalf("mirrored assignment = %+v, want completed chat-session execution ref", mirrored)
	}
}

func TestProjectWorkAPI_PreflightExternalAgentAssignmentShowsSessionTargetWithoutPreparing(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	runner := &fakeAgentChatRunner{nativeSessionID: "native_project_external"}
	handler.SetAgentChatRunner(runner)
	workspace := t.TempDir()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace:           workspace,
		Driver:              projectwork.AssignmentDriverExternalAgent,
		WithoutRoleDefaults: true,
	})
	if _, err := handler.agentProfiles.Create(t.Context(), agentprofiles.Profile{
		ID:                "prof_external",
		Name:              "External implementer",
		Surface:           agentprofiles.SurfaceExternalAgent,
		ExecutionProfile:  "external_implementation",
		ExternalAgentKind: "codex",
	}); err != nil {
		t.Fatalf("Create external profile: %v", err)
	}
	if _, err := handler.projectWork.UpdateRole(t.Context(), "proj_start", "role_backend", func(role *projectwork.AgentRoleProfile) {
		role.DefaultAgentProfile = "prof_external"
	}); err != nil {
		t.Fatalf("Update role profile: %v", err)
	}

	packetResp := mustRequestJSON[ChatContextPacketResponse](newAPITestClient(t, server), http.MethodGet, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/preflight", "")
	if packetResp.Data.ExecutionMode != chat.ExecutionModeExternalAgent || packetResp.Data.Provider != "" || packetResp.Data.Model != "" {
		t.Fatalf("preflight mode/provider/model = %q/%q/%q, want external agent without provider/model", packetResp.Data.ExecutionMode, packetResp.Data.Provider, packetResp.Data.Model)
	}
	if packetResp.Data.Refs == nil || packetResp.Data.Refs.SessionID != "" || packetResp.Data.Refs.TaskID != "" || packetResp.Data.Refs.RunID != "" {
		t.Fatalf("preflight refs = %+v, want no prepared chat/task/run refs", packetResp.Data.Refs)
	}
	item := findRenderedContextItemByOrigin(packetResp.Data, "project_assignment.preflight")
	if item == nil || item.Section != contextSectionRuntime || item.Included {
		t.Fatalf("preflight item = %+v, want inspect-only runtime item", item)
	}
	for _, want := range []string{"Driver: external_agent", "Adapter ID: codex", "Chat session: created when the assignment is prepared", "Session title: Native assignment start - Backend engineer"} {
		if !strings.Contains(item.Body, want) {
			t.Fatalf("preflight body = %q, want %q", item.Body, want)
		}
	}
	if len(runner.prepareRequests) != 0 || len(runner.runRequests) != 0 {
		t.Fatalf("runner requests = prepare %d run %d, want no external-agent side effects", len(runner.prepareRequests), len(runner.runRequests))
	}
	sessions, err := handler.agentChat.List(t.Context())
	if err != nil {
		t.Fatalf("List chat sessions: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("sessions = %+v, want no chat session created by preflight", sessions)
	}
}

func TestProjectWorkAPI_PreflightAndStartShareExternalAgentLaunchPlan(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	runner := &fakeAgentChatRunner{nativeSessionID: "native_project_external"}
	handler.SetAgentChatRunner(runner)
	workspace := t.TempDir()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace:           workspace,
		Driver:              projectwork.AssignmentDriverExternalAgent,
		WithoutRoleDefaults: true,
	})
	if _, err := handler.agentProfiles.Create(t.Context(), agentprofiles.Profile{
		ID:                "prof_external",
		Name:              "External implementer",
		Surface:           agentprofiles.SurfaceExternalAgent,
		ExecutionProfile:  "external_implementation",
		ExternalAgentKind: "codex",
		SkillIDs:          []string{"project-handoff"},
	}); err != nil {
		t.Fatalf("Create external profile: %v", err)
	}
	if _, err := handler.projectWork.UpdateRole(t.Context(), "proj_start", "role_backend", func(role *projectwork.AgentRoleProfile) {
		role.DefaultAgentProfile = "prof_external"
	}); err != nil {
		t.Fatalf("Update role profile: %v", err)
	}

	client := newAPITestClient(t, server)
	preflight := mustRequestJSON[ChatContextPacketResponse](client, http.MethodGet, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/preflight", "")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{"driver_kind":"external_agent"}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("start assignment status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var assignment ProjectWorkAssignmentEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &assignment); err != nil {
		t.Fatalf("decode assignment: %v", err)
	}
	ref := assignmentExecutionRefForTest(t, assignment.Data)
	started := mustRequestJSON[ChatContextPacketResponse](client, http.MethodGet, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/context", "")

	if preflight.Data.ExecutionMode != started.Data.ExecutionMode ||
		preflight.Data.Provider != started.Data.Provider ||
		preflight.Data.Model != started.Data.Model ||
		preflight.Data.ExecutionProfile != started.Data.ExecutionProfile ||
		preflight.Data.Workspace != started.Data.Workspace {
		t.Fatalf("preflight external shape = mode/provider/model/profile/workspace %q/%q/%q/%q/%q, started = %q/%q/%q/%q/%q",
			preflight.Data.ExecutionMode, preflight.Data.Provider, preflight.Data.Model, preflight.Data.ExecutionProfile, preflight.Data.Workspace,
			started.Data.ExecutionMode, started.Data.Provider, started.Data.Model, started.Data.ExecutionProfile, started.Data.Workspace)
	}
	if started.Data.Refs == nil || started.Data.Refs.SessionID != ref.ChatSessionID || started.Data.Refs.TaskID != "" || started.Data.Refs.RunID != "" {
		t.Fatalf("started refs = %+v, want external chat session only", started.Data.Refs)
	}
	if len(runner.prepareRequests) != 1 {
		t.Fatalf("prepare requests = %d, want 1", len(runner.prepareRequests))
	}
	prepare := runner.prepareRequests[0]
	if prepare.AdapterID != "codex" || prepare.SessionID != ref.ChatSessionID || prepare.Workspace != preflight.Data.Workspace {
		t.Fatalf("prepare request = %+v, want codex session %q in preflight workspace %q", prepare, ref.ChatSessionID, preflight.Data.Workspace)
	}
	session, ok, err := handler.agentChat.Get(t.Context(), ref.ChatSessionID)
	if err != nil || !ok {
		t.Fatalf("Get chat session found=%v err=%v, want linked session", ok, err)
	}
	if session.Title != "Native assignment start - Backend engineer - Codex" || session.Workspace != preflight.Data.Workspace || session.AgentID != "codex" {
		t.Fatalf("session = %+v, want preflight launch target", session)
	}
}

func TestProjectWorkAPI_StartExternalAgentAssignmentConcurrentRequestsCreateOneChat(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	runner := newConcurrentProjectExternalPrepareRunner()
	handler.SetAgentChatRunner(runner)
	workspace := t.TempDir()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace:           workspace,
		Driver:              projectwork.AssignmentDriverExternalAgent,
		WithoutRoleDefaults: true,
	})
	if _, err := handler.agentProfiles.Create(t.Context(), agentprofiles.Profile{
		ID:                "prof_external",
		Name:              "External implementer",
		Surface:           agentprofiles.SurfaceExternalAgent,
		ExternalAgentKind: "claude_code",
	}); err != nil {
		t.Fatalf("Create external profile: %v", err)
	}
	if _, err := handler.projectWork.UpdateRole(t.Context(), "proj_start", "role_backend", func(role *projectwork.AgentRoleProfile) {
		role.DefaultAgentProfile = "prof_external"
	}); err != nil {
		t.Fatalf("Update role profile: %v", err)
	}

	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{"driver_kind":"external_agent"}`))))
			statuses <- rec.Code
		}()
	}
	released := false
	defer func() {
		if !released {
			close(runner.releasePrepare)
		}
	}()
	for range 2 {
		select {
		case <-runner.prepareStarted:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for concurrent external-agent prepares")
		}
	}
	close(runner.releasePrepare)
	released = true
	wg.Wait()
	close(statuses)

	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	if counts[http.StatusOK] != 1 || counts[http.StatusConflict] != 1 {
		t.Fatalf("concurrent external start statuses = %+v, want one 200 and one 409", counts)
	}
	sessions, err := handler.agentChat.List(t.Context())
	if err != nil {
		t.Fatalf("List chats: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("chat session count = %d, want one surviving linked chat: %+v", len(sessions), sessions)
	}
	assignments, err := handler.projectWork.ListAssignments(t.Context(), projectwork.AssignmentFilter{ProjectID: "proj_start", WorkItemID: "work_start"})
	if err != nil {
		t.Fatalf("ListAssignments: %v", err)
	}
	if len(assignments) != 1 || assignments[0].ExecutionRef.ChatSessionID != sessions[0].ID {
		t.Fatalf("assignment/chat link = %+v / %+v, want one linked surviving chat", assignments, sessions)
	}
	if got := runner.prepareCount(); got != 2 {
		t.Fatalf("prepare requests = %d, want both requests to reach prepare", got)
	}
	if got := runner.deletedCount(); got != 1 {
		t.Fatalf("deleted sessions = %d, want losing prepared chat deleted", got)
	}
	if got := runner.closedCount(); got != 0 {
		t.Fatalf("closed sessions = %d, want destructive rollback to use delete", got)
	}
}

func TestProjectWorkAPI_StartExternalAgentAssignmentRejectsUnsupportedProfileOptions(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	runner := &fakeAgentChatRunner{}
	handler.SetAgentChatRunner(runner)
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace:           t.TempDir(),
		Driver:              projectwork.AssignmentDriverExternalAgent,
		WithoutRoleDefaults: true,
	})
	if _, err := handler.agentProfiles.Create(t.Context(), agentprofiles.Profile{
		ID:                   "prof_external_bad_options",
		Name:                 "External implementer",
		Surface:              agentprofiles.SurfaceExternalAgent,
		ExecutionProfile:     "external_implementation",
		ExternalAgentKind:    "codex",
		ExternalAgentOptions: map[string]string{"unsupported": "value"},
	}); err != nil {
		t.Fatalf("Create external profile: %v", err)
	}
	if _, err := handler.projectWork.UpdateRole(t.Context(), "proj_start", "role_backend", func(role *projectwork.AgentRoleProfile) {
		role.DefaultAgentProfile = "prof_external_bad_options"
	}); err != nil {
		t.Fatalf("Update role profile: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{"driver_kind":"external_agent"}`))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("start assignment status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
	if len(runner.prepareRequests) != 0 || len(runner.runRequests) != 0 {
		t.Fatalf("external agent requests = prepare %d run %d, want none when profile launch options are invalid", len(runner.prepareRequests), len(runner.runRequests))
	}
}

func TestProjectWorkAPI_StartAssignmentSnapshotsMissingProfileWarning(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	workspace := t.TempDir()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace:           workspace,
		Driver:              projectwork.AssignmentDriverHecateTask,
		WithoutRoleDefaults: true,
	})
	if _, err := handler.projects.Update(t.Context(), "proj_start", func(project *projects.Project) {
		project.DefaultAgentProfile = "prof_missing"
	}); err != nil {
		t.Fatalf("Update project profile: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("start assignment status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var assignment ProjectWorkAssignmentEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &assignment); err != nil {
		t.Fatalf("decode assignment: %v", err)
	}
	ref := assignmentExecutionRefForTest(t, assignment.Data)
	packetResp := mustRequestJSON[ChatContextPacketResponse](newAPITestClient(t, server), http.MethodGet, "/hecate/v1/tasks/"+ref.TaskID+"/runs/"+ref.RunID+"/context", "")
	profileItem := findRenderedContextItemByOrigin(packetResp.Data, "prof_missing")
	if profileItem == nil || profileItem.Included || profileItem.Section != contextSectionProfile {
		t.Fatalf("profile item = %+v, want excluded missing profile item", profileItem)
	}
	if !strings.Contains(profileItem.Body, "profile \"prof_missing\" was not found") {
		t.Fatalf("profile body = %q, want missing profile warning", profileItem.Body)
	}
	warning := findRenderedContextItemByKind(packetResp.Data, "profile_warning")
	if warning == nil || warning.Included || !strings.Contains(warning.Body, "profile \"prof_missing\" was not found") {
		t.Fatalf("profile warning = %+v, want excluded warning item", warning)
	}
}

func TestProjectWorkAPI_StartAssignmentReturnsErrorWhenProfileStoreFails(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	workspace := t.TempDir()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace:           workspace,
		Driver:              projectwork.AssignmentDriverHecateTask,
		WithoutRoleDefaults: true,
	})
	if _, err := handler.projects.Update(t.Context(), "proj_start", func(project *projects.Project) {
		project.DefaultAgentProfile = "prof_project"
	}); err != nil {
		t.Fatalf("Update project profile: %v", err)
	}
	handler.SetAgentProfileStore(failingAgentProfileStore{err: errors.New("profile store unavailable")})

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("start assignment status = %d body=%s, want 500", rec.Code, rec.Body.String())
	}
}

func TestProjectWorkAPI_StartAssignmentKeepsExplicitModelEqualToRouterDefault(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	handler.config.Router.DefaultModel = "qwen2.5-coder"
	workspace := t.TempDir()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace:           workspace,
		Driver:              projectwork.AssignmentDriverHecateTask,
		WithoutRoleDefaults: true,
	})
	if _, err := handler.agentProfiles.Create(t.Context(), agentprofiles.Profile{
		ID:        "prof_project",
		Name:      "Project profile",
		Surface:   agentprofiles.SurfaceHecateTask,
		ModelHint: "claude-sonnet-4",
	}); err != nil {
		t.Fatalf("Create project profile: %v", err)
	}
	if _, err := handler.projects.Update(t.Context(), "proj_start", func(project *projects.Project) {
		project.DefaultAgentProfile = "prof_project"
		project.DefaultModel = "qwen2.5-coder"
	}); err != nil {
		t.Fatalf("Update project defaults: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("start assignment status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var assignment ProjectWorkAssignmentEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &assignment); err != nil {
		t.Fatalf("decode assignment: %v", err)
	}
	ref := assignmentExecutionRefForTest(t, assignment.Data)
	task, found, err := handler.taskStore.GetTask(t.Context(), ref.TaskID)
	if err != nil || !found {
		t.Fatalf("GetTask(%q) found=%v err=%v, want task", ref.TaskID, found, err)
	}
	if task.RequestedModel != "qwen2.5-coder" {
		t.Fatalf("task requested model = %q, want explicit project default", task.RequestedModel)
	}
}

func TestProjectWorkAPI_StartAssignmentKeepsExplicitRoleModelEqualToRouterDefault(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	handler.config.Router.DefaultModel = "qwen2.5-coder"
	workspace := t.TempDir()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace:           workspace,
		Driver:              projectwork.AssignmentDriverHecateTask,
		WithoutRoleDefaults: true,
	})
	if _, err := handler.agentProfiles.Create(t.Context(), agentprofiles.Profile{
		ID:        "prof_role",
		Name:      "Role profile",
		Surface:   agentprofiles.SurfaceHecateTask,
		ModelHint: "claude-sonnet-4",
	}); err != nil {
		t.Fatalf("Create role profile: %v", err)
	}
	if _, err := handler.projects.Update(t.Context(), "proj_start", func(project *projects.Project) {
		project.DefaultModel = ""
	}); err != nil {
		t.Fatalf("Update project defaults: %v", err)
	}
	if _, err := handler.projectWork.UpdateRole(t.Context(), "proj_start", "role_backend", func(role *projectwork.AgentRoleProfile) {
		role.DefaultAgentProfile = "prof_role"
		role.DefaultModel = "qwen2.5-coder"
	}); err != nil {
		t.Fatalf("Update role defaults: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("start assignment status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var assignment ProjectWorkAssignmentEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &assignment); err != nil {
		t.Fatalf("decode assignment: %v", err)
	}
	ref := assignmentExecutionRefForTest(t, assignment.Data)
	task, found, err := handler.taskStore.GetTask(t.Context(), ref.TaskID)
	if err != nil || !found {
		t.Fatalf("GetTask(%q) found=%v err=%v, want task", ref.TaskID, found, err)
	}
	if task.RequestedModel != "qwen2.5-coder" {
		t.Fatalf("task requested model = %q, want explicit role default", task.RequestedModel)
	}
}

func TestProjectWorkAPI_AssignmentContextFallsBackToLinkedChatPacket(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Driver: projectwork.AssignmentDriverExternalAgent,
		Status: projectwork.AssignmentStatusCompleted,
	})
	if _, err := handler.agentChat.Create(t.Context(), chat.Session{ID: "chat_linked", ProjectID: "proj_start"}); err != nil {
		t.Fatalf("Create chat session: %v", err)
	}
	packet := chatcontext.Normalize(chat.ContextPacket{
		ID:            "ctx_linked",
		Version:       chatContextPacketVersion,
		ExecutionMode: chat.ExecutionModeExternalAgent,
		Items: []chat.ContextItem{{
			Kind:            "external_agent_session",
			TrustLevel:      contextTrustRuntimeState,
			Origin:          "adapter:Codex",
			Title:           "Codex ACP session",
			Included:        true,
			InclusionReason: "Visible external-agent metadata for this turn",
		}},
	}, chat.ContextRefs{
		SessionID: "chat_linked",
		MessageID: "msg_linked",
		ProjectID: "proj_start",
	})
	if _, err := handler.agentChat.AppendMessage(t.Context(), "chat_linked", chat.Message{
		ID:      "msg_linked",
		Role:    "assistant",
		Content: "done",
		Context: packet,
	}); err != nil {
		t.Fatalf("Append linked message: %v", err)
	}
	if _, err := handler.projectWork.UpdateAssignment(t.Context(), "proj_start", "asgn_start", func(item *projectwork.Assignment) {
		item.ExecutionRef = projectwork.AssignmentExecutionRef{
			Kind:          projectwork.AssignmentExecutionKindChatSession,
			ChatSessionID: "chat_linked",
			MessageID:     "msg_linked",
		}
	}); err != nil {
		t.Fatalf("Update assignment links: %v", err)
	}

	resp := mustRequestJSON[ChatContextPacketResponse](newAPITestClient(t, server), http.MethodGet, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/context", "")
	if resp.Data.ID != "ctx_linked" || resp.Data.Refs == nil || resp.Data.Refs.SessionID != "chat_linked" || resp.Data.Refs.MessageID != "msg_linked" {
		t.Fatalf("assignment chat fallback packet = %+v, want linked chat refs", resp.Data)
	}
}

func TestProjectWorkAPI_StartAssignmentFallsBackToProjectDefaults(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	workspace := t.TempDir()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace:           workspace,
		Driver:              projectwork.AssignmentDriverHecateTask,
		ProjectAgentProfile: "project_review",
		WithoutRoleDefaults: true,
	})

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("start assignment status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var assignment ProjectWorkAssignmentEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &assignment); err != nil {
		t.Fatalf("decode assignment: %v", err)
	}
	ref := assignmentExecutionRefForTest(t, assignment.Data)
	task, found, err := handler.taskStore.GetTask(t.Context(), ref.TaskID)
	if err != nil || !found {
		t.Fatalf("GetTask(%q) found=%v err=%v, want task", ref.TaskID, found, err)
	}
	if task.RequestedProvider != "ollama" || task.RequestedModel != "qwen2.5-coder" || task.ExecutionProfile != "project_review" {
		t.Fatalf("task provider/model/profile = %q/%q/%q, want project defaults", task.RequestedProvider, task.RequestedModel, task.ExecutionProfile)
	}
	for _, want := range []string{
		"- Provider: ollama",
		"- Model: qwen2.5-coder",
		"- Profile: project_review",
		"Role defaults: none",
		"Project defaults: provider=ollama, model=qwen2.5-coder, profile=project_review, workspace_mode=in_place",
	} {
		if !strings.Contains(task.Prompt, want) {
			t.Fatalf("task prompt = %q, want project-default fragment %q", task.Prompt, want)
		}
	}
}

func TestProjectWorkAPI_AssignmentPromptIndentsMultilineLaunchContextValues(t *testing.T) {
	t.Parallel()
	prompt := projectworkapp.AssignmentPrompt(
		projects.Project{
			ID:              "proj_multiline",
			Name:            "Hecate",
			DefaultProvider: "ollama",
			DefaultModel:    "qwen2.5-coder",
		},
		projectwork.WorkItem{
			ID:       "work_multiline",
			Title:    "Launch context",
			Brief:    "Expose project work.\nKeep the first launch editable.",
			Status:   projectwork.WorkItemStatusReady,
			Priority: "high",
		},
		projectwork.Assignment{
			ID:         "asgn_multiline",
			RoleID:     "role_multiline",
			DriverKind: projectwork.AssignmentDriverHecateTask,
			Status:     projectwork.AssignmentStatusQueued,
		},
		projectwork.AgentRoleProfile{
			ID:           "role_multiline",
			Name:         "Software developer",
			Description:  "Owns implementation work.\nCoordinates with review.",
			Instructions: "Keep changes reviewable.\nCall out risks.",
		},
	)

	assertLaunchContextContract(t, prompt)
	for _, want := range []string{
		"- Brief: Expose project work.\n  Keep the first launch editable.",
		"- Description: Owns implementation work.\n  Coordinates with review.",
		"- Instructions: Keep changes reviewable.\n  Call out risks.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt = %q, want indented multiline fragment %q", prompt, want)
		}
	}
}

func TestProjectWorkAPI_StartAssignmentAllowsChunkedEmptyBody(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace: t.TempDir(),
		Driver:    projectwork.AssignmentDriverHecateTask,
	})

	req := httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", io.NopCloser(strings.NewReader("")))
	req.ContentLength = -1
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("start assignment with empty chunked body status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
}

func TestProjectWorkAPI_StartAssignmentRejectsMalformedBody(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace: t.TempDir(),
		Driver:    projectwork.AssignmentDriverHecateTask,
	})

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", strings.NewReader(`{`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("start assignment malformed body status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
}

func TestProjectWorkAPI_StartAssignmentRejectsMissingWorkspaceRoot(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Driver: projectwork.AssignmentDriverHecateTask,
	})

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("start missing workspace status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
	tasks, err := handler.taskStore.ListTasks(t.Context(), taskstateFilterAll())
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks = %+v, want none created", tasks)
	}
}

func TestProjectWorkAPI_StartExternalAgentAssignmentRequiresProfileKind(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	handler.SetAgentChatRunner(&fakeAgentChatRunner{})
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace: t.TempDir(),
		Driver:    projectwork.AssignmentDriverExternalAgent,
	})

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{"driver_kind":"external_agent"}`))))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("start external assignment status = %d body=%s, want 422", rec.Code, rec.Body.String())
	}
}

func TestProjectWorkAPI_StartAssignmentRejectsDriverMismatch(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace: t.TempDir(),
		Driver:    projectwork.AssignmentDriverHecateTask,
	})

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{"driver_kind":"external_agent"}`))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("start mismatched driver status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
	tasks, err := handler.taskStore.ListTasks(t.Context(), taskstateFilterAll())
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks = %+v, want none created", tasks)
	}
}

func TestProjectWorkAPI_StartAssignmentRepeatedReturnsCurrentAssignment(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace: t.TempDir(),
		Driver:    projectwork.AssignmentDriverHecateTask,
	})

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("first start status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var first ProjectWorkAssignmentEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode first assignment: %v", err)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusConflict {
		t.Fatalf("second start status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}
	var second ProjectWorkAssignmentEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode second assignment: %v", err)
	}
	firstRef := assignmentExecutionRefForTest(t, first.Data)
	secondRef := assignmentExecutionRefForTest(t, second.Data)
	if secondRef.TaskID != firstRef.TaskID || secondRef.RunID != firstRef.RunID {
		t.Fatalf("second assignment execution_ref = %+v, want existing %+v", secondRef, firstRef)
	}
	tasks, err := handler.taskStore.ListTasks(t.Context(), taskstateFilterAll())
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("task count = %d, want 1", len(tasks))
	}
}

func TestProjectWorkAPI_StartAssignmentActiveConflictBeatsModelValidation(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	taskID := "task_active_start"
	runID := "run_active_start"
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace: t.TempDir(),
		Driver:    projectwork.AssignmentDriverHecateTask,
		Status:    projectwork.AssignmentStatusQueued,
		TaskID:    taskID,
		RunID:     runID,
	})
	if _, err := handler.taskStore.CreateTask(t.Context(), types.Task{
		ID:          taskID,
		Title:       "Active assignment",
		Status:      "running",
		LatestRunID: runID,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := handler.taskStore.CreateRun(t.Context(), types.TaskRun{
		ID:     runID,
		TaskID: taskID,
		Number: 1,
		Status: "running",
	}); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	handler.config.Router.DefaultModel = ""
	if _, err := handler.projects.Update(t.Context(), "proj_start", func(project *projects.Project) {
		project.DefaultModel = ""
		project.DefaultAgentProfile = ""
	}); err != nil {
		t.Fatalf("Update project defaults: %v", err)
	}
	if _, err := handler.projectWork.UpdateRole(t.Context(), "proj_start", "role_backend", func(role *projectwork.AgentRoleProfile) {
		role.DefaultModel = ""
		role.DefaultAgentProfile = ""
	}); err != nil {
		t.Fatalf("Update role defaults: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusConflict {
		t.Fatalf("start status = %d body=%s, want 409 before model validation", rec.Code, rec.Body.String())
	}
	var response ProjectWorkAssignmentEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode assignment: %v", err)
	}
	ref := assignmentExecutionRefForTest(t, response.Data)
	if ref.TaskID != taskID || ref.RunID != runID {
		t.Fatalf("assignment execution_ref = %+v, want existing task/run %s/%s", ref, taskID, runID)
	}
}

func TestProjectWorkAPI_StartAssignmentLinklessActiveStatusReturnsConflict(t *testing.T) {
	t.Parallel()
	for _, status := range []string{projectwork.AssignmentStatusRunning, projectwork.AssignmentStatusAwaitingApproval} {
		status := status
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			handler, server := newProjectWorkTestServer()
			seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
				Workspace: t.TempDir(),
				Driver:    projectwork.AssignmentDriverHecateTask,
				Status:    status,
			})

			rec := httptest.NewRecorder()
			server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{}`))))
			if rec.Code != http.StatusConflict {
				t.Fatalf("start status = %d body=%s, want 409", rec.Code, rec.Body.String())
			}
			tasks, err := handler.taskStore.ListTasks(t.Context(), taskstateFilterAll())
			if err != nil {
				t.Fatalf("ListTasks: %v", err)
			}
			if len(tasks) != 0 {
				t.Fatalf("tasks = %+v, want none created", tasks)
			}
		})
	}
}

func TestProjectWorkAPI_StartAssignmentConcurrentRequestsCreateOneTask(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace: t.TempDir(),
		Driver:    projectwork.AssignmentDriverHecateTask,
	})

	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{}`))))
			statuses <- rec.Code
		}()
	}
	wg.Wait()
	close(statuses)

	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	if counts[http.StatusOK] != 1 || counts[http.StatusConflict] != 1 {
		t.Fatalf("concurrent start statuses = %+v, want one 200 and one 409", counts)
	}
	tasks, err := handler.taskStore.ListTasks(t.Context(), taskstateFilterAll())
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("task count = %d, want 1", len(tasks))
	}
}

func TestProjectWorkAPI_StartAssignmentTerminalReturnsCurrentAssignment(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace: t.TempDir(),
		Driver:    projectwork.AssignmentDriverHecateTask,
		Status:    projectwork.AssignmentStatusCompleted,
		TaskID:    "task_existing",
		RunID:     "run_existing",
	})

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusConflict {
		t.Fatalf("terminal start status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}
	var assignment ProjectWorkAssignmentEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &assignment); err != nil {
		t.Fatalf("decode assignment: %v", err)
	}
	ref := assignmentExecutionRefForTest(t, assignment.Data)
	if ref.TaskID != "task_existing" || ref.RunID != "run_existing" {
		t.Fatalf("terminal assignment execution_ref = %+v, want existing links", ref)
	}
	tasks, err := handler.taskStore.ListTasks(t.Context(), taskstateFilterAll())
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks = %+v, want none created", tasks)
	}
}

func TestProjectWorkAPI_StartAssignmentPreservesTaskLinkWhenStartFails(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	failingRunner := orchestrator.NewRunner(quietLogger(), nil, nil, orchestrator.Config{})
	t.Cleanup(func() { _ = failingRunner.Shutdown(t.Context()) })
	handler.taskRunner = failingRunner
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace: t.TempDir(),
		Driver:    projectwork.AssignmentDriverHecateTask,
	})

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("start failure status = %d body=%s, want 500", rec.Code, rec.Body.String())
	}
	assignments, err := handler.projectWork.ListAssignments(t.Context(), projectwork.AssignmentFilter{ProjectID: "proj_start", WorkItemID: "work_start"})
	if err != nil {
		t.Fatalf("ListAssignments: %v", err)
	}
	if len(assignments) != 1 {
		t.Fatalf("assignments = %+v, want one assignment", assignments)
	}
	assignment := assignments[0]
	if assignment.ExecutionRef.TaskID == "" {
		t.Fatalf("assignment execution_ref = %+v, want preserved task link", assignment.ExecutionRef)
	}
	if assignment.Status != projectwork.AssignmentStatusFailed {
		t.Fatalf("assignment status = %q, want failed", assignment.Status)
	}
	if assignment.CompletedAt.IsZero() {
		t.Fatalf("assignment completed_at is zero, want failure timestamp")
	}
	if _, found, err := handler.taskStore.GetTask(t.Context(), assignment.ExecutionRef.TaskID); err != nil || !found {
		t.Fatalf("GetTask(%q) found=%v err=%v, want preserved task", assignment.ExecutionRef.TaskID, found, err)
	}
}

func TestProjectWorkAPI_StartAssignmentFailureMirrorsResultToCairnline(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkCairnlineMirrorTestServer(t)
	failingRunner := orchestrator.NewRunner(quietLogger(), nil, nil, orchestrator.Config{})
	t.Cleanup(func() { _ = failingRunner.Shutdown(t.Context()) })
	handler.taskRunner = failingRunner
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace: t.TempDir(),
		Driver:    projectwork.AssignmentDriverHecateTask,
	})

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("start failure status = %d body=%s, want 500", rec.Code, rec.Body.String())
	}
	stored := getStoredProjectWorkAssignmentForTest(t, handler, "proj_start", "work_start", "asgn_start")
	if stored.Status != projectwork.AssignmentStatusFailed || stored.ExecutionRef.TaskID == "" {
		t.Fatalf("stored assignment = %+v, want failed assignment with preserved task ref", stored)
	}

	mirrored := getMirroredCairnlineAssignmentForTest(t, handler, "proj_start", "asgn_start")
	if mirrored.Status != cairnline.AssignmentFailed || mirrored.ExecutionMode != cairnline.ExecutionOrchestrated {
		t.Fatalf("mirrored assignment status/mode = %q/%q, want failed/orchestrated", mirrored.Status, mirrored.ExecutionMode)
	}
	if mirrored.ExecutionRef != stored.ExecutionRef.TaskID || mirrored.ContextSnapshotID != "" {
		t.Fatalf("mirrored assignment execution = ref %q context %q, want failed task ref %q and no context snapshot", mirrored.ExecutionRef, mirrored.ContextSnapshotID, stored.ExecutionRef.TaskID)
	}
}

func TestProjectWorkAPI_StartAssignmentClearsClaimWhenTaskCreateFails(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	handler.taskStore = failingCreateTaskStore{Store: handler.taskStore}
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace: t.TempDir(),
		Driver:    projectwork.AssignmentDriverHecateTask,
	})

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("task create failure status = %d body=%s, want 500", rec.Code, rec.Body.String())
	}
	assignments, err := handler.projectWork.ListAssignments(t.Context(), projectwork.AssignmentFilter{ProjectID: "proj_start", WorkItemID: "work_start"})
	if err != nil {
		t.Fatalf("ListAssignments: %v", err)
	}
	if len(assignments) != 1 {
		t.Fatalf("assignments = %+v, want one assignment", assignments)
	}
	assignment := assignments[0]
	if assignment.ExecutionRef.TaskID != "" || assignment.ExecutionRef.RunID != "" {
		t.Fatalf("assignment execution_ref = %+v, want cleared task/run links", assignment.ExecutionRef)
	}
	if assignment.Status != projectwork.AssignmentStatusQueued {
		t.Fatalf("assignment status = %q, want queued for retry", assignment.Status)
	}
	if !assignment.StartedAt.IsZero() || !assignment.CompletedAt.IsZero() {
		t.Fatalf("assignment timestamps = started %v completed %v, want cleared", assignment.StartedAt, assignment.CompletedAt)
	}
}

func TestProjectWorkAPI_StartAssignmentReleasesMirroredCairnlineClaimWhenTaskCreateFails(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkCairnlineMirrorTestServer(t)
	handler.taskStore = failingCreateTaskStore{Store: handler.taskStore}
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace: t.TempDir(),
		Driver:    projectwork.AssignmentDriverHecateTask,
	})
	assignment := getStoredProjectWorkAssignmentForTest(t, handler, "proj_start", "work_start", "asgn_start")
	if err := handler.writeProjectAssignmentToCairnline(t.Context(), assignment); err != nil {
		t.Fatalf("writeProjectAssignmentToCairnline() error = %v", err)
	}
	func() {
		service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
		if err != nil {
			t.Fatalf("open Cairnline mirror: %v", err)
		}
		defer store.Close()
		claimed, err := service.ClaimAssignment(t.Context(), "proj_start", "asgn_start", "hecate")
		if err != nil {
			t.Fatalf("ClaimAssignment() error = %v", err)
		}
		if claimed.Status != cairnline.AssignmentClaimed || claimed.ClaimedBy != "hecate" {
			t.Fatalf("claimed assignment = %+v, want hecate claim", claimed)
		}
	}()

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("task create failure status = %d body=%s, want 500", rec.Code, rec.Body.String())
	}
	mirrored := getMirroredCairnlineAssignmentForTest(t, handler, "proj_start", "asgn_start")
	if mirrored.Status != cairnline.AssignmentQueued || mirrored.ClaimedBy != "" || mirrored.ExecutionRef != "" || mirrored.ContextSnapshotID != "" || !mirrored.StartedAt.IsZero() || !mirrored.CompletedAt.IsZero() {
		t.Fatalf("mirrored assignment = %+v, want released queued claim for retry", mirrored)
	}
}

func TestProjectWorkAPI_AssignmentExecutionProjection(t *testing.T) {
	t.Parallel()
	for _, backend := range []string{"memory", "sqlite"} {
		backend := backend
		t.Run(backend, func(t *testing.T) {
			t.Parallel()
			handler, server := newProjectWorkProjectionTestServer(t, backend)
			seedProjectWorkProjectionTest(t, handler)

			running := getProjectWorkAssignmentForTest(t, server, "work_running", "asgn_running")
			if running.Status != projectwork.AssignmentStatusRunning || running.Execution == nil || running.Execution.RunStatus != "running" {
				t.Fatalf("running assignment = %+v, want projected running execution", running)
			}
			if running.ExecutionRef == nil || running.ExecutionRef.Kind != "task_run" || running.ExecutionRef.TaskID != running.Execution.TaskID || running.ExecutionRef.RunID != running.Execution.RunID || running.ExecutionRef.Status != running.Status {
				t.Fatalf("running execution_ref = %+v, want projected task/run ref for %+v", running.ExecutionRef, running.Execution)
			}
			if running.StartedAt == "" {
				t.Fatalf("running assignment started_at is empty, want projected run timestamp")
			}
			assertStoredProjectWorkAssignmentStatusForTest(t, handler, "work_running", "asgn_running", projectwork.AssignmentStatusQueued)

			queued := getProjectWorkAssignmentForTest(t, server, "work_queued", "asgn_queued")
			if queued.Status != projectwork.AssignmentStatusQueued || queued.Execution == nil || queued.Execution.RunStatus != "queued" {
				t.Fatalf("queued assignment = %+v, want projected queued execution", queued)
			}
			assertProjectWorkStatusForTest(t, server, "work_queued", projectwork.WorkItemStatusRunning)

			awaiting := getProjectWorkAssignmentForTest(t, server, "work_awaiting", "asgn_awaiting")
			if awaiting.Status != projectwork.AssignmentStatusAwaitingApproval || awaiting.Execution == nil || awaiting.Execution.PendingApprovalCount != 1 {
				t.Fatalf("awaiting assignment = %+v, want awaiting approval with one pending approval", awaiting)
			}
			if awaiting.ExecutionRef == nil || awaiting.ExecutionRef.PendingApprovalCount != 1 || awaiting.ExecutionRef.Status != projectwork.AssignmentStatusAwaitingApproval {
				t.Fatalf("awaiting execution_ref = %+v, want awaiting ref with pending approval count", awaiting.ExecutionRef)
			}

			completed := getProjectWorkAssignmentForTest(t, server, "work_completed", "asgn_completed")
			if completed.Status != projectwork.AssignmentStatusCompleted || completed.CompletedAt == "" || completed.Execution == nil || completed.Execution.RunStatus != "completed" {
				t.Fatalf("completed assignment = %+v, want completed projection", completed)
			}
			assertProjectWorkStatusForTest(t, server, "work_completed", projectwork.WorkItemStatusDone)

			failed := getProjectWorkAssignmentForTest(t, server, "work_failed", "asgn_failed")
			if failed.Status != projectwork.AssignmentStatusFailed || failed.Execution == nil || failed.Execution.LastError != "model failed" {
				t.Fatalf("failed assignment = %+v, want failed projection with run error", failed)
			}
			assertProjectWorkStatusForTest(t, server, "work_failed", projectwork.WorkItemStatusBlocked)

			cancelled := getProjectWorkAssignmentForTest(t, server, "work_cancelled", "asgn_cancelled")
			if cancelled.Status != projectwork.AssignmentStatusCancelled || cancelled.Execution == nil || cancelled.Execution.RunStatus != "cancelled" {
				t.Fatalf("cancelled assignment = %+v, want cancelled projection", cancelled)
			}
			assertProjectWorkStatusForTest(t, server, "work_cancelled", projectwork.WorkItemStatusCancelled)

			missing := getProjectWorkAssignmentForTest(t, server, "work_missing", "asgn_missing")
			if missing.Status != projectwork.AssignmentStatusQueued || missing.Execution == nil || !missing.Execution.Missing {
				t.Fatalf("missing assignment = %+v, want stored queued status with missing execution marker", missing)
			}
			assertProjectWorkStatusForTest(t, server, "work_missing", projectwork.WorkItemStatusReady)

			runOnly := getProjectWorkAssignmentForTest(t, server, "work_run_only", "asgn_run_only")
			runOnlyRef := assignmentExecutionRefForTest(t, runOnly)
			if runOnly.Status != projectwork.AssignmentStatusQueued || runOnlyRef.RunID == "" || runOnly.Execution != nil {
				t.Fatalf("run-only assignment = %+v, want stored queued status without execution projection", runOnly)
			}
			if runOnlyRef.Kind != "task_run" {
				t.Fatalf("run-only execution_ref = %+v, want raw run ref", runOnly.ExecutionRef)
			}
			assertProjectWorkStatusForTest(t, server, "work_run_only", projectwork.WorkItemStatusReady)

			manual := getProjectWorkAssignmentForTest(t, server, "work_manual_terminal", "asgn_manual_terminal")
			if manual.Status != projectwork.AssignmentStatusFailed || manual.Execution == nil || manual.Execution.RunStatus != "completed" {
				t.Fatalf("manual terminal assignment = %+v, want newer explicit failed status over stale completed run", manual)
			}
			assertProjectWorkStatusForTest(t, server, "work_manual_terminal", projectwork.WorkItemStatusBlocked)

			external := getProjectWorkAssignmentForTest(t, server, "work_external_chat", "asgn_external_chat")
			externalRef := assignmentExecutionRefForTest(t, external)
			if external.Status != projectwork.AssignmentStatusCompleted || external.CompletedAt == "" || external.Execution != nil {
				t.Fatalf("external chat assignment = %+v, want projected completed chat without task execution summary", external)
			}
			if externalRef.Kind != projectwork.AssignmentExecutionKindChatSession || externalRef.ChatSessionID != "chat_external_projection" || externalRef.MessageID != "msg_external_done" || externalRef.Status != projectwork.AssignmentStatusCompleted {
				t.Fatalf("external chat execution_ref = %+v, want completed linked chat ref", externalRef)
			}

			assertProjectWorkStatusForTest(t, server, "work_mixed", projectwork.WorkItemStatusBlocked)
			assertProjectWorkListStatusForTest(t, server, "work_mixed", projectwork.WorkItemStatusBlocked)
		})
	}
}

func TestProjectWorkAPI_ProjectActivity(t *testing.T) {
	t.Parallel()
	for _, backend := range []string{"memory", "sqlite"} {
		backend := backend
		t.Run(backend, func(t *testing.T) {
			t.Parallel()
			handler, server := newProjectWorkProjectionTestServer(t, backend)
			seedProjectWorkProjectionTest(t, handler)
			handler.agentChat = failingChatGetStore{Store: handler.agentChat, failingID: "chat_external_error"}

			rec := httptest.NewRecorder()
			server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_projection/activity", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("activity status = %d body=%s, want 200", rec.Code, rec.Body.String())
			}
			var response ProjectActivityEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode activity: %v", err)
			}
			if response.Object != "project_activity" || response.Data.ProjectID != "proj_projection" {
				t.Fatalf("activity envelope = %+v, want project_activity for project", response)
			}
			if response.Data.ReadBackend != "hecate" {
				t.Fatalf("activity read_backend = %q, want hecate", response.Data.ReadBackend)
			}
			if response.Data.Summary.WorkItemCount == 0 || response.Data.Summary.AssignmentCount == 0 {
				t.Fatalf("activity summary = %+v, want work and assignment counts", response.Data.Summary)
			}

			awaiting := findProjectActivityItemForTest(t, response.Data.Buckets.Blocked, "asgn_awaiting")
			if awaiting.BlockingSignal != "awaiting_approval" || awaiting.Assignment.Execution == nil || awaiting.Assignment.Execution.PendingApprovalCount != 1 {
				t.Fatalf("awaiting activity = %+v, want approval blocking signal", awaiting)
			}
			if awaiting.Assignment.ExecutionRef == nil || awaiting.Assignment.ExecutionRef.PendingApprovalCount != 1 || awaiting.LinkedTaskID != awaiting.Assignment.ExecutionRef.TaskID || awaiting.LinkedRunID != awaiting.Assignment.ExecutionRef.RunID {
				t.Fatalf("awaiting activity execution_ref = %+v linked=%q/%q, want ref-backed links", awaiting.Assignment.ExecutionRef, awaiting.LinkedTaskID, awaiting.LinkedRunID)
			}
			if awaiting.WorkItem.Status != projectwork.WorkItemStatusRunning || awaiting.Role.Name != "Projection engineer" {
				t.Fatalf("awaiting context = %+v, want projected work item and role", awaiting)
			}

			failed := findProjectActivityItemForTest(t, response.Data.Buckets.Blocked, "asgn_failed")
			if failed.BlockingSignal != "failed" || failed.StatusSummary != "model failed" {
				t.Fatalf("failed activity = %+v, want failed signal and compact error", failed)
			}

			cancelled := findProjectActivityItemForTest(t, response.Data.Buckets.Blocked, "asgn_cancelled")
			if cancelled.BlockingSignal != "cancelled" || cancelled.StatusSummary != "cancelled" {
				t.Fatalf("cancelled activity = %+v, want cancelled signal without chat-specific summary", cancelled)
			}

			missing := findProjectActivityItemForTest(t, response.Data.Buckets.Blocked, "asgn_missing")
			if missing.BlockingSignal != "stale_unknown" || missing.LinkedTaskID == "" {
				t.Fatalf("missing activity = %+v, want stale/unknown with linked task id", missing)
			}

			runOnly := findProjectActivityItemForTest(t, response.Data.Buckets.Blocked, "asgn_run_only")
			if runOnly.BlockingSignal != "stale_unknown" || runOnly.LinkedRunID != "run_without_task" {
				t.Fatalf("run-only activity = %+v, want stale/unknown with linked run id", runOnly)
			}

			notStarted := findProjectActivityItemForTest(t, response.Data.Buckets.Blocked, "asgn_not_started")
			if notStarted.BlockingSignal != "not_started" || notStarted.LinkedTaskID != "" || notStarted.LinkedRunID != "" {
				t.Fatalf("not-started activity = %+v, want not_started without linked runtime ids", notStarted)
			}
			if notStarted.ArtifactSummary.Count != 1 || notStarted.ArtifactSummary.AssignmentID != "" {
				t.Fatalf("not-started artifact summary = %+v, want work-item artifact without assignment attribution", notStarted.ArtifactSummary)
			}

			completed := findProjectActivityItemForTest(t, response.Data.Buckets.Completed, "asgn_completed")
			if completed.BlockingSignal != "completed" || completed.ArtifactSummary.Count != 1 || completed.ArtifactSummary.LatestTitle != "Completion handoff" || completed.ArtifactSummary.AssignmentID != "asgn_completed" {
				t.Fatalf("completed activity = %+v, want artifact signal", completed)
			}
			if completed.HandoffSummary.Count != 1 || completed.HandoffSummary.LatestTitle != "Review follow-up" {
				t.Fatalf("completed handoff summary = %+v, want source assignment handoff signal", completed.HandoffSummary)
			}

			queued := findProjectActivityItemForTest(t, response.Data.Buckets.Active, "asgn_queued")
			if queued.HandoffSummary.Count != 1 || queued.HandoffSummary.LatestTitle != "Review follow-up" || queued.HandoffSummary.TargetWorkItem != "work_queued" {
				t.Fatalf("target handoff summary = %+v, want target assignment handoff signal", queued.HandoffSummary)
			}

			external := findProjectActivityItemForTest(t, allProjectActivityItemsForTest(response.Data), "asgn_external_chat")
			if external.LinkedChat == nil || external.LinkedChat.ID != "chat_external_projection" || external.LinkedChat.LatestMessageID != "msg_external_done" {
				t.Fatalf("external linked chat = %+v, want chat summary with latest message", external.LinkedChat)
			}
			if external.Assignment.ExecutionRef == nil || external.Assignment.ExecutionRef.Kind != "chat_session" || external.LinkedChatID != external.Assignment.ExecutionRef.ChatSessionID {
				t.Fatalf("external execution_ref = %+v linked_chat=%q, want chat-session ref", external.Assignment.ExecutionRef, external.LinkedChatID)
			}
			if external.BlockingSignal != "completed" || external.StatusSummary != "linked chat · running · assistant completed · 2 messages" {
				t.Fatalf("external activity = %+v, want linked chat completed summary", external)
			}

			missingChat := findProjectActivityItemForTest(t, allProjectActivityItemsForTest(response.Data), "asgn_missing_chat")
			if missingChat.LinkedChat == nil || !missingChat.LinkedChat.Missing || missingChat.BlockingSignal != "stale_unknown" || missingChat.StatusSummary != "linked chat missing" {
				t.Fatalf("missing chat activity = %+v, want stale linked chat signal", missingChat)
			}

			crossProjectChat := findProjectActivityItemForTest(t, allProjectActivityItemsForTest(response.Data), "asgn_cross_project_chat")
			if crossProjectChat.LinkedChat == nil || !crossProjectChat.LinkedChat.Missing || crossProjectChat.LinkedChat.Title != "" || crossProjectChat.LinkedChat.LatestMessageID != "" {
				t.Fatalf("cross-project chat activity = %+v, want missing without foreign chat metadata", crossProjectChat)
			}

			errorChat := findProjectActivityItemForTest(t, allProjectActivityItemsForTest(response.Data), "asgn_error_chat")
			if errorChat.LinkedChat == nil || !errorChat.LinkedChat.Missing || errorChat.BlockingSignal != "stale_unknown" || errorChat.StatusSummary != "linked chat missing" {
				t.Fatalf("error chat activity = %+v, want degraded missing linked chat", errorChat)
			}

			preparedChat := findProjectActivityItemForTest(t, allProjectActivityItemsForTest(response.Data), "asgn_prepared_chat")
			if preparedChat.BlockingSignal != "running" || preparedChat.LinkedChat == nil || preparedChat.LinkedChat.Status != "idle" {
				t.Fatalf("prepared chat activity = %+v, want idle linked chat treated as running assignment", preparedChat)
			}

			failedChat := findProjectActivityItemForTest(t, allProjectActivityItemsForTest(response.Data), "asgn_failed_chat")
			if failedChat.BlockingSignal != "failed" || failedChat.StatusSummary != "adapter auth failed" {
				t.Fatalf("failed chat activity = %+v, want linked chat error surfaced", failedChat)
			}

			if len(response.Data.Recent) == 0 || len(response.Data.Buckets.Recent) != len(response.Data.Recent) {
				t.Fatalf("recent activity = %+v buckets=%+v, want mirrored recent list", response.Data.Recent, response.Data.Buckets.Recent)
			}
		})
	}
}

func TestProjectWorkAPI_ProjectActivityCairnlineConfiguredUsesReadModel(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{CoordinationBackend: "cairnline"},
	}, quietLogger(), nil, nil, nil, nil)
	server := NewServer(quietLogger(), handler)
	seedProjectWorkProjectionTest(t, handler)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_projection/activity", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("activity status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectActivityEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode activity: %v", err)
	}
	if response.Data.ReadBackend != "cairnline" {
		t.Fatalf("activity read_backend = %q, want cairnline", response.Data.ReadBackend)
	}
	if response.Data.Summary.WorkItemCount == 0 || response.Data.Summary.AssignmentCount == 0 || response.Data.Summary.RecentCount == 0 {
		t.Fatalf("activity summary = %+v, want Cairnline-backed activity counts", response.Data.Summary)
	}
	awaiting := findProjectActivityItemForTest(t, response.Data.Buckets.Blocked, "asgn_awaiting")
	if awaiting.BlockingSignal != "awaiting_approval" || awaiting.StatusSummary != "1 approval pending" || awaiting.Assignment.ExecutionRef == nil {
		t.Fatalf("awaiting activity = %+v, want Hecate approval projection over Cairnline activity", awaiting)
	}
	if awaiting.WorkItem.Title != "work_awaiting" || awaiting.Role.Name != "Projection engineer" {
		t.Fatalf("awaiting context = %+v role=%+v, want work and role enrichment", awaiting.WorkItem, awaiting.Role)
	}
	completed := findProjectActivityItemForTest(t, response.Data.Buckets.Completed, "asgn_completed")
	if completed.ArtifactSummary.Count != 1 || completed.HandoffSummary.Count != 1 {
		t.Fatalf("completed activity = %+v, want artifact and handoff enrichment", completed)
	}
}

func TestProjectWorkAPI_ProjectActivityStrictEmbeddedReadModelReadsWithoutHecateProject(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend: "cairnline",
			CairnlineReadSource: "embedded",
		},
	}, quietLogger(), nil, nil, nil, nil)
	server := NewServer(quietLogger(), handler)
	const projectID = "proj_embedded_activity"

	if err := handler.withCairnlineEmbeddedMirrorService(t.Context(), func(service *cairnline.Service) error {
		if _, err := service.CreateProject(t.Context(), cairnline.Project{
			ID:            projectID,
			Name:          "Embedded Activity",
			DefaultRootID: "root_embedded_activity",
			Roots: []cairnline.Root{{
				ID:     "root_embedded_activity",
				Path:   "/workspace/embedded-activity",
				Kind:   "git",
				Active: true,
			}},
		}); err != nil {
			return err
		}
		if _, err := service.CreateRole(t.Context(), cairnline.Role{
			ID:        "role_embedded_activity",
			ProjectID: projectID,
			Name:      "Activity Reviewer",
		}); err != nil {
			return err
		}
		if _, err := service.CreateWorkItem(t.Context(), cairnline.WorkItem{
			ID:          "work_embedded_activity",
			ProjectID:   projectID,
			Title:       "Review embedded activity",
			Brief:       "Exercise embedded Cairnline activity projection.",
			Status:      cairnline.WorkStatusReady,
			Priority:    cairnline.PriorityNormal,
			OwnerRoleID: "role_embedded_activity",
			RootID:      "root_embedded_activity",
		}); err != nil {
			return err
		}
		if _, err := service.CreateAssignment(t.Context(), cairnline.Assignment{
			ID:            "asgn_embedded_activity",
			ProjectID:     projectID,
			WorkItemID:    "work_embedded_activity",
			RoleID:        "role_embedded_activity",
			RootID:        "root_embedded_activity",
			ExecutionMode: cairnline.ExecutionMCPPull,
		}); err != nil {
			return err
		}
		if _, err := service.ClaimAssignment(t.Context(), projectID, "asgn_embedded_activity", "agent-activity"); err != nil {
			return err
		}
		if _, err := service.UpdateAssignmentStatus(t.Context(), projectID, "asgn_embedded_activity", cairnline.AssignmentRunning, "run_embedded_activity"); err != nil {
			return err
		}
		if _, err := service.CreateEvidence(t.Context(), cairnline.Evidence{
			ID:           "ev_embedded_activity",
			ProjectID:    projectID,
			WorkItemID:   "work_embedded_activity",
			AssignmentID: "asgn_embedded_activity",
			Title:        "Embedded activity evidence",
			Body:         "Activity can render from embedded Cairnline rows.",
			SourceKind:   "operator_note",
		}); err != nil {
			return err
		}
		_, err := service.CreateHandoff(t.Context(), cairnline.Handoff{
			ID:                 "handoff_embedded_activity",
			ProjectID:          projectID,
			WorkItemID:         "work_embedded_activity",
			SourceAssignmentID: "asgn_embedded_activity",
			FromRoleID:         "role_embedded_activity",
			ToRoleID:           "role_embedded_activity",
			Title:              "Activity follow-up",
			Body:               "Keep this handoff visible in activity.",
			Status:             cairnline.HandoffStatusOpen,
		})
		return err
	}); err != nil {
		t.Fatalf("seed embedded Cairnline activity: %v", err)
	}
	if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("Hecate project store seeded ok=%v err=%v, want no project row", ok, err)
	}
	requireCairnlineOnlyProjectReadsForTest(t, handler, projectID)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+projectID+"/activity", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("activity status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectActivityEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode activity: %v", err)
	}
	if response.Object != "project_activity" || response.Data.ProjectID != projectID || response.Data.ReadBackend != "cairnline" {
		t.Fatalf("activity response = %+v, want embedded Cairnline activity", response)
	}
	if response.Data.Summary.WorkItemCount != 1 || response.Data.Summary.AssignmentCount != 1 || response.Data.Summary.ActiveCount != 1 || response.Data.Summary.RecentCount != 1 {
		t.Fatalf("activity summary = %+v, want one active embedded assignment", response.Data.Summary)
	}
	item := findProjectActivityItemForTest(t, response.Data.Buckets.Active, "asgn_embedded_activity")
	if item.BlockingSignal != "running" || item.WorkItem.ID != "work_embedded_activity" || item.Role.ID != "role_embedded_activity" {
		t.Fatalf("activity item = %+v, want running embedded assignment with work and role enrichment", item)
	}
	if item.ArtifactSummary.Count != 1 || item.HandoffSummary.Count != 1 {
		t.Fatalf("activity item = %+v, want embedded artifact and handoff enrichment", item)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_missing/activity", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing project activity status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
}

func TestProjectWorkAPI_ProjectActivityUsesCairnlineSidecarWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineSidecarReadTestServer(t, "collaboration-fixture+strict-projects")
	if handler.projectReadRoutesUseCairnlineReadModel() {
		t.Fatal("sidecar activity enabled embedded Cairnline read-model routes")
	}
	if !handler.projectCairnlineSidecarReadRoutesEnabled() {
		t.Fatal("sidecar read-route predicate = false, want true")
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/activity", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("activity status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectActivityEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode activity: %v", err)
	}
	if response.Object != "project_activity" || response.Data.ProjectID != "proj_fixture" || response.Data.ReadBackend != "cairnline" {
		t.Fatalf("activity response = %+v, want sidecar Cairnline activity", response)
	}
	if response.Data.Summary.WorkItemCount != 1 || response.Data.Summary.AssignmentCount != 1 || response.Data.Summary.BlockedCount != 1 {
		t.Fatalf("activity summary = %+v, want one blocked sidecar assignment", response.Data.Summary)
	}
	item := findProjectActivityItemForTest(t, response.Data.Buckets.Blocked, "asg_fixture")
	if item.BlockingSignal != "not_started" || item.WorkItem.ID != "work_fixture" || item.Role.ID != "role_fixture" {
		t.Fatalf("activity item = %+v, want queued sidecar assignment with work and role enrichment", item)
	}
	if item.ArtifactSummary.Count == 0 || item.HandoffSummary.Count == 0 {
		t.Fatalf("activity item = %+v, want sidecar artifact and handoff enrichment", item)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/missing/activity", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing project activity status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
}

func TestProjectWorkAPI_ProjectActivityCairnlineSidecarReadRequiresStructuredContent(t *testing.T) {
	t.Parallel()
	_, server := newProjectsCairnlineSidecarReadTestServer(t, "projects.activity-text-only")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/activity", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("activity status = %d body=%s, want 502", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "structuredContent") {
		t.Fatalf("error body = %s, want structuredContent diagnostic", rec.Body.String())
	}
}

func TestProjectWorkAPI_ProjectActivityCairnlineMatchesHecate(t *testing.T) {
	t.Parallel()
	hecateHandler, hecateServer := newProjectWorkProjectionTestServer(t, "memory")
	seedProjectWorkProjectionTest(t, hecateHandler)
	hecateHandler.agentChat = failingChatGetStore{Store: hecateHandler.agentChat, failingID: "chat_external_error"}
	cairnlineHandler, cairnlineServer := newProjectWorkCairnlineReadTestServer()
	seedProjectWorkProjectionTest(t, cairnlineHandler)
	cairnlineHandler.agentChat = failingChatGetStore{Store: cairnlineHandler.agentChat, failingID: "chat_external_error"}

	hecate := mustRequestJSON[ProjectActivityEnvelope](newAPITestClient(t, hecateServer), http.MethodGet, "/hecate/v1/projects/proj_projection/activity", "")
	cairnline := mustRequestJSON[ProjectActivityEnvelope](newAPITestClient(t, cairnlineServer), http.MethodGet, "/hecate/v1/projects/proj_projection/activity", "")
	if hecate.Data.ReadBackend != "hecate" {
		t.Fatalf("Hecate activity read_backend = %q, want hecate", hecate.Data.ReadBackend)
	}
	if cairnline.Data.ReadBackend != "cairnline" {
		t.Fatalf("Cairnline activity read_backend = %q, want cairnline", cairnline.Data.ReadBackend)
	}

	hecateData := normalizeProjectActivityForParity(hecate.Data)
	cairnlineData := normalizeProjectActivityForParity(cairnline.Data)
	if !reflect.DeepEqual(hecateData, cairnlineData) {
		t.Fatalf("activity mismatch: %s", projectActivityParityMismatch(hecateData, cairnlineData))
	}
}

func normalizeProjectActivityForParity(item ProjectActivityDataResponse) ProjectActivityDataResponse {
	item.ReadBackend = ""
	normalizeProjectActivityItemsForParity(item.Recent)
	normalizeProjectActivityItemsForParity(item.Buckets.Active)
	normalizeProjectActivityItemsForParity(item.Buckets.Blocked)
	normalizeProjectActivityItemsForParity(item.Buckets.Completed)
	normalizeProjectActivityItemsForParity(item.Buckets.Recent)
	return item
}

func normalizeProjectActivityItemsForParity(items []ProjectActivityItemResponse) {
	for idx := range items {
		items[idx].UpdatedAt = ""
		items[idx].Role.ReadBackend = ""
		items[idx].Role.CreatedAt = ""
		items[idx].Role.UpdatedAt = ""
		normalizeProjectWorkAssignmentResponseForParity(&items[idx].Assignment)
		for artifactIdx := range items[idx].RecentArtifacts {
			items[idx].RecentArtifacts[artifactIdx].ReadBackend = ""
			items[idx].RecentArtifacts[artifactIdx].CreatedAt = ""
			items[idx].RecentArtifacts[artifactIdx].UpdatedAt = ""
		}
		for handoffIdx := range items[idx].RecentHandoffs {
			items[idx].RecentHandoffs[handoffIdx].ReadBackend = ""
			items[idx].RecentHandoffs[handoffIdx].CreatedAt = ""
			items[idx].RecentHandoffs[handoffIdx].UpdatedAt = ""
			items[idx].RecentHandoffs[handoffIdx].StatusChangedAt = ""
		}
	}
}

func normalizeProjectWorkAssignmentResponseForParity(item *ProjectWorkAssignmentResponse) {
	item.ReadBackend = ""
	item.CreatedAt = ""
	item.UpdatedAt = ""
	item.StartedAt = ""
	item.CompletedAt = ""
	if item.Execution != nil {
		item.Execution.StartedAt = ""
		item.Execution.FinishedAt = ""
	}
}

func projectActivityParityMismatch(left, right ProjectActivityDataResponse) string {
	if left.ProjectID != right.ProjectID {
		return fmt.Sprintf("project_id %q != %q", left.ProjectID, right.ProjectID)
	}
	if !reflect.DeepEqual(left.Summary, right.Summary) {
		return fmt.Sprintf("summary %+v != %+v", left.Summary, right.Summary)
	}
	if message := projectActivityItemsParityMismatch("recent", left.Recent, right.Recent); message != "" {
		return message
	}
	if message := projectActivityItemsParityMismatch("active", left.Buckets.Active, right.Buckets.Active); message != "" {
		return message
	}
	if message := projectActivityItemsParityMismatch("blocked", left.Buckets.Blocked, right.Buckets.Blocked); message != "" {
		return message
	}
	if message := projectActivityItemsParityMismatch("completed", left.Buckets.Completed, right.Buckets.Completed); message != "" {
		return message
	}
	if message := projectActivityItemsParityMismatch("bucket_recent", left.Buckets.Recent, right.Buckets.Recent); message != "" {
		return message
	}
	return fmt.Sprintf("\nHecate:   %+v\nCairnline: %+v", left, right)
}

func projectActivityItemsParityMismatch(name string, left, right []ProjectActivityItemResponse) string {
	if !reflect.DeepEqual(projectActivityIDsForParity(left), projectActivityIDsForParity(right)) {
		return fmt.Sprintf("%s IDs %v != %v", name, projectActivityIDsForParity(left), projectActivityIDsForParity(right))
	}
	for idx := range left {
		if reflect.DeepEqual(left[idx], right[idx]) {
			continue
		}
		leftJSON, _ := json.Marshal(left[idx])
		rightJSON, _ := json.Marshal(right[idx])
		return fmt.Sprintf("%s item %q differs\nHecate:   %s\nCairnline: %s", name, left[idx].ID, leftJSON, rightJSON)
	}
	return ""
}

func projectActivityIDsForParity(items []ProjectActivityItemResponse) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.ID)
	}
	return out
}

func TestProjectWorkAPI_ProjectWorkItemReadsCairnlineConfiguredUseReadModel(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{CoordinationBackend: "cairnline"},
	}, quietLogger(), nil, nil, nil, nil)
	server := NewServer(quietLogger(), handler)
	seedProjectWorkProjectionTest(t, handler)
	if _, err := handler.projectWork.CreateWorkItem(t.Context(), projectwork.WorkItem{
		ID:        "work_needs_evidence",
		ProjectID: "proj_projection",
		Title:     "Needs evidence",
		Status:    projectwork.WorkItemStatusReview,
	}); err != nil {
		t.Fatalf("CreateWorkItem(work_needs_evidence): %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(t.Context(), projectwork.Assignment{
		ID:         "asgn_needs_evidence",
		ProjectID:  "proj_projection",
		WorkItemID: "work_needs_evidence",
		RoleID:     "role_projection",
		Status:     projectwork.AssignmentStatusCompleted,
	}); err != nil {
		t.Fatalf("CreateAssignment(asgn_needs_evidence): %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_projection/roles", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list roles status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var roles ProjectWorkRolesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &roles); err != nil {
		t.Fatalf("decode roles: %v", err)
	}
	projectionRole := findProjectWorkRoleForTest(t, roles.Data, "role_projection")
	if projectionRole.ReadBackend != "cairnline" || projectionRole.DefaultProvider != "openai" || projectionRole.DefaultModel != "gpt-5" || projectionRole.DefaultAgentProfile != "implementation" {
		t.Fatalf("projection role = %+v, want Cairnline role read with Hecate execution defaults", projectionRole)
	}
	builtInRole := findProjectWorkRoleForTest(t, roles.Data, "product_manager")
	if builtInRole.ReadBackend != "cairnline" || !builtInRole.BuiltIn {
		t.Fatalf("built-in role = %+v, want Cairnline role read preserving built-in flag", builtInRole)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_projection/work-items", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list work items status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var listed ProjectWorkItemsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode work items: %v", err)
	}
	awaitingListItem := findProjectWorkItemForTest(t, listed.Data, "work_awaiting")
	if awaitingListItem.ReadBackend != "cairnline" {
		t.Fatalf("listed work item read_backend = %q, want cairnline", awaitingListItem.ReadBackend)
	}
	if awaitingListItem.Status != projectwork.WorkItemStatusRunning || len(awaitingListItem.Assignments) != 1 || awaitingListItem.Assignments[0].ExecutionRef == nil || awaitingListItem.Assignments[0].ReadBackend != "cairnline" {
		t.Fatalf("listed work item = %+v, want Hecate runtime projection over Cairnline work item", awaitingListItem)
	}
	evidenceListItem := findProjectWorkItemForTest(t, listed.Data, "work_needs_evidence")
	if evidenceListItem.ReadBackend != "cairnline" || evidenceListItem.Status != projectwork.WorkItemStatusDone {
		t.Fatalf("listed evidence work item = %+v, want Cairnline item with completed assignment projection", evidenceListItem)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_projection/work-items/work_awaiting", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("get work item status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var detail ProjectWorkItemEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode work item detail: %v", err)
	}
	if detail.Data.ReadBackend != "cairnline" || detail.Data.Status != projectwork.WorkItemStatusRunning || len(detail.Data.Assignments) != 1 || detail.Data.Assignments[0].ReadBackend != "cairnline" {
		t.Fatalf("work item detail = %+v, want Cairnline read backend with projected assignment", detail.Data)
	}
	if detail.Data.Assignments[0].ExecutionRef == nil || detail.Data.Assignments[0].ExecutionRef.PendingApprovalCount != 1 {
		t.Fatalf("detail assignment = %+v, want Hecate approval projection", detail.Data.Assignments[0])
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_projection/work-items/work_awaiting/assignments", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list assignments status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var assignments ProjectWorkAssignmentsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &assignments); err != nil {
		t.Fatalf("decode assignments: %v", err)
	}
	if len(assignments.Data) != 1 || assignments.Data[0].ID != "asgn_awaiting" || assignments.Data[0].ReadBackend != "cairnline" {
		t.Fatalf("assignments = %+v, want Cairnline-backed assignment list", assignments.Data)
	}
	if assignments.Data[0].Status != projectwork.AssignmentStatusAwaitingApproval || assignments.Data[0].ExecutionRef == nil || assignments.Data[0].ExecutionRef.PendingApprovalCount != 1 {
		t.Fatalf("assignment projection = %+v, want Hecate approval projection over Cairnline assignment list", assignments.Data[0])
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_projection/work-items/work_not_started/assignments/asgn_not_started/context", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("assignment context status = %d body=%s, want Cairnline read-model packet", rec.Code, rec.Body.String())
	}
	var packetResp ChatContextPacketResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &packetResp); err != nil {
		t.Fatalf("decode assignment context: %v", err)
	}
	if packetResp.Data.ID != "cairnline_assignment_context_asgn_not_started" || packetResp.Data.ExecutionMode != "orchestrated" {
		t.Fatalf("assignment context id/mode = %q/%q, want deterministic Cairnline orchestrated packet", packetResp.Data.ID, packetResp.Data.ExecutionMode)
	}
	if packetResp.Data.Refs == nil || packetResp.Data.Refs.ProjectID != "proj_projection" || packetResp.Data.Refs.WorkItemID != "work_not_started" || packetResp.Data.Refs.AssignmentID != "asgn_not_started" || packetResp.Data.Refs.RoleID != "role_projection" {
		t.Fatalf("assignment context refs = %+v, want project/work/assignment/role refs", packetResp.Data.Refs)
	}
	if item := findRenderedContextItemByOrigin(packetResp.Data, "cairnline.assignment_launch_packet"); item == nil || item.Section != contextSectionRuntime || item.Included || item.Metadata["read_backend"] != "cairnline" {
		t.Fatalf("Cairnline runtime item = %+v, want inspect-only runtime preview metadata", item)
	}
	if item := findRenderedContextItemByOrigin(packetResp.Data, "asgn_not_started"); item == nil || item.Section != contextSectionProjectWork || !item.Included {
		t.Fatalf("Cairnline assignment item = %+v, want included project_work assignment metadata", item)
	}
	if item := findRenderedContextItemByOrigin(packetResp.Data, "art_work_not_started"); item == nil || item.Section != contextSectionProjectWork || item.Included {
		t.Fatalf("Cairnline artifact item = %+v, want inspect-only work-item artifact metadata", item)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_projection/work-items/work_needs_evidence/readiness", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("readiness status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var readiness ProjectWorkItemReadinessEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &readiness); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	if readiness.Data.ReadBackend != "cairnline" || readiness.Data.Ready || readiness.Data.Status != "blocked" {
		t.Fatalf("readiness = %+v, want blocked Cairnline readiness", readiness.Data)
	}
	if len(readiness.Data.MissingEvidenceAssignmentIDs) != 1 || readiness.Data.MissingEvidenceAssignmentIDs[0] != "asgn_needs_evidence" {
		t.Fatalf("missing evidence = %+v, want asgn_needs_evidence", readiness.Data.MissingEvidenceAssignmentIDs)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_projection/work-items/work_missing_from_cairnline", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing work item status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
}

func TestProjectWorkAPI_ProjectWorkItemReadinessCairnlineMatchesHecate(t *testing.T) {
	t.Parallel()
	hecateHandler, hecateServer := newProjectWorkProjectionTestServer(t, "memory")
	seedProjectWorkProjectionTest(t, hecateHandler)
	cairnlineHandler, cairnlineServer := newProjectWorkCairnlineReadTestServer()
	seedProjectWorkProjectionTest(t, cairnlineHandler)

	hecateClient := newAPITestClient(t, hecateServer)
	cairnlineClient := newAPITestClient(t, cairnlineServer)
	for _, workID := range []string{
		"work_running",
		"work_queued",
		"work_awaiting",
		"work_completed",
		"work_failed",
		"work_cancelled",
		"work_missing",
		"work_run_only",
		"work_manual_terminal",
		"work_external_chat",
		"work_mixed",
		"work_not_started",
	} {
		workID := workID
		t.Run(workID, func(t *testing.T) {
			hecate := mustRequestJSON[ProjectWorkItemReadinessEnvelope](hecateClient, http.MethodGet, "/hecate/v1/projects/proj_projection/work-items/"+workID+"/readiness", "")
			cairnline := mustRequestJSON[ProjectWorkItemReadinessEnvelope](cairnlineClient, http.MethodGet, "/hecate/v1/projects/proj_projection/work-items/"+workID+"/readiness", "")
			if hecate.Data.ReadBackend != "hecate" {
				t.Fatalf("Hecate readiness read_backend = %q, want hecate", hecate.Data.ReadBackend)
			}
			if cairnline.Data.ReadBackend != "cairnline" {
				t.Fatalf("Cairnline readiness read_backend = %q, want cairnline", cairnline.Data.ReadBackend)
			}

			hecateData := normalizeProjectWorkItemReadinessForParity(hecate.Data)
			cairnlineData := normalizeProjectWorkItemReadinessForParity(cairnline.Data)
			if !reflect.DeepEqual(hecateData, cairnlineData) {
				t.Fatalf("readiness mismatch for %s\nHecate:   %+v\nCairnline: %+v", workID, hecateData, cairnlineData)
			}
		})
	}
}

func normalizeProjectWorkItemReadinessForParity(item ProjectWorkItemReadinessResponse) ProjectWorkItemReadinessResponse {
	item.ReadBackend = ""
	if len(item.Blockers) == 0 {
		item.Blockers = nil
	}
	if len(item.Warnings) == 0 {
		item.Warnings = nil
	}
	if len(item.ReviewFollowUpArtifactIDs) == 0 {
		item.ReviewFollowUpArtifactIDs = nil
	}
	if len(item.ReviewFollowUps) == 0 {
		item.ReviewFollowUps = nil
	}
	if len(item.MissingEvidenceAssignmentIDs) == 0 {
		item.MissingEvidenceAssignmentIDs = nil
	}
	return item
}

func TestProjectWorkAPI_ProjectArtifactsAndHandoffsCairnlineConfiguredUseReadModel(t *testing.T) {
	t.Parallel()
	nativeHandler, nativeServer := newProjectWorkTestServer()
	seedProjectWorkProjectionTest(t, nativeHandler)
	seedProjectWorkArtifactsAndHandoffsReadModelTest(t, nativeHandler)
	cairnlineHandler, cairnlineServer := newProjectWorkCairnlineReadTestServer()
	seedProjectWorkProjectionTest(t, cairnlineHandler)
	seedProjectWorkArtifactsAndHandoffsReadModelTest(t, cairnlineHandler)

	nativeArtifacts := mustRequestJSON[ProjectWorkArtifactsResponse](newAPITestClient(t, nativeServer), http.MethodGet, "/hecate/v1/projects/proj_projection/work-items/work_completed/artifacts", "")
	cairnlineArtifacts := mustRequestJSON[ProjectWorkArtifactsResponse](newAPITestClient(t, cairnlineServer), http.MethodGet, "/hecate/v1/projects/proj_projection/work-items/work_completed/artifacts", "")
	assertProjectWorkArtifactsParity(t, nativeArtifacts.Data, cairnlineArtifacts.Data)

	decision := findProjectWorkArtifactForTest(t, cairnlineArtifacts.Data, "art_completion_decision")
	if decision.ReadBackend != "cairnline" || decision.Kind != projectwork.ArtifactKindDecisionNote || decision.AuthorRoleID != "role_projection" || decision.Body != "Keep the completed assignment as the closeout record." {
		t.Fatalf("decision artifact = %+v, want Cairnline-backed generic artifact", decision)
	}
	handoffArtifact := findProjectWorkArtifactForTest(t, cairnlineArtifacts.Data, "art_asgn_completed")
	if handoffArtifact.ReadBackend != "cairnline" || handoffArtifact.Kind != projectwork.ArtifactKindHandoff || handoffArtifact.Body != "Ready for review." {
		t.Fatalf("handoff artifact = %+v, want Cairnline-backed generic handoff artifact", handoffArtifact)
	}
	evidence := findProjectWorkArtifactForTest(t, cairnlineArtifacts.Data, "art_completion_evidence")
	if evidence.ReadBackend != "cairnline" || evidence.Kind != projectwork.ArtifactKindEvidenceLink || evidence.EvidenceURL != "https://example.invalid/evidence/completion" || evidence.EvidenceTrustLabel != projectwork.EvidenceTrustOperatorProvided {
		t.Fatalf("evidence artifact = %+v, want Cairnline-backed evidence metadata", evidence)
	}
	review := findProjectWorkArtifactForTest(t, cairnlineArtifacts.Data, "art_completion_review")
	if review.ReadBackend != "cairnline" || review.Kind != projectwork.ArtifactKindReview || review.ReviewedAssignmentID != "asgn_completed" || review.ReviewVerdict != projectwork.ReviewVerdictApproved || review.ReviewRisk != projectwork.ReviewRiskLow || review.ReviewFollowUpRequired {
		t.Fatalf("review artifact = %+v, want Cairnline-backed review metadata", review)
	}

	nativeWorkHandoffs := mustRequestJSON[ProjectHandoffsResponse](newAPITestClient(t, nativeServer), http.MethodGet, "/hecate/v1/projects/proj_projection/work-items/work_completed/handoffs", "")
	cairnlineWorkHandoffs := mustRequestJSON[ProjectHandoffsResponse](newAPITestClient(t, cairnlineServer), http.MethodGet, "/hecate/v1/projects/proj_projection/work-items/work_completed/handoffs", "")
	assertProjectHandoffsParity(t, nativeWorkHandoffs.Data, cairnlineWorkHandoffs.Data)
	handoff := findProjectHandoffForTest(t, cairnlineWorkHandoffs.Data, "handoff_review_followup")
	if handoff.ReadBackend != "cairnline" || handoff.TargetAssignmentID != "asgn_queued" || handoff.TargetWorkItemID != "work_queued" || handoff.Status != projectwork.HandoffStatusPending {
		t.Fatalf("work-item handoff = %+v, want Cairnline-backed handoff metadata", handoff)
	}

	nativeProjectHandoffs := mustRequestJSON[ProjectHandoffsResponse](newAPITestClient(t, nativeServer), http.MethodGet, "/hecate/v1/projects/proj_projection/handoffs?work_item_id=work_completed&status=pending", "")
	cairnlineProjectHandoffs := mustRequestJSON[ProjectHandoffsResponse](newAPITestClient(t, cairnlineServer), http.MethodGet, "/hecate/v1/projects/proj_projection/handoffs?work_item_id=work_completed&status=pending", "")
	assertProjectHandoffsParity(t, nativeProjectHandoffs.Data, cairnlineProjectHandoffs.Data)
	if len(cairnlineProjectHandoffs.Data) != 1 || cairnlineProjectHandoffs.Data[0].ID != "handoff_review_followup" || cairnlineProjectHandoffs.Data[0].ReadBackend != "cairnline" {
		t.Fatalf("project handoffs = %+v, want filtered Cairnline-backed handoff", cairnlineProjectHandoffs.Data)
	}
}

func seedProjectWorkArtifactsAndHandoffsReadModelTest(t *testing.T, handler *Handler) {
	t.Helper()
	if _, err := handler.projectWork.CreateArtifact(t.Context(), projectwork.CollaborationArtifact{
		ID:           "art_completion_decision",
		ProjectID:    "proj_projection",
		WorkItemID:   "work_completed",
		AssignmentID: "asgn_completed",
		Kind:         projectwork.ArtifactKindDecisionNote,
		Title:        "Completion decision",
		Body:         "Keep the completed assignment as the closeout record.",
		AuthorRoleID: "role_projection",
		CreatedAt:    time.Date(2026, 6, 3, 12, 3, 30, 0, time.UTC),
		UpdatedAt:    time.Date(2026, 6, 3, 12, 3, 30, 0, time.UTC),
	}); err != nil {
		t.Fatalf("CreateArtifact(decision): %v", err)
	}
	if _, err := handler.projectWork.CreateArtifact(t.Context(), projectwork.CollaborationArtifact{
		ID:                 "art_completion_evidence",
		ProjectID:          "proj_projection",
		WorkItemID:         "work_completed",
		AssignmentID:       "asgn_completed",
		Kind:               projectwork.ArtifactKindEvidenceLink,
		Title:              "Completion evidence",
		Body:               "Focused test output was captured.",
		EvidenceURL:        "https://example.invalid/evidence/completion",
		EvidenceTrustLabel: projectwork.EvidenceTrustOperatorProvided,
		CreatedAt:          time.Date(2026, 6, 3, 12, 4, 30, 0, time.UTC),
		UpdatedAt:          time.Date(2026, 6, 3, 12, 4, 30, 0, time.UTC),
	}); err != nil {
		t.Fatalf("CreateArtifact(evidence): %v", err)
	}
	if _, err := handler.projectWork.CreateArtifact(t.Context(), projectwork.CollaborationArtifact{
		ID:                     "art_completion_review",
		ProjectID:              "proj_projection",
		WorkItemID:             "work_completed",
		AssignmentID:           "asgn_completed",
		ReviewedAssignmentID:   "asgn_completed",
		Kind:                   projectwork.ArtifactKindReview,
		Title:                  "Completion review",
		Body:                   "Verdict: approved.",
		AuthorRoleID:           "role_projection",
		ReviewVerdict:          projectwork.ReviewVerdictApproved,
		ReviewRisk:             projectwork.ReviewRiskLow,
		ReviewFollowUpRequired: false,
		CreatedAt:              time.Date(2026, 6, 3, 12, 5, 30, 0, time.UTC),
		UpdatedAt:              time.Date(2026, 6, 3, 12, 5, 30, 0, time.UTC),
	}); err != nil {
		t.Fatalf("CreateArtifact(review): %v", err)
	}
}

func assertProjectWorkArtifactsParity(t *testing.T, hecate, cairnline []ProjectWorkArtifactResponse) {
	t.Helper()
	normalizedHecate := normalizeProjectWorkArtifactsForParity(t, hecate, "hecate")
	normalizedCairnline := normalizeProjectWorkArtifactsForParity(t, cairnline, "cairnline")
	if !reflect.DeepEqual(normalizedHecate, normalizedCairnline) {
		t.Fatalf("project work artifacts mismatch\nHecate:   %+v\nCairnline: %+v", normalizedHecate, normalizedCairnline)
	}
}

func normalizeProjectWorkArtifactsForParity(t *testing.T, items []ProjectWorkArtifactResponse, backend string) []ProjectWorkArtifactResponse {
	t.Helper()
	out := append([]ProjectWorkArtifactResponse(nil), items...)
	for index := range out {
		if out[index].ReadBackend != backend {
			t.Fatalf("%s artifact[%d] read_backend = %q, want %s", backend, index, out[index].ReadBackend, backend)
		}
		out[index].ReadBackend = ""
	}
	return out
}

func assertProjectHandoffsParity(t *testing.T, hecate, cairnline []ProjectHandoffResponse) {
	t.Helper()
	normalizedHecate := normalizeProjectHandoffsForParity(t, hecate, "hecate")
	normalizedCairnline := normalizeProjectHandoffsForParity(t, cairnline, "cairnline")
	if !reflect.DeepEqual(normalizedHecate, normalizedCairnline) {
		t.Fatalf("project handoffs mismatch\nHecate:   %+v\nCairnline: %+v", normalizedHecate, normalizedCairnline)
	}
}

func normalizeProjectHandoffsForParity(t *testing.T, items []ProjectHandoffResponse, backend string) []ProjectHandoffResponse {
	t.Helper()
	out := append([]ProjectHandoffResponse(nil), items...)
	for index := range out {
		if out[index].ReadBackend != backend {
			t.Fatalf("%s handoff[%d] read_backend = %q, want %s", backend, index, out[index].ReadBackend, backend)
		}
		out[index].ReadBackend = ""
	}
	return out
}

func TestProjectWorkAPI_ProjectActivityShowsFreshQueuedAssignments(t *testing.T) {
	t.Parallel()
	_, server := newProjectWorkTestServer()
	project := createProjectForWorkTest(t, server)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/work-items", bytes.NewReader([]byte(`{
		"id":"work_dogfood",
		"title":"Dogfood Projects loop",
		"brief":"Confirm queued work appears in project activity.",
		"status":"ready",
		"priority":"normal",
		"owner_role_id":"developer"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create work item status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_dogfood/assignments", bytes.NewReader([]byte(`{
		"id":"asgn_dogfood",
		"role_id":"developer",
		"driver_kind":"hecate_task",
		"status":"queued"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create assignment status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+project.Data.ID+"/activity", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("activity status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectActivityEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode activity: %v", err)
	}

	if response.Data.Summary.AssignmentCount != 1 || response.Data.Summary.BlockedCount != 1 || response.Data.Summary.RecentCount != 1 {
		t.Fatalf("activity summary = %+v, want queued assignment counted as blocked/recent", response.Data.Summary)
	}
	item := findProjectActivityItemForTest(t, response.Data.Buckets.Blocked, "asgn_dogfood")
	if item.BlockingSignal != "not_started" || item.StatusSummary != "not started" || item.LinkedTaskID != "" || item.LinkedRunID != "" {
		t.Fatalf("queued activity = %+v, want not-started without runtime links", item)
	}
	recent := findProjectActivityItemForTest(t, response.Data.Recent, "asgn_dogfood")
	if recent.ID != item.ID || len(response.Data.Buckets.Recent) != len(response.Data.Recent) {
		t.Fatalf("recent activity = %+v buckets=%+v, want recent assignment mirrored", response.Data.Recent, response.Data.Buckets.Recent)
	}
}

func TestProjectWorkAPI_ProjectActivityUsesEmbeddedCairnlineWorkGraph(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkCairnlineMirrorTestServer(t)
	project := createProjectForWorkTest(t, server)

	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	defer store.Close()
	if _, err := service.CreateRole(t.Context(), cairnline.Role{
		ID:                   "role_mirror_only",
		ProjectID:            project.Data.ID,
		Name:                 "Mirror-only role",
		DefaultExecutionMode: cairnline.ExecutionOrchestrated,
	}); err != nil {
		t.Fatalf("CreateRole(role_mirror_only): %v", err)
	}
	if _, err := service.CreateWorkItem(t.Context(), cairnline.WorkItem{
		ID:        "work_mirror_only",
		ProjectID: project.Data.ID,
		Title:     "Mirror-only work",
		Status:    cairnline.WorkStatusReady,
		Priority:  cairnline.PriorityNormal,
	}); err != nil {
		t.Fatalf("CreateWorkItem(work_mirror_only): %v", err)
	}
	if _, err := service.CreateAssignment(t.Context(), cairnline.Assignment{
		ID:            "asgn_mirror_only",
		ProjectID:     project.Data.ID,
		WorkItemID:    "work_mirror_only",
		RoleID:        "role_mirror_only",
		ExecutionMode: cairnline.ExecutionOrchestrated,
		Status:        cairnline.AssignmentQueued,
	}); err != nil {
		t.Fatalf("CreateAssignment(asgn_mirror_only): %v", err)
	}

	nativeWorkItems, err := handler.projectWork.ListWorkItems(t.Context(), project.Data.ID)
	if err != nil {
		t.Fatalf("ListWorkItems(native): %v", err)
	}
	if len(nativeWorkItems) != 0 {
		t.Fatalf("native work items = %+v, want mirror-only graph to stay out of Hecate store", nativeWorkItems)
	}

	client := newAPITestClient(t, server)
	listed := mustRequestJSON[ProjectWorkItemsResponse](client, http.MethodGet, "/hecate/v1/projects/"+project.Data.ID+"/work-items", "")
	listedItem := findProjectWorkItemForTest(t, listed.Data, "work_mirror_only")
	if listedItem.ReadBackend != "cairnline" || len(listedItem.Assignments) != 1 || listedItem.Assignments[0].ID != "asgn_mirror_only" {
		t.Fatalf("listed work item = %+v, want Cairnline service assignment projection", listedItem)
	}
	if listedItem.Assignments[0].DriverKind != projectwork.AssignmentDriverHecateTask || listedItem.Assignments[0].ReadBackend != "cairnline" {
		t.Fatalf("listed assignment = %+v, want projected Hecate task assignment from Cairnline service row", listedItem.Assignments[0])
	}

	detail := mustRequestJSON[ProjectWorkItemEnvelope](client, http.MethodGet, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_mirror_only", "")
	if detail.Data.ReadBackend != "cairnline" || len(detail.Data.Assignments) != 1 || detail.Data.Assignments[0].ID != "asgn_mirror_only" {
		t.Fatalf("detail work item = %+v, want Cairnline service assignment projection", detail.Data)
	}

	assignments := mustRequestJSON[ProjectWorkAssignmentsResponse](client, http.MethodGet, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_mirror_only/assignments", "")
	if len(assignments.Data) != 1 || assignments.Data[0].ID != "asgn_mirror_only" || assignments.Data[0].ReadBackend != "cairnline" {
		t.Fatalf("assignment list = %+v, want mirror-only Cairnline assignment", assignments.Data)
	}

	response := mustRequestJSON[ProjectActivityEnvelope](client, http.MethodGet, "/hecate/v1/projects/"+project.Data.ID+"/activity", "")
	if response.Data.ReadBackend != "cairnline" {
		t.Fatalf("activity read_backend = %q, want cairnline", response.Data.ReadBackend)
	}
	if response.Data.Summary.WorkItemCount != 1 || response.Data.Summary.AssignmentCount != 1 || response.Data.Summary.BlockedCount != 1 {
		t.Fatalf("activity summary = %+v, want mirror-only work and queued assignment counted", response.Data.Summary)
	}
	item := findProjectActivityItemForTest(t, response.Data.Buckets.Blocked, "asgn_mirror_only")
	if item.WorkItem.ID != "work_mirror_only" || item.WorkItem.Title != "Mirror-only work" || item.Role.ID != "role_mirror_only" || item.Role.Name != "Mirror-only role" {
		t.Fatalf("activity item = %+v, want Cairnline service work/role projection", item)
	}
}

type failingCreateTaskStore struct {
	taskstate.Store
}

func (s failingCreateTaskStore) CreateTask(context.Context, types.Task) (types.Task, error) {
	return types.Task{}, errors.New("create task failed")
}

type failingAgentProfileStore struct {
	err error
}

func (s failingAgentProfileStore) Backend() string { return "failing" }

func (s failingAgentProfileStore) Create(context.Context, agentprofiles.Profile) (agentprofiles.Profile, error) {
	return agentprofiles.Profile{}, s.err
}

func (s failingAgentProfileStore) Get(context.Context, string) (agentprofiles.Profile, bool, error) {
	return agentprofiles.Profile{}, false, s.err
}

func (s failingAgentProfileStore) List(context.Context) ([]agentprofiles.Profile, error) {
	return nil, s.err
}

func (s failingAgentProfileStore) Update(context.Context, string, func(*agentprofiles.Profile)) (agentprofiles.Profile, error) {
	return agentprofiles.Profile{}, s.err
}

func (s failingAgentProfileStore) Delete(context.Context, string) error {
	return s.err
}

type failingChatGetStore struct {
	chat.Store
	failingID string
}

func (s failingChatGetStore) Get(ctx context.Context, id string) (chat.Session, bool, error) {
	if id == s.failingID {
		return chat.Session{}, false, errors.New("chat get failed")
	}
	return s.Store.Get(ctx, id)
}

type projectWorkAssignmentStartSeed struct {
	Workspace           string
	Driver              string
	Status              string
	TaskID              string
	RunID               string
	ProjectAgentProfile string
	RoleAgentProfile    string
	WithoutRoleDefaults bool
}

func seedProjectWorkAssignmentStartTest(t *testing.T, handler *Handler, seed projectWorkAssignmentStartSeed) {
	t.Helper()
	project := projects.Project{
		ID:                   "proj_start",
		Name:                 "Hecate",
		DefaultProvider:      "ollama",
		DefaultModel:         "qwen2.5-coder",
		DefaultWorkspaceMode: "in_place",
		DefaultSystemPrompt:  "Project default system prompt.",
	}
	if seed.ProjectAgentProfile != "" {
		project.DefaultAgentProfile = seed.ProjectAgentProfile
	}
	if seed.Workspace != "" {
		project.Roots = []projects.Root{{ID: "root_start", Path: seed.Workspace, Kind: "git", Active: true}}
		project.DefaultRootID = "root_start"
	}
	if _, err := handler.projects.Create(t.Context(), project); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	role := projectwork.AgentRoleProfile{
		ID:           "role_backend",
		ProjectID:    "proj_start",
		Name:         "Backend engineer",
		Instructions: "Follow backend invariants.",
	}
	if !seed.WithoutRoleDefaults {
		role.DefaultProvider = "anthropic"
		role.DefaultModel = "claude-sonnet-4"
		role.DefaultAgentProfile = strings.TrimSpace(seed.RoleAgentProfile)
		if role.DefaultAgentProfile == "" {
			role.DefaultAgentProfile = "implementation"
		}
	}
	if _, err := handler.projectWork.CreateRole(t.Context(), role); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if _, err := handler.projectWork.CreateWorkItem(t.Context(), projectwork.WorkItem{
		ID:        "work_start",
		ProjectID: "proj_start",
		Title:     "Native assignment start",
		Brief:     "Implement the native assignment start path.",
		Priority:  "high",
	}); err != nil {
		t.Fatalf("CreateWorkItem: %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(t.Context(), projectwork.Assignment{
		ID:         "asgn_start",
		ProjectID:  "proj_start",
		WorkItemID: "work_start",
		RoleID:     "role_backend",
		DriverKind: seed.Driver,
		Status:     seed.Status,
		ExecutionRef: projectwork.AssignmentExecutionRef{
			Kind:   projectwork.AssignmentExecutionRefKind(seed.TaskID, seed.RunID, "", "", ""),
			TaskID: seed.TaskID,
			RunID:  seed.RunID,
		},
	}); err != nil {
		t.Fatalf("CreateAssignment: %v", err)
	}
}

func taskstateFilterAll() taskstate.TaskFilter {
	return taskstate.TaskFilter{Limit: 0}
}

func newProjectWorkProjectionTestServer(t *testing.T, backend string) (*Handler, http.Handler) {
	t.Helper()
	if backend == "memory" {
		return newProjectWorkTestServer()
	}
	ctx := t.Context()
	client, err := storage.NewSQLiteClient(ctx, storage.SQLiteConfig{
		Path:        filepath.Join(t.TempDir(), "projection.db"),
		TablePrefix: "test",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	projectStore, err := projects.NewSQLiteStore(ctx, client)
	if err != nil {
		t.Fatalf("NewSQLiteStore(projects): %v", err)
	}
	projectWorkStore, err := projectwork.NewSQLiteStore(ctx, client)
	if err != nil {
		t.Fatalf("NewSQLiteStore(projectwork): %v", err)
	}
	taskStore, err := taskstate.NewSQLiteStore(ctx, client)
	if err != nil {
		t.Fatalf("NewSQLiteStore(taskstate): %v", err)
	}
	handler := NewHandler(config.Config{}, quietLogger(), nil, nil, taskStore, nil)
	t.Cleanup(func() { _ = handler.taskRunner.Shutdown(t.Context()) })
	handler.SetProjectStore(projectStore)
	handler.SetProjectWorkStore(projectWorkStore)
	return handler, NewServer(quietLogger(), handler)
}

func seedProjectWorkProjectionTest(t *testing.T, handler *Handler) {
	t.Helper()
	ctx := t.Context()
	if _, err := handler.projects.Create(ctx, projects.Project{ID: "proj_projection", Name: "Projection"}); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	if _, err := handler.projectWork.CreateRole(ctx, projectwork.AgentRoleProfile{
		ID:                  "role_projection",
		ProjectID:           "proj_projection",
		Name:                "Projection engineer",
		DefaultDriverKind:   projectwork.AssignmentDriverHecateTask,
		DefaultProvider:     "openai",
		DefaultModel:        "gpt-5",
		DefaultAgentProfile: "implementation",
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	base := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		workID            string
		assignmentID      string
		assignmentStatus  string
		runStatus         string
		runStartedAt      time.Time
		runFinishedAt     time.Time
		assignmentUpdated time.Time
		lastError         string
		approvalPending   bool
		missing           bool
	}{
		{workID: "work_running", assignmentID: "asgn_running", runStatus: "running", runStartedAt: base.Add(1 * time.Minute)},
		{workID: "work_queued", assignmentID: "asgn_queued", runStatus: "queued"},
		{workID: "work_awaiting", assignmentID: "asgn_awaiting", runStatus: "awaiting_approval", runStartedAt: base.Add(2 * time.Minute), approvalPending: true},
		{workID: "work_completed", assignmentID: "asgn_completed", runStatus: "completed", runStartedAt: base.Add(3 * time.Minute), runFinishedAt: base.Add(4 * time.Minute)},
		{workID: "work_failed", assignmentID: "asgn_failed", runStatus: "failed", runStartedAt: base.Add(5 * time.Minute), runFinishedAt: base.Add(6 * time.Minute), lastError: "model failed"},
		{workID: "work_cancelled", assignmentID: "asgn_cancelled", runStatus: "cancelled", runStartedAt: base.Add(7 * time.Minute), runFinishedAt: base.Add(8 * time.Minute)},
		{workID: "work_missing", assignmentID: "asgn_missing", runStatus: "queued", missing: true},
		{workID: "work_manual_terminal", assignmentID: "asgn_manual_terminal", assignmentStatus: projectwork.AssignmentStatusFailed, runStatus: "completed", runStartedAt: base.Add(9 * time.Minute), runFinishedAt: base.Add(10 * time.Minute), assignmentUpdated: base.Add(11 * time.Minute)},
	}
	for _, tc := range cases {
		seedProjectWorkProjectionCase(t, handler, tc.workID, tc.assignmentID, tc.assignmentStatus, tc.runStatus, tc.runStartedAt, tc.runFinishedAt, tc.assignmentUpdated, tc.lastError, tc.approvalPending, tc.missing)
	}
	seedProjectWorkRunOnlyProjectionCase(t, handler)
	seedProjectWorkNotStartedProjectionCase(t, handler)
	seedProjectWorkExternalChatProjectionCase(t, handler)
	seedProjectWorkProjectionCase(t, handler, "work_mixed", "asgn_mixed_completed", "", "completed", base.Add(12*time.Minute), base.Add(13*time.Minute), time.Time{}, "", false, false)
	seedProjectWorkProjectionCase(t, handler, "work_mixed", "asgn_mixed_failed", "", "failed", base.Add(14*time.Minute), base.Add(15*time.Minute), time.Time{}, "review failed", false, false)
}

func seedProjectWorkExternalChatProjectionCase(t *testing.T, handler *Handler) {
	t.Helper()
	ctx := t.Context()
	createdAt := time.Date(2026, 6, 3, 12, 16, 0, 0, time.UTC)
	if _, err := handler.projectWork.CreateWorkItem(ctx, projectwork.WorkItem{
		ID:        "work_external_chat",
		ProjectID: "proj_projection",
		Title:     "work_external_chat",
		Status:    projectwork.WorkItemStatusRunning,
		UpdatedAt: createdAt,
	}); err != nil {
		t.Fatalf("CreateWorkItem(work_external_chat): %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(ctx, projectwork.Assignment{
		ID:         "asgn_external_chat",
		ProjectID:  "proj_projection",
		WorkItemID: "work_external_chat",
		RoleID:     "role_projection",
		DriverKind: projectwork.AssignmentDriverExternalAgent,
		Status:     projectwork.AssignmentStatusRunning,
		ExecutionRef: projectwork.AssignmentExecutionRef{
			Kind:          projectwork.AssignmentExecutionKindChatSession,
			ChatSessionID: "chat_external_projection",
		},
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}); err != nil {
		t.Fatalf("CreateAssignment(asgn_external_chat): %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(ctx, projectwork.Assignment{
		ID:         "asgn_missing_chat",
		ProjectID:  "proj_projection",
		WorkItemID: "work_external_chat",
		RoleID:     "role_projection",
		DriverKind: projectwork.AssignmentDriverExternalAgent,
		Status:     projectwork.AssignmentStatusRunning,
		ExecutionRef: projectwork.AssignmentExecutionRef{
			Kind:          projectwork.AssignmentExecutionKindChatSession,
			ChatSessionID: "chat_external_missing",
		},
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}); err != nil {
		t.Fatalf("CreateAssignment(asgn_missing_chat): %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(ctx, projectwork.Assignment{
		ID:         "asgn_failed_chat",
		ProjectID:  "proj_projection",
		WorkItemID: "work_external_chat",
		RoleID:     "role_projection",
		DriverKind: projectwork.AssignmentDriverExternalAgent,
		Status:     projectwork.AssignmentStatusRunning,
		ExecutionRef: projectwork.AssignmentExecutionRef{
			Kind:          projectwork.AssignmentExecutionKindChatSession,
			ChatSessionID: "chat_external_failed",
		},
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}); err != nil {
		t.Fatalf("CreateAssignment(asgn_failed_chat): %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(ctx, projectwork.Assignment{
		ID:         "asgn_prepared_chat",
		ProjectID:  "proj_projection",
		WorkItemID: "work_external_chat",
		RoleID:     "role_projection",
		DriverKind: projectwork.AssignmentDriverExternalAgent,
		Status:     projectwork.AssignmentStatusRunning,
		ExecutionRef: projectwork.AssignmentExecutionRef{
			Kind:          projectwork.AssignmentExecutionKindChatSession,
			ChatSessionID: "chat_external_prepared",
		},
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}); err != nil {
		t.Fatalf("CreateAssignment(asgn_prepared_chat): %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(ctx, projectwork.Assignment{
		ID:         "asgn_cross_project_chat",
		ProjectID:  "proj_projection",
		WorkItemID: "work_external_chat",
		RoleID:     "role_projection",
		DriverKind: projectwork.AssignmentDriverExternalAgent,
		Status:     projectwork.AssignmentStatusRunning,
		ExecutionRef: projectwork.AssignmentExecutionRef{
			Kind:          projectwork.AssignmentExecutionKindChatSession,
			ChatSessionID: "chat_external_other_project",
		},
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}); err != nil {
		t.Fatalf("CreateAssignment(asgn_cross_project_chat): %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(ctx, projectwork.Assignment{
		ID:         "asgn_error_chat",
		ProjectID:  "proj_projection",
		WorkItemID: "work_external_chat",
		RoleID:     "role_projection",
		DriverKind: projectwork.AssignmentDriverExternalAgent,
		Status:     projectwork.AssignmentStatusRunning,
		ExecutionRef: projectwork.AssignmentExecutionRef{
			Kind:          projectwork.AssignmentExecutionKindChatSession,
			ChatSessionID: "chat_external_error",
		},
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}); err != nil {
		t.Fatalf("CreateAssignment(asgn_error_chat): %v", err)
	}
	if _, err := handler.agentChat.Create(ctx, chat.Session{
		ID:              "chat_external_projection",
		Title:           "External projection",
		ProjectID:       "proj_projection",
		AgentID:         "codex",
		DriverKind:      agentadapters.DriverKindACP,
		NativeSessionID: "native_external_projection",
		Workspace:       t.TempDir(),
		Status:          "running",
		CreatedAt:       createdAt,
		UpdatedAt:       createdAt.Add(2 * time.Minute),
		Messages: []chat.Message{
			{ID: "msg_external_user", Role: "user", Content: "Continue", Status: "completed", CreatedAt: createdAt.Add(time.Minute)},
			{ID: "msg_external_done", Role: "assistant", Content: "Done", Status: "completed", CreatedAt: createdAt.Add(2 * time.Minute), CompletedAt: createdAt.Add(3 * time.Minute)},
		},
	}); err != nil {
		t.Fatalf("Create chat session: %v", err)
	}
	if _, err := handler.agentChat.Create(ctx, chat.Session{
		ID:              "chat_external_failed",
		Title:           "External failed projection",
		ProjectID:       "proj_projection",
		AgentID:         "codex",
		DriverKind:      agentadapters.DriverKindACP,
		NativeSessionID: "native_external_failed",
		Workspace:       t.TempDir(),
		Status:          "failed",
		CreatedAt:       createdAt,
		UpdatedAt:       createdAt.Add(4 * time.Minute),
		Messages: []chat.Message{
			{ID: "msg_external_failed", Role: "assistant", Content: "", Status: "failed", Error: "adapter auth failed", CreatedAt: createdAt.Add(4 * time.Minute)},
		},
	}); err != nil {
		t.Fatalf("Create failed chat session: %v", err)
	}
	if _, err := handler.agentChat.Create(ctx, chat.Session{
		ID:              "chat_external_prepared",
		Title:           "External prepared projection",
		ProjectID:       "proj_projection",
		AgentID:         "codex",
		DriverKind:      agentadapters.DriverKindACP,
		NativeSessionID: "native_external_prepared",
		Workspace:       t.TempDir(),
		CreatedAt:       createdAt,
	}); err != nil {
		t.Fatalf("Create prepared chat session: %v", err)
	}
	if _, err := handler.agentChat.Create(ctx, chat.Session{
		ID:              "chat_external_other_project",
		Title:           "Foreign external projection",
		ProjectID:       "proj_other",
		AgentID:         "codex",
		DriverKind:      agentadapters.DriverKindACP,
		NativeSessionID: "native_external_other_project",
		Workspace:       t.TempDir(),
		Status:          "completed",
		CreatedAt:       createdAt,
		UpdatedAt:       createdAt.Add(5 * time.Minute),
		Messages: []chat.Message{
			{ID: "msg_foreign_done", Role: "assistant", Content: "Other project", Status: "completed", CreatedAt: createdAt.Add(5 * time.Minute)},
		},
	}); err != nil {
		t.Fatalf("Create cross-project chat session: %v", err)
	}
}

func seedProjectWorkRunOnlyProjectionCase(t *testing.T, handler *Handler) {
	t.Helper()
	ctx := t.Context()
	createdAt := time.Date(2026, 6, 3, 11, 57, 0, 0, time.UTC)
	if _, err := handler.projectWork.CreateWorkItem(ctx, projectwork.WorkItem{
		ID:        "work_run_only",
		ProjectID: "proj_projection",
		Title:     "work_run_only",
		Status:    projectwork.WorkItemStatusReady,
	}); err != nil {
		t.Fatalf("CreateWorkItem(work_run_only): %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(ctx, projectwork.Assignment{
		ID:         "asgn_run_only",
		ProjectID:  "proj_projection",
		WorkItemID: "work_run_only",
		RoleID:     "role_projection",
		DriverKind: projectwork.AssignmentDriverHecateTask,
		Status:     projectwork.AssignmentStatusQueued,
		ExecutionRef: projectwork.AssignmentExecutionRef{
			Kind:  projectwork.AssignmentExecutionKindTaskRun,
			RunID: "run_without_task",
		},
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}); err != nil {
		t.Fatalf("CreateAssignment(asgn_run_only): %v", err)
	}
}

func seedProjectWorkNotStartedProjectionCase(t *testing.T, handler *Handler) {
	t.Helper()
	ctx := t.Context()
	if _, err := handler.projectWork.CreateWorkItem(ctx, projectwork.WorkItem{
		ID:        "work_not_started",
		ProjectID: "proj_projection",
		Title:     "work_not_started",
		Status:    projectwork.WorkItemStatusReady,
	}); err != nil {
		t.Fatalf("CreateWorkItem(work_not_started): %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(ctx, projectwork.Assignment{
		ID:         "asgn_not_started",
		ProjectID:  "proj_projection",
		WorkItemID: "work_not_started",
		RoleID:     "role_projection",
		DriverKind: projectwork.AssignmentDriverHecateTask,
		Status:     projectwork.AssignmentStatusQueued,
		CreatedAt:  time.Date(2026, 6, 3, 11, 58, 0, 0, time.UTC),
		UpdatedAt:  time.Date(2026, 6, 3, 11, 58, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("CreateAssignment(asgn_not_started): %v", err)
	}
	if _, err := handler.projectWork.CreateArtifact(ctx, projectwork.CollaborationArtifact{
		ID:         "art_work_not_started",
		ProjectID:  "proj_projection",
		WorkItemID: "work_not_started",
		Kind:       projectwork.ArtifactKindHandoff,
		Title:      "Work-item handoff",
		Body:       "Shared at the work-item level.",
		CreatedAt:  time.Date(2026, 6, 3, 11, 58, 30, 0, time.UTC),
		UpdatedAt:  time.Date(2026, 6, 3, 11, 58, 30, 0, time.UTC),
	}); err != nil {
		t.Fatalf("CreateArtifact(work_not_started): %v", err)
	}
}

func seedProjectWorkProjectionCase(t *testing.T, handler *Handler, workID, assignmentID, assignmentStatus, runStatus string, runStartedAt, runFinishedAt, assignmentUpdated time.Time, lastError string, approvalPending, missing bool) {
	t.Helper()
	ctx := t.Context()
	if assignmentStatus == "" {
		assignmentStatus = projectwork.AssignmentStatusQueued
	}
	if assignmentUpdated.IsZero() {
		assignmentUpdated = runStartedAt.Add(-time.Minute)
		if runStartedAt.IsZero() {
			assignmentUpdated = time.Date(2026, 6, 3, 11, 59, 0, 0, time.UTC)
		}
	}
	if _, ok, err := handler.projectWork.GetWorkItem(ctx, "proj_projection", workID); err != nil {
		t.Fatalf("GetWorkItem(%s): %v", workID, err)
	} else if !ok {
		if _, err := handler.projectWork.CreateWorkItem(ctx, projectwork.WorkItem{
			ID:        workID,
			ProjectID: "proj_projection",
			Title:     workID,
			Status:    projectwork.WorkItemStatusReady,
			CreatedAt: assignmentUpdated,
			UpdatedAt: assignmentUpdated,
		}); err != nil {
			t.Fatalf("CreateWorkItem(%s): %v", workID, err)
		}
	}

	taskID := "task_" + assignmentID
	runID := "run_" + assignmentID
	if _, err := handler.projectWork.CreateAssignment(ctx, projectwork.Assignment{
		ID:         assignmentID,
		ProjectID:  "proj_projection",
		WorkItemID: workID,
		RoleID:     "role_projection",
		DriverKind: projectwork.AssignmentDriverHecateTask,
		Status:     assignmentStatus,
		ExecutionRef: projectwork.AssignmentExecutionRef{
			Kind:   projectwork.AssignmentExecutionRefKind(taskID, runID, "", "", ""),
			TaskID: taskID,
			RunID:  runID,
		},
		CreatedAt: assignmentUpdated,
		UpdatedAt: assignmentUpdated,
	}); err != nil {
		t.Fatalf("CreateAssignment(%s): %v", assignmentID, err)
	}
	if missing {
		return
	}
	if _, err := handler.taskStore.CreateTask(ctx, types.Task{
		ID:          taskID,
		Title:       assignmentID,
		Status:      runStatus,
		LatestRunID: runID,
		CreatedAt:   assignmentUpdated,
		UpdatedAt:   projectworkapp.FirstNonZeroTime(runFinishedAt, runStartedAt, assignmentUpdated),
	}); err != nil {
		t.Fatalf("CreateTask(%s): %v", taskID, err)
	}
	if _, err := handler.taskStore.CreateRun(ctx, types.TaskRun{
		ID:            runID,
		TaskID:        taskID,
		Number:        1,
		Status:        runStatus,
		Model:         "qwen2.5-coder",
		Provider:      "ollama",
		StepCount:     2,
		ApprovalCount: boolToInt(approvalPending),
		ArtifactCount: 1,
		LastError:     lastError,
		StartedAt:     runStartedAt,
		FinishedAt:    runFinishedAt,
		TraceID:       "trace_" + assignmentID,
	}); err != nil {
		t.Fatalf("CreateRun(%s): %v", runID, err)
	}
	if approvalPending {
		if _, err := handler.taskStore.CreateApproval(ctx, types.TaskApproval{
			ID:        "ap_" + assignmentID,
			TaskID:    taskID,
			RunID:     runID,
			Kind:      "agent_loop_tool_call",
			Status:    "pending",
			CreatedAt: runStartedAt,
		}); err != nil {
			t.Fatalf("CreateApproval(%s): %v", assignmentID, err)
		}
	}
	if assignmentID == "asgn_completed" {
		if _, err := handler.projectWork.CreateArtifact(ctx, projectwork.CollaborationArtifact{
			ID:           "art_" + assignmentID,
			ProjectID:    "proj_projection",
			WorkItemID:   workID,
			AssignmentID: assignmentID,
			Kind:         projectwork.ArtifactKindHandoff,
			Title:        "Completion handoff",
			Body:         "Ready for review.",
			CreatedAt:    runFinishedAt.Add(time.Minute),
			UpdatedAt:    runFinishedAt.Add(time.Minute),
		}); err != nil {
			t.Fatalf("CreateArtifact(%s): %v", assignmentID, err)
		}
		if _, err := handler.projectWork.CreateHandoff(ctx, projectwork.Handoff{
			ID:                    "handoff_review_followup",
			ProjectID:             "proj_projection",
			WorkItemID:            workID,
			SourceAssignmentID:    assignmentID,
			TargetAssignmentID:    "asgn_queued",
			TargetWorkItemID:      "work_queued",
			Title:                 "Review follow-up",
			Summary:               "Implementation is ready for queue review.",
			RecommendedNextAction: "Review the queued follow-up assignment.",
			CreatedAt:             runFinishedAt.Add(2 * time.Minute),
			UpdatedAt:             runFinishedAt.Add(3 * time.Minute),
			StatusChangedAt:       runFinishedAt.Add(2 * time.Minute),
		}); err != nil {
			t.Fatalf("CreateHandoff(%s): %v", assignmentID, err)
		}
	}
}

func getProjectWorkAssignmentForTest(t *testing.T, server http.Handler, workID, assignmentID string) ProjectWorkAssignmentResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_projection/work-items/"+workID+"/assignments", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list assignments %s status = %d body=%s, want 200", workID, rec.Code, rec.Body.String())
	}
	var response ProjectWorkAssignmentsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode assignments: %v", err)
	}
	for _, assignment := range response.Data {
		if assignment.ID == assignmentID {
			return assignment
		}
	}
	t.Fatalf("assignment %s not found in %+v", assignmentID, response.Data)
	return ProjectWorkAssignmentResponse{}
}

func assignmentExecutionRefForTest(t *testing.T, assignment ProjectWorkAssignmentResponse) ProjectWorkAssignmentExecutionRefResponse {
	t.Helper()
	if assignment.ExecutionRef == nil {
		t.Fatalf("assignment %q execution_ref is nil", assignment.ID)
	}
	return *assignment.ExecutionRef
}

func assertCairnlineLaunchPacketEvidenceForTest(t *testing.T, packet ChatContextPacketItem) {
	t.Helper()
	item := findRenderedContextItemByKind(packet, "cairnline_launch_packet")
	if item == nil || item.Section != contextSectionRuntime || item.Included {
		t.Fatalf("Cairnline launch packet item = %+v, want inspect-only runtime evidence", item)
	}
	if item.Origin != "cairnline.assignment_launch_packet" || item.InclusionReason != cairnlineAssignmentLaunchEvidenceReason {
		t.Fatalf("Cairnline launch packet origin/reason = %q/%q, want replacement-readiness evidence", item.Origin, item.InclusionReason)
	}
	for _, want := range []string{
		"Ready: true",
		"Assignment: asgn_start",
		"Execution mode: orchestrated",
		"Root: root_start",
		"Skills: 0; artifacts: 0; evidence: 0; reviews: 0; handoffs: 0; memory: 0; memory candidates: 0",
	} {
		if !strings.Contains(item.Body, want) {
			t.Fatalf("Cairnline launch packet body = %q, want %q", item.Body, want)
		}
	}
	for key, want := range map[string]string{
		"read_backend":   "cairnline",
		"ready":          "true",
		"project_id":     "proj_start",
		"work_item_id":   "work_start",
		"assignment_id":  "asgn_start",
		"role_id":        "role_backend",
		"root_id":        "root_start",
		"execution_mode": "orchestrated",
	} {
		if item.Metadata[key] != want {
			t.Fatalf("Cairnline launch packet metadata[%q] = %q, want %q in %+v", key, item.Metadata[key], want, item.Metadata)
		}
	}
}

func assertProjectWorkStatusForTest(t *testing.T, server http.Handler, workID, want string) {
	t.Helper()
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_projection/work-items/"+workID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("get work item %s status = %d body=%s, want 200", workID, rec.Code, rec.Body.String())
	}
	var response ProjectWorkItemEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode work item: %v", err)
	}
	if response.Data.Status != want {
		t.Fatalf("work item %s status = %q, want %q", workID, response.Data.Status, want)
	}
}

func assertProjectWorkListStatusForTest(t *testing.T, server http.Handler, workID, want string) {
	t.Helper()
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_projection/work-items", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list work items status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectWorkItemsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode work items: %v", err)
	}
	for _, item := range response.Data {
		if item.ID == workID {
			if item.Status != want {
				t.Fatalf("work item list status = %q, want %q", item.Status, want)
			}
			return
		}
	}
	t.Fatalf("work item %s not found in %+v", workID, response.Data)
}

func assertStoredProjectWorkAssignmentStatusForTest(t *testing.T, handler *Handler, workID, assignmentID, want string) {
	t.Helper()
	assignments, err := handler.projectWork.ListAssignments(t.Context(), projectwork.AssignmentFilter{
		ProjectID:  "proj_projection",
		WorkItemID: workID,
	})
	if err != nil {
		t.Fatalf("ListAssignments(%s): %v", workID, err)
	}
	for _, assignment := range assignments {
		if assignment.ID == assignmentID {
			if assignment.Status != want {
				t.Fatalf("stored assignment status = %q, want %q", assignment.Status, want)
			}
			return
		}
	}
	t.Fatalf("stored assignment %s not found in %+v", assignmentID, assignments)
}

type concurrentProjectExternalPrepareRunner struct {
	mu              sync.Mutex
	prepareStarted  chan struct{}
	releasePrepare  chan struct{}
	prepareRequests []agentadapters.PrepareSessionRequest
	closedSessions  []string
	deletedSessions []string
}

func newConcurrentProjectExternalPrepareRunner() *concurrentProjectExternalPrepareRunner {
	return &concurrentProjectExternalPrepareRunner{
		prepareStarted: make(chan struct{}, 2),
		releasePrepare: make(chan struct{}),
	}
}

func (r *concurrentProjectExternalPrepareRunner) PrepareSession(ctx context.Context, req agentadapters.PrepareSessionRequest) (agentadapters.PrepareSessionResult, error) {
	r.mu.Lock()
	r.prepareRequests = append(r.prepareRequests, req)
	r.mu.Unlock()
	r.prepareStarted <- struct{}{}
	select {
	case <-r.releasePrepare:
	case <-ctx.Done():
		return agentadapters.PrepareSessionResult{}, ctx.Err()
	}
	adapter, _ := agentadapters.BuiltInByID(req.AdapterID)
	return agentadapters.PrepareSessionResult{
		Adapter:         adapter,
		DriverKind:      agentadapters.DriverKindACP,
		NativeSessionID: "native_" + req.SessionID,
	}, nil
}

func (r *concurrentProjectExternalPrepareRunner) Run(_ context.Context, _ agentadapters.RunRequest) (agentadapters.RunResult, error) {
	return agentadapters.RunResult{}, nil
}

func (r *concurrentProjectExternalPrepareRunner) SetSessionConfigOption(_ context.Context, _ agentadapters.SetSessionConfigOptionRequest) (agentadapters.SetSessionConfigOptionResult, error) {
	return agentadapters.SetSessionConfigOptionResult{ConfigOptions: []agentcontrols.ConfigOption{}}, nil
}

func (r *concurrentProjectExternalPrepareRunner) CloseSession(_ context.Context, sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closedSessions = append(r.closedSessions, sessionID)
	return nil
}

func (r *concurrentProjectExternalPrepareRunner) DeleteSession(_ context.Context, sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deletedSessions = append(r.deletedSessions, sessionID)
	return nil
}

func (r *concurrentProjectExternalPrepareRunner) Shutdown(context.Context) error {
	return nil
}

func (r *concurrentProjectExternalPrepareRunner) prepareCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.prepareRequests)
}

func (r *concurrentProjectExternalPrepareRunner) closedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.closedSessions)
}

func (r *concurrentProjectExternalPrepareRunner) deletedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.deletedSessions)
}

func findProjectActivityItemForTest(t *testing.T, items []ProjectActivityItemResponse, assignmentID string) ProjectActivityItemResponse {
	t.Helper()
	for _, item := range items {
		if item.Assignment.ID == assignmentID {
			return item
		}
	}
	t.Fatalf("activity assignment %s not found in %+v", assignmentID, items)
	return ProjectActivityItemResponse{}
}

func findProjectWorkItemForTest(t *testing.T, items []ProjectWorkItemResponse, workItemID string) ProjectWorkItemResponse {
	t.Helper()
	for _, item := range items {
		if item.ID == workItemID {
			return item
		}
	}
	t.Fatalf("work item %s not found in %+v", workItemID, items)
	return ProjectWorkItemResponse{}
}

func findProjectWorkArtifactForTest(t *testing.T, items []ProjectWorkArtifactResponse, artifactID string) ProjectWorkArtifactResponse {
	t.Helper()
	for _, item := range items {
		if item.ID == artifactID {
			return item
		}
	}
	t.Fatalf("artifact %s not found in %+v", artifactID, items)
	return ProjectWorkArtifactResponse{}
}

func findProjectHandoffForTest(t *testing.T, items []ProjectHandoffResponse, handoffID string) ProjectHandoffResponse {
	t.Helper()
	for _, item := range items {
		if item.ID == handoffID {
			return item
		}
	}
	t.Fatalf("handoff %s not found in %+v", handoffID, items)
	return ProjectHandoffResponse{}
}

func assertHecateShadowArtifactForTest(t *testing.T, handler *Handler, projectID, workItemID, artifactID, kind string) {
	t.Helper()
	items, err := handler.projectWork.ListArtifacts(t.Context(), projectwork.ArtifactFilter{ProjectID: projectID, WorkItemID: workItemID})
	if err != nil {
		t.Fatalf("ListArtifacts(%q, %q): %v", projectID, workItemID, err)
	}
	for _, item := range items {
		if item.ID == artifactID {
			if item.Kind != kind {
				t.Fatalf("shadow artifact %q kind = %q, want %q", artifactID, item.Kind, kind)
			}
			return
		}
	}
	t.Fatalf("shadow artifact %q not found in %+v", artifactID, items)
}

func assertHecateShadowWorkItemForTest(t *testing.T, handler *Handler, projectID, workItemID, status string) {
	t.Helper()
	item, ok, err := handler.projectWork.GetWorkItem(t.Context(), projectID, workItemID)
	if err != nil {
		t.Fatalf("GetWorkItem(%q, %q): %v", projectID, workItemID, err)
	}
	if !ok {
		t.Fatalf("shadow work item %q not found", workItemID)
	}
	if item.Status != status {
		t.Fatalf("shadow work item %q status = %q, want %q", workItemID, item.Status, status)
	}
}

func assertHecateShadowHandoffStatusForTest(t *testing.T, handler *Handler, projectID, workItemID, handoffID, status string) {
	t.Helper()
	items, err := handler.projectWork.ListHandoffs(t.Context(), projectwork.HandoffFilter{ProjectID: projectID, WorkItemID: workItemID})
	if err != nil {
		t.Fatalf("ListHandoffs(%q, %q): %v", projectID, workItemID, err)
	}
	for _, item := range items {
		if item.ID == handoffID {
			if item.Status != status {
				t.Fatalf("shadow handoff %q status = %q, want %q", handoffID, item.Status, status)
			}
			return
		}
	}
	t.Fatalf("shadow handoff %q not found in %+v", handoffID, items)
}

func allProjectActivityItemsForTest(data ProjectActivityDataResponse) []ProjectActivityItemResponse {
	items := make([]ProjectActivityItemResponse, 0, len(data.Buckets.Active)+len(data.Buckets.Blocked)+len(data.Buckets.Completed)+len(data.Buckets.Recent)+len(data.Recent))
	items = append(items, data.Buckets.Active...)
	items = append(items, data.Buckets.Blocked...)
	items = append(items, data.Buckets.Completed...)
	items = append(items, data.Buckets.Recent...)
	items = append(items, data.Recent...)
	return items
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func createProjectForWorkTest(t *testing.T, server http.Handler) ProjectResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{"name":"Hecate"}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create project status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var project ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &project); err != nil {
		t.Fatalf("decode project: %v", err)
	}
	return project
}

func getMirroredCairnlineRoleForTest(t *testing.T, handler *Handler, projectID, roleID string) cairnline.Role {
	t.Helper()
	role := mirroredCairnlineRoleForTest(t, handler, projectID, roleID)
	if role == nil {
		t.Fatalf("mirrored role %q not found", roleID)
	}
	return *role
}

func mirroredCairnlineRoleForTest(t *testing.T, handler *Handler, projectID, roleID string) *cairnline.Role {
	t.Helper()
	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	defer store.Close()
	roles, err := service.ListRoles(t.Context(), projectID)
	if err != nil {
		t.Fatalf("ListRoles(%q): %v", projectID, err)
	}
	for _, role := range roles {
		if role.ID == roleID {
			found := role
			return &found
		}
	}
	return nil
}

func assertHecateShadowRoleForTest(t *testing.T, handler *Handler, projectID, roleID string) {
	t.Helper()
	if !hasHecateRoleForTest(t, handler, projectID, roleID) {
		t.Fatalf("Hecate shadow role %q not found", roleID)
	}
}

func hasHecateRoleForTest(t *testing.T, handler *Handler, projectID, roleID string) bool {
	t.Helper()
	roles, err := handler.projectWork.ListRoles(t.Context(), projectID)
	if err != nil {
		t.Fatalf("ListRoles(%q): %v", projectID, err)
	}
	for _, role := range roles {
		if role.ID == roleID {
			return true
		}
	}
	return false
}

func getMirroredCairnlineWorkItemForTest(t *testing.T, handler *Handler, projectID, workItemID string) cairnline.WorkItem {
	t.Helper()
	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	defer store.Close()
	item, err := service.GetWorkItem(t.Context(), projectID, workItemID)
	if err != nil {
		t.Fatalf("GetWorkItem(%q, %q): %v", projectID, workItemID, err)
	}
	return item
}

func getMirroredCairnlineAssignmentForTest(t *testing.T, handler *Handler, projectID, assignmentID string) cairnline.Assignment {
	t.Helper()
	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	defer store.Close()
	item, err := service.GetAssignment(t.Context(), projectID, assignmentID)
	if err != nil {
		t.Fatalf("GetAssignment(%q, %q): %v", projectID, assignmentID, err)
	}
	return item
}

func getStoredProjectWorkAssignmentForTest(t *testing.T, handler *Handler, projectID, workItemID, assignmentID string) projectwork.Assignment {
	t.Helper()
	assignments, err := handler.projectWork.ListAssignments(t.Context(), projectwork.AssignmentFilter{
		ProjectID:  projectID,
		WorkItemID: workItemID,
	})
	if err != nil {
		t.Fatalf("ListAssignments(%q, %q): %v", projectID, workItemID, err)
	}
	for _, assignment := range assignments {
		if assignment.ID == assignmentID {
			return assignment
		}
	}
	t.Fatalf("assignment %q not found in %+v", assignmentID, assignments)
	return projectwork.Assignment{}
}

func getMirroredCairnlineArtifactForTest(t *testing.T, handler *Handler, projectID, workItemID, artifactID string) cairnline.Artifact {
	t.Helper()
	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	defer store.Close()
	item, err := service.GetArtifact(t.Context(), projectID, workItemID, artifactID)
	if err != nil {
		t.Fatalf("GetArtifact(%q, %q, %q): %v", projectID, workItemID, artifactID, err)
	}
	return item
}

func getMirroredCairnlineEvidenceForTest(t *testing.T, handler *Handler, projectID, workItemID, evidenceID string) cairnline.Evidence {
	t.Helper()
	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	defer store.Close()
	item, err := service.GetEvidence(t.Context(), projectID, workItemID, evidenceID)
	if err != nil {
		t.Fatalf("GetEvidence(%q, %q, %q): %v", projectID, workItemID, evidenceID, err)
	}
	return item
}

func getMirroredCairnlineReviewForTest(t *testing.T, handler *Handler, projectID, workItemID, reviewID string) cairnline.Review {
	t.Helper()
	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	defer store.Close()
	item, err := service.GetReview(t.Context(), projectID, workItemID, reviewID)
	if err != nil {
		t.Fatalf("GetReview(%q, %q, %q): %v", projectID, workItemID, reviewID, err)
	}
	return item
}

func getMirroredCairnlineHandoffForTest(t *testing.T, handler *Handler, projectID, workItemID, handoffID string) cairnline.Handoff {
	t.Helper()
	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	defer store.Close()
	item, err := service.GetHandoff(t.Context(), projectID, workItemID, handoffID)
	if err != nil {
		t.Fatalf("GetHandoff(%q, %q, %q): %v", projectID, workItemID, handoffID, err)
	}
	return item
}

func projectWorkRoleExists(roles []ProjectWorkRoleResponse, id string, builtIn bool) bool {
	for _, role := range roles {
		if role.ID == id && role.BuiltIn == builtIn {
			return true
		}
	}
	return false
}

func findProjectWorkRoleForTest(t *testing.T, roles []ProjectWorkRoleResponse, id string) ProjectWorkRoleResponse {
	t.Helper()
	for _, role := range roles {
		if role.ID == id {
			return role
		}
	}
	t.Fatalf("role %q not found in %+v", id, roles)
	return ProjectWorkRoleResponse{}
}

func projectWorkRoleExistsStore(roles []projectwork.AgentRoleProfile, id string, builtIn bool) bool {
	for _, role := range roles {
		if role.ID == id && role.BuiltIn == builtIn {
			return true
		}
	}
	return false
}
