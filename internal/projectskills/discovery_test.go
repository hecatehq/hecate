package projectskills

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/projects"
)

func TestDiscoverFindsLocalAndGuidanceLinkedSkillMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	writeSkillFile(t, root, ".agents/skills/backend/SKILL.md", "---\nname: Backend Engineer\ndescription: Build backend slices.\n---\n# Ignored\n")
	writeSkillFile(t, root, ".hecate/skills/qa/SKILL.md", "# QA Review\n")
	writeSkillFile(t, root, ".cairnline/skills/planning/SKILL.md", "# Planning\n")
	writeSkillFile(t, root, ".claude/skills/debug/SKILL.md", "# Claude Debug\n")
	writeSkillFile(t, root, ".gemini/skills/research-gemini/SKILL.md", "# Gemini Research\n")
	writeSkillFile(t, root, "docs-ai/skills/research/SKILL.md", "# Research\n")
	writeSkillFile(t, root, "claude-skills/review/SKILL.md", "---\ntitle: Claude Review\ndescription: Host-specific review posture.\n---\n")
	writeSkillFile(t, root, "gemini-skills/release/SKILL.md", "# Release Coordination\n")
	writeSkillFile(t, root, ".worktrees/refactor/docs-ai/skills/backend/SKILL.md", "# Worktree Backend\n")
	writeSkillFile(t, root, ".claude/worktrees/refactor/docs-ai/skills/qa/SKILL.md", "# Claude Worktree QA\n")
	writeFileForDiscoveryTest(t, root, "AGENTS.md", "Use [`docs-ai/skills/research/SKILL.md`](docs-ai/skills/research/SKILL.md).")
	writeFileForDiscoveryTest(t, root, "CLAUDE.md", "Use [`claude-skills/review/SKILL.md`](claude-skills/review/SKILL.md).")
	writeFileForDiscoveryTest(t, root, "GEMINI.md", "Use [`gemini-skills/release/SKILL.md`](gemini-skills/release/SKILL.md).")
	writeFileForDiscoveryTest(t, root, ".worktrees/refactor/AGENTS.md", "Use `.worktrees/refactor/docs-ai/skills/backend/SKILL.md`.")
	writeFileForDiscoveryTest(t, root, ".claude/worktrees/refactor/AGENTS.md", "Use `.claude/worktrees/refactor/docs-ai/skills/qa/SKILL.md`.")
	writeSkillFile(t, root, "unreferenced/skills/ignore/SKILL.md", "# Ignore\n")

	skills, warnings := Discover(ctx, projects.Project{
		ID: "proj_skills",
		Roots: []projects.Root{{
			ID:     "root_a",
			Path:   root,
			Kind:   "git",
			Active: true,
		}},
		ContextSources: []projects.ContextSource{
			{
				ID:      "ctx_agents",
				Kind:    "workspace_instruction",
				Path:    "AGENTS.md",
				Enabled: true,
				Format:  "agents_md",
				Metadata: map[string]string{
					"root_id": "root_a",
				},
			},
			{
				ID:      "ctx_claude",
				Kind:    "host_instruction",
				Path:    "CLAUDE.md",
				Enabled: true,
				Format:  "claude_md",
				Metadata: map[string]string{
					"root_id": "root_a",
				},
			},
			{
				ID:      "ctx_gemini",
				Kind:    "host_instruction",
				Path:    "GEMINI.md",
				Enabled: true,
				Format:  "gemini_md",
				Metadata: map[string]string{
					"root_id": "root_a",
				},
			},
			{
				ID:      "ctx_worktree",
				Kind:    "workspace_instruction",
				Path:    ".worktrees/refactor/AGENTS.md",
				Enabled: true,
				Format:  "agents_md",
				Metadata: map[string]string{
					"root_id": "root_a",
				},
			},
			{
				ID:      "ctx_claude_worktree",
				Kind:    "workspace_instruction",
				Path:    ".claude/worktrees/refactor/AGENTS.md",
				Enabled: true,
				Format:  "agents_md",
				Metadata: map[string]string{
					"root_id": "root_a",
				},
			},
		},
	})
	if len(warnings) != 0 {
		t.Fatalf("warnings = %+v, want none", warnings)
	}
	if len(skills) != 8 {
		t.Fatalf("skills = %+v, want eight discovered skills", skills)
	}
	assertSkillForDiscoveryTest(t, skills, "backend", "Backend Engineer", "Build backend slices.", ".agents/skills/backend/SKILL.md", nil)
	assertSkillForDiscoveryTest(t, skills, "qa", "QA Review", "", ".hecate/skills/qa/SKILL.md", nil)
	assertSkillForDiscoveryTest(t, skills, "planning", "Planning", "", ".cairnline/skills/planning/SKILL.md", nil)
	assertSkillForDiscoveryTest(t, skills, "debug", "Claude Debug", "", ".claude/skills/debug/SKILL.md", nil)
	assertSkillForDiscoveryTest(t, skills, "research-gemini", "Gemini Research", "", ".gemini/skills/research-gemini/SKILL.md", nil)
	assertSkillForDiscoveryTest(t, skills, "research", "Research", "", "docs-ai/skills/research/SKILL.md", []string{"ctx_agents"})
	assertSkillForDiscoveryTest(t, skills, "review", "Claude Review", "Host-specific review posture.", "claude-skills/review/SKILL.md", []string{"ctx_claude"})
	assertSkillForDiscoveryTest(t, skills, "release", "Release Coordination", "", "gemini-skills/release/SKILL.md", []string{"ctx_gemini"})
	if findSkillForTest(skills, "ignore") != nil {
		t.Fatalf("skills = %+v, want unreferenced skill root ignored", skills)
	}
}

