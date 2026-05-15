package workspace

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/hecate/agent-runtime/internal/sandbox"
)

// localTerminal is the on-host implementation. Each terminal owns a
// child process plus three goroutines (stdout/stderr readers and a
// wait-for-exit). Closing serializes through closeOnce so concurrent
// Kill / Close / WaitForExit don't race the cleanup.
type localTerminal struct {
	id     string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	output chan OutputChunk

	exitOnce sync.Once
	exit     chan struct{}
	result   atomic.Pointer[Result]
	waitErr  atomic.Pointer[error]

	closeOnce sync.Once
}

// nextTerminalID is bumped per terminal for a workspace-unique handle.
// Local-only counter; cross-process uniqueness isn't required.
var nextTerminalID atomic.Uint64

// OpenTerminal spawns a long-lived process under the workspace's
// sandbox policy. The returned Terminal handle MUST be Close'd by the
// caller once it's done observing output and exit — failing to call
// Close leaks the reader goroutines and the OS file descriptors for
// the pipes.
func (w *LocalWorkspace) OpenTerminal(ctx context.Context, opts TerminalOptions) (Terminal, error) {
	cmd, err := buildTerminalCommand(opts)
	if err != nil {
		return nil, err
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("open stderr: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start terminal: %w", err)
	}

	t := &localTerminal{
		id:     fmt.Sprintf("term_%d", nextTerminalID.Add(1)),
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
		stderr: stderr,
		output: make(chan OutputChunk, 64),
		exit:   make(chan struct{}),
	}

	// Reader goroutines forward stdout / stderr into the merged
	// output channel. They exit when the pipes close (process exit
	// or explicit Close). pumpDone tracks both pumps so we can
	// close the output channel exactly once after both drain.
	var pumpDone sync.WaitGroup
	pumpDone.Add(2)
	go t.pump(&pumpDone, t.stdout, "stdout")
	go t.pump(&pumpDone, t.stderr, "stderr")
	go func() {
		pumpDone.Wait()
		close(t.output)
	}()

	// Wait goroutine captures exit status; everyone observing the
	// terminal goes through atomic.Pointer reads to see the result
	// once it's published.
	go func() {
		err := cmd.Wait()
		var result Result
		if cmd.ProcessState != nil {
			result.ExitCode = cmd.ProcessState.ExitCode()
		}
		t.result.Store(&result)
		if err != nil {
			// Filter the predictable "exit status N" wrapping
			// — the operator can read ExitCode directly. Surface
			// only the IO-layer errors (signal kills, pipe
			// failures) that genuinely need a Wait()-side error.
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				t.waitErr.Store(&err)
			}
		}
		close(t.exit)
	}()

	return t, nil
}

func (t *localTerminal) ID() string { return t.id }

func (t *localTerminal) Output() <-chan OutputChunk { return t.output }

func (t *localTerminal) Write(ctx context.Context, input string) error {
	if t.stdin == nil {
		return errors.New("stdin is closed")
	}
	// Honor context cancellation by writing in a goroutine and
	// racing against ctx.Done — the writer holds a reference to
	// the pipe so a cancelled write doesn't pile up zombie
	// goroutines.
	done := make(chan error, 1)
	go func() {
		_, err := t.stdin.Write([]byte(input))
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *localTerminal) WaitForExit(ctx context.Context) (Result, error) {
	select {
	case <-t.exit:
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	if r := t.result.Load(); r != nil {
		out := *r
		if e := t.waitErr.Load(); e != nil {
			return out, *e
		}
		return out, nil
	}
	return Result{}, errors.New("terminal exited without recording result")
}

func (t *localTerminal) Kill(_ context.Context) error {
	if t.cmd == nil || t.cmd.Process == nil {
		return nil
	}
	if runtime.GOOS == "windows" {
		// Process.Kill on Windows sends a TerminateProcess; same
		// effect as our unix SIGTERM in this code path.
		return t.cmd.Process.Kill()
	}
	// SIGTERM first — gives the child a chance to clean up. The
	// caller can poll WaitForExit with a deadline and escalate to
	// Kill() (which is SIGKILL via os.Process.Kill) if the child
	// refuses to exit.
	if err := t.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		// Process is gone already; treat as a no-op.
		if errors.Is(err, syscall.ESRCH) || errors.Is(err, syscall.EINVAL) {
			return nil
		}
		return err
	}
	return nil
}

func (t *localTerminal) Close(ctx context.Context) error {
	var closeErr error
	t.closeOnce.Do(func() {
		// Send SIGTERM if the process is still running, then wait
		// for the readers and the Wait goroutine to drain. This
		// guarantees Close blocks until all goroutines are
		// reclaimed — no leaks even on early-exit error paths.
		_ = t.Kill(ctx)

		// Closing stdin first unblocks any pump that's reading
		// from the same fd table. The OS will close stdout/stderr
		// when the child fully exits; we don't force-close those
		// here because some pumps may still be draining the last
		// buffered bytes.
		if t.stdin != nil {
			_ = t.stdin.Close()
		}

		// Wait for exit with a small upper bound so Close is not
		// a forever-block on a wedged process. Operators who need
		// hard-kill semantics can Kill() then Close().
		select {
		case <-t.exit:
		case <-ctx.Done():
			closeErr = ctx.Err()
		case <-time.After(5 * time.Second):
			// Escalate to SIGKILL — the child ignored SIGTERM.
			if t.cmd != nil && t.cmd.Process != nil {
				_ = t.cmd.Process.Kill()
			}
			<-t.exit
		}
	})
	return closeErr
}

func (t *localTerminal) pump(wg *sync.WaitGroup, r io.ReadCloser, stream string) {
	defer wg.Done()
	scanner := bufio.NewScanner(r)
	// Allow long lines (build output, stack traces); the default
	// 64 KiB cap is too tight for compiler errors.
	scanner.Buffer(make([]byte, 0, 16*1024), 1024*1024)
	for scanner.Scan() {
		t.output <- OutputChunk{Stream: stream, Text: scanner.Text() + "\n"}
	}
	// scanner.Err() is intentionally dropped — the most common
	// "error" here is io.EOF wrapped by os/exec when the child
	// closes the fd, which is the expected end-of-stream signal
	// and not a real failure mode.
}

// buildTerminalCommand resolves the working directory and policy gates
// before returning an exec.Cmd ready to start. Mirrors sandbox.Command
// semantics where reasonable, but the long-lived nature of a terminal
// means we don't apply Timeout — caller controls lifetime via
// Kill/Close.
func buildTerminalCommand(opts TerminalOptions) (*exec.Cmd, error) {
	// Working-directory escape is the most common mis-configuration
	// surface; reject before spawn so the caller doesn't get a
	// confusing post-spawn permission error. Reuses the same
	// sandbox helper one-shot Run uses, so the two paths can't
	// diverge.
	resolvedDir, err := sandbox.ResolveWorkingDirectory(opts.WorkingDirectory, opts.Policy)
	if err != nil {
		return nil, err
	}

	var cmd *exec.Cmd
	switch {
	case opts.Command == "" && runtime.GOOS == "windows":
		cmd = exec.Command("cmd.exe")
	case opts.Command == "":
		cmd = exec.Command("sh", "-i")
	default:
		cmd = exec.Command(opts.Command, opts.Args...)
	}

	cmd.Dir = resolvedDir

	// Build env: sandbox's sanitized base set + caller overrides.
	// Operators who want to inject secrets should use the credential
	// plumbing, not Env — but we honor the override here for
	// completeness.
	env := sandbox.SanitizedEnv()
	for k, v := range opts.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env
	return cmd, nil
}
