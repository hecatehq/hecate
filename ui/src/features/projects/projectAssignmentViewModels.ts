import type { ProjectActivityItemRecord, ProjectAssignmentRecord } from "../../types/project";

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

function firstNonEmpty(...values: Array<string | undefined | null>): string {
  for (const value of values) {
    const trimmed = value?.trim();
    if (trimmed) return trimmed;
  }
  return "";
}
