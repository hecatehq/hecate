import type {
  CreateProjectHandoffPayload,
  ProjectActivityItemRecord,
  ProjectAssignmentExecutionRefRecord,
  ProjectAssignmentRecord,
  ProjectHandoffRecord,
  ProjectWorkRoleRecord,
  UpdateProjectAssignmentPayload,
  UpdateProjectWorkItemPayload,
} from "../../types/project";
import { toProjectAssignmentExecutionViewModel } from "./projectAssignmentViewModels";

export type NewWorkItemForm = {
  title: string;
  brief: string;
  priority: string;
  ownerRoleID: string;
  rootID: string;
};

export type NewAssignmentForm = {
  roleID: string;
  driverKind: string;
  rootID: string;
};

export type EditWorkItemForm = NewWorkItemForm & {
  id: string;
  status: string;
  reviewerRoleIDs: string;
};

export type EditAssignmentForm = NewAssignmentForm & {
  id: string;
  status: string;
  taskID: string;
  runID: string;
  chatSessionID: string;
  messageID: string;
  contextSnapshotID: string;
};

export type HandoffForm = {
  id: string;
  sourceAssignmentID: string;
  sourceRunID: string;
  sourceChatSessionID: string;
  sourceMessageID: string;
  targetRoleID: string;
  targetAssignmentID: string;
  title: string;
  summary: string;
  recommendedNextAction: string;
  linkedArtifactIDs: string;
  linkedMemoryIDs: string;
  contextRefs: string;
  status: string;
  provenanceKind: string;
  trustLabel: string;
};

export const WORK_ITEM_STATUSES = [
  "backlog",
  "ready",
  "running",
  "review",
  "blocked",
  "done",
  "cancelled",
];
export const WORK_ITEM_PRIORITIES = ["low", "normal", "high", "urgent"];
export const ASSIGNMENT_STATUSES = [
  "queued",
  "running",
  "awaiting_approval",
  "completed",
  "failed",
  "cancelled",
];
export const HANDOFF_STATUSES = ["pending", "accepted", "superseded", "dismissed"];

export function defaultDriverForRole(role: ProjectWorkRoleRecord | null): string {
  return role?.default_driver_kind || "hecate_task";
}

export function projectAssignmentExecutionKindFromForm(form: EditAssignmentForm) {
  if (form.taskID.trim() || form.runID.trim()) return "task_run";
  if (form.chatSessionID.trim() || form.messageID.trim()) return "chat_session";
  if (form.contextSnapshotID.trim()) return "context_snapshot";
  return "none";
}

export function projectAssignmentExecutionRefFromForm(
  form: EditAssignmentForm,
): ProjectAssignmentExecutionRefRecord {
  const ref: ProjectAssignmentExecutionRefRecord = {
    kind: projectAssignmentExecutionKindFromForm(form),
  };
  const taskID = form.taskID.trim();
  const runID = form.runID.trim();
  const chatSessionID = form.chatSessionID.trim();
  const messageID = form.messageID.trim();
  const contextSnapshotID = form.contextSnapshotID.trim();
  if (taskID) ref.task_id = taskID;
  if (runID) ref.run_id = runID;
  if (chatSessionID) ref.chat_session_id = chatSessionID;
  if (messageID) ref.message_id = messageID;
  if (contextSnapshotID) ref.context_snapshot_id = contextSnapshotID;
  return ref;
}

export function workItemCreatePayloadFromForm(form: NewWorkItemForm) {
  const rootID = form.rootID.trim();
  return {
    title: form.title.trim(),
    brief: form.brief.trim() || undefined,
    status: "ready",
    priority: form.priority || "normal",
    owner_role_id: form.ownerRoleID || undefined,
    ...(rootID ? { root_id: rootID } : {}),
  };
}

export function workItemUpdatePayloadFromForm(
  form: EditWorkItemForm,
): UpdateProjectWorkItemPayload {
  return {
    title: form.title.trim(),
    brief: form.brief.trim(),
    status: form.status,
    priority: form.priority || "normal",
    owner_role_id: form.ownerRoleID,
    root_id: form.rootID.trim(),
    reviewer_role_ids: splitRoleIDs(form.reviewerRoleIDs),
  };
}

