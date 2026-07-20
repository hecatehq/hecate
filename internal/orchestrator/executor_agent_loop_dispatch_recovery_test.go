package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/runtimeevents"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestPendingToolCallsForResume_PartialBatchReturnsOnlyUnresolvedCalls(t *testing.T) {
	calls := []types.ToolCall{
		agentLoopToolCall("call-1", "shell_exec", `{"command":"first"}`),
		agentLoopToolCall("call-2", "shell_exec", `{"command":"second"}`),
	}
	messages := []types.Message{
		{Role: "user", Content: "run both"},
		makeAssistantMsg("", calls...),
		{Role: "tool", ToolCallID: "call-1", Content: "first result"},
	}

	pending := pendingToolCallsForResume(messages)
	if len(pending) != 1 || pending[0].ID != "call-2" {
		t.Fatalf("pendingToolCallsForResume() = %+v, want only call-2", pending)
	}
	conversation := agentLoopConversation{messages: messages}
	assistant, ok := conversation.TailAssistantForResume()
	if !ok || len(assistant.ToolCalls) != 2 {
		t.Fatalf("TailAssistantForResume() = %+v/%t, want original two-call assistant batch", assistant, ok)
	}
}

func TestAgentLoop_InvalidToolCallIDsFailBeforeApprovalOrDispatch(t *testing.T) {
	oversized := make([]types.ToolCall, 0, agentToolCallMaxPerBatch+1)
	for index := 0; index <= agentToolCallMaxPerBatch; index++ {
		oversized = append(oversized, agentLoopToolCall("oversized-"+string(rune('a'+index)), "shell_exec", `{"command":"effect"}`))
	}
	tests := []struct {
		name  string
		calls []types.ToolCall
		want  string
	}{
		{
			name:  "empty",
			calls: []types.ToolCall{agentLoopToolCall("", "shell_exec", `{"command":"effect"}`)},
			want:  "empty id",
		},
		{
			name: "duplicate",
			calls: []types.ToolCall{
				agentLoopToolCall("same-id", "shell_exec", `{"command":"first"}`),
				agentLoopToolCall("same-id", "shell_exec", `{"command":"second"}`),
			},
			want: "duplicates id",
		},
		{
			name:  "oversized",
			calls: oversized,
			want:  "maximum is 16",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			llm := &scriptedLLM{responses: []*types.ChatResponse{
				makeChatResp(makeAssistantMsg("", test.calls...)),
			}}
			shell := &stubExecutor{result: &ExecutionResult{Status: "completed"}}
			loop := NewAgentLoopExecutor(llm, shell, &stubExecutor{}, &stubExecutor{}, 8, []string{"shell_exec"}, HTTPRequestPolicy{})
			spec := newAgentLoopSpec(t)
			proposedEvents := 0
			spec.EmitRunEvent = func(eventType string, _ map[string]any) {
				if eventType == runtimeevents.EventAssistantToolCallProposed.String() {
					proposedEvents++
				}
			}

			res, err := loop.Execute(context.Background(), spec)
			if err != nil {
				t.Fatalf("Execute(): %v", err)
			}
			if res.Status != "failed" || !strings.Contains(res.LastError, test.want) {
				t.Fatalf("result = status %q error %q, want failed containing %q", res.Status, res.LastError, test.want)
			}
			if len(shell.calls) != 0 || len(res.PendingApprovals) != 0 {
				t.Fatalf("invalid bundle caused effects/approval: shell=%+v approvals=%+v", shell.calls, res.PendingApprovals)
			}
			if proposedEvents != 0 {
				t.Fatalf("invalid bundle emitted %d assistant tool proposal events, want zero", proposedEvents)
			}
			var modelStep *types.TaskStep
			for index := range res.Steps {
				if res.Steps[index].ToolName == "builtin.agent_loop_llm" {
					modelStep = &res.Steps[index]
					break
				}
			}
			if modelStep == nil || intField(modelStep.OutputSummary["tool_call_count"]) != len(test.calls) || modelStep.OutputSummary["invalid_tool_call_bundle"] != true {
				t.Fatalf("invalid bundle model Step = %+v, want bounded count %d", modelStep, len(test.calls))
			}
			if _, retainedNames := modelStep.OutputSummary["tool_calls"]; retainedNames {
				t.Fatalf("invalid bundle model Step retained tool names: %+v", modelStep.OutputSummary)
			}
			if encoded, marshalErr := json.Marshal(modelStep.OutputSummary); marshalErr != nil {
				t.Fatalf("marshal model Step output: %v", marshalErr)
			} else if len(encoded) > 1024 {
				t.Fatalf("invalid bundle model Step output = %d bytes, want bounded evidence: %s", len(encoded), encoded)
			}
			conversation := findArtifactByKind(res.Artifacts, "agent_conversation")
			if conversation == nil || !strings.Contains(conversation.ContentText, "builtin.invalid_tool_call_bundle") {
				t.Fatalf("invalid bundle conversation evidence = %+v, want bounded invalid marker", conversation)
			}
			var persistedMessages []types.Message
			if unmarshalErr := json.Unmarshal([]byte(conversation.ContentText), &persistedMessages); unmarshalErr != nil {
				t.Fatalf("decode invalid bundle conversation: %v", unmarshalErr)
			}
			persistedAssistant := persistedMessages[len(persistedMessages)-1]
			if len(persistedAssistant.ToolCalls) != 1 || persistedAssistant.ToolCalls[0].ID != "" ||
				persistedAssistant.ToolCalls[0].Function.Name != "builtin.invalid_tool_call_bundle" ||
				persistedAssistant.ToolCalls[0].Function.Arguments != `{}` {
				t.Fatalf("persisted invalid bundle = %+v, want one argument-free invalid marker", persistedAssistant.ToolCalls)
			}
		})
	}
}

