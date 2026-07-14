import { render, screen } from "@testing-library/react";
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
}

describe("ProjectHealthPanel", () => {
  it("renders attention items and delegates direct row activation", async () => {
    const { handlers } = renderPanel();

    await openMenu();
    expect(screen.getByRole("menu", { name: "Project attention" })).toBeTruthy();
    await userEvent.click(
      screen.getByRole("button", { name: "Open attention item Provider/model defaults missing" }),
    );

    expect(handlers.onAttentionDefaults).toHaveBeenCalledTimes(1);
  });

  it("delegates nested action buttons without also activating the row", async () => {
    const { candidate, handlers } = renderPanel();

    await openMenu();
    await userEvent.click(screen.getByRole("button", { name: "View blocked" }));
    expect(handlers.onAttentionBucket).toHaveBeenCalledWith("blocked");
    expect(handlers.onAttentionDefaults).not.toHaveBeenCalled();

    await openMenu();
    expect(screen.getByText("Open review")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Open review" }));
    expect(handlers.onAttentionWorkItem).toHaveBeenCalledWith("work_1");

    await openMenu();
    await userEvent.click(screen.getByRole("button", { name: "Open attention task" }));
    expect(handlers.onAttentionTask).toHaveBeenCalledWith("task_1", "run_1");

    await openMenu();
    await userEvent.click(screen.getByRole("button", { name: "Review memory candidate" }));
    expect(handlers.onAttentionReviewCandidate).toHaveBeenCalledWith(candidate);
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

  it("falls back to the memory view when candidate details are missing", async () => {
    const { handlers } = renderPanel({ memoryCandidates: [] });

    await openMenu();
    await userEvent.click(
      screen.getByRole("button", { name: "Open attention item Memory candidate pending review" }),
    );

    expect(handlers.onAttentionMemory).toHaveBeenCalledTimes(1);
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
    await userEvent.click(screen.getByRole("button", { name: "View blocked" }));

    expect(handlers.onAttentionError).toHaveBeenCalledWith(
      "Project attention target changed. Refresh project work and try again.",
    );
    expect(handlers.onAttentionBucket).not.toHaveBeenCalled();
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

    expect(screen.getByLabelText("Project health summary")).toBeTruthy();
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
