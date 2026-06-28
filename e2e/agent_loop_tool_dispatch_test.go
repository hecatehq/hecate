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
	if run.Provider != "fake" || run.ProviderKind != "local" || run.Model != agentLoopE2EModel {
		t.Fatalf("run route = provider %q kind %q model %q, want fake/local/%s", run.Provider, run.ProviderKind, run.Model, agentLoopE2EModel)
	}

	steps := getJSON[e2eTaskStepsResponse](t, baseURL+"/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/steps")
	foundToolStep := false
	var modelSteps []e2eTaskStep
	if len(steps.Data) != 3 {
		t.Fatalf("steps = %d, want 3 (model, tool, model): %+v", len(steps.Data), steps.Data)
	}
	for i, step := range steps.Data {
		if step.Index != i+1 {
			t.Fatalf("step[%d] index = %d, want %d; steps=%+v", i, step.Index, i+1, steps.Data)
		}
		if step.Kind == "model" {
			modelSteps = append(modelSteps, step)
		}
	}
	if len(modelSteps) != 2 {
		t.Fatalf("model steps = %d, want 2: %+v", len(modelSteps), steps.Data)
	}
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

	events := getJSON[e2eTaskEventsResponse](t, baseURL+"/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/events")
	assertE2EEventTypes(t, events.Data, "turn.started", "assistant.tool_call_proposed", "assistant.final_answer")
	var turnStarted []e2eEventEnvelope
	var turnEvents []e2eEventEnvelope
	foundToolProposal := false
	foundFinalAnswer := false
	for _, event := range events.Data {
		if event.Type == "turn.started" {
			turnStarted = append(turnStarted, event)
		}
		if event.Type == "turn.completed" {
			turnEvents = append(turnEvents, event)
		}
		if event.Type == "assistant.tool_call_proposed" &&
			event.Data["tool_call_id"] == "call-shell-e2e" &&
			event.Data["tool_name"] == "shell_exec" {
			foundToolProposal = true
		}
		if event.Type == "assistant.final_answer" && strings.Contains(fmt.Sprint(event.Data["summary"]), "Tool dispatch completed.") {
			foundFinalAnswer = true
		}
	}
	if len(turnStarted) != 2 {
		t.Fatalf("turn.started events = %d, want 2: %+v", len(turnStarted), events.Data)
	}
	assertE2ENumber(t, turnStarted[0].Data, "turn_index", 1)
	assertE2ENumber(t, turnStarted[1].Data, "turn_index", 2)
	if !foundToolProposal {
		t.Fatalf("assistant.tool_call_proposed for call-shell-e2e not found in %+v", events.Data)
	}
	if !foundFinalAnswer {
		t.Fatalf("assistant.final_answer not found in %+v", events.Data)
	}
	if len(turnEvents) != 2 {
		t.Fatalf("turn.completed events = %d, want 2: %+v", len(turnEvents), events.Data)
	}
	assertE2ENumber(t, turnEvents[0].Data, "turn_index", 1)
	assertE2ENumber(t, turnEvents[0].Data, "tool_calls", 1)
	assertE2ENumber(t, turnEvents[0].Data, "run_cumulative_cost_micros_usd", 0)
	if turnEvents[0].Data["step_id"] != modelSteps[0].ID {
		t.Fatalf("turn 1 step_id = %v, want %s", turnEvents[0].Data["step_id"], modelSteps[0].ID)
	}
	assertE2ENumber(t, turnEvents[1].Data, "turn_index", 2)
	assertE2ENumber(t, turnEvents[1].Data, "tool_calls", 0)
	assertE2ENumber(t, turnEvents[1].Data, "run_cumulative_cost_micros_usd", 0)
	if turnEvents[1].Data["step_id"] != modelSteps[1].ID {
		t.Fatalf("turn 2 step_id = %v, want %s", turnEvents[1].Data["step_id"], modelSteps[1].ID)
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

func TestAgentLoopTerminalToolsE2E(t *testing.T) {
	workDir := t.TempDir()
	canonicalWorkDir, err := filepath.EvalSymlinks(workDir)
	if err != nil {
		t.Fatalf("canonicalize temp dir: %v", err)
	}
	workDir = canonicalWorkDir
	upstream, captured := fakeAgentLoopTerminalUpstream(t)
	baseURL := gatewayServer(t,
		"HECATE_TASK_APPROVAL_POLICIES=",
		"PROVIDER_FAKE_API_KEY=dummy",
		"PROVIDER_FAKE_BASE_URL="+upstream,
		"PROVIDER_FAKE_KIND=local",
		"PROVIDER_FAKE_MODELS="+agentLoopE2EModel,
	)

	taskBody := fmt.Sprintf(`{
		"title": "agent loop terminal tools e2e",
		"prompt": "Use terminal_open and terminal_wait to print terminal-e2e, then summarize the result.",
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
	foundOpen := false
	foundWait := false
	for _, step := range steps.Data {
		if step.Kind == "tool" && step.ToolName == "terminal_open" && step.Status == "completed" {
			foundOpen = true
		}
		if step.Kind == "tool" && step.ToolName == "terminal_wait" && step.Status == "completed" {
			foundWait = true
		}
	}
	if !foundOpen || !foundWait {
		t.Fatalf("terminal tool steps open=%v wait=%v steps=%+v", foundOpen, foundWait, steps.Data)
	}

	events := getJSON[e2eTaskEventsResponse](t, baseURL+"/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/events")
	foundOpenProposal := false
	foundWaitProposal := false
	for _, event := range events.Data {
		if event.Type != "assistant.tool_call_proposed" {
			continue
		}
		switch event.Data["tool_name"] {
		case "terminal_open":
			foundOpenProposal = true
		case "terminal_wait":
			foundWaitProposal = true
		}
	}
	if !foundOpenProposal || !foundWaitProposal {
		t.Fatalf("terminal tool proposals open=%v wait=%v events=%+v", foundOpenProposal, foundWaitProposal, events.Data)
	}

	bodies := capturedBodies(captured)
	if len(bodies) != 3 {
		t.Fatalf("upstream chat requests = %d, want 3: %+v", len(bodies), bodies)
	}
	if !requestAdvertisedTool(bodies[0], "terminal_open") || !requestAdvertisedTool(bodies[0], "terminal_wait") {
		t.Fatalf("first upstream request did not advertise terminal tools: %+v", bodies[0])
	}
	if !requestHasToolResult(bodies[1], "call-terminal-open", "terminal_id=") {
		t.Fatalf("second upstream request did not include terminal_open result: %+v", bodies[1])
	}
	if !requestHasToolResult(bodies[2], "call-terminal-wait", "terminal-e2e") {
		t.Fatalf("third upstream request did not include terminal output: %+v", bodies[2])
	}
}

func TestAgentLoopWebSearchToolE2E(t *testing.T) {
	workDir := t.TempDir()
	canonicalWorkDir, err := filepath.EvalSymlinks(workDir)
	if err != nil {
		t.Fatalf("canonicalize temp dir: %v", err)
	}
	workDir = canonicalWorkDir
	searchEndpoint, searchCalls := fakeBraveSearchEndpoint(t)
	upstream, captured := fakeAgentLoopWebSearchUpstream(t)
	baseURL := gatewayServer(t,
		"HECATE_TASK_APPROVAL_POLICIES=",
		"PROVIDER_FAKE_API_KEY=dummy",
		"PROVIDER_FAKE_BASE_URL="+upstream,
		"PROVIDER_FAKE_KIND=local",
		"PROVIDER_FAKE_MODELS="+agentLoopE2EModel,
		"HECATE_TASK_WEB_SEARCH_PROVIDER=brave",
		"HECATE_TASK_WEB_SEARCH_API_KEY=search-token",
		"HECATE_TASK_WEB_SEARCH_ENDPOINT="+searchEndpoint,
	)

	taskBody := fmt.Sprintf(`{
		"title": "agent loop web search e2e",
		"prompt": "Search for Hecate agent runtime, then summarize the result.",
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
	foundSearchStep := false
	for _, step := range steps.Data {
		if step.Kind != "tool" || step.ToolName != "web_search" {
			continue
		}
		foundSearchStep = true
		if step.Status != "completed" {
			t.Fatalf("web_search step = %+v, want completed", step)
		}
		if step.OutputSummary["provider"] != "brave" {
			t.Fatalf("web_search provider summary = %#v, want brave", step.OutputSummary)
		}
		assertE2ENumber(t, step.OutputSummary, "result_count", 1)
	}
	if !foundSearchStep {
		t.Fatalf("web_search tool step not found in %+v", steps.Data)
	}

	events := getJSON[e2eTaskEventsResponse](t, baseURL+"/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/events")
	foundToolProposal := false
	for _, event := range events.Data {
		if event.Type == "assistant.tool_call_proposed" && event.Data["tool_name"] == "web_search" {
			foundToolProposal = true
		}
	}
	if !foundToolProposal {
		t.Fatalf("assistant.tool_call_proposed for web_search not found in %+v", events.Data)
	}

	bodies := capturedBodies(captured)
	if len(bodies) != 2 {
		t.Fatalf("upstream chat requests = %d, want 2: %+v", len(bodies), bodies)
	}
	if !requestAdvertisedTool(bodies[0], "web_search") {
		t.Fatalf("first upstream request did not advertise web_search tool: %+v", bodies[0])
	}
	if !requestHasToolResult(bodies[1], "call-web-search-e2e", "https://example.test/hecate-runtime") {
		t.Fatalf("second upstream request did not include web_search result URL: %+v", bodies[1])
	}
	if got := searchCalls.Load(); got != 1 {
		t.Fatalf("search endpoint calls = %d, want 1", got)
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

func fakeAgentLoopTerminalUpstream(t *testing.T) (string, *capturedRequests) {
	t.Helper()
	captured := &capturedRequests{}
	var chatCalls atomic.Int32
	openArgs := `{"command":"sh","args":["-c","printf terminal-e2e"]}`

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
			switch callNumber {
			case 1:
				writeAgentLoopNamedToolCallStream(t, w, "chatcmpl-agent-loop-terminal-1", "call-terminal-open", "terminal_open", openArgs, "I will open a terminal.")
			case 2:
				terminalID := terminalIDFromToolResult(body, "call-terminal-open")
				waitArgs := fmt.Sprintf(`{"terminal_id":%q,"timeout_ms":2000}`, terminalID)
				writeAgentLoopNamedToolCallStream(t, w, "chatcmpl-agent-loop-terminal-2", "call-terminal-wait", "terminal_wait", waitArgs, "I will wait for it.")
			default:
				writeAgentLoopTerminalFinalAnswerStream(t, w)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		switch callNumber {
		case 1:
			fmt.Fprintf(w, `{"id":"chatcmpl-agent-loop-terminal-1","object":"chat.completion","created":1700000000,"model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"I will open a terminal.","tool_calls":[{"id":"call-terminal-open","type":"function","function":{"name":"terminal_open","arguments":%q}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":5,"total_tokens":10}}`, agentLoopE2EModel, openArgs)
		case 2:
			terminalID := terminalIDFromToolResult(body, "call-terminal-open")
			waitArgs := fmt.Sprintf(`{"terminal_id":%q,"timeout_ms":2000}`, terminalID)
			fmt.Fprintf(w, `{"id":"chatcmpl-agent-loop-terminal-2","object":"chat.completion","created":1700000001,"model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"I will wait for it.","tool_calls":[{"id":"call-terminal-wait","type":"function","function":{"name":"terminal_wait","arguments":%q}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":5,"total_tokens":10}}`, agentLoopE2EModel, waitArgs)
		default:
			fmt.Fprintf(w, `{"id":"chatcmpl-agent-loop-terminal-3","object":"chat.completion","created":1700000002,"model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"Terminal tools completed."},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":5,"total_tokens":10}}`, agentLoopE2EModel)
		}
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL, captured
}

func fakeAgentLoopWebSearchUpstream(t *testing.T) (string, *capturedRequests) {
	t.Helper()
	captured := &capturedRequests{}
	var chatCalls atomic.Int32
	searchArgs := `{"query":"hecate agent runtime","count":2}`

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
				writeAgentLoopNamedToolCallStream(t, w, "chatcmpl-agent-loop-search-1", "call-web-search-e2e", "web_search", searchArgs, "I will search.")
			} else {
				writeAgentLoopWebSearchFinalAnswerStream(t, w)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if callNumber == 1 {
			fmt.Fprintf(w, `{"id":"chatcmpl-agent-loop-search-1","object":"chat.completion","created":1700000000,"model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"I will search.","tool_calls":[{"id":"call-web-search-e2e","type":"function","function":{"name":"web_search","arguments":%q}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":5,"total_tokens":10}}`, agentLoopE2EModel, searchArgs)
			return
		}
		fmt.Fprintf(w, `{"id":"chatcmpl-agent-loop-search-2","object":"chat.completion","created":1700000001,"model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"Web search completed."},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":5,"total_tokens":10}}`, agentLoopE2EModel)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL, captured
}

func fakeBraveSearchEndpoint(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if got := r.Header.Get("X-Subscription-Token"); got != "search-token" {
			t.Errorf("X-Subscription-Token = %q, want search-token", got)
		}
		if got := r.URL.Query().Get("q"); got != "hecate agent runtime" {
			t.Errorf("q = %q, want hecate agent runtime", got)
		}
		if got := r.URL.Query().Get("count"); got != "2" {
			t.Errorf("count = %q, want 2", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"query": {"original": "hecate agent runtime", "more_results_available": true},
			"web": {"results": [{
				"title": "Hecate agent runtime",
				"url": "https://example.test/hecate-runtime",
				"description": "Runtime docs for Hecate agents.",
				"extra_snippets": ["Tools, approvals, and web search."],
				"age": "June 2026",
				"language": "en"
			}]}
		}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL + "/search", &calls
}

func writeAgentLoopToolCallStream(t *testing.T, w http.ResponseWriter, shellArgs string) {
	t.Helper()
	writeAgentLoopNamedToolCallStream(t, w, "chatcmpl-agent-loop-1", "call-shell-e2e", "shell_exec", shellArgs, "I will inspect.")
}

func writeAgentLoopNamedToolCallStream(t *testing.T, w http.ResponseWriter, completionID, toolCallID, toolName, toolArgs, content string) {
	t.Helper()
	writeOpenAIStreamChunk(t, w, map[string]any{
		"id":      completionID,
		"object":  "chat.completion.chunk",
		"created": 1700000000,
		"model":   agentLoopE2EModel,
		"choices": []map[string]any{{
			"index": 0,
			"delta": map[string]any{
				"role":    "assistant",
				"content": content,
			},
			"finish_reason": nil,
		}},
	})
	writeOpenAIStreamChunk(t, w, map[string]any{
		"id":      completionID,
		"object":  "chat.completion.chunk",
		"created": 1700000000,
		"model":   agentLoopE2EModel,
		"choices": []map[string]any{{
			"index": 0,
			"delta": map[string]any{
				"tool_calls": []map[string]any{{
					"index": 0,
					"id":    toolCallID,
					"type":  "function",
					"function": map[string]any{
						"name":      toolName,
						"arguments": toolArgs,
					},
				}},
			},
			"finish_reason": nil,
		}},
	})
	writeOpenAIStreamChunk(t, w, map[string]any{
		"id":      completionID,
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

func writeAgentLoopTerminalFinalAnswerStream(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	writeOpenAIStreamChunk(t, w, map[string]any{
		"id":      "chatcmpl-agent-loop-terminal-3",
		"object":  "chat.completion.chunk",
		"created": 1700000002,
		"model":   agentLoopE2EModel,
		"choices": []map[string]any{{
			"index": 0,
			"delta": map[string]any{
				"role":    "assistant",
				"content": "Terminal tools completed.",
			},
			"finish_reason": nil,
		}},
	})
	writeOpenAIStreamChunk(t, w, map[string]any{
		"id":      "chatcmpl-agent-loop-terminal-3",
		"object":  "chat.completion.chunk",
		"created": 1700000002,
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

func writeAgentLoopWebSearchFinalAnswerStream(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	writeOpenAIStreamChunk(t, w, map[string]any{
		"id":      "chatcmpl-agent-loop-search-2",
		"object":  "chat.completion.chunk",
		"created": 1700000001,
		"model":   agentLoopE2EModel,
		"choices": []map[string]any{{
			"index": 0,
			"delta": map[string]any{
				"role":    "assistant",
				"content": "Web search completed.",
			},
			"finish_reason": nil,
		}},
	})
	writeOpenAIStreamChunk(t, w, map[string]any{
		"id":      "chatcmpl-agent-loop-search-2",
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

func assertE2ENumber(t *testing.T, data map[string]any, key string, want float64) {
	t.Helper()
	got, ok := data[key].(float64)
	if !ok {
		t.Fatalf("%s = %T(%v), want number %v", key, data[key], data[key], want)
	}
	if got != want {
		t.Fatalf("%s = %v, want %v", key, got, want)
	}
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

func terminalIDFromToolResult(body map[string]any, toolCallID string) string {
	content := toolResultContent(body, toolCallID)
	for _, field := range strings.Fields(content) {
		if strings.HasPrefix(field, "terminal_id=") {
			return strings.TrimPrefix(field, "terminal_id=")
		}
	}
	return "missing-terminal-id"
}

func toolResultContent(body map[string]any, toolCallID string) string {
	messages, ok := body["messages"].([]any)
	if !ok {
		return ""
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
		return content
	}
	return ""
}
