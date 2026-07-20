package workspace

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hecatehq/hecate/internal/sandbox"
)

// localTerminal is the on-host implementation. Each terminal owns a
// process tree plus three goroutines (stdout/stderr readers and a
// wait-for-exit). Closing serializes so concurrent Kill / Close /
// WaitForExit don't race cleanup, while a cancelled Close remains retryable.
type localTerminal struct {
	id     string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
	tree   terminalProcessTree

	output chan OutputChunk

	exit    chan struct{}
	result  atomic.Pointer[Result]
	waitErr atomic.Pointer[error]

	// Retained stdout/stderr for WaitForExit. Bounded so a chatty
	// child doesn't grow this without limit; once the cap is hit we
	// stop retaining (but the Output() channel continues to receive
	// chunks, subject to its own drop-on-full policy).
	bufMu     sync.Mutex
	stdoutBuf strings.Builder
	stderrBuf strings.Builder

	inputPolicyMu    sync.Mutex
	inputPolicyState sandbox.SupervisedTerminalInputState
	inputPolicyErr   error
	inputPolicyMode  sandbox.SupervisedTerminalInputMode

	closeGate        chan struct{}
	closeGracePeriod time.Duration
}

// localTerminalBufLimit caps the retained stdout/stderr that
// WaitForExit returns. Mirrors a sensible upper bound for "captured
// command output" without letting a runaway child OOM the gateway.
const localTerminalBufLimit = 256 * 1024

