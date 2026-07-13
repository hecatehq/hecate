import type { CSSProperties, ReactNode } from "react";

import { formatAbsoluteTime } from "../../lib/format";
import type {
  ProjectAssignmentRecord,
  ProjectRecord,
  ProjectWorkRoleRecord,
} from "../../types/project";
import { Badge, CopyableID, Icon, Icons, InlineError } from "../shared/ui";
import {
  toProjectAssignmentEvidenceViewModel,
  toProjectAssignmentExecutionViewModel,
  type ProjectAssignmentEvidenceViewModel,
} from "./projectAssignmentViewModels";
import { assignmentStatusLabel, projectRootDisplayLabel, projectRootTitle } from "./projectDisplay";

export type ProjectAssignmentExecutionStoryProps = {
  assignment: ProjectAssignmentRecord;
  chatModel: string;
  contextControl: ReactNode;
  error: string;
  onCreateHandoff: () => void;
  onCreateReviewArtifact?: () => void;
  onCreateReviewHandoff?: () => void;
  onDelete: () => void;
  onEdit: () => void;
  onOpenChat?: () => void;
  onOpenTask?: (taskID: string, runID?: string) => void;
  onReviewLaunch: () => void;
  project: ProjectRecord | null;
  promoteCompletionAction?: boolean;
  readinessControl?: ReactNode;
  role?: ProjectWorkRoleRecord;
  starting: boolean;
};

export type ProjectAssignmentExecutionMilestone = {
  at?: string;
  current: boolean;
  detail: string;
  key: "assigned" | "started" | "current" | "finished";
  label: string;
};

type ProjectAssignmentPrimaryAction = {
  ariaLabel?: string;
  disabled?: boolean;
  icon: string;
  key: "launch" | "open-chat" | "open-task" | "record-review" | "request-review";
  label: string;
  onClick: () => void;
};

type ProjectAssignmentFollowThroughAction = {
  ariaLabel: string;
  icon: string;
  key: "handoff" | "record-review" | "request-review";
  label: string;
  onClick: () => void;
};

