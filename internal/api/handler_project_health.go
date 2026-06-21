package api

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/projectworkapp"
)

const (
	projectHealthAttentionLimit = 5
)

type ProjectHealthEnvelope struct {
	Object string                `json:"object"`
	Data   ProjectHealthResponse `json:"data"`
}

type ProjectHealthResponse struct {
	ProjectID   string                       `json:"project_id"`
	GeneratedAt string                       `json:"generated_at"`
	Summary     ProjectHealthSummaryResponse `json:"summary"`
	Attention   []ProjectHealthAttentionItem `json:"attention"`
}

type ProjectHealthSummaryResponse struct {
	AttentionCount                int  `json:"attention_count"`
	AvailableAttentionCount       int  `json:"available_attention_count"`
	OmittedAttentionCount         int  `json:"omitted_attention_count"`
	AttentionLimit                int  `json:"attention_limit"`
	MissingDefaults               bool `json:"missing_defaults"`
	MissingProjectRoot            bool `json:"missing_project_root"`
	EnabledMemoryCount            int  `json:"enabled_memory_count"`
	SavedMemoryCount              int  `json:"saved_memory_count"`
	EnabledContextSourceCount     int  `json:"enabled_context_source_count"`
	PendingMemoryCandidateCount   int  `json:"pending_memory_candidate_count"`
	PromotedMemoryCandidateCount  int  `json:"promoted_memory_candidate_count"`
	RejectedMemoryCandidateCount  int  `json:"rejected_memory_candidate_count"`
	PendingHandoffCount           int  `json:"pending_handoff_count"`
	AcceptedHandoffCount          int  `json:"accepted_handoff_count"`
	SupersededHandoffCount        int  `json:"superseded_handoff_count"`
	DismissedHandoffCount         int  `json:"dismissed_handoff_count"`
	ReviewFollowUpCount           int  `json:"review_follow_up_count"`
	BlockedReviewCount            int  `json:"blocked_review_count"`
	ChangesRequestedReviewCount   int  `json:"changes_requested_review_count"`
	StaleOrUnknownAssignmentCount int  `json:"stale_or_unknown_assignment_count"`
}

type ProjectHealthAttentionItem struct {
	ID          string                `json:"id"`
	ProjectID   string                `json:"project_id"`
	Title       string                `json:"title"`
	Detail      string                `json:"detail"`
	Status      string                `json:"status"`
	Action      ProjectActionResponse `json:"action"`
	Bucket      string                `json:"bucket,omitempty"`
	WorkItemID  string                `json:"work_item_id,omitempty"`
	TaskID      string                `json:"task_id,omitempty"`
	RunID       string                `json:"run_id,omitempty"`
	ChatID      string                `json:"chat_id,omitempty"`
	CandidateID string                `json:"candidate_id,omitempty"`
	ActionLabel string                `json:"action_label,omitempty"`
}

func (h *Handler) HandleProjectHealth(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if !h.requireProject(w, r, projectID) {
		return
	}
	health, err := h.renderProjectHealth(r.Context(), projectID)
	if err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
			return
		}
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, ProjectHealthEnvelope{Object: "project_health", Data: health})
}

