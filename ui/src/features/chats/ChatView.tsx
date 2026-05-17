import { useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import { useRuntimeConsoleContext } from "../../app/RuntimeConsoleContext";
import { discoverLocalProviders } from "../../lib/api";
import { resolveChatSetupRepairState, type ChatSetupRepairState } from "../../lib/chat-setup-readiness";
import { describeGatewayError } from "../../lib/error-diagnostics";
import { formatInteger, formatLocaleTime } from "../../lib/format";
import { buildSelectedModelIssue } from "../../lib/provider-issues";
import { providerDisplayName } from "../../lib/provider-utils";
import type { AgentAdapterRecord, ChatActivityRecord, ChatSegmentRecord, ChatSessionRecord, ChatUsageRecord, LocalProviderDiscoveryRecord, ProviderPresetRecord } from "../../types/runtime";
import { Icon, Icons } from "../shared/ui";
import { AgentApprovalAutoModeBanner, AgentApprovalsBanner } from "./AgentApprovalBanner";
import { AgentApprovalModal } from "./AgentApprovalModal";
import { AddProviderModal } from "../providers/AddProviderModal";
import { ChatComposer, compactID } from "./ChatComposer";
import { ChatEmptyState } from "./ChatEmptyState";
import { ChatHeader } from "./ChatHeader";
import { ChatSidebar, sidebarSessionAgentLabel, sidebarSessionBrand } from "./ChatSidebar";
import { ChatTranscript, buildTranscriptItems, type VisibleChatMessage } from "./ChatTranscript";
import { ExternalAgentSettingsControls, chatAgentOption } from "./ChatAgentControls";
import { ChatInstructionsPanel } from "./ChatInstructionsPanel";
import { ChatNoticeFrame, ChatNoticeHeader, ChatNoticeRow } from "./ChatNotice";
import { claudeCodePreflightState } from "./ClaudeCodeSetup";

type Props = {
  onNavigate?: (workspace: "connections" | "runs" | "overview" | "settings") => void;
  onOpenTask?: (taskID: string, runID?: string) => void;
  onOpenTrace?: (requestID: string) => void;
};

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
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const focusComposerAfterNewChatRef = useRef(false);

  const activeSessionIsExternal = Boolean(state.activeChatSession?.runtime_kind === "external_agent" || state.activeChatSession?.adapter_id);
  const activeSessionIsHecate = Boolean(state.activeChatSession && !activeSessionIsExternal);
  const isHecateChat = activeSessionIsHecate || (!activeSessionIsExternal && (state.chatTarget === "agent" || state.chatTarget === "model"));
  const isAgentChat = isHecateChat || state.chatTarget === "external_agent";
  const isHecateAgentChat = isHecateChat && state.chatTarget === "agent";
  const isExternalAgentChat = activeSessionIsExternal || (!activeSessionIsHecate && state.chatTarget === "external_agent");
  const externalAgentHasConfigControls = Boolean(isExternalAgentChat && state.activeChatSession?.config_options?.length);
  const instructionsAvailable = isHecateChat;
  const activeSessionID = state.activeChatSessionID;
  const chatCanvasActive = Boolean(activeSessionID || draftChatOpen);
  const activeQueuedChatMessages = activeSessionID
    ? state.queuedChatMessages.filter((queued) => queued.session_id === activeSessionID)
    : [];
  const activeTitle = state.activeChatSession?.title;
  const messages: VisibleChatMessage[] = (state.activeChatSession?.messages ?? []).map((m, index) => ({
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
  }));
  const pendingTaskApprovals = isHecateChat
    ? pendingHecateTaskApprovals(state.activeChatSession)
    : [];
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
    state.activeChatSession?.segments,
    isHecateChat,
  );
  const streaming = state.chatLoading;
  const chatDiagnostic = describeGatewayError(state.chatErrorCode, state.chatErrorStatus ?? undefined);
  const activeAgentAdapterID = state.activeChatSession?.adapter_id || state.agentAdapterID;
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
    : providerDisplayName(state.providerFilter, configuredProviders, state.providerPresets, state.providers);
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
        name: providerDisplayName(p.id, configured, state.providerPresets, state.providers),
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
  const activeHecateAgentSegment = activeTaskBackedHecateSegment(state.activeChatSession);
  const hecateAgentBusy = isHecateChat && Boolean(activeHecateAgentSegment);
  const activeHecateTaskID = activeHecateAgentSegment?.task_id || "";
  const activeHecateRunID = activeHecateAgentSegment?.latest_run_id || "";
  const hecateAgentModelLocked = isHecateChat && Boolean(activeHecateAgentSegment);
  const hecateChatProviderValue = hecateAgentModelLocked
    ? (activeHecateAgentSegment?.provider || state.activeChatSession?.provider || "auto")
    : state.providerFilter;
  const hecateChatModelValue = hecateAgentModelLocked
    ? (activeHecateAgentSegment?.model || state.activeChatSession?.model || "")
    : state.model;
  const activeHeaderBrand = isAgentChat
    ? (state.activeChatSession ? sidebarSessionBrand(state.activeChatSession) : newChatAgentID)
    : selectedConfiguredProvider?.id || selectedRuntimeProvider?.name || state.providerFilter;
  const activeHeaderFallback = isAgentChat
    ? (state.activeChatSession
        ? sidebarSessionAgentLabel(state.activeChatSession, state.agentAdapters)
        : chatAgentOption(newChatAgentID, state.agentAdapters).label)
    : selectedProviderName;
  const activeHeaderSubline = buildActiveChatHeaderSubline({
    isAgentChat,
    isExternalAgentChat,
    isHecateAgentChat,
    activeSession: state.activeChatSession,
    selectedAgent,
    newChatAgentID,
    adapters: state.agentAdapters,
  });
  const latestChatUsage = isAgentChat ? findLatestAgentUsage(state.activeChatSession) : null;
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
    ? state.activeChatSession?.capabilities
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
    && !state.activeChatSessionID
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
    const taskID = state.activeChatSession?.task_id;
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
        {chatCanvasActive && (
          <ChatHeader
            sidebarOpen={sidebarOpen}
            onOpenSidebar={() => setSidebarOpen(true)}
            brand={activeHeaderBrand}
            fallback={activeHeaderFallback}
            title={activeTitle || (state.chatSessions.length === 0 ? "New chat" : "Select a chat")}
            subline={activeHeaderSubline}
            sublineHoverTitle={isExternalAgentChat ? formatAgentSessionTitle(state.activeChatSession, selectedAgent) : activeHeaderSubline}
            isAgentChat={isAgentChat}
            isExternalAgentChat={isExternalAgentChat}
            showWorkspaceButton={showHeaderWorkspaceButton}
            workspacePath={state.agentWorkspace}
            chatSettingsOpen={chatSettingsOpen}
            onChooseWorkspace={() => void chooseWorkspace()}
            onToggleChatSettings={() => setChatSettingsOpen((open) => !open)}
            activeChatSession={state.activeChatSession}
          />
        )}

        <div style={{ flex: 1, display: "flex", minHeight: 0, overflow: "hidden" }}>
          <div style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", overflow: "hidden", position: "relative" }}>
        {!chatCanvasActive ? (
          <NoActiveChatState
            agentLabel={chatAgentOption(newChatAgentID, state.agentAdapters).label}
            hasSessions={state.chatSessions.length > 0}
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
            {state.activeChatSessionID && (
              <AgentApprovalsBanner
                pending={state.pendingApprovalsBySessionID.get(state.activeChatSessionID) ?? []}
                onSelect={(id) => setApprovalModalID(id)}
              />
            )}
          </>
        )}

        {isHecateAgentChat && state.activeChatSession?.task_id && pendingTaskApprovals.length > 0 && (
          <HecateTaskApprovalsBanner
            approvals={pendingTaskApprovals}
            taskID={state.activeChatSession.task_id}
            runID={state.activeChatSession.latest_run_id}
            busyID={taskApprovalBusyID}
            onOpenTask={onOpenTask}
            onResolve={handleResolveTaskApproval}
          />
        )}

        <ChatTranscript
          isHecateAgentChat={isHecateAgentChat}
          activeSessionID={activeSessionID}
          transcriptItems={transcriptItems}
          visibleMessageCount={visibleMessages.length}
          streaming={streaming}
          onNavigate={onNavigate}
          onOpenTask={onOpenTask}
          onOpenTrace={onOpenTrace}
          openClaudeCodeSetup={openClaudeCodeSetup}
          emptyState={
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
          }
        />

        <ChatComposer
          isAgentChat={isAgentChat}
          isHecateChat={isHecateChat}
          isExternalAgentChat={isExternalAgentChat}
          isHecateAgentChat={isHecateAgentChat}
          activeSessionID={activeSessionID}
          textareaRef={textareaRef}
          composerVisible={composerVisible}
          composerRepair={composerRepair}
          messageControlsVisible={messageControlsVisible}
          showClaudeCodeEmptyPreflight={showClaudeCodeEmptyPreflight}
          sendDisabled={sendDisabled}
          agentBusy={agentBusy}
          queueingMessage={queueingMessage}
          selectedModelIssue={selectedModelIssue}
          chatDiagnostic={chatDiagnostic}
          hecateAgentModelLocked={hecateAgentModelLocked}
          hecateChatProviderValue={hecateChatProviderValue}
          hecateChatModelValue={hecateChatModelValue}
          hecateProviderOptions={hecateProviderOptions}
          hecateDisabledProviderReasons={hecateDisabledProviderReasons}
          selectableModels={selectableModels}
          selectedAgent={selectedAgent}
          selectedAgentHealthLoading={selectedAgentHealthLoading}
          claudeCodePreflight={claudeCodePreflight}
          selectedCapabilityProvider={selectedCapabilityProvider}
          selectedCapabilityModel={selectedCapabilityModel}
          capabilitySaving={capabilitySaving}
          enableToolsForSelectedModel={enableToolsForSelectedModel}
          chooseWorkspace={chooseWorkspace}
          openClaudeCodeSetup={openClaudeCodeSetup}
          activeHecateTaskID={activeHecateTaskID}
          activeHecateRunID={activeHecateRunID}
          activeQueuedChatMessages={activeQueuedChatMessages}
          messageHistory={messageHistory}
          onNavigate={onNavigate}
          onOpenTask={onOpenTask}
          onOpenTrace={onOpenTrace}
        />
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
            taskID={state.activeChatSession?.task_id}
            agentName={selectedAgent?.name || activeHeaderFallback}
            model={state.model}
            provider={selectedProviderName}
            workspace={state.activeChatSession?.workspace || state.agentWorkspace}
            status={state.activeChatSession?.status || ""}
            messageCount={state.activeChatSession?.messages?.length ?? 0}
            agentUsage={latestChatUsage}
            usageSource={isHecateChat ? "hecate" : "adapter"}
            externalSession={isExternalAgentChat ? state.activeChatSession : null}
            instructionsAvailable={instructionsAvailable}
            isHecateAgentChat={isHecateAgentChat}
            instructionsLocked={messages.length > 0}
            systemPrompt={state.systemPrompt}
            onToolsChange={(enabled) => actions.setChatTarget(enabled ? "agent" : "model")}
            onRTKChange={handleRTKChange}
            onConfigOptionChange={actions.setChatConfigOption}
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

      {approvalModalID && isExternalAgentChat && state.activeChatSessionID && (
        <AgentApprovalModal
          sessionID={state.activeChatSessionID}
          approvalID={approvalModalID}
          onClose={() => setApprovalModalID(null)}
          fetchApproval={actions.getChatApproval}
          onResolve={actions.resolveChatApproval}
          onCancel={actions.cancelChatApproval}
        />
      )}
      <AddProviderModal
        open={addProviderOpen}
        onClose={() => setAddProviderOpen(false)}
      />
    </div>
  );
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
  activeSession: ChatSessionRecord | null;
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

