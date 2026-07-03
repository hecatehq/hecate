package api

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/projectwork"
)

func (h *Handler) projectAssistantWorkStoreForApplication() projectwork.Store {
	if h == nil {
		return nil
	}
	if h.projectReadRoutesUseCairnlineReadModel() && h.requiresEmbeddedCairnlineProjectReads() {
		return projectAssistantCairnlineWorkReadStore{
			Store:   h.projectWork,
			handler: h,
		}
	}
	return h.projectWork
}

type projectAssistantCairnlineWorkReadStore struct {
	projectwork.Store
	handler *Handler
}

func (store projectAssistantCairnlineWorkReadStore) Backend() string {
	if store.usesCairnlineReads() {
		return "cairnline"
	}
	if store.Store != nil {
		return store.Store.Backend()
	}
	return ""
}

func (store projectAssistantCairnlineWorkReadStore) ListRoles(ctx context.Context, projectID string) ([]projectwork.AgentRoleProfile, error) {
	if !store.usesCairnlineReads() {
		return store.Store.ListRoles(ctx, projectID)
	}
	view, err := store.handler.cairnlineProjectWorkView(ctx, projectID)
	if err != nil {
		return nil, projectAssistantWorkReadError(err)
	}
	defer view.Close()
	roles, err := view.service.ListRoles(ctx, view.snapshot.Project.ID)
	if err != nil {
		return nil, projectAssistantWorkReadError(err)
	}
	executionProfiles, err := view.service.ListExecutionProfiles(ctx)
	if err != nil {
		return nil, projectAssistantWorkReadError(err)
	}
	nativeRoles := append([]projectwork.AgentRoleProfile(nil), view.snapshot.Roles...)
	nativeRoles = append(nativeRoles, projectwork.BuiltInRoleProfiles(view.snapshot.Project.ID)...)
	nativeByID := projectWorkRolesByID(nativeRoles)
	executionProfilesByID := cairnlineExecutionProfilesByID(executionProfiles)
	seen := make(map[string]struct{}, len(roles))
	out := make([]projectwork.AgentRoleProfile, 0, len(roles))
	for _, role := range roles {
		seen[role.ID] = struct{}{}
		out = append(out, projectWorkRoleFromCairnline(role, executionProfilesByID, nativeByID[role.ID]))
	}
	for _, role := range projectwork.BuiltInRoleProfiles(view.snapshot.Project.ID) {
		if _, ok := seen[role.ID]; ok {
			continue
		}
		out = append(out, role)
	}
	sortProjectAssistantRoles(out)
	return out, nil
}

func (store projectAssistantCairnlineWorkReadStore) ListWorkItems(ctx context.Context, projectID string, options ...projectwork.ListOptions) ([]projectwork.WorkItem, error) {
	if !store.usesCairnlineReads() {
		return store.Store.ListWorkItems(ctx, projectID, options...)
	}
	view, err := store.handler.cairnlineProjectWorkView(ctx, projectID)
	if err != nil {
		return nil, projectAssistantWorkReadError(err)
	}
	defer view.Close()
	items, err := view.service.ListWorkItems(ctx, view.snapshot.Project.ID)
	if err != nil {
		return nil, projectAssistantWorkReadError(err)
	}
	out := make([]projectwork.WorkItem, 0, len(items))
	for _, item := range items {
		out = append(out, projectWorkItemFromCairnline(item))
	}
	return projectAssistantFilterWorkItems(out, options...), nil
}

func (store projectAssistantCairnlineWorkReadStore) GetWorkItem(ctx context.Context, projectID, id string) (projectwork.WorkItem, bool, error) {
	if !store.usesCairnlineReads() {
		return store.Store.GetWorkItem(ctx, projectID, id)
	}
	view, err := store.handler.cairnlineProjectWorkView(ctx, projectID)
	if err != nil {
		if errors.Is(err, cairnline.ErrNotFound) {
			return projectwork.WorkItem{}, false, nil
		}
		return projectwork.WorkItem{}, false, projectAssistantWorkReadError(err)
	}
	defer view.Close()
	item, err := view.service.GetWorkItem(ctx, view.snapshot.Project.ID, strings.TrimSpace(id))
	if err != nil {
		if errors.Is(err, cairnline.ErrNotFound) {
			return projectwork.WorkItem{}, false, nil
		}
		return projectwork.WorkItem{}, false, projectAssistantWorkReadError(err)
	}
	return projectWorkItemFromCairnline(item), true, nil
}

