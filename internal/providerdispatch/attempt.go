// Package providerdispatch carries the narrow, internal hook that lets a
// task-backed agent persist its rich-input route immediately before gateway
// I/O. It deliberately depends only on the shared route contract so the
// gateway does not depend on the orchestrator.
package providerdispatch

import (
	"context"

	"github.com/hecatehq/hecate/pkg/types"
)

// AttemptRecorder persists the concrete route that is about to receive a
// provider-bound input. Returning an error prevents the gateway from sending
// the request, which keeps durable recovery state and provider dispatch in
// lock-step.
type AttemptRecorder func(types.RouteDecision) error

type attemptRecorderContextKey struct{}

type attemptRecorderState struct {
	recorder AttemptRecorder
}

// WithAttemptRecorder attaches an optional recorder to a request context.
// The hook is internal transport plumbing: ordinary gateway callers do not
// install one and retain the existing behavior.
func WithAttemptRecorder(ctx context.Context, recorder AttemptRecorder) context.Context {
	if recorder == nil {
		return ctx
	}
	return context.WithValue(ctx, attemptRecorderContextKey{}, &attemptRecorderState{recorder: recorder})
}

// RecordAttempt invokes the task-runtime recorder installed by
// WithAttemptRecorder. A missing recorder is intentionally a no-op so this
// remains safe for public gateway and direct-model traffic.
func RecordAttempt(ctx context.Context, route types.RouteDecision) error {
	state, _ := ctx.Value(attemptRecorderContextKey{}).(*attemptRecorderState)
	if state == nil || state.recorder == nil {
		return nil
	}
	if err := state.recorder(route); err != nil {
		return err
	}
	return nil
}
