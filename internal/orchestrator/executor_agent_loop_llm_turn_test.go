package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/pkg/types"
)

func TestAgentLoopLLMTurn_SuccessRecordsStateAndConversation(t *testing.T) {
	toolCall := types.ToolCall{
		ID:   "call-1",
		Type: "function",
		Function: types.ToolCallFunction{
			Name:      "shell_exec",
			Arguments: `{"command":"pwd"}`,
		},
	}
	resp := withResolvedRoute(makeChatResp(makeAssistantMsg("I will inspect.", toolCall)))
	resp.Cost.TotalMicrosUSD = 42
	llm := &scriptedLLM{responses: []*types.ChatResponse{resp}}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Run.Provider = "ollama"
	var upsertedArtifacts []types.TaskArtifact
	spec.UpsertArtifact = func(artifact types.TaskArtifact) error {
		upsertedArtifacts = append(upsertedArtifacts, artifact)
		return nil
	}
	var eventTypes []string
	spec.EmitRunEvent = func(eventType string, _ map[string]any) {
		eventTypes = append(eventTypes, eventType)
	}
	conversation := newAgentLoopConversation(spec)
	runState := newAgentLoopRunState(spec, 4)

	turn, failed, err := loop.runLLMTurn(context.Background(), spec, &conversation, runState, agentToolDefinitions(), 1, time.Now().UTC())
	if err != nil {
		t.Fatalf("runLLMTurn error = %v", err)
	}
	if failed != nil {
		t.Fatalf("runLLMTurn failed result = %+v, want nil", failed)
	}
	if turn.Assistant.Content != "I will inspect." || len(turn.Assistant.ToolCalls) != 1 {
		t.Fatalf("assistant = %+v, want content plus one tool call", turn.Assistant)
	}
	if turn.ThinkingStep.Index != 1 || turn.ThinkingStep.Status != "completed" {
		t.Fatalf("thinking step = %+v, want completed index 1", turn.ThinkingStep)
	}
	if runState.NextStepIndex() != 2 || len(runState.Steps()) != 1 {
		t.Fatalf("run state steps = next %d steps %+v, want one recorded step and next index 2", runState.NextStepIndex(), runState.Steps())
	}
	result := runState.Result("completed")
	assertResolvedRoute(t, result)
	if result.CostMicrosUSD != 42 || len(result.TurnCosts) != 1 || result.TurnCosts[0].StepID != turn.ThinkingStep.ID {
		t.Fatalf("accounting = cost %d turn costs %+v, want cost 42 tied to thinking step", result.CostMicrosUSD, result.TurnCosts)
	}
	messages := conversation.Messages()
	if messages[len(messages)-1].Role != "assistant" || messages[len(messages)-1].ToolCalls[0].ID != "call-1" {
		t.Fatalf("conversation tail = %+v, want assistant tool call", messages[len(messages)-1])
	}
	if len(upsertedArtifacts) != 1 || upsertedArtifacts[0].Kind != "agent_conversation" || !strings.Contains(upsertedArtifacts[0].ContentText, "call-1") {
		t.Fatalf("upserted artifacts = %+v, want conversation snapshot containing call-1", upsertedArtifacts)
	}
	if len(llm.lastReqs) != 1 || llm.lastReqs[0].Scope.ProviderHint != "ollama" || len(llm.lastReqs[0].Tools) == 0 {
		t.Fatalf("LLM request = %+v, want provider hint and tools", llm.lastReqs)
	}
	assertEventTypes(t, eventTypes, "turn.started", "assistant.text_complete", "assistant.tool_call_proposed")
}

func TestAgentLoopLLMTurn_EmptyResponseReturnsFailureWithRoute(t *testing.T) {
	resp := withResolvedRoute(&types.ChatResponse{Model: "ministral-3:latest"})
	llm := &scriptedLLM{responses: []*types.ChatResponse{resp}}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	conversation := newAgentLoopConversation(spec)
	runState := newAgentLoopRunState(spec, 4)

	_, failed, err := loop.runLLMTurn(context.Background(), spec, &conversation, runState, agentToolDefinitions(), 1, time.Now().UTC())
	if err != nil {
		t.Fatalf("runLLMTurn error = %v", err)
	}
	if failed == nil {
		t.Fatal("runLLMTurn failed result = nil, want failed result")
	}
	if failed.Status != "failed" || !strings.Contains(failed.LastError, "empty response") {
		t.Fatalf("failed result = %+v, want empty-response failure", failed)
	}
	assertResolvedRoute(t, failed)
	if len(failed.Steps) != 1 || failed.Steps[0].Index != 1 || failed.Steps[0].Status != "failed" {
		t.Fatalf("failure steps = %+v, want one failed model step at index 1", failed.Steps)
	}
	if countAssistantTurns(conversation.Messages()) != 0 {
		t.Fatalf("conversation = %+v, want no assistant appended on empty response", conversation.Messages())
	}
}

