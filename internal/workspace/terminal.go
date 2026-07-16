package workspace

import (
	"context"
)

// TerminalOptions configures a new long-lived terminal session. Unlike
// the one-shot Run / RunStreaming surface, the caller drives a
// Terminal interactively — write to stdin, observe streaming output,
// kill or wait for exit on demand.
//
// LocalWorkspace spawns the command under the existing sandbox policy gates,
// owns its Unix process group or Windows Job Object, and keeps the terminal
// alive until the complete owned unit and output pipes drain or the caller
// closes it.
type TerminalOptions struct {
	// Command is the program to execute. When empty, an interactive
	// shell is started (sh -i on unix, cmd.exe on windows). The
	// shell case is what interactive terminal callers expect when an
	// agent asks for a terminal pane it can drive.
	//
	// Unix LocalWorkspace terminals reject known ways literal commands can
	// escape the owned process group. That check is static and best effort;
	// callers must still treat terminal commands as trusted subprocesses.
	Command string

	// Args are additional arguments passed to Command. Ignored when
	// Command is empty.
	Args []string

	// WorkingDirectory is the spawn cwd. Resolved against
	// Policy.AllowedRoot via the same machinery as one-shot Run.
	WorkingDirectory string

	// Policy gates spawn-time decisions (working-directory escape,
	// network access for commands that hint at it).
	Policy Policy

	// Env is additional environment to merge on top of the sanitized
	// base set. Keys collide with the sanitized set on a last-write
	// basis; operators should treat this as override-only and not as
	// a way to pass secrets (use the credential plumbing instead).
	Env map[string]string
}

// Terminal is a long-lived spawn handle. Lifecycle:
//
//	t, err := ws.OpenTerminal(ctx, opts)        // spawn
//	go consume(t.Output())                       // stream stdout/stderr
//	t.Write(ctx, "echo hello\n")                 // optional stdin
//	result, err := t.WaitForExit(ctx)            // block until done
//	t.Close(ctx)                                 // release the handle
//
// Closing a terminal whose process is still running implies Kill
// before release. Callers MUST call Close once per OpenTerminal to
// free goroutines and OS handles.
type Terminal interface {
	// ID is a workspace-unique handle, suitable for telemetry and
	// for cross-referencing ACP terminal/* RPCs. Stable for the
	// lifetime of the terminal.
	ID() string

	// Output is the merged stdout+stderr stream. Each chunk carries
	// its origin in OutputChunk.Stream ("stdout" or "stderr"). The
	// channel is bounded and publishes are non-blocking — a slow
	// or absent consumer drops chunks rather than wedging the
	// terminal. Callers who need the full transcript should
	// consume Output() with a goroutine and / or rely on the
	// bounded retention surfaced by WaitForExit.
	Output() <-chan OutputChunk

	// Write sends bytes to the process's stdin. Blocks until the
	// write completes or stdin is closed (process exit, Kill, or
	// Close). The ctx is reserved for future deadline support;
	// today implementations do not honor it for the inner write.
	Write(ctx context.Context, input string) error

	// WaitForExit blocks until the owned process unit exits and output drains,
	// then returns its
	// exit code plus a bounded snapshot of the stdout / stderr the
	// implementation retained. Retention is best-effort and capped
	// per implementation (currently 256 KiB per stream on
	// LocalWorkspace). Callers who require the full transcript should
	// consume Output() instead. Safe to call concurrently with Output()
	// consumers.
	WaitForExit(ctx context.Context) (Result, error)

	// Kill sends SIGTERM to the owned Unix process group (or terminates the
	// Windows Job Object) and returns
	// immediately. Use WaitForExit afterward to observe the result.
	Kill(ctx context.Context) error

	// Close releases the terminal. If the owned process unit is still running,
	// Close kills it first and waits for output drain within ctx. A deadline can
	// return before drain after force termination; callers may retry. Subsequent
	// calls after completed drain are no-ops.
	Close(ctx context.Context) error
}
