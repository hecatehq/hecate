package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/projectworkapp"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestProjectOperationsBrief_ReadOnlyProjectOperations(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	if _, err := handler.projects.Create(t.Context(), projects.Project{
		ID:    "proj_ops",
		Name:  "Operations",
		Roots: []projects.Root{{ID: "root_ops", Path: t.TempDir(), Kind: "git", Active: true}},
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	if _, err := handler.projectWork.CreateWorkItem(t.Context(), projectwork.WorkItem{
		ID:        "work_review",
		ProjectID: "proj_ops",
		Title:     "Review backend",
		Status:    projectwork.WorkItemStatusRunning,
		Priority:  "high",
	}); err != nil {
		t.Fatalf("CreateWorkItem(work_review): %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(t.Context(), projectwork.Assignment{
		ID:         "asgn_approval",
		ProjectID:  "proj_ops",
		WorkItemID: "work_review",
		RoleID:     "software_developer",
		DriverKind: projectwork.AssignmentDriverHecateTask,
		Status:     projectwork.AssignmentStatusAwaitingApproval,
		ExecutionRef: projectwork.AssignmentExecutionRef{
			Kind:                 projectwork.AssignmentExecutionKindTaskRun,
			TaskID:               "task_approval",
			RunID:                "run_approval",
			Status:               projectwork.AssignmentStatusAwaitingApproval,
			PendingApprovalCount: 1,
		},
	}); err != nil {
		t.Fatalf("CreateAssignment(asgn_approval): %v", err)
	}
	if _, err := handler.projectWork.CreateWorkItem(t.Context(), projectwork.WorkItem{
		ID:        "work_empty",
		ProjectID: "proj_ops",
		Title:     "Build operator brief",
		Status:    projectwork.WorkItemStatusReady,
		Priority:  "normal",
	}); err != nil {
		t.Fatalf("CreateWorkItem(work_empty): %v", err)
	}
	if _, err := handler.projectWork.CreateHandoff(t.Context(), projectwork.Handoff{
		ID:                    "handoff_review",
		ProjectID:             "proj_ops",
		WorkItemID:            "work_review",
		SourceAssignmentID:    "asgn_approval",
		TargetRoleID:          "reviewer_qa",
		Title:                 "Review follow-up",
		Summary:               "Review the pending backend handoff.",
		RecommendedNextAction: "Create a review assignment.",
		Status:                projectwork.HandoffStatusPending,
	}); err != nil {
		t.Fatalf("CreateHandoff: %v", err)
	}
	if _, err := handler.memoryCandidates.CreateCandidate(t.Context(), memory.Candidate{
		ID:        "memcand_ops",
		ProjectID: "proj_ops",
		Title:     "Remember review boundary",
		Body:      "Review work should stay explicit.",
		Status:    memory.CandidateStatusPending,
	}); err != nil {
		t.Fatalf("CreateCandidate: %v", err)
	}

	beforeAssignments, err := handler.projectWork.ListAssignments(t.Context(), projectwork.AssignmentFilter{ProjectID: "proj_ops"})
	if err != nil {
		t.Fatalf("ListAssignments before: %v", err)
	}
	beforeCandidates, err := handler.memoryCandidates.ListCandidates(t.Context(), memory.CandidateFilter{ProjectID: "proj_ops"})
	if err != nil {
		t.Fatalf("ListCandidates before: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_ops/operations/brief", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("operations brief status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectOperationsBriefEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode operations brief: %v", err)
	}
	if response.Object != "project_operations_brief" || response.Data.ProjectID != "proj_ops" {
		t.Fatalf("operations envelope = %+v, want project_operations_brief for project", response)
	}
	if response.Data.ReadBackend != "hecate" {
		t.Fatalf("operations read_backend = %q, want hecate", response.Data.ReadBackend)
	}
	if response.Data.Summary.PendingMemoryCandidateCount != 1 || response.Data.Summary.PendingHandoffCount != 1 {
		t.Fatalf("operations summary = %+v, want memory candidate and handoff counts", response.Data.Summary)
	}
	if response.Data.Summary.ItemLimit != projectOperationsBriefItemLimit || response.Data.Summary.AvailableItemCount != response.Data.Summary.ItemCount || response.Data.Summary.OmittedItemCount != 0 {
		t.Fatalf("operations summary = %+v, want untruncated item counts", response.Data.Summary)
	}
	assertProjectOperationsItemsHaveActions(t, response.Data.Items)

	defaults := findProjectOperationsItemForTest(t, response.Data.Items, "configure_project_defaults")
	if defaults.Target.Surface != "project_settings" || defaults.Action.Type != projectOperationsActionOpenProjectSettings || defaults.ActionLabel != "Project settings" {
		t.Fatalf("defaults item = %+v, want project settings target", defaults)
	}
	approval := findProjectOperationsItemForTest(t, response.Data.Items, "approve_assignment")
	if approval.Target.WorkItemID != "work_review" || approval.Target.AssignmentID != "asgn_approval" || approval.Target.ActivityBucket != "blocked" || approval.Action.Type != projectOperationsActionOpenWorkItem {
		t.Fatalf("approval item = %+v, want blocked assignment target", approval)
	}
	handoff := findProjectOperationsItemForTest(t, response.Data.Items, "review_pending_handoff")
	if handoff.Handoff == nil || handoff.Handoff.ID != "handoff_review" || handoff.Target.HandoffID != "handoff_review" || handoff.Action.Type != projectOperationsActionOpenWorkItem {
		t.Fatalf("handoff item = %+v, want rendered handoff target", handoff)
	}
	memoryItem := findProjectOperationsItemForTest(t, response.Data.Items, "review_memory_candidates")
	if memoryItem.Target.Surface != "memory" || memoryItem.Action.Type != projectOperationsActionOpenMemoryReview {
		t.Fatalf("memory item = %+v, want memory target", memoryItem)
	}
	assignmentGap := findProjectOperationsItemForTest(t, response.Data.Items, "prepare_first_assignment")
	if assignmentGap.Target.WorkItemID != "work_empty" || assignmentGap.Action.Type != projectOperationsActionDraftProjectProposal || assignmentGap.Action.Request == "" {
		t.Fatalf("assignment gap item = %+v, want draft action for empty work item", assignmentGap)
	}

	afterAssignments, err := handler.projectWork.ListAssignments(t.Context(), projectwork.AssignmentFilter{ProjectID: "proj_ops"})
	if err != nil {
		t.Fatalf("ListAssignments after: %v", err)
	}
	afterCandidates, err := handler.memoryCandidates.ListCandidates(t.Context(), memory.CandidateFilter{ProjectID: "proj_ops"})
	if err != nil {
		t.Fatalf("ListCandidates after: %v", err)
	}
	if len(afterAssignments) != len(beforeAssignments) || len(afterCandidates) != len(beforeCandidates) {
		t.Fatalf("operations brief mutated project state: assignments %d->%d candidates %d->%d", len(beforeAssignments), len(afterAssignments), len(beforeCandidates), len(afterCandidates))
	}
}