export function ProjectAssignmentExecutionStory({
  assignment,
  chatModel,
  contextControl,
  error,
  onCreateHandoff,
  onCreateReviewArtifact,
  onCreateReviewHandoff,
  onDelete,
  onEdit,
  onOpenChat,
  onOpenTask,
  onReviewLaunch,
  project,
  promoteCompletionAction = true,
  readinessControl,
  role,
  starting,
}: ProjectAssignmentExecutionStoryProps) {
  const execution = toProjectAssignmentExecutionViewModel(assignment);
  const evidence = toProjectAssignmentEvidenceViewModel(assignment);
  const status = execution.status || assignment.status;
  const destination = projectAssignmentDestinationLabel(assignment.driver_kind);
  const external = assignment.driver_kind === "external_agent";
  const startable = (assignment.driver_kind === "hecate_task" || external) && status === "queued";
  const canOpenTask = Boolean(execution.taskID && onOpenTask);
  const canOpenChat = Boolean(onOpenChat && execution.chatSessionID);
  const canStartRelatedChat = Boolean(
    onOpenChat && !external && !execution.chatSessionID && chatModel,
  );
  const milestones = projectAssignmentExecutionMilestones(assignment);
  const primaryAction = projectAssignmentPrimaryAction({
    assignmentID: assignment.id,
    canOpenChat,
    canOpenTask,
    execution,
    external,
    onCreateReviewArtifact,
    onCreateReviewHandoff,
    onOpenChat,
    onOpenTask,
    onReviewLaunch,
    promoteCompletionAction,
    startable,
    starting,
    status,
  });
  const statusSummary = projectAssignmentStatusSummary(
    assignment,
    destination,
    execution.pendingApprovalCount,
  );
  const primaryKey = primaryAction?.key;
  const followThroughActions: ProjectAssignmentFollowThroughAction[] = [];
  if (execution.hasAnyLink) {
    followThroughActions.push({
      ariaLabel: `Create handoff from assignment ${assignment.id}`,
      key: "handoff",
      label: "Handoff",
      icon: Icons.plus,
      onClick: onCreateHandoff,
    });
  }
  if (execution.hasAnyLink && onCreateReviewHandoff && primaryKey !== "request-review") {
    followThroughActions.push({
      ariaLabel: `Request review for assignment ${assignment.id}`,
      key: "request-review",
      label: "Request review",
      icon: Icons.check,
      onClick: onCreateReviewHandoff,
    });
  }
  if (onCreateReviewArtifact && primaryKey !== "record-review") {
    followThroughActions.push({
      ariaLabel: `Record review for assignment ${assignment.id}`,
      key: "record-review",
      label: "Record review",
      icon: Icons.check,
      onClick: onCreateReviewArtifact,
    });
  }
  const runtimeMissing = Boolean(execution.missing || assignment.execution?.missing);

  return (
    <article
      aria-label={`${role?.name ?? assignment.role_id} assignment execution ${assignment.id}`}
      style={storyStyle}
    >
      <header style={storyHeaderStyle}>
        <div style={storyIdentityStyle}>
          <div style={storyBadgesStyle}>
            <Badge
              status={status}
              label={projectAssignmentStateLabel(status, execution.pendingApprovalCount)}
            />
            <span className="badge badge-muted">{destination}</span>
          </div>
          <h3 style={storyTitleStyle}>{role?.name ?? assignment.role_id}</h3>
          <p
            aria-live={status === "awaiting_approval" ? "polite" : undefined}
            style={storySummaryStyle}
          >
            {statusSummary}
          </p>
        </div>
        {primaryAction && (
          <button
            aria-label={primaryAction.ariaLabel}
            className="btn btn-primary btn-sm"
            type="button"
            onClick={primaryAction.onClick}
            disabled={primaryAction.disabled}
          >
            <Icon d={primaryAction.icon} size={12} />
            {primaryAction.label}
          </button>
        )}
      </header>

      <ol aria-label="Execution milestones" style={milestoneListStyle}>
        {milestones.map((milestone) => (
          <li key={milestone.key} style={milestoneStyle}>
            <span
              aria-hidden="true"
              style={{
                ...milestoneMarkerStyle,
                background: milestone.current ? "var(--teal)" : "var(--bg2)",
                borderColor: milestone.current ? "var(--teal)" : "var(--border-hi)",
              }}
            />
            <div style={milestoneContentStyle}>
              <div style={milestoneHeaderStyle}>
                <span style={milestoneLabelStyle}>{milestone.label}</span>
                {milestone.current && <span style={currentLabelStyle}>Current</span>}
                {milestone.at && (
                  <time dateTime={milestone.at}>{formatAbsoluteTime(milestone.at)}</time>
                )}
              </div>
              <div style={milestoneDetailStyle}>{milestone.detail}</div>
            </div>
          </li>
        ))}
      </ol>

      {runtimeMissing && (
        <div aria-live="polite" role="status" style={attentionStyle}>
          <Icon d={Icons.warning} size={13} />
          <span>The linked runtime record is missing or unavailable.</span>
        </div>
      )}
      {!runtimeMissing &&
        assignment.driver_kind === "hecate_task" &&
        status !== "queued" &&
        !execution.taskID &&
        !execution.chatSessionID && (
          <div aria-live="polite" role="status" style={attentionStyle}>
            <Icon d={Icons.warning} size={13} />
            <span>No linked task or chat is available for this assignment.</span>
          </div>
        )}
      {!runtimeMissing && external && !startable && !execution.chatSessionID && (
        <div aria-live="polite" role="status" style={attentionStyle}>
          <Icon d={Icons.warning} size={13} />
          <span>No prepared External Agent chat is linked to this assignment.</span>
        </div>
      )}
      {assignment.execution?.last_error && (
        <InlineError message={assignment.execution.last_error} />
      )}
      {error && <InlineError message={error} />}

      <details style={detailsStyle}>
        <summary style={detailsSummaryStyle}>Execution details</summary>
        <div style={detailsBodyStyle}>
          <div style={detailsActionsStyle}>
            {canOpenTask && primaryKey !== "open-task" && (
              <button
                className="btn btn-ghost btn-sm"
                type="button"
                onClick={() => onOpenTask?.(execution.taskID, execution.runID)}
              >
                <Icon d={Icons.tasks} size={12} />
                Open task
              </button>
            )}
            {canOpenChat && primaryKey !== "open-chat" && (
              <button className="btn btn-ghost btn-sm" type="button" onClick={onOpenChat}>
                <Icon d={Icons.chat} size={12} />
                Open chat
              </button>
            )}
            {canStartRelatedChat && (
              <button className="btn btn-ghost btn-sm" type="button" onClick={onOpenChat}>
                <Icon d={Icons.chat} size={12} />
                Start related chat
              </button>
            )}
            {contextControl}
            <button
              className="btn btn-ghost btn-sm"
              type="button"
              aria-label={`Edit assignment ${assignment.id}`}
              onClick={onEdit}
            >
              <Icon d={Icons.edit} size={12} />
              Edit
            </button>
            <button
              className="btn btn-ghost btn-sm"
              type="button"
              aria-label={`Delete assignment ${assignment.id}`}
              onClick={onDelete}
              style={{ color: "var(--red)" }}
            >
              <Icon d={Icons.trash} size={12} />
              Delete
            </button>
          </div>
          <div style={detailsMetadataStyle}>
            {execution.taskID && <CopyableID text={execution.taskID} compact />}
            {execution.runID && <CopyableID text={execution.runID} compact />}
            {execution.chatSessionID && <CopyableID text={execution.chatSessionID} compact />}
            {typeof assignment.execution?.step_count === "number" && (
              <span className="badge badge-muted">
                {projectCountLabel(assignment.execution.step_count, "step")}
              </span>
            )}
            {typeof assignment.execution?.artifact_count === "number" && (
              <span className="badge badge-muted">
                {projectCountLabel(assignment.execution.artifact_count, "artifact")}
              </span>
            )}
            {assignment.execution?.provider || assignment.execution?.model ? (
              <span className="badge badge-muted">
                {[assignment.execution.provider, assignment.execution.model]
                  .filter(Boolean)
                  .join(" / ")}
              </span>
            ) : null}
            {assignment.root_id && project && (
              <span
                className="badge badge-muted"
                title={projectRootTitle(project, assignment.root_id)}
              >
                root {projectRootDisplayLabel(project, assignment.root_id)}
              </span>
            )}
          </div>
          {readinessControl}
          {evidence.hasEvidence && <ProjectAssignmentExecutionEvidence evidence={evidence} />}
        </div>
      </details>

      {followThroughActions.length > 0 && (
        <div aria-label="Follow through" role="group" style={followThroughStyle}>
          <span style={followThroughLabelStyle}>Follow through</span>
          {followThroughActions.map((action) => (
            <button
              key={action.key}
              aria-label={action.ariaLabel}
              className="btn btn-ghost btn-sm"
              type="button"
              onClick={action.onClick}
            >
              <Icon d={action.icon} size={12} />
              {action.label}
            </button>
          ))}
        </div>
      )}
    </article>
  );
}

