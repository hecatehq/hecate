package orchestrator

import (
	"context"
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

func (r *Runner) reconcileStaleRuns(ctx context.Context, staleThreshold time.Duration) error {
	return r.requireQueueCoordinator().reconcileStaleRuns(ctx, staleThreshold)
}

func (r *Runner) enqueueRun(taskID, runID string) error {
	return r.requireQueueCoordinator().enqueueRun(taskID, runID)
}

func (r *Runner) Shutdown(ctx context.Context) error {
	return r.requireQueueCoordinator().Shutdown(ctx)
}

func (r *Runner) registerJob(runID string, cancel context.CancelFunc) {
	r.requireQueueCoordinator().registerJob(runID, cancel)
}

func (r *Runner) unregisterJob(runID string) {
	r.requireQueueCoordinator().unregisterJob(runID)
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
