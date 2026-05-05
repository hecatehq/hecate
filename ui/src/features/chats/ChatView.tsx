import { useEffect, useRef, useState } from "react";
import type { SyntheticEvent } from "react";
import type { RuntimeConsoleViewModel } from "../../app/useRuntimeConsole";
import { describeGatewayError, formatErrorCode } from "../../lib/error-diagnostics";
import { parseInlineNodes, parseMarkdownBlocks } from "../../lib/markdown";
import type { AgentAdapterRecord, AgentChatActivityRecord, AgentChatSessionRecord, AgentChatUsageRecord } from "../../types/runtime";
import { AgentAdapterPicker, CodeBlock, Icon, Icons, InlineError, ModelPicker, ProviderPicker } from "../shared/ui";
import { AgentApprovalAutoModeBanner, AgentApprovalsBanner } from "./AgentApprovalBanner";
import { AgentApprovalModal } from "./AgentApprovalModal";

type Props = {
  state: RuntimeConsoleViewModel["state"];
  actions: RuntimeConsoleViewModel["actions"];
  onNavigate?: (workspace: "providers") => void;
};

type VisibleChatMessage = {
  id: string;
  run_id?: string;
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
  diff_stat?: string;
  diff?: string;
  raw_output?: string;
  activities?: AgentChatActivityRecord[];
  usage?: AgentChatUsageRecord;
  duration_ms?: number;
  error?: string;
};

type SidebarSession = {
  id: string;
  title?: string;
  message_count: number;
  provider_call_count: number;
  last_provider?: string;
  last_model?: string;
  created_at?: string;
  updated_at?: string;
};

