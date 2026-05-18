import { useEffect, useRef, useState, type ReactNode } from "react";

import { useChat } from "../../app/state/chat";
import { useChatActions } from "../../app/state/coordinators/chat";
import { useChatTarget } from "../../app/state/derived";
import { useWiredSettingsActions } from "../../app/state/coordinators/wired";
import { formatDurationMs } from "../../lib/format";
import type {
  ChatActivityRecord,
  ChatSegmentRecord,
  ChatTimingRecord,
  ChatUsageRecord,
} from "../../types/chat";
import { CodeBlock, Icon, Icons } from "../shared/ui";
import { TranscriptMessageRow } from "../transcript/TranscriptMessageRow";

import { compactID } from "./ChatComposer";

export type VisibleChatMessage = {
  id: string;
  runtime_kind?: string;
  segment_id?: string;
  task_id?: string;
  run_id?: string;
  request_id?: string;
  trace_id?: string;
  native_session_id?: string;
  role: string;
  content: string | null;
  created_at?: string;
  produced_by_call_id?: string;
  agent_adapter_id?: string;
  agent_adapter_name?: string;
  agent_status?: string;
  cost_mode?: string;
  provider?: string;
  model?: string;
  diff_stat?: string;
  diff?: string;
  raw_output?: string;
  activities?: ChatActivityRecord[];
  usage?: ChatUsageRecord;
  timing?: ChatTimingRecord;
  duration_ms?: number;
  error?: string;
};

export type TranscriptItem =
  | { type: "segment"; key: string; segment: ChatSegmentRecord }
  | { type: "message"; key: string; message: VisibleChatMessage };

type Props = {
  // Active chat shape (derived in ChatView and threaded through here).
  isHecateAgentChat: boolean;
  activeSessionID: string;

  // Transcript content.
  transcriptItems: TranscriptItem[];
  visibleMessageCount: number;
  streaming: boolean;

  // Pre-rendered empty state. ChatView builds the <ChatEmptyState> with
  // its derived props bag; transcript just slots it in below the
  // messages list when there's nothing to show yet.
  emptyState: ReactNode;

  // Cross-region navigation. onOpenTask / onOpenTrace fall back to
  // onNavigate when the parent doesn't wire them.
  onNavigate?: (workspace: "connections" | "runs" | "overview" | "settings") => void;
  onOpenTask?: (taskID: string, runID?: string) => void;
  onOpenTrace?: (requestID: string) => void;
  openClaudeCodeSetup: () => void;
};

