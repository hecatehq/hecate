import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { ProjectRecord, ProjectRootRecord } from "../../types/project";
import { ProjectRootSelect } from "./ProjectRootSelect";

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
      root({ id: "root_main", path: "/workspace/main", git_branch: "main" }),
      root({
        id: "root_feature",
        path: "/workspace/feature",
        kind: "git_worktree",
        git_branch: "feature/ui",
      }),
    ],
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
    ...overrides,
  };
}

describe("ProjectRootSelect", () => {
  it("renders nothing when the project has no roots", () => {
    const { container } = render(
      <ProjectRootSelect
        inheritLabel="Use work item root"
        project={project({ roots: [] })}
        value=""
        onChange={vi.fn()}
      />,
    );

    expect(container).toBeEmptyDOMElement();
  });

  it("emits the selected root id with labelled root options", () => {
    const onChange = vi.fn();

    render(
      <ProjectRootSelect
        inheritLabel="Use work item root"
        label="Assignment root"
        project={project()}
        value=""
        onChange={onChange}
      />,
    );

    expect(screen.getByRole("option", { name: "Use work item root" })).toHaveValue("");
    expect(
      screen.getByRole("option", { name: "/workspace/feature · git:feature/ui · git_worktree" }),
    ).toHaveValue("root_feature");

    fireEvent.change(screen.getByLabelText("Assignment root"), {
      target: { value: "root_feature" },
    });

    expect(onChange).toHaveBeenCalledWith("root_feature");
  });
});