func TestAgentLoop_InvalidRecoveredToolCallIDsNeverDispatch(t *testing.T) {
	oversized := make([]types.ToolCall, 0, agentToolCallMaxPerBatch+1)
	for index := 0; index <= agentToolCallMaxPerBatch; index++ {
		oversized = append(oversized, agentLoopToolCall("oversized-"+string(rune('a'+index)), "shell_exec", `{"command":"effect"}`))
	}
	tests := []struct {
		name  string
		calls []types.ToolCall
		want  string
	}{
		{
			name:  "empty",
			calls: []types.ToolCall{agentLoopToolCall("", "shell_exec", `{"command":"effect"}`)},
			want:  "empty id",
		},
		{
			name: "duplicate",
			calls: []types.ToolCall{
				agentLoopToolCall("same-id", "shell_exec", `{"command":"first"}`),
				agentLoopToolCall("same-id", "shell_exec", `{"command":"second"}`),
			},
			want: "duplicates id",
		},
		{
			name:  "oversized",
			calls: oversized,
			want:  "maximum is 16",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			saved, err := json.Marshal([]types.Message{
				{Role: "user", Content: "run it"},
				makeAssistantMsg("", test.calls...),
			})
			if err != nil {
				t.Fatalf("marshal conversation: %v", err)
			}
			spec := newAgentLoopSpec(t)
			intent := buildAgentToolDispatchIntent(spec, test.calls[0], 2, 1, time.Now().UTC())
			spec.ResumeCheckpoint = &ResumeCheckpoint{
				SourceRunID:              spec.Run.ID,
				SameRun:                  true,
				LastStepIndex:            intent.Index,
				LastCompletedStepID:      "model-step",
				CompletedStepCount:       1,
				AgentConversation:        saved,
				ThisRunModelCallCount:    1,
				ToolDispatchSteps:        []types.TaskStep{intent},
				PendingToolCallsApproved: true,
			}
			llm := &scriptedLLM{}
			shell := &stubExecutor{result: &ExecutionResult{Status: "completed"}}
			loop := NewAgentLoopExecutor(llm, shell, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})

			res, err := loop.Execute(context.Background(), spec)
			if err != nil {
				t.Fatalf("Execute(): %v", err)
			}
			if res.Status != "failed" || !strings.Contains(res.LastError, test.want) {
				t.Fatalf("result = status %q error %q, want failed containing %q", res.Status, res.LastError, test.want)
			}
			if len(shell.calls) != 0 || llm.calls.Load() != 0 {
				t.Fatalf("invalid recovered bundle was replayed: shell=%+v llm_calls=%d", shell.calls, llm.calls.Load())
			}
		})
	}
}

