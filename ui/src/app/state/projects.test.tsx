import { act, renderHook, waitFor } from "@testing-library/react";
import { type ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ProjectsProvider, useProjects } from "./projects";
import { createProject, deleteProject, getProjects, updateProject } from "../../lib/api";
import type { ProjectDeleteRecord, ProjectRecord } from "../../types/project";

vi.mock("../../lib/api", () => ({
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

const deleteResult = {
  object: "project_delete",
  data: {
    project_id: project.id,
    project_name: project.name,
    chat_sessions_deleted: 1,
    project_work_rows_deleted: 2,
    project_skills_deleted: 1,
    memory_entries_deleted: 3,
    memory_candidates_deleted: 4,
  },
};

function wrapper({ children }: { children: ReactNode }) {
  return <ProjectsProvider>{children}</ProjectsProvider>;
}

describe("ProjectsProvider", () => {
  beforeEach(() => {
    window.localStorage.clear();
    vi.mocked(createProject).mockReset();
    vi.mocked(deleteProject).mockReset();
    vi.mocked(getProjects).mockReset();
    vi.mocked(updateProject).mockReset();
  });

  it("loads projects without auto-selecting one", async () => {
    vi.mocked(getProjects).mockResolvedValue({ object: "projects", data: [project] });
    const { result } = renderHook(() => useProjects(), { wrapper });

    expect(result.current.state.loaded).toBe(false);

    await act(async () => {
      await result.current.actions.loadProjects();
    });

    expect(result.current.state.projects).toEqual([project]);
    expect(result.current.state.loaded).toBe(true);
    expect(result.current.activeProjectID).toBe("");
    expect(window.localStorage.getItem("hecate.project")).toBeNull();
  });

  it("keeps the catalog unresolved when its initial load fails", async () => {
    vi.mocked(getProjects).mockRejectedValue(new Error("catalog unavailable"));
    const { result } = renderHook(() => useProjects(), { wrapper });

    await act(async () => {
      await result.current.actions.loadProjects();
    });

    expect(result.current.state.loaded).toBe(false);
    expect(result.current.state.projects).toEqual([]);
    expect(result.current.state.catalogError).toBe("catalog unavailable");
    expect(result.current.state.error).toBe("");
  });

  it("clears the prior error while a catalog retry is pending", async () => {
    let resolveRetry!: (value: { object: "projects"; data: ProjectRecord[] }) => void;
    const retryRequest = new Promise<{ object: "projects"; data: ProjectRecord[] }>((resolve) => {
      resolveRetry = resolve;
    });
    vi.mocked(getProjects)
      .mockRejectedValueOnce(new Error("catalog unavailable"))
      .mockReturnValueOnce(retryRequest);
    const { result } = renderHook(() => useProjects(), { wrapper });

    await act(async () => {
      await result.current.actions.loadProjects();
    });
    expect(result.current.state.catalogError).toBe("catalog unavailable");

    let retryPromise!: Promise<void>;
    let duplicateRetryPromise!: Promise<void>;
    act(() => {
      retryPromise = result.current.actions.loadProjects();
      duplicateRetryPromise = result.current.actions.loadProjects();
    });
    expect(duplicateRetryPromise).toBe(retryPromise);
    await waitFor(() => {
      expect(result.current.state.loading).toBe(true);
      expect(result.current.state.catalogError).toBe("");
    });

    resolveRetry({ object: "projects", data: [] });
    await act(async () => {
      await retryPromise;
    });
    expect(result.current.state.loading).toBe(false);
    expect(result.current.state.loaded).toBe(true);
    expect(getProjects).toHaveBeenCalledTimes(2);
  });

  it("keeps background catalog failures quiet and lets the cockpit report them politely", async () => {
    const onError = vi.fn();
    vi.mocked(getProjects).mockRejectedValue(new Error("catalog unavailable"));
    const { result } = renderHook(() => useProjects(), {
      wrapper: ({ children }) => (
        <ProjectsProvider
          initialState={{ projects: [project], loaded: true, catalogError: "prior outage" }}
        >
          {children}
        </ProjectsProvider>
      ),
    });

    await act(async () => {
      await result.current.actions.loadProjects({ background: true, onError });
    });

    expect(result.current.state.projects).toEqual([project]);
    expect(result.current.state.loading).toBe(false);
    expect(result.current.state.catalogError).toBe("prior outage");
    expect(onError).toHaveBeenCalledWith("catalog unavailable");
  });

  it("does not commit a background catalog response after its interaction becomes stale", async () => {
    const renamed = { ...project, name: "Late catalog" };
    let resolveCatalog!: (value: { object: "projects"; data: ProjectRecord[] }) => void;
    let shouldApply = true;
    vi.mocked(getProjects).mockReturnValue(
      new Promise<{ object: "projects"; data: ProjectRecord[] }>((resolve) => {
        resolveCatalog = resolve;
      }),
    );
    const { result } = renderHook(() => useProjects(), {
      wrapper: ({ children }) => (
        <ProjectsProvider initialState={{ projects: [project], loaded: true }}>
          {children}
        </ProjectsProvider>
      ),
    });

    let loadPromise!: Promise<void>;
    act(() => {
      loadPromise = result.current.actions.loadProjects({
        background: true,
        shouldApply: () => shouldApply,
      });
    });
    shouldApply = false;
    resolveCatalog({ object: "projects", data: [renamed] });
    await act(async () => {
      await loadPromise;
    });

    expect(result.current.state.projects).toEqual([project]);
  });

  it("queues a fresh foreground catalog load behind an invalidated background read", async () => {
    const backgroundProject = { ...project, name: "Background snapshot" };
    const foregroundProject = { ...project, name: "Foreground snapshot" };
    let resolveBackground!: (value: { object: "projects"; data: ProjectRecord[] }) => void;
    let backgroundCurrent = true;
    vi.mocked(getProjects)
      .mockReturnValueOnce(
        new Promise<{ object: "projects"; data: ProjectRecord[] }>((resolve) => {
          resolveBackground = resolve;
        }),
      )
      .mockResolvedValueOnce({ object: "projects", data: [foregroundProject] });
    const { result } = renderHook(() => useProjects(), {
      wrapper: ({ children }) => (
        <ProjectsProvider initialState={{ projects: [project], loaded: true }}>
          {children}
        </ProjectsProvider>
      ),
    });

    let backgroundPromise!: Promise<void>;
    let foregroundPromise!: Promise<void>;
    act(() => {
      backgroundPromise = result.current.actions.loadProjects({
        background: true,
        shouldApply: () => backgroundCurrent,
      });
      backgroundCurrent = false;
      foregroundPromise = result.current.actions.loadProjects();
    });
    resolveBackground({ object: "projects", data: [backgroundProject] });
    await act(async () => {
      await backgroundPromise;
      await foregroundPromise;
    });

    expect(getProjects).toHaveBeenCalledTimes(2);
    expect(result.current.state.projects).toEqual([foregroundProject]);
  });

  it("returns create failures to the owning surface without changing catalog feedback", async () => {
    let resolveLoad!: (value: { object: "projects"; data: ProjectRecord[] }) => void;
    vi.mocked(getProjects).mockReturnValue(
      new Promise<{ object: "projects"; data: ProjectRecord[] }>((resolve) => {
        resolveLoad = resolve;
      }),
    );
    vi.mocked(createProject).mockRejectedValue(new Error("create failed"));
    const { result } = renderHook(() => useProjects(), { wrapper });

    let loadPromise!: Promise<void>;
    act(() => {
      loadPromise = result.current.actions.loadProjects();
    });
    await act(async () => {
      await expect(
        result.current.actions.createProject({ name: "Keep this draft" }),
      ).rejects.toThrow("create failed");
    });
    expect(result.current.state.error).toBe("");

    resolveLoad({ object: "projects", data: [] });
    await act(async () => {
      await loadPromise;
    });

    expect(result.current.state.loaded).toBe(true);
    expect(result.current.state.catalogError).toBe("");
    expect(result.current.state.error).toBe("");
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

  it("refetches a catalog snapshot that races with creating and selecting a project", async () => {
    const createdProject = { ...project, id: "proj_created", name: "Created while loading" };
    let resolveInitialLoad!: (value: { object: "projects"; data: ProjectRecord[] }) => void;
    const initialLoad = new Promise<{ object: "projects"; data: ProjectRecord[] }>((resolve) => {
      resolveInitialLoad = resolve;
    });
    vi.mocked(getProjects)
      .mockReturnValueOnce(initialLoad)
      .mockResolvedValueOnce({ object: "projects", data: [project, createdProject] });
    vi.mocked(createProject).mockResolvedValue({ object: "project", data: createdProject });
    window.localStorage.setItem("hecate.project", project.id);
    const { result } = renderHook(() => useProjects(), {
      wrapper: ({ children }) => (
        <ProjectsProvider initialState={{ projects: [project] }}>{children}</ProjectsProvider>
      ),
    });

    let loadPromise!: Promise<void>;
    act(() => {
      loadPromise = result.current.actions.loadProjects();
    });
    await act(async () => {
      await result.current.actions.createProject({ name: createdProject.name });
    });
    expect(result.current.activeProjectID).toBe(project.id);

    resolveInitialLoad({ object: "projects", data: [project] });
    await act(async () => {
      await loadPromise;
    });

    expect(getProjects).toHaveBeenCalledTimes(2);
    expect(result.current.state.projects).toEqual([project, createdProject]);
    expect(result.current.activeProjectID).toBe(project.id);
    expect(window.localStorage.getItem("hecate.project")).toBe(project.id);
  });

  it("validates the current selection when a catalog load finishes", async () => {
    const priorProject = { ...project, id: "proj_prior", name: "Prior selection" };
    let resolveLoad!: (value: { object: "projects"; data: ProjectRecord[] }) => void;
    vi.mocked(getProjects).mockReturnValue(
      new Promise<{ object: "projects"; data: ProjectRecord[] }>((resolve) => {
        resolveLoad = resolve;
      }),
    );
    window.localStorage.setItem("hecate.project", priorProject.id);
    const { result } = renderHook(() => useProjects(), {
      wrapper: ({ children }) => (
        <ProjectsProvider initialState={{ projects: [priorProject, project] }}>
          {children}
        </ProjectsProvider>
      ),
    });

    let loadPromise!: Promise<void>;
    act(() => {
      loadPromise = result.current.actions.loadProjects();
      result.current.actions.setActiveProjectID(project.id);
    });
    resolveLoad({ object: "projects", data: [project] });
    await act(async () => {
      await loadPromise;
    });

    expect(result.current.activeProjectID).toBe(project.id);
    expect(window.localStorage.getItem("hecate.project")).toBe(project.id);
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

  it("persists the last selected project", async () => {
    const opened = { ...project, last_opened_at: "2026-05-21T11:00:00Z" };
    vi.mocked(updateProject).mockResolvedValue({ object: "project", data: opened });
    const { result } = renderHook(() => useProjects(), {
      wrapper: ({ children }) => (
        <ProjectsProvider initialState={{ projects: [project] }}>{children}</ProjectsProvider>
      ),
    });

    await act(async () => {
      await result.current.actions.selectProject(project.id);
    });

    expect(updateProject).toHaveBeenCalledWith(project.id, {
      last_opened_at: expect.any(String) as string,
    });
    expect(result.current.activeProjectID).toBe(project.id);
    await waitFor(() => {
      expect(window.localStorage.getItem("hecate.project")).toBe(project.id);
    });
  });

  it("preserves an opaque project id when selecting it", async () => {
    const opaqueProject = { ...project, id: " project/+ % λ " };
    vi.mocked(updateProject).mockResolvedValue({ object: "project", data: opaqueProject });
    const { result } = renderHook(() => useProjects(), {
      wrapper: ({ children }) => (
        <ProjectsProvider initialState={{ projects: [opaqueProject], loaded: true }}>
          {children}
        </ProjectsProvider>
      ),
    });

    await act(async () => {
      await result.current.actions.selectProject(opaqueProject.id);
    });

    expect(updateProject).toHaveBeenCalledWith(opaqueProject.id, {
      last_opened_at: expect.any(String) as string,
    });
    expect(result.current.activeProjectID).toBe(opaqueProject.id);
  });

  it("creates a rootless project without taking presentation selection ownership", async () => {
    const created = { ...project, id: "proj_research", name: "Research", roots: [] };
    vi.mocked(createProject).mockResolvedValue({ object: "project", data: created });
    const { result } = renderHook(() => useProjects(), { wrapper });

    await act(async () => {
      await result.current.actions.createProject({
        name: " Research ",
        description: " Durable research workspace. ",
      });
    });

    expect(createProject).toHaveBeenCalledWith({
      name: "Research",
      description: "Durable research workspace.",
    });
    expect(result.current.state.projects).toEqual([created]);
    expect(result.current.activeProjectID).toBe("");
    expect(window.localStorage.getItem("hecate.project")).toBeNull();
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
    vi.mocked(deleteProject).mockResolvedValue(deleteResult);
    const { result } = renderHook(() => useProjects(), {
      wrapper: ({ children }) => (
        <ProjectsProvider initialState={{ projects: [project] }}>{children}</ProjectsProvider>
      ),
    });

    let deleted: ProjectDeleteRecord | null = null;
    await act(async () => {
      deleted = await result.current.actions.deleteProject(project.id);
    });

    expect(deleteProject).toHaveBeenCalledWith(project.id);
    expect(deleted).toEqual(deleteResult.data);
    expect(result.current.state.projects).toEqual([]);
    expect(result.current.activeProjectID).toBe("");
    expect(window.localStorage.getItem("hecate.project")).toBeNull();
  });

  it("returns null and keeps local state when project deletion fails", async () => {
    window.localStorage.setItem("hecate.project", project.id);
    vi.mocked(deleteProject).mockRejectedValue(new Error("delete failed"));
    const { result } = renderHook(() => useProjects(), {
      wrapper: ({ children }) => (
        <ProjectsProvider initialState={{ projects: [project] }}>{children}</ProjectsProvider>
      ),
    });

    let deleted: ProjectDeleteRecord | null = deleteResult.data;
    await act(async () => {
      deleted = await result.current.actions.deleteProject(project.id);
    });

    expect(deleted).toBeNull();
    expect(result.current.state.projects).toEqual([project]);
    expect(result.current.activeProjectID).toBe(project.id);
    expect(result.current.state.error).toBe("delete failed");
  });
});
