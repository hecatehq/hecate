package api

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/cairnlinebridge"
	"github.com/hecatehq/hecate/internal/projectassistant"
)

const projectCairnlineWriteAuthorityProjectAssistantProposals = "project-assistant-proposals"

func (h *Handler) projectAssistantProposalWritesUseCairnlineAuthority() bool {
	return h != nil &&
		h.projectCairnlineEmbeddedConnectorEnabled() &&
		h.config.ProjectsCairnlineWriteAuthorityEnabled(projectCairnlineWriteAuthorityProjectAssistantProposals)
}

func (h *Handler) projectAssistantProposalStoreForApplication() projectassistant.ProposalStore {
	if h == nil {
		return nil
	}
	if h.projectAssistantProposalWritesUseCairnlineAuthority() {
		return cairnlineProjectAssistantProposalAuthorityStore{
			handler: h,
			shadow:  h.projectAssistantProposals,
		}
	}
	return h.projectAssistantProposals
}

type cairnlineProjectAssistantProposalAuthorityStore struct {
	handler *Handler
	shadow  projectassistant.ProposalStore
}

func (s cairnlineProjectAssistantProposalAuthorityStore) Backend() string {
	shadowBackend := "unconfigured"
	if s.shadow != nil {
		shadowBackend = s.shadow.Backend()
	}
	return "cairnline+" + shadowBackend
}

func (s cairnlineProjectAssistantProposalAuthorityStore) UpsertProposal(ctx context.Context, record projectassistant.ProposalRecord) (projectassistant.ProposalRecord, error) {
	if s.handler == nil {
		return projectassistant.ProposalRecord{}, projectassistant.ErrStoreNotConfigured
	}
	projectID, err := projectassistant.ProposalRecordProjectID(record.Proposal, record.ProjectID)
	if err != nil {
		return projectassistant.ProposalRecord{}, err
	}
	record.ProjectID = projectID
	mutationCtx, release, _, err := s.handler.beginProjectAssistantMutation(ctx, record.Proposal, projectID)
	if err != nil {
		return projectassistant.ProposalRecord{}, err
	}
	defer release()
	ctx = mutationCtx
	written, err := s.writeRecord(ctx, record)
	if err != nil {
		return projectassistant.ProposalRecord{}, projectAssistantCairnlineAuthorityError(err)
	}
	shadowed, ok := s.shadowProposalRecord(ctx, "project_assistant_proposal_cairnline_authority_upsert", written)
	if ok {
		return shadowed, nil
	}
	return normalizeProjectAssistantProposalRecordForAuthority(written)
}

func (s cairnlineProjectAssistantProposalAuthorityStore) ListProposals(ctx context.Context, projectID string) ([]projectassistant.ProposalRecord, error) {
	if s.handler == nil {
		return nil, projectassistant.ErrStoreNotConfigured
	}
	projectID = strings.TrimSpace(projectID)
	var records []projectassistant.ProposalRecord
	err := s.handler.withCairnlineEmbeddedService(ctx, func(service *cairnline.Service) error {
		items, err := service.ListAssistantProposals(ctx, projectID)
		if err != nil {
			return err
		}
		records = make([]projectassistant.ProposalRecord, 0, len(items))
		for _, item := range items {
			record, ok := cairnlinebridge.ProjectAssistantProposalRecord(item)
			if !ok {
				continue
			}
			record, err = normalizeProjectAssistantProposalRecordForAuthority(record)
			if err != nil {
				return err
			}
			records = append(records, record)
		}
		return nil
	})
	if err != nil {
		return nil, projectAssistantCairnlineAuthorityError(err)
	}
	return records, nil
}

func (s cairnlineProjectAssistantProposalAuthorityStore) GetProposal(ctx context.Context, id string) (projectassistant.ProposalRecord, bool, error) {
	record, ok, err := s.getRecord(ctx, id)
	if err != nil || !ok {
		return projectassistant.ProposalRecord{}, ok, err
	}
	projectID, err := projectassistant.ProposalRecordProjectID(record.Proposal, record.ProjectID)
	if err != nil {
		return projectassistant.ProposalRecord{}, false, err
	}
	record.ProjectID = projectID
	projectIDs, err := s.handler.projectAssistantMutationProjectIDs(ctx, record.Proposal, projectID)
	if err != nil {
		return projectassistant.ProposalRecord{}, false, err
	}
	mutationCtx, release, err := s.handler.projectMutationGate.beginMany(ctx, projectIDs)
	if err != nil {
		return projectassistant.ProposalRecord{}, false, err
	}
	defer release()
	ctx = mutationCtx
	// The first read discovers the coordination key. Re-read after admission so
	// a delete that won in between cannot be followed by a stale shadow write.
	record, ok, err = s.getRecord(ctx, id)
	if err != nil || !ok {
		return projectassistant.ProposalRecord{}, ok, err
	}
	latestProjectID, err := projectassistant.ProposalRecordProjectID(record.Proposal, record.ProjectID)
	if err != nil {
		return projectassistant.ProposalRecord{}, false, err
	}
	latestProjectIDs, err := s.handler.projectAssistantMutationProjectIDs(ctx, record.Proposal, latestProjectID)
	if err != nil {
		return projectassistant.ProposalRecord{}, false, err
	}
	if !sameProjectMutationIDs(latestProjectIDs, projectIDs) {
		return projectassistant.ProposalRecord{}, false, fmt.Errorf("%w: project assistant proposal changed project scope", projectassistant.ErrConflict)
	}
	record.ProjectID = latestProjectID
	shadowed, shadowOK := s.shadowProposalRecord(ctx, "project_assistant_proposal_cairnline_authority_get", record)
	if shadowOK {
		return shadowed, true, nil
	}
	record, err = normalizeProjectAssistantProposalRecordForAuthority(record)
	if err != nil {
		return projectassistant.ProposalRecord{}, false, err
	}
	return record, true, nil
}

