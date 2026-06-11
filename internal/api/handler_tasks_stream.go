package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/eventprotocol"
	"github.com/hecatehq/hecate/internal/runtimeevents"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

// runStreamSubscriber is the optional store capability that lets the
// run stream wake on mutations instead of polling. Both production
// stores (memory, sqlite) implement it; type-asserting here keeps the
// taskstate.Store interface free of this streaming concern and lets a
// bare test fake fall back to the short poll below.
type runStreamSubscriber interface {
	SubscribeRun(runID string) (<-chan struct{}, func())
}

func (h *Handler) HandleTaskRunStream(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if h.taskStore == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task store is not configured")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "streaming not supported by server")
		return
	}
	task, ok := h.loadAuthorizedTask(ctx, w, r)
	if !ok {
		return
	}
	run, ok := h.loadAuthorizedTaskRun(ctx, w, r, task)
	if !ok {
		return
	}
	runID := run.ID

	writeSSEHeaders(w)
	projector := newTaskRunStreamProjector(h.taskStore)
	stream := newTaskRunStreamWriter(w, flusher)

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	// Wake on store mutations rather than busy-polling. Subscribing
	// before the first read closes the lost-wakeup gap: any change
	// between a read and the select below leaves a pending signal on
	// the buffered channel, so the next select returns immediately. The
	// store stays the source of truth — every wake triggers a full
	// re-read, so a coalesced or duplicate signal is harmless. The 15s
	// heartbeat doubles as a safety re-poll. A store without the
	// capability (a bare test fake) falls back to the original 50ms
	// poll so its behavior is unchanged.
	var wake <-chan struct{}
	var pollC <-chan time.Time
	if sub, ok := h.taskStore.(runStreamSubscriber); ok {
		ch, unsubscribe := sub.SubscribeRun(runID)
		defer unsubscribe()
		wake = ch
	} else {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		pollC = ticker.C
	}

	afterSequence := parseAfterSequence(r)
	lastStateJSON := ""
	for {
		events, err := h.taskStore.ListRunEvents(ctx, task.ID, runID, afterSequence, 200)
		if err != nil {
			stream.writeError(err)
			return
		}
		for _, event := range events {
			state, err := projector.projectEvent(ctx, task.ID, runID, event)
			if err != nil {
				stream.writeError(err)
				return
			}

			payload, err := taskRunStreamSnapshotPayload(state)
			if err != nil {
				stream.writeError(err)
				return
			}
			stream.writeSnapshotPayload(event.Sequence, payload)
			if state.Terminal {
				if finalState, ok := projector.terminalLiveState(ctx, task.ID, runID, event.Sequence, state.EventType); ok {
					if finalPayload, marshalErr := taskRunStreamSnapshotPayload(finalState); marshalErr == nil {
						payload = finalPayload
						stream.writeSnapshotPayload(event.Sequence, payload)
					}
				}
				stream.writeDonePayload(event.Sequence, payload)
			}
			stream.flush()
			afterSequence = event.Sequence
			if stateJSON, marshalErr := taskRunStreamStateJSON(state); marshalErr == nil {
				lastStateJSON = string(stateJSON)
			}
			if state.Terminal {
				return
			}
		}

		state, err := projector.liveState(ctx, task.ID, runID)
		if err != nil {
			stream.writeError(err)
			return
		}
		stateJSON, err := taskRunStreamStateJSON(state)
		if err != nil {
			stream.writeError(err)
			return
		}
		if string(stateJSON) != lastStateJSON {
			state.Sequence = int(afterSequence)
			state.EventType = "snapshot"
			state.Terminal = types.IsTerminalTaskRunStatus(state.Run.Status)
			payload, marshalErr := taskRunStreamSnapshotPayload(state)
			if marshalErr != nil {
				stream.writeError(marshalErr)
				return
			}
			stream.writeSnapshotPayload(afterSequence, payload)
			if state.Terminal {
				stream.writeDonePayload(afterSequence, payload)
			}
			stream.flush()
			lastStateJSON = string(stateJSON)
			if state.Terminal {
				return
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-pollC:
		case <-heartbeat.C:
			stream.writeKeepAlive()
		}
	}
}

