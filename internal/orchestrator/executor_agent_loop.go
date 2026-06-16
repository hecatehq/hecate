package orchestrator

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/gitrunner"
	"github.com/hecatehq/hecate/internal/runtimeevents"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/internal/workspacefs"
	"github.com/hecatehq/hecate/pkg/types"
)

// AgentLLMClient is the seam the agent loop uses to talk to a model.
// Production wires this to gateway.Service.HandleChat — that gives the
// agent the same provider routing, caching, usage tracking, and audit
// trail as any other client. Tests substitute a fake.
//
// The interface accepts a full ChatRequest (with Tools populated) and
// returns a ChatResponse — the loop then inspects the assistant's
// message for tool_calls.
type AgentLLMClient interface {
	Chat(ctx context.Context, req types.ChatRequest) (*types.ChatResponse, error)
}

// AgentLLMStreamingClient is the optional streaming extension for
// AgentLLMClient. The agent loop uses it when available so chat UIs
// can see assistant text while the model is still producing the turn,
// then falls back to Chat for test doubles and non-streaming backends.
type AgentLLMStreamingClient interface {
	ChatStream(ctx context.Context, req types.ChatRequest, onContentDelta func(string)) (*types.ChatResponse, error)
}

// AgentLLMClientFunc is the function-form of AgentLLMClient — saves
// callers from having to declare a struct just to satisfy a one-method
// interface. Production wiring uses this to adapt
// gateway.Service.HandleChat (which returns a wrapped ChatResult) into
// the bare ChatResponse the loop expects.
type AgentLLMClientFunc func(ctx context.Context, req types.ChatRequest) (*types.ChatResponse, error)

func (f AgentLLMClientFunc) Chat(ctx context.Context, req types.ChatRequest) (*types.ChatResponse, error) {
	return f(ctx, req)
}

// AgentLoopExecutor drives an LLM in a tool-use loop. The flow each
// turn:
//
//  1. Send the conversation (system prompt + user prompt + prior turns)
//     plus the tool catalog to the LLM
//  2. If the assistant returns tool_calls, dispatch each one to the
//     local tool executor (shell / git / file) and append the result
//     as a tool-role message
//  3. If no tool_calls, the assistant has finished; return its message
//     as the final answer
//  4. Loop until done or MaxTurns hits
//
// Mid-loop tool calls can be gated on approvals: when the LLM
// requests a tool whose name is in `gatedTools` (e.g. shell_exec,
// http_request), the loop pauses before dispatching, persists the
// conversation, and emits an approval record. On operator approve,
// the same run is re-queued; the loop detects the trailing
// assistant tool_calls without resolved results and dispatches them
// without a second LLM call.
type AgentLoopExecutor struct {
	llm            AgentLLMClient
	maxTurns       int
	approvalGate   agentLoopApprovalGate
	toolDispatcher *agentLoopToolDispatcher
	// mcpFactory builds a per-run MCP host from the task's
	// MCPServers config. nil = no MCP support; tasks that configure
	// MCPServers will fail with a clear error.
	mcpFactory AgentMCPHostFactory
}

// NewAgentLoopExecutor constructs the loop. A nil LLM client is
// allowed at construction time so the gateway can boot before its
// chat service is wired (main.go calls SetAgentLLMClient as a second
// step). Running an agent_loop task with a nil client fails fast
// with a clear "no LLM configured" error — the right signal for the
// operator to wire a model before retrying.
//
// maxTurns caps how many LLM round-trips a single run can do. 0 (or
// negative) defaults to 8 — generous enough for typical multi-step
// tasks but tight enough that a runaway loop costs <$0.10 even on
// expensive models.
//
// gatedTools is the set of tool names that require operator approval
// before execution (e.g. {"shell_exec", "git_exec"}). When the LLM
// asks for any tool in this set, the loop pauses, emits an approval
// record, and returns awaiting_approval. The runner persists the
// approval; when the operator approves, the same run is re-queued
// and the loop hydrates from the saved conversation, dispatches the
// previously-pending tool calls, and continues. Empty/nil = no gating
// (every tool runs immediately).
func NewAgentLoopExecutor(llm AgentLLMClient, shell Executor, file Executor, git Executor, maxTurns int, gatedTools []string, httpPolicy HTTPRequestPolicy) *AgentLoopExecutor {
	if maxTurns <= 0 {
		maxTurns = 8
	}
	// Apply safe defaults to the HTTP policy. Operators who don't
	// configure HECATE_TASK_HTTP_* still get sensible bounds.
	if httpPolicy.Timeout <= 0 {
		httpPolicy.Timeout = 30 * time.Second
	}
	if httpPolicy.MaxResponseBytes <= 0 {
		httpPolicy.MaxResponseBytes = 256 * 1024
	}
	// Dedicated client per executor so the timeout is enforced and
	// connections are pooled. We don't enable redirects-following
	// past 10 (Go's default) — agents that get stuck redirect-looping
	// blow through their max-turns cap before causing damage.
	httpClient := &http.Client{Timeout: httpPolicy.Timeout}

	return &AgentLoopExecutor{
		llm:          llm,
		maxTurns:     maxTurns,
		approvalGate: newAgentLoopApprovalGate(gatedTools),
		toolDispatcher: &agentLoopToolDispatcher{
			shell:      shell,
			file:       file,
			git:        git,
			httpPolicy: httpPolicy,
			httpClient: httpClient,
		},
	}
}

// SetMetrics wires an OrchestratorMetrics instance for MCP-tool-call
// telemetry. Safe to call after construction; nil clears any
// previously-set metrics. Production wires this once at runner setup
// (the runner already holds the metrics via SetMetrics; it forwards
// the same instance here so the agent loop and the runner share
// instruments).
func (e *AgentLoopExecutor) SetMetrics(m *telemetry.OrchestratorMetrics) {
	if e.toolDispatcher != nil {
		e.toolDispatcher.SetMetrics(m)
	}
}

// SetMCPHostFactory wires the factory used to bring up per-task MCP
// hosts. Production runners set this to DefaultMCPHostFactory at
// startup; tests substitute an in-memory factory. nil disables MCP
// host support — agent_loop tasks that configure MCPServers will be
// failed at the start of Execute with a clear error rather than
// silently dropping the configured tools.
func (e *AgentLoopExecutor) SetMCPHostFactory(f AgentMCPHostFactory) {
	e.mcpFactory = f
}

