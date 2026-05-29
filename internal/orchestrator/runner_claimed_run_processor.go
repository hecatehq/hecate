package orchestrator

import (
	"context"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

type claimedRunProcessor struct {
	coordinator *runQueueCoordinator
	runner      *Runner
	claim       QueueClaim

	queue        RunQueue
	task         types.Task
	run          types.TaskRun
	requestID    string
	trace        *profiler.Trace
	queueBackend string
}

func newClaimedRunProcessor(coordinator *runQueueCoordinator, claim QueueClaim) *claimedRunProcessor {
	return &claimedRunProcessor{
		coordinator: coordinator,
		runner:      coordinator.runner,
		claim:       claim,
	}
}

func (p *claimedRunProcessor) process() {
	if !p.loadClaimedRun() {
		return
	}

	p.startTrace()
	defer p.trace.Finalize()

	jobCtx, stopJob := p.beginJob()
	defer stopJob()

	stopHeartbeat := make(chan struct{})
	go p.coordinator.heartbeatClaim(p.claim.ClaimID, stopHeartbeat)
	defer close(stopHeartbeat)

	ctx := telemetry.WithTraceIDs(jobCtx, p.trace.TraceID, p.trace.RootSpanID())
	if !p.startClaimedRun(ctx) {
		return
	}
	resumeCheckpoint := p.emitRunStarted(ctx)
	p.executeClaimedRun(ctx, jobCtx, resumeCheckpoint)
	p.recordQueueAcked()
	p.ackClaim()
}

func (p *claimedRunProcessor) loadClaimedRun() bool {
	p.queue = p.coordinator.getQueue()
	task, found, err := p.runner.store.GetTask(context.Background(), p.claim.Job.TaskID)
	if err != nil || !found {
		p.ackClaim()
		return false
	}
	run, found, err := p.runner.store.GetRun(context.Background(), p.claim.Job.TaskID, p.claim.Job.RunID)
	if err != nil || !found {
		p.ackClaim()
		return false
	}
	if run.Status != "queued" {
		p.ackClaim()
		return false
	}
	p.task = task
	p.run = run
	return true
}

func (p *claimedRunProcessor) startTrace() {
	requestID := strings.TrimSpace(p.run.RequestID)
	if requestID == "" {
		requestID = defaultResourceID("request")
	}
	p.requestID = requestID
	p.trace = p.runner.tracer.Start(requestID)
}

func (p *claimedRunProcessor) beginJob() (context.Context, func()) {
	jobCtx, jobCancel := context.WithCancel(p.coordinator.workerCtx)
	p.coordinator.workerWg.Add(1)
	p.coordinator.registerJob(p.run.ID, jobCancel)
	return jobCtx, func() {
		jobCancel()
		p.coordinator.unregisterJob(p.run.ID)
		p.coordinator.workerWg.Done()
	}
}

func (p *claimedRunProcessor) startClaimedRun(ctx context.Context) bool {
	now := time.Now().UTC()

	var queueWaitMS int64
	if !p.run.StartedAt.IsZero() {
		queueWaitMS = now.Sub(p.run.StartedAt).Milliseconds()
	}
	p.queueBackend = ""
	if p.queue != nil {
		p.queueBackend = p.queue.Backend()
	}
	p.trace.Record(telemetry.EventQueueClaimed, map[string]any{
		telemetry.AttrHecateTaskID:       p.task.ID,
		telemetry.AttrHecateRunID:        p.run.ID,
		telemetry.AttrHecateQueueBackend: p.queueBackend,
		telemetry.AttrHecateQueueWaitMS:  queueWaitMS,
		telemetry.AttrHecateWorkerID:     p.coordinator.workerID,
	})
	p.runner.metrics.RecordQueueWait(ctx, telemetry.QueueWaitRecord{
		TaskID:       p.task.ID,
		RunID:        p.run.ID,
		QueueBackend: p.queueBackend,
		WaitMS:       queueWaitMS,
	})

	p.run.Status = "running"
	p.run.RequestID = p.requestID
	p.run.TraceID = p.trace.TraceID
	p.run.RootSpanID = p.trace.RootSpanID()
	if p.run.StartedAt.IsZero() {
		p.run.StartedAt = now
	}
	p.run.LastError = ""
	p.run.FinishedAt = time.Time{}
	if _, err := p.runner.store.UpdateRun(ctx, p.run); err != nil {
		return false
	}

	p.task.Status = "running"
	p.task.LatestRunID = p.run.ID
	if p.task.StartedAt.IsZero() {
		p.task.StartedAt = now
	}
	p.task.UpdatedAt = now
	p.task.FinishedAt = time.Time{}
	p.task.LastError = ""
	p.task.RootTraceID = p.trace.TraceID
	p.task.LatestTraceID = p.trace.TraceID
	p.task.LatestRequestID = p.requestID
	if _, err := p.runner.store.UpdateTask(ctx, p.task); err != nil {
		return false
	}

	recordOrchestratorRunStarted(p.trace, p.task.ID, p.run)
	return true
}

func (p *claimedRunProcessor) emitRunStarted(ctx context.Context) *ResumeCheckpoint {
	resumeCheckpoint, checkpointErr := p.runner.resumeCheckpointForRun(ctx, p.task.ID, p.run.ID)
	if checkpointErr != nil {
		_, _ = p.runner.emitRunEvent(ctx, p.task.ID, p.run.ID, "gap.run_disconnected", p.requestID, p.trace.TraceID, map[string]any{
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
	_, _ = p.runner.emitRunEvent(ctx, p.task.ID, p.run.ID, "run.started", p.requestID, p.trace.TraceID, runEvent)
	return resumeCheckpoint
}

func (p *claimedRunProcessor) executeClaimedRun(ctx context.Context, jobCtx context.Context, resumeCheckpoint *ResumeCheckpoint) {
	if _, err := p.runner.executeRun(ctx, p.trace, p.task, p.run, p.requestID, resumeCheckpoint); err != nil {
		finalStatus := "failed"
		lastError := err.Error()
		if jobCtx.Err() != nil {
			finalStatus = "cancelled"
			lastError = "run cancelled"
		}
		_ = p.runner.finalizeFailedRun(ctx, p.trace, p.task, p.run, p.requestID, finalStatus, lastError)
	}
}

func (p *claimedRunProcessor) recordQueueAcked() {
	p.trace.Record(telemetry.EventQueueAcked, map[string]any{
		telemetry.AttrHecateTaskID:       p.task.ID,
		telemetry.AttrHecateRunID:        p.run.ID,
		telemetry.AttrHecateQueueBackend: p.queueBackend,
	})
}

func (p *claimedRunProcessor) ackClaim() {
	if p.queue != nil {
		_ = p.queue.Ack(context.Background(), p.claim.ClaimID)
	}
}
