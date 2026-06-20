import { formatAbsoluteTime } from "../../lib/format";
import type { AgentProfileRecord } from "../../types/agent-profile";
import type {
  ProjectActivityData,
  ProjectActivityItemRecord,
  ProjectAssignmentRecord,
  ProjectCollaborationArtifactRecord,
  ProjectHandoffRecord,
  ProjectMemoryCandidateRecord,
  ProjectMemoryRecord,
  ProjectRecord,
  ProjectSkillRecord,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
} from "../../types/project";
import {
  toProjectActivityAssignmentExecutionViewModel,
  toProjectActivityItemViewModel,
  toProjectAssignmentExecutionViewModel,
} from "./projectAssignmentViewModels";
import { firstNonEmpty } from "./projectUtils";

export type ProjectActivityBucketKey = "all" | "active" | "blocked" | "completed" | "recent";

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

export type ProjectHealthMetric = {
  key:
    | ProjectActivityBucketKey
    | "approvals"
    | "failed"
    | "stale"
    | "defaults"
    | "context"
    | "reviews"
    | "handoffs"
    | "memory_candidates";
  label: string;
  value: number | string;
  status: string;
  detail: string;
  bucket?: ProjectActivityBucketKey;
};

export type ProjectHealthAttention = {
  id: string;
  title: string;
  detail: string;
  status: string;
  action?: "memory" | "profiles" | "roles" | "settings" | "skills";
  bucket?: ProjectActivityBucketKey;
  workItemID?: string;
  taskID?: string;
  runID?: string;
  chatID?: string;
  candidateID?: string;
  actionLabel?: string;
};

export type ProjectHealthSummary = {
  staleAssignments: number;
  missingDefaults: boolean;
  enabledMemory: number;
  savedMemory: number;
  enabledContextSources: number;
  memoryCandidates: {
    pending: number;
    promoted: number;
    rejected: number;
  };
  handoffs: {
    total: number;
    pending: number;
    accepted: number;
    superseded: number;
    dismissed: number;
  };
  reviews: {
    total: number;
    followUpRequired: number;
    blocked: number;
    changesRequested: number;
  };
  attention: ProjectHealthAttention[];
};

