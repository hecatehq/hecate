package api

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/cairnlinebridge"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projectassistant"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
)

func (h *Handler) mirrorProjectIdentityToCairnline(ctx context.Context, operation string, project projects.Project) {
	if err := h.writeProjectIdentityToCairnline(ctx, project); err != nil {
		h.logCairnlineMirrorError(ctx, operation, project.ID, err)
	}
}

func (h *Handler) mirrorProjectDefaultsToCairnline(ctx context.Context, operation string, project projects.Project) {
	if err := h.writeProjectDefaultsToCairnline(ctx, project); err != nil {
		h.logCairnlineMirrorError(ctx, operation, project.ID, err)
	}
}

func (h *Handler) mirrorProjectDeleteToCairnline(ctx context.Context, operation string, project projects.Project) {
	if err := h.deleteProjectIdentityFromCairnline(ctx, project); err != nil {
		h.logCairnlineMirrorError(ctx, operation, project.ID, err)
	}
}

func (h *Handler) mirrorProjectRootToCairnline(ctx context.Context, operation string, project projects.Project, root projects.Root) {
	if err := h.writeProjectRootToCairnline(ctx, project, root); err != nil {
		h.logCairnlineMirrorError(ctx, operation, project.ID, err)
	}
}

func (h *Handler) mirrorProjectRootDeleteToCairnline(ctx context.Context, operation, projectID, rootID string) {
	if err := h.deleteProjectRootFromCairnline(ctx, projectID, rootID); err != nil {
		h.logCairnlineMirrorError(ctx, operation, projectID, err)
	}
}

func (h *Handler) mirrorProjectContextSourceToCairnline(ctx context.Context, operation string, project projects.Project, source projects.ContextSource) {
	if err := h.writeProjectContextSourceToCairnline(ctx, project, source); err != nil {
		h.logCairnlineMirrorError(ctx, operation, project.ID, err)
	}
}

func (h *Handler) mirrorProjectContextSourcesToCairnline(ctx context.Context, operation string, project projects.Project, sources []projects.ContextSource) {
	for _, source := range sources {
		h.mirrorProjectContextSourceToCairnline(ctx, operation, project, source)
	}
}

func (h *Handler) mirrorProjectContextSourceDeleteToCairnline(ctx context.Context, operation, projectID, sourceID string) {
	if err := h.deleteProjectContextSourceFromCairnline(ctx, projectID, sourceID); err != nil {
		h.logCairnlineMirrorError(ctx, operation, projectID, err)
	}
}

func (h *Handler) mirrorProjectSkillsToCairnline(ctx context.Context, operation string, project projects.Project, skills []projectskills.Skill) {
	if err := h.writeProjectSkillsToCairnline(ctx, project, skills); err != nil {
		h.logCairnlineMirrorError(ctx, operation, project.ID, err)
	}
}

func (h *Handler) mirrorProjectRoleToCairnline(ctx context.Context, operation string, project projects.Project, role projectwork.AgentRoleProfile) {
	if err := h.writeProjectRoleToCairnline(ctx, project, role); err != nil {
		h.logCairnlineMirrorError(ctx, operation, project.ID, err)
	}
}

func (h *Handler) mirrorProjectRoleByIDToCairnline(ctx context.Context, operation, projectID string, role projectwork.AgentRoleProfile) {
	project, ok := h.projectForCairnlineMirror(ctx, operation, projectID)
	if !ok {
		return
	}
	h.mirrorProjectRoleToCairnline(ctx, operation, project, role)
}

func (h *Handler) mirrorProjectRoleDeleteToCairnline(ctx context.Context, operation string, role projectwork.AgentRoleProfile) {
	if err := h.deleteProjectRoleFromCairnline(ctx, role); err != nil {
		h.logCairnlineMirrorError(ctx, operation, role.ProjectID, err)
	}
}

func (h *Handler) mirrorProjectWorkItemToCairnline(ctx context.Context, operation string, project projects.Project, item projectwork.WorkItem) {
	if err := h.writeProjectWorkItemToCairnline(ctx, project, item); err != nil {
		h.logCairnlineMirrorError(ctx, operation, project.ID, err)
	}
}

