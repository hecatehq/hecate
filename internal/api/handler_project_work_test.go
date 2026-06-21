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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/agentcontrols"
	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/chatcontext"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/orchestrator"
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
	return handler, NewServer(quietLogger(), handler)
}

func newProjectWorkTestServerWithProviders(items ...providers.Provider) (*Handler, http.Handler) {
	handler := newTestAPIHandlerWithSettings(quietLogger(), items, config.Config{}, nil)
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
	if deleted.Data.ProjectID != project.Data.ID || deleted.Data.ProjectWorkRowsDeleted != 4 {
		t.Fatalf("delete response = %+v, want project id and 4 project work rows", deleted)
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

func TestProjectWorkAPI_ExternalAgentChatTurnReconcilesAssignmentStatus(t *testing.T) {
	t.Parallel()

	handler, server := newProjectWorkTestServer()
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

func assignmentExecutionRefForTest(t *testing.T, assignment ProjectWorkAssignmentResponse) ProjectWorkAssignmentExecutionRefResponse {
	t.Helper()
	if assignment.ExecutionRef == nil {
		t.Fatalf("assignment %q execution_ref is nil", assignment.ID)
	}
	return *assignment.ExecutionRef
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
