import type { ProjectActivityItemRecord, ProjectAssignmentRecord } from "../../types/project";
import { firstNonEmpty } from "./projectUtils";

export type ProjectAssignmentExecutionKind =
  | "task_run"
  | "chat_session"
  | "context_snapshot"
  | "none"
  | string;

export type ProjectAssignmentExecutionViewModel = {
  kind: ProjectAssignmentExecutionKind;
  taskID: string;
  runID: string;
  chatSessionID: string;
  messageID: string;
  contextSnapshotID: string;
  status: string;
  pendingApprovalCount: number;
  traceID: string;
  missing: boolean;
  hasTaskRun: boolean;
  hasChatSession: boolean;
  hasContextSnapshot: boolean;
  hasAnyLink: boolean;
};

export type ProjectActivityItemViewModel = {
  execution: ProjectAssignmentExecutionViewModel;
  status: string;
  blockingSignal: string;
  statusSummary: string;
  bucket: "active" | "blocked" | "completed";
  startedAt: string;
  finishedAt: string;
};

export type ProjectAssignmentEvidenceItem = {
  key: string;
  label: string;
  value: string;
};

export type ProjectAssignmentEvidenceViewModel = {
  items: ProjectAssignmentEvidenceItem[];
  warnings: string[];
  hasEvidence: boolean;
};

type LinkedExecutionOverrides = {
  taskID?: string;
  runID?: string;
  chatSessionID?: string;
  messageID?: string;
};

export function toProjectAssignmentExecutionViewModel(
  assignment: ProjectAssignmentRecord,
  linked: LinkedExecutionOverrides = {},
): ProjectAssignmentExecutionViewModel {
  const ref = assignment.execution_ref;
  const taskID = firstNonEmpty(linked.taskID, ref?.task_id);
  const runID = firstNonEmpty(linked.runID, ref?.run_id);
  const chatSessionID = firstNonEmpty(linked.chatSessionID, ref?.chat_session_id);
  const messageID = firstNonEmpty(linked.messageID, ref?.message_id);
  const contextSnapshotID = firstNonEmpty(ref?.context_snapshot_id);
  const kind = ref?.kind || "none";
  const pendingApprovalCount = ref?.pending_approval_count ?? 0;
  const missing = ref?.missing ?? false;
  return {
    kind,
    taskID,
    runID,
    chatSessionID,
    messageID,
    contextSnapshotID,
    status: firstNonEmpty(ref?.status, assignment.status),
    pendingApprovalCount,
    traceID: firstNonEmpty(ref?.trace_id),
    missing,
    hasTaskRun: Boolean(taskID || runID),
    hasChatSession: Boolean(chatSessionID || messageID),
    hasContextSnapshot: Boolean(contextSnapshotID),
    hasAnyLink: Boolean(taskID || runID || chatSessionID || messageID || contextSnapshotID),
  };
}

export function toProjectActivityAssignmentExecutionViewModel(
  item: ProjectActivityItemRecord,
): ProjectAssignmentExecutionViewModel {
  return toProjectAssignmentExecutionViewModel(item.assignment, {
    taskID: item.linked_task_id,
    runID: item.linked_run_id,
    chatSessionID: item.linked_chat_id,
    messageID: item.linked_message_id,
  });
}

export function projectActivityMatchesAssignmentVersion(
  assignment: ProjectAssignmentRecord,
  activityItem?: ProjectActivityItemRecord,
): boolean {
  if (!activityItem) return false;
  return (
    activityItem.assignment.id === assignment.id &&
    activityItem.assignment.project_id === assignment.project_id &&
    activityItem.assignment.work_item_id === assignment.work_item_id &&
    activityItem.assignment.updated_at === assignment.updated_at
  );
}

export function toProjectActivityItemViewModel(
  item: ProjectActivityItemRecord,
): ProjectActivityItemViewModel {
  const execution = toProjectActivityAssignmentExecutionViewModel(item);
  const blockingSignal = firstNonEmpty(item.blocking_signal, "running");
  return {
    execution,
    status: firstNonEmpty(item.status, execution.status, item.assignment.status, "unknown"),
    blockingSignal,
    statusSummary: firstNonEmpty(item.status_summary),
    bucket: activityBucket(blockingSignal),
    startedAt: firstNonEmpty(item.assignment.execution?.started_at, item.assignment.started_at),
    finishedAt: firstNonEmpty(item.assignment.execution?.finished_at, item.assignment.completed_at),
  };
}

