package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/internal/terminal"
)

func TestTerminalRejectsNonLoopbackClient(t *testing.T) {
	t.Parallel()

	handler := newTestHTTPHandlerWithConfig(slog.New(slog.NewJSONHandler(io.Discard, nil)), &fakeProvider{name: "openai"}, config.Config{})
	req := httptest.NewRequest(http.MethodGet, "/hecate/v1/terminal?workspace=/tmp", nil)
	req.RemoteAddr = "203.0.113.12:4321"
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d, body=%s", recorder.Code, http.StatusForbidden, recorder.Body.String())
	}
}

func TestTerminalRejectsForwardedClientHeaders(t *testing.T) {
	t.Parallel()

	handler := newTestHTTPHandlerWithConfig(slog.New(slog.NewJSONHandler(io.Discard, nil)), &fakeProvider{name: "openai"}, config.Config{})
	for _, header := range []string{"X-Forwarded-For", "X-Real-IP"} {
		req := httptest.NewRequest(http.MethodGet, "/hecate/v1/terminal?workspace=/tmp", nil)
		req.RemoteAddr = "127.0.0.1:4321"
		req.Header.Set(header, "203.0.113.12")
		recorder := httptest.NewRecorder()

		handler.ServeHTTP(recorder, req)

		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s status = %d, want %d, body=%s", header, recorder.Code, http.StatusForbidden, recorder.Body.String())
		}
	}
}

func TestTerminalRejectsInvalidWorkspaceBeforeUpgrade(t *testing.T) {
	t.Parallel()

	handler := newTestHTTPHandlerWithConfig(slog.New(slog.NewJSONHandler(io.Discard, nil)), &fakeProvider{name: "openai"}, config.Config{})
	req := httptest.NewRequest(http.MethodGet, "/hecate/v1/terminal", nil)
	req.RemoteAddr = "127.0.0.1:4321"
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
}

func TestTerminalWebSocketBridgesSession(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{name: "openai"}}, config.Config{}, nil)
	session := newFakeTerminalSession()
	launcher := &fakeTerminalLauncher{session: session}
	apiHandler.SetTerminalLauncher(launcher)
	server := httptest.NewServer(NewServer(logger, apiHandler))
	t.Cleanup(server.Close)

	workspace := t.TempDir()
	wsURL := "ws" + server.URL[len("http"):] + "/hecate/v1/terminal?workspace=" + url.QueryEscape(workspace) + "&cols=100&rows=30"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("Dial terminal: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close(websocket.StatusNormalClosure, "")
	})

	wantWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatalf("canonicalize temp workspace: %v", err)
	}
	launcher.mu.Lock()
	gotReq := launcher.req
	launcher.mu.Unlock()
	if gotReq.Workspace != wantWorkspace || gotReq.Cols != 100 || gotReq.Rows != 30 {
		t.Fatalf("terminal start request = %+v, want workspace=%q cols=100 rows=30", gotReq, wantWorkspace)
	}

	if err := wsjson.Write(ctx, conn, terminalClientMessage{Type: "input", Data: "echo hi\r"}); err != nil {
		t.Fatalf("write input message: %v", err)
	}
	if got := receiveTerminalTestValue(t, session.writes); got != "echo hi\r" {
		t.Fatalf("terminal write = %q, want echo command", got)
	}

	if err := wsjson.Write(ctx, conn, terminalClientMessage{Type: "resize", Cols: 120, Rows: 40}); err != nil {
		t.Fatalf("write resize message: %v", err)
	}
	if got := receiveTerminalTestValue(t, session.resizes); got != [2]int{120, 40} {
		t.Fatalf("terminal resize = %v, want 120x40", got)
	}

	session.outputs <- []byte("hello from pty\r\n")
	var output terminalServerMessage
	if err := wsjson.Read(ctx, conn, &output); err != nil {
		t.Fatalf("read output message: %v", err)
	}
	if output.Type != "output" || output.Data != "hello from pty\r\n" {
		t.Fatalf("output message = %+v, want terminal output", output)
	}

	session.wait <- nil
	var exit terminalServerMessage
	if err := wsjson.Read(ctx, conn, &exit); err != nil {
		t.Fatalf("read exit message: %v", err)
	}
	if exit.Type != "exit" || exit.Code != 0 {
		t.Fatalf("exit message = %+v, want zero exit", exit)
	}
}

type fakeTerminalLauncher struct {
	mu      sync.Mutex
	req     terminal.StartRequest
	session *fakeTerminalSession
}

func (l *fakeTerminalLauncher) Start(_ context.Context, req terminal.StartRequest) (terminal.Session, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.req = req
	return l.session, nil
}

type fakeTerminalSession struct {
	outputs chan []byte
	writes  chan string
	resizes chan [2]int
	wait    chan error
	closed  chan struct{}
	once    sync.Once
}

func newFakeTerminalSession() *fakeTerminalSession {
	return &fakeTerminalSession{
		outputs: make(chan []byte, 1),
		writes:  make(chan string, 1),
		resizes: make(chan [2]int, 1),
		wait:    make(chan error, 1),
		closed:  make(chan struct{}),
	}
}

func (s *fakeTerminalSession) Read(p []byte) (int, error) {
	select {
	case output := <-s.outputs:
		return copy(p, output), nil
	case <-s.closed:
		return 0, io.EOF
	}
}

func (s *fakeTerminalSession) Write(p []byte) (int, error) {
	select {
	case s.writes <- string(p):
		return len(p), nil
	case <-s.closed:
		return 0, errors.New("terminal closed")
	}
}

func (s *fakeTerminalSession) Close() error {
	s.once.Do(func() {
		close(s.closed)
	})
	return nil
}

func (s *fakeTerminalSession) Resize(cols, rows int) error {
	select {
	case s.resizes <- [2]int{cols, rows}:
		return nil
	case <-s.closed:
		return errors.New("terminal closed")
	}
}

func (s *fakeTerminalSession) Wait() error {
	select {
	case err := <-s.wait:
		return err
	case <-s.closed:
		return nil
	}
}

func receiveTerminalTestValue[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(2 * time.Second):
		var zero T
		t.Fatalf("timed out waiting for terminal test value")
		return zero
	}
}
