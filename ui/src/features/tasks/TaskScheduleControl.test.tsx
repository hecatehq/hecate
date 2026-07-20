import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import type { UpsertTaskSchedulePayload } from "../../lib/api";
import type { TaskScheduleRecord } from "../../types/task";
import {
  formatScheduleDateTime,
  scheduleSummary,
  scheduleVisibleStatus,
  TaskScheduleControl,
  toLocalDateTimeInput,
} from "./TaskScheduleControl";

function recurringSchedule(overrides: Partial<TaskScheduleRecord> = {}): TaskScheduleRecord {
  return {
    id: "schedule_1",
    task_id: "task_1",
    kind: "cron",
    cron_expression: "0 9 * * 1-5",
    timezone: "Europe/Madrid",
    enabled: true,
    created_at: "2026-07-20T08:00:00Z",
    updated_at: "2026-07-20T08:00:00Z",
    ...overrides,
  };
}

describe("TaskScheduleControl helpers", () => {
  it("describes recurring schedules without inventing natural-language cron semantics", () => {
    expect(scheduleSummary(recurringSchedule())).toBe("0 9 * * 1-5 · Europe/Madrid");
  });

  it("labels disabled schedules as paused", () => {
    expect(
      scheduleSummary({
        id: "schedule_1",
        task_id: "task_1",
        kind: "once",
        timezone: "UTC",
        run_at: "2026-07-21T08:00:00Z",
        enabled: false,
        created_at: "2026-07-20T08:00:00Z",
        updated_at: "2026-07-20T08:00:00Z",
      }),
    ).toBe("Schedule paused");
  });

  it("formats a valid timestamp for datetime-local", () => {
    expect(toLocalDateTimeInput("2026-07-21T08:05:00Z")).toMatch(/^2026-07-21T\d{2}:05$/);
  });

  it("distinguishes a fired one-off from a paused schedule", () => {
    const fired = recurringSchedule({
      kind: "once",
      cron_expression: undefined,
      run_at: "2026-07-20T08:00:00Z",
      enabled: false,
      updated_at: "2026-07-20T08:00:01Z",
    });
    expect(scheduleSummary(fired)).toMatch(/^Completed ·/);
    expect(scheduleVisibleStatus(fired)).toBe("Completed schedule");
  });

  it("formats schedule instants in the named IANA timezone", () => {
    expect(formatScheduleDateTime("2026-07-21T08:05:00Z", "Europe/Madrid")).toContain(
      "Europe/Madrid",
    );
  });
});

