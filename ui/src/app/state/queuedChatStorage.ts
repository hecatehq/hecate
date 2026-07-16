import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type Dispatch,
  type SetStateAction,
} from "react";

import { warn as logWarn } from "../../lib/log";
import { sha256Hex } from "../../lib/sha256";
import {
  isCanonicalQueuedChatStorageEpoch,
  parseQueuedChatMessageList,
  queuedChatMessagesStorageKey,
  type QueuedChatMessage,
} from "./_shared";

export const queuedChatMessagesV2MarkerStorageKey = `${queuedChatMessagesStorageKey}.v2`;
export const queuedChatMessageStorageKeyPrefix = `${queuedChatMessagesStorageKey}.item.`;
export const queuedChatMessagesResetEpochStorageKey = `${queuedChatMessagesStorageKey}.resetEpoch`;
export const queuedChatDeletedProjectStorageKeyPrefix = `${queuedChatMessagesStorageKey}.deletedProject.`;
export const queuedChatDeletedSessionStorageKeyPrefix = `${queuedChatMessagesStorageKey}.deletedSession.`;
export const queuedChatMigratedLegacyStorageKeyPrefix = `${queuedChatMessagesStorageKey}.migratedLegacy.`;
export const queuedChatDeletedItemStorageKeyPrefix = `${queuedChatMessagesStorageKey}.deletedItem.`;

const queuedChatMessagesV2MarkerValue = "1";
const queuedChatDeletedProjectMarkerPrefix = "deleted:v2:";
const queuedChatDeletedProjectLegacyMarkerPrefix = "deleted:";
const queuedChatDeletedSessionMarkerPrefix = "deleted:v1:";
const queuedChatMigratedLegacyMarkerPrefix = "sha256:";
const queuedChatDeletedItemMarkerPrefix = "deleted:v1:";

type StoredSnapshot = Map<string, QueuedChatMessage>;
type StoredQueuedChatRecord = {
  message: QueuedChatMessage;
  storageEpoch?: string;
  storageRevision?: string;
};
type StaleCleanupFailures = Map<string, StoredQueuedChatRecord>;
type PendingRecord = QueuedChatMessage | null;
type StoredSnapshotRead = {
  messages: StoredSnapshot;
  complete: boolean;
};

type ResetEpochRead = {
  epoch: string;
  complete: boolean;
  present: boolean;
};

type InitializedQueue = {
  messages: QueuedChatMessage[];
  durable: StoredSnapshot;
  pending: Map<string, PendingRecord>;
  projectedSubmittingIDs: Set<string>;
  staleCleanupFailures: StaleCleanupFailures;
  resetEpoch: string;
  resetEpochKnown: boolean;
};

function browserStorage(): Storage | null {
  if (typeof window === "undefined") return null;
  try {
    return window.localStorage;
  } catch (error) {
    logWarn("queued chat storage: localStorage is unavailable:", error);
    return null;
  }
}

export function queuedChatMessageStorageKey(
  id: string,
  resetEpoch = "0",
  storageRevision?: string,
): string {
  const revision = storageRevision ? `${encodeURIComponent(storageRevision)}:` : "";
  return `${queuedChatMessageStorageKeyPrefix}${encodeURIComponent(
    resetEpoch,
  )}:${revision}${encodeURIComponent(id)}`;
}

export function queuedChatDeletedProjectStorageKey(projectID: string, resetEpoch = "0"): string {
  return `${queuedChatDeletedProjectStorageKeyPrefix}${encodeURIComponent(
    resetEpoch,
  )}:${encodeURIComponent(projectID)}`;
}

export function queuedChatDeletedSessionStorageKey(sessionID: string, resetEpoch = "0"): string {
  return `${queuedChatDeletedSessionStorageKeyPrefix}${encodeURIComponent(
    resetEpoch,
  )}:${encodeURIComponent(sessionID)}`;
}

function legacyQueuedChatMessageStorageKey(id: string): string {
  return `${queuedChatMessageStorageKeyPrefix}${encodeURIComponent(id)}`;
}

function legacyQueuedChatDeletedProjectStorageKey(projectID: string): string {
  return `${queuedChatDeletedProjectStorageKeyPrefix}${encodeURIComponent(projectID)}`;
}

function queuedChatMigratedLegacyStorageKey(id: string, resetEpoch = "0"): string {
  return `${queuedChatMigratedLegacyStorageKeyPrefix}${encodeURIComponent(
    resetEpoch,
  )}:${encodeURIComponent(id)}`;
}

function queuedChatDeletedItemStorageKey(
  id: string,
  resetEpoch: string,
  fingerprint: string,
): string {
  return `${queuedChatDeletedItemStorageKeyPrefix}${encodeURIComponent(
    resetEpoch,
  )}:${fingerprint}:${encodeURIComponent(id)}`;
}

function queuedChatStorageIdentityFromKey(
  key: string,
  prefix: string,
): { id: string; storageEpoch?: string } | null {
  if (!key.startsWith(prefix)) return null;
  const suffix = key.slice(prefix.length);
  const separator = suffix.indexOf(":");
  try {
    if (separator < 0) return { id: decodeURIComponent(suffix) };
    const storageEpoch = decodeURIComponent(suffix.slice(0, separator));
    const id = decodeURIComponent(suffix.slice(separator + 1));
    return isCanonicalQueuedChatStorageEpoch(storageEpoch) && id ? { storageEpoch, id } : null;
  } catch {
    return null;
  }
}

function queuedChatMessageIdentityFromStorageKey(
  key: string,
): { id: string; storageEpoch?: string; storageRevision?: string } | null {
  if (!key.startsWith(queuedChatMessageStorageKeyPrefix)) return null;
  const suffix = key.slice(queuedChatMessageStorageKeyPrefix.length);
  const firstSeparator = suffix.indexOf(":");
  const secondSeparator = firstSeparator < 0 ? -1 : suffix.indexOf(":", firstSeparator + 1);
  try {
    if (firstSeparator < 0) return { id: decodeURIComponent(suffix) };
    if (secondSeparator < 0) {
      const storageEpoch = decodeURIComponent(suffix.slice(0, firstSeparator));
      const id = decodeURIComponent(suffix.slice(firstSeparator + 1));
      return isCanonicalQueuedChatStorageEpoch(storageEpoch) && id ? { storageEpoch, id } : null;
    }
    const storageRevision = decodeURIComponent(suffix.slice(firstSeparator + 1, secondSeparator));
    const storageEpoch = decodeURIComponent(suffix.slice(0, firstSeparator));
    const id = decodeURIComponent(suffix.slice(secondSeparator + 1));
    if (
      !isCanonicalQueuedChatStorageEpoch(storageEpoch) ||
      !storageRevision ||
      storageRevision !== storageRevision.trim() ||
      !id
    ) {
      return null;
    }
    return { storageEpoch, storageRevision, id };
  } catch {
    return null;
  }
}

type QueuedChatDeletedItemIdentity = {
  id: string;
  storageEpoch: string;
  fingerprint: string;
};

function queuedChatDeletedItemIdentityFromStorageKey(
  key: string,
): QueuedChatDeletedItemIdentity | null {
  if (!key.startsWith(queuedChatDeletedItemStorageKeyPrefix)) return null;
  const suffix = key.slice(queuedChatDeletedItemStorageKeyPrefix.length);
  const firstSeparator = suffix.indexOf(":");
  const secondSeparator = firstSeparator < 0 ? -1 : suffix.indexOf(":", firstSeparator + 1);
  if (firstSeparator < 1 || secondSeparator < firstSeparator + 2) return null;
  const fingerprint = suffix.slice(firstSeparator + 1, secondSeparator);
  if (!/^[0-9a-f]{64}$/.test(fingerprint)) return null;
  try {
    const identity = {
      storageEpoch: decodeURIComponent(suffix.slice(0, firstSeparator)),
      fingerprint,
      id: decodeURIComponent(suffix.slice(secondSeparator + 1)),
    };
    return isCanonicalQueuedChatStorageEpoch(identity.storageEpoch) && identity.id
      ? identity
      : null;
  } catch {
    return null;
  }
}

function queuedChatDeletedProjectIdentityFromStorageKey(
  key: string,
): { id: string; storageEpoch?: string } | null {
  return queuedChatStorageIdentityFromKey(key, queuedChatDeletedProjectStorageKeyPrefix);
}

function queuedChatDeletedSessionIdentityFromStorageKey(
  key: string,
): { id: string; storageEpoch?: string } | null {
  return queuedChatStorageIdentityFromKey(key, queuedChatDeletedSessionStorageKeyPrefix);
}

function queuedChatMessageStorageKeyForEpoch(
  id: string,
  storageEpoch?: string,
  storageRevision?: string,
): string {
  return storageEpoch === undefined
    ? legacyQueuedChatMessageStorageKey(id)
    : queuedChatMessageStorageKey(id, storageEpoch, storageRevision);
}

function newQueuedChatStorageRevision(): string {
  try {
    if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
      return crypto.randomUUID();
    }
  } catch {
    // Fall through to a browser-local uniqueness token.
  }
  return `${Date.now()}-${Math.random().toString(36).slice(2)}-${Math.random()
    .toString(36)
    .slice(2)}`;
}

function queuedChatLegacyFingerprint(message: QueuedChatMessage): string {
  const {
    delivery_storage_epoch: _storageEpoch,
    delivery_storage_revision: _storageRevision,
    ...payload
  } = durableQueuedChatMessage(message);
  return sha256Hex(JSON.stringify(payload, Object.keys(payload).sort()));
}

function queuedChatMigratedLegacyMarker(
  storage: Storage,
  id: string,
  resetEpoch: string,
): { fingerprint: string | null; complete: boolean } {
  const read = readStorageItemWithStatus(
    storage,
    queuedChatMigratedLegacyStorageKey(id, resetEpoch),
  );
  if (!read.complete) return { fingerprint: null, complete: false };
  if (read.value === null) return { fingerprint: null, complete: true };
  const fingerprint = read.value.startsWith(queuedChatMigratedLegacyMarkerPrefix)
    ? read.value.slice(queuedChatMigratedLegacyMarkerPrefix.length)
    : "";
  return /^[0-9a-f]{64}$/.test(fingerprint)
    ? {
        fingerprint,
        complete: true,
      }
    : { fingerprint: null, complete: false };
}

function writeQueuedChatMigratedLegacyMarker(
  storage: Storage,
  message: QueuedChatMessage,
  resetEpoch: string,
): boolean {
  const key = queuedChatMigratedLegacyStorageKey(message.id, resetEpoch);
  const marker = `${queuedChatMigratedLegacyMarkerPrefix}${queuedChatLegacyFingerprint(message)}`;
  try {
    storage.setItem(key, marker);
    return storage.getItem(key) === marker;
  } catch (error) {
    logWarn(`queued chat storage: legacy migration marker failed for ${key}:`, error);
    return false;
  }
}

function durableQueuedChatMessage(
  message: QueuedChatMessage,
  storageEpoch?: string,
): QueuedChatMessage {
  const {
    delivery_storage_fenced: _localProjection,
    delivery_storage_failed: _localFailure,
    delivery_storage_source_fingerprint: _sourceFingerprint,
    delivery_storage_conflict: _storageConflict,
    ...durable
  } = message;
  return storageEpoch === undefined
    ? durable
    : { ...durable, delivery_storage_epoch: storageEpoch };
}

function storageFailedProjection(message: QueuedChatMessage): QueuedChatMessage {
  const sourceFingerprint = message.delivery_storage_source_fingerprint;
  const storageConflict = message.delivery_storage_conflict;
  const durable = durableQueuedChatMessage(message);
  return {
    ...durable,
    delivery_state:
      durable.delivery_state === "submitting" || durable.delivery_state === "reconcile_required"
        ? "reconcile_required"
        : "retryable",
    delivery_storage_failed: true,
    ...(sourceFingerprint ? { delivery_storage_source_fingerprint: sourceFingerprint } : {}),
    ...(storageConflict ? { delivery_storage_conflict: storageConflict } : {}),
  };
}

function cleanupFailedProjection(message: QueuedChatMessage): QueuedChatMessage {
  const sourceFingerprint = message.delivery_storage_source_fingerprint;
  const storageConflict = message.delivery_storage_conflict;
  const fenced =
    message.delivery_state === "submitting" || message.delivery_storage_fenced === true;
  return {
    ...durableQueuedChatMessage(message),
    delivery_state: "reconcile_required",
    delivery_storage_failed: true,
    ...(fenced ? { delivery_storage_fenced: true } : {}),
    ...(sourceFingerprint ? { delivery_storage_source_fingerprint: sourceFingerprint } : {}),
    ...(storageConflict ? { delivery_storage_conflict: storageConflict } : {}),
  };
}

function staleCleanupBlockedMessages(
  messages: Iterable<QueuedChatMessage>,
  failures: StaleCleanupFailures,
): QueuedChatMessage[] {
  const source = [...messages];
  const blocked = new Map(source.map((message) => [message.id, message]));
  const fencedIDs = new Set(
    source
      .filter(
        (message) =>
          message.delivery_state === "submitting" || message.delivery_storage_fenced === true,
      )
      .map((message) => message.id),
  );
  for (const failure of failures.values()) {
    if (
      failure.message.delivery_state === "submitting" ||
      failure.message.delivery_storage_fenced === true
    ) {
      fencedIDs.add(failure.message.id);
    }
    if (!blocked.has(failure.message.id)) blocked.set(failure.message.id, failure.message);
  }
  return sortQueuedChatMessages(
    [...blocked.values()].map((message) => {
      const projected = cleanupFailedProjection(message);
      return fencedIDs.has(message.id)
        ? { ...projected, delivery_storage_fenced: true }
        : projected;
    }),
  );
}

function canonicalQueuedChatMessage(message: QueuedChatMessage): string {
  const durable = durableQueuedChatMessage(message);
  return JSON.stringify(durable, Object.keys(durable).sort());
}

function sameQueuedChatMessage(
  left: QueuedChatMessage | undefined,
  right: QueuedChatMessage | undefined,
): boolean {
  if (!left || !right) return left === right;
  return canonicalQueuedChatMessage(left) === canonicalQueuedChatMessage(right);
}

function sameQueuedChatPayload(
  left: QueuedChatMessage | undefined,
  right: QueuedChatMessage | undefined,
): boolean {
  if (!left || !right) return left === right;
  const { delivery_storage_revision: _leftRevision, ...leftPayload } =
    durableQueuedChatMessage(left);
  const { delivery_storage_revision: _rightRevision, ...rightPayload } =
    durableQueuedChatMessage(right);
  return (
    JSON.stringify(leftPayload, Object.keys(leftPayload).sort()) ===
    JSON.stringify(rightPayload, Object.keys(rightPayload).sort())
  );
}

function sameLocalQueuedChatMessage(
  left: QueuedChatMessage | undefined,
  right: QueuedChatMessage | undefined,
): boolean {
  if (!left || !right) return left === right;
  return (
    JSON.stringify(left, Object.keys(left).sort()) ===
    JSON.stringify(right, Object.keys(right).sort())
  );
}

export function sortQueuedChatMessages(messages: Iterable<QueuedChatMessage>): QueuedChatMessage[] {
  return [...messages].sort(
    (left, right) =>
      left.created_at.localeCompare(right.created_at) || left.id.localeCompare(right.id),
  );
}

function parseQueuedChatMessageRecord(
  raw: string | null,
  preserveSubmitting: boolean,
): QueuedChatMessage | null {
  if (!raw) return null;
  try {
    const parsed = parseQueuedChatMessageList([JSON.parse(raw)], { preserveSubmitting });
    return parsed?.length === 1 ? parsed[0] : null;
  } catch {
    return null;
  }
}

type QueuedChatMessageRecordRead =
  | { status: "present"; message: QueuedChatMessage }
  | { status: "absent" }
  | { status: "unknown" };

function readStorageItemWithStatus(
  storage: Storage,
  key: string,
): { value: string | null; complete: boolean } {
  try {
    return { value: storage.getItem(key), complete: true };
  } catch (error) {
    logWarn(`queued chat storage: read failed for ${key}:`, error);
    return { value: null, complete: false };
  }
}

function readQueuedChatMessageRecordWithStatus(
  storage: Storage,
  id: string,
  storageEpoch: string | undefined,
  storageRevision?: string,
): QueuedChatMessageRecordRead {
  const key = queuedChatMessageStorageKeyForEpoch(id, storageEpoch, storageRevision);
  const read = readStorageItemWithStatus(storage, key);
  if (!read.complete) return { status: "unknown" };
  if (read.value === null) return { status: "absent" };
  const message = parseQueuedChatMessageRecord(read.value, true);
  return message?.id === id &&
    (storageEpoch === undefined || message.delivery_storage_epoch === storageEpoch) &&
    message.delivery_storage_revision === storageRevision
    ? { status: "present", message }
    : { status: "unknown" };
}

