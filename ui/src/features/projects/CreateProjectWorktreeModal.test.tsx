import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { ProjectRecord, ProjectRootRecord } from "../../types/project";
import { CreateProjectWorktreeModal } from "./CreateProjectWorktreeModal";

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

function project(): ProjectRecord {
  return {
    id: "proj_1",
    name: "Hecate",
    roots: [
      root({ id: "root_main", path: "/workspace/main", active: true }),
      root({
        id: "root_default",
        path: "/workspace/default",
        kind: "git_worktree",
        git_branch: "main",
        active: true,
      }),
    ],
    default_root_id: "root_default",
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
  };
}

describe("CreateProjectWorktreeModal", () => {
  it("submits a worktree request from the selected project root defaults", async () => {
    const onCreate = vi.fn();

    render(
      <CreateProjectWorktreeModal
        error=""
        pending={false}
        project={project()}
        onClose={vi.fn()}
        onCreate={onCreate}
      />,
    );

    const createButton = screen.getByRole("button", { name: "Create worktree" });
    expect(createButton).toBeDisabled();

    fireEvent.change(screen.getByLabelText("Branch"), {
      target: { value: "feature/project-settings" },
    });
    fireEvent.change(screen.getByLabelText("Start point"), {
      target: { value: "origin/master" },
    });
    fireEvent.change(screen.getByLabelText("Path"), {
      target: { value: ".worktrees/project-settings" },
    });
    await userEvent.click(screen.getByLabelText("Make default root"));
    await userEvent.click(createButton);

    expect(onCreate).toHaveBeenCalledWith({
      baseRootID: "root_default",
      branch: "feature/project-settings",
      startPoint: "origin/master",
      path: ".worktrees/project-settings",
      active: true,
      setDefault: true,
    });
  });
});
