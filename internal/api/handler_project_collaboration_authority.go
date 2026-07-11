package api

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/cairnlinebridge"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/projectworkapp"
)

const projectCairnlineWriteAuthorityProjectCollaboration = "project-collaboration"

func (h *Handler) projectCollaborationWritesUseCairnlineAuthority() bool {
	return h != nil &&
		h.projectCairnlineEmbeddedConnectorEnabled() &&
		h.config.ProjectsCairnlineWriteAuthorityEnabled(projectCairnlineWriteAuthorityProjectCollaboration)
}

func (h *Handler) createProjectWorkArtifactWithCairnlineAuthority(ctx context.Context, projectID, workItemID string, cmd projectworkapp.CreateArtifactCommand) (projectwork.CollaborationArtifact, error) {
	artifact := projectwork.CollaborationArtifact{
		ID:                     firstNonEmptyString(strings.TrimSpace(cmd.ID), newOpaqueTaskResourceID("art")),
		ProjectID:              projectID,
		WorkItemID:             workItemID,
		AssignmentID:           cmd.AssignmentID,
		Kind:                   cmd.Kind,
		Title:                  cmd.Title,
		Body:                   cmd.Body,
		AuthorRoleID:           cmd.AuthorRoleID,
		EvidenceSourceKind:     cmd.EvidenceSourceKind,
		EvidenceURL:            cmd.EvidenceURL,
		EvidenceExternalID:     cmd.EvidenceExternalID,
		EvidenceProvider:       cmd.EvidenceProvider,
		EvidenceTrustLabel:     cmd.EvidenceTrustLabel,
		ReviewedAssignmentID:   cmd.ReviewedAssignmentID,
		ReviewVerdict:          cmd.ReviewVerdict,
		ReviewRisk:             cmd.ReviewRisk,
		ReviewFollowUpRequired: cmd.ReviewFollowUpRequired,
	}
	if err := validateProjectCollaborationArtifactForCairnlineAuthority(artifact); err != nil {
		return projectwork.CollaborationArtifact{}, err
	}

	var created projectwork.CollaborationArtifact
	err := h.withCairnlineEmbeddedService(ctx, func(service *cairnline.Service) error {
		if err := h.seedProjectArtifactDependenciesForCairnlineAuthority(ctx, service, artifact); err != nil {
			return err
		}
		switch strings.TrimSpace(artifact.Kind) {
		case projectwork.ArtifactKindEvidenceLink:
			item, ok := cairnlinebridge.Evidence(artifact)
			if !ok {
				return errors.Join(cairnline.ErrInvalid, errors.New("artifact kind is not evidence"))
			}
			if item.Title == "" {
				item.Title = "Evidence link"
			}
			recorded, err := service.CreateEvidence(ctx, item)
			if err != nil {
				return err
			}
			created = projectHealthEvidenceFromCairnline(recorded)
		case projectwork.ArtifactKindReview:
			item, ok := cairnlinebridge.Review(artifact)
			if !ok {
				return errors.Join(cairnline.ErrInvalid, errors.New("artifact kind is not review"))
			}
			recorded, err := service.CreateReview(ctx, item)
			if err != nil {
				return err
			}
			created = projectHealthReviewFromCairnline(recorded)
		default:
			item, ok := cairnlinebridge.Artifact(artifact)
			if !ok {
				return errors.Join(cairnline.ErrInvalid, errors.New("artifact kind is not generic collaboration artifact"))
			}
			recorded, err := service.CreateArtifact(ctx, item)
			if err != nil {
				return err
			}
			created = projectWorkArtifactFromCairnline(recorded)
		}
		return nil
	})
	if err != nil {
		return projectwork.CollaborationArtifact{}, err
	}
	h.shadowProjectWorkArtifactToHecate(ctx, "project_artifact_cairnline_authority", created)
	return created, nil
}

