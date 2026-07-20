import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { TaskList } from "./TaskList";
import type { TaskRecord } from "../../types/task";

function makeTask(overrides: Partial<TaskRecord> = {}): TaskRecord {
  return {
    id: "task-1",
    title: "List the working directory",
    prompt: "ls -la",
    status: "completed",
    execution_kind: "shell",
    shell_command: "ls -la",
    latest_run_step_count: 2,
    latest_run_id: "run-abcdef12",
    ...overrides,
  } as TaskRecord;
}

function setup(propOverrides: Partial<React.ComponentProps<typeof TaskList>> = {}) {
  const props: React.ComponentProps<typeof TaskList> = {
    tasks: [makeTask()],
    selectedTaskID: "task-1",
    loading: false,
    busyAction: "",
    onSelect: vi.fn(),
    onDelete: vi.fn(),
    onNewTask: vi.fn(),
    ...propOverrides,
  };
  const user = userEvent.setup();
  return { props, user, render: () => render(<TaskList {...props} />) };
}

describe("TaskList", () => {
  it("shows the loading state when loading is true and no tasks", () => {
    const { render } = setup({ tasks: [], loading: true });
    render();
    expect(screen.getByText(/loading tasks/i)).toBeTruthy();
  });

  it("keeps existing rows visible during refresh instead of flashing a list loading state", () => {
    const { render } = setup({ loading: true });
    render();
    expect(screen.getByText("List the working directory")).toBeTruthy();
    expect(screen.queryByText(/loading tasks/i)).toBeNull();
  });

  it("uses the same sidebar width as the chat list", () => {
    const { render } = setup();
    render();
    expect(screen.getByRole("complementary", { name: "Tasks" }).style.width).toBe(
      "var(--entity-index-width, 220px)",
    );
  });

  it("shows the empty state when not loading and no tasks", () => {
    const { render } = setup({ tasks: [], loading: false });
    render();
    expect(screen.getByText(/no tasks yet/i)).toBeTruthy();
  });

  it("renders task title, kind badge, and latest Run step count without exposing Run ids", () => {
    const { render } = setup();
    render();
    expect(screen.getByText("List the working directory")).toBeTruthy();
    expect(screen.getByText("shell")).toBeTruthy();
    expect(screen.getByText("Latest Run · 2 steps")).toBeTruthy();
    expect(screen.queryByText(/run abcdef12/)).toBeNull();
  });

  it("renders the kind label preview ($ ls -la for shell tasks)", () => {
    const { render } = setup();
    render();
    expect(screen.getByText("$ ls -la")).toBeTruthy();
  });

  it("falls back to the prompt when the title is missing", () => {
    const { render } = setup({ tasks: [makeTask({ title: undefined, prompt: "do the thing" })] });
    render();
    expect(screen.getByText("do the thing")).toBeTruthy();
  });

  it("falls back to 'Untitled task' when both title and prompt are missing", () => {
    const { render } = setup({ tasks: [makeTask({ title: undefined, prompt: undefined })] });
    render();
    expect(screen.getByText(/untitled task/i)).toBeTruthy();
  });

  it("clicking a row calls onSelect with the task id", async () => {
    const onSelect = vi.fn();
    const { render, user } = setup({ onSelect });
    render();
    await user.click(screen.getByText("List the working directory"));
    expect(onSelect).toHaveBeenCalledWith("task-1");
  });

  it("selects a row with Enter and Space when focused", async () => {
    const onSelect = vi.fn();
    const { render, user } = setup({ onSelect });
    render();
    const row = screen.getByRole("link", { name: /^Task List the working directory$/ });
    expect(row).toHaveAttribute("href", "/tasks?task=task-1&run=run-abcdef12");
    act(() => row.focus());
    await user.keyboard("{Enter}");
    expect(onSelect).toHaveBeenLastCalledWith("task-1");
    await user.keyboard(" ");
    expect(onSelect).toHaveBeenLastCalledWith("task-1");
  });

  it("clicking the delete icon calls onDelete and does NOT trigger onSelect", async () => {
    // Row actions stay beside the navigation anchor. Reintroducing a
    // nested action would make deletion race task selection and produce
    // invalid interactive markup.
    const onSelect = vi.fn();
    const onDelete = vi.fn();
    const { render, user } = setup({ onSelect, onDelete });
    render();
    const row = screen.getByRole("link", { name: /^Task List the working directory$/ });
    await user.hover(row);
    const deleteButton = (await screen.findByRole("button", {
      name: /delete task list the working directory/i,
    })) as HTMLButtonElement;
    await user.click(deleteButton);
    expect(onDelete).toHaveBeenCalledWith("task-1");
    expect(onSelect).not.toHaveBeenCalled();
  });

  it("keeps the delete action visually hidden until the row is hovered or focused", async () => {
    const { render, user } = setup();
    render();
    const row = screen.getByRole("link", { name: /^Task List the working directory$/ });
    expect(
      screen.queryByRole("button", { name: /delete task list the working directory/i }),
    ).toBeNull();

    await user.hover(row);
    expect(
      await screen.findByRole("button", { name: /delete task list the working directory/i }),
    ).toBeTruthy();

    await user.unhover(row);
    await waitFor(() => {
      expect(
        screen.queryByRole("button", { name: /delete task list the working directory/i }),
      ).toBeNull();
    });

    fireEvent.focus(row);
    expect(
      await screen.findByRole("button", { name: /delete task list the working directory/i }),
    ).toBeTruthy();
  });

  it("hides the delete button while a task is running", () => {
    const { render } = setup({ tasks: [makeTask({ status: "running" })] });
    render();
    expect(screen.queryByTitle("Delete")).toBeNull();
  });

  it("disables the delete button during any conflicting Task mutation", () => {
    const { render } = setup({ busyAction: "start" });
    render();
    fireEvent.focus(screen.getByRole("link", { name: /^Task List the working directory$/ }));
    expect(
      screen.getByRole("button", {
        name: /delete task list the working directory/i,
      }),
    ).toBeDisabled();
  });

  it("keeps the delete button disabled from delete-owned pending state", () => {
    const { render } = setup({ deletingTaskID: "task-1" });
    render();
    fireEvent.focus(screen.getByRole("link", { name: /^Task List the working directory$/ }));
    expect(
      screen.getByRole("button", {
        name: /delete task list the working directory/i,
      }),
    ).toBeDisabled();
  });

  it("disables the delete button while that task's delete is in flight", () => {
    const { render } = setup({ busyAction: "delete:task-1" });
    render();
    fireEvent.focus(screen.getByRole("link", { name: /^Task List the working directory$/ }));
    expect(
      (
        screen.getByRole("button", {
          name: /delete task list the working directory/i,
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
  });

  it("keeps modified task-row clicks as native navigation", () => {
    const onSelect = vi.fn();
    const { render } = setup({ onSelect });
    render();
    const row = screen.getByRole("link", { name: /^Task List the working directory$/ });
    document.addEventListener("click", (event) => event.preventDefault(), { once: true });
    fireEvent.click(row, { metaKey: true });
    expect(onSelect).not.toHaveBeenCalled();
  });

  it("'New task' button calls onNewTask without an inline plus label", async () => {
    const onNewTask = vi.fn();
    const { render, user } = setup({ onNewTask });
    render();
    const button = screen.getByRole("button", { name: "New task" });
    await user.click(button);
    expect(onNewTask).toHaveBeenCalled();
  });

  it("disables Task creation while the Task list is unavailable", () => {
    const { render } = setup({
      newTaskDisabled: true,
      newTaskDisabledReason: "Retry the Task list before creating a Task.",
    });
    render();
    const button = screen.getByRole("button", { name: "New task" });
    expect(button).toBeDisabled();
    expect(button).toHaveAttribute("title", "Retry the Task list before creating a Task.");
  });

  it("exposes the four task filters as pressed-state buttons", async () => {
    const onFilterChange = vi.fn();
    const { render, user } = setup({ filter: "scheduled", onFilterChange });
    render();

    expect(screen.getByRole("button", { name: "Scheduled" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    await user.click(screen.getByRole("button", { name: "Needs attention" }));
    expect(onFilterChange).toHaveBeenCalledWith("attention");
  });

  it("gates Scheduled filtering while Schedule data is unavailable", async () => {
    const onFilterChange = vi.fn();
    const { render, user } = setup({
      filter: "scheduled",
      onFilterChange,
      scheduleLoadState: "error",
      scheduleLoadError: "scheduler unavailable",
    });
    render();

    const scheduled = screen.getByRole("button", { name: "Scheduled" });
    expect(scheduled).toHaveAttribute("aria-disabled", "true");
    expect(scheduled).toHaveAttribute("aria-describedby", "task-scheduled-filter-status");
    expect(screen.getByRole("button", { name: "All" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByText(/Scheduled filter unavailable: scheduler unavailable/)).toBeTruthy();

    await user.click(scheduled);
    expect(onFilterChange).not.toHaveBeenCalled();
    expect(screen.getByText("List the working directory")).toBeTruthy();
  });

  it("marks tasks with paused or enabled schedule configuration", () => {
    const schedules = new Map([
      [
        "task-1",
        {
          id: "schedule-1",
          task_id: "task-1",
          kind: "cron" as const,
          cron_expression: "0 9 * * *",
          timezone: "UTC",
          enabled: false,
          created_at: "2026-07-20T08:00:00Z",
          updated_at: "2026-07-20T08:00:00Z",
        },
      ],
    ]);
    const { render } = setup({ schedulesByTaskID: schedules });
    render();
    expect(screen.getByText("Schedule paused")).toBeTruthy();
  });

  it("shows the exact next Run and timezone in the task row", () => {
    const schedules = new Map([
      [
        "task-1",
        {
          id: "schedule-1",
          task_id: "task-1",
          kind: "cron" as const,
          cron_expression: "0 9 * * *",
          timezone: "Europe/Madrid",
          enabled: true,
          next_run_at: "2026-07-21T07:00:00Z",
          created_at: "2026-07-20T08:00:00Z",
          updated_at: "2026-07-20T08:00:00Z",
        },
      ],
    ]);
    const { render } = setup({ schedulesByTaskID: schedules });
    render();

    expect(screen.getByText(/Next · .*Europe\/Madrid/)).toBeTruthy();
    expect(
      screen.getByRole("link", {
        name: /Task List the working directory, Next · .*Europe\/Madrid/,
      }),
    ).toBeTruthy();
  });

  it("labels a fired one-off as completed instead of paused", () => {
    const schedules = new Map([
      [
        "task-1",
        {
          id: "schedule-1",
          task_id: "task-1",
          kind: "once" as const,
          timezone: "UTC",
          run_at: "2026-07-20T08:00:00Z",
          enabled: false,
          created_at: "2026-07-19T08:00:00Z",
          updated_at: "2026-07-20T08:00:01Z",
        },
      ],
    ]);
    const { render } = setup({ schedulesByTaskID: schedules });
    render();

    expect(screen.getByText("Completed schedule")).toBeTruthy();
    expect(screen.queryByText("Schedule paused")).toBeNull();
  });

  it("labels a Task without Runs as not started and omits latest Run metadata", () => {
    const { render } = setup({
      tasks: [
        makeTask({
          latest_run_id: undefined,
          latest_model: "stale-model",
          latest_provider: "stale-provider",
        }),
      ],
    });
    render();
    expect(screen.getByText("not started")).toBeTruthy();
    expect(screen.queryByText(/Latest Run/i)).toBeNull();
    expect(screen.queryByText(/stale-model/i)).toBeNull();
    expect(
      screen.getByRole("link", { name: "Task List the working directory, not started" }),
    ).toBeTruthy();
  });

  it("renders the file path as the kind label for file tasks", () => {
    const { render } = setup({
      tasks: [
        makeTask({ execution_kind: "file", file_path: "/tmp/note.txt", shell_command: undefined }),
      ],
    });
    render();
    expect(screen.getByText("/tmp/note.txt")).toBeTruthy();
  });

  it("renders 'agent' as the kind label for agent_loop tasks", () => {
    const { render } = setup({
      tasks: [makeTask({ execution_kind: "agent_loop", shell_command: undefined })],
    });
    render();
    expect(screen.getByText("agent")).toBeTruthy();
  });

  it("renders Hecate Chat origin metadata", () => {
    const { render } = setup({
      tasks: [
        makeTask({
          execution_kind: "agent_loop",
          execution_profile: "chat_agent",
          origin_kind: "chat",
          origin_id: "chat_123",
          shell_command: undefined,
        }),
      ],
    });
    render();
    expect(screen.getByText("From chat")).toBeTruthy();
    expect(screen.getByText("hecate agent")).toBeTruthy();
  });

  it("labels project-assignment and standalone task sources", () => {
    const { render } = setup({
      tasks: [
        makeTask({
          id: "task-assigned",
          assignment_id: "asgn_123",
          origin_kind: "project_work_item",
        }),
        makeTask({ id: "task-standalone", origin_kind: undefined, origin_id: undefined }),
      ],
    });
    render();
    expect(screen.getByText("Project assignment")).toBeTruthy();
    expect(screen.getByText("Standalone")).toBeTruthy();
  });

  it("renders an 'MCP × N' chip when the task configures MCP servers", () => {
    // Two configured servers → "MCP × 2" with an aria-label that
    // names the count, so screen readers announce the chip
    // consistently across rows.
    const { render } = setup({
      tasks: [
        makeTask({
          execution_kind: "agent_loop",
          shell_command: undefined,
          mcp_servers: [
            { name: "github", url: "https://api.example.com/mcp" },
            { name: "filesystem", command: "bunx" },
          ],
        }),
      ],
    });
    render();
    expect(screen.getByText("MCP × 2")).toBeTruthy();
    expect(screen.getByLabelText(/2 MCP servers configured/i)).toBeTruthy();
  });

  it("hides the MCP chip when mcp_servers is empty or missing", () => {
    // Both shell tasks (no mcp_servers field) and agent_loop tasks
    // with an empty array must render WITHOUT the chip — otherwise
    // operators can't tell at a glance which agent_loop runs use
    // external tool sources.
    const { render } = setup({
      tasks: [
        makeTask({ id: "task-no-mcp", execution_kind: "agent_loop", shell_command: undefined }),
      ],
    });
    render();
    expect(screen.queryByText(/^MCP × /)).toBeNull();
    expect(screen.queryByLabelText(/MCP server.*configured/i)).toBeNull();
  });

  it("renders 'MCP × 1' (singular) and a singular aria-label for one server", () => {
    // Pluralization edge-case — "1 MCP servers" reads wrong.
    const { render } = setup({
      tasks: [
        makeTask({
          execution_kind: "agent_loop",
          shell_command: undefined,
          mcp_servers: [{ name: "fs", command: "bunx" }],
        }),
      ],
    });
    render();
    expect(screen.getByText("MCP × 1")).toBeTruthy();
    expect(screen.getByLabelText(/^1 MCP server configured$/i)).toBeTruthy();
  });
});
