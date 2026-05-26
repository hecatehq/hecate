package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/ncruces/zenity"
)

func TestWorkspaceDialogCancelled(t *testing.T) {
	t.Parallel()

	got, err := chooseWorkspaceDirectoryWithPicker(context.Background(), func(context.Context) (string, error) {
		return "", zenity.ErrCanceled
	})
	if err != nil {
		t.Fatalf("chooseWorkspaceDirectoryWithPicker() error = %v, want nil", err)
	}
	if got != "" {
		t.Fatalf("chooseWorkspaceDirectoryWithPicker() = %q, want empty cancellation", got)
	}
}

func TestWorkspaceDialogUnsupported(t *testing.T) {
	t.Parallel()

	_, err := chooseWorkspaceDirectoryWithPicker(context.Background(), func(context.Context) (string, error) {
		return "", zenity.ErrUnsupported
	})
	if !errors.Is(err, errWorkspaceDialogUnsupported) {
		t.Fatalf("chooseWorkspaceDirectoryWithPicker() error = %v, want unsupported", err)
	}
}

func TestWorkspaceDialogSingleFlightRejectsSecondDialog(t *testing.T) {
	t.Parallel()

	var active atomic.Bool
	active.Store(true)

	_, err := chooseWorkspaceDirectorySingleFlight(context.Background(), func(context.Context) (string, error) {
		t.Fatal("picker should not run while another dialog is active")
		return "", nil
	}, &active)
	if !errors.Is(err, errWorkspaceDialogAlreadyOpen) {
		t.Fatalf("chooseWorkspaceDirectorySingleFlight() error = %v, want already-open", err)
	}
}

func TestWorkspaceDialogSingleFlightReleasesAfterPickerReturns(t *testing.T) {
	t.Parallel()

	var active atomic.Bool
	dir := t.TempDir()
	got, err := chooseWorkspaceDirectorySingleFlight(context.Background(), func(context.Context) (string, error) {
		if !active.Load() {
			t.Fatal("dialog should be marked active while picker runs")
		}
		return dir, nil
	}, &active)
	if err != nil {
		t.Fatalf("chooseWorkspaceDirectorySingleFlight() error = %v", err)
	}
	if got == "" {
		t.Fatal("chooseWorkspaceDirectorySingleFlight() = empty path, want selected directory")
	}
	if active.Load() {
		t.Fatal("dialog active flag still set after picker returned")
	}
}

func TestWorkspaceDialogCanonicalizesSelectedDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	got, err := chooseWorkspaceDirectoryWithPicker(context.Background(), func(context.Context) (string, error) {
		return filepath.Join(dir, "."), nil
	})
	if err != nil {
		t.Fatalf("chooseWorkspaceDirectoryWithPicker() error = %v", err)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", dir, err)
	}
	if got != want {
		t.Fatalf("chooseWorkspaceDirectoryWithPicker() = %q, want %q", got, want)
	}
}

func TestWorkspaceDialogRejectsSelectedFile(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "README.md")
	if err := os.WriteFile(file, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	_, err := chooseWorkspaceDirectoryWithPicker(context.Background(), func(context.Context) (string, error) {
		return file, nil
	})
	if err == nil || !strings.Contains(err.Error(), "selected workspace is not a directory") {
		t.Fatalf("chooseWorkspaceDirectoryWithPicker() error = %v, want selected-file error", err)
	}
}

func TestWorkspaceDialogRejectsNonLoopbackClient(t *testing.T) {
	t.Parallel()

	handler := newTestHTTPHandlerWithConfig(slog.New(slog.NewJSONHandler(io.Discard, nil)), &fakeProvider{name: "openai"}, config.Config{})
	req := httptest.NewRequest(http.MethodPost, "/hecate/v1/workspace-dialog", nil)
	req.RemoteAddr = "203.0.113.12:4321"
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d, body=%s", recorder.Code, http.StatusForbidden, recorder.Body.String())
	}
}

func TestWorkspaceDialogRejectsForwardedClientHeaders(t *testing.T) {
	t.Parallel()

	handler := newTestHTTPHandlerWithConfig(slog.New(slog.NewJSONHandler(io.Discard, nil)), &fakeProvider{name: "openai"}, config.Config{})
	for _, header := range []string{"X-Forwarded-For", "X-Real-IP"} {
		req := httptest.NewRequest(http.MethodPost, "/hecate/v1/workspace-dialog", nil)
		req.RemoteAddr = "127.0.0.1:4321"
		req.Header.Set(header, "203.0.113.12")
		recorder := httptest.NewRecorder()

		handler.ServeHTTP(recorder, req)

		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s status = %d, want %d, body=%s", header, recorder.Code, http.StatusForbidden, recorder.Body.String())
		}
	}
}