export type ProjectHealthSummaryOptions = {
  agentProfiles?: AgentProfileRecord[];
  artifacts?: ProjectCollaborationArtifactRecord[];
  roles?: ProjectWorkRoleRecord[];
  skills?: ProjectSkillRecord[];
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

export function buildProjectHealthSummary(
  project: ProjectRecord | null,
  activity: ProjectActivityData | null,
  workItems: ProjectWorkItemRecord[],
  memoryEntries: ProjectMemoryRecord[],
  memoryCandidates: ProjectMemoryCandidateRecord[],
  options: ProjectHealthSummaryOptions = {},
): ProjectHealthSummary {
  const activityItems = uniqueActivityItems(activity);
  const projectedAssignments = workItems.flatMap((item) =>
    (item.assignments ?? []).map((assignment) => ({
      assignment,
      workItem: item,
      status: toProjectAssignmentExecutionViewModel(assignment).status,
    })),
  );
  const staleItems = [
    ...activityItems.filter(
      (item) => toProjectActivityItemViewModel(item).blockingSignal === "stale_unknown",
    ),
    ...activityItems.filter((item) => toProjectActivityItemViewModel(item).execution.missing),
    ...projectedAssignments
      .filter((item) => isStaleAssignment(item.assignment, item.status))
      .map((item) => projectAssignmentToActivityAttention(project, item.workItem, item.assignment)),
  ].filter(Boolean) as ProjectActivityItemRecord[];
  const enabledMemory = memoryEntries.filter((entry) => entry.enabled).length;
  const enabledContextSources = (project?.context_sources ?? []).filter(
    (source) => source.enabled,
  ).length;
  const memoryCandidateSummary = summarizeProjectMemoryCandidates(memoryCandidates);
  const handoffSummary = summarizeProjectHandoffs(activityItems);
  const reviewSummary = summarizeReviewArtifacts(options.artifacts ?? []);
  const missingDefaults = Boolean(project && (!project.default_provider || !project.default_model));
  const missingProjectRoot = Boolean(
    project &&
    (project.roots ?? []).filter((root) => root.active !== false && root.path).length === 0,
  );
  const attention: ProjectHealthAttention[] = [];
  if (missingProjectRoot && project) {
    attention.push({
      id: `${project.id}:root`,
      title: "No project root configured",
      detail:
        "Assignment starts need an active local workspace root for files, tools, and guidance discovery.",
      status: "stale_unknown",
      action: "settings",
    });
  }
  if (missingDefaults && project) {
    attention.push({
      id: `${project.id}:defaults`,
      title: "Provider/model defaults missing",
      detail: "Native project starts and assignment chats need a default provider and model.",
      status: "awaiting_approval",
      action: "settings",
    });
  }
  if (project) {
    attention.push(
      ...profileAttentionItems(project, options.roles ?? [], options.agentProfiles ?? []),
    );
    attention.push(
      ...skillAttentionItems(
        project,
        options.roles ?? [],
        options.agentProfiles ?? [],
        options.skills ?? [],
      ),
    );
  }
  const firstPendingHandoff = activityItems.find((item) => hasPendingHandoff(item));
  if (firstPendingHandoff) {
    const latestHandoff = firstPendingHandoff.recent_handoffs?.find(
      (handoff) => handoff.status === "pending",
    );
    attention.push({
      id: `${firstPendingHandoff.id}:handoff`,
      title: `Pending handoff: ${firstPendingHandoff.work_item.title}`,
      detail: [
        firstNonEmpty(
          latestHandoff?.title,
          firstPendingHandoff.handoff_summary?.latest_title,
          "Handoff awaiting operator follow-up",
        ),
        firstPendingHandoff.role.name || firstPendingHandoff.assignment.role_id,
        firstPendingHandoff.handoff_summary?.latest_at
          ? `updated ${formatAbsoluteTime(firstPendingHandoff.handoff_summary.latest_at)}`
          : "",
      ]
        .filter(Boolean)
        .join(" · "),
      status: "awaiting_approval",
      bucket: "recent",
      workItemID: firstPendingHandoff.work_item.id,
      actionLabel: "View recent",
    });
  }
  const firstReviewFollowUp = reviewFollowUpAttentionItem(options.artifacts ?? [], workItems);
  if (firstReviewFollowUp) {
    attention.push(firstReviewFollowUp);
  }
  const firstStale = staleItems[0];
  if (firstStale) {
    attention.push(
      activityAttention(firstStale, "Stale or unknown assignment", "View blocked", "blocked"),
    );
  }
  const firstFailedExternal = activityItems.find(
    (item) =>
      item.assignment.driver_kind === "external_agent" &&
      (toProjectActivityItemViewModel(item).blockingSignal === "failed" ||
        toProjectActivityItemViewModel(item).blockingSignal === "cancelled"),
  );
  if (firstFailedExternal) {
    attention.push(
      activityAttention(
        firstFailedExternal,
        "External assignment needs review",
        "View blocked",
        "blocked",
      ),
    );
  }
  if (enabledMemory === 0 && enabledContextSources === 0 && project) {
    attention.push({
      id: `${project.id}:context`,
      title: "No project memory or context sources enabled",
      detail: "Project-scoped context is empty for new chats and linked context packets.",
      status: "stale_unknown",
      action: "memory",
    });
  }
  const firstPendingCandidate = memoryCandidates.find(
    (candidate) => candidate.status === "pending",
  );
  if (firstPendingCandidate) {
    attention.push({
      id: `${firstPendingCandidate.id}:memory-candidate`,
      title: "Memory candidate pending review",
      detail: `${firstPendingCandidate.title} · ${firstPendingCandidate.suggested_trust_label}`,
      status: "awaiting_approval",
      candidateID: firstPendingCandidate.id,
      action: "memory",
    });
  }

  return {
    staleAssignments: uniqueByID(staleItems).length,
    missingDefaults,
    enabledMemory,
    savedMemory: memoryEntries.length,
    enabledContextSources,
    memoryCandidates: memoryCandidateSummary,
    handoffs: handoffSummary,
    reviews: reviewSummary,
    attention: uniqueAttention(attention).slice(0, 5),
  };
}

export function projectHealthMetrics(health: ProjectHealthSummary): ProjectHealthMetric[] {
  return [
    {
      key: "defaults",
      label: "Defaults",
      value: health.missingDefaults ? "missing" : "set",
      status: health.missingDefaults ? "awaiting_approval" : "completed",
      detail: "provider and model",
    },
    {
      key: "context",
      label: "Context",
      value: health.enabledMemory + health.enabledContextSources,
      status:
        health.enabledMemory + health.enabledContextSources > 0 ? "completed" : "stale_unknown",
      detail: `${health.enabledMemory}/${health.savedMemory} memory, ${health.enabledContextSources} sources`,
    },
    {
      key: "memory_candidates",
      label: "Memory review",
      value: health.memoryCandidates.pending,
      status: health.memoryCandidates.pending > 0 ? "awaiting_approval" : "completed",
      detail: `${health.memoryCandidates.promoted} promoted, ${health.memoryCandidates.rejected} rejected`,
    },
    {
      key: "reviews",
      label: "Review follow-up",
      value: health.reviews.followUpRequired,
      status: health.reviews.followUpRequired > 0 ? "awaiting_approval" : "completed",
      detail: `${health.reviews.blocked} blocked, ${health.reviews.changesRequested} changes requested`,
    },
    {
      key: "handoffs",
      label: "Recent handoffs",
      value: health.handoffs.pending,
      status: health.handoffs.pending > 0 ? "awaiting_approval" : "completed",
      detail: `${health.handoffs.accepted} recent accepted, ${health.handoffs.superseded} superseded, ${health.handoffs.dismissed} dismissed`,
    },
    {
      key: "stale",
      label: "Stale links",
      value: health.staleAssignments,
      status: health.staleAssignments > 0 ? "stale_unknown" : "completed",
      detail: "missing or outdated run links",
    },
  ];
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

function summarizeProjectMemoryCandidates(
  candidates: ProjectMemoryCandidateRecord[],
): ProjectHealthSummary["memoryCandidates"] {
  return candidates.reduce<ProjectHealthSummary["memoryCandidates"]>(
    (summary, candidate) => {
      if (candidate.status === "pending") summary.pending += 1;
      if (candidate.status === "promoted") summary.promoted += 1;
      if (candidate.status === "rejected") summary.rejected += 1;
      return summary;
    },
    { pending: 0, promoted: 0, rejected: 0 },
  );
}

function summarizeProjectHandoffs(
  items: ProjectActivityItemRecord[],
): ProjectHealthSummary["handoffs"] {
  const seenHandoffIDs = new Set<string>();
  return items.reduce<ProjectHealthSummary["handoffs"]>(
    (summary, item) => {
      const handoffSummary = item.handoff_summary;
      const recentHandoffs = item.recent_handoffs ?? [];
      if (recentHandoffs.length > 0) {
        for (const handoff of recentHandoffs) {
          if (seenHandoffIDs.has(handoff.id)) continue;
          seenHandoffIDs.add(handoff.id);
          addHandoffStatus(summary, handoff.status);
        }
        return summary;
      }
      if (handoffSummary) {
        summary.total += handoffSummary.count;
        summary.pending += handoffSummary.pending_count ?? 0;
        summary.accepted += handoffSummary.accepted_count ?? 0;
      }
      return summary;
    },
    { total: 0, pending: 0, accepted: 0, superseded: 0, dismissed: 0 },
  );
}

function summarizeReviewArtifacts(
  artifacts: ProjectCollaborationArtifactRecord[],
): ProjectHealthSummary["reviews"] {
  return artifacts.reduce<ProjectHealthSummary["reviews"]>(
    (summary, artifact) => {
      if (artifact.kind !== "review") return summary;
      summary.total += 1;
      if (reviewArtifactRequiresFollowUp(artifact)) summary.followUpRequired += 1;
      if (artifact.review_verdict === "blocked") summary.blocked += 1;
      if (artifact.review_verdict === "changes_requested") summary.changesRequested += 1;
      return summary;
    },
    { total: 0, followUpRequired: 0, blocked: 0, changesRequested: 0 },
  );
}

function reviewFollowUpAttentionItem(
  artifacts: ProjectCollaborationArtifactRecord[],
  workItems: ProjectWorkItemRecord[],
): ProjectHealthAttention | null {
  const workByID = new Map(workItems.map((item) => [item.id, item]));
  const artifact = artifacts.find(reviewArtifactRequiresFollowUp);
  if (!artifact) return null;
  const workItem = workByID.get(artifact.work_item_id);
  const verdict = artifact.review_verdict ? artifact.review_verdict.replaceAll("_", " ") : "review";
  const risk = artifact.review_risk ? `risk ${artifact.review_risk}` : "";
  const reviewed = artifact.reviewed_assignment_id
    ? `reviewed ${artifact.reviewed_assignment_id}`
    : "";
  return {
    id: `${artifact.id}:review-follow-up`,
    title: `Review follow-up: ${workItem?.title ?? artifact.title ?? artifact.id}`,
    detail: [artifact.title || artifact.id, verdict, risk, reviewed].filter(Boolean).join(" · "),
    status: artifact.review_verdict === "blocked" ? "blocked" : "awaiting_approval",
    workItemID: artifact.work_item_id,
    actionLabel: "Open review",
  };
}

export function reviewArtifactRequiresFollowUp(
  artifact: ProjectCollaborationArtifactRecord,
): boolean {
  return (
    artifact.kind === "review" &&
    (artifact.review_follow_up_required === true ||
      artifact.review_verdict === "blocked" ||
      artifact.review_verdict === "changes_requested")
  );
}

export function reviewArtifactNeedsFollowUpPath(
  artifact: ProjectCollaborationArtifactRecord,
  handoffs: ProjectHandoffRecord[],
): boolean {
  if (!reviewArtifactRequiresFollowUp(artifact)) return false;
  return !handoffs.some(
    (handoff) =>
      (handoff.linked_artifact_ids ?? []).includes(artifact.id) &&
      (handoff.status === "pending" ||
        handoff.status === "dismissed" ||
        handoff.status === "superseded" ||
        Boolean(handoff.target_assignment_id)),
  );
}

function addHandoffStatus(summary: ProjectHealthSummary["handoffs"], status: string) {
  summary.total += 1;
  if (status === "pending") summary.pending += 1;
  if (status === "accepted") summary.accepted += 1;
  if (status === "superseded") summary.superseded += 1;
  if (status === "dismissed") summary.dismissed += 1;
}

function profileAttentionItems(
  project: ProjectRecord,
  roles: ProjectWorkRoleRecord[],
  profiles: AgentProfileRecord[],
): ProjectHealthAttention[] {
  if (profiles.length === 0) return [];
  const profileIDs = new Set(profiles.map((profile) => profile.id));
  const missing = new Set<string>();
  if (project.default_agent_profile && !profileIDs.has(project.default_agent_profile)) {
    missing.add(project.default_agent_profile);
  }
  for (const role of roles) {
    if (role.default_agent_profile && !profileIDs.has(role.default_agent_profile)) {
      missing.add(role.default_agent_profile);
    }
  }
  if (missing.size === 0) return [];
  const missingList = Array.from(missing).slice(0, 3).join(", ");
  return [
    {
      id: `${project.id}:profiles:missing`,
      title: "Agent profile reference missing",
      detail: `Project or role defaults reference ${missingList}${
        missing.size > 3 ? ` and ${missing.size - 3} more` : ""
      }.`,
      status: "stale_unknown",
      action: "profiles",
    },
  ];
}

function skillAttentionItems(
  project: ProjectRecord,
  roles: ProjectWorkRoleRecord[],
  profiles: AgentProfileRecord[],
  skills: ProjectSkillRecord[],
): ProjectHealthAttention[] {
  const skillsByID = new Map(skills.map((skill) => [skill.id, skill]));
  const referencedSkillIDs = referencedProjectSkillIDs(project, roles, profiles);
  const unresolved = referencedSkillIDs.filter((skillID) => !skillsByID.has(skillID));
  const disabledReferenced = referencedSkillIDs.filter((skillID) => {
    const skill = skillsByID.get(skillID);
    return skill && !skill.enabled;
  });
  const unavailable = skills.filter(
    (skill) =>
      skill.status !== "available" && (skill.enabled || referencedSkillIDs.includes(skill.id)),
  );
  const details: string[] = [];
  if (unresolved.length > 0) {
    details.push(`unresolved: ${summarizeIDs(unresolved)}`);
  }
  if (disabledReferenced.length > 0) {
    details.push(`disabled: ${summarizeIDs(disabledReferenced)}`);
  }
  if (unavailable.length > 0) {
    details.push(`unavailable: ${summarizeIDs(unavailable.map((skill) => skill.id))}`);
  }
  if (details.length === 0) return [];
  return [
    {
      id: `${project.id}:skills`,
      title: "Project skills need review",
      detail: `${details.join("; ")}.`,
      status: disabledReferenced.length > 0 ? "awaiting_approval" : "stale_unknown",
      action: "skills",
    },
  ];
}

function referencedProjectSkillIDs(
  project: ProjectRecord,
  roles: ProjectWorkRoleRecord[],
  profiles: AgentProfileRecord[],
): string[] {
  const referenced = new Set<string>();
  const relevantProfileIDs = new Set<string>();
  if (project.default_agent_profile) relevantProfileIDs.add(project.default_agent_profile);
  for (const role of roles) {
    for (const skillID of role.skill_ids ?? []) {
      if (skillID.trim()) referenced.add(skillID.trim());
    }
    if (role.default_agent_profile) relevantProfileIDs.add(role.default_agent_profile);
  }
  for (const profile of profiles) {
    if (!relevantProfileIDs.has(profile.id)) continue;
    for (const skillID of profile.skill_ids ?? []) {
      if (skillID.trim()) referenced.add(skillID.trim());
    }
  }
  return Array.from(referenced);
}

function summarizeIDs(ids: string[]): string {
  const unique = Array.from(new Set(ids));
  const shown = unique.slice(0, 3).join(", ");
  if (unique.length <= 3) return shown;
  return `${shown}, and ${unique.length - 3} more`;
}

function hasPendingHandoff(item: ProjectActivityItemRecord): boolean {
  return (
    (item.handoff_summary?.pending_count ?? 0) > 0 ||
    Boolean(item.recent_handoffs?.some((handoff) => handoff.status === "pending"))
  );
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

function uniqueActivityItems(activity: ProjectActivityData | null): ProjectActivityItemRecord[] {
  return uniqueByID(projectActivityItems(activity));
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

function uniqueByID<T extends { id: string }>(items: T[]): T[] {
  const seen = new Set<string>();
  const unique: T[] = [];
  for (const item of items) {
    if (seen.has(item.id)) continue;
    seen.add(item.id);
    unique.push(item);
  }
  return unique;
}

function uniqueAttention(items: ProjectHealthAttention[]): ProjectHealthAttention[] {
  return uniqueByID(items);
}

function isStaleAssignment(assignment: ProjectAssignmentRecord, status: string): boolean {
  if (status !== "queued" && status !== "running" && status !== "awaiting_approval") return false;
  const updatedAt = Date.parse(assignment.updated_at || assignment.started_at || "");
  if (!Number.isFinite(updatedAt)) return false;
  return Date.now() - updatedAt > 24 * 60 * 60 * 1000;
}

function projectAssignmentToActivityAttention(
  project: ProjectRecord | null,
  workItem: ProjectWorkItemRecord,
  assignment: ProjectAssignmentRecord,
): ProjectActivityItemRecord | null {
  if (!project) return null;
  const execution = toProjectAssignmentExecutionViewModel(assignment);
  return {
    id: assignment.id,
    project_id: project.id,
    work_item: {
      id: workItem.id,
      title: workItem.title,
      status: workItem.status,
      priority: workItem.priority,
    },
    assignment,
    role: {
      id: assignment.role_id,
      project_id: project.id,
      name: assignment.role_id,
      built_in: false,
    },
    status: execution.status,
    blocking_signal: "stale_unknown",
    status_summary: "active assignment has not changed recently",
    linked_task_id: execution.taskID,
    linked_run_id: execution.runID,
    linked_chat_id: execution.chatSessionID,
    artifact_summary: { count: assignment.execution?.artifact_count ?? 0 },
    updated_at: assignment.updated_at,
  };
}

function activityAttention(
  item: ProjectActivityItemRecord,
  title: string,
  actionLabel: string,
  bucket: ProjectActivityBucketKey,
): ProjectHealthAttention {
  const execution = toProjectActivityAssignmentExecutionViewModel(item);
  return {
    id: item.id,
    title: `${title}: ${item.work_item.title}`,
    detail: [
      item.status_summary,
      item.role.name || item.assignment.role_id,
      item.updated_at ? `updated ${formatAbsoluteTime(item.updated_at)}` : "",
    ]
      .filter(Boolean)
      .join(" · "),
    status: item.blocking_signal || item.status,
    bucket,
    workItemID: item.work_item.id,
    taskID: execution.taskID,
    runID: execution.runID,
    chatID: execution.chatSessionID,
    actionLabel,
  };
}
