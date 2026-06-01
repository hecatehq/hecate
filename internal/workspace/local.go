package workspace

import (
	"context"

	"github.com/hecatehq/hecate/internal/sandbox"
)

// LocalWorkspace is the on-host implementation. Every method delegates
// to sandbox.LocalExecutor today; the wrapper exists so the call
// graph already routes through the workspace abstraction.
//
// Stateless: LocalExecutor itself holds no state per call, and any
// future per-workspace state (caches, telemetry context) belongs on
// LocalWorkspace, not behind it.
type LocalWorkspace struct {
	exec *sandbox.LocalExecutor
}

// NewLocalWorkspace constructs the local implementation around a fresh
// sandbox.LocalExecutor.
func NewLocalWorkspace() *LocalWorkspace {
	return &LocalWorkspace{exec: sandbox.NewLocalExecutor()}
}

// NewLocalWorkspaceFromExecutor wraps an existing executor. Used by
// tests and by call sites that want to inject a stubbed executor for
// behavioral isolation.
func NewLocalWorkspaceFromExecutor(exec *sandbox.LocalExecutor) *LocalWorkspace {
	if exec == nil {
		exec = sandbox.NewLocalExecutor()
	}
	return &LocalWorkspace{exec: exec}
}

func (w *LocalWorkspace) Run(ctx context.Context, command Command) (Result, error) {
	return w.exec.Run(ctx, command)
}

func (w *LocalWorkspace) RunStreaming(ctx context.Context, command Command, onChunk func(OutputChunk)) (Result, error) {
	return w.exec.RunStreaming(ctx, command, onChunk)
}

func (w *LocalWorkspace) WriteFile(ctx context.Context, request FileRequest) (FileResult, error) {
	return w.exec.WriteFile(ctx, request)
}

func (w *LocalWorkspace) AppendFile(ctx context.Context, request FileRequest) (FileResult, error) {
	return w.exec.AppendFile(ctx, request)
}

// Compile-time check that LocalWorkspace satisfies the Workspace
// interface. Catches accidental method-signature drift between the
// interface and the implementation at build time.
var _ Workspace = (*LocalWorkspace)(nil)
