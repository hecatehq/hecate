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
	// race itself into duplicate durable writes. The proposal ledger records the
	// latest committed action count so retrying the same action set resumes after
	// the last ledgered mutation instead of replaying earlier actions.
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.ensureApplyProposalRecord(ctx, proposal, fingerprint)
	if err != nil {
		return ApplyResult{}, err
	}
	if record.LatestResult != nil && record.LatestResult.Applied {
		return ApplyResult{}, fmt.Errorf("%w: proposal %q was already applied", ErrConflict, proposal.ID)
	}
	if record.Status == ApplyStatusApplied {
		return ApplyResult{}, fmt.Errorf("%w: proposal %q was already applied", ErrConflict, proposal.ID)
	}
	results := applyRecordResults(record)
	if failedActionIndex, err := s.preflightApply(ctx, proposal.Actions, results); err != nil {
		partial := applyResult(proposal.ID, ApplyStatusBlockedBeforeApply, false, len(proposal.Actions), &failedActionIndex, results)
		if _, ledgerErr := s.proposals.RecordApplyAttempt(ctx, applyAttemptForResult(s.idgen("paatt"), confirmed, partial, err)); ledgerErr != nil {
			err = fmt.Errorf("%v; record apply attempt: %w", err, ledgerErr)
		}
		return partial, &ApplyError{
			ProposalID:        proposal.ID,
			FailedActionIndex: failedActionIndex,
			Result:            partial,
			Err:               err,
		}
	}

	for idx := len(results); idx < len(proposal.Actions); idx++ {
		action := proposal.Actions[idx]
		result, err := s.applyAction(ctx, action, results)
		if err != nil {
			partial := applyResult(proposal.ID, ApplyStatusPartialDueToRuntimeFailure, false, len(proposal.Actions), &idx, results)
			if _, ledgerErr := s.proposals.RecordApplyAttempt(ctx, applyAttemptForResult(s.idgen("paatt"), confirmed, partial, err)); ledgerErr != nil {
				err = fmt.Errorf("%v; record apply attempt: %w", err, ledgerErr)
			}
			return partial, &ApplyError{
				ProposalID:        proposal.ID,
				FailedActionIndex: idx,
				Result:            partial,
				Err:               err,
			}
		}
		results = append(results, result)
		progress := applyResult(proposal.ID, ProposalStatusApplying, false, len(proposal.Actions), nil, results)
		if _, err := s.proposals.UpdateProposalApplyState(ctx, proposal.ID, progress); err != nil {
			partial := applyResult(proposal.ID, ApplyStatusPartialDueToRuntimeFailure, false, len(proposal.Actions), &idx, results)
			return partial, &ApplyError{
				ProposalID:        proposal.ID,
				FailedActionIndex: idx,
				Result:            partial,
				Err:               err,
			}
		}
	}

	applied := applyResult(proposal.ID, ApplyStatusApplied, true, len(proposal.Actions), nil, results)
	if _, err := s.proposals.RecordApplyAttempt(ctx, applyAttemptForResult(s.idgen("paatt"), confirmed, applied, nil)); err != nil {
		return applied, err
	}
	return applied, nil
}

func (s *Service) ensureApplyProposalRecord(ctx context.Context, proposal Proposal, fingerprint string) (ProposalRecord, error) {
	if s == nil || s.proposals == nil {
		return ProposalRecord{}, ErrStoreNotConfigured
	}
	record, ok, err := s.proposals.GetProposal(ctx, proposal.ID)
	if err != nil {
		return ProposalRecord{}, err
	}
	if ok {
		if strings.TrimSpace(record.Fingerprint) != fingerprint {
			return ProposalRecord{}, fmt.Errorf("%w: proposal %q action set changed after partial apply", ErrConflict, proposal.ID)
		}
		return record, nil
	}
	return s.storeProposal(ctx, proposal, proposalProjectID(proposal), ProposalSourceApplyRequest, "")
}

func applyRecordResults(record ProposalRecord) []ActionResult {
	if record.LatestResult == nil {
		return nil
	}
	return cloneActionResults(record.LatestResult.Actions)
}

type applyActionSpec struct {
	kind      string
	preflight func(*applyPreflight, context.Context, Action) error
	apply     func(*Service, context.Context, Action, []ActionResult) (ActionResult, error)
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

func (s *Service) applyAction(ctx context.Context, action Action, previous []ActionResult) (ActionResult, error) {
	spec, ok := lookupApplyActionSpec(action.Kind)
	if !ok {
		return ActionResult{}, fmt.Errorf("%w: %s", ErrUnknownActionKind, action.Kind)
	}
	return spec.apply(s, ctx, action, previous)
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

func applyResult(proposalID, status string, applied bool, totalActionCount int, failedActionIndex *int, results []ActionResult) ApplyResult {
	committedActionCount := len(results)
	resumeActionIndex := committedActionCount
	failed := cloneIntPtr(failedActionIndex)
	return ApplyResult{
		ProposalID:           proposalID,
		Status:               status,
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