export function toProjectAssignmentEvidenceViewModel(
  assignment: ProjectAssignmentRecord,
  activityItem?: ProjectActivityItemRecord,
): ProjectAssignmentEvidenceViewModel {
  const assignmentExecution = toProjectAssignmentExecutionViewModel(assignment);
  const activityView = activityItem ? toProjectActivityItemViewModel(activityItem) : null;
  const execution =
    assignmentExecution.hasAnyLink || !activityView ? assignmentExecution : activityView.execution;
  const summary = assignment.execution;
  const items: ProjectAssignmentEvidenceItem[] = [];

  pushEvidenceItem(items, "kind", "Kind", execution.kind === "none" ? "" : execution.kind);
  pushEvidenceItem(items, "status", "Status", firstNonEmpty(execution.status, assignment.status));
  pushEvidenceItem(items, "task", "Task", execution.taskID);
  pushEvidenceItem(items, "run", "Run", execution.runID);
  pushEvidenceItem(items, "chat", "Chat", execution.chatSessionID);
  pushEvidenceItem(items, "message", "Message", execution.messageID);
  pushEvidenceItem(items, "context", "Context snapshot", execution.contextSnapshotID);
  pushEvidenceItem(items, "trace", "Trace", execution.traceID);
  if (activityItem?.linked_chat) {
    pushEvidenceItem(
      items,
      "agent_implementation",
      "Agent implementation",
      [activityItem.linked_chat.agent_title, activityItem.linked_chat.agent_version]
        .filter(Boolean)
        .join(" "),
    );
    pushEvidenceItem(
      items,
      "available_commands",
      "Commands",
      typeof activityItem.linked_chat.available_command_count === "number"
        ? String(activityItem.linked_chat.available_command_count)
        : "",
    );
  }
  pushEvidenceItem(
    items,
    "provider_model",
    "Provider / model",
    [summary?.provider, summary?.model].filter(Boolean).join(" / "),
  );
  pushEvidenceItem(
    items,
    "steps",
    "Steps",
    typeof summary?.step_count === "number" ? String(summary.step_count) : "",
  );
  pushEvidenceItem(
    items,
    "artifacts",
    "Artifacts",
    typeof summary?.artifact_count === "number" ? String(summary.artifact_count) : "",
  );
  const warnings: string[] = [];
  if (execution.pendingApprovalCount > 0) {
    addEvidenceWarning(warnings, `${execution.pendingApprovalCount} approval pending`);
  }
  if (execution.missing || summary?.missing || activityItem?.linked_chat?.missing) {
    addEvidenceWarning(warnings, "Linked runtime record is missing or unavailable.");
  }
  if (activityItem?.linked_chat?.latest_error) {
    addEvidenceWarning(warnings, activityItem.linked_chat.latest_error);
  }
  if (!execution.hasAnyLink && assignment.status !== "queued") {
    addEvidenceWarning(warnings, "No canonical execution refs are stored for this assignment.");
  }
  if (summary?.last_error) {
    addEvidenceWarning(warnings, summary.last_error);
  }

  const hasEvidence = items.some((item) => item.key !== "status") || warnings.length > 0;

  return {
    items: hasEvidence ? items : [],
    warnings,
    hasEvidence,
  };
}

function activityBucket(signal: string): ProjectActivityItemViewModel["bucket"] {
  switch (signal) {
    case "awaiting_approval":
    case "failed":
    case "cancelled":
    case "not_started":
    case "stale_unknown":
      return "blocked";
    case "completed":
      return "completed";
    default:
      return "active";
  }
}

function pushEvidenceItem(
  items: ProjectAssignmentEvidenceItem[],
  key: string,
  label: string,
  value: string,
) {
  const trimmed = value.trim();
  if (!trimmed) return;
  items.push({ key, label, value: trimmed });
}

function addEvidenceWarning(warnings: string[], value: string) {
  const trimmed = value.trim();
  if (!trimmed || warnings.includes(trimmed)) return;
  warnings.push(trimmed);
}