func (h *Handler) HandleTaskRunEvents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	task, ok := h.loadAuthorizedTask(ctx, w, r)
	if !ok {
		return
	}
	run, ok := h.loadAuthorizedTaskRun(ctx, w, r, task)
	if !ok {
		return
	}
	afterSequence := parseAfterSequence(r)
	events, err := h.taskStore.ListRunEvents(ctx, task.ID, run.ID, afterSequence, 500)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, TaskRunEventsResponse{
		Object: "task_run_events",
		Data:   eventprotocol.FromTaskRunEvents(events),
	})
}

func (h *Handler) HandleAppendTaskRunEvent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	task, ok := h.loadAuthorizedTask(ctx, w, r)
	if !ok {
		return
	}
	run, ok := h.loadAuthorizedTaskRun(ctx, w, r, task)
	if !ok {
		return
	}
	var req AppendTaskRunEventRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	eventType := strings.TrimSpace(req.Type)
	if eventType == "" {
		eventType = "external.event"
	}
	extra := map[string]any{}
	for key, value := range req.Data {
		extra[key] = value
	}
	if req.StepID != "" {
		extra["step_id"] = req.StepID
	}
	if req.Status != "" {
		extra["status"] = req.Status
	}
	if req.Note != "" {
		extra["note"] = req.Note
	}
	event, err := h.taskRunEventRecorder().Append(ctx, runtimeevents.Event{
		TaskID:            task.ID,
		RunID:             run.ID,
		EventType:         eventType,
		Data:              extra,
		RequestID:         RequestIDFromContext(ctx),
		TraceID:           telemetry.TraceIDsFromContext(ctx).TraceID,
		SnapshotMode:      runtimeevents.SnapshotBestEffort,
		SnapshotPlacement: runtimeevents.SnapshotOverridesData,
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"object": "task_run_event",
		"data":   eventprotocol.FromTaskRunEvent(event),
	})
}

func parseAfterSequence(r *http.Request) int64 {
	raw := strings.TrimSpace(r.URL.Query().Get("after_sequence"))
	if raw == "" {
		raw = strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	}
	if raw == "" {
		return 0
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func (h *Handler) loadAuthorizedTask(ctx context.Context, w http.ResponseWriter, r *http.Request) (types.Task, bool) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task id is required")
		return types.Task{}, false
	}

	task, err := h.taskApplication().LoadTask(ctx, id)
	if err != nil {
		if errors.Is(err, errTaskStoreNotConfigured) {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
			return types.Task{}, false
		}
		if errors.Is(err, errTaskNotFound) {
			WriteError(w, http.StatusNotFound, errCodeNotFound, "task not found")
			return types.Task{}, false
		}
		telemetry.Error(h.logger, ctx, "gateway.tasks.load.failed",
			slog.String("event.name", "gateway.tasks.load.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return types.Task{}, false
	}
	return task, true
}

func (h *Handler) loadAuthorizedTaskRun(ctx context.Context, w http.ResponseWriter, r *http.Request, task types.Task) (types.TaskRun, bool) {
	runID := strings.TrimSpace(r.PathValue("run_id"))
	if runID == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "run id is required")
		return types.TaskRun{}, false
	}

	run, err := h.taskApplication().LoadTaskRun(ctx, task, runID)
	if err != nil {
		if errors.Is(err, errTaskStoreNotConfigured) {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
			return types.TaskRun{}, false
		}
		if errors.Is(err, errTaskRunNotFound) {
			WriteError(w, http.StatusNotFound, errCodeNotFound, "task run not found")
			return types.TaskRun{}, false
		}
		telemetry.Error(h.logger, ctx, "gateway.tasks.runs.load.failed",
			slog.String("event.name", "gateway.tasks.runs.load.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return types.TaskRun{}, false
	}
	return run, true
}

func writeSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
}
