package orchestrator

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/runtimeevents"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestAgentLoopCrossRunApproval_PreservesSourceCallThroughResolutionAndDispatch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	runner, queue := newApprovalLifecycleRunner(store)
	now := time.Now().UTC()

	spec := newAgentLoopSpec(t)
	task := spec.Task
	task.ID = "task-cross-run-approval"
	task.ExecutionKind = "agent_loop"
	task.Status = "running"
	task.LatestRunID = "run-resumed"
	task.CreatedAt = now
	task.UpdatedAt = now
	task.StartedAt = now
	sourceRun := types.TaskRun{
		ID:             "run-source",
		TaskID:         task.ID,
		Number:         1,
		Status:         "failed",
		ModelCallCount: 3,
		StartedAt:      now,
		FinishedAt:     now,
	}
	resumedRun := types.TaskRun{
		ID:        task.LatestRunID,
		TaskID:    task.ID,
		Number:    2,
		Status:    "running",
		StartedAt: now,
		RequestID: "request-cross-run",
		TraceID:   "trace-cross-run",
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	for _, run := range []types.TaskRun{sourceRun, resumedRun} {
		if _, err := store.CreateRun(ctx, run); err != nil {
			t.Fatalf("CreateRun(%s): %v", run.ID, err)
		}
	}

	call := agentLoopToolCall("call-source-shell", "shell_exec", `{"command":"pwd"}`)
	sourceConversation, err := json.Marshal([]types.Message{
		{Role: "user", Content: "inspect the workspace"},
		makeAssistantMsg("I need to inspect it.", call),
	})
	if err != nil {
		t.Fatalf("marshal source conversation: %v", err)
	}
	if _, err := store.CreateArtifact(ctx, types.TaskArtifact{
		ID:          "convo-" + sourceRun.ID,
		TaskID:      task.ID,
		RunID:       sourceRun.ID,
		Kind:        "agent_conversation",
		StorageKind: "inline",
		ContentText: string(sourceConversation),
		Status:      "ready",
		CreatedAt:   now,
	}); err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	if _, err := store.AppendRunEvent(ctx, types.TaskRunEvent{
		ID:        "event-cross-run-resume",
		TaskID:    task.ID,
		RunID:     resumedRun.ID,
		EventType: runtimeevents.EventRunResumedFromEvent.String(),
		Data:      map[string]any{"from_run_id": sourceRun.ID},
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("AppendRunEvent: %v", err)
	}

	checkpoint, err := runner.resumeCheckpointForRun(ctx, task.ID, resumedRun.ID)
	if err != nil {
		t.Fatalf("cross-Run checkpoint: %v", err)
	}
	if checkpoint == nil || checkpoint.SameRun || checkpoint.ThisRunModelCallCount != 0 ||
		checkpoint.PendingToolCallsOriginRunID != sourceRun.ID || checkpoint.PendingToolCallsOriginModelCallIndex != 3 {
		t.Fatalf("cross-Run checkpoint = %+v, want source Run model call 3 and zero current calls", checkpoint)
	}
	// Model-call checkpointing can make the inherited conversation current-Run
	// state before the fresh approval Step is durable. Recovery must reconstruct
	// provenance from the resume event rather than inventing model call zero.
	if _, err := store.CreateArtifact(ctx, types.TaskArtifact{
		ID:          "convo-" + resumedRun.ID,
		TaskID:      task.ID,
		RunID:       resumedRun.ID,
		Kind:        "agent_conversation",
		StorageKind: "inline",
		ContentText: string(sourceConversation),
		Status:      "ready",
		CreatedAt:   now,
	}); err != nil {
		t.Fatalf("CreateArtifact(current Run checkpoint): %v", err)
	}
	checkpoint, err = runner.resumeCheckpointForRun(ctx, task.ID, resumedRun.ID)
	if err != nil {
		t.Fatalf("current-Run checkpoint before approval Step: %v", err)
	}
	if checkpoint == nil || !checkpoint.SameRun || checkpoint.PendingToolCallsApproved || checkpoint.ThisRunModelCallCount != 0 ||
		checkpoint.PendingToolCallsOriginRunID != sourceRun.ID || checkpoint.PendingToolCallsOriginModelCallIndex != 3 {
		t.Fatalf("current-Run checkpoint before approval = %+v, want reconstructed source provenance", checkpoint)
	}
	// A second Run can inherit that pre-approval crash window. It must walk the
	// immediate source's resume lineage back to the Run that actually called the
	// provider rather than inventing an origin for the zero-call middle Run.
	chainedRun := types.TaskRun{
		ID:        "run-resumed-again",
		TaskID:    task.ID,
		Number:    3,
		Status:    "running",
		StartedAt: now,
	}
	if _, err := store.CreateRun(ctx, chainedRun); err != nil {
		t.Fatalf("CreateRun(%s): %v", chainedRun.ID, err)
	}
	if _, err := store.AppendRunEvent(ctx, types.TaskRunEvent{
		ID:        "event-chained-resume",
		TaskID:    task.ID,
		RunID:     chainedRun.ID,
		EventType: runtimeevents.EventRunResumedFromEvent.String(),
		Data:      map[string]any{"from_run_id": resumedRun.ID},
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("AppendRunEvent(chained resume): %v", err)
	}
	chainedCheckpoint, err := runner.resumeCheckpointForRun(ctx, task.ID, chainedRun.ID)
	if err != nil {
		t.Fatalf("chained cross-Run checkpoint: %v", err)
	}
	if chainedCheckpoint == nil || chainedCheckpoint.SameRun || chainedCheckpoint.SourceRunID != resumedRun.ID ||
		chainedCheckpoint.PendingToolCallsOriginRunID != sourceRun.ID || chainedCheckpoint.PendingToolCallsOriginModelCallIndex != 3 {
		t.Fatalf("chained checkpoint = %+v, want immediate source %s and origin %s/model call 3", chainedCheckpoint, resumedRun.ID, sourceRun.ID)
	}

	llm := &scriptedLLM{responses: []*types.ChatResponse{makeChatResp(makeAssistantMsg("Inspection complete."))}}
	shell := &stubExecutor{result: &ExecutionResult{Status: "completed"}}
	loop := NewAgentLoopExecutor(llm, shell, &stubExecutor{}, &stubExecutor{}, 8, []string{"shell_exec"}, HTTPRequestPolicy{})
	spec.Task = task
	spec.Run = resumedRun
	spec.ResumeCheckpoint = checkpoint
	spec.UpsertStep = func(step types.TaskStep) error { return runner.upsertStep(ctx, step) }
	spec.UpsertArtifact = func(artifact types.TaskArtifact) error { return runner.upsertArtifact(ctx, artifact) }

	paused, err := loop.Execute(ctx, spec)
	if err != nil {
		t.Fatalf("cross-Run approval request Execute: %v", err)
	}
	if paused.Status != "awaiting_approval" || paused.ModelCallCount != 0 || len(paused.PendingApprovals) != 1 || len(shell.calls) != 0 {
		t.Fatalf("paused result = %+v shell calls=%d, want fresh approval without model call or dispatch", paused, len(shell.calls))
	}
	approvalStep := paused.Steps[len(paused.Steps)-1]
	assertSourceModelCallStep(t, approvalStep, sourceRun.ID, 3)

	trace := runner.tracer.Start(resumedRun.RequestID)
	persisted, err := newExecutionResultPersister(runner, trace, task, resumedRun, resumedRun.RequestID).persist(ctx, paused)
	trace.Finalize()
	if err != nil {
		t.Fatalf("persist approval request: %v", err)
	}
	if persisted.Run.ModelCallCount != 0 || persisted.Run.Status != "awaiting_approval" {
		t.Fatalf("persisted paused Run = %+v, want zero calls awaiting approval", persisted.Run)
	}
	// Simulate upgrading an outstanding approval created before source
	// provenance existed. That version persisted model_call_index=0 on this
	// exact approval Step shape; the new runtime must recover its lineage.
	legacyApprovalStep := approvalStep
	legacyApprovalStep.Input = make(map[string]any, len(approvalStep.Input))
	for key, value := range approvalStep.Input {
		legacyApprovalStep.Input[key] = value
	}
	delete(legacyApprovalStep.Input, agentLoopSourceRunIDKey)
	delete(legacyApprovalStep.Input, agentLoopSourceModelCallIndexKey)
	legacyApprovalStep.Input[agentLoopModelCallIndexKey] = 0
	if err := runner.upsertStep(ctx, legacyApprovalStep); err != nil {
		t.Fatalf("persist legacy zero-index approval Step: %v", err)
	}

	resolved, err := runner.ResolveTaskApproval(ctx, ResolveApprovalRequest{
		Task:        persisted.Task,
		ApprovalID:  paused.PendingApprovals[0].ID,
		Decision:    "approve",
		ResolvedBy:  "operator",
		IDGenerator: deterministicApprovalID,
	})
	if err != nil {
		t.Fatalf("ResolveTaskApproval: %v", err)
	}
	if resolved.Run.ModelCallCount != 0 || len(queue.enqueued) != 1 {
		t.Fatalf("resolved Run = %+v queue=%+v, want same zero-call Run queued once", resolved.Run, queue.enqueued)
	}

	checkpoint, err = runner.resumeCheckpointForRun(ctx, task.ID, resumedRun.ID)
	if err != nil {
		t.Fatalf("same-Run checkpoint after approval: %v", err)
	}
	if checkpoint == nil || !checkpoint.SameRun || !checkpoint.PendingToolCallsApproved || checkpoint.ThisRunModelCallCount != 0 ||
		checkpoint.PendingToolCallsOriginRunID != sourceRun.ID || checkpoint.PendingToolCallsOriginModelCallIndex != 3 {
		t.Fatalf("approved same-Run checkpoint = %+v, want retained source provenance and zero current calls", checkpoint)
	}

	spec.Task = resolved.Task
	spec.Run = resolved.Run
	spec.ResumeCheckpoint = checkpoint
	completed, err := loop.Execute(ctx, spec)
	if err != nil {
		t.Fatalf("approved same-Run Execute: %v", err)
	}
	if completed.Status != "completed" || len(shell.calls) != 1 || llm.calls.Load() != 1 {
		t.Fatalf("completed result status=%q shell=%d model=%d, want exactly one dispatch and one post-tool model call", completed.Status, len(shell.calls), llm.calls.Load())
	}
	var resumeStep, dispatchStep *types.TaskStep
	for index := range completed.Steps {
		step := &completed.Steps[index]
		switch step.ToolName {
		case "builtin.agent_loop_resume":
			resumeStep = step
		case "shell_exec":
			dispatchStep = step
		}
	}
	if resumeStep == nil || dispatchStep == nil {
		t.Fatalf("completed Steps = %+v, want resume and dispatch Steps", completed.Steps)
	}
	assertSourceModelCallStep(t, *resumeStep, sourceRun.ID, 3)
	assertSourceModelCallStep(t, *dispatchStep, sourceRun.ID, 3)

	finalTrace := runner.tracer.Start(resolved.Run.RequestID)
	finalPersisted, err := newExecutionResultPersister(runner, finalTrace, resolved.Task, resolved.Run, resolved.Run.RequestID).persist(ctx, completed)
	finalTrace.Finalize()
	if err != nil {
		t.Fatalf("persist completed resumed Run: %v", err)
	}
	if finalPersisted.Run.ModelCallCount != 1 {
		t.Fatalf("persisted completed Run model calls = %d, want exactly one current-Run provider call", finalPersisted.Run.ModelCallCount)
	}
}

func TestAgentLoopModelCallBoundaryIgnoresAncestorDispatchRefs(t *testing.T) {
	t.Parallel()

	spec := newAgentLoopSpec(t)
	spec.Run.ModelCallCount = 1
	call := agentLoopToolCall("call-ancestor", "shell_exec", `{"command":"pwd"}`)
	step := buildAgentToolDispatchIntentForModelCallRef(
		spec,
		call,
		2,
		agentLoopModelCallRef{RunID: "run-ancestor", Index: 9},
		time.Now().UTC(),
	)

	boundary, err := agentLoopModelCallBoundary(spec.Run, []types.TaskStep{step})
	if err != nil {
		t.Fatalf("agentLoopModelCallBoundary(): %v", err)
	}
	if boundary != 1 {
		t.Fatalf("Run-local model-call boundary = %d, want 1", boundary)
	}
}

func assertSourceModelCallStep(t *testing.T, step types.TaskStep, sourceRunID string, sourceModelCallIndex int) {
	t.Helper()
	if got := step.Input[agentLoopSourceRunIDKey]; got != sourceRunID {
		t.Fatalf("Step %q source_run_id = %v, want %q", step.ID, got, sourceRunID)
	}
	if got := step.Input[agentLoopSourceModelCallIndexKey]; got != sourceModelCallIndex {
		t.Fatalf("Step %q source_model_call_index = %v, want %d", step.ID, got, sourceModelCallIndex)
	}
	if _, found := step.Input[agentLoopModelCallIndexKey]; found {
		t.Fatalf("Step %q attributed source work to current Run: %+v", step.ID, step.Input)
	}
}
