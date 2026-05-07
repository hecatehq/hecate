import { useState } from "react";

import type { AgentChatActivityRecord, AgentChatChangedFileDiffRecord, AgentChatChangedFileRecord, AgentChatTimingRecord, AgentChatUsageRecord } from "../../types/runtime";
import { CodeBlock } from "../shared/Atoms";
import { Icon, Icons } from "../shared/Icons";
import { TranscriptActivityTimeline } from "./TranscriptActivityTimeline";
import { TranscriptDiffReview } from "./TranscriptDiffReview";
import { TranscriptMarkdown } from "./TranscriptMarkdown";

export function TranscriptMessageRow({ id, role, model, content, time, promptTokens, completionTokens, costUsd, badge, runtimeMeta, taskLink, traceLink, activities, diffStat, diff, agentSessionID, onListAgentFiles, onGetAgentFileDiff, onRevertAgentFiles, rawOutput, agentUsage, agentTiming, error, onCopy, copied }: {
  id: string; role: "user" | "assistant"; model?: string; content: string;
  time: string; promptTokens?: number; completionTokens?: number; costUsd?: string;
  badge?: string; runtimeMeta?: string; agentSessionID?: string;
  taskLink?: { label: string; title?: string; onClick: () => void };
  traceLink?: { label: string; title?: string; onClick: () => void };
  activities?: AgentChatActivityRecord[]; diffStat?: string; diff?: string;
  onListAgentFiles?: (sessionID: string, messageID: string) => Promise<AgentChatChangedFileRecord[]>;
  onGetAgentFileDiff?: (sessionID: string, messageID: string, path: string) => Promise<AgentChatChangedFileDiffRecord | null>;
  onRevertAgentFiles?: (sessionID: string, messageID: string, paths: string[]) => Promise<boolean>;
  rawOutput?: string; agentUsage?: AgentChatUsageRecord; agentTiming?: AgentChatTimingRecord; error?: string;
  onCopy: (id: string, text: string) => void; copied: boolean;
}) {
  const [hovered, setHovered] = useState(false);
  const isAssistant = role === "assistant";
  const hasTokenData = isAssistant && (promptTokens ?? 0) > 0;
  const showRawOutput = isAssistant && rawOutput && rawOutput.trim() && rawOutput.trim() !== content.trim();
  const waitingForAgentOutput = isAssistant && !content.trim() && activities?.some(isActiveAgentActivity);
  const failed = isAssistant && badge === "failed";
  const cancelled = isAssistant && badge === "cancelled";
  const thinkingForAgent = isAssistant
    && badge === "running"
    && content.trim() !== ""
    && isLikelyTransientAgentNarration(content)
    && !(activities ?? []).some(activity => activity.type === "tool_call");
  const renderActivityAdvanced = isAssistant && activities?.length
    ? (activity: AgentChatActivityRecord) => renderAgentActivityAdvanced(activity, activities, taskLink)
    : undefined;

  return (
    <div onMouseEnter={() => setHovered(true)} onMouseLeave={() => setHovered(false)}
      style={{ padding: "4px 16px 12px", maxWidth: 820, margin: "0 auto", width: "100%" }}>
      <div style={{ display: "flex", gap: 10, alignItems: "flex-start" }}>
        <div style={{
          width: 28, height: 28, borderRadius: "var(--radius-sm)", flexShrink: 0, marginTop: 2,
          background: isAssistant ? "var(--teal-bg)" : "var(--bg3)",
          border: `1px solid ${isAssistant ? "var(--teal-border)" : "var(--border)"}`,
          display: "flex", alignItems: "center", justifyContent: "center",
        }}>
          <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: isAssistant ? "var(--teal)" : "var(--t1)", fontWeight: 600 }}>
            {isAssistant ? (model || "H")[0].toUpperCase() : "U"}
          </span>
        </div>
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ display: "flex", alignItems: "center", flexWrap: "wrap", gap: "5px 8px", marginBottom: 5 }}>
            {isAssistant
              ? <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--teal)" }}>{model || "hecate"}</span>
              : <span style={{ fontSize: 11, color: "var(--t2)", fontWeight: 500 }}>You</span>
            }
            <span style={{ fontSize: 10, color: "var(--t3)", fontFamily: "var(--font-mono)" }}>{time}</span>
            {hasTokenData && (
              <span style={{ fontSize: 10, color: "var(--t3)", fontFamily: "var(--font-mono)" }}>
                {promptTokens}↑ {completionTokens}↓
                {costUsd && costUsd !== "0" ? ` · $${Number(costUsd).toFixed(5)}` : ""}
              </span>
            )}
            {isAssistant && badge && (
              <span className="badge badge-muted" style={{ fontSize: 10 }}>{badge}</span>
            )}
            {isAssistant && taskLink && (
              <HeaderMetaButton label={taskLink.label} title={taskLink.title} onClick={taskLink.onClick} />
            )}
            {isAssistant && traceLink && (
              <HeaderMetaButton label={traceLink.label} title={traceLink.title} onClick={traceLink.onClick} />
            )}
            {isAssistant && runtimeMeta && (
              <span style={{ fontSize: 10, color: "var(--t3)", fontFamily: "var(--font-mono)" }}>{runtimeMeta}</span>
            )}
            <div style={{ marginLeft: "auto", display: "flex", gap: 4, opacity: hovered ? 1 : 0, transition: "opacity 0.15s" }}>
              <button className="btn btn-ghost btn-sm" style={{ padding: "2px 6px", gap: 4 }}
                onClick={() => onCopy(id, content)}>
                <Icon d={copied ? Icons.check : Icons.copy} size={12} />
              </button>
            </div>
          </div>
          {failed || cancelled ? (
            <AgentRunNotice status={failed ? "failed" : "cancelled"} message={error || content} />
          ) : thinkingForAgent ? (
            <AgentLiveText content={content} />
          ) : waitingForAgentOutput ? (
            <div style={{ alignItems: "center", color: "var(--t2)", display: "flex", fontSize: 13, gap: 8, lineHeight: 1.7 }}>
              <span style={{ background: "var(--teal)", borderRadius: 999, display: "inline-block", height: 6, opacity: 0.8, width: 6 }} />
              Waiting for agent output...
            </div>
          ) : (
            <TranscriptMarkdown content={content} />
          )}
          {isAssistant && activities && activities.length > 0 && (
            <TranscriptActivityTimeline
              activities={activities}
              diffStat={diffStat}
              renderAdvancedActivity={renderActivityAdvanced}
            />
          )}
          {isAssistant && agentTiming && !agentTimingEmpty(agentTiming) && (
            <AgentTiming timing={agentTiming} />
          )}
          {isAssistant && agentUsage && !agentUsageEmpty(agentUsage) && (
            <AgentUsage usage={agentUsage} />
          )}
          {isAssistant && (diff || diffStat) && (
            <TranscriptDiffReview
              sessionID={agentSessionID ?? ""}
              messageID={id}
              diffStat={diffStat}
              diff={diff}
              onListFiles={onListAgentFiles}
              onGetFileDiff={onGetAgentFileDiff}
              onRevertFiles={onRevertAgentFiles}
            />
          )}
          {showRawOutput && (
            <details style={{ marginTop: 8 }}>
              <summary style={{ cursor: "pointer", fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)" }}>
                raw adapter output{rawOutput ? ` · ${formatLineCount(rawOutput)}` : ""}
              </summary>
              <div style={{ marginTop: 6 }}>
                <CodeBlock code={rawOutput} lang="text" />
              </div>
            </details>
          )}
        </div>
      </div>
    </div>
  );
}

