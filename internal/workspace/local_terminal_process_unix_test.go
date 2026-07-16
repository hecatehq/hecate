//go:build !windows

package workspace

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/sandbox"
)

func TestLocalTerminal_CloseTerminatesBackgroundDescendant(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	term, err := NewLocalWorkspace().OpenTerminal(context.Background(), TerminalOptions{
		Command:          "sh",
		Args:             []string{"-c", `sh -c 'printf "child-stdout\n"; printf "child-stderr\n" >&2; sleep 60' & printf 'spawned:%s\n' "$!"`},
		WorkingDirectory: dir,
		Policy:           Policy{AllowedRoot: dir},
	})
	if err != nil {
		t.Fatalf("OpenTerminal: %v", err)
	}
	t.Cleanup(func() { _ = term.Close(context.Background()) })

	seen := map[string]bool{}
	outputDeadline := time.After(2 * time.Second)
	for !seen["spawned:"] || !seen["child-stdout"] || !seen["child-stderr"] {
		select {
		case chunk, ok := <-term.Output():
			if !ok {
				t.Fatalf("terminal output closed before markers arrived: %v", seen)
			}
			for _, marker := range []string{"spawned:", "child-stdout", "child-stderr"} {
				if strings.Contains(chunk.Text, marker) {
					seen[marker] = true
				}
			}
		case <-outputDeadline:
			t.Fatalf("terminal output markers = %v, want parent plus inherited stdout/stderr", seen)
		}
	}

	// The command leader exits immediately, but the inherited output handles
	// and owned process group keep the terminal live until its child exits.
	waitCtx, cancelWait := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancelWait()
	if _, err := term.WaitForExit(waitCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitForExit error = %v, want deadline while background child remains live", err)
	}

	closeCtx, cancelClose := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelClose()
	if err := term.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	result, err := term.WaitForExit(context.Background())
	if err != nil {
		t.Fatalf("WaitForExit after Close: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("leader exit code = %d, want 0", result.ExitCode)
	}
	if !strings.Contains(result.Stdout, "child-stdout") || !strings.Contains(result.Stderr, "child-stderr") {
		t.Fatalf("captured output = stdout %q, stderr %q; want background child streams", result.Stdout, result.Stderr)
	}
}

func TestLocalTerminal_CloseDrainsNonDetachingBackgroundForms(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command string
		script  string
	}{
		{name: "nohup", command: "sh", script: "nohup sleep 60 >/dev/null 2>&1 & printf 'ready\\n'"},
		{name: "disown", command: "bash", script: "sleep 60 >/dev/null 2>&1 & disown; printf 'ready\\n'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := exec.LookPath(tc.command); err != nil {
				t.Skipf("%s unavailable: %v", tc.command, err)
			}

			dir := t.TempDir()
			term, err := NewLocalWorkspace().OpenTerminal(context.Background(), TerminalOptions{
				Command:          tc.command,
				Args:             []string{"-c", tc.script},
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
					t.Fatalf("output = %q, want ready", chunk.Text)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("terminal did not report readiness")
			}
			waitCtx, cancelWait := context.WithTimeout(context.Background(), 75*time.Millisecond)
			defer cancelWait()
			if _, err := term.WaitForExit(waitCtx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("WaitForExit error = %v, want deadline while owned background child remains live", err)
			}
			closeCtx, cancelClose := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancelClose()
			if err := term.Close(closeCtx); err != nil {
				t.Fatalf("Close: %v", err)
			}
		})
	}
}

func TestLocalTerminal_WriteRejectsSplitSetsidCommand(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	marker := filepath.Join(dir, "escaped-writer")
	term, err := NewLocalWorkspace().OpenTerminal(context.Background(), TerminalOptions{
		Command:          "sh",
		WorkingDirectory: dir,
		Policy:           Policy{AllowedRoot: dir},
	})
	if err != nil {
		t.Fatalf("OpenTerminal: %v", err)
	}
	t.Cleanup(func() { _ = term.Close(context.Background()) })
	if err := term.Write(context.Background(), "set"); err != nil {
		t.Fatalf("Write first fragment: %v", err)
	}
	if err := term.Write(context.Background(), "sid sh -c 'printf escaped > escaped-writer'\n"); !sandbox.IsPolicyDenied(err) {
		t.Fatalf("Write detachment fragment error = %T %v, want PolicyError", err, err)
	}
	time.Sleep(50 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("escaped writer marker exists or is unreadable: %v", err)
	}
}

