// Chat coordinator: the largest bundle. Owns chat submission,
// session lifecycle, target routing, file + config operations,
// approvals, and the reset helpers. These cross-reference each
// other heavily (submitAgentChat → applyChatSession,
// refreshChatSession → applyChatSession,
// resolveTaskApproval → refreshChatSession, …) so keeping them in
// one hook closure avoids inter-hook plumbing.
//
// applyChatSession, syncHecateSelectionFromSession, and
// refreshRuntimeState are also consumed by the dashboard
// coordinator (loadDashboard threads them through the snapshot
// commit + the secondary refresh path). They live here because
// chat is their primary home; the dashboard hook re-exposes them.

import { useContext, type SyntheticEvent } from "react";

import { applyOverride, CoordinatorOverridesContext } from "./overrides";
import {
  ApiError,
  type ChatMessage,
  cancelChatSession as cancelChatSessionRequest,
  chatCompletionsStream,
  chooseWorkspaceDirectory as chooseWorkspaceDirectoryRequest,
  compactChatSession as compactChatSessionRequest,
  createChatMessage as createChatMessageRequest,
  createChatSession as createChatSessionRequest,
  deleteChatSession as deleteChatSessionRequest,
  getChatMessageFileDiff as getChatMessageFileDiffRequest,
  getChatWorkspaceDiff as getChatWorkspaceDiffRequest,
  getChatWorkspaceFileDiff as getChatWorkspaceFileDiffRequest,
  getChatWorkspaceFiles as getChatWorkspaceFilesRequest,
  getChatSession,
  getUsageEvents,
  getUsageSummary,
  listChatMessageFiles as listChatMessageFilesRequest,
  type ResolveChatApprovalPayload,
  type ResolveTaskApprovalPayload,
  resolveTaskApproval as resolveTaskApprovalRequest,
  revertChatMessageFiles as revertChatMessageFilesRequest,
  revertChatWorkspaceFiles as revertChatWorkspaceFilesRequest,
  setChatConfigOption as setChatConfigOptionRequest,
  setChatSettings as setChatSettingsRequest,
  streamChatSession,
  updateChatSession as updateChatSessionRequest,
} from "../../../lib/api";
import {
  buildAssistantToolCallMessage,
  buildSyntheticChatResult,
  defaultModelForProvider,
  deriveChatSessionTitle,
  isModelValidForProvider,
  renderChatSessionSummary,
} from "../../runtimeConsoleChatHelpers";
import { mcpServerFormEntriesToPayload } from "../../../lib/mcp-server-form";
import { modelSelectionHasNoToolCalling } from "../../../lib/chat-setup-readiness";
import { projectByID, projectDefaultWorkspace } from "../../../lib/project-workspace";
import {
  toChatMessageViewModel,
  toChatSegmentViewModel,
} from "../../../features/chats/chatTurnViewModels";
import {
  type ChatExecutionMode,
  type ChatTarget,
  type QueuedChatMessage,
  chatTargetToExecutionMode,
} from "../_shared";
import { useApprovals } from "../approvals";
import { useChat } from "../chat";
import { reconcileChatSession } from "../reconcileChatSession";
import { useProjects } from "../projects";
import { useProvidersAndModels } from "../providersAndModels";
import { useRuntime } from "../runtime";
import { useSettings } from "../settings";
import { useUsage } from "../usage";
import type { RuntimeHeaders } from "../../../types/runtime";
import type {
  ConfiguredStateResponse,
  ProviderFilter,
  ProviderPresetRecord,
  ProviderStatusResponse,
} from "../../../types/provider";
import type { ModelRecord } from "../../../types/model";
import type {
  ChatActivityRecord,
  ChatApprovalRecord,
  ChatChangedFileDiffRecord,
  ChatChangedFileRecord,
  ChatWorkspaceDiffRecord,
  ChatWorkspaceFilesRecord,
  ChatResponse,
  ChatSessionSummaryRecord,
  ChatSessionRecord,
} from "../../../types/chat";
import type { SettingsActions } from "./settings";

export type UseChatActionsParams = {
  chatTarget: ChatTarget;
  setNoticeMessage: SettingsActions["setNoticeMessage"];
};

function chatSessionIsExternal(session: ChatSessionRecord | null): boolean {
  return Boolean(session?.agent_id && session.agent_id !== "hecate");
}

function chatSessionIsBusy(session: ChatSessionRecord | null): boolean {
  const busy = (status?: string) =>
    status === "queued" || status === "running" || status === "awaiting_approval";
  if (!session) return false;
  if (busy(session.status)) return true;
  if ((session.segments ?? []).some((segment) => busy(segment.status))) return true;
  return (session.messages ?? []).some(
    (message) => message.role === "assistant" && busy(message.status),
  );
}

function isExpectedHecateChatSetupError(error: unknown): boolean {
  if (!(error instanceof ApiError)) return false;
  return (
    error.code === "chat.workspace_required" ||
    error.code === "chat.model_required" ||
    error.code === "model_not_configured" ||
    error.code === "route.no_routable_provider"
  );
}

function chatWorkspaceRequiredError(): ApiError {
  return new ApiError(
    "Choose a workspace before using Hecate Chat tools or External Agent.",
    400,
    "chat.workspace_required",
    {
      operatorAction: "Choose a workspace, or use Hecate direct model chat.",
    },
  );
}

function chatModelRequiredError(): ApiError {
  return new ApiError("Choose a model before starting this chat.", 400, "chat.model_required", {
    operatorAction: "Open Connections or choose a model from the composer.",
  });
}

function deriveHecateChatSelectionFromSession(session: ChatSessionRecord | null): {
  provider: string;
  model: string;
} {
  if (!session || chatSessionIsExternal(session)) {
    return { provider: "", model: "" };
  }
  const segments = [...(session.segments ?? [])].reverse();
  const segment = segments.find((item) => toChatSegmentViewModel(item).isHecateOwned);
  if (segment?.provider || segment?.model) {
    return { provider: segment.provider ?? "", model: segment.model ?? "" };
  }
  const messages = [...(session.messages ?? [])].reverse();
  const message = messages.find((item) => toChatMessageViewModel(item).isHecateOwned);
  if (message?.provider || message?.model) {
    return { provider: message.provider ?? "", model: message.model ?? "" };
  }
  return { provider: session.provider ?? "", model: session.model ?? "" };
}

function effectiveHecateToolsEnabled({
  requested,
  models,
  providerFilter,
  model,
  toolsEnabled,
}: {
  requested: ChatExecutionMode;
  models: ModelRecord[];
  providerFilter: ProviderFilter;
  model: string;
  toolsEnabled: boolean;
}): boolean {
  if (requested !== "hecate_task") return true;
  if (!toolsEnabled) return false;
  return !modelSelectionHasNoToolCalling({ models, providerFilter, model });
}

