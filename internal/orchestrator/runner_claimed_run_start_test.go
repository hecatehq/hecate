package orchestrator

import (
	"testing"
	"time"

	"github.com/hecatehq/hecate/pkg/types"
)

func TestClaimedRunStartTransition_PopulatesRunAndTask(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	queuedAt := now.Add(-1500 * time.Millisecond)
	finishedAt := now.Add(-time.Hour)

	transition := prepareClaimedRunStartTransition(claimedRunStartTransitionInput{
		Task: types.Task{
			ID:              "task-start",
			Status:          "queued",
			StartedAt:       finishedAt,
			UpdatedAt:       finishedAt,
			FinishedAt:      finishedAt,
			LastError:       "old task error",
			RootTraceID:     "old-trace",
			LatestTraceID:   "old-trace",
			LatestRequestID: "old-request",
		},
		Run: types.TaskRun{
			ID:          "run-start",
			TaskID:      "task-start",
			Status:      "queued",
			StartedAt:   queuedAt,
			FinishedAt:  finishedAt,
			RequestID:   "old-request",
			TraceID:     "old-trace",
			RootSpanID:  "old-span",
			LastError:   "old run error",
			WorkspaceID: "workspace",
		},
		RequestID:  "request-new",
		TraceID:    "trace-new",
		RootSpanID: "span-new",
		Now:        now,
	})

	if transition.QueueWaitMS != 1500 {
		t.Fatalf("queue wait = %d, want 1500", transition.QueueWaitMS)
	}
	if transition.Run.Status != "running" {
		t.Fatalf("run status = %q, want running", transition.Run.Status)
	}
	if transition.Run.RequestID != "request-new" || transition.Run.TraceID != "trace-new" || transition.Run.RootSpanID != "span-new" {
		t.Fatalf("run ids = request:%q trace:%q span:%q, want new ids", transition.Run.RequestID, transition.Run.TraceID, transition.Run.RootSpanID)
	}
	if !transition.Run.StartedAt.Equal(queuedAt) {
		t.Fatalf("run started_at = %v, want preserved queued timestamp %v", transition.Run.StartedAt, queuedAt)
	}
	if !transition.Run.FinishedAt.IsZero() || transition.Run.LastError != "" {
		t.Fatalf("run terminal fields = finished:%v last_error:%q, want cleared", transition.Run.FinishedAt, transition.Run.LastError)
	}
	if transition.Run.WorkspaceID != "workspace" {
		t.Fatalf("run workspace id = %q, want preserved", transition.Run.WorkspaceID)
	}
	if transition.Task.Status != "running" || transition.Task.LatestRunID != "run-start" {
		t.Fatalf("task status/latest run = %q/%q, want running/run-start", transition.Task.Status, transition.Task.LatestRunID)
	}
	if !transition.Task.StartedAt.Equal(finishedAt) {
		t.Fatalf("task started_at = %v, want preserved %v", transition.Task.StartedAt, finishedAt)
	}
	if !transition.Task.UpdatedAt.Equal(now) {
		t.Fatalf("task updated_at = %v, want %v", transition.Task.UpdatedAt, now)
	}
	if !transition.Task.FinishedAt.IsZero() || transition.Task.LastError != "" {
		t.Fatalf("task terminal fields = finished:%v last_error:%q, want cleared", transition.Task.FinishedAt, transition.Task.LastError)
	}
	if transition.Task.RootTraceID != "trace-new" || transition.Task.LatestTraceID != "trace-new" || transition.Task.LatestRequestID != "request-new" {
		t.Fatalf("task trace ids = root:%q latest:%q request:%q, want new ids", transition.Task.RootTraceID, transition.Task.LatestTraceID, transition.Task.LatestRequestID)
	}
}

func TestClaimedRunStartTransition_InitializesFreshStartTimes(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 29, 12, 30, 0, 0, time.UTC)
	transition := prepareClaimedRunStartTransition(claimedRunStartTransitionInput{
		Task:      types.Task{ID: "task-fresh", Status: "queued"},
		Run:       types.TaskRun{ID: "run-fresh", TaskID: "task-fresh", Status: "queued"},
		RequestID: "request-fresh",
		TraceID:   "trace-fresh",
		Now:       now,
	})

	if transition.QueueWaitMS != 0 {
		t.Fatalf("queue wait = %d, want 0", transition.QueueWaitMS)
	}
	if !transition.Run.StartedAt.Equal(now) {
		t.Fatalf("run started_at = %v, want %v", transition.Run.StartedAt, now)
	}
	if !transition.Task.StartedAt.Equal(now) {
		t.Fatalf("task started_at = %v, want %v", transition.Task.StartedAt, now)
	}
}
