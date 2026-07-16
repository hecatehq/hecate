// Dashboard coordinator: bulk loaders that fan a resolved
// dashboard snapshot across the slices. loadDashboard is invoked
// at mount + by the settings / provider mutations (refresh after
// write); refreshProviders is the polling refresh used by the
// providers tab; refreshRuntimeState (alias to chat's helper) is
// re-exposed for symmetry.
//
// The hook composes chat for applyChatSession +
// syncHecateSelectionFromSession (snapshot commit reuses chat's
// state-merge logic) so dashboard and chat agree on session shape
// without duplicating the renderChatSessionSummary chain.

import { useContext } from "react";

import { applyOverride, CoordinatorOverridesContext } from "./overrides";
import { resolveDashboardSnapshot } from "../../runtimeConsoleDashboard";
import { useChat, type ChatSessionSnapshotSource } from "../chat";
import { useProvidersAndModels } from "../providersAndModels";
import { useRuntime } from "../runtime";
import type { ChatSessionRecord, ChatSessionSummaryRecord } from "../../../types/chat";
import type { ConfiguredStateResponse } from "../../../types/provider";
import type { ChatActions } from "./chat";

type SetStateAction<T> = T | ((prev: T) => T);

export type UseDashboardActionsParams = {
  settingsConfig: ConfiguredStateResponse["data"] | null;
  setSettingsConfig: (next: SetStateAction<ConfiguredStateResponse["data"] | null>) => void;
  setSettingsError: (value: string) => void;
  applyChatSession: ChatActions["applyChatSession"];
  syncHecateSelectionFromSession: ChatActions["syncHecateSelectionFromSession"];
  refreshRuntimeState: ChatActions["refreshRuntimeState"];
};

