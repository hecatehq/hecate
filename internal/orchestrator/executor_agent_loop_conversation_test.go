package orchestrator

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/taskworkflow"
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
	if !strings.Contains(messages[0].Content, "`code_intelligence`") || !strings.Contains(messages[0].Content, "fall back to `grep`") {
		t.Fatalf("environment system message = %q, want code-intelligence guidance", messages[0].Content)
	}
	if messages[1].Role != "system" || messages[1].Content != "Use concise answers." {
		t.Fatalf("operator system message = %+v", messages[1])
	}
	if messages[2].Role != "user" || messages[2].Content != spec.Task.Prompt {
		t.Fatalf("user message = %+v, want task prompt", messages[2])
	}
}

func TestAgentLoopConversation_ToolsDisabledPreludeDoesNotEncourageToolCalls(t *testing.T) {
	t.Parallel()
	toolsEnabled := false
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = "/workspace/run"
	spec.Task.AgentPresetToolsEnabled = &toolsEnabled

	conversation := newAgentLoopConversation(spec)
	messages := conversation.Messages()
	if len(messages) < 1 || !strings.Contains(messages[0].Content, "Tools are disabled by the resolved agent preset") {
		t.Fatalf("environment system message = %+v, want tools-disabled guidance", messages)
	}
	for _, unexpected := range []string{"/workspace/run", "when calling tools", "`shell_exec`", "`read_file`"} {
		if strings.Contains(messages[0].Content, unexpected) {
			t.Fatalf("environment system message = %q, must not encourage %q", messages[0].Content, unexpected)
		}
	}
}

