package projectworkapp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
)

func TestApplication_ResolveTaskAssignmentLaunchPlanAppliesProfileHintsAndPromptContext(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("Use focused launch tests."), 0o600); err != nil {
		t.Fatalf("write workspace instruction: %v", err)
	}

	profiles := agentprofiles.NewMemoryStore()
	if _, err := profiles.Create(ctx, agentprofiles.Profile{
		ID:                  "prof_backend",
		Name:                "Backend",
		Surface:             agentprofiles.SurfaceHecateTask,
		Instructions:        "Prefer small backend slices.",
		ProviderHint:        "openai",
		ModelHint:           "gpt-4o-mini",
		ExecutionProfile:    "implementation",
		ProjectMemoryPolicy: agentprofiles.MemoryInclude,
		ContextSourcePolicy: agentprofiles.ContextIncludeEnabled,
		SkillIDs:            []string{"backend"},
	}); err != nil {
		t.Fatalf("Create(profile) error = %v", err)
	}
	memories := memory.NewMemoryStore()
	if _, err := memories.Create(ctx, memory.Entry{
		ID:         "mem_1",
		Scope:      memory.ScopeProject,
		ProjectID:  "proj_1",
		Title:      "Testing",
		Body:       "Prefer focused tests.",
		TrustLabel: memory.TrustLabelOperatorMemory,
		SourceKind: memory.SourceKindOperator,
		Enabled:    true,
	}); err != nil {
		t.Fatalf("Create(memory) error = %v", err)
	}
	skills := projectskills.NewMemoryStore()
	if _, err := skills.UpsertDiscovered(ctx, "proj_1", []projectskills.Skill{{
		ID:         "backend",
		Title:      "Backend",
		Path:       ".hecate/skills/backend/SKILL.md",
		Format:     projectskills.FormatSkillMD,
		Enabled:    true,
		Status:     projectskills.StatusAvailable,
		TrustLabel: projectskills.TrustWorkspaceSkill,
	}}); err != nil {
		t.Fatalf("UpsertDiscovered() error = %v", err)
	}
	app := New(Options{
		ProfileStore: profiles,
		MemoryStore:  memories,
		SkillStore:   skills,
	})
	project := launchPlanTestProject(workspace)
	role := projectwork.AgentRoleProfile{
		ID:                  "role_backend",
		Name:                "Backend engineer",
		Instructions:        "Keep the change reviewable.",
		DefaultAgentProfile: "prof_backend",
		SkillIDs:            []string{"missing_skill"},
	}

	plan, err := app.ResolveTaskAssignmentLaunchPlan(ctx, project, launchPlanTestWorkItem(), launchPlanTestAssignment(), role)
	if err != nil {
		t.Fatalf("ResolveTaskAssignmentLaunchPlan() error = %v", err)
	}
	if plan.WorkingDirectory != workspace || plan.WorkspaceMode != "in_place" {
		t.Fatalf("workspace = (%q, %q), want (%q, in_place)", plan.WorkingDirectory, plan.WorkspaceMode, workspace)
	}
	if plan.RequestedProvider != "openai" || plan.RequestedModel != "gpt-4o-mini" || plan.ExecutionProfile != "implementation" {
		t.Fatalf("launch hints = provider %q model %q profile %q", plan.RequestedProvider, plan.RequestedModel, plan.ExecutionProfile)
	}
	if plan.Profile.ID != "prof_backend" || plan.Profile.Source != "role_default" {
		t.Fatalf("profile = %+v, want resolved role default", plan.Profile)
	}
	if plan.PromptContext.IncludedMemory != 1 || plan.PromptContext.IncludedSources != 1 {
		t.Fatalf("prompt context = %+v, want one memory and one source", plan.PromptContext)
	}
	systemContext := plan.PromptContext.SystemPrompt()
	if !strings.Contains(systemContext, "Prefer focused tests.") || !strings.Contains(systemContext, "Use focused launch tests.") {
		t.Fatalf("system context = %q, want memory and workspace instruction", systemContext)
	}
	if got, want := plan.ResolvedSkills.Requested, []string{"missing_skill", "backend"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("requested skills = %#v, want %#v", got, want)
	}
	if len(plan.ResolvedSkills.Resolved) != 1 || plan.ResolvedSkills.Resolved[0].ID != "backend" {
		t.Fatalf("resolved skills = %+v, want backend", plan.ResolvedSkills.Resolved)
	}
	if len(plan.ResolvedSkills.Skipped) != 1 || plan.ResolvedSkills.Skipped[0].ID != "missing_skill" {
		t.Fatalf("skipped skills = %+v, want missing_skill", plan.ResolvedSkills.Skipped)
	}

	task := NewAssignmentTask("task_1", project, launchPlanTestWorkItem(), launchPlanTestAssignment(), role, plan, time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC))
	if task.RequestedProvider != "openai" || task.RequestedModel != "gpt-4o-mini" || task.ExecutionProfile != "implementation" {
		t.Fatalf("task launch hints = provider %q model %q profile %q", task.RequestedProvider, task.RequestedModel, task.ExecutionProfile)
	}
	if task.WorkingDirectory != workspace || task.SandboxAllowedRoot != workspace || task.WorkspaceMode != "in_place" {
		t.Fatalf("task workspace = %+v, want planned workspace", task)
	}
	if !strings.Contains(task.SystemPrompt, "Prefer focused tests.") || !strings.Contains(task.SystemPrompt, "Use focused launch tests.") {
		t.Fatalf("task system prompt = %q, want prompt context", task.SystemPrompt)
	}
}

