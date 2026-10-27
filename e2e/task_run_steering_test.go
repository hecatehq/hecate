//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTaskRunLiveSteeringE2E(t *testing.T) {
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	firstStarted := make(chan struct{})
	release := make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(release) })
	var calls atomic.Int32
	var guidanceSeen atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "bad request", 400)
			return
		}
		if calls.Add(1) == 1 {
			close(firstStarted)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		} else {
			raw, _ := json.Marshal(request["messages"])
			guidanceSeen.Store(strings.Contains(string(raw), "Include the risks"))
		}
		if streamed, _ := request["stream"].(bool); streamed {
			writeAgentLoopFinalAnswerStream(t, w)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "steering-completion", "model": agentLoopE2EModel, "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "Done"}, "finish_reason": "stop"}}})
	}))
	defer upstream.Close()
	baseURL := gatewayServer(t, "HECATE_BACKEND=sqlite", "HECATE_TASK_APPROVAL_POLICIES=", "PROVIDER_FAKE_API_KEY=dummy", "PROVIDER_FAKE_BASE_URL="+upstream.URL+"/v1", "PROVIDER_FAKE_KIND=local", "PROVIDER_FAKE_MODELS="+agentLoopE2EModel)
	task := postJSONDecode[e2eTaskResponse](t, baseURL+"/hecate/v1/tasks", fmt.Sprintf(`{"prompt":"Inspect the workspace","execution_kind":"agent_loop","requested_provider":"fake","requested_model":%q,"working_directory":%q,"sandbox_allowed_root":%q,"workspace_mode":"in_place","timeout_ms":15000}`, agentLoopE2EModel, workspace, workspace))
	run := postJSONDecode[e2eTaskRunResponse](t, baseURL+"/hecate/v1/tasks/"+task.Data.ID+"/start", `{}`)
	select {
	case <-firstStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("provider request never started")
	}
	endpoint := baseURL + "/hecate/v1/tasks/" + task.Data.ID + "/runs/" + run.Data.ID
	body := `{"client_request_id":"operator-guidance","content":"Include the risks"}`
	type guidanceResponse struct {
		Data struct {
			ID, Status string
			Replay     bool
		} `json:"data"`
	}
	submitted := postJSONDecode[guidanceResponse](t, endpoint+"/steer", body)
	if submitted.Data.Status != "pending" {
		t.Fatalf("admission=%+v", submitted)
	}
	replay := postJSONDecode[guidanceResponse](t, endpoint+"/steer", body)
	if !replay.Data.Replay || replay.Data.ID != submitted.Data.ID {
		t.Fatalf("replay=%+v", replay)
	}
	unblock.Do(func() { close(release) })
	result := waitForE2ETaskRunTerminal(t, baseURL, task.Data.ID, run.Data.ID, 10*time.Second)
	if result.Status != "completed" || calls.Load() != 2 || !guidanceSeen.Load() {
		t.Fatalf("result=%+v calls=%d guidance=%t", result, calls.Load(), guidanceSeen.Load())
	}
	applied := postJSONDecode[guidanceResponse](t, endpoint+"/steer", body)
	if applied.Data.Status != "applied" || !applied.Data.Replay {
		t.Fatalf("applied=%+v", applied)
	}
	events := getJSON[e2eTaskEventsResponse](t, endpoint+"/events")
	assertE2EEventOrder(t, events.Data, []string{"run.steering_submitted", "run.steering_applied", "run.finished"})
}
