import type {
  ProjectActivityData,
  ProjectActivityItemRecord,
  ProjectAssignmentRecord,
  ProjectCollaborationArtifactRecord,
  ProjectHandoffRecord,
  ProjectMemoryCandidateRecord,
  ProjectMemoryRecord,
  ProjectRecord,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
} from "../../types/project";
import { toProjectActivityItemViewModel } from "./projectAssignmentViewModels";

export type ProjectTimelineItemKind =
  | "assignment"
  | "artifact"
  | "decision"
  | "handoff"
  | "memory"
  | "memory_candidate";

export type ProjectTimelineItem = {
  id: string;
  kind: ProjectTimelineItemKind;
  title: string;
  summary: string;
  actor: string;
  source: string;
  timestamp: string;
  status?: string;
  workItemID?: string;
  taskID?: string;
  runID?: string;
  chatID?: string;
  memoryEntry?: ProjectMemoryRecord;
  assignment?: ProjectAssignmentRecord;
};

export function buildProjectTimelineItems({
  activity,
  artifacts,
  handoffs,
  memoryCandidates,
  memoryEntries,
  project,
  roles,
  workItems,
}: {
  activity: ProjectActivityData | null;
  artifacts: ProjectCollaborationArtifactRecord[];
  handoffs: ProjectHandoffRecord[];
  memoryCandidates: ProjectMemoryCandidateRecord[];
  memoryEntries: ProjectMemoryRecord[];
  project: ProjectRecord;
  roles: ProjectWorkRoleRecord[];
  workItems: ProjectWorkItemRecord[];
}): ProjectTimelineItem[] {
  const items = new Map<string, ProjectTimelineItem>();
  const roleByID = new Map(roles.map((role) => [role.id, role]));
  const workByID = new Map(workItems.map((item) => [item.id, item]));
  for (const activityItem of projectActivityItems(activity)) {
    const activityView = toProjectActivityItemViewModel(activityItem);
    const workItem =
      workByID.get(activityItem.work_item.id) ??
      projectActivityWorkItemToWorkItem(project.id, activityItem.work_item);
    const role = roleByID.get(activityItem.assignment.role_id) ?? activityItem.role;
    if (activityView.blockingSignal !== "not_started") {
      setTimelineItem(items, {
        id: `assignment:${activityItem.assignment.id}`,
        kind: "assignment",
        title: workItem.title,
        summary: activityView.statusSummary,
        actor: `role ${role?.name || activityItem.assignment.role_id}`,
        source: activityItem.assignment.driver_kind,
        timestamp: activityItem.updated_at || activityItem.assignment.updated_at,
        status: activityView.blockingSignal,
        workItemID: workItem.id,
        taskID: activityView.execution.taskID,
        runID: activityView.execution.runID,
        chatID: activityView.execution.chatSessionID,
        assignment: activityItem.assignment,
      });
    }
    for (const artifact of activityItem.recent_artifacts ?? []) {
      addTimelineArtifact(items, artifact, workItem.title);
    }
    for (const handoff of activityItem.recent_handoffs ?? []) {
      addTimelineHandoff(items, handoff, workItem.title);
    }
  }
  for (const artifact of artifacts) {
    const workTitle = workByID.get(artifact.work_item_id)?.title ?? "";
    addTimelineArtifact(items, artifact, workTitle);
  }
  for (const handoff of handoffs) {
    const workTitle = workByID.get(handoff.work_item_id)?.title ?? "";
    addTimelineHandoff(items, handoff, workTitle);
  }
  for (const entry of memoryEntries) {
    setTimelineItem(items, {
      id: `memory:${entry.id}`,
      kind: "memory",
      title: `Context memory: ${entry.title}`,
      summary: `${entry.enabled ? "Enabled" : "Disabled"} project memory entry`,
      actor: entry.source_kind || "operator",
      source: `${entry.trust_label}${entry.enabled ? "" : " / disabled"}`,
      timestamp: entry.updated_at || entry.created_at,
      status: entry.enabled ? "completed" : "stale_unknown",
      memoryEntry: entry,
    });
  }
  for (const candidate of memoryCandidates) {
    setTimelineItem(items, {
      id: `memory_candidate:${candidate.id}`,
      kind: "memory_candidate",
      title: `Memory candidate: ${candidate.title}`,
      summary: candidate.body,
      actor: candidate.suggested_source_kind || "generated",
      source: `${candidate.suggested_trust_label} / ${candidate.status}`,
      timestamp: candidate.updated_at || candidate.created_at,
      status: candidate.status === "pending" ? "awaiting_approval" : candidate.status,
    });
  }
  return Array.from(items.values()).sort(compareTimelineItems);
}

