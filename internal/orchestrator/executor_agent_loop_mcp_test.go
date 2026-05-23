package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hecatehq/hecate/pkg/types"
)

// fakeMCPHost is an in-memory AgentMCPHost for tests. Pre-populated
// with a tool catalog and a per-tool handler map; records calls and
// closures so tests can pin both the dispatch routing and the
// shutdown contract (host closes exactly once when Execute returns).
type fakeMCPHost struct {
	tools    []types.Tool
	handlers map[string]func(args json.RawMessage) (text string, isError bool, err error)

	mu     sync.Mutex
	calls  []fakeMCPCall
	closed atomic.Int32
}

type fakeMCPCall struct {
	Name string
	Args json.RawMessage
}

func (h *fakeMCPHost) Tools() []types.Tool { return h.tools }

func (h *fakeMCPHost) Call(_ context.Context, name string, args json.RawMessage) (string, bool, error) {
	h.mu.Lock()
	h.calls = append(h.calls, fakeMCPCall{Name: name, Args: append(json.RawMessage(nil), args...)})
	h.mu.Unlock()
	fn, ok := h.handlers[name]
	if !ok {
		return "", false, errors.New("fakeMCPHost: no handler for " + name)
	}
	return fn(args)
}

func (h *fakeMCPHost) Close() error {
	h.closed.Add(1)
	return nil
}

func mcpTool(name, description string) types.Tool {
	return types.Tool{
		Type: "function",
		Function: types.ToolFunction{
			Name:        name,
			Description: description,
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
		},
	}
}

// TestAgentLoop_MCPTool_DispatchedToHost: the LLM asks for a
// namespaced MCP tool, the host runs it, the result feeds back as a
// tool message, the LLM finalizes. Pins:
//   - host.Tools() got merged into the catalog the LLM sees
//   - dispatch routed by name to the host (not the built-in switch)
//   - the resulting tool-role message has the host's text
//   - host.Close was called exactly once
func TestAgentLoop_MCPTool_DispatchedToHost(t *testing.T) {
	t.Parallel()
	host := &fakeMCPHost{
		tools: []types.Tool{mcpTool("mcp__filesystem__read_file", "Read a file under /workspace")},
		handlers: map[string]func(json.RawMessage) (string, bool, error){
			"mcp__filesystem__read_file": func(args json.RawMessage) (string, bool, error) {
				return "file contents: hello\n", false, nil
			},
		},
	}
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("",
				types.ToolCall{
					ID:   "call-1",
					Type: "function",
					Function: types.ToolCallFunction{
						Name:      "mcp__filesystem__read_file",
						Arguments: `{"path":"/workspace/notes.txt"}`,
					},
				},
			)),
			makeChatResp(makeAssistantMsg("Read the file; it says hello.")),
		},
	}
	executor := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})
	executor.SetMCPHostFactory(func(_ context.Context, _ []types.MCPServerConfig) (AgentMCPHost, error) {
		return host, nil
	})

	spec := newAgentLoopSpec(t)
	spec.Task.MCPServers = []types.MCPServerConfig{
		{Name: "filesystem", Command: "fake"},
	}

	result, err := executor.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("status = %q, want completed; lastError=%q", result.Status, result.LastError)
	}

	// Catalog must include the host's tool. Inspect what the LLM
	// saw on the first turn — that's the freshest evidence the
	// merge happened.
	if len(llm.lastReqs) == 0 {
		t.Fatal("LLM not called")
	}
	firstTurn := llm.lastReqs[0]
	var sawMCPTool bool
	for _, tt := range firstTurn.Tools {
		if tt.Function.Name == "mcp__filesystem__read_file" {
			sawMCPTool = true
			break
		}
	}
	if !sawMCPTool {
		t.Errorf("LLM did not see mcp__filesystem__read_file in tool catalog")
	}

	// Host got exactly one call to the right tool with the right args.
	host.mu.Lock()
	gotCalls := append([]fakeMCPCall(nil), host.calls...)
	host.mu.Unlock()
	if len(gotCalls) != 1 {
		t.Fatalf("host calls = %d, want 1", len(gotCalls))
	}
	if gotCalls[0].Name != "mcp__filesystem__read_file" {
		t.Errorf("call name = %q, want mcp__filesystem__read_file", gotCalls[0].Name)
	}
	if !strings.Contains(string(gotCalls[0].Args), `"/workspace/notes.txt"`) {
		t.Errorf("call args = %s, want path passthrough", gotCalls[0].Args)
	}

	// The LLM's second turn should see the host's result as a tool
	// message in the conversation history.
	if len(llm.lastReqs) < 2 {
		t.Fatal("LLM second turn missing")
	}
	secondTurn := llm.lastReqs[1]
	var sawToolMsg bool
	for _, m := range secondTurn.Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "file contents: hello") {
			sawToolMsg = true
			break
		}
	}
	if !sawToolMsg {
		t.Errorf("second turn did not see tool result; messages=%v", secondTurn.Messages)
	}

	// Host shutdown happens via deferred Close in Execute.
	if got := host.closed.Load(); got != 1 {
		t.Errorf("host.Close called %d times, want 1", got)
	}
}

