package api

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/projectwork"
)

type ProjectActivityEnvelope struct {
	Object string                      `json:"object"`
	Data   ProjectActivityDataResponse `json:"data"`
}

type ProjectActivityDataResponse struct {
	ProjectID string                         `json:"project_id"`
	Summary   ProjectActivitySummaryResponse `json:"summary"`
	Buckets   ProjectActivityBucketsResponse `json:"buckets"`
	Recent    []ProjectActivityItemResponse  `json:"recent"`
}

type ProjectActivitySummaryResponse struct {
	WorkItemCount   int `json:"work_item_count"`
	AssignmentCount int `json:"assignment_count"`
	ActiveCount     int `json:"active_count"`
	BlockedCount    int `json:"blocked_count"`
	CompletedCount  int `json:"completed_count"`
	RecentCount     int `json:"recent_count"`
}

type ProjectActivityBucketsResponse struct {
	Active    []ProjectActivityItemResponse `json:"active"`
	Blocked   []ProjectActivityItemResponse `json:"blocked"`
	Completed []ProjectActivityItemResponse `json:"completed"`
	Recent    []ProjectActivityItemResponse `json:"recent"`
}

type ProjectActivityItemResponse struct {
	ID              string                                 `json:"id"`
	ProjectID       string                                 `json:"project_id"`
	WorkItem        ProjectActivityWorkItemResponse        `json:"work_item"`
	Assignment      ProjectWorkAssignmentResponse          `json:"assignment"`
	Role            ProjectWorkRoleResponse                `json:"role"`
	Status          string                                 `json:"status"`
	BlockingSignal  string                                 `json:"blocking_signal"`
	StatusSummary   string                                 `json:"status_summary"`
	LinkedTaskID    string                                 `json:"linked_task_id,omitempty"`
	LinkedRunID     string                                 `json:"linked_run_id,omitempty"`
	LinkedChatID    string                                 `json:"linked_chat_id,omitempty"`
	LinkedChat      *ProjectActivityLinkedChatResponse     `json:"linked_chat,omitempty"`
	LinkedMessageID string                                 `json:"linked_message_id,omitempty"`
	RecentArtifacts []ProjectWorkArtifactResponse          `json:"recent_artifacts,omitempty"`
	ArtifactSummary ProjectActivityArtifactSummaryResponse `json:"artifact_summary"`
	RecentHandoffs  []ProjectHandoffResponse               `json:"recent_handoffs,omitempty"`
	HandoffSummary  ProjectActivityHandoffSummaryResponse  `json:"handoff_summary"`
	UpdatedAt       string                                 `json:"updated_at"`
}

type ProjectActivityLinkedChatResponse struct {
	ID              string `json:"id"`
	Title           string `json:"title,omitempty"`
	AgentID         string `json:"agent_id,omitempty"`
	DriverKind      string `json:"driver_kind,omitempty"`
	NativeSessionID string `json:"native_session_id,omitempty"`
	Status          string `json:"status,omitempty"`
	LatestMessageID string `json:"latest_message_id,omitempty"`
	LatestRole      string `json:"latest_role,omitempty"`
	LatestStatus    string `json:"latest_status,omitempty"`
	LatestError     string `json:"latest_error,omitempty"`
	MessageCount    int    `json:"message_count,omitempty"`
	CreatedAt       string `json:"created_at,omitempty"`
	UpdatedAt       string `json:"updated_at,omitempty"`
	Missing         bool   `json:"missing,omitempty"`
}

type ProjectActivityWorkItemResponse struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Priority string `json:"priority"`
}

type ProjectActivityArtifactSummaryResponse struct {
	Count        int    `json:"count"`
	LatestKind   string `json:"latest_kind,omitempty"`
	LatestTitle  string `json:"latest_title,omitempty"`
	LatestAt     string `json:"latest_at,omitempty"`
	AssignmentID string `json:"assignment_id,omitempty"`
}

