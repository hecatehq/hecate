import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import type { AgentChatActivityRecord, TaskActivityRecord, TaskApprovalRecord, TaskArtifactRecord, TaskRecord, TaskRunEventRecord, TaskRunRecord, TaskStepRecord } from "../../types/runtime";
import { Badge, BrandAvatar, Dot, Icon, Icons, Modal } from "../shared/ui";
import { TranscriptActivityTimeline } from "../transcript/TranscriptActivityTimeline";

type StreamState = "idle" | "connecting" | "live" | "closed" | "error";

const STEP_STATUS_COLOR: Record<string, string> = {
  completed: "var(--green)",
  running:   "var(--teal)",
  awaiting_approval: "var(--amber)",
  failed:    "var(--red)",
  cancelled: "var(--red)",
};
function stepColor(status: string) { return STEP_STATUS_COLOR[status] || "var(--t3)"; }

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

// splitNamespacedToolName mirrors the Go-side SplitNamespacedToolName
// (internal/mcp/client/pool.go). Returns server + tool when the name
// matches the namespacing scheme, otherwise null. Tool names may
// themselves contain "__" — we honor only the FIRST split after the
// server segment, so `mcp__weird__double__under` parses as
// (weird, double__under), matching the gateway's split.
type MCPToolName = { server: string; tool: string };
function splitNamespacedToolName(name: string | undefined): MCPToolName | null {
  if (!name || !name.startsWith(MCP_TOOL_PREFIX)) return null;
  const rest = name.slice(MCP_TOOL_PREFIX.length);
  const idx = rest.indexOf(MCP_TOOL_SEP);
  if (idx <= 0) return null;
  const server = rest.slice(0, idx);
  const tool = rest.slice(idx + MCP_TOOL_SEP.length);
  if (!server || !tool) return null;
  return { server, tool };
}

function taskBadgeStatus(status: string): string {
  if (status === "completed") return "done";
  if (status === "awaiting_approval") return "awaiting";
  return status;
}

// formatMicrosUSD renders a µUSD amount as a $-prefixed number.
// Hecate stores LLM cost in millionths of a dollar (µUSD) for
// integer math precision; the UI swaps to fractional dollars for
// display. Three decimal places lets sub-cent amounts (common for a
// few-token call) render meaningfully without exploding to scientific
// notation.
function formatMicrosUSD(micros: number): string {
  return `$${(micros / 1_000_000).toFixed(3)}`;
}

// RunCostBadge shows this run's cost — and, when prior runs exist
// in the resume chain, the task-cumulative figure too. Operators
// scan it to spot runaway spend across a chain of resumes/retries.
function RunCostBadge({ run }: { run: TaskRunRecord }) {
  const total = run.total_cost_micros_usd ?? 0;
  const prior = run.prior_cost_micros_usd ?? 0;
  const cumulative = total + prior;
  // Whole-task figure only adds value when prior > 0; otherwise
  // it's identical to total and would just be visual noise.
  const showCumulative = prior > 0;
  const tooltip = showCumulative
    ? `This run: ${formatMicrosUSD(total)} · Prior runs in resume chain: ${formatMicrosUSD(prior)} · Task total: ${formatMicrosUSD(cumulative)}`
    : `LLM spend for this run: ${formatMicrosUSD(total)}`;
  return (
    <span
      title={tooltip}
      style={{
        fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t2)",
        background: "var(--bg2)", border: "1px solid var(--border)",
        borderRadius: "var(--radius-sm)", padding: "1px 6px",
      }}
    >
      {formatMicrosUSD(total)}
      {showCumulative && (
        <span style={{ color: "var(--t3)" }}> / {formatMicrosUSD(cumulative)} task</span>
      )}
    </span>
  );
}

// CostCeilingBanner is the inline affordance shown when a run failed
// specifically because of the per-task cost ceiling. Surfaces what
// was spent vs. the ceiling, suggests a doubled value as a sensible
// next step, and pairs the budget update with the resume in a
// single server call. The banner only renders for runs whose
// otel_status_message is "cost_ceiling_exceeded" — TaskDetail gates
// rendering, this component just owns the inline form.
function CostCeilingBanner({
  run,
  task,
  busy,
  onResumeRaisingCeiling,
}: {
  run: TaskRunRecord;
  task: TaskRecord;
  busy: boolean;
  onResumeRaisingCeiling: (budgetMicrosUSD: number) => void;
}) {
  const currentCeilingMicros = task.budget_micros_usd ?? 0;
  // Pre-fill with double the current ceiling — common operator move
  // ("clearly underestimated, give it more room"). Operators can
  // type any value >= the current ceiling. Stored as a USD string
  // so the input retains "1.50" rather than collapsing to "1.5".
  const defaultRaisedUSD = (() => {
    if (currentCeilingMicros > 0) return ((currentCeilingMicros * 2) / 1_000_000).toFixed(3);
    return "";
  })();
  const [raisedUSD, setRaisedUSD] = useState(defaultRaisedUSD);

  const totalSpentMicros = (run.total_cost_micros_usd ?? 0) + (run.prior_cost_micros_usd ?? 0);
  const proposedMicros = Math.max(0, Math.round(parseFloat(raisedUSD || "0") * 1_000_000));
  const isValid = proposedMicros >= currentCeilingMicros && proposedMicros > 0;

  return (
    <div style={{ margin: "14px 16px", border: "1px solid var(--amber-border)", borderRadius: "var(--radius)", background: "var(--amber-bg)", overflow: "hidden" }}>
      <div style={{ padding: "10px 14px", borderBottom: "1px solid var(--amber-border)", display: "flex", alignItems: "center", gap: 8 }}>
        <Icon d={Icons.warning} size={15} />
        <span style={{ fontWeight: 500, color: "var(--amber)", fontSize: 13 }}>Cost ceiling exceeded</span>
        <span style={{ fontSize: 11, color: "var(--amber-lo)", fontFamily: "var(--font-mono)", marginLeft: "auto" }}>
          spent {formatMicrosUSD(totalSpentMicros)} · ceiling {formatMicrosUSD(currentCeilingMicros)}
        </span>
      </div>
      <div style={{ padding: "12px 14px" }}>
        <div style={{ fontSize: 12, color: "var(--amber)", marginBottom: 10 }}>
          The agent loop hit the per-task budget. Raise the ceiling and resume to continue from where it stopped. The new ceiling persists on the task and applies to every future run.
        </div>
        <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
          <label style={{ fontSize: 11, color: "var(--t2)", fontFamily: "var(--font-mono)" }}>NEW CEILING</label>
          <div style={{ display: "flex", alignItems: "center", background: "var(--bg0)", border: "1px solid var(--border)", borderRadius: "var(--radius-sm)", padding: "0 8px" }}>
            <span style={{ fontFamily: "var(--font-mono)", fontSize: 12, color: "var(--t3)", marginRight: 4 }}>$</span>
            <input
              className="input"
              style={{ border: "none", background: "transparent", padding: "5px 0", width: 90, fontFamily: "var(--font-mono)" }}
              type="number"
              step="0.01"
              min={(currentCeilingMicros / 1_000_000).toFixed(3)}
              value={raisedUSD}
              onChange={e => setRaisedUSD(e.target.value)}
              onKeyDown={e => { if (e.key === "Enter" && isValid && !busy) onResumeRaisingCeiling(proposedMicros); }}
            />
          </div>
          <button
            className="btn btn-primary btn-sm"
            disabled={busy || !isValid}
            onClick={() => onResumeRaisingCeiling(proposedMicros)}
            title={!isValid ? `Must be >= ${formatMicrosUSD(currentCeilingMicros)}` : undefined}
            style={{ gap: 5 }}
          >
            <Icon d={Icons.refresh} size={13} />
            Raise ceiling & resume
          </button>
        </div>
        {!isValid && raisedUSD !== "" && (
          <div style={{ fontSize: 10, color: "var(--red)", fontFamily: "var(--font-mono)", marginTop: 6 }}>
            Must be at least {formatMicrosUSD(currentCeilingMicros)} (the current ceiling).
          </div>
        )}
      </div>
    </div>
  );
}

