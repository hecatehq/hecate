import { useEffect, useRef, useState } from "react";
import type { ReactNode, SyntheticEvent } from "react";
import { useRuntimeConsoleContext } from "../../app/RuntimeConsoleContext";
import type { RuntimeConsoleViewModel } from "../../app/useRuntimeConsole";
import { discoverLocalProviders } from "../../lib/api";
import { resolveChatSetupRepairState, type ChatSetupRepairState } from "../../lib/chat-setup-readiness";
import { describeGatewayError, formatErrorCode } from "../../lib/error-diagnostics";
import { formatDurationMs, formatInteger, formatLocaleTime } from "../../lib/format";
import { usePersistedState } from "../../lib/persistedState";
import { buildSelectedModelIssue } from "../../lib/provider-issues";
import type { SelectedModelIssue } from "../../lib/provider-issues";
import type { AgentAdapterRecord, AgentAdapterSetupCommandStatus, AgentChatActivityRecord, AgentChatSegmentRecord, AgentChatSessionRecord, AgentChatTimingRecord, AgentChatUsageRecord, LocalProviderDiscoveryRecord, ProviderPresetRecord } from "../../types/runtime";
import { BrandAvatar, CodeBlock, Icon, Icons, InlineError } from "../shared/ui";
import { TranscriptMessageRow } from "../transcript/TranscriptMessageRow";
import { AgentApprovalAutoModeBanner, AgentApprovalsBanner } from "./AgentApprovalBanner";
import { AgentApprovalModal } from "./AgentApprovalModal";
import { AddProviderModal } from "../providers/AddProviderModal";
import { ChatSidebar, sidebarSessionAgentLabel, sidebarSessionBrand } from "./ChatSidebar";
import { ExternalAgentConfigControls, ExternalAgentSettingsControls, HecateModelConfigControl, HecateProviderConfigControl, LockedHecateModelSnapshot, chatAgentOption } from "./ChatAgentControls";
import { ChatInstructionsPanel } from "./ChatInstructionsPanel";
import { ChatNoticeFrame, ChatNoticeHeader, ChatNoticeInline, ChatNoticeRow } from "./ChatNotice";
import { AgentSetupHints, ClaudeCodePreflightCard, ClaudeCodeSetupEmptyPanel, claudeCodePreflightState, claudeCodeSetupTokenCommand } from "./ClaudeCodeSetup";
import type { ClaudeCodePreflightState } from "./ClaudeCodeSetup";

