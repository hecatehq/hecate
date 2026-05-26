package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/config"
)

func TestWorkspaceOpenRejectsNonLoopbackClient(t *testing.T) {
	t.Parallel()

	handler := newTestHTTPHandlerWithConfig(slog.New(slog.NewJSONHandler(io.Discard, nil)), &fakeProvider{name: "openai"}, config.Config{})
	req := httptest.NewRequest(http.MethodPost, "/hecate/v1/workspace-open", strings.NewReader(`{"path":"/tmp","target":"finder"}`))
	req.RemoteAddr = "203.0.113.12:4321"
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d, body=%s", recorder.Code, http.StatusForbidden, recorder.Body.String())
	}
}

func TestWorkspaceOpenLaunchesValidatedLocalTarget(t *testing.T) {
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", dir, err)
	}
	var gotPath string
	var gotTarget workspaceOpenTarget
	original := launchWorkspaceOpenTarget
	launchWorkspaceOpenTarget = func(path string, target workspaceOpenTarget) error {
		gotPath = path
		gotTarget = target
		return nil
	}
	t.Cleanup(func() {
		launchWorkspaceOpenTarget = original
	})

	handler := newTestHTTPHandlerWithConfig(slog.New(slog.NewJSONHandler(io.Discard, nil)), &fakeProvider{name: "openai"}, config.Config{})
	body, err := json.Marshal(WorkspaceOpenRequest{Path: filepath.Join(dir, "."), Target: "cursor"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/hecate/v1/workspace-open", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:4321"
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if gotPath != resolved {
		t.Fatalf("launched path = %q, want %q", gotPath, resolved)
	}
	if gotTarget != workspaceOpenCursor {
		t.Fatalf("launched target = %q, want %q", gotTarget, workspaceOpenCursor)
	}
	payload := decodeRecorder[WorkspaceOpenResponse](t, recorder)
	if payload.Object != "workspace_open" || payload.Data.Path != resolved || payload.Data.Target != "cursor" {
		t.Fatalf("response = %#v, want workspace_open for cursor", payload)
	}
}

func TestWorkspaceOpenRejectsFilesAndUnknownTargets(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "README.md")
	if err := os.WriteFile(file, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, _, err := validateWorkspaceOpenRequest(file, "cursor"); err == nil || !strings.Contains(err.Error(), "selected workspace is not a directory") {
		t.Fatalf("validateWorkspaceOpenRequest(file) error = %v, want file rejection", err)
	}
	if _, _, err := validateWorkspaceOpenRequest(t.TempDir(), "unknown"); err == nil || !strings.Contains(err.Error(), "unknown workspace open target") {
		t.Fatalf("validateWorkspaceOpenRequest(target) error = %v, want target rejection", err)
	}
}

func TestWorkspaceOpenLoopbackDetection(t *testing.T) {
	t.Parallel()

	for _, addr := range []string{"127.0.0.1:8765", "[::1]:8765", "::1"} {
		if !isLoopbackRemoteAddr(addr) {
			t.Fatalf("isLoopbackRemoteAddr(%q) = false, want true", addr)
		}
	}
	for _, addr := range []string{"", "203.0.113.10:8765", "example.com:8765"} {
		if isLoopbackRemoteAddr(addr) {
			t.Fatalf("isLoopbackRemoteAddr(%q) = true, want false", addr)
		}
	}
}