// Execute runs the loop. Steps and artifacts produced by each turn
// (model thinking + tool execution) are upserted via the spec's
// callbacks; the final ExecutionResult mirrors the standard executor
// shape so the runner can persist it identically.
func (e *AgentLoopExecutor) Execute(ctx context.Context, spec ExecutionSpec) (*ExecutionResult, error) {
	if spec.NewID == nil {
		return nil, fmt.Errorf("resource id generator is required")
	}
	if e.llm == nil {
		// No LLM configured — fall back to deterministic pass-through.
		// This isn't an "agent loop" but it's better than a hard
		// failure at runtime. The operator sees the result and knows
		// to configure a model.
		return e.runWithoutLLM(ctx, spec)
	}

	runState := newAgentLoopRunState(spec, e.maxTurns)
	conversation := newAgentLoopConversation(spec)
	tools := agentToolDefinitions(projectAssistantDraftToolAvailable(spec.Task, e.toolDispatcher.projectAssistantDraftTool))

	// Bring up external MCP servers if the task configured any. Their
	// tools are appended to the built-in catalog under names of the
	// form `mcp__<server>__<tool>`. The host owns the subprocesses and
	// dies when this run finishes — long-lived per-task pooling is a
	// follow-up. We fail fast rather than silently running without
	// the configured tools: the operator asked for those tools to be
	// available, so a half-configured run is the wrong default.
	var mcpHost AgentMCPHost
	if len(spec.Task.MCPServers) > 0 {
		if e.mcpFactory == nil {
			return e.failedFromError(spec, runState.Steps(), runState.Artifacts(), runState.NextStepIndex(), time.Now().UTC(),
				"task configured mcp_servers but no MCP host factory is wired; this gateway build does not support external MCP servers")
		}
		host, err := e.mcpFactory(ctx, spec.Task.MCPServers)
		if err != nil {
			return e.failedFromError(spec, runState.Steps(), runState.Artifacts(), runState.NextStepIndex(), time.Now().UTC(),
				fmt.Sprintf("start mcp servers: %v", err))
		}
		if host != nil {
			mcpHost = host
			defer func() { _ = host.Close() }()
			tools = append(tools, host.Tools()...)
		}
	}

	// Resume detection: if the conversation tail is an assistant
	// message with tool_calls and no following tool messages, we're
	// resuming after operator approval. Dispatch the pending tool
	// calls before doing the next LLM turn — they were just approved.
	pendingToolCalls := conversation.PendingToolCallsForResume()

	for turn := 1; turn <= e.maxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			res := runState.Result("cancelled")
			res.LastError = err.Error()
			res.OtelStatusCode = "error"
			res.OtelStatusMessage = "context cancelled mid-loop"
			return res, nil
		}

		var assistantMsg types.Message
		turnStartedAt := time.Now().UTC()

		if len(pendingToolCalls) > 0 {
			// Skip the LLM call this turn — the assistant message is
			// already at the tail of `messages` (saved by the previous
			// run). Dispatch the approved tool calls and let the next
			// turn's LLM call reason over the results.
			var ok bool
			assistantMsg, ok = conversation.TailAssistantForResume()
			if !ok {
				failed, ferr := e.failedFromError(spec, runState.Steps(), runState.Artifacts(), runState.NextStepIndex(), turnStartedAt,
					"resume checkpoint had pending tool calls but no assistant tail")
				return runState.attachAccounting(failed), ferr
			}
			thinkingStep := buildResumeThinkingStep(spec, runState.NextStepIndex(), turn, turnStartedAt, assistantMsg)
			if err := runState.AddStep(spec, thinkingStep); err != nil {
				return nil, err
			}
		} else {
			turnResult, failed, err := e.runLLMTurn(ctx, spec, &conversation, runState, tools, turn, turnStartedAt)
			if failed != nil || err != nil {
				return failed, err
			}
			assistantMsg = turnResult.Assistant

			// If no tool calls, assistant gave a final answer.
			if len(assistantMsg.ToolCalls) == 0 {
				emitAssistantFinalAnswer(spec, turn, assistantMsg)
				finalArtifact := buildFinalAnswerArtifact(spec, turnResult.ThinkingStep.ID, turnStartedAt, assistantMsg.Content)
				if err := runState.AddArtifact(spec, finalArtifact); err != nil {
					return nil, err
				}
				res := runState.Result("completed")
				res.OtelStatusCode = "ok"
				return res, nil
			}

			// 4b. Approval gate. If any tool in this turn is gated,
			// pause the loop: persist conversation (already done),
			// emit an approval record covering all pending tool
			// calls, return awaiting_approval. The runner persists
			// the approval and stops the run; on operator approve,
			// the same run is re-queued and we re-enter the loop
			// with the same conversation tail — this branch is
			// short-circuited by the resume-detection above.
			pause, ok := e.approvalGate.Evaluate(spec, turn, runState.NextStepIndex(), turnStartedAt, assistantMsg.ToolCalls)
			if ok {
				if err := runState.AddStep(spec, pause.Step); err != nil {
					return nil, err
				}
				res := runState.Result("awaiting_approval")
				res.PendingApprovals = []types.TaskApproval{pause.Approval}
				res.OtelStatusCode = "ok"
				return res, nil
			}
		}

		// 5. Dispatch each tool call in order.
		callsToRun := assistantMsg.ToolCalls
		for _, toolCall := range callsToRun {
			dispatch, dispatchErr := e.toolDispatcher.Dispatch(ctx, spec, toolCall, runState.NextStepIndex(), mcpHost)
			if dispatch.Step != nil {
				if err := runState.AddStep(spec, *dispatch.Step); err != nil {
					return nil, err
				}
			}
			if err := runState.AddArtifacts(spec, dispatch.Artifacts); err != nil {
				return nil, err
			}
			// Mark the tool message as errored on any failure
			// path so Anthropic providers can emit is_error=true
			// on the wire (OpenAI-shaped providers ignore it; the
			// error context is also in the result content).
			//
			//   - dispatchErr != nil: internal failure surfaced by
			//     the dispatcher (rare; most errors are encoded in
			//     toolResultText).
			//   - toolStep == nil: dispatcher couldn't run the
			//     tool at all — bad args, unknown tool, missing
			//     sub-executor. The result text describes the
			//     failure.
			//   - toolStep.Status == "failed": tool ran but exited
			//     non-zero / errored at runtime (sandbox rejected,
			//     non-zero exit, file system error, HTTP non-2xx).
			isToolError := dispatchErr != nil ||
				dispatch.Step == nil ||
				(dispatch.Step != nil && dispatch.Step.Status == "failed")
			conversation.AppendToolResult(toolCall.ID, dispatch.Text, isToolError)
			_ = dispatchErr
		}
		// Snapshot after tool results.
		if _, err := conversation.UpsertArtifact(spec, turn, turnStartedAt); err != nil {
			return nil, err
		}
		// Resume mode is a one-shot — clear so subsequent turns hit
		// the LLM normally.
		pendingToolCalls = nil

		// Per-task cost ceiling check. We do this AFTER the turn is
		// fully recorded (assistant message + tool results in the
		// conversation snapshot) so the operator sees what was paid
		// for. The ceiling is task-cumulative — prior resume-chain
		// spend plus this run's spend. Crossing the ceiling marks
		// the run failed with an actionable error; future turns don't
		// fire. Operators can raise the ceiling and resume to continue.
		if msg, exceeded := runState.CostCeilingExceededMessage(); exceeded {
			res := runState.Result("failed")
			res.LastError = msg
			res.OtelStatusCode = "error"
			res.OtelStatusMessage = "cost_ceiling_exceeded"
			return res, nil
		}
	}

	// Hit max turns without a final answer. Mark incomplete; the user
	// can resume the run if they want more turns.
	res := runState.Result("failed")
	res.LastError = fmt.Sprintf("agent loop hit maxTurns=%d without producing a final answer", e.maxTurns)
	res.OtelStatusCode = "error"
	res.OtelStatusMessage = "max_turns_exceeded"
	return res, nil
}

func emitAgentTurnStarted(spec ExecutionSpec, turn int, req types.ChatRequest) {
	if spec.EmitRunEvent == nil {
		return
	}
	spec.EmitRunEvent(runtimeevents.EventTurnStarted.String(), map[string]any{
		"turn_index":            turn,
		"model":                 req.Model,
		"provider":              req.Scope.ProviderHint,
		"input_tokens_estimate": estimateAgentPromptTokens(req.Messages),
	})
}

func emitAssistantMessageEvents(spec ExecutionSpec, turn int, msg types.Message) {
	if spec.EmitRunEvent == nil {
		return
	}
	if strings.TrimSpace(msg.Content) != "" {
		spec.EmitRunEvent(runtimeevents.EventAssistantTextComplete.String(), map[string]any{
			"turn_index":  turn,
			"block_index": 0,
			"text":        msg.Content,
		})
	}
	for _, call := range msg.ToolCalls {
		spec.EmitRunEvent(runtimeevents.EventAssistantToolCallProposed.String(), map[string]any{
			"turn_index":   turn,
			"tool_call_id": call.ID,
			"tool_name":    call.Function.Name,
			"input":        decodeToolArgumentsForEvent(call.Function.Arguments),
		})
	}
}

func emitAssistantFinalAnswer(spec ExecutionSpec, turn int, msg types.Message) {
	if spec.EmitRunEvent == nil {
		return
	}
	spec.EmitRunEvent(runtimeevents.EventAssistantFinalAnswer.String(), map[string]any{
		"turn_index": turn,
		"summary":    msg.Content,
	})
}

func decodeToolArgumentsForEvent(raw string) any {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return map[string]any{}
	}
	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return map[string]any{
			"raw": trimmed,
		}
	}
	if decoded == nil {
		return map[string]any{}
	}
	return decoded
}

func estimateAgentPromptTokens(messages []types.Message) int {
	chars := 0
	for _, msg := range messages {
		chars += len(msg.Role) + len(msg.Content) + len(msg.ToolCallID)
		for _, block := range msg.ContentBlocks {
			chars += len(block.Type) + len(block.Text)
		}
		for _, call := range msg.ToolCalls {
			chars += len(call.ID) + len(call.Function.Name) + len(call.Function.Arguments)
		}
	}
	if chars == 0 {
		return 0
	}
	return chars/4 + 1
}

// ─── Tool definitions ────────────────────────────────────────────────

