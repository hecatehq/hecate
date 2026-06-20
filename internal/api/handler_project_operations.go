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
)

const (
	projectOperationsPriorityHigh   = "high"
	projectOperationsPriorityMedium = "medium"
	projectOperationsPriorityLow    = "low"
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
	HighCount                   int `json:"high_count"`
	MediumCount                 int `json:"medium_count"`
	LowCount                    int `json:"low_count"`
	PendingMemoryCandidateCount int `json:"pending_memory_candidate_count"`
	PendingHandoffCount         int `json:"pending_handoff_count"`
}

type ProjectOperationsBriefItemResponse struct {
	ID           string                               `json:"id"`
	Kind         string                               `json:"kind"`
	Priority     string                               `json:"priority"`
	Title        string                               `json:"title"`
	Detail       string                               `json:"detail"`
	ActionLabel  string                               `json:"action_label"`
	Status       string                               `json:"status,omitempty"`
	Target       ProjectOperationsBriefTargetResponse `json:"target"`
	DraftRequest string                               `json:"draft_request,omitempty"`
	WorkItem     *ProjectActivityWorkItemResponse     `json:"work_item,omitempty"`
	Assignment   *ProjectWorkAssignmentResponse       `json:"assignment,omitempty"`
	Handoff      *ProjectHandoffResponse              `json:"handoff,omitempty"`
	UpdatedAt    string                               `json:"updated_at,omitempty"`
	Metadata     map[string]string                    `json:"metadata,omitempty"`
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
	pendingMemoryCandidates, err := h.pendingProjectMemoryCandidateCount(ctx, projectID)
	if err != nil {
		return ProjectOperationsBriefResponse{}, err
	}

	items := make([]ProjectOperationsBriefItemResponse, 0, 8)
	items = append(items, projectDefaultOperationItems(project)...)
	items = append(items, assignmentOperationItems(activity)...)
	items = append(items, handoffOperationItems(projectID, handoffs)...)
	if pendingMemoryCandidates > 0 {
		items = append(items, memoryCandidateOperationItem(projectID, pendingMemoryCandidates))
	}
	items = append(items, assignmentGapOperationItems(projectID, workItems, assignments)...)

	sortProjectOperationsItems(items)
	items = boundedProjectOperationsItems(items, 8)
	response := ProjectOperationsBriefResponse{
		ProjectID:   projectID,
		GeneratedAt: formatOptionalTime(time.Now().UTC()),
		Items:       items,
	}
	response.Summary.ItemCount = len(items)
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
	for _, item := range activity.Buckets.Completed {
		if item.ArtifactSummary.Count > 0 {
			continue
		}
		items = append(items, projectOperationItemFromActivity(item, "record_completion_evidence", projectOperationsPriorityLow, "Record completion evidence", "Completed assignments should leave reviewable evidence before work is closed.", "Open work"))
	}
	return items
}

func projectOperationItemFromActivity(activity ProjectActivityItemResponse, kind, priority, title, detail, actionLabel string) ProjectOperationsBriefItemResponse {
	workItem := activity.WorkItem
	assignment := activity.Assignment
	return ProjectOperationsBriefItemResponse{
		ID:          projectOperationsItemID(kind, activity.ProjectID, activity.Assignment.ID),
		Kind:        kind,
		Priority:    priority,
		Title:       title + ": " + firstNonEmpty(activity.WorkItem.Title, activity.Assignment.ID),
		Detail:      firstNonEmpty(detail, activity.StatusSummary),
		ActionLabel: actionLabel,
		Status:      activity.BlockingSignal,
		Target: ProjectOperationsBriefTargetResponse{
			Surface:        "work",
			ProjectID:      activity.ProjectID,
			WorkItemID:     activity.WorkItem.ID,
			AssignmentID:   activity.Assignment.ID,
			ActivityBucket: projectActivityBucket(activity),
		},
		WorkItem:   &workItem,
		Assignment: &assignment,
		UpdatedAt:  activity.UpdatedAt,
	}
}

func handoffOperationItems(projectID string, handoffs []projectwork.Handoff) []ProjectOperationsBriefItemResponse {
	items := make([]ProjectOperationsBriefItemResponse, 0, len(handoffs))
	for _, handoff := range handoffs {
		if handoff.Status != projectwork.HandoffStatusPending {
			continue
		}
		rendered := renderProjectHandoff(handoff)
		items = append(items, ProjectOperationsBriefItemResponse{
			ID:          projectOperationsItemID("review_pending_handoff", projectID, handoff.ID),
			Kind:        "review_pending_handoff",
			Priority:    projectOperationsPriorityMedium,
			Title:       "Review pending handoff: " + firstNonEmpty(handoff.Title, handoff.ID),
			Detail:      firstNonEmpty(handoff.RecommendedNextAction, handoff.Summary, "Review the handoff and decide the next assignment."),
			ActionLabel: "Open handoff",
			Status:      handoff.Status,
			Target: ProjectOperationsBriefTargetResponse{
				Surface:      "work",
				ProjectID:    projectID,
				WorkItemID:   handoff.WorkItemID,
				HandoffID:    handoff.ID,
				AssignmentID: firstNonEmpty(handoff.TargetAssignmentID, handoff.SourceAssignmentID),
			},
			Handoff:   &rendered,
			UpdatedAt: formatOptionalTime(projectworkTime(handoff.UpdatedAt, handoff.CreatedAt)),
		})
	}
	return items
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
			ID:           projectOperationsItemID("create_first_work_item", projectID),
			Kind:         "create_first_work_item",
			Priority:     projectOperationsPriorityMedium,
			Title:        "Create the first work item",
			Detail:       "Start with one reviewable project work item before queueing assignments.",
			ActionLabel:  "Draft work",
			DraftRequest: "Create the first project work item",
			Target: ProjectOperationsBriefTargetResponse{
				Surface:   "work",
				ProjectID: projectID,
			},
		})
		return items
	}
	for _, workItem := range workItems {
		if projectWorkItemClosed(workItem.Status) || len(assignmentsByWorkItem[workItem.ID]) > 0 {
			continue
		}
		rendered := renderProjectActivityWorkItem(renderProjectWorkItem(workItem))
		items = append(items, ProjectOperationsBriefItemResponse{
			ID:           projectOperationsItemID("prepare_first_assignment", projectID, workItem.ID),
			Kind:         "prepare_first_assignment",
			Priority:     projectOperationsPriorityMedium,
			Title:        "Prepare first assignment: " + firstNonEmpty(workItem.Title, workItem.ID),
			Detail:       "This work item has no queued or running assignments yet.",
			ActionLabel:  "Draft assignment",
			DraftRequest: "Queue an assignment for " + firstNonEmpty(workItem.Title, workItem.ID),
			Status:       firstNonEmpty(workItem.Status, projectwork.WorkItemStatusReady),
			Target: ProjectOperationsBriefTargetResponse{
				Surface:    "work",
				ProjectID:  projectID,
				WorkItemID: workItem.ID,
			},
			WorkItem:  &rendered,
			UpdatedAt: formatOptionalTime(projectworkTime(workItem.UpdatedAt, workItem.CreatedAt)),
		})
	}
	return items
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

func projectWorkItemClosed(status string) bool {
	switch strings.TrimSpace(status) {
	case projectwork.WorkItemStatusDone, projectwork.WorkItemStatusCancelled:
		return true
	default:
		return false
	}
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