type ProjectActivityHandoffSummaryResponse struct {
	Count          int    `json:"count"`
	PendingCount   int    `json:"pending_count,omitempty"`
	AcceptedCount  int    `json:"accepted_count,omitempty"`
	LatestStatus   string `json:"latest_status,omitempty"`
	LatestTitle    string `json:"latest_title,omitempty"`
	LatestAt       string `json:"latest_at,omitempty"`
	AssignmentID   string `json:"assignment_id,omitempty"`
	TargetRoleID   string `json:"target_role_id,omitempty"`
	TargetWorkItem string `json:"target_work_item_id,omitempty"`
}

func (h *Handler) renderProjectActivity(ctx context.Context, projectID string) (ProjectActivityDataResponse, error) {
	roles, err := h.projectWork.ListRoles(ctx, projectID)
	if err != nil {
		return ProjectActivityDataResponse{}, err
	}
	workItems, err := h.projectWork.ListWorkItems(ctx, projectID)
	if err != nil {
		return ProjectActivityDataResponse{}, err
	}
	assignments, err := h.projectWork.ListAssignments(ctx, projectwork.AssignmentFilter{ProjectID: projectID})
	if err != nil {
		return ProjectActivityDataResponse{}, err
	}
	artifacts, err := h.projectWork.ListArtifacts(ctx, projectwork.ArtifactFilter{ProjectID: projectID})
	if err != nil {
		return ProjectActivityDataResponse{}, err
	}
	handoffs, err := h.projectWork.ListHandoffs(ctx, projectwork.HandoffFilter{ProjectID: projectID})
	if err != nil {
		return ProjectActivityDataResponse{}, err
	}

	roleByID := make(map[string]projectwork.AgentRoleProfile, len(roles))
	for _, role := range roles {
		roleByID[role.ID] = role
	}
	assignmentsByWorkItem := groupProjectWorkAssignmentsByWorkItem(assignments)
	linkedChats := h.projectActivityLinkedChats(ctx, projectID, assignments)
	projectedWorkItems := make(map[string]ProjectWorkItemResponse, len(workItems))
	for _, item := range workItems {
		projected, err := h.renderProjectedProjectWorkItemWithAssignments(ctx, item, assignmentsByWorkItem[item.ID])
		if err != nil {
			return ProjectActivityDataResponse{}, err
		}
		projectedWorkItems[item.ID] = projected
	}

	artifactsByAssignment, artifactsByWorkItem := groupProjectActivityArtifacts(artifacts)
	handoffsByAssignment, handoffsByWorkItem := groupProjectActivityHandoffs(handoffs)
	items := make([]ProjectActivityItemResponse, 0, len(assignments))
	for _, workItem := range projectedWorkItems {
		for _, projected := range workItem.Assignments {
			activityArtifacts := artifactsByAssignment[projected.ID]
			if len(activityArtifacts) == 0 {
				activityArtifacts = artifactsByWorkItem[projected.WorkItemID]
			}
			activityHandoffs := handoffsByAssignment[projected.ID]
			if len(activityHandoffs) == 0 {
				activityHandoffs = handoffsByWorkItem[projected.WorkItemID]
			}
			role, _ := roleByID[projected.RoleID]
			items = append(items, renderProjectActivityItem(workItem, projected, role, activityArtifacts, activityHandoffs, linkedChats[projected.ID]))
		}
	}
	sortProjectActivityItems(items)

	response := ProjectActivityDataResponse{
		ProjectID: projectID,
		Recent:    boundedProjectActivityItems(items, 20),
	}
	response.Summary.WorkItemCount = len(workItems)
	response.Summary.AssignmentCount = len(assignments)
	for _, item := range items {
		switch projectActivityBucket(item) {
		case "active":
			response.Buckets.Active = append(response.Buckets.Active, item)
			response.Summary.ActiveCount++
		case "blocked":
			response.Buckets.Blocked = append(response.Buckets.Blocked, item)
			response.Summary.BlockedCount++
		case "completed":
			response.Buckets.Completed = append(response.Buckets.Completed, item)
			response.Summary.CompletedCount++
		}
	}
	response.Buckets.Recent = response.Recent
	response.Summary.RecentCount = len(response.Recent)
	response.Buckets.Active = boundedProjectActivityItems(response.Buckets.Active, 20)
	response.Buckets.Blocked = boundedProjectActivityItems(response.Buckets.Blocked, 20)
	response.Buckets.Completed = boundedProjectActivityItems(response.Buckets.Completed, 20)
	return response, nil
}

