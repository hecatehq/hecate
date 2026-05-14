package llamacpp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// ExecProcessStarter is the production ProcessStarter. It shells out
// to llama-server with the canonical OpenAI-compat flags and polls
// /health over HTTP. Tests inject a fake ProcessStarter instead.
type ExecProcessStarter struct {
	// Stderr is the writer the child's stderr is duplicated to.
	// Nil means discard. The Tauri sidecar wires this to the
	// gateway log so a startup failure (model load OOM, etc.) is
	// diagnosable.
	Stderr io.Writer
}

// NewExecProcessStarter returns a production starter. stderr is
// optional — pass nil to discard.
func NewExecProcessStarter(stderr io.Writer) *ExecProcessStarter {
	return &ExecProcessStarter{Stderr: stderr}
}

// Start spawns llama-server with model + port + context-size, returns
// a handle the Runtime can use to wait for health and stop. The child
// is launched detached from any controlling terminal so the gateway
// owns its stdio.
func (s *ExecProcessStarter) Start(ctx context.Context, opts ProcessStartOptions) (ProcessHandle, error) {
	if opts.BinaryPath == "" {
		return nil, errors.New("exec starter: BinaryPath is required")
	}
	if opts.ModelPath == "" {
		return nil, errors.New("exec starter: ModelPath is required")
	}
	if opts.Port <= 0 {
		return nil, errors.New("exec starter: Port is required")
	}
	host := opts.Host
	if host == "" {
		host = "127.0.0.1"
	}
	args := []string{
		"-m", opts.ModelPath,
		"--host", host,
		"--port", strconv.Itoa(opts.Port),
	}
	if opts.ContextSize > 0 {
		args = append(args, "-c", strconv.Itoa(opts.ContextSize))
	}

	// Use Background() not ctx — the runtime owns the child's
	// lifetime via Stop(); we don't want a parent context cancel
	// to wedge the child without our cleanup running.
	cmd := exec.Command(opts.BinaryPath, args...)
	if s.Stderr != nil {
		cmd.Stderr = s.Stderr
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("spawn llama-server: %w", err)
	}

	handle := &execProcessHandle{
		cmd:    cmd,
		port:   opts.Port,
		host:   host,
		exited: make(chan ProcessExitInfo, 1),
		client: &http.Client{Timeout: 2 * time.Second},
	}
	// Reap the child. The wait goroutine MUST run for every
	// successful Start so PID resources are returned even when the
	// Runtime never calls Stop (e.g. operator yanks power).
	go handle.reap()

	return handle, nil
}

// execProcessHandle implements ProcessHandle around a real exec.Cmd.
type execProcessHandle struct {
	cmd    *exec.Cmd
	port   int
	host   string
	exited chan ProcessExitInfo
	client *http.Client

	stopOnce sync.Once
}

func (h *execProcessHandle) PID() int  { return h.cmd.Process.Pid }
func (h *execProcessHandle) Port() int { return h.port }
func (h *execProcessHandle) Host() string {
	if h.host == "" {
		return "127.0.0.1"
	}
	return h.host
}
func (h *execProcessHandle) Exited() <-chan ProcessExitInfo { return h.exited }

// WaitForHealth polls /health every 250 ms until it returns OK or
// ctx is cancelled. The endpoint shape matches upstream llama-server
// (200 OK with a small JSON body once the model is loaded; 503 while
// still loading).
func (h *execProcessHandle) WaitForHealth(ctx context.Context) error {
	url := fmt.Sprintf("http://%s:%d/health", h.Host(), h.port)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err == nil {
			resp, doErr := h.client.Do(req)
			if doErr == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Stop sends SIGTERM, waits up to timeout, then SIGKILLs. Idempotent
// — multiple calls coalesce via stopOnce; the second caller's wait
// piggybacks on the first's exit channel.
func (h *execProcessHandle) Stop(_ context.Context, timeout time.Duration) error {
	var stopErr error
	h.stopOnce.Do(func() {
		if h.cmd.Process == nil {
			return
		}
		// Graceful first.
		if err := h.cmd.Process.Signal(syscall.SIGTERM); err != nil &&
			!errors.Is(err, syscall.ESRCH) &&
			err.Error() != "os: process already finished" {
			stopErr = fmt.Errorf("sigterm: %w", err)
		}
	})

	select {
	case <-h.exited:
		return stopErr
	case <-time.After(timeout):
	}

	// Escalation: SIGKILL and wait again. The reap goroutine
	// drains cmd.Wait so a second close on exited is safe (channel
	// is buffered 1; reap only sends once).
	if h.cmd.Process != nil {
		_ = h.cmd.Process.Kill()
	}
	select {
	case <-h.exited:
	case <-time.After(2 * time.Second):
		return errors.New("child did not exit after sigkill")
	}
	return stopErr
}

// reap waits for the child to exit and pushes the exit info onto the
// channel. Exactly one send per process — the channel is buffered 1.
func (h *execProcessHandle) reap() {
	err := h.cmd.Wait()
	info := ProcessExitInfo{At: time.Now()}
	if err == nil {
		info.ExitCode = 0
	} else if exitErr, ok := err.(*exec.ExitError); ok {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			if status.Signaled() {
				info.Signal = status.Signal().String()
				info.ExitCode = -1
			} else {
				info.ExitCode = status.ExitStatus()
			}
		} else {
			info.ExitCode = exitErr.ExitCode()
		}
	} else {
		// e.g. failed to fork; surface as -1.
		info.ExitCode = -1
	}
	h.exited <- info
	close(h.exited)
}

// freeTCPPort opens a loopback listener on :0 to learn what the
// kernel hands out, closes it, returns the port. There's a tiny
// chance the port is taken between close and reuse — acceptable for
// a feature that immediately listens on it.
func freeTCPPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected listener addr type %T", l.Addr())
	}
	return addr.Port, nil
}
