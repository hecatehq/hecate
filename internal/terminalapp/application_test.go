package terminalapp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/hecatehq/hecate/internal/workspace"
	"github.com/hecatehq/hecate/internal/workspacecoord"
)

func TestApplicationDisabledByDefault(t *testing.T) {
	t.Parallel()

	app := New(Options{})
	_, err := app.Start(context.Background(), StartCommand{Workspace: t.TempDir(), Command: "true"})
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("Start error = %v, want ErrDisabled", err)
	}
}

func TestApplicationRequiresWorkspaceCoordinator(t *testing.T) {
	t.Parallel()

	app := New(Options{Enabled: true})
	_, err := app.Start(t.Context(), StartCommand{Workspace: t.TempDir(), Command: "true"})
	if err == nil || !strings.Contains(err.Error(), "workspace coordination is unavailable") {
		t.Fatalf("Start error = %v, want unavailable workspace coordination", err)
	}
}

func TestApplicationRejectsStartDuringWorkspaceClosure(t *testing.T) {
	t.Parallel()

	workspacePath := t.TempDir()
	registry := workspacecoord.NewRegistry()
	closure, err := registry.TryClose(t.Context(), workspacePath)
	if err != nil {
		t.Fatalf("TryClose: %v", err)
	}
	defer closure.Release()
	app := New(Options{Enabled: true, WorkspaceCoordinator: registry})
	_, err = app.Start(t.Context(), StartCommand{Workspace: workspacePath, Command: "true"})
	if !errors.Is(err, ErrWorkspaceBusy) {
		t.Fatalf("Start error = %v, want ErrWorkspaceBusy", err)
	}
}

func TestApplicationTerminalLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell semantics")
	}
	t.Parallel()

	dir := t.TempDir()
	app := New(Options{Enabled: true, WorkspaceCoordinator: workspacecoord.NewRegistry()})
	snap, err := app.Start(context.Background(), StartCommand{
		Workspace: dir,
		Command:   "sh",
		Args:      []string{"-c", "printf hello; printf err 1>&2"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = app.Release(context.Background(), snap.ID) })

	wait, err := app.Wait(context.Background(), snap.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if wait.Running {
		t.Fatalf("Wait snapshot running = true, want false")
	}
	if wait.ExitCode == nil || *wait.ExitCode != 0 {
		t.Fatalf("Wait exit code = %v, want 0", wait.ExitCode)
	}
	if !strings.Contains(wait.Output, "hello") || !strings.Contains(wait.Output, "err") {
		t.Fatalf("Wait output = %q, want stdout and stderr", wait.Output)
	}

	if err := app.Release(context.Background(), snap.ID); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := app.Output(snap.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Output after release error = %v, want ErrNotFound", err)
	}
}

func TestApplicationTerminalWrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell semantics")
	}
	t.Parallel()

	app := New(Options{Enabled: true, WorkspaceCoordinator: workspacecoord.NewRegistry()})
	snap, err := app.Start(context.Background(), StartCommand{
		Workspace: t.TempDir(),
		Command:   "cat",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = app.Release(context.Background(), snap.ID) })

	if _, err := app.Write(context.Background(), snap.ID, "ping\n"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	waitForOutput(t, app, snap.ID, "ping")
}

func TestApplicationLiveTerminalBlocksOverlappingWorkspaceClosure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell semantics")
	}
	t.Parallel()

	root := t.TempDir()
	workspacePath := filepath.Join(root, "nested")
	if err := os.Mkdir(workspacePath, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	registry := workspacecoord.NewRegistry()
	app := New(Options{Enabled: true, WorkspaceCoordinator: registry})
	snap, err := app.Start(t.Context(), StartCommand{
		Workspace: workspacePath,
		Command:   "cat",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = app.Release(context.Background(), snap.ID) })

	if _, err := registry.TryClose(t.Context(), root); !errors.Is(err, workspacecoord.ErrBusy) {
		t.Fatalf("TryClose(parent) with live nested terminal error = %v, want ErrBusy", err)
	}

	if err := app.Release(t.Context(), snap.ID); err != nil {
		t.Fatalf("Release: %v", err)
	}
	closure, err := registry.TryClose(t.Context(), root)
	if err != nil {
		t.Fatalf("TryClose(parent) after release: %v", err)
	}
	closure.Release()
}