// TestAgentLoop_MCPTool_UpstreamErrorFeedsBackToLLM pins that an
// IsError result from the host becomes a tool-role message with
// ToolError=true on the next turn — the LLM gets a chance to retry
// rather than the run failing outright.
func TestAgentLoop_MCPTool_UpstreamErrorFeedsBackToLLM(t *testing.T) {
	t.Parallel()
	host := &fakeMCPHost{
		tools: []types.Tool{mcpTool("mcp__fs__read", "")},
		handlers: map[string]func(json.RawMessage) (string, bool, error){
			"mcp__fs__read": func(json.RawMessage) (string, bool, error) {
				return "permission denied: /etc/shadow", true, nil
			},
		},
	}
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("",
				types.ToolCall{
					ID:   "call-1",
					Type: "function",
					Function: types.ToolCallFunction{
						Name:      "mcp__fs__read",
						Arguments: `{"path":"/etc/shadow"}`,
					},
				},
			)),
			makeChatResp(makeAssistantMsg("That file is protected; abandoning.")),
		},
	}
	executor := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})
	executor.SetMCPHostFactory(func(_ context.Context, _ []types.MCPServerConfig) (AgentMCPHost, error) {
		return host, nil
	})

	spec := newAgentLoopSpec(t)
	spec.Task.MCPServers = []types.MCPServerConfig{{Name: "fs", Command: "fake"}}

	result, err := executor.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("status = %q, want completed (loop should recover from tool error)", result.Status)
	}
	// Second turn's conversation must carry the tool message with
	// ToolError set. That's the contract the providers rely on to
	// surface is_error=true on the wire.
	if len(llm.lastReqs) < 2 {
		t.Fatal("LLM second turn missing")
	}
	var foundErr bool
	for _, m := range llm.lastReqs[1].Messages {
		if m.Role == "tool" && m.ToolError && strings.Contains(m.Content, "permission denied") {
			foundErr = true
			break
		}
	}
	if !foundErr {
		t.Errorf("second turn did not see ToolError=true tool message; messages=%+v", llm.lastReqs[1].Messages)
	}
}

// TestAgentLoop_MCPHostFactoryError fails the run with a clear error
// when the factory returns an error (e.g. subprocess failed to spawn
// or initialize). Without this, the LLM would run blind without the
// configured tools — a silent regression in capability.
func TestAgentLoop_MCPHostFactoryError(t *testing.T) {
	t.Parallel()
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("never reached")),
		},
	}
	executor := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})
	executor.SetMCPHostFactory(func(_ context.Context, _ []types.MCPServerConfig) (AgentMCPHost, error) {
		return nil, errors.New("npx exited 127: command not found")
	})

	spec := newAgentLoopSpec(t)
	spec.Task.MCPServers = []types.MCPServerConfig{{Name: "fs", Command: "missing"}}

	result, err := executor.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != "failed" {
		t.Errorf("status = %q, want failed", result.Status)
	}
	if !strings.Contains(result.LastError, "start mcp servers") {
		t.Errorf("LastError = %q, want 'start mcp servers' wrapping", result.LastError)
	}
	if !strings.Contains(result.LastError, "command not found") {
		t.Errorf("LastError = %q, should surface underlying cause", result.LastError)
	}
}

// TestAgentLoop_MCPConfiguredButNoFactory protects against a
// misconfigured deploy: if MCPServers is set but the runner forgot
// to wire the factory, the run fails fast with a diagnostic rather
// than silently dropping the tools.
func TestAgentLoop_MCPConfiguredButNoFactory(t *testing.T) {
	t.Parallel()
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("never reached")),
		},
	}
	executor := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})
	// SetMCPHostFactory deliberately not called.

	spec := newAgentLoopSpec(t)
	spec.Task.MCPServers = []types.MCPServerConfig{{Name: "fs", Command: "fake"}}

	result, err := executor.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != "failed" {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if !strings.Contains(result.LastError, "no MCP host factory") {
		t.Errorf("LastError = %q, want 'no MCP host factory' diagnostic", result.LastError)
	}
}

