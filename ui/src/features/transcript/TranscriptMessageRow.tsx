import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type MouseEvent as ReactMouseEvent,
  type ReactNode,
} from "react";

import type {
  ChatActivityRecord,
  ChatContextPacketRecord,
  ChatMCPAppRecord,
  ChatTimingRecord,
  ChatUsageRecord,
} from "../../types/chat";
import { formatDurationMs } from "../../lib/format";
import { CodeBlock } from "../shared/Atoms";
import { BrandAvatar } from "../shared/BrandAvatar";
import { ContextInspectorDetails, contextPacketEmpty } from "../shared/ContextInspector";
import { DiffViewer } from "../shared/DiffViewer";
import { Icon, Icons } from "../shared/Icons";
import { DiffStatList, TranscriptActivityTimeline } from "./TranscriptActivityTimeline";
import { TranscriptMarkdown } from "./TranscriptMarkdown";
import { readAloudStatusIsBlocked } from "./readAloudEligibility";
import {
  activityDisplay,
  activityEffectiveStatus,
  capturedToolOutput,
} from "./transcriptActivityHelpers";

const ANSI_ESCAPE_PATTERN = new RegExp(`${String.fromCharCode(27)}\\[[0-9;]*m`, "g");
const VISUALLY_HIDDEN_STYLE = {
  border: 0,
  clip: "rect(0 0 0 0)",
  height: 1,
  margin: -1,
  overflow: "hidden",
  padding: 0,
  position: "absolute" as const,
  whiteSpace: "nowrap" as const,
  width: 1,
};

