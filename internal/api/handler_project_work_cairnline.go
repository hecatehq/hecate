package api

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/cairnlinebridge"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectwork"
)

func (h *Handler) renderProjectWorkArtifacts(ctx context.Context, projectID, workItemID string) ([]ProjectWorkArtifactResponse, error) {
	if h.projectReadRoutesUseCairnlineReadModel() {
		return h.renderCairnlineProjectWorkArtifacts(ctx, projectID, workItemID)
	}
	items, err := h.projectWork.ListArtifacts(ctx, projectwork.ArtifactFilter{ProjectID: projectID, WorkItemID: workItemID})
	if err != nil {
		return nil, err
	}
	data := make([]ProjectWorkArtifactResponse, 0, len(items))
	for _, item := range items {
		projected := renderProjectWorkArtifact(item)
		projected.ReadBackend = "hecate"
		data = append(data, projected)
	}
	return data, nil
}

func (h *Handler) renderCairnlineProjectWorkArtifacts(ctx context.Context, projectID, workItemID string) ([]ProjectWorkArtifactResponse, error) {
	view, err := h.cairnlineProjectWorkView(ctx, projectID)
	if err != nil {
		return nil, err
	}
	defer view.Close()
	if _, err := view.service.GetWorkItem(ctx, view.snapshot.Project.ID, workItemID); errors.Is(err, cairnline.ErrNotFound) {
		return nil, projectwork.ErrNotFound
	} else if err != nil {
		return nil, err
	}
	items, err := cairnlineProjectWorkArtifacts(ctx, view.service, view.snapshot.Project.ID, workItemID)
	if err != nil {
		return nil, err
	}
	return renderCairnlineProjectWorkArtifactResponses(items), nil
}

func renderCairnlineProjectWorkArtifactResponses(items []projectwork.CollaborationArtifact) []ProjectWorkArtifactResponse {
	data := make([]ProjectWorkArtifactResponse, 0, len(items))
	for _, item := range items {
		projected := renderProjectWorkArtifact(item)
		projected.ReadBackend = "cairnline"
		data = append(data, projected)
	}
	return data
}

func (h *Handler) renderProjectHandoffs(ctx context.Context, filter projectwork.HandoffFilter) ([]ProjectHandoffResponse, error) {
	return h.renderProjectHandoffsWithWorkItemRequirement(ctx, filter, false)
}

func (h *Handler) renderProjectWorkItemHandoffs(ctx context.Context, filter projectwork.HandoffFilter) ([]ProjectHandoffResponse, error) {
	return h.renderProjectHandoffsWithWorkItemRequirement(ctx, filter, true)
}

func (h *Handler) renderProjectHandoffsWithWorkItemRequirement(ctx context.Context, filter projectwork.HandoffFilter, requireWorkItem bool) ([]ProjectHandoffResponse, error) {
	if h.projectReadRoutesUseCairnlineReadModel() {
		return h.renderCairnlineProjectHandoffs(ctx, filter, requireWorkItem)
	}
	items, err := h.projectWork.ListHandoffs(ctx, filter)
	if err != nil {
		return nil, err
	}
	data := make([]ProjectHandoffResponse, 0, len(items))
	for _, item := range items {
		projected := renderProjectHandoff(item)
		projected.ReadBackend = "hecate"
		data = append(data, projected)
	}
	return data, nil
}

func (h *Handler) renderCairnlineProjectHandoffs(ctx context.Context, filter projectwork.HandoffFilter, requireWorkItem bool) ([]ProjectHandoffResponse, error) {
	view, err := h.cairnlineProjectWorkView(ctx, filter.ProjectID)
	if err != nil {
		return nil, err
	}
	defer view.Close()
	workItemID := strings.TrimSpace(filter.WorkItemID)
	if requireWorkItem {
		if workItemID == "" {
			return nil, projectwork.ErrNotFound
		}
		if _, err := view.service.GetWorkItem(ctx, view.snapshot.Project.ID, workItemID); errors.Is(err, cairnline.ErrNotFound) {
			return nil, projectwork.ErrNotFound
		} else if err != nil {
			return nil, err
		}
	}
	items, err := cairnlineProjectHandoffs(ctx, view.service, view.snapshot.Project.ID, workItemID, strings.TrimSpace(filter.Status))
	if errors.Is(err, cairnline.ErrNotFound) {
		return nil, projectwork.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return renderCairnlineProjectHandoffResponses(items), nil
}

func renderCairnlineProjectHandoffResponses(items []projectwork.Handoff) []ProjectHandoffResponse {
	data := make([]ProjectHandoffResponse, 0, len(items))
	for _, item := range items {
		projected := renderProjectHandoff(item)
		projected.ReadBackend = "cairnline"
		data = append(data, projected)
	}
	return data
}

func (h *Handler) renderProjectWorkRoles(ctx context.Context, projectID string) ([]ProjectWorkRoleResponse, error) {
	if h.projectCairnlineSidecarReadRoutesEnabled() {
		return h.renderCairnlineSidecarProjectWorkRoles(ctx, projectID)
	}
	if h.projectReadRoutesUseCairnlineReadModel() {
		return h.renderCairnlineProjectWorkRoles(ctx, projectID)
	}
	roles, err := h.projectWork.ListRoles(ctx, projectID)
	if err != nil {
		return nil, err
	}
	data := make([]ProjectWorkRoleResponse, 0, len(roles))
	for _, role := range roles {
		projected := renderProjectWorkRole(role)
		projected.ReadBackend = "hecate"
		data = append(data, projected)
	}
	return data, nil
}

func (h *Handler) renderCairnlineSidecarProjectWorkRoles(ctx context.Context, projectID string) ([]ProjectWorkRoleResponse, error) {
	projectItem, ok, err := h.cairnlineSidecarProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, projects.ErrNotFound
	}
	project := projectFromCairnlineSidecar(projectItem)
	roles, err := h.cairnlineSidecarProjectRoles(ctx, project.ID)
	if err != nil {
		return nil, err
	}
	data := make([]ProjectWorkRoleResponse, 0, len(roles))
	for _, role := range projectRolesFromCairnlineSidecar(roles) {
		projected := renderProjectWorkRole(role)
		projected.ReadBackend = "cairnline"
		data = append(data, projected)
	}
	return data, nil
}

