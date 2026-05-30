import type { ChatActivityRecord } from "../../types/chat";

const terminalRunSummaryTypes = new Set(["run_result", "completed", "failed", "cancelled"]);

export function formatDiffStatSummary(diffStat: string): string {
  const lines = diffStat
    .split(/\\n|\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean);
  const summary = lines.find((line) => /\bfiles? changed\b/.test(line));
  if (summary) return summary;
  const rows = parseDiffStatRows(diffStat);
  if (rows.length > 0) return `${rows.length} changed file${rows.length === 1 ? "" : "s"}`;
  return compactInlineDetail(lines[0] || "");
}

export function parseDiffStatRows(diffStat: string): Array<{ path: string; change: string }> {
  return diffStat
    .split(/\\n|\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
    .filter((line) => !/\bfiles? changed\b/.test(line))
    .map((line) => {
      const match = line.match(/^(.+?)\s+\|\s+(.+)$/);
      if (!match) return null;
      return { path: match[1].trim(), change: match[2].trim() };
    })
    .filter((row): row is { path: string; change: string } => row !== null);
}

export function compactAgentActivities(
  activities: ChatActivityRecord[],
  hasDiffStat = false,
): ChatActivityRecord[] {
  const hiddenTypes = new Set(["artifact", "changed_files", "final_answer", "output"]);
  const terminalIndex = pickTerminalActivityIndex(activities);
  const lastTaskRunIndex = lastIndexOfTaskRunActivity(activities);
  const lastApprovalIndexByID = lastIndexByApprovalID(activities);
  const out: ChatActivityRecord[] = [];
  for (const [index, activity] of activities.entries()) {
    if (hiddenTypes.has(activity.type)) continue;
    if (hasDiffStat && activity.type === "files_changed") continue;
    if (activity.type === "completed" && activity.title.toLowerCase() === "final answer") continue;
    if (isTerminalRunSummary(activity)) continue;
    // Drop terminal-shaped rows that aren't the chosen one. The
    // chooser prefers a diagnostic `terminal: true` row over a
    // generic agent-chat-handler row (see pickTerminalActivityIndex)
    // so an informative "LLM call failed on turn 3" beats a
    // bare-bones "Failed". When no row is chosen we keep them all.
    if (terminalIndex !== -1 && index !== terminalIndex && isTerminalActivity(activity)) continue;
    if (terminalIndex !== -1 && (activity.type === "started" || activity.type === "running"))
      continue;
    if (activity.type === "running" && activities.some((item) => item.type === "output")) continue;
    if (isTaskRunActivity(activity) && index !== lastTaskRunIndex) continue;
    if (
      activity.type === "approval" &&
      activity.approval_id &&
      lastApprovalIndexByID.get(activity.approval_id) !== index
    )
      continue;
    out.push(activity);
  }
  return collapseModelTurnActivities(out);
}

export function summarizeTimelineActivities(
  activities: ChatActivityRecord[],
): ChatActivityRecord[] {
  const commandActivities = activities.filter(isGenericCommandToolActivity);
  if (commandActivities.length < 4) return activities;

  const firstCommandIndex = activities.findIndex(isGenericCommandToolActivity);
  const status = aggregateActivityStatus(commandActivities);
  const failed = commandActivities.filter(
    (activity) => activityEffectiveStatus(activity) === "failed",
  ).length;
  const detail = [
    failed > 0 ? `${failed} failed` : "",
    commandActivities.some(toolDetailHasOutput) ? "output captured" : "",
  ]
    .filter(Boolean)
    .join(" · ");
  const summary: ChatActivityRecord = {
    id: "hecate-agent:commands-summary",
    type: "tool_group",
    status,
    title: `Ran ${commandActivities.length} commands`,
    detail: detail || undefined,
    children: commandActivities,
  };

  const out = activities.filter((activity) => !isGenericCommandToolActivity(activity));
  out.splice(Math.max(firstCommandIndex, 0), 0, summary);
  return out;
}

// isTerminalActivity is the canonical predicate for "this activity
// represents a terminal status." Used both by the dedupe filter and
// by pickTerminalActivityIndex so the two cannot disagree on what
// counts as terminal — an earlier mismatch let `terminalAgentActivity`
// (which only looked at completed/failed/cancelled+terminal) pick
// the wrong row when a `run_result`-typed terminal arrived later.
export function isTerminalActivity(activity: ChatActivityRecord): boolean {
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
export function pickTerminalActivityIndex(activities: ChatActivityRecord[]): number {
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

export function compactDetailActivities(
  activities: ChatActivityRecord[],
  hasDiffStat: boolean,
): ChatActivityRecord[] {
  const detailTypes = new Set(["artifact", "changed_files", "final_answer", "output"]);
  return activities.filter((activity) => {
    if (!detailTypes.has(activity.type)) return false;
    if (hasDiffStat && activity.type === "changed_files") return false;
    if (
      activity.type === "artifact" &&
      isOutputArtifactActivity(activity) &&
      !hasOutputPreview(activity)
    )
      return false;
    if (activity.type === "output" && !hasOutputPreview(activity)) return false;
    return true;
  });
}

export function orderVisibleActivities(activities: ChatActivityRecord[]): ChatActivityRecord[] {
  return activities
    .map((activity, index) => ({
      activity,
      index,
      time: activitySortTime(activity.created_at),
      phase: activitySortPhase(activity),
    }))
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

function lastIndexByApprovalID(activities: ChatActivityRecord[]): Map<string, number> {
  const out = new Map<string, number>();
  for (const [index, activity] of activities.entries()) {
    if (activity.type === "approval" && activity.approval_id) {
      out.set(activity.approval_id, index);
    }
  }
  return out;
}

function lastIndexOfTaskRunActivity(activities: ChatActivityRecord[]): number {
  for (let index = activities.length - 1; index >= 0; index -= 1) {
    if (isTaskRunActivity(activities[index])) return index;
  }
  return -1;
}

function isTaskRunActivity(activity: ChatActivityRecord): boolean {
  return (
    activity.type === "task_run" ||
    (activity.type.startsWith("task_") && activity.title.startsWith("Task run"))
  );
}

function isTerminalRunSummary(activity: ChatActivityRecord): boolean {
  if (!terminalRunSummaryTypes.has(activity.type)) return false;
  return /^run\s+(completed|failed|cancelled)$/i.test(activity.title.trim());
}

function collapseModelTurnActivities(activities: ChatActivityRecord[]): ChatActivityRecord[] {
  const turnActivities = activities.filter(isModelTurnActivity);
  if (turnActivities.length <= 1) return activities;

  const firstTurnIndex = activities.findIndex(isModelTurnActivity);
  const status = aggregateActivityStatus(turnActivities);
  const collapsed: ChatActivityRecord = {
    id: "hecate-agent:model-turns",
    type: "model_turns",
    status,
    kind: "model",
    title: "Thinking",
    detail: `${turnActivities.length} model turns ${humanActivityStatus(status)}`,
  };

  const out = activities.filter((activity) => !isModelTurnActivity(activity));
  out.splice(Math.max(firstTurnIndex, 0), 0, collapsed);
  return out;
}

function isModelTurnActivity(activity: ChatActivityRecord): boolean {
  return activity.type === "thinking" && /^Agent turn \d+/i.test(activity.title.trim());
}

function aggregateActivityStatus(activities: ChatActivityRecord[]): string {
  if (activities.some((activity) => activityEffectiveStatus(activity) === "failed"))
    return "failed";
  if (activities.some((activity) => activityEffectiveStatus(activity) === "cancelled"))
    return "cancelled";
  if (activities.some(isActiveAgentActivity)) return "running";
  if (activities.every((activity) => activityEffectiveStatus(activity) === "completed"))
    return "completed";
  return activityEffectiveStatus(activities[activities.length - 1]) || "completed";
}

export function activityEffectiveStatus(activity?: ChatActivityRecord): string {
  if (!activity) return "completed";
  if (activity.status === "completed" && hasFailureLikeToolOutput(activity)) return "failed";
  return activity.status || "completed";
}

export function activityDisplay(activity: ChatActivityRecord): { title: string; detail?: string } {
  if (activity.type === "approval") {
    const title = approvalActivityTitle(activity);
    const detail = cleanApprovalDetail(activity.detail);
    return { title, detail };
  }
  if (activity.type === "tool_call") {
    const title = toolActivityTitle(activity);
    return { title, detail: toolActivityDetail(activity, title) };
  }
  if (activity.type === "tool_group") {
    return { title: activity.title, detail: cleanActivityDetail(activity) };
  }
  if (activity.type === "thinking" && isModelTurnActivity(activity)) {
    return { title: "Thinking", detail: modelTurnDetail(activity) };
  }
  if (activity.type === "thinking") {
    return { title: "Thinking" };
  }
  if (activity.type === "model_turns") {
    return { title: "Thinking", detail: activity.detail };
  }
  if (activity.type === "files_changed") {
    return { title: "Workspace changes", detail: activity.detail };
  }
  if (activity.type === "artifact") {
    return { title: "Artifact", detail: cleanActivityDetail(activity) || activity.title };
  }
  if (activity.type === "output") {
    return { title: "Output", detail: outputActivityDetail(activity) };
  }
  if (activity.type === "changed_files") {
    return {
      title: "Workspace changes",
      detail: formatDiffStatSummary(cleanActivityDetail(activity) || activity.title),
    };
  }
  if (activity.type === "final_answer") {
    return {
      title: "Final answer artifact",
      detail: cleanActivityDetail(activity) || activity.title,
    };
  }
  if (activity.type === "started" && /^Starting Hecate Chat tools$/i.test(activity.title.trim())) {
    return { title: "Starting agent", detail: cleanActivityDetail(activity) };
  }
  if (activity.type === "cancelled") {
    return {
      title: "Cancelled",
      detail: cleanActivityDetail(activity) || "stopped before the run finished",
    };
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

export function activityLinePrefix(activity: ChatActivityRecord): string | undefined {
  switch (activity.type) {
    case "tool_call":
      if (isThinkingToolActivity(activity)) return "model";
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

function toolActivityTitle(activity: ChatActivityRecord): string {
  if (isAdapterContextReadFailure(activity)) return "Could not read context";
  if (isThinkingToolActivity(activity)) return "Thinking";

  const raw = stripStatusSuffix(activity.title || activity.kind || "tool").trim();
  const normalized = raw.toLowerCase();
  const kind = (activity.kind || activity.detail || "").trim().toLowerCase();

  if (opaqueToolCallID(raw)) {
    if (kind.includes("execute") || kind.includes("command") || kind.includes("shell"))
      return "Ran command";
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

function toolActivityDetail(
  activity: ChatActivityRecord,
  displayTitle: string,
): string | undefined {
  if (isAdapterContextReadFailure(activity)) {
    return "adapter session file was unavailable";
  }
  if (isThinkingToolActivity(activity)) return undefined;
  const detail = cleanActivityDetail(activity) || fallbackToolDetail(activity, displayTitle);
  if (!detail) return undefined;
  return compactToolOutputDetail(detail);
}

export function capturedToolOutput(activity: ChatActivityRecord): string | undefined {
  if (activity.type !== "tool_call") return undefined;
  const preview = activity.artifact_preview?.trimEnd();
  const detail = activity.detail?.trim();
  const parsed = detail ? parseToolOutputDetail(detail)?.output : undefined;
  if (preview && parsed) return parsed.length > preview.length ? parsed : preview;
  return preview || parsed;
}

function isThinkingToolActivity(activity: ChatActivityRecord): boolean {
  if (activity.type !== "tool_call") return false;
  const kind = (activity.kind || "").trim().toLowerCase();
  const title = stripStatusSuffix(activity.title || "")
    .trim()
    .toLowerCase();
  return kind === "think" || title === "think";
}

function modelTurnDetail(activity: ChatActivityRecord): string {
  const status = humanActivityStatus(activity.status);
  const turn = activity.title.match(/turn\s+(\d+)/i)?.[1];
  return turn ? `turn ${turn} ${status}` : status;
}

function fallbackToolDetail(
  activity: ChatActivityRecord,
  displayTitle: string,
): string | undefined {
  const raw = stripStatusSuffix(activity.title || "").trim();
  const opaqueID = opaqueToolCallID(raw);
  if (opaqueID) {
    const kind = activity.kind || activity.detail;
    return (
      [toolKindLabel(kind), shortToolCallID(opaqueID)].filter(Boolean).join(" · ") || undefined
    );
  }
  if (!raw) return undefined;
  if (raw === displayTitle) return undefined;
  return raw;
}

function isGenericCommandToolActivity(activity: ChatActivityRecord): boolean {
  if (activity.type !== "tool_call") return false;
  return toolActivityTitle(activity) === "Ran command";
}

function isAdapterContextReadFailure(activity: ChatActivityRecord): boolean {
  if (activity.type !== "tool_call" || activity.status !== "failed") return false;
  const label = `${activity.title} ${activity.kind ?? ""} ${activity.detail ?? ""}`.toLowerCase();
  return label.includes("failed to read file:") && label.includes("/.grok/sessions/");
}

function toolDetailHasOutput(activity: ChatActivityRecord): boolean {
  return Boolean(capturedToolOutput(activity)) || /\boutput\s*:/i.test(activity.detail ?? "");
}

function hasFailureLikeToolOutput(activity: ChatActivityRecord): boolean {
  if (activity.type !== "tool_call") return false;
  const output = capturedToolOutput(activity) ?? activity.detail ?? "";
  // Some ACP adapters report the tool call as completed even when the
  // command itself printed a fatal error. Keep this inference narrow:
  // it is a UI tone, not a persisted status rewrite.
  return /(^|\s)(fatal|panic):/i.test(output) || /\bexit(?:ed)?\s+code\s+[1-9]\d*/i.test(output);
}

function compactToolOutputDetail(detail: string): string | undefined {
  const parsed = parseToolOutputDetail(detail);
  if (!parsed) return detail;

  const { prefix, output } = parsed;
  if (/^failed to read file:/i.test(output)) return `${prefix} · read failed`;
  if (/^cannot read binary file:/i.test(output)) return `${prefix} · binary file skipped`;
  return simpleToolOutputPrefix(prefix) ? undefined : prefix;
}

function simpleToolOutputPrefix(prefix: string): boolean {
  return /^(execute|read|write|edit|command|shell)$/i.test(prefix.trim());
}

function parseToolOutputDetail(detail: string): { prefix: string; output: string } | undefined {
  const match = detail.match(/^(.+?)\s*·\s*output:\s*(.*)$/is);
  if (!match) return undefined;
  return {
    prefix: match[1].trim(),
    output: match[2].trim(),
  };
}

function opaqueToolCallID(value: string): string | undefined {
  const match = value.match(/^(?:call|toolu)_([a-z0-9_-]+)$/i);
  return match?.[1];
}

export function isOutputArtifactActivity(activity: ChatActivityRecord): boolean {
  const label = `${activity.title} ${activity.detail ?? ""} ${activity.kind ?? ""}`.toLowerCase();
  return /\b(std(out|err)|git-std(out|err))\b/.test(label);
}

function shortToolCallID(id: string): string {
  return `tool ${id.slice(0, 8)}`;
}

function toolKindLabel(kind?: string): string | undefined {
  const normalized = kind?.trim().toLowerCase();
  if (!normalized) return undefined;
  if (
    normalized.includes("execute") ||
    normalized.includes("command") ||
    normalized.includes("shell")
  )
    return "execute";
  if (normalized.includes("read")) return "read";
  if (normalized.includes("edit") || normalized.includes("write")) return "edit";
  return normalized.replaceAll("_", " ");
}

function outputActivityDetail(activity: ChatActivityRecord): string | undefined {
  const preview = activity.artifact_preview?.trim();
  const lineCount = preview ? formatTextLineCount(preview) : undefined;
  const stream = outputActivityLabel(activity);
  const size = formatBytes(activity.artifact_size_bytes);
  return [stream, lineCount, size].filter(Boolean).join(" · ") || undefined;
}

function hasOutputPreview(activity: ChatActivityRecord): boolean {
  return Boolean(activity.artifact_preview?.trim());
}

function outputActivityLabel(activity: ChatActivityRecord): string | undefined {
  const label = `${activity.title} ${activity.detail ?? ""} ${activity.kind ?? ""}`.toLowerCase();
  if (label.includes("stderr")) return "stderr";
  if (label.includes("stdout")) return "stdout";
  const title = activity.title.trim();
  if (!title || title.toLowerCase() === "output") return undefined;
  return compactInlineDetail(title);
}

function formatTextLineCount(text: string): string {
  const count = text.split(/\r?\n/).length;
  return `${count} line${count === 1 ? "" : "s"}`;
}

function compactInlineDetail(value: string, max = 72): string {
  const oneLine = value.replace(/\s+/g, " ").trim();
  if (oneLine.length <= max) return oneLine;
  return `${oneLine.slice(0, max - 1)}…`;
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
    .replace(
      /\s+-\s+(awaiting_approval|pending|approved|rejected|denied|cancelled|timed_out)$/i,
      "",
    )
    .trim();
  if (!cleaned || cleaned.startsWith("builtin.agent_loop_")) return undefined;
  return cleaned || undefined;
}

function cleanActivityDetail(activity: ChatActivityRecord): string | undefined {
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
  ]
    .filter((value): value is string => Boolean(value))
    .map((value) => value.toLowerCase());

  return duplicateForms.includes(detail.toLowerCase()) ? undefined : detail;
}

function stripStatusSuffix(value: string): string {
  return value.replace(
    /\s+\((running|completed|failed|cancelled|awaiting_approval|pending|approved|rejected|denied|timed_out)\)$/i,
    "",
  );
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

function approvalActivityTitle(activity: ChatActivityRecord): string {
  if (
    activity.needs_action ||
    activity.status === "awaiting_approval" ||
    activity.status === "pending"
  ) {
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
export function terminalAgentActivity(
  activities: ChatActivityRecord[],
): ChatActivityRecord | undefined {
  const index = pickTerminalActivityIndex(activities);
  return index === -1 ? undefined : activities[index];
}

export function terminalStatusLabel(status?: string): string {
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

export function detailSummaryLabel(details: ChatActivityRecord[]): string {
  const count = `${details.length} item${details.length === 1 ? "" : "s"}`;
  const hasOutput = details.some(
    (activity) =>
      activity.type === "output" ||
      /\bstd(out|err)\b/i.test(`${activity.title} ${activity.detail ?? ""}`),
  );
  const hasArtifact = details.some(
    (activity) => activity.type === "artifact" || activity.type === "final_answer",
  );
  if (hasOutput && hasArtifact) return `Output and artifacts · ${count}`;
  if (hasOutput) return `Output · ${count}`;
  if (hasArtifact) return `Artifacts · ${count}`;
  return `More details · ${count}`;
}

function activitySortPhase(activity: ChatActivityRecord): number {
  if (activity.type === "started") return 0;
  if (activity.type === "running") return 1;
  if (isTaskRunActivity(activity)) return 2;
  if (activity.type === "plan") return 3;
  if (activity.type === "thinking" || activity.type === "model_turns") return 4;
  if (activity.type === "approval") return 5;
  if (activity.type === "tool_call") return 6;
  if (activity.type === "files_changed") return 7;
  if (
    activity.type === "failed" ||
    activity.type === "cancelled" ||
    activity.type === "completed" ||
    activity.type === "run_result"
  )
    return 9;
  return 8;
}

export function activityStatusColor(status?: string) {
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

export function isActiveAgentActivity(activity: ChatActivityRecord): boolean {
  return (
    activity.status === "running" ||
    activity.status === "in_progress" ||
    activity.status === "awaiting_approval" ||
    activity.status === "pending" ||
    Boolean(activity.needs_action)
  );
}
