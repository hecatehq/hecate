package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

// newEventsTestHandler builds the smallest possible Handler that can
// serve the public events endpoints — just a logger and a task store.
// The full NewHandler wires gateway.Service, runner, etc., which are
// irrelevant here and would mask the actual events behavior under
// noise. Each test seeds events via store.AppendRunEvent directly.
func newEventsTestHandler(t *testing.T) (*Handler, taskstate.Store) {
	t.Helper()
	taskStore := taskstate.NewMemoryStore()

	h := &Handler{
		logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		taskStore: taskStore,
	}
	return h, taskStore
}

func seedTaskAndEvents(t *testing.T, store taskstate.Store, taskID string, events []types.TaskRunEvent) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.CreateTask(ctx, types.Task{ID: taskID, Status: "running"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	for _, evt := range events {
		evt.TaskID = taskID
		if _, err := store.AppendRunEvent(ctx, evt); err != nil {
			t.Fatalf("AppendRunEvent: %v", err)
		}
	}
}

// callEvents drives a GET request through the handler and returns the
// parsed JSON + status. Keeps each test focused on the assertion
// rather than scaffolding.
func callEvents(t *testing.T, h *Handler, path string) (int, EventsResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.HandleEvents(rec, req)
	if rec.Code != http.StatusOK {
		return rec.Code, EventsResponse{}
	}
	var resp EventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, rec.Body.String())
	}
	return rec.Code, resp
}

func TestHandleEvents_ReturnsAllEvents(t *testing.T) {
	h, store := newEventsTestHandler(t)
	seedTaskAndEvents(t, store, "task-A", []types.TaskRunEvent{
		{RunID: "run-A", EventType: "run.created"},
		{RunID: "run-A", EventType: "turn.completed"},
	})
	seedTaskAndEvents(t, store, "task-B", []types.TaskRunEvent{
		{RunID: "run-B", EventType: "run.created"},
	})

	code, resp := callEvents(t, h, "/hecate/v1/events")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(resp.Data) != 3 {
		t.Errorf("got %d events, want 3", len(resp.Data))
	}
}

func TestHandleEvents_EventTypeFilter(t *testing.T) {
	h, store := newEventsTestHandler(t)
	seedTaskAndEvents(t, store, "task-A", []types.TaskRunEvent{
		{RunID: "run-A", EventType: "run.created"},
		{RunID: "run-A", EventType: "turn.completed"},
		{RunID: "run-A", EventType: "turn.completed"},
		{RunID: "run-A", EventType: "run.finished"},
	})

	code, resp := callEvents(t, h, "/hecate/v1/events?event_type=turn.completed")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(resp.Data) != 2 {
		t.Errorf("got %d events, want 2 (event_type=turn.completed)", len(resp.Data))
	}
	for _, e := range resp.Data {
		if e.Type != "turn.completed" {
			t.Errorf("filter leaked %q", e.Type)
		}
	}
}

func TestHandleEvents_AfterSequenceCursor(t *testing.T) {
	h, store := newEventsTestHandler(t)
	seedTaskAndEvents(t, store, "task-A", []types.TaskRunEvent{
		{RunID: "run-A", EventType: "run.created"},
		{RunID: "run-A", EventType: "turn.completed"},
		{RunID: "run-A", EventType: "run.finished"},
	})

	code, page1 := callEvents(t, h, "/hecate/v1/events?limit=1")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(page1.Data) != 1 {
		t.Fatalf("page1 size = %d, want 1", len(page1.Data))
	}
	if page1.NextAfterSequence == 0 {
		t.Errorf("NextAfterSequence missing on non-empty response")
	}

	resumePath := "/hecate/v1/events?after_sequence=" + strconv.FormatInt(page1.NextAfterSequence, 10) + "&limit=10"
	code2, page2 := callEvents(t, h, resumePath)
	if code2 != http.StatusOK {
		t.Fatalf("page2 status = %d, want 200", code2)
	}
	if len(page2.Data) != 2 {
		t.Errorf("page2 size = %d, want 2 (the remaining events)", len(page2.Data))
	}
	for _, e := range page2.Data {
		if e.Sequence <= page1.NextAfterSequence {
			t.Errorf("cursor leak: event seq %d <= cursor %d", e.Sequence, page1.NextAfterSequence)
		}
	}
}

func TestHandleEvents_RejectsBadAfterSequence(t *testing.T) {
	h, _ := newEventsTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/hecate/v1/events?after_sequence=not-a-number", nil)
	rec := httptest.NewRecorder()
	h.HandleEvents(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (bad after_sequence)", rec.Code)
	}
}