function storageKeysWithPrefix(
  storage: Storage,
  prefix: string,
): { keys: string[]; complete: boolean } {
  const keys: string[] = [];
  try {
    for (let index = 0; index < storage.length; index += 1) {
      const key = storage.key(index);
      if (key?.startsWith(prefix)) keys.push(key);
    }
  } catch (error) {
    logWarn("queued chat storage: key scan failed:", error);
    return { keys, complete: false };
  }
  return { keys, complete: true };
}

function queuedChatStorageKeys(storage: Storage): { keys: string[]; complete: boolean } {
  return storageKeysWithPrefix(storage, queuedChatMessageStorageKeyPrefix);
}

function queuedChatDeletedProjectStorageKeys(storage: Storage): {
  keys: string[];
  complete: boolean;
} {
  return storageKeysWithPrefix(storage, queuedChatDeletedProjectStorageKeyPrefix);
}

function queuedChatDeletedSessionStorageKeys(storage: Storage): {
  keys: string[];
  complete: boolean;
} {
  return storageKeysWithPrefix(storage, queuedChatDeletedSessionStorageKeyPrefix);
}

function queuedChatMigratedLegacyStorageKeys(storage: Storage): {
  keys: string[];
  complete: boolean;
} {
  return storageKeysWithPrefix(storage, queuedChatMigratedLegacyStorageKeyPrefix);
}

function queuedChatDeletedItemStorageKeys(storage: Storage): {
  keys: string[];
  complete: boolean;
} {
  return storageKeysWithPrefix(storage, queuedChatDeletedItemStorageKeyPrefix);
}

function readQueuedChatMessagesSnapshot(
  storage: Storage,
  { preserveSubmitting = true }: { preserveSubmitting?: boolean } = {},
): { records: StoredQueuedChatRecord[]; complete: boolean } {
  const records: StoredQueuedChatRecord[] = [];
  const scan = queuedChatStorageKeys(storage);
  let complete = scan.complete;
  for (const key of scan.keys) {
    const identity = queuedChatMessageIdentityFromStorageKey(key);
    if (
      !identity?.id ||
      queuedChatMessageStorageKeyForEpoch(
        identity.id,
        identity.storageEpoch,
        identity.storageRevision,
      ) !== key
    ) {
      complete = false;
      continue;
    }
    let raw: string | null = null;
    try {
      raw = storage.getItem(key);
    } catch (error) {
      logWarn(`queued chat storage: read failed for ${key}:`, error);
      complete = false;
      continue;
    }
    const message = parseQueuedChatMessageRecord(raw, preserveSubmitting);
    if (
      !message ||
      message.id !== identity.id ||
      (identity.storageEpoch !== undefined &&
        message.delivery_storage_epoch !== identity.storageEpoch) ||
      message.delivery_storage_revision !== identity.storageRevision
    ) {
      complete = false;
      continue;
    }
    records.push({
      message,
      storageEpoch: identity.storageEpoch,
      storageRevision: identity.storageRevision,
    });
  }
  return { records, complete };
}

export function readQueuedChatMessagesFromStorage(
  storage: Storage,
  options: { preserveSubmitting?: boolean } = {},
): QueuedChatMessage[] {
  const read = readStoredSnapshotWithStatus(storage);
  const messages = [...read.messages.values()];
  if (options.preserveSubmitting !== false) return sortQueuedChatMessages(messages);
  return sortQueuedChatMessages(
    messages.map((message) =>
      message.delivery_state === "submitting"
        ? { ...message, delivery_state: "reconcile_required" as const }
        : message,
    ),
  );
}

function readStoredSnapshotWithStatus(
  storage: Storage,
  expectedResetEpoch?: ResetEpochRead | null,
): StoredSnapshotRead {
  const read = readQueuedChatMessagesSnapshot(storage, { preserveSubmitting: true });
  const messages = new Map<string, QueuedChatMessage>();
  const messageAuthorities = new Map<string, number>();
  let complete = read.complete;
  const epochRead = expectedResetEpoch === undefined ? readResetEpoch(storage) : expectedResetEpoch;
  const mergeRecord = (record: StoredQueuedChatRecord, blocked: boolean) => {
    const { message } = record;
    const authority = record.storageRevision ? 2 : record.storageEpoch === undefined ? 0 : 1;
    const existing = messages.get(message.id);
    const candidate = blocked ? cleanupFailedProjection(message) : message;
    if (!existing) {
      messages.set(message.id, candidate);
      messageAuthorities.set(message.id, authority);
      return;
    }
    if (!sameQueuedChatMessage(existing, candidate)) {
      const existingAuthority = messageAuthorities.get(message.id) ?? 0;
      const preferred =
        authority === existingAuthority
          ? undefined
          : authority > existingAuthority
            ? candidate
            : existing;
      messages.set(
        message.id,
        cleanupFailedProjection(resolveMigrationConflict(existing, candidate, preferred)),
      );
      messageAuthorities.set(message.id, Math.max(existingAuthority, authority));
      complete = false;
    }
  };
  if (epochRead === null) {
    for (const record of read.records) mergeRecord(record, true);
    return { messages, complete };
  }
  if (
    !epochRead.complete ||
    (!epochRead.present &&
      read.records.some(
        (record) =>
          (record.storageEpoch !== undefined && record.storageEpoch !== "0") ||
          (record.message.delivery_storage_epoch !== undefined &&
            record.message.delivery_storage_epoch !== "0"),
      ))
  ) {
    for (const record of read.records) mergeRecord(record, true);
    return { messages, complete: false };
  }
  const epoch = epochRead.epoch;
  const deletedItemsRead = readQueuedChatDeletedItemTombstones(storage, epoch);
  if (!deletedItemsRead.complete) complete = false;
  for (const record of read.records) {
    if (record.storageEpoch !== epoch && !(record.storageEpoch === undefined && epoch === "0")) {
      continue;
    }
    const itemFingerprint = queuedChatLegacyFingerprint(record.message);
    if (
      deletedItemsRead.deleted.has(queuedChatDeletedItemToken(record.message.id, itemFingerprint))
    ) {
      if (record.storageRevision) {
        mergeRecord(
          {
            ...record,
            message: {
              ...cleanupFailedProjection(record.message),
              delivery_storage_source_fingerprint: itemFingerprint,
            },
          },
          false,
        );
      }
      continue;
    }
    if (deletedItemsRead.ids.has(record.message.id)) {
      const conflictedMessage =
        record.message.delivery_state === "submitting"
          ? {
              ...record.message,
              delivery_storage_source_fingerprint: itemFingerprint,
            }
          : conflictProjection(record.message, itemFingerprint);
      mergeRecord(
        {
          ...record,
          message: conflictedMessage,
        },
        false,
      );
      continue;
    }
    if (record.storageRevision === undefined) {
      const marker = queuedChatMigratedLegacyMarker(storage, record.message.id, epoch);
      if (!marker.complete) complete = false;
      if (marker.fingerprint === queuedChatLegacyFingerprint(record.message)) continue;
    }
    const generationMatches =
      record.message.delivery_storage_epoch === epoch ||
      (record.storageEpoch === undefined &&
        epoch === "0" &&
        record.message.delivery_storage_epoch === undefined);
    mergeRecord(record, !generationMatches);
    if (!generationMatches) complete = false;
  }
  return {
    messages,
    complete,
  };
}

function writeQueuedChatMessage(
  storage: Storage,
  message: QueuedChatMessage,
  resetEpoch: string,
  previous?: QueuedChatMessage,
): QueuedChatMessage | null {
  const expectedResetEpoch = { epoch: resetEpoch, complete: true, present: true };
  if (queuedChatItemDeletionStatus(storage, message, expectedResetEpoch) !== "active") {
    return null;
  }
  for (let attempt = 0; attempt < 3; attempt += 1) {
    const revision = newQueuedChatStorageRevision();
    const durable = {
      ...durableQueuedChatMessage(message, resetEpoch),
      delivery_storage_revision: revision,
    };
    const key = queuedChatMessageStorageKey(durable.id, resetEpoch, revision);
    const serialized = JSON.stringify(durable);
    try {
      // Revision keys are immutable. A collision is retried rather than
      // overwriting another tab's durable payload.
      if (storage.getItem(key) !== null) continue;
      storage.setItem(key, serialized);
      if (storage.getItem(key) !== serialized) continue;
    } catch (error) {
      logWarn(`queued chat storage: write failed for ${key}:`, error);
      return null;
    }

    const deletionStatus = queuedChatItemDeletionStatus(storage, durable, expectedResetEpoch);
    if (deletionStatus !== "active") {
      if (deletionStatus === "deleted") {
        cleanupDeletedQueuedChatItemArtifacts(storage, durable, resetEpoch);
      }
      return null;
    }

    if (!previous) return durable;
    if (!previous.delivery_storage_revision) {
      return writeQueuedChatMigratedLegacyMarker(storage, previous, resetEpoch) ? durable : null;
    }
    const previousKey = queuedChatMessageStorageKeyForEpoch(
      previous.id,
      previous.delivery_storage_epoch,
      previous.delivery_storage_revision,
    );
    try {
      storage.removeItem(previousKey);
      if (storage.getItem(previousKey) === null) return durable;
    } catch (error) {
      logWarn(`queued chat storage: prior revision cleanup failed for ${previousKey}:`, error);
    }

    // Keep the new immutable revision when prior cleanup is unknown. If the
    // old revision also survived, the duplicate-read path quarantines both;
    // deleting the new record here could lose the only durable copy.
    return null;
  }
  return null;
}

function removeQueuedChatMessageWhere(
  storage: Storage,
  id: string,
  storageEpoch: string | undefined,
  storageRevision: string | undefined,
  predicate: (message: QueuedChatMessage) => boolean,
  logicalDelete = false,
): { message?: QueuedChatMessage; complete: boolean } {
  const key = queuedChatMessageStorageKeyForEpoch(id, storageEpoch, storageRevision);
  for (let attempt = 0; attempt < 3; attempt += 1) {
    const before = readStorageItemWithStatus(storage, key);
    if (!before.complete) return { complete: false };
    if (before.value === null) return { complete: true };
    const candidate = parseQueuedChatMessageRecord(before.value, true);
    if (!candidate || candidate.id !== id) return { complete: false };
    if (!predicate(candidate)) return { message: candidate, complete: true };

    if (logicalDelete) {
      const epochRead = readResetEpoch(storage);
      const belongsToCurrentGeneration =
        epochRead.complete &&
        (storageEpoch === epochRead.epoch ||
          (storageEpoch === undefined && epochRead.epoch === "0"));
      if (!belongsToCurrentGeneration) return { message: candidate, complete: false };
      if (!tombstoneQueuedChatItem(storage, candidate, epochRead.epoch)) {
        return { message: candidate, complete: false };
      }
      const epochAfterTombstone = readResetEpoch(storage);
      if (
        !epochAfterTombstone.complete ||
        epochAfterTombstone.epoch !== epochRead.epoch ||
        queuedChatItemDeletionStatus(storage, candidate, epochAfterTombstone) !== "deleted"
      ) {
        return { message: candidate, complete: false };
      }
      return cleanupDeletedQueuedChatItemArtifacts(storage, candidate, epochRead.epoch)
        ? { complete: true }
        : { message: candidate, complete: false };
    }

    // Current writers never replace this immutable revision key; concurrent
    // edits and submitting fences use a different physical key. Re-read here
    // still gives legacy revisionless records a best-effort conflict check.
    const verified = readStorageItemWithStatus(storage, key);
    if (!verified.complete) return { message: candidate, complete: false };
    if (verified.value !== before.value) continue;
    try {
      storage.removeItem(key);
    } catch (error) {
      logWarn(`queued chat storage: conditional remove failed for ${key}:`, error);
      return { message: candidate, complete: false };
    }

    const after = readStorageItemWithStatus(storage, key);
    if (!after.complete) return { message: candidate, complete: false };
    if (after.value === null) return { complete: true };
    if (after.value === before.value) return { message: candidate, complete: false };
  }

  const latest = readQueuedChatMessageRecordWithStatus(storage, id, storageEpoch, storageRevision);
  if (latest.status === "absent") return { complete: true };
  if (latest.status === "present" && !predicate(latest.message)) {
    return { message: latest.message, complete: true };
  }
  return {
    ...(latest.status === "present" ? { message: latest.message } : {}),
    complete: false,
  };
}

function revokeQueuedChatMessage(storage: Storage, expected: QueuedChatMessage): boolean {
  const cleanup = removeQueuedChatMessageWhere(
    storage,
    expected.id,
    expected.delivery_storage_epoch,
    expected.delivery_storage_revision,
    (candidate) => sameQueuedChatMessage(candidate, expected),
  );
  if (cleanup.complete) return true;
  if (!cleanup.message || !sameQueuedChatMessage(cleanup.message, expected)) return false;
  const epoch = cleanup.message.delivery_storage_epoch;
  if (epoch === undefined) return false;
  return Boolean(
    writeQueuedChatMessage(
      storage,
      {
        ...cleanup.message,
        delivery_state: "reconcile_required",
      },
      epoch,
      cleanup.message,
    ),
  );
}

function projectStoredSubmitting(message: QueuedChatMessage): QueuedChatMessage {
  const storageFailed = message.delivery_storage_failed === true;
  const sourceFingerprint = message.delivery_storage_source_fingerprint;
  const storageConflict = message.delivery_storage_conflict;
  const durable = durableQueuedChatMessage(message);
  let projected: QueuedChatMessage =
    durable.delivery_state === "submitting"
      ? {
          ...durable,
          delivery_state: "reconcile_required",
          delivery_storage_fenced: true,
        }
      : durable;
  if (sourceFingerprint) {
    projected = { ...projected, delivery_storage_source_fingerprint: sourceFingerprint };
  }
  if (storageConflict) projected = { ...projected, delivery_storage_conflict: storageConflict };
  return storageFailed ? { ...projected, delivery_storage_failed: true } : projected;
}

function conflictProjection(
  stored: QueuedChatMessage,
  sourceFingerprint = stored.delivery_storage_source_fingerprint,
): QueuedChatMessage {
  if (stored.delivery_state === "submitting") {
    const projected = projectStoredSubmitting(stored);
    return sourceFingerprint
      ? {
          ...projected,
          delivery_storage_source_fingerprint: sourceFingerprint,
        }
      : projected;
  }
  const projected: QueuedChatMessage = {
    ...durableQueuedChatMessage(stored),
    delivery_state: "reconcile_required",
    ...(sourceFingerprint ? { delivery_storage_source_fingerprint: sourceFingerprint } : {}),
    ...(sourceFingerprint ? { delivery_storage_conflict: "ready_replacement" as const } : {}),
  };
  return stored.delivery_storage_failed
    ? { ...projected, delivery_storage_failed: true }
    : projected;
}

function sameQueuedChatRemovalTarget(
  candidate: QueuedChatMessage,
  expected: QueuedChatMessage,
): boolean {
  const sourceFingerprint = expected.delivery_storage_source_fingerprint;
  return sourceFingerprint
    ? candidate.id === expected.id && queuedChatLegacyFingerprint(candidate) === sourceFingerprint
    : sameQueuedChatMessage(candidate, expected);
}

function resolveMigrationConflict(
  left: QueuedChatMessage,
  right: QueuedChatMessage,
  preferred?: QueuedChatMessage,
): QueuedChatMessage {
  if (sameQueuedChatMessage(left, right)) return durableQueuedChatMessage(left);
  if (left.delivery_state === "submitting") return durableQueuedChatMessage(left);
  if (right.delivery_state === "submitting") return durableQueuedChatMessage(right);
  const winner =
    preferred ??
    (canonicalQueuedChatMessage(left) <= canonicalQueuedChatMessage(right) ? left : right);
  return {
    ...durableQueuedChatMessage(winner),
    delivery_state: "reconcile_required",
  };
}

function safeStorageItem(storage: Storage, key: string): string | null {
  try {
    return storage.getItem(key);
  } catch (error) {
    logWarn(`queued chat storage: read failed for ${key}:`, error);
    return null;
  }
}

type QueuedChatItemDeletionStatus = "active" | "deleted" | "conflict" | "unknown";
type QueuedChatProjectDeletionStatus = "active" | "deleted" | "unknown";
type QueuedChatSessionDeletionStatus = "active" | "deleted" | "unknown";

export type QueuedChatSessionDeletionFenceStatus = "absent" | "present" | "unknown";

function queuedChatDeletedItemMarker(resetEpoch: string, fingerprint: string): string {
  return `${queuedChatDeletedItemMarkerPrefix}${encodeURIComponent(resetEpoch)}:${fingerprint}`;
}

