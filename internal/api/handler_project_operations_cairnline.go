package api

import (
	"context"
	"strings"
	"time"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/cairnlinebridge"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectwork"
)

func (h *Handler) projectReadRoutesUseCairnlineReadModel() bool {
	if h == nil || !h.projectCairnlineEmbeddedConnectorEnabled() {
		return false
	}
	if h.requiresEmbeddedCairnlineProjectReads() {
		return true
	}
	ready, _ := h.cairnlineReadModelReadiness()
	return ready
}

func (h *Handler) renderCairnlineProjectOperationsBrief(ctx context.Context, projectID string) (ProjectOperationsBriefResponse, error) {
	view, err := h.cairnlineProjectWorkView(ctx, projectID)
	if err != nil {
		return ProjectOperationsBriefResponse{}, err
	}
	defer view.Close()
	return h.renderCairnlineProjectOperationsBriefFromService(ctx, view.snapshot.Project, view.service, view.snapshot)
}

func (h *Handler) renderCairnlineProjectOperationsBriefFromService(ctx context.Context, project projects.Project, service *cairnline.Service, snapshot cairnlinebridge.Snapshot) (ProjectOperationsBriefResponse, error) {
	activity, err := h.renderCairnlineProjectActivityFromService(ctx, service, snapshot)
	if err != nil {
		return ProjectOperationsBriefResponse{}, err
	}
	cairnlineWorkItems, err := service.ListWorkItems(ctx, snapshot.Project.ID)
	if err != nil {
		return ProjectOperationsBriefResponse{}, err
	}
	assignments, err := h.cairnlineProjectAssignments(ctx, service, snapshot)
	if err != nil {
		return ProjectOperationsBriefResponse{}, err
	}
	cairnlineRoles, err := service.ListRoles(ctx, snapshot.Project.ID)
	if err != nil {
		return ProjectOperationsBriefResponse{}, err
	}
	workItems := projectWorkItemsFromCairnline(cairnlineWorkItems)
	artifacts, err := projectHealthCairnlineArtifacts(ctx, service, snapshot.Project.ID, cairnlineWorkItems)
	if err != nil {
		return ProjectOperationsBriefResponse{}, err
	}
	handoffs, err := projectHealthCairnlineHandoffs(ctx, service, snapshot.Project.ID, cairnlineWorkItems)
	if err != nil {
		return ProjectOperationsBriefResponse{}, err
	}
	pendingCandidates, err := service.ListMemoryCandidates(ctx, cairnline.MemoryCandidateFilter{
		ProjectID: snapshot.Project.ID,
		Status:    cairnline.MemoryCandidatePending,
	})
	if err != nil {
		return ProjectOperationsBriefResponse{}, err
	}

	items := make([]ProjectOperationsBriefItemResponse, 0, projectOperationsBriefItemLimit)
	items = append(items, projectDefaultOperationItems(project, assignments, projectSetupRolesFromCairnline(cairnlineRoles))...)
	items = append(items, assignmentOperationItems(activity)...)
	items = append(items, handoffOperationItems(project.ID, handoffs)...)
	items = append(items, selectedWorkFollowThroughOperationItems(project.ID, workItems, assignments, artifacts, handoffs)...)
	if len(pendingCandidates) > 0 {
		items = append(items, memoryCandidateOperationItem(project.ID, len(pendingCandidates)))
	}
	items = append(items, assignmentGapOperationItems(project.ID, workItems, assignments)...)
	if len(items) == 0 {
		if item := latestWorkOperationItem(project.ID, workItems); item != nil {
			items = append(items, *item)
		}
	}

	sortProjectOperationsItems(items)
	availableItemCount := len(items)
	items = boundedProjectOperationsItems(items, projectOperationsBriefItemLimit)
	response := ProjectOperationsBriefResponse{
		ProjectID:   project.ID,
		GeneratedAt: formatOptionalTime(time.Now().UTC()),
		ReadBackend: "cairnline",
		Items:       items,
	}
	response.Summary.ItemCount = len(items)
	response.Summary.AvailableItemCount = availableItemCount
	response.Summary.OmittedItemCount = availableItemCount - len(items)
	response.Summary.ItemLimit = projectOperationsBriefItemLimit
	response.Summary.PendingMemoryCandidateCount = len(pendingCandidates)
	for _, handoff := range handoffs {
		if handoff.Status == projectwork.HandoffStatusPending {
			response.Summary.PendingHandoffCount++
		}
	}
	for _, item := range items {
		switch item.Priority {
		case projectOperationsPriorityHigh:
			response.Summary.HighCount++
		case projectOperationsPriorityMedium:
			response.Summary.MediumCount++
		case projectOperationsPriorityLow:
			response.Summary.LowCount++
		}
	}
	return response, nil
}

