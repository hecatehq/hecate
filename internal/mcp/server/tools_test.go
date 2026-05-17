package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeGateway spins up an in-process HTTP server with the routes the
// MCP tools call. Test bodies install handlers per-route via the
// handlers map; unmocked routes 404.
func fakeGateway(t *testing.T, handlers map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, h := range handlers {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			h(w, r)
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestTool_ListTasks_FormatsRows(t *testing.T) {
	srv := fakeGateway(t, map[string]http.HandlerFunc{
		"/hecate/v1/tasks": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("limit") != "5" {
				t.Errorf("limit query = %q, want 5", r.URL.Query().Get("limit"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[
				{"id":"task-abc12345","title":"List wd","status":"completed","execution_kind":"shell","step_count":2,"latest_run_id":"run-fedc0987"},
				{"id":"task-xyz98765","title":"","prompt":"echo hi","status":"running","execution_kind":"shell","step_count":1}
			]}`))
		},
	})
	client := NewGatewayClient(srv.URL)
	server := NewServer("hecate-test", "0.0.0")
	RegisterDefaultTools(server, client)

	args := json.RawMessage(`{"limit":5}`)
	handler := registeredToolFor(t, server, "list_tasks")
	result, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("list_tasks: %v", err)
	}
	if len(result.Content) == 0 {
		t.Fatal("no content blocks")
	}
	body := result.Content[0].Text
	if !strings.Contains(body, "Found 2 task(s)") {
		t.Errorf("want count header, got: %s", body)
	}
	if !strings.Contains(body, "task-abc") {
		t.Errorf("want short id task-abc, got: %s", body)
	}
	if !strings.Contains(body, "List wd") {
		t.Errorf("want title 'List wd', got: %s", body)
	}
	// Empty title falls back to prompt.
	if !strings.Contains(body, "echo hi") {
		t.Errorf("want prompt fallback 'echo hi', got: %s", body)
	}
}

func TestTool_ListTasks_EmptyState(t *testing.T) {
	srv := fakeGateway(t, map[string]http.HandlerFunc{
		"/hecate/v1/tasks": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[]}`))
		},
	})
	server := NewServer("t", "0")
	RegisterDefaultTools(server, NewGatewayClient(srv.URL))
	result, err := registeredToolFor(t, server, "list_tasks")(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(result.Content[0].Text, "No tasks yet") {
		t.Errorf("want empty state, got: %s", result.Content[0].Text)
	}
}

func TestTool_GetTaskStatus_RequiresID(t *testing.T) {
	server := NewServer("t", "0")
	RegisterDefaultTools(server, NewGatewayClient("http://unused"))
	_, err := registeredToolFor(t, server, "get_task_status")(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "task_id is required") {
		t.Fatalf("want task_id required error, got: %v", err)
	}
}

func TestTool_GetTaskStatus_FormatsDetail(t *testing.T) {
	srv := fakeGateway(t, map[string]http.HandlerFunc{
		"/hecate/v1/tasks/abc-123": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"id":"abc-123","title":"Run db migration","status":"completed","execution_kind":"shell","shell_command":"./migrate.sh","step_count":3,"latest_run_id":"run-1","created_at":"2026-04-22T10:00:00Z"}}`))
		},
	})
	server := NewServer("t", "0")
	RegisterDefaultTools(server, NewGatewayClient(srv.URL))
	result, err := registeredToolFor(t, server, "get_task_status")(context.Background(),
		json.RawMessage(`{"task_id":"abc-123"}`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	body := result.Content[0].Text
	for _, want := range []string{"abc-123", "Run db migration", "completed", "shell", "./migrate.sh", "Steps: 3"} {
		if !strings.Contains(body, want) {
			t.Errorf("want %q in output, got: %s", want, body)
		}
	}
}

func TestTool_SummarizeTraffic_AggregatesByProvider(t *testing.T) {
	srv := fakeGateway(t, map[string]http.HandlerFunc{
		"/hecate/v1/traces": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[
				{"request_id":"r1","provider":"openai","duration_ms":120,"status_code":"ok","total_tokens":150,"cost_usd":0.001},
				{"request_id":"r2","provider":"openai","duration_ms":80,"status_code":"ok","total_tokens":100,"cost_usd":0.0008},
				{"request_id":"r3","provider":"anthropic","duration_ms":300,"status_code":"error"}
			]}`))
		},
	})
	server := NewServer("t", "0")
	RegisterDefaultTools(server, NewGatewayClient(srv.URL))
	result, err := registeredToolFor(t, server, "summarize_recent_traffic")(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	body := result.Content[0].Text
	for _, want := range []string{"3 requests", "openai: 2 req", "anthropic: 1 req", "1 errors"} {
		if !strings.Contains(body, want) {
			t.Errorf("want %q in output, got: %s", want, body)
		}
	}
}

