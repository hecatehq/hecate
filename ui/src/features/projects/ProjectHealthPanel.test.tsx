import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { ProjectHealthAttention, ProjectMemoryCandidateRecord } from "../../types/project";
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

function renderPanel(overrides: Partial<Parameters<typeof ProjectHealthPanel>[0]> = {}) {
  const candidate = memoryCandidate();
  const attentionItems: ProjectHealthAttention[] = [
    {
      id: "proj_1:defaults",
      title: "Provider/model defaults missing",
      detail: "Set defaults before starting assignments.",
      status: "awaiting_approval",
      action: "settings",
    },
    {
      id: "assign_1:blocked",
      title: "Blocked assignment",
      detail: "One assignment needs review.",
      status: "failed",
      bucket: "blocked",
      action_label: "View blocked",
    },
    {
      id: "work_1:item",
      title: "Work item needs attention",
      detail: "Open the work item.",
      status: "running",
      work_item_id: "work_1",
      action_label: "Open review",
    },
    {
      id: "task_1:item",
      title: "Task needs attention",
      detail: "Open the linked task.",
      status: "running",
      task_id: "task_1",
      run_id: "run_1",
    },
    {
      id: "memcand_1:memory-candidate",
      title: "Memory candidate pending review",
      detail: "Review generated memory.",
      status: "awaiting_approval",
      candidate_id: candidate.id,
      action: "memory",
    },
  ];
  const handlers = {
    onAttentionBucket: vi.fn(),
    onAttentionDefaults: vi.fn(),
    onAttentionMemory: vi.fn(),
    onAttentionProfiles: vi.fn(),
    onAttentionReviewCandidate: vi.fn(),
    onAttentionRoles: vi.fn(),
    onAttentionSkills: vi.fn(),
    onAttentionTask: vi.fn(),
    onAttentionWorkItem: vi.fn(),
  };
  render(
    <ProjectHealthPanel
      attentionItems={attentionItems}
      memoryCandidates={[candidate]}
      {...handlers}
      {...overrides}
    />,
  );
  return { candidate, handlers };
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

  it("falls back to the memory view when candidate details are missing", async () => {
    const { handlers } = renderPanel({ memoryCandidates: [] });

    await openMenu();
    await userEvent.click(
      screen.getByRole("button", { name: "Open attention item Memory candidate pending review" }),
    );

    expect(handlers.onAttentionMemory).toHaveBeenCalledTimes(1);
  });

  it("shows when lower-priority attention items are hidden", async () => {
    renderPanel({ omittedAttentionCount: 2 });

    await userEvent.click(screen.getByRole("button", { name: "Project attention: 5, 2 hidden" }));

    expect(screen.getAllByText("5+").length).toBeGreaterThan(0);
    expect(screen.getByText("2 lower-priority items are hidden by the server cap.")).toBeTruthy();
  });

  it("shows empty guidance when there are no attention items", async () => {
    renderPanel({ attentionItems: [] });

    await userEvent.click(screen.getByRole("button", { name: "Project attention" }));

    expect(screen.getByText("No project attention items detected.")).toBeTruthy();
  });
});