func (h *Handler) mirrorProjectWorkItemByIDToCairnline(ctx context.Context, operation, projectID string, item projectwork.WorkItem) {
	project, ok := h.projectForCairnlineMirror(ctx, operation, projectID)
	if !ok {
		return
	}
	h.mirrorProjectWorkItemToCairnline(ctx, operation, project, item)
}

func (h *Handler) mirrorProjectWorkItemDeleteToCairnline(ctx context.Context, operation, projectID, workItemID string) {
	if err := h.deleteProjectWorkItemFromCairnline(ctx, projectID, workItemID); err != nil {
		h.logCairnlineMirrorError(ctx, operation, projectID, err)
	}
}

func (h *Handler) mirrorProjectAssignmentToCairnline(ctx context.Context, operation string, assignment projectwork.Assignment) {
	if err := h.writeProjectAssignmentToCairnline(ctx, assignment); err != nil {
		h.logCairnlineMirrorError(ctx, operation, assignment.ProjectID, err)
	}
}

func (h *Handler) mirrorProjectAssignmentDeleteToCairnline(ctx context.Context, operation, projectID, assignmentID string) {
	if err := h.deleteProjectAssignmentFromCairnline(ctx, projectID, assignmentID); err != nil {
		h.logCairnlineMirrorError(ctx, operation, projectID, err)
	}
}

func (h *Handler) mirrorProjectArtifactToCairnline(ctx context.Context, operation string, artifact projectwork.CollaborationArtifact) {
	if err := h.writeProjectArtifactToCairnline(ctx, artifact); err != nil {
		h.logCairnlineMirrorError(ctx, operation, artifact.ProjectID, err)
	}
}

func (h *Handler) mirrorProjectHandoffToCairnline(ctx context.Context, operation string, handoff projectwork.Handoff) {
	if err := h.writeProjectHandoffToCairnline(ctx, handoff); err != nil {
		h.logCairnlineMirrorError(ctx, operation, handoff.ProjectID, err)
	}
}

func (h *Handler) mirrorProjectHandoffDeleteToCairnline(ctx context.Context, operation, projectID, workItemID, handoffID string) {
	if err := h.deleteProjectHandoffFromCairnline(ctx, projectID, workItemID, handoffID); err != nil {
		h.logCairnlineMirrorError(ctx, operation, projectID, err)
	}
}

func (h *Handler) mirrorProjectMemoryEntryToCairnline(ctx context.Context, operation string, entry memory.Entry) {
	if err := h.writeProjectMemoryEntryToCairnline(ctx, entry); err != nil {
		h.logCairnlineMirrorError(ctx, operation, entry.ProjectID, err)
	}
}

func (h *Handler) mirrorProjectMemoryEntryDeleteToCairnline(ctx context.Context, operation, projectID, memoryID string) {
	if err := h.deleteProjectMemoryEntryFromCairnline(ctx, projectID, memoryID); err != nil {
		h.logCairnlineMirrorError(ctx, operation, projectID, err)
	}
}

func (h *Handler) mirrorProjectMemoryCandidateToCairnline(ctx context.Context, operation string, candidate memory.Candidate) {
	if err := h.writeProjectMemoryCandidateToCairnline(ctx, candidate); err != nil {
		h.logCairnlineMirrorError(ctx, operation, candidate.ProjectID, err)
	}
}

func (h *Handler) mirrorAgentProfileToCairnline(ctx context.Context, operation string, profile agentprofiles.Profile) {
	if err := h.writeAgentProfileToCairnline(ctx, profile); err != nil {
		h.logCairnlineAgentProfileMirrorError(ctx, operation, profile.ID, err)
	}
}

func (h *Handler) mirrorAgentProfileDeleteToCairnline(ctx context.Context, operation, profileID string) {
	if err := h.deleteAgentProfileFromCairnline(ctx, profileID); err != nil {
		h.logCairnlineAgentProfileMirrorError(ctx, operation, profileID, err)
	}
}

