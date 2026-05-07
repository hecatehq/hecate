import { useEffect, useRef, useState } from "react";
import type { SyntheticEvent } from "react";
import type { RuntimeConsoleViewModel } from "../../app/useRuntimeConsole";
import { discoverLocalProviders } from "../../lib/api";
import { describeGatewayError, formatErrorCode } from "../../lib/error-diagnostics";
import { buildSelectedModelIssue } from "../../lib/provider-issues";
import { describeCredentialState, describeHealthErrorClass, describeRoutingBlockedReason } from "../../lib/runtime-utils";
import type { SelectedModelIssue } from "../../lib/provider-issues";
import type { AgentAdapterRecord, AgentChatActivityRecord, AgentChatSegmentRecord, AgentChatSessionRecord, AgentChatTimingRecord, AgentChatUsageRecord, LocalProviderDiscoveryRecord, ProviderPresetRecord } from "../../types/runtime";
import { CompactProviderReadinessChecks } from "../shared/ProviderReadiness";
import { AgentAdapterPicker, CodeBlock, Icon, Icons, InlineError, ModelPicker, ProviderPicker } from "../shared/ui";
import { TranscriptMessageRow } from "../transcript/TranscriptMessageRow";
import { AgentApprovalAutoModeBanner, AgentApprovalsBanner } from "./AgentApprovalBanner";
import { AgentApprovalModal } from "./AgentApprovalModal";
import { AddProviderModal } from "../providers/AddProviderModal";

type Props = {
  state: RuntimeConsoleViewModel["state"];
  actions: RuntimeConsoleViewModel["actions"];
  onNavigate?: (workspace: "providers" | "runs" | "overview") => void;
  onOpenTask?: (taskID: string, runID?: string) => void;
  onOpenTrace?: (requestID: string) => void;
};

type VisibleChatMessage = {
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
  activities?: AgentChatActivityRecord[];
  usage?: AgentChatUsageRecord;
  timing?: AgentChatTimingRecord;
  duration_ms?: number;
  error?: string;
};

type TranscriptItem =
  | { type: "segment"; key: string; segment: AgentChatSegmentRecord }
  | { type: "message"; key: string; message: VisibleChatMessage };

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

type HecateTaskApproval = {
  approvalID: string;
  title: string;
  kind?: string;
  detail?: string;
  createdAt?: string;
};

