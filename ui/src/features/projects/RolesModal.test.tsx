import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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
  it("quick-creates a responsibility with advanced settings disclosed on demand", async () => {
    const created = role({ id: "researcher", name: "Researcher" });
    const onCreate = vi.fn(async () => created);
    const onCreated = vi.fn();

    render(
      <RolesModal
        agentPresets={[preset()]}
        error=""
        mode="quick-create"
        pending={false}
        projectSkills={[skill()]}
        roles={[]}
        onClose={vi.fn()}
        onCreate={onCreate}
        onCreated={onCreated}
        onDelete={vi.fn()}
        onUpdate={vi.fn()}
      />,
    );

    const dialog = screen.getByRole("dialog", { name: "Add responsibility" });
    expect(within(dialog).getByLabelText("Name")).toHaveFocus();
    expect(within(dialog).queryByRole("button", { name: "New custom role" })).toBeNull();
    const advanced = within(dialog)
      .getByText("Instructions & execution defaults")
      .closest("details");
    expect(advanced).not.toHaveAttribute("open");
    await userEvent.type(within(dialog).getByLabelText("Name"), "Researcher");
    await userEvent.type(within(dialog).getByLabelText("Description"), "Owns discovery work.");
    await userEvent.click(within(dialog).getByText("Instructions & execution defaults"));
    fireEvent.change(within(dialog).getByLabelText("Default destination"), {
      target: { value: "manual" },
    });
    await userEvent.click(within(dialog).getByRole("button", { name: "Add responsibility" }));

    expect(onCreate).toHaveBeenCalledWith(
      expect.objectContaining({
        name: "Researcher",
        description: "Owns discovery work.",
        defaultDriverKind: "manual",
      }),
    );
    expect(onCreated).toHaveBeenCalledWith(created);
  });

  it("keeps a quick responsibility draft when creation fails", async () => {
    const onCreate = vi.fn(async () => undefined);

    render(
      <RolesModal
        agentPresets={[]}
        error="Could not add responsibility."
        mode="quick-create"
        pending={false}
        projectSkills={[]}
        roles={[]}
        onClose={vi.fn()}
        onCreate={onCreate}
        onDelete={vi.fn()}
        onUpdate={vi.fn()}
      />,
    );

    const name = screen.getByLabelText("Name");
    await userEvent.type(name, "Researcher");
    const submit = screen.getByRole("button", { name: "Add responsibility" });
    await userEvent.click(submit);

    expect(onCreate).toHaveBeenCalledTimes(1);
    expect(name).toHaveValue("Researcher");
    expect(screen.getByRole("alert")).toHaveTextContent("Could not add responsibility.");
    expect(submit).toHaveFocus();
  });

  it("prevents dismissing a responsibility while it is being added", async () => {
    const onClose = vi.fn();
    const onCreate = vi.fn();

    render(
      <RolesModal
        agentPresets={[]}
        error=""
        mode="quick-create"
        pending
        projectSkills={[]}
        roles={[]}
        onClose={onClose}
        onCreate={onCreate}
        onDelete={vi.fn()}
        onUpdate={vi.fn()}
      />,
    );

    expect(
      screen.getByRole("dialog", { name: "Add responsibility" }).querySelector("form"),
    ).toHaveAttribute("aria-busy", "true");
    expect(screen.getByRole("button", { name: "Adding…" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Close" })).toBeDisabled();
    fireEvent.submit(
      screen.getByRole("dialog", { name: "Add responsibility" }).querySelector("form")!,
    );
    await userEvent.keyboard("{Escape}");
    expect(onCreate).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
  });

  it("submits a responsibility only once while creation is in flight", async () => {
    const created = role({ id: "researcher", name: "Researcher" });
    let resolveCreate = (_role: ProjectWorkRoleRecord | undefined) => {};
    const creation = new Promise<ProjectWorkRoleRecord | undefined>((resolve) => {
      resolveCreate = resolve;
    });
    const onCreate = vi.fn(() => creation);
    const onCreated = vi.fn();

    render(
      <RolesModal
        agentPresets={[]}
        error=""
        mode="quick-create"
        pending={false}
        projectSkills={[]}
        roles={[]}
        onClose={vi.fn()}
        onCreate={onCreate}
        onCreated={onCreated}
        onDelete={vi.fn()}
        onUpdate={vi.fn()}
      />,
    );

    const name = screen.getByLabelText("Name");
    await userEvent.type(name, "Researcher");
    const form = name.closest("form")!;
    fireEvent.submit(form);
    fireEvent.submit(form);

    expect(onCreate).toHaveBeenCalledTimes(1);
    resolveCreate(created);
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith(created));
  });

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

    expect(screen.getByRole("button", { name: "New custom role" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getByRole("button", { name: "Create role" })).not.toHaveAttribute("aria-pressed");
    await userEvent.type(screen.getByLabelText("Name"), "Designer");
    const destination = screen.getByLabelText("Default destination");
    expect(screen.getByRole("option", { name: "Human" })).toBeTruthy();
    expect(screen.getByRole("option", { name: "Hecate Task" })).toBeTruthy();
    expect(screen.getByRole("option", { name: "External Agent" })).toBeTruthy();
    fireEvent.change(destination, {
      target: { value: "manual" },
    });
    expect(destination).toHaveAccessibleDescription(
      "Track work completed by a person outside Hecate.",
    );
    fireEvent.change(screen.getByLabelText("Default preset"), {
      target: { value: "implementation" },
    });
    await userEvent.click(screen.getByLabelText("Use skill Backend"));
    await userEvent.click(screen.getByRole("button", { name: "Create role" }));

    expect(onCreate).toHaveBeenCalledWith(
      expect.objectContaining({
        name: "Designer",
        defaultDriverKind: "manual",
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

    expect(screen.getByRole("button", { name: "Developer" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getByRole("button", { name: "Save role" })).not.toHaveAttribute("aria-pressed");
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
