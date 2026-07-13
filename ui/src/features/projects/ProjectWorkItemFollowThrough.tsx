import type { CSSProperties } from "react";

import type {
  ProjectAssignmentRecord,
  ProjectCollaborationArtifactRecord,
  ProjectHandoffRecord,
  ProjectOperationsBriefItem,
  ProjectWorkItemReadinessRecord,
  ProjectWorkItemRecord,
} from "../../types/project";
import { Badge, Icon, Icons } from "../shared/ui";
import { projectOperationHasActionTargetMismatch } from "./projectActionRouting";
import { toProjectAssignmentExecutionViewModel } from "./projectAssignmentViewModels";

export type ProjectWorkItemFollowThroughIntent =
  | { kind: "focus_assignment"; assignmentID: string }
  | { kind: "plan_review_follow_up"; artifactID: string }
  | { kind: "record_evidence"; assignmentID: string }
  | { kind: "refresh_work" }
  | { kind: "review_closeout" }
  | { kind: "review_handoff"; handoffID: string };

type ProjectWorkItemFollowThroughProps = {
  closeout: ProjectWorkItemReadinessRecord;
  operation: ProjectOperationsBriefItem | null;
  pending: boolean;
  targetAvailable?: boolean;
  workItem: ProjectWorkItemRecord;
  onFocusAssignment: (assignmentID: string) => void;
  onPlanReviewFollowUp: (artifactID: string) => void;
  onRecordEvidence: (assignmentID: string) => void;
  onRefresh: () => void;
  onReviewCloseout: () => void;
  onReviewHandoff: (handoffID: string) => void;
};

export function ProjectWorkItemFollowThrough({
  closeout,
  operation,
  pending,
  targetAvailable = true,
  workItem,
  onFocusAssignment,
  onPlanReviewFollowUp,
  onRecordEvidence,
  onRefresh,
  onReviewCloseout,
  onReviewHandoff,
}: ProjectWorkItemFollowThroughProps) {
  const workClosed = closeout.status === "done" || workItemClosed(workItem.status);
  const intent = projectWorkItemFollowThroughIntent(closeout, operation, workItem, targetAvailable);
  const copy = projectWorkItemFollowThroughCopy(closeout, operation, workItem, targetAvailable);
  return (
    <section
      aria-label="Next work item action"
      className="project-work-follow-through project-work-focus-target"
      id="project-work-follow-through"
      style={followThroughStyle}
      tabIndex={-1}
    >
      <div style={followThroughCopyStyle}>
        <div style={followThroughKickerStyle}>
          <span className="kicker">Next action</span>
          {operation && !workClosed && (
            <span className="badge badge-muted">{operation.priority}</span>
          )}
          {workClosed && (
            <Badge
              status={workItem.status === "cancelled" ? "cancelled" : "completed"}
              label={workItem.status === "cancelled" ? "cancelled" : "done"}
            />
          )}
        </div>
        <h3 style={followThroughTitleStyle}>{copy.title}</h3>
        <p style={followThroughDetailStyle}>{copy.detail}</p>
      </div>
      {intent && !workClosed && (
        <button
          className="btn btn-primary btn-sm project-work-follow-through-action"
          type="button"
          disabled={pending}
          onClick={() =>
            runFollowThroughIntent(intent, {
              onFocusAssignment,
              onPlanReviewFollowUp,
              onRecordEvidence,
              onRefresh,
              onReviewCloseout,
              onReviewHandoff,
            })
          }
        >
          <Icon d={projectWorkItemFollowThroughIcon(intent)} size={12} />
          {pending ? "Working…" : projectWorkItemFollowThroughLabel(intent, operation)}
        </button>
      )}
    </section>
  );
}

export function projectWorkItemFollowThroughIntent(
  closeout: ProjectWorkItemReadinessRecord,
  operation: ProjectOperationsBriefItem | null,
  workItem: Pick<ProjectWorkItemRecord, "id" | "status">,
  targetAvailable = true,
): ProjectWorkItemFollowThroughIntent | null {
  if (
    closeout.status === "done" ||
    workItemClosed(workItem.status) ||
    !operation ||
    !operationTargetsWorkItem(operation, workItem.id)
  ) {
    return null;
  }
  if (projectOperationHasActionTargetMismatch(operation)) {
    return { kind: "refresh_work" };
  }
  if (!targetAvailable) {
    return { kind: "refresh_work" };
  }
  const artifactID = operation.action.artifact_id?.trim() || "";
  if (artifactID) {
    if ((closeout.review_follow_up_artifact_ids ?? []).includes(artifactID)) {
      return { kind: "plan_review_follow_up", artifactID };
    }
    return { kind: "refresh_work" };
  }
  const handoffID = operation.action.handoff_id?.trim() || "";
  if (handoffID) {
    if ((closeout.open_handoff_ids ?? []).includes(handoffID)) {
      return { kind: "review_handoff", handoffID };
    }
    return { kind: "refresh_work" };
  }
  const assignmentID = operation.action.assignment_id?.trim() || "";
  if (assignmentID) {
    if ((closeout.missing_evidence_assignment_ids ?? []).includes(assignmentID)) {
      return { kind: "record_evidence", assignmentID };
    }
    return { kind: "focus_assignment", assignmentID };
  }
  return closeout.ready && closeout.status === "ready"
    ? { kind: "review_closeout" }
    : { kind: "refresh_work" };
}

function operationTargetsWorkItem(
  operation: ProjectOperationsBriefItem,
  workItemID: string,
): boolean {
  return (
    (operation.action?.type === "open_work_item" ||
      operation.action?.type === "open_assignment_preflight") &&
    operation.action.work_item_id?.trim() === workItemID
  );
}

