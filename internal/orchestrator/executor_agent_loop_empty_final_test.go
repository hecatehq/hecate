package orchestrator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/runtimeevents"
	"github.com/hecatehq/hecate/internal/taskworkflow"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestAgentLoop_EmptyToolFreeAssistantFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
	}{
		{name: "empty"},
		{name: "whitespace", content: " \n\t"},
	} {
		t.Run(test.name, func(t *testing.T) {
			llm := &scriptedLLM{responses: []*types.ChatResponse{
				makeChatResp(makeAssistantMsg(test.content)),
			}}
			loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})
			spec := newAgentLoopSpec(t)
			var eventTypes []string
			spec.EmitRunEvent = func(eventType string, _ map[string]any) {
				eventTypes = append(eventTypes, eventType)
			}

			result, err := loop.Execute(context.Background(), spec)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			wantError := emptyAssistantFinalAnswerMessage(1)
			if result.Status != "failed" || result.LastError != wantError {
				t.Fatalf("result = status %q error %q, want failed/%q", result.Status, result.LastError, wantError)
			}
			if got := llm.calls.Load(); got != 1 || result.ModelCallCount != 1 {
				t.Fatalf("model calls = provider %d result %d, want 1/1", got, result.ModelCallCount)
			}
			if len(result.Steps) != 2 || result.Steps[0].Status != "completed" || result.Steps[1].Status != "failed" {
				t.Fatalf("steps = %+v, want completed model evidence followed by failed control step", result.Steps)
			}
			if got := result.Steps[0].OutputSummary["content_chars"]; got != len(test.content) {
				t.Fatalf("content_chars = %v, want %d", got, len(test.content))
			}
			if got := result.Steps[0].OutputSummary["finish_reason"]; got != "stop" {
				t.Fatalf("finish_reason = %v, want stop", got)
			}
			if findArtifactByKind(result.Artifacts, "summary") != nil || findArtifactByKind(result.Artifacts, "workflow_report") != nil {
				t.Fatalf("blank response produced terminal artifact: %+v", result.Artifacts)
			}
			for _, eventType := range eventTypes {
				if eventType == runtimeevents.EventAssistantFinalAnswer.String() {
					t.Fatalf("blank response emitted %q", eventType)
				}
			}
			conversation := findArtifactByKind(result.Artifacts, "agent_conversation")
			if conversation == nil {
				t.Fatal("blank response omitted durable conversation evidence")
			}
			var messages []types.Message
			if err := json.Unmarshal([]byte(conversation.ContentText), &messages); err != nil {
				t.Fatalf("decode conversation: %v", err)
			}
			if len(messages) == 0 || messages[len(messages)-1].Role != "assistant" || messages[len(messages)-1].Content != test.content {
				t.Fatalf("conversation tail = %+v, want preserved blank assistant", messages)
			}
		})
	}
}

func TestAgentLoop_ToolCallThenEmptyFinalAnswerFailsClosed(t *testing.T) {
	call := types.ToolCall{
		ID:   "call-before-empty",
		Type: "function",
		Function: types.ToolCallFunction{
			Name:      "shell_exec",
			Arguments: `{"command":"pwd"}`,
		},
	}
	llm := &scriptedLLM{responses: []*types.ChatResponse{
		makeChatResp(makeAssistantMsg("", call)),
		makeChatResp(makeAssistantMsg("")),
	}}
	shell := &stubExecutor{result: &ExecutionResult{Status: "completed"}}
	loop := NewAgentLoopExecutor(llm, shell, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})

	result, err := loop.Execute(context.Background(), newAgentLoopSpec(t))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != "failed" || result.LastError != emptyAssistantFinalAnswerMessage(2) {
		t.Fatalf("result = status %q error %q, want empty second-answer failure", result.Status, result.LastError)
	}
	if len(shell.calls) != 1 || llm.calls.Load() != 2 || result.ModelCallCount != 2 {
		t.Fatalf("work = shell %d provider %d result calls %d, want 1/2/2", len(shell.calls), llm.calls.Load(), result.ModelCallCount)
	}
	if len(result.Steps) != 4 || result.Steps[1].ToolName != "shell_exec" || result.Steps[1].Status != "completed" || result.Steps[3].Status != "failed" {
		t.Fatalf("steps = %+v, want retained tool evidence and terminal failure", result.Steps)
	}
	if findArtifactByKind(result.Artifacts, "summary") != nil {
		t.Fatalf("empty second response produced a summary: %+v", result.Artifacts)
	}
}

func TestAgentLoopSameRunRecoveryRejectsSavedEmptyFinalAssistant(t *testing.T) {
	llm := &scriptedLLM{}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.ResumeCheckpoint = &ResumeCheckpoint{
		SourceRunID:           spec.Run.ID,
		SameRun:               true,
		LastCompletedStepID:   "step-model-1",
		LastStepIndex:         1,
		ThisRunModelCallCount: 1,
		ThisRunCostMicrosUSD:  17,
		AgentConversation:     []byte(`[{"role":"user","content":"answer"},{"role":"assistant","content":" \n\t"}]`),
	}
	var eventTypes []string
	spec.EmitRunEvent = func(eventType string, _ map[string]any) {
		eventTypes = append(eventTypes, eventType)
	}

	result, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != "failed" || result.LastError != emptyAssistantFinalAnswerMessage(1) {
		t.Fatalf("result = status %q error %q, want recovered-empty failure", result.Status, result.LastError)
	}
	if got := llm.calls.Load(); got != 0 || result.ModelCallCount != 1 || result.CostMicrosUSD != 17 {
		t.Fatalf("recovery accounting = provider %d calls %d cost %d, want 0/1/17", got, result.ModelCallCount, result.CostMicrosUSD)
	}
	if findArtifactByKind(result.Artifacts, "agent_conversation") == nil {
		t.Fatal("same-run failure omitted recovered conversation")
	}
	if findArtifactByKind(result.Artifacts, "summary") != nil || findArtifactByKind(result.Artifacts, "workflow_report") != nil {
		t.Fatalf("same-run recovery produced blank terminal artifact: %+v", result.Artifacts)
	}
	for _, eventType := range eventTypes {
		if eventType == runtimeevents.EventAssistantFinalAnswer.String() {
			t.Fatalf("same-run recovery emitted %q", eventType)
		}
	}
}

func TestBuildTerminalArtifactRejectsBlankContent(t *testing.T) {
	for _, test := range []struct {
		name string
		qa   bool
	}{
		{name: "ordinary"},
		{name: "qa", qa: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := newAgentLoopSpec(t)
			if test.qa {
				spec.Task.WorkflowMode = types.WorkflowModeQA
				spec.Task.WorkflowVersion = taskworkflow.QAVersion
				spec.Run.WorkflowMode = types.WorkflowModeQA
				spec.Run.WorkflowVersion = taskworkflow.QAVersion
			}
			_, err := buildTerminalArtifact(spec, "step-final", time.Now().UTC(), " \n\t")
			if err == nil || !strings.Contains(err.Error(), "content is required") {
				t.Fatalf("buildTerminalArtifact error = %v, want blank-content rejection", err)
			}
		})
	}
}