func (h *Handler) renderProjectHealth(ctx context.Context, projectID string) (ProjectHealthResponse, error) {
	project, ok, err := h.projects.Get(ctx, projectID)
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	if !ok {
		return ProjectHealthResponse{}, projects.ErrNotFound
	}
	activity, err := h.renderProjectActivity(ctx, projectID)
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	roles, err := h.projectWork.ListRoles(ctx, projectID)
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	workItems, err := h.projectWork.ListWorkItems(ctx, projectID)
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	assignments, err := h.projectWork.ListAssignments(ctx, projectwork.AssignmentFilter{ProjectID: projectID})
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	handoffs, err := h.projectWork.ListHandoffs(ctx, projectwork.HandoffFilter{ProjectID: projectID})
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	artifacts, err := h.projectWork.ListArtifacts(ctx, projectwork.ArtifactFilter{ProjectID: projectID})
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	memoryEntries, err := h.projectHealthMemoryEntries(ctx, projectID)
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	memoryCandidates, err := h.projectHealthMemoryCandidates(ctx, projectID)
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	agentProfiles, err := h.projectHealthAgentProfiles(ctx)
	if err != nil {
		return ProjectHealthResponse{}, err
	}
	skills, err := h.projectHealthSkills(ctx, projectID)
	if err != nil {
		return ProjectHealthResponse{}, err
	}

	now := time.Now().UTC()
	staleAssignments := projectHealthStaleAssignments(project, activity, workItems, assignments, now)
	summary := projectHealthSummary(project, memoryEntries, memoryCandidates, handoffs, artifacts, staleAssignments)
	attention := projectHealthAttentionItems(project, activity, workItems, roles, handoffs, artifacts, memoryEntries, memoryCandidates, agentProfiles, skills, staleAssignments)
	availableAttentionCount := len(attention)
	attention = boundedProjectHealthAttention(attention, projectHealthAttentionLimit)
	summary.AttentionCount = len(attention)
	summary.AvailableAttentionCount = availableAttentionCount
	summary.OmittedAttentionCount = availableAttentionCount - len(attention)
	summary.AttentionLimit = projectHealthAttentionLimit

	return ProjectHealthResponse{
		ProjectID:   project.ID,
		GeneratedAt: formatOptionalTime(now),
		Summary:     summary,
		Attention:   attention,
	}, nil
}

func (h *Handler) projectHealthMemoryEntries(ctx context.Context, projectID string) ([]memory.Entry, error) {
	if h.memory == nil {
		return nil, nil
	}
	return h.memory.List(ctx, memory.Filter{ProjectID: strings.TrimSpace(projectID), IncludeDisabled: true})
}

func (h *Handler) projectHealthMemoryCandidates(ctx context.Context, projectID string) ([]memory.Candidate, error) {
	if h.memoryCandidates == nil {
		return nil, nil
	}
	return h.memoryCandidates.ListCandidates(ctx, memory.CandidateFilter{ProjectID: strings.TrimSpace(projectID)})
}

func (h *Handler) projectHealthAgentProfiles(ctx context.Context) ([]agentprofiles.Profile, error) {
	if h.agentProfiles == nil {
		return nil, nil
	}
	return h.agentProfiles.List(ctx)
}

func (h *Handler) projectHealthSkills(ctx context.Context, projectID string) ([]projectskills.Skill, error) {
	if h.projectSkills == nil {
		return nil, nil
	}
	return h.projectSkills.List(ctx, projectID)
}

func projectHealthSummary(project projects.Project, entries []memory.Entry, candidates []memory.Candidate, handoffs []projectwork.Handoff, artifacts []projectwork.CollaborationArtifact, staleAssignments []ProjectActivityItemResponse) ProjectHealthSummaryResponse {
	summary := ProjectHealthSummaryResponse{
		MissingDefaults:               projectHealthMissingDefaults(project),
		MissingProjectRoot:            !projectHasActiveRoot(project),
		StaleOrUnknownAssignmentCount: len(staleAssignments),
	}
	for _, entry := range entries {
		summary.SavedMemoryCount++
		if entry.Enabled {
			summary.EnabledMemoryCount++
		}
	}
	for _, source := range project.ContextSources {
		if source.Enabled {
			summary.EnabledContextSourceCount++
		}
	}
	for _, candidate := range candidates {
		switch candidate.Status {
		case memory.CandidateStatusPending:
			summary.PendingMemoryCandidateCount++
		case memory.CandidateStatusPromoted:
			summary.PromotedMemoryCandidateCount++
		case memory.CandidateStatusRejected:
			summary.RejectedMemoryCandidateCount++
		}
	}
	for _, handoff := range handoffs {
		switch handoff.Status {
		case projectwork.HandoffStatusPending:
			summary.PendingHandoffCount++
		case projectwork.HandoffStatusAccepted:
			summary.AcceptedHandoffCount++
		case projectwork.HandoffStatusSuperseded:
			summary.SupersededHandoffCount++
		case projectwork.HandoffStatusDismissed:
			summary.DismissedHandoffCount++
		}
	}
	for _, artifact := range artifacts {
		if artifact.Kind != projectwork.ArtifactKindReview {
			continue
		}
		if projectworkapp.ReviewArtifactRequiresFollowUp(artifact) {
			summary.ReviewFollowUpCount++
		}
		switch artifact.ReviewVerdict {
		case projectwork.ReviewVerdictBlocked:
			summary.BlockedReviewCount++
		case projectwork.ReviewVerdictChangesRequested:
			summary.ChangesRequestedReviewCount++
		}
	}
	return summary
}

