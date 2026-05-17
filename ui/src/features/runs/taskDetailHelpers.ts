import type {
  AgentChatActivityRecord,
  TaskActivityRecord,
  TaskArtifactRecord,
  TaskRecord,
  TaskRunEventRecord,
} from "../../types/runtime";

export const STEP_STATUS_COLOR: Record<string, string> = {
  completed: "var(--green)",
  running:   "var(--teal)",
  awaiting_approval: "var(--amber)",
  failed:    "var(--red)",
  cancelled: "var(--red)",
};

export function stepColor(status: string) {
  return STEP_STATUS_COLOR[status] || "var(--t3)";
}

// MCP_TOOL_PREFIX mirrors mcpclient.PoolToolNamespacePrefix +
// PoolToolNamespaceSep on the gateway side. Tool calls dispatched
// through an external MCP server arrive here as steps whose
// tool_name follows this shape:
//
//   mcp__<server>__<tool>
//
// We split it so the timeline can render server + tool separately
// (operators want to see "github · create_pr", not the raw
// double-underscore string), and the StepDetail can break out
// transport / server / tool labels.
const MCP_TOOL_PREFIX = "mcp__";
const MCP_TOOL_SEP = "__";

export type MCPToolName = { server: string; tool: string };

// splitNamespacedToolName mirrors the Go-side SplitNamespacedToolName
// (internal/mcp/client/pool.go). Returns server + tool when the name
// matches the namespacing scheme, otherwise null. Tool names may
// themselves contain "__" — we honor only the FIRST split after the
// server segment, so `mcp__weird__double__under` parses as
// (weird, double__under), matching the gateway's split.
export function splitNamespacedToolName(name: string | undefined): MCPToolName | null {
  if (!name || !name.startsWith(MCP_TOOL_PREFIX)) return null;
  const rest = name.slice(MCP_TOOL_PREFIX.length);
  const idx = rest.indexOf(MCP_TOOL_SEP);
  if (idx <= 0) return null;
  const server = rest.slice(0, idx);
  const tool = rest.slice(idx + MCP_TOOL_SEP.length);
  if (!server || !tool) return null;
  return { server, tool };
}

export function taskBadgeStatus(status: string): string {
  if (status === "completed") return "done";
  if (status === "awaiting_approval") return "awaiting";
  return status;
}

export function approvalCommandPreview(task: TaskRecord): string {
  if (task.execution_kind === "git" && task.git_command) return `git ${task.git_command}`;
  if (task.shell_command) return task.shell_command;
  if (task.file_path) return `${task.file_operation || "write"} ${task.file_path}`;
  return "";
}

export function describeRunEvent(eventType: string): { label: string; tone: "queued" | "running" | "awaiting" | "done" | "failed" } {
  const labels: Record<string, { label: string; tone: "queued" | "running" | "awaiting" | "done" | "failed" }> = {
    "run.created": { label: "Run created", tone: "queued" },
    "run.queued": { label: "Queued", tone: "queued" },
    "run.started": { label: "Started", tone: "running" },
    "run.awaiting_approval": { label: "Approval wait", tone: "awaiting" },
    "run.cancelled": { label: "Cancelled", tone: "failed" },
    "run.failed": { label: "Failed", tone: "failed" },
    "run.finished": { label: "Completed", tone: "done" },
    "run.resumed_from_event": { label: "Resumed", tone: "running" },
    "gap.run_disconnected": { label: "Runtime recovered", tone: "queued" },
    "turn.started": { label: "Turn started", tone: "running" },
    "turn.completed": { label: "Turn done", tone: "done" },
    "assistant.tool_call_proposed": { label: "Tool proposed", tone: "queued" },
    "tool.invoked": { label: "Tool invoked", tone: "running" },
    "tool.started": { label: "Tool started", tone: "running" },
    "tool.shell_command": { label: "Shell command", tone: "running" },
    "tool.failed": { label: "Tool failed", tone: "failed" },
    "tool.completed": { label: "Tool done", tone: "done" },
    "approval.requested": { label: "Approval asked", tone: "awaiting" },
    "approval.resolved": { label: "Approval done", tone: "done" },
  };
  return labels[eventType] ?? { label: eventType.replaceAll("_", " "), tone: "queued" };
}

