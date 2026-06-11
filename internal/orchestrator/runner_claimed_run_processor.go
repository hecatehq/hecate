package orchestrator

import (
	"context"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/runtimeevents"
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
	newClaimedRunExecution(p, jobCtx, resumeCheckpoint).execute(ctx)
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
	transition := prepareClaimedRunStartTransition(claimedRunStartTransitionInput{
		Task:       p.task,
		Run:        p.run,
		RequestID:  p.requestID,
		TraceID:    p.trace.TraceID,
		RootSpanID: p.trace.RootSpanID(),
		Now:        now,
	})
	p.queueBackend = ""
	if p.queue != nil {
		p.queueBackend = p.queue.Backend()
	}
	p.trace.Record(telemetry.EventQueueClaimed, map[string]any{
		telemetry.AttrHecateTaskID:       p.task.ID,
		telemetry.AttrHecateRunID:        p.run.ID,
		telemetry.AttrHecateQueueBackend: p.queueBackend,
		telemetry.AttrHecateQueueWaitMS:  transition.QueueWaitMS,
		telemetry.AttrHecateWorkerID:     p.coordinator.workerID,
	})
	p.runner.metrics.RecordQueueWait(ctx, telemetry.QueueWaitRecord{
		TaskID:       p.task.ID,
		RunID:        p.run.ID,
		QueueBackend: p.queueBackend,
		WaitMS:       transition.QueueWaitMS,
	})

	if err := persistClaimedRunStartTransition(ctx, p.runner.store, transition); err != nil {
		return false
	}

	// Downstream run.started emission, execution, and queue-ack telemetry
	// all read the processor's current run/task snapshots.
	p.run = transition.Run
	p.task = transition.Task

	recordOrchestratorRunStarted(p.trace, p.task.ID, p.run)
	return true
}

func (p *claimedRunProcessor) emitRunStarted(ctx context.Context) *ResumeCheckpoint {
	resumeCheckpoint, checkpointErr := p.runner.resumeCheckpointForRun(ctx, p.task.ID, p.run.ID)
	if checkpointErr != nil {
		_, _ = p.runner.emitRunEvent(ctx, p.task.ID, p.run.ID, runtimeevents.EventGapRunDisconnected.String(), p.requestID, p.trace.TraceID, map[string]any{
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
	_, _ = p.runner.emitRunEvent(ctx, p.task.ID, p.run.ID, runtimeevents.EventRunStarted.String(), p.requestID, p.trace.TraceID, runEvent)
	return resumeCheckpoint
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
