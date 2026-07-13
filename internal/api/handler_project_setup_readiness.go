package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
)

const (
	projectSetupReadinessStatusReady    = "ready"
	projectSetupReadinessStatusTodo     = "todo"
	projectSetupReadinessStatusOptional = "optional"

	projectSetupReadinessActionBootstrap       = "bootstrap_project"
	projectSetupReadinessActionCreateWorkItem  = "create_work_item"
	projectSetupReadinessActionProjectSettings = "open_project_settings"
)

type ProjectSetupReadinessEnvelope struct {
	Object string                        `json:"object"`
	Data   ProjectSetupReadinessResponse `json:"data"`
}

type ProjectSetupReadinessResponse struct {
	ProjectID      string                               `json:"project_id"`
	GeneratedAt    string                               `json:"generated_at"`
	ReadBackend    string                               `json:"read_backend,omitempty"`
	ShowOnboarding bool                                 `json:"show_onboarding"`
	SetupStarted   bool                                 `json:"setup_started"`
	FirstWorkReady bool                                 `json:"first_work_ready"`
	Summary        ProjectSetupReadinessSummaryResponse `json:"summary"`
	PrimaryAction  ProjectSetupReadinessActionResponse  `json:"primary_action"`
	Checks         []ProjectSetupReadinessCheckResponse `json:"checks"`
}

type ProjectSetupReadinessSummaryResponse struct {
	WorkItemCount               int  `json:"work_item_count"`
	RoleCount                   int  `json:"role_count"`
	SkillCount                  int  `json:"skill_count"`
	EnabledContextSourceCount   int  `json:"enabled_context_source_count"`
	SavedMemoryCount            int  `json:"saved_memory_count"`
	PendingMemoryCandidateCount int  `json:"pending_memory_candidate_count"`
	HasPurpose                  bool `json:"has_purpose"`
	HasActiveRoot               bool `json:"has_active_root"`
	MissingDefaults             bool `json:"missing_defaults"`
}

type ProjectSetupReadinessCheckResponse struct {
	ID       string                               `json:"id"`
	Label    string                               `json:"label"`
	Detail   string                               `json:"detail"`
	Status   string                               `json:"status"`
	Optional bool                                 `json:"optional,omitempty"`
	Action   *ProjectSetupReadinessActionResponse `json:"action,omitempty"`
}

type ProjectSetupReadinessActionResponse struct {
	Type      string `json:"type"`
	ProjectID string `json:"project_id"`
	Label     string `json:"label"`
}

func (h *Handler) HandleProjectSetupReadiness(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	strictEmbeddedRead := h.projectReadRoutesUseCairnlineReadModel() && h.requiresEmbeddedCairnlineProjectReads()
	if !strictEmbeddedRead && !h.requireProject(w, r, projectID) {
		return
	}
	readiness, err := h.renderProjectSetupReadiness(r.Context(), projectID)
	if err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
			return
		}
		writeProjectReadRenderError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, ProjectSetupReadinessEnvelope{Object: "project_setup_readiness", Data: readiness})
}

func (h *Handler) renderProjectSetupReadiness(ctx context.Context, projectID string) (ProjectSetupReadinessResponse, error) {
	if h.projectReadRoutesUseCairnlineReadModel() {
		return h.renderCairnlineProjectSetupReadiness(ctx, projectID)
	}
	project, ok, err := h.projects.Get(ctx, projectID)
	if err != nil {
		return ProjectSetupReadinessResponse{}, err
	}
	if !ok {
		return ProjectSetupReadinessResponse{}, projects.ErrNotFound
	}
	roles, err := h.projectWork.ListRoles(ctx, projectID)
	if err != nil {
		return ProjectSetupReadinessResponse{}, err
	}
	workItems, err := h.projectWork.ListWorkItems(ctx, projectID)
	if err != nil {
		return ProjectSetupReadinessResponse{}, err
	}
	entries, err := h.projectSetupReadinessMemoryEntries(ctx, projectID)
	if err != nil {
		return ProjectSetupReadinessResponse{}, err
	}
	candidates, err := h.projectSetupReadinessMemoryCandidates(ctx, projectID)
	if err != nil {
		return ProjectSetupReadinessResponse{}, err
	}
	skills, err := h.projectSetupReadinessSkills(ctx, projectID)
	if err != nil {
		return ProjectSetupReadinessResponse{}, err
	}

	summary := projectSetupReadinessSummary(project, workItems, roles, skills, entries, candidates)
	setupStarted := projectSetupReadinessStarted(summary)
	showOnboarding := summary.WorkItemCount == 0 && !setupStarted
	firstWorkReady := summary.WorkItemCount == 0 && setupStarted
	return ProjectSetupReadinessResponse{
		ProjectID:      project.ID,
		GeneratedAt:    formatOptionalTime(time.Now().UTC()),
		ReadBackend:    "hecate",
		ShowOnboarding: showOnboarding,
		SetupStarted:   setupStarted,
		FirstWorkReady: firstWorkReady,
		Summary:        summary,
		PrimaryAction:  projectSetupReadinessPrimaryAction(project.ID, summary, showOnboarding, firstWorkReady),
		Checks:         projectSetupReadinessChecks(project, summary),
	}, nil
}

