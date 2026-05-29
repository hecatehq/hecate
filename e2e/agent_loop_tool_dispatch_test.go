//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const agentLoopE2EModel = "gpt-4o-mini"

func TestAgentLoopToolDispatchE2E(t *testing.T) {
	workDir := t.TempDir()
	canonicalWorkDir, err := filepath.EvalSymlinks(workDir)
	if err != nil {
		t.Fatalf("canonicalize temp dir: %v", err)
	}
	workDir = canonicalWorkDir
	upstream, captured := fakeAgentLoopToolCallingUpstream(t)
	baseURL := gatewayServer(t,
		"HECATE_TASK_APPROVAL_POLICIES=",
		"PROVIDER_FAKE_API_KEY=dummy",
		"PROVIDER_FAKE_BASE_URL="+upstream,
		"PROVIDER_FAKE_KIND=local",
		"PROVIDER_FAKE_MODELS="+agentLoopE2EModel,
	)

	taskBody := fmt.Sprintf(`{
		"title": "agent loop tool dispatch e2e",
		"prompt": "Use shell_exec to print agent-loop-e2e, then summarize the result.",
		"execution_kind": "agent_loop",
		"requested_model": %q,
		"working_directory": %q,
		"sandbox_allowed_root": %q,
		"workspace_mode": "in_place",
		"timeout_ms": 10000
	}`, agentLoopE2EModel, workDir, workDir)
	created := postJSONDecode[e2eTaskResponse](t, baseURL+"/hecate/v1/tasks", taskBody)
	if created.Data.ID == "" {
		t.Fatal("created task id is empty")
	}

	started := postJSONDecode[e2eTaskRunResponse](t, baseURL+"/hecate/v1/tasks/"+created.Data.ID+"/start", `{}`)
	if started.Data.ID == "" {
		t.Fatal("started run id is empty")
	}
	run := waitForE2ETaskRunTerminal(t, baseURL, created.Data.ID, started.Data.ID, 10*time.Second)
	if run.Status != "completed" {
		t.Fatalf("run status = %q last_error=%q, want completed", run.Status, run.LastError)
	}

	steps := getJSON[e2eTaskStepsResponse](t, baseURL+"/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/steps")
	foundToolStep := false
	for _, step := range steps.Data {
		if step.Kind == "tool" && step.ToolName == "shell_exec" {
			foundToolStep = true
			if step.Status != "completed" {
				t.Fatalf("shell_exec step = %+v, want completed", step)
			}
		}
	}
	if !foundToolStep {
		t.Fatalf("shell_exec tool step not found in %+v", steps.Data)
	}

	bodies := capturedBodies(captured)
	if len(bodies) != 2 {
		t.Fatalf("upstream chat requests = %d, want 2: %+v", len(bodies), bodies)
	}
	if !requestAdvertisedTool(bodies[0], "shell_exec") {
		t.Fatalf("first upstream request did not advertise shell_exec tool: %+v", bodies[0])
	}
	if !requestHasToolResult(bodies[1], "call-shell-e2e", "agent-loop-e2e") {
		t.Fatalf("second upstream request did not include shell tool result: %+v", bodies[1])
	}
}

