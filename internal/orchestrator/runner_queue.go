package orchestrator

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/hecatehq/hecate/pkg/types"
)

func (r *Runner) requireQueueCoordinator() *runQueueCoordinator {
	if r.queueCoordinator == nil {
		panic("orchestrator: run queue coordinator is not configured")
	}
	return r.queueCoordinator
}

func (r *Runner) getQueue() RunQueue {
	if r.queueCoordinator == nil {
		return nil
	}
	return r.queueCoordinator.getQueue()
}

func (r *Runner) SetQueue(queue RunQueue) {
	r.requireQueueCoordinator().SetQueue(queue)
}

func (r *Runner) ReconcilePendingRuns(ctx context.Context) error {
	return r.requireQueueCoordinator().ReconcilePendingRuns(ctx)
}

func (r *Runner) StartReconcileLoop() {
	r.requireQueueCoordinator().StartReconcileLoop()
}

// StartQueueRuntime performs boot reconciliation before enabling claimers,
// then starts periodic recovery. API composition calls this only after all
// durable origin validators and owner stores have been installed.
func (r *Runner) StartQueueRuntime(ctx context.Context) error {
	err := r.ReconcilePendingRuns(ctx)
	workers := r.config.QueueWorkers
	if workers <= 0 {
		workers = 1
	}
	r.requireQueueCoordinator().StartWorkers(workers)
	r.StartReconcileLoop()
	return err
}

func (r *Runner) reconcileStaleRuns(ctx context.Context, staleThreshold time.Duration) error {
	return r.requireQueueCoordinator().reconcileStaleRuns(ctx, staleThreshold)
}

func (r *Runner) enqueueRun(taskID, runID string) error {
	return r.requireQueueCoordinator().enqueueRun(taskID, runID)
}

// enqueueRunWithReconcile handles the queue write that follows a durable Run
// transition. Queue errors can be ambiguous (the job may have committed), so
// retain the Run for state-aware reconciliation instead of assuming either
// success or failure.
func (r *Runner) enqueueRunWithReconcile(taskID, runID string) error {
	job := QueueJob{TaskID: taskID, RunID: runID}
	err := r.enqueueRun(taskID, runID)
	if err != nil {
		r.requireQueueCoordinator().rememberPendingEnqueueReconcile(job)
		return err
	}
	r.requireQueueCoordinator().forgetPendingReconcile(job)
	return nil
}

func (r *Runner) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	queueErr := r.requireQueueCoordinator().Shutdown(ctx)
	if closer, ok := r.agent.(agentTerminalShutdownCloser); ok {
		closer.CloseAllTerminals(ctx)
	}
	return errors.Join(queueErr, ctx.Err())
}

func (r *Runner) registerJob(runID string, cancel context.CancelFunc) *inFlightJob {
	return r.requireQueueCoordinator().registerJob(runID, cancel)
}

func (r *Runner) unregisterJob(runID string, job ...*inFlightJob) {
	r.requireQueueCoordinator().unregisterJob(runID, job...)
}

func (r *Runner) hasInFlightJob(runID string) bool {
	return r.requireQueueCoordinator().hasInFlightJob(runID)
}

func (r *Runner) inFlightJobCount() int {
	if r.queueCoordinator == nil {
		return 0
	}
	return r.queueCoordinator.inFlightJobCount()
}

func (r *Runner) cancelInFlightJob(runID string) {
	if r.queueCoordinator != nil {
		r.queueCoordinator.cancelJob(runID)
	}
}

func (r *Runner) cancelAndWaitForInFlightJob(ctx context.Context, runID string) error {
	if r.queueCoordinator == nil {
		return nil
	}
	return r.queueCoordinator.cancelAndWaitForJob(ctx, runID)
}

func (r *Runner) executorForTask(task types.Task) Executor {
	if task.ExecutionKind == "agent_loop" && r.agent != nil {
		return r.agent
	}
	if task.ExecutionKind == "shell" && strings.TrimSpace(task.ShellCommand) != "" && r.shell != nil {
		return r.shell
	}
	if task.ExecutionKind == "file" && strings.TrimSpace(task.FilePath) != "" && r.file != nil {
		return r.file
	}
	if task.ExecutionKind == "git" && strings.TrimSpace(task.GitCommand) != "" && r.git != nil {
		return r.git
	}
	return r.exec
}