function queuedChatDeletedItemToken(id: string, fingerprint: string): string {
  return `${fingerprint}\u0000${id}`;
}

function readQueuedChatDeletedItemTombstones(
  storage: Storage,
  resetEpoch: string,
): { deleted: Set<string>; ids: Set<string>; complete: boolean } {
  const scan = queuedChatDeletedItemStorageKeys(storage);
  const deleted = new Set<string>();
  const ids = new Set<string>();
  let complete = scan.complete;
  for (const key of scan.keys) {
    const identity = queuedChatDeletedItemIdentityFromStorageKey(key);
    const canonicalKey = identity
      ? queuedChatDeletedItemStorageKey(identity.id, identity.storageEpoch, identity.fingerprint)
      : null;
    if (!identity || canonicalKey !== key) {
      complete = false;
      continue;
    }
    const read = readStorageItemWithStatus(storage, key);
    if (
      !read.complete ||
      read.value !== queuedChatDeletedItemMarker(identity.storageEpoch, identity.fingerprint)
    ) {
      complete = false;
      continue;
    }
    if (identity.storageEpoch === resetEpoch) {
      ids.add(identity.id);
      deleted.add(queuedChatDeletedItemToken(identity.id, identity.fingerprint));
    }
  }
  return { deleted, ids, complete };
}

function queuedChatItemDeletionStatus(
  storage: Storage,
  message: QueuedChatMessage,
  expectedResetEpoch: ResetEpochRead,
): QueuedChatItemDeletionStatus {
  if (!expectedResetEpoch.complete) return "unknown";
  const fingerprint = queuedChatLegacyFingerprint(message);
  const tombstones = readQueuedChatDeletedItemTombstones(storage, expectedResetEpoch.epoch);
  if (!tombstones.complete) return "unknown";
  if (tombstones.deleted.has(queuedChatDeletedItemToken(message.id, fingerprint))) {
    return "deleted";
  }
  return tombstones.ids.has(message.id) ? "conflict" : "active";
}

function tombstoneQueuedChatItem(
  storage: Storage,
  message: QueuedChatMessage,
  resetEpoch: string,
): boolean {
  const fingerprint = queuedChatLegacyFingerprint(message);
  const key = queuedChatDeletedItemStorageKey(message.id, resetEpoch, fingerprint);
  const marker = queuedChatDeletedItemMarker(resetEpoch, fingerprint);
  try {
    const existing = storage.getItem(key);
    if (existing !== null) return existing === marker;
    storage.setItem(key, marker);
    return storage.getItem(key) === marker;
  } catch (error) {
    logWarn(`queued chat storage: item tombstone failed for ${key}:`, error);
    return false;
  }
}

function cleanupDeletedQueuedChatItemArtifacts(
  storage: Storage,
  message: QueuedChatMessage,
  resetEpoch: string,
): boolean {
  const fingerprint = queuedChatLegacyFingerprint(message);
  for (let attempt = 0; attempt < 3; attempt += 1) {
    const scan = queuedChatStorageKeys(storage);
    if (!scan.complete) return false;
    let remaining = false;
    for (const key of scan.keys) {
      const identity = queuedChatMessageIdentityFromStorageKey(key);
      if (
        identity?.id !== message.id ||
        (identity.storageEpoch !== undefined && identity.storageEpoch !== resetEpoch)
      ) {
        continue;
      }
      // Legacy and early revisionless keys are mutable. A get/get/remove
      // sequence cannot prove another tab did not replace their payload at the
      // delete boundary, so leave them as tombstone-suppressed shadows.
      if (!identity.storageRevision) continue;
      const read = readStorageItemWithStatus(storage, key);
      if (!read.complete) return false;
      if (read.value === null) continue;
      const candidate = parseQueuedChatMessageRecord(read.value, true);
      if (
        !candidate ||
        candidate.id !== message.id ||
        queuedChatLegacyFingerprint(candidate) !== fingerprint
      ) {
        continue;
      }
      if (!removeStorageItemIfUnchanged(storage, key, read.value)) remaining = true;
      const after = readStorageItemWithStatus(storage, key);
      if (!after.complete) return false;
      if (after.value !== null) remaining = true;
    }
    if (!remaining) return true;
  }
  return false;
}

function parseQueuedChatDeletedProjectEpoch(raw: string): string | null {
  if (raw.startsWith(queuedChatDeletedProjectMarkerPrefix)) {
    const encoded = raw.slice(queuedChatDeletedProjectMarkerPrefix.length).split(":", 1)[0];
    if (!encoded) return null;
    try {
      return decodeURIComponent(encoded);
    } catch {
      return null;
    }
  }
  return raw.startsWith(queuedChatDeletedProjectLegacyMarkerPrefix) &&
    raw.length > queuedChatDeletedProjectLegacyMarkerPrefix.length
    ? "0"
    : null;
}

function removeStorageItemIfUnchanged(storage: Storage, key: string, expected: string): boolean {
  const before = readStorageItemWithStatus(storage, key);
  if (!before.complete) return false;
  if (before.value === null || before.value !== expected) return true;
  const verified = readStorageItemWithStatus(storage, key);
  if (!verified.complete) return false;
  if (verified.value !== expected) return true;
  try {
    storage.removeItem(key);
  } catch (error) {
    logWarn(`queued chat storage: conditional remove failed for ${key}:`, error);
    return false;
  }
  const after = readStorageItemWithStatus(storage, key);
  return after.complete && after.value !== expected;
}

function queuedChatProjectDeletionStatus(
  storage: Storage,
  projectID: string | undefined,
  expectedResetEpoch?: ResetEpochRead,
): QueuedChatProjectDeletionStatus {
  if (projectID === "") return "active";
  const epochRead = expectedResetEpoch ?? readResetEpoch(storage);
  if (!epochRead.complete) return "unknown";
  const scan = queuedChatDeletedProjectStorageKeys(storage);
  if (!scan.complete) return "unknown";
  let matchingCurrentTombstone = false;
  for (const key of scan.keys) {
    const identity = queuedChatDeletedProjectIdentityFromStorageKey(key);
    if (!identity?.id) return "unknown";
    const canonicalKey =
      identity.storageEpoch === undefined
        ? legacyQueuedChatDeletedProjectStorageKey(identity.id)
        : queuedChatDeletedProjectStorageKey(identity.id, identity.storageEpoch);
    if (canonicalKey !== key) return "unknown";
    const belongsToCurrentGeneration =
      identity.storageEpoch === epochRead.epoch ||
      (identity.storageEpoch === undefined && epochRead.epoch === "0");
    if (!belongsToCurrentGeneration) continue;
    if (projectID !== undefined && identity.id !== projectID) continue;
    const read = readStorageItemWithStatus(storage, key);
    if (!read.complete || read.value === null) return "unknown";
    if (parseQueuedChatDeletedProjectEpoch(read.value) !== epochRead.epoch) return "unknown";
    matchingCurrentTombstone = true;
    if (projectID !== undefined) return "deleted";
  }
  return matchingCurrentTombstone ? "unknown" : "active";
}

function removeQueuedChatProjectTombstoneFromEpoch(
  storage: Storage,
  projectID: string,
  resetEpoch: string,
): boolean {
  const key = queuedChatDeletedProjectStorageKey(projectID, resetEpoch);
  const read = readStorageItemWithStatus(storage, key);
  if (!read.complete) return false;
  if (read.value === null) return true;
  if (parseQueuedChatDeletedProjectEpoch(read.value) !== resetEpoch) return true;
  return removeStorageItemIfUnchanged(storage, key, read.value);
}

function tombstoneQueuedChatProject(
  storage: Storage,
  projectID: string,
  resetEpoch: string,
): boolean {
  const key = queuedChatDeletedProjectStorageKey(projectID, resetEpoch);
  const marker = `${queuedChatDeletedProjectMarkerPrefix}${encodeURIComponent(
    resetEpoch,
  )}:${Date.now()}-${Math.random().toString(36).slice(2)}`;
  try {
    storage.setItem(key, marker);
    return storage.getItem(key) === marker;
  } catch (error) {
    logWarn(`queued chat storage: project tombstone failed for ${key}:`, error);
    return false;
  }
}

function parseQueuedChatDeletedSessionEpoch(raw: string): string | null {
  if (!raw.startsWith(queuedChatDeletedSessionMarkerPrefix)) return null;
  const encoded = raw.slice(queuedChatDeletedSessionMarkerPrefix.length).split(":", 1)[0];
  if (!encoded) return null;
  try {
    return decodeURIComponent(encoded);
  } catch {
    return null;
  }
}

function queuedChatSessionDeletionStatus(
  storage: Storage,
  sessionID: string,
  expectedResetEpoch?: ResetEpochRead,
): QueuedChatSessionDeletionStatus {
  if (!sessionID || sessionID !== sessionID.trim()) return "unknown";
  const epochRead = expectedResetEpoch ?? readResetEpoch(storage);
  if (!epochRead.complete) return "unknown";
  const scan = queuedChatDeletedSessionStorageKeys(storage);
  if (!scan.complete) return "unknown";
  for (const key of scan.keys) {
    const identity = queuedChatDeletedSessionIdentityFromStorageKey(key);
    if (!identity?.id || identity.storageEpoch === undefined) return "unknown";
    const canonicalKey = queuedChatDeletedSessionStorageKey(identity.id, identity.storageEpoch);
    if (canonicalKey !== key) return "unknown";
    if (identity.storageEpoch !== epochRead.epoch || identity.id !== sessionID) continue;
    const read = readStorageItemWithStatus(storage, key);
    if (!read.complete || read.value === null) return "unknown";
    return parseQueuedChatDeletedSessionEpoch(read.value) === epochRead.epoch
      ? "deleted"
      : "unknown";
  }
  return "active";
}

export function queuedChatSessionDeletionFenceStatus(
  sessionID: string,
): QueuedChatSessionDeletionFenceStatus {
  const storage = browserStorage();
  if (!storage) return "unknown";
  const status = queuedChatSessionDeletionStatus(storage, sessionID);
  if (status === "active") return "absent";
  return status === "deleted" ? "present" : "unknown";
}

function removeQueuedChatSessionTombstoneFromEpoch(
  storage: Storage,
  sessionID: string,
  resetEpoch: string,
): boolean {
  const key = queuedChatDeletedSessionStorageKey(sessionID, resetEpoch);
  const read = readStorageItemWithStatus(storage, key);
  if (!read.complete) return false;
  if (read.value === null) return true;
  if (parseQueuedChatDeletedSessionEpoch(read.value) !== resetEpoch) return true;
  return removeStorageItemIfUnchanged(storage, key, read.value);
}

function tombstoneQueuedChatSession(
  storage: Storage,
  sessionID: string,
  resetEpoch: string,
): boolean {
  const key = queuedChatDeletedSessionStorageKey(sessionID, resetEpoch);
  const marker = `${queuedChatDeletedSessionMarkerPrefix}${encodeURIComponent(
    resetEpoch,
  )}:${Date.now()}-${Math.random().toString(36).slice(2)}`;
  try {
    storage.setItem(key, marker);
    return storage.getItem(key) === marker;
  } catch (error) {
    logWarn(`queued chat storage: session tombstone failed for ${key}:`, error);
    return false;
  }
}

function readResetEpoch(storage: Storage): ResetEpochRead {
  const read = readStorageItemWithStatus(storage, queuedChatMessagesResetEpochStorageKey);
  const epoch = read.value ?? "0";
  return {
    epoch,
    complete: read.complete && isCanonicalQueuedChatStorageEpoch(epoch),
    present: read.value !== null,
  };
}