export function isVisibleRunEvent(event: TaskRunEventRecord): boolean {
  return event.type !== "snapshot" && event.type !== "run.snapshot";
}

// describeRunEventNote extracts a human-readable annotation from an event's
// data payload. Returns null when there is nothing worth surfacing.
//
// Covers two axes:
//   retry_from_turn — the turn number a retry-from-turn was branched at
//   reason          — the operator's annotation for why they resumed/branched
//
// run.resumed_from_event stores the operator annotation under "reason".
export function describeRunEventNote(event: { data?: Record<string, unknown> }): string | null {
  const d = event.data;
  if (!d) return null;
  const turn = typeof d["retry_from_turn"] === "number" ? (d["retry_from_turn"] as number) : null;
  const reason = (
    typeof d["reason"] === "string" ? d["reason"] : ""
  ).trim();
  if (!turn && !reason) return null;
  const parts: string[] = [];
  if (turn) parts.push(`turn ${turn}`);
  if (reason) parts.push(reason);
  return parts.join(" — ");
}

export function describeApprovalKind(kind: string): string {
  switch (kind) {
    case "shell_command":        return "Shell execution";
    case "git_exec":             return "Git execution";
    case "file_write":           return "File write";
    case "network_egress":       return "Network egress";
    case "agent_loop_tool_call": return "Agent tool call";
    default:                     return kind.replaceAll("_", " ");
  }
}

export type OutputActivityIndex = {
  all: TaskActivityRecord[];
  byStepID: Map<string, TaskActivityRecord[]>;
};

export function buildOutputActivityIndex(activity: TaskActivityRecord[]): OutputActivityIndex {
  const all: TaskActivityRecord[] = [];
  const byStepID = new Map<string, TaskActivityRecord[]>();
  const seen = new Set<string>();
  for (const item of activity) {
    if (item.type !== "artifact" || !isOutputArtifactActivity(item)) continue;
    const key = item.artifact_id || item.id;
    if (seen.has(key)) continue;
    seen.add(key);
    all.push(item);
    if (item.step_id) {
      const scoped = byStepID.get(item.step_id) ?? [];
      scoped.push(item);
      byStepID.set(item.step_id, scoped);
    }
  }
  return { all, byStepID };
}

export function failedToolOutputArtifacts(activity: TaskActivityRecord, outputArtifacts: OutputActivityIndex): TaskActivityRecord[] {
  if (activity.type !== "tool_call" || activity.status !== "failed") return [];
  const matchingStep = activity.step_id || "";
  if (matchingStep) {
    return outputArtifacts.byStepID.get(matchingStep) ?? [];
  }
  return outputArtifacts.all;
}

export function isOutputArtifactActivity(activity: TaskActivityRecord): boolean {
  return outputActivityStream(activity) !== "";
}

export function outputActivityStream(activity: TaskActivityRecord): "stdout" | "stderr" | "" {
  const label = `${activity.kind ?? ""} ${activity.title ?? ""} ${activity.path ?? ""}`.toLowerCase();
  if (label.includes("stderr")) return "stderr";
  if (label.includes("stdout")) return "stdout";
  return "";
}

export function taskActivityAdvancedRows(activity: TaskActivityRecord): Array<{ label: string; value: string; multiline?: boolean }> {
  const rows: Array<{ label: string; value: string; multiline?: boolean }> = [
    ["type", activity.type],
    ["status", activity.status],
    ["occurred", activity.occurred_at],
    ["activity", activity.id],
    ["step", activity.step_id],
    ["artifact", activity.artifact_id],
    ["approval", activity.approval_id],
    ["tool", activity.tool_name],
    ["kind", activity.kind],
    ["path", activity.path],
    ["needs action", activity.needs_action ? "yes" : ""],
    ["terminal", activity.terminal ? "yes" : ""],
  ]
    .filter((row): row is [string, string] => Boolean(row[1]))
    .map(([label, value]) => ({ label, value }));

  if (activity.summary && Object.keys(activity.summary).length > 0) {
    rows.push({
      label: "summary",
      value: JSON.stringify(activity.summary, null, 2),
      multiline: true,
    });
  }

  return rows;
}

