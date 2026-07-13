package api

import (
	"context"
	"errors"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/cairnlinebridge"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projectassistant"
	"github.com/hecatehq/hecate/internal/projectassistantapp"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
)

func (h *Handler) cairnlineProjectAssistantContext(ctx context.Context, input projectassistant.ContextInput) (projectassistant.DraftContext, error) {
	view, err := h.cairnlineProjectWorkView(ctx, input.ProjectID)
	if errors.Is(err, projects.ErrNotFound) {
		return projectassistant.DraftContext{}, projectassistant.ErrNotFound
	}
	if err != nil {
		return projectassistant.DraftContext{}, err
	}
	defer view.Close()
	seed, err := h.cairnlineProjectAssistantContextSeed(ctx, view.service, view.snapshot)
	if err != nil {
		return projectassistant.DraftContext{}, err
	}
	draftContext, err := projectassistant.NewService(projectassistant.Stores{
		Projects:         seed.projects,
		Work:             seed.work,
		ProjectSkills:    seed.skills,
		Memory:           seed.memory,
		MemoryCandidates: seed.memory,
	}, nil).Context(ctx, input)
	if err != nil {
		return projectassistant.DraftContext{}, err
	}
	draftContext.ReadBackend = "cairnline"
	return draftContext, nil
}

func (h *Handler) cairnlineProjectAssistantDraft(ctx context.Context, command projectassistantapp.DraftCommand) (projectassistant.Proposal, error) {
	view, err := h.cairnlineProjectWorkView(ctx, command.ProjectID)
	if errors.Is(err, projects.ErrNotFound) {
		return projectassistant.Proposal{}, projectassistant.ErrNotFound
	}
	if err != nil {
		return projectassistant.Proposal{}, err
	}
	defer view.Close()
	seed, err := h.cairnlineProjectAssistantContextSeed(ctx, view.service, view.snapshot)
	if err != nil {
		return projectassistant.Proposal{}, err
	}
	return projectassistant.NewService(projectassistant.Stores{
		Projects:         seed.projects,
		Chats:            h.agentChat,
		Work:             seed.work,
		ProjectSkills:    seed.skills,
		Memory:           seed.memory,
		MemoryCandidates: seed.memory,
		Proposals:        h.projectAssistantProposalStoreForApplication(),
		LLM:              gatewayAgentLLMClient{service: h.service},
	}, newOpaqueTaskResourceID).Draft(ctx, projectassistant.DraftInput{
		ProjectID:        command.ProjectID,
		WorkItemID:       command.WorkItemID,
		Request:          command.Request,
		RoleID:           command.RoleID,
		DriverKind:       command.DriverKind,
		DraftMode:        command.DraftMode,
		ReviewArtifactID: command.ReviewArtifactID,
		Provider:         command.Provider,
		Model:            command.Model,
		RequestID:        command.RequestID,
		TraceID:          command.TraceID,
	})
}

type cairnlineProjectAssistantContextSeed struct {
	projects *projects.MemoryStore
	work     *projectwork.MemoryStore
	skills   *projectskills.MemoryStore
	memory   *memory.MemoryStore
}

func (h *Handler) cairnlineProjectAssistantContextSeed(ctx context.Context, service *cairnline.Service, snapshot cairnlinebridge.Snapshot) (cairnlineProjectAssistantContextSeed, error) {
	return h.cairnlineProjectAssistantContextSeedFromService(ctx, service, snapshot.Project.ID, snapshot)
}

func (h *Handler) cairnlineProjectAssistantContextSeedFromService(ctx context.Context, service *cairnline.Service, projectID string, snapshot cairnlinebridge.Snapshot) (cairnlineProjectAssistantContextSeed, error) {
	var seed cairnlineProjectAssistantContextSeed
	projectID = strings.TrimSpace(firstNonEmpty(snapshot.Project.ID, projectID))
	project, err := service.GetProject(ctx, projectID)
	if errors.Is(err, cairnline.ErrNotFound) {
		return seed, projectassistant.ErrNotFound
	}
	if err != nil {
		return seed, err
	}
	seed.projects = projects.NewMemoryStore()
	if _, err := seed.projects.Create(ctx, projectFromCairnline(project, snapshot.Project)); err != nil {
		return seed, err
	}

	seed.work = projectwork.NewMemoryStore()
	if err := h.seedCairnlineProjectAssistantWork(ctx, seed.work, service, projectID, snapshot); err != nil {
		return seed, err
	}

	seed.skills = projectskills.NewMemoryStore()
	if err := seedCairnlineProjectAssistantSkills(ctx, seed.skills, service, projectID, snapshot); err != nil {
		return seed, err
	}

	seed.memory = memory.NewMemoryStore()
	if err := seedCairnlineProjectAssistantMemory(ctx, seed.memory, service, projectID); err != nil {
		return seed, err
	}
	return seed, nil
}

