import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  parseQueuedChatMessageList,
  queuedChatMessagesStorageKey,
  type QueuedChatMessage,
} from "./_shared";
import {
  queuedChatDeletedItemStorageKeyPrefix,
  queuedChatDeletedProjectStorageKey,
  queuedChatDeletedSessionStorageKey,
  queuedChatDeletedSessionStorageKeyPrefix,
  queuedChatSessionDeletionFenceStatus,
  queuedChatMessageStorageKey,
  queuedChatMessageStorageKeyPrefix,
  queuedChatMessagesResetEpochStorageKey,
  queuedChatMessagesV2MarkerStorageKey,
  queuedChatMigratedLegacyStorageKeyPrefix,
  readQueuedChatMessagesFromStorage,
  useQueuedChatMessageStore,
} from "./queuedChatStorage";

function queued(id: string, createdAt = "2026-07-13T10:00:00Z"): QueuedChatMessage {
  return {
    id,
    session_id: "chat-a",
    content: `message ${id}`,
    delivery_storage_epoch: "0",
    execution_mode: "hecate_task",
    tools_enabled: false,
    provider_filter: "openai",
    model: "gpt-4o-mini",
    workspace: "",
    system_prompt: "",
    agent_id: "hecate",
    created_at: createdAt,
  };
}

function dispatchStorage(key: string, oldValue: string | null, newValue: string | null) {
  window.dispatchEvent(
    new StorageEvent("storage", {
      key,
      oldValue,
      newValue,
      storageArea: window.localStorage,
    }),
  );
}

function isQueuedItemStorageKey(key: string, id: string): boolean {
  const encodedID = encodeURIComponent(id);
  return (
    key.startsWith(queuedChatMessageStorageKeyPrefix) &&
    (key === `${queuedChatMessageStorageKeyPrefix}${encodedID}` || key.endsWith(`:${encodedID}`))
  );
}

function queuedItemStorageKeys(id: string): string[] {
  const keys: string[] = [];
  for (let index = 0; index < window.localStorage.length; index += 1) {
    const key = window.localStorage.key(index);
    if (key && isQueuedItemStorageKey(key, id)) keys.push(key);
  }
  return keys.sort();
}

function storageKeysWithPrefix(prefix: string): string[] {
  const keys: string[] = [];
  for (let index = 0; index < window.localStorage.length; index += 1) {
    const key = window.localStorage.key(index);
    if (key?.startsWith(prefix)) keys.push(key);
  }
  return keys.sort();
}

function queuedRevisionStorageKey(id: string): string {
  const keys = queuedItemStorageKeys(id).filter((key) => {
    const raw = window.localStorage.getItem(key);
    if (!raw) return false;
    const parsed = JSON.parse(raw) as QueuedChatMessage;
    return Boolean(parsed.delivery_storage_revision);
  });
  expect(keys).toHaveLength(1);
  return keys[0];
}

function persistQueuedRevision(
  message: QueuedChatMessage,
  revision = `revision-${message.id}`,
): { message: QueuedChatMessage; key: string; raw: string } {
  const durable = {
    ...message,
    delivery_storage_epoch: message.delivery_storage_epoch ?? "0",
    delivery_storage_revision: revision,
  };
  const key = queuedChatMessageStorageKey(durable.id, durable.delivery_storage_epoch, revision);
  const raw = JSON.stringify(durable);
  window.localStorage.setItem(key, raw);
  return { message: durable, key, raw };
}