export function ChatView({ state, actions, onNavigate }: Props) {
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [syspromptOpen, setSyspromptOpen] = useState(false);
  // approvalModalID is the per-banner-click open state for the
  // approval modal. The modal itself fetches the full row on mount;
  // we only carry the id here.
  const [approvalModalID, setApprovalModalID] = useState<string | null>(null);
  const [renamingId, setRenamingId] = useState<string | null>(null);
  const [renameValue, setRenameValue] = useState("");
  const [hoveredChatId, setHoveredChatId] = useState<string | null>(null);
  const [copiedMsgId, setCopiedMsgId] = useState<string | null>(null);
  const [atBottom, setAtBottom] = useState(true);
  const [workspaceEntryOpen, setWorkspaceEntryOpen] = useState(false);
  const [workspacePathValue, setWorkspacePathValue] = useState("");
  const [sidebarQuery, setSidebarQuery] = useState("");
  const isMac = typeof navigator !== "undefined" && /mac/i.test(navigator.platform);
  const modKey = isMac ? "⌘" : "Ctrl";
  const [modEnterMode, setModEnterMode] = useState(
    () => localStorage.getItem("hecate.shiftEnterMode") === "1"
  );
  const formRef = useRef<HTMLFormElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  const bottomRef = useRef<HTMLDivElement>(null);
  const sidebarScrollRef = useRef<HTMLDivElement>(null);
  const userScrolledRef = useRef(false);

  const isAgentChat = state.chatTarget === "agent";
  const sessions: SidebarSession[] = isAgentChat
    ? (state.agentChatSessions ?? []).map((s) => ({
        id: s.id,
        title: s.title,
        message_count: s.message_count,
        provider_call_count: 0,
        last_provider: s.adapter_id,
        last_model: s.status,
        created_at: s.created_at,
        updated_at: s.updated_at,
      }))
    : (state.chatSessions ?? []);
  const filteredSessions = filterSidebarSessions(sessions, sidebarQuery);
  const groupedSessions = groupSidebarSessions(filteredSessions);
  const activeSessionID = isAgentChat ? state.activeAgentChatSessionID : state.activeChatSessionID;
  const activeTitle = isAgentChat
    ? state.activeAgentChatSession?.title
    : state.activeChatSession?.title;
  const messages: VisibleChatMessage[] = isAgentChat
    ? (state.activeAgentChatSession?.messages ?? []).map((m, index) => ({
        id: m.id || `agent-message-${index}`,
        run_id: m.run_id,
        trace_id: m.trace_id,
        native_session_id: m.native_session_id,
        role: m.role,
        content: m.content,
        created_at: m.created_at,
        agent_adapter_id: m.adapter_id,
        agent_adapter_name: m.adapter_name,
        agent_status: m.status,
        cost_mode: m.cost_mode,
        diff_stat: m.diff_stat,
        diff: m.diff,
        raw_output: m.raw_output,
        activities: m.activities,
        usage: m.usage,
        duration_ms: m.duration_ms,
        error: m.error,
      }))
    : (state.activeChatSession?.messages ?? []).map((m) => ({
        id: m.id,
        role: m.role,
        content: m.content,
        created_at: m.created_at,
        produced_by_call_id: m.produced_by_call_id,
      }));
  const providerCalls = isAgentChat ? [] : (state.activeChatSession?.provider_calls ?? []);
  // Lookup map so the assistant rows can pull tokens/cost from the
  // call that produced them. The relationship is many-messages → one
  // call (server-driven tool loops fold many tool steps under a single
  // call), but for now the chat surface only emits one assistant per
  // call.
  const callsByID = new Map(providerCalls.map((c) => [c.id, c]));
  // Hide system messages and any assistant placeholder that is still
  // waiting for content — the streaming-content block below renders
  // the live text instead.
  const visibleMessages = messages.filter((m) => {
    if (m.role === "system") return false;
    if (m.role === "assistant" && m.content === null) return false;
    return true;
  });
  const streaming = state.chatLoading;
  const chatDiagnostic = describeGatewayError(state.chatErrorCode, state.chatErrorStatus ?? undefined);
  const activeAgentAdapterID = state.activeAgentChatSession?.adapter_id || state.agentAdapterID;
  const selectedAgent = state.agentAdapters.find((adapter) => adapter.id === activeAgentAdapterID);
  const availableAgents = state.agentAdapters.filter((adapter) => adapter.available);
  const configuredProviders = state.controlPlaneConfig?.providers ?? [];
  const providerConfigLoaded = state.controlPlaneConfig !== null;
  const selectableModels = (() => {
    // Scope the model list to providers the operator has explicitly
    // configured. The /v1/models endpoint may return models from
    // env-driven providers too, but those aren't routable from Chats
    // unless the control-plane store knows about them.
    if (!providerConfigLoaded) return state.providerScopedModels;
    if (configuredProviders.length === 0) return [];
    const ids = new Set(configuredProviders.map(c => c.id));
    return state.providerScopedModels.filter(m => {
      const provider = m.metadata?.provider;
      return typeof provider === "string" ? ids.has(provider) : true;
    });
  })();
  const modelRouteUnavailable = providerConfigLoaded && selectableModels.length === 0;
  const agentRouteUnavailable = availableAgents.length === 0;
  const selectedAgentUnavailable = isAgentChat && Boolean(selectedAgent) && !selectedAgent?.available;
  const nothingRunnable = !state.loading && modelRouteUnavailable && agentRouteUnavailable;
  const agentPickerLocked = isAgentChat && Boolean(state.activeAgentChatSessionID);
  const sendDisabled = !state.message.trim()
    || streaming
    || (!isAgentChat && modelRouteUnavailable)
    || (isAgentChat && (!state.agentWorkspace.trim() || !selectedAgent?.available));

  useEffect(() => {
    if (!userScrolledRef.current) {
      bottomRef.current?.scrollIntoView({ behavior: "instant" });
    }
  }, [state.streamingContent, visibleMessages.length]);

  useEffect(() => {
    // Reset scroll state on every session change. Focus is NOT applied
    // here on purpose: data-load (sessions arriving from the API) also
    // triggers an activeChatSessionID transition, and stealing focus on
    // load would block page-level keyboard shortcuts (1/2/3/4/5) for
    // the entire dashboard. Focus is instead applied at the explicit
    // user-driven entry points: the New-session button and the session
    // row onClick handlers.
    userScrolledRef.current = false;
    setAtBottom(true);
    bottomRef.current?.scrollIntoView({ behavior: "instant" });
  }, [activeSessionID]);

  useEffect(() => {
    setWorkspacePathValue(state.agentWorkspace);
  }, [state.agentWorkspace]);

  function handleScroll() {
    const el = scrollRef.current;
    if (!el) return;
    const threshold = 80;
    const isAtBottom = el.scrollHeight - el.scrollTop - el.clientHeight < threshold;
    setAtBottom(isAtBottom);
    userScrolledRef.current = !isAtBottom;
  }

  function handleSidebarScroll() {
    const el = sidebarScrollRef.current;
    if (isAgentChat || sidebarQuery.trim() || !el || !state.chatSessionsHasMore || state.chatSessionsLoadingMore) return;
    const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 60;
    if (nearBottom) {
      void actions.loadMoreChatSessions();
    }
  }

  function scrollToBottom() {
    userScrolledRef.current = false;
    setAtBottom(true);
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }

  async function chooseWorkspace() {
    const selected = await actions.chooseAgentWorkspace();
    if (!selected) {
      setWorkspaceEntryOpen(true);
    }
  }

  function useTypedWorkspace() {
    const next = workspacePathValue.trim();
    if (!next) return;
    actions.setAgentWorkspace(next);
    setWorkspaceEntryOpen(false);
  }

  function copyMsg(id: string, text: string) {
    navigator.clipboard?.writeText(text).catch(() => {});
    setCopiedMsgId(id);
    setTimeout(() => setCopiedMsgId(null), 2000);
  }

  function toggleModEnterMode() {
    setModEnterMode(v => {
      const next = !v;
      localStorage.setItem("hecate.shiftEnterMode", next ? "1" : "0");
      return next;
    });
  }

  function handleKeyDown(e: React.KeyboardEvent<HTMLTextAreaElement>) {
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

  return (
    <div style={{ display: "flex", height: "100%", overflow: "hidden" }}>
      {/* Conversation sidebar */}
      {sidebarOpen && (
        <div style={{ width: 220, borderRight: "1px solid var(--border)", display: "flex", flexDirection: "column", flexShrink: 0, background: "var(--bg1)" }}>
          <div style={{ padding: 8, borderBottom: "1px solid var(--border)", display: "flex", gap: 6 }}>
            <button
              className="btn btn-primary btn-sm"
              style={{ flex: 1, justifyContent: "center" }}
              onClick={() => {
                actions.createChatSession();
                textareaRef.current?.focus();
              }}
            >
              <Icon d={Icons.plus} size={13} /> New chat
            </button>
            <button className="btn btn-ghost btn-sm" onClick={() => setSidebarOpen(false)} title="Close">
              <Icon d={Icons.chevL} size={13} />
            </button>
          </div>
          <div style={{ padding: "8px 12px 4px", fontFamily: "var(--font-mono)", fontSize: 10, letterSpacing: "0.08em", textTransform: "uppercase", color: "var(--t3)" }}>
            Chats{sessions.length > 0 ? ` · ${filteredSessions.length}${filteredSessions.length === sessions.length ? "" : `/${sessions.length}`}` : ""}
          </div>
          <div style={{ padding: "4px 8px 8px" }}>
            <input
              aria-label="Search chats"
              className="input"
              onChange={(e) => setSidebarQuery(e.target.value)}
              placeholder="Search chats"
              style={{ height: 28, fontSize: 12, padding: "0 8px" }}
              value={sidebarQuery}
            />
          </div>
          <div ref={sidebarScrollRef} onScroll={handleSidebarScroll} style={{ flex: 1, overflowY: "auto", padding: "2px 0 6px" }}>
            {sessions.length === 0 && (
              <div style={{ padding: "16px 12px", fontSize: 12, color: "var(--t3)", textAlign: "center" }}>No chats yet</div>
            )}
            {sessions.length > 0 && filteredSessions.length === 0 && (
              <div style={{ padding: "16px 12px", fontSize: 12, color: "var(--t3)", textAlign: "center" }}>No matching chats</div>
            )}
            {groupedSessions.map((group) => (
              <div key={group.label}>
                <div style={{ padding: "8px 12px 3px", fontFamily: "var(--font-mono)", fontSize: 9, letterSpacing: "0.08em", textTransform: "uppercase", color: "var(--t3)" }}>
                  {group.label}
                </div>
                {group.sessions.map(s => (
                  <div key={s.id}
                    onClick={() => {
                      if (renamingId === s.id) return;
                      void actions.selectChatSession(s.id);
                      textareaRef.current?.focus();
                    }}
                    onMouseEnter={() => setHoveredChatId(s.id)}
                    onMouseLeave={() => setHoveredChatId(null)}
                    style={{
                      padding: "8px 12px", cursor: "pointer",
                      background: activeSessionID === s.id ? "var(--teal-bg)" : "transparent",
                      borderLeft: activeSessionID === s.id ? "2px solid var(--teal)" : "2px solid transparent",
                      transition: "background 0.1s",
                    }}>
                    <div style={{ display: "flex", alignItems: "center", gap: 2, height: 18 }}>
                      {renamingId === s.id ? (
                        <input
                          autoFocus
                          value={renameValue}
                          onChange={e => setRenameValue(e.target.value)}
                          onClick={e => e.stopPropagation()}
                          onKeyDown={e => {
                            if (e.key === "Enter") { void actions.renameChatSession(s.id, renameValue); setRenamingId(null); }
                            if (e.key === "Escape") setRenamingId(null);
                          }}
                          onBlur={() => { void actions.renameChatSession(s.id, renameValue); setRenamingId(null); }}
                          style={{ flex: 1, minWidth: 0, height: 18, boxSizing: "border-box", fontSize: 12, background: "var(--bg3)", border: "1px solid var(--teal)", borderRadius: "var(--radius-sm)", color: "var(--t0)", padding: "0 4px", outline: "none", fontFamily: "var(--font-sans)", lineHeight: "16px" }}
                        />
                      ) : (
                        <>
                          <div style={{ flex: 1, minWidth: 0, fontSize: 12, lineHeight: "18px", color: activeSessionID === s.id ? "var(--t0)" : "var(--t1)", fontWeight: activeSessionID === s.id ? 500 : 400, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                            {s.title || "Untitled"}
                          </div>
                          <div style={{ display: "flex", gap: 1, opacity: hoveredChatId === s.id ? 1 : 0, transition: "opacity 0.15s", flexShrink: 0 }}>
                            {!isAgentChat && (
                              <button
                                className="btn btn-ghost btn-sm"
                                onClick={e => { e.stopPropagation(); setRenamingId(s.id); setRenameValue(s.title || ""); }}
                                style={{ padding: "1px 3px" }}
                                title="Rename"
                              >
                                <Icon d={Icons.edit} size={10} />
                              </button>
                            )}
                            <button
                              className="btn btn-ghost btn-sm"
                              onClick={e => { e.stopPropagation(); void actions.deleteChatSession(s.id); }}
                              style={{ padding: "1px 3px", color: "var(--red)" }}
                              title="Delete"
                            >
                              <Icon d={Icons.trash} size={10} />
                            </button>
                          </div>
                        </>
                      )}
                    </div>
                    <div style={{ fontSize: 10, color: "var(--t3)", marginTop: 1, fontFamily: "var(--font-mono)" }}>
                      {isAgentChat
                        ? `${s.message_count} msg${s.last_provider ? ` · ${s.last_provider}` : ""}${s.last_model ? ` · ${s.last_model}` : ""}`
                        : `${s.message_count} msg · ${s.provider_call_count} call${s.provider_call_count === 1 ? "" : "s"}${s.last_provider ? ` · ${s.last_provider}` : ""}`}
                    </div>
                  </div>
                ))}
              </div>
            ))}
            {!isAgentChat && state.chatSessionsLoadingMore && (
              <div style={{ padding: "8px 12px", fontSize: 11, color: "var(--t3)", textAlign: "center" }}>Loading chats…</div>
            )}
            {!isAgentChat && state.chatSessionsHasMore && !state.chatSessionsLoadingMore && (
              <div style={{ padding: "8px 12px" }}>
                <button className="btn btn-ghost btn-sm" onClick={() => void actions.loadMoreChatSessions()} style={{ width: "100%", justifyContent: "center" }} type="button">
                  {sidebarQuery.trim() ? "Search earlier chats" : "Load earlier chats"}
                </button>
              </div>
            )}
          </div>
        </div>
      )}

      {/* Chats main */}
      <div style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden", minWidth: 0, position: "relative" }}>
        {/* Topbar */}
        <div style={{ height: "var(--topbar-h)", borderBottom: "1px solid var(--border)", display: "flex", alignItems: "center", padding: "0 12px", gap: 8, flexShrink: 0, background: "var(--bg1)" }}>
          {!sidebarOpen && (
            <button className="btn btn-ghost btn-sm" onClick={() => setSidebarOpen(true)} title="Open chats">
              <Icon d={Icons.chevR} size={13} />
            </button>
          )}
          <div style={{ display: "flex", border: "1px solid var(--border)", borderRadius: "var(--radius-sm)", overflow: "hidden", flexShrink: 0 }}>
            {(["agent", "model"] as const).map((target) => (
              <button
                key={target}
                className="btn btn-ghost btn-sm"
                onClick={() => actions.setChatTarget(target)}
                style={{
                  borderRadius: 0,
                  background: state.chatTarget === target ? "var(--teal-bg)" : "transparent",
                  color: state.chatTarget === target ? "var(--teal)" : "var(--t2)",
                  border: 0,
                }}
                title={target === "model" ? "Chat with a model through Hecate providers" : "Chat with an external coding agent"}
              >
                {target === "model" ? "Model" : "Agent"}
              </button>
            ))}
          </div>
          <span style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)", flex: 1, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            {activeTitle || (sessions.length === 0 ? "New chat" : "Select a chat")}
          </span>
          {isAgentChat && (
            <span
              title={formatAgentSessionTitle(state.activeAgentChatSession, selectedAgent)}
              style={{ flexShrink: 0, fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)", maxWidth: 260, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
            >
              {formatAgentSessionLabel(state.activeAgentChatSession, selectedAgent)}
            </span>
          )}
          {isAgentChat ? (
            <>
              <AgentAdapterPicker
                value={activeAgentAdapterID}
                onChange={actions.setAgentAdapterID}
                adapters={state.agentAdapters}
                healthByID={state.agentAdapterHealthByID}
                disabled={agentPickerLocked}
                disabledReason="Agent is fixed for this chat. Start a new chat to choose another agent."
              />
              <button
                className="btn btn-ghost btn-sm"
                onClick={() => void chooseWorkspace()}
                title={state.agentWorkspace ? `Workspace: ${state.agentWorkspace}` : "Choose workspace folder"}
                type="button"
              >
                <Icon d={Icons.folder} size={13} />
                <span style={{ fontSize: 11 }}>{state.agentWorkspace ? "workspace" : "choose workspace"}</span>
              </button>
              <button
                className="btn btn-ghost btn-sm"
                onClick={() => {
                  setWorkspacePathValue(state.agentWorkspace);
                  setWorkspaceEntryOpen(v => !v);
                }}
                title="Paste a workspace path"
                type="button"
              >
                <span style={{ fontSize: 11 }}>paste path</span>
              </button>
              {(() => {
                const sess = state.activeAgentChatSession;
                if (!sess || !sess.max_turns_per_session) return null;
                const turnsUsed = sess.turns_used ?? 0;
                const maxTurns = sess.max_turns_per_session;
                const atLimit = turnsUsed >= maxTurns;
                return (
                  <span
                    data-testid="agent-chat-turns-badge"
                    style={{
                      flexShrink: 0,
                      fontFamily: "var(--font-mono)",
                      fontSize: 10,
                      color: atLimit ? "var(--amber)" : "var(--t3)",
                      whiteSpace: "nowrap",
                    }}
                    title={atLimit ? "Turn limit reached — start a new chat to continue" : `${turnsUsed} of ${maxTurns} turns used`}
                  >
                    {turnsUsed}/{maxTurns} turns
                  </span>
                );
              })()}
            </>
          ) : (
            <>
              <ProviderPicker
                value={state.providerFilter}
                onChange={v => actions.setProviderFilter(v as typeof state.providerFilter)}
                options={(() => {
                  // Source the picker from the operator's configured providers
                  // (the CP store), not the runtime status list. Health is not
                  // a filter — a temporarily-down provider is still a valid
                  // selection.
                  const configured = state.controlPlaneConfig?.providers ?? [];
                  const source = configured.length > 0
                    ? configured.map(c => ({ id: c.id, name: c.name, kind: c.kind }))
                    : state.providers
                        .filter(p => p.name)
                        .map(p => ({ id: p.name, name: p.name, kind: state.providerPresets.find(pr => pr.id === p.name)?.kind }));

                  return source
                    .map(p => {
                      const cfg = state.controlPlaneConfig?.providers.find(c => c.id === p.id);
                      // Cloud-with-no-credentials is the only "disabled"
                      // reason left now that the toggle is gone — we
                      // surface it as a tooltip + key icon rather than
                      // hiding the row, so the operator sees why the
                      // provider isn't usable and where to fix it.
                      const cloudUnconfigured = !!cfg && cfg.kind === "cloud" && !cfg.credential_configured;
                      return {
                        id: p.id,
                        name: state.providerPresets.find(pr => pr.id === p.id)?.name || p.name || p.id,
                        healthy: true, // dot suppressed in the picker; field kept for type compatibility
                        kind: p.kind,
                        configured: cfg ? cfg.credential_configured : undefined,
                        disabledReason: cloudUnconfigured ? `Add an API key for ${cfg!.name || cfg!.id} on the Providers tab` : undefined,
                      };
                    });
                })()}
                includeAuto
              />
              <ModelPicker
                value={state.model}
                onChange={actions.setModel}
                // Scope the model list to providers the operator has explicitly
                // configured. The /v1/models endpoint may return models from
                // env-driven providers too (e.g. Docker's PROVIDER_*_BASE_URL
                // pre-filled vars), but those aren't in controlPlaneConfig.providers
                // and shouldn't be selectable from the chat picker.
                models={selectableModels}
                presets={state.providerPresets}
                // Pinned width pairs the chat header's model picker with
                // the provider picker for a stable, non-jittery layout.
                triggerWidth={220}
                // Show the provider suffix only when "All providers" is
                // selected — when a specific provider is filtered, the
                // suffix is redundant on every row.
                showProvider={state.providerFilter === "auto"}
                // Provider ids whose models should render as disabled rows
                // (with a key indicator). Cloud-with-no-credentials is the
                // only "disabled" reason now that the toggle is gone.
                disabledProviders={(() => {
                  const out = new Map<string, string>();
                  for (const cfg of state.controlPlaneConfig?.providers ?? []) {
                    if (cfg.kind === "cloud" && !cfg.credential_configured) {
                      out.set(cfg.id, `Add an API key for ${cfg.name || cfg.id} on the Providers tab`);
                    }
                  }
                  return out;
                })()}
              />
              <button className="btn btn-ghost btn-sm" onClick={() => setSyspromptOpen(o => !o)}
                style={{ color: syspromptOpen ? "var(--teal)" : "var(--t2)" }} title="System prompt">
                <Icon d={Icons.edit} size={13} />
                <span style={{ fontSize: 11 }}>system</span>
              </button>
            </>
          )}
        </div>

        {isAgentChat && workspaceEntryOpen && (
          <div style={{ borderBottom: "1px solid var(--border)", padding: "10px 14px", background: "var(--bg2)", display: "flex", alignItems: "center", gap: 8 }}>
            <span style={{ fontSize: 11, color: "var(--t2)", fontFamily: "var(--font-mono)", flexShrink: 0 }}>WORKSPACE PATH</span>
            <input
              className="input"
              onChange={e => setWorkspacePathValue(e.target.value)}
              onKeyDown={e => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  useTypedWorkspace();
                }
              }}
              placeholder="/Users/alice/dev/project"
              style={{ height: 30, minWidth: 0 }}
              value={workspacePathValue}
            />
            <button className="btn btn-primary btn-sm" disabled={!workspacePathValue.trim()} onClick={useTypedWorkspace} type="button">
              Use
            </button>
          </div>
        )}

        {/* System prompt editor */}
        {!isAgentChat && syspromptOpen && (
          <div style={{ borderBottom: "1px solid var(--border)", padding: "10px 14px", background: "var(--bg2)" }}>
            <div style={{ display: "flex", alignItems: "center", marginBottom: 5, gap: 8 }}>
              <span style={{ fontSize: 11, color: "var(--t2)", fontFamily: "var(--font-mono)" }}>SYSTEM PROMPT</span>
              {messages.length > 0 && <span style={{ fontSize: 10, color: "var(--t3)", fontFamily: "var(--font-mono)" }}>locked — start a new chat to change</span>}
            </div>
            <textarea
              value={state.systemPrompt}
              onChange={e => actions.setSystemPrompt(e.target.value)}
              disabled={messages.length > 0}
              style={{ width: "100%", background: "var(--bg3)", border: "1px solid var(--border)", borderRadius: "var(--radius-sm)", color: messages.length > 0 ? "var(--t2)" : "var(--t0)", fontFamily: "var(--font-mono)", fontSize: 12, padding: "8px 10px", resize: "vertical", minHeight: 72, outline: "none", lineHeight: 1.5, opacity: messages.length > 0 ? 0.6 : 1 }}
            />
          </div>
        )}

        {/* External-agent approval surfaces. Both banners are agent-chat-only;
            the auto-mode warning is persistent for as long as the gateway
            runs in auto, the pending banner appears only when there's at
            least one pending approval for the active session. */}
        {isAgentChat && (
          <>
            <AgentApprovalAutoModeBanner mode={state.agentAdapterApprovalMode} />
            {state.activeAgentChatSessionID && (
              <AgentApprovalsBanner
                pending={state.pendingApprovalsBySessionID.get(state.activeAgentChatSessionID) ?? []}
                onSelect={(id) => setApprovalModalID(id)}
              />
            )}
          </>
        )}

        {/* Messages */}
        <div style={{ flex: 1, overflow: "hidden", position: "relative" }}>
        <div ref={scrollRef} onScroll={handleScroll} style={{ height: "100%", overflowY: "auto", padding: "16px 0" }}>
          {visibleMessages.map(m => {
            const call = m.produced_by_call_id ? callsByID.get(m.produced_by_call_id) : undefined;
            const role = m.role === "assistant" ? "assistant" : "user";
            const content = typeof m.content === "string" ? m.content : (m.content === null ? "" : JSON.stringify(m.content));
            const time = m.created_at ? new Date(m.created_at).toLocaleTimeString("en-US", { hour: "2-digit", minute: "2-digit" }) : "";
            const agentModel = m.agent_adapter_name || m.agent_adapter_id;
            const agentRuntime = isAgentChat && role === "assistant"
              ? formatAgentRuntimeMeta(m.run_id, m.duration_ms, m.trace_id, m.native_session_id)
              : "";
            return (
              <MessageRow
                key={m.id}
                id={m.id}
                role={role}
                model={isAgentChat ? agentModel : call?.model}
                content={content}
                time={time}
                promptTokens={call?.prompt_tokens}
                completionTokens={call?.completion_tokens}
                costUsd={call?.cost_usd}
                badge={isAgentChat && role === "assistant" ? (m.agent_status || m.cost_mode) : undefined}
                runtimeMeta={agentRuntime}
                activities={isAgentChat && role === "assistant" ? m.activities : undefined}
                diffStat={isAgentChat && role === "assistant" ? m.diff_stat : undefined}
                diff={isAgentChat && role === "assistant" ? m.diff : undefined}
                rawOutput={isAgentChat && role === "assistant" ? m.raw_output : undefined}
                agentUsage={isAgentChat && role === "assistant" ? m.usage : undefined}
                error={isAgentChat && role === "assistant" ? m.error : undefined}
                onCopy={copyMsg}
                copied={copiedMsgId === m.id}
              />
            );
          })}

          {/* Streaming */}
          {!isAgentChat && streaming && state.streamingContent !== null && (
            <div style={{ padding: "4px 16px 16px", maxWidth: 820, margin: "0 auto", width: "100%" }}>
              <div style={{ display: "flex", gap: 10, alignItems: "flex-start" }}>
                <div style={{ width: 28, height: 28, borderRadius: "var(--radius-sm)", background: "var(--teal-bg)", border: "1px solid var(--teal-border)", display: "flex", alignItems: "center", justifyContent: "center", flexShrink: 0, marginTop: 2 }}>
                  <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--teal)", fontWeight: 600 }}>{(state.model || "H")[0].toUpperCase()}</span>
                </div>
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 6 }}>
                    <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--teal)" }}>
                      {isAgentChat ? (selectedAgent?.name || state.agentAdapterID || "agent") : (state.model || "hecate")}
                    </span>
                    <span className="badge badge-teal" style={{ animation: "pulse 1s ease-in-out infinite", fontSize: 10 }}>
                      {isAgentChat ? "running" : "streaming"}
                    </span>
                  </div>
                  <p style={{ fontSize: 13, color: "var(--t0)", lineHeight: 1.7, whiteSpace: "pre-wrap" }}>
                    {state.streamingContent}<span className="cursor-blink">▋</span>
                  </p>
                </div>
              </div>
            </div>
          )}

          {/* Pending tool calls */}
          {state.pendingToolCalls.length > 0 && (
            <div style={{ padding: "4px 16px 16px", maxWidth: 820, margin: "0 auto", width: "100%" }}>
              <div style={{ fontSize: 11, color: "var(--t2)", marginBottom: 8 }}>
                {state.pendingToolCalls.length} tool call{state.pendingToolCalls.length > 1 ? "s" : ""} pending
              </div>
              {state.pendingToolCalls.map((tc, i) => (
                <div key={tc.id} style={{ border: "1px solid var(--border)", borderRadius: "var(--radius)", padding: "10px 12px", background: "var(--bg2)", marginBottom: 8 }}>
                  <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 6 }}>
                    <span style={{ fontFamily: "var(--font-mono)", fontSize: 12, fontWeight: 600, color: "var(--teal)" }}>{tc.name}</span>
                    <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>{tc.id}</span>
                  </div>
                  <CodeBlock code={(() => { try { return JSON.stringify(JSON.parse(tc.arguments), null, 2); } catch { return tc.arguments; } })()} lang="json" />
                  <div style={{ marginTop: 8 }}>
                    <label style={{ fontSize: 11, color: "var(--t2)", display: "block", marginBottom: 4 }}>Result</label>
                    <textarea
                      className="input"
                      onChange={e => actions.updateToolResult(i, e.target.value)}
                      placeholder="Enter the tool result"
                      rows={3}
                      style={{ resize: "vertical" }}
                      value={tc.result}
                    />
                  </div>
                </div>
              ))}
              <button className="btn btn-primary btn-sm"
                disabled={state.chatLoading || state.pendingToolCalls.some(tc => !tc.result.trim())}
                onClick={() => void actions.submitToolResults()}>
                {state.chatLoading ? "Running…" : "Submit results"}
              </button>
            </div>
          )}

          {visibleMessages.length === 0 && !streaming && state.pendingToolCalls.length === 0 && (
            <ChatEmptyState
              isAgentChat={isAgentChat}
              modelRouteUnavailable={modelRouteUnavailable}
              agentRouteUnavailable={agentRouteUnavailable}
              nothingRunnable={nothingRunnable}
              agentAdapters={state.agentAdapters}
              selectedAgent={selectedAgent}
              selectedAgentUnavailable={selectedAgentUnavailable}
              onAddProvider={() => onNavigate?.("providers")}
              onSwitchTarget={actions.setChatTarget}
            />
          )}
          <div ref={bottomRef} />
        </div>

        {!atBottom && (
          <button onClick={scrollToBottom} style={{
            position: "absolute", bottom: 16, left: "50%", transform: "translateX(-50%)",
            height: 28, padding: "0 12px", borderRadius: 14,
            background: "var(--bg3)", border: "1px solid var(--border)",
            cursor: "pointer", display: "flex", alignItems: "center", gap: 5,
            color: "var(--t1)", fontSize: 12, boxShadow: "var(--shadow-popover)",
            zIndex: 10, whiteSpace: "nowrap",
          }}>
            <Icon d={Icons.chevD} size={12} /> Scroll to bottom
          </button>
        )}
        </div>

        {/* Input bar */}
        <form ref={formRef} onSubmit={handleSubmit} style={{ borderTop: "1px solid var(--border)", padding: "10px 12px", background: "var(--bg1)", flexShrink: 0 }}>
          {state.chatError && (
            <div style={{ marginBottom: 8 }}>
              <ChatErrorPanel
                message={state.chatError}
                provider={state.runtimeHeaders?.provider}
                code={state.chatErrorCode}
                status={state.chatErrorStatus ?? undefined}
                diagnostic={chatDiagnostic}
              />
            </div>
          )}
          <div style={{ maxWidth: 820, margin: "0 auto", position: "relative" }}>
            <textarea
              ref={textareaRef}
              value={state.message}
              onChange={e => actions.setMessage(e.target.value)}
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
            {isAgentChat && streaming ? (
              <button type="button"
                className="btn btn-danger"
                aria-label="Stop agent"
                disabled={state.agentChatCancelling}
                title={state.agentChatCancelling ? "Stopping agent..." : "Stop agent"}
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
                disabled={sendDisabled}
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
          {isAgentChat && state.agentChatCancelling && (
            <div style={{ maxWidth: 820, margin: "6px auto 0", color: "var(--t3)", fontFamily: "var(--font-mono)", fontSize: 11 }}>
              Stopping external agent...
            </div>
          )}
          <div style={{ maxWidth: 820, margin: "3px auto 0", display: "flex", alignItems: "center", gap: 10, justifyContent: "space-between" }}>
            {isAgentChat ? (
              <span style={{ color: "var(--t3)", fontFamily: "var(--font-mono)", fontSize: 10 }}>
                External agents run as your OS user in the selected workspace — no sandbox
              </span>
            ) : <span />}
            <button type="button" onClick={toggleModEnterMode} style={{
              fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)",
              background: "none", border: "none", cursor: "pointer", padding: 0,
            }}>
              {modEnterMode ? `${modKey}+↵ to send` : "↵ to send"}
            </button>
          </div>
        </form>
      </div>

      <style>{`
        .cursor-blink { color: var(--teal); }
        @keyframes pulse { 0%,100%{opacity:1} 50%{opacity:0.5} }
        @keyframes hecate-live-caret {
          0%, 100% { opacity: 0.25; transform: translateY(-1px) scale(0.85); }
          50% { opacity: 0.9; transform: translateY(-1px) scale(1.15); }
        }
      `}</style>

      {approvalModalID && state.activeAgentChatSessionID && (
        <AgentApprovalModal
          sessionID={state.activeAgentChatSessionID}
          approvalID={approvalModalID}
          onClose={() => setApprovalModalID(null)}
          fetchApproval={actions.getAgentChatApproval}
          onResolve={actions.resolveAgentChatApproval}
          onCancel={actions.cancelAgentChatApproval}
        />
      )}
    </div>
  );
}

