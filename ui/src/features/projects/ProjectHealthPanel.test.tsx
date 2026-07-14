import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type {
  ProjectAction,
  ProjectHealthAttention,
  ProjectHealthSummary,
  ProjectMemoryCandidateRecord,
} from "../../types/project";
import { ProjectHealthPanel } from "./ProjectHealthPanel";

function memoryCandidate(overrides: Partial<ProjectMemoryCandidateRecord> = {}) {
  return {
    id: "memcand_1",
    project_id: "proj_1",
    title: "Generated memory",
    body: "Keep this if useful.",
    suggested_trust_label: "generated_summary",
    suggested_source_kind: "task_output",
    status: "pending",
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
    ...overrides,
  } satisfies ProjectMemoryCandidateRecord;
}

function healthSummary(overrides: Partial<ProjectHealthSummary> = {}) {
  return {
    attention_count: 5,
    available_attention_count: 5,
    omitted_attention_count: 0,
    attention_limit: 5,
    missing_defaults: false,
    missing_project_root: false,
    enabled_memory_count: 0,
    saved_memory_count: 0,
    enabled_context_source_count: 0,
    pending_memory_candidate_count: 0,
    promoted_memory_candidate_count: 0,
    rejected_memory_candidate_count: 0,
    pending_handoff_count: 0,
    accepted_handoff_count: 0,
    superseded_handoff_count: 0,
    dismissed_handoff_count: 0,
    review_follow_up_count: 0,
    blocked_review_count: 0,
    changes_requested_review_count: 0,
    stale_or_unknown_assignment_count: 0,
    ...overrides,
  } satisfies ProjectHealthSummary;
}

function projectAction(
  type: ProjectAction["type"],
  overrides: Partial<ProjectAction> = {},
): ProjectAction {
  return { type, project_id: "proj_1", ...overrides };
}

function renderPanel(overrides: Partial<Parameters<typeof ProjectHealthPanel>[0]> = {}) {
  const candidate = memoryCandidate();
  const attentionItems: ProjectHealthAttention[] = [
    {
      id: "proj_1:defaults",
      project_id: "proj_1",
      title: "Provider/model defaults missing",
      detail: "Set defaults before starting assignments.",
      status: "awaiting_approval",
      action: projectAction("open_project_settings"),
    },
    {
      id: "assign_1:blocked",
      project_id: "proj_1",
      title: "Blocked assignment",
      detail: "One assignment needs review.",
      status: "failed",
      bucket: "blocked",
      action: projectAction("open_activity_bucket", { activity_bucket: "blocked" }),
      action_label: "View blocked",
    },
    {
      id: "work_1:item",
      project_id: "proj_1",
      title: "Work item needs attention",
      detail: "Open the work item.",
      status: "running",
      action: projectAction("open_work_item", { work_item_id: "work_1" }),
      work_item_id: "work_1",
      action_label: "Open review",
    },
    {
      id: "task_1:item",
      project_id: "proj_1",
      title: "Task needs attention",
      detail: "Open the linked task.",
      status: "running",
      action: projectAction("open_task", { run_id: "run_1", task_id: "task_1" }),
      task_id: "task_1",
      run_id: "run_1",
    },
    {
      id: "memcand_1:memory-candidate",
      project_id: "proj_1",
      title: "Memory candidate pending review",
      detail: "Review generated memory.",
      status: "awaiting_approval",
      action: projectAction("review_memory_candidate", { candidate_id: candidate.id }),
      candidate_id: candidate.id,
    },
  ];
  const handlers = {
    onAttentionBucket: vi.fn(),
    onAttentionDefaults: vi.fn(),
    onAttentionError: vi.fn(),
    onAttentionMemory: vi.fn(),
    onAttentionPresets: vi.fn(),
    onAttentionReviewCandidate: vi.fn(),
    onAttentionRoles: vi.fn(),
    onAttentionSkills: vi.fn(),
    onAttentionTask: vi.fn(),
    onAttentionWorkItem: vi.fn(),
  };
  const props: Parameters<typeof ProjectHealthPanel>[0] = {
    attentionItems,
    memoryCandidates: [candidate],
    ...handlers,
    ...overrides,
  };
  const result = render(<ProjectHealthPanel {...props} />);
  return { candidate, handlers, props, ...result };
}

async function openMenu() {
  await userEvent.click(screen.getByRole("button", { name: "Project attention: 5" }));
  return screen.getByRole("dialog", { name: "Needs Attention" });
}