export function ChatTranscript({
  isHecateAgentChat,
  activeSessionID,
  transcriptItems,
  visibleMessageCount,
  streaming,
  emptyState,
  onNavigate,
  onOpenTask,
  onOpenTrace,
  openClaudeCodeSetup,
}: Props) {
  const chat = useChat();
  const chatTarget = useChatTarget();
  const { actions: settingsActions } = useWiredSettingsActions();
  const chatActions = useChatActions({
    chatTarget,
    setNoticeMessage: settingsActions.setNoticeMessage,
  });
  const streamingContent = chat.state.streamingContent;
  const activeChatSession = chat.state.activeChatSession;
  const pendingToolCalls = chat.state.pendingToolCalls;
  const activeChatSessionID = chat.state.activeChatSessionID;
  const chatLoading = chat.state.chatLoading;
  const scrollRef = useRef<HTMLDivElement>(null);
  const bottomRef = useRef<HTMLDivElement>(null);
  const userScrolledRef = useRef(false);
  const [atBottom, setAtBottom] = useState(true);
  const [copiedMsgId, setCopiedMsgId] = useState<string | null>(null);

  useEffect(() => {
    if (!userScrolledRef.current) {
      bottomRef.current?.scrollIntoView({ behavior: "instant" });
    }
  }, [streamingContent, visibleMessageCount]);

  useEffect(() => {
    // Reset scroll state on every session change. Focus is NOT applied
    // here on purpose: data-load (sessions arriving from the API) also
    // triggers an activeChatSessionID transition, and stealing focus on
    // load would hijack normal page navigation and screen-reader flow.
    // Focus is instead applied at the explicit user-driven entry points:
    // the New-session button and the session row onClick handlers.
    userScrolledRef.current = false;
    setAtBottom(true);
    bottomRef.current?.scrollIntoView({ behavior: "instant" });
  }, [activeSessionID]);

  function handleScroll() {
    const el = scrollRef.current;
    if (!el) return;
    const threshold = 80;
    const isAtBottom = el.scrollHeight - el.scrollTop - el.clientHeight < threshold;
    setAtBottom(isAtBottom);
    userScrolledRef.current = !isAtBottom;
  }

  function scrollToBottom() {
    userScrolledRef.current = false;
    setAtBottom(true);
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }

  function copyMsg(id: string, text: string) {
    navigator.clipboard?.writeText(text).catch(() => {});
    setCopiedMsgId(id);
    setTimeout(() => setCopiedMsgId(null), 2000);
  }

  return (
    <div style={{ flex: 1, overflow: "hidden", position: "relative" }}>
      <div
        ref={scrollRef}
        onScroll={handleScroll}
        style={{ height: "100%", overflowY: "auto", padding: "16px 0" }}
      >
        {transcriptItems.map((item) => {
          if (item.type === "segment") {
            return <ChatSegmentDivider key={item.key} segment={item.segment} />;
          }
          const m = item.message;
          const role = m.role === "assistant" ? "assistant" : "user";
          const content =
            typeof m.content === "string"
              ? m.content
              : m.content === null
                ? ""
                : JSON.stringify(m.content);
          const time = m.created_at
            ? new Date(m.created_at).toLocaleTimeString("en-US", {
                hour: "2-digit",
                minute: "2-digit",
              })
            : "";
          const agentModel = isHecateAgentChat
            ? m.model || activeChatSession?.model || "Hecate Agent"
            : m.agent_adapter_name || m.agent_adapter_id;
          const agentRuntime =
            role === "assistant"
              ? formatAgentRuntimeMeta(m.run_id, m.duration_ms, m.native_session_id)
              : "";
          const taskID = m.runtime_kind === "agent" ? m.task_id : "";
          const taskRunID = taskID ? m.run_id : "";
          const traceRequestID = m.request_id;
          const traceID = m.trace_id;
          return (
            <TranscriptMessageRow
              key={item.key}
              id={m.id}
              role={role}
              model={agentModel}
              brand={messageBrand(m, isHecateAgentChat)}
              content={content}
              time={time}
              badge={role === "assistant" ? m.agent_status || m.cost_mode : undefined}
              runtimeMeta={agentRuntime}
              taskLink={
                isHecateAgentChat && role === "assistant" && taskID
                  ? {
                      label: formatTaskLinkLabel(taskID),
                      title: formatTaskLinkTitle(taskID, taskRunID),
                      onClick: () => {
                        if (!taskID) return;
                        if (onOpenTask) onOpenTask(taskID, taskRunID);
                        else onNavigate?.("runs");
                      },
                    }
                  : undefined
              }
              traceLink={
                role === "assistant" && traceRequestID
                  ? {
                      label: formatTraceLinkLabel(traceRequestID),
                      title: formatTraceLinkTitle(traceRequestID, traceID),
                      onClick: () => {
                        if (onOpenTrace) onOpenTrace(traceRequestID);
                        else onNavigate?.("overview");
                      },
                    }
                  : undefined
              }
              activities={role === "assistant" ? m.activities : undefined}
              diffStat={role === "assistant" ? m.diff_stat : undefined}
              diff={role === "assistant" ? m.diff : undefined}
              agentSessionID={activeChatSessionID}
              onListAgentFiles={chatActions.listChatMessageFiles}
              onGetAgentFileDiff={chatActions.getChatMessageFileDiff}
              onRevertAgentFiles={chatActions.revertChatMessageFiles}
              rawOutput={role === "assistant" ? m.raw_output : undefined}
              agentUsage={role === "assistant" ? m.usage : undefined}
              agentTiming={role === "assistant" ? m.timing : undefined}
              error={role === "assistant" ? m.error : undefined}
              setupAction={
                // Render the "Open Claude Code setup" button only
                // when the server-side message carries the
                // claude_code_auth_required marker. Pattern-match
                // (not strict equality) is deliberate — the marker
                // is part of a paragraph that may be reworded over
                // time; the token itself is stable contract between
                // internal/agentadapters/auth_status.go and this UI
                // handler.
                role === "assistant" &&
                m.agent_adapter_id === "claude_code" &&
                typeof m.error === "string" &&
                m.error.includes("claude_code_auth_required")
                  ? {
                      label: "Open Claude Code setup",
                      title: "Open Connections and scroll to the guided setup card",
                      onClick: openClaudeCodeSetup,
                    }
                  : undefined
              }
              onCopy={copyMsg}
              copied={copiedMsgId === m.id}
            />
          );
        })}

        {/* Pending tool calls */}
        {pendingToolCalls.length > 0 && (
          <div style={{ padding: "4px 16px 16px", maxWidth: 820, margin: "0 auto", width: "100%" }}>
            <div style={{ fontSize: 11, color: "var(--t2)", marginBottom: 8 }}>
              {pendingToolCalls.length} tool call{pendingToolCalls.length > 1 ? "s" : ""} pending
            </div>
            {pendingToolCalls.map((tc, i) => (
              <div
                key={tc.id}
                style={{
                  border: "1px solid var(--border)",
                  borderRadius: "var(--radius)",
                  padding: "10px 12px",
                  background: "var(--bg2)",
                  marginBottom: 8,
                }}
              >
                <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 6 }}>
                  <span
                    style={{
                      fontFamily: "var(--font-mono)",
                      fontSize: 12,
                      fontWeight: 600,
                      color: "var(--teal)",
                    }}
                  >
                    {tc.name}
                  </span>
                  <span
                    style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}
                  >
                    {tc.id}
                  </span>
                </div>
                <CodeBlock
                  code={(() => {
                    try {
                      return JSON.stringify(JSON.parse(tc.arguments), null, 2);
                    } catch {
                      return tc.arguments;
                    }
                  })()}
                  lang="json"
                />
                <div style={{ marginTop: 8 }}>
                  <label
                    style={{ fontSize: 11, color: "var(--t2)", display: "block", marginBottom: 4 }}
                  >
                    Result
                  </label>
                  <textarea
                    className="input"
                    onChange={(e) => chatActions.updateToolResult(i, e.target.value)}
                    placeholder="Enter the tool result"
                    rows={3}
                    style={{ resize: "vertical" }}
                    value={tc.result}
                  />
                </div>
              </div>
            ))}
            <button
              className="btn btn-primary btn-sm"
              disabled={chatLoading || pendingToolCalls.some((tc) => !tc.result.trim())}
              onClick={() => void chatActions.submitToolResults()}
              type="button"
            >
              {chatLoading ? "Running…" : "Submit results"}
            </button>
          </div>
        )}

        {visibleMessageCount === 0 && !streaming && pendingToolCalls.length === 0 && emptyState}
        <div ref={bottomRef} />
      </div>

      {!atBottom && (
        <button
          type="button"
          aria-label="Scroll to bottom"
          onClick={scrollToBottom}
          style={{
            position: "absolute",
            bottom: 16,
            left: "50%",
            transform: "translateX(-50%)",
            height: 28,
            padding: "0 12px",
            borderRadius: 14,
            background: "var(--bg3)",
            border: "1px solid var(--border)",
            cursor: "pointer",
            display: "flex",
            alignItems: "center",
            gap: 5,
            color: "var(--t1)",
            fontSize: 12,
            boxShadow: "var(--shadow-popover)",
            zIndex: 10,
            whiteSpace: "nowrap",
          }}
        >
          <Icon d={Icons.chevD} size={12} /> Scroll to bottom
        </button>
      )}
    </div>
  );
}