func projectHealthAttentionItems(project projects.Project, activity ProjectActivityDataResponse, workItems []projectwork.WorkItem, roles []projectwork.AgentRoleProfile, handoffs []projectwork.Handoff, artifacts []projectwork.CollaborationArtifact, memoryEntries []memory.Entry, memoryCandidates []memory.Candidate, agentProfiles []agentprofiles.Profile, skills []projectskills.Skill, staleAssignments []ProjectActivityItemResponse) []ProjectHealthAttentionItem {
	items := make([]ProjectHealthAttentionItem, 0, projectHealthAttentionLimit+4)
	if !projectHasActiveRoot(project) {
		items = append(items, ProjectHealthAttentionItem{
			ID:        projectHealthItemID(project.ID, "root"),
			ProjectID: project.ID,
			Title:     "No project root configured",
			Detail:    "Assignment starts need an active local workspace root for files, tools, and guidance discovery.",
			Status:    "stale_unknown",
			Action:    newProjectActionOpenProjectSettings(project.ID),
		})
	}
	if projectHealthMissingDefaults(project) {
		items = append(items, ProjectHealthAttentionItem{
			ID:        projectHealthItemID(project.ID, "defaults"),
			ProjectID: project.ID,
			Title:     "Provider/model defaults missing",
			Detail:    "Native project starts and assignment chats need a default provider and model.",
			Status:    "awaiting_approval",
			Action:    newProjectActionOpenProjectSettings(project.ID),
		})
	}
	items = append(items, projectHealthProfileAttentionItems(project, roles, agentProfiles)...)
	items = append(items, projectHealthSkillAttentionItems(project, roles, agentProfiles, skills)...)
	if item := projectHealthPendingHandoffAttention(project.ID, activity, workItems, handoffs); item != nil {
		items = append(items, *item)
	}
	if item := projectHealthReviewFollowUpAttention(project.ID, workItems, artifacts, handoffs); item != nil {
		items = append(items, *item)
	}
	if len(staleAssignments) > 0 {
		items = append(items, projectHealthActivityAttention(staleAssignments[0], "Stale or unknown assignment", "View blocked", "blocked"))
	}
	if item := projectHealthFailedExternalAttention(activity); item != nil {
		items = append(items, *item)
	}
	if projectHealthEnabledMemoryCount(memoryEntries) == 0 && projectHealthEnabledContextSourceCount(project) == 0 {
		items = append(items, ProjectHealthAttentionItem{
			ID:        projectHealthItemID(project.ID, "context"),
			ProjectID: project.ID,
			Title:     "No project memory or context sources enabled",
			Detail:    "Project-scoped context is empty for new chats and linked context packets.",
			Status:    "stale_unknown",
			Action:    newProjectActionOpenMemoryReview(project.ID),
		})
	}
	if candidate := firstPendingProjectHealthMemoryCandidate(memoryCandidates); candidate != nil {
		items = append(items, ProjectHealthAttentionItem{
			ID:          projectHealthItemID(candidate.ID, "memory-candidate"),
			ProjectID:   project.ID,
			Title:       "Memory candidate pending review",
			Detail:      strings.Join(projectHealthNonEmpty(candidate.Title, candidate.SuggestedTrustLabel), " - "),
			Status:      "awaiting_approval",
			Action:      newProjectActionReviewMemoryCandidate(project.ID, candidate.ID),
			CandidateID: candidate.ID,
		})
	}
	return uniqueProjectHealthAttention(items)
}

