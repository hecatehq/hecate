// Package workspace is the abstraction over whatever owns the files and
// processes the agent operates on. It exists so the orchestrator can be
// driven by either:
//
//   - LocalWorkspace, which is today's behavior: file writes land on the
//     host filesystem under Policy.AllowedRoot and shell commands run
//     through the existing sandbox shell wrapper. HTTP chat and
//     agent_loop tasks use this path.
//
//   - ACPWorkspace (added later in the refactor), which dispatches
//     file and terminal operations back to the connected ACP editor
//     as reverse-RPC calls. Used by `hecate-acp` when the operator
//     opts into editor-owned mode.
//
// The interface is a 1:1 mirror of sandbox.Executor today; the
// additional ACP-shaped primitives (long-lived terminals, dynamic
// permission gates) land in later commits without breaking call sites
// that only need the four methods below.
package workspace

import (
	"context"

	"github.com/hecate/agent-runtime/internal/sandbox"
)

// Re-export sandbox value types so call sites can stay in the
// workspace package after the refactor moves callers off
// sandbox.Executor. The aliases are zero-cost — Go treats them as
// the same type — and they collapse to nothing if the sandbox
// package ever absorbs into workspace.
type (
	Command     = sandbox.Command
	FileRequest = sandbox.FileRequest
	Result      = sandbox.Result
	FileResult  = sandbox.FileResult
	OutputChunk = sandbox.OutputChunk
	Policy      = sandbox.Policy
)

// Workspace is the boundary between the orchestrator and whatever owns
// the agent's filesystem and processes. The four methods mirror the
// current sandbox.Executor surface so the swap from
// `sandbox.Executor` → `workspace.Workspace` at the orchestrator
// layer is mechanical. ACP-shaped additions (OpenTerminal,
// RequestPermission) land later in the refactor.
type Workspace interface {
	Run(ctx context.Context, command Command) (Result, error)
	RunStreaming(ctx context.Context, command Command, onChunk func(OutputChunk)) (Result, error)
	WriteFile(ctx context.Context, request FileRequest) (FileResult, error)
	AppendFile(ctx context.Context, request FileRequest) (FileResult, error)
}
