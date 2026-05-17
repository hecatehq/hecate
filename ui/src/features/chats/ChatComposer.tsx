import { useEffect, useRef, type RefObject, type SyntheticEvent } from "react";

import { useRuntimeConsoleContext } from "../../app/RuntimeConsoleContext";
import type { RuntimeConsoleViewModel } from "../../app/useRuntimeConsole";
import { type ChatSetupRepairState } from "../../lib/chat-setup-readiness";
import { claudeCodeSetupTokenCommand } from "../../lib/claude-code-setup";
import { describeGatewayError, formatErrorCode } from "../../lib/error-diagnostics";
import { usePersistedState } from "../../lib/persistedState";
import type { SelectedModelIssue } from "../../lib/provider-issues";
import { providerDisplayName } from "../../lib/provider-utils";
import type { AgentAdapterRecord } from "../../types/agent-adapter";
import type { ModelRecord } from "../../types/model";
import { Icon, Icons, InlineError } from "../shared/ui";

import {
  ExternalAgentConfigControls,
  HecateModelConfigControl,
  HecateProviderConfigControl,
  LockedHecateModelSnapshot,
} from "./ChatAgentControls";
import { ChatNoticeInline } from "./ChatNotice";
import { ClaudeCodePreflightCard } from "./ClaudeCodeSetup";
import type { ClaudeCodePreflightState } from "./ClaudeCodeSetup";

type HecateProviderOption = {
  id: string;
  name: string;
  healthy: boolean;
  kind?: string;
  configured?: boolean;
  disabledReason?: string;
};

export type ChatComposerProps = {
  // Active chat shape (derived in ChatView and threaded through here).
  isAgentChat: boolean;
  isHecateChat: boolean;
  isExternalAgentChat: boolean;
  isHecateAgentChat: boolean;
  activeSessionID: string;

  // Cross-region ref. ChatView owns creation so onSelectSession can
  // focus the textarea without reaching into composer internals.
  textareaRef: RefObject<HTMLTextAreaElement | null>;

  // Composer gating.
  composerVisible: boolean;
  composerRepair: ChatSetupRepairState | null;
  messageControlsVisible: boolean;
  showClaudeCodeEmptyPreflight: boolean;
  sendDisabled: boolean;
  agentBusy: boolean;
  queueingMessage: boolean;
  selectedModelIssue: SelectedModelIssue | null;
  chatDiagnostic: ReturnType<typeof describeGatewayError>;

  // Hecate-chat config view.
  hecateAgentModelLocked: boolean;
  hecateChatProviderValue: string;
  hecateChatModelValue: string;
  hecateProviderOptions: HecateProviderOption[];
  hecateDisabledProviderReasons: Map<string, string>;
  selectableModels: ModelRecord[];

  // Repair / preflight / capability writes.
  selectedAgent: AgentAdapterRecord | undefined;
  selectedAgentHealthLoading: boolean;
  claudeCodePreflight: ClaudeCodePreflightState | null;
  selectedCapabilityProvider: string;
  selectedCapabilityModel: string;
  capabilitySaving: boolean;
  enableToolsForSelectedModel: () => Promise<void> | void;
  chooseWorkspace: () => Promise<void> | void;
  openClaudeCodeSetup: () => void;

  // Active task tracking + queue.
  activeHecateTaskID: string;
  activeHecateRunID: string;
  // Filtered to the active session — ChatView already does the filter.
  activeQueuedChatMessages: RuntimeConsoleViewModel["state"]["queuedChatMessages"];

  // User-message history feeds the arrow-key recall, derived in
  // ChatView from visibleMessages.
  messageHistory: string[];

  // Threaded from ChatView's own Props.
  onNavigate?: (workspace: "connections" | "runs" | "overview" | "settings") => void;
  onOpenTask?: (taskID: string, runID?: string) => void;
  onOpenTrace?: (requestID: string) => void;
};

