import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { ProjectRecord, ProjectSkillRecord } from "../../types/project";
import { ProjectSkillsPanel } from "./ProjectSkillsPanel";

function project(overrides: Partial<ProjectRecord> = {}): ProjectRecord {
  return {
    id: "proj_1",
    name: "Hecate",
    roots: [],
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
    ...overrides,
  };
}

function skill(overrides: Partial<ProjectSkillRecord> = {}): ProjectSkillRecord {
  return {
    id: "backend",
    project_id: "proj_1",
    title: "Backend",
    description: "Build backend changes.",
    path: ".hecate/skills/backend/SKILL.md",
    root_id: "root_1",
    format: "skill_md",
    suggested_tools: ["git.diff", "file.read"],
    required_permissions: { tools: true, writes: false, network: false },
    enabled: true,
    status: "available",
    trust_label: "workspace_skill",
    source_context_source_ids: ["ctx_agents"],
    warnings: [],
    discovered_at: "2026-06-12T00:00:00Z",
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
    ...overrides,
  };
}

describe("ProjectSkillsPanel", () => {
  it("renders registered skills and dispatches refresh, discover, enable, and save actions", async () => {
    const onDiscover = vi.fn();
    const onRefresh = vi.fn();
    const onUpdate = vi.fn();

    render(
      <ProjectSkillsPanel
        discovering={false}
        error=""
        loading={false}
        onDiscover={onDiscover}
        onRefresh={onRefresh}
        onUpdate={onUpdate}
        project={project()}
        skills={[skill()]}
        updatingSkillID=""
      />,
    );

    expect(screen.getByText("1 enabled / 1 available / 1 registered")).toBeTruthy();
    expect(screen.getByText("Build backend changes.")).toBeTruthy();
    expect(screen.getByText(/\.hecate\/skills\/backend\/SKILL\.md/)).toBeTruthy();
    expect(screen.getByText(/root root_1/)).toBeTruthy();
    expect(screen.getByText(/sources ctx_agents/)).toBeTruthy();
    expect(
      screen.getByText(
        "Suggested tools: git.diff, file.read · Required posture: tools on, writes off, network off",
      ),
    ).toBeTruthy();

    await userEvent.click(screen.getByRole("button", { name: "Refresh project skills" }));
    await userEvent.click(screen.getByRole("button", { name: "Discover" }));
    await userEvent.click(screen.getByRole("checkbox", { name: "Enable skill Backend" }));

    const titleInput = screen.getByLabelText("Title");
    await userEvent.clear(titleInput);
    await userEvent.type(titleInput, "Backend runtime");
    fireEvent.change(screen.getByLabelText("Trust label"), {
      target: { value: "operator_reviewed" },
    });
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(onRefresh).toHaveBeenCalledTimes(1);
    expect(onDiscover).toHaveBeenCalledTimes(1);
    expect(onUpdate).toHaveBeenCalledWith(expect.objectContaining({ id: "backend" }), {
      enabled: false,
    });
    expect(onUpdate).toHaveBeenCalledWith(expect.objectContaining({ id: "backend" }), {
      title: "Backend runtime",
      description: "Build backend changes.",
      trust_label: "operator_reviewed",
    });
  });

  it("renders empty state and errors", () => {
    render(
      <ProjectSkillsPanel
        discovering={false}
        error="Failed to load project skills."
        loading={false}
        onDiscover={vi.fn()}
        onRefresh={vi.fn()}
        onUpdate={vi.fn()}
        project={project()}
        skills={[]}
        updatingSkillID=""
      />,
    );

    expect(screen.getByText("Failed to load project skills.")).toBeTruthy();
    expect(screen.getByText("No project skills registered")).toBeTruthy();
    expect(
      screen.getByText(
        "Discover skills from guidance-linked roots, .agents/skills, .cairnline/skills, .claude/skills, .gemini/skills, or .hecate/skills.",
      ),
    ).toBeTruthy();
  });

  it("caps long suggested tool summaries", () => {
    render(
      <ProjectSkillsPanel
        discovering={false}
        error=""
        loading={false}
        onDiscover={vi.fn()}
        onRefresh={vi.fn()}
        onUpdate={vi.fn()}
        project={project()}
        skills={[
          skill({
            suggested_tools: [
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
            ],
          }),
        ]}
        updatingSkillID=""
      />,
    );

    expect(
      screen.getByText(
        "Suggested tools: tool.00, tool.01, tool.02, tool.03, tool.04, tool.05, tool.06, tool.07, +2 more · Required posture: tools on, writes off, network off",
      ),
    ).toBeTruthy();
  });
});
