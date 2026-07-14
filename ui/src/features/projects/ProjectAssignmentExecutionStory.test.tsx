import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { ProjectAssignmentRecord, ProjectWorkRoleRecord } from "../../types/project";
import {
  ProjectAssignmentExecutionStory,
  type ProjectAssignmentExecutionStoryProps,
  projectAssignmentExecutionMilestones,
} from "./ProjectAssignmentExecutionStory";

function assignment(overrides: Partial<ProjectAssignmentRecord> = {}): ProjectAssignmentRecord {
  return {
    id: "assign_1",
    project_id: "proj_1",
    work_item_id: "work_1",
    role_id: "developer",
    driver_kind: "hecate_task",
    status: "queued",
    execution_ref: {
      kind: "none",
      status: "queued",
    },
    created_at: "2026-07-10T10:00:00Z",
    updated_at: "2026-07-11T11:00:00Z",
    ...overrides,
  };
}

const role: ProjectWorkRoleRecord = {
  id: "developer",
  project_id: "proj_1",
  name: "Developer",
  default_driver_kind: "hecate_task",
  built_in: false,
  created_at: "2026-07-01T00:00:00Z",
  updated_at: "2026-07-01T00:00:00Z",
};

function renderStory(
  record: ProjectAssignmentRecord,
  overrides: Partial<ProjectAssignmentExecutionStoryProps> = {},
) {
  const handlers = {
    onCreateHandoff: vi.fn(),
    onCreateReviewArtifact: vi.fn(),
    onCreateReviewHandoff: vi.fn(),
    onCompleteWork: vi.fn(),
    onDelete: vi.fn(),
    onEdit: vi.fn(),
    onOpenChat: vi.fn(),
    onOpenTask: vi.fn(),
    onReviewLaunch: vi.fn(),
    onResumeWork: vi.fn(),
    onStartWork: vi.fn(),
  };
  const result = render(
    <ProjectAssignmentExecutionStory
      assignment={record}
      chatModel="gpt-5"
      contextControl={<button type="button">Inspect context</button>}
      error=""
      project={null}
      role={role}
      starting={false}
      {...handlers}
      {...overrides}
    />,
  );
  return { ...result, handlers };
}