// agentToolDefinitions returns the OpenAI tool-call format the loop
// passes to the LLM each turn. Names match the dispatch switch in
// dispatchToolCall(). Schemas are JSON Schema 2020-12, kept minimal
// because verbose schemas waste tokens.
func agentToolDefinitions(includeProjectAssistantDraftOpt ...bool) []types.Tool {
	includeProjectAssistantDraft := len(includeProjectAssistantDraftOpt) > 0 && includeProjectAssistantDraftOpt[0]
	tools := []types.Tool{
		{
			Type: "function",
			Function: types.ToolFunction{
				Name:        "shell_exec",
				Description: "Execute a shell command in the task workspace. Use for any inspection or computation that doesn't write a file.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"command": {"type": "string", "description": "The shell command to run, e.g. 'ls -la' or 'cat README.md'."},
						"working_directory": {"type": "string", "description": "Optional subdirectory under the workspace. Empty = workspace root."}
					},
					"required": ["command"]
				}`),
			},
		},
		{
			Type: "function",
			Function: types.ToolFunction{
				Name:        "git_exec",
				Description: "Run a git command in the task workspace. Use for inspecting history, status, diffs.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"command": {"type": "string", "description": "The git subcommand and args, e.g. 'status' or 'log --oneline -5'."},
						"working_directory": {"type": "string", "description": "Optional subdirectory under the workspace."}
					},
					"required": ["command"]
				}`),
			},
		},
		{
			Type: "function",
			Function: types.ToolFunction{
				Name:        "file_write",
				Description: "Write or append to a file in the task workspace. Use to produce deliverables or update existing files.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"path": {"type": "string", "description": "Relative path under the workspace, e.g. 'output.txt' or 'src/main.py'."},
						"content": {"type": "string", "description": "The full content to write (for write) or to append (for append)."},
						"operation": {"type": "string", "enum": ["write", "append"], "default": "write", "description": "write replaces the file; append adds to the end."}
					},
					"required": ["path", "content"]
				}`),
			},
		},
		{
			Type: "function",
			Function: types.ToolFunction{
				Name:        "file_edit",
				Description: "Replace exact text in an existing workspace file. Prefer this over file_write for targeted code edits because it fails when the match is missing or ambiguous.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"path": {"type": "string", "description": "Relative path under the workspace, e.g. 'src/main.go'."},
						"old_text": {"type": "string", "description": "Exact text to replace. Must appear exactly once unless replace_all=true."},
						"new_text": {"type": "string", "description": "Replacement text."},
						"replace_all": {"type": "boolean", "default": false, "description": "Replace every occurrence instead of requiring exactly one match."},
						"propose": {"type": "boolean", "default": false, "description": "Create a proposed patch artifact without applying it. The operator can apply it later via the patch API."}
					},
					"required": ["path", "old_text", "new_text"]
				}`),
			},
		},
		{
			Type: "function",
			Function: types.ToolFunction{
				Name:        "read_file",
				Description: "Read the contents of a file in the task workspace. Use this instead of `shell_exec(cat ...)` — it's faster and doesn't need a shell. Ungated by default, but operators can gate it with read_file or all_tools approval policy.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"path": {"type": "string", "description": "Relative path under the workspace."},
						"max_bytes": {"type": "integer", "minimum": 1, "maximum": 1048576, "default": 65536, "description": "Cap the read to this many bytes. Larger files are truncated; the truncation is reported in the result."},
						"start_line": {"type": "integer", "minimum": 1, "description": "Optional 1-based first line to return."},
						"end_line": {"type": "integer", "minimum": 1, "description": "Optional 1-based final line to return, inclusive."}
					},
					"required": ["path"]
				}`),
			},
		},
		{
			Type: "function",
			Function: types.ToolFunction{
				Name:        "grep",
				Description: "Search text files in the workspace with a regular expression. Use this instead of `shell_exec(rg ...)` for structured, bounded code search.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"pattern": {"type": "string", "description": "Go regular expression to search for."},
						"path": {"type": "string", "default": ".", "description": "Relative file or directory path to search under."},
						"include": {"type": "string", "description": "Optional glob filter such as '*.go' or 'internal/**/*.go'."},
						"case_sensitive": {"type": "boolean", "default": true},
						"max_matches": {"type": "integer", "minimum": 1, "maximum": 500, "default": 100}
					},
					"required": ["pattern"]
				}`),
			},
		},
		{
			Type: "function",
			Function: types.ToolFunction{
				Name:        "glob",
				Description: "Find workspace files by glob pattern. Use this instead of `shell_exec(find ...)` for structured file discovery.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"pattern": {"type": "string", "description": "Glob pattern matched against workspace-relative paths, e.g. '**/*.go' or 'docs/*.md'."},
						"path": {"type": "string", "default": ".", "description": "Relative directory to search under."},
						"max_matches": {"type": "integer", "minimum": 1, "maximum": 1000, "default": 200}
					},
					"required": ["pattern"]
				}`),
			},
		},
		{
			Type: "function",
			Function: types.ToolFunction{
				Name:        "artifact_read",
				Description: "Read a persisted artifact from the current task by artifact_id. Use this to inspect prior outputs, summaries, stdout, or proposed patches without re-running tools.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"artifact_id": {"type": "string", "description": "Artifact ID from the current task."},
						"max_bytes": {"type": "integer", "minimum": 1, "maximum": 1048576, "default": 65536, "description": "Cap inline content to this many bytes. Larger artifacts are truncated."}
					},
					"required": ["artifact_id"]
				}`),
			},
		},
		{
			Type: "function",
			Function: types.ToolFunction{
				Name:        "list_dir",
				Description: "List files and directories under a workspace path. Use this instead of `shell_exec(ls ...)` for a structured listing that includes file sizes and types.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"path": {"type": "string", "default": ".", "description": "Relative path under the workspace. '.' or empty = workspace root."}
					}
				}`),
			},
		},
		{
			Type: "function",
			Function: types.ToolFunction{
				Name:        "git_status",
				Description: "Return structured git status for the workspace. Use this instead of `git_exec(status)` when you only need branch and file state.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {}
				}`),
			},
		},
		{
			Type: "function",
			Function: types.ToolFunction{
				Name:        "git_diff",
				Description: "Return a bounded git diff for the workspace. Use this instead of `git_exec(diff)` for read-only diff inspection.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"path": {"type": "string", "description": "Optional relative file or directory path to diff."},
						"staged": {"type": "boolean", "default": false, "description": "When true, return the staged diff using --cached."},
						"max_bytes": {"type": "integer", "minimum": 1, "maximum": 1048576, "default": 65536, "description": "Cap diff output to this many bytes. Larger diffs are truncated."}
					}
				}`),
			},
		},
		{
			Type: "function",
			Function: types.ToolFunction{
				Name:        "apply_patch",
				Description: "Apply a structured patch to workspace files. Use for multi-file edits when exact file_edit calls would be tedious. Supports add, update, and delete patch sections.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"patch_text": {"type": "string", "description": "Patch text with *** Begin Patch / *** End Patch markers and Add File, Update File, or Delete File sections."},
						"propose": {"type": "boolean", "default": false, "description": "Create proposed patch artifacts without writing files."}
					},
					"required": ["patch_text"]
				}`),
			},
		},
		{
			Type: "function",
			Function: types.ToolFunction{
				Name:        "http_request",
				Description: "Make an outbound HTTP(S) request. Use for fetching URLs, calling external APIs, or posting to webhooks. Response body is capped to keep prompts cheap; private IPs and unsafe schemes are blocked unless the operator opts in.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"url": {"type": "string", "description": "Absolute http:// or https:// URL."},
						"method": {"type": "string", "enum": ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"], "default": "GET"},
						"headers": {"type": "object", "additionalProperties": {"type": "string"}, "description": "Optional request headers as a flat object."},
						"body": {"type": "string", "description": "Optional request body. For JSON APIs, set Content-Type explicitly via headers."}
					},
					"required": ["url"]
				}`),
			},
		},
	}
	if includeProjectAssistantDraft {
		tools = append(tools, types.Tool{
			Type: "function",
			Function: types.ToolFunction{
				Name:        AgentToolDraftProjectProposal,
				Description: "Draft a reviewable Project Assistant proposal for the linked project. This creates a proposal artifact only; it does not apply changes, start tasks, create chats, run agents, or promote memory. Use when the operator asks to plan, queue, assign, hand off, or capture project work for review.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"request": {"type": "string", "description": "The operator's project-planning request to turn into a reviewable proposal."},
						"work_item_id": {"type": "string", "description": "Optional selected project work item id when the request is about an existing item."},
						"role_id": {"type": "string", "description": "Optional project role id to use for assignment proposals."},
						"driver_kind": {"type": "string", "description": "Optional assignment driver hint, such as hecate_task or external_agent."}
					},
					"required": ["request"]
				}`),
			},
		})
	}
	return tools
}

type shellExecArgs struct {
	Command          string `json:"command"`
	WorkingDirectory string `json:"working_directory,omitempty"`
}

type gitExecArgs struct {
	Command          string `json:"command"`
	WorkingDirectory string `json:"working_directory,omitempty"`
}

type fileWriteArgs struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Operation string `json:"operation,omitempty"`
}

type fileEditArgs struct {
	Path       string `json:"path"`
	OldText    string `json:"old_text"`
	NewText    string `json:"new_text"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
	Propose    bool   `json:"propose,omitempty"`
}

type readFileArgs struct {
	Path      string `json:"path"`
	MaxBytes  int    `json:"max_bytes,omitempty"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
}

type grepArgs struct {
	Pattern       string `json:"pattern"`
	Path          string `json:"path,omitempty"`
	Include       string `json:"include,omitempty"`
	CaseSensitive *bool  `json:"case_sensitive,omitempty"`
	MaxMatches    int    `json:"max_matches,omitempty"`
}

type globArgs struct {
	Pattern    string `json:"pattern"`
	Path       string `json:"path,omitempty"`
	MaxMatches int    `json:"max_matches,omitempty"`
}

type artifactReadArgs struct {
	ArtifactID string `json:"artifact_id"`
	MaxBytes   int    `json:"max_bytes,omitempty"`
}

type listDirArgs struct {
	Path string `json:"path,omitempty"`
}

type gitStatusArgs struct{}

type gitDiffArgs struct {
	Path     string `json:"path,omitempty"`
	Staged   bool   `json:"staged,omitempty"`
	MaxBytes int    `json:"max_bytes,omitempty"`
}

type applyPatchArgs struct {
	PatchText string `json:"patch_text"`
	Propose   bool   `json:"propose,omitempty"`
}

// ─── Helpers ────────────────────────────────────────────────────────

func buildThinkingStep(spec ExecutionSpec, index, turn int, startedAt time.Time, msg types.Message, resp *types.ChatResponse, runCumulativeMicrosUSD int64) types.TaskStep {
	toolNames := make([]string, 0, len(msg.ToolCalls))
	for _, tc := range msg.ToolCalls {
		toolNames = append(toolNames, tc.Function.Name)
	}
	model := ""
	turnCost := int64(0)
	if resp != nil {
		model = resp.Model
		turnCost = resp.Cost.TotalMicrosUSD
	}
	return types.TaskStep{
		ID:       spec.NewID("step"),
		TaskID:   spec.Task.ID,
		RunID:    spec.Run.ID,
		Index:    index,
		Kind:     "model",
		Title:    fmt.Sprintf("Agent turn %d", turn),
		Status:   "completed",
		Phase:    "thinking",
		Result:   telemetry.ResultSuccess,
		ToolName: "builtin.agent_loop_llm",
		Input: map[string]any{
			"turn":  turn,
			"model": model,
		},
		// cost_micros_usd is this turn's LLM spend; the UI renders
		// it next to the turn label in the conversation viewer so
		// operators see cost in context. run_cumulative_cost_micros_usd
		// is the running total for this run only — task-level
		// cumulative (including prior runs in the resume chain) lives
		// on the run cost badge in the header. Both numbers serialize
		// as JSON ints; clients should treat absent/zero as "no LLM
		// cost was attributed" (e.g. resumed-after-approval steps
		// emitted via buildResumeThinkingStep).
		OutputSummary: map[string]any{
			"content_chars":                  len(msg.Content),
			"tool_calls":                     toolNames,
			"finish_reason":                  finishReason(resp),
			"cost_micros_usd":                turnCost,
			"run_cumulative_cost_micros_usd": runCumulativeMicrosUSD,
		},
		StartedAt:  startedAt,
		FinishedAt: time.Now().UTC(),
		RequestID:  spec.RequestID,
		TraceID:    spec.TraceID,
	}
}