// ─── create_task ─────────────────────────────────────────────────────

func TestTool_CreateTask_PostsAgentLoopAndSummarizes(t *testing.T) {
	srv := fakeGateway(t, map[string]http.HandlerFunc{
		"/hecate/v1/tasks": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("method = %s, want POST", r.Method)
			}
			var body createTaskWireRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			// Execution kind is hardcoded — the MCP tool only
			// creates agent_loop tasks (deliberate scope cap).
			if body.ExecutionKind != "agent_loop" {
				t.Errorf("execution_kind = %q, want agent_loop", body.ExecutionKind)
			}
			if body.Prompt != "summarize the working dir" {
				t.Errorf("prompt = %q, want passthrough", body.Prompt)
			}
			if body.RequestedModel != "claude-opus-4-5" {
				t.Errorf("requested_model = %q, want passthrough", body.RequestedModel)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"id":"task-new1","status":"queued","execution_kind":"agent_loop","latest_run_id":"run-first1"}}`))
		},
	})
	server := NewServer("t", "0")
	RegisterDefaultTools(server, NewGatewayClient(srv.URL))

	args := json.RawMessage(`{"prompt":"summarize the working dir","title":"Summarize","requested_model":"claude-opus-4-5"}`)
	result, err := registeredToolFor(t, server, "create_task")(context.Background(), args)
	if err != nil {
		t.Fatalf("create_task: %v", err)
	}
	body := result.Content[0].Text
	for _, want := range []string{"task-new1", "agent_loop", "queued", "run-first1", "get_task_status"} {
		if !strings.Contains(body, want) {
			t.Errorf("output missing %q; got: %s", want, body)
		}
	}
}

func TestTool_CreateTask_RequiresPrompt(t *testing.T) {
	server := NewServer("t", "0")
	RegisterDefaultTools(server, NewGatewayClient("http://unused"))
	_, err := registeredToolFor(t, server, "create_task")(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "prompt is required") {
		t.Fatalf("want prompt-required error, got: %v", err)
	}
}

func TestTool_CreateTask_RequiresWorkingDirectoryForInPlaceMode(t *testing.T) {
	// in_place workspace runs the agent directly in the named
	// directory. Without working_directory the gateway would 400 us;
	// catch it locally so the operator gets a clear error inside the
	// editor instead of a "400 invalid_request_error" passthrough.
	server := NewServer("t", "0")
	RegisterDefaultTools(server, NewGatewayClient("http://unused"))
	_, err := registeredToolFor(t, server, "create_task")(context.Background(),
		json.RawMessage(`{"prompt":"do the thing","workspace_mode":"in_place"}`))
	if err == nil || !strings.Contains(err.Error(), "working_directory is required") {
		t.Fatalf("want working_directory-required error for in_place mode, got: %v", err)
	}
}

// ─── resolve_approval ────────────────────────────────────────────────

func TestTool_ResolveApproval_ApprovePostsDecision(t *testing.T) {
	srv := fakeGateway(t, map[string]http.HandlerFunc{
		"/hecate/v1/tasks/task-1/approvals/appr-9/resolve": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("method = %s, want POST", r.Method)
			}
			var body resolveApprovalWireRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body.Decision != "approve" {
				t.Errorf("decision = %q, want approve", body.Decision)
			}
			if body.Note != "looks safe" {
				t.Errorf("note = %q, want passthrough", body.Note)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"id":"appr-9","status":"approved","kind":"agent_loop_tool_call"}}`))
		},
	})
	server := NewServer("t", "0")
	RegisterDefaultTools(server, NewGatewayClient(srv.URL))

	args := json.RawMessage(`{"task_id":"task-1","approval_id":"appr-9","decision":"approve","note":"looks safe"}`)
	result, err := registeredToolFor(t, server, "resolve_approval")(context.Background(), args)
	if err != nil {
		t.Fatalf("resolve_approval: %v", err)
	}
	body := result.Content[0].Text
	for _, want := range []string{"appr-9", "approved", "agent_loop_tool_call"} {
		if !strings.Contains(body, want) {
			t.Errorf("output missing %q; got: %s", want, body)
		}
	}
}