export function projectAssignmentDestinationLabel(driverKind: string): string {
  switch (driverKind) {
    case "hecate_task":
      return "Hecate Task";
    case "external_agent":
      return "External Agent";
    default:
      return driverKind || "Unspecified destination";
  }
}

export function projectAssignmentExecutionMilestones(
  assignment: ProjectAssignmentRecord,
): ProjectAssignmentExecutionMilestone[] {
  const execution = toProjectAssignmentExecutionViewModel(assignment);
  const status = execution.status || assignment.status;
  const startedAt = assignment.execution?.started_at || assignment.started_at;
  const finishedAt = assignment.execution?.finished_at || assignment.completed_at;
  const terminal = status === "completed" || status === "failed" || status === "cancelled";
  const milestones: ProjectAssignmentExecutionMilestone[] = [
    {
      at: assignment.created_at,
      current: status === "queued",
      detail: status === "queued" ? "Waiting for launch review." : "Assignment recorded.",
      key: "assigned",
      label: "Assigned",
    },
  ];
  if (startedAt) {
    milestones.push({
      at: startedAt,
      current: false,
      detail: "Execution began.",
      key: "started",
      label: "Started",
    });
  }
  if (terminal && finishedAt) {
    milestones.push({
      at: finishedAt,
      current: true,
      detail: projectAssignmentTerminalDetail(status),
      key: "finished",
      label: "Finished",
    });
  } else if (status !== "queued") {
    milestones.push({
      current: true,
      detail: projectAssignmentCurrentMilestoneDetail(status, execution.pendingApprovalCount),
      key: "current",
      label: projectAssignmentStateLabel(status, execution.pendingApprovalCount),
    });
  }
  return milestones;
}

