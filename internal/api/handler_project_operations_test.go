package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectwork"
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
	if response.Data.Summary.PendingMemoryCandidateCount != 1 || response.Data.Summary.PendingHandoffCount != 1 {
		t.Fatalf("operations summary = %+v, want memory candidate and handoff counts", response.Data.Summary)
	}

	defaults := findProjectOperationsItemForTest(t, response.Data.Items, "configure_project_defaults")
	if defaults.Target.Surface != "project_settings" || defaults.ActionLabel != "Project settings" {
		t.Fatalf("defaults item = %+v, want project settings target", defaults)
	}
	approval := findProjectOperationsItemForTest(t, response.Data.Items, "approve_assignment")
	if approval.Target.WorkItemID != "work_review" || approval.Target.AssignmentID != "asgn_approval" || approval.Target.ActivityBucket != "blocked" {
		t.Fatalf("approval item = %+v, want blocked assignment target", approval)
	}
	handoff := findProjectOperationsItemForTest(t, response.Data.Items, "review_pending_handoff")
	if handoff.Handoff == nil || handoff.Handoff.ID != "handoff_review" || handoff.Target.HandoffID != "handoff_review" {
		t.Fatalf("handoff item = %+v, want rendered handoff target", handoff)
	}
	memoryItem := findProjectOperationsItemForTest(t, response.Data.Items, "review_memory_candidates")
	if memoryItem.Target.Surface != "memory" {
		t.Fatalf("memory item = %+v, want memory target", memoryItem)
	}
	assignmentGap := findProjectOperationsItemForTest(t, response.Data.Items, "prepare_first_assignment")
	if assignmentGap.Target.WorkItemID != "work_empty" || assignmentGap.DraftRequest == "" {
		t.Fatalf("assignment gap item = %+v, want draft request for empty work item", assignmentGap)
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
