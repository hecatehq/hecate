package processrunner

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Request describes one local process invocation owned by Hecate.
type Request struct {
	Command        string
	Args           []string
	Dir            string
	Env            []string
	Stdin          string
	Timeout        time.Duration
	MaxStdoutBytes int64
	MaxStderrBytes int64
}

// Result is the captured result of a local process invocation.
type Result struct {
	Stdout          string
	Stderr          string
	ExitCode        int
	StdoutTruncated bool
	StderrTruncated bool
}

// Chunk is one streamed process-output chunk.
type Chunk struct {
	Stream string
	Text   string
}

// Runner is the process seam for Hecate-owned subprocesses. Long-lived ACP
// adapter sessions still manage their own lifecycle; this seam is for bounded,
// command-style invocations such as git probes and workspace setup.
type Runner interface {
	Run(ctx context.Context, req Request) (Result, error)
	RunStreaming(ctx context.Context, req Request, onChunk func(Chunk)) (Result, error)
}

type LocalRunner struct{}

func NewLocalRunner() *LocalRunner {
	return &LocalRunner{}
}

func (r *LocalRunner) Run(ctx context.Context, req Request) (Result, error) {
	return r.RunStreaming(ctx, req, nil)
}

func (r *LocalRunner) RunStreaming(ctx context.Context, req Request, onChunk func(Chunk)) (Result, error) {
	command := strings.TrimSpace(req.Command)
	if command == "" {
		return Result{ExitCode: -1}, fmt.Errorf("process command is required")
	}
	runCtx := ctx
	var cancel context.CancelFunc
	if req.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(runCtx, command, req.Args...)
	if strings.TrimSpace(req.Dir) != "" {
		cmd.Dir = req.Dir
	}
	if req.Env != nil {
		cmd.Env = req.Env
	}
	if req.Stdin != "" {
		cmd.Stdin = strings.NewReader(req.Stdin)
	}

	var stdout limitedBuffer
	var stderr limitedBuffer
	stdout.limit = req.MaxStdoutBytes
	stderr.limit = req.MaxStderrBytes
	stdout.stream = "stdout"
	stderr.stream = "stderr"
	stdout.onChunk = onChunk
	stderr.onChunk = onChunk
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := Result{
		Stdout:          stdout.String(),
		Stderr:          stderr.String(),
		ExitCode:        0,
		StdoutTruncated: stdout.Truncated(),
		StderrTruncated: stderr.Truncated(),
	}
	if err == nil {
		return result, nil
	}
	if errors.Is(runCtx.Err(), context.Canceled) {
		result.ExitCode = -1
		return result, context.Canceled
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		result.ExitCode = -1
		return result, runCtx.Err()
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, err
	}
	result.ExitCode = -1
	return result, err
}

type limitedBuffer struct {
	buf       strings.Builder
	mu        sync.Mutex
	limit     int64
	truncated bool
	stream    string
	onChunk   func(Chunk)
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	text := string(p)
	if b.onChunk != nil && text != "" {
		b.onChunk(Chunk{Stream: b.stream, Text: text})
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit <= 0 {
		_, _ = b.buf.Write(p)
		return len(p), nil
	}
	remaining := b.limit - int64(b.buf.Len())
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if int64(len(p)) > remaining {
		b.truncated = true
		_, _ = b.buf.Write(p[:remaining])
		return len(p), nil
	}
	_, _ = b.buf.Write(p)
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *limitedBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}
