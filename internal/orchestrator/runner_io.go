package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/gitrunner"
	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/runtimeevents"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

func (r *Runner) gitSummaryArtifact(ctx context.Context, task types.Task, run types.TaskRun, requestID, traceID string) (types.TaskArtifact, bool) {
	workspace := strings.TrimSpace(run.WorkspacePath)
	if workspace == "" {
		return types.TaskArtifact{}, false
	}
	gitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	git := gitrunner.NewLocalRunner()
	if !git.IsWorkTree(gitCtx, workspace) {
		return types.TaskArtifact{}, false
	}
	statusOut, err := git.Run(gitCtx, workspace, "status", "--porcelain=v1")
	if err != nil {
		return types.TaskArtifact{}, false
	}
	changes := parseGitPorcelainStatus(statusOut.Stdout)
	if len(changes) == 0 {
		return types.TaskArtifact{}, false
	}
	diffStat := ""
	if diffStatOut, err := git.Run(gitCtx, workspace, "diff", "--stat", "HEAD", "--"); err == nil {
		diffStat = strings.TrimSpace(diffStatOut.Stdout)
	}
	payload := gitSummaryArtifactPayload{
		WorkspacePath: workspace,
		Files:         changes,
		DiffStat:      diffStat,
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return types.TaskArtifact{}, false
	}
	createdAt := time.Now().UTC()
	return types.TaskArtifact{
		ID:          defaultResourceID("artifact"),
		TaskID:      task.ID,
		RunID:       run.ID,
		Kind:        "git_summary",
		Name:        "git-changes.json",
		Description: "Git changed-files summary captured after the run",
		MimeType:    "application/json",
		StorageKind: "inline",
		Path:        workspace,
		ContentText: string(raw),
		SizeBytes:   int64(len(raw)),
		Status:      "ready",
		CreatedAt:   createdAt,
		RequestID:   requestID,
		TraceID:     traceID,
	}, true
}

func (r *Runner) finalizeFailedRun(ctx context.Context, trace *profiler.Trace, task types.Task, run types.TaskRun, requestID, status, message string) error {
	now := time.Now().UTC()
	var failedRunDurationMS int64
	if !run.StartedAt.IsZero() {
		failedRunDurationMS = now.Sub(run.StartedAt).Milliseconds()
	}
	result, err := r.applyTerminalRunTransition(ctx, failedRunTerminalTransition(task, run, requestID, status, message, trace, now))
	if err != nil {
		return err
	}
	if result.Skipped {
		return nil
	}
	r.metrics.RecordRun(ctx, telemetry.RunMetricsRecord{
		TaskID:        task.ID,
		RunID:         run.ID,
		Status:        status,
		ExecutionKind: task.ExecutionKind,
		Model:         run.Model,
		DurationMS:    failedRunDurationMS,
	})
	return nil
}

func (r *Runner) upsertStep(ctx context.Context, step types.TaskStep) error {
	if existing, found, err := r.store.GetStep(ctx, step.RunID, step.ID); err != nil {
		return err
	} else if found {
		step.SpanID = firstNonEmpty(step.SpanID, existing.SpanID)
		step.ParentSpanID = firstNonEmpty(step.ParentSpanID, existing.ParentSpanID)
		_, err = r.store.UpdateStep(ctx, step)
		return err
	}
	_, err := r.store.AppendStep(ctx, step)
	return err
}

func (r *Runner) upsertArtifact(ctx context.Context, artifact types.TaskArtifact) error {
	if existing, found, err := r.store.GetArtifact(ctx, artifact.TaskID, artifact.ID); err != nil {
		return err
	} else if found {
		artifact.SpanID = firstNonEmpty(artifact.SpanID, existing.SpanID)
		_, err = r.store.UpdateArtifact(ctx, artifact)
		return err
	}
	_, err := r.store.CreateArtifact(ctx, artifact)
	return err
}

