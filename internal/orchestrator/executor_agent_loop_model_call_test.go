package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/pkg/types"
)

func TestAgentLoopModelCall_SuccessRecordsStateAndConversation(t *testing.T) {
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

	modelCall, failed, err := loop.runModelCall(context.Background(), spec, &conversation, runState, agentToolDefinitions(), 1, time.Now().UTC())
	if err != nil {
		t.Fatalf("runModelCall error = %v", err)
	}
	if failed != nil {
		t.Fatalf("runModelCall failed result = %+v, want nil", failed)
	}
	if modelCall.Assistant.Content != "I will inspect." || len(modelCall.Assistant.ToolCalls) != 1 {
		t.Fatalf("assistant = %+v, want content plus one tool call", modelCall.Assistant)
	}
	if modelCall.ThinkingStep.Index != 1 || modelCall.ThinkingStep.Status != "completed" {
		t.Fatalf("thinking step = %+v, want completed index 1", modelCall.ThinkingStep)
	}
	if runState.NextStepIndex() != 2 || len(runState.Steps()) != 1 {
		t.Fatalf("run state steps = next %d steps %+v, want one recorded step and next index 2", runState.NextStepIndex(), runState.Steps())
	}
	result := runState.Result("completed")
	assertResolvedRoute(t, result)
	if result.CostMicrosUSD != 42 || len(result.ModelCallCosts) != 1 || result.ModelCallCosts[0].StepID != modelCall.ThinkingStep.ID {
		t.Fatalf("accounting = cost %d model-call costs %+v, want cost 42 tied to thinking step", result.CostMicrosUSD, result.ModelCallCosts)
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
	assertEventTypes(t, eventTypes, "model.call.started", "assistant.text_complete", "assistant.tool_call_proposed")
}

func TestAgentLoopSameRunRecoveryFinishesSavedAssistantWithoutAnotherModelCall(t *testing.T) {
	llm := &scriptedLLM{}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.ResumeCheckpoint = &ResumeCheckpoint{
		SourceRunID:           spec.Run.ID,
		SameRun:               true,
		LastCompletedStepID:   "step-model-1",
		ThisRunModelCallCount: 1,
		AgentConversation:     []byte(`[{"role":"user","content":"answer"},{"role":"assistant","content":"saved final"}]`),
	}

	result, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.Status != "completed" || result.ModelCallCount != 1 {
		t.Fatalf("result = %+v, want completed with one recovered model call", result)
	}
	if got := llm.calls.Load(); got != 0 {
		t.Fatalf("provider calls = %d, want 0 during saved-final recovery", got)
	}
	if artifact := findArtifactByKind(result.Artifacts, "summary"); artifact == nil || artifact.ContentText != "saved final" {
		t.Fatalf("summary artifact = %+v, want saved final answer", artifact)
	}
}

func TestAgentLoopSameRunRecoveryUpsertsExistingFinalAnswerArtifact(t *testing.T) {
	llm := &scriptedLLM{responses: []*types.ChatResponse{
		makeChatResp(makeAssistantMsg("saved final")),
	}}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	steps := make(map[string]types.TaskStep)
	artifacts := make(map[string]types.TaskArtifact)
	spec.UpsertStep = func(step types.TaskStep) error {
		steps[step.ID] = step
		return nil
	}
	spec.UpsertArtifact = func(artifact types.TaskArtifact) error {
		artifacts[artifact.ID] = artifact
		return nil
	}

	first, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("first Execute error = %v", err)
	}
	if first.Status != "completed" || len(first.Steps) != 1 {
		t.Fatalf("first result = %+v, want one completed model call", first)
	}
	conversation, ok := artifacts["convo-"+spec.Run.ID]
	if !ok {
		t.Fatalf("durable artifacts = %+v, want conversation checkpoint", artifacts)
	}
	finalID := agentLoopFinalAnswerArtifactID(spec.Run.ID)
	if _, ok := artifacts[finalID]; !ok {
		t.Fatalf("durable artifacts = %+v, want final answer %q", artifacts, finalID)
	}

	// Simulate a crash after the final-answer upsert but before the Run's
	// terminal transition. Recovery must overwrite the same summary identity.
	spec.ResumeCheckpoint = &ResumeCheckpoint{
		SourceRunID:           spec.Run.ID,
		SameRun:               true,
		LastCompletedStepID:   first.Steps[0].ID,
		LastStepIndex:         first.Steps[0].Index,
		ThisRunModelCallCount: 1,
		AgentConversation:     []byte(conversation.ContentText),
	}
	recovered, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("recovery Execute error = %v", err)
	}
	if recovered.Status != "completed" {
		t.Fatalf("recovery result = %+v, want completed", recovered)
	}
	if got := llm.calls.Load(); got != 1 {
		t.Fatalf("provider calls = %d, want only the pre-crash call", got)
	}
	summaries := 0
	for _, artifact := range artifacts {
		if artifact.Kind == "summary" {
			summaries++
			if artifact.ID != finalID || artifact.ContentText != "saved final" {
				t.Fatalf("summary artifact = %+v, want idempotent final answer %q", artifact, finalID)
			}
		}
	}
	if summaries != 1 {
		t.Fatalf("summary artifacts = %d in %+v, want exactly one after recovery", summaries, artifacts)
	}
}