func projectOperationItemFromCairnline(projectID string, item cairnline.ProjectOperationItem, workItems map[string]projectwork.WorkItem, assignments map[string]projectwork.Assignment, assignmentsByWorkItem map[string][]projectwork.Assignment, artifacts map[string]projectwork.CollaborationArtifact, handoffs map[string]projectwork.Handoff) (ProjectOperationsBriefItemResponse, bool) {
	switch strings.TrimSpace(item.Kind) {
	case cairnline.ProjectOperationKindAssignment:
		return assignmentOperationItemFromCairnline(projectID, item, workItems, assignments)
	case cairnline.ProjectOperationKindHandoff:
		handoff, ok := handoffs[item.ArtifactID]
		if !ok {
			return genericCairnlineOperationItem(projectID, item, "review_pending_handoff", projectOperationsPriorityMedium, "Open handoff"), true
		}
		return handoffOperationItem(projectID, handoff), true
	case cairnline.ProjectOperationKindMissingEvidence:
		workItem, workOK := workItems[item.WorkItemID]
		assignment, assignmentOK := assignments[item.AssignmentID]
		if workOK && assignmentOK {
			return completionEvidenceOperationItem(projectID, workItem, assignment), true
		}
		return genericCairnlineOperationItem(projectID, item, "record_completion_evidence", projectOperationsPriorityLow, "Open work"), true
	case cairnline.ProjectOperationKindReviewFollowUp:
		workItem, workOK := workItems[item.WorkItemID]
		artifact, artifactOK := artifacts[item.ArtifactID]
		if workOK && artifactOK {
			return reviewFollowUpOperationItem(projectID, workItem, artifact), true
		}
		return genericCairnlineOperationItem(projectID, item, "review_follow_up", projectOperationsPriorityMedium, "Open review"), true
	case cairnline.ProjectOperationKindCloseoutReady:
		workItem, ok := workItems[item.WorkItemID]
		if !ok {
			return genericCairnlineOperationItem(projectID, item, "close_work_item", projectOperationsPriorityLow, "Open closeout"), true
		}
		return closeWorkItemOperationItem(projectID, workItem, assignmentsByWorkItem[workItem.ID]), true
	case cairnline.ProjectOperationKindWorkItem:
		workItem, ok := workItems[item.WorkItemID]
		if !ok {
			return genericCairnlineOperationItem(projectID, item, "prepare_first_assignment", projectOperationsPriorityMedium, "Draft assignment"), true
		}
		rendered := renderProjectActivityWorkItem(renderProjectWorkItem(workItem))
		target := ProjectOperationsBriefTargetResponse{
			Surface:    "work",
			ProjectID:  projectID,
			WorkItemID: workItem.ID,
		}
		return ProjectOperationsBriefItemResponse{
			ID:          projectOperationsItemID("prepare_first_assignment", projectID, workItem.ID),
			Kind:        "prepare_first_assignment",
			Priority:    projectOperationsPriorityMedium,
			Title:       "Prepare first assignment: " + firstNonEmpty(workItem.Title, workItem.ID),
			Detail:      firstNonEmpty(item.Detail, "This work item has no queued or running assignments yet."),
			ActionLabel: "Draft assignment",
			Status:      firstNonEmpty(workItem.Status, projectwork.WorkItemStatusReady),
			Target:      target,
			Action:      projectOperationsDraftProjectProposalAction(projectID, workItem.ID, "Queue an assignment for "+firstNonEmpty(workItem.Title, workItem.ID)),
			WorkItem:    &rendered,
			UpdatedAt:   formatOptionalTime(firstNonZeroTime(item.UpdatedAt, projectworkTime(workItem.UpdatedAt, workItem.CreatedAt))),
		}, true
	case cairnline.ProjectOperationKindMemoryCandidate:
		return ProjectOperationsBriefItemResponse{}, false
	default:
		return genericCairnlineOperationItem(projectID, item, strings.TrimSpace(item.Kind), cairnlineOperationPriority(item), "Open work"), true
	}
}