func (h *Handler) createProjectHandoffWithCairnlineAuthority(ctx context.Context, projectID, workItemID string, cmd projectworkapp.CreateHandoffCommand) (projectwork.Handoff, error) {
	handoff := projectwork.Handoff{
		ID:                    firstNonEmptyString(strings.TrimSpace(cmd.ID), newOpaqueTaskResourceID("handoff")),
		ProjectID:             projectID,
		WorkItemID:            workItemID,
		SourceAssignmentID:    cmd.SourceAssignmentID,
		SourceRunID:           cmd.SourceRunID,
		SourceChatSessionID:   cmd.SourceChatSessionID,
		SourceMessageID:       cmd.SourceMessageID,
		TargetRoleID:          cmd.TargetRoleID,
		TargetAssignmentID:    cmd.TargetAssignmentID,
		TargetWorkItemID:      cmd.TargetWorkItemID,
		Title:                 cmd.Title,
		Summary:               cmd.Summary,
		RecommendedNextAction: cmd.RecommendedNextAction,
		LinkedArtifactIDs:     append([]string(nil), cmd.LinkedArtifactIDs...),
		LinkedMemoryIDs:       append([]string(nil), cmd.LinkedMemoryIDs...),
		ContextRefs:           append([]string(nil), cmd.ContextRefs...),
		Status:                cmd.Status,
		ProvenanceKind:        cmd.ProvenanceKind,
		TrustLabel:            cmd.TrustLabel,
		CreatedByRoleID:       cmd.CreatedByRoleID,
	}
	if err := validateProjectHandoffForCairnlineAuthority(handoff); err != nil {
		return projectwork.Handoff{}, err
	}
	var created projectwork.Handoff
	err := h.withCairnlineEmbeddedService(ctx, func(service *cairnline.Service) error {
		if err := h.seedProjectHandoffDependenciesForCairnlineAuthority(ctx, service, handoff); err != nil {
			return err
		}
		item := cairnlinebridge.Handoff(handoff)
		recorded, err := service.CreateHandoff(ctx, item)
		if err != nil {
			return err
		}
		created = projectHealthHandoffFromCairnline(recorded)
		return nil
	})
	if err != nil {
		return projectwork.Handoff{}, err
	}
	h.shadowProjectHandoffToHecate(ctx, "project_handoff_cairnline_authority_create", created)
	return created, nil
}

func (h *Handler) updateProjectHandoffWithCairnlineAuthority(ctx context.Context, projectID, workItemID, handoffID string, cmd projectworkapp.UpdateHandoffCommand) (projectwork.Handoff, error) {
	var updated projectwork.Handoff
	err := h.withCairnlineEmbeddedService(ctx, func(service *cairnline.Service) error {
		existing, err := service.GetHandoff(ctx, projectID, workItemID, handoffID)
		if err != nil {
			return err
		}
		handoff := projectHealthHandoffFromCairnline(existing)
		applyProjectHandoffUpdate(&handoff, cmd)
		if err := validateProjectHandoffForCairnlineAuthority(handoff); err != nil {
			return err
		}
		if err := h.seedProjectHandoffDependenciesForCairnlineAuthority(ctx, service, handoff); err != nil {
			return err
		}
		recorded, err := service.UpdateHandoff(ctx, cairnlinebridge.Handoff(handoff))
		if err != nil {
			return err
		}
		updated = projectHealthHandoffFromCairnline(recorded)
		return nil
	})
	if err != nil {
		return projectwork.Handoff{}, err
	}
	h.shadowProjectHandoffToHecate(ctx, "project_handoff_cairnline_authority_update", updated)
	return updated, nil
}

func (h *Handler) deleteProjectHandoffWithCairnlineAuthority(ctx context.Context, projectID, workItemID, handoffID string) error {
	if err := h.withCairnlineEmbeddedService(ctx, func(service *cairnline.Service) error {
		return service.DeleteHandoff(ctx, projectID, workItemID, handoffID)
	}); err != nil {
		return err
	}
	h.shadowProjectHandoffDeleteToHecate(ctx, "project_handoff_cairnline_authority_delete", projectID, workItemID, handoffID)
	return nil
}

func (h *Handler) seedProjectArtifactDependenciesForCairnlineAuthority(ctx context.Context, service *cairnline.Service, artifact projectwork.CollaborationArtifact) error {
	if err := h.writeProjectWorkItemDependencyForCairnlineAuthority(ctx, service, artifact.ProjectID, artifact.WorkItemID); err != nil {
		return err
	}
	if err := h.writeProjectRoleDependencyForCairnlineAuthority(ctx, service, artifact.ProjectID, artifact.AuthorRoleID); err != nil {
		return err
	}
	if err := h.writeProjectAssignmentDependencyForCairnlineAuthority(ctx, service, artifact.ProjectID, artifact.AssignmentID); err != nil {
		return err
	}
	return h.writeProjectAssignmentDependencyForCairnlineAuthority(ctx, service, artifact.ProjectID, artifact.ReviewedAssignmentID)
}

