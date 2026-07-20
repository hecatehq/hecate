//go:build !windows

package codeintel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/processrunner"
)

func TestCodeIntelProcessRunner_PreservesExactExecutablePath(t *testing.T) {
	directory := t.TempDir()
	exact := filepath.Join(directory, "provider ")
	trimmed := filepath.Join(directory, "provider")
	for path, output := range map[string]string{exact: "exact", trimmed: "trimmed"} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf "+output+"\n"), 0o755); err != nil {
			t.Fatalf("write executable %q: %v", path, err)
		}
	}

	result, err := newCodeIntelProcessRunner().Run(context.Background(), processrunner.Request{
		Command: exact,
		// Full-repository race instrumentation can delay process-exit
		// observation substantially even for this tiny fixture. Keep a hard
		// bound without making scheduler load part of the identity assertion.
		Timeout:        30 * time.Second,
		MaxStdoutBytes: 1024,
		MaxStderrBytes: 1024,
	})
	if err != nil {
		t.Fatalf("run exact executable: %v", err)
	}
	if result.Stdout != "exact" {
		t.Fatalf("stdout = %q, want exact executable identity", result.Stdout)
	}
}

func TestCodeIntelProcessRunner_SuccessKillsDescendantsBeforeReap(t *testing.T) {
	runner := newCodeIntelProcessRunner()
	result, err := runner.Run(context.Background(), processrunner.Request{
		Command:        "/bin/sh",
		Args:           []string{"-c", "sleep 60 >/dev/null 2>&1 & echo $!"},
		Timeout:        2 * time.Second,
		MaxStdoutBytes: 1024,
		MaxStderrBytes: 1024,
	})
	if err != nil {
		t.Fatalf("run successful parent: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", result.ExitCode)
	}
	pid, parseErr := strconv.Atoi(strings.TrimSpace(result.Stdout))
	if parseErr != nil {
		t.Fatalf("child pid output = %q: %v", result.Stdout, parseErr)
	}
	assertProcessExited(t, pid, "successful one-shot descendant")
}

func TestCodeIntelProcessRunner_PreservesOutputAndExitStatus(t *testing.T) {
	runner := newCodeIntelProcessRunner()
	result, err := runner.Run(context.Background(), processrunner.Request{
		Command:        "/bin/sh",
		Args:           []string{"-c", "printf stdout-value; printf stderr-value >&2; exit 7"},
		Timeout:        2 * time.Second,
		MaxStdoutBytes: 1024,
		MaxStderrBytes: 1024,
	})
	var exitErr interface{ ExitCode() int }
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatalf("error = %v, want exit status 7", err)
	}
	if result.ExitCode != 7 || result.Stdout != "stdout-value" || result.Stderr != "stderr-value" {
		t.Fatalf("result = %+v, want exact output and exit status", result)
	}
}

func TestCodeIntelProcessRunner_CancellationKillsDescendants(t *testing.T) {
	runner := newCodeIntelProcessRunner()
	result, err := runner.Run(context.Background(), processrunner.Request{
		Command:        "/bin/sh",
		Args:           []string{"-c", "sleep 60 & echo $!; wait"},
		Timeout:        100 * time.Millisecond,
		MaxStdoutBytes: 1024,
		MaxStderrBytes: 1024,
	})
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("error = %v, want deadline", err)
	}
	pid, parseErr := strconv.Atoi(strings.TrimSpace(result.Stdout))
	if parseErr != nil {
		t.Fatalf("child pid output = %q: %v", result.Stdout, parseErr)
	}
	assertProcessExited(t, pid, "cancelled one-shot descendant")
}

func assertProcessExited(t *testing.T, pid int, label string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for processExists(pid) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if processExists(pid) {
		t.Fatalf("%s process %d survived supervision", label, pid)
	}
}
