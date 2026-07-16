package orchestrator

import (
	"context"
	"errors"
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

const pendingRunReconcilePageSize = 500

type runQueueCoordinator struct {
	runner *Runner

	queueMu sync.RWMutex
	queue   RunQueue

	lease             time.Duration
	reconcileInterval time.Duration
	workerID          string

	jobMu sync.Mutex
	jobs  map[string]map[*inFlightJob]struct{}

	pendingReconcileMu sync.Mutex
	pendingReconciles  map[QueueJob]struct{}
	retryFullReconcile bool

	// workerCtx is the lifetime context for queue-worker goroutines and
	// every in-flight job they process. Shutdown cancels this; processQueue
	// observes the cancel and stops claiming new work, and every job's
	// context is parented from it so cancellation cascades into running
	// agent loops.
	workerCtx    context.Context
	workerCancel context.CancelFunc

	// workerWg tracks both the worker goroutines and in-flight jobs. Shutdown
	// waits on it so the gateway doesn't return while a run is still finalizing.
	workerWg      sync.WaitGroup
	workersOnce   sync.Once
	reconcileOnce sync.Once
	shutdownOnce  sync.Once
}

type inFlightJob struct {
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func (job *inFlightJob) finish() {
	if job != nil {
		job.once.Do(func() { close(job.done) })
	}
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
		jobs:              make(map[string]map[*inFlightJob]struct{}),
		pendingReconciles: make(map[QueueJob]struct{}),
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
	c.workersOnce.Do(func() {
		for worker := 0; worker < workers; worker++ {
			c.workerWg.Add(1)
			go c.processQueue()
		}
	})
}

func (c *runQueueCoordinator) ReconcilePendingRuns(ctx context.Context) error {
	r := c.runner
	if r.store == nil {
		return nil
	}
	var reconcileErrors []error
	afterID := ""
	for {
		runs, err := r.store.ListRunsByFilter(ctx, taskstate.RunFilter{
			Statuses:  []string{"queued", "running"},
			Limit:     pendingRunReconcilePageSize,
			OrderByID: true,
			AfterID:   afterID,
		})
		if err != nil {
			c.pendingReconcileMu.Lock()
			c.retryFullReconcile = true
			c.pendingReconcileMu.Unlock()
			reconcileErrors = append(reconcileErrors, err)
			return errors.Join(reconcileErrors...)
		}
		for _, run := range runs {
			task, found, err := r.store.GetTask(ctx, run.TaskID)
			if err != nil {
				c.rememberPendingReconcile(QueueJob{TaskID: run.TaskID, RunID: run.ID})
				reconcileErrors = append(reconcileErrors, fmt.Errorf("load task %q for run %q: %w", run.TaskID, run.ID, err))
				continue
			}
			if !found {
				continue
			}
			if err := r.requeueDisconnectedRun(ctx, task, run, disconnectedRunRequeueOptions{
				Reason:           "boot_reconcile",
				RecoveryStrategy: "requeue",
			}); err != nil {
				c.rememberPendingReconcile(QueueJob{TaskID: task.ID, RunID: run.ID})
				reconcileErrors = append(reconcileErrors, fmt.Errorf("reconcile run %q: %w", run.ID, err))
			} else {
				c.forgetPendingReconcile(QueueJob{TaskID: task.ID, RunID: run.ID})
			}
		}
		if len(runs) < pendingRunReconcilePageSize {
			break
		}
		nextAfterID := runs[len(runs)-1].ID
		if nextAfterID <= afterID {
			c.pendingReconcileMu.Lock()
			c.retryFullReconcile = true
			c.pendingReconcileMu.Unlock()
			return errors.Join(append(reconcileErrors, fmt.Errorf("pending run reconcile cursor did not advance after %q", afterID))...)
		}
		afterID = nextAfterID
	}
	c.clearFullReconcile()
	return errors.Join(reconcileErrors...)
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
	c.reconcileOnce.Do(func() {
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
					if c.needsFullReconcile() {
						if err := c.ReconcilePendingRuns(c.workerCtx); err != nil {
							telemetry.Warn(c.runner.logger, c.workerCtx, "full task reconcile retry failed", slog.Any("error", err))
						} else {
							c.clearFullReconcile()
						}
					}
					if err := c.retryPendingReconciles(c.workerCtx); err != nil {
						telemetry.Warn(c.runner.logger, c.workerCtx, "pending task reconcile retry failed", slog.Any("error", err))
					}
					if err := c.reconcileStaleRuns(c.workerCtx, staleThreshold); err != nil {
						telemetry.Warn(c.runner.logger, c.workerCtx, "periodic task reconcile failed", slog.Any("error", err))
					}
				}
			}
		}()
	})
}

func (c *runQueueCoordinator) needsFullReconcile() bool {
	c.pendingReconcileMu.Lock()
	defer c.pendingReconcileMu.Unlock()
	return c.retryFullReconcile
}