function renderAgentActivityAdvanced(
  activity: AgentChatActivityRecord,
  activities: AgentChatActivityRecord[],
  taskLink?: { label: string; title?: string; onClick: () => void },
) {
  if (activity.type !== "tool_call" || activity.status !== "failed") return null;

  const outputArtifacts = relatedOutputArtifacts(activities);
  if (outputArtifacts.length === 0) return null;

  return (
    <div style={{ display: "grid", gap: 7 }}>
      <div style={{ color: "var(--t2)", fontSize: 11, lineHeight: 1.5 }}>
        This tool failed. Inspect the related run output for details.
      </div>
      <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
        {outputArtifacts.map(artifact => (
          <button
            key={artifact.artifact_id || artifact.title}
            type="button"
            className="btn btn-ghost btn-sm"
            onClick={taskLink?.onClick}
            disabled={!taskLink}
            title={taskLink ? `Open ${taskLink.label} output` : "Open the backing task to inspect this output"}
            style={{
              borderColor: artifact.title.toLowerCase().includes("stderr") ? "rgba(239, 95, 95, 0.35)" : "var(--border)",
              color: artifact.title.toLowerCase().includes("stderr") ? "var(--red)" : "var(--t1)",
              fontFamily: "var(--font-mono)",
              fontSize: 10,
              padding: "2px 7px",
            }}
          >
            {artifact.title}
          </button>
        ))}
      </div>
    </div>
  );
}

