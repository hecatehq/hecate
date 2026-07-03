package projectworkapp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/agentcontrols"
	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/workspacefs"
	"github.com/hecatehq/hecate/pkg/types"
)

const (
	LaunchPlanInvalidRequest     = "invalid_request"
	LaunchPlanUnprocessable      = "unprocessable"
	LaunchPlanModelNotConfigured = "model_not_configured"
	LaunchPlanAdapterNotFound    = "adapter_not_found"
)

type LaunchPlanError struct {
	Kind      string
	Message   string
	AdapterID string
}

func (err LaunchPlanError) Error() string {
	return err.Message
}

func launchPlanError(kind, message string) error {
	return LaunchPlanError{Kind: kind, Message: message}
}

func launchPlanAdapterNotFound(adapterID string) error {
	return LaunchPlanError{
		Kind:      LaunchPlanAdapterNotFound,
		Message:   "external-agent adapter not found: " + strings.TrimSpace(adapterID),
		AdapterID: strings.TrimSpace(adapterID),
	}
}

type AgentProfileStore interface {
	Get(ctx context.Context, id string) (agentprofiles.Profile, bool, error)
}

type ProjectMemoryStore interface {
	List(ctx context.Context, filter memory.Filter) ([]memory.Entry, error)
}

type ProjectSkillStore interface {
	List(ctx context.Context, projectID string) ([]projectskills.Skill, error)
}

type TaskAssignmentLaunchPlan struct {
	WorkingDirectory  string
	WorkspaceMode     string
	Root              projects.Root
	RequestedProvider string
	RequestedModel    string
	ExecutionProfile  string
	Profile           ResolvedAgentProfile
	ResolvedSkills    ResolvedProjectSkills
	PromptContext     AssignmentPromptContext
}

type ExternalAgentAssignmentLaunchPlan struct {
	Workspace        string
	Root             projects.Root
	AdapterID        string
	Adapter          agentadapters.Adapter
	ConfigOptions    []agentcontrols.ConfigOption
	SessionTitle     string
	ExecutionProfile string
	Profile          ResolvedAgentProfile
	ResolvedSkills   ResolvedProjectSkills
}

type ResolvedAgentProfile struct {
	ID                   string
	Name                 string
	Source               string
	Instructions         string
	Missing              bool
	Surface              string
	ProviderHint         string
	ModelHint            string
	ExecutionProfile     string
	ToolsEnabled         bool
	WritesAllowed        bool
	NetworkAllowed       bool
	ApprovalPolicy       string
	ProjectMemoryPolicy  string
	ContextSourcePolicy  string
	SkillIDs             []string
	ExternalAgentKind    string
	ExternalAgentOptions map[string]string
	Warnings             []string
}

type ResolvedProjectSkills struct {
	Requested []string
	Resolved  []projectskills.Skill
	Skipped   []ResolvedProjectSkillSkip
	Warnings  []string
}

type ResolvedProjectSkillSkip struct {
	ID     string
	Reason string
	Status string
}

const (
	assignmentPromptContextMaxBytes       = 12 * 1024
	assignmentPromptContextMemoryMaxBytes = 2 * 1024
	assignmentPromptContextSourceMaxBytes = 8 * 1024
	assignmentPromptContextMaxWarnings    = 8
)

type AssignmentPromptContext struct {
	Sections        []string
	IncludedMemory  int
	IncludedSources int
	Truncated       int
	Warnings        []string
}

func (ctx AssignmentPromptContext) SystemPrompt() string {
	if len(ctx.Sections) == 0 {
		return ""
	}
	return strings.Join(ctx.Sections, "\n\n")
}