export function projectWorkItemOperationTargetAvailable(
  operation: ProjectOperationsBriefItem | null,
  records: {
    artifacts: ProjectCollaborationArtifactRecord[];
    assignments: ProjectAssignmentRecord[];
    handoffs: ProjectHandoffRecord[];
  },
): boolean {
  if (!operation?.action) return true;
  if (operation.action.type === "open_assignment_preflight") {
    const assignmentID = operation.action.assignment_id?.trim() || "";
    const assignment = records.assignments.find((record) => record.id === assignmentID);
    if (!assignment) return false;
    const status = toProjectAssignmentExecutionViewModel(assignment).status;
    return (
      (assignment.driver_kind === "hecate_task" || assignment.driver_kind === "external_agent") &&
      status === "queued"
    );
  }
  const artifactID = operation.action.artifact_id?.trim() || "";
  if (artifactID) return records.artifacts.some((artifact) => artifact.id === artifactID);
  const handoffID = operation.action.handoff_id?.trim() || "";
  if (handoffID) return records.handoffs.some((handoff) => handoff.id === handoffID);
  const assignmentID = operation.action.assignment_id?.trim() || "";
  if (assignmentID) {
    return records.assignments.some((assignment) => assignment.id === assignmentID);
  }
  return true;
}

function projectWorkItemFollowThroughCopy(
  closeout: ProjectWorkItemReadinessRecord,
  operation: ProjectOperationsBriefItem | null,
  workItem: Pick<ProjectWorkItemRecord, "id" | "status">,
  targetAvailable: boolean,
): { title: string; detail: string } {
  if (closeout.status === "done" || workItemClosed(workItem.status)) {
    return {
      title: "Work closed",
      detail:
        workItem.status === "cancelled"
          ? "This work item was cancelled. Its assignments, reviews, evidence, and handoffs remain available for inspection."
          : "This work item is complete. Its assignments, reviews, evidence, and handoffs remain available for inspection.",
    };
  }
  if (
    projectWorkItemFollowThroughIntent(closeout, operation, workItem, targetAvailable)?.kind ===
    "refresh_work"
  ) {
    return {
      title: "Next action unavailable",
      detail: "Project work changed. Refresh project work before continuing.",
    };
  }
  if (operation) {
    return {
      title: operation.title,
      detail: operation.detail,
    };
  }
  if (closeout.status === "ready") {
    return {
      title: "Ready for closeout",
      detail:
        "No next action is currently highlighted for this work item. Closeout checks remain available below.",
    };
  }
  return {
    title: "Review closeout checks",
    detail:
      closeout.detail ||
      "Review the assignment timeline and closeout checks below before choosing a follow-through.",
  };
}

function workItemClosed(status: string): boolean {
  return status.trim() === "done" || status.trim() === "cancelled";
}

function projectWorkItemFollowThroughLabel(
  intent: ProjectWorkItemFollowThroughIntent,
  operation: ProjectOperationsBriefItem | null,
): string {
  switch (intent.kind) {
    case "record_evidence":
      return "Record evidence";
    case "refresh_work":
      return "Refresh work";
    case "plan_review_follow_up":
      return "Plan follow-up";
    case "review_handoff":
      return "Review handoff";
    case "review_closeout":
      return "Review closeout";
    case "focus_assignment":
      return operation?.action_label || "Open assignment";
  }
}

function projectWorkItemFollowThroughIcon(
  intent: ProjectWorkItemFollowThroughIntent,
): string | string[] {
  switch (intent.kind) {
    case "record_evidence":
      return Icons.open;
    case "refresh_work":
      return Icons.refresh;
    case "plan_review_follow_up":
      return Icons.tasks;
    case "review_handoff":
      return Icons.chevR;
    case "review_closeout":
      return Icons.check;
    case "focus_assignment":
      return Icons.chevR;
  }
}

function runFollowThroughIntent(
  intent: ProjectWorkItemFollowThroughIntent,
  callbacks: Pick<
    ProjectWorkItemFollowThroughProps,
    | "onFocusAssignment"
    | "onPlanReviewFollowUp"
    | "onRecordEvidence"
    | "onRefresh"
    | "onReviewCloseout"
    | "onReviewHandoff"
  >,
) {
  switch (intent.kind) {
    case "record_evidence":
      callbacks.onRecordEvidence(intent.assignmentID);
      return;
    case "refresh_work":
      callbacks.onRefresh();
      return;
    case "plan_review_follow_up":
      callbacks.onPlanReviewFollowUp(intent.artifactID);
      return;
    case "review_handoff":
      callbacks.onReviewHandoff(intent.handoffID);
      return;
    case "review_closeout":
      callbacks.onReviewCloseout();
      return;
    case "focus_assignment":
      callbacks.onFocusAssignment(intent.assignmentID);
  }
}

const followThroughStyle: CSSProperties = {
  alignItems: "center",
  background: "color-mix(in srgb, var(--teal) 7%, var(--bg1))",
  border: "1px solid color-mix(in srgb, var(--teal) 28%, var(--border))",
  borderRadius: 9,
  display: "grid",
  gap: 16,
  gridTemplateColumns: "minmax(0, 1fr) auto",
  padding: "14px 16px",
};

const followThroughCopyStyle: CSSProperties = {
  display: "grid",
  gap: 5,
  minWidth: 0,
};

const followThroughKickerStyle: CSSProperties = {
  alignItems: "center",
  display: "flex",
  flexWrap: "wrap",
  gap: 7,
};

const followThroughTitleStyle: CSSProperties = {
  color: "var(--t0)",
  fontSize: 14,
  fontWeight: 650,
  lineHeight: 1.35,
  margin: 0,
};

const followThroughDetailStyle: CSSProperties = {
  color: "var(--t2)",
  fontSize: 12,
  lineHeight: 1.5,
  margin: 0,
};
