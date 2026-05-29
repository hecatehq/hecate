package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/internal/telemetry"
)

type runQueueCoordinatorConfig struct {
	Queue             RunQueue
	Lease             time.Duration
	ReconcileInterval time.Duration
	WorkerID          string
}

type runQueueCoordinator struct {
	runner *Runner

	queueMu sync.RWMutex
	queue   RunQueue

	lease             time.Duration
	reconcileInterval time.Duration
	workerID          string

	jobMu sync.Mutex
	jobs  map[string]context.CancelFunc

	// workerCtx is the lifetime context for queue-worker goroutines and
	// every in-flight job they process. Shutdown cancels this; processQueue
	// observes the cancel and stops claiming new work, and every job's
	// context is parented from it so cancellation cascades into running
	// agent loops.
	workerCtx    context.Context
	workerCancel context.CancelFunc

	// workerWg tracks both the worker goroutines and in-flight jobs. Shutdown
	// waits on it so the gateway doesn't return while a run is still finalizing.
	workerWg     sync.WaitGroup
	shutdownOnce sync.Once
}

func newRunQueueCoordinator(runner *Runner, cfg runQueueCoordinatorConfig) *runQueueCoordinator {
	lease := cfg.Lease
	if lease <= 0 {
		lease = 30 * time.Second
	}
	reconcileInterval := cfg.ReconcileInterval
	if reconcileInterval <= 0 {
		reconcileInterval = 30 * time.Second
	}
	workerID := strings.TrimSpace(cfg.WorkerID)
	if workerID == "" {
		workerID = defaultWorkerID()
	}
	workerCtx, workerCancel := context.WithCancel(context.Background())
	return &runQueueCoordinator{
		runner:            runner,
		queue:             cfg.Queue,
		lease:             lease,
		reconcileInterval: reconcileInterval,
		workerID:          workerID,
		jobs:              make(map[string]context.CancelFunc),
		workerCtx:         workerCtx,
		workerCancel:      workerCancel,
	}
}

func (c *runQueueCoordinator) getQueue() RunQueue {
	c.queueMu.RLock()
	q := c.queue
	c.queueMu.RUnlock()
	return q
}

func (c *runQueueCoordinator) SetQueue(queue RunQueue) {
	if queue == nil {
		return
	}
	c.queueMu.Lock()
	c.queue = queue
	c.queueMu.Unlock()
}

func (c *runQueueCoordinator) StartWorkers(workers int) {
	for worker := 0; worker < workers; worker++ {
		c.workerWg.Add(1)
		go c.processQueue()
	}
}

func (c *runQueueCoordinator) ReconcilePendingRuns(ctx context.Context) error {
	r := c.runner
	if r.store == nil {
		return nil
	}
	runs, err := r.store.ListRunsByFilter(ctx, taskstate.RunFilter{
		Statuses: []string{"queued", "running"},
		Limit:    500,
	})
	if err != nil {
		return err
	}
	for _, run := range runs {
		task, found, err := r.store.GetTask(ctx, run.TaskID)
		if err != nil || !found {
			continue
		}
		r.requeueDisconnectedRun(ctx, task, run, disconnectedRunRequeueOptions{
			Reason:           "boot_reconcile",
			RecoveryStrategy: "requeue",
		})
	}
	return nil
}

// StartReconcileLoop starts a background goroutine that periodically scans for
// runs stuck in "running" state and re-enqueues them. It is distinct from the
// boot-time ReconcilePendingRuns: it only targets runs whose StartedAt is older
// than 3x the queue lease, so it does not race with legitimately in-flight
// workers.
func (c *runQueueCoordinator) StartReconcileLoop() {
	staleThreshold := c.lease * 3
	if staleThreshold <= 0 {
		staleThreshold = 90 * time.Second
	}
	c.workerWg.Add(1)
	go func() {
		defer c.workerWg.Done()
		ticker := time.NewTicker(c.reconcileInterval)
		defer ticker.Stop()
		for {
			select {
			case <-c.workerCtx.Done():
				return
			case <-ticker.C:
				if err := c.reconcileStaleRuns(c.workerCtx, staleThreshold); err != nil {
					telemetry.Warn(c.runner.logger, c.workerCtx, "periodic task reconcile failed", slog.Any("error", err))
				}
			}
		}
	}()
}

