package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/hecate/agent-runtime/internal/mcp"
)

// RegisterDefaultTools wires the default tool set onto the supplied
// server. Every tool here dispatches through the GatewayClient — the MCP
// server is a translator, not a re-implementation of Hecate's core.
//
// Tool design conventions:
//   - inputSchema is JSON Schema 2020-12 (what MCP clients expect).
//     Properties are typed and described; required fields are marked.
//   - Tool output is always plain text (one block). Rich content
//     (markdown tables, JSON dumps) is rendered as text the client
//     formats. We may switch to structured `resource` blocks once
//     clients render them better.
//   - Errors that originate at the upstream HTTP layer become the
//     handler's error return → CallToolResult with isError=true.
//   - Read-only tools set ReadOnlyHint so MCP clients can auto-
//     approve. Write tools set DestructiveHint when the action is
//     irreversible (resolve_approval, cancel_run); IdempotentHint
//     when retries are safe (cancel_run only — cancelling an
//     already-cancelled run is a no-op).
func RegisterDefaultTools(s *Server, client *GatewayClient) {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: mcp.BoolPtr(true)}
	destructive := &mcp.ToolAnnotations{DestructiveHint: mcp.BoolPtr(true)}
	destructiveIdempotent := &mcp.ToolAnnotations{
		DestructiveHint: mcp.BoolPtr(true),
		IdempotentHint:  mcp.BoolPtr(true),
	}

	s.RegisterTool(mcp.Tool{
		Name:        "list_tasks",
		Title:       "List recent tasks",
		Description: "List recent agent tasks tracked by the Hecate gateway. Returns each task's id, title, status, and execution kind.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"limit": {"type": "integer", "minimum": 1, "maximum": 100, "default": 30, "description": "Maximum number of tasks to return."}
			}
		}`),
		Annotations: readOnly,
	}, listTasksHandler(client))

	s.RegisterTool(mcp.Tool{
		Name:        "get_task_status",
		Title:       "Get task status",
		Description: "Get the current status of a specific Hecate task by id, including its latest run and step count.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"task_id": {"type": "string", "description": "The task id (UUID-shaped)."}
			},
			"required": ["task_id"]
		}`),
		Annotations: readOnly,
	}, getTaskStatusHandler(client))

	s.RegisterTool(mcp.Tool{
		Name:        "list_chat_sessions",
		Title:       "List chat sessions",
		Description: "List recent chat sessions on the Hecate gateway. Returns each session's id, title, turn count, and last-updated time.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"limit": {"type": "integer", "minimum": 1, "maximum": 100, "default": 20, "description": "Maximum number of sessions to return."},
				"tenant": {"type": "string", "description": "Filter to a single tenant id. Empty = all tenants the caller can see."}
			}
		}`),
		Annotations: readOnly,
	}, listChatSessionsHandler(client))

	s.RegisterTool(mcp.Tool{
		Name:        "summarize_recent_traffic",
		Title:       "Summarize recent traffic",
		Description: "Summarize recent gateway request activity: total count, by-provider breakdown, error rate, and average latency.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"limit": {"type": "integer", "minimum": 1, "maximum": 500, "default": 100, "description": "Number of recent traces to inspect."}
			}
		}`),
		Annotations: readOnly,
	}, summarizeRecentTrafficHandler(client))

	// ─── write tools ─────────────────────────────────────────────────

	s.RegisterTool(mcp.Tool{
		Name:  "create_task",
		Title: "Create an agent task",
		Description: "Queue a new agent_loop task on the Hecate gateway. " +
			"The task runs an LLM-driven loop with built-in tools (shell_exec, " +
			"git_exec, file_write, file_edit, read_file, list_dir, http_request). " +
			"Returns the new task id; use get_task_status to follow progress. " +
			"For non-agent_loop kinds (raw shell / git / file), use the gateway HTTP API directly.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"prompt": {"type": "string", "minLength": 1, "description": "The user prompt the agent loop runs against. Required."},
				"title": {"type": "string", "description": "Short human-readable title for the task list. Optional."},
				"working_directory": {"type": "string", "description": "Absolute path to the workspace. Required when workspace_mode=in_place; otherwise the gateway clones from this path."},
				"workspace_mode": {"type": "string", "enum": ["", "persistent", "ephemeral", "in_place"], "description": "Workspace strategy. Empty/persistent/ephemeral all clone; in_place runs directly in working_directory."},
				"system_prompt": {"type": "string", "description": "Per-task system-prompt layer (narrowest of the four). Optional."},
				"requested_model": {"type": "string", "description": "Pin a specific model. Empty = use the gateway default."},
				"requested_provider": {"type": "string", "description": "Pin a specific provider. Empty = let the router decide."},
				"budget_micros_usd": {"type": "integer", "minimum": 0, "description": "Per-task LLM cost ceiling in micro-USD. 0 = no ceiling."}
			},
			"required": ["prompt"]
		}`),
		// Not destructive — creating a new task adds state but
		// doesn't destroy any. Not idempotent — repeated calls
		// queue independent tasks.
	}, createTaskHandler(client))

	s.RegisterTool(mcp.Tool{
		Name:  "resolve_approval",
		Title: "Resolve a pending approval",
		Description: "Approve or reject a pending approval gate (pre-execution or mid-loop tool call). " +
			"On approve, the run continues from where it paused; on reject, the run terminates failed. " +
			"This is irreversible — the run can't undo a rejection.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"task_id": {"type": "string", "minLength": 1, "description": "The task id."},
				"approval_id": {"type": "string", "minLength": 1, "description": "The pending approval id."},
				"decision": {"type": "string", "enum": ["approve", "reject"], "description": "approve resumes the run; reject terminates it."},
				"note": {"type": "string", "description": "Optional operator note attached to the resolution (audit trail)."}
			},
			"required": ["task_id", "approval_id", "decision"]
		}`),
		Annotations: destructive,
	}, resolveApprovalHandler(client))

	s.RegisterTool(mcp.Tool{
		Name:  "cancel_run",
		Title: "Cancel a task run",
		Description: "Cancel an in-flight task run. Cooperative cancellation: " +
			"the worker stops at the next safe checkpoint. Already-terminal runs " +
			"are a no-op (idempotent). Use list_tasks → get_task_status to find the run id.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"task_id": {"type": "string", "minLength": 1, "description": "The task id owning the run."},
				"run_id": {"type": "string", "minLength": 1, "description": "The run id to cancel."},
				"reason": {"type": "string", "description": "Optional cancellation reason for the audit log."}
			},
			"required": ["task_id", "run_id"]
		}`),
		Annotations: destructiveIdempotent,
	}, cancelRunHandler(client))
}

// ─── list_tasks ─────────────────────────────────────────────────────

type listTasksArgs struct {
	Limit int `json:"limit"`
}

type listTasksResponse struct {
	Data []struct {
		ID            string `json:"id"`
		Title         string `json:"title"`
		Prompt        string `json:"prompt"`
		Status        string `json:"status"`
		ExecutionKind string `json:"execution_kind"`
		StepCount     int    `json:"step_count"`
		LatestRunID   string `json:"latest_run_id"`
		CreatedAt     string `json:"created_at"`
	} `json:"data"`
}

func listTasksHandler(client *GatewayClient) ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (mcp.CallToolResult, error) {
		var args listTasksArgs
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &args)
		}
		if args.Limit <= 0 {
			args.Limit = 30
		}
		q := url.Values{}
		q.Set("limit", fmt.Sprintf("%d", args.Limit))

		var resp listTasksResponse
		if err := client.Get(ctx, "/v1/tasks", q, &resp); err != nil {
			return mcp.CallToolResult{}, err
		}
		if len(resp.Data) == 0 {
			return mcp.CallToolResult{Content: mcp.TextContent("No tasks yet.")}, nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Found %d task(s):\n\n", len(resp.Data))
		for _, t := range resp.Data {
			title := t.Title
			if title == "" {
				title = t.Prompt
			}
			fmt.Fprintf(&b, "- %s [%s] %s — %s (%d steps)",
				shortID(t.ID), t.ExecutionKind, t.Status, title, t.StepCount)
			if t.LatestRunID != "" {
				fmt.Fprintf(&b, " · run %s", shortID(t.LatestRunID))
			}
			b.WriteByte('\n')
		}
		return mcp.CallToolResult{Content: mcp.TextContent(b.String())}, nil
	}
}

// ─── get_task_status ────────────────────────────────────────────────

type getTaskStatusArgs struct {
	TaskID string `json:"task_id"`
}

type getTaskStatusResponse struct {
	Data struct {
		ID            string `json:"id"`
		Title         string `json:"title"`
		Prompt        string `json:"prompt"`
		Status        string `json:"status"`
		ExecutionKind string `json:"execution_kind"`
		ShellCommand  string `json:"shell_command,omitempty"`
		GitCommand    string `json:"git_command,omitempty"`
		FilePath      string `json:"file_path,omitempty"`
		StepCount     int    `json:"step_count"`
		LatestRunID   string `json:"latest_run_id"`
		CreatedAt     string `json:"created_at"`
		UpdatedAt     string `json:"updated_at"`
	} `json:"data"`
}

func getTaskStatusHandler(client *GatewayClient) ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (mcp.CallToolResult, error) {
		var args getTaskStatusArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return mcp.CallToolResult{}, fmt.Errorf("invalid arguments: %w", err)
		}
		if strings.TrimSpace(args.TaskID) == "" {
			return mcp.CallToolResult{}, fmt.Errorf("task_id is required")
		}
		var resp getTaskStatusResponse
		if err := client.Get(ctx, "/v1/tasks/"+url.PathEscape(args.TaskID), nil, &resp); err != nil {
			return mcp.CallToolResult{}, err
		}
		t := resp.Data
		var b strings.Builder
		fmt.Fprintf(&b, "Task %s\n", t.ID)
		if t.Title != "" {
			fmt.Fprintf(&b, "Title: %s\n", t.Title)
		}
		fmt.Fprintf(&b, "Status: %s\n", t.Status)
		fmt.Fprintf(&b, "Kind: %s\n", t.ExecutionKind)
		switch t.ExecutionKind {
		case "shell":
			if t.ShellCommand != "" {
				fmt.Fprintf(&b, "Command: %s\n", t.ShellCommand)
			}
		case "git":
			if t.GitCommand != "" {
				fmt.Fprintf(&b, "Command: git %s\n", t.GitCommand)
			}
		case "file":
			if t.FilePath != "" {
				fmt.Fprintf(&b, "File: %s\n", t.FilePath)
			}
		}
		fmt.Fprintf(&b, "Steps: %d\n", t.StepCount)
		if t.LatestRunID != "" {
			fmt.Fprintf(&b, "Latest run: %s\n", t.LatestRunID)
		}
		if t.CreatedAt != "" {
			fmt.Fprintf(&b, "Created: %s\n", t.CreatedAt)
		}
		if t.UpdatedAt != "" {
			fmt.Fprintf(&b, "Updated: %s\n", t.UpdatedAt)
		}
		return mcp.CallToolResult{Content: mcp.TextContent(b.String())}, nil
	}
}

// ─── list_chat_sessions ──────────────────────────────────────────────

type listChatSessionsArgs struct {
	Limit  int    `json:"limit"`
	Tenant string `json:"tenant"`
}

type listChatSessionsResponse struct {
	Data []struct {
		ID                string `json:"id"`
		Title             string `json:"title"`
		Tenant            string `json:"tenant"`
		MessageCount      int    `json:"message_count"`
		ProviderCallCount int    `json:"provider_call_count"`
		UpdatedAt         string `json:"updated_at"`
	} `json:"data"`
}

func listChatSessionsHandler(client *GatewayClient) ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (mcp.CallToolResult, error) {
		var args listChatSessionsArgs
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &args)
		}
		if args.Limit <= 0 {
			args.Limit = 20
		}
		q := url.Values{}
		q.Set("limit", fmt.Sprintf("%d", args.Limit))
		q.Set("tenant", args.Tenant)

		var resp listChatSessionsResponse
		if err := client.Get(ctx, "/v1/chat/sessions", q, &resp); err != nil {
			return mcp.CallToolResult{}, err
		}
		if len(resp.Data) == 0 {
			return mcp.CallToolResult{Content: mcp.TextContent("No chat sessions yet.")}, nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Found %d chat session(s):\n\n", len(resp.Data))
		for _, sess := range resp.Data {
			title := sess.Title
			if title == "" {
				title = "(untitled)"
			}
			fmt.Fprintf(&b, "- %s · %s (%d messages, %d calls)", shortID(sess.ID), title, sess.MessageCount, sess.ProviderCallCount)
			if sess.Tenant != "" {
				fmt.Fprintf(&b, " · tenant=%s", sess.Tenant)
			}
			if sess.UpdatedAt != "" {
				fmt.Fprintf(&b, " · updated %s", sess.UpdatedAt)
			}
			b.WriteByte('\n')
		}
		return mcp.CallToolResult{Content: mcp.TextContent(b.String())}, nil
	}
}

// ─── summarize_recent_traffic ────────────────────────────────────────

type summarizeArgs struct {
	Limit int `json:"limit"`
}

type traceListResponse struct {
	Data []struct {
		RequestID  string  `json:"request_id"`
		StartedAt  string  `json:"started_at"`
		FinishedAt string  `json:"finished_at,omitempty"`
		DurationMS int64   `json:"duration_ms,omitempty"`
		Provider   string  `json:"provider,omitempty"`
		Model      string  `json:"model,omitempty"`
		StatusCode string  `json:"status_code,omitempty"`
		StatusErr  bool    `json:"-"`
		Tokens     int64   `json:"total_tokens,omitempty"`
		CostUSD    float64 `json:"cost_usd,omitempty"`
	} `json:"data"`
}

func summarizeRecentTrafficHandler(client *GatewayClient) ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (mcp.CallToolResult, error) {
		var args summarizeArgs
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &args)
		}
		if args.Limit <= 0 {
			args.Limit = 100
		}
		q := url.Values{}
		q.Set("limit", fmt.Sprintf("%d", args.Limit))

		var resp traceListResponse
		if err := client.Get(ctx, "/v1/traces", q, &resp); err != nil {
			return mcp.CallToolResult{}, err
		}
		if len(resp.Data) == 0 {
			return mcp.CallToolResult{Content: mcp.TextContent("No recent traffic.")}, nil
		}

		// Aggregate by provider; track error count and latency.
		type bucket struct {
			count   int
			errors  int
			totalMS int64
			tokens  int64
			cost    float64
		}
		byProvider := map[string]*bucket{}
		var totalCount, totalErrors int
		var totalLatency int64
		for _, t := range resp.Data {
			provider := t.Provider
			if provider == "" {
				provider = "unknown"
			}
			b, ok := byProvider[provider]
			if !ok {
				b = &bucket{}
				byProvider[provider] = b
			}
			b.count++
			b.totalMS += t.DurationMS
			b.tokens += t.Tokens
			b.cost += t.CostUSD
			isError := t.StatusCode == "error" || strings.HasPrefix(t.StatusCode, "5") || strings.HasPrefix(t.StatusCode, "4")
			if isError {
				b.errors++
				totalErrors++
			}
			totalCount++
			totalLatency += t.DurationMS
		}

		var b strings.Builder
		fmt.Fprintf(&b, "Recent traffic (last %d requests):\n", totalCount)
		fmt.Fprintf(&b, "  Total errors: %d (%.1f%%)\n", totalErrors, percent(totalErrors, totalCount))
		if totalCount > 0 {
			fmt.Fprintf(&b, "  Avg latency: %dms\n", totalLatency/int64(totalCount))
		}
		b.WriteString("\nBy provider:\n")
		for name, agg := range byProvider {
			avg := int64(0)
			if agg.count > 0 {
				avg = agg.totalMS / int64(agg.count)
			}
			fmt.Fprintf(&b, "  - %s: %d req, %d errors (%.1f%%), avg %dms",
				name, agg.count, agg.errors, percent(agg.errors, agg.count), avg)
			if agg.tokens > 0 {
				fmt.Fprintf(&b, ", %d tokens", agg.tokens)
			}
			if agg.cost > 0 {
				fmt.Fprintf(&b, ", $%.4f", agg.cost)
			}
			b.WriteByte('\n')
		}
		return mcp.CallToolResult{Content: mcp.TextContent(b.String())}, nil
	}
}

// ─── create_task ─────────────────────────────────────────────────────

type createTaskArgs struct {
	Prompt            string `json:"prompt"`
	Title             string `json:"title"`
	WorkingDirectory  string `json:"working_directory"`
	WorkspaceMode     string `json:"workspace_mode"`
	SystemPrompt      string `json:"system_prompt"`
	RequestedModel    string `json:"requested_model"`
	RequestedProvider string `json:"requested_provider"`
	BudgetMicrosUSD   int64  `json:"budget_micros_usd"`
}

// createTaskWireRequest mirrors the gateway's CreateTaskRequest shape
// for the agent_loop subset. Fields outside this subset (sandbox*,
// shell_command, git_command, file_*) are reachable only via the
// HTTP API; the MCP tool deliberately stays narrow to avoid
// foot-guns from inside an editor.
type createTaskWireRequest struct {
	Prompt            string `json:"prompt"`
	Title             string `json:"title,omitempty"`
	ExecutionKind     string `json:"execution_kind"`
	WorkingDirectory  string `json:"working_directory,omitempty"`
	WorkspaceMode     string `json:"workspace_mode,omitempty"`
	SystemPrompt      string `json:"system_prompt,omitempty"`
	RequestedModel    string `json:"requested_model,omitempty"`
	RequestedProvider string `json:"requested_provider,omitempty"`
	BudgetMicrosUSD   int64  `json:"budget_micros_usd,omitempty"`
}

type createTaskWireResponse struct {
	Data struct {
		ID            string `json:"id"`
		Status        string `json:"status"`
		ExecutionKind string `json:"execution_kind"`
		LatestRunID   string `json:"latest_run_id"`
	} `json:"data"`
}

func createTaskHandler(client *GatewayClient) ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (mcp.CallToolResult, error) {
		var args createTaskArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return mcp.CallToolResult{}, fmt.Errorf("invalid arguments: %w", err)
		}
		if strings.TrimSpace(args.Prompt) == "" {
			return mcp.CallToolResult{}, fmt.Errorf("prompt is required")
		}
		// in_place workspace requires an absolute working_directory.
		// Validate at the MCP boundary so the caller gets an actionable
		// error instead of a 400 from the gateway with less context.
		if args.WorkspaceMode == "in_place" && strings.TrimSpace(args.WorkingDirectory) == "" {
			return mcp.CallToolResult{}, fmt.Errorf("working_directory is required when workspace_mode=in_place")
		}
		body := createTaskWireRequest{
			Prompt:            args.Prompt,
			Title:             args.Title,
			ExecutionKind:     "agent_loop",
			WorkingDirectory:  args.WorkingDirectory,
			WorkspaceMode:     args.WorkspaceMode,
			SystemPrompt:      args.SystemPrompt,
			RequestedModel:    args.RequestedModel,
			RequestedProvider: args.RequestedProvider,
			BudgetMicrosUSD:   args.BudgetMicrosUSD,
		}
		var resp createTaskWireResponse
		if err := client.Post(ctx, "/v1/tasks", body, &resp); err != nil {
			return mcp.CallToolResult{}, err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Created task %s (%s) — status: %s",
			resp.Data.ID, resp.Data.ExecutionKind, resp.Data.Status)
		if resp.Data.LatestRunID != "" {
			fmt.Fprintf(&b, "; first run: %s", resp.Data.LatestRunID)
		}
		b.WriteByte('\n')
		fmt.Fprintf(&b, "\nUse get_task_status with task_id=%s to follow progress.", resp.Data.ID)
		return mcp.CallToolResult{Content: mcp.TextContent(b.String())}, nil
	}
}

// ─── resolve_approval ────────────────────────────────────────────────

type resolveApprovalArgs struct {
	TaskID     string `json:"task_id"`
	ApprovalID string `json:"approval_id"`
	Decision   string `json:"decision"`
	Note       string `json:"note"`
}

type resolveApprovalWireRequest struct {
	Decision string `json:"decision"`
	Note     string `json:"note,omitempty"`
}

type resolveApprovalWireResponse struct {
	Data struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Kind   string `json:"kind"`
	} `json:"data"`
}

func resolveApprovalHandler(client *GatewayClient) ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (mcp.CallToolResult, error) {
		var args resolveApprovalArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return mcp.CallToolResult{}, fmt.Errorf("invalid arguments: %w", err)
		}
		if strings.TrimSpace(args.TaskID) == "" {
			return mcp.CallToolResult{}, fmt.Errorf("task_id is required")
		}
		if strings.TrimSpace(args.ApprovalID) == "" {
			return mcp.CallToolResult{}, fmt.Errorf("approval_id is required")
		}
		switch args.Decision {
		case "approve", "reject":
		default:
			return mcp.CallToolResult{}, fmt.Errorf("decision must be \"approve\" or \"reject\" (got %q)", args.Decision)
		}
		body := resolveApprovalWireRequest{Decision: args.Decision, Note: args.Note}
		path := fmt.Sprintf("/v1/tasks/%s/approvals/%s/resolve",
			url.PathEscape(args.TaskID), url.PathEscape(args.ApprovalID))
		var resp resolveApprovalWireResponse
		if err := client.Post(ctx, path, body, &resp); err != nil {
			return mcp.CallToolResult{}, err
		}
		return mcp.CallToolResult{Content: mcp.TextContent(fmt.Sprintf(
			"Approval %s (%s) is now %s.",
			resp.Data.ID, resp.Data.Kind, resp.Data.Status,
		))}, nil
	}
}

// ─── cancel_run ──────────────────────────────────────────────────────

type cancelRunArgs struct {
	TaskID string `json:"task_id"`
	RunID  string `json:"run_id"`
	Reason string `json:"reason"`
}

type cancelRunWireResponse struct {
	Data struct {
		ID         string `json:"id"`
		TaskID     string `json:"task_id"`
		Status     string `json:"status"`
		FinishedAt string `json:"finished_at,omitempty"`
		LastError  string `json:"last_error,omitempty"`
	} `json:"data"`
}

func cancelRunHandler(client *GatewayClient) ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (mcp.CallToolResult, error) {
		var args cancelRunArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return mcp.CallToolResult{}, fmt.Errorf("invalid arguments: %w", err)
		}
		if strings.TrimSpace(args.TaskID) == "" {
			return mcp.CallToolResult{}, fmt.Errorf("task_id is required")
		}
		if strings.TrimSpace(args.RunID) == "" {
			return mcp.CallToolResult{}, fmt.Errorf("run_id is required")
		}
		path := fmt.Sprintf("/v1/tasks/%s/runs/%s/cancel",
			url.PathEscape(args.TaskID), url.PathEscape(args.RunID))
		body := struct {
			Reason string `json:"reason,omitempty"`
		}{Reason: strings.TrimSpace(args.Reason)}
		var resp cancelRunWireResponse
		if err := client.Post(ctx, path, body, &resp); err != nil {
			return mcp.CallToolResult{}, err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Run %s on task %s — status: %s",
			resp.Data.ID, resp.Data.TaskID, resp.Data.Status)
		if resp.Data.FinishedAt != "" {
			fmt.Fprintf(&b, " · finished at %s", resp.Data.FinishedAt)
		}
		if resp.Data.LastError != "" {
			fmt.Fprintf(&b, "\nLast error: %s", resp.Data.LastError)
		}
		return mcp.CallToolResult{Content: mcp.TextContent(b.String())}, nil
	}
}

// ─── helpers ────────────────────────────────────────────────────────

// shortID truncates a UUID-ish identifier to its first 8 chars for
// readability. Full ID still appears in the wrapping detail tools.
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func percent(part, whole int) float64 {
	if whole == 0 {
		return 0
	}
	return float64(part) * 100 / float64(whole)
}
