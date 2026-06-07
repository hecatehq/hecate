import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  getModels,
  getProviders,
  getTaskApprovals,
  getTaskRunArtifacts,
  getTaskRunEvents,
  getTaskRuns,
  getTaskRunSteps,
  getTasks,
  streamTaskRun,
} from "../../lib/api";
import {
  createRuntimeConsoleActions,
  createRuntimeConsoleFixture,
} from "../../test/runtime-console-fixture";
import { withRuntimeConsole } from "../../test/runtime-console-render";
import type { ProjectRecord } from "../../types/project";
import type { TaskRecord, TaskRunRecord } from "../../types/task";
import { streamTurnCostKey, TasksView } from "./TasksView";

vi.mock("../../lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../lib/api")>();
  return {
    ...actual,
    getTasks: vi.fn(async () => ({ object: "list", data: [] })),
    getTaskRuns: vi.fn(async () => ({ object: "list", data: [] })),
    getTaskApprovals: vi.fn(async () => ({ object: "list", data: [] })),
    getTaskRunSteps: vi.fn(async () => ({ object: "list", data: [] })),
    getTaskRunArtifacts: vi.fn(async () => ({ object: "list", data: [] })),
    getTaskRunEvents: vi.fn(async () => ({ object: "list", data: [] })),
    streamTaskRun: vi.fn(async () => {}),
    getModels: vi.fn(async () => ({ object: "list", data: [] })),
    getProviders: vi.fn(async () => ({ object: "list", data: [] })),
  };
});

const localSession = { label: "Local" };
const task: TaskRecord = {
  id: "task_1",
  title: "Review project cockpit",
  prompt: "Review project cockpit",
  project_id: "",
  status: "completed",
  execution_kind: "agent_loop",
  latest_run_id: "run_1",
  latest_model: "gpt-4o-mini",
  step_count: 1,
  created_at: "2026-06-04T10:00:00Z",
  updated_at: "2026-06-04T10:05:00Z",
};
const run: TaskRunRecord = {
  id: "run_1",
  task_id: task.id,
  number: 1,
  status: "completed",
  model: "gpt-4o-mini",
  started_at: "2026-06-04T10:00:00Z",
  finished_at: "2026-06-04T10:05:00Z",
};

afterEach(() => {
  window.localStorage.clear();
  vi.mocked(getTasks).mockReset();
  vi.mocked(getTasks).mockResolvedValue({ object: "list", data: [] });
  vi.mocked(getTaskRuns).mockReset();
  vi.mocked(getTaskRuns).mockResolvedValue({ object: "list", data: [] });
  vi.mocked(getTaskApprovals).mockReset();
  vi.mocked(getTaskApprovals).mockResolvedValue({ object: "list", data: [] });
  vi.mocked(getTaskRunSteps).mockReset();
  vi.mocked(getTaskRunSteps).mockResolvedValue({ object: "list", data: [] });
  vi.mocked(getTaskRunArtifacts).mockReset();
  vi.mocked(getTaskRunArtifacts).mockResolvedValue({ object: "list", data: [] });
  vi.mocked(getTaskRunEvents).mockReset();
  vi.mocked(getTaskRunEvents).mockResolvedValue({ object: "list", data: [] });
  vi.mocked(streamTaskRun).mockReset();
  vi.mocked(streamTaskRun).mockResolvedValue();
  vi.mocked(getModels).mockReset();
  vi.mocked(getModels).mockResolvedValue({ object: "list", data: [] });
  vi.mocked(getProviders).mockReset();
  vi.mocked(getProviders).mockResolvedValue({ object: "list", data: [] });
});

describe("streamTurnCostKey", () => {
  it("normalizes zero-based backend turn indexes to one-based UI turn numbers", () => {
    expect(streamTurnCostKey(0)).toBe(1);
    expect(streamTurnCostKey(1)).toBe(2);
  });

  it("rejects invalid turn indexes", () => {
    expect(streamTurnCostKey(undefined)).toBeNull();
    expect(streamTurnCostKey(-1)).toBeNull();
    expect(streamTurnCostKey(Number.NaN)).toBeNull();
  });
});

