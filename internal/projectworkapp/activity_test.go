package projectworkapp

import (
	"testing"

	"github.com/hecatehq/hecate/internal/projectwork"
)

func TestProjectActivityAssignmentState_AwaitingApprovalUsesCanonicalRef(t *testing.T) {
	t.Parallel()

	state := ProjectActivityAssignmentState(ActivityAssignmentInput{
		Status: projectwork.AssignmentStatusQueued,
		Execution: &AssignmentExecutionSummary{
			Status:               projectwork.AssignmentStatusAwaitingApproval,
			PendingApprovalCount: 1,
		},
		ExecutionRef: &AssignmentExecutionRef{
			Kind:                 AssignmentExecutionKindTaskRun,
			TaskID:               "task_1",
			RunID:                "run_1",
			Status:               projectwork.AssignmentStatusAwaitingApproval,
			PendingApprovalCount: 2,
		},
	})
	if state.Status != projectwork.AssignmentStatusAwaitingApproval || state.BlockingSignal != "awaiting_approval" || state.Bucket != "blocked" {
		t.Fatalf("state = %+v, want awaiting approval blocked state", state)
	}
	if state.StatusSummary != "2 approval pending" || state.LinkedTaskID != "task_1" || state.LinkedRunID != "run_1" {
		t.Fatalf("state = %+v, want ref-backed links and approval summary", state)
	}
}

func TestProjectActivityAssignmentState_MissingRuntimeAndMissingChatAreStale(t *testing.T) {
	t.Parallel()

	missingRun := ProjectActivityAssignmentState(ActivityAssignmentInput{
		Status: projectwork.AssignmentStatusRunning,
		Execution: &AssignmentExecutionSummary{
			Missing: true,
		},
		ExecutionRef: &AssignmentExecutionRef{
			Kind:    AssignmentExecutionKindTaskRun,
			TaskID:  "task_missing",
			RunID:   "run_missing",
			Missing: true,
		},
	})
	if missingRun.BlockingSignal != "stale_unknown" || missingRun.Bucket != "blocked" || missingRun.LinkedTaskID != "task_missing" {
		t.Fatalf("missing run state = %+v, want stale blocked state with canonical task link", missingRun)
	}

	missingChat := ProjectActivityAssignmentState(ActivityAssignmentInput{
		Status: projectwork.AssignmentStatusRunning,
		ExecutionRef: &AssignmentExecutionRef{
			Kind:          AssignmentExecutionKindChatSession,
			ChatSessionID: "chat_missing",
		},
		LinkedChat: &ActivityLinkedChat{ID: "chat_missing", Missing: true},
	})
	if missingChat.BlockingSignal != "stale_unknown" || missingChat.StatusSummary != "linked chat missing" || missingChat.LinkedChatID != "chat_missing" {
		t.Fatalf("missing chat state = %+v, want stale linked-chat state", missingChat)
	}
}

func TestProjectActivityAssignmentState_LinkedChatAndCompletedSummaries(t *testing.T) {
	t.Parallel()

	chatState := ProjectActivityAssignmentState(ActivityAssignmentInput{
		Status: projectwork.AssignmentStatusRunning,
		ExecutionRef: &AssignmentExecutionRef{
			Kind:          AssignmentExecutionKindChatSession,
			ChatSessionID: "chat_1",
			Status:        projectwork.AssignmentStatusRunning,
		},
		LinkedChat: &ActivityLinkedChat{
			ID:           "chat_1",
			Status:       "running",
			LatestRole:   "assistant",
			LatestStatus: "completed",
			MessageCount: 2,
		},
	})
	if chatState.BlockingSignal != "running" || chatState.StatusSummary != "linked chat · running · assistant completed · 2 messages" || chatState.Bucket != "active" {
		t.Fatalf("chat state = %+v, want active linked-chat summary", chatState)
	}

	completed := ProjectActivityAssignmentState(ActivityAssignmentInput{
		Status:        projectwork.AssignmentStatusCompleted,
		ArtifactCount: 2,
		ExecutionRef: &AssignmentExecutionRef{
			Kind:   AssignmentExecutionKindTaskRun,
			Status: projectwork.AssignmentStatusCompleted,
		},
	})
	if completed.BlockingSignal != "completed" || completed.StatusSummary != "completed with 2 artifacts" || completed.Bucket != "completed" {
		t.Fatalf("completed state = %+v, want completed artifact summary", completed)
	}
}