func (h *Handler) renderCairnlineProjectWorkRoles(ctx context.Context, projectID string) ([]ProjectWorkRoleResponse, error) {
	view, err := h.cairnlineProjectWorkView(ctx, projectID)
	if err != nil {
		return nil, err
	}
	defer view.Close()
	roles, err := view.service.ListRoles(ctx, view.snapshot.Project.ID)
	if err != nil {
		return nil, err
	}
	nativeByID := projectWorkRolesByID(view.snapshot.Roles)
	data := make([]ProjectWorkRoleResponse, 0, len(roles))
	for _, role := range roles {
		projected := renderProjectWorkRole(projectWorkRoleFromCairnline(role, nativeByID[role.ID]))
		projected.ReadBackend = "cairnline"
		data = append(data, projected)
	}
	return data, nil
}

func (h *Handler) renderProjectWorkItems(ctx context.Context, projectID string) ([]ProjectWorkItemResponse, error) {
	if h.projectCairnlineSidecarReadRoutesEnabled() {
		return h.renderCairnlineSidecarProjectWorkItems(ctx, projectID)
	}
	if h.projectReadRoutesUseCairnlineReadModel() {
		return h.renderCairnlineProjectWorkItems(ctx, projectID)
	}
	return h.renderNativeProjectWorkItems(ctx, projectID)
}

func (h *Handler) renderNativeProjectWorkItems(ctx context.Context, projectID string) ([]ProjectWorkItemResponse, error) {
	items, err := h.projectWork.ListWorkItems(ctx, projectID)
	if err != nil {
		return nil, err
	}
	assignments, err := h.projectWork.ListAssignments(ctx, projectwork.AssignmentFilter{ProjectID: projectID})
	if err != nil {
		return nil, err
	}
	assignments, err = h.projectWorkApplication().ApplyAssignmentsRuntime(ctx, assignments)
	if err != nil {
		return nil, err
	}
	assignmentsByWorkItem := groupProjectWorkAssignmentsByWorkItem(assignments)
	data := make([]ProjectWorkItemResponse, 0, len(items))
	for _, item := range items {
		projected, err := h.renderProjectedProjectWorkItemWithAssignments(ctx, item, assignmentsByWorkItem[item.ID])
		if err != nil {
			return nil, err
		}
		projected.ReadBackend = "hecate"
		markProjectWorkAssignmentReadBackend(projected.Assignments, "hecate")
		data = append(data, projected)
	}
	return data, nil
}

func (h *Handler) renderCairnlineSidecarProjectWorkItems(ctx context.Context, projectID string) ([]ProjectWorkItemResponse, error) {
	projectItem, ok, err := h.cairnlineSidecarProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, projects.ErrNotFound
	}
	project := projectFromCairnlineSidecar(projectItem)
	items, err := h.cairnlineSidecarProjectWorkItems(ctx, project.ID)
	if err != nil {
		return nil, err
	}
	assignments, err := h.cairnlineSidecarProjectAssignments(ctx, project.ID)
	if err != nil {
		return nil, err
	}
	workItems := projectWorkItemsFromCairnlineSidecar(items)
	assignmentsByWorkItem := groupProjectWorkAssignmentsByWorkItem(projectAssignmentsFromCairnlineSidecar(assignments))
	data := make([]ProjectWorkItemResponse, 0, len(workItems))
	for _, item := range workItems {
		projected, err := h.renderProjectedProjectWorkItemWithAssignments(ctx, item, assignmentsByWorkItem[item.ID])
		if err != nil {
			return nil, err
		}
		projected.ReadBackend = "cairnline"
		markProjectWorkAssignmentReadBackend(projected.Assignments, "cairnline")
		data = append(data, projected)
	}
	return data, nil
}