export function taskActivityToTranscriptActivity(item: TaskActivityRecord): AgentChatActivityRecord {
  return {
    id: item.id,
    type: item.type,
    status: item.needs_action ? "awaiting_approval" : item.status,
    title: taskActivityTitle(item),
    kind: item.kind,
    detail: taskActivitySubtitle(item),
    created_at: item.occurred_at,
    artifact_id: item.artifact_id,
    artifact_size_bytes: taskActivityArtifactSize(item),
    artifact_preview: taskActivityArtifactPreview(item),
    approval_id: item.approval_id,
    needs_action: item.needs_action,
    terminal: item.terminal,
  };
}

export function taskActivityArtifactSize(item: TaskActivityRecord): number | undefined {
  const value = item.summary?.size_bytes;
  return typeof value === "number" ? value : undefined;
}

export function taskActivityArtifactPreview(item: TaskActivityRecord): string | undefined {
  const value = item.summary?.content_preview;
  return typeof value === "string" && value.trimEnd() ? value.replace(/[\r\n]+$/, "") : undefined;
}

export function artifactHasBytes(artifact: TaskArtifactRecord): boolean {
  if (typeof artifact.size_bytes === "number") return artifact.size_bytes > 0;
  return Boolean(artifact.content_text);
}

export function taskActivityTitle(item: TaskActivityRecord): string {
  switch (item.type) {
    case "approval":
      if (item.needs_action || item.status === "pending" || item.status === "awaiting_approval") return "Waiting for approval";
      if (item.status === "approved") return "Approval granted";
      if (item.status === "rejected" || item.status === "denied") return "Approval rejected";
      return "Approval";
    case "artifact":
      return "Artifact";
    case "changed_files":
      return "Changed files";
    case "final_answer":
      return "Final answer artifact";
    case "patch":
      return "Patch";
    case "tool_call":
      return item.tool_name || item.title || item.path || "tool";
    default:
      return item.title || item.tool_name || item.path || item.type.replaceAll("_", " ");
  }
}

export function taskActivitySubtitle(item: TaskActivityRecord): string | undefined {
  const status = item.status || "";
  const reason = summaryString(item, "reason");
  const command = summaryString(item, "command");
  const filename = item.path || item.title || "";
  const parts = (() => {
    switch (item.type) {
      case "approval":
        return [reason, nonInternalKind(item.kind)];
      case "tool_call":
        return [item.path, command];
      case "artifact":
      case "changed_files":
      case "final_answer":
        return [filename, status && status !== "ready" ? status : ""];
      case "patch":
        return [filename, status && status !== "ready" ? status : ""];
      default:
        return [item.path, nonInternalKind(item.kind), status];
    }
  })().filter(Boolean);
  return parts.join(" · ") || undefined;
}

export function summaryString(item: TaskActivityRecord, key: string): string {
  const value = item.summary?.[key];
  return typeof value === "string" ? value.trim() : "";
}

export function summaryNumber(item: TaskActivityRecord, key: string): number | undefined {
  const value = item.summary?.[key];
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

export function nonInternalKind(kind?: string): string {
  const value = kind?.trim() || "";
  return value.startsWith("builtin.agent_loop_") ? "" : value;
}

// Filters artifact chips that are redundant with other surfaces:
//   stdout / stderr are previewed inline under the failing tool call
//   agent_conversation is rendered as a chat-bubble timeline
// Both would be redundant as bare chips, so we hide them.
export function isVisibleArtifactBadge(a: TaskArtifactRecord): boolean {
  return a.kind !== "stdout" && a.kind !== "stderr" && a.kind !== "agent_conversation";
}
