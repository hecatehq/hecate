import { useEffect, useMemo, useRef, useState } from "react";
import { useApprovals } from "../../app/state/approvals";
import { useChat } from "../../app/state/chat";
import { useProvidersAndModels } from "../../app/state/providersAndModels";
import { useProjects } from "../../app/state/projects";
import { useRuntime } from "../../app/state/runtime";
import { useSettings } from "../../app/state/settings";
import { useChatActions } from "../../app/state/coordinators/chat";
import { useAgentAdapterActions } from "../../app/state/coordinators/agentAdapters";
import {
  useChatTarget,
  useChatToolsEnabled,
  useNewChatAgentID,
  useRuntimeDerivedState,
} from "../../app/state/derived";
import {
  useWiredProviderActions,
  useWiredSettingsActions,
  useWiredDashboardActions,
} from "../../app/state/coordinators/wired";
import { discoverLocalProviders, draftChatProjectAssistant } from "../../lib/api";
import { writeProjectAssistantChatHandoff } from "../../lib/project-assistant-chat-handoff";
import {
  modelSelectionHasNoToolCalling,
  resolveChatSetupRepairState,
  toolCallingSupportsTaskMode,
  type ChatSetupRepairState,
} from "../../lib/chat-setup-readiness";
import { describeGatewayError } from "../../lib/error-diagnostics";
import { resolveExternalAgentReadiness } from "../../lib/external-agent-readiness";
import { buildSelectedModelIssue } from "../../lib/provider-issues";
import { providerDisplayName } from "../../lib/provider-utils";
import { projectByID, projectDefaultWorkspace } from "../../lib/project-workspace";
import type { AgentAdapterRecord } from "../../types/agent-adapter";
import type { ChatConfigOptionRecord, ChatSessionRecord, ChatUsageRecord } from "../../types/chat";
import type { LocalProviderDiscoveryRecord, ProviderFilter } from "../../types/provider";
import { AgentApprovalAutoModeBanner, AgentApprovalsBanner } from "./AgentApprovalBanner";
import { AgentApprovalModal } from "./AgentApprovalModal";
import { AddProviderModal } from "../providers/AddProviderModal";
import { ChatComposer } from "./ChatComposer";
import { ChatEmptyState } from "./ChatEmptyState";
import { ChatHeader } from "./ChatHeader";
import { ChatRightPanel } from "./ChatRightPanel";
import { ChatSettingsPanel } from "./ChatSettingsPanel";
import { ChatSidebar, sidebarSessionAgentLabel, sidebarSessionBrand } from "./ChatSidebar";
import {
  ChatTranscript,
  buildTranscriptItems,
  projectVisibleMessage,
  type VisibleChatMessage,
} from "./ChatTranscript";
import { ChatWorkspaceChangesPanel } from "./ChatWorkspaceChangesPanel";
import { externalAgentRequiresModelSelection, mergeAgentConfigOptions } from "./agentConfigOptions";
import { chatAgentOption } from "./ChatAgentControls";
import { toChatSegmentViewModel } from "./chatTurnViewModels";
import {
  HecateTaskApprovalsBanner,
  activeTaskBackedHecateSegment,
  pendingHecateTaskApprovals,
} from "./HecateTaskApprovalsBanner";

type Props = {
  onNavigate?: (workspace: "connections" | "runs" | "overview" | "settings" | "projects") => void;
  onOpenTask?: (taskID: string, runID?: string) => void;
  onOpenTrace?: (requestID: string) => void;
};

const RIGHT_PANEL_WIDTH_KEY = "hecate.chat.rightPanelWidth";
const DEFAULT_RIGHT_PANEL_WIDTH = 380;

