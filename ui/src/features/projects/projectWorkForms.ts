import type {
  CreateProjectCollaborationArtifactPayload,
  CreateProjectHandoffPayload,
  ProjectActivityItemRecord,
  ProjectAssignmentExecutionRefRecord,
  ProjectCollaborationArtifactRecord,
  ProjectAssignmentRecord,
  ProjectHandoffRecord,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
  UpdateProjectAssignmentPayload,
  UpdateProjectWorkItemPayload,
} from "../../types/project";
import {
  toProjectActivityAssignmentExecutionViewModel,
  toProjectAssignmentExecutionViewModel,
  type ProjectAssignmentExecutionViewModel,
} from "./projectAssignmentViewModels";
import { firstNonEmpty, splitIDs, splitRoleIDs } from "./projectUtils";

export const WORK_ITEM_STATUSES = [
  "backlog",
  "ready",
  "running",
  "review",
  "blocked",
  "done",
  "cancelled",
] as const;
export type WorkItemStatus = (typeof WORK_ITEM_STATUSES)[number];

export const WORK_ITEM_PRIORITIES = ["low", "normal", "high", "urgent"] as const;
export type WorkItemPriority = (typeof WORK_ITEM_PRIORITIES)[number];

export const ASSIGNMENT_STATUSES = [
  "queued",
  "running",
  "awaiting_approval",
  "completed",
  "failed",
  "cancelled",
] as const;
export type AssignmentStatus = (typeof ASSIGNMENT_STATUSES)[number];

export const HANDOFF_STATUSES = ["pending", "accepted", "superseded", "dismissed"] as const;
export type HandoffStatus = (typeof HANDOFF_STATUSES)[number];

// Keep these values in sync with internal/projectwork review constants; the
// server rejects review artifacts that use values outside its enum set.
export const REVIEW_VERDICTS = ["approved", "changes_requested", "blocked", "risk"] as const;
export type ReviewVerdict = (typeof REVIEW_VERDICTS)[number];

export const REVIEW_RISKS = ["low", "medium", "high", "unknown"] as const;
export type ReviewRisk = (typeof REVIEW_RISKS)[number];

export type NewWorkItemForm = {
  title: string;
  brief: string;
  priority: WorkItemPriority;
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
  status: WorkItemStatus;
  reviewerRoleIDs: string;
};

export type EditAssignmentForm = NewAssignmentForm & {
  id: string;
  status: AssignmentStatus;
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
  status: HandoffStatus;
  provenanceKind: string;
  trustLabel: string;
};

export type ReviewArtifactForm = {
  assignmentID: string;
  reviewedAssignmentID: string;
  authorRoleID: string;
  title: string;
  verdict: ReviewVerdict;
  risk: ReviewRisk;
  summary: string;
  verification: string;
  followUp: string;
};

export type EvidenceLinkForm = {
  assignmentID: string;
  title: string;
  sourceKind: string;
  url: string;
  externalID: string;
  provider: string;
  trustLabel: string;
  summary: string;
};

export function defaultDriverForRole(role: ProjectWorkRoleRecord | null): string {
  return role?.default_driver_kind || "hecate_task";
}

export function workItemStatusFromValue(value: string | undefined | null): WorkItemStatus {
  return choiceFromValue(WORK_ITEM_STATUSES, value, "ready");
}

export function workItemPriorityFromValue(value: string | undefined | null): WorkItemPriority {
  return choiceFromValue(WORK_ITEM_PRIORITIES, value, "normal");
}

export function assignmentStatusFromValue(value: string | undefined | null): AssignmentStatus {
  return choiceFromValue(ASSIGNMENT_STATUSES, value, "queued");
}

export function handoffStatusFromValue(value: string | undefined | null): HandoffStatus {
  return choiceFromValue(HANDOFF_STATUSES, value, "pending");
}

export function reviewVerdictFromValue(value: string | undefined | null): ReviewVerdict {
  return choiceFromValue(REVIEW_VERDICTS, value, "approved");
}

