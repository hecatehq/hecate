package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	mcpclient "github.com/hecatehq/hecate/internal/mcp/client"
	"github.com/hecatehq/hecate/internal/runtimeevents"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/internal/websearch"
	"github.com/hecatehq/hecate/pkg/types"
)

type agentLoopToolDispatcher struct {
	shell                     Executor
	file                      Executor
	git                       Executor
	httpPolicy                HTTPRequestPolicy
	httpClient                *http.Client
	webSearch                 websearch.Client
	projectAssistantDraftTool ProjectAssistantDraftTool
	metrics                   *telemetry.OrchestratorMetrics
}

type agentLoopToolDispatchResult struct {
	Text      string
	Step      *types.TaskStep
	Artifacts []types.TaskArtifact
	ToolError bool
}

const mcpAppHTMLMaxBytes = 1 << 20

func (d *agentLoopToolDispatcher) SetMetrics(m *telemetry.OrchestratorMetrics) {
	d.metrics = m
}

func (d *agentLoopToolDispatcher) Dispatch(ctx context.Context, spec ExecutionSpec, call types.ToolCall, stepIndex int, mcpHost AgentMCPHost, terminals *agentLoopTerminals) (agentLoopToolDispatchResult, error) {
	startedAt := time.Now().UTC()
	if agentPresetBlocksNativeNetwork(spec.Task, call.Function.Name) {
		return blockedNativeToolCall(
			spec,
			call,
			stepIndex,
			startedAt,
			"sandbox_network",
			"network access is disabled by the resolved agent preset",
			"choose a non-network path or ask the operator to use a network-enabled preset",
		), nil
	}
	if agentReadOnlyBlocksTool(spec.Task, call.Function.Name) {
		return blockedNativeReadOnlyToolCall(spec, call, stepIndex, startedAt), nil
	}

	// External MCP tools surface under names of the form
	// `mcp__<server>__<tool>`. Route them to the host before the
	// built-in switch so a server can't accidentally collide with a
	// built-in name.
	if mcpHost != nil && isMCPToolName(call.Function.Name) {
		return d.dispatchMCPToolCall(ctx, spec, call, stepIndex, startedAt, mcpHost)
	}

	// Decode the tool arguments. Each tool gets its own typed shape;
	// see agentToolDefinitions() for the schemas. A malformed args
	// blob is reported back to the LLM as a tool failure rather than
	// crashing the run — gives the model a chance to retry.
	switch call.Function.Name {
	case "shell_exec":
		var args shellExecArgs
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return agentLoopToolDispatchResult{Text: fmt.Sprintf("invalid arguments for shell_exec: %v", err)}, nil
		}
		taskCopy := spec.Task
		taskCopy.ExecutionKind = "shell"
		taskCopy.ShellCommand = args.Command
		if args.WorkingDirectory != "" {
			taskCopy.WorkingDirectory = args.WorkingDirectory
		}
		return d.runSubExecutor(ctx, spec, d.shell, taskCopy, stepIndex, startedAt, call.ID, call.Function.Name)

	case AgentToolTerminalOpen:
		var args terminalOpenArgs
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return agentLoopToolDispatchResult{Text: fmt.Sprintf("invalid arguments for %s: %v", AgentToolTerminalOpen, err)}, nil
		}
		return dispatchResult(dispatchTerminalOpenTool(ctx, spec, args, stepIndex, startedAt, call.ID, call.Function.Name, terminals))

	case AgentToolTerminalWrite:
		var args terminalWriteArgs
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return agentLoopToolDispatchResult{Text: fmt.Sprintf("invalid arguments for %s: %v", AgentToolTerminalWrite, err)}, nil
		}
		return dispatchResult(dispatchTerminalWriteTool(ctx, spec, args, stepIndex, startedAt, call.Function.Name, terminals))

	case AgentToolTerminalRead:
		var args terminalReadArgs
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return agentLoopToolDispatchResult{Text: fmt.Sprintf("invalid arguments for %s: %v", AgentToolTerminalRead, err)}, nil
		}
		return dispatchResult(dispatchTerminalReadTool(spec, args, stepIndex, startedAt, call.Function.Name, terminals))

	case AgentToolTerminalWait:
		var args terminalWaitArgs
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return agentLoopToolDispatchResult{Text: fmt.Sprintf("invalid arguments for %s: %v", AgentToolTerminalWait, err)}, nil
		}
		return dispatchResult(dispatchTerminalWaitTool(ctx, spec, args, stepIndex, startedAt, call.Function.Name, terminals))

	case AgentToolTerminalKill:
		var args terminalKillArgs
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return agentLoopToolDispatchResult{Text: fmt.Sprintf("invalid arguments for %s: %v", AgentToolTerminalKill, err)}, nil
		}
		return dispatchResult(dispatchTerminalKillTool(ctx, spec, args, stepIndex, startedAt, call.Function.Name, terminals))

	case "git_exec":
		var args gitExecArgs
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return agentLoopToolDispatchResult{Text: fmt.Sprintf("invalid arguments for git_exec: %v", err)}, nil
		}
		taskCopy := spec.Task
		taskCopy.ExecutionKind = "git"
		taskCopy.GitCommand = args.Command
		if args.WorkingDirectory != "" {
			taskCopy.WorkingDirectory = args.WorkingDirectory
		}
		return d.runSubExecutor(ctx, spec, d.git, taskCopy, stepIndex, startedAt, call.ID, call.Function.Name)

	case "file_write":
		var args fileWriteArgs
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return agentLoopToolDispatchResult{Text: fmt.Sprintf("invalid arguments for file_write: %v", err)}, nil
		}
		op := args.Operation
		if op == "" {
			op = "write"
		}
		taskCopy := spec.Task
		taskCopy.ExecutionKind = "file"
		taskCopy.FilePath = args.Path
		taskCopy.FileContent = args.Content
		taskCopy.FileOperation = op
		return d.runSubExecutor(ctx, spec, d.file, taskCopy, stepIndex, startedAt, call.ID, call.Function.Name)

	case "file_edit":
		var args fileEditArgs
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return agentLoopToolDispatchResult{Text: fmt.Sprintf("invalid arguments for file_edit: %v", err)}, nil
		}
		if spec.Task.SandboxReadOnly && !args.Propose {
			return blockedNativeReadOnlyApplyCall(spec, call, stepIndex, startedAt), nil
		}
		taskCopy, before, after, absPath, errMsg := prepareFileEditTask(spec, args)
		if errMsg != "" {
			return agentLoopToolDispatchResult{Text: errMsg}, nil
		}
		if args.Propose {
			return dispatchResult(proposedFileEditToolResult(spec, args, stepIndex, startedAt, call.ID, call.Function.Name, absPath, before, after))
		}
		return d.runSubExecutor(ctx, spec, d.file, taskCopy, stepIndex, startedAt, call.ID, call.Function.Name)

	case "read_file":
		var args readFileArgs
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return agentLoopToolDispatchResult{Text: fmt.Sprintf("invalid arguments for read_file: %v", err)}, nil
		}
		return dispatchResult(readFileTool(spec, args, stepIndex, startedAt, call.Function.Name))

	case "grep":
		var args grepArgs
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return agentLoopToolDispatchResult{Text: fmt.Sprintf("invalid arguments for grep: %v", err)}, nil
		}
		return dispatchResult(grepTool(spec, args, stepIndex, startedAt, call.Function.Name))

	case "glob":
		var args globArgs
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return agentLoopToolDispatchResult{Text: fmt.Sprintf("invalid arguments for glob: %v", err)}, nil
		}
		return dispatchResult(globTool(spec, args, stepIndex, startedAt, call.Function.Name))

	case "artifact_read":
		var args artifactReadArgs
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return agentLoopToolDispatchResult{Text: fmt.Sprintf("invalid arguments for artifact_read: %v", err)}, nil
		}
		return dispatchResult(artifactReadTool(spec, args, stepIndex, startedAt, call.Function.Name))

	case "list_dir":
		var args listDirArgs
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return agentLoopToolDispatchResult{Text: fmt.Sprintf("invalid arguments for list_dir: %v", err)}, nil
		}
		return dispatchResult(listDirTool(spec, args, stepIndex, startedAt, call.Function.Name))

	case "git_status":
		var args gitStatusArgs
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return agentLoopToolDispatchResult{Text: fmt.Sprintf("invalid arguments for git_status: %v", err)}, nil
		}
		return dispatchResult(gitStatusTool(ctx, spec, args, stepIndex, startedAt, call.Function.Name))

	case "git_diff":
		var args gitDiffArgs
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return agentLoopToolDispatchResult{Text: fmt.Sprintf("invalid arguments for git_diff: %v", err)}, nil
		}
		return dispatchResult(gitDiffTool(ctx, spec, args, stepIndex, startedAt, call.Function.Name))

	case "apply_patch":
		var args applyPatchArgs
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return agentLoopToolDispatchResult{Text: fmt.Sprintf("invalid arguments for apply_patch: %v", err)}, nil
		}
		if spec.Task.SandboxReadOnly && !args.Propose {
			return blockedNativeReadOnlyApplyCall(spec, call, stepIndex, startedAt), nil
		}
		return dispatchResult(applyPatchTool(spec, args, stepIndex, startedAt, call.ID, call.Function.Name))

	case AgentToolHTTPRequest:
		var args httpRequestArgs
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return agentLoopToolDispatchResult{Text: fmt.Sprintf("invalid arguments for http_request: %v", err)}, nil
		}
		return d.httpRequestTool(ctx, spec, args, stepIndex, startedAt, call.Function.Name)

	case AgentToolWebSearch:
		var args webSearchArgs
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return agentLoopToolDispatchResult{Text: fmt.Sprintf("invalid arguments for web_search: %v", err)}, nil
		}
		return d.webSearchTool(ctx, spec, args, stepIndex, startedAt, call.Function.Name)

	case AgentToolDraftProjectProposal:
		var args projectAssistantDraftArgs
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return agentLoopToolDispatchResult{Text: fmt.Sprintf("invalid arguments for %s: %v", AgentToolDraftProjectProposal, err)}, nil
		}
		return d.projectAssistantDraftProposalTool(ctx, spec, args, stepIndex, startedAt, call.Function.Name), nil

	default:
		return agentLoopToolDispatchResult{Text: fmt.Sprintf("unknown tool: %s", call.Function.Name)}, nil
	}
}