describe("ProjectHealthPanel", () => {
  it("renders attention items and delegates direct row activation", async () => {
    const { handlers } = renderPanel();
    const trigger = screen.getByRole("button", { name: "Project attention: 5" });

    await openMenu();
    const dialog = screen.getByRole("dialog", { name: "Needs Attention" });
    expect(trigger).toHaveAttribute("aria-haspopup", "dialog");
    expect(trigger).toHaveAttribute("aria-controls", dialog.id);
    expect(
      screen.getByRole("button", {
        name: "Open attention item Provider/model defaults missing",
      }),
    ).toHaveFocus();
    await userEvent.click(
      screen.getByRole("button", { name: "Open attention item Provider/model defaults missing" }),
    );

    expect(handlers.onAttentionDefaults).toHaveBeenCalledTimes(1);
  });

  it("delegates compact action buttons without also activating the primary action", async () => {
    const { candidate, handlers } = renderPanel();

    await openMenu();
    await userEvent.click(screen.getByRole("button", { name: "View blocked: Blocked assignment" }));
    expect(handlers.onAttentionBucket).toHaveBeenCalledWith("blocked");
    expect(handlers.onAttentionDefaults).not.toHaveBeenCalled();

    await openMenu();
    expect(screen.getByText("Open review")).toBeTruthy();
    await userEvent.click(
      screen.getByRole("button", { name: "Open review: Work item needs attention" }),
    );
    expect(handlers.onAttentionWorkItem).toHaveBeenCalledWith("work_1");

    await openMenu();
    await userEvent.click(
      screen.getByRole("button", { name: "Open attention task: Task needs attention" }),
    );
    expect(handlers.onAttentionTask).toHaveBeenCalledWith("task_1", "run_1");

    await openMenu();
    await userEvent.click(
      screen.getByRole("button", {
        name: "Review memory candidate: Memory candidate pending review",
      }),
    );
    expect(handlers.onAttentionReviewCandidate).toHaveBeenCalledWith(candidate);
  });

  it("keeps compact actions distinct when activated from the keyboard", async () => {
    const user = userEvent.setup();
    const { handlers } = renderPanel();

    await openMenu();
    screen.getByRole("button", { name: "View blocked: Blocked assignment" }).focus();
    await user.keyboard("{Enter}");

    expect(handlers.onAttentionBucket).toHaveBeenCalledWith("blocked");
    expect(handlers.onAttentionDefaults).not.toHaveBeenCalled();

    await openMenu();
    screen.getByRole("button", { name: "Open attention task: Task needs attention" }).focus();
    await user.keyboard(" ");

    expect(handlers.onAttentionTask).toHaveBeenCalledWith("task_1", "run_1");
    expect(handlers.onAttentionWorkItem).not.toHaveBeenCalled();
  });

  it("preserves exact server focus targets for the canonical action dispatcher", async () => {
    const onAttentionRoute = vi.fn();
    renderPanel({
      attentionItems: [
        {
          id: "handoff_1:pending",
          project_id: "proj_1",
          title: "Pending review handoff",
          detail: "Open the exact handoff that needs a decision.",
          status: "awaiting_approval",
          action: projectAction("open_work_item", {
            activity_bucket: "recent",
            artifact_id: "artifact_1",
            assignment_id: "assign_1",
            handoff_id: "handoff_1",
            work_item_id: "work_1",
          }),
        },
      ],
      onAttentionRoute,
    });

    await userEvent.click(screen.getByRole("button", { name: "Project attention: 1" }));
    await userEvent.click(
      screen.getByRole("button", { name: "Open attention item Pending review handoff" }),
    );

    expect(onAttentionRoute).toHaveBeenCalledWith({
      kind: "open_work_item",
      bucket: "recent",
      artifactID: "artifact_1",
      assignmentID: "assign_1",
      handoffID: "handoff_1",
      workItemID: "work_1",
    });
  });

  it("closes on Escape and restores focus to the attention trigger", async () => {
    const user = userEvent.setup();
    renderPanel();
    const trigger = screen.getByRole("button", { name: "Project attention: 5" });

    await openMenu();
    await user.keyboard("{Escape}");

    expect(screen.queryByRole("dialog", { name: "Needs Attention" })).toBeNull();
    expect(trigger).toHaveFocus();
    expect(trigger).toHaveAttribute("aria-expanded", "false");
  });

  it("returns focus to the trigger when refreshed attention removes the focused row", async () => {
    const result = renderPanel();
    expect(screen.getByRole("status")).toHaveTextContent(/^$/);
    await openMenu();
    screen
      .getByRole("button", { name: "Open attention item Provider/model defaults missing" })
      .focus();

    result.rerender(
      <ProjectHealthPanel
        {...result.props}
        attentionItems={result.props.attentionItems.filter((item) => item.id !== "proj_1:defaults")}
      />,
    );

    expect(screen.getByRole("button", { name: "Project attention: 4" })).toHaveFocus();
    expect(screen.queryByRole("dialog", { name: "Needs Attention" })).toBeNull();
    expect(screen.getByRole("button", { name: "Project attention: 4" })).toHaveAttribute(
      "aria-expanded",
      "false",
    );
    expect(
      screen.getByText("Project attention changed. Focus returned to the attention button."),
    ).toBeTruthy();
  });

  it("returns focus when a same-label attention item changes its canonical target", async () => {
    const result = renderPanel();
    await openMenu();
    screen.getByRole("button", { name: "Open attention item Work item needs attention" }).focus();
    const attentionItems = result.props.attentionItems.map((item) =>
      item.id === "work_1:item"
        ? {
            ...item,
            action: projectAction("open_work_item", { work_item_id: "work_2" }),
            work_item_id: "work_2",
          }
        : item,
    );

    result.rerender(<ProjectHealthPanel {...result.props} attentionItems={attentionItems} />);

    expect(screen.getByRole("button", { name: "Project attention: 5" })).toHaveFocus();
    expect(screen.queryByRole("dialog", { name: "Needs Attention" })).toBeNull();
    expect(screen.getByRole("status")).toHaveTextContent(
      "Project attention changed. Focus returned to the attention button.",
    );
  });

  it("re-announces repeated identical focus recovery events", async () => {
    const result = renderPanel();
    const withoutDefaults = result.props.attentionItems.filter(
      (item) => item.id !== "proj_1:defaults",
    );
    await openMenu();
    screen
      .getByRole("button", { name: "Open attention item Provider/model defaults missing" })
      .focus();

    result.rerender(<ProjectHealthPanel {...result.props} attentionItems={withoutDefaults} />);
    const firstAnnouncement = screen.getByRole("status").firstElementChild;
    expect(firstAnnouncement).toHaveTextContent(
      "Project attention changed. Focus returned to the attention button.",
    );

    result.rerender(<ProjectHealthPanel {...result.props} />);
    await openMenu();
    screen
      .getByRole("button", { name: "Open attention item Provider/model defaults missing" })
      .focus();
    result.rerender(<ProjectHealthPanel {...result.props} attentionItems={withoutDefaults} />);

    const secondAnnouncement = screen.getByRole("status").firstElementChild;
    expect(secondAnnouncement).toHaveTextContent(
      "Project attention changed. Focus returned to the attention button.",
    );
    expect(secondAnnouncement).not.toBe(firstAnnouncement);
  });

  it("restores focus after non-focusable outside dismissal without stealing focus from a control", async () => {
    const user = userEvent.setup();
    renderPanel();
    const trigger = screen.getByRole("button", { name: "Project attention: 5" });
    const outsideSurface = document.createElement("div");
    const outsideButton = document.createElement("button");
    outsideButton.type = "button";
    outsideButton.textContent = "Outside action";
    document.body.append(outsideSurface, outsideButton);

    try {
      await openMenu();
      fireEvent.mouseDown(outsideSurface);
      await waitFor(() => expect(trigger).toHaveFocus());
      expect(screen.queryByRole("dialog", { name: "Needs Attention" })).toBeNull();

      await openMenu();
      await user.click(outsideButton);
      expect(outsideButton).toHaveFocus();
      expect(screen.queryByRole("dialog", { name: "Needs Attention" })).toBeNull();
    } finally {
      outsideSurface.remove();
      outsideButton.remove();
    }
  });

  it("closes when keyboard focus leaves the nonmodal dialog", async () => {
    const user = userEvent.setup();
    renderPanel();
    const trigger = screen.getByRole("button", { name: "Project attention: 5" });
    const outsideButton = document.createElement("button");
    outsideButton.type = "button";
    outsideButton.textContent = "Outside keyboard action";
    document.body.append(outsideButton);

    try {
      const dialog = await openMenu();
      const dialogButtons = dialog.querySelectorAll<HTMLButtonElement>("button");
      dialogButtons.item(dialogButtons.length - 1).focus();

      await user.tab();

      expect(outsideButton).toHaveFocus();
      expect(screen.queryByRole("dialog", { name: "Needs Attention" })).toBeNull();
      expect(trigger).toHaveAttribute("aria-expanded", "false");
      await user.keyboard("{Escape}");
      expect(outsideButton).toHaveFocus();

      const reopenedDialog = await openMenu();
      expect(
        reopenedDialog.querySelector<HTMLElement>("[data-project-attention-primary]"),
      ).toHaveFocus();
      await user.tab({ shift: true });
      expect(trigger).toHaveFocus();
      expect(screen.getByRole("dialog", { name: "Needs Attention" })).toBeTruthy();
      await user.keyboard("{Escape}");
      expect(screen.queryByRole("dialog", { name: "Needs Attention" })).toBeNull();
      expect(trigger).toHaveFocus();
    } finally {
      outsideButton.remove();
    }
  });

  it("falls back to the memory view when candidate details are missing", async () => {
    const { handlers } = renderPanel({ memoryCandidates: [] });

    await openMenu();
    await userEvent.click(
      screen.getByRole("button", { name: "Open attention item Memory candidate pending review" }),
    );

    expect(handlers.onAttentionMemory).toHaveBeenCalledTimes(1);
  });

  it("keeps the focused memory action available when candidate details disappear", async () => {
    const result = renderPanel();
    await openMenu();
    screen
      .getByRole("button", {
        name: "Review memory candidate: Memory candidate pending review",
      })
      .focus();

    result.rerender(<ProjectHealthPanel {...result.props} memoryCandidates={[]} />);

    const fallback = screen.getByRole("button", {
      name: "Open memory review: Memory candidate pending review",
    });
    expect(fallback).toHaveFocus();
    await userEvent.click(fallback);
    expect(result.handlers.onAttentionMemory).toHaveBeenCalledTimes(1);
  });

  it("reports stale project attention targets without navigating", async () => {
    const { handlers } = renderPanel({
      attentionItems: [
        {
          id: "proj_other:defaults",
          project_id: "proj_other",
          title: "Provider/model defaults missing",
          detail: "Set defaults before starting assignments.",
          status: "awaiting_approval",
          action: { type: "open_project_settings", project_id: "proj_other" },
        },
      ],
      selectedProjectID: "proj_1",
    });

    await userEvent.click(screen.getByRole("button", { name: "Project attention: 1" }));
    await userEvent.click(
      screen.getByRole("button", { name: "Open attention item Provider/model defaults missing" }),
    );

    expect(handlers.onAttentionError).toHaveBeenCalledWith(
      "Project attention target changed. Refresh project work and try again.",
    );
    expect(handlers.onAttentionDefaults).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Project attention: 1" })).toHaveFocus();
  });

  it("applies the stale-project guard to compact attention controls", async () => {
    const { handlers } = renderPanel({
      attentionItems: [
        {
          id: "proj_other:blocked",
          project_id: "proj_other",
          title: "Blocked assignment",
          detail: "One assignment needs review.",
          status: "failed",
          action: {
            type: "open_activity_bucket",
            project_id: "proj_other",
            activity_bucket: "blocked",
          },
          bucket: "blocked",
          action_label: "View blocked",
        },
      ],
      selectedProjectID: "proj_1",
    });

    await userEvent.click(screen.getByRole("button", { name: "Project attention: 1" }));
    await userEvent.click(screen.getByRole("button", { name: "View blocked: Blocked assignment" }));

    expect(handlers.onAttentionError).toHaveBeenCalledWith(
      "Project attention target changed. Refresh project work and try again.",
    );
    expect(handlers.onAttentionBucket).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Project attention: 1" })).toHaveFocus();
  });

  it("shows when lower-priority attention items are hidden", async () => {
    renderPanel({ omittedAttentionCount: 2 });

    await userEvent.click(screen.getByRole("button", { name: "Project attention: 5, 2 hidden" }));

    expect(screen.getAllByText("5+").length).toBeGreaterThan(0);
    expect(
      screen.getByText("Showing 5 of 7 attention items; 2 lower-priority items are hidden."),
    ).toBeTruthy();
  });

  it("shows the server project health summary without deriving attention", async () => {
    renderPanel({
      summary: healthSummary({
        available_attention_count: 8,
        omitted_attention_count: 3,
        missing_defaults: true,
        missing_project_root: true,
        enabled_memory_count: 2,
        saved_memory_count: 3,
        enabled_context_source_count: 4,
        pending_memory_candidate_count: 1,
        pending_handoff_count: 1,
        review_follow_up_count: 2,
        stale_or_unknown_assignment_count: 1,
      }),
    });

    await userEvent.click(screen.getByRole("button", { name: "Project attention: 5, 3 hidden" }));

    expect(screen.getByRole("group", { name: "Project health summary" })).toBeTruthy();
    expect(screen.getByText("2 gaps")).toBeTruthy();
    expect(screen.getByText("defaults, root")).toBeTruthy();
    expect(screen.getByText("2/3 enabled")).toBeTruthy();
    expect(screen.getByText("1 candidate pending")).toBeTruthy();
    expect(screen.getByText("4 sources")).toBeTruthy();
    expect(screen.getByText("4 follow-ups")).toBeTruthy();
    expect(screen.getByText("1 handoff, 2 reviews, 1 assignment link")).toBeTruthy();
  });

  it("uses cleaner summary copy when the project has no saved memory", async () => {
    renderPanel({ summary: healthSummary() });

    await openMenu();

    expect(screen.getByText("No memory yet")).toBeTruthy();
    expect(screen.queryByText("0/0 enabled")).toBeNull();
  });

  it("shows empty guidance when there are no attention items", async () => {
    renderPanel({ attentionItems: [] });

    await userEvent.click(screen.getByRole("button", { name: "Project attention" }));

    expect(screen.getByText("No project attention items detected.")).toBeTruthy();
  });
});