func projectHealthProfileAttentionItems(project projects.Project, roles []projectwork.AgentRoleProfile, profiles []agentprofiles.Profile) []ProjectHealthAttentionItem {
	if len(profiles) == 0 {
		return nil
	}
	profileIDs := make(map[string]struct{}, len(profiles))
	for _, profile := range profiles {
		if id := strings.TrimSpace(profile.ID); id != "" {
			profileIDs[id] = struct{}{}
		}
	}
	missing := make([]string, 0)
	missingSet := make(map[string]struct{})
	addMissing := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, ok := profileIDs[id]; ok {
			return
		}
		if _, seen := missingSet[id]; seen {
			return
		}
		missingSet[id] = struct{}{}
		missing = append(missing, id)
	}
	addMissing(project.DefaultAgentProfile)
	for _, role := range roles {
		addMissing(role.DefaultAgentProfile)
	}
	if len(missing) == 0 {
		return nil
	}
	return []ProjectHealthAttentionItem{{
		ID:        projectHealthItemID(project.ID, "profiles", "missing"),
		ProjectID: project.ID,
		Title:     "Agent profile reference missing",
		Detail:    "Project or role defaults reference " + projectHealthSummarizeIDs(missing) + ".",
		Status:    "stale_unknown",
		Action:    newProjectActionOpenProfiles(project.ID),
	}}
}

func projectHealthSkillAttentionItems(project projects.Project, roles []projectwork.AgentRoleProfile, profiles []agentprofiles.Profile, skills []projectskills.Skill) []ProjectHealthAttentionItem {
	skillsByID := make(map[string]projectskills.Skill, len(skills))
	for _, skill := range skills {
		if id := strings.TrimSpace(skill.ID); id != "" {
			skillsByID[id] = skill
		}
	}
	referenced := referencedProjectHealthSkillIDs(project, roles, profiles)
	unresolved := make([]string, 0)
	disabled := make([]string, 0)
	for _, skillID := range referenced {
		skill, ok := skillsByID[skillID]
		if !ok {
			unresolved = append(unresolved, skillID)
			continue
		}
		if !skill.Enabled {
			disabled = append(disabled, skillID)
		}
	}
	unavailable := make([]string, 0)
	referencedSet := projectHealthStringSet(referenced)
	for _, skill := range skills {
		if skill.Status == projectskills.StatusAvailable {
			continue
		}
		_, referencedSkill := referencedSet[skill.ID]
		if skill.Enabled || referencedSkill {
			unavailable = append(unavailable, skill.ID)
		}
	}
	details := make([]string, 0, 3)
	if len(unresolved) > 0 {
		details = append(details, "unresolved: "+projectHealthSummarizeIDs(unresolved))
	}
	if len(disabled) > 0 {
		details = append(details, "disabled: "+projectHealthSummarizeIDs(disabled))
	}
	if len(unavailable) > 0 {
		details = append(details, "unavailable: "+projectHealthSummarizeIDs(unavailable))
	}
	if len(details) == 0 {
		return nil
	}
	status := "stale_unknown"
	if len(disabled) > 0 {
		status = "awaiting_approval"
	}
	return []ProjectHealthAttentionItem{{
		ID:        projectHealthItemID(project.ID, "skills"),
		ProjectID: project.ID,
		Title:     "Project skills need review",
		Detail:    strings.Join(details, "; ") + ".",
		Status:    status,
		Action:    newProjectActionOpenSkills(project.ID),
	}}
}

func referencedProjectHealthSkillIDs(project projects.Project, roles []projectwork.AgentRoleProfile, profiles []agentprofiles.Profile) []string {
	referenced := make([]string, 0)
	referencedSet := make(map[string]struct{})
	relevantProfileIDs := make(map[string]struct{})
	if id := strings.TrimSpace(project.DefaultAgentProfile); id != "" {
		relevantProfileIDs[id] = struct{}{}
	}
	addSkill := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, seen := referencedSet[id]; seen {
			return
		}
		referencedSet[id] = struct{}{}
		referenced = append(referenced, id)
	}
	for _, role := range roles {
		for _, skillID := range role.SkillIDs {
			addSkill(skillID)
		}
		if id := strings.TrimSpace(role.DefaultAgentProfile); id != "" {
			relevantProfileIDs[id] = struct{}{}
		}
	}
	for _, profile := range profiles {
		if _, ok := relevantProfileIDs[strings.TrimSpace(profile.ID)]; !ok {
			continue
		}
		for _, skillID := range profile.SkillIDs {
			addSkill(skillID)
		}
	}
	return referenced
}