func (h *Handler) projectActivityLinkedChats(ctx context.Context, projectID string, assignments []projectwork.Assignment) map[string]*ProjectActivityLinkedChatResponse {
	linked := make(map[string]*ProjectActivityLinkedChatResponse)
	if h == nil || h.agentChat == nil {
		return linked
	}
	for _, assignment := range assignments {
		chatID := strings.TrimSpace(assignment.ChatSessionID)
		if chatID == "" {
			continue
		}
		session, ok, err := h.agentChat.Get(ctx, chatID)
		if err != nil || !ok || strings.TrimSpace(session.ProjectID) != strings.TrimSpace(projectID) {
			linked[assignment.ID] = missingProjectActivityLinkedChat(chatID)
			continue
		}
		linked[assignment.ID] = renderProjectActivityLinkedChat(session)
	}
	return linked
}

func missingProjectActivityLinkedChat(chatID string) *ProjectActivityLinkedChatResponse {
	return &ProjectActivityLinkedChatResponse{
		ID:      chatID,
		Missing: true,
	}
}

func renderProjectActivityLinkedChat(session chat.Session) *ProjectActivityLinkedChatResponse {
	item := &ProjectActivityLinkedChatResponse{
		ID:              session.ID,
		Title:           session.Title,
		AgentID:         renderChatAgentID(session),
		DriverKind:      session.DriverKind,
		NativeSessionID: session.NativeSessionID,
		Status:          session.Status,
		MessageCount:    len(session.Messages),
		CreatedAt:       formatOptionalTime(session.CreatedAt),
		UpdatedAt:       formatOptionalTime(session.UpdatedAt),
	}
	if latest := latestProjectActivityChatMessage(session.Messages); latest != nil {
		item.LatestMessageID = latest.ID
		item.LatestRole = latest.Role
		item.LatestStatus = latest.Status
		item.LatestError = latest.Error
		if !latest.CompletedAt.IsZero() {
			item.UpdatedAt = formatOptionalTime(latest.CompletedAt)
		} else if !latest.StartedAt.IsZero() {
			item.UpdatedAt = formatOptionalTime(latest.StartedAt)
		} else if !latest.CreatedAt.IsZero() {
			item.UpdatedAt = formatOptionalTime(latest.CreatedAt)
		}
	}
	return item
}

func latestProjectActivityChatMessage(messages []chat.Message) *chat.Message {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if strings.TrimSpace(message.ID) != "" {
			return &messages[i]
		}
	}
	return nil
}

func renderProjectActivityItem(workItem ProjectWorkItemResponse, assignment ProjectWorkAssignmentResponse, role projectwork.AgentRoleProfile, artifacts []projectwork.CollaborationArtifact, handoffs []projectwork.Handoff, linkedChat *ProjectActivityLinkedChatResponse) ProjectActivityItemResponse {
	artifactSummary, recentArtifacts := renderProjectActivityArtifactSignals(artifacts)
	handoffSummary, recentHandoffs := renderProjectActivityHandoffSignals(handoffs)
	status := strings.TrimSpace(projectActivityProjectedStatus(assignment, linkedChat))
	if status == "" {
		status = "unknown"
	}
	signal := projectActivityBlockingSignal(assignment, linkedChat)
	return ProjectActivityItemResponse{
		ID:              assignment.ID,
		ProjectID:       assignment.ProjectID,
		WorkItem:        renderProjectActivityWorkItem(workItem),
		Assignment:      assignment,
		Role:            renderProjectWorkRole(role),
		Status:          status,
		BlockingSignal:  signal,
		StatusSummary:   projectActivityStatusSummary(assignment, linkedChat, signal, artifactSummary.Count),
		LinkedTaskID:    firstNonEmpty(projectActivityExecutionTaskID(assignment), assignment.TaskID),
		LinkedRunID:     firstNonEmpty(projectActivityExecutionRunID(assignment), assignment.RunID),
		LinkedChatID:    assignment.ChatSessionID,
		LinkedChat:      linkedChat,
		LinkedMessageID: assignment.MessageID,
		RecentArtifacts: recentArtifacts,
		ArtifactSummary: artifactSummary,
		RecentHandoffs:  recentHandoffs,
		HandoffSummary:  handoffSummary,
		UpdatedAt:       projectActivityUpdatedAt(workItem, assignment, linkedChat, artifactSummary, handoffSummary),
	}
}

