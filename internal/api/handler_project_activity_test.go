package api

import (
	"reflect"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/projectwork"
)

func TestProjectActivityProjection_ArtifactSignalsSortLimitAndGroup(t *testing.T) {
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	artifacts := []projectwork.CollaborationArtifact{
		{ID: "artifact-1", ProjectID: "proj", WorkItemID: "work", AssignmentID: "asgn-old", Kind: "note", Title: "Old", CreatedAt: base, UpdatedAt: base.Add(time.Minute)},
		{ID: "artifact-4", ProjectID: "proj", WorkItemID: "work", AssignmentID: "asgn-latest", Kind: "patch", Title: "Latest patch", CreatedAt: base, UpdatedAt: base.Add(4 * time.Minute)},
		{ID: "artifact-2", ProjectID: "proj", WorkItemID: "work-loose", Kind: "brief", Title: "Loose", CreatedAt: base, UpdatedAt: base.Add(2 * time.Minute)},
		{ID: "artifact-3", ProjectID: "proj", WorkItemID: "work", AssignmentID: "asgn-mid", Kind: "summary", Title: "Middle", CreatedAt: base, UpdatedAt: base.Add(3 * time.Minute)},
	}

	summary, recent := renderProjectActivityArtifactSignals(artifacts)
	if summary.Count != 4 || summary.LatestKind != "patch" || summary.LatestTitle != "Latest patch" || summary.LatestAt != formatOptionalTime(base.Add(4*time.Minute)) || summary.AssignmentID != "asgn-latest" {
		t.Fatalf("artifact summary = %+v, want latest artifact metadata and total count", summary)
	}
	if got, want := projectWorkArtifactIDsForTest(recent), []string{"artifact-4", "artifact-3", "artifact-2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("recent artifact ids = %v, want %v", got, want)
	}

	byAssignment, byWorkItem := groupProjectActivityArtifacts(artifacts)
	if got := len(byAssignment["asgn-latest"]); got != 1 {
		t.Fatalf("assignment artifacts = %d, want 1", got)
	}
	if got := len(byWorkItem["work-loose"]); got != 1 {
		t.Fatalf("work-item artifacts = %d, want 1", got)
	}
	if _, ok := byWorkItem["work"]; ok {
		t.Fatalf("assigned artifacts also grouped by work item")
	}
}

func TestProjectActivityProjection_HandoffSignalsSortLimitAndGroup(t *testing.T) {
	base := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	handoffs := []projectwork.Handoff{
		{ID: "handoff-old", ProjectID: "proj", WorkItemID: "work", SourceAssignmentID: "asgn-source", Title: "Old", Status: projectwork.HandoffStatusPending, CreatedAt: base, UpdatedAt: base.Add(time.Minute)},
		{ID: "handoff-latest", ProjectID: "proj", WorkItemID: "work", SourceAssignmentID: "asgn-source", TargetAssignmentID: "asgn-target", TargetRoleID: "role-review", TargetWorkItemID: "work-next", Title: "Latest", Status: projectwork.HandoffStatusAccepted, CreatedAt: base, UpdatedAt: base.Add(4 * time.Minute)},
		{ID: "handoff-target", ProjectID: "proj", WorkItemID: "work", TargetAssignmentID: "asgn-target", Title: "Target", Status: projectwork.HandoffStatusPending, CreatedAt: base, UpdatedAt: base.Add(3 * time.Minute)},
		{ID: "handoff-work", ProjectID: "proj", WorkItemID: "work-loose", Title: "Loose", Status: "declined", CreatedAt: base, UpdatedAt: base.Add(2 * time.Minute)},
	}

	summary, recent := renderProjectActivityHandoffSignals(handoffs)
	if summary.Count != 4 || summary.PendingCount != 2 || summary.AcceptedCount != 1 || summary.LatestTitle != "Latest" || summary.TargetRoleID != "role-review" || summary.TargetWorkItem != "work-next" {
		t.Fatalf("handoff summary = %+v, want latest metadata and status counts", summary)
	}
	if got, want := projectHandoffIDsForTest(recent), []string{"handoff-latest", "handoff-target", "handoff-work"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("recent handoff ids = %v, want %v", got, want)
	}

	byAssignment, byWorkItem := groupProjectActivityHandoffs(handoffs)
	if got := projectWorkHandoffIDsForTest(byAssignment["asgn-source"]); !reflect.DeepEqual(got, []string{"handoff-old", "handoff-latest"}) {
		t.Fatalf("source assignment handoffs = %v, want old+latest", got)
	}
	if got := projectWorkHandoffIDsForTest(byAssignment["asgn-target"]); !reflect.DeepEqual(got, []string{"handoff-latest", "handoff-target"}) {
		t.Fatalf("target assignment handoffs = %v, want latest+target", got)
	}
	if got := projectWorkHandoffIDsForTest(byWorkItem["work-loose"]); !reflect.DeepEqual(got, []string{"handoff-work"}) {
		t.Fatalf("work-item handoffs = %v, want loose", got)
	}
}