func (h *Handler) mirrorProjectAssistantProposalByIDToCairnline(ctx context.Context, operation, proposalID string) {
	record, ok, err := h.loadProjectAssistantProposalForCairnlineMirror(ctx, proposalID)
	if err != nil {
		h.logCairnlineMirrorError(ctx, operation, "", err)
		return
	}
	if !ok {
		return
	}
	h.mirrorProjectAssistantProposalRecordToCairnline(ctx, operation, record)
}

func (h *Handler) mirrorProjectAssistantProposalRecordToCairnline(ctx context.Context, operation string, record projectassistant.ProposalRecord) {
	if err := h.writeProjectAssistantProposalRecordToCairnline(ctx, record); err != nil {
		h.logCairnlineMirrorError(ctx, operation, record.ProjectID, err)
	}
}

func (h *Handler) mirrorProjectAssistantApplyResultToCairnline(ctx context.Context, operation string, result projectassistant.ApplyResult) {
	if h == nil || h.config.ProjectsCoordinationBackend() != "cairnline" {
		return
	}
	for _, action := range result.Actions {
		if err := h.writeProjectAssistantActionResultToCairnline(ctx, action); err != nil {
			h.logCairnlineMirrorError(ctx, operation, projectAssistantActionResultProjectID(action), err)
		}
	}
}

func (h *Handler) projectForCairnlineMirror(ctx context.Context, operation, projectID string) (projects.Project, bool) {
	if h == nil || h.config.ProjectsCoordinationBackend() != "cairnline" {
		return projects.Project{}, false
	}
	project, ok, err := h.projects.Get(ctx, projectID)
	if err != nil {
		h.logCairnlineMirrorError(ctx, operation, projectID, err)
		return projects.Project{}, false
	}
	if !ok {
		h.logCairnlineMirrorError(ctx, operation, projectID, errors.Join(cairnline.ErrNotFound, errors.New("project not found for Cairnline mirror")))
		return projects.Project{}, false
	}
	return project, true
}

func (h *Handler) projectWorkRoleForCairnlineMirror(ctx context.Context, operation, projectID, roleID string) (projectwork.AgentRoleProfile, bool) {
	if h == nil || h.config.ProjectsCoordinationBackend() != "cairnline" {
		return projectwork.AgentRoleProfile{}, false
	}
	role, ok, err := h.loadProjectWorkRoleForCairnlineMirror(ctx, projectID, roleID)
	if err != nil {
		h.logCairnlineMirrorError(ctx, operation, projectID, err)
		return projectwork.AgentRoleProfile{}, false
	}
	return role, ok
}

func (h *Handler) loadProjectWorkRoleForCairnlineMirror(ctx context.Context, projectID, roleID string) (projectwork.AgentRoleProfile, bool, error) {
	if h == nil || h.projectWork == nil {
		return projectwork.AgentRoleProfile{}, false, errors.New("project work store is not configured")
	}
	roles, err := h.projectWork.ListRoles(ctx, projectID)
	if err != nil {
		return projectwork.AgentRoleProfile{}, false, err
	}
	roleID = strings.TrimSpace(roleID)
	for _, role := range roles {
		if role.ID == roleID {
			return role, true, nil
		}
	}
	return projectwork.AgentRoleProfile{}, false, nil
}

func (h *Handler) loadProjectWorkAssignmentForCairnlineMirror(ctx context.Context, projectID, assignmentID string) (projectwork.Assignment, bool, error) {
	assignmentID = strings.TrimSpace(assignmentID)
	if assignmentID == "" {
		return projectwork.Assignment{}, false, nil
	}
	if h == nil || h.projectWork == nil {
		return projectwork.Assignment{}, false, errors.New("project work store is not configured")
	}
	assignments, err := h.projectWork.ListAssignments(ctx, projectwork.AssignmentFilter{ProjectID: projectID})
	if err != nil {
		return projectwork.Assignment{}, false, err
	}
	for _, assignment := range assignments {
		if assignment.ID == assignmentID {
			return assignment, true, nil
		}
	}
	return projectwork.Assignment{}, false, nil
}