func TestAgentLoopLLMTurn_ErrorPreservesExistingAccounting(t *testing.T) {
	llm := &erroringLLM{err: errors.New("temporary upstream failure")}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Run.Model = "gpt-test"
	conversation := newAgentLoopConversation(spec)
	runState := newAgentLoopRunState(spec, 4)
	runState.RecordRoute(withResolvedRoute(&types.ChatResponse{}))
	runState.AccumulateCost(&types.ChatResponse{Cost: types.CostBreakdown{TotalMicrosUSD: 75}})
	runState.AddTurnCost(1, "step-prior", 75, 0)

	_, failed, err := loop.runLLMTurn(context.Background(), spec, &conversation, runState, agentToolDefinitions(), 2, time.Now().UTC())
	if err != nil {
		t.Fatalf("runLLMTurn error = %v", err)
	}
	if failed == nil {
		t.Fatal("runLLMTurn failed result = nil, want failed result")
	}
	if !strings.Contains(failed.LastError, "LLM call failed on turn 2") {
		t.Fatalf("LastError = %q, want turn-specific LLM failure", failed.LastError)
	}
	if failed.CostMicrosUSD != 75 || len(failed.TurnCosts) != 1 || failed.TurnCosts[0].StepID != "step-prior" {
		t.Fatalf("failed accounting = cost %d turn costs %+v, want prior accounting preserved", failed.CostMicrosUSD, failed.TurnCosts)
	}
	assertResolvedRoute(t, failed)
}

func TestAgentLoopLLMTurn_PinsAutoImageRouteAcrossToolTurns(t *testing.T) {
	instance := types.ProviderInstanceIdentity{ID: "runtime-image-route", Kind: types.ProviderInstanceIdentityRuntime}
	toolCall := types.ToolCall{
		ID:   "call-route",
		Type: "function",
		Function: types.ToolCallFunction{
			Name:      "shell_exec",
			Arguments: `{"command":"pwd"}`,
		},
	}
	first := makeChatResp(makeAssistantMsg("I will inspect.", toolCall))
	first.Route = types.RouteDecision{
		Provider:         "vision-a",
		ProviderKind:     "cloud",
		ProviderInstance: instance,
		Model:            "shared-vision",
	}
	second := makeChatResp(makeAssistantMsg("Done."))
	second.Route = first.Route
	llm := &scriptedLLM{responses: []*types.ChatResponse{first, second}}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Run.Provider = ""
	spec.Run.ProviderKind = ""
	// The first Auto route can normalize the requested model. Every later turn
	// that still retains the image must use the resolved model, not this stale
	// request value.
	spec.Run.Model = "requested-vision"
	spec.ChatRequirements = types.ChatRequestRequirements{
		ImageInput:         true,
		NoProviderFailover: true,
	}
	conversation := newAgentLoopConversation(spec)
	runState := newAgentLoopRunState(spec, 4)
	tools := agentToolDefinitions()

	if _, failed, err := loop.runLLMTurn(context.Background(), spec, &conversation, runState, tools, 1, time.Now().UTC()); err != nil || failed != nil {
		t.Fatalf("first runLLMTurn = failed %+v, error %v", failed, err)
	}
	if _, failed, err := loop.runLLMTurn(context.Background(), spec, &conversation, runState, tools, 2, time.Now().UTC()); err != nil || failed != nil {
		t.Fatalf("second runLLMTurn = failed %+v, error %v", failed, err)
	}
	if len(llm.lastReqs) != 2 {
		t.Fatalf("LLM requests = %d, want 2", len(llm.lastReqs))
	}
	if firstReq := llm.lastReqs[0]; firstReq.Model != "requested-vision" || firstReq.Scope.ProviderHint != "" || firstReq.Requirements.ExactProvider || firstReq.Requirements.ProviderInstance.Valid() || !firstReq.Requirements.ToolCalling {
		t.Fatalf("first Auto request = %+v, want capability-only route selection", firstReq.Requirements)
	}
	secondReq := llm.lastReqs[1]
	if secondReq.Scope.ProviderHint != "vision-a" || secondReq.Model != "shared-vision" || !secondReq.Requirements.ExactProvider || !secondReq.Requirements.NoProviderFailover || secondReq.Requirements.ProviderInstance != instance || !secondReq.Requirements.ToolCalling {
		t.Fatalf("second request hint=%q requirements=%+v, want exact first-turn provider instance", secondReq.Scope.ProviderHint, secondReq.Requirements)
	}
	result := runState.Result("completed")
	if result.Provider != "vision-a" || result.ProviderInstance != instance {
		t.Fatalf("result route = provider %q instance %+v, want vision-a/%+v", result.Provider, result.ProviderInstance, instance)
	}
}

