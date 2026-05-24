package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/workspace"
	"github.com/hecatehq/hecate/pkg/types"
)

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return string(raw)
}

// scriptedLLM returns a canned response on each call. Tests build the
// script in advance — { "turn 1 wants shell_exec(ls)", "turn 2 wants
// final answer" } — and the loop drives through it. Each call records
// what messages it received so we can assert the conversation grew
// correctly.
type scriptedLLM struct {
	responses []*types.ChatResponse
	calls     atomic.Int32
	lastReqs  []types.ChatRequest
}

func (s *scriptedLLM) Chat(ctx context.Context, req types.ChatRequest) (*types.ChatResponse, error) {
	idx := int(s.calls.Load())
	s.calls.Add(1)
	s.lastReqs = append(s.lastReqs, req)
	if idx >= len(s.responses) {
		return nil, errors.New("scriptedLLM: ran out of canned responses")
	}
	return s.responses[idx], nil
}

type streamingScriptedLLM struct {
	response      *types.ChatResponse
	chunks        []string
	chatCalls     atomic.Int32
	streamCalls   atomic.Int32
	lastStreamReq types.ChatRequest
}

func (s *streamingScriptedLLM) Chat(_ context.Context, _ types.ChatRequest) (*types.ChatResponse, error) {
	s.chatCalls.Add(1)
	return nil, errors.New("streamingScriptedLLM: non-streaming Chat should not be called")
}

func (s *streamingScriptedLLM) ChatStream(_ context.Context, req types.ChatRequest, onContentDelta func(string)) (*types.ChatResponse, error) {
	s.streamCalls.Add(1)
	s.lastStreamReq = req
	for _, chunk := range s.chunks {
		onContentDelta(chunk)
	}
	return s.response, nil
}

// erroringLLM returns the same canned error on every call. Used by
// failure-path tests that need to assert the agent loop's wrapping
// of upstream provider errors.
type erroringLLM struct {
	err error
}

func (e *erroringLLM) Chat(_ context.Context, _ types.ChatRequest) (*types.ChatResponse, error) {
	return nil, e.err
}

type firstResponseThenErrorLLM struct {
	first *types.ChatResponse
	err   error
	calls atomic.Int32
}

func (e *firstResponseThenErrorLLM) Chat(_ context.Context, _ types.ChatRequest) (*types.ChatResponse, error) {
	idx := e.calls.Add(1)
	if idx == 1 {
		return e.first, nil
	}
	return nil, e.err
}

// stubExecutor records what task it was asked to run and returns a
// canned ExecutionResult. Saves us from spinning up a real shell
// sandbox in unit tests.
type stubExecutor struct {
	calls  []types.Task
	result *ExecutionResult
}

func (s *stubExecutor) Execute(_ context.Context, spec ExecutionSpec) (*ExecutionResult, error) {
	s.calls = append(s.calls, spec.Task)
	if s.result != nil {
		return s.result, nil
	}
	return &ExecutionResult{Status: "completed"}, nil
}

func makeAssistantMsg(content string, calls ...types.ToolCall) types.Message {
	return types.Message{Role: "assistant", Content: content, ToolCalls: calls}
}

func makeChatResp(msg types.Message) *types.ChatResponse {
	return &types.ChatResponse{
		Choices: []types.ChatChoice{{Message: msg, FinishReason: "stop"}},
	}
}

func withResolvedRoute(resp *types.ChatResponse) *types.ChatResponse {
	resp.Model = "ministral-3:latest"
	resp.Route = types.RouteDecision{
		Provider:     "ollama",
		ProviderKind: "local",
		Model:        "ministral-3:latest",
		Reason:       "selected",
	}
	return resp
}

func assertResolvedRoute(t *testing.T, res *ExecutionResult) {
	t.Helper()
	if res.Provider != "ollama" {
		t.Errorf("Provider = %q, want ollama", res.Provider)
	}
	if res.ProviderKind != "local" {
		t.Errorf("ProviderKind = %q, want local", res.ProviderKind)
	}
	if res.Model != "ministral-3:latest" {
		t.Errorf("Model = %q, want ministral-3:latest", res.Model)
	}
}

func newAgentLoopSpec(t *testing.T) ExecutionSpec {
	t.Helper()
	var counter atomic.Int32
	return ExecutionSpec{
		Task: types.Task{
			ID:     "task-1",
			Prompt: "summarize the working directory",
		},
		Run: types.TaskRun{
			ID:    "run-1",
			Model: "gpt-4o-mini",
		},
		StartedAt: time.Now().UTC(),
		NewID: func(prefix string) string {
			counter.Add(1)
			return fmt.Sprintf("%s-%d", prefix, counter.Load())
		},
		UpsertStep:     func(types.TaskStep) error { return nil },
		UpsertArtifact: func(types.TaskArtifact) error { return nil },
	}
}

func TestAgentLoop_FinalAnswerOnFirstTurn(t *testing.T) {
	// Simplest happy path: assistant answers immediately, no tool
	// calls. Loop should produce one thinking step + one final-answer
	// artifact and return completed.
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("The working directory contains a README.")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	res, err := loop.Execute(context.Background(), newAgentLoopSpec(t))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "completed" {
		t.Fatalf("Status = %q, want completed", res.Status)
	}
	if len(res.Steps) != 1 {
		t.Errorf("Steps = %d, want 1 (just the thinking step)", len(res.Steps))
	}
	// Two artifacts now: the conversation snapshot (persisted every
	// turn for resume) and the final-answer summary.
	finalAnswer := findArtifactByKind(res.Artifacts, "summary")
	if finalAnswer == nil {
		t.Fatalf("no summary artifact; got: %+v", res.Artifacts)
	}
	if finalAnswer.Name != "agent-final-answer.txt" || !strings.Contains(finalAnswer.ContentText, "README") {
		t.Errorf("final answer artifact wrong: %+v", finalAnswer)
	}
	convo := findArtifactByKind(res.Artifacts, "agent_conversation")
	if convo == nil {
		t.Fatalf("no agent_conversation artifact persisted; got: %+v", res.Artifacts)
	}
}

func TestAgentLoop_StreamingLLMPersistsPartialConversation(t *testing.T) {
	llm := &streamingScriptedLLM{
		chunks: []string{"This is a streaming answer that arrives before completion."},
		response: makeChatResp(makeAssistantMsg(
			"This is a streaming answer that arrives before completion.",
		)),
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	var conversationUpserts []types.TaskArtifact
	spec.UpsertArtifact = func(artifact types.TaskArtifact) error {
		if artifact.Kind == "agent_conversation" {
			conversationUpserts = append(conversationUpserts, artifact)
		}
		return nil
	}

	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "completed" {
		t.Fatalf("status = %q, want completed", res.Status)
	}
	if llm.streamCalls.Load() != 1 || llm.chatCalls.Load() != 0 {
		t.Fatalf("llm calls = stream %d chat %d, want stream-only", llm.streamCalls.Load(), llm.chatCalls.Load())
	}
	if len(conversationUpserts) < 2 {
		t.Fatalf("conversation upserts = %d, want streaming partial + final snapshot: %+v", len(conversationUpserts), conversationUpserts)
	}
	if !strings.Contains(conversationUpserts[0].ContentText, "streaming answer") {
		t.Fatalf("first conversation upsert did not contain streamed assistant text:\n%s", conversationUpserts[0].ContentText)
	}
	if llm.lastStreamReq.Model != "gpt-4o-mini" || len(llm.lastStreamReq.Tools) == 0 {
		t.Fatalf("stream request = model %q tools %d, want agent loop model+tools", llm.lastStreamReq.Model, len(llm.lastStreamReq.Tools))
	}
}

func TestAgentLoop_ToolCallThenAnswer(t *testing.T) {
	// Realistic two-turn flow: LLM calls shell_exec, gets the result,
	// then produces a final answer. Asserts the dispatched task
	// carries the right command and that the second LLM request sees
	// the tool result in its conversation history.
	shell := &stubExecutor{
		result: &ExecutionResult{
			Status: "completed",
			Artifacts: []types.TaskArtifact{
				{Kind: "stdout", Name: "stdout.txt", ContentText: "README.md\nmain.go\n"},
			},
		},
	}
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID:   "call-1",
				Type: "function",
				Function: types.ToolCallFunction{
					Name:      "shell_exec",
					Arguments: `{"command":"ls"}`,
				},
			})),
			makeChatResp(makeAssistantMsg("Two files: README.md and main.go.")),
		},
	}
	loop := NewAgentLoopExecutor(llm, shell, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	res, err := loop.Execute(context.Background(), newAgentLoopSpec(t))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "completed" {
		t.Fatalf("Status = %q, want completed", res.Status)
	}
	if len(shell.calls) != 1 || shell.calls[0].ShellCommand != "ls" {
		t.Errorf("shell tool calls: %+v, want one call with command='ls'", shell.calls)
	}
	// Steps: thinking-1 + tool-1 + thinking-2 = 3
	if len(res.Steps) != 3 {
		t.Errorf("Steps = %d, want 3 (thinking + tool + thinking)", len(res.Steps))
	}
	// Second LLM request must have seen the tool result.
	if len(llm.lastReqs) != 2 {
		t.Fatalf("LLM call count = %d, want 2", len(llm.lastReqs))
	}
	secondReq := llm.lastReqs[1]
	foundToolMsg := false
	for _, m := range secondReq.Messages {
		if m.Role == "tool" && m.ToolCallID == "call-1" && strings.Contains(m.Content, "README.md") {
			foundToolMsg = true
		}
	}
	if !foundToolMsg {
		t.Errorf("second LLM request missing tool-role message; got: %+v", secondReq.Messages)
	}
}

