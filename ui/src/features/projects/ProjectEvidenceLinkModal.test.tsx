import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { ProjectAssignmentRecord, ProjectWorkRoleRecord } from "../../types/project";
import { ProjectEvidenceLinkModal } from "./ProjectEvidenceLinkModal";

const assignments: ProjectAssignmentRecord[] = [
  {
    id: "assignment_decoy",
    project_id: "project_1",
    work_item_id: "work_1",
    role_id: "developer",
    driver_kind: "manual",
    status: "completed",
    created_at: "2026-07-13T10:00:00Z",
    updated_at: "2026-07-13T11:00:00Z",
  },
  {
    id: "assignment_target",
    project_id: "project_1",
    work_item_id: "work_1",
    role_id: "reviewer",
    driver_kind: "manual",
    status: "completed",
    created_at: "2026-07-13T10:00:00Z",
    updated_at: "2026-07-13T11:00:00Z",
  },
];
const roles: ProjectWorkRoleRecord[] = [
  {
    id: "developer",
    project_id: "project_1",
    name: "Developer",
    built_in: false,
  },
  {
    id: "reviewer",
    project_id: "project_1",
    name: "Release reviewer",
    built_in: false,
  },
];

describe("ProjectEvidenceLinkModal", () => {
  it("preselects only a loaded structured assignment target", () => {
    const { unmount } = render(
      <ProjectEvidenceLinkModal
        assignments={assignments}
        error=""
        initialAssignmentID="assignment_target"
        pending={false}
        roles={roles}
        onClose={vi.fn()}
        onSave={vi.fn()}
      />,
    );

    expect(screen.getByLabelText("Assignment")).toHaveValue("assignment_target");
    expect(screen.getByRole("option", { name: /Release reviewer · done/ })).toBeTruthy();
    unmount();

    render(
      <ProjectEvidenceLinkModal
        assignments={assignments}
        error=""
        initialAssignmentID="assignment_missing"
        pending={false}
        roles={roles}
        onClose={vi.fn()}
        onSave={vi.fn()}
      />,
    );
    expect(screen.getByLabelText("Assignment")).toHaveValue("");
  });

  it("cannot be dismissed while evidence is being recorded", async () => {
    const onClose = vi.fn();
    const onSave = vi.fn();
    const view = render(
      <ProjectEvidenceLinkModal
        assignments={assignments}
        error=""
        pending={false}
        roles={roles}
        onClose={onClose}
        onSave={onSave}
      />,
    );
    await userEvent.type(screen.getByLabelText("Title"), "Release evidence");
    await userEvent.type(screen.getByLabelText("URL"), "https://example.invalid/release");
    await userEvent.type(screen.getByLabelText("Summary"), "Verified release output.");
    view.rerender(
      <ProjectEvidenceLinkModal
        assignments={assignments}
        error=""
        pending
        roles={roles}
        onClose={onClose}
        onSave={onSave}
      />,
    );

    const dialog = screen.getByRole("dialog", { name: "Record evidence" });
    expect(within(dialog).getByRole("button", { name: "Close" })).toBeDisabled();
    screen.getByLabelText("Title").focus();
    await userEvent.keyboard("{Enter}");
    await userEvent.keyboard("{Escape}");
    await userEvent.click(dialog.parentElement as HTMLElement);
    expect(onClose).not.toHaveBeenCalled();
    expect(onSave).not.toHaveBeenCalled();
  });
});
