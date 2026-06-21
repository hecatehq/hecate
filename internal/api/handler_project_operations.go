package api

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/projectworkapp"
)

const (
	projectOperationsPriorityHigh   = "high"
	projectOperationsPriorityMedium = "medium"
	projectOperationsPriorityLow    = "low"

	projectOperationsBriefItemLimit = 8

	projectOperationsActionDraftProjectProposal    = projectActionDraftProjectProposal
	projectOperationsActionOpenAssignmentPreflight = projectActionOpenAssignmentPreflight
	projectOperationsActionOpenMemoryReview        = projectActionOpenMemoryReview
	projectOperationsActionOpenProjectSettings     = projectActionOpenProjectSettings
	projectOperationsActionOpenWorkItem            = projectActionOpenWorkItem
)

type ProjectOperationsBriefEnvelope struct {
	Object string                         `json:"object"`
	Data   ProjectOperationsBriefResponse `json:"data"`
}

type ProjectOperationsBriefResponse struct {
	ProjectID   string                                `json:"project_id"`
	GeneratedAt string                                `json:"generated_at"`
	Summary     ProjectOperationsBriefSummaryResponse `json:"summary"`
	Items       []ProjectOperationsBriefItemResponse  `json:"items"`
}

type ProjectOperationsBriefSummaryResponse struct {
	ItemCount                   int `json:"item_count"`
	AvailableItemCount          int `json:"available_item_count"`
	OmittedItemCount            int `json:"omitted_item_count"`
	ItemLimit                   int `json:"item_limit"`
	HighCount                   int `json:"high_count"`
	MediumCount                 int `json:"medium_count"`
	LowCount                    int `json:"low_count"`
	PendingMemoryCandidateCount int `json:"pending_memory_candidate_count"`
	PendingHandoffCount         int `json:"pending_handoff_count"`
}

type ProjectOperationsBriefItemResponse struct {
	ID          string                               `json:"id"`
	Kind        string                               `json:"kind"`
	Priority    string                               `json:"priority"`
	Title       string                               `json:"title"`
	Detail      string                               `json:"detail"`
	ActionLabel string                               `json:"action_label"`
	Status      string                               `json:"status,omitempty"`
	Target      ProjectOperationsBriefTargetResponse `json:"target"`
	Action      ProjectOperationsBriefActionResponse `json:"action"`
	WorkItem    *ProjectActivityWorkItemResponse     `json:"work_item,omitempty"`
	Assignment  *ProjectWorkAssignmentResponse       `json:"assignment,omitempty"`
	Handoff     *ProjectHandoffResponse              `json:"handoff,omitempty"`
	UpdatedAt   string                               `json:"updated_at,omitempty"`
	Metadata    map[string]string                    `json:"metadata,omitempty"`
}

type ProjectOperationsBriefTargetResponse struct {
	Surface        string `json:"surface"`
	ProjectID      string `json:"project_id"`
	WorkItemID     string `json:"work_item_id,omitempty"`
	AssignmentID   string `json:"assignment_id,omitempty"`
	HandoffID      string `json:"handoff_id,omitempty"`
	ActivityBucket string `json:"activity_bucket,omitempty"`
}

func (h *Handler) HandleProjectOperationsBrief(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if !h.requireProject(w, r, projectID) {
		return
	}
	brief, err := h.renderProjectOperationsBrief(r.Context(), projectID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, ProjectOperationsBriefEnvelope{Object: "project_operations_brief", Data: brief})
}

