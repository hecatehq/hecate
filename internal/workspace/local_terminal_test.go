package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/sandbox"
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
// confirms the child echoes it back. Locks the stdin path for
// interactive terminal callers.
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

func TestLocalTerminal_CloseCanRetryAfterCancelledContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix signal semantics")
	}
	t.Parallel()

	ws := NewLocalWorkspace()
	dir := t.TempDir()
	term, err := ws.OpenTerminal(context.Background(), TerminalOptions{
		Command:          "sh",
		Args:             []string{"-c", "trap '' TERM; printf 'ready\\n'; sleep 1"},
		WorkingDirectory: dir,
		Policy:           Policy{AllowedRoot: dir},
	})
	if err != nil {
		t.Fatalf("OpenTerminal: %v", err)
	}
	t.Cleanup(func() { _ = term.Close(context.Background()) })

	select {
	case chunk := <-term.Output():
		if !strings.Contains(chunk.Text, "ready") {
			t.Fatalf("first output = %q, want ready", chunk.Text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("terminal did not report readiness")
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := term.Close(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("first Close error = %v, want context.Canceled", err)
	}
	retryCtx, retryCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer retryCancel()
	if err := term.Close(retryCtx); err != nil {
		t.Fatalf("second Close after forced cancellation cleanup: %v", err)
	}
	if _, err := term.WaitForExit(context.Background()); err != nil {
		t.Fatalf("WaitForExit: %v", err)
	}
	if err := term.Close(context.Background()); err != nil {
		t.Fatalf("Close after exit: %v", err)
	}
}

// TestLocalTerminal_WaitForExitRetainsBoundedOutput confirms WaitForExit
// returns captured stdout/stderr even when the caller never consumes
// Output() — the documented bounded-retention contract.
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

func TestBuildTerminalCommandRejectsPolicyViolationsBeforeSpawn(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_, err := buildTerminalCommand(TerminalOptions{
		Command:          "touch",
		Args:             []string{"blocked.txt"},
		WorkingDirectory: dir,
		Policy:           Policy{AllowedRoot: dir, ReadOnly: true},
	})
	if err == nil {
		t.Fatal("buildTerminalCommand succeeded with mutating read-only command; want sandbox refusal")
	}
	if !strings.Contains(err.Error(), "write access is disabled") {
		t.Fatalf("buildTerminalCommand error = %v, want read-only policy error", err)
	}
}

func TestBuildTerminalCommandAppliesSandboxWrapper(t *testing.T) {
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperBwrap)
	defer reset()

	dir := t.TempDir()
	cmd, err := buildTerminalCommand(TerminalOptions{
		Command:          "echo",
		Args:             []string{"hello"},
		WorkingDirectory: dir,
		Policy:           Policy{AllowedRoot: dir},
	})
	if err != nil {
		t.Fatalf("buildTerminalCommand: %v", err)
	}
	if len(cmd.Args) == 0 || cmd.Args[0] != "/usr/bin/bwrap" {
		t.Fatalf("terminal argv = %#v, want bwrap wrapper", cmd.Args)
	}
	if !containsString(cmd.Args, "--bind") || !containsString(cmd.Args, dir) {
		t.Fatalf("terminal argv = %#v, want workspace bind", cmd.Args)
	}
	if !containsString(cmd.Args, "--unshare-net") {
		t.Fatalf("terminal argv = %#v, want network isolation by default", cmd.Args)
	}
}

func TestBuildTerminalCommandRejectsMissingExecutableBeforeWrapper(t *testing.T) {
	for _, kind := range []sandbox.WrapperKind{sandbox.WrapperBwrap, sandbox.WrapperSandboxExec} {
		t.Run(string(kind), func(t *testing.T) {
			reset := sandbox.SetWrapperForTesting(kind)
			defer reset()

			dir := t.TempDir()
			missing := filepath.Join(t.TempDir(), "missing-terminal-command")
			_, err := buildTerminalCommand(TerminalOptions{
				Command:          missing,
				WorkingDirectory: dir,
				Policy:           Policy{AllowedRoot: dir},
			})
			if err == nil || !strings.Contains(err.Error(), "resolve terminal executable") {
				t.Fatalf("buildTerminalCommand error = %v, want pre-wrapper executable rejection", err)
			}
		})
	}
}

func TestResolveTerminalExecutableDoesNotSearchPATHForDotSlash(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix relative executable spelling")
	}
	bin := t.TempDir()
	name := "hecate-terminal-relative-path-fixture"
	if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write PATH fixture: %v", err)
	}
	t.Setenv("PATH", bin)

	cmd := exec.Command("./" + name)
	err := resolveTerminalExecutable(cmd, "")
	if err == nil {
		t.Fatalf("resolveTerminalExecutable selected PATH entry %q for ./ spelling", cmd.Path)
	}
}

func TestBuildTerminalCommandResolvesDotSlashFromProcessWorkingDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix relative executable spelling")
	}
	processDir := t.TempDir()
	t.Chdir(processDir)
	name := "hecate-terminal-local-fixture"
	localPath := filepath.Join(processDir, name)
	if err := os.WriteFile(localPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write process-cwd fixture: %v", err)
	}
	pathDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(pathDir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write PATH fixture: %v", err)
	}
	t.Setenv("PATH", pathDir)

	for _, kind := range []sandbox.WrapperKind{sandbox.WrapperNone, sandbox.WrapperBwrap, sandbox.WrapperSandboxExec} {
		t.Run(string(kind), func(t *testing.T) {
			reset := sandbox.SetWrapperForTesting(kind)
			defer reset()

			cmd, err := buildTerminalCommand(TerminalOptions{Command: "./" + name})
			if err != nil {
				t.Fatalf("buildTerminalCommand: %v", err)
			}
			if len(cmd.Args) == 0 || cmd.Args[len(cmd.Args)-1] != localPath {
				t.Fatalf("terminal argv = %#v, want exact process-cwd target %q", cmd.Args, localPath)
			}
			if kind == sandbox.WrapperNone && cmd.Path != localPath {
				t.Fatalf("terminal path = %q, want process-cwd target %q", cmd.Path, localPath)
			}
		})
	}
}

func TestBuildTerminalCommandPreservesTrailingWhitespaceExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows strips trailing path whitespace")
	}
	path := filepath.Join(t.TempDir(), "tool ")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}

	for _, kind := range []sandbox.WrapperKind{sandbox.WrapperNone, sandbox.WrapperBwrap, sandbox.WrapperSandboxExec} {
		t.Run(string(kind), func(t *testing.T) {
			reset := sandbox.SetWrapperForTesting(kind)
			defer reset()

			cmd, err := buildTerminalCommand(TerminalOptions{Command: path})
			if err != nil {
				t.Fatalf("buildTerminalCommand: %v", err)
			}
			if len(cmd.Args) == 0 || cmd.Args[len(cmd.Args)-1] != path {
				t.Fatalf("terminal argv = %#v, want exact whitespace-preserving target %q", cmd.Args, path)
			}
			if kind == sandbox.WrapperNone && cmd.Path != path {
				t.Fatalf("terminal path = %q, want exact whitespace-preserving %q", cmd.Path, path)
			}
		})
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
