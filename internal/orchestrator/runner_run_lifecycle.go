package orchestrator

import (
	"context"
	"time"

	"github.com/hecatehq/hecate/internal/runtimeevents"
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

func (r *Runner) requeueDisconnectedRun(ctx context.Context, task types.Task, run types.TaskRun, opts disconnectedRunRequeueOptions) {
	priorStatus := run.Status
	now := time.Now().UTC()

	run.Status = "queued"
	run.LastError = ""
	run.FinishedAt = time.Time{}
	run.OtelStatusCode = ""
	run.OtelStatusMessage = ""
	if _, err := r.store.UpdateRun(ctx, run); err != nil {
		return
	}

	task.Status = "queued"
	task.LatestRunID = run.ID
	task.LastError = ""
	task.UpdatedAt = now
	task.FinishedAt = time.Time{}
	_, _ = r.store.UpdateTask(ctx, task)

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
	_, _ = r.emitRunEvent(ctx, task.ID, run.ID, runtimeevents.EventGapRunDisconnected.String(), opts.RequestID, opts.TraceID, data)
	_ = r.enqueueRun(task.ID, run.ID)
}
