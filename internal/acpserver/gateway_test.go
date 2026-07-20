package acpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPRuntimeUsesAuthenticatedTaskRuntimeContract(t *testing.T) {
	t.Parallel()

	var requests []recordedRequest
	runtime, err := NewHTTPRuntime("http://127.0.0.1:8765", "runtime-secret")
	if err != nil {
		t.Fatalf("NewHTTPRuntime: %v", err)
	}
	runtime.setHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			body := map[string]any{}
			if r.Body != nil {
				_ = json.NewDecoder(r.Body).Decode(&body)
			}
			requests = append(requests, recordedRequest{
				Method: r.Method,
				Path:   r.URL.RequestURI(),
				Token:  r.Header.Get("X-Hecate-Runtime-Token"),
				Body:   body,
			})
			if r.Header.Get("X-Hecate-Runtime-Token") != "runtime-secret" {
				return testHTTPResponse(http.StatusUnauthorized, "missing runtime token")
			}
			switch r.URL.Path {
			case "/hecate/v1/providers/status":
				return testHTTPResponse(http.StatusOK, map[string]any{"data": []map[string]any{
					{"name": "zeta", "routing_ready": true, "auto_route_ready": true, "default_model": "z-default", "models": []string{"z-a", "z-default"}},
					{"name": "alpha", "routing_ready": true, "auto_route_ready": false, "models": []string{"a-2", "a-1"}},
					{"name": "blocked", "routing_ready": false, "auto_route_ready": false, "default_model": "nope"},
				}})
			case "/hecate/v1/tasks":
				return testHTTPResponse(http.StatusOK, map[string]any{"data": map[string]any{"id": "task_1"}})
			case "/hecate/v1/tasks/task_1/start":
				return testHTTPResponse(http.StatusOK, map[string]any{"data": map[string]any{"id": "run_1", "status": "queued"}})
			case "/hecate/v1/tasks/task_1/runs/run_1/continue":
				return testHTTPResponse(http.StatusOK, map[string]any{"data": map[string]any{"id": "run_2", "status": "queued"}})
			case "/hecate/v1/tasks/task_1/runs/run_2/cancel":
				return testHTTPResponse(http.StatusNoContent, nil)
			case "/hecate/v1/tasks/task_1/runs/run_2/events":
				return testHTTPResponse(http.StatusOK, map[string]any{"data": []map[string]any{{"sequence": 7, "type": "run.finished", "data": map[string]any{}}}})
			default:
				return testHTTPResponse(http.StatusNotFound, "not found")
			}
		}),
	})

	if err := runtime.EnsureReady(context.Background()); err != nil {
		t.Fatalf("EnsureReady: %v", err)
	}

	task, err := runtime.CreateTask(context.Background(), CreateTaskRequest{
		Title:            "ACP session",
		Prompt:           "Inspect it",
		WorkingDirectory: "/workspace",
	})
	if err != nil || task.ID != "task_1" {
		t.Fatalf("CreateTask = %#v, %v", task, err)
	}
	run, err := runtime.StartTask(context.Background(), task.ID)
	if err != nil || run.ID != "run_1" {
		t.Fatalf("StartTask = %#v, %v", run, err)
	}
	continued, err := runtime.ContinueTask(context.Background(), task.ID, run.ID, "continue")
	if err != nil || continued.ID != "run_2" {
		t.Fatalf("ContinueTask = %#v, %v", continued, err)
	}
	if err := runtime.CancelRun(context.Background(), task.ID, continued.ID, "closed"); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	events, err := runtime.ListRunEvents(context.Background(), task.ID, continued.ID, 6)
	if err != nil || len(events) != 1 || events[0].Sequence != 7 || events[0].Type != "run.finished" {
		t.Fatalf("ListRunEvents = %#v, %v", events, err)
	}

	if len(requests) != 6 {
		t.Fatalf("requests = %#v, want six", requests)
	}
	created := requests[1]
	if created.Path != "/hecate/v1/tasks" || created.Method != http.MethodPost {
		t.Fatalf("create request = %#v", created)
	}
	for key, want := range map[string]string{
		"execution_kind":    "agent_loop",
		"workspace_mode":    "in_place",
		"working_directory": "/workspace",
	} {
		if got, _ := created.Body[key].(string); got != want {
			t.Fatalf("create request body[%q] = %q, want %q; body=%#v", key, got, want, created.Body)
		}
	}
	for _, key := range []string{"requested_provider", "requested_model"} {
		if _, exists := created.Body[key]; exists {
			t.Fatalf("create request unexpectedly pins %q: %#v", key, created.Body)
		}
	}
	if eventsRequest := requests[5]; eventsRequest.Path != "/hecate/v1/tasks/task_1/runs/run_2/events?after_sequence=6" {
		t.Fatalf("events request = %#v", eventsRequest)
	}
	for _, request := range requests {
		if request.Token != "runtime-secret" {
			t.Fatalf("request missing runtime token: %#v", request)
		}
	}
}