func TestAgentLoopChatRequestLeavesUnknownToolCapabilitiesRoutableWithoutRichInput(t *testing.T) {
	spec := newAgentLoopSpec(t)
	request := agentLoopChatRequest(spec, nil, agentToolDefinitions())
	if request.Requirements.ToolCalling {
		t.Fatalf("ordinary tool request requirements = %+v, want no hard tool-capability requirement", request.Requirements)
	}

	spec.ChatRequirements.ImageInput = true
	richRequest := agentLoopChatRequest(spec, nil, agentToolDefinitions())
	if !richRequest.Requirements.ToolCalling {
		t.Fatalf("rich tool request requirements = %+v, want hard tool-capability requirement", richRequest.Requirements)
	}
}

func TestAgentLoopLLMTurn_ErrorRecordsAttemptedProviderInstance(t *testing.T) {
	instance := types.ProviderInstanceIdentity{ID: "runtime-failed-route", Kind: types.ProviderInstanceIdentityRuntime}
	llm := AgentLLMClientFunc(func(context.Context, types.ChatRequest) (*types.ChatResponse, error) {
		return &types.ChatResponse{Route: types.RouteDecision{
			Provider:         "vision-a",
			ProviderKind:     "cloud",
			ProviderInstance: instance,
			Model:            "shared-vision",
		}}, errors.New("upstream rejected request")
	})
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	runState := newAgentLoopRunState(spec, 4)
	conversation := newAgentLoopConversation(spec)

	_, failed, err := loop.runLLMTurn(context.Background(), spec, &conversation, runState, agentToolDefinitions(), 1, time.Now().UTC())
	if err != nil {
		t.Fatalf("runLLMTurn error = %v", err)
	}
	if failed == nil || failed.Provider != "vision-a" || failed.ProviderInstance != instance {
		t.Fatalf("failed result = %+v, want attempted provider instance", failed)
	}
}

func TestAgentLoopLLMTurn_StreamingErrorRecordsAttemptedProviderInstance(t *testing.T) {
	instance := types.ProviderInstanceIdentity{ID: "runtime-stream-failed-route", Kind: types.ProviderInstanceIdentityRuntime}
	llm := &streamingScriptedLLM{
		response: &types.ChatResponse{Route: types.RouteDecision{
			Provider:         "vision-a",
			ProviderKind:     "cloud",
			ProviderInstance: instance,
			Model:            "shared-vision",
		}},
		err: errors.New("upstream stream reset"),
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	runState := newAgentLoopRunState(spec, 4)
	conversation := newAgentLoopConversation(spec)

	_, failed, err := loop.runLLMTurn(context.Background(), spec, &conversation, runState, agentToolDefinitions(), 1, time.Now().UTC())
	if err != nil {
		t.Fatalf("runLLMTurn error = %v", err)
	}
	if failed == nil || failed.Provider != "vision-a" || failed.ProviderInstance != instance {
		t.Fatalf("failed result = %+v, want attempted streaming provider instance", failed)
	}
}

func assertEventTypes(t *testing.T, got []string, want ...string) {
	t.Helper()
	seen := make(map[string]bool, len(got))
	for _, eventType := range got {
		seen[eventType] = true
	}
	for _, eventType := range want {
		if !seen[eventType] {
			t.Fatalf("missing event %q in %v", eventType, got)
		}
	}
}