func TestAgentLoop_EmitsCandidateCoreAssistantEvents(t *testing.T) {
	shell := &stubExecutor{
		result: &ExecutionResult{
			Status: "completed",
			Artifacts: []types.TaskArtifact{
				{Kind: "stdout", Name: "stdout.txt", ContentText: "README.md\n"},
			},
		},
	}
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("I'll inspect the files.", types.ToolCall{
				ID:   "call-1",
				Type: "function",
				Function: types.ToolCallFunction{
					Name:      "shell_exec",
					Arguments: `{"command":"ls"}`,
				},
			})),
			makeChatResp(makeAssistantMsg("The workspace contains README.md.")),
		},
	}
	cap := &captureRunEvent{}
	spec := newAgentLoopSpec(t)
	spec.EmitRunEvent = cap.emit
	loop := NewAgentLoopExecutor(llm, shell, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})

	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "completed" {
		t.Fatalf("Status = %q, want completed", res.Status)
	}

	cap.mu.Lock()
	events := append([]capturedEvent(nil), cap.events...)
	cap.mu.Unlock()
	wantTypes := []string{
		"turn.started",
		"assistant.text_complete",
		"assistant.tool_call_proposed",
		"turn.started",
		"assistant.text_complete",
		"assistant.final_answer",
	}
	if len(events) != len(wantTypes) {
		t.Fatalf("event count = %d, want %d: %+v", len(events), len(wantTypes), events)
	}
	for i, want := range wantTypes {
		if events[i].Type != want {
			t.Fatalf("events[%d].Type = %q, want %q", i, events[i].Type, want)
		}
		assertAgentLoopEventContract(t, events[i])
	}

	if got := len(cap.byType("turn.started")); got != 2 {
		t.Fatalf("turn.started count = %d, want 2", got)
	}
	textEvents := cap.byType("assistant.text_complete")
	if len(textEvents) != 2 {
		t.Fatalf("assistant.text_complete count = %d, want 2", len(textEvents))
	}
	if textEvents[0].Data["text"] != "I'll inspect the files." {
		t.Fatalf("first text_complete text = %v", textEvents[0].Data["text"])
	}
	proposed := cap.byType("assistant.tool_call_proposed")
	if len(proposed) != 1 {
		t.Fatalf("assistant.tool_call_proposed count = %d, want 1", len(proposed))
	}
	if proposed[0].Data["tool_call_id"] != "call-1" || proposed[0].Data["tool_name"] != "shell_exec" {
		t.Fatalf("tool proposal data = %+v", proposed[0].Data)
	}
	input, ok := proposed[0].Data["input"].(map[string]any)
	if !ok {
		t.Fatalf("tool proposal input = %T, want map", proposed[0].Data["input"])
	}
	if input["command"] != "ls" {
		t.Fatalf("tool proposal command = %v, want ls", input["command"])
	}
	finals := cap.byType("assistant.final_answer")
	if len(finals) != 1 {
		t.Fatalf("assistant.final_answer count = %d, want 1", len(finals))
	}
	if finals[0].Data["summary"] != "The workspace contains README.md." {
		t.Fatalf("final answer summary = %v", finals[0].Data["summary"])
	}
}

func assertAgentLoopEventContract(t *testing.T, event capturedEvent) {
	t.Helper()
	required := map[string][]string{
		"turn.started": {
			"turn_index",
			"model",
			"provider",
			"input_tokens_estimate",
		},
		"assistant.text_complete": {
			"turn_index",
			"block_index",
			"text",
		},
		"assistant.tool_call_proposed": {
			"turn_index",
			"tool_call_id",
			"tool_name",
			"input",
		},
		"assistant.final_answer": {
			"turn_index",
			"summary",
		},
	}
	for _, key := range required[event.Type] {
		if _, ok := event.Data[key]; !ok {
			t.Fatalf("%s missing required data key %q: %+v", event.Type, key, event.Data)
		}
	}
	for _, legacyKey := range []string{"turn", "tool_call_count"} {
		if _, ok := event.Data[legacyKey]; ok {
			t.Fatalf("%s carried legacy data key %q: %+v", event.Type, legacyKey, event.Data)
		}
	}
}

func TestAgentLoop_MaxTurnsHonored(t *testing.T) {
	// LLM keeps asking for tool calls forever; loop must stop at
	// maxTurns and return failed status. Without this cap a runaway
	// agent could exhaust the model budget.
	loopingResponse := makeChatResp(makeAssistantMsg("", types.ToolCall{
		ID: "call-x", Type: "function",
		Function: types.ToolCallFunction{Name: "shell_exec", Arguments: `{"command":"ls"}`},
	}))
	llm := &scriptedLLM{}
	for i := 0; i < 20; i++ {
		llm.responses = append(llm.responses, loopingResponse)
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 3, nil, HTTPRequestPolicy{})
	res, err := loop.Execute(context.Background(), newAgentLoopSpec(t))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "failed" {
		t.Errorf("Status = %q, want failed (max turns)", res.Status)
	}
	if !strings.Contains(res.LastError, "maxTurns=3") {
		t.Errorf("LastError = %q, want mention of maxTurns=3", res.LastError)
	}
	if got := llm.calls.Load(); got != 3 {
		t.Errorf("LLM calls = %d, want 3 (capped)", got)
	}
}

func TestAgentLoop_LLMErrorBubbles(t *testing.T) {
	// LLM call fails → loop produces a "failed" step and returns
	// failed status. The error message must reach the run output so
	// the operator can diagnose.
	llm := &scriptedLLM{} // empty responses → returns error on first call
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	res, err := loop.Execute(context.Background(), newAgentLoopSpec(t))
	if err != nil {
		t.Fatalf("Execute (should not return Go-level error): %v", err)
	}
	if res.Status != "failed" {
		t.Errorf("Status = %q, want failed", res.Status)
	}
	if !strings.Contains(res.LastError, "LLM call failed") {
		t.Errorf("LastError = %q, want 'LLM call failed'", res.LastError)
	}
}

func TestAgentLoop_NoLLM_FailsWithActionableError(t *testing.T) {
	// agent_loop without an LLM is a misconfiguration, not a use case.
	// The loop must surface a clear error so the operator knows to
	// wire a model rather than seeing a confusing silent success.
	loop := NewAgentLoopExecutor(nil, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	res, err := loop.Execute(context.Background(), newAgentLoopSpec(t))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "failed" {
		t.Errorf("Status = %q, want failed", res.Status)
	}
	if !strings.Contains(res.LastError, "requires an LLM") {
		t.Errorf("LastError = %q, want mention of 'requires an LLM'", res.LastError)
	}
}

func TestAgentLoop_BadToolArgsBecomeToolError(t *testing.T) {
	// Malformed tool arguments must NOT crash the loop or become a
	// Go error — the LLM should see the parse error as its tool
	// result and decide what to do. Then on the next turn we provide
	// a valid answer to terminate the loop.
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "call-1", Type: "function",
				Function: types.ToolCallFunction{Name: "shell_exec", Arguments: `not json`},
			})),
			makeChatResp(makeAssistantMsg("I gave up.")),
		},
	}
	shell := &stubExecutor{}
	loop := NewAgentLoopExecutor(llm, shell, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	res, err := loop.Execute(context.Background(), newAgentLoopSpec(t))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "completed" {
		t.Errorf("Status = %q, want completed", res.Status)
	}
	// The shell executor should NOT have been called — args were
	// invalid, the dispatcher returned an error string instead of
	// running the tool.
	if len(shell.calls) != 0 {
		t.Errorf("shell tool was called despite bad args: %+v", shell.calls)
	}
	// The second LLM request should have a tool-role message
	// describing the parse failure.
	secondReq := llm.lastReqs[1]
	hasParseError := false
	for _, m := range secondReq.Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "invalid arguments") {
			hasParseError = true
		}
	}
	if !hasParseError {
		t.Errorf("expected parse-error tool message in conversation; got: %+v", secondReq.Messages)
	}
}

func TestAgentLoop_UnknownToolBecomesToolError(t *testing.T) {
	// LLM hallucinates a tool name; loop must report it as a tool
	// failure rather than crashing the run.
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "call-1", Type: "function",
				Function: types.ToolCallFunction{Name: "fly_to_moon", Arguments: `{}`},
			})),
			makeChatResp(makeAssistantMsg("Sorry, I can't.")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	res, err := loop.Execute(context.Background(), newAgentLoopSpec(t))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "completed" {
		t.Errorf("Status = %q, want completed", res.Status)
	}
	// Tool message must carry the "unknown tool" hint AND be flagged
	// as ToolError so Anthropic-bound providers can emit
	// is_error=true on the wire (without it, the model only sees
	// error context as free-form text and may not reliably
	// distinguish failures from successful results).
	secondReq := llm.lastReqs[1]
	hasUnknown := false
	hasErrorFlag := false
	for _, m := range secondReq.Messages {
		if m.Role != "tool" {
			continue
		}
		if strings.Contains(m.Content, "unknown tool") {
			hasUnknown = true
		}
		if m.ToolError {
			hasErrorFlag = true
		}
	}
	if !hasUnknown {
		t.Errorf("expected unknown-tool tool message; got: %+v", secondReq.Messages)
	}
	if !hasErrorFlag {
		t.Errorf("expected ToolError=true on the failed tool message; got: %+v", secondReq.Messages)
	}
}

func TestAgentLoop_ReadFileTool(t *testing.T) {
	// Happy path: agent calls read_file on a workspace file. Loop
	// reads the file inline (no FileExecutor, no shell), surfaces
	// the content as the tool result, and the next LLM turn answers.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "c1", Type: "function",
				Function: types.ToolCallFunction{Name: "read_file", Arguments: `{"path":"hello.txt"}`},
			})),
			makeChatResp(makeAssistantMsg("It says: hello world.")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir
	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "completed" {
		t.Fatalf("Status = %q, want completed", res.Status)
	}
	// Second LLM request must have seen the file contents in a tool message.
	secondReq := llm.lastReqs[1]
	hasContent := false
	for _, m := range secondReq.Messages {
		if m.Role == "tool" && m.ToolCallID == "c1" && strings.Contains(m.Content, "hello world") {
			hasContent = true
		}
	}
	if !hasContent {
		t.Errorf("read_file content didn't surface to next LLM turn: %+v", secondReq.Messages)
	}
}

func TestAgentLoop_ReadFileRejectsTraversal(t *testing.T) {
	// Path traversal must fail safely — the agent can't ../ out of
	// its workspace. The tool returns an error string the LLM sees,
	// and the file system isn't touched.
	dir := t.TempDir()
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "c1", Type: "function",
				Function: types.ToolCallFunction{Name: "read_file", Arguments: `{"path":"../../etc/passwd"}`},
			})),
			makeChatResp(makeAssistantMsg("Sorry, can't.")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir
	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "completed" {
		t.Fatalf("Status = %q, want completed", res.Status)
	}
	secondReq := llm.lastReqs[1]
	hasEscape := false
	for _, m := range secondReq.Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "escapes the workspace root") {
			hasEscape = true
		}
	}
	if !hasEscape {
		t.Errorf("traversal not rejected with workspace-escape error: %+v", secondReq.Messages)
	}
}

func TestAgentLoop_ReadFileBinaryDetection(t *testing.T) {
	// A binary file (NUL bytes) must be reported, not dumped. Lets
	// the LLM know the file exists without polluting the conversation
	// with raw bytes that just waste tokens.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "blob.bin"), []byte{0x00, 0x01, 0x02, 0xff, 0xfe}, 0o644); err != nil {
		t.Fatal(err)
	}
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "c1", Type: "function",
				Function: types.ToolCallFunction{Name: "read_file", Arguments: `{"path":"blob.bin"}`},
			})),
			makeChatResp(makeAssistantMsg("It's binary.")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir
	if _, err := loop.Execute(context.Background(), spec); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	hasBinary := false
	for _, m := range llm.lastReqs[1].Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "binary file") {
			hasBinary = true
		}
	}
	if !hasBinary {
		t.Errorf("binary file not flagged: %+v", llm.lastReqs[1].Messages)
	}
}

func TestAgentLoop_ReadFileLineRange(t *testing.T) {
	dir := t.TempDir()
	content := "one\ntwo\nthree\nfour\n"
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "c1", Type: "function",
				Function: types.ToolCallFunction{Name: "read_file", Arguments: `{"path":"notes.txt","start_line":2,"end_line":3}`},
			})),
			makeChatResp(makeAssistantMsg("Read the range.")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir
	if _, err := loop.Execute(context.Background(), spec); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var toolResult string
	for _, m := range llm.lastReqs[1].Messages {
		if m.Role == "tool" && m.ToolCallID == "c1" {
			toolResult = m.Content
		}
	}
	if !strings.Contains(toolResult, "lines=2-3") || !strings.Contains(toolResult, "two\nthree\n") {
		t.Fatalf("range content missing from tool result: %q", toolResult)
	}
	if strings.Contains(toolResult, "one\n") || strings.Contains(toolResult, "four\n") {
		t.Fatalf("range included out-of-range content: %q", toolResult)
	}
}

