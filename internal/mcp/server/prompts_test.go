package server

import (
	"context"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/mcp"
)

func TestDefaultPrompts_List(t *testing.T) {
	server := NewServer("t", "0")
	RegisterDefaultPrompts(server)

	prompts := server.prompts.list()
	for _, want := range []string{"create_agent_task", "investigate_task", "investigate_trace", "operator_briefing"} {
		if !hasPrompt(prompts, want) {
			t.Fatalf("prompt %q missing from %+v", want, prompts)
		}
	}
}

func TestDefaultPrompt_CreateAgentTask(t *testing.T) {
	server := NewServer("t", "0")
	RegisterDefaultPrompts(server)

	prompt := server.prompts.byName["create_agent_task"]
	result, err := prompt.handler(context.Background(), map[string]string{
		"prompt":            "fix the failing tests",
		"working_directory": "/tmp/workspace",
	})
	if err != nil {
		t.Fatalf("create_agent_task prompt: %v", err)
	}
	text := result.Messages[0].Content.Text
	for _, want := range []string{"create_task", "fix the failing tests", "workspace_mode `in_place`", "/tmp/workspace"} {
		if !strings.Contains(text, want) {
			t.Fatalf("prompt text missing %q: %s", want, text)
		}
	}
	if !strings.Contains(text, "no Run has started yet") || strings.Contains(text, "latest run id") {
		t.Fatalf("prompt does not preserve the unstarted task contract: %s", text)
	}
}

func TestDefaultPrompt_RequiresArguments(t *testing.T) {
	server := NewServer("t", "0")
	RegisterDefaultPrompts(server)

	_, err := server.prompts.byName["investigate_task"].handler(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "task_id is required") {
		t.Fatalf("want task_id required error, got: %v", err)
	}
}

func hasPrompt(prompts []mcp.Prompt, name string) bool {
	for _, prompt := range prompts {
		if prompt.Name == name {
			return true
		}
	}
	return false
}
