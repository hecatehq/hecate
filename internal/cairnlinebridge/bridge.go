// Package cairnlinebridge maps Hecate Projects records into Cairnline's
// portable coordination model.
//
// The mapping now backs live behavior, not just an integration proof: the
// configured Cairnline read routes serve project reads, opt-in write-authority
// switchpoints commit portable coordination state to Cairnline first, live
// mirrors shadow the remaining families into the embedded Cairnline database,
// and an armed embedded replacement mode can report Cairnline as authoritative
// for portable coordination state. Hecate stays the execution/orchestration
// authority: task/chat/External Agent dispatch, approvals, traces, root
// discovery, Git worktree creation, and assignment-start remain Hecate-owned
// runtime/workspace side effects, and migration cutover is still a rehearsal
// rather than a one-way storage switch.
package cairnlinebridge

import (
	"context"
	"fmt"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projectassistant"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
)

const AssignmentClaimedByOperator = "hecate_operator"

type Snapshot struct {
	Project            projects.Project
	Skills             []projectskills.Skill
	Roles              []projectwork.AgentRoleProfile
	WorkItems          []projectwork.WorkItem
	Assignments        []projectwork.Assignment
	Artifacts          []projectwork.CollaborationArtifact
	Handoffs           []projectwork.Handoff
	MemoryEntries      []memory.Entry
	MemoryCandidates   []memory.Candidate
	AssistantProposals []projectassistant.ProposalRecord
}

func Seed(ctx context.Context, service *cairnline.Service, snapshot Snapshot) error {
	return SeedSnapshots(ctx, service, []Snapshot{snapshot})
}

