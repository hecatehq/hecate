package orchestrator

import (
	"context"
	"fmt"
	"time"

	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/runtimeevents"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

type executionResultPersister struct {
	runner    *Runner
	trace     *profiler.Trace
	task      types.Task
	run       types.TaskRun
	requestID string
}

func newExecutionResultPersister(runner *Runner, trace *profiler.Trace, task types.Task, run types.TaskRun, requestID string) executionResultPersister {
	return executionResultPersister{
		runner:    runner,
		trace:     trace,
		task:      task,
		run:       run,
		requestID: requestID,
	}
}

func (p executionResultPersister) persist(ctx context.Context, execution *ExecutionResult) (*StartTaskResult, error) {
	persistedSteps, err := p.persistSteps(ctx, execution.Steps)
	if err != nil {
		return nil, err
	}

	persistedArtifacts, err := p.persistArtifacts(ctx, execution.Artifacts)
	if err != nil {
		return nil, err
	}

	p.emitModelCallCostEvents(ctx, execution.ModelCallCosts)
	if err := p.persistPendingApprovals(ctx, execution.PendingApprovals); err != nil {
		return nil, err
	}

	return p.applyFinalResult(ctx, execution, persistedSteps, persistedArtifacts)
}

func (p executionResultPersister) persistSteps(ctx context.Context, steps []types.TaskStep) ([]types.TaskStep, error) {
	persistedSteps := make([]types.TaskStep, 0, len(steps))
	for _, step := range steps {
		p.recordStep(ctx, step)
		step.SpanID = spanIDByName(p.trace, "orchestrator.step")
		step.ParentSpanID = p.trace.RootSpanID()
		if err := p.runner.upsertStep(ctx, step); err != nil {
			return nil, err
		}
		persistedSteps = append(persistedSteps, step)
	}
	return persistedSteps, nil
}

func (p executionResultPersister) recordStep(ctx context.Context, step types.TaskStep) {
	eventName := telemetry.EventOrchestratorStepCompleted
	if step.Status == "failed" || step.Status == "cancelled" || step.Result == telemetry.ResultError {
		eventName = telemetry.EventOrchestratorStepFailed
	}
	var stepDurationMS int64
	if !step.StartedAt.IsZero() && !step.FinishedAt.IsZero() {
		stepDurationMS = step.FinishedAt.Sub(step.StartedAt).Milliseconds()
	}
	stepAttrs := map[string]any{
		telemetry.AttrHecatePhase:        firstNonEmpty(step.Phase, "execution"),
		telemetry.AttrHecateResult:       firstNonEmpty(step.Result, telemetry.ResultSuccess),
		telemetry.AttrHecateTaskID:       p.task.ID,
		telemetry.AttrHecateRunID:        p.run.ID,
		telemetry.AttrHecateStepID:       step.ID,
		telemetry.AttrHecateStepKind:     step.Kind,
		telemetry.AttrHecateStepIndex:    step.Index,
		telemetry.AttrHecateStepToolName: step.ToolName,
	}
	if stepDurationMS > 0 {
		stepAttrs[telemetry.AttrHecateStepDurationMS] = stepDurationMS
	}
	mergeStepTelemetryAttrs(stepAttrs, step.Input)
	mergeStepTelemetryAttrs(stepAttrs, step.OutputSummary)
	p.trace.Record(eventName, stepAttrs)
	p.runner.metrics.RecordStep(ctx, telemetry.StepMetricsRecord{
		TaskID:     p.task.ID,
		RunID:      p.run.ID,
		StepKind:   step.Kind,
		Result:     firstNonEmpty(step.Result, telemetry.ResultSuccess),
		DurationMS: stepDurationMS,
	})
}

func (p executionResultPersister) persistArtifacts(ctx context.Context, artifacts []types.TaskArtifact) ([]types.TaskArtifact, error) {
	persistedArtifacts := make([]types.TaskArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		persistedArtifact, err := p.persistArtifact(ctx, artifact, true)
		if err != nil {
			return nil, err
		}
		persistedArtifacts = append(persistedArtifacts, persistedArtifact)
	}
	if artifact, ok := p.runner.gitSummaryArtifact(ctx, p.task, p.run, p.requestID, p.trace.TraceID); ok {
		persistedArtifact, err := p.persistArtifact(ctx, artifact, false)
		if err != nil {
			return nil, err
		}
		persistedArtifacts = append(persistedArtifacts, persistedArtifact)
	}
	return persistedArtifacts, nil
}

func (p executionResultPersister) persistArtifact(ctx context.Context, artifact types.TaskArtifact, includeStepID bool) (types.TaskArtifact, error) {
	attrs := map[string]any{
		telemetry.AttrHecatePhase:             "artifact",
		telemetry.AttrHecateResult:            telemetry.ResultSuccess,
		telemetry.AttrHecateTaskID:            p.task.ID,
		telemetry.AttrHecateRunID:             p.run.ID,
		telemetry.AttrHecateArtifactID:        artifact.ID,
		telemetry.AttrHecateArtifactKind:      artifact.Kind,
		telemetry.AttrHecateArtifactSizeBytes: artifact.SizeBytes,
	}
	if includeStepID {
		attrs[telemetry.AttrHecateStepID] = artifact.StepID
	}
	p.trace.Record(telemetry.EventOrchestratorArtifactCreated, attrs)
	artifact.SpanID = spanIDByName(p.trace, "orchestrator.artifact")
	return artifact, p.runner.upsertArtifact(ctx, artifact)
}

func (p executionResultPersister) emitModelCallCostEvents(ctx context.Context, modelCallCosts []ModelCallCostRecord) {
	// Per-model-call cost telemetry. The agent loop reports ModelCallCosts —
	// one entry per LLM round-trip — and we emit a `model.call.completed`
	// event for each. Operators replay these via the events feed to
	// see how spend evolved across the run; the cumulative figure
	// includes prior runs in the resume chain so a long chain shows
	// total task spend, not just per-run.
	for _, modelCallCost := range modelCallCosts {
		_, _ = p.runner.emitRunEvent(ctx, p.task.ID, p.run.ID, runtimeevents.EventModelCallCompleted.String(), p.requestID, p.trace.TraceID, runtimeevents.ModelCallCompleted(runtimeevents.ModelCallCompletedFields{
			ModelCallIndex:              modelCallCost.ModelCall,
			StepID:                      modelCallCost.StepID,
			CostMicrosUSD:               modelCallCost.CostMicrosUSD,
			RunCumulativeCostMicrosUSD:  modelCallCost.CumulativeMicrosUSD,
			TaskCumulativeCostMicrosUSD: p.run.PriorCostMicrosUSD + modelCallCost.CumulativeMicrosUSD,
			ToolCalls:                   modelCallCost.ToolCallCount,
		}))
	}
}

func (p executionResultPersister) persistPendingApprovals(ctx context.Context, approvals []types.TaskApproval) error {
	// Persist mid-loop approvals the executor emitted (agent_loop
	// pauses on gated tool calls). The runner owns the store
	// touch-points, so executors return the approvals via
	// ExecutionResult and we write them here. Skipped on non-paused
	// executions — PendingApprovals is empty.
	for _, approval := range approvals {
		if approval.SpanID == "" {
			approval.SpanID = p.trace.RootSpanID()
		}
		if _, err := p.runner.store.CreateApproval(ctx, approval); err != nil {
			return err
		}
		_, _ = p.runner.emitRunEvent(ctx, p.task.ID, p.run.ID, runtimeevents.EventApprovalRequested.String(), p.requestID, p.trace.TraceID, runtimeevents.ApprovalRequested(approval))
	}
	return nil
}

func (p executionResultPersister) applyFinalResult(ctx context.Context, execution *ExecutionResult, persistedSteps []types.TaskStep, persistedArtifacts []types.TaskArtifact) (*StartTaskResult, error) {
	// Steps and artifacts are upserted while an agent loop is running, before
	// this terminal pass. A same-Run recovery result contains only children
	// produced by the recovery invocation, so count from the complete durable
	// sets instead of overwriting Run totals with that partial slice.
	durableSteps, err := p.runner.store.ListSteps(ctx, p.run.ID)
	if err != nil {
		return nil, fmt.Errorf("list durable steps before Run settlement: %w", err)
	}
	durableArtifacts, err := p.runner.store.ListArtifacts(ctx, taskstate.ArtifactFilter{TaskID: p.task.ID, RunID: p.run.ID})
	if err != nil {
		return nil, fmt.Errorf("list durable artifacts before Run settlement: %w", err)
	}
	durableModelCallCount, err := completedModelCallCountFromSteps(durableSteps)
	if err != nil {
		return nil, fmt.Errorf("reconstruct completed model-call count before Run settlement: %w", err)
	}
	terminalExecution := *execution
	if durableModelCallCount > terminalExecution.ModelCallCount {
		terminalExecution.ModelCallCount = durableModelCallCount
	}

	finishedAt := time.Now().UTC()
	transitionInput := executionResultTerminalTransitionInput{
		Task:               p.task,
		Run:                p.run,
		Execution:          &terminalExecution,
		PersistedSteps:     durableSteps,
		PersistedArtifacts: durableArtifacts,
		RequestID:          p.requestID,
		Trace:              p.trace,
		FinishedAt:         finishedAt,
	}
	terminalTransition := executionResultTerminalTransition(transitionInput)
	transition, err := p.runner.applyTerminalRunTransition(ctx, terminalTransition)
	if err != nil {
		return nil, err
	}

	resultKind := telemetry.ResultSuccess
	if transition.Run.Status == "failed" || transition.Run.Status == "cancelled" {
		resultKind = telemetry.ResultError
	}
	durationEnd := transition.Run.FinishedAt
	if durationEnd.IsZero() {
		durationEnd = finishedAt
	}
	runDurationMS := int64(0)
	if !transition.Run.StartedAt.IsZero() && durationEnd.After(transition.Run.StartedAt) {
		runDurationMS = durationEnd.Sub(transition.Run.StartedAt).Milliseconds()
	}
	runFinishedAttrs := map[string]any{
		telemetry.AttrHecatePhase:  "orchestration",
		telemetry.AttrHecateResult: resultKind,
		telemetry.AttrHecateTaskID: transition.Task.ID,
		telemetry.AttrHecateRunID:  transition.Run.ID,
	}
	if runDurationMS > 0 {
		runFinishedAttrs[telemetry.AttrHecateRunDurationMS] = runDurationMS
	}
	p.trace.Record(telemetry.EventOrchestratorRunFinished, runFinishedAttrs)
	p.trace.Record(telemetry.EventOrchestratorTaskFinished, map[string]any{
		telemetry.AttrHecatePhase:  "orchestration",
		telemetry.AttrHecateResult: resultKind,
		telemetry.AttrHecateTaskID: transition.Task.ID,
	})
	p.runner.metrics.RecordRun(ctx, telemetry.RunMetricsRecord{
		TaskID:        transition.Task.ID,
		RunID:         transition.Run.ID,
		Status:        transition.Run.Status,
		ExecutionKind: p.task.ExecutionKind,
		Model:         transition.Run.Model,
		DurationMS:    runDurationMS,
	})

	return &StartTaskResult{
		Task:      transition.Task,
		Run:       transition.Run,
		Steps:     persistedSteps,
		Artifacts: persistedArtifacts,
		TraceID:   p.trace.TraceID,
		SpanID:    p.trace.RootSpanID(),
	}, nil
}