function projectAssignmentPrimaryAction({
  assignmentID,
  canOpenChat,
  canOpenTask,
  execution,
  external,
  onCreateReviewArtifact,
  onCreateReviewHandoff,
  onOpenChat,
  onOpenTask,
  onReviewLaunch,
  promoteCompletionAction,
  startable,
  starting,
  status,
}: {
  assignmentID: string;
  canOpenChat: boolean;
  canOpenTask: boolean;
  execution: ReturnType<typeof toProjectAssignmentExecutionViewModel>;
  external: boolean;
  onCreateReviewArtifact?: () => void;
  onCreateReviewHandoff?: () => void;
  onOpenChat?: () => void;
  onOpenTask?: (taskID: string, runID?: string) => void;
  onReviewLaunch: () => void;
  promoteCompletionAction: boolean;
  startable: boolean;
  starting: boolean;
  status: string;
}): ProjectAssignmentPrimaryAction | null {
  if (startable) {
    return {
      disabled: starting,
      icon: external ? Icons.chat : Icons.send,
      key: "launch",
      label: starting
        ? external
          ? "Preparing…"
          : "Starting…"
        : external
          ? "Review & prepare chat"
          : "Review & start",
      onClick: onReviewLaunch,
    };
  }
  if (status === "completed") {
    if (!promoteCompletionAction) return null;
    if (onCreateReviewArtifact) {
      return {
        ariaLabel: `Record review for assignment ${assignmentID}`,
        icon: Icons.check,
        key: "record-review",
        label: "Record review",
        onClick: onCreateReviewArtifact,
      };
    }
    if (execution.hasAnyLink && onCreateReviewHandoff) {
      return {
        ariaLabel: `Request review for assignment ${assignmentID}`,
        icon: Icons.check,
        key: "request-review",
        label: "Request review",
        onClick: onCreateReviewHandoff,
      };
    }
  }
  if (external && canOpenChat && onOpenChat) {
    return {
      icon: Icons.chat,
      key: "open-chat",
      label: status === "failed" || status === "cancelled" ? "Inspect chat" : "Open chat",
      onClick: onOpenChat,
    };
  }
  if (canOpenTask && onOpenTask) {
    const label =
      status === "awaiting_approval"
        ? execution.pendingApprovalCount > 0
          ? "Review in task"
          : "Review task"
        : status === "failed" || status === "cancelled"
          ? "Inspect task"
          : "Open task";
    return {
      icon: Icons.tasks,
      key: "open-task",
      label,
      onClick: () => onOpenTask(execution.taskID, execution.runID),
    };
  }
  if (canOpenChat && onOpenChat) {
    return {
      icon: Icons.chat,
      key: "open-chat",
      label: status === "failed" || status === "cancelled" ? "Inspect chat" : "Open chat",
      onClick: onOpenChat,
    };
  }
  return null;
}

function projectAssignmentStatusSummary(
  assignment: ProjectAssignmentRecord,
  destination: string,
  pendingApprovalCount: number,
): string {
  const execution = toProjectAssignmentExecutionViewModel(assignment);
  const status = execution.status || assignment.status;
  switch (status) {
    case "queued":
      return `Review launch context before starting this ${destination}.`;
    case "running":
      return `${destination} is running.`;
    case "awaiting_approval":
      return pendingApprovalCount > 0
        ? `${pendingApprovalCount} approval${pendingApprovalCount === 1 ? "" : "s"} ${pendingApprovalCount === 1 ? "needs" : "need"} operator review.`
        : "Assignment needs operator review.";
    case "failed":
      return "Execution failed. Inspect the linked runtime.";
    case "cancelled":
      return "Execution was cancelled.";
    case "completed":
      return "Execution completed. Review the outcome and choose the follow-through.";
    default:
      return `Current state: ${assignmentStatusLabel(status)}.`;
  }
}