func TestHTTPRuntimeRequiresHecateAutoRouteReadiness(t *testing.T) {
	t.Parallel()

	runtime, err := NewHTTPRuntime("http://127.0.0.1:8765", "")
	if err != nil {
		t.Fatalf("NewHTTPRuntime: %v", err)
	}
	runtime.setHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return testHTTPResponse(http.StatusOK, map[string]any{"data": []map[string]any{
			{"routing_ready": true, "auto_route_ready": false, "models": []string{"discovered-but-unpinned"}},
		}})
	})})

	if err := runtime.EnsureReady(context.Background()); err == nil {
		t.Fatal("EnsureReady succeeded without a Hecate auto-route")
	}
}

func TestNewHTTPRuntimeRejectsNonLoopbackAndMalformedOrigins(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"",
		"https://example.com",
		"ftp://127.0.0.1:8765",
		"http://0.0.0.0:8765",
		"http://127.0.0.1:8765/runtime",
		"http://user@127.0.0.1:8765",
		"http://127.0.0.1:8765?token=bad",
	} {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			if _, err := NewHTTPRuntime(raw, ""); err == nil {
				t.Fatalf("NewHTTPRuntime(%q) succeeded", raw)
			}
		})
	}
	for _, raw := range []string{
		"http://127.0.0.1:8765",
		"http://[::1]:8765",
	} {
		raw := raw
		t.Run("accept "+raw, func(t *testing.T) {
			t.Parallel()
			if _, err := NewHTTPRuntime(raw, ""); err != nil {
				t.Fatalf("NewHTTPRuntime(%q): %v", raw, err)
			}
		})
	}
	for _, raw := range []string{
		"http://localhost:8765",
		"http://localhost.:8765",
	} {
		raw := raw
		t.Run("reject hostname "+raw, func(t *testing.T) {
			t.Parallel()
			if _, err := NewHTTPRuntime(raw, ""); err == nil {
				t.Fatalf("NewHTTPRuntime(%q) succeeded", raw)
			}
		})
	}
}

func TestHTTPRuntimeDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()

	targetHit := make(chan struct{}, 1)
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		select {
		case targetHit <- struct{}{}:
		default:
		}
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", target.URL+"/stolen")
		w.WriteHeader(http.StatusFound)
	}))
	defer origin.Close()

	runtime, err := NewHTTPRuntime(origin.URL, "runtime-secret")
	if err != nil {
		t.Fatalf("NewHTTPRuntime: %v", err)
	}
	if err := runtime.EnsureReady(context.Background()); err == nil {
		t.Fatal("EnsureReady succeeded through a redirect")
	}
	select {
	case <-targetHit:
		t.Fatal("runtime client followed a redirect and contacted its target")
	case <-time.After(50 * time.Millisecond):
	}
}

type recordedRequest struct {
	Method string
	Path   string
	Token  string
	Body   map[string]any
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func testHTTPResponse(status int, value any) (*http.Response, error) {
	var body bytes.Buffer
	if value != nil {
		if err := json.NewEncoder(&body).Encode(value); err != nil {
			return nil, err
		}
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(&body),
		Header:     make(http.Header),
	}, nil
}
