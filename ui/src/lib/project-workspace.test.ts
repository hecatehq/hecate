import { describe, expect, it } from "vitest";

import { projectByID, projectDefaultWorkspace } from "./project-workspace";
import type { ProjectRecord, ProjectRootRecord } from "../types/project";

function root(overrides: Partial<ProjectRootRecord>): ProjectRootRecord {
  return {
    id: "root_1",
    path: "/workspace/default",
    kind: "workspace",
    active: false,
    created_at: "2026-05-29T00:00:00Z",
    updated_at: "2026-05-29T00:00:00Z",
    ...overrides,
  };
}

function project(overrides: Partial<ProjectRecord>): ProjectRecord {
  return {
    id: "proj_1",
    name: "Hecate",
    roots: [],
    created_at: "2026-05-29T00:00:00Z",
    updated_at: "2026-05-29T00:00:00Z",
    ...overrides,
  };
}

describe("project workspace helpers", () => {
  it("uses the default root before active or first roots", () => {
    const record = project({
      default_root_id: "root_default",
      roots: [
        root({ id: "root_active", path: "/workspace/active", active: true }),
        root({ id: "root_default", path: "/workspace/default" }),
      ],
    });

    expect(projectDefaultWorkspace(record)).toBe("/workspace/default");
  });

  it("falls back to the active root, then the first root", () => {
    expect(
      projectDefaultWorkspace(
        project({
          roots: [
            root({ id: "root_first", path: "/workspace/first" }),
            root({ id: "root_active", path: "/workspace/active", active: true }),
          ],
        }),
      ),
    ).toBe("/workspace/active");

    expect(
      projectDefaultWorkspace(
        project({
          roots: [root({ id: "root_first", path: "/workspace/first" })],
        }),
      ),
    ).toBe("/workspace/first");
  });

  it("returns an empty workspace for no project or root", () => {
    expect(projectDefaultWorkspace(null)).toBe("");
    expect(projectDefaultWorkspace(project({ roots: [] }))).toBe("");
  });

  it("finds projects by trimmed id", () => {
    const record = project({ id: "proj_hecate" });

    expect(projectByID([record], " proj_hecate ")).toBe(record);
    expect(projectByID([record], "")).toBeNull();
    expect(projectByID([record], "missing")).toBeNull();
  });
});