func TestTool_ResolveApproval_RejectsInvalidDecision(t *testing.T) {
	server := NewServer("t", "0")
	RegisterDefaultTools(server, NewGatewayClient("http://unused"))
	_, err := registeredToolFor(t, server, "resolve_approval")(context.Background(),
		json.RawMessage(`{"task_id":"t","approval_id":"a","decision":"maybe"}`))
	if err == nil || !strings.Contains(err.Error(), `decision must be "approve" or "reject"`) {
		t.Fatalf("want decision-validation error, got: %v", err)
	}
}

func TestTool_ResolveApproval_RequiresIDs(t *testing.T) {
	// task_id and approval_id are both URL-path components on the
	// gateway side; missing either yields a 404 there. We surface
	// the validation error from the MCP tool so the operator gets
	// the right hint without a round-trip.
	server := NewServer("t", "0")
	RegisterDefaultTools(server, NewGatewayClient("http://unused"))
	_, err := registeredToolFor(t, server, "resolve_approval")(context.Background(),
		json.RawMessage(`{"approval_id":"a","decision":"approve"}`))
	if err == nil || !strings.Contains(err.Error(), "task_id is required") {
		t.Errorf("want task_id error, got: %v", err)
	}
	_, err = registeredToolFor(t, server, "resolve_approval")(context.Background(),
		json.RawMessage(`{"task_id":"t","decision":"approve"}`))
	if err == nil || !strings.Contains(err.Error(), "approval_id is required") {
		t.Errorf("want approval_id error, got: %v", err)
	}
}

// ─── cancel_run ──────────────────────────────────────────────────────