func (h *Handler) seedProjectHandoffDependenciesForCairnlineAuthority(ctx context.Context, service *cairnline.Service, handoff projectwork.Handoff) error {
	if err := h.writeProjectWorkItemDependencyForCairnlineAuthority(ctx, service, handoff.ProjectID, handoff.WorkItemID); err != nil {
		return err
	}
	if err := h.writeProjectWorkItemDependencyForCairnlineAuthority(ctx, service, handoff.ProjectID, handoff.TargetWorkItemID); err != nil {
		return err
	}
	if err := h.writeProjectRoleDependencyForCairnlineAuthority(ctx, service, handoff.ProjectID, handoff.CreatedByRoleID); err != nil {
		return err
	}
	if err := h.writeProjectRoleDependencyForCairnlineAuthority(ctx, service, handoff.ProjectID, handoff.TargetRoleID); err != nil {
		return err
	}
	if err := h.writeProjectAssignmentDependencyForCairnlineAuthority(ctx, service, handoff.ProjectID, handoff.SourceAssignmentID); err != nil {
		return err
	}
	return h.writeProjectAssignmentDependencyForCairnlineAuthority(ctx, service, handoff.ProjectID, handoff.TargetAssignmentID)
}

func (h *Handler) writeProjectWorkItemDependencyForCairnlineAuthority(ctx context.Context, service *cairnline.Service, projectID, workItemID string) error {
	workItemID = strings.TrimSpace(workItemID)
	if workItemID == "" {
		return nil
	}
	if _, err := service.GetWorkItem(ctx, projectID, workItemID); err == nil {
		return nil
	} else if !errors.Is(err, cairnline.ErrNotFound) {
		return err
	}
	if h == nil || h.projectWork == nil {
		return errors.Join(cairnline.ErrNotFound, errors.New("work item not found for Cairnline authority"))
	}
	item, ok, err := h.projectWork.GetWorkItem(ctx, projectID, workItemID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.Join(cairnline.ErrNotFound, errors.New("work item not found for Cairnline authority"))
	}
	project, err := h.projectForCairnlineWriteAuthority(ctx, projectID)
	if err != nil {
		return err
	}
	if err := h.seedProjectWorkItemDependenciesForCairnlineAuthority(ctx, service, project, item); err != nil {
		return err
	}
	_, err = cairnlinebridge.UpsertWorkItem(ctx, service, item)
	return err
}

func (h *Handler) writeProjectRoleDependencyForCairnlineAuthority(ctx context.Context, service *cairnline.Service, projectID, roleID string) error {
	roleID = strings.TrimSpace(roleID)
	if roleID == "" {
		return nil
	}
	if _, err := getCairnlineProjectRoleForAuthority(ctx, service, projectID, roleID); err == nil {
		return nil
	} else if !errors.Is(err, cairnline.ErrNotFound) {
		return err
	}
	role, ok, err := h.loadProjectWorkRoleForCairnlineMirror(ctx, projectID, roleID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.Join(cairnline.ErrNotFound, errors.New("role not found for Cairnline authority"))
	}
	return h.writeProjectRoleRecordToCairnline(ctx, service, role)
}

func (h *Handler) writeProjectAssignmentDependencyForCairnlineAuthority(ctx context.Context, service *cairnline.Service, projectID, assignmentID string) error {
	assignmentID = strings.TrimSpace(assignmentID)
	if assignmentID == "" {
		return nil
	}
	if _, err := service.GetAssignment(ctx, projectID, assignmentID); err == nil {
		return nil
	} else if !errors.Is(err, cairnline.ErrNotFound) {
		return err
	}
	assignment, ok, err := h.loadProjectWorkAssignmentForCairnlineMirror(ctx, projectID, assignmentID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.Join(cairnline.ErrNotFound, errors.New("assignment not found for Cairnline authority"))
	}
	return h.writeProjectAssignmentRecordToCairnline(ctx, service, assignment)
}

