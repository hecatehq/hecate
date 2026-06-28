package api

import (
	"context"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/cairnlinebridge"
	"github.com/hecatehq/hecate/internal/projectwork"
)

func (h *Handler) renderCairnlineProjectActivity(ctx context.Context, projectID string) (ProjectActivityDataResponse, error) {
	service, snapshot, err := h.cairnlineProjectWorkService(ctx, projectID)
	if err != nil {
		return ProjectActivityDataResponse{}, err
	}
	return h.renderCairnlineProjectActivityFromService(ctx, service, snapshot)
}

func (h *Handler) renderCairnlineProjectActivityFromService(ctx context.Context, service *cairnline.Service, snapshot cairnlinebridge.Snapshot) (ProjectActivityDataResponse, error) {
	projectID := snapshot.Project.ID
	activity, err := service.ProjectActivity(ctx, projectID)
	if err != nil {
		return ProjectActivityDataResponse{}, err
	}
	assignmentsByWorkItem := groupProjectWorkAssignmentsByWorkItem(snapshot.Assignments)
	projectedWorkItems := make(map[string]ProjectWorkItemResponse, len(snapshot.WorkItems))
	projectedAssignments := make(map[string]ProjectWorkAssignmentResponse, len(snapshot.Assignments))
	for _, item := range snapshot.WorkItems {
		projected, err := h.renderProjectedProjectWorkItemWithAssignments(ctx, item, assignmentsByWorkItem[item.ID])
		if err != nil {
			return ProjectActivityDataResponse{}, err
		}
		projectedWorkItems[item.ID] = projected
		for _, assignment := range projected.Assignments {
			projectedAssignments[assignment.ID] = assignment
		}
	}
	rolesByID := projectActivitySnapshotRolesByID(snapshot.Roles)
	linkedChats := h.projectActivityLinkedChats(ctx, projectID, snapshot.Assignments)
	artifactsByAssignment, artifactsByWorkItem := groupProjectActivityArtifacts(snapshot.Artifacts)
	handoffsByAssignment, handoffsByWorkItem := groupProjectActivityHandoffs(snapshot.Handoffs)

	items := make([]ProjectActivityItemResponse, 0, len(activity.Items))
	for _, item := range activity.Items {
		projectedWorkItem, ok := projectedWorkItems[item.WorkItemID]
		if !ok {
			continue
		}
		projectedAssignment, ok := projectedAssignments[item.AssignmentID]
		if !ok {
			continue
		}
		activityArtifacts := artifactsByAssignment[projectedAssignment.ID]
		if len(activityArtifacts) == 0 {
			activityArtifacts = artifactsByWorkItem[projectedAssignment.WorkItemID]
		}
		activityHandoffs := handoffsByAssignment[projectedAssignment.ID]
		if len(activityHandoffs) == 0 {
			activityHandoffs = handoffsByWorkItem[projectedAssignment.WorkItemID]
		}
		role, _ := rolesByID[projectedAssignment.RoleID]
		items = append(items, renderProjectActivityItem(projectedWorkItem, projectedAssignment, role, activityArtifacts, activityHandoffs, linkedChats[projectedAssignment.ID]))
	}
	sortProjectActivityItems(items)

	response := ProjectActivityDataResponse{
		ProjectID:   projectID,
		ReadBackend: "cairnline",
		Recent:      boundedProjectActivityItems(items, 20),
	}
	response.Summary.WorkItemCount = len(snapshot.WorkItems)
	response.Summary.AssignmentCount = activity.Counts.Assignments
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

func projectActivitySnapshotRolesByID(items []projectwork.AgentRoleProfile) map[string]projectwork.AgentRoleProfile {
	out := make(map[string]projectwork.AgentRoleProfile, len(items))
	for _, item := range items {
		out[item.ID] = item
	}
	return out
}
