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

import type { ProviderFilter } from "../../types/runtime";

export type ChatTarget = "model" | "agent" | "external_agent";
export type HecateChatTarget = "model" | "agent";

export type QueuedChatMessage = {
  id: string;
  session_id: string;
  content: string;
  runtime_kind: ChatTarget;
  provider_filter: ProviderFilter;
  model: string;
  workspace: string;
  system_prompt: string;
  adapter_id: string;
  created_at: string;
};

export const queuedChatMessagesStorageKey = "hecate.queuedChatMessages";

// Coercive normalizer — drops unknown values onto the safe default
// rather than rejecting them. Used by code paths where the wider
// type system has already vouched for the input (e.g. an
// `ChatSessionRecord.runtime_kind` field straight off the
// wire). For localStorage reads use the strict
// `parseStoredChatTarget` below so corrupt keys get wiped.
export function normalizeStoredChatTarget(value: string): ChatTarget {
  switch (value) {
    case "model":
    case "agent":
    case "external_agent":
      return value;
    default:
      return "agent";
  }
}

// Strict guard for the `hecate.chatTarget` localStorage key.
// usePersistedState's contract is "loud failure replaces silent
// shape-drift fallback" — return null so a corrupt key gets wiped
// and the hook falls back to the explicit "agent" default rather
// than silently coercing the bad value and re-persisting it.
// normalizeStoredChatTarget (above) keeps its coercive behaviour
// for non-storage sources where the wider type system has already
// vouched for the input.
export function parseStoredChatTarget(raw: string): ChatTarget | null {
  return raw === "model" || raw === "agent" || raw === "external_agent" ? raw : null;
}

export function normalizeStoredHecateChatTarget(value: string): HecateChatTarget | "" {
  switch (value) {
    case "model":
    case "agent":
      return value;
    default:
      return "";
  }
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
    return [{
      id,
      session_id: sessionID,
      content,
      runtime_kind: normalizeStoredChatTarget(typeof item.runtime_kind === "string" ? item.runtime_kind : ""),
      provider_filter: typeof item.provider_filter === "string" ? item.provider_filter as ProviderFilter : "auto",
      model: typeof item.model === "string" ? item.model : "",
      workspace: typeof item.workspace === "string" ? item.workspace : "",
      system_prompt: typeof item.system_prompt === "string" ? item.system_prompt : "",
      adapter_id: typeof item.adapter_id === "string" ? item.adapter_id : "",
      created_at: typeof item.created_at === "string" ? item.created_at : new Date().toISOString(),
    }];
  });
}

// Structural guard for the per-session HecateChatTarget map. Stored as
// a JSON object {sessionID: target}; rebuilt into a Map so the consuming
// code can use Map semantics (.get / .set / iteration order).
export function parseChatTargetsBySessionID(parsed: unknown): Map<string, HecateChatTarget> | null {
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return null;
  const entries = Object.entries(parsed as Record<string, unknown>)
    .map(([sessionID, target]) =>
      [sessionID, typeof target === "string" ? normalizeStoredHecateChatTarget(target) : ""] as const,
    )
    .filter((entry): entry is readonly [string, HecateChatTarget] => Boolean(entry[0] && entry[1]));
  return new Map(entries);
}

export function serializeChatTargetsBySessionID(targets: Map<string, HecateChatTarget>): string {
  return JSON.stringify(Object.fromEntries(targets));
}