func TestAgentLoop_ReadFileLineRangeScansBeyondMaxBytes(t *testing.T) {
	dir := t.TempDir()
	var content strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&content, "line-%02d\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte(content.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "c1", Type: "function",
				Function: types.ToolCallFunction{Name: "read_file", Arguments: `{"path":"notes.txt","start_line":20,"end_line":21,"max_bytes":64}`},
			})),
			makeChatResp(makeAssistantMsg("Read the range.")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir
	if _, err := loop.Execute(context.Background(), spec); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var toolResult string
	for _, m := range llm.lastReqs[1].Messages {
		if m.Role == "tool" && m.ToolCallID == "c1" {
			toolResult = m.Content
		}
	}
	if !strings.Contains(toolResult, "lines=20-21") || !strings.Contains(toolResult, "line-20\nline-21\n") {
		t.Fatalf("range content missing from tool result: %q", toolResult)
	}
	if strings.Contains(toolResult, "line-01") {
		t.Fatalf("range was selected from the initial max_bytes window instead of the full file: %q", toolResult)
	}
}

func TestAgentLoop_ReadFileLineRangeDoesNotExposePhantomTrailingLine(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "c1", Type: "function",
				Function: types.ToolCallFunction{Name: "read_file", Arguments: `{"path":"notes.txt","start_line":3,"end_line":3}`},
			})),
			makeChatResp(makeAssistantMsg("Handled.")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir
	if _, err := loop.Execute(context.Background(), spec); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var toolResult string
	for _, m := range llm.lastReqs[1].Messages {
		if m.Role == "tool" && m.ToolCallID == "c1" {
			toolResult = m.Content
		}
	}
	if !strings.Contains(toolResult, "start_line (3) is beyond file line count (2)") {
		t.Fatalf("tool result = %q, want real out-of-range line count", toolResult)
	}
}

func TestAgentLoop_GrepToolFindsMatches(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc Target() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("Target in docs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "grep-1", Type: "function",
				Function: types.ToolCallFunction{Name: "grep", Arguments: `{"pattern":"Target","include":"*.go"}`},
			})),
			makeChatResp(makeAssistantMsg("Found it.")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir
	if _, err := loop.Execute(context.Background(), spec); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var toolResult string
	for _, m := range llm.lastReqs[1].Messages {
		if m.Role == "tool" && m.ToolCallID == "grep-1" {
			toolResult = m.Content
		}
	}
	if !strings.Contains(toolResult, "main.go:2:func Target() {}") {
		t.Fatalf("grep match missing: %s", toolResult)
	}
	if strings.Contains(toolResult, "README.md") {
		t.Fatalf("grep include filter leaked README match: %s", toolResult)
	}
}

func TestAgentLoop_GlobToolFindsWorkspacePaths(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"internal/a.go", "internal/b_test.go", "README.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "glob-1", Type: "function",
				Function: types.ToolCallFunction{Name: "glob", Arguments: `{"pattern":"internal/*.go"}`},
			})),
			makeChatResp(makeAssistantMsg("Found Go files.")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir
	if _, err := loop.Execute(context.Background(), spec); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var toolResult string
	for _, m := range llm.lastReqs[1].Messages {
		if m.Role == "tool" && m.ToolCallID == "glob-1" {
			toolResult = m.Content
		}
	}
	for _, want := range []string{"internal/a.go", "internal/b_test.go"} {
		if !strings.Contains(toolResult, want) {
			t.Fatalf("glob output missing %q: %s", want, toolResult)
		}
	}
	if strings.Contains(toolResult, "README.md") {
		t.Fatalf("glob leaked README match: %s", toolResult)
	}
}

func TestAgentLoop_ArtifactReadToolReadsTaskArtifact(t *testing.T) {
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "artifact-1", Type: "function",
				Function: types.ToolCallFunction{Name: "artifact_read", Arguments: `{"artifact_id":"artifact-123","max_bytes":11}`},
			})),
			makeChatResp(makeAssistantMsg("Read artifact.")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.GetArtifact = func(taskID, artifactID string) (types.TaskArtifact, bool, error) {
		if taskID != spec.Task.ID || artifactID != "artifact-123" {
			return types.TaskArtifact{}, false, nil
		}
		return types.TaskArtifact{
			ID:          artifactID,
			TaskID:      taskID,
			RunID:       spec.Run.ID,
			Kind:        "stdout",
			Name:        "stdout.txt",
			MimeType:    "text/plain",
			StorageKind: "inline",
			ContentText: "hello world and beyond",
			SizeBytes:   int64(len("hello world and beyond")),
			Status:      "ready",
		}, true, nil
	}
	if _, err := loop.Execute(context.Background(), spec); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var toolResult string
	for _, m := range llm.lastReqs[1].Messages {
		if m.Role == "tool" && m.ToolCallID == "artifact-1" {
			toolResult = m.Content
		}
	}
	if !strings.Contains(toolResult, "artifact_id=artifact-123") || !strings.Contains(toolResult, "hello world") {
		t.Fatalf("artifact content missing from tool result: %q", toolResult)
	}
	if strings.Contains(toolResult, "and beyond") || !strings.Contains(toolResult, "truncated=true") {
		t.Fatalf("artifact content was not capped: %q", toolResult)
	}
}

func TestAgentLoop_GitStatusToolReportsWorkspaceChanges(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "main.go")
	runGit(t, dir, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "status-1", Type: "function",
				Function: types.ToolCallFunction{Name: "git_status", Arguments: `{}`},
			})),
			makeChatResp(makeAssistantMsg("Status checked.")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir
	if _, err := loop.Execute(context.Background(), spec); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var toolResult string
	for _, m := range llm.lastReqs[1].Messages {
		if m.Role == "tool" && m.ToolCallID == "status-1" {
			toolResult = m.Content
		}
	}
	if !strings.Contains(toolResult, "main.go") || !strings.Contains(toolResult, "new.txt") {
		t.Fatalf("git status output missing changed files: %q", toolResult)
	}
	if strings.Contains(toolResult, "diff --git") {
		t.Fatalf("git_status should not include diff output: %q", toolResult)
	}
}

func TestAgentLoop_GitDiffToolReturnsBoundedDiff(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "main.go")
	runGit(t, dir, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "diff-1", Type: "function",
				Function: types.ToolCallFunction{Name: "git_diff", Arguments: `{"path":"main.go","max_bytes":4096}`},
			})),
			makeChatResp(makeAssistantMsg("Diff checked.")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir
	if _, err := loop.Execute(context.Background(), spec); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var toolResult string
	for _, m := range llm.lastReqs[1].Messages {
		if m.Role == "tool" && m.ToolCallID == "diff-1" {
			toolResult = m.Content
		}
	}
	if !strings.Contains(toolResult, "diff --git") || !strings.Contains(toolResult, "+func main() {}") {
		t.Fatalf("git diff output missing expected hunk: %q", toolResult)
	}
}

func TestAgentLoop_GitDiffToolDisablesTextconv(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")
	runGit(t, dir, "config", "diff.failtextconv.textconv", "sh -c 'echo textconv should not run >&2; exit 42'")
	if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("*.txt diff=failtextconv\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".gitattributes", "notes.txt")
	runGit(t, dir, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "diff-1", Type: "function",
				Function: types.ToolCallFunction{Name: "git_diff", Arguments: `{"path":"notes.txt","max_bytes":4096}`},
			})),
			makeChatResp(makeAssistantMsg("Diff checked.")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir
	if _, err := loop.Execute(context.Background(), spec); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var toolResult string
	for _, m := range llm.lastReqs[1].Messages {
		if m.Role == "tool" && m.ToolCallID == "diff-1" {
			toolResult = m.Content
		}
	}
	if strings.Contains(toolResult, "textconv should not run") {
		t.Fatalf("git diff invoked configured textconv: %q", toolResult)
	}
	if !strings.Contains(toolResult, "-before") || !strings.Contains(toolResult, "+after") {
		t.Fatalf("git diff output missing raw hunk: %q", toolResult)
	}
}

func TestAgentLoop_ListDirTool(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.go", "c.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "c1", Type: "function",
				Function: types.ToolCallFunction{Name: "list_dir", Arguments: `{"path":"."}`},
			})),
			makeChatResp(makeAssistantMsg("Done.")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir
	if _, err := loop.Execute(context.Background(), spec); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// The tool result must list each entry with its kind.
	var toolResult string
	for _, m := range llm.lastReqs[1].Messages {
		if m.Role == "tool" {
			toolResult = m.Content
		}
	}
	for _, want := range []string{"a.txt", "b.go", "c.md", "subdir", "dir ", "file"} {
		if !strings.Contains(toolResult, want) {
			t.Errorf("list_dir output missing %q: %s", want, toolResult)
		}
	}
	// Sorted output: a.txt < b.go < c.md (alphabetical).
	posA := strings.Index(toolResult, "a.txt")
	posB := strings.Index(toolResult, "b.go")
	posC := strings.Index(toolResult, "c.md")
	if !(posA < posB && posB < posC) {
		t.Errorf("entries not sorted: a=%d b=%d c=%d in %s", posA, posB, posC, toolResult)
	}
}

func TestAgentLoop_ListDirOnFileFailsCleanly(t *testing.T) {
	// list_dir on a regular file returns an error string, not a
	// stack trace. Lets the LLM self-correct by switching to read_file.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "c1", Type: "function",
				Function: types.ToolCallFunction{Name: "list_dir", Arguments: `{"path":"x.txt"}`},
			})),
			makeChatResp(makeAssistantMsg("OK.")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir
	if _, err := loop.Execute(context.Background(), spec); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	hasNotDir := false
	for _, m := range llm.lastReqs[1].Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "not a directory") {
			hasNotDir = true
		}
	}
	if !hasNotDir {
		t.Errorf("list_dir on a file should report 'not a directory'")
	}
}

// findArtifactByKind picks the first artifact matching kind. Multiple
// artifacts now exist per run (conversation snapshot + final-answer
// summary); tests target a specific kind rather than indexing.
func findArtifactByKind(arts []types.TaskArtifact, kind string) *types.TaskArtifact {
	for i := range arts {
		if arts[i].Kind == kind {
			return &arts[i]
		}
	}
	return nil
}