func blockedNativeToolCall(spec ExecutionSpec, call types.ToolCall, stepIndex int, startedAt time.Time, policy, reason, recovery string) agentLoopToolDispatchResult {
	finishedAt := time.Now().UTC()
	text := fmt.Sprintf("%s: %s; %s", call.Function.Name, reason, recovery)
	step := types.TaskStep{
		ID:         spec.NewID("step"),
		TaskID:     spec.Task.ID,
		RunID:      spec.Run.ID,
		Index:      stepIndex,
		Kind:       "tool",
		Title:      fmt.Sprintf("%s (blocked)", call.Function.Name),
		Status:     "completed",
		Phase:      "policy",
		Result:     telemetry.ResultDenied,
		ToolName:   call.Function.Name,
		Input:      map[string]any{"tool": call.Function.Name},
		ErrorKind:  "sandbox_policy_denied",
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		RequestID:  spec.RequestID,
		TraceID:    spec.TraceID,
		OutputSummary: map[string]any{
			"blocked": true,
			"policy":  policy,
			"reason":  reason,
		},
	}
	if spec.EmitRunEvent != nil {
		spec.EmitRunEvent(runtimeevents.EventPolicyToolBlocked.String(), map[string]any{
			"tool_call_id": call.ID,
			"tool_name":    call.Function.Name,
			"kind":         "builtin",
			"result":       telemetry.MCPCallResultBlocked,
			"policy":       policy,
			"reason":       reason,
		})
	}
	return agentLoopToolDispatchResult{Text: text, Step: &step, ToolError: true}
}

