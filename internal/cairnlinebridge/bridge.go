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
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
)

type Snapshot struct {
	Project          projects.Project
	AgentProfiles    []agentprofiles.Profile
	Skills           []projectskills.Skill
	Roles            []projectwork.AgentRoleProfile
	WorkItems        []projectwork.WorkItem
	Assignments      []projectwork.Assignment
	Artifacts        []projectwork.CollaborationArtifact
	Handoffs         []projectwork.Handoff
	MemoryEntries    []memory.Entry
	MemoryCandidates []memory.Candidate
}

func Seed(ctx context.Context, service *cairnline.Service, snapshot Snapshot) error {
	if _, err := service.CreateProject(ctx, Project(snapshot.Project)); err != nil {
		return err
	}
	profilesByID := make(map[string]agentprofiles.Profile, len(snapshot.AgentProfiles))
	executionProfileIDs := make(map[string]struct{}, len(snapshot.AgentProfiles))
	for _, profile := range snapshot.AgentProfiles {
		profilesByID[profile.ID] = profile
		if _, err := service.CreateAgentProfile(ctx, AgentProfile(profile)); err != nil {
			return err
		}
		executionProfile := ExecutionProfile(profile)
		if _, ok := executionProfileIDs[executionProfile.ID]; ok {
			continue
		}
		executionProfileIDs[executionProfile.ID] = struct{}{}
		if _, err := service.CreateExecutionProfile(ctx, executionProfile); err != nil {
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
	for _, artifact := range snapshot.Artifacts {
		if item, ok := Evidence(artifact); ok {
			if _, err := service.CreateEvidence(ctx, item); err != nil {
				return err
			}
			continue
		}
		if item, ok := Review(artifact); ok {
			if _, err := service.CreateReview(ctx, item); err != nil {
				return err
			}
		}
	}
	for _, handoff := range snapshot.Handoffs {
		if _, err := service.CreateHandoff(ctx, Handoff(handoff)); err != nil {
			return err
		}
	}
	for _, entry := range snapshot.MemoryEntries {
		if _, err := service.CreateMemoryEntry(ctx, MemoryEntry(entry)); err != nil {
			return err
		}
	}
	for _, candidate := range snapshot.MemoryCandidates {
		item := MemoryCandidate(candidate)
		created, err := service.CreateMemoryCandidate(ctx, item)
		if err != nil {
			return err
		}
		if created.Status != item.Status || created.StatusReason != item.StatusReason || created.PromotedMemoryID != item.PromotedMemoryID {
			if _, err := service.UpdateMemoryCandidate(ctx, item); err != nil {
				return err
			}
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

func Evidence(artifact projectwork.CollaborationArtifact) (cairnline.Evidence, bool) {
	if strings.TrimSpace(artifact.Kind) != projectwork.ArtifactKindEvidenceLink {
		return cairnline.Evidence{}, false
	}
	return cairnline.Evidence{
		ID:         strings.TrimSpace(artifact.ID),
		ProjectID:  strings.TrimSpace(artifact.ProjectID),
		WorkItemID: strings.TrimSpace(artifact.WorkItemID),
		Title:      strings.TrimSpace(artifact.Title),
		Body:       strings.TrimSpace(artifact.Body),
		Locator:    firstNonEmpty(strings.TrimSpace(artifact.EvidenceURL), strings.TrimSpace(artifact.EvidenceExternalID)),
		TrustLabel: firstNonEmpty(strings.TrimSpace(artifact.EvidenceTrustLabel), projectwork.EvidenceTrustOperatorProvided),
		CreatedAt:  artifact.CreatedAt,
		UpdatedAt:  artifact.UpdatedAt,
	}, true
}

func Review(artifact projectwork.CollaborationArtifact) (cairnline.Review, bool) {
	if strings.TrimSpace(artifact.Kind) != projectwork.ArtifactKindReview {
		return cairnline.Review{}, false
	}
	return cairnline.Review{
		ID:             strings.TrimSpace(artifact.ID),
		ProjectID:      strings.TrimSpace(artifact.ProjectID),
		WorkItemID:     strings.TrimSpace(artifact.WorkItemID),
		AssignmentID:   firstNonEmpty(strings.TrimSpace(artifact.ReviewedAssignmentID), strings.TrimSpace(artifact.AssignmentID)),
		ReviewerRoleID: strings.TrimSpace(artifact.AuthorRoleID),
		Title:          strings.TrimSpace(artifact.Title),
		Body:           strings.TrimSpace(artifact.Body),
		Verdict:        ReviewVerdict(artifact.ReviewVerdict),
		Risk:           ReviewRisk(artifact.ReviewRisk),
		Status:         cairnline.ReviewStatusRecorded,
		CreatedAt:      artifact.CreatedAt,
		UpdatedAt:      artifact.UpdatedAt,
	}, true
}

func Handoff(handoff projectwork.Handoff) cairnline.Handoff {
	return cairnline.Handoff{
		ID:         strings.TrimSpace(handoff.ID),
		ProjectID:  strings.TrimSpace(handoff.ProjectID),
		WorkItemID: strings.TrimSpace(handoff.WorkItemID),
		FromRoleID: strings.TrimSpace(handoff.CreatedByRoleID),
		ToRoleID:   strings.TrimSpace(handoff.TargetRoleID),
		Title:      strings.TrimSpace(handoff.Title),
		Body:       handoffBody(handoff),
		Status:     cairnline.HandoffStatusOpen,
		CreatedAt:  handoff.CreatedAt,
		UpdatedAt:  handoff.UpdatedAt,
	}
}

func MemoryEntry(entry memory.Entry) cairnline.MemoryEntry {
	return cairnline.MemoryEntry{
		ID:         strings.TrimSpace(entry.ID),
		ProjectID:  strings.TrimSpace(entry.ProjectID),
		Title:      strings.TrimSpace(entry.Title),
		Body:       strings.TrimSpace(entry.Body),
		TrustLabel: strings.TrimSpace(entry.TrustLabel),
		SourceKind: strings.TrimSpace(entry.SourceKind),
		SourceID:   strings.TrimSpace(entry.SourceID),
		Enabled:    entry.Enabled,
		CreatedAt:  entry.CreatedAt,
		UpdatedAt:  entry.UpdatedAt,
	}
}

func MemoryCandidate(candidate memory.Candidate) cairnline.MemoryCandidate {
	return cairnline.MemoryCandidate{
		ID:                  strings.TrimSpace(candidate.ID),
		ProjectID:           strings.TrimSpace(candidate.ProjectID),
		Title:               strings.TrimSpace(candidate.Title),
		Body:                strings.TrimSpace(candidate.Body),
		SuggestedKind:       strings.TrimSpace(candidate.SuggestedKind),
		SuggestedTrustLabel: strings.TrimSpace(candidate.SuggestedTrustLabel),
		SuggestedSourceKind: strings.TrimSpace(candidate.SuggestedSourceKind),
		SuggestedSourceID:   strings.TrimSpace(candidate.SuggestedSourceID),
		SourceRefs:          MemoryCandidateSourceRefs(candidate.SourceRefs),
		Status:              MemoryCandidateStatus(candidate.Status),
		StatusReason:        strings.TrimSpace(candidate.StatusReason),
		PromotedMemoryID:    strings.TrimSpace(candidate.PromotedMemoryID),
		CreatedAt:           candidate.CreatedAt,
		UpdatedAt:           candidate.UpdatedAt,
	}
}

func MemoryCandidateSourceRefs(refs []memory.CandidateSourceRef) []cairnline.MemoryCandidateSourceRef {
	if len(refs) == 0 {
		return nil
	}
	out := make([]cairnline.MemoryCandidateSourceRef, 0, len(refs))
	for _, ref := range refs {
		item := cairnline.MemoryCandidateSourceRef{
			Kind:  strings.TrimSpace(ref.Kind),
			ID:    strings.TrimSpace(ref.ID),
			Title: strings.TrimSpace(ref.Title),
			URL:   strings.TrimSpace(ref.URL),
		}
		if item.Kind == "" && item.ID == "" && item.Title == "" && item.URL == "" {
			continue
		}
		out = append(out, item)
	}
	return out
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

func ReviewVerdict(verdict string) string {
	switch strings.TrimSpace(verdict) {
	case projectwork.ReviewVerdictApproved:
		return cairnline.ReviewVerdictPass
	case projectwork.ReviewVerdictBlocked:
		return cairnline.ReviewVerdictBlocked
	default:
		return cairnline.ReviewVerdictConcerns
	}
}

func ReviewRisk(risk string) string {
	switch strings.TrimSpace(risk) {
	case projectwork.ReviewRiskLow:
		return cairnline.ReviewRiskLow
	case projectwork.ReviewRiskMedium:
		return cairnline.ReviewRiskMedium
	case projectwork.ReviewRiskHigh:
		return cairnline.ReviewRiskHigh
	default:
		return ""
	}
}

func MemoryCandidateStatus(status string) string {
	switch strings.TrimSpace(status) {
	case memory.CandidateStatusPromoted:
		return cairnline.MemoryCandidatePromoted
	case memory.CandidateStatusRejected:
		return cairnline.MemoryCandidateRejected
	default:
		return cairnline.MemoryCandidatePending
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

func handoffBody(handoff projectwork.Handoff) string {
	return joinParagraphs(
		strings.TrimSpace(handoff.Summary),
		labelValue("Recommended next action", handoff.RecommendedNextAction),
		labelValue("Source assignment", handoff.SourceAssignmentID),
		labelValue("Target assignment", handoff.TargetAssignmentID),
		labelList("Linked artifacts", handoff.LinkedArtifactIDs),
		labelList("Linked memory", handoff.LinkedMemoryIDs),
		labelList("Context refs", handoff.ContextRefs),
	)
}

func labelValue(label, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return label + ": " + value
}

func labelPair(kind, id string) string {
	kind = strings.TrimSpace(kind)
	id = strings.TrimSpace(id)
	if kind == "" || id == "" {
		return ""
	}
	return kind + ":" + id
}

func labelList(label string, values []string) string {
	values = compactStrings(values)
	if len(values) == 0 {
		return ""
	}
	return label + ": " + strings.Join(values, ", ")
}

func joinParagraphs(values ...string) string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return strings.Join(out, "\n\n")
}
