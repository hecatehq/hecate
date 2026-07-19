package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/providerdispatch"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

type agentLoopModelCall struct {
	Assistant    types.Message
	ThinkingStep types.TaskStep
}

func (e *AgentLoopExecutor) runModelCall(ctx context.Context, spec ExecutionSpec, conversation *agentLoopConversation, runState *agentLoopRunState, tools []types.Tool, modelCall int, startedAt time.Time) (agentLoopModelCall, *ExecutionResult, error) {
	messages := conversation.Messages()
	req := agentLoopChatRequest(spec, messages, tools)
	runState.fenceProviderBoundRequest(&req)
	emitAgentModelCallStarted(spec, modelCall, req)

	modelCallCtx := providerdispatch.WithAttemptRecorder(ctx, spec.RecordProviderAttempt)
	resp, err := e.performModelCall(modelCallCtx, spec, conversation.ArtifactID(), messages, modelCall, req)
	runState.RecordRoute(resp)
	restoreCompletedConversation := func(cause error) error {
		repairSpec := spec
		if repairSpec.RepairArtifact != nil {
			repairSpec.UpsertArtifact = repairSpec.RepairArtifact
		}
		artifact, restoreErr := conversation.UpsertArtifact(repairSpec, modelCall-1, time.Now().UTC())
		if artifact != nil {
			runState.TrackInitialConversationArtifact(artifact)
		}
		if restoreErr != nil {
			return errors.Join(cause, fmt.Errorf("restore completed conversation after failed model call: %w", restoreErr))
		}
		return cause
	}
	if err != nil {
		err = restoreCompletedConversation(err)
		failed, ferr := e.failedFromError(spec, runState.Steps(), runState.Artifacts(), runState.NextStepIndex(), startedAt, modelCallErrorMessage(spec, modelCall, len(tools) > 0, err))
		return agentLoopModelCall{}, runState.attachAccounting(failed), ferr
	}
	if resp == nil || len(resp.Choices) == 0 {
		emptyErr := restoreCompletedConversation(fmt.Errorf("LLM returned empty response on model call %d", modelCall))
		failed, ferr := e.failedFromError(spec, runState.Steps(), runState.Artifacts(), runState.NextStepIndex(), startedAt,
			emptyErr.Error())
		return agentLoopModelCall{}, runState.attachAccounting(failed), ferr
	}

	assistantMsg := resp.Choices[0].Message
	// Browser targets intentionally exclude query strings and fragments. Strip
	// any invalid raw arguments before the assistant message is emitted or
	// checkpointed, where a query could otherwise be retained as operator data.
	assistantMsg = sanitizeBrowserInspectionToolCalls(assistantMsg)
	emitAssistantMessageEvents(spec, modelCall, assistantMsg)

	modelCallCost := runState.AccumulateCost(resp)
	thinkingStep := buildThinkingStep(spec, runState.NextStepIndex(), modelCall, startedAt, assistantMsg, resp, runState.CostSpent())
	if err := runState.AddStep(spec, thinkingStep); err != nil {
		return agentLoopModelCall{}, nil, err
	}
	runState.AddModelCallCost(modelCall, thinkingStep.ID, modelCallCost, len(assistantMsg.ToolCalls))

	conversation.AppendAssistant(assistantMsg)
	if art, err := conversation.UpsertArtifact(spec, modelCall, startedAt); err != nil {
		return agentLoopModelCall{}, nil, err
	} else {
		runState.TrackInitialConversationArtifact(art)
	}

	return agentLoopModelCall{
		Assistant:    assistantMsg,
		ThinkingStep: thinkingStep,
	}, nil, nil
}

func agentLoopChatRequest(spec ExecutionSpec, messages []types.Message, tools []types.Tool) types.ChatRequest {
	// ProviderHint carries the operator's pinned provider from
	// task.RequestedProvider (mirrored to run.Provider at run-create
	// time). Without it the router falls back to its default — which
	// historically picked OpenAI for generic model ids and surfaced
	// as "api key is required for cloud provider openai" when the
	// operator had only configured a local provider like Ollama.
	// Empty hint preserves the existing auto-route behavior for
	// tasks that didn't specify a provider.
	requirements := spec.ChatRequirements
	// Rich input needs one candidate that is explicitly capable of both the
	// image and the tool catalog. Ordinary Task Runs retain their established
	// optimistic behavior for providers whose capability discovery is unknown;
	// the provider remains the authority for a normal tool-call rejection.
	requirements.ToolCalling = requirements.ImageInput && len(tools) > 0
	return types.ChatRequest{
		RequestID:    spec.RequestID,
		Model:        spec.Run.Model,
		Messages:     messages,
		Tools:        tools,
		Requirements: requirements,
		Scope: types.RequestScope{
			ProviderHint: spec.Run.Provider,
		},
	}
}

func modelCallErrorMessage(spec ExecutionSpec, modelCall int, toolsRequired bool, err error) string {
	message := fmt.Sprintf("model call %d failed: %v", modelCall, err)
	if !toolsRequired || !isModelLacksToolsError(err) {
		return message
	}
	return fmt.Sprintf("model call %d failed: model %q does not support tool-calling, which agent_loop requires. Pick a tool-capable model (e.g. gpt-4o-mini, claude-sonnet-4-6, qwen2.5-coder for Ollama). Underlying error: %v", modelCall, spec.Run.Model, err)
}

