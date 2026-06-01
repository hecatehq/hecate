// Cross-slice types, storage keys, and storage-format normalize /
// parse / serialize helpers. Shared by the per-domain hooks under
// `app/state/` and by `useRuntimeConsole.ts` (which composes the
// slices into the legacy view model).
//
// What's here: anything that's coupled to the on-disk localStorage
// format. Types that live in `types/runtime.ts` (network response
// shapes) stay there; the leak between "what the server sends" and
// "what we persist" is intentional and minor.
//
// What's NOT here yet: session-derive helpers
// (`deriveHecateChatTargetFromSession`,
// `deriveHecateChatSelectionFromSession`,
// `chatSession{IsExternal,IsBusy}`). Those are
// chats-domain specific and move with the chats slice.

import type { ProviderFilter } from "../../types/provider";

// ChatTarget discriminates where a new chat dispatches to. Two values:
// the local Hecate agent ("agent") or one of the external coding agents
// (Codex, Claude Code, Cursor Agent, Grok Build — all "external_agent").
// Tools on/off within an agent chat is a separate axis carried on
// `chatToolsEnabledBySessionID` / `defaultChatToolsEnabled`.
export type ChatTarget = "agent" | "external_agent";
// HecateChatTarget kept as a single-value alias so call sites that
// specifically require the non-external branch read self-documenting.
// Collapses to "agent" — could be inlined, but the named type signals
// intent at use sites that operate only on the Hecate-targeted slice.
export type HecateChatTarget = "agent";
// ChatExecutionMode is the runtime owner category. Hecate-owned turns
// always use hecate_task; tools on/off is carried separately.
export type ChatExecutionMode = "hecate_task" | "external_agent";

export type QueuedChatMessage = {
  id: string;
  session_id: string;
  content: string;
  execution_mode: ChatExecutionMode;
  tools_enabled: boolean;
  provider_filter: ProviderFilter;
  model: string;
  workspace: string;
  system_prompt: string;
  agent_id: string;
  created_at: string;
};

export const queuedChatMessagesStorageKey = "hecate.queuedChatMessages";

// Coercive normalizer — drops unknown values onto the safe default
// rather than rejecting them. Used by code paths where the wider
// type system has already vouched for the input. For localStorage
// reads use the strict `parseStoredChatTarget` below.
export function normalizeStoredChatTarget(value: string): ChatTarget {
  return value === "external_agent" ? "external_agent" : "agent";
}

export function chatTargetToExecutionMode(target: ChatTarget): ChatExecutionMode {
  switch (target) {
    case "agent":
      return "hecate_task";
    case "external_agent":
      return "external_agent";
  }
}

export function executionModeToChatTarget(mode: string): ChatTarget | "" {
  switch (mode) {
    case "hecate_task":
      return "agent";
    case "external_agent":
      return "external_agent";
    default:
      return "";
  }
}

// Strict guard for the `hecate.chatTarget` localStorage key.
// usePersistedState's contract is "loud failure replaces silent
// shape-drift fallback" — return null so a corrupt key gets wiped
// and the hook falls back to the explicit "agent" default rather
// than silently coercing the bad value and re-persisting it.
//
export function parseStoredChatTarget(raw: string): ChatTarget | null {
  if (raw === "agent" || raw === "external_agent") return raw;
  return null;
}

// Single-value collapse: HecateChatTarget only has "agent" today.
export function normalizeStoredHecateChatTarget(value: string): HecateChatTarget | "" {
  return value === "agent" ? "agent" : "";
}

