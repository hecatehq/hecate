import { act, renderHook } from "@testing-library/react";
import { type ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ProjectsProvider, useProjects } from "./projects";
import {
  chooseWorkspaceDirectory,
  createProject,
  deleteProject,
  getProjects,
  updateProject,
} from "../../lib/api";
import type { ProjectRecord } from "../../types/project";

vi.mock("../../lib/api", () => ({
  chooseWorkspaceDirectory: vi.fn(),
  createProject: vi.fn(),
  deleteProject: vi.fn(),
  getProjects: vi.fn(),
  updateProject: vi.fn(),
}));

const project: ProjectRecord = {
  id: "proj_1",
  name: "Hecate",
  roots: [
    {
      id: "root_1",
      path: "/Users/alice/dev/hecate",
      kind: "local",
      active: true,
      created_at: "2026-05-21T10:00:00Z",
      updated_at: "2026-05-21T10:00:00Z",
    },
  ],
  created_at: "2026-05-21T10:00:00Z",
  updated_at: "2026-05-21T10:00:00Z",
};

function wrapper({ children }: { children: ReactNode }) {
  return <ProjectsProvider>{children}</ProjectsProvider>;
}

describe("ProjectsProvider", () => {
  beforeEach(() => {
    window.localStorage.clear();
    vi.mocked(chooseWorkspaceDirectory).mockReset();
    vi.mocked(createProject).mockReset();
    vi.mocked(deleteProject).mockReset();
    vi.mocked(getProjects).mockReset();
    vi.mocked(updateProject).mockReset();
  });

  it("loads projects without auto-selecting one", async () => {
    vi.mocked(getProjects).mockResolvedValue({ object: "projects", data: [project] });
    const { result } = renderHook(() => useProjects(), { wrapper });

    await act(async () => {
      await result.current.actions.loadProjects();
    });

    expect(result.current.state.projects).toEqual([project]);
    expect(result.current.activeProjectID).toBe("");
    expect(window.localStorage.getItem("hecate.project")).toBeNull();
  });

  it("clears a stale persisted project id after loading current projects", async () => {
    window.localStorage.setItem("hecate.project", "proj_old");
    vi.mocked(getProjects).mockResolvedValue({ object: "projects", data: [project] });
    const { result } = renderHook(() => useProjects(), { wrapper });

    await act(async () => {
      await result.current.actions.loadProjects();
    });

    expect(result.current.activeProjectID).toBe("");
    expect(window.localStorage.getItem("hecate.project")).toBeNull();
  });

  it("treats selecting No project as a first-class context", async () => {
    window.localStorage.setItem("hecate.project", project.id);
    const { result } = renderHook(() => useProjects(), { wrapper });

    await act(async () => {
      await result.current.actions.selectProject("");
    });

    expect(result.current.activeProjectID).toBe("");
    expect(window.localStorage.getItem("hecate.project")).toBeNull();
    expect(updateProject).not.toHaveBeenCalled();
  });

  it("renames a project and upserts the returned record", async () => {
    const renamed = { ...project, name: "New name" };
    vi.mocked(updateProject).mockResolvedValue({ object: "project", data: renamed });
    const { result } = renderHook(() => useProjects(), {
      wrapper: ({ children }) => (
        <ProjectsProvider initialState={{ projects: [project] }}>{children}</ProjectsProvider>
      ),
    });

    await act(async () => {
      await result.current.actions.renameProject(project.id, " New name ");
    });

    expect(updateProject).toHaveBeenCalledWith(project.id, { name: "New name" });
    expect(result.current.state.projects).toEqual([renamed]);
  });

  it("deletes a project and clears the active project when needed", async () => {
    window.localStorage.setItem("hecate.project", project.id);
    vi.mocked(deleteProject).mockResolvedValue();
    const { result } = renderHook(() => useProjects(), {
      wrapper: ({ children }) => (
        <ProjectsProvider initialState={{ projects: [project] }}>{children}</ProjectsProvider>
      ),
    });

    await act(async () => {
      await result.current.actions.deleteProject(project.id);
    });

    expect(deleteProject).toHaveBeenCalledWith(project.id);
    expect(result.current.state.projects).toEqual([]);
    expect(result.current.activeProjectID).toBe("");
    expect(window.localStorage.getItem("hecate.project")).toBeNull();
  });

  it("returns false and keeps local state when project deletion fails", async () => {
    window.localStorage.setItem("hecate.project", project.id);
    vi.mocked(deleteProject).mockRejectedValue(new Error("delete failed"));
    const { result } = renderHook(() => useProjects(), {
      wrapper: ({ children }) => (
        <ProjectsProvider initialState={{ projects: [project] }}>{children}</ProjectsProvider>
      ),
    });

    let deleted = true;
    await act(async () => {
      deleted = await result.current.actions.deleteProject(project.id);
    });

    expect(deleted).toBe(false);
    expect(result.current.state.projects).toEqual([project]);
    expect(result.current.activeProjectID).toBe(project.id);
    expect(result.current.state.error).toBe("delete failed");
  });
});
