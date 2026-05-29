package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

func (r *Runner) getQueue() RunQueue {
	r.queueMu.RLock()
	q := r.queue
	r.queueMu.RUnlock()
	return q
}

func (r *Runner) SetQueue(queue RunQueue) {
	if queue == nil {
		return
	}
	r.queueMu.Lock()
	r.queue = queue
	r.queueMu.Unlock()
}

func (r *Runner) ReconcilePendingRuns(ctx context.Context) error {
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

// StartReconcileLoop starts a background goroutine that periodically scans
// for runs stuck in "running" state and re-enqueues them. It is distinct from
// the boot-time ReconcilePendingRuns: it only targets runs whose StartedAt is
// older than 3× the queue lease (i.e., the worker should have heartbeated or
// completed by now), so it does not race with legitimately in-flight workers.
//
// The loop is tied to the runner's worker context and stops automatically when
// Shutdown is called. It is safe to call once at startup after the boot-time
// reconcile.
func (r *Runner) StartReconcileLoop() {
	staleThreshold := r.queueLease * 3
	if staleThreshold <= 0 {
		staleThreshold = 90 * time.Second
	}
	r.workerWg.Add(1)
	go func() {
		defer r.workerWg.Done()
		ticker := time.NewTicker(r.reconcileInterval)
		defer ticker.Stop()
		for {
			select {
			case <-r.workerCtx.Done():
				return
			case <-ticker.C:
				if err := r.reconcileStaleRuns(r.workerCtx, staleThreshold); err != nil {
					telemetry.Warn(r.logger, r.workerCtx, "periodic task reconcile failed", slog.Any("error", err))
				}
			}
		}
	}()
}

// reconcileStaleRuns re-enqueues runs that are stuck in "running" state
// with a StartedAt older than staleThreshold. Unlike ReconcilePendingRuns
// (which is a boot-time sweep of all non-terminal runs), this targets only
// runs that an active worker should have completed or heartbeated by now.
func (r *Runner) reconcileStaleRuns(ctx context.Context, staleThreshold time.Duration) error {
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
		if r.hasInFlightJob(run.ID) {
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

func (r *Runner) processQueue() {
	defer r.workerWg.Done()
	for {
		// Fast-exit when shutdown has fired. The two select-on-ctx
		// blocks below also catch this, but checking up-front keeps a
		// freshly-cancelled worker from issuing one last Claim against
		// the store on its way out.
		if r.workerCtx.Err() != nil {
			return
		}
		q := r.getQueue()
		if q == nil {
			// No queue wired (transient during boot). Bounded sleep
			// instead of a hot loop, but unblock immediately on
			// shutdown so the goroutine returns inside the deadline.
			select {
			case <-time.After(200 * time.Millisecond):
			case <-r.workerCtx.Done():
				return
			}
			continue
		}
		claim, ok, err := q.Claim(r.workerCtx, r.workerID, 2*time.Second)
		if err != nil {
			// Claim failure may be transient (lock contention, brief
			// store hiccup) OR shutdown — distinguish so a real error
			// gets a brief backoff while a cancelled context exits.
			if r.workerCtx.Err() != nil {
				return
			}
			select {
			case <-time.After(150 * time.Millisecond):
			case <-r.workerCtx.Done():
				return
			}
			continue
		}
		if !ok {
			continue
		}
		r.processQueuedRun(claim)
	}
}

func (r *Runner) processQueuedRun(claim QueueClaim) {
	q := r.getQueue()
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

	// Parent jobCtx off the runner's worker context so Shutdown
	// cascades cancellation into the agent loop, which in turn closes
	// its MCP host (via the existing defer host.Close chain) — that's
	// what stops orphaned subprocesses on gateway exit. workerWg counts
	// this job so Shutdown's drain wait covers it as well as the
	// claiming goroutine itself.
	jobCtx, jobCancel := context.WithCancel(r.workerCtx)
	r.workerWg.Add(1)
	defer r.workerWg.Done()
	r.registerJob(run.ID, jobCancel)
	defer r.unregisterJob(run.ID)
	defer jobCancel()

	stopHeartbeat := make(chan struct{})
	go r.heartbeatClaim(claim.ClaimID, stopHeartbeat)
	defer close(stopHeartbeat)

	ctx := telemetry.WithTraceIDs(jobCtx, trace.TraceID, trace.RootSpanID())
	now := time.Now().UTC()

	// Compute queue wait before overwriting run.StartedAt.
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
		telemetry.AttrHecateWorkerID:     r.workerID,
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

func (r *Runner) enqueueRun(taskID, runID string) error {
	q := r.getQueue()
	if q == nil {
		return fmt.Errorf("run queue is not configured")
	}
	return q.Enqueue(context.Background(), QueueJob{TaskID: taskID, RunID: runID})
}

func (r *Runner) Shutdown(ctx context.Context) error {
	r.shutdownOnce.Do(func() {
		// Cancel the worker lifetime context first — this stops new
		// queue claims and (because every jobCtx is parented from it)
		// cascades cancellation into running agent loops.
		r.workerCancel()
		// Belt-and-braces: also fire each registered job's cancel
		// directly, in case any future code path detaches a jobCtx
		// from workerCtx. Iterating r.jobs under jobMu is what the
		// existing CancelRun path does.
		r.jobMu.Lock()
		for _, cancel := range r.jobs {
			cancel()
		}
		r.jobMu.Unlock()
	})
	// Wait for all worker goroutines AND in-flight jobs to finish,
	// or for the caller's deadline to expire.
	done := make(chan struct{})
	go func() {
		r.workerWg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Runner) registerJob(runID string, cancel context.CancelFunc) {
	r.jobMu.Lock()
	defer r.jobMu.Unlock()
	r.jobs[runID] = cancel
}

func (r *Runner) unregisterJob(runID string) {
	r.jobMu.Lock()
	defer r.jobMu.Unlock()
	delete(r.jobs, runID)
}

func (r *Runner) hasInFlightJob(runID string) bool {
	r.jobMu.Lock()
	defer r.jobMu.Unlock()
	_, ok := r.jobs[runID]
	return ok
}

func (r *Runner) heartbeatClaim(claimID string, stop <-chan struct{}) {
	if r.getQueue() == nil || claimID == "" {
		return
	}
	interval := r.queueLease / 2
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
			if q := r.getQueue(); q != nil {
				if err := q.ExtendLease(context.Background(), claimID, r.queueLease); err != nil {
					r.metrics.RecordLeaseExtendFailed(context.Background())
				}
			}
		}
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