func (h *Handler) renderCairnlineSidecarProjectWorkItem(ctx context.Context, projectID, workItemID string) (ProjectWorkItemResponse, error) {
	items, err := h.renderCairnlineSidecarProjectWorkItems(ctx, projectID)
	if err != nil {
		return ProjectWorkItemResponse{}, err
	}
	workItemID = strings.TrimSpace(workItemID)
	for _, item := range items {
		if item.ID == workItemID {
			return item, nil
		}
	}
	return ProjectWorkItemResponse{}, projectwork.ErrNotFound
}

func (h *Handler) renderCairnlineSidecarProjectWorkAssignments(ctx context.Context, projectID, workItemID string) ([]ProjectWorkAssignmentResponse, error) {
	project, err := h.cairnlineSidecarProjectWithRequiredWorkItem(ctx, projectID, workItemID)
	if err != nil {
		return nil, err
	}
	workItemID = strings.TrimSpace(workItemID)
	items, err := h.cairnlineSidecarProjectAssignments(ctx, project.ID)
	if err != nil {
		return nil, err
	}
	assignments := projectAssignmentsFromCairnlineSidecar(items)
	data := make([]ProjectWorkAssignmentResponse, 0, len(assignments))
	for _, assignment := range assignments {
		if strings.TrimSpace(assignment.WorkItemID) != workItemID {
			continue
		}
		projected, err := h.renderProjectedProjectWorkAssignment(ctx, assignment)
		if err != nil {
			return nil, err
		}
		projected.ReadBackend = "cairnline"
		data = append(data, projected)
	}
	return data, nil
}

func (h *Handler) renderCairnlineSidecarProjectWorkArtifacts(ctx context.Context, projectID, workItemID string) ([]ProjectWorkArtifactResponse, error) {
	project, err := h.cairnlineSidecarProjectWithRequiredWorkItem(ctx, projectID, workItemID)
	if err != nil {
		return nil, err
	}
	workItemID = strings.TrimSpace(workItemID)
	artifacts, err := h.cairnlineSidecarProjectArtifactList(ctx, project.ID, workItemID)
	if err != nil {
		return nil, err
	}
	evidence, err := h.cairnlineSidecarProjectEvidenceList(ctx, project.ID, workItemID)
	if err != nil {
		return nil, err
	}
	reviews, err := h.cairnlineSidecarProjectReviewList(ctx, project.ID, workItemID)
	if err != nil {
		return nil, err
	}
	items := projectArtifactsFromCairnlineSidecar(artifacts, evidence, reviews)
	sortProjectWorkArtifactsForProjection(items)
	data := make([]ProjectWorkArtifactResponse, 0, len(items))
	for _, item := range items {
		projected := renderProjectWorkArtifact(item)
		projected.ReadBackend = "cairnline"
		data = append(data, projected)
	}
	return data, nil
}

func (h *Handler) renderCairnlineSidecarProjectWorkItemReadiness(ctx context.Context, projectID, workItemID string) (ProjectWorkItemReadinessResponse, error) {
	project, workItem, err := h.cairnlineSidecarProjectAndRequiredWorkItem(ctx, projectID, workItemID)
	if err != nil {
		return ProjectWorkItemReadinessResponse{}, err
	}
	workItemID = strings.TrimSpace(workItemID)
	assignmentItems, err := h.cairnlineSidecarProjectAssignments(ctx, project.ID)
	if err != nil {
		return ProjectWorkItemReadinessResponse{}, err
	}
	assignments := filterProjectWorkAssignments(projectAssignmentsFromCairnlineSidecar(assignmentItems), workItemID)
	artifactItems, err := h.cairnlineSidecarProjectArtifactList(ctx, project.ID, workItemID)
	if err != nil {
		return ProjectWorkItemReadinessResponse{}, err
	}
	evidenceItems, err := h.cairnlineSidecarProjectEvidenceList(ctx, project.ID, workItemID)
	if err != nil {
		return ProjectWorkItemReadinessResponse{}, err
	}
	reviewItems, err := h.cairnlineSidecarProjectReviewList(ctx, project.ID, workItemID)
	if err != nil {
		return ProjectWorkItemReadinessResponse{}, err
	}
	handoffItems, err := h.cairnlineSidecarProjectHandoffList(ctx, project.ID, workItemID)
	if err != nil {
		return ProjectWorkItemReadinessResponse{}, err
	}
	readiness := projectwork.EvaluateWorkItemReadiness(
		workItem,
		assignments,
		projectArtifactsFromCairnlineSidecar(artifactItems, evidenceItems, reviewItems),
		projectHandoffsFromCairnlineSidecar(handoffItems),
	)
	rendered := renderProjectWorkItemReadiness(readiness)
	rendered.ReadBackend = "cairnline"
	return rendered, nil
}

func (h *Handler) renderCairnlineSidecarProjectHandoffs(ctx context.Context, filter projectwork.HandoffFilter) ([]ProjectHandoffResponse, error) {
	projectItem, ok, err := h.cairnlineSidecarProject(ctx, filter.ProjectID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, projects.ErrNotFound
	}
	project := projectFromCairnlineSidecar(projectItem)
	return h.renderCairnlineSidecarProjectHandoffsForProject(ctx, project.ID, filter)
}

