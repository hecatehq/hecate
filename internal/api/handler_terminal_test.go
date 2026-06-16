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
	"net/url"
	"os"
	"path/filepath"
	"strings"
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

	handler := newTestHTTPHandler(slog.New(slog.NewJSONHandler(io.Discard, nil)), &fakeProvider{name: "openai"})
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

	handler := newTestHTTPHandler(slog.New(slog.NewJSONHandler(io.Discard, nil)), &fakeProvider{name: "openai"})
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

	handler := newTestHTTPHandler(slog.New(slog.NewJSONHandler(io.Discard, nil)), &fakeProvider{name: "openai"})
	req := httptest.NewRequest(http.MethodGet, "/hecate/v1/terminal?workspace=%00", nil)
	req.RemoteAddr = "127.0.0.1:4321"
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
}

func TestTerminalDisabledReturnsNotFound(t *testing.T) {
	t.Parallel()

	apiHandler := newTestAPIHandlerWithSettings(slog.New(slog.NewJSONHandler(io.Discard, nil)), []providers.Provider{&fakeProvider{name: "openai"}}, config.Config{
		Server: config.ServerConfig{EmbeddedTerminalDisabled: true},
	}, nil)
	req := httptest.NewRequest(http.MethodGet, "/hecate/v1/terminal?workspace=/tmp", nil)
	req.RemoteAddr = "127.0.0.1:4321"
	recorder := httptest.NewRecorder()

	apiHandler.HandleTerminal(recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body=%s", recorder.Code, http.StatusNotFound, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "embedded terminal is disabled") {
		t.Fatalf("body = %q, want disabled terminal message", recorder.Body.String())
	}
}

func TestCreateTerminalSessionDisabledReturnsNotFound(t *testing.T) {
	t.Parallel()

	apiHandler := newTestAPIHandlerWithSettings(slog.New(slog.NewJSONHandler(io.Discard, nil)), []providers.Provider{&fakeProvider{name: "openai"}}, config.Config{
		Server: config.ServerConfig{EmbeddedTerminalDisabled: true},
	}, nil)
	body, err := json.Marshal(createTerminalSessionRequest{Workspace: t.TempDir()})
	if err != nil {
		t.Fatalf("marshal terminal session request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/hecate/v1/terminal/sessions", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:4321"
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	apiHandler.HandleCreateTerminalSession(recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body=%s", recorder.Code, http.StatusNotFound, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "embedded terminal is disabled") {
		t.Fatalf("body = %q, want disabled terminal message", recorder.Body.String())
	}
}

func TestCreateTerminalSessionDefaultsToRuntimeWorkspace(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{name: "openai"}}, config.Config{}, nil)
	server := httptest.NewServer(NewServer(logger, apiHandler))
	t.Cleanup(server.Close)

	session := createTerminalSessionForTest(t, server, "")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	wantWorkspace, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatalf("canonicalize cwd: %v", err)
	}
	if session.Data.Workspace != wantWorkspace {
		t.Fatalf("workspace = %q, want runtime cwd %q", session.Data.Workspace, wantWorkspace)
	}
}

func TestCreateTerminalSessionRejectsCrossOriginBrowserRequest(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{name: "openai"}}, config.Config{}, nil)
	server := httptest.NewServer(NewServer(logger, apiHandler))
	t.Cleanup(server.Close)

	body, err := json.Marshal(createTerminalSessionRequest{Workspace: t.TempDir()})
	if err != nil {
		t.Fatalf("marshal terminal session request: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, server.URL+"/hecate/v1/terminal/sessions", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create terminal session request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://example.invalid")

	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("create terminal session: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want %d, body=%s", resp.StatusCode, http.StatusForbidden, payload)
	}
}

