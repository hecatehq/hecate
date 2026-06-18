import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { ProjectRecord } from "../../types/project";
import { ProjectAssistantPanel } from "./ProjectAssistantPanel";

function project(overrides: Partial<ProjectRecord> = {}): ProjectRecord {
  return {
    id: "proj_1",
    name: "Hecate",
    roots: [],
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
    ...overrides,
  };
}

function renderAssistantPanel(
  overrides: Partial<Parameters<typeof ProjectAssistantPanel>[0]> = {},
) {
  const handlers = {
    onApply: vi.fn(),
    onBootstrap: vi.fn(),
    onCreateWork: vi.fn(),
    onDismiss: vi.fn(),
    onInspectContext: vi.fn(),
    onManageRoles: vi.fn(),
    onOpenWork: vi.fn(),
    onPropose: vi.fn(),
    onReviewMemory: vi.fn(),
  };

  render(
    <ProjectAssistantPanel
      applyResult={null}
      bootstrapPending={false}
      context={null}
      contextError=""
      contextStatus="idle"
      error=""
      project={project()}
      proposal={null}
      roles={[]}
      status="idle"
      workItem={null}
      {...handlers}
      {...overrides}
    />,
  );

  return handlers;
}

describe("ProjectAssistantPanel", () => {
  it("shows setup-ready actions after project setup has begun", async () => {
    const user = userEvent.setup();
    const handlers = renderAssistantPanel({
      memoryCandidateCount: 1,
      roleCount: 2,
      setupFirst: true,
      setupStarted: true,
      workItemCount: 0,
    });

    const assistant = screen.getByRole("region", { name: "Project Assistant" });
    expect(within(assistant).getByText("Setup ready")).toBeTruthy();
    expect(
      within(assistant).getByText(/Project setup has 2 roles · 1 memory candidate/),
    ).toBeTruthy();
    expect(within(assistant).queryByRole("button", { name: "Set up project" })).toBeNull();

    await user.click(within(assistant).getByRole("button", { name: "Review memory" }));
    await user.click(within(assistant).getByRole("button", { name: "Review roles" }));
    await user.click(within(assistant).getByRole("button", { name: "Create first work" }));
    await user.click(within(assistant).getByRole("button", { name: "Refresh setup" }));

    expect(handlers.onReviewMemory).toHaveBeenCalledTimes(1);
    expect(handlers.onManageRoles).toHaveBeenCalledTimes(1);
    expect(handlers.onCreateWork).toHaveBeenCalledTimes(1);
    expect(handlers.onBootstrap).toHaveBeenCalledTimes(1);
  });

  it("turns applied setup proposals into follow-up actions", async () => {
    const user = userEvent.setup();
    const handlers = renderAssistantPanel({
      applyResult: {
        proposal_id: "pa_setup",
        applied: true,
        actions: [{ kind: "create_memory_candidate" }, { kind: "create_role" }],
      },
      memoryCandidateCount: 1,
      roleCount: 2,
      setupFirst: true,
      setupStarted: true,
      status: "applied",
      workItemCount: 0,
    });

    const assistant = screen.getByRole("region", { name: "Project Assistant" });
    const result = within(assistant).getByRole("status", {
      name: "Project Assistant apply result",
    });
    expect(within(result).getByText("Applied 2 actions")).toBeTruthy();
    expect(within(assistant).queryByRole("button", { name: "Set up project" })).toBeNull();

    await user.click(within(result).getByRole("button", { name: "Review memory" }));
    await user.click(within(result).getByRole("button", { name: "Review roles" }));
    await user.click(within(result).getByRole("button", { name: "Create first work" }));

    expect(handlers.onReviewMemory).toHaveBeenCalledTimes(1);
    expect(handlers.onManageRoles).toHaveBeenCalledTimes(1);
    expect(handlers.onCreateWork).toHaveBeenCalledTimes(1);
  });

  it("routes applied work proposals back to the work queue", async () => {
    const user = userEvent.setup();
    const handlers = renderAssistantPanel({
      applyResult: {
        proposal_id: "pa_assignment",
        applied: true,
        actions: [{ kind: "create_assignment" }],
      },
      status: "applied",
      workItemCount: 1,
    });

    const result = screen.getByRole("status", { name: "Project Assistant apply result" });
    expect(within(result).queryByRole("button", { name: "Create first work" })).toBeNull();

    await user.click(within(result).getByRole("button", { name: "Open work queue" }));

    expect(handlers.onOpenWork).toHaveBeenCalledTimes(1);
  });
});