export function ChatComposer(props: ChatComposerProps) {
  const { state, actions } = useRuntimeConsoleContext();
  const {
    isAgentChat,
    isHecateChat,
    isExternalAgentChat,
    isHecateAgentChat,
    activeSessionID,
    textareaRef,
    composerVisible,
    composerRepair,
    messageControlsVisible,
    showClaudeCodeEmptyPreflight,
    sendDisabled,
    agentBusy,
    queueingMessage,
    selectedModelIssue,
    chatDiagnostic,
    hecateAgentModelLocked,
    hecateChatProviderValue,
    hecateChatModelValue,
    hecateProviderOptions,
    hecateDisabledProviderReasons,
    selectableModels,
    selectedAgent,
    selectedAgentHealthLoading,
    claudeCodePreflight,
    selectedCapabilityProvider,
    selectedCapabilityModel,
    capabilitySaving,
    enableToolsForSelectedModel,
    chooseWorkspace,
    openClaudeCodeSetup,
    activeHecateTaskID,
    activeHecateRunID,
    activeQueuedChatMessages,
    messageHistory,
    onNavigate,
    onOpenTask,
    onOpenTrace,
  } = props;

  const isMac = typeof navigator !== "undefined" && /mac/i.test(navigator.platform);
  const modKey = isMac ? "⌘" : "Ctrl";
  const [modEnterMode, setModEnterMode] = usePersistedState<boolean>(
    "hecate.shiftEnterMode",
    (raw) => raw === "1" ? true : raw === "0" ? false : null,
    false,
    { serialize: (v) => v ? "1" : "0" },
  );
  const formRef = useRef<HTMLFormElement>(null);
  const messageHistoryCursorRef = useRef<number | null>(null);
  const messageHistoryDraftRef = useRef("");

  // Reset history navigation on session change. Scroll-side reset
  // lives in ChatView since it concerns the transcript surface.
  useEffect(() => {
    messageHistoryCursorRef.current = null;
    messageHistoryDraftRef.current = "";
  }, [activeSessionID]);

  function setComposerText(value: string, cursorAtEnd = false) {
    actions.setMessage(value);
    if (!cursorAtEnd) return;
    requestAnimationFrame(() => {
      const node = textareaRef.current;
      if (!node) return;
      const end = node.value.length;
      node.setSelectionRange(end, end);
    });
  }

  function handleMessageChange(value: string) {
    messageHistoryCursorRef.current = null;
    messageHistoryDraftRef.current = value;
    actions.setMessage(value);
  }

  function handleMessageHistoryKey(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key !== "ArrowUp" && e.key !== "ArrowDown") return false;
    if (messageHistory.length === 0) return false;

    const node = e.currentTarget;
    const selectionStart = node.selectionStart ?? 0;
    const selectionEnd = node.selectionEnd ?? 0;
    const hasSelection = selectionStart !== selectionEnd;
    const browsing = messageHistoryCursorRef.current !== null;
    const isEmpty = state.message.length === 0;
    const singleLine = !state.message.includes("\n");
    const atStart = selectionStart === 0 && selectionEnd === 0;
    const atEnd = selectionStart === state.message.length && selectionEnd === state.message.length;

    if (hasSelection) return false;

    if (e.key === "ArrowUp") {
      // Preserve normal multiline navigation unless the operator is
      // deliberately at the top of the composer or already browsing.
      if (!singleLine && !isEmpty && !atStart && !browsing) return false;
      e.preventDefault();
      if (!browsing) {
        messageHistoryDraftRef.current = state.message;
      }
      const current = messageHistoryCursorRef.current;
      const next = current === null ? messageHistory.length - 1 : Math.max(0, current - 1);
      messageHistoryCursorRef.current = next;
      setComposerText(messageHistory[next], true);
      return true;
    }

    if (!singleLine && !isEmpty && !atEnd && !browsing) return false;
    e.preventDefault();
    const current = messageHistoryCursorRef.current;
    if (current === null) return true;
    const next = current + 1;
    if (next >= messageHistory.length) {
      messageHistoryCursorRef.current = null;
      setComposerText(messageHistoryDraftRef.current, true);
      return true;
    }
    messageHistoryCursorRef.current = next;
    setComposerText(messageHistory[next], true);
    return true;
  }

  function handleKeyDown(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (handleMessageHistoryKey(e)) return;
    if (e.key !== "Enter") return;
    const modPressed = isMac ? e.metaKey : e.ctrlKey;
    if (modEnterMode) {
      // ⌘/Ctrl+Enter sends; plain Enter is a newline (default behaviour)
      if (modPressed) { e.preventDefault(); formRef.current?.requestSubmit(); }
    } else {
      // Enter sends; Shift+Enter or ⌘/Ctrl+Enter inserts a newline
      if (e.shiftKey || modPressed) return;
      e.preventDefault();
      formRef.current?.requestSubmit();
    }
  }

  function handleSubmit(e: SyntheticEvent<HTMLFormElement>) {
    void actions.submitChat(e);
    if (textareaRef.current) {
      textareaRef.current.style.height = "auto";
    }
  }

  function toggleModEnterMode() {
    setModEnterMode(v => !v);
  }

  if (showClaudeCodeEmptyPreflight) return null;
  if (!composerVisible && !messageControlsVisible && !state.chatError && !selectedModelIssue) return null;

  return (
    <form ref={formRef} onSubmit={handleSubmit} style={{ borderTop: "1px solid var(--border)", padding: "10px 12px", background: "var(--bg1)", flexShrink: 0 }}>
      {state.chatError && (
        <div style={{ marginBottom: 8 }}>
          <ChatErrorPanel
            message={state.chatError}
            provider={state.runtimeHeaders?.provider}
            code={state.chatErrorCode}
            action={state.chatErrorAction}
            requestID={state.chatErrorRequestID}
            status={state.chatErrorStatus ?? undefined}
            traceID={state.chatErrorTraceID}
            onOpenTrace={onOpenTrace}
            diagnostic={chatDiagnostic}
          />
        </div>
      )}
      {isHecateChat && selectedModelIssue && (
        <div style={{ marginBottom: composerVisible ? 8 : 0 }}>
          <SelectedModelReadinessNotice
            issue={selectedModelIssue}
            onOpenProviders={() => onNavigate?.("connections")}
            onUseSuggestedModel={(model) => {
              actions.setProviderFilter("auto");
              actions.setModel(model);
            }}
          />
        </div>
      )}
      {composerVisible && (
      <>
      {isExternalAgentChat && claudeCodePreflight && !showClaudeCodeEmptyPreflight && (
        <ClaudeCodePreflightCard
          state={claudeCodePreflight}
          loading={selectedAgentHealthLoading}
          onCopyInstall={() => void actions.copyCommand("npx -y @anthropic-ai/claude-code --version")}
          onCopySetup={() => void actions.copyCommand(claudeCodeSetupTokenCommand(selectedAgent?.claude_code_cli))}
          onOpenSetup={openClaudeCodeSetup}
          onTest={() => void actions.probeAgentAdapter("claude_code")}
        />
      )}
      {composerRepair && (
        <ChatSetupRepairNotice
          repair={composerRepair}
          actionBusy={composerRepair.action === "enable_tools" && capabilitySaving}
          actionDisabled={composerRepair.action === "enable_tools" && (!selectedCapabilityProvider || !selectedCapabilityModel || capabilitySaving)}
          actionTitle={composerRepair.action === "enable_tools" && selectedCapabilityProvider
            ? `Enable tools for ${selectedCapabilityProvider}/${selectedCapabilityModel}`
            : undefined}
          onAction={(repair) => {
            if (repair.action === "choose_workspace") {
              void chooseWorkspace();
            } else if (repair.action === "enable_tools") {
              void enableToolsForSelectedModel();
            } else if (repair.action === "open_agent_setup") {
              openClaudeCodeSetup();
            } else if (repair.action === "open_connections") {
              onNavigate?.("connections");
            }
          }}
        />
      )}
      {activeQueuedChatMessages.length > 0 && (
        <div
          aria-label="Queued messages"
          style={{
            maxWidth: 820,
            margin: "0 auto 8px",
            border: "1px solid var(--border)",
            borderRadius: "var(--radius-sm)",
            background: "var(--bg2)",
            padding: "7px 9px",
            display: "grid",
            gap: 6,
          }}
        >
          <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 8 }}>
            <span style={{ color: "var(--t2)", fontFamily: "var(--font-mono)", fontSize: 10, textTransform: "uppercase", letterSpacing: "0.08em" }}>
              Queued next
            </span>
            <span style={{ color: "var(--t3)", fontSize: 11 }}>
              will send when the active run finishes
            </span>
          </div>
          {activeQueuedChatMessages.map((queued, index) => (
            <div
              key={queued.id}
              style={{
                display: "grid",
                gridTemplateColumns: "auto minmax(0, 1fr) auto",
                alignItems: "center",
                gap: 8,
                color: "var(--t0)",
                fontSize: 12,
              }}
            >
              <span style={{ color: "var(--teal)", fontFamily: "var(--font-mono)", fontSize: 10 }}>
                #{index + 1}
              </span>
              <textarea
                aria-label={`Queued message ${index + 1}`}
                className="queued-chat-message-input"
                value={queued.content}
                onChange={(event) => actions.updateQueuedChatMessage(queued.id, event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === "Enter" && !event.shiftKey) event.preventDefault();
                }}
                rows={Math.min(4, Math.max(1, queued.content.split("\n").length))}
                style={{
                  minWidth: 0,
                  width: "100%",
                  resize: "vertical",
                  borderRadius: "var(--radius-sm)",
                  color: "var(--t0)",
                  font: "inherit",
                  padding: "3px 6px",
                  outline: "none",
                }}
              />
              <button
                type="button"
                className="btn btn-ghost btn-sm"
                aria-label={`Remove queued message ${index + 1}`}
                onClick={() => actions.removeQueuedChatMessage(queued.id)}
                style={{ padding: "2px 6px", fontFamily: "var(--font-mono)", fontSize: 10 }}
              >
                remove
              </button>
            </div>
          ))}
        </div>
      )}
      {messageControlsVisible && (
        <div
          aria-label={isExternalAgentChat ? "External agent message controls" : "Hecate message controls"}
          style={{
            maxWidth: 820,
            margin: "0 auto 8px",
            display: "flex",
            justifyContent: "flex-start",
            flexWrap: "wrap",
            gap: 6,
          }}
        >
          {isExternalAgentChat ? (
            <ExternalAgentConfigControls
              session={state.activeChatSession}
              onChange={actions.setChatConfigOption}
              placement="composer"
            />
          ) : hecateAgentModelLocked ? (
            <LockedHecateModelSnapshot
              provider={providerLabelForHecateChat(state, hecateChatProviderValue)}
              model={hecateChatModelValue}
            />
          ) : (
            <>
              <HecateProviderConfigControl
                value={state.providerFilter}
                onChange={v => actions.setProviderFilter(v as typeof state.providerFilter)}
                options={hecateProviderOptions}
              />
              <HecateModelConfigControl
                value={state.model}
                onChange={actions.setModel}
                models={selectableModels}
                presets={state.providerPresets}
                showProvider={false}
                disabledProviders={hecateDisabledProviderReasons}
              />
            </>
          )}
        </div>
      )}
      <div style={{ maxWidth: 820, margin: "0 auto", position: "relative" }}>
        <textarea
          ref={textareaRef}
          aria-label="Message"
          value={state.message}
          onChange={e => handleMessageChange(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder={modEnterMode ? `Message… (${modKey}+Enter to send)` : "Message… (Shift+Enter for newline)"}
          rows={1}
          style={{
            width: "100%", background: "var(--bg3)", border: "1px solid var(--border)",
            borderRadius: "var(--radius)", color: "var(--t0)", fontFamily: "var(--font-sans)",
            fontSize: 13, padding: "10px 44px 10px 12px", outline: "none", resize: "none",
            lineHeight: 1.5, transition: "border-color 0.1s", minHeight: 42, maxHeight: 160, overflowY: "auto",
          }}
          onInput={e => {
            const el = e.target as HTMLTextAreaElement;
            el.style.height = "auto";
            el.style.height = Math.min(el.scrollHeight, 160) + "px";
          }}
          onFocus={e => (e.target.style.borderColor = "var(--teal)")}
          onBlur={e => (e.target.style.borderColor = "var(--border)")}
        />
        {agentBusy && !queueingMessage ? (
          <button type="button"
            className="btn btn-danger"
            aria-label="Stop current run"
            disabled={state.chatCancelling}
            title={state.chatCancelling ? "Stopping..." : "Stop current run"}
            onClick={actions.cancelAgentChat}
            style={{
              position: "absolute", right: 8, top: "50%", transform: "translateY(-50%)",
              width: 28, height: 28, borderRadius: "var(--radius-sm)",
              padding: 0,
              display: "flex", alignItems: "center", justifyContent: "center",
            }}>
            <Icon d={Icons.stop} size={13} fill="currentColor" strokeWidth={0} />
          </button>
        ) : (
          <button type="submit"
            aria-label={queueingMessage ? "Queue message" : "Send message"}
            disabled={sendDisabled}
            title={queueingMessage ? "Queue this message after the active run finishes" : "Send message"}
            style={{
              position: "absolute", right: 8, top: "50%", transform: "translateY(-50%)",
              width: 28, height: 28, borderRadius: "var(--radius-sm)",
              background: !sendDisabled ? "var(--teal)" : "var(--bg4)",
              border: "none", cursor: !sendDisabled ? "pointer" : "default",
              display: "flex", alignItems: "center", justifyContent: "center",
              transition: "background 0.1s",
              color: !sendDisabled ? "var(--bg0)" : "var(--t3)",
            }}>
            <Icon d={Icons.send} size={14} />
          </button>
        )}
      </div>
      {agentBusy && (
        <div style={{ maxWidth: 820, margin: "6px auto 0", color: "var(--amber)", fontFamily: "var(--font-mono)", fontSize: 11, lineHeight: 1.45, display: "flex", alignItems: "center", gap: 8, justifyContent: "space-between", flexWrap: "wrap" }}>
          <span>
            {isExternalAgentChat
              ? "External Agent is still working. New messages will queue until it finishes."
              : "Hecate Chat is still working on this task. New messages will queue until the active task finishes."}
          </span>
          <span style={{ display: "inline-flex", alignItems: "center", gap: 6, flexWrap: "wrap" }}>
            {onOpenTask && activeHecateTaskID && (
              <button
                type="button"
                className="btn btn-ghost btn-sm"
                onClick={() => onOpenTask(activeHecateTaskID, activeHecateRunID)}
                style={{ fontFamily: "var(--font-mono)", fontSize: 10, padding: "2px 6px", color: "var(--amber)" }}
              >
                Open task
              </button>
            )}
            <button
              type="button"
              className="btn btn-ghost btn-sm"
              aria-label={isExternalAgentChat ? "Stop external agent" : "Stop active task"}
              title={state.chatCancelling ? "Stopping..." : isExternalAgentChat ? "Stop external agent" : "Stop active task"}
              onClick={actions.cancelAgentChat}
              disabled={state.chatCancelling}
              style={{ fontFamily: "var(--font-mono)", fontSize: 10, padding: "2px 6px", color: "var(--danger)" }}
            >
              Stop
            </button>
          </span>
        </div>
      )}
      {isAgentChat && state.chatCancelling && (
        <div style={{ maxWidth: 820, margin: "6px auto 0", color: "var(--t3)", fontFamily: "var(--font-mono)", fontSize: 11 }}>
          Stopping...
        </div>
      )}
      <div style={{ maxWidth: 820, margin: "3px auto 0", display: "flex", alignItems: "center", gap: 10, justifyContent: "space-between" }}>
        {isExternalAgentChat ? (
          <span style={{ color: "var(--t3)", fontFamily: "var(--font-mono)", fontSize: 10 }}>
            External agents run as your OS user in the selected workspace — no sandbox
          </span>
        ) : isHecateAgentChat ? (
          <span style={{ color: "var(--t3)", fontFamily: "var(--font-mono)", fontSize: 10 }}>
            Hecate Agent runs through task approvals and per-call sandboxing in the selected workspace.
          </span>
        ) : <span />}
        <button type="button" onClick={toggleModEnterMode} style={{
          fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)",
          background: "none", border: "none", cursor: "pointer", padding: 0,
        }}>
          {modEnterMode ? `${modKey}+↵ to send` : "↵ to send"}
        </button>
      </div>
      </>
      )}
    </form>
  );
}