function filterSidebarSessions(sessions: SidebarSession[], query: string): SidebarSession[] {
  const needle = query.trim().toLowerCase();
  if (!needle) return sessions;
  return sessions.filter((session) => {
    const searchable = [
      session.title,
      session.last_provider,
      session.last_model,
    ].filter(Boolean).join(" ").toLowerCase();
    return searchable.includes(needle);
  });
}

function groupSidebarSessions(sessions: SidebarSession[]): Array<{ label: string; sessions: SidebarSession[] }> {
  const groups = new Map<string, SidebarSession[]>();
  for (const session of sessions) {
    const label = sidebarDateGroup(session.updated_at || session.created_at);
    const group = groups.get(label) ?? [];
    group.push(session);
    groups.set(label, group);
  }
  return ["Today", "This week", "Older", "No date"]
    .map((label) => ({ label, sessions: groups.get(label) ?? [] }))
    .filter((group) => group.sessions.length > 0);
}

function sidebarDateGroup(value?: string): string {
  if (!value) return "No date";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "No date";
  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const chatDay = new Date(date.getFullYear(), date.getMonth(), date.getDate());
  const ageDays = Math.floor((today.getTime() - chatDay.getTime()) / 86_400_000);
  if (ageDays <= 0) return "Today";
  if (ageDays < 7) return "This week";
  return "Older";
}