func projectSetupReadinessStarted(summary ProjectSetupReadinessSummaryResponse) bool {
	return summary.EnabledContextSourceCount > 0 ||
		summary.RoleCount > 0 ||
		summary.SkillCount > 0 ||
		summary.SavedMemoryCount > 0 ||
		summary.PendingMemoryCandidateCount > 0
}

func projectSetupReadinessPrimaryAction(projectID string, summary ProjectSetupReadinessSummaryResponse, showOnboarding, firstWorkReady bool) ProjectSetupReadinessActionResponse {
	if firstWorkReady || (showOnboarding && !summary.HasActiveRoot) {
		return projectSetupReadinessAction(projectSetupReadinessActionCreateWorkItem, projectID, "Create first work")
	}
	return projectSetupReadinessAction(projectSetupReadinessActionBootstrap, projectID, "Set up project")
}

func (h *Handler) projectSetupReadinessMemoryEntries(ctx context.Context, projectID string) ([]memory.Entry, error) {
	if h.memory == nil {
		return nil, nil
	}
	return h.memory.List(ctx, memory.Filter{ProjectID: strings.TrimSpace(projectID), IncludeDisabled: true})
}

func (h *Handler) projectSetupReadinessMemoryCandidates(ctx context.Context, projectID string) ([]memory.Candidate, error) {
	if h.memoryCandidates == nil {
		return nil, nil
	}
	return h.memoryCandidates.ListCandidates(ctx, memory.CandidateFilter{ProjectID: strings.TrimSpace(projectID), Status: memory.CandidateStatusPending})
}

func (h *Handler) projectSetupReadinessSkills(ctx context.Context, projectID string) ([]projectskills.Skill, error) {
	if h.projectSkills == nil {
		return nil, nil
	}
	return h.projectSkills.List(ctx, projectID)
}

func projectSetupReadinessSummary(project projects.Project, workItems []projectwork.WorkItem, roles []projectwork.AgentRoleProfile, skills []projectskills.Skill, entries []memory.Entry, candidates []memory.Candidate) ProjectSetupReadinessSummaryResponse {
	summary := ProjectSetupReadinessSummaryResponse{
		WorkItemCount:   len(workItems),
		RoleCount:       len(projectSetupCustomRoles(roles)),
		SkillCount:      len(skills),
		HasPurpose:      strings.TrimSpace(project.Description) != "",
		HasActiveRoot:   projectHasActiveRoot(project),
		MissingDefaults: projectHealthMissingDefaults(project),
	}
	for _, source := range project.ContextSources {
		if source.Enabled {
			summary.EnabledContextSourceCount++
		}
	}
	summary.SavedMemoryCount = len(entries)
	summary.PendingMemoryCandidateCount = len(candidates)
	return summary
}

func projectSetupCustomRoles(roles []projectwork.AgentRoleProfile) []projectwork.AgentRoleProfile {
	items := make([]projectwork.AgentRoleProfile, 0, len(roles))
	for _, role := range roles {
		if role.BuiltIn || projectwork.IsBuiltInRoleID(role.ID) {
			continue
		}
		items = append(items, role)
	}
	return items
}

func projectSetupReadinessChecks(project projects.Project, summary ProjectSetupReadinessSummaryResponse) []ProjectSetupReadinessCheckResponse {
	projectID := project.ID
	hasGuidance := summary.EnabledContextSourceCount > 0 || summary.SkillCount > 0 || summary.SavedMemoryCount > 0 || summary.PendingMemoryCandidateCount > 0
	return []ProjectSetupReadinessCheckResponse{
		projectSetupCheck("purpose", "Project purpose", strings.TrimSpace(project.Description), summary.HasPurpose, false, projectSetupReadinessAction(projectSetupReadinessActionProjectSettings, projectID, "Add purpose"), "Add a short purpose."),
		projectSetupWorkspaceCheck(project),
		projectSetupCheck("launch_defaults", "Provider and model", projectSetupDefaultsDetail(project, summary), !summary.MissingDefaults, false, projectSetupReadinessAction(projectSetupReadinessActionProjectSettings, projectID, "Set defaults"), "Not set"),
		projectSetupCheck("sources_memory", "Sources and memory", projectSetupGuidanceDetail(summary, project), hasGuidance, false, projectSetupReadinessAction(projectSetupReadinessActionBootstrap, projectID, "Set up project"), ""),
		projectSetupCheck("roles", "Roles", projectSetupRoleDetail(summary), summary.RoleCount > 0, false, projectSetupReadinessAction(projectSetupReadinessActionBootstrap, projectID, "Set up project"), ""),
		projectSetupCheck("first_work_item", "First work item", projectSetupFirstWorkDetail(summary), summary.WorkItemCount > 0, false, projectSetupReadinessAction(projectSetupReadinessActionCreateWorkItem, projectID, "Create work"), ""),
	}
}