function TaskApprovalCallout({
  task,
  approvals,
  busyAction,
  onResolveApproval,
}: {
  task: TaskRecord;
  approvals: TaskApprovalRecord[];
  busyAction: string;
  onResolveApproval: (approval: TaskApprovalRecord, decision: "approve" | "reject") => void;
}) {
  return (
    <div
      data-testid="task-approval-callout"
      style={{
        flexShrink: 0,
        borderBottom: "1px solid var(--amber-border)",
        background: "var(--amber-bg)",
        padding: "12px 16px",
        display: "flex",
        flexDirection: "column",
        gap: 10,
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <Icon d={Icons.warning} size={15} />
        <div style={{ minWidth: 0, flex: 1 }}>
          <div style={{ fontWeight: 600, color: "var(--amber)", fontSize: 13 }}>
            {approvals.length === 1 ? "Approval required" : `${approvals.length} approvals required`}
          </div>
          <div style={{ color: "var(--amber-lo)", fontSize: 11, fontFamily: "var(--font-mono)", marginTop: 2 }}>
            This run is paused until you approve or deny the pending action.
          </div>
        </div>
      </div>

      {approvals.map(approval => (
        <div
          key={approval.id}
          style={{
            display: "grid",
            gridTemplateColumns: "minmax(0, 1fr) auto",
            gap: 12,
            alignItems: "center",
            border: "1px solid var(--amber-border)",
            borderRadius: "var(--radius)",
            background: "rgba(0,0,0,0.16)",
            padding: "10px 12px",
          }}
        >
          <div style={{ minWidth: 0 }}>
            <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
              <Badge status="awaiting" label={describeApprovalKind(approval.kind)} />
              {approval.requested_by && (
                <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t2)" }}>
                  requested by <span style={{ color: "var(--t1)" }}>{approval.requested_by}</span>
                </span>
              )}
              {approval.created_at && (
                <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
                  {new Date(approval.created_at).toLocaleTimeString()}
                </span>
              )}
            </div>
            {approval.reason && (
              <div style={{ color: "var(--amber)", fontSize: 12, marginTop: 6, lineHeight: 1.45 }}>
                {approval.reason}
              </div>
            )}
            {approvalCommandPreview(task) && (
              <div style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t1)", marginTop: 8, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                {approvalCommandPreview(task)}
              </div>
            )}
          </div>
          <div style={{ display: "flex", gap: 8, justifyContent: "flex-end" }}>
            <button className="btn btn-primary btn-sm" disabled={busyAction !== ""} onClick={() => onResolveApproval(approval, "approve")} style={{ gap: 5 }}>
              <Icon d={Icons.approve} size={13} /> Approve
            </button>
            <button className="btn btn-danger btn-sm" disabled={busyAction !== ""} onClick={() => onResolveApproval(approval, "reject")} style={{ gap: 5 }}>
              <Icon d={Icons.deny} size={13} /> Deny
            </button>
          </div>
        </div>
      ))}
    </div>
  );
}

function approvalCommandPreview(task: TaskRecord): string {
  if (task.execution_kind === "git" && task.git_command) return `git ${task.git_command}`;
  if (task.shell_command) return task.shell_command;
  if (task.file_path) return `${task.file_operation || "write"} ${task.file_path}`;
  return "";
}

function formatDuration(start?: string, end?: string): string {
  if (!start) return "";
  const startMs = new Date(start).getTime();
  const endMs = end ? new Date(end).getTime() : Date.now();
  const seconds = Math.max(0, (endMs - startMs) / 1000);
  if (seconds < 1) return `${Math.round(seconds * 1000)}ms`;
  if (seconds < 60) return `${seconds.toFixed(1)}s`;
  return `${Math.floor(seconds / 60)}m ${Math.round(seconds % 60)}s`;
}