function newResetEpoch(): string {
  return `${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

function initializeQueuedChatMessages(fallback: QueuedChatMessage[]): InitializedQueue {
  const storage = browserStorage();
  if (!storage) {
    return {
      messages: sortQueuedChatMessages(fallback.map(storageFailedProjection)),
      durable: new Map(),
      pending: new Map(),
      projectedSubmittingIDs: new Set(),
      staleCleanupFailures: new Map(),
      resetEpoch: "0",
      resetEpochKnown: false,
    };
  }

  const markerRead = readStorageItemWithStatus(storage, queuedChatMessagesV2MarkerStorageKey);
  const markerPresent = markerRead.value === queuedChatMessagesV2MarkerValue;
  const resetEpochRead = readResetEpoch(storage);
  const resetEpoch = resetEpochRead.epoch;
  const pending = new Map<string, PendingRecord>();
  const staleCleanupFailures: StaleCleanupFailures = new Map();
  const storedRecordsRead = readQueuedChatMessagesSnapshot(storage, {
    preserveSubmitting: true,
  });
  let initializationKnown =
    markerRead.complete && resetEpochRead.complete && storedRecordsRead.complete;
  if (resetEpochRead.complete && resetEpochRead.present) {
    for (const record of storedRecordsRead.records) {
      const cleanupEpochRead = readResetEpoch(storage);
      if (!cleanupEpochRead.complete || cleanupEpochRead.epoch !== resetEpoch) {
        initializationKnown = false;
        break;
      }
      const belongsToCurrentGeneration =
        record.storageEpoch === resetEpoch ||
        (record.storageEpoch === undefined && resetEpoch === "0");
      if (belongsToCurrentGeneration) continue;
      const key = queuedChatMessageStorageKeyForEpoch(
        record.message.id,
        record.storageEpoch,
        record.storageRevision,
      );
      // Earlier releases used unscoped keys even after a profile reset. There
      // is no trustworthy generation in that key, so preserve the prompt as a
      // visible blocked recovery item instead of deleting or auto-delivering
      // it. An explicit Remove can target that exact legacy key.
      if (record.storageEpoch === undefined) {
        staleCleanupFailures.set(key, record);
        continue;
      }
      const cleanup = removeQueuedChatMessageWhere(
        storage,
        record.message.id,
        record.storageEpoch,
        record.storageRevision,
        () => true,
      );
      if (!cleanup.complete) {
        staleCleanupFailures.set(key, {
          message: cleanup.message ?? record.message,
          storageEpoch: record.storageEpoch,
          storageRevision: record.storageRevision,
        });
      }
    }
  }
  if (initializationKnown && resetEpoch === "0") {
    for (const record of storedRecordsRead.records) {
      if (record.storageEpoch !== undefined) continue;
      if (
        record.message.delivery_storage_epoch !== undefined &&
        record.message.delivery_storage_epoch !== resetEpoch
      ) {
        initializationKnown = false;
        continue;
      }
      const message = durableQueuedChatMessage(record.message, resetEpoch);
      const itemDeletionStatus = queuedChatItemDeletionStatus(
        storage,
        record.message,
        resetEpochRead,
      );
      if (itemDeletionStatus === "deleted") continue;
      if (itemDeletionStatus === "conflict") continue;
      if (itemDeletionStatus === "unknown") {
        initializationKnown = false;
        continue;
      }
      const migratedMarker = queuedChatMigratedLegacyMarker(storage, record.message.id, resetEpoch);
      if (!migratedMarker.complete) {
        initializationKnown = false;
        continue;
      }
      if (migratedMarker.fingerprint === queuedChatLegacyFingerprint(record.message)) {
        continue;
      }
      const scopedRecords = storedRecordsRead.records.filter(
        (candidate) => candidate.message.id === message.id && candidate.storageEpoch === resetEpoch,
      );
      if (scopedRecords.length > 1) {
        initializationKnown = false;
        continue;
      }
      const scopedRecord = scopedRecords[0];
      if (scopedRecord) {
        if (!sameQueuedChatPayload(scopedRecord.message, message)) {
          // A scoped record is already authoritative. Never let a partial
          // migration overwrite it with an older unscoped payload.
          initializationKnown = false;
          continue;
        }
        if (scopedRecord.storageRevision) {
          if (!writeQueuedChatMigratedLegacyMarker(storage, record.message, resetEpoch)) {
            initializationKnown = false;
          }
          continue;
        }
      }
      const written = writeQueuedChatMessage(
        storage,
        message,
        resetEpoch,
        scopedRecord?.message ?? record.message,
      );
      if (!written) {
        pending.set(message.id, message);
        initializationKnown = false;
      }
    }
  }
  const rawRead = readStoredSnapshotWithStatus(storage, resetEpochRead);
  const rawBeforeMigration = rawRead.messages;
  initializationKnown = initializationKnown && rawRead.complete;
  if (
    !resetEpochRead.present &&
    [...rawBeforeMigration.values()].some(
      (message) =>
        message.delivery_storage_epoch !== undefined && message.delivery_storage_epoch !== "0",
    )
  ) {
    initializationKnown = false;
  }

  let legacyMessages: QueuedChatMessage[] = [];
  if (!markerPresent && (!resetEpochRead.complete || resetEpoch === "0")) {
    const legacyRead = readStorageItemWithStatus(storage, queuedChatMessagesStorageKey);
    if (!legacyRead.complete) initializationKnown = false;
    if (legacyRead.value) {
      try {
        legacyMessages =
          parseQueuedChatMessageList(JSON.parse(legacyRead.value), {
            preserveSubmitting: false,
          }) ?? [];
      } catch {
        // A malformed legacy array is non-authoritative and is never copied
        // into the per-item format.
      }
    }
  }

  const durableBeforeMigrationRead = initializationKnown
    ? readStoredSnapshotWithStatus(storage, resetEpochRead)
    : { messages: rawBeforeMigration, complete: false };
  if (!durableBeforeMigrationRead.complete) initializationKnown = false;
  const durableBeforeMigration = durableBeforeMigrationRead.messages;
  const merged = new Map(durableBeforeMigration);

  for (const legacy of legacyMessages) {
    const existing = merged.get(legacy.id);
    merged.set(
      legacy.id,
      existing ? resolveMigrationConflict(existing, legacy) : durableQueuedChatMessage(legacy),
    );
  }

  if (!initializationKnown) {
    if (merged.size === 0) {
      for (const message of fallback) merged.set(message.id, durableQueuedChatMessage(message));
    }
    for (const failure of staleCleanupFailures.values()) {
      if (!merged.has(failure.message.id)) merged.set(failure.message.id, failure.message);
    }
    return {
      messages: sortQueuedChatMessages([...merged.values()].map(cleanupFailedProjection)),
      durable: rawBeforeMigration,
      pending: new Map([...merged.keys()].map((id) => [id, null as PendingRecord])),
      projectedSubmittingIDs: new Set(),
      staleCleanupFailures,
      resetEpoch,
      resetEpochKnown: false,
    };
  }

  if (merged.size === 0) {
    for (const message of fallback) {
      const normalized =
        message.delivery_state === "submitting"
          ? { ...message, delivery_state: "reconcile_required" as const }
          : message;
      merged.set(normalized.id, durableQueuedChatMessage(normalized));
    }
  }

  let migrationDurable = true;
  for (const message of merged.values()) {
    const existing = durableBeforeMigration.get(message.id);
    if (sameQueuedChatMessage(existing, message)) continue;
    if (!writeQueuedChatMessage(storage, message, resetEpoch)) {
      migrationDurable = false;
      pending.set(message.id, durableQueuedChatMessage(message, resetEpoch));
    }
  }

  if (!markerPresent && migrationDurable) {
    try {
      storage.setItem(queuedChatMessagesV2MarkerStorageKey, queuedChatMessagesV2MarkerValue);
      migrationDurable =
        storage.getItem(queuedChatMessagesV2MarkerStorageKey) === queuedChatMessagesV2MarkerValue;
    } catch (error) {
      migrationDurable = false;
      logWarn("queued chat storage: v2 marker write failed:", error);
    }
  }
  if (
    (markerPresent || migrationDurable) &&
    safeStorageItem(storage, queuedChatMessagesStorageKey)
  ) {
    try {
      storage.removeItem(queuedChatMessagesStorageKey);
    } catch {
      // A durable marker makes a leftover legacy array non-authoritative.
    }
  }

  const durableRead = readStoredSnapshotWithStatus(storage);
  if (!durableRead.complete) migrationDurable = false;
  const durable = durableRead.messages;
  const deletedOwnerCleanupFailures = new Set<string>();
  for (const [id, message] of durable) {
    const deletionStatus = queuedChatSessionDeletionStatus(storage, message.session_id);
    if (deletionStatus === "active") continue;
    const cleanup =
      deletionStatus === "deleted"
        ? removeQueuedChatMessageWhere(
            storage,
            id,
            message.delivery_storage_epoch,
            message.delivery_storage_revision,
            (candidate) => candidate.session_id === message.session_id,
          )
        : { message, complete: false };
    if (deletionStatus === "deleted" && cleanup.complete && !cleanup.message) {
      durable.delete(id);
      pending.delete(id);
    } else {
      if (cleanup.message) durable.set(id, cleanup.message);
      deletedOwnerCleanupFailures.add(id);
      pending.set(id, null);
    }
  }
  for (const [id, message] of durable) {
    const deletionStatus = queuedChatProjectDeletionStatus(storage, message.project_id);
    if (deletionStatus === "active") continue;
    const cleanup =
      deletionStatus === "deleted"
        ? removeQueuedChatMessageWhere(
            storage,
            id,
            message.delivery_storage_epoch,
            message.delivery_storage_revision,
            (candidate) => sameQueuedChatMessage(candidate, message),
          )
        : { message, complete: false };
    if (deletionStatus === "deleted" && cleanup.complete && !cleanup.message) {
      durable.delete(id);
      pending.delete(id);
    } else {
      if (cleanup.message) durable.set(id, cleanup.message);
      deletedOwnerCleanupFailures.add(id);
      pending.set(id, null);
    }
  }
  for (const [id, intent] of pending) {
    if (
      intent &&
      (queuedChatSessionDeletionStatus(storage, intent.session_id) !== "active" ||
        queuedChatProjectDeletionStatus(storage, intent.project_id) !== "active")
    ) {
      pending.set(id, null);
    }
  }
  const projectedSubmittingIDs = new Set<string>();
  const local = new Map<string, QueuedChatMessage>();
  for (const message of durable.values()) {
    if (message.delivery_state === "submitting") projectedSubmittingIDs.add(message.id);
    local.set(
      message.id,
      deletedOwnerCleanupFailures.has(message.id)
        ? cleanupFailedProjection(message)
        : projectStoredSubmitting(message),
    );
  }
  for (const [id, intent] of pending) {
    if (intent && !projectedSubmittingIDs.has(id)) {
      local.set(id, storageFailedProjection(intent));
    }
  }

  const finalEpochRead = readResetEpoch(storage);
  const resetEpochStable = finalEpochRead.complete && finalEpochRead.epoch === resetEpoch;
  if (!resetEpochStable) {
    const blocked = new Map(durable);
    for (const message of merged.values()) {
      if (!blocked.has(message.id)) blocked.set(message.id, message);
    }
    return {
      messages: sortQueuedChatMessages([...blocked.values()].map(cleanupFailedProjection)),
      durable,
      pending: new Map([...blocked.keys()].map((id) => [id, null as PendingRecord])),
      projectedSubmittingIDs: new Set(),
      staleCleanupFailures,
      resetEpoch: finalEpochRead.epoch,
      resetEpochKnown: false,
    };
  }

  return {
    messages:
      staleCleanupFailures.size > 0
        ? staleCleanupBlockedMessages(local.values(), staleCleanupFailures)
        : sortQueuedChatMessages(local.values()),
    durable,
    pending,
    projectedSubmittingIDs,
    staleCleanupFailures,
    resetEpoch: finalEpochRead.epoch,
    resetEpochKnown: true,
  };
}

function submittingFenceMatches(stored: QueuedChatMessage, queued: QueuedChatMessage): boolean {
  const expectedBaseline = queued.delivery_baseline_message_ids ?? [];
  const storedBaseline = stored.delivery_baseline_message_ids ?? [];
  return (
    stored.id === queued.id &&
    stored.session_id === queued.session_id &&
    stored.project_id === queued.project_id &&
    stored.delivery_storage_epoch === queued.delivery_storage_epoch &&
    stored.content === queued.content &&
    stored.delivery_state === "submitting" &&
    stored.delivery_idempotency_keyed === true &&
    stored.execution_mode === queued.execution_mode &&
    stored.tools_enabled === queued.tools_enabled &&
    stored.provider_filter === queued.provider_filter &&
    stored.model === queued.model &&
    stored.workspace === queued.workspace &&
    stored.system_prompt === queued.system_prompt &&
    stored.agent_id === queued.agent_id &&
    storedBaseline.length === expectedBaseline.length &&
    storedBaseline.every((messageID, index) => messageID === expectedBaseline[index])
  );
}

export type QueuedChatMessageStore = {
  messages: QueuedChatMessage[];
  setMessages: Dispatch<SetStateAction<QueuedChatMessage[]>>;
  enqueueMessage: (message: QueuedChatMessage) => QueuedChatEnqueueResult;
  hasDurableSubmittingFence: (queued: QueuedChatMessage) => boolean;
  deleteWhere: (predicate: (message: QueuedChatMessage) => boolean) => boolean;
  deleteSession: (sessionID: string) => boolean;
  deleteProjectWhere: (
    projectID: string,
    predicate: (message: QueuedChatMessage) => boolean,
  ) => boolean;
  clear: () => boolean;
};

export type QueuedChatEnqueueResult =
  | "admitted"
  | "storage_failed"
  | "reset_observed"
  | "item_conflict"
  | "session_deleted"
  | "project_deleted";

export function useQueuedChatMessageStore(fallback: QueuedChatMessage[]): QueuedChatMessageStore {
  const initializedRef = useRef<InitializedQueue | null>(null);
  if (!initializedRef.current) initializedRef.current = initializeQueuedChatMessages(fallback);
  const [messages, setMessagesState] = useState(initializedRef.current.messages);
  const messagesRef = useRef(messages);
  const durableSnapshotRef = useRef(initializedRef.current.durable);
  const pendingWritesRef = useRef(initializedRef.current.pending);
  const projectedSubmittingIDsRef = useRef(initializedRef.current.projectedSubmittingIDs);
  const staleCleanupFailuresRef = useRef(initializedRef.current.staleCleanupFailures);
  const staleCleanupRemovalIntentsRef = useRef(new Map<string, QueuedChatMessage | null>());
  const resetEpochRef = useRef(initializedRef.current.resetEpoch);
  const resetEpochKnownRef = useRef(initializedRef.current.resetEpochKnown);
  const externalResetObservedRef = useRef(false);
  const storageSnapshotConflictRef = useRef(false);

  useLayoutEffect(() => {
    messagesRef.current = messages;
  }, [messages]);

  const commitMessages = useCallback((next: Iterable<QueuedChatMessage>) => {
    const sorted = sortQueuedChatMessages(
      new Map([...next].map((message) => [message.id, message])).values(),
    );
    messagesRef.current = sorted;
    setMessagesState(sorted);
  }, []);

  const observeExternalReset = useCallback(
    (epoch: string) => {
      externalResetObservedRef.current = true;
      resetEpochRef.current = epoch;
      resetEpochKnownRef.current = true;
      durableSnapshotRef.current = new Map();
      pendingWritesRef.current = new Map();
      projectedSubmittingIDsRef.current = new Set();
      staleCleanupFailuresRef.current = new Map();
      staleCleanupRemovalIntentsRef.current = new Map();
      storageSnapshotConflictRef.current = false;
      commitMessages([]);
    },
    [commitMessages],
  );

  const blockForUnknownStorage = useCallback(
    (additional: Iterable<QueuedChatMessage> = []) => {
      resetEpochKnownRef.current = false;
      storageSnapshotConflictRef.current = false;
      const blocked = new Map(messagesRef.current.map((message) => [message.id, message]));
      for (const message of additional) blocked.set(message.id, message);
      pendingWritesRef.current = new Map(
        [...blocked.keys()].map((id) => [id, null as PendingRecord]),
      );
      projectedSubmittingIDsRef.current = new Set();
      commitMessages([...blocked.values()].map(cleanupFailedProjection));
    },
    [commitMessages],
  );

  const blockForStaleCleanupFailures = useCallback(() => {
    projectedSubmittingIDsRef.current = new Set();
    commitMessages(
      staleCleanupBlockedMessages(messagesRef.current, staleCleanupFailuresRef.current),
    );
  }, [commitMessages]);

  const setMessages = useCallback<Dispatch<SetStateAction<QueuedChatMessage[]>>>(
    (next) => {
      const current = messagesRef.current;
      const requested =
        typeof next === "function"
          ? (next as (value: QueuedChatMessage[]) => QueuedChatMessage[])(current)
          : next;
      const currentByID = new Map(current.map((message) => [message.id, message]));
      const requestedByID = new Map(requested.map((message) => [message.id, message]));
      const storage = browserStorage();
      if (!storage) {
        const failed = new Map(currentByID);
        const pending = new Map(pendingWritesRef.current);
        for (const id of new Set([...currentByID.keys(), ...requestedByID.keys()])) {
          const before = currentByID.get(id);
          const desired = requestedByID.get(id);
          if (sameLocalQueuedChatMessage(before, desired)) continue;
          if (desired) {
            const durable = durableQueuedChatMessage(desired);
            pending.set(id, durable);
            failed.set(id, storageFailedProjection(durable));
          } else if (before) {
            pending.set(id, null);
            failed.set(id, storageFailedProjection(before));
          }
        }
        pendingWritesRef.current = pending;
        commitMessages(failed.values());
        return;
      }

      if (staleCleanupFailuresRef.current.size > 0) {
        const removedIDs = new Set(
          current.filter((message) => !requestedByID.has(message.id)).map((message) => message.id),
        );
        const removalIntents = new Map(staleCleanupRemovalIntentsRef.current);
        for (const id of removedIDs) {
          removalIntents.set(id, durableSnapshotRef.current.get(id) ?? null);
        }
        staleCleanupRemovalIntentsRef.current = removalIntents;
        const retryTargets = [...staleCleanupFailuresRef.current].filter(([, failure]) =>
          removalIntents.has(failure.message.id),
        );
        if (retryTargets.length === 0) {
          blockForStaleCleanupFailures();
          return;
        }

        const failures = new Map(staleCleanupFailuresRef.current);
        for (const [key, failure] of retryTargets) {
          const cleanup = removeQueuedChatMessageWhere(
            storage,
            failure.message.id,
            failure.storageEpoch,
            failure.storageRevision,
            (candidate) =>
              candidate.delivery_state !== "submitting" &&
              sameQueuedChatMessage(candidate, failure.message),
          );
          if (cleanup.complete && !cleanup.message) {
            failures.delete(key);
          } else {
            failures.set(key, {
              message: cleanup.message ?? failure.message,
              storageEpoch: failure.storageEpoch,
              storageRevision: failure.storageRevision,
            });
          }
        }
        staleCleanupFailuresRef.current = failures;
        if (failures.size > 0) {
          blockForStaleCleanupFailures();
          return;
        }

        const epochRead = readResetEpoch(storage);
        if (
          !epochRead.complete ||
          !resetEpochKnownRef.current ||
          epochRead.epoch !== resetEpochRef.current
        ) {
          blockForUnknownStorage();
          return;
        }
        const durableRead = readStoredSnapshotWithStatus(storage, epochRead);
        if (!durableRead.complete) {
          blockForUnknownStorage(durableRead.messages.values());
          return;
        }
        durableSnapshotRef.current = durableRead.messages;
        const safeRemovalIDs = new Set<string>();
        for (const [id, expected] of removalIntents) {
          const stored = durableRead.messages.get(id);
          if (!stored) {
            safeRemovalIDs.add(id);
            continue;
          }
          if (
            expected &&
            stored.delivery_state !== "submitting" &&
            sameQueuedChatRemovalTarget(stored, expected)
          ) {
            safeRemovalIDs.add(id);
          }
        }
        for (const id of safeRemovalIDs) requestedByID.delete(id);
        staleCleanupRemovalIntentsRef.current = new Map();
        const pending = new Map(pendingWritesRef.current);
        pendingWritesRef.current = pending;
        projectedSubmittingIDsRef.current = new Set(
          [...durableRead.messages.values()]
            .filter((message) => message.delivery_state === "submitting")
            .map((message) => message.id),
        );
        // Normalize rows that were globally blocked by the stale record, then
        // continue through the ordinary setter path. A Remove intent applies
        // only to the exact ready durable payload seen when it was clicked;
        // edited replacements and submitting fences remain for reconciliation.
        for (const [id, message] of durableRead.messages) {
          const restored =
            removalIntents.has(id) && !safeRemovalIDs.has(id)
              ? conflictProjection(message)
              : projectStoredSubmitting(message);
          currentByID.set(id, restored);
          if (!safeRemovalIDs.has(id)) requestedByID.set(id, restored);
        }
        for (const [id, intent] of pending) {
          if (!intent) continue;
          const restored = storageFailedProjection(intent);
          currentByID.set(id, restored);
          if (!safeRemovalIDs.has(id)) requestedByID.set(id, restored);
        }
      }

      const resetEpochRead = readResetEpoch(storage);
      if (externalResetObservedRef.current) {
        commitMessages([]);
        return;
      }
      if (!resetEpochRead.complete || !resetEpochKnownRef.current) {
        blockForUnknownStorage(requestedByID.values());
        return;
      }
      const resetEpoch = resetEpochRead.epoch;
      if (resetEpoch !== resetEpochRef.current) {
        observeExternalReset(resetEpoch);
        return;
      }
      const unknownOwnerItemIDs = new Set<string>();
      const staleEpochItemIDs = new Set<string>();
      for (const [id, desired] of requestedByID) {
        if (desired.delivery_storage_epoch !== resetEpoch) staleEpochItemIDs.add(id);
        const sessionDeletionStatus = queuedChatSessionDeletionStatus(
          storage,
          desired.session_id,
          resetEpochRead,
        );
        const projectDeletionStatus = queuedChatProjectDeletionStatus(
          storage,
          desired.project_id,
          resetEpochRead,
        );
        if (sessionDeletionStatus === "deleted" || projectDeletionStatus === "deleted") {
          requestedByID.delete(id);
        } else if (sessionDeletionStatus === "unknown" || projectDeletionStatus === "unknown") {
          unknownOwnerItemIDs.add(id);
        }
      }

      const storedNowRead = readStoredSnapshotWithStatus(storage, resetEpochRead);
      if (!storedNowRead.complete) {
        const epochAfterRead = readResetEpoch(storage);
        if (epochAfterRead.complete && epochAfterRead.epoch !== resetEpoch) {
          observeExternalReset(epochAfterRead.epoch);
          return;
        }
        blockForUnknownStorage([...requestedByID.values(), ...storedNowRead.messages.values()]);
        return;
      }
      const storedNow = storedNowRead.messages;
      const knownDurable = durableSnapshotRef.current;
      const pending = new Map(pendingWritesRef.current);
      const projected = new Set(projectedSubmittingIDsRef.current);
      const result = new Map(currentByID);
      const changedIDs = new Set<string>();
      for (const id of new Set([...currentByID.keys(), ...requestedByID.keys()])) {
        if (!sameLocalQueuedChatMessage(currentByID.get(id), requestedByID.get(id))) {
          changedIDs.add(id);
        }
      }
      const touchedIDs = new Set([
        ...changedIDs,
        ...pending.keys(),
        ...unknownOwnerItemIDs,
        ...staleEpochItemIDs,
      ]);

      // Converge untouched ids with another tab without turning this setter
      // into a stale whole-array rewrite. Failed local writes remain explicit
      // pending intents and are retried below.
      for (const id of new Set([...knownDurable.keys(), ...storedNow.keys()])) {
        if (touchedIDs.has(id) || pending.has(id)) continue;
        const known = knownDurable.get(id);
        const stored = storedNow.get(id);
        if (sameQueuedChatMessage(known, stored)) continue;
        if (!stored) {
          result.delete(id);
          projected.delete(id);
          continue;
        }
        const currentMessage = currentByID.get(id);
        const local =
          currentMessage && !sameQueuedChatMessage(currentMessage, stored)
            ? conflictProjection(stored)
            : projectStoredSubmitting(stored);
        result.set(id, local);
        if (stored.delivery_state === "submitting") projected.add(id);
        else projected.delete(id);
      }

      for (const id of touchedIDs) {
        if (unknownOwnerItemIDs.has(id) || staleEpochItemIDs.has(id)) {
          const desired = requestedByID.get(id) ?? currentByID.get(id);
          if (desired) {
            const durable = durableQueuedChatMessage(desired);
            pending.set(id, durable);
            result.set(id, storageFailedProjection(durable));
            projected.delete(id);
          }
          continue;
        }
        const pendingIntent = pending.get(id);
        const desired = changedIDs.has(id)
          ? requestedByID.get(id)
          : pendingIntent === null
            ? undefined
            : (pendingIntent ?? requestedByID.get(id));
        const stored = storedNow.get(id);
        const known = knownDurable.get(id);

        // Explicit Check status may claim the exact foreign/stale durable
        // fence for a safe same-key replay. It does not rewrite that record.
        if (
          desired?.delivery_state === "submitting" &&
          stored?.delivery_state === "submitting" &&
          submittingFenceMatches(stored, desired)
        ) {
          result.set(id, durableQueuedChatMessage(desired));
          projected.delete(id);
          pending.delete(id);
          continue;
        }

        if (projected.has(id) && stored?.delivery_state === "submitting") {
          result.set(id, projectStoredSubmitting(stored));
          pending.delete(id);
          continue;
        }

        if (!sameQueuedChatMessage(known, stored)) {
          pending.delete(id);
          if (!stored) {
            result.delete(id);
            projected.delete(id);
          } else {
            result.set(id, conflictProjection(stored));
            if (stored.delivery_state === "submitting") projected.add(id);
            else projected.delete(id);
          }
          continue;
        }

        if (!desired) {
          let cleanup: { message?: QueuedChatMessage; complete: boolean };
          if (stored) {
            cleanup = removeQueuedChatMessageWhere(
              storage,
              id,
              resetEpoch,
              stored.delivery_storage_revision,
              (candidate) =>
                candidate.delivery_storage_epoch === resetEpoch &&
                sameQueuedChatRemovalTarget(candidate, stored),
              true,
            );
          } else {
            // A failed edit may have revoked its older ready revision while
            // retaining only a pending in-memory replacement. Removing that
            // logical row still needs an ID-scoped tombstone: otherwise a
            // stale tab can rewrite the prior same-ID payload after this
            // setter's final audit and make it drainable again.
            const localRemovalTarget = pendingIntent ?? currentByID.get(id) ?? known;
            const expected = localRemovalTarget
              ? durableQueuedChatMessage(localRemovalTarget, resetEpoch)
              : undefined;
            if (!expected) {
              cleanup = { complete: true };
            } else {
              const tombstoned = tombstoneQueuedChatItem(storage, expected, resetEpoch);
              const epochAfterTombstone = readResetEpoch(storage);
              const complete =
                tombstoned &&
                epochAfterTombstone.complete &&
                epochAfterTombstone.epoch === resetEpoch &&
                queuedChatItemDeletionStatus(storage, expected, epochAfterTombstone) === "deleted";
              cleanup = complete ? { complete: true } : { message: expected, complete: false };
            }
          }
          if (cleanup.complete && !cleanup.message) {
            result.delete(id);
            pending.delete(id);
            projected.delete(id);
          } else if (cleanup.complete && cleanup.message) {
            pending.delete(id);
            result.set(id, conflictProjection(cleanup.message));
            if (cleanup.message.delivery_state === "submitting") projected.add(id);
            else projected.delete(id);
          } else {
            const failedStored = cleanup.message ?? stored;
            if (!failedStored) {
              result.delete(id);
              pending.delete(id);
              projected.delete(id);
              continue;
            }
            pending.set(id, null);
            result.set(id, storageFailedProjection(conflictProjection(failedStored)));
            if (failedStored.delivery_state === "submitting") projected.add(id);
          }
          continue;
        }

        const durableDesired = durableQueuedChatMessage(desired, resetEpoch);
        const persisted = sameQueuedChatMessage(stored, durableDesired)
          ? stored
          : writeQueuedChatMessage(storage, durableDesired, resetEpoch, stored);
        if (persisted) {
          result.set(id, persisted);
          pending.delete(id);
          projected.delete(id);
        } else {
          // Revoke any older ready payload before retaining the latest edit in
          // memory. This keeps a reload from silently draining stale content
          // after a quota-style setItem failure. If removal also fails, the
          // visible failure state and unload warning remain the only safe
          // recovery path, so the operator is told to clear site data.
          if (stored && !stored.delivery_state) {
            removeQueuedChatMessageWhere(
              storage,
              id,
              resetEpoch,
              stored.delivery_storage_revision,
              (candidate) =>
                candidate.delivery_storage_epoch === resetEpoch &&
                sameQueuedChatRemovalTarget(candidate, stored),
            );
          }
          pending.set(id, durableDesired);
          result.set(id, storageFailedProjection(durableDesired));
          projected.delete(id);
        }
      }

      for (const id of touchedIDs) {
        const candidate = result.get(id);
        if (!candidate) continue;
        if (candidate.delivery_storage_epoch !== resetEpoch) {
          pending.set(id, null);
          result.set(id, cleanupFailedProjection(candidate));
          projected.delete(id);
          continue;
        }
        const sessionDeletionStatus = queuedChatSessionDeletionStatus(
          storage,
          candidate.session_id,
          resetEpochRead,
        );
        if (sessionDeletionStatus !== "active") {
          const cleanup =
            sessionDeletionStatus === "deleted"
              ? removeQueuedChatMessageWhere(
                  storage,
                  id,
                  resetEpoch,
                  candidate.delivery_storage_revision,
                  (stored) =>
                    stored.delivery_storage_epoch === resetEpoch &&
                    stored.session_id === candidate.session_id,
                )
              : { message: candidate, complete: false };
          if (sessionDeletionStatus === "deleted" && cleanup.complete && !cleanup.message) {
            result.delete(id);
            pending.delete(id);
            projected.delete(id);
          } else {
            pending.set(id, null);
            result.set(id, cleanupFailedProjection(cleanup.message ?? candidate));
          }
          continue;
        }
        const deletionStatus = queuedChatProjectDeletionStatus(storage, candidate.project_id);
        if (deletionStatus === "active") continue;
        const cleanup =
          deletionStatus === "deleted"
            ? removeQueuedChatMessageWhere(
                storage,
                id,
                resetEpoch,
                candidate.delivery_storage_revision,
                (stored) =>
                  stored.delivery_storage_epoch === resetEpoch &&
                  stored.project_id === candidate.project_id,
              )
            : { message: candidate, complete: false };
        if (deletionStatus === "deleted" && cleanup.complete && !cleanup.message) {
          result.delete(id);
          pending.delete(id);
          projected.delete(id);
        } else {
          pending.set(id, null);
          result.set(id, cleanupFailedProjection(cleanup.message ?? candidate));
        }
      }

      const resetEpochAfterRead = readResetEpoch(storage);
      if (!resetEpochAfterRead.complete) {
        blockForUnknownStorage(result.values());
        return;
      }
      const durableAfterRead = readStoredSnapshotWithStatus(storage, resetEpochAfterRead);
      if (!durableAfterRead.complete) {
        blockForUnknownStorage([...result.values(), ...durableAfterRead.messages.values()]);
        return;
      }
      const durableAfter = durableAfterRead.messages;
      const finalEpochRead = readResetEpoch(storage);
      if (!finalEpochRead.complete) {
        blockForUnknownStorage([...result.values(), ...durableAfter.values()]);
        return;
      }
      const resetEpochAfter = finalEpochRead.epoch;
      if (resetEpochAfter !== resetEpochAfterRead.epoch) {
        observeExternalReset(resetEpochAfter);
        return;
      }
      if (resetEpochAfter !== resetEpoch) {
        for (const id of touchedIDs) {
          removeQueuedChatMessageWhere(
            storage,
            id,
            resetEpoch,
            result.get(id)?.delivery_storage_revision,
            (candidate) => candidate.delivery_storage_epoch === resetEpoch,
          );
        }
        observeExternalReset(resetEpochAfter);
        return;
      }
      for (const [id, stored] of durableAfter) {
        const intent = pending.get(id);
        if (intent === null) continue;
        if (intent && sameQueuedChatMessage(intent, stored)) {
          pending.delete(id);
          result.set(id, durableQueuedChatMessage(intent));
          projected.delete(id);
          continue;
        }
        if (!result.has(id)) {
          result.set(id, projectStoredSubmitting(stored));
          if (stored.delivery_state === "submitting") projected.add(id);
        }
      }
      for (const [id, intent] of pending) {
        if (intent === null && !durableAfter.has(id)) pending.delete(id);
      }

      durableSnapshotRef.current = durableAfter;
      pendingWritesRef.current = pending;
      projectedSubmittingIDsRef.current = projected;
      commitMessages(result.values());
    },
    [blockForStaleCleanupFailures, blockForUnknownStorage, commitMessages, observeExternalReset],
  );

  const enqueueMessage = useCallback(
    (message: QueuedChatMessage): QueuedChatEnqueueResult => {
      const storage = browserStorage();
      if (!storage) return "storage_failed";
      if (staleCleanupFailuresRef.current.size > 0) {
        blockForStaleCleanupFailures();
        return "storage_failed";
      }
      const resetEpochRead = readResetEpoch(storage);
      if (externalResetObservedRef.current) return "reset_observed";
      if (!resetEpochRead.complete || !resetEpochKnownRef.current) {
        blockForUnknownStorage();
        return "storage_failed";
      }
      const resetEpoch = resetEpochRead.epoch;
      if (resetEpoch !== resetEpochRef.current) {
        observeExternalReset(resetEpoch);
        return "reset_observed";
      }

      let durableMessage = durableQueuedChatMessage(message, resetEpoch);
      const sessionDeletionStatus = queuedChatSessionDeletionStatus(
        storage,
        durableMessage.session_id,
        resetEpochRead,
      );
      if (sessionDeletionStatus === "deleted") return "session_deleted";
      if (sessionDeletionStatus === "unknown") return "storage_failed";
      const projectDeletionStatus = queuedChatProjectDeletionStatus(
        storage,
        durableMessage.project_id,
      );
      if (projectDeletionStatus === "deleted") return "project_deleted";
      if (projectDeletionStatus === "unknown") return "storage_failed";
      const itemDeletionStatus = queuedChatItemDeletionStatus(
        storage,
        durableMessage,
        resetEpochRead,
      );
      if (itemDeletionStatus === "deleted" || itemDeletionStatus === "conflict") {
        return "item_conflict";
      }
      if (itemDeletionStatus === "unknown") return "storage_failed";
      const durableBeforeRead = readStoredSnapshotWithStatus(storage, resetEpochRead);
      if (!durableBeforeRead.complete) {
        const epochAfterRead = readResetEpoch(storage);
        if (epochAfterRead.complete && epochAfterRead.epoch !== resetEpoch) {
          observeExternalReset(epochAfterRead.epoch);
          return "reset_observed";
        }
        blockForUnknownStorage(durableBeforeRead.messages.values());
        return "storage_failed";
      }
      const durableBefore = durableBeforeRead.messages;
      const existing = durableBefore.get(durableMessage.id);
      if (existing && !sameQueuedChatMessage(existing, durableMessage)) return "item_conflict";
      if (!existing) {
        const written = writeQueuedChatMessage(storage, durableMessage, resetEpoch);
        if (!written) return "storage_failed";
        durableMessage = written;
      }

      const resetEpochAfterRead = readResetEpoch(storage);
      if (!resetEpochAfterRead.complete) {
        if (!revokeQueuedChatMessage(storage, durableMessage)) {
          blockForUnknownStorage([durableMessage]);
        } else {
          blockForUnknownStorage();
        }
        return "storage_failed";
      }
      const durableAfterRead = readStoredSnapshotWithStatus(storage, resetEpochAfterRead);
      const finalEpochRead = readResetEpoch(storage);
      if (!finalEpochRead.complete) {
        if (!revokeQueuedChatMessage(storage, durableMessage)) {
          blockForUnknownStorage([durableMessage]);
        } else {
          blockForUnknownStorage(durableAfterRead.messages.values());
        }
        return "storage_failed";
      }
      const resetEpochAfter = finalEpochRead.epoch;
      if (resetEpochAfter !== resetEpochAfterRead.epoch || resetEpochAfter !== resetEpoch) {
        removeQueuedChatMessageWhere(
          storage,
          durableMessage.id,
          resetEpoch,
          durableMessage.delivery_storage_revision,
          (candidate) =>
            candidate.delivery_storage_epoch === resetEpoch &&
            sameQueuedChatMessage(candidate, durableMessage),
        );
        observeExternalReset(resetEpochAfter);
        return "reset_observed";
      }
      if (!durableAfterRead.complete) {
        if (!revokeQueuedChatMessage(storage, durableMessage)) {
          blockForUnknownStorage([durableMessage, ...durableAfterRead.messages.values()]);
        } else {
          blockForUnknownStorage(durableAfterRead.messages.values());
        }
        return "storage_failed";
      }
      const durableAfter = durableAfterRead.messages;
      const sessionDeletionStatusAfter = queuedChatSessionDeletionStatus(
        storage,
        durableMessage.session_id,
        finalEpochRead,
      );
      if (sessionDeletionStatusAfter !== "active") {
        const cleanup = removeQueuedChatMessageWhere(
          storage,
          durableMessage.id,
          resetEpoch,
          durableMessage.delivery_storage_revision,
          (candidate) =>
            candidate.delivery_storage_epoch === resetEpoch &&
            sameQueuedChatMessage(candidate, durableMessage),
        );
        return sessionDeletionStatusAfter === "deleted" && cleanup.complete && !cleanup.message
          ? "session_deleted"
          : "storage_failed";
      }
      const projectDeletionStatusAfter = queuedChatProjectDeletionStatus(
        storage,
        durableMessage.project_id,
      );
      if (projectDeletionStatusAfter !== "active") {
        revokeQueuedChatMessage(storage, durableMessage);
        return projectDeletionStatusAfter === "deleted" ? "project_deleted" : "storage_failed";
      }
      const itemDeletionStatusAfter = queuedChatItemDeletionStatus(
        storage,
        durableMessage,
        finalEpochRead,
      );
      if (itemDeletionStatusAfter !== "active") {
        if (itemDeletionStatusAfter === "deleted") {
          cleanupDeletedQueuedChatItemArtifacts(storage, durableMessage, resetEpoch);
          return "item_conflict";
        }
        if (itemDeletionStatusAfter === "conflict") return "item_conflict";
        return "storage_failed";
      }
      if (!sameQueuedChatMessage(durableAfter.get(durableMessage.id), durableMessage)) {
        revokeQueuedChatMessage(storage, durableMessage);
        return "storage_failed";
      }

      const next = new Map(messagesRef.current.map((queued) => [queued.id, queued]));
      const pending = new Map(pendingWritesRef.current);
      const projected = new Set(projectedSubmittingIDsRef.current);

      // Fold in storage changes that may have landed in another tab before
      // this enqueue. The new item is authoritative only after its own exact
      // write/read verification succeeds.
      for (const id of durableSnapshotRef.current.keys()) {
        if (!durableAfter.has(id) && !pending.has(id)) {
          next.delete(id);
          projected.delete(id);
        }
      }
      for (const [id, stored] of durableAfter) {
        if (id === durableMessage.id) continue;
        const current = next.get(id);
        if (projected.has(id) && stored.delivery_state === "submitting") {
          next.set(id, projectStoredSubmitting(stored));
        } else if (current && !sameQueuedChatMessage(current, stored)) {
          next.set(id, conflictProjection(stored));
          if (stored.delivery_state === "submitting") projected.add(id);
          else projected.delete(id);
        } else if (!current) {
          next.set(id, projectStoredSubmitting(stored));
          if (stored.delivery_state === "submitting") projected.add(id);
        }
      }

      next.set(durableMessage.id, durableMessage);
      pending.delete(durableMessage.id);
      projected.delete(durableMessage.id);
      durableSnapshotRef.current = durableAfter;
      pendingWritesRef.current = pending;
      projectedSubmittingIDsRef.current = projected;
      commitMessages(next.values());
      return "admitted";
    },
    [blockForStaleCleanupFailures, blockForUnknownStorage, commitMessages, observeExternalReset],
  );

  const deleteWhere = useCallback(
    (predicate: (message: QueuedChatMessage) => boolean): boolean => {
      const storage = browserStorage();
      if (externalResetObservedRef.current) {
        commitMessages([]);
        return false;
      }
      if (!storage) {
        blockForUnknownStorage();
        return false;
      }
      const resetEpochRead = readResetEpoch(storage);
      if (!resetEpochRead.complete || !resetEpochKnownRef.current) {
        blockForUnknownStorage();
        return false;
      }
      const resetEpoch = resetEpochRead.epoch;
      if (resetEpoch !== resetEpochRef.current) {
        observeExternalReset(resetEpoch);
        return false;
      }
      const deferredRemovalIntents = new Map(staleCleanupRemovalIntentsRef.current);
      const matchesDeferredRemoval = (message: QueuedChatMessage) => {
        const expected = deferredRemovalIntents.get(message.id);
        return Boolean(
          expected &&
          message.delivery_state !== "submitting" &&
          message.delivery_storage_fenced !== true &&
          sameQueuedChatRemovalTarget(message, expected),
        );
      };
      const shouldDeleteCurrent = (message: QueuedChatMessage) =>
        predicate(message) || matchesDeferredRemoval(message);
      let recoveredStaleCleanup = false;
      if (staleCleanupFailuresRef.current.size > 0) {
        const failures = new Map(staleCleanupFailuresRef.current);
        for (const [key, failure] of staleCleanupFailuresRef.current) {
          if (!predicate(failure.message) && !deferredRemovalIntents.has(failure.message.id)) {
            continue;
          }
          const cleanup = removeQueuedChatMessageWhere(
            storage,
            failure.message.id,
            failure.storageEpoch,
            failure.storageRevision,
            (candidate) =>
              predicate(candidate) ||
              (deferredRemovalIntents.has(candidate.id) &&
                candidate.delivery_state !== "submitting" &&
                sameQueuedChatMessage(candidate, failure.message)),
          );
          if (cleanup.complete && !cleanup.message) {
            failures.delete(key);
          } else {
            failures.set(key, {
              message: cleanup.message ?? failure.message,
              storageEpoch: failure.storageEpoch,
              storageRevision: failure.storageRevision,
            });
          }
        }
        staleCleanupFailuresRef.current = failures;
        if (failures.size > 0) {
          let recoveredRead = readStoredSnapshotWithStatus(storage, resetEpochRead);
          let recoveredEpochRead = readResetEpoch(storage);
          if (
            !recoveredRead.complete ||
            !recoveredEpochRead.complete ||
            recoveredEpochRead.epoch !== resetEpoch
          ) {
            blockForUnknownStorage(recoveredRead.messages.values());
            return false;
          }
          const pending = new Map(pendingWritesRef.current);
          for (const message of recoveredRead.messages.values()) {
            if (!predicate(message)) continue;
            const cleanup = removeQueuedChatMessageWhere(
              storage,
              message.id,
              resetEpoch,
              message.delivery_storage_revision,
              (candidate) =>
                candidate.delivery_storage_epoch === resetEpoch && predicate(candidate),
              true,
            );
            if (cleanup.complete && !cleanup.message) pending.delete(message.id);
            else pending.set(message.id, null);
          }
          recoveredRead = readStoredSnapshotWithStatus(storage, resetEpochRead);
          recoveredEpochRead = readResetEpoch(storage);
          if (
            !recoveredRead.complete ||
            !recoveredEpochRead.complete ||
            recoveredEpochRead.epoch !== resetEpoch
          ) {
            blockForUnknownStorage(recoveredRead.messages.values());
            return false;
          }
          durableSnapshotRef.current = recoveredRead.messages;
          pendingWritesRef.current = pending;
          const recovered = new Map<string, QueuedChatMessage>();
          for (const [messageID, message] of recoveredRead.messages) {
            recovered.set(messageID, projectStoredSubmitting(message));
          }
          for (const [messageID, intent] of pending) {
            if (intent) recovered.set(messageID, storageFailedProjection(intent));
          }
          commitMessages(staleCleanupBlockedMessages(recovered.values(), failures));
          return false;
        }
        recoveredStaleCleanup = true;
        staleCleanupRemovalIntentsRef.current = new Map();
      }
      const durableRead = readStoredSnapshotWithStatus(storage);
      const durable = durableRead.messages;
      const epochAfterRead = readResetEpoch(storage);
      if (!epochAfterRead.complete) {
        blockForUnknownStorage(durable.values());
        return false;
      }
      if (epochAfterRead.epoch !== resetEpoch) {
        observeExternalReset(epochAfterRead.epoch);
        return false;
      }
      const all = new Map(durable);
      for (const message of messagesRef.current) {
        if (
          recoveredStaleCleanup &&
          deferredRemovalIntents.has(message.id) &&
          !durable.has(message.id)
        ) {
          continue;
        }
        if (!recoveredStaleCleanup || !durable.has(message.id)) all.set(message.id, message);
      }
      const survivors = new Map(all);
      const pending = new Map(pendingWritesRef.current);
      const projected = new Set(projectedSubmittingIDsRef.current);
      for (const id of deferredRemovalIntents.keys()) {
        if (!durable.has(id)) {
          pending.delete(id);
          projected.delete(id);
        }
      }
      let success = Boolean(storage && durableRead.complete);
      let currentRead = durableRead;
      for (let attempt = 0; attempt < 3; attempt += 1) {
        const candidates = attempt === 0 ? all.values() : currentRead.messages.values();
        for (const message of candidates) {
          if (!shouldDeleteCurrent(message)) continue;
          const cleanup = removeQueuedChatMessageWhere(
            storage,
            message.id,
            resetEpoch,
            message.delivery_storage_revision,
            (candidate) =>
              candidate.delivery_storage_epoch === resetEpoch && shouldDeleteCurrent(candidate),
            true,
          );
          if (cleanup.complete && !cleanup.message) {
            pending.delete(message.id);
            projected.delete(message.id);
            survivors.delete(message.id);
          } else if (cleanup.complete && cleanup.message) {
            pending.delete(message.id);
            projected.delete(message.id);
            survivors.set(message.id, projectStoredSubmitting(cleanup.message));
          } else {
            success = false;
            pending.set(message.id, null);
            survivors.set(message.id, cleanupFailedProjection(cleanup.message ?? message));
          }
        }

        currentRead = readStoredSnapshotWithStatus(storage);
        if (!currentRead.complete) success = false;
        const currentEpochRead = readResetEpoch(storage);
        if (!currentEpochRead.complete) {
          blockForUnknownStorage([...survivors.values(), ...currentRead.messages.values()]);
          return false;
        }
        if (currentEpochRead.epoch !== resetEpoch) {
          observeExternalReset(currentEpochRead.epoch);
          return false;
        }
        if (![...currentRead.messages.values()].some(shouldDeleteCurrent)) break;
        if (attempt === 2) success = false;
      }

      for (const [id, stored] of currentRead.messages) {
        if (shouldDeleteCurrent(stored)) {
          success = false;
          pending.set(id, null);
          survivors.set(id, cleanupFailedProjection(stored));
          continue;
        }
        if (deferredRemovalIntents.has(id) && !predicate(stored)) {
          pending.delete(id);
          survivors.set(id, conflictProjection(stored));
          if (stored.delivery_state === "submitting") projected.add(id);
          else projected.delete(id);
          continue;
        }
        const current = survivors.get(id);
        if (!current) {
          survivors.set(id, projectStoredSubmitting(stored));
          if (stored.delivery_state === "submitting") projected.add(id);
          else projected.delete(id);
        } else if (!sameQueuedChatMessage(current, stored)) {
          survivors.set(id, conflictProjection(stored));
          if (stored.delivery_state === "submitting") projected.add(id);
          else projected.delete(id);
        }
      }

      for (const [id, message] of survivors) {
        const sessionDeletionStatus = queuedChatSessionDeletionStatus(
          storage,
          message.session_id,
          resetEpochRead,
        );
        if (sessionDeletionStatus !== "active") {
          if (sessionDeletionStatus === "deleted") {
            const cleanup = removeQueuedChatMessageWhere(
              storage,
              id,
              resetEpoch,
              message.delivery_storage_revision,
              (candidate) => candidate.session_id === message.session_id,
            );
            if (cleanup.complete && !cleanup.message) {
              survivors.delete(id);
              pending.delete(id);
              projected.delete(id);
              continue;
            }
          }
          success = false;
          pending.set(id, null);
          projected.delete(id);
          survivors.set(id, cleanupFailedProjection(message));
          continue;
        }
        const deletionStatus = queuedChatProjectDeletionStatus(
          storage,
          message.project_id,
          resetEpochRead,
        );
        if (deletionStatus === "active") continue;
        if (deletionStatus === "deleted") {
          const cleanup = removeQueuedChatMessageWhere(
            storage,
            id,
            resetEpoch,
            message.delivery_storage_revision,
            (candidate) => candidate.project_id === message.project_id,
          );
          if (cleanup.complete && !cleanup.message) {
            survivors.delete(id);
            pending.delete(id);
            projected.delete(id);
            continue;
          }
        }
        success = false;
        pending.set(id, null);
        projected.delete(id);
        survivors.set(id, cleanupFailedProjection(message));
      }

      durableSnapshotRef.current = currentRead.messages;
      pendingWritesRef.current = pending;
      projectedSubmittingIDsRef.current = projected;
      commitMessages(survivors.values());
      return success;
    },
    [blockForStaleCleanupFailures, blockForUnknownStorage, commitMessages, observeExternalReset],
  );

  const deleteProjectWhere = useCallback(
    (projectID: string, predicate: (message: QueuedChatMessage) => boolean): boolean => {
      const id = projectID.trim();
      const storage = browserStorage();
      if (!id || !storage || externalResetObservedRef.current) return false;
      const resetEpochRead = readResetEpoch(storage);
      if (!resetEpochRead.complete || !resetEpochKnownRef.current) {
        blockForUnknownStorage();
        return false;
      }
      const resetEpoch = resetEpochRead.epoch;
      if (resetEpoch !== resetEpochRef.current) {
        observeExternalReset(resetEpoch);
        return false;
      }
      tombstoneQueuedChatProject(storage, id, resetEpoch);
      const resetEpochAfterRead = readResetEpoch(storage);
      if (!resetEpochAfterRead.complete) {
        blockForUnknownStorage();
        return false;
      }
      if (resetEpochAfterRead.epoch !== resetEpoch) {
        removeQueuedChatProjectTombstoneFromEpoch(storage, id, resetEpoch);
        observeExternalReset(resetEpochAfterRead.epoch);
        return false;
      }
      const initialDeletionStatus = queuedChatProjectDeletionStatus(
        storage,
        id,
        resetEpochAfterRead,
      );
      if (initialDeletionStatus !== "deleted") {
        // Never create a crash window in which prompts are gone but no
        // durable project-deletion fence exists for stale tabs and reloads.
        blockForUnknownStorage();
        return false;
      }
      if (!deleteWhere(predicate)) return false;

      const finalEpochRead = readResetEpoch(storage);
      if (!finalEpochRead.complete) {
        blockForUnknownStorage();
        return false;
      }
      if (finalEpochRead.epoch !== resetEpoch) {
        removeQueuedChatProjectTombstoneFromEpoch(storage, id, resetEpoch);
        observeExternalReset(finalEpochRead.epoch);
        return false;
      }
      const deletionStatus = queuedChatProjectDeletionStatus(storage, id, finalEpochRead);
      if (deletionStatus !== "deleted") {
        blockForUnknownStorage();
        return false;
      }
      return true;
    },
    [blockForUnknownStorage, deleteWhere, observeExternalReset],
  );

  const deleteSession = useCallback(
    (sessionID: string): boolean => {
      const id = sessionID.trim();
      const storage = browserStorage();
      if (!id || !storage || externalResetObservedRef.current) return false;
      const resetEpochRead = readResetEpoch(storage);
      if (!resetEpochRead.complete || !resetEpochKnownRef.current) {
        blockForUnknownStorage();
        return false;
      }
      const resetEpoch = resetEpochRead.epoch;
      if (resetEpoch !== resetEpochRef.current) {
        observeExternalReset(resetEpoch);
        return false;
      }
      tombstoneQueuedChatSession(storage, id, resetEpoch);
      const resetEpochAfterRead = readResetEpoch(storage);
      if (!resetEpochAfterRead.complete) {
        blockForUnknownStorage();
        return false;
      }
      if (resetEpochAfterRead.epoch !== resetEpoch) {
        removeQueuedChatSessionTombstoneFromEpoch(storage, id, resetEpoch);
        observeExternalReset(resetEpochAfterRead.epoch);
        return false;
      }
      const initialDeletionStatus = queuedChatSessionDeletionStatus(
        storage,
        id,
        resetEpochAfterRead,
      );
      if (initialDeletionStatus !== "deleted") {
        // A durable fence must precede prompt cleanup so a stale tab cannot
        // re-enqueue against a server-deleted session in the crash window.
        blockForUnknownStorage();
        return false;
      }
      if (!deleteWhere((message) => message.session_id === id)) return false;

      const finalEpochRead = readResetEpoch(storage);
      if (!finalEpochRead.complete) {
        blockForUnknownStorage();
        return false;
      }
      if (finalEpochRead.epoch !== resetEpoch) {
        removeQueuedChatSessionTombstoneFromEpoch(storage, id, resetEpoch);
        observeExternalReset(finalEpochRead.epoch);
        return false;
      }
      const deletionStatus = queuedChatSessionDeletionStatus(storage, id, finalEpochRead);
      if (deletionStatus !== "deleted") {
        blockForUnknownStorage();
        return false;
      }
      return true;
    },
    [blockForUnknownStorage, deleteWhere, observeExternalReset],
  );

  const clear = useCallback((): boolean => {
    const storage = browserStorage();
    if (!storage) {
      blockForUnknownStorage();
      return false;
    }

    let success = true;
    // Capture only records that existed before the new reset generation. A
    // fresh tab may legitimately enqueue after the epoch advances; cleanup
    // must never erase that newly admitted prompt with a later broad scan.
    const oldRecords = new Map<string, string>();
    const oldRecordScan = queuedChatStorageKeys(storage);
    if (!oldRecordScan.complete) success = false;
    const oldDeletedProjectScan = queuedChatDeletedProjectStorageKeys(storage);
    if (!oldDeletedProjectScan.complete) success = false;
    const oldDeletedSessionScan = queuedChatDeletedSessionStorageKeys(storage);
    if (!oldDeletedSessionScan.complete) success = false;
    const oldMigratedLegacyScan = queuedChatMigratedLegacyStorageKeys(storage);
    if (!oldMigratedLegacyScan.complete) success = false;
    const oldDeletedItemScan = queuedChatDeletedItemStorageKeys(storage);
    if (!oldDeletedItemScan.complete) success = false;
    for (const key of [
      ...oldRecordScan.keys,
      ...oldDeletedProjectScan.keys,
      ...oldDeletedSessionScan.keys,
      ...oldMigratedLegacyScan.keys,
      ...oldDeletedItemScan.keys,
    ]) {
      try {
        const raw = storage.getItem(key);
        if (raw !== null) oldRecords.set(key, raw);
      } catch (error) {
        success = false;
        logWarn(`queued chat storage: reset snapshot failed for ${key}:`, error);
      }
    }
    let oldLegacyRecord: string | null = null;
    try {
      oldLegacyRecord = storage.getItem(queuedChatMessagesStorageKey);
    } catch (error) {
      success = false;
      logWarn("queued chat storage: reset legacy snapshot failed:", error);
    }

    const epoch = newResetEpoch();
    let epochEstablished = false;
    try {
      storage.setItem(queuedChatMessagesResetEpochStorageKey, epoch);
      epochEstablished = storage.getItem(queuedChatMessagesResetEpochStorageKey) === epoch;
      if (!epochEstablished) success = false;
    } catch (error) {
      success = false;
      logWarn("queued chat storage: reset epoch write failed:", error);
    }
    if (!epochEstablished) {
      const raw = readStoredSnapshotWithStatus(storage, null);
      blockForUnknownStorage(raw.messages.values());
      return false;
    }
    resetEpochRef.current = epoch;
    resetEpochKnownRef.current = true;
    externalResetObservedRef.current = false;
    storageSnapshotConflictRef.current = false;

    const failedOldItemIDs = new Set<string>();
    const failedCleanupMessages = new Map<string, QueuedChatMessage>();
    const failedStaleCleanupRecords: StaleCleanupFailures = new Map();
    const removeRawKeyIfUnchanged = (key: string, expected: string) => {
      if (!removeStorageItemIfUnchanged(storage, key, expected)) {
        success = false;
        const identity = queuedChatMessageIdentityFromStorageKey(key);
        if (identity?.id) {
          failedOldItemIDs.add(identity.id);
          const message = parseQueuedChatMessageRecord(expected, true);
          if (message?.id === identity.id) {
            failedCleanupMessages.set(identity.id, message);
            if (identity.storageEpoch !== epoch) {
              failedStaleCleanupRecords.set(key, {
                message,
                storageEpoch: identity.storageEpoch,
                storageRevision: identity.storageRevision,
              });
            }
          }
        }
      }
    };
    for (const [key, raw] of oldRecords) removeRawKeyIfUnchanged(key, raw);
    if (oldLegacyRecord !== null) {
      removeRawKeyIfUnchanged(queuedChatMessagesStorageKey, oldLegacyRecord);
    }
    try {
      storage.setItem(queuedChatMessagesV2MarkerStorageKey, queuedChatMessagesV2MarkerValue);
      if (
        storage.getItem(queuedChatMessagesV2MarkerStorageKey) !== queuedChatMessagesV2MarkerValue
      ) {
        success = false;
      }
    } catch (error) {
      success = false;
      logWarn("queued chat storage: reset marker write failed:", error);
    }

    const postAuditItems = queuedChatStorageKeys(storage);
    if (!postAuditItems.complete) success = false;
    for (const key of postAuditItems.keys) {
      const identity = queuedChatMessageIdentityFromStorageKey(key);
      if (
        !identity?.id ||
        queuedChatMessageStorageKeyForEpoch(
          identity.id,
          identity.storageEpoch,
          identity.storageRevision,
        ) !== key
      ) {
        success = false;
        continue;
      }
      const record = readQueuedChatMessageRecordWithStatus(
        storage,
        identity.id,
        identity.storageEpoch,
        identity.storageRevision,
      );
      if (record.status === "absent") continue;
      if (record.status === "unknown") {
        success = false;
        failedOldItemIDs.add(identity.id);
        const raw = readStorageItemWithStatus(storage, key);
        const message = raw.complete ? parseQueuedChatMessageRecord(raw.value, true) : null;
        if (message?.id === identity.id) {
          failedCleanupMessages.set(identity.id, message);
          if (identity.storageEpoch !== epoch) {
            failedStaleCleanupRecords.set(key, {
              message,
              storageEpoch: identity.storageEpoch,
              storageRevision: identity.storageRevision,
            });
          }
        }
        continue;
      }
      if (identity.storageEpoch === epoch) continue;
      const cleanupEpochRead = readResetEpoch(storage);
      if (!cleanupEpochRead.complete) {
        blockForUnknownStorage();
        return false;
      }
      if (cleanupEpochRead.epoch !== epoch) {
        observeExternalReset(cleanupEpochRead.epoch);
        return false;
      }
      const cleanup = removeQueuedChatMessageWhere(
        storage,
        identity.id,
        identity.storageEpoch,
        identity.storageRevision,
        () => true,
      );
      if (!cleanup.complete) {
        success = false;
        failedOldItemIDs.add(identity.id);
        if (cleanup.message) {
          failedCleanupMessages.set(identity.id, cleanup.message);
          failedStaleCleanupRecords.set(key, {
            message: cleanup.message,
            storageEpoch: identity.storageEpoch,
            storageRevision: identity.storageRevision,
          });
        }
      }
    }

    const postAuditTombstones = queuedChatDeletedProjectStorageKeys(storage);
    if (!postAuditTombstones.complete) success = false;
    for (const key of postAuditTombstones.keys) {
      const identity = queuedChatDeletedProjectIdentityFromStorageKey(key);
      const canonicalKey = identity
        ? identity.storageEpoch === undefined
          ? legacyQueuedChatDeletedProjectStorageKey(identity.id)
          : queuedChatDeletedProjectStorageKey(identity.id, identity.storageEpoch)
        : null;
      if (!identity?.id || canonicalKey !== key) {
        success = false;
        continue;
      }
      const read = readStorageItemWithStatus(storage, key);
      if (!read.complete || read.value === null) {
        if (!read.complete) success = false;
        continue;
      }
      const markerEpoch = parseQueuedChatDeletedProjectEpoch(read.value);
      if (identity.storageEpoch === epoch) {
        if (markerEpoch !== epoch) success = false;
        continue;
      }
      const cleanupEpochRead = readResetEpoch(storage);
      if (!cleanupEpochRead.complete) {
        blockForUnknownStorage();
        return false;
      }
      if (cleanupEpochRead.epoch !== epoch) {
        observeExternalReset(cleanupEpochRead.epoch);
        return false;
      }
      if (!removeStorageItemIfUnchanged(storage, key, read.value)) {
        success = false;
      }
    }

    const postAuditSessionTombstones = queuedChatDeletedSessionStorageKeys(storage);
    if (!postAuditSessionTombstones.complete) success = false;
    for (const key of postAuditSessionTombstones.keys) {
      const identity = queuedChatDeletedSessionIdentityFromStorageKey(key);
      const canonicalKey =
        identity?.storageEpoch !== undefined
          ? queuedChatDeletedSessionStorageKey(identity.id, identity.storageEpoch)
          : null;
      if (!identity?.id || canonicalKey !== key) {
        success = false;
        continue;
      }
      const read = readStorageItemWithStatus(storage, key);
      if (!read.complete || read.value === null) {
        if (!read.complete) success = false;
        continue;
      }
      const markerEpoch = parseQueuedChatDeletedSessionEpoch(read.value);
      if (identity.storageEpoch === epoch) {
        if (markerEpoch !== epoch) success = false;
        continue;
      }
      const cleanupEpochRead = readResetEpoch(storage);
      if (!cleanupEpochRead.complete) {
        blockForUnknownStorage();
        return false;
      }
      if (cleanupEpochRead.epoch !== epoch) {
        observeExternalReset(cleanupEpochRead.epoch);
        return false;
      }
      if (!removeStorageItemIfUnchanged(storage, key, read.value)) {
        success = false;
      }
    }

    const postAuditItemTombstones = queuedChatDeletedItemStorageKeys(storage);
    if (!postAuditItemTombstones.complete) success = false;
    for (const key of postAuditItemTombstones.keys) {
      const identity = queuedChatDeletedItemIdentityFromStorageKey(key);
      const canonicalKey = identity
        ? queuedChatDeletedItemStorageKey(identity.id, identity.storageEpoch, identity.fingerprint)
        : null;
      if (!identity || canonicalKey !== key) {
        success = false;
        continue;
      }
      const read = readStorageItemWithStatus(storage, key);
      if (!read.complete || read.value === null) {
        if (!read.complete) success = false;
        continue;
      }
      const validMarker =
        read.value === queuedChatDeletedItemMarker(identity.storageEpoch, identity.fingerprint);
      if (identity.storageEpoch === epoch) {
        if (!validMarker) success = false;
        continue;
      }
      const cleanupEpochRead = readResetEpoch(storage);
      if (!cleanupEpochRead.complete) {
        blockForUnknownStorage();
        return false;
      }
      if (cleanupEpochRead.epoch !== epoch) {
        observeExternalReset(cleanupEpochRead.epoch);
        return false;
      }
      if (!validMarker || !removeStorageItemIfUnchanged(storage, key, read.value)) {
        success = false;
      }
    }

    const postAuditMigrationMarkers = queuedChatMigratedLegacyStorageKeys(storage);
    if (!postAuditMigrationMarkers.complete) success = false;
    for (const key of postAuditMigrationMarkers.keys) {
      const identity = queuedChatStorageIdentityFromKey(
        key,
        queuedChatMigratedLegacyStorageKeyPrefix,
      );
      const canonicalKey = identity?.storageEpoch
        ? queuedChatMigratedLegacyStorageKey(identity.id, identity.storageEpoch)
        : null;
      if (!identity?.id || canonicalKey !== key) {
        success = false;
        continue;
      }
      const read = readStorageItemWithStatus(storage, key);
      if (!read.complete || read.value === null) {
        if (!read.complete) success = false;
        continue;
      }
      const validMarker =
        read.value.startsWith(queuedChatMigratedLegacyMarkerPrefix) &&
        /^[0-9a-f]{64}$/.test(read.value.slice(queuedChatMigratedLegacyMarkerPrefix.length));
      if (identity.storageEpoch === epoch) {
        if (!validMarker) success = false;
        continue;
      }
      const cleanupEpochRead = readResetEpoch(storage);
      if (!cleanupEpochRead.complete) {
        blockForUnknownStorage();
        return false;
      }
      if (cleanupEpochRead.epoch !== epoch) {
        observeExternalReset(cleanupEpochRead.epoch);
        return false;
      }
      if (!removeStorageItemIfUnchanged(storage, key, read.value)) success = false;
    }

    const durableAfterRead = readStoredSnapshotWithStatus(storage, {
      epoch,
      complete: true,
      present: true,
    });
    if (!durableAfterRead.complete) success = false;
    const durableAfter = durableAfterRead.messages;
    for (const [id, message] of durableAfter) {
      const deletionStatus = queuedChatSessionDeletionStatus(storage, message.session_id, {
        epoch,
        complete: true,
        present: true,
      });
      if (deletionStatus === "active") continue;
      const cleanup =
        deletionStatus === "deleted"
          ? removeQueuedChatMessageWhere(
              storage,
              id,
              epoch,
              message.delivery_storage_revision,
              (candidate) =>
                candidate.delivery_storage_epoch === epoch &&
                candidate.session_id === message.session_id,
            )
          : { message, complete: false };
      if (deletionStatus === "deleted" && cleanup.complete && !cleanup.message) {
        durableAfter.delete(id);
      } else {
        success = false;
        failedOldItemIDs.add(id);
        failedCleanupMessages.set(id, cleanup.message ?? message);
      }
    }
    const finalEpochRead = readResetEpoch(storage);
    if (!finalEpochRead.complete) {
      blockForUnknownStorage(durableAfter.values());
      return false;
    }
    if (finalEpochRead.epoch !== epoch) {
      observeExternalReset(finalEpochRead.epoch);
      return false;
    }

    const cleanupFailedItemIDs = new Set(failedOldItemIDs);
    for (const message of durableAfter.values()) {
      if (message.delivery_storage_failed) cleanupFailedItemIDs.add(message.id);
    }

    const localAfter = new Map(durableAfter);
    for (const [id, failed] of failedCleanupMessages) {
      localAfter.set(id, durableAfter.get(id) ?? failed);
    }

    // A pre-v2 tab can rewrite the prompt-bearing whole-array key after the
    // reset snapshot or during the keyed post-audit above. It is never a valid
    // new-generation write, so prove its absence at the final state-commit
    // boundary. Bounded retries turn a tab that keeps replacing the value into
    // an explicit reset failure instead of claiming browser plaintext cleared.
    let legacyQueueCleared = false;
    for (let attempt = 0; attempt < 3; attempt += 1) {
      const legacyRead = readStorageItemWithStatus(storage, queuedChatMessagesStorageKey);
      if (!legacyRead.complete) break;
      if (legacyRead.value === null) {
        legacyQueueCleared = true;
        break;
      }
      removeStorageItemIfUnchanged(storage, queuedChatMessagesStorageKey, legacyRead.value);
    }
    const finalLegacyRead = readStorageItemWithStatus(storage, queuedChatMessagesStorageKey);
    if (!finalLegacyRead.complete || finalLegacyRead.value !== null) {
      success = false;
    } else {
      legacyQueueCleared = true;
    }
    if (!legacyQueueCleared) success = false;

    const epochAfterLegacyAudit = readResetEpoch(storage);
    if (!epochAfterLegacyAudit.complete) {
      blockForUnknownStorage(durableAfter.values());
      return false;
    }
    if (epochAfterLegacyAudit.epoch !== epoch) {
      observeExternalReset(epochAfterLegacyAudit.epoch);
      return false;
    }

    durableSnapshotRef.current = durableAfter;
    staleCleanupFailuresRef.current = failedStaleCleanupRecords;
    staleCleanupRemovalIntentsRef.current = new Map();
    pendingWritesRef.current = new Map(
      [...cleanupFailedItemIDs].map((id) => [id, null as PendingRecord]),
    );
    projectedSubmittingIDsRef.current = new Set(
      [...durableAfter.values()]
        .filter((message) => message.delivery_state === "submitting")
        .map((message) => message.id),
    );
    commitMessages(
      [...localAfter.values()].map((message) =>
        cleanupFailedItemIDs.has(message.id)
          ? cleanupFailedProjection(message)
          : projectStoredSubmitting(message),
      ),
    );
    return success;
  }, [blockForUnknownStorage, commitMessages, observeExternalReset]);

  const hasDurableSubmittingFence = useCallback((queued: QueuedChatMessage) => {
    if (projectedSubmittingIDsRef.current.has(queued.id)) return false;
    if (staleCleanupFailuresRef.current.size > 0) return false;
    const storage = browserStorage();
    if (!storage) return false;
    const epochRead = readResetEpoch(storage);
    if (
      externalResetObservedRef.current ||
      !resetEpochKnownRef.current ||
      !epochRead.complete ||
      epochRead.epoch !== resetEpochRef.current
    ) {
      return false;
    }
    if (queuedChatSessionDeletionStatus(storage, queued.session_id, epochRead) !== "active") {
      return false;
    }
    if (queuedChatProjectDeletionStatus(storage, queued.project_id, epochRead) !== "active") {
      return false;
    }
    if (queued.delivery_storage_epoch !== epochRead.epoch) return false;
    const storedRead = readStoredSnapshotWithStatus(storage, epochRead);
    if (!storedRead.complete) return false;
    const finalEpochRead = readResetEpoch(storage);
    if (
      !finalEpochRead.complete ||
      finalEpochRead.epoch !== epochRead.epoch ||
      queuedChatSessionDeletionStatus(storage, queued.session_id, finalEpochRead) !== "active" ||
      queuedChatProjectDeletionStatus(storage, queued.project_id, finalEpochRead) !== "active"
    ) {
      return false;
    }
    const stored = storedRead.messages.get(queued.id);
    return (
      stored?.delivery_storage_epoch === epochRead.epoch && submittingFenceMatches(stored, queued)
    );
  }, []);

  useEffect(() => {
    const storage = browserStorage();
    if (!storage) return;
    const onStorage = (event: StorageEvent) => {
      if (event.storageArea && event.storageArea !== storage) return;
      const eventKey = event.key ?? "";
      const deletedSessionIdentity = queuedChatDeletedSessionIdentityFromStorageKey(eventKey);
      if (eventKey.startsWith(queuedChatDeletedSessionStorageKeyPrefix)) {
        if (
          !deletedSessionIdentity?.id ||
          deletedSessionIdentity.storageEpoch === undefined ||
          event.newValue === null
        ) {
          if (event.newValue !== null) blockForUnknownStorage();
          return;
        }
        const currentEpochRead = readResetEpoch(storage);
        if (!currentEpochRead.complete) {
          blockForUnknownStorage();
          return;
        }
        const belongsToCurrentGeneration =
          deletedSessionIdentity.storageEpoch === currentEpochRead.epoch;
        if (!belongsToCurrentGeneration) return;
        const deletedSessionID = deletedSessionIdentity.id;
        const deletionStatus = queuedChatSessionDeletionStatus(
          storage,
          deletedSessionID,
          currentEpochRead,
        );
        if (deletionStatus === "active") return;
        if (deletionStatus === "deleted") {
          deleteWhere((message) => message.session_id === deletedSessionID);
        } else {
          const pending = new Map(pendingWritesRef.current);
          const blocked = messagesRef.current.map((message) => {
            if (message.session_id !== deletedSessionID) return message;
            pending.set(message.id, null);
            return cleanupFailedProjection(message);
          });
          pendingWritesRef.current = pending;
          commitMessages(blocked);
        }
        return;
      }
      const deletedProjectIdentity = queuedChatDeletedProjectIdentityFromStorageKey(eventKey);
      if (deletedProjectIdentity?.id && event.newValue !== null) {
        const currentEpochRead = readResetEpoch(storage);
        const belongsToCurrentGeneration =
          currentEpochRead.complete &&
          (deletedProjectIdentity.storageEpoch === currentEpochRead.epoch ||
            (deletedProjectIdentity.storageEpoch === undefined && currentEpochRead.epoch === "0"));
        if (!belongsToCurrentGeneration) return;
        const deletedProjectID = deletedProjectIdentity.id;
        const deletionStatus = queuedChatProjectDeletionStatus(
          storage,
          deletedProjectID,
          currentEpochRead,
        );
        if (deletionStatus === "active") return;
        if (deletionStatus === "deleted") {
          deleteWhere((message) => message.project_id === deletedProjectID);
        } else {
          const pending = new Map(pendingWritesRef.current);
          const blocked = messagesRef.current.map((message) => {
            if (message.project_id !== deletedProjectID && message.project_id !== undefined) {
              return message;
            }
            pending.set(message.id, null);
            return cleanupFailedProjection(message);
          });
          pendingWritesRef.current = pending;
          commitMessages(blocked);
        }
        return;
      }
      if (event.key === queuedChatMessagesResetEpochStorageKey) {
        const epochRead = readResetEpoch(storage);
        if (!epochRead.complete) {
          blockForUnknownStorage();
        } else if (!resetEpochKnownRef.current || epochRead.epoch !== resetEpochRef.current) {
          observeExternalReset(epochRead.epoch);
        }
        return;
      }
      const epochRead = readResetEpoch(storage);
      const itemIdentity = queuedChatMessageIdentityFromStorageKey(eventKey);
      const migratedLegacyIdentity = queuedChatStorageIdentityFromKey(
        eventKey,
        queuedChatMigratedLegacyStorageKeyPrefix,
      );
      const deletedItemIdentity = queuedChatDeletedItemIdentityFromStorageKey(eventKey);
      const id = itemIdentity?.id ?? migratedLegacyIdentity?.id ?? deletedItemIdentity?.id ?? "";
      if (!epochRead.complete || !resetEpochKnownRef.current) {
        const eventMessage = itemIdentity
          ? parseQueuedChatMessageRecord(event.newValue, true)
          : null;
        blockForUnknownStorage(eventMessage ? [eventMessage] : []);
        return;
      }
      const epoch = epochRead.epoch;
      if (epoch !== resetEpochRef.current) {
        observeExternalReset(epoch);
        return;
      }
      if (externalResetObservedRef.current) return;
      if (!id) return;
      const canonicalItemKey = itemIdentity
        ? queuedChatMessageStorageKeyForEpoch(
            id,
            itemIdentity.storageEpoch,
            itemIdentity.storageRevision,
          )
        : null;
      const canonicalMigratedLegacyKey = migratedLegacyIdentity?.storageEpoch
        ? queuedChatMigratedLegacyStorageKey(id, migratedLegacyIdentity.storageEpoch)
        : null;
      const canonicalDeletedItemKey = deletedItemIdentity
        ? queuedChatDeletedItemStorageKey(
            id,
            deletedItemIdentity.storageEpoch,
            deletedItemIdentity.fingerprint,
          )
        : null;
      if (
        canonicalItemKey !== eventKey &&
        canonicalMigratedLegacyKey !== eventKey &&
        canonicalDeletedItemKey !== eventKey
      ) {
        blockForUnknownStorage();
        return;
      }
      const resolveStaleCleanupFailure = () => {
        if (!staleCleanupFailuresRef.current.has(eventKey)) return;
        const failures = new Map(staleCleanupFailuresRef.current);
        failures.delete(eventKey);
        staleCleanupFailuresRef.current = failures;
        if (failures.size > 0) {
          blockForStaleCleanupFailures();
          return;
        }
        const removalIntents = new Map(staleCleanupRemovalIntentsRef.current);
        staleCleanupRemovalIntentsRef.current = new Map();
        let durableRead = readStoredSnapshotWithStatus(storage, epochRead);
        let finalEpochRead = readResetEpoch(storage);
        if (!durableRead.complete || !finalEpochRead.complete || finalEpochRead.epoch !== epoch) {
          blockForUnknownStorage(durableRead.messages.values());
          return;
        }
        const pending = new Map(pendingWritesRef.current);
        const conflictedRemovalIDs = new Set<string>();
        for (const [removalID, expected] of removalIntents) {
          const stored = durableRead.messages.get(removalID);
          if (!stored) {
            pending.delete(removalID);
            continue;
          }
          if (!expected || !sameQueuedChatRemovalTarget(stored, expected)) {
            pending.delete(removalID);
            conflictedRemovalIDs.add(removalID);
            continue;
          }
          // A deferred UI removal is no longer authoritative once another
          // tab has established an ambiguous submitting fence. Preserve it
          // for explicit reconciliation instead of deleting it by id.
          if (stored.delivery_state === "submitting") {
            pending.delete(removalID);
            conflictedRemovalIDs.add(removalID);
            continue;
          }
          const cleanup = removeQueuedChatMessageWhere(
            storage,
            removalID,
            epoch,
            stored.delivery_storage_revision,
            (candidate) =>
              candidate.delivery_storage_epoch === epoch &&
              candidate.delivery_state !== "submitting" &&
              sameQueuedChatRemovalTarget(candidate, stored),
            true,
          );
          if (cleanup.complete) {
            pending.delete(removalID);
            if (cleanup.message) conflictedRemovalIDs.add(removalID);
          } else {
            pending.set(removalID, null);
          }
        }
        durableRead = readStoredSnapshotWithStatus(storage, epochRead);
        finalEpochRead = readResetEpoch(storage);
        if (!durableRead.complete || !finalEpochRead.complete || finalEpochRead.epoch !== epoch) {
          blockForUnknownStorage(durableRead.messages.values());
          return;
        }
        for (const removalID of removalIntents.keys()) {
          if (durableRead.messages.has(removalID)) conflictedRemovalIDs.add(removalID);
        }
        // An ownership tombstone may have arrived while an unrelated stale-key
        // failure globally blocked cleanup. Reapply durable session and
        // project ownership before any rows become drainable again.
        for (const [messageID, message] of durableRead.messages) {
          const sessionDeletionStatus = queuedChatSessionDeletionStatus(
            storage,
            message.session_id,
            epochRead,
          );
          if (sessionDeletionStatus !== "active") {
            if (sessionDeletionStatus === "deleted") {
              const cleanup = removeQueuedChatMessageWhere(
                storage,
                messageID,
                epoch,
                message.delivery_storage_revision,
                (candidate) =>
                  candidate.delivery_storage_epoch === epoch &&
                  candidate.session_id === message.session_id,
              );
              if (cleanup.complete && !cleanup.message) {
                pending.delete(messageID);
                conflictedRemovalIDs.delete(messageID);
                continue;
              }
            }
            pending.set(messageID, null);
            continue;
          }
          const deletionStatus = queuedChatProjectDeletionStatus(
            storage,
            message.project_id,
            epochRead,
          );
          if (deletionStatus === "active") continue;
          if (deletionStatus === "deleted") {
            const cleanup = removeQueuedChatMessageWhere(
              storage,
              messageID,
              epoch,
              message.delivery_storage_revision,
              (candidate) =>
                candidate.delivery_storage_epoch === epoch &&
                candidate.project_id === message.project_id,
            );
            if (cleanup.complete && !cleanup.message) {
              pending.delete(messageID);
              conflictedRemovalIDs.delete(messageID);
              continue;
            }
          }
          pending.set(messageID, null);
        }
        durableRead = readStoredSnapshotWithStatus(storage, epochRead);
        finalEpochRead = readResetEpoch(storage);
        if (!durableRead.complete || !finalEpochRead.complete || finalEpochRead.epoch !== epoch) {
          blockForUnknownStorage(durableRead.messages.values());
          return;
        }
        durableSnapshotRef.current = durableRead.messages;
        pendingWritesRef.current = pending;
        projectedSubmittingIDsRef.current = new Set(
          [...durableRead.messages.values()]
            .filter((message) => message.delivery_state === "submitting")
            .map((message) => message.id),
        );
        const local = new Map<string, QueuedChatMessage>();
        for (const [messageID, message] of durableRead.messages) {
          local.set(
            messageID,
            conflictedRemovalIDs.has(messageID)
              ? conflictProjection(message)
              : projectStoredSubmitting(message),
          );
        }
        for (const [messageID, intent] of pending) {
          if (intent) {
            local.set(messageID, storageFailedProjection(intent));
          } else {
            const durable = durableRead.messages.get(messageID);
            if (durable) local.set(messageID, cleanupFailedProjection(durable));
          }
        }
        commitMessages(local.values());
      };

      const eventStorageEpoch =
        itemIdentity?.storageEpoch ??
        migratedLegacyIdentity?.storageEpoch ??
        deletedItemIdentity?.storageEpoch;
      const belongsToCurrentGeneration =
        eventStorageEpoch === epoch || (eventStorageEpoch === undefined && epoch === "0");
      if (!belongsToCurrentGeneration) {
        if (!itemIdentity) return;
        const staleRead = readQueuedChatMessageRecordWithStatus(
          storage,
          id,
          itemIdentity?.storageEpoch,
          itemIdentity?.storageRevision,
        );
        if (staleRead.status === "present") {
          const cleanup = removeQueuedChatMessageWhere(
            storage,
            id,
            itemIdentity?.storageEpoch,
            itemIdentity?.storageRevision,
            () => true,
          );
          if (!cleanup.complete) {
            const failures = new Map(staleCleanupFailuresRef.current);
            failures.set(eventKey, {
              message: cleanup.message ?? staleRead.message,
              storageEpoch: itemIdentity?.storageEpoch,
              storageRevision: itemIdentity?.storageRevision,
            });
            staleCleanupFailuresRef.current = failures;
            blockForStaleCleanupFailures();
          } else {
            resolveStaleCleanupFailure();
          }
        } else if (staleRead.status === "unknown") {
          const eventMessage = parseQueuedChatMessageRecord(event.newValue, true);
          const current = messagesRef.current.find((message) => message.id === id);
          const failureMessage = eventMessage ?? current;
          if (!failureMessage) {
            blockForUnknownStorage();
            return;
          }
          const failures = new Map(staleCleanupFailuresRef.current);
          failures.set(eventKey, {
            message: failureMessage,
            storageEpoch: itemIdentity?.storageEpoch,
            storageRevision: itemIdentity?.storageRevision,
          });
          staleCleanupFailuresRef.current = failures;
          blockForStaleCleanupFailures();
        } else {
          resolveStaleCleanupFailure();
        }
        return;
      }

      const logicalRead = readStoredSnapshotWithStatus(storage, epochRead);
      const logicalEpochRead = readResetEpoch(storage);
      if (!logicalEpochRead.complete) {
        blockForUnknownStorage(logicalRead.messages.values());
        return;
      }
      if (logicalEpochRead.epoch !== epoch) {
        observeExternalReset(logicalEpochRead.epoch);
        return;
      }
      if (!logicalRead.complete) {
        const blocked = new Map(messagesRef.current.map((message) => [message.id, message]));
        for (const message of logicalRead.messages.values()) {
          blocked.set(message.id, message);
        }
        const eventMessage = itemIdentity
          ? parseQueuedChatMessageRecord(event.newValue, true)
          : null;
        if (eventMessage) blocked.set(eventMessage.id, eventMessage);
        storageSnapshotConflictRef.current = true;
        projectedSubmittingIDsRef.current = new Set();
        commitMessages([...blocked.values()].map(cleanupFailedProjection));
        return;
      }
      const durable = logicalRead.messages.get(id) ?? null;
      const sessionDeletionStatus = durable
        ? queuedChatSessionDeletionStatus(storage, durable.session_id, epochRead)
        : "active";
      if (durable && sessionDeletionStatus === "deleted") {
        deleteWhere((message) => message.session_id === durable.session_id);
        return;
      }
      const projectDeletionStatus = durable
        ? queuedChatProjectDeletionStatus(storage, durable.project_id, epochRead)
        : "active";
      if (durable && projectDeletionStatus === "deleted") {
        deleteWhere((message) => message.project_id === durable.project_id);
        return;
      }
      if (storageSnapshotConflictRef.current) {
        storageSnapshotConflictRef.current = false;
        const pending = new Map(pendingWritesRef.current);
        const recovered = new Map<string, QueuedChatMessage>();
        for (const [messageID, message] of logicalRead.messages) {
          recovered.set(messageID, projectStoredSubmitting(message));
        }
        for (const [messageID, intent] of pending) {
          if (intent) {
            recovered.set(messageID, storageFailedProjection(intent));
          } else {
            const durableMessage = logicalRead.messages.get(messageID);
            if (durableMessage) {
              recovered.set(messageID, cleanupFailedProjection(durableMessage));
            }
          }
        }
        durableSnapshotRef.current = new Map(logicalRead.messages);
        projectedSubmittingIDsRef.current = new Set(
          [...logicalRead.messages.values()]
            .filter((message) => message.delivery_state === "submitting")
            .map((message) => message.id),
        );
        commitMessages(recovered.values());
        return;
      }
      const current = messagesRef.current.find((message) => message.id === id);
      const next = new Map(messagesRef.current.map((message) => [message.id, message]));
      const durableSnapshot = new Map(logicalRead.messages);
      const pending = new Map(pendingWritesRef.current);
      const projected = new Set(projectedSubmittingIDsRef.current);

      if (!durable) {
        next.delete(id);
        pending.delete(id);
        projected.delete(id);
      } else {
        pending.delete(id);
        if (sessionDeletionStatus === "unknown" || projectDeletionStatus === "unknown") {
          pending.set(id, null);
          next.set(id, cleanupFailedProjection(durable));
          if (durable.delivery_state === "submitting") projected.add(id);
          else projected.delete(id);
        } else if (
          durable.delivery_state === "submitting" &&
          !(
            current?.delivery_state === "submitting" &&
            !projected.has(id) &&
            sameQueuedChatMessage(current, durable)
          )
        ) {
          next.set(id, projectStoredSubmitting(durable));
          projected.add(id);
        } else if (current && !sameQueuedChatMessage(current, durable)) {
          next.set(id, conflictProjection(durable));
          projected.delete(id);
        } else {
          // The logical snapshot may carry non-durable conflict/source
          // metadata derived from a deletion tombstone. Preserve that local
          // projection while still normalizing persisted submitting fences.
          next.set(id, projectStoredSubmitting(durable));
          projected.delete(id);
        }
      }

      durableSnapshotRef.current = durableSnapshot;
      pendingWritesRef.current = pending;
      projectedSubmittingIDsRef.current = projected;
      commitMessages(
        staleCleanupFailuresRef.current.size > 0
          ? staleCleanupBlockedMessages(next.values(), staleCleanupFailuresRef.current)
          : next.values(),
      );
    };
    window.addEventListener("storage", onStorage);
    return () => window.removeEventListener("storage", onStorage);
  }, [
    blockForStaleCleanupFailures,
    blockForUnknownStorage,
    commitMessages,
    deleteWhere,
    observeExternalReset,
  ]);

  return {
    messages,
    setMessages,
    enqueueMessage,
    hasDurableSubmittingFence,
    deleteWhere,
    deleteSession,
    deleteProjectWhere,
    clear,
  };
}
