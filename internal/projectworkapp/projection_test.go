package projectworkapp

import (
	"context"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestProjectAssignmentExecution_ProjectsRunAndCanonicalRef(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	startedAt := time.Date(2026, 6, 11, 10, 0, 0, 0, time.UTC)
	if _, err := store.CreateTask(ctx, types.Task{ID: "task_1", Status: "running", LatestRunID: "run_1"}); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if _, err := store.CreateRun(ctx, types.TaskRun{
		ID:            "run_1",
		TaskID:        "task_1",
		Status:        "awaiting_approval",
		Model:         "gpt-4o-mini",
		Provider:      "openai",
		StepCount:     3,
		ApprovalCount: 1,
		ArtifactCount: 2,
		StartedAt:     startedAt,
		TraceID:       "trace_1",
	}); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	if _, err := store.CreateApproval(ctx, types.TaskApproval{ID: "approval_1", TaskID: "task_1", RunID: "run_1", Status: "pending"}); err != nil {
		t.Fatalf("CreateApproval() error = %v", err)
	}

	assignment := projectwork.Assignment{
		ID: "asgn_1",
		ExecutionRef: projectwork.AssignmentExecutionRef{
			Kind:   projectwork.AssignmentExecutionKindTaskRun,
			TaskID: "task_1",
			RunID:  "run_1",
		},
		Status:    projectwork.AssignmentStatusQueued,
		UpdatedAt: startedAt.Add(-time.Minute),
	}
	projection, err := ProjectAssignmentExecution(ctx, store, assignment)
	if err != nil {
		t.Fatalf("ProjectAssignmentExecution() error = %v", err)
	}
	if projection == nil {
		t.Fatal("ProjectAssignmentExecution() = nil, want projection")
	}
	if projection.Status != projectwork.AssignmentStatusAwaitingApproval || projection.Execution.PendingApprovalCount != 1 {
		t.Fatalf("projection = %+v, want awaiting approval with pending count", projection)
	}
	if projection.Execution.StepCount != 3 || projection.Execution.ArtifactCount != 2 || projection.Execution.TraceID != "trace_1" {
		t.Fatalf("execution summary = %+v, want run metadata copied", projection.Execution)
	}
	ref := AssignmentExecutionRefFor(assignment, &projection.Execution, projection.Status)
	if ref == nil || ref.Kind != AssignmentExecutionKindTaskRun || ref.TaskID != "task_1" || ref.RunID != "run_1" || ref.Status != projectwork.AssignmentStatusAwaitingApproval || ref.PendingApprovalCount != 1 || ref.TraceID != "trace_1" {
		t.Fatalf("execution ref = %+v, want canonical task-run ref", ref)
	}
}

func TestProjectAssignmentExecution_MissingTaskMarksExecutionMissing(t *testing.T) {
	t.Parallel()

	projection, err := ProjectAssignmentExecution(context.Background(), taskstate.NewMemoryStore(), projectwork.Assignment{
		ID:           "asgn_1",
		ExecutionRef: projectwork.AssignmentExecutionRef{Kind: projectwork.AssignmentExecutionKindTaskRun, TaskID: "task_missing"},
		Status:       projectwork.AssignmentStatusQueued,
	})
	if err != nil {
		t.Fatalf("ProjectAssignmentExecution() error = %v", err)
	}
	if projection == nil || !projection.Execution.Missing || projection.Execution.TaskID != "task_missing" {
		t.Fatalf("projection = %+v, want missing execution summary", projection)
	}
	ref := AssignmentExecutionRefFor(projectwork.Assignment{ExecutionRef: projectwork.AssignmentExecutionRef{TaskID: "task_missing"}}, &projection.Execution, projection.Status)
	if ref == nil || !ref.Missing || ref.Kind != AssignmentExecutionKindTaskRun {
		t.Fatalf("execution ref = %+v, want missing task-run ref", ref)
	}
}

func TestAssignmentExecutionRefFor_RawChatAndContextLinks(t *testing.T) {
	t.Parallel()

	chatRef := AssignmentExecutionRefFor(projectwork.Assignment{
		ExecutionRef: projectwork.AssignmentExecutionRef{
			Kind:          projectwork.AssignmentExecutionKindChatSession,
			ChatSessionID: "chat_1",
			MessageID:     "msg_1",
		},
		Status: projectwork.AssignmentStatusRunning,
	}, nil, projectwork.AssignmentStatusRunning)
	if chatRef == nil || chatRef.Kind != AssignmentExecutionKindChatSession || chatRef.ChatSessionID != "chat_1" || chatRef.MessageID != "msg_1" {
		t.Fatalf("chat ref = %+v, want chat-session ref", chatRef)
	}
	contextRef := AssignmentExecutionRefFor(projectwork.Assignment{
		ExecutionRef: projectwork.AssignmentExecutionRef{
			Kind:              projectwork.AssignmentExecutionKindContextSnapshot,
			ContextSnapshotID: "ctx_1",
		},
		Status: projectwork.AssignmentStatusQueued,
	}, nil, projectwork.AssignmentStatusQueued)
	if contextRef == nil || contextRef.Kind != AssignmentExecutionKindContextSnapshot || contextRef.ContextSnapshotID != "ctx_1" {
		t.Fatalf("context ref = %+v, want context-snapshot ref", contextRef)
	}
}

func TestProjectedAssignmentStatus_ManualTerminalWinsUntilRunIsNewer(t *testing.T) {
	t.Parallel()

	assignment := projectwork.Assignment{
		Status:    projectwork.AssignmentStatusFailed,
		UpdatedAt: time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC),
	}
	if got := ProjectedAssignmentStatus(assignment, projectwork.AssignmentStatusCompleted, assignment.UpdatedAt.Add(-time.Minute)); got != projectwork.AssignmentStatusFailed {
		t.Fatalf("ProjectedAssignmentStatus(stale run) = %q, want manual failed", got)
	}
	if got := ProjectedAssignmentStatus(assignment, projectwork.AssignmentStatusCompleted, assignment.UpdatedAt.Add(time.Minute)); got != projectwork.AssignmentStatusCompleted {
		t.Fatalf("ProjectedAssignmentStatus(new run) = %q, want completed", got)
	}
}
