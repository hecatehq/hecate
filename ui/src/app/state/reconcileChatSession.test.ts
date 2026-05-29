import { describe, expect, it } from "vitest";

import type { ChatMessageRecord, ChatSessionRecord } from "../../types/chat";
import { reconcileChatSession } from "./reconcileChatSession";

function session(
  id: string,
  messages: ChatMessageRecord[],
  extra?: Partial<ChatSessionRecord>,
): ChatSessionRecord {
  return {
    id,
    title: "t",
    workspace: "/tmp/ws",
    status: "running",
    messages,
    ...extra,
  };
}

function message(
  id: string,
  content: string,
  extra?: Partial<ChatMessageRecord>,
): ChatMessageRecord {
  return { id, role: "assistant", content, ...extra };
}

describe("reconcileChatSession", () => {
  it("returns the next snapshot as-is when there is no previous session", () => {
    const next = session("chat-1", [message("m1", "hi")]);
    expect(reconcileChatSession(null, next)).toBe(next);
  });

  it("returns the next snapshot as-is when the session id changed", () => {
    const prev = session("chat-1", [message("m1", "hi")]);
    const next = session("chat-2", [message("m1", "hi")]);
    // Operator switched sessions: nothing from the old transcript is reusable.
    expect(reconcileChatSession(prev, next)).toBe(next);
  });

  it("preserves the reference of messages that did not change", () => {
    const prev = session("chat-1", [message("m1", "hello"), message("m2", "world")]);
    const next = session("chat-1", [message("m1", "hello"), message("m2", "world")]);

    const result = reconcileChatSession(prev, next);

    // Top-level object is always rebuilt (spread), but unchanged rows keep identity.
    expect(result.messages?.[0]).toBe(prev.messages?.[0]);
    expect(result.messages?.[1]).toBe(prev.messages?.[1]);
  });

  it("uses the next reference only for the message that changed", () => {
    const prev = session("chat-1", [message("m1", "hello"), message("m2", "wor")]);
    const next = session("chat-1", [message("m1", "hello"), message("m2", "world")]);

    const result = reconcileChatSession(prev, next);

    // Unchanged row reused; streamed row takes the fresh identity so its
    // memoized transcript row re-renders.
    expect(result.messages?.[0]).toBe(prev.messages?.[0]);
    expect(result.messages?.[1]).toBe(next.messages?.[1]);
  });

  it("treats a deep change in a nested field as a changed message", () => {
    const prev = session("chat-1", [
      message("m1", "hello", { activities: [{ id: "a1", type: "tool", title: "run" }] }),
    ]);
    const next = session("chat-1", [
      message("m1", "hello", {
        activities: [{ id: "a1", type: "tool", title: "run", status: "done" }],
      }),
    ]);

    const result = reconcileChatSession(prev, next);

    expect(result.messages?.[0]).toBe(next.messages?.[0]);
  });

  it("never reuses messages without a stable id", () => {
    const prev = session("chat-1", [message("", "optimistic")]);
    const next = session("chat-1", [message("", "optimistic")]);

    const result = reconcileChatSession(prev, next);

    // Id-less (synthetic/optimistic) rows have no stable identity across
    // snapshots, so they always take the incoming reference.
    expect(result.messages?.[0]).toBe(next.messages?.[0]);
  });

  it("uses the next reference for messages that are new in the snapshot", () => {
    const prev = session("chat-1", [message("m1", "hello")]);
    const next = session("chat-1", [message("m1", "hello"), message("m2", "new")]);

    const result = reconcileChatSession(prev, next);

    expect(result.messages?.[0]).toBe(prev.messages?.[0]);
    expect(result.messages?.[1]).toBe(next.messages?.[1]);
  });

  it("returns next messages directly when either side is empty", () => {
    const prevEmpty = session("chat-1", []);
    const next = session("chat-1", [message("m1", "hello")]);
    expect(reconcileChatSession(prevEmpty, next).messages).toBe(next.messages);

    const prev = session("chat-1", [message("m1", "hello")]);
    const nextEmpty = session("chat-1", []);
    expect(reconcileChatSession(prev, nextEmpty).messages).toBe(nextEmpty.messages);
  });

  it("reuses segments and config_options when deep-equal", () => {
    const segments = [{ id: "s1", execution_mode: "external_agent", message_count: 1 }];
    const configOptions = [{ id: "c1", name: "Mode", type: "select", current_value: "auto" }];
    const prev = session("chat-1", [message("m1", "hi")], {
      segments: [{ id: "s1", execution_mode: "external_agent", message_count: 1 }],
      config_options: [{ id: "c1", name: "Mode", type: "select", current_value: "auto" }],
    });
    const next = session("chat-1", [message("m1", "hi")], {
      segments,
      config_options: configOptions,
    });

    const result = reconcileChatSession(prev, next);

    expect(result.segments).toBe(prev.segments);
    expect(result.config_options).toBe(prev.config_options);
  });

  it("takes next segments and config_options when they changed", () => {
    const prev = session("chat-1", [message("m1", "hi")], {
      segments: [{ id: "s1", execution_mode: "external_agent", message_count: 1 }],
      config_options: [{ id: "c1", name: "Mode", type: "select", current_value: "auto" }],
    });
    const next = session("chat-1", [message("m1", "hi")], {
      segments: [{ id: "s1", execution_mode: "external_agent", message_count: 2 }],
      config_options: [{ id: "c1", name: "Mode", type: "select", current_value: "plan" }],
    });

    const result = reconcileChatSession(prev, next);

    expect(result.segments).toBe(next.segments);
    expect(result.config_options).toBe(next.config_options);
  });
});