func (h *Handler) loadProjectWorkItemForCairnlineMirror(ctx context.Context, projectID, workItemID string) (projectwork.WorkItem, bool, error) {
	workItemID = strings.TrimSpace(workItemID)
	if workItemID == "" {
		return projectwork.WorkItem{}, false, nil
	}
	if h == nil || h.projectWork == nil {
		return projectwork.WorkItem{}, false, errors.New("project work store is not configured")
	}
	return h.projectWork.GetWorkItem(ctx, projectID, workItemID)
}

func (h *Handler) loadProjectWorkHandoffForCairnlineMirror(ctx context.Context, projectID, handoffID string) (projectwork.Handoff, bool, error) {
	handoffID = strings.TrimSpace(handoffID)
	if handoffID == "" {
		return projectwork.Handoff{}, false, nil
	}
	if h == nil || h.projectWork == nil {
		return projectwork.Handoff{}, false, errors.New("project work store is not configured")
	}
	handoffs, err := h.projectWork.ListHandoffs(ctx, projectwork.HandoffFilter{ProjectID: projectID})
	if err != nil {
		return projectwork.Handoff{}, false, err
	}
	for _, handoff := range handoffs {
		if handoff.ID == handoffID {
			return handoff, true, nil
		}
	}
	return projectwork.Handoff{}, false, nil
}

func (h *Handler) loadProjectMemoryCandidateForCairnlineMirror(ctx context.Context, projectID, candidateID string) (memory.Candidate, bool, error) {
	candidateID = strings.TrimSpace(candidateID)
	if candidateID == "" {
		return memory.Candidate{}, false, nil
	}
	if h == nil || h.memoryCandidates == nil {
		return memory.Candidate{}, false, errors.New("project memory candidate store is not configured")
	}
	return h.memoryCandidates.GetCandidate(ctx, projectID, candidateID)
}

func (h *Handler) loadProjectAssistantProposalForCairnlineMirror(ctx context.Context, proposalID string) (projectassistant.ProposalRecord, bool, error) {
	proposalID = strings.TrimSpace(proposalID)
	if proposalID == "" {
		return projectassistant.ProposalRecord{}, false, nil
	}
	if h == nil || h.projectAssistantProposals == nil {
		return projectassistant.ProposalRecord{}, false, errors.New("project assistant proposal store is not configured")
	}
	return h.projectAssistantProposals.GetProposal(ctx, proposalID)
}

func (h *Handler) writeProjectAssistantActionResultToCairnline(ctx context.Context, result projectassistant.ActionResult) error {
	projectID := projectAssistantActionResultProjectID(result)
	switch strings.TrimSpace(result.Kind) {
	case projectassistant.ActionCreateProject,
		projectassistant.ActionUpdateProject,
		projectassistant.ActionAttachProjectRoot,
		projectassistant.ActionRemoveProjectRoot,
		projectassistant.ActionSetProjectDefaults:
		project, ok := h.projectForCairnlineMirror(ctx, "project_assistant_apply_result", projectID)
		if !ok {
			return nil
		}
		return h.writeProjectIdentityToCairnline(ctx, project)
	case projectassistant.ActionCreateRole:
		project, ok := h.projectForCairnlineMirror(ctx, "project_assistant_apply_result", projectID)
		if !ok {
			return nil
		}
		role, ok, err := h.loadProjectWorkRoleForCairnlineMirror(ctx, projectID, projectAssistantActionResultValue(result, "role_id"))
		if err != nil || !ok {
			return err
		}
		return h.writeProjectRoleToCairnline(ctx, project, role)
	case projectassistant.ActionCreateWorkItem, projectassistant.ActionUpdateWorkItem:
		item, ok, err := h.loadProjectWorkItemForCairnlineMirror(ctx, projectID, projectAssistantActionResultValue(result, "work_item_id"))
		if err != nil || !ok {
			return err
		}
		project, ok := h.projectForCairnlineMirror(ctx, "project_assistant_apply_result", projectID)
		if !ok {
			return nil
		}
		return h.writeProjectWorkItemToCairnline(ctx, project, item)
	case projectassistant.ActionCreateAssignment:
		assignment, ok, err := h.loadProjectWorkAssignmentForCairnlineMirror(ctx, projectID, projectAssistantActionResultValue(result, "assignment_id"))
		if err != nil || !ok {
			return err
		}
		return h.writeProjectAssignmentToCairnline(ctx, assignment)
	case projectassistant.ActionCreateHandoff, projectassistant.ActionUpdateHandoff:
		handoff, ok, err := h.loadProjectWorkHandoffForCairnlineMirror(ctx, projectID, projectAssistantActionResultValue(result, "handoff_id"))
		if err != nil || !ok {
			return err
		}
		return h.writeProjectHandoffToCairnline(ctx, handoff)
	case projectassistant.ActionCreateMemoryCandidate:
		candidate, ok, err := h.loadProjectMemoryCandidateForCairnlineMirror(ctx, projectID, projectAssistantActionResultValue(result, "candidate_id"))
		if err != nil || !ok {
			return err
		}
		return h.writeProjectMemoryCandidateToCairnline(ctx, candidate)
	default:
		return nil
	}
}