func (h *Handler) renderCairnlineSidecarProjectWorkItemHandoffs(ctx context.Context, filter projectwork.HandoffFilter) ([]ProjectHandoffResponse, error) {
	project, err := h.cairnlineSidecarProjectWithRequiredWorkItem(ctx, filter.ProjectID, filter.WorkItemID)
	if err != nil {
		return nil, err
	}
	return h.renderCairnlineSidecarProjectHandoffsForProject(ctx, project.ID, filter)
}

func (h *Handler) renderCairnlineSidecarProjectHandoffsForProject(ctx context.Context, projectID string, filter projectwork.HandoffFilter) ([]ProjectHandoffResponse, error) {
	items, err := h.cairnlineSidecarProjectHandoffList(ctx, projectID, filter.WorkItemID)
	if err != nil {
		return nil, err
	}
	handoffs := projectHandoffsFromCairnlineSidecar(items)
	status := strings.TrimSpace(filter.Status)
	sortProjectHandoffsForProjection(handoffs)
	data := make([]ProjectHandoffResponse, 0, len(handoffs))
	for _, handoff := range handoffs {
		if status != "" && handoff.Status != status {
			continue
		}
		projected := renderProjectHandoff(handoff)
		projected.ReadBackend = "cairnline"
		data = append(data, projected)
	}
	return data, nil
}

func (h *Handler) cairnlineSidecarProjectWithRequiredWorkItem(ctx context.Context, projectID, workItemID string) (projects.Project, error) {
	project, _, err := h.cairnlineSidecarProjectAndRequiredWorkItem(ctx, projectID, workItemID)
	return project, err
}

func (h *Handler) cairnlineSidecarProjectAndRequiredWorkItem(ctx context.Context, projectID, workItemID string) (projects.Project, projectwork.WorkItem, error) {
	projectItem, ok, err := h.cairnlineSidecarProject(ctx, projectID)
	if err != nil {
		return projects.Project{}, projectwork.WorkItem{}, err
	}
	if !ok {
		return projects.Project{}, projectwork.WorkItem{}, projects.ErrNotFound
	}
	project := projectFromCairnlineSidecar(projectItem)
	workItemID = strings.TrimSpace(workItemID)
	if workItemID == "" {
		return projects.Project{}, projectwork.WorkItem{}, projectwork.ErrNotFound
	}
	workItems, err := h.cairnlineSidecarProjectWorkItems(ctx, project.ID)
	if err != nil {
		return projects.Project{}, projectwork.WorkItem{}, err
	}
	for _, item := range projectWorkItemsFromCairnlineSidecar(workItems) {
		if item.ID == workItemID {
			return project, item, nil
		}
	}
	return projects.Project{}, projectwork.WorkItem{}, projectwork.ErrNotFound
}

func (h *Handler) renderCairnlineProjectWorkItems(ctx context.Context, projectID string) ([]ProjectWorkItemResponse, error) {
	view, err := h.cairnlineProjectWorkView(ctx, projectID)
	if err != nil {
		return nil, err
	}
	defer view.Close()
	return h.renderCairnlineProjectWorkItemsFromService(ctx, view.service, view.snapshot)
}

func (h *Handler) renderCairnlineProjectWorkItemsFromService(ctx context.Context, service *cairnline.Service, snapshot cairnlinebridge.Snapshot) ([]ProjectWorkItemResponse, error) {
	items, err := service.ListWorkItems(ctx, snapshot.Project.ID)
	if err != nil {
		return nil, err
	}
	cairnlineAssignments, err := service.ListAssignments(ctx, snapshot.Project.ID)
	if err != nil {
		return nil, err
	}
	workItems := projectWorkItemsFromCairnlineWithNativeTimestamps(items, snapshot.WorkItems)
	assignments := projectWorkAssignmentsFromCairnline(cairnlineAssignments, snapshot.Assignments)
	assignmentsByWorkItem := groupProjectWorkAssignmentsByWorkItem(assignments)
	data := make([]ProjectWorkItemResponse, 0, len(items))
	for _, item := range workItems {
		projected, err := h.renderProjectedProjectWorkItemWithAssignments(ctx, item, assignmentsByWorkItem[item.ID])
		if err != nil {
			return nil, err
		}
		projected.ReadBackend = "cairnline"
		markProjectWorkAssignmentReadBackend(projected.Assignments, "cairnline")
		data = append(data, projected)
	}
	return data, nil
}