func blockedNativeReadOnlyToolCall(spec ExecutionSpec, call types.ToolCall, stepIndex int, startedAt time.Time) agentLoopToolDispatchResult {
	return blockedNativeToolCall(
		spec,
		call,
		stepIndex,
		startedAt,
		"sandbox_read_only",
		"this tool is unavailable because task workspace writes are disabled",
		"use structured read-only tools or ask the operator to use a write-enabled preset",
	)
}

func blockedNativeReadOnlyApplyCall(spec ExecutionSpec, call types.ToolCall, stepIndex int, startedAt time.Time) agentLoopToolDispatchResult {
	return blockedNativeToolCall(
		spec,
		call,
		stepIndex,
		startedAt,
		"sandbox_read_only",
		"this tool cannot apply changes because task workspace writes are disabled",
		"request a proposal-only edit or ask the operator to use a write-enabled preset",
	)
}

func dispatchResult(text string, step *types.TaskStep, artifacts []types.TaskArtifact, err error) (agentLoopToolDispatchResult, error) {
	return agentLoopToolDispatchResult{Text: text, Step: step, Artifacts: artifacts}, err
}

func isMCPToolName(name string) bool {
	const prefix = "mcp__"
	return len(name) > len(prefix) && name[:len(prefix)] == prefix
}

