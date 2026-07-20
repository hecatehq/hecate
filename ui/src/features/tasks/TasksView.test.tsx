import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  ApiError,
  createTask,
  deleteTask,
  getModels,
  getProviders,
  getTask,
  getTaskApprovals,
  getTaskRunArtifacts,
  getTaskRunEvents,
  getTaskRuns,
  getTaskRunSteps,
  getTaskScheduleOccurrences,
  getTaskSchedules,
  getTasks,
  startTask,
  streamTaskRun,
  upsertTaskSchedule,
} from "../../lib/api";
import {
  createRuntimeConsoleActions,
  createRuntimeConsoleFixture,
} from "../../test/runtime-console-fixture";
import { withRuntimeConsole } from "../../test/runtime-console-render";
import type { ProjectDeleteRecord, ProjectRecord } from "../../types/project";
import type { TaskApprovalRecord, TaskRecord, TaskRunRecord } from "../../types/task";
import { streamModelCallCostKey, taskMatchesFilter, TasksView } from "./TasksView";

vi.mock("../../lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../lib/api")>();
  return {
    ...actual,
    getTask: vi.fn(async () => ({ object: "task", data: task })),
    getTasks: vi.fn(async () => ({ object: "list", data: [] })),
    getTaskSchedules: vi.fn(async () => ({ object: "task_schedules", data: [] })),
    getTaskScheduleOccurrences: vi.fn(async () => ({
      object: "task_schedule_occurrences",
      data: [],
    })),
    getTaskRuns: vi.fn(async () => ({ object: "list", data: [] })),
    getTaskApprovals: vi.fn(async () => ({ object: "list", data: [] })),
    getTaskRunSteps: vi.fn(async () => ({ object: "list", data: [] })),
    getTaskRunArtifacts: vi.fn(async () => ({ object: "list", data: [] })),
    getTaskRunEvents: vi.fn(async () => ({ object: "list", data: [] })),
    streamTaskRun: vi.fn(async () => {}),
    getModels: vi.fn(async () => ({ object: "list", data: [] })),
    getProviders: vi.fn(async () => ({ object: "list", data: [] })),
    createTask: vi.fn(),
    deleteTask: vi.fn(),
    startTask: vi.fn(),
    upsertTaskSchedule: vi.fn(),
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

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, reject, resolve };
}

