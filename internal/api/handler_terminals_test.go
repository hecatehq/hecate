package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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

func TestOperatorTerminalRejectsUnixProcessGroupEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix process-group policy")
	}
	t.Parallel()

	handler := newTerminalTestHandler()
	workspace := t.TempDir()
	marker := filepath.Join(workspace, "spawned")
	req := newTerminalRequest(t, http.MethodPost, "/hecate/v1/terminals", TerminalStartRequest{
		Workspace: workspace,
		Command:   "sh",
		Args:      []string{"-c", "printf spawned > " + marker + "; setsid sleep 60"},
	})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "process group") {
		t.Fatalf("body = %s, want process-group explanation", rec.Body.String())
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marker stat error = %v, want command not spawned", err)
	}
}

func TestOperatorTerminalInputRejectsUnixProcessGroupEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix process-group policy")
	}
	t.Parallel()

	handler := newTerminalTestHandler()
	workspace := t.TempDir()
	marker := filepath.Join(workspace, "escaped")
	createReq := newTerminalRequest(t, http.MethodPost, "/hecate/v1/terminals", TerminalStartRequest{
		Workspace: workspace,
		Command:   "sh",
	})
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d, body=%s", createRec.Code, http.StatusCreated, createRec.Body.String())
	}
	created := decodeRecorder[TerminalResponse](t, createRec)
	t.Cleanup(func() {
		releaseReq := newTerminalRequest(t, http.MethodDelete, "/hecate/v1/terminals/"+created.Data.ID, nil)
		handler.ServeHTTP(httptest.NewRecorder(), releaseReq)
	})

	inputReq := newTerminalRequest(t, http.MethodPost, "/hecate/v1/terminals/"+created.Data.ID+"/input", TerminalInputRequest{
		Input: "setsid sh -c 'printf escaped > " + marker + "'\n",
	})
	inputRec := httptest.NewRecorder()
	handler.ServeHTTP(inputRec, inputReq)
	if inputRec.Code != http.StatusBadRequest {
		t.Fatalf("input status = %d, want %d, body=%s", inputRec.Code, http.StatusBadRequest, inputRec.Body.String())
	}
	if !strings.Contains(inputRec.Body.String(), "process group") {
		t.Fatalf("input body = %s, want process-group explanation", inputRec.Body.String())
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marker stat error = %v, want rejected input not executed", err)
	}
}

func TestOperatorTerminalStartConflictsWithOverlappingWorkspaceClosure(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := NewHandler(config.Config{
		Server: config.ServerConfig{OperatorTerminals: true},
	}, logger, nil, nil, nil, nil)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = apiHandler.Shutdown(ctx)
	})
	handler := NewServer(logger, apiHandler)
	root := t.TempDir()
	workspacePath := filepath.Join(root, "nested")
	if err := os.Mkdir(workspacePath, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	closure, err := apiHandler.workspaceCoordinator.TryClose(t.Context(), root)
	if err != nil {
		t.Fatalf("TryClose(parent): %v", err)
	}
	defer closure.Release()

	req := newTerminalRequest(t, http.MethodPost, "/hecate/v1/terminals", TerminalStartRequest{
		Workspace: workspacePath,
		Command:   "true",
	})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	var response struct {
		Error struct {
			Type           string `json:"type"`
			Message        string `json:"message"`
			UserMessage    string `json:"user_message"`
			OperatorAction string `json:"operator_action"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Error.Type != errCodeConflict || response.Error.Message != "workspace is temporarily closed for a destructive operation" {
		t.Fatalf("error contract = %#v", response.Error)
	}
	if response.Error.UserMessage != "Another workspace operation is finishing." || response.Error.OperatorAction != "Wait for it to finish, refresh the workspace state, and try again." {
		t.Fatalf("operator metadata = %#v", response.Error)
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