func renderProjectActivityWorkItem(item ProjectWorkItemResponse) ProjectActivityWorkItemResponse {
	return ProjectActivityWorkItemResponse{
		ID:       item.ID,
		Title:    item.Title,
		Status:   item.Status,
		Priority: item.Priority,
	}
}

func renderProjectActivityArtifactSignals(artifacts []projectwork.CollaborationArtifact) (ProjectActivityArtifactSummaryResponse, []ProjectWorkArtifactResponse) {
	if len(artifacts) == 0 {
		return ProjectActivityArtifactSummaryResponse{}, nil
	}
	items := append([]projectwork.CollaborationArtifact(nil), artifacts...)
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if !left.UpdatedAt.Equal(right.UpdatedAt) {
			return left.UpdatedAt.After(right.UpdatedAt)
		}
		if !left.CreatedAt.Equal(right.CreatedAt) {
			return left.CreatedAt.After(right.CreatedAt)
		}
		return left.ID < right.ID
	})
	latest := items[0]
	summary := ProjectActivityArtifactSummaryResponse{
		Count:        len(items),
		LatestKind:   latest.Kind,
		LatestTitle:  firstNonEmpty(latest.Title, latest.ID),
		LatestAt:     formatOptionalTime(firstNonZeroTime(latest.UpdatedAt, latest.CreatedAt)),
		AssignmentID: latest.AssignmentID,
	}
	limit := len(items)
	if limit > 3 {
		limit = 3
	}
	recent := make([]ProjectWorkArtifactResponse, 0, limit)
	for _, artifact := range items[:limit] {
		recent = append(recent, renderProjectWorkArtifact(artifact))
	}
	return summary, recent
}

func groupProjectActivityArtifacts(artifacts []projectwork.CollaborationArtifact) (map[string][]projectwork.CollaborationArtifact, map[string][]projectwork.CollaborationArtifact) {
	byAssignment := make(map[string][]projectwork.CollaborationArtifact)
	byWorkItem := make(map[string][]projectwork.CollaborationArtifact)
	for _, artifact := range artifacts {
		if artifact.AssignmentID != "" {
			byAssignment[artifact.AssignmentID] = append(byAssignment[artifact.AssignmentID], artifact)
			continue
		}
		byWorkItem[artifact.WorkItemID] = append(byWorkItem[artifact.WorkItemID], artifact)
	}
	return byAssignment, byWorkItem
}

func renderProjectActivityHandoffSignals(handoffs []projectwork.Handoff) (ProjectActivityHandoffSummaryResponse, []ProjectHandoffResponse) {
	if len(handoffs) == 0 {
		return ProjectActivityHandoffSummaryResponse{}, nil
	}
	items := append([]projectwork.Handoff(nil), handoffs...)
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if !left.UpdatedAt.Equal(right.UpdatedAt) {
			return left.UpdatedAt.After(right.UpdatedAt)
		}
		if !left.CreatedAt.Equal(right.CreatedAt) {
			return left.CreatedAt.After(right.CreatedAt)
		}
		return left.ID < right.ID
	})
	latest := items[0]
	summary := ProjectActivityHandoffSummaryResponse{
		Count:          len(items),
		LatestStatus:   latest.Status,
		LatestTitle:    firstNonEmpty(latest.Title, latest.ID),
		LatestAt:       formatOptionalTime(firstNonZeroTime(latest.UpdatedAt, latest.CreatedAt)),
		AssignmentID:   latest.SourceAssignmentID,
		TargetRoleID:   latest.TargetRoleID,
		TargetWorkItem: latest.TargetWorkItemID,
	}
	for _, item := range items {
		switch item.Status {
		case projectwork.HandoffStatusPending:
			summary.PendingCount++
		case projectwork.HandoffStatusAccepted:
			summary.AcceptedCount++
		}
	}
	limit := len(items)
	if limit > 3 {
		limit = 3
	}
	recent := make([]ProjectHandoffResponse, 0, limit)
	for _, handoff := range items[:limit] {
		recent = append(recent, renderProjectHandoff(handoff))
	}
	return summary, recent
}

