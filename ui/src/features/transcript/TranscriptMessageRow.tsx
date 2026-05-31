import { useState } from "react";

import type {
  ChatActivityRecord,
  ChatContextPacketRecord,
  ChatTimingRecord,
  ChatUsageRecord,
} from "../../types/chat";
import { formatDurationMs } from "../../lib/format";
import { CodeBlock } from "../shared/Atoms";
import { BrandAvatar } from "../shared/BrandAvatar";
import { DiffViewer } from "../shared/DiffViewer";
import { Icon, Icons } from "../shared/Icons";
import { DiffStatList, TranscriptActivityTimeline } from "./TranscriptActivityTimeline";
import { TranscriptMarkdown } from "./TranscriptMarkdown";
import { capturedToolOutput } from "./transcriptActivityHelpers";

const ANSI_ESCAPE_PATTERN = new RegExp(`${String.fromCharCode(27)}\\[[0-9;]*m`, "g");

export function TranscriptMessageRow({
  id,
  role,
  model,
  brand,
  content,
  diffStat,
  diff,
  time,
  promptTokens,
  completionTokens,
  costUsd,
  badge,
  runtimeMeta,
  runtimeMetaTitle,
  taskLink,
  traceLink,
  changedFilesLink,
  activities,
  rawOutput,
  agentUsage,
  agentTiming,
  contextPacket,
  error,
  setupAction,
  onCopy,
  copied,
}: {
  id: string;
  role: "user" | "assistant";
  model?: string;
  brand?: string;
  content: string;
  diffStat?: string;
  diff?: string;
  time: string;
  promptTokens?: number;
  completionTokens?: number;
  costUsd?: string;
  badge?: string;
  runtimeMeta?: string;
  runtimeMetaTitle?: string;
  taskLink?: { label: string; title?: string; onClick: () => void };
  traceLink?: { label: string; title?: string; onClick: () => void };
  changedFilesLink?: { label: string; title?: string; onClick?: () => void };
  activities?: ChatActivityRecord[];
  rawOutput?: string;
  agentUsage?: ChatUsageRecord;
  agentTiming?: ChatTimingRecord;
  contextPacket?: ChatContextPacketRecord;
  error?: string;
  // setupAction is an inline button rendered inside the agent-run
  // failure notice. The chat passes it when the failure has a
  // known one-click recovery path — currently just the Claude Code
  // auth error (where the button deep-links to the guided setup
  // card in Connections). Optional in all other
  // cases.
  setupAction?: { label: string; title?: string; onClick: () => void };
  onCopy: (id: string, text: string) => void;
  copied: boolean;
}) {
  const [hovered, setHovered] = useState(false);
  const isAssistant = role === "assistant";
  const hasTokenData = isAssistant && (promptTokens ?? 0) > 0;
  const failed = isAssistant && badge === "failed";
  const cancelled = isAssistant && badge === "cancelled";
  const showRawOutput =
    isAssistant &&
    rawOutput &&
    rawOutput.trim() &&
    rawOutput.trim() !== content.trim() &&
    !(cancelled && isRoutineCancellationRawOutput(rawOutput));
  const waitingForAgentOutput =
    isAssistant && !content.trim() && activities?.some(isActiveAgentActivity);
  const thinkingForAgent =
    isAssistant &&
    badge === "running" &&
    content.trim() !== "" &&
    isLikelyTransientAgentNarration(content) &&
    !(activities ?? []).some((activity) => activity.type === "tool_call");
  const visibleActivities =
    isAssistant && activities?.length
      ? activities.filter((activity) => {
          if ((failed || cancelled) && isTerminalSessionMetadata(activity)) return false;
          if ((failed || cancelled) && isStaleTerminalPlaceholder(activity)) return false;
          if (failed && duplicatesFailureNotice(activity, error || content)) return false;
          return true;
        })
      : activities;
  const renderActivityAdvanced =
    isAssistant && visibleActivities?.length
      ? (activity: ChatActivityRecord) =>
          renderAgentActivityAdvanced(activity, visibleActivities, {
            taskLink,
            diffStat,
            diff,
          })
      : undefined;
  const showCapturedDiff =
    isAssistant &&
    Boolean(diffStat?.trim() || diff?.trim()) &&
    !changedFilesLink &&
    !(visibleActivities ?? []).some(isFilesChangedActivity);

  return (
    <div
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      style={{ padding: "4px 16px 12px", maxWidth: 820, margin: "0 auto", width: "100%" }}
    >
      <div style={{ display: "flex", gap: 10, alignItems: "flex-start" }}>
        <BrandAvatar
          assistant={isAssistant}
          brand={isAssistant ? brand || model : undefined}
          fallback={isAssistant ? model : "U"}
          size={28}
          style={{ marginTop: 2 }}
        />
        <div style={{ flex: 1, minWidth: 0 }}>
          <div
            style={{
              display: "flex",
              alignItems: "center",
              flexWrap: "wrap",
              gap: "5px 8px",
              marginBottom: 5,
            }}
          >
            {isAssistant ? (
              <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--teal)" }}>
                {model || "hecate"}
              </span>
            ) : (
              <span style={{ fontSize: 11, color: "var(--t2)", fontWeight: 500 }}>You</span>
            )}
            <span style={{ fontSize: 10, color: "var(--t3)", fontFamily: "var(--font-mono)" }}>
              {time}
            </span>
            {hasTokenData && (
              <span style={{ fontSize: 10, color: "var(--t3)", fontFamily: "var(--font-mono)" }}>
                {promptTokens}↑ {completionTokens}↓
                {costUsd && costUsd !== "0" ? ` · $${Number(costUsd).toFixed(5)}` : ""}
              </span>
            )}
            {isAssistant && badge && (
              <span className="badge badge-muted" style={{ fontSize: 10 }}>
                {badge}
              </span>
            )}
            {isAssistant && taskLink && (
              <HeaderMetaButton
                label={taskLink.label}
                title={taskLink.title}
                onClick={taskLink.onClick}
              />
            )}
            {isAssistant && traceLink && (
              <HeaderMetaButton
                label={traceLink.label}
                title={traceLink.title}
                onClick={traceLink.onClick}
              />
            )}
            {isAssistant && changedFilesLink && (
              <HeaderMetaButton
                label={changedFilesLink.label}
                title={changedFilesLink.title}
                onClick={changedFilesLink.onClick}
              />
            )}
            {isAssistant && runtimeMeta && (
              <span
                title={runtimeMetaTitle}
                style={{ fontSize: 10, color: "var(--t3)", fontFamily: "var(--font-mono)" }}
              >
                {runtimeMeta}
              </span>
            )}
            <div
              style={{
                marginLeft: "auto",
                display: "flex",
                gap: 4,
                opacity: hovered ? 1 : 0,
                transition: "opacity 0.15s",
              }}
            >
              <button
                className="btn btn-ghost btn-sm"
                style={{ padding: "2px 6px", gap: 4 }}
                onClick={() => onCopy(id, content)}
              >
                <Icon d={copied ? Icons.check : Icons.copy} size={12} />
              </button>
            </div>
          </div>
          {failed ? (
            <>
              {shouldRenderFailedContent(content, error) ? (
                <TranscriptMarkdown content={content} />
              ) : null}
              <AgentRunNotice status="failed" message={error || content} action={setupAction} />
            </>
          ) : cancelled ? (
            <>
              {/* Cancel messages may be normalized by the backend; preserve partial text as-is. */}
              {content.trim() ? <TranscriptMarkdown content={content} /> : null}
              <AgentRunNotice
                status="cancelled"
                message={error || "Stopped before the agent returned more output."}
              />
            </>
          ) : thinkingForAgent ? (
            <AgentLiveText content={content} />
          ) : waitingForAgentOutput ? (
            <div
              style={{
                alignItems: "center",
                color: "var(--t2)",
                display: "flex",
                fontSize: 13,
                gap: 8,
                lineHeight: 1.7,
              }}
            >
              <span
                style={{
                  background: "var(--teal)",
                  borderRadius: 999,
                  display: "inline-block",
                  height: 6,
                  opacity: 0.8,
                  width: 6,
                }}
              />
              Waiting for agent output...
            </div>
          ) : (
            <TranscriptMarkdown content={content} />
          )}
          {isAssistant && visibleActivities && visibleActivities.length > 0 && (
            <TranscriptActivityTimeline
              activities={visibleActivities}
              renderAdvancedActivity={renderActivityAdvanced}
            />
          )}
          {showCapturedDiff && <CapturedDiffDetails diffStat={diffStat} diff={diff} />}
          {isAssistant && agentTiming && !agentTimingEmpty(agentTiming) && (
            <AgentTiming timing={agentTiming} />
          )}
          {isAssistant && contextPacket && !contextPacketEmpty(contextPacket) && (
            <ContextInspector packet={contextPacket} />
          )}
          {isAssistant && agentUsage && !agentUsageEmpty(agentUsage) && (
            <AgentUsage usage={agentUsage} />
          )}
          {showRawOutput && (
            <details style={{ marginTop: 8 }}>
              <summary
                style={{
                  cursor: "pointer",
                  fontFamily: "var(--font-mono)",
                  fontSize: 11,
                  color: "var(--t3)",
                }}
              >
                raw agent output{rawOutput ? ` · ${formatLineCount(rawOutput)}` : ""}
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
  activity: ChatActivityRecord,
  activities: ChatActivityRecord[],
  options: {
    taskLink?: { label: string; title?: string; onClick: () => void };
    diffStat?: string;
    diff?: string;
  },
) {
  if (activity.type === "changed_files" || activity.type === "files_changed") {
    return (
      <ActivityFilesPreview activity={activity} diffStat={options.diffStat} diff={options.diff} />
    );
  }

  if (
    activity.type === "output" ||
    (activity.type === "artifact" && isOutputArtifactActivity(activity))
  ) {
    return <OutputArtifactPreview artifact={activity} />;
  }

  const toolOutput = capturedToolOutput(activity);
  if (toolOutput) {
    return (
      <ToolOutputPreview
        title={activity.kind === "read" ? "Read output" : "Tool output"}
        output={toolOutput}
      />
    );
  }

  if (activity.type !== "tool_call" || activity.status !== "failed") return null;

  const outputArtifacts = relatedOutputArtifacts(activities);
  if (outputArtifacts.length === 0) return null;

  return (
    <div style={{ display: "grid", gap: 7 }}>
      <div style={{ color: "var(--t2)", fontSize: 11, lineHeight: 1.5 }}>
        This tool failed. Preview the related run output here, or open the backing task for the full
        capture.
      </div>
      <div style={{ display: "grid", gap: 7 }}>
        {outputArtifacts.map((artifact) => (
          <OutputArtifactPreview key={artifact.artifact_id || artifact.title} artifact={artifact} />
        ))}
      </div>
      {options.taskLink && (
        <div>
          <button
            type="button"
            className="btn btn-ghost btn-sm"
            onClick={options.taskLink.onClick}
            title={`Open ${options.taskLink.label} output`}
            style={{ fontSize: 10, padding: "2px 7px" }}
          >
            Open task output
          </button>
        </div>
      )}
    </div>
  );
}

function ActivityFilesPreview({
  activity,
  diffStat,
  diff,
}: {
  activity: ChatActivityRecord;
  diffStat?: string;
  diff?: string;
}) {
  const fallbackStat = [activity.detail, activity.title]
    .filter((value): value is string => Boolean(value?.trim()))
    .join("\n");
  const stat = (diffStat?.trim() ? diffStat : fallbackStat).trim();
  const patch = diff?.trim() ?? "";

  if (!stat && !patch) {
    return (
      <div style={{ color: "var(--t3)", fontSize: 11, lineHeight: 1.5 }}>
        Workspace changes were captured, but this snapshot does not include a diffstat preview.
      </div>
    );
  }

  return (
    <div style={{ display: "grid", gap: 7 }}>
      {patch ? <DiffViewer diff={patch} compact embedded /> : <DiffStatList diffStat={stat} />}
    </div>
  );
}

function ToolOutputPreview({ title, output }: { title: string; output: string }) {
  const preview = normalizeToolOutputPreview(output);
  return (
    <div
      style={{
        background: "var(--bg0)",
        border: "1px solid var(--border)",
        borderRadius: "var(--radius-sm)",
        overflow: "hidden",
      }}
    >
      <div
        style={{
          borderBottom: "1px solid var(--border)",
          color: "var(--t1)",
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          padding: "4px 7px",
        }}
      >
        {title}
      </div>
      <pre
        style={{
          color: "var(--t1)",
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          lineHeight: 1.55,
          margin: 0,
          maxHeight: 180,
          overflow: "auto",
          padding: "7px",
          whiteSpace: "pre-wrap",
        }}
      >
        {preview}
      </pre>
    </div>
  );
}

function normalizeToolOutputPreview(output: string): string {
  const withoutAnsi = stripAnsi(output)
    .replace(/\r\n/g, "\n")
    .replace(/\r/g, "\n");

  return withoutAnsi
    .replace(/(?:^|[ \t\n])\d{1,6}\s*(?:>|→|\|)\s*/g, "\n")
    .replace(/^\n+/, "")
    .replace(/\n{3,}/g, "\n\n")
    .trim();
}

function stripAnsi(value: string): string {
  return value.replace(ANSI_ESCAPE_PATTERN, "");
}

function CapturedDiffDetails({ diffStat, diff }: { diffStat?: string; diff?: string }) {
  const stat = diffStat?.trim() ?? "";
  const patch = diff?.trim() ?? "";
  const summary = stat ? formatCapturedDiffSummary(stat) : "captured workspace diff";
  return (
    <details style={{ marginTop: 8 }}>
      <summary
        style={{
          color: "var(--t3)",
          cursor: "pointer",
          fontFamily: "var(--font-mono)",
          fontSize: 11,
        }}
      >
        workspace changes{summary ? ` · ${summary}` : ""}
      </summary>
      <div style={{ display: "grid", gap: 7, marginTop: 6 }}>
        {stat && <DiffStatList diffStat={stat} />}
        {patch && <DiffViewer diff={patch} />}
      </div>
    </details>
  );
}

function formatCapturedDiffSummary(diffStat: string): string {
  const lines = diffStat
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean);
  return lines[lines.length - 1] ?? "";
}

function OutputArtifactPreview({ artifact }: { artifact: ChatActivityRecord }) {
  const isStderr = outputArtifactStream(artifact) === "stderr";
  const preview = artifact.artifact_preview?.replace(/[\r\n]+$/, "");
  return (
    <div
      style={{
        border: `1px solid ${isStderr ? "rgba(239, 95, 95, 0.28)" : "var(--border)"}`,
        borderRadius: "var(--radius-sm)",
        background: "var(--bg0)",
        overflow: "hidden",
      }}
    >
      <div
        style={{
          alignItems: "center",
          borderBottom: "1px solid var(--border)",
          display: "flex",
          gap: 8,
          padding: "4px 7px",
        }}
      >
        <span
          style={{
            color: isStderr ? "var(--red)" : "var(--t1)",
            fontFamily: "var(--font-mono)",
            fontSize: 10,
          }}
        >
          {artifact.title}
        </span>
        {artifact.artifact_size_bytes ? (
          <span style={{ color: "var(--t3)", fontFamily: "var(--font-mono)", fontSize: 10 }}>
            {artifact.artifact_size_bytes}b
          </span>
        ) : null}
      </div>
      {preview ? (
        <pre
          style={{
            color: isStderr ? "var(--red)" : "var(--t1)",
            fontFamily: "var(--font-mono)",
            fontSize: 10,
            lineHeight: 1.55,
            margin: 0,
            maxHeight: 130,
            overflow: "auto",
            padding: "7px",
            whiteSpace: "pre-wrap",
          }}
        >
          {preview}
        </pre>
      ) : (
        <div style={{ color: "var(--t3)", fontSize: 11, padding: "7px" }}>
          No output preview was captured for this snapshot.
        </div>
      )}
    </div>
  );
}

function relatedOutputArtifacts(activities: ChatActivityRecord[]): ChatActivityRecord[] {
  const seen = new Set<string>();
  const out: ChatActivityRecord[] = [];
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

function isOutputArtifactActivity(activity: ChatActivityRecord): boolean {
  const label = `${activity.title} ${activity.detail ?? ""} ${activity.kind ?? ""}`.toLowerCase();
  return /\b(std(out|err)|git-std(out|err))\b/.test(label);
}

function outputArtifactStream(activity: ChatActivityRecord): "stdout" | "stderr" {
  const label = `${activity.title} ${activity.detail ?? ""} ${activity.kind ?? ""}`.toLowerCase();
  return label.includes("stderr") ? "stderr" : "stdout";
}

function HeaderMetaButton({
  label,
  title,
  onClick,
}: {
  label: string;
  title?: string;
  onClick?: () => void;
}) {
  if (!onClick) {
    return (
      <span
        title={title}
        style={{
          border: "1px solid var(--border)",
          borderRadius: "var(--radius-sm)",
          color: "var(--t2)",
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          lineHeight: 1.5,
          padding: "1px 6px",
        }}
      >
        {label}
      </span>
    );
  }
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
  ].some((prefix) => normalized.startsWith(prefix));
}

function isActiveAgentActivity(activity: ChatActivityRecord): boolean {
  return activity.status === "running" || activity.status === "in_progress";
}

function isStaleTerminalPlaceholder(activity: ChatActivityRecord): boolean {
  return (
    isActiveAgentActivity(activity) && (activity.type === "running" || activity.type === "started")
  );
}

function isTerminalSessionMetadata(activity: ChatActivityRecord): boolean {
  return (
    activity.type === "resumed" || activity.type === "started" || activity.type === "recovered"
  );
}

function isFilesChangedActivity(activity: ChatActivityRecord): boolean {
  // Keep in sync with changed-file activity rows emitted from
  // internal/api/handler_chat_activities.go and handler_chat_files.go.
  return activity.type === "changed_files" || activity.type === "files_changed";
}

function isRoutineCancellationRawOutput(rawOutput: string): boolean {
  return rawOutput.trim().toLowerCase() === "context canceled";
}

function shouldRenderFailedContent(content: string, error?: string): boolean {
  const visible = content.trim();
  if (!visible) return false;
  return visible !== (error ?? "").trim();
}

function duplicatesFailureNotice(activity: ChatActivityRecord, message: string): boolean {
  if (activity.type !== "failed" && activity.status !== "failed") return false;
  if (activity.type === "tool_call") return false;
  // Keep this title list in sync with generic terminal failure rows from
  // internal/api/handler_chat_activities.go and handler_chat.go. Richer
  // diagnostic titles should remain visible in the activity timeline.
  const title = activity.title.trim().toLowerCase();
  if (title !== "failed" && title !== "run failed") return false;
  const detail = activity.detail?.trim() ?? "";
  if (!detail) return true;
  return detail === message.trim();
}

function AgentRunNotice({
  status,
  message,
  action,
}: {
  status: "failed" | "cancelled";
  message: string;
  action?: { label: string; title?: string; onClick: () => void };
}) {
  const color = status === "failed" ? "var(--red)" : "var(--amber)";
  // The trailing parenthetical marker (e.g. "(claude_code_auth_required)")
  // is intentional in the server-side string — the chat uses it to
  // decide whether to render the recovery action. Strip it from
  // the visible copy so operators don't see the implementation
  // detail.
  const visible = message ? message.replace(/\s*\([a-z][a-z0-9_]+_required\)\s*$/i, "").trim() : "";
  return (
    <div
      style={{
        border: "1px solid var(--border)",
        borderLeft: `3px solid ${color}`,
        borderRadius: "var(--radius-sm)",
        background: "var(--bg2)",
        padding: "9px 10px",
      }}
    >
      <div style={{ color, fontFamily: "var(--font-mono)", fontSize: 11, marginBottom: 4 }}>
        agent run {status}
      </div>
      {visible && (
        <div style={{ color: "var(--t0)", fontSize: 13, lineHeight: 1.6, whiteSpace: "pre-wrap" }}>
          {visible}
        </div>
      )}
      {action && (
        <div style={{ marginTop: 8 }}>
          <button
            type="button"
            className="btn btn-primary btn-sm"
            onClick={action.onClick}
            title={action.title}
            style={{ fontSize: 12, padding: "5px 10px" }}
          >
            {action.label}
          </button>
        </div>
      )}
    </div>
  );
}

function AgentLiveText({ content }: { content: string }) {
  return (
    <div style={{ alignItems: "baseline", display: "flex", gap: 6, minWidth: 0 }}>
      <div
        style={{
          color: "var(--t0)",
          flex: "0 1 auto",
          fontSize: 13,
          lineHeight: 1.7,
          minWidth: 0,
          whiteSpace: "pre-wrap",
        }}
      >
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

function AgentUsage({ usage }: { usage: ChatUsageRecord }) {
  const cost = formatAgentReportedCost(usage);
  const context = formatAgentContextUsage(usage);
  return (
    <div
      style={{
        display: "flex",
        flexWrap: "wrap",
        gap: 8,
        marginTop: 8,
        fontFamily: "var(--font-mono)",
        fontSize: 10,
        color: "var(--t3)",
      }}
    >
      {cost && <span>{cost}</span>}
      {context && <span>{context}</span>}
      <span>reported usage · not enforced by Hecate</span>
    </div>
  );
}

function ContextInspector({ packet }: { packet: ChatContextPacketRecord }) {
  const modelLabel = [packet.provider, packet.model].filter(Boolean).join(" · ");
  const sources = packet.sources ?? [];
  const summaryParts = [
    "context",
    packet.message_count
      ? `${packet.message_count} message${packet.message_count === 1 ? "" : "s"}`
      : "",
    modelLabel,
  ].filter(Boolean);
  return (
    <details style={{ marginTop: 8 }}>
      <summary
        style={{
          color: "var(--t3)",
          cursor: "pointer",
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          lineHeight: 1.6,
        }}
      >
        {summaryParts.join(" · ")}
      </summary>
      <div
        style={{
          background: "var(--bg1)",
          border: "1px solid var(--border)",
          borderRadius: "var(--radius-sm)",
          display: "grid",
          gap: 6,
          marginTop: 6,
          padding: "8px 9px",
        }}
      >
        {packet.execution_mode && (
          <ContextRow label="mode" value={humanExecutionMode(packet.execution_mode)} />
        )}
        {packet.workspace && <ContextRow label="workspace" value={packet.workspace} />}
        {sources.length > 0 && (
          <div style={{ display: "grid", gap: 5 }}>
            {sources.map((source, index) => (
              <ContextSourceRow key={`${source.kind}-${source.label}-${index}`} source={source} />
            ))}
          </div>
        )}
      </div>
    </details>
  );
}

function ContextRow({ label, value }: { label: string; value: string }) {
  return (
    <div style={{ display: "grid", gridTemplateColumns: "96px minmax(0, 1fr)", gap: 8 }}>
      <span style={{ color: "var(--t3)", fontFamily: "var(--font-mono)", fontSize: 10 }}>
        {label}
      </span>
      <span
        style={{
          color: "var(--t1)",
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          minWidth: 0,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
        title={value}
      >
        {value}
      </span>
    </div>
  );
}

function ContextSourceRow({
  source,
}: {
  source: NonNullable<ChatContextPacketRecord["sources"]>[number];
}) {
  const detail = source.detail?.trim();
  const label = source.label || source.kind;
  const trust = source.trust ? ` · ${source.trust}` : "";
  return (
    <div
      style={{
        borderTop: "1px solid var(--border)",
        display: "grid",
        gap: 2,
        paddingTop: 5,
      }}
    >
      <div style={{ color: "var(--t1)", fontFamily: "var(--font-mono)", fontSize: 10 }}>
        {label}
        <span style={{ color: "var(--t3)" }}>{trust}</span>
      </div>
      {detail && (
        <div
          style={{
            color: "var(--t3)",
            fontSize: 10,
            lineHeight: 1.5,
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
          title={detail}
        >
          {detail}
        </div>
      )}
    </div>
  );
}

function AgentTiming({ timing }: { timing: ChatTimingRecord }) {
  const bottleneck =
    timing.bottleneck && timing.bottleneck_ms
      ? `${humanTimingLabel(timing.bottleneck)} ${formatDurationMs(timing.bottleneck_ms)}`
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
  ]
    .filter(Boolean)
    .join(" · ");
  return (
    <div
      aria-label="Hecate Chat timing summary"
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
        <span key={label}>
          {label} {formatDurationMs(value)}
        </span>
      ))}
      {counts && <span>{counts}</span>}
    </div>
  );
}

function contextPacketEmpty(packet: ChatContextPacketRecord): boolean {
  return (
    !packet.version &&
    !packet.execution_mode &&
    !packet.provider &&
    !packet.model &&
    !packet.workspace &&
    !packet.system_prompt_included &&
    !packet.message_count &&
    (packet.sources ?? []).length === 0
  );
}

function humanExecutionMode(mode: string): string {
  switch (mode) {
    case "direct_model":
      return "Direct model chat";
    case "external_agent":
      return "External agent";
    case "hecate_task":
      return "Hecate task runtime";
    default:
      return mode;
  }
}

function agentTimingEmpty(timing: ChatTimingRecord): boolean {
  return (
    !timing.total_ms &&
    !timing.queue_ms &&
    !timing.model_ms &&
    !timing.tool_ms &&
    !timing.approval_wait_ms &&
    !timing.overhead_ms &&
    !timing.turn_count &&
    !timing.tool_count &&
    !timing.bottleneck
  );
}

function agentUsageEmpty(usage: ChatUsageRecord): boolean {
  return (
    !usage.reported_cost_amount &&
    !usage.reported_cost_currency &&
    !(usage.context_size ?? 0) &&
    !(usage.context_used ?? 0)
  );
}

function humanTimingLabel(label: string): string {
  return label === "tools" ? "tools" : label;
}

function formatAgentReportedCost(usage: ChatUsageRecord): string {
  if (!usage.reported_cost_amount && !usage.reported_cost_currency) return "";
  const currency = usage.reported_cost_currency ? ` ${usage.reported_cost_currency}` : "";
  return `${usage.reported_cost_amount || "0"}${currency}`;
}

function formatAgentContextUsage(usage: ChatUsageRecord): string {
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
