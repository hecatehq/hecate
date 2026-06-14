package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
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
	// The embedded terminal is an operator-only UI surface. Agents must use
	// governed task/runtime tools instead of receiving or reusing these tickets.
	if !requireLoopbackClient(w, r, "embedded terminal") {
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
	if !requireLoopbackClient(w, r, "embedded terminal") {
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
		// The desktop gateway, Vite dev UI, and local browser UI can run on
		// different loopback ports. The terminal is still gated by the
		// loopback socket check above and rejects forwarded-client headers.
		OriginPatterns: []string{
			"localhost:*",
			"127.0.0.1:*",
			"[::1]:*",
		},
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
			Message: fmt.Sprintf("failed to start terminal: %v", err),
		})
		return
	}
	defer session.Close()

	h.bridgeTerminal(ctx, conn, session)
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
	h.terminalTickets[token] = ticket
}

func (h *Handler) consumeTerminalTicket(token, workspace string, now time.Time) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("terminal session token is required")
	}
	h.terminalTicketsMu.Lock()
	defer h.terminalTicketsMu.Unlock()
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

func validateTerminalWorkspace(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("workspace path is required")
	}
	return canonicalWorkspaceDialogPath(path)
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