func TestApplication_ResolveTaskAssignmentLaunchPlanRejectsMissingModel(t *testing.T) {
	t.Parallel()

	app := New(Options{})
	_, err := app.ResolveTaskAssignmentLaunchPlan(context.Background(), launchPlanTestProject(t.TempDir()), launchPlanTestWorkItem(), launchPlanTestAssignment(), projectwork.AgentRoleProfile{Name: "Builder"})
	var launchErr LaunchPlanError
	if !errors.As(err, &launchErr) || launchErr.Kind != LaunchPlanModelNotConfigured {
		t.Fatalf("ResolveTaskAssignmentLaunchPlan() error = %v, want LaunchPlanModelNotConfigured", err)
	}
}

func TestNewAssignmentTask_SnapshotsAgentPresetSandboxPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		profile      ResolvedAgentProfile
		wantReadOnly bool
		wantNetwork  bool
		wantPresetID string
	}{
		{
			name:         "review preset is read only and offline",
			profile:      ResolvedAgentProfile{ID: "review_qa", WritesAllowed: false, NetworkAllowed: false},
			wantReadOnly: true,
			wantPresetID: "review_qa",
		},
		{
			name:         "implementation preset permits writes and network",
			profile:      ResolvedAgentProfile{ID: "implementation", WritesAllowed: true, NetworkAllowed: true},
			wantNetwork:  true,
			wantPresetID: "implementation",
		},
		{
			name:         "missing preset posture fails closed",
			profile:      ResolvedAgentProfile{ID: "missing", Missing: true},
			wantReadOnly: true,
			wantPresetID: "missing",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			workspace := t.TempDir()
			task := NewAssignmentTask(
				"task_1",
				launchPlanTestProject(workspace),
				launchPlanTestWorkItem(),
				launchPlanTestAssignment(),
				projectwork.AgentRoleProfile{Name: "Reviewer"},
				TaskAssignmentLaunchPlan{
					WorkingDirectory: workspace,
					WorkspaceMode:    "in_place",
					ExecutionProfile: "repo_local",
					Profile:          test.profile,
				},
				time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC),
			)
			if task.AgentPresetID != test.wantPresetID || task.SandboxReadOnly != test.wantReadOnly || task.SandboxNetwork != test.wantNetwork {
				t.Fatalf("task policy snapshot = preset %q read_only=%v network=%v, want %q/%v/%v", task.AgentPresetID, task.SandboxReadOnly, task.SandboxNetwork, test.wantPresetID, test.wantReadOnly, test.wantNetwork)
			}
		})
	}
}

