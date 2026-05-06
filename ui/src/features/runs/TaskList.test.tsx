import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { TaskList } from "./TaskList";
import type { TaskRecord } from "../../types/runtime";

function makeTask(overrides: Partial<TaskRecord> = {}): TaskRecord {
  return {
    id: "task-1",
    title: "List the working directory",
    prompt: "ls -la",
    status: "completed",
    execution_kind: "shell",
    shell_command: "ls -la",
    step_count: 2,
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
    onRefresh: vi.fn(),
    ...propOverrides,
  };
  const user = userEvent.setup();
  return { props, user, render: () => render(<TaskList {...props} />) };
}

describe("TaskList", () => {
  it("shows the loading state when loading is true and no tasks", () => {
    const { render } = setup({ tasks: [], loading: true });
    render();
    expect(screen.getByText(/loading/i)).toBeTruthy();
  });

  it("shows the empty state when not loading and no tasks", () => {
    const { render } = setup({ tasks: [], loading: false });
    render();
    expect(screen.getByText(/no tasks yet/i)).toBeTruthy();
  });

  it("renders task title, kind badge, step count, and short run id", () => {
    const { render } = setup();
    render();
    expect(screen.getByText("List the working directory")).toBeTruthy();
    expect(screen.getByText("shell")).toBeTruthy();
    expect(screen.getByText(/2 steps/)).toBeTruthy();
    // Run id is truncated to 8 chars in the list row.
    expect(screen.getByText(/run: run-abcd/)).toBeTruthy();
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
    const row = screen.getByRole("button", { name: /^Task List the working directory$/ });
    row.focus();
    await user.keyboard("{Enter}");
    expect(onSelect).toHaveBeenLastCalledWith("task-1");
    await user.keyboard(" ");
    expect(onSelect).toHaveBeenLastCalledWith("task-1");
  });

  it("clicking the delete icon calls onDelete and does NOT trigger onSelect", async () => {
    // The row's onSelect handler wraps the delete button, so the button
    // must stop propagation. If it stops calling stopPropagation, every
    // delete action would also re-select the deleted task — confusing
    // and racy.
    const onSelect = vi.fn();
    const onDelete = vi.fn();
    const { render, user } = setup({ onSelect, onDelete });
    render();
    await user.click(screen.getByRole("button", { name: /delete task list the working directory/i }));
    expect(onDelete).toHaveBeenCalledWith("task-1");
    expect(onSelect).not.toHaveBeenCalled();
  });

  it("hides the delete button while a task is running", () => {
    const { render } = setup({ tasks: [makeTask({ status: "running" })] });
    render();
    expect(screen.queryByTitle("Delete")).toBeNull();
  });

  it("disables the delete button while that task's delete is in flight", () => {
    const { render } = setup({ busyAction: "delete:task-1" });
    render();
    expect((screen.getByTitle("Delete") as HTMLButtonElement).disabled).toBe(true);
  });

  it("'New task' button calls onNewTask", async () => {
    const onNewTask = vi.fn();
    const { render, user } = setup({ onNewTask });
    render();
    await user.click(screen.getByRole("button", { name: /new task/i }));
    expect(onNewTask).toHaveBeenCalled();
  });

  it("refresh button calls onRefresh", async () => {
    const onRefresh = vi.fn();
    const { render, user } = setup({ onRefresh });
    render();
    await user.click(screen.getByRole("button", { name: /refresh tasks/i }));
    expect(onRefresh).toHaveBeenCalled();
  });

  it("shows 'not started' when the task has no latest_run_id", () => {
    const { render } = setup({ tasks: [makeTask({ latest_run_id: undefined })] });
    render();
    expect(screen.getByText(/not started/i)).toBeTruthy();
  });

  it("renders the file path as the kind label for file tasks", () => {
    const { render } = setup({
      tasks: [makeTask({ execution_kind: "file", file_path: "/tmp/note.txt", shell_command: undefined })],
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

  it("renders Hecate Agent chat origin metadata", () => {
    const { render } = setup({
      tasks: [
        makeTask({
          execution_kind: "agent_loop",
          execution_profile: "chat_hecate_agent",
          origin_kind: "agent_chat",
          origin_id: "agent_chat_123",
          shell_command: undefined,
        }),
      ],
    });
    render();
    expect(screen.getByText("from chat")).toBeTruthy();
    expect(screen.getByText("hecate agent")).toBeTruthy();
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
