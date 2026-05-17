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

import { type SyntheticEvent } from "react";

import {
  type ChatMessage,
  cancelChatSession as cancelChatSessionRequest,
  chatCompletionsStream,
  chooseWorkspaceDirectory as chooseWorkspaceDirectoryRequest,
  createChatMessage as createChatMessageRequest,
  createChatSession as createChatSessionRequest,
  deleteChatSession as deleteChatSessionRequest,
  getChatMessageFileDiff as getChatMessageFileDiffRequest,
  getChatSession,
  getUsageEvents,
  getUsageSummary,
  listChatMessageFiles as listChatMessageFilesRequest,
  type ResolveChatApprovalPayload,
  type ResolveTaskApprovalPayload,
  resolveTaskApproval as resolveTaskApprovalRequest,
  revertChatMessageFiles as revertChatMessageFilesRequest,
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
import {
  type ChatTarget,
  type QueuedChatMessage,
} from "../_shared";
import { useApprovals } from "../approvals";
import { useChat } from "../chat";
import { useProvidersAndModels } from "../providersAndModels";
import { useRuntime } from "../runtime";
import { useUsage } from "../usage";
import type { RuntimeHeaders } from "../../../types/runtime";
import type { ProviderFilter } from "../../../types/provider";
import type {
  ChatActivityRecord,
  ChatApprovalRecord,
  ChatChangedFileDiffRecord,
  ChatChangedFileRecord,
  ChatResponse,
  ChatSessionRecord,
} from "../../../types/chat";
import type { SettingsActions } from "./settings";

export type UseChatActionsParams = {
  chatTarget: ChatTarget;
  setNoticeMessage: SettingsActions["setNoticeMessage"];
};

export type ChatActions = ReturnType<typeof useChatActions>;

function chatSessionIsExternal(session: ChatSessionRecord | null): boolean {
  return Boolean(session?.runtime_kind === "external_agent" || session?.adapter_id);
}

function chatSessionIsBusy(session: ChatSessionRecord | null): boolean {
  const busy = (status?: string) => status === "queued" || status === "running" || status === "awaiting_approval";
  if (!session) return false;
  if (busy(session.status)) return true;
  if ((session.segments ?? []).some((segment) => busy(segment.status))) return true;
  return (session.messages ?? []).some((message) => message.role === "assistant" && busy(message.status));
}

function deriveHecateChatSelectionFromSession(session: ChatSessionRecord | null): { provider: string; model: string } {
  if (!session || chatSessionIsExternal(session)) {
    return { provider: "", model: "" };
  }
  const segments = [...(session.segments ?? [])].reverse();
  const segment = segments.find((item) => item.runtime_kind === "agent" || item.runtime_kind === "model");
  if (segment?.provider || segment?.model) {
    return { provider: segment.provider ?? "", model: segment.model ?? "" };
  }
  const messages = [...(session.messages ?? [])].reverse();
  const message = messages.find((item) => item.runtime_kind === "agent" || item.runtime_kind === "model");
  if (message?.provider || message?.model) {
    return { provider: message.provider ?? "", model: message.model ?? "" };
  }
  return { provider: session.provider ?? "", model: session.model ?? "" };
}

export { chatSessionIsExternal, chatSessionIsBusy };

export function useChatActions(params: UseChatActionsParams) {
  const runtime = useRuntime();
  const usage = useUsage();
  const providersAndModels = useProvidersAndModels();
  const chat = useChat();
  const approvals = useApprovals();

  const { message, hecateRTKEnabled } = runtime.state;
  const {
    setMessage,
    setRuntimeHeaders,
    setHecateRTKEnabled: setHecateRTKEnabledState,
  } = runtime.actions;
  const { setSummary: setUsageSummary, setEvents: setUsageEvents } = usage.actions;
  const { agentAdapters, models, providers, providerPresets } = providersAndModels.state;
  const {
    defaultChatTarget,
    agentAdapterID,
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
    setAgentAdapterID,
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
    setModel(defaultModelForProvider(nextProvider, models, providers, providerPresets));
  }

  function updateAgentWorkspace(nextWorkspace: string) {
    setAgentWorkspace(nextWorkspace);
    setAgentWorkspaceBranch("");
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

  function setNewChatAgent(nextAgentID: string) {
    if (nextAgentID === "hecate") {
      setDefaultChatTarget("agent");
      return;
    }
    setAgentAdapterID(nextAgentID);
    setDefaultChatTarget("external_agent");
  }

  async function submitChat(event: SyntheticEvent<HTMLFormElement>) {
    event.preventDefault();
    await submitAgentChat();
  }

  function buildQueuedChatMessage(content: string, runtimeKind: ChatTarget, sessionID: string): QueuedChatMessage {
    return {
      id: `queued-chat-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
      session_id: sessionID,
      content,
      runtime_kind: runtimeKind,
      provider_filter: providerFilter,
      model,
      workspace: agentWorkspace.trim(),
      system_prompt: systemPrompt,
      adapter_id: agentAdapterID,
      created_at: new Date().toISOString(),
    };
  }

  function queueChatMessage(content: string, runtimeKind: ChatTarget, sessionID: string) {
    setQueuedChatMessages((current) => [...current, buildQueuedChatMessage(content, runtimeKind, sessionID)]);
    setMessage("");
  }

  function applyChatSession(session: ChatSessionRecord) {
    setActiveChatSession(session);
    syncHecateSelectionFromSession(session);
    setAgentWorkspaceBranch(session.workspace_branch ?? "");
    setChatSessions((current) => [renderChatSessionSummary(session), ...current.filter((entry) => entry.id !== session.id)]);
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

    const turnRuntimeKind = queued?.runtime_kind ?? (params.chatTarget === "external_agent" ? "external_agent" : params.chatTarget === "agent" ? "agent" : "model");
    if (!queued && activeChatSessionID && chatSessionIsBusy(activeChatSession)) {
      queueChatMessage(content, turnRuntimeKind, activeChatSessionID);
      return;
    }

    setChatLoading(true);
    clearChatErrorState();
    setRuntimeHeaders(null);
    const isExternalAgent = turnRuntimeKind === "external_agent";
    const isModelTurn = turnRuntimeKind === "model";
    const turnProviderFilter = queued?.provider_filter ?? providerFilter;
    const turnModel = queued?.model ?? model;
    const turnWorkspace = queued?.workspace ?? agentWorkspace.trim();
    const turnSystemPrompt = queued?.system_prompt ?? systemPrompt;
    const turnAdapterID = queued?.adapter_id ?? agentAdapterID;
    setStreamingContent(isExternalAgent ? "Starting external agent..." : isModelTurn ? "Waiting for model output..." : "Starting Hecate Agent...");
    let streamAbort: AbortController | null = null;
    let streamPromise: Promise<void> | null = null;

    try {
      if (!isModelTurn && !turnWorkspace) {
        setChatError("Choose a workspace path before starting an agent chat.");
        return;
      }
      if (!isExternalAgent && !turnModel) {
        setChatError("Choose a model before sending through Hecate.");
        return;
      }

      let sessionID = queued?.session_id ?? activeChatSessionID;
      if (sessionID && !activeChatSession) {
        // The server owns chat persistence. If localStorage points at a
        // deleted or unavailable session, start clean instead of making the
        // next prompt fail with a stale 404.
        sessionID = "";
        setActiveChatSessionID("");
      }
      if (sessionID && activeChatSession?.runtime_kind) {
        const activeExternal = activeChatSession.runtime_kind === "external_agent";
        if (activeExternal !== isExternalAgent) {
          sessionID = "";
          setActiveChatSessionID("");
          setActiveChatSession(null);
        }
      }
      if (!sessionID) {
        const created = await createChatSessionRequest({
          title: deriveChatSessionTitle(content),
          runtime_kind: turnRuntimeKind,
          ...(isExternalAgent
            ? { adapter_id: turnAdapterID }
            : { provider: turnProviderFilter === "auto" ? "" : turnProviderFilter, model: turnModel }),
          ...(!isModelTurn ? { workspace: turnWorkspace } : {}),
          ...(!isExternalAgent ? { rtk_enabled: hecateRTKEnabled } : {}),
        });
        sessionID = created.data.id;
        setActiveChatSessionID(sessionID);
        applyChatSession(created.data);
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
                  runtime_kind: turnRuntimeKind,
                  provider: !isExternalAgent ? (turnProviderFilter === "auto" ? "" : turnProviderFilter) : undefined,
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
                setStreamingContent(last.content || (isExternalAgent ? "External agent is running..." : isModelTurn ? "Model is responding..." : "Hecate Agent is running..."));
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
        runtime_kind: turnRuntimeKind,
        ...(!isExternalAgent ? { provider: turnProviderFilter === "auto" ? "" : turnProviderFilter, model: turnModel } : {}),
        ...(!isExternalAgent ? { system_prompt: turnSystemPrompt } : {}),
        ...(turnRuntimeKind === "agent" ? { workspace: turnWorkspace } : {}),
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

  async function createChatSession() {
    if (defaultChatTarget === "external_agent") {
      const workspace = agentWorkspace.trim();
      if (!workspace) {
        startNewChat();
        return;
      }
      setChatLoading(true);
      clearChatErrorState();
      try {
        const adapter = agentAdapters.find((item) => item.id === agentAdapterID);
        const created = await createChatSessionRequest({
          title: adapter ? `${adapter.name} chat` : "External agent chat",
          runtime_kind: "external_agent",
          adapter_id: agentAdapterID,
          workspace,
        });
        setActiveChatSessionID(created.data.id);
        applyChatSession(created.data);
      } catch (error) {
        setChatErrorState(error, "failed to create external agent chat");
        params.setNoticeMessage("error", error instanceof Error ? error.message : "Failed to create external agent chat.");
      } finally {
        setChatLoading(false);
      }
      return;
    }

    const runtimeKind = defaultChatTarget === "model" ? "model" : "agent";
    const workspace = agentWorkspace.trim();
    if (runtimeKind === "agent" && !workspace) {
      startNewChat();
      return;
    }
    if (!model) {
      startNewChat();
      return;
    }
    setChatLoading(true);
    clearChatErrorState();
    try {
      const created = await createChatSessionRequest({
        runtime_kind: runtimeKind,
        provider: providerFilter === "auto" ? "" : providerFilter,
        model,
        ...(runtimeKind === "agent" ? {
          workspace,
          rtk_enabled: hecateRTKEnabled,
        } : {}),
      });
      setActiveChatSessionID(created.data.id);
      applyChatSession(created.data);
    } catch (error) {
      setChatErrorState(error, "failed to create Hecate chat");
      params.setNoticeMessage("error", error instanceof Error ? error.message : "Failed to create Hecate chat.");
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
      if (payload.data.adapter_id) {
        setAgentAdapterID(payload.data.adapter_id);
      }
      const selection = deriveHecateChatSelectionFromSession(payload.data);
      if (selection.provider) {
        setProviderFilter(selection.provider as ProviderFilter);
      }
      if (selection.model) {
        setModel(selection.model);
      }
      if (payload.data.workspace) {
        setAgentWorkspace(payload.data.workspace);
        setAgentWorkspaceBranch(payload.data.workspace_branch ?? "");
      }
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
      setQueuedChatMessages((current) => current.filter((item) => item.session_id !== activeChatSessionID));
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
      params.setNoticeMessage("error", error instanceof Error ? error.message : "Failed to delete agent chat.");
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
      activeChatSession && activeChatSession.task_id === taskID
        ? activeChatSession
        : null;
    if (snapshot) {
      setActiveChatSession((current) => {
        if (!current || current.task_id !== taskID) return current;
        return {
          ...current,
          messages: (current.messages ?? []).map((message) => ({
            ...message,
            activities: message.activities?.map((activity) => {
              if (activity.approval_id !== approvalID && activity.id !== `task:approval:${approvalID}`) return activity;
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
        params.setNoticeMessage("error", "Approval was already resolved upstream and the session refresh failed; reload to see the current state.");
        return false;
      }
      // Genuine failure — roll back so the row reappears and the
      // operator can retry.
      rollbackOptimisticApproval();
      params.setNoticeMessage("error", error instanceof Error ? error.message : "Failed to resolve task approval.");
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

  async function listChatMessageFiles(sessionID: string, messageID: string): Promise<ChatChangedFileRecord[]> {
    try {
      const payload = await listChatMessageFilesRequest(sessionID, messageID);
      return payload.data ?? [];
    } catch (error) {
      params.setNoticeMessage("error", error instanceof Error ? error.message : "Failed to load changed files.");
      return [];
    }
  }

  async function setChatConfigOption(sessionID: string, configID: string, value: string | boolean): Promise<boolean> {
    try {
      const payload = await setChatConfigOptionRequest(sessionID, configID, value);
      applyChatSession(payload.data);
      return true;
    } catch (error) {
      params.setNoticeMessage("error", error instanceof Error ? error.message : "Failed to update adapter control.");
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
      params.setNoticeMessage("error", error instanceof Error ? error.message : "Failed to update chat settings.");
      return false;
    }
  }

  async function getChatMessageFileDiff(sessionID: string, messageID: string, path: string): Promise<ChatChangedFileDiffRecord | null> {
    try {
      const payload = await getChatMessageFileDiffRequest(sessionID, messageID, path);
      return payload.data;
    } catch (error) {
      params.setNoticeMessage("error", error instanceof Error ? error.message : "Failed to load file diff.");
      return null;
    }
  }

  async function revertChatMessageFiles(sessionID: string, messageID: string, paths: string[]): Promise<boolean> {
    try {
      await revertChatMessageFilesRequest(sessionID, messageID, paths);
      await refreshChatSession(sessionID);
      params.setNoticeMessage("success", paths.length > 0 ? "Selected files reverted." : "Captured diff reverted.");
      return true;
    } catch (error) {
      params.setNoticeMessage("error", error instanceof Error ? error.message : "Failed to revert changed files.");
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
        current.map((s) => (s.id === id ? { ...s, title: payload.data.title, updated_at: payload.data.updated_at ?? s.updated_at } : s)),
      );
      if (activeChatSessionID === id) {
        setActiveChatSession((current) => (current ? { ...current, title: payload.data.title, updated_at: payload.data.updated_at ?? current.updated_at } : current));
      }
    } catch (error) {
      params.setNoticeMessage("error", error instanceof Error ? error.message : "Failed to rename chat.");
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

  return {
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
    getChatMessageFileDiff,
    revertChatMessageFiles,
    setChatConfigOption,
    setHecateRTKEnabled,
  };
}