func projectHealthPendingHandoffAttention(projectID string, activity ProjectActivityDataResponse, workItems []projectwork.WorkItem, handoffs []projectwork.Handoff) *ProjectHealthAttentionItem {
	for _, item := range projectHealthUniqueActivityItems(activity) {
		if !projectHealthHasPendingHandoff(item) {
			continue
		}
		latestTitle := firstNonEmpty(item.HandoffSummary.LatestTitle, "Handoff awaiting operator follow-up")
		latestAt := item.HandoffSummary.LatestAt
		for _, handoff := range item.RecentHandoffs {
			if handoff.Status != projectwork.HandoffStatusPending {
				continue
			}
			latestTitle = firstNonEmpty(handoff.Title, latestTitle)
			latestAt = firstNonEmpty(handoff.UpdatedAt, handoff.CreatedAt, latestAt)
			break
		}
		detail := strings.Join(projectHealthNonEmpty(
			latestTitle,
			firstNonEmpty(item.Role.Name, item.Assignment.RoleID),
			projectHealthUpdatedAtDetail(latestAt),
		), " - ")
		return &ProjectHealthAttentionItem{
			ID:          projectHealthItemID(item.ID, "handoff"),
			ProjectID:   projectID,
			Title:       "Pending handoff: " + firstNonEmpty(item.WorkItem.Title, item.WorkItem.ID),
			Detail:      detail,
			Status:      "awaiting_approval",
			Action:      newProjectActionOpenWorkItem(projectID, item.WorkItem.ID, item.Assignment.ID, "", "recent"),
			Bucket:      "recent",
			WorkItemID:  item.WorkItem.ID,
			ActionLabel: "View recent",
		}
	}
	workByID := make(map[string]projectwork.WorkItem, len(workItems))
	for _, workItem := range workItems {
		workByID[workItem.ID] = workItem
	}
	pending := make([]projectwork.Handoff, 0, len(handoffs))
	for _, handoff := range handoffs {
		if handoff.Status == projectwork.HandoffStatusPending {
			pending = append(pending, handoff)
		}
	}
	sort.SliceStable(pending, func(i, j int) bool {
		left, right := projectworkTime(pending[i].UpdatedAt, pending[i].CreatedAt), projectworkTime(pending[j].UpdatedAt, pending[j].CreatedAt)
		if !left.Equal(right) {
			return left.After(right)
		}
		return pending[i].ID < pending[j].ID
	})
	for _, handoff := range pending {
		workItem := workByID[handoff.WorkItemID]
		detail := strings.Join(projectHealthNonEmpty(
			firstNonEmpty(handoff.Title, handoff.Summary, "Handoff awaiting operator follow-up"),
			handoff.TargetRoleID,
			projectHealthUpdatedAtDetail(formatOptionalTime(projectworkTime(handoff.UpdatedAt, handoff.CreatedAt))),
		), " - ")
		return &ProjectHealthAttentionItem{
			ID:          projectHealthItemID(handoff.ID, "handoff"),
			ProjectID:   projectID,
			Title:       "Pending handoff: " + firstNonEmpty(workItem.Title, handoff.Title, handoff.ID),
			Detail:      detail,
			Status:      "awaiting_approval",
			Action:      newProjectActionOpenWorkItem(projectID, handoff.WorkItemID, firstNonEmpty(handoff.TargetAssignmentID, handoff.SourceAssignmentID), handoff.ID, ""),
			WorkItemID:  handoff.WorkItemID,
			ActionLabel: "Open handoff",
		}
	}
	return nil
}

