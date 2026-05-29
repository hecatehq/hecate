package orchestrator

import (
	"context"

	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/pkg/types"
)

type claimedRunExecution struct {
	runner           *Runner
	trace            *profiler.Trace
	task             types.Task
	run              types.TaskRun
	requestID        string
	resumeCheckpoint *ResumeCheckpoint
	jobCtx           context.Context
}

func newClaimedRunExecution(p *claimedRunProcessor, jobCtx context.Context, resumeCheckpoint *ResumeCheckpoint) claimedRunExecution {
	return claimedRunExecution{
		runner:           p.runner,
		trace:            p.trace,
		task:             p.task,
		run:              p.run,
		requestID:        p.requestID,
		resumeCheckpoint: resumeCheckpoint,
		jobCtx:           jobCtx,
	}
}

func (e claimedRunExecution) execute(ctx context.Context) {
	if _, err := e.runner.executeRun(ctx, e.trace, e.task, e.run, e.requestID, e.resumeCheckpoint); err != nil {
		status, lastError := e.failureTerminalState(err)
		_ = e.runner.finalizeFailedRun(ctx, e.trace, e.task, e.run, e.requestID, status, lastError)
	}
}

func (e claimedRunExecution) failureTerminalState(err error) (status string, lastError string) {
	if e.jobCtx != nil && e.jobCtx.Err() != nil {
		return "cancelled", "run cancelled"
	}
	return "failed", err.Error()
}