func groupProjectActivityHandoffs(handoffs []projectwork.Handoff) (map[string][]projectwork.Handoff, map[string][]projectwork.Handoff) {
	byAssignment := make(map[string][]projectwork.Handoff)
	byWorkItem := make(map[string][]projectwork.Handoff)
	for _, handoff := range handoffs {
		attached := false
		if handoff.SourceAssignmentID != "" {
			byAssignment[handoff.SourceAssignmentID] = append(byAssignment[handoff.SourceAssignmentID], handoff)
			attached = true
		}
		if handoff.TargetAssignmentID != "" && handoff.TargetAssignmentID != handoff.SourceAssignmentID {
			byAssignment[handoff.TargetAssignmentID] = append(byAssignment[handoff.TargetAssignmentID], handoff)
			attached = true
		}
		if !attached {
			byWorkItem[handoff.WorkItemID] = append(byWorkItem[handoff.WorkItemID], handoff)
		}
	}
	return byAssignment, byWorkItem
}

func sortProjectActivityItems(items []ProjectActivityItemResponse) {
	sort.SliceStable(items, func(i, j int) bool {
		left, right := parseProjectActivityTime(items[i].UpdatedAt), parseProjectActivityTime(items[j].UpdatedAt)
		if !left.Equal(right) {
			return left.After(right)
		}
		return items[i].ID < items[j].ID
	})
}

func boundedProjectActivityItems(items []ProjectActivityItemResponse, limit int) []ProjectActivityItemResponse {
	if len(items) == 0 {
		return []ProjectActivityItemResponse{}
	}
	if limit > 0 && len(items) > limit {
		return append([]ProjectActivityItemResponse(nil), items[:limit]...)
	}
	return append([]ProjectActivityItemResponse(nil), items...)
}

func projectActivityBucket(item ProjectActivityItemResponse) string {
	switch item.BlockingSignal {
	case "awaiting_approval", "failed", "cancelled", "not_started", "stale_unknown":
		return "blocked"
	case "completed":
		return "completed"
	case "running":
		return "active"
	default:
		return "active"
	}
}

func projectActivityBlockingSignal(assignment ProjectWorkAssignmentResponse, linkedChat *ProjectActivityLinkedChatResponse) string {
	if linkedChat != nil && linkedChat.Missing {
		return "stale_unknown"
	}
	status := strings.TrimSpace(projectActivityProjectedStatus(assignment, linkedChat))
	switch status {
	case projectwork.AssignmentStatusAwaitingApproval:
		return "awaiting_approval"
	case projectwork.AssignmentStatusFailed:
		return "failed"
	case projectwork.AssignmentStatusCancelled, "closed":
		return "cancelled"
	case projectwork.AssignmentStatusCompleted:
		return "completed"
	case projectwork.AssignmentStatusRunning, projectwork.AssignmentStatusQueued:
		if assignment.Execution == nil && assignment.TaskID == "" && assignment.RunID != "" {
			return "stale_unknown"
		}
		if assignment.Execution != nil && assignment.Execution.Missing {
			return "stale_unknown"
		}
		if status == projectwork.AssignmentStatusQueued && assignment.TaskID == "" && assignment.RunID == "" && assignment.ChatSessionID == "" {
			return "not_started"
		}
		return "running"
	default:
		if assignment.Execution != nil && assignment.Execution.Missing {
			return "stale_unknown"
		}
		return "stale_unknown"
	}
}

