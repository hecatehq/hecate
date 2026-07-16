//go:build !windows

package terminalapp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/workspacecoord"
)

func TestApplicationShutdownDrainsBackgroundDescendantBeforeLeaseRelease(t *testing.T) {
	t.Parallel()

	workspacePath := t.TempDir()
	registry := workspacecoord.NewRegistry()
	app := New(Options{Enabled: true, WorkspaceCoordinator: registry})
	snapshot, err := app.Start(t.Context(), StartCommand{
		Workspace: workspacePath,
		Command:   "sh",
		Args:      []string{"-c", `sleep 60 & printf 'spawned:%s\n' "$!"`},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = app.Release(context.Background(), snapshot.ID) })
	waitForOutput(t, app, snapshot.ID, "spawned:")

	waitCtx, cancelWait := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancelWait()
	if _, err := app.Wait(waitCtx, snapshot.ID); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait error = %v, want deadline while background child remains live", err)
	}
	if _, err := registry.TryClose(t.Context(), workspacePath); !errors.Is(err, workspacecoord.ErrBusy) {
		t.Fatalf("TryClose before descendant drain error = %v, want ErrBusy", err)
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelShutdown()
	if err := app.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	closure, err := registry.TryClose(t.Context(), workspacePath)
	if err != nil {
		t.Fatalf("TryClose after Shutdown: %v", err)
	}
	closure.Release()
}

func TestApplicationRejectsUnixTerminalDetachmentBeforeLeaseIsRetained(t *testing.T) {
	t.Parallel()

	workspacePath := t.TempDir()
	marker := filepath.Join(workspacePath, "spawned")
	registry := workspacecoord.NewRegistry()
	app := New(Options{Enabled: true, WorkspaceCoordinator: registry})
	_, err := app.Start(t.Context(), StartCommand{
		Workspace: workspacePath,
		Command:   "sh",
		Args:      []string{"-c", "printf spawned > " + marker + "; setsid sleep 60"},
	})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("Start error = %T %v, want ErrValidation", err, err)
	}
	if !strings.Contains(err.Error(), "process group") {
		t.Fatalf("Start error = %q, want process-group explanation", err)
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("marker stat error = %v, want command not spawned", statErr)
	}
	closure, closeErr := registry.TryClose(t.Context(), workspacePath)
	if closeErr != nil {
		t.Fatalf("TryClose after rejected Start: %v", closeErr)
	}
	closure.Release()
}

func TestApplicationRejectsSplitInteractiveDetachmentInput(t *testing.T) {
	t.Parallel()

	workspacePath := t.TempDir()
	marker := filepath.Join(workspacePath, "escaped")
	app := New(Options{Enabled: true, WorkspaceCoordinator: workspacecoord.NewRegistry()})
	snapshot, err := app.Start(t.Context(), StartCommand{Workspace: workspacePath, Command: "sh"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = app.Release(context.Background(), snapshot.ID) })
	if _, err := app.Write(t.Context(), snapshot.ID, "set"); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	_, err = app.Write(t.Context(), snapshot.ID, "sid sh -c 'printf escaped > "+marker+"'\n")
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("second Write error = %T %v, want ErrValidation", err, err)
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("marker stat error = %v, want rejected input not executed", statErr)
	}
}
