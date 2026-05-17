package orchestrator

import (
	"context"
	"strings"
	"time"

	"github.com/hecate/agent-runtime/internal/telemetry"
	"github.com/hecate/agent-runtime/pkg/types"
)

func (e *AgentLoopExecutor) chatTurn(ctx context.Context, spec ExecutionSpec, conversationArtifactID string, messages []types.Message, turn int, req types.ChatRequest) (*types.ChatResponse, error) {
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
		_, persistErr = upsertConversationArtifact(spec, conversationArtifactID, partial, turn, now)
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
		return nil, err
	}
	if persistErr != nil {
		return nil, persistErr
	}
	return resp, nil
}

func (e *AgentLoopExecutor) runWithoutLLM(_ context.Context, spec ExecutionSpec) (*ExecutionResult, error) {
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
	return &ExecutionResult{
		Status:            "failed",
		Steps:             []types.TaskStep{step},
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