func TestAgentLoopSameRunRecoveryDoesNotFinishInheritedAssistantWithoutCompletedModelCall(t *testing.T) {
	llm := &scriptedLLM{responses: []*types.ChatResponse{
		makeChatResp(makeAssistantMsg("fresh answer from this Run")),
	}}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.ResumeCheckpoint = &ResumeCheckpoint{
		SourceRunID:           spec.Run.ID,
		SameRun:               true,
		ThisRunModelCallCount: 0,
		AgentConversation:     []byte(`[{"role":"user","content":"earlier prompt"},{"role":"assistant","content":"inherited answer"}]`),
	}

	result, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.Status != "completed" || result.ModelCallCount != 1 {
		t.Fatalf("result = %+v, want a fresh completed model call", result)
	}
	if got := llm.calls.Load(); got != 1 {
		t.Fatalf("provider calls = %d, want 1 because the saved tail is inherited", got)
	}
	if artifact := findArtifactByKind(result.Artifacts, "summary"); artifact == nil || artifact.ContentText != "fresh answer from this Run" {
		t.Fatalf("summary artifact = %+v, want fresh Run-local answer", artifact)
	}
}

func TestAgentLoopModelCall_EmptyResponseReturnsFailureWithRoute(t *testing.T) {
	resp := withResolvedRoute(&types.ChatResponse{Model: "ministral-3:latest"})
	llm := &scriptedLLM{responses: []*types.ChatResponse{resp}}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	conversation := newAgentLoopConversation(spec)
	runState := newAgentLoopRunState(spec, 4)

	_, failed, err := loop.runModelCall(context.Background(), spec, &conversation, runState, agentToolDefinitions(), 1, time.Now().UTC())
	if err != nil {
		t.Fatalf("runModelCall error = %v", err)
	}
	if failed == nil {
		t.Fatal("runModelCall failed result = nil, want failed result")
	}
	if failed.Status != "failed" || !strings.Contains(failed.LastError, "empty response") {
		t.Fatalf("failed result = %+v, want empty-response failure", failed)
	}
	assertResolvedRoute(t, failed)
	if len(failed.Steps) != 1 || failed.Steps[0].Index != 1 || failed.Steps[0].Status != "failed" {
		t.Fatalf("failure steps = %+v, want one failed model step at index 1", failed.Steps)
	}
	if countAssistantMessages(conversation.Messages()) != 0 {
		t.Fatalf("conversation = %+v, want no assistant appended on empty response", conversation.Messages())
	}
}

func TestAgentLoopModelCall_ErrorPreservesExistingAccounting(t *testing.T) {
	llm := &erroringLLM{err: errors.New("temporary upstream failure")}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Run.Model = "gpt-test"
	conversation := newAgentLoopConversation(spec)
	runState := newAgentLoopRunState(spec, 4)
	runState.RecordRoute(withResolvedRoute(&types.ChatResponse{}))
	runState.AccumulateCost(&types.ChatResponse{Cost: types.CostBreakdown{TotalMicrosUSD: 75}})
	runState.AddModelCallCost(1, "step-prior", 75, 0)

	_, failed, err := loop.runModelCall(context.Background(), spec, &conversation, runState, agentToolDefinitions(), 2, time.Now().UTC())
	if err != nil {
		t.Fatalf("runModelCall error = %v", err)
	}
	if failed == nil {
		t.Fatal("runModelCall failed result = nil, want failed result")
	}
	if !strings.Contains(failed.LastError, "model call 2 failed") {
		t.Fatalf("LastError = %q, want model-call-specific LLM failure", failed.LastError)
	}
	if failed.CostMicrosUSD != 75 || len(failed.ModelCallCosts) != 1 || failed.ModelCallCosts[0].StepID != "step-prior" {
		t.Fatalf("failed accounting = cost %d model-call costs %+v, want prior accounting preserved", failed.CostMicrosUSD, failed.ModelCallCosts)
	}
	assertResolvedRoute(t, failed)
}