func projectHealthReviewFollowUpAttention(projectID string, workItems []projectwork.WorkItem, artifacts []projectwork.CollaborationArtifact, handoffs []projectwork.Handoff) *ProjectHealthAttentionItem {
	workByID := make(map[string]projectwork.WorkItem, len(workItems))
	for _, workItem := range workItems {
		workByID[workItem.ID] = workItem
	}
	handoffsByWorkItem := projectOperationsHandoffsByWorkItem(handoffs)
	artifactsByWorkItem := projectOperationsArtifactsByWorkItem(artifacts)
	items := make([]projectwork.CollaborationArtifact, 0, len(artifacts))
	for _, artifactsForWork := range artifactsByWorkItem {
		items = append(items, artifactsForWork...)
	}
	sort.SliceStable(items, func(i, j int) bool {
		left, right := projectworkTime(items[i].UpdatedAt, items[i].CreatedAt), projectworkTime(items[j].UpdatedAt, items[j].CreatedAt)
		if !left.Equal(right) {
			return left.After(right)
		}
		return items[i].ID < items[j].ID
	})
	for _, artifact := range items {
		if !projectworkapp.ReviewArtifactNeedsFollowUpPath(artifact, handoffsByWorkItem[artifact.WorkItemID]) {
			continue
		}
		workItem := workByID[artifact.WorkItemID]
		detail := strings.Join(projectHealthNonEmpty(
			firstNonEmpty(artifact.Title, artifact.ID),
			strings.ReplaceAll(artifact.ReviewVerdict, "_", " "),
			projectHealthRiskDetail(artifact.ReviewRisk),
			projectHealthReviewedAssignmentDetail(artifact.ReviewedAssignmentID),
		), " - ")
		status := "awaiting_approval"
		if artifact.ReviewVerdict == projectwork.ReviewVerdictBlocked {
			status = "blocked"
		}
		return &ProjectHealthAttentionItem{
			ID:          projectHealthItemID(artifact.ID, "review-follow-up"),
			ProjectID:   projectID,
			Title:       "Review follow-up: " + firstNonEmpty(workItem.Title, artifact.Title, artifact.ID),
			Detail:      detail,
			Status:      status,
			Action:      newProjectActionOpenWorkItem(projectID, artifact.WorkItemID, "", "", ""),
			WorkItemID:  artifact.WorkItemID,
			ActionLabel: "Open review",
		}
	}
	return nil
}

func projectHealthFailedExternalAttention(activity ProjectActivityDataResponse) *ProjectHealthAttentionItem {
	for _, item := range projectHealthUniqueActivityItems(activity) {
		if item.Assignment.DriverKind != projectwork.AssignmentDriverExternalAgent {
			continue
		}
		if item.BlockingSignal != "failed" && item.BlockingSignal != "cancelled" {
			continue
		}
		attention := projectHealthActivityAttention(item, "External assignment needs review", "View blocked", "blocked")
		return &attention
	}
	return nil
}

func projectHealthActivityAttention(item ProjectActivityItemResponse, title, actionLabel, bucket string) ProjectHealthAttentionItem {
	taskID, runID, chatID := projectHealthExecutionRefs(item)
	return ProjectHealthAttentionItem{
		ID:          item.ID,
		ProjectID:   item.ProjectID,
		Title:       title + ": " + firstNonEmpty(item.WorkItem.Title, item.Assignment.ID),
		Detail:      strings.Join(projectHealthNonEmpty(item.StatusSummary, firstNonEmpty(item.Role.Name, item.Assignment.RoleID), projectHealthUpdatedAtDetail(item.UpdatedAt)), " - "),
		Status:      firstNonEmpty(item.BlockingSignal, item.Status),
		Action:      newProjectActionOpenWorkItem(item.ProjectID, item.WorkItem.ID, item.Assignment.ID, "", bucket),
		Bucket:      bucket,
		WorkItemID:  item.WorkItem.ID,
		TaskID:      taskID,
		RunID:       runID,
		ChatID:      chatID,
		ActionLabel: actionLabel,
	}
}

