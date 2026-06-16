import { memo, useEffect, useRef, useState, type ReactNode } from "react";

import { useChat } from "../../app/state/chat";
import { useChatActions } from "../../app/state/coordinators/chat";
import { useChatTarget } from "../../app/state/derived";
import { useProjects } from "../../app/state/projects";
import { useWiredSettingsActions } from "../../app/state/coordinators/wired";
import { getTaskRunArtifact } from "../../lib/api";
import { formatDurationMs } from "../../lib/format";
import { writeProjectAssistantChatHandoff } from "../../lib/project-assistant-chat-handoff";
import type {
  ChatActivityRecord,
  ChatContextPacketRecord,
  ChatMessageRecord,
  ChatSegmentRecord,
  ChatTimingRecord,
  ChatUsageRecord,
} from "../../types/chat";
import type { ProjectAssistantProposal } from "../../types/project";
import { CodeBlock, Icon, Icons } from "../shared/ui";
import { TranscriptMessageRow } from "../transcript/TranscriptMessageRow";

import { compactID } from "./ChatComposer";
import {
  compactWorkspaceChangeLabel,
  workspaceChangeSummaryLabel,
} from "./ChatWorkspaceChangesPanel";
import { toChatMessageViewModel, toChatSegmentViewModel } from "./chatTurnViewModels";

export type VisibleChatMessage = {
  id: string;
  turn_kind?: string;
  execution_mode?: string;
  tools_enabled?: boolean;
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
  agent_id?: string;
  agent_name?: string;
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
  context_packet?: ChatContextPacketRecord;
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
  onNavigate?: (workspace: "connections" | "runs" | "overview" | "settings" | "projects") => void;
  onOpenTask?: (taskID: string, runID?: string) => void;
  onOpenTrace?: (requestID: string) => void;
  onOpenWorkspaceChanges?: () => void;
  openExternalAgentSetup: (adapterID?: string) => void;
};

const noop = () => {};

// useStableCallback returns a callback whose identity is constant for the
// component's lifetime but that always invokes the most recent function
// passed in. This lets the memoized transcript rows treat handlers as
// stable props even though ChatView recreates some of them every render.
function useStableCallback<A extends unknown[], R>(fn: (...args: A) => R): (...args: A) => R {
  const ref = useRef(fn);
  useEffect(() => {
    ref.current = fn;
  });
  return useRef((...args: A) => ref.current(...args)).current;
}

type ProjectAssistantProposalArtifactPayload = {
  project_id: string;
  source_session_id?: string;
  request?: string;
  proposal: ProjectAssistantProposal;
};

function parseProjectAssistantProposalArtifact(
  content: string,
): ProjectAssistantProposalArtifactPayload | null {
  try {
    const parsed = JSON.parse(content) as {
      project_id?: unknown;
      source_chat_session_id?: unknown;
      request?: unknown;
      proposal?: unknown;
    };
    const projectID = typeof parsed.project_id === "string" ? parsed.project_id.trim() : "";
    if (!projectID || !isProjectAssistantProposal(parsed.proposal)) return null;
    return {
      project_id: projectID,
      source_session_id:
        typeof parsed.source_chat_session_id === "string"
          ? parsed.source_chat_session_id.trim()
          : undefined,
      request: typeof parsed.request === "string" ? parsed.request : undefined,
      proposal: parsed.proposal,
    };
  } catch {
    return null;
  }
}