func (s cairnlineProjectAssistantProposalAuthorityStore) UpdateProposalApplyState(ctx context.Context, proposalID string, result projectassistant.ApplyResult) (projectassistant.ProposalRecord, error) {
	record, ok, err := s.GetProposal(ctx, proposalID)
	if err != nil {
		return projectassistant.ProposalRecord{}, err
	}
	if !ok {
		return projectassistant.ProposalRecord{}, projectassistant.ErrNotFound
	}
	mutationCtx, release, projectID, err := s.handler.beginProjectAssistantMutation(ctx, record.Proposal, record.ProjectID)
	if err != nil {
		return projectassistant.ProposalRecord{}, err
	}
	defer release()
	ctx = mutationCtx
	record.ProjectID = projectID
	record = projectAssistantProposalRecordWithApplyResult(record, result)
	written, err := s.writeRecord(ctx, record)
	if err != nil {
		return projectassistant.ProposalRecord{}, projectAssistantCairnlineAuthorityError(err)
	}
	if !s.skipHecateCompatibilityShadow() && s.shadow != nil {
		if shadowed, err := s.shadow.UpdateProposalApplyState(ctx, proposalID, result); err == nil {
			return shadowed, nil
		} else if s.handler != nil {
			s.handler.logCairnlineMirrorError(ctx, "project_assistant_proposal_cairnline_authority_shadow_apply_state", record.ProjectID, err)
		}
	}
	return normalizeProjectAssistantProposalRecordForAuthority(written)
}

func (s cairnlineProjectAssistantProposalAuthorityStore) RecordApplyAttempt(ctx context.Context, attempt projectassistant.ApplyAttempt) (projectassistant.ProposalRecord, error) {
	proposalID := strings.TrimSpace(firstNonEmpty(attempt.ProposalID, attempt.Result.ProposalID))
	if proposalID == "" {
		return projectassistant.ProposalRecord{}, fmt.Errorf("%w: apply attempt proposal_id is required", projectassistant.ErrInvalid)
	}
	attempt.ProposalID = proposalID
	if attempt.Result.ProposalID == "" {
		attempt.Result.ProposalID = proposalID
	}
	record, ok, err := s.GetProposal(ctx, proposalID)
	if err != nil {
		return projectassistant.ProposalRecord{}, err
	}
	if !ok {
		return projectassistant.ProposalRecord{}, projectassistant.ErrNotFound
	}
	mutationCtx, release, projectID, err := s.handler.beginProjectAssistantMutation(ctx, record.Proposal, record.ProjectID)
	if err != nil {
		return projectassistant.ProposalRecord{}, err
	}
	defer release()
	ctx = mutationCtx
	record.ProjectID = projectID
	record = projectAssistantProposalRecordWithApplyResult(record, attempt.Result)
	record.ApplyAttempts = append(record.ApplyAttempts, attempt)
	written, err := s.writeRecord(ctx, record)
	if err != nil {
		return projectassistant.ProposalRecord{}, projectAssistantCairnlineAuthorityError(err)
	}
	if !s.skipHecateCompatibilityShadow() && s.shadow != nil {
		if shadowed, err := s.shadow.RecordApplyAttempt(ctx, attempt); err == nil {
			return shadowed, nil
		} else if s.handler != nil {
			s.handler.logCairnlineMirrorError(ctx, "project_assistant_proposal_cairnline_authority_shadow_apply_attempt", record.ProjectID, err)
		}
	}
	return normalizeProjectAssistantProposalRecordForAuthority(written)
}

func (s cairnlineProjectAssistantProposalAuthorityStore) DeleteProject(ctx context.Context, projectID string) (int, error) {
	if s.shadow == nil {
		return 0, nil
	}
	return s.shadow.DeleteProject(ctx, projectID)
}

func (s cairnlineProjectAssistantProposalAuthorityStore) Clear(ctx context.Context) (int, error) {
	if s.shadow == nil {
		return 0, nil
	}
	return s.shadow.Clear(ctx)
}

