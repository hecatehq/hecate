package taskapp

import (
	"context"
	"time"

	"github.com/hecatehq/hecate/internal/taskruncoord"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

type SteerRunCommand struct {
	RunID, ClientRequestID, Content, RequestID, TraceID string
}

func (app *Application) SteerRun(ctx context.Context, task types.Task, cmd SteerRunCommand) (taskstate.RunSteeringResult, error) {
	if app == nil || app.store == nil {
		return taskstate.RunSteeringResult{}, ErrStoreNotConfigured
	}
	in := taskstate.RunSteeringSubmission{TaskID: task.ID, RunID: cmd.RunID, ClientRequestID: cmd.ClientRequestID,
		Content: cmd.Content, RequestID: cmd.RequestID, TraceID: cmd.TraceID, CreatedAt: app.now().UTC()}
	if err := taskstate.ValidateRunSteeringSubmission(in); err != nil {
		return taskstate.RunSteeringResult{}, Validation(err)
	}
	store, ok := app.store.(taskstate.RunSteeringStore)
	if !ok {
		return taskstate.RunSteeringResult{}, taskstate.ErrSteeringUnavailable
	}
	lease, err := app.originRunGate.Begin(ctx, taskruncoord.Origin{Kind: task.OriginKind, ID: task.OriginID})
	if err != nil {
		return taskstate.RunSteeringResult{}, err
	}
	defer lease.Release()
	if err := ctx.Err(); err != nil {
		return taskstate.RunSteeringResult{}, err
	}
	// Once admitted, finish this bounded atomic write even if the HTTP waiter
	// disconnects. Same-key replay supplies the authoritative outcome.
	commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return store.SubmitRunSteering(commitCtx, in)
}
