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
export type QueuedChatDeliveryState = "submitting" | "retryable" | "reconcile_required";
export type QueuedChatDeliveryErrorCode = "chat.client_request_conflict";

export type QueuedChatMessage = {
  id: string;
  session_id: string;
  // Browser-local ownership metadata used only to purge prompts when their
  // project is deleted. Legacy records may omit it; an unknown owner then
  // makes project cleanup fail closed instead of claiming the prompt is gone.
  project_id?: string;
  content: string;
  // Missing means ready for FIFO drain. A submitting fence is written through
  // before dispatch; on reload the parser converts it to reconcile_required.
  delivery_state?: QueuedChatDeliveryState;
  delivery_baseline_message_ids?: string[];
  // True for snapshots whose durable queue id is also the server-side
  // client_request_id. Manual reconciliation can replay the exact stored
  // payload safely instead of guessing from visible transcript text.
  delivery_idempotency_keyed?: boolean;
  // Durable provenance for reconciliation states that cannot safely use
  // transcript-content heuristics. In particular, the same visible text can
  // still be a different request payload (model, tools, workspace, etc.).
  delivery_error_code?: QueuedChatDeliveryErrorCode;
  // Local-only projection for a durable submitting fence observed from
  // another tab or after reload. The per-item storage serializer omits this
  // field: observing a live fence must never rewrite or claim it.
  delivery_storage_fenced?: boolean;
  // Local-only signal that the latest queue mutation could not be verified in
  // browser storage. The per-item serializer omits it; UI and drain logic must
  // keep the item blocked until an explicit retry persists the exact payload.
  delivery_storage_failed?: boolean;
  // Durable browser-only generation fence. Every per-item write stamps the
  // reset epoch it observed before writing; a different current epoch makes
  // the record stale and ineligible for delivery.
  delivery_storage_epoch?: string;
  // Durable browser-only immutable revision. Every mutation writes a new
  // physical localStorage key before retiring the previous revision, so a
  // concurrent edit or submitting fence cannot be erased by stale cleanup.
  delivery_storage_revision?: string;
  // Local-only fingerprint of the raw durable payload behind a conflict
  // projection. It lets an explicit Remove target that exact payload even
  // though the UI-facing delivery_state is changed to reconcile_required.
  delivery_storage_source_fingerprint?: string;
  // Local-only reason for a browser-queue reconciliation projection. A ready
  // same-id payload found behind a different exact deletion tombstone cannot
  // reuse that id; it must be removed and submitted again with a fresh id.
  delivery_storage_conflict?: "ready_replacement";
  execution_mode: ChatExecutionMode;
  tools_enabled: boolean;
  provider_filter: ProviderFilter;
  model: string;
  workspace: string;
  system_prompt: string;
  agent_id: string;
  created_at: string;
};