export function buildTranscriptItems(
  messages: VisibleChatMessage[],
  segments: ChatSegmentRecord[] | undefined,
  showSegments: boolean,
): TranscriptItem[] {
  if (!showSegments) {
    return messages.map((message) => ({ type: "message", key: `message:${message.id}`, message }));
  }
  const segmentsByID = new Map((segments ?? []).map((segment) => [segment.id, segment]));
  const items: TranscriptItem[] = [];
  let previousSegmentID = "";
  messages.forEach((message, index) => {
    const segmentID = message.segment_id || fallbackSegmentID(message);
    if (segmentID && segmentID !== previousSegmentID) {
      items.push({
        type: "segment",
        key: `segment:${segmentID}:${index}`,
        segment: segmentsByID.get(segmentID) ?? segmentFromMessage(message, segmentID),
      });
      previousSegmentID = segmentID;
    }
    items.push({ type: "message", key: `message:${message.id}`, message });
  });
  return items;
}

function fallbackSegmentID(message: VisibleChatMessage): string {
  if (message.task_id) return `task:${message.task_id}`;
  if (message.native_session_id) return `external:${message.native_session_id}`;
  return "";
}

function segmentFromMessage(message: VisibleChatMessage, segmentID: string): ChatSegmentRecord {
  return {
    id: segmentID,
    runtime_kind: message.runtime_kind || "model",
    provider: message.provider,
    model: message.model,
    task_id: message.task_id,
    latest_run_id: message.run_id,
    status: message.agent_status,
    message_count: 0,
    started_at: message.created_at,
    updated_at: message.created_at,
  };
}