func TestTool_CancelRun_PostsAndSummarizes(t *testing.T) {
	srv := fakeGateway(t, map[string]http.HandlerFunc{
		"/hecate/v1/tasks/task-2/runs/run-3/cancel": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("method = %s, want POST", r.Method)
			}
			// Reason must be forwarded in the request body.
			var body struct {
				Reason string `json:"reason"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode body: %v", err)
			}
			if body.Reason != "runaway loop" {
				t.Errorf("reason = %q, want %q", body.Reason, "runaway loop")
			}
			w.Header().Set("Content-Type", "application/json")
			// Gateway echoes the reason in last_error so the tool output includes it.
			_, _ = w.Write([]byte(`{"data":{"id":"run-3","task_id":"task-2","status":"cancelled","finished_at":"2026-04-29T10:00:00Z","last_error":"run cancelled: runaway loop"}}`))
		},
	})
	server := NewServer("t", "0")
	RegisterDefaultTools(server, NewGatewayClient(srv.URL))

	args := json.RawMessage(`{"task_id":"task-2","run_id":"run-3","reason":"runaway loop"}`)
	result, err := registeredToolFor(t, server, "cancel_run")(context.Background(), args)
	if err != nil {
		t.Fatalf("cancel_run: %v", err)
	}
	body := result.Content[0].Text
	for _, want := range []string{"run-3", "task-2", "cancelled", "2026-04-29T10:00:00Z", "runaway loop"} {
		if !strings.Contains(body, want) {
			t.Errorf("output missing %q; got: %s", want, body)
		}
	}
}

func TestTool_CancelRun_RequiresIDs(t *testing.T) {
	server := NewServer("t", "0")
	RegisterDefaultTools(server, NewGatewayClient("http://unused"))
	_, err := registeredToolFor(t, server, "cancel_run")(context.Background(),
		json.RawMessage(`{"run_id":"r"}`))
	if err == nil || !strings.Contains(err.Error(), "task_id is required") {
		t.Errorf("want task_id error, got: %v", err)
	}
	_, err = registeredToolFor(t, server, "cancel_run")(context.Background(),
		json.RawMessage(`{"task_id":"t"}`))
	if err == nil || !strings.Contains(err.Error(), "run_id is required") {
		t.Errorf("want run_id error, got: %v", err)
	}
}

// TestTool_WriteToolAnnotations pins the safety hints clients see.
// MCP-aware editors use these to decide whether to auto-approve a
// tool call or prompt the user. Getting them wrong in either
// direction is a UX bug: too liberal = silent destructive action;
// too conservative = friction on every read.
func TestTool_WriteToolAnnotations(t *testing.T) {
	server := NewServer("t", "0")
	RegisterDefaultTools(server, NewGatewayClient("http://unused"))

	cases := []struct {
		name              string
		wantReadOnly      bool
		wantDestructive   bool
		wantIdempotent    bool
		wantHasReadOnly   bool // whether the field is set at all
		wantHasDestruct   bool
		wantHasIdempotent bool
	}{
		// Reads — auto-approvable.
		{"list_tasks", true, false, false, true, false, false},
		{"get_task_status", true, false, false, true, false, false},
		{"summarize_recent_traffic", true, false, false, true, false, false},
		// Creates — not destructive (creates new state, doesn't
		// destroy existing); no annotations declared.
		{"create_task", false, false, false, false, false, false},
		// resolve_approval — irreversible decision, NOT idempotent
		// (re-resolving a resolved approval is a no-op on the
		// gateway today, but the semantic intent is "this is a
		// one-shot decision," so we don't promise idempotency).
		{"resolve_approval", false, true, false, false, true, false},
		// cancel_run — destructive AND idempotent (cancelling an
		// already-cancelled run is a no-op).
		{"cancel_run", false, true, true, false, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool, ok := server.tools.byName[tc.name]
			if !ok {
				t.Fatalf("tool %q not registered", tc.name)
			}
			ann := tool.descriptor.Annotations
			if !tc.wantHasReadOnly && !tc.wantHasDestruct && !tc.wantHasIdempotent {
				if ann != nil {
					t.Errorf("Annotations should be nil for %q; got %+v", tc.name, ann)
				}
				return
			}
			if ann == nil {
				t.Fatalf("Annotations is nil for %q; want hints set", tc.name)
			}
			checkBool := func(label string, ptr *bool, wantPresent, wantValue bool) {
				switch {
				case wantPresent && ptr == nil:
					t.Errorf("%s: %s missing; want %v", tc.name, label, wantValue)
				case wantPresent && *ptr != wantValue:
					t.Errorf("%s: %s = %v, want %v", tc.name, label, *ptr, wantValue)
				case !wantPresent && ptr != nil:
					t.Errorf("%s: %s set to %v but should be unset", tc.name, label, *ptr)
				}
			}
			checkBool("ReadOnlyHint", ann.ReadOnlyHint, tc.wantHasReadOnly, tc.wantReadOnly)
			checkBool("DestructiveHint", ann.DestructiveHint, tc.wantHasDestruct, tc.wantDestructive)
			checkBool("IdempotentHint", ann.IdempotentHint, tc.wantHasIdempotent, tc.wantIdempotent)
		})
	}
}

func TestTool_UpstreamError_BubblesAsToolError(t *testing.T) {
	srv := fakeGateway(t, map[string]http.HandlerFunc{
		"/hecate/v1/tasks": func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "internal", http.StatusInternalServerError)
		},
	})
	server := NewServer("t", "0")
	RegisterDefaultTools(server, NewGatewayClient(srv.URL))
	_, err := registeredToolFor(t, server, "list_tasks")(context.Background(), json.RawMessage(`{}`))
	// Tool returns the error directly; the dispatcher wraps it in
	// CallToolResult.IsError=true, but at this seam we just see the
	// error value.
	if err == nil {
		t.Fatal("expected upstream error to propagate")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("want status code in error, got: %v", err)
	}
}

// registeredToolFor pulls a registered tool's handler out of the
// server. Tests would otherwise need to drive everything through the
// stdio loop, which is excessive for tool-level assertions.
func registeredToolFor(t *testing.T, s *Server, name string) ToolHandler {
	t.Helper()
	tool, ok := s.tools.byName[name]
	if !ok {
		t.Fatalf("tool %q not registered; have: %+v", name, toolNames(s))
	}
	return tool.handler
}

func toolNames(s *Server) []string {
	out := make([]string, 0, len(s.tools.byName))
	for n := range s.tools.byName {
		out = append(out, n)
	}
	return out
}
