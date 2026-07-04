import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { AgentPresetRecord } from "../../types/agent-preset";
import type { ProjectSkillRecord, ProjectWorkRoleRecord } from "../../types/project";
import { RolesModal } from "./RolesModal";

function preset(overrides: Partial<AgentPresetRecord> = {}): AgentPresetRecord {
  return {
    id: "implementation",
    name: "Implementation",
    surface: "hecate_task",
    tools_enabled: true,
    writes_allowed: true,
    network_allowed: false,
    approval_policy: "inherit",
    project_memory_policy: "inherit",
    context_source_policy: "inherit",
    ...overrides,
  };
}

function skill(overrides: Partial<ProjectSkillRecord> = {}): ProjectSkillRecord {
  return {
    id: "backend",
    project_id: "proj_1",
    title: "Backend",
    description: "Backend guidance",
    path: "docs-ai/skills/backend/SKILL.md",
    format: "skill",
    enabled: true,
    status: "available",
    trust_label: "workspace_skill",
    discovered_at: "2026-06-12T00:00:00Z",
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
    ...overrides,
  };
}

function role(overrides: Partial<ProjectWorkRoleRecord> = {}): ProjectWorkRoleRecord {
  return {
    id: "developer",
    project_id: "proj_1",
    name: "Developer",
    built_in: false,
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
    ...overrides,
  };
}

describe("RolesModal", () => {
  it("creates a custom role with defaults and project skills", async () => {
    const onCreate = vi.fn(async (form) => role({ id: "designer", name: form.name }));

    render(
      <RolesModal
        agentPresets={[preset()]}
        error=""
        pending={false}
        projectSkills={[skill()]}
        roles={[role({ id: "reviewer", name: "Reviewer", built_in: true })]}
        onClose={vi.fn()}
        onCreate={onCreate}
        onDelete={vi.fn()}
        onUpdate={vi.fn()}
      />,
    );

    await userEvent.type(screen.getByLabelText("Name"), "Designer");
    fireEvent.change(screen.getByLabelText("Default driver"), {
      target: { value: "hecate_task" },
    });
    fireEvent.change(screen.getByLabelText("Default preset"), {
      target: { value: "implementation" },
    });
    await userEvent.click(screen.getByLabelText("Use skill Backend"));
    await userEvent.click(screen.getByRole("button", { name: "Create role" }));

    expect(onCreate).toHaveBeenCalledWith(
      expect.objectContaining({
        name: "Designer",
        defaultDriverKind: "hecate_task",
        defaultAgentPreset: "implementation",
        skillIDs: "backend",
      }),
    );
  });

  it("updates the first editable role", async () => {
    const onUpdate = vi.fn(async (roleID, form) =>
      role({ id: roleID, name: form.name, description: form.description }),
    );

    render(
      <RolesModal
        agentPresets={[]}
        error=""
        pending={false}
        projectSkills={[]}
        roles={[role({ id: "developer", name: "Developer", description: "Old" })]}
        onClose={vi.fn()}
        onCreate={vi.fn()}
        onDelete={vi.fn()}
        onUpdate={onUpdate}
      />,
    );

    await userEvent.clear(screen.getByLabelText("Description"));
    await userEvent.type(screen.getByLabelText("Description"), "Owns scoped implementation work.");
    await userEvent.click(screen.getByRole("button", { name: "Save role" }));

    expect(onUpdate).toHaveBeenCalledWith(
      "developer",
      expect.objectContaining({
        name: "Developer",
        description: "Owns scoped implementation work.",
      }),
    );
  });

  it("keeps built-in roles read-only", async () => {
    render(
      <RolesModal
        agentPresets={[]}
        error=""
        pending={false}
        projectSkills={[]}
        roles={[
          role({ id: "developer", name: "Developer" }),
          role({ id: "reviewer", name: "Reviewer", built_in: true }),
        ]}
        onClose={vi.fn()}
        onCreate={vi.fn()}
        onDelete={vi.fn()}
        onUpdate={vi.fn()}
      />,
    );

    await userEvent.click(screen.getByRole("button", { name: /Reviewer/ }));

    expect(screen.getByText(/Built-in roles are read-only/)).toBeInTheDocument();
    expect(screen.getByLabelText("Name")).toBeDisabled();
    expect(screen.getByRole("button", { name: "Save role" })).toBeDisabled();
  });
});
