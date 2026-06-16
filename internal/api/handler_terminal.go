package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/hecatehq/hecate/internal/terminal"
)

type terminalClientMessage struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

type terminalServerMessage struct {
	Type    string `json:"type"`
	Data    string `json:"data,omitempty"`
	Message string `json:"message,omitempty"`
	Code    int    `json:"code,omitempty"`
}

const terminalTicketTTL = 30 * time.Second

type terminalTicket struct {
	Workspace string
	ExpiresAt time.Time
}

type createTerminalSessionRequest struct {
	Workspace string `json:"workspace"`
}

type terminalSessionResponse struct {
	Object string              `json:"object"`
	Data   terminalSessionData `json:"data"`
}

type terminalSessionData struct {
	Token     string    `json:"token"`
	Workspace string    `json:"workspace"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (h *Handler) HandleCreateTerminalSession(w http.ResponseWriter, r *http.Request) {
	if !h.requireTerminalEnabled(w) {
		return
	}
	// The embedded terminal is an operator-only UI surface. Agents must use
	// governed task/runtime tools instead of receiving or reusing these tickets.
	if !h.requireTerminalOperatorAccess(w, r) {
		return
	}
	var req createTerminalSessionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	workspace, err := validateTerminalWorkspace(req.Workspace)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	token, err := newTerminalTicketToken()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeInternalError, "failed to create terminal session")
		return
	}
	expiresAt := time.Now().UTC().Add(terminalTicketTTL)
	h.storeTerminalTicket(token, terminalTicket{Workspace: workspace, ExpiresAt: expiresAt})
	WriteJSON(w, http.StatusOK, terminalSessionResponse{
		Object: "terminal_session",
		Data: terminalSessionData{
			Token:     token,
			Workspace: workspace,
			ExpiresAt: expiresAt,
		},
	})
}

func (h *Handler) HandleTerminal(w http.ResponseWriter, r *http.Request) {
	if !h.requireTerminalEnabled(w) {
		return
	}
	if !h.requireTerminalOperatorAccess(w, r) {
		return
	}
	workspace, err := validateTerminalWorkspace(r.URL.Query().Get("workspace"))
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	if err := h.consumeTerminalTicket(r.URL.Query().Get("token"), workspace, time.Now().UTC()); err != nil {
		WriteError(w, http.StatusUnauthorized, errCodeUnauthorized, err.Error())
		return
	}
	cols := positiveQueryInt(r, "cols", 80)
	rows := positiveQueryInt(r, "rows", 24)

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// The protected POST /terminal/sessions route mints a short-lived,
		// one-use ticket. The WebSocket upgrade consumes that ticket and still
		// enforces browser Origin checks, including configured UI origins when
		// Hecate runs hosted.
		OriginPatterns: h.terminalOriginPatterns(r),
	})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	session, err := h.getTerminalLauncher().Start(ctx, terminal.StartRequest{
		Workspace: workspace,
		Cols:      cols,
		Rows:      rows,
	})
	if err != nil {
		_ = wsjson.Write(ctx, conn, terminalServerMessage{
			Type:    "error",
			Message: terminalStartErrorMessage(err),
		})
		return
	}
	defer session.Close()

	h.bridgeTerminal(ctx, conn, session)
}

func (h *Handler) requireTerminalEnabled(w http.ResponseWriter) bool {
	if h.config.EmbeddedTerminalEnabled() {
		return true
	}
	WriteError(w, http.StatusNotFound, errCodeNotFound, "embedded terminal is disabled")
	return false
}

func (h *Handler) requireTerminalOperatorAccess(w http.ResponseWriter, r *http.Request) bool {
	if h.config.Server.RemoteRuntimeMode {
		return true
	}
	return requireLoopbackClient(w, r, "embedded terminal")
}

func newTerminalTicketToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func (h *Handler) storeTerminalTicket(token string, ticket terminalTicket) {
	h.terminalTicketsMu.Lock()
	defer h.terminalTicketsMu.Unlock()
	if h.terminalTickets == nil {
		h.terminalTickets = make(map[string]terminalTicket)
	}
	h.pruneExpiredTerminalTicketsLocked(time.Now().UTC())
	h.terminalTickets[token] = ticket
}

func (h *Handler) consumeTerminalTicket(token, workspace string, now time.Time) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("terminal session token is required")
	}
	h.terminalTicketsMu.Lock()
	defer h.terminalTicketsMu.Unlock()
	h.pruneExpiredTerminalTicketsLocked(now)
	ticket, ok := h.terminalTickets[token]
	delete(h.terminalTickets, token)
	if !ok || now.After(ticket.ExpiresAt) {
		return errors.New("terminal session token is invalid or expired")
	}
	if ticket.Workspace != workspace {
		return errors.New("terminal session token does not match workspace")
	}
	return nil
}

func (h *Handler) pruneExpiredTerminalTicketsLocked(now time.Time) {
	for token, ticket := range h.terminalTickets {
		if !ticket.ExpiresAt.After(now) {
			delete(h.terminalTickets, token)
		}
	}
}

func validateTerminalWorkspace(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve default workspace: %w", err)
		}
		path = cwd
	}
	return canonicalWorkspaceDialogPath(path)
}

func (h *Handler) terminalOriginPatterns(r *http.Request) []string {
	seen := make(map[string]struct{})
	patterns := make([]string, 0, 4+len(h.config.Server.AllowedOrigins))
	add := func(pattern string) {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			return
		}
		key := strings.ToLower(pattern)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		patterns = append(patterns, pattern)
	}

	// Local desktop/Vite/browser surfaces can run on different loopback ports.
	add("localhost:*")
	add("127.0.0.1:*")
	add("[::1]:*")
	add(r.Host)
	for _, origin := range h.config.Server.AllowedOrigins {
		parsed, err := url.Parse(strings.TrimSpace(origin))
		if err != nil || parsed.Host == "" {
			continue
		}
		add(parsed.Host)
	}
	return patterns
}

func positiveQueryInt(r *http.Request, key string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get(key)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func (h *Handler) bridgeTerminal(ctx context.Context, conn *websocket.Conn, session terminal.Session) {
	var writeMu sync.Mutex
	writeJSON := func(msg terminalServerMessage) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return wsjson.Write(ctx, conn, msg)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 8192)
		for {
			n, err := session.Read(buf)
			if n > 0 {
				if writeJSON(terminalServerMessage{Type: "output", Data: string(buf[:n])}) != nil {
					return
				}
			}
			if err != nil {
				if !errors.Is(err, io.EOF) && ctx.Err() == nil {
					_ = writeJSON(terminalServerMessage{Type: "error", Message: err.Error()})
				}
				return
			}
		}
	}()

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- session.Wait()
	}()

	clientMessages := make(chan terminalClientMessage)
	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		defer close(clientMessages)
		for {
			var msg terminalClientMessage
			if err := wsjson.Read(ctx, conn, &msg); err != nil {
				return
			}
			select {
			case clientMessages <- msg:
			case <-ctx.Done():
				return
			}
		}
	}()

	for {
		select {
		case err := <-waitDone:
			_ = writeJSON(terminalServerMessage{Type: "exit", Code: terminalExitCode(err)})
			return
		case <-done:
			return
		case <-clientDone:
			return
		case msg, ok := <-clientMessages:
			if !ok {
				return
			}
			switch msg.Type {
			case "input":
				if msg.Data != "" {
					_, _ = session.Write([]byte(msg.Data))
				}
			case "resize":
				if msg.Cols <= 0 || msg.Rows <= 0 {
					continue
				}
				if err := session.Resize(msg.Cols, msg.Rows); err != nil {
					_ = writeJSON(terminalServerMessage{Type: "error", Message: err.Error()})
				}
			case "close":
				return
			}
		}
	}
}

func terminalExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func terminalStartErrorMessage(err error) string {
	if terminal.DeviceNotConfigured(err) {
		return "failed to start terminal: macOS could not allocate a pseudo-terminal " +
			"(device not configured). Restart Hecate and try again; if this persists in a " +
			"packaged build, use a build that allows PTY access."
	}
	return fmt.Sprintf("failed to start terminal: %v", err)
}