const defaultLocalTerminalCloseGracePeriod = 5 * time.Second

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
	tree, err := prepareTerminalProcessTree(cmd)
	if err != nil {
		return nil, fmt.Errorf("prepare terminal process tree: %w", err)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		tree.close()
		return nil, fmt.Errorf("open stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		tree.close()
		return nil, fmt.Errorf("open stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		tree.close()
		return nil, fmt.Errorf("open stderr: %w", err)
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		tree.close()
		return nil, fmt.Errorf("start terminal: %w", err)
	}
	if err := tree.attach(cmd); err != nil {
		// Windows starts the process suspended so it cannot create an
		// unowned descendant before attach succeeds. A direct kill is the
		// only safe cleanup when process-tree ownership could not be
		// established; Unix attach is infallible.
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		tree.close()
		return nil, fmt.Errorf("attach terminal process tree: %w", err)
	}

	t := &localTerminal{
		id:               fmt.Sprintf("term_%d", nextTerminalID.Add(1)),
		cmd:              cmd,
		stdin:            stdin,
		stdout:           stdout,
		stderr:           stderr,
		tree:             tree,
		output:           make(chan OutputChunk, 64),
		exit:             make(chan struct{}),
		inputPolicyMode:  sandbox.SupervisedTerminalInputModeForCommand(opts.Command, opts.Args),
		closeGate:        newTerminalCloseGate(),
		closeGracePeriod: defaultLocalTerminalCloseGracePeriod,
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
	// once it's published. Completion includes the entire owned process
	// tree, not only the command leader, so background children cannot
	// outlive the terminal or release its workspace lease early. Use
	// Process.Wait instead of Cmd.Wait:
	// Cmd.StdoutPipe/Cmd.StderrPipe docs require callers to finish
	// reading before Cmd.Wait because Cmd.Wait closes the pipes. The
	// pumps below own those reads, so we wait for the process tree to
	// exit and both pumps to drain before publishing the terminal result.
	go func() {
		state, err := cmd.Process.Wait()
		tree.wait()
		pumpDone.Wait()
		// Natural completion must reclaim the parent-side stdin handle too.
		// Close after the full owned tree drains so a background member that
		// inherited stdin can still receive input while the terminal is live.
		_ = stdin.Close()
		tree.close()
		var result Result
		if state != nil {
			cmd.ProcessState = state
			result.ExitCode = state.ExitCode()
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

func (t *localTerminal) Write(_ context.Context, input string) error {
	if t.stdin == nil {
		return errors.New("stdin is closed")
	}
	// Keep validation, tail accounting, and the pipe write in one order. If
	// concurrent callers could validate in one order and reach the pipe in
	// another, separately harmless fragments could assemble into a rejected
	// detachment command after validation.
	t.inputPolicyMu.Lock()
	defer t.inputPolicyMu.Unlock()
	nextPolicyState, err := t.validateTerminalInputOwnership(input)
	if err != nil {
		return err
	}
	// Synchronous write: an *os.File-backed pipe doesn't honor a
	// deadline cleanly, and a goroutine-based ctx race would leak
	// the inner write if the pipe blocks. Callers who need to
	// unblock a stuck stdin should Kill or Close — those close the
	// pipe and the write returns.
	written, writeErr := t.stdin.Write([]byte(input))
	stateErr := t.recordTerminalInputWrite(input, written, nextPolicyState)
	if stateErr != nil {
		return errors.Join(writeErr, stateErr)
	}
	if writeErr == nil && written != len(input) {
		return io.ErrShortWrite
	}
	return writeErr
}

func (t *localTerminal) validateTerminalInputOwnership(input string) (sandbox.SupervisedTerminalInputState, error) {
	if t == nil || t.inputPolicyMode == sandbox.SupervisedTerminalInputNone || input == "" {
		if t == nil {
			return sandbox.SupervisedTerminalInputState{}, nil
		}
		return t.inputPolicyState, nil
	}
	if t.inputPolicyErr != nil {
		return t.inputPolicyState, t.inputPolicyErr
	}
	return sandbox.ValidateSupervisedTerminalInputWrite(t.inputPolicyMode, t.inputPolicyState, input)
}

func (t *localTerminal) recordTerminalInputWrite(input string, written int, completeState sandbox.SupervisedTerminalInputState) error {
	if t == nil || t.inputPolicyMode == sandbox.SupervisedTerminalInputNone || written <= 0 {
		return nil
	}
	if written > len(input) {
		written = len(input)
	}
	if written == len(input) {
		t.inputPolicyState = completeState
		return nil
	}
	partialState, err := sandbox.ValidateSupervisedTerminalInputWrite(t.inputPolicyMode, t.inputPolicyState, input[:written])
	if err != nil {
		t.inputPolicyErr = &sandbox.PolicyError{Reason: fmt.Sprintf("terminal input supervision state is indeterminate after a partial write: %v", err)}
		return t.inputPolicyErr
	}
	t.inputPolicyState = partialState
	return nil
}

func (t *localTerminal) WaitForExit(ctx context.Context) (Result, error) {
	select {
	case <-t.exit:
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	r := t.result.Load()
	if r == nil {
		return Result{}, errors.New("terminal exited without recording result")
	}
	t.bufMu.Lock()
	out := Result{
		Stdout:   t.stdoutBuf.String(),
		Stderr:   t.stderrBuf.String(),
		ExitCode: r.ExitCode,
	}
	t.bufMu.Unlock()
	if e := t.waitErr.Load(); e != nil {
		return out, *e
	}
	return out, nil
}

func (t *localTerminal) Kill(_ context.Context) error {
	if t == nil || t.tree == nil {
		return nil
	}
	select {
	case <-t.exit:
		return nil
	default:
	}
	// Unix gives the full process group a graceful SIGTERM. Windows has
	// no equivalent tree-wide graceful signal, so its Job Object
	// implementation terminates the job.
	return t.tree.terminate()
}

func newTerminalCloseGate() chan struct{} {
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	return gate
}

func (t *localTerminal) forceKill() {
	if t != nil && t.tree != nil {
		_ = t.tree.forceKill()
	}
}

func (t *localTerminal) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-t.exit:
		return nil
	default:
	}

	// Close attempts serialize through a context-aware gate. A caller racing a
	// slow drain must not wait past its own shutdown deadline, and a cancelled
	// caller must not permanently consume close authority.
	select {
	case <-t.exit:
		return nil
	case <-ctx.Done():
		t.forceKill()
		return ctx.Err()
	case <-t.closeGate:
	}
	defer func() { t.closeGate <- struct{}{} }()
	select {
	case <-t.exit:
		return nil
	default:
	}

	// Send SIGTERM if the process is still running, then wait for the readers
	// and Wait goroutine to drain. Closing stdin first also unblocks a child
	// waiting for input.
	_ = t.Kill(ctx)
	if t.stdin != nil {
		_ = t.stdin.Close()
	}

	gracePeriod := t.closeGracePeriod
	if gracePeriod <= 0 {
		gracePeriod = defaultLocalTerminalCloseGracePeriod
	}
	timer := time.NewTimer(gracePeriod)
	defer timer.Stop()
	select {
	case <-t.exit:
		return nil
	case <-ctx.Done():
		t.forceKill()
		return ctx.Err()
	case <-timer.C:
		// Escalate the complete tree. Output pumps can still be draining
		// after the OS reports process termination, so keep the final wait
		// context-aware instead of pinning application shutdown forever.
		t.forceKill()
		select {
		case <-t.exit:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (t *localTerminal) pump(wg *sync.WaitGroup, r io.ReadCloser, stream string) {
	defer wg.Done()
	// We use cmd.Process.Wait (not cmd.Wait) in OpenTerminal so the
	// pumps own the reads without racing the Cmd.Wait pipe-close.
	// That means the read end of this pipe is never closed by
	// os/exec — close it ourselves once the scanner sees EOF, or
	// the file descriptor leaks for the lifetime of the process.
	defer r.Close()
	scanner := bufio.NewScanner(r)
	// Allow long lines (build output, stack traces); the default
	// 64 KiB cap is too tight for compiler errors.
	scanner.Buffer(make([]byte, 0, 16*1024), 1024*1024)
	for scanner.Scan() {
		text := scanner.Text() + "\n"
		// Retain into the bounded WaitForExit buffer first so a
		// slow Output() consumer doesn't lose the captured copy.
		t.bufMu.Lock()
		buf := &t.stdoutBuf
		if stream == "stderr" {
			buf = &t.stderrBuf
		}
		if buf.Len() < localTerminalBufLimit {
			buf.WriteString(text)
		}
		t.bufMu.Unlock()
		// Non-blocking publish: an absent or slow consumer of
		// Output() drops chunks rather than wedging the pump and
		// (through it) cmd.Wait — which would prevent t.exit
		// closing and pin Close past its escalation deadline.
		select {
		case t.output <- OutputChunk{Stream: stream, Text: text}:
		default:
		}
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
	if err := sandbox.ValidateSupervisedTerminalCommand(opts.Command, opts.Args); err != nil {
		return nil, err
	}
	// Working-directory escape is the most common mis-configuration
	// surface; reject before spawn so the caller doesn't get a
	// confusing post-spawn permission error. Reuses the same
	// sandbox helper one-shot Run uses, so the two paths can't
	// diverge.
	resolvedDir, err := sandbox.ResolveWorkingDirectory(opts.WorkingDirectory, opts.Policy)
	if err != nil {
		return nil, err
	}

	policyCommand := terminalPolicyCommand(opts.Command, opts.Args)
	if policyCommand != "" {
		if err := sandbox.ValidateCommand(policyCommand, opts.Policy); err != nil {
			return nil, err
		}
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
	if err := resolveTerminalExecutable(cmd, resolvedDir); err != nil {
		return nil, err
	}

	cmd.Dir = resolvedDir
	sandbox.ApplyWrapper(cmd, resolvedDir, opts.Policy.Network)

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

func resolveTerminalExecutable(cmd *exec.Cmd, workingDirectory string) error {
	if cmd == nil {
		return fmt.Errorf("resolve terminal executable: command is nil")
	}
	if cmd.Err != nil {
		return fmt.Errorf("resolve terminal executable: %w", cmd.Err)
	}
	executable := cmd.Path
	if strings.TrimSpace(executable) == "" {
		return fmt.Errorf("resolve terminal executable: command path is empty")
	}
	if !filepath.IsAbs(executable) && strings.ContainsAny(executable, `/\`) {
		base := workingDirectory
		if base == "" {
			var err error
			base, err = os.Getwd()
			if err != nil {
				return fmt.Errorf("resolve terminal process working directory: %w", err)
			}
		} else {
			var err error
			base, err = filepath.Abs(base)
			if err != nil {
				return fmt.Errorf("resolve terminal working directory: %w", err)
			}
		}
		executable = filepath.Join(base, executable)
	}
	resolved, err := exec.LookPath(executable)
	if err != nil {
		return fmt.Errorf("resolve terminal executable %q: %w", executable, err)
	}
	cmd.Path = resolved
	if len(cmd.Args) > 0 {
		// OS wrappers become argv[0] at launch, so the original target must
		// also be absolute in Args or a wrapper could hide a missing command
		// until after OpenTerminal reports success.
		cmd.Args[0] = resolved
	}
	return nil
}

func terminalPolicyCommand(command string, args []string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	// Static policy validation is intentionally best-effort. Join argv into
	// the same kind of approximate command text validateCommand already
	// inspects for one-shot shell strings; the OS wrapper is the real
	// containment layer for exact argv semantics and interactive input.
	if len(args) == 0 {
		return command
	}
	parts := make([]string, 0, 1+len(args))
	parts = append(parts, command)
	parts = append(parts, args...)
	return strings.Join(parts, " ")
}
