package projectassistant

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
)

const (
	contextAssignmentLimit       = 12
	contextMemoryLimit           = 12
	contextMemoryCandidateLimit  = 12
	contextActivityLimit         = 16
	contextMemoryBodyMaxBytes    = 4096
	contextCandidateBodyMaxBytes = 2048
	contextTruncatedSuffix       = "\n...[truncated]"
)

type DraftContext struct {
	Project          ProjectContext           `json:"project"`
	Request          string                   `json:"request"`
	SelectedWork     *WorkItemContext         `json:"selected_work,omitempty"`
	Roles            []RoleContext            `json:"roles"`
	Skills           []ProjectSkillContext    `json:"skills,omitempty"`
	Assignments      []AssignmentContext      `json:"assignments,omitempty"`
	Memory           []MemoryContext          `json:"memory,omitempty"`
	MemoryCandidates []MemoryCandidateContext `json:"memory_candidates,omitempty"`
	RecentActivity   []ActivityContext        `json:"recent_activity,omitempty"`
	Budget           ContextBudget            `json:"budget"`
	Selection        DraftSelection           `json:"selection"`
}

type ContextBudget struct {
	MemoryBodyMaxBytes          int `json:"memory_body_max_bytes"`
	MemoryCandidateBodyMaxBytes int `json:"memory_candidate_body_max_bytes"`
	BodyOriginalBytes           int `json:"body_original_bytes"`
	BodyReturnedBytes           int `json:"body_returned_bytes"`
	BodyTokensEstimate          int `json:"body_tokens_estimate"`
	BodyTruncatedCount          int `json:"body_truncated_count"`
}

type ProjectContext struct {
	ID                   string               `json:"id"`
	Name                 string               `json:"name"`
	Description          string               `json:"description,omitempty"`
	Roots                []ProjectRootContext `json:"roots,omitempty"`
	ContextSources       []ContextSource      `json:"context_sources,omitempty"`
	DefaultRootID        string               `json:"default_root_id,omitempty"`
	DefaultProvider      string               `json:"default_provider,omitempty"`
	DefaultModel         string               `json:"default_model,omitempty"`
	DefaultAgentProfile  string               `json:"default_agent_profile,omitempty"`
	DefaultWorkspaceMode string               `json:"default_workspace_mode,omitempty"`
	CreatedAt            time.Time            `json:"created_at"`
	UpdatedAt            time.Time            `json:"updated_at"`
}

type ProjectRootContext struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	GitRemote string `json:"git_remote,omitempty"`
	GitBranch string `json:"git_branch,omitempty"`
	Active    bool   `json:"active"`
}