// TestAgentLoop_MCPRequireApproval_PausesBeforeHostCall pins that
// when an MCP server is configured with ApprovalPolicy=require_approval,
// a tool call to that server pauses the loop with an approval record
// — the host is never called, the conversation snapshot is saved, and
// the resume path can dispatch the approved call later.
func TestAgentLoop_MCPRequireApproval_PausesBeforeHostCall(t *testing.T) {
	t.Parallel()
	host := &fakeMCPHost{
		tools: []types.Tool{mcpTool("mcp__github__create_pr", "Create a pull request")},
		handlers: map[string]func(json.RawMessage) (string, bool, error){
			"mcp__github__create_pr": func(args json.RawMessage) (string, bool, error) {
				return "pr created", false, nil
			},
		},
	}
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("I'll open a PR.", types.ToolCall{
				ID: "call-1", Type: "function",
				Function: types.ToolCallFunction{
					Name:      "mcp__github__create_pr",
					Arguments: `{"title":"feat: x","body":"y"}`,
				},
			})),
		},
	}
	executor := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})
	executor.SetMCPHostFactory(func(_ context.Context, _ []types.MCPServerConfig) (AgentMCPHost, error) {
		return host, nil
	})

	spec := newAgentLoopSpec(t)
	spec.Task.MCPServers = []types.MCPServerConfig{
		{Name: "github", Command: "fake", ApprovalPolicy: types.MCPApprovalRequireApproval},
	}

	res, err := executor.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "awaiting_approval" {
		t.Fatalf("Status = %q, want awaiting_approval", res.Status)
	}

	// Host must NOT have been called — the whole point of the gate
	// is that the operator decides before the side effect lands.
	host.mu.Lock()
	gotCalls := len(host.calls)
	host.mu.Unlock()
	if gotCalls != 0 {
		t.Errorf("host.Call ran without approval; calls=%d", gotCalls)
	}

	// One approval record covering the gated tool.
	if len(res.PendingApprovals) != 1 {
		t.Fatalf("PendingApprovals = %d, want 1", len(res.PendingApprovals))
	}
	approval := res.PendingApprovals[0]
	if approval.Status != "pending" || approval.Kind != "agent_loop_tool_call" {
		t.Errorf("approval shape wrong: %+v", approval)
	}
	if !strings.Contains(approval.Reason, "mcp__github__create_pr") {
		t.Errorf("approval reason should name the gated tool: %q", approval.Reason)
	}

	// Conversation must be persisted — that's how the resume run
	// learns which tool calls to dispatch on operator approve.
	convo := findArtifactByKind(res.Artifacts, "agent_conversation")
	if convo == nil {
		t.Fatalf("conversation artifact missing on pause; got: %+v", res.Artifacts)
	}
	if !strings.Contains(convo.ContentText, "mcp__github__create_pr") {
		t.Errorf("conversation snapshot lost MCP tool call: %s", convo.ContentText)
	}
}

