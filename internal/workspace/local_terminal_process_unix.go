//go:build !windows

package workspace

import (
	"errors"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type unixTerminalProcessTree struct {
	mu      sync.Mutex
	pgid    int
	drained bool
}

func prepareTerminalProcessTree(cmd *exec.Cmd) (terminalProcessTree, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// A dedicated process group lets termination reach background children
	// even after the command leader exits. Pgid=0 makes the child the group
	// leader; preserve every unrelated SysProcAttr field a sandbox wrapper or
	// caller may already have configured.
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pgid = 0
	return &unixTerminalProcessTree{}, nil
}

func (t *unixTerminalProcessTree) attach(cmd *exec.Cmd) error {
	t.pgid = cmd.Process.Pid
	return nil
}

func (t *unixTerminalProcessTree) terminate() error {
	return t.signal(syscall.SIGTERM)
}

func (t *unixTerminalProcessTree) forceKill() error {
	return t.signal(syscall.SIGKILL)
}

func (t *unixTerminalProcessTree) signal(signal syscall.Signal) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.drained || t.pgid <= 0 {
		return nil
	}
	if err := syscall.Kill(-t.pgid, signal); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			t.drained = true
			return nil
		}
		return err
	}
	return nil
}

func (t *unixTerminalProcessTree) wait() {
	ticker := time.NewTicker(terminalProcessTreePollInterval)
	defer ticker.Stop()
	for {
		t.mu.Lock()
		if t.drained || t.pgid <= 0 {
			t.mu.Unlock()
			return
		}
		err := syscall.Kill(-t.pgid, 0)
		if errors.Is(err, syscall.ESRCH) {
			t.drained = true
			t.mu.Unlock()
			return
		}
		t.mu.Unlock()

		// nil means the group still has members; EPERM means it exists but
		// cannot be signalled. Any other unexpected error is treated
		// conservatively as still alive so a workspace lease is never
		// released while ownership is uncertain.
		<-ticker.C
	}
}

func (t *unixTerminalProcessTree) close() {}