describe("queued chat per-item storage", () => {
  beforeEach(() => window.localStorage.clear());
  afterEach(() => {
    vi.restoreAllMocks();
    window.localStorage.clear();
  });

  it("migrates the legacy array once and never resurrects stale legacy data", () => {
    const legacy = queued("legacy");
    const legacyRecord = { ...legacy };
    delete legacyRecord.delivery_storage_epoch;
    window.localStorage.setItem(queuedChatMessagesStorageKey, JSON.stringify([legacyRecord]));

    const first = renderHook(() => useQueuedChatMessageStore([]));
    expect(first.result.current.messages).toEqual([expect.objectContaining(legacy)]);
    expect(window.localStorage.getItem(queuedChatMessagesV2MarkerStorageKey)).toBe("1");
    expect(window.localStorage.getItem(queuedChatMessagesStorageKey)).toBeNull();
    expect(readQueuedChatMessagesFromStorage(window.localStorage)).toEqual([
      expect.objectContaining(legacy),
    ]);
    first.unmount();

    for (const key of queuedItemStorageKeys(legacy.id)) window.localStorage.removeItem(key);
    window.localStorage.setItem(queuedChatMessagesStorageKey, JSON.stringify([legacy]));
    const second = renderHook(() => useQueuedChatMessageStore([]));
    expect(second.result.current.messages).toEqual([]);
    expect(window.localStorage.getItem(queuedChatMessagesStorageKey)).toBeNull();
  });

  it("migrates the previous unscoped per-item key into the current generation", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const message = queued("legacy-unscoped-key");
    const legacyKey = `${queuedChatMessagesStorageKey}.item.${encodeURIComponent(message.id)}`;
    const legacyRaw = JSON.stringify(message);
    window.localStorage.setItem(legacyKey, legacyRaw);

    const store = renderHook(() => useQueuedChatMessageStore([]));

    expect(store.result.current.messages).toEqual([expect.objectContaining(message)]);
    expect(window.localStorage.getItem(legacyKey)).toBe(legacyRaw);
    const revision = JSON.parse(
      window.localStorage.getItem(queuedRevisionStorageKey(message.id)) ?? "null",
    );
    expect(revision).toEqual(
      expect.objectContaining({ ...message, delivery_storage_revision: expect.any(String) }),
    );
  });

  it("never overwrites a scoped record during partial unscoped-key migration", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const legacy = queued("partial-key-migration");
    const scoped = { ...legacy, content: "scoped prompt wins" };
    const legacyKey = `${queuedChatMessagesStorageKey}.item.${encodeURIComponent(legacy.id)}`;
    const scopedKey = queuedChatMessageStorageKey(scoped.id);
    const legacyRaw = JSON.stringify(legacy);
    const scopedRaw = JSON.stringify(scoped);
    window.localStorage.setItem(legacyKey, legacyRaw);
    window.localStorage.setItem(scopedKey, scopedRaw);

    const store = renderHook(() => useQueuedChatMessageStore([]));

    expect(window.localStorage.getItem(scopedKey)).toBe(scopedRaw);
    expect(window.localStorage.getItem(legacyKey)).toBe(legacyRaw);
    expect(store.result.current.messages).toEqual([
      expect.objectContaining({
        id: scoped.id,
        content: scoped.content,
        delivery_state: "reconcile_required",
        delivery_storage_failed: true,
      }),
    ]);
  });

  it("quarantines an unscoped record that claims a nonzero generation", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const message = {
      ...queued("unscoped-nonzero-generation"),
      delivery_storage_epoch: "orphaned-epoch",
    };
    const legacyKey = `${queuedChatMessagesStorageKey}.item.${encodeURIComponent(message.id)}`;
    const raw = JSON.stringify(message);
    window.localStorage.setItem(legacyKey, raw);

    const store = renderHook(() => useQueuedChatMessageStore([]));

    expect(window.localStorage.getItem(legacyKey)).toBe(raw);
    expect(window.localStorage.getItem(queuedChatMessageStorageKey(message.id))).toBeNull();
    expect(store.result.current.messages).toEqual([
      expect.objectContaining({
        id: message.id,
        delivery_state: "reconcile_required",
        delivery_storage_failed: true,
      }),
    ]);
  });

  it("preserves a previous-release unscoped prompt after a nonzero reset", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    window.localStorage.setItem(queuedChatMessagesResetEpochStorageKey, "current-epoch");
    const message = queued("legacy-unscoped-after-reset");
    const legacyRecord = { ...message };
    delete legacyRecord.delivery_storage_epoch;
    const legacyKey = `${queuedChatMessagesStorageKey}.item.${encodeURIComponent(message.id)}`;
    const raw = JSON.stringify(legacyRecord);
    window.localStorage.setItem(legacyKey, raw);

    const store = renderHook(() => useQueuedChatMessageStore([]));

    expect(window.localStorage.getItem(legacyKey)).toBe(raw);
    expect(store.result.current.messages).toEqual([
      expect.objectContaining({
        id: message.id,
        delivery_state: "reconcile_required",
        delivery_storage_failed: true,
      }),
    ]);
    let admission = "admitted" as ReturnType<typeof store.result.current.enqueueMessage>;
    act(() => {
      admission = store.result.current.enqueueMessage({
        ...queued("blocked-by-legacy-upgrade"),
        delivery_storage_epoch: "current-epoch",
      });
    });
    expect(admission).toBe("storage_failed");
  });

  it("preserves an ambiguous unscoped submitting fence for manual reconciliation", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    window.localStorage.setItem(queuedChatMessagesResetEpochStorageKey, "current-epoch");
    const message = {
      ...queued("legacy-unscoped-submitting"),
      delivery_state: "submitting" as const,
      delivery_idempotency_keyed: true,
      delivery_baseline_message_ids: [],
    };
    const legacyRecord = { ...message };
    delete legacyRecord.delivery_storage_epoch;
    const legacyKey = `${queuedChatMessagesStorageKey}.item.${encodeURIComponent(message.id)}`;
    const raw = JSON.stringify(legacyRecord);
    window.localStorage.setItem(legacyKey, raw);

    const store = renderHook(() => useQueuedChatMessageStore([]));

    expect(window.localStorage.getItem(legacyKey)).toBe(raw);
    expect(store.result.current.messages).toEqual([
      expect.objectContaining({
        id: message.id,
        delivery_state: "reconcile_required",
        delivery_storage_failed: true,
        delivery_storage_fenced: true,
      }),
    ]);
  });

  it("cleans a generation-scoped stale prompt during reload", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    window.localStorage.setItem(queuedChatMessagesResetEpochStorageKey, "current-epoch");
    const stale = queued("reload-stale-generation");
    const staleKey = queuedChatMessageStorageKey(stale.id, "0");
    window.localStorage.setItem(staleKey, JSON.stringify(stale));

    const store = renderHook(() => useQueuedChatMessageStore([]));

    expect(window.localStorage.getItem(staleKey)).toBeNull();
    expect(store.result.current.messages).toEqual([]);
    let admission = "storage_failed" as ReturnType<typeof store.result.current.enqueueMessage>;
    act(() => {
      admission = store.result.current.enqueueMessage(queued("after-reload-cleanup"));
    });
    expect(admission).toBe("admitted");
  });

  it("does not delete a newer generation when reset advances during initialization", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    window.localStorage.setItem(queuedChatMessagesResetEpochStorageKey, "epoch-1");
    const stale = { ...queued("reload-reset-race-stale"), project_id: "" };
    const staleKey = queuedChatMessageStorageKey(stale.id, "0");
    window.localStorage.setItem(staleKey, JSON.stringify(stale));
    const fresh = {
      ...queued("reload-reset-race-fresh"),
      project_id: "",
      delivery_storage_epoch: "epoch-2",
    };
    const freshKey = queuedChatMessageStorageKey(fresh.id, "epoch-2");
    const freshRaw = JSON.stringify(fresh);
    const originalGetItem = window.localStorage.getItem.bind(window.localStorage);
    const originalSetItem = window.localStorage.setItem.bind(window.localStorage);
    let epochReads = 0;
    vi.spyOn(Storage.prototype, "getItem").mockImplementation((key) => {
      if (key === queuedChatMessagesResetEpochStorageKey) {
        epochReads += 1;
        if (epochReads === 2) {
          originalSetItem(queuedChatMessagesResetEpochStorageKey, "epoch-2");
          originalSetItem(freshKey, freshRaw);
        }
      }
      return originalGetItem(key);
    });

    const store = renderHook(() => useQueuedChatMessageStore([]));

    expect(epochReads).toBeGreaterThanOrEqual(2);
    expect(window.localStorage.getItem(freshKey)).toBe(freshRaw);
    expect(window.localStorage.getItem(staleKey)).not.toBeNull();
    let admission = "admitted" as ReturnType<typeof store.result.current.enqueueMessage>;
    act(() => {
      admission = store.result.current.enqueueMessage({
        ...queued("blocked-after-reload-reset-race"),
        project_id: "",
        delivery_storage_epoch: "epoch-2",
      });
    });
    expect(admission).toBe("storage_failed");
  });

  it("keeps failed reload cleanup visible until Remove clears the stale generation", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    window.localStorage.setItem(queuedChatMessagesResetEpochStorageKey, "current-epoch");
    const stale = queued("reload-stale-cleanup-failure");
    const staleKey = queuedChatMessageStorageKey(stale.id, "0");
    const raw = JSON.stringify(stale);
    window.localStorage.setItem(staleKey, raw);
    const originalRemoveItem = window.localStorage.removeItem.bind(window.localStorage);
    const removeSpy = vi.spyOn(Storage.prototype, "removeItem").mockImplementation((key) => {
      if (key === staleKey) return;
      return originalRemoveItem(key);
    });

    const store = renderHook(() => useQueuedChatMessageStore([]));
    expect(store.result.current.messages).toEqual([
      expect.objectContaining({
        id: stale.id,
        delivery_state: "reconcile_required",
        delivery_storage_failed: true,
      }),
    ]);

    act(() => store.result.current.setMessages([]));
    expect(window.localStorage.getItem(staleKey)).toBe(raw);
    expect(store.result.current.messages).toHaveLength(1);

    removeSpy.mockRestore();
    act(() => store.result.current.setMessages([]));
    expect(window.localStorage.getItem(staleKey)).toBeNull();
    expect(store.result.current.messages).toEqual([]);
  });

  it("retains deferred removals across multiple stale cleanup failures", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    window.localStorage.setItem(queuedChatMessagesResetEpochStorageKey, "current-epoch");
    const currentA = {
      ...queued("multi-cleanup-a"),
      project_id: "",
      delivery_storage_epoch: "current-epoch",
    };
    const currentB = {
      ...queued("multi-cleanup-b"),
      project_id: "",
      delivery_storage_epoch: "current-epoch",
    };
    const staleA = { ...currentA, content: "stale A", delivery_storage_epoch: "0" };
    const staleB = { ...currentB, content: "stale B", delivery_storage_epoch: "0" };
    const persistedCurrentA = persistQueuedRevision(currentA, "revision-current-a");
    const persistedCurrentB = persistQueuedRevision(currentB, "revision-current-b");
    const currentAKey = persistedCurrentA.key;
    const currentBKey = persistedCurrentB.key;
    const staleAKey = queuedChatMessageStorageKey(staleA.id, "0");
    const staleBKey = queuedChatMessageStorageKey(staleB.id, "0");
    window.localStorage.setItem(staleAKey, JSON.stringify(staleA));
    window.localStorage.setItem(staleBKey, JSON.stringify(staleB));
    const blockedKeys = new Set([staleAKey, staleBKey]);
    const originalRemoveItem = window.localStorage.removeItem.bind(window.localStorage);
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation((key) => {
      if (blockedKeys.has(key)) return;
      return originalRemoveItem(key);
    });

    const store = renderHook(() => useQueuedChatMessageStore([]));
    expect(store.result.current.messages).toHaveLength(2);

    blockedKeys.delete(staleAKey);
    act(() =>
      store.result.current.setMessages((current) =>
        current.filter((message) => message.id !== currentA.id),
      ),
    );
    expect(window.localStorage.getItem(staleAKey)).toBeNull();
    expect(window.localStorage.getItem(currentAKey)).not.toBeNull();
    expect(store.result.current.messages).toHaveLength(2);

    blockedKeys.delete(staleBKey);
    act(() =>
      store.result.current.setMessages((current) =>
        current.filter((message) => message.id !== currentB.id),
      ),
    );

    expect(window.localStorage.getItem(staleBKey)).toBeNull();
    expect(window.localStorage.getItem(currentAKey)).toBeNull();
    expect(window.localStorage.getItem(currentBKey)).toBeNull();
    expect(store.result.current.messages).toEqual([]);
  });

  it("retries orphan cleanup without retaining a removed stale row", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    window.localStorage.setItem(queuedChatMessagesResetEpochStorageKey, "current-epoch");
    const staleA = {
      ...queued("stale-orphan-a"),
      session_id: "orphan-a",
      project_id: "",
    };
    const staleB = {
      ...queued("stale-orphan-b"),
      session_id: "orphan-b",
      project_id: "",
    };
    const staleAKey = queuedChatMessageStorageKey(staleA.id, "0");
    const staleBKey = queuedChatMessageStorageKey(staleB.id, "0");
    window.localStorage.setItem(staleAKey, JSON.stringify(staleA));
    window.localStorage.setItem(staleBKey, JSON.stringify(staleB));
    const blockedKeys = new Set([staleAKey, staleBKey]);
    const originalRemoveItem = window.localStorage.removeItem.bind(window.localStorage);
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation((key) => {
      if (blockedKeys.has(key)) return;
      return originalRemoveItem(key);
    });
    const store = renderHook(() => useQueuedChatMessageStore([]));

    blockedKeys.delete(staleAKey);
    let cleaned = true;
    act(() => {
      cleaned = store.result.current.deleteWhere((message) => message.session_id === "orphan-a");
    });

    expect(cleaned).toBe(false);
    expect(window.localStorage.getItem(staleAKey)).toBeNull();
    expect(store.result.current.messages.map((message) => message.id)).toEqual([staleB.id]);

    blockedKeys.delete(staleBKey);
    act(() => store.result.current.setMessages([]));
    expect(window.localStorage.getItem(staleBKey)).toBeNull();
    expect(store.result.current.messages).toEqual([]);
  });

  it("does not apply a stale owner predicate to a fresh same-id record", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    window.localStorage.setItem(queuedChatMessagesResetEpochStorageKey, "current-epoch");
    const fresh = {
      ...queued("same-id-different-owner"),
      project_id: "project-fresh",
      delivery_storage_epoch: "current-epoch",
    };
    const stale = {
      ...fresh,
      content: "stale project prompt",
      project_id: "project-stale",
      delivery_storage_epoch: "0",
    };
    const freshKey = queuedChatMessageStorageKey(fresh.id, "current-epoch");
    const staleKey = queuedChatMessageStorageKey(stale.id, "0");
    const freshRaw = JSON.stringify(fresh);
    window.localStorage.setItem(freshKey, freshRaw);
    window.localStorage.setItem(staleKey, JSON.stringify(stale));
    const originalRemoveItem = window.localStorage.removeItem.bind(window.localStorage);
    let blockStaleCleanup = true;
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation((key) => {
      if (blockStaleCleanup && key === staleKey) return;
      return originalRemoveItem(key);
    });
    const store = renderHook(() => useQueuedChatMessageStore([]));

    blockStaleCleanup = false;
    let cleaned = false;
    act(() => {
      cleaned = store.result.current.deleteWhere(
        (message) => message.project_id === "project-stale",
      );
    });

    expect(cleaned).toBe(true);
    expect(window.localStorage.getItem(staleKey)).toBeNull();
    expect(window.localStorage.getItem(freshKey)).toBe(freshRaw);
    expect(store.result.current.messages).toEqual([fresh]);
  });

  it("writes an empty-v2 fallback durably and keeps it usable when writes fail", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const fallback = queued("fallback");
    const durable = renderHook(() => useQueuedChatMessageStore([fallback]));
    expect(durable.result.current.messages).toEqual([expect.objectContaining(fallback)]);
    expect(readQueuedChatMessagesFromStorage(window.localStorage)).toEqual([
      expect.objectContaining(fallback),
    ]);
    durable.unmount();

    window.localStorage.clear();
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const originalSetItem = window.localStorage.setItem.bind(window.localStorage);
    vi.spyOn(Storage.prototype, "setItem").mockImplementation((key, value) => {
      if (key.startsWith(`${queuedChatMessagesStorageKey}.item.`)) {
        throw new DOMException("quota exceeded", "QuotaExceededError");
      }
      return originalSetItem(key, value);
    });
    const memoryOnly = renderHook(() => useQueuedChatMessageStore([fallback]));
    expect(memoryOnly.result.current.messages).toEqual([
      expect.objectContaining({
        id: fallback.id,
        delivery_state: "retryable",
        delivery_storage_failed: true,
      }),
    ]);
    expect(readQueuedChatMessagesFromStorage(window.localStorage)).toEqual([]);
    expect(
      memoryOnly.result.current.hasDurableSubmittingFence({
        ...fallback,
        delivery_state: "submitting",
        delivery_idempotency_keyed: true,
        delivery_baseline_message_ids: [],
      }),
    ).toBe(false);
    act(() => memoryOnly.result.current.setMessages([]));
    expect(memoryOnly.result.current.messages).toEqual([]);
    expect(readQueuedChatMessagesFromStorage(window.localStorage)).toEqual([]);
  });

  it("refuses a new queue admission when its exact item cannot be persisted", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const store = renderHook(() => useQueuedChatMessageStore([]));
    const message = queued("new-write-failure");
    const originalSetItem = window.localStorage.setItem.bind(window.localStorage);
    vi.spyOn(Storage.prototype, "setItem").mockImplementation((key, value) => {
      if (isQueuedItemStorageKey(key, message.id)) {
        throw new DOMException("quota exceeded", "QuotaExceededError");
      }
      return originalSetItem(key, value);
    });

    let admission = "admitted" as ReturnType<typeof store.result.current.enqueueMessage>;
    act(() => {
      admission = store.result.current.enqueueMessage(message);
    });

    expect(admission).toBe("storage_failed");
    expect(store.result.current.messages).toEqual([]);
    expect(queuedItemStorageKeys(message.id)).toEqual([]);
  });

  it("revokes stale ready content after a failed edit and preserves ambiguous fences", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const original = queued("failed-edit");
    const persistedOriginal = persistQueuedRevision(original);
    const store = renderHook(() => useQueuedChatMessageStore([]));
    const originalSetItem = window.localStorage.setItem.bind(window.localStorage);
    const writeSpy = vi.spyOn(Storage.prototype, "setItem").mockImplementation((key, value) => {
      if (isQueuedItemStorageKey(key, original.id) && key !== persistedOriginal.key) {
        throw new DOMException("quota exceeded", "QuotaExceededError");
      }
      return originalSetItem(key, value);
    });

    act(() =>
      store.result.current.setMessages((current) =>
        current.map((message) =>
          message.id === original.id ? { ...message, content: "edited but not durable" } : message,
        ),
      ),
    );
    expect(store.result.current.messages).toEqual([
      expect.objectContaining({
        id: original.id,
        content: "edited but not durable",
        delivery_state: "retryable",
        delivery_storage_failed: true,
      }),
    ]);
    expect(window.localStorage.getItem(persistedOriginal.key)).toBeNull();

    store.unmount();
    writeSpy.mockRestore();
    const reloaded = renderHook(() => useQueuedChatMessageStore([]));
    expect(reloaded.result.current.messages).toEqual([]);
    reloaded.unmount();

    const submitting = {
      ...original,
      delivery_state: "submitting" as const,
      delivery_idempotency_keyed: true,
      delivery_baseline_message_ids: ["before"],
    };
    const persistedSubmitting = persistQueuedRevision(submitting, "revision-submitting");
    const ambiguous = renderHook(() => useQueuedChatMessageStore([]));
    const submittingRaw = window.localStorage.getItem(persistedSubmitting.key);
    act(() => ambiguous.result.current.setMessages([submitting]));
    expect(ambiguous.result.current.hasDurableSubmittingFence(submitting)).toBe(true);
    vi.spyOn(Storage.prototype, "setItem").mockImplementation((key, value) => {
      if (isQueuedItemStorageKey(key, submitting.id) && key !== persistedSubmitting.key) {
        throw new DOMException("quota exceeded", "QuotaExceededError");
      }
      return originalSetItem(key, value);
    });
    act(() =>
      ambiguous.result.current.setMessages((current) =>
        current.map((message) => ({
          ...message,
          delivery_state: "reconcile_required" as const,
        })),
      ),
    );
    expect(ambiguous.result.current.messages).toEqual([
      expect.objectContaining({
        delivery_state: "reconcile_required",
        delivery_storage_failed: true,
      }),
    ]);
    expect(window.localStorage.getItem(persistedSubmitting.key)).toBe(submittingRaw);
  });

  it("tombstones a removed failed edit so a stale same-id writer stays blocked", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const original = queued("failed-edit-remove");
    const persistedOriginal = persistQueuedRevision(original);
    const store = renderHook(() => useQueuedChatMessageStore([]));
    const originalSetItem = window.localStorage.setItem.bind(window.localStorage);
    vi.spyOn(Storage.prototype, "setItem").mockImplementation((key, value) => {
      if (isQueuedItemStorageKey(key, original.id) && key !== persistedOriginal.key) {
        throw new DOMException("quota exceeded", "QuotaExceededError");
      }
      return originalSetItem(key, value);
    });

    act(() =>
      store.result.current.setMessages((current) =>
        current.map((message) =>
          message.id === original.id ? { ...message, content: "edited but not durable" } : message,
        ),
      ),
    );
    expect(window.localStorage.getItem(persistedOriginal.key)).toBeNull();
    expect(store.result.current.messages).toEqual([
      expect.objectContaining({
        id: original.id,
        content: "edited but not durable",
        delivery_storage_failed: true,
      }),
    ]);

    act(() => store.result.current.setMessages([]));
    expect(store.result.current.messages).toEqual([]);
    expect(storageKeysWithPrefix(queuedChatDeletedItemStorageKeyPrefix)).not.toEqual([]);

    vi.restoreAllMocks();
    const stale = {
      ...original,
      delivery_storage_revision: "stale-after-remove",
    };
    const staleKey = queuedChatMessageStorageKey(
      stale.id,
      stale.delivery_storage_epoch,
      stale.delivery_storage_revision,
    );
    const staleRaw = JSON.stringify(stale);
    window.localStorage.setItem(staleKey, staleRaw);
    act(() => dispatchStorage(staleKey, null, staleRaw));

    expect(store.result.current.messages).toEqual([
      expect.objectContaining({
        id: stale.id,
        content: stale.content,
        delivery_state: "reconcile_required",
        delivery_storage_conflict: "ready_replacement",
      }),
    ]);
    store.unmount();

    const reloaded = renderHook(() => useQueuedChatMessageStore([]));
    expect(reloaded.result.current.messages).toEqual([
      expect.objectContaining({
        id: stale.id,
        delivery_state: "reconcile_required",
        delivery_storage_conflict: "ready_replacement",
      }),
    ]);
  });

  it("unions stale different-id writers and preserves a newer unknown item on removal", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const a = renderHook(() => useQueuedChatMessageStore([]));
    const b = renderHook(() => useQueuedChatMessageStore([]));
    const first = queued("first", "2026-07-13T10:00:00Z");
    const second = queued("second", "2026-07-13T10:00:01Z");

    act(() => a.result.current.setMessages([first]));
    act(() => b.result.current.setMessages([second]));
    expect(readQueuedChatMessagesFromStorage(window.localStorage).map((item) => item.id)).toEqual([
      "first",
      "second",
    ]);

    act(() => a.result.current.setMessages([]));
    expect(readQueuedChatMessagesFromStorage(window.localStorage).map((item) => item.id)).toEqual([
      "second",
    ]);
    expect(a.result.current.messages.map((item) => item.id)).toEqual(["second"]);
  });

  it("projects another tab's live fence without rewriting it and propagates owner removal", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const pending = queued("shared");
    persistQueuedRevision(pending, "revision-shared-ready");
    const owner = renderHook(() => useQueuedChatMessageStore([]));
    const submitting = {
      ...pending,
      delivery_state: "submitting" as const,
      delivery_idempotency_keyed: true,
      delivery_baseline_message_ids: ["before"],
    };
    act(() => owner.result.current.setMessages([submitting]));
    const durableSubmittingKey = queuedRevisionStorageKey(pending.id);
    const durableSubmitting = window.localStorage.getItem(durableSubmittingKey);
    expect(owner.result.current.hasDurableSubmittingFence(submitting)).toBe(true);

    const observer = renderHook(() => useQueuedChatMessageStore([]));
    expect(observer.result.current.messages).toEqual([
      expect.objectContaining({
        id: pending.id,
        delivery_state: "reconcile_required",
        delivery_storage_fenced: true,
      }),
    ]);
    expect(window.localStorage.getItem(durableSubmittingKey)).toBe(durableSubmitting);
    expect(observer.result.current.hasDurableSubmittingFence(submitting)).toBe(false);

    const unrelated = queued("unrelated", "2026-07-13T10:00:01Z");
    act(() => observer.result.current.setMessages((current) => [...current, unrelated]));
    expect(window.localStorage.getItem(durableSubmittingKey)).toBe(durableSubmitting);
    act(() =>
      observer.result.current.setMessages((current) =>
        current.filter((message) => message.id !== pending.id),
      ),
    );
    expect(observer.result.current.messages.some((message) => message.id === pending.id)).toBe(
      true,
    );
    expect(window.localStorage.getItem(durableSubmittingKey)).toBe(durableSubmitting);

    act(() =>
      observer.result.current.setMessages((current) =>
        current.map((message) =>
          message.id === pending.id ? { ...message, delivery_state: "retryable" } : message,
        ),
      ),
    );
    expect(observer.result.current.messages).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          id: pending.id,
          delivery_state: "reconcile_required",
          delivery_storage_fenced: true,
        }),
      ]),
    );
    expect(window.localStorage.getItem(durableSubmittingKey)).toBe(durableSubmitting);

    act(() =>
      observer.result.current.setMessages((current) =>
        current.map((message) =>
          message.id === pending.id ? { ...message, delivery_state: "submitting" } : message,
        ),
      ),
    );
    expect(observer.result.current.hasDurableSubmittingFence(submitting)).toBe(true);
    expect(
      observer.result.current.messages.find((message) => message.id === pending.id),
    ).not.toHaveProperty("delivery_storage_fenced");

    act(() => owner.result.current.setMessages([]));
    expect(window.localStorage.getItem(durableSubmittingKey)).toBeNull();
    act(() => dispatchStorage(durableSubmittingKey, durableSubmitting, null));
    expect(observer.result.current.messages.map((message) => message.id)).toEqual(["unrelated"]);
    expect(window.localStorage.getItem(durableSubmittingKey)).toBeNull();
  });

  it("clears malformed per-item and legacy prompt storage while retaining the migration marker", () => {
    const malformedKey = queuedChatMessageStorageKey("malformed");
    const deletedProjectKey = queuedChatDeletedProjectStorageKey("project-before-reset");
    window.localStorage.setItem(malformedKey, "{prompt-bearing-corruption");
    window.localStorage.setItem(queuedChatMessagesStorageKey, JSON.stringify([queued("legacy")]));
    window.localStorage.setItem(deletedProjectKey, "deleted:before-reset");
    const store = renderHook(() => useQueuedChatMessageStore([]));

    // Reintroduce corrupt/legacy values after migration to prove reset scans
    // raw keys rather than only parsed queue records.
    window.localStorage.setItem(malformedKey, "{prompt-bearing-corruption");
    window.localStorage.setItem(queuedChatMessagesStorageKey, "prompt-bearing-legacy");
    act(() => {
      store.result.current.clear();
    });

    expect(window.localStorage.getItem(malformedKey)).toBeNull();
    expect(window.localStorage.getItem(queuedChatMessagesStorageKey)).toBeNull();
    expect(window.localStorage.getItem(deletedProjectKey)).toBeNull();
    expect(window.localStorage.getItem(queuedChatMessagesV2MarkerStorageKey)).toBe("1");
    expect(store.result.current.messages).toEqual([]);
  });

  it("post-audits a legacy whole-array prompt written after the reset epoch advances", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const store = renderHook(() => useQueuedChatMessageStore([]));
    const lateLegacy = JSON.stringify([queued("late-legacy-reset-write")]);
    const originalSetItem = window.localStorage.setItem.bind(window.localStorage);
    let injected = false;
    vi.spyOn(Storage.prototype, "setItem").mockImplementation((key, value) => {
      const result = originalSetItem(key, value);
      if (!injected && key === queuedChatMessagesResetEpochStorageKey) {
        injected = true;
        originalSetItem(queuedChatMessagesStorageKey, lateLegacy);
      }
      return result;
    });

    let cleared = false;
    act(() => {
      cleared = store.result.current.clear();
    });

    expect(injected).toBe(true);
    expect(cleared).toBe(true);
    expect(window.localStorage.getItem(queuedChatMessagesStorageKey)).toBeNull();
  });

  it("removes a legacy whole-array prompt written during the final durable scan", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const store = renderHook(() => useQueuedChatMessageStore([]));
    const originalSetItem = window.localStorage.setItem.bind(window.localStorage);
    const originalGetItem = window.localStorage.getItem.bind(window.localStorage);
    const lateLegacy = JSON.stringify([queued("durable-scan-legacy-write")]);
    const freshBase = queued("durable-scan-fresh");
    let fresh: QueuedChatMessage | undefined;
    let freshKey = "";
    let freshRaw = "";
    let freshReads = 0;
    let injected = false;

    vi.spyOn(Storage.prototype, "setItem").mockImplementation((key, value) => {
      const result = originalSetItem(key, value);
      if (!fresh && key === queuedChatMessagesResetEpochStorageKey) {
        fresh = {
          ...freshBase,
          delivery_storage_epoch: value,
          delivery_storage_revision: "fresh-during-reset",
        };
        freshKey = queuedChatMessageStorageKey(fresh.id, value, fresh.delivery_storage_revision);
        freshRaw = JSON.stringify(fresh);
        originalSetItem(freshKey, freshRaw);
      }
      return result;
    });
    vi.spyOn(Storage.prototype, "getItem").mockImplementation((key) => {
      const value = originalGetItem(key);
      if (freshKey && key === freshKey && value === freshRaw) {
        freshReads += 1;
        if (!injected && freshReads === 2) {
          injected = true;
          originalSetItem(queuedChatMessagesStorageKey, lateLegacy);
        }
      }
      return value;
    });

    let cleared = false;
    act(() => {
      cleared = store.result.current.clear();
    });

    expect(injected).toBe(true);
    expect(cleared).toBe(true);
    expect(window.localStorage.getItem(queuedChatMessagesStorageKey)).toBeNull();
    expect(window.localStorage.getItem(freshKey)).toBe(freshRaw);
    expect(store.result.current.messages).toEqual([fresh]);
  });

  it("reports reset failure when a late legacy whole-array prompt cannot be removed", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const store = renderHook(() => useQueuedChatMessageStore([]));
    const lateLegacy = JSON.stringify([queued("late-legacy-reset-failure")]);
    const originalSetItem = window.localStorage.setItem.bind(window.localStorage);
    const originalRemoveItem = window.localStorage.removeItem.bind(window.localStorage);
    let injected = false;
    vi.spyOn(Storage.prototype, "setItem").mockImplementation((key, value) => {
      const result = originalSetItem(key, value);
      if (!injected && key === queuedChatMessagesResetEpochStorageKey) {
        injected = true;
        originalSetItem(queuedChatMessagesStorageKey, lateLegacy);
      }
      return result;
    });
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation((key) => {
      if (key === queuedChatMessagesStorageKey) return;
      return originalRemoveItem(key);
    });

    let cleared = true;
    act(() => {
      cleared = store.result.current.clear();
    });

    expect(injected).toBe(true);
    expect(cleared).toBe(false);
    expect(window.localStorage.getItem(queuedChatMessagesStorageKey)).toBe(lateLegacy);
  });

  it("keeps failed immutable cleanup visible across reload and retries Remove", () => {
    const message = queued("cleanup-failure");
    const persisted = persistQueuedRevision(message);
    const store = renderHook(() => useQueuedChatMessageStore([]));
    const originalRemoveItem = window.localStorage.removeItem.bind(window.localStorage);
    const removeSpy = vi.spyOn(Storage.prototype, "removeItem").mockImplementation((key) => {
      if (key === persisted.key) return;
      originalRemoveItem(key);
    });

    let deleted = true;
    act(() => {
      deleted = store.result.current.deleteWhere(
        (queuedMessage) => queuedMessage.id === message.id,
      );
    });
    expect(deleted).toBe(false);
    expect(store.result.current.messages).toEqual([
      expect.objectContaining({
        id: message.id,
        delivery_state: "reconcile_required",
        delivery_storage_failed: true,
      }),
    ]);
    expect(window.localStorage.getItem(persisted.key)).toBe(persisted.raw);
    store.unmount();

    const reloaded = renderHook(() => useQueuedChatMessageStore([]));
    expect(reloaded.result.current.messages).toEqual([
      expect.objectContaining({
        id: message.id,
        delivery_state: "reconcile_required",
        delivery_storage_failed: true,
      }),
    ]);
    removeSpy.mockRestore();
    let retried = false;
    act(() => {
      retried = reloaded.result.current.deleteWhere(
        (queuedMessage) => queuedMessage.id === message.id,
      );
    });

    expect(retried).toBe(true);
    expect(window.localStorage.getItem(persisted.key)).toBeNull();
    expect(reloaded.result.current.messages).toEqual([]);
  });

  it("quarantines a tab after a reset epoch without deleting new-epoch queue items", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const owner = renderHook(() => useQueuedChatMessageStore([]));
    const staleTab = renderHook(() => useQueuedChatMessageStore([]));
    const oldEpoch = window.localStorage.getItem(queuedChatMessagesResetEpochStorageKey);

    let cleared = false;
    act(() => {
      cleared = owner.result.current.clear();
    });
    expect(cleared).toBe(true);
    const newEpoch = window.localStorage.getItem(queuedChatMessagesResetEpochStorageKey);
    expect(newEpoch).not.toBe(oldEpoch);
    act(() => dispatchStorage(queuedChatMessagesResetEpochStorageKey, oldEpoch, newEpoch));

    const fresh = queued("fresh-after-reset");
    let admission = "storage_failed" as ReturnType<typeof owner.result.current.enqueueMessage>;
    act(() => {
      admission = owner.result.current.enqueueMessage(fresh);
    });
    expect(admission).toBe("admitted");
    const freshKey = queuedRevisionStorageKey(fresh.id);
    const freshRaw = window.localStorage.getItem(freshKey);
    act(() => dispatchStorage(freshKey, null, freshRaw));
    let staleAdmission = "admitted" as ReturnType<typeof staleTab.result.current.enqueueMessage>;
    act(() => {
      staleAdmission = staleTab.result.current.enqueueMessage(queued("stale-enqueue"));
    });
    act(() => staleTab.result.current.setMessages([queued("stale-after-reset")]));

    expect(staleAdmission).toBe("reset_observed");
    expect(staleTab.result.current.messages).toEqual([]);
    expect(window.localStorage.getItem(freshKey)).toBe(freshRaw);
    expect(readQueuedChatMessagesFromStorage(window.localStorage).map((item) => item.id)).toEqual([
      fresh.id,
    ]);
  });

  it("does not erase a prompt admitted after the reset epoch advances", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const old = queued("old-before-reset");
    window.localStorage.setItem(queuedChatMessageStorageKey(old.id), JSON.stringify(old));
    const projectMarkerKey = queuedChatDeletedProjectStorageKey("deleted-during-reset");
    window.localStorage.setItem(projectMarkerKey, "deleted:old-generation");
    const store = renderHook(() => useQueuedChatMessageStore([]));
    const fresh = queued("fresh-in-new-epoch");
    let freshInEpoch: QueuedChatMessage | undefined;
    let freshRaw = "";
    let freshKey = "";
    let freshProjectMarker = "";
    let freshProjectMarkerKey = "";
    const originalRemoveItem = window.localStorage.removeItem.bind(window.localStorage);
    let injected = false;
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation((key) => {
      originalRemoveItem(key);
      if (!injected && key === queuedChatMessageStorageKey(old.id)) {
        injected = true;
        freshInEpoch = {
          ...fresh,
          delivery_storage_epoch:
            window.localStorage.getItem(queuedChatMessagesResetEpochStorageKey) ?? "0",
        };
        freshRaw = JSON.stringify(freshInEpoch);
        freshKey = queuedChatMessageStorageKey(
          fresh.id,
          freshInEpoch.delivery_storage_epoch ?? "0",
        );
        freshProjectMarker = `deleted:v2:${encodeURIComponent(
          freshInEpoch.delivery_storage_epoch ?? "0",
        )}:new-generation`;
        freshProjectMarkerKey = queuedChatDeletedProjectStorageKey(
          "deleted-during-reset",
          freshInEpoch.delivery_storage_epoch ?? "0",
        );
        window.localStorage.setItem(freshKey, freshRaw);
        window.localStorage.setItem(freshProjectMarkerKey, freshProjectMarker);
      }
    });

    let cleared = false;
    act(() => {
      cleared = store.result.current.clear();
    });

    expect(cleared).toBe(true);
    expect(injected).toBe(true);
    expect(window.localStorage.getItem(queuedChatMessageStorageKey(old.id))).toBeNull();
    expect(window.localStorage.getItem(freshKey)).toBe(freshRaw);
    expect(window.localStorage.getItem(projectMarkerKey)).toBeNull();
    expect(window.localStorage.getItem(freshProjectMarkerKey)).toBe(freshProjectMarker);
    expect(store.result.current.messages).toEqual([freshInEpoch]);
  });

  it("stops reset cleanup when another tab advances the generation again", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const old = { ...queued("second-reset-old"), project_id: "" };
    window.localStorage.setItem(queuedChatMessageStorageKey(old.id), JSON.stringify(old));
    const store = renderHook(() => useQueuedChatMessageStore([]));
    const fresh = {
      ...queued("second-reset-fresh"),
      project_id: "",
      delivery_storage_epoch: "epoch-2",
    };
    const freshKey = queuedChatMessageStorageKey(fresh.id, "epoch-2");
    const freshRaw = JSON.stringify(fresh);
    const originalSetItem = window.localStorage.setItem.bind(window.localStorage);
    let injected = false;
    vi.spyOn(Storage.prototype, "setItem").mockImplementation((key, value) => {
      const result = originalSetItem(key, value);
      if (!injected && key === queuedChatMessagesV2MarkerStorageKey) {
        injected = true;
        originalSetItem(queuedChatMessagesResetEpochStorageKey, "epoch-2");
        originalSetItem(freshKey, freshRaw);
      }
      return result;
    });

    let cleared = true;
    act(() => {
      cleared = store.result.current.clear();
    });

    expect(injected).toBe(true);
    expect(cleared).toBe(false);
    expect(window.localStorage.getItem(freshKey)).toBe(freshRaw);
    expect(store.result.current.messages).toEqual([]);
  });

  it("isolates final cleanup from same-id and same-project replacements in the new generation", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const old = { ...queued("generation-key-race"), project_id: "" };
    const oldKey = queuedChatMessageStorageKey(old.id);
    const markerProjectID = "generation-key-project";
    const oldMarkerKey = queuedChatDeletedProjectStorageKey(markerProjectID);
    window.localStorage.setItem(oldKey, JSON.stringify(old));
    window.localStorage.setItem(oldMarkerKey, "deleted:v2:0:old-generation");
    const store = renderHook(() => useQueuedChatMessageStore([]));
    const originalSetItem = window.localStorage.setItem.bind(window.localStorage);
    const originalRemoveItem = window.localStorage.removeItem.bind(window.localStorage);
    let fresh: QueuedChatMessage | undefined;
    let freshRaw = "";
    let freshKey = "";
    let freshMarkerKey = "";
    let freshMarker = "";

    vi.spyOn(Storage.prototype, "removeItem").mockImplementation((key) => {
      const epoch = window.localStorage.getItem(queuedChatMessagesResetEpochStorageKey) ?? "0";
      if (key === oldKey && !fresh) {
        fresh = {
          ...old,
          content: "fresh same-id replacement",
          delivery_storage_epoch: epoch,
        };
        freshRaw = JSON.stringify(fresh);
        freshKey = queuedChatMessageStorageKey(old.id, epoch);
        originalSetItem(freshKey, freshRaw);
      }
      if (key === oldMarkerKey && !freshMarker) {
        freshMarkerKey = queuedChatDeletedProjectStorageKey(markerProjectID, epoch);
        freshMarker = `deleted:v2:${encodeURIComponent(epoch)}:fresh-generation`;
        originalSetItem(freshMarkerKey, freshMarker);
      }
      return originalRemoveItem(key);
    });

    let cleared = false;
    act(() => {
      cleared = store.result.current.clear();
    });

    expect(cleared).toBe(true);
    expect(window.localStorage.getItem(oldKey)).toBeNull();
    expect(window.localStorage.getItem(freshKey)).toBe(freshRaw);
    expect(window.localStorage.getItem(oldMarkerKey)).toBeNull();
    expect(window.localStorage.getItem(freshMarkerKey)).toBe(freshMarker);
    expect(store.result.current.messages).toEqual([fresh]);
  });

  it("blocks a stale write that lands after reset when cleanup cannot remove it", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const owner = renderHook(() => useQueuedChatMessageStore([]));
    const staleTab = renderHook(() => useQueuedChatMessageStore([]));
    const stale = queued("stale-write-after-reset");
    let staleKey = "";
    const originalSetItem = window.localStorage.setItem.bind(window.localStorage);
    const originalRemoveItem = window.localStorage.removeItem.bind(window.localStorage);
    let resetInjected = false;
    let ownerCleared = false;

    vi.spyOn(Storage.prototype, "setItem").mockImplementation((key, value) => {
      if (!resetInjected && isQueuedItemStorageKey(key, stale.id)) {
        staleKey = key;
        resetInjected = true;
        ownerCleared = owner.result.current.clear();
      }
      return originalSetItem(key, value);
    });
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation((key) => {
      if (key === staleKey) return;
      return originalRemoveItem(key);
    });

    let admission = "admitted" as ReturnType<typeof staleTab.result.current.enqueueMessage>;
    act(() => {
      admission = staleTab.result.current.enqueueMessage(stale);
    });

    expect(resetInjected).toBe(true);
    expect(ownerCleared).toBe(true);
    expect(admission).toBe("reset_observed");
    expect(staleKey).not.toBe("");
    const staleRaw = window.localStorage.getItem(staleKey);
    expect(staleRaw).not.toBeNull();
    expect(JSON.parse(staleRaw ?? "{}").delivery_storage_epoch).toBe("0");

    act(() => dispatchStorage(staleKey, null, staleRaw));

    expect(owner.result.current.messages).toEqual([
      expect.objectContaining({
        id: stale.id,
        delivery_state: "reconcile_required",
        delivery_storage_failed: true,
        delivery_storage_epoch: "0",
      }),
    ]);
    expect(
      owner.result.current.hasDurableSubmittingFence({
        ...stale,
        delivery_state: "submitting",
        delivery_idempotency_keyed: true,
        delivery_baseline_message_ids: [],
      }),
    ).toBe(false);
  });

  it("retries stale-event cleanup before honoring removal of a fresh same-id prompt", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    window.localStorage.setItem(queuedChatMessagesResetEpochStorageKey, "current-epoch");
    const fresh = {
      ...queued("stale-event-same-id"),
      content: "fresh current-generation prompt",
      delivery_storage_epoch: "current-epoch",
    };
    const persistedFresh = persistQueuedRevision(fresh, "revision-fresh-current");
    const freshKey = persistedFresh.key;
    const freshRaw = persistedFresh.raw;
    const store = renderHook(() => useQueuedChatMessageStore([]));
    const stale = { ...fresh, content: "stale old-generation prompt", delivery_storage_epoch: "0" };
    const staleKey = queuedChatMessageStorageKey(stale.id, "0");
    const staleRaw = JSON.stringify(stale);
    window.localStorage.setItem(staleKey, staleRaw);
    const originalRemoveItem = window.localStorage.removeItem.bind(window.localStorage);
    const removeSpy = vi.spyOn(Storage.prototype, "removeItem").mockImplementation((key) => {
      if (key === staleKey) return;
      return originalRemoveItem(key);
    });

    act(() => dispatchStorage(staleKey, null, staleRaw));

    expect(store.result.current.messages).toEqual([
      expect.objectContaining({
        id: fresh.id,
        content: fresh.content,
        delivery_state: "reconcile_required",
        delivery_storage_failed: true,
      }),
    ]);
    expect(window.localStorage.getItem(freshKey)).toBe(freshRaw);
    act(() => dispatchStorage(freshKey, freshRaw, freshRaw));
    expect(store.result.current.messages[0]).toEqual(
      expect.objectContaining({
        id: fresh.id,
        content: fresh.content,
        delivery_storage_failed: true,
      }),
    );

    act(() => store.result.current.setMessages([]));
    expect(window.localStorage.getItem(staleKey)).toBe(staleRaw);
    expect(window.localStorage.getItem(freshKey)).toBe(freshRaw);
    expect(store.result.current.messages).toHaveLength(1);

    removeSpy.mockRestore();
    act(() => store.result.current.setMessages([]));
    expect(window.localStorage.getItem(staleKey)).toBeNull();
    expect(window.localStorage.getItem(freshKey)).toBeNull();
    expect(store.result.current.messages).toEqual([]);
  });

  it("restores unrelated ready prompts after another tab clears a stale failure", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    window.localStorage.setItem(queuedChatMessagesResetEpochStorageKey, "current-epoch");
    const ready = {
      ...queued("ready-after-external-cleanup"),
      project_id: "",
      delivery_storage_epoch: "current-epoch",
    };
    const readyKey = queuedChatMessageStorageKey(ready.id, "current-epoch");
    window.localStorage.setItem(readyKey, JSON.stringify(ready));
    const stale = { ...queued("externally-cleaned-stale"), project_id: "" };
    const staleKey = queuedChatMessageStorageKey(stale.id, "0");
    const staleRaw = JSON.stringify(stale);
    window.localStorage.setItem(staleKey, staleRaw);
    const originalRemoveItem = window.localStorage.removeItem.bind(window.localStorage);
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation((key) => {
      if (key === staleKey) return;
      return originalRemoveItem(key);
    });
    const store = renderHook(() => useQueuedChatMessageStore([]));
    expect(store.result.current.messages).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          id: ready.id,
          delivery_state: "reconcile_required",
          delivery_storage_failed: true,
        }),
      ]),
    );

    originalRemoveItem(staleKey);
    act(() => dispatchStorage(staleKey, staleRaw, null));

    expect(store.result.current.messages).toEqual([ready]);
    expect(window.localStorage.getItem(readyKey)).toBe(JSON.stringify(ready));
  });

  it("purges project tombstones while an unrelated stale failure remains", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    window.localStorage.setItem(queuedChatMessagesResetEpochStorageKey, "current-epoch");
    const projectPrompt = {
      ...queued("tombstoned-while-blocked"),
      project_id: "project-deleted",
      delivery_storage_epoch: "current-epoch",
    };
    const unknownOwner = {
      ...queued("unknown-owner-while-blocked"),
      delivery_storage_epoch: "current-epoch",
    };
    delete unknownOwner.project_id;
    const persistedProjectPrompt = persistQueuedRevision(projectPrompt, "revision-project-prompt");
    const projectKey = persistedProjectPrompt.key;
    const unknownKey = queuedChatMessageStorageKey(unknownOwner.id, "current-epoch");
    const unknownRaw = JSON.stringify(unknownOwner);
    window.localStorage.setItem(unknownKey, unknownRaw);
    const stale = { ...queued("unrelated-stale-project-cleanup"), project_id: "" };
    const staleKey = queuedChatMessageStorageKey(stale.id, "0");
    const staleRaw = JSON.stringify(stale);
    window.localStorage.setItem(staleKey, staleRaw);
    const originalRemoveItem = window.localStorage.removeItem.bind(window.localStorage);
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation((key) => {
      if (key === staleKey) return;
      return originalRemoveItem(key);
    });
    const store = renderHook(() => useQueuedChatMessageStore([]));
    const markerKey = queuedChatDeletedProjectStorageKey("project-deleted", "current-epoch");
    const marker = `deleted:v2:${encodeURIComponent("current-epoch")}:external`;
    window.localStorage.setItem(markerKey, marker);
    act(() => dispatchStorage(markerKey, null, marker));
    expect(window.localStorage.getItem(projectKey)).toBeNull();

    originalRemoveItem(staleKey);
    act(() => dispatchStorage(staleKey, staleRaw, null));

    expect(window.localStorage.getItem(projectKey)).toBeNull();
    expect(window.localStorage.getItem(unknownKey)).toBe(unknownRaw);
    expect(store.result.current.messages).toEqual([
      expect.objectContaining({
        id: unknownOwner.id,
        delivery_state: "reconcile_required",
        delivery_storage_failed: true,
      }),
    ]);
  });

  it.each(["edited", "submitting"] as const)(
    "preserves a %s same-id replacement when deferred removal resolves externally",
    (replacementKind) => {
      window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
      window.localStorage.setItem(queuedChatMessagesResetEpochStorageKey, "current-epoch");
      const ready = {
        ...queued(`deferred-${replacementKind}-replacement`),
        project_id: "",
        delivery_storage_epoch: "current-epoch",
      };
      const readyKey = queuedChatMessageStorageKey(ready.id, "current-epoch");
      const readyRaw = JSON.stringify(ready);
      window.localStorage.setItem(readyKey, readyRaw);
      const stale = { ...queued(`deferred-${replacementKind}-stale`), project_id: "" };
      const staleKey = queuedChatMessageStorageKey(stale.id, "0");
      const staleRaw = JSON.stringify(stale);
      window.localStorage.setItem(staleKey, staleRaw);
      const originalRemoveItem = window.localStorage.removeItem.bind(window.localStorage);
      vi.spyOn(Storage.prototype, "removeItem").mockImplementation((key) => {
        if (key === staleKey) return;
        return originalRemoveItem(key);
      });
      const store = renderHook(() => useQueuedChatMessageStore([]));

      act(() =>
        store.result.current.setMessages((current) =>
          current.filter((message) => message.id !== ready.id),
        ),
      );
      const replacement =
        replacementKind === "submitting"
          ? {
              ...ready,
              delivery_state: "submitting" as const,
              delivery_idempotency_keyed: true,
              delivery_baseline_message_ids: [],
            }
          : { ...ready, content: "edited in another tab" };
      const replacementRaw = JSON.stringify(replacement);
      window.localStorage.setItem(readyKey, replacementRaw);
      act(() => dispatchStorage(readyKey, readyRaw, replacementRaw));

      originalRemoveItem(staleKey);
      act(() => dispatchStorage(staleKey, staleRaw, null));

      expect(window.localStorage.getItem(readyKey)).toBe(replacementRaw);
      expect(store.result.current.messages).toEqual([
        expect.objectContaining({
          id: ready.id,
          content: replacement.content,
          delivery_state: "reconcile_required",
          ...(replacementKind === "submitting" ? { delivery_storage_fenced: true } : {}),
        }),
      ]);
    },
  );

  it("preserves a cross-tab submitting fence when predicate cleanup resolves stale storage", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    window.localStorage.setItem(queuedChatMessagesResetEpochStorageKey, "current-epoch");
    const ready = {
      ...queued("deferred-submitting-predicate"),
      project_id: "",
      delivery_storage_epoch: "current-epoch",
    };
    const readyKey = queuedChatMessageStorageKey(ready.id, "current-epoch");
    const readyRaw = JSON.stringify(ready);
    window.localStorage.setItem(readyKey, readyRaw);
    const stale = {
      ...queued("predicate-resolved-stale"),
      session_id: "orphan-session",
      project_id: "",
    };
    const staleKey = queuedChatMessageStorageKey(stale.id, "0");
    window.localStorage.setItem(staleKey, JSON.stringify(stale));
    const originalRemoveItem = window.localStorage.removeItem.bind(window.localStorage);
    let blockStaleCleanup = true;
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation((key) => {
      if (blockStaleCleanup && key === staleKey) return;
      return originalRemoveItem(key);
    });
    const store = renderHook(() => useQueuedChatMessageStore([]));
    act(() =>
      store.result.current.setMessages((current) =>
        current.filter((message) => message.id !== ready.id),
      ),
    );
    const submitting = {
      ...ready,
      delivery_state: "submitting" as const,
      delivery_idempotency_keyed: true,
      delivery_baseline_message_ids: [],
    };
    const submittingRaw = JSON.stringify(submitting);
    window.localStorage.setItem(readyKey, submittingRaw);
    act(() => dispatchStorage(readyKey, readyRaw, submittingRaw));

    blockStaleCleanup = false;
    let cleaned = false;
    act(() => {
      cleaned = store.result.current.deleteWhere(
        (message) => message.session_id === "orphan-session",
      );
    });

    expect(cleaned).toBe(true);
    expect(window.localStorage.getItem(staleKey)).toBeNull();
    expect(window.localStorage.getItem(readyKey)).toBe(submittingRaw);
    expect(store.result.current.messages).toEqual([
      expect.objectContaining({
        id: ready.id,
        delivery_state: "reconcile_required",
        delivery_storage_fenced: true,
      }),
    ]);
  });

  it("quarantines an unreadable stale-generation storage event", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    window.localStorage.setItem(queuedChatMessagesResetEpochStorageKey, "current-epoch");
    const store = renderHook(() => useQueuedChatMessageStore([]));
    const stale = queued("unreadable-stale-event");
    const staleKey = queuedChatMessageStorageKey(stale.id, "0");
    const staleRaw = JSON.stringify(stale);
    window.localStorage.setItem(staleKey, staleRaw);
    const originalGetItem = window.localStorage.getItem.bind(window.localStorage);
    const readSpy = vi.spyOn(Storage.prototype, "getItem").mockImplementation((key) => {
      if (key === staleKey) throw new DOMException("read denied", "SecurityError");
      return originalGetItem(key);
    });

    act(() => dispatchStorage(staleKey, null, staleRaw));

    expect(store.result.current.messages).toEqual([
      expect.objectContaining({
        id: stale.id,
        delivery_state: "reconcile_required",
        delivery_storage_failed: true,
      }),
    ]);
    let admission = "admitted" as ReturnType<typeof store.result.current.enqueueMessage>;
    act(() => {
      admission = store.result.current.enqueueMessage({
        ...queued("blocked-by-unreadable-stale-event"),
        delivery_storage_epoch: "current-epoch",
      });
    });
    expect(admission).toBe("storage_failed");
    readSpy.mockRestore();
    expect(window.localStorage.getItem(staleKey)).toBe(staleRaw);
  });

  it("reports reset failure when a late stale item cannot be removed", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const store = renderHook(() => useQueuedChatMessageStore([]));
    const late = queued("late-stale-reset-write");
    const lateKey = queuedChatMessageStorageKey(late.id);
    const lateRaw = JSON.stringify(late);
    const originalSetItem = window.localStorage.setItem.bind(window.localStorage);
    const originalRemoveItem = window.localStorage.removeItem.bind(window.localStorage);
    let injected = false;

    vi.spyOn(Storage.prototype, "setItem").mockImplementation((key, value) => {
      const result = originalSetItem(key, value);
      if (!injected && key === queuedChatMessagesResetEpochStorageKey) {
        injected = true;
        originalSetItem(lateKey, lateRaw);
      }
      return result;
    });
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation((key) => {
      if (key === lateKey) return;
      return originalRemoveItem(key);
    });

    let cleared = true;
    act(() => {
      cleared = store.result.current.clear();
    });

    expect(injected).toBe(true);
    expect(cleared).toBe(false);
    expect(window.localStorage.getItem(lateKey)).toBe(lateRaw);
    expect(store.result.current.messages).toEqual([
      expect.objectContaining({
        id: late.id,
        delivery_state: "reconcile_required",
        delivery_storage_failed: true,
      }),
    ]);
  });

  it("preserves a current-generation same-id replacement during stale enqueue cleanup", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const store = renderHook(() => useQueuedChatMessageStore([]));
    const stale = queued("same-id-enqueue-race");
    const fresh = {
      ...stale,
      content: "fresh replacement",
      delivery_storage_epoch: "replacement-epoch",
    };
    const freshRaw = JSON.stringify(fresh);
    const originalGetItem = window.localStorage.getItem.bind(window.localStorage);
    const originalSetItem = window.localStorage.setItem.bind(window.localStorage);
    let epochReads = 0;

    vi.spyOn(Storage.prototype, "getItem").mockImplementation((key) => {
      if (key === queuedChatMessagesResetEpochStorageKey) {
        epochReads += 1;
        if (epochReads === 2) {
          originalSetItem(queuedChatMessagesResetEpochStorageKey, "replacement-epoch");
          originalSetItem(queuedChatMessageStorageKey(stale.id, "replacement-epoch"), freshRaw);
        }
      }
      return originalGetItem(key);
    });

    let admission = "admitted" as ReturnType<typeof store.result.current.enqueueMessage>;
    act(() => {
      admission = store.result.current.enqueueMessage(stale);
    });

    expect(admission).toBe("reset_observed");
    expect(
      window.localStorage.getItem(queuedChatMessageStorageKey(stale.id, "replacement-epoch")),
    ).toBe(freshRaw);
  });

  it("preserves a current-generation same-id replacement during stale setter cleanup", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const original = queued("same-id-setter-race");
    window.localStorage.setItem(queuedChatMessageStorageKey(original.id), JSON.stringify(original));
    const store = renderHook(() => useQueuedChatMessageStore([]));
    const fresh = {
      ...original,
      content: "fresh replacement",
      delivery_storage_epoch: "replacement-epoch",
    };
    const freshRaw = JSON.stringify(fresh);
    const originalGetItem = window.localStorage.getItem.bind(window.localStorage);
    const originalSetItem = window.localStorage.setItem.bind(window.localStorage);
    let epochReads = 0;

    vi.spyOn(Storage.prototype, "getItem").mockImplementation((key) => {
      if (key === queuedChatMessagesResetEpochStorageKey) {
        epochReads += 1;
        if (epochReads === 2) {
          originalSetItem(queuedChatMessagesResetEpochStorageKey, "replacement-epoch");
          originalSetItem(queuedChatMessageStorageKey(original.id, "replacement-epoch"), freshRaw);
        }
      }
      return originalGetItem(key);
    });

    act(() =>
      store.result.current.setMessages((current) =>
        current.map((message) => ({ ...message, content: "stale edit" })),
      ),
    );

    expect(
      window.localStorage.getItem(queuedChatMessageStorageKey(original.id, "replacement-epoch")),
    ).toBe(freshRaw);
    expect(store.result.current.messages).toEqual([]);
  });

  it("preserves and blocks records when the reset epoch cannot be read", () => {
    const message = { ...queued("unreadable-epoch"), delivery_storage_epoch: "epoch-1" };
    const key = queuedChatMessageStorageKey(message.id, "epoch-1");
    const raw = JSON.stringify(message);
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    window.localStorage.setItem(queuedChatMessagesResetEpochStorageKey, "epoch-1");
    window.localStorage.setItem(key, raw);
    const originalGetItem = window.localStorage.getItem.bind(window.localStorage);
    const readSpy = vi.spyOn(Storage.prototype, "getItem").mockImplementation((storageKey) => {
      if (storageKey === queuedChatMessagesResetEpochStorageKey) {
        throw new DOMException("read denied", "SecurityError");
      }
      return originalGetItem(storageKey);
    });

    const store = renderHook(() => useQueuedChatMessageStore([]));

    expect(store.result.current.messages).toEqual([
      expect.objectContaining({
        id: message.id,
        delivery_state: "reconcile_required",
        delivery_storage_failed: true,
      }),
    ]);
    readSpy.mockRestore();
    expect(window.localStorage.getItem(key)).toBe(raw);
  });

  it("treats a missing epoch with nonzero item generations as inconsistent", () => {
    const message = { ...queued("orphaned-generation"), delivery_storage_epoch: "epoch-1" };
    const key = queuedChatMessageStorageKey(message.id, "epoch-1");
    const raw = JSON.stringify(message);
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    window.localStorage.setItem(key, raw);

    const store = renderHook(() => useQueuedChatMessageStore([]));

    expect(store.result.current.messages).toEqual([
      expect.objectContaining({
        id: message.id,
        delivery_state: "reconcile_required",
        delivery_storage_failed: true,
      }),
    ]);
    expect(window.localStorage.getItem(key)).toBe(raw);
  });

  it.each(["", " padded "])(
    "rejects the noncanonical reset epoch %j and blocks new queue admission",
    (malformedEpoch) => {
      window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
      window.localStorage.setItem(queuedChatMessagesResetEpochStorageKey, malformedEpoch);
      const message = {
        ...queued("malformed-reset-epoch"),
        delivery_storage_epoch: malformedEpoch,
        delivery_storage_revision: "malformed-epoch-revision",
      };
      const key = queuedChatMessageStorageKey(
        message.id,
        malformedEpoch,
        message.delivery_storage_revision,
      );
      const raw = JSON.stringify(message);
      window.localStorage.setItem(key, raw);

      const store = renderHook(() => useQueuedChatMessageStore([]));
      expect(store.result.current.messages).toEqual([]);
      expect(readQueuedChatMessagesFromStorage(window.localStorage)).toEqual([]);

      let admission = "admitted" as ReturnType<typeof store.result.current.enqueueMessage>;
      act(() => {
        admission = store.result.current.enqueueMessage(queued("blocked-by-malformed-epoch"));
      });
      expect(admission).toBe("storage_failed");
      expect(window.localStorage.getItem(key)).toBe(raw);
    },
  );

  it("retains a blocked item when a storage event record read fails", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const message = queued("event-read-failure");
    const key = queuedChatMessageStorageKey(message.id);
    const raw = JSON.stringify(message);
    window.localStorage.setItem(key, raw);
    const store = renderHook(() => useQueuedChatMessageStore([]));
    const originalGetItem = window.localStorage.getItem.bind(window.localStorage);
    const readSpy = vi.spyOn(Storage.prototype, "getItem").mockImplementation((storageKey) => {
      if (storageKey === key) throw new DOMException("read denied", "SecurityError");
      return originalGetItem(storageKey);
    });

    act(() => dispatchStorage(key, raw, raw));

    expect(store.result.current.messages).toEqual([
      expect.objectContaining({
        id: message.id,
        delivery_state: "reconcile_required",
        delivery_storage_failed: true,
      }),
    ]);
    readSpy.mockRestore();
    expect(window.localStorage.getItem(key)).toBe(raw);
  });

  it("quarantines legacy unknown-owner prompts after a project tombstone event and reload", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const legacy = queued("unknown-owner-after-project-delete");
    const key = queuedChatMessageStorageKey(legacy.id);
    const raw = JSON.stringify(legacy);
    window.localStorage.setItem(key, raw);
    const observer = renderHook(() => useQueuedChatMessageStore([]));
    const markerKey = queuedChatDeletedProjectStorageKey("deleted-project");
    const marker = "deleted:v2:0:cross-tab";

    window.localStorage.setItem(markerKey, marker);
    act(() => dispatchStorage(markerKey, null, marker));

    expect(observer.result.current.messages).toEqual([
      expect.objectContaining({
        id: legacy.id,
        delivery_state: "reconcile_required",
        delivery_storage_failed: true,
      }),
    ]);
    expect(window.localStorage.getItem(key)).toBe(raw);
    observer.unmount();

    const reloaded = renderHook(() => useQueuedChatMessageStore([]));
    expect(reloaded.result.current.messages).toEqual([
      expect.objectContaining({
        id: legacy.id,
        delivery_state: "reconcile_required",
        delivery_storage_failed: true,
      }),
    ]);
    expect(window.localStorage.getItem(key)).toBe(raw);
  });

  it("never accepts an old-generation durable submitting fence", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    window.localStorage.setItem(queuedChatMessagesResetEpochStorageKey, "current-epoch");
    const stale = {
      ...queued("stale-submitting-fence"),
      delivery_state: "submitting" as const,
      delivery_idempotency_keyed: true,
      delivery_baseline_message_ids: [],
    };
    const staleKey = queuedChatMessageStorageKey(stale.id);
    const staleRaw = JSON.stringify(stale);
    const store = renderHook(() => useQueuedChatMessageStore([]));
    window.localStorage.setItem(staleKey, staleRaw);
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation(() => {});

    act(() => dispatchStorage(staleKey, null, staleRaw));

    expect(store.result.current.messages).toEqual([
      expect.objectContaining({
        id: stale.id,
        delivery_state: "reconcile_required",
        delivery_storage_failed: true,
      }),
    ]);
    expect(store.result.current.hasDurableSubmittingFence(stale)).toBe(false);
  });

  it("removes an old-generation project tombstone written inside the reset snapshot window", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const store = renderHook(() => useQueuedChatMessageStore([]));
    const markerKey = queuedChatDeletedProjectStorageKey("snapshot-race-project");
    const originalSetItem = window.localStorage.setItem.bind(window.localStorage);
    let injected = false;

    vi.spyOn(Storage.prototype, "setItem").mockImplementation((key, value) => {
      if (!injected && key === queuedChatMessagesResetEpochStorageKey) {
        injected = true;
        originalSetItem(markerKey, "deleted:v2:0:late-old-generation");
      }
      return originalSetItem(key, value);
    });

    let cleared = false;
    act(() => {
      cleared = store.result.current.clear();
    });

    expect(injected).toBe(true);
    expect(cleared).toBe(true);
    expect(window.localStorage.getItem(markerKey)).toBeNull();
  });

  it("removes late unscoped records even when their payload claims the new generation", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const store = renderHook(() => useQueuedChatMessageStore([]));
    const message = queued("late-unscoped-reset-record");
    const itemKey = `${queuedChatMessagesStorageKey}.item.${encodeURIComponent(message.id)}`;
    const projectID = "late-unscoped-reset-project";
    const markerKey = `${queuedChatMessagesStorageKey}.deletedProject.${encodeURIComponent(
      projectID,
    )}`;
    const originalSetItem = window.localStorage.setItem.bind(window.localStorage);
    let injected = false;

    vi.spyOn(Storage.prototype, "setItem").mockImplementation((key, value) => {
      const result = originalSetItem(key, value);
      if (!injected && key === queuedChatMessagesResetEpochStorageKey) {
        injected = true;
        originalSetItem(itemKey, JSON.stringify({ ...message, delivery_storage_epoch: value }));
        originalSetItem(markerKey, `deleted:v2:${encodeURIComponent(value)}:late-unscoped-writer`);
      }
      return result;
    });

    let cleared = false;
    act(() => {
      cleared = store.result.current.clear();
    });

    expect(injected).toBe(true);
    expect(cleared).toBe(true);
    expect(window.localStorage.getItem(itemKey)).toBeNull();
    expect(window.localStorage.getItem(markerKey)).toBeNull();
    expect(store.result.current.messages).toEqual([]);
  });

  it("retries a failed legacy migration with its captured reset generation", () => {
    const legacy = queued("retry-migration");
    const legacyRecord = { ...legacy };
    delete legacyRecord.delivery_storage_epoch;
    window.localStorage.setItem(queuedChatMessagesStorageKey, JSON.stringify([legacyRecord]));
    const originalSetItem = window.localStorage.setItem.bind(window.localStorage);
    const writeSpy = vi.spyOn(Storage.prototype, "setItem").mockImplementation((key, value) => {
      if (isQueuedItemStorageKey(key, legacy.id)) {
        throw new DOMException("quota exceeded", "QuotaExceededError");
      }
      return originalSetItem(key, value);
    });

    const store = renderHook(() => useQueuedChatMessageStore([]));
    expect(store.result.current.messages).toEqual([
      expect.objectContaining({
        id: legacy.id,
        delivery_storage_epoch: "0",
        delivery_storage_failed: true,
      }),
    ]);
    writeSpy.mockRestore();

    act(() => store.result.current.setMessages((current) => current));

    expect(readQueuedChatMessagesFromStorage(window.localStorage)).toEqual([
      expect.objectContaining(legacy),
    ]);
    expect(store.result.current.messages).toEqual([expect.objectContaining(legacy)]);
  });

  it("tombstones deleted projects and refuses later queue admission", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const store = renderHook(() => useQueuedChatMessageStore([]));

    let cleaned = false;
    act(() => {
      cleaned = store.result.current.deleteProjectWhere(
        "project-deleted",
        (message) => message.project_id === "project-deleted",
      );
    });
    expect(cleaned).toBe(true);
    expect(
      window.localStorage.getItem(queuedChatDeletedProjectStorageKey("project-deleted")),
    ).toMatch(/^deleted:/);

    let admission = "admitted" as ReturnType<typeof store.result.current.enqueueMessage>;
    act(() => {
      admission = store.result.current.enqueueMessage({
        ...queued("after-project-delete"),
        project_id: "project-deleted",
      });
    });
    expect(admission).toBe("project_deleted");
    expect(store.result.current.messages).toEqual([]);
  });

  it("tombstones deleted sessions and refuses stale-tab queue admission", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const owner = renderHook(() => useQueuedChatMessageStore([]));
    const staleTab = renderHook(() => useQueuedChatMessageStore([]));

    let cleaned = false;
    act(() => {
      cleaned = owner.result.current.deleteSession("chat-a");
    });

    expect(cleaned).toBe(true);
    const marker = window.localStorage.getItem(queuedChatDeletedSessionStorageKey("chat-a"));
    expect(marker).toMatch(/^deleted:v1:0:/);
    expect(marker).not.toContain("message");

    let admission = "admitted" as ReturnType<typeof staleTab.result.current.enqueueMessage>;
    act(() => {
      admission = staleTab.result.current.enqueueMessage(queued("after-session-delete"));
    });
    expect(admission).toBe("session_deleted");
    expect(staleTab.result.current.messages).toEqual([]);
    expect(queuedItemStorageKeys("after-session-delete")).toEqual([]);
  });

  it("reports an unknown session deletion fence when browser storage is unavailable", () => {
    const storageGetter = vi.spyOn(window, "localStorage", "get").mockImplementation(() => {
      throw new DOMException("storage denied", "SecurityError");
    });

    expect(queuedChatSessionDeletionFenceStatus("chat-a")).toBe("unknown");

    storageGetter.mockRestore();
  });

  it("reports whether the current session deletion fence is present", () => {
    expect(queuedChatSessionDeletionFenceStatus("chat-a")).toBe("absent");
    window.localStorage.setItem(
      queuedChatDeletedSessionStorageKey("chat-a"),
      "deleted:v1:0:status-check",
    );

    expect(queuedChatSessionDeletionFenceStatus("chat-a")).toBe("present");
  });

  it("blocks submitting fences and queued writes after a session tombstone", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const store = renderHook(() => useQueuedChatMessageStore([]));
    const ready = queued("session-fenced-submitting");
    act(() => {
      expect(store.result.current.enqueueMessage(ready)).toBe("admitted");
    });
    act(() => {
      store.result.current.setMessages((current) =>
        current.map((message) => ({
          ...message,
          delivery_state: "submitting" as const,
          delivery_idempotency_keyed: true,
          delivery_baseline_message_ids: [],
        })),
      );
    });
    const submitting = store.result.current.messages[0];
    expect(store.result.current.hasDurableSubmittingFence(submitting)).toBe(true);

    window.localStorage.setItem(
      queuedChatDeletedSessionStorageKey(ready.session_id),
      "deleted:v1:0:before-stale-write",
    );

    expect(store.result.current.hasDurableSubmittingFence(submitting)).toBe(false);
    act(() => {
      store.result.current.setMessages((current) => current);
    });
    expect(store.result.current.messages).toEqual([]);
    expect(queuedItemStorageKeys(ready.id)).toEqual([]);
  });

  it("rejects a submitting fence when a project tombstone lands during its durable read", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const store = renderHook(() => useQueuedChatMessageStore([]));
    const ready = { ...queued("project-fenced-submitting"), project_id: "project-a" };
    act(() => {
      expect(store.result.current.enqueueMessage(ready)).toBe("admitted");
      store.result.current.setMessages((current) =>
        current.map((message) => ({
          ...message,
          delivery_state: "submitting" as const,
          delivery_idempotency_keyed: true,
          delivery_baseline_message_ids: [],
        })),
      );
    });
    const submitting = store.result.current.messages[0];
    const durableKey = queuedRevisionStorageKey(ready.id);
    const projectMarkerKey = queuedChatDeletedProjectStorageKey("project-a");
    const originalGetItem = window.localStorage.getItem.bind(window.localStorage);
    const originalSetItem = window.localStorage.setItem.bind(window.localStorage);
    let injected = false;
    vi.spyOn(Storage.prototype, "getItem").mockImplementation((key) => {
      const value = originalGetItem(key);
      if (!injected && key === durableKey) {
        injected = true;
        originalSetItem(projectMarkerKey, "deleted:v2:0:project-fence-read-race");
      }
      return value;
    });

    expect(store.result.current.hasDurableSubmittingFence(submitting)).toBe(false);
    expect(injected).toBe(true);
  });

  it("refuses session prompt cleanup until its tombstone is durable", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const persisted = persistQueuedRevision(queued("session-quota-prompt"));
    const store = renderHook(() => useQueuedChatMessageStore([]));
    const markerKey = queuedChatDeletedSessionStorageKey("chat-a");
    const originalSetItem = window.localStorage.setItem.bind(window.localStorage);
    vi.spyOn(Storage.prototype, "setItem").mockImplementation((key, value) => {
      if (key === markerKey && window.localStorage.getItem(persisted.key) !== null) {
        throw new DOMException("quota exceeded", "QuotaExceededError");
      }
      return originalSetItem(key, value);
    });

    let cleaned = true;
    act(() => {
      cleaned = store.result.current.deleteSession("chat-a");
    });

    expect(cleaned).toBe(false);
    expect(window.localStorage.getItem(markerKey)).toBeNull();
    expect(window.localStorage.getItem(persisted.key)).toBe(persisted.raw);
    expect(store.result.current.messages).toEqual([
      expect.objectContaining({
        id: persisted.message.id,
        delivery_state: "reconcile_required",
        delivery_storage_failed: true,
      }),
    ]);
  });

  it("keeps a session fence when record cleanup fails and completes on reload", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const persisted = persistQueuedRevision(queued("session-cleanup-retry"));
    const store = renderHook(() => useQueuedChatMessageStore([]));
    const originalRemoveItem = window.localStorage.removeItem.bind(window.localStorage);
    const removeSpy = vi.spyOn(Storage.prototype, "removeItem").mockImplementation((key) => {
      if (key === persisted.key) return;
      originalRemoveItem(key);
    });

    let cleaned = true;
    act(() => {
      cleaned = store.result.current.deleteSession("chat-a");
    });

    expect(cleaned).toBe(false);
    expect(window.localStorage.getItem(queuedChatDeletedSessionStorageKey("chat-a"))).toMatch(
      /^deleted:v1:0:/,
    );
    expect(window.localStorage.getItem(persisted.key)).toBe(persisted.raw);
    expect(store.result.current.messages).toEqual([
      expect.objectContaining({
        id: persisted.message.id,
        delivery_state: "reconcile_required",
        delivery_storage_failed: true,
      }),
    ]);

    removeSpy.mockRestore();
    store.unmount();
    const reloaded = renderHook(() => useQueuedChatMessageStore([]));
    expect(reloaded.result.current.messages).toEqual([]);
    expect(window.localStorage.getItem(persisted.key)).toBeNull();
  });

  it("purges a tombstoned session across storage events and reload", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const message = queued("cross-tab-session");
    const persisted = persistQueuedRevision(message);
    const observer = renderHook(() => useQueuedChatMessageStore([]));
    expect(observer.result.current.messages).toEqual([expect.objectContaining(message)]);

    const markerKey = queuedChatDeletedSessionStorageKey(message.session_id);
    const marker = "deleted:v1:0:cross-tab";
    window.localStorage.setItem(markerKey, marker);
    act(() => dispatchStorage(markerKey, null, marker));
    expect(observer.result.current.messages).toEqual([]);
    expect(window.localStorage.getItem(persisted.key)).toBeNull();

    const late = { ...message, id: "late-after-session-tombstone" };
    const persistedLate = persistQueuedRevision(late);
    act(() => dispatchStorage(persistedLate.key, null, persistedLate.raw));
    expect(observer.result.current.messages).toEqual([]);
    expect(window.localStorage.getItem(persistedLate.key)).toBeNull();
    observer.unmount();

    const crashWindow = { ...message, id: "crash-window-session" };
    const persistedCrashWindow = persistQueuedRevision(crashWindow);
    const reloaded = renderHook(() => useQueuedChatMessageStore([]));
    expect(reloaded.result.current.messages).toEqual([]);
    expect(window.localStorage.getItem(persistedCrashWindow.key)).toBeNull();
  });

  it("rejects an enqueue when a session tombstone lands at the item-write boundary", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const store = renderHook(() => useQueuedChatMessageStore([]));
    const message = queued("session-delete-write-race");
    const markerKey = queuedChatDeletedSessionStorageKey(message.session_id);
    const marker = "deleted:v1:0:item-write-race";
    const originalSetItem = window.localStorage.setItem.bind(window.localStorage);
    let injected = false;
    vi.spyOn(Storage.prototype, "setItem").mockImplementation((key, value) => {
      const result = originalSetItem(key, value);
      if (!injected && isQueuedItemStorageKey(key, message.id)) {
        injected = true;
        originalSetItem(markerKey, marker);
      }
      return result;
    });

    let admission = "admitted" as ReturnType<typeof store.result.current.enqueueMessage>;
    act(() => {
      admission = store.result.current.enqueueMessage(message);
    });

    expect(injected).toBe(true);
    expect(admission).toBe("session_deleted");
    expect(store.result.current.messages).toEqual([]);
    expect(queuedItemStorageKeys(message.id)).toEqual([]);
  });

  it("reports storage failure when a fenced enqueue cannot remove its late record", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const store = renderHook(() => useQueuedChatMessageStore([]));
    const message = queued("session-delete-write-cleanup-failure");
    const markerKey = queuedChatDeletedSessionStorageKey(message.session_id);
    const marker = "deleted:v1:0:item-cleanup-failure";
    const originalSetItem = window.localStorage.setItem.bind(window.localStorage);
    const originalRemoveItem = window.localStorage.removeItem.bind(window.localStorage);
    let injected = false;
    vi.spyOn(Storage.prototype, "setItem").mockImplementation((key, value) => {
      const result = originalSetItem(key, value);
      if (!injected && isQueuedItemStorageKey(key, message.id)) {
        injected = true;
        originalSetItem(markerKey, marker);
      }
      return result;
    });
    const removeSpy = vi.spyOn(Storage.prototype, "removeItem").mockImplementation((key) => {
      if (isQueuedItemStorageKey(key, message.id)) return;
      originalRemoveItem(key);
    });

    let admission = "admitted" as ReturnType<typeof store.result.current.enqueueMessage>;
    act(() => {
      admission = store.result.current.enqueueMessage(message);
    });

    expect(injected).toBe(true);
    expect(admission).toBe("storage_failed");
    expect(store.result.current.messages).toEqual([]);
    expect(queuedItemStorageKeys(message.id)).toHaveLength(1);

    removeSpy.mockRestore();
    store.unmount();
    const reloaded = renderHook(() => useQueuedChatMessageStore([]));
    expect(reloaded.result.current.messages).toEqual([]);
    expect(queuedItemStorageKeys(message.id)).toEqual([]);
  });

  it("does not migrate a legacy prompt for a tombstoned session", () => {
    const legacy = queued("legacy-session-delete");
    const legacyRecord = { ...legacy };
    delete legacyRecord.delivery_storage_epoch;
    window.localStorage.setItem(
      queuedChatDeletedSessionStorageKey(legacy.session_id),
      "deleted:v1:0:before-migration",
    );
    window.localStorage.setItem(queuedChatMessagesStorageKey, JSON.stringify([legacyRecord]));

    const store = renderHook(() => useQueuedChatMessageStore([]));

    expect(store.result.current.messages).toEqual([]);
    expect(queuedItemStorageKeys(legacy.id)).toEqual([]);
    expect(window.localStorage.getItem(queuedChatMessagesStorageKey)).toBeNull();
  });

  it("refuses project prompt cleanup until its tombstone is durable", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const projectID = "project-quota-retry";
    const message = { ...queued("project-quota-prompt"), project_id: projectID };
    const itemKey = queuedChatMessageStorageKey(message.id);
    window.localStorage.setItem(itemKey, JSON.stringify(message));
    const store = renderHook(() => useQueuedChatMessageStore([]));
    const markerKey = queuedChatDeletedProjectStorageKey(projectID);
    const originalSetItem = window.localStorage.setItem.bind(window.localStorage);
    vi.spyOn(Storage.prototype, "setItem").mockImplementation((key, value) => {
      if (key === markerKey && window.localStorage.getItem(itemKey) !== null) {
        throw new DOMException("quota exceeded", "QuotaExceededError");
      }
      return originalSetItem(key, value);
    });

    let cleaned = false;
    act(() => {
      cleaned = store.result.current.deleteProjectWhere(
        projectID,
        (queuedMessage) => queuedMessage.project_id === projectID,
      );
    });

    expect(cleaned).toBe(false);
    expect(window.localStorage.getItem(itemKey)).toBe(JSON.stringify(message));
    expect(window.localStorage.getItem(markerKey)).toBeNull();
    expect(store.result.current.messages).toEqual([
      expect.objectContaining({
        id: message.id,
        delivery_state: "reconcile_required",
        delivery_storage_failed: true,
      }),
    ]);
  });

  it("quarantines malformed project ownership instead of treating it as project-free", () => {
    for (const [suffix, projectID] of [
      ["whitespace", "   "],
      ["null", null],
      ["number", 42],
    ] as const) {
      window.localStorage.clear();
      window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
      const malformed = {
        ...queued(`malformed-project-owner-${suffix}`),
        project_id: projectID,
      };
      const key = queuedChatMessageStorageKey(malformed.id);
      const raw = JSON.stringify(malformed);
      window.localStorage.setItem(key, raw);

      const store = renderHook(() => useQueuedChatMessageStore([]));

      expect(store.result.current.messages).toEqual([]);
      expect(window.localStorage.getItem(key)).toBe(raw);
      let admission = "admitted" as ReturnType<typeof store.result.current.enqueueMessage>;
      act(() => {
        admission = store.result.current.enqueueMessage(
          queued(`blocked-by-malformed-owner-${suffix}`),
        );
      });
      expect(admission).toBe("storage_failed");
      store.unmount();
    }
  });

  it("fails queue admission closed when a project tombstone cannot be interpreted", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    window.localStorage.setItem(queuedChatDeletedProjectStorageKey("project-unknown"), "corrupt");
    const store = renderHook(() => useQueuedChatMessageStore([]));
    const message = { ...queued("unknown-project-state"), project_id: "project-unknown" };

    let admission = "admitted" as ReturnType<typeof store.result.current.enqueueMessage>;
    act(() => {
      admission = store.result.current.enqueueMessage(message);
    });

    expect(admission).toBe("storage_failed");
    expect(store.result.current.messages).toEqual([]);
    expect(window.localStorage.getItem(queuedChatMessageStorageKey(message.id))).toBeNull();
  });

  it("purges a tombstoned project on storage notification and initialization", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const message = { ...queued("cross-tab-project"), project_id: "project-deleted" };
    const persistedMessage = persistQueuedRevision(message);
    const observer = renderHook(() => useQueuedChatMessageStore([]));
    expect(observer.result.current.messages).toEqual([expect.objectContaining(message)]);

    const markerKey = queuedChatDeletedProjectStorageKey("project-deleted");
    const marker = "deleted:cross-tab";
    window.localStorage.setItem(markerKey, marker);
    act(() => dispatchStorage(markerKey, null, marker));
    expect(observer.result.current.messages).toEqual([]);
    expect(window.localStorage.getItem(persistedMessage.key)).toBeNull();

    const late = { ...message, id: "late-after-project-tombstone" };
    const persistedLate = persistQueuedRevision(late);
    act(() => dispatchStorage(persistedLate.key, null, persistedLate.raw));
    expect(observer.result.current.messages).toEqual([]);
    expect(window.localStorage.getItem(persistedLate.key)).toBeNull();
    observer.unmount();

    const crashWindow = { ...message, id: "crash-window-project" };
    const persistedCrashWindow = persistQueuedRevision(crashWindow);
    const reloaded = renderHook(() => useQueuedChatMessageStore([]));
    expect(reloaded.result.current.messages).toEqual([]);
    expect(window.localStorage.getItem(persistedCrashWindow.key)).toBeNull();
  });

  it("quarantines all rows until an immutable revision handoff becomes unambiguous", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const ready = persistQueuedRevision(queued("revision-handoff"), "revision-a");
    const unrelated = persistQueuedRevision(
      queued("revision-handoff-unrelated", "2026-07-13T10:00:01Z"),
      "revision-unrelated",
    );
    const observer = renderHook(() => useQueuedChatMessageStore([]));
    const submitting: QueuedChatMessage = {
      ...ready.message,
      delivery_state: "submitting",
      delivery_idempotency_keyed: true,
      delivery_baseline_message_ids: [],
      delivery_storage_revision: "revision-b",
    };
    const submittingKey = queuedChatMessageStorageKey(
      submitting.id,
      submitting.delivery_storage_epoch,
      submitting.delivery_storage_revision,
    );
    const submittingRaw = JSON.stringify(submitting);

    window.localStorage.setItem(submittingKey, submittingRaw);
    act(() => dispatchStorage(submittingKey, null, submittingRaw));

    expect(observer.result.current.messages).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          id: ready.message.id,
          delivery_state: "reconcile_required",
          delivery_storage_failed: true,
          delivery_storage_fenced: true,
        }),
        expect.objectContaining({
          id: unrelated.message.id,
          delivery_state: "reconcile_required",
          delivery_storage_failed: true,
        }),
      ]),
    );

    window.localStorage.removeItem(ready.key);
    act(() => dispatchStorage(ready.key, ready.raw, null));

    expect(observer.result.current.messages).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          id: submitting.id,
          delivery_state: "reconcile_required",
          delivery_storage_fenced: true,
        }),
        expect.objectContaining({
          id: unrelated.message.id,
          content: unrelated.message.content,
        }),
      ]),
    );
    expect(
      observer.result.current.messages.find((message) => message.id === unrelated.message.id),
    ).not.toHaveProperty("delivery_state");
    expect(window.localStorage.getItem(submittingKey)).toBe(submittingRaw);
  });

  it.each(["edited", "submitting"] as const)(
    "preserves and blocks a concurrent %s revision at the Remove boundary",
    (replacementKind) => {
      window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
      const ready = persistQueuedRevision(
        queued(`remove-revision-race-${replacementKind}`),
        "revision-a",
      );
      const store = renderHook(() => useQueuedChatMessageStore([]));
      const replacement: QueuedChatMessage = {
        ...ready.message,
        content: `${replacementKind} replacement`,
        ...(replacementKind === "submitting"
          ? {
              delivery_state: "submitting" as const,
              delivery_idempotency_keyed: true,
              delivery_baseline_message_ids: [],
            }
          : {}),
        delivery_storage_revision: "revision-b",
      };
      const replacementKey = queuedChatMessageStorageKey(
        replacement.id,
        replacement.delivery_storage_epoch,
        replacement.delivery_storage_revision,
      );
      const replacementRaw = JSON.stringify(replacement);
      const originalRemoveItem = window.localStorage.removeItem.bind(window.localStorage);
      let injected = false;
      vi.spyOn(Storage.prototype, "removeItem").mockImplementation((key) => {
        if (!injected && key === ready.key) {
          injected = true;
          window.localStorage.setItem(replacementKey, replacementRaw);
        }
        return originalRemoveItem(key);
      });

      act(() => store.result.current.setMessages([]));

      expect(injected).toBe(true);
      expect(window.localStorage.getItem(ready.key)).toBeNull();
      expect(window.localStorage.getItem(replacementKey)).toBe(replacementRaw);
      expect(storageKeysWithPrefix(queuedChatDeletedItemStorageKeyPrefix)).not.toEqual([]);
      expect(store.result.current.messages).toEqual([
        expect.objectContaining({
          id: replacement.id,
          content: replacement.content,
          delivery_state: "reconcile_required",
          ...(replacementKind === "submitting"
            ? { delivery_storage_fenced: true }
            : { delivery_storage_conflict: "ready_replacement" }),
        }),
      ]);
      store.unmount();

      const reloaded = renderHook(() => useQueuedChatMessageStore([]));
      expect(reloaded.result.current.messages).toEqual([
        expect.objectContaining({
          id: replacement.id,
          content: replacement.content,
          delivery_state: "reconcile_required",
          ...(replacementKind === "submitting"
            ? { delivery_storage_fenced: true }
            : { delivery_storage_conflict: "ready_replacement" }),
        }),
      ]);
    },
  );

  it("keeps migrated legacy shadows consumed and blocks changed rewrites", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const message = queued("migrated-delete-reload");
    const legacy = { ...message };
    delete legacy.delivery_storage_epoch;
    const legacyKey = `${queuedChatMessageStorageKeyPrefix}${encodeURIComponent(message.id)}`;
    const legacyRaw = JSON.stringify(legacy);
    window.localStorage.setItem(legacyKey, legacyRaw);
    const store = renderHook(() => useQueuedChatMessageStore([]));
    expect(queuedItemStorageKeys(message.id)).toHaveLength(2);

    act(() => store.result.current.setMessages([]));
    expect(store.result.current.messages).toEqual([]);
    expect(window.localStorage.getItem(legacyKey)).toBe(legacyRaw);
    expect(queuedItemStorageKeys(message.id)).toEqual([legacyKey]);
    expect(storageKeysWithPrefix(queuedChatMigratedLegacyStorageKeyPrefix)).not.toEqual([]);
    store.unmount();

    const consumed = renderHook(() => useQueuedChatMessageStore([]));
    expect(consumed.result.current.messages).toEqual([]);
    window.localStorage.setItem(legacyKey, legacyRaw);
    act(() => dispatchStorage(legacyKey, legacyRaw, legacyRaw));
    expect(consumed.result.current.messages).toEqual([]);

    const changed = { ...legacy, content: "changed legacy rewrite" };
    const changedRaw = JSON.stringify(changed);
    window.localStorage.setItem(legacyKey, changedRaw);
    act(() => dispatchStorage(legacyKey, legacyRaw, changedRaw));
    expect(consumed.result.current.messages).toEqual([
      expect.objectContaining({
        id: message.id,
        content: changed.content,
        delivery_state: "reconcile_required",
        delivery_storage_conflict: "ready_replacement",
      }),
    ]);
    consumed.unmount();

    const reloaded = renderHook(() => useQueuedChatMessageStore([]));
    expect(reloaded.result.current.messages).toEqual([
      expect.objectContaining({
        id: message.id,
        content: changed.content,
        delivery_state: "reconcile_required",
        delivery_storage_conflict: "ready_replacement",
      }),
    ]);
    expect(queuedItemStorageKeys(message.id)).toEqual([legacyKey]);
  });

  it("deletes a current predicate match despite an unrelated stale cleanup failure", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    window.localStorage.setItem(queuedChatMessagesResetEpochStorageKey, "current-epoch");
    const current = persistQueuedRevision(
      {
        ...queued("current-predicate-cleanup"),
        session_id: "remove-this-session",
        project_id: "",
        delivery_storage_epoch: "current-epoch",
      },
      "revision-current",
    );
    const stale = persistQueuedRevision(
      {
        ...queued("unrelated-stale-cleanup"),
        session_id: "keep-blocked-session",
        project_id: "",
      },
      "revision-stale",
    );
    const originalRemoveItem = window.localStorage.removeItem.bind(window.localStorage);
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation((key) => {
      if (key === stale.key) return;
      return originalRemoveItem(key);
    });
    const store = renderHook(() => useQueuedChatMessageStore([]));

    let deleted = true;
    act(() => {
      deleted = store.result.current.deleteWhere(
        (message) => message.session_id === "remove-this-session",
      );
    });

    expect(deleted).toBe(false);
    expect(window.localStorage.getItem(current.key)).toBeNull();
    expect(store.result.current.messages.some((message) => message.id === current.message.id)).toBe(
      false,
    );
    originalRemoveItem(stale.key);
    act(() => dispatchStorage(stale.key, stale.raw, null));
    expect(store.result.current.messages.some((message) => message.id === current.message.id)).toBe(
      false,
    );
  });

  it("reset removes migration fingerprints and item, session tombstones", () => {
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const message = queued("reset-legacy-artifacts");
    const legacy = { ...message };
    delete legacy.delivery_storage_epoch;
    const legacyKey = `${queuedChatMessageStorageKeyPrefix}${encodeURIComponent(message.id)}`;
    window.localStorage.setItem(legacyKey, JSON.stringify(legacy));
    const store = renderHook(() => useQueuedChatMessageStore([]));
    act(() => store.result.current.setMessages([]));
    act(() => {
      expect(store.result.current.deleteSession("reset-deleted-session")).toBe(true);
    });
    expect(storageKeysWithPrefix(queuedChatMigratedLegacyStorageKeyPrefix)).not.toEqual([]);
    expect(storageKeysWithPrefix(queuedChatDeletedItemStorageKeyPrefix)).not.toEqual([]);
    expect(storageKeysWithPrefix(queuedChatDeletedSessionStorageKeyPrefix)).not.toEqual([]);

    let cleared = false;
    act(() => {
      cleared = store.result.current.clear();
    });

    expect(cleared).toBe(true);
    expect(storageKeysWithPrefix(queuedChatMigratedLegacyStorageKeyPrefix)).toEqual([]);
    expect(storageKeysWithPrefix(queuedChatDeletedItemStorageKeyPrefix)).toEqual([]);
    expect(storageKeysWithPrefix(queuedChatDeletedSessionStorageKeyPrefix)).toEqual([]);
    expect(window.localStorage.getItem(legacyKey)).toBeNull();
  });

  it("rejects malformed immutable revisions and hash markers", () => {
    const malformedRevision = {
      ...queued("malformed-revision"),
      delivery_storage_revision: " padded ",
    };
    expect(parseQueuedChatMessageList([malformedRevision])).toEqual([]);

    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    const legacy = queued("malformed-migration-marker");
    const legacyRecord = { ...legacy };
    delete legacyRecord.delivery_storage_epoch;
    const legacyKey = `${queuedChatMessageStorageKeyPrefix}${encodeURIComponent(legacy.id)}`;
    window.localStorage.setItem(legacyKey, JSON.stringify(legacyRecord));
    window.localStorage.setItem(
      `${queuedChatMigratedLegacyStorageKeyPrefix}0:${encodeURIComponent(legacy.id)}`,
      `sha256:${"z".repeat(64)}`,
    );

    const store = renderHook(() => useQueuedChatMessageStore([]));
    expect(store.result.current.messages).toEqual([
      expect.objectContaining({
        id: legacy.id,
        delivery_state: "reconcile_required",
        delivery_storage_failed: true,
      }),
    ]);
  });
});
