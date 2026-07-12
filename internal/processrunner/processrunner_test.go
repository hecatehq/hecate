package processrunner

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestLocalRunner_RunCapturesOutputAndExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses sh")
	}
	runner := NewLocalRunner()

	result, err := runner.Run(context.Background(), Request{
		Command: "sh",
		Args:    []string{"-c", "printf stdout; printf stderr >&2; exit 7"},
	})

	if err == nil {
		t.Fatal("expected non-zero exit error")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error = %T, want *exec.ExitError", err)
	}
	if result.ExitCode != 7 {
		t.Fatalf("ExitCode = %d, want 7", result.ExitCode)
	}
	if result.Stdout != "stdout" {
		t.Fatalf("Stdout = %q, want stdout", result.Stdout)
	}
	if result.Stderr != "stderr" {
		t.Fatalf("Stderr = %q, want stderr", result.Stderr)
	}
}

func TestLocalRunner_RunTruncatesOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses sh")
	}
	runner := NewLocalRunner()

	result, err := runner.Run(context.Background(), Request{
		Command:        "sh",
		Args:           []string{"-c", "printf abcdef; printf ghijkl >&2"},
		MaxStdoutBytes: 3,
		MaxStderrBytes: 2,
	})

	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Stdout != "abc" || !result.StdoutTruncated {
		t.Fatalf("stdout = %q truncated=%v, want abc true", result.Stdout, result.StdoutTruncated)
	}
	if result.Stderr != "gh" || !result.StderrTruncated {
		t.Fatalf("stderr = %q truncated=%v, want gh true", result.Stderr, result.StderrTruncated)
	}
}

func TestLocalRunner_RunProvidesStandardInput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses sh")
	}
	result, err := NewLocalRunner().Run(context.Background(), Request{
		Command: "sh",
		Args:    []string{"-c", "cat"},
		Stdin:   "first\x00second",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Stdout != "first\x00second" {
		t.Fatalf("Stdout = %q, want binary-safe stdin echo", result.Stdout)
	}
}

func TestLocalRunner_RunRejectsBlankCommand(t *testing.T) {
	runner := NewLocalRunner()

	_, err := runner.Run(context.Background(), Request{Command: "   "})

	if err == nil || !strings.Contains(err.Error(), "command is required") {
		t.Fatalf("error = %v, want command required", err)
	}
}

func TestLocalRunner_RunStreamingEmitsOutputChunks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses sh")
	}
	runner := NewLocalRunner()

	var mu sync.Mutex
	var chunks []Chunk
	result, err := runner.RunStreaming(context.Background(), Request{
		Command: "sh",
		Args:    []string{"-c", "printf stdout; printf stderr >&2"},
	}, func(chunk Chunk) {
		mu.Lock()
		defer mu.Unlock()
		chunks = append(chunks, chunk)
	})

	if err != nil {
		t.Fatalf("RunStreaming: %v", err)
	}
	if result.Stdout != "stdout" {
		t.Fatalf("Stdout = %q, want stdout", result.Stdout)
	}
	if result.Stderr != "stderr" {
		t.Fatalf("Stderr = %q, want stderr", result.Stderr)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(chunks) == 0 {
		t.Fatal("expected at least one streamed chunk")
	}
	var sawStdout, sawStderr bool
	for _, chunk := range chunks {
		switch chunk.Stream {
		case "stdout":
			sawStdout = sawStdout || strings.Contains(chunk.Text, "stdout")
		case "stderr":
			sawStderr = sawStderr || strings.Contains(chunk.Text, "stderr")
		}
	}
	if !sawStdout || !sawStderr {
		t.Fatalf("chunks = %+v, want stdout and stderr chunks", chunks)
	}
}