export function useDashboardActions(params: UseDashboardActionsParams) {
  const runtime = useRuntime();
  const providersAndModels = useProvidersAndModels();
  const chat = useChat();

  const {
    setHealth,
    setSessionInfo,
    setLoading,
    setError,
    setHecateRTKAvailable,
    setHecateRTKPath,
  } = runtime.actions;
  const { providers, agentAdapters } = providersAndModels.state;
  const { setProviders, setModels, setAgentAdapters, setAgentAdapterApprovalMode } =
    providersAndModels.actions;
  const { activeChatSession, chatSessions } = chat.state;
  const {
    captureActiveChatTransition,
    isCurrentActiveChatTransition,
    setChatSessions,
    setActiveChatSessionID,
    setActiveChatSession,
    fenceChatSessionsMissingFromAuthoritativeSnapshot,
    currentChatSessionIntent,
    isCurrentChatSessionIntent,
    hasChatAttachmentTurn,
    currentActiveChatSessionID,
    isChatSessionDeleted,
    stopReadTokenAtRequestStart,
    chatStopFenceAllowsSnapshot,
    chatStopFenceAllowsOmission,
    chatStopFenceProtectsSession,
  } = chat.actions;

  async function loadDashboard() {
    const chatSessionIntent = currentChatSessionIntent();
    const dashboardActiveChatSessionID = currentActiveChatSessionID();
    // Dashboard hydration is passive: it may publish the active chat only if
    // no newer operator selection, creation, or new-chat transition began
    // while its two request waves were resolving.
    const activeChatTransition = captureActiveChatTransition();
    let chatSessionsReadStopTokens = new Map<string, number>();
    let activeChatSessionReadStopToken: { sessionID: string; stopToken: number } | null = null;
    const captureChatSessionsReadStopTokens = () => {
      const sessionIDs = new Set(chatSessions.map((session) => session.id));
      if (dashboardActiveChatSessionID) sessionIDs.add(dashboardActiveChatSessionID);
      const captured = new Map<string, number>();
      for (const sessionID of sessionIDs) {
        const stopToken = stopReadTokenAtRequestStart(sessionID);
        if (stopToken !== null) captured.set(sessionID, stopToken);
      }
      chatSessionsReadStopTokens = captured;
    };
    setLoading(true);
    setError("");
    params.setSettingsError("");

    try {
      const snapshot = await resolveDashboardSnapshot({
        activeChatSessionID: dashboardActiveChatSessionID,
        previous: {
          providers,
          agentAdapters,
          chatSessions,
          activeChatSession,
          settingsConfig: params.settingsConfig,
        },
        // Commit just enough state to drop the AuthLoadingShell as
        // soon as wave 1 resolves — the activity bar + status bar
        // can render with what's here while the secondary wave
        // (chats, providers, usage, retention, …) finishes in the
        // background. The big batch update below then idempotently
        // re-applies these slots alongside the secondary results.
        onEssentials: (essentials) => {
          setHealth(essentials.health);
          setSessionInfo(essentials.sessionInfo);
          params.setSettingsConfig(essentials.settingsConfig);
        },
        onChatSessionsReadStart: captureChatSessionsReadStopTokens,
        onActiveChatSessionReadStart: (sessionID) => {
          const stopToken = stopReadTokenAtRequestStart(sessionID);
          activeChatSessionReadStopToken = stopToken === null ? null : { sessionID, stopToken };
        },
      });

      setHealth(snapshot.health);
      setSessionInfo(snapshot.sessionInfo);
      setModels(snapshot.models);
      setProviders(snapshot.providers);
      setAgentAdapters(snapshot.agentAdapters);
      const liveChatSessions = snapshot.chatSessions.filter(
        (session: ChatSessionRecord) => !isChatSessionDeleted(session.id, session.project_id),
      );
      if (
        activeChatTransition !== null &&
        isCurrentActiveChatTransition(activeChatTransition) &&
        isCurrentChatSessionIntent(chatSessionIntent) &&
        currentActiveChatSessionID() === dashboardActiveChatSessionID &&
        !hasChatAttachmentTurn()
      ) {
        const activeReadStopToken = activeChatSessionReadStopToken as {
          sessionID: string;
          stopToken: number;
        } | null;
        const unscopedSource: ChatSessionSnapshotSource = { kind: "unscoped" };
        const chatSessionsReadSource = (sessionID: string): ChatSessionSnapshotSource => {
          const stopToken = snapshot.chatSessionsFresh
            ? (chatSessionsReadStopTokens.get(sessionID) ?? null)
            : null;
          return stopToken === null ? unscopedSource : { kind: "stop_read", stopToken };
        };
        const activeChatSessionReadSource = (): ChatSessionSnapshotSource => {
          if (
            !snapshot.activeChatSessionFresh ||
            !activeReadStopToken ||
            activeReadStopToken.sessionID !== snapshot.activeChatSession?.id
          ) {
            return unscopedSource;
          }
          return { kind: "stop_read", stopToken: activeReadStopToken.stopToken };
        };
        const nextActiveSnapshot = snapshot.activeChatSession;
        const activeSnapshotAllowedBeforeDeletion = nextActiveSnapshot
          ? chatStopFenceAllowsSnapshot(nextActiveSnapshot, activeChatSessionReadSource())
          : false;
        const allowedChatSessionSummaries = new Map<string, boolean>();
        for (const session of liveChatSessions) {
          const activeDetailRejected =
            snapshot.activeChatSessionFresh &&
            nextActiveSnapshot?.id === session.id &&
            !activeSnapshotAllowedBeforeDeletion;
          allowedChatSessionSummaries.set(
            session.id,
            activeDetailRejected
              ? false
              : chatStopFenceAllowsSnapshot(session, chatSessionsReadSource(session.id)),
          );
        }

        const authoritativeSessionIDs = new Set(liveChatSessions.map((session) => session.id));
        const authoritativeOmissionStopTokens = snapshot.chatSessionsFresh
          ? new Map(chatSessionsReadStopTokens)
          : new Map<string, number>();
        if (
          snapshot.activeChatSessionMissing &&
          activeReadStopToken?.sessionID === dashboardActiveChatSessionID &&
          chatStopFenceAllowsOmission(dashboardActiveChatSessionID, activeReadStopToken.stopToken)
        ) {
          authoritativeSessionIDs.delete(dashboardActiveChatSessionID);
          authoritativeOmissionStopTokens.set(
            dashboardActiveChatSessionID,
            activeReadStopToken.stopToken,
          );
        }
        const queueCleanupSucceeded = snapshot.chatSessionsFresh
          ? fenceChatSessionsMissingFromAuthoritativeSnapshot(
              authoritativeSessionIDs,
              authoritativeOmissionStopTokens,
            )
          : true;
        const snapshotActiveDeleted = Boolean(
          (dashboardActiveChatSessionID &&
            isChatSessionDeleted(
              dashboardActiveChatSessionID,
              snapshot.activeChatSession?.project_id ?? activeChatSession?.project_id,
            )) ||
          (snapshot.activeChatSession?.id &&
            isChatSessionDeleted(
              snapshot.activeChatSession.id,
              snapshot.activeChatSession.project_id,
            )),
        );
        const committableChatSessions = liveChatSessions.filter(
          (session) => !isChatSessionDeleted(session.id, session.project_id),
        );
        setChatSessions((current) => {
          const currentByID = new Map(current.map((session) => [session.id, session]));
          const next = committableChatSessions.flatMap((session: ChatSessionSummaryRecord) => {
            if (allowedChatSessionSummaries.get(session.id)) return [session];
            const retained = currentByID.get(session.id);
            return retained ? [retained] : [];
          });
          const nextIDs = new Set(next.map((session) => session.id));
          for (const session of current) {
            if (!nextIDs.has(session.id) && chatStopFenceProtectsSession(session.id)) {
              next.push(session);
            }
          }
          return next;
        });
        const nextActiveSession = snapshotActiveDeleted ? null : snapshot.activeChatSession;
        const activeSnapshotAllowed =
          snapshotActiveDeleted ||
          (nextActiveSession
            ? activeSnapshotAllowedBeforeDeletion
            : !chatStopFenceProtectsSession(dashboardActiveChatSessionID));
        if (activeSnapshotAllowed) {
          setActiveChatSessionID(snapshotActiveDeleted ? "" : snapshot.activeChatSessionID);
          setActiveChatSession(nextActiveSession);
          params.syncHecateSelectionFromSession(nextActiveSession);
        }
        if (!queueCleanupSucceeded) {
          setError(
            "The dashboard removed a deleted chat, but browser queue cleanup needs attention before queued work can continue.",
          );
        }
      } else {
        // The snapshot predates a newer operator transition. Keep every newer
        // local summary authoritative, but still add non-conflicting sessions
        // discovered by the dashboard so the rest of the sidebar does not
        // remain empty until another refresh.
        const allowedAdditions = liveChatSessions.filter((session) => {
          const stopToken = snapshot.chatSessionsFresh
            ? (chatSessionsReadStopTokens.get(session.id) ?? null)
            : null;
          return chatStopFenceAllowsSnapshot(
            session,
            stopToken === null ? { kind: "unscoped" } : { kind: "stop_read", stopToken },
          );
        });
        setChatSessions((current) => {
          const currentIDs = new Set(current.map((session) => session.id));
          const additions = allowedAdditions.filter((session) => !currentIDs.has(session.id));
          return additions.length > 0 ? [...current, ...additions] : current;
        });
      }
      params.setSettingsConfig(snapshot.settingsConfig);
      setAgentAdapterApprovalMode(snapshot.agentAdapterApprovalMode);
      setHecateRTKAvailable(snapshot.rtkAvailable);
      setHecateRTKPath(snapshot.rtkPath);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "unknown load error");
    } finally {
      setLoading(false);
    }
  }

  // refreshProviders re-fetches /hecate/v1/providers/status (runtime health)
  // and /v1/models (model catalog) for explicit refreshes and the
  // Connections auto-poll. Do not gate this on captured settings state:
  // provider rows can be created by another slice between renders, and a
  // stale closure here leaves the UI stuck on PENDING until a full reload.
  async function refreshProviders() {
    await providersAndModels.actions.refreshProviders();
  }

  const overrides = useContext(CoordinatorOverridesContext);
  return applyOverride(
    {
      loadDashboard,
      refreshProviders,
      refreshRuntimeState: params.refreshRuntimeState,
    },
    overrides?.dashboard,
  );
}