func (r *Runner) resumeCheckpointForRun(ctx context.Context, taskID, runID string) (*ResumeCheckpoint, error) {
	if r.store == nil {
		return nil, nil
	}
	events, err := r.store.ListRunEvents(ctx, taskID, runID, 0, 500)
	if err != nil {
		return nil, err
	}
	// Current-Run progress always wins over historical resume metadata. A Run
	// created from another Run may itself pause for approval or be requeued
	// after a worker crash; once it has written its own conversation, resuming
	// from the parent would discard completed work and corrupt Run-local call
	// numbering.
	if checkpoint, found, ownErr := r.ownRunResumeCheckpoint(ctx, taskID, runID, events); ownErr != nil {
		return nil, ownErr
	} else if found {
		return checkpoint, nil
	}
	sourceRunID := ""
	reason := ""
	appendUserPrompt := ""
	sourceModelCallIndex := 0
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if event.EventType != runtimeevents.EventRunResumedFromEvent.String() {
			continue
		}
		value, ok := event.Data["from_run_id"]
		if !ok {
			continue
		}
		candidate, _ := value.(string)
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		sourceRunID = candidate
		if rawReason, ok := event.Data["reason"]; ok {
			reason, _ = rawReason.(string)
		}
		if rawPrompt, ok := event.Data["append_user_prompt"]; ok {
			appendUserPrompt, _ = rawPrompt.(string)
		}
		// source_model_call_index is event-data JSON-decoded — depending on
		// the store it may come back as float64 (JSON-roundtripped)
		// or int. Accept both. Zero/missing means a regular resume,
		// not a retry-from-model-call.
		if raw, ok := event.Data["source_model_call_index"]; ok {
			switch v := raw.(type) {
			case int:
				sourceModelCallIndex = v
			case int64:
				sourceModelCallIndex = int(v)
			case float64:
				sourceModelCallIndex = int(v)
			}
		}
		break
	}
	if sourceRunID == "" {
		return nil, nil
	}
	sourceRun, found, err := r.store.GetRun(ctx, taskID, sourceRunID)
	if err != nil {
		return nil, fmt.Errorf("load source run for resume checkpoint: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("source run %q not found for resume checkpoint", sourceRunID)
	}
	steps, err := r.store.ListSteps(ctx, sourceRunID)
	if err != nil {
		return nil, err
	}
	sourceDispatchSteps := durableToolDispatchSteps(steps)
	sourceDispatchModelCall := sourceRun.ModelCallCount
	for _, step := range sourceDispatchSteps {
		if modelCall := intField(step.Input[toolDispatchModelCallKey]); modelCall > sourceDispatchModelCall {
			sourceDispatchModelCall = modelCall
		}
	}
	artifacts, err := r.store.ListArtifacts(ctx, taskstate.ArtifactFilter{TaskID: taskID, RunID: sourceRunID})
	if err != nil {
		return nil, err
	}
	sourceEvents, err := r.store.ListRunEvents(ctx, taskID, sourceRunID, 0, 5000)
	if err != nil {
		return nil, err
	}
	checkpoint := &ResumeCheckpoint{
		SourceRunID:                sourceRunID,
		Reason:                     strings.TrimSpace(reason),
		AppendUserPrompt:           strings.TrimSpace(appendUserPrompt),
		LastStepIndex:              0,
		ArtifactCount:              len(artifacts),
		ToolDispatchSteps:          sourceDispatchSteps,
		ToolDispatchModelCallIndex: sourceDispatchModelCall,
	}
	// Surface the new run's PriorCostMicrosUSD so the agent loop can
	// apply the per-task cost ceiling against the cumulative spend
	// across the entire resume chain. We populated this on the run
	// at create time (startTaskWithOptions) by summing the source's
	// prior + total. Re-reading from the store here keeps that
	// value the single source of truth. ThisRunCostMicrosUSD is
	// always 0 for a cross-run resume (the new run hasn't run yet).
	if currentRun, found, lookupErr := r.store.GetRun(ctx, taskID, runID); lookupErr == nil && found {
		checkpoint.PriorCostMicrosUSD = currentRun.PriorCostMicrosUSD
		checkpoint.ThisRunCostMicrosUSD = currentRun.TotalCostMicrosUSD
		checkpoint.ThisRunModelCallCount = currentRun.ModelCallCount
	}
	// Pull the agent-conversation artifact (if any) so the agent loop
	// can hydrate state on resume. We use a stable kind + ID
	// convention so the lookup is a single linear scan rather than a
	// new store method. Non-agent_loop runs simply won't have one.
	for _, art := range artifacts {
		if art.Kind == "agent_conversation" && len(art.ContentText) > 0 {
			messages, _, decodeErr := completedConversationMessages(art)
			if decodeErr != nil {
				return nil, fmt.Errorf("decode source conversation: %w", decodeErr)
			}
			payload, encodeErr := json.Marshal(messages)
			if encodeErr != nil {
				return nil, fmt.Errorf("encode source conversation: %w", encodeErr)
			}
			checkpoint.AgentConversation = payload
			break
		}
	}
	var lastSequence int64
	maxCompletedIndex := 0
	for _, event := range sourceEvents {
		if event.Sequence > lastSequence {
			lastSequence = event.Sequence
		}
	}
	checkpoint.LastEventSequence = lastSequence
	for _, step := range steps {
		if step.Index > checkpoint.LastStepIndex {
			checkpoint.LastStepIndex = step.Index
		}
		if step.Status == "completed" {
			checkpoint.CompletedStepCount++
			if checkpoint.LastCompletedStepID == "" || step.Index >= maxCompletedIndex {
				maxCompletedIndex = step.Index
				checkpoint.LastCompletedStepID = step.ID
			}
		}
	}
	// Retry-from-model-call-N truncates the saved conversation to right
	// before model call N's assistant message. Every new run starts its
	// own step indices at 1; retry changes the inherited conversation,
	// not that run-local indexing rule.
	if sourceModelCallIndex > 0 {
		if len(checkpoint.AgentConversation) == 0 {
			return nil, fmt.Errorf("source run %q has no agent conversation for retry-from-model-call", sourceRunID)
		}
		var saved []types.Message
		if err := json.Unmarshal(checkpoint.AgentConversation, &saved); err != nil {
			return nil, fmt.Errorf("decode source conversation for retry: %w", err)
		}
		truncated, err := truncateConversationToRunModelCall(saved, sourceRun.ModelCallCount, sourceModelCallIndex)
		if err != nil {
			return nil, fmt.Errorf("retry-from-model-call truncation: %w", err)
		}
		payload, err := json.Marshal(truncated)
		if err != nil {
			return nil, fmt.Errorf("encode truncated conversation: %w", err)
		}
		checkpoint.AgentConversation = payload
		checkpoint.LastStepIndex = 0
		checkpoint.LastCompletedStepID = ""
		checkpoint.CompletedStepCount = 0
		checkpoint.ToolDispatchSteps = nil
		checkpoint.ToolDispatchModelCallIndex = 0
	}
	return checkpoint, nil
}