describe("ProjectAssignmentExecutionStory", () => {
  it("guides a queued Hecate assignment through one primary launch action", async () => {
    const { container, handlers } = renderStory(assignment());

    expect(screen.getByText("Hecate Task")).toBeTruthy();
    expect(
      screen.getByText("Review launch context before starting this Hecate Task."),
    ).toBeTruthy();
    expect(container.querySelectorAll(".btn-primary")).toHaveLength(1);

    const milestones = screen.getByRole("list", { name: "Execution milestones" });
    expect(within(milestones).getByText("Assigned")).toBeTruthy();
    expect(within(milestones).getByText("Current")).toBeTruthy();
    expect(within(milestones).queryByText("Started")).toBeNull();

    await userEvent.click(screen.getByRole("button", { name: "Review & start" }));
    expect(handlers.onReviewLaunch).toHaveBeenCalledTimes(1);
  });

  it("makes the linked task the primary action while execution is running", async () => {
    const record = assignment({
      status: "running",
      execution_ref: {
        kind: "task_run",
        task_id: "task_123",
        run_id: "run_123",
        status: "running",
      },
      execution: {
        status: "running",
        started_at: "2026-07-10T10:05:00Z",
        provider: "openai",
        model: "gpt-5",
        step_count: 3,
        artifact_count: 1,
      },
    });
    const { container, handlers } = renderStory(record);

    expect(container.querySelectorAll(".btn-primary")).toHaveLength(1);
    await userEvent.click(screen.getByRole("button", { name: "Open task" }));
    expect(handlers.onOpenTask).toHaveBeenCalledWith("task_123", "run_123");

    const details = screen.getByText("Execution details").closest("details");
    expect(details).not.toHaveAttribute("open");
    await userEvent.click(screen.getByText("Execution details"));
    expect(details).toHaveAttribute("open");
    expect(screen.getByText("3 steps")).toBeTruthy();
    expect(screen.getByText("1 artifact")).toBeTruthy();
    expect(screen.getByRole("region", { name: "Execution evidence" })).toBeTruthy();

    const milestones = screen.getByRole("list", { name: "Execution milestones" });
    expect(within(milestones).getByText("Started")).toBeTruthy();
    expect(within(milestones).getByText("running")).toBeTruthy();
  });

  it("keeps approvals visible and routes the operator to the linked task", async () => {
    const { handlers } = renderStory(
      assignment({
        status: "awaiting_approval",
        execution_ref: {
          kind: "task_run",
          task_id: "task_approval",
          run_id: "run_approval",
          status: "awaiting_approval",
          pending_approval_count: 1,
        },
      }),
    );

    expect(screen.getByText("1 approval needs operator review.")).toBeTruthy();
    expect(screen.queryByText(/approval waiting in the linked task/i)).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Review in task" }));
    expect(handlers.onOpenTask).toHaveBeenCalledWith("task_approval", "run_approval");
  });

  it("uses neutral review language when no pending approval is recorded", async () => {
    const record = assignment({
      status: "awaiting_approval",
      execution_ref: {
        kind: "task_run",
        task_id: "task_review",
        status: "awaiting_approval",
      },
    });
    const { handlers } = renderStory(record);

    expect(screen.getAllByText("review", { exact: true })).toHaveLength(2);
    expect(screen.getByText("Assignment needs operator review.")).toBeTruthy();
    expect(screen.queryByText(/operator approval/i)).toBeNull();
    expect(screen.queryByText("awaiting approval")).toBeNull();
    expect(screen.getByText("Assignment is waiting for review.")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Review task" }));
    expect(handlers.onOpenTask).toHaveBeenCalledWith("task_review", "");
  });

  it("shows failure evidence without fabricating a finish milestone", () => {
    const record = assignment({
      status: "failed",
      execution_ref: {
        kind: "task_run",
        task_id: "task_failed",
        status: "failed",
        missing: true,
      },
      execution: {
        status: "failed",
        last_error: "The provider stopped before producing an artifact.",
      },
    });
    renderStory(record);

    expect(screen.getByRole("button", { name: "Inspect task" })).toBeTruthy();
    expect(screen.getByText("The linked runtime record is missing or unavailable.")).toBeTruthy();
    expect(screen.getByRole("alert")).toHaveTextContent(
      "The provider stopped before producing an artifact.",
    );

    const milestones = screen.getByRole("list", { name: "Execution milestones" });
    expect(within(milestones).getByText("failed")).toBeTruthy();
    expect(within(milestones).queryByText("Execution finished with a failure.")).toBeNull();
    expect(within(milestones).getByText(/no finish time was recorded/i)).toBeTruthy();
  });

  it("prefers recording a reviewer outcome after completion", async () => {
    const { container, handlers } = renderStory(
      assignment({
        status: "completed",
        completed_at: "2026-07-10T10:30:00Z",
        execution_ref: {
          kind: "task_run",
          task_id: "task_done",
          run_id: "run_done",
          status: "completed",
        },
      }),
    );

    expect(container.querySelectorAll(".btn-primary")).toHaveLength(1);
    await userEvent.click(
      screen.getByRole("button", { name: "Record review for assignment assign_1" }),
    );
    expect(handlers.onCreateReviewArtifact).toHaveBeenCalledTimes(1);
    expect(screen.getAllByRole("button", { name: /Record review/ })).toHaveLength(1);

    const finished = screen
      .getByRole("list", { name: "Execution milestones" })
      .querySelector('time[datetime="2026-07-10T10:30:00Z"]');
    expect(finished).toBeTruthy();
    expect(screen.getByText("Execution finished successfully.")).toBeTruthy();
  });

  it("promotes a review request only when execution evidence is linked", async () => {
    const linked = assignment({
      status: "completed",
      execution_ref: {
        kind: "task_run",
        task_id: "task_done",
        status: "completed",
      },
    });
    const { handlers } = renderStory(linked, { onCreateReviewArtifact: undefined });

    await userEvent.click(
      screen.getByRole("button", { name: "Request review for assignment assign_1" }),
    );
    expect(handlers.onCreateReviewHandoff).toHaveBeenCalledTimes(1);
  });

  it("does not request review for a legacy completion without execution evidence", () => {
    const { container } = renderStory(
      assignment({
        status: "completed",
        execution_ref: { kind: "none", status: "completed" },
      }),
      { onCreateReviewArtifact: undefined },
    );

    expect(container.querySelectorAll(".btn-primary")).toHaveLength(0);
    expect(screen.queryByRole("button", { name: /Request review/ })).toBeNull();
    expect(
      screen.getByText("No linked task or chat is available for this assignment."),
    ).toBeTruthy();
  });

  it("defers completed-assignment actions when closeout is authoritative", () => {
    const { container } = renderStory(
      assignment({
        status: "completed",
        execution_ref: {
          kind: "task_run",
          task_id: "task_done",
          status: "completed",
        },
      }),
      { promoteCompletionAction: false },
    );

    expect(container.querySelectorAll(".btn-primary")).toHaveLength(0);
    expect(
      screen.getByRole("button", { name: "Record review for assignment assign_1" }),
    ).toBeTruthy();
    expect(
      screen.getByRole("button", { name: "Request review for assignment assign_1" }),
    ).toBeTruthy();
  });

  it("distinguishes a related chat draft from linked execution", async () => {
    const { container } = renderStory(
      assignment({
        status: "running",
        execution_ref: { kind: "none", status: "running" },
      }),
    );

    expect(container.querySelectorAll(".btn-primary")).toHaveLength(0);
    expect(screen.queryByRole("button", { name: "Open chat" })).toBeNull();
    await userEvent.click(screen.getByText("Execution details"));
    expect(screen.getByRole("button", { name: "Start related chat" })).toBeTruthy();
    expect(
      screen.getByText("No linked task or chat is available for this assignment."),
    ).toBeTruthy();
  });

  it("wraps long server-provided role and milestone content", () => {
    const running = assignment({
      status: "running",
      execution_ref: { kind: "task_run", task_id: "task_long", status: "running" },
    });
    const longRoleName = `Specialist-${"unbroken".repeat(40)}`;
    renderStory(running, { role: { ...role, name: longRoleName } });

    expect(screen.getByText(longRoleName)).toHaveStyle({ overflowWrap: "anywhere" });
    expect(screen.getByText("Execution is in progress.")).toHaveStyle({
      overflowWrap: "anywhere",
    });
  });

  it("keeps External Agent preparation supervised and plain-language", async () => {
    const { handlers } = renderStory(
      assignment({ driver_kind: "external_agent", execution_ref: { kind: "none" } }),
    );

    expect(screen.getByText("External Agent")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Open chat" })).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Review & prepare chat" }));
    expect(handlers.onReviewLaunch).toHaveBeenCalledTimes(1);
  });

  it("shows a prepared External Agent chat before any response is recorded", async () => {
    const { container, handlers } = renderStory(
      assignment({
        driver_kind: "external_agent",
        status: "running",
        started_at: "2026-07-10T10:05:00Z",
        execution_ref: {
          kind: "chat_session",
          chat_session_id: "chat_external",
          context_snapshot_id: "ctx_external",
          status: "running",
        },
      }),
    );

    expect(screen.getByText("Chat ready")).toBeTruthy();
    expect(screen.getByText("Chat is prepared; no agent response is recorded yet.")).toBeTruthy();
    expect(screen.queryByText("External Agent is running.")).toBeNull();
    expect(container.querySelectorAll(".btn-primary")).toHaveLength(1);

    const milestones = screen.getByRole("list", { name: "Execution milestones" });
    expect(within(milestones).getByText("Chat prepared")).toBeTruthy();
    expect(within(milestones).queryByText("Started")).toBeNull();
    expect(within(milestones).queryByText("Agent working")).toBeNull();

    await userEvent.click(screen.getByRole("button", { name: "Continue in chat" }));
    expect(handlers.onOpenChat).toHaveBeenCalledTimes(1);
  });

  it("keeps an untimed prepared External Agent chat visible as the current milestone", () => {
    renderStory(
      assignment({
        driver_kind: "external_agent",
        status: "running",
        execution_ref: {
          kind: "chat_session",
          chat_session_id: "chat_external",
          status: "running",
        },
      }),
    );

    const milestones = screen.getByRole("list", { name: "Execution milestones" });
    const preparedMilestone = within(milestones).getByText("Chat prepared").closest("li");
    expect(preparedMilestone).not.toBeNull();
    expect(within(preparedMilestone as HTMLElement).getByText("Current")).toBeTruthy();
    expect(
      within(preparedMilestone as HTMLElement).getByText(
        "The supervised chat is ready for the first prompt.",
      ),
    ).toBeTruthy();
    expect(within(preparedMilestone as HTMLElement).queryByRole("time")).toBeNull();
  });

  it("does not offer a missing linked External Agent chat as ready or openable", () => {
    const { handlers } = renderStory(
      assignment({
        driver_kind: "external_agent",
        status: "running",
        execution_ref: {
          kind: "chat_session",
          chat_session_id: "chat_missing",
          status: "running",
          missing: true,
        },
      }),
    );

    expect(
      screen.getByText("No linked External Agent chat is available for this assignment."),
    ).toBeTruthy();
    expect(screen.getByText("The linked runtime record is missing or unavailable.")).toBeTruthy();
    expect(screen.queryByText("Chat ready")).toBeNull();
    expect(screen.queryByRole("button", { name: "Continue in chat" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Open chat" })).toBeNull();
    expect(handlers.onOpenChat).not.toHaveBeenCalled();
  });

  it("does not offer preparation again for a queued assignment with a missing chat", () => {
    renderStory(
      assignment({
        driver_kind: "external_agent",
        status: "queued",
        execution_ref: {
          kind: "chat_session",
          chat_session_id: "chat_missing",
          status: "queued",
          missing: true,
        },
      }),
    );

    expect(
      screen.getByText("No linked External Agent chat is available for this assignment."),
    ).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Review & prepare chat" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Continue in chat" })).toBeNull();
  });

  it("shows message-backed External Agent work as active in its linked chat", async () => {
    const { handlers } = renderStory(
      assignment({
        driver_kind: "external_agent",
        status: "running",
        started_at: "2026-07-10T10:05:00Z",
        execution_ref: {
          kind: "chat_session",
          chat_session_id: "chat_external",
          message_id: "msg_external",
          context_snapshot_id: "ctx_external",
          status: "running",
        },
      }),
    );

    expect(screen.getByText("External Agent work is continuing in the linked chat.")).toBeTruthy();
    const milestones = screen.getByRole("list", { name: "Execution milestones" });
    expect(within(milestones).getByText("Chat prepared")).toBeTruthy();
    expect(within(milestones).getByText("Agent working")).toBeTruthy();
    expect(
      within(milestones).getByText("The External Agent is working in the linked chat."),
    ).toBeTruthy();

    await userEvent.click(screen.getByRole("button", { name: "Open chat" }));
    expect(handlers.onOpenChat).toHaveBeenCalledTimes(1);
  });

  it.each([
    {
      status: "awaiting_approval",
      pendingApprovalCount: 1,
      action: "Review in chat",
      summary: "1 approval needs operator review in the linked chat.",
    },
    {
      status: "failed",
      pendingApprovalCount: 0,
      action: "Inspect chat",
      summary: "External Agent work failed. Inspect the linked chat.",
    },
    {
      status: "cancelled",
      pendingApprovalCount: 0,
      action: "Inspect chat",
      summary: "External Agent work was cancelled. Inspect the linked chat.",
    },
  ])("routes External Agent $status attention to the linked chat", async (state) => {
    const { handlers } = renderStory(
      assignment({
        driver_kind: "external_agent",
        status: state.status,
        started_at: "2026-07-10T10:05:00Z",
        execution_ref: {
          kind: "chat_session",
          chat_session_id: "chat_external",
          message_id: "msg_external",
          status: state.status,
          pending_approval_count: state.pendingApprovalCount,
        },
      }),
    );

    expect(screen.getByText(state.summary)).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: state.action }));
    expect(handlers.onOpenChat).toHaveBeenCalledTimes(1);
  });

  it("guides queued Human work without launch preflight or runtime requirements", async () => {
    const { container, handlers } = renderStory(
      assignment({ driver_kind: "manual", execution_ref: { kind: "none", status: "queued" } }),
    );

    expect(screen.getByText("Human")).toBeTruthy();
    expect(screen.getByText("Ready")).toBeTruthy();
    expect(screen.getAllByText("Ready for a person to begin.").length).toBeGreaterThan(0);
    expect(screen.getByRole("list", { name: "Assignment progress" })).toBeTruthy();
    expect(container.querySelectorAll(".btn-primary")).toHaveLength(1);
    expect(screen.queryByText("Launch readiness")).toBeNull();
    expect(screen.queryByText(/linked task or chat/i)).toBeNull();

    await userEvent.click(screen.getByRole("button", { name: "Start work" }));

    expect(handlers.onStartWork).toHaveBeenCalledTimes(1);
    expect(handlers.onReviewLaunch).not.toHaveBeenCalled();
    expect(
      screen.getByRole("button", { name: "Create handoff from assignment assign_1" }),
    ).toBeTruthy();
    expect(
      screen.getByRole("button", { name: "Request review for assignment assign_1" }),
    ).toBeTruthy();
  });

  it("makes completion the single primary action for active Human work", async () => {
    const { container, handlers } = renderStory(
      assignment({
        driver_kind: "manual",
        status: "running",
        started_at: "2026-07-10T10:15:00Z",
        execution_ref: { kind: "none", status: "running" },
      }),
    );

    expect(screen.getAllByText("In progress").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Human work is in progress.").length).toBeGreaterThan(0);
    expect(screen.getByRole("status")).toHaveAttribute("aria-live", "polite");
    expect(container.querySelectorAll(".btn-primary")).toHaveLength(1);

    await userEvent.click(screen.getByRole("button", { name: "Mark complete" }));

    expect(handlers.onCompleteWork).toHaveBeenCalledTimes(1);
    expect(handlers.onStartWork).not.toHaveBeenCalled();
  });

  it("recovers an interrupted Human start instead of offering completion", async () => {
    const { handlers } = renderStory(
      assignment({
        driver_kind: "manual",
        status: "running",
        execution_ref: { kind: "none", status: "running" },
      }),
    );

    expect(screen.getAllByText("Starting").length).toBeGreaterThan(0);
    expect(
      screen.getByText("Starting was interrupted before work began. Finish starting to continue."),
    ).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Mark complete" })).toBeNull();

    await userEvent.click(screen.getByRole("button", { name: "Finish starting" }));
    expect(handlers.onStartWork).toHaveBeenCalledTimes(1);
  });

  it("does not offer recovery for a Human start prepared elsewhere", async () => {
    const { container } = renderStory(
      assignment({
        driver_kind: "manual",
        status: "running",
        execution_ref: {
          kind: "context_snapshot",
          context_snapshot_id: "ctx_prepared_elsewhere",
          status: "running",
        },
      }),
    );

    const blockedLabels = screen.getAllByText("Start blocked");
    expect(blockedLabels.length).toBeGreaterThan(0);
    expect(blockedLabels.find((item) => item.classList.contains("badge"))).toHaveClass(
      "badge-amber",
    );
    expect(
      screen.getByText(
        "This start was prepared elsewhere. Resolve it with the owning operator or system.",
      ),
    ).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Finish starting" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Mark complete" })).toBeNull();
    expect(container.querySelectorAll(".btn-primary")).toHaveLength(0);

    await userEvent.click(screen.getByText("Assignment details"));
    expect(screen.getByRole("button", { name: "Edit assignment assign_1" })).toBeDisabled();
  });

  it("presents completed Human work as an evidence and follow-through state", () => {
    renderStory(
      assignment({
        driver_kind: "manual",
        status: "completed",
        started_at: "2026-07-10T10:15:00Z",
        completed_at: "2026-07-10T10:45:00Z",
        execution_ref: { kind: "none", status: "completed" },
      }),
    );

    expect(screen.getByText("Done")).toBeTruthy();
    expect(
      screen.getByText("Human work is complete. Add evidence or choose the follow-through."),
    ).toBeTruthy();
    expect(screen.getByText("Work marked complete.")).toBeTruthy();
    expect(
      screen.getByRole("button", { name: "Record review for assignment assign_1" }),
    ).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Edit assignment assign_1" })).toBeNull();
  });

  it("locks mutations while a Human start is pending but leaves inspection available", async () => {
    renderStory(
      assignment({
        driver_kind: "manual",
        status: "queued",
        execution_ref: { kind: "none", status: "queued" },
      }),
      { starting: true },
    );

    expect(screen.getByRole("button", { name: "Starting…" })).toBeDisabled();
    await userEvent.click(screen.getByText("Assignment details"));
    expect(screen.getByRole("button", { name: "Edit assignment assign_1" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Delete assignment assign_1" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Inspect context" })).not.toBeDisabled();
    expect(
      screen.getByRole("button", { name: "Create handoff from assignment assign_1" }),
    ).toBeDisabled();
  });

  it("promotes a review request for completed Human work without runtime evidence", async () => {
    const { container, handlers } = renderStory(
      assignment({
        driver_kind: "manual",
        status: "completed",
        completed_at: "2026-07-10T10:45:00Z",
        execution_ref: { kind: "none", status: "completed" },
      }),
      { onCreateReviewArtifact: undefined },
    );

    expect(container.querySelectorAll(".btn-primary")).toHaveLength(1);
    await userEvent.click(
      screen.getByRole("button", { name: "Request review for assignment assign_1" }),
    );
    expect(handlers.onCreateReviewHandoff).toHaveBeenCalledTimes(1);
  });

  it("lets the operator resume Human work while keeping review recording available", async () => {
    const { handlers } = renderStory(
      assignment({
        driver_kind: "manual",
        status: "awaiting_approval",
        execution_ref: { kind: "none", status: "awaiting_approval" },
      }),
    );

    expect(screen.getAllByText("Needs review").length).toBeGreaterThan(0);
    expect(screen.getByText("This work is waiting for review.")).toBeTruthy();
    expect(
      screen.getByRole("button", { name: "Record review for assignment assign_1" }),
    ).toBeTruthy();
    await userEvent.click(
      screen.getByRole("button", { name: "Resume work on assignment assign_1" }),
    );
    expect(handlers.onResumeWork).toHaveBeenCalledTimes(1);
  });

  it.each([
    [
      "failed",
      "failed",
      "This work failed and blocks closeout. Review the evidence before deciding whether to replace this record.",
    ],
    [
      "cancelled",
      "cancelled",
      "This work was cancelled and blocks closeout. Review the record before choosing the next step.",
    ],
  ])("explains the closeout impact of Human %s work", (status, label, summary) => {
    const { container } = renderStory(
      assignment({
        driver_kind: "manual",
        status,
        execution_ref: { kind: "none", status },
      }),
    );

    expect(screen.getAllByText(label).length).toBeGreaterThan(0);
    expect(screen.getByText(summary)).toBeTruthy();
    expect(screen.queryByText(/linked task or chat/i)).toBeNull();
    expect(container.querySelectorAll(".btn-primary")).toHaveLength(0);
  });

  it("never treats updated_at as execution history", () => {
    const milestones = projectAssignmentExecutionMilestones(
      assignment({
        status: "completed",
        execution_ref: { kind: "none", status: "completed" },
        updated_at: "2026-07-12T12:00:00Z",
      }),
    );

    expect(milestones.map((milestone) => milestone.key)).toEqual(["assigned", "current"]);
    expect(milestones[1]).toMatchObject({
      detail: "Execution is currently marked complete; no finish time was recorded.",
    });
    expect(milestones[1].at).toBeUndefined();
  });
});