func buildFinalAnswerArtifact(spec ExecutionSpec, stepID string, startedAt time.Time, content string) types.TaskArtifact {
	return types.TaskArtifact{
		ID:          spec.NewID("artifact"),
		TaskID:      spec.Task.ID,
		RunID:       spec.Run.ID,
		StepID:      stepID,
		Kind:        "summary",
		Name:        "agent-final-answer.txt",
		Description: "Agent loop final answer",
		MimeType:    "text/plain",
		StorageKind: "inline",
		ContentText: content,
		SizeBytes:   int64(len(content)),
		Status:      "ready",
		CreatedAt:   startedAt,
		RequestID:   spec.RequestID,
		TraceID:     spec.TraceID,
	}
}

func toolInputForLog(name string, task types.Task) map[string]any {
	switch name {
	case "shell_exec":
		return map[string]any{"command": task.ShellCommand, "working_directory": task.WorkingDirectory}
	case "git_exec":
		return map[string]any{"command": task.GitCommand, "working_directory": task.WorkingDirectory}
	case "file_write", "file_edit":
		return map[string]any{"path": task.FilePath, "operation": task.FileOperation, "content_chars": len(task.FileContent)}
	}
	return nil
}

// summarizeSubResult builds the text the LLM sees as the tool result.
// We include status + last_error + a content digest (stdout for
// shell/git, the written path for file_write) — enough for the model
// to decide what to do next without bloating the next prompt.
//
// The token-efficiency trade-off: dumping full stdout would let the
// model "see" the file it just inspected, but pushes context cost up
// fast on a real task. Operators can ship a custom executor that
// summarizes more aggressively if they have specific token budgets.
func summarizeSubResult(r *ExecutionResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "status=%s", r.Status)
	if r.LastError != "" {
		fmt.Fprintf(&b, "\nerror=%s", r.LastError)
	}
	for _, art := range r.Artifacts {
		switch art.Kind {
		case "stdout", "stderr":
			content := art.ContentText
			if len(content) > 4000 {
				content = content[:4000] + "…(truncated)"
			}
			fmt.Fprintf(&b, "\n--- %s ---\n%s", art.Kind, content)
		case "file":
			fmt.Fprintf(&b, "\nwrote file: %s (%d bytes)", art.Name, art.SizeBytes)
		}
	}
	return b.String()
}

// ─── Inline read tools ──────────────────────────────────────────────
//
// Read-only inspection tools are deliberately implemented inline here
// rather than going through the FileExecutor. They don't need a
// sandbox, and the LLM hits them frequently — keeping them off the
// executor path saves goroutine + sandbox overhead while still
// allowing operators to gate them through approval policy.
//
// Path safety: every relative path is resolved against the workspace
// root and rejected if the result would land outside. This is the
// same protection a sandbox would provide; we do it explicitly here
// because we're bypassing the sandbox.

const (
	readFileDefaultMaxBytes = 64 * 1024
	readFileHardCapBytes    = 1024 * 1024
	grepDefaultMaxMatches   = 100
	grepHardCapMatches      = 500
	grepFileHardCapBytes    = 1024 * 1024
	globDefaultMaxMatches   = 200
	globHardCapMatches      = 1000
	artifactDefaultMaxBytes = 64 * 1024
	artifactHardCapBytes    = 1024 * 1024
	gitDiffDefaultMaxBytes  = 64 * 1024
	gitDiffHardCapBytes     = 1024 * 1024
	fileEditHardCapBytes    = 2 * 1024 * 1024
	listDirEntryCap         = 500
)

func workspaceFileSystem(spec ExecutionSpec) (*workspacefs.FS, string) {
	root := strings.TrimSpace(spec.Task.WorkingDirectory)
	if root == "" {
		// No workspace configured — operate from current dir as a
		// permissive fallback for tests. In production runner sets
		// this to the run's WorkspacePath before dispatching.
		root, _ = os.Getwd()
	}
	fsys, err := workspacefs.New(root)
	if err != nil {
		return nil, err.Error()
	}
	return fsys, ""
}

func workspaceFSPath(spec ExecutionSpec, relPath string) (*workspacefs.FS, string, string, string) {
	rel := strings.TrimSpace(relPath)
	if rel == "" || rel == "." {
		rel = "."
	}
	fsys, errMsg := workspaceFileSystem(spec)
	if errMsg != "" {
		return nil, "", "", errMsg
	}
	abs, err := fsys.Resolve(rel)
	if err != nil {
		if filepath.IsAbs(rel) {
			return nil, "", "", fmt.Sprintf("path must be relative to the workspace, got absolute: %q", rel)
		}
		return nil, "", "", err.Error()
	}
	return fsys, rel, abs, ""
}

// resolveWorkspacePath joins relPath onto the run's workspace root using the
// same symlink-aware resolver as sandboxed file writes.
func resolveWorkspacePath(spec ExecutionSpec, relPath string) (string, string) {
	_, _, abs, errMsg := workspaceFSPath(spec, relPath)
	if errMsg != "" {
		return "", errMsg
	}
	return abs, ""
}

func prepareFileEditTask(spec ExecutionSpec, args fileEditArgs) (types.Task, string, string, string, string) {
	if args.Path == "" {
		return types.Task{}, "", "", "", "file_edit: path is required"
	}
	if args.OldText == "" {
		return types.Task{}, "", "", "", "file_edit: old_text is required"
	}
	fsys, rel, abs, errMsg := workspaceFSPath(spec, args.Path)
	if errMsg != "" {
		return types.Task{}, "", "", "", errMsg
	}
	info, _, err := fsys.Stat(rel)
	if err != nil {
		return types.Task{}, "", "", "", fmt.Sprintf("file_edit: %v", err)
	}
	if info.IsDir() {
		return types.Task{}, "", "", "", fmt.Sprintf("file_edit: %q is a directory", args.Path)
	}
	if info.Size() > fileEditHardCapBytes {
		return types.Task{}, "", "", "", fmt.Sprintf("file_edit: %q is too large (%d bytes > %d)", args.Path, info.Size(), fileEditHardCapBytes)
	}
	raw, _, err := fsys.ReadFile(rel)
	if err != nil {
		return types.Task{}, "", "", "", fmt.Sprintf("file_edit: %v", err)
	}
	current := string(raw)
	count := strings.Count(current, args.OldText)
	if count == 0 {
		return types.Task{}, "", "", "", fmt.Sprintf("file_edit: old_text not found in %q", args.Path)
	}
	if count > 1 && !args.ReplaceAll {
		return types.Task{}, "", "", "", fmt.Sprintf("file_edit: old_text appears %d times in %q; set replace_all=true or provide a more specific match", count, args.Path)
	}
	limit := 1
	if args.ReplaceAll {
		limit = -1
	}
	next := strings.Replace(current, args.OldText, args.NewText, limit)
	if next == current {
		return types.Task{}, "", "", "", fmt.Sprintf("file_edit: replacement produced no change in %q", args.Path)
	}

	taskCopy := spec.Task
	taskCopy.ExecutionKind = "file"
	taskCopy.FilePath = args.Path
	taskCopy.FileContent = next
	taskCopy.FileOperation = "write"
	return taskCopy, current, next, abs, ""
}

func proposedFileEditToolResult(spec ExecutionSpec, args fileEditArgs, stepIndex int, startedAt time.Time, toolCallID, toolName, absPath, before, after string) (string, *types.TaskStep, []types.TaskArtifact, error) {
	finishedAt := time.Now().UTC()
	step := types.TaskStep{
		ID:         spec.NewID("step"),
		TaskID:     spec.Task.ID,
		RunID:      spec.Run.ID,
		Index:      stepIndex,
		Kind:       "tool",
		Title:      fmt.Sprintf("%s (proposed)", toolName),
		Status:     "completed",
		Phase:      "execution",
		Result:     telemetry.ResultSuccess,
		ToolName:   toolName,
		Input:      map[string]any{"path": args.Path, "operation": "propose", "content_chars": len(after)},
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		RequestID:  spec.RequestID,
		TraceID:    spec.TraceID,
	}
	artifact := newPatchArtifact(spec, step.ID, "write", args.Path, absPath, before, after, true, finishedAt)
	artifact.Status = "proposed"
	artifact.Description = "Proposed unified diff produced by file_edit"
	if spec.EmitRunEvent != nil {
		spec.EmitRunEvent(runtimeevents.EventFilePatch.String(), map[string]any{
			"tool_call_id":    firstNonEmpty(toolCallID, step.ID),
			"tool_name":       toolName,
			"kind":            "file",
			"operation":       "propose",
			"path":            artifact.Path,
			"artifact_id":     artifact.ID,
			"bytes_written":   0,
			"diff_bytes":      artifact.SizeBytes,
			"before_existed":  true,
			"artifact_status": artifact.Status,
		})
	}
	return fmt.Sprintf("status=proposed\npatch_artifact_id=%s\npath=%s", artifact.ID, args.Path), &step, []types.TaskArtifact{artifact}, nil
}