function ChatErrorPanel({
  message,
  provider,
  code,
  status,
  diagnostic,
}: {
  message: string;
  provider?: string;
  code?: string;
  status?: number;
  diagnostic: ReturnType<typeof describeGatewayError>;
}) {
  const label = formatErrorCode(code, status);
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
      <div style={{ fontSize: 11, color: "var(--t2)", lineHeight: 1.45, marginTop: 5 }}>
        {provider ? `${provider}: ` : ""}{diagnostic.action}
      </div>
    </div>
  );
}

function ChatEmptyState({
  isAgentChat,
  modelRouteUnavailable,
  agentRouteUnavailable,
  nothingRunnable,
  agentAdapters,
  selectedAgent,
  selectedAgentUnavailable,
  onAddProvider,
  onSwitchTarget,
}: {
  isAgentChat: boolean;
  modelRouteUnavailable: boolean;
  agentRouteUnavailable: boolean;
  nothingRunnable: boolean;
  agentAdapters: AgentAdapterRecord[];
  selectedAgent?: AgentAdapterRecord;
  selectedAgentUnavailable: boolean;
  onAddProvider: () => void;
  onSwitchTarget: (target: "model" | "agent") => void;
}) {
  const title = isAgentChat && selectedAgentUnavailable
      ? `${selectedAgent?.name || "Selected agent"} is unavailable`
      : isAgentChat && agentRouteUnavailable
      ? "No available coding agent"
      : nothingRunnable
        ? "Nothing runnable yet"
        : !isAgentChat && modelRouteUnavailable
          ? "No routable model"
          : "Start a chat";
  const detail = isAgentChat && selectedAgentUnavailable
      ? `Hecate could not start ${selectedAgent?.name || "the selected agent"} because its CLI is not ready in this environment.`
      : isAgentChat && agentRouteUnavailable
      ? "Hecate did not find any supported coding-agent CLI or local adapter runner in the known operator locations."
      : nothingRunnable
        ? "Add a model provider or install a supported coding-agent CLI before sending a message."
        : !isAgentChat && modelRouteUnavailable
          ? "Add a provider with discovered models before sending through Hecate."
          : "Send a message to start this chat.";

  return (
    <div style={{ padding: "48px 16px", maxWidth: 820, margin: "0 auto", textAlign: "center" }}>
      <div style={{ fontSize: 13, fontWeight: 600, color: "var(--t1)", marginBottom: 5 }}>{title}</div>
      <div style={{ fontSize: 12, color: "var(--t3)", lineHeight: 1.5, maxWidth: 430, margin: "0 auto" }}>{detail}</div>
      {isAgentChat && (agentRouteUnavailable || selectedAgentUnavailable) && (
        <AgentSetupHints adapters={agentAdapters} selectedID={selectedAgent?.id} />
      )}
      {(modelRouteUnavailable || agentRouteUnavailable) && (
        <div style={{ display: "flex", justifyContent: "center", gap: 8, marginTop: 14, flexWrap: "wrap" }}>
          {modelRouteUnavailable && !isAgentChat && (
            <button className="btn btn-primary btn-sm" onClick={onAddProvider} type="button">
              <Icon d={Icons.providers} size={13} /> Add provider
            </button>
          )}
          {agentRouteUnavailable && !isAgentChat && (
            <button className="btn btn-ghost btn-sm" onClick={() => onSwitchTarget("agent")} type="button">
              <Icon d={Icons.terminal} size={13} /> Check agents
            </button>
          )}
          {!agentRouteUnavailable && !isAgentChat && (
            <button className="btn btn-ghost btn-sm" onClick={() => onSwitchTarget("agent")} type="button">
              <Icon d={Icons.terminal} size={13} /> Use agent
            </button>
          )}
          {!modelRouteUnavailable && isAgentChat && (
            <button className="btn btn-ghost btn-sm" onClick={() => onSwitchTarget("model")} type="button">
              <Icon d={Icons.model} size={13} /> Use model
            </button>
          )}
        </div>
      )}
    </div>
  );
}

