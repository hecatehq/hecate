import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type {
  ProjectOperationsBriefItem,
  ProjectWorkItemReadinessRecord,
  ProjectWorkItemRecord,
} from "../../types/project";
import {
  ProjectWorkItemFollowThrough,
  projectWorkItemFollowThroughIntent,
} from "./ProjectWorkItemFollowThrough";

function readiness(
  overrides: Partial<ProjectWorkItemReadinessRecord> = {},
): ProjectWorkItemReadinessRecord {
  return {
    project_id: "project_1",
    work_item_id: "work_1",
    ready: false,
    status: "blocked",
    title: "Closeout is blocked",
    detail: "Resolve the exact follow-through target.",
    blockers: [],
    warnings: [],
    assignment_count: 2,
    completed_assignments: 2,
    review_follow_up_count: 1,
    missing_evidence_assignment_ids: ["assignment_target"],
    review_follow_up_artifact_ids: ["artifact_target"],
    open_handoff_ids: ["handoff_target"],
    ...overrides,
  };
}

function operation(
  kind: string,
  target: Partial<ProjectOperationsBriefItem["target"]> = {},
  action: Partial<ProjectOperationsBriefItem["action"]> = {},
): ProjectOperationsBriefItem {
  return {
    id: `${kind}:work_1`,
    kind,
    priority: "high",
    title: "Resolve the next work step",
    detail: "Use the structured target from the project operation.",
    action_label: "Open work",
    target: {
      surface: "work",
      project_id: "project_1",
      work_item_id: "work_1",
      ...target,
    },
    action: {
      type: "open_work_item",
      project_id: "project_1",
      work_item_id: "work_1",
      assignment_id: target.assignment_id,
      artifact_id: target.artifact_id,
      handoff_id: target.handoff_id,
      ...action,
    },
  };
}

const workItem: ProjectWorkItemRecord = {
  id: "work_1",
  project_id: "project_1",
  title: "Prepare release",
  status: "review",
  priority: "high",
  created_at: "2026-07-13T10:00:00Z",
  updated_at: "2026-07-13T11:00:00Z",
};

