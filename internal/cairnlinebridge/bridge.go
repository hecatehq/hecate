// Package cairnlinebridge maps Hecate Projects records into Cairnline's
// portable coordination model.
//
// This package is an integration proof, not a runtime backend switch. Hecate
// remains the execution/orchestration authority; Cairnline receives durable
// coordination records that can later back MCP pull or migration experiments.
package cairnlinebridge

import (
	"context"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
)

type Snapshot struct {
	Project       projects.Project
	AgentProfiles []agentprofiles.Profile
	Skills        []projectskills.Skill
	Roles         []projectwork.AgentRoleProfile
	WorkItems     []projectwork.WorkItem
	Assignments   []projectwork.Assignment
}

func Seed(ctx context.Context, service *cairnline.Service, snapshot Snapshot) error {
	if _, err := service.CreateProject(ctx, Project(snapshot.Project)); err != nil {
		return err
	}
	profilesByID := make(map[string]agentprofiles.Profile, len(snapshot.AgentProfiles))
	for _, profile := range snapshot.AgentProfiles {
		profilesByID[profile.ID] = profile
		if _, err := service.CreateAgentProfile(ctx, AgentProfile(profile)); err != nil {
			return err
		}
		if _, err := service.CreateExecutionProfile(ctx, ExecutionProfile(profile)); err != nil {
			return err
		}
	}
	for _, skill := range snapshot.Skills {
		if _, err := service.CreateProjectSkill(ctx, ProjectSkill(skill)); err != nil {
			return err
		}
	}
	rolesByID := make(map[string]projectwork.AgentRoleProfile, len(snapshot.Roles))
	for _, role := range snapshot.Roles {
		rolesByID[role.ID] = role
		if _, err := service.CreateRole(ctx, Role(role)); err != nil {
			return err
		}
	}
	for _, item := range snapshot.WorkItems {
		if _, err := service.CreateWorkItem(ctx, WorkItem(item)); err != nil {
			return err
		}
	}
	for _, assignment := range snapshot.Assignments {
		role := rolesByID[assignment.RoleID]
		profile := profilesByID[role.DefaultAgentProfile]
		item := Assignment(assignment, role, profile)
		if _, err := service.CreateAssignment(ctx, item); err != nil {
			return err
		}
		if err := syncAssignmentStatus(ctx, service, item); err != nil {
			return err
		}
	}
	return nil
}

func Project(project projects.Project) cairnline.Project {
	return cairnline.Project{
		ID:             strings.TrimSpace(project.ID),
		Name:           strings.TrimSpace(project.Name),
		Description:    strings.TrimSpace(project.Description),
		Roots:          Roots(project.Roots),
		ContextSources: Sources(project.ContextSources),
		CreatedAt:      project.CreatedAt,
		UpdatedAt:      project.UpdatedAt,
	}
}

func Roots(items []projects.Root) []cairnline.Root {
	out := make([]cairnline.Root, 0, len(items))
	for _, item := range items {
		out = append(out, cairnline.Root{
			ID:        strings.TrimSpace(item.ID),
			Path:      strings.TrimSpace(item.Path),
			Kind:      strings.TrimSpace(item.Kind),
			GitRemote: strings.TrimSpace(item.GitRemote),
			GitBranch: strings.TrimSpace(item.GitBranch),
			Active:    item.Active,
		})
	}
	return out
}

func Sources(items []projects.ContextSource) []cairnline.Source {
	out := make([]cairnline.Source, 0, len(items))
	for _, item := range items {
		out = append(out, cairnline.Source{
			ID:         strings.TrimSpace(item.ID),
			Kind:       strings.TrimSpace(item.Kind),
			Title:      strings.TrimSpace(item.Title),
			Locator:    strings.TrimSpace(item.Path),
			Enabled:    item.Enabled,
			TrustLabel: strings.TrimSpace(item.TrustLabel),
		})
	}
	return out
}

func AgentProfile(profile agentprofiles.Profile) cairnline.AgentProfile {
	return cairnline.AgentProfile{
		ID:           strings.TrimSpace(profile.ID),
		Name:         strings.TrimSpace(profile.Name),
		Description:  strings.TrimSpace(profile.Description),
		Instructions: strings.TrimSpace(profile.Instructions),
		MemoryPolicy: strings.TrimSpace(profile.ProjectMemoryPolicy),
		SourcePolicy: strings.TrimSpace(profile.ContextSourcePolicy),
		SkillIDs:     compactStrings(profile.SkillIDs),
		CreatedAt:    profile.CreatedAt,
		UpdatedAt:    profile.UpdatedAt,
	}
}