// dispatchMCPToolCall hands an MCP tool call off to the host and
// builds the agent-loop step that records it. We don't validate the
// arguments shape here — the upstream MCP server owns the schema and
// will reject bad input via CallToolResult.IsError, which the LLM can
// see and retry. Same shape as the other dispatch helpers so the
// caller treats every tool uniformly.
func (d *agentLoopToolDispatcher) dispatchMCPToolCall(ctx context.Context, spec ExecutionSpec, call types.ToolCall, stepIndex int, startedAt time.Time, host AgentMCPHost) (agentLoopToolDispatchResult, error) {
	args := json.RawMessage(call.Function.Arguments)
	if len(args) == 0 {
		// MCP servers typically expect at least `{}` rather than an
		// empty body. Substitute so a model that elides the
		// arguments object doesn't trip an upstream parse error.
		args = json.RawMessage(`{}`)
	}

	// Decompose the namespaced name once — used for both metric
	// attributes (server / tool) and event payloads. Built-in tools
	// don't reach this function, so a parse failure here would be a
	// programming error in the dispatcher; we still tolerate it
	// gracefully by leaving the attributes blank rather than crashing
	// the run.
	server, toolLeaf, _ := mcpclient.SplitNamespacedToolName(call.Function.Name)

	// Block policy: never call upstream. Emit a denied policy step and feed
	// a tool-error message back to the LLM so the model can pick a
	// different path on the next turn. Operators use this to disable
	// risky tool surfaces (e.g. write-side GitHub tools) without
	// editing the upstream server's tool catalog.
	if mcpServerPolicy(call.Function.Name, spec.Task) == types.MCPApprovalBlock {
		finishedAt := time.Now().UTC()
		durationMS := finishedAt.Sub(startedAt).Milliseconds()
		text := fmt.Sprintf("mcp tool %q is blocked by the configured approval policy on this task; pick a different tool", call.Function.Name)
		reason := "blocked by mcp approval policy"
		step := types.TaskStep{
			ID:         spec.NewID("step"),
			TaskID:     spec.Task.ID,
			RunID:      spec.Run.ID,
			Index:      stepIndex,
			Kind:       "tool",
			Title:      fmt.Sprintf("%s (blocked)", call.Function.Name),
			Status:     "completed",
			Phase:      "policy",
			Result:     telemetry.ResultDenied,
			ToolName:   call.Function.Name,
			Input:      mcpToolInputForLog(call.Function.Name, args),
			StartedAt:  startedAt,
			FinishedAt: finishedAt,
			RequestID:  spec.RequestID,
			TraceID:    spec.TraceID,
		}
		step.OutputSummary = map[string]any{
			"is_error":  true,
			"blocked":   true,
			"policy":    "mcp_approval_policy",
			"reason":    reason,
			"text_size": len(text),
		}
		d.recordMCPCallTelemetry(ctx, spec, call.ID, call.Function.Name, server, toolLeaf, telemetry.MCPCallResultBlocked, durationMS, reason)
		return agentLoopToolDispatchResult{Text: text, Step: &step, ToolError: true}, nil
	}

	var detailed *mcpclient.ToolCallResult
	var text string
	var isError bool
	var err error
	if detailHost, ok := host.(AgentMCPDetailedHost); ok {
		detail, detailErr := detailHost.CallDetailed(ctx, call.Function.Name, args)
		text = detail.Text
		isError = detail.IsError
		err = detailErr
		if detailErr == nil {
			detailed = &detail
		}
	} else {
		text, isError, err = host.Call(ctx, call.Function.Name, args)
	}
	finishedAt := time.Now().UTC()
	durationMS := finishedAt.Sub(startedAt).Milliseconds()

	status := "completed"
	resultKind := resultFromStatus(status)
	stepError := ""
	callResult := telemetry.MCPCallResultDispatched
	if err != nil {
		// Protocol-level failure (transport closed, RPC error, unknown
		// tool). Surface as a tool error to the LLM with the diagnostic
		// in the result text — the model can either retry or fall
		// back to a different tool.
		status = "failed"
		resultKind = resultFromStatus(status)
		stepError = err.Error()
		text = fmt.Sprintf("mcp tool %q failed: %v", call.Function.Name, err)
		callResult = telemetry.MCPCallResultFailed
	} else if isError {
		// Tool-level error. The text already carries the upstream
		// reason; mark the step failed so the run timeline shows it
		// in red and the next-turn message ToolError is set.
		status = "failed"
		resultKind = resultFromStatus(status)
		callResult = telemetry.MCPCallResultToolError
	}

	step := types.TaskStep{
		ID:         spec.NewID("step"),
		TaskID:     spec.Task.ID,
		RunID:      spec.Run.ID,
		Index:      stepIndex,
		Kind:       "tool",
		Title:      fmt.Sprintf("%s (%s)", call.Function.Name, status),
		Status:     status,
		Phase:      "execution",
		Result:     resultKind,
		ToolName:   call.Function.Name,
		Input:      mcpToolInputForLog(call.Function.Name, args),
		Error:      stepError,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		RequestID:  spec.RequestID,
		TraceID:    spec.TraceID,
	}
	summary := map[string]any{
		"is_error":  isError || err != nil,
		"text_size": len(text),
	}
	if detailed != nil {
		if appSummary := mcpAppOutputSummary(call.Function.Name, args, *detailed); len(appSummary) > 0 {
			summary["mcp_app"] = appSummary
		}
	}
	step.OutputSummary = summary
	d.recordMCPCallTelemetry(ctx, spec, call.ID, call.Function.Name, server, toolLeaf, callResult, durationMS, stepError)
	return agentLoopToolDispatchResult{Text: text, Step: &step}, nil
}

