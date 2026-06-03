package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectwork"
)

func newProjectWorkTestServer() (*Handler, http.Handler) {
	handler := NewHandler(config.Config{}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetProjectWorkStore(projectwork.NewMemoryStore())
	return handler, NewServer(quietLogger(), handler)
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
		"description":"Coordinates release work"
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
		"task_id":"task_123",
		"run_id":"run_123",
		"context_snapshot_id":"ctx_123"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create assignment status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var assignment ProjectWorkAssignmentEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &assignment); err != nil {
		t.Fatalf("decode assignment: %v", err)
	}
	if assignment.Data.Status != projectwork.AssignmentStatusQueued || assignment.Data.TaskID != "task_123" {
		t.Fatalf("assignment = %+v, want queued linked task", assignment.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_backend/assignments/asgn_backend", bytes.NewReader([]byte(`{"status":"completed","chat_session_id":"chat_123","message_id":"msg_123"}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("patch assignment status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &assignment); err != nil {
		t.Fatalf("decode patched assignment: %v", err)
	}
	if assignment.Data.Status != projectwork.AssignmentStatusCompleted || assignment.Data.ChatSessionID != "chat_123" {
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
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+project.Data.ID+"/work-items/work_backend/artifacts", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list artifacts status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var artifacts ProjectWorkArtifactsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &artifacts); err != nil {
		t.Fatalf("decode artifacts: %v", err)
	}
	if len(artifacts.Data) != 1 || artifacts.Data[0].ID != "art_handoff" {
		t.Fatalf("artifacts = %+v, want created artifact", artifacts.Data)
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
	if _, err := handler.projectWork.CreateArtifact(ctx, projectwork.CollaborationArtifact{ID: "art_cleanup", ProjectID: project.Data.ID, WorkItemID: "work_cleanup", Kind: projectwork.ArtifactKindReview, Body: "Looks good."}); err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/projects/"+project.Data.ID, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete project status = %d body=%s, want 204", rec.Code, rec.Body.String())
	}
	if items, err := handler.projectWork.ListWorkItems(ctx, project.Data.ID); err != nil || len(items) != 0 {
		t.Fatalf("project work items after project delete = %+v err=%v, want none", items, err)
	}
	if roles, err := handler.projectWork.ListRoles(ctx, project.Data.ID); err != nil || projectWorkRoleExistsStore(roles, "role_custom", false) {
		t.Fatalf("project roles after project delete = %+v err=%v, want custom role gone", roles, err)
	}
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

func projectWorkRoleExists(roles []ProjectWorkRoleResponse, id string, builtIn bool) bool {
	for _, role := range roles {
		if role.ID == id && role.BuiltIn == builtIn {
			return true
		}
	}
	return false
}

func projectWorkRoleExistsStore(roles []projectwork.AgentRoleProfile, id string, builtIn bool) bool {
	for _, role := range roles {
		if role.ID == id && role.BuiltIn == builtIn {
			return true
		}
	}
	return false
}