func TestApplication_AssignmentLaunchPlansRejectIncompatiblePresetSurface(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		profile agentprofiles.Profile
		resolve func(*Application, context.Context, projects.Project, projectwork.AgentRoleProfile) error
	}{
		{
			name: "native task rejects external agent preset",
			profile: agentprofiles.Profile{
				ID:                "prof_external",
				Name:              "External",
				Surface:           agentprofiles.SurfaceExternalAgent,
				ExternalAgentKind: "codex",
			},
			resolve: func(app *Application, ctx context.Context, project projects.Project, role projectwork.AgentRoleProfile) error {
				_, err := app.ResolveTaskAssignmentLaunchPlan(ctx, project, launchPlanTestWorkItem(), launchPlanTestAssignment(), role)
				return err
			},
		},
		{
			name: "external agent rejects native task preset",
			profile: agentprofiles.Profile{
				ID:                "prof_native",
				Name:              "Native",
				Surface:           agentprofiles.SurfaceHecateTask,
				ExternalAgentKind: "codex",
			},
			resolve: func(app *Application, ctx context.Context, project projects.Project, role projectwork.AgentRoleProfile) error {
				_, err := app.ResolveExternalAgentAssignmentLaunchPlan(ctx, project, launchPlanTestWorkItem(), launchPlanTestAssignment(), role)
				return err
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			profiles := agentprofiles.NewMemoryStore()
			if _, err := profiles.Create(ctx, test.profile); err != nil {
				t.Fatalf("Create(profile) error = %v", err)
			}
			app := New(Options{ProfileStore: profiles, RuntimeDefaultModel: "test-model"})
			err := test.resolve(app, ctx, launchPlanTestProject(t.TempDir()), projectwork.AgentRoleProfile{
				Name:                "Worker",
				DefaultAgentProfile: test.profile.ID,
			})
			var launchErr LaunchPlanError
			if !errors.As(err, &launchErr) || launchErr.Kind != LaunchPlanUnprocessable || !strings.Contains(launchErr.Message, "requires") {
				t.Fatalf("launch error = %#v, want typed incompatible-surface blocker", err)
			}
		})
	}
}

func TestApplication_ResolveAssignmentSkillsWarnsForProfileCapabilityMismatch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	skills := projectskills.NewMemoryStore()
	if _, err := skills.UpsertDiscovered(ctx, "proj_1", []projectskills.Skill{
		{
			ID:         "review",
			Title:      "Review",
			Path:       ".hecate/skills/review/SKILL.md",
			Format:     projectskills.FormatSkillMD,
			Enabled:    true,
			Status:     projectskills.StatusAvailable,
			TrustLabel: projectskills.TrustWorkspaceSkill,
			RequiredPermissions: projectskills.RequiredPermissions{
				Tools:   boolForLaunchPlanTest(true),
				Writes:  boolForLaunchPlanTest(false),
				Network: boolForLaunchPlanTest(false),
			},
		},
		{
			ID:         "research",
			Title:      "Research",
			Path:       ".hecate/skills/research/SKILL.md",
			Format:     projectskills.FormatSkillMD,
			Enabled:    true,
			Status:     projectskills.StatusAvailable,
			TrustLabel: projectskills.TrustWorkspaceSkill,
			SuggestedTools: []string{
				"tool.00",
				"tool.01",
				"tool.02",
				"tool.03",
				"tool.04",
				"tool.05",
				"tool.06",
				"tool.07",
				"tool.08",
				"tool.09",
			},
		},
	}); err != nil {
		t.Fatalf("UpsertDiscovered() error = %v", err)
	}
	app := New(Options{SkillStore: skills})

	resolved := app.ResolveAssignmentSkills(ctx, "proj_1", projectwork.AgentRoleProfile{
		SkillIDs: []string{"review", "research"},
	}, ResolvedAgentProfile{
		ID:             "safe_review",
		ToolsEnabled:   false,
		WritesAllowed:  false,
		NetworkAllowed: true,
	})

	if len(resolved.Resolved) != 2 {
		t.Fatalf("resolved skills = %+v, want both skills resolved", resolved.Resolved)
	}
	if !launchPlanWarningContains(resolved.Warnings, "Project skill Review (review) declares tools enabled") {
		t.Fatalf("warnings = %+v, want tools mismatch", resolved.Warnings)
	}
	if !launchPlanWarningContains(resolved.Warnings, "Project skill Review (review) declares network disabled") {
		t.Fatalf("warnings = %+v, want network mismatch", resolved.Warnings)
	}
	if !launchPlanWarningContains(resolved.Warnings, "Project skill Research (research) suggests tools (tool.00, tool.01, tool.02, tool.03, tool.04, tool.05, tool.06, tool.07, +2 more)") {
		t.Fatalf("warnings = %+v, want suggested tools mismatch", resolved.Warnings)
	}
}

