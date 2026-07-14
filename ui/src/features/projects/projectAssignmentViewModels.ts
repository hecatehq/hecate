import type { ProjectActivityItemRecord, ProjectAssignmentRecord } from "../../types/project";
import { firstNonEmpty } from "./projectUtils";

export type ProjectAssignmentExecutionKind =
  | "task_run"
  | "chat_session"
  | "context_snapshot"
  | "none"
  | string;

export type ProjectExternalAgentPhase =
  | "queued"
  | "prepared"
  | "working"
  | "needs_review"
  | "completed"
  | "failed"
  | "cancelled"
  | "unlinked"
  | "unknown";

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
  externalAgentPhase: ProjectExternalAgentPhase | null;
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
  const status = firstNonEmpty(ref?.status, assignment.status);
  return {
    kind,
    taskID,
    runID,
    chatSessionID,
    messageID,
    contextSnapshotID,
    status,
    pendingApprovalCount,
    traceID: firstNonEmpty(ref?.trace_id),
    missing,
    hasTaskRun: Boolean(taskID || runID),
    hasChatSession: Boolean(chatSessionID || messageID),
    hasContextSnapshot: Boolean(contextSnapshotID),
    hasAnyLink: Boolean(taskID || runID || chatSessionID || messageID || contextSnapshotID),
    externalAgentPhase:
      assignment.driver_kind === "external_agent"
        ? projectExternalAgentPhase(status, chatSessionID, messageID)
        : null,
  };
}

function projectExternalAgentPhase(
  status: string,
  chatSessionID: string,
  messageID: string,
): ProjectExternalAgentPhase {
  if (status !== "queued" && !chatSessionID) return "unlinked";
  switch (status) {
    case "queued":
      return "queued";
    case "awaiting_approval":
      return "needs_review";
    case "completed":
      return "completed";
    case "failed":
      return "failed";
    case "cancelled":
      return "cancelled";
    case "running":
      return messageID ? "working" : "prepared";
    default:
      return "unknown";
  }
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

export function toProjectAssignmentEvidenceViewModel(
  assignment: ProjectAssignmentRecord,
): ProjectAssignmentEvidenceViewModel {
  const execution = toProjectAssignmentExecutionViewModel(assignment);
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
  if (execution.missing || summary?.missing) {
    addEvidenceWarning(warnings, "Linked runtime record is missing or unavailable.");
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
