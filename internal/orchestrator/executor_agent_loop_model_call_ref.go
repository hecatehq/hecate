package orchestrator

import (
	"fmt"
	"strings"

	"github.com/hecatehq/hecate/pkg/types"
)

const (
	agentLoopModelCallIndexKey       = "model_call_index"
	agentLoopSourceRunIDKey          = "source_run_id"
	agentLoopSourceModelCallIndexKey = "source_model_call_index"
)

// agentLoopModelCallRef identifies the Run that actually produced a pending
// assistant tool-call batch. Cross-Run recovery must retain this provenance
// without attributing a provider call to the recovering Run.
type agentLoopModelCallRef struct {
	RunID string
	Index int
}

func currentAgentLoopModelCallRef(spec ExecutionSpec, index int) agentLoopModelCallRef {
	return agentLoopModelCallRef{RunID: spec.Run.ID, Index: index}
}

func (ref agentLoopModelCallRef) validate() error {
	if strings.TrimSpace(ref.RunID) == "" {
		return fmt.Errorf("origin Run id is required")
	}
	if ref.Index < 1 {
		return fmt.Errorf("origin model-call index must be 1-based, got %d", ref.Index)
	}
	return nil
}

func (ref agentLoopModelCallRef) addStepInput(currentRunID string, input map[string]any) {
	if ref.RunID == currentRunID {
		input[agentLoopModelCallIndexKey] = ref.Index
		return
	}
	input[agentLoopSourceRunIDKey] = ref.RunID
	input[agentLoopSourceModelCallIndexKey] = ref.Index
}

func (ref agentLoopModelCallRef) title(currentRunID, currentTitle, sourceTitle string) string {
	if ref.RunID == currentRunID {
		return fmt.Sprintf(currentTitle, ref.Index)
	}
	return fmt.Sprintf(sourceTitle, ref.Index)
}

func agentLoopModelCallRefFromStep(currentRunID string, step types.TaskStep) (agentLoopModelCallRef, bool, error) {
	if step.Input == nil {
		return agentLoopModelCallRef{}, false, nil
	}
	modelCallValue, hasModelCall := step.Input[agentLoopModelCallIndexKey]
	sourceRunValue, hasSourceRun := step.Input[agentLoopSourceRunIDKey]
	sourceIndexValue, hasSourceIndex := step.Input[agentLoopSourceModelCallIndexKey]
	if hasSourceRun || hasSourceIndex {
		if hasModelCall {
			return agentLoopModelCallRef{}, true, fmt.Errorf("Step %q mixes local and source model-call provenance", step.ID)
		}
		sourceRunID, _ := sourceRunValue.(string)
		ref := agentLoopModelCallRef{RunID: strings.TrimSpace(sourceRunID), Index: intField(sourceIndexValue)}
		if err := ref.validate(); err != nil {
			return agentLoopModelCallRef{}, true, fmt.Errorf("Step %q has invalid source model-call provenance: %w", step.ID, err)
		}
		return ref, true, nil
	}
	if !hasModelCall {
		return agentLoopModelCallRef{}, false, nil
	}
	ref := agentLoopModelCallRef{RunID: currentRunID, Index: intField(modelCallValue)}
	if isNumericZeroModelCallIndex(modelCallValue) && step.Kind == "approval" && step.ToolName == "builtin.agent_loop_approval" {
		// Before source provenance existed, cross-Run re-gating persisted this
		// exact approval shape with model_call_index=0. It carries no Run-local
		// model-call identity; callers must reconstruct the origin from lineage.
		return agentLoopModelCallRef{}, false, nil
	}
	if err := ref.validate(); err != nil {
		return agentLoopModelCallRef{}, true, fmt.Errorf("Step %q has invalid model-call provenance: %w", step.ID, err)
	}
	return ref, true, nil
}

func isNumericZeroModelCallIndex(value any) bool {
	switch typed := value.(type) {
	case int:
		return typed == 0
	case int64:
		return typed == 0
	case float64:
		return typed == 0
	default:
		return false
	}
}

func latestAgentLoopModelCallRef(currentRunID string, steps []types.TaskStep) (agentLoopModelCallRef, bool, error) {
	var latest types.TaskStep
	var latestRef agentLoopModelCallRef
	found := false
	for _, step := range steps {
		ref, hasRef, err := agentLoopModelCallRefFromStep(currentRunID, step)
		if err != nil {
			return agentLoopModelCallRef{}, false, err
		}
		if !hasRef || (found && step.Index < latest.Index) {
			continue
		}
		latest = step
		latestRef = ref
		found = true
	}
	return latestRef, found, nil
}

func pendingAgentLoopModelCallRef(spec ExecutionSpec, currentRunModelCallCount int) (agentLoopModelCallRef, error) {
	if checkpoint := spec.ResumeCheckpoint; checkpoint != nil &&
		(checkpoint.PendingToolCallsOriginRunID != "" || checkpoint.PendingToolCallsOriginModelCallIndex != 0) {
		ref := agentLoopModelCallRef{
			RunID: strings.TrimSpace(checkpoint.PendingToolCallsOriginRunID),
			Index: checkpoint.PendingToolCallsOriginModelCallIndex,
		}
		if err := ref.validate(); err != nil {
			return agentLoopModelCallRef{}, fmt.Errorf("resume checkpoint pending tool-call provenance: %w", err)
		}
		return ref, nil
	}
	ref := currentAgentLoopModelCallRef(spec, currentRunModelCallCount)
	if err := ref.validate(); err != nil {
		return agentLoopModelCallRef{}, fmt.Errorf("pending tool calls have no durable model-call provenance: %w", err)
	}
	return ref, nil
}