func applyPatchTool(spec ExecutionSpec, args applyPatchArgs, stepIndex int, startedAt time.Time, toolCallID, toolName string) (string, *types.TaskStep, []types.TaskArtifact, error) {
	ops, errMsg := parseStructuredPatch(args.PatchText)
	if errMsg != "" {
		return errMsg, nil, nil, nil
	}
	if len(ops) == 0 {
		return "apply_patch: patch contains no file operations", nil, nil, nil
	}
	if !args.Propose && spec.Task.SandboxReadOnly {
		return "apply_patch: sandbox policy denied: write access is disabled", nil, nil, nil
	}

	step := types.TaskStep{
		ID:       spec.NewID("step"),
		TaskID:   spec.Task.ID,
		RunID:    spec.Run.ID,
		Index:    stepIndex,
		Kind:     "tool",
		Title:    fmt.Sprintf("%s (%d files)", toolName, len(ops)),
		Status:   "completed",
		Phase:    "execution",
		Result:   telemetry.ResultSuccess,
		ToolName: toolName,
		Input: map[string]any{
			"patch_chars": len(args.PatchText),
			"propose":     args.Propose,
			"file_count":  len(ops),
		},
		StartedAt: startedAt,
		RequestID: spec.RequestID,
		TraceID:   spec.TraceID,
	}

	prepared := make([]preparedPatchOperation, 0, len(ops))
	for _, op := range ops {
		if errMsg := validatePatchOperationPath(op); errMsg != "" {
			return errMsg, nil, nil, nil
		}
		if args.Propose && op.Kind == "delete" {
			return "apply_patch: propose=true does not support delete sections because proposed-patch apply currently writes after-content rather than removing files", nil, nil, nil
		}
		fsys, rel, abs, errMsg := workspaceFSPath(spec, op.Path)
		if errMsg != "" {
			return "apply_patch: " + errMsg, nil, nil, nil
		}
		before, after, beforeExists, errMsg := preparePatchOperation(fsys, rel, op)
		if errMsg != "" {
			return errMsg, nil, nil, nil
		}
		prepared = append(prepared, preparedPatchOperation{op: op, relPath: rel, absPath: abs, before: before, after: after, beforeExists: beforeExists})
	}
	if !args.Propose {
		for _, item := range prepared {
			fsys, _, _, errMsg := workspaceFSPath(spec, item.relPath)
			if errMsg != "" {
				return "apply_patch: " + errMsg, nil, nil, nil
			}
			if err := writePatchOperation(fsys, item.relPath, item.after, item.op.Kind); err != nil {
				return fmt.Sprintf("apply_patch: %v", err), nil, nil, nil
			}
		}
	}

	finishedAt := time.Now().UTC()
	step.FinishedAt = finishedAt
	artifacts := make([]types.TaskArtifact, 0, len(prepared))
	var summaries []string
	for _, item := range prepared {
		op := item.op
		artifact := newPatchArtifact(spec, step.ID, op.Kind, op.Path, item.absPath, item.before, item.after, item.beforeExists, finishedAt)
		if args.Propose {
			artifact.Status = "proposed"
			artifact.Description = "Proposed unified diff produced by apply_patch"
		}
		artifacts = append(artifacts, artifact)
		emitInlinePatchEvent(spec, step.ID, toolCallID, toolName, op.Kind, artifact, item.beforeExists, len(item.after), args.Propose)
		summaries = append(summaries, fmt.Sprintf("%s %s patch_artifact_id=%s", op.Kind, op.Path, artifact.ID))
	}
	step.OutputSummary = map[string]any{
		"file_count": len(ops),
		"proposed":   args.Propose,
	}
	status := "applied"
	if args.Propose {
		status = "proposed"
	}
	return fmt.Sprintf("status=%s\n%s", status, strings.Join(summaries, "\n")), &step, artifacts, nil
}

// readFileTool reads a workspace file and returns the content as the
// tool result text. Bounded by max_bytes; binary files are reported
// rather than dumped (to avoid pushing garbage into the conversation).
func readFileTool(spec ExecutionSpec, args readFileArgs, stepIndex int, startedAt time.Time, toolName string) (string, *types.TaskStep, []types.TaskArtifact, error) {
	fsys, rel, _, errMsg := workspaceFSPath(spec, args.Path)
	if errMsg != "" {
		return errMsg, nil, nil, nil
	}
	maxBytes := args.MaxBytes
	if maxBytes <= 0 {
		maxBytes = readFileDefaultMaxBytes
	}
	if maxBytes > readFileHardCapBytes {
		maxBytes = readFileHardCapBytes
	}

	info, _, err := fsys.Stat(rel)
	if err != nil {
		return fmt.Sprintf("read_file: %v", err), nil, nil, nil
	}
	if info.IsDir() {
		return fmt.Sprintf("read_file: %q is a directory; use list_dir instead", args.Path), nil, nil, nil
	}

	displayContent, lineRange, n, truncated, errMsg := readWorkspaceFileContent(fsys, rel, args.Path, info.Size(), maxBytes, args.StartLine, args.EndLine)
	if errMsg != "" {
		return "read_file: " + errMsg, nil, nil, nil
	}

	step := buildReadFileStep(spec, stepIndex, startedAt, toolName, args.Path, info.Size(), int64(n), truncated)
	var b strings.Builder
	fmt.Fprintf(&b, "path=%s size=%d bytes=%d", args.Path, info.Size(), n)
	if truncated {
		fmt.Fprintf(&b, " truncated=true")
	}
	if lineRange != "" {
		fmt.Fprintf(&b, " lines=%s", lineRange)
	}
	b.WriteString("\n--- content ---\n")
	b.WriteString(displayContent)
	if truncated {
		b.WriteString("\n…(truncated)")
	}
	return b.String(), &step, nil, nil
}

func grepTool(spec ExecutionSpec, args grepArgs, stepIndex int, startedAt time.Time, toolName string) (string, *types.TaskStep, []types.TaskArtifact, error) {
	if strings.TrimSpace(args.Pattern) == "" {
		return "grep: pattern is required", nil, nil, nil
	}
	pattern := args.Pattern
	caseSensitive := true
	if args.CaseSensitive != nil {
		caseSensitive = *args.CaseSensitive
	}
	if !caseSensitive {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Sprintf("grep: invalid regex: %v", err), nil, nil, nil
	}
	maxMatches := args.MaxMatches
	if maxMatches <= 0 {
		maxMatches = grepDefaultMaxMatches
	}
	if maxMatches > grepHardCapMatches {
		maxMatches = grepHardCapMatches
	}
	fsys, rootRel, _, errMsg := workspaceFSPath(spec, args.Path)
	if errMsg != "" {
		return "grep: " + errMsg, nil, nil, nil
	}

	var matches []grepMatch
	err = fsys.WalkDir(rootRel, func(_ string, rel string, entry workspacefs.DirEntry) error {
		if len(matches) >= maxMatches {
			return filepath.SkipAll
		}
		if shouldSkipSearchDir(entry) {
			return filepath.SkipDir
		}
		if entry.IsDir {
			return nil
		}
		displayRel := filepath.ToSlash(rel)
		if args.Include != "" && !globPatternMatches(args.Include, displayRel) && !globPatternMatches(args.Include, filepath.Base(displayRel)) {
			return nil
		}
		fileMatches, err := grepFile(fsys, rel, displayRel, re, maxMatches-len(matches))
		if err != nil {
			return nil
		}
		matches = append(matches, fileMatches...)
		return nil
	})
	if err != nil && err != filepath.SkipAll {
		return fmt.Sprintf("grep: %v", err), nil, nil, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "pattern=%q matches=%d", args.Pattern, len(matches))
	if len(matches) >= maxMatches {
		fmt.Fprintf(&b, " truncated=true")
	}
	b.WriteByte('\n')
	for _, m := range matches {
		fmt.Fprintf(&b, "%s:%d:%s\n", m.Path, m.Line, m.Text)
	}
	step := buildGenericReadToolStep(spec, stepIndex, startedAt, toolName, map[string]any{
		"pattern":        args.Pattern,
		"path":           firstNonEmpty(args.Path, "."),
		"include":        args.Include,
		"case_sensitive": caseSensitive,
		"matches":        len(matches),
		"max_matches":    maxMatches,
	})
	return b.String(), &step, nil, nil
}

func globTool(spec ExecutionSpec, args globArgs, stepIndex int, startedAt time.Time, toolName string) (string, *types.TaskStep, []types.TaskArtifact, error) {
	if strings.TrimSpace(args.Pattern) == "" {
		return "glob: pattern is required", nil, nil, nil
	}
	maxMatches := args.MaxMatches
	if maxMatches <= 0 {
		maxMatches = globDefaultMaxMatches
	}
	if maxMatches > globHardCapMatches {
		maxMatches = globHardCapMatches
	}
	fsys, rootRel, _, errMsg := workspaceFSPath(spec, args.Path)
	if errMsg != "" {
		return "glob: " + errMsg, nil, nil, nil
	}
	var matches []string
	err := fsys.WalkDir(rootRel, func(_ string, rel string, entry workspacefs.DirEntry) error {
		if shouldSkipSearchDir(entry) {
			return filepath.SkipDir
		}
		displayRel := filepath.ToSlash(rel)
		if displayRel == "." {
			return nil
		}
		if globPatternMatches(args.Pattern, displayRel) || globPatternMatches(args.Pattern, filepath.Base(displayRel)) {
			suffix := ""
			if entry.IsDir {
				suffix = "/"
			}
			matches = append(matches, displayRel+suffix)
			if len(matches) >= maxMatches {
				return filepath.SkipAll
			}
		}
		return nil
	})
	if err != nil && err != filepath.SkipAll {
		return fmt.Sprintf("glob: %v", err), nil, nil, nil
	}
	sort.Strings(matches)
	var b strings.Builder
	fmt.Fprintf(&b, "pattern=%q matches=%d", args.Pattern, len(matches))
	if len(matches) >= maxMatches {
		fmt.Fprintf(&b, " truncated=true")
	}
	b.WriteByte('\n')
	for _, match := range matches {
		b.WriteString(match)
		b.WriteByte('\n')
	}
	step := buildGenericReadToolStep(spec, stepIndex, startedAt, toolName, map[string]any{
		"pattern":     args.Pattern,
		"path":        firstNonEmpty(args.Path, "."),
		"matches":     len(matches),
		"max_matches": maxMatches,
	})
	return b.String(), &step, nil, nil
}