describe("TaskScheduleControl dialog", () => {
  it("opens an accessible schedule form and submits a future one-off", async () => {
    const onSave = vi.fn(async (_payload: UpsertTaskSchedulePayload) => {});
    const user = userEvent.setup();
    render(
      <TaskScheduleControl
        schedule={null}
        occurrences={[]}
        operation={null}
        onSave={onSave}
        onDelete={vi.fn(async () => {})}
      />,
    );

    const trigger = screen.getByRole("button", { name: "Schedule" });
    await user.click(trigger);
    expect(screen.getByRole("dialog", { name: "Schedule this task" })).toBeTruthy();
    expect(screen.getByLabelText("Run at")).toBeTruthy();
    expect(screen.getByLabelText("Timezone")).toBeTruthy();
    expect(screen.getByLabelText(/Schedule enabled/)).toBeTruthy();
    expect(screen.getByLabelText("Run at")).toHaveAttribute("name", "schedule-run-at");
    expect(screen.getByLabelText("Run at")).toHaveAttribute("autocomplete", "off");

    await user.click(screen.getByRole("button", { name: "Save schedule" }));
    expect(onSave).toHaveBeenCalledTimes(1);
    expect(onSave.mock.calls[0][0]).toMatchObject({ kind: "once", enabled: true });
    expect(onSave.mock.calls[0][0].run_at).toMatch(/Z$/);
  });

  it("announces an invalid six-field cron expression and links it to the input", async () => {
    const user = userEvent.setup();
    render(
      <TaskScheduleControl
        schedule={null}
        occurrences={[]}
        operation={null}
        onSave={vi.fn(async () => {})}
        onDelete={vi.fn(async () => {})}
      />,
    );
    await user.click(screen.getByRole("button", { name: "Schedule" }));
    await user.click(screen.getByLabelText("Recurring"));
    const cron = screen.getByLabelText("Cron expression");
    await user.clear(cron);
    await user.type(cron, "0 0 9 * * *");
    await user.click(screen.getByRole("button", { name: "Save schedule" }));

    expect(screen.getByRole("alert").textContent).toMatch(/five-field cron expression/i);
    expect(cron).toHaveAttribute("aria-invalid", "true");
    expect(cron.getAttribute("aria-describedby")).toContain("task-schedule-cron-error");
    expect(cron).not.toHaveAttribute("placeholder");
  });

  it("disables schedule mutations until availability is known and exposes load failure", () => {
    const props: React.ComponentProps<typeof TaskScheduleControl> = {
      schedule: null,
      occurrences: [],
      availability: "loading",
      operation: null,
      onSave: vi.fn(async () => {}),
      onDelete: vi.fn(async () => {}),
    };
    const view = render(<TaskScheduleControl {...props} />);

    expect(screen.getByRole("button", { name: "Loading schedule…" })).toBeDisabled();

    view.rerender(
      <TaskScheduleControl
        {...props}
        availability="error"
        availabilityError="scheduler unavailable"
      />,
    );
    const unavailable = screen.getByRole("button", { name: "Schedule unavailable" });
    expect(unavailable).toBeDisabled();
    expect(unavailable).toHaveAttribute("title", "scheduler unavailable");
  });

  it("distinguishes loading, failed, and empty occurrence history", async () => {
    const user = userEvent.setup();
    const schedule = recurringSchedule();
    const props: React.ComponentProps<typeof TaskScheduleControl> = {
      schedule,
      occurrences: [],
      historyState: "loading",
      operation: null,
      onSave: vi.fn(async () => {}),
      onDelete: vi.fn(async () => {}),
    };
    const view = render(<TaskScheduleControl {...props} />);
    await user.click(screen.getByTitle(/Edit schedule/));

    expect(screen.getByRole("status")).toHaveTextContent("Loading occurrences…");

    view.rerender(
      <TaskScheduleControl {...props} historyState="error" historyError="history unavailable" />,
    );
    expect(screen.getByRole("alert")).toHaveTextContent(
      /Occurrence history could not be loaded: history unavailable/i,
    );

    view.rerender(<TaskScheduleControl {...props} historyState="loaded" />);
    expect(screen.getByText("No occurrences yet.")).toBeTruthy();
  });

  it("associates a backend timezone error with the timezone field and focuses it", async () => {
    const user = userEvent.setup();
    render(
      <TaskScheduleControl
        schedule={recurringSchedule()}
        occurrences={[]}
        operation={null}
        onSave={vi.fn(async () => {
          throw new Error("task schedule timezone is invalid: Mars/Olympus");
        })}
        onDelete={vi.fn(async () => {})}
      />,
    );
    await user.click(screen.getByTitle(/Edit schedule/));
    await user.click(screen.getByRole("button", { name: "Save schedule" }));

    const timezone = screen.getByLabelText("Timezone");
    expect(timezone).toHaveAttribute("aria-invalid", "true");
    expect(timezone.getAttribute("aria-describedby")).toContain("task-schedule-timezone-error");
    expect(document.activeElement).toBe(timezone);
  });

  it("shows the next Run with timezone identity in the visible trigger and dialog", async () => {
    const user = userEvent.setup();
    const schedule = recurringSchedule({ next_run_at: "2026-07-21T08:05:00Z" });
    render(
      <TaskScheduleControl
        schedule={schedule}
        occurrences={[]}
        operation={null}
        onSave={vi.fn(async () => {})}
        onDelete={vi.fn(async () => {})}
      />,
    );

    const trigger = screen.getByRole("button", { name: /Next · .*Europe\/Madrid/ });
    await user.click(trigger);
    expect(screen.getByText("Next Run")).toBeTruthy();
    expect(screen.getAllByText(/Europe\/Madrid/).length).toBeGreaterThan(0);
  });

  it("uses delete-specific pending copy and disables editable schedule fields", async () => {
    const user = userEvent.setup();
    const schedule = recurringSchedule();
    const props: React.ComponentProps<typeof TaskScheduleControl> = {
      schedule,
      occurrences: [],
      operation: null,
      onSave: vi.fn(async () => {}),
      onDelete: vi.fn(async () => {}),
    };
    const view = render(<TaskScheduleControl {...props} />);
    await user.click(screen.getByTitle(/Edit schedule/));
    view.rerender(<TaskScheduleControl {...props} operation="delete" />);

    expect(screen.getByRole("button", { name: "Removing schedule…" })).toBeDisabled();
    expect(screen.getByLabelText("Cron expression")).toBeDisabled();
    expect(screen.getByLabelText("Timezone")).toBeDisabled();
    expect(screen.getByRole("button", { name: "Save schedule" })).toBeDisabled();
  });

  it("keeps focus in the modal while deletion is pending and restores confirm focus on rejection", async () => {
    let rejectDelete!: (error: Error) => void;
    const pendingDelete = new Promise<void>((_resolve, reject) => {
      rejectDelete = reject;
    });
    const schedule = recurringSchedule();
    const onSave = vi.fn(async () => {});
    const user = userEvent.setup();

    function RejectingDeleteHarness() {
      const [operation, setOperation] = useState<"delete" | null>(null);
      return (
        <TaskScheduleControl
          schedule={schedule}
          occurrences={[]}
          operation={operation}
          onSave={onSave}
          onDelete={async () => {
            setOperation("delete");
            try {
              await pendingDelete;
            } finally {
              setOperation(null);
            }
          }}
        />
      );
    }

    render(<RejectingDeleteHarness />);
    await user.click(screen.getByTitle(/Edit schedule/));
    await user.click(screen.getByRole("button", { name: "Remove schedule" }));
    await user.click(screen.getByRole("button", { name: "Confirm remove" }));

    const dialog = screen.getByRole("dialog", { name: "Edit task schedule" });
    await waitFor(() =>
      expect(screen.getByText(/Permanently deletes this Schedule/)).toHaveFocus(),
    );
    expect(dialog).toContainElement(document.activeElement as HTMLElement);
    expect(screen.getByRole("button", { name: "Removing schedule…" })).toBeDisabled();
    await user.keyboard("{Escape}");
    expect(screen.getByRole("dialog", { name: "Edit task schedule" })).toBeTruthy();

    await act(async () => {
      rejectDelete(new Error("schedule deletion unavailable"));
      await pendingDelete.catch(() => {});
    });

    expect(await screen.findByText("schedule deletion unavailable")).toBeTruthy();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Confirm remove" })).toHaveFocus(),
    );
  });

  it("discloses permanent occurrence-history deletion before confirming removal", async () => {
    const onDelete = vi.fn(async () => {});
    const user = userEvent.setup();
    render(
      <TaskScheduleControl
        schedule={recurringSchedule()}
        occurrences={[]}
        operation={null}
        onSave={vi.fn(async () => {})}
        onDelete={onDelete}
      />,
    );

    const trigger = screen.getByTitle(/Edit schedule/);
    await user.click(trigger);
    await user.click(screen.getByRole("button", { name: "Remove schedule" }));

    const confirm = screen.getByRole("button", { name: "Confirm remove" });
    expect(confirm).toHaveFocus();
    expect(confirm).toHaveAttribute("aria-describedby", "task-schedule-delete-warning");
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Permanently deletes this Schedule and its occurrence history. This cannot be undone.",
    );
    expect(onDelete).not.toHaveBeenCalled();

    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog", { name: "Edit task schedule" })).toBeNull();
    expect(trigger).toHaveFocus();

    await user.click(trigger);
    await user.click(screen.getByRole("button", { name: "Remove schedule" }));
    await user.click(screen.getByRole("button", { name: "Confirm remove" }));
    expect(onDelete).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("dialog", { name: "Edit task schedule" })).toBeNull();
    expect(trigger).toHaveFocus();
  });

  it("submits the native form with Enter", async () => {
    const onSave = vi.fn(async (_payload: UpsertTaskSchedulePayload) => {});
    const user = userEvent.setup();
    render(
      <TaskScheduleControl
        schedule={recurringSchedule()}
        occurrences={[]}
        operation={null}
        onSave={onSave}
        onDelete={vi.fn(async () => {})}
      />,
    );
    await user.click(screen.getByTitle(/Edit schedule/));
    const cron = screen.getByLabelText("Cron expression");
    await user.click(cron);
    await user.keyboard("{Enter}");
    expect(onSave).toHaveBeenCalledTimes(1);
  });
});