function describeRunEvent(eventType: string): { label: string; tone: "queued" | "running" | "awaiting" | "done" | "failed" } {
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

function isVisibleRunEvent(event: TaskRunEventRecord): boolean {
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
function describeRunEventNote(event: { data?: Record<string, unknown> }): string | null {
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

function describeApprovalKind(kind: string): string {
  switch (kind) {
    case "shell_command":        return "Shell execution";
    case "git_exec":             return "Git execution";
    case "file_write":           return "File write";
    case "network_egress":       return "Network egress";
    case "agent_loop_tool_call": return "Agent tool call";
    default:                     return kind.replaceAll("_", " ");
  }
}

type Props = {
  task: TaskRecord;
  run: TaskRunRecord | null;
  runs: TaskRunRecord[];
  selectedRunID: string;
  events: TaskRunEventRecord[];
  steps: TaskStepRecord[];
  artifacts: TaskArtifactRecord[];
  activity: TaskActivityRecord[];
  approvals: TaskApprovalRecord[];
  // streamTurnCosts holds per-turn LLM spend pushed by the SSE stream
  // (one entry per `turn.completed` event). Used as a fallback
  // for the model-step output_summary path so old runs or steps
  // missing the cost field still show a per-turn figure.
  streamTurnCosts: Map<number, number>;
  streamState: StreamState;
  busyAction: string;
  notice: { tone: "success" | "error"; message: string } | null;
  onSelectRun: (runID: string) => void;
  onResolveApproval: (approval: TaskApprovalRecord, decision: "approve" | "reject") => void;
  onCancelRun: () => void;
  onRetryRun: () => void;
  onResumeRun: () => void;
  // onRetryFromTurn re-runs the agent_loop from turn N (1-indexed),
  // preserving the conversation up to that turn's assistant message.
  // The button appears next to each assistant bubble in the
  // conversation viewer. Only meaningful for terminal agent_loop runs;
  // the conversation viewer itself only renders for those, so we don't
  // need to gate the button further at the bubble level.
  // reason is the operator's annotation for why they're branching —
  // stored in run events and shown in the timeline.
  onRetryFromTurn: (turn: number, reason: string) => void;
  onOpenAgentChat?: (sessionID: string) => void;
  // onResumeRaisingCeiling raises the task's per-task cost ceiling
  // and resumes the run in one server-side transaction. Surfaced
  // only when the run failed with otel_status_message =
  // "cost_ceiling_exceeded" — the inline banner inside this
  // component drives it via an embedded budget input.
  onResumeRaisingCeiling: (budgetMicrosUSD: number) => void;
  onApplyPatch: (artifactID: string) => void;
  onRevertPatch: (artifactID: string) => void;
  // onOpenTrace opens the Observability drawer pre-targeted on the
  // run's request_id. Surfaced as a clickable Request ID in the run
  // metadata grid when both the callback and the run.request_id are
  // present. Optional so unit tests can render TaskDetail in
  // isolation without wiring AppShell.
  onOpenTrace?: (requestID: string) => void;
};

export function TaskDetail({
  task, run, runs, selectedRunID, events, steps, artifacts, activity, approvals,
  streamTurnCosts, streamState, busyAction, notice,
  onSelectRun, onResolveApproval, onCancelRun, onRetryRun, onResumeRun, onRetryFromTurn,
  onOpenAgentChat, onResumeRaisingCeiling, onApplyPatch, onRevertPatch, onOpenTrace,
}: Props) {
  const termRef = useRef<HTMLDivElement>(null);
  const [runPickerOpen, setRunPickerOpen] = useState(false);
  const [expandedStepID, setExpandedStepID] = useState<string>("");
  const [previewPatchID, setPreviewPatchID] = useState<string>("");
  const stdoutArtifact = artifacts.find(a => a.kind === "stdout") ?? null;
  const stderrArtifact = artifacts.find(a => a.kind === "stderr") ?? null;
  const conversationArtifact = artifacts.find(a => a.kind === "agent_conversation") ?? null;
  const previewPatch = artifacts.find(a => a.id === previewPatchID && a.kind === "patch") ?? null;
  const pendingApprovals = approvals.filter(a => a.status === "pending");
  const visibleEvents = events.filter(isVisibleRunEvent);

  useEffect(() => {
    if (termRef.current) termRef.current.scrollTop = termRef.current.scrollHeight;
  }, [stdoutArtifact]);

  useEffect(() => { setExpandedStepID(""); }, [selectedRunID]);

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden", minWidth: 0 }}>
      <div style={{ height: "var(--topbar-h)", borderBottom: "1px solid var(--border)", display: "flex", alignItems: "center", padding: "0 16px", gap: 10, flexShrink: 0, background: "var(--bg1)" }}>
        <Badge status={taskBadgeStatus(task.status)} />
        <span style={{ fontWeight: 500, fontSize: 13, color: "var(--t0)", flex: 1, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          {task.title || task.prompt || "Untitled"}
        </span>
        {task.origin_kind === "agent_chat" && task.origin_id && onOpenAgentChat && (
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            onClick={() => onOpenAgentChat?.(task.origin_id!)}
            title={`Open source chat ${task.origin_id}`}
            style={{ fontFamily: "var(--font-mono)", fontSize: 10 }}
          >
            from chat
          </button>
        )}
        {runs.length > 0 && (
          <div style={{ position: "relative" }}>
            <button
              className="btn btn-ghost btn-sm"
              onClick={() => setRunPickerOpen(o => !o)}
              aria-haspopup="listbox"
              aria-expanded={runPickerOpen}
              aria-label="Select run"
              style={{ fontFamily: "var(--font-mono)", fontSize: 11, gap: 6 }}
            >
              <span>run #{run?.number ?? "?"}</span>
              {runs.length > 1 && <span style={{ color: "var(--t3)" }}>of {runs.length}</span>}
              <Icon d={Icons.chevD} size={11} />
            </button>
            {runPickerOpen && (
              <>
                <div
                  style={{ position: "fixed", inset: 0, zIndex: 40 }}
                  onClick={() => setRunPickerOpen(false)}
                />
                <div
                  role="listbox"
                  style={{
                    position: "absolute", top: "calc(100% + 4px)", right: 0, zIndex: 41,
                    minWidth: 220, maxHeight: 320, overflowY: "auto",
                    background: "var(--bg1)", border: "1px solid var(--border)",
                    borderRadius: "var(--radius)", boxShadow: "var(--shadow-dropdown)",
                  }}
                >
                  {runs.map(r => (
                    <button
                      key={r.id}
                      role="option"
                      aria-selected={r.id === selectedRunID}
                      onClick={() => { onSelectRun(r.id); setRunPickerOpen(false); }}
                      style={{
                        width: "100%", textAlign: "left", display: "flex", alignItems: "center", gap: 8,
                        padding: "8px 10px", border: "none",
                        background: r.id === selectedRunID ? "var(--bg2)" : "transparent",
                        cursor: "pointer", borderBottom: "1px solid var(--border)",
                      }}
                    >
                      <Badge status={taskBadgeStatus(r.status)} />
                      <span style={{ fontFamily: "var(--font-mono)", fontSize: 12, color: "var(--t0)", flex: 1 }}>
                        run #{r.number}
                      </span>
                      {r.started_at && (
                        <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
                          {new Date(r.started_at).toLocaleTimeString()}
                        </span>
                      )}
                    </button>
                  ))}
                </div>
              </>
            )}
          </div>
        )}
        {run?.model && <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t2)" }}>{run.model}</span>}
        {run && (run.total_cost_micros_usd || run.prior_cost_micros_usd) ? (
          <RunCostBadge run={run} />
        ) : null}
        <div style={{ display: "flex", gap: 6 }}>
          {(run?.status === "queued" || run?.status === "running" || run?.status === "awaiting_approval") && (
            <button className="btn btn-danger btn-sm" disabled={busyAction === "cancel"} onClick={onCancelRun}>Cancel</button>
          )}
          {(run?.status === "failed" || run?.status === "cancelled") && (
            <>
              <button className="btn btn-sm" disabled={busyAction !== ""} onClick={onRetryRun}>Retry</button>
              <button className="btn btn-sm" disabled={busyAction !== ""} onClick={onResumeRun}>Resume</button>
            </>
          )}
        </div>
      </div>

      {notice && (
        <div style={{ padding: "6px 16px", fontSize: 12, fontFamily: "var(--font-mono)", background: notice.tone === "success" ? "var(--green-bg)" : "var(--red-bg)", color: notice.tone === "success" ? "var(--green)" : "var(--red)", borderBottom: "1px solid var(--border)" }}>
          {notice.message}
        </div>
      )}

      {pendingApprovals.length > 0 && (
        <TaskApprovalCallout
          task={task}
          approvals={pendingApprovals}
          busyAction={busyAction}
          onResolveApproval={onResolveApproval}
        />
      )}

      <div style={{ flex: 1, overflowY: "auto", display: "flex", flexDirection: "column" }}>
        {run && (
          <div style={{ padding: "12px 16px", borderBottom: "1px solid var(--border)", background: "var(--bg1)" }}>
            <div className="kicker" style={{ marginBottom: 8 }}>Run overview</div>
            <div style={{ display: "flex", flexWrap: "wrap", gap: 10, alignItems: "center", marginBottom: 10 }}>
              {(run.provider || run.model) && (
                <div style={{ display: "inline-flex", alignItems: "center", gap: 7, minWidth: 0 }}>
                  <BrandAvatar brand={run.provider || run.model} fallback={run.provider || run.model} size={22} />
                  <span style={{ fontSize: 12, fontWeight: 500, color: "var(--t0)" }}>{run.provider || "provider auto"}</span>
                  {run.model && (
                    <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t2)", maxWidth: 260, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                      {run.model}
                    </span>
                  )}
                </div>
              )}
              <div style={{ display: "inline-flex", flexWrap: "wrap", gap: 8, alignItems: "center" }}>
                <Badge status={taskBadgeStatus(run.status)} />
                {run.provider_kind && <Badge status={run.provider_kind === "local" ? "healthy" : "disabled"} label={run.provider_kind} />}
                {run.otel_status_message && run.status === "failed" && <Badge status="error" label={run.otel_status_message} />}
              </div>
            </div>
            <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(160px, 1fr))", gap: "8px 14px" }}>
              {([
                ["Model", run.model || "—"],
                ["Duration", formatDuration(run.started_at, run.finished_at) || "—"],
                // Request ID becomes a clickable trace link when both
                // the run carries a request_id and the parent wired an
                // onOpenTrace callback. Otherwise it's plain text — same
                // shape as the other cells.
                ["Request ID", run.request_id && onOpenTrace
                  ? <button
                      type="button"
                      onClick={() => onOpenTrace(run.request_id!)}
                      title={`Open trace for ${run.request_id}`}
                      style={{
                        background: "none", border: "none", padding: 0, cursor: "pointer",
                        fontFamily: "var(--font-mono)", fontSize: 12, color: "var(--teal)",
                        textAlign: "left", wordBreak: "break-word",
                      }}>
                      {run.request_id}
                    </button>
                  : (run.request_id || "—")],
                ["Trace ID", run.trace_id || "—"],
                ["Last error", run.last_error || "—"],
              ] as Array<[string, ReactNode]>).map(([label, value]) => (
                <div key={label}>
                  <div className="kicker" style={{ marginBottom: 3 }}>{label}</div>
                  <div style={{ fontSize: 12, color: value === "—" ? "var(--t3)" : label === "Last error" && value !== "—" ? "var(--red)" : "var(--t1)", fontFamily: "var(--font-mono)", wordBreak: "break-word" }}>
                    {value}
                  </div>
                </div>
              ))}
            </div>
          </div>
        )}

        {visibleEvents.length > 0 && (
          <div style={{ padding: "12px 16px", borderBottom: "1px solid var(--border)" }}>
            <div className="kicker" style={{ marginBottom: 8 }}>Run timeline</div>
            <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
              {visibleEvents.slice().sort((left, right) => left.sequence - right.sequence).map((event) => {
                const meta = describeRunEvent(event.type);
                return (
                  <div key={event.event_id || `${event.sequence}-${event.type}`} style={{ display: "grid", gridTemplateColumns: "64px minmax(132px, auto) minmax(0, 1fr)", gap: 10, alignItems: "start" }}>
                    <div style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
                      #{event.sequence}
                    </div>
                    <span style={{ minWidth: 0 }} title={meta.label}>
                      <Badge status={meta.tone} label={meta.label} />
                    </span>
                    <div style={{ minWidth: 0 }}>
                      <div style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t2)" }}>
                        {event.occurred_at ? new Date(event.occurred_at).toLocaleTimeString() : "streamed"}
                      </div>
                      {(() => {
                        const note = describeRunEventNote(event);
                        return note ? (
                          <div style={{ fontSize: 11, color: "var(--t2)", marginTop: 2, wordBreak: "break-word" }}>
                            {note}
                          </div>
                        ) : null;
                      })()}
                    </div>
                  </div>
                );
              })}
            </div>
          </div>
        )}

        {activity.length > 0 && (
          <RuntimeActivity activity={activity} />
        )}

        {/* Cost-ceiling banner: shown only when this run failed
            specifically because of the per-task budget. Lets the
            operator raise the ceiling and resume in one click rather
            than calling two endpoints. */}
        {run && run.status === "failed" && run.otel_status_message === "cost_ceiling_exceeded" && (
          <CostCeilingBanner
            run={run}
            task={task}
            busy={busyAction !== ""}
            onResumeRaisingCeiling={onResumeRaisingCeiling}
          />
        )}

        {steps.length > 0 && (
          <div style={{ padding: "12px 16px", borderBottom: "1px solid var(--border)" }}>
            <div className="kicker" style={{ marginBottom: 8 }}>Steps</div>
            <div style={{ display: "flex", flexDirection: "column" }}>
              {steps.map((step, i) => {
                const expanded = expandedStepID === step.id;
                const hasDetail = !!(step.input || step.output_summary || step.error || step.tool_name || step.phase);
                return (
                  <div key={step.id} style={{ display: "flex", flexDirection: "column" }}>
                    <button
                      type="button"
                      aria-expanded={expanded}
                      aria-label={`Step ${step.title || step.kind || step.tool_name || "step"}`}
                      onClick={() => hasDetail && setExpandedStepID(expanded ? "" : step.id)}
                      style={{
                        display: "flex", alignItems: "center", gap: 10, padding: "5px 0", position: "relative",
                        background: "transparent", border: "none", textAlign: "left",
                        cursor: hasDetail ? "pointer" : "default", color: "inherit",
                      }}
                    >
                      {i < steps.length - 1 && (
                        <div style={{ position: "absolute", left: 6, top: "50%", width: 1, height: "100%", background: "var(--border)", zIndex: 0 }} />
                      )}
                      <div style={{
                        width: 13, height: 13, borderRadius: "50%", background: stepColor(step.status), flexShrink: 0, zIndex: 1,
                        boxShadow: step.status === "running" ? `0 0 8px ${stepColor(step.status)}` : "none",
                      }} />
                      <StepRowTitle step={step} />
                      {step.exit_code !== undefined && step.exit_code !== 0 && step.status !== "running" && (
                        <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--red)" }}>exit {step.exit_code}</span>
                      )}
                      {step.started_at && step.status === "completed" && (
                        <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
                          {new Date(step.started_at).toLocaleTimeString()}
                        </span>
                      )}
                      {step.status === "running" && <span className="badge badge-teal" style={{ fontSize: 10, animation: "pulse 1.5s infinite" }}>running</span>}
                      {step.status === "awaiting_approval" && <span className="badge badge-amber" style={{ fontSize: 10 }}>awaiting</span>}
                      {step.status === "failed" && step.error && (
                        <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--red)", maxWidth: 120, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }} title={step.error}>
                          {step.error}
                        </span>
                      )}
                      {hasDetail && (
                        <span style={{ display: "inline-flex", color: "var(--t3)", transform: expanded ? "rotate(180deg)" : undefined, transition: "transform 0.1s" }}>
                          <Icon d={Icons.chevD} size={11} />
                        </span>
                      )}
                    </button>
                    {expanded && <StepDetail step={step} />}
                  </div>
                );
              })}
            </div>
          </div>
        )}

        {conversationArtifact?.content_text && (
          <AgentConversationView
            raw={conversationArtifact.content_text}
            // Only show the per-turn retry control once the run is
            // terminal — retrying mid-flight would race the running
            // worker. The button is also disabled while another
            // action is in flight (e.g. cancel) to avoid stacking.
            canRetryFromTurn={run ? (run.status === "completed" || run.status === "failed" || run.status === "cancelled") : false}
            busy={busyAction !== ""}
            onRetryFromTurn={onRetryFromTurn}
            // Pass model-kind steps so each assistant bubble can show
            // the LLM cost for its turn. Index N model step → turn N
            // assistant message; the viewer joins them by ordinal.
            steps={steps}
            // Stream-pushed per-turn costs are a fallback when the
            // model-step output_summary path doesn't carry the cost
            // (older runs, or step writes that completed before the
            // cost was attached). Same key (turn number).
            streamTurnCosts={streamTurnCosts}
          />
        )}

        <div style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 180 }}>
          <div style={{ padding: "8px 16px", borderBottom: "1px solid var(--border)", display: "flex", alignItems: "center", gap: 8, background: "var(--bg1)" }}>
            <Icon d={Icons.terminal} size={13} />
            <span style={{ fontSize: 11, color: "var(--t2)", fontFamily: "var(--font-mono)" }}>run output</span>
            {stdoutArtifact && (
              <span style={{ fontSize: 10, color: "var(--t3)", fontFamily: "var(--font-mono)" }}>
                stdout{stdoutArtifact.size_bytes ? ` ${stdoutArtifact.size_bytes}b` : ""}
              </span>
            )}
            {streamState === "live" && <Dot color="green" pulse />}
            {streamState === "connecting" && <Dot color="amber" pulse />}
            {stderrArtifact && (
              <span style={{
                fontSize: 10,
                color: artifactHasBytes(stderrArtifact) ? "var(--red)" : "var(--t3)",
                fontFamily: "var(--font-mono)",
                marginLeft: "auto",
              }}>
                stderr {artifactHasBytes(stderrArtifact) ? "available" : "empty"}
              </span>
            )}
          </div>
          <div ref={termRef} style={{ flex: 1, overflowY: "auto", padding: "10px 16px", background: "var(--bg0)", fontFamily: "var(--font-mono)", fontSize: 12, lineHeight: 1.8 }}>
            {stdoutArtifact?.content_text ? (
              stdoutArtifact.content_text.split("\n").map((line, i) => (
                <div key={i} style={{ color: "var(--t1)" }}>{line || " "}</div>
              ))
            ) : (
              <div style={{ color: "var(--t3)" }}>
                {run?.status === "queued" ? "Waiting in queue…"
                  : run?.status === "running" ? "Running…"
                  : run?.status === "awaiting_approval" ? "Awaiting approval…"
                  : "No output."}
              </div>
            )}
            {stderrArtifact?.content_text && (
              <>
                <div style={{ color: "var(--t3)", marginTop: 8, borderTop: "1px solid var(--border)", paddingTop: 8 }}>— stderr —</div>
                {stderrArtifact.content_text.split("\n").map((line, i) => (
                  <div key={i} style={{ color: "var(--red)" }}>{line || " "}</div>
                ))}
              </>
            )}
            {(task.status === "running") && (
              <div style={{ color: "var(--teal)", animation: "blink 0.8s step-end infinite" }}>█</div>
            )}
          </div>
        </div>

        {/* Bottom artifacts strip — excludes stdout/stderr (rendered
            in the terminal pane above) and agent_conversation
            (rendered as the chat-bubble timeline). */}
        {artifacts.filter(isVisibleArtifactBadge).length > 0 && (
          <div style={{ padding: "10px 16px", borderTop: "1px solid var(--border)", display: "flex", flexWrap: "wrap", gap: 6, background: "var(--bg1)" }}>
            <span className="kicker" style={{ alignSelf: "center", marginRight: 4 }}>artifacts</span>
            {artifacts.filter(isVisibleArtifactBadge).map(a => (
              <div key={a.id} style={{ display: "flex", alignItems: "center", gap: 6, background: "var(--bg3)", border: "1px solid var(--border)", borderRadius: "var(--radius-sm)", padding: "3px 8px" }}>
                <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t0)" }}>{a.name || a.kind}</span>
                {a.kind === "patch" && a.status && <Badge status={a.status === "proposed" ? "warn" : a.status === "applied" ? "ok" : "disabled"} label={a.status} />}
                {a.size_bytes != null && a.size_bytes > 0 && <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--green)" }}>{a.size_bytes}b</span>}
                {a.kind === "patch" && (
                  <button className="btn btn-ghost btn-sm" onClick={() => setPreviewPatchID(a.id)}>Preview</button>
                )}
                {a.kind === "patch" && a.status === "proposed" && (
                  <button className="btn btn-primary btn-sm" disabled={busyAction !== ""} onClick={() => onApplyPatch(a.id)}>Apply</button>
                )}
                {a.kind === "patch" && a.status === "applied" && (
                  <button className="btn btn-ghost btn-sm" disabled={busyAction !== ""} onClick={() => onRevertPatch(a.id)}>Revert</button>
                )}
              </div>
            ))}
          </div>
        )}
      </div>
      {previewPatch && (
        <Modal
          title="Patch preview"
          width={760}
          onClose={() => setPreviewPatchID("")}
          footer={(
            <>
              <button className="btn btn-ghost" onClick={() => setPreviewPatchID("")}>Close</button>
              {previewPatch.status === "proposed" && (
                <button className="btn btn-primary" disabled={busyAction !== ""} onClick={() => onApplyPatch(previewPatch.id)}>Apply patch</button>
              )}
              {previewPatch.status === "applied" && (
                <button className="btn btn-ghost" disabled={busyAction !== ""} onClick={() => onRevertPatch(previewPatch.id)}>Revert patch</button>
              )}
            </>
          )}
        >
          <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
            <div style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0 }}>
              <Badge status={previewPatch.status === "proposed" ? "warn" : previewPatch.status === "applied" ? "ok" : "disabled"} label={previewPatch.status || "patch"} />
              <span style={{ fontFamily: "var(--font-mono)", fontSize: 12, color: "var(--t0)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                {previewPatch.path || previewPatch.name || previewPatch.id}
              </span>
            </div>
            <PatchDiffPreview diff={previewPatch.content_text || "No diff content captured for this patch."} />
          </div>
        </Modal>
      )}
    </div>
  );
}