func TestAgentLoop_ConversationPersistsAcrossTurns(t *testing.T) {
	// Pin the resume contract: every turn writes a snapshot to the
	// same stable artifact ID (`convo-{run.ID}`). A test stub records
	// each upsert so we can verify (a) the artifact ID is stable
	// across turns, (b) the JSON-decoded payload reflects the latest
	// conversation state, and (c) tool results are in the snapshot.
	upserts := make([]types.TaskArtifact, 0)
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "call-1", Type: "function",
				Function: types.ToolCallFunction{Name: "shell_exec", Arguments: `{"command":"ls"}`},
			})),
			makeChatResp(makeAssistantMsg("Done.")),
		},
	}
	shell := &stubExecutor{
		result: &ExecutionResult{
			Status: "completed",
			Artifacts: []types.TaskArtifact{
				{Kind: "stdout", Name: "stdout.txt", ContentText: "README.md\n"},
			},
		},
	}
	loop := NewAgentLoopExecutor(llm, shell, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.UpsertArtifact = func(art types.TaskArtifact) error {
		upserts = append(upserts, art)
		return nil
	}
	if _, err := loop.Execute(context.Background(), spec); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	convoUpserts := make([]types.TaskArtifact, 0)
	for _, u := range upserts {
		if u.Kind == "agent_conversation" {
			convoUpserts = append(convoUpserts, u)
		}
	}
	if len(convoUpserts) < 2 {
		t.Fatalf("conversation upserts = %d, want >= 2 (one per turn)", len(convoUpserts))
	}
	// Stable ID across all upserts.
	for i, u := range convoUpserts {
		if u.ID != "convo-run-1" {
			t.Errorf("upsert[%d].ID = %q, want stable convo-run-1", i, u.ID)
		}
	}
	// Last snapshot must contain the final assistant message.
	last := convoUpserts[len(convoUpserts)-1]
	if !strings.Contains(last.ContentText, "Done.") {
		t.Errorf("last snapshot missing final assistant turn: %s", last.ContentText)
	}
	// Tool result was in the conversation between turn 1 and turn 2,
	// so an intermediate snapshot must include it.
	hasToolResult := false
	for _, u := range convoUpserts {
		if strings.Contains(u.ContentText, "README.md") {
			hasToolResult = true
		}
	}
	if !hasToolResult {
		t.Errorf("no snapshot captured tool result: %+v", convoUpserts)
	}
}

func TestAgentLoop_HydratesFromResumeCheckpoint(t *testing.T) {
	// On resume: the loop starts with the saved conversation, NOT
	// the user prompt. We verify by encoding a 3-message history and
	// checking that the next LLM call sees those exact messages.
	saved := []types.Message{
		{Role: "user", Content: "original prompt"},
		{Role: "assistant", Content: "", ToolCalls: []types.ToolCall{{ID: "c1", Type: "function", Function: types.ToolCallFunction{Name: "shell_exec", Arguments: `{"command":"ls"}`}}}},
		{Role: "tool", Content: "status=completed\n--- stdout ---\nREADME.md\n", ToolCallID: "c1"},
	}
	savedJSON, _ := json.Marshal(saved)

	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("Resumed and answered.")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.ResumeCheckpoint = &ResumeCheckpoint{
		SourceRunID:       "run-prev",
		AgentConversation: savedJSON,
		LastStepIndex:     5,
	}
	if _, err := loop.Execute(context.Background(), spec); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(llm.lastReqs) != 1 {
		t.Fatalf("LLM calls = %d, want 1 (single resume turn)", len(llm.lastReqs))
	}
	resumed := llm.lastReqs[0].Messages
	if len(resumed) != 3 {
		t.Fatalf("resumed conversation = %d messages, want 3 (saved history, no fresh user prompt)", len(resumed))
	}
	if resumed[0].Content != "original prompt" {
		t.Errorf("resumed[0].Content = %q, want 'original prompt'", resumed[0].Content)
	}
	if len(resumed[1].ToolCalls) != 1 || resumed[1].ToolCalls[0].ID != "c1" {
		t.Errorf("resumed[1] tool call lost: %+v", resumed[1])
	}
	if resumed[2].Role != "tool" || resumed[2].ToolCallID != "c1" {
		t.Errorf("resumed[2] tool message lost: %+v", resumed[2])
	}
}

func TestAgentLoop_AppendsPromptFromContinuationCheckpoint(t *testing.T) {
	saved := []types.Message{
		{Role: "user", Content: "first prompt"},
		{Role: "assistant", Content: "first answer"},
	}
	savedJSON, _ := json.Marshal(saved)

	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("Second answer.")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.ResumeCheckpoint = &ResumeCheckpoint{
		SourceRunID:       "run-prev",
		AgentConversation: savedJSON,
		AppendUserPrompt:  "second prompt",
	}

	if _, err := loop.Execute(context.Background(), spec); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(llm.lastReqs) != 1 {
		t.Fatalf("LLM calls = %d, want 1", len(llm.lastReqs))
	}
	got := llm.lastReqs[0].Messages
	if len(got) != 3 {
		t.Fatalf("messages = %d, want saved history plus appended prompt: %+v", len(got), got)
	}
	if got[2].Role != "user" || got[2].Content != "second prompt" {
		t.Fatalf("appended message = %+v, want second user prompt", got[2])
	}
}

func TestAgentLoop_HydrateGracefulFallbackOnCorruptCheckpoint(t *testing.T) {
	// Corrupt JSON in the resume artifact must not crash the loop —
	// fall back to a fresh user-prompt-only conversation. Lets a
	// hand-edited or out-of-band artifact still produce a useful run.
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("Fresh start.")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.ResumeCheckpoint = &ResumeCheckpoint{
		SourceRunID:       "run-prev",
		AgentConversation: []byte(`not valid json {`),
	}
	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "completed" {
		t.Errorf("Status = %q, want completed (fallback)", res.Status)
	}
	if len(llm.lastReqs[0].Messages) != 1 || llm.lastReqs[0].Messages[0].Content != "summarize the working directory" {
		t.Errorf("expected fresh-start user-prompt-only conversation; got: %+v", llm.lastReqs[0].Messages)
	}
}

func TestTruncateConversationToTurn(t *testing.T) {
	// Build a 3-turn conversation: system + user, then turns 1/2/3,
	// each with a tool call + result, plus a final answer in turn 3.
	conv := []types.Message{
		{Role: "system", Content: "be concise"},
		{Role: "user", Content: "list /etc"},
		{Role: "assistant", Content: "", ToolCalls: []types.ToolCall{{ID: "c1", Function: types.ToolCallFunction{Name: "shell_exec"}}}},
		{Role: "tool", Content: "result1", ToolCallID: "c1"},
		{Role: "assistant", Content: "", ToolCalls: []types.ToolCall{{ID: "c2", Function: types.ToolCallFunction{Name: "shell_exec"}}}},
		{Role: "tool", Content: "result2", ToolCallID: "c2"},
		{Role: "assistant", Content: "done."},
	}

	t.Run("turn 1 keeps prelude only", func(t *testing.T) {
		got, err := truncateConversationToTurn(conv, 1)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("len = %d, want 2 (system+user)", len(got))
		}
		if got[0].Role != "system" || got[1].Role != "user" {
			t.Errorf("prelude shape wrong: %+v", got)
		}
	})

	t.Run("turn 2 keeps turn 1's tool result", func(t *testing.T) {
		got, err := truncateConversationToTurn(conv, 2)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		// Should be: system, user, assistant_1, tool_1
		if len(got) != 4 {
			t.Fatalf("len = %d, want 4", len(got))
		}
		if got[3].Role != "tool" || got[3].ToolCallID != "c1" {
			t.Errorf("tail expected to be tool result for c1, got %+v", got[3])
		}
	})

	t.Run("final turn drops only that assistant message", func(t *testing.T) {
		got, err := truncateConversationToTurn(conv, 3)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(got) != 6 {
			t.Fatalf("len = %d, want 6", len(got))
		}
		// Tail is the second tool result; assistant_3 (final) and
		// nothing else have been dropped.
		if got[len(got)-1].Role != "tool" || got[len(got)-1].ToolCallID != "c2" {
			t.Errorf("tail wrong: %+v", got[len(got)-1])
		}
	})

	t.Run("out-of-range turn fails", func(t *testing.T) {
		if _, err := truncateConversationToTurn(conv, 4); err == nil {
			t.Errorf("turn 4 of 3-turn conv should fail")
		}
		if _, err := truncateConversationToTurn(conv, 0); err == nil {
			t.Errorf("turn 0 should fail")
		}
		if _, err := truncateConversationToTurn(conv, -1); err == nil {
			t.Errorf("negative turn should fail")
		}
	})

	t.Run("does not mutate input", func(t *testing.T) {
		before := len(conv)
		_, _ = truncateConversationToTurn(conv, 2)
		if len(conv) != before {
			t.Errorf("input mutated: len changed from %d to %d", before, len(conv))
		}
	})
}