func (h *Handler) renderProjectOperationsBrief(ctx context.Context, projectID string) (ProjectOperationsBriefResponse, error) {
	project, ok, err := h.projects.Get(ctx, projectID)
	if err != nil {
		return ProjectOperationsBriefResponse{}, err
	}
	if !ok {
		return ProjectOperationsBriefResponse{}, projects.ErrNotFound
	}
	activity, err := h.renderProjectActivity(ctx, projectID)
	if err != nil {
		return ProjectOperationsBriefResponse{}, err
	}
	workItems, err := h.projectWork.ListWorkItems(ctx, projectID)
	if err != nil {
		return ProjectOperationsBriefResponse{}, err
	}
	assignments, err := h.projectWork.ListAssignments(ctx, projectwork.AssignmentFilter{ProjectID: projectID})
	if err != nil {
		return ProjectOperationsBriefResponse{}, err
	}
	handoffs, err := h.projectWork.ListHandoffs(ctx, projectwork.HandoffFilter{ProjectID: projectID})
	if err != nil {
		return ProjectOperationsBriefResponse{}, err
	}
	artifacts, err := h.projectWork.ListArtifacts(ctx, projectwork.ArtifactFilter{ProjectID: projectID})
	if err != nil {
		return ProjectOperationsBriefResponse{}, err
	}
	pendingMemoryCandidates, err := h.pendingProjectMemoryCandidateCount(ctx, projectID)
	if err != nil {
		return ProjectOperationsBriefResponse{}, err
	}

	items := make([]ProjectOperationsBriefItemResponse, 0, projectOperationsBriefItemLimit)
	items = append(items, projectDefaultOperationItems(project)...)
	items = append(items, assignmentOperationItems(activity)...)
	items = append(items, handoffOperationItems(projectID, handoffs)...)
	items = append(items, selectedWorkFollowThroughOperationItems(projectID, workItems, assignments, artifacts, handoffs)...)
	if pendingMemoryCandidates > 0 {
		items = append(items, memoryCandidateOperationItem(projectID, pendingMemoryCandidates))
	}
	items = append(items, assignmentGapOperationItems(projectID, workItems, assignments)...)
	if len(items) == 0 {
		if item := latestWorkOperationItem(projectID, workItems); item != nil {
			items = append(items, *item)
		}
	}

	sortProjectOperationsItems(items)
	availableItemCount := len(items)
	items = boundedProjectOperationsItems(items, projectOperationsBriefItemLimit)
	response := ProjectOperationsBriefResponse{
		ProjectID:   projectID,
		GeneratedAt: formatOptionalTime(time.Now().UTC()),
		Items:       items,
	}
	response.Summary.ItemCount = len(items)
	response.Summary.AvailableItemCount = availableItemCount
	response.Summary.OmittedItemCount = availableItemCount - len(items)
	response.Summary.ItemLimit = projectOperationsBriefItemLimit
	response.Summary.PendingMemoryCandidateCount = pendingMemoryCandidates
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

func (h *Handler) pendingProjectMemoryCandidateCount(ctx context.Context, projectID string) (int, error) {
	if h.memoryCandidates == nil {
		return 0, nil
	}
	items, err := h.memoryCandidates.ListCandidates(ctx, memory.CandidateFilter{
		ProjectID: strings.TrimSpace(projectID),
		Status:    memory.CandidateStatusPending,
	})
	if err != nil {
		return 0, err
	}
	return len(items), nil
}

func projectDefaultOperationItems(project projects.Project) []ProjectOperationsBriefItemResponse {
	missing := make([]string, 0, 3)
	if strings.TrimSpace(project.DefaultProvider) == "" {
		missing = append(missing, "provider")
	}
	if strings.TrimSpace(project.DefaultModel) == "" {
		missing = append(missing, "model")
	}
	if projectHasActiveRoot(project) && strings.TrimSpace(project.DefaultRootID) == "" {
		missing = append(missing, "default root")
	}
	if len(missing) == 0 {
		return nil
	}
	return []ProjectOperationsBriefItemResponse{{
		ID:          projectOperationsItemID("configure_project_defaults", project.ID),
		Kind:        "configure_project_defaults",
		Priority:    projectOperationsPriorityHigh,
		Title:       "Set project launch defaults",
		Detail:      "Missing " + strings.Join(missing, ", ") + ". Assignments can override defaults, but project launches need a clear baseline.",
		ActionLabel: "Project settings",
		Target: ProjectOperationsBriefTargetResponse{
			Surface:   "project_settings",
			ProjectID: project.ID,
		},
		Action:    projectOperationsOpenProjectSettingsAction(project.ID),
		UpdatedAt: formatOptionalTime(project.UpdatedAt),
		Metadata: map[string]string{
			"missing": strings.Join(missing, ","),
		},
	}}
}

func assignmentOperationItems(activity ProjectActivityDataResponse) []ProjectOperationsBriefItemResponse {
	items := make([]ProjectOperationsBriefItemResponse, 0, len(activity.Buckets.Blocked)+len(activity.Buckets.Active))
	for _, item := range activity.Buckets.Blocked {
		switch item.BlockingSignal {
		case "awaiting_approval":
			items = append(items, projectOperationItemFromActivity(item, "approve_assignment", projectOperationsPriorityHigh, "Review pending approval", item.StatusSummary, "Open approval"))
		case "failed":
			items = append(items, projectOperationItemFromActivity(item, "review_failed_assignment", projectOperationsPriorityHigh, "Review failed assignment", item.StatusSummary, "Open work"))
		case "cancelled":
			items = append(items, projectOperationItemFromActivity(item, "review_cancelled_assignment", projectOperationsPriorityMedium, "Review cancelled assignment", item.StatusSummary, "Open work"))
		case "stale_unknown":
			items = append(items, projectOperationItemFromActivity(item, "inspect_stale_assignment", projectOperationsPriorityHigh, "Inspect stale assignment link", item.StatusSummary, "Open work"))
		case "not_started":
			items = append(items, projectOperationItemFromActivity(item, "start_queued_assignment", projectOperationsPriorityHigh, "Review queued assignment", "Open launch preflight before starting this assignment.", "Review start"))
		}
	}
	for _, item := range activity.Buckets.Active {
		items = append(items, projectOperationItemFromActivity(item, "inspect_active_assignment", projectOperationsPriorityLow, "Inspect active assignment", item.StatusSummary, "Inspect work"))
	}
	return items
}

func projectOperationItemFromActivity(activity ProjectActivityItemResponse, kind, priority, title, detail, actionLabel string) ProjectOperationsBriefItemResponse {
	workItem := activity.WorkItem
	assignment := activity.Assignment
	target := ProjectOperationsBriefTargetResponse{
		Surface:        "work",
		ProjectID:      activity.ProjectID,
		WorkItemID:     activity.WorkItem.ID,
		AssignmentID:   activity.Assignment.ID,
		ActivityBucket: projectActivityBucket(activity),
	}
	action := projectOperationsOpenWorkItemAction(target.ProjectID, target.WorkItemID, target.AssignmentID, "", target.ActivityBucket)
	if kind == "start_queued_assignment" {
		action = projectOperationsOpenAssignmentPreflightAction(target.ProjectID, target.WorkItemID, target.AssignmentID, target.ActivityBucket)
	}
	return ProjectOperationsBriefItemResponse{
		ID:          projectOperationsItemID(kind, activity.ProjectID, activity.Assignment.ID),
		Kind:        kind,
		Priority:    priority,
		Title:       title + ": " + firstNonEmpty(activity.WorkItem.Title, activity.Assignment.ID),
		Detail:      firstNonEmpty(detail, activity.StatusSummary),
		ActionLabel: actionLabel,
		Status:      activity.BlockingSignal,
		Target:      target,
		Action:      action,
		WorkItem:    &workItem,
		Assignment:  &assignment,
		UpdatedAt:   activity.UpdatedAt,
	}
}

func handoffOperationItems(projectID string, handoffs []projectwork.Handoff) []ProjectOperationsBriefItemResponse {
	items := make([]ProjectOperationsBriefItemResponse, 0, len(handoffs))
	for _, handoff := range handoffs {
		if handoff.Status != projectwork.HandoffStatusPending {
			continue
		}
		rendered := renderProjectHandoff(handoff)
		target := ProjectOperationsBriefTargetResponse{
			Surface:      "work",
			ProjectID:    projectID,
			WorkItemID:   handoff.WorkItemID,
			HandoffID:    handoff.ID,
			AssignmentID: firstNonEmpty(handoff.TargetAssignmentID, handoff.SourceAssignmentID),
		}
		items = append(items, ProjectOperationsBriefItemResponse{
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
		})
	}
	return items
}

func selectedWorkFollowThroughOperationItems(projectID string, workItems []projectwork.WorkItem, assignments []projectwork.Assignment, artifacts []projectwork.CollaborationArtifact, handoffs []projectwork.Handoff) []ProjectOperationsBriefItemResponse {
	assignmentsByWorkItem := groupProjectWorkAssignmentsByWorkItem(assignments)
	assignmentsByID := projectworkapp.AssignmentsByID(assignments)
	allArtifactsByWorkItem := projectOperationsArtifactsByWorkItem(artifacts)
	handoffsByWorkItem := projectOperationsHandoffsByWorkItem(handoffs)
	items := make([]ProjectOperationsBriefItemResponse, 0, len(workItems))
	for _, workItem := range workItems {
		if projectworkapp.WorkItemClosed(workItem.Status) {
			continue
		}
		workItemAssignments := assignmentsByWorkItem[workItem.ID]
		workItemArtifacts := allArtifactsByWorkItem[workItem.ID]
		workItemHandoffs := handoffsByWorkItem[workItem.ID]
		readiness := projectworkapp.EvaluateWorkItemReadiness(workItem, workItemAssignments, workItemArtifacts, workItemHandoffs)
		if review := projectworkapp.ReviewFollowUpArtifact(workItemArtifacts, workItemHandoffs); review != nil {
			items = append(items, reviewFollowUpOperationItem(projectID, workItem, *review))
		}
		for _, assignmentID := range readiness.MissingEvidenceAssignmentIDs {
			assignment, ok := assignmentsByID[assignmentID]
			if !ok {
				continue
			}
			items = append(items, completionEvidenceOperationItem(projectID, workItem, assignment))
		}
		if readiness.Ready && len(workItemAssignments) > 0 {
			items = append(items, closeWorkItemOperationItem(projectID, workItem, workItemAssignments))
		}
	}
	return items
}

func reviewFollowUpOperationItem(projectID string, workItem projectwork.WorkItem, artifact projectwork.CollaborationArtifact) ProjectOperationsBriefItemResponse {
	renderedWorkItem := renderProjectActivityWorkItem(renderProjectWorkItem(workItem))
	status := "awaiting_approval"
	if artifact.ReviewVerdict == projectwork.ReviewVerdictBlocked {
		status = "blocked"
	}
	target := ProjectOperationsBriefTargetResponse{
		Surface:    "work",
		ProjectID:  projectID,
		WorkItemID: workItem.ID,
	}
	return ProjectOperationsBriefItemResponse{
		ID:          projectOperationsItemID("review_follow_up", projectID, artifact.ID),
		Kind:        "review_follow_up",
		Priority:    projectOperationsPriorityMedium,
		Title:       "Review follow-up: " + firstNonEmpty(workItem.Title, artifact.Title, artifact.ID),
		Detail:      "Review artifact " + firstNonEmpty(artifact.Title, artifact.ID) + " needs a follow-up path before closeout.",
		ActionLabel: "Open review",
		Status:      status,
		Target:      target,
		Action:      projectOperationsOpenWorkItemAction(target.ProjectID, target.WorkItemID, "", "", ""),
		WorkItem:    &renderedWorkItem,
		UpdatedAt:   formatOptionalTime(projectworkTime(artifact.UpdatedAt, artifact.CreatedAt, workItem.UpdatedAt, workItem.CreatedAt)),
		Metadata: map[string]string{
			"artifact_id":    artifact.ID,
			"review_verdict": artifact.ReviewVerdict,
		},
	}
}

func completionEvidenceOperationItem(projectID string, workItem projectwork.WorkItem, assignment projectwork.Assignment) ProjectOperationsBriefItemResponse {
	renderedWorkItem := renderProjectActivityWorkItem(renderProjectWorkItem(workItem))
	renderedAssignment := renderProjectWorkAssignment(assignment)
	target := ProjectOperationsBriefTargetResponse{
		Surface:      "work",
		ProjectID:    projectID,
		WorkItemID:   workItem.ID,
		AssignmentID: assignment.ID,
	}
	return ProjectOperationsBriefItemResponse{
		ID:          projectOperationsItemID("record_completion_evidence", projectID, assignment.ID),
		Kind:        "record_completion_evidence",
		Priority:    projectOperationsPriorityLow,
		Title:       "Record completion evidence: " + firstNonEmpty(workItem.Title, assignment.ID),
		Detail:      "Completed assignments should leave reviewable evidence before work is closed.",
		ActionLabel: "Open work",
		Status:      projectwork.AssignmentStatusCompleted,
		Target:      target,
		Action:      projectOperationsOpenWorkItemAction(target.ProjectID, target.WorkItemID, target.AssignmentID, "", "completed"),
		WorkItem:    &renderedWorkItem,
		Assignment:  &renderedAssignment,
		UpdatedAt:   formatOptionalTime(projectworkTime(assignment.CompletedAt, assignment.UpdatedAt, assignment.CreatedAt, workItem.UpdatedAt, workItem.CreatedAt)),
	}
}

func closeWorkItemOperationItem(projectID string, workItem projectwork.WorkItem, assignments []projectwork.Assignment) ProjectOperationsBriefItemResponse {
	renderedWorkItem := renderProjectActivityWorkItem(renderProjectWorkItem(workItem))
	target := ProjectOperationsBriefTargetResponse{
		Surface:    "work",
		ProjectID:  projectID,
		WorkItemID: workItem.ID,
	}
	return ProjectOperationsBriefItemResponse{
		ID:          projectOperationsItemID("close_work_item", projectID, workItem.ID),
		Kind:        "close_work_item",
		Priority:    projectOperationsPriorityLow,
		Title:       "Close out work item: " + firstNonEmpty(workItem.Title, workItem.ID),
		Detail:      "Assignments, evidence, handoffs, and review follow-up are clear. Mark done from selected-work detail.",
		ActionLabel: "Open closeout",
		Status:      "ready",
		Target:      target,
		Action:      projectOperationsOpenWorkItemAction(target.ProjectID, target.WorkItemID, "", "", ""),
		WorkItem:    &renderedWorkItem,
		UpdatedAt:   formatOptionalTime(projectOperationsLatestAssignmentTime(assignments, projectworkTime(workItem.UpdatedAt, workItem.CreatedAt))),
		Metadata: map[string]string{
			"assignment_count": intString(len(assignments)),
		},
	}
}

func memoryCandidateOperationItem(projectID string, count int) ProjectOperationsBriefItemResponse {
	title := "Review memory candidates"
	if count == 1 {
		title = "Review 1 memory candidate"
	}
	return ProjectOperationsBriefItemResponse{
		ID:          projectOperationsItemID("review_memory_candidates", projectID),
		Kind:        "review_memory_candidates",
		Priority:    projectOperationsPriorityMedium,
		Title:       title,
		Detail:      "Promote, edit, or reject pending memory candidates before they become stale.",
		ActionLabel: "Review memory",
		Status:      "pending",
		Target: ProjectOperationsBriefTargetResponse{
			Surface:   "memory",
			ProjectID: projectID,
		},
		Action: projectOperationsOpenMemoryReviewAction(projectID),
		Metadata: map[string]string{
			"candidate_count": intString(count),
		},
	}
}

func assignmentGapOperationItems(projectID string, workItems []projectwork.WorkItem, assignments []projectwork.Assignment) []ProjectOperationsBriefItemResponse {
	assignmentsByWorkItem := groupProjectWorkAssignmentsByWorkItem(assignments)
	items := make([]ProjectOperationsBriefItemResponse, 0, len(workItems)+1)
	if len(workItems) == 0 {
		items = append(items, ProjectOperationsBriefItemResponse{
			ID:          projectOperationsItemID("create_first_work_item", projectID),
			Kind:        "create_first_work_item",
			Priority:    projectOperationsPriorityMedium,
			Title:       "Create the first work item",
			Detail:      "Start with one reviewable project work item before queueing assignments.",
			ActionLabel: "Draft work",
			Target: ProjectOperationsBriefTargetResponse{
				Surface:   "work",
				ProjectID: projectID,
			},
			Action: projectOperationsDraftProjectProposalAction(projectID, "", "Create the first project work item"),
		})
		return items
	}
	for _, workItem := range workItems {
		if projectworkapp.WorkItemClosed(workItem.Status) || len(assignmentsByWorkItem[workItem.ID]) > 0 {
			continue
		}
		rendered := renderProjectActivityWorkItem(renderProjectWorkItem(workItem))
		items = append(items, ProjectOperationsBriefItemResponse{
			ID:          projectOperationsItemID("prepare_first_assignment", projectID, workItem.ID),
			Kind:        "prepare_first_assignment",
			Priority:    projectOperationsPriorityMedium,
			Title:       "Prepare first assignment: " + firstNonEmpty(workItem.Title, workItem.ID),
			Detail:      "This work item has no queued or running assignments yet.",
			ActionLabel: "Draft assignment",
			Status:      firstNonEmpty(workItem.Status, projectwork.WorkItemStatusReady),
			Target: ProjectOperationsBriefTargetResponse{
				Surface:    "work",
				ProjectID:  projectID,
				WorkItemID: workItem.ID,
			},
			Action:    projectOperationsDraftProjectProposalAction(projectID, workItem.ID, "Queue an assignment for "+firstNonEmpty(workItem.Title, workItem.ID)),
			WorkItem:  &rendered,
			UpdatedAt: formatOptionalTime(projectworkTime(workItem.UpdatedAt, workItem.CreatedAt)),
		})
	}
	return items
}

func latestWorkOperationItem(projectID string, workItems []projectwork.WorkItem) *ProjectOperationsBriefItemResponse {
	if len(workItems) == 0 {
		return nil
	}
	items := append([]projectwork.WorkItem(nil), workItems...)
	sort.SliceStable(items, func(i, j int) bool {
		left, right := projectworkTime(items[i].UpdatedAt, items[i].CreatedAt), projectworkTime(items[j].UpdatedAt, items[j].CreatedAt)
		if !left.Equal(right) {
			return left.After(right)
		}
		return items[i].ID < items[j].ID
	})
	workItem := items[0]
	rendered := renderProjectActivityWorkItem(renderProjectWorkItem(workItem))
	target := ProjectOperationsBriefTargetResponse{
		Surface:    "work",
		ProjectID:  projectID,
		WorkItemID: workItem.ID,
	}
	return &ProjectOperationsBriefItemResponse{
		ID:          projectOperationsItemID("open_latest_work", projectID, workItem.ID),
		Kind:        "open_latest_work",
		Priority:    projectOperationsPriorityLow,
		Title:       "Open latest work: " + firstNonEmpty(workItem.Title, workItem.ID),
		Detail:      "Review the most recently updated work item.",
		ActionLabel: "Open work",
		Status:      firstNonEmpty(workItem.Status, projectwork.WorkItemStatusReady),
		Target:      target,
		Action:      projectOperationsOpenWorkItemAction(target.ProjectID, target.WorkItemID, "", "", ""),
		WorkItem:    &rendered,
		UpdatedAt:   formatOptionalTime(projectworkTime(workItem.UpdatedAt, workItem.CreatedAt)),
	}
}

func sortProjectOperationsItems(items []ProjectOperationsBriefItemResponse) {
	sort.SliceStable(items, func(i, j int) bool {
		leftRank, rightRank := projectOperationsPriorityRank(items[i].Priority), projectOperationsPriorityRank(items[j].Priority)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		leftKindRank, rightKindRank := projectOperationsKindRank(items[i].Kind), projectOperationsKindRank(items[j].Kind)
		if leftKindRank != rightKindRank {
			return leftKindRank < rightKindRank
		}
		leftTime, rightTime := parseProjectActivityTime(items[i].UpdatedAt), parseProjectActivityTime(items[j].UpdatedAt)
		if !leftTime.Equal(rightTime) {
			return leftTime.After(rightTime)
		}
		return items[i].ID < items[j].ID
	})
}

func projectOperationsPriorityRank(priority string) int {
	switch strings.TrimSpace(priority) {
	case projectOperationsPriorityHigh:
		return 0
	case projectOperationsPriorityMedium:
		return 1
	case projectOperationsPriorityLow:
		return 2
	default:
		return 3
	}
}

func projectOperationsKindRank(kind string) int {
	// Kind rank is operator urgency within each priority tier because the brief cap
	// keeps only the first sorted items.
	switch strings.TrimSpace(kind) {
	case "approve_assignment":
		return 0
	case "review_failed_assignment":
		return 10
	case "inspect_stale_assignment":
		return 20
	case "start_queued_assignment":
		return 30
	case "configure_project_defaults":
		return 40
	case "review_cancelled_assignment":
		return 50
	case "review_pending_handoff":
		return 60
	case "review_follow_up":
		return 65
	case "prepare_first_assignment":
		return 70
	case "create_first_work_item":
		return 80
	case "review_memory_candidates":
		return 90
	case "inspect_active_assignment":
		return 100
	case "record_completion_evidence":
		return 110
	case "close_work_item":
		return 120
	case "open_latest_work":
		return 130
	default:
		return 1000
	}
}

func boundedProjectOperationsItems(items []ProjectOperationsBriefItemResponse, limit int) []ProjectOperationsBriefItemResponse {
	if len(items) == 0 {
		return []ProjectOperationsBriefItemResponse{}
	}
	if limit > 0 && len(items) > limit {
		return append([]ProjectOperationsBriefItemResponse(nil), items[:limit]...)
	}
	return append([]ProjectOperationsBriefItemResponse(nil), items...)
}

func projectOperationsDraftProjectProposalAction(projectID, workItemID, request string) ProjectOperationsBriefActionResponse {
	return newProjectActionDraftProposal(projectID, workItemID, request)
}

func projectOperationsOpenAssignmentPreflightAction(projectID, workItemID, assignmentID, bucket string) ProjectOperationsBriefActionResponse {
	return newProjectActionOpenAssignmentPreflight(projectID, workItemID, assignmentID, bucket)
}

func projectOperationsOpenMemoryReviewAction(projectID string) ProjectOperationsBriefActionResponse {
	return newProjectActionOpenMemoryReview(projectID)
}

func projectOperationsOpenProjectSettingsAction(projectID string) ProjectOperationsBriefActionResponse {
	return newProjectActionOpenProjectSettings(projectID)
}

func projectOperationsOpenWorkItemAction(projectID, workItemID, assignmentID, handoffID, bucket string) ProjectOperationsBriefActionResponse {
	return newProjectActionOpenWorkItem(projectID, workItemID, assignmentID, handoffID, bucket)
}

func projectOperationsItemID(kind string, parts ...string) string {
	values := make([]string, 0, len(parts)+1)
	values = append(values, strings.TrimSpace(kind))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			values = append(values, part)
		}
	}
	return strings.Join(values, ":")
}

