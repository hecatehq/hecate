package agentadapters

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const agentDiagnosticHelperMode = "HECATE_AGENT_DIAGNOSTIC_HELPER"
const agentDiagnosticHelperPIDFile = "HECATE_AGENT_DIAGNOSTIC_PID_FILE"

func TestRunAgentDiagnosticBoundsOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := runAgentDiagnostic(
		ctx,
		os.Args[0],
		[]string{"-test.run=^TestAgentDiagnosticHelperProcess$"},
		agentDiagnosticHelperEnv("flood"),
	)
	if err != nil {
		t.Fatalf("run diagnostic: %v", err)
	}
	if len(out.stdout) != int(agentDiagnosticOutputLimit) || !out.stdoutTruncated {
		t.Fatalf("stdout length/truncated = %d/%t, want %d/true", len(out.stdout), out.stdoutTruncated, agentDiagnosticOutputLimit)
	}
	if len(out.stderr) != int(agentDiagnosticOutputLimit) || !out.stderrTruncated {
		t.Fatalf("stderr length/truncated = %d/%t, want %d/true", len(out.stderr), out.stderrTruncated, agentDiagnosticOutputLimit)
	}
}

func TestRunAgentDiagnosticHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := runAgentDiagnostic(
		ctx,
		os.Args[0],
		[]string{"-test.run=^TestAgentDiagnosticHelperProcess$"},
		agentDiagnosticHelperEnv("block"),
	)
	if err == nil {
		t.Fatal("run diagnostic succeeded, want cancellation error")
	}
	if ctx.Err() != context.DeadlineExceeded {
		t.Fatalf("context error = %v, want deadline exceeded", ctx.Err())
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("cancelled diagnostic returned after %s, want prompt process-tree stop", elapsed)
	}
}

func TestRunAgentDiagnosticTerminatesDescendantsAfterLeaderExit(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	env := setAgentDiagnosticHelperEnv(
		agentDiagnosticHelperEnv("orphan-parent"),
		agentDiagnosticHelperPIDFile,
		pidFile,
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := runAgentDiagnostic(
		ctx,
		os.Args[0],
		[]string{"-test.run=^TestAgentDiagnosticHelperProcess$"},
		env,
	)
	if err != nil {
		t.Fatalf("run diagnostic parent: %v", err)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read helper child pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		t.Fatalf("parse helper child pid %q: %v", raw, err)
	}
	waitForAgentDiagnosticProcessExit(t, pid)
}

func TestAgentDiagnosticHelperProcess(t *testing.T) {
	switch os.Getenv(agentDiagnosticHelperMode) {
	case "flood":
		payload := bytes.Repeat([]byte("x"), int(agentDiagnosticOutputLimit)+4096)
		_, _ = os.Stdout.Write(payload)
		_, _ = os.Stderr.Write(payload)
	case "block":
		for {
			time.Sleep(time.Hour)
		}
	case "orphan-parent":
		child := exec.Command(os.Args[0], "-test.run=^TestAgentDiagnosticHelperProcess$")
		child.Env = agentDiagnosticHelperEnv("orphan-child")
		if err := child.Start(); err != nil {
			panic("start diagnostic helper child: " + err.Error())
		}
		if err := os.WriteFile(os.Getenv(agentDiagnosticHelperPIDFile), []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			panic("write diagnostic helper child pid: " + err.Error())
		}
	case "orphan-child":
		for {
			time.Sleep(time.Hour)
		}
	}
}

func agentDiagnosticHelperEnv(mode string) []string {
	prefix := agentDiagnosticHelperMode + "="
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, prefix) {
			env = append(env, entry)
		}
	}
	return append(env, prefix+mode)
}

func setAgentDiagnosticHelperEnv(env []string, name, value string) []string {
	prefix := name + "="
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			out = append(out, entry)
		}
	}
	return append(out, prefix+value)
}
