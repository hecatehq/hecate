package api

import (
	"context"
	"errors"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/cairnlinebridge"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/projectworkapp"
)

const (
	projectAssignmentStartClaimedByHecate          = "hecate"
	projectAssignmentStartClaimedByExternalAdapter = "external_adapter"
)

func (h *Handler) projectAssignmentStartUsesCairnlineAuthority() bool {
	return h.projectAssignmentWritesUseCairnlineAuthority()
}

func (h *Handler) claimProjectAssignmentStartInCairnlineAuthority(ctx context.Context, project projects.Project, assignment projectwork.Assignment, claimedBy string) (projectwork.Assignment, bool, error) {
	if !h.projectAssignmentStartUsesCairnlineAuthority() {
		return projectwork.Assignment{}, false, nil
	}
	claimedBy = strings.TrimSpace(claimedBy)
	if claimedBy == "" {
		claimedBy = projectAssignmentStartClaimedBy(assignment)
	}
	var recorded projectwork.Assignment
	err := h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		existing, err := service.GetAssignment(ctx, assignment.ProjectID, assignment.ID)
		if err != nil {
			if !errors.Is(err, cairnline.ErrNotFound) {
				return err
			}
			role, profile, seedErr := h.seedProjectAssignmentDependenciesForCairnlineAuthority(ctx, service, project, assignment)
			if seedErr != nil {
				return seedErr
			}
			seeded := assignment
			seeded.DriverKind = resolvedProjectAssignmentDriverKind(seeded.DriverKind, role.DefaultDriverKind)
			if _, upsertErr := cairnlinebridge.UpsertAssignment(ctx, service, seeded, role, profile); upsertErr != nil {
				return upsertErr
			}
		} else if existing.ID != "" && existing.WorkItemID != assignment.WorkItemID {
			return cairnline.ErrNotFound
		}

		claimed, err := service.ClaimAssignment(ctx, assignment.ProjectID, assignment.ID, claimedBy)
		if err != nil {
			if errors.Is(err, cairnline.ErrConflict) {
				if current, getErr := service.GetAssignment(ctx, assignment.ProjectID, assignment.ID); getErr == nil {
					recorded = projectWorkAssignmentFromCairnlineAuthority(current, assignment)
				}
				return projectworkapp.ErrAssignmentStartConflict
			}
			return err
		}
		recorded = projectWorkAssignmentFromCairnlineAuthority(claimed, assignment)
		return nil
	})
	if err != nil {
		if recorded.ID == "" {
			recorded = assignment
		}
		if errors.Is(err, projectworkapp.ErrAssignmentStartConflict) {
			h.shadowProjectAssignmentToHecate(ctx, "project_assignment_cairnline_authority_start_conflict", recorded)
		}
		return recorded, false, err
	}
	if recorded.ID != "" {
		h.shadowProjectAssignmentToHecate(ctx, "project_assignment_cairnline_authority_start_claim", recorded)
	}
	return recorded, true, nil
}

func (h *Handler) releaseProjectAssignmentStartInCairnlineAuthority(ctx context.Context, assignment projectwork.Assignment, claimedBy string) {
	if !h.projectAssignmentStartUsesCairnlineAuthority() {
		return
	}
	claimedBy = strings.TrimSpace(claimedBy)
	if claimedBy == "" {
		claimedBy = projectAssignmentStartClaimedBy(assignment)
	}
	var released projectwork.Assignment
	err := h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		item, err := service.ReleaseAssignment(ctx, assignment.ProjectID, assignment.ID, claimedBy)
		if err != nil {
			if errors.Is(err, cairnline.ErrConflict) || errors.Is(err, cairnline.ErrNotFound) {
				return nil
			}
			return err
		}
		released = projectWorkAssignmentFromCairnlineAuthority(item, assignment)
		return nil
	})
	if err != nil {
		h.logCairnlineMirrorError(ctx, "project_assignment_cairnline_authority_start_release", assignment.ProjectID, err)
		return
	}
	if released.ID != "" {
		h.shadowProjectAssignmentToHecate(ctx, "project_assignment_cairnline_authority_start_release", released)
	}
}

func projectAssignmentStartClaimedBy(assignment projectwork.Assignment) string {
	if strings.TrimSpace(assignment.DriverKind) == projectwork.AssignmentDriverExternalAgent {
		return projectAssignmentStartClaimedByExternalAdapter
	}
	return projectAssignmentStartClaimedByHecate
}