function isProjectAssistantProposal(value: unknown): value is ProjectAssistantProposal {
  if (!value || typeof value !== "object") return false;
  const proposal = value as Partial<ProjectAssistantProposal>;
  return (
    typeof proposal.id === "string" &&
    typeof proposal.title === "string" &&
    Array.isArray(proposal.actions)
  );
}

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
  onOpenWorkspaceChanges,
  openExternalAgentSetup,
}: Props) {
  const chat = useChat();
  const projects = useProjects();
  const chatTarget = useChatTarget();
  const { actions: settingsActions } = useWiredSettingsActions();
  const chatActions = useChatActions({
    chatTarget,
    setNoticeMessage: settingsActions.setNoticeMessage,
  });
  const streamingContent = chat.state.streamingContent;
  const activeChatSession = chat.state.activeChatSession;
  const pendingToolCalls = chat.state.pendingToolCalls;
  const chatLoading = chat.state.chatLoading;
  const scrollRef = useRef<HTMLDivElement>(null);
  const bottomRef = useRef<HTMLDivElement>(null);
  const userScrolledRef = useRef(false);
  const [atBottom, setAtBottom] = useState(true);
  const [copiedMsgId, setCopiedMsgId] = useState<string | null>(null);
  const copiedMsgTimerRef = useRef<number | null>(null);

  useEffect(() => {
    return () => {
      if (copiedMsgTimerRef.current !== null) {
        window.clearTimeout(copiedMsgTimerRef.current);
      }
    };
  }, []);

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
    if (copiedMsgTimerRef.current !== null) {
      window.clearTimeout(copiedMsgTimerRef.current);
    }
    setCopiedMsgId(id);
    copiedMsgTimerRef.current = window.setTimeout(() => {
      setCopiedMsgId(null);
      copiedMsgTimerRef.current = null;
    }, 2000);
  }

  // Stable handler identities so the memoized rows below can bail out of
  // re-rendering. onOpenTask/onOpenTrace fold in the onNavigate fallback so
  // the row never needs onNavigate directly.
  const handleCopy = useStableCallback(copyMsg);
  const handleOpenTask = useStableCallback((taskID: string, runID?: string) => {
    if (onOpenTask) onOpenTask(taskID, runID);
    else onNavigate?.("runs");
  });
  const handleOpenTrace = useStableCallback((requestID: string) => {
    if (onOpenTrace) onOpenTrace(requestID);
    else onNavigate?.("overview");
  });
  const handleOpenSetup = useStableCallback((adapterID?: string) => {
    openExternalAgentSetup(adapterID);
  });
  const handleOpenProjectProposal = useStableCallback(
    async (message: VisibleChatMessage, activity: ChatActivityRecord) => {
      const taskID = (message.task_id ?? "").trim();
      const runID = (message.run_id ?? "").trim();
      const artifactID = (activity.artifact_id ?? "").trim();
      if (!taskID || !runID || !artifactID) {
        settingsActions.setNoticeMessage(
          "error",
          "Project Assistant proposal artifact is missing.",
        );
        return;
      }
      try {
        const artifact = await getTaskRunArtifact(taskID, runID, artifactID);
        const payload = parseProjectAssistantProposalArtifact(artifact.data.content_text ?? "");
        if (!payload) {
          settingsActions.setNoticeMessage(
            "error",
            "Project Assistant proposal artifact could not be opened.",
          );
          return;
        }
        const projectID = payload.project_id.trim();
        const handoffWritten = writeProjectAssistantChatHandoff({
          project_id: projectID,
          proposal: payload.proposal,
          request: payload.request,
          source_session_id: payload.source_session_id || activeSessionID,
          created_at: new Date().toISOString(),
        });
        if (!handoffWritten) {
          settingsActions.setNoticeMessage(
            "error",
            "Failed to hand off the proposal to Projects. Try opening it again.",
          );
          return;
        }
        void projects.actions.selectProject(projectID);
        onNavigate?.("projects");
        settingsActions.setNoticeMessage(
          "success",
          "Project Assistant proposal loaded. Review it in Projects.",
        );
      } catch (error) {
        settingsActions.setNoticeMessage(
          "error",
          error instanceof Error ? error.message : "Failed to open Project Assistant proposal.",
        );
      }
    },
  );
  const stableWorkspaceChanges = useStableCallback(onOpenWorkspaceChanges ?? noop);
  // Preserve the undefined case: TranscriptMessageRow renders a plain,
  // non-clickable label when changedFilesLink.onClick is undefined, so a
  // missing handler must stay undefined rather than become a no-op button.
  const workspaceChangesHandler = onOpenWorkspaceChanges ? stableWorkspaceChanges : undefined;

  return (
    <div style={{ flex: 1, overflow: "hidden", position: "relative" }}>
      <div
        ref={scrollRef}
        onScroll={handleScroll}
        style={{ height: "100%", overflowY: "auto", padding: "16px 0" }}
      >
        {(() => {
          let latestUserPrompt = "";
          return transcriptItems.map((item) => {
            if (item.type === "segment") {
              return <ChatSegmentDivider key={item.key} segment={item.segment} />;
            }
            const m = item.message;
            const role = m.role === "assistant" ? "assistant" : "user";
            const turnPrompt = role === "assistant" ? latestUserPrompt : undefined;
            if (role === "user") {
              latestUserPrompt = visibleMessageContent(m);
            }
            return (
              <ChatTranscriptRow
                key={item.key}
                message={m}
                isHecateAgentChat={isHecateAgentChat}
                activeModel={activeChatSession?.model}
                copied={copiedMsgId === m.id}
                copiedDebug={copiedMsgId === `${m.id}:debug`}
                turnPrompt={turnPrompt}
                onCopy={handleCopy}
                onOpenTask={handleOpenTask}
                onOpenTrace={handleOpenTrace}
                onOpenProjectProposal={handleOpenProjectProposal}
                onOpenWorkspaceChanges={workspaceChangesHandler}
                openExternalAgentSetup={handleOpenSetup}
              />
            );
          });
        })()}

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

type ChatTranscriptRowProps = {
  message: VisibleChatMessage;
  isHecateAgentChat: boolean;
  activeModel?: string;
  copied: boolean;
  copiedDebug: boolean;
  turnPrompt?: string;
  onCopy: (id: string, text: string) => void;
  onOpenTask: (taskID: string, runID?: string) => void;
  onOpenTrace: (requestID: string) => void;
  onOpenProjectProposal: (message: VisibleChatMessage, activity: ChatActivityRecord) => void;
  onOpenWorkspaceChanges?: () => void;
  openExternalAgentSetup: (adapterID?: string) => void;
};

// ChatTranscriptRow is memoized so a streamed snapshot only re-renders the
// one message whose projected record changed identity (see
// projectVisibleMessage + reconcileChatSession). Every callback prop is
// stabilized by the parent, so the shallow prop compare bails for each
// unchanged row — skipping its markdown re-parse and subtree render.
const ChatTranscriptRow = memo(function ChatTranscriptRow({
  message: m,
  isHecateAgentChat,
  activeModel,
  copied,
  copiedDebug,
  turnPrompt,
  onCopy,
  onOpenTask,
  onOpenTrace,
  onOpenProjectProposal,
  onOpenWorkspaceChanges,
  openExternalAgentSetup,
}: ChatTranscriptRowProps) {
  const role = m.role === "assistant" ? "assistant" : "user";
  const content = visibleMessageContent(m);
  const time = m.created_at
    ? new Date(m.created_at).toLocaleTimeString("en-US", {
        hour: "2-digit",
        minute: "2-digit",
      })
    : "";
  const agentModel = isHecateAgentChat
    ? m.model || activeModel || "Hecate Chat"
    : m.agent_name || m.agent_id;
  const agentRuntime = role === "assistant" ? formatAgentRuntimeMeta(m.run_id, m.duration_ms) : "";
  const agentRuntimeTitle =
    role === "assistant"
      ? formatAgentRuntimeMetaTitle(m.run_id, m.duration_ms, m.native_session_id)
      : "";
  const turn = toChatMessageViewModel(m);
  const taskID = turn.isTaskBacked ? turn.taskID : "";
  const taskRunID = taskID ? m.run_id : "";
  const traceRequestID = m.request_id;
  const traceID = m.trace_id;
  const changedFilesSummary =
    role === "assistant" && (m.diff_stat || m.diff)
      ? workspaceChangeSummaryLabel({
          key: `workspace-files:${m.id}`,
          messageID: m.id,
          label: "",
          diffStat: m.diff_stat,
          diff: m.diff,
        })
      : "";
  return (
    <TranscriptMessageRow
      id={m.id}
      role={role}
      model={agentModel}
      brand={messageBrand(m, isHecateAgentChat)}
      content={content}
      diffStat={role === "assistant" ? m.diff_stat : undefined}
      diff={role === "assistant" ? m.diff : undefined}
      time={time}
      badge={role === "assistant" ? m.agent_status || m.cost_mode : undefined}
      runtimeMeta={agentRuntime}
      runtimeMetaTitle={agentRuntimeTitle}
      taskLink={
        isHecateAgentChat && role === "assistant" && taskID
          ? {
              label: formatTaskLinkLabel(taskID),
              title: formatTaskLinkTitle(taskID, taskRunID),
              onClick: () => onOpenTask(taskID, taskRunID),
            }
          : undefined
      }
      traceLink={
        role === "assistant" && traceRequestID
          ? {
              label: formatTraceLinkLabel(traceRequestID),
              title: formatTraceLinkTitle(traceRequestID, traceID),
              onClick: () => onOpenTrace(traceRequestID),
            }
          : undefined
      }
      changedFilesLink={
        changedFilesSummary
          ? {
              label: compactWorkspaceChangeLabel(m.diff_stat),
              title: changedFilesSummary,
              onClick: onOpenWorkspaceChanges,
            }
          : undefined
      }
      activities={role === "assistant" ? m.activities : undefined}
      onOpenProjectProposal={
        role === "assistant" ? (activity) => onOpenProjectProposal(m, activity) : undefined
      }
      rawOutput={role === "assistant" ? m.raw_output : undefined}
      agentUsage={role === "assistant" ? m.usage : undefined}
      agentTiming={role === "assistant" ? m.timing : undefined}
      contextPacket={role === "assistant" ? m.context_packet : undefined}
      error={role === "assistant" ? m.error : undefined}
      setupAction={externalAgentSetupAction(role, m, openExternalAgentSetup)}
      onCopy={onCopy}
      copied={copied}
      copiedDebug={copiedDebug}
      turnPrompt={turnPrompt}
    />
  );
});

function visibleMessageContent(message: VisibleChatMessage): string {
  if (typeof message.content === "string") return message.content;
  if (message.content === null) return "";
  return JSON.stringify(message.content);
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

const visibleMessageCache = new WeakMap<ChatMessageRecord, VisibleChatMessage>();

// projectVisibleMessage maps a persisted chat message onto the lean shape
// the transcript renders. It is cached by message-object identity so that,
// combined with reconcileChatSession preserving identity across snapshots,
// an unchanged message yields the *same* VisibleChatMessage reference on
// every render — which is what lets ChatTranscriptRow's memo bail out.
// Id-less rows (optimistic/synthetic) fall back to an index-derived id and
// also get a fresh object every snapshot, so they are never cached.
export function projectVisibleMessage(
  message: ChatMessageRecord,
  index: number,
): VisibleChatMessage {
  if (!message.id) {
    return buildVisibleMessage(message, `agent-message-${index}`);
  }
  const cached = visibleMessageCache.get(message);
  if (cached) return cached;
  const projected = buildVisibleMessage(message, message.id);
  visibleMessageCache.set(message, projected);
  return projected;
}

function buildVisibleMessage(m: ChatMessageRecord, id: string): VisibleChatMessage {
  return {
    id,
    turn_kind: m.turn_kind,
    execution_mode: m.execution_mode,
    tools_enabled: m.tools_enabled,
    segment_id: m.segment_id,
    task_id: m.task_id,
    run_id: m.run_id,
    request_id: m.request_id,
    trace_id: m.trace_id,
    native_session_id: m.native_session_id,
    role: m.role,
    content: m.content,
    created_at: m.created_at,
    agent_id: m.agent_id,
    agent_name: m.agent_name,
    agent_status: m.status,
    cost_mode: m.cost_mode,
    provider: m.provider,
    model: m.model,
    diff_stat: m.diff_stat,
    diff: m.diff,
    raw_output: m.raw_output,
    activities: m.activities,
    usage: m.usage,
    duration_ms: m.duration_ms,
    error: m.error,
    timing: m.timing,
    context_packet: m.context_packet,
  };
}

function fallbackSegmentID(message: VisibleChatMessage): string {
  if (message.task_id) return `task:${message.task_id}`;
  if (message.native_session_id) return `external:${message.native_session_id}`;
  return "";
}

function segmentFromMessage(message: VisibleChatMessage, segmentID: string): ChatSegmentRecord {
  return {
    id: segmentID,
    turn_kind: message.turn_kind,
    execution_mode: message.execution_mode || "hecate_task",
    tools_enabled: message.tools_enabled,
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
          background: "var(--bg2)",
          borderRadius: 999,
          padding: "5px 10px",
          boxShadow: "var(--shadow-popover)",
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
  const turn = toChatSegmentViewModel(segment);
  switch (turn.turnKind) {
    case "direct_model": {
      const meta = [provider, "direct model chat"].filter(Boolean).join(" · ");
      return {
        kicker: "Tools off",
        title: model,
        meta,
        label: `Tools off segment using ${model}`,
        tone: "off",
      };
    }
    case "hecate_task": {
      const meta = [provider, turn.taskID ? formatTaskLinkLabel(turn.taskID) : "", turn.status]
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
      const meta = [turn.status, turn.workspace ? "workspace" : ""].filter(Boolean).join(" · ");
      return {
        kicker: "External",
        title: model === "selected model" ? "External Agent" : model,
        meta,
        label: "External Agent segment",
        tone: "external",
      };
    }
    default: {
      const meta = [provider, segment.status].filter(Boolean).join(" · ");
      return {
        kicker: "Chat",
        title: model,
        meta,
        label: `Chat segment using ${model}`,
        tone: "external",
      };
    }
  }
}

function messageBrand(message: VisibleChatMessage, isHecateAgentChat: boolean): string | undefined {
  if (message.agent_id) return message.agent_id;
  if (message.agent_name) return message.agent_name;
  if (isHecateAgentChat) return message.provider || message.model || "hecate";
  return message.provider || message.model;
}

function externalAgentSetupAction(
  role: string,
  message: VisibleChatMessage,
  openExternalAgentSetup: (adapterID?: string) => void,
): { label: string; title: string; onClick: () => void } | undefined {
  if (role !== "assistant" || !message.agent_id || message.agent_id === "hecate") return undefined;
  const error = typeof message.error === "string" ? message.error : "";
  if (!isExternalAgentSetupError(error)) return undefined;
  const label = message.agent_name || externalAgentDisplayName(message.agent_id);
  return {
    label: `Open ${label} setup`,
    title: `Open Connections and scroll to ${label} setup`,
    onClick: () => openExternalAgentSetup(message.agent_id),
  };
}

function isExternalAgentSetupError(error: string): boolean {
  const normalized = error.toLowerCase();
  return (
    normalized.includes("auth_required") ||
    normalized.includes("authentication required") ||
    normalized.includes("unauthenticated") ||
    normalized.includes("not authenticated") ||
    normalized.includes("not signed in") ||
    normalized.includes("not logged in") ||
    normalized.includes("login required") ||
    normalized.includes("command not found") ||
    normalized.includes("not installed")
  );
}

function externalAgentDisplayName(agentID: string): string {
  switch (agentID) {
    case "claude_code":
      return "Claude Code";
    case "codex":
      return "Codex";
    case "cursor_agent":
      return "Cursor";
    case "grok_build":
      return "Grok Build";
    default:
      return "agent";
  }
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

function formatAgentRuntimeMeta(runID?: string, durationMS?: number): string {
  const parts: string[] = [];
  if (runID) {
    parts.push(`Run ${compactID(runID, ["run_"], 12)}`);
  }
  if (durationMS && durationMS > 0) {
    parts.push(formatDurationMs(durationMS));
  }
  return parts.join(" · ");
}

function formatAgentRuntimeMetaTitle(
  runID?: string,
  durationMS?: number,
  nativeSessionID?: string,
): string {
  const parts = [
    runID ? `Run ${runID}` : "",
    nativeSessionID ? `Native session ${nativeSessionID}` : "",
    durationMS && durationMS > 0 ? `Duration ${formatDurationMs(durationMS)}` : "",
  ].filter(Boolean);
  return parts.join(" · ");
}