func TestDiscoverSkipsWorktreeGuidanceAndSkillRoots(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSkillFile(t, root, ".agents/skills/backend/SKILL.md", "# Backend\n")
	writeSkillFile(t, root, ".worktrees/branch/docs-ai/skills/backend/SKILL.md", "# Worktree Backend\n")
	writeSkillFile(t, root, ".claude/worktrees/branch/docs-ai/skills/frontend/SKILL.md", "# Worktree Frontend\n")
	writeFileForDiscoveryTest(t, root, ".worktrees/branch/AGENTS.md", "Use `.worktrees/branch/docs-ai/skills/backend/SKILL.md`.")
	writeFileForDiscoveryTest(t, root, "AGENTS.md", "Use `.claude/worktrees/branch/docs-ai/skills/frontend/SKILL.md`.")

	skills, warnings := Discover(context.Background(), projects.Project{
		ID: "proj_worktree_skills",
		Roots: []projects.Root{{
			ID:     "root_a",
			Path:   root,
			Active: true,
		}},
		ContextSources: []projects.ContextSource{
			{
				ID:      "ctx_stale_worktree",
				Kind:    "workspace_instruction",
				Path:    ".worktrees/branch/AGENTS.md",
				Enabled: true,
				Format:  "agents_md",
				Metadata: map[string]string{
					"root_id": "root_a",
				},
			},
			{
				ID:      "ctx_agents",
				Kind:    "workspace_instruction",
				Path:    "AGENTS.md",
				Enabled: true,
				Format:  "agents_md",
				Metadata: map[string]string{
					"root_id": "root_a",
				},
			},
		},
	})
	if len(warnings) != 0 {
		t.Fatalf("warnings = %+v, want none", warnings)
	}
	if len(skills) != 1 {
		t.Fatalf("skills = %+v, want only non-worktree skill", skills)
	}
	if skills[0].ID != "backend" || skills[0].Path != ".agents/skills/backend/SKILL.md" || skills[0].Status != StatusAvailable {
		t.Fatalf("skill = %+v, want canonical backend skill only", skills[0])
	}
}