describe("ProjectWorkItemFollowThrough", () => {
  it("selects exact action targets without deriving intent from operation kind", () => {
    expect(
      projectWorkItemFollowThroughIntent(
        readiness(),
        operation("review_follow_up", { assignment_id: "assignment_target" }),
        workItem,
      ),
    ).toEqual({ kind: "record_evidence", assignmentID: "assignment_target" });
    expect(
      projectWorkItemFollowThroughIntent(
        readiness(),
        operation("review_follow_up", { artifact_id: "artifact_target" }),
        workItem,
      ),
    ).toEqual({ kind: "plan_review_follow_up", artifactID: "artifact_target" });
    expect(
      projectWorkItemFollowThroughIntent(
        readiness(),
        operation("review_pending_handoff", { handoff_id: "handoff_target" }),
        workItem,
      ),
    ).toEqual({ kind: "review_handoff", handoffID: "handoff_target" });
    expect(
      projectWorkItemFollowThroughIntent(
        readiness(),
        operation("record_completion_evidence", {
          assignment_id: "assignment_decoy",
        }),
        workItem,
      ),
    ).toEqual({ kind: "focus_assignment", assignmentID: "assignment_decoy" });
    expect(
      projectWorkItemFollowThroughIntent(
        readiness(),
        operation("review_follow_up", { artifact_id: "artifact_decoy" }),
        workItem,
      ),
    ).toEqual({ kind: "refresh_work" });
  });

  it.each<[string, ProjectOperationsBriefItem]>([
    ["review artifact", operation("review_follow_up", { artifact_id: "artifact_stale" })],
    ["handoff", operation("review_pending_handoff", { handoff_id: "handoff_stale" })],
    ["closeout", operation("close_work_item")],
  ])("offers refresh when %s operation and readiness are out of sync", (_label, item) => {
    expect(projectWorkItemFollowThroughIntent(readiness(), item, workItem)).toEqual({
      kind: "refresh_work",
    });
  });

  it("offers authoritative refresh when the descriptive target disagrees with the action", async () => {
    const mismatchedOperation = operation(
      "record_completion_evidence",
      { assignment_id: "assignment_target" },
      { assignment_id: "assignment_decoy" },
    );

    const onRefresh = vi.fn();
    render(
      <ProjectWorkItemFollowThrough
        closeout={readiness()}
        operation={mismatchedOperation}
        pending={false}
        workItem={workItem}
        onFocusAssignment={vi.fn()}
        onPlanReviewFollowUp={vi.fn()}
        onRecordEvidence={vi.fn()}
        onRefresh={onRefresh}
        onReviewCloseout={vi.fn()}
        onReviewHandoff={vi.fn()}
      />,
    );

    expect(screen.getByText("Next action unavailable")).toBeTruthy();
    expect(
      screen.getByText("Project work changed. Refresh project work before continuing."),
    ).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Refresh work" }));
    expect(onRefresh).toHaveBeenCalledTimes(1);
    expect(projectWorkItemFollowThroughIntent(readiness(), mismatchedOperation, workItem)).toEqual({
      kind: "refresh_work",
    });
  });

  it("delegates the exact missing-evidence assignment", async () => {
    const onRecordEvidence = vi.fn();
    render(
      <ProjectWorkItemFollowThrough
        closeout={readiness()}
        operation={operation("record_completion_evidence", {
          assignment_id: "assignment_target",
        })}
        pending={false}
        workItem={workItem}
        onFocusAssignment={vi.fn()}
        onPlanReviewFollowUp={vi.fn()}
        onRecordEvidence={onRecordEvidence}
        onRefresh={vi.fn()}
        onReviewCloseout={vi.fn()}
        onReviewHandoff={vi.fn()}
      />,
    );

    await userEvent.click(screen.getByRole("button", { name: "Record evidence" }));
    expect(onRecordEvidence).toHaveBeenCalledWith("assignment_target");
  });

  it("fails closed when the typed record is absent from loaded detail", () => {
    const item = operation("record_completion_evidence", {
      assignment_id: "assignment_target",
    });

    expect(projectWorkItemFollowThroughIntent(readiness(), item, workItem, false)).toEqual({
      kind: "refresh_work",
    });
  });

  it("uses readiness done as a read-only terminal signal", () => {
    render(
      <ProjectWorkItemFollowThrough
        closeout={readiness({ ready: false, status: "done" })}
        operation={operation("close_work_item")}
        pending={false}
        workItem={{ ...workItem, status: "review" }}
        onFocusAssignment={vi.fn()}
        onPlanReviewFollowUp={vi.fn()}
        onRecordEvidence={vi.fn()}
        onRefresh={vi.fn()}
        onReviewCloseout={vi.fn()}
        onReviewHandoff={vi.fn()}
      />,
    );

    expect(screen.getByText("Work closed")).toBeTruthy();
    expect(screen.queryByRole("button")).toBeNull();
  });

  it.each(["done", "cancelled"])(
    "uses persisted %s status as a read-only terminal signal when readiness is stale",
    (status) => {
      render(
        <ProjectWorkItemFollowThrough
          closeout={readiness()}
          operation={operation("record_completion_evidence", {
            assignment_id: "assignment_target",
          })}
          pending={false}
          workItem={{ ...workItem, status }}
          onFocusAssignment={vi.fn()}
          onPlanReviewFollowUp={vi.fn()}
          onRecordEvidence={vi.fn()}
          onRefresh={vi.fn()}
          onReviewCloseout={vi.fn()}
          onReviewHandoff={vi.fn()}
        />,
      );

      expect(screen.getByText("Work closed")).toBeTruthy();
      expect(screen.queryByRole("button")).toBeNull();
      expect(
        projectWorkItemFollowThroughIntent(readiness(), operation("close_work_item"), {
          ...workItem,
          status,
        }),
      ).toBeNull();
    },
  );
});
