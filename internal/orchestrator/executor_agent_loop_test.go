package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/hecatehq/hecate/internal/gitrunner"
	"github.com/hecatehq/hecate/internal/runtimeevents"
	"github.com/hecatehq/hecate/internal/sandbox"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/internal/websearch"
	"github.com/hecatehq/hecate/internal/workspace"
	"github.com/hecatehq/hecate/internal/workspacefs"
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
// script in advance — { "model call 1 wants shell_exec(ls)", "model call 2 wants
// final answer" } — and the loop drives through it. Each call records
// what messages it received so we can assert the conversation grew
// correctly.
type scriptedLLM struct {
	responses []*types.ChatResponse
	calls     atomic.Int32
	lastReqs  []types.ChatRequest
}

type cancelAfterChecksContext struct {
	context.Context
	remaining int
}

func (c *cancelAfterChecksContext) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
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
	err           error
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
	return s.response, s.err
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

func newNetworkAgentLoopSpec(t *testing.T) ExecutionSpec {
	t.Helper()
	spec := newAgentLoopSpec(t)
	spec.Task.SandboxNetwork = true
	return spec
}

func TestAgentLoop_FinalAnswerOnFirstModelCall(t *testing.T) {
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
	// model call for resume) and the final-answer summary.
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
	// Realistic two-model-call flow: LLM calls shell_exec, gets the result,
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
		"model.call.started",
		"assistant.text_complete",
		"assistant.tool_call_proposed",
		"model.call.started",
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

	if got := len(cap.byType("model.call.started")); got != 2 {
		t.Fatalf("model.call.started count = %d, want 2", got)
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
		"model.call.started": {
			"model_call_index",
			"model",
			"provider",
			"input_tokens_estimate",
		},
		"assistant.text_complete": {
			"model_call_index",
			"block_index",
			"text",
		},
		"assistant.tool_call_proposed": {
			"model_call_index",
			"tool_call_id",
			"tool_name",
			"input",
		},
		"assistant.final_answer": {
			"model_call_index",
			"summary",
		},
	}
	for _, key := range required[event.Type] {
		if _, ok := event.Data[key]; !ok {
			t.Fatalf("%s missing required data key %q: %+v", event.Type, key, event.Data)
		}
	}
	for _, legacyKey := range []string{"turn", "turn_index", "tool_call_count"} {
		if _, ok := event.Data[legacyKey]; ok {
			t.Fatalf("%s carried legacy data key %q: %+v", event.Type, legacyKey, event.Data)
		}
	}
}

func TestAgentLoop_MaxModelCallsHonored(t *testing.T) {
	// LLM keeps asking for tool calls forever; loop must stop at
	// maxModelCalls and return failed status. Without this cap a runaway
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
		t.Errorf("Status = %q, want failed (max model calls)", res.Status)
	}
	if !strings.Contains(res.LastError, "maximum of 3 model calls") {
		t.Errorf("LastError = %q, want plain-language model-call limit", res.LastError)
	}
	if got := llm.calls.Load(); got != 3 {
		t.Errorf("LLM calls = %d, want 3 (capped)", got)
	}
	conversation := findArtifactByKind(res.Artifacts, "agent_conversation")
	if conversation == nil {
		t.Fatal("max-model-call result omitted the conversation artifact")
	}
	var messages []types.Message
	if err := json.Unmarshal([]byte(conversation.ContentText), &messages); err != nil {
		t.Fatalf("decode max-model-call conversation: %v", err)
	}
	if len(messages) == 0 || messages[len(messages)-1].Role != "tool" || messages[len(messages)-1].ToolCallID != "call-x" {
		t.Fatalf("conversation tail = %+v, want the completed final tool result", messages)
	}
	if pending := pendingToolCallsForResume(messages); len(pending) != 0 {
		t.Fatalf("completed final tool call remained replayable after max-model-call failure: %+v", pending)
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
	if !strings.Contains(res.LastError, "model call 1 failed") {
		t.Errorf("LastError = %q, want 'model call 1 failed'", res.LastError)
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
	// result and decide what to do. Then on the next model call we provide
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
	// the content as the tool result, and the next model call answers.
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
		t.Errorf("read_file content didn't surface to next model call: %+v", secondReq.Messages)
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
		if m.Role == "tool" && (strings.Contains(m.Content, "escapes the workspace root") || strings.Contains(m.Content, "unsafe relative workspace path")) {
			hasEscape = true
		}
	}
	if !hasEscape {
		t.Errorf("traversal not rejected with workspace-escape error: %+v", secondReq.Messages)
	}
}