afterEach(() => {
  window.localStorage.clear();
  vi.mocked(getTask).mockReset();
  vi.mocked(getTask).mockResolvedValue({ object: "task", data: task });
  vi.mocked(getTasks).mockReset();
  vi.mocked(getTasks).mockResolvedValue({ object: "list", data: [] });
  vi.mocked(getTaskSchedules).mockReset();
  vi.mocked(getTaskSchedules).mockResolvedValue({ object: "task_schedules", data: [] });
  vi.mocked(getTaskScheduleOccurrences).mockReset();
  vi.mocked(getTaskScheduleOccurrences).mockResolvedValue({
    object: "task_schedule_occurrences",
    data: [],
  });
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
  vi.mocked(createTask).mockReset();
  vi.mocked(deleteTask).mockReset();
  vi.mocked(deleteTask).mockResolvedValue();
  vi.mocked(startTask).mockReset();
  vi.mocked(upsertTaskSchedule).mockReset();
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

describe("taskMatchesFilter", () => {
  const schedules = new Map([
    [
      "scheduled",
      {
        id: "schedule_1",
        task_id: "scheduled",
        kind: "cron" as const,
        cron_expression: "0 9 * * *",
        timezone: "UTC",
        enabled: false,
        created_at: "2026-07-20T08:00:00Z",
        updated_at: "2026-07-20T08:00:00Z",
      },
    ],
  ]);

  it("uses attention, configured schedule, and chat origin semantics", () => {
    expect(taskMatchesFilter({ ...task, status: "failed" }, "attention", schedules)).toBe(true);
    expect(taskMatchesFilter({ ...task, id: "scheduled" }, "scheduled", schedules)).toBe(true);
    expect(taskMatchesFilter({ ...task, origin_kind: "chat" }, "chat", schedules)).toBe(true);
    expect(taskMatchesFilter(task, "all", schedules)).toBe(true);
  });

  it("includes paused schedules in Scheduled", () => {
    expect(taskMatchesFilter({ ...task, id: "scheduled" }, "scheduled", schedules)).toBe(true);
  });
});

describe("TasksView scheduling", () => {
  it("requests schedules for every visible Task with the batched filter", async () => {
    const listedTask = { ...task, latest_run_id: "" };
    vi.mocked(getTasks).mockResolvedValue({ object: "list", data: [listedTask] });
    vi.mocked(getTaskRuns).mockResolvedValue({ object: "list", data: [] });
    vi.mocked(getTaskSchedules).mockResolvedValue({
      object: "task_schedules",
      data: [
        {
          id: "schedule_1",
          task_id: listedTask.id,
          kind: "cron",
          cron_expression: "0 9 * * *",
          timezone: "Europe/Madrid",
          enabled: true,
          next_run_at: "2026-07-21T07:00:00Z",
          created_at: "2026-07-20T08:00:00Z",
          updated_at: "2026-07-20T08:00:00Z",
        },
      ],
    });

    const state = createRuntimeConsoleFixture({ session: localSession });
    render(withRuntimeConsole(<TasksView />, { state, actions: createRuntimeConsoleActions() }));

    await waitFor(() => expect(getTaskSchedules).toHaveBeenCalledWith([listedTask.id]));
    expect((await screen.findAllByText(/Next · .*Europe\/Madrid/)).length).toBeGreaterThan(0);
  });

  it("keeps Tasks usable but disables Schedule mutations when schedules fail to load", async () => {
    const listedTask = { ...task, latest_run_id: "" };
    vi.mocked(getTasks).mockResolvedValue({ object: "list", data: [listedTask] });
    vi.mocked(getTaskRuns).mockResolvedValue({ object: "list", data: [] });
    vi.mocked(getTaskSchedules).mockRejectedValue(new Error("scheduler unavailable"));

    const state = createRuntimeConsoleFixture({ session: localSession });
    render(withRuntimeConsole(<TasksView />, { state, actions: createRuntimeConsoleActions() }));

    expect(
      await screen.findByRole("link", { name: `Task ${listedTask.title}, not started` }),
    ).toBeTruthy();
    expect(screen.getByRole("button", { name: "Schedule unavailable" })).toBeDisabled();
    expect(await screen.findByRole("alert")).toHaveTextContent(/scheduler unavailable/i);
    expect(screen.getByRole("button", { name: "New task" })).toBeEnabled();
  });

  it("falls back to All instead of treating failed Schedule data as an empty Scheduled filter", async () => {
    const scheduledTask = {
      ...task,
      id: "task_scheduled",
      title: "Scheduled task",
      latest_run_id: "",
      status: "not_started" as const,
    };
    const plainTask = {
      ...task,
      id: "task_plain",
      title: "Plain task",
      latest_run_id: "",
      status: "not_started" as const,
    };
    vi.mocked(getTasks).mockResolvedValue({ object: "list", data: [scheduledTask, plainTask] });
    vi.mocked(getTaskRuns).mockResolvedValue({ object: "list", data: [] });
    vi.mocked(getTaskSchedules)
      .mockResolvedValueOnce({
        object: "task_schedules",
        data: [
          {
            id: "schedule_1",
            task_id: scheduledTask.id,
            kind: "cron",
            cron_expression: "0 9 * * *",
            timezone: "Europe/Madrid",
            enabled: true,
            next_run_at: "2026-07-21T07:00:00Z",
            created_at: "2026-07-20T08:00:00Z",
            updated_at: "2026-07-20T08:00:00Z",
          },
        ],
      })
      .mockRejectedValueOnce(new Error("scheduler unavailable"));

    const state = createRuntimeConsoleFixture({ session: localSession });
    render(withRuntimeConsole(<TasksView />, { state, actions: createRuntimeConsoleActions() }));

    const scheduledFilter = await screen.findByRole("button", { name: "Scheduled" });
    await waitFor(() => expect(scheduledFilter).not.toHaveAttribute("aria-disabled"));
    fireEvent.click(scheduledFilter);
    expect(screen.queryByRole("link", { name: "Task Plain task, not started" })).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Refresh task" }));

    expect(
      await screen.findByText(/Scheduled filter unavailable: scheduler unavailable/),
    ).toBeTruthy();
    expect(screen.getByRole("button", { name: "All" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("button", { name: "Scheduled" })).toHaveAttribute(
      "aria-disabled",
      "true",
    );
    expect(screen.getByRole("link", { name: "Task Plain task, not started" })).toBeTruthy();
  });

  it("keeps New task available when only the selected Task detail fails to load", async () => {
    vi.mocked(getTasks).mockResolvedValue({ object: "list", data: [task] });
    vi.mocked(getTaskRuns).mockRejectedValue(new Error("detail unavailable"));

    const state = createRuntimeConsoleFixture({ session: localSession });
    render(withRuntimeConsole(<TasksView />, { state, actions: createRuntimeConsoleActions() }));

    expect(await screen.findByRole("heading", { name: task.title })).toBeTruthy();
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Task details could not be loaded: detail unavailable",
    );
    expect(screen.queryByText(/Tasks could not be (loaded|refreshed)/i)).toBeNull();

    const newTask = screen.getByRole("button", { name: "New task" });
    expect(newTask).toBeEnabled();
    fireEvent.click(newTask);
    expect(screen.getByRole("dialog", { name: "New task" })).toBeTruthy();
  });

  it("disables Schedule mutations while the initial schedule request is pending", async () => {
    const listedTask = { ...task, latest_run_id: "" };
    const schedules = deferred<Awaited<ReturnType<typeof getTaskSchedules>>>();
    vi.mocked(getTasks).mockResolvedValue({ object: "list", data: [listedTask] });
    vi.mocked(getTaskRuns).mockResolvedValue({ object: "list", data: [] });
    vi.mocked(getTaskSchedules).mockReturnValue(schedules.promise);

    const state = createRuntimeConsoleFixture({ session: localSession });
    render(withRuntimeConsole(<TasksView />, { state, actions: createRuntimeConsoleActions() }));

    expect(await screen.findByRole("button", { name: "Loading schedule…" })).toBeDisabled();
    await act(async () => {
      schedules.resolve({ object: "task_schedules", data: [] });
      await schedules.promise;
    });
    expect(await screen.findByRole("button", { name: "Schedule" })).toBeEnabled();
  });

  it("reports a successful save even when occurrence history cannot refresh", async () => {
    const listedTask = { ...task, latest_run_id: "" };
    vi.mocked(getTasks).mockResolvedValue({ object: "list", data: [listedTask] });
    vi.mocked(getTaskRuns).mockResolvedValue({ object: "list", data: [] });
    vi.mocked(getTaskScheduleOccurrences).mockRejectedValue(new Error("history unavailable"));
    vi.mocked(upsertTaskSchedule).mockImplementation(async (taskID, payload) => ({
      object: "task_schedule",
      data: {
        id: "schedule_1",
        task_id: taskID,
        kind: payload.kind,
        cron_expression: payload.cron_expression,
        timezone: payload.timezone,
        run_at: payload.run_at,
        enabled: payload.enabled,
        next_run_at: payload.run_at,
        created_at: "2026-07-20T08:00:00Z",
        updated_at: "2026-07-20T08:00:00Z",
      },
    }));

    const state = createRuntimeConsoleFixture({ session: localSession });
    render(withRuntimeConsole(<TasksView />, { state, actions: createRuntimeConsoleActions() }));
    await screen.findByRole("heading", { name: listedTask.title });
    fireEvent.click(screen.getByRole("button", { name: "Schedule" }));
    fireEvent.click(screen.getByRole("button", { name: "Save schedule" }));

    const savedNotice = await screen.findByText("Schedule saved.");
    expect(savedNotice).toHaveAttribute("role", "status");
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "Schedule this task" })).toBeNull(),
    );
    fireEvent.click(screen.getByTitle(/Edit schedule/));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      /Occurrence history could not be loaded: history unavailable/i,
    );
  });

  it("does not apply an old Task's save notice after selection changes", async () => {
    const firstTask = { ...task, id: "task_first", title: "First task", latest_run_id: "" };
    const secondTask = { ...task, id: "task_second", title: "Second task", latest_run_id: "" };
    const pendingSave = deferred<Awaited<ReturnType<typeof upsertTaskSchedule>>>();
    vi.mocked(getTasks).mockResolvedValue({ object: "list", data: [firstTask, secondTask] });
    vi.mocked(getTaskRuns).mockResolvedValue({ object: "list", data: [] });
    vi.mocked(upsertTaskSchedule).mockReturnValue(pendingSave.promise);

    const state = createRuntimeConsoleFixture({ session: localSession });
    render(withRuntimeConsole(<TasksView />, { state, actions: createRuntimeConsoleActions() }));
    await screen.findByRole("heading", { name: "First task" });
    fireEvent.click(screen.getByRole("button", { name: "Schedule" }));
    fireEvent.click(screen.getByRole("button", { name: "Save schedule" }));
    await waitFor(() =>
      expect(upsertTaskSchedule).toHaveBeenCalledWith(firstTask.id, expect.anything()),
    );

    fireEvent.click(screen.getByRole("link", { name: "Task Second task, not started" }));
    await screen.findByRole("heading", { name: "Second task" });
    await act(async () => {
      pendingSave.resolve({
        object: "task_schedule",
        data: {
          id: "schedule_first",
          task_id: firstTask.id,
          kind: "once",
          timezone: "Europe/Madrid",
          run_at: "2099-07-21T08:00:00Z",
          enabled: true,
          next_run_at: "2099-07-21T08:00:00Z",
          created_at: "2026-07-20T08:00:00Z",
          updated_at: "2026-07-20T08:00:00Z",
        },
      });
      await pendingSave.promise;
    });

    expect(screen.getByRole("heading", { name: "Second task" })).toBeTruthy();
    expect(screen.queryByText("Schedule saved.")).toBeNull();
  });

  it("creates a Task without starting Run #1 when the operator chooses create-only", async () => {
    const createdTask = {
      ...task,
      id: "task_later",
      title: "echo later",
      prompt: "echo later",
      execution_kind: "shell" as const,
      shell_command: "echo later",
      latest_run_id: "",
    };
    vi.mocked(getTasks)
      .mockResolvedValueOnce({ object: "list", data: [] })
      .mockRejectedValue(new Error("refresh unavailable"));
    vi.mocked(createTask).mockResolvedValue({ object: "task", data: createdTask });
    vi.mocked(getTaskRuns).mockResolvedValue({ object: "list", data: [] });
    const state = createRuntimeConsoleFixture({ session: localSession });
    render(withRuntimeConsole(<TasksView />, { state, actions: createRuntimeConsoleActions() }));

    await screen.findByText("Start a task");
    fireEvent.click(screen.getAllByRole("button", { name: "New task" })[0]);
    fireEvent.change(screen.getByLabelText("Shell command"), { target: { value: "echo later" } });
    fireEvent.click(screen.getByRole("radio", { name: /Create without starting/i }));
    fireEvent.click(screen.getByRole("button", { name: /^Create task$/i }));

    await waitFor(() => expect(createTask).toHaveBeenCalledTimes(1));
    expect(createTask).toHaveBeenCalledWith(
      expect.objectContaining({ execution_kind: "shell", shell_command: "echo later" }),
    );
    expect(vi.mocked(createTask).mock.calls[0][0]).not.toHaveProperty("start_immediately");
    expect(startTask).not.toHaveBeenCalled();
    expect(await screen.findByRole("button", { name: "Start first Run" })).toBeTruthy();
    const createdNotice = screen.getByText(/Task created without a Run/);
    expect(createdNotice).toHaveAttribute("role", "status");
  });

  it("selects the durable Task and reports a precise partial failure when Run #1 cannot start", async () => {
    const createdTask = {
      ...task,
      id: "task_partial",
      title: "echo partial",
      prompt: "echo partial",
      execution_kind: "shell" as const,
      shell_command: "echo partial",
      latest_run_id: "",
    };
    vi.mocked(getTasks)
      .mockResolvedValueOnce({ object: "list", data: [] })
      .mockResolvedValue({ object: "list", data: [createdTask] });
    vi.mocked(createTask).mockResolvedValue({ object: "task", data: createdTask });
    vi.mocked(startTask).mockRejectedValue(new Error("executor offline"));
    vi.mocked(getTaskRuns).mockResolvedValue({ object: "list", data: [] });
    const onSelectionChange = vi.fn();
    const state = createRuntimeConsoleFixture({ session: localSession });
    render(
      withRuntimeConsole(<TasksView onSelectionChange={onSelectionChange} />, {
        state,
        actions: createRuntimeConsoleActions(),
      }),
    );

    await screen.findByText("Start a task");
    fireEvent.click(screen.getAllByRole("button", { name: "New task" })[0]);
    fireEvent.change(screen.getByLabelText("Shell command"), {
      target: { value: "echo partial" },
    });
    fireEvent.click(screen.getByRole("button", { name: /Create task & start Run/i }));

    expect(await screen.findByRole("heading", { name: "echo partial" })).toBeTruthy();
    expect(screen.getByRole("heading", { name: "No Runs yet" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Start first Run" })).toBeTruthy();
    expect(
      screen.getByText(
        "Task created, but its first Run could not start: executor offline. Use Start first Run to retry.",
      ),
    ).toHaveAttribute("role", "alert");
    expect(onSelectionChange).toHaveBeenLastCalledWith(createdTask.id, null);
    expect(screen.queryByRole("dialog", { name: "New task" })).toBeNull();
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

  it("shows a retryable initial Task-list failure without treating it as an empty workspace", async () => {
    vi.mocked(getTasks)
      .mockRejectedValueOnce(new Error("task index unavailable"))
      .mockResolvedValue({ object: "list", data: [] });
    const state = createRuntimeConsoleFixture({ session: localSession });
    render(withRuntimeConsole(<TasksView />, { state, actions: createRuntimeConsoleActions() }));

    expect(await screen.findByText("Tasks unavailable")).toBeTruthy();
    expect(screen.getByRole("alert")).toHaveTextContent("task index unavailable");
    expect(screen.getByRole("button", { name: "New task" })).toBeDisabled();
    expect(screen.queryByText("Start a task")).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Retry task load" }));
    expect(await screen.findByText("Start a task")).toBeTruthy();
    expect(screen.getAllByRole("button", { name: "New task" })[0]).toBeEnabled();
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
      expect(screen.getByRole("link", { name: "Task Alpha task, not started" })).toBeTruthy();
    });

    fireEvent.click(screen.getByRole("button", { name: "Project Alpha" }));
    await waitFor(() => {
      expect(screen.getByRole("button", { name: "Project Beta" })).toBeTruthy();
    });
    fireEvent.click(screen.getByRole("button", { name: "Project Beta" }));

    await waitFor(() => {
      expect(vi.mocked(getTasks)).toHaveBeenCalledWith(30, projectB.id);
      expect(screen.getByRole("link", { name: "Task Beta task, not started" })).toBeTruthy();
    });

    expect(screen.queryByRole("link", { name: "Task Alpha task, not started" })).toBeNull();
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

    expect(await screen.findByText("Latest Run · 4 steps")).toBeTruthy();
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

    expect(await screen.findByText("Latest Run · 1 step")).toBeTruthy();
    expect(screen.queryByText("Latest Run · 9 steps")).toBeNull();
  });
});

describe("TasksView selected task", () => {
  it("keeps a committed first Run selected when the follow-up refresh fails", async () => {
    const notStartedTask: TaskRecord = {
      ...task,
      status: "not_started",
      latest_run_id: "",
      latest_model: undefined,
      latest_provider: undefined,
    };
    const startedRun: TaskRunRecord = {
      ...run,
      status: "queued",
      started_at: undefined,
      finished_at: undefined,
    };
    const stream = deferred<void>();
    vi.mocked(getTasks)
      .mockResolvedValueOnce({ object: "list", data: [notStartedTask] })
      .mockRejectedValueOnce(new Error("refresh unavailable"));
    vi.mocked(getTaskRuns).mockResolvedValue({ object: "list", data: [] });
    vi.mocked(startTask).mockResolvedValue({ object: "task_run", data: startedRun });
    vi.mocked(streamTaskRun).mockReturnValue(stream.promise);
    const onSelectionChange = vi.fn();
    const state = createRuntimeConsoleFixture({ session: localSession });
    render(
      withRuntimeConsole(<TasksView onSelectionChange={onSelectionChange} />, {
        state,
        actions: createRuntimeConsoleActions(),
      }),
    );

    fireEvent.click(await screen.findByRole("button", { name: "Start first Run" }));

    expect(await screen.findByRole("button", { name: "Select run" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Start first Run" })).toBeNull();
    expect(
      screen.getByText(
        "First Run started, but Tasks could not refresh. Run #1 is shown from the successful start response; use Refresh task to reconcile.",
      ),
    ).toHaveAttribute("role", "alert");
    expect(onSelectionChange).toHaveBeenCalledWith(notStartedTask.id, startedRun.id);
  });

  it("keeps a committed first Run on its Task without stealing a newer selection", async () => {
    const firstTask: TaskRecord = {
      ...task,
      id: "task_first",
      title: "First task",
      status: "not_started",
      latest_run_id: "",
      latest_model: undefined,
      latest_provider: undefined,
    };
    const secondTask: TaskRecord = {
      ...firstTask,
      id: "task_second",
      title: "Second task",
    };
    const startedRun: TaskRunRecord = {
      ...run,
      id: "run_first",
      task_id: firstTask.id,
      status: "queued",
      started_at: undefined,
      finished_at: undefined,
    };
    const pendingStart = deferred<Awaited<ReturnType<typeof startTask>>>();
    vi.mocked(getTasks)
      .mockResolvedValueOnce({ object: "list", data: [firstTask, secondTask] })
      .mockRejectedValueOnce(new Error("refresh unavailable"));
    vi.mocked(getTaskRuns).mockResolvedValue({ object: "list", data: [] });
    vi.mocked(startTask).mockReturnValue(pendingStart.promise);
    const onSelectionChange = vi.fn();
    const state = createRuntimeConsoleFixture({ session: localSession });
    render(
      withRuntimeConsole(<TasksView onSelectionChange={onSelectionChange} />, {
        state,
        actions: createRuntimeConsoleActions(),
      }),
    );

    fireEvent.click(await screen.findByRole("button", { name: "Start first Run" }));
    await waitFor(() => expect(startTask).toHaveBeenCalledWith(firstTask.id));
    fireEvent.click(screen.getByRole("link", { name: "Task Second task, not started" }));
    expect(await screen.findByRole("heading", { name: "Second task" })).toBeTruthy();

    await act(async () => {
      pendingStart.resolve({ object: "task_run", data: startedRun });
      await pendingStart.promise;
    });

    const firstTaskLink = await screen.findByRole("link", { name: "Task First task" });
    expect(firstTaskLink).toHaveAttribute(
      "href",
      `/tasks?task=${firstTask.id}&run=${startedRun.id}`,
    );
    expect(screen.getByRole("heading", { name: "Second task" })).toBeTruthy();
    expect(screen.queryByRole("link", { name: "Task First task, not started" })).toBeNull();
    expect(onSelectionChange).not.toHaveBeenCalledWith(firstTask.id, startedRun.id);

    fireEvent.click(screen.getByRole("button", { name: "Refresh task" }));
    expect(
      await screen.findByText(/Tasks could not be refreshed: refresh unavailable/i),
    ).toBeTruthy();
    expect(screen.getByRole("link", { name: "Task First task" })).toHaveAttribute(
      "href",
      `/tasks?task=${firstTask.id}&run=${startedRun.id}`,
    );
  });

  it("clears Task-scoped Runs and approvals while a new Task detail load rejects", async () => {
    const firstTask: TaskRecord = {
      ...task,
      id: "task_first",
      title: "First task",
      status: "awaiting_approval",
      latest_run_id: "run_first",
    };
    const secondTask: TaskRecord = {
      ...task,
      id: "task_second",
      title: "Second task",
      status: "not_started",
      latest_run_id: "",
    };
    const firstRun: TaskRunRecord = {
      ...run,
      id: "run_first",
      task_id: firstTask.id,
      status: "awaiting_approval",
    };
    const firstApproval: TaskApprovalRecord = {
      id: "approval_first",
      task_id: firstTask.id,
      run_id: firstRun.id,
      kind: "shell_command",
      status: "pending",
      reason: "First task approval",
    };
    const secondRuns = deferred<Awaited<ReturnType<typeof getTaskRuns>>>();
    const stream = deferred<void>();
    vi.mocked(getTasks).mockResolvedValue({ object: "list", data: [firstTask, secondTask] });
    vi.mocked(getTaskRuns).mockImplementation((taskID) =>
      taskID === firstTask.id
        ? Promise.resolve({ object: "list", data: [firstRun] })
        : secondRuns.promise,
    );
    vi.mocked(getTaskApprovals).mockImplementation(async (taskID) => ({
      object: "list",
      data: taskID === firstTask.id ? [firstApproval] : [],
    }));
    vi.mocked(streamTaskRun).mockReturnValue(stream.promise);
    const state = createRuntimeConsoleFixture({ session: localSession });
    render(withRuntimeConsole(<TasksView />, { state, actions: createRuntimeConsoleActions() }));

    expect(await screen.findByText("Approval required")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Select run" })).toBeTruthy();

    fireEvent.click(screen.getByRole("link", { name: "Task Second task, not started" }));

    expect(await screen.findByRole("heading", { name: "Second task" })).toBeTruthy();
    expect(screen.queryByText("Approval required")).toBeNull();
    expect(screen.queryByRole("button", { name: "Select run" })).toBeNull();
    expect(screen.getByRole("button", { name: "Start first Run" })).toBeTruthy();

    await act(async () => {
      secondRuns.reject(new Error("detail unavailable"));
      try {
        await secondRuns.promise;
      } catch {
        // The rejected detail request is the regression condition.
      }
    });

    expect(screen.queryByText("Approval required")).toBeNull();
    expect(screen.queryByRole("button", { name: "Select run" })).toBeNull();
    expect(screen.getByRole("button", { name: "Start first Run" })).toBeTruthy();
  });

  it("preserves stale Tasks and offers a local retry when refresh fails", async () => {
    vi.mocked(getTasks)
      .mockResolvedValueOnce({ object: "list", data: [task] })
      .mockRejectedValueOnce(new Error("refresh unavailable"))
      .mockResolvedValue({ object: "list", data: [task] });
    vi.mocked(getTaskRuns).mockResolvedValue({ object: "list", data: [run] });
    const state = createRuntimeConsoleFixture({ session: localSession });
    render(withRuntimeConsole(<TasksView />, { state, actions: createRuntimeConsoleActions() }));

    expect(await screen.findByRole("heading", { name: task.title })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Refresh task" }));

    expect(
      await screen.findByText(/Tasks could not be refreshed: refresh unavailable/i),
    ).toBeTruthy();
    expect(screen.getByRole("link", { name: `Task ${task.title}` })).toBeTruthy();
    expect(screen.getByRole("button", { name: "New task" })).toBeDisabled();

    fireEvent.click(screen.getByRole("button", { name: "Retry task load" }));
    await waitFor(() => expect(screen.queryByText(/Tasks could not be refreshed/i)).toBeNull());
    expect(screen.getByRole("link", { name: `Task ${task.title}` })).toBeTruthy();
  });

  it("requires explicit confirmation before permanently deleting a Task", async () => {
    vi.mocked(getTasks).mockResolvedValue({ object: "list", data: [task] });
    vi.mocked(getTaskRuns).mockResolvedValue({ object: "list", data: [run] });
    const state = createRuntimeConsoleFixture({ session: localSession });
    render(withRuntimeConsole(<TasksView />, { state, actions: createRuntimeConsoleActions() }));

    const taskIndexHeading = screen.getByRole("heading", { name: /^Tasks/ });
    expect(taskIndexHeading).toHaveAttribute("tabindex", "-1");
    const row = await screen.findByRole("link", { name: `Task ${task.title}` });
    fireEvent.focus(row);
    const deleteButton = screen.getByRole("button", { name: `Delete task ${task.title}` });
    fireEvent.focus(deleteButton);
    fireEvent.click(deleteButton);

    const dialog = screen.getByRole("dialog", { name: "Delete task" });
    expect(dialog).toHaveTextContent(/all of its Runs, its Schedule, and occurrence history/i);
    expect(deleteTask).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Delete task" }));
    await waitFor(() => expect(deleteTask).toHaveBeenCalledWith(task.id));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Delete task" })).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(taskIndexHeading));
  });

  it("keeps Task deletion modal while the destructive request is pending", async () => {
    const pendingDelete = deferred<void>();
    vi.mocked(getTasks).mockResolvedValue({ object: "list", data: [task] });
    vi.mocked(getTaskRuns).mockResolvedValue({ object: "list", data: [run] });
    vi.mocked(deleteTask).mockReturnValue(pendingDelete.promise);
    const state = createRuntimeConsoleFixture({ session: localSession });
    render(withRuntimeConsole(<TasksView />, { state, actions: createRuntimeConsoleActions() }));

    const row = await screen.findByRole("link", { name: `Task ${task.title}` });
    fireEvent.focus(row);
    fireEvent.click(screen.getByRole("button", { name: `Delete task ${task.title}` }));
    fireEvent.click(screen.getByRole("button", { name: "Delete task" }));

    const workingButton = await screen.findByRole("button", { name: "Working…" });
    expect(workingButton).toBeDisabled();
    fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.getByRole("dialog", { name: "Delete task" })).toBeTruthy();
    fireEvent.click(workingButton);
    expect(deleteTask).toHaveBeenCalledTimes(1);

    await act(async () => {
      pendingDelete.resolve();
      await pendingDelete.promise;
    });
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Delete task" })).toBeNull());
  });

  it("returns delete focus to the Tasks index when New task is disabled", async () => {
    const secondTask: TaskRecord = {
      ...task,
      id: "task_second",
      title: "Second task",
      latest_run_id: "run_second",
    };
    vi.mocked(getTasks)
      .mockResolvedValueOnce({ object: "list", data: [task, secondTask] })
      .mockRejectedValueOnce(new Error("refresh unavailable"));
    vi.mocked(getTaskRuns).mockResolvedValue({ object: "list", data: [run] });
    const state = createRuntimeConsoleFixture({ session: localSession });
    render(withRuntimeConsole(<TasksView />, { state, actions: createRuntimeConsoleActions() }));

    expect(await screen.findByRole("heading", { name: task.title })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Refresh task" }));
    expect(
      await screen.findByText(/Tasks could not be refreshed: refresh unavailable/i),
    ).toBeTruthy();
    expect(screen.getByRole("button", { name: "New task" })).toBeDisabled();

    const taskIndexHeading = screen.getByRole("heading", { name: /^Tasks/ });
    const secondRow = screen.getByRole("link", { name: "Task Second task" });
    fireEvent.focus(secondRow);
    fireEvent.click(screen.getByRole("button", { name: "Delete task Second task" }));
    fireEvent.click(screen.getByRole("button", { name: "Delete task" }));

    await waitFor(() => expect(deleteTask).toHaveBeenCalledWith(secondTask.id));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Delete task" })).toBeNull());
    expect(screen.getByRole("button", { name: "New task" })).toBeDisabled();
    await waitFor(() => expect(document.activeElement).toBe(taskIndexHeading));
  });

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

    expect(await screen.findByRole("link", { name: "Task Addressed task" })).toBeTruthy();
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
    expect(await screen.findByRole("link", { name: "Task Task B" })).toBeTruthy();
    await act(async () => resolveTaskA?.({ object: "task", data: taskA }));

    await waitFor(() => expect(screen.getByRole("link", { name: "Task Task B" })).toBeTruthy());
    expect(
      screen.queryByRole("link", { name: "Task Task A" })?.getAttribute("aria-current"),
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
