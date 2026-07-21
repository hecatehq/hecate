//go:build !windows

package agentadapters

import (
	"errors"
	"syscall"
	"testing"
	"time"
)

func waitForAgentDiagnosticProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if err != nil {
			t.Fatalf("inspect diagnostic child %d: %v", pid, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("diagnostic child %d survived leader exit", pid)
}