func TestLocalTerminal_WriteRejectsDetachmentForExplicitShellStdin(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skipf("bash unavailable: %v", err)
	}

	dir := t.TempDir()
	marker := filepath.Join(dir, "escaped-shell-writer")
	term, err := NewLocalWorkspace().OpenTerminal(context.Background(), TerminalOptions{
		Command:          "bash",
		Args:             []string{"-"},
		WorkingDirectory: dir,
		Policy:           Policy{AllowedRoot: dir},
	})
	if err != nil {
		t.Fatalf("OpenTerminal: %v", err)
	}
	t.Cleanup(func() { _ = term.Close(context.Background()) })
	input := "setsid sh -c 'printf escaped > escaped-shell-writer'\n"
	if err := term.Write(context.Background(), input); !sandbox.IsPolicyDenied(err) {
		t.Fatalf("Write detachment command error = %T %v, want PolicyError", err, err)
	}
	time.Sleep(50 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("escaped shell writer marker exists or is unreadable: %v", err)
	}
}

func TestLocalTerminal_WriteRejectsDetachmentForInteractivePythonInlineCommand(t *testing.T) {
	t.Parallel()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skipf("python3 unavailable: %v", err)
	}

	dir := t.TempDir()
	term, err := NewLocalWorkspace().OpenTerminal(context.Background(), TerminalOptions{
		Command:          python,
		Args:             []string{"-i", "-c", "print('ready')"},
		WorkingDirectory: dir,
		Policy:           Policy{AllowedRoot: dir},
	})
	if err != nil {
		t.Fatalf("OpenTerminal: %v", err)
	}
	t.Cleanup(func() { _ = term.Close(context.Background()) })
	input := "import subprocess; subprocess.Popen(['sh', '-c', 'true'], start_new_session=True)\n"
	if err := term.Write(context.Background(), input); !sandbox.IsPolicyDenied(err) {
		t.Fatalf("Write detachment code error = %T %v, want PolicyError", err, err)
	}
}

func TestLocalTerminal_WriteRejectsMonitorFlagAfterLongSplit(t *testing.T) {
	t.Parallel()

	stdin := &scriptedTerminalWriter{}
	term := &localTerminal{
		stdin:           stdin,
		inputPolicyMode: sandbox.SupervisedTerminalInputShell,
	}
	prefix := "set " + strings.Repeat(" ", 256)
	if err := term.Write(context.Background(), prefix); err != nil {
		t.Fatalf("Write long first fragment: %v", err)
	}
	if err := term.Write(context.Background(), "-m\n"); !sandbox.IsPolicyDenied(err) {
		t.Fatalf("Write monitor fragment error = %T %v, want PolicyError", err, err)
	}
	if got := term.inputPolicyState.RetainedBytes(); got != len(prefix) {
		t.Fatalf("retained input policy state = %d bytes, want %d", got, len(prefix))
	}
}

func TestLocalTerminal_WriteFailsClosedAtIncompleteInputPolicyLimit(t *testing.T) {
	t.Parallel()

	stdin := &scriptedTerminalWriter{}
	term := &localTerminal{
		stdin:           stdin,
		inputPolicyMode: sandbox.SupervisedTerminalInputShell,
	}
	prefix := strings.Repeat(" ", sandbox.SupervisedTerminalInputPendingLimit)
	if err := term.Write(context.Background(), prefix); err != nil {
		t.Fatalf("Write input at policy limit: %v", err)
	}
	if err := term.Write(context.Background(), "x"); !sandbox.IsPolicyDenied(err) {
		t.Fatalf("Write beyond policy limit error = %T %v, want PolicyError", err, err)
	}
	if got := term.inputPolicyState.RetainedBytes(); got != sandbox.SupervisedTerminalInputPendingLimit {
		t.Fatalf("retained input policy state = %d bytes, want %d", got, sandbox.SupervisedTerminalInputPendingLimit)
	}
}

