package orchestrator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/runtimeevents"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestAgentLoop_CostCeilingRejectsPaidFinalAnswerBeforeCompletion(t *testing.T) {
	response := makeChatResp(makeAssistantMsg("paid final answer"))
	response.Cost.TotalMicrosUSD = 500
	llm := &scriptedLLM{responses: []*types.ChatResponse{response}}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.BudgetMicrosUSD = 500
	finalEvents := 0
	spec.EmitRunEvent = func(eventType string, _ map[string]any) {
		if eventType == runtimeevents.EventAssistantFinalAnswer.String() {
			finalEvents++
		}
	}

	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "failed" || !strings.Contains(res.LastError, "cost ceiling") {
		t.Fatalf("result = status %q error %q, want cost-ceiling failure", res.Status, res.LastError)
	}
	if res.CostMicrosUSD != 500 || llm.calls.Load() != 1 {
		t.Fatalf("accounting = cost %d calls %d, want 500/1", res.CostMicrosUSD, llm.calls.Load())
	}
	if finalEvents != 0 || findArtifactByKind(res.Artifacts, "summary") != nil {
		t.Fatalf("over-budget answer was finalized: events=%d artifacts=%+v", finalEvents, res.Artifacts)
	}
	conversation := findArtifactByKind(res.Artifacts, "agent_conversation")
	if conversation == nil {
		t.Fatal("cost-ceiling failure omitted the paid assistant conversation")
	}
	var messages []types.Message
	if err := json.Unmarshal([]byte(conversation.ContentText), &messages); err != nil {
		t.Fatalf("decode conversation: %v", err)
	}
	if len(messages) == 0 || messages[len(messages)-1].Role != "assistant" || messages[len(messages)-1].Content != "paid final answer" {
		t.Fatalf("conversation tail = %+v, want paid assistant answer preserved", messages)
	}
}

func TestAgentLoop_CostCeilingBlocksToolDispatchAndApproval(t *testing.T) {
	for _, test := range []struct {
		name       string
		gatedTools []string
	}{
		{name: "ungated"},
		{name: "gated", gatedTools: []string{"shell_exec"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			call := agentLoopToolCall("call-costly", "shell_exec", `{"command":"non-idempotent-effect"}`)
			response := makeChatResp(makeAssistantMsg("", call))
			response.Cost.TotalMicrosUSD = 600
			llm := &scriptedLLM{responses: []*types.ChatResponse{response}}
			shell := &stubExecutor{result: &ExecutionResult{Status: "completed"}}
			loop := NewAgentLoopExecutor(llm, shell, &stubExecutor{}, &stubExecutor{}, 8, test.gatedTools, HTTPRequestPolicy{})
			spec := newAgentLoopSpec(t)
			spec.Task.BudgetMicrosUSD = 500

			res, err := loop.Execute(context.Background(), spec)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if res.Status != "failed" || !strings.Contains(res.LastError, "cost ceiling") {
				t.Fatalf("result = status %q error %q, want cost-ceiling failure", res.Status, res.LastError)
			}
			if len(shell.calls) != 0 || len(res.PendingApprovals) != 0 {
				t.Fatalf("post-ceiling effects = shell %+v approvals %+v, want none", shell.calls, res.PendingApprovals)
			}
		})
	}
}

func TestAgentLoop_CostCeilingBlocksApprovedSameRunDispatch(t *testing.T) {
	call := agentLoopToolCall("call-approved", "shell_exec", `{"command":"non-idempotent-effect"}`)
	saved, err := json.Marshal([]types.Message{
		{Role: "user", Content: "run it"},
		makeAssistantMsg("", call),
	})
	if err != nil {
		t.Fatalf("marshal conversation: %v", err)
	}
	llm := &scriptedLLM{}
	shell := &stubExecutor{result: &ExecutionResult{Status: "completed"}}
	loop := NewAgentLoopExecutor(llm, shell, &stubExecutor{}, &stubExecutor{}, 8, []string{"shell_exec"}, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.BudgetMicrosUSD = 500
	spec.ResumeCheckpoint = &ResumeCheckpoint{
		SourceRunID:              spec.Run.ID,
		SameRun:                  true,
		AgentConversation:        saved,
		ThisRunCostMicrosUSD:     500,
		ThisRunModelCallCount:    1,
		PendingToolCallsApproved: true,
	}

	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "failed" || !strings.Contains(res.LastError, "cost ceiling") {
		t.Fatalf("result = status %q error %q, want cost-ceiling failure", res.Status, res.LastError)
	}
	if len(shell.calls) != 0 || llm.calls.Load() != 0 || len(res.PendingApprovals) != 0 {
		t.Fatalf("post-ceiling work = shell %d model %d approvals %d, want zero", len(shell.calls), llm.calls.Load(), len(res.PendingApprovals))
	}
}

func TestAgentLoop_CostCeilingBlocksInheritedResumeBeforeModelCall(t *testing.T) {
	saved, err := json.Marshal([]types.Message{{Role: "user", Content: "continue"}})
	if err != nil {
		t.Fatalf("marshal conversation: %v", err)
	}
	llm := &scriptedLLM{responses: []*types.ChatResponse{makeChatResp(makeAssistantMsg("must not run"))}}
	shell := &stubExecutor{result: &ExecutionResult{Status: "completed"}}
	loop := NewAgentLoopExecutor(llm, shell, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.BudgetMicrosUSD = 500
	spec.ResumeCheckpoint = &ResumeCheckpoint{
		SourceRunID:        "run-source",
		AgentConversation:  saved,
		PriorCostMicrosUSD: 500,
		AppendUserPrompt:   "one more thing",
	}

	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "failed" || !strings.Contains(res.LastError, "cost ceiling") {
		t.Fatalf("result = status %q error %q, want inherited cost-ceiling failure", res.Status, res.LastError)
	}
	if llm.calls.Load() != 0 || len(shell.calls) != 0 {
		t.Fatalf("inherited over-budget resume performed work: model=%d shell=%d", llm.calls.Load(), len(shell.calls))
	}
	conversation := findArtifactByKind(res.Artifacts, "agent_conversation")
	if conversation == nil || !strings.Contains(conversation.ContentText, "one more thing") {
		t.Fatalf("failed resume did not preserve deferred continuation: %+v", conversation)
	}
}

func TestAgentLoop_CostCeilingBlocksSameRunFinalTailRecovery(t *testing.T) {
	saved, err := json.Marshal([]types.Message{
		{Role: "user", Content: "answer"},
		makeAssistantMsg("paid final answer"),
	})
	if err != nil {
		t.Fatalf("marshal conversation: %v", err)
	}
	llm := &scriptedLLM{}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.BudgetMicrosUSD = 500
	spec.ResumeCheckpoint = &ResumeCheckpoint{
		SourceRunID:           spec.Run.ID,
		SameRun:               true,
		AgentConversation:     saved,
		ThisRunCostMicrosUSD:  500,
		ThisRunModelCallCount: 1,
		LastCompletedStepID:   "step-model",
	}

	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "failed" || !strings.Contains(res.LastError, "cost ceiling") {
		t.Fatalf("result = status %q error %q, want cost-ceiling failure", res.Status, res.LastError)
	}
	if llm.calls.Load() != 0 || findArtifactByKind(res.Artifacts, "summary") != nil {
		t.Fatalf("same-run final tail bypassed ceiling: model=%d artifacts=%+v", llm.calls.Load(), res.Artifacts)
	}
}
