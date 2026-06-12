import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { ProjectRecord, ProjectRootRecord } from "../../types/project";
import { ProjectSettingsPanel } from "./ProjectSettingsPanel";

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
  it("saves editable defaults and updates the workspace preview from form roots", async () => {
    const onSave = vi.fn();

    render(
      <ProjectSettingsPanel
        agentProfiles={[]}
        agentProfilesError=""
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

    fireEvent.change(screen.getByLabelText("Default project root"), {
      target: { value: "root_feature" },
    });

    expect(screen.getAllByText("/workspace/feature")).toHaveLength(2);

    await userEvent.click(screen.getByRole("button", { name: "Save defaults" }));

    expect(onSave).toHaveBeenCalledWith(
      expect.objectContaining({
        defaultRootID: "root_feature",
        roots: expect.arrayContaining([
          expect.objectContaining({ id: "root_feature", active: true }),
        ]),
      }),
    );
  });
});
