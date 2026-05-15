package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"sync"
	"time"
)

// OpenTerminal asks the editor to spawn a long-lived terminal and
// returns a handle that polls terminal/output for streaming reads
// and dispatches terminal/wait_for_exit / terminal/kill / terminal/
// release on demand. The editor owns the actual child process —
// rendering it in its terminal pane, surfacing it in its UI — and
// Hecate just orchestrates.
func (w *ACPWorkspace) OpenTerminal(ctx context.Context, opts TerminalOptions) (Terminal, error) {
	if err := w.ensureWired(); err != nil {
		return nil, err
	}
	command, args := terminalSpawnSpec(opts)
	terminalID, err := w.createTerminal(ctx, acpTerminalCreateParams{
		SessionID: w.sessionID,
		Command:   command,
		Args:      args,
		Cwd:       opts.WorkingDirectory,
		Env:       opts.Env,
	})
	if err != nil {
		return nil, err
	}

	pollCtx, cancelPoll := context.WithCancel(context.Background())
	waitCtx, cancelWait := context.WithCancel(context.Background())
	term := &acpTerminal{
		caller:     w.caller,
		sessionID:  w.sessionID,
		terminalID: terminalID,
		output:     make(chan OutputChunk, 64),
		exit:       make(chan struct{}),
		pollDone:   make(chan struct{}),
		cancelPoll: cancelPoll,
		cancelWait: cancelWait,
	}
	// Polling goroutine drains terminal/output until the editor
	// reports exit or the caller closes the terminal. Mirrors the
	// LocalWorkspace.OpenTerminal goroutine model — Output() is a
	// channel callers can range over.
	go term.pollOutput(pollCtx)
	// Wait goroutine resolves terminal/wait_for_exit so
	// WaitForExit callers don't each have to issue their own RPC.
	// waitCtx lets Close cancel a hung RPC if the editor never
	// responds — without it, awaitExit would leak forever on
	// transport loss.
	go term.awaitExit(waitCtx)

	return term, nil
}

// terminalSpawnSpec picks the (command, args) pair the editor should
// spawn. Mirrors the LocalWorkspace behavior: empty Command means
// "interactive shell," Args are passed through unchanged. The
// editor's terminal subsystem is responsible for shell selection —
// Zed defaults to the user's $SHELL, JetBrains uses its terminal
// settings — so we only need to pass an empty command to signal "no
// override."
func terminalSpawnSpec(opts TerminalOptions) (string, []string) {
	if opts.Command == "" {
		return "", nil
	}
	return opts.Command, opts.Args
}

// acpRunShellSpec wraps a freeform shell string for ACP terminal/create.
// Run/RunStreaming receive Command.Command as a single shell line
// ("git status", "cd repo && make test"), so the editor needs a shell
// to parse and execute it. We pick based on the gateway's GOOS —
// which today equals the editor host's GOOS (the bridge always runs
// loopback per cmd/hecate-acp/main.go). Cross-host editors are a
// future RFC; when they land they'll negotiate shell flavor through
// ACP capabilities.
func acpRunShellSpec(command string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd.exe", []string{"/C", command}
	}
	return "sh", []string{"-lc", command}
}

// acpTerminal is the editor-owned Terminal handle returned by
// ACPWorkspace.OpenTerminal. Each handle owns one terminal id, one
// polling goroutine, and one wait goroutine. Close serializes
// teardown through a sync.Once so concurrent Kill / WaitForExit /
// Close don't race on terminal/release.
type acpTerminal struct {
	caller     Caller
	sessionID  string
	terminalID string

	output   chan OutputChunk
	exit     chan struct{}
	pollDone chan struct{} // closed by pollOutput when it returns

	// stdout/stderr seen-so-far buffers track what we've already
	// streamed via Output() — same shape as ACPWorkspace.drainOnce
	// uses for one-shot Run.
	bufMu     sync.Mutex
	stdoutBuf strings.Builder
	stderrBuf strings.Builder

	exitResult acpTerminalWaitResult
	exitErr    error

	cancelPoll func()
	cancelWait func()

	closeOnce sync.Once
}

func (t *acpTerminal) ID() string                 { return t.terminalID }
func (t *acpTerminal) Output() <-chan OutputChunk { return t.output }

func (t *acpTerminal) Write(_ context.Context, _ string) error {
	// ACP's terminal surface today is read-only from the agent's
	// perspective — there's no terminal/input or terminal/write
	// method. Long-lived interactive terminals driven by the agent
	// aren't part of the v1 reverse-RPC contract. Surface this
	// explicitly so callers don't silently lose stdin.
	return errors.New("acp workspace: terminal stdin is not supported by the current ACP surface")
}