describe("TasksView empty state", () => {
  it("shows an actionable task-start canvas instead of a passive selection placeholder", async () => {
    const state = createRuntimeConsoleFixture({ session: localSession });
    render(withRuntimeConsole(<TasksView />, { state, actions: createRuntimeConsoleActions() }));

    await waitFor(() => {
      expect(screen.getByText("Start a task")).toBeTruthy();
    });

    expect(screen.queryByText("Select a task to inspect.")).toBeNull();
    expect(screen.getByText("Projects")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Add project" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Project No project" })).toBeTruthy();
    // One button lives in the task sidebar; the second is the
    // main-pane start affordance for an empty task workspace.
    expect(screen.getAllByRole("button", { name: "New task" }).length).toBeGreaterThanOrEqual(2);
  });

  it("seeds new tasks from the selected project's default workspace", async () => {
    window.localStorage.setItem("hecate.agentWorkspace", "/stale/chat/workspace");
    const project: ProjectRecord = {
      id: "proj_1",
      name: "Hecate",
      default_root_id: "root_default",
      roots: [
        {
          id: "root_active",
          path: "/workspace/active",
          kind: "workspace",
          active: true,
          created_at: "2026-05-29T00:00:00Z",
          updated_at: "2026-05-29T00:00:00Z",
        },
        {
          id: "root_default",
          path: "/workspace/default",
          kind: "workspace",
          active: false,
          created_at: "2026-05-29T00:00:00Z",
          updated_at: "2026-05-29T00:00:00Z",
        },
      ],
      created_at: "2026-05-29T00:00:00Z",
      updated_at: "2026-05-29T00:00:00Z",
    };
    const state = createRuntimeConsoleFixture({
      session: localSession,
      projects: [project],
      activeProjectID: project.id,
    });

    render(withRuntimeConsole(<TasksView />, { state, actions: createRuntimeConsoleActions() }));

    await waitFor(() => {
      expect(screen.getByText("Start a task")).toBeTruthy();
    });
    expect(screen.getByRole("button", { name: "Project Hecate" })).toBeTruthy();
    fireEvent.click(screen.getAllByRole("button", { name: "New task" })[0]);

    await waitFor(() => {
      expect((screen.getByLabelText("Workspace path") as HTMLInputElement).value).toBe(
        "/workspace/default",
      );
    });
  });

  it("loads tasks scoped to the active project", async () => {
    const project: ProjectRecord = {
      id: "proj_1",
      name: "Hecate",
      roots: [],
      created_at: "2026-05-29T00:00:00Z",
      updated_at: "2026-05-29T00:00:00Z",
    };
    const state = createRuntimeConsoleFixture({
      session: localSession,
      projects: [project],
      activeProjectID: project.id,
    });

    render(withRuntimeConsole(<TasksView />, { state, actions: createRuntimeConsoleActions() }));

    await waitFor(() => {
      expect(vi.mocked(getTasks)).toHaveBeenCalledWith(30, project.id);
    });
  });

  it("switches the visible task list when the selected project changes", async () => {
    const projectA: ProjectRecord = {
      id: "proj_a",
      name: "Alpha",
      roots: [],
      created_at: "2026-05-29T00:00:00Z",
      updated_at: "2026-05-29T00:00:00Z",
    };
    const projectB: ProjectRecord = {
      id: "proj_b",
      name: "Beta",
      roots: [],
      created_at: "2026-05-29T00:00:00Z",
      updated_at: "2026-05-29T00:00:00Z",
    };
    const alphaTask: TaskRecord = {
      ...task,
      id: "task_alpha",
      title: "Alpha task",
      prompt: "Alpha task",
      project_id: projectA.id,
      latest_run_id: "",
    };
    const betaTask: TaskRecord = {
      ...task,
      id: "task_beta",
      title: "Beta task",
      prompt: "Beta task",
      project_id: projectB.id,
      latest_run_id: "",
    };
    vi.mocked(getTasks).mockImplementation(async (_limit, projectID) => {
      if (projectID === projectA.id) return { object: "list", data: [alphaTask] };
      if (projectID === projectB.id) return { object: "list", data: [betaTask] };
      return { object: "list", data: [] };
    });
    vi.mocked(getTaskRuns).mockResolvedValue({ object: "list", data: [] });

    const state = createRuntimeConsoleFixture({
      session: localSession,
      projects: [projectA, projectB],
      activeProjectID: projectA.id,
    });

    render(withRuntimeConsole(<TasksView />, { state, actions: createRuntimeConsoleActions() }));

    await waitFor(() => {
      expect(screen.getByRole("button", { name: "Task Alpha task" })).toBeTruthy();
    });

    fireEvent.click(screen.getByRole("button", { name: "Project Alpha" }));
    await waitFor(() => {
      expect(screen.getByRole("button", { name: "Project Beta" })).toBeTruthy();
    });
    fireEvent.click(screen.getByRole("button", { name: "Project Beta" }));

    await waitFor(() => {
      expect(vi.mocked(getTasks)).toHaveBeenCalledWith(30, projectB.id);
      expect(screen.getByRole("button", { name: "Task Beta task" })).toBeTruthy();
    });

    expect(screen.queryByRole("button", { name: "Task Alpha task" })).toBeNull();
  });
});

describe("TasksView selected task", () => {
  it("keeps refresh in the task header instead of the task list header", async () => {
    vi.mocked(getTasks).mockResolvedValue({ object: "list", data: [task] });
    vi.mocked(getTaskRuns).mockResolvedValue({ object: "list", data: [run] });
    const state = createRuntimeConsoleFixture({ session: localSession });
    render(withRuntimeConsole(<TasksView />, { state, actions: createRuntimeConsoleActions() }));

    await waitFor(() => {
      expect(screen.getByRole("button", { name: "Refresh task" })).toBeTruthy();
    });

    expect(screen.queryByRole("button", { name: "Refresh tasks" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Refresh task" }));

    await waitFor(() => {
      expect(vi.mocked(getTasks).mock.calls.length).toBeGreaterThanOrEqual(2);
    });
  });
});