function pendingHecateTaskApprovals(session: ChatSessionRecord | null): HecateTaskApproval[] {
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

function taskApprovalDisplayKind(activity: ChatActivityRecord): string {
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

function activeTaskBackedHecateSegment(session: ChatSessionRecord | null): ChatSegmentRecord | null {
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


function isQuickAddableLocalProvider(discovery: LocalProviderDiscoveryRecord): boolean {
  return discovery.http_available || discovery.command_available;
}


function normalizeProviderBaseURL(baseURL: string | undefined): string {
  return (baseURL ?? "").trim();
}


function formatAgentSessionLabel(session: ChatSessionRecord | null, adapter?: AgentAdapterRecord): string {
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

function formatAgentSessionTitle(session: ChatSessionRecord | null, adapter?: AgentAdapterRecord): string {
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


function findLatestAgentUsage(session: ChatSessionRecord | null): ChatUsageRecord | null {
  const messages = session?.messages ?? [];
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const usage = messages[index]?.usage;
    if (usage && !agentUsageEmpty(usage)) return usage;
  }
  return null;
}

function agentUsageEmpty(usage: ChatUsageRecord): boolean {
  return !usage.reported_cost_amount && !usage.reported_cost_currency && !(usage.context_size ?? 0) && !(usage.context_used ?? 0);
}

function formatAgentContextUsage(usage: ChatUsageRecord): string {
  const used = usage.context_used ?? 0;
  const size = usage.context_size ?? 0;
  if (size > 0) return `${formatInteger(used)} / ${formatInteger(size)}`;
  if (used > 0) return formatInteger(used);
  return "—";
}

function formatAgentReportedCost(usage: ChatUsageRecord): string {
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
  agentUsage: ChatUsageRecord | null;
  usageSource: "hecate" | "adapter";
  externalSession: ChatSessionRecord | null;
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
  return compactID(id, ["task_", "run_", "chat_"], 8);
}