func projectHealthStaleAssignments(project projects.Project, activity ProjectActivityDataResponse, workItems []projectwork.WorkItem, assignments []projectwork.Assignment, now time.Time) []ProjectActivityItemResponse {
	items := make([]ProjectActivityItemResponse, 0)
	for _, item := range projectHealthUniqueActivityItems(activity) {
		if item.BlockingSignal == "stale_unknown" || projectHealthAssignmentExecutionMissing(item) {
			items = append(items, item)
		}
	}
	workByID := make(map[string]projectwork.WorkItem, len(workItems))
	for _, workItem := range workItems {
		workByID[workItem.ID] = workItem
	}
	for _, assignment := range assignments {
		status := projectworkapp.AssignmentReadinessStatus(assignment)
		if !projectHealthAssignmentIsStale(assignment, status, now) {
			continue
		}
		workItem, ok := workByID[assignment.WorkItemID]
		if !ok {
			continue
		}
		items = append(items, projectHealthProjectedStaleAssignment(project, workItem, assignment))
	}
	return uniqueProjectHealthActivityItems(items)
}

func projectHealthProjectedStaleAssignment(project projects.Project, workItem projectwork.WorkItem, assignment projectwork.Assignment) ProjectActivityItemResponse {
	renderedAssignment := renderProjectWorkAssignment(assignment)
	taskID, runID, chatID := "", "", ""
	if renderedAssignment.ExecutionRef != nil {
		taskID = renderedAssignment.ExecutionRef.TaskID
		runID = renderedAssignment.ExecutionRef.RunID
		chatID = renderedAssignment.ExecutionRef.ChatSessionID
	}
	return ProjectActivityItemResponse{
		ID:             assignment.ID,
		ProjectID:      project.ID,
		WorkItem:       renderProjectActivityWorkItem(renderProjectWorkItem(workItem)),
		Assignment:     renderedAssignment,
		Role:           ProjectWorkRoleResponse{ID: assignment.RoleID, ProjectID: project.ID, Name: assignment.RoleID},
		Status:         projectworkapp.AssignmentReadinessStatus(assignment),
		BlockingSignal: "stale_unknown",
		StatusSummary:  "active assignment has not changed recently",
		LinkedTaskID:   taskID,
		LinkedRunID:    runID,
		LinkedChatID:   chatID,
		UpdatedAt:      formatOptionalTime(projectworkTime(assignment.UpdatedAt, assignment.StartedAt, assignment.CreatedAt)),
	}
}

func projectHealthUniqueActivityItems(activity ProjectActivityDataResponse) []ProjectActivityItemResponse {
	items := make([]ProjectActivityItemResponse, 0, len(activity.Buckets.Blocked)+len(activity.Buckets.Active)+len(activity.Buckets.Completed)+len(activity.Buckets.Recent)+len(activity.Recent))
	items = append(items, activity.Buckets.Blocked...)
	items = append(items, activity.Buckets.Active...)
	items = append(items, activity.Buckets.Completed...)
	items = append(items, activity.Buckets.Recent...)
	items = append(items, activity.Recent...)
	return uniqueProjectHealthActivityItems(items)
}

func uniqueProjectHealthActivityItems(items []ProjectActivityItemResponse) []ProjectActivityItemResponse {
	seen := make(map[string]struct{}, len(items))
	out := make([]ProjectActivityItemResponse, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.ID) == "" {
			continue
		}
		if _, ok := seen[item.ID]; ok {
			continue
		}
		seen[item.ID] = struct{}{}
		out = append(out, item)
	}
	return out
}

func uniqueProjectHealthAttention(items []ProjectHealthAttentionItem) []ProjectHealthAttentionItem {
	seen := make(map[string]struct{}, len(items))
	out := make([]ProjectHealthAttentionItem, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.ID) == "" {
			continue
		}
		if _, ok := seen[item.ID]; ok {
			continue
		}
		seen[item.ID] = struct{}{}
		out = append(out, item)
	}
	return out
}