function PatchDiffPreview({ diff }: { diff: string }) {
  return (
    <pre
      data-testid="patch-diff-preview"
      style={{
        margin: 0,
        maxHeight: "56vh",
        overflow: "auto",
        padding: 12,
        borderRadius: "var(--radius-sm)",
        border: "1px solid var(--border)",
        background: "var(--bg0)",
        fontFamily: "var(--font-mono)",
        fontSize: 12,
        lineHeight: 1.7,
      }}
    >
      {diff.split("\n").map((line, index) => {
        const color = line.startsWith("+") && !line.startsWith("+++") ? "var(--green)" :
          line.startsWith("-") && !line.startsWith("---") ? "var(--red)" :
            line.startsWith("@@") ? "var(--amber)" : "var(--t1)";
        return <div key={index} style={{ color }}>{line || " "}</div>;
      })}
    </pre>
  );
}

// StepRowTitle renders the headline label for one row in the steps
// timeline. For built-in tools and non-tool steps (model thinking,
// approvals, etc.) we keep the existing "title or kind" fallback —
// changing it would churn every other surface. For MCP tool calls we
// swap in a small "MCP" badge plus a parsed "server · tool" label so
// the operator can scan the timeline and immediately distinguish
// external-server calls from built-ins. The raw namespaced name
// remains available via the row's title attribute for accessibility
// and copy-paste.
function StepRowTitle({ step }: { step: TaskStepRecord }) {
  const baseStyle = {
    fontSize: 12,
    color: (step.status === "queued" || !step.status) ? "var(--t3)" : "var(--t0)",
    flex: 1,
  } as const;
  const mcp = splitNamespacedToolName(step.tool_name);
  if (mcp) {
    return (
      <span
        style={{ ...baseStyle, display: "inline-flex", alignItems: "center", gap: 6, minWidth: 0 }}
        title={step.tool_name}
      >
        <span
          className="badge badge-muted"
          aria-label="MCP tool call"
          style={{ fontSize: 9, fontFamily: "var(--font-mono)", padding: "1px 5px", flexShrink: 0 }}
        >
          MCP
        </span>
        <span style={{ minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          <span style={{ color: "var(--t2)" }}>{mcp.server}</span>
          <span style={{ color: "var(--t3)", margin: "0 4px" }}>·</span>
          <span style={{ color: "var(--t0)", fontFamily: "var(--font-mono)" }}>{mcp.tool}</span>
        </span>
      </span>
    );
  }
  return (
    <span style={baseStyle}>
      {step.title || step.kind || step.tool_name || "step"}
    </span>
  );
}

function RuntimeActivity({ activity }: { activity: TaskActivityRecord[] }) {
  const activityByID = new Map(activity.map(item => [item.id, item]));
  const outputArtifacts = useMemo(() => buildOutputActivityIndex(activity), [activity]);
  const rows = activity.map(taskActivityToTranscriptActivity);
  return (
    <div style={{ padding: "12px 16px", borderBottom: "1px solid var(--border)" }}>
      <div className="kicker" style={{ marginBottom: 8 }}>Runtime activity</div>
      <TranscriptActivityTimeline
        activities={rows}
        defaultOpen
        renderAdvancedActivity={(item) => item.id
          ? <TaskActivityAdvancedDetails activity={activityByID.get(item.id)} outputArtifacts={outputArtifacts} />
          : null}
      />
    </div>
  );
}

type OutputActivityIndex = {
  all: TaskActivityRecord[];
  byStepID: Map<string, TaskActivityRecord[]>;
};

function TaskActivityAdvancedDetails({ activity, outputArtifacts }: { activity?: TaskActivityRecord; outputArtifacts: OutputActivityIndex }) {
  if (!activity) return null;
  const rows = taskActivityAdvancedRows(activity);
  const diagnostics = failedToolOutputArtifacts(activity, outputArtifacts);
  const isFailedTool = activity.type === "tool_call" && activity.status === "failed";
  if (rows.length === 0 && diagnostics.length === 0 && !isFailedTool) return null;

  return (
    <div style={{ display: "grid", gap: 5 }}>
      {isFailedTool && (
        <TaskActivityFailureDiagnostics activity={activity} artifacts={diagnostics} />
      )}
      {rows.length > 0 && (
        <details open={!isFailedTool}>
          <summary style={{ cursor: "pointer", fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
            Raw metadata
          </summary>
          <div style={{ display: "grid", gap: 5, marginTop: 6 }}>
            {rows.map(row => (
              <div
                key={row.label}
                style={{
                  display: "grid",
                  gridTemplateColumns: "92px minmax(0, 1fr)",
                  gap: 8,
                  alignItems: "baseline",
                }}
              >
                <span style={{ color: "var(--t3)", fontFamily: "var(--font-mono)", fontSize: 10 }}>
                  {row.label}
                </span>
                <span style={{
                  color: "var(--t1)",
                  fontFamily: "var(--font-mono)",
                  fontSize: 10,
                  overflowWrap: "anywhere",
                  whiteSpace: row.multiline ? "pre-wrap" : "normal",
                }}>
                  {row.value}
                </span>
              </div>
            ))}
          </div>
        </details>
      )}
    </div>
  );
}

function TaskActivityFailureDiagnostics({ activity, artifacts }: { activity: TaskActivityRecord; artifacts: TaskActivityRecord[] }) {
  const command = summaryString(activity, "command");
  const exitCode = summaryNumber(activity, "exit_code");
  const stdoutBytes = summaryNumber(activity, "stdout_bytes");
  const stderrBytes = summaryNumber(activity, "stderr_bytes");
  const hasStdout = artifacts.some(artifact => outputActivityStream(artifact) === "stdout");
  const hasStderr = artifacts.some(artifact => outputActivityStream(artifact) === "stderr");
  const facts = [
    command ? { label: "command", value: command } : null,
    exitCode !== undefined ? { label: "exit", value: String(exitCode) } : null,
    stdoutBytes !== undefined ? { label: "stdout", value: `${stdoutBytes}b` } : null,
    stderrBytes !== undefined ? { label: "stderr", value: `${stderrBytes}b` } : null,
  ].filter((item): item is { label: string; value: string } => Boolean(item));

  return (
    <div style={{ display: "grid", gap: 7 }}>
      <div style={{ color: "var(--t2)", fontSize: 11, lineHeight: 1.5 }}>
        This tool failed. The summary below shows what Hecate captured for the tool call; full streams remain in run output when artifacts exist.
      </div>
      {facts.length > 0 && (
        <div style={{
          display: "flex",
          flexWrap: "wrap",
          gap: 6,
        }}>
          {facts.map(fact => (
            <span
              key={fact.label}
              style={{
                border: "1px solid var(--border)",
                borderRadius: 999,
                color: "var(--t2)",
                fontFamily: "var(--font-mono)",
                fontSize: 10,
                padding: "3px 7px",
              }}
            >
              <span style={{ color: "var(--t3)" }}>{fact.label}</span>{" "}
              <span style={{ color: fact.label === "exit" && fact.value !== "0" ? "var(--red)" : "var(--t1)" }}>
                {fact.value}
              </span>
            </span>
          ))}
        </div>
      )}
      <div style={{ display: "grid", gap: 7 }}>
        {artifacts.length > 0 ? (
          artifacts.map(artifact => (
            <TaskActivityOutputPreview
              key={artifact.artifact_id || artifact.id}
              artifact={artifact}
            />
          ))
        ) : (
          <TaskActivityMissingOutput message="No stdout or stderr artifacts were captured for this tool." />
        )}
        {!hasStdout && artifacts.length > 0 && (
          <TaskActivityMissingOutput message="stdout artifact was not captured for this tool." />
        )}
        {!hasStderr && artifacts.length > 0 && (
          <TaskActivityMissingOutput message="stderr artifact was not captured for this tool." />
        )}
      </div>
    </div>
  );
}

function TaskActivityMissingOutput({ message }: { message: string }) {
  return (
    <div style={{
      border: "1px dashed var(--border)",
      borderRadius: "var(--radius-sm)",
      color: "var(--t3)",
      fontSize: 11,
      padding: "7px",
    }}>
      {message}
    </div>
  );
}

function TaskActivityOutputPreview({ artifact }: { artifact: TaskActivityRecord }) {
  const stream = outputActivityStream(artifact);
  const isStderr = stream === "stderr";
  const preview = taskActivityArtifactPreview(artifact);
  const size = taskActivityArtifactSize(artifact);
  const sizeLabel = size === undefined ? "unknown size" : size === 0 ? "empty" : `${size}b`;
  const emptyMessage = size === undefined
    ? "Preview unavailable in this snapshot."
    : size === 0
      ? "No bytes captured for this stream."
      : "Preview unavailable in this snapshot.";
  return (
    <div style={{
      border: `1px solid ${isStderr ? "rgba(239, 95, 95, 0.28)" : "var(--border)"}`,
      borderRadius: "var(--radius-sm)",
      background: "var(--bg0)",
      overflow: "hidden",
    }}>
      <div style={{
        alignItems: "center",
        borderBottom: "1px solid var(--border)",
        display: "flex",
        gap: 8,
        padding: "4px 7px",
      }}>
        <span style={{
          color: isStderr ? "var(--red)" : "var(--t1)",
          fontFamily: "var(--font-mono)",
          fontSize: 10,
        }}>
          {artifact.title || stream}
        </span>
        <span style={{ color: "var(--t3)", fontFamily: "var(--font-mono)", fontSize: 10 }}>
          {sizeLabel}
        </span>
      </div>
      {preview ? (
        <pre style={{
          color: isStderr ? "var(--red)" : "var(--t1)",
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          lineHeight: 1.55,
          margin: 0,
          maxHeight: 130,
          overflow: "auto",
          padding: "7px",
          whiteSpace: "pre-wrap",
        }}>{preview}</pre>
      ) : (
        <div style={{ color: "var(--t3)", fontSize: 11, padding: "7px" }}>
          {emptyMessage}
        </div>
      )}
    </div>
  );
}

function buildOutputActivityIndex(activity: TaskActivityRecord[]): OutputActivityIndex {
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

function failedToolOutputArtifacts(activity: TaskActivityRecord, outputArtifacts: OutputActivityIndex): TaskActivityRecord[] {
  if (activity.type !== "tool_call" || activity.status !== "failed") return [];
  const matchingStep = activity.step_id || "";
  if (matchingStep) {
    return outputArtifacts.byStepID.get(matchingStep) ?? [];
  }
  return outputArtifacts.all;
}

function isOutputArtifactActivity(activity: TaskActivityRecord): boolean {
  return outputActivityStream(activity) !== "";
}

function outputActivityStream(activity: TaskActivityRecord): "stdout" | "stderr" | "" {
  const label = `${activity.kind ?? ""} ${activity.title ?? ""} ${activity.path ?? ""}`.toLowerCase();
  if (label.includes("stderr")) return "stderr";
  if (label.includes("stdout")) return "stdout";
  return "";
}

function taskActivityAdvancedRows(activity: TaskActivityRecord): Array<{ label: string; value: string; multiline?: boolean }> {
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

function taskActivityToTranscriptActivity(item: TaskActivityRecord): AgentChatActivityRecord {
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

function taskActivityArtifactSize(item: TaskActivityRecord): number | undefined {
  const value = item.summary?.size_bytes;
  return typeof value === "number" ? value : undefined;
}

function taskActivityArtifactPreview(item: TaskActivityRecord): string | undefined {
  const value = item.summary?.content_preview;
  return typeof value === "string" && value.trimEnd() ? value.replace(/[\r\n]+$/, "") : undefined;
}

function artifactHasBytes(artifact: TaskArtifactRecord): boolean {
  if (typeof artifact.size_bytes === "number") return artifact.size_bytes > 0;
  return Boolean(artifact.content_text);
}

function taskActivityTitle(item: TaskActivityRecord): string {
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

function taskActivitySubtitle(item: TaskActivityRecord): string | undefined {
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

function summaryString(item: TaskActivityRecord, key: string): string {
  const value = item.summary?.[key];
  return typeof value === "string" ? value.trim() : "";
}

function summaryNumber(item: TaskActivityRecord, key: string): number | undefined {
  const value = item.summary?.[key];
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

function nonInternalKind(kind?: string): string {
  const value = kind?.trim() || "";
  return value.startsWith("builtin.agent_loop_") ? "" : value;
}

function StepDetail({ step }: { step: TaskStepRecord }) {
  const duration = formatDuration(step.started_at, step.finished_at);
  const mcp = splitNamespacedToolName(step.tool_name);
  return (
    <div
      style={{
        margin: "4px 0 8px 24px",
        padding: "10px 12px",
        background: "var(--bg2)",
        border: "1px solid var(--border)",
        borderRadius: "var(--radius-sm)",
        display: "flex",
        flexDirection: "column",
        gap: 8,
      }}
    >
      <div style={{ display: "flex", flexWrap: "wrap", gap: 12, fontSize: 10, fontFamily: "var(--font-mono)", color: "var(--t3)" }}>
        {/* Tool identity. MCP tool calls carry a `mcp__<server>__<tool>`
            namespaced name; we break it out here so the operator
            sees transport/server/tool as separate facts rather than
            one long opaque token. Built-in tools fall through to
            the existing single-line rendering. */}
        {step.tool_name && mcp && (
          <>
            <span>transport: <span style={{ color: "var(--t1)" }}>MCP</span></span>
            <span>server: <span style={{ color: "var(--t1)" }}>{mcp.server}</span></span>
            <span>tool: <span style={{ color: "var(--t1)" }}>{mcp.tool}</span></span>
          </>
        )}
        {step.tool_name && !mcp && <span>tool: <span style={{ color: "var(--t1)" }}>{step.tool_name}</span></span>}
        {step.phase && <span>phase: <span style={{ color: "var(--t1)" }}>{step.phase}</span></span>}
        {step.error_kind && <span>error kind: <span style={{ color: "var(--t1)" }}>{step.error_kind}</span></span>}
        {step.exit_code !== undefined && <span>exit: <span style={{ color: step.exit_code === 0 ? "var(--green)" : "var(--red)" }}>{step.exit_code}</span></span>}
        {duration && <span>took: <span style={{ color: "var(--t1)" }}>{duration}</span></span>}
        {step.started_at && <span>started: <span style={{ color: "var(--t1)" }}>{new Date(step.started_at).toLocaleString()}</span></span>}
        {step.trace_id && <span>trace: <span style={{ color: "var(--t1)" }}>{step.trace_id}</span></span>}
      </div>
      {/* Hint pointing operators to the conversation viewer where
          the upstream server's full text result is rendered. The
          step's output_summary captures only is_error + text_size
          (the dispatcher trims to keep step rows small in the
          store); the full text lives as a tool-role message in the
          agent_conversation artifact, which the bottom timeline
          already shows as a chat bubble. */}
      {mcp && (
        <div style={{ fontSize: 10, fontFamily: "var(--font-mono)", color: "var(--t3)" }}>
          Full upstream result rendered in the agent conversation below.
        </div>
      )}
      {step.error && (
        <div>
          <div className="kicker" style={{ marginBottom: 4 }}>Error</div>
          <pre style={{ margin: 0, padding: "6px 8px", fontSize: 11, fontFamily: "var(--font-mono)", color: "var(--red)", background: "var(--bg0)", border: "1px solid var(--border)", borderRadius: "var(--radius-sm)", whiteSpace: "pre-wrap", wordBreak: "break-word" }}>
            {step.error}
          </pre>
        </div>
      )}
      {step.input && Object.keys(step.input).length > 0 && (
        <div>
          <div className="kicker" style={{ marginBottom: 4 }}>Input</div>
          <pre style={{ margin: 0, padding: "6px 8px", fontSize: 11, fontFamily: "var(--font-mono)", color: "var(--t1)", background: "var(--bg0)", border: "1px solid var(--border)", borderRadius: "var(--radius-sm)", overflowX: "auto", maxHeight: 200 }}>
            {JSON.stringify(step.input, null, 2)}
          </pre>
        </div>
      )}
      {step.output_summary && Object.keys(step.output_summary).length > 0 && (
        <div>
          <div className="kicker" style={{ marginBottom: 4 }}>Output</div>
          <pre style={{ margin: 0, padding: "6px 8px", fontSize: 11, fontFamily: "var(--font-mono)", color: "var(--t1)", background: "var(--bg0)", border: "1px solid var(--border)", borderRadius: "var(--radius-sm)", overflowX: "auto", maxHeight: 200 }}>
            {JSON.stringify(step.output_summary, null, 2)}
          </pre>
        </div>
      )}
    </div>
  );
}

// isVisibleArtifactBadge filters which artifacts get a chip in the
// bottom strip. stdout/stderr are rendered in the terminal pane;
// agent_conversation is rendered as a chat-bubble timeline. Both
// would be redundant as bare chips, so we hide them.
function isVisibleArtifactBadge(a: TaskArtifactRecord): boolean {
  return a.kind !== "stdout" && a.kind !== "stderr" && a.kind !== "agent_conversation";
}

// AgentConversationMessage mirrors pkg/types.Message (the shape the
// agent loop persists). Only fields the viewer renders are typed —
// extra fields on the wire (cache control, etc.) are ignored.
type AgentConversationMessage = {
  role: "user" | "assistant" | "tool" | string;
  content?: string;
  tool_call_id?: string;
  tool_calls?: Array<{
    id: string;
    type?: string;
    function?: { name?: string; arguments?: string };
  }>;
};

// AgentConversationView renders the agent_loop conversation as a
// chat-bubble timeline. User prompts on the right, assistant turns on
// the left (with their tool calls expanded), tool results in muted
// frames. The conversation is the agent's reasoning trail — operators
// scan it to understand WHY the agent did what it did, not just WHAT
// it did (the step timeline already covers that).
//
// Robustness: the artifact's content is JSON parsed inline. If the
// JSON is corrupt we render an inline error and continue rendering
// the rest of the run UI — losing the conversation viewer is much
// better than crashing the whole page.
function AgentConversationView({
  raw,
  canRetryFromTurn = false,
  busy = false,
  onRetryFromTurn,
  steps = [],
  streamTurnCosts,
}: {
  raw: string;
  canRetryFromTurn?: boolean;
  busy?: boolean;
  // reason is collected via the inline modal before the call is made.
  onRetryFromTurn?: (turn: number, reason: string) => void;
  steps?: TaskStepRecord[];
  // streamTurnCosts is a turn → µUSD map pushed by the SSE stream.
  // Used to fill in costs missing from the model-step output_summary
  // path. Optional — the steps path is the primary source.
  streamTurnCosts?: Map<number, number>;
}) {
  // pendingRetryTurn is set when the operator clicks "↻ retry from here"
  // on a bubble. The inline modal collects an optional reason, then
  // fires onRetryFromTurn(turn, reason) on confirm.
  const [pendingRetryTurn, setPendingRetryTurn] = useState<number | null>(null);

  let messages: AgentConversationMessage[] = [];
  try {
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed)) messages = parsed as AgentConversationMessage[];
  } catch {
    return (
      <div style={{ padding: "10px 16px", borderBottom: "1px solid var(--border)", fontSize: 11, color: "var(--red)", fontFamily: "var(--font-mono)" }}>
        Could not parse agent conversation artifact (invalid JSON).
      </div>
    );
  }
  if (messages.length === 0) return null;
  const visibleMessages = messages.filter(isVisibleConversationMessage);
  if (visibleMessages.length === 0) return null;

  // Compute per-message turn numbers up-front so each bubble can render
  // its own "↻ retry from turn N" affordance. Only assistant messages
  // get a turn number — user/tool/system messages get 0 and won't show
  // the button. Counting in a single pass here keeps the bubble itself
  // O(1) at render time.
  let assistantSeen = 0;
  const turnByIndex: number[] = visibleMessages.map(m => {
    if (m.role === "assistant") {
      assistantSeen++;
      return assistantSeen;
    }
    return 0;
  });

  // Build turn → cost map by walking model-kind steps in step.index
  // order. The Nth model step corresponds to the Nth assistant turn,
  // so we just zip them. Steps whose OutputSummary lacks the cost
  // field (older runs, or resumed-after-approval steps that didn't
  // re-call the LLM) map to undefined; we fall back to the SSE-
  // streamed per-turn cost (when available) so the figure still
  // shows up; otherwise the bubble renders nothing rather than a
  // misleading "$0.000".
  const costByTurn = buildTurnCostMap(steps);
  if (streamTurnCosts) {
    for (const [turn, micros] of streamTurnCosts) {
      if (!costByTurn.has(turn) && micros > 0) {
        costByTurn.set(turn, micros);
      }
    }
  }

  return (
    <>
      <div style={{ padding: "12px 16px", borderBottom: "1px solid var(--border)", display: "flex", flexDirection: "column", gap: 8 }}>
        <div className="kicker" style={{ marginBottom: 4 }}>
          Agent conversation · {visibleMessages.length} message{visibleMessages.length === 1 ? "" : "s"}
        </div>
        {visibleMessages.map((m, i) => (
          <ConversationBubble
            key={i}
            message={m}
            turn={turnByIndex[i]}
            turnCostMicros={turnByIndex[i] > 0 ? costByTurn.get(turnByIndex[i]) : undefined}
            canRetryFromTurn={canRetryFromTurn}
            busy={busy || pendingRetryTurn !== null}
            onRetryFromTurn={canRetryFromTurn ? (turn) => setPendingRetryTurn(turn) : undefined}
          />
        ))}
      </div>
      {pendingRetryTurn !== null && (
        <RetryFromTurnModal
          turn={pendingRetryTurn}
          busy={busy}
          onConfirm={(reason) => {
            onRetryFromTurn?.(pendingRetryTurn, reason);
            setPendingRetryTurn(null);
          }}
          onClose={() => setPendingRetryTurn(null)}
        />
      )}
    </>
  );
}

function isVisibleConversationMessage(message: AgentConversationMessage): boolean {
  return message.role === "user" || message.role === "assistant" || message.role === "tool";
}

// RetryFromTurnModal collects an optional reason before submitting the
// retry-from-turn request. The reason is stored in the run.resumed_from_event event
// and shown in the run timeline so operators can annotate why they
// branched from a particular turn.
function RetryFromTurnModal({
  turn,
  busy,
  onConfirm,
  onClose,
}: {
  turn: number;
  busy: boolean;
  onConfirm: (reason: string) => void;
  onClose: () => void;
}) {
  const [reason, setReason] = useState("");
  return (
    <Modal
      title={`Retry from turn ${turn}`}
      onClose={onClose}
      width={440}
      footer={
        <div style={{ display: "flex", gap: 8, justifyContent: "flex-end" }}>
          <button className="btn btn-ghost" onClick={onClose} disabled={busy}>
            Cancel
          </button>
          <button
            className="btn btn-primary"
            disabled={busy}
            onClick={() => onConfirm(reason.trim())}
          >
            {busy ? "Working…" : "Retry"}
          </button>
        </div>
      }
    >
      <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
        <p style={{ margin: 0, fontSize: 13, color: "var(--t1)", lineHeight: 1.5 }}>
          A new run will be created with the conversation truncated to just before
          turn {turn}'s assistant message. The prior steps and file state are preserved.
        </p>
        <label style={{ display: "flex", flexDirection: "column", gap: 4 }}>
          <span style={{ fontSize: 11, fontWeight: 500, color: "var(--t2)", textTransform: "uppercase", letterSpacing: "0.05em" }}>
            Reason <span style={{ fontWeight: 400, color: "var(--t3)" }}>(optional)</span>
          </span>
          <textarea
            autoFocus
            rows={2}
            value={reason}
            onChange={e => setReason(e.target.value)}
            placeholder="Why are you branching from this turn?"
            style={{
              resize: "vertical", fontFamily: "var(--font-sans)", fontSize: 13,
              color: "var(--t0)", background: "var(--bg2)",
              border: "1px solid var(--border)", borderRadius: "var(--radius-sm)",
              padding: "6px 8px", lineHeight: 1.5, width: "100%", boxSizing: "border-box",
            }}
          />
        </label>
      </div>
    </Modal>
  );
}

// buildTurnCostMap walks `steps` in step.index order, picks out the
// model-kind ones, and pairs them with their turn ordinal. The agent
// loop emits exactly one model step per turn (resumed-after-approval
// turns use a different ToolName but still count as model steps), so
// "first model step" = turn 1, "second" = turn 2, etc. Returns a map
// keyed by turn number with the cost in µUSD; turns whose step has no
// cost in OutputSummary are simply absent from the map.
function buildTurnCostMap(steps: TaskStepRecord[]): Map<number, number> {
  const sorted = [...steps].sort((a, b) => (a.index ?? 0) - (b.index ?? 0));
  const out = new Map<number, number>();
  let turn = 0;
  for (const step of sorted) {
    if (step.kind !== "model") continue;
    turn++;
    const summary = step.output_summary as Record<string, unknown> | undefined;
    if (!summary) continue;
    const raw = summary["cost_micros_usd"];
    if (typeof raw === "number" && raw > 0) {
      out.set(turn, raw);
    }
  }
  return out;
}

function ConversationBubble({
  message,
  turn,
  turnCostMicros,
  canRetryFromTurn = false,
  busy = false,
  onRetryFromTurn,
}: {
  message: AgentConversationMessage;
  turn?: number;
  turnCostMicros?: number;
  canRetryFromTurn?: boolean;
  busy?: boolean;
  onRetryFromTurn?: (turn: number) => void;
}) {
  if (message.role === "user") {
    return (
      <div style={{ display: "flex", justifyContent: "flex-end" }}>
        <div style={{
          maxWidth: "80%", padding: "8px 12px",
          background: "var(--teal-bg)", border: "1px solid var(--teal-border)",
          borderRadius: "var(--radius)", color: "var(--t0)", fontSize: 13, lineHeight: 1.5,
          whiteSpace: "pre-wrap", wordBreak: "break-word",
        }}>
          {message.content || ""}
        </div>
      </div>
    );
  }
  if (message.role === "tool") {
    // Tool results are typically formatted "status=…\n--- stdout ---\n…"
    // — render as a code block with monospace + scroll for long outputs.
    const callRef = message.tool_call_id ? ` · ${message.tool_call_id.slice(0, 12)}` : "";
    return (
      <div style={{ display: "flex", justifyContent: "flex-start" }}>
        <div style={{
          maxWidth: "90%", padding: "6px 10px",
          background: "var(--bg2)", border: "1px solid var(--border)",
          borderRadius: "var(--radius-sm)", fontSize: 11,
          fontFamily: "var(--font-mono)", color: "var(--t1)",
        }}>
          <div className="kicker" style={{ marginBottom: 4 }}>
            tool result{callRef}
          </div>
          <pre style={{ margin: 0, whiteSpace: "pre-wrap", wordBreak: "break-word", maxHeight: 200, overflowY: "auto", color: "var(--t1)" }}>
            {message.content || ""}
          </pre>
        </div>
      </div>
    );
  }
  // assistant — content + any tool calls
  const showRetry = canRetryFromTurn && !!turn && turn > 0 && !!onRetryFromTurn;
  const showCost = typeof turnCostMicros === "number" && turnCostMicros > 0;
  return (
    <div style={{ display: "flex", justifyContent: "flex-start", flexDirection: "column", gap: 6, alignItems: "stretch" }}>
      <div className="kicker" style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <span>turn {turn || "?"}</span>
        {showCost && (
          <span
            title={`LLM cost for turn ${turn}: ${formatMicrosUSD(turnCostMicros!)}`}
            style={{ color: "var(--t3)", fontFamily: "var(--font-mono)" }}
          >
            · {formatMicrosUSD(turnCostMicros!)}
          </span>
        )}
        {showRetry && (
          <button
            type="button"
            className="btn btn-ghost btn-sm"
            disabled={busy}
            onClick={() => onRetryFromTurn?.(turn!)}
            title={`Re-run from turn ${turn} with the prior conversation preserved`}
            style={{ fontFamily: "var(--font-mono)", fontSize: 10, padding: "2px 6px" }}
          >
            ↻ retry from here
          </button>
        )}
      </div>
      {message.content && (
        <div style={{
          alignSelf: "flex-start", maxWidth: "80%", padding: "8px 12px",
          background: "var(--bg3)", border: "1px solid var(--border)",
          borderRadius: "var(--radius)", color: "var(--t0)", fontSize: 13, lineHeight: 1.5,
          whiteSpace: "pre-wrap", wordBreak: "break-word",
        }}>
          {message.content}
        </div>
      )}
      {message.tool_calls && message.tool_calls.length > 0 && (
        <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
          {message.tool_calls.map((tc, i) => (
            <ToolCallChip key={tc.id || i} call={tc} />
          ))}
        </div>
      )}
    </div>
  );
}

function ToolCallChip({ call }: { call: NonNullable<AgentConversationMessage["tool_calls"]>[number] }) {
  // Pretty-print the JSON arguments when possible — collapsed to a
  // single line for compactness, with a click-to-expand affordance.
  const argsText = (() => {
    if (!call.function?.arguments) return "";
    try {
      const parsed = JSON.parse(call.function.arguments);
      return JSON.stringify(parsed);
    } catch {
      return call.function.arguments;
    }
  })();
  return (
    <div style={{
      alignSelf: "flex-start", maxWidth: "90%",
      padding: "6px 10px", background: "var(--bg2)",
      border: "1px solid var(--teal-border)", borderRadius: "var(--radius-sm)",
      fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t1)",
    }}>
      <span style={{ color: "var(--teal)", fontWeight: 500 }}>→ {call.function?.name || "(unknown)"}</span>
      {argsText && (
        <>
          <span style={{ color: "var(--t3)" }}> </span>
          <span title={argsText} style={{ color: "var(--t2)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", maxWidth: "100%", display: "inline-block", verticalAlign: "bottom" }}>
            {argsText.length > 200 ? argsText.slice(0, 200) + "…" : argsText}
          </span>
        </>
      )}
    </div>
  );
}