func (h *Handler) renderCairnlineProjectWorkItem(ctx context.Context, projectID, workItemID string) (ProjectWorkItemResponse, error) {
	view, err := h.cairnlineProjectWorkView(ctx, projectID)
	if err != nil {
		return ProjectWorkItemResponse{}, err
	}
	defer view.Close()
	items, err := view.service.ListWorkItems(ctx, view.snapshot.Project.ID)
	if err != nil {
		return ProjectWorkItemResponse{}, err
	}
	cairnlineAssignments, err := view.service.ListAssignments(ctx, view.snapshot.Project.ID)
	if err != nil {
		return ProjectWorkItemResponse{}, err
	}
	workItems := projectWorkItemsFromCairnlineWithNativeTimestamps(items, view.snapshot.WorkItems)
	assignments := projectWorkAssignmentsFromCairnline(cairnlineAssignments, view.snapshot.Assignments)
	assignmentsByWorkItem := groupProjectWorkAssignmentsByWorkItem(assignments)
	for _, item := range workItems {
		if item.ID != workItemID {
			continue
		}
		projected, err := h.renderProjectedProjectWorkItemWithAssignments(ctx, item, assignmentsByWorkItem[item.ID])
		if err != nil {
			return ProjectWorkItemResponse{}, err
		}
		projected.ReadBackend = "cairnline"
		markProjectWorkAssignmentReadBackend(projected.Assignments, "cairnline")
		return projected, nil
	}
	return ProjectWorkItemResponse{}, projectwork.ErrNotFound
}

func cairnlineProjectWorkArtifacts(ctx context.Context, service *cairnline.Service, projectID, workItemID string) ([]projectwork.CollaborationArtifact, error) {
	artifacts, err := service.ListArtifacts(ctx, projectID, workItemID)
	if err != nil {
		return nil, err
	}
	evidence, err := service.ListEvidence(ctx, projectID, workItemID)
	if err != nil {
		return nil, err
	}
	reviews, err := service.ListReviews(ctx, projectID, workItemID)
	if err != nil {
		return nil, err
	}
	out := make([]projectwork.CollaborationArtifact, 0, len(artifacts)+len(evidence)+len(reviews))
	for _, item := range artifacts {
		out = append(out, projectWorkArtifactFromCairnline(item))
	}
	for _, item := range evidence {
		out = append(out, projectHealthEvidenceFromCairnline(item))
	}
	for _, item := range reviews {
		out = append(out, projectHealthReviewFromCairnline(item))
	}
	sort.SliceStable(out, func(i, j int) bool {
		return projectWorkArtifactProjectionLess(out[i], out[j])
	})
	return out, nil
}

func projectWorkArtifactFromCairnline(item cairnline.Artifact) projectwork.CollaborationArtifact {
	return projectwork.CollaborationArtifact{
		ID:           item.ID,
		ProjectID:    item.ProjectID,
		WorkItemID:   item.WorkItemID,
		AssignmentID: item.AssignmentID,
		Kind:         item.Kind,
		Title:        item.Title,
		Body:         item.Body,
		AuthorRoleID: item.AuthorRoleID,
		CreatedAt:    item.CreatedAt,
		UpdatedAt:    item.UpdatedAt,
	}
}

func cairnlineProjectHandoffs(ctx context.Context, service *cairnline.Service, projectID, workItemID, status string) ([]projectwork.Handoff, error) {
	workItems, err := service.ListWorkItems(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out := make([]projectwork.Handoff, 0)
	for _, item := range workItems {
		if workItemID != "" && item.ID != workItemID {
			continue
		}
		handoffs, err := service.ListHandoffs(ctx, projectID, item.ID)
		if err != nil {
			return nil, err
		}
		for _, handoff := range handoffs {
			projected := projectHealthHandoffFromCairnline(handoff)
			if status != "" && projected.Status != status {
				continue
			}
			out = append(out, projected)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return projectHandoffProjectionLess(out[i], out[j])
	})
	return out, nil
}

func sortProjectWorkArtifactsForProjection(items []projectwork.CollaborationArtifact) {
	sort.SliceStable(items, func(i, j int) bool {
		return projectWorkArtifactProjectionLess(items[i], items[j])
	})
}

func filterProjectWorkAssignments(items []projectwork.Assignment, workItemID string) []projectwork.Assignment {
	workItemID = strings.TrimSpace(workItemID)
	out := make([]projectwork.Assignment, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.WorkItemID) == workItemID {
			out = append(out, item)
		}
	}
	return out
}

func projectWorkArtifactProjectionLess(a, b projectwork.CollaborationArtifact) bool {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.ID < b.ID
}

func sortProjectHandoffsForProjection(items []projectwork.Handoff) {
	sort.SliceStable(items, func(i, j int) bool {
		return projectHandoffProjectionLess(items[i], items[j])
	})
}

func projectHandoffProjectionLess(a, b projectwork.Handoff) bool {
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return a.UpdatedAt.After(b.UpdatedAt)
	}
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.After(b.CreatedAt)
	}
	return a.ID < b.ID
}

func (h *Handler) renderCairnlineProjectWorkAssignments(ctx context.Context, projectID, workItemID string) ([]ProjectWorkAssignmentResponse, error) {
	view, err := h.cairnlineProjectWorkView(ctx, projectID)
	if err != nil {
		return nil, err
	}
	defer view.Close()
	return h.renderCairnlineProjectWorkAssignmentsFromService(ctx, view.service, view.snapshot, workItemID)
}