func mcpAppOutputSummary(toolName string, args json.RawMessage, result mcpclient.ToolCallResult) map[string]any {
	if result.App == nil && result.AppError == "" {
		return nil
	}
	out := map[string]any{
		"tool_name":   toolName,
		"tool_input":  rawJSONSummaryValue(args),
		"tool_result": result.Result,
	}
	if result.Tool.UIResourceURI != "" {
		out["resource_uri"] = result.Tool.UIResourceURI
	}
	if len(result.Tool.Meta) > 0 {
		out["tool_meta"] = rawJSONSummaryValue(result.Tool.Meta)
	}
	if result.AppError != "" {
		out["error"] = result.AppError
	}
	if result.App == nil {
		return out
	}
	if result.App.URI != "" {
		out["resource_uri"] = result.App.URI
	}
	if result.App.MIMEType != "" {
		out["mime_type"] = result.App.MIMEType
	}
	if len(result.App.Meta) > 0 {
		out["resource_meta"] = rawJSONSummaryValue(result.App.Meta)
	}
	if len([]byte(result.App.HTML)) > mcpAppHTMLMaxBytes {
		out["html_truncated"] = true
		out["error"] = fmt.Sprintf("mcp app HTML resource exceeded %d byte capture limit", mcpAppHTMLMaxBytes)
		return out
	}
	out["html"] = result.App.HTML
	return out
}