func TestLocalTerminal_WriteRetainsMultilineIncompleteShellState(t *testing.T) {
	t.Parallel()

	term := &localTerminal{
		stdin:           &scriptedTerminalWriter{},
		inputPolicyMode: sandbox.SupervisedTerminalInputShell,
	}
	if err := term.Write(context.Background(), "if true; then\n"); err != nil {
		t.Fatalf("Write compound prefix: %v", err)
	}
	if got := term.inputPolicyState.RetainedBytes(); got == 0 {
		t.Fatal("compound prefix was discarded before its closing command")
	}
	if err := term.Write(context.Background(), "  printf safe\n"); err != nil {
		t.Fatalf("Write compound body: %v", err)
	}
	if got := term.inputPolicyState.RetainedBytes(); got == 0 {
		t.Fatal("incomplete compound body was discarded before fi")
	}
	if err := term.Write(context.Background(), "fi\n"); err != nil {
		t.Fatalf("Write compound terminator: %v", err)
	}
	if got := term.inputPolicyState.RetainedBytes(); got != 0 {
		t.Fatalf("retained input after complete compound command = %d bytes, want 0", got)
	}
}

func TestLocalTerminal_WriteRejectsMonitorFlagAcrossBackslashContinuation(t *testing.T) {
	t.Parallel()

	term := &localTerminal{
		stdin:           &scriptedTerminalWriter{},
		inputPolicyMode: sandbox.SupervisedTerminalInputShell,
	}
	if err := term.Write(context.Background(), "set \\\n"); err != nil {
		t.Fatalf("Write continued command prefix: %v", err)
	}
	if err := term.Write(context.Background(), strings.Repeat(" ", 256)); err != nil {
		t.Fatalf("Write continued command spacing: %v", err)
	}
	if err := term.Write(context.Background(), "-m\n"); !sandbox.IsPolicyDenied(err) {
		t.Fatalf("Write continued monitor flag error = %T %v, want PolicyError", err, err)
	}
}

func TestLocalTerminal_WriteDiscardsCompleteCommandHistory(t *testing.T) {
	t.Parallel()

	term := &localTerminal{
		stdin:           &scriptedTerminalWriter{},
		inputPolicyMode: sandbox.SupervisedTerminalInputShell,
	}
	for index := 0; index < 5000; index++ {
		if err := term.Write(context.Background(), "true\n"); err != nil {
			t.Fatalf("Write complete command %d: %v", index, err)
		}
		if got := term.inputPolicyState.RetainedBytes(); got != 0 {
			t.Fatalf("retained input after complete command %d = %d bytes, want 0", index, got)
		}
	}
}

func TestLocalTerminal_LongLivedShellDoesNotAccumulateCompletedInput(t *testing.T) {
	t.Parallel()

	term := &localTerminal{
		stdin:           &scriptedTerminalWriter{},
		inputPolicyMode: sandbox.SupervisedTerminalInputShell,
	}
	command := "printf safe # " + strings.Repeat("x", 2048) + "\n"
	for index := 0; index < 600; index++ {
		if err := term.Write(context.Background(), command); err != nil {
			t.Fatalf("Write long-lived command %d: %v", index, err)
		}
	}
	if got := term.inputPolicyState.RetainedBytes(); got != 0 {
		t.Fatalf("retained input after long-lived complete commands = %d bytes, want 0", got)
	}
}

func TestLocalTerminal_SingleLargeWriteDiscardsCompleteCommands(t *testing.T) {
	t.Parallel()

	term := &localTerminal{
		stdin:           &scriptedTerminalWriter{},
		inputPolicyMode: sandbox.SupervisedTerminalInputShell,
	}
	var input strings.Builder
	for input.Len() <= sandbox.SupervisedTerminalInputPendingLimit {
		input.WriteString("true\n")
	}
	if err := term.Write(context.Background(), input.String()); err != nil {
		t.Fatalf("Write complete commands larger than pending limit: %v", err)
	}
	if got := term.inputPolicyState.RetainedBytes(); got != 0 {
		t.Fatalf("retained input after large complete write = %d bytes, want 0", got)
	}
}