func TestCountAssistantTurns(t *testing.T) {
	cases := []struct {
		name string
		msgs []types.Message
		want int
	}{
		{"empty", nil, 0},
		{"user only", []types.Message{{Role: "user", Content: "hi"}}, 0},
		{"three turns", []types.Message{
			{Role: "user"},
			{Role: "assistant", ToolCalls: []types.ToolCall{{ID: "c1"}}},
			{Role: "tool", ToolCallID: "c1"},
			{Role: "assistant", ToolCalls: []types.ToolCall{{ID: "c2"}}},
			{Role: "tool", ToolCallID: "c2"},
			{Role: "assistant", Content: "done"},
		}, 3},
		{"system message ignored", []types.Message{
			{Role: "system"},
			{Role: "user"},
			{Role: "assistant", Content: "answer"},
		}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := countAssistantTurns(tc.msgs); got != tc.want {
				t.Errorf("countAssistantTurns = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestAgentLoop_RetryFromTurn_TruncatedConversationDrivesNextLLMCall(t *testing.T) {
	// Simulate a retry-from-turn where the runner has already
	// truncated the conversation (e.g. operator clicked "retry from
	// turn 2"). The loop should see the truncated history, call the
	// LLM again at that point, and run normally from there. We
	// pre-truncate to turn 2 (drops assistant_2 onwards), leaving
	// system+user+assistant_1+tool_1 — the next LLM call happens at
	// turn 2 with that context.
	saved := []types.Message{
		{Role: "user", Content: "list things"},
		{Role: "assistant", Content: "", ToolCalls: []types.ToolCall{{ID: "c1", Type: "function", Function: types.ToolCallFunction{Name: "shell_exec", Arguments: `{"command":"ls"}`}}}},
		{Role: "tool", Content: "result1", ToolCallID: "c1"},
		// turn 2's assistant message + everything after has been
		// dropped by the runner before we get here.
	}
	savedJSON, _ := json.Marshal(saved)

	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("retried answer")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.ResumeCheckpoint = &ResumeCheckpoint{
		SourceRunID:       "run-prev",
		AgentConversation: savedJSON,
		LastStepIndex:     0, // runner zeroes this for retry-from-turn so step indices restart at 1
		RetryFromTurn:     2,
	}
	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "completed" {
		t.Fatalf("Status = %q, want completed", res.Status)
	}
	if len(llm.lastReqs) != 1 {
		t.Fatalf("LLM calls = %d, want 1 (retry runs the truncated turn once)", len(llm.lastReqs))
	}
	// Critical: the LLM saw the prior context (3 messages), not the
	// dropped assistant_2. This is what lets the LLM produce a
	// different answer than the original turn 2.
	got := llm.lastReqs[0].Messages
	if len(got) != 3 {
		t.Fatalf("LLM saw %d messages, want 3 (the truncated context)", len(got))
	}
	if got[2].Role != "tool" || got[2].ToolCallID != "c1" {
		t.Errorf("last message before retry should be tool result for c1, got %+v", got[2])
	}
	// And the retry produced an assistant final answer, so the loop
	// completed (no further turns).
	if res.Steps[0].Index != 1 {
		t.Errorf("first step index = %d, want 1 (fresh-numbered for retry-from-turn)", res.Steps[0].Index)
	}
}

func TestAgentLoop_GatedToolPausesAndEmitsApproval(t *testing.T) {
	// LLM asks for shell_exec, which is gated. Loop must pause:
	// status=awaiting_approval, one approval in PendingApprovals
	// covering the turn, conversation persisted, shell NOT executed.
	// On the runner side, this drives the run into awaiting_approval
	// where the operator decides whether to allow the tool call.
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("I need to inspect the workspace.", types.ToolCall{
				ID: "call-1", Type: "function",
				Function: types.ToolCallFunction{Name: "shell_exec", Arguments: `{"command":"ls"}`},
			})),
		},
	}
	shell := &stubExecutor{}
	loop := NewAgentLoopExecutor(llm, shell, &stubExecutor{}, &stubExecutor{}, 8, []string{"shell_exec"}, HTTPRequestPolicy{})
	res, err := loop.Execute(context.Background(), newAgentLoopSpec(t))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "awaiting_approval" {
		t.Fatalf("Status = %q, want awaiting_approval", res.Status)
	}
	if len(shell.calls) != 0 {
		t.Errorf("shell_exec ran without approval; calls=%+v", shell.calls)
	}
	if len(res.PendingApprovals) != 1 {
		t.Fatalf("PendingApprovals = %d, want 1", len(res.PendingApprovals))
	}
	approval := res.PendingApprovals[0]
	if approval.Status != "pending" || approval.Kind != "agent_loop_tool_call" {
		t.Errorf("approval shape wrong: %+v", approval)
	}
	if !strings.Contains(approval.Reason, "shell_exec") {
		t.Errorf("approval reason should name the gated tool: %q", approval.Reason)
	}
	// Conversation snapshot must be present so the resume path
	// hydrates from it.
	convo := findArtifactByKind(res.Artifacts, "agent_conversation")
	if convo == nil {
		t.Fatalf("conversation artifact missing on pause; got: %+v", res.Artifacts)
	}
	// Saved conversation must include the assistant's tool_call so
	// the resume run can dispatch it.
	if !strings.Contains(convo.ContentText, "shell_exec") {
		t.Errorf("conversation snapshot lost tool call: %s", convo.ContentText)
	}
}

func TestAgentLoop_NonGatedToolDispatchesNormally(t *testing.T) {
	// file_write is NOT in the gated set; loop runs it inline and
	// continues to the next turn. Verifies that gating is opt-in by
	// tool name, not blanket.
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "call-1", Type: "function",
				Function: types.ToolCallFunction{Name: "file_write", Arguments: `{"path":"out.txt","content":"hi"}`},
			})),
			makeChatResp(makeAssistantMsg("Done.")),
		},
	}
	file := &stubExecutor{result: &ExecutionResult{Status: "completed"}}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, file, &stubExecutor{}, 8, []string{"shell_exec"}, HTTPRequestPolicy{})
	res, err := loop.Execute(context.Background(), newAgentLoopSpec(t))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "completed" {
		t.Errorf("Status = %q, want completed (file_write isn't gated)", res.Status)
	}
	if len(file.calls) != 1 {
		t.Errorf("file_write should have run; calls=%+v", file.calls)
	}
	if len(res.PendingApprovals) != 0 {
		t.Errorf("PendingApprovals = %d, want 0", len(res.PendingApprovals))
	}
}

func TestAgentLoop_FileEditToolUsesExactReplacementAndPatchArtifact(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {\n\tprintln(\"old\")\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var events []capturedRunEvent
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "edit-1", Type: "function",
				Function: types.ToolCallFunction{
					Name:      "file_edit",
					Arguments: `{"path":"main.go","old_text":"println(\"old\")","new_text":"println(\"new\")"}`,
				},
			})),
			makeChatResp(makeAssistantMsg("Updated main.go.")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, NewFileExecutor(workspace.NewLocalWorkspace()), &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir
	spec.Task.SandboxAllowedRoot = dir
	spec.EmitRunEvent = func(eventType string, data map[string]any) {
		events = append(events, capturedRunEvent{eventType: eventType, data: data})
	}

	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "completed" {
		t.Fatalf("Status = %q, want completed", res.Status)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(content), `println("new")`) {
		t.Fatalf("file was not edited:\n%s", string(content))
	}
	patch := findArtifactByKind(res.Artifacts, "patch")
	if patch == nil {
		t.Fatalf("patch artifact missing; artifacts=%+v", res.Artifacts)
	}
	if !strings.Contains(patch.ContentText, `-	println("old")`) || !strings.Contains(patch.ContentText, `+	println("new")`) {
		t.Fatalf("patch does not show exact replacement:\n%s", patch.ContentText)
	}
	foundPatchEvent := false
	for _, event := range events {
		if event.eventType != "tool.file.patch" {
			continue
		}
		foundPatchEvent = true
		if got := event.data["tool_name"]; got != "file_edit" {
			t.Fatalf("tool.file.patch tool_name = %v, want file_edit", got)
		}
		if got := event.data["artifact_id"]; got != patch.ID {
			t.Fatalf("tool.file.patch artifact_id = %v, want %s", got, patch.ID)
		}
	}
	if !foundPatchEvent {
		t.Fatalf("tool.file.patch event missing: %+v", events)
	}
}

func TestAgentLoop_FileEditRejectsAmbiguousMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repeat.txt")
	if err := os.WriteFile(path, []byte("same\nsame\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "edit-1", Type: "function",
				Function: types.ToolCallFunction{
					Name:      "file_edit",
					Arguments: `{"path":"repeat.txt","old_text":"same","new_text":"changed"}`,
				},
			})),
			makeChatResp(makeAssistantMsg("I need a more specific match.")),
		},
	}
	file := &stubExecutor{}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, file, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir
	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "completed" {
		t.Fatalf("Status = %q, want completed", res.Status)
	}
	if len(file.calls) != 0 {
		t.Fatalf("file executor should not run on ambiguous edit; calls=%+v", file.calls)
	}
	var toolResult string
	for _, msg := range llm.lastReqs[1].Messages {
		if msg.Role == "tool" && msg.ToolCallID == "edit-1" {
			toolResult = msg.Content
			break
		}
	}
	if !strings.Contains(toolResult, "appears 2 times") {
		t.Fatalf("ambiguous edit error missing from tool result: %q", toolResult)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(content) != "same\nsame\n" {
		t.Fatalf("file changed despite ambiguous edit:\n%s", string(content))
	}
}

func TestAgentLoop_FileEditCanProposePatchWithoutApplying(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	original := "package main\n\nfunc main() {\n\tprintln(\"old\")\n}\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	var events []capturedRunEvent
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "edit-1", Type: "function",
				Function: types.ToolCallFunction{
					Name:      "file_edit",
					Arguments: `{"path":"main.go","old_text":"println(\"old\")","new_text":"println(\"new\")","propose":true}`,
				},
			})),
			makeChatResp(makeAssistantMsg("Patch proposed.")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, NewFileExecutor(workspace.NewLocalWorkspace()), &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir
	spec.Task.SandboxAllowedRoot = dir
	spec.EmitRunEvent = func(eventType string, data map[string]any) {
		events = append(events, capturedRunEvent{eventType: eventType, data: data})
	}

	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(content) != original {
		t.Fatalf("proposed edit changed file:\n%s", string(content))
	}
	patch := findArtifactByKind(res.Artifacts, "patch")
	if patch == nil {
		t.Fatalf("patch artifact missing; artifacts=%+v", res.Artifacts)
	}
	if patch.Status != "proposed" {
		t.Fatalf("patch status = %q, want proposed", patch.Status)
	}
	if !strings.Contains(patch.ContentText, `+	println("new")`) {
		t.Fatalf("patch does not show proposed replacement:\n%s", patch.ContentText)
	}
	foundPatchEvent := false
	for _, event := range events {
		if event.eventType != "tool.file.patch" {
			continue
		}
		foundPatchEvent = true
		if got := event.data["artifact_status"]; got != "proposed" {
			t.Fatalf("artifact_status = %v, want proposed", got)
		}
		if got := event.data["operation"]; got != "propose" {
			t.Fatalf("operation = %v, want propose", got)
		}
	}
	if !foundPatchEvent {
		t.Fatalf("tool.file.patch event missing: %+v", events)
	}
}

func TestAgentLoop_ApplyPatchToolAppliesMultiFilePatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "old.txt"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	patchText := "*** Begin Patch\n" +
		"*** Update File: old.txt\n" +
		"@@\n" +
		" alpha\n" +
		"-beta\n" +
		"+gamma\n" +
		"*** Add File: new.txt\n" +
		"+fresh\n" +
		"*** End Patch\n"
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "patch-1", Type: "function",
				Function: types.ToolCallFunction{
					Name:      "apply_patch",
					Arguments: mustJSON(t, map[string]any{"patch_text": patchText}),
				},
			})),
			makeChatResp(makeAssistantMsg("Patch applied.")),
		},
	}
	var events []capturedRunEvent
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, NewFileExecutor(workspace.NewLocalWorkspace()), &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir
	spec.EmitRunEvent = func(eventType string, data map[string]any) {
		events = append(events, capturedRunEvent{eventType: eventType, data: data})
	}
	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	updated, err := os.ReadFile(filepath.Join(dir, "old.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(updated) != "alpha\ngamma\n" {
		t.Fatalf("old.txt = %q, want updated content", string(updated))
	}
	added, err := os.ReadFile(filepath.Join(dir, "new.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(added) != "fresh\n" {
		t.Fatalf("new.txt = %q, want fresh", string(added))
	}
	patchCount := 0
	for _, artifact := range res.Artifacts {
		if artifact.Kind == "patch" {
			patchCount++
		}
	}
	if patchCount != 2 {
		t.Fatalf("patch artifact count = %d, want 2; artifacts=%+v", patchCount, res.Artifacts)
	}
	eventCount := 0
	for _, event := range events {
		if event.eventType == "tool.file.patch" && event.data["tool_name"] == "apply_patch" {
			eventCount++
		}
	}
	if eventCount != 2 {
		t.Fatalf("tool.file.patch apply_patch events = %d, want 2: %+v", eventCount, events)
	}
}

func TestAgentLoop_ApplyPatchToolCanProposeWithoutApplying(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "old.txt"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	patchText := "*** Begin Patch\n" +
		"*** Update File: old.txt\n" +
		"@@\n" +
		" alpha\n" +
		"-beta\n" +
		"+gamma\n" +
		"*** End Patch\n"
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "patch-1", Type: "function",
				Function: types.ToolCallFunction{
					Name:      "apply_patch",
					Arguments: mustJSON(t, map[string]any{"patch_text": patchText, "propose": true}),
				},
			})),
			makeChatResp(makeAssistantMsg("Patch proposed.")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, NewFileExecutor(workspace.NewLocalWorkspace()), &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir
	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "old.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "alpha\nbeta\n" {
		t.Fatalf("proposed patch changed file: %q", string(content))
	}
	patch := findArtifactByKind(res.Artifacts, "patch")
	if patch == nil {
		t.Fatalf("patch artifact missing: %+v", res.Artifacts)
	}
	if patch.Status != "proposed" {
		t.Fatalf("patch status = %q, want proposed", patch.Status)
	}
}

