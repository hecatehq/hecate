import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type {
  ProjectAssignmentRecord,
  ProjectHandoffRecord,
  ProjectWorkRoleRecord,
} from "../../types/project";
import { ProjectHandoffModal } from "./ProjectHandoffModal";

function role(overrides: Partial<ProjectWorkRoleRecord> = {}): ProjectWorkRoleRecord {
  return {
    id: "reviewer",
    project_id: "proj_1",
    name: "Reviewer",
    built_in: false,
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
    ...overrides,
  };
}

function assignment(overrides: Partial<ProjectAssignmentRecord> = {}): ProjectAssignmentRecord {
  return {
    id: "assign_1234567890",
    project_id: "proj_1",
    work_item_id: "work_1",
    role_id: "developer",
    driver_kind: "hecate_task",
    status: "queued",
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
    ...overrides,
  };
}

function handoff(overrides: Partial<ProjectHandoffRecord> = {}): ProjectHandoffRecord {
  return {
    id: "handoff_1",
    project_id: "proj_1",
    work_item_id: "work_1",
    title: "QA review",
    summary: "Ready for QA.",
    recommended_next_action: "Verify the behavior.",
    status: "pending",
    provenance_kind: "operator",
    trust_label: "operator_reviewed",
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
    status_changed_at: "2026-06-12T00:00:00Z",
    ...overrides,
  };
}

describe("ProjectHandoffModal", () => {
  it("creates a handoff with source assignment and target role", async () => {
    const onSave = vi.fn();

    render(
      <ProjectHandoffModal
        assignments={[assignment()]}
        error=""
        handoff={null}
        pending={false}
        roles={[role()]}
        onClose={vi.fn()}
        onSave={onSave}
      />,
    );

    await userEvent.type(screen.getByLabelText("Title"), "QA review");
    await userEvent.type(screen.getByLabelText("Summary"), "Ready for test.");
    await userEvent.type(screen.getByLabelText("Recommended next action"), "Run regression.");
    fireEvent.change(screen.getByLabelText("Source assignment"), {
      target: { value: "assign_1234567890" },
    });
    fireEvent.change(screen.getByLabelText("Target role"), { target: { value: "reviewer" } });
    await userEvent.type(screen.getByLabelText("Artifact IDs"), "art_1, art_2");
    await userEvent.click(screen.getByRole("button", { name: "Save handoff" }));

    expect(onSave).toHaveBeenCalledWith(
      expect.objectContaining({
        sourceAssignmentID: "assign_1234567890",
        targetRoleID: "reviewer",
        title: "QA review",
        summary: "Ready for test.",
        recommendedNextAction: "Run regression.",
        linkedArtifactIDs: "art_1, art_2",
      }),
    );
  });

  it("edits an existing handoff status", async () => {
    const onSave = vi.fn();

    render(
      <ProjectHandoffModal
        assignments={[assignment()]}
        error=""
        handoff={handoff({ target_role_id: "reviewer" })}
        pending={false}
        roles={[role()]}
        onClose={vi.fn()}
        onSave={onSave}
      />,
    );

    fireEvent.change(screen.getByLabelText("Status"), { target: { value: "accepted" } });
    await userEvent.click(screen.getByRole("button", { name: "Save handoff" }));

    expect(onSave).toHaveBeenCalledWith(
      expect.objectContaining({
        id: "handoff_1",
        status: "accepted",
        targetRoleID: "reviewer",
      }),
    );
  });
});