// reconcileStaleRuns re-enqueues runs that are stuck in "running" state with a
// StartedAt older than staleThreshold. Unlike ReconcilePendingRuns (which is a
// boot-time sweep of all non-terminal runs), this targets only runs that an
// active worker should have completed or heartbeated by now.
func (c *runQueueCoordinator) reconcileStaleRuns(ctx context.Context, staleThreshold time.Duration) error {
	r := c.runner
	if r.store == nil {
		return nil
	}
	runs, err := r.store.ListRunsByFilter(ctx, taskstate.RunFilter{
		Statuses: []string{"running"},
		Limit:    500,
	})
	if err != nil {
		return err
	}
	cutoff := time.Now().UTC().Add(-staleThreshold)
	for _, run := range runs {
		if ctx.Err() != nil {
			return nil
		}
		if c.hasInFlightJob(run.ID) {
			continue
		}
		if run.StartedAt.IsZero() || run.StartedAt.After(cutoff) {
			continue
		}
		task, found, err := r.store.GetTask(ctx, run.TaskID)
		if err != nil || !found {
			continue
		}
		r.requeueDisconnectedRun(ctx, task, run, disconnectedRunRequeueOptions{
			Reason:           "worker_lease_expired",
			RecoveryStrategy: "periodic_requeue",
			Extra: map[string]any{
				"stale_threshold_ms": staleThreshold.Milliseconds(),
			},
		})
	}
	return nil
}

func (c *runQueueCoordinator) processQueue() {
	defer c.workerWg.Done()
	for {
		if c.workerCtx.Err() != nil {
			return
		}
		q := c.getQueue()
		if q == nil {
			select {
			case <-time.After(200 * time.Millisecond):
			case <-c.workerCtx.Done():
				return
			}
			continue
		}
		claim, ok, err := q.Claim(c.workerCtx, c.workerID, 2*time.Second)
		if err != nil {
			if c.workerCtx.Err() != nil {
				return
			}
			select {
			case <-time.After(150 * time.Millisecond):
			case <-c.workerCtx.Done():
				return
			}
			continue
		}
		if !ok {
			continue
		}
		c.processQueuedRun(claim)
	}
}

func (c *runQueueCoordinator) processQueuedRun(claim QueueClaim) {
	r := c.runner
	q := c.getQueue()
	task, found, err := r.store.GetTask(context.Background(), claim.Job.TaskID)
	if err != nil || !found {
		_ = q.Ack(context.Background(), claim.ClaimID)
		return
	}
	run, found, err := r.store.GetRun(context.Background(), claim.Job.TaskID, claim.Job.RunID)
	if err != nil || !found {
		_ = q.Ack(context.Background(), claim.ClaimID)
		return
	}
	if run.Status != "queued" {
		_ = q.Ack(context.Background(), claim.ClaimID)
		return
	}
	requestID := strings.TrimSpace(run.RequestID)
	if requestID == "" {
		requestID = defaultResourceID("request")
	}
	trace := r.tracer.Start(requestID)
	defer trace.Finalize()

	jobCtx, jobCancel := context.WithCancel(c.workerCtx)
	c.workerWg.Add(1)
	defer c.workerWg.Done()
	c.registerJob(run.ID, jobCancel)
	defer c.unregisterJob(run.ID)
	defer jobCancel()

	stopHeartbeat := make(chan struct{})
	go c.heartbeatClaim(claim.ClaimID, stopHeartbeat)
	defer close(stopHeartbeat)

	ctx := telemetry.WithTraceIDs(jobCtx, trace.TraceID, trace.RootSpanID())
	now := time.Now().UTC()

	var queueWaitMS int64
	if !run.StartedAt.IsZero() {
		queueWaitMS = now.Sub(run.StartedAt).Milliseconds()
	}
	queueBackend := ""
	if q != nil {
		queueBackend = q.Backend()
	}
	trace.Record(telemetry.EventQueueClaimed, map[string]any{
		telemetry.AttrHecateTaskID:       task.ID,
		telemetry.AttrHecateRunID:        run.ID,
		telemetry.AttrHecateQueueBackend: queueBackend,
		telemetry.AttrHecateQueueWaitMS:  queueWaitMS,
		telemetry.AttrHecateWorkerID:     c.workerID,
	})
	r.metrics.RecordQueueWait(ctx, telemetry.QueueWaitRecord{
		TaskID:       task.ID,
		RunID:        run.ID,
		QueueBackend: queueBackend,
		WaitMS:       queueWaitMS,
	})

	run.Status = "running"
	run.RequestID = requestID
	run.TraceID = trace.TraceID
	run.RootSpanID = trace.RootSpanID()
	if run.StartedAt.IsZero() {
		run.StartedAt = now
	}
	run.LastError = ""
	run.FinishedAt = time.Time{}
	if _, err := r.store.UpdateRun(ctx, run); err != nil {
		return
	}
	task.Status = "running"
	task.LatestRunID = run.ID
	if task.StartedAt.IsZero() {
		task.StartedAt = now
	}
	task.UpdatedAt = now
	task.FinishedAt = time.Time{}
	task.LastError = ""
	task.RootTraceID = trace.TraceID
	task.LatestTraceID = trace.TraceID
	task.LatestRequestID = requestID
	if _, err := r.store.UpdateTask(ctx, task); err != nil {
		return
	}

	recordOrchestratorRunStarted(trace, task.ID, run)

	resumeCheckpoint, checkpointErr := r.resumeCheckpointForRun(ctx, task.ID, run.ID)
	if checkpointErr != nil {
		_, _ = r.emitRunEvent(ctx, task.ID, run.ID, "gap.run_disconnected", requestID, trace.TraceID, map[string]any{
			"reason":  "resume_checkpoint_unavailable",
			"action":  "start_fresh",
			"message": checkpointErr.Error(),
		})
	}
	runEvent := map[string]any{}
	if resumeCheckpoint != nil {
		runEvent["resume_from_run_id"] = resumeCheckpoint.SourceRunID
		runEvent["resume_from_step_id"] = resumeCheckpoint.LastCompletedStepID
		runEvent["resume_from_event_sequence"] = resumeCheckpoint.LastEventSequence
	}
	_, _ = r.emitRunEvent(ctx, task.ID, run.ID, "run.started", requestID, trace.TraceID, runEvent)

	if _, err := r.executeRun(ctx, trace, task, run, requestID, resumeCheckpoint); err != nil {
		finalStatus := "failed"
		lastError := err.Error()
		if jobCtx.Err() != nil {
			finalStatus = "cancelled"
			lastError = "run cancelled"
		}
		_ = r.finalizeFailedRun(ctx, trace, task, run, requestID, finalStatus, lastError)
	}
	trace.Record(telemetry.EventQueueAcked, map[string]any{
		telemetry.AttrHecateTaskID:       task.ID,
		telemetry.AttrHecateRunID:        run.ID,
		telemetry.AttrHecateQueueBackend: queueBackend,
	})
	_ = q.Ack(context.Background(), claim.ClaimID)
}

