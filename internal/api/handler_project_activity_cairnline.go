package api

import (
	"context"
	"errors"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/cairnlinebridge"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectwork"
)

func (h *Handler) renderCairnlineProjectActivity(ctx context.Context, projectID string) (ProjectActivityDataResponse, error) {
	if h.requiresEmbeddedCairnlineProjectReads() {
		return h.renderStrictEmbeddedCairnlineProjectActivity(ctx, projectID)
	}
	view, err := h.cairnlineProjectWorkView(ctx, projectID)
	if err != nil {
		return ProjectActivityDataResponse{}, err
	}
	defer view.Close()
	return h.renderCairnlineProjectActivityFromService(ctx, view.service, view.snapshot)
}

func (h *Handler) renderStrictEmbeddedCairnlineProjectActivity(ctx context.Context, projectID string) (ProjectActivityDataResponse, error) {
	_, service, store, err := h.openCairnlineEmbeddedService(ctx)
	if err != nil {
		return ProjectActivityDataResponse{}, err
	}
	defer store.Close()
	project, err := service.GetProject(ctx, projectID)
	if errors.Is(err, cairnline.ErrNotFound) {
		return ProjectActivityDataResponse{}, projects.ErrNotFound
	}
	if err != nil {
		return ProjectActivityDataResponse{}, err
	}
	return h.renderCairnlineProjectActivityFromService(ctx, service, cairnlinebridge.Snapshot{
		Project: projectFromCairnline(project, nil, projects.Project{}),
	})
}

func (h *Handler) renderCairnlineSidecarProjectActivity(ctx context.Context, projectID string) (ProjectActivityDataResponse, error) {
	projectItem, ok, err := h.cairnlineSidecarProject(ctx, projectID)
	if err != nil {
		return ProjectActivityDataResponse{}, err
	}
	if !ok {
		return ProjectActivityDataResponse{}, projects.ErrNotFound
	}
	project := projectFromCairnlineSidecar(projectItem)
	activity, err := h.cairnlineSidecarProjectActivity(ctx, project.ID)
	if err != nil {
		return ProjectActivityDataResponse{}, err
	}
	workItemItems, err := h.cairnlineSidecarProjectWorkItems(ctx, project.ID)
	if err != nil {
		return ProjectActivityDataResponse{}, err
	}
	assignmentItems, err := h.cairnlineSidecarProjectAssignments(ctx, project.ID)
	if err != nil {
		return ProjectActivityDataResponse{}, err
	}
	roleItems, err := h.cairnlineSidecarProjectRoles(ctx, project.ID)
	if err != nil {
		return ProjectActivityDataResponse{}, err
	}
	artifactItems, err := h.cairnlineSidecarProjectArtifacts(ctx, project.ID)
	if err != nil {
		return ProjectActivityDataResponse{}, err
	}
	evidenceItems, err := h.cairnlineSidecarProjectEvidence(ctx, project.ID)
	if err != nil {
		return ProjectActivityDataResponse{}, err
	}
	reviewItems, err := h.cairnlineSidecarProjectReviews(ctx, project.ID)
	if err != nil {
		return ProjectActivityDataResponse{}, err
	}
	handoffItems, err := h.cairnlineSidecarProjectHandoffs(ctx, project.ID)
	if err != nil {
		return ProjectActivityDataResponse{}, err
	}

	workItems := projectWorkItemsFromCairnlineSidecar(workItemItems)
	assignments := projectAssignmentsFromCairnlineSidecar(assignmentItems)
	assignmentsByWorkItem := groupProjectWorkAssignmentsByWorkItem(assignments)
	projectedWorkItems := make(map[string]ProjectWorkItemResponse, len(workItems))
	projectedAssignments := make(map[string]ProjectWorkAssignmentResponse, len(assignments))
	for _, item := range workItems {
		projected, err := h.renderProjectedProjectWorkItemWithAssignments(ctx, item, assignmentsByWorkItem[item.ID])
		if err != nil {
			return ProjectActivityDataResponse{}, err
		}
		projected.ReadBackend = "cairnline"
		markProjectWorkAssignmentReadBackend(projected.Assignments, "cairnline")
		projectedWorkItems[item.ID] = projected
		for _, assignment := range projected.Assignments {
			projectedAssignments[assignment.ID] = assignment
		}
	}
	rolesByID := projectWorkRolesByID(projectRolesFromCairnlineSidecar(roleItems))
	linkedChats := h.projectActivityLinkedChats(ctx, project.ID, assignments)
	artifactsByAssignment, artifactsByWorkItem := groupProjectActivityArtifacts(projectArtifactsFromCairnlineSidecar(artifactItems, evidenceItems, reviewItems))
	handoffsByAssignment, handoffsByWorkItem := groupProjectActivityHandoffs(projectHandoffsFromCairnlineSidecar(handoffItems))

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
		items = append(items, renderProjectActivityItem(projectedWorkItem, projectedAssignment, rolesByID[projectedAssignment.RoleID], activityArtifacts, activityHandoffs, linkedChats[projectedAssignment.ID]))
	}
	sortProjectActivityItems(items)

	response := ProjectActivityDataResponse{
		ProjectID:   project.ID,
		ReadBackend: "cairnline",
		Recent:      boundedProjectActivityItems(items, 20),
	}
	response.Summary.WorkItemCount = len(workItems)
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

