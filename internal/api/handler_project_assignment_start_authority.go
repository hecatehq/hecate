package api

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/cairnlinebridge"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/projectworkapp"
)

const (
	projectAssignmentStartClaimedByHecate          = "hecate"
	projectAssignmentStartClaimedByExternalAdapter = "external_adapter"
	projectAssignmentStartClaimedByOperator        = cairnlinebridge.AssignmentClaimedByOperator
)

func (h *Handler) projectAssignmentStartUsesCairnlineAuthority() bool {
	return h.projectAssignmentWritesUseCairnlineAuthority()
}

func (h *Handler) claimProjectAssignmentStartInCairnlineAuthority(
	ctx context.Context,
	project projects.Project,
	assignment projectwork.Assignment,
	claimedBy string,
	expectedCoordination *cairnline.AssignmentCoordination,
) (projectwork.Assignment, bool, error) {
	if !h.projectAssignmentStartUsesCairnlineAuthority() {
		return projectwork.Assignment{}, false, nil
	}
	claimedBy = strings.TrimSpace(claimedBy)
	if claimedBy == "" {
		claimedBy = projectAssignmentStartClaimedBy(assignment)
	}
	var recorded projectwork.Assignment
	err := h.withCairnlineEmbeddedService(ctx, func(service *cairnline.Service) error {
		var expected cairnline.AssignmentCoordination
		existing, err := service.GetAssignment(ctx, assignment.ProjectID, assignment.ID)
		if err != nil {
			if !errors.Is(err, cairnline.ErrNotFound) {
				return err
			}
			role, seedErr := h.seedProjectAssignmentDependenciesForCairnlineAuthority(ctx, service, project, assignment)
			if seedErr != nil {
				return seedErr
			}
			seeded := assignment
			seeded.DriverKind = resolvedProjectAssignmentDriverKind(seeded.DriverKind, role.DefaultDriverKind)
			seededPortable, upsertErr := cairnlinebridge.UpsertAssignment(ctx, service, seeded, role)
			if upsertErr != nil {
				return upsertErr
			}
			existing = seededPortable
		} else {
			if existing.ID != "" && existing.WorkItemID != assignment.WorkItemID {
				return cairnline.ErrNotFound
			}
			if existing.ExecutionMode == cairnline.ExecutionManual &&
				existing.Status == cairnline.AssignmentClaimed &&
				strings.TrimSpace(existing.ClaimedBy) == projectAssignmentStartClaimedByOperator &&
				strings.TrimSpace(claimedBy) == projectAssignmentStartClaimedByOperator &&
				existing.ExecutionRef.Empty() &&
				strings.TrimSpace(existing.ContextSnapshotID) == "" {
				expected = projectAssignmentExpectedStartCoordination(assignment, existing, expectedCoordination)
				if !projectAssignmentClaimMatchesStart(existing, expected) {
					recorded = projectWorkAssignmentFromCairnlineAuthority(existing, assignment)
					return projectworkapp.ErrAssignmentStartConflict
				}
				recorded = projectWorkAssignmentFromCairnlineAuthority(existing, assignment)
				return nil
			}
		}
		expected = projectAssignmentExpectedStartCoordination(assignment, existing, expectedCoordination)

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
		if !projectAssignmentClaimMatchesStart(claimed, expected) {
			if _, releaseErr := service.ReleaseAssignment(ctx, assignment.ProjectID, assignment.ID, claimedBy); releaseErr != nil && !errors.Is(releaseErr, cairnline.ErrConflict) {
				return releaseErr
			}
			recorded = projectWorkAssignmentFromCairnlineAuthority(claimed, assignment)
			return projectworkapp.ErrAssignmentStartConflict
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

func projectAssignmentExpectedStartCoordination(
	assignment projectwork.Assignment,
	portable cairnline.Assignment,
	expected *cairnline.AssignmentCoordination,
) cairnline.AssignmentCoordination {
	if expected != nil {
		return *expected
	}
	coordination := portable.Coordination()
	coordination.WorkItemID = strings.TrimSpace(assignment.WorkItemID)
	coordination.RoleID = strings.TrimSpace(assignment.RoleID)
	coordination.RootID = strings.TrimSpace(assignment.RootID)
	coordination.ExecutionMode = cairnlinebridge.ExecutionMode(assignment.DriverKind)
	coordination.DesiredAgent.Kind = cairnlinebridge.DesiredAgentKind(assignment.DriverKind)
	return coordination
}

func projectAssignmentClaimMatchesStart(claimed cairnline.Assignment, expected cairnline.AssignmentCoordination) bool {
	actual := claimed.Coordination()
	return actual.WorkItemID == expected.WorkItemID &&
		actual.RoleID == expected.RoleID &&
		actual.RootID == expected.RootID &&
		actual.ExecutionMode == expected.ExecutionMode &&
		actual.DesiredAgent.Kind == expected.DesiredAgent.Kind &&
		slices.Equal(actual.DesiredAgent.SkillIDs, expected.DesiredAgent.SkillIDs)
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
	err := h.withCairnlineEmbeddedService(ctx, func(service *cairnline.Service) error {
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
	switch strings.TrimSpace(assignment.DriverKind) {
	case projectwork.AssignmentDriverExternalAgent:
		return projectAssignmentStartClaimedByExternalAdapter
	case projectwork.AssignmentDriverManual:
		return projectAssignmentStartClaimedByOperator
	default:
		return projectAssignmentStartClaimedByHecate
	}
}

func (h *Handler) startManualProjectAssignmentWithCairnlineAuthority(
	ctx context.Context,
	project projects.Project,
	assignment projectwork.Assignment,
	expectedCoordination *cairnline.AssignmentCoordination,
) (projectwork.Assignment, error) {
	claimedBy := projectAssignmentStartClaimedBy(assignment)
	claimed, claimedOK, err := h.claimProjectAssignmentStartInCairnlineAuthority(ctx, project, assignment, claimedBy, expectedCoordination)
	if err != nil {
		return claimed, err
	}

	var recorded projectwork.Assignment
	err = h.withCairnlineEmbeddedService(ctx, func(service *cairnline.Service) error {
		started, updateErr := service.UpdateAssignmentStatus(ctx, assignment.ProjectID, assignment.ID, cairnline.AssignmentRunning, cairnline.ExecutionRef{})
		if updateErr != nil {
			return updateErr
		}
		recorded = projectWorkAssignmentFromCairnlineAuthority(started, claimed)
		return nil
	})
	if err != nil {
		if claimedOK {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			h.releaseProjectAssignmentStartInCairnlineAuthority(cleanupCtx, assignment, claimedBy)
			cancel()
		}
		return assignment, err
	}

	h.shadowProjectAssignmentToHecate(ctx, "project_assignment_cairnline_authority_manual_start", recorded)
	return recorded, nil
}