func (r *Runner) ownRunResumeCheckpoint(ctx context.Context, taskID, runID string, events []types.TaskRunEvent) (*ResumeCheckpoint, bool, error) {
	artifacts, err := r.store.ListArtifacts(ctx, taskstate.ArtifactFilter{TaskID: taskID, RunID: runID})
	if err != nil {
		return nil, false, err
	}
	var conversationArtifact *types.TaskArtifact
	for i := range artifacts {
		if artifacts[i].Kind == "agent_conversation" && len(artifacts[i].ContentText) > 0 {
			conversationArtifact = &artifacts[i]
			break
		}
	}
	if conversationArtifact == nil {
		return nil, false, nil
	}

	steps, err := r.store.ListSteps(ctx, runID)
	if err != nil {
		return nil, false, err
	}
	durableModelCalls, err := completedModelCallCountFromSteps(steps)
	if err != nil {
		return nil, false, fmt.Errorf("recover Run-local model-call count: %w", err)
	}
	currentRun, found, err := r.store.GetRun(ctx, taskID, runID)
	if err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, fmt.Errorf("run %q not found while recovering its checkpoint", runID)
	}
	modelCallCount := currentRun.ModelCallCount
	if durableModelCalls > modelCallCount {
		modelCallCount = durableModelCalls
	}

	messages, repaired, err := completedConversationMessages(*conversationArtifact)
	if err != nil {
		return nil, false, fmt.Errorf("recover current Run conversation: %w", err)
	}
	if countAssistantMessages(messages) < modelCallCount {
		return nil, false, fmt.Errorf("current Run conversation has %d completed assistant response(s), fewer than authoritative model_call_count %d", countAssistantMessages(messages), modelCallCount)
	}
	payload, err := json.Marshal(messages)
	if err != nil {
		return nil, false, fmt.Errorf("encode recovered current Run conversation: %w", err)
	}
	if repaired {
		conversationArtifact.ContentText = string(payload)
		conversationArtifact.SizeBytes = int64(len(payload))
		conversationArtifact.Status = "ready"
		conversationArtifact.Description = conversationArtifactDescription(modelCallCount)
		if _, err := r.store.UpdateArtifact(ctx, *conversationArtifact); err != nil {
			return nil, false, fmt.Errorf("persist recovered current Run conversation: %w", err)
		}
	}

	checkpoint := &ResumeCheckpoint{
		SourceRunID:                runID,
		SameRun:                    true,
		Reason:                     "same_run_progress",
		ArtifactCount:              len(artifacts),
		AgentConversation:          payload,
		PriorCostMicrosUSD:         currentRun.PriorCostMicrosUSD,
		ThisRunCostMicrosUSD:       currentRun.TotalCostMicrosUSD,
		ThisRunModelCallCount:      modelCallCount,
		ToolDispatchSteps:          durableToolDispatchSteps(steps),
		ToolDispatchModelCallIndex: modelCallCount,
	}
	maxCompletedIndex := 0
	for _, event := range events {
		if event.Sequence > checkpoint.LastEventSequence {
			checkpoint.LastEventSequence = event.Sequence
		}
	}
	for _, step := range steps {
		if step.Index > checkpoint.LastStepIndex {
			checkpoint.LastStepIndex = step.Index
		}
		if step.Status == "completed" {
			checkpoint.CompletedStepCount++
			if checkpoint.LastCompletedStepID == "" || step.Index >= maxCompletedIndex {
				maxCompletedIndex = step.Index
				checkpoint.LastCompletedStepID = step.ID
			}
		}
		if cost := int64Field(step.OutputSummary["run_cumulative_cost_micros_usd"]); cost > checkpoint.ThisRunCostMicrosUSD {
			checkpoint.ThisRunCostMicrosUSD = cost
		}
	}
	if pendingToolCalls := pendingToolCallsForResume(messages); len(pendingToolCalls) > 0 {
		approved, err := r.sameRunPendingToolCallsApproved(ctx, taskID, runID, steps, pendingToolCalls)
		if err != nil {
			return nil, false, err
		}
		checkpoint.PendingToolCallsApproved = approved
	}
	return checkpoint, true, nil
}

