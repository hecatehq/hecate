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
    const onClose = vi.fn();

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
        onClose={onClose}
        onDiscoverRoots={vi.fn()}
        onOpenCreateWorktree={vi.fn()}
        onSave={onSave}
      />,
    );

    expect(screen.getByRole("heading", { level: 1, name: "Project settings" })).toHaveFocus();
    expect(screen.getByRole("heading", { level: 2, name: "Launch defaults" })).toBeTruthy();
    expect(screen.getByRole("heading", { level: 2, name: "Local files" })).toBeTruthy();

    fireEvent.change(screen.getByLabelText("Default folder"), {
      target: { value: "persisted-root:root_feature" },
    });

    expect(screen.getAllByText("/workspace/feature")).toHaveLength(2);

    await userEvent.click(screen.getByRole("button", { name: "Save settings" }));

    expect(onSave).toHaveBeenCalledWith(
      expect.objectContaining({
        defaultRootID: "persisted-root:root_feature",
        workspaceMode: "",
        roots: expect.arrayContaining([
          expect.objectContaining({ id: "root_feature", active: false }),
        ]),
      }),
    );

    await userEvent.click(screen.getByRole("button", { name: "Back to project" }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("keeps dirty defaults while merging newly authoritative roots", async () => {
    const mainRoot = root({ id: "root_main", path: "/workspace/main", active: true });
    const discoveredRoot = root({
      id: "root_discovered",
      path: "/workspace/discovered",
      kind: "git_worktree",
      git_branch: "feature/discovered",
      active: true,
    });
    const original = project({
      default_workspace_mode: "in_place",
      roots: [mainRoot],
    });
    const props = {
      agentPresets: [],
      agentPresetsError: "",
      error: "",
      models: [],
      pending: false,
      providerOptions: [],
      providerPresets: [],
      project: original,
      rootsPending: false,
      onClose: vi.fn(),
      onDiscoverRoots: vi.fn(),
      onOpenCreateWorktree: vi.fn(),
      onSave: vi.fn(),
    };
    const { rerender } = render(<ProjectSettingsPanel {...props} />);

    await userEvent.selectOptions(screen.getByLabelText("Workspace behavior"), "persistent");
    expect(screen.getByLabelText("Workspace behavior")).toHaveValue("persistent");

    rerender(
      <ProjectSettingsPanel
        {...props}
        project={{
          ...original,
          name: "Hecate refreshed",
          default_workspace_mode: "ephemeral",
          roots: [mainRoot, discoveredRoot],
          updated_at: "2026-06-12T00:01:00Z",
        }}
      />,
    );

    expect(screen.getByLabelText("Workspace behavior")).toHaveValue("persistent");
    expect(screen.getByText("/workspace/discovered")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Save settings" })).toBeEnabled();
    await userEvent.click(screen.getByRole("button", { name: "Save settings" }));
    expect(props.onSave).toHaveBeenCalledWith(
      expect.objectContaining({
        workspaceMode: "persistent",
        roots: expect.arrayContaining([
          expect.objectContaining({ id: "root_discovered", path: "/workspace/discovered" }),
        ]),
      }),
    );
  });

  it("keeps a local activation change while accepting refreshed root metadata", async () => {
    const mainRoot = root({
      id: "root_main",
      path: "/workspace/main",
      kind: "local",
      git_remote: "git@example.com:old/hecate.git",
      git_branch: "main",
      active: true,
    });
    const original = project({ roots: [mainRoot] });
    const onSave = vi.fn();
    const props = {
      agentPresets: [],
      agentPresetsError: "",
      error: "",
      models: [],
      pending: false,
      providerOptions: [],
      providerPresets: [],
      project: original,
      rootsPending: false,
      onClose: vi.fn(),
      onDiscoverRoots: vi.fn(),
      onOpenCreateWorktree: vi.fn(),
      onSave,
    };
    const { rerender } = render(<ProjectSettingsPanel {...props} />);

    await userEvent.click(screen.getByLabelText("Active project root /workspace/main"));

    rerender(
      <ProjectSettingsPanel
        {...props}
        project={{
          ...original,
          roots: [
            {
              ...mainRoot,
              kind: "git",
              git_remote: "git@example.com:current/hecate.git",
              git_branch: "feature/current",
              updated_at: "2026-06-12T00:01:00Z",
            },
          ],
          updated_at: "2026-06-12T00:01:00Z",
        }}
      />,
    );

    expect(screen.getByLabelText("Active project root /workspace/main")).not.toBeChecked();
    await userEvent.click(screen.getByRole("button", { name: "Save settings" }));
    expect(onSave).toHaveBeenCalledWith(
      expect.objectContaining({
        roots: [
          expect.objectContaining({
            id: "root_main",
            kind: "git",
            git_remote: "git@example.com:current/hecate.git",
            git_branch: "feature/current",
            active: false,
          }),
        ],
      }),
    );
  });

  it("drops an authoritatively removed root despite a local activation change", async () => {
    const mainRoot = root({ id: "root_main", path: "/workspace/main", active: true });
    const removedRoot = root({
      id: "root_removed",
      path: "/workspace/removed",
      kind: "git_worktree",
      git_branch: "feature/removed",
      active: false,
    });
    const original = project({ roots: [mainRoot, removedRoot] });
    const onSave = vi.fn();
    const props = {
      agentPresets: [],
      agentPresetsError: "",
      error: "",
      models: [],
      pending: false,
      providerOptions: [],
      providerPresets: [],
      project: original,
      rootsPending: false,
      onClose: vi.fn(),
      onDiscoverRoots: vi.fn(),
      onOpenCreateWorktree: vi.fn(),
      onSave,
    };
    const { rerender } = render(<ProjectSettingsPanel {...props} />);

    await userEvent.click(screen.getByLabelText("Active project root /workspace/removed"));

    rerender(
      <ProjectSettingsPanel
        {...props}
        project={{
          ...original,
          roots: [mainRoot],
          updated_at: "2026-06-12T00:01:00Z",
        }}
      />,
    );

    expect(screen.queryByText("/workspace/removed")).toBeNull();
    await userEvent.selectOptions(screen.getByLabelText("Workspace behavior"), "persistent");
    await userEvent.click(screen.getByRole("button", { name: "Save settings" }));
    expect(onSave).toHaveBeenCalledWith(
      expect.objectContaining({
        roots: [expect.objectContaining({ id: "root_main" })],
      }),
    );
  });

  it("rebases a removed dirty default root to the authoritative default", async () => {
    const mainRoot = root({ id: "root_main", path: "/workspace/main", active: true });
    const removedRoot = root({
      id: "root_removed",
      path: "/workspace/removed",
      kind: "git_worktree",
      git_branch: "feature/removed",
      active: false,
    });
    const original = project({ roots: [mainRoot, removedRoot] });
    const onSave = vi.fn();
    const props = {
      agentPresets: [],
      agentPresetsError: "",
      error: "",
      models: [],
      pending: false,
      providerOptions: [],
      providerPresets: [],
      project: original,
      rootsPending: false,
      onClose: vi.fn(),
      onDiscoverRoots: vi.fn(),
      onOpenCreateWorktree: vi.fn(),
      onSave,
    };
    const { rerender } = render(<ProjectSettingsPanel {...props} />);

    await userEvent.selectOptions(
      screen.getByLabelText("Default folder"),
      "persisted-root:root_removed",
    );

    rerender(
      <ProjectSettingsPanel
        {...props}
        project={{
          ...original,
          roots: [mainRoot],
          default_root_id: "root_main",
          updated_at: "2026-06-12T00:01:00Z",
        }}
      />,
    );

    expect(screen.getByLabelText("Default folder")).toHaveValue("persisted-root:root_main");
    await userEvent.selectOptions(screen.getByLabelText("Workspace behavior"), "persistent");
    await userEvent.click(screen.getByRole("button", { name: "Save settings" }));
    expect(onSave).toHaveBeenCalledWith(
      expect.objectContaining({
        defaultRootID: "persisted-root:root_main",
        roots: [expect.objectContaining({ id: "root_main" })],
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
        onClose={vi.fn()}
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
        defaultRootID: "unsaved-root:/workspace/research",
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
        onClose={vi.fn()}
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
        onClose={vi.fn()}
        onDiscoverRoots={vi.fn()}
        onOpenCreateWorktree={vi.fn()}
        onSave={onSave}
      />,
    );

    expect(screen.getByLabelText("Workspace behavior")).toHaveValue("future_clone");
    expect(screen.getByRole("option", { name: "Existing setting (future_clone)" })).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Default folder"), {
      target: { value: "persisted-root:root_feature" },
    });
    await userEvent.click(screen.getByRole("button", { name: "Save settings" }));

    expect(onSave).toHaveBeenCalledWith(
      expect.objectContaining({
        workspaceMode: "future_clone",
        defaultRootID: "persisted-root:root_feature",
      }),
    );
  });

  it("preserves Cairnline's default folder independently from active state", async () => {
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
        project={project({
          roots: [
            root({ id: "root_main", path: "/workspace/main", active: true }),
            root({ id: "root_feature", path: "/workspace/feature", active: true }),
          ],
        })}
        rootsPending={false}
        onClose={vi.fn()}
        onDiscoverRoots={vi.fn()}
        onOpenCreateWorktree={vi.fn()}
        onSave={onSave}
      />,
    );

    expect(screen.queryByRole("option", { name: "No default folder" })).toBeNull();
    await userEvent.click(
      screen.getByRole("checkbox", { name: "Active project root /workspace/main" }),
    );
    expect(
      screen.getByRole("checkbox", { name: "Active project root /workspace/main" }),
    ).not.toBeChecked();
    expect(screen.getByLabelText("Default folder")).toHaveValue("persisted-root:root_main");

    await userEvent.click(screen.getByRole("button", { name: "Save settings" }));
    expect(onSave).toHaveBeenCalledWith(
      expect.objectContaining({
        defaultRootID: "persisted-root:root_main",
        roots: expect.arrayContaining([
          expect.objectContaining({ id: "root_main", active: false }),
          expect.objectContaining({ id: "root_feature", active: true }),
        ]),
      }),
    );
  });

  it("locks navigation and all drafts while saving", async () => {
    const onClose = vi.fn();
    const onSave = vi.fn();
    render(
      <ProjectSettingsPanel
        agentPresets={[]}
        agentPresetsError=""
        error=""
        models={[]}
        pending
        providerOptions={[]}
        providerPresets={[]}
        project={project()}
        rootsPending={false}
        onClose={onClose}
        onDiscoverRoots={vi.fn()}
        onOpenCreateWorktree={vi.fn()}
        onSave={onSave}
      />,
    );

    expect(screen.getByRole("heading", { level: 1, name: "Project settings" })).toHaveFocus();
    expect(screen.getByRole("button", { name: "Back to project" })).toBeDisabled();
    expect(screen.getByLabelText("Workspace behavior")).toBeDisabled();
    expect(screen.getByLabelText("Default folder")).toBeDisabled();
    expect(screen.getByRole("button", { name: "Add folder" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Saving…" })).toBeDisabled();
    expect(
      screen.getByRole("checkbox", { name: "Active project root /workspace/main" }),
    ).toBeDisabled();
    fireEvent.submit(screen.getByLabelText("Workspace behavior").closest("form")!);
    await userEvent.keyboard("{Escape}");
    expect(onSave).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
  });
});