// Structural guard for the queued-chat-messages localStorage payload.
// The previous read helper massaged each missing field into an empty
// string and re-persisted the half-zeroed record; the new contract
// drops malformed *items* (preserving the rest of the queue) and
// rejects the *whole array* if it isn't an array — `usePersistedState`
// then wipes the key on the array-shape failure. Per-item filtering
// is preserved because losing the entire queue to one corrupt entry
// is worse than losing the bad entry.
export function parseQueuedChatMessageList(parsed: unknown): QueuedChatMessage[] | null {
  if (!Array.isArray(parsed)) return null;
  return parsed.flatMap((item): QueuedChatMessage[] => {
    if (!item || typeof item !== "object") return [];
    const id = typeof item.id === "string" ? item.id : "";
    const sessionID = typeof item.session_id === "string" ? item.session_id : "";
    const content = typeof item.content === "string" ? item.content : "";
    if (!id || !sessionID || !content.trim()) return [];
    const executionMode =
      item.execution_mode === "hecate_task" || item.execution_mode === "external_agent"
        ? item.execution_mode
        : "";
    if (!executionMode) return [];
    return [
      {
        id,
        session_id: sessionID,
        content,
        execution_mode: executionMode,
        tools_enabled: typeof item.tools_enabled === "boolean" ? item.tools_enabled : true,
        provider_filter:
          typeof item.provider_filter === "string"
            ? (item.provider_filter as ProviderFilter)
            : "auto",
        model: typeof item.model === "string" ? item.model : "",
        workspace: typeof item.workspace === "string" ? item.workspace : "",
        system_prompt: typeof item.system_prompt === "string" ? item.system_prompt : "",
        agent_id: typeof item.agent_id === "string" ? item.agent_id : "",
        created_at:
          typeof item.created_at === "string" ? item.created_at : new Date().toISOString(),
      },
    ];
  });
}

// Structural guard for the per-session HecateChatTarget map. Stored as
// a JSON object {sessionID: target}; rebuilt into a Map so the consuming
// code can use Map semantics (.get / .set / iteration order).
export function parseChatTargetsBySessionID(parsed: unknown): Map<string, HecateChatTarget> | null {
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return null;
  const entries = Object.entries(parsed as Record<string, unknown>)
    .map(
      ([sessionID, target]) =>
        [
          sessionID,
          typeof target === "string" ? normalizeStoredHecateChatTarget(target) : "",
        ] as const,
    )
    .filter((entry): entry is readonly [string, HecateChatTarget] => Boolean(entry[0] && entry[1]));
  return new Map(entries);
}

export function serializeChatTargetsBySessionID(targets: Map<string, HecateChatTarget>): string {
  return JSON.stringify(Object.fromEntries(targets));
}

// Storage key for the user-default tools-enabled preference. Persisted
// separately from the per-session override below so a user's "tools on
// by default for new chats" intent survives across browser restarts
// without bleeding into individual session overrides.
export const chatToolsEnabledStorageKey = "hecate.chatToolsEnabled";

// Storage key for the per-session tools-enabled map. Each entry pins a
// session to an explicit tools-on/off override; a missing key falls back
// to the default. Stored as JSON `{sessionID: boolean}`; rebuilt into a
// Map so consumers use Map semantics (.get / .set / iteration order),
// matching the existing `chatTargetBySessionID` pattern.
export const chatToolsEnabledBySessionIDStorageKey = "hecate.chatToolsEnabledBySessionID";

// Strict guard for the user-default tools-enabled localStorage key.
// usePersistedState's contract is "loud failure replaces silent
// shape-drift fallback" — return null so a corrupt key gets wiped and
// the hook falls back to the explicit `true` default.
export function parseStoredChatToolsEnabled(raw: string): boolean | null {
  if (raw === "true") return true;
  if (raw === "false") return false;
  return null;
}

// Structural guard for the per-session chat-tools-enabled map. Drops
// non-boolean entries instead of failing the whole payload so one
// corrupt key doesn't wipe a user's well-formed sibling overrides.
// Returns null only if the top-level shape is wrong (not an object) —
// usePersistedState then wipes the key, matching the contract.
export function parseChatToolsEnabledBySessionID(parsed: unknown): Map<string, boolean> | null {
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return null;
  const entries = Object.entries(parsed as Record<string, unknown>).filter(
    (entry): entry is [string, boolean] => Boolean(entry[0]) && typeof entry[1] === "boolean",
  );
  return new Map(entries);
}

export function serializeChatToolsEnabledBySessionID(toolsEnabled: Map<string, boolean>): string {
  return JSON.stringify(Object.fromEntries(toolsEnabled));
}
