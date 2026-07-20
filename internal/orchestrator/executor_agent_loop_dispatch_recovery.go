package orchestrator

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

const (
	agentToolDispatchIntentVersion = 1
	agentToolCallMaxPerBatch       = 16
	agentToolCallIDMaxBytes        = 256
	toolRecoveryLabelMaxBytes      = 128
	toolRecoveryResultMaxBytes     = 1024
)

const (
	toolDispatchIntentVersionKey = "dispatch_intent_version"
	toolDispatchCallIDKey        = "tool_call_id"
	toolDispatchDigestKey        = "tool_call_digest"
	toolDispatchModelCallKey     = "model_call_index"
	toolCallBundleDigestKey      = "tool_call_bundle_digest"
)

func buildAgentToolDispatchIntent(spec ExecutionSpec, call types.ToolCall, stepIndex, modelCall int, startedAt time.Time) types.TaskStep {
	input := map[string]any{
		toolDispatchIntentVersionKey: agentToolDispatchIntentVersion,
		toolDispatchCallIDKey:        call.ID,
		toolDispatchDigestKey:        agentToolCallDigest(call),
		"tool":                       call.Function.Name,
	}
	if modelCall > 0 {
		input[toolDispatchModelCallKey] = modelCall
	}
	return types.TaskStep{
		ID:        spec.NewID("step"),
		TaskID:    spec.Task.ID,
		RunID:     spec.Run.ID,
		Index:     stepIndex,
		Kind:      "tool",
		Title:     fmt.Sprintf("%s (dispatching)", call.Function.Name),
		Status:    "running",
		Phase:     "execution",
		ToolName:  call.Function.Name,
		Input:     input,
		StartedAt: startedAt,
		RequestID: spec.RequestID,
		TraceID:   spec.TraceID,
	}
}

func finalizeAgentToolDispatch(intent types.TaskStep, call types.ToolCall, dispatch agentLoopToolDispatchResult, dispatchErr error, finishedAt time.Time) (types.TaskStep, []types.TaskArtifact) {
	step := intent
	if dispatch.Step != nil {
		step = *dispatch.Step
		step.ID = intent.ID
		step.TaskID = intent.TaskID
		step.RunID = intent.RunID
		step.Index = intent.Index
		step.StartedAt = intent.StartedAt
		step.RequestID = firstNonEmpty(step.RequestID, intent.RequestID)
		step.TraceID = firstNonEmpty(step.TraceID, intent.TraceID)
	}
	step.Kind = "tool"
	step.ToolName = call.Function.Name
	if step.Title == "" {
		step.Title = fmt.Sprintf("%s (%s)", call.Function.Name, firstNonEmpty(step.Status, "failed"))
	}
	if step.Status == "" || step.Status == "running" {
		step.Status = "failed"
	}
	if dispatch.Step == nil {
		step.Title = fmt.Sprintf("%s (failed)", call.Function.Name)
		step.Status = "failed"
		step.Phase = "execution"
		step.ErrorKind = "tool_dispatch_unavailable"
		step.Error = truncateUTF8(strings.TrimSpace(dispatch.Text), toolRecoveryResultMaxBytes)
	}
	if dispatchErr != nil {
		step.Status = "failed"
		step.Result = telemetry.ResultError
		if step.ErrorKind == "" {
			step.ErrorKind = "tool_dispatch_error"
		}
		if step.Error == "" {
			step.Error = "tool dispatcher returned an internal error"
		}
	}
	if step.Result == "" {
		step.Result = resultFromStatus(step.Status)
	}
	if step.FinishedAt.IsZero() {
		step.FinishedAt = finishedAt
	}
	step.Input = mergeToolDispatchBinding(step.Input, intent.Input)
	if step.OutputSummary == nil {
		step.OutputSummary = make(map[string]any)
	}
	step.OutputSummary["dispatch_intent_settled"] = true

	artifacts := append([]types.TaskArtifact(nil), dispatch.Artifacts...)
	for index := range artifacts {
		artifacts[index].StepID = step.ID
	}
	return step, artifacts
}

