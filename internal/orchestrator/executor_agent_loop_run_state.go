package orchestrator

import (
	"fmt"

	"github.com/hecatehq/hecate/pkg/types"
)

type agentLoopRunState struct {
	nextIndex int

	steps     []types.TaskStep
	artifacts []types.TaskArtifact

	provider     string
	providerKind string
	model        string

	costCeiling int64
	priorCost   int64
	costSpent   int64
	turnCosts   []TurnCostRecord
}

func newAgentLoopRunState(spec ExecutionSpec, maxTurns int) *agentLoopRunState {
	baseIndex := 0
	costSpent := int64(0)
	priorCost := int64(0)
	if spec.ResumeCheckpoint != nil {
		if spec.ResumeCheckpoint.LastStepIndex > 0 {
			baseIndex = spec.ResumeCheckpoint.LastStepIndex
		}
		priorCost = spec.ResumeCheckpoint.PriorCostMicrosUSD
		// Same-run mid-approval resume: seed costSpent with the
		// pre-pause spend so ceiling checks and the persisted Total
		// account for it. Cross-run resumes see zero here (new run
		// hasn't spent anything yet).
		costSpent = spec.ResumeCheckpoint.ThisRunCostMicrosUSD
	}
	return &agentLoopRunState{
		nextIndex:   baseIndex + 1,
		steps:       make([]types.TaskStep, 0, maxTurns*2),
		artifacts:   make([]types.TaskArtifact, 0, maxTurns),
		costCeiling: spec.Task.BudgetMicrosUSD,
		priorCost:   priorCost,
		costSpent:   costSpent,
		turnCosts:   make([]TurnCostRecord, 0, maxTurns),
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

func (s *agentLoopRunState) TrackInitialConversationArtifact(artifact *types.TaskArtifact) {
	if artifact != nil && len(s.artifacts) == 0 {
		s.artifacts = append(s.artifacts, *artifact)
	}
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
	if resp.Route.Model != "" {
		s.model = resp.Route.Model
	} else if resp.Model != "" {
		s.model = resp.Model
	}
}

func (s *agentLoopRunState) AccumulateCost(resp *types.ChatResponse) int64 {
	turnCost := int64(0)
	if resp != nil {
		turnCost = resp.Cost.TotalMicrosUSD
	}
	s.costSpent += turnCost
	return turnCost
}

func (s *agentLoopRunState) AddTurnCost(turn int, stepID string, turnCost int64, toolCallCount int) {
	s.turnCosts = append(s.turnCosts, TurnCostRecord{
		Turn:                turn,
		StepID:              stepID,
		CostMicrosUSD:       turnCost,
		CumulativeMicrosUSD: s.costSpent,
		ToolCallCount:       toolCallCount,
	})
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
	res.Model = firstNonEmpty(res.Model, s.model)
	res.CostMicrosUSD = s.costSpent
	res.TurnCosts = s.turnCosts
	return res
}