func validateProjectHandoffForCairnlineAuthority(handoff projectwork.Handoff) error {
	if strings.TrimSpace(handoff.Title) == "" {
		return fmt.Errorf("%w: handoff title is required", projectwork.ErrInvalid)
	}
	if strings.TrimSpace(handoff.Summary) == "" {
		return fmt.Errorf("%w: handoff summary is required", projectwork.ErrInvalid)
	}
	if strings.TrimSpace(handoff.RecommendedNextAction) == "" {
		return fmt.Errorf("%w: recommended_next_action is required", projectwork.ErrInvalid)
	}
	switch strings.TrimSpace(handoff.Status) {
	case "", projectwork.HandoffStatusPending, projectwork.HandoffStatusAccepted, projectwork.HandoffStatusSuperseded, projectwork.HandoffStatusDismissed:
		return nil
	default:
		return fmt.Errorf("%w: unsupported handoff status %q", projectwork.ErrInvalid, strings.TrimSpace(handoff.Status))
	}
}

func validateProjectCollaborationArtifactForCairnlineAuthority(artifact projectwork.CollaborationArtifact) error {
	kind := strings.TrimSpace(artifact.Kind)
	if kind == "" {
		return fmt.Errorf("%w: collaboration artifact kind is required", projectwork.ErrInvalid)
	}
	switch kind {
	case projectwork.ArtifactKindBrief, projectwork.ArtifactKindHandoff, projectwork.ArtifactKindReview, projectwork.ArtifactKindDecisionNote, projectwork.ArtifactKindEvidenceLink:
	default:
		return fmt.Errorf("%w: unsupported collaboration artifact kind %q", projectwork.ErrInvalid, kind)
	}
	if strings.TrimSpace(artifact.Body) == "" {
		return fmt.Errorf("%w: artifact body is required", projectwork.ErrInvalid)
	}
	if kind == projectwork.ArtifactKindEvidenceLink && strings.TrimSpace(artifact.EvidenceURL) == "" && strings.TrimSpace(artifact.EvidenceExternalID) == "" {
		return fmt.Errorf("%w: evidence_url or evidence_external_id is required for evidence links", projectwork.ErrInvalid)
	}
	if kind == projectwork.ArtifactKindReview {
		if strings.TrimSpace(artifact.ReviewVerdict) == "" {
			return fmt.Errorf("%w: review_verdict is required for Cairnline-authoritative reviews", projectwork.ErrInvalid)
		}
		if !validProjectCollaborationReviewVerdictForCairnlineAuthority(artifact.ReviewVerdict) {
			return fmt.Errorf("%w: unsupported review_verdict %q", projectwork.ErrInvalid, strings.TrimSpace(artifact.ReviewVerdict))
		}
		if strings.TrimSpace(artifact.ReviewRisk) != "" && !validProjectCollaborationReviewRiskForCairnlineAuthority(artifact.ReviewRisk) {
			return fmt.Errorf("%w: unsupported review_risk %q", projectwork.ErrInvalid, strings.TrimSpace(artifact.ReviewRisk))
		}
	}
	return nil
}

func validProjectCollaborationReviewVerdictForCairnlineAuthority(verdict string) bool {
	switch strings.TrimSpace(verdict) {
	case projectwork.ReviewVerdictApproved, projectwork.ReviewVerdictChangesRequested, projectwork.ReviewVerdictBlocked, projectwork.ReviewVerdictRisk:
		return true
	default:
		return false
	}
}

func validProjectCollaborationReviewRiskForCairnlineAuthority(risk string) bool {
	switch strings.TrimSpace(risk) {
	case projectwork.ReviewRiskLow, projectwork.ReviewRiskMedium, projectwork.ReviewRiskHigh, projectwork.ReviewRiskUnknown:
		return true
	default:
		return false
	}
}