func projectActivityStatusSummary(assignment ProjectWorkAssignmentResponse, linkedChat *ProjectActivityLinkedChatResponse, signal string, artifactCount int) string {
	switch signal {
	case "awaiting_approval":
		count := 0
		if assignment.Execution != nil {
			count = assignment.Execution.PendingApprovalCount
		}
		if count > 0 {
			return fmt.Sprintf("%d approval pending", count)
		}
		return "awaiting approval"
	case "failed":
		if linkedChat != nil && linkedChat.LatestError != "" {
			return linkedChat.LatestError
		}
		if assignment.Execution != nil && assignment.Execution.LastError != "" {
			return assignment.Execution.LastError
		}
		if linkedChat != nil {
			return "linked chat failed"
		}
		return "failed run"
	case "cancelled":
		if linkedChat != nil {
			return "linked chat cancelled"
		}
		return "cancelled"
	case "not_started":
		return "not started"
	case "running":
		if linkedChat != nil {
			return projectActivityLinkedChatSummary(linkedChat)
		}
		return "running"
	case "completed":
		if artifactCount > 0 {
			return fmt.Sprintf("completed with %d artifact%s", artifactCount, pluralSuffix(artifactCount))
		}
		return "completed"
	default:
		if linkedChat != nil && linkedChat.Missing {
			return "linked chat missing"
		}
		return "stale or unknown"
	}
}

func projectActivityProjectedStatus(assignment ProjectWorkAssignmentResponse, linkedChat *ProjectActivityLinkedChatResponse) string {
	if linkedChat != nil {
		if linkedChat.Missing {
			return "stale_unknown"
		}
		if status := strings.TrimSpace(linkedChat.Status); status != "" && status != "idle" {
			return status
		}
		if status := strings.TrimSpace(linkedChat.LatestStatus); status != "" {
			return status
		}
		if strings.TrimSpace(linkedChat.Status) == "idle" {
			return firstNonEmpty(projectActivityExecutionStatus(assignment), assignment.Status, projectwork.AssignmentStatusRunning)
		}
	}
	return firstNonEmpty(projectActivityExecutionStatus(assignment), assignment.Status)
}

func projectActivityLinkedChatSummary(linkedChat *ProjectActivityLinkedChatResponse) string {
	if linkedChat == nil {
		return ""
	}
	parts := []string{"linked chat"}
	if linkedChat.Status != "" {
		parts = append(parts, linkedChat.Status)
	}
	if linkedChat.LatestRole != "" && linkedChat.LatestStatus != "" {
		parts = append(parts, linkedChat.LatestRole+" "+linkedChat.LatestStatus)
	}
	if linkedChat.MessageCount > 0 {
		parts = append(parts, fmt.Sprintf("%d message%s", linkedChat.MessageCount, pluralSuffix(linkedChat.MessageCount)))
	}
	return strings.Join(parts, " · ")
}

func projectActivityExecutionStatus(assignment ProjectWorkAssignmentResponse) string {
	if assignment.Execution == nil {
		return ""
	}
	return assignment.Execution.Status
}

func projectActivityExecutionTaskID(assignment ProjectWorkAssignmentResponse) string {
	if assignment.Execution == nil {
		return ""
	}
	return assignment.Execution.TaskID
}

func projectActivityExecutionRunID(assignment ProjectWorkAssignmentResponse) string {
	if assignment.Execution == nil {
		return ""
	}
	return assignment.Execution.RunID
}

func projectActivityUpdatedAt(workItem ProjectWorkItemResponse, assignment ProjectWorkAssignmentResponse, linkedChat *ProjectActivityLinkedChatResponse, artifacts ProjectActivityArtifactSummaryResponse, handoffs ProjectActivityHandoffSummaryResponse) string {
	latest := parseProjectActivityTime(firstNonEmpty(assignment.CompletedAt, assignment.StartedAt, assignment.UpdatedAt, assignment.CreatedAt))
	workUpdated := parseProjectActivityTime(workItem.UpdatedAt)
	chatUpdated := time.Time{}
	if linkedChat != nil {
		chatUpdated = parseProjectActivityTime(linkedChat.UpdatedAt)
	}
	artifactUpdated := parseProjectActivityTime(artifacts.LatestAt)
	handoffUpdated := parseProjectActivityTime(handoffs.LatestAt)
	if workUpdated.After(latest) {
		latest = workUpdated
	}
	if chatUpdated.After(latest) {
		latest = chatUpdated
	}
	if artifactUpdated.After(latest) {
		latest = artifactUpdated
	}
	if handoffUpdated.After(latest) {
		latest = handoffUpdated
	}
	return formatOptionalTime(latest)
}

func parseProjectActivityTime(value string) time.Time {
	if strings.TrimSpace(value) == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}