export function ChatErrorPanel({
  message,
  provider,
  code,
  action,
  requestID,
  status,
  traceID,
  onOpenTrace,
  diagnostic,
}: {
  message: string;
  provider?: string;
  code?: string;
  action?: string;
  requestID?: string;
  status?: number;
  traceID?: string;
  onOpenTrace?: (requestID: string) => void;
  diagnostic: ReturnType<typeof describeGatewayError>;
}) {
  const label = formatErrorCode(code, status);
  const recommendedAction = action || diagnostic?.action || "";
  if (!diagnostic) {
    return <InlineError message={`${provider ? `[${provider}] ` : ""}${message}`} />;
  }

  return (
    <div
      role="alert"
      style={{
        border: "1px solid var(--red-border)",
        background: "var(--red-bg)",
        borderRadius: "var(--radius)",
        padding: "9px 11px",
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 4 }}>
        <span style={{ fontSize: 12, fontWeight: 600, color: "var(--red)" }}>{diagnostic.title}</span>
        {label && (
          <span style={{ marginLeft: "auto", fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--red)" }}>
            {label}
          </span>
        )}
      </div>
      <div style={{ fontSize: 12, color: "var(--t0)", lineHeight: 1.45 }}>{message}</div>
      {recommendedAction && (
        <div style={{ fontSize: 11, color: "var(--t2)", lineHeight: 1.45, marginTop: 5 }}>
          {provider ? `${provider}: ` : ""}{recommendedAction}
        </div>
      )}
      {(requestID || traceID) && (
        <div style={{ marginTop: 7, display: "flex", flexWrap: "wrap", gap: 8, alignItems: "center" }}>
          {requestID && (
            <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
              request <span style={{ color: "var(--t1)" }}>{compactID(requestID, [], 10)}</span>
            </span>
          )}
          {traceID && (
            <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
              trace <span style={{ color: "var(--t1)" }}>{compactID(traceID, [], 10)}</span>
            </span>
          )}
          {requestID && onOpenTrace && (
            <button
              type="button"
              onClick={() => onOpenTrace(requestID)}
              style={{
                border: "1px solid var(--red-border)",
                background: "transparent",
                color: "var(--red)",
                borderRadius: 999,
                padding: "2px 8px",
                fontSize: 10,
                fontFamily: "var(--font-mono)",
                cursor: "pointer",
              }}
            >
              Open trace
            </button>
          )}
        </div>
      )}
    </div>
  );
}

export function ChatSetupRepairNotice({
  repair,
  actionBusy = false,
  actionDisabled = false,
  actionTitle,
  onAction,
}: {
  repair: ChatSetupRepairState;
  actionBusy?: boolean;
  actionDisabled?: boolean;
  actionTitle?: string;
  onAction: (repair: ChatSetupRepairState) => void;
}) {
  return (
    <ChatNoticeInline
      tone={repair.tone}
      title={repair.title}
      message={repair.message}
      action={repair.actionLabel}
      actionBusy={actionBusy}
      actionBusyLabel="Saving..."
      actionDisabled={actionDisabled}
      actionTitle={actionTitle}
      onAction={() => onAction(repair)}
    />
  );
}

export function SelectedModelReadinessNotice({
  issue,
  compact = false,
  onOpenProviders,
  onUseSuggestedModel,
}: {
  issue: SelectedModelIssue;
  compact?: boolean;
  onOpenProviders?: () => void;
  onUseSuggestedModel?: (model: string) => void;
}) {
  return (
    <div style={{
      margin: compact ? "14px auto 0" : "0 auto",
      maxWidth: compact ? 560 : 820,
      border: "1px solid rgba(245, 191, 79, 0.32)",
      borderRadius: "var(--radius)",
      background: "rgba(245, 191, 79, 0.06)",
      padding: 12,
      textAlign: "left",
    }}>
      {!compact && (
        <div style={{ display: "flex", alignItems: "flex-start", justifyContent: "space-between", gap: 12 }}>
          <div style={{ minWidth: 0 }}>
            <div style={{ fontSize: 12, fontWeight: 700, color: "var(--amber)", marginBottom: 4 }}>
              {issue.title}
            </div>
            <div style={{ fontSize: 12, color: "var(--t2)", lineHeight: 1.5 }}>
              {issue.message}
            </div>
          </div>
          {onOpenProviders && (
            <button className="btn btn-ghost btn-sm" type="button" onClick={onOpenProviders} style={{ flexShrink: 0 }}>
              Open Connections
            </button>
          )}
          {issue.suggestedModel && onUseSuggestedModel && (
            <button
              className="btn btn-primary btn-sm"
              type="button"
              onClick={() => onUseSuggestedModel(issue.suggestedModel!)}
              style={{ flexShrink: 0 }}
            >
              Use {issue.suggestedModel}
            </button>
          )}
        </div>
      )}
      <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(150px, 1fr))", gap: 8, marginTop: 10 }}>
        {selectedModelNoticeDetails(issue.details, compact).map((detail) => (
          <InfoChip key={detail.label} label={detail.label} value={detail.value} />
        ))}
      </div>
      {compact ? (
        <>
          <ul style={{ margin: "10px 0 0", paddingLeft: 18, color: "var(--t3)", fontSize: 11, lineHeight: 1.55 }}>
            {issue.steps.slice(0, 2).map((step) => <li key={step}>{step}</li>)}
          </ul>
          {issue.suggestedModel && onUseSuggestedModel && (
            <button
              className="btn btn-primary btn-sm"
              type="button"
              onClick={() => onUseSuggestedModel(issue.suggestedModel!)}
              style={{ marginTop: 10 }}
            >
              Use {issue.suggestedModel}
            </button>
          )}
        </>
      ) : (
        <ul style={{ margin: "10px 0 0", paddingLeft: 18, color: "var(--t3)", fontSize: 11, lineHeight: 1.55 }}>
          {issue.steps.map((step) => <li key={step}>{step}</li>)}
        </ul>
      )}
    </div>
  );
}