func boundedProjectHealthAttention(items []ProjectHealthAttentionItem, limit int) []ProjectHealthAttentionItem {
	if len(items) == 0 {
		return []ProjectHealthAttentionItem{}
	}
	if limit > 0 && len(items) > limit {
		return append([]ProjectHealthAttentionItem(nil), items[:limit]...)
	}
	return append([]ProjectHealthAttentionItem(nil), items...)
}

func projectHealthMissingDefaults(project projects.Project) bool {
	return strings.TrimSpace(project.DefaultProvider) == "" || strings.TrimSpace(project.DefaultModel) == ""
}

func projectHealthEnabledMemoryCount(entries []memory.Entry) int {
	count := 0
	for _, entry := range entries {
		if entry.Enabled {
			count++
		}
	}
	return count
}

func projectHealthEnabledContextSourceCount(project projects.Project) int {
	count := 0
	for _, source := range project.ContextSources {
		if source.Enabled {
			count++
		}
	}
	return count
}

func firstPendingProjectHealthMemoryCandidate(candidates []memory.Candidate) *memory.Candidate {
	for idx := range candidates {
		if candidates[idx].Status == memory.CandidateStatusPending {
			return &candidates[idx]
		}
	}
	return nil
}

func projectHealthHasPendingHandoff(item ProjectActivityItemResponse) bool {
	if item.HandoffSummary.PendingCount > 0 {
		return true
	}
	for _, handoff := range item.RecentHandoffs {
		if handoff.Status == projectwork.HandoffStatusPending {
			return true
		}
	}
	return false
}

func projectHealthAssignmentExecutionMissing(item ProjectActivityItemResponse) bool {
	if item.Assignment.ExecutionRef != nil && item.Assignment.ExecutionRef.Missing {
		return true
	}
	if item.Assignment.Execution != nil && item.Assignment.Execution.Missing {
		return true
	}
	return false
}

func projectHealthAssignmentIsStale(assignment projectwork.Assignment, status string, now time.Time) bool {
	if !projectworkapp.IsActiveAssignmentStatus(status) {
		return false
	}
	updated := projectworkTime(assignment.UpdatedAt, assignment.StartedAt)
	if updated.IsZero() {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return now.Sub(updated.UTC()) > 24*time.Hour
}

func projectHealthExecutionRefs(item ProjectActivityItemResponse) (string, string, string) {
	taskID := firstNonEmpty(item.LinkedTaskID)
	runID := firstNonEmpty(item.LinkedRunID)
	chatID := firstNonEmpty(item.LinkedChatID)
	if item.Assignment.ExecutionRef != nil {
		taskID = firstNonEmpty(taskID, item.Assignment.ExecutionRef.TaskID)
		runID = firstNonEmpty(runID, item.Assignment.ExecutionRef.RunID)
		chatID = firstNonEmpty(chatID, item.Assignment.ExecutionRef.ChatSessionID)
	}
	if item.Assignment.Execution != nil {
		taskID = firstNonEmpty(taskID, item.Assignment.Execution.TaskID)
		runID = firstNonEmpty(runID, item.Assignment.Execution.RunID)
	}
	return taskID, runID, chatID
}

func projectHealthUpdatedAtDetail(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return "updated " + strings.TrimSpace(value)
}

func projectHealthRiskDetail(risk string) string {
	risk = strings.TrimSpace(risk)
	if risk == "" {
		return ""
	}
	return "risk " + risk
}

func projectHealthReviewedAssignmentDetail(assignmentID string) string {
	assignmentID = strings.TrimSpace(assignmentID)
	if assignmentID == "" {
		return ""
	}
	return "reviewed " + assignmentID
}

func projectHealthNonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func projectHealthSummarizeIDs(ids []string) string {
	unique := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	shown := unique
	if len(shown) > 3 {
		shown = shown[:3]
	}
	if len(unique) <= 3 {
		return strings.Join(shown, ", ")
	}
	return strings.Join(shown, ", ") + ", and " + intString(len(unique)-3) + " more"
}

func projectHealthStringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			set[value] = struct{}{}
		}
	}
	return set
}

func projectHealthItemID(parts ...string) string {
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			values = append(values, part)
		}
	}
	return strings.Join(values, ":")
}