type Props = {
  onNavigate?: (workspace: "connections" | "runs" | "overview" | "settings") => void;
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

type HecateTaskApproval = {
  approvalID: string;
  title: string;
  kind?: string;
  detail?: string;
  createdAt?: string;
};

export function ChatView({ onNavigate, onOpenTask, onOpenTrace }: Props) {
  const { state, actions } = useRuntimeConsoleContext();
  const [sidebarOpen, setSidebarOpen] = useState(true);
  // approvalModalID is the per-banner-click open state for the
  // approval modal. The modal itself fetches the full row on mount;
  // we only carry the id here.
  const [approvalModalID, setApprovalModalID] = useState<string | null>(null);
  const [copiedMsgId, setCopiedMsgId] = useState<string | null>(null);
  const [atBottom, setAtBottom] = useState(true);
  const [workspaceEntryOpen, setWorkspaceEntryOpen] = useState(false);
  const [chatSettingsOpen, setChatSettingsOpen] = useState(false);
  const [rtkOnboardingDismissed, setRTKOnboardingDismissed] = useState(false);
  const [draftChatOpen, setDraftChatOpen] = useState(() => Boolean(
    state.message.trim()
    || state.chatError
    || state.pendingToolCalls.length > 0
    || state.streamingContent,
  ));
  const [addProviderOpen, setAddProviderOpen] = useState(false);
  const [workspacePathValue, setWorkspacePathValue] = useState("");
  const [quickLocalProviders, setQuickLocalProviders] = useState<LocalProviderDiscoveryRecord[]>([]);
  const [quickLocalLoading, setQuickLocalLoading] = useState(false);
  const [quickLocalError, setQuickLocalError] = useState("");
  const [quickAddingProviders, setQuickAddingProviders] = useState(false);
  const [taskApprovalBusyID, setTaskApprovalBusyID] = useState("");
  const [capabilitySaving, setCapabilitySaving] = useState(false);
  const [claudeTokenDraft, setClaudeTokenDraft] = useState("");
  const [claudeTokenSaving, setClaudeTokenSaving] = useState(false);
  const isMac = typeof navigator !== "undefined" && /mac/i.test(navigator.platform);
  const modKey = isMac ? "⌘" : "Ctrl";
  const [modEnterMode, setModEnterMode] = usePersistedState<boolean>(
    "hecate.shiftEnterMode",
    (raw) => raw === "1" ? true : raw === "0" ? false : null,
    false,
    { serialize: (v) => v ? "1" : "0" },
  );
  const formRef = useRef<HTMLFormElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  const bottomRef = useRef<HTMLDivElement>(null);
  const userScrolledRef = useRef(false);
  const focusComposerAfterNewChatRef = useRef(false);
  const messageHistoryCursorRef = useRef<number | null>(null);
  const messageHistoryDraftRef = useRef("");

  const activeSessionIsExternal = Boolean(state.activeAgentChatSession?.runtime_kind === "external_agent" || state.activeAgentChatSession?.adapter_id);
  const activeSessionIsHecate = Boolean(state.activeAgentChatSession && !activeSessionIsExternal);
  const isHecateChat = activeSessionIsHecate || (!activeSessionIsExternal && (state.chatTarget === "agent" || state.chatTarget === "model"));
  const isAgentChat = isHecateChat || state.chatTarget === "external_agent";
  const isHecateAgentChat = isHecateChat && state.chatTarget === "agent";
  const isExternalAgentChat = activeSessionIsExternal || (!activeSessionIsHecate && state.chatTarget === "external_agent");
  const externalAgentHasConfigControls = Boolean(isExternalAgentChat && state.activeAgentChatSession?.config_options?.length);
  const instructionsAvailable = isHecateChat;
  const activeSessionID = isAgentChat ? state.activeAgentChatSessionID : state.activeChatSessionID;
  const chatCanvasActive = Boolean(activeSessionID || draftChatOpen);
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
  const pendingTaskApprovals = isHecateChat
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
  const messageHistory = visibleMessages
    .filter((m) => m.role === "user" && typeof m.content === "string" && m.content.trim())
    .map((m) => (m.content ?? "").trimEnd());
  const transcriptItems = buildTranscriptItems(
    visibleMessages,
    state.activeAgentChatSession?.segments,
    isHecateChat,
  );
  const streaming = state.chatLoading;
  const chatDiagnostic = describeGatewayError(state.chatErrorCode, state.chatErrorStatus ?? undefined);
  const activeAgentAdapterID = state.activeAgentChatSession?.adapter_id || state.agentAdapterID;
  const selectedAgent = state.agentAdapters.find((adapter) => adapter.id === activeAgentAdapterID);
  const selectedAgentHealth = activeAgentAdapterID
    ? state.agentAdapterHealthByID.get(activeAgentAdapterID) ?? null
    : null;
  const selectedAgentHealthLoading = activeAgentAdapterID
    ? Boolean(state.agentAdapterHealthLoadingByID.get(activeAgentAdapterID))
    : false;
  const claudeCodePreflight = claudeCodePreflightState(selectedAgent, selectedAgentHealth);
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
  const selectedProviderName = state.providerFilter === "auto"
    ? "Select provider"
    : selectedConfiguredProvider?.name || selectedRuntimeProvider?.name || state.providerFilter;
  const hecateProviderOptions = (() => {
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

    return source.map(p => {
      const cfg = state.settingsConfig?.providers.find(c => c.id === p.id);
      // Cloud-with-no-credentials is the only "disabled"
      // reason left now that the toggle is gone — we surface it as a
      // tooltip + key icon rather than hiding the row, so the operator
      // sees why the provider isn't usable and where to fix it.
      const cloudUnconfigured = !!cfg && cfg.kind === "cloud" && !cfg.credential_configured;
      return {
        id: p.id,
        name: state.providerPresets.find(pr => pr.id === p.id)?.name || p.name || p.id,
        healthy: true,
        kind: p.kind,
        configured: cfg ? cfg.credential_configured : undefined,
        disabledReason: cloudUnconfigured ? `Add an API key for ${cfg!.name || cfg!.id} in Connections` : undefined,
      };
    });
  })();
  const hecateDisabledProviderReasons = (() => {
    const out = new Map<string, string>();
    for (const cfg of state.settingsConfig?.providers ?? []) {
      if (cfg.kind === "cloud" && !cfg.credential_configured) {
        out.set(cfg.id, `Add an API key for ${cfg.name || cfg.id} in Connections`);
      }
    }
    return out;
  })();
  const agentRouteUnavailable = availableAgents.length === 0;
  const selectedAgentUnavailable = isExternalAgentChat && Boolean(selectedAgent) && !selectedAgent?.available;
  const newChatAgentID = state.newChatAgentID || "hecate";
  const nothingRunnable = !state.loading && modelRouteUnavailable && agentRouteUnavailable;
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
  const activeHeaderBrand = isAgentChat
    ? (state.activeAgentChatSession ? sidebarSessionBrand(state.activeAgentChatSession) : newChatAgentID)
    : selectedConfiguredProvider?.id || selectedRuntimeProvider?.name || state.providerFilter;
  const activeHeaderFallback = isAgentChat
    ? (state.activeAgentChatSession
        ? sidebarSessionAgentLabel(state.activeAgentChatSession, state.agentAdapters)
        : chatAgentOption(newChatAgentID, state.agentAdapters).label)
    : selectedProviderName;
  const activeHeaderSubline = buildActiveChatHeaderSubline({
    isAgentChat,
    isExternalAgentChat,
    isHecateAgentChat,
    activeSession: state.activeAgentChatSession,
    selectedAgent,
    newChatAgentID,
    adapters: state.agentAdapters,
  });
  const latestChatUsage = isAgentChat ? findLatestAgentUsage(state.activeAgentChatSession) : null;
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
  const showHeaderWorkspaceButton = isExternalAgentChat || isHecateAgentChat;
  const showClaudeCodeEmptyPreflight = chatCanvasActive
    && isExternalAgentChat
    && visibleMessages.length === 0
    && state.pendingToolCalls.length === 0
    && !streaming
    && Boolean(claudeCodePreflight?.blockSend);
  const showRTKOnboardingHint = chatCanvasActive
    && isHecateChat
    && !chatSettingsOpen
    && !rtkOnboardingDismissed
    && !state.activeAgentChatSessionID
    && visibleMessages.length === 0
    && activeQueuedChatMessages.length === 0
    && state.pendingToolCalls.length === 0
    && state.message.trim() === "";
  const chatSetupRepair = resolveChatSetupRepairState({
    target: state.chatTarget,
    hasConfiguredProviders,
    modelRouteUnavailable,
    selectedModelIssue,
    toolsDisabledForModel: hecateAgentToolsDisabledForModel,
    workspace: state.agentWorkspace,
    selectedAgentName: selectedAgent?.name,
    selectedAgentAvailable: Boolean(selectedAgent?.available),
    anyAgentAvailable: availableAgents.length > 0,
    claudeCodeSetupRequired: Boolean(claudeCodePreflight?.blockSend),
  });
  const composerVisible = chatCanvasActive && (isExternalAgentChat || (isHecateChat && hecateChatModelReady)) && !showClaudeCodeEmptyPreflight;
  const hecateHasMessageControls = chatCanvasActive && isHecateChat && (hecateAgentModelLocked || hasConfiguredProviders || selectableModels.length > 0);
  const messageControlsVisible = chatCanvasActive && (externalAgentHasConfigControls || hecateHasMessageControls);
  const composerRepair = composerVisible && !emptyStateAlreadyShowsRepair(chatSetupRepair, visibleMessages.length)
    ? composerVisibleRepair(chatSetupRepair)
    : null;
  const agentBusy = isAgentChat && (streaming || hecateAgentBusy);
  const queueingMessage = agentBusy && Boolean(state.message.trim());
  const sendDisabled = !state.message.trim()
    || (!agentBusy && streaming)
    || (!isAgentChat && modelRouteUnavailable)
    || (!agentBusy && isExternalAgentChat && (!state.agentWorkspace.trim() || !selectedAgent?.available))
    || (!agentBusy && isExternalAgentChat && Boolean(claudeCodePreflight?.blockSend))
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

  function openClaudeCodeSetup() {
    try {
      sessionStorage.setItem("hecate.connectionsFocus", "claude-code-guided-setup");
    } catch {
      // sessionStorage unavailable — navigation still
      // works, just no auto-scroll to the guided setup card.
    }
    onNavigate?.("connections");
  }

  async function saveClaudeCodeToken() {
    const token = claudeTokenDraft.trim();
    if (!token || claudeTokenSaving) return;
    setClaudeTokenSaving(true);
    try {
      const saved = await actions.setAgentAdapterCredential("claude_code", token, "CLAUDE_CODE_OAUTH_TOKEN");
      if (saved) {
        setClaudeTokenDraft("");
        await actions.probeAgentAdapter("claude_code");
      }
    } finally {
      setClaudeTokenSaving(false);
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
    // load would hijack normal page navigation and screen-reader flow.
    // Focus is instead applied at the explicit user-driven entry points:
    // the New-session button and the session row onClick handlers.
    userScrolledRef.current = false;
    messageHistoryCursorRef.current = null;
    messageHistoryDraftRef.current = "";
    setAtBottom(true);
    bottomRef.current?.scrollIntoView({ behavior: "instant" });
  }, [activeSessionID]);

  useEffect(() => {
    if (activeSessionID) {
      setDraftChatOpen(false);
    }
  }, [activeSessionID]);

  useEffect(() => {
    if (!focusComposerAfterNewChatRef.current || !chatCanvasActive) return;
    const frame = requestAnimationFrame(() => {
      if (!textareaRef.current) return;
      textareaRef.current.focus();
      focusComposerAfterNewChatRef.current = false;
    });
    return () => cancelAnimationFrame(frame);
  }, [chatCanvasActive, composerVisible, messageControlsVisible]);

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

  function scrollToBottom() {
    userScrolledRef.current = false;
    setAtBottom(true);
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }

  async function chooseWorkspace() {
    setWorkspacePathValue(state.agentWorkspace);
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
    setModEnterMode(v => !v);
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

  function handleRTKChange(enabled: boolean) {
    if (!enabled) {
      setRTKOnboardingDismissed(true);
    }
    void actions.setHecateRTKEnabled(enabled);
  }

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

  function focusComposerWhenReady() {
    focusComposerAfterNewChatRef.current = true;
    requestAnimationFrame(() => {
      if (!textareaRef.current) return;
      textareaRef.current.focus();
      focusComposerAfterNewChatRef.current = false;
    });
  }

  return (
    <div style={{ display: "flex", height: "100%", overflow: "hidden" }}>
      {sidebarOpen && (
        <ChatSidebar
          isAgentChat={isAgentChat}
          onSelectSession={(sessionID) => {
            focusComposerWhenReady();
            setDraftChatOpen(false);
            void actions.selectChatSession(sessionID);
            textareaRef.current?.focus();
          }}
          onCreateChat={() => {
            setDraftChatOpen(true);
            setChatSettingsOpen(false);
            focusComposerWhenReady();
            void actions.createChatSession();
          }}
        />
      )}

      {/* Chats main */}
      <div style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden", minWidth: 0, position: "relative" }}>
        {/* Topbar */}
        {chatCanvasActive && (
        <div style={{ height: "var(--topbar-h)", borderBottom: "1px solid var(--border)", display: "flex", alignItems: "center", padding: "0 12px", gap: 8, flexShrink: 0, background: "var(--bg1)" }}>
          {!sidebarOpen && (
            <button className="btn btn-ghost btn-sm" onClick={() => setSidebarOpen(true)} title="Open chats" aria-label="Open chats sidebar" type="button">
              <Icon d={Icons.chevR} size={13} />
            </button>
          )}
          <BrandAvatar
            brand={activeHeaderBrand}
            fallback={activeHeaderFallback}
            boxed={false}
            size={24}
            title={activeHeaderFallback}
            style={{ flexShrink: 0 }}
          />
          <div style={{ flex: 1, minWidth: 0 }}>
            <div style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
              {activeTitle || ((isAgentChat ? state.agentChatSessions : state.chatSessions).length === 0 ? "New chat" : "Select a chat")}
            </div>
            {activeHeaderSubline && (
              <div
                title={isExternalAgentChat ? formatAgentSessionTitle(state.activeAgentChatSession, selectedAgent) : activeHeaderSubline}
                style={{
                  color: "var(--t3)",
                  fontFamily: "var(--font-mono)",
                  fontSize: 10,
                  lineHeight: 1.25,
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                }}
              >
                {activeHeaderSubline}
              </div>
            )}
          </div>
          {isExternalAgentChat && (() => {
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
          {isAgentChat && (
            <div
              aria-label="Chat header actions"
              style={{
                display: "flex",
                alignItems: "center",
                gap: 4,
                flexShrink: 0,
              }}
            >
              {showHeaderWorkspaceButton && (
                <button
                  className="btn btn-ghost btn-sm"
                  onClick={() => void chooseWorkspace()}
                  title={state.agentWorkspace ? `Workspace: ${state.agentWorkspace}` : "Choose workspace folder"}
                  aria-label={state.agentWorkspace ? `Workspace: ${state.agentWorkspace}` : "Choose workspace folder"}
                  type="button"
                  style={{
                    width: 30,
                    height: 30,
                    padding: 0,
                    justifyContent: "center",
                    color: "var(--t2)",
                    borderColor: "transparent",
                    background: "transparent",
                    boxShadow: "none",
                  }}
                >
                  <Icon d={Icons.folder} size={13} />
                </button>
              )}
              <button
                className="btn btn-ghost btn-sm"
                type="button"
                aria-expanded={chatSettingsOpen}
                aria-label="Chat settings"
                onClick={() => setChatSettingsOpen((open) => !open)}
                title="Chat settings"
                style={{
                  minWidth: 30,
                  height: 30,
                  padding: 0,
                  gap: 6,
                  justifyContent: "center",
                  color: chatSettingsOpen ? "var(--teal)" : "var(--t2)",
                  borderColor: "transparent",
                  background: chatSettingsOpen ? "var(--teal-bg)" : "transparent",
                  boxShadow: "none",
                }}
              >
                <Icon d={Icons.settings} size={13} />
              </button>
            </div>
          )}
        </div>
        )}

        <div style={{ flex: 1, display: "flex", minHeight: 0, overflow: "hidden" }}>
          <div style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", overflow: "hidden", position: "relative" }}>
        {!chatCanvasActive ? (
          <NoActiveChatState
            agentLabel={chatAgentOption(newChatAgentID, state.agentAdapters).label}
            hasSessions={(isAgentChat ? state.agentChatSessions : state.chatSessions).length > 0}
          />
        ) : (
          <>
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
                brand={messageBrand(m, call?.provider, isAgentChat, isHecateAgentChat)}
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
                setupAction={
                  // Render the "Open Claude Code setup" button only
                  // when the server-side message carries the
                  // claude_code_auth_required marker. Pattern-match
                  // (not strict equality) is deliberate — the marker
                  // is part of a paragraph that may be reworded over
                  // time; the token itself is stable contract between
                  // internal/agentadapters/auth_status.go and this UI
                  // handler.
                  isAgentChat && role === "assistant" && m.agent_adapter_id === "claude_code"
                    && typeof m.error === "string" && m.error.includes("claude_code_auth_required")
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

          {/* Streaming */}
          {!isAgentChat && streaming && state.streamingContent !== null && (
            <div style={{ padding: "4px 16px 16px", maxWidth: 820, margin: "0 auto", width: "100%" }}>
              <div style={{ display: "flex", gap: 10, alignItems: "flex-start" }}>
                <BrandAvatar
                  brand={isAgentChat ? selectedAgent?.id : (selectedConfiguredProvider?.id || selectedRuntimeProvider?.name || hecateChatModelValue)}
                  fallback={isAgentChat ? selectedAgent?.name : state.model}
                  size={28}
                  style={{ marginTop: 2 }}
                />
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
              claudeCodePreflight={showClaudeCodeEmptyPreflight ? claudeCodePreflight : null}
              claudeCodePreflightLoading={selectedAgentHealthLoading}
              setupRepair={chatSetupRepair}
              modelRouteUnavailable={modelRouteUnavailable}
              selectedModelIssue={selectedModelIssue}
              agentRouteUnavailable={isExternalAgentChat && agentRouteUnavailable}
              nothingRunnable={nothingRunnable}
              agentAdapters={state.agentAdapters}
              selectedAgent={selectedAgent}
              selectedAgentUnavailable={selectedAgentUnavailable}
              hasConfiguredProviders={hasConfiguredProviders}
              providerPresets={state.providerPresets}
              quickLocalProviders={quickLocalProviders}
              quickLocalLoading={quickLocalLoading}
              quickLocalError={quickLocalError}
              quickAddingProviders={quickAddingProviders}
              onOpenProviders={() => {
                if (onNavigate) {
                  onNavigate("connections");
                } else {
                  setAddProviderOpen(true);
                }
              }}
              onUseSuggestedModel={(model) => {
                actions.setProviderFilter("auto");
                actions.setModel(model);
              }}
              onChooseWorkspace={() => void chooseWorkspace()}
              onOpenAgentSetup={openClaudeCodeSetup}
              onQuickAddLocalProviders={quickAddLocalProviders}
              onRefreshQuickLocalProviders={refreshQuickLocalProviders}
              onSwitchTarget={actions.setChatTarget}
              claudeCodeCLI={selectedAgent?.claude_code_cli}
              claudeTokenDraft={claudeTokenDraft}
              claudeTokenSaving={claudeTokenSaving}
              onClaudeTokenDraftChange={setClaudeTokenDraft}
              onSaveClaudeCodeToken={() => void saveClaudeCodeToken()}
              onTestClaudeCode={() => void actions.probeAgentAdapter("claude_code")}
              rtkAvailable={state.hecateRTKAvailable}
              rtkPath={state.hecateRTKPath}
              rtkEnabled={state.hecateRTKEnabled}
              showRTKOnboardingHint={showRTKOnboardingHint}
              onEnableRTK={() => void actions.setHecateRTKEnabled(true)}
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

        {!showClaudeCodeEmptyPreflight && (composerVisible || messageControlsVisible || state.chatError || selectedModelIssue) && (
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
                  session={state.activeAgentChatSession}
                  onChange={actions.setAgentChatConfigOption}
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
        </>
        )}
          </div>
        {chatCanvasActive && isAgentChat && chatSettingsOpen && (
          <ChatSettingsPanel
            showHecateControls={isHecateChat}
            toolsEnabled={isHecateAgentChat}
            toolsDisabledForModel={hecateAgentToolsDisabledForModel}
            rtkEnabled={Boolean(state.hecateRTKEnabled)}
            rtkAvailable={Boolean(state.hecateRTKAvailable)}
            rtkPath={state.hecateRTKPath}
            externalAgentID={isExternalAgentChat ? activeAgentAdapterID : ""}
            taskID={state.activeAgentChatSession?.task_id}
            agentName={selectedAgent?.name || activeHeaderFallback}
            model={state.model}
            provider={selectedProviderName}
            workspace={state.activeAgentChatSession?.workspace || state.agentWorkspace}
            status={state.activeAgentChatSession?.status || ""}
            messageCount={state.activeAgentChatSession?.messages?.length ?? 0}
            agentUsage={latestChatUsage}
            usageSource={isHecateChat ? "hecate" : "adapter"}
            externalSession={isExternalAgentChat ? state.activeAgentChatSession : null}
            instructionsAvailable={instructionsAvailable}
            isHecateAgentChat={isHecateAgentChat}
            instructionsLocked={messages.length > 0}
            systemPrompt={state.systemPrompt}
            onToolsChange={(enabled) => actions.setChatTarget(enabled ? "agent" : "model")}
            onRTKChange={handleRTKChange}
            onConfigOptionChange={actions.setAgentChatConfigOption}
            onSystemPromptChange={actions.setSystemPrompt}
            onCopyCommand={actions.copyCommand}
          />
        )}
        </div>
      </div>

      <style>{`
        .cursor-blink { color: var(--teal); }
        @keyframes pulse { 0%,100%{opacity:1} 50%{opacity:0.5} }
        @keyframes hecate-live-caret {
          0%, 100% { opacity: 0.25; transform: translateY(-1px) scale(0.85); }
          50% { opacity: 0.9; transform: translateY(-1px) scale(1.15); }
        }
      `}</style>

      {approvalModalID && isExternalAgentChat && state.activeAgentChatSessionID && (
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

function NoActiveChatState({ agentLabel, hasSessions }: { agentLabel: string; hasSessions: boolean }) {
  return (
    <div
      style={{
        flex: 1,
        minHeight: 0,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        padding: 24,
        textAlign: "center",
      }}
    >
      <div style={{ maxWidth: 420 }}>
        <div style={{ fontSize: 14, fontWeight: 600, color: "var(--t1)", marginBottom: 8 }}>
          {hasSessions ? "No chat selected" : "No chats yet"}
        </div>
        <div style={{ fontSize: 12, color: "var(--t3)", lineHeight: 1.6 }}>
          {hasSessions
            ? `Choose a chat from the sidebar, or start a new ${agentLabel} chat.`
            : `Start your first ${agentLabel} chat from the sidebar.`}
        </div>
      </div>
    </div>
  );
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

function buildActiveChatHeaderSubline({
  isAgentChat,
  isExternalAgentChat,
  isHecateAgentChat,
  activeSession,
  selectedAgent,
  newChatAgentID,
  adapters,
}: {
  isAgentChat: boolean;
  isExternalAgentChat: boolean;
  isHecateAgentChat: boolean;
  activeSession: AgentChatSessionRecord | null;
  selectedAgent?: AgentAdapterRecord;
  newChatAgentID: string;
  adapters: AgentAdapterRecord[];
}): string {
  if (!isAgentChat) return "";
  if (isExternalAgentChat) {
    const base = activeSession
      ? formatAgentSessionLabel(activeSession, selectedAgent)
      : `${chatAgentOption(newChatAgentID, adapters).label} · new session`;
    return [base, activeSession?.workspace || ""]
      .filter(Boolean)
      .join(" · ");
  }
  const mode = isHecateAgentChat ? "Tools on" : "Tools off";
  return [
    mode,
    activeSession?.workspace || "",
  ].filter(Boolean).join(" · ");
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
        kind: taskApprovalDisplayKind(activity),
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
    .replace(/^Agent requested tools that require approval:\s*/i, "")
    .replace(/^builtin\.agent_loop_approval$/i, "")
    .trim();
}

function taskApprovalDisplayKind(activity: AgentChatActivityRecord): string {
  const kind = (activity.kind || "").trim();
  if (kind && kind !== "approval" && kind !== "agent_loop_approval") {
    return kind;
  }
  const haystack = `${activity.title || ""} ${activity.detail || ""}`.toLowerCase();
  if (haystack.includes("shell_exec")) return "shell_command";
  if (haystack.includes("git_exec")) return "git_exec";
  if (haystack.includes("file_write")) return "file_write";
  if (haystack.includes("network_egress")) return "network_egress";
  if (haystack.includes("agent_loop_tool_call")) return "agent_loop_tool_call";
  return "approval";
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

function messageBrand(
  message: VisibleChatMessage,
  provider: string | undefined,
  isAgentChat: boolean,
  isHecateAgentChat: boolean,
): string | undefined {
  if (!isAgentChat) return provider || message.model;
  if (message.agent_adapter_id) return message.agent_adapter_id;
  if (message.agent_adapter_name) return message.agent_adapter_name;
  if (isHecateAgentChat) return provider || message.provider || message.model || "hecate";
  return message.provider || message.model;
}

function providerLabelForHecateChat(state: RuntimeConsoleViewModel["state"], providerID: string): string {
  if (!providerID || providerID === "auto") {
    return "Select provider";
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
    <ChatNoticeFrame
      aria-label="Pending Hecate Agent task approvals"
      testID="hecate-task-approval-banner"
      tone="amber"
    >
      <ChatNoticeHeader
        tone="amber"
        title={approvals.length === 1 ? "Approval required" : `${approvals.length} approvals required`}
        action={onOpenTask && (
          <button
            type="button"
            className="btn btn-ghost btn-sm"
            onClick={() => onOpenTask(taskID, runID)}
            style={{ marginLeft: "auto" }}
          >
            Open task
          </button>
        )}
      />
      {visible.map((approval) => {
        const approveBusy = busyID === `${approval.approvalID}:approve`;
        const rejectBusy = busyID === `${approval.approvalID}:reject`;
        const disabled = busyID !== "";
        const label = describeTaskApprovalKind(approval.kind || approval.title);
        return (
          <ChatNoticeRow
            key={approval.approvalID}
            tone="amber"
            style={{
              display: "grid",
              gridTemplateColumns: "minmax(0, 1fr) auto",
              gap: 12,
              alignItems: "center",
            }}
          >
            <div style={{ minWidth: 0 }}>
              <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--amber)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                  {label}
                </span>
                {approval.createdAt && (
                  <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--amber-lo)" }}>
                    {formatLocaleTime(approval.createdAt)}
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
          </ChatNoticeRow>
        );
      })}
      {overflow > 0 && (
        <ChatNoticeRow tone="amber" style={{ padding: "7px 12px", color: "var(--amber)", fontFamily: "var(--font-mono)", fontSize: 11 }}>
          + {overflow} more in the backing Task
        </ChatNoticeRow>
      )}
    </ChatNoticeFrame>
  );
}

function describeTaskApprovalKind(kind: string): string {
  switch (kind) {
    case "approval":             return "Approval";
    case "shell_command":        return "Shell execution";
    case "git_exec":             return "Git execution";
    case "file_write":           return "File write";
    case "network_egress":       return "Network egress";
    case "agent_loop_tool_call": return "Agent tool call";
    default:                     return kind.replaceAll("_", " ");
  }
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
  claudeCodePreflight,
  claudeCodePreflightLoading,
  setupRepair,
  modelRouteUnavailable,
  selectedModelIssue,
  agentRouteUnavailable,
  nothingRunnable,
  agentAdapters,
  selectedAgent,
  selectedAgentUnavailable,
  hasConfiguredProviders,
  providerPresets,
  quickLocalProviders,
  quickLocalLoading,
  quickLocalError,
  quickAddingProviders,
  onOpenProviders,
  onUseSuggestedModel,
  onChooseWorkspace,
  onOpenAgentSetup,
  onQuickAddLocalProviders,
  onRefreshQuickLocalProviders,
  onSwitchTarget,
  claudeCodeCLI,
  claudeTokenDraft,
  claudeTokenSaving,
  onClaudeTokenDraftChange,
  onSaveClaudeCodeToken,
  onTestClaudeCode,
  rtkAvailable,
  rtkPath,
  rtkEnabled,
  showRTKOnboardingHint,
  onEnableRTK,
}: {
  isAgentChat: boolean;
  isHecateChat: boolean;
  isExternalAgentChat: boolean;
  claudeCodePreflight: ClaudeCodePreflightState | null;
  claudeCodePreflightLoading: boolean;
  setupRepair: ChatSetupRepairState | null;
  modelRouteUnavailable: boolean;
  selectedModelIssue: SelectedModelIssue | null;
  agentRouteUnavailable: boolean;
  nothingRunnable: boolean;
  agentAdapters: AgentAdapterRecord[];
  selectedAgent?: AgentAdapterRecord;
  selectedAgentUnavailable: boolean;
  hasConfiguredProviders: boolean;
  providerPresets: ProviderPresetRecord[];
  quickLocalProviders: LocalProviderDiscoveryRecord[];
  quickLocalLoading: boolean;
  quickLocalError: string;
  quickAddingProviders: boolean;
  onOpenProviders: () => void;
  onUseSuggestedModel: (model: string) => void;
  onChooseWorkspace: () => void;
  onOpenAgentSetup: () => void;
  onQuickAddLocalProviders: (providers: LocalProviderDiscoveryRecord[]) => void;
  onRefreshQuickLocalProviders: () => void;
  onSwitchTarget: (target: "model" | "agent" | "external_agent") => void;
  claudeCodeCLI?: AgentAdapterSetupCommandStatus;
  claudeTokenDraft: string;
  claudeTokenSaving: boolean;
  onClaudeTokenDraftChange: (value: string) => void;
  onSaveClaudeCodeToken: () => void;
  onTestClaudeCode: () => void;
  rtkAvailable: boolean;
  rtkPath: string;
  rtkEnabled: boolean;
  showRTKOnboardingHint: boolean;
  onEnableRTK: () => void;
}) {
  const hecateModelUnavailable = isHecateChat && (modelRouteUnavailable || Boolean(selectedModelIssue));
  const setupRepairForEmpty = setupRepair?.action === "enable_tools" ? null : setupRepair;
  const readyTitle = isExternalAgentChat ? `Ready for ${selectedAgent?.name || "the agent"}` : "Ready when you are";
  const readyDetail = isExternalAgentChat
    ? "Describe the task and Hecate will start the selected agent in this workspace."
    : "Ask a question, inspect the workspace, or describe the change you want to make.";
  const title = claudeCodePreflight
      ? "Set up Claude Code"
      : isAgentChat && selectedAgentUnavailable
      ? `${selectedAgent?.name || "Selected agent"} is unavailable`
      : isExternalAgentChat && agentRouteUnavailable
      ? "No available coding agent"
      : nothingRunnable
        ? "Nothing runnable yet"
        : selectedModelIssue
          ? selectedModelIssue.title
        : setupRepairForEmpty
          ? setupRepairForEmpty.title
        : hecateModelUnavailable
          ? "No routable model"
        : readyTitle;
  const detail = claudeCodePreflight
      ? "Claude Code needs its own adapter-visible credential before Hecate can start a session."
      : isAgentChat && selectedAgentUnavailable
      ? `Hecate could not start ${selectedAgent?.name || "the selected agent"} because its CLI is not ready in this environment.`
      : isExternalAgentChat && agentRouteUnavailable
      ? "Hecate did not find any supported coding-agent CLI or local adapter runner in the known operator locations."
      : nothingRunnable
        ? "Add a model provider or install a supported coding-agent CLI before sending a message."
        : selectedModelIssue
          ? selectedModelIssue.message
        : setupRepairForEmpty
          ? setupRepairForEmpty.message
        : hecateModelUnavailable
          ? "Add a provider with discovered models before sending through Hecate."
        : readyDetail;
  const emptyRepairAction = setupRepairForEmpty && !claudeCodePreflight
    ? setupRepairForEmpty
    : null;

  function runEmptyRepairAction() {
    if (!emptyRepairAction) return;
    switch (emptyRepairAction.action) {
      case "open_connections":
        onOpenProviders();
        return;
      case "use_suggested_model":
        if (emptyRepairAction.suggestedModel) onUseSuggestedModel(emptyRepairAction.suggestedModel);
        return;
      case "choose_workspace":
        onChooseWorkspace();
        return;
      case "open_agent_setup":
        onOpenAgentSetup();
        return;
      case "enable_tools":
        // Tools-enabled repair is handled by the composer notice, where we
        // can disable the action while capability override writes are busy.
        return;
    }
  }

  return (
    <div style={{ padding: "28px 16px 18px", maxWidth: 820, margin: "0 auto", textAlign: "center" }}>
      <div style={{ fontSize: 13, fontWeight: 600, color: "var(--t1)", marginBottom: 5 }}>{title}</div>
      <div style={{ fontSize: 12, color: "var(--t3)", lineHeight: 1.5, maxWidth: 430, margin: "0 auto" }}>{detail}</div>
      {claudeCodePreflight && (
        <ClaudeCodeSetupEmptyPanel
          state={claudeCodePreflight}
          loading={claudeCodePreflightLoading}
          cliStatus={claudeCodeCLI}
          tokenDraft={claudeTokenDraft}
          tokenSaving={claudeTokenSaving}
          onTokenDraftChange={onClaudeTokenDraftChange}
          onSaveToken={onSaveClaudeCodeToken}
          onTest={onTestClaudeCode}
        />
      )}
      {isAgentChat && (agentRouteUnavailable || selectedAgentUnavailable) && (
        <AgentSetupHints adapters={agentAdapters} selectedID={selectedAgent?.id} />
      )}
      {isHecateChat && selectedModelIssue && (
        <SelectedModelReadinessNotice issue={selectedModelIssue} compact onUseSuggestedModel={onUseSuggestedModel} />
      )}
      {showRTKOnboardingHint && isHecateChat && rtkAvailable && !rtkEnabled && !hecateModelUnavailable && !setupRepairForEmpty && (
        <RTKOnboardingHint path={rtkPath} onEnable={onEnableRTK} />
      )}
      {(emptyRepairAction || modelRouteUnavailable || selectedModelIssue || agentRouteUnavailable) && (
        <div style={{ display: "flex", justifyContent: "center", gap: 8, marginTop: 14, flexWrap: "wrap" }}>
          {emptyRepairAction && (
            <button
              className="btn btn-primary btn-sm"
              onClick={runEmptyRepairAction}
              type="button"
              style={{ display: "flex", alignItems: "center", gap: 4 }}>
              <Icon d={repairActionIcon(emptyRepairAction)} size={13} /> {emptyRepairAction.actionLabel}
            </button>
          )}
          {!emptyRepairAction && (modelRouteUnavailable || selectedModelIssue) && isHecateChat && (
            <button
              className="btn btn-primary btn-sm"
              onClick={onOpenProviders}
              type="button"
              style={{ display: "flex", alignItems: "center", gap: 4 }}>
              <Icon d={Icons.connections} size={13} /> Open Connections
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

function RTKOnboardingHint({ path, onEnable }: { path: string; onEnable: () => void }) {
  return (
    <div
      style={{
        margin: "16px auto 0",
        maxWidth: 520,
        border: "1px solid var(--teal-border)",
        borderRadius: "var(--radius)",
        background: "var(--teal-bg)",
        padding: "12px 14px",
        display: "grid",
        gap: 8,
        textAlign: "left",
      }}
    >
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 10 }}>
        <div>
          <div style={{ fontSize: 12, fontWeight: 650, color: "var(--teal)" }}>Compact command output is available</div>
          <div style={{ marginTop: 3, fontSize: 11, color: "var(--t2)", lineHeight: 1.45 }}>
            Hecate found RTK{path ? ` at ${path}` : ""}. Turn it on for this chat now, or change it later from Chat settings.
          </div>
        </div>
        <button className="btn btn-primary btn-sm" type="button" onClick={onEnable} style={{ flexShrink: 0 }}>
          Turn on
        </button>
      </div>
    </div>
  );
}

function ChatSetupRepairNotice({
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

function SelectedModelReadinessNotice({
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

function composerVisibleRepair(repair: ChatSetupRepairState | null): ChatSetupRepairState | null {
  if (!repair) return null;
  switch (repair.kind) {
    case "workspace_required":
    case "tools_disabled":
    case "external_agent_unavailable":
    case "claude_code_setup":
      return repair;
    default:
      return null;
  }
}

function emptyStateAlreadyShowsRepair(repair: ChatSetupRepairState | null, visibleMessageCount: number): boolean {
  if (!repair || visibleMessageCount > 0) return false;
  // The tools-disabled repair needs the composer notice because that notice
  // owns the capability-write busy/disabled state. Other empty-chat repairs
  // already render the same copy and CTA in ChatEmptyState.
  return repair.action !== "enable_tools";
}

function repairActionIcon(repair: ChatSetupRepairState) {
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
  const candidateKeys = candidates.map(localProviderDiscoveryKey).join("\u0000");
  const [selectedKeys, setSelectedKeys] = useState<Set<string>>(() => new Set(candidates.map(localProviderDiscoveryKey)));
  useEffect(() => {
    setSelectedKeys(new Set(candidates.map(localProviderDiscoveryKey)));
  }, [candidateKeys]);
  const selectedCandidates = candidates.filter(discovery => selectedKeys.has(localProviderDiscoveryKey(discovery)));

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
              const key = localProviderDiscoveryKey(discovery);
              const selected = selectedKeys.has(key);
              const status = localProviderReadiness(discovery);
              const modelCount = discovery.model_count ?? discovery.models?.length ?? 0;
              const detail = discovery.http_available
                ? `${discovery.base_url} · ${modelCount} model${modelCount === 1 ? "" : "s"}`
                : `${discovery.command || "Command"} found${discovery.command_path ? ` · ${discovery.command_path}` : ""}`;
              return (
                <button key={key}
                  type="button"
                  aria-pressed={selected}
                  aria-label={`${selected ? "Deselect" : "Select"} ${preset?.name || discovery.name}`}
                  onClick={() => {
                    setSelectedKeys((current) => {
                      const next = new Set(current);
                      if (next.has(key)) {
                        next.delete(key);
                      } else {
                        next.add(key);
                      }
                      return next;
                    });
                  }}
                  style={{
                    appearance: "none",
                    background: selected ? "var(--teal-bg)" : "transparent",
                    color: "inherit",
                    minHeight: 60,
                    display: "flex",
                    alignItems: "center",
                    gap: 10,
                    border: `1px solid ${selected ? "var(--teal-border)" : "var(--border)"}`,
                    borderRadius: "var(--radius)",
                    cursor: "pointer",
                    padding: "10px 12px",
                    minWidth: 0,
                    textAlign: "left",
                  }}>
                  <BrandAvatar brand={discovery.preset_id || discovery.name} fallback={preset?.name || discovery.name} size={28} />
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
                  <span
                    aria-hidden
                    style={{
                      alignItems: "center",
                      border: `1px solid ${selected ? "var(--teal-border)" : "var(--border)"}`,
                      borderRadius: 999,
                      color: selected ? "var(--teal)" : "var(--t3)",
                      display: "inline-flex",
                      flexShrink: 0,
                      height: 18,
                      justifyContent: "center",
                      width: 18,
                    }}
                  >
                    {selected && <Icon d={Icons.check} size={11} strokeWidth={2} />}
                  </span>
                </button>
              );
            })}
          </div>
          <div style={{ display: "flex", flexDirection: "column", alignItems: "center", gap: 8 }}>
            <span style={{ fontSize: 11, color: "var(--t3)", lineHeight: 1.4, textAlign: "center" }}>
              Selected {selectedCandidates.length} of {candidates.length}. You can edit names and URLs later in Connections.
            </span>
            <button
              className="btn btn-primary btn-sm"
              disabled={adding || selectedCandidates.length === 0}
              onClick={() => onAdd(selectedCandidates)}
              type="button"
              style={{ display: "flex", alignItems: "center" }}>
              {adding ? "Adding..." : "Add selected"}
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

function localProviderDiscoveryKey(discovery: LocalProviderDiscoveryRecord): string {
  return discovery.preset_id || discovery.base_url || discovery.name;
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

function formatAgentSessionLabel(session: AgentChatSessionRecord | null, adapter?: AgentAdapterRecord): string {
  const agentName = adapter?.name || (session?.adapter_id ? chatAgentOption(session.adapter_id, []).label : "External agent");
  if (!session) {
    return adapter?.available ? `${agentName} · New session` : `${agentName} · Not ready`;
  }
  return `${agentName} session · ${formatChatStatusLabel(session.status)}`;
}

function formatChatStatusLabel(status?: string): string {
  switch (status) {
    case "awaiting_approval":
      return "Waiting for approval";
    case "in_progress":
    case "running":
      return "Running";
    case "completed":
      return "Completed";
    case "cancelled":
      return "Cancelled";
    case "failed":
      return "Failed";
    case "idle":
      return "Idle";
    case "queued":
      return "Queued";
    default:
      return status ? titleCaseWords(status.replace(/[_-]+/g, " ")) : "New";
  }
}

function titleCaseWords(value: string): string {
  return value.replace(/\b\w/g, (char) => char.toUpperCase());
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
    parts.push(formatDurationMs(durationMS));
  }
  return parts.join(" · ");
}

function findLatestAgentUsage(session: AgentChatSessionRecord | null): AgentChatUsageRecord | null {
  const messages = session?.messages ?? [];
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const usage = messages[index]?.usage;
    if (usage && !agentUsageEmpty(usage)) return usage;
  }
  return null;
}

function agentUsageEmpty(usage: AgentChatUsageRecord): boolean {
  return !usage.reported_cost_amount && !usage.reported_cost_currency && !(usage.context_size ?? 0) && !(usage.context_used ?? 0);
}

function formatAgentContextUsage(usage: AgentChatUsageRecord): string {
  const used = usage.context_used ?? 0;
  const size = usage.context_size ?? 0;
  if (size > 0) return `${formatInteger(used)} / ${formatInteger(size)}`;
  if (used > 0) return formatInteger(used);
  return "—";
}

function formatAgentReportedCost(usage: AgentChatUsageRecord): string {
  if (!usage.reported_cost_amount && !usage.reported_cost_currency) return "";
  const currency = usage.reported_cost_currency ? ` ${usage.reported_cost_currency}` : "";
  return `${usage.reported_cost_amount || "0"}${currency}`;
}

function ChatSettingsPanel({
  showHecateControls,
  toolsEnabled,
  toolsDisabledForModel,
  rtkEnabled,
  rtkAvailable,
  rtkPath,
  externalAgentID,
  taskID,
  agentName,
  model,
  provider,
  workspace,
  status,
  messageCount,
  agentUsage,
  usageSource,
  externalSession,
  instructionsAvailable,
  isHecateAgentChat,
  instructionsLocked,
  systemPrompt,
  onToolsChange,
  onRTKChange,
  onConfigOptionChange,
  onSystemPromptChange,
  onCopyCommand,
}: {
  showHecateControls: boolean;
  toolsEnabled: boolean;
  toolsDisabledForModel: boolean;
  rtkEnabled: boolean;
  rtkAvailable: boolean;
  rtkPath: string;
  externalAgentID?: string;
  taskID?: string;
  agentName?: string;
  model?: string;
  provider?: string;
  workspace?: string;
  status?: string;
  messageCount: number;
  agentUsage: AgentChatUsageRecord | null;
  usageSource: "hecate" | "adapter";
  externalSession: AgentChatSessionRecord | null;
  instructionsAvailable: boolean;
  isHecateAgentChat: boolean;
  instructionsLocked: boolean;
  systemPrompt: string;
  onToolsChange: (enabled: boolean) => void;
  onRTKChange: (enabled: boolean) => void;
  onConfigOptionChange: (sessionID: string, configID: string, value: string | boolean) => Promise<boolean>;
  onSystemPromptChange: (value: string) => void;
  onCopyCommand: (command: string) => void;
}) {
  const externalRTK = !showHecateControls ? externalAgentRTKInfo(externalAgentID || "", rtkAvailable, rtkPath) : null;
  return (
    <aside
      aria-label="Chat settings panel"
      style={{
        width: "min(380px, 36vw)",
        minWidth: 320,
        maxWidth: 420,
        flexShrink: 0,
        borderLeft: "1px solid var(--border)",
        background: "var(--bg1)",
        display: "flex",
        flexDirection: "column",
        minHeight: 0,
      }}
    >
      <div
        style={{
          borderBottom: "1px solid var(--border)",
          padding: "14px 14px 12px",
          display: "flex",
          alignItems: "flex-start",
          justifyContent: "space-between",
          gap: 12,
        }}
      >
        <div>
          <div style={{ fontSize: 12, fontWeight: 650, color: "var(--t0)" }}>Chat settings</div>
          <div style={{ marginTop: 4, fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>
            {showHecateControls
              ? "Controls for future turns in this Hecate Chat. Running task turns keep the settings they started with."
              : "Adapter controls and session details for this External Agent chat. Options apply to future turns in this session."}
          </div>
        </div>
      </div>
      <div style={{ overflowY: "auto", padding: 14, display: "grid", gap: 14 }}>
        {showHecateControls && (
          <>
            <ChatSettingsSection title="Mode">
              <ChatSettingsToolsRow
                enabled={toolsEnabled}
                disabled={toolsDisabledForModel}
                onChange={onToolsChange}
              />
            </ChatSettingsSection>
            <ChatSettingsSection title="Command output">
              <ChatSettingsRTKRow
                available={rtkAvailable}
                path={rtkPath}
                enabled={rtkEnabled}
                shellArgv={rtkEnabled ? "rtk sh -lc <command>" : "sh -lc <command>"}
                onChange={onRTKChange}
              />
            </ChatSettingsSection>
          </>
        )}
        {!showHecateControls && externalSession?.config_options?.length ? (
          <ChatSettingsSection title="Adapter controls">
            <ExternalAgentSettingsControls
              session={externalSession}
              onChange={onConfigOptionChange}
            />
          </ChatSettingsSection>
        ) : null}
        {externalRTK && (
          <ChatSettingsSection title="RTK setup">
            <ChatSettingsExternalRTKRow info={externalRTK} onCopyCommand={onCopyCommand} />
          </ChatSettingsSection>
        )}
        {showHecateControls && instructionsAvailable && (
          <ChatSettingsSection title="System prompt">
            <ChatInstructionsPanel
              embedded
              isHecateAgentChat={isHecateAgentChat}
              locked={instructionsLocked}
              value={systemPrompt}
              onChange={onSystemPromptChange}
            />
          </ChatSettingsSection>
        )}
        {agentUsage && (
          <ChatSettingsSection title={usageSource === "hecate" ? "Usage" : "Reported usage"}>
            <div
              style={{
                border: "1px solid var(--border)",
                borderRadius: 12,
                background: "var(--bg1)",
                padding: 12,
                display: "grid",
                gap: 8,
              }}
            >
              <ChatSettingsField label="Context" value={formatAgentContextUsage(agentUsage)} mono />
              <ChatSettingsField label="Cost" value={formatAgentReportedCost(agentUsage) || "not reported"} mono />
              <div style={{ fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>
                {usageSource === "hecate"
                  ? "Measured by Hecate when it controls the provider or task-backed turn. Values can be empty for local providers or older turns."
                  : "Reported by the adapter for orientation. Hecate does not enforce external-agent billing."}
              </div>
            </div>
          </ChatSettingsSection>
        )}
        <ChatSettingsSection title="Session context">
          <div style={{ display: "grid", gap: 5, fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>
            {showHecateControls ? (
              <>
                <ChatSettingsField label="Provider" value={provider || "Select provider"} />
                <ChatSettingsField label="Model" value={model || "not selected"} mono />
              </>
            ) : (
              <ChatSettingsField label="Agent" value={agentName || "External agent"} />
            )}
            <ChatSettingsField label="Workspace" value={workspace || "not selected"} mono title={workspace} />
            <ChatSettingsField label="Status" value={status || "new chat"} />
            <ChatSettingsField label="Messages" value={String(messageCount)} mono />
            {taskID && <ChatSettingsField label="Task" value={shortID(taskID)} mono />}
          </div>
        </ChatSettingsSection>
      </div>
    </aside>
  );
}

function ChatSettingsSection({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section>
      <div className="kicker" style={{ marginBottom: 7 }}>{title}</div>
      {children}
    </section>
  );
}

function ChatSettingsField({ label, value, mono, title }: { label: string; value: string; mono?: boolean; title?: string }) {
  return (
    <div style={{ display: "flex", gap: 8, alignItems: "baseline" }}>
      <span style={{ color: "var(--t3)", fontSize: 11, minWidth: 78 }}>{label}</span>
      <span title={title} style={{ color: "var(--t1)", fontSize: 11, fontFamily: mono ? "var(--font-mono)" : "inherit", wordBreak: "break-all" }}>
        {value}
      </span>
    </div>
  );
}

function ChatSettingsToolsRow({
  enabled,
  disabled,
  onChange,
}: {
  enabled: boolean;
  disabled: boolean;
  onChange: (enabled: boolean) => void;
}) {
  return (
    <div
      style={{
        border: "1px solid var(--border)",
        borderRadius: 12,
        background: "var(--bg1)",
        padding: 12,
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
        gap: 14,
      }}
    >
      <div>
        <div style={{ fontSize: 12, fontWeight: 650, color: "var(--t0)" }}>Tools</div>
        <div style={{ marginTop: 3, fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>
          {enabled
            ? "Use Hecate's task runtime, approvals, artifacts, and sandboxed tool calls."
            : "Send the next turn directly to the selected provider/model without local tools."}
        </div>
        {disabled && (
          <div style={{ marginTop: 4, fontSize: 11, color: "var(--amber)", lineHeight: 1.45 }}>
            This model does not have known tool-calling support.
          </div>
        )}
      </div>
      <button
        className="btn btn-ghost btn-sm"
        type="button"
        aria-label={`Tools ${enabled ? "on" : "off"}`}
        aria-pressed={enabled}
        disabled={disabled && !enabled}
        onClick={() => onChange(!enabled)}
        style={{
          flexShrink: 0,
          minWidth: 72,
          justifyContent: "center",
          color: enabled ? "var(--teal)" : "var(--t2)",
          borderColor: enabled ? "var(--teal-border)" : "var(--border)",
          background: enabled ? "var(--teal-bg)" : "transparent",
        }}
      >
        {enabled ? "on" : "off"}
      </button>
    </div>
  );
}

function ChatSettingsRTKRow({
  available,
  path,
  enabled,
  shellArgv,
  onChange,
}: {
  available: boolean;
  path: string;
  enabled: boolean;
  shellArgv: string;
  onChange: (enabled: boolean) => void;
}) {
  return (
    <div
      style={{
        border: "1px solid var(--border)",
        borderRadius: 12,
        background: "var(--bg1)",
        padding: 12,
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
        gap: 14,
      }}
    >
      <div>
        <div style={{ fontSize: 12, fontWeight: 650, color: "var(--t0)" }}>Compact command output</div>
        <div style={{ marginTop: 3, fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>
          {available
            ? <>RTK is installed{path ? <> at <code>{path}</code></> : ""}. Hecate can run shell and git tools as <code>rtk sh -lc &lt;command&gt;</code> for shorter output.</>
            : <>RTK is not installed in the gateway PATH. Install it to enable compact shell/git output.</>}
          {" "}Hecate still applies approvals, sandbox policy, limits, and timeouts.
        </div>
        <div style={{ marginTop: 9, display: "grid", gap: 5, fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>
          <ChatSettingsField label="Shell argv" value={shellArgv} mono />
        </div>
      </div>
      <button
        className="btn btn-ghost btn-sm"
        type="button"
        aria-label={`Compact command output ${enabled ? "on" : "off"}`}
        aria-pressed={enabled}
        disabled={!available && !enabled}
        onClick={() => onChange(!enabled)}
        style={{
          flexShrink: 0,
          minWidth: 72,
          justifyContent: "center",
          color: enabled ? "var(--teal)" : "var(--t2)",
          borderColor: enabled ? "var(--teal-border)" : "var(--border)",
          background: enabled ? "var(--teal-bg)" : "transparent",
          opacity: !available && !enabled ? 0.55 : 1,
        }}
      >
        {enabled ? "on" : "off"}
      </button>
    </div>
  );
}

type ExternalAgentRTKInfo = {
  title: string;
  detail: string;
  command: string;
  verify?: string;
  tier: string;
  available: boolean;
  path: string;
};

function externalAgentRTKInfo(agentID: string, available: boolean, path: string): ExternalAgentRTKInfo | null {
  switch (agentID) {
    case "claude_code":
      return {
        title: "Claude Code shell hook",
        detail: "RTK installs a Claude Code PreToolUse hook. Hecate starts Claude Code normally; Claude rewrites shell commands through its native hook.",
        command: "rtk init --global",
        verify: "rtk init --show",
        tier: "native hook",
        available,
        path,
      };
    case "cursor_agent":
      return {
        title: "Cursor shell hook",
        detail: "RTK installs a Cursor preToolUse hook. Hecate starts Cursor Agent normally; Cursor rewrites commands before executing them.",
        command: "rtk init --global --cursor",
        verify: "rtk init --show",
        tier: "native hook",
        available,
        path,
      };
    case "codex":
      return {
        title: "Codex instructions",
        detail: "RTK patches AGENTS.md with guidance for Codex to prefer RTK-prefixed commands. This is instruction-based rather than a guaranteed hook.",
        command: "rtk init --codex",
        tier: "instructions",
        available,
        path,
      };
    default:
      return null;
  }
}

function ChatSettingsExternalRTKRow({
  info,
  onCopyCommand,
}: {
  info: ExternalAgentRTKInfo;
  onCopyCommand: (command: string) => void;
}) {
  return (
    <div
      style={{
        border: "1px solid var(--border)",
        borderRadius: 12,
        background: "var(--bg1)",
        padding: 12,
        display: "grid",
        gap: 10,
      }}
    >
      <div>
        <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
          <span style={{ fontSize: 12, fontWeight: 650, color: "var(--t0)" }}>{info.title}</span>
          <span className={info.available ? "badge badge-teal" : "badge"}>
            {info.available ? "rtk installed" : "rtk missing"}
          </span>
          <span className="badge">{info.tier}</span>
        </div>
        <div style={{ marginTop: 5, fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>
          {info.detail}
        </div>
      </div>
      {info.path && <ChatSettingsField label="RTK path" value={info.path} mono />}
      <div style={{ display: "grid", gap: 6 }}>
        <CopyCommandRow label="Setup" command={info.command} onCopy={onCopyCommand} />
        {info.verify && <CopyCommandRow label="Verify" command={info.verify} onCopy={onCopyCommand} />}
      </div>
      <div style={{ fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>
        Run setup once where the external agent reads its settings, then restart that agent if RTK requires it.
      </div>
    </div>
  );
}

function CopyCommandRow({ label, command, onCopy }: { label: string; command: string; onCopy: (command: string) => void }) {
  return (
    <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
      <span style={{ minWidth: 48, color: "var(--t3)", fontSize: 11 }}>{label}</span>
      <button
        type="button"
        className="btn btn-ghost btn-sm"
        onClick={() => onCopy(command)}
        title={`Copy ${command}`}
        style={{
          minWidth: 0,
          justifyContent: "flex-start",
          color: "var(--teal)",
          fontFamily: "var(--font-mono)",
          fontSize: 11,
          padding: "4px 7px",
        }}
      >
        <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{command}</span>
        <Icon d={Icons.copy} size={12} />
      </button>
    </div>
  );
}

function shortID(id: string): string {
  return compactID(id, ["task_", "run_", "agent_chat_"], 8);
}

function compactID(id: string, prefixes: string[], length: number): string {
  const trimmed = id.trim();
  const withoutPrefix = prefixes.reduce((current, prefix) => (
    current.startsWith(prefix) ? current.slice(prefix.length) : current
  ), trimmed);
  return withoutPrefix.slice(0, length);
}