func (r *Runner) sameRunPendingToolCallsApproved(ctx context.Context, taskID, runID string, steps []types.TaskStep, pendingToolCalls []types.ToolCall) (bool, error) {
	var latest types.TaskStep
	for _, step := range steps {
		if step.Index > latest.Index {
			latest = step
		}
	}
	if latest.Kind != "approval" || latest.ApprovalID == "" {
		return false, nil
	}
	approvedDigest, _ := latest.Input[toolCallBundleDigestKey].(string)
	if approvedDigest == "" || approvedDigest != agentToolCallBundleDigest(pendingToolCalls) {
		return false, nil
	}
	approvals, err := r.store.ListApprovals(ctx, taskID)
	if err != nil {
		return false, fmt.Errorf("list approvals for current Run checkpoint: %w", err)
	}
	for _, approval := range approvals {
		if approval.ID == latest.ApprovalID && approval.RunID == runID &&
			approval.StepID == latest.ID &&
			approval.Kind == "agent_loop_tool_call" && approval.Status == "approved" {
			return true, nil
		}
	}
	return false, nil
}

func completedConversationMessages(artifact types.TaskArtifact) ([]types.Message, bool, error) {
	var messages []types.Message
	if err := json.Unmarshal([]byte(artifact.ContentText), &messages); err != nil {
		return nil, false, fmt.Errorf("malformed agent_conversation artifact: %w", err)
	}
	incomplete := artifact.Status == "streaming" || artifact.Status == "cancelled"
	if !incomplete {
		return messages, false, nil
	}
	if len(messages) == 0 || messages[len(messages)-1].Role != "assistant" {
		return nil, false, fmt.Errorf("incomplete agent_conversation has no trailing partial assistant response")
	}
	return messages[:len(messages)-1], true, nil
}

func completedModelCallCountFromSteps(steps []types.TaskStep) (int, error) {
	indices := map[int]struct{}{}
	maxIndex := 0
	for _, step := range steps {
		if step.Status != "completed" || step.Kind != "model" || step.ToolName != "builtin.agent_loop_llm" {
			continue
		}
		index := intField(step.Input["model_call_index"])
		if index < 1 {
			return 0, fmt.Errorf("completed model Step %q has invalid model_call_index", step.ID)
		}
		indices[index] = struct{}{}
		if index > maxIndex {
			maxIndex = index
		}
	}
	for index := 1; index <= maxIndex; index++ {
		if _, ok := indices[index]; !ok {
			return 0, fmt.Errorf("completed model Steps have a gap at model_call_index %d", index)
		}
	}
	return maxIndex, nil
}

func intField(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		if typed == float64(int(typed)) {
			return int(typed)
		}
	}
	return 0
}

func int64Field(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	}
	return 0
}

func (r *Runner) emitRunEvent(ctx context.Context, taskID, runID, eventType, requestID, traceID string, extra map[string]any) (types.TaskRunEvent, error) {
	if r.store == nil || runID == "" {
		return types.TaskRunEvent{}, nil
	}
	return runtimeevents.NewRecorder(r.store, runtimeevents.WithSnapshot(r.runEventSnapshot)).Append(ctx, runtimeevents.Event{
		TaskID:            taskID,
		RunID:             runID,
		EventType:         eventType,
		Data:              extra,
		RequestID:         requestID,
		TraceID:           traceID,
		SnapshotMode:      runtimeevents.SnapshotRequired,
		SnapshotPlacement: runtimeevents.SnapshotProvidesBase,
	})
}

func (r *Runner) runEventSnapshot(ctx context.Context, taskID, runID string) (map[string]any, error) {
	run, _, err := r.store.GetRun(ctx, taskID, runID)
	if err != nil {
		return nil, err
	}
	steps, _ := r.store.ListSteps(ctx, runID)
	artifacts, _ := r.store.ListArtifacts(ctx, taskstate.ArtifactFilter{TaskID: taskID, RunID: runID})
	return map[string]any{
		"run":       run,
		"steps":     steps,
		"artifacts": artifacts,
	}, nil
}