func (app *Application) ResolveTaskAssignmentLaunchPlan(ctx context.Context, project projects.Project, workItem projectwork.WorkItem, assignment projectwork.Assignment, role projectwork.AgentRoleProfile) (TaskAssignmentLaunchPlan, error) {
	root, workingDirectory, workspaceMode, err := ResolveAssignmentWorkspace(project, workItem, assignment)
	if err != nil {
		return TaskAssignmentLaunchPlan{}, launchPlanError(LaunchPlanInvalidRequest, err.Error())
	}
	requestedProvider := strings.TrimSpace(firstNonEmpty(role.DefaultProvider, project.DefaultProvider))
	requestedModel := strings.TrimSpace(firstNonEmpty(role.DefaultModel, project.DefaultModel))
	profile, err := app.ResolveAssignmentProfile(ctx, role, project)
	if err != nil {
		return TaskAssignmentLaunchPlan{}, err
	}
	executionProfile := strings.TrimSpace(firstNonEmpty(profile.ExecutionProfile, role.DefaultAgentProfile, project.DefaultAgentProfile, "project_assignment"))
	if profile.ProviderHint != "" && requestedProvider == "" {
		requestedProvider = profile.ProviderHint
	}
	if profile.ModelHint != "" && requestedModel == "" {
		requestedModel = profile.ModelHint
	}
	requestedModel = strings.TrimSpace(firstNonEmpty(requestedModel, app.runtimeDefaultModel))
	if requestedModel == "" {
		return TaskAssignmentLaunchPlan{}, launchPlanError(LaunchPlanModelNotConfigured, "project assignment start requires a default model")
	}
	return TaskAssignmentLaunchPlan{
		WorkingDirectory:  workingDirectory,
		WorkspaceMode:     workspaceMode,
		Root:              root,
		RequestedProvider: requestedProvider,
		RequestedModel:    requestedModel,
		ExecutionProfile:  executionProfile,
		Profile:           profile,
		ResolvedSkills:    app.ResolveAssignmentSkills(ctx, project.ID, role, profile),
		PromptContext:     app.AssignmentPromptContext(ctx, project, profile, workingDirectory),
	}, nil
}

func (app *Application) ResolveExternalAgentAssignmentLaunchPlan(ctx context.Context, project projects.Project, workItem projectwork.WorkItem, assignment projectwork.Assignment, role projectwork.AgentRoleProfile) (ExternalAgentAssignmentLaunchPlan, error) {
	root, workingDirectory, _, err := ResolveAssignmentWorkspace(project, workItem, assignment)
	if err != nil {
		return ExternalAgentAssignmentLaunchPlan{}, launchPlanError(LaunchPlanInvalidRequest, err.Error())
	}
	profile, err := app.ResolveAssignmentProfile(ctx, role, project)
	if err != nil {
		return ExternalAgentAssignmentLaunchPlan{}, err
	}
	adapterID := strings.TrimSpace(profile.ExternalAgentKind)
	if adapterID == "" {
		return ExternalAgentAssignmentLaunchPlan{}, launchPlanError(LaunchPlanUnprocessable, "external-agent assignment requires an agent preset with external_agent_kind")
	}
	adapter, ok := agentadapters.BuiltInByID(adapterID)
	if !ok {
		return ExternalAgentAssignmentLaunchPlan{}, launchPlanAdapterNotFound(adapterID)
	}
	configOptions, err := ExternalAgentConfigOptions(adapterID, profile.ExternalAgentOptions)
	if err != nil {
		return ExternalAgentAssignmentLaunchPlan{}, launchPlanError(LaunchPlanInvalidRequest, err.Error())
	}
	workspace, err := agentadapters.ValidateWorkspace(workingDirectory)
	if err != nil {
		return ExternalAgentAssignmentLaunchPlan{}, launchPlanError(LaunchPlanInvalidRequest, err.Error())
	}
	return ExternalAgentAssignmentLaunchPlan{
		Workspace:        workspace,
		Root:             root,
		AdapterID:        adapterID,
		Adapter:          adapter,
		ConfigOptions:    configOptions,
		SessionTitle:     ExternalAgentAssignmentTitle(workItem, role, adapter),
		ExecutionProfile: firstNonEmpty(profile.ExecutionProfile, "external_agent_assignment"),
		Profile:          profile,
		ResolvedSkills:   app.ResolveAssignmentSkills(ctx, project.ID, role, profile),
	}, nil
}