func TestLocalTerminal_WriteSerializesPolicyValidationWithPipeOrder(t *testing.T) {
	t.Parallel()

	stdin := newBlockingTerminalWriter()
	term := &localTerminal{
		stdin:           stdin,
		inputPolicyMode: sandbox.SupervisedTerminalInputShell,
	}
	firstResult := make(chan error, 1)
	go func() {
		firstResult <- term.Write(context.Background(), "printf first\\n")
	}()
	select {
	case <-stdin.firstEntered:
	case <-time.After(time.Second):
		t.Fatal("first terminal write did not reach the pipe")
	}

	secondStarted := make(chan struct{})
	secondResult := make(chan error, 1)
	go func() {
		close(secondStarted)
		secondResult <- term.Write(context.Background(), "printf second\\n")
	}()
	<-secondStarted
	select {
	case <-stdin.secondEntered:
		t.Fatal("second terminal write reached the pipe before the first write committed")
	case <-time.After(100 * time.Millisecond):
	}

	close(stdin.releaseFirst)
	for index, result := range []<-chan error{firstResult, secondResult} {
		select {
		case err := <-result:
			if err != nil {
				t.Fatalf("Write %d error = %v", index+1, err)
			}
		case <-time.After(time.Second):
			t.Fatalf("Write %d did not return", index+1)
		}
	}
}

func TestLocalTerminal_WriteTracksOnlyCommittedBytes(t *testing.T) {
	t.Parallel()

	writeFailure := errors.New("pipe write failed")
	stdin := &scriptedTerminalWriter{results: []terminalWriteResult{
		{err: writeFailure},
		{n: len("sid echo safe\n")},
	}}
	term := &localTerminal{
		stdin:           stdin,
		inputPolicyMode: sandbox.SupervisedTerminalInputShell,
	}
	if err := term.Write(context.Background(), "set"); !errors.Is(err, writeFailure) {
		t.Fatalf("failed Write error = %v, want %v", err, writeFailure)
	}
	if got := term.inputPolicyState.RetainedBytes(); got != 0 {
		t.Fatalf("retained input after zero-byte write = %d bytes, want 0", got)
	}
	if err := term.Write(context.Background(), "sid echo safe\n"); err != nil {
		t.Fatalf("subsequent Write error = %v; failed bytes must not create a phantom setsid command", err)
	}
	if got := term.inputPolicyState.RetainedBytes(); got != 0 {
		t.Fatalf("retained input after complete committed command = %d bytes, want 0", got)
	}
}

func TestLocalTerminal_WriteFailsClosedOnShortWrite(t *testing.T) {
	t.Parallel()

	stdin := &scriptedTerminalWriter{results: []terminalWriteResult{{n: 3}}}
	term := &localTerminal{
		stdin:           stdin,
		inputPolicyMode: sandbox.SupervisedTerminalInputShell,
	}
	if err := term.Write(context.Background(), "echo safe\n"); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("Write error = %v, want io.ErrShortWrite", err)
	}
	if got := term.inputPolicyState.RetainedBytes(); got != len("ech") {
		t.Fatalf("retained input after short write = %d bytes, want %d", got, len("ech"))
	}
}

type blockingTerminalWriter struct {
	mu            sync.Mutex
	calls         int
	firstEntered  chan struct{}
	secondEntered chan struct{}
	releaseFirst  chan struct{}
}

func newBlockingTerminalWriter() *blockingTerminalWriter {
	return &blockingTerminalWriter{
		firstEntered:  make(chan struct{}),
		secondEntered: make(chan struct{}),
		releaseFirst:  make(chan struct{}),
	}
}

func (writer *blockingTerminalWriter) Write(input []byte) (int, error) {
	writer.mu.Lock()
	writer.calls++
	call := writer.calls
	writer.mu.Unlock()
	switch call {
	case 1:
		close(writer.firstEntered)
		<-writer.releaseFirst
	case 2:
		close(writer.secondEntered)
	}
	return len(input), nil
}

func (*blockingTerminalWriter) Close() error { return nil }

type terminalWriteResult struct {
	n   int
	err error
}

type scriptedTerminalWriter struct {
	mu      sync.Mutex
	results []terminalWriteResult
}

func (writer *scriptedTerminalWriter) Write(input []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if len(writer.results) == 0 {
		return len(input), nil
	}
	result := writer.results[0]
	writer.results = writer.results[1:]
	return result.n, result.err
}

func (*scriptedTerminalWriter) Close() error { return nil }