func assignmentOperationItemFromCairnline(projectID string, item cairnline.ProjectOperationItem, workItems map[string]projectwork.WorkItem, assignments map[string]projectwork.Assignment) (ProjectOperationsBriefItemResponse, bool) {
	assignment, assignmentOK := assignments[item.AssignmentID]
	workItem := workItems[item.WorkItemID]
	if !assignmentOK {
		return genericCairnlineOperationItem(projectID, item, "inspect_stale_assignment", projectOperationsPriorityHigh, "Open work"), true
	}
	status := strings.TrimSpace(item.Status)
	status = firstNonEmpty(assignment.Status, status)
	if assignment.Status == projectwork.AssignmentStatusAwaitingApproval ||
		assignment.ExecutionRef.Status == projectwork.AssignmentStatusAwaitingApproval ||
		assignment.ExecutionRef.PendingApprovalCount > 0 {
		return assignmentOperationItemFromHecate(projectID, workItem, assignment, "approve_assignment", projectOperationsPriorityHigh, "Review pending approval", projectActivityApprovalSummary(assignment), "Open approval", "awaiting_approval", "blocked"), true
	}
	switch status {
	case projectwork.AssignmentStatusQueued:
		if assignment.DriverKind == projectwork.AssignmentDriverManual {
			return assignmentOperationItemFromHecate(projectID, workItem, assignment, "start_queued_assignment", projectOperationsPriorityHigh, "Human work ready", "This assignment is ready for a person to begin.", "Open work", "not_started", "blocked"), true
		}
		return assignmentOperationItemFromHecate(projectID, workItem, assignment, "start_queued_assignment", projectOperationsPriorityHigh, "Review queued assignment", "Open launch preflight before starting this assignment.", "Review start", "not_started", "blocked"), true
	case projectwork.AssignmentStatusFailed:
		return assignmentOperationItemFromHecate(projectID, workItem, assignment, "review_failed_assignment", projectOperationsPriorityHigh, "Review failed assignment", firstNonEmpty(item.Detail, "failed run"), "Open work", "failed", "blocked"), true
	case projectwork.AssignmentStatusCancelled:
		return assignmentOperationItemFromHecate(projectID, workItem, assignment, "review_cancelled_assignment", projectOperationsPriorityMedium, "Review cancelled assignment", firstNonEmpty(item.Detail, "cancelled"), "Open work", "cancelled", "blocked"), true
	case projectwork.AssignmentStatusRunning, cairnline.AssignmentClaimed, cairnline.AssignmentReview:
		return assignmentOperationItemFromHecate(projectID, workItem, assignment, "inspect_active_assignment", projectOperationsPriorityLow, "Inspect active assignment", firstNonEmpty(item.Detail, "running"), "Inspect work", "running", "active"), true
	default:
		return assignmentOperationItemFromHecate(projectID, workItem, assignment, "inspect_stale_assignment", projectOperationsPriorityHigh, "Inspect stale assignment link", firstNonEmpty(item.Detail, "stale assignment link"), "Open work", "stale_unknown", "blocked"), true
	}
}

func assignmentOperationItemFromHecate(projectID string, workItem projectwork.WorkItem, assignment projectwork.Assignment, kind, priority, titlePrefix, detail, actionLabel, status, bucket string) ProjectOperationsBriefItemResponse {
	renderedWorkItem := renderProjectActivityWorkItem(renderProjectWorkItem(workItem))
	renderedAssignment := renderProjectWorkAssignment(assignment)
	target := ProjectOperationsBriefTargetResponse{
		Surface:        "work",
		ProjectID:      projectID,
		WorkItemID:     firstNonEmpty(workItem.ID, assignment.WorkItemID),
		AssignmentID:   assignment.ID,
		ActivityBucket: bucket,
	}
	action := projectOperationsOpenWorkItemAction(target.ProjectID, target.WorkItemID, target.AssignmentID, "", target.ActivityBucket)
	if kind == "start_queued_assignment" && assignment.DriverKind != projectwork.AssignmentDriverManual {
		action = projectOperationsOpenAssignmentPreflightAction(target.ProjectID, target.WorkItemID, target.AssignmentID, target.ActivityBucket)
	}
	return ProjectOperationsBriefItemResponse{
		ID:          projectOperationsItemID(kind, projectID, assignment.ID),
		Kind:        kind,
		Priority:    priority,
		Title:       titlePrefix + ": " + firstNonEmpty(workItem.Title, assignment.ID),
		Detail:      detail,
		ActionLabel: actionLabel,
		Status:      status,
		Target:      target,
		Action:      action,
		WorkItem:    &renderedWorkItem,
		Assignment:  &renderedAssignment,
		UpdatedAt:   formatOptionalTime(projectworkTime(assignment.UpdatedAt, assignment.StartedAt, assignment.CreatedAt, workItem.UpdatedAt, workItem.CreatedAt)),
	}
}