func ResolveAssignmentWorkspace(project projects.Project, workItem projectwork.WorkItem, assignment projectwork.Assignment) (projects.Root, string, string, error) {
	root, ok := SelectAssignmentRoot(project, workItem, assignment)
	if !ok {
		return projects.Root{}, "", "", fmt.Errorf("project has no workspace root; add a project root before starting an assignment")
	}
	path := strings.TrimSpace(root.Path)
	if path == "" {
		return projects.Root{}, "", "", fmt.Errorf("project root %q has no path", root.ID)
	}
	if !filepath.IsAbs(path) {
		return projects.Root{}, "", "", fmt.Errorf("project root %q path must be absolute", root.ID)
	}
	info, err := os.Stat(path)
	if err != nil {
		return projects.Root{}, "", "", fmt.Errorf("project root %q is not accessible: %w", root.ID, err)
	}
	if !info.IsDir() {
		return projects.Root{}, "", "", fmt.Errorf("project root %q is not a directory", root.ID)
	}
	workspaceMode := strings.TrimSpace(project.DefaultWorkspaceMode)
	if workspaceMode == "" {
		workspaceMode = "ephemeral"
	}
	return root, path, workspaceMode, nil
}

func SelectAssignmentRoot(project projects.Project, workItem projectwork.WorkItem, assignment projectwork.Assignment) (projects.Root, bool) {
	for _, id := range []string{
		strings.TrimSpace(assignment.RootID),
		strings.TrimSpace(workItem.RootID),
		strings.TrimSpace(project.DefaultRootID),
	} {
		if id == "" {
			continue
		}
		for _, root := range project.Roots {
			if root.ID == id {
				return root, true
			}
		}
		return projects.Root{}, false
	}
	for _, root := range project.Roots {
		if root.Active {
			return root, true
		}
	}
	if len(project.Roots) > 0 {
		return project.Roots[0], true
	}
	return projects.Root{}, false
}

func (app *Application) ResolveAssignmentProfile(ctx context.Context, role projectwork.AgentRoleProfile, project projects.Project) (ResolvedAgentProfile, error) {
	for _, candidate := range []struct {
		id     string
		source string
	}{
		{strings.TrimSpace(role.DefaultAgentProfile), "role_default"},
		{strings.TrimSpace(project.DefaultAgentProfile), "project_default"},
	} {
		if candidate.id == "" {
			continue
		}
		if app != nil && app.profileStore != nil {
			profile, ok, err := app.profileStore.Get(ctx, candidate.id)
			if err != nil {
				return ResolvedAgentProfile{}, err
			}
			if ok {
				return resolvedProfileFromStore(profile, candidate.source), nil
			}
		}
		return ResolvedAgentProfile{
			ID:                  candidate.id,
			Name:                candidate.id,
			Source:              candidate.source,
			Missing:             true,
			ExecutionProfile:    candidate.id,
			ApprovalPolicy:      agentprofiles.ApprovalInherit,
			ProjectMemoryPolicy: agentprofiles.MemoryInherit,
			ContextSourcePolicy: agentprofiles.ContextInherit,
			Warnings:            []string{fmt.Sprintf("Referenced agent preset %q was not found; using stored preset id as execution_profile hint.", candidate.id)},
		}, nil
	}
	if profile, ok := agentprofiles.BuiltInProfile("project_assignment"); ok {
		return resolvedProfileFromStore(profile, "built_in_fallback"), nil
	}
	return ResolvedAgentProfile{}, fmt.Errorf("%w: project_assignment built-in profile is unavailable", agentprofiles.ErrNotFound)
}

