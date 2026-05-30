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
  createChatMessage as createChatMessageRequest,
  createChatSession as createChatSessionRequest,
  deleteChatSession as deleteChatSessionRequest,
  getChatMessageFileDiff as getChatMessageFileDiffRequest,
  getChatWorkspaceDiff as getChatWorkspaceDiffRequest,
  getChatWorkspaceFileDiff as getChatWorkspaceFileDiffRequest,
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
  renderChatSessionSummary,
} from "../../runtimeConsoleChatHelpers";
import { modelSelectionHasNoToolCalling } from "../../../lib/chat-setup-readiness";
import { projectByID, projectDefaultWorkspace } from "../../../lib/project-workspace";
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
import { useUsage } from "../usage";
import type { RuntimeHeaders } from "../../../types/runtime";
import type { ProviderFilter } from "../../../types/provider";
import type { ModelRecord } from "../../../types/model";
import type {
  ChatActivityRecord,
  ChatApprovalRecord,
  ChatChangedFileDiffRecord,
  ChatChangedFileRecord,
  ChatWorkspaceDiffRecord,
  ChatResponse,
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
  const segment = segments.find(
    (item) => item.execution_mode === "hecate_task" || item.execution_mode === "direct_model",
  );
  if (segment?.provider || segment?.model) {
    return { provider: segment.provider ?? "", model: segment.model ?? "" };
  }
  const messages = [...(session.messages ?? [])].reverse();
  const message = messages.find(
    (item) => item.execution_mode === "hecate_task" || item.execution_mode === "direct_model",
  );
  if (message?.provider || message?.model) {
    return { provider: message.provider ?? "", model: message.model ?? "" };
  }
  return { provider: session.provider ?? "", model: session.model ?? "" };
}

function effectiveHecateExecutionMode({
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
  // User intent: false means the operator explicitly toggled tools off
  // for this Hecate-targeted chat, so downgrade the execution mode to
  // direct_model regardless of model capability. Independent of the
  // capability-derived downgrade below, which handles the case where
  // the model itself doesn't support tool calling.
  toolsEnabled: boolean;
}): ChatExecutionMode {
  if (requested !== "hecate_task") return requested;
  if (!toolsEnabled) return "direct_model";
  return modelSelectionHasNoToolCalling({ models, providerFilter, model })
    ? "direct_model"
    : requested;
}

function modelAvailableForProviderFilter(
  models: ModelRecord[],
  providerFilter: ProviderFilter,
  model: string,
): boolean {
  if (!model.trim()) return false;
  return models.some((entry) => {
    if (entry.id !== model) return false;
    if (!providerFilter || providerFilter === "auto") return true;
    return entry.metadata?.provider === providerFilter;
  });
}

