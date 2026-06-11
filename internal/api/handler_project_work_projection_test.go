package api

import (
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/projectworkapp"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestProjectWorkProjection_StatusPrefersStoredTerminalUntilRunIsNewer(t *testing.T) {
	t.Parallel()

	storedAt := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	assignment := projectwork.Assignment{
		ID:        "asgn_terminal",
		Status:    projectwork.AssignmentStatusFailed,
		UpdatedAt: storedAt,
	}

	if got := projectworkapp.ProjectedAssignmentStatus(assignment, projectwork.AssignmentStatusCompleted, storedAt); got != projectwork.AssignmentStatusFailed {
		t.Fatalf("status with same projection timestamp = %q, want stored failed", got)
	}
	if got := projectworkapp.ProjectedAssignmentStatus(assignment, projectwork.AssignmentStatusCompleted, storedAt.Add(-time.Second)); got != projectwork.AssignmentStatusFailed {
		t.Fatalf("status with older projection timestamp = %q, want stored failed", got)
	}
	if got := projectworkapp.ProjectedAssignmentStatus(assignment, projectwork.AssignmentStatusCompleted, storedAt.Add(time.Second)); got != projectwork.AssignmentStatusCompleted {
		t.Fatalf("status with newer projection timestamp = %q, want projected completed", got)
	}
}

func TestProjectWorkProjection_AssignmentExecutionHydratesAwaitingRun(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	store := taskstate.NewMemoryStore()
	handler := &Handler{taskStore: store}
	startedAt := time.Date(2026, 6, 4, 13, 0, 0, 0, time.UTC)
	assignment := projectwork.Assignment{
		ID:        "asgn_awaiting",
		Status:    projectwork.AssignmentStatusQueued,
		TaskID:    "task_awaiting",
		RunID:     "run_awaiting",
		UpdatedAt: startedAt.Add(-time.Minute),
	}

	if _, err := store.CreateTask(ctx, types.Task{
		ID:          assignment.TaskID,
		Status:      "awaiting_approval",
		LatestRunID: assignment.RunID,
		CreatedAt:   startedAt,
		UpdatedAt:   startedAt,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.CreateRun(ctx, types.TaskRun{
		ID:            assignment.RunID,
		TaskID:        assignment.TaskID,
		Number:        1,
		Status:        "awaiting_approval",
		Model:         "qwen2.5-coder",
		Provider:      "ollama",
		StepCount:     2,
		ApprovalCount: 2,
		ArtifactCount: 1,
		StartedAt:     startedAt,
		TraceID:       "trace_awaiting",
	}); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	for _, approval := range []types.TaskApproval{
		{ID: "ap_pending", TaskID: assignment.TaskID, RunID: assignment.RunID, Status: "pending", CreatedAt: startedAt},
		{ID: "ap_approved", TaskID: assignment.TaskID, RunID: assignment.RunID, Status: "approved", CreatedAt: startedAt},
		{ID: "ap_other_run", TaskID: assignment.TaskID, RunID: "run_other", Status: "pending", CreatedAt: startedAt},
	} {
		if _, err := store.CreateApproval(ctx, approval); err != nil {
			t.Fatalf("CreateApproval(%s): %v", approval.ID, err)
		}
	}

	response, err := handler.renderProjectedProjectWorkAssignment(ctx, assignment)
	if err != nil {
		t.Fatalf("renderProjectedProjectWorkAssignment: %v", err)
	}
	if response.Status != projectwork.AssignmentStatusAwaitingApproval {
		t.Fatalf("response status = %q, want awaiting approval", response.Status)
	}
	if response.StartedAt == "" {
		t.Fatalf("response started_at is empty, want projected run timestamp")
	}
	if response.Execution == nil {
		t.Fatalf("response execution is nil, want hydrated execution")
	}
	execution := response.Execution
	if execution.TaskID != assignment.TaskID || execution.RunID != assignment.RunID {
		t.Fatalf("execution ids = %q/%q, want linked task/run", execution.TaskID, execution.RunID)
	}
	if execution.TaskStatus != "awaiting_approval" || execution.RunStatus != "awaiting_approval" || execution.Status != projectwork.AssignmentStatusAwaitingApproval {
		t.Fatalf("execution statuses = task %q run %q assignment %q, want awaiting approval", execution.TaskStatus, execution.RunStatus, execution.Status)
	}
	if execution.PendingApprovalCount != 1 {
		t.Fatalf("pending approvals = %d, want only the pending approval for this run", execution.PendingApprovalCount)
	}
	if execution.StepCount != 2 || execution.ApprovalCount != 2 || execution.ArtifactCount != 1 {
		t.Fatalf("execution counts = steps %d approvals %d artifacts %d, want 2/2/1", execution.StepCount, execution.ApprovalCount, execution.ArtifactCount)
	}
	if execution.Model != "qwen2.5-coder" || execution.Provider != "ollama" || execution.TraceID != "trace_awaiting" {
		t.Fatalf("execution route/trace = model %q provider %q trace %q, want run metadata", execution.Model, execution.Provider, execution.TraceID)
	}
}
