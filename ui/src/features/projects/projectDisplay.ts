import { ApiError } from "../../lib/api";
import { projectRootOptionLabel } from "./projectSettings";
import { shortID } from "./projectUtils";
import type { ProjectDeleteRecord, ProjectRecord } from "../../types/project";

export function formatProjectRowRelativeTime(iso: string): string {
  const parsed = Date.parse(iso);
  if (!Number.isFinite(parsed)) return iso || "—";
  const diff = Math.max(0, Date.now() - parsed);
  const sec = Math.floor(diff / 1000);
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const day = Math.floor(hr / 24);
  if (day < 14) return `${day}d ago`;
  const week = Math.floor(day / 7);
  if (week < 8) return `${week}w ago`;
  const month = Math.floor(day / 30);
  if (day < 365) return `${Math.max(1, month)}mo ago`;
  return `${Math.max(1, Math.floor(day / 365))}y ago`;
}

export function projectErrorMessage(error: unknown, fallback: string): string {
  if (error instanceof ApiError) {
    return error.operatorAction
      ? `${error.message} ${error.operatorAction}`
      : error.message || fallback;
  }
  return error instanceof Error ? error.message : fallback;
}

export function workStatusLabel(status: string): string {
  if (status === "done") return "done";
  return status.replaceAll("_", " ");
}

export function assignmentStatusLabel(status: string | undefined): string {
  if (!status) return "unknown";
  if (status === "awaiting_approval") return "approval";
  if (status === "completed") return "done";
  return status.replaceAll("_", " ");
}

export function handoffStatusLabel(status: string): string {
  switch (status) {
    case "pending":
      return "Pending";
    case "accepted":
      return "Accepted";
    case "superseded":
      return "Superseded";
    case "dismissed":
      return "Dismissed";
    default:
      return status || "Unknown";
  }
}

export function projectRootDisplayLabel(project: ProjectRecord, rootID: string): string {
  const root = project.roots.find((item) => item.id === rootID);
  if (!root) return shortID(rootID);
  if (root.git_branch) return root.git_branch;
  const parts = root.path.split(/[\\/]/).filter(Boolean);
  return parts[parts.length - 1] || root.id;
}

export function projectRootTitle(project: ProjectRecord, rootID: string): string {
  const root = project.roots.find((item) => item.id === rootID);
  if (!root) return rootID;
  return projectRootOptionLabel(root);
}

export function formatProjectDeleteSummary(result: ProjectDeleteRecord): string {
  const projectName = result.project_name?.trim() || result.project_id;
  const cleaned = [
    countLabel(result.chat_sessions_deleted, "chat"),
    countLabel(result.project_work_rows_deleted, "work row"),
    countLabel(result.project_skills_deleted, "skill"),
    countLabel(result.memory_entries_deleted, "memory entry", "memory entries"),
    countLabel(result.memory_candidates_deleted, "memory candidate"),
  ].filter(Boolean);
  if (cleaned.length === 0) {
    return `Deleted ${projectName}. No scoped project records needed cleanup.`;
  }
  return `Deleted ${projectName}. Cleaned up ${cleaned.join(", ")}.`;
}

function countLabel(count: number, singular: string, plural = `${singular}s`): string {
  return count > 0 ? `${count} ${count === 1 ? singular : plural}` : "";
}
