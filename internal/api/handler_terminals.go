package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/hecatehq/hecate/internal/terminalapp"
)

func (h *Handler) HandleCreateTerminal(w http.ResponseWriter, r *http.Request) {
	if !operatorTerminalLocalRequestAllowed(w, r) {
		return
	}
	var req TerminalStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "invalid JSON body")
		return
	}
	app, ok := h.operatorTerminalApp(w)
	if !ok {
		return
	}
	snap, err := app.Start(r.Context(), terminalapp.StartCommand{
		Workspace:        req.Workspace,
		WorkingDirectory: req.WorkingDirectory,
		Command:          req.Command,
		Args:             req.Args,
		Env:              req.Env,
		OutputByteLimit:  req.OutputByteLimit,
	})
	if err != nil {
		writeTerminalError(w, err)
		return
	}
	writeTerminalResponse(w, http.StatusCreated, snap)
}

func (h *Handler) HandleTerminalOutput(w http.ResponseWriter, r *http.Request) {
	if !operatorTerminalLocalRequestAllowed(w, r) {
		return
	}
	app, ok := h.operatorTerminalApp(w)
	if !ok {
		return
	}
	snap, err := app.Output(r.PathValue("terminal_id"))
	if err != nil {
		writeTerminalError(w, err)
		return
	}
	writeTerminalResponse(w, http.StatusOK, snap)
}

func (h *Handler) HandleWriteTerminalInput(w http.ResponseWriter, r *http.Request) {
	if !operatorTerminalLocalRequestAllowed(w, r) {
		return
	}
	var req TerminalInputRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "invalid JSON body")
		return
	}
	app, ok := h.operatorTerminalApp(w)
	if !ok {
		return
	}
	snap, err := app.Write(r.Context(), r.PathValue("terminal_id"), req.Input)
	if err != nil {
		writeTerminalError(w, err)
		return
	}
	writeTerminalResponse(w, http.StatusOK, snap)
}

func (h *Handler) HandleWaitTerminal(w http.ResponseWriter, r *http.Request) {
	if !operatorTerminalLocalRequestAllowed(w, r) {
		return
	}
	app, ok := h.operatorTerminalApp(w)
	if !ok {
		return
	}
	snap, err := app.Wait(r.Context(), r.PathValue("terminal_id"))
	if err != nil {
		writeTerminalError(w, err)
		return
	}
	writeTerminalResponse(w, http.StatusOK, snap)
}

func (h *Handler) HandleKillTerminal(w http.ResponseWriter, r *http.Request) {
	if !operatorTerminalLocalRequestAllowed(w, r) {
		return
	}
	app, ok := h.operatorTerminalApp(w)
	if !ok {
		return
	}
	snap, err := app.Kill(r.Context(), r.PathValue("terminal_id"))
	if err != nil {
		writeTerminalError(w, err)
		return
	}
	writeTerminalResponse(w, http.StatusOK, snap)
}

func (h *Handler) HandleReleaseTerminal(w http.ResponseWriter, r *http.Request) {
	if !operatorTerminalLocalRequestAllowed(w, r) {
		return
	}
	app, ok := h.operatorTerminalApp(w)
	if !ok {
		return
	}
	if err := app.Release(r.Context(), r.PathValue("terminal_id")); err != nil {
		writeTerminalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) operatorTerminalApp(w http.ResponseWriter) (*terminalapp.Application, bool) {
	if h == nil || h.operatorTerminals == nil {
		WriteError(w, http.StatusForbidden, errCodeForbidden, "operator terminals are disabled")
		return nil, false
	}
	return h.operatorTerminals, true
}

func operatorTerminalLocalRequestAllowed(w http.ResponseWriter, r *http.Request) bool {
	if !isLoopbackRemoteAddr(r.RemoteAddr) {
		WriteError(w, http.StatusForbidden, errCodeInvalidRequest, "operator terminals are only available to local loopback clients")
		return false
	}
	if hasForwardedClientHeaders(r) {
		WriteError(w, http.StatusForbidden, errCodeInvalidRequest, "operator terminals reject forwarded client headers")
		return false
	}
	return true
}

func writeTerminalError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, terminalapp.ErrDisabled):
		WriteError(w, http.StatusForbidden, errCodeForbidden, "operator terminals are disabled")
	case errors.Is(err, terminalapp.ErrValidation):
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
	case errors.Is(err, terminalapp.ErrNotFound):
		WriteError(w, http.StatusNotFound, errCodeNotFound, "terminal not found")
	case errors.Is(err, terminalapp.ErrWorkspaceBusy):
		writeWorkspaceMutationConflict(w)
	case errors.Is(err, terminalapp.ErrShuttingDown):
		WriteErrorDetails(w, http.StatusConflict, errCodeConflict, "operator terminals are shutting down", ErrorDetails{
			UserMessage:    "The local runtime is shutting down.",
			OperatorAction: "Wait for the runtime to restart before opening another terminal.",
		})
	default:
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
	}
}

func writeTerminalResponse(w http.ResponseWriter, status int, snap terminalapp.Snapshot) {
	WriteJSON(w, status, TerminalResponse{
		Object: "terminal",
		Data: TerminalResponseItem{
			ID:               snap.ID,
			Workspace:        snap.Workspace,
			WorkingDirectory: snap.WorkingDirectory,
			Command:          snap.Command,
			Args:             snap.Args,
			Output:           snap.Output,
			Truncated:        snap.Truncated,
			Running:          snap.Running,
			ExitCode:         snap.ExitCode,
			Error:            snap.Error,
			CreatedAt:        snap.CreatedAt,
			UpdatedAt:        snap.UpdatedAt,
		},
	})
}
