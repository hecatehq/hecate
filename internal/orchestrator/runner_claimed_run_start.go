package orchestrator

import (
	"context"
	"time"

	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

type claimedRunStartTransitionInput struct {
	Task       types.Task
	Run        types.TaskRun
	RequestID  string
	TraceID    string
	RootSpanID string
	Now        time.Time
}

type claimedRunStartTransition struct {
	Task        types.Task
	Run         types.TaskRun
	QueueWaitMS int64
}

func prepareClaimedRunStartTransition(input claimedRunStartTransitionInput) claimedRunStartTransition {
	now := input.Now.UTC()
	run := input.Run
	task := input.Task

	var queueWaitMS int64
	if !run.StartedAt.IsZero() {
		queueWaitMS = now.Sub(run.StartedAt).Milliseconds()
	}

	run.Status = "running"
	run.RequestID = input.RequestID
	run.TraceID = input.TraceID
	run.RootSpanID = input.RootSpanID
	if run.StartedAt.IsZero() {
		run.StartedAt = now
	}
	run.LastError = ""
	run.FinishedAt = time.Time{}

	task.Status = "running"
	task.LatestRunID = run.ID
	if task.StartedAt.IsZero() {
		task.StartedAt = now
	}
	task.UpdatedAt = now
	task.FinishedAt = time.Time{}
	task.LastError = ""
	task.RootTraceID = input.TraceID
	task.LatestTraceID = input.TraceID
	task.LatestRequestID = input.RequestID

	return claimedRunStartTransition{
		Task:        task,
		Run:         run,
		QueueWaitMS: queueWaitMS,
	}
}

func persistClaimedRunStartTransition(ctx context.Context, store taskstate.Store, transition claimedRunStartTransition) error {
	if _, err := store.UpdateRun(ctx, transition.Run); err != nil {
		return err
	}
	if _, err := store.UpdateTask(ctx, transition.Task); err != nil {
		return err
	}
	return nil
}