func seedProjectScopedSnapshot(ctx context.Context, service *cairnline.Service, snapshot Snapshot) error {
	if _, err := service.CreateProject(ctx, Project(snapshot.Project)); err != nil {
		return err
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
		if _, err := CreateAssignment(ctx, service, assignment, role); err != nil {
			return err
		}
	}
	for _, artifact := range snapshot.Artifacts {
		if item, ok := Artifact(artifact); ok {
			if _, err := service.CreateArtifact(ctx, item); err != nil {
				return err
			}
			continue
		}
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
		if _, err := UpsertMemoryEntry(ctx, service, entry); err != nil {
			return err
		}
	}
	for _, candidate := range snapshot.MemoryCandidates {
		if _, err := UpsertMemoryCandidate(ctx, service, candidate); err != nil {
			return err
		}
	}
	for _, proposal := range snapshot.AssistantProposals {
		item, ok := AssistantProposalRecord(proposal)
		if !ok {
			continue
		}
		if _, err := service.ImportAssistantProposalRecord(ctx, item); err != nil {
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
		DefaultRootID:  strings.TrimSpace(project.DefaultRootID),
		ContextSources: Sources(project.ContextSources),
		CreatedAt:      project.CreatedAt,
		UpdatedAt:      project.UpdatedAt,
	}
}

func Roots(items []projects.Root) []cairnline.Root {
	out := make([]cairnline.Root, 0, len(items))
	for _, item := range items {
		out = append(out, Root(item))
	}
	return out
}

func Root(item projects.Root) cairnline.Root {
	return cairnline.Root{
		ID:        strings.TrimSpace(item.ID),
		Path:      strings.TrimSpace(item.Path),
		Kind:      strings.TrimSpace(item.Kind),
		GitRemote: strings.TrimSpace(item.GitRemote),
		GitBranch: strings.TrimSpace(item.GitBranch),
		Active:    item.Active,
	}
}

func Sources(items []projects.ContextSource) []cairnline.Source {
	out := make([]cairnline.Source, 0, len(items))
	for _, item := range items {
		out = append(out, Source(item))
	}
	return out
}

func Source(item projects.ContextSource) cairnline.Source {
	return cairnline.Source{
		ID:             strings.TrimSpace(item.ID),
		Kind:           strings.TrimSpace(item.Kind),
		Title:          strings.TrimSpace(item.Title),
		Locator:        strings.TrimSpace(item.Path),
		Enabled:        item.Enabled,
		Format:         strings.TrimSpace(item.Format),
		Scope:          strings.TrimSpace(item.Scope),
		TrustLabel:     strings.TrimSpace(item.TrustLabel),
		SourceCategory: strings.TrimSpace(item.SourceCategory),
		Metadata:       stringMapString(item.Metadata),
		CreatedAt:      item.CreatedAt,
		UpdatedAt:      item.UpdatedAt,
	}
}

func ProjectSkill(skill projectskills.Skill) cairnline.ProjectSkill {
	return cairnline.ProjectSkill{
		ID:                  strings.TrimSpace(skill.ID),
		ProjectID:           strings.TrimSpace(skill.ProjectID),
		Title:               strings.TrimSpace(skill.Title),
		Description:         strings.TrimSpace(skill.Description),
		Path:                strings.TrimSpace(skill.Path),
		RootID:              strings.TrimSpace(skill.RootID),
		Format:              strings.TrimSpace(skill.Format),
		SuggestedTools:      compactStrings(skill.SuggestedTools),
		RequiredPermissions: RequiredPermissions(skill.RequiredPermissions),
		Enabled:             skill.Enabled,
		Status:              strings.TrimSpace(skill.Status),
		TrustLabel:          strings.TrimSpace(skill.TrustLabel),
		SourceRefs:          compactStrings(skill.SourceContextSourceIDs),
		Warnings:            compactStrings(skill.Warnings),
		DiscoveredAt:        skill.DiscoveredAt,
		CreatedAt:           skill.CreatedAt,
		UpdatedAt:           skill.UpdatedAt,
	}
}

func Role(role projectwork.AgentRoleProfile) cairnline.Role {
	return cairnline.Role{
		ID:                   strings.TrimSpace(role.ID),
		ProjectID:            strings.TrimSpace(role.ProjectID),
		Name:                 strings.TrimSpace(role.Name),
		Description:          strings.TrimSpace(role.Description),
		Instructions:         strings.TrimSpace(role.Instructions),
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

func Assignment(assignment projectwork.Assignment, role projectwork.AgentRoleProfile) cairnline.Assignment {
	return cairnline.Assignment{
		ID:            strings.TrimSpace(assignment.ID),
		ProjectID:     strings.TrimSpace(assignment.ProjectID),
		WorkItemID:    strings.TrimSpace(assignment.WorkItemID),
		RoleID:        strings.TrimSpace(assignment.RoleID),
		RootID:        strings.TrimSpace(assignment.RootID),
		ExecutionMode: ExecutionMode(assignment.DriverKind),
		Status:        AssignmentStatus(assignment.Status),
		DesiredAgent: cairnline.DesiredAgent{
			Kind:     DesiredAgentKind(assignment.DriverKind),
			SkillIDs: compactStrings(role.SkillIDs),
		},
		ExecutionRef:      ExecutionRef(assignment.ExecutionRef),
		ContextSnapshotID: strings.TrimSpace(assignment.ExecutionRef.ContextSnapshotID),
		CreatedAt:         assignment.CreatedAt,
		UpdatedAt:         assignment.UpdatedAt,
		StartedAt:         assignment.StartedAt,
		CompletedAt:       assignment.CompletedAt,
	}
}

func Artifact(artifact projectwork.CollaborationArtifact) (cairnline.Artifact, bool) {
	switch strings.TrimSpace(artifact.Kind) {
	case projectwork.ArtifactKindEvidenceLink, projectwork.ArtifactKindReview:
		return cairnline.Artifact{}, false
	}
	return cairnline.Artifact{
		ID:           strings.TrimSpace(artifact.ID),
		ProjectID:    strings.TrimSpace(artifact.ProjectID),
		WorkItemID:   strings.TrimSpace(artifact.WorkItemID),
		AssignmentID: strings.TrimSpace(artifact.AssignmentID),
		Kind:         strings.TrimSpace(artifact.Kind),
		Title:        strings.TrimSpace(artifact.Title),
		Body:         strings.TrimSpace(artifact.Body),
		AuthorRoleID: strings.TrimSpace(artifact.AuthorRoleID),
		CreatedAt:    artifact.CreatedAt,
		UpdatedAt:    artifact.UpdatedAt,
	}, true
}

func Evidence(artifact projectwork.CollaborationArtifact) (cairnline.Evidence, bool) {
	if strings.TrimSpace(artifact.Kind) != projectwork.ArtifactKindEvidenceLink {
		return cairnline.Evidence{}, false
	}
	return cairnline.Evidence{
		ID:           strings.TrimSpace(artifact.ID),
		ProjectID:    strings.TrimSpace(artifact.ProjectID),
		WorkItemID:   strings.TrimSpace(artifact.WorkItemID),
		AssignmentID: strings.TrimSpace(artifact.AssignmentID),
		Title:        strings.TrimSpace(artifact.Title),
		Body:         strings.TrimSpace(artifact.Body),
		Locator:      firstNonEmpty(strings.TrimSpace(artifact.EvidenceURL), strings.TrimSpace(artifact.EvidenceExternalID)),
		SourceKind:   strings.TrimSpace(artifact.EvidenceSourceKind),
		ExternalID:   strings.TrimSpace(artifact.EvidenceExternalID),
		Provider:     strings.TrimSpace(artifact.EvidenceProvider),
		TrustLabel:   firstNonEmpty(strings.TrimSpace(artifact.EvidenceTrustLabel), projectwork.EvidenceTrustOperatorProvided),
		CreatedAt:    artifact.CreatedAt,
		UpdatedAt:    artifact.UpdatedAt,
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
	statusChangedAt := handoff.StatusChangedAt
	if statusChangedAt.IsZero() {
		statusChangedAt = handoff.CreatedAt
	}
	return cairnline.Handoff{
		ID:                    strings.TrimSpace(handoff.ID),
		ProjectID:             strings.TrimSpace(handoff.ProjectID),
		WorkItemID:            strings.TrimSpace(handoff.WorkItemID),
		SourceAssignmentID:    strings.TrimSpace(handoff.SourceAssignmentID),
		SourceRunID:           strings.TrimSpace(handoff.SourceRunID),
		SourceChatSessionID:   strings.TrimSpace(handoff.SourceChatSessionID),
		SourceMessageID:       strings.TrimSpace(handoff.SourceMessageID),
		FromRoleID:            strings.TrimSpace(handoff.CreatedByRoleID),
		ToRoleID:              strings.TrimSpace(handoff.TargetRoleID),
		TargetAssignmentID:    strings.TrimSpace(handoff.TargetAssignmentID),
		TargetWorkItemID:      strings.TrimSpace(handoff.TargetWorkItemID),
		Title:                 strings.TrimSpace(handoff.Title),
		Body:                  firstNonEmpty(strings.TrimSpace(handoff.Summary), handoffBody(handoff)),
		RecommendedNextAction: strings.TrimSpace(handoff.RecommendedNextAction),
		LinkedArtifactIDs:     compactStrings(handoff.LinkedArtifactIDs),
		LinkedMemoryIDs:       compactStrings(handoff.LinkedMemoryIDs),
		ContextRefs:           compactStrings(handoff.ContextRefs),
		Status:                HandoffStatus(handoff.Status),
		ProvenanceKind:        strings.TrimSpace(handoff.ProvenanceKind),
		TrustLabel:            strings.TrimSpace(handoff.TrustLabel),
		CreatedAt:             handoff.CreatedAt,
		UpdatedAt:             handoff.UpdatedAt,
		StatusChangedAt:       statusChangedAt,
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
	case projectwork.AssignmentDriverManual:
		return cairnline.ExecutionManual
	default:
		return cairnline.ExecutionMCPPull
	}
}

func AssignmentStatus(status string) string {
	switch strings.TrimSpace(status) {
	case projectwork.AssignmentStatusRunning:
		return cairnline.AssignmentRunning
	case projectwork.AssignmentStatusAwaitingApproval:
		return cairnline.AssignmentAwaitingApproval
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

// AssignmentStatusFromCairnline maps a portable Cairnline assignment status
// back into Hecate's assignment vocabulary. Hecate has no separate
// awaiting_review assignment state, so Cairnline's review status also lands on
// awaiting_approval: both are operator-gated pauses and collapsing them keeps
// the Hecate side conservative (blocked) rather than optimistic (running).
func AssignmentStatusFromCairnline(status string) string {
	switch strings.TrimSpace(status) {
	case cairnline.AssignmentRunning, cairnline.AssignmentClaimed:
		return projectwork.AssignmentStatusRunning
	case cairnline.AssignmentAwaitingApproval, cairnline.AssignmentReview:
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

// AssignmentStatusVocabularyGap reports the first Hecate assignment status that
// does not survive a portable round trip through the Cairnline status
// vocabulary. The replacement-readiness smoke calls this so a mapping clamp
// (the old awaiting_approval -> running collapse) blocks replacement_ready
// instead of silently mislabelling operator-gated assignments as running.
func AssignmentStatusVocabularyGap() error {
	statuses := []string{
		projectwork.AssignmentStatusQueued,
		projectwork.AssignmentStatusRunning,
		projectwork.AssignmentStatusAwaitingApproval,
		projectwork.AssignmentStatusCompleted,
		projectwork.AssignmentStatusFailed,
		projectwork.AssignmentStatusCancelled,
	}
	for _, status := range statuses {
		if got := AssignmentStatusFromCairnline(AssignmentStatus(status)); got != status {
			return fmt.Errorf("hecate assignment status %q maps to portable %q which reads back as %q", status, AssignmentStatus(status), got)
		}
	}
	return nil
}

func ReviewVerdict(verdict string) string {
	switch strings.TrimSpace(verdict) {
	case projectwork.ReviewVerdictApproved:
		return cairnline.ReviewVerdictApproved
	case projectwork.ReviewVerdictChangesRequested:
		return cairnline.ReviewVerdictChangesRequested
	case projectwork.ReviewVerdictBlocked:
		return cairnline.ReviewVerdictBlocked
	case projectwork.ReviewVerdictRisk:
		return cairnline.ReviewVerdictRisk
	default:
		return cairnline.ReviewVerdictChangesRequested
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
	case projectwork.ReviewRiskUnknown:
		return cairnline.ReviewRiskUnknown
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

func HandoffStatus(status string) string {
	switch strings.TrimSpace(status) {
	case projectwork.HandoffStatusAccepted:
		return cairnline.HandoffStatusAccepted
	case projectwork.HandoffStatusSuperseded:
		return cairnline.HandoffStatusSuperseded
	case projectwork.HandoffStatusDismissed:
		return cairnline.HandoffStatusDismissed
	default:
		return cairnline.HandoffStatusOpen
	}
}

func DesiredAgentKind(driverKind string) string {
	switch strings.TrimSpace(driverKind) {
	case projectwork.AssignmentDriverHecateTask:
		return "hecate"
	case projectwork.AssignmentDriverManual:
		return "human"
	default:
		return cairnline.DesiredAgentAny
	}
}

func syncAssignmentStatus(ctx context.Context, service *cairnline.Service, existing, assignment cairnline.Assignment) error {
	if existing.ID == "" {
		return nil
	}
	desiredStatus := strings.TrimSpace(assignment.Status)
	if desiredStatus == "" {
		desiredStatus = cairnline.AssignmentQueued
	}
	if existing.Status == desiredStatus && assignmentTerminalStatus(desiredStatus) {
		return nil
	}
	if desiredStatus == cairnline.AssignmentQueued {
		switch existing.Status {
		case cairnline.AssignmentQueued:
			return nil
		case cairnline.AssignmentClaimed:
			// A successfully dispatched Hecate task can remain locally queued
			// while its runner is prepared. Releasing is rollback, not generic
			// reconciliation, and must stay explicit in the start handlers.
			return nil
		default:
			return cairnline.ErrConflict
		}
	}
	if assignmentTerminalStatus(existing.Status) {
		return cairnline.ErrConflict
	}
	if existing.Status == cairnline.AssignmentQueued && assignmentTerminalStatus(desiredStatus) && assignment.ExecutionRef.Empty() && strings.TrimSpace(assignment.ContextSnapshotID) == "" {
		_, err := service.CompleteAssignment(ctx, assignment.ProjectID, assignment.ID, desiredStatus, assignment.ExecutionRef)
		return err
	}
	if existing.Status == cairnline.AssignmentQueued {
		var err error
		existing, err = service.ClaimAssignment(ctx, assignment.ProjectID, assignment.ID, claimedBy(assignment))
		if err != nil {
			return err
		}
	}
	if existing.Status == cairnline.AssignmentClaimed {
		if existing.ClaimedBy != claimedBy(assignment) {
			return cairnline.ErrConflict
		}
		if !assignment.ExecutionRef.Empty() || strings.TrimSpace(assignment.ContextSnapshotID) != "" {
			prepared, err := service.PrepareAssignment(ctx, assignment.ProjectID, assignment.ID, cairnline.AssignmentPreparation{
				ClaimedBy:         existing.ClaimedBy,
				ExecutionRef:      assignment.ExecutionRef,
				ContextSnapshotID: assignment.ContextSnapshotID,
			})
			if err != nil {
				return err
			}
			existing = prepared
		}
	} else if strings.TrimSpace(assignment.ContextSnapshotID) != "" && assignment.ContextSnapshotID != existing.ContextSnapshotID {
		return cairnline.ErrConflict
	}
	switch desiredStatus {
	case cairnline.AssignmentRunning, cairnline.AssignmentAwaitingApproval, cairnline.AssignmentReview:
		_, err := service.UpdateAssignmentStatus(ctx, assignment.ProjectID, assignment.ID, desiredStatus, assignment.ExecutionRef)
		return err
	case cairnline.AssignmentCompleted, cairnline.AssignmentFailed, cairnline.AssignmentCancelled:
		_, err := service.CompleteAssignment(ctx, assignment.ProjectID, assignment.ID, desiredStatus, assignment.ExecutionRef)
		return err
	default:
		return cairnline.ErrInvalid
	}
}

func assignmentTerminalStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case cairnline.AssignmentCompleted, cairnline.AssignmentFailed, cairnline.AssignmentCancelled:
		return true
	default:
		return false
	}
}

func claimedBy(assignment cairnline.Assignment) string {
	if assignment.ExecutionMode == cairnline.ExecutionManual {
		return AssignmentClaimedByOperator
	}
	switch assignment.DesiredAgent.Kind {
	case "hecate":
		return "hecate"
	default:
		return "external_adapter"
	}
}

// ExecutionRef maps Hecate's assignment execution ref onto Cairnline's
// host-neutral structured ref. MessageID, Status, and Missing are deliberately
// not carried: message linkage is a Hecate chat-runtime detail, and
// status/missing are runtime projections recomputed on every read, so
// persisting them portable-side would freeze stale values into the
// coordination row. ContextSnapshotID stays on the assignment-level Cairnline
// field, never inside the ref.
func ExecutionRef(ref projectwork.AssignmentExecutionRef) cairnline.ExecutionRef {
	normalized := projectwork.NormalizeAssignmentExecutionRef(ref)
	out := cairnline.ExecutionRef{
		TaskID:           normalized.TaskID,
		RunID:            normalized.RunID,
		SessionID:        normalized.ChatSessionID,
		TraceID:          normalized.TraceID,
		PendingApprovals: normalized.PendingApprovalCount,
	}
	// A ref that would carry only a kind (for example a context-snapshot-only
	// ref, whose kind is derived from an id that maps outside the ref) stays
	// empty so it cannot clobber a stored portable ref: Cairnline only
	// overwrites the stored ref when the incoming one is non-empty.
	if out.Empty() {
		return out
	}
	out.Kind = normalized.Kind
	return out
}

// AssignmentExecutionRefFromCairnline maps the portable ref back into Hecate's
// richer shape. ContextSnapshotID lives on the Cairnline assignment record, so
// callers pass it alongside; MessageID, Status, and Missing stay zero because
// they are Hecate runtime projections recomputed on read.
func AssignmentExecutionRefFromCairnline(ref cairnline.ExecutionRef, contextSnapshotID string) projectwork.AssignmentExecutionRef {
	return projectwork.NormalizeAssignmentExecutionRef(projectwork.AssignmentExecutionRef{
		Kind:                 ref.Kind,
		TaskID:               ref.TaskID,
		RunID:                ref.RunID,
		ChatSessionID:        ref.SessionID,
		ContextSnapshotID:    contextSnapshotID,
		TraceID:              ref.TraceID,
		PendingApprovalCount: ref.PendingApprovals,
	})
}

// ExecutionRefFidelityGap describes the first Hecate execution-ref field that
// the stored portable ref fails to carry, or "" when the portable row is
// faithful. Parity is strict, including Kind: Cairnline rejects pre-structured
// bare-string refs outright, so rows written before the structured contract
// are rebuilt via the full-refresh sync path rather than tolerated here.
func ExecutionRefFidelityGap(ref projectwork.AssignmentExecutionRef, portable cairnline.ExecutionRef) string {
	want := ExecutionRef(ref)
	if want.Empty() {
		return ""
	}
	if portable.TaskID != want.TaskID {
		return fmt.Sprintf("portable execution_ref task_id %q does not match hecate task_id %q", portable.TaskID, want.TaskID)
	}
	if portable.RunID != want.RunID {
		return fmt.Sprintf("portable execution_ref run_id %q does not match hecate run_id %q", portable.RunID, want.RunID)
	}
	if portable.SessionID != want.SessionID {
		return fmt.Sprintf("portable execution_ref session_id %q does not match hecate chat_session_id %q", portable.SessionID, want.SessionID)
	}
	if portable.TraceID != want.TraceID {
		return fmt.Sprintf("portable execution_ref trace_id %q does not match hecate trace_id %q", portable.TraceID, want.TraceID)
	}
	if portable.PendingApprovals != want.PendingApprovals {
		return fmt.Sprintf("portable execution_ref pending_approvals %d does not match hecate pending_approval_count %d", portable.PendingApprovals, want.PendingApprovals)
	}
	if portable.Kind != want.Kind {
		return fmt.Sprintf("portable execution_ref kind %q does not match hecate kind %q", portable.Kind, want.Kind)
	}
	return ""
}

func RequiredPermissions(permissions projectskills.RequiredPermissions) cairnline.RequiredPermissions {
	return cairnline.RequiredPermissions{
		Tools:   cloneBoolPointer(permissions.Tools),
		Writes:  cloneBoolPointer(permissions.Writes),
		Network: cloneBoolPointer(permissions.Network),
	}
}

func cloneBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	out := *value
	return &out
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

func stringMapString(items map[string]string) map[string]string {
	if len(items) == 0 {
		return nil
	}
	out := make(map[string]string, len(items))
	for key, value := range items {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = strings.TrimSpace(value)
	}
	if len(out) == 0 {
		return nil
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