func TestApplication_ResolveTaskAssignmentLaunchPlanUsesRuntimeDefaultModel(t *testing.T) {
	t.Parallel()

	app := New(Options{RuntimeDefaultModel: "runtime-default"})
	plan, err := app.ResolveTaskAssignmentLaunchPlan(context.Background(), launchPlanTestProject(t.TempDir()), launchPlanTestWorkItem(), launchPlanTestAssignment(), projectwork.AgentRoleProfile{Name: "Builder"})
	if err != nil {
		t.Fatalf("ResolveTaskAssignmentLaunchPlan() error = %v", err)
	}
	if plan.RequestedModel != "runtime-default" {
		t.Fatalf("requested model = %q, want runtime-default", plan.RequestedModel)
	}
}

func TestSelectAssignmentRootPrecedence(t *testing.T) {
	t.Parallel()

	project := projects.Project{
		ID:            "proj_1",
		DefaultRootID: "root_default",
		Roots: []projects.Root{
			{ID: "root_default", Path: "/workspace/default", Active: false},
			{ID: "root_active", Path: "/workspace/active", Active: true},
			{ID: "root_work", Path: "/workspace/work", Active: true},
			{ID: "root_assignment", Path: "/workspace/assignment", Active: true},
		},
	}
	workItem := projectwork.WorkItem{ID: "work_1", RootID: "root_work"}
	assignment := projectwork.Assignment{ID: "asgn_1", RootID: "root_assignment"}

	root, ok := SelectAssignmentRoot(project, workItem, assignment)
	if !ok || root.ID != "root_assignment" {
		t.Fatalf("assignment override root = %+v ok=%v, want root_assignment", root, ok)
	}

	assignment.RootID = ""
	root, ok = SelectAssignmentRoot(project, workItem, assignment)
	if !ok || root.ID != "root_work" {
		t.Fatalf("work item root = %+v ok=%v, want root_work", root, ok)
	}

	workItem.RootID = ""
	root, ok = SelectAssignmentRoot(project, workItem, assignment)
	if !ok || root.ID != "root_default" {
		t.Fatalf("project default root = %+v ok=%v, want root_default", root, ok)
	}

	project.DefaultRootID = ""
	root, ok = SelectAssignmentRoot(project, workItem, assignment)
	if !ok || root.ID != "root_active" {
		t.Fatalf("active root fallback = %+v ok=%v, want root_active", root, ok)
	}
}

func TestApplication_ResolveExternalAgentAssignmentLaunchPlanResolvesAdapter(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	profiles := agentprofiles.NewMemoryStore()
	if _, err := profiles.Create(ctx, agentprofiles.Profile{
		ID:                  "prof_codex",
		Name:                "Codex",
		Surface:             agentprofiles.SurfaceExternalAgent,
		ExecutionProfile:    "external_implementation",
		ExternalAgentKind:   "codex",
		ProjectMemoryPolicy: agentprofiles.MemoryVisibleOnly,
		ContextSourcePolicy: agentprofiles.ContextVisibleOnly,
	}); err != nil {
		t.Fatalf("Create(profile) error = %v", err)
	}
	app := New(Options{ProfileStore: profiles})
	role := projectwork.AgentRoleProfile{
		ID:                  "role_ext",
		Name:                "External implementer",
		DefaultAgentProfile: "prof_codex",
	}

	plan, err := app.ResolveExternalAgentAssignmentLaunchPlan(ctx, launchPlanTestProject(t.TempDir()), launchPlanTestWorkItem(), launchPlanTestAssignment(), role)
	if err != nil {
		t.Fatalf("ResolveExternalAgentAssignmentLaunchPlan() error = %v", err)
	}
	adapter, _ := agentadapters.BuiltInByID("codex")
	if plan.AdapterID != "codex" || plan.Adapter.ID != adapter.ID || plan.Adapter.Name != adapter.Name {
		t.Fatalf("adapter = %+v, want codex", plan.Adapter)
	}
	if plan.ExecutionProfile != "external_implementation" {
		t.Fatalf("execution profile = %q, want external_implementation", plan.ExecutionProfile)
	}
	for _, part := range []string{"Build feature", "External implementer", "Codex"} {
		if !strings.Contains(plan.SessionTitle, part) {
			t.Fatalf("session title = %q, missing %q", plan.SessionTitle, part)
		}
	}
}

