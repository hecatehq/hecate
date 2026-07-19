import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  ApiError,
  getModels,
  getProviders,
  getTask,
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
import type { ProjectDeleteRecord, ProjectRecord } from "../../types/project";
import type { TaskRecord, TaskRunRecord } from "../../types/task";
import { streamModelCallCostKey, TasksView } from "./TasksView";

vi.mock("../../lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../lib/api")>();
  return {
    ...actual,
    getTask: vi.fn(async () => ({ object: "task", data: task })),
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
  latest_run_step_count: 1,
  created_at: "2026-06-04T10:00:00Z",
  updated_at: "2026-06-04T10:05:00Z",
};
const run: TaskRunRecord = {
  id: "run_1",
  task_id: task.id,
  number: 1,
  status: "completed",
  model_call_count: 1,
  model: "gpt-4o-mini",
  started_at: "2026-06-04T10:00:00Z",
  finished_at: "2026-06-04T10:05:00Z",
};

afterEach(() => {
  window.localStorage.clear();
  vi.mocked(getTask).mockReset();
  vi.mocked(getTask).mockResolvedValue({ object: "task", data: task });
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

describe("streamModelCallCostKey", () => {
  it("uses the backend's one-based model-call numbers directly", () => {
    expect(streamModelCallCostKey(1)).toBe(1);
    expect(streamModelCallCostKey(2)).toBe(2);
  });

  it("rejects invalid model-call indexes", () => {
    expect(streamModelCallCostKey(undefined)).toBeNull();
    expect(streamModelCallCostKey(0)).toBeNull();
    expect(streamModelCallCostKey(-1)).toBeNull();
    expect(streamModelCallCostKey(Number.NaN)).toBeNull();
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
    expect(screen.getByText(/Tasks are durable units of work/i)).toBeTruthy();
    expect(
      screen.getByText(/Each start, continuation, retry, or resume creates a Run/i),
    ).toBeTruthy();
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

  it("keeps project deletion modal and ownership locked while deletion is pending", async () => {
    const project: ProjectRecord = {
      id: "proj_1",
      name: "Hecate",
      roots: [],
      created_at: "2026-05-29T00:00:00Z",
      updated_at: "2026-05-29T00:00:00Z",
    };
    let finishDelete: ((value: ProjectDeleteRecord | null) => void) | undefined;
    const deleteProject = vi.fn(
      () =>
        new Promise<ProjectDeleteRecord | null>((resolve) => {
          finishDelete = resolve;
        }),
    );
    const state = createRuntimeConsoleFixture({
      session: localSession,
      projects: [project],
      activeProjectID: project.id,
    });
    render(
      withRuntimeConsole(<TasksView />, {
        state,
        actions: { ...createRuntimeConsoleActions(), deleteProject },
      }),
    );

    await waitFor(() => expect(screen.getByText("Start a task")).toBeTruthy());
    fireEvent.click(screen.getByRole("button", { name: "Project Hecate" }));
    const projectButton = screen.getByRole("button", { name: "Project Hecate" });
    fireEvent.mouseEnter(projectButton.parentElement as HTMLElement);
    fireEvent.click(screen.getByRole("button", { name: "Delete project Hecate" }));
    fireEvent.click(screen.getByRole("button", { name: "Delete project" }));

    await waitFor(() => expect(deleteProject).toHaveBeenCalledTimes(1));
    const dialog = screen.getByRole("dialog", { name: "Delete project" });
    const pendingButton = screen.getByRole("button", { name: "Working…" });
    expect(pendingButton).toBeDisabled();
    expect(screen.getByRole("button", { name: "Close" })).toBeDisabled();
    fireEvent.keyDown(window, { key: "Escape" });
    fireEvent.click(dialog.parentElement as HTMLElement);
    fireEvent.click(pendingButton);
    expect(deleteProject).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("dialog", { name: "Delete project" })).toBe(dialog);

    await act(async () => {
      finishDelete?.({
        project_id: project.id,
        project_name: project.name,
        chat_sessions_deleted: 0,
        project_work_rows_deleted: 0,
        project_skills_deleted: 0,
        memory_entries_deleted: 0,
        memory_candidates_deleted: 0,
      });
    });
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "Delete project" })).toBeNull(),
    );
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

describe("TasksView streamed Task summaries", () => {
  it("starts a selected Run stream at sequence zero instead of reusing the prior Run cursor", async () => {
    const listedTask = { ...task, latest_run_id: "run_latest" };
    const latestRun = { ...run, id: "run_latest", number: 2, task_id: listedTask.id };
    const olderRun = { ...run, id: "run_older", number: 1, task_id: listedTask.id };
    const streamStarts: Array<{ runID: string; afterSequence: number }> = [];
    vi.mocked(getTasks).mockResolvedValue({ object: "list", data: [listedTask] });
    vi.mocked(getTaskRuns).mockResolvedValue({ object: "list", data: [latestRun, olderRun] });
    vi.mocked(streamTaskRun).mockImplementation(
      async (_taskID, runID, onEvent, afterSequence = 0) => {
        streamStarts.push({ runID, afterSequence });
        if (runID === latestRun.id) {
          onEvent({
            event: "snapshot",
            payload: {
              object: "task_run_stream_event",
              data: { sequence: 15, run: latestRun },
            },
          });
        }
      },
    );

    const state = createRuntimeConsoleFixture({ session: localSession });
    render(withRuntimeConsole(<TasksView />, { state, actions: createRuntimeConsoleActions() }));

    await waitFor(() =>
      expect(streamStarts).toContainEqual({ runID: latestRun.id, afterSequence: 0 }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Select run" }));
    fireEvent.click(
      screen.getAllByRole("option").find((option) => option.textContent?.includes("run #1"))!,
    );

    await waitFor(() =>
      expect(streamStarts).toContainEqual({ runID: olderRun.id, afterSequence: 0 }),
    );
  });

  it("updates latest-Run summary fields from the latest Run stream", async () => {
    const listedTask: TaskRecord = {
      ...task,
      status: "running",
      latest_run_step_count: 0,
      latest_model: undefined,
      latest_provider: undefined,
    };
    const completedRun: TaskRunRecord = {
      ...run,
      status: "completed",
      model: "gpt-5.4-mini",
      provider: "openai",
      step_count: 4,
      artifact_count: 2,
      model_call_count: 2,
    };
    vi.mocked(getTasks).mockResolvedValue({ object: "list", data: [listedTask] });
    vi.mocked(getTaskRuns).mockResolvedValue({ object: "list", data: [completedRun] });
    vi.mocked(streamTaskRun).mockImplementation(async (_taskID, _runID, onEvent) => {
      onEvent({
        event: "snapshot",
        payload: {
          object: "task_run_stream_event",
          data: { sequence: 2, run: completedRun },
        },
      });
    });

    const state = createRuntimeConsoleFixture({ session: localSession });
    render(withRuntimeConsole(<TasksView />, { state, actions: createRuntimeConsoleActions() }));

    expect(await screen.findByText("Latest run · 4 steps")).toBeTruthy();
  });

  it("does not overwrite the latest-Run summary from a historical Run stream", async () => {
    const listedTask: TaskRecord = {
      ...task,
      status: "completed",
      latest_run_id: "run_latest",
      latest_run_step_count: 1,
    };
    const latestRun: TaskRunRecord = { ...run, id: "run_latest", task_id: listedTask.id };
    const historicalRun: TaskRunRecord = {
      ...run,
      id: "run_old",
      task_id: listedTask.id,
      number: 1,
      status: "failed",
      step_count: 9,
      model_call_count: 3,
    };
    vi.mocked(getTask).mockResolvedValue({ object: "task", data: listedTask });
    vi.mocked(getTasks).mockResolvedValue({ object: "list", data: [listedTask] });
    vi.mocked(getTaskRuns).mockResolvedValue({
      object: "list",
      data: [latestRun, historicalRun],
    });
    vi.mocked(streamTaskRun).mockImplementation(async (_taskID, runID, onEvent) => {
      if (runID !== historicalRun.id) return;
      onEvent({
        event: "snapshot",
        payload: {
          object: "task_run_stream_event",
          data: { sequence: 3, run: historicalRun },
        },
      });
    });

    const state = createRuntimeConsoleFixture({ session: localSession });
    render(
      withRuntimeConsole(
        <TasksView focusRequest={{ taskID: listedTask.id, runID: historicalRun.id, nonce: 1 }} />,
        { state, actions: createRuntimeConsoleActions() },
      ),
    );

    expect(await screen.findByText("Latest run · 1 step")).toBeTruthy();
    expect(screen.queryByText("Latest run · 9 steps")).toBeNull();
  });
});

describe("TasksView selected task", () => {
  it("resolves an addressed Task outside the active project and first list page", async () => {
    const projectA: ProjectRecord = {
      id: "proj_a",
      name: "Alpha",
      roots: [],
      created_at: "2026-05-29T00:00:00Z",
      updated_at: "2026-05-29T00:00:00Z",
    };
    const projectB: ProjectRecord = { ...projectA, id: "proj_b", name: "Beta" };
    const addressedTask: TaskRecord = {
      ...task,
      id: "task_addressed",
      title: "Addressed task",
      project_id: projectB.id,
      latest_run_id: "run_addressed",
    };
    const addressedRun: TaskRunRecord = {
      ...run,
      id: "run_addressed",
      task_id: addressedTask.id,
    };
    const firstPage = Array.from({ length: 30 }, (_, index) => ({
      ...task,
      id: `task_page_${index}`,
      title: `Listed task ${index}`,
      project_id: projectB.id,
    }));
    vi.mocked(getTask).mockResolvedValue({ object: "task", data: addressedTask });
    vi.mocked(getTasks).mockResolvedValue({ object: "list", data: firstPage });
    vi.mocked(getTaskRuns).mockResolvedValue({ object: "list", data: [addressedRun] });
    const selectProject = vi.fn(async () => undefined);
    const onSelectionChange = vi.fn();
    const state = createRuntimeConsoleFixture({
      session: localSession,
      projects: [projectA, projectB],
      activeProjectID: projectA.id,
    });

    render(
      withRuntimeConsole(
        <TasksView
          focusRequest={{
            taskID: addressedTask.id,
            runID: addressedRun.id,
            nonce: 1,
          }}
          onSelectionChange={onSelectionChange}
        />,
        {
          state,
          actions: { ...createRuntimeConsoleActions(), selectProject },
        },
      ),
    );

    expect(await screen.findByRole("button", { name: "Task Addressed task" })).toBeTruthy();
    expect(selectProject).toHaveBeenCalledWith(projectB.id);
    expect(getTasks).toHaveBeenCalledWith(30, projectB.id);
    expect(onSelectionChange).not.toHaveBeenCalled();
  });

  it("ignores an older addressed Task response after navigation moves on", async () => {
    const taskA: TaskRecord = { ...task, id: "task_a", title: "Task A" };
    const taskB: TaskRecord = { ...task, id: "task_b", title: "Task B" };
    const runA: TaskRunRecord = { ...run, id: "run_a", task_id: taskA.id };
    const runB: TaskRunRecord = { ...run, id: "run_b", task_id: taskB.id };
    let resolveTaskA: ((value: { object: string; data: TaskRecord }) => void) | undefined;
    let resolveTaskB: ((value: { object: string; data: TaskRecord }) => void) | undefined;
    vi.mocked(getTask).mockImplementation(
      (taskID) =>
        new Promise((resolve) => {
          if (taskID === taskA.id) resolveTaskA = resolve;
          else resolveTaskB = resolve;
        }),
    );
    vi.mocked(getTasks).mockResolvedValue({ object: "list", data: [taskA, taskB] });
    vi.mocked(getTaskRuns).mockImplementation(async (taskID) => ({
      object: "list",
      data: taskID === taskA.id ? [runA] : [runB],
    }));
    const state = createRuntimeConsoleFixture({ session: localSession });
    const actions = createRuntimeConsoleActions();
    const view = render(
      withRuntimeConsole(
        <TasksView focusRequest={{ taskID: taskA.id, runID: runA.id, nonce: 1 }} />,
        { state, actions },
      ),
    );

    await waitFor(() => expect(getTask).toHaveBeenCalledWith(taskA.id));
    view.rerender(
      withRuntimeConsole(
        <TasksView focusRequest={{ taskID: taskB.id, runID: runB.id, nonce: 2 }} />,
        { state, actions },
      ),
    );
    await waitFor(() => expect(getTask).toHaveBeenCalledWith(taskB.id));
    await act(async () => resolveTaskB?.({ object: "task", data: taskB }));
    expect(await screen.findByRole("button", { name: "Task Task B" })).toBeTruthy();
    await act(async () => resolveTaskA?.({ object: "task", data: taskA }));

    await waitFor(() => expect(screen.getByRole("button", { name: "Task Task B" })).toBeTruthy());
    expect(
      screen.queryByRole("button", { name: "Task Task A" })?.getAttribute("aria-current"),
    ).not.toBe("true");
  });

  it("replaces a bare Tasks URL with the selected Task and Run", async () => {
    vi.mocked(getTasks).mockResolvedValue({ object: "list", data: [task] });
    vi.mocked(getTaskRuns).mockResolvedValue({ object: "list", data: [run] });
    const onSelectionChange = vi.fn();
    const state = createRuntimeConsoleFixture({ session: localSession });

    render(
      withRuntimeConsole(<TasksView onSelectionChange={onSelectionChange} />, {
        state,
        actions: createRuntimeConsoleActions(),
      }),
    );

    await waitFor(() =>
      expect(onSelectionChange).toHaveBeenCalledWith("task_1", "run_1", "replace"),
    );
  });

  it("replaces a stale routed Run with the Task's latest available Run", async () => {
    vi.mocked(getTasks).mockResolvedValue({ object: "list", data: [task] });
    vi.mocked(getTaskRuns).mockResolvedValue({ object: "list", data: [run] });
    const onSelectionChange = vi.fn();
    const state = createRuntimeConsoleFixture({ session: localSession });

    render(
      withRuntimeConsole(
        <TasksView
          focusRequest={{ taskID: "task_1", runID: "run_missing", nonce: 1 }}
          onSelectionChange={onSelectionChange}
        />,
        { state, actions: createRuntimeConsoleActions() },
      ),
    );

    await waitFor(() =>
      expect(onSelectionChange).toHaveBeenCalledWith("task_1", "run_1", "replace"),
    );
  });

  it("settles loading and surfaces a non-404 addressed Task failure", async () => {
    vi.mocked(getTask).mockRejectedValue(new ApiError("Task service unavailable.", 503));
    const onSelectionChange = vi.fn();
    const state = createRuntimeConsoleFixture({ session: localSession });

    render(
      withRuntimeConsole(
        <TasksView
          focusRequest={{ taskID: "task_unavailable", runID: "run_1", nonce: 1 }}
          onSelectionChange={onSelectionChange}
        />,
        { state, actions: createRuntimeConsoleActions() },
      ),
    );

    expect(await screen.findByRole("alert")).toHaveTextContent("Task service unavailable.");
    expect(screen.getByText("Start a task")).toBeTruthy();
    expect(screen.queryByText("Loading tasks…")).toBeNull();
    expect(onSelectionChange).not.toHaveBeenCalled();
  });

  it("pushes an explicitly selected Run", async () => {
    const secondRun: TaskRunRecord = { ...run, id: "run_2", number: 2 };
    vi.mocked(getTasks).mockResolvedValue({ object: "list", data: [task] });
    vi.mocked(getTaskRuns).mockResolvedValue({ object: "list", data: [secondRun, run] });
    const onSelectionChange = vi.fn();
    const state = createRuntimeConsoleFixture({ session: localSession });

    render(
      withRuntimeConsole(<TasksView onSelectionChange={onSelectionChange} />, {
        state,
        actions: createRuntimeConsoleActions(),
      }),
    );

    await waitFor(() => expect(screen.getByRole("button", { name: "Select run" })).toBeTruthy());
    onSelectionChange.mockClear();
    fireEvent.click(screen.getByRole("button", { name: "Select run" }));
    fireEvent.click(screen.getByRole("option", { name: /run #1/i }));

    await waitFor(() => expect(onSelectionChange).toHaveBeenCalledWith("task_1", "run_1"));
  });

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
