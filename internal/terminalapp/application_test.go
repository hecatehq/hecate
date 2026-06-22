package terminalapp

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestApplicationDisabledByDefault(t *testing.T) {
	t.Parallel()

	app := New(Options{})
	_, err := app.Start(context.Background(), StartCommand{Workspace: t.TempDir(), Command: "true"})
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("Start error = %v, want ErrDisabled", err)
	}
}

func TestApplicationTerminalLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell semantics")
	}
	t.Parallel()

	dir := t.TempDir()
	app := New(Options{Enabled: true})
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

	app := New(Options{Enabled: true})
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

func TestApplicationRejectsWorkingDirectoryEscape(t *testing.T) {
	t.Parallel()

	app := New(Options{Enabled: true})
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

	app := New(Options{Enabled: true, OutputMaxBytes: 9})
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