func TestApplication_ResolveExternalAgentAssignmentLaunchPlanErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workspace := t.TempDir()
	project := launchPlanTestProject(workspace)
	workItem := launchPlanTestWorkItem()

	for _, tc := range []struct {
		name    string
		profile agentprofiles.Profile
		want    string
		adapter string
	}{
		{
			name: "missing external kind",
			profile: agentprofiles.Profile{
				ID:      "prof_missing_kind",
				Name:    "Missing kind",
				Surface: agentprofiles.SurfaceExternalAgent,
			},
			want: LaunchPlanUnprocessable,
		},
		{
			name: "unknown adapter",
			profile: agentprofiles.Profile{
				ID:                "prof_unknown_adapter",
				Name:              "Unknown adapter",
				Surface:           agentprofiles.SurfaceExternalAgent,
				ExternalAgentKind: "does_not_exist",
			},
			want:    LaunchPlanAdapterNotFound,
			adapter: "does_not_exist",
		},
		{
			name: "unknown launch option",
			profile: agentprofiles.Profile{
				ID:                   "prof_bad_option",
				Name:                 "Bad option",
				Surface:              agentprofiles.SurfaceExternalAgent,
				ExternalAgentKind:    "codex",
				ExternalAgentOptions: map[string]string{"definitely_unknown": "x"},
			},
			want: LaunchPlanInvalidRequest,
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			profiles := agentprofiles.NewMemoryStore()
			if _, err := profiles.Create(ctx, tc.profile); err != nil {
				t.Fatalf("Create(profile) error = %v", err)
			}
			app := New(Options{ProfileStore: profiles})
			role := projectwork.AgentRoleProfile{
				ID:                  "role_ext",
				Name:                "External implementer",
				DefaultAgentProfile: tc.profile.ID,
			}
			_, err := app.ResolveExternalAgentAssignmentLaunchPlan(ctx, project, workItem, launchPlanTestAssignment(), role)
			var launchErr LaunchPlanError
			if !errors.As(err, &launchErr) || launchErr.Kind != tc.want {
				t.Fatalf("ResolveExternalAgentAssignmentLaunchPlan() error = %v, want %s", err, tc.want)
			}
			if launchErr.AdapterID != tc.adapter {
				t.Fatalf("adapter id = %q, want %q", launchErr.AdapterID, tc.adapter)
			}
		})
	}
}

func launchPlanTestProject(workspace string) projects.Project {
	return projects.Project{
		ID:                   "proj_1",
		Name:                 "Hecate",
		DefaultRootID:        "root_1",
		DefaultWorkspaceMode: "in_place",
		DefaultSystemPrompt:  "Follow project conventions.",
		Roots: []projects.Root{{
			ID:     "root_1",
			Path:   workspace,
			Kind:   "git",
			Active: true,
		}},
		ContextSources: []projects.ContextSource{{
			ID:         "ctx_agents",
			Kind:       "workspace_instruction",
			Title:      "Agents",
			Path:       "AGENTS.md",
			Enabled:    true,
			Format:     "agents_md",
			TrustLabel: "workspace_guidance",
		}},
	}
}

func launchPlanTestWorkItem() projectwork.WorkItem {
	return projectwork.WorkItem{
		ID:       "work_1",
		Title:    "Build feature",
		Brief:    "Ship the feature.",
		Status:   "ready",
		Priority: "high",
	}
}

func launchPlanTestAssignment() projectwork.Assignment {
	return projectwork.Assignment{
		ID:         "asgn_1",
		WorkItemID: "work_1",
		RoleID:     "role_backend",
		Status:     projectwork.AssignmentStatusQueued,
		DriverKind: projectwork.AssignmentDriverHecateTask,
	}
}

func boolForLaunchPlanTest(value bool) *bool {
	return &value
}

func launchPlanWarningContains(items []string, want string) bool {
	for _, item := range items {
		if strings.Contains(item, want) {
			return true
		}
	}
	return false
}