export { chatSessionIsExternal, chatSessionIsBusy };

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
  updateToolResult: (index: number, result: string) => void;
  submitToolResults: () => Promise<void>;
  createChatSession: (options?: { agentID?: string; projectID?: string }) => Promise<void>;
  selectChatSession: (id: string) => Promise<void>;
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

  const { message, hecateRTKEnabled } = runtime.state;
  const {
    setMessage,
    setRuntimeHeaders,
    setHecateRTKEnabled: setHecateRTKEnabledState,
  } = runtime.actions;
  const { setSummary: setUsageSummary, setEvents: setUsageEvents } = usage.actions;
  const { agentAdapters, models, providers } = providersAndModels.state;
  const activeProjectID = projects.activeProjectID.trim();
  const {
    defaultChatTarget,
    defaultChatToolsEnabled,
    chatToolsEnabledBySessionID,
    agentAdapterID,
    agentConfigOptions,
    agentWorkspace,
    activeChatSessionID,
    activeChatSession,
    model,
    systemPrompt,
    pendingToolCalls,
    pendingThread,
    providerFilter,
  } = chat.state;
  const {
    setDefaultChatTarget,
    setChatTargetBySessionID,
    setDefaultChatToolsEnabled,
    setChatToolsEnabledBySessionID,
    setAgentAdapterID,
    setAgentConfigOptions,
    setAgentWorkspace,
    setAgentWorkspaceBranch,
    setChatSessions,
    setActiveChatSessionID,
    setActiveChatSession,
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
    setModel(defaultModelForProvider(nextProvider, models, providers));
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
    return activeChatSession?.workspace?.trim() || workspaceForNewChat(activeProjectID);
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

  async function submitChat(event: SyntheticEvent<HTMLFormElement>) {
    event.preventDefault();
    await submitAgentChat();
  }

  function buildQueuedChatMessage(
    content: string,
    executionMode: ChatExecutionMode,
    sessionID: string,
  ): QueuedChatMessage {
    return {
      id: `queued-chat-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
      session_id: sessionID,
      content,
      execution_mode: executionMode,
      provider_filter: providerFilter,
      model,
      workspace: workspaceForActiveTurn(),
      system_prompt: systemPrompt,
      agent_id: executionMode === "external_agent" ? agentAdapterID : "hecate",
      created_at: new Date().toISOString(),
    };
  }

  function queueChatMessage(content: string, executionMode: ChatExecutionMode, sessionID: string) {
    setQueuedChatMessages((current) => [
      ...current,
      buildQueuedChatMessage(content, executionMode, sessionID),
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
    // Resolve tools-enabled against the *active* session, not the
    // queued message's pinned session: queued messages reuse the
    // execution_mode they were enqueued with, so the toolsEnabled
    // signal only affects fresh-from-the-composer turns.
    const turnToolsEnabled = queued
      ? requestedExecutionMode !== "direct_model"
      : resolveToolsEnabled(activeChatSessionID);
    const turnExecutionMode = effectiveHecateExecutionMode({
      requested: requestedExecutionMode,
      models,
      providerFilter: turnProviderFilter,
      model: turnModel,
      toolsEnabled: turnToolsEnabled,
    });
    if (!queued && activeChatSessionID && chatSessionIsBusy(activeChatSession)) {
      queueChatMessage(content, turnExecutionMode, activeChatSessionID);
      return;
    }

    setChatLoading(true);
    clearChatErrorState();
    setRuntimeHeaders(null);
    const isExternalAgent = turnExecutionMode === "external_agent";
    const isModelTurn = turnExecutionMode === "direct_model";
    let turnWorkspace = queued?.workspace ?? workspaceForActiveTurn();
    const turnSystemPrompt = queued?.system_prompt ?? systemPrompt;
    const turnAgentID = queued?.agent_id ?? agentAdapterID;
    setStreamingContent(
      isExternalAgent
        ? "Starting external agent..."
        : isModelTurn
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
      let sessionForSubmit = activeChatSession;
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
      if (!isModelTurn && !turnWorkspace) {
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
          ...(!isModelTurn ? { workspace: turnWorkspace } : {}),
          ...(!isExternalAgent ? { rtk_enabled: hecateRTKEnabled } : {}),
          ...(isExternalAgent && configOptions.length > 0 ? { config_options: configOptions } : {}),
        });
        sessionID = created.data.id;
        setActiveChatSessionID(sessionID);
        applyChatSession(created.data);
      }
      if (!isExternalAgent && sessionID) {
        // Pin the session target to "agent" (the user-target perspective)
        // regardless of whether this turn ran with tools on or off. The
        // tools state lives in chatToolsEnabledBySessionID now — mirror
        // the just-resolved turn into it so a fresh client opening this
        // session resumes with the same toggle position the user had
        // when they submitted. Without the toolsEnabled mirror, only
        // the in-memory toggle state is correct; localStorage would
        // miss the most recent turn's intent.
        const sid = sessionID;
        setChatTargetBySessionID((current) => {
          const next = new Map(current);
          next.set(sid, "agent");
          return next;
        });
        setChatToolsEnabledBySessionID((current) => {
          const next = new Map(current);
          next.set(sid, turnExecutionMode === "hecate_task");
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
                      : isModelTurn
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
        execution_mode: turnExecutionMode,
        ...(!isExternalAgent
          ? { provider: turnProviderFilter === "auto" ? "" : turnProviderFilter, model: turnModel }
          : {}),
        ...(!isExternalAgent ? { system_prompt: turnSystemPrompt } : {}),
        ...(turnExecutionMode === "hecate_task" ? { workspace: turnWorkspace } : {}),
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

  async function createChatSession(options?: { agentID?: string; projectID?: string }) {
    const requestedAgentID = options?.agentID?.trim();
    const createProjectID =
      options && "projectID" in options ? options.projectID?.trim() || "" : activeProjectID;
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
        return;
      }
      setChatLoading(true);
      clearChatErrorState();
      try {
        const adapter = agentAdapters.find((item) => item.id === externalAgentID);
        const configOptions = configOptionsForExternalAgent(externalAgentID);
        const created = await createChatSessionRequest({
          title: adapter ? `${adapter.name} chat` : "External agent chat",
          ...(createProjectID ? { project_id: createProjectID } : {}),
          agent_id: externalAgentID,
          workspace,
          ...(configOptions.length > 0 ? { config_options: configOptions } : {}),
        });
        setActiveChatSessionID(created.data.id);
        applyChatSession(created.data);
      } catch (error) {
        setChatErrorState(error, "failed to create external agent chat");
        params.setNoticeMessage(
          "error",
          error instanceof Error ? error.message : "Failed to create external agent chat.",
        );
      } finally {
        setChatLoading(false);
      }
      return;
    }

    const requestedChatTarget = requestedAgentID === "hecate" ? "agent" : defaultChatTarget;
    const requestedExecutionMode = chatTargetToExecutionMode(requestedChatTarget);
    const requestedModel =
      requestedExecutionMode === "hecate_task" &&
      model &&
      !modelAvailableForProviderFilter(models, providerFilter, model)
        ? ""
        : model;
    const executionMode = effectiveHecateExecutionMode({
      requested: requestedExecutionMode,
      models,
      providerFilter,
      model: requestedModel,
      // No active session yet — fall back to the user default. The
      // composer hasn't had a chance to call setChatToolsEnabled
      // against the new session ID, so the per-session map can't
      // contribute.
      toolsEnabled: defaultChatToolsEnabled,
    });
    const workspace = workspaceForNewChat(createProjectID);
    if (executionMode === "hecate_task" && !workspace) {
      setChatErrorState(chatWorkspaceRequiredError());
      setActiveChatSessionID("");
      setActiveChatSession(null);
      return;
    }
    setChatLoading(true);
    clearChatErrorState();
    try {
      const created = await createChatSessionRequest({
        ...(createProjectID ? { project_id: createProjectID } : {}),
        agent_id: "hecate",
        provider: providerFilter === "auto" ? "" : providerFilter,
        model: requestedModel,
        ...(workspace ? { workspace } : {}),
        ...(executionMode === "hecate_task" ? { rtk_enabled: hecateRTKEnabled } : {}),
      });
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
        next.set(created.data.id, executionMode === "hecate_task");
        return next;
      });
      applyChatSession(created.data);
    } catch (error) {
      setChatErrorState(error, "failed to create Hecate chat");
      if (!isExpectedHecateChatSetupError(error)) {
        params.setNoticeMessage(
          "error",
          error instanceof Error ? error.message : "Failed to create Hecate chat.",
        );
      }
    } finally {
      setChatLoading(false);
    }
  }

  async function selectChatSession(id: string) {
    setActiveChatSessionID(id);
    if (!id) {
      setActiveChatSession(null);
      return;
    }
    try {
      const payload = await getChatSession(id);
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
    } catch (error) {
      const msg = error instanceof Error ? error.message : "failed to load agent chat";
      setActiveChatSessionID("");
      setActiveChatSession(null);
      setAgentWorkspaceBranch("");
      setChatErrorState(error, "failed to load agent chat");
      params.setNoticeMessage("error", msg);
    }
  }

  function startNewChat() {
    if (activeChatSessionID) {
      setQueuedChatMessages((current) =>
        current.filter((item) => item.session_id !== activeChatSessionID),
      );
    }
    setActiveChatSessionID("");
    setActiveChatSession(null);
    setAgentWorkspaceBranch("");
    resetChatWorkspaceState();
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
