package projectskills

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/projects"
)

func TestDiscoverFindsLocalAndGuidanceLinkedSkillMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	writeSkillFile(t, root, ".agents/skills/backend/SKILL.md", "---\nname: Backend Engineer\ndescription: Build backend slices.\n---\n# Ignored\n")
	writeSkillFile(t, root, ".hecate/skills/qa/SKILL.md", "# QA Review\n")
	writeSkillFile(t, root, "docs-ai/skills/research/SKILL.md", "# Research\n")
	writeSkillFile(t, root, "claude-skills/review/SKILL.md", "---\ntitle: Claude Review\ndescription: Host-specific review posture.\n---\n")
	writeFileForDiscoveryTest(t, root, "AGENTS.md", "Use [`docs-ai/skills/research/SKILL.md`](docs-ai/skills/research/SKILL.md).")
	writeFileForDiscoveryTest(t, root, "CLAUDE.md", "Use [`claude-skills/review/SKILL.md`](claude-skills/review/SKILL.md).")
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
		},
	})
	if len(warnings) != 0 {
		t.Fatalf("warnings = %+v, want none", warnings)
	}
	if len(skills) != 4 {
		t.Fatalf("skills = %+v, want four discovered skills", skills)
	}
	assertSkillForDiscoveryTest(t, skills, "backend", "Backend Engineer", "Build backend slices.", ".agents/skills/backend/SKILL.md", nil)
	assertSkillForDiscoveryTest(t, skills, "qa", "QA Review", "", ".hecate/skills/qa/SKILL.md", nil)
	assertSkillForDiscoveryTest(t, skills, "research", "Research", "", "docs-ai/skills/research/SKILL.md", []string{"ctx_agents"})
	assertSkillForDiscoveryTest(t, skills, "review", "Claude Review", "Host-specific review posture.", "claude-skills/review/SKILL.md", []string{"ctx_claude"})
	if findSkillForTest(skills, "ignore") != nil {
		t.Fatalf("skills = %+v, want unreferenced skill root ignored", skills)
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