func rawJSONSummaryValue(raw json.RawMessage) any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	return value
}

func (d *agentLoopToolDispatcher) runSubExecutor(ctx context.Context, spec ExecutionSpec, exec Executor, task types.Task, stepIndex int, startedAt time.Time, toolCallID, toolName string) (agentLoopToolDispatchResult, error) {
	if exec == nil {
		return agentLoopToolDispatchResult{Text: fmt.Sprintf("%s tool is not configured on this gateway", toolName)}, nil
	}
	subSpec := ExecutionSpec{
		Task:       task,
		Run:        spec.Run,
		RequestID:  spec.RequestID,
		TraceID:    spec.TraceID,
		RootSpanID: spec.RootSpanID,
		StartedAt:  startedAt,
		NewID:      spec.NewID,
		// Sub-executor must NOT independently upsert into the store —
		// we batch artifacts/steps at the agent-loop level so the
		// indices stay coherent. Pass nil callbacks; the returned
		// ExecutionResult carries the rows for us to renumber.
		UpsertStep:     nil,
		UpsertArtifact: nil,
		EmitRunEvent:   spec.EmitRunEvent,
		RTKEnabled:     spec.RTKEnabled,
		ToolCallID:     toolCallID,
		ToolName:       toolName,
	}
	subResult, err := exec.Execute(ctx, subSpec)
	if err != nil {
		return agentLoopToolDispatchResult{Text: fmt.Sprintf("%s tool internal error: %v", toolName, err)}, nil
	}
	if subResult == nil {
		return agentLoopToolDispatchResult{Text: fmt.Sprintf("%s tool returned nothing", toolName)}, nil
	}

	// Build a single agent-loop step that summarizes the sub-tool's
	// outcome. We don't replay every sub-step the per-kind executor
	// produced — that would clutter the timeline. Instead, the step's
	// OutputSummary captures the tool's status + last error, and any
	// artifacts (stdout/stderr/files) get linked.
	finishedAt := time.Now().UTC()
	step := types.TaskStep{
		ID:         spec.NewID("step"),
		TaskID:     spec.Task.ID,
		RunID:      spec.Run.ID,
		Index:      stepIndex,
		Kind:       "tool",
		Title:      fmt.Sprintf("%s (%s)", toolName, subResult.Status),
		Status:     subResult.Status,
		Phase:      "execution",
		Result:     resultFromStatus(subResult.Status),
		ToolName:   toolName,
		Input:      toolInputForLog(toolName, task),
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		RequestID:  spec.RequestID,
		TraceID:    spec.TraceID,
	}
	step.OutputSummary = map[string]any{
		"sub_status":     subResult.Status,
		"last_error":     subResult.LastError,
		"sub_step_count": len(subResult.Steps),
		"artifact_count": len(subResult.Artifacts),
	}

	// Re-stamp artifacts with the loop's step ID so the run UI groups
	// them under this turn rather than the sub-executor's step.
	artifacts := make([]types.TaskArtifact, 0, len(subResult.Artifacts))
	for _, art := range subResult.Artifacts {
		art.StepID = step.ID
		artifacts = append(artifacts, art)
	}

	// What the LLM sees on the next turn. We summarize for token
	// efficiency: include status, error if any, and a digest of the
	// stdout/file content. Full artifacts are still in the run for
	// the UI; the LLM gets the relevant signal.
	resultText := summarizeSubResult(subResult)
	return agentLoopToolDispatchResult{Text: resultText, Step: &step, Artifacts: artifacts}, nil
}