func TestAgentLoop_CrashAfterFirstOfTwoCallsDoesNotReplayCompletedPrefix(t *testing.T) {
	injectedCrash := errors.New("injected crash before second dispatch")
	calls := []types.ToolCall{
		agentLoopToolCall("call-1", "shell_exec", `{"command":"first"}`),
		agentLoopToolCall("call-2", "shell_exec", `{"command":"second"}`),
	}
	llm := &scriptedLLM{responses: []*types.ChatResponse{
		makeChatResp(makeAssistantMsg("", calls...)),
		makeChatResp(makeAssistantMsg("both complete")),
	}}
	shell := &stubExecutor{result: &ExecutionResult{Status: "completed"}}
	loop := NewAgentLoopExecutor(llm, shell, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)

	persistedSteps := make(map[string]types.TaskStep)
	var conversation types.TaskArtifact
	crashBeforeSecond := true
	spec.UpsertStep = func(step types.TaskStep) error {
		if crashBeforeSecond && isDurableToolDispatchStep(step) &&
			step.Status == "running" && stringField(step.Input[toolDispatchCallIDKey]) == "call-2" {
			return injectedCrash
		}
		persistedSteps[step.ID] = step
		return nil
	}
	spec.UpsertArtifact = func(artifact types.TaskArtifact) error {
		if artifact.Kind == "agent_conversation" {
			conversation = artifact
		}
		return nil
	}

	if _, err := loop.Execute(context.Background(), spec); !errors.Is(err, injectedCrash) {
		t.Fatalf("first Execute() error = %v, want injected crash", err)
	}
	if len(shell.calls) != 1 || shell.calls[0].ShellCommand != "first" {
		t.Fatalf("effects before crash = %+v, want only first command", shell.calls)
	}
	assertToolResultCounts(t, conversation.ContentText, map[string]int{"call-1": 1, "call-2": 0})

	spec.ResumeCheckpoint = sameRunDispatchCheckpoint(t, spec, conversation, persistedSteps, 1)
	crashBeforeSecond = false
	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("resumed Execute(): %v", err)
	}
	if res.Status != "completed" {
		t.Fatalf("resumed status = %q, want completed", res.Status)
	}
	if len(shell.calls) != 2 || shell.calls[0].ShellCommand != "first" || shell.calls[1].ShellCommand != "second" {
		t.Fatalf("effects across crash = %+v, want first then second exactly once", shell.calls)
	}
	if got := llm.calls.Load(); got != 2 {
		t.Fatalf("model calls across crash = %d, want two", got)
	}
	assertToolResultCounts(t, conversation.ContentText, map[string]int{"call-1": 1, "call-2": 1})
}

func TestAgentLoop_CrashAfterEffectBeforeFinalizationSettlesIntentWithoutReplay(t *testing.T) {
	injectedCrash := errors.New("injected crash after tool effect")
	call := agentLoopToolCall("call-1", "shell_exec", `{"command":"non-idempotent-effect"}`)
	llm := &scriptedLLM{responses: []*types.ChatResponse{
		makeChatResp(makeAssistantMsg("", call)),
		makeChatResp(makeAssistantMsg("recovery acknowledged")),
	}}
	shell := &stubExecutor{result: &ExecutionResult{Status: "completed"}}
	loop := NewAgentLoopExecutor(llm, shell, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)

	persistedSteps := make(map[string]types.TaskStep)
	var conversation types.TaskArtifact
	crashOnFinalization := true
	spec.UpsertStep = func(step types.TaskStep) error {
		if crashOnFinalization && isDurableToolDispatchStep(step) && step.Status != "running" {
			return injectedCrash
		}
		persistedSteps[step.ID] = step
		return nil
	}
	spec.UpsertArtifact = func(artifact types.TaskArtifact) error {
		if artifact.Kind == "agent_conversation" {
			conversation = artifact
		}
		return nil
	}

	if _, err := loop.Execute(context.Background(), spec); !errors.Is(err, injectedCrash) {
		t.Fatalf("first Execute() error = %v, want injected crash", err)
	}
	if len(shell.calls) != 1 {
		t.Fatalf("effects before crash = %d, want one", len(shell.calls))
	}
	running := onlyDurableDispatchStep(t, persistedSteps)
	if running.Status != "running" {
		t.Fatalf("durable intent status after crash = %q, want running", running.Status)
	}
	assertToolResultCounts(t, conversation.ContentText, map[string]int{"call-1": 0})

	spec.ResumeCheckpoint = sameRunDispatchCheckpoint(t, spec, conversation, persistedSteps, 1)
	crashOnFinalization = false
	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("resumed Execute(): %v", err)
	}
	if res.Status != "completed" {
		t.Fatalf("resumed status = %q, want completed", res.Status)
	}
	if len(shell.calls) != 1 {
		t.Fatalf("non-idempotent effect count across crash = %d, want exactly one", len(shell.calls))
	}
	settled := persistedSteps[running.ID]
	if settled.Status != "failed" || settled.ErrorKind != "tool_dispatch_outcome_unknown" || settled.Phase != "recovery" {
		t.Fatalf("settled intent = %+v, want fail-closed recovery on same Step", settled)
	}
	if len(llm.lastReqs) != 2 {
		t.Fatalf("LLM requests = %d, want two across crash", len(llm.lastReqs))
	}
	assertRecoveryToolError(t, llm.lastReqs[1].Messages, "call-1")
}