func mergeToolDispatchBinding(input, binding map[string]any) map[string]any {
	merged := make(map[string]any, len(input)+len(binding))
	for key, value := range input {
		merged[key] = value
	}
	for _, key := range []string{
		toolDispatchIntentVersionKey,
		toolDispatchCallIDKey,
		toolDispatchDigestKey,
		toolDispatchModelCallKey,
	} {
		if value, ok := binding[key]; ok {
			merged[key] = value
		}
	}
	if _, exists := merged["tool"]; !exists {
		merged["tool"] = binding["tool"]
	}
	return merged
}

func agentToolCallDigest(call types.ToolCall) string {
	hash := sha256.New()
	writeAgentToolCallDigest(hash, call)
	return hex.EncodeToString(hash.Sum(nil))
}

func agentToolCallBundleDigest(calls []types.ToolCall) string {
	digest := sha256.New()
	var count [8]byte
	binary.BigEndian.PutUint64(count[:], uint64(len(calls)))
	_, _ = digest.Write(count[:])
	for _, call := range calls {
		writeAgentToolCallDigest(digest, call)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func writeAgentToolCallDigest(digest hash.Hash, call types.ToolCall) {
	writeAgentToolCallDigestField(digest, call.ID)
	writeAgentToolCallDigestField(digest, call.Type)
	writeAgentToolCallDigestField(digest, call.Function.Name)
	writeAgentToolCallDigestField(digest, call.Function.Arguments)
}

func writeAgentToolCallDigestField(digest hash.Hash, value string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = digest.Write(size[:])
	_, _ = digest.Write([]byte(value))
}

func validateAgentToolCallBatch(calls []types.ToolCall) error {
	if len(calls) > agentToolCallMaxPerBatch {
		return fmt.Errorf("assistant tool-call bundle contains %d calls; maximum is %d", len(calls), agentToolCallMaxPerBatch)
	}
	seen := make(map[string]struct{}, len(calls))
	for index, call := range calls {
		callID := call.ID
		if strings.TrimSpace(callID) == "" {
			return fmt.Errorf("assistant tool call %d has an empty id", index+1)
		}
		if len(callID) > agentToolCallIDMaxBytes {
			return fmt.Errorf("assistant tool call %d id exceeds %d bytes", index+1, agentToolCallIDMaxBytes)
		}
		if _, duplicate := seen[callID]; duplicate {
			return fmt.Errorf("assistant tool call %d duplicates id %q", index+1, truncateUTF8(callID, toolRecoveryLabelMaxBytes))
		}
		seen[callID] = struct{}{}
	}
	return nil
}

func durableToolDispatchSteps(steps []types.TaskStep) []types.TaskStep {
	durable := make([]types.TaskStep, 0)
	for _, step := range steps {
		if !isDurableToolDispatchStep(step) {
			continue
		}
		durable = append(durable, step)
	}
	return durable
}

func isDurableToolDispatchStep(step types.TaskStep) bool {
	if step.Kind != "tool" || intField(step.Input[toolDispatchIntentVersionKey]) != agentToolDispatchIntentVersion {
		return false
	}
	callID, _ := step.Input[toolDispatchCallIDKey].(string)
	digest, _ := step.Input[toolDispatchDigestKey].(string)
	return callID != "" && len(digest) == sha256.Size*2
}

func findDurableToolDispatch(steps []types.TaskStep, call types.ToolCall, modelCall int) (types.TaskStep, bool, bool) {
	wantDigest := agentToolCallDigest(call)
	var mismatch types.TaskStep
	var exact types.TaskStep
	for _, step := range steps {
		if !isDurableToolDispatchStep(step) {
			continue
		}
		callID, _ := step.Input[toolDispatchCallIDKey].(string)
		if callID != call.ID || intField(step.Input[toolDispatchModelCallKey]) != modelCall {
			continue
		}
		digest, _ := step.Input[toolDispatchDigestKey].(string)
		if digest != wantDigest {
			if step.Index >= mismatch.Index {
				mismatch = step
			}
			continue
		}
		if step.Index >= exact.Index {
			exact = step
		}
	}
	if exact.ID != "" {
		return exact, true, false
	}
	if mismatch.ID != "" {
		return mismatch, true, true
	}
	return types.TaskStep{}, false, false
}

func (e *AgentLoopExecutor) recoverDurableToolDispatches(spec ExecutionSpec, conversation *agentLoopConversation, runState *agentLoopRunState, pending []types.ToolCall) ([]types.ToolCall, error) {
	checkpoint := spec.ResumeCheckpoint
	if checkpoint == nil || len(pending) == 0 || len(checkpoint.ToolDispatchSteps) == 0 {
		return pending, nil
	}
	evidenceModelCall := checkpoint.ToolDispatchModelCallIndex
	if checkpoint.SameRun && evidenceModelCall == 0 {
		evidenceModelCall = runState.ModelCallCount()
	}
	type recoveryMatch struct {
		call     types.ToolCall
		step     types.TaskStep
		mismatch bool
	}
	matches := make([]recoveryMatch, 0, len(pending))
	for _, call := range pending {
		step, found, mismatch := findDurableToolDispatch(checkpoint.ToolDispatchSteps, call, evidenceModelCall)
		if !found {
			continue
		}
		matches = append(matches, recoveryMatch{call: call, step: step, mismatch: mismatch})
	}
	if len(matches) == 0 {
		return pending, nil
	}
	if !checkpoint.SameRun {
		// Materialize every source evidence match in the current Run before
		// checkpointing any synthetic result. If this worker crashes midway
		// through a multi-call batch, own-Run recovery then retains fail-closed
		// evidence for the entire unresolved suffix instead of falling back to
		// dispatch once the current conversation artifact exists.
		for _, match := range matches {
			step := buildInheritedToolDispatchRecoveryStep(spec, runState.NextStepIndex(), match.call, match.step, evidenceModelCall, match.mismatch, time.Now().UTC())
			if err := runState.AddStep(spec, step); err != nil {
				return nil, fmt.Errorf("record inherited tool dispatch recovery %q: %w", match.call.ID, err)
			}
		}
	}
	for _, match := range matches {
		step := match.step
		status := strings.TrimSpace(step.Status)
		if checkpoint.SameRun && (status == "" || status == "running" || status == "awaiting_approval") {
			step = settleAmbiguousToolDispatch(step, match.call, match.mismatch, time.Now().UTC())
			if err := runState.UpdateRecoveredStep(spec, step); err != nil {
				return nil, fmt.Errorf("settle ambiguous tool dispatch %q: %w", match.call.ID, err)
			}
			status = step.Status
		}
		conversation.AppendToolResult(match.call.ID, toolDispatchRecoveryResult(match.call, status, match.mismatch), true)
		if conversation.HasDeferredContinuation() {
			// Continue must insert results for the entire unresolved source batch
			// before its new user message. Persist that normalized sequence in one
			// snapshot below; a partial snapshot would make own-Run recovery lose
			// the still-deferred continuation carried by the source resume event.
			continue
		}
		when := time.Now().UTC()
		artifact, err := conversation.UpsertArtifact(spec, runState.ModelCallCount(), when)
		if err != nil {
			return nil, fmt.Errorf("checkpoint recovered tool result %q: %w", match.call.ID, err)
		}
		runState.TrackConversationArtifact(artifact)
	}
	return conversation.PendingToolCallsForResume(), nil
}

func toolDispatchSupersededByContinuationResult(call types.ToolCall) string {
	callID := truncateUTF8(strings.TrimSpace(call.ID), toolRecoveryLabelMaxBytes)
	toolName := truncateUTF8(strings.TrimSpace(call.Function.Name), toolRecoveryLabelMaxBytes)
	text := fmt.Sprintf("Hecate did not execute tool call %q (%s): a new continuation prompt superseded the unresolved source Run request before dispatch. Re-request the action explicitly if it is still needed.", callID, toolName)
	return truncateUTF8(text, toolRecoveryResultMaxBytes)
}

func buildInheritedToolDispatchRecoveryStep(spec ExecutionSpec, index int, call types.ToolCall, source types.TaskStep, sourceModelCall int, mismatch bool, when time.Time) types.TaskStep {
	input := map[string]any{
		toolDispatchIntentVersionKey: agentToolDispatchIntentVersion,
		toolDispatchCallIDKey:        call.ID,
		toolDispatchDigestKey:        agentToolCallDigest(call),
		"tool":                       call.Function.Name,
		"source_run_id":              source.RunID,
		"source_step_id":             source.ID,
		"source_step_status":         source.Status,
	}
	if sourceModelCall > 0 {
		input["source_model_call_index"] = sourceModelCall
	}
	errorMessage := "source Run dispatch may have produced side effects before its exact result was checkpointed; automatic replay was denied"
	if mismatch {
		errorMessage = "tool-call identity changed after the source Run dispatch intent; automatic replay was denied"
	}
	return types.TaskStep{
		ID:        spec.NewID("step"),
		TaskID:    spec.Task.ID,
		RunID:     spec.Run.ID,
		Index:     index,
		Kind:      "tool",
		Title:     fmt.Sprintf("%s (source outcome unknown)", call.Function.Name),
		Status:    "failed",
		Phase:     "recovery",
		Result:    telemetry.ResultError,
		ToolName:  call.Function.Name,
		Input:     input,
		ErrorKind: "tool_dispatch_outcome_unknown",
		Error:     errorMessage,
		OutputSummary: map[string]any{
			"recovery_result_missing":   true,
			"automatic_replay_denied":   true,
			"inherited_source_evidence": true,
		},
		StartedAt:  when,
		FinishedAt: when,
		RequestID:  spec.RequestID,
		TraceID:    spec.TraceID,
	}
}

func settleAmbiguousToolDispatch(step types.TaskStep, call types.ToolCall, mismatch bool, when time.Time) types.TaskStep {
	step.Status = "failed"
	step.Phase = "recovery"
	step.Result = telemetry.ResultError
	step.Title = fmt.Sprintf("%s (outcome unknown)", call.Function.Name)
	step.ErrorKind = "tool_dispatch_outcome_unknown"
	step.Error = "tool dispatch may have produced side effects before interruption; automatic replay was denied"
	if mismatch {
		step.Error = "tool-call identity changed after dispatch intent; automatic replay was denied"
	}
	step.FinishedAt = when
	if step.OutputSummary == nil {
		step.OutputSummary = make(map[string]any)
	}
	step.OutputSummary["recovery_result_missing"] = true
	step.OutputSummary["automatic_replay_denied"] = true
	return step
}

func toolDispatchRecoveryResult(call types.ToolCall, status string, mismatch bool) string {
	callID := truncateUTF8(strings.TrimSpace(call.ID), toolRecoveryLabelMaxBytes)
	toolName := truncateUTF8(strings.TrimSpace(call.Function.Name), toolRecoveryLabelMaxBytes)
	var detail string
	switch {
	case mismatch:
		detail = "a durable dispatch record used this call ID with different arguments"
	case status == "completed":
		detail = "the durable dispatch Step completed, but its exact result was not checkpointed in the conversation"
	case status == "failed" || status == "cancelled":
		detail = "the durable dispatch Step ended without an exact checkpointed conversation result"
	default:
		detail = "the prior worker began dispatch, but its exact outcome was not checkpointed"
	}
	text := fmt.Sprintf("Hecate recovery did not replay tool call %q (%s): %s. The call may have produced side effects; inspect the recorded Step, artifacts, workspace, and external state before explicitly retrying.", callID, toolName, detail)
	return truncateUTF8(text, toolRecoveryResultMaxBytes)
}