function selectedModelNoticeDetails(
  details: SelectedModelIssue["details"],
  compact: boolean,
): SelectedModelIssue["details"] {
  if (!compact) {
    return details;
  }
  const priorityLabels = new Set(["Selected model", "Provider route", "Discovered models", "Health", "Blocked by", "Last error"]);
  const selected = details.filter((detail) => priorityLabels.has(detail.label));
  return selected.length > 0 ? selected : details;
}

function InfoChip({ label, value }: { label: string; value: string }) {
  return (
    <div style={{ border: "1px solid var(--border)", borderRadius: "var(--radius-sm)", background: "var(--bg3)", padding: "7px 8px", minWidth: 0 }}>
      <div style={{ fontSize: 10, color: "var(--t3)", fontFamily: "var(--font-mono)", textTransform: "uppercase", letterSpacing: "0.04em" }}>
        {label}
      </div>
      <div title={value} style={{ marginTop: 3, fontSize: 11, color: "var(--t1)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
        {value}
      </div>
    </div>
  );
}

function providerLabelForHecateChat(state: RuntimeConsoleViewModel["state"], providerID: string): string {
  if (!providerID || providerID === "auto") {
    return "Select provider";
  }
  return providerDisplayName(providerID, state.settingsConfig?.providers, state.providerPresets, state.providers);
}

export function repairActionIcon(repair: ChatSetupRepairState) {
  switch (repair.action) {
    case "choose_workspace":
      return Icons.folder;
    case "open_agent_setup":
      return Icons.terminal;
    case "use_suggested_model":
      return Icons.model;
    case "open_connections":
      return Icons.connections;
    case "enable_tools":
      return Icons.providers;
  }
  return Icons.providers;
}

export function compactID(id: string, prefixes: string[], length: number): string {
  const trimmed = id.trim();
  const withoutPrefix = prefixes.reduce((current, prefix) => (
    current.startsWith(prefix) ? current.slice(prefix.length) : current
  ), trimmed);
  return withoutPrefix.slice(0, length);
}