export function ChatView({ onNavigate, onOpenTask, onOpenTrace }: Props) {
  const runtime = useRuntime();
  const chat = useChat();
  const providersAndModels = useProvidersAndModels();
  const projects = useProjects();
  const approvals = useApprovals();
  const settings = useSettings();
  const chatTarget = useChatTarget();
  const hecateChatToolsEnabled = useChatToolsEnabled();
  const newChatAgentID = useNewChatAgentID();
  const derived = useRuntimeDerivedState();
  const { actions: settingsActions } = useWiredSettingsActions();
  const chatActions = useChatActions({
    chatTarget,
    setNoticeMessage: settingsActions.setNoticeMessage,
  });
  const agentAdapterActions = useAgentAdapterActions({
    setNoticeMessage: settingsActions.setNoticeMessage,
  });
  const providerActions = useWiredProviderActions();
  const dashboardActions = useWiredDashboardActions();
  const activeProjectWorkspace = projectDefaultWorkspace(projects.activeProject);
  const agentWorkspace = chat.state.agentWorkspace || activeProjectWorkspace;
  // Compose the legacy `state` and `actions` lookalikes so the JSX
  // below stays close to the pre-migration shape. Each field is read
  // off the slice (or computed via a derived hook) the field used to
  // live on; coordinator actions stay on `chatActions` / `providerActions`
  // / `agentAdapterActions` so the call sites read the intent clearly.
  const state = {
    activeChatSession: chat.state.activeChatSession,
    activeChatSessionID: chat.state.activeChatSessionID,
    agentAdapterID: chat.state.agentAdapterID,
    agentConfigOptions: chat.state.agentConfigOptions,
    agentAdapters: providersAndModels.state.agentAdapters,
    agentAdapterApprovalMode: providersAndModels.state.agentAdapterApprovalMode,
    agentAdapterHealthByID: providersAndModels.state.agentAdapterHealthByID,
    agentAdapterHealthLoadingByID: providersAndModels.state.agentAdapterHealthLoadingByID,
    agentWorkspace,
    chatCancelling: chat.state.chatCancelling,
    chatError: chat.state.chatError,
    chatErrorCode: chat.state.chatErrorCode,
    chatErrorStatus: chat.state.chatErrorStatus,
    chatLoading: chat.state.chatLoading,
    chatSessions: chat.state.chatSessions,
    chatTarget,
    hecateRTKAvailable: runtime.state.hecateRTKAvailable,
    hecateRTKEnabled: runtime.state.hecateRTKEnabled,
    hecateRTKPath: runtime.state.hecateRTKPath,
    loading: runtime.state.loading,
    message: runtime.state.message,
    model: chat.state.model,
    newChatAgentID,
    pendingApprovalsBySessionID: approvals.state.pendingBySessionID,
    pendingToolCalls: chat.state.pendingToolCalls,
    providerFilter: chat.state.providerFilter,
    providerPresets: providersAndModels.state.providerPresets,
    providers: providersAndModels.state.providers,
    providerScopedModels: derived.providerScopedModels,
    queuedChatMessages: chat.state.queuedChatMessages,
    settingsConfig: settings.state.config,
    streamingContent: chat.state.streamingContent,
    systemPrompt: chat.state.systemPrompt,
  };
  const actions = {
    cancelAgentChat: chatActions.cancelAgentChat,
    chooseAgentWorkspace: chatActions.chooseAgentWorkspace,
    createChatSession: chatActions.createChatSession,
    copyCommand: runtime.actions.copyCommand,
    createProvider: providerActions.createProvider,
    getChatApproval: chatActions.getChatApproval,
    loadDashboard: dashboardActions.loadDashboard,
    probeAgentAdapter: agentAdapterActions.probeAgentAdapter,
    resolveChatApproval: chatActions.resolveChatApproval,
    resolveTaskApproval: chatActions.resolveTaskApproval,
    cancelChatApproval: chatActions.cancelChatApproval,
    selectChatSession: chatActions.selectChatSession,
    setAgentWorkspace: chatActions.updateAgentWorkspace,
    setChatConfigOption: chatActions.setChatConfigOption,
    setChatTarget: chatActions.setChatTarget,
    setChatToolsEnabled: chatActions.setChatToolsEnabled,
    setHecateRTKEnabled: chatActions.setHecateRTKEnabled,
    setModel: chat.actions.setModel,
    setProviderFilter: chatActions.selectProviderRoute,
    setSystemPrompt: chat.actions.setSystemPrompt,
  };
  const [sidebarOpen, setSidebarOpen] = useState(true);
  // approvalModalID is the per-banner-click open state for the
  // approval modal. The modal itself fetches the full row on mount;
  // we only carry the id here.
  const [approvalModalID, setApprovalModalID] = useState<string | null>(null);
  const [workspaceEntryOpen, setWorkspaceEntryOpen] = useState(false);
  const [workspaceDialogOpen, setWorkspaceDialogOpen] = useState(false);
  const workspaceDialogOpenRef = useRef(false);
  const [chatSettingsOpen, setChatSettingsOpen] = useState(false);
  const [workspaceChangesOpen, setWorkspaceChangesOpen] = useState(false);
  const [workspaceRefreshSignal, setWorkspaceRefreshSignal] = useState(0);
  const [rightPanelWidth, setRightPanelWidth] = useState(() => readStoredRightPanelWidth());
  const [draftChatStarted, setDraftChatStarted] = useState(false);
  const [rtkOnboardingDismissed, setRTKOnboardingDismissed] = useState(false);
  const [addProviderOpen, setAddProviderOpen] = useState(false);
  const [workspacePathValue, setWorkspacePathValue] = useState("");
  const [quickLocalProviders, setQuickLocalProviders] = useState<LocalProviderDiscoveryRecord[]>(
    [],
  );
  const [projectProposalDrafting, setProjectProposalDrafting] = useState(false);

  function updateRightPanelWidth(width: number) {
    setRightPanelWidth(width);
    rememberRightPanelWidth(width);
  }
  const [quickLocalLoading, setQuickLocalLoading] = useState(false);
  const [quickLocalError, setQuickLocalError] = useState("");
  const [quickAddingProviders, setQuickAddingProviders] = useState(false);
  const [taskApprovalBusyID, setTaskApprovalBusyID] = useState("");
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const focusComposerAfterNewChatRef = useRef(false);
  const workspaceRefreshStateRef = useRef({ agentBusy: false, sessionID: "" });

  const activeSessionIsExternal = Boolean(
    state.activeChatSession?.agent_id && state.activeChatSession.agent_id !== "hecate",
  );
  const activeSessionIsHecate = Boolean(state.activeChatSession && !activeSessionIsExternal);
  const isHecateChat =
    activeSessionIsHecate || (!activeSessionIsExternal && state.chatTarget === "agent");
  const isAgentChat = isHecateChat || state.chatTarget === "external_agent";
  // "Hecate-agent chat" semantically means: the user wants the *next
  // turn* to run through the Hecate agent path with tools enabled.
  // Three orthogonal facts must all hold:
  //   1. The user isn't routing the turn to an external agent
  //      (`chatTarget === "agent"`) — even when the active session is
  //      Hecate-backed, switching the target to external_agent should
  //      flip downstream "agent instructions" copy back to the generic
  //      "instructions" label.
  //   2. The session is on a Hecate-shaped path overall (`isHecateChat`).
  //   3. The user has tools turned on for this session
  //      (`hecateChatToolsEnabled`).
  const isHecateAgentChat = isHecateChat && state.chatTarget === "agent" && hecateChatToolsEnabled;
  const isExternalAgentChat =
    activeSessionIsExternal || (!activeSessionIsHecate && state.chatTarget === "external_agent");
  const instructionsAvailable = isHecateChat;
  const activeSessionID = state.activeChatSessionID;
  const selectedChatReady = Boolean(activeSessionID && state.activeChatSession);
  const activeQueuedChatMessages = activeSessionID
    ? state.queuedChatMessages.filter((queued) => queued.session_id === activeSessionID)
    : [];
  const activeTitle = state.activeChatSession?.title;
  // Project + derive through useMemo so a streamed snapshot only rebuilds
  // these lists when the underlying messages/segments actually change.
  // projectVisibleMessage preserves per-message reference identity (keyed
  // on the reconciled record), so the memoized transcript rows downstream
  // skip re-rendering every row that did not change.
  const messages: VisibleChatMessage[] = useMemo(
    () =>
      (state.activeChatSession?.messages ?? []).map((m, index) => projectVisibleMessage(m, index)),
    [state.activeChatSession?.messages],
  );
  const pendingTaskApprovals = isHecateChat
    ? pendingHecateTaskApprovals(state.activeChatSession)
    : [];
  // Hide system messages and any assistant placeholder that is still
  // waiting for content — the streaming-content block below renders
  // the live text instead.
  const visibleMessages = useMemo(
    () =>
      messages.filter((m) => {
        if (m.role === "system") return false;
        if (m.role === "assistant" && m.content === null) return false;
        return true;
      }),
    [messages],
  );
  const messageHistory = useMemo(
    () =>
      visibleMessages
        .filter((m) => m.role === "user" && typeof m.content === "string" && m.content.trim())
        .map((m) => (m.content ?? "").trimEnd()),
    [visibleMessages],
  );
  const transcriptItems = useMemo(
    () => buildTranscriptItems(visibleMessages, state.activeChatSession?.segments, isHecateChat),
    [visibleMessages, state.activeChatSession?.segments, isHecateChat],
  );
  const streaming = state.chatLoading;
  const chatDiagnostic = describeGatewayError(
    state.chatErrorCode,
    state.chatErrorStatus ?? undefined,
  );
  const activeAgentAdapterID =
    state.activeChatSession?.agent_id && state.activeChatSession.agent_id !== "hecate"
      ? state.activeChatSession.agent_id
      : state.agentAdapterID;
  const selectedAgent = state.agentAdapters.find((adapter) => adapter.id === activeAgentAdapterID);
  const externalAgentConfigOptions: ChatConfigOptionRecord[] = isExternalAgentChat
    ? mergeAgentConfigOptions(
        selectedAgent?.config_options ?? [],
        state.activeChatSession
          ? (state.activeChatSession.config_options ?? [])
          : state.agentConfigOptions,
      )
    : [];
  const externalAgentHasConfigControls = externalAgentConfigOptions.length > 0;
  const selectedAgentHealth = activeAgentAdapterID
    ? (state.agentAdapterHealthByID.get(activeAgentAdapterID) ?? null)
    : null;

  const selectedAgentReadiness = resolveExternalAgentReadiness(selectedAgent, selectedAgentHealth);
  const externalAgentSetupRequired = selectedAgentReadiness.needsRepair;
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
    const ids = new Set(configuredProviders.map((c) => c.id));
    return state.providerScopedModels.filter((m) => {
      const provider = m.metadata?.provider;
      return typeof provider === "string" ? ids.has(provider) : true;
    });
  })();
  const modelRouteUnavailable = providerConfigLoaded && selectableModels.length === 0;
  const hasConfiguredProviders = configuredProviders.length > 0;
  const selectedConfiguredProvider =
    state.providerFilter === "auto"
      ? configuredProviders.length === 1
        ? configuredProviders[0]
        : undefined
      : configuredProviders.find((provider) => provider.id === state.providerFilter);
  const selectedRuntimeProvider =
    state.providerFilter === "auto"
      ? state.providers.length === 1
        ? state.providers[0]
        : undefined
      : state.providers.find((provider) => provider.name === state.providerFilter);
  const selectedProviderName =
    state.providerFilter === "auto"
      ? "Select provider"
      : providerDisplayName(
          state.providerFilter,
          configuredProviders,
          state.providerPresets,
          state.providers,
        );
  const hecateProviderOptions = (() => {
    // Source the picker from the operator's configured providers
    // (the CP store), not the runtime status list. Health is not
    // a filter — a temporarily-down provider is still a valid
    // selection.
    const configured = state.settingsConfig?.providers ?? [];
    const source =
      configured.length > 0
        ? configured.map((c) => ({ id: c.id, name: c.name, kind: c.kind }))
        : state.providers
            .filter((p) => p.name)
            .map((p) => ({
              id: p.name,
              name: p.name,
              kind: state.providerPresets.find((pr) => pr.id === p.name)?.kind,
            }));

    return source.map((p) => {
      const cfg = state.settingsConfig?.providers.find((c) => c.id === p.id);
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
        disabledReason: cloudUnconfigured
          ? `Add an API key for ${cfg!.name || cfg!.id} in Connections`
          : undefined,
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
  const selectedAgentUnavailable =
    isExternalAgentChat && Boolean(selectedAgent) && !selectedAgent?.available;
  // newChatAgentID is already computed at the top of the component
  // via useNewChatAgentID(); no need to re-derive here.
  const nothingRunnable = !state.loading && modelRouteUnavailable && agentRouteUnavailable;
  const activeHecateAgentSegment = activeTaskBackedHecateSegment(state.activeChatSession);
  const hecateAgentBusy = isHecateChat && Boolean(activeHecateAgentSegment);
  const externalAgentBusy =
    isExternalAgentChat && externalAgentSessionIsBusy(state.activeChatSession);
  const activeHecateTaskID = activeHecateAgentSegment?.task_id || "";
  const activeHecateRunID = activeHecateAgentSegment?.latest_run_id || "";
  const hecateAgentModelLocked = isHecateChat && Boolean(activeHecateAgentSegment);
  const hecateChatProviderValue = hecateAgentModelLocked
    ? activeHecateAgentSegment?.provider || state.activeChatSession?.provider || "auto"
    : state.providerFilter;
  const hecateChatModelValue = hecateAgentModelLocked
    ? activeHecateAgentSegment?.model || state.activeChatSession?.model || ""
    : state.model;
  const activeHeaderBrand = isAgentChat
    ? state.activeChatSession
      ? sidebarSessionBrand(state.activeChatSession)
      : newChatAgentID
    : selectedConfiguredProvider?.id || selectedRuntimeProvider?.name || state.providerFilter;
  const activeHeaderFallback = isAgentChat
    ? state.activeChatSession
      ? sidebarSessionAgentLabel(state.activeChatSession, state.agentAdapters)
      : chatAgentOption(newChatAgentID, state.agentAdapters).label
    : selectedProviderName;
  const latestChatUsage = isAgentChat ? findLatestAgentUsage(state.activeChatSession) : null;
  const selectedModelIssue =
    !hecateAgentModelLocked && providerConfigLoaded && state.model && selectableModels.length > 0
      ? buildSelectedModelIssue({
          model: state.model,
          providerFilter: state.providerFilter,
          selectableModels,
          configuredProvider: selectedConfiguredProvider,
          runtimeProvider: selectedRuntimeProvider,
        })
      : null;
  const hecateAgentToolsDisabledForModel = hecateAgentModelLocked
    ? !toolCallingSupportsTaskMode(state.activeChatSession?.capabilities?.tool_calling)
    : modelSelectionHasNoToolCalling({
        models: selectableModels,
        providerFilter: state.providerFilter,
        model: state.model,
      });
  const hecateTaskToolsAvailable = isHecateAgentChat && !hecateAgentToolsDisabledForModel;
  const activeWorkspacePath = state.activeChatSession?.workspace || state.agentWorkspace;
  const activeSessionProjectID = state.activeChatSession?.project_id?.trim() ?? "";
  const activeSessionProject = projectByID(projects.state.projects, activeSessionProjectID);
  const linkedProjectName = activeSessionProject?.name?.trim() || activeSessionProjectID;
  const workspaceChangesPanelOpen =
    selectedChatReady && isAgentChat && workspaceChangesOpen && Boolean(activeWorkspacePath.trim());
  const chatSettingsPanelOpen = selectedChatReady && isAgentChat && chatSettingsOpen;
  const rightPanelOpen = chatSettingsPanelOpen || workspaceChangesPanelOpen;
  const rightPanelLabel = chatSettingsPanelOpen ? "Chat settings panel" : "Workspace changes panel";
  const chatSetupRepairTarget = state.chatTarget;
  const hecateChatModelReady =
    isHecateAgentChat && hecateAgentModelLocked
      ? Boolean(hecateChatModelValue)
      : Boolean(state.model) && !modelRouteUnavailable && !selectedModelIssue;
  const showHeaderWorkspaceButton = isExternalAgentChat || hecateTaskToolsAvailable;
  const activeHeaderSubline = buildActiveChatHeaderSubline({
    isAgentChat,
    isExternalAgentChat,
    isHecateAgentChat,
    hecateTaskToolsAvailable,
    activeSession: state.activeChatSession,
    selectedAgent,
    newChatAgentID,
    adapters: state.agentAdapters,
  });
  const showRTKOnboardingHint =
    isHecateChat &&
    !chatSettingsOpen &&
    !rtkOnboardingDismissed &&
    !state.activeChatSessionID &&
    visibleMessages.length === 0 &&
    activeQueuedChatMessages.length === 0 &&
    state.pendingToolCalls.length === 0 &&
    state.message.trim() === "";
  const chatSetupRepair = resolveChatSetupRepairState({
    target: chatSetupRepairTarget,
    workspaceRequired: isExternalAgentChat || hecateTaskToolsAvailable,
    hasConfiguredProviders,
    modelRouteUnavailable,
    selectedModelIssue,
    workspace: state.agentWorkspace,
    selectedAgentID: selectedAgent?.id,
    selectedAgentName: selectedAgent?.name,
    selectedAgentAvailable: Boolean(selectedAgent?.available),
    anyAgentAvailable: availableAgents.length > 0,
    externalAgentSetupRequired,
    selectedAgentReadiness,
  });
  const externalAgentModelRequired = externalAgentRequiresModelSelection(
    externalAgentConfigOptions,
  );
  const draftHecateReadyForComposer =
    isHecateChat &&
    draftChatStarted &&
    !selectedChatReady &&
    hecateChatModelReady &&
    !selectedModelIssue &&
    (!hecateTaskToolsAvailable || Boolean(state.agentWorkspace.trim()));
  const draftExternalAgentReadyForComposer =
    isExternalAgentChat &&
    draftChatStarted &&
    !selectedChatReady &&
    Boolean(selectedAgent?.available) &&
    !externalAgentSetupRequired &&
    Boolean(state.agentWorkspace.trim());
  const composerVisible =
    isAgentChat &&
    (selectedChatReady || draftHecateReadyForComposer || draftExternalAgentReadyForComposer);
  const hecateHasMessageControls =
    isHecateChat &&
    (hecateAgentModelLocked || hasConfiguredProviders || selectableModels.length > 0);
  const externalMessageControlsVisible =
    isExternalAgentChat &&
    externalAgentHasConfigControls &&
    (selectedChatReady || draftExternalAgentReadyForComposer);
  const messageControlsVisible =
    externalMessageControlsVisible ||
    ((selectedChatReady || draftHecateReadyForComposer) && hecateHasMessageControls);
  const composerShellVisible = selectedChatReady || messageControlsVisible;
  const composerRepair =
    composerVisible && !emptyStateAlreadyShowsRepair(chatSetupRepair, visibleMessages.length)
      ? composerVisibleRepair(chatSetupRepair)
      : null;
  const emptyStateExplainsModelRoute =
    visibleMessages.length === 0 &&
    (emptyStateAlreadyShowsModelRepair(chatSetupRepair, visibleMessages.length) ||
      (state.chatErrorCode === "model_not_configured" && modelRouteUnavailable));
  const emptyStateExplainsSetupRepair =
    emptyStateAlreadyShowsRepair(chatSetupRepair, visibleMessages.length) &&
    (state.chatErrorCode === "chat.workspace_required" ||
      state.chatErrorCode === "chat.model_required");
  const suppressComposerChatError =
    (state.chatErrorCode === "model_not_configured" ||
      emptyStateExplainsSetupRepair ||
      (state.chatErrorCode === "chat.model_required" &&
        (emptyStateExplainsModelRoute || externalAgentModelRequired))) &&
    !streaming &&
    state.pendingToolCalls.length === 0 &&
    (emptyStateExplainsModelRoute || emptyStateExplainsSetupRepair || externalAgentModelRequired);
  const agentBusy = isAgentChat && (streaming || hecateAgentBusy || externalAgentBusy);
  const queueingMessage = agentBusy && Boolean(state.message.trim());
  const messageSendBlocked =
    !agentBusy &&
    ((isHecateChat && !hecateChatModelReady) ||
      (isExternalAgentChat && externalAgentModelRequired));

  function handleHecateModelChange(model: string) {
    actions.setModel(model);
  }

  const sendDisabled =
    !state.message.trim() ||
    messageSendBlocked ||
    (!agentBusy && streaming) ||
    (!isAgentChat && modelRouteUnavailable) ||
    (!agentBusy &&
      isExternalAgentChat &&
      (!state.agentWorkspace.trim() || !selectedAgent?.available)) ||
    (!agentBusy && isExternalAgentChat && externalAgentSetupRequired) ||
    (!agentBusy &&
      isHecateAgentChat &&
      (!hecateChatModelReady ||
        (!hecateAgentToolsDisabledForModel && !state.agentWorkspace.trim())));
  const projectProposalAvailable =
    selectedChatReady &&
    isHecateChat &&
    !agentBusy &&
    Boolean(activeSessionProjectID) &&
    Boolean(onNavigate) &&
    Boolean(state.message.trim());

  function openAgentSetup(adapterID = activeAgentAdapterID) {
    try {
      if (adapterID) {
        sessionStorage.setItem("hecate.connectionsFocus", `external-agent-auth-setup-${adapterID}`);
      }
    } catch {
      // sessionStorage unavailable — navigation still
      // works, just no auto-scroll to the auth setup card.
    }
    onNavigate?.("connections");
  }

  function openLinkedProject() {
    const projectID = activeSessionProjectID.trim();
    if (!projectID) return;
    void projects.actions.selectProject(projectID);
    onNavigate?.("projects");
  }

  async function draftProjectProposalFromChat() {
    const sessionID = activeSessionID.trim();
    const projectID = activeSessionProjectID.trim();
    const request = state.message.trim();
    if (!sessionID || !projectID || !request || projectProposalDrafting) return;
    setProjectProposalDrafting(true);
    try {
      const payload = await draftChatProjectAssistant(sessionID, {
        request,
        draft_mode: "deterministic",
      });
      const handoffWritten = writeProjectAssistantChatHandoff({
        project_id: projectID,
        proposal: payload.data,
        request,
        source_session_id: sessionID,
        created_at: new Date().toISOString(),
      });
      if (!handoffWritten) {
        settingsActions.setNoticeMessage(
          "error",
          "Failed to hand off the proposal to Projects. Try drafting again.",
        );
        return;
      }
      runtime.actions.setMessage("");
      void projects.actions.selectProject(projectID);
      onNavigate?.("projects");
      settingsActions.setNoticeMessage(
        "success",
        "Project Assistant proposal drafted. Review it in Projects.",
      );
    } catch (error) {
      settingsActions.setNoticeMessage(
        "error",
        error instanceof Error ? error.message : "Failed to draft Project Assistant proposal.",
      );
    } finally {
      setProjectProposalDrafting(false);
    }
  }

  useEffect(() => {
    if (selectedChatReady) setDraftChatStarted(false);
  }, [selectedChatReady]);

  useEffect(() => {
    if (!selectedChatReady || !activeWorkspacePath.trim()) {
      setWorkspaceChangesOpen(false);
    }
  }, [activeWorkspacePath, selectedChatReady]);

  useEffect(() => {
    const previous = workspaceRefreshStateRef.current;
    workspaceRefreshStateRef.current = { agentBusy, sessionID: activeSessionID };
    if (
      workspaceChangesPanelOpen &&
      previous.sessionID === activeSessionID &&
      previous.agentBusy &&
      !agentBusy
    ) {
      setWorkspaceRefreshSignal((current) => current + 1);
    }
  }, [activeSessionID, agentBusy, workspaceChangesPanelOpen]);

  useEffect(() => {
    if (!focusComposerAfterNewChatRef.current || !composerVisible) return;
    const frame = requestAnimationFrame(() => {
      if (!textareaRef.current) return;
      textareaRef.current.focus();
      focusComposerAfterNewChatRef.current = false;
    });
    return () => cancelAnimationFrame(frame);
  }, [activeSessionID, composerVisible, messageControlsVisible, state.activeChatSession]);

  useEffect(() => {
    setWorkspacePathValue(state.agentWorkspace);
  }, [state.agentWorkspace]);

  useEffect(() => {
    if (
      !isHecateChat ||
      !modelRouteUnavailable ||
      hasConfiguredProviders ||
      quickLocalProviders.length > 0 ||
      quickLocalLoading
    )
      return;
    void refreshQuickLocalProviders();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isHecateChat, modelRouteUnavailable, hasConfiguredProviders]);

  async function chooseWorkspace() {
    if (workspaceDialogOpenRef.current) return;
    workspaceDialogOpenRef.current = true;
    setWorkspaceDialogOpen(true);
    setWorkspacePathValue(state.agentWorkspace);
    try {
      const selected = await actions.chooseAgentWorkspace();
      if (!selected) {
        setWorkspaceEntryOpen(true);
      }
    } finally {
      workspaceDialogOpenRef.current = false;
      setWorkspaceDialogOpen(false);
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
      setQuickLocalError(
        error instanceof Error ? error.message : "Failed to check local providers",
      );
    } finally {
      setQuickLocalLoading(false);
    }
  }

  async function quickAddLocalProviders(discoveries: LocalProviderDiscoveryRecord[]) {
    if (quickAddingProviders) return;
    const seenBaseURLs = new Set<string>();
    const addable = discoveries
      .filter((discovery) => discovery.preset_id != null)
      .filter((discovery) => {
        const preset = state.providerPresets.find((p) => p.id === discovery.preset_id);
        const baseURL = normalizeProviderBaseURL(discovery.base_url || preset?.base_url || "");
        if (!baseURL) return true;
        if (seenBaseURLs.has(baseURL)) return false;
        seenBaseURLs.add(baseURL);
        return true;
      });
    if (addable.length === 0) {
      setQuickLocalError(
        "No detected local providers are available to add. They may already be configured or share an endpoint with an existing provider.",
      );
      return;
    }

    setQuickAddingProviders(true);
    setQuickLocalError("");
    let createdCount = 0;
    let firstError: unknown = null;
    const createdDiscoveries: LocalProviderDiscoveryRecord[] = [];
    try {
      for (const discovery of addable) {
        const preset = state.providerPresets.find((p) => p.id === discovery.preset_id);
        try {
          await actions.createProvider(
            {
              name: preset?.name ?? discovery.name,
              preset_id: discovery.preset_id ?? preset?.id,
              base_url: discovery.base_url || preset?.base_url || "",
              kind: preset?.kind ?? "local",
              protocol: preset?.protocol ?? "openai",
            },
            { refresh: false },
          );
          createdCount++;
          createdDiscoveries.push(discovery);
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
        const hasModels = (discovery: LocalProviderDiscoveryRecord) =>
          (discovery.model_count ?? discovery.models?.length ?? 0) > 0;
        const isHealthy = (discovery: LocalProviderDiscoveryRecord) =>
          discovery.status === "running" || discovery.http_available;
        const preferred =
          createdDiscoveries.find((discovery) => isHealthy(discovery) && hasModels(discovery)) ??
          createdDiscoveries.find(hasModels) ??
          createdDiscoveries[0];
        if (preferred?.preset_id) {
          actions.setProviderFilter(preferred.preset_id as ProviderFilter);
          const preferredModel = preferred.models?.[0];
          if (preferredModel) {
            actions.setModel(preferredModel);
          }
          setDraftChatStarted(true);
          focusComposerWhenReady();
        }
      }
      if (firstError) {
        setQuickLocalError(
          firstError instanceof Error
            ? firstError.message
            : "Some detected providers could not be added",
        );
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
  }

  return (
    <div style={{ display: "flex", height: "100%", overflow: "hidden" }}>
      {sidebarOpen && (
        <ChatSidebar
          isAgentChat={isAgentChat}
          onSelectSession={(sessionID) => {
            setDraftChatStarted(!sessionID);
            focusComposerWhenReady();
            void actions.selectChatSession(sessionID);
            textareaRef.current?.focus();
          }}
          onCreateChat={(agentID, projectID) => {
            setChatSettingsOpen(false);
            setDraftChatStarted(true);
            focusComposerWhenReady();
            void actions.createChatSession({ agentID, projectID });
          }}
        />
      )}

      {/* Chats main */}
      <div
        style={{
          flex: 1,
          display: "flex",
          flexDirection: "column",
          overflow: "hidden",
          minWidth: 0,
          position: "relative",
        }}
      >
        {selectedChatReady && (
          <ChatHeader
            sidebarOpen={sidebarOpen}
            onOpenSidebar={() => setSidebarOpen(true)}
            brand={activeHeaderBrand}
            fallback={activeHeaderFallback}
            title={activeTitle || "New chat"}
            subline={activeHeaderSubline}
            sublineHoverTitle={
              isExternalAgentChat
                ? formatAgentSessionTitle(state.activeChatSession, selectedAgent)
                : activeHeaderSubline
            }
            isAgentChat={isAgentChat}
            isExternalAgentChat={isExternalAgentChat}
            linkedProjectName={linkedProjectName}
            onOpenProject={
              activeSessionProjectID && onNavigate ? () => openLinkedProject() : undefined
            }
            showWorkspaceButton={showHeaderWorkspaceButton}
            workspacePath={activeWorkspacePath}
            workspaceDialogOpen={workspaceDialogOpen}
            workspaceChangesOpen={workspaceChangesOpen}
            chatSettingsOpen={chatSettingsOpen}
            onChooseWorkspace={() => void chooseWorkspace()}
            onToggleWorkspaceChanges={() => {
              setChatSettingsOpen(false);
              setWorkspaceChangesOpen((open) => !open);
            }}
            onToggleChatSettings={() => {
              setWorkspaceChangesOpen(false);
              setChatSettingsOpen((open) => !open);
            }}
            activeChatSession={state.activeChatSession}
          />
        )}

        <div style={{ flex: 1, display: "flex", minHeight: 0, overflow: "hidden" }}>
          <div
            style={{
              flex: 1,
              minWidth: 0,
              display: "flex",
              flexDirection: "column",
              overflow: "hidden",
              position: "relative",
            }}
          >
            {isAgentChat && workspaceEntryOpen && (
              <div
                style={{
                  borderBottom: "1px solid var(--border)",
                  padding: "10px 14px",
                  background: "var(--bg2)",
                  display: "flex",
                  alignItems: "center",
                  gap: 8,
                }}
              >
                <span
                  style={{
                    fontSize: 11,
                    color: "var(--t2)",
                    fontFamily: "var(--font-mono)",
                    flexShrink: 0,
                  }}
                >
                  WORKSPACE PATH
                </span>
                <input
                  className="input"
                  onChange={(e) => setWorkspacePathValue(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") {
                      e.preventDefault();
                      useTypedWorkspace();
                    }
                  }}
                  placeholder="/Users/alice/dev/project"
                  style={{ height: 30, minWidth: 0 }}
                  value={workspacePathValue}
                />
                <button
                  className="btn btn-primary btn-sm"
                  disabled={!workspacePathValue.trim()}
                  onClick={useTypedWorkspace}
                  type="button"
                >
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

            {isHecateAgentChat &&
              state.activeChatSession?.task_id &&
              pendingTaskApprovals.length > 0 && (
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
              onOpenWorkspaceChanges={() => {
                setChatSettingsOpen(false);
                setWorkspaceChangesOpen(true);
              }}
              openExternalAgentSetup={openAgentSetup}
              emptyState={
                <ChatEmptyState
                  isAgentChat={isAgentChat}
                  isHecateChat={isHecateChat}
                  isExternalAgentChat={isExternalAgentChat}
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
                  onOpenAgentSetup={() => openAgentSetup()}
                  onQuickAddLocalProviders={quickAddLocalProviders}
                  onRefreshQuickLocalProviders={refreshQuickLocalProviders}
                  onSwitchTarget={actions.setChatTarget}
                  rtkAvailable={state.hecateRTKAvailable}
                  rtkPath={state.hecateRTKPath}
                  rtkEnabled={state.hecateRTKEnabled}
                  showRTKOnboardingHint={showRTKOnboardingHint}
                  onEnableRTK={() => void actions.setHecateRTKEnabled(true)}
                />
              }
            />

            {composerShellVisible && (
              <ChatComposer
                isAgentChat={isAgentChat}
                isHecateChat={isHecateChat}
                isExternalAgentChat={isExternalAgentChat}
                hecateTaskToolsAvailable={hecateTaskToolsAvailable}
                activeSessionID={activeSessionID}
                textareaRef={textareaRef}
                composerVisible={composerVisible}
                composerRepair={composerRepair}
                suppressChatError={suppressComposerChatError}
                messageControlsVisible={messageControlsVisible}
                messageSendBlocked={messageSendBlocked}
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
                onHecateModelChange={handleHecateModelChange}
                chooseWorkspace={chooseWorkspace}
                openExternalAgentSetup={openAgentSetup}
                activeHecateTaskID={activeHecateTaskID}
                activeHecateRunID={activeHecateRunID}
                activeQueuedChatMessages={activeQueuedChatMessages}
                projectProposalAvailable={projectProposalAvailable}
                projectProposalDrafting={projectProposalDrafting}
                messageHistory={messageHistory}
                onDraftProjectProposal={() => void draftProjectProposalFromChat()}
                onNavigate={onNavigate}
                onOpenTask={onOpenTask}
                onOpenTrace={onOpenTrace}
              />
            )}
          </div>
          {rightPanelOpen && (
            <ChatRightPanel
              ariaLabel={rightPanelLabel}
              width={rightPanelWidth}
              onWidthChange={updateRightPanelWidth}
            >
              {chatSettingsPanelOpen ? (
                <ChatSettingsPanel
                  showHecateControls={isHecateChat}
                  toolsEnabled={isHecateChat && hecateChatToolsEnabled}
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
                  onToolsChange={actions.setChatToolsEnabled}
                  onRTKChange={handleRTKChange}
                  onConfigOptionChange={actions.setChatConfigOption}
                  onSystemPromptChange={actions.setSystemPrompt}
                  onCopyCommand={actions.copyCommand}
                />
              ) : (
                <ChatWorkspaceChangesPanel
                  sessionID={activeSessionID}
                  workspace={activeWorkspacePath}
                  refreshSignal={workspaceRefreshSignal}
                  onGetWorkspaceDiff={chatActions.getChatWorkspaceDiff}
                  onGetWorkspaceFiles={chatActions.getChatWorkspaceFiles}
                  onGetWorkspaceFileDiff={chatActions.getChatWorkspaceFileDiff}
                  onRevertWorkspaceFiles={chatActions.revertChatWorkspaceFiles}
                />
              )}
            </ChatRightPanel>
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
      <AddProviderModal open={addProviderOpen} onClose={() => setAddProviderOpen(false)} />
    </div>
  );
}

function buildActiveChatHeaderSubline({
  isAgentChat,
  isExternalAgentChat,
  isHecateAgentChat,
  hecateTaskToolsAvailable,
  activeSession,
  selectedAgent,
  newChatAgentID,
  adapters,
}: {
  isAgentChat: boolean;
  isExternalAgentChat: boolean;
  isHecateAgentChat: boolean;
  hecateTaskToolsAvailable: boolean;
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
    return [base, activeSession?.workspace || ""].filter(Boolean).join(" · ");
  }
  const mode = isHecateAgentChat
    ? hecateTaskToolsAvailable
      ? "Tools on"
      : "Direct chat · tools unavailable"
    : "Tools off";
  return [mode, activeSession?.workspace || ""].filter(Boolean).join(" · ");
}

function composerVisibleRepair(repair: ChatSetupRepairState | null): ChatSetupRepairState | null {
  if (!repair) return null;
  switch (repair.kind) {
    case "workspace_required":
    case "external_agent_unavailable":
    case "external_agent_setup":
      return repair;
    default:
      return null;
  }
}

function emptyStateAlreadyShowsRepair(
  repair: ChatSetupRepairState | null,
  visibleMessageCount: number,
): boolean {
  if (!repair || visibleMessageCount > 0) return false;
  return true;
}

function emptyStateAlreadyShowsModelRepair(
  repair: ChatSetupRepairState | null,
  visibleMessageCount: number,
): boolean {
  if (!repair || visibleMessageCount > 0) return false;
  return (
    repair.kind === "no_provider" ||
    repair.kind === "no_routable_model" ||
    repair.kind === "selected_model_not_ready"
  );
}

function isQuickAddableLocalProvider(discovery: LocalProviderDiscoveryRecord): boolean {
  return discovery.http_available || discovery.command_available;
}

function normalizeProviderBaseURL(baseURL: string | undefined): string {
  return (baseURL ?? "").trim();
}

function formatAgentSessionLabel(
  session: ChatSessionRecord | null,
  adapter?: AgentAdapterRecord,
): string {
  const agentName =
    adapter?.name ||
    (session?.agent_id && session.agent_id !== "hecate"
      ? chatAgentOption(session.agent_id, []).label
      : "External agent");
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

function formatAgentSessionTitle(
  session: ChatSessionRecord | null,
  adapter?: AgentAdapterRecord,
): string {
  if (!session) {
    return adapter?.available
      ? `A new ${adapter.name} session will be created on send.`
      : "Install or authenticate the local agent before sending.";
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

function externalAgentSessionIsBusy(session: ChatSessionRecord | null): boolean {
  const busy = (status?: string) =>
    status === "queued" ||
    status === "running" ||
    status === "in_progress" ||
    status === "awaiting_approval" ||
    status === "pending";
  if (!session?.agent_id || session.agent_id === "hecate") return false;
  if (busy(session.status)) return true;
  if (
    (session.segments ?? []).some((segment) => {
      const turn = toChatSegmentViewModel(segment);
      return turn.isExternalAgent && busy(turn.status);
    })
  ) {
    return true;
  }
  return (session.messages ?? []).some(
    (message) => message.role === "assistant" && busy(message.status),
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

function readStoredRightPanelWidth(): number {
  try {
    const value = Number.parseInt(localStorage.getItem(RIGHT_PANEL_WIDTH_KEY) ?? "", 10);
    return Number.isFinite(value) && value > 0 ? value : DEFAULT_RIGHT_PANEL_WIDTH;
  } catch {
    return DEFAULT_RIGHT_PANEL_WIDTH;
  }
}

function rememberRightPanelWidth(width: number) {
  try {
    localStorage.setItem(RIGHT_PANEL_WIDTH_KEY, String(width));
  } catch {
    // Best-effort preference only.
  }
}