func TestAgentLoop_ApplyPatchPreflightsAllFilesBeforeWriting(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "two.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	patchText := "*** Begin Patch\n" +
		"*** Update File: one.txt\n" +
		"@@\n" +
		"-one\n" +
		"+ONE\n" +
		"*** Update File: two.txt\n" +
		"@@\n" +
		"-missing\n" +
		"+TWO\n" +
		"*** End Patch\n"
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "patch-1", Type: "function",
				Function: types.ToolCallFunction{
					Name:      "apply_patch",
					Arguments: mustJSON(t, map[string]any{"patch_text": patchText}),
				},
			})),
			makeChatResp(makeAssistantMsg("Patch failed.")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, NewFileExecutor(workspace.NewLocalWorkspace()), &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir
	if _, err := loop.Execute(context.Background(), spec); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "one.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "one\n" {
		t.Fatalf("first file was partially patched before second failed: %q", string(content))
	}
}

func TestAgentLoop_ParseStructuredPatchRequiresBeginMarker(t *testing.T) {
	patchText := "*** Update File: file.txt\n" +
		"@@\n" +
		"-old\n" +
		"+new\n" +
		"*** End Patch\n"
	if _, errMsg := parseStructuredPatch(patchText); errMsg == "" {
		t.Fatal("parseStructuredPatch accepted patch without begin marker")
	} else if !strings.Contains(errMsg, "*** Begin Patch") {
		t.Fatalf("error = %q, want begin-marker guidance", errMsg)
	}

	if _, errMsg := parseStructuredPatch("*** End Patch\n"); errMsg == "" {
		t.Fatal("parseStructuredPatch accepted end marker without begin marker")
	}
}

func TestAgentLoop_ApplyPatchUpdateHandlesBlankLines(t *testing.T) {
	current := "alpha\n\nbeta\n"
	patchLines := []string{
		"@@\n",
		" alpha\n",
		"-\n",
		"+spacer\n",
		" beta\n",
	}
	next, errMsg := applyPatchUpdate(current, patchLines)
	if errMsg != "" {
		t.Fatalf("applyPatchUpdate returned error: %s", errMsg)
	}
	if next != "alpha\nspacer\nbeta\n" {
		t.Fatalf("updated content = %q, want blank line replaced", next)
	}
}

func TestAgentLoop_ResumeAfterApprovalDispatchesPendingCalls(t *testing.T) {
	// On resume: the conversation has a trailing assistant message
	// with tool_calls and no following tool result. The loop must
	// detect this, skip the LLM call (which already happened in the
	// previous run), dispatch the approved tool, and continue. Then
	// the next turn's LLM call sees the tool result and produces a
	// final answer.
	saved := []types.Message{
		{Role: "user", Content: "summarize the working directory"},
		{Role: "assistant", Content: "I need to inspect.", ToolCalls: []types.ToolCall{{
			ID: "call-1", Type: "function",
			Function: types.ToolCallFunction{Name: "shell_exec", Arguments: `{"command":"ls"}`},
		}}},
	}
	savedJSON, _ := json.Marshal(saved)

	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			// The resumed loop only calls the LLM AFTER dispatching
			// the pending tool call — at which point it provides a
			// final answer over the tool result.
			makeChatResp(makeAssistantMsg("Two files: README.md and main.go.")),
		},
	}
	shell := &stubExecutor{
		result: &ExecutionResult{
			Status: "completed",
			Artifacts: []types.TaskArtifact{
				{Kind: "stdout", Name: "stdout.txt", ContentText: "README.md\nmain.go\n"},
			},
		},
	}
	loop := NewAgentLoopExecutor(llm, shell, &stubExecutor{}, &stubExecutor{}, 8, []string{"shell_exec"}, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.ResumeCheckpoint = &ResumeCheckpoint{
		SourceRunID:       "run-1",
		AgentConversation: savedJSON,
		Reason:            "approved_mid_loop",
	}
	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "completed" {
		t.Fatalf("Status = %q, want completed; LastError=%q", res.Status, res.LastError)
	}
	if len(shell.calls) != 1 {
		t.Errorf("shell_exec should have run on resume; got %+v", shell.calls)
	}
	// Exactly one LLM call (the post-dispatch reasoning turn). The
	// resumed turn does NOT call the LLM since the assistant message
	// is already in the saved conversation.
	if got := llm.calls.Load(); got != 1 {
		t.Errorf("LLM calls = %d, want 1 (resume skips the first LLM round-trip)", got)
	}
	// The single LLM request must have seen the tool result.
	if len(llm.lastReqs) != 1 {
		t.Fatalf("LLM request count = %d, want 1", len(llm.lastReqs))
	}
	hasToolResult := false
	for _, m := range llm.lastReqs[0].Messages {
		if m.Role == "tool" && m.ToolCallID == "call-1" && strings.Contains(m.Content, "README.md") {
			hasToolResult = true
		}
	}
	if !hasToolResult {
		t.Errorf("post-resume LLM request missing tool result: %+v", llm.lastReqs[0].Messages)
	}
}

func TestAgentLoop_GatedToolListedWithMultipleToolsInTurn(t *testing.T) {
	// LLM asks for both a gated and a non-gated tool in one turn.
	// We pause for approval (any gated tool gates the whole turn);
	// the approval reason mentions only the gated tool name to match
	// what the operator must consent to. No tools dispatched yet.
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("",
				types.ToolCall{ID: "c1", Type: "function", Function: types.ToolCallFunction{Name: "shell_exec", Arguments: `{"command":"ls"}`}},
				types.ToolCall{ID: "c2", Type: "function", Function: types.ToolCallFunction{Name: "file_write", Arguments: `{"path":"x","content":"y"}`}},
			)),
		},
	}
	shell := &stubExecutor{}
	file := &stubExecutor{}
	loop := NewAgentLoopExecutor(llm, shell, file, &stubExecutor{}, 8, []string{"shell_exec"}, HTTPRequestPolicy{})
	res, err := loop.Execute(context.Background(), newAgentLoopSpec(t))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "awaiting_approval" {
		t.Fatalf("Status = %q, want awaiting_approval", res.Status)
	}
	if len(shell.calls) != 0 || len(file.calls) != 0 {
		t.Errorf("no tools should run before approval; shell=%d file=%d", len(shell.calls), len(file.calls))
	}
	if len(res.PendingApprovals) != 1 {
		t.Fatalf("PendingApprovals = %d, want 1 (one approval covers the whole turn)", len(res.PendingApprovals))
	}
	reason := res.PendingApprovals[0].Reason
	if !strings.Contains(reason, "shell_exec") {
		t.Errorf("reason missing gated tool name: %q", reason)
	}
	if strings.Contains(reason, "file_write") {
		t.Errorf("reason should not mention non-gated tool: %q", reason)
	}
}

func TestAgentLoop_SystemPromptPrependedOnFreshRuns(t *testing.T) {
	// When the runner composes a system prompt, the agent loop must
	// prepend it as the first message. Without this the four-layer
	// composition (global / tenant / workspace / task) has no
	// effect — the model never sees the directives.
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("answered")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.SystemPrompt = "You are a careful agent. Always cite sources."
	if _, err := loop.Execute(context.Background(), spec); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(llm.lastReqs) != 1 {
		t.Fatalf("LLM calls = %d, want 1", len(llm.lastReqs))
	}
	first := llm.lastReqs[0].Messages
	if len(first) != 2 {
		t.Fatalf("messages = %d, want 2 (system + user)", len(first))
	}
	if first[0].Role != "system" || !strings.Contains(first[0].Content, "careful agent") {
		t.Errorf("first message not the system prompt: %+v", first[0])
	}
	if first[1].Role != "user" {
		t.Errorf("second message not user: %+v", first[1])
	}
}

func TestAgentLoop_AnnotatesModelLacksToolsError(t *testing.T) {
	// Tiny / non-tool-calling models (e.g. smollm2:135m on Ollama)
	// reject the `tools` field with a 400. The raw upstream message
	// — "registry.ollama.ai/library/smollm2:135m does not support
	// tools" — is technically correct but offers no remedy. The
	// agent loop wraps it with a concrete "pick a tool-capable
	// model" hint so the operator knows what to do next.
	llm := &erroringLLM{err: fmt.Errorf("provider ollama call failed: upstream error (400/invalid_request_error): registry.ollama.ai/library/smollm2:135m does not support tools")}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Run.Model = "smollm2:135m"
	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "failed" {
		t.Fatalf("Status = %q, want failed", res.Status)
	}
	// The friendly hint must mention the model name and the remedy
	// so the run log is self-explanatory.
	if !strings.Contains(res.LastError, "smollm2:135m") || !strings.Contains(res.LastError, "tool-capable") {
		t.Errorf("LastError missing model name or remedy: %q", res.LastError)
	}
}

func TestAgentLoop_PassesProviderHintFromRun(t *testing.T) {
	// run.Provider is set from task.RequestedProvider at create
	// time. The agent loop has to forward it into the ChatRequest
	// scope so the router pins to the operator's choice — without
	// this a task created with provider=ollama got auto-routed to
	// OpenAI by the default router and failed with "api key is
	// required for cloud provider openai" even when the operator
	// only had Ollama configured.
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("done")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Run.Provider = "ollama"
	if _, err := loop.Execute(context.Background(), spec); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(llm.lastReqs) != 1 {
		t.Fatalf("LLM calls = %d, want 1", len(llm.lastReqs))
	}
	if got := llm.lastReqs[0].Scope.ProviderHint; got != "ollama" {
		t.Errorf("ProviderHint = %q, want %q (must mirror run.Provider)", got, "ollama")
	}
}

func TestAgentLoop_PrependsWorkspaceEnvironmentSystemMessage(t *testing.T) {
	// The agent loop must inform the LLM where the workspace lives,
	// otherwise the model uses paths verbatim from the user prompt
	// (e.g. the operator's source repo) and the sandbox rejects
	// tool calls that target paths outside the cloned workspace.
	// This was a real failure mode in the field — pinning it here.
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("done")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	// Task.WorkingDirectory is what the runner sets via taskForRun
	// from run.WorkspacePath — the actual sandbox root.
	spec.Task.WorkingDirectory = "/tmp/hecate-workspaces/task_x/run_y"
	if _, err := loop.Execute(context.Background(), spec); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	first := llm.lastReqs[0].Messages
	// Expect [env-system, user] (no operator system prompt set).
	if len(first) != 2 {
		t.Fatalf("messages = %d, want 2 (env + user); got: %+v", len(first), first)
	}
	if first[0].Role != "system" {
		t.Fatalf("first message role = %q, want system", first[0].Role)
	}
	if !strings.Contains(first[0].Content, "/tmp/hecate-workspaces/task_x/run_y") {
		t.Errorf("env system message missing workspace path; got: %q", first[0].Content)
	}
	// Reaffirms the contract the LLM must follow — sanity check
	// that the message body actually instructs about path scoping.
	if !strings.Contains(first[0].Content, "outside this directory") {
		t.Errorf("env system message missing the sandbox-scope warning; got: %q", first[0].Content)
	}
	if first[1].Role != "user" {
		t.Errorf("second message role = %q, want user", first[1].Role)
	}
}

