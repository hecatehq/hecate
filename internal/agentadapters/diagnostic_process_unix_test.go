//go:build !windows

package agentadapters

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const agentDiagnosticEscapedPipeMode = "HECATE_AGENT_DIAGNOSTIC_ESCAPED_PIPE_MODE"
const agentDiagnosticEscapedPipePIDFile = "HECATE_AGENT_DIAGNOSTIC_ESCAPED_PIPE_PID_FILE"

func TestRunAgentDiagnosticBoundsEscapedInheritedPipes(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "escaped-child.pid")
	t.Cleanup(func() {
		raw, err := os.ReadFile(pidFile)
		if err != nil {
			return
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err == nil && pid > 0 {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}
	})
	env := setAgentDiagnosticHelperEnv(os.Environ(), agentDiagnosticEscapedPipeMode, "parent")
	env = setAgentDiagnosticHelperEnv(env, agentDiagnosticEscapedPipePIDFile, pidFile)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := time.Now()
	_, err := runAgentDiagnostic(
		ctx,
		os.Args[0],
		[]string{"-test.run=^TestAgentDiagnosticEscapedPipeHelperProcess$"},
		env,
	)
	if !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("run diagnostic error = %v, want bounded inherited-pipe wait", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("diagnostic with escaped pipe returned after %s, want bounded wait", elapsed)
	}

	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read escaped helper pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		t.Fatalf("parse escaped helper pid %q: %v", raw, err)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("escaped helper exited before cleanup: %v", err)
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	waitForAgentDiagnosticProcessExit(t, pid)
}

func TestAgentDiagnosticEscapedPipeHelperProcess(t *testing.T) {
	switch os.Getenv(agentDiagnosticEscapedPipeMode) {
	case "":
		return
	case "parent":
		child := exec.Command(os.Args[0], "-test.run=^TestAgentDiagnosticEscapedPipeHelperProcess$")
		child.Env = setAgentDiagnosticHelperEnv(os.Environ(), agentDiagnosticEscapedPipeMode, "child")
		child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			panic("start escaped diagnostic helper: " + err.Error())
		}
		if err := os.WriteFile(os.Getenv(agentDiagnosticEscapedPipePIDFile), []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			panic("write escaped diagnostic helper pid: " + err.Error())
		}
	case "child":
		for {
			time.Sleep(time.Hour)
		}
	}
}

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
