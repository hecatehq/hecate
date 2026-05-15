import { useEffect, useState, type ReactNode } from "react";

import type { AgentChatActivityRecord } from "../../types/runtime";

const terminalRunSummaryTypes = new Set(["run_result", "completed", "failed", "cancelled"]);

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
  const lines = diffStat.split(/\\n|\r?\n/).map(line => line.trim()).filter(Boolean);
  return lines.find(line => /\bfiles? changed\b/.test(line)) || lines[0] || "";
}

export function TranscriptActivityTimeline({
  activities,
  diffStat,
  defaultOpen = false,
  renderAdvancedActivity,
}: {
  activities: AgentChatActivityRecord[];
  diffStat?: string;
  defaultOpen?: boolean;
  renderAdvancedActivity?: (activity: AgentChatActivityRecord) => ReactNode;
}) {
  const visible = orderVisibleActivities(compactAgentActivities(activities));
  const details = orderVisibleActivities(compactDetailActivities(activities, Boolean(diffStat)));
  const primary = diffStat ? [...visible, fileChangesActivity(diffStat)] : visible;
  const terminal = terminalAgentActivity(activities);
  const hasRunning = !terminal && activities.some(isActiveAgentActivity);
  const [open, setOpen] = useState(hasRunning || defaultOpen);

  useEffect(() => {
    if (hasRunning) {
      setOpen(true);
    }
  }, [hasRunning]);

  if (primary.length === 0 && details.length === 0) return null;

  const plan = primary.filter(activity => activity.type === "plan");
  const tools = primary.filter(activity => activity.type === "tool_call");
  const failedTools = tools.filter(activity => activity.status === "failed").length;
  const summary = [
    terminal ? terminalStatusLabel(terminal.status) : hasRunning ? "working" : "details",
    plan.length > 0 ? `${plan.filter(item => item.status === "completed").length}/${plan.length} plan` : "",
    tools.length > 0
      ? terminal?.status === "cancelled" && failedTools > 0
        ? `${failedTools} interrupted tool${failedTools === 1 ? "" : "s"}`
        : failedTools > 0
        ? `${failedTools} failed tool${failedTools === 1 ? "" : "s"}`
        : `${tools.length} tool${tools.length === 1 ? "" : "s"}`
      : "",
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
        {primary.map((activity, index) => (
          <TimelineActivityLine
            key={activity.id || `${activity.type}-${activity.created_at ?? index}`}
            activity={activity}
            renderAdvancedActivity={renderAdvancedActivity}
          />
        ))}
        {details.length > 0 && (
          <details style={{ borderTop: primary.length > 0 ? "1px solid var(--border)" : "none", marginTop: primary.length > 0 ? 4 : 0, paddingTop: primary.length > 0 ? 6 : 0 }}>
            <summary style={{ cursor: "pointer", fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
              {detailSummaryLabel(details)}
            </summary>
            <div style={{ display: "grid", gap: 5, marginTop: 6 }}>
              {details.map((activity, index) => (
                <TimelineActivityLine
                  key={activity.id || `detail-${activity.type}-${activity.created_at ?? index}`}
                  activity={activity}
                  renderAdvancedActivity={renderAdvancedActivity}
                />
              ))}
            </div>
          </details>
        )}
      </div>
    </details>
  );
}

function fileChangesActivity(diffStat: string): AgentChatActivityRecord {
  return {
    id: "hecate-agent:files-changed",
    type: "files_changed",
    status: "completed",
    title: "Files changed",
    detail: formatDiffStatSummary(diffStat),
  };
}

function parseDiffStatRows(diffStat: string): Array<{ path: string; change: string }> {
  return diffStat
    .split(/\\n|\r?\n/)
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

function TimelineActivityLine({
  activity,
  renderAdvancedActivity,
}: {
  activity: AgentChatActivityRecord;
  renderAdvancedActivity?: (activity: AgentChatActivityRecord) => ReactNode;
}) {
  const line = activity.type === "plan"
    ? <PlanActivityLine activity={activity} />
    : <ActivityLine activity={activity} prefix={activityLinePrefix(activity)} />;
  const hasAdvanced = Boolean(renderAdvancedActivity?.(activity));
  const [advancedOpen, setAdvancedOpen] = useState(false);
  if (!hasAdvanced) return line;

  return (
    <div style={{ display: "grid", gap: 4, minWidth: 0 }}>
      {line}
      <details
        onToggle={(event) => setAdvancedOpen(event.currentTarget.open)}
        style={{ marginLeft: 15 }}
      >
        <summary style={{ cursor: "pointer", fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
          {advancedSummaryLabel(activity)}
        </summary>
        {advancedOpen && (
          <div style={{
            marginTop: 6,
            padding: "7px 9px",
            border: "1px solid var(--border)",
            borderRadius: "var(--radius-sm)",
            background: "var(--bg1)",
          }}>
            {renderAdvancedActivity?.(activity)}
          </div>
        )}
      </details>
    </div>
  );
}

function advancedSummaryLabel(activity: AgentChatActivityRecord): string {
  if (activity.type === "output") return "Preview";
  if (activity.type === "artifact" && isOutputArtifactActivity(activity)) return "Preview";
  return "Advanced";
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
  const terminalIndex = pickTerminalActivityIndex(activities);
  const lastTaskRunIndex = lastIndexOfTaskRunActivity(activities);
  const lastApprovalIndexByID = lastIndexByApprovalID(activities);
  const out: AgentChatActivityRecord[] = [];
  for (const [index, activity] of activities.entries()) {
    if (hiddenTypes.has(activity.type)) continue;
    if (activity.type === "completed" && activity.title.toLowerCase() === "final answer") continue;
    if (isTerminalRunSummary(activity)) continue;
    // Drop terminal-shaped rows that aren't the chosen one. The
    // chooser prefers a diagnostic `terminal: true` row over a
    // generic agent-chat-handler row (see pickTerminalActivityIndex)
    // so an informative "LLM call failed on turn 3" beats a
    // bare-bones "Failed". When no row is chosen we keep them all.
    if (terminalIndex !== -1 && index !== terminalIndex && isTerminalActivity(activity)) continue;
    if (terminalIndex !== -1 && (activity.type === "started" || activity.type === "running")) continue;
    if (activity.type === "running" && activities.some(item => item.type === "output")) continue;
    if (isTaskRunActivity(activity) && index !== lastTaskRunIndex) continue;
    if (activity.type === "approval" && activity.approval_id && lastApprovalIndexByID.get(activity.approval_id) !== index) continue;
    out.push(activity);
  }
  return collapseModelTurnActivities(out);
}

// isTerminalActivity is the canonical predicate for "this activity
// represents a terminal status." Used both by the dedupe filter and
// by pickTerminalActivityIndex so the two cannot disagree on what
// counts as terminal — an earlier mismatch let `terminalAgentActivity`
// (which only looked at completed/failed/cancelled+terminal) pick
// the wrong row when a `run_result`-typed terminal arrived later.
function isTerminalActivity(activity: AgentChatActivityRecord): boolean {
  return activity.terminal === true || terminalRunSummaryTypes.has(activity.type);
}

// pickTerminalActivityIndex selects the row that should win on the
// timeline when several terminal-shaped rows exist for one run.
// Preference order:
//
//   1. The latest row with `terminal: true`. These are explicit
//      diagnostic rows from the runtime (the synced `task_run`
//      mirror, kind=run_result with detail like "LLM call failed
//      on turn 3"). They carry the most useful operator
//      information; if one exists, it should win.
//   2. Otherwise, the latest row whose type alone makes it
//      terminal-shaped (completed/failed/cancelled/run_result
//      without an explicit `terminal: true` flag). These are the
//      generic agent-chat-handler rows with titles like "Final
//      answer" / "Failed" / "Cancelled" — fine when nothing more
//      informative is available.
//   3. -1 when no terminal-shaped row exists; the dedupe filter
//      becomes a no-op and the timeline stays as-is.
function pickTerminalActivityIndex(activities: AgentChatActivityRecord[]): number {
  let lastByFlag = -1;
  let lastByShape = -1;
  for (let index = 0; index < activities.length; index += 1) {
    const activity = activities[index];
    if (activity.terminal === true) lastByFlag = index;
    if (isTerminalActivity(activity)) lastByShape = index;
  }
  if (lastByFlag !== -1) return lastByFlag;
  return lastByShape;
}

function compactDetailActivities(activities: AgentChatActivityRecord[], hasDiffStat: boolean): AgentChatActivityRecord[] {
  const detailTypes = new Set(["artifact", "changed_files", "final_answer", "output"]);
  return activities.filter(activity => {
    if (!detailTypes.has(activity.type)) return false;
    if (hasDiffStat && activity.type === "changed_files") return false;
    return true;
  });
}

function orderVisibleActivities(activities: AgentChatActivityRecord[]): AgentChatActivityRecord[] {
  return activities
    .map((activity, index) => ({ activity, index, time: activitySortTime(activity.created_at), phase: activitySortPhase(activity) }))
    .sort((a, b) => {
      if (a.time !== b.time) return a.time - b.time;
      if (a.phase !== b.phase) return a.phase - b.phase;
      return a.index - b.index;
    })
    .map(({ activity }) => activity);
}

function activitySortTime(value?: string): number {
  if (!value) return Number.MAX_SAFE_INTEGER;
  const parsed = new Date(value).getTime();
  return Number.isNaN(parsed) ? Number.MAX_SAFE_INTEGER : parsed;
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
  if (!terminalRunSummaryTypes.has(activity.type)) return false;
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
    const title = toolActivityTitle(activity);
    return { title, detail: cleanActivityDetail(activity) || fallbackToolDetail(activity, title) };
  }
  if (activity.type === "thinking" && isModelTurnActivity(activity)) {
    return { title: "Thinking", detail: modelTurnDetail(activity) };
  }
  if (activity.type === "model_turns") {
    return { title: "Thinking", detail: activity.detail };
  }
  if (activity.type === "files_changed") {
    return { title: "Files changed", detail: activity.detail };
  }
  if (activity.type === "artifact") {
    return { title: "Artifact", detail: cleanActivityDetail(activity) || activity.title };
  }
  if (activity.type === "output") {
    return { title: "Output", detail: outputActivityDetail(activity) };
  }
  if (activity.type === "changed_files") {
    return { title: "Changed files", detail: cleanActivityDetail(activity) || activity.title };
  }
  if (activity.type === "final_answer") {
    return { title: "Final answer artifact", detail: cleanActivityDetail(activity) || activity.title };
  }
  if (activity.type === "started" && /^Starting Hecate Agent$/i.test(activity.title.trim())) {
    return { title: "Starting agent", detail: cleanActivityDetail(activity) };
  }
  if (activity.type === "cancelled") {
    return { title: "Cancelled", detail: cleanActivityDetail(activity) || "stopped before the run finished" };
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
  const raw = stripStatusSuffix(activity.title || activity.kind || "tool").trim();
  const normalized = raw.toLowerCase();
  const kind = (activity.kind || activity.detail || "").trim().toLowerCase();

  if (/^call_[a-z0-9_-]+$/i.test(raw)) {
    if (kind.includes("execute") || kind.includes("command") || kind.includes("shell")) return "Ran command";
    if (kind.includes("read")) return "Read context";
    if (kind.includes("edit") || kind.includes("write")) return "Edited file";
    return "Used tool";
  }

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

  if (kind === "execute" || kind === "command") {
    return "Ran command";
  }
  if (kind === "read") {
    return "Read context";
  }

  return raw;
}

function modelTurnDetail(activity: AgentChatActivityRecord): string {
  const status = humanActivityStatus(activity.status);
  const turn = activity.title.match(/turn\s+(\d+)/i)?.[1];
  return turn ? `turn ${turn} ${status}` : status;
}

function fallbackToolDetail(activity: AgentChatActivityRecord, displayTitle: string): string | undefined {
  const raw = stripStatusSuffix(activity.title || "").trim();
  const opaqueID = opaqueToolCallID(raw);
  if (opaqueID) {
    const kind = activity.kind || activity.detail;
    return [toolKindLabel(kind), shortToolCallID(opaqueID)].filter(Boolean).join(" · ") || undefined;
  }
  if (!raw) return undefined;
  if (raw === displayTitle) return undefined;
  return raw;
}

function opaqueToolCallID(value: string): string | undefined {
  const match = value.match(/^call_([a-z0-9_-]+)$/i);
  return match?.[1];
}

function isOutputArtifactActivity(activity: AgentChatActivityRecord): boolean {
  const label = `${activity.title} ${activity.detail ?? ""} ${activity.kind ?? ""}`.toLowerCase();
  return /\b(std(out|err)|git-std(out|err))\b/.test(label);
}

function shortToolCallID(id: string): string {
  return `tool ${id.slice(0, 8)}`;
}

function toolKindLabel(kind?: string): string | undefined {
  const normalized = kind?.trim().toLowerCase();
  if (!normalized) return undefined;
  if (normalized.includes("execute") || normalized.includes("command") || normalized.includes("shell")) return "execute";
  if (normalized.includes("read")) return "read";
  if (normalized.includes("edit") || normalized.includes("write")) return "edit";
  return normalized.replaceAll("_", " ");
}

function outputActivityDetail(activity: AgentChatActivityRecord): string | undefined {
  const detail = cleanActivityDetail(activity) || activity.title;
  const size = formatBytes(activity.artifact_size_bytes);
  return [detail, size].filter(Boolean).join(" · ") || undefined;
}

function formatBytes(bytes?: number): string | undefined {
  if (bytes === undefined || bytes < 0) return undefined;
  if (bytes < 1024) return `${bytes}b`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(bytes < 10 * 1024 ? 1 : 0)}kb`;
  return `${(bytes / (1024 * 1024)).toFixed(1)}mb`;
}

function cleanApprovalDetail(detail?: string): string | undefined {
  const cleaned = detail
    ?.replace(/^Agent requested tools that require approval:\s*/i, "")
    .replace(/\s+-\s+(awaiting_approval|pending|approved|rejected|denied|cancelled|timed_out)$/i, "")
    .trim();
  if (!cleaned || cleaned.startsWith("builtin.agent_loop_")) return undefined;
  return cleaned || undefined;
}

function cleanActivityDetail(activity: AgentChatActivityRecord): string | undefined {
  const detail = activity.detail?.trim();
  const title = activity.title.trim();
  if (/^call_[a-z0-9_-]+$/i.test(title)) {
    if (!detail || /^(execute|read|write|edit|command)$/i.test(detail)) return undefined;
  }
  if (!detail) return undefined;
  if (detail.startsWith("builtin.agent_loop_")) return undefined;

  const baseTitle = stripStatusSuffix(title);
  const status = activity.status?.trim();
  const duplicateForms = [
    title,
    baseTitle,
    status,
    status ? `${title} - ${status}` : "",
    status ? `${title} · ${status}` : "",
    status ? `${baseTitle} - ${status}` : "",
    status ? `${baseTitle} · ${status}` : "",
  ].filter((value): value is string => Boolean(value)).map(value => value.toLowerCase());

  return duplicateForms.includes(detail.toLowerCase()) ? undefined : detail;
}

function stripStatusSuffix(value: string): string {
  return value.replace(/\s+\((running|completed|failed|cancelled|awaiting_approval|pending|approved|rejected|denied|timed_out)\)$/i, "");
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

// terminalAgentActivity returns the row that should represent the
// run's terminal status — the same row pickTerminalActivityIndex
// selects for the dedupe filter, so the TIMELINE summary (uses
// this) and the dedupe (uses pickTerminalActivityIndex) cannot
// disagree about which terminal row to surface. Previously the
// helper had a narrower set ({completed, failed, cancelled}) and
// could miss a `run_result`-typed terminal that pickTerminalActivityIndex
// would correctly pick — leading to the dedupe dropping the row
// the timeline summary needed.
function terminalAgentActivity(activities: AgentChatActivityRecord[]): AgentChatActivityRecord | undefined {
  const index = pickTerminalActivityIndex(activities);
  return index === -1 ? undefined : activities[index];
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

function detailSummaryLabel(details: AgentChatActivityRecord[]): string {
  const count = `${details.length} item${details.length === 1 ? "" : "s"}`;
  const hasOutput = details.some(activity => activity.type === "output" || /\bstd(out|err)\b/i.test(`${activity.title} ${activity.detail ?? ""}`));
  const hasArtifact = details.some(activity => activity.type === "artifact" || activity.type === "final_answer");
  if (hasOutput && hasArtifact) return `Output and artifacts · ${count}`;
  if (hasOutput) return `Output · ${count}`;
  if (hasArtifact) return `Artifacts · ${count}`;
  return `More details · ${count}`;
}

function activitySortPhase(activity: AgentChatActivityRecord): number {
  if (activity.type === "started") return 0;
  if (activity.type === "running") return 1;
  if (isTaskRunActivity(activity)) return 2;
  if (activity.type === "plan") return 3;
  if (activity.type === "thinking" || activity.type === "model_turns") return 4;
  if (activity.type === "approval") return 5;
  if (activity.type === "tool_call") return 6;
  if (activity.type === "files_changed") return 7;
  if (activity.type === "failed" || activity.type === "cancelled" || activity.type === "completed" || activity.type === "run_result") return 9;
  return 8;
}

function activityStatusColor(status?: string) {
  switch (status) {
  case "failed":
    return "var(--red)";
  case "cancelled":
    return "var(--amber)";
  case "awaiting_approval":
  case "pending":
  case "proposed":
    return "var(--amber)";
  case "running":
  case "in_progress":
    return "var(--teal)";
  default:
    return "var(--green)";
  }
}

function isActiveAgentActivity(activity: AgentChatActivityRecord): boolean {
  return activity.status === "running" || activity.status === "in_progress" || activity.status === "awaiting_approval" || activity.status === "pending" || Boolean(activity.needs_action);
}
