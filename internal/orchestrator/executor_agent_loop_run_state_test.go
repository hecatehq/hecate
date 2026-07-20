package orchestrator

import (
	"strings"
	"testing"

	"github.com/hecatehq/hecate/pkg/types"
)

func TestAgentLoopRunState_AddStepAndArtifacts(t *testing.T) {
	spec := newAgentLoopSpec(t)
	spec.ResumeCheckpoint = &ResumeCheckpoint{SameRun: true, LastStepIndex: 4}
	var upsertedSteps []types.TaskStep
	var upsertedArtifacts []types.TaskArtifact
	spec.UpsertStep = func(step types.TaskStep) error {
		upsertedSteps = append(upsertedSteps, step)
		return nil
	}
	spec.UpsertArtifact = func(artifact types.TaskArtifact) error {
		upsertedArtifacts = append(upsertedArtifacts, artifact)
		return nil
	}
	state := newAgentLoopRunState(spec, 4)

	if got := state.NextStepIndex(); got != 5 {
		t.Fatalf("NextStepIndex() = %d, want 5", got)
	}
	step := types.TaskStep{ID: "step-1", TaskID: spec.Task.ID, RunID: spec.Run.ID, Index: state.NextStepIndex(), Status: "completed"}
	if err := state.AddStep(spec, step); err != nil {
		t.Fatalf("AddStep: %v", err)
	}
	if got := state.NextStepIndex(); got != 6 {
		t.Fatalf("NextStepIndex() after AddStep = %d, want 6", got)
	}
	if len(state.Steps()) != 1 || len(upsertedSteps) != 1 || upsertedSteps[0].ID != "step-1" {
		t.Fatalf("step state/upserts = state %+v upserts %+v, want step-1 recorded once", state.Steps(), upsertedSteps)
	}

	artifact := types.TaskArtifact{ID: "artifact-1", TaskID: spec.Task.ID, RunID: spec.Run.ID, Kind: "summary"}
	if err := state.AddArtifact(spec, artifact); err != nil {
		t.Fatalf("AddArtifact: %v", err)
	}
	moreArtifacts := []types.TaskArtifact{
		{ID: "artifact-2", TaskID: spec.Task.ID, RunID: spec.Run.ID, Kind: "stdout"},
		{ID: "artifact-3", TaskID: spec.Task.ID, RunID: spec.Run.ID, Kind: "stderr"},
	}
	if err := state.AddArtifacts(spec, moreArtifacts); err != nil {
		t.Fatalf("AddArtifacts: %v", err)
	}
	if len(state.Artifacts()) != 3 || len(upsertedArtifacts) != 3 {
		t.Fatalf("artifact state/upserts = state %+v upserts %+v, want three artifacts", state.Artifacts(), upsertedArtifacts)
	}
}

func TestAgentLoopRunState_NewRunRestartsStepIndex(t *testing.T) {
	spec := newAgentLoopSpec(t)
	spec.ResumeCheckpoint = &ResumeCheckpoint{LastStepIndex: 4}

	state := newAgentLoopRunState(spec, 4)

	if got := state.NextStepIndex(); got != 1 {
		t.Fatalf("NextStepIndex() = %d, want 1 for a new run", got)
	}
}

func TestAgentLoopRunState_TrackConversationArtifactOnce(t *testing.T) {
	spec := newAgentLoopSpec(t)
	state := newAgentLoopRunState(spec, 4)

	first := &types.TaskArtifact{ID: "conversation-1", TaskID: spec.Task.ID, RunID: spec.Run.ID, Kind: "agent_conversation"}
	second := &types.TaskArtifact{ID: "conversation-2", TaskID: spec.Task.ID, RunID: spec.Run.ID, Kind: "agent_conversation"}
	state.TrackConversationArtifact(first)
	state.TrackConversationArtifact(second)
	state.TrackConversationArtifact(nil)

	artifacts := state.Artifacts()
	if len(artifacts) != 1 || artifacts[0].ID != "conversation-1" {
		t.Fatalf("tracked artifacts = %+v, want only first conversation artifact", artifacts)
	}
}

func TestAgentLoopRunState_RecordRouteAndAttachAccounting(t *testing.T) {
	spec := newAgentLoopSpec(t)
	state := newAgentLoopRunState(spec, 4)
	providerInstance := types.ProviderInstanceIdentity{ID: "runtime-route", Kind: types.ProviderInstanceIdentityRuntime}

	resp := &types.ChatResponse{
		Model: "fallback-model",
		Route: types.RouteDecision{
			Provider:         "ollama",
			ProviderKind:     "local",
			ProviderInstance: providerInstance,
			Model:            "resolved-model",
		},
		Cost: types.CostBreakdown{TotalMicrosUSD: 125},
	}
	state.RecordRoute(resp)
	modelCallCost := state.AccumulateCost(resp)
	state.AddModelCallCost(2, "step-1", modelCallCost, 3)

	res := state.Result("completed")
	if res.Provider != "ollama" || res.ProviderKind != "local" || res.ProviderInstance != providerInstance || res.Model != "resolved-model" {
		t.Fatalf("route = provider %q kind %q model %q, want resolved ollama/local/resolved-model", res.Provider, res.ProviderKind, res.Model)
	}
	if res.CostMicrosUSD != 125 {
		t.Fatalf("CostMicrosUSD = %d, want 125", res.CostMicrosUSD)
	}
	if len(res.ModelCallCosts) != 1 {
		t.Fatalf("ModelCallCosts = %+v, want one entry", res.ModelCallCosts)
	}
	record := res.ModelCallCosts[0]
	if record.ModelCall != 2 || record.StepID != "step-1" || record.CostMicrosUSD != 125 || record.CumulativeMicrosUSD != 125 || record.ToolCallCount != 3 {
		t.Fatalf("ModelCallCosts[0] = %+v, want model call 2 step-1 cost/cumulative 125 tool calls 3", record)
	}
}

func TestAgentLoopRunState_RecordRouteFallsBackToResponseModel(t *testing.T) {
	spec := newAgentLoopSpec(t)
	state := newAgentLoopRunState(spec, 4)

	state.RecordRoute(&types.ChatResponse{Model: "response-model"})

	res := state.Result("completed")
	if res.Model != "response-model" {
		t.Fatalf("Model = %q, want response-model", res.Model)
	}
}

func TestAgentLoopRunState_CostCeilingUsesPriorAndCurrentRun(t *testing.T) {
	spec := newAgentLoopSpec(t)
	spec.Task.BudgetMicrosUSD = 500
	spec.ResumeCheckpoint = &ResumeCheckpoint{
		PriorCostMicrosUSD:   400,
		ThisRunCostMicrosUSD: 50,
	}
	state := newAgentLoopRunState(spec, 4)

	if _, exceeded := state.CostCeilingExceededMessage(); exceeded {
		t.Fatalf("CostCeilingExceededMessage() exceeded before current model call, want false")
	}
	state.AccumulateCost(&types.ChatResponse{Cost: types.CostBreakdown{TotalMicrosUSD: 60}})

	msg, exceeded := state.CostCeilingExceededMessage()
	if !exceeded {
		t.Fatalf("CostCeilingExceededMessage() exceeded = false, want true")
	}
	for _, want := range []string{"spent 110", "400", "510", "ceiling 500"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message %q does not contain %q", msg, want)
		}
	}
	if got := state.Result("failed").CostMicrosUSD; got != 110 {
		t.Fatalf("Result().CostMicrosUSD = %d, want current-run total 110", got)
	}
}