func projectAssistantActionResultProjectID(result projectassistant.ActionResult) string {
	return projectAssistantActionResultValue(result, "project_id")
}

func projectAssistantActionResultValue(result projectassistant.ActionResult, key string) string {
	if result.Data != nil {
		if value := strings.TrimSpace(result.Data[key]); value != "" {
			return value
		}
	}
	return strings.TrimSpace(result.ID)
}

func (h *Handler) writeProjectIdentityToCairnline(ctx context.Context, project projects.Project) error {
	return h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		_, err := cairnlinebridge.UpsertProject(ctx, service, project)
		return err
	})
}

func (h *Handler) writeProjectDefaultsToCairnline(ctx context.Context, project projects.Project) error {
	return h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		_, err := cairnlinebridge.UpsertProjectDefaults(ctx, service, project)
		return err
	})
}

func (h *Handler) deleteProjectIdentityFromCairnline(ctx context.Context, project projects.Project) error {
	return h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		if err := cairnlinebridge.DeleteProject(ctx, service, project); err != nil && !errors.Is(err, cairnline.ErrNotFound) {
			return err
		}
		return nil
	})
}

func (h *Handler) writeProjectRootToCairnline(ctx context.Context, project projects.Project, root projects.Root) error {
	return h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		_, err := cairnlinebridge.UpsertRoot(ctx, service, project, root)
		return err
	})
}

func (h *Handler) deleteProjectRootFromCairnline(ctx context.Context, projectID, rootID string) error {
	return h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		if err := cairnlinebridge.DeleteRoot(ctx, service, projectID, rootID); err != nil && !errors.Is(err, cairnline.ErrNotFound) {
			return err
		}
		return nil
	})
}

func (h *Handler) writeProjectContextSourceToCairnline(ctx context.Context, project projects.Project, source projects.ContextSource) error {
	return h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		_, err := cairnlinebridge.UpsertContextSource(ctx, service, project, source)
		return err
	})
}

func (h *Handler) deleteProjectContextSourceFromCairnline(ctx context.Context, projectID, sourceID string) error {
	return h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		if err := cairnlinebridge.DeleteContextSource(ctx, service, projectID, sourceID); err != nil && !errors.Is(err, cairnline.ErrNotFound) {
			return err
		}
		return nil
	})
}

func (h *Handler) writeProjectRoleToCairnline(ctx context.Context, project projects.Project, role projectwork.AgentRoleProfile) error {
	return h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		if _, err := cairnlinebridge.UpsertProject(ctx, service, project); err != nil {
			return err
		}
		return h.writeProjectRoleRecordToCairnline(ctx, service, role)
	})
}

func (h *Handler) writeProjectRoleRecordToCairnline(ctx context.Context, service *cairnline.Service, role projectwork.AgentRoleProfile) error {
	if _, err := h.writeRoleAgentProfileToCairnline(ctx, service, role); err != nil {
		return err
	}
	_, err := cairnlinebridge.UpsertRole(ctx, service, role)
	return err
}

