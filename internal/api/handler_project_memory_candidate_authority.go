package api

import (
	"context"
	"errors"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/cairnlinebridge"
	"github.com/hecatehq/hecate/internal/memory"
)

func (h *Handler) projectMemoryCandidatesWriteUseCairnlineAuthority() bool {
	return h != nil &&
		h.projectCairnlineEmbeddedConnectorEnabled() &&
		h.config.ProjectsCairnlineWriteAuthorityEnabled("project-memory") &&
		h.config.ProjectsCairnlineWriteAuthorityEnabled("memory-candidates")
}

func (h *Handler) createProjectMemoryCandidateWithCairnlineAuthority(ctx context.Context, projectID string, candidate memory.Candidate) (memory.Candidate, error) {
	var created cairnline.MemoryCandidate
	err := h.withCairnlineEmbeddedService(ctx, func(service *cairnline.Service) error {
		if err := h.seedProjectMetadataForCairnlineMemoryAuthority(ctx, service, projectID); err != nil {
			return err
		}
		item, err := service.CreateMemoryCandidate(ctx, cairnlinebridge.MemoryCandidate(normalizeProjectMemoryCandidateForCairnlineAuthority(candidate)))
		if err != nil {
			return err
		}
		created = item
		return nil
	})
	if err != nil {
		return memory.Candidate{}, err
	}
	return projectMemoryCandidateFromCairnline(created), nil
}

func (h *Handler) promoteProjectMemoryCandidateWithCairnlineAuthority(ctx context.Context, projectID, candidateID string, req promoteProjectMemoryCandidateRequest) (memory.Candidate, memory.Entry, error) {
	var updated cairnline.MemoryCandidate
	var promoted cairnline.MemoryEntry
	err := h.withCairnlineEmbeddedService(ctx, func(service *cairnline.Service) error {
		if err := h.seedProjectMetadataForCairnlineMemoryAuthority(ctx, service, projectID); err != nil {
			return err
		}
		candidate, entry, err := service.PromoteMemoryCandidate(ctx, cairnline.MemoryCandidatePromotion{
			ProjectID:   projectID,
			CandidateID: candidateID,
			Title:       req.Title,
			Body:        req.Body,
			TrustLabel:  req.TrustLabel,
			SourceKind:  req.SourceKind,
			SourceID:    req.SourceID,
			Enabled:     req.Enabled,
		})
		if err != nil {
			return err
		}
		updated = candidate
		promoted = entry
		return nil
	})
	if err != nil {
		return memory.Candidate{}, memory.Entry{}, err
	}
	return projectMemoryCandidateFromCairnline(updated), projectMemoryFromCairnline(promoted), nil
}

func (h *Handler) rejectProjectMemoryCandidateWithCairnlineAuthority(ctx context.Context, projectID, candidateID, reason string) (memory.Candidate, error) {
	var updated cairnline.MemoryCandidate
	err := h.withCairnlineEmbeddedService(ctx, func(service *cairnline.Service) error {
		if err := h.seedProjectMetadataForCairnlineMemoryAuthority(ctx, service, projectID); err != nil {
			return err
		}
		item, err := service.RejectMemoryCandidate(ctx, projectID, candidateID, reason)
		if err != nil {
			return err
		}
		updated = item
		return nil
	})
	if err != nil {
		return memory.Candidate{}, err
	}
	return projectMemoryCandidateFromCairnline(updated), nil
}

func normalizeProjectMemoryCandidateForCairnlineAuthority(candidate memory.Candidate) memory.Candidate {
	candidate.ID = strings.TrimSpace(candidate.ID)
	candidate.ProjectID = strings.TrimSpace(candidate.ProjectID)
	candidate.Title = strings.TrimSpace(candidate.Title)
	candidate.Body = strings.TrimSpace(candidate.Body)
	candidate.SuggestedKind = strings.TrimSpace(candidate.SuggestedKind)
	candidate.SuggestedTrustLabel = strings.TrimSpace(candidate.SuggestedTrustLabel)
	if candidate.SuggestedTrustLabel == "" {
		candidate.SuggestedTrustLabel = memory.TrustLabelGenerated
	}
	candidate.SuggestedSourceKind = strings.TrimSpace(candidate.SuggestedSourceKind)
	if candidate.SuggestedSourceKind == "" {
		candidate.SuggestedSourceKind = memory.SourceKindGenerated
	}
	candidate.SuggestedSourceID = strings.TrimSpace(candidate.SuggestedSourceID)
	candidate.Status = strings.TrimSpace(candidate.Status)
	if candidate.Status == "" {
		candidate.Status = memory.CandidateStatusPending
	}
	candidate.StatusReason = strings.TrimSpace(candidate.StatusReason)
	candidate.PromotedMemoryID = strings.TrimSpace(candidate.PromotedMemoryID)
	return candidate
}

func (h *Handler) shadowProjectMemoryCandidateToHecate(ctx context.Context, operation string, candidate memory.Candidate) {
	if h == nil || h.memoryCandidates == nil {
		return
	}
	if h.projectCairnlineEmbeddedReplacementModeArmed() {
		return
	}
	if _, ok, err := h.memoryCandidates.GetCandidate(ctx, candidate.ProjectID, candidate.ID); err != nil {
		h.logProjectMemoryCandidateShadowError(ctx, operation, candidate.ProjectID, candidate.ID, err)
		return
	} else if ok {
		_, err := h.memoryCandidates.UpdateCandidate(ctx, candidate.ProjectID, candidate.ID, func(item *memory.Candidate) {
			item.Title = candidate.Title
			item.Body = candidate.Body
			item.SuggestedKind = candidate.SuggestedKind
			item.SuggestedTrustLabel = candidate.SuggestedTrustLabel
			item.SuggestedSourceKind = candidate.SuggestedSourceKind
			item.SuggestedSourceID = candidate.SuggestedSourceID
			item.SourceRefs = append([]memory.CandidateSourceRef(nil), candidate.SourceRefs...)
			item.Status = candidate.Status
			item.StatusReason = candidate.StatusReason
			item.PromotedMemoryID = candidate.PromotedMemoryID
		})
		if err != nil {
			h.logProjectMemoryCandidateShadowError(ctx, operation, candidate.ProjectID, candidate.ID, err)
		}
		return
	}
	if _, err := h.memoryCandidates.CreateCandidate(ctx, candidate); err != nil && !errors.Is(err, memory.ErrAlreadyExists) {
		h.logProjectMemoryCandidateShadowError(ctx, operation, candidate.ProjectID, candidate.ID, err)
	}
}

func (h *Handler) logProjectMemoryCandidateShadowError(ctx context.Context, operation, projectID, candidateID string, err error) {
	if err == nil || h == nil || h.logger == nil {
		return
	}
	h.logger.WarnContext(ctx, "project memory candidate Hecate shadow failed", "operation", operation, "project_id", projectID, "candidate_id", candidateID, "error", err)
}