func TestAgentLoop_EnvSystemMessageStacksWithOperatorSystemPrompt(t *testing.T) {
	// The four-layer operator system prompt and the workspace env
	// message live as two separate system messages. The env one
	// goes FIRST so it grounds the model in environmental fact
	// before the operator's behavioral directives.
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("done")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = "/srv/workspace"
	spec.SystemPrompt = "Operator directive: cite sources."
	if _, err := loop.Execute(context.Background(), spec); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	first := llm.lastReqs[0].Messages
	if len(first) != 3 {
		t.Fatalf("messages = %d, want 3 (env + operator + user); got: %+v", len(first), first)
	}
	if !strings.Contains(first[0].Content, "/srv/workspace") {
		t.Errorf("first system message should be env (workspace path); got: %q", first[0].Content)
	}
	if !strings.Contains(first[1].Content, "Operator directive") {
		t.Errorf("second system message should be operator prompt; got: %q", first[1].Content)
	}
	if first[2].Role != "user" {
		t.Errorf("third message role = %q, want user", first[2].Role)
	}
}

func TestAgentLoop_NoSystemPromptWhenEmpty(t *testing.T) {
	// Empty composed prompt = no system message at all. Avoids
	// emitting a wasted role:system message with empty content.
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("ok")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.SystemPrompt = ""
	if _, err := loop.Execute(context.Background(), spec); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	first := llm.lastReqs[0].Messages
	if len(first) != 1 || first[0].Role != "user" {
		t.Errorf("messages should be just [user], got: %+v", first)
	}
}

func TestAgentLoop_SystemPromptNotReinjectedOnResume(t *testing.T) {
	// On resume, the saved conversation already contains the system
	// message from the original run. We must NOT re-prepend it —
	// double-system would confuse the model and waste tokens.
	saved := []types.Message{
		{Role: "system", Content: "Original system prompt"},
		{Role: "user", Content: "original prompt"},
		{Role: "assistant", Content: "answer"},
	}
	savedJSON, _ := json.Marshal(saved)
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("done")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	// Different SystemPrompt would normally apply — but on resume
	// the saved one wins. Lets us assert that we don't blend layers.
	spec.SystemPrompt = "Different prompt that should NOT show up"
	spec.ResumeCheckpoint = &ResumeCheckpoint{
		SourceRunID:       "run-1",
		AgentConversation: savedJSON,
	}
	// Add another assistant message to provoke another LLM call.
	llm.responses = append(llm.responses, makeChatResp(makeAssistantMsg("end")))
	if _, err := loop.Execute(context.Background(), spec); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	first := llm.lastReqs[0].Messages
	systemMessages := 0
	for _, m := range first {
		if m.Role == "system" {
			systemMessages++
		}
	}
	if systemMessages != 1 {
		t.Errorf("system message count on resume = %d, want 1", systemMessages)
	}
	for _, m := range first {
		if m.Role == "system" && strings.Contains(m.Content, "Different prompt") {
			t.Errorf("re-composed system prompt leaked into resumed conversation")
		}
	}
}

func TestAgentLoop_CostAccumulatesAcrossTurns(t *testing.T) {
	// Each LLM response carries Cost.TotalMicrosUSD; the loop must
	// sum these across turns and surface the total on
	// ExecutionResult.CostMicrosUSD. The runner reads this value to
	// populate run.TotalCostMicrosUSD.
	respWithCost := func(content string, cost int64) *types.ChatResponse {
		r := makeChatResp(makeAssistantMsg(content))
		r.Cost.TotalMicrosUSD = cost
		return r
	}
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			respWithCost("", 100),              // turn 1: tool call
			respWithCost("Final answer.", 250), // turn 2: final
		},
	}
	llm.responses[0].Choices[0].Message.ToolCalls = []types.ToolCall{{
		ID: "c1", Type: "function",
		Function: types.ToolCallFunction{Name: "shell_exec", Arguments: `{"command":"ls"}`},
	}}

	loop := NewAgentLoopExecutor(llm, &stubExecutor{result: &ExecutionResult{Status: "completed"}}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	res, err := loop.Execute(context.Background(), newAgentLoopSpec(t))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.CostMicrosUSD != 350 {
		t.Errorf("CostMicrosUSD = %d, want 350 (100 + 250)", res.CostMicrosUSD)
	}
}

func TestAgentLoop_ResultCapturesResolvedRoute(t *testing.T) {
	resp := withResolvedRoute(makeChatResp(makeAssistantMsg("Final answer.")))
	llm := &scriptedLLM{responses: []*types.ChatResponse{resp}}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{result: &ExecutionResult{Status: "completed"}}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Run.Provider = "auto"

	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertResolvedRoute(t, res)
}

func TestAgentLoop_ResultCapturesResolvedRouteWhenAwaitingApproval(t *testing.T) {
	resp := withResolvedRoute(makeChatResp(makeAssistantMsg("I need shell access.", types.ToolCall{
		ID: "call-1", Type: "function",
		Function: types.ToolCallFunction{Name: "shell_exec", Arguments: `{"command":"ls"}`},
	})))
	llm := &scriptedLLM{responses: []*types.ChatResponse{resp}}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, []string{"shell_exec"}, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Run.Provider = "auto"

	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "awaiting_approval" {
		t.Fatalf("Status = %q, want awaiting_approval", res.Status)
	}
	assertResolvedRoute(t, res)
}