func (h *Handler) writeRoleAgentProfileToCairnline(ctx context.Context, service *cairnline.Service, role projectwork.AgentRoleProfile) (agentprofiles.Profile, error) {
	profileID := strings.TrimSpace(role.DefaultAgentProfile)
	if profileID == "" || h == nil || h.agentProfiles == nil {
		return agentprofiles.Profile{}, nil
	}
	profile, ok, err := h.agentProfiles.Get(ctx, profileID)
	if err != nil {
		return agentprofiles.Profile{}, err
	}
	if !ok {
		return agentprofiles.Profile{}, nil
	}
	_, err = cairnlinebridge.UpsertAgentProfile(ctx, service, profile)
	return profile, err
}

func (h *Handler) deleteProjectRoleFromCairnline(ctx context.Context, role projectwork.AgentRoleProfile) error {
	return h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		if err := cairnlinebridge.DeleteRole(ctx, service, role); err != nil && !errors.Is(err, cairnline.ErrNotFound) {
			return err
		}
		return nil
	})
}

func (h *Handler) writeProjectWorkItemToCairnline(ctx context.Context, project projects.Project, item projectwork.WorkItem) error {
	return h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		if _, err := cairnlinebridge.UpsertProject(ctx, service, project); err != nil {
			return err
		}
		_, err := cairnlinebridge.UpsertWorkItem(ctx, service, item)
		return err
	})
}

func (h *Handler) deleteProjectWorkItemFromCairnline(ctx context.Context, projectID, workItemID string) error {
	return h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		if err := cairnlinebridge.DeleteWorkItem(ctx, service, projectID, workItemID); err != nil && !errors.Is(err, cairnline.ErrNotFound) {
			return err
		}
		return nil
	})
}

func (h *Handler) writeProjectAssignmentToCairnline(ctx context.Context, assignment projectwork.Assignment) error {
	return h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		return h.writeProjectAssignmentRecordToCairnline(ctx, service, assignment)
	})
}

func (h *Handler) writeProjectAssignmentRecordToCairnline(ctx context.Context, service *cairnline.Service, assignment projectwork.Assignment) error {
	if err := h.writeProjectWorkItemDependencyToCairnline(ctx, service, "project_assignment_mutation", assignment.ProjectID, assignment.WorkItemID); err != nil {
		return err
	}
	role, ok, err := h.loadProjectWorkRoleForCairnlineMirror(ctx, assignment.ProjectID, assignment.RoleID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.Join(cairnline.ErrNotFound, errors.New("assignment role not found for Cairnline mirror"))
	}
	profile, err := h.writeRoleAgentProfileToCairnline(ctx, service, role)
	if err != nil {
		return err
	}
	if _, err := cairnlinebridge.UpsertRole(ctx, service, role); err != nil {
		return err
	}
	_, err = cairnlinebridge.UpsertAssignment(ctx, service, assignment, role, profile)
	return err
}

func (h *Handler) writeProjectWorkItemDependencyToCairnline(ctx context.Context, service *cairnline.Service, operation, projectID, workItemID string) error {
	if strings.TrimSpace(workItemID) == "" {
		return nil
	}
	project, ok := h.projectForCairnlineMirror(ctx, operation, projectID)
	if !ok {
		return nil
	}
	if _, err := cairnlinebridge.UpsertProject(ctx, service, project); err != nil {
		return err
	}
	if h == nil || h.projectWork == nil {
		return errors.New("project work store is not configured")
	}
	workItem, ok, err := h.projectWork.GetWorkItem(ctx, projectID, workItemID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.Join(cairnline.ErrNotFound, errors.New("work item not found for Cairnline mirror"))
	}
	_, err = cairnlinebridge.UpsertWorkItem(ctx, service, workItem)
	return err
}

func (h *Handler) writeProjectRoleDependencyToCairnline(ctx context.Context, service *cairnline.Service, projectID, roleID string) error {
	roleID = strings.TrimSpace(roleID)
	if roleID == "" {
		return nil
	}
	role, ok, err := h.loadProjectWorkRoleForCairnlineMirror(ctx, projectID, roleID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.Join(cairnline.ErrNotFound, errors.New("role not found for Cairnline mirror"))
	}
	return h.writeProjectRoleRecordToCairnline(ctx, service, role)
}