function ChatSegmentDivider({ segment }: { segment: ChatSegmentRecord }) {
  const description = describeChatSegment(segment);
  return (
    <div
      aria-label={description.label}
      style={{
        maxWidth: 820,
        margin: "10px auto 14px",
        padding: "0 16px",
        display: "flex",
        alignItems: "center",
        gap: 10,
      }}
    >
      <div
        style={{
          height: 1,
          flex: 1,
          background: "linear-gradient(90deg, transparent, var(--border))",
        }}
      />
      <div
        style={{
          display: "inline-flex",
          alignItems: "center",
          gap: 8,
          minWidth: 0,
          maxWidth: "100%",
          border: "1px solid var(--border)",
          background: "rgba(12, 18, 22, 0.78)",
          borderRadius: 999,
          padding: "5px 10px",
          boxShadow: "0 0 0 1px rgba(255,255,255,0.02)",
        }}
      >
        <span
          style={{
            color: description.tone === "on" ? "var(--teal)" : "var(--t2)",
            fontFamily: "var(--font-mono)",
            fontSize: 10,
            fontWeight: 700,
            textTransform: "uppercase",
            letterSpacing: "0.08em",
            whiteSpace: "nowrap",
          }}
        >
          {description.kicker}
        </span>
        <span
          style={{
            color: "var(--t1)",
            fontSize: 12,
            whiteSpace: "nowrap",
            overflow: "hidden",
            textOverflow: "ellipsis",
          }}
        >
          {description.title}
        </span>
        {description.meta && (
          <span
            style={{
              color: "var(--t3)",
              fontFamily: "var(--font-mono)",
              fontSize: 10,
              whiteSpace: "nowrap",
              overflow: "hidden",
              textOverflow: "ellipsis",
            }}
          >
            {description.meta}
          </span>
        )}
      </div>
      <div
        style={{
          height: 1,
          flex: 1,
          background: "linear-gradient(90deg, var(--border), transparent)",
        }}
      />
    </div>
  );
}

function describeChatSegment(segment: ChatSegmentRecord): {
  kicker: string;
  title: string;
  meta: string;
  label: string;
  tone: "on" | "off" | "external";
} {
  const model = segment.model || "selected model";
  const provider = segment.provider && segment.provider !== "auto" ? segment.provider : "";
  switch (segment.runtime_kind) {
    case "agent": {
      const meta = [
        provider,
        segment.task_id ? formatTaskLinkLabel(segment.task_id) : "",
        segment.status,
      ]
        .filter(Boolean)
        .join(" · ");
      return {
        kicker: "Tools on",
        title: model,
        meta,
        label: `Tools on segment using ${model}`,
        tone: "on",
      };
    }
    case "external_agent": {
      const meta = [segment.status, segment.workspace ? "workspace" : ""]
        .filter(Boolean)
        .join(" · ");
      return {
        kicker: "External",
        title: model === "selected model" ? "External Agent" : model,
        meta,
        label: "External Agent segment",
        tone: "external",
      };
    }
    default: {
      const meta = [provider, "direct model chat"].filter(Boolean).join(" · ");
      return {
        kicker: "Tools off",
        title: model,
        meta,
        label: `Tools off segment using ${model}`,
        tone: "off",
      };
    }
  }
}

function messageBrand(message: VisibleChatMessage, isHecateAgentChat: boolean): string | undefined {
  if (message.agent_adapter_id) return message.agent_adapter_id;
  if (message.agent_adapter_name) return message.agent_adapter_name;
  if (isHecateAgentChat) return message.provider || message.model || "hecate";
  return message.provider || message.model;
}

function formatTaskLinkLabel(taskID: string): string {
  return `Task ${compactID(taskID, ["task_"], 12)}`;
}

function formatTaskLinkTitle(taskID: string, runID?: string): string {
  return [`Open backing task ${taskID}`, runID ? `run ${runID}` : ""].filter(Boolean).join(" · ");
}

function formatTraceLinkLabel(requestID: string): string {
  return `Trace ${requestID.slice(0, 8)}`;
}

function formatTraceLinkTitle(requestID: string, traceID?: string): string {
  return [`Open trace for request ${requestID}`, traceID ? `trace ${traceID}` : ""]
    .filter(Boolean)
    .join(" · ");
}

function formatAgentRuntimeMeta(
  runID?: string,
  durationMS?: number,
  nativeSessionID?: string,
): string {
  const parts: string[] = [];
  if (nativeSessionID) {
    parts.push(`ACP ${nativeSessionID.slice(0, 12)}`);
  }
  if (runID) {
    parts.push(`Run ${compactID(runID, ["run_"], 12)}`);
  }
  if (durationMS && durationMS > 0) {
    parts.push(formatDurationMs(durationMS));
  }
  return parts.join(" · ");
}