func ExecutionProfile(profile agentprofiles.Profile) cairnline.ExecutionProfile {
	return cairnline.ExecutionProfile{
		ID:             firstNonEmpty(strings.TrimSpace(profile.ExecutionProfile), strings.TrimSpace(profile.ID)),
		Name:           strings.TrimSpace(profile.Name),
		Description:    strings.TrimSpace(profile.Description),
		AgentKind:      executionAgentKind(profile),
		ModelHint:      strings.TrimSpace(profile.ModelHint),
		ProviderHint:   strings.TrimSpace(profile.ProviderHint),
		ToolsPolicy:    boolPolicy(profile.ToolsEnabled),
		WritesPolicy:   boolPolicy(profile.WritesAllowed),
		NetworkPolicy:  boolPolicy(profile.NetworkAllowed),
		ApprovalPolicy: strings.TrimSpace(profile.ApprovalPolicy),
		AdapterOptions: stringMapAny(profile.ExternalAgentOptions),
		CreatedAt:      profile.CreatedAt,
		UpdatedAt:      profile.UpdatedAt,
	}
}

func ProjectSkill(skill projectskills.Skill) cairnline.ProjectSkill {
	return cairnline.ProjectSkill{
		ID:          strings.TrimSpace(skill.ID),
		ProjectID:   strings.TrimSpace(skill.ProjectID),
		Title:       strings.TrimSpace(skill.Title),
		Description: strings.TrimSpace(skill.Description),
		Path:        strings.TrimSpace(skill.Path),
		RootID:      strings.TrimSpace(skill.RootID),
		Format:      strings.TrimSpace(skill.Format),
		Enabled:     skill.Enabled,
		Status:      strings.TrimSpace(skill.Status),
		TrustLabel:  strings.TrimSpace(skill.TrustLabel),
		SourceRefs:  compactStrings(skill.SourceContextSourceIDs),
		Warnings:    compactStrings(skill.Warnings),
		CreatedAt:   skill.CreatedAt,
		UpdatedAt:   skill.UpdatedAt,
	}
}

func Role(role projectwork.AgentRoleProfile) cairnline.Role {
	return cairnline.Role{
		ID:                   strings.TrimSpace(role.ID),
		ProjectID:            strings.TrimSpace(role.ProjectID),
		Name:                 strings.TrimSpace(role.Name),
		Description:          strings.TrimSpace(role.Description),
		Instructions:         strings.TrimSpace(role.Instructions),
		DefaultProfileID:     strings.TrimSpace(role.DefaultAgentProfile),
		DefaultSkillIDs:      compactStrings(role.SkillIDs),
		DefaultExecutionMode: ExecutionMode(role.DefaultDriverKind),
	}
}

func WorkItem(item projectwork.WorkItem) cairnline.WorkItem {
	return cairnline.WorkItem{
		ID:              strings.TrimSpace(item.ID),
		ProjectID:       strings.TrimSpace(item.ProjectID),
		Title:           strings.TrimSpace(item.Title),
		Brief:           strings.TrimSpace(item.Brief),
		Status:          strings.TrimSpace(item.Status),
		Priority:        strings.TrimSpace(item.Priority),
		OwnerRoleID:     strings.TrimSpace(item.OwnerRoleID),
		ReviewerRoleIDs: compactStrings(item.ReviewerRoleIDs),
		RootID:          strings.TrimSpace(item.RootID),
		CreatedAt:       item.CreatedAt,
		UpdatedAt:       item.UpdatedAt,
	}
}