func projectHasActiveRoot(project projects.Project) bool {
	for _, root := range project.Roots {
		if root.Active && strings.TrimSpace(root.Path) != "" {
			return true
		}
	}
	return false
}

func projectOperationsArtifactsByWorkItem(artifacts []projectwork.CollaborationArtifact) map[string][]projectwork.CollaborationArtifact {
	byWorkItem := make(map[string][]projectwork.CollaborationArtifact)
	for _, artifact := range artifacts {
		if artifact.WorkItemID == "" {
			continue
		}
		byWorkItem[artifact.WorkItemID] = append(byWorkItem[artifact.WorkItemID], artifact)
	}
	return byWorkItem
}

func projectOperationsHandoffsByWorkItem(handoffs []projectwork.Handoff) map[string][]projectwork.Handoff {
	byWorkItem := make(map[string][]projectwork.Handoff)
	for _, handoff := range handoffs {
		if handoff.WorkItemID == "" {
			continue
		}
		byWorkItem[handoff.WorkItemID] = append(byWorkItem[handoff.WorkItemID], handoff)
	}
	return byWorkItem
}

func projectOperationsLatestAssignmentTime(assignments []projectwork.Assignment, fallback time.Time) time.Time {
	latest := fallback
	for _, assignment := range assignments {
		for _, value := range []time.Time{assignment.CompletedAt, assignment.StartedAt, assignment.UpdatedAt, assignment.CreatedAt} {
			if value.After(latest) {
				latest = value
			}
		}
	}
	return latest
}

func projectworkTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func intString(value int) string {
	return strconv.Itoa(value)
}