func (e *AgentLoopExecutor) performModelCall(ctx context.Context, spec ExecutionSpec, conversationArtifactID string, messages []types.Message, modelCall int, req types.ChatRequest) (*types.ChatResponse, error) {
	streamer, ok := e.llm.(AgentLLMStreamingClient)
	if !ok {
		return e.llm.Chat(ctx, req)
	}
	var streamed strings.Builder
	var lastPersistedLen int
	var lastPersistedAt time.Time
	var persistErr error
	persistPartial := func(force bool) {
		if persistErr != nil {
			return
		}
		content := streamed.String()
		if strings.TrimSpace(content) == "" {
			return
		}
		now := time.Now().UTC()
		if !force && len(content)-lastPersistedLen < 24 && now.Sub(lastPersistedAt) < 100*time.Millisecond {
			return
		}
		partial := make([]types.Message, 0, len(messages))
		partial = append(partial, messages...)
		partial = append(partial, types.Message{Role: "assistant", Content: content})
		_, persistErr = upsertConversationArtifact(spec, conversationArtifactID, partial, modelCall-1, "streaming", now)
		if persistErr == nil {
			lastPersistedLen = len(content)
			lastPersistedAt = now
		}
	}
	resp, err := streamer.ChatStream(ctx, req, func(delta string) {
		if delta == "" {
			return
		}
		streamed.WriteString(delta)
		persistPartial(false)
	})
	persistPartial(true)
	if err != nil {
		// Streaming providers can return the route that received an attempted
		// request alongside a transport error. Preserve it so a rich-input run
		// records the disclosure fence even though no complete reply exists.
		return resp, err
	}
	if persistErr != nil {
		return resp, persistErr
	}
	return resp, nil
}

func (e *AgentLoopExecutor) runWithoutLLM(_ context.Context, spec ExecutionSpec, initialArtifacts []types.TaskArtifact) (*ExecutionResult, error) {
	startedAt := spec.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	const errMsg = "agent_loop requires an LLM client — configure a provider and restart, or use execution_kind=shell/git/file for deterministic tasks"
	step := types.TaskStep{
		ID:         spec.NewID("step"),
		TaskID:     spec.Task.ID,
		RunID:      spec.Run.ID,
		Index:      1,
		Kind:       "model",
		Title:      "Agent loop unavailable",
		Status:     "failed",
		Phase:      "planning",
		Result:     telemetry.ResultError,
		ToolName:   "builtin.agent_loop",
		Error:      errMsg,
		StartedAt:  startedAt,
		FinishedAt: startedAt,
		RequestID:  spec.RequestID,
		TraceID:    spec.TraceID,
	}
	if err := upsertTaskStep(spec, step); err != nil {
		return nil, err
	}
	for _, artifact := range initialArtifacts {
		if err := upsertTaskArtifact(spec, artifact); err != nil {
			return nil, err
		}
	}
	return &ExecutionResult{
		Status:            "failed",
		Steps:             []types.TaskStep{step},
		Artifacts:         initialArtifacts,
		LastError:         errMsg,
		OtelStatusCode:    "error",
		OtelStatusMessage: errMsg,
	}, nil
}

// isModelLacksToolsError detects the upstream signal that the chosen
// model rejects the `tools` field. Different providers phrase it
// differently, so we match a few common substrings rather than a
// rigid status-code check. False positives just mean an extra hint
// in the error — preferable to silently leaving the operator
// puzzled by a "400 invalid_request_error" with no remedy.
func isModelLacksToolsError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	// Ollama: "<model> does not support tools"
	// OpenAI: "tools is not supported with <model>" / "<model> does not support tool calls"
	// Anthropic: "this model does not support tool use"
	// Together AI: "this model does not support function calling"
	for _, needle := range []string{
		"does not support tools",
		"does not support tool calls",
		"does not support tool use",
		"does not support function calling",
		"tools is not supported",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// failedFromError appends a synthetic "agent loop failed" step that
// carries the error message as its output. Returns a "failed"
// ExecutionResult ready for the runner.
func (e *AgentLoopExecutor) failedFromError(spec ExecutionSpec, allSteps []types.TaskStep, allArtifacts []types.TaskArtifact, stepIndex int, startedAt time.Time, msg string) (*ExecutionResult, error) {
	step := types.TaskStep{
		ID:         spec.NewID("step"),
		TaskID:     spec.Task.ID,
		RunID:      spec.Run.ID,
		Index:      stepIndex,
		Kind:       "model",
		Title:      "Agent loop failed",
		Status:     "failed",
		Phase:      "execution",
		Result:     telemetry.ResultError,
		ToolName:   "builtin.agent_loop",
		Error:      msg,
		StartedAt:  startedAt,
		FinishedAt: time.Now().UTC(),
		RequestID:  spec.RequestID,
		TraceID:    spec.TraceID,
	}
	if err := upsertTaskStep(spec, step); err != nil {
		return nil, err
	}
	allSteps = append(allSteps, step)
	return &ExecutionResult{
		Status:            "failed",
		Steps:             allSteps,
		Artifacts:         allArtifacts,
		LastError:         msg,
		OtelStatusCode:    "error",
		OtelStatusMessage: msg,
	}, nil
}