func artifactReadTool(spec ExecutionSpec, args artifactReadArgs, stepIndex int, startedAt time.Time, toolName string) (string, *types.TaskStep, []types.TaskArtifact, error) {
	artifactID := strings.TrimSpace(args.ArtifactID)
	if artifactID == "" {
		return "artifact_read: artifact_id is required", nil, nil, nil
	}
	if spec.GetArtifact == nil {
		return "artifact_read: artifact lookup is not available for this run", nil, nil, nil
	}
	maxBytes := args.MaxBytes
	if maxBytes <= 0 {
		maxBytes = artifactDefaultMaxBytes
	}
	if maxBytes > artifactHardCapBytes {
		maxBytes = artifactHardCapBytes
	}
	artifact, found, err := spec.GetArtifact(spec.Task.ID, artifactID)
	if err != nil {
		return fmt.Sprintf("artifact_read: %v", err), nil, nil, nil
	}
	if !found {
		return fmt.Sprintf("artifact_read: artifact %q not found for task %q", artifactID, spec.Task.ID), nil, nil, nil
	}

	content := artifact.ContentText
	truncated := false
	if len(content) > maxBytes {
		content = content[:maxBytes]
		truncated = true
	}
	var b strings.Builder
	fmt.Fprintf(&b, "artifact_id=%s kind=%s name=%s storage=%s status=%s size=%d truncated=%v",
		artifact.ID, artifact.Kind, artifact.Name, artifact.StorageKind, artifact.Status, artifact.SizeBytes, truncated)
	if artifact.Path != "" {
		fmt.Fprintf(&b, " path=%s", artifact.Path)
	}
	if artifact.MimeType != "" {
		fmt.Fprintf(&b, " mime=%s", artifact.MimeType)
	}
	if content != "" {
		fmt.Fprintf(&b, "\n--- content ---\n%s", content)
	} else if artifact.StorageKind != "inline" {
		fmt.Fprintf(&b, "\ncontent unavailable inline; storage_kind=%s object_ref=%s path=%s", artifact.StorageKind, artifact.ObjectRef, artifact.Path)
	}

	step := buildGenericReadToolStep(spec, stepIndex, startedAt, toolName, map[string]any{
		"artifact_id": artifactID,
		"kind":        artifact.Kind,
		"name":        artifact.Name,
		"max_bytes":   maxBytes,
		"truncated":   truncated,
	})
	return b.String(), &step, nil, nil
}

// listDirTool lists a workspace directory. Returns one line per entry
// with kind (file/dir/link) and size. Capped at listDirEntryCap so
// huge directories don't bloat the conversation.
func listDirTool(spec ExecutionSpec, args listDirArgs, stepIndex int, startedAt time.Time, toolName string) (string, *types.TaskStep, []types.TaskArtifact, error) {
	fsys, rel, _, errMsg := workspaceFSPath(spec, args.Path)
	if errMsg != "" {
		return errMsg, nil, nil, nil
	}
	info, _, err := fsys.Stat(rel)
	if err != nil {
		return fmt.Sprintf("list_dir: %v", err), nil, nil, nil
	}
	if !info.IsDir() {
		return fmt.Sprintf("list_dir: %q is not a directory", args.Path), nil, nil, nil
	}
	entries, _, err := fsys.ReadDir(rel)
	if err != nil {
		return fmt.Sprintf("list_dir: %v", err), nil, nil, nil
	}
	// Sort for deterministic output — saves token churn across
	// equivalent calls in different turns.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	relPath := args.Path
	if relPath == "" {
		relPath = "."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "path=%s entries=%d", relPath, len(entries))
	if len(entries) > listDirEntryCap {
		fmt.Fprintf(&b, " truncated=%d", listDirEntryCap)
	}
	b.WriteString("\n")
	emitted := 0
	for _, entry := range entries {
		if emitted >= listDirEntryCap {
			break
		}
		kind := "file"
		size := int64(0)
		if entry.IsDir {
			kind = "dir"
		} else if entry.Type&os.ModeSymlink != 0 {
			kind = "link"
		}
		if !entry.IsDir {
			size = entry.Size
		}
		fmt.Fprintf(&b, "%-4s %10d  %s\n", kind, size, entry.Name)
		emitted++
	}

	step := buildListDirStep(spec, stepIndex, startedAt, toolName, relPath, len(entries))
	return b.String(), &step, nil, nil
}

func gitStatusTool(ctx context.Context, spec ExecutionSpec, _ gitStatusArgs, stepIndex int, startedAt time.Time, toolName string) (string, *types.TaskStep, []types.TaskArtifact, error) {
	root, errMsg := workspaceRoot(spec)
	if errMsg != "" {
		return "git_status: " + errMsg, nil, nil, nil
	}
	out, truncated, err := runGitReadCommand(ctx, root, gitDiffDefaultMaxBytes, "status", "--porcelain=v1", "-b")
	if err != nil {
		return fmt.Sprintf("git_status: %v", err), nil, nil, nil
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	branch := ""
	var entries []string
	for _, line := range lines {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			branch = strings.TrimPrefix(line, "## ")
			continue
		}
		entries = append(entries, line)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "branch=%s entries=%d truncated=%v\n", branch, len(entries), truncated)
	for _, entry := range entries {
		fmt.Fprintf(&b, "%s\n", entry)
	}
	step := buildGenericReadToolStep(spec, stepIndex, startedAt, toolName, map[string]any{
		"branch":    branch,
		"entries":   len(entries),
		"truncated": truncated,
	})
	return b.String(), &step, nil, nil
}

func gitDiffTool(ctx context.Context, spec ExecutionSpec, args gitDiffArgs, stepIndex int, startedAt time.Time, toolName string) (string, *types.TaskStep, []types.TaskArtifact, error) {
	root, errMsg := workspaceRoot(spec)
	if errMsg != "" {
		return "git_diff: " + errMsg, nil, nil, nil
	}
	maxBytes := args.MaxBytes
	if maxBytes <= 0 {
		maxBytes = gitDiffDefaultMaxBytes
	}
	if maxBytes > gitDiffHardCapBytes {
		maxBytes = gitDiffHardCapBytes
	}
	relPath := strings.TrimSpace(args.Path)
	gitArgs := []string{"diff", "--no-ext-diff", "--no-textconv"}
	if args.Staged {
		gitArgs = append(gitArgs, "--cached")
	}
	if relPath != "" {
		if _, errMsg := resolveWorkspacePath(spec, relPath); errMsg != "" {
			return "git_diff: " + errMsg, nil, nil, nil
		}
		gitArgs = append(gitArgs, "--", relPath)
	}
	out, truncated, err := runGitReadCommand(ctx, root, maxBytes, gitArgs...)
	if err != nil {
		return fmt.Sprintf("git_diff: %v", err), nil, nil, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "path=%s staged=%v bytes=%d truncated=%v\n", firstNonEmpty(relPath, "."), args.Staged, len(out), truncated)
	if out != "" {
		b.WriteString(out)
	}
	step := buildGenericReadToolStep(spec, stepIndex, startedAt, toolName, map[string]any{
		"path":      firstNonEmpty(relPath, "."),
		"staged":    args.Staged,
		"bytes":     len(out),
		"max_bytes": maxBytes,
		"truncated": truncated,
	})
	return b.String(), &step, nil, nil
}

func runGitReadCommand(ctx context.Context, root string, maxBytes int, args ...string) (string, bool, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	gitArgs := append([]string{"--no-pager"}, args...)
	result, err := gitrunner.NewLocalRunner().RunLimited(cmdCtx, root, int64(maxBytes), gitArgs...)
	if cmdCtx.Err() == context.DeadlineExceeded {
		return "", false, fmt.Errorf("git command timed out")
	}
	out := combineGitReadOutput(result.Stdout, result.Stderr)
	if err != nil {
		text := strings.TrimSpace(out)
		if text != "" {
			return "", false, fmt.Errorf("%w: %s", err, text)
		}
		return "", false, err
	}
	return out, result.StdoutTruncated || result.StderrTruncated, nil
}

func combineGitReadOutput(stdout, stderr string) string {
	if strings.TrimSpace(stderr) == "" {
		return stdout
	}
	if strings.TrimSpace(stdout) == "" {
		return stderr
	}
	if strings.HasSuffix(stdout, "\n") {
		return stdout + stderr
	}
	return stdout + "\n" + stderr
}

func readWorkspaceFileContent(fsys *workspacefs.FS, rel, displayPath string, fileSize int64, maxBytes, startLine, endLine int) (string, string, int, bool, string) {
	f, _, err := fsys.Open(rel)
	if err != nil {
		return "", "", 0, false, err.Error()
	}
	defer f.Close()

	probe := make([]byte, 512)
	probeN, _ := f.Read(probe)
	for _, b := range probe[:probeN] {
		if b == 0 {
			return "", "", 0, false, fmt.Sprintf("%q is a binary file (%d bytes); skipped content. Use file_write to overwrite or shell_exec for inspection.", displayPath, fileSize)
		}
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", "", 0, false, err.Error()
	}

	if startLine > 0 || endLine > 0 {
		content, lineRange, truncated, errMsg := readWorkspaceFileLineRange(f, maxBytes, startLine, endLine)
		return content, lineRange, len(content), truncated, errMsg
	}

	buf := make([]byte, maxBytes)
	n, _ := f.Read(buf)
	content := string(buf[:n])
	truncated := fileSize > int64(n)
	return content, "", n, truncated, ""
}