func TestProjectActivityProjection_ItemStatusSummaryAndBucket(t *testing.T) {
	base := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	workItem := ProjectWorkItemResponse{
		ID:        "work-1",
		ProjectID: "proj",
		Title:     "Implement projection",
		Status:    projectwork.WorkItemStatusRunning,
		Priority:  "high",
		UpdatedAt: formatOptionalTime(base.Add(2 * time.Minute)),
	}
	role := projectwork.AgentRoleProfile{
		ID:        "role-1",
		ProjectID: "proj",
		Name:      "Projection engineer",
	}
	assignment := ProjectWorkAssignmentResponse{
		ID:         "asgn-awaiting",
		ProjectID:  "proj",
		WorkItemID: "work-1",
		RoleID:     "role-1",
		Status:     projectwork.AssignmentStatusAwaitingApproval,
		TaskID:     "task-stored",
		RunID:      "run-stored",
		Execution: &ProjectWorkAssignmentExecutionResponse{
			TaskID:               "task-projected",
			RunID:                "run-projected",
			Status:               projectwork.AssignmentStatusAwaitingApproval,
			PendingApprovalCount: 2,
		},
		UpdatedAt: formatOptionalTime(base.Add(time.Minute)),
	}
	artifacts := []projectwork.CollaborationArtifact{
		{ID: "artifact-latest", ProjectID: "proj", WorkItemID: "work-1", AssignmentID: "asgn-awaiting", Kind: "patch", Title: "Patch", UpdatedAt: base.Add(5 * time.Minute)},
	}

	item := renderProjectActivityItem(workItem, assignment, role, artifacts, nil, nil)
	if item.Status != projectwork.AssignmentStatusAwaitingApproval || item.BlockingSignal != "awaiting_approval" || item.StatusSummary != "2 approval pending" {
		t.Fatalf("awaiting item = %+v, want approval signal and summary", item)
	}
	if item.LinkedTaskID != "task-projected" || item.LinkedRunID != "run-projected" {
		t.Fatalf("linked runtime ids = task %q run %q, want projected ids", item.LinkedTaskID, item.LinkedRunID)
	}
	if item.UpdatedAt != formatOptionalTime(base.Add(5*time.Minute)) {
		t.Fatalf("updated_at = %q, want latest artifact timestamp", item.UpdatedAt)
	}
	if got := projectActivityBucket(item); got != "blocked" {
		t.Fatalf("bucket = %q, want blocked", got)
	}

	completed := renderProjectActivityItem(workItem, ProjectWorkAssignmentResponse{
		ID:         "asgn-completed",
		ProjectID:  "proj",
		WorkItemID: "work-1",
		RoleID:     "role-1",
		Status:     projectwork.AssignmentStatusCompleted,
		Execution: &ProjectWorkAssignmentExecutionResponse{
			Status: projectwork.AssignmentStatusCompleted,
		},
	}, role, []projectwork.CollaborationArtifact{
		{ID: "artifact-1", ProjectID: "proj", WorkItemID: "work-1", AssignmentID: "asgn-completed", UpdatedAt: base.Add(time.Minute)},
		{ID: "artifact-2", ProjectID: "proj", WorkItemID: "work-1", AssignmentID: "asgn-completed", UpdatedAt: base.Add(2 * time.Minute)},
	}, nil, nil)
	if completed.BlockingSignal != "completed" || completed.StatusSummary != "completed with 2 artifacts" || projectActivityBucket(completed) != "completed" {
		t.Fatalf("completed item = %+v, want completed artifact summary", completed)
	}

	missingChat := renderProjectActivityItem(workItem, ProjectWorkAssignmentResponse{
		ID:            "asgn-chat",
		ProjectID:     "proj",
		WorkItemID:    "work-1",
		RoleID:        "role-1",
		Status:        projectwork.AssignmentStatusRunning,
		ChatSessionID: "chat-missing",
	}, role, nil, nil, missingProjectActivityLinkedChat("chat-missing"))
	if missingChat.BlockingSignal != "stale_unknown" || missingChat.StatusSummary != "linked chat missing" || projectActivityBucket(missingChat) != "blocked" {
		t.Fatalf("missing chat item = %+v, want stale linked-chat signal", missingChat)
	}
}

func projectWorkArtifactIDsForTest(items []ProjectWorkArtifactResponse) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func projectHandoffIDsForTest(items []ProjectHandoffResponse) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func projectWorkHandoffIDsForTest(items []projectwork.Handoff) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}