func (c *runQueueCoordinator) clearFullReconcile() {
	c.pendingReconcileMu.Lock()
	c.retryFullReconcile = false
	c.pendingReconcileMu.Unlock()
}

func (c *runQueueCoordinator) rememberPendingReconcile(job QueueJob) {
	c.pendingReconcileMu.Lock()
	c.pendingReconciles[job] = struct{}{}
	c.pendingReconcileMu.Unlock()
}

func (c *runQueueCoordinator) forgetPendingReconcile(job QueueJob) {
	c.pendingReconcileMu.Lock()
	delete(c.pendingReconciles, job)
	c.pendingReconcileMu.Unlock()
}

func (c *runQueueCoordinator) retryPendingReconciles(ctx context.Context) error {
	c.pendingReconcileMu.Lock()
	jobs := make([]QueueJob, 0, len(c.pendingReconciles))
	for job := range c.pendingReconciles {
		jobs = append(jobs, job)
	}
	c.pendingReconcileMu.Unlock()
	var retryErrors []error
	for _, job := range jobs {
		run, found, err := c.runner.store.GetRun(ctx, job.TaskID, job.RunID)
		if err != nil {
			retryErrors = append(retryErrors, fmt.Errorf("load pending run %q: %w", job.RunID, err))
			continue
		}
		if !found || (run.Status != "queued" && run.Status != "running") {
			c.forgetPendingReconcile(job)
			continue
		}
		task, found, err := c.runner.store.GetTask(ctx, job.TaskID)
		if err != nil {
			retryErrors = append(retryErrors, fmt.Errorf("load pending task %q: %w", job.TaskID, err))
			continue
		}
		if !found {
			c.forgetPendingReconcile(job)
			continue
		}
		if err := c.runner.requeueDisconnectedRun(ctx, task, run, disconnectedRunRequeueOptions{
			Reason: "boot_reconcile_retry", RecoveryStrategy: "requeue",
		}); err != nil {
			retryErrors = append(retryErrors, fmt.Errorf("retry pending run %q: %w", job.RunID, err))
			continue
		}
		c.forgetPendingReconcile(job)
	}
	return errors.Join(retryErrors...)
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
	var reconcileErrors []error
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
		if err := r.requeueDisconnectedRun(ctx, task, run, disconnectedRunRequeueOptions{
			Reason:           "worker_lease_expired",
			RecoveryStrategy: "periodic_requeue",
			Extra: map[string]any{
				"stale_threshold_ms": staleThreshold.Milliseconds(),
			},
		}); err != nil {
			reconcileErrors = append(reconcileErrors, fmt.Errorf("reconcile stale run %q: %w", run.ID, err))
		}
	}
	return errors.Join(reconcileErrors...)
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
	newClaimedRunProcessor(c, claim).process()
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

func (c *runQueueCoordinator) registerJob(runID string, cancel context.CancelFunc) *inFlightJob {
	job := &inFlightJob{cancel: cancel, done: make(chan struct{})}
	c.jobMu.Lock()
	if c.jobs[runID] == nil {
		c.jobs[runID] = make(map[*inFlightJob]struct{})
	}
	c.jobs[runID][job] = struct{}{}
	c.jobMu.Unlock()
	return job
}

func (c *runQueueCoordinator) unregisterJob(runID string, selected ...*inFlightJob) {
	c.jobMu.Lock()
	jobs := c.jobs[runID]
	if len(selected) == 0 {
		delete(c.jobs, runID)
	} else {
		for _, job := range selected {
			delete(jobs, job)
		}
		if len(jobs) == 0 {
			delete(c.jobs, runID)
		}
	}
	c.jobMu.Unlock()
	if len(selected) == 0 {
		for job := range jobs {
			job.finish()
		}
		return
	}
	for _, job := range selected {
		job.finish()
	}
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
	count := 0
	for _, jobs := range c.jobs {
		count += len(jobs)
	}
	return count
}

func (c *runQueueCoordinator) cancelJob(runID string) {
	c.jobMu.Lock()
	jobs := make([]*inFlightJob, 0, len(c.jobs[runID]))
	for job := range c.jobs[runID] {
		jobs = append(jobs, job)
	}
	c.jobMu.Unlock()
	for _, job := range jobs {
		job.cancel()
	}
}

func (c *runQueueCoordinator) cancelAndWaitForJob(ctx context.Context, runID string) error {
	c.jobMu.Lock()
	jobs := make([]*inFlightJob, 0, len(c.jobs[runID]))
	for job := range c.jobs[runID] {
		jobs = append(jobs, job)
	}
	c.jobMu.Unlock()
	for _, job := range jobs {
		job.cancel()
	}
	for _, job := range jobs {
		select {
		case <-job.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (c *runQueueCoordinator) cancelAllJobs() {
	c.jobMu.Lock()
	jobs := make([]*inFlightJob, 0)
	for _, runJobs := range c.jobs {
		for job := range runJobs {
			jobs = append(jobs, job)
		}
	}
	c.jobMu.Unlock()
	for _, job := range jobs {
		job.cancel()
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