func (c *runQueueCoordinator) enqueueRun(taskID, runID string) error {
	q := c.getQueue()
	if q == nil {
		return fmt.Errorf("run queue is not configured")
	}
	return q.Enqueue(context.Background(), QueueJob{TaskID: taskID, RunID: runID})
}

func (c *runQueueCoordinator) Shutdown(ctx context.Context) error {
	c.shutdownOnce.Do(func() {
		c.workerCancel()
		c.cancelAllJobs()
	})
	done := make(chan struct{})
	go func() {
		c.workerWg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *runQueueCoordinator) registerJob(runID string, cancel context.CancelFunc) {
	c.jobMu.Lock()
	defer c.jobMu.Unlock()
	c.jobs[runID] = cancel
}

func (c *runQueueCoordinator) unregisterJob(runID string) {
	c.jobMu.Lock()
	defer c.jobMu.Unlock()
	delete(c.jobs, runID)
}

func (c *runQueueCoordinator) hasInFlightJob(runID string) bool {
	c.jobMu.Lock()
	defer c.jobMu.Unlock()
	_, ok := c.jobs[runID]
	return ok
}

func (c *runQueueCoordinator) inFlightJobCount() int {
	c.jobMu.Lock()
	defer c.jobMu.Unlock()
	return len(c.jobs)
}

func (c *runQueueCoordinator) cancelJob(runID string) {
	c.jobMu.Lock()
	cancel, ok := c.jobs[runID]
	c.jobMu.Unlock()
	if ok {
		cancel()
	}
}

func (c *runQueueCoordinator) cancelAllJobs() {
	c.jobMu.Lock()
	defer c.jobMu.Unlock()
	for _, cancel := range c.jobs {
		cancel()
	}
}

func (c *runQueueCoordinator) heartbeatClaim(claimID string, stop <-chan struct{}) {
	if c.getQueue() == nil || claimID == "" {
		return
	}
	interval := c.lease / 2
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if q := c.getQueue(); q != nil {
				if err := q.ExtendLease(context.Background(), claimID, c.lease); err != nil {
					c.runner.metrics.RecordLeaseExtendFailed(context.Background())
				}
			}
		}
	}
}