func TestDiscoverMatchesCairnlinePortableSkillDiscoverySubset(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	writeSkillFile(t, root, ".agents/skills/backend/SKILL.md", `---
name: Backend Engineer
description: Build backend slices.
hecate:
  suggested_tools:
    - git.diff
    - file.read
  required_permissions:
    tools: true
    writes: false
    network: true
---
# Ignored
`)
	writeSkillFile(t, root, ".cairnline/skills/planning/SKILL.md", "# Planning\n")
	writeSkillFile(t, root, ".claude/skills/debug/SKILL.md", "# Claude Debug\n")
	writeSkillFile(t, root, ".gemini/skills/research-gemini/SKILL.md", "# Gemini Research\n")
	writeSkillFile(t, root, ".hecate/skills/qa/SKILL.md", "# QA Review\n")
	writeSkillFile(t, root, "docs-ai/skills/research/SKILL.md", "# Research\n")
	writeSkillFile(t, root, "claude-skills/review/SKILL.md", "---\ntitle: Claude Review\ndescription: Host-specific review posture.\n---\n")
	writeSkillFile(t, root, "gemini-skills/release/SKILL.md", "# Release Coordination\n")
	writeFileForDiscoveryTest(t, root, "AGENTS.md", "Use [`docs-ai/skills/research/SKILL.md`](docs-ai/skills/research/SKILL.md).")
	writeFileForDiscoveryTest(t, root, "CLAUDE.md", "Use [`claude-skills/review/SKILL.md`](claude-skills/review/SKILL.md).")
	writeFileForDiscoveryTest(t, root, "GEMINI.md", "Use [`gemini-skills/release/SKILL.md`](gemini-skills/release/SKILL.md).")
	writeFileForDiscoveryTest(t, root, "docs/AGENTS.md", "Use `.cairnline/skills/planning/SKILL.md`.")

	hecateProject := projects.Project{
		ID: "proj_skills",
		Roots: []projects.Root{{
			ID:     "root_a",
			Path:   root,
			Kind:   "git",
			Active: true,
		}},
		ContextSources: []projects.ContextSource{
			{
				ID:      "ctx_agents",
				Kind:    "workspace_instruction",
				Title:   "AGENTS.md",
				Path:    "AGENTS.md",
				Enabled: true,
				Format:  "agents_md",
				Metadata: map[string]string{
					"root_id": "root_a",
				},
			},
			{
				ID:      "ctx_claude",
				Kind:    "host_instruction",
				Title:   "CLAUDE.md",
				Path:    "CLAUDE.md",
				Enabled: true,
				Format:  "claude_md",
				Metadata: map[string]string{
					"root_id": "root_a",
				},
			},
			{
				ID:      "ctx_gemini",
				Kind:    "host_instruction",
				Title:   "GEMINI.md",
				Path:    "GEMINI.md",
				Enabled: true,
				Format:  "gemini_md",
				Metadata: map[string]string{
					"root_id": "root_a",
				},
			},
			{
				ID:      "ctx_nested",
				Kind:    "workspace_instruction",
				Title:   "Nested AGENTS.md",
				Path:    "docs/AGENTS.md",
				Enabled: true,
				Format:  "agents_md",
				Metadata: map[string]string{
					"root_id": "root_a",
				},
			},
		},
	}
	hecateSkills, hecateWarnings := Discover(ctx, hecateProject)
	if len(hecateWarnings) != 0 {
		t.Fatalf("Hecate Discover warnings = %+v, want none", hecateWarnings)
	}

	service := cairnline.NewMemoryService()
	cairnlineProject, err := service.CreateProject(ctx, cairnline.Project{
		ID:   "proj_skills",
		Name: "Skill discovery parity",
		Roots: []cairnline.Root{{
			ID:     "root_a",
			Path:   root,
			Kind:   "git",
			Active: true,
		}},
		ContextSources: []cairnline.Source{
			{
				ID:      "ctx_agents",
				Kind:    "workspace_instruction",
				Title:   "AGENTS.md",
				Locator: "AGENTS.md",
				Enabled: true,
				Format:  "agents_md",
				Metadata: map[string]string{
					"root_id": "root_a",
				},
			},
			{
				ID:      "ctx_claude",
				Kind:    "host_instruction",
				Title:   "CLAUDE.md",
				Locator: "CLAUDE.md",
				Enabled: true,
				Format:  "claude_md",
				Metadata: map[string]string{
					"root_id": "root_a",
				},
			},
			{
				ID:      "ctx_gemini",
				Kind:    "host_instruction",
				Title:   "GEMINI.md",
				Locator: "GEMINI.md",
				Enabled: true,
				Format:  "gemini_md",
				Metadata: map[string]string{
					"root_id": "root_a",
				},
			},
			{
				ID:      "ctx_nested",
				Kind:    "workspace_instruction",
				Title:   "Nested AGENTS.md",
				Locator: "docs/AGENTS.md",
				Enabled: true,
				Format:  "agents_md",
				Metadata: map[string]string{
					"root_id": "root_a",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Cairnline CreateProject() error = %v", err)
	}
	cairnlineSkills, err := service.DiscoverProjectSkills(ctx, cairnlineProject.ID)
	if err != nil {
		t.Fatalf("Cairnline DiscoverProjectSkills() error = %v", err)
	}

	got := portableSkillDiscoverySnapshotFromHecate(hecateSkills)
	want := portableSkillDiscoverySnapshotFromCairnline(cairnlineSkills)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("portable skill discovery snapshot mismatch\nHecate:   %#v\nCairnline: %#v", got, want)
	}
}

func TestDiscoverMarksDuplicateSkillIDsAsConflict(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSkillFile(t, root, ".agents/skills/review/SKILL.md", "# Review\n")
	writeSkillFile(t, root, ".hecate/skills/review/SKILL.md", "# Review Again\n")

	skills, warnings := Discover(context.Background(), projects.Project{
		ID: "proj_conflict",
		Roots: []projects.Root{{
			ID:     "root_a",
			Path:   root,
			Active: true,
		}},
	})
	if len(warnings) != 0 {
		t.Fatalf("warnings = %+v, want none", warnings)
	}
	if len(skills) != 1 {
		t.Fatalf("skills = %+v, want one conflict record", skills)
	}
	if skills[0].ID != "review" || skills[0].Status != StatusConflict || len(skills[0].Warnings) == 0 {
		t.Fatalf("skill = %+v, want conflict warning", skills[0])
	}
}

func TestDiscoverParsesHecateCapabilityHints(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSkillFile(t, root, ".hecate/skills/review/SKILL.md", `---
title: Review
description: Review implementation output.
suggested_tools:
  - shell.exec
required_permissions:
  network: true
hecate:
  suggested_tools:
    - git.diff
    - file.read
  required_permissions:
    tools: true
    writes: false
    network: false
---
# Ignored
`)

	skills, warnings := Discover(context.Background(), projects.Project{
		ID: "proj_capabilities",
		Roots: []projects.Root{{
			ID:     "root_a",
			Path:   root,
			Active: true,
		}},
	})
	if len(warnings) != 0 {
		t.Fatalf("warnings = %+v, want none", warnings)
	}
	review := findSkillForTest(skills, "review")
	if review == nil {
		t.Fatalf("skills = %+v, missing review", skills)
	}
	if review.Title != "Review" || review.Description != "Review implementation output." {
		t.Fatalf("review metadata = %+v, want frontmatter title and description", review)
	}
	if strings.Join(review.SuggestedTools, ",") != "file.read,git.diff" {
		t.Fatalf("suggested tools = %+v, want sorted frontmatter tools", review.SuggestedTools)
	}
	if review.RequiredPermissions.Tools == nil || !*review.RequiredPermissions.Tools {
		t.Fatalf("required tools = %+v, want true", review.RequiredPermissions.Tools)
	}
	if review.RequiredPermissions.Writes == nil || *review.RequiredPermissions.Writes {
		t.Fatalf("required writes = %+v, want false", review.RequiredPermissions.Writes)
	}
	if review.RequiredPermissions.Network == nil || *review.RequiredPermissions.Network {
		t.Fatalf("required network = %+v, want false", review.RequiredPermissions.Network)
	}
}

func TestDiscoverBoundsGuidanceAndSkillMetadata(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFileForDiscoveryTest(t, root, "AGENTS.md", strings.Repeat("x", guidanceMaxBytes+1))
	writeSkillFile(t, root, ".hecate/skills/huge/SKILL.md", strings.Repeat("x", skillMaxBytes+1))

	skills, warnings := Discover(context.Background(), projects.Project{
		ID: "proj_bounds",
		Roots: []projects.Root{{
			ID:     "root_a",
			Path:   root,
			Active: true,
		}},
		ContextSources: []projects.ContextSource{{
			ID:      "ctx_agents",
			Kind:    "workspace_instruction",
			Path:    "AGENTS.md",
			Enabled: true,
			Format:  "agents_md",
		}},
	})
	if len(warnings) != 2 {
		t.Fatalf("warnings = %+v, want guidance and skill bounds warnings", warnings)
	}
	huge := findSkillForTest(skills, "huge")
	if huge == nil || huge.Status != StatusInvalid || len(huge.Warnings) == 0 {
		t.Fatalf("huge skill = %+v, want invalid metadata warning", huge)
	}
}

func TestDiscoverWarnsForNonAbsoluteProjectRoot(t *testing.T) {
	t.Parallel()
	skills, warnings := Discover(context.Background(), projects.Project{
		ID: "proj_relative",
		Roots: []projects.Root{{
			ID:     "root_relative",
			Path:   "relative/path",
			Active: true,
		}},
	})
	if len(skills) != 0 {
		t.Fatalf("skills = %+v, want none", skills)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "non-absolute project root root_relative") {
		t.Fatalf("warnings = %+v, want non-absolute root warning", warnings)
	}
}

func assertSkillForDiscoveryTest(t *testing.T, skills []Skill, id, title, description, path string, sourceIDs []string) {
	t.Helper()
	skill := findSkillForTest(skills, id)
	if skill == nil {
		t.Fatalf("skills = %+v, missing %s", skills, id)
	}
	if skill.Title != title || skill.Description != description || skill.Path != path || skill.Status != StatusAvailable || !skill.Enabled || skill.TrustLabel != TrustWorkspaceSkill {
		t.Fatalf("skill %s = %+v, want metadata title=%q desc=%q path=%q", id, *skill, title, description, path)
	}
	if strings.Join(skill.SourceContextSourceIDs, ",") != strings.Join(sourceIDs, ",") {
		t.Fatalf("skill %s source ids = %+v, want %+v", id, skill.SourceContextSourceIDs, sourceIDs)
	}
}

type portableSkillDiscoverySnapshot struct {
	ID                     string
	Title                  string
	Description            string
	Path                   string
	RootID                 string
	Format                 string
	SuggestedTools         []string
	RequiredPermissions    portableRequiredPermissionsSnapshot
	Enabled                bool
	Status                 string
	TrustLabel             string
	SourceContextSourceIDs []string
	Warnings               []string
}

type portableRequiredPermissionsSnapshot struct {
	Tools   *bool
	Writes  *bool
	Network *bool
}

func portableSkillDiscoverySnapshotFromHecate(skills []Skill) []portableSkillDiscoverySnapshot {
	out := make([]portableSkillDiscoverySnapshot, 0, len(skills))
	for _, skill := range skills {
		out = append(out, portableSkillDiscoverySnapshot{
			ID:                     skill.ID,
			Title:                  skill.Title,
			Description:            skill.Description,
			Path:                   skill.Path,
			RootID:                 skill.RootID,
			Format:                 skill.Format,
			SuggestedTools:         append([]string(nil), skill.SuggestedTools...),
			RequiredPermissions:    portableRequiredPermissionsSnapshot{Tools: skill.RequiredPermissions.Tools, Writes: skill.RequiredPermissions.Writes, Network: skill.RequiredPermissions.Network},
			Enabled:                skill.Enabled,
			Status:                 skill.Status,
			TrustLabel:             skill.TrustLabel,
			SourceContextSourceIDs: append([]string(nil), skill.SourceContextSourceIDs...),
			Warnings:               append([]string(nil), skill.Warnings...),
		})
	}
	sortPortableSkillDiscoverySnapshots(out)
	return out
}

func portableSkillDiscoverySnapshotFromCairnline(skills []cairnline.ProjectSkill) []portableSkillDiscoverySnapshot {
	out := make([]portableSkillDiscoverySnapshot, 0, len(skills))
	for _, skill := range skills {
		out = append(out, portableSkillDiscoverySnapshot{
			ID:                     skill.ID,
			Title:                  skill.Title,
			Description:            skill.Description,
			Path:                   skill.Path,
			RootID:                 skill.RootID,
			Format:                 skill.Format,
			SuggestedTools:         append([]string(nil), skill.SuggestedTools...),
			RequiredPermissions:    portableRequiredPermissionsSnapshot{Tools: skill.RequiredPermissions.Tools, Writes: skill.RequiredPermissions.Writes, Network: skill.RequiredPermissions.Network},
			Enabled:                skill.Enabled,
			Status:                 skill.Status,
			TrustLabel:             skill.TrustLabel,
			SourceContextSourceIDs: append([]string(nil), skill.SourceRefs...),
			Warnings:               append([]string(nil), skill.Warnings...),
		})
	}
	sortPortableSkillDiscoverySnapshots(out)
	return out
}

func sortPortableSkillDiscoverySnapshots(items []portableSkillDiscoverySnapshot) {
	slices.SortFunc(items, func(a, b portableSkillDiscoverySnapshot) int {
		return strings.Compare(a.ID, b.ID)
	})
}

func writeSkillFile(t *testing.T, root, rel, body string) {
	t.Helper()
	writeFileForDiscoveryTest(t, root, rel, body)
}

func writeFileForDiscoveryTest(t *testing.T, root, rel, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}