func (s cairnlineProjectAssistantProposalAuthorityStore) getRecord(ctx context.Context, id string) (projectassistant.ProposalRecord, bool, error) {
	if s.handler == nil {
		return projectassistant.ProposalRecord{}, false, projectassistant.ErrStoreNotConfigured
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return projectassistant.ProposalRecord{}, false, nil
	}
	var record projectassistant.ProposalRecord
	err := s.handler.withCairnlineEmbeddedService(ctx, func(service *cairnline.Service) error {
		item, err := service.GetAssistantProposal(ctx, id)
		if errors.Is(err, cairnline.ErrNotFound) {
			return projectassistant.ErrNotFound
		}
		if err != nil {
			return err
		}
		projected, ok := cairnlinebridge.ProjectAssistantProposalRecord(item)
		if !ok {
			return projectassistant.ErrNotFound
		}
		record = projected
		return nil
	})
	if errors.Is(err, projectassistant.ErrNotFound) {
		return projectassistant.ProposalRecord{}, false, nil
	}
	if err != nil {
		return projectassistant.ProposalRecord{}, false, projectAssistantCairnlineAuthorityError(err)
	}
	return record, true, nil
}

func (s cairnlineProjectAssistantProposalAuthorityStore) writeRecord(ctx context.Context, record projectassistant.ProposalRecord) (projectassistant.ProposalRecord, error) {
	if s.handler == nil {
		return projectassistant.ProposalRecord{}, projectassistant.ErrStoreNotConfigured
	}
	projectID, err := projectassistant.ProposalRecordProjectID(record.Proposal, record.ProjectID)
	if err != nil {
		return projectassistant.ProposalRecord{}, err
	}
	record.ProjectID = projectID
	mutationCtx, release, _, err := s.handler.beginProjectAssistantMutation(ctx, record.Proposal, projectID)
	if err != nil {
		return projectassistant.ProposalRecord{}, err
	}
	defer release()
	ctx = mutationCtx
	var written projectassistant.ProposalRecord
	err = s.handler.withCairnlineEmbeddedService(ctx, func(service *cairnline.Service) error {
		if err := s.handler.seedProjectMetadataForAssistantProposalRecord(ctx, service, record.ProjectID); err != nil {
			return err
		}
		item, ok := cairnlinebridge.AssistantProposalRecord(record)
		if !ok {
			return fmt.Errorf("%w: project assistant proposal cannot be projected to Cairnline", projectassistant.ErrInvalid)
		}
		imported, err := service.ImportAssistantProposalRecord(ctx, item)
		if err != nil {
			return err
		}
		projected, ok := cairnlinebridge.ProjectAssistantProposalRecord(imported)
		if !ok {
			return fmt.Errorf("%w: Cairnline assistant proposal cannot be projected to Hecate", projectassistant.ErrInvalid)
		}
		written = projected
		return nil
	})
	if err != nil {
		return projectassistant.ProposalRecord{}, err
	}
	return written, nil
}

func (s cairnlineProjectAssistantProposalAuthorityStore) shadowProposalRecord(ctx context.Context, operation string, record projectassistant.ProposalRecord) (projectassistant.ProposalRecord, bool) {
	if s.skipHecateCompatibilityShadow() || s.shadow == nil {
		return projectassistant.ProposalRecord{}, false
	}
	shadowed, err := s.shadow.UpsertProposal(ctx, record)
	if err != nil {
		if s.handler != nil {
			s.handler.logCairnlineMirrorError(ctx, operation, record.ProjectID, err)
		}
		return projectassistant.ProposalRecord{}, false
	}
	return shadowed, true
}

func (s cairnlineProjectAssistantProposalAuthorityStore) skipHecateCompatibilityShadow() bool {
	return s.handler != nil && s.handler.projectCairnlineEmbeddedReplacementModeArmed()
}

func normalizeProjectAssistantProposalRecordForAuthority(record projectassistant.ProposalRecord) (projectassistant.ProposalRecord, error) {
	store := projectassistant.NewMemoryProposalStore()
	normalized, err := store.UpsertProposal(context.Background(), record)
	if err != nil {
		return projectassistant.ProposalRecord{}, err
	}
	for _, attempt := range record.ApplyAttempts {
		normalized, err = store.RecordApplyAttempt(context.Background(), attempt)
		if err != nil {
			return projectassistant.ProposalRecord{}, err
		}
	}
	return normalized, nil
}

func projectAssistantProposalRecordWithApplyResult(record projectassistant.ProposalRecord, result projectassistant.ApplyResult) projectassistant.ProposalRecord {
	if result.ProposalID == "" {
		result.ProposalID = record.ID
	}
	if result.TotalActionCount == 0 {
		result.TotalActionCount = len(record.Proposal.Actions)
	}
	record.LatestResult = &result
	record.Status = firstNonEmpty(result.Status, record.Status, projectassistant.ProposalStatusProposed)
	return record
}

func projectAssistantCairnlineAuthorityError(err error) error {
	if errors.Is(err, cairnline.ErrNotFound) {
		return projectassistant.ErrNotFound
	}
	if errors.Is(err, cairnline.ErrInvalid) {
		return projectassistant.ErrInvalid
	}
	if errors.Is(err, cairnline.ErrConflict) || errors.Is(err, cairnline.ErrDuplicate) {
		return projectassistant.ErrConflict
	}
	return err
}