func resolvedProfileFromStore(profile agentprofiles.Profile, source string) ResolvedAgentProfile {
	return ResolvedAgentProfile{
		ID:                   profile.ID,
		Name:                 profile.Name,
		Source:               source,
		Instructions:         profile.Instructions,
		Surface:              profile.Surface,
		ProviderHint:         profile.ProviderHint,
		ModelHint:            profile.ModelHint,
		ExecutionProfile:     firstNonEmpty(profile.ExecutionProfile, profile.ID),
		ToolsEnabled:         profile.ToolsEnabled,
		WritesAllowed:        profile.WritesAllowed,
		NetworkAllowed:       profile.NetworkAllowed,
		ApprovalPolicy:       profile.ApprovalPolicy,
		ProjectMemoryPolicy:  profile.ProjectMemoryPolicy,
		ContextSourcePolicy:  profile.ContextSourcePolicy,
		SkillIDs:             append([]string(nil), profile.SkillIDs...),
		ExternalAgentKind:    profile.ExternalAgentKind,
		ExternalAgentOptions: cloneStringMap(profile.ExternalAgentOptions),
	}
}

func (app *Application) ResolveAssignmentSkills(ctx context.Context, projectID string, role projectwork.AgentRoleProfile, profile ResolvedAgentProfile) ResolvedProjectSkills {
	requested := normalizeStringList(append(append([]string(nil), role.SkillIDs...), profile.SkillIDs...))
	result := ResolvedProjectSkills{Requested: requested}
	if len(requested) == 0 {
		return result
	}
	if app == nil || app.skillStore == nil {
		result.Warnings = []string{"Project skills store is not configured; skill references were not resolved."}
		for _, id := range requested {
			result.Skipped = append(result.Skipped, ResolvedProjectSkillSkip{ID: id, Reason: "store_unavailable"})
		}
		return result
	}
	items, err := app.skillStore.List(ctx, projectID)
	if err != nil {
		result.Warnings = []string{"Project skills could not be loaded; skill references were not resolved."}
		for _, id := range requested {
			result.Skipped = append(result.Skipped, ResolvedProjectSkillSkip{ID: id, Reason: "store_error"})
		}
		return result
	}
	byID := make(map[string]projectskills.Skill, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	for _, id := range requested {
		item, ok := byID[id]
		switch {
		case !ok:
			result.Skipped = append(result.Skipped, ResolvedProjectSkillSkip{ID: id, Reason: "missing", Status: projectskills.StatusMissing})
		case !item.Enabled:
			result.Skipped = append(result.Skipped, ResolvedProjectSkillSkip{ID: id, Reason: "disabled", Status: item.Status})
		case item.Status != projectskills.StatusAvailable:
			result.Skipped = append(result.Skipped, ResolvedProjectSkillSkip{ID: id, Reason: item.Status, Status: item.Status})
		default:
			result.Resolved = append(result.Resolved, item)
			result.Warnings = append(result.Warnings, projectSkillCapabilityWarnings(item, profile)...)
		}
	}
	result.Warnings = normalizeStringList(result.Warnings)
	return result
}

func projectSkillCapabilityWarnings(skill projectskills.Skill, profile ResolvedAgentProfile) []string {
	var warnings []string
	label := labelWithID(firstNonEmpty(skill.Title, skill.ID), skill.ID)
	appendPermissionWarning := func(name string, required *bool, actual bool) {
		if required == nil || *required == actual {
			return
		}
		wanted := "disabled"
		if *required {
			wanted = "enabled"
		}
		got := "disabled"
		if actual {
			got = "enabled"
		}
		warnings = append(warnings, fmt.Sprintf("Project skill %s declares %s %s, but resolved preset %s has %s %s.", label, name, wanted, firstNonEmpty(profile.ID, "unknown"), name, got))
	}
	appendPermissionWarning("tools", skill.RequiredPermissions.Tools, profile.ToolsEnabled)
	appendPermissionWarning("writes", skill.RequiredPermissions.Writes, profile.WritesAllowed)
	appendPermissionWarning("network", skill.RequiredPermissions.Network, profile.NetworkAllowed)
	if toolsSummary := projectskills.SuggestedToolsSummary(skill.SuggestedTools); toolsSummary != "" && skill.RequiredPermissions.Tools == nil && !profile.ToolsEnabled {
		warnings = append(warnings, fmt.Sprintf("Project skill %s suggests tools (%s), but resolved preset %s has tools disabled.", label, toolsSummary, firstNonEmpty(profile.ID, "unknown")))
	}
	return warnings
}

func (app *Application) AssignmentPromptContext(ctx context.Context, project projects.Project, profile ResolvedAgentProfile, workingDirectory string) AssignmentPromptContext {
	builder := promptContextBuilder{Remaining: assignmentPromptContextMaxBytes}
	if effectiveProjectMemoryPolicy(profile.ProjectMemoryPolicy) == agentprofiles.MemoryInclude {
		builder.AppendMemory(app.enabledProjectMemoryEntries(ctx, project.ID))
	}
	if effectiveContextSourcePolicy(profile.ContextSourcePolicy) == agentprofiles.ContextIncludeEnabled {
		builder.AppendSources(project, workingDirectory)
	}
	return builder.Result()
}

func (app *Application) enabledProjectMemoryEntries(ctx context.Context, projectID string) []memory.Entry {
	if app == nil || app.memoryStore == nil || strings.TrimSpace(projectID) == "" {
		return nil
	}
	items, err := app.memoryStore.List(ctx, memory.Filter{ProjectID: projectID})
	if err != nil {
		return nil
	}
	return items
}

func NewAssignmentTask(taskID string, project projects.Project, workItem projectwork.WorkItem, assignment projectwork.Assignment, role projectwork.AgentRoleProfile, plan TaskAssignmentLaunchPlan, now time.Time) types.Task {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return types.Task{
		ID:                          taskID,
		Title:                       AssignmentTaskTitle(workItem, role),
		Prompt:                      AssignmentPrompt(project, workItem, assignment, role),
		ProjectID:                   project.ID,
		WorkItemID:                  workItem.ID,
		AssignmentID:                assignment.ID,
		SystemPrompt:                AssignmentSystemPrompt(project, role, plan.Profile, plan.PromptContext),
		WorkspaceSystemPromptPolicy: types.WorkspaceSystemPromptExclude,
		ExecutionKind:               "agent_loop",
		ExecutionProfile:            plan.ExecutionProfile,
		OriginKind:                  "project_work_item",
		OriginID:                    workItem.ID,
		WorkspaceMode:               plan.WorkspaceMode,
		WorkingDirectory:            plan.WorkingDirectory,
		SandboxAllowedRoot:          plan.WorkingDirectory,
		Status:                      "queued",
		Priority:                    firstNonEmpty(workItem.Priority, "normal"),
		RequestedProvider:           plan.RequestedProvider,
		RequestedModel:              plan.RequestedModel,
		CreatedAt:                   now,
		UpdatedAt:                   now,
	}
}

func AssignmentTaskTitle(workItem projectwork.WorkItem, role projectwork.AgentRoleProfile) string {
	title := strings.TrimSpace(workItem.Title)
	roleName := strings.TrimSpace(role.Name)
	switch {
	case title != "" && roleName != "":
		return title + " - " + roleName
	case title != "":
		return title
	case roleName != "":
		return roleName + " assignment"
	default:
		return "Project work assignment"
	}
}

func AssignmentPrompt(project projects.Project, workItem projectwork.WorkItem, assignment projectwork.Assignment, role projectwork.AgentRoleProfile) string {
	provider := firstNonEmpty(role.DefaultProvider, project.DefaultProvider, "auto")
	model := firstNonEmpty(role.DefaultModel, project.DefaultModel, "project/runtime default")
	profile := firstNonEmpty(role.DefaultAgentProfile, project.DefaultAgentProfile, "none")
	driver := firstNonEmpty(assignment.DriverKind, role.DefaultDriverKind, projectwork.AssignmentDriverHecateTask)
	sections := []string{
		"Launch context",
		"Project: " + labelWithID(project.Name, project.ID),
		strings.Join([]string{
			"Work item:",
			"- Title: " + firstNonEmpty(workItem.Title, workItem.ID),
			launchContextBullet("Brief", firstNonEmpty(workItem.Brief, "No brief recorded.")),
			"- Status: " + firstNonEmpty(workItem.Status, "unknown"),
			"- Priority: " + firstNonEmpty(workItem.Priority, "normal"),
		}, "\n"),
		strings.Join([]string{
			"Assignment:",
			"- ID: " + assignment.ID,
			"- Status: " + firstNonEmpty(assignment.Status, projectwork.AssignmentStatusQueued),
			"- Driver: " + driver,
		}, "\n"),
		strings.Join([]string{
			"Role:",
			"- Name: " + firstNonEmpty(role.Name, assignment.RoleID),
			launchContextBullet("Description", firstNonEmpty(role.Description, "No description recorded.")),
			launchContextBullet("Instructions", firstNonEmpty(role.Instructions, "No role instructions recorded.")),
		}, "\n"),
		strings.Join([]string{
			"Execution hints:",
			"- Driver: " + driver,
			"- Provider: " + provider,
			"- Model: " + model,
			"- Agent preset: " + profile,
			"- Role defaults: " + formatAssignmentHints([]assignmentHint{
				{"driver", role.DefaultDriverKind},
				{"provider", role.DefaultProvider},
				{"model", role.DefaultModel},
				{"preset", role.DefaultAgentProfile},
			}),
			"- Project defaults: " + formatAssignmentHints([]assignmentHint{
				{"provider", project.DefaultProvider},
				{"model", project.DefaultModel},
				{"preset", project.DefaultAgentProfile},
				{"workspace_mode", project.DefaultWorkspaceMode},
			}),
		}, "\n"),
		"Request:\nExecute this assignment as a native agent_loop task. Keep outputs and artifacts linked to this work item.",
	}
	return strings.Join(sections, "\n\n")
}

func AssignmentSystemPrompt(project projects.Project, role projectwork.AgentRoleProfile, profile ResolvedAgentProfile, promptContext AssignmentPromptContext) string {
	var parts []string
	if prompt := strings.TrimSpace(project.DefaultSystemPrompt); prompt != "" {
		parts = append(parts, "Project system prompt:\n"+prompt)
	}
	if instructions := strings.TrimSpace(profile.Instructions); instructions != "" && !profile.Missing {
		parts = append(parts, "Agent preset instructions:\n"+instructions)
	}
	if instructions := strings.TrimSpace(role.Instructions); instructions != "" {
		parts = append(parts, "Role instructions:\n"+instructions)
	} else if role.Name != "" {
		parts = append(parts, "Act as the "+strings.TrimSpace(role.Name)+" for this project work assignment.")
	}
	if contextText := promptContext.SystemPrompt(); contextText != "" {
		parts = append(parts, contextText)
	}
	return strings.Join(parts, "\n\n")
}

func ExternalAgentConfigOptions(adapterID string, options map[string]string) ([]agentcontrols.ConfigOption, error) {
	if len(options) == 0 {
		return nil, nil
	}
	out := make([]agentcontrols.ConfigOption, 0, len(options))
	for key, value := range options {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		option, ok := agentadapters.LaunchConfigOptionForSet(adapterID, key, value)
		if !ok {
			return nil, fmt.Errorf("external_agent_options.%s is not a launch option for %s", key, adapterID)
		}
		out = append(out, option)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func ExternalAgentAssignmentTitle(workItem projectwork.WorkItem, role projectwork.AgentRoleProfile, adapter agentadapters.Adapter) string {
	parts := []string{}
	if title := strings.TrimSpace(workItem.Title); title != "" {
		parts = append(parts, title)
	}
	if roleName := strings.TrimSpace(role.Name); roleName != "" {
		parts = append(parts, roleName)
	}
	if adapter.Name != "" {
		parts = append(parts, adapter.Name)
	}
	if len(parts) == 0 {
		return "External Agent assignment"
	}
	return strings.Join(parts, " - ")
}

type promptContextBuilder struct {
	Remaining int
	ResultCtx AssignmentPromptContext
}

func (builder *promptContextBuilder) AppendMemory(entries []memory.Entry) {
	for _, entry := range entries {
		if builder.Remaining <= 0 {
			builder.Warn("project memory prompt context budget exhausted; remaining memory entries were skipped")
			return
		}
		title := firstNonEmpty(strings.TrimSpace(entry.Title), strings.TrimSpace(entry.ID))
		body := strings.TrimSpace(entry.Body)
		if body == "" {
			continue
		}
		header := fmt.Sprintf("Project memory: %s\nID: %s\nTrust: %s", title, strings.TrimSpace(entry.ID), firstNonEmpty(strings.TrimSpace(entry.TrustLabel), "operator_memory"))
		section, truncated := boundedPromptContextSection(header, body, assignmentPromptContextMemoryMaxBytes, &builder.Remaining)
		if section == "" {
			builder.Warn("project memory prompt context budget exhausted before " + strings.TrimSpace(entry.ID))
			return
		}
		if truncated {
			builder.ResultCtx.Truncated++
			builder.Warn("project memory " + strings.TrimSpace(entry.ID) + " was truncated for prompt context")
		}
		builder.ResultCtx.IncludedMemory++
		builder.ResultCtx.Sections = append(builder.ResultCtx.Sections, section)
	}
}

func (builder *promptContextBuilder) AppendSources(project projects.Project, workingDirectory string) {
	for _, source := range project.ContextSources {
		if !source.Enabled {
			continue
		}
		if builder.Remaining <= 0 {
			builder.Warn("project source prompt context budget exhausted; remaining sources were skipped")
			return
		}
		if !projectContextSourcePromptEligible(source) {
			if strings.TrimSpace(source.Path) != "" {
				builder.Warn("project source " + strings.TrimSpace(source.Path) + " is metadata-only for Hecate prompt context")
			}
			continue
		}
		rootPath := projectContextSourceRootPath(project, source, workingDirectory)
		if strings.TrimSpace(rootPath) == "" {
			builder.Warn("project source " + strings.TrimSpace(source.Path) + " could not resolve an active root")
			continue
		}
		fsys, err := workspacefs.New(rootPath)
		if err != nil {
			builder.Warn("project source " + strings.TrimSpace(source.Path) + " could not open its workspace root")
			continue
		}
		raw, _, err := fsys.ReadFile(source.Path)
		if err != nil {
			builder.Warn("project source " + strings.TrimSpace(source.Path) + " could not be read for prompt context")
			continue
		}
		body := strings.TrimSpace(string(raw))
		if body == "" {
			continue
		}
		title := firstNonEmpty(strings.TrimSpace(source.Title), strings.TrimSpace(source.Path))
		header := fmt.Sprintf("Workspace instruction: %s\nPath: %s\nTrust: %s", title, strings.TrimSpace(source.Path), firstNonEmpty(strings.TrimSpace(source.TrustLabel), "workspace_guidance"))
		section, truncated := boundedPromptContextSection(header, body, assignmentPromptContextSourceMaxBytes, &builder.Remaining)
		if section == "" {
			builder.Warn("project source prompt context budget exhausted before " + strings.TrimSpace(source.Path))
			return
		}
		if truncated {
			builder.ResultCtx.Truncated++
			builder.Warn("project source " + strings.TrimSpace(source.Path) + " was truncated for prompt context")
		}
		builder.ResultCtx.IncludedSources++
		builder.ResultCtx.Sections = append(builder.ResultCtx.Sections, section)
	}
}

func (builder *promptContextBuilder) Warn(warning string) {
	warning = strings.TrimSpace(warning)
	if warning == "" || len(builder.ResultCtx.Warnings) >= assignmentPromptContextMaxWarnings {
		return
	}
	builder.ResultCtx.Warnings = append(builder.ResultCtx.Warnings, warning)
}

func (builder promptContextBuilder) Result() AssignmentPromptContext {
	return builder.ResultCtx
}

func projectContextSourcePromptEligible(source projects.ContextSource) bool {
	return strings.TrimSpace(source.Kind) == "workspace_instruction" && strings.TrimSpace(source.Format) == "agents_md"
}

func projectContextSourceRootPath(project projects.Project, source projects.ContextSource, fallback string) string {
	rootID := ""
	if source.Metadata != nil {
		rootID = strings.TrimSpace(source.Metadata["root_id"])
	}
	if rootID != "" {
		for _, root := range project.Roots {
			if root.Active && strings.TrimSpace(root.ID) == rootID {
				return strings.TrimSpace(root.Path)
			}
		}
		return ""
	}
	return strings.TrimSpace(fallback)
}

func boundedPromptContextSection(header, body string, itemMaxBytes int, remaining *int) (string, bool) {
	if remaining == nil || *remaining <= 0 {
		return "", false
	}
	header = strings.TrimSpace(header)
	body = strings.TrimSpace(body)
	if header == "" || body == "" {
		return "", false
	}
	limit := itemMaxBytes
	if *remaining < limit {
		limit = *remaining
	}
	text := header + "\n" + body
	text, truncated := truncatePromptContextText(text, limit)
	if text == "" {
		return "", truncated
	}
	*remaining -= len(text)
	return text, truncated
}

func truncatePromptContextText(text string, maxBytes int) (string, bool) {
	text = strings.TrimSpace(text)
	if maxBytes <= 0 {
		return "", text != ""
	}
	if len(text) <= maxBytes {
		return text, false
	}
	if maxBytes <= len("\n[truncated]") {
		return "", true
	}
	cut := maxBytes - len("\n[truncated]")
	raw := []byte(text)
	for cut > 0 && !utf8.Valid(raw[:cut]) {
		cut--
	}
	if cut <= 0 {
		return "", true
	}
	return strings.TrimSpace(string(raw[:cut])) + "\n[truncated]", true
}

type assignmentHint struct {
	label string
	value string
}

func formatAssignmentHints(items []assignmentHint) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		value := strings.TrimSpace(item.value)
		if value == "" {
			continue
		}
		parts = append(parts, item.label+"="+value)
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

func labelWithID(label, id string) string {
	label = strings.TrimSpace(label)
	id = strings.TrimSpace(id)
	if label != "" && id != "" {
		return label + " (" + id + ")"
	}
	return firstNonEmpty(label, id)
}

func launchContextBullet(label, value string) string {
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n"), "\n")
	if len(lines) == 0 {
		return "- " + label + ": "
	}
	if len(lines) == 1 {
		return "- " + label + ": " + lines[0]
	}
	return "- " + label + ": " + lines[0] + "\n  " + strings.Join(lines[1:], "\n  ")
}

func effectiveProjectMemoryPolicy(policy string) string {
	switch strings.TrimSpace(policy) {
	case agentprofiles.MemoryInclude:
		return agentprofiles.MemoryInclude
	case agentprofiles.MemoryExclude:
		return agentprofiles.MemoryExclude
	case agentprofiles.MemoryVisibleOnly:
		return agentprofiles.MemoryVisibleOnly
	default:
		return agentprofiles.MemoryVisibleOnly
	}
}

func effectiveContextSourcePolicy(policy string) string {
	switch strings.TrimSpace(policy) {
	case agentprofiles.ContextIncludeEnabled:
		return agentprofiles.ContextIncludeEnabled
	case agentprofiles.ContextExclude:
		return agentprofiles.ContextExclude
	case agentprofiles.ContextVisibleOnly:
		return agentprofiles.ContextVisibleOnly
	default:
		return agentprofiles.ContextVisibleOnly
	}
}

func normalizeStringList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func cloneStringMap(items map[string]string) map[string]string {
	if len(items) == 0 {
		return nil
	}
	out := make(map[string]string, len(items))
	for key, value := range items {
		out[key] = value
	}
	return out
}