func (h *Handler) writeProjectAssignmentDependencyToCairnline(ctx context.Context, service *cairnline.Service, projectID, assignmentID string) error {
	assignment, ok, err := h.loadProjectWorkAssignmentForCairnlineMirror(ctx, projectID, assignmentID)
	if err != nil {
		return err
	}
	if !ok {
		if strings.TrimSpace(assignmentID) == "" {
			return nil
		}
		return errors.Join(cairnline.ErrNotFound, errors.New("assignment not found for Cairnline mirror"))
	}
	return h.writeProjectAssignmentRecordToCairnline(ctx, service, assignment)
}

func (h *Handler) deleteProjectAssignmentFromCairnline(ctx context.Context, projectID, assignmentID string) error {
	return h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		if err := cairnlinebridge.DeleteAssignment(ctx, service, projectID, assignmentID); err != nil && !errors.Is(err, cairnline.ErrNotFound) {
			return err
		}
		return nil
	})
}

func (h *Handler) writeProjectArtifactToCairnline(ctx context.Context, artifact projectwork.CollaborationArtifact) error {
	return h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		if err := h.writeProjectWorkItemDependencyToCairnline(ctx, service, "project_artifact_mutation", artifact.ProjectID, artifact.WorkItemID); err != nil {
			return err
		}
		if err := h.writeProjectRoleDependencyToCairnline(ctx, service, artifact.ProjectID, artifact.AuthorRoleID); err != nil {
			return err
		}
		if err := h.writeProjectAssignmentDependencyToCairnline(ctx, service, artifact.ProjectID, artifact.AssignmentID); err != nil {
			return err
		}
		if err := h.writeProjectAssignmentDependencyToCairnline(ctx, service, artifact.ProjectID, artifact.ReviewedAssignmentID); err != nil {
			return err
		}
		switch strings.TrimSpace(artifact.Kind) {
		case projectwork.ArtifactKindEvidenceLink:
			_, err := cairnlinebridge.RecordEvidence(ctx, service, artifact)
			return err
		case projectwork.ArtifactKindReview:
			_, err := cairnlinebridge.RecordReview(ctx, service, artifact)
			return err
		default:
			_, err := cairnlinebridge.RecordArtifact(ctx, service, artifact)
			return err
		}
	})
}

func (h *Handler) writeProjectHandoffToCairnline(ctx context.Context, handoff projectwork.Handoff) error {
	return h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		if err := h.writeProjectWorkItemDependencyToCairnline(ctx, service, "project_handoff_mutation", handoff.ProjectID, handoff.WorkItemID); err != nil {
			return err
		}
		if err := h.writeProjectWorkItemDependencyToCairnline(ctx, service, "project_handoff_mutation", handoff.ProjectID, handoff.TargetWorkItemID); err != nil {
			return err
		}
		if err := h.writeProjectRoleDependencyToCairnline(ctx, service, handoff.ProjectID, handoff.CreatedByRoleID); err != nil {
			return err
		}
		if err := h.writeProjectRoleDependencyToCairnline(ctx, service, handoff.ProjectID, handoff.TargetRoleID); err != nil {
			return err
		}
		if err := h.writeProjectAssignmentDependencyToCairnline(ctx, service, handoff.ProjectID, handoff.SourceAssignmentID); err != nil {
			return err
		}
		if err := h.writeProjectAssignmentDependencyToCairnline(ctx, service, handoff.ProjectID, handoff.TargetAssignmentID); err != nil {
			return err
		}
		_, err := cairnlinebridge.UpsertHandoff(ctx, service, handoff)
		return err
	})
}

func (h *Handler) deleteProjectHandoffFromCairnline(ctx context.Context, projectID, workItemID, handoffID string) error {
	return h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		if err := cairnlinebridge.DeleteHandoff(ctx, service, projectID, workItemID, handoffID); err != nil && !errors.Is(err, cairnline.ErrNotFound) {
			return err
		}
		return nil
	})
}