func (h *Handler) renderCairnlineProjectWorkAssignmentsFromService(ctx context.Context, service *cairnline.Service, snapshot cairnlinebridge.Snapshot, workItemID string) ([]ProjectWorkAssignmentResponse, error) {
	if _, err := service.GetWorkItem(ctx, snapshot.Project.ID, workItemID); err != nil {
		if errors.Is(err, cairnline.ErrNotFound) {
			return nil, projectwork.ErrNotFound
		}
		return nil, err
	}
	items, err := service.ListAssignments(ctx, snapshot.Project.ID)
	if err != nil {
		return nil, err
	}
	assignments := projectWorkAssignmentsFromCairnline(items, snapshot.Assignments)
	data := make([]ProjectWorkAssignmentResponse, 0, len(items))
	for _, assignment := range assignments {
		if strings.TrimSpace(assignment.WorkItemID) != workItemID {
			continue
		}
		projected, err := h.renderProjectedProjectWorkAssignment(ctx, assignment)
		if err != nil {
			return nil, err
		}
		projected.ReadBackend = "cairnline"
		data = append(data, projected)
	}
	return data, nil
}

func markProjectWorkAssignmentReadBackend(items []ProjectWorkAssignmentResponse, backend string) {
	for index := range items {
		items[index].ReadBackend = backend
	}
}

func (h *Handler) renderCairnlineProjectWorkItemReadiness(ctx context.Context, projectID, workItemID string) (ProjectWorkItemReadinessResponse, error) {
	view, err := h.cairnlineProjectWorkView(ctx, projectID)
	if err != nil {
		return ProjectWorkItemReadinessResponse{}, err
	}
	defer view.Close()
	readiness, err := h.cairnlineProjectWorkItemReadiness(ctx, view.service, view.snapshot, workItemID)
	if err != nil {
		return ProjectWorkItemReadinessResponse{}, err
	}
	rendered := renderProjectWorkItemReadiness(readiness)
	rendered.ReadBackend = "cairnline"
	return rendered, nil
}

func (h *Handler) cairnlineProjectWorkItemReadiness(ctx context.Context, service *cairnline.Service, snapshot cairnlinebridge.Snapshot, workItemID string) (projectwork.WorkItemReadiness, error) {
	workItem, err := service.GetWorkItem(ctx, snapshot.Project.ID, workItemID)
	if errors.Is(err, cairnline.ErrNotFound) {
		return projectwork.WorkItemReadiness{}, projectwork.ErrNotFound
	}
	if err != nil {
		return projectwork.WorkItemReadiness{}, err
	}
	cairnlineAssignments, err := service.ListAssignments(ctx, snapshot.Project.ID)
	if err != nil {
		return projectwork.WorkItemReadiness{}, err
	}
	assignments := filterProjectWorkAssignments(projectWorkAssignmentsFromCairnline(cairnlineAssignments, snapshot.Assignments), workItemID)
	assignments, err = h.applyRuntimeForCairnlineReadiness(ctx, assignments)
	if err != nil {
		return projectwork.WorkItemReadiness{}, err
	}
	artifacts, err := cairnlineProjectWorkArtifacts(ctx, service, snapshot.Project.ID, workItemID)
	if err != nil {
		return projectwork.WorkItemReadiness{}, err
	}
	handoffs, err := cairnlineProjectHandoffs(ctx, service, snapshot.Project.ID, workItemID, "")
	if err != nil {
		return projectwork.WorkItemReadiness{}, err
	}
	return projectwork.EvaluateWorkItemReadiness(projectWorkItemFromCairnline(workItem), assignments, artifacts, handoffs), nil
}

func (h *Handler) applyRuntimeForCairnlineReadiness(ctx context.Context, assignments []projectwork.Assignment) ([]projectwork.Assignment, error) {
	projected, err := h.projectWorkApplication().ApplyAssignmentsRuntimeProjection(ctx, assignments)
	if err != nil {
		return nil, err
	}
	for index := range projected {
		if index >= len(assignments) {
			break
		}
		status := strings.TrimSpace(assignments[index].Status)
		if !terminalProjectWorkAssignmentStatus(status) {
			continue
		}
		projected[index].Status = status
		projected[index].ExecutionRef.Status = status
	}
	return projected, nil
}

func terminalProjectWorkAssignmentStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case projectwork.AssignmentStatusCompleted, projectwork.AssignmentStatusFailed, projectwork.AssignmentStatusCancelled:
		return true
	default:
		return false
	}
}

type cairnlineProjectWorkView struct {
	service  *cairnline.Service
	snapshot cairnlinebridge.Snapshot
	close    func() error
}

func (v *cairnlineProjectWorkView) Close() error {
	if v == nil || v.close == nil {
		return nil
	}
	return v.close()
}

func (h *Handler) cairnlineProjectWorkView(ctx context.Context, projectID string) (*cairnlineProjectWorkView, error) {
	if h.requiresEmbeddedCairnlineProjectReads() {
		return h.cairnlineEmbeddedProjectWorkView(ctx, projectID)
	}
	snapshot, err := cairnlinebridge.LoadSnapshot(ctx, h.cairnlineSnapshotSources(), projectID)
	if err != nil {
		return nil, err
	}
	if h.prefersEmbeddedCairnlineProjectReads() {
		_, service, store, err := h.openCairnlineEmbeddedService(ctx)
		if err == nil {
			if _, err := service.GetProject(ctx, snapshot.Project.ID); err != nil {
				_ = store.Close()
				if errors.Is(err, cairnline.ErrNotFound) {
					return h.cairnlineProjectWorkSeededView(ctx, snapshot)
				}
				return nil, err
			}
			return &cairnlineProjectWorkView{
				service:  service,
				snapshot: snapshot,
				close:    store.Close,
			}, nil
		}
		if !errors.Is(err, cairnline.ErrNotFound) {
			return nil, err
		}
	}
	return h.cairnlineProjectWorkSeededView(ctx, snapshot)
}

