import { describe, expect, it } from "vitest";

import {
  chatTargetToExecutionMode,
  executionModeToChatTarget,
  normalizeStoredChatTarget,
  normalizeStoredHecateChatTarget,
  parseQueuedChatMessageList,
  parseStoredChatTarget,
} from "./_shared";

describe("parseQueuedChatMessageList", () => {
  const validQueuedMessage = {
    id: "queued-1",
    session_id: "chat-1",
    content: "continue",
    execution_mode: "hecate_task",
    tools_enabled: false,
    provider_filter: "auto",
    model: "gpt-4o-mini",
    workspace: "/tmp/hecate",
    system_prompt: "Be concise.",
    agent_id: "hecate",
    created_at: "2026-05-18T10:00:00.000Z",
  };

  it("keeps valid ready queued chat messages", () => {
    expect(parseQueuedChatMessageList([validQueuedMessage])).toEqual([validQueuedMessage]);
  });

  it("loads stored submitting attempts as reconciliation-required with a sanitized baseline", () => {
    expect(
      parseQueuedChatMessageList([
        {
          ...validQueuedMessage,
          delivery_state: "submitting",
          delivery_baseline_message_ids: ["message-1", "", "message-1", 2, "message-2"],
        },
      ]),
    ).toEqual([
      {
        ...validQueuedMessage,
        delivery_state: "reconcile_required",
        delivery_baseline_message_ids: ["message-1", "message-2"],
      },
    ]);
  });

  it("preserves current delivery states and fails unknown states closed", () => {
    expect(
      parseQueuedChatMessageList([
        { ...validQueuedMessage, id: "retryable", delivery_state: "retryable" },
        {
          ...validQueuedMessage,
          id: "reconcile",
          delivery_state: "reconcile_required",
        },
        { ...validQueuedMessage, id: "unknown", delivery_state: "future_state" },
      ]),
    ).toEqual([
      expect.objectContaining({ id: "retryable", delivery_state: "retryable" }),
      expect.objectContaining({ id: "reconcile", delivery_state: "reconcile_required" }),
      expect.objectContaining({ id: "unknown", delivery_state: "reconcile_required" }),
    ]);
  });

  it("preserves client-request conflict provenance for fail-closed reconciliation", () => {
    expect(
      parseQueuedChatMessageList([
        {
          ...validQueuedMessage,
          delivery_state: "reconcile_required",
          delivery_error_code: "chat.client_request_conflict",
          delivery_idempotency_keyed: true,
        },
      ]),
    ).toEqual([
      expect.objectContaining({
        delivery_state: "reconcile_required",
        delivery_error_code: "chat.client_request_conflict",
        delivery_idempotency_keyed: true,
      }),
    ]);
  });

  it("loads the legacy retry marker as reconciliation-required instead of retrying", () => {
    expect(parseQueuedChatMessageList([{ ...validQueuedMessage, retry_required: true }])).toEqual([
      {
        ...validQueuedMessage,
        delivery_state: "reconcile_required",
      },
    ]);
  });

  it("drops queued chat messages without a supported execution mode", () => {
    expect(
      parseQueuedChatMessageList([
        {
          id: "legacy-queued-model",
          session_id: "chat-1",
          content: "legacy direct turn",
          runtime_kind: "model",
        },
        {
          id: "queued-tools",
          session_id: "chat-1",
          content: "valid tools turn",
          execution_mode: "hecate_task",
        },
      ]),
    ).toEqual([
      expect.objectContaining({
        id: "queued-tools",
        execution_mode: "hecate_task",
      }),
    ]);
  });

  it("rejects padded session ownership that could bypass a deletion fence", () => {
    expect(parseQueuedChatMessageList([{ ...validQueuedMessage, session_id: " chat-1 " }])).toEqual(
      [],
    );
  });

  it.each(["", " padded ", 7])(
    "rejects a noncanonical queued storage epoch %j",
    (deliveryStorageEpoch) => {
      expect(
        parseQueuedChatMessageList([
          {
            ...validQueuedMessage,
            delivery_storage_epoch: deliveryStorageEpoch,
          },
        ]),
      ).toEqual([]);
    },
  );
});

describe("parseStoredChatTarget", () => {
  it("accepts the two current discriminant values", () => {
    expect(parseStoredChatTarget("agent")).toBe("agent");
    expect(parseStoredChatTarget("external_agent")).toBe("external_agent");
  });

  it("returns null for unknown values so usePersistedState wipes the key", () => {
    expect(parseStoredChatTarget("")).toBeNull();
    expect(parseStoredChatTarget("garbage")).toBeNull();
    expect(parseStoredChatTarget("AGENT")).toBeNull();
    expect(parseStoredChatTarget("model")).toBeNull();
  });
});

describe("normalizeStoredChatTarget", () => {
  it("preserves external_agent and coerces everything else to agent", () => {
    expect(normalizeStoredChatTarget("agent")).toBe("agent");
    expect(normalizeStoredChatTarget("external_agent")).toBe("external_agent");
    expect(normalizeStoredChatTarget("model")).toBe("agent");
    expect(normalizeStoredChatTarget("garbage")).toBe("agent");
    expect(normalizeStoredChatTarget("")).toBe("agent");
  });
});

describe("normalizeStoredHecateChatTarget", () => {
  it("preserves only the current Hecate target", () => {
    expect(normalizeStoredHecateChatTarget("agent")).toBe("agent");
  });

  it("returns the empty string for unknown values so the entry is dropped", () => {
    expect(normalizeStoredHecateChatTarget("")).toBe("");
    expect(normalizeStoredHecateChatTarget("external_agent")).toBe("");
    expect(normalizeStoredHecateChatTarget("garbage")).toBe("");
    expect(normalizeStoredHecateChatTarget("model")).toBe("");
  });
});

describe("chatTargetToExecutionMode", () => {
  it("maps agent to hecate_task", () => {
    expect(chatTargetToExecutionMode("agent")).toBe("hecate_task");
  });

  it("maps external_agent to external_agent", () => {
    expect(chatTargetToExecutionMode("external_agent")).toBe("external_agent");
  });
});

describe("executionModeToChatTarget", () => {
  it("maps hecate_task back to agent", () => {
    expect(executionModeToChatTarget("hecate_task")).toBe("agent");
  });

  it("maps external_agent to external_agent", () => {
    expect(executionModeToChatTarget("external_agent")).toBe("external_agent");
  });

  it("returns the empty string for unknown modes", () => {
    expect(executionModeToChatTarget("")).toBe("");
    expect(executionModeToChatTarget("garbage")).toBe("");
    expect(executionModeToChatTarget("model")).toBe("");
  });
});