func TestCreateTerminalSessionAllowsRemoteRuntimeClient(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	apiHandler := newTestAPIHandlerWithSettings(slog.New(slog.NewJSONHandler(io.Discard, nil)), []providers.Provider{&fakeProvider{name: "openai"}}, config.Config{
		Server: config.ServerConfig{RemoteRuntimeMode: true},
	}, nil)
	body, err := json.Marshal(createTerminalSessionRequest{Workspace: workspace})
	if err != nil {
		t.Fatalf("marshal terminal session request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/hecate/v1/terminal/sessions", bytes.NewReader(body))
	req.RemoteAddr = "203.0.113.12:4321"
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	apiHandler.HandleCreateTerminalSession(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
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
	ticket := createTerminalSessionForTest(t, server, workspace)
	wsURL := "ws" + server.URL[len("http"):] + "/hecate/v1/terminal?workspace=" + url.QueryEscape(ticket.Data.Workspace) + "&token=" + url.QueryEscape(ticket.Data.Token) + "&cols=100&rows=30"
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

func TestTerminalRejectsMissingTicket(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	handler := newTestHTTPHandler(slog.New(slog.NewJSONHandler(io.Discard, nil)), &fakeProvider{name: "openai"})
	req := httptest.NewRequest(http.MethodGet, "/hecate/v1/terminal?workspace="+url.QueryEscape(workspace), nil)
	req.RemoteAddr = "127.0.0.1:4321"
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d, body=%s", recorder.Code, http.StatusUnauthorized, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "terminal session token is required") {
		t.Fatalf("body = %q, want missing token message", recorder.Body.String())
	}
}

func TestCreateTerminalSessionReturnsWorkspaceBoundTicket(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{name: "openai"}}, config.Config{}, nil)
	server := httptest.NewServer(NewServer(logger, apiHandler))
	t.Cleanup(server.Close)

	workspace := t.TempDir()
	session := createTerminalSessionForTest(t, server, workspace)
	wantWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatalf("canonicalize temp workspace: %v", err)
	}
	if session.Object != "terminal_session" {
		t.Fatalf("object = %q, want terminal_session", session.Object)
	}
	if session.Data.Token == "" {
		t.Fatal("terminal session token is empty")
	}
	if session.Data.Workspace != wantWorkspace {
		t.Fatalf("workspace = %q, want %q", session.Data.Workspace, wantWorkspace)
	}
	if !session.Data.ExpiresAt.After(time.Now().UTC()) {
		t.Fatalf("expires_at = %s, want future timestamp", session.Data.ExpiresAt)
	}
}

func TestTerminalTicketIsWorkspaceBoundAndOneUse(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	otherWorkspace := t.TempDir()
	handler := newTestAPIHandlerWithSettings(slog.New(slog.NewJSONHandler(io.Discard, nil)), []providers.Provider{&fakeProvider{name: "openai"}}, config.Config{}, nil)
	canonicalWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatalf("canonicalize workspace: %v", err)
	}
	canonicalOther, err := filepath.EvalSymlinks(otherWorkspace)
	if err != nil {
		t.Fatalf("canonicalize other workspace: %v", err)
	}

	handler.storeTerminalTicket("mismatch", terminalTicket{
		Workspace: canonicalWorkspace,
		ExpiresAt: time.Now().UTC().Add(time.Minute),
	})
	if err := handler.consumeTerminalTicket("mismatch", canonicalOther, time.Now().UTC()); err == nil {
		t.Fatal("consume mismatched terminal ticket returned nil error")
	}
	if err := handler.consumeTerminalTicket("mismatch", canonicalWorkspace, time.Now().UTC()); err == nil {
		t.Fatal("mismatched ticket was not consumed")
	}

	handler.storeTerminalTicket("one-use", terminalTicket{
		Workspace: canonicalWorkspace,
		ExpiresAt: time.Now().UTC().Add(time.Minute),
	})
	if err := handler.consumeTerminalTicket("one-use", canonicalWorkspace, time.Now().UTC()); err != nil {
		t.Fatalf("consume valid terminal ticket: %v", err)
	}
	if err := handler.consumeTerminalTicket("one-use", canonicalWorkspace, time.Now().UTC()); err == nil {
		t.Fatal("second terminal ticket consume returned nil error")
	}
}

func TestTerminalTicketStorePrunesExpiredTickets(t *testing.T) {
	t.Parallel()

	handler := newTestAPIHandlerWithSettings(slog.New(slog.NewJSONHandler(io.Discard, nil)), []providers.Provider{&fakeProvider{name: "openai"}}, config.Config{}, nil)
	now := time.Now().UTC()
	handler.storeTerminalTicket("expired", terminalTicket{
		Workspace: t.TempDir(),
		ExpiresAt: now.Add(-time.Minute),
	})
	handler.storeTerminalTicket("fresh", terminalTicket{
		Workspace: t.TempDir(),
		ExpiresAt: now.Add(time.Minute),
	})

	handler.terminalTicketsMu.Lock()
	defer handler.terminalTicketsMu.Unlock()
	if _, ok := handler.terminalTickets["expired"]; ok {
		t.Fatal("expired terminal ticket was not pruned")
	}
	if _, ok := handler.terminalTickets["fresh"]; !ok {
		t.Fatal("fresh terminal ticket was pruned")
	}
}

func createTerminalSessionForTest(t *testing.T, server *httptest.Server, workspace string) terminalSessionResponse {
	t.Helper()
	body, err := json.Marshal(createTerminalSessionRequest{Workspace: workspace})
	if err != nil {
		t.Fatalf("marshal terminal session request: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, server.URL+"/hecate/v1/terminal/sessions", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create terminal session request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("create terminal session: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("create terminal session status = %d body=%s, want 200", resp.StatusCode, payload)
	}
	var session terminalSessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		t.Fatalf("decode terminal session response: %v", err)
	}
	return session
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