function relatedOutputArtifacts(activities: AgentChatActivityRecord[]): AgentChatActivityRecord[] {
  const seen = new Set<string>();
  const out: AgentChatActivityRecord[] = [];
  for (const activity of activities) {
    if (activity.type !== "artifact") continue;
    if ((activity.artifact_size_bytes ?? 0) <= 0) continue;
    const label = `${activity.title} ${activity.detail ?? ""} ${activity.kind ?? ""}`.toLowerCase();
    if (!/\b(std(out|err)|git-std(out|err))\b/.test(label)) continue;
    const key = activity.artifact_id || activity.title;
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(activity);
  }
  return out;
}

function HeaderMetaButton({
  label,
  title,
  onClick,
}: {
  label: string;
  title?: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      className="btn btn-ghost btn-sm"
      onClick={onClick}
      title={title}
      aria-label={`Open ${label}`}
      style={{
        borderColor: "var(--border)",
        color: "var(--t2)",
        fontFamily: "var(--font-mono)",
        fontSize: 10,
        gap: 4,
        padding: "1px 6px",
      }}
    >
      {label}
    </button>
  );
}

function isLikelyTransientAgentNarration(text: string): boolean {
  const normalized = text.trim().toLowerCase();
  if (!normalized) return false;
  return [
    "i'll ",
    "i’ll ",
    "i will ",
    "i'm going to ",
    "i’m going to ",
    "i'm checking ",
    "i’m checking ",
    "i'll check ",
    "i’ll check ",
    "i'll inspect ",
    "i’ll inspect ",
    "let me ",
    "checking ",
  ].some(prefix => normalized.startsWith(prefix));
}

function isActiveAgentActivity(activity: AgentChatActivityRecord): boolean {
  return activity.status === "running" || activity.status === "in_progress";
}

function AgentRunNotice({ status, message }: { status: "failed" | "cancelled"; message: string }) {
  const color = status === "failed" ? "var(--red)" : "var(--amber)";
  return (
    <div style={{
      border: "1px solid var(--border)",
      borderLeft: `3px solid ${color}`,
      borderRadius: "var(--radius-sm)",
      background: "var(--bg2)",
      padding: "9px 10px",
    }}>
      <div style={{ color, fontFamily: "var(--font-mono)", fontSize: 11, marginBottom: 4 }}>
        agent run {status}
      </div>
      {message && (
        <div style={{ color: "var(--t0)", fontSize: 13, lineHeight: 1.6, whiteSpace: "pre-wrap" }}>
          {message}
        </div>
      )}
    </div>
  );
}

function AgentLiveText({ content }: { content: string }) {
  return (
    <div style={{ alignItems: "baseline", display: "flex", gap: 6, minWidth: 0 }}>
      <div style={{ color: "var(--t0)", flex: "0 1 auto", fontSize: 13, lineHeight: 1.7, minWidth: 0, whiteSpace: "pre-wrap" }}>
        {content}
      </div>
      <span
        aria-hidden="true"
        style={{
          animation: "hecate-live-caret 1.1s ease-in-out infinite",
          background: "var(--teal)",
          borderRadius: 999,
          display: "inline-block",
          flexShrink: 0,
          height: 5,
          opacity: 0.75,
          transform: "translateY(-1px)",
          width: 5,
        }}
      />
    </div>
  );
}