func TestApplicationExitedTerminalReleasesWorkspaceWriter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell semantics")
	}
	t.Parallel()

	workspacePath := t.TempDir()
	registry := workspacecoord.NewRegistry()
	app := New(Options{Enabled: true, WorkspaceCoordinator: registry})
	snap, err := app.Start(t.Context(), StartCommand{
		Workspace: workspacePath,
		Command:   "sh",
		Args:      []string{"-c", "true"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = app.Release(context.Background(), snap.ID) })
	if _, err := app.Wait(t.Context(), snap.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	closure, err := registry.TryClose(t.Context(), workspacePath)
	if err != nil {
		t.Fatalf("TryClose after process exit: %v", err)
	}
	closure.Release()
}

func TestApplicationOpenFailureReleasesWorkspaceWriter(t *testing.T) {
	t.Parallel()

	workspacePath := t.TempDir()
	registry := workspacecoord.NewRegistry()
	app := New(Options{Enabled: true, WorkspaceCoordinator: registry})
	missingCommand := filepath.Join(t.TempDir(), "missing-terminal-command")
	if _, err := app.Start(t.Context(), StartCommand{
		Workspace: workspacePath,
		Command:   missingCommand,
	}); err == nil {
		t.Fatal("Start error = nil, want missing command failure")
	}

	closure, err := registry.TryClose(t.Context(), workspacePath)
	if err != nil {
		t.Fatalf("TryClose after failed OpenTerminal: %v", err)
	}
	closure.Release()
}

func TestApplicationShutdownReleasesWorkspaceWriter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell semantics")
	}
	t.Parallel()

	workspacePath := t.TempDir()
	registry := workspacecoord.NewRegistry()
	app := New(Options{Enabled: true, WorkspaceCoordinator: registry})
	if _, err := app.Start(t.Context(), StartCommand{
		Workspace: workspacePath,
		Command:   "cat",
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := app.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	closure, err := registry.TryClose(t.Context(), workspacePath)
	if err != nil {
		t.Fatalf("TryClose after Shutdown: %v", err)
	}
	closure.Release()
}

func TestApplicationFailedReleaseRemainsReachableUntilObservedExit(t *testing.T) {
	t.Parallel()

	workspacePath := t.TempDir()
	registry := workspacecoord.NewRegistry()
	term := newControlledTerminal(errors.New("close interrupted"))
	app := New(Options{
		Enabled:              true,
		WorkspaceCoordinator: registry,
		NewWorkspace: func() workspace.Workspace {
			return &terminalTestWorkspace{openTerminal: func(context.Context, workspace.TerminalOptions) (workspace.Terminal, error) {
				return term, nil
			}}
		},
	})
	snap, err := app.Start(t.Context(), StartCommand{Workspace: workspacePath, Command: "test"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := app.Release(t.Context(), snap.ID); err == nil || !strings.Contains(err.Error(), "close interrupted") {
		t.Fatalf("Release error = %v, want close interruption", err)
	}
	if _, err := app.Output(snap.ID); err != nil {
		t.Fatalf("Output after failed Release: %v", err)
	}
	if _, err := registry.TryClose(t.Context(), workspacePath); !errors.Is(err, workspacecoord.ErrBusy) {
		t.Fatalf("TryClose before observed exit error = %v, want ErrBusy", err)
	}

	term.exit()
	if _, err := app.Wait(t.Context(), snap.ID); err != nil {
		t.Fatalf("Wait after exit: %v", err)
	}
	closure, err := registry.TryClose(t.Context(), workspacePath)
	if err != nil {
		t.Fatalf("TryClose after observed exit: %v", err)
	}
	closure.Release()
	if err := app.Release(t.Context(), snap.ID); err != nil {
		t.Fatalf("Release after observed exit: %v", err)
	}
	if _, err := app.Output(snap.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Output after successful Release error = %v, want ErrNotFound", err)
	}
}

func TestApplicationReleaseCompletedTerminalIgnoresCancelledContext(t *testing.T) {
	t.Parallel()

	term := newControlledTerminal(nil)
	app := New(Options{
		Enabled:              true,
		WorkspaceCoordinator: workspacecoord.NewRegistry(),
		NewWorkspace: func() workspace.Workspace {
			return &terminalTestWorkspace{openTerminal: func(context.Context, workspace.TerminalOptions) (workspace.Terminal, error) {
				return term, nil
			}}
		},
	})
	snapshot, err := app.Start(t.Context(), StartCommand{Workspace: t.TempDir(), Command: "test"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	term.exit()
	if _, err := app.Wait(t.Context(), snapshot.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := app.Release(ctx, snapshot.ID); err != nil {
		t.Fatalf("Release completed terminal: %v", err)
	}
	if _, err := app.Output(snapshot.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Output after Release error = %v, want ErrNotFound", err)
	}
}

func TestApplicationShutdownWaitsForInFlightStart(t *testing.T) {
	t.Parallel()

	workspacePath := t.TempDir()
	registry := workspacecoord.NewRegistry()
	openStarted := make(chan struct{})
	allowOpen := make(chan struct{})
	term := newControlledTerminal(nil)
	app := New(Options{
		Enabled:              true,
		WorkspaceCoordinator: registry,
		NewWorkspace: func() workspace.Workspace {
			return &terminalTestWorkspace{openTerminal: func(ctx context.Context, _ workspace.TerminalOptions) (workspace.Terminal, error) {
				close(openStarted)
				select {
				case <-allowOpen:
					return term, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}}
		},
	})
	type startResult struct {
		snapshot Snapshot
		err      error
	}
	startResultCh := make(chan startResult, 1)
	go func() {
		snapshot, err := app.Start(t.Context(), StartCommand{Workspace: workspacePath, Command: "test"})
		startResultCh <- startResult{snapshot: snapshot, err: err}
	}()
	<-openStarted

	shutdownCalled := make(chan struct{})
	shutdownResultCh := make(chan error, 1)
	go func() {
		close(shutdownCalled)
		shutdownResultCh <- app.Shutdown(t.Context())
	}()
	<-shutdownCalled
	var earlyShutdownErr error
	shutdownReturnedEarly := false
	select {
	case earlyShutdownErr = <-shutdownResultCh:
		shutdownReturnedEarly = true
	case <-time.After(100 * time.Millisecond):
	}
	close(allowOpen)
	started := <-startResultCh
	if started.err != nil {
		t.Fatalf("Start: %v", started.err)
	}
	if !shutdownReturnedEarly {
		earlyShutdownErr = <-shutdownResultCh
	}
	if earlyShutdownErr != nil {
		t.Fatalf("Shutdown: %v", earlyShutdownErr)
	}
	if shutdownReturnedEarly {
		t.Fatal("Shutdown returned while Start was still between admission and session registration")
	}
	if _, err := app.Output(started.snapshot.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Output after Shutdown error = %v, want ErrNotFound", err)
	}
	select {
	case <-term.exited:
	default:
		t.Fatal("Shutdown did not close the terminal admitted by the in-flight Start")
	}
	if _, err := app.Start(t.Context(), StartCommand{Workspace: workspacePath, Command: "test"}); !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("Start after Shutdown error = %v, want ErrShuttingDown", err)
	}
}

func TestApplicationShutdownDeadlineInterruptsInFlightStartDrain(t *testing.T) {
	t.Parallel()

	workspacePath := t.TempDir()
	openStarted := make(chan struct{})
	allowOpen := make(chan struct{})
	term := newControlledTerminal(nil)
	app := New(Options{
		Enabled:              true,
		WorkspaceCoordinator: workspacecoord.NewRegistry(),
		NewWorkspace: func() workspace.Workspace {
			return &terminalTestWorkspace{openTerminal: func(ctx context.Context, _ workspace.TerminalOptions) (workspace.Terminal, error) {
				close(openStarted)
				select {
				case <-allowOpen:
					return term, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}}
		},
	})
	type startResult struct {
		snapshot Snapshot
		err      error
	}
	startedCh := make(chan startResult, 1)
	go func() {
		snapshot, err := app.Start(t.Context(), StartCommand{Workspace: workspacePath, Command: "test"})
		startedCh <- startResult{snapshot: snapshot, err: err}
	}()
	<-openStarted

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := app.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown error = %v, want context deadline", err)
	}
	if _, err := app.Start(t.Context(), StartCommand{Workspace: workspacePath, Command: "test"}); !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("Start after timed-out Shutdown error = %v, want ErrShuttingDown", err)
	}

	close(allowOpen)
	started := <-startedCh
	if started.err != nil {
		t.Fatalf("in-flight Start: %v", started.err)
	}
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), time.Second)
	defer cancelShutdown()
	if err := app.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("retry Shutdown: %v", err)
	}
	if _, err := app.Output(started.snapshot.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Output after retry Shutdown error = %v, want ErrNotFound", err)
	}
}

func TestApplicationCancelledShutdownFencesAndForceClosesWithoutReleasingLeaseEarly(t *testing.T) {
	t.Parallel()

	workspacePath := t.TempDir()
	registry := workspacecoord.NewRegistry()
	term := newBlockingCloseTerminal()
	app := New(Options{
		Enabled:              true,
		WorkspaceCoordinator: registry,
		NewWorkspace: func() workspace.Workspace {
			return &terminalTestWorkspace{openTerminal: func(context.Context, workspace.TerminalOptions) (workspace.Terminal, error) {
				return term, nil
			}}
		},
	})
	if _, err := app.Start(t.Context(), StartCommand{Workspace: workspacePath, Command: "test"}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := app.Shutdown(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Shutdown error = %v, want context cancellation", err)
	}
	select {
	case <-term.closeStarted:
	default:
		t.Fatal("Shutdown did not invoke Close with its already-exhausted budget")
	}
	if _, err := app.Start(t.Context(), StartCommand{Workspace: workspacePath, Command: "test"}); !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("Start after cancelled Shutdown error = %v, want ErrShuttingDown", err)
	}
	if _, err := registry.TryClose(t.Context(), workspacePath); !errors.Is(err, workspacecoord.ErrBusy) {
		t.Fatalf("TryClose before terminal exit error = %v, want ErrBusy", err)
	}

	term.allowClose()
	deadline := time.Now().Add(time.Second)
	for {
		closure, err := registry.TryClose(t.Context(), workspacePath)
		if err == nil {
			closure.Release()
			break
		}
		if !errors.Is(err, workspacecoord.ErrBusy) {
			t.Fatalf("TryClose after terminal exit error = %v, want nil or ErrBusy", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("workspace writer lease was not released after observed terminal exit")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestApplicationShutdownCloseErrorForcesAndRetainsCleanup(t *testing.T) {
	t.Parallel()

	workspacePath := t.TempDir()
	registry := workspacecoord.NewRegistry()
	closeErr := errors.New("close interrupted")
	term := newControlledTerminal(closeErr)
	app := New(Options{
		Enabled:              true,
		WorkspaceCoordinator: registry,
		NewWorkspace: func() workspace.Workspace {
			return &terminalTestWorkspace{openTerminal: func(context.Context, workspace.TerminalOptions) (workspace.Terminal, error) {
				return term, nil
			}}
		},
	})
	snapshot, err := app.Start(t.Context(), StartCommand{Workspace: workspacePath, Command: "test"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := app.Shutdown(t.Context()); !errors.Is(err, closeErr) {
		t.Fatalf("Shutdown error = %v, want wrapped close interruption", err)
	}
	select {
	case <-term.exited:
	default:
		t.Fatal("Shutdown did not force the terminal after Close returned an error")
	}

	deadline := time.Now().Add(time.Second)
	for {
		_, err := app.Output(snapshot.ID)
		if errors.Is(err, ErrNotFound) {
			break
		}
		if err != nil {
			t.Fatalf("Output during retained cleanup: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("retained cleanup did not remove the exited terminal session")
		}
		time.Sleep(10 * time.Millisecond)
	}
	closure, err := registry.TryClose(t.Context(), workspacePath)
	if err != nil {
		t.Fatalf("TryClose after retained cleanup: %v", err)
	}
	closure.Release()
}

func TestApplicationConcurrentReleaseWaitHonorsContext(t *testing.T) {
	t.Parallel()

	term := newBlockingCloseTerminal()
	app := New(Options{
		Enabled:              true,
		WorkspaceCoordinator: workspacecoord.NewRegistry(),
		NewWorkspace: func() workspace.Workspace {
			return &terminalTestWorkspace{openTerminal: func(context.Context, workspace.TerminalOptions) (workspace.Terminal, error) {
				return term, nil
			}}
		},
	})
	snapshot, err := app.Start(t.Context(), StartCommand{Workspace: t.TempDir(), Command: "test"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	firstDone := make(chan error, 1)
	go func() { firstDone <- app.Release(context.Background(), snapshot.ID) }()
	select {
	case <-term.closeStarted:
	case <-time.After(time.Second):
		t.Fatal("first Release did not enter terminal Close")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := app.Release(ctx, snapshot.ID); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("concurrent Release error = %v, want context deadline", err)
	}
	term.allowClose()
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first Release: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first Release did not finish after terminal close was allowed")
	}
}

func TestApplicationConcurrentShutdownWaitHonorsContext(t *testing.T) {
	t.Parallel()

	term := newBlockingCloseTerminal()
	app := New(Options{
		Enabled:              true,
		WorkspaceCoordinator: workspacecoord.NewRegistry(),
		NewWorkspace: func() workspace.Workspace {
			return &terminalTestWorkspace{openTerminal: func(context.Context, workspace.TerminalOptions) (workspace.Terminal, error) {
				return term, nil
			}}
		},
	})
	if _, err := app.Start(t.Context(), StartCommand{Workspace: t.TempDir(), Command: "test"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	firstDone := make(chan error, 1)
	go func() { firstDone <- app.Shutdown(context.Background()) }()
	select {
	case <-term.closeStarted:
	case <-time.After(time.Second):
		t.Fatal("first Shutdown did not enter terminal Close")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := app.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("concurrent Shutdown error = %v, want context deadline", err)
	}
	term.allowClose()
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first Shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first Shutdown did not finish after terminal close was allowed")
	}
}

func TestApplicationRejectsWorkingDirectoryEscape(t *testing.T) {
	t.Parallel()

	app := New(Options{Enabled: true, WorkspaceCoordinator: workspacecoord.NewRegistry()})
	_, err := app.Start(context.Background(), StartCommand{
		Workspace:        t.TempDir(),
		WorkingDirectory: filepath.Dir(t.TempDir()),
		Command:          "true",
	})
	if err == nil || !strings.Contains(err.Error(), "escapes allowed root") {
		t.Fatalf("Start error = %v, want allowed-root escape", err)
	}
}

func TestApplicationOutputTruncatesAtUTF8Boundary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell semantics")
	}
	t.Parallel()

	app := New(Options{
		Enabled:              true,
		OutputMaxBytes:       9,
		WorkspaceCoordinator: workspacecoord.NewRegistry(),
	})
	snap, err := app.Start(context.Background(), StartCommand{
		Workspace: t.TempDir(),
		Command:   "sh",
		Args:      []string{"-c", "printf 'alpha🙂omega'"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = app.Release(context.Background(), snap.ID) })

	wait, err := app.Wait(context.Background(), snap.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !wait.Truncated {
		t.Fatal("Truncated = false, want true")
	}
	if !strings.Contains(wait.Output, "omega") {
		t.Fatalf("Output = %q, want retained tail", wait.Output)
	}
	if !utf8.ValidString(wait.Output) {
		t.Fatalf("Output = %q, want valid UTF-8", wait.Output)
	}
}

func waitForOutput(t *testing.T, app *Application, id, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snap, err := app.Output(id)
		if err == nil && strings.Contains(snap.Output, want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	snap, _ := app.Output(id)
	t.Fatalf("terminal output = %q, want %q", snap.Output, want)
}

type terminalTestWorkspace struct {
	workspace.Workspace
	openTerminal func(context.Context, workspace.TerminalOptions) (workspace.Terminal, error)
}

func (w *terminalTestWorkspace) OpenTerminal(ctx context.Context, opts workspace.TerminalOptions) (workspace.Terminal, error) {
	return w.openTerminal(ctx, opts)
}

type controlledTerminal struct {
	output   chan workspace.OutputChunk
	exited   chan struct{}
	exitOnce sync.Once
	closeErr error
}

type blockingCloseTerminal struct {
	*controlledTerminal
	closeStarted chan struct{}
	closeOnce    sync.Once
	allow        chan struct{}
	allowOnce    sync.Once
}

func newBlockingCloseTerminal() *blockingCloseTerminal {
	return &blockingCloseTerminal{
		controlledTerminal: newControlledTerminal(nil),
		closeStarted:       make(chan struct{}),
		allow:              make(chan struct{}),
	}
}

func (t *blockingCloseTerminal) Close(ctx context.Context) error {
	t.closeOnce.Do(func() { close(t.closeStarted) })
	select {
	case <-t.allow:
		t.exit()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *blockingCloseTerminal) Kill(context.Context) error { return nil }

func (t *blockingCloseTerminal) allowClose() {
	t.allowOnce.Do(func() { close(t.allow) })
}

func newControlledTerminal(closeErr error) *controlledTerminal {
	return &controlledTerminal{
		output:   make(chan workspace.OutputChunk),
		exited:   make(chan struct{}),
		closeErr: closeErr,
	}
}

func (t *controlledTerminal) ID() string { return "controlled-terminal" }

func (t *controlledTerminal) Output() <-chan workspace.OutputChunk { return t.output }

func (t *controlledTerminal) Write(context.Context, string) error { return nil }

func (t *controlledTerminal) WaitForExit(ctx context.Context) (workspace.Result, error) {
	select {
	case <-t.exited:
		return workspace.Result{}, nil
	case <-ctx.Done():
		return workspace.Result{}, ctx.Err()
	}
}

func (t *controlledTerminal) Kill(context.Context) error {
	t.exit()
	return nil
}

func (t *controlledTerminal) Close(context.Context) error {
	select {
	case <-t.exited:
		return nil
	default:
	}
	if t.closeErr != nil {
		return t.closeErr
	}
	t.exit()
	return nil
}

func (t *controlledTerminal) exit() {
	t.exitOnce.Do(func() {
		close(t.output)
		close(t.exited)
	})
}