func projectAssistantWorkItemStatusFromCairnline(status string) string {
	switch strings.TrimSpace(status) {
	case "running":
		return projectwork.WorkItemStatusRunning
	case "review":
		return projectwork.WorkItemStatusReview
	case "blocked", "failed":
		return projectwork.WorkItemStatusBlocked
	case "completed", "done":
		return projectwork.WorkItemStatusDone
	case "cancelled", "canceled":
		return projectwork.WorkItemStatusCancelled
	case "ready":
		return projectwork.WorkItemStatusReady
	default:
		return projectwork.WorkItemStatusBacklog
	}
}

func (h *Handler) seedCairnlineProjectAssistantWork(ctx context.Context, store *projectwork.MemoryStore, service *cairnline.Service, projectID string, snapshot cairnlinebridge.Snapshot) error {
	projectID = strings.TrimSpace(firstNonEmpty(snapshot.Project.ID, projectID))
	roles, err := service.ListRoles(ctx, projectID)
	if err != nil {
		return err
	}
	nativeRolesByID := projectWorkRolesByID(snapshot.Roles)
	for _, role := range roles {
		if projectwork.IsBuiltInRoleID(role.ID) {
			continue
		}
		if _, err := store.CreateRole(ctx, projectWorkRoleFromCairnline(role, nativeRolesByID[role.ID])); err != nil {
			return err
		}
	}

	workItems, err := service.ListWorkItems(ctx, projectID)
	if err != nil {
		return err
	}
	nativeWorkItemsByID := projectWorkItemsByID(snapshot.WorkItems)
	for _, item := range workItems {
		workItem := projectWorkItemFromCairnline(item)
		if native, ok := nativeWorkItemsByID[item.ID]; ok {
			workItem = native
		}
		if _, err := store.CreateWorkItem(ctx, workItem); err != nil {
			return err
		}
	}

	assignments, err := h.cairnlineProjectAssignments(ctx, service, snapshot)
	if err != nil {
		return err
	}
	for _, assignment := range assignments {
		if _, err := store.CreateAssignment(ctx, assignment); err != nil {
			return err
		}
	}
	for _, item := range workItems {
		artifacts, err := cairnlineProjectWorkArtifacts(ctx, service, projectID, item.ID)
		if err != nil {
			return err
		}
		for _, artifact := range artifacts {
			if _, err := store.CreateArtifact(ctx, artifact); err != nil {
				return err
			}
		}
		handoffs, err := cairnlineProjectHandoffs(ctx, service, projectID, item.ID, "")
		if err != nil {
			return err
		}
		for _, handoff := range handoffs {
			if _, err := store.CreateHandoff(ctx, handoff); err != nil {
				return err
			}
		}
	}
	return nil
}

func seedCairnlineProjectAssistantSkills(ctx context.Context, store *projectskills.MemoryStore, service *cairnline.Service, projectID string, snapshot cairnlinebridge.Snapshot) error {
	projectID = strings.TrimSpace(firstNonEmpty(snapshot.Project.ID, projectID))
	items, err := service.ListProjectSkills(ctx, projectID)
	if err != nil {
		return err
	}
	skills := make([]projectskills.Skill, 0, len(items))
	nativeByID := projectSkillsByID(snapshot.Skills)
	for _, item := range items {
		skills = append(skills, projectSkillFromCairnline(item, nativeByID[item.ID]))
	}
	if _, err := store.UpsertDiscovered(ctx, projectID, skills); err != nil {
		return err
	}
	return nil
}

func seedCairnlineProjectAssistantMemory(ctx context.Context, store *memory.MemoryStore, service *cairnline.Service, projectID string) error {
	entries, err := service.ListMemoryEntries(ctx, projectID, true)
	if err != nil {
		return err
	}
	for _, item := range entries {
		if _, err := store.Create(ctx, projectMemoryFromCairnline(item)); err != nil {
			return err
		}
	}
	candidates, err := service.ListMemoryCandidates(ctx, cairnline.MemoryCandidateFilter{
		ProjectID:       projectID,
		IncludeResolved: true,
	})
	if err != nil {
		return err
	}
	for _, item := range candidates {
		if _, err := store.CreateCandidate(ctx, projectMemoryCandidateFromCairnline(item)); err != nil {
			return err
		}
	}
	return nil
}