func (store projectAssistantCairnlineWorkReadStore) ListAssignments(ctx context.Context, filter projectwork.AssignmentFilter, options ...projectwork.ListOptions) ([]projectwork.Assignment, error) {
	if !store.usesCairnlineReads() {
		return store.Store.ListAssignments(ctx, filter, options...)
	}
	view, err := store.handler.cairnlineProjectWorkView(ctx, filter.ProjectID)
	if err != nil {
		return nil, projectAssistantWorkReadError(err)
	}
	defer view.Close()
	items, err := view.service.ListAssignments(ctx, view.snapshot.Project.ID)
	if err != nil {
		return nil, projectAssistantWorkReadError(err)
	}
	assignments := projectWorkAssignmentsFromCairnline(items, view.snapshot.Assignments)
	projected, err := store.handler.applyRuntimeForCairnlineReadiness(ctx, assignments)
	if err != nil {
		return nil, projectAssistantWorkReadError(err)
	}
	return projectAssistantFilterAssignments(projected, filter, options...), nil
}

func (store projectAssistantCairnlineWorkReadStore) ListArtifacts(ctx context.Context, filter projectwork.ArtifactFilter) ([]projectwork.CollaborationArtifact, error) {
	if !store.usesCairnlineReads() {
		return store.Store.ListArtifacts(ctx, filter)
	}
	view, err := store.handler.cairnlineProjectWorkView(ctx, filter.ProjectID)
	if err != nil {
		return nil, projectAssistantWorkReadError(err)
	}
	defer view.Close()
	items, err := store.listCairnlineArtifacts(ctx, view, filter)
	if err != nil {
		return nil, projectAssistantWorkReadError(err)
	}
	return projectAssistantFilterArtifacts(items, filter), nil
}

func (store projectAssistantCairnlineWorkReadStore) ListHandoffs(ctx context.Context, filter projectwork.HandoffFilter) ([]projectwork.Handoff, error) {
	if !store.usesCairnlineReads() {
		return store.Store.ListHandoffs(ctx, filter)
	}
	view, err := store.handler.cairnlineProjectWorkView(ctx, filter.ProjectID)
	if err != nil {
		return nil, projectAssistantWorkReadError(err)
	}
	defer view.Close()
	items, err := cairnlineProjectHandoffs(ctx, view.service, view.snapshot.Project.ID, strings.TrimSpace(filter.WorkItemID), strings.TrimSpace(filter.Status))
	if err != nil {
		return nil, projectAssistantWorkReadError(err)
	}
	return items, nil
}

func (store projectAssistantCairnlineWorkReadStore) usesCairnlineReads() bool {
	return store.handler != nil &&
		store.handler.projectReadRoutesUseCairnlineReadModel() &&
		store.handler.requiresEmbeddedCairnlineProjectReads()
}

