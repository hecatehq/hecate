package orchestrator

import (
	"fmt"

	"github.com/hecatehq/hecate/pkg/types"
)

type agentLoopRunState struct {
	nextIndex int

	steps     []types.TaskStep
	artifacts []types.TaskArtifact

	provider         string
	providerKind     string
	providerInstance types.ProviderInstanceIdentity
	model            string

	costCeiling    int64
	priorCost      int64
	costSpent      int64
	modelCallCount int
	modelCallCosts []ModelCallCostRecord
}

func newAgentLoopRunState(spec ExecutionSpec, maxModelCalls int) *agentLoopRunState {
	baseIndex := 0
	costSpent := int64(0)
	priorCost := int64(0)
	modelCallCount := 0
	if spec.ResumeCheckpoint != nil {
		if spec.ResumeCheckpoint.SameRun && spec.ResumeCheckpoint.LastStepIndex > 0 {
			baseIndex = spec.ResumeCheckpoint.LastStepIndex
		}
		priorCost = spec.ResumeCheckpoint.PriorCostMicrosUSD
		// Same-run mid-approval resume: seed costSpent with the
		// pre-pause spend so ceiling checks and the persisted Total
		// account for it. Cross-run resumes see zero here (new run
		// hasn't spent anything yet).
		costSpent = spec.ResumeCheckpoint.ThisRunCostMicrosUSD
		modelCallCount = spec.ResumeCheckpoint.ThisRunModelCallCount
	}
	return &agentLoopRunState{
		nextIndex:      baseIndex + 1,
		steps:          make([]types.TaskStep, 0, maxModelCalls*2),
		artifacts:      make([]types.TaskArtifact, 0, maxModelCalls),
		costCeiling:    spec.Task.BudgetMicrosUSD,
		priorCost:      priorCost,
		costSpent:      costSpent,
		modelCallCount: modelCallCount,
		modelCallCosts: make([]ModelCallCostRecord, 0, maxModelCalls),
	}
}

func (s *agentLoopRunState) NextStepIndex() int {
	return s.nextIndex
}

func (s *agentLoopRunState) Steps() []types.TaskStep {
	return s.steps
}

func (s *agentLoopRunState) Artifacts() []types.TaskArtifact {
	return s.artifacts
}

func (s *agentLoopRunState) AddStep(spec ExecutionSpec, step types.TaskStep) error {
	if err := upsertTaskStep(spec, step); err != nil {
		return err
	}
	s.steps = append(s.steps, step)
	s.nextIndex++
	return nil
}

func (s *agentLoopRunState) FinalizeStep(spec ExecutionSpec, step types.TaskStep) error {
	if err := upsertTaskStep(spec, step); err != nil {
		return err
	}
	for index := range s.steps {
		if s.steps[index].ID == step.ID {
			s.steps[index] = step
			return nil
		}
	}
	return fmt.Errorf("cannot finalize untracked step %q", step.ID)
}

func (s *agentLoopRunState) UpdateRecoveredStep(spec ExecutionSpec, step types.TaskStep) error {
	if err := upsertTaskStep(spec, step); err != nil {
		return err
	}
	for index := range s.steps {
		if s.steps[index].ID == step.ID {
			s.steps[index] = step
			return nil
		}
	}
	// Same-run checkpoints do not hydrate historical Steps into runState, but
	// returning the settled record lets terminal result handling observe the
	// fail-closed recovery update without consuming another Step index.
	s.steps = append(s.steps, step)
	return nil
}

func (s *agentLoopRunState) AddArtifact(spec ExecutionSpec, artifact types.TaskArtifact) error {
	if err := upsertTaskArtifact(spec, artifact); err != nil {
		return err
	}
	s.artifacts = append(s.artifacts, artifact)
	return nil
}

func (s *agentLoopRunState) AddArtifacts(spec ExecutionSpec, artifacts []types.TaskArtifact) error {
	for _, artifact := range artifacts {
		if err := s.AddArtifact(spec, artifact); err != nil {
			return err
		}
	}
	return nil
}

