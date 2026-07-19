package orchestrator

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/runtimeevents"
	"github.com/hecatehq/hecate/internal/taskruncoord"
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
	resumeCheckpoint, checkpointErr := p.emitRunStarted(ctx)
	if checkpointErr != nil {
		_ = p.runner.finalizeFailedRun(ctx, p.trace, p.task, p.run, p.requestID, "failed", "resume checkpoint unavailable: "+checkpointErr.Error())
		p.recordQueueAcked()
		p.ackClaim()
		return
	}
	newClaimedRunExecution(p, jobCtx, resumeCheckpoint).execute(ctx)
	p.recordQueueAcked()
	p.ackClaim()
}

func (p *claimedRunProcessor) loadClaimedRun() bool {
	p.queue = p.coordinator.getQueue()
	task, found, err := p.runner.store.GetTask(context.Background(), p.claim.Job.TaskID)
	if err != nil {
		return false
	}
	if !found {
		p.ackClaim()
		return false
	}
	run, found, err := p.runner.store.GetRun(context.Background(), p.claim.Job.TaskID, p.claim.Job.RunID)
	if err != nil {
		return false
	}
	if !found {
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
	job := p.coordinator.registerJob(p.run.ID, jobCancel)
	return jobCtx, func() {
		jobCancel()
		p.coordinator.unregisterJob(p.run.ID, job)
		p.coordinator.workerWg.Done()
	}
}

func (p *claimedRunProcessor) startClaimedRun(ctx context.Context) bool {
	originLease, err := p.runner.beginOriginRunMutation(ctx, p.task)
	if err != nil {
		if errors.Is(err, taskruncoord.ErrOriginUnavailable) {
			p.settleUnavailableOriginClaim(ctx)
		}
		return false
	}
	if originLease != nil {
		defer originLease.Release()
	}

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

	applied, err := persistClaimedRunStartTransition(ctx, p.runner.store, transition)
	if err != nil {
		return false
	}
	if !applied {
		// A concurrent cancellation or another valid claimant won the
		// queued-state compare-and-swap. This queue claim is obsolete.
		p.ackClaim()
		return false
	}

	// Downstream run.started emission, execution, and queue-ack telemetry
	// all read the processor's current run/task snapshots.
	p.run = transition.Run
	p.task = transition.Task

	recordOrchestratorRunStarted(p.trace, p.task.ID, p.run)
	return true
}

func (p *claimedRunProcessor) settleUnavailableOriginClaim(ctx context.Context) {
	message := "run cancelled: task origin is unavailable"
	result, err := p.runner.applyTerminalRunTransition(ctx, cancelRunTerminalTransition(
		p.task,
		p.run,
		message,
		p.requestID,
		p.trace.TraceID,
		p.trace,
		time.Now().UTC(),
	))
	if err == nil && (result.Skipped || result.Run.Status == "cancelled") {
		p.ackClaim()
	}
}

func (p *claimedRunProcessor) emitRunStarted(ctx context.Context) (*ResumeCheckpoint, error) {
	resumeCheckpoint, checkpointErr := p.runner.resumeCheckpointForRun(ctx, p.task.ID, p.run.ID)
	if checkpointErr != nil {
		_, _ = p.runner.emitRunEvent(ctx, p.task.ID, p.run.ID, runtimeevents.EventGapRunDisconnected.String(), p.requestID, p.trace.TraceID, map[string]any{
			"reason":  "resume_checkpoint_unavailable",
			"action":  "fail_run",
			"message": checkpointErr.Error(),
		})
		return nil, checkpointErr
	}
	runEvent := map[string]any{}
	if resumeCheckpoint != nil {
		runEvent["resume_from_run_id"] = resumeCheckpoint.SourceRunID
		runEvent["resume_from_step_id"] = resumeCheckpoint.LastCompletedStepID
		runEvent["resume_from_event_sequence"] = resumeCheckpoint.LastEventSequence
	}
	_, _ = p.runner.emitRunEvent(ctx, p.task.ID, p.run.ID, runtimeevents.EventRunStarted.String(), p.requestID, p.trace.TraceID, runEvent)
	return resumeCheckpoint, nil
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
