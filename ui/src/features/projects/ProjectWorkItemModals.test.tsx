import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type {
  ProjectRecord,
  ProjectRootRecord,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
} from "../../types/project";
import { EditWorkItemModal, NewWorkItemModal } from "./ProjectWorkItemModals";

function root(overrides: Partial<ProjectRootRecord>): ProjectRootRecord {
  return {
    id: "root_main",
    path: "/workspace/main",
    kind: "workspace",
    active: true,
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
    ...overrides,
  };
}

function project(overrides: Partial<ProjectRecord> = {}): ProjectRecord {
  return {
    id: "proj_1",
    name: "Hecate",
    roots: [
      root({ id: "root_main", path: "/workspace/main" }),
      root({ id: "root_feature", path: "/workspace/feature", kind: "git_worktree" }),
    ],
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
    ...overrides,
  };
}

function role(overrides: Partial<ProjectWorkRoleRecord> = {}): ProjectWorkRoleRecord {
  return {
    id: "software_developer",
    project_id: "proj_1",
    name: "Software developer",
    built_in: false,
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
    ...overrides,
  };
}

function workItem(overrides: Partial<ProjectWorkItemRecord> = {}): ProjectWorkItemRecord {
  return {
    id: "work_1",
    project_id: "proj_1",
    title: "Build cockpit",
    brief: "Ship the first slice.",
    status: "ready",
    priority: "normal",
    owner_role_id: "software_developer",
    root_id: "root_main",
    reviewer_role_ids: ["architect"],
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
    ...overrides,
  };
}

describe("ProjectWorkItemModals", () => {
  it("creates work items with default owner role and selected root", async () => {
    const onCreate = vi.fn();

    render(
      <NewWorkItemModal
        error=""
        pending={false}
        project={project()}
        roles={[role(), role({ id: "architect", name: "Architect" })]}
        onClose={vi.fn()}
        onCreate={onCreate}
      />,
    );

    await userEvent.type(screen.getByLabelText("Title"), "Implement project queue");
    await userEvent.type(screen.getByLabelText("Brief"), "Keep parent orchestration thin.");
    fireEvent.change(screen.getByLabelText("Root"), { target: { value: "root_feature" } });
    await userEvent.click(screen.getByRole("button", { name: "Create work item" }));

    expect(onCreate).toHaveBeenCalledWith({
      title: "Implement project queue",
      brief: "Keep parent orchestration thin.",
      priority: "normal",
      ownerRoleID: "software_developer",
      rootID: "root_feature",
    });
  });

  it("blocks duplicate submission and dismissal while creation is pending", () => {
    const onClose = vi.fn();
    const onCreate = vi.fn();

    const { rerender } = render(
      <NewWorkItemModal
        error=""
        initialDraft={{ title: "Pending work" }}
        pending
        project={project({ roots: [] })}
        roles={[]}
        onClose={onClose}
        onCreate={onCreate}
      />,
    );

    const dialog = screen.getByRole("dialog", { name: "New work item" });
    const form = screen.getByLabelText("Title").closest("form");
    expect(form).toHaveAttribute("aria-busy", "true");
    fireEvent.submit(form!);
    fireEvent.keyDown(document, { key: "Escape" });

    expect(onCreate).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Creating…" })).toBeDisabled();
    expect(dialog).toBeTruthy();

    rerender(
      <NewWorkItemModal
        error="Creation failed."
        initialDraft={{ title: "Pending work" }}
        pending={false}
        project={project({ roots: [] })}
        roles={[]}
        onClose={onClose}
        onCreate={onCreate}
      />,
    );
    expect(screen.getByRole("button", { name: "Create work item" })).toHaveFocus();
  });

  it("edits work item metadata including reviewer roles", async () => {
    const onSave = vi.fn();

    render(
      <EditWorkItemModal
        error=""
        item={workItem()}
        pending={false}
        project={project()}
        roles={[role(), role({ id: "architect", name: "Architect" })]}
        onClose={vi.fn()}
        onSave={onSave}
      />,
    );

    fireEvent.change(screen.getByLabelText("Status"), { target: { value: "review" } });
    await userEvent.clear(screen.getByLabelText("Reviewer roles"));
    await userEvent.type(screen.getByLabelText("Reviewer roles"), "architect, reviewer_qa");
    await userEvent.click(screen.getByRole("button", { name: "Save work item" }));

    expect(onSave).toHaveBeenCalledWith(
      expect.objectContaining({
        id: "work_1",
        status: "review",
        reviewerRoleIDs: "architect, reviewer_qa",
      }),
    );
  });
});