export function TranscriptMessageRow({
  id,
  role,
  model,
  brand,
  content,
  contentPrefix,
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
  readAloud,
  turnPrompt,
  copiedDebug,
  onOpenProjectProposal,
}: {
  id: string;
  role: "user" | "assistant";
  model?: string;
  brand?: string;
  content: string;
  contentPrefix?: ReactNode;
  diffStat?: string;
  diff?: string;
  time: string;
  promptTokens?: number;
  completionTokens?: number;
  costUsd?: string;
  badge?: string;
  runtimeMeta?: string;
  runtimeMetaTitle?: string;
  taskLink?: {
    label: string;
    title?: string;
    href: string;
    onClick?: (event: ReactMouseEvent<HTMLAnchorElement>) => void;
  };
  traceLink?: { label: string; title?: string; onClick: () => void };
  changedFilesLink?: { label: string; title?: string; onClick?: () => void };
  activities?: ChatActivityRecord[];
  rawOutput?: string;
  agentUsage?: ChatUsageRecord;
  agentTiming?: ChatTimingRecord;
  contextPacket?: ChatContextPacketRecord;
  error?: string;
  // setupAction is an inline button rendered inside the agent-turn
  // failure notice. The chat passes it when the failure has a
  // known one-click recovery path — currently just the Claude Code
  // auth error (where the button deep-links to the guided setup
  // card in Connections). Optional in all other
  // cases.
  setupAction?: { label: string; title?: string; onClick: () => void };
  onCopy: (id: string, text: string) => void;
  copied: boolean;
  readAloud?: {
    active: boolean;
    disabledReason?: string;
    onToggle: (id: string, content: string) => void;
  };
  turnPrompt?: string;
  copiedDebug?: boolean;
  onOpenProjectProposal?: (activity: ChatActivityRecord) => void;
}) {
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
          if (failed && duplicatesFailureNotice(activity, error || content, Boolean(taskLink))) {
            return false;
          }
          if (
            isFilesChangedActivity(activity) &&
            (changedFilesLink || diffStat?.trim() || diff?.trim())
          ) {
            return false;
          }
          return true;
        })
      : activities;
  const inlineMCPAppActivities =
    isAssistant && visibleActivities?.length ? visibleActivities.filter(hasMCPApp) : [];
  const inlineMCPAppActivitySet = new Set<ChatActivityRecord>(inlineMCPAppActivities);
  const renderActivityAdvanced =
    isAssistant && visibleActivities?.length
      ? (activity: ChatActivityRecord) => {
          if (inlineMCPAppActivitySet.has(activity)) return null;
          if (isProjectAssistantProposalActivity(activity) && onOpenProjectProposal) {
            return (
              <ProjectAssistantProposalActivity
                activity={activity}
                onOpen={() => onOpenProjectProposal(activity)}
              />
            );
          }
          return renderAgentActivityAdvanced(activity, {
            taskLink,
            diffStat,
            diff,
          });
        }
      : undefined;
  const showCapturedDiff =
    isAssistant &&
    Boolean(diffStat?.trim() || diff?.trim()) &&
    !changedFilesLink &&
    !(visibleActivities ?? []).some(isFilesChangedActivity);
  const showReadAloud =
    isAssistant &&
    Boolean(content.trim()) &&
    Boolean(readAloud) &&
    !readAloudStatusIsBlocked(badge);
  const readAloudUnavailable = Boolean(readAloud?.disabledReason) && !readAloud?.active;
  const readAloudDescriptionID = `${id}-read-aloud-description`;

  return (
    <div
      id={id}
      className="cross-surface-focus-target transcript-message-row"
      tabIndex={-1}
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
              <HeaderMetaLink
                label={taskLink.label}
                title={taskLink.title}
                href={taskLink.href}
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
              className="transcript-message-actions"
              data-active={readAloud?.active ? "true" : undefined}
              style={{
                marginLeft: "auto",
                display: "flex",
                gap: 4,
              }}
            >
              {isAssistant && (
                <button
                  aria-label="Copy turn debug bundle"
                  className="btn btn-ghost btn-sm"
                  title="Copy prompt, response, activity summary, trace, and context packet"
                  style={{ padding: "2px 6px", gap: 4 }}
                  onClick={() =>
                    onCopy(
                      `${id}:debug`,
                      buildTurnDebugBundle({
                        id,
                        model,
                        badge,
                        content,
                        turnPrompt,
                        traceLabel: traceLink?.label,
                        traceTitle: traceLink?.title,
                        taskLabel: taskLink?.label,
                        runtimeMeta,
                        runtimeMetaTitle,
                        diffStat,
                        activities: visibleActivities,
                        contextPacket,
                        error,
                      }),
                    )
                  }
                  type="button"
                >
                  <Icon d={copiedDebug ? Icons.check : Icons.copy} size={12} />
                  <span style={{ fontSize: 10 }}>debug</span>
                </button>
              )}
              {showReadAloud && readAloud && (
                <button
                  aria-label="Read response aloud"
                  aria-pressed={readAloudUnavailable ? undefined : readAloud.active}
                  aria-describedby={readAloudUnavailable ? readAloudDescriptionID : undefined}
                  className="btn btn-ghost btn-sm transcript-message-read-aloud"
                  onClick={() => readAloud.onToggle(id, content)}
                  title={
                    readAloud.active
                      ? "Stop reading this response"
                      : readAloud.disabledReason || "Read this response with a local system voice"
                  }
                  type="button"
                  style={{ padding: "2px 6px", gap: 4 }}
                >
                  <Icon d={readAloud.active ? Icons.stop : Icons.volume} size={12} />
                </button>
              )}
              {showReadAloud && readAloudUnavailable && readAloud?.disabledReason && (
                <span id={readAloudDescriptionID} style={VISUALLY_HIDDEN_STYLE}>
                  {readAloud.disabledReason}
                </span>
              )}
              <button
                aria-label="Copy message"
                className="btn btn-ghost btn-sm"
                style={{ padding: "2px 6px", gap: 4 }}
                onClick={() => onCopy(id, content)}
                type="button"
              >
                <Icon d={copied ? Icons.check : Icons.copy} size={12} />
              </button>
            </div>
          </div>
          {contentPrefix}
          {failed ? (
            <>
              {shouldRenderFailedContent(content, error) ? (
                <TranscriptMarkdown content={content} />
              ) : null}
              <AgentTurnNotice status="failed" message={error || content} action={setupAction} />
            </>
          ) : cancelled ? (
            <>
              {/* Cancel messages may be normalized by the backend; preserve partial text as-is. */}
              {content.trim() ? <TranscriptMarkdown content={content} /> : null}
              <AgentTurnNotice
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
          {inlineMCPAppActivities.length > 0 && (
            <div
              style={{
                display: "grid",
                gap: 8,
                marginTop: content.trim() ? 8 : 0,
                minWidth: 0,
              }}
            >
              {inlineMCPAppActivities.map((activity, index) => (
                <MCPAppFrame
                  key={activity.id || activity.mcp_app.resource_uri || `mcp-app-${index}`}
                  activity={activity}
                  app={activity.mcp_app}
                />
              ))}
            </div>
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
            <ContextInspectorDetails packet={contextPacket} />
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

type ChatActivityWithMCPApp = ChatActivityRecord & { mcp_app: ChatMCPAppRecord };

function hasMCPApp(activity: ChatActivityRecord): activity is ChatActivityWithMCPApp {
  return Boolean(activity.mcp_app);
}

function isProjectAssistantProposalActivity(activity: ChatActivityRecord): boolean {
  return (
    activity.type === "project_assistant_proposal" || activity.kind === "project_assistant_proposal"
  );
}

function ProjectAssistantProposalActivity({
  activity,
  onOpen,
}: {
  activity: ChatActivityRecord;
  onOpen: () => void;
}) {
  return (
    <div style={{ display: "grid", gap: 7 }}>
      <div style={{ color: "var(--t2)", fontSize: 11, lineHeight: 1.5 }}>
        {activity.detail || activity.title || "Project Assistant proposal"}
      </div>
      <div>
        <button
          type="button"
          className="btn btn-ghost btn-sm"
          onClick={onOpen}
          title="Review Project Assistant proposal in Projects"
          style={{ fontSize: 10, padding: "2px 7px", gap: 5 }}
        >
          <Icon d={Icons.projects} size={12} />
          Review in Projects
        </button>
      </div>
    </div>
  );
}

function renderAgentActivityAdvanced(
  activity: ChatActivityRecord,
  options: {
    taskLink?: {
      label: string;
      title?: string;
      href: string;
      onClick?: (event: ReactMouseEvent<HTMLAnchorElement>) => void;
    };
    diffStat?: string;
    diff?: string;
  },
) {
  if (activity.mcp_app) {
    return <MCPAppFrame activity={activity} app={activity.mcp_app} />;
  }

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
        title={
          activity.type === "terminal"
            ? "Terminal output"
            : activity.kind === "read"
              ? "Read output"
              : "Tool output"
        }
        output={toolOutput}
      />
    );
  }

  if (activity.type !== "tool_call" || activity.status !== "failed") return null;
  if (!options.taskLink) return null;

  return (
    <div style={{ display: "grid", gap: 7 }}>
      <div style={{ color: "var(--t2)", fontSize: 11, lineHeight: 1.5 }}>
        This tool failed. Open the backing task for the full run output and approval history.
      </div>
      <div>
        <a
          className="btn btn-ghost btn-sm"
          href={options.taskLink.href}
          onClick={options.taskLink.onClick}
          title={`Open ${options.taskLink.label} output`}
          style={{ fontSize: 10, padding: "2px 7px", textDecoration: "none" }}
        >
          Open task output
        </a>
      </div>
    </div>
  );
}

function MCPAppFrame({ activity, app }: { activity: ChatActivityRecord; app: ChatMCPAppRecord }) {
  const iframeRef = useRef<HTMLIFrameElement | null>(null);
  const appRef = useRef(app);
  const [height, setHeight] = useState(360);
  const srcDoc = useMemo(
    () => htmlWithMCPAppCSP(app.html ?? "", app.resource_meta),
    [app.html, app.resource_meta],
  );
  const prefersBorder = mcpAppPrefersBorder(app.resource_meta);

  useEffect(() => {
    appRef.current = app;
  }, [app]);

  useEffect(() => {
    function postToApp(message: Record<string, unknown>) {
      iframeRef.current?.contentWindow?.postMessage(message, "*");
    }

    function respond(id: unknown, payload: { result?: unknown; error?: MCPAppRPCError }) {
      if (id === undefined || id === null) return;
      postToApp({
        jsonrpc: "2.0",
        id,
        ...(payload.error ? { error: payload.error } : { result: payload.result ?? {} }),
      });
    }

    function sendToolPayloads() {
      const current = appRef.current;
      postToApp({
        jsonrpc: "2.0",
        method: "ui/notifications/tool-input",
        params: { arguments: objectRecord(current.tool_input) },
      });
      postToApp({
        jsonrpc: "2.0",
        method: "ui/notifications/tool-result",
        params: current.tool_result ?? { content: [] },
      });
    }

    function handleMessage(event: MessageEvent) {
      if (!iframeRef.current || event.source !== iframeRef.current.contentWindow) return;
      if (!isMCPAppRPCMessage(event.data)) return;

      const message = event.data;
      if (message.method === "ui/notifications/initialized") {
        sendToolPayloads();
        return;
      }
      if (message.method === "notifications/message") {
        return;
      }
      if (message.method === "ui/notifications/size-changed") {
        const nextHeight = numericRecord(message.params).height;
        if (typeof nextHeight === "number" && Number.isFinite(nextHeight)) {
          setHeight(Math.max(180, Math.min(760, Math.ceil(nextHeight))));
        }
        return;
      }

      switch (message.method) {
        case "ui/initialize":
        case "initialize":
          respond(message.id, {
            result: {
              protocolVersion: "2026-01-26",
              hostCapabilities: {
                logging: {},
                serverResources: {},
                sandbox: { csp: cspMetadata(appRef.current.resource_meta) },
              },
              hostInfo: { name: "Hecate", version: "0.0.0" },
              hostContext: {
                toolInfo: {
                  tool: {
                    name: appRef.current.tool_name || activity.title,
                    _meta: appRef.current.tool_meta,
                  },
                },
                theme: document.documentElement.dataset.theme === "light" ? "light" : "dark",
                displayMode: "inline",
                availableDisplayModes: ["inline"],
                containerDimensions: { maxWidth: 760, maxHeight: 760 },
                locale: navigator.language,
                timeZone: Intl.DateTimeFormat().resolvedOptions().timeZone,
                userAgent: "hecate",
                platform: "desktop",
                deviceCapabilities: { hover: true },
              },
            },
          });
          return;
        case "ping":
          respond(message.id, { result: {} });
          return;
        case "resources/read":
          handleMCPAppResourceRead(message, appRef.current, respond);
          return;
        case "ui/request-display-mode":
          respond(message.id, { result: { mode: "inline" } });
          return;
        case "tools/call":
          respond(message.id, {
            error: {
              code: -32000,
              message:
                "Interactive MCP App tool calls are not available from persisted Hecate chat apps yet.",
            },
          });
          return;
        default:
          respond(message.id, {
            error: { code: -32601, message: `Unsupported MCP App method: ${message.method}` },
          });
      }
    }

    window.addEventListener("message", handleMessage);
    return () => window.removeEventListener("message", handleMessage);
  }, [activity.title]);

  if (app.error && !app.html) {
    return (
      <div style={{ color: "var(--red)", fontSize: 11, lineHeight: 1.5 }}>
        Could not render MCP App: {app.error}
      </div>
    );
  }
  if (!srcDoc) {
    return (
      <div style={{ color: "var(--t3)", fontSize: 11, lineHeight: 1.5 }}>
        This MCP tool advertised an app, but no HTML resource was captured.
      </div>
    );
  }

  return (
    <div style={{ display: "grid", gap: 5, minWidth: 0 }}>
      {app.error && (
        <div style={{ color: "var(--amber)", fontSize: 11, lineHeight: 1.5 }}>{app.error}</div>
      )}
      <iframe
        ref={iframeRef}
        data-testid="mcp-app-frame"
        srcDoc={srcDoc}
        title="MCP App"
        aria-label={app.tool_name || activity.title || "MCP App"}
        sandbox="allow-scripts"
        referrerPolicy="no-referrer"
        style={{
          background: "var(--bg0)",
          border: prefersBorder ? "1px solid var(--border)" : "none",
          borderRadius: prefersBorder ? "var(--radius-sm)" : 0,
          display: "block",
          height,
          maxHeight: 760,
          minHeight: 180,
          overflow: "hidden",
          width: "100%",
        }}
      />
      {app.html_truncated && (
        <div style={{ color: "var(--amber)", fontSize: 10, lineHeight: 1.5 }}>
          App HTML exceeded the capture limit, so the rendered view may be incomplete.
        </div>
      )}
    </div>
  );
}

type MCPAppRPCError = { code: number; message: string };

type MCPAppRPCMessage = {
  jsonrpc?: string;
  id?: unknown;
  method?: string;
  params?: unknown;
};

function isMCPAppRPCMessage(value: unknown): value is MCPAppRPCMessage {
  return Boolean(
    value &&
    typeof value === "object" &&
    "method" in value &&
    typeof (value as { method?: unknown }).method === "string",
  );
}

function handleMCPAppResourceRead(
  message: MCPAppRPCMessage,
  app: ChatMCPAppRecord,
  respond: (id: unknown, payload: { result?: unknown; error?: MCPAppRPCError }) => void,
) {
  const requestedURI = stringRecord(message.params).uri;
  const resourceURI = app.resource_uri ?? "";
  if (requestedURI && requestedURI !== resourceURI) {
    respond(message.id, {
      error: { code: -32602, message: `Unknown MCP App resource: ${requestedURI}` },
    });
    return;
  }
  respond(message.id, {
    result: {
      contents: [
        {
          uri: resourceURI,
          mimeType: app.mime_type || "text/html;profile=mcp-app",
          text: app.html ?? "",
          _meta: app.resource_meta,
        },
      ],
    },
  });
}

function htmlWithMCPAppCSP(html: string, meta: unknown): string {
  const trimmed = html.trim();
  if (!trimmed) return "";
  const csp = mcpAppCSP(meta);
  const tag = `<meta http-equiv="Content-Security-Policy" content="${escapeHTMLAttribute(csp)}">`;
  if (/<head(\s[^>]*)?>/i.test(trimmed)) {
    return trimmed.replace(/<head(\s[^>]*)?>/i, (match) => `${match}${tag}`);
  }
  if (/<html(\s[^>]*)?>/i.test(trimmed)) {
    return trimmed.replace(/<html(\s[^>]*)?>/i, (match) => `${match}<head>${tag}</head>`);
  }
  return `<!doctype html><html><head>${tag}</head><body>${trimmed}</body></html>`;
}

function mcpAppCSP(meta: unknown): string {
  const csp = cspMetadata(meta);
  const resourceDomains = csp.resourceDomains.length > 0 ? csp.resourceDomains.join(" ") : "'self'";
  const connectDomains = csp.connectDomains.length > 0 ? csp.connectDomains.join(" ") : "'none'";
  const frameDomains = csp.frameDomains.length > 0 ? csp.frameDomains.join(" ") : "'none'";
  const baseUriDomains = csp.baseUriDomains.length > 0 ? csp.baseUriDomains.join(" ") : "'self'";
  return [
    "default-src 'none'",
    `script-src 'self' 'unsafe-inline' ${resourceDomains}`,
    `style-src 'self' 'unsafe-inline' ${resourceDomains}`,
    `img-src 'self' data: ${resourceDomains}`,
    `font-src 'self' data: ${resourceDomains}`,
    `media-src 'self' data: ${resourceDomains}`,
    `connect-src ${connectDomains}`,
    `frame-src ${frameDomains}`,
    `base-uri ${baseUriDomains}`,
    "object-src 'none'",
  ].join("; ");
}

function cspMetadata(meta: unknown): {
  connectDomains: string[];
  resourceDomains: string[];
  frameDomains: string[];
  baseUriDomains: string[];
} {
  const ui = objectRecord(objectRecord(meta).ui);
  const csp = objectRecord(ui.csp);
  return {
    connectDomains: cspSourceArray(csp.connectDomains, "connect"),
    resourceDomains: cspSourceArray(csp.resourceDomains, "resource"),
    frameDomains: cspSourceArray(csp.frameDomains, "frame"),
    baseUriDomains: cspSourceArray(csp.baseUriDomains, "base"),
  };
}

function mcpAppPrefersBorder(meta: unknown): boolean {
  const ui = objectRecord(objectRecord(meta).ui);
  return typeof ui.prefersBorder === "boolean" ? ui.prefersBorder : true;
}

function objectRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

function numericRecord(value: unknown): Record<string, number> {
  return objectRecord(value) as Record<string, number>;
}

function stringRecord(value: unknown): Record<string, string> {
  return objectRecord(value) as Record<string, string>;
}

type CSPSourceKind = "resource" | "connect" | "frame" | "base";

function cspSourceArray(value: unknown, kind: CSPSourceKind): string[] {
  if (!Array.isArray(value)) return [];
  const out: string[] = [];
  const seen = new Set<string>();
  for (const item of value) {
    const source = cspSource(item, kind);
    if (!source || seen.has(source)) continue;
    seen.add(source);
    out.push(source);
  }
  return out;
}

function cspSource(value: unknown, kind: CSPSourceKind): string {
  if (typeof value !== "string") return "";
  const trimmed = value.trim();
  if (trimmed === "" || !/^[^\s;]+$/.test(trimmed)) return "";
  let parsed: URL;
  try {
    parsed = new URL(trimmed);
  } catch {
    return "";
  }
  if (parsed.protocol === "https:") return parsed.origin;
  if (kind === "connect" && parsed.protocol === "wss:") return parsed.origin;
  if (kind === "connect" && parsed.protocol === "ws:" && isLoopbackHost(parsed.hostname)) {
    return parsed.origin;
  }
  if (parsed.protocol === "http:" && isLoopbackHost(parsed.hostname)) {
    return parsed.origin;
  }
  return "";
}

function isLoopbackHost(hostname: string): boolean {
  const normalized = hostname.toLowerCase().replace(/^\[|\]$/g, "");
  if (normalized === "localhost" || normalized === "::1") return true;
  const parts = normalized.split(".");
  if (parts.length !== 4 || parts[0] !== "127") return false;
  return parts.every((part) => /^\d{1,3}$/.test(part) && Number(part) <= 255);
}

function escapeHTMLAttribute(value: string): string {
  return value.replace(/&/g, "&amp;").replace(/"/g, "&quot;");
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
  const [showRaw, setShowRaw] = useState(false);
  const [wrap, setWrap] = useState(true);
  const prettyPreview = useMemo(() => normalizeToolOutputPreview(output), [output]);
  const rawPreview = useMemo(() => normalizeRawToolOutput(output), [output]);
  const preview = showRaw ? rawPreview : prettyPreview;
  const copyLabel = `Copy ${title}`;
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
          alignItems: "center",
          borderBottom: "1px solid var(--border)",
          color: "var(--t1)",
          display: "flex",
          gap: 8,
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          padding: "4px 7px",
        }}
      >
        <span style={{ minWidth: 0, overflow: "hidden", textOverflow: "ellipsis" }}>{title}</span>
        <div style={{ display: "flex", gap: 4, marginLeft: "auto" }}>
          <button
            aria-label={copyLabel}
            className="btn btn-ghost btn-sm"
            onClick={() => {
              void navigator.clipboard?.writeText(preview)?.catch(() => {});
            }}
            style={{ fontSize: 10, padding: "1px 6px" }}
            type="button"
          >
            Copy
          </button>
          <button
            aria-label={showRaw ? `Show cleaned ${title}` : `Show raw ${title}`}
            className="btn btn-ghost btn-sm"
            onClick={() => setShowRaw((current) => !current)}
            style={{ fontSize: 10, padding: "1px 6px" }}
            type="button"
          >
            {showRaw ? "Clean" : "Raw"}
          </button>
          <button
            aria-label={wrap ? `Disable wrapping for ${title}` : `Wrap ${title}`}
            className="btn btn-ghost btn-sm"
            onClick={() => setWrap((current) => !current)}
            style={{ fontSize: 10, padding: "1px 6px" }}
            type="button"
          >
            {wrap ? "No wrap" : "Wrap"}
          </button>
        </div>
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
          whiteSpace: wrap ? "pre-wrap" : "pre",
        }}
      >
        {preview}
      </pre>
    </div>
  );
}