func (h *Handler) writeProjectMemoryEntryToCairnline(ctx context.Context, entry memory.Entry) error {
	return h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		project, ok := h.projectForCairnlineMirror(ctx, "project_memory_mutation", entry.ProjectID)
		if !ok {
			return nil
		}
		if _, err := cairnlinebridge.UpsertProject(ctx, service, project); err != nil {
			return err
		}
		_, err := cairnlinebridge.UpsertMemoryEntry(ctx, service, entry)
		return err
	})
}

func (h *Handler) deleteProjectMemoryEntryFromCairnline(ctx context.Context, projectID, memoryID string) error {
	return h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		if err := cairnlinebridge.DeleteMemoryEntry(ctx, service, projectID, memoryID); err != nil && !errors.Is(err, cairnline.ErrNotFound) {
			return err
		}
		return nil
	})
}

func (h *Handler) writeProjectMemoryCandidateToCairnline(ctx context.Context, candidate memory.Candidate) error {
	return h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		project, ok := h.projectForCairnlineMirror(ctx, "project_memory_candidate_mutation", candidate.ProjectID)
		if !ok {
			return nil
		}
		if _, err := cairnlinebridge.UpsertProject(ctx, service, project); err != nil {
			return err
		}
		_, err := cairnlinebridge.UpsertMemoryCandidate(ctx, service, candidate)
		return err
	})
}

func (h *Handler) writeAgentProfileToCairnline(ctx context.Context, profile agentprofiles.Profile) error {
	return h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		_, err := cairnlinebridge.UpsertAgentProfile(ctx, service, profile)
		return err
	})
}

func (h *Handler) deleteAgentProfileFromCairnline(ctx context.Context, profileID string) error {
	return h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		if err := cairnlinebridge.DeleteAgentProfile(ctx, service, profileID); err != nil && !errors.Is(err, cairnline.ErrNotFound) {
			return err
		}
		return nil
	})
}

func (h *Handler) writeProjectAssistantProposalRecordToCairnline(ctx context.Context, record projectassistant.ProposalRecord) error {
	return h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		projectID := strings.TrimSpace(record.ProjectID)
		if projectID != "" && h != nil && h.projects != nil {
			project, ok, err := h.projects.Get(ctx, projectID)
			if err != nil {
				return err
			}
			if ok {
				if _, err := cairnlinebridge.UpsertProject(ctx, service, project); err != nil {
					return err
				}
			}
		}
		_, _, err := cairnlinebridge.UpsertAssistantProposalRecord(ctx, service, record)
		return err
	})
}

func (h *Handler) writeProjectSkillsToCairnline(ctx context.Context, project projects.Project, skills []projectskills.Skill) error {
	return h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		if _, err := cairnlinebridge.UpsertProject(ctx, service, project); err != nil {
			return err
		}
		_, err := cairnlinebridge.UpsertProjectSkills(ctx, service, skills)
		return err
	})
}

func (h *Handler) withCairnlineEmbeddedMirrorService(ctx context.Context, fn func(*cairnline.Service) error) error {
	if h == nil || h.config.ProjectsCoordinationBackend() != "cairnline" {
		return nil
	}
	dbPath := h.cairnlineEmbeddedDatabasePath()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return err
	}
	service, store, err := cairnline.NewSQLiteService(ctx, dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	return fn(service)
}

func (h *Handler) logCairnlineMirrorError(ctx context.Context, operation, projectID string, err error) {
	logger := slog.Default()
	if h != nil && h.logger != nil {
		logger = h.logger
	}
	logger.WarnContext(ctx, "cairnline project mirror write failed",
		"operation", operation,
		"project_id", projectID,
		"err", err)
}

func (h *Handler) logCairnlineAgentProfileMirrorError(ctx context.Context, operation, profileID string, err error) {
	logger := slog.Default()
	if h != nil && h.logger != nil {
		logger = h.logger
	}
	logger.WarnContext(ctx, "cairnline agent profile mirror write failed",
		"operation", operation,
		"profile_id", profileID,
		"err", err)
}