function AgentUsage({ usage }: { usage: AgentChatUsageRecord }) {
  const cost = formatAgentReportedCost(usage);
  const context = formatAgentContextUsage(usage);
  return (
    <div style={{ display: "flex", flexWrap: "wrap", gap: 8, marginTop: 8, fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
      {cost && <span>{cost}</span>}
      {context && <span>{context}</span>}
      <span>reported by adapter · not enforced by Hecate</span>
    </div>
  );
}

function AgentTiming({ timing }: { timing: AgentChatTimingRecord }) {
  const bottleneck = timing.bottleneck && timing.bottleneck_ms
    ? `${humanTimingLabel(timing.bottleneck)} ${formatDurationMS(timing.bottleneck_ms)}`
    : "";
  const items = [
    ["total", timing.total_ms],
    ["queue", timing.queue_ms],
    ["model", timing.model_ms],
    ["tools", timing.tool_ms],
    ["approval", timing.approval_wait_ms],
    ["overhead", timing.overhead_ms],
  ].filter(([, value]) => typeof value === "number" && value > 0) as [string, number][];
  const counts = [
    timing.turn_count ? `${timing.turn_count} turn${timing.turn_count === 1 ? "" : "s"}` : "",
    timing.tool_count ? `${timing.tool_count} tool${timing.tool_count === 1 ? "" : "s"}` : "",
  ].filter(Boolean).join(" · ");
  return (
    <div
      aria-label="Hecate Agent timing summary"
      style={{
        background: "rgba(0, 194, 184, 0.05)",
        border: "1px solid var(--teal-border)",
        borderRadius: "var(--radius-sm)",
        color: "var(--t2)",
        display: "flex",
        flexWrap: "wrap",
        fontFamily: "var(--font-mono)",
        fontSize: 10,
        gap: 8,
        lineHeight: 1.6,
        marginTop: 8,
        padding: "7px 9px",
      }}
    >
      {bottleneck && <span style={{ color: "var(--teal)" }}>bottleneck · {bottleneck}</span>}
      {items.map(([label, value]) => (
        <span key={label}>{label} {formatDurationMS(value)}</span>
      ))}
      {counts && <span>{counts}</span>}
    </div>
  );
}

function agentTimingEmpty(timing: AgentChatTimingRecord): boolean {
  return !timing.total_ms &&
    !timing.queue_ms &&
    !timing.model_ms &&
    !timing.tool_ms &&
    !timing.approval_wait_ms &&
    !timing.overhead_ms &&
    !timing.turn_count &&
    !timing.tool_count &&
    !timing.bottleneck;
}

function agentUsageEmpty(usage: AgentChatUsageRecord): boolean {
  return !usage.reported_cost_amount && !usage.reported_cost_currency && !(usage.context_size ?? 0) && !(usage.context_used ?? 0);
}

function humanTimingLabel(label: string): string {
  return label === "tools" ? "tools" : label;
}

function formatDurationMS(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "0ms";
  if (value < 1000) return `${Math.round(value)}ms`;
  if (value < 60_000) return `${(value / 1000).toFixed(value < 10_000 ? 1 : 0)}s`;
  const minutes = Math.floor(value / 60_000);
  const seconds = Math.round((value - minutes * 60_000) / 1000);
  return `${minutes}m ${seconds}s`;
}

function formatAgentReportedCost(usage: AgentChatUsageRecord): string {
  if (!usage.reported_cost_amount && !usage.reported_cost_currency) return "";
  const currency = usage.reported_cost_currency ? ` ${usage.reported_cost_currency}` : "";
  return `${usage.reported_cost_amount || "0"}${currency}`;
}

function formatAgentContextUsage(usage: AgentChatUsageRecord): string {
  const used = usage.context_used ?? 0;
  const size = usage.context_size ?? 0;
  if (!used && !size) return "";
  if (!size) return `${used} context used`;
  return `${used}/${size} context`;
}

function formatLineCount(value: string): string {
  const trimmed = value.trim();
  if (!trimmed) return "0 lines";
  const count = trimmed.split(/\r?\n/).length;
  return `${count} line${count === 1 ? "" : "s"}`;
}