type ContextSource struct {
	ID             string            `json:"id"`
	Kind           string            `json:"kind"`
	Title          string            `json:"title,omitempty"`
	Path           string            `json:"path"`
	Enabled        bool              `json:"enabled"`
	Format         string            `json:"format,omitempty"`
	Scope          string            `json:"scope,omitempty"`
	TrustLabel     string            `json:"trust_label,omitempty"`
	SourceCategory string            `json:"source_category,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

type WorkItemContext struct {
	ID              string    `json:"id"`
	Title           string    `json:"title"`
	Brief           string    `json:"brief,omitempty"`
	Status          string    `json:"status"`
	Priority        string    `json:"priority,omitempty"`
	OwnerRoleID     string    `json:"owner_role_id,omitempty"`
	ReviewerRoleIDs []string  `json:"reviewer_role_ids,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type RoleContext struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	Description         string    `json:"description,omitempty"`
	DefaultDriverKind   string    `json:"default_driver_kind,omitempty"`
	DefaultProvider     string    `json:"default_provider,omitempty"`
	DefaultModel        string    `json:"default_model,omitempty"`
	DefaultAgentProfile string    `json:"default_agent_profile,omitempty"`
	SkillIDs            []string  `json:"skill_ids,omitempty"`
	BuiltIn             bool      `json:"built_in"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type ProjectSkillContext struct {
	ID                     string    `json:"id"`
	Title                  string    `json:"title"`
	Description            string    `json:"description,omitempty"`
	Path                   string    `json:"path"`
	RootID                 string    `json:"root_id,omitempty"`
	Format                 string    `json:"format"`
	Enabled                bool      `json:"enabled"`
	Status                 string    `json:"status"`
	TrustLabel             string    `json:"trust_label"`
	SourceContextSourceIDs []string  `json:"source_context_source_ids,omitempty"`
	Warnings               []string  `json:"warnings,omitempty"`
	DiscoveredAt           time.Time `json:"discovered_at"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

type AssignmentContext struct {
	ID           string                             `json:"id"`
	WorkItemID   string                             `json:"work_item_id"`
	RoleID       string                             `json:"role_id"`
	DriverKind   string                             `json:"driver_kind"`
	Status       string                             `json:"status"`
	ExecutionRef projectwork.AssignmentExecutionRef `json:"execution_ref,omitempty"`
	CreatedAt    time.Time                          `json:"created_at"`
	UpdatedAt    time.Time                          `json:"updated_at"`
	StartedAt    *time.Time                         `json:"started_at,omitempty"`
	CompletedAt  *time.Time                         `json:"completed_at,omitempty"`
}

type MemoryContext struct {
	ID                 string    `json:"id"`
	Title              string    `json:"title"`
	Body               string    `json:"body"`
	BodyOriginalBytes  int       `json:"body_original_bytes"`
	BodyReturnedBytes  int       `json:"body_returned_bytes"`
	BodyTokensEstimate int       `json:"body_tokens_estimate"`
	BodyTruncated      bool      `json:"body_truncated"`
	TrustLabel         string    `json:"trust_label"`
	SourceKind         string    `json:"source_kind"`
	SourceID           string    `json:"source_id,omitempty"`
	Enabled            bool      `json:"enabled"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type MemoryCandidateContext struct {
	ID                  string                      `json:"id"`
	Title               string                      `json:"title"`
	Body                string                      `json:"body"`
	BodyOriginalBytes   int                         `json:"body_original_bytes"`
	BodyReturnedBytes   int                         `json:"body_returned_bytes"`
	BodyTokensEstimate  int                         `json:"body_tokens_estimate"`
	BodyTruncated       bool                        `json:"body_truncated"`
	SuggestedKind       string                      `json:"suggested_kind,omitempty"`
	SuggestedTrustLabel string                      `json:"suggested_trust_label,omitempty"`
	SuggestedSourceKind string                      `json:"suggested_source_kind,omitempty"`
	SuggestedSourceID   string                      `json:"suggested_source_id,omitempty"`
	SourceRefs          []memory.CandidateSourceRef `json:"source_refs,omitempty"`
	Status              string                      `json:"status"`
	StatusReason        string                      `json:"status_reason,omitempty"`
	PromotedMemoryID    string                      `json:"promoted_memory_id,omitempty"`
	CreatedAt           time.Time                   `json:"created_at"`
	UpdatedAt           time.Time                   `json:"updated_at"`
}

type ActivityContext struct {
	Kind      string    `json:"kind"`
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Status    string    `json:"status,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

type DraftSelection struct {
	RoleID       string `json:"role_id,omitempty"`
	RoleName     string `json:"role_name,omitempty"`
	RoleSource   string `json:"role_source,omitempty"`
	DriverKind   string `json:"driver_kind"`
	DriverSource string `json:"driver_source"`
	Reason       string `json:"reason"`
}

func (s *Service) Context(ctx context.Context, input ContextInput) (DraftContext, error) {
	if s == nil || s.projects == nil || s.work == nil {
		return DraftContext{}, ErrStoreNotConfigured
	}
	projectID := strings.TrimSpace(input.ProjectID)
	if projectID == "" {
		return DraftContext{}, fmt.Errorf("%w: project_id is required", ErrInvalid)
	}
	project, ok, err := s.projects.Get(ctx, projectID)
	if err != nil {
		return DraftContext{}, err
	}
	if !ok {
		return DraftContext{}, fmt.Errorf("%w: project %q", ErrNotFound, projectID)
	}
	roles, err := s.work.ListRoles(ctx, project.ID)
	if err != nil {
		return DraftContext{}, err
	}
	workItem, err := s.contextWorkItem(ctx, project.ID, input.WorkItemID)
	if err != nil {
		return DraftContext{}, err
	}
	selection, err := draftSelection(input, roles, workItem)
	if err != nil {
		return DraftContext{}, err
	}
	if workItem != nil && selection.RoleID == "" {
		return DraftContext{}, fmt.Errorf("%w: role_id is required for assignment drafts", ErrInvalid)
	}
	assignments, err := s.contextAssignments(ctx, project.ID)
	if err != nil {
		return DraftContext{}, err
	}
	skills, err := s.contextProjectSkills(ctx, project.ID)
	if err != nil {
		return DraftContext{}, err
	}
	memoryItems, err := s.contextMemory(ctx, project.ID)
	if err != nil {
		return DraftContext{}, err
	}
	candidates, err := s.contextMemoryCandidates(ctx, project.ID)
	if err != nil {
		return DraftContext{}, err
	}
	return DraftContext{
		Project:          projectContext(project),
		Request:          strings.TrimSpace(input.Request),
		SelectedWork:     workItemContextPtr(workItem),
		Roles:            roleContexts(roles),
		Skills:           skills,
		Assignments:      assignments,
		Memory:           memoryItems,
		MemoryCandidates: candidates,
		RecentActivity:   recentActivity(workItem, assignments, memoryItems, candidates),
		Budget:           contextBudget(memoryItems, candidates),
		Selection:        selection,
	}, nil
}

func (s *Service) contextWorkItem(ctx context.Context, projectID, workItemID string) (*projectwork.WorkItem, error) {
	workItemID = strings.TrimSpace(workItemID)
	if workItemID == "" {
		return nil, nil
	}
	item, found, err := s.work.GetWorkItem(ctx, projectID, workItemID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%w: project work item %q", ErrNotFound, workItemID)
	}
	return &item, nil
}

func (s *Service) contextProjectSkills(ctx context.Context, projectID string) ([]ProjectSkillContext, error) {
	if s.projectSkills == nil {
		return nil, nil
	}
	items, err := s.projectSkills.List(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out := make([]ProjectSkillContext, 0, len(items))
	for _, item := range items {
		out = append(out, projectSkillContext(item))
	}
	return out, nil
}

func (s *Service) contextAssignments(ctx context.Context, projectID string) ([]AssignmentContext, error) {
	items, err := s.work.ListAssignments(ctx, projectwork.AssignmentFilter{ProjectID: projectID})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	if len(items) > contextAssignmentLimit {
		items = items[:contextAssignmentLimit]
	}
	out := make([]AssignmentContext, 0, len(items))
	for _, item := range items {
		out = append(out, assignmentContext(item))
	}
	return out, nil
}

func (s *Service) contextMemory(ctx context.Context, projectID string) ([]MemoryContext, error) {
	if s.memory == nil {
		return nil, nil
	}
	items, err := s.memory.List(ctx, memory.Filter{ProjectID: projectID})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	if len(items) > contextMemoryLimit {
		items = items[:contextMemoryLimit]
	}
	out := make([]MemoryContext, 0, len(items))
	for _, item := range items {
		out = append(out, memoryContext(item))
	}
	return out, nil
}

func (s *Service) contextMemoryCandidates(ctx context.Context, projectID string) ([]MemoryCandidateContext, error) {
	if s.memoryCandidates == nil {
		return nil, nil
	}
	items, err := s.memoryCandidates.ListCandidates(ctx, memory.CandidateFilter{
		ProjectID: projectID,
		Status:    memory.CandidateStatusPending,
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	if len(items) > contextMemoryCandidateLimit {
		items = items[:contextMemoryCandidateLimit]
	}
	out := make([]MemoryCandidateContext, 0, len(items))
	for _, item := range items {
		out = append(out, memoryCandidateContext(item))
	}
	return out, nil
}

func draftSelection(input ContextInput, roles []projectwork.AgentRoleProfile, workItem *projectwork.WorkItem) (DraftSelection, error) {
	role, roleSource, roleReason := selectDraftRole(strings.TrimSpace(input.RoleID), roles, workItem)
	if strings.TrimSpace(input.RoleID) != "" && role == nil {
		return DraftSelection{}, fmt.Errorf("%w: role %q", ErrNotFound, strings.TrimSpace(input.RoleID))
	}
	driverKind, driverSource, driverReason := selectDraftDriver(strings.TrimSpace(input.DriverKind), role)
	if !validDraftDriverKind(driverKind) {
		return DraftSelection{}, fmt.Errorf("%w: unsupported assignment driver_kind %q", ErrInvalid, driverKind)
	}
	selection := DraftSelection{
		DriverKind:   driverKind,
		DriverSource: driverSource,
		Reason:       strings.Join(nonEmptyStrings(roleReason, driverReason), " "),
	}
	if role != nil {
		selection.RoleID = role.ID
		selection.RoleName = role.Name
		selection.RoleSource = roleSource
	}
	return selection, nil
}

func selectDraftRole(roleID string, roles []projectwork.AgentRoleProfile, workItem *projectwork.WorkItem) (*projectwork.AgentRoleProfile, string, string) {
	if roleID != "" {
		if role := findDraftRole(roleID, roles); role != nil {
			return role, "explicit", fmt.Sprintf("Operator selected role %s.", roleDisplayName(*role))
		}
		return nil, "", ""
	}
	if workItem != nil && strings.TrimSpace(workItem.OwnerRoleID) != "" {
		if role := findDraftRole(workItem.OwnerRoleID, roles); role != nil {
			return role, "selected_work_owner", fmt.Sprintf("Selected work item is owned by %s.", roleDisplayName(*role))
		}
		if len(roles) > 0 {
			role := roles[0]
			return &role, "first_role", fmt.Sprintf("Selected work item owner role %q is not loaded; using first project role %s.", workItem.OwnerRoleID, roleDisplayName(role))
		}
		return nil, "", fmt.Sprintf("Selected work item owner role %q is not loaded and no project roles are available.", workItem.OwnerRoleID)
	}
	if len(roles) > 0 {
		role := roles[0]
		return &role, "first_role", fmt.Sprintf("No selected work owner role; using first project role %s.", roleDisplayName(role))
	}
	return nil, "", "No project roles are available; new work item drafts will be unowned."
}

func selectDraftDriver(driverKind string, role *projectwork.AgentRoleProfile) (string, string, string) {
	if driverKind != "" {
		return driverKind, "explicit", fmt.Sprintf("Operator selected driver %s.", driverKind)
	}
	if role != nil && strings.TrimSpace(role.DefaultDriverKind) != "" {
		driver := strings.TrimSpace(role.DefaultDriverKind)
		return driver, "role_default", fmt.Sprintf("Using %s from the selected role default.", driver)
	}
	return projectwork.AssignmentDriverHecateTask, "fallback", "No driver hint was set; using hecate_task."
}

func findDraftRole(roleID string, roles []projectwork.AgentRoleProfile) *projectwork.AgentRoleProfile {
	roleID = strings.TrimSpace(roleID)
	for _, role := range roles {
		if role.ID == roleID {
			found := role
			return &found
		}
	}
	return nil
}

func projectContext(project projects.Project) ProjectContext {
	return ProjectContext{
		ID:                   project.ID,
		Name:                 project.Name,
		Description:          project.Description,
		Roots:                projectRootContexts(project.Roots),
		ContextSources:       contextSources(project.ContextSources),
		DefaultRootID:        project.DefaultRootID,
		DefaultProvider:      project.DefaultProvider,
		DefaultModel:         project.DefaultModel,
		DefaultAgentProfile:  project.DefaultAgentProfile,
		DefaultWorkspaceMode: project.DefaultWorkspaceMode,
		CreatedAt:            project.CreatedAt,
		UpdatedAt:            project.UpdatedAt,
	}
}

func projectRootContexts(items []projects.Root) []ProjectRootContext {
	out := make([]ProjectRootContext, 0, len(items))
	for _, item := range items {
		out = append(out, ProjectRootContext{
			ID:        item.ID,
			Path:      item.Path,
			Kind:      item.Kind,
			GitRemote: item.GitRemote,
			GitBranch: item.GitBranch,
			Active:    item.Active,
		})
	}
	return out
}

func contextSources(items []projects.ContextSource) []ContextSource {
	out := make([]ContextSource, 0, len(items))
	for _, item := range items {
		out = append(out, ContextSource{
			ID:             item.ID,
			Kind:           item.Kind,
			Title:          item.Title,
			Path:           item.Path,
			Enabled:        item.Enabled,
			Format:         item.Format,
			Scope:          item.Scope,
			TrustLabel:     item.TrustLabel,
			SourceCategory: item.SourceCategory,
			Metadata:       cloneContextMetadata(item.Metadata),
			CreatedAt:      item.CreatedAt,
			UpdatedAt:      item.UpdatedAt,
		})
	}
	return out
}

func cloneContextMetadata(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func workItemContextPtr(item *projectwork.WorkItem) *WorkItemContext {
	if item == nil {
		return nil
	}
	context := workItemContext(*item)
	return &context
}

func workItemContext(item projectwork.WorkItem) WorkItemContext {
	return WorkItemContext{
		ID:              item.ID,
		Title:           item.Title,
		Brief:           item.Brief,
		Status:          item.Status,
		Priority:        item.Priority,
		OwnerRoleID:     item.OwnerRoleID,
		ReviewerRoleIDs: append([]string(nil), item.ReviewerRoleIDs...),
		CreatedAt:       item.CreatedAt,
		UpdatedAt:       item.UpdatedAt,
	}
}

func roleContexts(items []projectwork.AgentRoleProfile) []RoleContext {
	out := make([]RoleContext, 0, len(items))
	for _, item := range items {
		out = append(out, RoleContext{
			ID:                  item.ID,
			Name:                item.Name,
			Description:         item.Description,
			DefaultDriverKind:   item.DefaultDriverKind,
			DefaultProvider:     item.DefaultProvider,
			DefaultModel:        item.DefaultModel,
			DefaultAgentProfile: item.DefaultAgentProfile,
			SkillIDs:            append([]string(nil), item.SkillIDs...),
			BuiltIn:             item.BuiltIn,
			CreatedAt:           item.CreatedAt,
			UpdatedAt:           item.UpdatedAt,
		})
	}
	return out
}

func projectSkillContext(item projectskills.Skill) ProjectSkillContext {
	return ProjectSkillContext{
		ID:                     item.ID,
		Title:                  item.Title,
		Description:            item.Description,
		Path:                   item.Path,
		RootID:                 item.RootID,
		Format:                 item.Format,
		Enabled:                item.Enabled,
		Status:                 item.Status,
		TrustLabel:             item.TrustLabel,
		SourceContextSourceIDs: append([]string(nil), item.SourceContextSourceIDs...),
		Warnings:               append([]string(nil), item.Warnings...),
		DiscoveredAt:           item.DiscoveredAt,
		CreatedAt:              item.CreatedAt,
		UpdatedAt:              item.UpdatedAt,
	}
}

func assignmentContext(item projectwork.Assignment) AssignmentContext {
	return AssignmentContext{
		ID:           item.ID,
		WorkItemID:   item.WorkItemID,
		RoleID:       item.RoleID,
		DriverKind:   item.DriverKind,
		Status:       item.Status,
		ExecutionRef: projectwork.NormalizeAssignmentExecutionRef(item.ExecutionRef),
		CreatedAt:    item.CreatedAt,
		UpdatedAt:    item.UpdatedAt,
		StartedAt:    timePtrIfSet(item.StartedAt),
		CompletedAt:  timePtrIfSet(item.CompletedAt),
	}
}

func memoryContext(item memory.Entry) MemoryContext {
	body := budgetedBody(item.Body, contextMemoryBodyMaxBytes)
	return MemoryContext{
		ID:                 item.ID,
		Title:              item.Title,
		Body:               body.Text,
		BodyOriginalBytes:  body.OriginalBytes,
		BodyReturnedBytes:  body.ReturnedBytes,
		BodyTokensEstimate: body.TokensEstimate,
		BodyTruncated:      body.Truncated,
		TrustLabel:         item.TrustLabel,
		SourceKind:         item.SourceKind,
		SourceID:           item.SourceID,
		Enabled:            item.Enabled,
		CreatedAt:          item.CreatedAt,
		UpdatedAt:          item.UpdatedAt,
	}
}

func memoryCandidateContext(item memory.Candidate) MemoryCandidateContext {
	body := budgetedBody(item.Body, contextCandidateBodyMaxBytes)
	return MemoryCandidateContext{
		ID:                  item.ID,
		Title:               item.Title,
		Body:                body.Text,
		BodyOriginalBytes:   body.OriginalBytes,
		BodyReturnedBytes:   body.ReturnedBytes,
		BodyTokensEstimate:  body.TokensEstimate,
		BodyTruncated:       body.Truncated,
		SuggestedKind:       item.SuggestedKind,
		SuggestedTrustLabel: item.SuggestedTrustLabel,
		SuggestedSourceKind: item.SuggestedSourceKind,
		SuggestedSourceID:   item.SuggestedSourceID,
		SourceRefs:          append([]memory.CandidateSourceRef(nil), item.SourceRefs...),
		Status:              item.Status,
		StatusReason:        item.StatusReason,
		PromotedMemoryID:    item.PromotedMemoryID,
		CreatedAt:           item.CreatedAt,
		UpdatedAt:           item.UpdatedAt,
	}
}

func recentActivity(workItem *projectwork.WorkItem, assignments []AssignmentContext, memoryItems []MemoryContext, candidates []MemoryCandidateContext) []ActivityContext {
	var out []ActivityContext
	if workItem != nil {
		out = append(out, ActivityContext{Kind: "selected_work", ID: workItem.ID, Title: workItem.Title, Status: workItem.Status, UpdatedAt: workItem.UpdatedAt})
	}
	for _, item := range assignments {
		out = append(out, ActivityContext{Kind: "assignment", ID: item.ID, Title: item.WorkItemID, Status: item.Status, UpdatedAt: item.UpdatedAt})
	}
	for _, item := range memoryItems {
		out = append(out, ActivityContext{Kind: "memory", ID: item.ID, Title: item.Title, Status: item.TrustLabel, UpdatedAt: item.UpdatedAt})
	}
	for _, item := range candidates {
		out = append(out, ActivityContext{Kind: "memory_candidate", ID: item.ID, Title: item.Title, Status: item.Status, UpdatedAt: item.UpdatedAt})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	if len(out) > contextActivityLimit {
		out = out[:contextActivityLimit]
	}
	return out
}

type contextBodyBudget struct {
	Text           string
	OriginalBytes  int
	ReturnedBytes  int
	TokensEstimate int
	Truncated      bool
}

func budgetedBody(body string, maxBytes int) contextBodyBudget {
	body = strings.TrimSpace(body)
	originalBytes := len(body)
	if originalBytes == 0 {
		return contextBodyBudget{}
	}
	if maxBytes <= 0 {
		return contextBodyBudget{
			OriginalBytes:  originalBytes,
			TokensEstimate: estimateTokensFromBytes(originalBytes),
			Truncated:      true,
		}
	}
	out := body
	truncated := false
	if len(out) > maxBytes {
		truncated = true
		if maxBytes <= len(contextTruncatedSuffix) {
			out = contextTruncatedSuffix[:maxBytes]
		} else {
			end := truncateStringByteIndex(out, maxBytes-len(contextTruncatedSuffix))
			out = strings.TrimRight(out[:end], "\r\n\t ") + contextTruncatedSuffix
			if len(out) > maxBytes {
				end = truncateStringByteIndex(out, maxBytes-len(contextTruncatedSuffix))
				out = out[:end] + contextTruncatedSuffix
			}
		}
	}
	return contextBodyBudget{
		Text:           out,
		OriginalBytes:  originalBytes,
		ReturnedBytes:  len(out),
		TokensEstimate: estimateTokensFromBytes(len(out)),
		Truncated:      truncated,
	}
}

func contextBudget(memoryItems []MemoryContext, candidates []MemoryCandidateContext) ContextBudget {
	budget := ContextBudget{
		MemoryBodyMaxBytes:          contextMemoryBodyMaxBytes,
		MemoryCandidateBodyMaxBytes: contextCandidateBodyMaxBytes,
	}
	for _, item := range memoryItems {
		budget.BodyOriginalBytes += item.BodyOriginalBytes
		budget.BodyReturnedBytes += item.BodyReturnedBytes
		budget.BodyTokensEstimate += item.BodyTokensEstimate
		if item.BodyTruncated {
			budget.BodyTruncatedCount++
		}
	}
	for _, item := range candidates {
		budget.BodyOriginalBytes += item.BodyOriginalBytes
		budget.BodyReturnedBytes += item.BodyReturnedBytes
		budget.BodyTokensEstimate += item.BodyTokensEstimate
		if item.BodyTruncated {
			budget.BodyTruncatedCount++
		}
	}
	return budget
}

func estimateTokensFromBytes(size int) int {
	if size <= 0 {
		return 0
	}
	return (size + 3) / 4
}

func truncateStringByteIndex(content string, maxBytes int) int {
	if len(content) <= maxBytes {
		return len(content)
	}
	end := 0
	for index := range content {
		if index > maxBytes {
			break
		}
		end = index
	}
	return end
}

func roleDisplayName(role projectwork.AgentRoleProfile) string {
	return firstNonEmpty(role.Name, role.ID, "role")
}

func nonEmptyStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func timePtrIfSet(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copy := value
	return &copy
}
