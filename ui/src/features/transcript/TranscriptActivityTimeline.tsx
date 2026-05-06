import { useEffect, useState } from "react";

import type { AgentChatActivityRecord } from "../../types/runtime";

export function DiffStatList({ diffStat }: { diffStat: string }) {
  const rows = parseDiffStatRows(diffStat);
  const summary = formatDiffStatSummary(diffStat);

  if (rows.length === 0) {
    return (
      <div style={{ color: "var(--t2)", fontFamily: "var(--font-mono)", fontSize: 11 }}>
        {summary}
      </div>
    );
  }

  return (
    <div style={{
      display: "grid",
      gap: 5,
      padding: "8px 10px",
      border: "1px solid var(--border)",
      borderRadius: "var(--radius-sm)",
      background: "var(--bg2)",
    }}>
      {rows.map(row => (
        <div key={row.path} style={{ display: "grid", gridTemplateColumns: "minmax(0, 1fr) auto", gap: 10, alignItems: "baseline" }}>
          <span style={{ color: "var(--t1)", fontFamily: "var(--font-mono)", fontSize: 11, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            {row.path}
          </span>
          <span style={{ color: "var(--t3)", fontFamily: "var(--font-mono)", fontSize: 11, whiteSpace: "nowrap" }}>
            {row.change}
          </span>
        </div>
      ))}
      {summary && (
        <div style={{ borderTop: "1px solid var(--border)", color: "var(--t2)", fontFamily: "var(--font-mono)", fontSize: 11, marginTop: 2, paddingTop: 6 }}>
          {summary}
        </div>
      )}
    </div>
  );
}

export function formatDiffStatSummary(diffStat: string): string {
  const lines = diffStat.split(/\r?\n/).map(line => line.trim()).filter(Boolean);
  return lines.find(line => /\bfiles? changed\b/.test(line)) || lines[0] || "";
}

export function TranscriptActivityTimeline({ activities, diffStat }: { activities: AgentChatActivityRecord[]; diffStat?: string }) {
  const visible = compactAgentActivities(activities);
  const terminal = terminalAgentActivity(activities);
  const hasRunning = !terminal && activities.some(isActiveAgentActivity);
  const [open, setOpen] = useState(hasRunning);

  useEffect(() => {
    if (hasRunning) {
      setOpen(true);
    }
  }, [hasRunning]);

  if (visible.length === 0) return null;

  const plan = visible.filter(activity => activity.type === "plan");
  const tools = visible.filter(activity => activity.type === "tool_call");
  const summary = [
    terminal ? terminalStatusLabel(terminal.status) : hasRunning ? "working" : "details",
    plan.length > 0 ? `${plan.filter(item => item.status === "completed").length}/${plan.length} plan` : "",
    tools.length > 0 ? `${tools.length} tool${tools.length === 1 ? "" : "s"}` : "",
    diffStat ? "files changed" : "",
  ].filter(Boolean).join(" · ");

  return (
    <details
      onToggle={(event) => setOpen(event.currentTarget.open)}
      open={open}
      style={{ marginTop: 8 }}
    >
      <summary style={{ cursor: "pointer", fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)" }}>
        {summary}
      </summary>
      <div style={{
        display: "grid",
        gap: 5,
        marginTop: 6,
        padding: "8px 10px",
        border: "1px solid var(--border)",
        borderRadius: "var(--radius-sm)",
        background: "var(--bg2)",
      }}>
        {visible.map((activity, index) => (
          <TimelineActivityLine
            key={activity.id || `${activity.type}-${activity.created_at ?? index}`}
            activity={activity}
          />
        ))}
      </div>
    </details>
  );
}

function parseDiffStatRows(diffStat: string): Array<{ path: string; change: string }> {
  return diffStat
    .split(/\r?\n/)
    .map(line => line.trim())
    .filter(Boolean)
    .filter(line => !/\bfiles? changed\b/.test(line))
    .map(line => {
      const match = line.match(/^(.+?)\s+\|\s+(.+)$/);
      if (!match) return null;
      return { path: match[1].trim(), change: match[2].trim() };
    })
    .filter((row): row is { path: string; change: string } => row !== null);
}

function TimelineActivityLine({ activity }: { activity: AgentChatActivityRecord }) {
  if (activity.type === "plan") {
    return <PlanActivityLine activity={activity} />;
  }
  return <ActivityLine activity={activity} prefix={activityLinePrefix(activity)} />;
}

function PlanActivityLine({ activity }: { activity: AgentChatActivityRecord }) {
  return (
    <div style={{ display: "flex", alignItems: "baseline", gap: 8, minWidth: 0 }}>
      <span style={{ color: activity.status === "completed" ? "var(--green)" : activity.status === "in_progress" ? "var(--teal)" : "var(--t3)", flexShrink: 0, fontFamily: "var(--font-mono)", fontSize: 11 }}>
        {activity.status === "completed" ? "x" : activity.status === "in_progress" ? ">" : "-"}
      </span>
      <span style={{ color: "var(--t1)", fontSize: 11, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
        {activity.title}
      </span>
      {activity.kind && (
        <span style={{ color: "var(--t3)", flexShrink: 0, fontFamily: "var(--font-mono)", fontSize: 10 }}>
          {activity.kind}
        </span>
      )}
    </div>
  );
}

function ActivityLine({ activity, prefix }: { activity: AgentChatActivityRecord; prefix?: string }) {
  const display = activityDisplay(activity);
  return (
    <div style={{ display: "flex", alignItems: "baseline", gap: 8, minWidth: 0 }}>
      <span style={{
        width: 7,
        height: 7,
        borderRadius: 999,
        background: activityStatusColor(activity.status),
        flexShrink: 0,
      }} />
      {prefix && (
        <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)", whiteSpace: "nowrap" }}>
          {prefix}
        </span>
      )}
      <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t1)", whiteSpace: "nowrap" }}>
        {display.title}
      </span>
      {display.detail && (
        <span style={{ fontSize: 11, color: "var(--t3)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          {display.detail}
        </span>
      )}
    </div>
  );
}

function compactAgentActivities(activities: AgentChatActivityRecord[]): AgentChatActivityRecord[] {
  const hiddenTypes = new Set(["artifact", "changed_files", "final_answer", "output"]);
  const terminal = terminalAgentActivity(activities);
  const lastTaskRunIndex = lastIndexOfTaskRunActivity(activities);
  const lastApprovalIndexByID = lastIndexByApprovalID(activities);
  const out: AgentChatActivityRecord[] = [];
  for (const [index, activity] of activities.entries()) {
    if (hiddenTypes.has(activity.type)) continue;
    if (activity.type === "completed" && activity.title.toLowerCase() === "final answer") continue;
    if (isTerminalRunSummary(activity)) continue;
    if (terminal && (activity.type === "started" || activity.type === "running")) continue;
    if (activity.type === "running" && activities.some(item => item.type === "output")) continue;
    if (isTaskRunActivity(activity) && index !== lastTaskRunIndex) continue;
    if (activity.type === "approval" && activity.approval_id && lastApprovalIndexByID.get(activity.approval_id) !== index) continue;
    out.push(activity);
  }
  return collapseModelTurnActivities(out);
}

function lastIndexByApprovalID(activities: AgentChatActivityRecord[]): Map<string, number> {
  const out = new Map<string, number>();
  for (const [index, activity] of activities.entries()) {
    if (activity.type === "approval" && activity.approval_id) {
      out.set(activity.approval_id, index);
    }
  }
  return out;
}

function lastIndexOfTaskRunActivity(activities: AgentChatActivityRecord[]): number {
  for (let index = activities.length - 1; index >= 0; index -= 1) {
    if (isTaskRunActivity(activities[index])) return index;
  }
  return -1;
}

function isTaskRunActivity(activity: AgentChatActivityRecord): boolean {
  return activity.type === "task_run" || (activity.type.startsWith("task_") && activity.title.startsWith("Task run"));
}

function isTerminalRunSummary(activity: AgentChatActivityRecord): boolean {
  if (activity.type !== "run_result" && activity.type !== "completed") return false;
  return /^run\s+(completed|failed|cancelled)$/i.test(activity.title.trim());
}

function collapseModelTurnActivities(activities: AgentChatActivityRecord[]): AgentChatActivityRecord[] {
  const turnActivities = activities.filter(isModelTurnActivity);
  if (turnActivities.length <= 1) return activities;

  const firstTurnIndex = activities.findIndex(isModelTurnActivity);
  const status = aggregateActivityStatus(turnActivities);
  const collapsed: AgentChatActivityRecord = {
    id: "hecate-agent:model-turns",
    type: "model_turns",
    status,
    kind: "model",
    title: "Thinking",
    detail: `${turnActivities.length} model turns ${humanActivityStatus(status)}`,
  };

  const out = activities.filter(activity => !isModelTurnActivity(activity));
  out.splice(Math.max(firstTurnIndex, 0), 0, collapsed);
  return out;
}

function isModelTurnActivity(activity: AgentChatActivityRecord): boolean {
  return activity.type === "thinking" && /^Agent turn \d+/i.test(activity.title.trim());
}

function aggregateActivityStatus(activities: AgentChatActivityRecord[]): string {
  if (activities.some(activity => activity.status === "failed")) return "failed";
  if (activities.some(activity => activity.status === "cancelled")) return "cancelled";
  if (activities.some(isActiveAgentActivity)) return "running";
  if (activities.every(activity => activity.status === "completed")) return "completed";
  return activities[activities.length - 1]?.status || "completed";
}

function activityDisplay(activity: AgentChatActivityRecord): { title: string; detail?: string } {
  if (activity.type === "approval") {
    const title = approvalActivityTitle(activity);
    const detail = cleanApprovalDetail(activity.detail);
    return { title, detail };
  }
  if (activity.type === "tool_call") {
    return { title: toolActivityTitle(activity), detail: cleanActivityDetail(activity) };
  }
  if (activity.type === "thinking" && isModelTurnActivity(activity)) {
    return { title: "Thinking", detail: modelTurnDetail(activity) };
  }
  if (activity.type === "model_turns") {
    return { title: "Thinking", detail: activity.detail };
  }
  if (activity.type === "started" && /^Starting Hecate Agent$/i.test(activity.title.trim())) {
    return { title: "Starting agent", detail: cleanActivityDetail(activity) };
  }
  if (!isTaskRunActivity(activity)) {
    return { title: activity.title, detail: cleanActivityDetail(activity) };
  }
  const status = activity.status || activity.title.replace(/^Task run\s+/, "");
  const humanStatus = humanActivityStatus(status);
  const existingDetail = activity.detail || "";
  const detail = cleanTaskRunDetail(existingDetail, humanStatus);
  return { title: "Backing task", detail };
}

function activityLinePrefix(activity: AgentChatActivityRecord): string | undefined {
  switch (activity.type) {
    case "tool_call":
      return "tool";
    case "thinking":
    case "model_turns":
      return "model";
    case "approval":
      return "approval";
    default:
      return undefined;
  }
}

function toolActivityTitle(activity: AgentChatActivityRecord): string {
  const raw = (activity.title || activity.kind || "tool").trim();
  const normalized = raw.toLowerCase();

  switch (normalized) {
    case "shell_exec":
      return "Ran shell";
    case "git_exec":
      return "Ran git";
    case "read_file":
      return "Read file";
    case "list_dir":
      return "Listed files";
    case "write_file":
    case "edit_file":
    case "apply_patch":
      return "Edited file";
    case "agent_loop_tool_call":
      return "Requested tool";
    default:
      break;
  }

  const execMatch = normalized.match(/^([a-z0-9_-]+)_exec$/);
  if (execMatch) {
    return `Ran ${execMatch[1].replaceAll("_", " ")}`;
  }

  return raw;
}

function modelTurnDetail(activity: AgentChatActivityRecord): string {
  const status = humanActivityStatus(activity.status);
  const turn = activity.title.match(/turn\s+(\d+)/i)?.[1];
  return turn ? `turn ${turn} ${status}` : status;
}

function cleanApprovalDetail(detail?: string): string | undefined {
  const cleaned = detail
    ?.replace(/^Agent requested tools that require approval:\s*/i, "")
    .replace(/\s+-\s+(awaiting_approval|pending|approved|rejected|denied|cancelled|timed_out)$/i, "")
    .trim();
  return cleaned || undefined;
}

function cleanActivityDetail(activity: AgentChatActivityRecord): string | undefined {
  const detail = activity.detail?.trim();
  if (!detail) return undefined;

  const title = activity.title.trim();
  const status = activity.status?.trim();
  const duplicateForms = [
    title,
    status,
    status ? `${title} - ${status}` : "",
    status ? `${title} · ${status}` : "",
  ].filter((value): value is string => Boolean(value)).map(value => value.toLowerCase());

  return duplicateForms.includes(detail.toLowerCase()) ? undefined : detail;
}

function cleanTaskRunDetail(existingDetail: string, humanStatus: string): string {
  const cleaned = existingDetail
    .replace(/\s+-\s+(running|completed|failed|cancelled|awaiting_approval)$/i, "")
    .trim();
  return cleaned.startsWith(humanStatus)
    ? existingDetail
    : [humanStatus, cleaned].filter(Boolean).join(" · ");
}

function humanActivityStatus(status?: string): string {
  switch (status) {
    case "awaiting_approval":
      return "waiting for approval";
    case "in_progress":
      return "running";
    default:
      return (status || "completed").replaceAll("_", " ");
  }
}

function approvalActivityTitle(activity: AgentChatActivityRecord): string {
  if (activity.needs_action || activity.status === "awaiting_approval" || activity.status === "pending") {
    return "Waiting for approval";
  }
  switch (activity.status) {
    case "approved":
      return "Approval granted";
    case "rejected":
    case "denied":
      return "Approval rejected";
    default:
      return activity.title;
  }
}

function terminalAgentActivity(activities: AgentChatActivityRecord[]): AgentChatActivityRecord | undefined {
  const terminalTypes = new Set(["completed", "failed", "cancelled"]);
  return [...activities].reverse().find(activity => terminalTypes.has(activity.type));
}

function terminalStatusLabel(status?: string): string {
  switch (status) {
    case "completed":
      return "completed";
    case "failed":
      return "failed";
    case "cancelled":
      return "cancelled";
    default:
      return status || "details";
  }
}

function activityStatusColor(status?: string) {
  switch (status) {
  case "failed":
    return "var(--red)";
  case "cancelled":
    return "var(--amber)";
  case "running":
  case "in_progress":
    return "var(--teal)";
  default:
    return "var(--green)";
  }
}

function isActiveAgentActivity(activity: AgentChatActivityRecord): boolean {
  return activity.status === "running" || activity.status === "in_progress";
}