func TestAgentLoop_ResultKeepsResolvedRouteOnLaterLLMFailure(t *testing.T) {
	resp := withResolvedRoute(makeChatResp(makeAssistantMsg("", types.ToolCall{
		ID: "call-1", Type: "function",
		Function: types.ToolCallFunction{Name: "shell_exec", Arguments: `{"command":"ls"}`},
	})))
	llm := &firstResponseThenErrorLLM{first: resp, err: errors.New("provider timed out")}
	shell := &stubExecutor{result: &ExecutionResult{Status: "completed"}}
	loop := NewAgentLoopExecutor(llm, shell, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Run.Provider = "auto"

	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res == nil || res.Status != "failed" {
		t.Fatalf("result = %+v, want failed result", res)
	}
	assertResolvedRoute(t, res)
}

func TestAgentLoop_PerTaskCostCeilingTriggersFail(t *testing.T) {
	// When BudgetMicrosUSD is set and cumulative cost crosses it,
	// the loop fails with an actionable error. Subsequent turns
	// don't fire — even if the LLM was about to give a final answer.
	respWithCost := func(content string, cost int64, calls ...types.ToolCall) *types.ChatResponse {
		msg := makeAssistantMsg(content, calls...)
		r := makeChatResp(msg)
		r.Cost.TotalMicrosUSD = cost
		return r
	}
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			respWithCost("", 600, types.ToolCall{
				ID: "c1", Type: "function",
				Function: types.ToolCallFunction{Name: "shell_exec", Arguments: `{"command":"ls"}`},
			}),
			respWithCost("would be the answer", 0), // never fires
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{result: &ExecutionResult{Status: "completed"}}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.BudgetMicrosUSD = 500 // ceiling under the first turn's cost
	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "failed" {
		t.Errorf("Status = %q, want failed", res.Status)
	}
	if !strings.Contains(res.LastError, "cost ceiling") {
		t.Errorf("LastError = %q, want mention of 'cost ceiling'", res.LastError)
	}
	if res.CostMicrosUSD != 600 {
		t.Errorf("CostMicrosUSD = %d, want 600 (the spent amount that crossed the ceiling)", res.CostMicrosUSD)
	}
	// Only the first LLM call should have happened — the ceiling
	// check fires before the second turn.
	if got := llm.calls.Load(); got != 1 {
		t.Errorf("LLM calls = %d, want 1 (loop bailed after first turn)", got)
	}
}

func TestAgentLoop_NoCeilingMeansUnlimited(t *testing.T) {
	// BudgetMicrosUSD == 0 (the default) disables the ceiling. The
	// loop runs to completion regardless of cost.
	respWithCost := func(content string, cost int64) *types.ChatResponse {
		r := makeChatResp(makeAssistantMsg(content))
		r.Cost.TotalMicrosUSD = cost
		return r
	}
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			respWithCost("done", 1_000_000), // huge cost, no cap
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.BudgetMicrosUSD = 0
	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "completed" {
		t.Errorf("Status = %q, want completed (no ceiling)", res.Status)
	}
}

func TestAgentLoop_TurnCostRecords_CapturedPerTurn(t *testing.T) {
	// Per-turn cost telemetry: the loop must surface a TurnCostRecord
	// for each LLM round-trip, including the assistant step ID and
	// the running cumulative for this run. The runner consumes these
	// to emit `turn.completed` events.
	respWithCost := func(content string, cost int64, calls ...types.ToolCall) *types.ChatResponse {
		msg := makeAssistantMsg(content, calls...)
		r := makeChatResp(msg)
		r.Cost.TotalMicrosUSD = cost
		return r
	}
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			respWithCost("", 100, types.ToolCall{ID: "c1", Type: "function", Function: types.ToolCallFunction{Name: "shell_exec", Arguments: `{"command":"ls"}`}}),
			respWithCost("done", 250),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{result: &ExecutionResult{Status: "completed"}}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	res, err := loop.Execute(context.Background(), newAgentLoopSpec(t))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(res.TurnCosts) != 2 {
		t.Fatalf("TurnCosts = %d, want 2 (one per LLM call)", len(res.TurnCosts))
	}
	if res.TurnCosts[0].Turn != 1 || res.TurnCosts[0].CostMicrosUSD != 100 || res.TurnCosts[0].CumulativeMicrosUSD != 100 {
		t.Errorf("TurnCosts[0] = %+v, want {Turn:1 Cost:100 Cumulative:100}", res.TurnCosts[0])
	}
	if res.TurnCosts[0].ToolCallCount != 1 {
		t.Errorf("TurnCosts[0].ToolCallCount = %d, want 1", res.TurnCosts[0].ToolCallCount)
	}
	if res.TurnCosts[1].Turn != 2 || res.TurnCosts[1].CostMicrosUSD != 250 || res.TurnCosts[1].CumulativeMicrosUSD != 350 {
		t.Errorf("TurnCosts[1] = %+v, want {Turn:2 Cost:250 Cumulative:350}", res.TurnCosts[1])
	}
	// StepID on each entry should match the corresponding thinking
	// step so consumers can join the cost back to the assistant turn.
	if res.TurnCosts[0].StepID == "" || res.TurnCosts[1].StepID == "" {
		t.Errorf("TurnCosts entries missing StepID: %+v", res.TurnCosts)
	}
}

func TestAgentLoop_ThinkingStepCarriesPerTurnCost(t *testing.T) {
	// The model-kind step's OutputSummary must surface this turn's
	// LLM cost (cost_micros_usd) and the run-cumulative figure
	// (run_cumulative_cost_micros_usd) so the run-replay UI can
	// render cost next to each "turn N" without joining against
	// the events feed.
	respWithCost := func(content string, cost int64, calls ...types.ToolCall) *types.ChatResponse {
		r := makeChatResp(makeAssistantMsg(content, calls...))
		r.Cost.TotalMicrosUSD = cost
		return r
	}
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			respWithCost("", 100, types.ToolCall{ID: "c1", Type: "function", Function: types.ToolCallFunction{Name: "shell_exec", Arguments: `{"command":"ls"}`}}),
			respWithCost("done", 250),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{result: &ExecutionResult{Status: "completed"}}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	res, err := loop.Execute(context.Background(), newAgentLoopSpec(t))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	modelSteps := make([]types.TaskStep, 0, 2)
	for _, s := range res.Steps {
		if s.Kind == "model" {
			modelSteps = append(modelSteps, s)
		}
	}
	if len(modelSteps) != 2 {
		t.Fatalf("model steps = %d, want 2", len(modelSteps))
	}

	// Turn 1: cost=100, run cumulative=100.
	if got := modelSteps[0].OutputSummary["cost_micros_usd"]; got != int64(100) {
		t.Errorf("turn 1 cost_micros_usd = %v, want 100", got)
	}
	if got := modelSteps[0].OutputSummary["run_cumulative_cost_micros_usd"]; got != int64(100) {
		t.Errorf("turn 1 run_cumulative_cost_micros_usd = %v, want 100", got)
	}
	// Turn 2: cost=250, run cumulative=350.
	if got := modelSteps[1].OutputSummary["cost_micros_usd"]; got != int64(250) {
		t.Errorf("turn 2 cost_micros_usd = %v, want 250", got)
	}
	if got := modelSteps[1].OutputSummary["run_cumulative_cost_micros_usd"]; got != int64(350) {
		t.Errorf("turn 2 run_cumulative_cost_micros_usd = %v, want 350", got)
	}
}

func TestAgentLoop_CumulativeCeilingAppliesPriorChainCost(t *testing.T) {
	// Cumulative ceiling: a fresh run's spend looks small in
	// isolation but, when combined with prior runs in the resume
	// chain (PriorCostMicrosUSD), can already exceed the ceiling.
	// The loop must bail at the first turn that crosses the cap,
	// not run unbounded.
	respWithCost := func(content string, cost int64, calls ...types.ToolCall) *types.ChatResponse {
		r := makeChatResp(makeAssistantMsg(content, calls...))
		r.Cost.TotalMicrosUSD = cost
		return r
	}
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			respWithCost("", 200, types.ToolCall{ID: "c1", Type: "function", Function: types.ToolCallFunction{Name: "shell_exec", Arguments: `{"command":"ls"}`}}),
			respWithCost("would-have-answered", 0),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{result: &ExecutionResult{Status: "completed"}}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.BudgetMicrosUSD = 500
	// Prior chain already spent 400 µUSD. This run can spend at most
	// 100 before hitting the ceiling. Turn 1 spends 200 — so the
	// ceiling fires after turn 1.
	spec.ResumeCheckpoint = &ResumeCheckpoint{
		SourceRunID:        "run-prev",
		PriorCostMicrosUSD: 400,
	}
	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "failed" {
		t.Errorf("Status = %q, want failed (cumulative ceiling)", res.Status)
	}
	if !strings.Contains(res.LastError, "ceiling") {
		t.Errorf("LastError = %q, want mention of ceiling", res.LastError)
	}
	if res.CostMicrosUSD != 200 {
		t.Errorf("CostMicrosUSD = %d, want 200 (this run only)", res.CostMicrosUSD)
	}
	// LLM call 1 happened; call 2 did not — ceiling check is between turns.
	if got := llm.calls.Load(); got != 1 {
		t.Errorf("LLM calls = %d, want 1 (bail before turn 2)", got)
	}
}

func TestAgentLoop_SameRunResumeSeedsCostSpentFromPrePauseTotal(t *testing.T) {
	// Same-run mid-approval resume: the run paused with
	// TotalCostMicrosUSD=X. On resume we don't get a fresh cost
	// counter — costSpent must seed from X so the persisted total
	// after this resume reflects the entire run's spend, not just
	// the post-resume turns.
	saved := []types.Message{
		{Role: "user", Content: "do work"},
		// Pretend turn 1 happened pre-pause and incurred cost X.
		// The conversation tail is an assistant msg with tool_calls
		// that triggers the resume-after-approval dispatch path.
		{Role: "assistant", Content: "", ToolCalls: []types.ToolCall{{ID: "c1", Type: "function", Function: types.ToolCallFunction{Name: "shell_exec", Arguments: `{"command":"ls"}`}}}},
	}
	savedJSON, _ := json.Marshal(saved)

	respWithCost := func(content string, cost int64) *types.ChatResponse {
		r := makeChatResp(makeAssistantMsg(content))
		r.Cost.TotalMicrosUSD = cost
		return r
	}
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			respWithCost("done", 50),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{result: &ExecutionResult{Status: "completed"}}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.ResumeCheckpoint = &ResumeCheckpoint{
		SourceRunID:          spec.Run.ID,
		Reason:               "approved_mid_loop",
		AgentConversation:    savedJSON,
		ThisRunCostMicrosUSD: 100, // pre-pause spend on THIS run
	}
	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "completed" {
		t.Errorf("Status = %q, want completed", res.Status)
	}
	// Pre-pause 100 + post-resume 50 = 150. Without seeding,
	// CostMicrosUSD would be just 50 and the runner would lose the
	// pre-pause portion when overwriting Total.
	if res.CostMicrosUSD != 150 {
		t.Errorf("CostMicrosUSD = %d, want 150 (100 pre-pause + 50 post-resume)", res.CostMicrosUSD)
	}
}

func TestAgentLoop_HTTPRequest_HappyPath(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello from upstream"))
	}))
	defer upstream.Close()

	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "c1", Type: "function",
				Function: types.ToolCallFunction{
					Name:      "http_request",
					Arguments: fmt.Sprintf(`{"url":"%s"}`, upstream.URL),
				},
			})),
			makeChatResp(makeAssistantMsg("got it")),
		},
	}
	// httptest binds 127.0.0.1 — loopback, blocked by default.
	// Allow private IPs for the test scope so the request fires.
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{AllowPrivateIPs: true})
	if _, err := loop.Execute(context.Background(), newAgentLoopSpec(t)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	hasUpstream := false
	for _, m := range llm.lastReqs[1].Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "hello from upstream") {
			hasUpstream = true
		}
	}
	if !hasUpstream {
		t.Errorf("upstream body didn't reach next LLM turn: %+v", llm.lastReqs[1].Messages)
	}
}

func TestAgentLoop_HTTPRequest_BlocksPrivateIPByDefault(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream should NOT be called when private IPs are blocked")
	}))
	defer upstream.Close()

	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "c1", Type: "function",
				Function: types.ToolCallFunction{
					Name:      "http_request",
					Arguments: fmt.Sprintf(`{"url":"%s"}`, upstream.URL),
				},
			})),
			makeChatResp(makeAssistantMsg("got blocked")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	if _, err := loop.Execute(context.Background(), newAgentLoopSpec(t)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	blocked := false
	for _, m := range llm.lastReqs[1].Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "private/loopback/link-local") {
			blocked = true
		}
	}
	if !blocked {
		t.Errorf("private IP not blocked: %+v", llm.lastReqs[1].Messages)
	}
}

func TestAgentLoop_HTTPRequest_BlocksUnsafeScheme(t *testing.T) {
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "c1", Type: "function",
				Function: types.ToolCallFunction{
					Name:      "http_request",
					Arguments: `{"url":"file:///etc/passwd"}`,
				},
			})),
			makeChatResp(makeAssistantMsg("ok")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{AllowPrivateIPs: true})
	if _, err := loop.Execute(context.Background(), newAgentLoopSpec(t)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	rejected := false
	for _, m := range llm.lastReqs[1].Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "scheme") {
			rejected = true
		}
	}
	if !rejected {
		t.Errorf("file:// not rejected: %+v", llm.lastReqs[1].Messages)
	}
}

func TestAgentLoop_HTTPRequest_HostAllowlistEnforced(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream should not be called when not in allowlist")
	}))
	defer upstream.Close()

	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "c1", Type: "function",
				Function: types.ToolCallFunction{
					Name:      "http_request",
					Arguments: fmt.Sprintf(`{"url":"%s"}`, upstream.URL),
				},
			})),
			makeChatResp(makeAssistantMsg("ok")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{
		AllowPrivateIPs: true,
		AllowedHosts:    []string{"example.com"},
	})
	if _, err := loop.Execute(context.Background(), newAgentLoopSpec(t)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	hasAllowlist := false
	for _, m := range llm.lastReqs[1].Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "allowlist") {
			hasAllowlist = true
		}
	}
	if !hasAllowlist {
		t.Errorf("host allowlist not enforced: %+v", llm.lastReqs[1].Messages)
	}
}

func TestAgentLoop_HTTPRequest_TruncatesOversizeBody(t *testing.T) {
	big := strings.Repeat("x", 2000)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, big)
	}))
	defer upstream.Close()

	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "c1", Type: "function",
				Function: types.ToolCallFunction{
					Name:      "http_request",
					Arguments: fmt.Sprintf(`{"url":"%s"}`, upstream.URL),
				},
			})),
			makeChatResp(makeAssistantMsg("ok")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{
		AllowPrivateIPs:  true,
		MaxResponseBytes: 500,
	})
	if _, err := loop.Execute(context.Background(), newAgentLoopSpec(t)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	truncated := false
	for _, m := range llm.lastReqs[1].Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "truncated=true") {
			truncated = true
		}
	}
	if !truncated {
		t.Errorf("oversize body not truncated: %+v", llm.lastReqs[1].Messages)
	}
}

func TestAgentLoop_HTTPRequest_GatedPausesRun(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream should not be called pre-approval")
	}))
	defer upstream.Close()

	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "c1", Type: "function",
				Function: types.ToolCallFunction{
					Name:      "http_request",
					Arguments: fmt.Sprintf(`{"url":"%s"}`, upstream.URL),
				},
			})),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8,
		[]string{"http_request"},
		HTTPRequestPolicy{AllowPrivateIPs: true})
	res, err := loop.Execute(context.Background(), newAgentLoopSpec(t))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "awaiting_approval" {
		t.Errorf("Status = %q, want awaiting_approval", res.Status)
	}
	if len(res.PendingApprovals) != 1 {
		t.Errorf("PendingApprovals = %d, want 1", len(res.PendingApprovals))
	}
}

func TestAgentLoop_ContextCancellation(t *testing.T) {
	// If the run is cancelled mid-loop (operator hits Cancel, gateway
	// shuts down), the loop must exit cleanly with cancelled status.
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "call-1", Type: "function",
				Function: types.ToolCallFunction{Name: "shell_exec", Arguments: `{"command":"ls"}`},
			})),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel
	res, err := loop.Execute(ctx, newAgentLoopSpec(t))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "cancelled" {
		t.Errorf("Status = %q, want cancelled", res.Status)
	}
}
