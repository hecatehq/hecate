package projectassistant

import (
	"context"
	"fmt"
	"strings"
)

func (s *Service) Apply(ctx context.Context, proposal Proposal, confirmed bool) (ApplyResult, error) {
	if s == nil {
		return ApplyResult{}, ErrStoreNotConfigured
	}
	proposal.ID = strings.TrimSpace(proposal.ID)
	if proposal.ID == "" {
		return ApplyResult{}, fmt.Errorf("%w: proposal id is required", ErrInvalid)
	}
	if proposal.RequiresConfirmation && !confirmed {
		return ApplyResult{}, ErrConfirmationRequired
	}
	for _, action := range proposal.Actions {
		if err := validateActionShape(action); err != nil {
			return ApplyResult{}, err
		}
	}
	fingerprint, err := actionSetFingerprint(proposal.Actions)
	if err != nil {
		return ApplyResult{}, err
	}

	// Hold the apply lock through the mutation sequence so a proposal ID cannot
	// race itself into duplicate durable writes. Progress is intentionally
	// in-process for v0; retrying the exact same action set resumes after the
	// last committed action instead of replaying earlier mutations.
	s.mu.Lock()
	defer s.mu.Unlock()

	progress, ok := s.applyProgress[proposal.ID]
	if !ok {
		progress = &applyProgress{fingerprint: fingerprint}
		s.applyProgress[proposal.ID] = progress
	}
	if progress.complete {
		return ApplyResult{}, fmt.Errorf("%w: proposal %q was already applied", ErrConflict, proposal.ID)
	}
	if progress.fingerprint != fingerprint {
		return ApplyResult{}, fmt.Errorf("%w: proposal %q action set changed after partial apply", ErrConflict, proposal.ID)
	}
	if failedActionIndex, err := s.preflightApply(ctx, proposal.Actions, len(progress.results)); err != nil {
		partial := applyResult(proposal.ID, false, len(proposal.Actions), &failedActionIndex, progress.results)
		return partial, &ApplyError{
			ProposalID:        proposal.ID,
			FailedActionIndex: failedActionIndex,
			Result:            partial,
			Err:               err,
		}
	}

	for idx := len(progress.results); idx < len(proposal.Actions); idx++ {
		action := proposal.Actions[idx]
		result, err := s.applyAction(ctx, action)
		if err != nil {
			partial := applyResult(proposal.ID, false, len(proposal.Actions), &idx, progress.results)
			return partial, &ApplyError{
				ProposalID:        proposal.ID,
				FailedActionIndex: idx,
				Result:            partial,
				Err:               err,
			}
		}
		progress.results = append(progress.results, result)
	}

	progress.complete = true

	return applyResult(proposal.ID, true, len(proposal.Actions), nil, progress.results), nil
}

type applyActionSpec struct {
	kind      string
	preflight func(*applyPreflight, context.Context, Action) error
	apply     func(*Service, context.Context, Action) (ActionResult, error)
}

// applyActionSpecs is the maintenance contract between preflight and durable
// apply. Add new Project Assistant mutation kinds here so the stale-target
// preflight and the store-writing handler stay visibly paired.
var applyActionSpecs = []applyActionSpec{
	{kind: ActionCreateProject, preflight: (*applyPreflight).createProject, apply: (*Service).applyCreateProject},
	{kind: ActionUpdateProject, preflight: (*applyPreflight).updateProject, apply: (*Service).applyUpdateProject},
	{kind: ActionAttachProjectRoot, preflight: (*applyPreflight).attachProjectRoot, apply: (*Service).applyAttachProjectRoot},
	{kind: ActionRemoveProjectRoot, preflight: (*applyPreflight).removeProjectRoot, apply: (*Service).applyRemoveProjectRoot},
	{kind: ActionSetProjectDefaults, preflight: (*applyPreflight).setProjectDefaults, apply: (*Service).applySetProjectDefaults},
	{kind: ActionMoveChatSession, preflight: (*applyPreflight).moveChatSession, apply: (*Service).applyMoveChatSession},
	{kind: ActionCreateRole, preflight: (*applyPreflight).createRole, apply: (*Service).applyCreateRole},
	{kind: ActionCreateWorkItem, preflight: (*applyPreflight).createWorkItem, apply: (*Service).applyCreateWorkItem},
	{kind: ActionUpdateWorkItem, preflight: (*applyPreflight).updateWorkItem, apply: (*Service).applyUpdateWorkItem},
	{kind: ActionCreateAssignment, preflight: (*applyPreflight).createAssignment, apply: (*Service).applyCreateAssignment},
	{kind: ActionCreateHandoff, preflight: (*applyPreflight).createHandoff, apply: (*Service).applyCreateHandoff},
	{kind: ActionUpdateHandoff, preflight: (*applyPreflight).updateHandoff, apply: (*Service).applyUpdateHandoff},
	{kind: ActionCreateMemoryCandidate, preflight: (*applyPreflight).createMemoryCandidate, apply: (*Service).applyCreateMemoryCandidate},
}

func lookupApplyActionSpec(kind string) (applyActionSpec, bool) {
	kind = normalizeKind(kind)
	for _, spec := range applyActionSpecs {
		if spec.kind == kind {
			return spec, true
		}
	}
	return applyActionSpec{}, false
}

func (s *Service) applyAction(ctx context.Context, action Action) (ActionResult, error) {
	spec, ok := lookupApplyActionSpec(action.Kind)
	if !ok {
		return ActionResult{}, fmt.Errorf("%w: %s", ErrUnknownActionKind, action.Kind)
	}
	return spec.apply(s, ctx, action)
}

func cloneActionResults(results []ActionResult) []ActionResult {
	if results == nil {
		return nil
	}
	cloned := make([]ActionResult, len(results))
	for idx, result := range results {
		cloned[idx] = ActionResult{
			Kind: result.Kind,
			ID:   result.ID,
			Data: cloneStringMap(result.Data),
		}
	}
	return cloned
}

func applyResult(proposalID string, applied bool, totalActionCount int, failedActionIndex *int, results []ActionResult) ApplyResult {
	committedActionCount := len(results)
	resumeActionIndex := committedActionCount
	failed := cloneIntPtr(failedActionIndex)
	return ApplyResult{
		ProposalID:           proposalID,
		Applied:              applied,
		Actions:              cloneActionResults(results),
		TotalActionCount:     totalActionCount,
		CommittedActionCount: committedActionCount,
		FailedActionIndex:    failed,
		ResumeActionIndex:    resumeActionIndex,
	}
}

func cloneIntPtr(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
