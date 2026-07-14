import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type {
  ProjectRecord,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
} from "../../types/project";
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

  const props: Parameters<typeof ProjectAssistantPanel>[0] = {
    applyResult: null,
    bootstrapPending: false,
    context: null,
    contextError: "",
    contextStatus: "idle",
    error: "",
    project: project(),
    proposal: null,
    roles: [],
    status: "idle",
    workItem: null,
    ...handlers,
    ...overrides,
  };
  const result = render(<ProjectAssistantPanel {...props} />);

  return { ...handlers, ...result, props };
}

describe("ProjectAssistantPanel", () => {
  it("supports secondary emphasis when the selected work has a guided next action", () => {
    renderAssistantPanel({ primaryEmphasis: false });

    expect(screen.getByRole("button", { name: "Draft proposal" })).toHaveClass("btn-ghost");
  });

  it("offers Human as an assignment destination", async () => {
    const user = userEvent.setup();
    const handlers = renderAssistantPanel();

    const destination = screen.getByLabelText("Work done by");
    await user.selectOptions(destination, "manual");

    expect(destination).toHaveValue("manual");
    expect(destination).toHaveAccessibleDescription(
      "Track work completed by a person outside Hecate.",
    );
    expect(screen.getByRole("option", { name: "Human" })).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "Draft proposal" }));
    expect(handlers.onPropose).toHaveBeenCalledWith(
      expect.objectContaining({ driverKind: "manual" }),
    );
  });

  it("preserves a draft when passive reads refresh the same work and roles", async () => {
    const user = userEvent.setup();
    const workItem: ProjectWorkItemRecord = {
      id: "work_1",
      project_id: "proj_1",
      title: "Refresh safely",
      brief: "Keep operator input stable.",
      status: "ready",
      priority: "normal",
      created_at: "2026-06-12T00:00:00Z",
      updated_at: "2026-06-12T00:00:00Z",
    };
    const role: ProjectWorkRoleRecord = {
      id: "role_1",
      project_id: "proj_1",
      name: "Reviewer",
      built_in: false,
      created_at: "2026-06-12T00:00:00Z",
      updated_at: "2026-06-12T00:00:00Z",
    };
    const result = renderAssistantPanel({ roles: [role], workItem });
    const request = screen.getByLabelText("Request");
    const destination = screen.getByLabelText("Work done by");
    await user.clear(request);
    await user.type(request, "Keep this operator draft");
    await user.selectOptions(destination, "manual");

    result.rerender(
      <ProjectAssistantPanel
        {...result.props}
        project={{ ...result.props.project!, updated_at: "2026-06-12T00:01:00Z" }}
        roles={[{ ...role }]}
        workItem={{ ...workItem, updated_at: "2026-06-12T00:01:00Z" }}
      />,
    );

    expect(request).toHaveValue("Keep this operator draft");
    expect(destination).toHaveValue("manual");
    expect(destination).toHaveFocus();
  });

  it("rebases an untouched draft when authoritative work defaults change", () => {
    const workItem: ProjectWorkItemRecord = {
      id: "work_1",
      project_id: "proj_1",
      title: "Old title",
      brief: "Keep defaults current.",
      status: "ready",
      priority: "normal",
      owner_role_id: "role_1",
      created_at: "2026-06-12T00:00:00Z",
      updated_at: "2026-06-12T00:00:00Z",
    };
    const role: ProjectWorkRoleRecord = {
      id: "role_1",
      project_id: "proj_1",
      name: "Reviewer",
      built_in: false,
      created_at: "2026-06-12T00:00:00Z",
      updated_at: "2026-06-12T00:00:00Z",
    };
    const result = renderAssistantPanel({ roles: [role], workItem });
    const request = screen.getByLabelText("Request");
    expect(request).toHaveValue("Queue Reviewer for Old title");

    result.rerender(
      <ProjectAssistantPanel
        {...result.props}
        roles={[{ ...role, name: "Release reviewer" }]}
        workItem={{ ...workItem, title: "Current title", updated_at: "2026-06-12T00:01:00Z" }}
      />,
    );

    expect(request).toHaveValue("Queue Release reviewer for Current title");
  });

  it("shows setup-ready actions after project setup has begun", async () => {
    const user = userEvent.setup();
    const handlers = renderAssistantPanel({
      memoryCandidateCount: 1,
      roleCount: 2,
      firstWorkReady: true,
      setupFirst: true,
      workItemCount: 0,
    });

    const assistant = screen.getByRole("region", { name: "Project Assistant" });
    expect(within(assistant).getByText("Ready for first work")).toBeTruthy();
    expect(
      within(assistant).getByText(/Project setup has 2 roles · 1 memory suggestion/),
    ).toBeTruthy();
    expect(within(assistant).queryByRole("button", { name: "Set up project" })).toBeNull();
    expect(within(assistant).getByRole("button", { name: "Create first work" })).toHaveClass(
      "btn-primary",
    );
    const setupDetails = within(assistant)
      .getByText("Setup details", { selector: "summary" })
      .closest("details");
    expect(setupDetails).not.toHaveAttribute("open");

    await user.click(within(assistant).getByText("Setup details", { selector: "summary" }));
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
        status: "applied",
        applied: true,
        actions: [{ kind: "create_memory_candidate" }, { kind: "create_role" }],
      },
      memoryCandidateCount: 1,
      roleCount: 2,
      setupFirst: true,
      status: "applied",
      workItemCount: 0,
    });

    const assistant = screen.getByRole("region", { name: "Project Assistant" });
    const result = within(assistant).getByRole("status", {
      name: "Project Assistant apply result",
    });
    expect(within(result).getByText("Applied 2 actions")).toBeTruthy();
    expect(within(result).getByText("Setup changes are applied.")).toBeTruthy();
    expect(within(result).queryByText("Proposal pa_setup is applied.")).toBeNull();
    expect(within(result).getByText("Ready for first work")).toBeTruthy();
    expect(within(result).getAllByText("Create first work").length).toBeGreaterThan(0);
    expect(within(assistant).queryByRole("button", { name: "Set up project" })).toBeNull();
    expect(within(result).getByRole("button", { name: "Create first work" })).toHaveClass(
      "btn-primary",
    );
    const setupDetails = within(result)
      .getByText("Setup details", { selector: "summary" })
      .closest("details");
    expect(setupDetails).not.toHaveAttribute("open");

    await user.click(within(result).getByText("Setup details", { selector: "summary" }));
    expect(within(result).getByText("pa_setup")).toBeTruthy();
    await user.click(within(result).getByRole("button", { name: "Review memory" }));
    await user.click(within(result).getByRole("button", { name: "Review roles" }));
    await user.click(within(result).getByRole("button", { name: "Create first work" }));
    expect(within(result).queryByRole("button", { name: "Continue setup" })).toBeNull();

    expect(handlers.onReviewMemory).toHaveBeenCalledTimes(1);
    expect(handlers.onManageRoles).toHaveBeenCalledTimes(1);
    expect(handlers.onCreateWork).toHaveBeenCalledTimes(1);
    expect(handlers.onOpenWork).not.toHaveBeenCalled();
  });

  it("keeps setup proposal details collapsed behind one review action", async () => {
    const user = userEvent.setup();
    const handlers = renderAssistantPanel({
      proposal: {
        id: "pa_setup",
        title: "Bootstrap Hecate guidance",
        summary: "Add a role and preserve guidance as a memory suggestion.",
        requires_confirmation: true,
        trace_id: "trace_setup",
        actions: [{ kind: "create_role", patch: { name: "Editor" } }],
      },
      setupFirst: true,
    });

    const assistant = screen.getByRole("region", { name: "Project Assistant" });
    expect(within(assistant).getByText("Review setup")).toBeTruthy();
    expect(within(assistant).queryByRole("button", { name: "Dismiss" })).toBeNull();
    const proposedChanges = within(assistant)
      .getByText("Review proposed changes", { selector: "summary" })
      .closest("details");
    expect(proposedChanges).not.toHaveAttribute("open");

    await user.click(
      within(assistant).getByText("Review proposed changes", { selector: "summary" }),
    );
    expect(within(assistant).getByText("trace_setup")).toBeTruthy();
    expect(within(assistant).getByText("Editor")).toBeTruthy();
    await user.click(within(assistant).getByRole("button", { name: "Apply setup" }));
    expect(handlers.onApply).toHaveBeenCalledTimes(1);
  });

  it("focuses a new work proposal heading before its apply action", () => {
    renderAssistantPanel({
      proposal: {
        id: "pa_assignment",
        title: "Assign research work",
        summary: "Create one reviewable assignment.",
        requires_confirmation: true,
        actions: [{ kind: "create_assignment", patch: { role_id: "researcher" } }],
      },
    });

    const heading = screen.getByRole("heading", { name: "Assign research work" });
    expect(heading).toHaveFocus();
    expect(heading).not.toHaveStyle({ outline: "none" });
    expect(screen.getByRole("button", { name: "Apply proposal" })).not.toHaveFocus();
  });

  it("keeps a dismiss action for a headerless apply result", async () => {
    const handlers = renderAssistantPanel({
      applyResult: {
        proposal_id: "pa_assignment",
        status: "applied",
        applied: true,
        actions: [{ kind: "create_assignment" }],
      },
      showHeader: false,
      status: "applied",
      workItemCount: 1,
    });

    await userEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(handlers.onDismiss).toHaveBeenCalledTimes(1);
  });

  it("routes applied work proposals back to the work queue", async () => {
    const user = userEvent.setup();
    const handlers = renderAssistantPanel({
      applyResult: {
        proposal_id: "pa_assignment",
        status: "applied",
        applied: true,
        actions: [{ kind: "create_assignment" }],
      },
      status: "applied",
      workItemCount: 1,
    });

    const result = screen.getByRole("status", { name: "Project Assistant apply result" });
    expect(within(result).queryByRole("button", { name: "Create first work" })).toBeNull();
    expect(within(result).getByText("Next up")).toBeTruthy();
    expect(within(result).getAllByText("Open work queue")).toHaveLength(2);

    await user.click(within(result).getByRole("button", { name: "Open work queue" }));

    expect(handlers.onOpenWork).toHaveBeenCalledTimes(1);
  });

  it("prevents dismissing a proposal while apply reconciliation is pending", async () => {
    const user = userEvent.setup();
    const handlers = renderAssistantPanel({
      proposal: {
        id: "pa_applying",
        title: "Create coordinated work",
        summary: "",
        requires_confirmation: true,
        actions: [{ kind: "create_work_item", patch: { title: "New work" } }],
      },
      status: "applying",
    });

    const dismiss = screen.getByRole("button", { name: "Dismiss proposal" });
    expect(dismiss).toBeDisabled();
    expect(screen.getByRole("button", { name: "Applying…" })).toBeDisabled();

    await user.click(dismiss);
    expect(handlers.onDismiss).not.toHaveBeenCalled();
  });
});