func Assignment(assignment projectwork.Assignment, role projectwork.AgentRoleProfile, profile agentprofiles.Profile) cairnline.Assignment {
	return cairnline.Assignment{
		ID:                 strings.TrimSpace(assignment.ID),
		ProjectID:          strings.TrimSpace(assignment.ProjectID),
		WorkItemID:         strings.TrimSpace(assignment.WorkItemID),
		RoleID:             strings.TrimSpace(assignment.RoleID),
		RootID:             strings.TrimSpace(assignment.RootID),
		ProfileID:          strings.TrimSpace(role.DefaultAgentProfile),
		ExecutionProfileID: firstNonEmpty(strings.TrimSpace(profile.ExecutionProfile), strings.TrimSpace(profile.ID)),
		ExecutionMode:      ExecutionMode(assignment.DriverKind),
		Status:             AssignmentStatus(assignment.Status),
		DesiredAgent: cairnline.DesiredAgent{
			Kind:     DesiredAgentKind(assignment.DriverKind),
			SkillIDs: compactStrings(role.SkillIDs),
		},
		ExecutionRef:      assignmentExecutionRef(assignment.ExecutionRef),
		ContextSnapshotID: strings.TrimSpace(assignment.ExecutionRef.ContextSnapshotID),
		CreatedAt:         assignment.CreatedAt,
		UpdatedAt:         assignment.UpdatedAt,
	}
}

func ExecutionMode(driverKind string) string {
	switch strings.TrimSpace(driverKind) {
	case projectwork.AssignmentDriverHecateTask:
		return cairnline.ExecutionOrchestrated
	case projectwork.AssignmentDriverExternalAgent:
		return cairnline.ExecutionExternalAdapter
	default:
		return cairnline.ExecutionMCPPull
	}
}

func AssignmentStatus(status string) string {
	switch strings.TrimSpace(status) {
	case projectwork.AssignmentStatusRunning, projectwork.AssignmentStatusAwaitingApproval:
		return cairnline.AssignmentRunning
	case projectwork.AssignmentStatusCompleted:
		return cairnline.AssignmentCompleted
	case projectwork.AssignmentStatusFailed:
		return cairnline.AssignmentFailed
	case projectwork.AssignmentStatusCancelled:
		return cairnline.AssignmentCancelled
	default:
		return cairnline.AssignmentQueued
	}
}

func DesiredAgentKind(driverKind string) string {
	if strings.TrimSpace(driverKind) == projectwork.AssignmentDriverHecateTask {
		return "hecate"
	}
	return cairnline.DesiredAgentAny
}

func syncAssignmentStatus(ctx context.Context, service *cairnline.Service, assignment cairnline.Assignment) error {
	switch assignment.Status {
	case cairnline.AssignmentQueued:
		return nil
	case cairnline.AssignmentRunning, cairnline.AssignmentReview:
		if _, err := service.ClaimAssignment(ctx, assignment.ProjectID, assignment.ID, claimedBy(assignment)); err != nil {
			return err
		}
		_, err := service.UpdateAssignmentStatus(ctx, assignment.ProjectID, assignment.ID, assignment.Status, assignment.ExecutionRef)
		return err
	case cairnline.AssignmentCompleted, cairnline.AssignmentFailed, cairnline.AssignmentCancelled:
		_, err := service.CompleteAssignment(ctx, assignment.ProjectID, assignment.ID, assignment.Status, assignment.ExecutionRef)
		return err
	default:
		return nil
	}
}

func claimedBy(assignment cairnline.Assignment) string {
	if assignment.DesiredAgent.Kind == "hecate" {
		return "hecate"
	}
	return "external_adapter"
}

func executionAgentKind(profile agentprofiles.Profile) string {
	if profile.ExternalAgentKind != "" {
		return strings.TrimSpace(profile.ExternalAgentKind)
	}
	switch strings.TrimSpace(profile.Surface) {
	case agentprofiles.SurfaceHecateTask, agentprofiles.SurfaceHecateChat:
		return "hecate"
	case agentprofiles.SurfaceExternalAgent:
		return cairnline.DesiredAgentAny
	default:
		return cairnline.DesiredAgentAny
	}
}

func assignmentExecutionRef(ref projectwork.AssignmentExecutionRef) string {
	return firstNonEmpty(
		strings.TrimSpace(ref.RunID),
		strings.TrimSpace(ref.TaskID),
		strings.TrimSpace(ref.ChatSessionID),
		strings.TrimSpace(ref.ContextSnapshotID),
	)
}

func boolPolicy(value bool) string {
	if value {
		return "allow"
	}
	return "block"
}

func compactStrings(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func stringMapAny(items map[string]string) map[string]any {
	if len(items) == 0 {
		return nil
	}
	out := make(map[string]any, len(items))
	for key, value := range items {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = strings.TrimSpace(value)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