func (store projectAssistantCairnlineWorkReadStore) listCairnlineArtifacts(ctx context.Context, view *cairnlineProjectWorkView, filter projectwork.ArtifactFilter) ([]projectwork.CollaborationArtifact, error) {
	workItemID := strings.TrimSpace(filter.WorkItemID)
	if workItemID != "" {
		return cairnlineProjectWorkArtifacts(ctx, view.service, view.snapshot.Project.ID, workItemID)
	}
	workItems, err := view.service.ListWorkItems(ctx, view.snapshot.Project.ID)
	if err != nil {
		return nil, err
	}
	out := make([]projectwork.CollaborationArtifact, 0)
	for _, workItem := range workItems {
		items, err := cairnlineProjectWorkArtifacts(ctx, view.service, view.snapshot.Project.ID, workItem.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	sortProjectAssistantArtifacts(out)
	return out, nil
}

func projectAssistantWorkReadError(err error) error {
	if errors.Is(err, cairnline.ErrNotFound) {
		return projectwork.ErrNotFound
	}
	return err
}

func projectAssistantFilterWorkItems(items []projectwork.WorkItem, options ...projectwork.ListOptions) []projectwork.WorkItem {
	statuses, limit := projectAssistantListOptions(options)
	out := make([]projectwork.WorkItem, 0, len(items))
	for _, item := range items {
		if len(statuses) > 0 {
			if _, ok := statuses[item.Status]; !ok {
				continue
			}
		}
		out = append(out, item)
	}
	sortProjectAssistantWorkItems(out)
	return projectAssistantLimitWorkItems(out, limit)
}

func projectAssistantFilterAssignments(items []projectwork.Assignment, filter projectwork.AssignmentFilter, options ...projectwork.ListOptions) []projectwork.Assignment {
	workItemID := strings.TrimSpace(filter.WorkItemID)
	statuses, limit := projectAssistantListOptions(options)
	out := make([]projectwork.Assignment, 0, len(items))
	for _, item := range items {
		if workItemID != "" && strings.TrimSpace(item.WorkItemID) != workItemID {
			continue
		}
		if len(statuses) > 0 {
			if _, ok := statuses[item.Status]; !ok {
				continue
			}
		}
		out = append(out, item)
	}
	sortProjectAssistantAssignments(out)
	return projectAssistantLimitAssignments(out, limit)
}

func projectAssistantFilterArtifacts(items []projectwork.CollaborationArtifact, filter projectwork.ArtifactFilter) []projectwork.CollaborationArtifact {
	workItemID := strings.TrimSpace(filter.WorkItemID)
	assignmentID := strings.TrimSpace(filter.AssignmentID)
	out := make([]projectwork.CollaborationArtifact, 0, len(items))
	for _, item := range items {
		if workItemID != "" && strings.TrimSpace(item.WorkItemID) != workItemID {
			continue
		}
		if assignmentID != "" && strings.TrimSpace(item.AssignmentID) != assignmentID {
			continue
		}
		out = append(out, item)
	}
	sortProjectAssistantArtifacts(out)
	return out
}

func projectAssistantListOptions(options []projectwork.ListOptions) (map[string]struct{}, int) {
	statuses := make(map[string]struct{})
	limit := 0
	for _, option := range options {
		if option.Limit > 0 {
			limit = option.Limit
		}
		for _, status := range option.Statuses {
			status = strings.TrimSpace(status)
			if status != "" {
				statuses[status] = struct{}{}
			}
		}
	}
	if len(statuses) == 0 {
		statuses = nil
	}
	return statuses, limit
}

func projectAssistantLimitWorkItems(items []projectwork.WorkItem, limit int) []projectwork.WorkItem {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func projectAssistantLimitAssignments(items []projectwork.Assignment, limit int) []projectwork.Assignment {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func sortProjectAssistantRoles(items []projectwork.AgentRoleProfile) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].BuiltIn != items[j].BuiltIn {
			return items[i].BuiltIn
		}
		if items[i].Name != items[j].Name {
			return items[i].Name < items[j].Name
		}
		return items[i].ID < items[j].ID
	})
}

func sortProjectAssistantWorkItems(items []projectwork.WorkItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if !items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		}
		return items[i].ID < items[j].ID
	})
}

func sortProjectAssistantAssignments(items []projectwork.Assignment) {
	sort.SliceStable(items, func(i, j int) bool {
		if !items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].CreatedAt.Before(items[j].CreatedAt)
		}
		return items[i].ID < items[j].ID
	})
}

func sortProjectAssistantArtifacts(items []projectwork.CollaborationArtifact) {
	sort.SliceStable(items, func(i, j int) bool {
		return projectWorkArtifactProjectionLess(items[i], items[j])
	})
}