function modelAvailableForProviderFilter(
  models: ModelRecord[],
  providers: ProviderStatusResponse["data"],
  configuredProviders: ConfiguredStateResponse["data"]["providers"],
  providerPresets: ProviderPresetRecord[],
  providerFilter: ProviderFilter,
  model: string,
): boolean {
  if (!model.trim()) return false;
  if (providerFilter === "auto") {
    const knownProviders = new Set<string>();
    for (const entry of models) {
      if (entry.id === model && typeof entry.metadata?.provider === "string") {
        knownProviders.add(entry.metadata.provider);
      }
    }
    for (const provider of providers) {
      if (provider.default_model === model || provider.models?.includes(model)) {
        knownProviders.add(provider.name);
      }
    }
    for (const configured of configuredProviders) {
      knownProviders.add(configured.id);
    }
    return Array.from(knownProviders).some((provider) =>
      isModelValidForProvider(
        model,
        provider,
        models,
        providers,
        configuredProviders,
        providerPresets,
      ),
    );
  }
  return isModelValidForProvider(
    model,
    providerFilter,
    models,
    providers,
    configuredProviders,
    providerPresets,
  );
}

export { chatSessionIsExternal, chatSessionIsBusy };

export function findReusableEmptyDraftSession(
  sessions: ChatSessionSummaryRecord[],
  {
    agentID,
    model,
    projectID,
    provider,
    title,
  }: {
    agentID: string;
    model: string;
    projectID: string;
    provider: string;
    title: string;
  },
): ChatSessionSummaryRecord | null {
  const expectedAgentID = agentID.trim() || "hecate";
  const expectedProjectID = projectID.trim();
  const expectedProvider = provider.trim();
  const expectedModel = model.trim();
  const expectedTitle = title.trim();
  if (!expectedTitle) return null;
  return (
    sessions.find((session) => {
      const sessionAgentID = (session.agent_id ?? "hecate").trim() || "hecate";
      const sessionProjectID = (session.project_id ?? "").trim();
      const sessionProvider = (session.provider ?? "").trim();
      const sessionModel = (session.model ?? "").trim();
      const sessionTitle = (session.title ?? "").trim();
      const sessionStatus = (session.status ?? "").trim();
      return (
        sessionAgentID === expectedAgentID &&
        sessionProjectID === expectedProjectID &&
        sessionProvider === expectedProvider &&
        sessionModel === expectedModel &&
        sessionTitle === expectedTitle &&
        (session.message_count ?? 0) === 0 &&
        (!sessionStatus || sessionStatus === "idle")
      );
    }) ?? null
  );
}

export type CreateChatSessionOptions = {
  agentID?: string;
  projectID?: string;
  provider?: string;
  model?: string;
  title?: string;
  draft?: string;
  reuseEmptyDraft?: boolean;
};

export type SelectChatSessionOptions = {
  draft?: string;
};

type ChatActionsReturn = {
  applyChatSession: (session: ChatSessionRecord) => void;
  syncHecateSelectionFromSession: (session: ChatSessionRecord | null) => void;
  refreshRuntimeState: () => Promise<void>;
  refreshChatSession: (sessionID: string) => Promise<void>;
  clearPendingToolState: () => void;
  resetChatWorkspaceState: () => void;
  submitAgentChat: (queued?: QueuedChatMessage) => Promise<void>;
  submitChat: (event: SyntheticEvent<HTMLFormElement>) => Promise<void>;
  cancelAgentChat: () => Promise<void>;
  compactChatSession: (sessionID?: string) => Promise<boolean>;
  updateToolResult: (index: number, result: string) => void;
  submitToolResults: () => Promise<void>;
  createChatSession: (options?: CreateChatSessionOptions) => Promise<void>;
  selectChatSession: (id: string, options?: SelectChatSessionOptions) => Promise<boolean>;
  startNewChat: () => void;
  deleteChatSession: (id: string) => Promise<void>;
  renameChatSession: (id: string, title: string) => Promise<void>;
  setChatTarget: (nextTarget: ChatTarget) => void;
  // Pin the tools-on/off state for the currently active session, or —
  // when no session is active — set the user default. Mirrors the
  // setChatTarget split: per-session override map + global default,
  // resolved by `useChatToolsEnabled`. The Hecate chat-settings panel
  // is the only caller today; new turn-level UX should funnel through
  // here so the resolution stays single-source.
  setChatToolsEnabled: (enabled: boolean) => void;
  setNewChatAgent: (nextAgentID: string) => void;
  updateAgentWorkspace: (nextWorkspace: string) => void;
  selectProviderRoute: (nextProvider: ProviderFilter) => void;
  chooseAgentWorkspace: () => Promise<boolean>;
  getChatApproval: (sessionID: string, approvalID: string) => Promise<ChatApprovalRecord | null>;
  resolveChatApproval: (
    sessionID: string,
    approvalID: string,
    decision: ResolveChatApprovalPayload,
  ) => Promise<boolean>;
  cancelChatApproval: (sessionID: string, approvalID: string) => Promise<boolean>;
  resolveTaskApproval: (
    taskID: string,
    approvalID: string,
    decision: ResolveTaskApprovalPayload,
  ) => Promise<boolean>;
  deleteChatGrant: (grantID: string) => Promise<boolean>;
  listChatMessageFiles: (sessionID: string, messageID: string) => Promise<ChatChangedFileRecord[]>;
  getChatWorkspaceDiff: (sessionID: string) => Promise<ChatWorkspaceDiffRecord | null>;
  getChatWorkspaceFiles: (sessionID: string) => Promise<ChatWorkspaceFilesRecord | null>;
  getChatWorkspaceFileDiff: (
    sessionID: string,
    path: string,
  ) => Promise<ChatChangedFileDiffRecord | null>;
  revertChatWorkspaceFiles: (
    sessionID: string,
    paths: string[],
  ) => Promise<ChatWorkspaceDiffRecord | null>;
  getChatMessageFileDiff: (
    sessionID: string,
    messageID: string,
    path: string,
  ) => Promise<ChatChangedFileDiffRecord | null>;
  revertChatMessageFiles: (
    sessionID: string,
    messageID: string,
    paths: string[],
  ) => Promise<boolean>;
  setChatConfigOption: (
    sessionID: string,
    configID: string,
    value: string | boolean,
  ) => Promise<boolean>;
  setHecateRTKEnabled: (enabled: boolean) => Promise<boolean>;
};
export type ChatActions = ChatActionsReturn;