func TestBuildApprovalResumeStepIsNotAnotherModelCall(t *testing.T) {
	spec := newAgentLoopSpec(t)
	step := buildApprovalResumeStep(spec, 3, 1, time.Now().UTC(), makeAssistantMsg("", types.ToolCall{
		ID:   "call-approved",
		Type: "function",
		Function: types.ToolCallFunction{
			Name:      "shell_exec",
			Arguments: `{"command":"pwd"}`,
		},
	}), true)

	if step.Kind != "control" || step.ToolName != "builtin.agent_loop_resume" {
		t.Fatalf("approval-resume step = kind %q tool %q, want control marker", step.Kind, step.ToolName)
	}
	if got := step.Input["model_call_index"]; got != 1 {
		t.Fatalf("approval-resume model_call_index = %v, want existing model call 1", got)
	}
	if _, ok := step.Input["model_call"]; ok {
		t.Fatalf("legacy model_call unexpectedly present: %#v", step.Input)
	}
	if !strings.Contains(step.Title, "Dispatch approved tools") {
		t.Fatalf("approval-resume title = %q, want approved-tool dispatch", step.Title)
	}
}

func TestBuildApprovalResumeStepLabelsUngatedCrashRecovery(t *testing.T) {
	spec := newAgentLoopSpec(t)
	step := buildApprovalResumeStep(spec, 4, 2, time.Now().UTC(), makeAssistantMsg("", types.ToolCall{
		ID:   "call-recovered",
		Type: "function",
		Function: types.ToolCallFunction{
			Name:      "read_file",
			Arguments: `{"path":"README.md"}`,
		},
	}), false)

	if !strings.Contains(step.Title, "Recover pending tools") || strings.Contains(strings.ToLower(step.Title), "approved") {
		t.Fatalf("recovery step title = %q, want non-approval recovery label", step.Title)
	}
	if _, ok := step.Input["approved_tools"]; ok {
		t.Fatalf("ungated recovery input claims approval: %#v", step.Input)
	}
	if _, ok := step.Input["recovered_tools"]; !ok {
		t.Fatalf("ungated recovery input = %#v, want recovered_tools", step.Input)
	}
}