func (s *agentLoopRunState) TrackConversationArtifact(artifact *types.TaskArtifact) {
	if artifact == nil {
		return
	}
	// A QA run records its workflow manifest before the conversation. Do not
	// use the total artifact count as a proxy for whether the conversation was
	// retained, or the caller's ExecutionResult will omit the durable chat
	// artifact. Replacing a matching entry also keeps the returned result in
	// sync with the latest streaming conversation snapshot.
	for i := range s.artifacts {
		if s.artifacts[i].Kind != "agent_conversation" {
			continue
		}
		if s.artifacts[i].ID == artifact.ID {
			s.artifacts[i] = *artifact
		}
		// The conversation has one stable logical slot. Retain the
		// existing record if a malformed caller supplies a second ID.
		return
	}
	s.artifacts = append(s.artifacts, *artifact)
}

func (s *agentLoopRunState) RecordRoute(resp *types.ChatResponse) {
	if resp == nil {
		return
	}
	if resp.Route.Provider != "" {
		s.provider = resp.Route.Provider
	}
	if resp.Route.ProviderKind != "" {
		s.providerKind = resp.Route.ProviderKind
	}
	if resp.Route.ProviderInstance.Valid() {
		s.providerInstance = resp.Route.ProviderInstance
	}
	if resp.Route.Model != "" {
		s.model = resp.Route.Model
	} else if resp.Model != "" {
		s.model = resp.Model
	}
}

func (s *agentLoopRunState) AccumulateCost(resp *types.ChatResponse) int64 {
	modelCallCost := int64(0)
	if resp != nil {
		modelCallCost = resp.Cost.TotalMicrosUSD
	}
	s.costSpent += modelCallCost
	return modelCallCost
}

func (s *agentLoopRunState) AddModelCallCost(modelCall int, stepID string, modelCallCost int64, toolCallCount int) {
	s.modelCallCosts = append(s.modelCallCosts, ModelCallCostRecord{
		ModelCall:           modelCall,
		StepID:              stepID,
		CostMicrosUSD:       modelCallCost,
		CumulativeMicrosUSD: s.costSpent,
		ToolCallCount:       toolCallCount,
	})
	if modelCall > s.modelCallCount {
		s.modelCallCount = modelCall
	}
}

func (s *agentLoopRunState) ModelCallCount() int {
	return s.modelCallCount
}

func (s *agentLoopRunState) EnsureModelCallCount(modelCallCount int) {
	if modelCallCount > s.modelCallCount {
		s.modelCallCount = modelCallCount
	}
}

func (s *agentLoopRunState) CostSpent() int64 {
	return s.costSpent
}

func (s *agentLoopRunState) CostCeilingExceededMessage() (string, bool) {
	if s.costCeiling <= 0 || (s.priorCost+s.costSpent) < s.costCeiling {
		return "", false
	}
	total := s.priorCost + s.costSpent
	return fmt.Sprintf("agent loop hit per-task cost ceiling: spent %d µUSD this run + %d µUSD prior = %d µUSD, ceiling %d µUSD", s.costSpent, s.priorCost, total, s.costCeiling), true
}

func (s *agentLoopRunState) Result(status string) *ExecutionResult {
	return s.attachAccounting(&ExecutionResult{
		Status:    status,
		Steps:     s.steps,
		Artifacts: s.artifacts,
	})
}

func (s *agentLoopRunState) attachAccounting(res *ExecutionResult) *ExecutionResult {
	if res == nil {
		return nil
	}
	res.Provider = firstNonEmpty(res.Provider, s.provider)
	res.ProviderKind = firstNonEmpty(res.ProviderKind, s.providerKind)
	if !res.ProviderInstance.Valid() {
		res.ProviderInstance = s.providerInstance
	}
	res.Model = firstNonEmpty(res.Model, s.model)
	res.CostMicrosUSD = s.costSpent
	res.ModelCallCosts = s.modelCallCosts
	res.ModelCallCount = s.modelCallCount
	return res
}

func (s *agentLoopRunState) fenceProviderBoundRequest(req *types.ChatRequest) {
	if req == nil || !req.Requirements.NoProviderFailover || !s.providerInstance.Valid() || s.provider == "" {
		return
	}
	req.Scope.ProviderHint = s.provider
	req.Requirements.ExactProvider = true
	req.Requirements.ProviderInstance = s.providerInstance
	if s.model != "" {
		req.Model = s.model
	}
}
