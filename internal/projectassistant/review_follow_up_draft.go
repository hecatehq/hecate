package projectassistant

import (
	"context"
	"fmt"
	"strings"

	"github.com/hecatehq/hecate/internal/projectwork"
)

type reviewFollowUpDraftTarget struct {
	workItem          projectwork.WorkItem
	artifact          projectwork.CollaborationArtifact
	assignmentByID    map[string]projectwork.Assignment
	defaultRoleID     string
	defaultDriverKind string
}

func (s *Service) defaultReviewFollowUpRoleID(ctx context.Context, input DraftInput) (string, error) {
	target, err := s.loadReviewFollowUpDraftTarget(ctx, input)
	if err != nil {
		return "", err
	}
	return target.defaultRoleID, nil
}

func (s *Service) draftReviewFollowUp(ctx context.Context, input DraftInput, draftContext DraftContext) (Proposal, error) {
	target, err := s.loadReviewFollowUpDraftTarget(ctx, input)
	if err != nil {
		return Proposal{}, err
	}
	roleID := firstNonEmpty(input.RoleID, draftContext.Selection.RoleID, target.defaultRoleID)
	if roleID == "" {
		return Proposal{}, fmt.Errorf("%w: role_id is required for review follow-up drafts", ErrInvalid)
	}
	driverKind := firstNonEmpty(input.DriverKind, draftContext.Selection.DriverKind, target.defaultDriverKind, projectwork.AssignmentDriverHecateTask)
	if !validDraftDriverKind(driverKind) {
		return Proposal{}, fmt.Errorf("%w: unsupported assignment driver_kind %q", ErrInvalid, driverKind)
	}

	handoffID := s.idgen("handoff")
	assignmentID := s.idgen("asgn")
	assignmentPatch := map[string]any{
		"id":           assignmentID,
		"project_id":   target.workItem.ProjectID,
		"work_item_id": target.workItem.ID,
		"role_id":      roleID,
		"driver_kind":  driverKind,
		"status":       projectwork.AssignmentStatusQueued,
	}
	if target.workItem.RootID != "" {
		assignmentPatch["root_id"] = target.workItem.RootID
	}

	title := reviewFollowUpHandoffTitle(target.artifact)
	handoffPatch := map[string]any{
		"id":                      handoffID,
		"project_id":              target.workItem.ProjectID,
		"work_item_id":            target.workItem.ID,
		"source_assignment_id":    target.artifact.AssignmentID,
		"target_role_id":          roleID,
		"target_work_item_id":     target.workItem.ID,
		"title":                   title,
		"summary":                 reviewFollowUpHandoffSummary(target.workItem, target.artifact),
		"recommended_next_action": "Complete the queued follow-up assignment, then close the review follow-up path.",
		"linked_artifact_ids":     []string{target.artifact.ID},
		"status":                  projectwork.HandoffStatusPending,
		"provenance_kind":         "operator",
		"trust_label":             "operator_reviewed",
	}

	return s.Propose(ctx, ProposalInput{
		ProjectID: target.workItem.ProjectID,
		Source:    ProposalSourceReviewFollowUp,
		SourceID:  target.artifact.ID,
		Title:     title,
		Summary:   fmt.Sprintf("Create a review follow-up handoff and queued %s assignment for %q.", driverKind, target.workItem.Title),
		Actions: []Action{
			{
				Kind:   ActionCreateHandoff,
				Target: map[string]string{"project_id": target.workItem.ProjectID, "work_item_id": target.workItem.ID},
				Patch:  mustRawJSON(handoffPatch),
				Reason: "Record the review follow-up path for operator review.",
			},
			{
				Kind:   ActionCreateAssignment,
				Target: map[string]string{"project_id": target.workItem.ProjectID, "work_item_id": target.workItem.ID},
				Patch:  mustRawJSON(assignmentPatch),
				Reason: "Queue the follow-up assignment without starting execution.",
			},
			{
				Kind: ActionUpdateHandoff,
				Target: map[string]string{
					"project_id":   target.workItem.ProjectID,
					"work_item_id": target.workItem.ID,
					"handoff_id":   handoffID,
				},
				Patch: mustRawJSON(updateHandoffPatch{
					TargetAssignmentID: stringPtr(assignmentID),
					TargetRoleID:       stringPtr(roleID),
					Status:             stringPtr(projectwork.HandoffStatusAccepted),
				}),
				Reason: "Link the queued assignment back to the review follow-up handoff.",
			},
		},
		TraceID: strings.TrimSpace(input.TraceID),
	})
}