export function reviewRiskFromValue(value: string | undefined | null): ReviewRisk {
  return choiceFromValue(REVIEW_RISKS, value, "unknown");
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
    priority: form.priority,
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
    priority: form.priority,
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
  const executionRef = projectAssignmentExecutionRefFromForm(form);
  return {
    role_id: form.roleID.trim(),
    root_id: form.rootID.trim(),
    driver_kind: form.driverKind || "hecate_task",
    status: form.status,
    ...(executionRef.kind === "none" ? {} : { execution_ref: executionRef }),
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
    status: form.status,
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
    status: handoffStatusFromValue(handoff?.status),
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
    execution.messageID || matchingActivityMessageID(assignment, execution, activityItem);
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

function matchingActivityMessageID(
  assignment: ProjectAssignmentRecord,
  execution: ProjectAssignmentExecutionViewModel,
  activityItem?: ProjectActivityItemRecord,
): string {
  if (
    !activityItem ||
    activityItem.assignment.id !== assignment.id ||
    activityItem.assignment.project_id !== assignment.project_id ||
    activityItem.assignment.work_item_id !== assignment.work_item_id ||
    activityItem.assignment.updated_at !== assignment.updated_at
  ) {
    return "";
  }
  const activityExecution = toProjectActivityAssignmentExecutionViewModel(activityItem);
  const hasCanonicalRuntime = Boolean(
    execution.taskID || execution.runID || execution.chatSessionID,
  );
  const sameRuntime =
    execution.taskID === activityExecution.taskID &&
    execution.runID === activityExecution.runID &&
    execution.chatSessionID === activityExecution.chatSessionID;
  if (!hasCanonicalRuntime || !sameRuntime) return "";
  return firstNonEmpty(activityItem.linked_message_id, activityItem.linked_chat?.latest_message_id);
}

export function reviewHandoffFormFromAssignment(
  assignment: ProjectAssignmentRecord,
  sourceRole: ProjectWorkRoleRecord | null,
  targetRole: ProjectWorkRoleRecord,
  workItem: ProjectWorkItemRecord,
  activityItem?: ProjectActivityItemRecord,
): HandoffForm {
  const draft = handoffFormFromAssignment(assignment, sourceRole, activityItem);
  const sourceRoleName = sourceRole?.name || assignment.role_id;
  const targetRoleName = targetRole.name || targetRole.id;
  const workTitle = workItem.title || workItem.id;
  return {
    ...draft,
    targetRoleID: targetRole.id,
    title: `${targetRoleName} review request`,
    summary: `Review ${sourceRoleName}'s assignment for "${workTitle}".`,
    recommendedNextAction:
      "Create and start the linked review assignment, then record findings as a review artifact or follow-up handoff.",
  };
}

export function reviewArtifactFormFromAssignment(
  assignment: ProjectAssignmentRecord,
  role: ProjectWorkRoleRecord | null,
  _workItem: ProjectWorkItemRecord,
  handoffs: ProjectHandoffRecord[] = [],
): ReviewArtifactForm {
  const roleName = role?.name || assignment.role_id;
  return {
    assignmentID: assignment.id,
    reviewedAssignmentID: reviewedAssignmentIDForReviewAssignment(assignment, handoffs),
    authorRoleID: assignment.role_id,
    title: `${roleName} review`,
    verdict: "approved",
    risk: "unknown",
    summary: "",
    verification: "",
    followUp: "",
  };
}

export function reviewArtifactPayloadFromForm(
  form: ReviewArtifactForm,
): CreateProjectCollaborationArtifactPayload {
  return {
    assignment_id: form.assignmentID.trim(),
    author_role_id: form.authorRoleID.trim(),
    kind: "review",
    title: form.title.trim() || "Review",
    body: reviewArtifactBodyFromForm(form),
    reviewed_assignment_id: form.reviewedAssignmentID.trim() || undefined,
    review_verdict: form.verdict,
    review_risk: form.risk,
    review_follow_up_required: reviewFollowUpRequired(form),
  };
}

export function evidenceLinkPayloadFromForm(
  form: EvidenceLinkForm,
): CreateProjectCollaborationArtifactPayload {
  return {
    assignment_id: form.assignmentID.trim() || undefined,
    kind: "evidence_link",
    title: form.title.trim() || "Evidence link",
    body: form.summary.trim(),
    evidence_source_kind: form.sourceKind.trim() || "external",
    evidence_url: form.url.trim() || undefined,
    evidence_external_id: form.externalID.trim() || undefined,
    evidence_provider: form.provider.trim() || undefined,
    evidence_trust_label: form.trustLabel.trim() || "operator_provided",
  };
}

export function handoffFormFromReviewArtifact(
  artifact: ProjectCollaborationArtifactRecord,
  workItem: ProjectWorkItemRecord,
): HandoffForm {
  return {
    id: "",
    sourceAssignmentID: artifact.assignment_id ?? "",
    sourceRunID: "",
    sourceChatSessionID: "",
    sourceMessageID: "",
    targetRoleID: workItem.owner_role_id ?? "",
    targetAssignmentID: "",
    title: `${artifact.title || "Review"} follow-up`,
    summary: `Follow up on review artifact ${artifact.title || artifact.id}.`,
    recommendedNextAction:
      "Create a follow-up assignment for requested changes, or dismiss this handoff if no follow-up is needed.",
    linkedArtifactIDs: artifact.id,
    linkedMemoryIDs: "",
    contextRefs: "",
    status: "pending",
    provenanceKind: "operator",
    trustLabel: "operator_reviewed",
  };
}

function reviewArtifactBodyFromForm(form: ReviewArtifactForm): string {
  return [
    `Verdict: ${reviewVerdictLabel(form.verdict)}`,
    `Risk: ${reviewRiskLabel(form.risk)}`,
    "",
    "Summary:",
    form.summary.trim() || "No summary recorded.",
    "",
    "Verification:",
    form.verification.trim() || "Not recorded.",
    "",
    "Follow-up:",
    form.followUp.trim() || "None recorded.",
  ].join("\n");
}

function reviewedAssignmentIDForReviewAssignment(
  assignment: ProjectAssignmentRecord,
  handoffs: ProjectHandoffRecord[],
): string {
  const handoff = handoffs.find(
    (item) => item.target_assignment_id === assignment.id && item.source_assignment_id,
  );
  return handoff?.source_assignment_id ?? "";
}

export function reviewFollowUpRequired(form: ReviewArtifactForm): boolean {
  if (form.verdict === "changes_requested" || form.verdict === "blocked") return true;
  return form.followUp.trim().length > 0;
}

export function reviewVerdictLabel(verdict: ReviewVerdict): string {
  switch (verdict) {
    case "changes_requested":
      return "Changes requested";
    case "blocked":
      return "Blocked";
    case "risk":
      return "Risk noted";
    case "approved":
      return "Approved";
  }
}

export function reviewRiskLabel(risk: ReviewRisk): string {
  switch (risk) {
    case "low":
      return "Low";
    case "medium":
      return "Medium";
    case "high":
      return "High";
    case "unknown":
      return "Unknown";
  }
}

function choiceFromValue<T extends readonly string[]>(
  values: T,
  value: string | undefined | null,
  fallback: T[number],
): T[number] {
  const candidate = value?.trim();
  return candidate && (values as readonly string[]).includes(candidate)
    ? (candidate as T[number])
    : fallback;
}