func (h *Handler) cairnlineEmbeddedProjectWorkView(ctx context.Context, projectID string) (*cairnlineProjectWorkView, error) {
	_, service, store, err := h.openCairnlineEmbeddedService(ctx)
	if err != nil {
		return nil, err
	}
	project, err := service.GetProject(ctx, projectID)
	if err != nil {
		_ = store.Close()
		if errors.Is(err, cairnline.ErrNotFound) {
			return nil, errors.Join(projects.ErrNotFound, err)
		}
		return nil, err
	}
	return &cairnlineProjectWorkView{
		service: service,
		snapshot: cairnlinebridge.Snapshot{
			Project: projectFromCairnline(project, projects.Project{}),
		},
		close: store.Close,
	}, nil
}

func (h *Handler) cairnlineProjectReadSource() string {
	if h == nil {
		return "auto"
	}
	return h.config.ProjectsCairnlineReadSource()
}

func (h *Handler) requiresEmbeddedCairnlineProjectReads() bool {
	return h != nil &&
		h.projectCairnlineEmbeddedConnectorEnabled() &&
		h.cairnlineProjectReadSource() == "embedded"
}

func (h *Handler) prefersEmbeddedCairnlineProjectReads() bool {
	if !h.projectCairnlineEmbeddedConnectorEnabled() {
		return false
	}
	switch h.cairnlineProjectReadSource() {
	case "embedded":
		return true
	case "snapshot":
		return false
	default:
		return strings.TrimSpace(h.config.Server.DataDir) != ""
	}
}

func (h *Handler) cairnlineProjectWorkSeededView(ctx context.Context, snapshot cairnlinebridge.Snapshot) (*cairnlineProjectWorkView, error) {
	service := cairnline.NewMemoryService()
	if err := cairnlinebridge.Seed(ctx, service, snapshot); err != nil {
		return nil, err
	}
	return &cairnlineProjectWorkView{
		service:  service,
		snapshot: snapshot,
	}, nil
}

func projectWorkItemFromCairnline(item cairnline.WorkItem) projectwork.WorkItem {
	return projectwork.WorkItem{
		ID:              item.ID,
		ProjectID:       item.ProjectID,
		Title:           item.Title,
		Brief:           item.Brief,
		Status:          item.Status,
		Priority:        item.Priority,
		OwnerRoleID:     item.OwnerRoleID,
		RootID:          item.RootID,
		ReviewerRoleIDs: append([]string(nil), item.ReviewerRoleIDs...),
		CreatedAt:       item.CreatedAt,
		UpdatedAt:       item.UpdatedAt,
	}
}

func projectWorkItemsFromCairnlineWithNativeTimestamps(items []cairnline.WorkItem, native []projectwork.WorkItem) []projectwork.WorkItem {
	nativeByID := projectWorkItemsByID(native)
	out := make([]projectwork.WorkItem, 0, len(items))
	for _, item := range items {
		projected := projectWorkItemFromCairnline(item)
		if nativeItem, ok := nativeByID[item.ID]; ok {
			if !nativeItem.CreatedAt.IsZero() {
				projected.CreatedAt = nativeItem.CreatedAt
			}
			if !nativeItem.UpdatedAt.IsZero() {
				projected.UpdatedAt = nativeItem.UpdatedAt
			}
		}
		out = append(out, projected)
	}
	return out
}

func projectWorkItemsByID(items []projectwork.WorkItem) map[string]projectwork.WorkItem {
	out := make(map[string]projectwork.WorkItem, len(items))
	for _, item := range items {
		out[item.ID] = item
	}
	return out
}

func projectWorkAssignmentsByID(items []projectwork.Assignment) map[string]projectwork.Assignment {
	out := make(map[string]projectwork.Assignment, len(items))
	for _, item := range items {
		out[item.ID] = item
	}
	return out
}

func projectWorkRolesByID(items []projectwork.AgentRoleProfile) map[string]projectwork.AgentRoleProfile {
	out := make(map[string]projectwork.AgentRoleProfile, len(items))
	for _, item := range items {
		out[item.ID] = item
	}
	return out
}

func projectWorkRoleFromCairnline(item cairnline.Role, native projectwork.AgentRoleProfile) projectwork.AgentRoleProfile {
	return projectwork.AgentRoleProfile{
		ID:                  item.ID,
		ProjectID:           item.ProjectID,
		Name:                item.Name,
		Description:         item.Description,
		Instructions:        item.Instructions,
		DefaultDriverKind:   projectWorkAssignmentDriverFromCairnline(item.DefaultExecutionMode),
		DefaultProvider:     native.DefaultProvider,
		DefaultModel:        native.DefaultModel,
		DefaultAgentProfile: item.DefaultProfileID,
		SkillIDs:            append([]string(nil), item.DefaultSkillIDs...),
		BuiltIn:             native.BuiltIn,
		CreatedAt:           native.CreatedAt,
		UpdatedAt:           native.UpdatedAt,
	}
}