func (s *Service) loadReviewFollowUpDraftTarget(ctx context.Context, input DraftInput) (reviewFollowUpDraftTarget, error) {
	if s == nil || s.projects == nil || s.work == nil {
		return reviewFollowUpDraftTarget{}, ErrStoreNotConfigured
	}
	projectID := strings.TrimSpace(input.ProjectID)
	workItemID := strings.TrimSpace(input.WorkItemID)
	artifactID := strings.TrimSpace(input.ReviewArtifactID)
	if projectID == "" || workItemID == "" || artifactID == "" {
		return reviewFollowUpDraftTarget{}, fmt.Errorf("%w: project_id, work_item_id, and review_artifact_id are required", ErrInvalid)
	}
	if _, err := s.requireProject(ctx, projectID); err != nil {
		return reviewFollowUpDraftTarget{}, err
	}
	workItem, found, err := s.work.GetWorkItem(ctx, projectID, workItemID)
	if err != nil {
		return reviewFollowUpDraftTarget{}, err
	}
	if !found {
		return reviewFollowUpDraftTarget{}, fmt.Errorf("%w: project work item %q", ErrNotFound, workItemID)
	}
	artifacts, err := s.work.ListArtifacts(ctx, projectwork.ArtifactFilter{ProjectID: projectID, WorkItemID: workItemID})
	if err != nil {
		return reviewFollowUpDraftTarget{}, err
	}
	var artifact projectwork.CollaborationArtifact
	for _, item := range artifacts {
		if item.ID == artifactID {
			artifact = item
			break
		}
	}
	if artifact.ID == "" {
		return reviewFollowUpDraftTarget{}, fmt.Errorf("%w: review artifact %q", ErrNotFound, artifactID)
	}
	handoffs, err := s.work.ListHandoffs(ctx, projectwork.HandoffFilter{ProjectID: projectID, WorkItemID: workItemID})
	if err != nil {
		return reviewFollowUpDraftTarget{}, err
	}
	if !projectwork.ReviewArtifactRequiresFollowUp(artifact) {
		return reviewFollowUpDraftTarget{}, fmt.Errorf("%w: review artifact %q does not require follow-up", ErrInvalid, artifactID)
	}
	if !projectwork.ReviewArtifactNeedsFollowUpPath(artifact, handoffs) {
		return reviewFollowUpDraftTarget{}, fmt.Errorf("%w: review artifact %q already has a follow-up path", ErrConflict, artifactID)
	}
	assignments, err := s.work.ListAssignments(ctx, projectwork.AssignmentFilter{ProjectID: projectID, WorkItemID: workItemID})
	if err != nil {
		return reviewFollowUpDraftTarget{}, err
	}
	assignmentByID := make(map[string]projectwork.Assignment, len(assignments))
	for _, assignment := range assignments {
		assignmentByID[assignment.ID] = assignment
	}
	defaultRoleID := strings.TrimSpace(workItem.OwnerRoleID)
	defaultDriverKind := ""
	if reviewed, ok := assignmentByID[strings.TrimSpace(artifact.ReviewedAssignmentID)]; ok {
		defaultRoleID = reviewed.RoleID
		defaultDriverKind = reviewed.DriverKind
	}
	return reviewFollowUpDraftTarget{
		workItem:          workItem,
		artifact:          artifact,
		assignmentByID:    assignmentByID,
		defaultRoleID:     defaultRoleID,
		defaultDriverKind: defaultDriverKind,
	}, nil
}

func reviewFollowUpHandoffTitle(artifact projectwork.CollaborationArtifact) string {
	title := firstNonEmpty(artifact.Title, "Review")
	if strings.Contains(strings.ToLower(title), "follow-up") {
		return title
	}
	return title + " follow-up"
}

func reviewFollowUpHandoffSummary(workItem projectwork.WorkItem, artifact projectwork.CollaborationArtifact) string {
	title := firstNonEmpty(artifact.Title, artifact.ID)
	if body := strings.TrimSpace(artifact.Body); body != "" {
		return fmt.Sprintf("Follow up on review artifact %s for %q.\n\n%s", title, workItem.Title, body)
	}
	return fmt.Sprintf("Follow up on review artifact %s for %q.", title, workItem.Title)
}

func stringPtr(value string) *string {
	return &value
}
