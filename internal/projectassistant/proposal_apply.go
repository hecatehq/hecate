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

	for idx := len(progress.results); idx < len(proposal.Actions); idx++ {
		action := proposal.Actions[idx]
		result, err := s.applyAction(ctx, action)
		if err != nil {
			partial := ApplyResult{
				ProposalID: proposal.ID,
				Applied:    false,
				Actions:    cloneActionResults(progress.results),
			}
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

	return ApplyResult{ProposalID: proposal.ID, Applied: true, Actions: cloneActionResults(progress.results)}, nil
}

func (s *Service) applyAction(ctx context.Context, action Action) (ActionResult, error) {
	switch normalizeKind(action.Kind) {
	case ActionCreateProject:
		return s.applyCreateProject(ctx, action)
	case ActionUpdateProject:
		return s.applyUpdateProject(ctx, action)
	case ActionAttachProjectRoot:
		return s.applyAttachProjectRoot(ctx, action)
	case ActionRemoveProjectRoot:
		return s.applyRemoveProjectRoot(ctx, action)
	case ActionSetProjectDefaults:
		return s.applySetProjectDefaults(ctx, action)
	case ActionMoveChatSession:
		return s.applyMoveChatSession(ctx, action)
	case ActionCreateRole:
		return s.applyCreateRole(ctx, action)
	case ActionCreateWorkItem:
		return s.applyCreateWorkItem(ctx, action)
	case ActionUpdateWorkItem:
		return s.applyUpdateWorkItem(ctx, action)
	case ActionCreateAssignment:
		return s.applyCreateAssignment(ctx, action)
	case ActionCreateHandoff:
		return s.applyCreateHandoff(ctx, action)
	case ActionCreateMemoryCandidate:
		return s.applyCreateMemoryCandidate(ctx, action)
	default:
		return ActionResult{}, fmt.Errorf("%w: %s", ErrUnknownActionKind, action.Kind)
	}
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
