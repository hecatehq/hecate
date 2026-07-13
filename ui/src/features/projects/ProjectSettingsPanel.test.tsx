import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { ProjectRecord, ProjectRootRecord } from "../../types/project";
import { ProjectSettingsPanel } from "./ProjectSettingsPanel";
import { chooseWorkspaceDirectory } from "../../lib/api";

vi.mock("../../lib/api", () => ({
  chooseWorkspaceDirectory: vi.fn(),
}));

function root(overrides: Partial<ProjectRootRecord>): ProjectRootRecord {
  return {
    id: "root_main",
    path: "/workspace/main",
    kind: "workspace",
    active: true,
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
    ...overrides,
  };
}

function project(overrides: Partial<ProjectRecord> = {}): ProjectRecord {
  return {
    id: "proj_1",
    name: "Hecate",
    roots: [
      root({ id: "root_main", path: "/workspace/main", active: true }),
      root({
        id: "root_feature",
        path: "/workspace/feature",
        kind: "git_worktree",
        git_branch: "feature/project-settings",
        active: false,
      }),
    ],
    default_root_id: "root_main",
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
    ...overrides,
  };
}

describe("ProjectSettingsPanel", () => {
  beforeEach(() => {
    vi.mocked(chooseWorkspaceDirectory).mockReset();
  });

  it("saves editable defaults and updates the workspace preview from form roots", async () => {
    const onSave = vi.fn();

    render(
      <ProjectSettingsPanel
        agentPresets={[]}
        agentPresetsError=""
        error=""
        models={[]}
        pending={false}
        providerOptions={[]}
        providerPresets={[]}
        project={project()}
        rootsPending={false}
        onDiscoverRoots={vi.fn()}
        onOpenCreateWorktree={vi.fn()}
        onSave={onSave}
      />,
    );

    fireEvent.change(screen.getByLabelText("Default folder"), {
      target: { value: "root_feature" },
    });

    expect(screen.getAllByText("/workspace/feature")).toHaveLength(2);

    await userEvent.click(screen.getByRole("button", { name: "Save settings" }));

    expect(onSave).toHaveBeenCalledWith(
      expect.objectContaining({
        defaultRootID: "root_feature",
        workspaceMode: "",
        roots: expect.arrayContaining([
          expect.objectContaining({ id: "root_feature", active: true }),
        ]),
      }),
    );
  });

  it("adds an optional folder root to rootless projects", async () => {
    const onSave = vi.fn();
    vi.mocked(chooseWorkspaceDirectory).mockResolvedValue({
      object: "workspace_dialog",
      data: { path: "/workspace/research", branch: "" },
    });

    render(
      <ProjectSettingsPanel
        agentPresets={[]}
        agentPresetsError=""
        error=""
        models={[]}
        pending={false}
        providerOptions={[]}
        providerPresets={[]}
        project={project({ roots: [], default_root_id: "" })}
        rootsPending={false}
        onDiscoverRoots={vi.fn()}
        onOpenCreateWorktree={vi.fn()}
        onSave={onSave}
      />,
    );

    expect(screen.getByText("No folders attached.")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Create worktree" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Find worktrees" })).toBeDisabled();

    await userEvent.click(screen.getByRole("button", { name: "Add folder" }));
    expect(await screen.findAllByText("/workspace/research")).toHaveLength(2);
    await userEvent.click(screen.getByRole("button", { name: "Save settings" }));

    expect(onSave).toHaveBeenCalledWith(
      expect.objectContaining({
        roots: [
          expect.objectContaining({
            path: "/workspace/research",
            kind: "local",
            active: true,
          }),
        ],
      }),
    );
  });

  it.each([
    ["Isolated copy (recommended)", ""],
    ["Isolated copy (ephemeral setting)", "ephemeral"],
    ["Isolated copy (persistent setting)", "persistent"],
    ["Attached folder (writes directly)", "in_place"],
  ])("maps %s to the exact workspace value", async (label, value) => {
    const onSave = vi.fn();
    render(
      <ProjectSettingsPanel
        agentPresets={[]}
        agentPresetsError=""
        error=""
        models={[]}
        pending={false}
        providerOptions={[]}
        providerPresets={[]}
        project={project({ default_workspace_mode: value === "" ? "persistent" : "" })}
        rootsPending={false}
        onDiscoverRoots={vi.fn()}
        onOpenCreateWorktree={vi.fn()}
        onSave={onSave}
      />,
    );

    await userEvent.selectOptions(screen.getByLabelText("Workspace behavior"), label);
    await userEvent.click(screen.getByRole("button", { name: "Save settings" }));

    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ workspaceMode: value }));
  });

  it("preserves an unknown isolated workspace value on unrelated saves", async () => {
    const onSave = vi.fn();
    render(
      <ProjectSettingsPanel
        agentPresets={[]}
        agentPresetsError=""
        error=""
        models={[]}
        pending={false}
        providerOptions={[]}
        providerPresets={[]}
        project={project({ default_workspace_mode: "future_clone" })}
        rootsPending={false}
        onDiscoverRoots={vi.fn()}
        onOpenCreateWorktree={vi.fn()}
        onSave={onSave}
      />,
    );

    expect(screen.getByLabelText("Workspace behavior")).toHaveValue("future_clone");
    expect(screen.getByRole("option", { name: "Isolated copy (future_clone)" })).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Default folder"), {
      target: { value: "root_feature" },
    });
    await userEvent.click(screen.getByRole("button", { name: "Save settings" }));

    expect(onSave).toHaveBeenCalledWith(
      expect.objectContaining({ workspaceMode: "future_clone", defaultRootID: "root_feature" }),
    );
  });
});