export function useChatActions(params: UseChatActionsParams): ChatActionsReturn {
  const runtime = useRuntime();
  const usage = useUsage();
  const providersAndModels = useProvidersAndModels();
  const chat = useChat();
  const projects = useProjects();
  const approvals = useApprovals();
  const settings = useSettings();

  const { message, hecateRTKEnabled } = runtime.state;
  const {
    setMessage,
    setRuntimeHeaders,
    setHecateRTKEnabled: setHecateRTKEnabledState,
  } = runtime.actions;
  const { setSummary: setUsageSummary, setEvents: setUsageEvents } = usage.actions;
  const { agentAdapters, models, providers, providerPresets } = providersAndModels.state;
  const configuredProviders = settings.state.config?.providers ?? [];
  const activeProjectID = projects.activeProjectID.trim();
  const {
    defaultChatTarget,
    defaultChatToolsEnabled,
    chatToolsEnabledBySessionID,
    agentAdapterID,
    agentConfigOptions,
    agentMCPServers,
    agentWorkspace,
    activeChatSessionID,
    activeChatSession,
    composerDraftsBySessionID,
    model,
    systemPrompt,
    pendingToolCalls,
    pendingThread,
    providerFilter,
  } = chat.state;
  const {
    beginActiveChatTransition,
    completeActiveChatTransition,
    isCurrentActiveChatTransition,
    setDefaultChatTarget,
    setChatTargetBySessionID,
    setDefaultChatToolsEnabled,
    setChatToolsEnabledBySessionID,
    setAgentAdapterID,
    setAgentConfigOptions,
    setAgentMCPServers,
    setAgentWorkspace,
    setAgentWorkspaceBranch,
    setChatSessions,
    setActiveChatSessionID,
    setActiveChatSession,
    setComposerDraftsBySessionID,
    setQueuedChatMessages,
    setModel,
    setSystemPrompt,
    setChatLoading,
    setChatCancelling,
    setStreamingContent,
    setChatResult,
    setPendingToolCalls,
    setPendingThread,
    setProviderFilter,
    setChatError,
    clearChatErrorState,
    setChatErrorState,
  } = chat.actions;
  const upsertPendingApproval = approvals.actions.upsertPending;
  const removePendingApproval = approvals.actions.removePending;

  function clearPendingToolState() {
    setPendingToolCalls([]);
    setPendingThread(null);
  }

  function resetChatWorkspaceState() {
    setMessage("");
    setChatResult(null);
    setStreamingContent(null);
    setRuntimeHeaders(null);
    clearPendingToolState();
    clearChatErrorState();
    setSystemPrompt("");
  }

  function rememberChatComposerDraft(sessionID: string, draft: string) {
    if (!sessionID) return;
    setComposerDraftsBySessionID((current) => {
      if (current.has(sessionID) && current.get(sessionID) === draft) return current;
      const next = new Map(current);
      next.set(sessionID, draft);
      return next;
    });
  }

  async function refreshRuntimeState() {
    try {
      const usageSummaryResult = await getUsageSummary("");
      setUsageSummary(usageSummaryResult.data);
    } catch {
      // Keep chat responsive even if refresh paths fail.
    }
    try {
      const usageEventsResult = await getUsageEvents(20);
      setUsageEvents(usageEventsResult.data ?? []);
    } catch {
      // Best-effort.
    }
  }

  function buildChatPayload(messages: ChatMessage[], sessionID?: string) {
    return {
      model,
      provider: providerFilter === "auto" ? "" : providerFilter,
      session_id: sessionID,
      user: "",
      messages,
    };
  }

  function selectProviderRoute(nextProvider: ProviderFilter) {
    setProviderFilter(nextProvider);
    setModel(
      defaultModelForProvider(
        nextProvider,
        models,
        providers,
        configuredProviders,
        providerPresets,
      ),
    );
  }

  function updateAgentWorkspace(nextWorkspace: string) {
    setAgentWorkspace(nextWorkspace);
    setAgentWorkspaceBranch("");
  }

  function workspaceForProjectID(projectID: string): string {
    const id = projectID.trim();
    if (!id) return "";
    const project =
      id === activeProjectID ? projects.activeProject : projectByID(projects.state.projects, id);
    return projectDefaultWorkspace(project);
  }

  function workspaceForNewChat(projectID: string): string {
    const id = projectID.trim();
    const inherited = workspaceForProjectID(id);
    if (inherited) return inherited;
    return id ? "" : agentWorkspace.trim();
  }

  function workspaceForActiveTurn(): string {
    const selectedWorkspace =
      activeChatSession?.id === activeChatSessionID
        ? activeChatSession.workspace?.trim() || ""
        : "";
    return selectedWorkspace || workspaceForNewChat(activeProjectID);
  }

  function setChatTarget(nextTarget: ChatTarget) {
    if (activeChatSessionID && activeChatSession) {
      const currentExternal = chatSessionIsExternal(activeChatSession);
      const nextExternal = nextTarget === "external_agent";
      if (currentExternal !== nextExternal) {
        setActiveChatSessionID("");
        setActiveChatSession(null);
        setAgentWorkspaceBranch("");
        setDefaultChatTarget(nextTarget);
        return;
      }
      if (!nextExternal) {
        setChatTargetBySessionID((current) => {
          const next = new Map(current);
          next.set(activeChatSessionID, nextTarget);
          return next;
        });
        return;
      }
    }
    setDefaultChatTarget(nextTarget);
  }

  // Resolve the tools-enabled flag for the given session, falling back
  // to the user default if no per-session override is set. Mirrors the
  // useChatToolsEnabled derived hook's order *minus* the message-tail
  // inspection — coordinator code paths fire after the user has already
  // toggled the panel, so deriving from prior turns here would cause a
  // freshly-toggled "tools off" to be ignored when the previous turn
  // ran with hecate_task.
  function resolveToolsEnabled(sessionID: string): boolean {
    if (!sessionID) return defaultChatToolsEnabled;
    const explicit = chatToolsEnabledBySessionID.get(sessionID);
    if (typeof explicit === "boolean") return explicit;
    return defaultChatToolsEnabled;
  }

  function setChatToolsEnabled(enabled: boolean) {
    if (activeChatSessionID) {
      setChatToolsEnabledBySessionID((current) => {
        const next = new Map(current);
        next.set(activeChatSessionID, enabled);
        return next;
      });
      return;
    }
    setDefaultChatToolsEnabled(enabled);
  }

  function setNewChatAgent(nextAgentID: string) {
    if (nextAgentID === "hecate") {
      setAgentConfigOptions([]);
      setAgentMCPServers([]);
      setDefaultChatTarget("agent");
      return;
    }
    setAgentAdapterID(nextAgentID);
    const adapter = agentAdapters.find((item) => item.id === nextAgentID);
    setAgentConfigOptions(adapter?.config_options ?? []);
    setDefaultChatTarget("external_agent");
  }

  function configOptionsForExternalAgent(agentID: string) {
    if (agentID === agentAdapterID && agentConfigOptions.length > 0) {
      return agentConfigOptions;
    }
    return agentAdapters.find((item) => item.id === agentID)?.config_options ?? [];
  }

  function mcpServersForExternalAgent() {
    return mcpServerFormEntriesToPayload(agentMCPServers, { includeApprovalPolicy: false });
  }

  async function submitChat(event: SyntheticEvent<HTMLFormElement>) {
    event.preventDefault();
    await submitAgentChat();
  }

  function buildQueuedChatMessage(
    content: string,
    executionMode: ChatExecutionMode,
    sessionID: string,
    toolsEnabled: boolean,
  ): QueuedChatMessage {
    return {
      id: `queued-chat-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
      session_id: sessionID,
      content,
      execution_mode: executionMode,
      tools_enabled: toolsEnabled,
      provider_filter: providerFilter,
      model,
      workspace: workspaceForActiveTurn(),
      system_prompt: systemPrompt,
      agent_id: executionMode === "external_agent" ? agentAdapterID : "hecate",
      created_at: new Date().toISOString(),
    };
  }

  function queueChatMessage(
    content: string,
    executionMode: ChatExecutionMode,
    sessionID: string,
    toolsEnabled: boolean,
  ) {
    setQueuedChatMessages((current) => [
      ...current,
      buildQueuedChatMessage(content, executionMode, sessionID, toolsEnabled),
    ]);
    setMessage("");
  }

  function applyChatSession(session: ChatSessionRecord) {
    // Fold the incoming snapshot onto the previous one so unchanged
    // message/segment objects keep their identity. The live stream
    // republishes a full session snapshot per coalesced flush; without
    // this, every transcript row would get a fresh object identity and
    // re-render (and re-parse its markdown) on every streamed batch.
    setActiveChatSession((prev) => reconcileChatSession(prev, session));
    syncHecateSelectionFromSession(session);
    setAgentWorkspaceBranch(session.workspace_branch ?? "");
    setChatSessions((current) => [
      renderChatSessionSummary(session),
      ...current.filter((entry) => entry.id !== session.id),
    ]);
  }

  function syncHecateSelectionFromSession(session: ChatSessionRecord | null) {
    const selection = deriveHecateChatSelectionFromSession(session);
    if (selection.provider) {
      setProviderFilter(selection.provider as ProviderFilter);
    }
    if (selection.model) {
      setModel(selection.model);
    }
  }

  async function refreshChatSession(sessionID: string): Promise<void> {
    const payload = await getChatSession(sessionID);
    applyChatSession(payload.data);
  }

  async function submitAgentChat(queued?: QueuedChatMessage) {
    const content = (queued?.content ?? message).trim();
    if (!content) return;

    const turnProviderFilter = queued?.provider_filter ?? providerFilter;
    const turnModel = queued?.model ?? model;
    const requestedExecutionMode =
      queued?.execution_mode ?? chatTargetToExecutionMode(params.chatTarget);
    const requestedToolsEnabled = queued?.tools_enabled ?? resolveToolsEnabled(activeChatSessionID);
    const isExternalAgent = requestedExecutionMode === "external_agent";
    const turnToolsEnabled = isExternalAgent
      ? true
      : effectiveHecateToolsEnabled({
          requested: requestedExecutionMode,
          models,
          providerFilter: turnProviderFilter,
          model: turnModel,
          toolsEnabled: requestedToolsEnabled,
        });
    const turnExecutionMode = requestedExecutionMode;
    const isDirectModelTurn = !isExternalAgent && !turnToolsEnabled;
    if (!queued && activeChatSessionID && chatSessionIsBusy(activeChatSession)) {
      queueChatMessage(content, turnExecutionMode, activeChatSessionID, turnToolsEnabled);
      return;
    }
    setChatLoading(true);
    clearChatErrorState();
    setRuntimeHeaders(null);
    let turnWorkspace = queued?.workspace ?? workspaceForActiveTurn();
    const turnSystemPrompt = queued?.system_prompt ?? systemPrompt;
    const turnAgentID = queued?.agent_id ?? agentAdapterID;
    setStreamingContent(
      isExternalAgent
        ? "Starting external agent..."
        : isDirectModelTurn
          ? "Waiting for model output..."
          : "Starting Hecate Chat tools...",
    );
    let streamAbort: AbortController | null = null;
    let streamPromise: Promise<void> | null = null;

    try {
      if (!isExternalAgent && !turnModel) {
        setChatErrorState(chatModelRequiredError());
        return;
      }

      let sessionID = queued?.session_id ?? activeChatSessionID;
      let sessionForSubmit = activeChatSession?.id === sessionID ? activeChatSession : null;
      if (sessionID && !sessionForSubmit) {
        try {
          const payload = await getChatSession(sessionID);
          sessionForSubmit = payload.data;
          applyChatSession(payload.data);
        } catch {
          // The server owns chat persistence. If localStorage points at a
          // deleted or unavailable session, start clean instead of making the
          // next prompt fail with a stale 404.
          sessionID = "";
          setActiveChatSessionID("");
        }
      }
      turnWorkspace = turnWorkspace || sessionForSubmit?.workspace?.trim() || "";
      if (!isDirectModelTurn && !turnWorkspace) {
        setChatErrorState(chatWorkspaceRequiredError());
        return;
      }
      if (sessionID && sessionForSubmit?.agent_id) {
        const activeExternal = sessionForSubmit.agent_id !== "hecate";
        if (activeExternal !== isExternalAgent) {
          sessionID = "";
          sessionForSubmit = null;
          setActiveChatSessionID("");
          setActiveChatSession(null);
        }
      }
      if (!sessionID) {
        const configOptions = isExternalAgent ? configOptionsForExternalAgent(turnAgentID) : [];
        const mcpServers = isExternalAgent ? mcpServersForExternalAgent() : [];
        const created = await createChatSessionRequest({
          title: deriveChatSessionTitle(content),
          ...(activeProjectID ? { project_id: activeProjectID } : {}),
          agent_id: isExternalAgent ? turnAgentID : "hecate",
          ...(!isExternalAgent
            ? {
                provider: turnProviderFilter === "auto" ? "" : turnProviderFilter,
                model: turnModel,
              }
            : {}),
          ...(!isDirectModelTurn ? { workspace: turnWorkspace } : {}),
          ...(!isExternalAgent && turnToolsEnabled ? { rtk_enabled: hecateRTKEnabled } : {}),
          ...(isExternalAgent && configOptions.length > 0 ? { config_options: configOptions } : {}),
          ...(isExternalAgent && mcpServers.length > 0 ? { mcp_servers: mcpServers } : {}),
        });
        sessionID = created.data.id;
        setActiveChatSessionID(sessionID);
        applyChatSession(created.data);
      }
      if (!isExternalAgent && sessionID) {
        const sid = sessionID;
        setChatTargetBySessionID((current) => {
          const next = new Map(current);
          next.set(sid, "agent");
          return next;
        });
        setChatToolsEnabledBySessionID((current) => {
          const next = new Map(current);
          next.set(sid, turnToolsEnabled);
          return next;
        });
      }

      const pendingContent = content;
      setMessage("");
      setActiveChatSession((prev) =>
        prev
          ? {
              ...prev,
              messages: [
                ...(prev.messages ?? []),
                {
                  id: `pending-agent-user-${Date.now()}`,
                  execution_mode: turnExecutionMode,
                  tools_enabled: !isExternalAgent ? turnToolsEnabled : undefined,
                  provider: !isExternalAgent
                    ? turnProviderFilter === "auto"
                      ? ""
                      : turnProviderFilter
                    : undefined,
                  model: !isExternalAgent ? turnModel : undefined,
                  role: "user",
                  content: pendingContent,
                  created_at: new Date().toISOString(),
                },
              ],
            }
          : prev,
      );

      streamAbort = new AbortController();
      streamPromise = streamChatSession(
        sessionID,
        (event) => {
          switch (event.type) {
            case "session_update": {
              applyChatSession(event.payload.data);
              const last = [...(event.payload.data.messages ?? [])]
                .reverse()
                .find((m) => m.role === "assistant");
              if (last?.status === "running") {
                setStreamingContent(
                  last.content ||
                    (isExternalAgent
                      ? "External agent is running..."
                      : isDirectModelTurn
                        ? "Model is responding..."
                        : "Hecate Chat tools are running..."),
                );
              }
              return;
            }
            case "approval.requested": {
              upsertPendingApproval(event.payload);
              return;
            }
            case "approval.resolved": {
              removePendingApproval(event.payload.session_id, event.payload.approval_id);
              return;
            }
          }
        },
        streamAbort.signal,
      ).catch((streamError) => {
        if (streamAbort?.signal.aborted) {
          return;
        }
        const msg = streamError instanceof Error ? streamError.message : "agent chat stream failed";
        setChatError((current) => current || msg);
      });
      const updated = await createChatMessageRequest(sessionID, {
        content: pendingContent,
        ...(isExternalAgent
          ? { execution_mode: turnExecutionMode }
          : { tools_enabled: turnToolsEnabled }),
        ...(!isExternalAgent
          ? { provider: turnProviderFilter === "auto" ? "" : turnProviderFilter, model: turnModel }
          : {}),
        ...(!isExternalAgent ? { system_prompt: turnSystemPrompt } : {}),
        ...(!isExternalAgent && turnToolsEnabled ? { workspace: turnWorkspace } : {}),
      });
      applyChatSession(updated.data);
    } catch (submitError) {
      setChatErrorState(submitError);
    } finally {
      streamAbort?.abort();
      await streamPromise?.catch(() => undefined);
      setStreamingContent(null);
      setChatLoading(false);
    }
  }

  async function cancelAgentChat() {
    if (!activeChatSessionID || chat.state.chatCancelling) {
      return;
    }
    setChatCancelling(true);
    setStreamingContent("Stopping external agent...");
    try {
      await cancelChatSessionRequest(activeChatSessionID);
    } catch (error) {
      setChatCancelling(false);
      setChatErrorState(error, "failed to cancel agent chat");
    }
  }

  async function compactChatSession(sessionID = activeChatSessionID): Promise<boolean> {
    if (!sessionID) {
      params.setNoticeMessage("error", "Open a Hecate chat before using /compact.");
      return false;
    }
    setChatLoading(true);
    clearChatErrorState();
    try {
      const payload = await compactChatSessionRequest(sessionID);
      applyChatSession(payload.data);
      const count = payload.data.context_summary?.message_count ?? 0;
      params.setNoticeMessage(
        "success",
        count > 0 ? `Compacted ${count} transcript messages.` : "Compacted chat context.",
      );
      return true;
    } catch (error) {
      setChatErrorState(error, "failed to compact chat context");
      return false;
    } finally {
      setChatLoading(false);
    }
  }

  function updateToolResult(index: number, result: string) {
    setPendingToolCalls((prev) => prev.map((tc, i) => (i === index ? { ...tc, result } : tc)));
  }

  async function submitToolResults() {
    if (!pendingThread || pendingToolCalls.length === 0) return;
    setChatLoading(true);
    clearChatErrorState();

    const toolMessages: ChatMessage[] = pendingToolCalls.map((tc) => ({
      role: "tool" as const,
      content: tc.result,
      tool_call_id: tc.id,
    }));

    const messages: ChatMessage[] = [...pendingThread, ...toolMessages];

    try {
      const chatExecution = await executeChatRequest(buildChatPayload(messages), messages);
      if (chatExecution.kind === "tool_calls") {
        return;
      }

      clearPendingToolState();
      setChatResult(chatExecution.chatResult);
      setStreamingContent(null);
      await refreshRuntimeState();
    } catch (err) {
      setChatErrorState(err, "unknown error");
    } finally {
      setChatLoading(false);
    }
  }

  async function executeChatRequest(
    chatPayload: {
      model: string;
      provider: string;
      session_id?: string;
      user: string;
      messages: ChatMessage[];
    },
    toolCallBaseMessages: ChatMessage[],
  ): Promise<
    | { kind: "tool_calls" }
    | { kind: "completed"; headers: RuntimeHeaders; chatResult: ChatResponse }
  > {
    let fullContent = "";
    setStreamingContent("");
    const response = await chatCompletionsStream(chatPayload, (delta) => {
      fullContent += delta;
      setStreamingContent(fullContent);
    });
    setRuntimeHeaders(response.headers);

    if (response.finishReason === "tool_calls" && response.toolCalls.length > 0) {
      setStreamingContent(null);
      const assistantMsg = buildAssistantToolCallMessage(fullContent, response.toolCalls);
      setPendingThread([...toolCallBaseMessages, assistantMsg]);
      setPendingToolCalls(response.toolCalls.map((tc) => ({ ...tc, result: "" })));
      return { kind: "tool_calls" };
    }

    return {
      kind: "completed",
      headers: response.headers,
      chatResult: buildSyntheticChatResult(response.headers, model, fullContent),
    };
  }

  async function createChatSession(options?: CreateChatSessionOptions) {
    const transitionGeneration = beginActiveChatTransition();
    rememberChatComposerDraft(activeChatSessionID, message);
    const requestedAgentID = options?.agentID?.trim();
    const requestedTitle = options?.title?.trim() || "";
    const requestedDraft = options?.draft ?? "";
    const requestedReuseEmptyDraft = Boolean(options?.reuseEmptyDraft && requestedDraft.trim());
    const createProjectID =
      options && "projectID" in options ? options.projectID?.trim() || "" : activeProjectID;
    const requestedProviderFilter = (
      options && "provider" in options ? options.provider?.trim() || "auto" : providerFilter
    ) as ProviderFilter;
    const requestedSelectionModel =
      options && "model" in options ? options.model?.trim() || "" : model;
    const createExternalAgent =
      requestedAgentID && requestedAgentID !== "hecate"
        ? true
        : !requestedAgentID && defaultChatTarget === "external_agent";
    if (createExternalAgent) {
      const externalAgentID = requestedAgentID || agentAdapterID;
      const workspace = workspaceForNewChat(createProjectID);
      if (!workspace) {
        setChatErrorState(chatWorkspaceRequiredError());
        setActiveChatSessionID("");
        setActiveChatSession(null);
        completeActiveChatTransition(transitionGeneration);
        return;
      }
      setChatLoading(true);
      clearChatErrorState();
      try {
        const adapter = agentAdapters.find((item) => item.id === externalAgentID);
        const configOptions = configOptionsForExternalAgent(externalAgentID);
        const mcpServers = mcpServersForExternalAgent();
        const created = await createChatSessionRequest({
          title: requestedTitle || (adapter ? `${adapter.name} chat` : "External agent chat"),
          ...(createProjectID ? { project_id: createProjectID } : {}),
          agent_id: externalAgentID,
          workspace,
          ...(configOptions.length > 0 ? { config_options: configOptions } : {}),
          ...(mcpServers.length > 0 ? { mcp_servers: mcpServers } : {}),
        });
        rememberChatComposerDraft(created.data.id, requestedDraft);
        if (!isCurrentActiveChatTransition(transitionGeneration)) {
          setChatSessions((current) => [
            renderChatSessionSummary(created.data),
            ...current.filter((entry) => entry.id !== created.data.id),
          ]);
          return;
        }
        setActiveChatSessionID(created.data.id);
        applyChatSession(created.data);
        setMessage(requestedDraft);
      } catch (error) {
        if (!isCurrentActiveChatTransition(transitionGeneration)) return;
        setChatErrorState(error, "failed to create external agent chat");
        params.setNoticeMessage(
          "error",
          error instanceof Error ? error.message : "Failed to create external agent chat.",
        );
      } finally {
        completeActiveChatTransition(transitionGeneration);
        setChatLoading(false);
      }
      return;
    }

    const requestedChatTarget = requestedAgentID === "hecate" ? "agent" : defaultChatTarget;
    const requestedExecutionMode = chatTargetToExecutionMode(requestedChatTarget);
    const toolsEnabled = effectiveHecateToolsEnabled({
      requested: requestedExecutionMode,
      models,
      providerFilter: requestedProviderFilter,
      model: requestedSelectionModel,
      // No active session yet — fall back to the user default. The
      // composer hasn't had a chance to call setChatToolsEnabled
      // against the new session ID, so the per-session map can't
      // contribute.
      toolsEnabled: defaultChatToolsEnabled,
    });
    // The Hecate-routing availability check matters only when tools are
    // enabled: an unroutable model falls back to "" so the gateway picks
    // a routable default. Tools-off chat is still a Hecate-owned session,
    // but it dispatches directly to the selected model.
    const requestedModel =
      toolsEnabled &&
      requestedSelectionModel &&
      !modelAvailableForProviderFilter(
        models,
        providers,
        configuredProviders,
        providerPresets,
        requestedProviderFilter,
        requestedSelectionModel,
      )
        ? ""
        : requestedSelectionModel;
    const workspace = workspaceForNewChat(createProjectID);
    const createProvider = requestedProviderFilter === "auto" ? "" : requestedProviderFilter;
    if (requestedReuseEmptyDraft) {
      const reusable = findReusableEmptyDraftSession(chat.state.chatSessions, {
        agentID: "hecate",
        projectID: createProjectID,
        provider: createProvider,
        model: requestedModel,
        title: requestedTitle,
      });
      if (reusable) {
        setChatTargetBySessionID((current) => {
          const next = new Map(current);
          next.set(reusable.id, "agent");
          return next;
        });
        setChatToolsEnabledBySessionID((current) => {
          const next = new Map(current);
          next.set(reusable.id, toolsEnabled);
          return next;
        });
        await selectChatSession(reusable.id, { draft: requestedDraft });
        return;
      }
    }
    setChatLoading(true);
    clearChatErrorState();
    try {
      const created = await createChatSessionRequest({
        ...(requestedTitle ? { title: requestedTitle } : {}),
        ...(createProjectID ? { project_id: createProjectID } : {}),
        agent_id: "hecate",
        provider: createProvider,
        model: requestedModel,
        ...(toolsEnabled && workspace ? { workspace } : {}),
        ...(toolsEnabled ? { rtk_enabled: hecateRTKEnabled } : {}),
      });
      rememberChatComposerDraft(created.data.id, requestedDraft);
      if (!isCurrentActiveChatTransition(transitionGeneration)) {
        setChatSessions((current) => [
          renderChatSessionSummary(created.data),
          ...current.filter((entry) => entry.id !== created.data.id),
        ]);
        return;
      }
      setActiveChatSessionID(created.data.id);
      // Same per-session pinning as the submit path: chatTarget stays
      // "agent" and the tools-on/off intent for this session is recorded
      // in chatToolsEnabledBySessionID. Keeps the two coordinator entry
      // points consistent and avoids the resume-shows-stale-toggle bug.
      setChatTargetBySessionID((current) => {
        const next = new Map(current);
        next.set(created.data.id, "agent");
        return next;
      });
      setChatToolsEnabledBySessionID((current) => {
        const next = new Map(current);
        next.set(created.data.id, toolsEnabled);
        return next;
      });
      applyChatSession(created.data);
      setMessage(requestedDraft);
    } catch (error) {
      if (!isCurrentActiveChatTransition(transitionGeneration)) return;
      setChatErrorState(error, "failed to create Hecate chat");
      if (!isExpectedHecateChatSetupError(error)) {
        params.setNoticeMessage(
          "error",
          error instanceof Error ? error.message : "Failed to create Hecate chat.",
        );
      }
    } finally {
      completeActiveChatTransition(transitionGeneration);
      setChatLoading(false);
    }
  }

  async function selectChatSession(
    id: string,
    options: SelectChatSessionOptions = {},
  ): Promise<boolean> {
    const selectionGeneration = beginActiveChatTransition();
    rememberChatComposerDraft(activeChatSessionID, message);
    const activeDraftIsOwned = composerDraftsBySessionID.has(id) || Boolean(message);
    const targetDraft =
      id === activeChatSessionID && activeDraftIsOwned
        ? message
        : (composerDraftsBySessionID.get(id) ?? options.draft ?? "");
    if (id && options.draft !== undefined && !composerDraftsBySessionID.has(id)) {
      rememberChatComposerDraft(id, options.draft);
    }
    setActiveChatSessionID(id);
    if (!id) {
      setActiveChatSession(null);
      setMessage("");
      completeActiveChatTransition(selectionGeneration);
      return true;
    }
    if (activeChatSession?.id !== id) {
      setActiveChatSession(null);
      setAgentWorkspaceBranch("");
    }
    // Transfer composer ownership with the selected id. A later response may
    // refresh the session snapshot, but it must not overwrite edits made while
    // that request is in flight.
    setMessage(targetDraft);
    try {
      const payload = await getChatSession(id);
      if (!isCurrentActiveChatTransition(selectionGeneration)) return false;
      setActiveChatSession(payload.data);
      if (payload.data.agent_id && payload.data.agent_id !== "hecate") {
        setAgentAdapterID(payload.data.agent_id);
      }
      const selection = deriveHecateChatSelectionFromSession(payload.data);
      if (selection.provider) {
        setProviderFilter(selection.provider as ProviderFilter);
      }
      if (selection.model) {
        setModel(selection.model);
      }
      setAgentWorkspace(payload.data.workspace ?? "");
      setAgentWorkspaceBranch(payload.data.workspace_branch ?? "");
      completeActiveChatTransition(selectionGeneration);
      return true;
    } catch (error) {
      if (!isCurrentActiveChatTransition(selectionGeneration)) return false;
      const msg = error instanceof Error ? error.message : "failed to load agent chat";
      setActiveChatSessionID("");
      setActiveChatSession(null);
      setAgentWorkspaceBranch("");
      setMessage("");
      setChatErrorState(error, "failed to load agent chat");
      params.setNoticeMessage("error", msg);
      completeActiveChatTransition(selectionGeneration);
      return false;
    }
  }

  function startNewChat() {
    const transitionGeneration = beginActiveChatTransition();
    rememberChatComposerDraft(activeChatSessionID, message);
    if (activeChatSessionID) {
      setQueuedChatMessages((current) =>
        current.filter((item) => item.session_id !== activeChatSessionID),
      );
    }
    setActiveChatSessionID("");
    setActiveChatSession(null);
    setAgentWorkspaceBranch("");
    resetChatWorkspaceState();
    completeActiveChatTransition(transitionGeneration);
  }

  async function deleteChatSession(id: string) {
    try {
      await deleteChatSessionRequest(id);
      setChatSessions((current) => current.filter((s) => s.id !== id));
      setQueuedChatMessages((current) => current.filter((item) => item.session_id !== id));
      setChatTargetBySessionID((current) => {
        if (!current.has(id)) return current;
        const next = new Map(current);
        next.delete(id);
        return next;
      });
      if (activeChatSessionID === id) {
        startNewChat();
      }
      setComposerDraftsBySessionID((current) => {
        if (!current.has(id)) return current;
        const next = new Map(current);
        next.delete(id);
        return next;
      });
      params.setNoticeMessage("success", "Agent chat deleted.");
    } catch (error) {
      params.setNoticeMessage(
        "error",
        error instanceof Error ? error.message : "Failed to delete agent chat.",
      );
    }
  }

  // getChatApproval is the modal-open path: fetches the full
  // approval row (ACP options, scope choices, decision_note, …).
  // Returns null on failure so the caller can render an error state;
  // the slice's getApproval returns a discriminated Result that the
  // shim unwraps into the legacy `record | null` shape and routes
  // the error string to the global notice banner.
  async function getChatApproval(
    sessionID: string,
    approvalID: string,
  ): Promise<ChatApprovalRecord | null> {
    const result = await approvals.actions.getApproval(sessionID, approvalID);
    if (!result.ok) {
      params.setNoticeMessage("error", result.error);
      return null;
    }
    return result.record;
  }

  async function resolveChatApproval(
    sessionID: string,
    approvalID: string,
    decision: ResolveChatApprovalPayload,
  ): Promise<boolean> {
    const result = await approvals.actions.resolveApproval(sessionID, approvalID, decision);
    if (!result.ok) params.setNoticeMessage("error", result.error);
    return result.ok;
  }

  async function cancelChatApproval(sessionID: string, approvalID: string): Promise<boolean> {
    const result = await approvals.actions.cancelApproval(sessionID, approvalID);
    if (!result.ok) params.setNoticeMessage("error", result.error);
    return result.ok;
  }

  async function resolveTaskApproval(
    taskID: string,
    approvalID: string,
    decision: ResolveTaskApprovalPayload,
  ): Promise<boolean> {
    const status = decision.decision === "approve" ? "approved" : "rejected";
    // Capture the pre-resolve session synchronously from closure so
    // we can roll back if the API call fails. We can't capture inside
    // the state updater function because React invokes it
    // asynchronously (and may invoke it twice under StrictMode); by
    // the time the catch branch runs, the closure variable would
    // either still be null or hold the already-patched state. Same
    // pattern as deleteProvider above.
    //
    // Optimistic-update-before-call means the banner row disappears
    // the moment the operator clicks; before this, the row hung
    // around for the full network round-trip (50–500 ms), which
    // looked unresponsive on slow links and let an operator
    // double-click a duplicate request through.
    const snapshot: ChatSessionRecord | null =
      activeChatSession && activeChatSession.task_id === taskID ? activeChatSession : null;
    if (snapshot) {
      setActiveChatSession((current) => {
        if (!current || current.task_id !== taskID) return current;
        return {
          ...current,
          messages: (current.messages ?? []).map((message) => ({
            ...message,
            activities: message.activities?.map((activity) => {
              if (
                activity.approval_id !== approvalID &&
                activity.id !== `task:approval:${approvalID}`
              )
                return activity;
              return { ...activity, status, needs_action: false };
            }),
          })),
        };
      });
    }
    // rollbackOptimisticApproval restores the specific approval
    // activity from the pre-resolve snapshot, while leaving every
    // other field of the active session untouched. Two concurrency
    // hazards force this surgical shape rather than
    // `setActiveChatSession(snapshot)`:
    //
    //   1. The operator may have navigated to a different session
    //      while the request was in flight. The functional updater
    //      bails when the active session id has changed.
    //   2. A stream `session_update` or a refresh may have applied
    //      newer messages/activities on top of the optimistic
    //      state. Restoring only the specific approval activity
    //      preserves them.
    //
    // Reused by both the generic-failure path AND the
    // not-pending+refresh-failed path so both cases produce the
    // same operator-visible state ("we're not sure what the
    // server thinks; show the row as still pending so the
    // operator can retry") instead of leaving a possibly-wrong
    // optimistic decision on screen.
    const rollbackOptimisticApproval = () => {
      if (!snapshot) return;
      const snapshotForRollback = snapshot;
      // Predicate matches the activity by approval_id (or the
      // projected `task:approval:<id>` id). Using the SAME
      // predicate on both sides matters because Activity.id is
      // optional — matching by id alone could (a) fail to restore
      // when the current row has no id and (b) wrongly match the
      // first id-less row if both sides have undefined ids.
      const matchesTargetApproval = (activity: ChatActivityRecord) =>
        activity.approval_id === approvalID || activity.id === `task:approval:${approvalID}`;
      setActiveChatSession((current) => {
        if (!current || current.id !== snapshotForRollback.id) return current;
        return {
          ...current,
          messages: (current.messages ?? []).map((message) => {
            const originalMessage = snapshotForRollback.messages?.find((m) => m.id === message.id);
            if (!originalMessage) return message;
            return {
              ...message,
              activities: message.activities?.map((activity) => {
                if (!matchesTargetApproval(activity)) return activity;
                const originalActivity = originalMessage.activities?.find(matchesTargetApproval);
                return originalActivity ?? activity;
              }),
            };
          }),
        };
      });
    };

    try {
      await resolveTaskApprovalRequest(taskID, approvalID, decision);
      if (activeChatSessionID) {
        try {
          await refreshChatSession(activeChatSessionID);
        } catch {
          // The local approval state above already removes the action;
          // a follow-up session refresh is best-effort because the run
          // may still be transitioning after the operator decision.
        }
      }
      return true;
    } catch (error) {
      if (error instanceof Error && /not pending/i.test(error.message)) {
        // Server says the approval is already resolved. The
        // resolution may NOT match the operator's chosen decision —
        // another tab could have approved while this one tried to
        // reject, the run might have timed out into auto-rejection,
        // or the run could have been cancelled. Refresh to pull
        // server-truth and let it overwrite our optimistic patch.
        if (activeChatSessionID) {
          try {
            await refreshChatSession(activeChatSessionID);
            return true;
          } catch {
            // Refresh failed — we cannot trust our optimistic patch
            // (it might claim a decision the server didn't make).
            // Fall through to rollback so the row reflects "still
            // pending" rather than a possibly-wrong final state.
          }
        }
        rollbackOptimisticApproval();
        params.setNoticeMessage(
          "error",
          "Approval was already resolved upstream and the session refresh failed; reload to see the current state.",
        );
        return false;
      }
      // Genuine failure — roll back so the row reappears and the
      // operator can retry.
      rollbackOptimisticApproval();
      params.setNoticeMessage(
        "error",
        error instanceof Error ? error.message : "Failed to resolve task approval.",
      );
      return false;
    }
  }

  async function deleteChatGrant(grantID: string): Promise<boolean> {
    const result = await approvals.actions.deleteGrant(grantID);
    if (result.ok) {
      params.setNoticeMessage("success", "Grant revoked.");
    } else {
      params.setNoticeMessage("error", result.error);
    }
    return result.ok;
  }

  async function listChatMessageFiles(
    sessionID: string,
    messageID: string,
  ): Promise<ChatChangedFileRecord[]> {
    try {
      const payload = await listChatMessageFilesRequest(sessionID, messageID);
      return payload.data ?? [];
    } catch (error) {
      params.setNoticeMessage(
        "error",
        error instanceof Error ? error.message : "Failed to load changed files.",
      );
      return [];
    }
  }

  async function getChatWorkspaceDiff(sessionID: string): Promise<ChatWorkspaceDiffRecord | null> {
    try {
      const payload = await getChatWorkspaceDiffRequest(sessionID);
      return payload.data;
    } catch (error) {
      params.setNoticeMessage(
        "error",
        error instanceof Error ? error.message : "Failed to load current workspace diff.",
      );
      return null;
    }
  }

  async function getChatWorkspaceFiles(
    sessionID: string,
  ): Promise<ChatWorkspaceFilesRecord | null> {
    try {
      const payload = await getChatWorkspaceFilesRequest(sessionID);
      return payload.data;
    } catch (error) {
      params.setNoticeMessage(
        "error",
        error instanceof Error ? error.message : "Failed to load workspace files.",
      );
      return null;
    }
  }

  async function getChatWorkspaceFileDiff(
    sessionID: string,
    path: string,
  ): Promise<ChatChangedFileDiffRecord | null> {
    try {
      const payload = await getChatWorkspaceFileDiffRequest(sessionID, path);
      return payload.data;
    } catch (error) {
      params.setNoticeMessage(
        "error",
        error instanceof Error ? error.message : "Failed to load current file diff.",
      );
      return null;
    }
  }

  async function revertChatWorkspaceFiles(
    sessionID: string,
    paths: string[],
  ): Promise<ChatWorkspaceDiffRecord | null> {
    try {
      const payload = await revertChatWorkspaceFilesRequest(sessionID, paths);
      params.setNoticeMessage(
        "success",
        paths.length > 0 ? "Selected workspace files discarded." : "Workspace changes discarded.",
      );
      return payload.data;
    } catch (error) {
      params.setNoticeMessage(
        "error",
        error instanceof Error ? error.message : "Failed to discard workspace changes.",
      );
      return null;
    }
  }

  async function setChatConfigOption(
    sessionID: string,
    configID: string,
    value: string | boolean,
  ): Promise<boolean> {
    try {
      const payload = await setChatConfigOptionRequest(sessionID, configID, value);
      applyChatSession(payload.data);
      return true;
    } catch (error) {
      params.setNoticeMessage(
        "error",
        error instanceof Error ? error.message : "Failed to update adapter control.",
      );
      return false;
    }
  }

  async function setHecateRTKEnabled(enabled: boolean): Promise<boolean> {
    setHecateRTKEnabledState(enabled);
    if (!activeChatSessionID || !activeChatSession || chatSessionIsExternal(activeChatSession)) {
      return true;
    }
    try {
      const payload = await setChatSettingsRequest(activeChatSessionID, { rtk_enabled: enabled });
      applyChatSession(payload.data);
      return true;
    } catch (error) {
      setHecateRTKEnabledState(Boolean(activeChatSession.rtk_enabled));
      params.setNoticeMessage(
        "error",
        error instanceof Error ? error.message : "Failed to update chat settings.",
      );
      return false;
    }
  }

  async function getChatMessageFileDiff(
    sessionID: string,
    messageID: string,
    path: string,
  ): Promise<ChatChangedFileDiffRecord | null> {
    try {
      const payload = await getChatMessageFileDiffRequest(sessionID, messageID, path);
      return payload.data;
    } catch (error) {
      params.setNoticeMessage(
        "error",
        error instanceof Error ? error.message : "Failed to load file diff.",
      );
      return null;
    }
  }

  async function revertChatMessageFiles(
    sessionID: string,
    messageID: string,
    paths: string[],
  ): Promise<boolean> {
    try {
      await revertChatMessageFilesRequest(sessionID, messageID, paths);
      await refreshChatSession(sessionID);
      params.setNoticeMessage(
        "success",
        paths.length > 0 ? "Selected files reverted." : "Captured diff reverted.",
      );
      return true;
    } catch (error) {
      params.setNoticeMessage(
        "error",
        error instanceof Error ? error.message : "Failed to revert changed files.",
      );
      return false;
    }
  }

  async function renameChatSession(id: string, title: string) {
    try {
      const nextTitle = title.trim();
      if (!nextTitle) {
        params.setNoticeMessage("error", "Chat title cannot be empty.");
        return;
      }
      const payload = await updateChatSessionRequest(id, nextTitle);
      setChatSessions((current) =>
        current.map((s) =>
          s.id === id
            ? {
                ...s,
                title: payload.data.title,
                updated_at: payload.data.updated_at ?? s.updated_at,
              }
            : s,
        ),
      );
      if (activeChatSessionID === id) {
        setActiveChatSession((current) =>
          current
            ? {
                ...current,
                title: payload.data.title,
                updated_at: payload.data.updated_at ?? current.updated_at,
              }
            : current,
        );
      }
    } catch (error) {
      params.setNoticeMessage(
        "error",
        error instanceof Error ? error.message : "Failed to rename chat.",
      );
    }
  }

  async function chooseAgentWorkspace(): Promise<boolean> {
    clearChatErrorState();
    try {
      const payload = await chooseWorkspaceDirectoryRequest();
      if (payload.data.path) {
        setAgentWorkspace(payload.data.path);
        setAgentWorkspaceBranch(payload.data.branch ?? "");
      }
      return true;
    } catch (error) {
      setChatErrorState(error, "workspace folder dialog is unavailable");
      return false;
    }
  }

  const real = {
    // helpers / internal state operations exposed for dashboard
    applyChatSession,
    syncHecateSelectionFromSession,
    refreshRuntimeState,
    refreshChatSession,
    clearPendingToolState,
    resetChatWorkspaceState,
    submitAgentChat,
    // The wide public surface that lands in the viewmodel actions bag
    submitChat,
    cancelAgentChat,
    compactChatSession,
    updateToolResult,
    submitToolResults,
    createChatSession,
    selectChatSession,
    startNewChat,
    deleteChatSession,
    renameChatSession,
    setChatTarget,
    setChatToolsEnabled,
    setNewChatAgent,
    updateAgentWorkspace,
    selectProviderRoute,
    chooseAgentWorkspace,
    getChatApproval,
    resolveChatApproval,
    cancelChatApproval,
    resolveTaskApproval,
    deleteChatGrant,
    listChatMessageFiles,
    getChatWorkspaceDiff,
    getChatWorkspaceFiles,
    getChatWorkspaceFileDiff,
    revertChatWorkspaceFiles,
    getChatMessageFileDiff,
    revertChatMessageFiles,
    setChatConfigOption,
    setHecateRTKEnabled,
  };
  const overrides = useContext(CoordinatorOverridesContext);
  return applyOverride(real, overrides?.chat);
}
