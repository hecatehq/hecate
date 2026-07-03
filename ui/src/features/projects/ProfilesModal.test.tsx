import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { AgentProfileRecord } from "../../types/agent-profile";
import type { ProjectRecord, ProjectSkillRecord, ProjectWorkRoleRecord } from "../../types/project";
import { ProfilesModal } from "./ProfilesModal";

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

function profile(overrides: Partial<AgentProfileRecord> = {}): AgentProfileRecord {
  return {
    id: "implementation",
    name: "Implementation",
    surface: "any",
    tools_enabled: true,
    writes_allowed: false,
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

describe("ProfilesModal", () => {
  it("creates a profile with selected project skills", async () => {
    const onCreate = vi.fn(async (form) => profile({ id: form.id, name: form.name }));

    render(
      <ProfilesModal
        error=""
        pending={false}
        profiles={[]}
        project={project()}
        projectSkills={[skill()]}
        roles={[]}
        onClose={vi.fn()}
        onCreate={onCreate}
        onDelete={vi.fn()}
        onUpdate={vi.fn()}
      />,
    );

    await userEvent.type(screen.getByLabelText("Preset id"), "implementation");
    await userEvent.type(screen.getByLabelText("Name"), "Implementation");
    await userEvent.click(screen.getByLabelText("Use skill Backend"));
    await userEvent.click(screen.getByRole("button", { name: "Create preset" }));

    expect(onCreate).toHaveBeenCalledWith(
      expect.objectContaining({
        id: "implementation",
        name: "Implementation",
        skillIDs: "backend",
        surface: "any",
        toolsEnabled: true,
      }),
    );
  });

  it("updates the selected profile", async () => {
    const onUpdate = vi.fn(async (profileID, form) =>
      profile({ id: profileID, name: form.name, instructions: form.instructions }),
    );

    render(
      <ProfilesModal
        error=""
        pending={false}
        profiles={[profile()]}
        project={project()}
        projectSkills={[]}
        roles={[]}
        onClose={vi.fn()}
        onCreate={vi.fn()}
        onDelete={vi.fn()}
        onUpdate={onUpdate}
      />,
    );

    await userEvent.clear(screen.getByLabelText("Name"));
    await userEvent.type(screen.getByLabelText("Name"), "Implementation lead");
    await userEvent.type(screen.getByLabelText("Instructions"), "Ship the scoped change.");
    await userEvent.click(screen.getByRole("button", { name: "Save preset" }));

    expect(onUpdate).toHaveBeenCalledWith(
      "implementation",
      expect.objectContaining({
        name: "Implementation lead",
        instructions: "Ship the scoped change.",
      }),
    );
  });

  it("shows built-in profiles as read-only", () => {
    render(
      <ProfilesModal
        error=""
        pending={false}
        profiles={[
          profile({
            id: "implementation",
            name: "Implementation",
            built_in: true,
            writes_allowed: true,
          }),
        ]}
        project={project()}
        projectSkills={[skill()]}
        roles={[]}
        onClose={vi.fn()}
        onCreate={vi.fn()}
        onDelete={vi.fn()}
        onUpdate={vi.fn()}
      />,
    );

    expect(screen.getAllByText("built-in").length).toBeGreaterThan(0);
    expect(screen.getByText("Built-in preset")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save preset" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Delete preset" })).not.toBeInTheDocument();
    expect(screen.getByLabelText("Name")).toBeDisabled();
    expect(screen.getByLabelText("Use skill Backend")).toBeDisabled();
  });

  it("confirms deletion with project and role references", async () => {
    const onDelete = vi.fn(async () => true);

    render(
      <ProfilesModal
        error=""
        pending={false}
        profiles={[profile()]}
        project={project({ default_agent_profile: "implementation" })}
        projectSkills={[]}
        roles={[role({ default_agent_profile: "implementation" })]}
        onClose={vi.fn()}
        onCreate={vi.fn()}
        onDelete={onDelete}
        onUpdate={vi.fn()}
      />,
    );

    await userEvent.click(screen.getByRole("button", { name: "Delete preset" }));

    expect(
      screen.getByText(
        /Referenced by this project's default preset; roles Developer\. Those references will fall back until changed\./,
      ),
    ).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Delete agent preset" }));

    expect(onDelete).toHaveBeenCalledWith(expect.objectContaining({ id: "implementation" }));
  });
});