func (h *Handler) renderCairnlineProjectActivityFromService(ctx context.Context, service *cairnline.Service, snapshot cairnlinebridge.Snapshot) (ProjectActivityDataResponse, error) {
	projectID := snapshot.Project.ID
	activity, err := service.ProjectActivity(ctx, projectID)
	if err != nil {
		return ProjectActivityDataResponse{}, err
	}
	cairnlineWorkItems, err := service.ListWorkItems(ctx, projectID)
	if err != nil {
		return ProjectActivityDataResponse{}, err
	}
	cairnlineAssignments, err := service.ListAssignments(ctx, projectID)
	if err != nil {
		return ProjectActivityDataResponse{}, err
	}
	cairnlineRoles, err := service.ListRoles(ctx, projectID)
	if err != nil {
		return ProjectActivityDataResponse{}, err
	}
	executionProfiles, err := service.ListExecutionProfiles(ctx)
	if err != nil {
		return ProjectActivityDataResponse{}, err
	}
	artifacts, err := projectHealthCairnlineArtifacts(ctx, service, projectID, cairnlineWorkItems)
	if err != nil {
		return ProjectActivityDataResponse{}, err
	}
	handoffs, err := projectHealthCairnlineHandoffs(ctx, service, projectID, cairnlineWorkItems)
	if err != nil {
		return ProjectActivityDataResponse{}, err
	}

	workItems := projectWorkItemsFromCairnlineWithNativeTimestamps(cairnlineWorkItems, snapshot.WorkItems)
	assignments := projectWorkAssignmentsFromCairnline(cairnlineAssignments, snapshot.Assignments)
	assignmentsByWorkItem := groupProjectWorkAssignmentsByWorkItem(assignments)
	projectedWorkItems := make(map[string]ProjectWorkItemResponse, len(workItems))
	projectedAssignments := make(map[string]ProjectWorkAssignmentResponse, len(assignments))
	for _, item := range workItems {
		projected, err := h.renderProjectedProjectWorkItemWithAssignments(ctx, item, assignmentsByWorkItem[item.ID])
		if err != nil {
			return ProjectActivityDataResponse{}, err
		}
		projectedWorkItems[item.ID] = projected
		for _, assignment := range projected.Assignments {
			projectedAssignments[assignment.ID] = assignment
		}
	}
	rolesByID := projectActivityCairnlineRolesByID(cairnlineRoles, executionProfiles, snapshot.Roles)
	linkedChats := h.projectActivityLinkedChats(ctx, projectID, assignments)
	artifactsByAssignment, artifactsByWorkItem := groupProjectActivityArtifacts(artifacts)
	handoffsByAssignment, handoffsByWorkItem := groupProjectActivityHandoffs(handoffs)

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
	response.Summary.WorkItemCount = len(workItems)
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

func projectActivityCairnlineRolesByID(items []cairnline.Role, executionProfiles []cairnline.ExecutionProfile, native []projectwork.AgentRoleProfile) map[string]projectwork.AgentRoleProfile {
	out := make(map[string]projectwork.AgentRoleProfile, len(items))
	executionProfilesByID := cairnlineExecutionProfilesByID(executionProfiles)
	nativeByID := projectWorkRolesByID(native)
	for _, item := range items {
		out[item.ID] = projectWorkRoleFromCairnline(item, executionProfilesByID, nativeByID[item.ID])
	}
	return out
}