export function projectActivityWorkItemToWorkItem(
  projectID: string,
  item: ProjectActivityItemRecord["work_item"],
): ProjectWorkItemRecord {
  return {
    id: item.id,
    project_id: projectID,
    title: item.title,
    status: item.status,
    priority: item.priority,
    created_at: "",
    updated_at: "",
  };
}

export function timelineKindLabel(kind: ProjectTimelineItemKind): string {
  switch (kind) {
    case "assignment":
      return "assignment";
    case "decision":
      return "decision";
    case "handoff":
      return "handoff";
    case "memory":
      return "memory";
    case "memory_candidate":
      return "memory candidate";
    case "artifact":
      return "artifact";
  }
}

export function timelineBadgeClass(item: ProjectTimelineItem): string {
  if (item.kind === "decision") return "badge badge-amber";
  if (item.kind === "handoff" && item.status === "pending") return "badge badge-amber";
  if (item.kind === "memory_candidate" && item.status === "awaiting_approval") {
    return "badge badge-amber";
  }
  if (item.kind === "memory" && item.status === "stale_unknown") return "badge badge-amber";
  return "badge badge-muted";
}

export function activitySignalLabel(signal: string): string {
  switch (signal) {
    case "awaiting_approval":
      return "approval";
    case "not_started":
      return "not started";
    case "stale_unknown":
      return "unknown";
    case "completed":
      return "done";
    default:
      return signal.replaceAll("_", " ");
  }
}

function projectActivityItems(activity: ProjectActivityData | null): ProjectActivityItemRecord[] {
  if (!activity) return [];
  return [
    ...activity.buckets.blocked,
    ...activity.buckets.active,
    ...activity.buckets.completed,
    ...activity.buckets.recent,
    ...(activity.recent ?? []),
  ];
}

function addTimelineArtifact(
  items: Map<string, ProjectTimelineItem>,
  artifact: ProjectCollaborationArtifactRecord,
  workTitle: string,
) {
  const title = artifact.title || artifact.id;
  setTimelineItem(items, {
    id: `artifact:${artifact.id}`,
    kind: artifact.kind === "decision_note" ? "decision" : "artifact",
    title,
    summary: artifact.body,
    actor: artifact.author_role_id || "project",
    source: workTitle ? `${artifact.kind} / ${workTitle}` : artifact.kind,
    timestamp: artifact.updated_at || artifact.created_at,
    workItemID: artifact.work_item_id,
  });
}

function addTimelineHandoff(
  items: Map<string, ProjectTimelineItem>,
  handoff: ProjectHandoffRecord,
  workTitle: string,
) {
  setTimelineItem(items, {
    id: `handoff:${handoff.id}`,
    kind: "handoff",
    title: handoff.title || handoff.id,
    summary: handoff.summary || handoff.recommended_next_action,
    actor: handoff.created_by_role_id || "handoff",
    source: workTitle ? `${handoff.status} / ${workTitle}` : handoff.status,
    timestamp: handoff.updated_at || handoff.created_at,
    status: handoff.status,
    workItemID: handoff.work_item_id,
    taskID: "",
    runID: handoff.source_run_id,
    chatID: handoff.source_chat_session_id,
  });
}

function setTimelineItem(items: Map<string, ProjectTimelineItem>, item: ProjectTimelineItem) {
  const current = items.get(item.id);
  if (!current || compareTimelineItems(item, current) < 0) {
    items.set(item.id, item);
  }
}

function compareTimelineItems(left: ProjectTimelineItem, right: ProjectTimelineItem): number {
  const leftTime = Date.parse(left.timestamp || "");
  const rightTime = Date.parse(right.timestamp || "");
  if (Number.isFinite(leftTime) && Number.isFinite(rightTime) && leftTime !== rightTime) {
    return rightTime - leftTime;
  }
  if (Number.isFinite(leftTime) !== Number.isFinite(rightTime)) {
    return Number.isFinite(leftTime) ? -1 : 1;
  }
  return left.id.localeCompare(right.id);
}