// Attachment drafts stay browser-memory-only until submit. Persisting File
// objects into the queued-message localStorage format would imply a
// durability guarantee the composer cannot provide.
export type PendingChatAttachment = {
  id: string;
  file: File;
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

export function isCanonicalQueuedChatStorageEpoch(value: unknown): value is string {
  return typeof value === "string" && value.length > 0 && value === value.trim();
}

// Structural guard for the queued-chat-messages localStorage payload.
// The previous read helper massaged each missing field into an empty
// string and re-persisted the half-zeroed record; the new contract
// drops malformed *items* (preserving the rest of the queue) and
// rejects the *whole array* if it isn't an array — `usePersistedState`
// then wipes the key on the array-shape failure. Per-item filtering
// is preserved because losing the entire queue to one corrupt entry
// is worse than losing the bad entry.
export function parseQueuedChatMessageList(
  parsed: unknown,
  { preserveSubmitting = false }: { preserveSubmitting?: boolean } = {},
): QueuedChatMessage[] | null {
  if (!Array.isArray(parsed)) return null;
  return parsed.flatMap((item): QueuedChatMessage[] => {
    if (!item || typeof item !== "object") return [];
    const candidate = item as Record<string, unknown>;
    const id = typeof candidate.id === "string" ? candidate.id : "";
    const sessionID = typeof candidate.session_id === "string" ? candidate.session_id : "";
    if (sessionID !== sessionID.trim()) return [];
    const hasProjectID = Object.prototype.hasOwnProperty.call(candidate, "project_id");
    if (hasProjectID && typeof candidate.project_id !== "string") return [];
    const rawProjectID = hasProjectID ? (candidate.project_id as string) : "";
    // The literal empty string is the trusted project-free sentinel. Trimming
    // malformed or padded ownership into that sentinel could let a persisted
    // prompt bypass a project tombstone, so non-canonical values fail parsing.
    if (hasProjectID && rawProjectID !== rawProjectID.trim()) return [];
    const projectID = rawProjectID;
    const content = typeof candidate.content === "string" ? candidate.content : "";
    if (!id || !sessionID || !content.trim()) return [];
    const hasStorageRevision = Object.prototype.hasOwnProperty.call(
      candidate,
      "delivery_storage_revision",
    );
    const hasStorageEpoch = Object.prototype.hasOwnProperty.call(
      candidate,
      "delivery_storage_epoch",
    );
    const storageEpoch = candidate.delivery_storage_epoch;
    if (hasStorageEpoch && !isCanonicalQueuedChatStorageEpoch(storageEpoch)) return [];
    const storageRevision =
      typeof candidate.delivery_storage_revision === "string"
        ? candidate.delivery_storage_revision
        : "";
    if (hasStorageRevision && (!storageRevision || storageRevision !== storageRevision.trim())) {
      return [];
    }
    const executionMode =
      candidate.execution_mode === "hecate_task" || candidate.execution_mode === "external_agent"
        ? candidate.execution_mode
        : "";
    if (!executionMode) return [];
    const rawDeliveryState = candidate.delivery_state;
    const deliveryState: QueuedChatDeliveryState | undefined =
      rawDeliveryState === "submitting"
        ? preserveSubmitting
          ? "submitting"
          : "reconcile_required"
        : rawDeliveryState === "retryable" || rawDeliveryState === "reconcile_required"
          ? rawDeliveryState
          : candidate.retry_required === true || typeof rawDeliveryState === "string"
            ? "reconcile_required"
            : undefined;
    const rawDeliveryBaselineMessageIDs = candidate.delivery_baseline_message_ids;
    const deliveryBaselineMessageIDs: string[] | undefined = Array.isArray(
      rawDeliveryBaselineMessageIDs,
    )
      ? [
          ...new Set(
            rawDeliveryBaselineMessageIDs.filter(
              (messageID): messageID is string =>
                typeof messageID === "string" && Boolean(messageID.trim()),
            ),
          ),
        ]
      : undefined;
    const deliveryErrorCode: QueuedChatDeliveryErrorCode | undefined =
      candidate.delivery_error_code === "chat.client_request_conflict"
        ? candidate.delivery_error_code
        : undefined;
    return [
      {
        id,
        session_id: sessionID,
        ...(hasProjectID ? { project_id: projectID } : {}),
        content,
        ...(deliveryState ? { delivery_state: deliveryState } : {}),
        ...(deliveryBaselineMessageIDs
          ? { delivery_baseline_message_ids: deliveryBaselineMessageIDs }
          : {}),
        ...(deliveryErrorCode ? { delivery_error_code: deliveryErrorCode } : {}),
        ...(candidate.delivery_idempotency_keyed === true
          ? { delivery_idempotency_keyed: true }
          : {}),
        ...(hasStorageEpoch ? { delivery_storage_epoch: storageEpoch as string } : {}),
        ...(hasStorageRevision ? { delivery_storage_revision: storageRevision } : {}),
        execution_mode: executionMode,
        tools_enabled:
          typeof candidate.tools_enabled === "boolean" ? candidate.tools_enabled : true,
        provider_filter:
          typeof candidate.provider_filter === "string"
            ? (candidate.provider_filter as ProviderFilter)
            : "auto",
        model: typeof candidate.model === "string" ? candidate.model : "",
        workspace: typeof candidate.workspace === "string" ? candidate.workspace : "",
        system_prompt: typeof candidate.system_prompt === "string" ? candidate.system_prompt : "",
        agent_id: typeof candidate.agent_id === "string" ? candidate.agent_id : "",
        created_at:
          typeof candidate.created_at === "string"
            ? candidate.created_at
            : new Date().toISOString(),
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
