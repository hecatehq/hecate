package codeintel

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/hecatehq/hecate/internal/processrunner"
)

// codeIntelProcessRunner keeps the shared bounded Request/Result seam while
// adding the process-group / Job Object ownership required for tools that may
// spawn descendants. The generic LocalRunner intentionally does not promise
// descendant supervision for ordinary one-shot commands.
type codeIntelProcessRunner struct{}

func newCodeIntelProcessRunner() *codeIntelProcessRunner {
	return &codeIntelProcessRunner{}
}

func (r *codeIntelProcessRunner) Run(ctx context.Context, request processrunner.Request) (processrunner.Result, error) {
	return r.RunStreaming(ctx, request, nil)
}

func (r *codeIntelProcessRunner) RunStreaming(ctx context.Context, request processrunner.Request, onChunk func(processrunner.Chunk)) (processrunner.Result, error) {
	command := strings.TrimSpace(request.Command)
	if command == "" {
		return processrunner.Result{ExitCode: -1}, fmt.Errorf("process command is required")
	}
	runCtx := ctx
	var cancel context.CancelFunc
	if request.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, request.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(runCtx, command, request.Args...)
	cmd.Dir = request.Dir
	cmd.Env = request.Env
	cmd.WaitDelay = time.Second
	if request.Stdin != "" {
		cmd.Stdin = strings.NewReader(request.Stdin)
	}
	stdout := &boundedOutput{limit: request.MaxStdoutBytes, stream: "stdout", onChunk: onChunk}
	stderr := &boundedOutput{limit: request.MaxStderrBytes, stream: "stderr", onChunk: onChunk}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	tree, err := prepareLSPProcess(cmd)
	if err != nil {
		return processrunner.Result{ExitCode: -1}, fmt.Errorf("prepare process supervision: %w", err)
	}
	defer tree.close()
	if err := cmd.Start(); err != nil {
		return processrunner.Result{ExitCode: -1}, err
	}
	if err := tree.attach(cmd); err != nil {
		_ = tree.forceKill(cmd)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return processrunner.Result{ExitCode: -1}, fmt.Errorf("attach process supervision: %w", err)
	}
	// Observe leader exit without reaping it. The process group / Job Object
	// must be terminated while the leader still owns its identity; calling
	// Wait first would let Unix reuse the numeric PID/PGID before forceKill.
	observeErr := waitProcessExitWithoutReaping(runCtx, cmd)
	_ = tree.forceKill(cmd)

	// This is the sole reap. Keep it asynchronous so a cancellation still has
	// a bounded return even if the OS cannot promptly finish a killed process.
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	var waitErr error
	select {
	case waitErr = <-wait:
	case <-time.After(2 * time.Second):
		result := processrunner.Result{
			Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: -1,
			StdoutTruncated: stdout.Truncated(), StderrTruncated: stderr.Truncated(),
		}
		if err := runCtx.Err(); err != nil {
			return result, err
		}
		return result, fmt.Errorf("code intelligence process did not exit after termination")
	}
	result := processrunner.Result{
		Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: 0,
		StdoutTruncated: stdout.Truncated(), StderrTruncated: stderr.Truncated(),
	}
	if err := runCtx.Err(); err != nil {
		result.ExitCode = -1
		return result, err
	}
	if observeErr != nil {
		result.ExitCode = -1
		return result, fmt.Errorf("observe code intelligence process exit: %w", observeErr)
	}
	if waitErr == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, waitErr
	}
	result.ExitCode = -1
	return result, waitErr
}

type boundedOutput struct {
	mu        sync.Mutex
	data      strings.Builder
	limit     int64
	truncated bool
	stream    string
	onChunk   func(processrunner.Chunk)
}

func (b *boundedOutput) Write(value []byte) (int, error) {
	if b.onChunk != nil && len(value) > 0 {
		b.onChunk(processrunner.Chunk{Stream: b.stream, Text: string(value)})
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit <= 0 {
		_, _ = b.data.Write(value)
		return len(value), nil
	}
	remaining := b.limit - int64(b.data.Len())
	if remaining <= 0 {
		b.truncated = true
		return len(value), nil
	}
	if int64(len(value)) > remaining {
		b.truncated = true
		_, _ = b.data.Write(value[:remaining])
		return len(value), nil
	}
	_, _ = b.data.Write(value)
	return len(value), nil
}

func (b *boundedOutput) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.String()
}

func (b *boundedOutput) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}

var _ processrunner.Runner = (*codeIntelProcessRunner)(nil)
