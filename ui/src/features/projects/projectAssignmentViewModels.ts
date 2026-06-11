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
  const execution = assignment.execution;
  const ref = assignment.execution_ref;
  const taskID = firstNonEmpty(linked.taskID, ref?.task_id, execution?.task_id, assignment.task_id);
  const runID = firstNonEmpty(linked.runID, ref?.run_id, execution?.run_id, assignment.run_id);
  const chatSessionID = firstNonEmpty(
    linked.chatSessionID,
    ref?.chat_session_id,
    assignment.chat_session_id,
  );
  const messageID = firstNonEmpty(linked.messageID, ref?.message_id, assignment.message_id);
  const contextSnapshotID = firstNonEmpty(ref?.context_snapshot_id, assignment.context_snapshot_id);
  const kind = ref?.kind || inferExecutionKind({ taskID, runID, chatSessionID, contextSnapshotID });
  const pendingApprovalCount =
    ref?.pending_approval_count ?? execution?.pending_approval_count ?? 0;
  const missing = ref?.missing ?? execution?.missing ?? false;
  return {
    kind,
    taskID,
    runID,
    chatSessionID,
    messageID,
    contextSnapshotID,
    status: firstNonEmpty(ref?.status, execution?.status, assignment.status),
    pendingApprovalCount,
    traceID: firstNonEmpty(ref?.trace_id, execution?.trace_id),
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

function inferExecutionKind({
  chatSessionID,
  contextSnapshotID,
  runID,
  taskID,
}: {
  chatSessionID: string;
  contextSnapshotID: string;
  runID: string;
  taskID: string;
}): ProjectAssignmentExecutionKind {
  if (taskID || runID) return "task_run";
  if (chatSessionID) return "chat_session";
  if (contextSnapshotID) return "context_snapshot";
  return "none";
}

function firstNonEmpty(...values: Array<string | undefined | null>): string {
  for (const value of values) {
    const trimmed = value?.trim();
    if (trimmed) return trimmed;
  }
  return "";
}