func TestAgentLoopConversation_QARunSnapshotPreludeOnlyNamesAllowedInspection(t *testing.T) {
	t.Parallel()
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = "/workspace/run"
	// The retained Run is authoritative even if the mutable Task no longer
	// carries the workflow fields.
	spec.Run.WorkflowMode = types.WorkflowModeQA
	spec.Run.WorkflowVersion = taskworkflow.QAVersion

	conversation := newAgentLoopConversation(spec)
	messages := conversation.Messages()
	if len(messages) == 0 || !strings.Contains(messages[0].Content, "report-only inspection tools") {
		t.Fatalf("environment system message = %+v, want QA inspection guidance", messages)
	}
	for _, allowed := range []string{"`read_file`", "`list_dir`", "`grep`", "`glob`", "`artifact_read`"} {
		if !strings.Contains(messages[0].Content, allowed) {
			t.Errorf("QA environment system message = %q, want allowed tool %s", messages[0].Content, allowed)
		}
	}
	for _, unavailable := range []string{"`code_intelligence`", "structural patterns", "`shell_exec`", "`git_exec`"} {
		if strings.Contains(messages[0].Content, unavailable) {
			t.Errorf("QA environment system message = %q, must not encourage %s", messages[0].Content, unavailable)
		}
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

func TestAgentLoopConversation_ContinuationAppendsRichInputMessage(t *testing.T) {
	saved := []types.Message{
		{Role: "user", Content: "first prompt"},
		{Role: "assistant", Content: "first answer"},
	}
	raw, err := json.Marshal(saved)
	if err != nil {
		t.Fatalf("marshal saved conversation: %v", err)
	}
	richInput := types.Message{
		Role:    "user",
		Content: "inspect the new image",
		ContentBlocks: []types.ContentBlock{
			{Type: "text", Text: "inspect the new image"},
			{Type: "image", Image: &types.ContentImage{URL: "data:image/png;base64,cG5n"}},
		},
	}
	spec := newAgentLoopSpec(t)
	spec.InputMessage = &richInput
	spec.ResumeCheckpoint = &ResumeCheckpoint{
		AgentConversation: raw,
		AppendUserPrompt:  "plain fallback must not be appended",
	}

	conversation := newAgentLoopConversation(spec)
	messages := conversation.Messages()
	if len(messages) != 2 || !conversation.HasDeferredContinuation() {
		t.Fatalf("messages = %d deferred=%t, want saved history with deferred rich input: %+v", len(messages), conversation.HasDeferredContinuation(), messages)
	}
	if !conversation.AppendDeferredContinuation() || conversation.HasDeferredContinuation() {
		t.Fatal("AppendDeferredContinuation() did not append exactly once")
	}
	messages = conversation.Messages()
	if len(messages) != 3 {
		t.Fatalf("messages after continuation = %d, want saved history plus rich input: %+v", len(messages), messages)
	}
	got := messages[2]
	if got.Role != "user" || got.Content != richInput.Content || len(got.ContentBlocks) != 2 || got.ContentBlocks[1].Image == nil {
		t.Fatalf("appended message = %+v, want rich input message", got)
	}
}

func TestAgentLoopConversation_SameRunResumeRestoresRichInputInPlace(t *testing.T) {
	saved := []types.Message{
		{
			Role:    "user",
			Content: "inspect the image",
			ContentBlocks: []types.ContentBlock{
				{Type: "text", Text: "inspect the image"},
				{Type: "text", Text: artifactImageOmissionText("image/png")},
			},
		},
		{Role: "assistant", ToolCalls: []types.ToolCall{agentLoopToolCall("call-1", "shell_exec", `{"command":"ls"}`)}},
	}
	raw, err := json.Marshal(saved)
	if err != nil {
		t.Fatalf("marshal saved conversation: %v", err)
	}
	richInput := types.Message{
		Role:    "user",
		Content: "inspect the image",
		ContentBlocks: []types.ContentBlock{
			{Type: "text", Text: "inspect the image"},
			{Type: "image", Image: &types.ContentImage{URL: "data:image/png;base64,cG5n"}},
		},
	}
	spec := newAgentLoopSpec(t)
	spec.InputMessage = &richInput
	spec.ResumeCheckpoint = &ResumeCheckpoint{AgentConversation: raw}

	conversation := newAgentLoopConversation(spec)
	messages := conversation.Messages()
	if len(messages) != 2 {
		t.Fatalf("messages = %d, want restored history without a duplicate input message: %+v", len(messages), messages)
	}
	if image := messages[0].ContentBlocks[1].Image; image == nil || image.URL != richInput.ContentBlocks[1].Image.URL {
		t.Fatalf("restored user message = %+v, want hydrated image block", messages[0])
	}
	pending := conversation.PendingToolCallsForResume()
	if len(pending) != 1 || pending[0].ID != "call-1" {
		t.Fatalf("pending tool calls = %+v, want approval-resume call-1", pending)
	}
}

func TestAgentLoopConversation_ArtifactSanitizesImageBodiesAndURLs(t *testing.T) {
	messages := []types.Message{{
		Role:    "user",
		Content: "compare images",
		ContentBlocks: []types.ContentBlock{
			{Type: "text", Text: "compare images"},
			{Type: "image_url", Image: &types.ContentImage{URL: "data:image/png;base64,cG5n", MediaType: "image/png"}},
			{Type: "image", Image: &types.ContentImage{Data: "anBlZw==", MediaType: "image/jpeg"}},
			{Type: "image_url", Image: &types.ContentImage{URL: "https://example.com/public.png", MediaType: "image/png"}},
		},
	}}

	sanitized := conversationMessagesForArtifact(messages)
	blocks := sanitized[0].ContentBlocks
	if len(blocks) != 4 {
		t.Fatalf("sanitized blocks = %d, want 4: %+v", len(blocks), blocks)
	}
	if blocks[1].Image != nil || blocks[1].Text != artifactImageOmissionText("image/png") {
		t.Fatalf("data URL block = %+v, want omission marker", blocks[1])
	}
	if blocks[2].Image != nil || blocks[2].Text != artifactImageOmissionText("image/jpeg") {
		t.Fatalf("inline data block = %+v, want omission marker", blocks[2])
	}
	if blocks[3].Image != nil || blocks[3].Text != artifactImageOmissionText("image/png") {
		t.Fatalf("remote URL block = %+v, want omission marker", blocks[3])
	}
	if messages[0].ContentBlocks[1].Image == nil {
		t.Fatal("sanitizer mutated the live conversation")
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
	if got.Description != "Agent loop conversation snapshot after model call 3" {
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