func handoffOperationItem(projectID string, handoff projectwork.Handoff) ProjectOperationsBriefItemResponse {
	rendered := renderProjectHandoff(handoff)
	target := ProjectOperationsBriefTargetResponse{
		Surface:      "work",
		ProjectID:    projectID,
		WorkItemID:   handoff.WorkItemID,
		HandoffID:    handoff.ID,
		AssignmentID: firstNonEmpty(handoff.TargetAssignmentID, handoff.SourceAssignmentID),
	}
	return ProjectOperationsBriefItemResponse{
		ID:          projectOperationsItemID("review_pending_handoff", projectID, handoff.ID),
		Kind:        "review_pending_handoff",
		Priority:    projectOperationsPriorityMedium,
		Title:       "Review pending handoff: " + firstNonEmpty(handoff.Title, handoff.ID),
		Detail:      firstNonEmpty(handoff.RecommendedNextAction, handoff.Summary, "Review the handoff and decide the next assignment."),
		ActionLabel: "Open handoff",
		Status:      handoff.Status,
		Target:      target,
		Action:      projectOperationsOpenWorkItemAction(target.ProjectID, target.WorkItemID, target.AssignmentID, target.HandoffID, ""),
		Handoff:     &rendered,
		UpdatedAt:   formatOptionalTime(projectworkTime(handoff.UpdatedAt, handoff.CreatedAt)),
	}
}

func genericCairnlineOperationItem(projectID string, item cairnline.ProjectOperationItem, kind, priority, actionLabel string) ProjectOperationsBriefItemResponse {
	target := ProjectOperationsBriefTargetResponse{
		Surface:      "work",
		ProjectID:    projectID,
		WorkItemID:   item.WorkItemID,
		AssignmentID: item.AssignmentID,
	}
	return ProjectOperationsBriefItemResponse{
		ID:          projectOperationsItemID(kind, projectID, firstNonEmpty(item.AssignmentID, item.ArtifactID, item.MemoryCandidateID, item.WorkItemID)),
		Kind:        kind,
		Priority:    priority,
		Title:       item.Title,
		Detail:      item.Detail,
		ActionLabel: actionLabel,
		Status:      item.Status,
		Target:      target,
		Action:      projectOperationsOpenWorkItemAction(target.ProjectID, target.WorkItemID, target.AssignmentID, "", ""),
		UpdatedAt:   formatOptionalTime(item.UpdatedAt),
	}
}

func projectActivityApprovalSummary(assignment projectwork.Assignment) string {
	if assignment.ExecutionRef.PendingApprovalCount > 0 {
		if assignment.ExecutionRef.PendingApprovalCount == 1 {
			return "1 approval pending"
		}
		return intString(assignment.ExecutionRef.PendingApprovalCount) + " approvals pending"
	}
	return "awaiting approval"
}

func cairnlineOperationPriority(item cairnline.ProjectOperationItem) string {
	switch strings.TrimSpace(item.Severity) {
	case cairnline.ProjectOperationSeverityBlocked:
		return projectOperationsPriorityHigh
	case cairnline.ProjectOperationSeverityAction:
		return projectOperationsPriorityMedium
	default:
		return projectOperationsPriorityLow
	}
}

func projectOperationsSnapshotWorkItemsByID(items []projectwork.WorkItem) map[string]projectwork.WorkItem {
	out := make(map[string]projectwork.WorkItem, len(items))
	for _, item := range items {
		out[item.ID] = item
	}
	return out
}

func projectOperationsSnapshotAssignmentsByID(items []projectwork.Assignment) map[string]projectwork.Assignment {
	out := make(map[string]projectwork.Assignment, len(items))
	for _, item := range items {
		out[item.ID] = item
	}
	return out
}

func projectOperationsSnapshotArtifactsByID(items []projectwork.CollaborationArtifact) map[string]projectwork.CollaborationArtifact {
	out := make(map[string]projectwork.CollaborationArtifact, len(items))
	for _, item := range items {
		out[item.ID] = item
	}
	return out
}

func projectOperationsSnapshotHandoffsByID(items []projectwork.Handoff) map[string]projectwork.Handoff {
	out := make(map[string]projectwork.Handoff, len(items))
	for _, item := range items {
		out[item.ID] = item
	}
	return out
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}