func readWorkspaceFileLineRange(f *os.File, maxBytes, startLine, endLine int) (string, string, bool, string) {
	if startLine <= 0 {
		startLine = 1
	}
	if endLine > 0 && endLine < startLine {
		return "", "", false, fmt.Sprintf("end_line (%d) must be >= start_line (%d)", endLine, startLine)
	}
	info, err := f.Stat()
	if err != nil {
		return "", "", false, err.Error()
	}
	if info.Size() > readFileHardCapBytes {
		return "", "", false, fmt.Sprintf("file is too large for ranged read (%d bytes > %d)", info.Size(), readFileHardCapBytes)
	}

	reader := bufio.NewReaderSize(f, readFileHardCapBytes+1)
	var b strings.Builder
	lineNo := 0
	lastReturnedLine := 0
	truncated := false

	for {
		line, err := reader.ReadSlice('\n')
		if len(line) > 0 {
			lineNo++
			if lineNo >= startLine && (endLine <= 0 || lineNo <= endLine) {
				lastReturnedLine = lineNo
				if b.Len() < maxBytes {
					remaining := maxBytes - b.Len()
					if len(line) > remaining {
						b.Write(line[:remaining])
						truncated = true
					} else {
						b.Write(line)
					}
				} else {
					truncated = true
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err == bufio.ErrBufferFull {
			return "", "", false, fmt.Sprintf("line %d exceeds ranged read limit (%d bytes)", lineNo, readFileHardCapBytes)
		}
		if err != nil {
			return "", "", false, err.Error()
		}
		if endLine > 0 && lineNo >= endLine {
			break
		}
		if endLine <= 0 && b.Len() >= maxBytes && lineNo >= startLine {
			truncated = true
			break
		}
	}

	if startLine > lineNo {
		return "", "", false, fmt.Sprintf("start_line (%d) is beyond file line count (%d)", startLine, lineNo)
	}
	if lastReturnedLine == 0 {
		lastReturnedLine = lineNo
	}
	return b.String(), fmt.Sprintf("%d-%d", startLine, lastReturnedLine), truncated, ""
}

func buildReadFileStep(spec ExecutionSpec, index int, startedAt time.Time, toolName, path string, fileSize, readBytes int64, truncated bool) types.TaskStep {
	return types.TaskStep{
		ID:       spec.NewID("step"),
		TaskID:   spec.Task.ID,
		RunID:    spec.Run.ID,
		Index:    index,
		Kind:     "tool",
		Title:    fmt.Sprintf("read_file %s", path),
		Status:   "completed",
		Phase:    "execution",
		Result:   telemetry.ResultSuccess,
		ToolName: toolName,
		Input: map[string]any{
			"path":      path,
			"size":      fileSize,
			"truncated": truncated,
		},
		OutputSummary: map[string]any{
			"bytes_read": readBytes,
		},
		StartedAt:  startedAt,
		FinishedAt: time.Now().UTC(),
		RequestID:  spec.RequestID,
		TraceID:    spec.TraceID,
	}
}

func buildListDirStep(spec ExecutionSpec, index int, startedAt time.Time, toolName, path string, entryCount int) types.TaskStep {
	return types.TaskStep{
		ID:       spec.NewID("step"),
		TaskID:   spec.Task.ID,
		RunID:    spec.Run.ID,
		Index:    index,
		Kind:     "tool",
		Title:    fmt.Sprintf("list_dir %s", path),
		Status:   "completed",
		Phase:    "execution",
		Result:   telemetry.ResultSuccess,
		ToolName: toolName,
		Input: map[string]any{
			"path": path,
		},
		OutputSummary: map[string]any{
			"entry_count": entryCount,
		},
		StartedAt:  startedAt,
		FinishedAt: time.Now().UTC(),
		RequestID:  spec.RequestID,
		TraceID:    spec.TraceID,
	}
}

type patchOperation struct {
	Kind  string
	Path  string
	Lines []string
}

type preparedPatchOperation struct {
	op           patchOperation
	relPath      string
	absPath      string
	before       string
	after        string
	beforeExists bool
}

type grepMatch struct {
	Path string
	Line int
	Text string
}

func buildGenericReadToolStep(spec ExecutionSpec, index int, startedAt time.Time, toolName string, input map[string]any) types.TaskStep {
	return types.TaskStep{
		ID:         spec.NewID("step"),
		TaskID:     spec.Task.ID,
		RunID:      spec.Run.ID,
		Index:      index,
		Kind:       "tool",
		Title:      toolName,
		Status:     "completed",
		Phase:      "execution",
		Result:     telemetry.ResultSuccess,
		ToolName:   toolName,
		Input:      input,
		StartedAt:  startedAt,
		FinishedAt: time.Now().UTC(),
		RequestID:  spec.RequestID,
		TraceID:    spec.TraceID,
	}
}

func workspaceRoot(spec ExecutionSpec) (string, string) {
	root, errMsg := resolveWorkspacePath(spec, ".")
	if errMsg != "" {
		return "", errMsg
	}
	return root, ""
}

func shouldSkipSearchDir(entry workspacefs.DirEntry) bool {
	if !entry.IsDir {
		return false
	}
	switch entry.Name {
	case ".git", "node_modules", ".gocache", "dist", "build":
		return true
	default:
		return false
	}
}

func grepFile(fsys *workspacefs.FS, rel, displayRel string, re *regexp.Regexp, remaining int) ([]grepMatch, error) {
	info, _, err := fsys.Stat(rel)
	if err != nil || info.IsDir() || info.Size() > grepFileHardCapBytes {
		return nil, err
	}
	raw, _, err := fsys.ReadFile(rel)
	if err != nil {
		return nil, err
	}
	probe := raw
	if len(probe) > 512 {
		probe = probe[:512]
	}
	for _, b := range probe {
		if b == 0 {
			return nil, nil
		}
	}
	lines := strings.Split(string(raw), "\n")
	matches := make([]grepMatch, 0)
	for i, line := range lines {
		if re.MatchString(line) {
			matches = append(matches, grepMatch{Path: displayRel, Line: i + 1, Text: line})
			if len(matches) >= remaining {
				break
			}
		}
	}
	return matches, nil
}

func globPatternMatches(pattern, rel string) bool {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	rel = filepath.ToSlash(rel)
	if ok, _ := filepath.Match(pattern, rel); ok {
		return true
	}
	re, err := regexp.Compile("^" + regexp.QuoteMeta(pattern) + "$")
	if err != nil {
		return false
	}
	expr := re.String()
	expr = strings.ReplaceAll(expr, `\*\*`, `.*`)
	expr = strings.ReplaceAll(expr, `\*`, `[^/]*`)
	expr = strings.ReplaceAll(expr, `\?`, `[^/]`)
	re, err = regexp.Compile(expr)
	if err != nil {
		return false
	}
	return re.MatchString(rel)
}

func parseStructuredPatch(patchText string) ([]patchOperation, string) {
	lines := strings.SplitAfter(patchText, "\n")
	var ops []patchOperation
	var current *patchOperation
	seenBegin := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			if current != nil {
				current.Lines = append(current.Lines, line)
			}
		case trimmed == "*** Begin Patch":
			seenBegin = true
		case trimmed == "*** End Patch":
			if !seenBegin {
				return nil, "apply_patch: patch must start with *** Begin Patch"
			}
			if current != nil {
				ops = append(ops, *current)
				current = nil
			}
			return ops, ""
		case strings.HasPrefix(trimmed, "*** Add File: "):
			if !seenBegin {
				return nil, "apply_patch: patch must start with *** Begin Patch"
			}
			if current != nil {
				ops = append(ops, *current)
			}
			current = &patchOperation{Kind: "add", Path: strings.TrimSpace(strings.TrimPrefix(trimmed, "*** Add File: "))}
		case strings.HasPrefix(trimmed, "*** Update File: "):
			if !seenBegin {
				return nil, "apply_patch: patch must start with *** Begin Patch"
			}
			if current != nil {
				ops = append(ops, *current)
			}
			current = &patchOperation{Kind: "update", Path: strings.TrimSpace(strings.TrimPrefix(trimmed, "*** Update File: "))}
		case strings.HasPrefix(trimmed, "*** Delete File: "):
			if !seenBegin {
				return nil, "apply_patch: patch must start with *** Begin Patch"
			}
			if current != nil {
				ops = append(ops, *current)
			}
			ops = append(ops, patchOperation{Kind: "delete", Path: strings.TrimSpace(strings.TrimPrefix(trimmed, "*** Delete File: "))})
			current = nil
		default:
			if !seenBegin {
				return nil, "apply_patch: patch must start with *** Begin Patch"
			}
			if current == nil {
				if strings.HasPrefix(trimmed, "***") {
					return nil, fmt.Sprintf("apply_patch: unsupported patch directive %q", trimmed)
				}
				continue
			}
			current.Lines = append(current.Lines, line)
		}
	}
	return nil, "apply_patch: patch missing *** End Patch"
}

func validatePatchOperationPath(op patchOperation) string {
	if strings.TrimSpace(op.Path) == "" {
		return fmt.Sprintf("apply_patch: %s file path is required", op.Kind)
	}
	return ""
}

func readPatchTargetFile(fsys *workspacefs.FS, rel, path string) ([]byte, string) {
	info, _, err := fsys.Stat(rel)
	if err != nil {
		return nil, fmt.Sprintf("apply_patch: %v", err)
	}
	if info.IsDir() {
		return nil, fmt.Sprintf("apply_patch: %q is a directory", path)
	}
	if info.Size() > fileEditHardCapBytes {
		return nil, fmt.Sprintf("apply_patch: %q is too large (%d bytes > %d)", path, info.Size(), fileEditHardCapBytes)
	}
	raw, _, err := fsys.ReadFile(rel)
	if err != nil {
		return nil, fmt.Sprintf("apply_patch: %v", err)
	}
	return raw, ""
}

func preparePatchOperation(fsys *workspacefs.FS, rel string, op patchOperation) (before, after string, beforeExists bool, errMsg string) {
	switch op.Kind {
	case "add":
		if _, _, err := fsys.Stat(rel); err == nil {
			return "", "", false, fmt.Sprintf("apply_patch: %q already exists", op.Path)
		} else if !os.IsNotExist(err) {
			return "", "", false, fmt.Sprintf("apply_patch: %v", err)
		}
		return "", patchAddedContent(op.Lines), false, ""
	case "delete":
		raw, errMsg := readPatchTargetFile(fsys, rel, op.Path)
		if errMsg != "" {
			return "", "", false, errMsg
		}
		return string(raw), "", true, ""
	case "update":
		raw, errMsg := readPatchTargetFile(fsys, rel, op.Path)
		if errMsg != "" {
			return "", "", false, errMsg
		}
		next, errMsg := applyPatchUpdate(string(raw), op.Lines)
		if errMsg != "" {
			return "", "", false, fmt.Sprintf("apply_patch: %s: %s", op.Path, errMsg)
		}
		if next == string(raw) {
			return "", "", false, fmt.Sprintf("apply_patch: %s: patch produced no change", op.Path)
		}
		return string(raw), next, true, ""
	default:
		return "", "", false, fmt.Sprintf("apply_patch: unsupported operation %q", op.Kind)
	}
}

func patchAddedContent(lines []string) string {
	var b strings.Builder
	for _, line := range lines {
		if strings.HasPrefix(line, "+") {
			b.WriteString(line[1:])
		}
	}
	return b.String()
}

func applyPatchUpdate(current string, patchLines []string) (string, string) {
	orig := strings.SplitAfter(current, "\n")
	if len(orig) == 1 && orig[0] == "" {
		orig = nil
	}
	pos := 0
	out := make([]string, 0, len(orig)+len(patchLines))
	consume := func(text string, keep bool) bool {
		for pos < len(orig) && orig[pos] != text {
			out = append(out, orig[pos])
			pos++
		}
		if pos >= len(orig) {
			return false
		}
		if keep {
			out = append(out, orig[pos])
		}
		pos++
		return true
	}
	for _, line := range patchLines {
		trimmed := strings.TrimSpace(line)
		if line == "" || isPatchSeparatorLine(line) || strings.HasPrefix(trimmed, "@@") || strings.HasPrefix(trimmed, `\ No newline`) {
			continue
		}
		switch line[0] {
		case ' ':
			if !consume(line[1:], true) {
				return "", fmt.Sprintf("context line not found: %q", strings.TrimSuffix(line[1:], "\n"))
			}
		case '-':
			if !consume(line[1:], false) {
				return "", fmt.Sprintf("removal line not found: %q", strings.TrimSuffix(line[1:], "\n"))
			}
		case '+':
			out = append(out, line[1:])
		default:
			return "", fmt.Sprintf("invalid update line %q", strings.TrimSuffix(line, "\n"))
		}
	}
	out = append(out, orig[pos:]...)
	return strings.Join(out, ""), ""
}

func writePatchOperation(fsys *workspacefs.FS, rel, after, kind string) error {
	switch kind {
	case "delete":
		_, err := fsys.Remove(rel)
		return err
	default:
		_, err := fsys.WriteFile(rel, []byte(after), 0o644)
		return err
	}
}

func isPatchSeparatorLine(line string) bool {
	if strings.TrimSpace(line) != "" {
		return false
	}
	switch line[0] {
	case ' ', '+', '-':
		return false
	default:
		return true
	}
}

func emitInlinePatchEvent(spec ExecutionSpec, stepID, toolCallID, toolName, operation string, artifact types.TaskArtifact, beforeExists bool, bytesWritten int, proposed bool) {
	if spec.EmitRunEvent == nil {
		return
	}
	status := artifact.Status
	if proposed {
		status = "proposed"
		bytesWritten = 0
	}
	spec.EmitRunEvent(runtimeevents.EventFilePatch.String(), map[string]any{
		"tool_call_id":    firstNonEmpty(toolCallID, stepID),
		"tool_name":       toolName,
		"kind":            "file",
		"operation":       operation,
		"path":            artifact.Path,
		"artifact_id":     artifact.ID,
		"bytes_written":   bytesWritten,
		"diff_bytes":      artifact.SizeBytes,
		"before_existed":  beforeExists,
		"artifact_status": status,
	})
}

// checkPublicHost returns an error message string if any of the
// host's resolved IPs falls in a blocked range. Empty string = safe.
//
// We resolve via net.DefaultResolver (DNS) explicitly here rather
// than relying on the http client's transport, because we want to
// inspect the IPs BEFORE the connection happens. A cleaner long-term
// solution wraps net.Dialer.Control with the same check (which also
// catches DNS rebinding) — tracked separately.
func checkPublicHost(ctx context.Context, host string) string {
	// Literal IP shortcut.
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return fmt.Sprintf("http_request: target IP %s is private/loopback/link-local; set HECATE_TASK_HTTP_ALLOW_PRIVATE_IPS=true to permit", ip)
		}
		return ""
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Sprintf("http_request: dns lookup failed: %v", err)
	}
	for _, a := range addrs {
		if isBlockedIP(a.IP) {
			return fmt.Sprintf("http_request: host %s resolves to a private/loopback/link-local address (%s); set HECATE_TASK_HTTP_ALLOW_PRIVATE_IPS=true to permit", host, a.IP)
		}
	}
	return ""
}

func isBlockedIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() ||
		ip.IsMulticast()
}

func buildHTTPRequestStep(spec ExecutionSpec, index int, startedAt time.Time, toolName, method, urlStr string, status, bytesRead int, truncated bool) types.TaskStep {
	return types.TaskStep{
		ID:       spec.NewID("step"),
		TaskID:   spec.Task.ID,
		RunID:    spec.Run.ID,
		Index:    index,
		Kind:     "tool",
		Title:    fmt.Sprintf("%s %s", method, urlStr),
		Status:   "completed",
		Phase:    "execution",
		Result:   telemetry.ResultSuccess,
		ToolName: toolName,
		Input: map[string]any{
			"method": method,
			"url":    urlStr,
		},
		OutputSummary: map[string]any{
			"status":     status,
			"bytes_read": bytesRead,
			"truncated":  truncated,
		},
		StartedAt:  startedAt,
		FinishedAt: time.Now().UTC(),
		RequestID:  spec.RequestID,
		TraceID:    spec.TraceID,
	}
}

// buildApprovalForTurn constructs the approval record covering one
// or more gated tool calls in a turn. The reason text lists the tool
// names so the operator UI can render a clear "approve agent's use of
// shell_exec, git_exec" prompt without parsing the conversation.
func buildApprovalForTurn(spec ExecutionSpec, turn int, gatedNames []string, when time.Time) types.TaskApproval {
	return types.TaskApproval{
		ID:        spec.NewID("approval"),
		TaskID:    spec.Task.ID,
		RunID:     spec.Run.ID,
		Kind:      "agent_loop_tool_call",
		Status:    "pending",
		Reason:    fmt.Sprintf("Agent requested tools that require approval: %s", strings.Join(gatedNames, ", ")),
		CreatedAt: when,
		RequestID: spec.RequestID,
		TraceID:   spec.TraceID,
	}
}

// buildAwaitingApprovalStep is the timeline step the run UI shows
// while paused. Carries the approval id so the operator UI can link
// the step to the approval action.
func buildAwaitingApprovalStep(spec ExecutionSpec, index, turn int, when time.Time, approval types.TaskApproval) types.TaskStep {
	return types.TaskStep{
		ID:         spec.NewID("step"),
		TaskID:     spec.Task.ID,
		RunID:      spec.Run.ID,
		Index:      index,
		Kind:       "approval",
		Title:      fmt.Sprintf("Awaiting approval — turn %d", turn),
		Status:     "awaiting_approval",
		Phase:      "approval",
		Result:     telemetry.ResultSuccess,
		ToolName:   "builtin.agent_loop_approval",
		ApprovalID: approval.ID,
		Input: map[string]any{
			"turn":   turn,
			"reason": approval.Reason,
		},
		StartedAt:  when,
		FinishedAt: when,
		RequestID:  spec.RequestID,
		TraceID:    spec.TraceID,
	}
}

// buildResumeThinkingStep marks the timeline entry for a resumed turn
// (where we skip the LLM call because the assistant message was
// produced by the previous run). Lets the operator see in the run
// history that the agent didn't re-think — it just dispatched the
// approved calls.
func buildResumeThinkingStep(spec ExecutionSpec, index, turn int, when time.Time, msg types.Message) types.TaskStep {
	toolNames := make([]string, 0, len(msg.ToolCalls))
	for _, tc := range msg.ToolCalls {
		toolNames = append(toolNames, tc.Function.Name)
	}
	return types.TaskStep{
		ID:       spec.NewID("step"),
		TaskID:   spec.Task.ID,
		RunID:    spec.Run.ID,
		Index:    index,
		Kind:     "model",
		Title:    fmt.Sprintf("Agent turn %d (resumed after approval)", turn),
		Status:   "completed",
		Phase:    "thinking",
		Result:   telemetry.ResultSuccess,
		ToolName: "builtin.agent_loop_resume",
		Input: map[string]any{
			"turn":           turn,
			"resumed":        true,
			"tool_calls":     toolNames,
			"approved_tools": toolNames,
		},
		StartedAt:  when,
		FinishedAt: when,
		RequestID:  spec.RequestID,
		TraceID:    spec.TraceID,
	}
}

// resultFromStatus maps an executor's status string ("completed",
// "failed", etc.) to the telemetry result vocabulary
// ("success" / "error"). The telemetry package itself only knows
// success / denied / error, so we collapse the executor's richer
// status set into those buckets for the agent-loop step output.
func resultFromStatus(status string) string {
	switch status {
	case "completed":
		return telemetry.ResultSuccess
	case "failed", "cancelled":
		return telemetry.ResultError
	}
	return telemetry.ResultError
}

func finishReason(resp *types.ChatResponse) string {
	if resp == nil || len(resp.Choices) == 0 {
		return ""
	}
	return resp.Choices[0].FinishReason
}