// TestAgentLoop_MCPBlock_ReturnsToolErrorWithoutCallingHost pins that
// ApprovalPolicy=block short-circuits at the dispatcher: the host is
// never called, a failed step is recorded, and a tool-error message
// goes back to the LLM so it can pick a different path on the next
// turn. The run does NOT pause for approval — block is a hard refusal,
// not a gate.
func TestAgentLoop_MCPBlock_ReturnsToolErrorWithoutCallingHost(t *testing.T) {
	t.Parallel()
	host := &fakeMCPHost{
		tools: []types.Tool{mcpTool("mcp__github__delete_repo", "Delete a repository")},
		handlers: map[string]func(json.RawMessage) (string, bool, error){
			"mcp__github__delete_repo": func(args json.RawMessage) (string, bool, error) {
				return "deleted", false, nil
			},
		},
	}
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "call-1", Type: "function",
				Function: types.ToolCallFunction{
					Name:      "mcp__github__delete_repo",
					Arguments: `{"repo":"acme/widgets"}`,
				},
			})),
			makeChatResp(makeAssistantMsg("Won't do that.")),
		},
	}
	executor := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})
	executor.SetMCPHostFactory(func(_ context.Context, _ []types.MCPServerConfig) (AgentMCPHost, error) {
		return host, nil
	})

	spec := newAgentLoopSpec(t)
	spec.Task.MCPServers = []types.MCPServerConfig{
		{Name: "github", Command: "fake", ApprovalPolicy: types.MCPApprovalBlock},
	}

	res, err := executor.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "completed" {
		t.Fatalf("Status = %q, want completed (block doesn't pause; the LLM moves on)", res.Status)
	}

	// Host must never have been called.
	host.mu.Lock()
	gotCalls := len(host.calls)
	host.mu.Unlock()
	if gotCalls != 0 {
		t.Errorf("host.Call ran despite block policy; calls=%d", gotCalls)
	}

	// LLM's second turn must have seen a tool message marked as an
	// error so the model knows the call was refused.
	if len(llm.lastReqs) < 2 {
		t.Fatalf("LLM second turn missing; got %d reqs", len(llm.lastReqs))
	}
	var sawBlockedToolMsg bool
	for _, m := range llm.lastReqs[1].Messages {
		if m.Role == "tool" && m.ToolCallID == "call-1" && m.ToolError && strings.Contains(m.Content, "blocked") {
			sawBlockedToolMsg = true
			break
		}
	}
	if !sawBlockedToolMsg {
		t.Errorf("second turn did not see blocked tool error; messages=%v", llm.lastReqs[1].Messages)
	}

	// A failed step for the blocked call should be in the timeline.
	var sawBlockedStep bool
	for _, s := range res.Steps {
		if s.ToolName == "mcp__github__delete_repo" && s.Status == "failed" {
			sawBlockedStep = true
			break
		}
	}
	if !sawBlockedStep {
		t.Errorf("expected a failed step for the blocked tool; steps=%+v", res.Steps)
	}
}

// TestAgentLoop_MCPAuto_DispatchesNormally pins that the default
// (empty / "auto") policy preserves the pre-policy behavior: tool
// calls go straight to the host, no approval pause, no block.
func TestAgentLoop_MCPAuto_DispatchesNormally(t *testing.T) {
	t.Parallel()
	host := &fakeMCPHost{
		tools: []types.Tool{mcpTool("mcp__filesystem__read_file", "Read a file")},
		handlers: map[string]func(json.RawMessage) (string, bool, error){
			"mcp__filesystem__read_file": func(args json.RawMessage) (string, bool, error) {
				return "contents", false, nil
			},
		},
	}
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "call-1", Type: "function",
				Function: types.ToolCallFunction{
					Name:      "mcp__filesystem__read_file",
					Arguments: `{"path":"x"}`,
				},
			})),
			makeChatResp(makeAssistantMsg("Read it.")),
		},
	}
	executor := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})
	executor.SetMCPHostFactory(func(_ context.Context, _ []types.MCPServerConfig) (AgentMCPHost, error) {
		return host, nil
	})

	spec := newAgentLoopSpec(t)
	// Empty policy → auto. Same expectation as if we'd set
	// ApprovalPolicy: types.MCPApprovalAuto explicitly.
	spec.Task.MCPServers = []types.MCPServerConfig{
		{Name: "filesystem", Command: "fake"},
	}

	res, err := executor.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "completed" {
		t.Fatalf("Status = %q, want completed; LastError=%q", res.Status, res.LastError)
	}
	if len(res.PendingApprovals) != 0 {
		t.Errorf("auto policy should not pause; got %d approvals", len(res.PendingApprovals))
	}
	host.mu.Lock()
	gotCalls := len(host.calls)
	host.mu.Unlock()
	if gotCalls != 1 {
		t.Errorf("host.Call count = %d, want 1 (auto must dispatch)", gotCalls)
	}
}

// TestAgentLoop_NoMCPServers_NoHostStarted verifies the unconfigured
// case is a no-op: tasks with empty MCPServers don't invoke the
// factory, don't start a host, and don't pay the spawn cost.
func TestAgentLoop_NoMCPServers_NoHostStarted(t *testing.T) {
	t.Parallel()
	var factoryCalled atomic.Int32
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("All done.")),
		},
	}
	executor := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})
	executor.SetMCPHostFactory(func(_ context.Context, _ []types.MCPServerConfig) (AgentMCPHost, error) {
		factoryCalled.Add(1)
		return nil, nil
	})

	spec := newAgentLoopSpec(t)
	// spec.Task.MCPServers intentionally empty.

	result, err := executor.Execute(context.Background(), spec)
	if err != nil || result.Status != "completed" {
		t.Fatalf("Execute: status=%q err=%v", result.Status, err)
	}
	if got := factoryCalled.Load(); got != 0 {
		t.Errorf("factory called %d times, want 0 when MCPServers is empty", got)
	}
}
