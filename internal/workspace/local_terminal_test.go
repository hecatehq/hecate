package workspace

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestLocalTerminal_OneShotCaptureExit drives a terminal through the
// happy path: spawn a short command, read its output, observe a clean
// exit, close cleanly. Skipped on Windows because the command shape
// here is unix-specific.
func TestLocalTerminal_OneShotCaptureExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell semantics; covered separately")
	}
	t.Parallel()

	ws := NewLocalWorkspace()
	t.Cleanup(func() { /* no global state to tear down */ })

	dir := t.TempDir()
	term, err := ws.OpenTerminal(context.Background(), TerminalOptions{
		Command:          "sh",
		Args:             []string{"-c", "printf hello && printf world 1>&2"},
		WorkingDirectory: dir,
		Policy:           Policy{AllowedRoot: dir},
	})
	if err != nil {
		t.Fatalf("OpenTerminal: %v", err)
	}
	t.Cleanup(func() { _ = term.Close(context.Background()) })

	var stdoutBuf, stderrBuf strings.Builder
	done := make(chan struct{})
	go func() {
		for chunk := range term.Output() {
			switch chunk.Stream {
			case "stdout":
				stdoutBuf.WriteString(chunk.Text)
			case "stderr":
				stderrBuf.WriteString(chunk.Text)
			}
		}
		close(done)
	}()

	result, err := term.WaitForExit(context.Background())
	if err != nil {
		t.Fatalf("WaitForExit: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit = %d; want 0", result.ExitCode)
	}
	// Wait for the reader to drain after exit; output close is what
	// signals "no more bytes coming."
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("output channel never closed after exit")
	}
	if !strings.Contains(stdoutBuf.String(), "hello") {
		t.Fatalf("stdout = %q; want to contain hello", stdoutBuf.String())
	}
	if !strings.Contains(stderrBuf.String(), "world") {
		t.Fatalf("stderr = %q; want to contain world", stderrBuf.String())
	}
}

// TestLocalTerminal_StdinDrivesProcess writes a line to stdin and
// confirms the child echoes it back. Locks the stdin path that
// editor-driven terminals will rely on once ACPWorkspace lands.
func TestLocalTerminal_StdinDrivesProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell semantics")
	}
	t.Parallel()

	ws := NewLocalWorkspace()
	dir := t.TempDir()
	term, err := ws.OpenTerminal(context.Background(), TerminalOptions{
		// `cat` echoes stdin back to stdout until EOF.
		Command:          "cat",
		WorkingDirectory: dir,
		Policy:           Policy{AllowedRoot: dir},
	})
	if err != nil {
		t.Fatalf("OpenTerminal: %v", err)
	}
	t.Cleanup(func() { _ = term.Close(context.Background()) })

	if err := term.Write(context.Background(), "ping\n"); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got := ""
	deadline := time.After(2 * time.Second)
	for !strings.Contains(got, "ping") {
		select {
		case chunk := <-term.Output():
			got += chunk.Text
		case <-deadline:
			t.Fatalf("never saw stdin echo; buffer=%q", got)
		}
	}
}

// TestLocalTerminal_CloseKillsRunningProcess verifies Close terminates
// a still-running child. Important because callers leak goroutines
// and file descriptors otherwise.
func TestLocalTerminal_CloseKillsRunningProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix signal semantics")
	}
	t.Parallel()

	ws := NewLocalWorkspace()
	dir := t.TempDir()
	term, err := ws.OpenTerminal(context.Background(), TerminalOptions{
		Command:          "sleep",
		Args:             []string{"60"},
		WorkingDirectory: dir,
		Policy:           Policy{AllowedRoot: dir},
	})
	if err != nil {
		t.Fatalf("OpenTerminal: %v", err)
	}

	closeStart := time.Now()
	if err := term.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if elapsed := time.Since(closeStart); elapsed > 6*time.Second {
		t.Fatalf("Close took %v; want < 6s (escalation deadline)", elapsed)
	}
}

// TestLocalTerminal_WaitForExitRetainsBoundedOutput confirms WaitForExit
// returns captured stdout/stderr even when the caller never consumes
// Output() — the documented bounded-retention contract. Mirrors the
// behavior ACPWorkspace.acpTerminal already had, and the asymmetry
// Copilot flagged on PR #107.
func TestLocalTerminal_WaitForExitRetainsBoundedOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell semantics")
	}
	t.Parallel()

	ws := NewLocalWorkspace()
	dir := t.TempDir()
	script := "for i in $(seq 1 128); do printf 'out-%03d\\n' \"$i\"; printf 'err-%03d\\n' \"$i\" 1>&2; done"
	term, err := ws.OpenTerminal(context.Background(), TerminalOptions{
		Command:          "sh",
		Args:             []string{"-c", script},
		WorkingDirectory: dir,
		Policy:           Policy{AllowedRoot: dir},
	})
	if err != nil {
		t.Fatalf("OpenTerminal: %v", err)
	}
	t.Cleanup(func() { _ = term.Close(context.Background()) })

	// Note: we intentionally do not consume Output() here — the
	// retention path must work even when the channel buffer fills.
	result, err := term.WaitForExit(context.Background())
	if err != nil {
		t.Fatalf("WaitForExit: %v", err)
	}
	for _, want := range []string{"out-001", "out-064", "out-128"} {
		if !strings.Contains(result.Stdout, want) {
			t.Fatalf("Result.Stdout missing %q; got %s", want, compactForTest(result.Stdout))
		}
	}
	for _, want := range []string{"err-001", "err-064", "err-128"} {
		if !strings.Contains(result.Stderr, want) {
			t.Fatalf("Result.Stderr missing %q; got %s", want, compactForTest(result.Stderr))
		}
	}
}

func compactForTest(s string) string {
	const max = 180
	if len(s) <= max {
		return fmt.Sprintf("%q", s)
	}
	return fmt.Sprintf("%q...%q (%d bytes)", s[:90], s[len(s)-90:], len(s))
}

func TestLocalTerminal_RejectsOutsideAllowedRoot(t *testing.T) {
	t.Parallel()

	ws := NewLocalWorkspace()
	dir := t.TempDir()
	_, err := ws.OpenTerminal(context.Background(), TerminalOptions{
		Command:          "true",
		WorkingDirectory: "/etc",
		Policy:           Policy{AllowedRoot: dir},
	})
	if err == nil {
		t.Fatal("OpenTerminal succeeded with cwd outside AllowedRoot; expected sandbox refusal")
	}
}