function projectAssignmentCurrentMilestoneDetail(
  status: string,
  pendingApprovalCount: number,
): string {
  switch (status) {
    case "running":
      return "Execution is in progress.";
    case "awaiting_approval":
      return pendingApprovalCount > 0
        ? "Execution is paused."
        : "Assignment is waiting for review.";
    case "failed":
      return "Execution is currently failed; no finish time was recorded.";
    case "cancelled":
      return "Execution is currently cancelled; no finish time was recorded.";
    case "completed":
      return "Execution is currently marked complete; no finish time was recorded.";
    default:
      return `Current assignment state: ${assignmentStatusLabel(status)}.`;
  }
}

function projectAssignmentTerminalDetail(status: string): string {
  switch (status) {
    case "completed":
      return "Execution finished successfully.";
    case "failed":
      return "Execution finished with a failure.";
    case "cancelled":
      return "Execution was cancelled.";
    default:
      return `Execution finished with status ${assignmentStatusLabel(status)}.`;
  }
}

function projectAssignmentStateLabel(status: string, pendingApprovalCount: number): string {
  if (status === "awaiting_approval") {
    return pendingApprovalCount > 0 ? "approval" : "review";
  }
  return assignmentStatusLabel(status);
}

function projectCountLabel(count: number, noun: string): string {
  return `${count} ${noun}${count === 1 ? "" : "s"}`;
}

function ProjectAssignmentExecutionEvidence({
  evidence,
}: {
  evidence: ProjectAssignmentEvidenceViewModel;
}) {
  if (!evidence.hasEvidence) return null;
  return (
    <section aria-label="Execution evidence" style={evidenceStyle}>
      <div style={sectionLabelStyle}>Canonical evidence</div>
      {evidence.items.length > 0 && (
        <div style={evidenceGridStyle}>
          {evidence.items.map((item) => (
            <div key={item.key} style={evidenceItemStyle}>
              <span style={evidenceLabelStyle}>{item.label}</span>
              <span style={evidenceValueStyle}>{item.value}</span>
            </div>
          ))}
        </div>
      )}
      {evidence.warnings.length > 0 && (
        <div style={evidenceWarningStyle}>
          {evidence.warnings.map((warning) => (
            <div key={warning}>{warning}</div>
          ))}
        </div>
      )}
    </section>
  );
}

const storyStyle: CSSProperties = {
  background: "var(--bg1)",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  display: "grid",
  gap: 12,
  minWidth: 0,
  padding: 12,
};

const storyHeaderStyle: CSSProperties = {
  alignItems: "flex-start",
  display: "flex",
  flexWrap: "wrap",
  gap: 12,
  justifyContent: "space-between",
  minWidth: 0,
};

const storyIdentityStyle: CSSProperties = { flex: "1 1 320px", minWidth: 0 };

const storyBadgesStyle: CSSProperties = {
  alignItems: "center",
  display: "flex",
  flexWrap: "wrap",
  gap: 6,
};

const storyTitleStyle: CSSProperties = {
  color: "var(--t0)",
  fontSize: 14,
  fontWeight: 650,
  lineHeight: 1.3,
  margin: "8px 0 0",
  overflowWrap: "anywhere",
};

const storySummaryStyle: CSSProperties = {
  color: "var(--t2)",
  fontSize: 12,
  lineHeight: 1.45,
  margin: "5px 0 0",
  maxWidth: 700,
  overflowWrap: "anywhere",
};

const milestoneListStyle: CSSProperties = {
  borderLeft: "1px solid var(--border-hi)",
  display: "grid",
  gap: 10,
  listStyle: "none",
  margin: "0 0 0 5px",
  padding: "0 0 0 18px",
};

const milestoneStyle: CSSProperties = { minWidth: 0, position: "relative" };