func fakeAgentLoopToolCallingUpstream(t *testing.T) (string, *capturedRequests) {
	t.Helper()
	captured := &capturedRequests{}
	var chatCalls atomic.Int32
	shellArgs := `{"command":"printf agent-loop-e2e"}`

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"object":"list","data":[{"id":%q,"object":"model"}]}`, agentLoopE2EModel)
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		captured.record(body)

		callNumber := chatCalls.Add(1)
		if streamed, _ := body["stream"].(bool); streamed {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)
			if callNumber == 1 {
				writeAgentLoopToolCallStream(t, w, shellArgs)
			} else {
				writeAgentLoopFinalAnswerStream(t, w)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if callNumber == 1 {
			fmt.Fprintf(w, `{"id":"chatcmpl-agent-loop-1","object":"chat.completion","created":1700000000,"model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"I will inspect.","tool_calls":[{"id":"call-shell-e2e","type":"function","function":{"name":"shell_exec","arguments":%q}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":5,"total_tokens":10}}`, agentLoopE2EModel, shellArgs)
			return
		}
		fmt.Fprintf(w, `{"id":"chatcmpl-agent-loop-2","object":"chat.completion","created":1700000001,"model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"Tool dispatch completed."},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":5,"total_tokens":10}}`, agentLoopE2EModel)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL, captured
}

func writeAgentLoopToolCallStream(t *testing.T, w http.ResponseWriter, shellArgs string) {
	t.Helper()
	writeOpenAIStreamChunk(t, w, map[string]any{
		"id":      "chatcmpl-agent-loop-1",
		"object":  "chat.completion.chunk",
		"created": 1700000000,
		"model":   agentLoopE2EModel,
		"choices": []map[string]any{{
			"index": 0,
			"delta": map[string]any{
				"role":    "assistant",
				"content": "I will inspect.",
			},
			"finish_reason": nil,
		}},
	})
	writeOpenAIStreamChunk(t, w, map[string]any{
		"id":      "chatcmpl-agent-loop-1",
		"object":  "chat.completion.chunk",
		"created": 1700000000,
		"model":   agentLoopE2EModel,
		"choices": []map[string]any{{
			"index": 0,
			"delta": map[string]any{
				"tool_calls": []map[string]any{{
					"index": 0,
					"id":    "call-shell-e2e",
					"type":  "function",
					"function": map[string]any{
						"name":      "shell_exec",
						"arguments": shellArgs,
					},
				}},
			},
			"finish_reason": nil,
		}},
	})
	writeOpenAIStreamChunk(t, w, map[string]any{
		"id":      "chatcmpl-agent-loop-1",
		"object":  "chat.completion.chunk",
		"created": 1700000000,
		"model":   agentLoopE2EModel,
		"choices": []map[string]any{{
			"index":         0,
			"delta":         map[string]any{},
			"finish_reason": "tool_calls",
		}},
	})
	fmt.Fprint(w, "data: [DONE]\n\n")
	flushSSE(w)
}

func writeAgentLoopFinalAnswerStream(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	writeOpenAIStreamChunk(t, w, map[string]any{
		"id":      "chatcmpl-agent-loop-2",
		"object":  "chat.completion.chunk",
		"created": 1700000001,
		"model":   agentLoopE2EModel,
		"choices": []map[string]any{{
			"index": 0,
			"delta": map[string]any{
				"role":    "assistant",
				"content": "Tool dispatch completed.",
			},
			"finish_reason": nil,
		}},
	})
	writeOpenAIStreamChunk(t, w, map[string]any{
		"id":      "chatcmpl-agent-loop-2",
		"object":  "chat.completion.chunk",
		"created": 1700000001,
		"model":   agentLoopE2EModel,
		"choices": []map[string]any{{
			"index":         0,
			"delta":         map[string]any{},
			"finish_reason": "stop",
		}},
	})
	fmt.Fprint(w, "data: [DONE]\n\n")
	flushSSE(w)
}

func writeOpenAIStreamChunk(t *testing.T, w http.ResponseWriter, chunk map[string]any) {
	t.Helper()
	raw, err := json.Marshal(chunk)
	if err != nil {
		t.Fatalf("marshal stream chunk: %v", err)
	}
	fmt.Fprintf(w, "data: %s\n\n", raw)
	flushSSE(w)
}

func flushSSE(w http.ResponseWriter) {
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func waitForE2ETaskRunTerminal(t *testing.T, baseURL, taskID, runID string, timeout time.Duration) e2eTaskRun {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last e2eTaskRun
	for time.Now().Before(deadline) {
		resp := getJSON[e2eTaskRunResponse](t, baseURL+"/hecate/v1/tasks/"+taskID+"/runs/"+runID)
		last = resp.Data
		switch resp.Data.Status {
		case "completed", "failed", "cancelled":
			return resp.Data
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("run %s did not reach terminal state within %s; last=%+v", runID, timeout, last)
	return e2eTaskRun{}
}

func capturedBodies(c *capturedRequests) []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]map[string]any, len(c.bodies))
	copy(out, c.bodies)
	return out
}

func requestAdvertisedTool(body map[string]any, toolName string) bool {
	tools, ok := body["tools"].([]any)
	if !ok {
		return false
	}
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		fn, ok := tool["function"].(map[string]any)
		if ok && fn["name"] == toolName {
			return true
		}
	}
	return false
}

func requestHasToolResult(body map[string]any, toolCallID, contentSubstring string) bool {
	messages, ok := body["messages"].([]any)
	if !ok {
		return false
	}
	for _, rawMessage := range messages {
		msg, ok := rawMessage.(map[string]any)
		if !ok {
			continue
		}
		if msg["role"] != "tool" || msg["tool_call_id"] != toolCallID {
			continue
		}
		content, _ := msg["content"].(string)
		if strings.Contains(content, contentSubstring) {
			return true
		}
	}
	return false
}
