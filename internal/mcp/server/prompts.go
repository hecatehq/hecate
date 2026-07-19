package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/hecatehq/hecate/internal/mcp"
)

// RegisterDefaultPrompts exposes user-invoked workflow templates for
// MCP clients that render server prompts as slash commands.
func RegisterDefaultPrompts(s *Server) {
	s.RegisterPrompt(mcp.Prompt{
		Name:        "create_agent_task",
		Title:       "Create Hecate agent task",
		Description: "Turn a request into a queued Hecate agent_loop task through the create_task tool.",
		Arguments: []mcp.PromptArgument{
			{Name: "prompt", Title: "Prompt", Description: "The work the Hecate agent_loop should perform.", Required: true},
			{Name: "working_directory", Title: "Working directory", Description: "Optional absolute workspace path for an in-place task."},
		},
	}, createAgentTaskPrompt)

	s.RegisterPrompt(mcp.Prompt{
		Name:        "investigate_task",
		Title:       "Investigate Hecate task",
		Description: "Inspect one Hecate task, summarize its state, and call out pending approvals or failure clues.",
		Arguments: []mcp.PromptArgument{
			{Name: "task_id", Title: "Task ID", Description: "The Hecate task id to inspect.", Required: true},
		},
	}, investigateTaskPrompt)

	s.RegisterPrompt(mcp.Prompt{
		Name:        "investigate_trace",
		Title:       "Investigate Hecate trace",
		Description: "Inspect one Hecate trace and explain routing, latency, status, and span-level clues.",
		Arguments: []mcp.PromptArgument{
			{Name: "request_id", Title: "Request ID", Description: "The Hecate request id to inspect.", Required: true},
		},
	}, investigateTracePrompt)

	s.RegisterPrompt(mcp.Prompt{
		Name:        "operator_briefing",
		Title:       "Hecate operator briefing",
		Description: "Summarize recent tasks and traffic into a short operator handoff.",
	}, operatorBriefingPrompt)
}

func createAgentTaskPrompt(_ context.Context, args map[string]string) (mcp.GetPromptResult, error) {
	prompt, err := requiredPromptArg(args, "prompt")
	if err != nil {
		return mcp.GetPromptResult{}, err
	}
	workingDirectory := strings.TrimSpace(args["working_directory"])
	var b strings.Builder
	b.WriteString("Create a Hecate agent_loop task for this request using the `create_task` MCP tool. ")
	b.WriteString("After creating it, report the task id and latest run id, then suggest checking status with `get_task_status`.\n\n")
	if workingDirectory != "" {
		fmt.Fprintf(&b, "Use workspace_mode `in_place` and working_directory `%s`.\n\n", workingDirectory)
	}
	fmt.Fprintf(&b, "Task prompt:\n%s", prompt)
	return textPrompt("Create a Hecate agent task", b.String()), nil
}

func investigateTaskPrompt(_ context.Context, args map[string]string) (mcp.GetPromptResult, error) {
	taskID, err := requiredPromptArg(args, "task_id")
	if err != nil {
		return mcp.GetPromptResult{}, err
	}
	text := fmt.Sprintf("Inspect Hecate Task `%s`. Read `hecate://tasks/%s` as context if resources are available, then call `get_task_status` if you need live status. Summarize the current state, latest Run and its Step count, and any pending approval or failure clue. Keep the answer operational and concise.", taskID, taskID)
	return textPrompt("Investigate a Hecate task", text), nil
}

func investigateTracePrompt(_ context.Context, args map[string]string) (mcp.GetPromptResult, error) {
	requestID, err := requiredPromptArg(args, "request_id")
	if err != nil {
		return mcp.GetPromptResult{}, err
	}
	text := fmt.Sprintf("Inspect Hecate trace `%s`. Read `hecate://traces/%s` as context if resources are available, then call `search_traces` with request_id `%s` if you need span details. Explain the route decision, final provider/model, status, latency clues, and any span/event that looks relevant.", requestID, requestID, requestID)
	return textPrompt("Investigate a Hecate trace", text), nil
}

func operatorBriefingPrompt(context.Context, map[string]string) (mcp.GetPromptResult, error) {
	text := "Prepare a short Hecate operator briefing. Use `hecate://tasks/recent` and `hecate://traces/recent` as context if resources are available; otherwise call `list_tasks` and `summarize_recent_traffic`. Cover: running or awaiting-approval tasks, recent failures, notable provider/latency patterns, and one suggested next action."
	return textPrompt("Prepare a Hecate operator briefing", text), nil
}

func requiredPromptArg(args map[string]string, name string) (string, error) {
	value := strings.TrimSpace(args[name])
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

func textPrompt(description, text string) mcp.GetPromptResult {
	return mcp.GetPromptResult{
		Description: description,
		Messages: []mcp.PromptMessage{{
			Role:    "user",
			Content: mcp.ContentBlock{Type: "text", Text: text},
		}},
	}
}