func TestAgentLoopModelCall_PinsAutoImageRouteAcrossToolCalls(t *testing.T) {
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
	// The first Auto route can normalize the requested model. Every later model call
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

	if _, failed, err := loop.runModelCall(context.Background(), spec, &conversation, runState, tools, 1, time.Now().UTC()); err != nil || failed != nil {
		t.Fatalf("first runModelCall = failed %+v, error %v", failed, err)
	}
	if _, failed, err := loop.runModelCall(context.Background(), spec, &conversation, runState, tools, 2, time.Now().UTC()); err != nil || failed != nil {
		t.Fatalf("second runModelCall = failed %+v, error %v", failed, err)
	}
	if len(llm.lastReqs) != 2 {
		t.Fatalf("LLM requests = %d, want 2", len(llm.lastReqs))
	}
	if firstReq := llm.lastReqs[0]; firstReq.Model != "requested-vision" || firstReq.Scope.ProviderHint != "" || firstReq.Requirements.ExactProvider || firstReq.Requirements.ProviderInstance.Valid() || !firstReq.Requirements.ToolCalling {
		t.Fatalf("first Auto request = %+v, want capability-only route selection", firstReq.Requirements)
	}
	secondReq := llm.lastReqs[1]
	if secondReq.Scope.ProviderHint != "vision-a" || secondReq.Model != "shared-vision" || !secondReq.Requirements.ExactProvider || !secondReq.Requirements.NoProviderFailover || secondReq.Requirements.ProviderInstance != instance || !secondReq.Requirements.ToolCalling {
		t.Fatalf("second request hint=%q requirements=%+v, want exact first-call provider instance", secondReq.Scope.ProviderHint, secondReq.Requirements)
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

	fence := types.ToolCallingVerificationFence{
		Provider:         "local-runtime",
		Model:            spec.Run.Model,
		ProviderInstance: types.ProviderInstanceIdentity{ID: "verified-generation", Kind: types.ProviderInstanceIdentityConfiguration},
		ExpiresAt:        time.Now().UTC().Add(time.Hour),
	}
	spec.Run.Provider = fence.Provider
	spec.ChatRequirements = fence.ToolCallingRequirements()
	verifiedRequest := agentLoopChatRequest(spec, nil, agentToolDefinitions())
	if verifiedRequest.Requirements.ImageInput || !verifiedRequest.Requirements.ToolCalling || !verifiedRequest.Requirements.ToolCallingVerified ||
		!verifiedRequest.Requirements.NoProviderFailover || !verifiedRequest.Requirements.ExactProvider || verifiedRequest.Requirements.ProviderInstance != fence.ProviderInstance ||
		verifiedRequest.Requirements.ToolCallingVerifiedModel != fence.Model || verifiedRequest.Scope.ProviderHint != fence.Provider {
		t.Fatalf("verified plain tool request = %+v scope=%+v, want durable exact tool-proof fence without image", verifiedRequest.Requirements, verifiedRequest.Scope)
	}
}

func TestAgentLoopModelCall_ErrorRecordsAttemptedProviderInstance(t *testing.T) {
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

	_, failed, err := loop.runModelCall(context.Background(), spec, &conversation, runState, agentToolDefinitions(), 1, time.Now().UTC())
	if err != nil {
		t.Fatalf("runModelCall error = %v", err)
	}
	if failed == nil || failed.Provider != "vision-a" || failed.ProviderInstance != instance {
		t.Fatalf("failed result = %+v, want attempted provider instance", failed)
	}
}

func TestAgentLoopModelCall_StreamingErrorRecordsAttemptedProviderInstance(t *testing.T) {
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

	_, failed, err := loop.runModelCall(context.Background(), spec, &conversation, runState, agentToolDefinitions(), 1, time.Now().UTC())
	if err != nil {
		t.Fatalf("runModelCall error = %v", err)
	}
	if failed == nil || failed.Provider != "vision-a" || failed.ProviderInstance != instance {
		t.Fatalf("failed result = %+v, want attempted streaming provider instance", failed)
	}
}

func TestAgentLoopModelCall_StreamingFailureRestoresCompletedRunLocalConversation(t *testing.T) {
	saved := []types.Message{
		{Role: "user", Content: "first prompt"},
		{Role: "assistant", Content: "inherited answer"},
		{Role: "user", Content: "continue"},
		{Role: "assistant", ToolCalls: []types.ToolCall{{ID: "current-1", Type: "function", Function: types.ToolCallFunction{Name: "shell_exec"}}}},
		{Role: "tool", ToolCallID: "current-1", Content: "result"},
	}
	savedJSON, err := json.Marshal(saved)
	if err != nil {
		t.Fatalf("marshal saved conversation: %v", err)
	}
	llm := &streamingScriptedLLM{
		chunks: []string{"partial failed response"},
		err:    errors.New("stream reset"),
	}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.ResumeCheckpoint = &ResumeCheckpoint{
		SourceRunID:           spec.Run.ID,
		AgentConversation:     savedJSON,
		ThisRunModelCallCount: 1,
	}
	var upserts []types.TaskArtifact
	spec.UpsertArtifact = func(artifact types.TaskArtifact) error {
		if artifact.Kind == "agent_conversation" {
			if artifact.Status != "streaming" {
				return errors.New("execution context is cancelled")
			}
			upserts = append(upserts, artifact)
		}
		return nil
	}
	spec.RepairArtifact = func(artifact types.TaskArtifact) error {
		if artifact.Kind == "agent_conversation" {
			upserts = append(upserts, artifact)
		}
		return nil
	}
	runState := newAgentLoopRunState(spec, 4)
	conversation := newAgentLoopConversation(spec)

	_, failed, err := loop.runModelCall(context.Background(), spec, &conversation, runState, agentToolDefinitions(), 2, time.Now().UTC())
	if err != nil {
		t.Fatalf("runModelCall error = %v", err)
	}
	if failed == nil || runState.ModelCallCount() != 1 {
		t.Fatalf("failed = %+v model_call_count = %d, want failed with one completed call", failed, runState.ModelCallCount())
	}
	if len(upserts) < 2 {
		t.Fatalf("conversation upserts = %d, want partial then restored baseline", len(upserts))
	}
	if upserts[0].Status != "streaming" || upserts[len(upserts)-1].Status != "ready" {
		t.Fatalf("conversation statuses = %q ... %q, want streaming then ready", upserts[0].Status, upserts[len(upserts)-1].Status)
	}
	var restored []types.Message
	if err := json.Unmarshal([]byte(upserts[len(upserts)-1].ContentText), &restored); err != nil {
		t.Fatalf("decode restored conversation: %v", err)
	}
	if countAssistantMessages(restored) != 2 || strings.Contains(upserts[len(upserts)-1].ContentText, "partial failed response") {
		t.Fatalf("restored conversation = %s, want inherited + completed current response without failed partial", upserts[len(upserts)-1].ContentText)
	}
	truncated, err := truncateConversationToRunModelCall(restored, 1, 1)
	if err != nil {
		t.Fatalf("truncate restored conversation: %v", err)
	}
	if len(truncated) != 3 || truncated[1].Content != "inherited answer" || truncated[2].Content != "continue" {
		t.Fatalf("Run-local retry context = %#v, want inherited response plus continuation prompt", truncated)
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
