import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { AgentPresetRecord } from "../../types/agent-preset";
import type { ProjectRecord, ProjectSkillRecord, ProjectWorkRoleRecord } from "../../types/project";
import { AgentPresetsModal } from "./AgentPresetsModal";

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

function preset(overrides: Partial<AgentPresetRecord> = {}): AgentPresetRecord {
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

describe("AgentPresetsModal", () => {
  it("creates a preset with selected project skills", async () => {
    const onCreate = vi.fn(async (form) => preset({ id: form.id, name: form.name }));

    render(
      <AgentPresetsModal
        error=""
        pending={false}
        presets={[]}
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

  it("updates the selected preset", async () => {
    const onUpdate = vi.fn(async (presetID, form) =>
      preset({ id: presetID, name: form.name, instructions: form.instructions }),
    );

    render(
      <AgentPresetsModal
        error=""
        pending={false}
        presets={[preset()]}
        project={project()}
        projectSkills={[]}
        roles={[]}
        onClose={vi.fn()}
        onCreate={vi.fn()}
        onDelete={vi.fn()}
        onUpdate={onUpdate}
      />,
    );

    const selectedPreset = screen.getByRole("button", { name: "Implementation" });
    expect(selectedPreset).toHaveAttribute("aria-pressed", "true");
    expect(selectedPreset).not.toHaveClass("btn-primary");
    expect(
      screen.getByRole("dialog", { name: "Agent presets" }).querySelectorAll(".btn-primary"),
    ).toHaveLength(1);

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

  it("requires exact origins before saving browser evidence and clears them when disabled", async () => {
    const onCreate = vi.fn(async (form) => preset({ id: form.id, name: form.name }));

    render(
      <AgentPresetsModal
        error=""
        pending={false}
        presets={[]}
        project={project()}
        projectSkills={[]}
        roles={[]}
        onClose={vi.fn()}
        onCreate={onCreate}
        onDelete={vi.fn()}
        onUpdate={vi.fn()}
      />,
    );

    await userEvent.type(screen.getByLabelText("Preset id"), "browser-review");
    await userEvent.type(screen.getByLabelText("Name"), "Browser review");
    await userEvent.click(screen.getByLabelText("Browser evidence allowed"));

    expect(screen.getByLabelText("Allowed browser origins")).toBeInTheDocument();
    expect(screen.getByText(/Add at least one exact origin/i)).toHaveAttribute("role", "alert");
    expect(screen.getByRole("button", { name: "Create preset" })).toBeDisabled();

    await userEvent.type(
      screen.getByLabelText("Allowed browser origins"),
      "https://app.example.test",
    );
    await userEvent.click(screen.getByLabelText("Browser evidence allowed"));
    expect(screen.queryByLabelText("Allowed browser origins")).toBeNull();
    await userEvent.click(screen.getByLabelText("Browser evidence allowed"));
    expect(screen.getByLabelText("Allowed browser origins")).toHaveValue("");
    await userEvent.type(
      screen.getByLabelText("Allowed browser origins"),
      "https://app.example.test",
    );
    await userEvent.click(screen.getByRole("button", { name: "Create preset" }));

    expect(onCreate).toHaveBeenCalledWith(
      expect.objectContaining({
        browserAllowed: true,
        browserAllowedOrigins: "https://app.example.test",
      }),
    );
  });

  it("shows unavailable browser-runtime guidance without blocking a valid preset", async () => {
    render(
      <AgentPresetsModal
        browserEvidenceReadiness={{
          available: false,
          status: "not_configured",
          message: "Native browser evidence is not configured on this runtime.",
          operator_action: "Set HECATE_TASK_BROWSER_EXECUTABLE, then restart Hecate.",
        }}
        error=""
        pending={false}
        presets={[]}
        project={project()}
        projectSkills={[]}
        roles={[]}
        onClose={vi.fn()}
        onCreate={vi.fn()}
        onDelete={vi.fn()}
        onUpdate={vi.fn()}
      />,
    );

    await userEvent.type(screen.getByLabelText("Preset id"), "browser-review");
    await userEvent.type(screen.getByLabelText("Name"), "Browser review");
    await userEvent.click(screen.getByLabelText("Browser evidence allowed"));
    await userEvent.type(
      screen.getByLabelText("Allowed browser origins"),
      "https://app.example.test/",
    );

    expect(screen.getByText(/Browser runtime unavailable/i)).toHaveTextContent(
      "Set HECATE_TASK_BROWSER_EXECUTABLE",
    );
    expect(screen.getByRole("button", { name: "Create preset" })).toBeEnabled();
  });

  it("rejects malformed browser origins in the form before save", async () => {
    render(
      <AgentPresetsModal
        error=""
        pending={false}
        presets={[]}
        project={project()}
        projectSkills={[]}
        roles={[]}
        onClose={vi.fn()}
        onCreate={vi.fn()}
        onDelete={vi.fn()}
        onUpdate={vi.fn()}
      />,
    );

    await userEvent.type(screen.getByLabelText("Preset id"), "browser-review");
    await userEvent.type(screen.getByLabelText("Name"), "Browser review");
    await userEvent.click(screen.getByLabelText("Browser evidence allowed"));
    const origins = screen.getByLabelText("Allowed browser origins");
    await userEvent.type(origins, "https://operator:secret@app.example.test/path?token=secret");

    expect(screen.getByRole("alert")).toHaveTextContent("Use exact http(s) origins only");
    expect(screen.getByRole("button", { name: "Create preset" })).toBeDisabled();
    expect(origins).toHaveAttribute("aria-invalid", "true");

    await userEvent.clear(origins);
    await userEvent.type(origins, "https://app.example.test/");
    expect(screen.getByRole("button", { name: "Create preset" })).toBeEnabled();
  });

  it("keeps browser evidence unavailable for an external-agent-only preset", async () => {
    render(
      <AgentPresetsModal
        error=""
        pending={false}
        presets={[]}
        project={project()}
        projectSkills={[]}
        roles={[]}
        onClose={vi.fn()}
        onCreate={vi.fn()}
        onDelete={vi.fn()}
        onUpdate={vi.fn()}
      />,
    );

    await userEvent.selectOptions(screen.getByLabelText("Surface"), "external_agent");
    expect(screen.getByLabelText("Browser evidence allowed")).toBeDisabled();
    expect(
      screen.getByText(/External Agents and Hecate Chat do not receive browser evidence/i),
    ).toBeInTheDocument();
  });

  it("clears browser evidence when tools are disabled", async () => {
    render(
      <AgentPresetsModal
        error=""
        pending={false}
        presets={[]}
        project={project()}
        projectSkills={[]}
        roles={[]}
        onClose={vi.fn()}
        onCreate={vi.fn()}
        onDelete={vi.fn()}
        onUpdate={vi.fn()}
      />,
    );

    await userEvent.click(screen.getByLabelText("Browser evidence allowed"));
    await userEvent.type(
      screen.getByLabelText("Allowed browser origins"),
      "https://app.example.test",
    );
    await userEvent.click(screen.getByLabelText("Tools enabled"));

    expect(screen.getByLabelText("Browser evidence allowed")).toBeDisabled();
    expect(screen.queryByLabelText("Allowed browser origins")).toBeNull();
    expect(screen.getByText(/Enable Tools to configure browser evidence/i)).toBeInTheDocument();
  });

  it("shows built-in presets as read-only", () => {
    render(
      <AgentPresetsModal
        error=""
        pending={false}
        presets={[
          preset({
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

  it("locks dismissal and repeat submission while a preset is saving", async () => {
    const onClose = vi.fn();
    const onUpdate = vi.fn();

    render(
      <AgentPresetsModal
        error=""
        pending
        presets={[preset()]}
        project={project()}
        projectSkills={[]}
        roles={[]}
        onClose={onClose}
        onCreate={vi.fn()}
        onDelete={vi.fn()}
        onUpdate={onUpdate}
      />,
    );

    expect(screen.getByRole("button", { name: "Close" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Saving…" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Implementation" })).toBeDisabled();
    expect(screen.getByLabelText("Name")).toBeDisabled();
    await userEvent.keyboard("{Escape}");
    expect(onClose).not.toHaveBeenCalled();
    expect(onUpdate).not.toHaveBeenCalled();
  });

  it("confirms deletion with project and role references", async () => {
    const onDelete = vi.fn(async () => true);

    render(
      <AgentPresetsModal
        error=""
        pending={false}
        presets={[preset()]}
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