func TestProjectOperationsBrief_CairnlineConfiguredUsesReadModel(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{CoordinationBackend: "cairnline"},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetProjectWorkStore(projectwork.NewMemoryStore())
	server := NewServer(quietLogger(), handler)

	if _, err := handler.projects.Create(t.Context(), projects.Project{
		ID:              "proj_cairnline_ops",
		Name:            "Cairnline Operations",
		DefaultProvider: "openai",
		DefaultModel:    "gpt-5",
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	if _, err := handler.projectWork.CreateWorkItem(t.Context(), projectwork.WorkItem{
		ID:        "work_cairnline_ops",
		ProjectID: "proj_cairnline_ops",
		Title:     "Exercise Cairnline operations",
		Status:    projectwork.WorkItemStatusRunning,
	}); err != nil {
		t.Fatalf("CreateWorkItem: %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(t.Context(), projectwork.Assignment{
		ID:         "asgn_cairnline_approval",
		ProjectID:  "proj_cairnline_ops",
		WorkItemID: "work_cairnline_ops",
		RoleID:     "software_developer",
		DriverKind: projectwork.AssignmentDriverHecateTask,
		Status:     projectwork.AssignmentStatusAwaitingApproval,
		ExecutionRef: projectwork.AssignmentExecutionRef{
			Kind:                 projectwork.AssignmentExecutionKindTaskRun,
			TaskID:               "task_cairnline",
			RunID:                "run_cairnline",
			Status:               projectwork.AssignmentStatusAwaitingApproval,
			PendingApprovalCount: 2,
		},
	}); err != nil {
		t.Fatalf("CreateAssignment: %v", err)
	}
	base := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	if _, err := handler.taskStore.CreateTask(t.Context(), types.Task{
		ID:          "task_cairnline",
		Title:       "Cairnline approval task",
		Status:      "awaiting_approval",
		LatestRunID: "run_cairnline",
		CreatedAt:   base,
		UpdatedAt:   base.Add(time.Minute),
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := handler.taskStore.CreateRun(t.Context(), types.TaskRun{
		ID:            "run_cairnline",
		TaskID:        "task_cairnline",
		Number:        1,
		Status:        "awaiting_approval",
		ApprovalCount: 2,
		StartedAt:     base,
	}); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	for _, approvalID := range []string{"ap_cairnline_1", "ap_cairnline_2"} {
		if _, err := handler.taskStore.CreateApproval(t.Context(), types.TaskApproval{
			ID:        approvalID,
			TaskID:    "task_cairnline",
			RunID:     "run_cairnline",
			Kind:      "agent_loop_tool_call",
			Status:    "pending",
			CreatedAt: base,
		}); err != nil {
			t.Fatalf("CreateApproval(%s): %v", approvalID, err)
		}
	}
	if _, err := handler.memoryCandidates.CreateCandidate(t.Context(), memory.Candidate{
		ID:        "memcand_cairnline_ops",
		ProjectID: "proj_cairnline_ops",
		Title:     "Cairnline memory",
		Body:      "Operations keeps memory review visible.",
		Status:    memory.CandidateStatusPending,
	}); err != nil {
		t.Fatalf("CreateCandidate: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_cairnline_ops/operations/brief", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("operations brief status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectOperationsBriefEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode operations brief: %v", err)
	}
	if response.Data.ReadBackend != "cairnline" {
		t.Fatalf("operations read_backend = %q, want cairnline", response.Data.ReadBackend)
	}
	if response.Data.Summary.PendingMemoryCandidateCount != 1 {
		t.Fatalf("operations summary = %+v, want pending memory candidate from Cairnline read model", response.Data.Summary)
	}
	approval := findProjectOperationsItemForTest(t, response.Data.Items, "approve_assignment")
	if approval.Target.AssignmentID != "asgn_cairnline_approval" || approval.Action.Type != projectOperationsActionOpenWorkItem || approval.Status != "awaiting_approval" {
		t.Fatalf("approval item = %+v, want Hecate action projection over Cairnline assignment item", approval)
	}
	if approval.Detail != "2 approval pending" {
		t.Fatalf("approval detail = %q, want pending approval count preserved", approval.Detail)
	}
	memoryItem := findProjectOperationsItemForTest(t, response.Data.Items, "review_memory_candidates")
	if memoryItem.Target.Surface != "memory" || memoryItem.Action.Type != projectOperationsActionOpenMemoryReview {
		t.Fatalf("memory item = %+v, want memory review action", memoryItem)
	}
}

func TestProjectOperationsBrief_UsesCairnlineSidecarWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineSidecarReadTestServer(t, "collaboration-fixture+strict-projects")
	if handler.projectReadRoutesUseCairnlineReadModel() {
		t.Fatal("sidecar operations enabled embedded Cairnline read-model routes")
	}
	if !handler.projectCairnlineSidecarReadRoutesEnabled() {
		t.Fatal("sidecar read-route predicate = false, want true")
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/operations/brief", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("operations status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectOperationsBriefEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode operations brief: %v", err)
	}
	if response.Object != "project_operations_brief" || response.Data.ProjectID != "proj_fixture" || response.Data.ReadBackend != "cairnline" {
		t.Fatalf("operations response = %+v, want sidecar Cairnline operations", response)
	}
	start := findProjectOperationsItemForTest(t, response.Data.Items, "start_queued_assignment")
	if start.Target.WorkItemID != "work_fixture" || start.Target.AssignmentID != "asg_fixture" || start.Action.Type != projectOperationsActionOpenAssignmentPreflight {
		t.Fatalf("start item = %+v, want queued sidecar assignment preflight action", start)
	}
	review := findProjectOperationsItemForTest(t, response.Data.Items, "review_follow_up")
	if review.Target.WorkItemID != "work_fixture" || review.Action.Type != projectOperationsActionOpenWorkItem {
		t.Fatalf("review item = %+v, want sidecar review follow-up action", review)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/missing/operations/brief", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing project operations status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
}

func TestProjectOperationsBrief_CairnlineSidecarReadRequiresStructuredContent(t *testing.T) {
	t.Parallel()
	_, server := newProjectsCairnlineSidecarReadTestServer(t, "projects.operations_brief-text-only")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/operations/brief", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("operations status = %d body=%s, want 502", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "structuredContent") {
		t.Fatalf("error body = %s, want structuredContent diagnostic", rec.Body.String())
	}
}

func TestProjectOperationsBrief_CairnlineMatchesHecateProjectionGraph(t *testing.T) {
	t.Parallel()
	hecateHandler, hecateServer := newProjectWorkProjectionTestServer(t, "memory")
	seedProjectWorkProjectionTest(t, hecateHandler)
	cairnlineHandler, cairnlineServer := newProjectWorkCairnlineReadTestServer()
	seedProjectWorkProjectionTest(t, cairnlineHandler)

	hecate := mustRequestJSON[ProjectOperationsBriefEnvelope](newAPITestClient(t, hecateServer), http.MethodGet, "/hecate/v1/projects/proj_projection/operations/brief", "")
	cairnline := mustRequestJSON[ProjectOperationsBriefEnvelope](newAPITestClient(t, cairnlineServer), http.MethodGet, "/hecate/v1/projects/proj_projection/operations/brief", "")
	if hecate.Data.ReadBackend != "hecate" {
		t.Fatalf("Hecate operations read_backend = %q, want hecate", hecate.Data.ReadBackend)
	}
	if cairnline.Data.ReadBackend != "cairnline" {
		t.Fatalf("Cairnline operations read_backend = %q, want cairnline", cairnline.Data.ReadBackend)
	}

	hecateData := normalizeProjectOperationsBriefForParity(hecate.Data)
	cairnlineData := normalizeProjectOperationsBriefForParity(cairnline.Data)
	if !reflect.DeepEqual(hecateData, cairnlineData) {
		t.Fatalf("operations mismatch\nHecate:   %+v\nCairnline: %+v", hecateData, cairnlineData)
	}
}

func normalizeProjectOperationsBriefForParity(item ProjectOperationsBriefResponse) ProjectOperationsBriefResponse {
	item.GeneratedAt = ""
	item.ReadBackend = ""
	for idx := range item.Items {
		item.Items[idx].UpdatedAt = ""
		if item.Items[idx].Assignment != nil {
			item.Items[idx].Assignment.ReadBackend = ""
			item.Items[idx].Assignment.CreatedAt = ""
			item.Items[idx].Assignment.UpdatedAt = ""
			item.Items[idx].Assignment.StartedAt = ""
			item.Items[idx].Assignment.CompletedAt = ""
			if item.Items[idx].Assignment.Execution != nil {
				item.Items[idx].Assignment.Execution.StartedAt = ""
				item.Items[idx].Assignment.Execution.FinishedAt = ""
			}
		}
		if item.Items[idx].Handoff != nil {
			item.Items[idx].Handoff.ReadBackend = ""
			item.Items[idx].Handoff.CreatedAt = ""
			item.Items[idx].Handoff.UpdatedAt = ""
			item.Items[idx].Handoff.StatusChangedAt = ""
		}
	}
	return item
}

func TestProjectOperationsBrief_SummaryReportsBoundedItems(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	if _, err := handler.projects.Create(t.Context(), projects.Project{
		ID:              "proj_cap",
		Name:            "Cap",
		DefaultProvider: "openai",
		DefaultModel:    "gpt-5",
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	for idx := 0; idx < projectOperationsBriefItemLimit+3; idx++ {
		if _, err := handler.projectWork.CreateWorkItem(t.Context(), projectwork.WorkItem{
			ID:        "work_cap_" + intString(idx),
			ProjectID: "proj_cap",
			Title:     "Prepare work " + intString(idx),
			Status:    projectwork.WorkItemStatusReady,
			UpdatedAt: time.Date(2026, 6, 20, 12, idx, 0, 0, time.UTC),
		}); err != nil {
			t.Fatalf("CreateWorkItem(%d): %v", idx, err)
		}
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_cap/operations/brief", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("operations brief status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectOperationsBriefEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode operations brief: %v", err)
	}
	if response.Data.Summary.ItemLimit != projectOperationsBriefItemLimit {
		t.Fatalf("summary item_limit = %d, want %d", response.Data.Summary.ItemLimit, projectOperationsBriefItemLimit)
	}
	if response.Data.Summary.AvailableItemCount != projectOperationsBriefItemLimit+3 || response.Data.Summary.ItemCount != projectOperationsBriefItemLimit || response.Data.Summary.OmittedItemCount != 3 {
		t.Fatalf("summary = %+v, want available %d visible %d omitted 3", response.Data.Summary, projectOperationsBriefItemLimit+3, projectOperationsBriefItemLimit)
	}
	if len(response.Data.Items) != projectOperationsBriefItemLimit {
		t.Fatalf("items len = %d, want %d", len(response.Data.Items), projectOperationsBriefItemLimit)
	}
}

func TestProjectOperationItemFromActivity_StartQueuedUsesPreflightAction(t *testing.T) {
	t.Parallel()
	item := projectOperationItemFromActivity(ProjectActivityItemResponse{
		ProjectID:       "proj_ops",
		BlockingSignal:  "not_started",
		StatusSummary:   "queued",
		UpdatedAt:       "2026-06-20T12:00:00Z",
		WorkItem:        ProjectActivityWorkItemResponse{ID: "work_ops", Title: "Prepare release"},
		Assignment:      ProjectWorkAssignmentResponse{ID: "asgn_ops", ProjectID: "proj_ops", WorkItemID: "work_ops"},
		ArtifactSummary: ProjectActivityArtifactSummaryResponse{},
	}, "start_queued_assignment", projectOperationsPriorityHigh, "Review queued assignment", "Open launch preflight before starting this assignment.", "Review start")

	if item.Action.Type != projectOperationsActionOpenAssignmentPreflight {
		t.Fatalf("operation action = %+v, want assignment preflight action", item.Action)
	}
	if item.Action.ProjectID != "proj_ops" || item.Action.WorkItemID != "work_ops" || item.Action.AssignmentID != "asgn_ops" || item.Action.ActivityBucket != "blocked" {
		t.Fatalf("operation action = %+v, want project/work/assignment/bucket refs", item.Action)
	}
}

func TestProjectOperationsBrief_SelectedWorkFollowThrough(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	if _, err := handler.projects.Create(t.Context(), projects.Project{
		ID:              "proj_follow",
		Name:            "Follow Through",
		DefaultProvider: "ollama",
		DefaultModel:    "qwen2.5-coder",
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	for _, item := range []projectwork.WorkItem{
		{ID: "work_review_followup", ProjectID: "proj_follow", Title: "Review requested changes", Status: projectwork.WorkItemStatusReview},
		{ID: "work_missing_evidence", ProjectID: "proj_follow", Title: "Record release evidence", Status: projectwork.WorkItemStatusReview},
		{ID: "work_close", ProjectID: "proj_follow", Title: "Ship operator brief", Status: projectwork.WorkItemStatusReview},
	} {
		if _, err := handler.projectWork.CreateWorkItem(t.Context(), item); err != nil {
			t.Fatalf("CreateWorkItem(%s): %v", item.ID, err)
		}
	}
	for _, assignment := range []projectwork.Assignment{
		{ID: "asgn_review_followup", ProjectID: "proj_follow", WorkItemID: "work_review_followup", RoleID: "reviewer", Status: projectwork.AssignmentStatusCompleted},
		{ID: "asgn_missing_evidence", ProjectID: "proj_follow", WorkItemID: "work_missing_evidence", RoleID: "developer", Status: projectwork.AssignmentStatusCompleted},
		{ID: "asgn_close", ProjectID: "proj_follow", WorkItemID: "work_close", RoleID: "developer", Status: projectwork.AssignmentStatusCompleted},
	} {
		if _, err := handler.projectWork.CreateAssignment(t.Context(), assignment); err != nil {
			t.Fatalf("CreateAssignment(%s): %v", assignment.ID, err)
		}
	}
	if _, err := handler.projectWork.CreateArtifact(t.Context(), projectwork.CollaborationArtifact{
		ID:                     "artifact_review_followup",
		ProjectID:              "proj_follow",
		WorkItemID:             "work_review_followup",
		AssignmentID:           "asgn_review_followup",
		Kind:                   "review",
		Title:                  "Architecture review",
		Body:                   "Needs a follow-up assignment.",
		ReviewVerdict:          "changes_requested",
		ReviewFollowUpRequired: true,
	}); err != nil {
		t.Fatalf("CreateArtifact(review): %v", err)
	}
	if _, err := handler.projectWork.CreateArtifact(t.Context(), projectwork.CollaborationArtifact{
		ID:                 "artifact_close_evidence",
		ProjectID:          "proj_follow",
		WorkItemID:         "work_close",
		AssignmentID:       "asgn_close",
		Kind:               "evidence_link",
		Title:              "Release evidence",
		Body:               "Evidence recorded.",
		EvidenceURL:        "https://example.com/evidence",
		EvidenceTrustLabel: "operator_reviewed",
	}); err != nil {
		t.Fatalf("CreateArtifact(evidence): %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_follow/operations/brief", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("operations brief status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectOperationsBriefEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode operations brief: %v", err)
	}
	assertProjectOperationsItemsHaveActions(t, response.Data.Items)

	review := findProjectOperationsItemForTest(t, response.Data.Items, "review_follow_up")
	if review.Target.WorkItemID != "work_review_followup" || review.Action.Type != projectOperationsActionOpenWorkItem || review.Metadata["artifact_id"] != "artifact_review_followup" {
		t.Fatalf("review follow-up item = %+v, want review follow-up work target", review)
	}
	reviewEvidence := findProjectOperationsItemForWorkItemForTest(t, response.Data.Items, "record_completion_evidence", "work_review_followup")
	if reviewEvidence.Target.AssignmentID != "asgn_review_followup" || reviewEvidence.Action.Type != projectOperationsActionOpenWorkItem {
		t.Fatalf("review evidence item = %+v, want review artifact not to satisfy evidence", reviewEvidence)
	}
	evidence := findProjectOperationsItemForWorkItemForTest(t, response.Data.Items, "record_completion_evidence", "work_missing_evidence")
	if evidence.Target.WorkItemID != "work_missing_evidence" || evidence.Target.AssignmentID != "asgn_missing_evidence" || evidence.Action.Type != projectOperationsActionOpenWorkItem {
		t.Fatalf("evidence item = %+v, want missing-evidence work target", evidence)
	}
	closeout := findProjectOperationsItemForTest(t, response.Data.Items, "close_work_item")
	if closeout.Target.WorkItemID != "work_close" || closeout.Status != "ready" || closeout.Action.Type != projectOperationsActionOpenWorkItem || closeout.Metadata["assignment_count"] != "1" {
		t.Fatalf("closeout item = %+v, want ready closeout work target", closeout)
	}
	requireProjectOperationsItemAbsentForTest(t, response.Data.Items, "prepare_first_assignment")
}

func TestProjectWorkItemReadiness_ReadOnlyCloseoutContract(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	if _, err := handler.projects.Create(t.Context(), projects.Project{
		ID:   "proj_readiness",
		Name: "Readiness",
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	if _, err := handler.projectWork.CreateWorkItem(t.Context(), projectwork.WorkItem{
		ID:        "work_readiness",
		ProjectID: "proj_readiness",
		Title:     "Verify closeout",
		Status:    projectwork.WorkItemStatusReview,
	}); err != nil {
		t.Fatalf("CreateWorkItem: %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(t.Context(), projectwork.Assignment{
		ID:         "asgn_complete",
		ProjectID:  "proj_readiness",
		WorkItemID: "work_readiness",
		RoleID:     "developer",
		Status:     projectwork.AssignmentStatusCompleted,
	}); err != nil {
		t.Fatalf("CreateAssignment: %v", err)
	}

	beforeAssignments, err := handler.projectWork.ListAssignments(t.Context(), projectwork.AssignmentFilter{ProjectID: "proj_readiness", WorkItemID: "work_readiness"})
	if err != nil {
		t.Fatalf("ListAssignments before: %v", err)
	}
	beforeArtifacts, err := handler.projectWork.ListArtifacts(t.Context(), projectwork.ArtifactFilter{ProjectID: "proj_readiness", WorkItemID: "work_readiness"})
	if err != nil {
		t.Fatalf("ListArtifacts before: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_readiness/work-items/work_readiness/readiness", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("readiness status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectWorkItemReadinessEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	if response.Object != "project_work_item_readiness" || response.Data.ProjectID != "proj_readiness" || response.Data.WorkItemID != "work_readiness" {
		t.Fatalf("readiness envelope = %+v, want work item readiness", response)
	}
	if response.Data.Ready || response.Data.Status != "blocked" || len(response.Data.MissingEvidenceAssignmentIDs) != 1 || response.Data.MissingEvidenceAssignmentIDs[0] != "asgn_complete" {
		t.Fatalf("readiness = %+v, want missing evidence blocker", response.Data)
	}

	afterAssignments, err := handler.projectWork.ListAssignments(t.Context(), projectwork.AssignmentFilter{ProjectID: "proj_readiness", WorkItemID: "work_readiness"})
	if err != nil {
		t.Fatalf("ListAssignments after: %v", err)
	}
	afterArtifacts, err := handler.projectWork.ListArtifacts(t.Context(), projectwork.ArtifactFilter{ProjectID: "proj_readiness", WorkItemID: "work_readiness"})
	if err != nil {
		t.Fatalf("ListArtifacts after: %v", err)
	}
	if len(afterAssignments) != len(beforeAssignments) || len(afterArtifacts) != len(beforeArtifacts) {
		t.Fatalf("readiness mutated project state: assignments %d->%d artifacts %d->%d", len(beforeAssignments), len(afterAssignments), len(beforeArtifacts), len(afterArtifacts))
	}

	if _, err := handler.projectWork.CreateArtifact(t.Context(), projectwork.CollaborationArtifact{
		ID:                 "artifact_evidence",
		ProjectID:          "proj_readiness",
		WorkItemID:         "work_readiness",
		AssignmentID:       "asgn_complete",
		Kind:               projectwork.ArtifactKindEvidenceLink,
		Title:              "Evidence",
		Body:               "Evidence recorded.",
		EvidenceURL:        "https://example.com/evidence",
		EvidenceTrustLabel: "operator_reviewed",
	}); err != nil {
		t.Fatalf("CreateArtifact(evidence): %v", err)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_readiness/work-items/work_readiness/readiness", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("readiness with evidence status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode readiness with evidence: %v", err)
	}
	if !response.Data.Ready || response.Data.Status != "ready" || response.Data.CompletedAssignments != 1 || response.Data.AssignmentCount != 1 {
		t.Fatalf("readiness with evidence = %+v, want ready closeout", response.Data)
	}
}

func TestProjectWorkItemReadiness_ReviewFollowUpsAreTyped(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	if _, err := handler.projects.Create(t.Context(), projects.Project{
		ID:   "proj_review_readiness",
		Name: "Review Readiness",
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	if _, err := handler.projectWork.CreateWorkItem(t.Context(), projectwork.WorkItem{
		ID:        "work_review_readiness",
		ProjectID: "proj_review_readiness",
		Title:     "Review closeout",
		Status:    projectwork.WorkItemStatusReview,
	}); err != nil {
		t.Fatalf("CreateWorkItem: %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(t.Context(), projectwork.Assignment{
		ID:         "asgn_impl",
		ProjectID:  "proj_review_readiness",
		WorkItemID: "work_review_readiness",
		RoleID:     "software_developer",
		Status:     projectwork.AssignmentStatusCompleted,
	}); err != nil {
		t.Fatalf("CreateAssignment: %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(t.Context(), projectwork.Assignment{
		ID:         "asgn_review",
		ProjectID:  "proj_review_readiness",
		WorkItemID: "work_review_readiness",
		RoleID:     "reviewer_qa",
		Status:     projectwork.AssignmentStatusCompleted,
	}); err != nil {
		t.Fatalf("CreateAssignment(review): %v", err)
	}
	if _, err := handler.projectWork.CreateArtifact(t.Context(), projectwork.CollaborationArtifact{
		ID:                     "artifact_review",
		ProjectID:              "proj_review_readiness",
		WorkItemID:             "work_review_readiness",
		AssignmentID:           "asgn_review",
		Kind:                   projectwork.ArtifactKindReview,
		Title:                  "Architecture review",
		Body:                   "Verdict: Changes requested.",
		ReviewedAssignmentID:   "asgn_impl",
		ReviewVerdict:          projectwork.ReviewVerdictChangesRequested,
		ReviewRisk:             projectwork.ReviewRiskHigh,
		ReviewFollowUpRequired: true,
	}); err != nil {
		t.Fatalf("CreateArtifact(review): %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_review_readiness/work-items/work_review_readiness/readiness", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("readiness status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectWorkItemReadinessEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	if len(response.Data.ReviewFollowUpArtifactIDs) != 1 || response.Data.ReviewFollowUpArtifactIDs[0] != "artifact_review" {
		t.Fatalf("review follow-up ids = %+v, want artifact_review", response.Data.ReviewFollowUpArtifactIDs)
	}
	if len(response.Data.ReviewFollowUps) != 1 {
		t.Fatalf("review follow-ups = %+v, want one typed follow-up", response.Data.ReviewFollowUps)
	}
	followUp := response.Data.ReviewFollowUps[0]
	if followUp.ArtifactID != "artifact_review" || followUp.Status != "needs_path" || followUp.Title != "Architecture review" || followUp.ReviewedAssignmentID != "asgn_impl" || followUp.ReviewVerdict != projectwork.ReviewVerdictChangesRequested || followUp.ReviewRisk != projectwork.ReviewRiskHigh || followUp.Blocker == "" {
		t.Fatalf("review follow-up = %+v, want typed missing-path record", followUp)
	}
}

func TestProjectOperationsBrief_OpenLatestWorkWhenNoOperations(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	if _, err := handler.projects.Create(t.Context(), projects.Project{
		ID:              "proj_latest",
		Name:            "Latest Work",
		DefaultProvider: "ollama",
		DefaultModel:    "qwen2.5-coder",
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	older := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 6, 20, 11, 0, 0, 0, time.UTC)
	for _, item := range []projectwork.WorkItem{
		{ID: "work_older", ProjectID: "proj_latest", Title: "Older work", Status: projectwork.WorkItemStatusDone, CreatedAt: older, UpdatedAt: older},
		{ID: "work_newer", ProjectID: "proj_latest", Title: "Newer work", Status: projectwork.WorkItemStatusDone, CreatedAt: newer, UpdatedAt: newer},
	} {
		if _, err := handler.projectWork.CreateWorkItem(t.Context(), item); err != nil {
			t.Fatalf("CreateWorkItem(%s): %v", item.ID, err)
		}
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_latest/operations/brief", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("operations brief status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectOperationsBriefEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode operations brief: %v", err)
	}
	if len(response.Data.Items) != 1 {
		t.Fatalf("operations items = %+v, want only latest work fallback", response.Data.Items)
	}
	item := response.Data.Items[0]
	if item.Kind != "open_latest_work" || item.Target.WorkItemID != "work_newer" || item.Action.Type != projectOperationsActionOpenWorkItem {
		t.Fatalf("latest work item = %+v, want newest work target", item)
	}
}

func TestProjectOperationsReviewFollowUpPathAndBlockerSemantics(t *testing.T) {
	t.Parallel()
	artifact := projectwork.CollaborationArtifact{
		ID:                     "artifact_review",
		Kind:                   projectwork.ArtifactKindReview,
		ReviewVerdict:          projectwork.ReviewVerdictChangesRequested,
		ReviewFollowUpRequired: true,
	}

	acceptedWithoutTarget := []projectwork.Handoff{{
		ID:                "handoff_accepted",
		Status:            projectwork.HandoffStatusAccepted,
		LinkedArtifactIDs: []string{artifact.ID},
	}}
	if projectworkapp.ReviewArtifactHasLinkedFollowUpPath(artifact.ID, acceptedWithoutTarget) {
		t.Fatal("accepted handoff without target assignment should not hide review follow-up operation")
	}
	if projectworkapp.ReviewFollowUpBlocker(artifact, acceptedWithoutTarget, nil) == "" {
		t.Fatal("accepted handoff without target assignment should still block closeout")
	}

	pending := []projectwork.Handoff{{
		ID:                "handoff_pending",
		Status:            projectwork.HandoffStatusPending,
		LinkedArtifactIDs: []string{artifact.ID},
	}}
	if !projectworkapp.ReviewArtifactHasLinkedFollowUpPath(artifact.ID, pending) || projectworkapp.ReviewFollowUpBlocker(artifact, pending, nil) == "" {
		t.Fatal("pending handoff should route through handoff review and block closeout")
	}

	dismissed := []projectwork.Handoff{{
		ID:                "handoff_dismissed",
		Status:            projectwork.HandoffStatusDismissed,
		LinkedArtifactIDs: []string{artifact.ID},
	}}
	if !projectworkapp.ReviewArtifactHasLinkedFollowUpPath(artifact.ID, dismissed) || projectworkapp.ReviewFollowUpBlocker(artifact, dismissed, nil) != "" {
		t.Fatal("dismissed handoff should clear review follow-up without blocking closeout")
	}

	completedTarget := []projectwork.Handoff{{
		ID:                 "handoff_completed",
		Status:             projectwork.HandoffStatusAccepted,
		TargetAssignmentID: "asgn_followup",
		LinkedArtifactIDs:  []string{artifact.ID},
	}}
	assignmentsByID := map[string]projectwork.Assignment{
		"asgn_followup": {ID: "asgn_followup", Status: projectwork.AssignmentStatusCompleted},
	}
	if !projectworkapp.ReviewArtifactHasLinkedFollowUpPath(artifact.ID, completedTarget) || projectworkapp.ReviewFollowUpBlocker(artifact, completedTarget, assignmentsByID) != "" {
		t.Fatal("completed target assignment should clear review follow-up closeout blocker")
	}
}

func TestSortProjectOperationsItems_UsesExplicitUrgencyBeforeRecency(t *testing.T) {
	t.Parallel()
	items := []ProjectOperationsBriefItemResponse{
		{
			ID:        "memory",
			Kind:      "review_memory_candidates",
			Priority:  projectOperationsPriorityMedium,
			UpdatedAt: "2026-06-20T12:04:00Z",
		},
		{
			ID:        "defaults",
			Kind:      "configure_project_defaults",
			Priority:  projectOperationsPriorityHigh,
			UpdatedAt: "2026-06-20T12:05:00Z",
		},
		{
			ID:        "handoff",
			Kind:      "review_pending_handoff",
			Priority:  projectOperationsPriorityMedium,
			UpdatedAt: "2026-06-20T12:00:00Z",
		},
		{
			ID:        "active",
			Kind:      "inspect_active_assignment",
			Priority:  projectOperationsPriorityLow,
			UpdatedAt: "2026-06-20T12:06:00Z",
		},
		{
			ID:        "approval",
			Kind:      "approve_assignment",
			Priority:  projectOperationsPriorityHigh,
			UpdatedAt: "2026-06-20T12:01:00Z",
		},
	}

	sortProjectOperationsItems(items)
	got := make([]string, 0, len(items))
	for _, item := range boundedProjectOperationsItems(items, 3) {
		got = append(got, item.Kind)
	}
	want := []string{"approve_assignment", "configure_project_defaults", "review_pending_handoff"}
	if len(got) != len(want) {
		t.Fatalf("bounded sorted operations = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("bounded sorted operations = %v, want %v", got, want)
		}
	}
}

func assertProjectOperationsItemsHaveActions(t *testing.T, items []ProjectOperationsBriefItemResponse) {
	t.Helper()
	if len(items) == 0 {
		t.Fatal("expected operation items")
	}
	for _, item := range items {
		if item.Action.Type == "" || item.Action.ProjectID == "" {
			t.Fatalf("operation item %q has incomplete action: %+v", item.Kind, item.Action)
		}
		if item.Action.Type == projectOperationsActionDraftProjectProposal && item.Action.Request == "" {
			t.Fatalf("draft operation item %q has empty action request: %+v", item.Kind, item.Action)
		}
	}
}

func findProjectOperationsItemForTest(t *testing.T, items []ProjectOperationsBriefItemResponse, kind string) ProjectOperationsBriefItemResponse {
	t.Helper()
	for _, item := range items {
		if item.Kind == kind {
			return item
		}
	}
	t.Fatalf("operations item kind %q missing from %+v", kind, items)
	return ProjectOperationsBriefItemResponse{}
}

func findProjectOperationsItemForWorkItemForTest(t *testing.T, items []ProjectOperationsBriefItemResponse, kind, workItemID string) ProjectOperationsBriefItemResponse {
	t.Helper()
	for _, item := range items {
		if item.Kind == kind && item.Target.WorkItemID == workItemID {
			return item
		}
	}
	t.Fatalf("operations item kind %q for work item %q missing from %+v", kind, workItemID, items)
	return ProjectOperationsBriefItemResponse{}
}

func requireProjectOperationsItemAbsentForTest(t *testing.T, items []ProjectOperationsBriefItemResponse, kind string) {
	t.Helper()
	for _, item := range items {
		if item.Kind == kind {
			t.Fatalf("operations item kind %q unexpectedly present in %+v", kind, items)
		}
	}
}