export function assignmentCreatePayloadFromForm(form: NewAssignmentForm) {
  const rootID = form.rootID.trim();
  return {
    role_id: form.roleID.trim(),
    driver_kind: form.driverKind || "hecate_task",
    ...(rootID ? { root_id: rootID } : {}),
  };
}

export function assignmentUpdatePayloadFromForm(
  form: EditAssignmentForm,
): UpdateProjectAssignmentPayload {
  return {
    role_id: form.roleID.trim(),
    root_id: form.rootID.trim(),
    driver_kind: form.driverKind || "hecate_task",
    status: form.status || "queued",
    execution_ref: projectAssignmentExecutionRefFromForm(form),
  };
}

export function handoffPayloadFromForm(form: HandoffForm): CreateProjectHandoffPayload {
  return {
    source_assignment_id: form.sourceAssignmentID.trim(),
    source_run_id: form.sourceRunID.trim(),
    source_chat_session_id: form.sourceChatSessionID.trim(),
    source_message_id: form.sourceMessageID.trim(),
    target_role_id: form.targetRoleID.trim(),
    target_assignment_id: form.targetAssignmentID.trim(),
    title: form.title.trim(),
    summary: form.summary.trim(),
    recommended_next_action: form.recommendedNextAction.trim(),
    linked_artifact_ids: splitIDs(form.linkedArtifactIDs),
    linked_memory_ids: splitIDs(form.linkedMemoryIDs),
    context_refs: splitIDs(form.contextRefs),
    status: form.status || "pending",
    provenance_kind: form.provenanceKind.trim() || "operator",
    trust_label: form.trustLabel.trim() || "operator_reviewed",
  };
}

export function handoffFormFromRecord(handoff: ProjectHandoffRecord | null): HandoffForm {
  return {
    id: handoff?.id ?? "",
    sourceAssignmentID: handoff?.source_assignment_id ?? "",
    sourceRunID: handoff?.source_run_id ?? "",
    sourceChatSessionID: handoff?.source_chat_session_id ?? "",
    sourceMessageID: handoff?.source_message_id ?? "",
    targetRoleID: handoff?.target_role_id ?? "",
    targetAssignmentID: handoff?.target_assignment_id ?? "",
    title: handoff?.title ?? "",
    summary: handoff?.summary ?? "",
    recommendedNextAction: handoff?.recommended_next_action ?? "",
    linkedArtifactIDs: (handoff?.linked_artifact_ids ?? []).join(", "),
    linkedMemoryIDs: (handoff?.linked_memory_ids ?? []).join(", "),
    contextRefs: (handoff?.context_refs ?? []).join(", "),
    status: handoff?.status ?? "pending",
    provenanceKind: handoff?.provenance_kind ?? "operator",
    trustLabel: handoff?.trust_label ?? "operator_reviewed",
  };
}

export function handoffFormFromAssignment(
  assignment: ProjectAssignmentRecord,
  role: ProjectWorkRoleRecord | null,
  activityItem?: ProjectActivityItemRecord,
): HandoffForm {
  const execution = toProjectAssignmentExecutionViewModel(assignment);
  const sourceChatSessionID = execution.chatSessionID;
  const sourceRunID = execution.runID;
  const sourceMessageID =
    execution.messageID ||
    activityItem?.linked_message_id ||
    activityItem?.linked_chat?.latest_message_id ||
    "";
  const contextRefs = [
    execution.contextSnapshotID,
    execution.taskID,
    sourceRunID,
    sourceChatSessionID,
    sourceMessageID,
  ]
    .filter(Boolean)
    .join(", ");
  return {
    id: "",
    sourceAssignmentID: assignment.id,
    sourceRunID,
    sourceChatSessionID,
    sourceMessageID,
    targetRoleID: "",
    targetAssignmentID: "",
    title: `${role?.name || assignment.role_id} handoff`,
    summary: "",
    recommendedNextAction: "",
    linkedArtifactIDs: "",
    linkedMemoryIDs: "",
    contextRefs,
    status: "pending",
    provenanceKind: "operator",
    trustLabel: "operator_reviewed",
  };
}

export function splitRoleIDs(value: string): string[] {
  return splitIDs(value);
}

export function splitIDs(value: string): string[] {
  return value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}
