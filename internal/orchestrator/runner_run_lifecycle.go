package orchestrator

import (
	"context"
	"errors"
	"time"

	"github.com/hecatehq/hecate/internal/runtimeevents"
	"github.com/hecatehq/hecate/internal/taskruncoord"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

type disconnectedRunRequeueOptions struct {
	Reason           string
	RecoveryStrategy string
	RequestID        string
	TraceID          string
	Extra            map[string]any
}

func (r *Runner) emitRunQueuedAndEnqueue(ctx context.Context, taskID, runID, requestID, traceID string, eventData map[string]any) error {
	_, _ = r.emitRunEvent(ctx, taskID, runID, runtimeevents.EventRunQueued.String(), requestID, traceID, eventData)
	return r.enqueueRun(taskID, runID)
}

func (r *Runner) requeueDisconnectedRun(ctx context.Context, task types.Task, run types.TaskRun, opts disconnectedRunRequeueOptions) error {
	originLease, err := r.beginOriginRunMutation(ctx, task)
	if err != nil {
		if errors.Is(err, taskruncoord.ErrOriginUnavailable) {
			_, cancelErr := r.cancelRunWithMessage(ctx, task, run, "run cancelled: task origin is unavailable", opts.RequestID, opts.TraceID)
			return cancelErr
		}
		return err
	}
	if originLease != nil {
		defer originLease.Release()
	}

	priorStatus := run.Status
	now := time.Now().UTC()

	run.Status = "queued"
	run.LastError = ""
	run.FinishedAt = time.Time{}
	run.OtelStatusCode = ""
	run.OtelStatusMessage = ""
	task.Status = "queued"
	task.LatestRunID = run.ID
	task.LastError = ""
	task.UpdatedAt = now
	task.FinishedAt = time.Time{}
	data := map[string]any{
		"reason":            opts.Reason,
		"action":            "requeued",
		"prior_status":      priorStatus,
		"recovered_status":  "queued",
		"recovery_strategy": opts.RecoveryStrategy,
	}
	for key, value := range opts.Extra {
		data[key] = value
	}
	result, err := r.store.ApplyRunStateTransition(ctx, taskstate.RunStateTransition{
		Task:                task,
		Run:                 run,
		ExpectedRunStatuses: []string{priorStatus},
		Events: []taskstate.RunEventSpec{{
			EventType: runtimeevents.EventGapRunDisconnected.String(),
			Data:      data,
			RequestID: opts.RequestID,
			TraceID:   opts.TraceID,
			CreatedAt: now,
		}},
	})
	if err != nil || !result.Applied {
		return err
	}
	return r.enqueueRun(task.ID, run.ID)
}