function AgentSetupHints({ adapters, selectedID }: { adapters: AgentAdapterRecord[]; selectedID?: string }) {
  const ordered = adapters
    .slice()
    .sort((a, b) => {
      if (a.id === selectedID) return -1;
      if (b.id === selectedID) return 1;
      if (a.available !== b.available) return a.available ? 1 : -1;
      return a.name.localeCompare(b.name);
    });

  if (ordered.length === 0) {
    return (
      <div style={{ margin: "14px auto 0", maxWidth: 520, borderTop: "1px solid var(--border)", paddingTop: 12, fontSize: 12, color: "var(--t2)", lineHeight: 1.5 }}>
        No agent adapters are registered by this Hecate build.
      </div>
    );
  }

  return (
    <div style={{ margin: "16px auto 0", maxWidth: 620, textAlign: "left", border: "1px solid var(--border)", borderRadius: "var(--radius)", background: "var(--bg2)", overflow: "hidden" }}>
      {ordered.map((adapter, index) => {
        const hint = agentSetupHint(adapter);
        return (
          <div
            key={adapter.id}
            style={{
              padding: "10px 12px",
              borderTop: index === 0 ? 0 : "1px solid var(--border)",
              display: "grid",
              gridTemplateColumns: "minmax(120px, 0.7fr) minmax(0, 1.3fr)",
              gap: 10,
              alignItems: "start",
            }}
          >
            <div style={{ minWidth: 0 }}>
              <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
                <span style={{ color: adapter.available ? "var(--green)" : "var(--red)", display: "inline-flex", flexShrink: 0 }}>
                  <Icon d={adapter.available ? Icons.check : Icons.x} size={11} />
                </span>
                <span style={{ fontSize: 12, fontWeight: 600, color: "var(--t1)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                  {adapter.name}
                </span>
              </div>
              <div style={{ marginTop: 3, fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                {adapter.managed ? `managed ${adapter.managed_package || adapter.command}` : `checks ${adapter.command}`}
              </div>
            </div>
            <div style={{ minWidth: 0, fontSize: 11, color: "var(--t2)", lineHeight: 1.45 }}>
              <div style={{ color: adapter.available ? "var(--green)" : "var(--t1)" }}>
                {adapter.available ? agentReadyLabel(adapter) : hint.action}
              </div>
              {!adapter.available && adapter.error && (
                <div style={{ marginTop: 3, fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                  {adapter.error}
                </div>
              )}
              {adapter.docs_url && (
                <a href={adapter.docs_url} target="_blank" rel="noreferrer" style={{ display: "inline-flex", marginTop: 5, color: "var(--teal)", textDecoration: "none" }}>
                  setup docs
                </a>
              )}
            </div>
          </div>
        );
      })}
    </div>
  );
}

function agentSetupHint(adapter: AgentAdapterRecord): { action: string } {
  switch (adapter.id) {
    case "codex":
      return { action: "Install Node/npm so Hecate can create its managed Codex ACP launcher, then authenticate the underlying Codex CLI." };
    case "claude_code":
      return { action: "Install Node/npm so Hecate can create its managed Claude ACP launcher, then authenticate the underlying Claude agent." };
    case "cursor_agent":
      return { action: "Install Cursor Agent, make sure `cursor-agent` is on PATH, then run `cursor-agent login` or set `CURSOR_API_KEY`." };
    default:
      return { action: `Install ${adapter.name}, make sure \`${adapter.command}\` is on PATH, then refresh this view.` };
  }
}

function agentReadyLabel(adapter: AgentAdapterRecord): string {
  if (adapter.managed && adapter.managed_package) {
    return `Ready through Hecate-managed ${adapter.managed_package}`;
  }
  return `Ready at ${adapter.path || adapter.command}`;
}

function formatAgentSessionLabel(session: AgentChatSessionRecord | null, adapter?: AgentAdapterRecord): string {
  if (!session) {
    return adapter?.available ? "new agent session" : "agent not ready";
  }
  const driver = (session.driver_kind || "agent").toUpperCase();
  if (session.native_session_id) {
    return `${driver} ${session.native_session_id.slice(0, 12)} · ${session.status}`;
  }
  return `${driver} session · ${session.status}`;
}

function formatAgentSessionTitle(session: AgentChatSessionRecord | null, adapter?: AgentAdapterRecord): string {
  if (!session) {
    return adapter?.available
      ? `A new ${adapter.name} session will be created on send.`
      : "Install or authenticate an agent adapter before sending.";
  }
  const parts = [
    `${session.title || "Agent chat"} is backed by a persistent ${session.driver_kind || "agent"} session.`,
    session.native_session_id ? `Native session: ${session.native_session_id}.` : "",
    session.workspace ? `Workspace: ${session.workspace}.` : "",
    session.workspace_branch ? `Branch: ${session.workspace_branch}.` : "",
  ].filter(Boolean);
  return parts.join(" ");
}

function formatAgentRuntimeMeta(runID?: string, durationMS?: number, traceID?: string, nativeSessionID?: string): string {
  const parts: string[] = [];
  if (nativeSessionID) {
    parts.push(`ACP ${nativeSessionID.slice(0, 12)}`);
  }
  if (runID) {
    parts.push(`run ${runID.slice(0, 12)}`);
  }
  if (traceID) {
    parts.push(`trace ${traceID.slice(0, 8)}`);
  }
  if (durationMS && durationMS > 0) {
    parts.push(formatDuration(durationMS));
  }
  return parts.join(" · ");
}

function formatDuration(durationMS: number): string {
  if (durationMS < 1000) {
    return `${durationMS}ms`;
  }
  const seconds = durationMS / 1000;
  if (seconds < 60) {
    return `${seconds.toFixed(seconds < 10 ? 1 : 0)}s`;
  }
  const minutes = Math.floor(seconds / 60);
  const rest = Math.round(seconds % 60);
  return `${minutes}m ${rest}s`;
}

// (ModelPicker now lives in shared/ui — single component shared by the
// chat header, the new-task slideover, and any future surface that
// needs to pick a model with type-to-filter + disabled-provider
// awareness.)

function MessageRow({ id, role, model, content, time, promptTokens, completionTokens, costUsd, badge, runtimeMeta, activities, diffStat, diff, rawOutput, agentUsage, error, onCopy, copied }: {
  id: string; role: "user" | "assistant"; model?: string; content: string;
  time: string; promptTokens?: number; completionTokens?: number; costUsd?: string;
  badge?: string; runtimeMeta?: string;
  activities?: AgentChatActivityRecord[]; diffStat?: string; diff?: string; rawOutput?: string; agentUsage?: AgentChatUsageRecord; error?: string;
  onCopy: (id: string, text: string) => void; copied: boolean;
}) {
  const [hovered, setHovered] = useState(false);
  const isAssistant = role === "assistant";
  const hasTokenData = isAssistant && (promptTokens ?? 0) > 0;
  const showRawOutput = isAssistant && rawOutput && rawOutput.trim() && rawOutput.trim() !== content.trim();
  const waitingForAgentOutput = isAssistant && !content.trim() && activities?.some(activity => activity.status === "running");
  const failed = isAssistant && badge === "failed";
  const cancelled = isAssistant && badge === "cancelled";
  const thinkingForAgent = isAssistant
    && badge === "running"
    && content.trim() !== ""
    && isLikelyTransientAgentNarration(content)
    && !(activities ?? []).some(activity => activity.type === "tool_call");

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
          <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 5 }}>
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
            <Markdown content={content} />
          )}
          {isAssistant && activities && activities.length > 0 && (
            <ActivityTimeline activities={activities} diffStat={diffStat} />
          )}
          {isAssistant && agentUsage && !agentUsageEmpty(agentUsage) && (
            <AgentUsage usage={agentUsage} />
          )}
          {isAssistant && (diff || diffStat) && (
            <details style={{ marginTop: 8 }}>
              <summary style={{ cursor: "pointer", fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)" }}>
                files changed{diffStat ? ` · ${formatDiffStatSummary(diffStat)}` : ""}
              </summary>
              <div style={{ display: "grid", gap: 6, marginTop: 6 }}>
                {diffStat && <DiffStatList diffStat={diffStat} />}
                {diff && <CodeBlock code={diff} lang="diff" />}
              </div>
            </details>
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

function agentUsageEmpty(usage: AgentChatUsageRecord): boolean {
  return !usage.reported_cost_amount && !usage.reported_cost_currency && !(usage.context_size ?? 0) && !(usage.context_used ?? 0);
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

function DiffStatList({ diffStat }: { diffStat: string }) {
  const rows = parseDiffStatRows(diffStat);
  const summary = formatDiffStatSummary(diffStat);

  if (rows.length === 0) {
    return (
      <div style={{ color: "var(--t2)", fontFamily: "var(--font-mono)", fontSize: 11 }}>
        {summary}
      </div>
    );
  }

  return (
    <div style={{
      display: "grid",
      gap: 5,
      padding: "8px 10px",
      border: "1px solid var(--border)",
      borderRadius: "var(--radius-sm)",
      background: "var(--bg2)",
    }}>
      {rows.map(row => (
        <div key={row.path} style={{ display: "grid", gridTemplateColumns: "minmax(0, 1fr) auto", gap: 10, alignItems: "baseline" }}>
          <span style={{ color: "var(--t1)", fontFamily: "var(--font-mono)", fontSize: 11, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            {row.path}
          </span>
          <span style={{ color: "var(--t3)", fontFamily: "var(--font-mono)", fontSize: 11, whiteSpace: "nowrap" }}>
            {row.change}
          </span>
        </div>
      ))}
      {summary && (
        <div style={{ borderTop: "1px solid var(--border)", color: "var(--t2)", fontFamily: "var(--font-mono)", fontSize: 11, marginTop: 2, paddingTop: 6 }}>
          {summary}
        </div>
      )}
    </div>
  );
}

function parseDiffStatRows(diffStat: string): Array<{ path: string; change: string }> {
  return diffStat
    .split(/\r?\n/)
    .map(line => line.trim())
    .filter(Boolean)
    .filter(line => !/\bfiles? changed\b/.test(line))
    .map(line => {
      const match = line.match(/^(.+?)\s+\|\s+(.+)$/);
      if (!match) return null;
      return { path: match[1].trim(), change: match[2].trim() };
    })
    .filter((row): row is { path: string; change: string } => row !== null);
}

function formatDiffStatSummary(diffStat: string): string {
  const lines = diffStat.split(/\r?\n/).map(line => line.trim()).filter(Boolean);
  return lines.find(line => /\bfiles? changed\b/.test(line)) || lines[0] || "";
}

function ActivityTimeline({ activities, diffStat }: { activities: AgentChatActivityRecord[]; diffStat?: string }) {
  const visible = compactAgentActivities(activities);
  if (visible.length === 0) return null;
  const terminal = terminalAgentActivity(activities);
  const hasRunning = !terminal && activities.some(activity => activity.status === "running");
  const plan = visible.filter(activity => activity.type === "plan");
  const tools = visible.filter(activity => activity.type === "tool_call");
  const other = visible.filter(activity => activity.type !== "plan" && activity.type !== "tool_call");
  const summary = [
    terminal ? terminal.status : hasRunning ? "running" : "details",
    plan.length > 0 ? `${plan.filter(item => item.status === "completed").length}/${plan.length} plan` : "",
    tools.length > 0 ? `${tools.length} tool${tools.length === 1 ? "" : "s"}` : "",
    diffStat ? "files changed" : "",
  ].filter(Boolean).join(" · ");

  return (
    <details open={hasRunning} style={{ marginTop: 8 }}>
      <summary style={{ cursor: "pointer", fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)" }}>
        {summary}
      </summary>
      <div style={{
        display: "grid",
        gap: 5,
        marginTop: 6,
        padding: "8px 10px",
        border: "1px solid var(--border)",
        borderRadius: "var(--radius-sm)",
        background: "var(--bg2)",
      }}>
        {plan.length > 0 && <PlanActivityList items={plan} />}
        {tools.length > 0 && <ToolActivityList items={tools} />}
        {other.map((activity, index) => (
          <ActivityLine key={activity.id || `${activity.type}-${activity.created_at ?? index}`} activity={activity} />
        ))}
      </div>
    </details>
  );
}

function PlanActivityList({ items }: { items: AgentChatActivityRecord[] }) {
  return (
    <div style={{ display: "grid", gap: 5 }}>
      {items.map((activity, index) => (
        <div key={activity.id || `${activity.title}-${index}`} style={{ display: "flex", alignItems: "baseline", gap: 8, minWidth: 0 }}>
          <span style={{ color: activity.status === "completed" ? "var(--green)" : activity.status === "in_progress" ? "var(--teal)" : "var(--t3)", flexShrink: 0, fontFamily: "var(--font-mono)", fontSize: 11 }}>
            {activity.status === "completed" ? "x" : activity.status === "in_progress" ? ">" : "-"}
          </span>
          <span style={{ color: "var(--t1)", fontSize: 11, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            {activity.title}
          </span>
          {activity.kind && (
            <span style={{ color: "var(--t3)", flexShrink: 0, fontFamily: "var(--font-mono)", fontSize: 10 }}>
              {activity.kind}
            </span>
          )}
        </div>
      ))}
    </div>
  );
}

function ToolActivityList({ items }: { items: AgentChatActivityRecord[] }) {
  return (
    <div style={{ display: "grid", gap: 5 }}>
      {items.map((activity, index) => (
        <ActivityLine key={activity.id || `${activity.type}-${activity.created_at ?? index}`} activity={activity} prefix={activity.kind || "tool"} />
      ))}
    </div>
  );
}

function ActivityLine({ activity, prefix }: { activity: AgentChatActivityRecord; prefix?: string }) {
  return (
    <div style={{ display: "flex", alignItems: "baseline", gap: 8, minWidth: 0 }}>
      <span style={{
        width: 7,
        height: 7,
        borderRadius: 999,
        background: activityStatusColor(activity.status),
        flexShrink: 0,
      }} />
      {prefix && (
        <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)", whiteSpace: "nowrap" }}>
          {prefix}
        </span>
      )}
      <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t1)", whiteSpace: "nowrap" }}>
        {activity.title}
      </span>
      {activity.detail && (
        <span style={{ fontSize: 11, color: "var(--t3)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          {activity.detail}
        </span>
      )}
    </div>
  );
}

function compactAgentActivities(activities: AgentChatActivityRecord[]): AgentChatActivityRecord[] {
  const hiddenTypes = new Set(["output"]);
  const terminal = terminalAgentActivity(activities);
  const out: AgentChatActivityRecord[] = [];
  for (const activity of activities) {
    if (hiddenTypes.has(activity.type)) continue;
    if (activity.type === "completed" && activity.title.toLowerCase() === "final answer") continue;
    if (terminal && (activity.type === "started" || activity.type === "running")) continue;
    if (activity.type === "running" && activities.some(item => item.type === "output")) continue;
    out.push(activity);
  }
  return out;
}

function terminalAgentActivity(activities: AgentChatActivityRecord[]): AgentChatActivityRecord | undefined {
  const terminalTypes = new Set(["completed", "failed", "cancelled"]);
  return [...activities].reverse().find(activity => terminalTypes.has(activity.type));
}

function activityStatusColor(status?: string) {
  switch (status) {
  case "failed":
    return "var(--red)";
  case "cancelled":
    return "var(--amber)";
  case "running":
    return "var(--teal)";
  default:
    return "var(--green)";
  }
}

function formatLineCount(value: string): string {
  const trimmed = value.trim();
  if (!trimmed) return "0 lines";
  const count = trimmed.split(/\r?\n/).length;
  return `${count} line${count === 1 ? "" : "s"}`;
}

function Markdown({ content }: { content: string }) {
  const blocks = parseMarkdownBlocks(content);
  return (
    <div style={{ fontSize: 13, color: "var(--t0)", lineHeight: 1.7 }}>
      {blocks.map((block, i) => {
        if (block.type === "code") {
          return <CodeBlock key={i} code={block.text} lang={block.lang ?? ""} />;
        }
        if (block.type === "heading") {
          const sizes: Record<number, string> = { 1: "16px", 2: "14px", 3: "13px" };
          return (
            <div key={i} style={{ fontWeight: 600, fontSize: sizes[block.level ?? 1] ?? "13px", margin: "10px 0 4px", color: "var(--t0)" }}>
              {renderInline(block.text)}
            </div>
          );
        }
        if (block.type === "ul") {
          return (
            <ul key={i} style={{ margin: "4px 0 4px 20px", padding: 0 }}>
              {block.items!.map((item, j) => (
                <li key={j} style={{ marginBottom: 2 }}>{renderInline(item)}</li>
              ))}
            </ul>
          );
        }
        if (block.type === "ol") {
          return (
            <ol key={i} style={{ margin: "4px 0 4px 20px", padding: 0 }}>
              {block.items!.map((item, j) => (
                <li key={j} style={{ marginBottom: 2 }}>{renderInline(item)}</li>
              ))}
            </ol>
          );
        }
        if (block.type === "task") {
          return (
            <ul key={i} style={{ display: "grid", gap: 4, listStyle: "none", margin: "6px 0", padding: 0 }}>
              {block.tasks!.map((task, j) => (
                <li key={j} style={{ alignItems: "flex-start", display: "flex", gap: 8 }}>
                  <span
                    aria-label={task.checked ? "Completed task" : "Incomplete task"}
                    role="img"
                    style={{
                      alignItems: "center",
                      background: task.checked ? "var(--teal-soft)" : "var(--bg3)",
                      border: `1px solid ${task.checked ? "var(--teal-border)" : "var(--border)"}`,
                      borderRadius: 4,
                      color: task.checked ? "var(--teal)" : "transparent",
                      display: "inline-flex",
                      flex: "0 0 auto",
                      fontSize: 10,
                      height: 14,
                      justifyContent: "center",
                      marginTop: 4,
                      width: 14,
                    }}
                  >
                    x
                  </span>
                  <span>{renderInline(task.text)}</span>
                </li>
              ))}
            </ul>
          );
        }
        if (block.type === "table") {
          return (
            <div key={i} style={{ border: "1px solid var(--border)", borderRadius: "var(--radius-sm)", margin: "8px 0", overflowX: "auto" }}>
              <table style={{ borderCollapse: "collapse", minWidth: "100%", fontSize: 12 }}>
                <thead>
                  <tr>
                    {block.table!.headers.map((header, j) => (
                      <th key={j} style={{ background: "var(--bg3)", borderBottom: "1px solid var(--border)", color: "var(--t1)", fontWeight: 600, padding: "6px 8px", textAlign: "left", whiteSpace: "nowrap" }}>
                        {renderInline(header)}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {block.table!.rows.map((row, j) => (
                    <tr key={j}>
                      {block.table!.headers.map((_, k) => (
                        <td key={k} style={{ borderTop: j === 0 ? "none" : "1px solid var(--border)", color: "var(--t0)", padding: "6px 8px", verticalAlign: "top" }}>
                          {renderInline(row[k] ?? "")}
                        </td>
                      ))}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          );
        }
        if (block.type === "hr") {
          return <hr key={i} style={{ border: "none", borderTop: "1px solid var(--border)", margin: "10px 0" }} />;
        }
        // paragraph
        return (
          <p key={i} style={{ margin: "0 0 6px", whiteSpace: "pre-wrap" }}>
            {renderInline(block.text)}
          </p>
        );
      })}
    </div>
  );
}

function renderInline(text: string): React.ReactNode {
  return parseInlineNodes(text).map((node, i) => {
    if (node.t === "bold") return <strong key={i}>{node.v}</strong>;
    if (node.t === "italic") return <em key={i}>{node.v}</em>;
    if (node.t === "code") return (
      <code key={i} style={{ fontFamily: "var(--font-mono)", fontSize: "0.9em", background: "var(--bg3)", padding: "1px 4px", borderRadius: "var(--radius-sm)", color: "var(--teal)" }}>
        {node.v}
      </code>
    );
    if (node.t === "link") {
      return (
        <a
          key={i}
          href={safeMarkdownHref(node.href)}
          rel="noreferrer"
          target="_blank"
          style={{ color: "var(--teal)", textDecoration: "none", borderBottom: "1px solid var(--teal-border)" }}
        >
          {node.v}
        </a>
      );
    }
    return node.v;
  });
}

function safeMarkdownHref(href: string): string {
  if (/^https?:\/\//i.test(href) || /^mailto:/i.test(href)) {
    return href;
  }
  return "#";
}
