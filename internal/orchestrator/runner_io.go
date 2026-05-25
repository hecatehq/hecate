package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/gitrunner"
	"github.com/hecatehq/hecate/internal/profiler"
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
	if currentRun, found, err := r.store.GetRun(ctx, task.ID, run.ID); err == nil && found {
		// Cancellation can arrive through the HTTP API while the worker is
		// still unwinding a cancelled executor. In that case CancelRun has
		// already persisted the terminal run/task state and emitted the
		// authoritative run.cancelled event; re-emitting it here creates
		// duplicate terminal events under racey shutdown/cancel timing.
		if types.IsTerminalTaskRunStatus(currentRun.Status) && currentRun.Status == status {
			return nil
		}
	}
	now := time.Now().UTC()
	var failedRunDurationMS int64
	if !run.StartedAt.IsZero() {
		failedRunDurationMS = now.Sub(run.StartedAt).Milliseconds()
	}
	run.Status = status
	run.LastError = message
	run.FinishedAt = now
	run.OtelStatusCode = "error"
	run.OtelStatusMessage = message
	if _, err := r.store.UpdateRun(ctx, run); err != nil {
		return err
	}
	r.metrics.RecordRun(ctx, telemetry.RunMetricsRecord{
		TaskID:        task.ID,
		RunID:         run.ID,
		Status:        status,
		ExecutionKind: task.ExecutionKind,
		Model:         run.Model,
		DurationMS:    failedRunDurationMS,
	})
	_, _ = r.emitRunEvent(ctx, task.ID, run.ID, terminalRunEventType(status), requestID, trace.TraceID, map[string]any{"error": message, "status": status})
	task.Status = status
	task.LatestRunID = run.ID
	task.LastError = message
	task.FinishedAt = now
	task.UpdatedAt = now
	task.LatestTraceID = trace.TraceID
	task.LatestRequestID = requestID
	_, err := r.store.UpdateTask(ctx, task)
	return err
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
	sourceRunID := ""
	reason := ""
	appendUserPrompt := ""
	retryFromTurn := 0
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if event.EventType != "run.resumed_from_event" {
			continue
		}
		value, ok := event.Data["resumed_from_run_id"]
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
		// retry_from_turn is event-data JSON-decoded — depending on
		// the store it may come back as float64 (JSON-roundtripped)
		// or int. Accept both. Zero/missing means a regular resume,
		// not a retry-from-turn.
		if raw, ok := event.Data["retry_from_turn"]; ok {
			switch v := raw.(type) {
			case int:
				retryFromTurn = v
			case int64:
				retryFromTurn = int(v)
			case float64:
				retryFromTurn = int(v)
			}
		}
		break
	}
	// If no separate source run, the caller might still be resuming
	// the SAME run after a mid-loop approval pause. The agent loop
	// persists conversation as an artifact on its own run; pull that
	// into a checkpoint so the loop can hydrate state and pick up
	// from the trailing tool_calls.
	if sourceRunID == "" {
		ownArtifacts, err := r.store.ListArtifacts(ctx, taskstate.ArtifactFilter{TaskID: taskID, RunID: runID})
		if err != nil {
			return nil, err
		}
		for _, art := range ownArtifacts {
			if art.Kind == "agent_conversation" && len(art.ContentText) > 0 {
				cp := &ResumeCheckpoint{
					SourceRunID:       runID,
					Reason:            "approved_mid_loop",
					AgentConversation: []byte(art.ContentText),
				}
				// Same-run mid-approval resume: surface BOTH the
				// chain-prior cost (so the ceiling holds across the
				// task lifecycle) AND this run's pre-pause spend
				// (so the loop seeds costSpent with it instead of
				// resetting to 0). Without ThisRunCostMicrosUSD the
				// pre-pause LLM spend would be lost when the runner
				// overwrites Total on finalization.
				if currentRun, found, err := r.store.GetRun(ctx, taskID, runID); err == nil && found {
					cp.PriorCostMicrosUSD = currentRun.PriorCostMicrosUSD
					cp.ThisRunCostMicrosUSD = currentRun.TotalCostMicrosUSD
				}
				return cp, nil
			}
		}
		return nil, nil
	}
	steps, err := r.store.ListSteps(ctx, sourceRunID)
	if err != nil {
		return nil, err
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
		SourceRunID:      sourceRunID,
		Reason:           strings.TrimSpace(reason),
		AppendUserPrompt: strings.TrimSpace(appendUserPrompt),
		LastStepIndex:    0,
		ArtifactCount:    len(artifacts),
		RetryFromTurn:    retryFromTurn,
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
	}
	// Pull the agent-conversation artifact (if any) so the agent loop
	// can hydrate state on resume. We use a stable kind + ID
	// convention so the lookup is a single linear scan rather than a
	// new store method. Non-agent_loop runs simply won't have one.
	for _, art := range artifacts {
		if art.Kind == "agent_conversation" && len(art.ContentText) > 0 {
			checkpoint.AgentConversation = []byte(art.ContentText)
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
	// Retry-from-turn-N: truncate the saved conversation to right
	// before turn N's assistant message and reset step-index
	// continuity. The new run's step indices start at 1 instead of
	// continuing the source's count — semantically this is a fresh
	// run that happens to share prior conversation context, not a
	// continuation.
	if retryFromTurn > 0 && len(checkpoint.AgentConversation) > 0 {
		var saved []types.Message
		if err := json.Unmarshal(checkpoint.AgentConversation, &saved); err != nil {
			return nil, fmt.Errorf("decode source conversation for retry: %w", err)
		}
		truncated, err := truncateConversationToTurn(saved, retryFromTurn)
		if err != nil {
			return nil, fmt.Errorf("retry-from-turn truncation: %w", err)
		}
		payload, err := json.Marshal(truncated)
		if err != nil {
			return nil, fmt.Errorf("encode truncated conversation: %w", err)
		}
		checkpoint.AgentConversation = payload
		checkpoint.LastStepIndex = 0
		checkpoint.LastCompletedStepID = ""
		checkpoint.CompletedStepCount = 0
	}
	return checkpoint, nil
}

func (r *Runner) emitRunEvent(ctx context.Context, taskID, runID, eventType, requestID, traceID string, extra map[string]any) (types.TaskRunEvent, error) {
	if r.store == nil || runID == "" {
		return types.TaskRunEvent{}, nil
	}
	run, _, err := r.store.GetRun(ctx, taskID, runID)
	if err != nil {
		return types.TaskRunEvent{}, err
	}
	steps, _ := r.store.ListSteps(ctx, runID)
	artifacts, _ := r.store.ListArtifacts(ctx, taskstate.ArtifactFilter{TaskID: taskID, RunID: runID})
	data := map[string]any{
		"run":       run,
		"steps":     steps,
		"artifacts": artifacts,
	}
	for key, value := range extra {
		data[key] = value
	}
	return r.store.AppendRunEvent(ctx, types.TaskRunEvent{
		TaskID:    taskID,
		RunID:     runID,
		EventType: eventType,
		Data:      data,
		RequestID: requestID,
		TraceID:   traceID,
		CreatedAt: time.Now().UTC(),
	})
}
