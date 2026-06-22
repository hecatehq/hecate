package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/config"
)

func TestOperatorTerminalsDisabledByDefault(t *testing.T) {
	t.Parallel()

	handler := newTestHTTPHandlerWithConfig(slog.New(slog.NewJSONHandler(io.Discard, nil)), &fakeProvider{name: "openai"}, config.Config{})
	req := newTerminalRequest(t, http.MethodPost, "/hecate/v1/terminals", TerminalStartRequest{
		Workspace: t.TempDir(),
		Command:   "true",
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestOperatorTerminalsRejectNonLoopbackClient(t *testing.T) {
	t.Parallel()

	handler := newTerminalTestHandler()
	req := newTerminalRequest(t, http.MethodPost, "/hecate/v1/terminals", TerminalStartRequest{
		Workspace: t.TempDir(),
		Command:   "true",
	})
	req.RemoteAddr = "203.0.113.12:4321"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestOperatorTerminalLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell semantics")
	}
	t.Parallel()

	handler := newTerminalTestHandler()
	workspace := t.TempDir()
	createReq := newTerminalRequest(t, http.MethodPost, "/hecate/v1/terminals", TerminalStartRequest{
		Workspace: workspace,
		Command:   "sh",
		Args:      []string{"-c", "printf hello; printf err 1>&2"},
	})
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d, body=%s", createRec.Code, http.StatusCreated, createRec.Body.String())
	}
	created := decodeRecorder[TerminalResponse](t, createRec)
	if created.Object != "terminal" || created.Data.ID == "" || created.Data.Workspace == "" {
		t.Fatalf("create response = %+v, want terminal with id/workspace", created)
	}

	waitReq := newTerminalRequest(t, http.MethodPost, "/hecate/v1/terminals/"+created.Data.ID+"/wait", nil)
	waitRec := httptest.NewRecorder()
	handler.ServeHTTP(waitRec, waitReq)
	if waitRec.Code != http.StatusOK {
		t.Fatalf("wait status = %d, want %d, body=%s", waitRec.Code, http.StatusOK, waitRec.Body.String())
	}
	wait := decodeRecorder[TerminalResponse](t, waitRec)
	if wait.Data.Running {
		t.Fatal("wait running = true, want false")
	}
	if wait.Data.ExitCode == nil || *wait.Data.ExitCode != 0 {
		t.Fatalf("wait exit code = %v, want 0", wait.Data.ExitCode)
	}
	if !strings.Contains(wait.Data.Output, "hello") || !strings.Contains(wait.Data.Output, "err") {
		t.Fatalf("wait output = %q, want stdout and stderr", wait.Data.Output)
	}

	releaseReq := newTerminalRequest(t, http.MethodDelete, "/hecate/v1/terminals/"+created.Data.ID, nil)
	releaseRec := httptest.NewRecorder()
	handler.ServeHTTP(releaseRec, releaseReq)
	if releaseRec.Code != http.StatusNoContent {
		t.Fatalf("release status = %d, want %d, body=%s", releaseRec.Code, http.StatusNoContent, releaseRec.Body.String())
	}

	outputReq := newTerminalRequest(t, http.MethodGet, "/hecate/v1/terminals/"+created.Data.ID+"/output", nil)
	outputRec := httptest.NewRecorder()
	handler.ServeHTTP(outputRec, outputReq)
	if outputRec.Code != http.StatusNotFound {
		t.Fatalf("output after release status = %d, want %d, body=%s", outputRec.Code, http.StatusNotFound, outputRec.Body.String())
	}
}

func TestOperatorTerminalRejectsWorkspaceEscape(t *testing.T) {
	t.Parallel()

	handler := newTerminalTestHandler()
	workspace := t.TempDir()
	req := newTerminalRequest(t, http.MethodPost, "/hecate/v1/terminals", TerminalStartRequest{
		Workspace:        workspace,
		WorkingDirectory: filepath.Dir(workspace),
		Command:          "true",
	})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "escapes allowed root") {
		t.Fatalf("body = %s, want workspace escape message", rec.Body.String())
	}
}

func newTerminalTestHandler() http.Handler {
	return newTestHTTPHandlerWithConfig(slog.New(slog.NewJSONHandler(io.Discard, nil)), &fakeProvider{name: "openai"}, config.Config{
		Server: config.ServerConfig{OperatorTerminals: true},
	})
}

func newTerminalRequest(t *testing.T, method, path string, body any) *http.Request {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	req.RemoteAddr = "127.0.0.1:4321"
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}
