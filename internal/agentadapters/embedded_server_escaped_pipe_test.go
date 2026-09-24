//go:build darwin || linux

package agentadapters

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	adapterprocess "github.com/hecatehq/acp-adapter-kit/process"
)

const providerRunnerEscapedPipeMode = "HECATE_PROVIDER_RUNNER_ESCAPED_PIPE_MODE"
const providerRunnerEscapedPipePIDFile = "HECATE_PROVIDER_RUNNER_ESCAPED_PIPE_PID_FILE"

func TestProviderProcessRunnerBoundsEscapedInheritedStdout(t *testing.T) {
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
	baseEnv := append([]string(nil), os.Environ()...)
	baseEnv = setProviderRunnerHelperEnv(baseEnv, providerRunnerEscapedPipeMode, "parent")
	baseEnv = setProviderRunnerHelperEnv(baseEnv, providerRunnerEscapedPipePIDFile, pidFile)
	runner := newProviderProcessRunner("provider", os.Args[0], baseEnv)

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	started := time.Now()
	result, err := runner.RunStream(ctx, adapterprocess.Spec{
		Command: "provider",
		Args:    []string{"-test.run=^TestProviderProcessRunnerEscapedPipeHelperProcess$"},
		Dir:     t.TempDir(),
		Env: adapterprocess.EnvPolicy{Inherit: []string{
			providerRunnerEscapedPipeMode,
			providerRunnerEscapedPipePIDFile,
		}},
	}, nil)
	if !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("provider run error = %v, want bounded inherited-pipe wait", err)
	}
	if elapsed := time.Since(started); elapsed > 4*time.Second {
		t.Fatalf("provider with escaped stdout returned after %s, want bounded wait", elapsed)
	}
	if got := strings.TrimSpace(string(result.Stdout)); !strings.Contains(got, "provider parent exited") {
		t.Fatalf("provider stdout = %q, want parent output drained before bounded wait", got)
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

func TestProviderProcessRunnerEscapedPipeHelperProcess(t *testing.T) {
	switch os.Getenv(providerRunnerEscapedPipeMode) {
	case "":
		return
	case "parent":
		child := exec.Command(os.Args[0], "-test.run=^TestProviderProcessRunnerEscapedPipeHelperProcess$")
		child.Env = setProviderRunnerHelperEnv(os.Environ(), providerRunnerEscapedPipeMode, "child")
		child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			panic("start escaped provider helper: " + err.Error())
		}
		if err := os.WriteFile(os.Getenv(providerRunnerEscapedPipePIDFile), []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			panic("write escaped provider helper pid: " + err.Error())
		}
		_, _ = fmt.Fprintln(os.Stdout, "provider parent exited")
	case "child":
		for {
			time.Sleep(time.Hour)
		}
	}
}

func setProviderRunnerHelperEnv(env []string, name, value string) []string {
	prefix := name + "="
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		out = append(out, entry)
	}
	return append(out, prefix+value)
}