const milestoneMarkerStyle: CSSProperties = {
  border: "2px solid",
  borderRadius: "50%",
  height: 9,
  left: -24,
  position: "absolute",
  top: 3,
  width: 9,
};

const milestoneContentStyle: CSSProperties = { display: "grid", gap: 3, minWidth: 0 };

const milestoneHeaderStyle: CSSProperties = {
  alignItems: "baseline",
  color: "var(--t2)",
  display: "flex",
  flexWrap: "wrap",
  fontSize: 10,
  gap: 7,
};

const milestoneLabelStyle: CSSProperties = {
  color: "var(--t1)",
  fontSize: 12,
  fontWeight: 600,
  overflowWrap: "anywhere",
  textTransform: "capitalize",
};

const currentLabelStyle: CSSProperties = {
  color: "var(--teal)",
  fontFamily: "var(--font-mono)",
  fontSize: 9,
  letterSpacing: "0.06em",
  textTransform: "uppercase",
};

const milestoneDetailStyle: CSSProperties = {
  color: "var(--t2)",
  fontSize: 11,
  lineHeight: 1.4,
  overflowWrap: "anywhere",
};

const attentionStyle: CSSProperties = {
  alignItems: "flex-start",
  background: "var(--amber-bg)",
  border: "1px solid var(--amber-border)",
  borderRadius: "var(--radius-sm)",
  color: "var(--amber-lo)",
  display: "flex",
  fontSize: 12,
  gap: 8,
  lineHeight: 1.4,
  overflowWrap: "anywhere",
  padding: "8px 10px",
};

const detailsStyle: CSSProperties = {
  borderTop: "1px solid var(--border)",
  color: "var(--t2)",
  paddingTop: 10,
};

const detailsSummaryStyle: CSSProperties = {
  color: "var(--t2)",
  cursor: "pointer",
  fontFamily: "var(--font-mono)",
  fontSize: 10,
  letterSpacing: "0.04em",
  textTransform: "uppercase",
};

const detailsBodyStyle: CSSProperties = { display: "grid", gap: 10, paddingTop: 10 };

const detailsActionsStyle: CSSProperties = {
  alignItems: "center",
  display: "flex",
  flexWrap: "wrap",
  gap: 6,
};

const detailsMetadataStyle: CSSProperties = {
  alignItems: "center",
  display: "flex",
  flexWrap: "wrap",
  gap: 7,
  minWidth: 0,
};

const followThroughStyle: CSSProperties = {
  alignItems: "center",
  borderTop: "1px solid var(--border)",
  display: "flex",
  flexWrap: "wrap",
  gap: 6,
  paddingTop: 10,
};

const followThroughLabelStyle: CSSProperties = {
  color: "var(--t2)",
  fontFamily: "var(--font-mono)",
  fontSize: 9,
  letterSpacing: "0.05em",
  marginRight: 2,
  textTransform: "uppercase",
};

const sectionLabelStyle: CSSProperties = {
  color: "var(--teal)",
  fontFamily: "var(--font-mono)",
  fontSize: 9,
  letterSpacing: "0.06em",
  textTransform: "uppercase",
};

const evidenceStyle: CSSProperties = {
  background: "var(--bg2)",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  display: "grid",
  gap: 9,
  minWidth: 0,
  padding: "10px 11px",
};

const evidenceGridStyle: CSSProperties = {
  display: "grid",
  gap: "8px 14px",
  gridTemplateColumns: "repeat(auto-fit, minmax(145px, 1fr))",
  minWidth: 0,
};

const evidenceItemStyle: CSSProperties = { display: "grid", gap: 3, minWidth: 0 };

const evidenceLabelStyle: CSSProperties = {
  color: "var(--t2)",
  fontFamily: "var(--font-mono)",
  fontSize: 9,
  textTransform: "uppercase",
};

const evidenceValueStyle: CSSProperties = {
  color: "var(--t1)",
  fontFamily: "var(--font-mono)",
  fontSize: 10,
  overflowWrap: "anywhere",
};

const evidenceWarningStyle: CSSProperties = {
  color: "var(--amber)",
  display: "grid",
  fontSize: 11,
  gap: 3,
  lineHeight: 1.4,
  overflowWrap: "anywhere",
};