func projectSetupCheck(id, label, detail string, done bool, optional bool, action ProjectSetupReadinessActionResponse, fallbackDetail string) ProjectSetupReadinessCheckResponse {
	status := projectSetupReadinessStatusTodo
	if optional {
		status = projectSetupReadinessStatusOptional
	} else if done {
		status = projectSetupReadinessStatusReady
	}
	check := ProjectSetupReadinessCheckResponse{
		ID:       id,
		Label:    label,
		Detail:   strings.TrimSpace(detail),
		Status:   status,
		Optional: optional,
	}
	if check.Detail == "" {
		check.Detail = fallbackDetail
	}
	if !done && !optional {
		check.Action = &action
	}
	return check
}

func projectSetupWorkspaceCheck(project projects.Project) ProjectSetupReadinessCheckResponse {
	if root, ok := projectSetupActiveRoot(project); ok {
		return ProjectSetupReadinessCheckResponse{
			ID:     "workspace_source",
			Label:  "Workspace source",
			Detail: root.Path,
			Status: projectSetupReadinessStatusReady,
		}
	}
	return ProjectSetupReadinessCheckResponse{
		ID:       "workspace_source",
		Label:    "Workspace source",
		Detail:   "Optional; attach files when this project needs them.",
		Status:   projectSetupReadinessStatusOptional,
		Optional: true,
	}
}

func projectSetupActiveRoot(project projects.Project) (projects.Root, bool) {
	for _, root := range project.Roots {
		if root.Active && strings.TrimSpace(root.Path) != "" {
			return root, true
		}
	}
	return projects.Root{}, false
}

func projectSetupDefaultsDetail(project projects.Project, summary ProjectSetupReadinessSummaryResponse) string {
	if summary.MissingDefaults {
		return "Not set"
	}
	return strings.TrimSpace(project.DefaultProvider) + " / " + strings.TrimSpace(project.DefaultModel)
}

func projectSetupGuidanceDetail(summary ProjectSetupReadinessSummaryResponse, project projects.Project) string {
	parts := make([]string, 0, 4)
	if summary.EnabledContextSourceCount > 0 {
		parts = append(parts, projectSetupCountLabel(summary.EnabledContextSourceCount, "source"))
	}
	if summary.SkillCount > 0 {
		parts = append(parts, projectSetupCountLabel(summary.SkillCount, "skill"))
	}
	if summary.SavedMemoryCount > 0 {
		parts = append(parts, projectSetupCountLabel(summary.SavedMemoryCount, "memory"))
	}
	if summary.PendingMemoryCandidateCount > 0 {
		parts = append(parts, projectSetupCountLabel(summary.PendingMemoryCandidateCount, "candidate"))
	}
	if len(parts) > 0 {
		return strings.Join(parts, " · ")
	}
	if projectHasActiveRoot(project) {
		return "Set up project can discover workspace guidance and local skills."
	}
	return "Attach a workspace when files matter, or add sources later."
}

func projectSetupRoleDetail(summary ProjectSetupReadinessSummaryResponse) string {
	if summary.RoleCount > 0 {
		return projectSetupCountLabel(summary.RoleCount, "role")
	}
	return "Set up project can suggest roles from skills."
}

func projectSetupFirstWorkDetail(summary ProjectSetupReadinessSummaryResponse) string {
	if summary.WorkItemCount > 0 {
		return projectSetupCountLabel(summary.WorkItemCount, "work item")
	}
	return "Create the first reviewable work item."
}

func projectSetupCountLabel(count int, singular string) string {
	if count == 1 {
		return "1 " + singular
	}
	return intString(count) + " " + singular + "s"
}

func projectSetupReadinessAction(actionType, projectID, label string) ProjectSetupReadinessActionResponse {
	return ProjectSetupReadinessActionResponse{
		Type:      actionType,
		ProjectID: projectID,
		Label:     label,
	}
}