func TestAgentLoop_ReadFileRejectsSymlinkComponent(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "c1", Type: "function",
				Function: types.ToolCallFunction{Name: "read_file", Arguments: `{"path":"linked/secret.txt"}`},
			})),
			makeChatResp(makeAssistantMsg("Denied.")),
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
	var toolResult string
	for _, m := range llm.lastReqs[1].Messages {
		if m.Role == "tool" && m.ToolCallID == "c1" {
			toolResult = m.Content
		}
	}
	if !strings.Contains(toolResult, "symlink component") {
		t.Fatalf("tool result = %q, want symlink rejection", toolResult)
	}
	if strings.Contains(toolResult, "secret") {
		t.Fatalf("tool result leaked outside file content: %q", toolResult)
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

func TestAgentLoop_ReadFileLineRangeRejectsOversizedFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "huge.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", readFileHardCapBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	_, _, _, errMsg := readWorkspaceFileLineRange(f, readFileDefaultMaxBytes, 1, 1)
	if !strings.Contains(errMsg, "too large for ranged read") {
		t.Fatalf("error = %q, want ranged read size guard", errMsg)
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

func TestRunGitReadCommandCapsOutputWhileReading(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "big.txt")
	runGit(t, dir, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(strings.Repeat("after\n", 200)), 0o644); err != nil {
		t.Fatal(err)
	}

	out, truncated, _, err := runGitReadCommand(context.Background(), dir, 64, "diff", "--no-ext-diff", "--no-textconv", "--", "big.txt")
	if err != nil {
		t.Fatalf("runGitReadCommand: %v", err)
	}
	if !truncated {
		t.Fatalf("truncated = false, want true; output=%q", out)
	}
	if len(out) != 64 {
		t.Fatalf("len(output) = %d, want 64", len(out))
	}
}

func TestRunGitReadCommandSupportsStagedDiffThroughReadOnlyView(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")
	path := filepath.Join(dir, "staged.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "staged.txt")
	runGit(t, dir, "commit", "-m", "initial")
	if err := os.WriteFile(path, []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "staged.txt")

	out, _, _, err := runGitReadCommand(context.Background(), dir, 4096, "diff", "--no-ext-diff", "--no-textconv", "--cached")
	if err != nil {
		t.Fatalf("runGitReadCommand: %v", err)
	}
	if !strings.Contains(out, "-before") || !strings.Contains(out, "+after") {
		t.Fatalf("staged passive diff = %q, want staged change", out)
	}
}

func TestStructuredGitReadsScopeNestedWorkspace(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")
	nested := filepath.Join(dir, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "root.txt"), []byte("before root\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "nested.txt"), []byte("before nested\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	assets := filepath.Join(dir, "assets")
	if err := os.Mkdir(assets, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assets, "asset.bin"), []byte("asset\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("assets/** filter=sibling-driver\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(dir, "root.txt"), []byte("after root\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "nested.txt"), []byte("after nested\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = nested
	status, _, _, err := gitStatusTool(context.Background(), spec, gitStatusArgs{}, 0, time.Now(), "git_status")
	if err != nil {
		t.Fatalf("gitStatusTool: %v", err)
	}
	if !strings.Contains(status, "nested.txt") || strings.Contains(status, "nested/nested.txt") || strings.Contains(status, "root.txt") {
		t.Fatalf("nested git status = %q, want only nested workspace change", status)
	}
	diff, _, _, err := gitDiffTool(context.Background(), spec, gitDiffArgs{Path: "nested.txt"}, 0, time.Now(), "git_diff")
	if err != nil {
		t.Fatalf("gitDiffTool: %v", err)
	}
	if !strings.Contains(diff, "+after nested") || strings.Contains(diff, "+after root") {
		t.Fatalf("nested git diff = %q, want only nested workspace change", diff)
	}
}

func TestParseGitStatusPorcelainZNormalizesNestedPaths(t *testing.T) {
	output := "## main\x00 M nested/file.txt\x00?? nested/line\nname.txt\x00R  nested/new.txt\x00nested/old.txt\x00"
	branch, entries, err := parseGitStatusPorcelainZ(output, "nested", false)
	if err != nil {
		t.Fatalf("parseGitStatusPorcelainZ: %v", err)
	}
	if branch != "main" {
		t.Fatalf("branch = %q, want main", branch)
	}
	want := []string{` M file.txt`, `?? "line\nname.txt"`, `R  old.txt -> new.txt`}
	if strings.Join(entries, "\n") != strings.Join(want, "\n") {
		t.Fatalf("entries = %#v, want %#v", entries, want)
	}
	if _, _, err := parseGitStatusPorcelainZ("## main\x00 M sibling.txt\x00", "nested", false); err == nil || !strings.Contains(err.Error(), "outside the workspace") {
		t.Fatalf("outside-path parse error = %v, want refusal", err)
	}
	truncatedOutput := "## main\x00 M nested/complete.txt\x00?? nested/incomplete"
	_, truncatedEntries, err := parseGitStatusPorcelainZ(truncatedOutput, "nested", true)
	if err != nil || len(truncatedEntries) != 1 || !strings.Contains(truncatedEntries[0], "complete.txt") {
		t.Fatalf("truncated entries = %#v error=%v, want one complete entry", truncatedEntries, err)
	}
	if _, _, err := parseGitStatusPorcelainZ(truncatedOutput, "nested", false); err == nil {
		t.Fatal("strict parser accepted incomplete status output")
	}
	for _, path := range []string{"escape\x1b[31m.txt", string([]byte{'b', 'a', 'd', 0xff})} {
		display := displayGitStatusPath(path)
		if !strings.HasPrefix(display, `"`) || strings.ContainsRune(display, '\x1b') || !utf8.ValidString(display) {
			t.Fatalf("displayGitStatusPath(%q) = %q, want safe ASCII quoting", path, display)
		}
	}
}

func TestGitStatusToolReturnsCompleteEntriesWhenOutputIsTruncated(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("tracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "tracked.txt")
	runGit(t, dir, "commit", "-m", "initial")
	for i := range 1800 {
		name := fmt.Sprintf("untracked-%04d-%s.txt", i, strings.Repeat("x", 40))
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir
	status, _, _, err := gitStatusTool(context.Background(), spec, gitStatusArgs{}, 0, time.Now(), "git_status")
	if err != nil {
		t.Fatalf("gitStatusTool: %v", err)
	}
	if !strings.Contains(status, "truncated=true") || strings.Contains(status, "malformed Git status") {
		t.Fatalf("truncated git status = %q, want bounded complete entries", status)
	}
	if len(status) > gitDiffDefaultMaxBytes {
		t.Fatalf("len(truncated git status) = %d, want <= %d", len(status), gitDiffDefaultMaxBytes)
	}
}

func TestGitReadRunnerUsesSanitizedEnvironment(t *testing.T) {
	t.Setenv("HECATE_TEST_SECRET", "must-not-leak")

	out := strings.Join(gitReadRunner().Env, "\n")
	if strings.Contains(out, "HECATE_TEST_SECRET=must-not-leak") {
		t.Fatalf("Git reader inherited non-allowlisted env:\n%s", out)
	}
	if !strings.Contains(out, "PATH=") {
		t.Fatalf("sanitized env omitted PATH:\n%s", out)
	}
	for _, want := range []string{
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_NO_LAZY_FETCH=1",
		"GIT_TERMINAL_PROMPT=0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("hardened Git reader omitted %q:\n%s", want, out)
		}
	}
}

func TestRunGitReadCommandRefusesRepositoryConversionFiltersWithoutWrapper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses POSIX content-conversion helpers")
	}
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()

	for _, tc := range []struct {
		name       string
		configKey  string
		commandArg []string
	}{
		{name: "clean filter during diff", configKey: "filter.evil.clean", commandArg: []string{"diff", "--no-ext-diff", "--no-textconv"}},
		{name: "process filter during status", configKey: "filter.evil.process", commandArg: []string{"status", "--porcelain=v1", "-b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			runGit(t, dir, "init")
			runGit(t, dir, "config", "user.email", "test@example.com")
			runGit(t, dir, "config", "user.name", "Test User")
			if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("*.txt filter=evil\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			tracked := filepath.Join(dir, "tracked.txt")
			if err := os.WriteFile(tracked, []byte("before\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			runGit(t, dir, "add", ".gitattributes", "tracked.txt")
			runGit(t, dir, "commit", "-m", "initial")

			marker := filepath.Join(t.TempDir(), "filter-called")
			helper := filepath.Join(t.TempDir(), "filter")
			script := fmt.Sprintf("#!/bin/sh\nprintf called > %q\ncat\n", marker)
			if err := os.WriteFile(helper, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			runGit(t, dir, "config", tc.configKey, helper)
			if err := os.WriteFile(tracked, []byte("after\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			_, _, _, err := runGitReadCommand(context.Background(), dir, 4096, tc.commandArg...)
			if err == nil || !strings.Contains(err.Error(), "content-conversion filter") {
				t.Fatalf("runGitReadCommand error = %v, want conversion-filter refusal", err)
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("repository conversion helper ran during passive Git read; stat error = %v", err)
			}
		})
	}
}

func TestRunGitReadCommandRefusesRepositoryAttributesBackedByGlobalFilter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX content-conversion helper and home config")
	}
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("*.bin filter=global-driver\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tracked := filepath.Join(dir, "tracked.bin")
	if err := os.WriteFile(tracked, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".gitattributes", "tracked.bin")
	runGit(t, dir, "commit", "-m", "initial")

	marker := filepath.Join(t.TempDir(), "global-filter-called")
	helper := filepath.Join(t.TempDir(), "global-filter")
	script := fmt.Sprintf("#!/bin/sh\nprintf called > %q\ncat\n", marker)
	if err := os.WriteFile(helper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	globalConfig := fmt.Sprintf("[filter \"global-driver\"]\n\tclean = %q\n", helper)
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(globalConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tracked, []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, _, err := runGitReadCommand(context.Background(), dir, 4096, "diff", "--no-ext-diff", "--no-textconv")
	if err == nil || !strings.Contains(err.Error(), "content-conversion filter") {
		t.Fatalf("runGitReadCommand error = %v, want repository attribute refusal", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("global conversion helper ran during passive Git read; stat error = %v", err)
	}
}

func TestRunGitReadCommandIgnoresUserGlobalAttributes(t *testing.T) {
	configHome := t.TempDir()
	if err := os.Mkdir(filepath.Join(configHome, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configHome, "git", "attributes"), []byte("* filter=user-global\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", configHome)
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")
	tracked := filepath.Join(dir, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "tracked.txt")
	runGit(t, dir, "commit", "-m", "initial")
	if err := os.WriteFile(tracked, []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, _, err := runGitReadCommand(context.Background(), dir, 4096, "diff", "--no-ext-diff", "--no-textconv", "--", ".")
	if err != nil {
		t.Fatalf("runGitReadCommand: %v", err)
	}
	if !strings.Contains(out, "+after") {
		t.Fatalf("passive diff = %q, want worktree change", out)
	}
}

func TestRunGitReadCommandRefusesIndexedAttributesMissingFromWorktree(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("*.txt filter=indexed-driver\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tracked := filepath.Join(dir, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".gitattributes", "tracked.txt")
	runGit(t, dir, "commit", "-m", "initial")
	if err := os.Remove(filepath.Join(dir, ".gitattributes")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tracked, []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, _, err := runGitReadCommand(context.Background(), dir, 4096, "diff", "--no-ext-diff", "--no-textconv", "--", ".")
	if err == nil || !strings.Contains(err.Error(), "content-conversion filter") {
		t.Fatalf("runGitReadCommand error = %v, want indexed attribute refusal", err)
	}
}

func TestRunGitReadCommandRefusesReservedFilterDriverNames(t *testing.T) {
	for _, driver := range []string{"unset", "unspecified"} {
		t.Run(driver, func(t *testing.T) {
			dir := t.TempDir()
			runGit(t, dir, "init")
			runGit(t, dir, "config", "user.email", "test@example.com")
			runGit(t, dir, "config", "user.name", "Test User")
			if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("*.txt filter="+driver+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("tracked\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			runGit(t, dir, "add", ".")
			runGit(t, dir, "commit", "-m", "initial")
			_, _, _, err := runGitReadCommand(context.Background(), dir, 4096, "status", "--porcelain=v1", "-b")
			if err == nil || !strings.Contains(err.Error(), "content-conversion filter") {
				t.Fatalf("runGitReadCommand error = %v, want reserved-driver refusal", err)
			}
		})
	}
}

func TestRunGitReadCommandPreservesWhitespaceInTrackedPaths(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")
	whitespaceDir := filepath.Join(dir, " nested ")
	if err := os.Mkdir(whitespaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(whitespaceDir, ".gitattributes"), []byte("*.txt filter=space-driver\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tracked := filepath.Join(whitespaceDir, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "initial")
	if err := os.WriteFile(tracked, []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, _, err := runGitReadCommand(context.Background(), dir, 4096, "diff", "--no-ext-diff", "--no-textconv", "--", ".")
	if err == nil || !strings.Contains(err.Error(), "content-conversion filter") {
		t.Fatalf("runGitReadCommand error = %v, want whitespace-path attribute refusal", err)
	}
}

func TestEffectiveGitAttributeParsingStopsWhenContextCancels(t *testing.T) {
	ctx := &cancelAfterChecksContext{Context: context.Background(), remaining: 4}
	output := strings.Repeat("nested/file.txt\x00text\x00set\x00", 100)
	err := gitrunner.RejectEffectiveContentConversionFilters(ctx, output)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("rejectEffectiveGitConversionFilters error = %v, want context cancellation", err)
	}
}

func TestRunGitReadCommandRefusesGitInfoConversionAttributes(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(dir, "tracked.bin"), []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "tracked.bin")
	runGit(t, dir, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(dir, ".git", "info", "attributes"), []byte("*.bin filter=local-info\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, _, err := runGitReadCommand(context.Background(), dir, 4096, "status", "--porcelain=v1", "-b")
	if err == nil || !strings.Contains(err.Error(), "content-conversion filter") {
		t.Fatalf("runGitReadCommand error = %v, want Git info attribute refusal", err)
	}
}

func TestGitAttributeResolutionCancelsWhenWorktreeAttributesAreFIFO(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mkfifo is not available on Windows")
	}
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("tracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "tracked.txt")
	runGit(t, dir, "commit", "-m", "initial")
	if err := exec.Command("mkfifo", filepath.Join(dir, ".gitattributes")).Run(); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	view, err := gitReadRunner().NewReadOnlyView(context.Background(), dir)
	if err != nil {
		t.Fatalf("NewReadOnlyView: %v", err)
	}
	defer view.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = view.RejectContentConversionAttributes(ctx)
	if err == nil {
		t.Fatal("rejectGitReadConversionAttributes succeeded, want cancellation")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("FIFO attribute cancellation took %v", elapsed)
	}
}

func TestRejectEffectiveGitConversionFilters(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "driver", value: "lfs", wantErr: true},
		{name: "set", value: "set", wantErr: true},
		{name: "ambiguous unset", value: "unset", wantErr: true},
		{name: "literal unspecified", value: "unspecified", wantErr: true},
		{name: "unrelated attribute", value: "true", wantErr: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attribute := "filter"
			if tc.name == "unrelated attribute" {
				attribute = "text"
			}
			output := "tracked.bin\x00" + attribute + "\x00" + tc.value + "\x00"
			err := gitrunner.RejectEffectiveContentConversionFilters(context.Background(), output)
			if (err != nil) != tc.wantErr {
				t.Fatalf("rejectEffectiveGitConversionFilters(%q) error = %v, wantErr %v", tc.value, err, tc.wantErr)
			}
		})
	}
}

func TestRunGitReadCommandDisablesRepositoryFSMonitor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX fsmonitor hook")
	}
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "main.go")
	runGit(t, dir, "commit", "-m", "initial")

	marker := filepath.Join(t.TempDir(), "fsmonitor-called")
	helper := filepath.Join(t.TempDir(), "fsmonitor")
	script := fmt.Sprintf("#!/bin/sh\nprintf called > %q\n", marker)
	if err := os.WriteFile(helper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "config", "core.fsmonitor", helper)
	runGit(t, dir, "config", "core.fsmonitorHookVersion", "2")

	if _, _, _, err := runGitReadCommand(context.Background(), dir, 4096, "status", "--porcelain=v1", "-b"); err != nil {
		t.Fatalf("runGitReadCommand: %v", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("repository fsmonitor helper ran during hardened Git read; stat error = %v", err)
	}
}

func TestRunGitReadCommandDoesNotRefreshIndex(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")
	tracked := filepath.Join(dir, "main.go")
	if err := os.WriteFile(tracked, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "main.go")
	runGit(t, dir, "commit", "-m", "initial")

	indexPath := filepath.Join(dir, ".git", "index")
	before, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	// Force Git to inspect the worktree entry. A normal `git status` writes the
	// refreshed stat data back to the index; the structured read must not.
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(tracked, future, future); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := runGitReadCommand(context.Background(), dir, 4096, "status", "--porcelain=v1", "-b"); err != nil {
		t.Fatalf("runGitReadCommand: %v", err)
	}
	after, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("hardened Git read refreshed the repository index")
	}
}

func TestCombineGitReadOutputSeparatesStdoutAndStderr(t *testing.T) {
	cases := []struct {
		name   string
		stdout string
		stderr string
		want   string
	}{
		{
			name:   "stdout without trailing newline",
			stdout: "stdout",
			stderr: "stderr",
			want:   "stdout\nstderr",
		},
		{
			name:   "stdout with trailing newline",
			stdout: "stdout\n",
			stderr: "stderr",
			want:   "stdout\nstderr",
		},
		{
			name:   "stderr only",
			stdout: "",
			stderr: "stderr",
			want:   "stderr",
		},
		{
			name:   "blank stderr ignored",
			stdout: "stdout",
			stderr: " \n",
			want:   "stdout",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := combineGitReadOutput(tc.stdout, tc.stderr); got != tc.want {
				t.Fatalf("combineGitReadOutput() = %q, want %q", got, tc.want)
			}
		})
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

func TestAgentLoop_ConversationPersistsAcrossModelCalls(t *testing.T) {
	// Pin the resume contract: every model call writes a snapshot to the
	// same stable artifact ID (`convo-{run.ID}`). A test stub records
	// each upsert so we can verify (a) the artifact ID is stable
	// across model calls, (b) the JSON-decoded payload reflects the latest
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
		t.Fatalf("conversation upserts = %d, want >= 2 (one per model call)", len(convoUpserts))
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
		t.Errorf("last snapshot missing final assistant message: %s", last.ContentText)
	}
	// Tool result was in the conversation between model call 1 and model call 2,
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
		t.Fatalf("LLM calls = %d, want 1 (single resumed model call)", len(llm.lastReqs))
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

func TestTruncateConversationToRunModelCall(t *testing.T) {
	// The source Run owns the final two model calls; the first two assistant
	// responses are inherited from prior Runs in the conversation artifact.
	conv := []types.Message{
		{Role: "system", Content: "be concise"},
		{Role: "user", Content: "first prompt"},
		{Role: "assistant", Content: "", ToolCalls: []types.ToolCall{{ID: "inherited-1", Function: types.ToolCallFunction{Name: "shell_exec"}}}},
		{Role: "tool", Content: "prior result", ToolCallID: "inherited-1"},
		{Role: "assistant", Content: "prior answer"},
		{Role: "user", Content: "continue"},
		{Role: "assistant", Content: "", ToolCalls: []types.ToolCall{{ID: "source-1", Function: types.ToolCallFunction{Name: "shell_exec"}}}},
		{Role: "tool", Content: "source result", ToolCallID: "source-1"},
		{Role: "assistant", Content: "source answer"},
	}

	t.Run("source Run model call 1 preserves inherited context", func(t *testing.T) {
		got, err := truncateConversationToRunModelCall(conv, 2, 1)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(got) != 6 {
			t.Fatalf("len = %d, want 6 inherited-context messages", len(got))
		}
		if got[len(got)-1].Role != "user" || got[len(got)-1].Content != "continue" {
			t.Errorf("tail = %+v, want source Run prompt", got[len(got)-1])
		}
	})

	t.Run("source Run model call 2 keeps its first tool result", func(t *testing.T) {
		got, err := truncateConversationToRunModelCall(conv, 2, 2)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(got) != 8 {
			t.Fatalf("len = %d, want 8", len(got))
		}
		if got[len(got)-1].Role != "tool" || got[len(got)-1].ToolCallID != "source-1" {
			t.Errorf("tail wrong: %+v", got[len(got)-1])
		}
	})

	t.Run("invalid Run-local ranges and artifact mismatch fail", func(t *testing.T) {
		if _, err := truncateConversationToRunModelCall(conv, 2, 3); err == nil {
			t.Errorf("model call 3 of a 2-call source Run should fail")
		}
		if _, err := truncateConversationToRunModelCall(conv, 2, 0); err == nil {
			t.Errorf("model call 0 should fail")
		}
		if _, err := truncateConversationToRunModelCall(conv, 0, 1); err == nil {
			t.Errorf("source Run with zero model calls should fail")
		}
		if _, err := truncateConversationToRunModelCall(conv, 5, 1); err == nil {
			t.Errorf("source Run count greater than conversation count should fail")
		}
	})

	t.Run("does not mutate input", func(t *testing.T) {
		before := len(conv)
		_, _ = truncateConversationToRunModelCall(conv, 2, 2)
		if len(conv) != before {
			t.Errorf("input mutated: len changed from %d to %d", before, len(conv))
		}
	})
}

func TestCountAssistantMessages(t *testing.T) {
	cases := []struct {
		name string
		msgs []types.Message
		want int
	}{
		{"empty", nil, 0},
		{"user only", []types.Message{{Role: "user", Content: "hi"}}, 0},
		{"three model calls", []types.Message{
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
			if got := countAssistantMessages(tc.msgs); got != tc.want {
				t.Errorf("countAssistantMessages = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestAgentLoop_RetryFromModelCall_TruncatedConversationDrivesNextLLMCall(t *testing.T) {
	// Simulate a retry-from-model-call where the runner has already
	// truncated the conversation (e.g. operator clicked "retry from
	// model call 2"). The loop should see the truncated history, call the
	// LLM again at that point, and run normally from there. We
	// pre-truncate to model call 2 (drops assistant_2 onwards), leaving
	// system+user+assistant_1+tool_1 — the next LLM call happens at
	// model call 2 with that context.
	saved := []types.Message{
		{Role: "user", Content: "list things"},
		{Role: "assistant", Content: "", ToolCalls: []types.ToolCall{{ID: "c1", Type: "function", Function: types.ToolCallFunction{Name: "shell_exec", Arguments: `{"command":"ls"}`}}}},
		{Role: "tool", Content: "result1", ToolCallID: "c1"},
		// model call 2's assistant message + everything after has been
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
		LastStepIndex:     0, // every new Run starts its Step indices at 1
	}
	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "completed" {
		t.Fatalf("Status = %q, want completed", res.Status)
	}
	if len(llm.lastReqs) != 1 {
		t.Fatalf("LLM calls = %d, want 1 (retry runs the truncated model call once)", len(llm.lastReqs))
	}
	// Critical: the LLM saw the prior context (3 messages), not the
	// dropped assistant_2. This is what lets the LLM produce a
	// different answer than the original model call 2.
	got := llm.lastReqs[0].Messages
	if len(got) != 3 {
		t.Fatalf("LLM saw %d messages, want 3 (the truncated context)", len(got))
	}
	if got[2].Role != "tool" || got[2].ToolCallID != "c1" {
		t.Errorf("last message before retry should be tool result for c1, got %+v", got[2])
	}
	// And the retry produced an assistant final answer, so the loop
	// completed (no further model calls).
	if res.Steps[0].Index != 1 {
		t.Errorf("first step index = %d, want 1 (fresh-numbered for retry-from-model-call)", res.Steps[0].Index)
	}
}

func TestAgentLoop_GatedToolPausesAndEmitsApproval(t *testing.T) {
	// LLM asks for shell_exec, which is gated. Loop must pause:
	// status=awaiting_approval, one approval in PendingApprovals
	// covering the model call, conversation persisted, shell NOT executed.
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
	// continues to the next model call. Verifies that gating is opt-in by
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
			if _, ok := event.data["bytes_written"].(int); !ok {
				t.Fatalf("bytes_written type = %T, want int", event.data["bytes_written"])
			}
		}
	}
	if eventCount != 2 {
		t.Fatalf("tool.file.patch apply_patch events = %d, want 2: %+v", eventCount, events)
	}
}

func TestAgentLoop_ApplyPatchToolIgnoresBlankSeparatorLines(t *testing.T) {
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
		"\n" +
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
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, NewFileExecutor(workspace.NewLocalWorkspace()), &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir
	if _, err := loop.Execute(context.Background(), spec); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "old.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "alpha\ngamma\n" {
		t.Fatalf("old.txt = %q, want patched content", string(content))
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

func TestAgentLoop_ApplyPatchToolRejectsReadOnlyWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	patchText := "*** Begin Patch\n" +
		"*** Update File: old.txt\n" +
		"@@\n" +
		" alpha\n" +
		"-beta\n" +
		"+gamma\n" +
		"*** End Patch\n"
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir
	spec.Task.SandboxReadOnly = true

	output, step, artifacts, err := applyPatchTool(spec, applyPatchArgs{PatchText: patchText}, 1, time.Now().UTC(), "patch-1", "apply_patch")
	if err != nil {
		t.Fatalf("applyPatchTool: %v", err)
	}
	if step != nil || len(artifacts) != 0 {
		t.Fatalf("expected preflight-only denial, got step=%+v artifacts=%+v", step, artifacts)
	}
	if !strings.Contains(output, "write access is disabled") {
		t.Fatalf("output = %q, want read-only denial", output)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "alpha\nbeta\n" {
		t.Fatalf("read-only patch changed file: %q", string(content))
	}
}

func TestAgentLoop_ReadOnlyProposalToolsAuditApplyAttemptsAndAllowProposals(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	patchText := "*** Begin Patch\n" +
		"*** Update File: old.txt\n" +
		"@@\n" +
		" alpha\n" +
		"-beta\n" +
		"+gamma\n" +
		"*** End Patch\n"
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir
	spec.Task.SandboxAllowedRoot = dir
	spec.Task.SandboxReadOnly = true
	spec.Run.WorkspacePath = dir
	var blockedEvents int
	spec.EmitRunEvent = func(eventType string, data map[string]any) {
		if eventType == runtimeevents.EventPolicyToolBlocked.String() && data["policy"] == "sandbox_read_only" {
			blockedEvents++
		}
	}
	dispatcher := &agentLoopToolDispatcher{file: NewFileExecutor(workspace.NewLocalWorkspace())}

	applyCalls := []types.ToolCall{
		agentLoopToolCall("edit-apply", "file_edit", `{"path":"old.txt","old_text":"beta","new_text":"gamma"}`),
		agentLoopToolCall("patch-apply", "apply_patch", mustJSON(t, map[string]any{"patch_text": patchText})),
	}
	for index, call := range applyCalls {
		result, err := dispatcher.Dispatch(context.Background(), spec, call, index+1, nil, nil)
		if err != nil {
			t.Fatalf("Dispatch(%s): %v", call.Function.Name, err)
		}
		if result.Step == nil || result.Step.Status != "completed" || result.Step.Phase != "policy" || result.Step.Result != telemetry.ResultDenied || !result.ToolError {
			t.Fatalf("Dispatch(%s) = %+v, want audited denied policy step", call.Function.Name, result)
		}
	}
	if blockedEvents != len(applyCalls) {
		t.Fatalf("policy.tool_blocked events = %d, want %d", blockedEvents, len(applyCalls))
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "alpha\nbeta\n" {
		t.Fatalf("read-only apply attempts changed file: %q", content)
	}

	proposalCalls := []types.ToolCall{
		agentLoopToolCall("edit-propose", "file_edit", `{"path":"old.txt","old_text":"beta","new_text":"gamma","propose":true}`),
		agentLoopToolCall("patch-propose", "apply_patch", mustJSON(t, map[string]any{"patch_text": patchText, "propose": true})),
	}
	for index, call := range proposalCalls {
		result, err := dispatcher.Dispatch(context.Background(), spec, call, index+3, nil, nil)
		if err != nil {
			t.Fatalf("Dispatch(%s proposal): %v", call.Function.Name, err)
		}
		if result.Step == nil || result.Step.Status != "completed" || result.Step.Result != telemetry.ResultSuccess || result.ToolError || len(result.Artifacts) == 0 || result.Artifacts[0].Status != "proposed" {
			t.Fatalf("Dispatch(%s proposal) = %+v, want successful proposed artifact", call.Function.Name, result)
		}
	}
}

func TestAgentLoop_ValidatePatchOperationPathRejectsEmptyPath(t *testing.T) {
	for _, kind := range []string{"add", "delete", "update"} {
		t.Run(kind, func(t *testing.T) {
			errMsg := validatePatchOperationPath(patchOperation{Kind: kind, Path: " \t"})
			if !strings.Contains(errMsg, "file path is required") {
				t.Fatalf("error = %q, want path-required message", errMsg)
			}
		})
	}
}

func TestAgentLoop_PreparePatchOperationRejectsLargeTargets(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "large.txt"), []byte(strings.Repeat("x", fileEditHardCapBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	fsys, err := workspacefs.New(dir)
	if err != nil {
		t.Fatalf("New workspacefs: %v", err)
	}
	for _, kind := range []string{"delete", "update"} {
		t.Run(kind, func(t *testing.T) {
			_, _, _, errMsg := preparePatchOperation(fsys, "large.txt", patchOperation{
				Kind: kind,
				Path: "large.txt",
				Lines: []string{
					"@@\n",
					"-x\n",
					"+y\n",
				},
			})
			if !strings.Contains(errMsg, "too large") {
				t.Fatalf("error = %q, want too-large guard", errMsg)
			}
		})
	}
}

func TestAgentLoop_ApplyPatchToolRejectsSymlinkComponents(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Symlink requires elevated privileges on Windows")
	}
	dir := t.TempDir()
	outside := t.TempDir()
	outsidePath := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(outsidePath, []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "linked")); err != nil {
		t.Fatal(err)
	}
	patchText := "*** Begin Patch\n" +
		"*** Update File: linked/secret.txt\n" +
		"@@\n" +
		"-secret\n" +
		"+leaked\n" +
		"*** End Patch\n"
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir

	output, step, artifacts, err := applyPatchTool(spec, applyPatchArgs{PatchText: patchText}, 1, time.Now().UTC(), "patch-1", "apply_patch")
	if err != nil {
		t.Fatalf("applyPatchTool: %v", err)
	}
	if step != nil || len(artifacts) != 0 {
		t.Fatalf("expected preflight-only denial, got step=%+v artifacts=%+v", step, artifacts)
	}
	if !strings.Contains(output, "symlink component") {
		t.Fatalf("output = %q, want symlink rejection", output)
	}
	content, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "secret\n" {
		t.Fatalf("symlinked outside file changed: %q", string(content))
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

func TestAgentLoop_CrossRunResumeRegatesPendingCallsBeforeDispatch(t *testing.T) {
	// Approval authority belongs to one Run. A rejected or cancelled source
	// Run can still have a trailing assistant tool call, but a new Run must not
	// inherit permission to execute it.
	saved := []types.Message{
		{Role: "user", Content: "summarize the working directory"},
		{Role: "assistant", Content: "I need to inspect.", ToolCalls: []types.ToolCall{{
			ID: "call-1", Type: "function",
			Function: types.ToolCallFunction{Name: "shell_exec", Arguments: `{"command":"ls"}`},
		}}},
	}
	savedJSON, _ := json.Marshal(saved)

	llm := &scriptedLLM{}
	shell := &stubExecutor{}
	loop := NewAgentLoopExecutor(llm, shell, &stubExecutor{}, &stubExecutor{}, 8, []string{"shell_exec"}, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.ResumeCheckpoint = &ResumeCheckpoint{
		SourceRunID:                          "run-source",
		AgentConversation:                    savedJSON,
		Reason:                               "resume_after_rejected_approval",
		ThisRunModelCallCount:                0,
		PendingToolCallsOriginRunID:          "run-source",
		PendingToolCallsOriginModelCallIndex: 3,
	}
	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "awaiting_approval" {
		t.Fatalf("Status = %q, want awaiting_approval; LastError=%q", res.Status, res.LastError)
	}
	if len(shell.calls) != 0 {
		t.Fatalf("cross-Run pending shell call executed without fresh approval: %+v", shell.calls)
	}
	if got := llm.calls.Load(); got != 0 {
		t.Fatalf("LLM calls = %d, want zero before fresh approval", got)
	}
	if len(res.PendingApprovals) != 1 || res.PendingApprovals[0].Kind != "agent_loop_tool_call" {
		t.Fatalf("PendingApprovals = %+v, want one fresh tool-call approval", res.PendingApprovals)
	}
	if len(res.Steps) != 1 || res.Steps[0].Kind != "approval" || res.Steps[0].ApprovalID != res.PendingApprovals[0].ID {
		t.Fatalf("Steps = %+v, want one linked approval Step", res.Steps)
	}
	if res.PendingApprovals[0].StepID != res.Steps[0].ID {
		t.Fatalf("approval StepID = %q, want %q", res.PendingApprovals[0].StepID, res.Steps[0].ID)
	}
	if got := res.Steps[0].Input[agentLoopSourceRunIDKey]; got != "run-source" {
		t.Fatalf("approval source_run_id = %v, want run-source", got)
	}
	if got := res.Steps[0].Input[agentLoopSourceModelCallIndexKey]; got != 3 {
		t.Fatalf("approval source_model_call_index = %v, want 3", got)
	}
	if _, found := res.Steps[0].Input[agentLoopModelCallIndexKey]; found {
		t.Fatalf("cross-Run approval attributed a model call to the new Run: %+v", res.Steps[0].Input)
	}
	if res.ModelCallCount != 0 || strings.Contains(res.Steps[0].Title, "model call 0") {
		t.Fatalf("cross-Run approval accounting = calls %d title %q, want source provenance without a new call", res.ModelCallCount, res.Steps[0].Title)
	}
	conversation := findArtifactByKind(res.Artifacts, "agent_conversation")
	if conversation == nil || !strings.Contains(conversation.ContentText, `"shell_exec"`) {
		t.Fatalf("fresh approval did not retain the pending conversation: %+v", conversation)
	}
}

func TestAgentLoop_SameRunRecoveryRegatesPendingCallsWithoutDurableApproval(t *testing.T) {
	savedJSON, err := json.Marshal([]types.Message{
		{Role: "user", Content: "summarize the working directory"},
		{Role: "assistant", Content: "I need to inspect.", ToolCalls: []types.ToolCall{{
			ID: "call-1", Type: "function",
			Function: types.ToolCallFunction{Name: "shell_exec", Arguments: `{"command":"ls"}`},
		}}},
	})
	if err != nil {
		t.Fatalf("marshal checkpoint: %v", err)
	}

	llm := &scriptedLLM{}
	shell := &stubExecutor{}
	loop := NewAgentLoopExecutor(llm, shell, &stubExecutor{}, &stubExecutor{}, 8, []string{"shell_exec"}, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.ResumeCheckpoint = &ResumeCheckpoint{
		SourceRunID:           spec.Run.ID,
		SameRun:               true,
		AgentConversation:     savedJSON,
		Reason:                "same_run_progress",
		ThisRunModelCallCount: 1,
	}

	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "awaiting_approval" {
		t.Fatalf("Status = %q, want awaiting_approval; LastError=%q", res.Status, res.LastError)
	}
	if len(shell.calls) != 0 {
		t.Fatalf("same-Run recovered shell call executed without durable approval: %+v", shell.calls)
	}
	if got := llm.calls.Load(); got != 0 {
		t.Fatalf("LLM calls = %d, want zero before approval", got)
	}
	if len(res.PendingApprovals) != 1 || len(res.Steps) != 1 || res.PendingApprovals[0].StepID != res.Steps[0].ID {
		t.Fatalf("approval pause = approvals %+v steps %+v, want one linked fresh approval", res.PendingApprovals, res.Steps)
	}
}

func TestAgentLoop_ToolsDisabledResumeDeniesPreviouslyPendingCall(t *testing.T) {
	t.Parallel()
	toolsEnabled := false
	savedJSON, err := json.Marshal([]types.Message{
		{Role: "user", Content: "summarize the working directory"},
		{Role: "assistant", Content: "I need to inspect.", ToolCalls: []types.ToolCall{
			agentLoopToolCall("call-1", "shell_exec", `{"command":"ls"}`),
		}},
	})
	if err != nil {
		t.Fatalf("marshal resume checkpoint: %v", err)
	}
	llm := &scriptedLLM{responses: []*types.ChatResponse{
		makeChatResp(makeAssistantMsg("I cannot inspect the workspace with this preset.")),
	}}
	shell := &stubExecutor{}
	loop := NewAgentLoopExecutor(llm, shell, &stubExecutor{}, &stubExecutor{}, 8, []string{"shell_exec"}, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.AgentPresetID = "review_qa"
	spec.Task.AgentPresetToolsEnabled = &toolsEnabled
	spec.ResumeCheckpoint = &ResumeCheckpoint{
		SourceRunID:                          "run-before-upgrade",
		AgentConversation:                    savedJSON,
		Reason:                               "approved_mid_loop",
		PendingToolCallsOriginRunID:          "run-before-upgrade",
		PendingToolCallsOriginModelCallIndex: 1,
	}

	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "completed" || len(shell.calls) != 0 {
		t.Fatalf("result status = %q shell calls = %d, want completed without dispatch", res.Status, len(shell.calls))
	}
	if len(llm.lastReqs) != 1 || len(llm.lastReqs[0].Tools) != 0 {
		t.Fatalf("post-resume request = %+v, want one zero-tool request", llm.lastReqs)
	}
	foundDeniedResult := false
	for _, message := range llm.lastReqs[0].Messages {
		if message.Role == "tool" && message.ToolCallID == "call-1" && message.ToolError && strings.Contains(message.Content, "tools are disabled") {
			foundDeniedResult = true
		}
	}
	if !foundDeniedResult {
		t.Fatalf("post-resume messages = %+v, want policy-denied pending tool result", llm.lastReqs[0].Messages)
	}
}

func TestAgentLoop_GatedToolListedWithMultipleToolsInModelCall(t *testing.T) {
	// LLM asks for both a gated and a non-gated tool in one model call.
	// We pause for approval (any gated tool gates the whole model call);
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
		t.Fatalf("PendingApprovals = %d, want 1 (one approval covers the whole model call)", len(res.PendingApprovals))
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

func TestAgentLoop_ToolsDisabledDoesNotRequireToolCapableModel(t *testing.T) {
	t.Parallel()
	toolsEnabled := false
	llm := &erroringLLM{err: fmt.Errorf("provider ollama call failed: upstream error: model does not support tools")}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.AgentPresetToolsEnabled = &toolsEnabled
	spec.Run.Model = "smollm2:135m"

	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "failed" || !strings.Contains(res.LastError, "does not support tools") {
		t.Fatalf("result = %+v, want underlying provider error", res)
	}
	if strings.Contains(res.LastError, "tool-capable") || strings.Contains(res.LastError, "agent_loop requires") {
		t.Fatalf("LastError = %q, must not claim a zero-tool run requires tool calling", res.LastError)
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

func TestAgentLoop_CostAccumulatesAcrossModelCalls(t *testing.T) {
	// Each LLM response carries Cost.TotalMicrosUSD; the loop must
	// sum these across model calls and surface the total on
	// ExecutionResult.CostMicrosUSD. The runner reads this value to
	// populate run.TotalCostMicrosUSD.
	respWithCost := func(content string, cost int64) *types.ChatResponse {
		r := makeChatResp(makeAssistantMsg(content))
		r.Cost.TotalMicrosUSD = cost
		return r
	}
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			respWithCost("", 100),              // model call 1: tool call
			respWithCost("Final answer.", 250), // model call 2: final
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
	// the loop fails with an actionable error. Subsequent model calls
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
	shell := &stubExecutor{result: &ExecutionResult{Status: "completed"}}
	loop := NewAgentLoopExecutor(llm, shell, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.BudgetMicrosUSD = 500 // ceiling under the first model call's cost
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
	// check fires before the second model call.
	if got := llm.calls.Load(); got != 1 {
		t.Errorf("LLM calls = %d, want 1 (loop bailed after first model call)", got)
	}
	if len(shell.calls) != 0 {
		t.Errorf("shell dispatches = %d, want zero after the paid response reached the ceiling", len(shell.calls))
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

func TestAgentLoop_ModelCallCostRecords_CapturedPerModelCall(t *testing.T) {
	// Per-model-call cost telemetry: the loop must surface a ModelCallCostRecord
	// for each LLM round-trip, including the assistant step ID and
	// the running cumulative for this run. The runner consumes these
	// to emit `model.call.completed` events.
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
	if len(res.ModelCallCosts) != 2 {
		t.Fatalf("ModelCallCosts = %d, want 2 (one per LLM call)", len(res.ModelCallCosts))
	}
	if res.ModelCallCosts[0].ModelCall != 1 || res.ModelCallCosts[0].CostMicrosUSD != 100 || res.ModelCallCosts[0].CumulativeMicrosUSD != 100 {
		t.Errorf("ModelCallCosts[0] = %+v, want {ModelCall:1 Cost:100 Cumulative:100}", res.ModelCallCosts[0])
	}
	if res.ModelCallCosts[0].ToolCallCount != 1 {
		t.Errorf("ModelCallCosts[0].ToolCallCount = %d, want 1", res.ModelCallCosts[0].ToolCallCount)
	}
	if res.ModelCallCosts[1].ModelCall != 2 || res.ModelCallCosts[1].CostMicrosUSD != 250 || res.ModelCallCosts[1].CumulativeMicrosUSD != 350 {
		t.Errorf("ModelCallCosts[1] = %+v, want {ModelCall:2 Cost:250 Cumulative:350}", res.ModelCallCosts[1])
	}
	// StepID on each entry should match the corresponding thinking
	// step so consumers can join the cost back to the assistant model call.
	if res.ModelCallCosts[0].StepID == "" || res.ModelCallCosts[1].StepID == "" {
		t.Errorf("ModelCallCosts entries missing StepID: %+v", res.ModelCallCosts)
	}
}

func TestAgentLoop_ThinkingStepCarriesPerModelCallCost(t *testing.T) {
	// The model-kind step's OutputSummary must surface this model call's
	// LLM cost (cost_micros_usd) and the run-cumulative figure
	// (run_cumulative_cost_micros_usd) so the run-replay UI can
	// render cost next to each "model call N" without joining against
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

	// Model call 1: cost=100, run cumulative=100.
	if got := modelSteps[0].OutputSummary["cost_micros_usd"]; got != int64(100) {
		t.Errorf("model call 1 cost_micros_usd = %v, want 100", got)
	}
	if got := modelSteps[0].OutputSummary["run_cumulative_cost_micros_usd"]; got != int64(100) {
		t.Errorf("model call 1 run_cumulative_cost_micros_usd = %v, want 100", got)
	}
	// Model call 2: cost=250, run cumulative=350.
	if got := modelSteps[1].OutputSummary["cost_micros_usd"]; got != int64(250) {
		t.Errorf("model call 2 cost_micros_usd = %v, want 250", got)
	}
	if got := modelSteps[1].OutputSummary["run_cumulative_cost_micros_usd"]; got != int64(350) {
		t.Errorf("model call 2 run_cumulative_cost_micros_usd = %v, want 350", got)
	}
}

func TestAgentLoop_CumulativeCeilingAppliesPriorChainCost(t *testing.T) {
	// Cumulative ceiling: a fresh run's spend looks small in
	// isolation but, when combined with prior runs in the resume
	// chain (PriorCostMicrosUSD), can already exceed the ceiling.
	// The loop must bail at the first model call that crosses the cap,
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
	shell := &stubExecutor{result: &ExecutionResult{Status: "completed"}}
	loop := NewAgentLoopExecutor(llm, shell, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.BudgetMicrosUSD = 500
	// Prior chain already spent 400 µUSD. This run can spend at most
	// 100 before hitting the ceiling. Model call 1 spends 200 — so the
	// ceiling fires after model call 1.
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
	// LLM call 1 happened; call 2 did not — ceiling check is between model calls.
	if got := llm.calls.Load(); got != 1 {
		t.Errorf("LLM calls = %d, want 1 (bail before model call 2)", got)
	}
	conversation := findArtifactByKind(res.Artifacts, "agent_conversation")
	if conversation == nil {
		t.Fatal("cost-ceiling result omitted the conversation artifact")
	}
	var messages []types.Message
	if err := json.Unmarshal([]byte(conversation.ContentText), &messages); err != nil {
		t.Fatalf("decode cost-ceiling conversation: %v", err)
	}
	if len(messages) == 0 || messages[len(messages)-1].Role != "assistant" || len(messages[len(messages)-1].ToolCalls) != 1 {
		t.Fatalf("cost-ceiling conversation tail = %+v, want paid assistant tool proposal preserved without dispatch", messages)
	}
	if len(shell.calls) != 0 {
		t.Fatalf("shell dispatches = %d, want zero after cumulative cost reached the ceiling", len(shell.calls))
	}
}

func TestAgentLoop_SameRunResumeSeedsCostSpentFromPrePauseTotal(t *testing.T) {
	// Same-run mid-approval resume: the run paused with
	// TotalCostMicrosUSD=X. On resume we don't get a fresh cost
	// counter — costSpent must seed from X so the persisted total
	// after this resume reflects the entire run's spend, not just
	// the post-resume model calls.
	saved := []types.Message{
		{Role: "user", Content: "do work"},
		// Pretend model call 1 happened pre-pause and incurred cost X.
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
	shell := &stubExecutor{result: &ExecutionResult{Status: "completed"}}
	loop := NewAgentLoopExecutor(llm, shell, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.ResumeCheckpoint = &ResumeCheckpoint{
		SourceRunID:              spec.Run.ID,
		SameRun:                  true,
		Reason:                   "approved_mid_loop",
		AgentConversation:        savedJSON,
		ThisRunCostMicrosUSD:     100, // pre-pause spend on THIS run
		ThisRunModelCallCount:    1,
		PendingToolCallsApproved: true,
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
	if len(shell.calls) != 1 {
		t.Fatalf("approved pending shell dispatches = %d, want exactly 1", len(shell.calls))
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
	if _, err := loop.Execute(context.Background(), newNetworkAgentLoopSpec(t)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	hasUpstream := false
	for _, m := range llm.lastReqs[1].Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "hello from upstream") {
			hasUpstream = true
		}
	}
	if !hasUpstream {
		t.Errorf("upstream body didn't reach next model call: %+v", llm.lastReqs[1].Messages)
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
	if _, err := loop.Execute(context.Background(), newNetworkAgentLoopSpec(t)); err != nil {
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
	if _, err := loop.Execute(context.Background(), newNetworkAgentLoopSpec(t)); err != nil {
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
	if _, err := loop.Execute(context.Background(), newNetworkAgentLoopSpec(t)); err != nil {
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
	if _, err := loop.Execute(context.Background(), newNetworkAgentLoopSpec(t)); err != nil {
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
	res, err := loop.Execute(context.Background(), newNetworkAgentLoopSpec(t))
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

func TestAgentLoop_PresetNetworkDisabledHidesAndBlocksNativeNetworkTools(t *testing.T) {
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "c1", Type: "function",
				Function: types.ToolCallFunction{
					Name:      AgentToolHTTPRequest,
					Arguments: `{"url":"https://example.invalid"}`,
				},
			})),
			makeChatResp(makeAssistantMsg("continued without network")),
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.AgentPresetID = "review_qa"
	var eventTypes []string
	var blockedEventData map[string]any
	spec.EmitRunEvent = func(eventType string, data map[string]any) {
		eventTypes = append(eventTypes, eventType)
		if eventType == runtimeevents.EventPolicyToolBlocked.String() {
			blockedEventData = data
		}
	}
	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "completed" {
		t.Fatalf("Status = %q, want completed after the model chooses a non-network path", res.Status)
	}
	if hasToolDefinition(llm.lastReqs[0].Tools, AgentToolHTTPRequest) || hasToolDefinition(llm.lastReqs[0].Tools, AgentToolWebSearch) {
		t.Fatalf("network-disabled tool catalog = %+v, want no native network tools", llm.lastReqs[0].Tools)
	}
	blocked := false
	for _, message := range llm.lastReqs[1].Messages {
		if message.Role == "tool" && strings.Contains(message.Content, "network access is disabled by the resolved agent preset") && message.ToolError {
			blocked = true
		}
	}
	if !blocked {
		t.Fatalf("tool messages = %+v, want a blocked network-policy result", llm.lastReqs[1].Messages)
	}
	blockedEvent := false
	for _, eventType := range eventTypes {
		if eventType == runtimeevents.EventPolicyToolBlocked.String() {
			blockedEvent = true
			break
		}
	}
	if !blockedEvent {
		t.Fatalf("event types = %+v, want %q", eventTypes, runtimeevents.EventPolicyToolBlocked)
	}
	if blockedEventData["policy"] != "sandbox_network" || blockedEventData["result"] != telemetry.MCPCallResultBlocked {
		t.Fatalf("blocked event data = %+v, want sandbox_network/blocked", blockedEventData)
	}
	var blockedStep *types.TaskStep
	for index := range res.Steps {
		if res.Steps[index].ToolName == AgentToolHTTPRequest {
			blockedStep = &res.Steps[index]
			break
		}
	}
	if blockedStep == nil || blockedStep.Status != "completed" || blockedStep.Phase != "policy" || blockedStep.Result != telemetry.ResultDenied || blockedStep.ErrorKind != "sandbox_policy_denied" || blockedStep.OutputSummary["policy"] != "sandbox_network" {
		t.Fatalf("blocked step = %+v, want persisted sandbox policy denial", blockedStep)
	}
}

func TestAgentLoop_WebSearch_ToolOnlyAdvertisedWhenConfigured(t *testing.T) {
	withoutSearch := agentToolDefinitionsWithOptions(agentToolDefinitionOptions{})
	if hasToolDefinition(withoutSearch, AgentToolWebSearch) {
		t.Fatal("web_search advertised without configured client")
	}
	withSearch := agentToolDefinitionsWithOptions(agentToolDefinitionOptions{IncludeWebSearch: true})
	if !hasToolDefinition(withSearch, AgentToolWebSearch) {
		t.Fatal("web_search not advertised when configured")
	}
}

func TestAgentLoop_NetworkToolDefinitionsFollowPresetSnapshotCompatibility(t *testing.T) {
	t.Parallel()

	legacy := agentToolDefinitionsForTask(types.Task{}, agentToolDefinitionOptions{IncludeWebSearch: true})
	if !hasToolDefinition(legacy, AgentToolHTTPRequest) || !hasToolDefinition(legacy, AgentToolWebSearch) {
		t.Fatalf("legacy tool catalog = %+v, want native network tools preserved without a preset snapshot", legacy)
	}
	disabled := agentToolDefinitionsForTask(types.Task{AgentPresetID: "review_qa"}, agentToolDefinitionOptions{IncludeWebSearch: true})
	if hasToolDefinition(disabled, AgentToolHTTPRequest) || hasToolDefinition(disabled, AgentToolWebSearch) {
		t.Fatalf("disabled tool catalog = %+v, want native network tools omitted", disabled)
	}
	enabled := agentToolDefinitionsForTask(types.Task{AgentPresetID: "implementation", SandboxNetwork: true}, agentToolDefinitionOptions{IncludeWebSearch: true})
	if !hasToolDefinition(enabled, AgentToolHTTPRequest) || !hasToolDefinition(enabled, AgentToolWebSearch) {
		t.Fatalf("enabled tool catalog = %+v, want HTTP and web search tools", enabled)
	}
}

func TestAgentLoop_ToolDefinitionsFollowPresetToolsSnapshotCompatibility(t *testing.T) {
	t.Parallel()
	disabledValue := false
	enabledValue := true
	opts := agentToolDefinitionOptions{IncludeProjectAssistantDraft: true, IncludeWebSearch: true}

	legacy := agentToolDefinitionsForTask(types.Task{AgentPresetID: "legacy"}, opts)
	if len(legacy) == 0 || !hasToolDefinition(legacy, "read_file") || !hasToolDefinition(legacy, AgentToolDraftProjectProposal) {
		t.Fatalf("legacy tool catalog = %+v, want existing behavior preserved without a tools snapshot", legacy)
	}
	disabled := agentToolDefinitionsForTask(types.Task{AgentPresetID: "review_qa", AgentPresetToolsEnabled: &disabledValue}, opts)
	if len(disabled) != 0 {
		t.Fatalf("tools-disabled catalog = %+v, want empty", disabled)
	}
	enabled := agentToolDefinitionsForTask(types.Task{AgentPresetID: "implementation", AgentPresetToolsEnabled: &enabledValue}, opts)
	if len(enabled) == 0 || !hasToolDefinition(enabled, "read_file") || !hasToolDefinition(enabled, AgentToolDraftProjectProposal) {
		t.Fatalf("tools-enabled catalog = %+v, want native tools", enabled)
	}
}

func TestAgentLoop_PresetToolsDisabledHidesAndBlocksEveryToolWithoutStartingMCP(t *testing.T) {
	t.Parallel()
	toolsEnabled := false
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("",
				agentLoopToolCall("call-shell", "shell_exec", `{"command":"pwd"}`),
				agentLoopToolCall("call-mcp", "mcp__docs__lookup", `{"query":"policy"}`),
			)),
			makeChatResp(makeAssistantMsg("I need workspace inspection to answer that.")),
		},
	}
	shell := &stubExecutor{}
	file := &stubExecutor{}
	git := &stubExecutor{}
	loop := NewAgentLoopExecutor(llm, shell, file, git, 8, []string{"shell_exec", "mcp__docs__lookup"}, HTTPRequestPolicy{})
	metrics, reader := newMetricsForTest(t)
	loop.SetMetrics(metrics)
	var factoryCalls atomic.Int32
	loop.SetMCPHostFactory(func(context.Context, []types.MCPServerConfig) (AgentMCPHost, error) {
		factoryCalls.Add(1)
		return nil, errors.New("MCP factory must not start when preset tools are disabled")
	})
	spec := newAgentLoopSpec(t)
	spec.Task.AgentPresetID = "review_qa"
	spec.Task.AgentPresetToolsEnabled = &toolsEnabled
	spec.Task.MCPServers = []types.MCPServerConfig{{Name: "docs", Command: "fake"}}
	var blockedEvents []map[string]any
	spec.EmitRunEvent = func(eventType string, data map[string]any) {
		if eventType == runtimeevents.EventPolicyToolBlocked.String() {
			blockedEvents = append(blockedEvents, data)
		}
	}

	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "completed" {
		t.Fatalf("Status = %q, want completed after model recovery", res.Status)
	}
	if factoryCalls.Load() != 0 {
		t.Fatalf("MCP factory calls = %d, want 0", factoryCalls.Load())
	}
	if len(shell.calls)+len(file.calls)+len(git.calls) != 0 {
		t.Fatalf("sub-executor calls = shell %d file %d git %d, want none", len(shell.calls), len(file.calls), len(git.calls))
	}
	if len(llm.lastReqs) != 2 || len(llm.lastReqs[0].Tools) != 0 {
		t.Fatalf("LLM requests = %d, first tool catalog = %+v; want two requests and no tools", len(llm.lastReqs), llm.lastReqs[0].Tools)
	}
	toolErrors := 0
	for _, message := range llm.lastReqs[1].Messages {
		if message.Role == "tool" && message.ToolError && strings.Contains(message.Content, "tools are disabled by the resolved agent preset") {
			toolErrors++
		}
	}
	if toolErrors != 2 {
		t.Fatalf("tool messages = %+v, want two preset-policy errors", llm.lastReqs[1].Messages)
	}
	if len(blockedEvents) != 2 {
		t.Fatalf("blocked events = %+v, want two", blockedEvents)
	}
	kinds := map[any]bool{}
	for _, event := range blockedEvents {
		if event["policy"] != "agent_preset_tools" || event["result"] != telemetry.MCPCallResultBlocked {
			t.Fatalf("blocked event = %+v, want agent_preset_tools/blocked", event)
		}
		kinds[event["kind"]] = true
		if event["kind"] == "mcp" {
			if event["mcp_server"] != "docs" || event["mcp_tool"] != "lookup" || event["error"] != agentPresetToolsDisabledReason {
				t.Fatalf("MCP blocked event = %+v, want full namespaced telemetry fields", event)
			}
			if _, ok := event["duration_ms"]; !ok {
				t.Fatalf("MCP blocked event = %+v, want duration_ms", event)
			}
		}
	}
	if !kinds["builtin"] || !kinds["mcp"] {
		t.Fatalf("blocked event kinds = %+v, want builtin and mcp", kinds)
	}
	deniedSteps := 0
	for _, step := range res.Steps {
		if step.Phase == "policy" && step.Result == telemetry.ResultDenied && step.ErrorKind == "agent_preset_policy_denied" {
			deniedSteps++
			if step.OutputSummary["policy"] != "agent_preset_tools" {
				t.Fatalf("blocked step = %+v, want agent_preset_tools summary", step)
			}
		}
	}
	if deniedSteps != 2 {
		t.Fatalf("denied policy steps = %d, want 2; steps = %+v", deniedSteps, res.Steps)
	}
	calls := findMetricSum(t, reader, telemetry.MetricOrchestratorMCPToolCallsTotal)
	if len(calls.DataPoints) != 1 || calls.DataPoints[0].Value != 1 {
		t.Fatalf("MCP blocked metric = %+v, want one call", calls.DataPoints)
	}
	result, ok := calls.DataPoints[0].Attributes.Value("hecate.mcp.call.result")
	if !ok || result.AsString() != telemetry.MCPCallResultBlocked {
		t.Fatalf("MCP metric result = %v ok=%v, want blocked", result, ok)
	}
}

func TestAgentLoop_ReadOnlyToolDefinitionsKeepStructuredInspectionAndProposalTools(t *testing.T) {
	t.Parallel()

	tools := agentToolDefinitionsForTask(types.Task{SandboxReadOnly: true}, agentToolDefinitionOptions{})
	for _, blocked := range []string{"shell_exec", "git_exec", "file_write", AgentToolTerminalOpen, AgentToolTerminalWrite, AgentToolTerminalRead, AgentToolTerminalWait, AgentToolTerminalKill} {
		if hasToolDefinition(tools, blocked) {
			t.Errorf("read-only tool catalog contains %q", blocked)
		}
	}
	for _, allowed := range []string{"read_file", "grep", "glob", AgentToolCodeIntelligence, "list_dir", "git_status", "git_diff", "file_edit", "apply_patch"} {
		if !hasToolDefinition(tools, allowed) {
			t.Errorf("read-only tool catalog omits structured tool %q", allowed)
		}
	}
}

func TestAgentLoop_WebSearch_HappyPath(t *testing.T) {
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "c1", Type: "function",
				Function: types.ToolCallFunction{
					Name:      AgentToolWebSearch,
					Arguments: `{"query":"agent client protocol","count":3}`,
				},
			})),
			makeChatResp(makeAssistantMsg("got it")),
		},
	}
	search := &stubWebSearchClient{
		response: websearch.Response{
			Provider:             websearch.ProviderBrave,
			Query:                "agent client protocol",
			MoreResultsAvailable: true,
			Results: []websearch.Result{{
				Title:       "Agent Client Protocol",
				URL:         "https://agentclientprotocol.com",
				Description: "Protocol docs",
			}},
		},
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{}, WithWebSearchClient(search))
	res, err := loop.Execute(context.Background(), newNetworkAgentLoopSpec(t))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "completed" {
		t.Fatalf("Status = %q, want completed", res.Status)
	}
	if search.query.Query != "agent client protocol" || search.query.Count != 3 {
		t.Fatalf("search query = %+v, want query + count", search.query)
	}
	hasSearchResult := false
	for _, m := range llm.lastReqs[1].Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "https://agentclientprotocol.com") {
			hasSearchResult = true
		}
	}
	if !hasSearchResult {
		t.Fatalf("web search result did not reach next model call: %+v", llm.lastReqs[1].Messages)
	}
	var searchStep *types.TaskStep
	for i := range res.Steps {
		if res.Steps[i].ToolName == AgentToolWebSearch {
			searchStep = &res.Steps[i]
			break
		}
	}
	if searchStep == nil {
		t.Fatal("web_search step not persisted in execution result")
	}
	if searchStep.OutputSummary["provider"] != websearch.ProviderBrave || searchStep.OutputSummary["result_count"] != 1 {
		t.Fatalf("web_search output summary = %#v, want provider + result count", searchStep.OutputSummary)
	}
}

func TestAgentLoop_WebSearch_GatedPausesRun(t *testing.T) {
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "c1", Type: "function",
				Function: types.ToolCallFunction{
					Name:      AgentToolWebSearch,
					Arguments: `{"query":"agent client protocol"}`,
				},
			})),
		},
	}
	search := &stubWebSearchClient{}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8,
		[]string{AgentToolWebSearch},
		HTTPRequestPolicy{},
		WithWebSearchClient(search))
	res, err := loop.Execute(context.Background(), newNetworkAgentLoopSpec(t))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "awaiting_approval" {
		t.Errorf("Status = %q, want awaiting_approval", res.Status)
	}
	if search.called {
		t.Error("web search should not run before approval")
	}
}

func hasToolDefinition(tools []types.Tool, name string) bool {
	for _, tool := range tools {
		if tool.Function.Name == name {
			return true
		}
	}
	return false
}

type stubWebSearchClient struct {
	called   bool
	query    websearch.Query
	response websearch.Response
	err      error
}

func (s *stubWebSearchClient) Search(_ context.Context, query websearch.Query) (websearch.Response, error) {
	s.called = true
	s.query = query
	return s.response, s.err
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
