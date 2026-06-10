package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/agentcontrols"
	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/storage"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

func newProjectWorkTestServer() (*Handler, http.Handler) {
	handler := NewHandler(config.Config{}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetProjectWorkStore(projectwork.NewMemoryStore())
	return handler, NewServer(quietLogger(), handler)
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
	if assignment.Data.TaskID == "" || assignment.Data.RunID == "" {
		t.Fatalf("assignment links = task %q run %q, want both set", assignment.Data.TaskID, assignment.Data.RunID)
	}
	if assignment.Data.DriverKind != projectwork.AssignmentDriverHecateTask {
		t.Fatalf("driver_kind = %q, want hecate_task", assignment.Data.DriverKind)
	}
	if assignment.Data.Execution == nil || assignment.Data.Execution.TaskID != assignment.Data.TaskID || assignment.Data.Execution.RunID != assignment.Data.RunID {
		t.Fatalf("assignment execution = %+v, want linked task/run summary", assignment.Data.Execution)
	}

	task, found, err := handler.taskStore.GetTask(t.Context(), assignment.Data.TaskID)
	if err != nil || !found {
		t.Fatalf("GetTask(%q) found=%v err=%v, want task", assignment.Data.TaskID, found, err)
	}
	if task.ExecutionKind != "agent_loop" || task.OriginKind != "project_work_item" || task.OriginID != "work_start" {
		t.Fatalf("task execution/origin = %q %q/%q, want agent_loop project_work_item/work_start", task.ExecutionKind, task.OriginKind, task.OriginID)
	}
	if task.RequestedProvider != "anthropic" || task.RequestedModel != "claude-sonnet-4" || task.ExecutionProfile != "implementation" {
		t.Fatalf("task provider/model/profile = %q/%q/%q, want role defaults", task.RequestedProvider, task.RequestedModel, task.ExecutionProfile)
	}
	if task.WorkingDirectory != workspace || task.SandboxAllowedRoot != workspace || task.WorkspaceMode != "in_place" {
		t.Fatalf("task workspace = dir %q root %q mode %q, want %q in_place", task.WorkingDirectory, task.SandboxAllowedRoot, task.WorkspaceMode, workspace)
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
	if _, found, err := handler.taskStore.GetRun(t.Context(), task.ID, assignment.Data.RunID); err != nil || !found {
		t.Fatalf("GetRun(%q) found=%v err=%v, want run", assignment.Data.RunID, found, err)
	}
}

func TestProjectWorkAPI_StartAssignmentPersistsInspectableContextPacket(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	handler.SetMemoryStore(memory.NewMemoryStore())
	workspace := t.TempDir()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace: workspace,
		Driver:    projectwork.AssignmentDriverHecateTask,
	})
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
	if assignment.Data.ContextSnapshotID == "" {
		t.Fatalf("context_snapshot_id = %q, want persisted packet id", assignment.Data.ContextSnapshotID)
	}

	packetResp := mustRequestJSON[ChatContextPacketResponse](newAPITestClient(t, server), http.MethodGet, "/hecate/v1/tasks/"+assignment.Data.TaskID+"/runs/"+assignment.Data.RunID+"/context", "")
	if packetResp.Data.ID != assignment.Data.ContextSnapshotID {
		t.Fatalf("task run context id = %q, want %q", packetResp.Data.ID, assignment.Data.ContextSnapshotID)
	}
	if packetResp.Data.ExecutionProfile != "implementation" {
		t.Fatalf("execution_profile = %q, want implementation", packetResp.Data.ExecutionProfile)
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
	if assignmentPacket.Data.ID != assignment.Data.ContextSnapshotID {
		t.Fatalf("assignment context id = %q, want %q", assignmentPacket.Data.ID, assignment.Data.ContextSnapshotID)
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
			seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
				Workspace: workspace,
				Driver:    projectwork.AssignmentDriverHecateTask,
			})
			if _, err := handler.agentProfiles.Create(t.Context(), agentprofiles.Profile{
				ID:                  "implementation",
				Name:                "Implementation",
				Surface:             agentprofiles.SurfaceHecateTask,
				ExecutionProfile:    "implementation",
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
			packetResp := mustRequestJSON[ChatContextPacketResponse](newAPITestClient(t, server), http.MethodGet, "/hecate/v1/tasks/"+assignment.Data.TaskID+"/runs/"+assignment.Data.RunID+"/context", "")

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

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("start assignment status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var assignment ProjectWorkAssignmentEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &assignment); err != nil {
		t.Fatalf("decode assignment: %v", err)
	}
	task, found, err := handler.taskStore.GetTask(t.Context(), assignment.Data.TaskID)
	if err != nil || !found {
		t.Fatalf("GetTask(%q) found=%v err=%v, want task", assignment.Data.TaskID, found, err)
	}
	if task.RequestedProvider != "anthropic" || task.RequestedModel != "claude-sonnet-4" || task.ExecutionProfile != "role_profile" {
		t.Fatalf("task provider/model/profile = %q/%q/%q, want role profile hints", task.RequestedProvider, task.RequestedModel, task.ExecutionProfile)
	}
	if !strings.Contains(task.SystemPrompt, "Agent profile instructions:\nUse the profile-specific review checklist.") {
		t.Fatalf("task system prompt = %q, want profile instructions", task.SystemPrompt)
	}

	packetResp := mustRequestJSON[ChatContextPacketResponse](newAPITestClient(t, server), http.MethodGet, "/hecate/v1/tasks/"+assignment.Data.TaskID+"/runs/"+assignment.Data.RunID+"/context", "")
	if packetResp.Data.ExecutionProfile != "role_profile" {
		t.Fatalf("packet execution_profile = %q, want role_profile", packetResp.Data.ExecutionProfile)
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
	if _, err := handler.agentProfiles.Create(t.Context(), agentprofiles.Profile{
		ID:                  "prof_external",
		Name:                "External implementer",
		Instructions:        "Use the linked project context before editing.",
		Surface:             agentprofiles.SurfaceExternalAgent,
		ExecutionProfile:    "external_implementation",
		ExternalAgentKind:   "codex",
		ApprovalPolicy:      agentprofiles.ApprovalRequire,
		ProjectMemoryPolicy: agentprofiles.MemoryVisibleOnly,
		ContextSourcePolicy: agentprofiles.ContextVisibleOnly,
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
	if assignment.Data.ChatSessionID == "" || assignment.Data.ContextSnapshotID == "" {
		t.Fatalf("assignment links = chat %q context %q, want linked session and context", assignment.Data.ChatSessionID, assignment.Data.ContextSnapshotID)
	}
	if assignment.Data.TaskID != "" || assignment.Data.RunID != "" || assignment.Data.MessageID != "" {
		t.Fatalf("assignment task/run/message links = %q/%q/%q, want no task run or dispatched message", assignment.Data.TaskID, assignment.Data.RunID, assignment.Data.MessageID)
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
	if prepare.AdapterID != "codex" || prepare.SessionID != assignment.Data.ChatSessionID || prepare.Workspace != resolvedWorkspace {
		t.Fatalf("prepare request = %+v, want codex session in workspace", prepare)
	}
	session, ok, err := handler.agentChat.Get(t.Context(), assignment.Data.ChatSessionID)
	if err != nil || !ok {
		t.Fatalf("Get chat session found=%v err=%v, want linked session", ok, err)
	}
	if session.AgentID != "codex" || session.DriverKind != agentadapters.DriverKindACP || session.NativeSessionID != "native_project_external" || session.ProjectID != "proj_start" {
		t.Fatalf("session = %+v, want prepared codex project session", session)
	}

	packetResp := mustRequestJSON[ChatContextPacketResponse](newAPITestClient(t, server), http.MethodGet, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/context", "")
	if packetResp.Data.ID != assignment.Data.ContextSnapshotID {
		t.Fatalf("assignment context id = %q, want %q", packetResp.Data.ID, assignment.Data.ContextSnapshotID)
	}
	if packetResp.Data.ExecutionMode != chat.ExecutionModeExternalAgent || packetResp.Data.Refs == nil || packetResp.Data.Refs.SessionID != assignment.Data.ChatSessionID {
		t.Fatalf("packet execution/refs = %q/%+v, want external agent session refs", packetResp.Data.ExecutionMode, packetResp.Data.Refs)
	}
	profileItem := findRenderedContextItemByOrigin(packetResp.Data, "prof_external")
	if profileItem == nil || profileItem.Section != contextSectionProfile || !strings.Contains(profileItem.Body, "External agent: codex") {
		t.Fatalf("profile item = %+v, want external profile metadata", profileItem)
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
	if len(assignments) != 1 || assignments[0].ChatSessionID != sessions[0].ID {
		t.Fatalf("assignment/chat link = %+v / %+v, want one linked surviving chat", assignments, sessions)
	}
	if got := runner.prepareCount(); got != 2 {
		t.Fatalf("prepare requests = %d, want both requests to reach prepare", got)
	}
	if got := runner.closedCount(); got != 1 {
		t.Fatalf("closed sessions = %d, want losing prepared chat closed", got)
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
	packetResp := mustRequestJSON[ChatContextPacketResponse](newAPITestClient(t, server), http.MethodGet, "/hecate/v1/tasks/"+assignment.Data.TaskID+"/runs/"+assignment.Data.RunID+"/context", "")
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
	task, found, err := handler.taskStore.GetTask(t.Context(), assignment.Data.TaskID)
	if err != nil || !found {
		t.Fatalf("GetTask(%q) found=%v err=%v, want task", assignment.Data.TaskID, found, err)
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
	task, found, err := handler.taskStore.GetTask(t.Context(), assignment.Data.TaskID)
	if err != nil || !found {
		t.Fatalf("GetTask(%q) found=%v err=%v, want task", assignment.Data.TaskID, found, err)
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
	packet := normalizeContextPacket(chat.ContextPacket{
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
		item.ChatSessionID = "chat_linked"
		item.MessageID = "msg_linked"
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
	task, found, err := handler.taskStore.GetTask(t.Context(), assignment.Data.TaskID)
	if err != nil || !found {
		t.Fatalf("GetTask(%q) found=%v err=%v, want task", assignment.Data.TaskID, found, err)
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
	prompt := projectAssignmentPrompt(
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
	if second.Data.TaskID != first.Data.TaskID || second.Data.RunID != first.Data.RunID {
		t.Fatalf("second assignment links = %q/%q, want existing %q/%q", second.Data.TaskID, second.Data.RunID, first.Data.TaskID, first.Data.RunID)
	}
	tasks, err := handler.taskStore.ListTasks(t.Context(), taskstateFilterAll())
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("task count = %d, want 1", len(tasks))
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
	if assignment.Data.TaskID != "task_existing" || assignment.Data.RunID != "run_existing" {
		t.Fatalf("terminal assignment links = %q/%q, want existing links", assignment.Data.TaskID, assignment.Data.RunID)
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
	if assignment.TaskID == "" {
		t.Fatalf("assignment task_id is empty, want preserved task link")
	}
	if assignment.Status != projectwork.AssignmentStatusFailed {
		t.Fatalf("assignment status = %q, want failed", assignment.Status)
	}
	if assignment.CompletedAt.IsZero() {
		t.Fatalf("assignment completed_at is zero, want failure timestamp")
	}
	if _, found, err := handler.taskStore.GetTask(t.Context(), assignment.TaskID); err != nil || !found {
		t.Fatalf("GetTask(%q) found=%v err=%v, want preserved task", assignment.TaskID, found, err)
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
	if assignment.TaskID != "" || assignment.RunID != "" {
		t.Fatalf("assignment links = %q/%q, want cleared task/run links", assignment.TaskID, assignment.RunID)
	}
	if assignment.Status != projectwork.AssignmentStatusQueued {
		t.Fatalf("assignment status = %q, want queued for retry", assignment.Status)
	}
	if !assignment.StartedAt.IsZero() || !assignment.CompletedAt.IsZero() {
		t.Fatalf("assignment timestamps = started %v completed %v, want cleared", assignment.StartedAt, assignment.CompletedAt)
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
			if runOnly.Status != projectwork.AssignmentStatusQueued || runOnly.RunID == "" || runOnly.Execution != nil {
				t.Fatalf("run-only assignment = %+v, want stored queued status without execution projection", runOnly)
			}
			assertProjectWorkStatusForTest(t, server, "work_run_only", projectwork.WorkItemStatusReady)

			manual := getProjectWorkAssignmentForTest(t, server, "work_manual_terminal", "asgn_manual_terminal")
			if manual.Status != projectwork.AssignmentStatusFailed || manual.Execution == nil || manual.Execution.RunStatus != "completed" {
				t.Fatalf("manual terminal assignment = %+v, want newer explicit failed status over stale completed run", manual)
			}
			assertProjectWorkStatusForTest(t, server, "work_manual_terminal", projectwork.WorkItemStatusBlocked)

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
			if response.Data.Summary.WorkItemCount == 0 || response.Data.Summary.AssignmentCount == 0 {
				t.Fatalf("activity summary = %+v, want work and assignment counts", response.Data.Summary)
			}

			awaiting := findProjectActivityItemForTest(t, response.Data.Buckets.Blocked, "asgn_awaiting")
			if awaiting.BlockingSignal != "awaiting_approval" || awaiting.Assignment.Execution == nil || awaiting.Assignment.Execution.PendingApprovalCount != 1 {
				t.Fatalf("awaiting activity = %+v, want approval blocking signal", awaiting)
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
			if external.BlockingSignal != "running" || external.StatusSummary != "linked chat · running · assistant completed · 2 messages" {
				t.Fatalf("external activity = %+v, want linked chat running summary", external)
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
		role.DefaultAgentProfile = "implementation"
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
		TaskID:     seed.TaskID,
		RunID:      seed.RunID,
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
	if _, err := handler.projectWork.CreateRole(ctx, projectwork.AgentRoleProfile{ID: "role_projection", ProjectID: "proj_projection", Name: "Projection engineer"}); err != nil {
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
		ID:            "asgn_external_chat",
		ProjectID:     "proj_projection",
		WorkItemID:    "work_external_chat",
		RoleID:        "role_projection",
		DriverKind:    projectwork.AssignmentDriverExternalAgent,
		Status:        projectwork.AssignmentStatusRunning,
		ChatSessionID: "chat_external_projection",
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt,
	}); err != nil {
		t.Fatalf("CreateAssignment(asgn_external_chat): %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(ctx, projectwork.Assignment{
		ID:            "asgn_missing_chat",
		ProjectID:     "proj_projection",
		WorkItemID:    "work_external_chat",
		RoleID:        "role_projection",
		DriverKind:    projectwork.AssignmentDriverExternalAgent,
		Status:        projectwork.AssignmentStatusRunning,
		ChatSessionID: "chat_external_missing",
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt,
	}); err != nil {
		t.Fatalf("CreateAssignment(asgn_missing_chat): %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(ctx, projectwork.Assignment{
		ID:            "asgn_failed_chat",
		ProjectID:     "proj_projection",
		WorkItemID:    "work_external_chat",
		RoleID:        "role_projection",
		DriverKind:    projectwork.AssignmentDriverExternalAgent,
		Status:        projectwork.AssignmentStatusRunning,
		ChatSessionID: "chat_external_failed",
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt,
	}); err != nil {
		t.Fatalf("CreateAssignment(asgn_failed_chat): %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(ctx, projectwork.Assignment{
		ID:            "asgn_prepared_chat",
		ProjectID:     "proj_projection",
		WorkItemID:    "work_external_chat",
		RoleID:        "role_projection",
		DriverKind:    projectwork.AssignmentDriverExternalAgent,
		Status:        projectwork.AssignmentStatusRunning,
		ChatSessionID: "chat_external_prepared",
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt,
	}); err != nil {
		t.Fatalf("CreateAssignment(asgn_prepared_chat): %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(ctx, projectwork.Assignment{
		ID:            "asgn_cross_project_chat",
		ProjectID:     "proj_projection",
		WorkItemID:    "work_external_chat",
		RoleID:        "role_projection",
		DriverKind:    projectwork.AssignmentDriverExternalAgent,
		Status:        projectwork.AssignmentStatusRunning,
		ChatSessionID: "chat_external_other_project",
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt,
	}); err != nil {
		t.Fatalf("CreateAssignment(asgn_cross_project_chat): %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(ctx, projectwork.Assignment{
		ID:            "asgn_error_chat",
		ProjectID:     "proj_projection",
		WorkItemID:    "work_external_chat",
		RoleID:        "role_projection",
		DriverKind:    projectwork.AssignmentDriverExternalAgent,
		Status:        projectwork.AssignmentStatusRunning,
		ChatSessionID: "chat_external_error",
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt,
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
		RunID:      "run_without_task",
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
	if _, ok, err := handler.projectWork.GetWorkItem(ctx, "proj_projection", workID); err != nil {
		t.Fatalf("GetWorkItem(%s): %v", workID, err)
	} else if !ok {
		if _, err := handler.projectWork.CreateWorkItem(ctx, projectwork.WorkItem{
			ID:        workID,
			ProjectID: "proj_projection",
			Title:     workID,
			Status:    projectwork.WorkItemStatusReady,
		}); err != nil {
			t.Fatalf("CreateWorkItem(%s): %v", workID, err)
		}
	}

	taskID := "task_" + assignmentID
	runID := "run_" + assignmentID
	if assignmentStatus == "" {
		assignmentStatus = projectwork.AssignmentStatusQueued
	}
	if assignmentUpdated.IsZero() {
		assignmentUpdated = runStartedAt.Add(-time.Minute)
		if runStartedAt.IsZero() {
			assignmentUpdated = time.Date(2026, 6, 3, 11, 59, 0, 0, time.UTC)
		}
	}
	if _, err := handler.projectWork.CreateAssignment(ctx, projectwork.Assignment{
		ID:         assignmentID,
		ProjectID:  "proj_projection",
		WorkItemID: workID,
		RoleID:     "role_projection",
		DriverKind: projectwork.AssignmentDriverHecateTask,
		Status:     assignmentStatus,
		TaskID:     taskID,
		RunID:      runID,
		CreatedAt:  assignmentUpdated,
		UpdatedAt:  assignmentUpdated,
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
		UpdatedAt:   firstNonZeroTime(runFinishedAt, runStartedAt, assignmentUpdated),
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
			UpdatedAt:             runFinishedAt.Add(2 * time.Minute),
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
