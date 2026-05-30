// Cross-slice derivations that views (and the test-only composer)
// consume. Moved out of the retired useRuntimeConsole facade so each
// view can opt into only the helpers it needs.
//
// These hooks read slice contexts directly; mounting them outside a
// SliceProviders tree will throw because the inner slice hooks do.

import { useContext, useMemo } from "react";

import { buildLocalProviderIssue, type LocalProviderIssue } from "../../lib/provider-issues";
import { filterModelsByKind, filterModelsByProvider } from "../../lib/runtime-utils";
import { useChat } from "./chat";
import { CoordinatorOverridesContext } from "./coordinators/overrides";
import { useProvidersAndModels } from "./providersAndModels";
import { useRuntime } from "./runtime";
import { deriveSessionState, type SessionState } from "../runtimeConsoleDashboard";
import {
  type ChatTarget,
  type HecateChatTarget,
  executionModeToChatTarget,
  normalizeStoredHecateChatTarget,
} from "./_shared";
import type { ChatSessionRecord } from "../../types/chat";
import type { ModelRecord } from "../../types/model";
import type { ProviderRecord } from "../../types/provider";

function chatSessionIsExternal(session: ChatSessionRecord | null): boolean {
  return Boolean(session?.agent_id && session.agent_id !== "hecate");
}

function deriveHecateChatTargetFromSession(session: ChatSessionRecord | null): HecateChatTarget {
  if (!session) return "agent";
  const messages = session.messages ?? [];
  for (let i = messages.length - 1; i >= 0; i--) {
    const target = normalizeStoredHecateChatTarget(
      executionModeToChatTarget(messages[i]?.execution_mode ?? ""),
    );
    if (target) return target;
  }
  return "agent";
}

function hecateTaskStatusIsActive(status?: string): boolean {
  return status === "queued" || status === "running" || status === "awaiting_approval";
}

function sessionHasActiveHecateTaskSegment(session: ChatSessionRecord | null): boolean {
  if (!session) return false;
  if (session.task_id && hecateTaskStatusIsActive(session.status)) return true;
  return (session.segments ?? []).some(
    (segment) =>
      segment.execution_mode === "hecate_task" &&
      Boolean(segment.task_id) &&
      hecateTaskStatusIsActive(segment.status),
  );
}

// chatTarget gates several views' route/copy decisions. The active
// agent-chat session forces a target via its agent_id
// shape; the per-session map override (used by Hecate Chat to flip
// between agent and direct model) and the default target take effect
// only when nothing in the session pins the target.
export function useChatTarget(): ChatTarget {
  const { state } = useChat();
  // Test escape: per-view fixtures stub `chatTarget` to a precomputed
  // value and expect that to win regardless of slice state. Production
  // never provides the override.
  const overrides = useContext(CoordinatorOverridesContext);
  if (overrides?.derivedChatTarget) return overrides.derivedChatTarget;
  if (state.activeChatSessionID && state.activeChatSession) {
    if (chatSessionIsExternal(state.activeChatSession)) return "external_agent";
    if (sessionHasActiveHecateTaskSegment(state.activeChatSession)) return "agent";
    return (
      state.chatTargetBySessionID.get(state.activeChatSessionID) ??
      deriveHecateChatTargetFromSession(state.activeChatSession)
    );
  }
  return state.defaultChatTarget;
}

// Resolve the tools-enabled state for the active Hecate chat session.
//
// Resolution order:
//   1. Per-session override (`chatToolsEnabledBySessionID`) if set —
//      this is what the in-panel toggle writes.
//   2. The user default (`defaultChatToolsEnabled`).
//
// External-agent sessions ignore this (they have their own tool model);
// callers should gate the call site on `chatTarget === "agent"` before
// using the result to drive UI state.
//
// The legacy `chatTargetBySessionID[id] === "model"` encoding for
// tools-off is migrated forward at slice mount (see chat.tsx), so this
// hook never has to look at chatTarget for back-compat. Deliberately no
// derivation from the message-tail `execution_mode`: the latest turn's
// mode is *historical* (was a downgrade applied because the model lacks
// tools, or because the user asked for tools off?) and confusing those
// would silently flip the toggle state on session resume.
export function useChatToolsEnabled(): boolean {
  const { state } = useChat();
  const sessionID = state.activeChatSessionID;
  if (sessionID) {
    const explicit = state.chatToolsEnabledBySessionID.get(sessionID);
    if (typeof explicit === "boolean") return explicit;
  }
  return state.defaultChatToolsEnabled;
}

// Provider/model derivations that several views read. Returned as one
// bag so a view that needs three of them only pays for one hook call.
export type RuntimeDerivedState = {
  healthyProviders: number;
  localProviders: ProviderRecord[];
  cloudProviders: ProviderRecord[];
  localModels: ModelRecord[];
  cloudModels: ModelRecord[];
  healthyLocalProviders: number;
  healthyCloudProviders: number;
  visibleModels: ModelRecord[];
  providerScopedModels: ModelRecord[];
  localProviderIssues: LocalProviderIssue[];
  session: SessionState;
};

export function useRuntimeDerivedState(): RuntimeDerivedState {
  const { state: runtimeState } = useRuntime();
  const { state: providersState } = useProvidersAndModels();
  const { state: chatState } = useChat();

  const { providers, models } = providersState;
  const { modelFilter, providerFilter } = chatState;

  const localProviders = useMemo(
    () => providers.filter((provider) => provider.kind === "local"),
    [providers],
  );
  const cloudProviders = useMemo(
    () => providers.filter((provider) => provider.kind === "cloud"),
    [providers],
  );
  const localModels = useMemo(
    () => models.filter((entry) => entry.metadata?.provider_kind === "local"),
    [models],
  );
  const cloudModels = useMemo(
    () => models.filter((entry) => entry.metadata?.provider_kind === "cloud"),
    [models],
  );
  const visibleModels = useMemo(
    () => filterModelsByKind(models, modelFilter),
    [modelFilter, models],
  );
  const providerScopedModels = useMemo(
    () => filterModelsByProvider(visibleModels, providerFilter),
    [providerFilter, visibleModels],
  );
  const localProviderIssues = useMemo(
    () =>
      localProviders
        .map((provider) => buildLocalProviderIssue(provider))
        .filter((issue): issue is LocalProviderIssue => issue !== null),
    [localProviders],
  );
  const session = useMemo(
    () => deriveSessionState(runtimeState.sessionInfo),
    [runtimeState.sessionInfo],
  );

  return {
    healthyProviders: providers.filter((provider) => provider.healthy).length,
    localProviders,
    cloudProviders,
    localModels,
    cloudModels,
    healthyLocalProviders: localProviders.filter((provider) => provider.healthy).length,
    healthyCloudProviders: cloudProviders.filter((provider) => provider.healthy).length,
    visibleModels,
    providerScopedModels,
    localProviderIssues,
    session,
  };
}

// newChatAgentID is derived from chat slice state — the default
// target determines whether we surface the configured external
// adapter ID or fall back to hecate. Lives here so AppShell and
// ChatSidebar don't duplicate the rule.
export function useNewChatAgentID(): string {
  const { state } = useChat();
  const overrides = useContext(CoordinatorOverridesContext);
  if (overrides?.derivedNewChatAgentID !== undefined) return overrides.derivedNewChatAgentID;
  return state.defaultChatTarget === "external_agent" ? state.agentAdapterID : "hecate";
}
