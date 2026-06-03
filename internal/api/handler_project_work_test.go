package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
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

	task, found, err := handler.taskStore.GetTask(t.Context(), assignment.Data.TaskID)
	if err != nil || !found {
		t.Fatalf("GetTask(%q) found=%v err=%v, want task", assignment.Data.TaskID, found, err)
	}
	if task.ExecutionKind != "agent_loop" || task.OriginKind != "project_work_item" || task.OriginID != "work_start" {
		t.Fatalf("task execution/origin = %q %q/%q, want agent_loop project_work_item/work_start", task.ExecutionKind, task.OriginKind, task.OriginID)
	}
	if task.RequestedProvider != "ollama" || task.RequestedModel != "qwen2.5-coder" {
		t.Fatalf("task provider/model = %q/%q, want ollama/qwen2.5-coder", task.RequestedProvider, task.RequestedModel)
	}
	if task.WorkingDirectory != workspace || task.SandboxAllowedRoot != workspace || task.WorkspaceMode != "in_place" {
		t.Fatalf("task workspace = dir %q root %q mode %q, want %q in_place", task.WorkingDirectory, task.SandboxAllowedRoot, task.WorkspaceMode, workspace)
	}
	for _, want := range []string{"Implement the native assignment start path.", "Follow backend invariants."} {
		if !strings.Contains(task.Prompt, want) {
			t.Fatalf("task prompt = %q, want %q", task.Prompt, want)
		}
	}
	if !strings.Contains(task.SystemPrompt, "Follow backend invariants.") || !strings.Contains(task.SystemPrompt, "Project default system prompt.") {
		t.Fatalf("task system_prompt = %q, want role and project prompts", task.SystemPrompt)
	}
	if _, found, err := handler.taskStore.GetRun(t.Context(), task.ID, assignment.Data.RunID); err != nil || !found {
		t.Fatalf("GetRun(%q) found=%v err=%v, want run", assignment.Data.RunID, found, err)
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

func TestProjectWorkAPI_StartAssignmentRejectsUnsupportedDriver(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	seedProjectWorkAssignmentStartTest(t, handler, projectWorkAssignmentStartSeed{
		Workspace: t.TempDir(),
		Driver:    projectwork.AssignmentDriverExternalAgent,
	})

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_start/work-items/work_start/assignments/asgn_start/start", bytes.NewReader([]byte(`{"driver_kind":"external_agent"}`))))
	if rec.Code != http.StatusConflict {
		t.Fatalf("start external assignment status = %d body=%s, want 409", rec.Code, rec.Body.String())
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

type failingCreateTaskStore struct {
	taskstate.Store
}

func (s failingCreateTaskStore) CreateTask(context.Context, types.Task) (types.Task, error) {
	return types.Task{}, errors.New("create task failed")
}

type projectWorkAssignmentStartSeed struct {
	Workspace string
	Driver    string
	Status    string
	TaskID    string
	RunID     string
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
	if seed.Workspace != "" {
		project.Roots = []projects.Root{{ID: "root_start", Path: seed.Workspace, Kind: "git", Active: true}}
		project.DefaultRootID = "root_start"
	}
	if _, err := handler.projects.Create(t.Context(), project); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	if _, err := handler.projectWork.CreateRole(t.Context(), projectwork.AgentRoleProfile{
		ID:           "role_backend",
		ProjectID:    "proj_start",
		Name:         "Backend engineer",
		Instructions: "Follow backend invariants.",
	}); err != nil {
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
