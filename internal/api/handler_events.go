package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hecate/agent-runtime/internal/eventprotocol"
	"github.com/hecate/agent-runtime/internal/taskstate"
)

// HandleEvents serves GET /v1/events — a paginated cross-run feed of
// task events. Useful for external dashboards (Grafana, Slack
// notifiers, audit log shippers) that want a single subscription
// rather than per-run polling.
//
// Query parameters:
//   - event_type: comma-separated allowlist (e.g. "agent.turn.completed,run.finished")
//   - task_id:    optional single task scope
//   - after_sequence: cursor; only events with sequence > this are returned
//   - limit:      max items, default 200, capped at 500
//
// Single-user mode: every event is visible to the operator.
func (h *Handler) HandleEvents(w http.ResponseWriter, r *http.Request) {
	if h.taskStore == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task store is not configured")
		return
	}
	ctx := r.Context()

	filter, errMsg := buildEventFilterFromRequest(r)
	if errMsg != "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, errMsg)
		return
	}
	events, err := h.taskStore.ListEvents(ctx, filter)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	items := make([]eventprotocol.Envelope, 0, len(events))
	var nextSeq int64
	for _, event := range events {
		items = append(items, eventprotocol.FromTaskRunEvent(event))
		if event.Sequence > nextSeq {
			nextSeq = event.Sequence
		}
	}
	WriteJSON(w, http.StatusOK, EventsResponse{
		Object:            "events",
		Data:              items,
		NextAfterSequence: nextSeq,
	})
}

// HandleEventsStream serves GET /v1/events/stream — a long-lived SSE
// connection that flushes new events as they're appended. Each
// message is one event; the SSE `id` field is the event sequence so
// reconnects via `Last-Event-ID` are seamless.
//
// Same auth + scope rules as HandleEvents. Non-admin tenant
// constraints are re-resolved every poll iteration to pick up newly
// created tasks during the stream's lifetime.
func (h *Handler) HandleEventsStream(w http.ResponseWriter, r *http.Request) {
	if h.taskStore == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task store is not configured")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "streaming not supported by server")
		return
	}
	ctx := r.Context()

	baseFilter, errMsg := buildEventFilterFromRequest(r)
	if errMsg != "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, errMsg)
		return
	}
	// Resume cursor: prefer Last-Event-ID over after_sequence so
	// browser EventSource reconnects pick up automatically.
	if v := strings.TrimSpace(r.Header.Get("Last-Event-ID")); v != "" {
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil && parsed > baseFilter.AfterSequence {
			baseFilter.AfterSequence = parsed
		}
	}

	writeSSEHeaders(w)
	// Send a comment line to flush headers immediately. Without this,
	// some proxies hold the connection until the first event, which
	// looks like a hang on the client side.
	fmt.Fprintln(w, ": ok")
	flusher.Flush()

	pollInterval := 250 * time.Millisecond
	heartbeatInterval := 15 * time.Second
	lastHeartbeat := time.Now()
	cursor := baseFilter.AfterSequence

	for {
		if err := ctx.Err(); err != nil {
			return
		}

		filter := baseFilter
		filter.AfterSequence = cursor

		events, err := h.taskStore.ListEvents(ctx, filter)
		if err != nil {
			fmt.Fprintf(w, "event: error\ndata: {\"error\":{\"message\":%q}}\n\n", err.Error())
			flusher.Flush()
			return
		}
		for _, event := range events {
			payload, marshalErr := json.Marshal(map[string]any{
				"object": "event",
				"data":   eventprotocol.FromTaskRunEvent(event),
			})
			if marshalErr != nil {
				continue
			}
			fmt.Fprintf(w, "id: %d\nevent: event\ndata: %s\n\n", event.Sequence, payload)
			cursor = event.Sequence
			lastHeartbeat = time.Now()
		}
		flusher.Flush()

		// Keep idle connections warm so proxies / load balancers
		// don't time them out. Only emit when nothing else has
		// flushed in the heartbeat window.
		if time.Since(lastHeartbeat) >= heartbeatInterval {
			fmt.Fprintln(w, ": heartbeat")
			flusher.Flush()
			lastHeartbeat = time.Now()
		}

		if !sleepWithContext(ctx, pollInterval) {
			return
		}
	}
}

// buildEventFilterFromRequest parses the public-events query string.
// Returns a filter with default Limit when none is supplied; on
// validation failure returns an empty filter and a human-readable
// error message for a 400 response.
func buildEventFilterFromRequest(r *http.Request) (taskstate.EventFilter, string) {
	q := r.URL.Query()
	filter := taskstate.EventFilter{}

	if raw := strings.TrimSpace(q.Get("event_type")); raw != "" {
		for _, t := range strings.Split(raw, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				filter.EventTypes = append(filter.EventTypes, t)
			}
		}
	}
	if raw := strings.TrimSpace(q.Get("task_id")); raw != "" {
		filter.TaskIDs = []string{raw}
	}
	if raw := strings.TrimSpace(q.Get("after_sequence")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			return taskstate.EventFilter{}, "after_sequence must be a non-negative integer"
		}
		filter.AfterSequence = parsed
	}
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			return taskstate.EventFilter{}, "limit must be a positive integer"
		}
		// Cap at 500 to keep responses bounded; rate-limited polling
		// + the cursor-based design still lets clients drain history.
		if parsed > 500 {
			parsed = 500
		}
		filter.Limit = parsed
	}
	if filter.Limit == 0 {
		filter.Limit = 200
	}
	return filter, ""
}

// sleepWithContext sleeps for d or returns false if ctx is cancelled
// first. Lets the SSE poll loop exit promptly when the client
// disconnects rather than always waiting the full poll interval.
func sleepWithContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