function normalizeToolOutputPreview(output: string): string {
  return stripLineNumberGutters(normalizeRawToolOutput(output))
    .replace(/^\n+/, "")
    .replace(/\n{3,}/g, "\n\n")
    .trim();
}

function normalizeRawToolOutput(output: string): string {
  return stripAnsi(output).replace(/\r\n/g, "\n").replace(/\r/g, "\n");
}

function stripLineNumberGutters(output: string): string {
  if (!looksLikeLineNumberGutter(output)) return output;
  return output
    .replace(/(^|\n)[ \t]*\d{1,6}\s*(?:>|→|\||│)\s*/g, "$1")
    .replace(/[ \t]+\d{1,6}\s*(?:>|→|\||│)\s*/g, "\n");
}

function looksLikeLineNumberGutter(output: string): boolean {
  const matches = output.match(/(?:^|[ \t\n])\d{1,6}\s*(?:>|→|\||│)\s*/g) ?? [];
  return matches.length > 1 || /^[ \t]*\d{1,6}\s*(?:>|→|\||│)\s*/.test(output);
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
        workspace diff snapshot{summary ? ` · ${summary}` : ""}
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

function HeaderMetaLink({
  label,
  title,
  href,
  onClick,
}: {
  label: string;
  title?: string;
  href: string;
  onClick?: (event: ReactMouseEvent<HTMLAnchorElement>) => void;
}) {
  return (
    <a
      className="btn btn-ghost btn-sm"
      href={href}
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
        textDecoration: "none",
      }}
    >
      {label}
    </a>
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

function duplicatesFailureNotice(
  activity: ChatActivityRecord,
  message: string,
  taskBacked: boolean,
): boolean {
  if (activity.type !== "failed" && activity.status !== "failed") return false;
  if (activity.type === "tool_call") return false;
  // Keep this title list in sync with generic terminal failure rows from
  // internal/api/handler_chat_activities.go and handler_chat.go. Richer
  // diagnostic titles should remain visible in the activity timeline.
  const title = activity.title.trim().toLowerCase();
  if (title !== "failed" && title !== "turn failed" && !(taskBacked && title === "run failed")) {
    return false;
  }
  const detail = activity.detail?.trim() ?? "";
  if (!detail) return true;
  return detail === message.trim();
}

function AgentTurnNotice({
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
        agent turn {status}
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
    timing.model_call_count
      ? `${timing.model_call_count} model call${timing.model_call_count === 1 ? "" : "s"}`
      : "",
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

function agentTimingEmpty(timing: ChatTimingRecord): boolean {
  return (
    !timing.total_ms &&
    !timing.queue_ms &&
    !timing.model_ms &&
    !timing.tool_ms &&
    !timing.approval_wait_ms &&
    !timing.overhead_ms &&
    !timing.model_call_count &&
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

function buildTurnDebugBundle(input: {
  id: string;
  model?: string;
  badge?: string;
  content: string;
  turnPrompt?: string;
  traceLabel?: string;
  traceTitle?: string;
  taskLabel?: string;
  runtimeMeta?: string;
  runtimeMetaTitle?: string;
  diffStat?: string;
  activities?: ChatActivityRecord[];
  contextPacket?: ChatContextPacketRecord;
  error?: string;
}): string {
  const sections: string[] = [];
  const meta = [
    `message: ${input.id}`,
    input.model ? `model: ${input.model}` : "",
    input.badge ? `status: ${input.badge}` : "",
    input.taskLabel ? `task: ${input.taskLabel}` : "",
    input.traceLabel
      ? `trace: ${input.traceLabel}${input.traceTitle ? ` (${input.traceTitle})` : ""}`
      : "",
    input.runtimeMeta
      ? `runtime: ${input.runtimeMeta}${input.runtimeMetaTitle ? ` (${input.runtimeMetaTitle})` : ""}`
      : "",
  ].filter(Boolean);
  sections.push(["Turn", ...meta].join("\n"));
  if (input.turnPrompt?.trim()) {
    sections.push(`Prompt\n${input.turnPrompt.trim()}`);
  }
  if (input.content.trim()) {
    sections.push(`Response\n${input.content.trim()}`);
  }
  if (input.error?.trim()) {
    sections.push(`Error\n${input.error.trim()}`);
  }
  if (input.diffStat?.trim()) {
    sections.push(`Workspace diff\n${input.diffStat.trim()}`);
  }
  if (input.activities?.length) {
    sections.push(`Activity\n${input.activities.map(formatDebugActivity).join("\n")}`);
  }
  if (input.contextPacket && !contextPacketEmpty(input.contextPacket)) {
    sections.push(`Context packet\n${JSON.stringify(input.contextPacket, null, 2)}`);
  }
  return `${sections.join("\n\n")}\n`;
}

function formatDebugActivity(activity: ChatActivityRecord): string {
  const display = activityDisplay(activity);
  const status = activityEffectiveStatus(activity);
  return [
    status ? `[${status}]` : "",
    activity.type,
    display.title,
    display.detail ? `— ${display.detail}` : "",
  ]
    .filter(Boolean)
    .join(" ");
}
