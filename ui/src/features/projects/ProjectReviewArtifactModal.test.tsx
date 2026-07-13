import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { ProjectAssignmentRecord, ProjectWorkRoleRecord } from "../../types/project";
import { ProjectReviewArtifactModal } from "./ProjectReviewArtifactModal";

const assignment: ProjectAssignmentRecord = {
  id: "assignment_review",
  project_id: "project_1",
  work_item_id: "work_1",
  role_id: "reviewer",
  driver_kind: "manual",
  status: "completed",
  created_at: "2026-07-13T10:00:00Z",
  updated_at: "2026-07-13T11:00:00Z",
};

const sourceAssignment: ProjectAssignmentRecord = {
  ...assignment,
  id: "assignment_source",
  role_id: "editor",
};

const roles: ProjectWorkRoleRecord[] = [
  {
    id: "editor",
    project_id: "project_1",
    name: "Editor",
    default_driver_kind: "manual",
    built_in: false,
  },
  {
    id: "reviewer",
    project_id: "project_1",
    name: "Reviewer",
    default_driver_kind: "manual",
    built_in: false,
  },
];

describe("ProjectReviewArtifactModal", () => {
  it("requires an explicit verdict before saving", async () => {
    const onSave = vi.fn();
    render(
      <ProjectReviewArtifactModal
        assignments={[sourceAssignment, assignment]}
        draft={{
          assignmentID: assignment.id,
          reviewedAssignmentID: "assignment_source",
          authorRoleID: "reviewer",
          title: "Release review",
          verdict: "approved",
          risk: "unknown",
          summary: "Verification passed.",
          verification: "",
          followUp: "",
        }}
        error=""
        pending={false}
        roles={roles}
        onClose={vi.fn()}
        onSave={onSave}
      />,
    );

    const context = screen.getByRole("region", { name: "Review context" });
    expect(within(context).getByText("Reviewing Editor assignment assignment...")).toBeTruthy();
    expect(within(context).getByText("Review assignment Reviewer · assignment...")).toBeTruthy();
    expect(screen.getByLabelText("Review assignment")).toHaveValue(assignment.id);
    expect(screen.getByRole("button", { name: "Save review" })).toBeDisabled();
    expect(screen.getByLabelText("Verdict")).toHaveValue("");

    fireEvent.change(screen.getByLabelText("Verdict"), {
      target: { value: "approved" },
    });
    await userEvent.click(screen.getByRole("button", { name: "Save review" }));

    expect(onSave).toHaveBeenCalledWith(
      expect.objectContaining({
        assignmentID: assignment.id,
        reviewedAssignmentID: sourceAssignment.id,
        verdict: "approved",
      }),
    );
  });

  it("cannot be dismissed while a review is being saved", async () => {
    const onClose = vi.fn();
    const onSave = vi.fn();
    const view = render(
      <ProjectReviewArtifactModal
        assignments={[sourceAssignment, assignment]}
        draft={{
          assignmentID: assignment.id,
          reviewedAssignmentID: sourceAssignment.id,
          authorRoleID: "reviewer",
          title: "Release review",
          verdict: "approved",
          risk: "unknown",
          summary: "Verification passed.",
          verification: "",
          followUp: "",
        }}
        error=""
        pending={false}
        roles={roles}
        onClose={onClose}
        onSave={onSave}
      />,
    );
    fireEvent.change(screen.getByLabelText("Verdict"), {
      target: { value: "approved" },
    });
    view.rerender(
      <ProjectReviewArtifactModal
        assignments={[sourceAssignment, assignment]}
        draft={{
          assignmentID: assignment.id,
          reviewedAssignmentID: sourceAssignment.id,
          authorRoleID: "reviewer",
          title: "Release review",
          verdict: "approved",
          risk: "unknown",
          summary: "Verification passed.",
          verification: "",
          followUp: "",
        }}
        error=""
        pending
        roles={roles}
        onClose={onClose}
        onSave={onSave}
      />,
    );

    const dialog = screen.getByRole("dialog", { name: "Record review" });
    expect(within(dialog).getByRole("button", { name: "Close" })).toBeDisabled();
    screen.getByLabelText("Title").focus();
    await userEvent.keyboard("{Enter}");
    await userEvent.keyboard("{Escape}");
    await userEvent.click(dialog.parentElement as HTMLElement);
    expect(onClose).not.toHaveBeenCalled();
    expect(onSave).not.toHaveBeenCalled();
  });
});
