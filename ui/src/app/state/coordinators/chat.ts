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

import { useContext, useRef, type SyntheticEvent } from "react";

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
  deleteChatAttachment as deleteChatAttachmentRequest,
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
  revertChatWorkspaceFiles as revertChatWorkspaceFilesRequest,
  setChatConfigOption as setChatConfigOptionRequest,
  setChatSettings as setChatSettingsRequest,
  streamChatSession,
  uploadChatAttachment as uploadChatAttachmentRequest,
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
import {
  modelSelectionHasNoToolCalling,
  modelSelectionImageInputCapability,
} from "../../../lib/chat-setup-readiness";
import {
  normalizeChatWorkspaceMode,
  projectByID,
  projectDefaultChatWorkspaceMode,
  projectDefaultWorkspace,
} from "../../../lib/project-workspace";
import {
  toChatMessageViewModel,
  toChatSegmentViewModel,
} from "../../../features/chats/chatTurnViewModels";
import {
  type ChatExecutionMode,
  type ChatTarget,
  type QueuedChatDeliveryErrorCode,
  type QueuedChatDeliveryState,
  type QueuedChatMessage,
  chatTargetToExecutionMode,
} from "../_shared";
import { useApprovals } from "../approvals";
import {
  composerDraftScope,
  composerDraftScopesMatch,
  useChat,
  type ChatCancellationOwner,
  type ChatSessionSnapshotSource,
  type ChatStopFence,
  type ComposerDraftScope,
} from "../chat";
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
  ChatAttachmentRecord,
  ChatApprovalRecord,
  ChatChangedFileDiffRecord,
  ChatChangedFileRecord,
  ChatWorkspaceDiffRecord,
  ChatWorkspaceFilesRecord,
  ChatResponse,
  ChatSessionSummaryRecord,
  ChatSessionRecord,
  ChatWorkspaceMode,
} from "../../../types/chat";

const definiteHecateServerRejectionCodes = new Set([
  "gateway_error",
  "internal_error",
  "upstream_error",
]);
const attachmentDraftCleanupAttempts = 2;
const chatStopSettlementPollIntervalMS = 100;
const directModelImageMediaTypes = new Set(["image/png", "image/jpeg", "image/webp"]);

type ChatSessionAfterCancellation = {
  session: ChatSessionRecord;
  source: ChatSessionSnapshotSource;
};

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

async function deleteUploadedAttachmentDrafts(
  sessionID: string,
  attachments: ChatAttachmentRecord[],
): Promise<number> {
  let remaining = attachments;
  for (
    let attempt = 0;
    attempt < attachmentDraftCleanupAttempts && remaining.length > 0;
    attempt += 1
  ) {
    const results = await Promise.allSettled(
      remaining.map((attachment) => deleteChatAttachmentRequest(sessionID, attachment.id)),
    );
    remaining = remaining.filter((_, index) => {
      const result = results[index];
      return (
        result.status === "rejected" &&
        !(result.reason instanceof ApiError && result.reason.status === 404)
      );
    });
  }
  return remaining.length;
}

function attachmentDraftCleanupError(submitError: unknown, failedCount: number): Error {
  const draftNoun = failedCount === 1 ? "draft" : "drafts";
  const fileNoun = failedCount === 1 ? "file is" : "files are";
  const copyNoun = failedCount === 1 ? "copy" : "copies";
  const cleanupMessage =
    `Hecate could not delete ${failedCount} uploaded file ${draftNoun} after retrying cleanup. ` +
    `The local ${fileNoun} restored in the composer, but the server ${copyNoun} may count ` +
    "toward attachment quota until a later upload reclaims stale drafts after 24 hours.";
  const recoveryAction =
    "Delete this chat to release retained server copies immediately, or remove the restored file and wait until after 24 hours before a later upload triggers stale-draft reclamation.";
  const originalMessage =
    submitError instanceof Error ? submitError.message : "The message submission was rejected.";

  if (submitError instanceof ApiError) {
    return new ApiError(
      `${originalMessage} ${cleanupMessage}`,
      submitError.status,
      submitError.code,
      {
        userMessage: submitError.userMessage,
        operatorAction: [submitError.operatorAction, recoveryAction].filter(Boolean).join(" "),
        requestId: submitError.requestId,
        traceId: submitError.traceId,
        fields: submitError.fields,
      },
    );
  }
  return new Error(`${originalMessage} ${cleanupMessage} ${recoveryAction}`);
}

function attachmentUploadResponseIsAmbiguous(error: unknown): boolean {
  if (!(error instanceof ApiError)) return true;
  // Even a Hecate-shaped 5xx cannot prove that durable attachment creation
  // did not commit: a SQL commit can succeed server-side while its result is
  // lost. Uploads have no idempotency key or draft-list recovery endpoint yet.
  return error.status >= 500 && error.status < 600;
}

function restoreComposerText(original: string, newer: string): string {
  if (!original) return newer;
  if (!newer) return original;
  return `${original}\n\n${newer}`;
}

function ambiguousAttachmentUploadError(submitError: unknown, failedCleanupCount: number): Error {
  const cleanupWarning =
    failedCleanupCount > 0
      ? ` Hecate also could not delete ${failedCleanupCount} acknowledged file ${failedCleanupCount === 1 ? "draft" : "drafts"} after retrying cleanup.`
      : "";
  const ambiguityWarning =
    "The file upload response could not be confirmed. The original prompt and files were restored, but one unlinked server copy may remain and count toward attachment quota until a later upload reclaims stale drafts after 24 hours.";
  const recoveryAction =
    "The upload will not be retried automatically. To avoid accumulating retained drafts, delete this chat before uploading the files again, or remove them and wait until after 24 hours before a later upload triggers reclamation.";
  // Proxy and gateway response bodies are untrusted. Preserve typed routing
  // metadata for support without rendering their raw body, user message, or
  // fields in the operator console.
  const message = `${ambiguityWarning}${cleanupWarning}`;

  if (submitError instanceof ApiError) {
    return new ApiError(message, submitError.status, submitError.code, {
      operatorAction: recoveryAction,
      requestId: submitError.requestId,
      traceId: submitError.traceId,
    });
  }
  return new Error(`${message} ${recoveryAction}`);
}

// The backend bounds agent-chat turns at 30 minutes. Keep the live replay
// observer slightly longer so a valid run is never declared uncertain first.
const queuedReplayFollowTimeoutMS = 31 * 60 * 1000;
const queuedReplayPollIntervalMS = 750;

function waitForQueuedReplayStream(
  stream: Promise<void>,
  isFresh: () => boolean,
  isTerminal: () => boolean,
  deadline: number,
): Promise<"closed" | "terminal" | "stale" | "timeout"> {
  return new Promise((resolve) => {
    let settled = false;
    const finish = (result: "closed" | "terminal" | "stale" | "timeout") => {
      if (settled) return;
      settled = true;
      window.clearInterval(freshnessTimer);
      window.clearTimeout(timeoutTimer);
      resolve(result);
    };
    const freshnessTimer = window.setInterval(() => {
      if (isTerminal()) finish("terminal");
      else if (!isFresh()) finish("stale");
    }, 100);
    const timeoutTimer = window.setTimeout(
      () => finish("timeout"),
      Math.max(0, deadline - Date.now()),
    );
    void stream.then(() => finish("closed"));
  });
}

function waitForQueuedReplayPoll(): Promise<void> {
  return new Promise((resolve) => window.setTimeout(resolve, queuedReplayPollIntervalMS));
}