func projectWorkAssignmentFromCairnline(item cairnline.Assignment) projectwork.Assignment {
	return projectwork.Assignment{
		ID:         item.ID,
		ProjectID:  item.ProjectID,
		WorkItemID: item.WorkItemID,
		RoleID:     item.RoleID,
		RootID:     item.RootID,
		DriverKind: projectWorkAssignmentDriverFromCairnline(item.ExecutionMode),
		Status:     projectWorkAssignmentStatusFromCairnline(item.Status),
		ExecutionRef: projectwork.NormalizeAssignmentExecutionRef(projectwork.AssignmentExecutionRef{
			ContextSnapshotID: item.ContextSnapshotID,
		}),
		CreatedAt:   item.CreatedAt,
		UpdatedAt:   item.UpdatedAt,
		StartedAt:   item.StartedAt,
		CompletedAt: item.CompletedAt,
	}
}

func projectWorkAssignmentDriverFromCairnline(mode string) string {
	switch strings.TrimSpace(mode) {
	case cairnline.ExecutionExternalAdapter:
		return projectwork.AssignmentDriverExternalAgent
	default:
		return projectwork.AssignmentDriverHecateTask
	}
}

func projectWorkAssignmentStatusFromCairnline(status string) string {
	switch strings.TrimSpace(status) {
	case cairnline.AssignmentRunning, cairnline.AssignmentClaimed:
		return projectwork.AssignmentStatusRunning
	case cairnline.AssignmentReview:
		return projectwork.AssignmentStatusAwaitingApproval
	case cairnline.AssignmentCompleted:
		return projectwork.AssignmentStatusCompleted
	case cairnline.AssignmentFailed:
		return projectwork.AssignmentStatusFailed
	case cairnline.AssignmentCancelled:
		return projectwork.AssignmentStatusCancelled
	default:
		return projectwork.AssignmentStatusQueued
	}
}

func renderCairnlineProjectWorkItemReadiness(readiness cairnline.WorkItemCloseoutReadiness) ProjectWorkItemReadinessResponse {
	return ProjectWorkItemReadinessResponse{
		ProjectID:                    readiness.ProjectID,
		WorkItemID:                   readiness.WorkItemID,
		ReadBackend:                  "cairnline",
		Ready:                        readiness.Ready,
		Status:                       readiness.Status,
		Title:                        readiness.Title,
		Detail:                       readiness.Detail,
		Blockers:                     renderCairnlineProjectWorkItemReadinessBlockers(readiness.Blockers),
		Warnings:                     append([]string(nil), readiness.Warnings...),
		AssignmentCount:              readiness.AssignmentCount,
		CompletedAssignments:         readiness.CompletedAssignments,
		ReviewFollowUpCount:          readiness.ReviewFollowUpCount,
		ReviewFollowUpArtifactIDs:    append([]string(nil), readiness.ReviewFollowUpArtifactIDs...),
		ReviewFollowUps:              renderCairnlineProjectWorkItemReviewFollowUps(readiness.ReviewFollowUps),
		MissingEvidenceAssignmentIDs: append([]string(nil), readiness.MissingEvidenceAssignmentIDs...),
	}
}

func renderCairnlineProjectWorkItemReviewFollowUps(items []cairnline.ReviewFollowUpReadiness) []ProjectWorkItemReviewFollowUpResponse {
	if len(items) == 0 {
		return nil
	}
	out := make([]ProjectWorkItemReviewFollowUpResponse, 0, len(items))
	for _, item := range items {
		out = append(out, ProjectWorkItemReviewFollowUpResponse{
			ArtifactID:           item.ArtifactID,
			Title:                item.Title,
			Status:               item.Status,
			Blocker:              renderCairnlineProjectWorkItemReadinessBlocker(item.Blocker),
			ReviewedAssignmentID: item.ReviewedAssignmentID,
			ReviewVerdict:        item.ReviewVerdict,
			ReviewRisk:           item.ReviewRisk,
		})
	}
	return out
}

func renderCairnlineProjectWorkItemReadinessBlockers(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, renderCairnlineProjectWorkItemReadinessBlocker(value))
	}
	return out
}

func renderCairnlineProjectWorkItemReadinessBlocker(value string) string {
	// Cairnline's portable handoff state is "open"; Hecate's Projects API has
	// historically exposed the same unresolved state as "pending".
	switch value {
	case "handoff is open":
		return "handoff is pending"
	case "handoffs are open":
		return "handoffs are pending"
	}
	value = strings.ReplaceAll(value, "handoff is open", "handoff is pending")
	value = strings.ReplaceAll(value, "handoffs are open", "handoffs are pending")
	return strings.ReplaceAll(value, " has an open handoff", " has a pending handoff")
}
