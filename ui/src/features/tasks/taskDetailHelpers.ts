import type { ChatActivityRecord } from "../../types/chat";
import type {
  TaskActivityRecord,
  TaskArtifactRecord,
  TaskRecord,
  TaskRunEventRecord,
  TaskRunRecord,
} from "../../types/task";

export const STEP_STATUS_COLOR: Record<string, string> = {
  completed: "var(--green)",
  running: "var(--teal)",
  awaiting_approval: "var(--amber)",
  failed: "var(--red)",
  cancelled: "var(--red)",
};

export function stepColor(status: string, result?: string) {
  if (result === "denied") return "var(--amber)";
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

export function isApprovalRejected(status: string, lastError?: string): boolean {
  return status === "cancelled" && (lastError || "").trim().toLowerCase() === "approval rejected";
}

export function taskBadgeProps(
  status: string,
  lastError?: string,
): { status: string; label?: string } {
  if (isApprovalRejected(status, lastError)) return { status: "cancelled", label: "rejected" };
  return { status: taskBadgeStatus(status) };
}

export type TaskSource = {
  kind: "standalone" | "chat" | "project_assignment";
  label: "Standalone" | "From chat" | "Project assignment";
  title: string;
};

export function taskSource(task: TaskRecord): TaskSource {
  const originKind = (task.origin_kind ?? "").trim();
  const originID = (task.origin_id ?? "").trim();
  const assignmentID = (task.assignment_id ?? "").trim();
  if (originKind === "chat") {
    return {
      kind: "chat",
      label: "From chat",
      title: originID ? `Created from chat ${originID}` : "Created from Hecate Chat",
    };
  }
  if (assignmentID || originKind === "project_work_item") {
    const sourceID = assignmentID || originID;
    return {
      kind: "project_assignment",
      label: "Project assignment",
      title: sourceID
        ? `Created from project assignment ${sourceID}`
        : "Created from a project assignment",
    };
  }
  return {
    kind: "standalone",
    label: "Standalone",
    title: "Created directly in Tasks",
  };
}

export type TaskChatSourceRef = {
  chatSessionID: string;
  turnID: string;
  messageID: string;
};

export function taskChatSourceRef(
  task: TaskRecord,
  run: TaskRunRecord | null,
): TaskChatSourceRef | null {
  const chatSessionID = (task.origin_id ?? "").trim();
  if ((task.origin_kind ?? "").trim() !== "chat" || !chatSessionID) return null;

  const sourceRef = run?.source_ref;
  if (
    !run ||
    run.task_id !== task.id ||
    sourceRef?.kind !== "chat_turn" ||
    sourceRef.chat_session_id.trim() !== chatSessionID
  ) {
    return { chatSessionID, turnID: "", messageID: "" };
  }

  const turnID = sourceRef.turn_id.trim();
  const messageID = sourceRef.message_id.trim();
  if (!turnID || !messageID) return { chatSessionID, turnID: "", messageID: "" };
  return { chatSessionID, turnID, messageID };
}

export type TaskRunOutcome = {
  label: "Last error" | "Outcome" | "Reason";
  value: string;
  tone: "muted" | "error" | "warning";
  detail?: string;
};

export function taskRunOutcome(status: string, lastError?: string): TaskRunOutcome {
  const error = (lastError || "").trim();
  if (isApprovalRejected(status, error)) {
    return {
      label: "Outcome",
      value: "Approval rejected",
      tone: "warning",
      detail: "The run was stopped because the pending approval was rejected.",
    };
  }
  if (status === "failed" && error) {
    return { label: "Last error", value: error, tone: "error" };
  }
  if (status === "cancelled") {
    return {
      label: "Reason",
      value: error || "Run cancelled",
      tone: error ? "warning" : "muted",
    };
  }
  return { label: "Last error", value: "—", tone: "muted" };
}

export function approvalCommandPreview(task: TaskRecord): string {
  if (task.execution_kind === "git" && task.git_command) return `git ${task.git_command}`;
  if (task.shell_command) return task.shell_command;
  if (task.file_path) return `${task.file_operation || "write"} ${task.file_path}`;
  return "";
}

export function describeRunEvent(eventType: string): {
  label: string;
  tone: "queued" | "running" | "awaiting" | "done" | "failed" | "warn";
} {
  const labels: Record<
    string,
    {
      label: string;
      tone: "queued" | "running" | "awaiting" | "done" | "failed" | "warn";
    }
  > = {
    "run.created": { label: "Run created", tone: "queued" },
    "run.queued": { label: "Queued", tone: "queued" },
    "run.started": { label: "Started", tone: "running" },
    "run.awaiting_approval": { label: "Approval wait", tone: "awaiting" },
    "run.cancelled": { label: "Cancelled", tone: "failed" },
    "run.failed": { label: "Failed", tone: "failed" },
    "run.finished": { label: "Completed", tone: "done" },
    "run.resumed_from_event": { label: "Resumed", tone: "running" },
    "gap.run_disconnected": { label: "Runtime recovered", tone: "queued" },
    "model.call.started": { label: "Model call started", tone: "running" },
    "model.call.completed": { label: "Model call done", tone: "done" },
    "assistant.tool_call_proposed": { label: "Tool proposed", tone: "queued" },
    "tool.invoked": { label: "Tool invoked", tone: "running" },
    "tool.started": { label: "Tool started", tone: "running" },
    "tool.shell_command": { label: "Shell command", tone: "running" },
    "tool.failed": { label: "Tool failed", tone: "failed" },
    "tool.completed": { label: "Tool done", tone: "done" },
    "policy.tool_blocked": { label: "Tool blocked", tone: "warn" },
    "approval.requested": { label: "Approval asked", tone: "awaiting" },
    "approval.resolved": { label: "Approval done", tone: "done" },
  };
  return labels[eventType] ?? { label: eventType.replaceAll("_", " "), tone: "queued" };
}

export function describeRunEventRecord(event: TaskRunEventRecord): {
  label: string;
  tone: "queued" | "running" | "awaiting" | "done" | "failed" | "warn";
} {
  const decision = runEventString(event, "decision").toLowerCase();
  if (event.type === "approval.resolved") {
    if (decision === "approved") return { label: "Approval granted", tone: "done" };
    if (decision === "rejected") return { label: "Approval rejected", tone: "awaiting" };
    if (decision === "cancelled") return { label: "Approval cancelled", tone: "failed" };
  }
  const reason = runEventString(event, "reason").toLowerCase();
  if (event.type === "run.cancelled" && reason === "approval rejected") {
    return { label: "Approval rejected", tone: "awaiting" };
  }
  return describeRunEvent(event.type);
}

function runEventString(event: TaskRunEventRecord, key: string): string {
  const value = event.data?.[key];
  return typeof value === "string" ? value.trim() : "";
}

export function isVisibleRunEvent(event: TaskRunEventRecord): boolean {
  return event.type !== "snapshot" && event.type !== "run.snapshot";
}

// describeRunEventNote extracts a human-readable annotation from an event's
// data payload. Returns null when there is nothing worth surfacing.
//
// Covers two axes:
//   source_model_call_index — the Run-local call a retry was branched at
//   reason                  — the operator's annotation for why they resumed/branched
//
// run.resumed_from_event stores the operator annotation under "reason".
export function describeRunEventNote(event: { data?: Record<string, unknown> }): string | null {
  const d = event.data;
  if (!d) return null;
  const modelCallIndex =
    typeof d["source_model_call_index"] === "number"
      ? (d["source_model_call_index"] as number)
      : null;
  const reason = (typeof d["reason"] === "string" ? d["reason"] : "").trim();
  if (!modelCallIndex && !reason) return null;
  const parts: string[] = [];
  if (modelCallIndex) parts.push(`source Run model call ${modelCallIndex}`);
  if (reason) parts.push(reason);
  return parts.join(" — ");
}

export function describeApprovalKind(kind: string): string {
  switch (kind) {
    case "shell_command":
      return "Shell execution";
    case "terminal_tool":
      return "Terminal tool";
    case "git_exec":
      return "Git execution";
    case "file_write":
      return "File write";
    case "network_egress":
      return "Network egress";
    case "agent_loop_tool_call":
      return "Agent tool call";
    default:
      return kind.replaceAll("_", " ");
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

export function failedToolOutputArtifacts(
  activity: TaskActivityRecord,
  outputArtifacts: OutputActivityIndex,
): TaskActivityRecord[] {
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
  const label =
    `${activity.kind ?? ""} ${activity.title ?? ""} ${activity.path ?? ""}`.toLowerCase();
  if (label.includes("stderr")) return "stderr";
  if (label.includes("stdout")) return "stdout";
  return "";
}

export function taskActivityAdvancedRows(
  activity: TaskActivityRecord,
): Array<{ label: string; value: string; multiline?: boolean }> {
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

export function taskActivityToTranscriptActivity(item: TaskActivityRecord): ChatActivityRecord {
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
    // TaskActivityItem.terminal means the individual item is settled for
    // steps and artifacts, while ChatActivityRecord.terminal identifies a
    // terminal run summary. Do not let completed timeline rows participate in
    // terminal-run deduplication.
    terminal: item.type === "run_result" ? item.terminal : undefined,
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
      if (item.needs_action || item.status === "pending" || item.status === "awaiting_approval")
        return "Waiting for approval";
      if (item.status === "approved") return "Approval granted";
      if (item.status === "rejected" || item.status === "denied") return "Approval rejected";
      return "Approval";
    case "artifact":
      if ((item.kind ?? "").trim() === "agent_conversation") return "Run model context";
      if (isOutputArtifactActivity(item)) return "Output";
      return "Artifact";
    case "changed_files":
      return "Changed files";
    case "final_answer":
      return "Final answer";
    case "patch":
      return "Patch";
    case "tool_call": {
      const tool = item.tool_name || item.title || item.path || "tool";
      return item.status === "denied" ? `Blocked ${tool}` : tool;
    }
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
        return [status === "denied" ? reason : "", item.path, command];
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
//   browser_evidence is rendered as a collapsible, text-only evidence panel
// Both would be redundant as bare chips, so we hide them.
export function isVisibleArtifactBadge(a: TaskArtifactRecord): boolean {
  return (
    a.kind !== "stdout" &&
    a.kind !== "stderr" &&
    a.kind !== "agent_conversation" &&
    a.kind !== "browser_evidence" &&
    a.kind !== "workflow_manifest" &&
    a.kind !== "workflow_report"
  );
}

export type QAWorkflowReport = {
  summaryMarkdown: string;
  agentOutcome: string;
  manifestArtifactID: string;
  workspacePosture: "read_only";
  nativeNetworkPosture: "blocked";
  mcpPosture: "blocked";
  gitEvidencePosture: "unavailable_in_v0";
};

// parseQAWorkflowReport accepts only the stable report envelope emitted by
// Hecate's report-only QA runbook. Artifact text is untrusted by default, so
// callers get null for malformed or future shapes and can render a plain-text
// fallback without treating arbitrary JSON as a result contract.
export function parseQAWorkflowReport(content: string | undefined): QAWorkflowReport | null {
  if (!content?.trim()) return null;
  let value: unknown;
  try {
    value = JSON.parse(content);
  } catch {
    return null;
  }
  if (
    !isRecord(value) ||
    value.schema_version !== "hecate.workflow_report.v0" ||
    value.runbook_id !== "builtin.qa.v0"
  )
    return null;
  if (
    !isRecord(value.workflow) ||
    value.workflow.mode !== "qa" ||
    value.workflow.version !== "v0" ||
    value.workflow.report_only !== true
  )
    return null;
  if (!isRecord(value.agent_reported) || value.agent_reported.outcome !== "reported") return null;
  if (typeof value.agent_reported.summary_markdown !== "string") return null;
  if (!isRecord(value.hecate_observed)) return null;
  const observed = value.hecate_observed;
  if (typeof observed.manifest_artifact_id !== "string") return null;
  if (
    observed.workspace_posture !== "read_only" ||
    observed.native_network_posture !== "blocked" ||
    observed.mcp_posture !== "blocked" ||
    observed.git_evidence_posture !== "unavailable_in_v0" ||
    observed.browser_evidence_posture !== "unavailable_in_v0"
  )
    return null;
  return {
    summaryMarkdown: value.agent_reported.summary_markdown,
    agentOutcome: value.agent_reported.outcome,
    manifestArtifactID: observed.manifest_artifact_id,
    workspacePosture: "read_only",
    nativeNetworkPosture: "blocked",
    mcpPosture: "blocked",
    gitEvidencePosture: "unavailable_in_v0",
  };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}