export function ChatView({ state, actions, onNavigate, onOpenTask, onOpenTrace }: Props) {
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
  const [addProviderOpen, setAddProviderOpen] = useState(false);
  const [workspacePathValue, setWorkspacePathValue] = useState("");
  const [sidebarQuery, setSidebarQuery] = useState("");
  const [quickLocalProviders, setQuickLocalProviders] = useState<LocalProviderDiscoveryRecord[]>([]);
  const [quickLocalLoading, setQuickLocalLoading] = useState(false);
  const [quickLocalError, setQuickLocalError] = useState("");
  const [quickAddingProviders, setQuickAddingProviders] = useState(false);
  const [taskApprovalBusyID, setTaskApprovalBusyID] = useState("");
  const [capabilitySaving, setCapabilitySaving] = useState(false);
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

  const isHecateChat = state.chatTarget === "agent" || state.chatTarget === "model";
  const isAgentChat = isHecateChat || state.chatTarget === "external_agent";
  const isHecateAgentChat = state.chatTarget === "agent";
  const isExternalAgentChat = state.chatTarget === "external_agent";
  const sessions: SidebarSession[] = isAgentChat
      ? (state.agentChatSessions ?? []).map((s) => ({
        id: s.id,
        title: s.title,
        message_count: s.message_count,
        provider_call_count: 0,
        last_provider: s.runtime_kind === "external_agent" || s.adapter_id ? s.adapter_id : s.provider,
        last_model: s.runtime_kind === "external_agent" || s.adapter_id ? s.status : s.model,
        created_at: s.created_at,
        updated_at: s.updated_at,
      }))
    : (state.chatSessions ?? []);
  const filteredSessions = filterSidebarSessions(sessions, sidebarQuery);
  const groupedSessions = groupSidebarSessions(filteredSessions);
  const activeSessionID = isAgentChat ? state.activeAgentChatSessionID : state.activeChatSessionID;
  const activeQueuedChatMessages = isAgentChat && activeSessionID
    ? state.queuedChatMessages.filter((queued) => queued.session_id === activeSessionID)
    : [];
  const activeTitle = isAgentChat
    ? state.activeAgentChatSession?.title
    : state.activeChatSession?.title;
  const messages: VisibleChatMessage[] = isAgentChat
    ? (state.activeAgentChatSession?.messages ?? []).map((m, index) => ({
        id: m.id || `agent-message-${index}`,
        runtime_kind: m.runtime_kind,
        segment_id: m.segment_id,
        task_id: m.task_id,
        run_id: m.run_id,
        request_id: m.request_id,
        trace_id: m.trace_id,
        native_session_id: m.native_session_id,
        role: m.role,
        content: m.content,
        created_at: m.created_at,
        agent_adapter_id: m.adapter_id,
        agent_adapter_name: m.adapter_name,
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
      }))
    : (state.activeChatSession?.messages ?? []).map((m) => ({
        id: m.id,
        role: m.role,
        content: m.content,
        created_at: m.created_at,
        produced_by_call_id: m.produced_by_call_id,
      }));
  const providerCalls = isAgentChat ? [] : (state.activeChatSession?.provider_calls ?? []);
  const pendingTaskApprovals = isHecateAgentChat
    ? pendingHecateTaskApprovals(state.activeAgentChatSession)
    : [];
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
  const transcriptItems = buildTranscriptItems(
    visibleMessages,
    state.activeAgentChatSession?.segments,
    isHecateChat,
  );
  const streaming = state.chatLoading;
  const chatDiagnostic = describeGatewayError(state.chatErrorCode, state.chatErrorStatus ?? undefined);
  const activeAgentAdapterID = state.activeAgentChatSession?.adapter_id || state.agentAdapterID;
  const selectedAgent = state.agentAdapters.find((adapter) => adapter.id === activeAgentAdapterID);
  const availableAgents = state.agentAdapters.filter((adapter) => adapter.available);
  const configuredProviders = state.settingsConfig?.providers ?? [];
  const providerConfigLoaded = state.settingsConfig !== null;
  const selectableModels = (() => {
    // Scope the model list to providers the operator has explicitly
    // configured. The /v1/models endpoint may return models from
    // env-driven providers too, but those aren't routable from Chats
    // unless the settings store knows about them.
    if (!providerConfigLoaded) return state.providerScopedModels;
    if (configuredProviders.length === 0) return [];
    const ids = new Set(configuredProviders.map(c => c.id));
    return state.providerScopedModels.filter(m => {
      const provider = m.metadata?.provider;
      return typeof provider === "string" ? ids.has(provider) : true;
    });
  })();
  const modelRouteUnavailable = providerConfigLoaded && selectableModels.length === 0;
  const hasConfiguredProviders = configuredProviders.length > 0;
  const selectedConfiguredProvider = state.providerFilter === "auto"
    ? configuredProviders.length === 1 ? configuredProviders[0] : undefined
    : configuredProviders.find(provider => provider.id === state.providerFilter);
  const selectedRuntimeProvider = state.providerFilter === "auto"
    ? state.providers.length === 1 ? state.providers[0] : undefined
    : state.providers.find(provider => provider.name === state.providerFilter);
  const agentRouteUnavailable = availableAgents.length === 0;
  const selectedAgentUnavailable = isExternalAgentChat && Boolean(selectedAgent) && !selectedAgent?.available;
  const nothingRunnable = !state.loading && modelRouteUnavailable && agentRouteUnavailable;
  const agentPickerLocked = isExternalAgentChat && Boolean(state.activeAgentChatSessionID);
  const activeHecateAgentSegment = activeTaskBackedHecateSegment(state.activeAgentChatSession);
  const hecateAgentBusy = isHecateChat && Boolean(activeHecateAgentSegment);
  const activeHecateTaskID = activeHecateAgentSegment?.task_id || "";
  const activeHecateRunID = activeHecateAgentSegment?.latest_run_id || "";
  const hecateAgentModelLocked = isHecateChat && Boolean(activeHecateAgentSegment);
  const hecateChatProviderValue = hecateAgentModelLocked
    ? (activeHecateAgentSegment?.provider || state.activeAgentChatSession?.provider || "auto")
    : state.providerFilter;
  const hecateChatModelValue = hecateAgentModelLocked
    ? (activeHecateAgentSegment?.model || state.activeAgentChatSession?.model || "")
    : state.model;
  const selectedHecateModelRecord = hecateAgentModelLocked
    ? undefined
    : selectableModels.find((entry) => entry.id === state.model && (!state.providerFilter || state.providerFilter === "auto" || entry.metadata?.provider === state.providerFilter));
  const selectedModelIssue = !hecateAgentModelLocked && providerConfigLoaded && state.model && selectableModels.length > 0
    ? buildSelectedModelIssue({
        model: state.model,
        providerFilter: state.providerFilter,
        selectableModels,
        configuredProvider: selectedConfiguredProvider,
        runtimeProvider: selectedRuntimeProvider,
      })
    : null;
  const selectedModelCapabilities = hecateAgentModelLocked
    ? state.activeAgentChatSession?.capabilities
    : selectedHecateModelRecord?.metadata?.capabilities;
  const hecateAgentToolsDisabledForModel = selectedModelCapabilities?.tool_calling === "none";
  const selectedCapabilityProvider = hecateAgentModelLocked
    ? ""
    : (state.providerFilter !== "auto" ? state.providerFilter : selectedHecateModelRecord?.metadata?.provider ?? "");
  const selectedCapabilityModel = hecateAgentModelLocked ? "" : state.model;
  const hecateChatModelReady = isHecateAgentChat && hecateAgentModelLocked
    ? Boolean(hecateChatModelValue)
    : Boolean(state.model) && !modelRouteUnavailable && !selectedModelIssue;
  const composerVisible = isExternalAgentChat || (isHecateChat && hecateChatModelReady);
  const agentBusy = isAgentChat && (streaming || hecateAgentBusy);
  const queueingMessage = agentBusy && Boolean(state.message.trim());
  const sendDisabled = !state.message.trim()
    || (!agentBusy && streaming)
    || (!isAgentChat && modelRouteUnavailable)
    || (!agentBusy && isExternalAgentChat && (!state.agentWorkspace.trim() || !selectedAgent?.available))
    || (!agentBusy && isHecateAgentChat && (!state.agentWorkspace.trim() || !hecateChatModelReady || hecateAgentToolsDisabledForModel));

  async function enableToolsForSelectedModel() {
    if (!selectedCapabilityProvider || !selectedCapabilityModel || capabilitySaving) {
      return;
    }
    setCapabilitySaving(true);
    try {
      await actions.upsertModelCapabilityOverride({
        provider: selectedCapabilityProvider,
        model: selectedCapabilityModel,
        tool_calling: "basic",
        streaming: selectedModelCapabilities?.streaming,
        max_context_tokens: selectedModelCapabilities?.max_context_tokens,
        note: "Tools enabled from Hecate Chat.",
      });
    } finally {
      setCapabilitySaving(false);
    }
  }

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

  useEffect(() => {
    if (!isHecateChat || !modelRouteUnavailable || hasConfiguredProviders || quickLocalProviders.length > 0 || quickLocalLoading) return;
    void refreshQuickLocalProviders();
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isHecateChat, modelRouteUnavailable, hasConfiguredProviders]);

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

  async function refreshQuickLocalProviders() {
    setQuickLocalLoading(true);
    setQuickLocalError("");
    try {
      const response = await discoverLocalProviders();
      setQuickLocalProviders((response.data ?? []).filter(isQuickAddableLocalProvider));
    } catch (error) {
      setQuickLocalError(error instanceof Error ? error.message : "Failed to check local providers");
    } finally {
      setQuickLocalLoading(false);
    }
  }

  async function quickAddLocalProviders(discoveries: LocalProviderDiscoveryRecord[]) {
    if (quickAddingProviders) return;
    const seenBaseURLs = new Set<string>();
    const addable = discoveries
      .map(discovery => ({ discovery, preset: state.providerPresets.find(p => p.id === discovery.preset_id) }))
      .filter((entry): entry is { discovery: LocalProviderDiscoveryRecord; preset: ProviderPresetRecord } => Boolean(entry.preset))
      .filter(({ discovery, preset }) => {
        const baseURL = normalizeProviderBaseURL(discovery.base_url || preset.base_url);
        if (!baseURL) return true;
        if (seenBaseURLs.has(baseURL)) return false;
        seenBaseURLs.add(baseURL);
        return true;
      });
    if (addable.length === 0) return;

    setQuickAddingProviders(true);
    setQuickLocalError("");
    let createdCount = 0;
    let firstError: unknown = null;
    try {
      for (const { discovery, preset } of addable) {
        try {
          await actions.createProvider({
            name: preset.name,
            preset_id: preset.id,
            base_url: discovery.base_url || preset.base_url,
            kind: preset.kind,
            protocol: preset.protocol ?? "openai",
          }, { refresh: false });
          createdCount++;
        } catch (error) {
          firstError ??= error;
        }
      }
      if (createdCount > 0) {
        try {
          await actions.loadDashboard();
        } catch (error) {
          firstError ??= error;
        }
      }
      if (firstError) {
        setQuickLocalError(firstError instanceof Error ? firstError.message : "Some detected providers could not be added");
      }
    } finally {
      setQuickAddingProviders(false);
    }
  }

  async function handleResolveTaskApproval(approvalID: string, decision: "approve" | "reject") {
    const taskID = state.activeAgentChatSession?.task_id;
    if (!taskID) return;
    setTaskApprovalBusyID(`${approvalID}:${decision}`);
    try {
      await actions.resolveTaskApproval(taskID, approvalID, { decision });
    } finally {
      setTaskApprovalBusyID("");
    }
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
              type="button"
              onClick={() => {
                actions.createChatSession();
                textareaRef.current?.focus();
              }}
            >
              <Icon d={Icons.plus} size={13} /> New chat
            </button>
            <button className="btn btn-ghost btn-sm" onClick={() => setSidebarOpen(false)} title="Close" aria-label="Close chats sidebar" type="button">
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
                    role="button"
                    tabIndex={renamingId === s.id ? -1 : 0}
                    aria-current={(activeSessionID === s.id) ? "true" : undefined}
                    aria-label={`Chat ${s.title || "Untitled"}`}
                    onClick={() => {
                      if (renamingId === s.id) return;
                      void actions.selectChatSession(s.id);
                      textareaRef.current?.focus();
                    }}
                    onKeyDown={e => {
                      if (e.target !== e.currentTarget) return;
                      if (renamingId === s.id) return;
                      if (e.key !== "Enter" && e.key !== " ") return;
                      e.preventDefault();
                      void actions.selectChatSession(s.id);
                      textareaRef.current?.focus();
                    }}
                    onFocus={() => setHoveredChatId(s.id)}
                    onBlur={e => {
                      const nextFocus = e.relatedTarget;
                      if (!(nextFocus instanceof Node) || !e.currentTarget.contains(nextFocus)) {
                        setHoveredChatId(null);
                      }
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
                                aria-label={`Rename chat ${s.title || "Untitled"}`}
                                type="button"
                                onClick={e => { e.stopPropagation(); setRenamingId(s.id); setRenameValue(s.title || ""); }}
                                style={{ padding: "1px 3px" }}
                                title="Rename"
                              >
                                <Icon d={Icons.edit} size={10} />
                              </button>
                            )}
                            <button
                              className="btn btn-ghost btn-sm"
                              aria-label={`Delete chat ${s.title || "Untitled"}`}
                              type="button"
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
            <button className="btn btn-ghost btn-sm" onClick={() => setSidebarOpen(true)} title="Open chats" aria-label="Open chats sidebar" type="button">
              <Icon d={Icons.chevR} size={13} />
            </button>
          )}
          <div style={{ display: "flex", border: "1px solid var(--border)", borderRadius: "var(--radius-sm)", overflow: "hidden", flexShrink: 0 }}>
            {([
              ["hecate", "Hecate Chat", "Chat with a selected model; enable tools to use Hecate's task runtime"],
              ["external_agent", "External Agent", "Chat with Codex, Claude Code, Cursor, or another external agent"],
            ] as const).map(([target, label, title]) => (
              <button
                key={target}
                className="btn btn-ghost btn-sm"
                type="button"
                aria-pressed={target === "hecate" ? isHecateChat : state.chatTarget === target}
                onClick={() => {
                  if (target === "hecate") {
                    if (!isHecateChat) actions.setChatTarget("agent");
                    return;
                  }
                  actions.setChatTarget(target);
                }}
                style={{
                  borderRadius: 0,
                  background: (target === "hecate" ? isHecateChat : state.chatTarget === target) ? "var(--teal-bg)" : "transparent",
                  color: (target === "hecate" ? isHecateChat : state.chatTarget === target) ? "var(--teal)" : "var(--t2)",
                  border: 0,
                }}
                title={title}
              >
                {label}
              </button>
            ))}
          </div>
          {isHecateChat && (
            <HecateToolsToggle
              enabled={isHecateAgentChat}
              toolsDisabledForModel={hecateAgentToolsDisabledForModel}
              onChange={(enabled) => actions.setChatTarget(enabled ? "agent" : "model")}
            />
          )}
          <span style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)", flex: 1, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            {activeTitle || (sessions.length === 0 ? "New chat" : "Select a chat")}
          </span>
          {isExternalAgentChat && (
            <span
              title={formatAgentSessionTitle(state.activeAgentChatSession, selectedAgent)}
              style={{ flexShrink: 0, fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)", maxWidth: 260, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
            >
              {formatAgentSessionLabel(state.activeAgentChatSession, selectedAgent)}
            </span>
          )}
          {isExternalAgentChat ? (
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
              {hecateAgentModelLocked ? (
                <LockedHecateModelSnapshot
                  provider={providerLabelForHecateChat(state, hecateChatProviderValue)}
                  model={hecateChatModelValue}
                />
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
                      const configured = state.settingsConfig?.providers ?? [];
                      const source = configured.length > 0
                        ? configured.map(c => ({ id: c.id, name: c.name, kind: c.kind }))
                        : state.providers
                            .filter(p => p.name)
                            .map(p => ({ id: p.name, name: p.name, kind: state.providerPresets.find(pr => pr.id === p.name)?.kind }));

                      return source
                        .map(p => {
                          const cfg = state.settingsConfig?.providers.find(c => c.id === p.id);
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
                    // pre-filled vars), but those aren't in settingsConfig.providers
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
                      for (const cfg of state.settingsConfig?.providers ?? []) {
                        if (cfg.kind === "cloud" && !cfg.credential_configured) {
                          out.set(cfg.id, `Add an API key for ${cfg.name || cfg.id} on the Providers tab`);
                        }
                      }
                      return out;
                    })()}
                  />
                </>
              )}
              {!isHecateAgentChat && (
                <button className="btn btn-ghost btn-sm" onClick={() => setSyspromptOpen(o => !o)}
                  style={{ color: syspromptOpen ? "var(--teal)" : "var(--t2)" }} title="System prompt">
                  <Icon d={Icons.edit} size={13} />
                  <span style={{ fontSize: 11 }}>system</span>
                </button>
              )}
            </>
          )}
          {isHecateAgentChat && (
            <>
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
        {state.chatTarget === "model" && syspromptOpen && (
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
        {isExternalAgentChat && (
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

        {isHecateAgentChat && state.activeAgentChatSession?.task_id && pendingTaskApprovals.length > 0 && (
          <HecateTaskApprovalsBanner
            approvals={pendingTaskApprovals}
            taskID={state.activeAgentChatSession.task_id}
            runID={state.activeAgentChatSession.latest_run_id}
            busyID={taskApprovalBusyID}
            onOpenTask={onOpenTask}
            onResolve={handleResolveTaskApproval}
          />
        )}

        {/* Messages */}
        <div style={{ flex: 1, overflow: "hidden", position: "relative" }}>
        <div ref={scrollRef} onScroll={handleScroll} style={{ height: "100%", overflowY: "auto", padding: "16px 0" }}>
          {transcriptItems.map(item => {
            if (item.type === "segment") {
              return <ChatSegmentDivider key={item.key} segment={item.segment} />;
            }
            const m = item.message;
            const call = m.produced_by_call_id ? callsByID.get(m.produced_by_call_id) : undefined;
            const role = m.role === "assistant" ? "assistant" : "user";
            const content = typeof m.content === "string" ? m.content : (m.content === null ? "" : JSON.stringify(m.content));
            const time = m.created_at ? new Date(m.created_at).toLocaleTimeString("en-US", { hour: "2-digit", minute: "2-digit" }) : "";
            const agentModel = isHecateAgentChat
              ? (m.model || state.activeAgentChatSession?.model || "Hecate Agent")
              : (m.agent_adapter_name || m.agent_adapter_id);
            const agentRuntime = isAgentChat && role === "assistant"
              ? formatAgentRuntimeMeta(m.run_id, m.duration_ms, m.native_session_id)
              : "";
            const taskID = m.runtime_kind === "agent" ? m.task_id : "";
            const taskRunID = taskID ? m.run_id : "";
            const traceRequestID = isAgentChat ? m.request_id : call?.request_id;
            const traceID = isAgentChat ? m.trace_id : undefined;
            return (
              <TranscriptMessageRow
                key={item.key}
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
                taskLink={isHecateAgentChat && role === "assistant" && taskID
                  ? {
                      label: formatTaskLinkLabel(taskID),
                      title: formatTaskLinkTitle(taskID, taskRunID),
                      onClick: () => {
                        if (!taskID) return;
                        if (onOpenTask) onOpenTask(taskID, taskRunID);
                        else onNavigate?.("runs");
                      },
                    }
                  : undefined}
                traceLink={role === "assistant" && traceRequestID
                  ? {
                      label: formatTraceLinkLabel(traceRequestID),
                      title: formatTraceLinkTitle(traceRequestID, traceID),
                      onClick: () => {
                        if (onOpenTrace) onOpenTrace(traceRequestID);
                        else onNavigate?.("overview");
                      },
                    }
                  : undefined}
                activities={isAgentChat && role === "assistant" ? m.activities : undefined}
                diffStat={isAgentChat && role === "assistant" ? m.diff_stat : undefined}
                diff={isAgentChat && role === "assistant" ? m.diff : undefined}
                agentSessionID={isAgentChat ? state.activeAgentChatSessionID : ""}
                onListAgentFiles={actions.listAgentChatMessageFiles}
                onGetAgentFileDiff={actions.getAgentChatMessageFileDiff}
                onRevertAgentFiles={actions.revertAgentChatMessageFiles}
                rawOutput={isAgentChat && role === "assistant" ? m.raw_output : undefined}
                agentUsage={isAgentChat && role === "assistant" ? m.usage : undefined}
                agentTiming={isAgentChat && role === "assistant" ? m.timing : undefined}
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
                onClick={() => void actions.submitToolResults()}
                type="button">
                {state.chatLoading ? "Running…" : "Submit results"}
              </button>
            </div>
          )}

          {visibleMessages.length === 0 && !streaming && state.pendingToolCalls.length === 0 && (
            <ChatEmptyState
              isAgentChat={isAgentChat}
              isHecateChat={isHecateChat}
              isExternalAgentChat={isExternalAgentChat}
              modelRouteUnavailable={modelRouteUnavailable}
              selectedModelIssue={selectedModelIssue}
              agentRouteUnavailable={isExternalAgentChat && agentRouteUnavailable}
              nothingRunnable={nothingRunnable}
              agentAdapters={state.agentAdapters}
              selectedAgent={selectedAgent}
              selectedAgentUnavailable={selectedAgentUnavailable}
              hasConfiguredProviders={hasConfiguredProviders}
              providerFilter={state.providerFilter}
              selectedConfiguredProvider={selectedConfiguredProvider}
              selectedRuntimeProvider={selectedRuntimeProvider}
              providerPresets={state.providerPresets}
              quickLocalProviders={quickLocalProviders}
              quickLocalLoading={quickLocalLoading}
              quickLocalError={quickLocalError}
              quickAddingProviders={quickAddingProviders}
              onAddProvider={() => setAddProviderOpen(true)}
              onQuickAddLocalProviders={quickAddLocalProviders}
              onRefreshQuickLocalProviders={refreshQuickLocalProviders}
              onSwitchTarget={actions.setChatTarget}
            />
          )}
          <div ref={bottomRef} />
        </div>

        {!atBottom && (
          <button type="button" aria-label="Scroll to bottom" onClick={scrollToBottom} style={{
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

        {(composerVisible || state.chatError || selectedModelIssue) && (
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
              <SelectedModelReadinessNotice issue={selectedModelIssue} onOpenProviders={() => onNavigate?.("providers")} />
            </div>
          )}
          {composerVisible && (
          <>
          {isHecateAgentChat && hecateChatModelValue && hecateAgentToolsDisabledForModel && (
            <div style={{
              maxWidth: 820,
              margin: "0 auto 8px",
              display: "flex",
              alignItems: "center",
              justifyContent: "space-between",
              gap: 12,
              fontSize: 12,
              color: "var(--amber)",
              lineHeight: 1.45,
            }}>
              <span>
                Tools are disabled for this model in Settings. Turn tools off for direct chat, or enable tools in Model capabilities.
              </span>
              <button
                type="button"
                className="btn btn-ghost btn-sm"
                onClick={() => void enableToolsForSelectedModel()}
                disabled={!selectedCapabilityProvider || !selectedCapabilityModel || capabilitySaving}
                title={selectedCapabilityProvider ? `Enable tools for ${selectedCapabilityProvider}/${selectedCapabilityModel}` : "Choose a concrete provider before enabling tools"}
                style={{ flexShrink: 0, color: "var(--amber)", borderColor: "rgba(245, 191, 79, 0.35)" }}
              >
                {capabilitySaving ? "Saving..." : "Enable tools"}
              </button>
            </div>
          )}
          {isAgentChat && !state.agentWorkspace.trim() && (
            <div style={{
              maxWidth: 820,
              margin: "0 auto 8px",
              display: "flex",
              alignItems: "center",
              justifyContent: "space-between",
              gap: 12,
              fontSize: 12,
              color: "var(--amber)",
              lineHeight: 1.45,
            }}>
              <span>
                Choose a workspace before sending. Hecate uses it as the working directory for this chat.
              </span>
              <button
                type="button"
                className="btn btn-ghost btn-sm"
                onClick={chooseWorkspace}
                style={{ flexShrink: 0, color: "var(--amber)", borderColor: "rgba(245, 191, 79, 0.35)" }}
              >
                Choose workspace
              </button>
            </div>
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
                  <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                    {queued.content}
                  </span>
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
          <div style={{ maxWidth: 820, margin: "0 auto", position: "relative" }}>
            <textarea
              ref={textareaRef}
              aria-label="Message"
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
            {agentBusy && !queueingMessage ? (
              <button type="button"
                className="btn btn-danger"
                aria-label="Stop current run"
                disabled={state.agentChatCancelling}
                title={state.agentChatCancelling ? "Stopping..." : "Stop current run"}
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
                  ? "External Agent is running. New messages will queue until it finishes."
                  : "Hecate Chat is working on this task. New messages will queue until the active run finishes."}
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
                  title={state.agentChatCancelling ? "Stopping..." : isExternalAgentChat ? "Stop external agent" : "Stop active task"}
                  onClick={actions.cancelAgentChat}
                  disabled={state.agentChatCancelling}
                  style={{ fontFamily: "var(--font-mono)", fontSize: 10, padding: "2px 6px", color: "var(--danger)" }}
                >
                  Stop
                </button>
              </span>
            </div>
          )}
          {isAgentChat && state.agentChatCancelling && (
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
        )}
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
      <AddProviderModal
        open={addProviderOpen}
        state={state}
        actions={actions}
        onClose={() => setAddProviderOpen(false)}
      />
    </div>
  );
}

function buildTranscriptItems(
  messages: VisibleChatMessage[],
  segments: AgentChatSegmentRecord[] | undefined,
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

function segmentFromMessage(message: VisibleChatMessage, segmentID: string): AgentChatSegmentRecord {
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

function ChatSegmentDivider({ segment }: { segment: AgentChatSegmentRecord }) {
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
      <div style={{ height: 1, flex: 1, background: "linear-gradient(90deg, transparent, var(--border))" }} />
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
        <span style={{ color: "var(--t1)", fontSize: 12, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>
          {description.title}
        </span>
        {description.meta && (
          <span style={{ color: "var(--t3)", fontFamily: "var(--font-mono)", fontSize: 10, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>
            {description.meta}
          </span>
        )}
      </div>
      <div style={{ height: 1, flex: 1, background: "linear-gradient(90deg, var(--border), transparent)" }} />
    </div>
  );
}

function describeChatSegment(segment: AgentChatSegmentRecord): { kicker: string; title: string; meta: string; label: string; tone: "on" | "off" | "external" } {
  const model = segment.model || "selected model";
  const provider = segment.provider && segment.provider !== "auto" ? segment.provider : "";
  switch (segment.runtime_kind) {
    case "agent": {
      const meta = [provider, segment.task_id ? formatTaskLinkLabel(segment.task_id) : "", segment.status].filter(Boolean).join(" · ");
      return {
        kicker: "Tools on",
        title: model,
        meta,
        label: `Tools on segment using ${model}`,
        tone: "on",
      };
    }
    case "external_agent": {
      const meta = [segment.status, segment.workspace ? "workspace" : ""].filter(Boolean).join(" · ");
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

function pendingHecateTaskApprovals(session: AgentChatSessionRecord | null): HecateTaskApproval[] {
  if (!session?.task_id) return [];
  const byID = new Map<string, HecateTaskApproval>();
  for (const message of session.messages ?? []) {
    for (const activity of message.activities ?? []) {
      if (activity.type !== "approval") continue;
      const approvalID = activity.approval_id || parseProjectedTaskApprovalID(activity.id);
      if (!approvalID) continue;
      const status = activity.status || "";
      if (isResolvedTaskApprovalStatus(status)) {
        byID.delete(approvalID);
        continue;
      }
      const pending = activity.needs_action || status === "pending" || status === "awaiting_approval";
      if (!pending) {
        byID.delete(approvalID);
        continue;
      }
      byID.set(approvalID, {
        approvalID,
        title: activity.title || activity.kind || "Approval required",
        kind: activity.kind,
        detail: cleanApprovalDetail(activity.detail),
        createdAt: activity.created_at,
      });
    }
  }
  return [...byID.values()].sort((a, b) => (a.createdAt || "").localeCompare(b.createdAt || ""));
}

function cleanApprovalDetail(detail?: string): string {
  return (detail || "")
    .replace(/\s+-\s+awaiting_approval$/i, "")
    .replace(/\s+-\s+pending$/i, "")
    .trim();
}

function hecateAgentSessionIsActive(status?: string): boolean {
  return status === "queued" || status === "running" || status === "awaiting_approval";
}

function activeTaskBackedHecateSegment(session: AgentChatSessionRecord | null): AgentChatSegmentRecord | null {
  const segments = [...(session?.segments ?? [])].reverse();
  const activeSegment = segments.find((segment) =>
    segment.runtime_kind === "agent"
    && Boolean(segment.task_id)
    && hecateAgentSessionIsActive(segment.status),
  );
  if (activeSegment) {
    return activeSegment;
  }
  if (session?.task_id && hecateAgentSessionIsActive(session.status)) {
    return {
      id: `task:${session.task_id}`,
      runtime_kind: "agent",
      provider: session.provider,
      model: session.model,
      task_id: session.task_id,
      latest_run_id: session.latest_run_id,
      workspace: session.workspace,
      status: session.status,
      message_count: 0,
      updated_at: session.updated_at,
    };
  }
  return null;
}

function parseProjectedTaskApprovalID(id?: string): string {
  const prefix = "task:approval:";
  if (!id?.startsWith(prefix)) return "";
  return id.slice(prefix.length);
}

function isResolvedTaskApprovalStatus(status: string): boolean {
  switch (status) {
    case "approved":
    case "rejected":
    case "denied":
    case "cancelled":
    case "timed_out":
      return true;
    default:
      return false;
  }
}

function formatTaskLinkLabel(taskID: string): string {
  return `Task ${compactID(taskID, ["task_"], 12)}`;
}

function formatTaskLinkTitle(taskID: string, runID?: string): string {
  return [
    `Open backing task ${taskID}`,
    runID ? `run ${runID}` : "",
  ].filter(Boolean).join(" · ");
}

function formatTraceLinkLabel(requestID: string): string {
  return `Trace ${requestID.slice(0, 8)}`;
}

function formatTraceLinkTitle(requestID: string, traceID?: string): string {
  return [
    `Open trace for request ${requestID}`,
    traceID ? `trace ${traceID}` : "",
  ].filter(Boolean).join(" · ");
}

function HecateToolsToggle({
  enabled,
  toolsDisabledForModel,
  onChange,
}: {
  enabled: boolean;
  toolsDisabledForModel: boolean;
  onChange: (enabled: boolean) => void;
}) {
  const toolsOnTitle = toolsDisabledForModel
    ? "Tools are disabled for this model in Settings. Enable them there or turn tools off for direct model chat."
    : "Use Hecate's task runtime with tools, approvals, artifacts, and telemetry.";
  return (
    <div
      role="group"
      aria-label="Hecate tools"
      style={{
        display: "inline-flex",
        alignItems: "center",
        border: "1px solid var(--border)",
        borderRadius: "999px",
        overflow: "hidden",
        flexShrink: 0,
        background: "var(--bg0)",
        height: 30,
      }}
    >
      <button
        type="button"
        className="btn btn-ghost btn-sm"
        aria-pressed={!enabled}
        onClick={() => onChange(false)}
        title="Chat directly with the selected model. No task run or tools."
        style={{
          border: 0,
          borderRadius: 0,
          width: 76,
          padding: "4px 0",
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          background: !enabled ? "var(--bg3)" : "transparent",
          color: !enabled ? "var(--t0)" : "var(--t3)",
          justifyContent: "center",
        }}
      >
        tools off
      </button>
      <button
        type="button"
        className="btn btn-ghost btn-sm"
        aria-pressed={enabled}
        onClick={() => onChange(true)}
        title={toolsOnTitle}
        style={{
          border: 0,
          borderLeft: "1px solid var(--border)",
          borderRadius: 0,
          width: 76,
          padding: "4px 0",
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          background: enabled ? "var(--teal-bg)" : "transparent",
          color: enabled ? (toolsDisabledForModel ? "var(--amber)" : "var(--teal)") : "var(--t3)",
          justifyContent: "center",
        }}
      >
        tools on
      </button>
    </div>
  );
}

function LockedHecateModelSnapshot({
  provider,
  model,
}: {
  provider: string;
  model: string;
}) {
  const title = "Provider and model are fixed for this Hecate Agent task. Turn tools off for direct model chat, or start a new chat to use a different tools-enabled model.";
  const sharedStyle = {
    fontFamily: "var(--font-mono)",
    fontSize: 11,
    gap: 5,
    color: "var(--t2)",
    opacity: 0.78,
    cursor: "not-allowed",
  } as const;
  return (
    <>
      <button
        type="button"
        className="btn btn-ghost btn-sm"
        disabled
        aria-label={`Fixed provider: ${provider}`}
        title={title}
        style={{ ...sharedStyle, width: 220 }}
      >
        <Icon d={Icons.providers} size={13} />
        <span style={{ flex: 1, minWidth: 0, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis", textAlign: "left" }}>
          {provider}
        </span>
      </button>
      <button
        type="button"
        className="btn btn-ghost btn-sm"
        disabled
        aria-label={`Fixed model: ${model || "model"}`}
        title={title}
        style={{ ...sharedStyle, width: 220 }}
      >
        <Icon d={Icons.model} size={13} />
        <span style={{ flex: 1, minWidth: 0, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis", textAlign: "left" }}>
          {model || "model"}
        </span>
      </button>
    </>
  );
}

function providerLabelForHecateChat(state: RuntimeConsoleViewModel["state"], providerID: string): string {
  if (!providerID || providerID === "auto") {
    return "All providers";
  }
  return state.settingsConfig?.providers.find(provider => provider.id === providerID)?.name
    || state.providerPresets.find(preset => preset.id === providerID)?.name
    || state.providers.find(provider => provider.name === providerID)?.name
    || providerID;
}

function HecateTaskApprovalsBanner({
  approvals,
  taskID,
  runID,
  busyID,
  onOpenTask,
  onResolve,
}: {
  approvals: HecateTaskApproval[];
  taskID: string;
  runID?: string;
  busyID: string;
  onOpenTask?: (taskID: string, runID?: string) => void;
  onResolve: (approvalID: string, decision: "approve" | "reject") => void;
}) {
  const visible = approvals.slice(0, 2);
  const overflow = approvals.length - visible.length;
  return (
    <div
      role="region"
      aria-label="Pending Hecate Agent task approvals"
      data-testid="hecate-task-approval-banner"
      style={{
        margin: "10px 16px 0",
        border: "1px solid var(--amber-border)",
        borderRadius: "var(--radius)",
        background: "var(--amber-bg)",
        overflow: "hidden",
        flexShrink: 0,
      }}
    >
      <div style={{
        padding: "8px 12px",
        borderBottom: "1px solid var(--amber-border)",
        display: "flex",
        alignItems: "center",
        gap: 8,
      }}>
        <Icon d={Icons.warning} size={14} />
        <span style={{ fontWeight: 500, color: "var(--amber)", fontSize: 12 }}>
          {approvals.length === 1 ? "Task approval required" : `${approvals.length} task approvals required`}
        </span>
        {onOpenTask && (
          <button
            type="button"
            className="btn btn-ghost btn-sm"
            onClick={() => onOpenTask(taskID, runID)}
            style={{ marginLeft: "auto" }}
          >
            Open Task
          </button>
        )}
      </div>
      {visible.map((approval) => {
        const approveBusy = busyID === `${approval.approvalID}:approve`;
        const rejectBusy = busyID === `${approval.approvalID}:reject`;
        const disabled = busyID !== "";
        const label = describeTaskApprovalKind(approval.kind || approval.title);
        return (
          <div
            key={approval.approvalID}
            style={{
              padding: "9px 12px",
              display: "grid",
              gridTemplateColumns: "minmax(0, 1fr) auto",
              gap: 12,
              alignItems: "center",
              borderTop: "1px solid var(--amber-border)",
            }}
          >
            <div style={{ minWidth: 0 }}>
              <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--amber)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                  {label}
                </span>
                {approval.createdAt && (
                  <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--amber-lo)" }}>
                    {new Date(approval.createdAt).toLocaleTimeString()}
                  </span>
                )}
              </div>
              {approval.detail && (
                <div style={{ fontSize: 11, color: "var(--amber-lo)", marginTop: 3, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                  {approval.detail}
                </div>
              )}
            </div>
            <div style={{ display: "flex", gap: 8 }}>
              <button
                type="button"
                className="btn btn-primary btn-sm"
                aria-label={`Approve ${label}`}
                disabled={disabled}
                onClick={() => onResolve(approval.approvalID, "approve")}
              >
                {approveBusy ? "Approving..." : "Approve"}
              </button>
              <button
                type="button"
                className="btn btn-danger btn-sm"
                aria-label={`Reject ${label}`}
                disabled={disabled}
                onClick={() => onResolve(approval.approvalID, "reject")}
              >
                {rejectBusy ? "Rejecting..." : "Reject"}
              </button>
            </div>
          </div>
        );
      })}
      {overflow > 0 && (
        <div style={{ padding: "7px 12px", borderTop: "1px solid var(--amber-border)", color: "var(--amber)", fontFamily: "var(--font-mono)", fontSize: 11 }}>
          + {overflow} more in the backing Task
        </div>
      )}
    </div>
  );
}

function describeTaskApprovalKind(kind: string): string {
  switch (kind) {
    case "shell_command":        return "Shell execution";
    case "git_exec":             return "Git execution";
    case "file_write":           return "File write";
    case "network_egress":       return "Network egress";
    case "agent_loop_tool_call": return "Agent tool call";
    default:                     return kind.replaceAll("_", " ");
  }
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

function ChatEmptyState({
  isAgentChat,
  isHecateChat,
  isExternalAgentChat,
  modelRouteUnavailable,
  selectedModelIssue,
  agentRouteUnavailable,
  nothingRunnable,
  agentAdapters,
  selectedAgent,
  selectedAgentUnavailable,
  hasConfiguredProviders,
  providerFilter,
  selectedConfiguredProvider,
  selectedRuntimeProvider,
  providerPresets,
  quickLocalProviders,
  quickLocalLoading,
  quickLocalError,
  quickAddingProviders,
  onAddProvider,
  onQuickAddLocalProviders,
  onRefreshQuickLocalProviders,
  onSwitchTarget,
}: {
  isAgentChat: boolean;
  isHecateChat: boolean;
  isExternalAgentChat: boolean;
  modelRouteUnavailable: boolean;
  selectedModelIssue: SelectedModelIssue | null;
  agentRouteUnavailable: boolean;
  nothingRunnable: boolean;
  agentAdapters: AgentAdapterRecord[];
  selectedAgent?: AgentAdapterRecord;
  selectedAgentUnavailable: boolean;
  hasConfiguredProviders: boolean;
  providerFilter: string;
  selectedConfiguredProvider?: NonNullable<RuntimeConsoleViewModel["state"]["settingsConfig"]>["providers"][number];
  selectedRuntimeProvider?: RuntimeConsoleViewModel["state"]["providers"][number];
  providerPresets: ProviderPresetRecord[];
  quickLocalProviders: LocalProviderDiscoveryRecord[];
  quickLocalLoading: boolean;
  quickLocalError: string;
  quickAddingProviders: boolean;
  onAddProvider: () => void;
  onQuickAddLocalProviders: (providers: LocalProviderDiscoveryRecord[]) => void;
  onRefreshQuickLocalProviders: () => void;
  onSwitchTarget: (target: "model" | "agent" | "external_agent") => void;
}) {
  const hecateModelUnavailable = isHecateChat && (modelRouteUnavailable || Boolean(selectedModelIssue));
  const title = isAgentChat && selectedAgentUnavailable
      ? `${selectedAgent?.name || "Selected agent"} is unavailable`
      : isExternalAgentChat && agentRouteUnavailable
      ? "No available coding agent"
      : nothingRunnable
        ? "Nothing runnable yet"
        : selectedModelIssue
          ? selectedModelIssue.title
        : hecateModelUnavailable
          ? "No routable model"
        : "Start a chat";
  const detail = isAgentChat && selectedAgentUnavailable
      ? `Hecate could not start ${selectedAgent?.name || "the selected agent"} because its CLI is not ready in this environment.`
      : isExternalAgentChat && agentRouteUnavailable
      ? "Hecate did not find any supported coding-agent CLI or local adapter runner in the known operator locations."
      : nothingRunnable
        ? "Add a model provider or install a supported coding-agent CLI before sending a message."
        : selectedModelIssue
          ? selectedModelIssue.message
        : hecateModelUnavailable
          ? "Add a provider with discovered models before sending through Hecate."
        : "Send a message to start this chat.";

  return (
    <div style={{ padding: "48px 16px", maxWidth: 820, margin: "0 auto", textAlign: "center" }}>
      <div style={{ fontSize: 13, fontWeight: 600, color: "var(--t1)", marginBottom: 5 }}>{title}</div>
      <div style={{ fontSize: 12, color: "var(--t3)", lineHeight: 1.5, maxWidth: 430, margin: "0 auto" }}>{detail}</div>
      {isAgentChat && (agentRouteUnavailable || selectedAgentUnavailable) && (
        <AgentSetupHints adapters={agentAdapters} selectedID={selectedAgent?.id} />
      )}
      {isHecateChat && modelRouteUnavailable && hasConfiguredProviders && (
        <ModelRouteTroubleshooting
          providerFilter={providerFilter}
          configuredProvider={selectedConfiguredProvider}
          runtimeProvider={selectedRuntimeProvider}
        />
      )}
      {isHecateChat && selectedModelIssue && (
        <SelectedModelReadinessNotice issue={selectedModelIssue} compact />
      )}
      {(modelRouteUnavailable || selectedModelIssue || agentRouteUnavailable) && (
        <div style={{ display: "flex", justifyContent: "center", gap: 8, marginTop: 14, flexWrap: "wrap" }}>
          {(modelRouteUnavailable || selectedModelIssue) && isHecateChat && (
            <button
              className="btn btn-primary btn-sm"
              onClick={onAddProvider}
              type="button"
              style={{ display: "flex", alignItems: "center", gap: 4 }}>
              <Icon d={Icons.plus} size={13} /> Add provider
            </button>
          )}
          {agentRouteUnavailable && !isAgentChat && (
            <button className="btn btn-ghost btn-sm" onClick={() => onSwitchTarget("external_agent")} type="button">
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
      {isHecateChat && modelRouteUnavailable && !hasConfiguredProviders && (
        <QuickLocalProviderAdd
          discoveries={quickLocalProviders}
          error={quickLocalError}
          loading={quickLocalLoading}
          presets={providerPresets}
          adding={quickAddingProviders}
          onAdd={onQuickAddLocalProviders}
          onRefresh={onRefreshQuickLocalProviders}
        />
      )}
    </div>
  );
}

function ModelRouteTroubleshooting({
  providerFilter,
  configuredProvider,
  runtimeProvider,
}: {
  providerFilter: string;
  configuredProvider?: NonNullable<RuntimeConsoleViewModel["state"]["settingsConfig"]>["providers"][number];
  runtimeProvider?: RuntimeConsoleViewModel["state"]["providers"][number];
}) {
  const providerName = providerFilter === "auto"
    ? "configured providers"
    : configuredProvider?.name || runtimeProvider?.name || providerFilter;
  const isLocal = configuredProvider?.kind === "local" || runtimeProvider?.kind === "local";
  const endpoint = runtimeProvider?.base_url || configuredProvider?.base_url || "";
  const modelCount = runtimeProvider?.model_count ?? runtimeProvider?.models?.length ?? 0;
  const blockedReason = runtimeProvider?.routing_blocked_reason ? describeRoutingBlockedReason(runtimeProvider.routing_blocked_reason) : "";
  const lastError = runtimeProvider?.last_error || "";
  const lastErrorClass = runtimeProvider?.last_error_class ? describeHealthErrorClass(runtimeProvider.last_error_class) : "";
  const discoverySource = runtimeProvider?.discovery_source || "";
  const rawCredentialState = runtimeProvider?.credential_state
    ?? (configuredProvider?.kind === "local"
      ? "not_required"
      : configuredProvider?.credential_configured
        ? "configured"
        : configuredProvider
          ? "missing"
          : undefined);
  const credentialState = describeCredentialState(rawCredentialState);
  const routingState = runtimeProvider?.routing_ready === false
    ? (blockedReason || "Blocked")
    : runtimeProvider?.routing_ready === true
      ? "Ready"
      : "Unknown";

  const guidance = isLocal
    ? [
        "Start the local provider app or server.",
        "Pull or load at least one model in that provider.",
        "Click Providers to confirm the endpoint and discovered model list.",
      ]
    : [
        "Check that credentials are configured for this provider.",
        "Confirm the account has access to at least one model.",
        "Click Providers to inspect the latest health and discovery error.",
      ];

  return (
    <div style={{
      margin: "14px auto 0",
      maxWidth: 560,
      border: "1px solid var(--border)",
      borderRadius: "var(--radius)",
      background: "var(--bg2)",
      padding: 12,
      textAlign: "left",
    }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 8 }}>
        <span style={{ fontSize: 11, color: "var(--t2)", fontFamily: "var(--font-mono)", textTransform: "uppercase", letterSpacing: "0.04em" }}>
          Provider is configured
        </span>
        <span style={{ fontSize: 11, color: "var(--t3)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          {providerName}
        </span>
      </div>
      <div style={{ fontSize: 12, color: "var(--t2)", lineHeight: 1.55 }}>
        Hecate can see the provider configuration, but no routable models are available yet.
      </div>
      <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(160px, 1fr))", gap: 8, marginTop: 10 }}>
        <InfoChip label="Endpoint" value={endpoint || "not reported"} />
        <InfoChip label="Credentials" value={credentialState} />
        <InfoChip label="Models" value={modelCount > 0 ? String(modelCount) : "none discovered"} />
        <InfoChip label="Routing" value={routingState} />
        <InfoChip label="Health" value={runtimeProvider?.status || "pending probe"} />
        <InfoChip label="Discovery" value={discoverySource || "pending"} />
      </div>
      <CompactProviderReadinessChecks checks={runtimeProvider?.readiness_checks ?? []} />
      {(blockedReason || lastError || lastErrorClass) && (
        <div style={{
          marginTop: 10,
          border: "1px solid var(--amber-border)",
          borderRadius: "var(--radius-sm)",
          background: "var(--amber-bg)",
          padding: "8px 9px",
          fontSize: 11,
          color: "var(--amber)",
          lineHeight: 1.45,
        }}>
          {blockedReason && <div>Route: {blockedReason}</div>}
          {lastErrorClass && <div>Error class: {lastErrorClass}</div>}
          {lastError && <div style={{ color: "var(--t2)", marginTop: 3, overflowWrap: "anywhere" }}>{lastError}</div>}
        </div>
      )}
      <ul style={{ margin: "10px 0 0", paddingLeft: 18, color: "var(--t3)", fontSize: 11, lineHeight: 1.55 }}>
        {guidance.map(item => <li key={item}>{item}</li>)}
      </ul>
    </div>
  );
}

function SelectedModelReadinessNotice({
  issue,
  compact = false,
  onOpenProviders,
}: {
  issue: SelectedModelIssue;
  compact?: boolean;
  onOpenProviders?: () => void;
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
              Open Providers
            </button>
          )}
        </div>
      )}
      <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(150px, 1fr))", gap: 8, marginTop: 10 }}>
        {issue.details.slice(0, compact ? 3 : issue.details.length).map((detail) => (
          <InfoChip key={detail.label} label={detail.label} value={detail.value} />
        ))}
      </div>
      {!compact && (
        <ul style={{ margin: "10px 0 0", paddingLeft: 18, color: "var(--t3)", fontSize: 11, lineHeight: 1.55 }}>
          {issue.steps.map((step) => <li key={step}>{step}</li>)}
        </ul>
      )}
    </div>
  );
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

function QuickLocalProviderAdd({
  discoveries,
  error,
  loading,
  presets,
  adding,
  onAdd,
  onRefresh,
}: {
  discoveries: LocalProviderDiscoveryRecord[];
  error: string;
  loading: boolean;
  presets: ProviderPresetRecord[];
  adding: boolean;
  onAdd: (providers: LocalProviderDiscoveryRecord[]) => void;
  onRefresh: () => void;
}) {
  const candidates = discoveries.filter(discovery => presets.some(preset => preset.id === discovery.preset_id));
  if (!loading && !error && candidates.length === 0) return null;

  return (
    <div style={{
      margin: "14px auto 0",
      maxWidth: 640,
      border: "1px solid var(--border)",
      borderRadius: "var(--radius)",
      background: "var(--bg2)",
      padding: 12,
      textAlign: "left",
    }}>
      <div style={{ display: "flex", alignItems: "flex-start", gap: 10, marginBottom: candidates.length > 0 || error ? 12 : 0 }}>
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ fontSize: 11, color: "var(--t2)", fontFamily: "var(--font-mono)", textTransform: "uppercase", letterSpacing: "0.04em" }}>
            Detected locally
          </div>
          <div style={{ fontSize: 12, color: "var(--t3)", lineHeight: 1.45, marginTop: 3 }}>
            Hecate found local inference tools on this machine. Add them now, then pull or load models in the provider app if needed.
          </div>
        </div>
        {loading && <span style={{ fontSize: 11, color: "var(--t3)", paddingTop: 2 }}>Checking...</span>}
        <button className="btn btn-ghost btn-sm" disabled={loading || adding} onClick={onRefresh} type="button" style={{ padding: "4px 8px", flexShrink: 0 }}>
          Check again
        </button>
      </div>
      {error && <InlineError message={error} />}
      {candidates.length > 0 && (
        <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
          <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(220px, 1fr))", gap: 8 }}>
            {candidates.map(discovery => {
              const preset = presets.find(preset => preset.id === discovery.preset_id);
              const status = localProviderReadiness(discovery);
              const modelCount = discovery.model_count ?? discovery.models?.length ?? 0;
              const detail = discovery.http_available
                ? `${discovery.base_url} · ${modelCount} model${modelCount === 1 ? "" : "s"}`
                : `${discovery.command || "Command"} found${discovery.command_path ? ` · ${discovery.command_path}` : ""}`;
              return (
                <div key={discovery.preset_id} style={{
                  minHeight: 60,
                  display: "flex",
                  alignItems: "center",
                  gap: 10,
                  border: "1px solid var(--border)",
                  borderRadius: "var(--radius)",
                  padding: "10px 12px",
                  minWidth: 0,
                }}>
                  <div style={{
                    width: 28,
                    height: 28,
                    borderRadius: "var(--radius-sm)",
                    background: "var(--bg3)",
                    border: "1px solid var(--border)",
                    display: "flex",
                    alignItems: "center",
                    justifyContent: "center",
                    fontFamily: "var(--font-mono)",
                    fontSize: 12,
                    fontWeight: 600,
                    color: "var(--teal)",
                    flexShrink: 0,
                  }}>
                    {(preset?.name || discovery.name)[0]?.toUpperCase()}
                  </div>
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div style={{ display: "flex", alignItems: "center", gap: 6, minWidth: 0 }}>
                      <div style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                        {preset?.name || discovery.name}
                      </div>
                      <span title={status.title} style={{
                        fontSize: 10,
                        lineHeight: "16px",
                        height: 16,
                        borderRadius: 999,
                        padding: "0 6px",
                        whiteSpace: "nowrap",
                        color: status.color,
                        background: status.background,
                        border: `1px solid ${status.border}`,
                        flexShrink: 0,
                      }}>
                        {status.label}
                      </span>
                    </div>
                    <div title={detail} style={{ fontSize: 11, color: "var(--t3)", lineHeight: 1.35, marginTop: 2, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                      {detail}
                    </div>
                  </div>
                </div>
              );
            })}
          </div>
          <div style={{ display: "flex", flexDirection: "column", alignItems: "center", gap: 8 }}>
            <span style={{ fontSize: 11, color: "var(--t3)", lineHeight: 1.4, textAlign: "center" }}>
              Adds {candidates.length} provider{candidates.length === 1 ? "" : "s"} with the detected/default endpoints. You can edit names and URLs later in Providers.
            </span>
            <button
              className="btn btn-primary btn-sm"
              disabled={adding}
              onClick={() => onAdd(candidates)}
              type="button"
              style={{ display: "flex", alignItems: "center", gap: 4 }}>
              <Icon d={Icons.plus} size={13} />
              {adding ? "Adding..." : `Add detected provider${candidates.length === 1 ? "" : "s"}`}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

function isQuickAddableLocalProvider(discovery: LocalProviderDiscoveryRecord): boolean {
  return discovery.http_available || discovery.command_available;
}

function normalizeProviderBaseURL(baseURL: string | undefined): string {
  return (baseURL ?? "").trim();
}

function localProviderReadiness(discovery: LocalProviderDiscoveryRecord): {
  label: string;
  title: string;
  color: string;
  background: string;
  border: string;
} {
  if (discovery.http_available) {
    const models = discovery.model_count ? ` · ${discovery.model_count} model${discovery.model_count === 1 ? "" : "s"}` : "";
    return {
      label: "Running",
      title: `HTTP probe passed at ${discovery.probe_url}${models}`,
      color: "var(--green)",
      background: "var(--green-bg)",
      border: "var(--green-border)",
    };
  }
  return {
    label: "Installed",
    title: `${discovery.command || "Command"} found${discovery.command_path ? ` at ${discovery.command_path}` : ""}; local HTTP endpoint is not running`,
    color: "var(--amber)",
    background: "var(--amber-bg)",
    border: "var(--amber-border)",
  };
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

function formatAgentRuntimeMeta(runID?: string, durationMS?: number, nativeSessionID?: string): string {
  const parts: string[] = [];
  if (nativeSessionID) {
    parts.push(`ACP ${nativeSessionID.slice(0, 12)}`);
  }
  if (runID) {
    parts.push(`Run ${compactID(runID, ["run_"], 12)}`);
  }
  if (durationMS && durationMS > 0) {
    parts.push(formatDuration(durationMS));
  }
  return parts.join(" · ");
}

function compactID(id: string, prefixes: string[], length: number): string {
  const trimmed = id.trim();
  const withoutPrefix = prefixes.reduce((current, prefix) => (
    current.startsWith(prefix) ? current.slice(prefix.length) : current
  ), trimmed);
  return withoutPrefix.slice(0, length);
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