export function queuedCommittedTurnIsTerminal(
  session: ChatSessionRecord | null,
  committedMessageID: string,
): boolean {
  if (!session || !committedMessageID) return false;
  const messages = session.messages ?? [];
  const committedIndex = messages.findIndex((message) => message.id === committedMessageID);
  if (committedIndex < 0) return false;
  const committed = messages[committedIndex];
  if (committed.role !== "user") return false;
  const nextTurnOffset = messages
    .slice(committedIndex + 1)
    .findIndex((message) => message.role === "user");
  const turnEnd = nextTurnOffset < 0 ? messages.length : committedIndex + 1 + nextTurnOffset;
  const identityFields = ["segment_id", "run_id", "task_id"] as const;
  return messages.slice(committedIndex + 1, turnEnd).some((message) => {
    if (
      message.role !== "assistant" ||
      !["completed", "failed", "cancelled"].includes(message.status ?? "")
    ) {
      return false;
    }
    return identityFields.every(
      (field) => !committed[field] || !message[field] || committed[field] === message[field],
    );
  });
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
  configuredProviders,
}: {
  requested: ChatExecutionMode;
  models: ModelRecord[];
  providerFilter: ProviderFilter;
  model: string;
  toolsEnabled: boolean;
  configuredProviders: ConfiguredStateResponse["data"]["providers"];
}): boolean {
  if (requested !== "hecate_task") return true;
  if (!toolsEnabled) return false;
  return !modelSelectionHasNoToolCalling({
    models,
    providerFilter,
    model,
    configuredProviders,
  });
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
    workspaceMode,
  }: {
    agentID: string;
    model: string;
    projectID: string;
    provider: string;
    title: string;
    workspaceMode?: ChatWorkspaceMode;
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
      const sessionWorkspaceMode = session.workspace_mode ?? "in_place";
      return (
        sessionAgentID === expectedAgentID &&
        sessionProjectID === expectedProjectID &&
        sessionProvider === expectedProvider &&
        sessionModel === expectedModel &&
        sessionTitle === expectedTitle &&
        (workspaceMode === undefined || sessionWorkspaceMode === workspaceMode) &&
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
  refreshRuntimeState: (isCurrent?: () => boolean) => Promise<void>;
  refreshChatSession: (sessionID: string) => Promise<void>;
  clearPendingToolState: () => void;
  resetChatWorkspaceState: () => void;
  submitAgentChat: (queued?: QueuedChatMessage) => Promise<void>;
  reconcileQueuedChatMessage: (id: string) => Promise<boolean>;
  submitChat: (event: SyntheticEvent<HTMLFormElement>) => Promise<void>;
  cancelAgentChat: () => Promise<void>;
  compactChatSession: (sessionID?: string) => Promise<boolean>;
  updateToolResult: (index: number, result: string) => void;
  submitToolResults: () => Promise<void>;
  createChatSession: (options?: CreateChatSessionOptions) => Promise<void>;
  selectChatSession: (id: string, options?: SelectChatSessionOptions) => Promise<boolean>;
  restoreSavedComposerDraft: (sessionID: string) => boolean;
  startNewChat: () => void;
  deleteChatSession: (id: string) => Promise<boolean>;
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
  selectChatModel: (nextModel: string) => void;
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
    expectedRevision: string,
  ) => Promise<ChatWorkspaceDiffRecord | null>;
  getChatMessageFileDiff: (
    sessionID: string,
    messageID: string,
    path: string,
  ) => Promise<ChatChangedFileDiffRecord | null>;
  setChatConfigOption: (
    sessionID: string,
    configID: string,
    value: string | boolean,
  ) => Promise<boolean>;
  setHecateRTKEnabled: (enabled: boolean) => Promise<boolean>;
  setHecateWorkspaceMode: (mode: ChatWorkspaceMode) => Promise<boolean>;
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
    getMessageSnapshot,
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
    agentWorkspaceBranch,
    agentWorkspaceMode,
    chatSessions,
    activeChatSessionID,
    activeChatSession,
    workspaceModeMutation,
    composerDraftsBySessionID,
    savedComposerDraftsBySessionID,
    recoverableComposerDraft,
    activeRecoverableComposerDraftID,
    pendingChatAttachments,
    model,
    systemPrompt,
    chatLoading,
    pendingToolCalls,
    pendingThread,
    providerFilter,
  } = chat.state;
  const {
    isChatCreationActive,
    beginChatTurn,
    bindChatTurnSession,
    registerChatTurnPreAdmissionCancel,
    startChatTurnAdmission,
    confirmChatTurnServerCancellation,
    cancelChatTurnBeforeAdmission,
    chatTurnServerCancellationReady,
    completeChatTurn,
    isChatTurnActive,
    isCurrentChatTurn,
    getActiveChatTurnSessionID,
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
    setAgentWorkspaceMode,
    setChatSessions,
    setActiveChatSessionID,
    setActiveChatSession,
    setWorkspaceModeMutation,
    setComposerDraftsBySessionID,
    setSavedComposerDraftsBySessionID,
    saveRecoverableComposerDraft,
    setRecoverableComposerDraft,
    setActiveRecoverableComposerDraftID,
    setPendingChatAttachments,
    setQueuedChatMessages,
    deleteQueuedChatMessagesForSession,
    enqueueQueuedChatMessage,
    setModel,
    setSystemPrompt,
    setChatLoading,
    beginChatCancellation,
    finishChatCancellation,
    hasChatCancellationOwner,
    chatCancellationOwnsSession,
    currentChatCancellationEpoch,
    waitForChatCancellationRelease,
    beginChatStopFence,
    clearChatStopFence,
    acceptChatStopFence,
    getChatStopFence,
    stopReadTokenAtRequestStart,
    chatStopFenceAllowsSnapshot,
    chatStopFenceForTurnSettlement,
    chatStopFenceSuppressesApproval,
    clearSettledChatStopFenceForNewTurn,
    setStreamingContent,
    setChatResult,
    setPendingToolCalls,
    setPendingThread,
    setProviderFilter,
    setChatError,
    clearChatErrorState,
    setChatErrorState,
    claimChatSessionIntent,
    currentChatSessionIntent,
    isCurrentChatSessionIntent,
    tryBeginChatSessionCreate,
    finishChatSessionCreate,
    currentChatResetGeneration,
    beginChatRequestOperation,
    bindChatRequestOperationSession,
    finishChatRequestOperation,
    isCurrentChatRequestOperation,
    hasPendingChatAttachments,
    beginChatOwnershipMutation,
    finishChatOwnershipMutation,
    isChatOwnershipMutationInFlight,
    beginChatAttachmentTurn,
    bindChatAttachmentTurn,
    finishChatAttachmentTurn,
    hasChatAttachmentTurn,
    chatAttachmentTurnSessionID,
    currentActiveChatSessionID,
    currentQueuedChatMessage,
    hasDurableQueuedChatSubmittingFence,
    tombstoneDeletedChatSession,
    isChatSessionDeleted,
  } = chat.actions;
  const upsertPendingApproval = approvals.actions.upsertPending;
  const removePendingApproval = approvals.actions.removePending;
  const invalidatePendingApprovals = approvals.actions.invalidatePendingForSession;
  const refetchPendingApprovals = approvals.actions.refetchPending;

  // A create has no canonical session id until POST returns, but its composer
  // remains editable. Keep transient ownership with the create so selecting
  // another chat cannot discard edits made while the request is pending.
  const pendingCreateDraftRef = useRef<{
    generation: number;
    draft: string;
    scope: ComposerDraftScope;
    recoveryID: number | null;
  } | null>(null);
  const nextWorkspaceModeMutationTokenRef = useRef(0);
  const workspaceModeMutationRef = useRef(workspaceModeMutation);
  workspaceModeMutationRef.current = workspaceModeMutation;
  const coordinatorRenderGenerationRef = useRef(0);
  coordinatorRenderGenerationRef.current += 1;
  const lastSubmitClaimRef = useRef<{ renderGeneration: number; content: string } | null>(null);
  if (
    pendingCreateDraftRef.current &&
    !activeChatSessionID &&
    isCurrentActiveChatTransition(pendingCreateDraftRef.current.generation)
  ) {
    pendingCreateDraftRef.current.draft = message;
  }

  function pendingCreateDraft(generation: number, fallback: string): string {
    return pendingCreateDraftRef.current?.generation === generation
      ? pendingCreateDraftRef.current.draft
      : fallback;
  }

  function pendingCreateRecoveryID(generation: number, fallback: number | null): number | null {
    return pendingCreateDraftRef.current?.generation === generation
      ? pendingCreateDraftRef.current.recoveryID
      : fallback;
  }

  function clearPendingCreateDraft(generation: number) {
    if (pendingCreateDraftRef.current?.generation === generation) {
      pendingCreateDraftRef.current = null;
    }
  }

  function clearRecoverableComposerDraft(recoveryID: number | null) {
    if (recoveryID === null) return;
    setRecoverableComposerDraft((current) => (current?.id === recoveryID ? null : current));
    setActiveRecoverableComposerDraftID((current) => (current === recoveryID ? null : current));
  }

  function preservePendingCreateDraft(generation: number, bindToComposer: boolean) {
    const pending = pendingCreateDraftRef.current;
    if (!pending || pending.generation !== generation) return;
    if (!pending.draft.trim()) {
      clearRecoverableComposerDraft(pending.recoveryID);
      pending.recoveryID = null;
      return;
    }
    const recoveryID = saveRecoverableComposerDraft({
      ...(pending.recoveryID === null ? {} : { id: pending.recoveryID }),
      content: pending.draft,
      scope: pending.scope,
    });
    pending.recoveryID = recoveryID;
    setActiveRecoverableComposerDraftID(bindToComposer ? recoveryID : null);
  }

  function currentComposerDraftScope(): ComposerDraftScope {
    const currentAgentID = defaultChatTarget === "external_agent" ? agentAdapterID : "hecate";
    return composerDraftScope({
      projectID: activeProjectID,
      agentID: currentAgentID,
      provider: providerFilter,
      model,
      workspace: workspaceForNewChat(activeProjectID),
    });
  }

  function releaseDetachedComposerDraft() {
    if (activeChatSessionID) return;
    if (activeRecoverableComposerDraftID !== null) {
      const recoveryID = activeRecoverableComposerDraftID;
      setRecoverableComposerDraft((current) => {
        if (current?.id !== recoveryID) return current;
        return message.trim() ? { ...current, content: message } : null;
      });
      setActiveRecoverableComposerDraftID(null);
      return;
    }
    if (!message.trim()) return;
    saveRecoverableComposerDraft({
      content: message,
      scope: currentComposerDraftScope(),
    });
  }

  function clearPendingToolState() {
    setPendingToolCalls([]);
    setPendingThread(null);
  }

  function resetChatWorkspaceState() {
    setMessage("");
    setPendingChatAttachments([]);
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

  function saveSessionComposerDraft(sessionID: string, draft: string) {
    if (!sessionID || !draft.trim()) return;
    setSavedComposerDraftsBySessionID((current) => {
      const next = new Map(current);
      next.set(sessionID, [...(current.get(sessionID) ?? []), draft]);
      return next;
    });
  }

  function consumeSavedComposerDraft(sessionID: string, draft: string) {
    setSavedComposerDraftsBySessionID((current) => {
      const saved = current.get(sessionID);
      if (!saved?.length) return current;
      const index = saved.indexOf(draft);
      if (index < 0) return current;
      const nextSaved = [...saved.slice(0, index), ...saved.slice(index + 1)];
      const next = new Map(current);
      if (nextSaved.length > 0) next.set(sessionID, nextSaved);
      else next.delete(sessionID);
      return next;
    });
  }

  function restoreSavedComposerDraft(sessionID: string): boolean {
    const saved = savedComposerDraftsBySessionID.get(sessionID);
    const restored = saved?.[0];
    if (!restored) return false;
    const currentDraft = activeChatSessionID === sessionID ? getMessageSnapshot().content : "";
    setSavedComposerDraftsBySessionID((current) => {
      const currentSaved = current.get(sessionID);
      if (!currentSaved?.length) return current;
      const nextSaved = currentSaved.slice(1);
      if (currentDraft.trim()) nextSaved.push(currentDraft);
      const next = new Map(current);
      if (nextSaved.length > 0) next.set(sessionID, nextSaved);
      else next.delete(sessionID);
      return next;
    });
    rememberChatComposerDraft(sessionID, restored);
    if (activeChatSessionID === sessionID) setMessage(restored);
    return true;
  }

  async function refreshRuntimeState(isCurrent: () => boolean = () => true) {
    try {
      const usageSummaryResult = await getUsageSummary("");
      if (!isCurrent()) return;
      setUsageSummary(usageSummaryResult.data);
    } catch {
      // Keep chat responsive even if refresh paths fail.
    }
    if (!isCurrent()) return;
    try {
      const usageEventsResult = await getUsageEvents(20);
      if (!isCurrent()) return;
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
    if (
      blockWhileChatCancellationOwnsSession(
        currentActiveChatSessionID(),
        "changing the provider route",
      )
    ) {
      return;
    }
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

  function selectChatModel(nextModel: string) {
    if (blockWhileChatCancellationOwnsSession(currentActiveChatSessionID(), "changing the model")) {
      return;
    }
    setModel(nextModel);
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

  function workspaceModeForNewChat(projectID: string): ChatWorkspaceMode {
    const id = projectID.trim();
    if (!id) return normalizeChatWorkspaceMode(agentWorkspaceMode);
    const project =
      id === activeProjectID ? projects.activeProject : projectByID(projects.state.projects, id);
    return projectDefaultChatWorkspaceMode(project);
  }

  function workspaceForActiveTurn(): string {
    const selectedWorkspace =
      activeChatSession?.id === activeChatSessionID
        ? activeChatSession.workspace?.trim() || ""
        : "";
    return selectedWorkspace || workspaceForNewChat(activeProjectID);
  }

  function snapshotSourceForStopRead(stopToken: number | null): ChatSessionSnapshotSource {
    return stopToken === null ? { kind: "unscoped" } : { kind: "stop_read", stopToken };
  }

  async function waitForChatStopFenceSettlement(
    fence: ChatStopFence,
  ): Promise<"terminal" | "stale" | "timeout"> {
    if (getChatStopFence(fence.sessionID) !== fence) return "stale";
    if (fence.phase === "settled") return "terminal";
    const remaining = Math.max(0, fence.settlementDeadline - Date.now());
    let timeoutID = 0;
    try {
      const outcome = await Promise.race([
        fence.settlement.then(() => "settled" as const),
        new Promise<"timeout">((resolve) => {
          timeoutID = window.setTimeout(() => resolve("timeout"), remaining);
        }),
      ]);
      if (outcome === "timeout") return "timeout";
      if (getChatStopFence(fence.sessionID) !== fence) return "stale";
      return "terminal";
    } finally {
      if (timeoutID) window.clearTimeout(timeoutID);
    }
  }

  async function waitForProtectedChatStopFenceSettlement(
    sessionID: string,
    turnGeneration: number,
    initialFence: ChatStopFence,
  ): Promise<"terminal" | "stale" | "timeout"> {
    let fence: ChatStopFence | null = initialFence;
    for (;;) {
      const outcome = await waitForChatStopFenceSettlement(fence);
      if (outcome !== "stale") return outcome;
      const restoredFence = chatStopFenceForTurnSettlement(sessionID, turnGeneration);
      if (!restoredFence || restoredFence === fence) return "stale";
      fence = restoredFence;
    }
  }

  async function pollAcceptedChatStopFence(fence: ChatStopFence): Promise<void> {
    while (
      getChatStopFence(fence.sessionID) === fence &&
      fence.phase === "accepted" &&
      Date.now() < fence.settlementDeadline
    ) {
      let timeoutID = 0;
      const remaining = Math.max(0, fence.settlementDeadline - Date.now());
      const outcome = await Promise.race([
        getChatSession(fence.sessionID).then(
          (payload) => ({ kind: "snapshot" as const, payload }),
          () => ({ kind: "read_failed" as const }),
        ),
        fence.settlement.then(() => ({ kind: "settled" as const })),
        new Promise<{ kind: "timeout" }>((resolve) => {
          timeoutID = window.setTimeout(() => resolve({ kind: "timeout" }), remaining);
        }),
      ]);
      if (timeoutID) window.clearTimeout(timeoutID);
      if (outcome.kind === "settled" || outcome.kind === "timeout") return;
      if (outcome.kind === "snapshot") {
        applyChatSession(outcome.payload.data, { kind: "stop_read", stopToken: fence.token });
      }
      if (getChatStopFence(fence.sessionID) !== fence || fence.phase !== "accepted") {
        return;
      }
      const pollDelay = Math.min(
        chatStopSettlementPollIntervalMS,
        Math.max(0, fence.settlementDeadline - Date.now()),
      );
      if (pollDelay === 0) return;
      let pollTimerID = 0;
      await Promise.race([
        fence.settlement,
        new Promise<void>((resolve) => {
          pollTimerID = window.setTimeout(resolve, pollDelay);
        }),
      ]);
      if (pollTimerID) window.clearTimeout(pollTimerID);
    }
  }

  function catchUpAfterFailedChatStop(sessionID: string, cancellationEpoch: number) {
    const isCurrent = () =>
      currentChatCancellationEpoch(sessionID) === cancellationEpoch &&
      !chatCancellationOwnsSession(sessionID) &&
      getChatStopFence(sessionID) === null &&
      !isChatSessionDeleted(sessionID);
    void getChatSession(sessionID)
      .then((payload) => {
        if (isCurrent()) applyChatSession(payload.data);
      })
      .catch(() => undefined);
    void refetchPendingApprovals(sessionID, isCurrent);
  }

  function blockWhileChatOwnershipMutationRuns(action: string): boolean {
    if (!isChatOwnershipMutationInFlight()) return false;
    params.setNoticeMessage(
      "error",
      `Wait for the current chat ownership change to finish before ${action}.`,
    );
    return true;
  }

  function blockWhileChatCancellationOwnsSession(sessionID: string, action: string): boolean {
    if (!chatCancellationOwnsSession(sessionID)) return false;
    params.setNoticeMessage("error", `Wait for Stop to finish before ${action}.`);
    return true;
  }

  async function readAfterChatCancellationSettles<T>(
    sessionID: string,
    read: () => Promise<T>,
  ): Promise<T | null> {
    for (;;) {
      const readEpoch = currentChatCancellationEpoch(sessionID);
      await waitForChatCancellationRelease(sessionID);
      if (isChatSessionDeleted(sessionID)) return null;
      if (
        currentChatCancellationEpoch(sessionID) !== readEpoch ||
        chatCancellationOwnsSession(sessionID)
      ) {
        continue;
      }
      const result = await read();
      if (isChatSessionDeleted(sessionID)) return null;
      if (
        currentChatCancellationEpoch(sessionID) !== readEpoch ||
        chatCancellationOwnsSession(sessionID)
      ) {
        continue;
      }
      return result;
    }
  }

  async function refetchPendingApprovalsAfterCancellation(sessionID: string): Promise<void> {
    for (;;) {
      const readEpoch = currentChatCancellationEpoch(sessionID);
      await waitForChatCancellationRelease(sessionID);
      if (isChatSessionDeleted(sessionID)) return;
      const stopFence = getChatStopFence(sessionID);
      if (stopFence && stopFence.phase !== "requesting") return;
      if (
        currentChatCancellationEpoch(sessionID) !== readEpoch ||
        chatCancellationOwnsSession(sessionID)
      ) {
        continue;
      }
      const readIsCurrent = () =>
        currentChatCancellationEpoch(sessionID) === readEpoch &&
        !chatCancellationOwnsSession(sessionID) &&
        getChatStopFence(sessionID) === null &&
        !isChatSessionDeleted(sessionID);
      await refetchPendingApprovals(sessionID, readIsCurrent);
      if (isChatSessionDeleted(sessionID)) return;
      if (!readIsCurrent()) continue;
      return;
    }
  }

  async function latestSessionAfterCancellation(
    sessionID: string,
    cancellationEpoch: number,
    fallback: ChatSessionRecord,
    fallbackSource: ChatSessionSnapshotSource = { kind: "unscoped" },
  ): Promise<ChatSessionAfterCancellation> {
    if (currentChatCancellationEpoch(sessionID) === cancellationEpoch) {
      return { session: fallback, source: fallbackSource };
    }
    let stopReadToken: number | null = null;
    const session = await readAfterChatCancellationSettles(sessionID, async () => {
      stopReadToken = stopReadTokenAtRequestStart(sessionID);
      return (await getChatSession(sessionID)).data;
    });
    return session
      ? { session, source: snapshotSourceForStopRead(stopReadToken) }
      : { session: fallback, source: fallbackSource };
  }

  async function refreshSessionAfterCancellation(
    sessionID: string,
    cancellationEpoch: number,
  ): Promise<boolean> {
    if (currentChatCancellationEpoch(sessionID) === cancellationEpoch) return false;
    let stopReadToken: number | null = null;
    const session = await readAfterChatCancellationSettles(sessionID, async () => {
      stopReadToken = stopReadTokenAtRequestStart(sessionID);
      return (await getChatSession(sessionID)).data;
    });
    if (session) applyChatSession(session, snapshotSourceForStopRead(stopReadToken));
    return true;
  }

  async function refreshApprovalStateAfterCancellation(
    sessionID: string,
    cancellationEpoch: number,
  ): Promise<void> {
    if (currentChatCancellationEpoch(sessionID) === cancellationEpoch) return;
    if (!isChatSessionDeleted(sessionID)) {
      try {
        let stopReadToken: number | null = null;
        const session = await readAfterChatCancellationSettles(sessionID, async () => {
          stopReadToken = stopReadTokenAtRequestStart(sessionID);
          return (await getChatSession(sessionID)).data;
        });
        if (session) applyChatSession(session, snapshotSourceForStopRead(stopReadToken));
      } catch {
        // Keep the terminal-fenced projection when this best-effort
        // post-cancellation read is unavailable.
      }
    }
    await refetchPendingApprovalsAfterCancellation(sessionID);
  }

  function setChatTarget(nextTarget: ChatTarget) {
    if (blockWhileChatOwnershipMutationRuns("switching agents")) return;
    if (nextTarget !== params.chatTarget && hasChatAttachmentTurn()) {
      params.setNoticeMessage("error", "Wait for the attachment response before switching agents.");
      return;
    }
    if (pendingChatAttachments.length > 0 && nextTarget === "agent") {
      const reason = pendingFilesHecateBlockReason();
      if (reason) {
        params.setNoticeMessage("error", reason);
        return;
      }
    }
    if (activeChatSessionID && !activeChatSession) {
      // A session selection has published its target id but has not hydrated
      // the record yet. Switching runtime ownership must still preempt that
      // transition; otherwise its late response can silently reselect it.
      claimChatSessionIntent();
      setActiveChatSessionID("");
      setActiveChatSession(null);
      setAgentWorkspaceBranch("");
      setDefaultChatTarget(nextTarget);
      return;
    }
    if (activeChatSessionID && activeChatSession) {
      const currentExternal = chatSessionIsExternal(activeChatSession);
      const nextExternal = nextTarget === "external_agent";
      if (currentExternal !== nextExternal) {
        claimChatSessionIntent();
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
    if (blockWhileChatOwnershipMutationRuns("changing Tools")) return;
    if (enabled && hasChatAttachmentTurn()) {
      params.setNoticeMessage("error", "Wait for the attachment response before turning Tools on.");
      return;
    }
    if (enabled && pendingChatAttachments.length > 0) {
      params.setNoticeMessage("error", "Remove attached files before turning Tools on.");
      return;
    }
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
    if (blockWhileChatOwnershipMutationRuns("switching agents")) return;
    if (hasChatAttachmentTurn()) {
      params.setNoticeMessage("error", "Wait for the attachment response before switching agents.");
      return;
    }
    if (nextAgentID === "hecate") {
      if (pendingChatAttachments.length > 0) {
        const reason = pendingFilesHecateBlockReason();
        if (reason) {
          params.setNoticeMessage("error", reason);
          return;
        }
      }
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

  function pendingFilesHecateBlockReason(): string {
    if (defaultChatToolsEnabled) {
      return "Remove attached files before switching to Hecate with Tools on.";
    }
    if (
      pendingChatAttachments.some((attachment) => {
        const mediaType = attachment.file.type.trim().toLowerCase();
        return !mediaType || !directModelImageMediaTypes.has(mediaType);
      })
    ) {
      return "Remove files without a declared PNG, JPEG, or WebP type before switching to Hecate Chat.";
    }
    const imageCapability = modelSelectionImageInputCapability({
      models,
      providerFilter,
      model,
      configuredProviders,
    });
    if (imageCapability !== "supported") {
      return "Remove attached files or choose a Hecate model with confirmed image input before switching.";
    }
    return "";
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
    const projectID =
      activeChatSession?.id === sessionID ? (activeChatSession.project_id ?? "").trim() : "";
    return {
      id: `queued-chat-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
      session_id: sessionID,
      project_id: projectID,
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
  ): boolean {
    const queued = buildQueuedChatMessage(content, executionMode, sessionID, toolsEnabled);
    const admission = enqueueQueuedChatMessage(queued);
    if (admission !== "admitted") {
      const resetObserved = admission === "reset_observed";
      const itemConflict = admission === "item_conflict";
      const sessionDeleted = admission === "session_deleted";
      const projectDeleted = admission === "project_deleted";
      setChatErrorState(
        new ApiError(
          resetObserved
            ? "This follow-up was not queued because another tab reset Hecate data."
            : itemConflict
              ? "This follow-up was not queued because its browser queue id is already in use."
              : sessionDeleted
                ? "This follow-up was not queued because its chat was deleted in another tab."
                : projectDeleted
                  ? "This follow-up was not queued because its project was deleted in another tab."
                  : "This follow-up was not queued because browser storage could not preserve it.",
          resetObserved || itemConflict || sessionDeleted || projectDeleted ? 409 : 507,
          resetObserved
            ? "chat.queue_reset_observed"
            : itemConflict
              ? "chat.queue_item_conflict"
              : sessionDeleted
                ? "chat.queue_session_deleted"
                : projectDeleted
                  ? "chat.queue_project_deleted"
                  : "chat.queue_storage_unavailable",
          {
            operatorAction: resetObserved
              ? "The prompt is still in the composer. Copy it, reload this tab, then submit against refreshed chat state."
              : itemConflict
                ? "The prompt is still in the composer. Retry to generate a fresh queue id."
                : sessionDeleted
                  ? "The prompt is still in the composer. Copy it, reload this tab, and choose an existing chat before submitting."
                  : projectDeleted
                    ? "The prompt is still in the composer. Copy it, reload this tab, and choose an existing chat before submitting."
                    : "The prompt is still in the composer. Free browser storage; if the failure persists, copy unsent prompts and clear Hecate site data in browser settings.",
          },
        ),
      );
      return false;
    }
    clearChatErrorState();
    setMessage("");
    return true;
  }

  function setQueuedSnapshotDeliveryState(
    queued: QueuedChatMessage,
    deliveryState: QueuedChatDeliveryState,
    deliveryErrorCode?: QueuedChatDeliveryErrorCode,
  ) {
    setQueuedChatMessages((current) =>
      current.map((item) =>
        item.id === queued.id && item.content === queued.content
          ? {
              ...item,
              delivery_state: deliveryState,
              delivery_error_code: deliveryErrorCode,
            }
          : item,
      ),
    );
  }

  function removeQueuedSnapshot(queued: QueuedChatMessage) {
    setQueuedChatMessages((current) =>
      current.filter((item) => item.id !== queued.id || item.content !== queued.content),
    );
  }

  function removeDeliveredQueuedSnapshot(queued: QueuedChatMessage) {
    consumeSavedComposerDraft(queued.session_id, queued.content);
    removeQueuedSnapshot(queued);
  }

  function queuedCommitIndex(
    messages: NonNullable<ChatSessionRecord["messages"]>,
    queued: QueuedChatMessage,
  ): number {
    const baseline = new Set(queued.delivery_baseline_message_ids ?? []);
    return messages.findIndex(
      (candidate) =>
        candidate.role === "user" &&
        !baseline.has(candidate.id) &&
        candidate.content.trim() === queued.content.trim(),
    );
  }

  function applyChatSession(
    session: ChatSessionRecord,
    source: ChatSessionSnapshotSource = { kind: "unscoped" },
  ) {
    if (isChatSessionDeleted(session.id, session.project_id)) return false;
    if (!chatStopFenceAllowsSnapshot(session, source)) return false;
    recordChatSessionSummary(session);
    if (currentActiveChatSessionID() !== session.id) return false;
    // Fold the incoming snapshot onto the previous one so unchanged
    // message/segment objects keep their identity. The live stream
    // republishes a full session snapshot per coalesced flush; without
    // this, every transcript row would get a fresh object identity and
    // re-render (and re-parse its markdown) on every streamed batch.
    setActiveChatSession((prev) => reconcileChatSession(prev, session));
    syncHecateSelectionFromSession(session);
    setAgentWorkspaceBranch(session.workspace_branch ?? "");
    return true;
  }

  function recordChatSessionSummary(session: ChatSessionRecord) {
    setChatSessions((current) => [
      renderChatSessionSummary(session),
      ...current.filter((entry) => entry.id !== session.id),
    ]);
  }

  function discardDeletedCreatedSession(
    session: ChatSessionRecord,
    resetGeneration: number,
    requestedProjectID: string,
  ): boolean {
    const deleted =
      currentChatResetGeneration() !== resetGeneration ||
      isChatSessionDeleted(session.id, session.project_id) ||
      (requestedProjectID !== "" && isChatSessionDeleted(session.id, requestedProjectID));
    if (!deleted) return false;
    tombstoneDeletedChatSession(session.id);
    return true;
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
    const stopToken = stopReadTokenAtRequestStart(sessionID);
    const payload = await getChatSession(sessionID);
    applyChatSession(payload.data, snapshotSourceForStopRead(stopToken));
  }

  async function reconcileQueuedChatMessage(id: string): Promise<boolean> {
    const queued = currentQueuedChatMessage(id);
    if (!queued || queued.delivery_state !== "reconcile_required") return false;
    if (blockWhileChatOwnershipMutationRuns("checking queued delivery")) return false;
    if (blockWhileChatCancellationOwnsSession(queued.session_id, "checking this queued message")) {
      return false;
    }

    const resetGeneration = currentChatResetGeneration();
    const cancellationEpoch = currentChatCancellationEpoch(queued.session_id);
    const stopRequestToken = stopReadTokenAtRequestStart(queued.session_id);
    const requestToken = beginChatRequestOperation(queued.session_id);
    let projectID =
      queued.project_id ??
      (activeChatSession?.id === queued.session_id ? (activeChatSession.project_id ?? "") : "");
    const isFresh = () =>
      isCurrentChatRequestOperation(requestToken) &&
      currentChatResetGeneration() === resetGeneration &&
      !isChatSessionDeleted(queued.session_id, projectID);
    const sourceIsCurrent = () => isFresh() && currentActiveChatSessionID() === queued.session_id;
    const setReconcileErrorIfSelected = (error: unknown, fallback?: string) => {
      if (sourceIsCurrent()) setChatErrorState(error, fallback);
    };

    setChatLoading(true);
    if (sourceIsCurrent()) clearChatErrorState();
    try {
      if (queued.delivery_storage_conflict === "ready_replacement") {
        setReconcileErrorIfSelected(
          new Error(
            "This browser queue id now refers to a different unsent payload. Remove it, review the text, and submit it again to create a fresh queue id.",
          ),
        );
        return false;
      }
      if (queued.delivery_error_code === "chat.client_request_conflict") {
        setReconcileErrorIfSelected(
          new Error(
            "This queued request id is committed to a different payload. Matching transcript text cannot prove this queued request was delivered; remove it after reviewing the authoritative chat.",
          ),
        );
        return false;
      }
      if (queued.delivery_idempotency_keyed) {
        const replaying = {
          ...queued,
          delivery_state: "submitting" as const,
          delivery_error_code: undefined,
        };
        setQueuedSnapshotDeliveryState(queued, "submitting");
        if (!hasDurableQueuedChatSubmittingFence(replaying)) {
          setQueuedSnapshotDeliveryState(queued, "reconcile_required");
          setReconcileErrorIfSelected(
            new Error(
              "Queued delivery cannot be checked because browser storage did not preserve its submission fence. Free browser storage, then check again.",
            ),
          );
          return false;
        }
        await submitAgentChat(replaying);
        return currentQueuedChatMessage(id) === undefined;
      }

      const payload = await getChatSession(queued.session_id);
      const latest = await latestSessionAfterCancellation(
        queued.session_id,
        cancellationEpoch,
        payload.data,
        snapshotSourceForStopRead(stopRequestToken),
      );
      const { session } = latest;
      projectID = session.project_id ?? projectID;
      if (!isFresh()) return false;
      applyChatSession(session, latest.source);
      const baseline = queued.delivery_baseline_message_ids;
      if (!baseline) {
        setReconcileErrorIfSelected(
          new Error(
            "This queued attempt predates safe delivery reconciliation. Check the transcript manually, then remove it if the message was accepted. Hecate will not retry it automatically.",
          ),
        );
        return false;
      }

      const messages = session.messages ?? [];
      if (queuedCommitIndex(messages, queued) >= 0) {
        removeDeliveredQueuedSnapshot(queued);
        if (sourceIsCurrent()) {
          params.setNoticeMessage("success", "The queued message is already in the transcript.");
        }
        return true;
      }
      if (chatSessionIsBusy(session)) {
        setReconcileErrorIfSelected(
          new Error(
            "This chat is still processing work, so queued-message delivery cannot be reconciled yet. Check status again after the chat is idle.",
          ),
        );
        return false;
      }

      const authoritativeMessageIDs = new Set(messages.map((message) => message.id));
      if (baseline.some((messageID) => !authoritativeMessageIDs.has(messageID))) {
        setReconcileErrorIfSelected(
          new Error(
            "The transcript changed before queued-message delivery could be reconciled safely. Check it manually; Hecate will not retry this message automatically.",
          ),
        );
        return false;
      }
      const baselineMessageIDs = new Set(baseline);
      if (messages.some((message) => !baselineMessageIDs.has(message.id))) {
        setReconcileErrorIfSelected(
          new Error(
            "The transcript gained other messages before queued-message delivery could be reconciled safely. Check it manually; Hecate will not retry this message automatically.",
          ),
        );
        return false;
      }

      setQueuedSnapshotDeliveryState(queued, "retryable");
      setReconcileErrorIfSelected(
        new Error(
          "No matching committed message was found in the authoritative transcript. Review the queued prompt, then choose Retry or remove it.",
        ),
      );
      return false;
    } catch (error) {
      if (!isFresh()) return false;
      setReconcileErrorIfSelected(error, "failed to reconcile queued message delivery");
      return false;
    } finally {
      if (finishChatRequestOperation(requestToken)) setChatLoading(false);
    }
  }

  async function submitAgentChat(queued?: QueuedChatMessage) {
    // A preparatory create owns its detached draft while the session id is
    // unknown. Keyboard/programmatic submits must not race a second create.
    if (!queued && isChatCreationActive() && !activeChatSessionID) return;
    const submitResetGeneration = currentChatResetGeneration();
    const submittedMessageSnapshot = getMessageSnapshot();
    const composerContentSnapshot = queued?.content ?? submittedMessageSnapshot.content;
    const content = composerContentSnapshot.trim();
    const attachmentDrafts = queued ? [] : pendingChatAttachments;
    if (!content && attachmentDrafts.length === 0) return;
    const currentRenderGeneration = coordinatorRenderGenerationRef.current;
    if (
      !queued &&
      lastSubmitClaimRef.current?.renderGeneration === currentRenderGeneration &&
      lastSubmitClaimRef.current.content === composerContentSnapshot
    ) {
      return;
    }
    const claimCurrentSubmit = () => {
      if (queued) return;
      lastSubmitClaimRef.current = {
        renderGeneration: currentRenderGeneration,
        content: composerContentSnapshot,
      };
    };
    const settleQueuedLocalFailure = () => {
      if (queued) setQueuedSnapshotDeliveryState(queued, "retryable");
    };
    if (hasChatCancellationOwner()) {
      settleQueuedLocalFailure();
      if (!queued) {
        setChatErrorState(
          new ApiError(
            "Wait for the current Stop request to finish before sending another message.",
            409,
            "chat.cancellation_in_flight",
          ),
        );
      }
      return;
    }
    if (isChatOwnershipMutationInFlight()) {
      setChatErrorState(
        new ApiError(
          "Wait for the current chat ownership change to finish before sending a message.",
          409,
          "chat.ownership_mutation_in_flight",
        ),
      );
      settleQueuedLocalFailure();
      return;
    }
    const workspaceMutation = workspaceModeMutationRef.current;
    const submittedSessionID = queued?.session_id ?? activeChatSessionID;
    if (workspaceMutation?.sessionID === submittedSessionID) {
      setChatErrorState(
        new ApiError(
          "Wait for Hecate to confirm workspace execution before sending a message.",
          409,
          "chat.workspace_mode_mutation_in_flight",
        ),
      );
      settleQueuedLocalFailure();
      return;
    }

    const turnProviderFilter = queued?.provider_filter ?? providerFilter;
    const turnModel = queued?.model ?? model;
    const requestedExecutionMode =
      queued?.execution_mode ?? chatTargetToExecutionMode(params.chatTarget);
    const requestedToolsEnabled = queued?.tools_enabled ?? resolveToolsEnabled(activeChatSessionID);
    const isExternalAgent = requestedExecutionMode === "external_agent";
    const turnToolsEnabled = queued
      ? queued.tools_enabled
      : isExternalAgent
        ? true
        : effectiveHecateToolsEnabled({
            requested: requestedExecutionMode,
            models,
            providerFilter: turnProviderFilter,
            model: turnModel,
            toolsEnabled: requestedToolsEnabled,
            configuredProviders,
          });
    const turnExecutionMode = requestedExecutionMode;
    const isDirectModelTurn = !isExternalAgent && !turnToolsEnabled;
    const turnAgentID = queued?.agent_id ?? agentAdapterID;
    const detachedSubmitScope = composerDraftScope({
      projectID: activeProjectID,
      agentID: isExternalAgent ? turnAgentID : "hecate",
      provider: turnProviderFilter,
      model: turnModel,
      workspace: workspaceForActiveTurn(),
    });
    const detachedRecovery =
      !queued &&
      !activeChatSessionID &&
      activeRecoverableComposerDraftID !== null &&
      recoverableComposerDraft?.id === activeRecoverableComposerDraftID
        ? recoverableComposerDraft
        : null;
    if (
      detachedRecovery &&
      !composerDraftScopesMatch(detachedRecovery.scope, detachedSubmitScope)
    ) {
      setRecoverableComposerDraft((current) =>
        current?.id === detachedRecovery.id
          ? { ...current, content: composerContentSnapshot }
          : current,
      );
      setActiveRecoverableComposerDraftID(null);
      setMessage("");
      params.setNoticeMessage(
        "error",
        "That draft belongs to another chat setup. Return to the matching setup and start a new chat to restore it.",
      );
      return;
    }
    const attachmentTurnSessionID = chatAttachmentTurnSessionID();
    if (hasChatAttachmentTurn()) {
      if (attachmentDrafts.length > 0) {
        setChatErrorState(
          new ApiError(
            "Wait for the current attachment response before sending more files.",
            409,
            "chat.attachments_turn_in_flight",
          ),
        );
        return;
      }
      if (
        !queued &&
        attachmentTurnSessionID &&
        currentActiveChatSessionID() === attachmentTurnSessionID
      ) {
        setChatErrorState(
          new ApiError(
            "Wait for the attachment response before sending this follow-up. It remains in the composer.",
            409,
            "chat.attachments_turn_in_flight",
            {
              operatorAction:
                "Send the retained text after the attachment response reaches a known outcome.",
            },
          ),
        );
        return;
      }
      setChatErrorState(
        new ApiError(
          "Wait for the current attachment response before sending another message.",
          409,
          "chat.attachments_turn_in_flight",
        ),
      );
      settleQueuedLocalFailure();
      return;
    }
    if (attachmentDrafts.length > 0 && !isDirectModelTurn && !isExternalAgent) {
      setChatErrorState(
        new ApiError(
          "Attachments are not available in Hecate Chat with Tools on.",
          400,
          "chat.attachments_not_supported",
        ),
      );
      return;
    }
    if (
      attachmentDrafts.length > 0 &&
      activeChatSessionID &&
      (chatLoading || isChatTurnActive() || chatSessionIsBusy(activeChatSession))
    ) {
      setChatErrorState(
        new ApiError(
          "Wait for the current response before sending files.",
          409,
          "chat.attachments_not_queueable",
          { operatorAction: "Send the files after the current response finishes." },
        ),
      );
      return;
    }
    if (
      !queued &&
      activeChatSessionID &&
      (chatLoading || isChatTurnActive() || chatSessionIsBusy(activeChatSession))
    ) {
      claimCurrentSubmit();
      queueChatMessage(content, turnExecutionMode, activeChatSessionID, turnToolsEnabled);
      return;
    }
    const activeTurnSessionID = getActiveChatTurnSessionID();
    if (!queued && !activeChatSessionID && activeTurnSessionID) {
      params.setNoticeMessage(
        "error",
        "Another chat is still working. Your draft is unchanged; wait for it to finish before sending.",
      );
      return;
    }
    if (!isExternalAgent && !turnModel) {
      setChatErrorState(chatModelRequiredError());
      settleQueuedLocalFailure();
      return;
    }
    let composerClaimRevision = submittedMessageSnapshot.revision;
    let newerDraftBeforeClaim = false;
    const claimSubmittedComposer = () => {
      if (queued) return;
      const beforeClaim = getMessageSnapshot();
      if (
        beforeClaim.revision > submittedMessageSnapshot.revision ||
        beforeClaim.content !== composerContentSnapshot
      ) {
        newerDraftBeforeClaim = true;
      }
      setMessage((current) => (current === composerContentSnapshot ? "" : current));
      composerClaimRevision = getMessageSnapshot().revision;
    };
    let submitSessionID = queued?.session_id ?? activeChatSessionID;
    let submitProjectID =
      queued?.project_id ??
      (activeChatSession?.id === submitSessionID
        ? (activeChatSession.project_id ?? "")
        : submitSessionID
          ? ""
          : activeProjectID);
    let implicitCreateIntent: number | null = null;
    if (!(queued?.session_id ?? activeChatSessionID)) {
      implicitCreateIntent = tryBeginChatSessionCreate();
      if (implicitCreateIntent === null) {
        const ownershipMutationInFlight = isChatOwnershipMutationInFlight();
        setChatErrorState(
          new ApiError(
            ownershipMutationInFlight
              ? "Wait for the current chat ownership change to finish before creating this chat."
              : "Wait for the current chat to finish creating before sending this message.",
            409,
            ownershipMutationInFlight
              ? "chat.ownership_mutation_in_flight"
              : "chat.session_create_in_flight",
          ),
        );
        settleQueuedLocalFailure();
        return;
      }
    }
    const turnGeneration = beginChatTurn(
      activeChatSessionID,
      isExternalAgent ? "external_agent" : isDirectModelTurn ? "direct_model" : "hecate_task",
    );
    if (turnGeneration === null) {
      if (implicitCreateIntent !== null) finishChatSessionCreate(implicitCreateIntent);
      settleQueuedLocalFailure();
      return;
    }
    const admittedTurnGeneration = turnGeneration;
    const preAdmissionAbort = new AbortController();
    const preAdmissionCancellation = new Error("The response was stopped before model dispatch.");
    let preAdmissionCancelled = false;
    let preAdmissionCancellationOwner: ChatCancellationOwner | null = null;
    if (
      !registerChatTurnPreAdmissionCancel(turnGeneration, (owner) => {
        preAdmissionCancellationOwner = owner;
        preAdmissionCancelled = true;
        preAdmissionAbort.abort();
      })
    ) {
      if (implicitCreateIntent !== null) finishChatSessionCreate(implicitCreateIntent);
      completeChatTurn(turnGeneration);
      settleQueuedLocalFailure();
      return;
    }
    if (submitSessionID) clearSettledChatStopFenceForNewTurn(submitSessionID);
    claimCurrentSubmit();
    const submitTransitionGeneration = beginActiveChatTransition();
    let submittedRecoveryID =
      !queued && !activeChatSessionID ? (detachedRecovery?.id ?? null) : null;
    if (!queued && !activeChatSessionID) {
      if (submittedRecoveryID === null && composerContentSnapshot.trim()) {
        submittedRecoveryID = saveRecoverableComposerDraft({
          content: composerContentSnapshot,
          scope: detachedSubmitScope,
        });
      } else if (submittedRecoveryID !== null) {
        const recoveryID = submittedRecoveryID;
        setRecoverableComposerDraft((current) =>
          current?.id === recoveryID ? { ...current, content: composerContentSnapshot } : current,
        );
      }
      setActiveRecoverableComposerDraftID(null);
    }
    claimSubmittedComposer();
    let textDraftRecovered = false;
    const recoverRejectedTextDraft = () => {
      if (queued || attachmentDrafts.length > 0 || textDraftRecovered) return;
      textDraftRecovered = true;
      const targetSessionID = uploadSessionID || submitSessionID;
      const latestDraft = getMessageSnapshot();
      const hasNewerDraft = newerDraftBeforeClaim || latestDraft.revision > composerClaimRevision;
      const turnStillCurrent = isCurrentChatTurn(turnGeneration);
      const selectionStillCurrent = isCurrentActiveChatTransition(submitTransitionGeneration);
      if (turnStillCurrent && selectionStillCurrent && !hasNewerDraft) {
        setMessage((current) => (current.trim() ? current : composerContentSnapshot));
        if (targetSessionID) rememberChatComposerDraft(targetSessionID, composerContentSnapshot);
        if (!targetSessionID && submittedRecoveryID !== null) {
          setActiveRecoverableComposerDraftID(submittedRecoveryID);
        }
      } else if (targetSessionID) {
        saveSessionComposerDraft(targetSessionID, composerContentSnapshot);
        if (!selectionStillCurrent) {
          params.setNoticeMessage(
            "error",
            sourceSessionTitle
              ? `A message was not sent in “${sourceSessionTitle}”. It is saved there.`
              : "A message was not sent. It is saved in its original chat.",
          );
        }
      }
      if (targetSessionID) clearRecoverableComposerDraft(submittedRecoveryID);
    };
    let attachmentTurnToken: number | null = null;
    if (attachmentDrafts.length > 0) {
      attachmentTurnToken = beginChatAttachmentTurn(
        activeChatSession?.id === currentActiveChatSessionID() ? currentActiveChatSessionID() : "",
        attachmentDrafts.length,
      );
      if (attachmentTurnToken === null) {
        setChatErrorState(
          new ApiError(
            "Wait for the current attachment response before sending more files.",
            409,
            "chat.attachments_turn_in_flight",
          ),
        );
        if (implicitCreateIntent !== null) finishChatSessionCreate(implicitCreateIntent);
        completeActiveChatTransition(submitTransitionGeneration);
        completeChatTurn(turnGeneration);
        return;
      }
      // File values are consumed with the revision-claimed prompt before the
      // first asynchronous boundary. Later edits belong to the next turn.
      setPendingChatAttachments([]);
    }
    const submitRequestToken = beginChatRequestOperation(submitSessionID);
    const submitRequestIsFresh = () =>
      isCurrentChatRequestOperation(submitRequestToken) &&
      currentChatResetGeneration() === submitResetGeneration &&
      !isChatSessionDeleted(submitSessionID, submitProjectID);
    const setQueuedChatErrorStateIfSelected = (error: unknown, fallback?: string) => {
      if (
        !queued ||
        !submitRequestIsFresh() ||
        currentActiveChatSessionID() !== queued.session_id
      ) {
        return;
      }
      setChatErrorState(error, fallback);
    };
    setChatLoading(true);
    clearChatErrorState();
    setRuntimeHeaders(null);
    let turnWorkspace = queued?.workspace ?? workspaceForActiveTurn();
    const turnSystemPrompt = queued?.system_prompt ?? systemPrompt;
    setStreamingContent(
      isExternalAgent
        ? "Starting external agent..."
        : isDirectModelTurn
          ? "Waiting for model output..."
          : "Starting Hecate Chat tools...",
    );
    let streamAbort: AbortController | null = null;
    let streamPromise: Promise<void> | null = null;
    let latestStreamSession: ChatSessionRecord | null = null;
    let queuedReplayCommittedMessageID = "";
    let queuedReplayTerminalSeen = false;
    let queuedReplayFollowSettled = false;
    let queuedStreamFailureMessage = "";
    let uploadSessionID = "";
    let uploadedAttachments: ChatAttachmentRecord[] = [];
    let attachmentUploadResponseAmbiguous = false;
    let optimisticMessageID = "";
    let messagePostStarted = false;
    let messageRequestSucceeded = false;
    let sourceSessionTitle =
      (activeChatSession?.id === submitSessionID ? activeChatSession.title : "") ||
      chat.state.chatSessions.find((entry) => entry.id === submitSessionID)?.title ||
      "";

    function clearCommittedComposerDraft(sessionID: string) {
      clearRecoverableComposerDraft(submittedRecoveryID);
      consumeSavedComposerDraft(sessionID, composerContentSnapshot);
      setComposerDraftsBySessionID((current) => {
        if (current.get(sessionID) !== composerContentSnapshot) return current;
        const next = new Map(current);
        next.delete(sessionID);
        return next;
      });
    }

    async function followQueuedReplayToTerminal(sessionID: string): Promise<boolean> {
      if (!queued || !streamPromise || !queuedReplayCommittedMessageID) return false;
      const deadline = Date.now() + queuedReplayFollowTimeoutMS;
      const exactTurnIsTerminal = (session: ChatSessionRecord | null) =>
        queuedCommittedTurnIsTerminal(session, queuedReplayCommittedMessageID);
      if (exactTurnIsTerminal(latestStreamSession)) return true;
      let followSettled = false;
      const pollOutcome = (async (): Promise<"terminal" | "stale" | "timeout"> => {
        while (!followSettled && submitRequestIsFresh() && Date.now() < deadline) {
          try {
            const latest = await getChatSession(sessionID);
            if (followSettled) return "stale";
            submitProjectID = latest.data.project_id ?? submitProjectID;
            if (!submitRequestIsFresh()) return "stale";
            if (queuedReplayTerminalSeen || exactTurnIsTerminal(latestStreamSession)) {
              return "terminal";
            }
            latestStreamSession = latest.data;
            applyChatSession(latest.data, {
              kind: "turn",
              turnGeneration: admittedTurnGeneration,
            });
            if (exactTurnIsTerminal(latest.data)) {
              queuedReplayTerminalSeen = true;
              return "terminal";
            }
          } catch (error) {
            if (followSettled || !submitRequestIsFresh()) return "stale";
            queuedStreamFailureMessage =
              error instanceof Error ? error.message : "agent chat replay follow-up failed";
          }
          if (!followSettled) await waitForQueuedReplayPoll();
        }
        return submitRequestIsFresh() ? "timeout" : "stale";
      })();
      const streamOutcome = waitForQueuedReplayStream(
        streamPromise,
        submitRequestIsFresh,
        () => queuedReplayTerminalSeen || exactTurnIsTerminal(latestStreamSession),
        deadline,
      );
      let deadlineTimer = 0;
      const deadlineOutcome = new Promise<"timeout">((resolve) => {
        deadlineTimer = window.setTimeout(
          () => resolve("timeout"),
          Math.max(0, deadline - Date.now()),
        );
      });
      const firstOutcome = await Promise.race([
        streamOutcome.then((outcome) => ({ source: "stream" as const, outcome })),
        pollOutcome.then((outcome) => ({ source: "poll" as const, outcome })),
        deadlineOutcome.then((outcome) => ({ source: "deadline" as const, outcome })),
      ]);
      let terminal = false;
      if (firstOutcome.source === "poll") {
        terminal = firstOutcome.outcome === "terminal";
      } else if (firstOutcome.outcome === "terminal") {
        terminal = true;
      } else if (firstOutcome.outcome === "closed") {
        terminal = (await Promise.race([pollOutcome, deadlineOutcome])) === "terminal";
      }
      followSettled = true;
      queuedReplayFollowSettled = true;
      window.clearTimeout(deadlineTimer);
      if (!submitRequestIsFresh()) return false;
      if (terminal || queuedReplayTerminalSeen || exactTurnIsTerminal(latestStreamSession)) {
        return true;
      }
      setQueuedChatErrorStateIfSelected(
        new Error(
          queuedStreamFailureMessage
            ? `The queued message was accepted, but live follow-up stopped: ${queuedStreamFailureMessage}. Refresh this chat before sending another queued message.`
            : "The queued message was accepted and is still running. Refresh this chat before sending another queued message.",
        ),
      );
      return false;
    }

    try {
      let sessionID = submitSessionID;
      let sessionForSubmit = activeChatSession?.id === sessionID ? activeChatSession : null;
      if (sessionID && !sessionForSubmit) {
        try {
          const payload = await getChatSession(sessionID);
          submitProjectID = payload.data.project_id ?? submitProjectID;
          if (!submitRequestIsFresh()) return;
          if (preAdmissionCancelled) throw preAdmissionCancellation;
          sessionForSubmit = payload.data;
          sourceSessionTitle = payload.data.title || sourceSessionTitle;
          applyChatSession(payload.data, { kind: "turn", turnGeneration });
        } catch (error) {
          if (preAdmissionCancelled) throw preAdmissionCancellation;
          if (!submitRequestIsFresh()) return;
          if (queued) {
            // A queued turn is permanently scoped to its original session.
            // A missing or temporarily unreadable target may be retried, but
            // must never be retargeted by silently creating another chat.
            settleQueuedLocalFailure();
            setQueuedChatErrorStateIfSelected(error, "failed to load the queued chat session");
            return;
          }
          // The server owns chat persistence. If localStorage points at a
          // deleted or unavailable session, start clean instead of making the
          // next prompt fail with a stale 404.
          sessionID = "";
          submitSessionID = "";
          bindChatRequestOperationSession(submitRequestToken, "");
          submitProjectID = activeProjectID;
          setActiveChatSessionID("");
        }
      }
      turnWorkspace = turnWorkspace || sessionForSubmit?.workspace?.trim() || "";
      if (!isDirectModelTurn && !turnWorkspace) {
        if (queued) setQueuedChatErrorStateIfSelected(chatWorkspaceRequiredError());
        else setChatErrorState(chatWorkspaceRequiredError());
        settleQueuedLocalFailure();
        return;
      }
      if (sessionID && sessionForSubmit?.agent_id) {
        const activeExternal = sessionForSubmit.agent_id !== "hecate";
        if (activeExternal !== isExternalAgent) {
          if (queued) {
            settleQueuedLocalFailure();
            setQueuedChatErrorStateIfSelected(
              new Error(
                "This queued message is permanently scoped to a chat with a different runtime owner. It was not sent or retargeted; remove it and submit a new message in the intended chat if it is still needed.",
              ),
            );
            return;
          }
          sessionID = "";
          submitSessionID = "";
          bindChatRequestOperationSession(submitRequestToken, "");
          submitProjectID = activeProjectID;
          sessionForSubmit = null;
          setActiveChatSessionID("");
          setActiveChatSession(null);
        }
      }
      if (!sessionID) {
        if (
          currentChatResetGeneration() !== submitResetGeneration ||
          (activeProjectID !== "" && isChatSessionDeleted("", activeProjectID))
        ) {
          settleQueuedLocalFailure();
          return;
        }
        if (implicitCreateIntent === null) {
          implicitCreateIntent = tryBeginChatSessionCreate();
          if (implicitCreateIntent === null) {
            const ownershipMutationInFlight = isChatOwnershipMutationInFlight();
            setChatErrorState(
              new ApiError(
                ownershipMutationInFlight
                  ? "Wait for the current chat ownership change to finish before creating this chat."
                  : "Wait for the current chat to finish creating before sending this message.",
                409,
                ownershipMutationInFlight
                  ? "chat.ownership_mutation_in_flight"
                  : "chat.session_create_in_flight",
              ),
            );
            settleQueuedLocalFailure();
            return;
          }
        }
        const configOptions = isExternalAgent ? configOptionsForExternalAgent(turnAgentID) : [];
        const mcpServers = isExternalAgent ? mcpServersForExternalAgent() : [];
        try {
          const created = await createChatSessionRequest(
            {
              title: deriveChatSessionTitle(content || attachmentDrafts[0]?.file.name || "File"),
              ...(activeProjectID ? { project_id: activeProjectID } : {}),
              agent_id: isExternalAgent ? turnAgentID : "hecate",
              ...(!isExternalAgent
                ? {
                    provider: turnProviderFilter === "auto" ? "" : turnProviderFilter,
                    model: turnModel,
                    workspace_mode: workspaceModeForNewChat(activeProjectID),
                  }
                : {}),
              ...(!isDirectModelTurn ? { workspace: turnWorkspace } : {}),
              ...(!isExternalAgent && turnToolsEnabled ? { rtk_enabled: hecateRTKEnabled } : {}),
              ...(isExternalAgent && configOptions.length > 0
                ? { config_options: configOptions }
                : {}),
              ...(isExternalAgent && mcpServers.length > 0 ? { mcp_servers: mcpServers } : {}),
            },
            preAdmissionAbort.signal,
          );
          if (discardDeletedCreatedSession(created.data, submitResetGeneration, activeProjectID)) {
            return;
          }
          sessionID = created.data.id;
          submitSessionID = created.data.id;
          bindChatRequestOperationSession(submitRequestToken, sessionID);
          submitProjectID = created.data.project_id ?? activeProjectID;
          bindChatTurnSession(turnGeneration, sessionID);
          if (
            isCurrentChatTurn(turnGeneration) &&
            isCurrentActiveChatTransition(submitTransitionGeneration)
          ) {
            setActiveChatSessionID(sessionID);
            applyChatSession(created.data, { kind: "turn", turnGeneration });
          } else {
            recordChatSessionSummary(created.data);
          }
          // An adapter or test double may ignore AbortSignal. Preserve the
          // acknowledged session shell, but fence every upload/message/runtime
          // dispatch from this cancelled turn.
          if (preAdmissionCancelled) throw preAdmissionCancellation;
        } finally {
          if (implicitCreateIntent !== null) {
            finishChatSessionCreate(implicitCreateIntent);
            implicitCreateIntent = null;
          }
        }
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

      uploadSessionID = sessionID;
      submitSessionID = sessionID;
      bindChatRequestOperationSession(submitRequestToken, sessionID);
      bindChatTurnSession(turnGeneration, sessionID);
      if (preAdmissionCancelled) throw preAdmissionCancellation;
      if (attachmentTurnToken !== null) {
        if (!bindChatAttachmentTurn(attachmentTurnToken, sessionID)) {
          throw new ApiError(
            "Wait for the current attachment response before sending more files.",
            409,
            "chat.attachments_turn_in_flight",
          );
        }
      }
      for (const attachment of attachmentDrafts) {
        try {
          uploadedAttachments.push(
            await uploadChatAttachmentRequest(sessionID, attachment.file, preAdmissionAbort.signal),
          );
          if (preAdmissionCancelled) throw preAdmissionCancellation;
        } catch (error) {
          if (error !== preAdmissionCancellation) {
            attachmentUploadResponseAmbiguous = attachmentUploadResponseIsAmbiguous(error);
          }
          throw preAdmissionCancelled ? preAdmissionCancellation : error;
        }
      }
      if (!submitRequestIsFresh()) return;
      if (preAdmissionCancelled) throw preAdmissionCancellation;

      const pendingContent = content;
      optimisticMessageID = `pending-agent-user-${Date.now()}`;
      setActiveChatSession((prev) =>
        prev
          ? {
              ...prev,
              messages: [
                ...(prev.messages ?? []),
                {
                  id: optimisticMessageID,
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
                  attachments: uploadedAttachments,
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
          if (!submitRequestIsFresh() || queuedReplayFollowSettled) return;
          switch (event.type) {
            case "session_update": {
              submitProjectID = event.payload.data.project_id ?? submitProjectID;
              if (!submitRequestIsFresh()) return;
              if (messagePostStarted && chatSessionIsBusy(event.payload.data)) {
                confirmChatTurnServerCancellation(turnGeneration);
              }
              latestStreamSession = event.payload.data;
              if (
                queuedReplayCommittedMessageID &&
                queuedCommittedTurnIsTerminal(event.payload.data, queuedReplayCommittedMessageID)
              ) {
                queuedReplayTerminalSeen = true;
              }
              if (!applyChatSession(event.payload.data, { kind: "turn", turnGeneration })) return;
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
              if (chatStopFenceSuppressesApproval(event.payload.session_id, turnGeneration)) {
                return;
              }
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
        if (!submitRequestIsFresh()) return;
        if (currentActiveChatSessionID() !== sessionID) return;
        const msg = streamError instanceof Error ? streamError.message : "agent chat stream failed";
        if (queued) {
          queuedStreamFailureMessage = msg;
          return;
        }
        setChatError((current) => current || msg);
      });
      if (!startChatTurnAdmission(turnGeneration)) {
        if (preAdmissionCancelled) throw preAdmissionCancellation;
        return;
      }
      messagePostStarted = true;
      const updated = await createChatMessageRequest(sessionID, {
        content: pendingContent,
        ...(queued ? { client_request_id: queued.id } : {}),
        ...(uploadedAttachments.length > 0
          ? { attachment_ids: uploadedAttachments.map((attachment) => attachment.id) }
          : {}),
        ...(isExternalAgent
          ? { execution_mode: turnExecutionMode }
          : { tools_enabled: turnToolsEnabled }),
        ...(!isExternalAgent
          ? { provider: turnProviderFilter === "auto" ? "" : turnProviderFilter, model: turnModel }
          : {}),
        ...(!isExternalAgent ? { system_prompt: turnSystemPrompt } : {}),
        ...(!isExternalAgent && turnToolsEnabled ? { workspace: turnWorkspace } : {}),
      });
      messageRequestSucceeded = true;
      if (chatSessionIsBusy(updated.data)) {
        confirmChatTurnServerCancellation(turnGeneration);
      }
      submitProjectID = updated.data.project_id ?? submitProjectID;
      if (!submitRequestIsFresh()) return;
      applyChatSession(updated.data, { kind: "turn", turnGeneration });
      const protectedStopFence = chatStopFenceForTurnSettlement(sessionID, turnGeneration);
      if (protectedStopFence && chatSessionIsBusy(updated.data)) {
        await waitForProtectedChatStopFenceSettlement(
          sessionID,
          turnGeneration,
          protectedStopFence,
        );
        if (!submitRequestIsFresh()) return;
      }
      if (queued) {
        const requestMetadata = updated.message_request;
        if (requestMetadata) {
          queuedReplayCommittedMessageID = requestMetadata.committed_message_id.trim();
          const committedMessagePresent = (updated.data.messages ?? []).some(
            (message) => message.id === queuedReplayCommittedMessageID,
          );
          if (!queuedReplayCommittedMessageID || !committedMessagePresent) {
            setQueuedSnapshotDeliveryState(queued, "reconcile_required");
            setQueuedChatErrorStateIfSelected(
              new Error(
                "The server accepted this queued request but did not return its committed message. Keep it paused and refresh the authoritative chat before removing it.",
              ),
            );
            return;
          }
          if (queuedCommittedTurnIsTerminal(updated.data, queuedReplayCommittedMessageID)) {
            queuedReplayTerminalSeen = true;
          }
          if (!queuedReplayTerminalSeen) {
            const terminal = await followQueuedReplayToTerminal(sessionID);
            if (!submitRequestIsFresh()) return;
            if (!terminal) {
              setQueuedSnapshotDeliveryState(queued, "reconcile_required");
              return;
            }
          }
          removeDeliveredQueuedSnapshot(queued);
        } else {
          setQueuedSnapshotDeliveryState(queued, "reconcile_required");
          setQueuedChatErrorStateIfSelected(
            new Error(
              "The server accepted this queued request without exact committed-message metadata. Keep it paused, refresh the authoritative chat, and upgrade Hecate before checking or sending later queued work.",
            ),
          );
          return;
        }
      } else {
        clearCommittedComposerDraft(sessionID);
      }
      uploadedAttachments = [];
    } catch (submitError) {
      if (preAdmissionCancelled) {
        if (!submitRequestIsFresh()) return;
        if (queued) {
          setQueuedSnapshotDeliveryState(queued, "retryable");
          return;
        }
        const attachmentSessionStillActive =
          !uploadSessionID || currentActiveChatSessionID() === uploadSessionID;
        let failedAttachmentCleanupCount = 0;
        if (uploadSessionID && uploadedAttachments.length > 0) {
          failedAttachmentCleanupCount = await deleteUploadedAttachmentDrafts(
            uploadSessionID,
            uploadedAttachments,
          );
          if (!submitRequestIsFresh()) return;
        }
        if (attachmentDrafts.length > 0 && attachmentSessionStillActive) {
          setPendingChatAttachments((current) => [
            ...attachmentDrafts,
            ...current.filter(
              (candidate) => !attachmentDrafts.some((draft) => draft.id === candidate.id),
            ),
          ]);
          setMessage((newerComposerText) =>
            restoreComposerText(composerContentSnapshot, newerComposerText),
          );
        } else {
          recoverRejectedTextDraft();
        }
        if (optimisticMessageID) {
          setActiveChatSession((current) =>
            current
              ? {
                  ...current,
                  messages: (current.messages ?? []).filter(
                    (message) => message.id !== optimisticMessageID,
                  ),
                }
              : current,
          );
        }
        if (attachmentUploadResponseAmbiguous && attachmentSessionStillActive) {
          setChatErrorState(
            ambiguousAttachmentUploadError(submitError, failedAttachmentCleanupCount),
          );
        } else if (failedAttachmentCleanupCount > 0 && attachmentSessionStillActive) {
          setChatErrorState(attachmentDraftCleanupError(submitError, failedAttachmentCleanupCount));
        }
        return;
      }
      const clientRequestConflict =
        submitError instanceof ApiError && submitError.code === "chat.client_request_conflict";
      const knownPrecommitError =
        submitError instanceof ApiError &&
        !clientRequestConflict &&
        [400, 401, 403, 404, 409, 413, 422, 429].includes(submitError.status);
      if (!submitRequestIsFresh()) return;

      if (queued) {
        if (clientRequestConflict) {
          if (optimisticMessageID && currentActiveChatSessionID() === queued.session_id) {
            setActiveChatSession((current) =>
              current
                ? {
                    ...current,
                    messages: (current.messages ?? []).filter(
                      (candidate) => candidate.id !== optimisticMessageID,
                    ),
                  }
                : current,
            );
          }
          setQueuedSnapshotDeliveryState(
            queued,
            "reconcile_required",
            "chat.client_request_conflict",
          );
          setQueuedChatErrorStateIfSelected(
            new Error(
              "This queued request id is already committed to a different payload. Review the authoritative transcript, then remove this item and submit its text as a new message if it is still needed.",
            ),
          );
          return;
        }
        if (
          !queued.delivery_idempotency_keyed &&
          messagePostStarted &&
          uploadSessionID &&
          !knownPrecommitError
        ) {
          try {
            const reconciled = await getChatSession(uploadSessionID);
            submitProjectID = reconciled.data.project_id ?? submitProjectID;
            if (!submitRequestIsFresh()) return;
            applyChatSession(reconciled.data, { kind: "turn", turnGeneration });
            const messages = reconciled.data.messages ?? [];
            const committedMessageIndex = queuedCommitIndex(messages, queued);
            if (committedMessageIndex >= 0) {
              removeDeliveredQueuedSnapshot(queued);
              const responseConfirmed = messages
                .slice(committedMessageIndex + 1)
                .some(
                  (candidate) =>
                    candidate.role === "assistant" &&
                    ["completed", "failed", "cancelled"].includes(candidate.status ?? ""),
                );
              if (!responseConfirmed) {
                setQueuedChatErrorStateIfSelected(
                  new Error(
                    "The queued message was accepted, but its model response could not be confirmed. Do not send it again. Refresh this chat to check the model run.",
                  ),
                );
              }
              return;
            }
          } catch {
            // The queued POST outcome remains ambiguous. Keep its persisted
            // snapshot so the operator can inspect or edit it, while the
            // queue drain's attempt fence prevents an automatic duplicate.
          }
        }
        if (!submitRequestIsFresh()) return;
        if (optimisticMessageID && currentActiveChatSessionID() === queued.session_id) {
          setActiveChatSession((current) =>
            current
              ? {
                  ...current,
                  messages: (current.messages ?? []).filter(
                    (candidate) => candidate.id !== optimisticMessageID,
                  ),
                }
              : current,
          );
        }
        if (!messagePostStarted || knownPrecommitError) {
          setQueuedSnapshotDeliveryState(queued, "retryable");
          setQueuedChatErrorStateIfSelected(submitError);
        } else {
          setQueuedSnapshotDeliveryState(queued, "reconcile_required");
          setQueuedChatErrorStateIfSelected(
            new Error(
              "The queued message submission could not be confirmed. It remains queued and will not be retried automatically. Check its delivery status before taking another action.",
            ),
          );
        }
        return;
      }

      let reconciliationProvedNoCommit = false;
      if (messagePostStarted && uploadSessionID && uploadedAttachments.length > 0) {
        try {
          const reconciled = await getChatSession(uploadSessionID);
          submitProjectID = reconciled.data.project_id ?? submitProjectID;
          if (!submitRequestIsFresh()) return;
          applyChatSession(reconciled.data, { kind: "turn", turnGeneration });
          const uploadedIDs = new Set(uploadedAttachments.map((attachment) => attachment.id));
          const messages = reconciled.data.messages ?? [];
          const committedMessageIndex = messages.findIndex((candidate) => {
            if (candidate.role !== "user") return false;
            const candidateIDs = new Set(
              (candidate.attachments ?? []).map((attachment) => attachment.id),
            );
            return [...uploadedIDs].every((id) => candidateIDs.has(id));
          });
          if (committedMessageIndex >= 0) {
            uploadedAttachments = [];
            clearCommittedComposerDraft(uploadSessionID);
            const responseConfirmed = messages
              .slice(committedMessageIndex + 1)
              .some(
                (candidate) =>
                  candidate.role === "assistant" &&
                  ["completed", "failed", "cancelled"].includes(candidate.status ?? ""),
              );
            if (!responseConfirmed) {
              setChatErrorState(
                new Error(
                  "The message was accepted, but its model response could not be confirmed. Do not send it again. Refresh this chat to check the model run.",
                ),
              );
            }
            return;
          }
          reconciliationProvedNoCommit = true;
        } catch {
          // The POST outcome remains ambiguous. The safe path below retains
          // server drafts and does not reconstruct a potentially duplicate
          // retry from the in-memory File objects.
        }
      }
      if (!submitRequestIsFresh()) return;

      const hecateServerErrorProvedNoCommit =
        attachmentDrafts.length > 0 &&
        reconciliationProvedNoCommit &&
        submitError instanceof ApiError &&
        submitError.status >= 500 &&
        submitError.status < 600 &&
        definiteHecateServerRejectionCodes.has(submitError.code.trim());
      const definitelyRejected =
        attachmentDrafts.length === 0 ||
        (!messagePostStarted && !attachmentUploadResponseAmbiguous) ||
        knownPrecommitError ||
        hecateServerErrorProvedNoCommit;
      const attachmentSessionStillActive =
        !uploadSessionID || currentActiveChatSessionID() === uploadSessionID;
      let failedAttachmentCleanupCount = 0;
      if (
        (definitelyRejected || attachmentUploadResponseAmbiguous) &&
        uploadSessionID &&
        uploadedAttachments.length > 0
      ) {
        failedAttachmentCleanupCount = await deleteUploadedAttachmentDrafts(
          uploadSessionID,
          uploadedAttachments,
        );
        if (!submitRequestIsFresh()) return;
      }
      if (definitelyRejected && !messageRequestSucceeded && attachmentDrafts.length === 0) {
        if (optimisticMessageID) {
          setActiveChatSession((current) =>
            current
              ? {
                  ...current,
                  messages: (current.messages ?? []).filter(
                    (message) => message.id !== optimisticMessageID,
                  ),
                }
              : current,
          );
        }
        recoverRejectedTextDraft();
      }
      if (definitelyRejected && attachmentDrafts.length > 0 && attachmentSessionStillActive) {
        setPendingChatAttachments((current) => [
          ...attachmentDrafts,
          ...current.filter(
            (candidate) => !attachmentDrafts.some((draft) => draft.id === candidate.id),
          ),
        ]);
        setMessage((newerComposerText) =>
          restoreComposerText(composerContentSnapshot, newerComposerText),
        );
        if (optimisticMessageID) {
          setActiveChatSession((current) =>
            current
              ? {
                  ...current,
                  messages: (current.messages ?? []).filter(
                    (message) => message.id !== optimisticMessageID,
                  ),
                }
              : current,
          );
        }
      }
      if (attachmentUploadResponseAmbiguous && attachmentSessionStillActive) {
        setPendingChatAttachments((current) => [
          ...attachmentDrafts,
          ...current.filter(
            (candidate) => !attachmentDrafts.some((draft) => draft.id === candidate.id),
          ),
        ]);
        setMessage((newerComposerText) =>
          restoreComposerText(composerContentSnapshot, newerComposerText),
        );
        setChatErrorState(
          ambiguousAttachmentUploadError(submitError, failedAttachmentCleanupCount),
        );
      } else if (definitelyRejected && attachmentSessionStillActive) {
        setChatErrorState(
          failedAttachmentCleanupCount > 0
            ? attachmentDraftCleanupError(submitError, failedAttachmentCleanupCount)
            : submitError,
        );
      } else if (!definitelyRejected && attachmentSessionStillActive) {
        if (optimisticMessageID) {
          setActiveChatSession((current) =>
            current
              ? {
                  ...current,
                  messages: (current.messages ?? []).filter(
                    (message) => message.id !== optimisticMessageID,
                  ),
                }
              : current,
          );
        }
        setChatErrorState(
          new Error(
            "The message submission could not be confirmed. Refresh this chat before sending again to avoid a duplicate model run.",
          ),
        );
      }
    } finally {
      streamAbort?.abort();
      await streamPromise?.catch(() => undefined);
      if (!queued && !messagePostStarted && !messageRequestSucceeded && submitRequestIsFresh()) {
        recoverRejectedTextDraft();
      }
      if (finishChatRequestOperation(submitRequestToken)) {
        if (!uploadSessionID || currentActiveChatSessionID() === uploadSessionID) {
          setStreamingContent(null);
        }
        // chatLoading is a global request gate. Only the latest request may
        // release it; a response fenced by reset/project deletion must not
        // clear a newer turn's loading state.
        setChatLoading(false);
      }
      if (implicitCreateIntent !== null) finishChatSessionCreate(implicitCreateIntent);
      if (attachmentTurnToken !== null) finishChatAttachmentTurn(attachmentTurnToken);
      completeActiveChatTransition(submitTransitionGeneration);
      completeChatTurn(turnGeneration);
      if (preAdmissionCancellationOwner) finishChatCancellation(preAdmissionCancellationOwner);
    }
  }

  async function cancelAgentChat() {
    if (blockWhileChatOwnershipMutationRuns("stopping this chat")) return;
    const cancelSessionID = currentActiveChatSessionID();
    const activeTurnSessionID = getActiveChatTurnSessionID();
    const detachedActiveTurn = isChatTurnActive() && !cancelSessionID && !activeTurnSessionID;
    if (!cancelSessionID && !detachedActiveTurn) return;
    const cancelProjectID =
      activeChatSession?.id === cancelSessionID ? (activeChatSession.project_id ?? "") : "";
    if (cancelSessionID && isChatSessionDeleted(cancelSessionID, cancelProjectID)) return;
    const cancellationOwner = beginChatCancellation(cancelSessionID);
    if (!cancellationOwner) return;
    if (cancelChatTurnBeforeAdmission(cancellationOwner)) return;
    if (!chatTurnServerCancellationReady(cancellationOwner)) {
      finishChatCancellation(cancellationOwner);
      return;
    }
    const stopFence = beginChatStopFence(cancellationOwner);
    const cancellationEpoch = currentChatCancellationEpoch(cancelSessionID);
    const cancelResetGeneration = currentChatResetGeneration();
    const cancellationIsFresh = () =>
      currentChatResetGeneration() === cancelResetGeneration &&
      !isChatSessionDeleted(cancelSessionID, cancelProjectID);
    let failedStopNeedsCatchUp = false;
    try {
      await cancelChatSessionRequest(cancellationOwner.sessionID);
      if (!cancellationIsFresh()) {
        clearChatStopFence(stopFence);
        return;
      }
      if (!acceptChatStopFence(stopFence)) {
        const restoredFence = clearChatStopFence(stopFence, true);
        if (restoredFence?.phase === "accepted") void pollAcceptedChatStopFence(restoredFence);
        return;
      }
      // A 202 response only acknowledges that cancellation was signalled. Its
      // session snapshot may predate the signal, so keep the exact turn fenced
      // until its stream/POST or a post-acceptance GET proves terminal state.
      invalidatePendingApprovals(cancelSessionID);
      void pollAcceptedChatStopFence(stopFence);
      await waitForChatStopFenceSettlement(stopFence);
    } catch (error) {
      const failureIsFresh = cancellationIsFresh();
      const restoredFence = clearChatStopFence(stopFence, failureIsFresh);
      if (!failureIsFresh) return;
      if (restoredFence?.phase === "accepted") void pollAcceptedChatStopFence(restoredFence);
      failedStopNeedsCatchUp = restoredFence === null;
      if (currentActiveChatSessionID() !== cancelSessionID) return;
      setChatErrorState(error, "failed to cancel agent chat");
    } finally {
      const released = finishChatCancellation(cancellationOwner);
      if (released && failedStopNeedsCatchUp) {
        // The request failed before an acknowledgement, so independently
        // reconcile both projections after releasing ownership. Either read
        // may hang; neither is allowed to retain Stop or overwrite a newer
        // cancellation epoch.
        catchUpAfterFailedChatStop(cancelSessionID, cancellationEpoch);
      }
    }
  }

  async function compactChatSession(sessionID = activeChatSessionID): Promise<boolean> {
    if (blockWhileChatOwnershipMutationRuns("compacting this chat")) return false;
    if (hasChatAttachmentTurn()) {
      params.setNoticeMessage(
        "error",
        "Wait for the attachment response before compacting this chat.",
      );
      return false;
    }
    const compactSessionID = sessionID.trim();
    if (!compactSessionID) {
      params.setNoticeMessage("error", "Open a Hecate chat before using /compact.");
      return false;
    }
    if (blockWhileChatCancellationOwnsSession(compactSessionID, "compacting this chat")) {
      return false;
    }
    if (chatLoading || isChatTurnActive() || chatSessionIsBusy(activeChatSession)) {
      params.setNoticeMessage(
        "error",
        "Wait for the current response before compacting this chat.",
      );
      return false;
    }
    const compactProjectID =
      activeChatSession?.id === compactSessionID
        ? (activeChatSession.project_id ?? "")
        : (chat.state.chatSessions.find((session) => session.id === compactSessionID)?.project_id ??
          "");
    if (isChatSessionDeleted(compactSessionID, compactProjectID)) return false;
    const compactResetGeneration = currentChatResetGeneration();
    const cancellationEpoch = currentChatCancellationEpoch(compactSessionID);
    const stopRequestToken = stopReadTokenAtRequestStart(compactSessionID);
    const compactSessionIntent = currentChatSessionIntent();
    const compactRequestToken = beginChatRequestOperation(compactSessionID);
    const compactRequestIsFresh = () =>
      isCurrentChatRequestOperation(compactRequestToken) &&
      currentChatResetGeneration() === compactResetGeneration &&
      isCurrentChatSessionIntent(compactSessionIntent) &&
      !isChatOwnershipMutationInFlight() &&
      !isChatSessionDeleted(compactSessionID, compactProjectID);
    const compactSourceIsCurrent = () =>
      compactRequestIsFresh() && currentActiveChatSessionID() === compactSessionID;
    setChatLoading(true);
    clearChatErrorState();
    try {
      const payload = await compactChatSessionRequest(compactSessionID);
      const latest = await latestSessionAfterCancellation(
        compactSessionID,
        cancellationEpoch,
        payload.data,
        snapshotSourceForStopRead(stopRequestToken),
      );
      const { session } = latest;
      if (!compactSourceIsCurrent()) return false;
      applyChatSession(session, latest.source);
      const count = session.context_summary?.message_count ?? 0;
      params.setNoticeMessage(
        "success",
        count > 0 ? `Compacted ${count} transcript messages.` : "Compacted chat context.",
      );
      return true;
    } catch (error) {
      if (!compactSourceIsCurrent()) return false;
      setChatErrorState(error, "failed to compact chat context");
      return false;
    } finally {
      if (finishChatRequestOperation(compactRequestToken)) setChatLoading(false);
    }
  }

  function updateToolResult(index: number, result: string) {
    setPendingToolCalls((prev) => prev.map((tc, i) => (i === index ? { ...tc, result } : tc)));
  }

  async function submitToolResults() {
    if (!pendingThread || pendingToolCalls.length === 0) return;
    if (blockWhileChatOwnershipMutationRuns("submitting tool results")) return;
    const continuationSessionID = currentActiveChatSessionID();
    const continuationProjectID =
      activeChatSession?.id === continuationSessionID ? (activeChatSession.project_id ?? "") : "";
    const continuationResetGeneration = currentChatResetGeneration();
    const continuationIsCurrent = () =>
      currentChatResetGeneration() === continuationResetGeneration &&
      !isChatSessionDeleted(continuationSessionID, continuationProjectID);
    setChatLoading(true);
    clearChatErrorState();

    const toolMessages: ChatMessage[] = pendingToolCalls.map((tc) => ({
      role: "tool" as const,
      content: tc.result,
      tool_call_id: tc.id,
    }));

    const messages: ChatMessage[] = [...pendingThread, ...toolMessages];

    try {
      const chatExecution = await executeChatRequest(
        buildChatPayload(messages),
        messages,
        continuationIsCurrent,
      );
      if (!continuationIsCurrent() || chatExecution.kind !== "completed") {
        return;
      }

      clearPendingToolState();
      setChatResult(chatExecution.chatResult);
      setStreamingContent(null);
      await refreshRuntimeState(continuationIsCurrent);
    } catch (err) {
      if (!continuationIsCurrent()) return;
      setChatErrorState(err, "unknown error");
    } finally {
      if (continuationIsCurrent()) setChatLoading(false);
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
    isCurrent: () => boolean,
  ): Promise<
    | { kind: "discarded" }
    | { kind: "tool_calls" }
    | { kind: "completed"; headers: RuntimeHeaders; chatResult: ChatResponse }
  > {
    let fullContent = "";
    if (!isCurrent()) return { kind: "discarded" };
    setStreamingContent("");
    const response = await chatCompletionsStream(chatPayload, (delta) => {
      if (!isCurrent()) return;
      fullContent += delta;
      setStreamingContent(fullContent);
    });
    if (!isCurrent()) return { kind: "discarded" };
    setRuntimeHeaders(response.headers);

    if (response.finishReason === "tool_calls" && response.toolCalls.length > 0) {
      if (!isCurrent()) return { kind: "discarded" };
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
    if (blockWhileChatOwnershipMutationRuns("starting a new chat")) return;
    if (hasPendingChatAttachments()) {
      params.setNoticeMessage("error", "Remove attached files before starting a new chat.");
      return;
    }
    if (hasChatAttachmentTurn()) {
      params.setNoticeMessage(
        "error",
        "Wait for the attachment response before starting a new chat.",
      );
      return;
    }
    const createIntent = tryBeginChatSessionCreate();
    if (createIntent === null) {
      params.setNoticeMessage("error", "Wait for the new chat to finish creating.");
      return;
    }
    const createResetGeneration = currentChatResetGeneration();
    const sourceSessionID = activeChatSessionID;
    const sourceSession = activeChatSession?.id === sourceSessionID ? activeChatSession : null;
    const liveSourceComposerSnapshot = getMessageSnapshot();
    const sourceComposerSnapshot =
      sourceSessionID !== currentActiveChatSessionID()
        ? { ...liveSourceComposerSnapshot, content: message }
        : liveSourceComposerSnapshot;
    const sourceWorkspace = agentWorkspace;
    const sourceWorkspaceBranch = agentWorkspaceBranch;
    const requestedAgentID = options?.agentID?.trim();
    const requestedTitle = options?.title?.trim() || "";
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
    const createAgentID = createExternalAgent ? requestedAgentID || agentAdapterID : "hecate";
    const createWorkspace = workspaceForNewChat(createProjectID);
    const createDraftScope = composerDraftScope({
      projectID: createProjectID,
      agentID: createAgentID,
      provider: requestedProviderFilter,
      model: requestedSelectionModel,
      workspace: createWorkspace,
    });
    const draftWasProvided = Boolean(
      options && Object.prototype.hasOwnProperty.call(options, "draft"),
    );
    const matchingRecovery =
      recoverableComposerDraft &&
      composerDraftScopesMatch(recoverableComposerDraft.scope, createDraftScope)
        ? recoverableComposerDraft
        : null;
    const boundRecovery =
      !sourceSessionID && matchingRecovery?.id === activeRecoverableComposerDraftID
        ? matchingRecovery
        : null;
    const detachedDraftBelongsElsewhere =
      !sourceSessionID &&
      activeRecoverableComposerDraftID !== null &&
      recoverableComposerDraft?.id === activeRecoverableComposerDraftID &&
      !matchingRecovery;
    const ambientDraft = detachedDraftBelongsElsewhere ? "" : sourceComposerSnapshot.content;
    const requestedDraft = draftWasProvided
      ? (options?.draft ?? "")
      : boundRecovery
        ? ambientDraft
        : ambientDraft || matchingRecovery?.content || "";
    const requestedReuseEmptyDraft = Boolean(options?.reuseEmptyDraft && requestedDraft.trim());
    const transitionGeneration = beginActiveChatTransition();
    rememberChatComposerDraft(sourceSessionID, sourceComposerSnapshot.content);
    let recoveryID =
      boundRecovery?.id ?? (!ambientDraft ? matchingRecovery?.id : undefined) ?? null;
    if (requestedDraft.trim()) {
      recoveryID = saveRecoverableComposerDraft({
        ...(recoveryID === null ? {} : { id: recoveryID }),
        content: requestedDraft,
        scope: createDraftScope,
      });
      setActiveRecoverableComposerDraftID(recoveryID);
    } else {
      clearRecoverableComposerDraft(boundRecovery?.id ?? null);
      recoveryID = null;
      setActiveRecoverableComposerDraftID(null);
    }
    pendingCreateDraftRef.current = {
      generation: transitionGeneration,
      draft: requestedDraft,
      scope: createDraftScope,
      recoveryID,
    };
    setMessage(requestedDraft);
    const createTransferSnapshot = getMessageSnapshot();
    setActiveChatSessionID("");
    setActiveChatSession(null);
    setAgentWorkspaceBranch("");
    const canActivateCreatedSession = () => {
      if (!isCurrentActiveChatTransition(transitionGeneration)) return false;
      if (!hasPendingChatAttachments()) return true;
      const liveComposerSnapshot = getMessageSnapshot();
      if (pendingCreateDraftRef.current?.generation === transitionGeneration) {
        // Text entered after the create request began belongs to the created
        // shell. Capture it before restoring the File-owning source composer.
        pendingCreateDraftRef.current.draft =
          liveComposerSnapshot.revision === createTransferSnapshot.revision
            ? createTransferSnapshot.content
            : liveComposerSnapshot.content;
      }
      // The picker raced creation after the old chat had been visually
      // detached. Files never transfer to the newly allocated session: put
      // their original owner back and leave the created shell discoverable.
      setActiveChatSessionID(sourceSessionID);
      setActiveChatSession(sourceSession);
      setAgentWorkspace(sourceWorkspace);
      setAgentWorkspaceBranch(sourceWorkspaceBranch);
      setMessage(sourceComposerSnapshot.content);
      rememberChatComposerDraft(sourceSessionID, sourceComposerSnapshot.content);
      params.setNoticeMessage(
        "error",
        "The new chat was created but not opened because attached files belong to the current chat.",
      );
      return false;
    };
    try {
      if (createExternalAgent) {
        const externalAgentID = createAgentID;
        const workspace = createWorkspace;
        if (!workspace) {
          setChatErrorState(chatWorkspaceRequiredError());
          setActiveChatSessionID("");
          setActiveChatSession(null);
          return;
        }
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
          if (discardDeletedCreatedSession(created.data, createResetGeneration, createProjectID)) {
            return;
          }
          const createWon = canActivateCreatedSession();
          const createdDraft = pendingCreateDraft(transitionGeneration, requestedDraft);
          setComposerDraftsBySessionID((current) => {
            const next = new Map(current);
            next.set(created.data.id, createdDraft);
            if (!draftWasProvided && createWon && sourceSessionID !== created.data.id) {
              next.delete(sourceSessionID);
            }
            return next;
          });
          clearRecoverableComposerDraft(pendingCreateRecoveryID(transitionGeneration, recoveryID));
          if (!createWon) {
            recordChatSessionSummary(created.data);
            return;
          }
          setActiveChatSessionID(created.data.id);
          applyChatSession(created.data);
        } catch (error) {
          if (!isCurrentActiveChatTransition(transitionGeneration)) return;
          preservePendingCreateDraft(transitionGeneration, true);
          setChatErrorState(error, "failed to create external agent chat");
          params.setNoticeMessage(
            "error",
            error instanceof Error ? error.message : "Failed to create external agent chat.",
          );
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
        configuredProviders,
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
      const workspaceMode = workspaceModeForNewChat(createProjectID);
      const createProvider = requestedProviderFilter === "auto" ? "" : requestedProviderFilter;
      if (requestedReuseEmptyDraft) {
        const reusable = findReusableEmptyDraftSession(chat.state.chatSessions, {
          agentID: "hecate",
          projectID: createProjectID,
          provider: createProvider,
          model: requestedModel,
          title: requestedTitle,
          workspaceMode,
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
          finishChatSessionCreate(createIntent);
          const selected = await selectChatSession(reusable.id, { draft: requestedDraft });
          if (selected) {
            clearRecoverableComposerDraft(
              pendingCreateRecoveryID(transitionGeneration, recoveryID),
            );
          }
          return;
        }
      }
      clearChatErrorState();
      try {
        const created = await createChatSessionRequest({
          ...(requestedTitle ? { title: requestedTitle } : {}),
          ...(createProjectID ? { project_id: createProjectID } : {}),
          agent_id: "hecate",
          provider: createProvider,
          model: requestedModel,
          workspace_mode: workspaceMode,
          ...(toolsEnabled && workspace ? { workspace } : {}),
          ...(toolsEnabled ? { rtk_enabled: hecateRTKEnabled } : {}),
        });
        if (discardDeletedCreatedSession(created.data, createResetGeneration, createProjectID)) {
          return;
        }
        const createWon = canActivateCreatedSession();
        const createdDraft = pendingCreateDraft(transitionGeneration, requestedDraft);
        setComposerDraftsBySessionID((current) => {
          const next = new Map(current);
          next.set(created.data.id, createdDraft);
          if (!draftWasProvided && createWon && sourceSessionID !== created.data.id) {
            next.delete(sourceSessionID);
          }
          return next;
        });
        clearRecoverableComposerDraft(pendingCreateRecoveryID(transitionGeneration, recoveryID));
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
        if (!createWon) {
          recordChatSessionSummary(created.data);
          return;
        }
        setActiveChatSessionID(created.data.id);
        applyChatSession(created.data);
      } catch (error) {
        if (!isCurrentActiveChatTransition(transitionGeneration)) return;
        preservePendingCreateDraft(transitionGeneration, true);
        setChatErrorState(error, "failed to create Hecate chat");
        if (!isExpectedHecateChatSetupError(error)) {
          params.setNoticeMessage(
            "error",
            error instanceof Error ? error.message : "Failed to create Hecate chat.",
          );
        }
      }
    } finally {
      completeActiveChatTransition(transitionGeneration);
      clearPendingCreateDraft(transitionGeneration);
      finishChatSessionCreate(createIntent);
    }
  }

  async function selectChatSession(
    id: string,
    options: SelectChatSessionOptions = {},
  ): Promise<boolean> {
    if (blockWhileChatOwnershipMutationRuns("switching chats")) return false;
    if (blockWhileChatCancellationOwnsSession(id, "opening this chat")) return false;
    const attachmentTurnSessionID = chatAttachmentTurnSessionID();
    if (hasChatAttachmentTurn() && (!attachmentTurnSessionID || id !== attachmentTurnSessionID)) {
      params.setNoticeMessage("error", "Wait for the attachment response before switching chats.");
      return false;
    }
    const currentSessionID = currentActiveChatSessionID();
    if (id !== currentSessionID && hasPendingChatAttachments()) {
      params.setNoticeMessage("error", "Remove attached files before switching chats.");
      return false;
    }
    const sourceSessionID = currentSessionID;
    const cancellationEpoch = currentChatCancellationEpoch(id);
    const stopReadToken = stopReadTokenAtRequestStart(id);
    const sourceSession = activeChatSession?.id === sourceSessionID ? activeChatSession : null;
    const liveSourceComposerSnapshot = getMessageSnapshot();
    const sourceComposerSnapshot =
      activeChatSessionID !== currentSessionID
        ? { ...liveSourceComposerSnapshot, content: message }
        : liveSourceComposerSnapshot;
    const sourceComposerOwnerSessionID =
      activeChatSessionID !== currentSessionID && id === activeChatSessionID
        ? activeChatSessionID
        : sourceSessionID;
    const sourceWorkspace = agentWorkspace;
    const sourceWorkspaceBranch = agentWorkspaceBranch;
    releaseDetachedComposerDraft();
    const selectionGeneration = beginActiveChatTransition();
    rememberChatComposerDraft(sourceComposerOwnerSessionID, sourceComposerSnapshot.content);
    const activeDraftIsOwned =
      composerDraftsBySessionID.has(id) || Boolean(sourceComposerSnapshot.content);
    const storedTargetDraft =
      id === activeChatSessionID && activeDraftIsOwned
        ? sourceComposerSnapshot.content
        : (composerDraftsBySessionID.get(id) ?? options.draft ?? "");
    const savedTargetDraft =
      options.draft === undefined && !storedTargetDraft.trim()
        ? savedComposerDraftsBySessionID.get(id)?.[0]
        : undefined;
    const targetDraft = savedTargetDraft ?? storedTargetDraft;
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
    setMessage(targetDraft);
    const selectionTransferSnapshot = getMessageSnapshot();
    const restoreAttachmentOwner = () => {
      if (!isCurrentActiveChatTransition(selectionGeneration)) return;
      const liveComposerSnapshot = getMessageSnapshot();
      const restoredComposer =
        liveComposerSnapshot.revision === selectionTransferSnapshot.revision &&
        liveComposerSnapshot.content === selectionTransferSnapshot.content
          ? sourceComposerSnapshot.content
          : liveComposerSnapshot.content;
      setActiveChatSessionID(sourceSessionID);
      setActiveChatSession(sourceSession);
      setAgentWorkspace(sourceWorkspace);
      setAgentWorkspaceBranch(sourceWorkspaceBranch);
      setMessage(restoredComposer);
      rememberChatComposerDraft(sourceSessionID, restoredComposer);
    };
    try {
      const payload = await getChatSession(id);
      const latest = await latestSessionAfterCancellation(
        id,
        cancellationEpoch,
        payload.data,
        snapshotSourceForStopRead(stopReadToken),
      );
      const { session } = latest;
      if (!isCurrentActiveChatTransition(selectionGeneration)) return false;
      if (isChatSessionDeleted(id, session.project_id)) return false;
      const liveAttachmentTurnSessionID = chatAttachmentTurnSessionID();
      if (
        hasChatAttachmentTurn() &&
        (!liveAttachmentTurnSessionID || id !== liveAttachmentTurnSessionID)
      ) {
        restoreAttachmentOwner();
        params.setNoticeMessage(
          "error",
          "Wait for the attachment response before switching chats.",
        );
        return false;
      }
      if (id !== sourceSessionID && hasPendingChatAttachments()) {
        restoreAttachmentOwner();
        params.setNoticeMessage("error", "Remove attached files before switching chats.");
        return false;
      }
      if (savedTargetDraft) {
        consumeSavedComposerDraft(id, savedTargetDraft);
        rememberChatComposerDraft(id, savedTargetDraft);
      }
      applyChatSession(session, latest.source);
      if (session.agent_id && session.agent_id !== "hecate") {
        setAgentAdapterID(session.agent_id);
      }
      setAgentWorkspace(session.workspace ?? "");
      setAgentWorkspaceBranch(session.workspace_branch ?? "");
      clearChatErrorState();
      completeActiveChatTransition(selectionGeneration);
      return true;
    } catch (error) {
      if (!isCurrentActiveChatTransition(selectionGeneration)) return false;
      if (isChatSessionDeleted(id)) return false;
      if (hasPendingChatAttachments() || hasChatAttachmentTurn()) {
        restoreAttachmentOwner();
        setChatErrorState(error, "failed to load agent chat");
        return false;
      }
      const msg = error instanceof Error ? error.message : "failed to load agent chat";
      rememberChatComposerDraft(id, getMessageSnapshot().content);
      setActiveChatSessionID("");
      setActiveChatSession(null);
      setAgentWorkspaceBranch("");
      setMessage("");
      setChatErrorState(error, "failed to load agent chat");
      params.setNoticeMessage("error", msg);
      completeActiveChatTransition(selectionGeneration);
      return false;
    } finally {
      completeActiveChatTransition(selectionGeneration);
    }
  }

  function startNewChat() {
    if (blockWhileChatOwnershipMutationRuns("starting a new chat")) return;
    if (hasPendingChatAttachments()) {
      params.setNoticeMessage("error", "Remove attached files before starting a new chat.");
      return;
    }
    if (hasChatAttachmentTurn()) {
      params.setNoticeMessage(
        "error",
        "Wait for the attachment response before starting a new chat.",
      );
      return;
    }
    releaseDetachedComposerDraft();
    const transitionGeneration = beginActiveChatTransition();
    const currentSessionID = currentActiveChatSessionID();
    rememberChatComposerDraft(currentSessionID, message);
    if (currentSessionID) {
      setQueuedChatMessages((current) =>
        current.filter((item) => item.session_id !== currentSessionID),
      );
    }
    setActiveChatSessionID("");
    setActiveChatSession(null);
    setAgentWorkspaceBranch("");
    resetChatWorkspaceState();
    completeActiveChatTransition(transitionGeneration);
  }

  async function deleteChatSession(id: string) {
    if (blockWhileChatCancellationOwnsSession(id, "deleting this chat")) return false;
    const ownershipMutationToken = beginChatOwnershipMutation();
    if (ownershipMutationToken === null) {
      const message = hasChatAttachmentTurn()
        ? "Wait for the attachment response before deleting a chat."
        : hasPendingChatAttachments()
          ? "Remove attached files before deleting a chat."
          : "Wait for the current chat ownership change to finish before deleting a chat.";
      params.setNoticeMessage("error", message);
      return false;
    }
    let ownershipReleased = false;
    try {
      await deleteChatSessionRequest(id);
      // The server row is gone as soon as DELETE succeeds. Establish the
      // process-local fence before fallible browser queue cleanup so late
      // hydrations, streams, and submissions cannot reapply the deleted chat
      // during a recoverable local-storage failure.
      tombstoneDeletedChatSession(id);
      const stopFence = getChatStopFence(id);
      if (stopFence) clearChatStopFence(stopFence);
      if (!deleteQueuedChatMessagesForSession(id)) {
        params.setNoticeMessage(
          "error",
          "The chat was deleted on the server, but Hecate could not safely fence and remove its queued prompts from browser storage. Free browser storage or clear Hecate site data, then retry Delete.",
        );
        return false;
      }
      setChatSessions((current) => current.filter((s) => s.id !== id));
      setChatTargetBySessionID((current) => {
        if (!current.has(id)) return current;
        const next = new Map(current);
        next.delete(id);
        return next;
      });
      setSavedComposerDraftsBySessionID((current) => {
        if (!current.has(id)) return current;
        const next = new Map(current);
        next.delete(id);
        return next;
      });
      if (currentActiveChatSessionID() === id) {
        // The DELETE response has settled and all late draft/turn admission
        // stayed excluded. Release immediately before the synchronous local
        // reset so startNewChat can pass the shared ownership guard without
        // opening an event-loop gap for late work.
        finishChatOwnershipMutation(ownershipMutationToken);
        ownershipReleased = true;
        startNewChat();
      }
      setComposerDraftsBySessionID((current) => {
        if (!current.has(id)) return current;
        const next = new Map(current);
        next.delete(id);
        return next;
      });
      params.setNoticeMessage("success", "Agent chat deleted.");
      return true;
    } catch (error) {
      params.setNoticeMessage(
        "error",
        error instanceof Error ? error.message : "Failed to delete agent chat.",
      );
      return false;
    } finally {
      if (!ownershipReleased) finishChatOwnershipMutation(ownershipMutationToken);
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
    if (blockWhileChatCancellationOwnsSession(sessionID, "resolving this approval")) {
      return false;
    }
    const cancellationEpoch = currentChatCancellationEpoch(sessionID);
    const result = await approvals.actions.resolveApproval(sessionID, approvalID, decision);
    await refreshApprovalStateAfterCancellation(sessionID, cancellationEpoch);
    if (!result.ok) params.setNoticeMessage("error", result.error);
    return result.ok;
  }

  async function cancelChatApproval(sessionID: string, approvalID: string): Promise<boolean> {
    if (blockWhileChatCancellationOwnsSession(sessionID, "cancelling this approval")) {
      return false;
    }
    const cancellationEpoch = currentChatCancellationEpoch(sessionID);
    const result = await approvals.actions.cancelApproval(sessionID, approvalID);
    await refreshApprovalStateAfterCancellation(sessionID, cancellationEpoch);
    if (!result.ok) params.setNoticeMessage("error", result.error);
    return result.ok;
  }

  async function resolveTaskApproval(
    taskID: string,
    approvalID: string,
    decision: ResolveTaskApprovalPayload,
  ): Promise<boolean> {
    const taskSessionID =
      activeChatSession?.task_id === taskID
        ? activeChatSession.id
        : (chatSessions.find((session) => session.task_id === taskID)?.id ?? "");
    if (
      taskSessionID &&
      blockWhileChatCancellationOwnsSession(taskSessionID, "resolving this approval")
    ) {
      return false;
    }
    const cancellationEpoch = currentChatCancellationEpoch(taskSessionID);
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
      if (taskSessionID) {
        try {
          if (!(await refreshSessionAfterCancellation(taskSessionID, cancellationEpoch))) {
            await refreshChatSession(taskSessionID);
          }
        } catch {
          // The local approval state above already removes the action;
          // a follow-up session refresh is best-effort because the run
          // may still be transitioning after the operator decision.
        }
      }
      return true;
    } catch (error) {
      if (taskSessionID && currentChatCancellationEpoch(taskSessionID) !== cancellationEpoch) {
        try {
          await refreshSessionAfterCancellation(taskSessionID, cancellationEpoch);
        } catch {
          // Keep the terminal-fenced projection when catch-up fails.
        }
        params.setNoticeMessage(
          "error",
          error instanceof Error ? error.message : "Failed to resolve task approval.",
        );
        return false;
      }
      if (error instanceof Error && /not pending/i.test(error.message)) {
        // Server says the approval is already resolved. The
        // resolution may NOT match the operator's chosen decision —
        // another tab could have approved while this one tried to
        // reject, the run might have timed out into auto-rejection,
        // or the run could have been cancelled. Refresh to pull
        // server-truth and let it overwrite our optimistic patch.
        if (taskSessionID) {
          try {
            await refreshChatSession(taskSessionID);
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
    expectedRevision: string,
  ): Promise<ChatWorkspaceDiffRecord | null> {
    if (blockWhileChatCancellationOwnsSession(sessionID, "discarding workspace changes")) {
      return null;
    }
    const cancellationEpoch = currentChatCancellationEpoch(sessionID);
    try {
      const payload = await revertChatWorkspaceFilesRequest(sessionID, paths, expectedRevision);
      let snapshot = payload.data;
      if (currentChatCancellationEpoch(sessionID) !== cancellationEpoch) {
        const latest = await readAfterChatCancellationSettles(
          sessionID,
          async () => (await getChatWorkspaceDiffRequest(sessionID)).data,
        );
        if (!latest) return null;
        snapshot = latest;
      }
      params.setNoticeMessage(
        "success",
        paths.length > 0 ? "Selected workspace files discarded." : "Workspace changes discarded.",
      );
      return snapshot;
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
    if (blockWhileChatCancellationOwnsSession(sessionID, "changing agent settings")) {
      return false;
    }
    const cancellationEpoch = currentChatCancellationEpoch(sessionID);
    const stopRequestToken = stopReadTokenAtRequestStart(sessionID);
    try {
      const payload = await setChatConfigOptionRequest(sessionID, configID, value);
      const latest = await latestSessionAfterCancellation(
        sessionID,
        cancellationEpoch,
        payload.data,
        snapshotSourceForStopRead(stopRequestToken),
      );
      applyChatSession(latest.session, latest.source);
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
    const sessionID = currentActiveChatSessionID();
    const session = activeChatSession?.id === sessionID ? activeChatSession : null;
    if (
      sessionID &&
      session &&
      !chatSessionIsExternal(session) &&
      blockWhileChatCancellationOwnsSession(sessionID, "changing chat settings")
    ) {
      return false;
    }
    setHecateRTKEnabledState(enabled);
    if (!sessionID || !session || chatSessionIsExternal(session)) {
      return true;
    }
    const cancellationEpoch = currentChatCancellationEpoch(sessionID);
    const stopRequestToken = stopReadTokenAtRequestStart(sessionID);
    try {
      const payload = await setChatSettingsRequest(sessionID, { rtk_enabled: enabled });
      const latest = await latestSessionAfterCancellation(
        sessionID,
        cancellationEpoch,
        payload.data,
        snapshotSourceForStopRead(stopRequestToken),
      );
      applyChatSession(latest.session, latest.source);
      return true;
    } catch (error) {
      setHecateRTKEnabledState(Boolean(session.rtk_enabled));
      params.setNoticeMessage(
        "error",
        error instanceof Error ? error.message : "Failed to update chat settings.",
      );
      return false;
    }
  }

  async function setHecateWorkspaceMode(mode: ChatWorkspaceMode): Promise<boolean> {
    const nextMode = normalizeChatWorkspaceMode(mode);
    const sessionID = currentActiveChatSessionID();
    const session = activeChatSession?.id === sessionID ? activeChatSession : null;
    if (!sessionID || !session) {
      if (activeProjectID) {
        params.setNoticeMessage(
          "error",
          "This new chat inherits its workspace mode from Project settings.",
        );
        return false;
      }
      setAgentWorkspaceMode(nextMode);
      return true;
    }
    if (chatSessionIsExternal(session)) return false;
    if (blockWhileChatCancellationOwnsSession(sessionID, "changing the workspace mode")) {
      return false;
    }
    if ((session.workspace_mode ?? "in_place") === nextMode) return true;

    const token = ++nextWorkspaceModeMutationTokenRef.current;
    const mutation = { sessionID, requestedMode: nextMode, token };
    workspaceModeMutationRef.current = mutation;
    setWorkspaceModeMutation(mutation);
    const mutationIsCurrent = () => workspaceModeMutationRef.current?.token === token;
    const cancellationEpoch = currentChatCancellationEpoch(sessionID);
    const stopRequestToken = stopReadTokenAtRequestStart(sessionID);
    try {
      const payload = await setChatSettingsRequest(sessionID, { workspace_mode: nextMode });
      if (!mutationIsCurrent()) return false;
      const latest = await latestSessionAfterCancellation(
        sessionID,
        cancellationEpoch,
        payload.data,
        snapshotSourceForStopRead(stopRequestToken),
      );
      if (!mutationIsCurrent()) return false;
      applyChatSession(latest.session, latest.source);
      return true;
    } catch (error) {
      if (!mutationIsCurrent()) return false;
      try {
        const authoritative = await getChatSession(sessionID);
        if (mutationIsCurrent()) {
          applyChatSession(authoritative.data, snapshotSourceForStopRead(stopRequestToken));
        }
      } catch {
        // Preserve the original mutation failure. A later dashboard/session
        // refresh remains authoritative if this recovery read also fails.
      }
      params.setNoticeMessage(
        "error",
        error instanceof Error ? error.message : "Failed to update the workspace mode.",
      );
      return false;
    } finally {
      if (mutationIsCurrent()) {
        workspaceModeMutationRef.current = null;
        setWorkspaceModeMutation(null);
      }
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

  async function renameChatSession(id: string, title: string) {
    if (blockWhileChatCancellationOwnsSession(id, "renaming this chat")) return;
    const cancellationEpoch = currentChatCancellationEpoch(id);
    try {
      const nextTitle = title.trim();
      if (!nextTitle) {
        params.setNoticeMessage("error", "Chat title cannot be empty.");
        return;
      }
      const payload = await updateChatSessionRequest(id, nextTitle);
      if (currentChatCancellationEpoch(id) !== cancellationEpoch) {
        await refreshSessionAfterCancellation(id, cancellationEpoch);
        return;
      }
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
    reconcileQueuedChatMessage,
    // The wide public surface that lands in the viewmodel actions bag
    submitChat,
    cancelAgentChat,
    compactChatSession,
    updateToolResult,
    submitToolResults,
    createChatSession,
    selectChatSession,
    restoreSavedComposerDraft,
    startNewChat,
    deleteChatSession,
    renameChatSession,
    setChatTarget,
    setChatToolsEnabled,
    setNewChatAgent,
    updateAgentWorkspace,
    selectProviderRoute,
    selectChatModel,
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
    setChatConfigOption,
    setHecateRTKEnabled,
    setHecateWorkspaceMode,
  };
  const overrides = useContext(CoordinatorOverridesContext);
  return applyOverride(real, overrides?.chat);
}