func TestAgentLoop_CompletedDispatchMissingConversationResultIsNotReplayed(t *testing.T) {
	call := agentLoopToolCall("call-completed", "shell_exec", `{"command":"already-ran"}`)
	saved, err := json.Marshal([]types.Message{
		{Role: "user", Content: "run it"},
		makeAssistantMsg("", call),
	})
	if err != nil {
		t.Fatalf("marshal conversation: %v", err)
	}
	spec := newAgentLoopSpec(t)
	intent := buildAgentToolDispatchIntent(spec, call, 2, 1, time.Now().UTC())
	completed := intent
	completed.Status = "completed"
	completed.FinishedAt = time.Now().UTC()
	completed.OutputSummary = map[string]any{"dispatch_intent_settled": true}
	spec.ResumeCheckpoint = &ResumeCheckpoint{
		SourceRunID:              spec.Run.ID,
		SameRun:                  true,
		LastStepIndex:            completed.Index,
		LastCompletedStepID:      completed.ID,
		CompletedStepCount:       2,
		AgentConversation:        saved,
		ThisRunModelCallCount:    1,
		ToolDispatchSteps:        []types.TaskStep{completed},
		PendingToolCallsApproved: true,
	}

	llm := &scriptedLLM{responses: []*types.ChatResponse{
		makeChatResp(makeAssistantMsg("recovery acknowledged")),
	}}
	shell := &stubExecutor{result: &ExecutionResult{Status: "completed"}}
	loop := NewAgentLoopExecutor(llm, shell, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if res.Status != "completed" || len(shell.calls) != 0 {
		t.Fatalf("result status = %q shell effects = %d, want completed without replay", res.Status, len(shell.calls))
	}
	if len(llm.lastReqs) != 1 {
		t.Fatalf("LLM requests = %d, want one", len(llm.lastReqs))
	}
	assertRecoveryToolError(t, llm.lastReqs[0].Messages, "call-completed")
}

func TestAgentLoop_ContinueNormalizesUnresolvedSourceBatchBeforeUserPrompt(t *testing.T) {
	completedCall := agentLoopToolCall("call-completed", "shell_exec", `{"command":"already-ran"}`)
	neverStartedCall := agentLoopToolCall("call-never-started", "shell_exec", `{"command":"stale"}`)
	saved, err := json.Marshal([]types.Message{
		{Role: "user", Content: "first request"},
		makeAssistantMsg("", completedCall, neverStartedCall),
	})
	if err != nil {
		t.Fatalf("marshal conversation: %v", err)
	}
	spec := newAgentLoopSpec(t)
	sourceSpec := spec
	sourceSpec.Run.ID = "run-source"
	completed := buildAgentToolDispatchIntent(sourceSpec, completedCall, 2, 1, time.Now().UTC())
	completed.Status = "completed"
	completed.FinishedAt = time.Now().UTC()
	spec.ResumeCheckpoint = &ResumeCheckpoint{
		SourceRunID:                sourceSpec.Run.ID,
		AgentConversation:          saved,
		AppendUserPrompt:           "second request",
		ToolDispatchSteps:          []types.TaskStep{completed},
		ToolDispatchModelCallIndex: 1,
	}

	llm := &scriptedLLM{responses: []*types.ChatResponse{
		makeChatResp(makeAssistantMsg("continued safely")),
	}}
	shell := &stubExecutor{result: &ExecutionResult{Status: "completed"}}
	loop := NewAgentLoopExecutor(llm, shell, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	res, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if res.Status != "completed" || len(shell.calls) != 0 {
		t.Fatalf("result status = %q shell effects = %d, want no source-call replay", res.Status, len(shell.calls))
	}
	if len(llm.lastReqs) != 1 {
		t.Fatalf("LLM requests = %d, want one", len(llm.lastReqs))
	}
	messages := llm.lastReqs[0].Messages
	if len(messages) != 5 {
		t.Fatalf("continued messages = %+v, want user/assistant/two tool results/user", messages)
	}
	if messages[2].Role != "tool" || messages[2].ToolCallID != completedCall.ID || !messages[2].ToolError ||
		!strings.Contains(messages[2].Content, "did not replay") {
		t.Fatalf("completed source result = %+v, want outcome-unknown recovery", messages[2])
	}
	if messages[3].Role != "tool" || messages[3].ToolCallID != neverStartedCall.ID || !messages[3].ToolError ||
		!strings.Contains(messages[3].Content, "continuation prompt superseded") {
		t.Fatalf("never-started source result = %+v, want explicit superseded result", messages[3])
	}
	if messages[4].Role != "user" || messages[4].Content != "second request" {
		t.Fatalf("continuation tail = %+v, want second user request after tool results", messages[4])
	}
}

func sameRunDispatchCheckpoint(t *testing.T, spec ExecutionSpec, conversation types.TaskArtifact, persisted map[string]types.TaskStep, modelCalls int) *ResumeCheckpoint {
	t.Helper()
	if conversation.Kind != "agent_conversation" || conversation.ContentText == "" {
		t.Fatalf("missing durable conversation snapshot: %+v", conversation)
	}
	steps := make([]types.TaskStep, 0, len(persisted))
	checkpoint := &ResumeCheckpoint{
		SourceRunID:           spec.Run.ID,
		SameRun:               true,
		AgentConversation:     []byte(conversation.ContentText),
		ThisRunModelCallCount: modelCalls,
		ArtifactCount:         1,
	}
	for _, step := range persisted {
		steps = append(steps, step)
		if step.Index > checkpoint.LastStepIndex {
			checkpoint.LastStepIndex = step.Index
		}
		if step.Status == "completed" {
			checkpoint.CompletedStepCount++
			if checkpoint.LastCompletedStepID == "" || step.Index >= persisted[checkpoint.LastCompletedStepID].Index {
				checkpoint.LastCompletedStepID = step.ID
			}
		}
	}
	checkpoint.ToolDispatchSteps = durableToolDispatchSteps(steps)
	return checkpoint
}

func onlyDurableDispatchStep(t *testing.T, persisted map[string]types.TaskStep) types.TaskStep {
	t.Helper()
	var durable []types.TaskStep
	for _, step := range persisted {
		if isDurableToolDispatchStep(step) {
			durable = append(durable, step)
		}
	}
	if len(durable) != 1 {
		t.Fatalf("durable dispatch Steps = %+v, want exactly one", durable)
	}
	return durable[0]
}

func assertToolResultCounts(t *testing.T, raw string, want map[string]int) {
	t.Helper()
	var messages []types.Message
	if err := json.Unmarshal([]byte(raw), &messages); err != nil {
		t.Fatalf("decode conversation: %v\n%s", err, raw)
	}
	counts := make(map[string]int)
	for _, message := range messages {
		if message.Role == "tool" {
			counts[message.ToolCallID]++
		}
	}
	for callID, count := range want {
		if counts[callID] != count {
			t.Fatalf("tool result count for %q = %d, want %d; messages=%+v", callID, counts[callID], count, messages)
		}
	}
}

func assertRecoveryToolError(t *testing.T, messages []types.Message, callID string) {
	t.Helper()
	for _, message := range messages {
		if message.Role == "tool" && message.ToolCallID == callID && message.ToolError &&
			strings.Contains(message.Content, "did not replay") && strings.Contains(message.Content, "may have produced side effects") {
			return
		}
	}
	t.Fatalf("messages = %+v, want bounded fail-closed recovery result for %q", messages, callID)
}

func stringField(value any) string {
	valueString, _ := value.(string)
	return valueString
}