func (t *acpTerminal) WaitForExit(ctx context.Context) (Result, error) {
	select {
	case <-t.exit:
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	// pollOutput may still be running concurrently — even though
	// exit closed, the poller doesn't bail until Close cancels it.
	// Take bufMu while snapshotting so we don't race a concurrent
	// WriteString on the builders.
	t.bufMu.Lock()
	stdout := t.stdoutBuf.String()
	stderr := t.stderrBuf.String()
	t.bufMu.Unlock()
	if t.exitErr != nil {
		return Result{Stdout: stdout, Stderr: stderr}, t.exitErr
	}
	return Result{
		Stdout:   stdout,
		Stderr:   stderr,
		ExitCode: t.exitResult.ExitCode,
	}, nil
}

func (t *acpTerminal) Kill(ctx context.Context) error {
	_, err := t.caller.Call(ctx, "terminal/kill", acpTerminalIDParams{
		SessionID:  t.sessionID,
		TerminalID: t.terminalID,
	})
	return err
}

func (t *acpTerminal) Close(ctx context.Context) error {
	var releaseErr error
	t.closeOnce.Do(func() {
		// Kill is best-effort: the process may already have exited.
		_ = t.Kill(ctx)
		// Stop the polling goroutine before releasing the handle
		// so terminal/output calls don't race terminal/release.
		t.cancelPoll()
		// Wait for the poller to actually return before closing
		// t.output — otherwise a late publishChunk could panic on
		// send-to-closed-channel.
		<-t.pollDone
		// Wait for the editor-side process to actually exit. The
		// wait goroutine closes t.exit when terminal/wait_for_exit
		// returns; bound the wait so a wedged editor doesn't pin
		// Close forever. If we time out or ctx cancels, cancel the
		// wait RPC explicitly so its goroutine doesn't leak.
		select {
		case <-t.exit:
		case <-time.After(5 * time.Second):
			t.cancelWait()
			<-t.exit
		case <-ctx.Done():
			t.cancelWait()
			<-t.exit
		}
		_, releaseErr = t.caller.Call(ctx, "terminal/release", acpTerminalIDParams{
			SessionID:  t.sessionID,
			TerminalID: t.terminalID,
		})
		close(t.output)
	})
	return releaseErr
}

// pollOutput is the streaming side of the terminal handle. Same
// cadence and same delta-by-prefix logic as ACPWorkspace.drainOnce
// uses for one-shot Run; differences are (a) we keep going until
// cancelPoll fires (Close was called) or the wait goroutine observes
// exit, and (b) we deliver chunks through the channel rather than
// calling onChunk.
func (t *acpTerminal) pollOutput(ctx context.Context) {
	defer close(t.pollDone)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.exit:
			// One last drain so WaitForExit sees any trailing
			// output buffered between the previous tick and exit.
			t.drainOnce(ctx)
			return
		case <-ticker.C:
		}
		t.drainOnce(ctx)
	}
}

func (t *acpTerminal) drainOnce(ctx context.Context) {
	raw, err := t.caller.Call(ctx, "terminal/output", acpTerminalIDParams{
		SessionID:  t.sessionID,
		TerminalID: t.terminalID,
	})
	if err != nil {
		// Best-effort polling — the editor may have already
		// released the terminal; the wait goroutine will
		// observe the exit shortly and we'll bail out.
		return
	}
	var out acpTerminalOutputResult
	if jerr := json.Unmarshal(raw, &out); jerr != nil {
		return
	}
	t.bufMu.Lock()
	defer t.bufMu.Unlock()
	if delta := stringSuffixAfter(&t.stdoutBuf, out.Stdout); delta != "" {
		t.stdoutBuf.WriteString(delta)
		t.publishChunk(OutputChunk{Stream: "stdout", Text: delta})
	}
	if delta := stringSuffixAfter(&t.stderrBuf, out.Stderr); delta != "" {
		t.stderrBuf.WriteString(delta)
		t.publishChunk(OutputChunk{Stream: "stderr", Text: delta})
	}
}

func (t *acpTerminal) publishChunk(chunk OutputChunk) {
	// Non-blocking publish: if the consumer is slow we'd rather
	// drop than wedge the poller. Bounded channel + drop-on-full
	// matches what real editors expect from a "tail this terminal"
	// surface anyway.
	select {
	case t.output <- chunk:
	default:
	}
}

func (t *acpTerminal) awaitExit(ctx context.Context) {
	raw, err := t.caller.Call(ctx, "terminal/wait_for_exit", acpTerminalIDParams{
		SessionID:  t.sessionID,
		TerminalID: t.terminalID,
	})
	if err != nil {
		t.exitErr = err
		close(t.exit)
		return
	}
	if jerr := json.Unmarshal(raw, &t.exitResult); jerr != nil {
		t.exitErr = jerr
	}
	close(t.exit)
}
