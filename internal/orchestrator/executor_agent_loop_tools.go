package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	mcpclient "github.com/hecate/agent-runtime/internal/mcp/client"
	"github.com/hecate/agent-runtime/internal/telemetry"
	"github.com/hecate/agent-runtime/pkg/types"
)

func (e *AgentLoopExecutor) isGated(toolName string, task types.Task) bool {
	if _, ok := e.gatedTools[toolName]; ok {
		return true
	}
	return mcpServerPolicy(toolName, task) == types.MCPApprovalRequireApproval
}

func mcpServerPolicy(toolName string, task types.Task) string {
	if !isMCPToolName(toolName) {
		return ""
	}
	server, _, ok := mcpclient.SplitNamespacedToolName(toolName)
	if !ok {
		return ""
	}
	for _, cfg := range task.MCPServers {
		if cfg.Name == server {
			return cfg.ApprovalPolicy
		}
	}
	return ""
}

func (e *AgentLoopExecutor) dispatchToolCall(ctx context.Context, spec ExecutionSpec, call types.ToolCall, stepIndex int, mcpHost AgentMCPHost) (string, *types.TaskStep, []types.TaskArtifact, error) {
	startedAt := time.Now().UTC()

	// External MCP tools surface under names of the form
	// `mcp__<server>__<tool>`. Route them to the host before the
	// built-in switch so a server can't accidentally collide with a
	// built-in name.
	if mcpHost != nil && isMCPToolName(call.Function.Name) {
		return e.dispatchMCPToolCall(ctx, spec, call, stepIndex, startedAt, mcpHost)
	}

	// Decode the tool arguments. Each tool gets its own typed shape;
	// see agentToolDefinitions() for the schemas. A malformed args
	// blob is reported back to the LLM as a tool failure rather than
	// crashing the run — gives the model a chance to retry.
	switch call.Function.Name {
	case "shell_exec":
		var args shellExecArgs
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return fmt.Sprintf("invalid arguments for shell_exec: %v", err), nil, nil, nil
		}
		taskCopy := spec.Task
		taskCopy.ExecutionKind = "shell"
		taskCopy.ShellCommand = args.Command
		taskCopy.WorkingDirectory = args.WorkingDirectory
		return e.runSubExecutor(ctx, spec, e.shell, taskCopy, stepIndex, startedAt, call.ID, call.Function.Name)

	case "git_exec":
		var args gitExecArgs
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return fmt.Sprintf("invalid arguments for git_exec: %v", err), nil, nil, nil
		}
		taskCopy := spec.Task
		taskCopy.ExecutionKind = "git"
		taskCopy.GitCommand = args.Command
		taskCopy.WorkingDirectory = args.WorkingDirectory
		return e.runSubExecutor(ctx, spec, e.git, taskCopy, stepIndex, startedAt, call.ID, call.Function.Name)

	case "file_write":
		var args fileWriteArgs
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return fmt.Sprintf("invalid arguments for file_write: %v", err), nil, nil, nil
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
		return e.runSubExecutor(ctx, spec, e.file, taskCopy, stepIndex, startedAt, call.ID, call.Function.Name)

	case "file_edit":
		var args fileEditArgs
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return fmt.Sprintf("invalid arguments for file_edit: %v", err), nil, nil, nil
		}
		taskCopy, before, after, absPath, errMsg := prepareFileEditTask(spec, args)
		if errMsg != "" {
			return errMsg, nil, nil, nil
		}
		if args.Propose {
			return proposedFileEditToolResult(spec, args, stepIndex, startedAt, call.ID, call.Function.Name, absPath, before, after)
		}
		return e.runSubExecutor(ctx, spec, e.file, taskCopy, stepIndex, startedAt, call.ID, call.Function.Name)

	case "read_file":
		var args readFileArgs
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return fmt.Sprintf("invalid arguments for read_file: %v", err), nil, nil, nil
		}
		return readFileTool(spec, args, stepIndex, startedAt, call.Function.Name)

	case "list_dir":
		var args listDirArgs
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return fmt.Sprintf("invalid arguments for list_dir: %v", err), nil, nil, nil
		}
		return listDirTool(spec, args, stepIndex, startedAt, call.Function.Name)

	case "http_request":
		var args httpRequestArgs
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return fmt.Sprintf("invalid arguments for http_request: %v", err), nil, nil, nil
		}
		return e.httpRequestTool(ctx, spec, args, stepIndex, startedAt, call.Function.Name)

	default:
		return fmt.Sprintf("unknown tool: %s", call.Function.Name), nil, nil, nil
	}
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
func (e *AgentLoopExecutor) dispatchMCPToolCall(ctx context.Context, spec ExecutionSpec, call types.ToolCall, stepIndex int, startedAt time.Time, host AgentMCPHost) (string, *types.TaskStep, []types.TaskArtifact, error) {
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

	// Block policy: never call upstream. Emit a failed step and feed
	// a tool-error message back to the LLM so the model can pick a
	// different path on the next turn. Operators use this to disable
	// risky tool surfaces (e.g. write-side GitHub tools) without
	// editing the upstream server's tool catalog.
	if mcpServerPolicy(call.Function.Name, spec.Task) == types.MCPApprovalBlock {
		finishedAt := time.Now().UTC()
		durationMS := finishedAt.Sub(startedAt).Milliseconds()
		text := fmt.Sprintf("mcp tool %q is blocked by the configured approval policy on this task; pick a different tool", call.Function.Name)
		step := types.TaskStep{
			ID:         spec.NewID("step"),
			TaskID:     spec.Task.ID,
			RunID:      spec.Run.ID,
			Index:      stepIndex,
			Kind:       "tool",
			Title:      fmt.Sprintf("%s (blocked)", call.Function.Name),
			Status:     "failed",
			Phase:      "execution",
			Result:     resultFromStatus("failed"),
			ToolName:   call.Function.Name,
			Input:      mcpToolInputForLog(call.Function.Name, args),
			Error:      "blocked by mcp approval policy",
			StartedAt:  startedAt,
			FinishedAt: finishedAt,
			RequestID:  spec.RequestID,
			TraceID:    spec.TraceID,
		}
		step.OutputSummary = map[string]any{
			"is_error":  true,
			"blocked":   true,
			"text_size": len(text),
		}
		e.recordMCPCallTelemetry(ctx, spec, call.ID, call.Function.Name, server, toolLeaf, telemetry.MCPCallResultBlocked, durationMS, step.Error)
		return text, &step, nil, nil
	}

	text, isError, err := host.Call(ctx, call.Function.Name, args)
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
	step.OutputSummary = map[string]any{
		"is_error":  isError || err != nil,
		"text_size": len(text),
	}
	e.recordMCPCallTelemetry(ctx, spec, call.ID, call.Function.Name, server, toolLeaf, callResult, durationMS, stepError)
	return text, &step, nil, nil
}

func (e *AgentLoopExecutor) runSubExecutor(ctx context.Context, spec ExecutionSpec, exec Executor, task types.Task, stepIndex int, startedAt time.Time, toolCallID, toolName string) (string, *types.TaskStep, []types.TaskArtifact, error) {
	if exec == nil {
		return fmt.Sprintf("%s tool is not configured on this gateway", toolName), nil, nil, nil
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
		return fmt.Sprintf("%s tool internal error: %v", toolName, err), nil, nil, nil
	}
	if subResult == nil {
		return fmt.Sprintf("%s tool returned nothing", toolName), nil, nil, nil
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
	return resultText, &step, artifacts, nil
}

func (e *AgentLoopExecutor) gatedToolsInTurn(calls []types.ToolCall, task types.Task) []string {
	seen := make(map[string]struct{}, len(calls))
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		if !e.isGated(c.Function.Name, task) {
			continue
		}
		if _, dup := seen[c.Function.Name]; dup {
			continue
		}
		seen[c.Function.Name] = struct{}{}
		out = append(out, c.Function.Name)
	}
	return out
}
