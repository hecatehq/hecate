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
  it("keeps valid queued chat messages", () => {
    expect(
      parseQueuedChatMessageList([
        {
          id: "queued-1",
          session_id: "chat-1",
          content: "continue",
          execution_mode: "direct_model",
          provider_filter: "auto",
          model: "gpt-4o-mini",
          workspace: "/tmp/hecate",
          system_prompt: "Be concise.",
          agent_id: "hecate",
          created_at: "2026-05-18T10:00:00.000Z",
        },
      ]),
    ).toEqual([
      {
        id: "queued-1",
        session_id: "chat-1",
        content: "continue",
        execution_mode: "direct_model",
        provider_filter: "auto",
        model: "gpt-4o-mini",
        workspace: "/tmp/hecate",
        system_prompt: "Be concise.",
        agent_id: "hecate",
        created_at: "2026-05-18T10:00:00.000Z",
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
});

describe("parseStoredChatTarget", () => {
  it("accepts the two current discriminant values", () => {
    expect(parseStoredChatTarget("agent")).toBe("agent");
    expect(parseStoredChatTarget("external_agent")).toBe("external_agent");
  });

  it("coerces the legacy 'model' literal forward to 'agent'", () => {
    // Older installs encoded "tools off for new chats" as
    // hecate.chatTarget = "model". The tools-off intent is recovered
    // separately by the per-session chat-tools-enabled migration; the
    // top-level discriminant should land on the safe default rather
    // than wipe.
    expect(parseStoredChatTarget("model")).toBe("agent");
  });

  it("returns null for unknown values so usePersistedState wipes the key", () => {
    expect(parseStoredChatTarget("")).toBeNull();
    expect(parseStoredChatTarget("garbage")).toBeNull();
    expect(parseStoredChatTarget("AGENT")).toBeNull();
  });
});

describe("normalizeStoredChatTarget", () => {
  it("preserves external_agent and coerces everything else to agent", () => {
    expect(normalizeStoredChatTarget("agent")).toBe("agent");
    expect(normalizeStoredChatTarget("external_agent")).toBe("external_agent");
    // Coercive normalizer — used by code paths where the wider type
    // has already vouched for the input. The legacy "model" literal
    // and unknown values land on the safe default.
    expect(normalizeStoredChatTarget("model")).toBe("agent");
    expect(normalizeStoredChatTarget("garbage")).toBe("agent");
    expect(normalizeStoredChatTarget("")).toBe("agent");
  });
});

describe("normalizeStoredHecateChatTarget", () => {
  it("coerces 'agent' and the legacy 'model' literal to 'agent'", () => {
    expect(normalizeStoredHecateChatTarget("agent")).toBe("agent");
    // Persisted `chatTargetBySessionID` entries from earlier installs
    // may still carry `"model"` for sessions that were tools-off.
    // Map those to "agent" so the per-session map stays well-typed;
    // the original tools-off intent is recovered separately by the
    // chat-slice migration that reads raw localStorage.
    expect(normalizeStoredHecateChatTarget("model")).toBe("agent");
  });

  it("returns the empty string for unknown values so the entry is dropped", () => {
    expect(normalizeStoredHecateChatTarget("")).toBe("");
    expect(normalizeStoredHecateChatTarget("external_agent")).toBe("");
    expect(normalizeStoredHecateChatTarget("garbage")).toBe("");
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
  it("collapses the agent-side execution modes back to 'agent'", () => {
    // Both tools-on (hecate_task) and the tools-off / capability-
    // downgrade runtime fallback (direct_model) resolve to the same
    // user-target value: "agent". The tools-on/off axis lives on the
    // separate chatToolsEnabledBySessionID map.
    expect(executionModeToChatTarget("hecate_task")).toBe("agent");
    expect(executionModeToChatTarget("direct_model")).toBe("agent");
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
