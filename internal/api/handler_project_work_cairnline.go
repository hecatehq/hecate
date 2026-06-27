package api

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/cairnlinebridge"
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
	service, snapshot, err := h.cairnlineProjectWorkService(ctx, projectID)
	if err != nil {
		return nil, err
	}
	items, err := cairnlineProjectWorkArtifacts(ctx, service, snapshot.Project.ID, workItemID)
	if errors.Is(err, cairnline.ErrNotFound) {
		return nil, projectwork.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	data := make([]ProjectWorkArtifactResponse, 0, len(items))
	for _, item := range items {
		projected := renderProjectWorkArtifact(item)
		projected.ReadBackend = "cairnline"
		data = append(data, projected)
	}
	return data, nil
}

func (h *Handler) renderProjectHandoffs(ctx context.Context, filter projectwork.HandoffFilter) ([]ProjectHandoffResponse, error) {
	if h.projectReadRoutesUseCairnlineReadModel() {
		return h.renderCairnlineProjectHandoffs(ctx, filter)
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

func (h *Handler) renderCairnlineProjectHandoffs(ctx context.Context, filter projectwork.HandoffFilter) ([]ProjectHandoffResponse, error) {
	service, snapshot, err := h.cairnlineProjectWorkService(ctx, filter.ProjectID)
	if err != nil {
		return nil, err
	}
	items, err := cairnlineProjectHandoffs(ctx, service, snapshot.Project.ID, strings.TrimSpace(filter.WorkItemID), strings.TrimSpace(filter.Status))
	if errors.Is(err, cairnline.ErrNotFound) {
		return nil, projectwork.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	data := make([]ProjectHandoffResponse, 0, len(items))
	for _, item := range items {
		projected := renderProjectHandoff(item)
		projected.ReadBackend = "cairnline"
		data = append(data, projected)
	}
	return data, nil
}

func (h *Handler) renderProjectWorkRoles(ctx context.Context, projectID string) ([]ProjectWorkRoleResponse, error) {
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

func (h *Handler) renderCairnlineProjectWorkRoles(ctx context.Context, projectID string) ([]ProjectWorkRoleResponse, error) {
	service, snapshot, err := h.cairnlineProjectWorkService(ctx, projectID)
	if err != nil {
		return nil, err
	}
	roles, err := service.ListRoles(ctx, snapshot.Project.ID)
	if err != nil {
		return nil, err
	}
	executionProfiles, err := service.ListExecutionProfiles(ctx)
	if err != nil {
		return nil, err
	}
	nativeByID := projectWorkRolesByID(snapshot.Roles)
	executionProfilesByID := cairnlineExecutionProfilesByID(executionProfiles)
	data := make([]ProjectWorkRoleResponse, 0, len(roles))
	for _, role := range roles {
		projected := renderProjectWorkRole(projectWorkRoleFromCairnline(role, executionProfilesByID, nativeByID[role.ID]))
		projected.ReadBackend = "cairnline"
		data = append(data, projected)
	}
	return data, nil
}

func (h *Handler) renderProjectWorkItems(ctx context.Context, projectID string) ([]ProjectWorkItemResponse, error) {
	if h.projectReadRoutesUseCairnlineReadModel() {
		return h.renderCairnlineProjectWorkItems(ctx, projectID)
	}
	items, err := h.projectWork.ListWorkItems(ctx, projectID)
	if err != nil {
		return nil, err
	}
	assignments, err := h.projectWork.ListAssignments(ctx, projectwork.AssignmentFilter{ProjectID: projectID})
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

func (h *Handler) renderCairnlineProjectWorkItems(ctx context.Context, projectID string) ([]ProjectWorkItemResponse, error) {
	service, snapshot, err := h.cairnlineProjectWorkService(ctx, projectID)
	if err != nil {
		return nil, err
	}
	items, err := service.ListWorkItems(ctx, snapshot.Project.ID)
	if err != nil {
		return nil, err
	}
	assignmentsByWorkItem := groupProjectWorkAssignmentsByWorkItem(snapshot.Assignments)
	data := make([]ProjectWorkItemResponse, 0, len(items))
	for _, item := range items {
		projected, err := h.renderProjectedProjectWorkItemWithAssignments(ctx, projectWorkItemFromCairnline(item), assignmentsByWorkItem[item.ID])
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
	service, snapshot, err := h.cairnlineProjectWorkService(ctx, projectID)
	if err != nil {
		return ProjectWorkItemResponse{}, err
	}
	items, err := service.ListWorkItems(ctx, snapshot.Project.ID)
	if err != nil {
		return ProjectWorkItemResponse{}, err
	}
	assignmentsByWorkItem := groupProjectWorkAssignmentsByWorkItem(snapshot.Assignments)
	for _, item := range items {
		if item.ID != workItemID {
			continue
		}
		projected, err := h.renderProjectedProjectWorkItemWithAssignments(ctx, projectWorkItemFromCairnline(item), assignmentsByWorkItem[item.ID])
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
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
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
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (h *Handler) renderCairnlineProjectWorkAssignments(ctx context.Context, projectID, workItemID string) ([]ProjectWorkAssignmentResponse, error) {
	service, snapshot, err := h.cairnlineProjectWorkService(ctx, projectID)
	if err != nil {
		return nil, err
	}
	items, err := service.ListAssignments(ctx, snapshot.Project.ID)
	if err != nil {
		return nil, err
	}
	nativeByID := projectWorkAssignmentsByID(snapshot.Assignments)
	data := make([]ProjectWorkAssignmentResponse, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.WorkItemID) != workItemID {
			continue
		}
		assignment := projectWorkAssignmentFromCairnline(item)
		if native, ok := nativeByID[item.ID]; ok {
			assignment = native
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
	service, snapshot, err := h.cairnlineProjectWorkService(ctx, projectID)
	if err != nil {
		return ProjectWorkItemReadinessResponse{}, err
	}
	readiness, err := service.WorkItemCloseoutReadiness(ctx, snapshot.Project.ID, workItemID)
	if errors.Is(err, cairnline.ErrNotFound) {
		return ProjectWorkItemReadinessResponse{}, projectwork.ErrNotFound
	}
	if err != nil {
		return ProjectWorkItemReadinessResponse{}, err
	}
	return renderCairnlineProjectWorkItemReadiness(readiness), nil
}

func (h *Handler) cairnlineProjectWorkService(ctx context.Context, projectID string) (*cairnline.Service, cairnlinebridge.Snapshot, error) {
	service := cairnline.NewMemoryService()
	snapshot, err := cairnlinebridge.SeedProjectFromStores(ctx, service, h.cairnlineSnapshotSources(), projectID)
	if err != nil {
		return nil, cairnlinebridge.Snapshot{}, err
	}
	return service, snapshot, nil
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

func cairnlineExecutionProfilesByID(items []cairnline.ExecutionProfile) map[string]cairnline.ExecutionProfile {
	out := make(map[string]cairnline.ExecutionProfile, len(items))
	for _, item := range items {
		out[item.ID] = item
	}
	return out
}

func projectWorkRoleFromCairnline(item cairnline.Role, executionProfiles map[string]cairnline.ExecutionProfile, native projectwork.AgentRoleProfile) projectwork.AgentRoleProfile {
	executionProfile := executionProfiles[item.DefaultExecutionProfileID]
	return projectwork.AgentRoleProfile{
		ID:                  item.ID,
		ProjectID:           item.ProjectID,
		Name:                item.Name,
		Description:         item.Description,
		Instructions:        item.Instructions,
		DefaultDriverKind:   projectWorkAssignmentDriverFromCairnline(item.DefaultExecutionMode),
		DefaultProvider:     executionProfile.ProviderHint,
		DefaultModel:        executionProfile.ModelHint,
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
		CreatedAt: item.CreatedAt,
		UpdatedAt: item.UpdatedAt,
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