func applyProjectHandoffUpdate(item *projectwork.Handoff, cmd projectworkapp.UpdateHandoffCommand) {
	if item == nil {
		return
	}
	if cmd.SourceAssignmentID != nil {
		item.SourceAssignmentID = *cmd.SourceAssignmentID
	}
	if cmd.SourceRunID != nil {
		item.SourceRunID = *cmd.SourceRunID
	}
	if cmd.SourceChatSessionID != nil {
		item.SourceChatSessionID = *cmd.SourceChatSessionID
	}
	if cmd.SourceMessageID != nil {
		item.SourceMessageID = *cmd.SourceMessageID
	}
	if cmd.TargetRoleID != nil {
		item.TargetRoleID = *cmd.TargetRoleID
	}
	if cmd.TargetAssignmentID != nil {
		item.TargetAssignmentID = *cmd.TargetAssignmentID
	}
	if cmd.TargetWorkItemID != nil {
		item.TargetWorkItemID = *cmd.TargetWorkItemID
	}
	if cmd.Title != nil {
		item.Title = *cmd.Title
	}
	if cmd.Summary != nil {
		item.Summary = *cmd.Summary
	}
	if cmd.RecommendedNextAction != nil {
		item.RecommendedNextAction = *cmd.RecommendedNextAction
	}
	if cmd.LinkedArtifactIDs != nil {
		item.LinkedArtifactIDs = append([]string(nil), *cmd.LinkedArtifactIDs...)
	}
	if cmd.LinkedMemoryIDs != nil {
		item.LinkedMemoryIDs = append([]string(nil), *cmd.LinkedMemoryIDs...)
	}
	if cmd.ContextRefs != nil {
		item.ContextRefs = append([]string(nil), *cmd.ContextRefs...)
	}
	if cmd.Status != nil {
		item.Status = *cmd.Status
	}
	if cmd.ProvenanceKind != nil {
		item.ProvenanceKind = *cmd.ProvenanceKind
	}
	if cmd.TrustLabel != nil {
		item.TrustLabel = *cmd.TrustLabel
	}
	if cmd.CreatedByRoleID != nil {
		item.CreatedByRoleID = *cmd.CreatedByRoleID
	}
}

func (h *Handler) shadowProjectWorkArtifactToHecate(ctx context.Context, operation string, artifact projectwork.CollaborationArtifact) {
	if h == nil || h.projectWork == nil {
		return
	}
	if h.projectCairnlineEmbeddedReplacementModeArmed() {
		return
	}
	if _, err := h.projectWork.CreateArtifact(ctx, artifact); err != nil && !errors.Is(err, projectwork.ErrDuplicate) {
		h.logProjectCollaborationShadowError(ctx, operation, artifact.ProjectID, artifact.ID, err)
	}
}

func (h *Handler) shadowProjectHandoffToHecate(ctx context.Context, operation string, handoff projectwork.Handoff) {
	if h == nil || h.projectWork == nil {
		return
	}
	if h.projectCairnlineEmbeddedReplacementModeArmed() {
		return
	}
	if existing, err := h.projectWork.UpdateHandoff(ctx, handoff.ProjectID, handoff.WorkItemID, handoff.ID, func(item *projectwork.Handoff) {
		*item = handoff
	}); err == nil {
		_ = existing
		return
	} else if !errors.Is(err, projectwork.ErrNotFound) {
		h.logProjectCollaborationShadowError(ctx, operation, handoff.ProjectID, handoff.ID, err)
		return
	}
	if _, err := h.projectWork.CreateHandoff(ctx, handoff); err != nil && !errors.Is(err, projectwork.ErrDuplicate) {
		h.logProjectCollaborationShadowError(ctx, operation, handoff.ProjectID, handoff.ID, err)
	}
}

func (h *Handler) shadowProjectHandoffDeleteToHecate(ctx context.Context, operation, projectID, workItemID, handoffID string) {
	if h == nil || h.projectWork == nil {
		return
	}
	if h.projectCairnlineEmbeddedReplacementModeArmed() {
		return
	}
	if err := h.projectWork.DeleteHandoff(ctx, projectID, workItemID, handoffID); err != nil && !errors.Is(err, projectwork.ErrNotFound) {
		h.logProjectCollaborationShadowError(ctx, operation, projectID, handoffID, err)
	}
}

func (h *Handler) logProjectCollaborationShadowError(ctx context.Context, operation, projectID, recordID string, err error) {
	if err == nil || h == nil || h.logger == nil {
		return
	}
	h.logger.WarnContext(ctx, "project collaboration Hecate shadow failed", "operation", operation, "project_id", projectID, "record_id", recordID, "error", err)
}
