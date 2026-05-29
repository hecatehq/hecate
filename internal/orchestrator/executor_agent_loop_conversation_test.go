package orchestrator

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/pkg/types"
)

func TestAgentLoopConversation_FreshRunBuildsPreludeAndStableArtifactID(t *testing.T) {
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = "/workspace/run"
	spec.SystemPrompt = "Use concise answers."

	conversation := newAgentLoopConversation(spec)
	if conversation.ArtifactID() != "convo-run-1" {
		t.Fatalf("ArtifactID() = %q, want convo-run-1", conversation.ArtifactID())
	}

	messages := conversation.Messages()
	if len(messages) != 3 {
		t.Fatalf("messages = %d, want env system + operator system + user: %+v", len(messages), messages)
	}
	if messages[0].Role != "system" || !strings.Contains(messages[0].Content, "/workspace/run") {
		t.Fatalf("environment system message = %+v, want workspace grounding", messages[0])
	}
	if messages[1].Role != "system" || messages[1].Content != "Use concise answers." {
		t.Fatalf("operator system message = %+v", messages[1])
	}
	if messages[2].Role != "user" || messages[2].Content != spec.Task.Prompt {
		t.Fatalf("user message = %+v, want task prompt", messages[2])
	}
}

func TestAgentLoopConversation_ResumePendingToolCallsClearAfterToolResult(t *testing.T) {
	saved := []types.Message{
		{Role: "user", Content: "inspect"},
		{Role: "assistant", Content: "checking", ToolCalls: []types.ToolCall{
			agentLoopToolCall("call-1", "shell_exec", `{"command":"ls"}`),
		}},
	}
	raw, err := json.Marshal(saved)
	if err != nil {
		t.Fatalf("marshal saved conversation: %v", err)
	}
	spec := newAgentLoopSpec(t)
	spec.ResumeCheckpoint = &ResumeCheckpoint{AgentConversation: raw}

	conversation := newAgentLoopConversation(spec)
	pending := conversation.PendingToolCallsForResume()
	if len(pending) != 1 || pending[0].ID != "call-1" {
		t.Fatalf("pending tool calls = %+v, want call-1", pending)
	}
	if tail, ok := conversation.TailAssistantForResume(); !ok || tail.ToolCalls[0].ID != "call-1" {
		t.Fatalf("TailAssistantForResume() = %+v/%v, want assistant call-1", tail, ok)
	}

	conversation.AppendToolResult("call-1", "status=completed", true)
	if pending := conversation.PendingToolCallsForResume(); len(pending) != 0 {
		t.Fatalf("pending after tool result = %+v, want none", pending)
	}
	messages := conversation.Messages()
	last := messages[len(messages)-1]
	if last.Role != "tool" || last.ToolCallID != "call-1" || !last.ToolError {
		t.Fatalf("last message = %+v, want errored tool result", last)
	}
}

func TestAgentLoopConversation_UpsertArtifactPersistsCurrentMessages(t *testing.T) {
	spec := newAgentLoopSpec(t)
	var got types.TaskArtifact
	spec.UpsertArtifact = func(artifact types.TaskArtifact) error {
		got = artifact
		return nil
	}
	when := time.Date(2026, 5, 29, 11, 45, 0, 0, time.UTC)
	conversation := newAgentLoopConversation(spec)
	conversation.AppendAssistant(types.Message{Role: "assistant", Content: "done"})

	artifact, err := conversation.UpsertArtifact(spec, 3, when)
	if err != nil {
		t.Fatalf("UpsertArtifact() error = %v", err)
	}
	if artifact == nil {
		t.Fatal("UpsertArtifact() artifact = nil")
	}
	if got.ID != "convo-run-1" || got.Kind != "agent_conversation" || got.Status != "ready" {
		t.Fatalf("artifact shape = %+v", got)
	}
	if got.Description != "Agent loop conversation snapshot after turn 3" {
		t.Fatalf("artifact description = %q", got.Description)
	}
	if !got.CreatedAt.Equal(when) {
		t.Fatalf("artifact CreatedAt = %s, want %s", got.CreatedAt, when)
	}
	var decoded []types.Message
	if err := json.Unmarshal([]byte(got.ContentText), &decoded); err != nil {
		t.Fatalf("decode artifact content: %v", err)
	}
	if len(decoded) == 0 || decoded[len(decoded)-1].Role != "assistant" || decoded[len(decoded)-1].Content != "done" {
		t.Fatalf("decoded conversation = %+v, want appended assistant message", decoded)
	}
}
