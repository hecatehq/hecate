package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/hecatehq/hecate/internal/taskapp"
	"github.com/hecatehq/hecate/internal/taskstate"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

func (h *Handler) HandleSteerTaskRun(w http.ResponseWriter, r *http.Request) {
	ctx, span := otel.Tracer("hecate/api").Start(r.Context(), "task.run.steer")
	defer span.End()
	task, ok := h.loadAuthorizedTask(ctx, w, r)
	if !ok {
		return
	}
	run, ok := h.loadAuthorizedTaskRun(ctx, w, r, task)
	if !ok {
		return
	}
	var req struct {
		ClientRequestID string `json:"client_request_id"`
		Content         string `json:"content"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "Expected one guidance object with client_request_id and content.")
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "Expected one guidance object.")
		return
	}
	result, err := h.taskApplication().SteerRun(ctx, task, taskapp.SteerRunCommand{RunID: run.ID,
		ClientRequestID: req.ClientRequestID, Content: req.Content, RequestID: RequestIDFromContext(ctx),
		TraceID: trace.SpanContextFromContext(ctx).TraceID().String()})
	if err != nil {
		switch {
		case errors.Is(err, taskstate.ErrSteeringUnavailable):
			WriteError(w, http.StatusConflict, "task.steering_unavailable", err.Error())
		case errors.Is(err, taskstate.ErrSteeringRequestConflict):
			WriteError(w, http.StatusConflict, "task.steering_request_conflict", err.Error())
		case errors.Is(err, taskstate.ErrSteeringFull):
			WriteError(w, http.StatusTooManyRequests, "task.steering_full", err.Error())
		default:
			if !writeTaskAppError(w, err) {
				WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "Could not save guidance. Retry with the same request id to check its status.")
			}
		}
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	WriteJSON(w, http.StatusOK, struct {
		Object string                      `json:"object"`
		Data   taskstate.RunSteeringResult `json:"data"`
	}{"task.run.steering", result})
}
